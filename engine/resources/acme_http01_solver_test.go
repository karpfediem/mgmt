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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/local"

	"github.com/go-acme/lego/v4/challenge/http01"
)

func testLocalAPI(t *testing.T) *local.API {
	t.Helper()

	return (&local.API{
		Prefix: t.TempDir(),
		Logf:   func(string, ...interface{}) {},
	}).Init()
}

func TestAcmeHTTP01ProviderPresentAndCleanUp(t *testing.T) {
	api := testLocalAPI(t)

	provider, err := newAcmeHTTP01Provider(api, "public-http01")
	if err != nil {
		t.Fatalf("newAcmeHTTP01Provider failed: %v", err)
	}

	ctx := context.Background()
	watch, err := api.ValueWatch(ctx, acmeHTTP01ChallengeStateKey("public-http01"))
	if err != nil {
		t.Fatalf("ValueWatch failed: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		for {
			select {
			case <-watch:
				state, err := loadAcmeHTTP01ChallengeState(ctx, api, "public-http01")
				if err != nil {
					errCh <- err
					return
				}
				if len(state.Challenges) == 0 {
					continue
				}

				presentation := &acmeHTTP01PresentationState{
					Entries: map[string]acmeHTTP01PresentationEntry{},
				}
				for key, challenge := range state.Challenges {
					presentation.Entries[key] = acmeHTTP01PresentationEntry{
						Attempt: challenge.Attempt,
						Domain:  challenge.Domain,
						Path:    challenge.Path,
						Ready:   true,
					}
				}
				errCh <- storeAcmeHTTP01PresentationState(ctx, api, "public-http01", presentation)
				return
			case <-time.After(5 * time.Second):
				errCh <- context.DeadlineExceeded
				return
			}
		}
	}()

	if err := provider.Present("example.com", "token", "key-authorization"); err != nil {
		t.Fatalf("Present failed: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("presentation helper failed: %v", err)
	}

	state, err := loadAcmeHTTP01ChallengeState(ctx, api, "public-http01")
	if err != nil {
		t.Fatalf("loadAcmeHTTP01ChallengeState failed: %v", err)
	}
	if len(state.Challenges) != 1 {
		t.Fatalf("expected one active challenge, got %d", len(state.Challenges))
	}

	if err := provider.CleanUp("example.com", "token", "key-authorization"); err != nil {
		t.Fatalf("CleanUp failed: %v", err)
	}

	state, err = loadAcmeHTTP01ChallengeState(ctx, api, "public-http01")
	if err != nil {
		t.Fatalf("loadAcmeHTTP01ChallengeState failed: %v", err)
	}
	if len(state.Challenges) != 0 {
		t.Fatalf("expected no active challenges after cleanup, got %d", len(state.Challenges))
	}
}

func TestAcmeHTTP01SolverServesActiveChallenge(t *testing.T) {
	api := testLocalAPI(t)
	sends := &AcmeHTTP01SolverSends{}

	res := &AcmeHTTP01SolverRes{
		Server: "public-80",
		Hosts:  []string{"example.com"},
	}
	res.SetName("public-http01")
	if err := res.Init(&engine.Init{
		Event: func(ctx context.Context) error { return nil },
		Send: func(st interface{}) error {
			payload, ok := st.(*AcmeHTTP01SolverSends)
			if !ok {
				t.Fatalf("unexpected send payload: %T", st)
			}
			*sends = *payload
			return nil
		},
		Local: api,
		Logf:  func(string, ...interface{}) {},
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	challenge := acmeHTTP01Challenge{
		Attempt: "attempt-1",
		Domain:  "example.com",
		Token:   "token",
		Path:    http01.ChallengePath("token"),
		Body:    "key-authorization",
	}
	if err := storeAcmeHTTP01ChallengeState(context.Background(), api, res.Name(), &acmeHTTP01ChallengeState{
		Challenges: map[string]acmeHTTP01Challenge{
			challenge.key(): challenge,
		},
	}); err != nil {
		t.Fatalf("storeAcmeHTTP01ChallengeState failed: %v", err)
	}
	if err := res.syncLocalState(context.Background()); err != nil {
		t.Fatalf("syncLocalState failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected solver checkOK to be false while a challenge is active")
	}
	if !sends.Pending {
		t.Fatalf("expected pending to be true")
	}
	if !sends.Ready {
		t.Fatalf("expected ready to be true")
	}
	if sends.ChallengeCount != 1 {
		t.Fatalf("expected one active challenge, got %d", sends.ChallengeCount)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com"+challenge.Path, nil)
	req.Host = "example.com"
	if err := res.AcceptHTTP(req); err != nil {
		t.Fatalf("AcceptHTTP failed: %v", err)
	}

	recorder := httptest.NewRecorder()
	res.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", recorder.Code)
	}
	if recorder.Body.String() != challenge.Body {
		t.Fatalf("unexpected challenge body: %q", recorder.Body.String())
	}
}

func TestAcmeHTTP01SolverRejectsUnhandledHost(t *testing.T) {
	api := testLocalAPI(t)
	sends := &AcmeHTTP01SolverSends{}

	res := &AcmeHTTP01SolverRes{
		Server: "public-80",
		Hosts:  []string{"allowed.example"},
	}
	res.SetName("public-http01")
	if err := res.Init(&engine.Init{
		Event: func(ctx context.Context) error { return nil },
		Send: func(st interface{}) error {
			payload, ok := st.(*AcmeHTTP01SolverSends)
			if !ok {
				t.Fatalf("unexpected send payload: %T", st)
			}
			*sends = *payload
			return nil
		},
		Local: api,
		Logf:  func(string, ...interface{}) {},
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	challenge := acmeHTTP01Challenge{
		Attempt: "attempt-1",
		Domain:  "example.com",
		Token:   "token",
		Path:    http01.ChallengePath("token"),
		Body:    "key-authorization",
	}
	if err := storeAcmeHTTP01ChallengeState(context.Background(), api, res.Name(), &acmeHTTP01ChallengeState{
		Challenges: map[string]acmeHTTP01Challenge{
			challenge.key(): challenge,
		},
	}); err != nil {
		t.Fatalf("storeAcmeHTTP01ChallengeState failed: %v", err)
	}
	if err := res.syncLocalState(context.Background()); err != nil {
		t.Fatalf("syncLocalState failed: %v", err)
	}

	if _, err := res.CheckApply(context.Background(), true); err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if sends.Ready {
		t.Fatalf("expected ready to be false")
	}
	if sends.Error == "" {
		t.Fatalf("expected an error when the host is not handled")
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com"+challenge.Path, nil)
	req.Host = "example.com"
	if err := res.AcceptHTTP(req); err == nil {
		t.Fatalf("expected AcceptHTTP to reject an unhandled host")
	}
}
