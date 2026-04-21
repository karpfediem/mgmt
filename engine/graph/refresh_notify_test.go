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
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/resources"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/pgraph"
)

const (
	notifyTestSourceKind = "notifytestsource"
	notifyTestSinkKind   = "notifytestsink"
)

var (
	notifyTestSourceState sync.Map // map[string]string
	notifyTestSinkState   sync.Map // map[string]*notifyTestSinkCounters
)

func init() {
	engine.RegisterResource(notifyTestSourceKind, func() engine.Res { return &notifyTestSourceRes{} })
	engine.RegisterResource(notifyTestSinkKind, func() engine.Res { return &notifyTestSinkRes{} })
}

type notifyTestSourceRes struct {
	traits.Base

	init  *engine.Init
	Value string `lang:"value"`
}

func (obj *notifyTestSourceRes) Default() engine.Res {
	return &notifyTestSourceRes{}
}

func (obj *notifyTestSourceRes) Validate() error {
	return nil
}

func (obj *notifyTestSourceRes) Init(init *engine.Init) error {
	obj.init = init
	return nil
}

func (obj *notifyTestSourceRes) Cleanup() error {
	return nil
}

func (obj *notifyTestSourceRes) Watch(ctx context.Context) error {
	if err := obj.init.Event(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (obj *notifyTestSourceRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	current, _ := notifyTestSourceState.Load(obj.Name())
	if current == obj.Value {
		return true, nil
	}
	if !apply {
		return false, nil
	}
	notifyTestSourceState.Store(obj.Name(), obj.Value)
	return false, nil
}

func (obj *notifyTestSourceRes) Cmp(r engine.Res) error {
	res, ok := r.(*notifyTestSourceRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.Value != res.Value {
		return fmt.Errorf("the Value differs")
	}
	return nil
}

func (obj *notifyTestSourceRes) UIDs() []engine.ResUID {
	return []engine.ResUID{
		&engine.BaseUID{
			Name: obj.Name(),
			Kind: obj.Kind(),
		},
	}
}

type notifyTestSinkCounters struct {
	checks    atomic.Int32
	refreshes atomic.Int32
}

type notifyTestSinkRes struct {
	traits.Base
	traits.Refreshable

	init *engine.Init
}

func (obj *notifyTestSinkRes) Default() engine.Res {
	return &notifyTestSinkRes{}
}

func (obj *notifyTestSinkRes) Validate() error {
	return nil
}

func (obj *notifyTestSinkRes) Init(init *engine.Init) error {
	obj.init = init
	return nil
}

func (obj *notifyTestSinkRes) Cleanup() error {
	return nil
}

func (obj *notifyTestSinkRes) Watch(ctx context.Context) error {
	if err := obj.init.Event(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (obj *notifyTestSinkRes) counters() *notifyTestSinkCounters {
	value, _ := notifyTestSinkState.LoadOrStore(obj.Name(), &notifyTestSinkCounters{})
	return value.(*notifyTestSinkCounters)
}

func (obj *notifyTestSinkRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	counters := obj.counters()
	counters.checks.Add(1)
	if obj.init.Refresh() {
		counters.refreshes.Add(1)
	}
	return true, nil
}

func (obj *notifyTestSinkRes) Cmp(r engine.Res) error {
	_, ok := r.(*notifyTestSinkRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	return nil
}

func (obj *notifyTestSinkRes) UIDs() []engine.ResUID {
	return []engine.ResUID{
		&engine.BaseUID{
			Name: obj.Name(),
			Kind: obj.Kind(),
		},
	}
}

func newNotifyTestSourceRes(t *testing.T, name, value string) *notifyTestSourceRes {
	t.Helper()

	res, err := engine.NewNamedResource(notifyTestSourceKind, name)
	if err != nil {
		t.Fatalf("NewNamedResource(%q): %v", name, err)
	}

	out, ok := res.(*notifyTestSourceRes)
	if !ok {
		t.Fatalf("unexpected resource type: %T", res)
	}
	out.Value = value
	return out
}

func newNotifyTestSinkRes(t *testing.T, name string) *notifyTestSinkRes {
	t.Helper()

	res, err := engine.NewNamedResource(notifyTestSinkKind, name)
	if err != nil {
		t.Fatalf("NewNamedResource(%q): %v", name, err)
	}

	out, ok := res.(*notifyTestSinkRes)
	if !ok {
		t.Fatalf("unexpected resource type: %T", res)
	}
	return out
}

func resetNotifyTestState() {
	notifyTestSourceState = sync.Map{}
	notifyTestSinkState = sync.Map{}
}

func notifySinkRefreshCount(name string) int32 {
	value, ok := notifyTestSinkState.Load(name)
	if !ok {
		return 0
	}
	return value.(*notifyTestSinkCounters).refreshes.Load()
}

func notifySourceValue(name string) string {
	value, ok := notifyTestSourceState.Load(name)
	if !ok {
		return ""
	}
	return value.(string)
}

func TestRepeatedNotifyRefreshAfterGraphSwap(t *testing.T) {
	resetNotifyTestState()

	ge := newTestEngine(t)

	buildGraph := func(graphName, sourceValue string) *pgraph.Graph {
		t.Helper()

		g, err := pgraph.NewGraph(graphName)
		if err != nil {
			t.Fatalf("NewGraph(%s): %v", graphName, err)
		}
		source := newNotifyTestSourceRes(t, "source", sourceValue)
		sink := newNotifyTestSinkRes(t, "sink")
		g.AddVertex(source)
		g.AddEdge(source, sink, &engine.Edge{Notify: true})
		return g
	}

	switchGraph(t, ge, buildGraph("g1", "alpha"))
	waitFor(t, time.Second, func() bool {
		return notifySinkRefreshCount("sink") >= 1
	}, "initial sink refresh")

	switchGraph(t, ge, buildGraph("g2", "beta"))
	waitFor(t, time.Second, func() bool {
		return notifySinkRefreshCount("sink") >= 2
	}, "second sink refresh")

	switchGraph(t, ge, buildGraph("g3", "gamma"))
	waitFor(t, time.Second, func() bool {
		return notifySinkRefreshCount("sink") >= 3
	}, "third sink refresh")
}

func TestRepeatedFileNotifyRefreshAfterGraphSwap(t *testing.T) {
	resetNotifyTestState()

	ge := newTestEngine(t)
	filePath := filepath.Join(t.TempDir(), "env")

	buildGraph := func(graphName, content string) *pgraph.Graph {
		t.Helper()

		g, err := pgraph.NewGraph(graphName)
		if err != nil {
			t.Fatalf("NewGraph(%s): %v", graphName, err)
		}

		res, err := engine.NewNamedResource(resources.KindFile, filePath)
		if err != nil {
			t.Fatalf("NewNamedResource(%q): %v", filePath, err)
		}

		fileRes, ok := res.(*resources.FileRes)
		if !ok {
			t.Fatalf("unexpected file resource type: %T", res)
		}

		fileRes.State = resources.FileStateExists
		fileRes.Content = &content

		sink := newNotifyTestSinkRes(t, "sink")
		g.AddVertex(fileRes)
		g.AddEdge(fileRes, sink, &engine.Edge{Notify: true})
		return g
	}

	switchGraph(t, ge, buildGraph("file-g1", "alpha\n"))
	waitFor(t, 3*time.Second, func() bool {
		return notifySinkRefreshCount("sink") >= 1
	}, "initial file sink refresh")

	switchGraph(t, ge, buildGraph("file-g2", "beta\n"))
	waitFor(t, 3*time.Second, func() bool {
		return notifySinkRefreshCount("sink") >= 2
	}, "second file sink refresh")

	switchGraph(t, ge, buildGraph("file-g3", "gamma\n"))
	waitFor(t, 3*time.Second, func() bool {
		return notifySinkRefreshCount("sink") >= 3
	}, "third file sink refresh")
}

func TestRepeatedNotifyRefreshWithMultipleIncomingEdgesAfterGraphSwap(t *testing.T) {
	resetNotifyTestState()

	ge := newTestEngine(t)

	buildGraph := func(graphName, changingValue string) *pgraph.Graph {
		t.Helper()

		g, err := pgraph.NewGraph(graphName)
		if err != nil {
			t.Fatalf("NewGraph(%s): %v", graphName, err)
		}

		changing := newNotifyTestSourceRes(t, "changing", changingValue)
		static := newNotifyTestSourceRes(t, "static", "constant")
		sink := newNotifyTestSinkRes(t, "sink")
		g.AddVertex(changing)
		g.AddVertex(static)
		g.AddEdge(changing, sink, &engine.Edge{Notify: true})
		g.AddEdge(static, sink, &engine.Edge{Notify: true})
		return g
	}

	switchGraph(t, ge, buildGraph("multi-g1", "alpha"))
	waitFor(t, time.Second, func() bool {
		return notifySinkRefreshCount("sink") >= 1
	}, "initial sink refresh")

	switchGraph(t, ge, buildGraph("multi-g2", "beta"))
	waitFor(t, time.Second, func() bool {
		return notifySourceValue("changing") == "beta"
	}, "changing source apply")
	waitFor(t, time.Second, func() bool {
		return notifySinkRefreshCount("sink") >= 2
	}, "second sink refresh from changing input")

	switchGraph(t, ge, buildGraph("multi-g3", "gamma"))
	waitFor(t, time.Second, func() bool {
		return notifySinkRefreshCount("sink") >= 3
	}, "third sink refresh from changing input")
}
