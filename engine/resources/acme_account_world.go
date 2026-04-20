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

package resources

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

type acmeAccountWorldAPI interface {
	StrIsNotExist(error) bool
	StrWatch(context.Context, string) (chan error, error)
	StrGet(context.Context, string) (string, error)
	StrSet(context.Context, string, string) error
	StrDel(context.Context, string) error
}

func acmeAccountStateKey(name string) string {
	return "acme/account/" + url.QueryEscape(strings.TrimSpace(name))
}

func loadAcmeAccountSharedState(ctx context.Context, world acmeAccountWorldAPI, name string) (*acmeAccountStoredState, error) {
	key := acmeAccountStateKey(name)

	value, err := world.StrGet(ctx, key)
	if err != nil {
		if world.StrIsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	state := &acmeAccountStoredState{}
	if err := json.Unmarshal([]byte(value), state); err != nil {
		return nil, err
	}
	state.normalize()
	return state, nil
}

func storeAcmeAccountSharedState(ctx context.Context, world acmeAccountWorldAPI, name string, state *acmeAccountStoredState) error {
	key := acmeAccountStateKey(name)
	if state == nil || !state.ready() {
		return world.StrDel(ctx, key)
	}

	clone := state.clone()
	clone.normalize()
	payload, err := json.Marshal(clone)
	if err != nil {
		return err
	}
	return world.StrSet(ctx, key, string(payload))
}
