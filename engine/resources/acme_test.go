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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/purpleidea/mgmt/engine"

	"github.com/go-acme/lego/v4/acme"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/registration"
)

func fakeAcmeInit(t *testing.T, refresh bool, world *fakeWorld) (*engine.Init, func() *AcmeSends) {
	t.Helper()

	tmpdir := fmt.Sprintf("%s/", t.TempDir())
	sends := &AcmeSends{}
	if world == nil {
		world = newFakeWorld("test-host")
	}

	return &engine.Init{
			Hostname: "test-host",
			Event: func(ctx context.Context) error {
				return nil
			},
			Refresh: func() bool {
				return refresh
			},
			Send: func(st interface{}) error {
				x, ok := st.(*AcmeSends)
				if !ok {
					return fmt.Errorf("unexpected send payload: %T", st)
				}
				*sends = *x
				return nil
			},
			VarDir: func(p string) (string, error) {
				return path.Join(tmpdir, p), nil
			},
			World: world,
			Logf:  func(string, ...interface{}) {},
		}, func() *AcmeSends {
			copy := *sends
			return &copy
		}
}

type fakeAcmeClient struct {
	accountKeyPEM        string
	registration         *registration.Resource
	beginHTTP01Result    *acmeHTTP01OrderState
	completeHTTP01Result *certificate.Resource
	beginDNS01Result     *acmeDNS01OrderState
	completeDNS01Result  *certificate.Resource
	beginHTTP01Err       error
	completeHTTP01Err    error
	beginDNS01Err        error
	completeDNS01Err     error

	ensureCalls         int
	beginHTTP01Calls    int
	completeHTTP01Calls int
	beginDNS01Calls     int
	completeDNS01Calls  int
}

func (obj *fakeAcmeClient) AccountKeyPEM() string {
	return obj.accountKeyPEM
}

func (obj *fakeAcmeClient) EnsureRegistration(emailChanged, acceptTOS bool) (*registration.Resource, error) {
	obj.ensureCalls++
	return obj.registration, nil
}

func (obj *fakeAcmeClient) BeginHTTP01(req acmeHTTP01IssueRequest) (*acmeHTTP01OrderState, error) {
	obj.beginHTTP01Calls++
	return obj.beginHTTP01Result, obj.beginHTTP01Err
}

func (obj *fakeAcmeClient) CompleteHTTP01(orderState *acmeHTTP01OrderState) (*certificate.Resource, error) {
	obj.completeHTTP01Calls++
	return obj.completeHTTP01Result, obj.completeHTTP01Err
}

func (obj *fakeAcmeClient) BeginDNS01(req acmeDNS01IssueRequest) (*acmeDNS01OrderState, error) {
	obj.beginDNS01Calls++
	return obj.beginDNS01Result, obj.beginDNS01Err
}

func (obj *fakeAcmeClient) CompleteDNS01(orderState *acmeDNS01OrderState) (*certificate.Resource, error) {
	obj.completeDNS01Calls++
	return obj.completeDNS01Result, obj.completeDNS01Err
}

func assertAcmeImmediateRecheckRequested(t *testing.T, res *AcmeRes, description string) {
	t.Helper()

	select {
	case <-res.recheckCh:
	default:
		t.Fatalf("expected immediate recheck after %s", description)
	}
}

func assertNoAcmeImmediateRecheckRequested(t *testing.T, res *AcmeRes, description string) {
	t.Helper()

	select {
	case <-res.recheckCh:
		t.Fatalf("unexpected immediate recheck after %s", description)
	default:
	}
}

func mustStoreTestAcmeAccountState(t *testing.T, world *fakeWorld, name, directoryURL string) {
	t.Helper()

	err := storeAcmeAccountSharedState(context.Background(), world, name, &acmeAccountStoredState{
		Version:         1,
		Email:           "ops@example.com",
		DirectoryURL:    directoryURL,
		PrivateKeyPEM:   "ACCOUNT\n",
		RegistrationURI: "https://example.test/acct/1",
	})
	if err != nil {
		t.Fatalf("storeAcmeAccountSharedState failed: %v", err)
	}
}

