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

package resources

import (
	"context"
	"fmt"
	"sync"

	"github.com/purpleidea/mgmt/engine"
	etcdinterfaces "github.com/purpleidea/mgmt/etcd/interfaces"
)

type fakeWorld struct {
	hostname string

	mu sync.Mutex

	values    map[string]string
	mapValues map[string]map[string]string

	strWatchers    map[string][]chan error
	strMapWatchers map[string][]chan error
}

func newFakeWorld(hostname string) *fakeWorld {
	return &fakeWorld{
		hostname:       hostname,
		values:         map[string]string{},
		mapValues:      map[string]map[string]string{},
		strWatchers:    map[string][]chan error{},
		strMapWatchers: map[string][]chan error{},
	}
}

func (obj *fakeWorld) Connect(ctx context.Context, init *engine.WorldInit) error {
	_ = ctx
	if init != nil && init.Hostname != "" {
		obj.hostname = init.Hostname
	}
	return nil
}

func (obj *fakeWorld) Cleanup() error {
	return nil
}

func (obj *fakeWorld) URI() string {
	return "fake://world"
}

func (obj *fakeWorld) Fs(ctx context.Context, uri string) (engine.Fs, error) {
	_ = ctx
	_ = uri
	return nil, fmt.Errorf("not implemented")
}

func (obj *fakeWorld) WatchDeploy(context.Context) (chan error, error) {
	ch := make(chan error)
	close(ch)
	return ch, nil
}

func (obj *fakeWorld) GetDeploy(context.Context, uint64) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (obj *fakeWorld) GetMaxDeployID(context.Context) (uint64, error) {
	return 0, fmt.Errorf("not implemented")
}

func (obj *fakeWorld) AddDeploy(context.Context, uint64, string, string, *string) error {
	return fmt.Errorf("not implemented")
}

func (obj *fakeWorld) StrWatch(ctx context.Context, namespace string) (chan error, error) {
	obj.mu.Lock()
	defer obj.mu.Unlock()

	ch := make(chan error, 16)
	obj.strWatchers[namespace] = append(obj.strWatchers[namespace], ch)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func (obj *fakeWorld) StrIsNotExist(err error) bool {
	return err == etcdinterfaces.ErrNotExist
}

func (obj *fakeWorld) StrGet(ctx context.Context, namespace string) (string, error) {
	_ = ctx
	obj.mu.Lock()
	defer obj.mu.Unlock()

	value, exists := obj.values[namespace]
	if !exists {
		return "", etcdinterfaces.ErrNotExist
	}
	return value, nil
}

func (obj *fakeWorld) StrSet(ctx context.Context, namespace, value string) error {
	_ = ctx
	obj.mu.Lock()
	changed := obj.values[namespace] != value
	obj.values[namespace] = value
	watchers := append([]chan error(nil), obj.strWatchers[namespace]...)
	obj.mu.Unlock()

	if changed {
		for _, ch := range watchers {
			select {
			case ch <- nil:
			default:
			}
		}
	}
	return nil
}

func (obj *fakeWorld) StrDel(ctx context.Context, namespace string) error {
	_ = ctx
	obj.mu.Lock()
	_, exists := obj.values[namespace]
	delete(obj.values, namespace)
	watchers := append([]chan error(nil), obj.strWatchers[namespace]...)
	obj.mu.Unlock()

	if exists {
		for _, ch := range watchers {
			select {
			case ch <- nil:
			default:
			}
		}
	}
	return nil
}

func (obj *fakeWorld) StrMapWatch(ctx context.Context, namespace string) (chan error, error) {
	obj.mu.Lock()
	defer obj.mu.Unlock()

	ch := make(chan error, 16)
	obj.strMapWatchers[namespace] = append(obj.strMapWatchers[namespace], ch)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func (obj *fakeWorld) StrMapGet(ctx context.Context, namespace string) (map[string]string, error) {
	_ = ctx
	obj.mu.Lock()
	defer obj.mu.Unlock()

	result := map[string]string{}
	for host, value := range obj.mapValues[namespace] {
		result[host] = value
	}
	return result, nil
}

func (obj *fakeWorld) StrMapSet(ctx context.Context, namespace, value string) error {
	_ = ctx
	obj.mu.Lock()
	if obj.mapValues[namespace] == nil {
		obj.mapValues[namespace] = map[string]string{}
	}
	changed := obj.mapValues[namespace][obj.hostname] != value
	obj.mapValues[namespace][obj.hostname] = value
	watchers := append([]chan error(nil), obj.strMapWatchers[namespace]...)
	obj.mu.Unlock()

	if changed {
		for _, ch := range watchers {
			select {
			case ch <- nil:
			default:
			}
		}
	}
	return nil
}

func (obj *fakeWorld) StrMapDel(ctx context.Context, namespace string) error {
	_ = ctx
	obj.mu.Lock()
	_, exists := obj.mapValues[namespace][obj.hostname]
	delete(obj.mapValues[namespace], obj.hostname)
	if len(obj.mapValues[namespace]) == 0 {
		delete(obj.mapValues, namespace)
	}
	watchers := append([]chan error(nil), obj.strMapWatchers[namespace]...)
	obj.mu.Unlock()

	if exists {
		for _, ch := range watchers {
			select {
			case ch <- nil:
			default:
			}
		}
	}
	return nil
}

func (obj *fakeWorld) ResWatch(context.Context, string) (chan error, error) {
	ch := make(chan error)
	close(ch)
	return ch, nil
}

func (obj *fakeWorld) ResCollect(context.Context, []*engine.ResFilter) ([]*engine.ResOutput, error) {
	return nil, nil
}

func (obj *fakeWorld) ResExport(context.Context, []*engine.ResExport) (bool, error) {
	return true, nil
}

func (obj *fakeWorld) ResDelete(context.Context, []*engine.ResDelete) (bool, error) {
	return true, nil
}
