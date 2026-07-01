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

package coreworld

import (
	"context"
	"fmt"

	"github.com/purpleidea/mgmt/lang/funcs"
	"github.com/purpleidea/mgmt/lang/interfaces"
	"github.com/purpleidea/mgmt/lang/types"
	"github.com/purpleidea/mgmt/util/errwrap"
)

const (
	// ListFuncName is the name this function is registered as.
	ListFuncName = "list"

	// arg names...
	listArgNamePrefix = "prefix"
)

func init() {
	funcs.ModuleRegister(ModuleName, ListFuncName, func() interfaces.Func { return &ListFunc{} })
}

var _ interfaces.StreamableFunc = &ListFunc{}

// ListFunc returns all world string keys with the given prefix.
type ListFunc struct {
	interfaces.Textarea

	init *interfaces.Init

	input  chan string // stream of inputs
	prefix *string     // the active prefix
}

// String returns a simple name for this function. This is needed so this struct
// can satisfy the pgraph.Vertex interface.
func (obj *ListFunc) String() string {
	return ListFuncName
}

// ArgGen returns the Nth arg name for this function.
func (obj *ListFunc) ArgGen(index int) (string, error) {
	seq := []string{listArgNamePrefix}
	if l := len(seq); index >= l {
		return "", fmt.Errorf("index %d exceeds arg length of %d", index, l)
	}
	return seq[index], nil
}

// Validate makes sure we've built our struct properly. It is usually unused for
// normal functions that users can use directly.
func (obj *ListFunc) Validate() error {
	return nil
}

// Info returns some static info about itself.
func (obj *ListFunc) Info() *interfaces.Info {
	return &interfaces.Info{
		Pure: false, // definitely false
		Memo: false,
		Fast: false,
		Spec: false,
		Sig:  types.NewType(fmt.Sprintf("func(%s str) []str", listArgNamePrefix)),
		Err:  obj.Validate(),
	}
}

// Init runs some startup code for this function.
func (obj *ListFunc) Init(init *interfaces.Init) error {
	obj.init = init
	obj.input = make(chan string)
	return nil
}

// Stream returns the changing values that this func has over time.
func (obj *ListFunc) Stream(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // important so that we cleanup the watch when exiting

	watchChan := make(chan error) // XXX: sender should close this, but did I implement that part yet???

	for {
		select {
		case prefix, ok := <-obj.input:
			if !ok {
				obj.input = nil // don't infinite loop back
				return fmt.Errorf("unexpected close")
			}

			if obj.prefix != nil && *obj.prefix == prefix {
				continue // nothing changed
			}

			// TODO: support changing the prefix over time...
			if obj.prefix == nil {
				obj.prefix = &prefix // store
				var err error
				watchChan, err = obj.init.World.StrListWatch(ctx, prefix)
				if err != nil {
					return err
				}
				continue // we get values on the watch chan, not here!
			}

			if *obj.prefix == prefix {
				continue // skip duplicates
			}

			// *obj.prefix != prefix
			return fmt.Errorf("can't change prefix, previously: `%s`", *obj.prefix)

		case err, ok := <-watchChan:
			if !ok { // closed
				// XXX: if we close, perhaps the engine is
				// switching etcd hosts and we should retry?
				// maybe instead we should get an "etcd
				// reconnect" signal, and the lang will restart?
				return nil
			}
			if err != nil {
				return errwrap.Wrapf(err, "channel watch failed on `%s`", *obj.prefix)
			}

			if err := obj.init.Event(ctx); err != nil { // send event
				return err
			}

		case <-ctx.Done():
			return nil
		}
	}
}

// Call this function with the input args and return the current key list.
func (obj *ListFunc) Call(ctx context.Context, args []types.Value) (types.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("not enough args")
	}
	prefix := args[0].Str()
	if prefix == "" {
		return nil, fmt.Errorf("can't use an empty prefix")
	}

	// Check before we send to a chan where we'd need Stream to be running.
	if obj.init == nil {
		return nil, funcs.ErrCantSpeculate
	}

	if obj.init.Debug {
		obj.init.Logf("prefix: %s", prefix)
	}

	select {
	case obj.input <- prefix:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	keys, err := obj.init.World.StrList(ctx, prefix)
	if err != nil {
		return nil, errwrap.Wrapf(err, "channel list failed on `%s`", prefix)
	}

	return types.ListStrToValue(keys), nil
}