func TestAcmeCheckApplyPreparesDNS01BeforeReady(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	ready := false
	world := newFakeWorld("test-host")
	mustStoreTestAcmeAccountState(t, world, "public-ca", "https://example.test/directory")

	client := &fakeAcmeClient{
		accountKeyPEM: "ACCOUNT\n",
		registration:  &registration.Resource{URI: "https://example.test/acct/1"},
		beginDNS01Result: &acmeDNS01OrderState{
			Attempt:        "attempt-1",
			Domains:        []string{"example.com"},
			KeyType:        "2048",
			MustStaple:     false,
			PreferredChain: "",
			OrderURL:       "https://example.test/order/1",
			FinalizeURL:    "https://example.test/order/1/finalize",
			PrivateKeyPEM:  "PRIVATE KEY\n",
			CSRPEM:         "CSR\n",
			Challenges: map[string]acmeDNS01Challenge{
				"challenge-1": {
					Attempt:          "attempt-1",
					Domain:           "example.com",
					Token:            "token",
					KeyAuthorization: "key-authorization",
					FQDN:             "_acme-challenge.example.com.",
					Value:            "txt-value",
					ChallengeURL:     "https://example.test/challenge/1",
				},
			},
		},
	}

	res := &AcmeRes{
		Account:   "public-ca",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeDNS01,
		Solver:    "public-dns01",
		Ready:     &ready,
	}
	init, sent := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState, account *acmeAccountData) (acmeClient, error) {
		return client, nil
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false while preparing dns-01")
	}
	if client.beginDNS01Calls != 1 {
		t.Fatalf("expected one BeginDNS01 call, got %d", client.beginDNS01Calls)
	}
	if client.completeDNS01Calls != 0 {
		t.Fatalf("expected zero CompleteDNS01 calls, got %d", client.completeDNS01Calls)
	}

	payload := sent()
	if !payload.Pending {
		t.Fatalf("expected pending to be true while preparing dns-01")
	}
	if payload.HTTP01Pending {
		t.Fatalf("expected http01_pending to be false for dns-01")
	}
	if payload.PrivateKey != "" {
		t.Fatalf("expected no private key material while waiting for issuance")
	}

	state, err := res.loadState()
	if err != nil {
		t.Fatalf("loadState failed: %v", err)
	}
	if state.DNS01 == nil {
		t.Fatalf("expected pending DNS-01 order state to be persisted")
	}

	challenges, err := loadAcmeDNS01ChallengeState(context.Background(), world, res.Solver)
	if err != nil {
		t.Fatalf("loadAcmeDNS01ChallengeState failed: %v", err)
	}
	if len(challenges.Challenges) != 1 {
		t.Fatalf("expected one published challenge, got %d", len(challenges.Challenges))
	}
}

func TestAcmeCheckApplyCompletesDNS01WhenReadyAndPresented(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	ready := true
	world := newFakeWorld("test-host")
	mustStoreTestAcmeAccountState(t, world, "public-ca", "https://example.test/directory")
	fullchain, leaf, issuer, key := mustTestCertificatePEM(t, now, now.Add(90*24*time.Hour))

	orderState := &acmeDNS01OrderState{
		Attempt:        "attempt-1",
		Domains:        []string{"example.com"},
		KeyType:        "2048",
		MustStaple:     false,
		PreferredChain: "",
		OrderURL:       "https://example.test/order/1",
		FinalizeURL:    "https://example.test/order/1/finalize",
		PrivateKeyPEM:  "PRIVATE KEY\n",
		CSRPEM:         "CSR\n",
		Challenges: map[string]acmeDNS01Challenge{
			"challenge-1": {
				Attempt:          "attempt-1",
				Domain:           "example.com",
				Token:            "token",
				KeyAuthorization: "key-authorization",
				FQDN:             "_acme-challenge.example.com.",
				Value:            "txt-value",
				ChallengeURL:     "https://example.test/challenge/1",
			},
		},
	}

	client := &fakeAcmeClient{
		accountKeyPEM: "ACCOUNT\n",
		registration:  &registration.Resource{URI: "https://example.test/acct/1"},
		completeDNS01Result: &certificate.Resource{
			Domain:            "example.com",
			CertURL:           "https://example.test/cert/1",
			CertStableURL:     "https://example.test/cert/stable/1",
			PrivateKey:        []byte(key),
			Certificate:       []byte(fullchain),
			IssuerCertificate: []byte(issuer),
		},
	}

	res := &AcmeRes{
		Account:   "public-ca",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeDNS01,
		Solver:    "public-dns01",
		Ready:     &ready,
	}
	init, sent := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState, account *acmeAccountData) (acmeClient, error) {
		return client, nil
	}

	if err := res.saveState(&acmeStoredState{
		Version:      1,
		DirectoryURL: "https://example.test/directory",
		Domains:      []string{"example.com"},
		KeyType:      "2048",
		DNS01:        orderState,
	}); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}

	if err := storeAcmeDNS01PresentationState(context.Background(), world, res.Solver, &acmeDNS01PresentationState{
		Entries: map[string]acmeDNS01PresentationEntry{
			"challenge-1": {
				Attempt: "attempt-1",
				Domain:  "example.com",
				FQDN:    "_acme-challenge.example.com.",
				Value:   "txt-value",
				Ready:   true,
			},
		},
	}); err != nil {
		t.Fatalf("storeAcmeDNS01PresentationState failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false after completing dns-01 in this cycle")
	}
	if client.completeDNS01Calls != 1 {
		t.Fatalf("expected one CompleteDNS01 call, got %d", client.completeDNS01Calls)
	}

	payload := sent()
	if payload.Pending {
		t.Fatalf("expected pending to be false after successful issuance")
	}
	if payload.HTTP01Pending {
		t.Fatalf("expected http01_pending to be false after successful issuance")
	}
	if payload.Certificate != normalizePEMString(leaf) {
		t.Fatalf("unexpected certificate payload")
	}
	if payload.FullChain != normalizePEMString(fullchain) {
		t.Fatalf("unexpected fullchain payload")
	}

	state, err := res.loadState()
	if err != nil {
		t.Fatalf("loadState failed: %v", err)
	}
	if state.DNS01 != nil {
		t.Fatalf("expected pending DNS-01 order state to be cleared")
	}
	if state.PrivateKeyPEM != normalizePEMString(key) {
		t.Fatalf("expected issued private key to be persisted")
	}

	challenges, err := loadAcmeDNS01ChallengeState(context.Background(), world, res.Solver)
	if err != nil {
		t.Fatalf("loadAcmeDNS01ChallengeState failed: %v", err)
	}
	if len(challenges.Challenges) != 0 {
		t.Fatalf("expected published challenges to be cleared")
	}
}

