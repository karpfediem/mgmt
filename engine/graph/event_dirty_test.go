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
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/resources"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/pgraph"
)

const queuedEventSourceKind = "queueeventsource"

func init() {
	engine.RegisterResource(queuedEventSourceKind, func() engine.Res { return (&queuedEventSourceRes{}).Default() })
}

type queuedEventSourceSends struct {
	Content string `lang:"content"`
}

type queuedEventSourceRes struct {
	traits.Base
	traits.Sendable

	init *engine.Init

	mu             sync.Mutex
	stage          int
	extraEvents    chan struct{}
	firstStartedCh chan struct{}
	releaseFirstCh chan struct{}
}

func (obj *queuedEventSourceRes) Default() engine.Res {
	return &queuedEventSourceRes{
		extraEvents:    make(chan struct{}, 16),
		firstStartedCh: make(chan struct{}),
		releaseFirstCh: make(chan struct{}),
	}
}

func (obj *queuedEventSourceRes) Validate() error { return nil }

func (obj *queuedEventSourceRes) Init(init *engine.Init) error {
	obj.init = init
	if obj.extraEvents == nil {
		obj.extraEvents = make(chan struct{}, 16)
	}
	if obj.firstStartedCh == nil {
		obj.firstStartedCh = make(chan struct{})
	}
	if obj.releaseFirstCh == nil {
		obj.releaseFirstCh = make(chan struct{})
	}
	return nil
}

func (obj *queuedEventSourceRes) Cleanup() error { return nil }

func (obj *queuedEventSourceRes) Watch(ctx context.Context) error {
	if err := obj.init.Event(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-obj.extraEvents:
			if err := obj.init.Event(ctx); err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (obj *queuedEventSourceRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	obj.mu.Lock()
	stage := obj.stage
	obj.mu.Unlock()

	switch stage {
	case 0:
		if err := obj.init.Send(&queuedEventSourceSends{Content: "staging\n"}); err != nil {
			return false, err
		}
		close(obj.firstStartedCh)
		select {
		case <-obj.releaseFirstCh:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		obj.mu.Lock()
		obj.stage = 1
		obj.mu.Unlock()
		return true, nil

	default:
		if err := obj.init.Send(&queuedEventSourceSends{Content: "prod\n"}); err != nil {
			return false, err
		}
		obj.mu.Lock()
		obj.stage = stage + 1
		obj.mu.Unlock()
		return true, nil
	}
}

func (obj *queuedEventSourceRes) Cmp(r engine.Res) error {
	_, ok := r.(*queuedEventSourceRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	return nil
}

func (obj *queuedEventSourceRes) UIDs() []engine.ResUID {
	return []engine.ResUID{
		&engine.BaseUID{
			Name: obj.Name(),
			Kind: obj.Kind(),
		},
	}
}

func (obj *queuedEventSourceRes) Sends() interface{} {
	return &queuedEventSourceSends{}
}

func (obj *queuedEventSourceRes) trigger() {
	select {
	case obj.extraEvents <- struct{}{}:
	default:
	}
}

func newQueuedEventSourceRes(t *testing.T, name string) *queuedEventSourceRes {
	t.Helper()

	res, err := engine.NewNamedResource(queuedEventSourceKind, name)
	if err != nil {
		t.Fatalf("NewNamedResource(%q): %v", name, err)
	}

	out, ok := res.(*queuedEventSourceRes)
	if !ok {
		t.Fatalf("unexpected resource type: %T", res)
	}
	return out
}

func TestQueuedWatchEventStaysDirtyUntilProcessed(t *testing.T) {
	ge := newTestEngine(t)

	filePath := filepath.Join(t.TempDir(), "certificate.pem")
	if err := os.WriteFile(filePath, []byte("staging\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	g, err := pgraph.NewGraph("queued-watch-event")
	if err != nil {
		t.Fatalf("NewGraph(queued-watch-event): %v", err)
	}

	source := newQueuedEventSourceRes(t, "source")
	res, err := engine.NewNamedResource(resources.KindFile, filePath)
	if err != nil {
		t.Fatalf("NewNamedResource(%q): %v", filePath, err)
	}
	fileRes, ok := res.(*resources.FileRes)
	if !ok {
		t.Fatalf("unexpected file resource type: %T", res)
	}
	fileRes.State = resources.FileStateExists
	fileRes.SetRecv(map[string]*engine.Send{
		"content": {
			Res: source,
			Key: "content",
		},
	})

	g.AddVertex(source)
	g.AddVertex(fileRes)

	switchGraph(t, ge, g)

	select {
	case <-source.firstStartedCh:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for first CheckApply to start")
	}

	source.trigger()
	close(source.releaseFirstCh)

	waitFor(t, 3*time.Second, func() bool {
		data, err := os.ReadFile(filePath)
		return err == nil && string(data) == "prod\n"
	}, "file content after queued watch event")
}
