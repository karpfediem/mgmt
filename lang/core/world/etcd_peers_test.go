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
	"testing"

	etcdinterfaces "github.com/purpleidea/mgmt/etcd/interfaces"
	"github.com/purpleidea/mgmt/lang/types"
	etcdtypes "go.etcd.io/etcd/client/pkg/v3/types"
)

func TestEtcdPeersValueIncludesLearnerState(t *testing.T) {
	peerURLs, err := etcdtypes.NewURLs([]string{"http://host-a:2380"})
	if err != nil {
		t.Fatalf("could not parse peer URLs: %v", err)
	}
	clientURLs, err := etcdtypes.NewURLs([]string{"http://host-a:2379"})
	if err != nil {
		t.Fatalf("could not parse client URLs: %v", err)
	}

	value, err := etcdPeersValue((&EtcdPeersFunc{}).sig().Out, []*etcdinterfaces.Member{
		{
			ID:         42,
			Name:       "host-a",
			IsLearner:  true,
			PeerURLs:   peerURLs,
			ClientURLs: clientURLs,
		},
	})
	if err != nil {
		t.Fatalf("could not build peers value: %v", err)
	}

	list := value.(*types.ListValue)
	if list.Len() != 1 {
		t.Fatalf("expected one peer, got %d", list.Len())
	}
	rawPeer, exists := list.Lookup(0)
	if !exists {
		t.Fatalf("peer 0 is missing")
	}
	peer := rawPeer.(*types.StructValue)

	assertField := func(name string, want types.Value) {
		t.Helper()
		got, exists := peer.Lookup(name)
		if !exists {
			t.Fatalf("field %s is missing", name)
		}
		if err := got.Cmp(want); err != nil {
			t.Fatalf("field %s did not match: %v", name, err)
		}
	}

	assertField(etcdPeersFieldID, &types.StrValue{V: "42"})
	assertField(etcdPeersFieldName, &types.StrValue{V: "host-a"})
	assertField(etcdPeersFieldPeerURLs, stringListValue([]string{"http://host-a:2380"}))
	assertField(etcdPeersFieldClientURLs, stringListValue([]string{"http://host-a:2379"}))
	assertField(etcdPeersFieldLearner, &types.BoolValue{V: true})
}
