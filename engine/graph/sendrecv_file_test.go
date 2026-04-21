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

const sendRecvFileSourceKind = "sendrecvfiletest"

func init() {
	engine.RegisterResource(sendRecvFileSourceKind, func() engine.Res { return &sendRecvFileSourceRes{} })
}

type sendRecvFileSourceSends struct {
	Content string `lang:"content"`
}

type sendRecvFileSourceRes struct {
	traits.Base
	traits.Sendable

	init *engine.Init

	mu     sync.RWMutex
	value  string
	events chan struct{}
}

func (obj *sendRecvFileSourceRes) Default() engine.Res {
	return &sendRecvFileSourceRes{
		events: make(chan struct{}, 16),
	}
}

func (obj *sendRecvFileSourceRes) Validate() error {
	return nil
}

func (obj *sendRecvFileSourceRes) Init(init *engine.Init) error {
	obj.init = init
	if obj.events == nil {
		obj.events = make(chan struct{}, 16)
	}
	return nil
}

func (obj *sendRecvFileSourceRes) Cleanup() error {
	return nil
}

func (obj *sendRecvFileSourceRes) Watch(ctx context.Context) error {
	if err := obj.init.Event(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-obj.events:
			if err := obj.init.Event(ctx); err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (obj *sendRecvFileSourceRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	obj.mu.RLock()
	content := obj.value
	obj.mu.RUnlock()

	if err := obj.init.Send(&sendRecvFileSourceSends{Content: content}); err != nil {
		return false, err
	}
	return true, nil
}

func (obj *sendRecvFileSourceRes) Cmp(r engine.Res) error {
	_, ok := r.(*sendRecvFileSourceRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	return nil
}

func (obj *sendRecvFileSourceRes) UIDs() []engine.ResUID {
	return []engine.ResUID{
		&engine.BaseUID{
			Name: obj.Name(),
			Kind: obj.Kind(),
		},
	}
}

func (obj *sendRecvFileSourceRes) Sends() interface{} {
	return &sendRecvFileSourceSends{}
}

func (obj *sendRecvFileSourceRes) setValue(value string) {
	obj.mu.Lock()
	obj.value = value
	obj.mu.Unlock()
}

func (obj *sendRecvFileSourceRes) trigger() {
	select {
	case obj.events <- struct{}{}:
	default:
	}
}

func newSendRecvFileSourceRes(t *testing.T, name, value string) *sendRecvFileSourceRes {
	t.Helper()

	res, err := engine.NewNamedResource(sendRecvFileSourceKind, name)
	if err != nil {
		t.Fatalf("NewNamedResource(%q): %v", name, err)
	}

	out, ok := res.(*sendRecvFileSourceRes)
	if !ok {
		t.Fatalf("unexpected resource type: %T", res)
	}
	out.setValue(value)
	if err := out.Send(&sendRecvFileSourceSends{Content: value}); err != nil {
		t.Fatalf("seed send value: %v", err)
	}
	return out
}

func TestSendRecvFileRewritesAfterSourceUpdate(t *testing.T) {
	ge := newTestEngine(t)

	filePath := filepath.Join(t.TempDir(), "certificate.pem")

	g, err := pgraph.NewGraph("sendrecv-file")
	if err != nil {
		t.Fatalf("NewGraph(sendrecv-file): %v", err)
	}

	source := newSendRecvFileSourceRes(t, "source", "alpha\n")
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

	waitFor(t, 3*time.Second, func() bool {
		data, err := os.ReadFile(filePath)
		return err == nil && string(data) == "alpha\n"
	}, "initial file content from send/recv")

	source.setValue("beta\n")
	source.trigger()

	waitFor(t, 3*time.Second, func() bool {
		data, err := os.ReadFile(filePath)
		return err == nil && string(data) == "beta\n"
	}, "updated file content after sender change")
}

func TestSendRecvFileRewritesAfterSenderBootstrapsFromNil(t *testing.T) {
	ge := newTestEngine(t)

	filePath := filepath.Join(t.TempDir(), "certificate.pem")
	if err := os.WriteFile(filePath, []byte("staging\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	g, err := pgraph.NewGraph("sendrecv-file-nil-bootstrap")
	if err != nil {
		t.Fatalf("NewGraph(sendrecv-file-nil-bootstrap): %v", err)
	}

	source := newSendRecvFileSourceRes(t, "source", "staging\n")
	source.Sendable.ResetSendChanged()
	source.Sendable = traits.Sendable{}

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

	waitFor(t, 3*time.Second, func() bool {
		data, err := os.ReadFile(filePath)
		return err == nil && string(data) == "staging\n"
	}, "initial on-disk staging content")

	source.setValue("prod\n")
	source.trigger()

	waitFor(t, 3*time.Second, func() bool {
		data, err := os.ReadFile(filePath)
		return err == nil && string(data) == "prod\n"
	}, "updated file content after sender publishes from nil state")
}

func TestSendRecvFileRewritesAfterGraphSwap(t *testing.T) {
	ge := newTestEngine(t)

	filePath := filepath.Join(t.TempDir(), "certificate.pem")

	buildGraph := func(graphName, content string) *pgraph.Graph {
		t.Helper()

		g, err := pgraph.NewGraph(graphName)
		if err != nil {
			t.Fatalf("NewGraph(%s): %v", graphName, err)
		}

		source := newSendRecvFileSourceRes(t, "source", content)
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
		return g
	}

	switchGraph(t, ge, buildGraph("sendrecv-file-g1", "alpha\n"))
	waitFor(t, 3*time.Second, func() bool {
		data, err := os.ReadFile(filePath)
		return err == nil && string(data) == "alpha\n"
	}, "initial file content after graph load")

	switchGraph(t, ge, buildGraph("sendrecv-file-g2", "beta\n"))
	waitFor(t, 3*time.Second, func() bool {
		data, err := os.ReadFile(filePath)
		return err == nil && string(data) == "beta\n"
	}, "updated file content after graph swap")
}