func TestAcmeCheckApplyCompletesDNS01WithoutPresentationWhenAuthorizationAlreadyValid(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	ready := false
	world := newFakeWorld("test-host")
	mustStoreTestAcmeAccountState(t, world, "public-ca", "https://example.test/directory")
	fullchain, leaf, issuer, key := mustTestCertificatePEM(t, now, now.Add(90*24*time.Hour))

	orderState := &acmeDNS01OrderState{
		Attempt:        "attempt-1",
		Domains:        []string{"example.com"},
		KeyType:        "2048",
		MustStaple:     false,
		PreferredChain: "",
		OrderURL:       "https://example.test/order/1",
		FinalizeURL:    "https://example.test/order/1/finalize",
		PrivateKeyPEM:  "PRIVATE KEY\n",
		CSRPEM:         "CSR\n",
		Challenges:     map[string]acmeDNS01Challenge{},
	}

	client := &fakeAcmeClient{
		accountKeyPEM: "ACCOUNT\n",
		registration:  &registration.Resource{URI: "https://example.test/acct/1"},
		completeDNS01Result: &certificate.Resource{
			Domain:            "example.com",
			CertURL:           "https://example.test/cert/1",
			CertStableURL:     "https://example.test/cert/stable/1",
			PrivateKey:        []byte(key),
			Certificate:       []byte(fullchain),
			IssuerCertificate: []byte(issuer),
		},
	}

	res := &AcmeRes{
		Account:   "public-ca",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeDNS01,
		Solver:    "public-dns01",
		Ready:     &ready,
	}
	init, sent := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState, account *acmeAccountData) (acmeClient, error) {
		return client, nil
	}

	if err := res.saveState(&acmeStoredState{
		Version:      1,
		DirectoryURL: "https://example.test/directory",
		Domains:      []string{"example.com"},
		KeyType:      "2048",
		DNS01:        orderState,
	}); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false after completing dns-01 in this cycle")
	}
	if client.completeDNS01Calls != 1 {
		t.Fatalf("expected one CompleteDNS01 call, got %d", client.completeDNS01Calls)
	}

	payload := sent()
	if payload.Pending {
		t.Fatalf("expected pending to be false after successful issuance")
	}
	if payload.Certificate != normalizePEMString(leaf) {
		t.Fatalf("unexpected certificate payload")
	}

	challenges, err := loadAcmeDNS01ChallengeState(context.Background(), world, res.Solver)
	if err != nil {
		t.Fatalf("loadAcmeDNS01ChallengeState failed: %v", err)
	}
	if len(challenges.Challenges) != 0 {
		t.Fatalf("expected published challenges to stay empty")
	}
}

func TestAcmeCheckApplyDoesNotRequestImmediateRecheckAfterDNS01PresentationError(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	ready := true
	world := newFakeWorld("test-host")
	mustStoreTestAcmeAccountState(t, world, "public-ca", "https://example.test/directory")

	orderState := &acmeDNS01OrderState{
		Attempt:        "attempt-1",
		Domains:        []string{"example.com"},
		KeyType:        "2048",
		MustStaple:     false,
		PreferredChain: "",
		OrderURL:       "https://example.test/order/1",
		FinalizeURL:    "https://example.test/order/1/finalize",
		PrivateKeyPEM:  "PRIVATE KEY\n",
		CSRPEM:         "CSR\n",
		Challenges: map[string]acmeDNS01Challenge{
			"challenge-1": {
				Attempt:          "attempt-1",
				Domain:           "example.com",
				Token:            "token",
				KeyAuthorization: "key-authorization",
				FQDN:             "_acme-challenge.example.com.",
				Value:            "txt-value",
				ChallengeURL:     "https://example.test/challenge/1",
			},
		},
	}

	client := &fakeAcmeClient{
		accountKeyPEM: "ACCOUNT\n",
		registration:  &registration.Resource{URI: "https://example.test/acct/1"},
	}

	res := &AcmeRes{
		Account:   "public-ca",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeDNS01,
		Solver:    "public-dns01",
		Ready:     &ready,
	}
	init, _ := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState, account *acmeAccountData) (acmeClient, error) {
		return client, nil
	}

	if err := res.saveState(&acmeStoredState{
		Version:      1,
		DirectoryURL: "https://example.test/directory",
		Domains:      []string{"example.com"},
		KeyType:      "2048",
		DNS01:        orderState,
	}); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}

	if err := storeAcmeDNS01PresentationState(context.Background(), world, res.Solver, &acmeDNS01PresentationState{
		Entries: map[string]acmeDNS01PresentationEntry{
			"challenge-1": {
				Attempt: "attempt-1",
				Domain:  "example.com",
				FQDN:    "_acme-challenge.example.com.",
				Value:   "txt-value",
				Error:   "dns-01 propagation: time limit exceeded",
			},
		},
	}); err != nil {
		t.Fatalf("storeAcmeDNS01PresentationState failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "dns-01 solver presentation failed") {
		t.Fatalf("expected dns-01 solver presentation error, got: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false after dns-01 presentation failure")
	}
	if client.beginDNS01Calls != 0 {
		t.Fatalf("expected zero BeginDNS01 calls while handling an existing pending order, got %d", client.beginDNS01Calls)
	}
	if client.completeDNS01Calls != 0 {
		t.Fatalf("expected zero CompleteDNS01 calls after presentation failure, got %d", client.completeDNS01Calls)
	}

	assertNoAcmeImmediateRecheckRequested(t, res, "dns-01 presentation failure")
}

