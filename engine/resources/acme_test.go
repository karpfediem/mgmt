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
	"strings"
	"testing"
	"time"

	"github.com/purpleidea/mgmt/engine"

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
	accountKeyPEM  string
	registration   *registration.Resource
	obtainResult   *certificate.Resource
	renewResult    *certificate.Resource
	beginResult    *acmeHTTP01OrderState
	completeResult *certificate.Resource

	ensureCalls   int
	obtainCalls   int
	renewCalls    int
	beginCalls    int
	completeCalls int
}

func (obj *fakeAcmeClient) AccountKeyPEM() string {
	return obj.accountKeyPEM
}

func (obj *fakeAcmeClient) EnsureRegistration(emailChanged, acceptTOS bool) (*registration.Resource, error) {
	obj.ensureCalls++
	return obj.registration, nil
}

func (obj *fakeAcmeClient) Obtain(req certificate.ObtainRequest) (*certificate.Resource, error) {
	obj.obtainCalls++
	return obj.obtainResult, nil
}

func (obj *fakeAcmeClient) Renew(certRes certificate.Resource, opts *certificate.RenewOptions) (*certificate.Resource, error) {
	obj.renewCalls++
	return obj.renewResult, nil
}

func (obj *fakeAcmeClient) BeginHTTP01(req acmeHTTP01IssueRequest) (*acmeHTTP01OrderState, error) {
	obj.beginCalls++
	return obj.beginResult, nil
}

func (obj *fakeAcmeClient) CompleteHTTP01(orderState *acmeHTTP01OrderState) (*certificate.Resource, error) {
	obj.completeCalls++
	return obj.completeResult, nil
}

func TestAcmeCheckApplyObtainsAndSendsPEM(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	fullchain, leaf, issuer, key := mustTestCertificatePEM(t, now, now.Add(90*24*time.Hour))

	client := &fakeAcmeClient{
		accountKeyPEM: "ACCOUNT\n",
		registration:  &registration.Resource{URI: "https://example.test/acct/1"},
		obtainResult: &certificate.Resource{
			Domain:            "example.com",
			CertURL:           "https://example.test/cert/1",
			CertStableURL:     "https://example.test/cert/stable/1",
			PrivateKey:        []byte(key),
			Certificate:       []byte(fullchain),
			IssuerCertificate: []byte(issuer),
		},
	}

	res := &AcmeRes{
		AcceptTOS: true,
		Email:     "ops@example.com",
		Domains:   []string{"example.com", "www.example.com"},
		Challenge: acmeChallengeDNS01,
	}
	init, sent := fakeAcmeInit(t, false, nil)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState) (acmeClient, error) {
		return client, nil
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false after issuing a certificate")
	}
	if client.obtainCalls != 1 {
		t.Fatalf("expected one obtain call, got %d", client.obtainCalls)
	}
	if client.renewCalls != 0 {
		t.Fatalf("expected zero renew calls, got %d", client.renewCalls)
	}

	payload := sent()
	if payload.Pending {
		t.Fatalf("expected pending to be false after successful issuance")
	}
	if payload.HTTP01Pending {
		t.Fatalf("expected http01_pending to be false after successful issuance")
	}
	if payload.PrivateKey != normalizePEMString(key) {
		t.Fatalf("unexpected private key payload")
	}
	if payload.Certificate != normalizePEMString(leaf) {
		t.Fatalf("unexpected certificate payload")
	}
	if payload.FullChain != normalizePEMString(fullchain) {
		t.Fatalf("unexpected fullchain payload")
	}
	if payload.NotAfter != now.Add(90*24*time.Hour).Unix() {
		t.Fatalf("unexpected expiry timestamp: %d", payload.NotAfter)
	}
}

func TestAcmeCheckApplyRenewsNearExpiry(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	currentFullchain, _, currentIssuer, currentKey := mustTestCertificatePEM(t, now.Add(-29*24*time.Hour), now.Add(20*24*time.Hour))
	nextFullchain, nextLeaf, nextIssuer, nextKey := mustTestCertificatePEM(t, now, now.Add(90*24*time.Hour))

	client := &fakeAcmeClient{
		accountKeyPEM: "ACCOUNT\n",
		registration:  &registration.Resource{URI: "https://example.test/acct/1"},
		renewResult: &certificate.Resource{
			Domain:            "example.com",
			CertURL:           "https://example.test/cert/2",
			CertStableURL:     "https://example.test/cert/stable/2",
			PrivateKey:        []byte(nextKey),
			Certificate:       []byte(nextFullchain),
			IssuerCertificate: []byte(nextIssuer),
		},
	}

	res := &AcmeRes{
		AcceptTOS:       true,
		Email:           "ops@example.com",
		Domains:         []string{"example.com"},
		Challenge:       acmeChallengeDNS01,
		RenewBeforeDays: 30,
	}
	init, sent := fakeAcmeInit(t, false, nil)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState) (acmeClient, error) {
		return client, nil
	}

	state := &acmeStoredState{
		Version:              1,
		Email:                "ops@example.com",
		DirectoryURL:         res.DirectoryURL,
		Domains:              []string{"example.com"},
		KeyType:              "2048",
		AccountPrivateKeyPEM: "ACCOUNT\n",
		Registration:         &registration.Resource{URI: "https://example.test/acct/1"},
		Domain:               "example.com",
		CertURL:              "https://example.test/cert/1",
		CertStableURL:        "https://example.test/cert/stable/1",
		PrivateKeyPEM:        normalizePEMString(currentKey),
		CertificatePEM:       normalizePEMString(currentFullchain),
		IssuerCertificatePEM: normalizePEMString(currentIssuer),
	}
	if err := res.saveState(state); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false after renewing a certificate")
	}
	if client.renewCalls != 1 {
		t.Fatalf("expected one renew call, got %d", client.renewCalls)
	}

	payload := sent()
	if payload.Pending {
		t.Fatalf("expected pending to be false after successful renewal")
	}
	if payload.HTTP01Pending {
		t.Fatalf("expected http01_pending to be false after successful renewal")
	}
	if payload.Certificate != normalizePEMString(nextLeaf) {
		t.Fatalf("unexpected renewed certificate payload")
	}
	if payload.FullChain != normalizePEMString(nextFullchain) {
		t.Fatalf("unexpected renewed fullchain payload")
	}
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
	res.AcceptTOS = true
	res.Email = "ops@example.com"
	res.Domains = []string{"example.com"}

	err := res.Validate()
	if err == nil || !strings.Contains(err.Error(), "Challenge") {
		t.Fatalf("expected challenge validation error, got: %v", err)
	}
}

