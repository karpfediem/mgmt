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

type acmeDNS01WorldAPI interface {
	StrIsNotExist(error) bool
	StrGet(context.Context, string) (string, error)
	StrSet(context.Context, string, string) error
	StrDel(context.Context, string) error
}

type acmeDNS01Challenge struct {
	Attempt          string `json:"attempt"`
	Domain           string `json:"domain"`
	Token            string `json:"token"`
	KeyAuthorization string `json:"key_authorization"`
	FQDN             string `json:"fqdn"`
	Value            string `json:"value"`
	ChallengeURL     string `json:"challenge_url"`
}

func (obj acmeDNS01Challenge) key() string {
	return strings.Join([]string{
		obj.Attempt,
		normalizeHTTPHost(obj.Domain),
		obj.Token,
	}, "\x00")
}

type acmeDNS01ChallengeState struct {
	Challenges map[string]acmeDNS01Challenge `json:"challenges"`
}

type acmeDNS01PresentationEntry struct {
	Attempt string `json:"attempt"`
	Domain  string `json:"domain"`
	FQDN    string `json:"fqdn"`
	Value   string `json:"value"`
	Ready   bool   `json:"ready"`
	Error   string `json:"error"`
}

type acmeDNS01PresentationState struct {
	Entries map[string]acmeDNS01PresentationEntry `json:"entries"`
}

func acmeDNS01ChallengeStateKey(solver string) string {
	return "acme/dns01/challenges/" + url.QueryEscape(strings.TrimSpace(solver))
}

func acmeDNS01PresentationStateKey(solver string) string {
	return "acme/dns01/presentation/" + url.QueryEscape(strings.TrimSpace(solver))
}

func loadAcmeDNS01ChallengeState(ctx context.Context, world acmeDNS01WorldAPI, solver string) (*acmeDNS01ChallengeState, error) {
	key := acmeDNS01ChallengeStateKey(solver)

	value, err := world.StrGet(ctx, key)
	if err != nil {
		if world.StrIsNotExist(err) {
			return &acmeDNS01ChallengeState{Challenges: map[string]acmeDNS01Challenge{}}, nil
		}
		return nil, err
	}

	state := &acmeDNS01ChallengeState{}
	if err := json.Unmarshal([]byte(value), state); err != nil {
		return nil, err
	}
	if state.Challenges == nil {
		state.Challenges = map[string]acmeDNS01Challenge{}
	}
	return state, nil
}

func storeAcmeDNS01ChallengeState(ctx context.Context, world acmeDNS01WorldAPI, solver string, state *acmeDNS01ChallengeState) error {
	key := acmeDNS01ChallengeStateKey(solver)
	if state == nil || len(state.Challenges) == 0 {
		return world.StrDel(ctx, key)
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return world.StrSet(ctx, key, string(payload))
}

func loadAcmeDNS01PresentationState(ctx context.Context, world acmeDNS01WorldAPI, solver string) (*acmeDNS01PresentationState, error) {
	key := acmeDNS01PresentationStateKey(solver)

	value, err := world.StrGet(ctx, key)
	if err != nil {
		if world.StrIsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	state := &acmeDNS01PresentationState{}
	if err := json.Unmarshal([]byte(value), state); err != nil {
		return nil, err
	}
	if state.Entries == nil {
		state.Entries = map[string]acmeDNS01PresentationEntry{}
	}
	return state, nil
}

func storeAcmeDNS01PresentationState(ctx context.Context, world acmeDNS01WorldAPI, solver string, state *acmeDNS01PresentationState) error {
	key := acmeDNS01PresentationStateKey(solver)
	if state == nil || len(state.Entries) == 0 {
		return world.StrDel(ctx, key)
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return world.StrSet(ctx, key, string(payload))
}