func TestAcmeCheckApplyDoesNotRequestImmediateRecheckAfterDNS01CompleteError(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	ready := true
	world := newFakeWorld("test-host")
	mustStoreTestAcmeAccountState(t, world, "public-ca", "https://example.test/directory")

	orderState := &acmeDNS01OrderState{
		Attempt:        "attempt-1",
		Domains:        []string{"example.com"},
		KeyType:        "2048",
		MustStaple:     false,
		PreferredChain: "",
		OrderURL:       "https://example.test/order/1",
		FinalizeURL:    "https://example.test/order/1/finalize",
		PrivateKeyPEM:  "PRIVATE KEY\n",
		CSRPEM:         "CSR\n",
		Challenges: map[string]acmeDNS01Challenge{
			"challenge-1": {
				Attempt:          "attempt-1",
				Domain:           "example.com",
				Token:            "token",
				KeyAuthorization: "key-authorization",
				FQDN:             "_acme-challenge.example.com.",
				Value:            "txt-value",
				ChallengeURL:     "https://example.test/challenge/1",
			},
		},
	}

	client := &fakeAcmeClient{
		accountKeyPEM:    "ACCOUNT\n",
		registration:     &registration.Resource{URI: "https://example.test/acct/1"},
		completeDNS01Err: fmt.Errorf("acme finalize failed"),
	}

	res := &AcmeRes{
		Account:   "public-ca",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeDNS01,
		Solver:    "public-dns01",
		Ready:     &ready,
	}
	init, _ := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState, account *acmeAccountData) (acmeClient, error) {
		return client, nil
	}

	if err := res.saveState(&acmeStoredState{
		Version:      1,
		DirectoryURL: "https://example.test/directory",
		Domains:      []string{"example.com"},
		KeyType:      "2048",
		DNS01:        orderState,
	}); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}

	if err := storeAcmeDNS01PresentationState(context.Background(), world, res.Solver, &acmeDNS01PresentationState{
		Entries: map[string]acmeDNS01PresentationEntry{
			"challenge-1": {
				Attempt: "attempt-1",
				Domain:  "example.com",
				FQDN:    "_acme-challenge.example.com.",
				Value:   "txt-value",
				Ready:   true,
			},
		},
	}); err != nil {
		t.Fatalf("storeAcmeDNS01PresentationState failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "could not complete dns-01 order") {
		t.Fatalf("expected dns-01 completion error, got: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false after dns-01 completion failure")
	}
	if client.beginDNS01Calls != 0 {
		t.Fatalf("expected zero BeginDNS01 calls while retrying an existing pending order, got %d", client.beginDNS01Calls)
	}
	if client.completeDNS01Calls != 1 {
		t.Fatalf("expected one CompleteDNS01 call, got %d", client.completeDNS01Calls)
	}

	assertNoAcmeImmediateRecheckRequested(t, res, "dns-01 completion failure")
}