func TestAcmeValidateAcceptsDNSChallengeWithProviderEnv(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.AcceptTOS = true
	res.Email = "ops@example.com"
	res.Domains = []string{"example.com"}
	res.Challenge = acmeChallengeDNS01
	res.DNSProvider = "cloudflare"
	res.DNSEnv = map[string]string{
		"CLOUDFLARE_DNS_API_TOKEN": "token",
	}

	if err := res.Validate(); err != nil {
		t.Fatalf("expected DNS challenge config to validate, got: %v", err)
	}
}

func TestAcmeValidateRejectsMissingDNSProviderEnv(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.AcceptTOS = true
	res.Email = "ops@example.com"
	res.Domains = []string{"example.com"}
	res.Challenge = acmeChallengeDNS01
	res.DNSProvider = "cloudflare"

	err := res.Validate()
	if err == nil || !strings.Contains(err.Error(), "DNSEnv") {
		t.Fatalf("expected dns env validation error, got: %v", err)
	}
}

func TestAcmeValidateRejectsMissingHTTP01Solver(t *testing.T) {
	res := (&AcmeRes{}).Default().(*AcmeRes)
	res.AcceptTOS = true
	res.Email = "ops@example.com"
	res.Domains = []string{"example.com"}
	res.Challenge = acmeChallengeHTTP01

	err := res.Validate()
	if err == nil || !strings.Contains(err.Error(), "Solver") {
		t.Fatalf("expected solver validation error, got: %v", err)
	}
}

func TestAcmeCheckApplyPreparesHTTP01BeforeReady(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	http01Ready := false
	world := newFakeWorld("test-host")

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
		beginResult:   orderState,
	}

	res := &AcmeRes{
		AcceptTOS:   true,
		Email:       "ops@example.com",
		Domains:     []string{"example.com"},
		Challenge:   acmeChallengeHTTP01,
		Solver:      "public-http01",
		HTTP01Ready: &http01Ready,
	}
	init, sent := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState) (acmeClient, error) {
		return client, nil
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false while preparing http-01")
	}
	if client.beginCalls != 1 {
		t.Fatalf("expected one BeginHTTP01 call, got %d", client.beginCalls)
	}
	if client.completeCalls != 0 {
		t.Fatalf("expected zero CompleteHTTP01 calls, got %d", client.completeCalls)
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
		completeResult: &certificate.Resource{
			Domain:            "example.com",
			CertURL:           "https://example.test/cert/1",
			CertStableURL:     "https://example.test/cert/stable/1",
			PrivateKey:        []byte(key),
			Certificate:       []byte(fullchain),
			IssuerCertificate: []byte(issuer),
		},
	}

	res := &AcmeRes{
		AcceptTOS:   true,
		Email:       "ops@example.com",
		Domains:     []string{"example.com"},
		Challenge:   acmeChallengeHTTP01,
		Solver:      "public-http01",
		HTTP01Ready: &http01Ready,
	}
	init, sent := fakeAcmeInit(t, false, world)
	if err := res.Init(init); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.nowFn = func() time.Time { return now }
	res.clientFactory = func(state *acmeStoredState) (acmeClient, error) {
		return client, nil
	}

	if err := res.saveState(&acmeStoredState{
		Version:              1,
		Email:                "ops@example.com",
		DirectoryURL:         res.DirectoryURL,
		Domains:              []string{"example.com"},
		KeyType:              "2048",
		AccountPrivateKeyPEM: "ACCOUNT\n",
		Registration:         &registration.Resource{URI: "https://example.test/acct/1"},
		HTTP01:               orderState,
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
	if client.completeCalls != 1 {
		t.Fatalf("expected one CompleteHTTP01 call, got %d", client.completeCalls)
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

func TestAcmeCheckApplyDNS01PendingDoesNotSetHTTP01Pending(t *testing.T) {
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	res := &AcmeRes{
		AcceptTOS: true,
		Email:     "ops@example.com",
		Domains:   []string{"example.com"},
		Challenge: acmeChallengeDNS01,
	}
	init, sent := fakeAcmeInit(t, false, nil)
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
