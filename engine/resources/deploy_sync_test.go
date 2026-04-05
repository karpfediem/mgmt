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
	"testing"

	"github.com/purpleidea/mgmt/engine"
	engineUtil "github.com/purpleidea/mgmt/engine/util"
)

func TestDeploySyncEncodeDecode(t *testing.T) {
	input, err := engine.NewNamedResource(KindDeploySync, "/tmp/dst/")
	if err != nil {
		t.Fatalf("can't create: %v", err)
	}
	res := input.(*DeploySync)
	res.Source = "/src/"
	res.Recurse = true
	res.Force = true
	res.Purge = true

	b64, err := engineUtil.ResToB64(input)
	if err != nil {
		t.Fatalf("can't encode: %v", err)
	}

	output, err := engineUtil.B64ToRes(b64)
	if err != nil {
		t.Fatalf("can't decode: %v", err)
	}

	res1, ok := input.(engine.Res)
	if !ok {
		t.Fatalf("input is not a Res")
	}
	res2, ok := output.(engine.Res)
	if !ok {
		t.Fatalf("output is not a Res")
	}
	if err := engine.ResCmp(res1, res2); err != nil {
		t.Fatalf("resources differ: %+v", err)
	}
}

func TestDeploySyncGraphQueryAllowed(t *testing.T) {
	res := &DeploySync{}
	res.SetKind(KindDeploySync)

	if err := res.GraphQueryAllowed(engine.GraphQueryableOptionKind(KindFile)); err != nil {
		t.Fatalf("file should be allowed: %v", err)
	}
	if err := res.GraphQueryAllowed(engine.GraphQueryableOptionKind(KindDeploySync)); err != nil {
		t.Fatalf("deploy:sync should be allowed: %v", err)
	}
	if err := res.GraphQueryAllowed(engine.GraphQueryableOptionKind("svc")); err == nil {
		t.Fatalf("svc should not be allowed")
	}
}