func TestAcmeCheckApplyRenewsDNS01CertificateWithinWindow(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	world := newFakeWorld("test-host")
	mustStoreTestAcmeAccountState(t, world, "public-ca", "https://example.test/directory")
	fullchain, _, issuer, key := mustTestCertificatePEM(t, now.Add(-60*24*time.Hour), now.Add(10*24*time.Hour))

	client := &fakeAcmeClient{
		accountKeyPEM: "ACCOUNT\n",
		registration:  &registration.Resource{URI: "https://example.test/acct/1"},
		beginDNS01Result: &acmeDNS01OrderState{
			Attempt:        "attempt-2",
			Domains:        []string{"example.com"},
			KeyType:        "2048",
			MustStaple:     false,
			PreferredChain: "",
			OrderURL:       "https://example.test/order/2",
			FinalizeURL:    "https://example.test/order/2/finalize",
			PrivateKeyPEM:  "NEW PRIVATE KEY\n",
			CSRPEM:         "NEW CSR\n",
			Challenges: map[string]acmeDNS01Challenge{
				"challenge-2": {
					Attempt:          "attempt-2",
					Domain:           "example.com",
					Token:            "token-2",
					KeyAuthorization: "key-authorization-2",
					FQDN:             "_acme-challenge.example.com.",
					Value:            "txt-value-2",
					ChallengeURL:     "https://example.test/challenge/2",
				},
			},
		},
	}

	res := &AcmeRes{
		Account:         "public-ca",
		Domains:         []string{"example.com"},
		Challenge:       acmeChallengeDNS01,
		Solver:          "public-dns01",
		RenewBeforeDays: acmeDefaultRenewBeforeDays,
	}
	init, sent := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState, account *acmeAccountData) (acmeClient, error) {
		return client, nil
	}

	if err := res.saveState(&acmeStoredState{
		Version:              1,
		DirectoryURL:         "https://example.test/directory",
		Domains:              []string{"example.com"},
		KeyType:              "2048",
		Domain:               "example.com",
		CertURL:              "https://example.test/cert/1",
		CertStableURL:        "https://example.test/cert/stable/1",
		PrivateKeyPEM:        normalizePEMString(key),
		CertificatePEM:       normalizePEMString(fullchain),
		IssuerCertificatePEM: normalizePEMString(issuer),
	}); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false while preparing renewal")
	}
	if client.beginDNS01Calls != 1 {
		t.Fatalf("expected one BeginDNS01 call during renewal, got %d", client.beginDNS01Calls)
	}
	if client.completeDNS01Calls != 0 {
		t.Fatalf("expected zero CompleteDNS01 calls during renewal preparation, got %d", client.completeDNS01Calls)
	}

	payload := sent()
	if !payload.Pending {
		t.Fatalf("expected renewal to report pending")
	}
	if payload.PrivateKey != "" {
		t.Fatalf("expected issued material to be cleared while renewal is pending")
	}

	state, err := res.loadState()
	if err != nil {
		t.Fatalf("loadState failed: %v", err)
	}
	if state.DNS01 == nil {
		t.Fatalf("expected pending DNS-01 renewal order state to be persisted")
	}
	if state.CertificatePEM != "" {
		t.Fatalf("expected old certificate material to be cleared while renewal is pending")
	}

	assertAcmeImmediateRecheckRequested(t, res, "successful dns-01 renewal preparation")
}

func TestBuildFullChainFromLeafAndIssuer(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	_, leaf, issuer, _ := mustTestCertificatePEM(t, now, now.Add(90*24*time.Hour))

	fullchain := buildFullChain(leaf, issuer)
	if fullchain != normalizePEMString(leaf)+normalizePEMString(issuer) {
		t.Fatalf("unexpected fullchain output")
	}
}

func TestAcmeValidateRequiresExplicitChallenge(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{"example.com"}

	err := res.Validate()
	if err == nil || !strings.Contains(err.Error(), "Challenge") {
		t.Fatalf("expected challenge validation error, got: %v", err)
	}
}

func TestAcmeValidateAcceptsDNSChallengeWithSolver(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{"example.com"}
	res.Challenge = acmeChallengeDNS01
	res.Solver = "public-dns01"

	if err := res.Validate(); err != nil {
		t.Fatalf("expected DNS challenge config to validate, got: %v", err)
	}
}

func TestAcmeValidateAcceptsMultipleDomains(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{"example.com", "www.example.com"}
	res.Challenge = acmeChallengeDNS01
	res.Solver = "public-dns01"

	if err := res.Validate(); err != nil {
		t.Fatalf("expected multiple domains to validate, got: %v", err)
	}
}

func TestAcmeValidateRejectsEmptyDomainEntry(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{""}
	res.Challenge = acmeChallengeDNS01
	res.Solver = "public-dns01"

	err := res.Validate()
	if err == nil || !strings.Contains(err.Error(), "domain must not be empty") {
		t.Fatalf("expected empty domain validation error, got: %v", err)
	}
}

func TestAcmeValidateRejectsWhitespaceOnlyDomainEntry(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{"   "}
	res.Challenge = acmeChallengeDNS01
	res.Solver = "public-dns01"

	err := res.Validate()
	if err == nil || !strings.Contains(err.Error(), "domain must not be empty") {
		t.Fatalf("expected whitespace-only domain validation error, got: %v", err)
	}
}

func TestAcmeValidateRejectsInvalidDomainWithSpaces(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{"invalid domain"}
	res.Challenge = acmeChallengeDNS01
	res.Solver = "public-dns01"

	err := res.Validate()
	if err == nil || !strings.Contains(err.Error(), "invalid domain") {
		t.Fatalf("expected invalid domain validation error, got: %v", err)
	}
}

func TestAcmeValidateAcceptsIDNADomainAndNormalizesIt(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{"bücher.example"}
	res.Challenge = acmeChallengeDNS01
	res.Solver = "public-dns01"

	if err := res.Validate(); err != nil {
		t.Fatalf("expected IDNA domain to validate, got: %v", err)
	}

	domains, err := res.desiredDomains()
	if err != nil {
		t.Fatalf("desiredDomains failed: %v", err)
	}
	if !reflect.DeepEqual(domains, []string{"xn--bcher-kva.example"}) {
		t.Fatalf("expected normalized IDNA domain, got: %#v", domains)
	}
}

func TestAcmeValidateAcceptsWildcardDomainAndNormalizesIt(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{"example.com", "*.bücher.example"}
	res.Challenge = acmeChallengeDNS01
	res.Solver = "public-dns01"

	if err := res.Validate(); err != nil {
		t.Fatalf("expected wildcard domains to validate, got: %v", err)
	}

	domains, err := res.desiredDomains()
	if err != nil {
		t.Fatalf("desiredDomains failed: %v", err)
	}
	if !reflect.DeepEqual(domains, []string{"example.com", "*.xn--bcher-kva.example"}) {
		t.Fatalf("expected normalized wildcard domains, got: %#v", domains)
	}
}

