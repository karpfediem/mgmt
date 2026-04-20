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
	"fmt"
	"net/url"
	"strings"
)

type acmeHTTP01WorldAPI interface {
	StrIsNotExist(error) bool
	StrGet(context.Context, string) (string, error)
	StrSet(context.Context, string, string) error
	StrDel(context.Context, string) error
	StrMapGet(context.Context, string) (map[string]string, error)
	StrMapSet(context.Context, string, string) error
	StrMapDel(context.Context, string) error
}

type acmeHTTP01Challenge struct {
	Attempt      string `json:"attempt"`
	Domain       string `json:"domain"`
	Token        string `json:"token"`
	Path         string `json:"path"`
	Body         string `json:"body"`
	ChallengeURL string `json:"challenge_url"`
}

func (obj acmeHTTP01Challenge) key() string {
	return strings.Join([]string{
		obj.Attempt,
		normalizeHTTPHost(obj.Domain),
		obj.Token,
	}, "\x00")
}

type acmeHTTP01ChallengeState struct {
	Challenges map[string]acmeHTTP01Challenge `json:"challenges"`
}

type acmeHTTP01PresentationEntry struct {
	Attempt string `json:"attempt"`
	Domain  string `json:"domain"`
	Path    string `json:"path"`
	Ready   bool   `json:"ready"`
	Error   string `json:"error"`
}

type acmeHTTP01PresentationState struct {
	Entries map[string]acmeHTTP01PresentationEntry `json:"entries"`
}

func acmeHTTP01ChallengeStateKey(solver string) string {
	return "acme/http01/challenges/" + url.QueryEscape(strings.TrimSpace(solver))
}

func acmeHTTP01PresentationStateKey(solver string) string {
	return "acme/http01/presentation/" + url.QueryEscape(strings.TrimSpace(solver))
}

func loadAcmeHTTP01ChallengeState(ctx context.Context, world acmeHTTP01WorldAPI, solver string) (*acmeHTTP01ChallengeState, error) {
	key := acmeHTTP01ChallengeStateKey(solver)

	value, err := world.StrGet(ctx, key)
	if err != nil {
		if world.StrIsNotExist(err) {
			return &acmeHTTP01ChallengeState{Challenges: map[string]acmeHTTP01Challenge{}}, nil
		}
		return nil, err
	}

	state := &acmeHTTP01ChallengeState{}
	if err := json.Unmarshal([]byte(value), state); err != nil {
		return nil, err
	}
	if state.Challenges == nil {
		state.Challenges = map[string]acmeHTTP01Challenge{}
	}
	return state, nil
}

func storeAcmeHTTP01ChallengeState(ctx context.Context, world acmeHTTP01WorldAPI, solver string, state *acmeHTTP01ChallengeState) error {
	key := acmeHTTP01ChallengeStateKey(solver)
	if state == nil || len(state.Challenges) == 0 {
		return world.StrDel(ctx, key)
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return world.StrSet(ctx, key, string(payload))
}

func loadAcmeHTTP01PresentationStates(ctx context.Context, world acmeHTTP01WorldAPI, solver string) (map[string]acmeHTTP01PresentationState, error) {
	values, err := world.StrMapGet(ctx, acmeHTTP01PresentationStateKey(solver))
	if err != nil {
		return nil, err
	}

	result := make(map[string]acmeHTTP01PresentationState, len(values))
	for hostname, value := range values {
		state := acmeHTTP01PresentationState{}
		if err := json.Unmarshal([]byte(value), &state); err != nil {
			return nil, fmt.Errorf("could not decode HTTP-01 presentation state for %s: %w", hostname, err)
		}
		if state.Entries == nil {
			state.Entries = map[string]acmeHTTP01PresentationEntry{}
		}
		result[hostname] = state
	}

	return result, nil
}

func storeAcmeHTTP01PresentationState(ctx context.Context, world acmeHTTP01WorldAPI, solver string, state *acmeHTTP01PresentationState) error {
	key := acmeHTTP01PresentationStateKey(solver)
	if state == nil || len(state.Entries) == 0 {
		return world.StrMapDel(ctx, key)
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return world.StrMapSet(ctx, key, string(payload))
}
