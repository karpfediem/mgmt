// Mgmt
// Copyright (C) 2013-2024+ James Shubin and the project contributors
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

package corenet

import (
	"context"
	"net"
	"strconv"
	"strings"

	"github.com/purpleidea/mgmt/lang/funcs/simple"
	"github.com/purpleidea/mgmt/lang/types"
)

func init() {
	simple.ModuleRegister(ModuleName, "cidr_to_ip", &simple.Scaffold{
		T: types.NewType("func(a str) str"),
		F: CidrToIP,
	})
	simple.ModuleRegister(ModuleName, "cidr_to_prefix", &simple.Scaffold{
		T: types.NewType("func(a str) str"),
		F: CidrToPrefix,
	})
	simple.ModuleRegister(ModuleName, "cidr_to_mask", &simple.Scaffold{
		T: types.NewType("func(a str) str"),
		F: CidrToMask,
	})
}

// CidrToIP returns the IP from a CIDR address.
func CidrToIP(ctx context.Context, input []types.Value) (types.Value, error) {
	cidr := input[0].Str()
	ip, _, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return nil, err
	}
	return &types.StrValue{
		V: ip.String(),
	}, nil
}

// CidrToPrefix returns the prefix from a CIDR address. For example, if you give
// us 192.0.2.0/24 then we will return "24" as a string.
func CidrToPrefix(ctx context.Context, input []types.Value) (types.Value, error) {
	cidr := input[0].Str()
	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return nil, err
	}

	ones, _ := ipnet.Mask.Size()

	return &types.StrValue{
		V: strconv.Itoa(ones),
	}, nil
}

// CidrToMask returns the subnet mask from a CIDR address.
func CidrToMask(ctx context.Context, input []types.Value) (types.Value, error) {
	cidr := input[0].Str()
	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return nil, err
	}
	return &types.StrValue{
		V: net.IP(ipnet.Mask).String(),
	}, nil
}