func TestAcmeValidateRejectsInvalidWildcardDomain(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{"*.*.example.com"}
	res.Challenge = acmeChallengeDNS01
	res.Solver = "public-dns01"

	err := res.Validate()
	if err == nil || !strings.Contains(err.Error(), "invalid wildcard") {
		t.Fatalf("expected invalid wildcard validation error, got: %v", err)
	}
}

func TestAcmeValidateRejectsMixedValidAndInvalidDomains(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{"valid.example", "invalid domain"}
	res.Challenge = acmeChallengeDNS01
	res.Solver = "public-dns01"

	err := res.Validate()
	if err == nil || !strings.Contains(err.Error(), "invalid domain") {
		t.Fatalf("expected mixed-domain validation error, got: %v", err)
	}
}

func TestNormalizeACMEDomainsRejectsMixedValidAndInvalidInput(t *testing.T) {
	domains, err := normalizeACMEDomains([]string{"valid.example", "invalid domain"})
	if err == nil || !strings.Contains(err.Error(), "invalid domain") {
		t.Fatalf("expected mixed-domain normalization error, got: %v", err)
	}
	if domains != nil {
		t.Fatalf("expected no normalized domains on error, got: %#v", domains)
	}
}

func TestAcmeValidateRejectsMissingDNS01Solver(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{"example.com"}
	res.Challenge = acmeChallengeDNS01

	err := res.Validate()
	if err == nil || !strings.Contains(err.Error(), "Solver") {
		t.Fatalf("expected solver validation error, got: %v", err)
	}
}

func TestAcmeValidateRejectsMissingHTTP01Solver(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.Account = "public-ca"
	res.Domains = []string{"example.com"}
	res.Challenge = acmeChallengeHTTP01

	err := res.Validate()
	if err == nil || !strings.Contains(err.Error(), "Solver") {
		t.Fatalf("expected solver validation error, got: %v", err)
	}
}

func TestAcmeCheckApplyWaitsForSharedAccountState(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	res := &AcmeRes{
		Account:   "public-ca",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeDNS01,
		Solver:    "public-dns01",
	}
	init, sent := fakeAcmeInit(t, false, nil)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState, account *acmeAccountData) (acmeClient, error) {
		t.Fatalf("clientFactory should not be called while account state is missing")
		return nil, nil
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false while waiting for account state")
	}

	payload := sent()
	if !payload.Pending {
		t.Fatalf("expected pending to be true while waiting for account state")
	}
	if payload.HTTP01Pending {
		t.Fatalf("expected http01_pending to be false while waiting for account data")
	}
}

func TestAcmeCheckApplyPreparesHTTP01BeforeReady(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	http01Ready := false
	world := newFakeWorld("test-host")
	mustStoreTestAcmeAccountState(t, world, "public-ca", "https://example.test/directory")

	orderState := &acmeHTTP01OrderState{
		Attempt:        "attempt-1",
		Domains:        []string{"example.com"},
		KeyType:        "2048",
		MustStaple:     false,
		PreferredChain: "",
		OrderURL:       "https://example.test/order/1",
		FinalizeURL:    "https://example.test/order/1/finalize",
		PrivateKeyPEM:  "PRIVATE KEY\n",
		CSRPEM:         "CSR\n",
		Challenges: map[string]acmeHTTP01Challenge{
			"challenge-1": {
				Attempt:      "attempt-1",
				Domain:       "example.com",
				Token:        "token",
				Path:         "/.well-known/acme-challenge/token",
				Body:         "key-authorization",
				ChallengeURL: "https://example.test/challenge/1",
			},
		},
	}
	client := &fakeAcmeClient{
		accountKeyPEM:     "ACCOUNT\n",
		registration:      &registration.Resource{URI: "https://example.test/acct/1"},
		beginHTTP01Result: orderState,
	}

	res := &AcmeRes{
		Account:   "public-ca",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeHTTP01,
		Solver:    "public-http01",
		Ready:     &http01Ready,
	}
	init, sent := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState, account *acmeAccountData) (acmeClient, error) {
		return client, nil
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false while preparing http-01")
	}
	if client.beginHTTP01Calls != 1 {
		t.Fatalf("expected one BeginHTTP01 call, got %d", client.beginHTTP01Calls)
	}
	if client.completeHTTP01Calls != 0 {
		t.Fatalf("expected zero CompleteHTTP01 calls, got %d", client.completeHTTP01Calls)
	}

	payload := sent()
	if !payload.Pending {
		t.Fatalf("expected pending to be true while preparing http-01")
	}
	if !payload.HTTP01Pending {
		t.Fatalf("expected http01_pending to be true while preparing http-01")
	}
	if payload.PrivateKey != "" {
		t.Fatalf("expected no private key material while waiting for issuance")
	}

	state, err := res.loadState()
	if err != nil {
		t.Fatalf("loadState failed: %v", err)
	}
	if state.HTTP01 == nil {
		t.Fatalf("expected pending HTTP-01 order state to be persisted")
	}

	challenges, err := loadAcmeHTTP01ChallengeState(context.Background(), world, res.Solver)
	if err != nil {
		t.Fatalf("loadAcmeHTTP01ChallengeState failed: %v", err)
	}
	if len(challenges.Challenges) != 1 {
		t.Fatalf("expected one published challenge, got %d", len(challenges.Challenges))
	}
}

