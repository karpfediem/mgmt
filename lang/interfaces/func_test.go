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

package interfaces

import (
	"testing"

	"github.com/purpleidea/mgmt/pgraph"
)

type testVertex string

func (obj testVertex) String() string { return string(obj) }

func TestMergeFuncEdges(t *testing.T) {
	edge := MergeFuncEdges(
		&FuncEdge{Args: []string{"arg0", "arg1"}},
		&FuncEdge{Args: []string{"arg1", "arg2"}},
	)

	expected := []string{"arg0", "arg1", "arg2"}
	if len(edge.Args) != len(expected) {
		t.Fatalf("got %d args, expected %d", len(edge.Args), len(expected))
	}
	for i, arg := range expected {
		if edge.Args[i] != arg {
			t.Fatalf("got arg %q at index %d, expected %q", edge.Args[i], i, arg)
		}
	}
}

func TestAddFuncEdgeMergesExistingArgs(t *testing.T) {
	graph, err := pgraph.NewGraph("test")
	if err != nil {
		t.Fatalf("graph err: %+v", err)
	}

	v1 := testVertex("v1")
	v2 := testVertex("v2")

	AddFuncEdge(graph, v1, v2, &FuncEdge{Args: []string{"runtime_state_prefix"}})
	AddFuncEdge(graph, v1, v2, &FuncEdge{Args: []string{"runtime_state_root"}})

	edge := graph.FindEdge(v1, v2)
	if edge == nil {
		t.Fatal("expected merged edge")
	}
	funcEdge, ok := edge.(*FuncEdge)
	if !ok {
		t.Fatalf("expected FuncEdge, got %T", edge)
	}

	expected := []string{"runtime_state_prefix", "runtime_state_root"}
	if len(funcEdge.Args) != len(expected) {
		t.Fatalf("got %d args, expected %d", len(funcEdge.Args), len(expected))
	}
	for i, arg := range expected {
		if funcEdge.Args[i] != arg {
			t.Fatalf("got arg %q at index %d, expected %q", funcEdge.Args[i], i, arg)
		}
	}
}
