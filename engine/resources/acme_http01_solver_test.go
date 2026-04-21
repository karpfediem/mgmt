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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/purpleidea/mgmt/engine"
)

func TestAcmeHTTP01SolverServesActiveChallenge(t *testing.T) {
	world := newFakeWorld("solver-a")
	sends := &AcmeHTTP01SolverSends{}

	res := &AcmeHTTP01SolverRes{
		Server: "public-80",
		Hosts:  []string{"example.com"},
	}
	res.SetName("public-http01")
	if err := res.Init(&engine.Init{
		Hostname: "solver-a",
		Event:    func(context.Context) error { return nil },
		Send: func(st interface{}) error {
			payload, ok := st.(*AcmeHTTP01SolverSends)
			if !ok {
				t.Fatalf("unexpected send payload: %T", st)
			}
			*sends = *payload
			return nil
		},
		World: world,
		Logf:  func(string, ...interface{}) {},
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	challenge := acmeHTTP01Challenge{
		Attempt:      "attempt-1",
		Domain:       "example.com",
		Token:        "token",
		Path:         "/.well-known/acme-challenge/token",
		Body:         "key-authorization",
		ChallengeURL: "https://ca.test/challenge/1",
	}
	if err := storeAcmeHTTP01ChallengeState(context.Background(), world, res.Name(), &acmeHTTP01ChallengeState{
		Challenges: map[string]acmeHTTP01Challenge{
			challenge.key(): challenge,
		},
	}); err != nil {
		t.Fatalf("storeAcmeHTTP01ChallengeState failed: %v", err)
	}
	if _, err := res.syncWorldState(context.Background()); err != nil {
		t.Fatalf("syncWorldState failed: %v", err)
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

	states, err := loadAcmeHTTP01PresentationStates(context.Background(), world, res.Name())
	if err != nil {
		t.Fatalf("loadAcmeHTTP01PresentationStates failed: %v", err)
	}
	state, exists := states["solver-a"]
	if !exists {
		t.Fatalf("expected presentation state for solver-a")
	}
	if len(state.Entries) != 1 {
		t.Fatalf("expected one presentation entry, got %d", len(state.Entries))
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

func TestAcmeHTTP01SolverServesThroughHTTPServerGrouping(t *testing.T) {
	world := newFakeWorld("solver-a")

	server := &HTTPServerRes{
		Address: ":80",
	}
	server.SetName("public-80")

	solver := &AcmeHTTP01SolverRes{
		Server: "public-80",
		Hosts:  []string{"example.com"},
	}
	solver.SetName("public-http01")

	if err := server.GroupCmp(solver); err != nil {
		t.Fatalf("GroupCmp failed: %v", err)
	}
	if err := server.GroupRes(solver); err != nil {
		t.Fatalf("GroupRes failed: %v", err)
	}

	if err := server.Init(&engine.Init{
		Hostname: "solver-a",
		Event:    func(context.Context) error { return nil },
		Send:     func(interface{}) error { return nil },
		World:    world,
		Logf:     func(string, ...interface{}) {},
	}); err != nil {
		t.Fatalf("server Init failed: %v", err)
	}

	challenge := acmeHTTP01Challenge{
		Attempt:      "attempt-1",
		Domain:       "example.com",
		Token:        "token",
		Path:         "/.well-known/acme-challenge/token",
		Body:         "key-authorization",
		ChallengeURL: "https://ca.test/challenge/1",
	}
	if err := storeAcmeHTTP01ChallengeState(context.Background(), world, solver.Name(), &acmeHTTP01ChallengeState{
		Challenges: map[string]acmeHTTP01Challenge{
			challenge.key(): challenge,
		},
	}); err != nil {
		t.Fatalf("storeAcmeHTTP01ChallengeState failed: %v", err)
	}
	if _, err := solver.syncWorldState(context.Background()); err != nil {
		t.Fatalf("syncWorldState failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com"+challenge.Path, nil)
	req.Host = "example.com"

	recorder := httptest.NewRecorder()
	server.handler()(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", recorder.Code)
	}
	if recorder.Body.String() != challenge.Body {
		t.Fatalf("unexpected challenge body: %q", recorder.Body.String())
	}
}

func TestAcmeHTTP01SolverRejectsUnhandledHost(t *testing.T) {
	world := newFakeWorld("solver-a")
	sends := &AcmeHTTP01SolverSends{}

	res := &AcmeHTTP01SolverRes{
		Server: "public-80",
		Hosts:  []string{"allowed.example"},
	}
	res.SetName("public-http01")
	if err := res.Init(&engine.Init{
		Hostname: "solver-a",
		Event:    func(context.Context) error { return nil },
		Send: func(st interface{}) error {
			payload, ok := st.(*AcmeHTTP01SolverSends)
			if !ok {
				t.Fatalf("unexpected send payload: %T", st)
			}
			*sends = *payload
			return nil
		},
		World: world,
		Logf:  func(string, ...interface{}) {},
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	challenge := acmeHTTP01Challenge{
		Attempt:      "attempt-1",
		Domain:       "example.com",
		Token:        "token",
		Path:         "/.well-known/acme-challenge/token",
		Body:         "key-authorization",
		ChallengeURL: "https://ca.test/challenge/1",
	}
	if err := storeAcmeHTTP01ChallengeState(context.Background(), world, res.Name(), &acmeHTTP01ChallengeState{
		Challenges: map[string]acmeHTTP01Challenge{
			challenge.key(): challenge,
		},
	}); err != nil {
		t.Fatalf("storeAcmeHTTP01ChallengeState failed: %v", err)
	}
	if _, err := res.syncWorldState(context.Background()); err != nil {
		t.Fatalf("syncWorldState failed: %v", err)
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

func TestAcmeHTTP01SolverWatchHandlesMultipleChallengeUpdates(t *testing.T) {
	world := newFakeWorld("solver-a")

	res := &AcmeHTTP01SolverRes{
		Server: "public-80",
		Hosts:  []string{"example.com", "www.example.com"},
	}
	res.SetName("public-http01")
	if err := res.Init(&engine.Init{
		Hostname: "solver-a",
		Event:    func(context.Context) error { return nil },
		Send:     func(interface{}) error { return nil },
		World:    world,
		Logf:     func(string, ...interface{}) {},
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				errCh <- fmt.Errorf("panic in Watch: %v", r)
			}
		}()
		errCh <- res.Watch(ctx)
	}()

	challenge1 := acmeHTTP01Challenge{
		Attempt:      "attempt-1",
		Domain:       "example.com",
		Token:        "token-1",
		Path:         "/.well-known/acme-challenge/token-1",
		Body:         "key-authorization-1",
		ChallengeURL: "https://ca.test/challenge/1",
	}
	if err := storeAcmeHTTP01ChallengeState(context.Background(), world, res.Name(), &acmeHTTP01ChallengeState{
		Challenges: map[string]acmeHTTP01Challenge{
			challenge1.key(): challenge1,
		},
	}); err != nil {
		t.Fatalf("storing first challenge state failed: %v", err)
	}
	waitForHTTP01PresentationEntries(t, world, res.Name(), "solver-a", 1)

	challenge2 := acmeHTTP01Challenge{
		Attempt:      "attempt-1",
		Domain:       "www.example.com",
		Token:        "token-2",
		Path:         "/.well-known/acme-challenge/token-2",
		Body:         "key-authorization-2",
		ChallengeURL: "https://ca.test/challenge/2",
	}
	if err := storeAcmeHTTP01ChallengeState(context.Background(), world, res.Name(), &acmeHTTP01ChallengeState{
		Challenges: map[string]acmeHTTP01Challenge{
			challenge1.key(): challenge1,
			challenge2.key(): challenge2,
		},
	}); err != nil {
		t.Fatalf("storing second challenge state failed: %v", err)
	}
	waitForHTTP01PresentationEntries(t, world, res.Name(), "solver-a", 2)

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Watch returned unexpected error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for Watch to exit")
	}
}

func TestAcmeHTTP01SolverWatchIgnoresNoOpWakeup(t *testing.T) {
	world := newFakeWorld("solver-a")
	events := make(chan struct{}, 4)

	res := &AcmeHTTP01SolverRes{
		Server: "public-80",
		Hosts:  []string{"example.com"},
	}
	res.SetName("public-http01")
	if err := res.Init(&engine.Init{
		Hostname: "solver-a",
		Event: func(context.Context) error {
			events <- struct{}{}
			return nil
		},
		Send:  func(interface{}) error { return nil },
		World: world,
		Logf:  func(string, ...interface{}) {},
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	challenge := acmeHTTP01Challenge{
		Attempt:      "attempt-1",
		Domain:       "example.com",
		Token:        "token-1",
		Path:         "/.well-known/acme-challenge/token-1",
		Body:         "key-authorization-1",
		ChallengeURL: "https://ca.test/challenge/1",
	}
	if err := storeAcmeHTTP01ChallengeState(context.Background(), world, res.Name(), &acmeHTTP01ChallengeState{
		Challenges: map[string]acmeHTTP01Challenge{
			challenge.key(): challenge,
		},
	}); err != nil {
		t.Fatalf("storeAcmeHTTP01ChallengeState failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- res.Watch(ctx)
	}()

	waitForFakeWorldStrWatchers(t, world, acmeHTTP01ChallengeStateKey(res.Name()), 1)
	waitForTestEvent(t, events, "initial HTTP-01 solver watch event")

	sendFakeWorldStrWatchEvent(world, acmeHTTP01ChallengeStateKey(res.Name()))
	assertNoTestEvent(t, events, "HTTP-01 solver event after a no-op wakeup")

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Watch returned unexpected error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for Watch to exit")
	}
}

func waitForHTTP01PresentationEntries(t *testing.T, world *fakeWorld, solver, hostname string, count int) {
	t.Helper()

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		states, err := loadAcmeHTTP01PresentationStates(context.Background(), world, solver)
		if err != nil {
			t.Fatalf("loadAcmeHTTP01PresentationStates failed: %v", err)
		}
		if len(states[hostname].Entries) == count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	states, err := loadAcmeHTTP01PresentationStates(context.Background(), world, solver)
	if err != nil {
		t.Fatalf("loadAcmeHTTP01PresentationStates failed: %v", err)
	}
	t.Fatalf("timed out waiting for %d presentation entries, got %d", count, len(states[hostname].Entries))
}