func TestAcmeCheckApplyCompletesHTTP01WhenReadyAndPresented(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	http01Ready := true
	world := newFakeWorld("test-host")
	mustStoreTestAcmeAccountState(t, world, "public-ca", "https://example.test/directory")
	fullchain, leaf, issuer, key := mustTestCertificatePEM(t, now, now.Add(90*24*time.Hour))

	orderState := &acmeHTTP01OrderState{
		Attempt:        "attempt-1",
		Domains:        []string{"example.com"},
		KeyType:        "2048",
		MustStaple:     false,
		PreferredChain: "",
		OrderURL:       "https://example.test/order/1",
		FinalizeURL:    "https://example.test/order/1/finalize",
		PrivateKeyPEM:  "PRIVATE KEY\n",
		CSRPEM:         "CSR\n",
		Challenges: map[string]acmeHTTP01Challenge{
			"challenge-1": {
				Attempt:      "attempt-1",
				Domain:       "example.com",
				Token:        "token",
				Path:         "/.well-known/acme-challenge/token",
				Body:         "key-authorization",
				ChallengeURL: "https://example.test/challenge/1",
			},
		},
	}
	client := &fakeAcmeClient{
		accountKeyPEM: "ACCOUNT\n",
		registration:  &registration.Resource{URI: "https://example.test/acct/1"},
		completeHTTP01Result: &certificate.Resource{
			Domain:            "example.com",
			CertURL:           "https://example.test/cert/1",
			CertStableURL:     "https://example.test/cert/stable/1",
			PrivateKey:        []byte(key),
			Certificate:       []byte(fullchain),
			IssuerCertificate: []byte(issuer),
		},
	}

	res := &AcmeRes{
		Account:   "public-ca",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeHTTP01,
		Solver:    "public-http01",
		Ready:     &http01Ready,
	}
	init, sent := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState, account *acmeAccountData) (acmeClient, error) {
		return client, nil
	}

	if err := res.saveState(&acmeStoredState{
		Version:      1,
		DirectoryURL: "https://example.test/directory",
		Domains:      []string{"example.com"},
		KeyType:      "2048",
		HTTP01:       orderState,
	}); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}

	if err := storeAcmeHTTP01PresentationState(context.Background(), world, res.Solver, &acmeHTTP01PresentationState{
		Entries: map[string]acmeHTTP01PresentationEntry{
			"challenge-1": {
				Attempt: "attempt-1",
				Domain:  "example.com",
				Path:    "/.well-known/acme-challenge/token",
				Ready:   true,
			},
		},
	}); err != nil {
		t.Fatalf("storeAcmeHTTP01PresentationState failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false after completing http-01 in this cycle")
	}
	if client.completeHTTP01Calls != 1 {
		t.Fatalf("expected one CompleteHTTP01 call, got %d", client.completeHTTP01Calls)
	}

	payload := sent()
	if payload.Pending {
		t.Fatalf("expected pending to be false after successful issuance")
	}
	if payload.HTTP01Pending {
		t.Fatalf("expected http01_pending to be false after successful issuance")
	}
	if payload.Certificate != normalizePEMString(leaf) {
		t.Fatalf("unexpected certificate payload")
	}

	state, err := res.loadState()
	if err != nil {
		t.Fatalf("loadState failed: %v", err)
	}
	if state.HTTP01 != nil {
		t.Fatalf("expected pending HTTP-01 order state to be cleared")
	}
	if state.PrivateKeyPEM != normalizePEMString(key) {
		t.Fatalf("expected issued private key to be persisted")
	}

	challenges, err := loadAcmeHTTP01ChallengeState(context.Background(), world, res.Solver)
	if err != nil {
		t.Fatalf("loadAcmeHTTP01ChallengeState failed: %v", err)
	}
	if len(challenges.Challenges) != 0 {
		t.Fatalf("expected published challenges to be cleared")
	}
}

