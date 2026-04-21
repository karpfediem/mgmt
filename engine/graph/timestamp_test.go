// Mgmt
// Copyright (C) James Shubin and the project contributors
// Written by James Shubin <james@shubin.ca> and the project contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// Additional permission under GNU GPL version 3 section 7
//
// If you modify this program, or any covered work, by linking or combining it
// with embedded mcl code and modules (and that the embedded mcl code and
// modules which link with this program, contain a copy of their source code in
// the authoritative form) containing parts covered by the terms of any other
// license, the licensors of this program grant you additional permission to
// convey the resulting work. Furthermore, the licensors of this program grant
// the original author, James Shubin, additional permission to update this
// additional permission if he deems it necessary to achieve the goals of this
// additional permission.

//go:build !root

package graph

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/purpleidea/mgmt/converger"
	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/local"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/pgraph"
)

const timestampTestKind = "timestamptest"

func init() {
	engine.RegisterResource(timestampTestKind, func() engine.Res { return &timestampTestRes{} })
}

type timestampTestRes struct {
	traits.Base

	init   *engine.Init
	checks atomic.Int32
}

func (obj *timestampTestRes) Default() engine.Res {
	return &timestampTestRes{}
}

func (obj *timestampTestRes) Validate() error {
	return nil
}

func (obj *timestampTestRes) Init(init *engine.Init) error {
	obj.init = init
	return nil
}

func (obj *timestampTestRes) Cleanup() error {
	return nil
}

func (obj *timestampTestRes) Watch(ctx context.Context) error {
	if err := obj.init.Event(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (obj *timestampTestRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	obj.checks.Add(1)
	return true, nil
}

func (obj *timestampTestRes) Cmp(r engine.Res) error {
	_, ok := r.(*timestampTestRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	return nil
}

func (obj *timestampTestRes) UIDs() []engine.ResUID {
	return []engine.ResUID{
		&engine.BaseUID{
			Name: obj.Name(),
			Kind: obj.Kind(),
		},
	}
}

func newTimestampTestRes(t *testing.T, name string) *timestampTestRes {
	t.Helper()

	res, err := engine.NewNamedResource(timestampTestKind, name)
	if err != nil {
		t.Fatalf("NewNamedResource(%q): %v", name, err)
	}

	out, ok := res.(*timestampTestRes)
	if !ok {
		t.Fatalf("unexpected resource type: %T", res)
	}
	return out
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()

	coord := converger.New(-1)
	go coord.Run(false)
	coord.Ready()

	ge := &Engine{
		Program:   "graph-test",
		Hostname:  "graph-test-host",
		Converger: coord,
		Local: (&local.API{
			Prefix: t.TempDir(),
			Debug:  false,
			Logf:   t.Logf,
		}).Init(),
		Prefix: t.TempDir(),
		Debug:  false,
		Logf:   t.Logf,
	}
	if err := ge.Init(); err != nil {
		t.Fatalf("engine init failed: %v", err)
	}

	t.Cleanup(func() {
		if !ge.paused {
			if err := ge.Pause(true); err != nil {
				t.Fatalf("engine pause failed during cleanup: %v", err)
			}
		}
		if err := ge.Shutdown(); err != nil {
			t.Fatalf("engine shutdown failed: %v", err)
		}
		coord.Shutdown()
	})

	return ge
}

func switchGraph(t *testing.T, ge *Engine, graph *pgraph.Graph) {
	t.Helper()

	if err := ge.Pause(false); err != nil {
		t.Fatalf("engine pause failed: %v", err)
	}
	if err := ge.Load(graph); err != nil {
		t.Fatalf("engine load failed: %v", err)
	}
	if err := ge.Validate(); err != nil {
		t.Fatalf("engine validate failed: %v", err)
	}
	if err := ge.Commit(); err != nil {
		t.Fatalf("engine commit failed: %v", err)
	}
	if err := ge.Resume(); err != nil {
		t.Fatalf("engine resume failed: %v", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool, msg string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

func TestBootstrapTimestampOnSuccessfulNoopSwap(t *testing.T) {
	ge := newTestEngine(t)

	g1, err := pgraph.NewGraph("g1")
	if err != nil {
		t.Fatalf("NewGraph(g1): %v", err)
	}
	up1 := newTimestampTestRes(t, "upstream")
	g1.AddVertex(up1)

	switchGraph(t, ge, g1)

	waitFor(t, time.Second, func() bool {
		return up1.checks.Load() > 0
	}, "upstream initial CheckApply")

	stateUp1, exists := ge.state[up1]
	if !exists {
		t.Fatalf("missing state for upstream")
	}
	stateUp1.mutex.RLock()
	up1Timestamp := stateUp1.timestamp
	stateUp1.mutex.RUnlock()
	if up1Timestamp == 0 {
		t.Fatalf("expected upstream timestamp to bootstrap on successful no-op run")
	}

	g2, err := pgraph.NewGraph("g2")
	if err != nil {
		t.Fatalf("NewGraph(g2): %v", err)
	}
	up2 := newTimestampTestRes(t, "upstream")
	down2 := newTimestampTestRes(t, "downstream")
	g2.AddVertex(up2)
	g2.AddEdge(up2, down2, &engine.Edge{})

	switchGraph(t, ge, g2)

	waitFor(t, time.Second, func() bool {
		return down2.checks.Load() > 0
	}, "downstream CheckApply after graph swap")
}
