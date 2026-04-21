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
	"testing"
	"time"

	"github.com/purpleidea/mgmt/engine"

	legochallenge "github.com/go-acme/lego/v4/challenge"
)

type fakeDNSChallengeProvider struct {
	presentCalls int
	cleanupCalls int
	presentErr   error
	cleanupErr   error
}

func (obj *fakeDNSChallengeProvider) Present(domain, token, keyAuth string) error {
	obj.presentCalls++
	return obj.presentErr
}

func (obj *fakeDNSChallengeProvider) CleanUp(domain, token, keyAuth string) error {
	obj.cleanupCalls++
	return obj.cleanupErr
}

func initFakeDNS01Solver(t *testing.T, world *fakeWorld, provider legochallenge.Provider, waitForPropagation func(context.Context, acmeDNS01Challenge) error) (*AcmeDNS01SolverRes, *AcmeDNS01SolverSends) {
	t.Helper()

	sends := &AcmeDNS01SolverSends{}
	res := &AcmeDNS01SolverRes{
		Provider: "fake",
		Env:      map[string]string{"TOKEN": "value"},
		providerFactory: func(string, map[string]string) (legochallenge.Provider, error) {
			return provider, nil
		},
		waitForPropagation: waitForPropagation,
	}
	res.SetName("public-dns01")
	if err := res.Init(&engine.Init{
		Hostname: "solver-a",
		Event:    func(context.Context) error { return nil },
		Send: func(st interface{}) error {
			payload, ok := st.(*AcmeDNS01SolverSends)
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
	return res, sends
}

func TestAcmeDNS01SolverPresentsActiveChallenge(t *testing.T) {
	world := newFakeWorld("solver-a")
	provider := &fakeDNSChallengeProvider{}
	res, sends := initFakeDNS01Solver(t, world, provider, func(context.Context, acmeDNS01Challenge) error {
		return nil
	})

	challenge := acmeDNS01Challenge{
		Attempt:          "attempt-1",
		Domain:           "example.com",
		Token:            "token",
		KeyAuthorization: "key-authorization",
		FQDN:             "_acme-challenge.example.com.",
		Value:            "txt-value",
		ChallengeURL:     "https://ca.test/challenge/1",
	}
	if err := storeAcmeDNS01ChallengeState(context.Background(), world, res.Name(), &acmeDNS01ChallengeState{
		Challenges: map[string]acmeDNS01Challenge{
			challenge.key(): challenge,
		},
	}); err != nil {
		t.Fatalf("storeAcmeDNS01ChallengeState failed: %v", err)
	}
	if _, err := res.syncDesiredState(context.Background()); err != nil {
		t.Fatalf("syncDesiredState failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected solver checkOK to be false while a challenge is active")
	}
	if provider.presentCalls != 1 {
		t.Fatalf("expected one Present call, got %d", provider.presentCalls)
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

	state, err := loadAcmeDNS01PresentationState(context.Background(), world, res.Name())
	if err != nil {
		t.Fatalf("loadAcmeDNS01PresentationState failed: %v", err)
	}
	if state == nil || len(state.Entries) != 1 {
		t.Fatalf("expected one presentation entry")
	}
	entry := state.Entries[challenge.key()]
	if !entry.Ready {
		t.Fatalf("expected presentation entry to be ready")
	}
}

func TestAcmeDNS01SolverCleansUpRemovedChallenge(t *testing.T) {
	world := newFakeWorld("solver-a")
	provider := &fakeDNSChallengeProvider{}
	res, sends := initFakeDNS01Solver(t, world, provider, func(context.Context, acmeDNS01Challenge) error {
		return nil
	})

	challenge := acmeDNS01Challenge{
		Attempt:          "attempt-1",
		Domain:           "example.com",
		Token:            "token",
		KeyAuthorization: "key-authorization",
		FQDN:             "_acme-challenge.example.com.",
		Value:            "txt-value",
		ChallengeURL:     "https://ca.test/challenge/1",
	}
	if err := storeAcmeDNS01ChallengeState(context.Background(), world, res.Name(), &acmeDNS01ChallengeState{
		Challenges: map[string]acmeDNS01Challenge{
			challenge.key(): challenge,
		},
	}); err != nil {
		t.Fatalf("storeAcmeDNS01ChallengeState failed: %v", err)
	}
	if _, err := res.syncDesiredState(context.Background()); err != nil {
		t.Fatalf("syncDesiredState failed: %v", err)
	}
	if _, err := res.CheckApply(context.Background(), true); err != nil {
		t.Fatalf("initial CheckApply failed: %v", err)
	}

	if err := storeAcmeDNS01ChallengeState(context.Background(), world, res.Name(), nil); err != nil {
		t.Fatalf("clearing challenge state failed: %v", err)
	}
	if _, err := res.syncDesiredState(context.Background()); err != nil {
		t.Fatalf("syncDesiredState failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected solver checkOK to be true once all challenges were removed")
	}
	if provider.cleanupCalls != 1 {
		t.Fatalf("expected one CleanUp call, got %d", provider.cleanupCalls)
	}
	if sends.Pending {
		t.Fatalf("expected pending to be false after cleanup")
	}
	if sends.Ready {
		t.Fatalf("expected ready to be false without active challenges")
	}

	state, err := loadAcmeDNS01PresentationState(context.Background(), world, res.Name())
	if err != nil {
		t.Fatalf("loadAcmeDNS01PresentationState failed: %v", err)
	}
	if state != nil {
		t.Fatalf("expected presentation state to be cleared")
	}
}

func TestAcmeDNS01SolverReportsProviderError(t *testing.T) {
	world := newFakeWorld("solver-a")
	provider := &fakeDNSChallengeProvider{presentErr: fmt.Errorf("present failed")}
	res, sends := initFakeDNS01Solver(t, world, provider, func(context.Context, acmeDNS01Challenge) error {
		return nil
	})

	challenge := acmeDNS01Challenge{
		Attempt:          "attempt-1",
		Domain:           "example.com",
		Token:            "token",
		KeyAuthorization: "key-authorization",
		FQDN:             "_acme-challenge.example.com.",
		Value:            "txt-value",
		ChallengeURL:     "https://ca.test/challenge/1",
	}
	if err := storeAcmeDNS01ChallengeState(context.Background(), world, res.Name(), &acmeDNS01ChallengeState{
		Challenges: map[string]acmeDNS01Challenge{
			challenge.key(): challenge,
		},
	}); err != nil {
		t.Fatalf("storeAcmeDNS01ChallengeState failed: %v", err)
	}
	if _, err := res.syncDesiredState(context.Background()); err != nil {
		t.Fatalf("syncDesiredState failed: %v", err)
	}

	if _, err := res.CheckApply(context.Background(), true); err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if sends.Ready {
		t.Fatalf("expected ready to be false when presentation fails")
	}
	if sends.Error == "" {
		t.Fatalf("expected an error when the provider presentation fails")
	}

	state, err := loadAcmeDNS01PresentationState(context.Background(), world, res.Name())
	if err != nil {
		t.Fatalf("loadAcmeDNS01PresentationState failed: %v", err)
	}
	entry, exists := state.Entries[challenge.key()]
	if !exists {
		t.Fatalf("expected presentation entry for failed challenge")
	}
	if entry.Error == "" {
		t.Fatalf("expected stored presentation error")
	}
}

func TestAcmeDNS01SolverReportsPropagationError(t *testing.T) {
	world := newFakeWorld("solver-a")
	provider := &fakeDNSChallengeProvider{}
	res, sends := initFakeDNS01Solver(t, world, provider, func(context.Context, acmeDNS01Challenge) error {
		return fmt.Errorf("propagation failed")
	})

	challenge := acmeDNS01Challenge{
		Attempt:          "attempt-1",
		Domain:           "example.com",
		Token:            "token",
		KeyAuthorization: "key-authorization",
		FQDN:             "_acme-challenge.example.com.",
		Value:            "txt-value",
		ChallengeURL:     "https://ca.test/challenge/1",
	}
	if err := storeAcmeDNS01ChallengeState(context.Background(), world, res.Name(), &acmeDNS01ChallengeState{
		Challenges: map[string]acmeDNS01Challenge{
			challenge.key(): challenge,
		},
	}); err != nil {
		t.Fatalf("storeAcmeDNS01ChallengeState failed: %v", err)
	}
	if _, err := res.syncDesiredState(context.Background()); err != nil {
		t.Fatalf("syncDesiredState failed: %v", err)
	}

	if _, err := res.CheckApply(context.Background(), true); err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if sends.Ready {
		t.Fatalf("expected ready to be false when propagation fails")
	}
	if sends.Error == "" {
		t.Fatalf("expected an error when DNS propagation fails")
	}

	state, err := loadAcmeDNS01PresentationState(context.Background(), world, res.Name())
	if err != nil {
		t.Fatalf("loadAcmeDNS01PresentationState failed: %v", err)
	}
	entry, exists := state.Entries[challenge.key()]
	if !exists {
		t.Fatalf("expected presentation entry for failed challenge")
	}
	if entry.Error == "" {
		t.Fatalf("expected stored propagation error")
	}
}

func TestAcmeDNS01SolverWatchIgnoresNoOpWakeup(t *testing.T) {
	world := newFakeWorld("solver-a")
	provider := &fakeDNSChallengeProvider{}
	events := make(chan struct{}, 4)

	res := &AcmeDNS01SolverRes{
		Provider: "fake",
		Env:      map[string]string{"TOKEN": "value"},
		providerFactory: func(string, map[string]string) (legochallenge.Provider, error) {
			return provider, nil
		},
		waitForPropagation: func(context.Context, acmeDNS01Challenge) error {
			return nil
		},
	}
	res.SetName("public-dns01")
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

	challenge := acmeDNS01Challenge{
		Attempt:          "attempt-1",
		Domain:           "example.com",
		Token:            "token",
		KeyAuthorization: "key-authorization",
		FQDN:             "_acme-challenge.example.com.",
		Value:            "txt-value",
		ChallengeURL:     "https://ca.test/challenge/1",
	}
	if err := storeAcmeDNS01ChallengeState(context.Background(), world, res.Name(), &acmeDNS01ChallengeState{
		Challenges: map[string]acmeDNS01Challenge{
			challenge.key(): challenge,
		},
	}); err != nil {
		t.Fatalf("storeAcmeDNS01ChallengeState failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- res.Watch(ctx)
	}()

	waitForFakeWorldStrWatchers(t, world, acmeDNS01ChallengeStateKey(res.Name()), 1)
	waitForTestEvent(t, events, "initial DNS-01 solver watch event")

	sendFakeWorldStrWatchEvent(world, acmeDNS01ChallengeStateKey(res.Name()))
	assertNoTestEvent(t, events, "DNS-01 solver event after a no-op wakeup")

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