func TestAcmeCheckApplyCompletesHTTP01WithoutPresentationWhenAuthorizationAlreadyValid(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	http01Ready := false
	world := newFakeWorld("test-host")
	mustStoreTestAcmeAccountState(t, world, "public-ca", "https://example.test/directory")
	fullchain, leaf, issuer, key := mustTestCertificatePEM(t, now, now.Add(90*24*time.Hour))

	orderState := &acmeHTTP01OrderState{
		Attempt:        "attempt-1",
		Domains:        []string{"example.com"},
		KeyType:        "2048",
		MustStaple:     false,
		PreferredChain: "",
		OrderURL:       "https://example.test/order/1",
		FinalizeURL:    "https://example.test/order/1/finalize",
		PrivateKeyPEM:  "PRIVATE KEY\n",
		CSRPEM:         "CSR\n",
		Challenges:     map[string]acmeHTTP01Challenge{},
	}
	client := &fakeAcmeClient{
		accountKeyPEM: "ACCOUNT\n",
		registration:  &registration.Resource{URI: "https://example.test/acct/1"},
		completeHTTP01Result: &certificate.Resource{
			Domain:            "example.com",
			CertURL:           "https://example.test/cert/1",
			CertStableURL:     "https://example.test/cert/stable/1",
			PrivateKey:        []byte(key),
			Certificate:       []byte(fullchain),
			IssuerCertificate: []byte(issuer),
		},
	}

	res := &AcmeRes{
		Account:   "public-ca",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeHTTP01,
		Solver:    "public-http01",
		Ready:     &http01Ready,
	}
	init, sent := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState, account *acmeAccountData) (acmeClient, error) {
		return client, nil
	}

	if err := res.saveState(&acmeStoredState{
		Version:      1,
		DirectoryURL: "https://example.test/directory",
		Domains:      []string{"example.com"},
		KeyType:      "2048",
		HTTP01:       orderState,
	}); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false after completing http-01 in this cycle")
	}
	if client.completeHTTP01Calls != 1 {
		t.Fatalf("expected one CompleteHTTP01 call, got %d", client.completeHTTP01Calls)
	}

	payload := sent()
	if payload.Pending {
		t.Fatalf("expected pending to be false after successful issuance")
	}
	if payload.Certificate != normalizePEMString(leaf) {
		t.Fatalf("unexpected certificate payload")
	}

	challenges, err := loadAcmeHTTP01ChallengeState(context.Background(), world, res.Solver)
	if err != nil {
		t.Fatalf("loadAcmeHTTP01ChallengeState failed: %v", err)
	}
	if len(challenges.Challenges) != 0 {
		t.Fatalf("expected published challenges to stay empty")
	}
}

func TestSelectACMEChallengeSkipsAlreadyValidAuthorization(t *testing.T) {
	for _, challengeType := range []string{acmeChallengeHTTP01, acmeChallengeDNS01} {
		selected, alreadyValid, err := selectACMEChallenge(acme.Authorization{
			Status: acme.StatusValid,
			Identifier: acme.Identifier{
				Type:  "dns",
				Value: "example.com",
			},
			Challenges: []acme.Challenge{
				{Type: acmeChallengeHTTP01},
			},
		}, challengeType)
		if err != nil {
			t.Fatalf("unexpected error for %s authorization reuse: %v", challengeType, err)
		}
		if !alreadyValid {
			t.Fatalf("expected alreadyValid to be true for %s authorization reuse", challengeType)
		}
		if selected != nil {
			t.Fatalf("expected no challenge to be returned for %s authorization reuse", challengeType)
		}
	}
}

func TestAcmeCheckApplyDNS01PendingDoesNotSetHTTP01Pending(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	world := newFakeWorld("test-host")
	mustStoreTestAcmeAccountState(t, world, "public-ca", "https://example.test/directory")

	res := &AcmeRes{
		Account:   "public-ca",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeDNS01,
		Solver:    "public-dns01",
	}
	init, sent := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false while dns-01 issuance is pending")
	}

	payload := sent()
	if !payload.Pending {
		t.Fatalf("expected pending to be true during planning pass")
	}
	if payload.HTTP01Pending {
		t.Fatalf("expected http01_pending to be false for dns-01")
	}
}

func TestAcmeWatchIgnoresNoOpHTTP01PresentationWakeup(t *testing.T) {
	world := newFakeWorld("test-host")
	mustStoreTestAcmeAccountState(t, world, "public-ca", "https://example.test/directory")

	if err := storeAcmeHTTP01PresentationState(context.Background(), world, "public-http01", &acmeHTTP01PresentationState{
		Entries: map[string]acmeHTTP01PresentationEntry{
			"challenge-1": {
				Attempt: "attempt-1",
				Domain:  "example.com",
				Path:    "/.well-known/acme-challenge/token-1",
				Ready:   true,
			},
		},
	}); err != nil {
		t.Fatalf("storeAcmeHTTP01PresentationState failed: %v", err)
	}

	res := &AcmeRes{
		Account:   "public-ca",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeHTTP01,
		Solver:    "public-http01",
	}

	init, _ := fakeAcmeInit(t, false, world)
	events := make(chan struct{}, 4)
	init.Event = func(context.Context) error {
		events <- struct{}{}
		return nil
	}

	if err := res.Init(init); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- res.Watch(ctx)
	}()

	waitForFakeWorldStrWatchers(t, world, acmeAccountStateKey(res.Account), 1)
	waitForFakeWorldStrMapWatchers(t, world, acmeHTTP01PresentationStateKey(res.normalizedSolver()), 1)
	waitForTestEvent(t, events, "initial ACME watch event")

	sendFakeWorldStrMapWatchEvent(world, acmeHTTP01PresentationStateKey(res.normalizedSolver()))
	assertNoTestEvent(t, events, "ACME watch event after a no-op HTTP-01 presentation wakeup")

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

func mustTestCertificatePEM(t *testing.T, notBefore, notAfter time.Time) (fullchain string, leaf string, issuer string, privateKey string) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             notBefore.Add(-time.Hour),
		NotAfter:              notAfter.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	leafTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "example.com"},
		DNSNames:              []string{"example.com"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caTemplate, leafKey.Public(), caKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	leafKeyPEM, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}

	return string(append(append([]byte{}, leafPEM...), caPEM...)), string(leafPEM), string(caPEM), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyPEM}))
}
