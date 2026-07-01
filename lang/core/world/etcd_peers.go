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
	"strconv"
	"sync"

	"github.com/purpleidea/mgmt/engine"
	etcdinterfaces "github.com/purpleidea/mgmt/etcd/interfaces"
	etcdUtil "github.com/purpleidea/mgmt/etcd/util"
	"github.com/purpleidea/mgmt/lang/funcs"
	"github.com/purpleidea/mgmt/lang/interfaces"
	"github.com/purpleidea/mgmt/lang/types"
	"github.com/purpleidea/mgmt/util/errwrap"
)

const (
	// EtcdPeersFuncName is the name this function is registered as.
	EtcdPeersFuncName = "etcd_peers"

	// struct field names...
	etcdPeersFieldID         = "id"
	etcdPeersFieldName       = "name"
	etcdPeersFieldPeerURLs   = "peer_urls"
	etcdPeersFieldClientURLs = "client_urls"
	etcdPeersFieldLearner    = "learner"

	// etcdPeersStruct is the struct type returned for each peer.
	etcdPeersStruct = "struct{" +
		etcdPeersFieldID + " str; " +
		etcdPeersFieldName + " str; " +
		etcdPeersFieldPeerURLs + " []str; " +
		etcdPeersFieldClientURLs + " []str; " +
		etcdPeersFieldLearner + " bool}"

	// etcdPeersType is the expected return type.
	etcdPeersType = "[]" + etcdPeersStruct
)

func init() {
	funcs.ModuleRegister(ModuleName, EtcdPeersFuncName, func() interfaces.Func { return &EtcdPeersFunc{} })
}

var _ interfaces.StreamableFunc = &EtcdPeersFunc{}

// EtcdPeersFunc returns the current etcd peers in the active world.
type EtcdPeersFunc struct {
	interfaces.Textarea

	init  *interfaces.Init
	world engine.EtcdWorld

	mutex *sync.Mutex // guards value
	value []*etcdinterfaces.Member
}

// String returns a simple name for this function. This is needed so this struct
// can satisfy the pgraph.Vertex interface.
func (obj *EtcdPeersFunc) String() string {
	return EtcdPeersFuncName
}

// ArgGen returns the Nth arg name for this function.
func (obj *EtcdPeersFunc) ArgGen(index int) (string, error) {
	return "", fmt.Errorf("index %d exceeds arg length of 0", index)
}

// helper
func (obj *EtcdPeersFunc) sig() *types.Type {
	return types.NewType(fmt.Sprintf("func() %s", etcdPeersType))
}

// Validate tells us if the input struct takes a valid form.
func (obj *EtcdPeersFunc) Validate() error {
	return nil
}

// Info returns some static info about itself.
func (obj *EtcdPeersFunc) Info() *interfaces.Info {
	return &interfaces.Info{
		Pure: false, // definitely false
		Memo: false,
		Fast: false,
		Spec: false,
		Sig:  obj.sig(),
		Err:  obj.Validate(),
	}
}

// Init runs some startup code for this function.
func (obj *EtcdPeersFunc) Init(init *interfaces.Init) error {
	obj.init = init
	world, ok := obj.init.World.(engine.EtcdWorld)
	if !ok {
		return fmt.Errorf("world backend does not support the EtcdWorld interface")
	}
	obj.world = world

	obj.mutex = &sync.Mutex{}
	obj.value = []*etcdinterfaces.Member{} // empty
	return nil
}

// Stream returns the changing values that this func has over time.
func (obj *EtcdPeersFunc) Stream(ctx context.Context) error {
	watchChan, err := obj.world.WatchMembers(ctx)
	if err != nil {
		return err
	}

	for {
		select {
		case result, ok := <-watchChan:
			if !ok {
				return nil
			}
			if result == nil {
				return fmt.Errorf("unexpected nil members result")
			}
			if err := result.Err; err != nil {
				return errwrap.Wrapf(err, "members result error")
			}

			obj.mutex.Lock()
			obj.value = result.Members
			obj.mutex.Unlock()

			if err := obj.init.Event(ctx); err != nil {
				return err
			}

		case <-ctx.Done():
			return nil
		}
	}
}

// Call this function with the input args and return the value if it is possible
// to do so at this time.
func (obj *EtcdPeersFunc) Call(ctx context.Context, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("unexpected args")
	}
	if obj.init == nil {
		return nil, funcs.ErrCantSpeculate
	}

	obj.mutex.Lock()
	value := obj.value
	obj.mutex.Unlock()

	return etcdPeersValue(obj.Info().Sig.Out, value)
}

func etcdPeersValue(out *types.Type, members []*etcdinterfaces.Member) (types.Value, error) {
	list := types.NewList(out)
	for _, member := range members {
		if member == nil {
			continue
		}

		st, err := etcdPeerValue(member)
		if err != nil {
			return nil, err
		}
		if err := list.Add(st); err != nil {
			return nil, err
		}
	}
	return list, nil
}

func etcdPeerValue(member *etcdinterfaces.Member) (types.Value, error) {
	st := types.NewStruct(types.NewType(etcdPeersStruct))

	fields := map[string]types.Value{
		etcdPeersFieldID:         &types.StrValue{V: strconv.FormatUint(member.ID, 10)},
		etcdPeersFieldName:       &types.StrValue{V: member.Name},
		etcdPeersFieldPeerURLs:   stringListValue(etcdUtil.FromURLsToStringList(member.PeerURLs)),
		etcdPeersFieldClientURLs: stringListValue(etcdUtil.FromURLsToStringList(member.ClientURLs)),
		etcdPeersFieldLearner:    &types.BoolValue{V: member.IsLearner},
	}

	for name, value := range fields {
		if err := st.Set(name, value); err != nil {
			return nil, errwrap.Wrapf(err, "struct could not add field `%s`, val: `%s`", name, value)
		}
	}
	return st, nil
}

func stringListValue(values []string) types.Value {
	list := types.NewList(types.NewType("[]str"))
	for _, value := range values {
		// This cannot fail with a []str list and a str value.
		_ = list.Add(&types.StrValue{V: value})
	}
	return list
}
