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
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/go-acme/lego/v4/acme"
	"github.com/go-acme/lego/v4/acme/api"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	legodns01 "github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/platform/wait"
	"github.com/go-acme/lego/v4/registration"
	"golang.org/x/net/idna"
)

func init() {
	engine.RegisterResource("acme:request", func() engine.Res { return &AcmeRes{} })
}

var acmeIDNAProfile = idna.New(
	idna.MapForLookup(),
	idna.BidiRule(),
	idna.VerifyDNSLength(true),
)

const (
	acmeStateFilename          = "state.json"
	acmeDefaultRenewBeforeDays = 30
	acmeDefaultKeyType         = "rsa2048"
	acmeImmediateRetryDelay    = time.Minute
	acmeChallengeHTTP01        = "http-01"
	acmeChallengeDNS01         = "dns-01"
)

// AcmeRes manages an ACME certificate request using explicit ACME challenge
// configuration and account state loaded from acme:account.
type AcmeRes struct {
	traits.Base
	traits.Refreshable
	traits.Sendable

	init *engine.Init

	// Account is the logical ACME account name this request expects.
	Account string `lang:"account" yaml:"account"`

	// Domains is the ordered list of domains for the requested certificate.
	// If omitted, the resource name is used as a single-domain certificate.
	Domains []string `lang:"domains" yaml:"domains"`

	// Challenge selects how the ACME authorization is solved.
	// Supported values are: http-01, dns-01.
	Challenge string `lang:"challenge" yaml:"challenge"`

	// Solver selects the explicit ACME solver resource that presents the chosen
	// challenge type.
	Solver string `lang:"solver" yaml:"solver"`

	// KeyType is the certificate private key type. Supported values are:
	// rsa2048, rsa3072, rsa4096, rsa8192, ec256, ec384.
	KeyType string `lang:"key_type" yaml:"key_type"`

	// RenewBeforeDays specifies how many days before expiry the certificate
	// should be renewed automatically.
	RenewBeforeDays uint16 `lang:"renew_before_days" yaml:"renew_before_days"`

	// MustStaple requests the OCSP Must Staple extension.
	MustStaple bool `lang:"must_staple" yaml:"must_staple"`

	// PreferredChain requests a particular issuer chain when the CA supports it.
	PreferredChain string `lang:"preferred_chain" yaml:"preferred_chain"`

	// Ready optionally gates when a pending challenge attempt may be validated
	// with the CA. A nil value means no external gate is in use.
	Ready *bool `lang:"ready" yaml:"ready"`

	varDir    string
	statePath string

	nowFn         func() time.Time
	clientFactory func(*acmeStoredState, *acmeAccountData) (acmeClient, error)

	scheduleMu sync.Mutex
	scheduleAt *time.Time
	scheduleCh chan struct{}
	recheckCh  chan struct{}
}

// Default returns some sensible defaults for this resource.
func (obj *AcmeRes) Default() engine.Res {
	return &AcmeRes{
		KeyType:         acmeDefaultKeyType,
		RenewBeforeDays: acmeDefaultRenewBeforeDays,
	}
}

// Validate if the params passed in are valid data.
func (obj *AcmeRes) Validate() error {
	if strings.TrimSpace(obj.Account) == "" {
		return fmt.Errorf("the Account field must not be empty")
	}
	switch obj.normalizedChallenge() {
	case acmeChallengeHTTP01:
	case acmeChallengeDNS01:
	default:
		return fmt.Errorf("the Challenge field must be one of %q or %q", acmeChallengeHTTP01, acmeChallengeDNS01)
	}
	if obj.normalizedSolver() == "" {
		return fmt.Errorf("the Solver field must not be empty")
	}
	if _, err := obj.certificateKeyType(); err != nil {
		return err
	}
	if _, err := obj.desiredDomains(); err != nil {
		return err
	}
	return nil
}

// Init runs some startup code for this resource.
func (obj *AcmeRes) Init(init *engine.Init) error {
	obj.init = init

	dir, err := obj.init.VarDir("")
	if err != nil {
		return errwrap.Wrapf(err, "could not get VarDir in Init()")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errwrap.Wrapf(err, "could not create VarDir")
	}

	obj.varDir = dir
	obj.statePath = path.Join(dir, acmeStateFilename)

	if obj.nowFn == nil {
		obj.nowFn = time.Now
	}
	if obj.clientFactory == nil {
		obj.clientFactory = obj.newLegoClient
	}
	if obj.scheduleCh == nil {
		obj.scheduleCh = make(chan struct{}, 1)
	}
	if obj.recheckCh == nil {
		obj.recheckCh = make(chan struct{}, 1)
	}

	return nil
}

// Cleanup is run by the engine to clean up after the resource is done.
func (obj *AcmeRes) Cleanup() error {
	switch obj.normalizedChallenge() {
	case acmeChallengeHTTP01:
		_ = obj.clearHTTP01Challenges(context.Background())
	case acmeChallengeDNS01:
		_ = obj.clearDNS01Challenges(context.Background())
	}
	return nil
}

type acmeWatchSnapshot struct {
	account     *acmeAccountStoredState
	http01State map[string]acmeHTTP01PresentationState
	dns01State  *acmeDNS01PresentationState
}

func (obj *AcmeRes) watchSnapshot(ctx context.Context) (*acmeWatchSnapshot, error) {
	snapshot := &acmeWatchSnapshot{}

	accountState, err := loadAcmeAccountSharedState(ctx, obj.init.World, obj.Account)
	if err != nil {
		return nil, err
	}
	snapshot.account = accountState

	switch obj.normalizedChallenge() {
	case acmeChallengeHTTP01:
		http01State, err := loadAcmeHTTP01PresentationStates(ctx, obj.init.World, obj.normalizedSolver())
		if err != nil {
			return nil, err
		}
		snapshot.http01State = http01State
	case acmeChallengeDNS01:
		dns01State, err := loadAcmeDNS01PresentationState(ctx, obj.init.World, obj.normalizedSolver())
		if err != nil {
			return nil, err
		}
		snapshot.dns01State = dns01State
	}

	return snapshot, nil
}

// Watch is the primary listener for this resource and it outputs events.
func (obj *AcmeRes) Watch(ctx context.Context) error {
	var accountCh chan error
	ch, err := obj.init.World.StrWatch(ctx, acmeAccountStateKey(obj.Account))
	if err != nil {
		return err
	}
	accountCh = ch

	var presentationCh chan error
	switch obj.normalizedChallenge() {
	case acmeChallengeHTTP01:
		ch, err := obj.init.World.StrMapWatch(ctx, acmeHTTP01PresentationStateKey(obj.normalizedSolver()))
		if err != nil {
			return err
		}
		presentationCh = ch
	case acmeChallengeDNS01:
		ch, err := obj.init.World.StrWatch(ctx, acmeDNS01PresentationStateKey(obj.normalizedSolver()))
		if err != nil {
			return err
		}
		presentationCh = ch
	}

	snapshot, err := obj.watchSnapshot(ctx)
	if err != nil {
		return err
	}

	if err := obj.init.Event(ctx); err != nil {
		return err
	}

	for {
		timer := obj.nextScheduleTimer()

		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return nil

		case <-obj.scheduleCh:
			if timer != nil {
				timer.Stop()
			}
			continue

		case <-obj.recheckCh:
			if timer != nil {
				timer.Stop()
			}

		case err, ok := <-accountCh:
			if timer != nil {
				timer.Stop()
			}
			if !ok {
				return nil
			}
			if err != nil {
				return err
			}
			nextSnapshot, err := obj.watchSnapshot(ctx)
			if err != nil {
				return err
			}
			if reflect.DeepEqual(snapshot, nextSnapshot) {
				continue
			}
			snapshot = nextSnapshot

		case err, ok := <-presentationCh:
			if timer != nil {
				timer.Stop()
			}
			if !ok {
				return nil
			}
			if err != nil {
				return err
			}
			nextSnapshot, err := obj.watchSnapshot(ctx)
			if err != nil {
				return err
			}
			if reflect.DeepEqual(snapshot, nextSnapshot) {
				continue
			}
			snapshot = nextSnapshot

		case <-timerChan(timer):
			obj.init.Logf("certificate renewal window reached")
		}

		if err := obj.init.Event(ctx); err != nil {
			return err
		}
	}
}

// CheckApply ensures the local ACME state is present and renewed when needed.
func (obj *AcmeRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	refresh := obj.init.Refresh()
	now := obj.now()

	state, err := obj.loadState()
	if err != nil {
		return false, errwrap.Wrapf(err, "could not load ACME state")
	}
	if state == nil {
		state = &acmeStoredState{}
	}

	accountData, err := obj.loadAccountData(ctx)
	if err != nil {
		return false, errwrap.Wrapf(err, "could not load ACME account state")
	}

	plan, err := obj.planState(state, refresh, now, accountData)
	if err != nil {
		return false, err
	}

	if plan.needsApply && !apply {
		obj.updateSchedule(state, now)
		if err := obj.init.Send(obj.buildSends(state, plan)); err != nil {
			return false, err
		}
		return false, nil
	}

	if plan.needsApply {
		if accountData == nil {
			obj.init.Logf("waiting for %s[%s] before reconciling the certificate request", "acme:account", obj.Account)
			obj.updateSchedule(state, now)
			if err := obj.init.Send(obj.buildSends(state, plan)); err != nil {
				return false, err
			}
			return false, nil
		}
		if err := obj.init.Send(obj.buildSends(state, plan)); err != nil {
			return false, err
		}
		obj.init.Logf("reconciling ACME state: %s", plan.reason)
		nextState, err := obj.reconcileState(state, plan, accountData)
		if nextState != nil {
			state = nextState
			if err := obj.saveState(state); err != nil {
				return false, errwrap.Wrapf(err, "could not save ACME state")
			}
			if err == nil {
				obj.requestImmediateRecheck()
			}
		}
		if err != nil {
			return false, err
		}
	}

	obj.updateSchedule(state, now)

	if err := obj.init.Send(obj.buildSends(state, nil)); err != nil {
		return false, err
	}

	if plan.needsApply {
		return false, nil
	}
	return true, nil
}

// Cmp compares two resources and returns an error if they are not equivalent.
func (obj *AcmeRes) Cmp(r engine.Res) error {
	res, ok := r.(*AcmeRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}

	if obj.Account != res.Account {
		return fmt.Errorf("the Account field differs")
	}
	objDomains, err := obj.desiredDomains()
	if err != nil {
		return err
	}
	resDomains, err := res.desiredDomains()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(objDomains, resDomains) {
		return fmt.Errorf("the Domains field differs")
	}
	if obj.normalizedChallenge() != res.normalizedChallenge() {
		return fmt.Errorf("the Challenge field differs")
	}
	if obj.normalizedSolver() != res.normalizedSolver() {
		return fmt.Errorf("the Solver field differs")
	}
	if !strings.EqualFold(obj.KeyType, res.KeyType) {
		return fmt.Errorf("the KeyType field differs")
	}
	if obj.RenewBeforeDays != res.RenewBeforeDays {
		return fmt.Errorf("the RenewBeforeDays field differs")
	}
	if obj.MustStaple != res.MustStaple {
		return fmt.Errorf("the MustStaple field differs")
	}
	if obj.PreferredChain != res.PreferredChain {
		return fmt.Errorf("the PreferredChain field differs")
	}
	if !reflect.DeepEqual(obj.Ready, res.Ready) {
		return fmt.Errorf("the Ready field differs")
	}

	return nil
}

// AcmeUID is the UID struct for AcmeRes.
type AcmeUID struct {
	engine.BaseUID

	name string
}

// UIDs includes all params to make a unique identification of this object.
func (obj *AcmeRes) UIDs() []engine.ResUID {
	x := &AcmeUID{
		BaseUID: engine.BaseUID{Name: obj.Name(), Kind: obj.Kind()},
		name:    obj.Name(),
	}
	return []engine.ResUID{x}
}

// AcmeSends is the PEM material and metadata exposed over send/recv.
type AcmeSends struct {
	Domain            string `lang:"domain"`
	CertURL           string `lang:"cert_url"`
	CertStableURL     string `lang:"cert_stable_url"`
	PrivateKey        string `lang:"private_key"`
	Certificate       string `lang:"certificate"`
	IssuerCertificate string `lang:"issuer_certificate"`
	FullChain         string `lang:"fullchain"`
	NotBefore         int64  `lang:"not_before"`
	NotAfter          int64  `lang:"not_after"`
	Pending           bool   `lang:"pending"`
	HTTP01Pending     bool   `lang:"http01_pending"`
}

// Sends represents the default struct of values we can send using Send/Recv.
func (obj *AcmeRes) Sends() interface{} {
	return &AcmeSends{}
}

// UnmarshalYAML is the custom unmarshal handler for this struct.
func (obj *AcmeRes) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawRes AcmeRes

	def := obj.Default()
	res, ok := def.(*AcmeRes)
	if !ok {
		return fmt.Errorf("could not convert to AcmeRes")
	}
	raw := rawRes(*res)

	if err := unmarshal(&raw); err != nil {
		return err
	}

	*obj = AcmeRes(raw)
	return nil
}

type acmePlan struct {
	needsApply bool
	prepare    bool
	complete   bool
	reason     string
}

type acmeStoredState struct {
	Version              int                   `json:"version"`
	DirectoryURL         string                `json:"directory_url"`
	Domains              []string              `json:"domains"`
	KeyType              string                `json:"key_type"`
	MustStaple           bool                  `json:"must_staple"`
	PreferredChain       string                `json:"preferred_chain"`
	Domain               string                `json:"domain"`
	CertURL              string                `json:"cert_url"`
	CertStableURL        string                `json:"cert_stable_url"`
	PrivateKeyPEM        string                `json:"private_key_pem"`
	CertificatePEM       string                `json:"certificate_pem"`
	IssuerCertificatePEM string                `json:"issuer_certificate_pem"`
	CSRPEM               string                `json:"csr_pem"`
	HTTP01               *acmeHTTP01OrderState `json:"http01,omitempty"`
	DNS01                *acmeDNS01OrderState  `json:"dns01,omitempty"`
}

type acmeHTTP01IssueRequest struct {
	Domains        []string
	MustStaple     bool
	PreferredChain string
}

type acmeHTTP01OrderState struct {
	Attempt        string                         `json:"attempt"`
	Domains        []string                       `json:"domains"`
	KeyType        string                         `json:"key_type"`
	MustStaple     bool                           `json:"must_staple"`
	PreferredChain string                         `json:"preferred_chain"`
	OrderURL       string                         `json:"order_url"`
	FinalizeURL    string                         `json:"finalize_url"`
	PrivateKeyPEM  string                         `json:"private_key_pem"`
	CSRPEM         string                         `json:"csr_pem"`
	Challenges     map[string]acmeHTTP01Challenge `json:"challenges"`
}

type acmeDNS01IssueRequest struct {
	Domains        []string
	MustStaple     bool
	PreferredChain string
}

type acmeDNS01OrderState struct {
	Attempt        string                        `json:"attempt"`
	Domains        []string                      `json:"domains"`
	KeyType        string                        `json:"key_type"`
	MustStaple     bool                          `json:"must_staple"`
	PreferredChain string                        `json:"preferred_chain"`
	OrderURL       string                        `json:"order_url"`
	FinalizeURL    string                        `json:"finalize_url"`
	PrivateKeyPEM  string                        `json:"private_key_pem"`
	CSRPEM         string                        `json:"csr_pem"`
	Challenges     map[string]acmeDNS01Challenge `json:"challenges"`
}

func (obj *acmeHTTP01OrderState) clone() *acmeHTTP01OrderState {
	if obj == nil {
		return nil
	}

	clone := *obj
	clone.Domains = append([]string(nil), obj.Domains...)
	if obj.Challenges != nil {
		clone.Challenges = make(map[string]acmeHTTP01Challenge, len(obj.Challenges))
		for key, challenge := range obj.Challenges {
			clone.Challenges[key] = challenge
		}
	}
	return &clone
}

func (obj *acmeDNS01OrderState) clone() *acmeDNS01OrderState {
	if obj == nil {
		return nil
	}

	clone := *obj
	clone.Domains = append([]string(nil), obj.Domains...)
	if obj.Challenges != nil {
		clone.Challenges = make(map[string]acmeDNS01Challenge, len(obj.Challenges))
		for key, challenge := range obj.Challenges {
			clone.Challenges[key] = challenge
		}
	}
	return &clone
}

func (obj *acmeHTTP01OrderState) matches(domains []string, keyType string, mustStaple bool, preferredChain string) bool {
	if obj == nil {
		return false
	}
	if !stringSliceEqual(obj.Domains, domains) {
		return false
	}
	if !strings.EqualFold(obj.KeyType, keyType) {
		return false
	}
	if obj.MustStaple != mustStaple {
		return false
	}
	if obj.PreferredChain != preferredChain {
		return false
	}
	return true
}

func (obj *acmeDNS01OrderState) matches(domains []string, keyType string, mustStaple bool, preferredChain string) bool {
	if obj == nil {
		return false
	}
	if !stringSliceEqual(obj.Domains, domains) {
		return false
	}
	if !strings.EqualFold(obj.KeyType, keyType) {
		return false
	}
	if obj.MustStaple != mustStaple {
		return false
	}
	if obj.PreferredChain != preferredChain {
		return false
	}
	return true
}

func (obj *acmeStoredState) clone() *acmeStoredState {
	if obj == nil {
		return &acmeStoredState{}
	}
	clone := *obj
	clone.Domains = append([]string(nil), obj.Domains...)
	clone.HTTP01 = obj.HTTP01.clone()
	clone.DNS01 = obj.DNS01.clone()
	return &clone
}

func (obj *acmeStoredState) hasCertificateMaterial() bool {
	return strings.TrimSpace(obj.PrivateKeyPEM) != "" && strings.TrimSpace(obj.CertificatePEM) != ""
}

func (obj *acmeStoredState) hasPendingChallenge() bool {
	return obj != nil && (obj.HTTP01 != nil || obj.DNS01 != nil)
}

func (obj *acmeStoredState) leafCert() (*x509.Certificate, error) {
	certificates, err := certcrypto.ParsePEMBundle([]byte(obj.CertificatePEM))
	if err != nil {
		return nil, err
	}
	if len(certificates) == 0 {
		return nil, fmt.Errorf("certificate bundle is empty")
	}
	return certificates[0], nil
}

func (obj *acmeStoredState) certResource() certificate.Resource {
	return certificate.Resource{
		Domain:            obj.Domain,
		CertURL:           obj.CertURL,
		CertStableURL:     obj.CertStableURL,
		PrivateKey:        []byte(obj.PrivateKeyPEM),
		Certificate:       []byte(obj.CertificatePEM),
		IssuerCertificate: []byte(obj.IssuerCertificatePEM),
		CSR:               []byte(obj.CSRPEM),
	}
}

func (obj *acmeStoredState) setCertificateResource(res *certificate.Resource) {
	obj.Domain = res.Domain
	obj.CertURL = res.CertURL
	obj.CertStableURL = res.CertStableURL
	obj.PrivateKeyPEM = normalizePEMString(string(res.PrivateKey))
	obj.CertificatePEM = normalizePEMString(string(res.Certificate))
	obj.IssuerCertificatePEM = normalizePEMString(string(res.IssuerCertificate))
	obj.CSRPEM = normalizePEMString(string(res.CSR))
}

type acmeClient interface {
	AccountKeyPEM() string
	EnsureRegistration(emailChanged, acceptTOS bool) (*registration.Resource, error)
	BeginHTTP01(acmeHTTP01IssueRequest) (*acmeHTTP01OrderState, error)
	CompleteHTTP01(*acmeHTTP01OrderState) (*certificate.Resource, error)
	BeginDNS01(acmeDNS01IssueRequest) (*acmeDNS01OrderState, error)
	CompleteDNS01(*acmeDNS01OrderState) (*certificate.Resource, error)
}

type legoAcmeClient struct {
	core          *api.Core
	registration  *registration.Registrar
	accountKeyPEM string
	user          *legoAcmeUser
	keyType       certcrypto.KeyType
}

func (obj *legoAcmeClient) AccountKeyPEM() string {
	return obj.accountKeyPEM
}

func (obj *legoAcmeClient) EnsureRegistration(emailChanged, acceptTOS bool) (*registration.Resource, error) {
	opts := registration.RegisterOptions{TermsOfServiceAgreed: acceptTOS}
	if obj.user.GetRegistration() == nil {
		reg, err := obj.registration.Register(opts)
		if err != nil {
			return nil, err
		}
		obj.user.registration = reg
		return reg, nil
	}
	if emailChanged {
		reg, err := obj.registration.UpdateRegistration(opts)
		if err != nil {
			return nil, err
		}
		obj.user.registration = reg
		return reg, nil
	}
	return obj.user.GetRegistration(), nil
}

func (obj *legoAcmeClient) BeginHTTP01(request acmeHTTP01IssueRequest) (*acmeHTTP01OrderState, error) {
	domains, err := normalizeACMEDomains(request.Domains)
	if err != nil {
		return nil, err
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("no domains to obtain a certificate for")
	}

	order, err := obj.core.Orders.New(domains)
	if err != nil {
		return nil, err
	}

	privateKey, err := certcrypto.GeneratePrivateKey(obj.keyType)
	if err != nil {
		return nil, err
	}

	commonName := ""
	if len(domains[0]) <= 64 {
		commonName = domains[0]
	}

	san := []string{}
	if commonName != "" {
		san = append(san, commonName)
	}
	for _, domain := range domains {
		if domain != commonName {
			san = append(san, domain)
		}
	}

	csrDER, err := certcrypto.CreateCSR(privateKey, certcrypto.CSROptions{
		Domain:     commonName,
		SAN:        san,
		MustStaple: request.MustStaple,
	})
	if err != nil {
		return nil, err
	}

	challenges := make(map[string]acmeHTTP01Challenge, len(order.Authorizations))
	attempt := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	for _, authzURL := range order.Authorizations {
		authz, err := obj.core.Authorizations.Get(authzURL)
		if err != nil {
			return nil, err
		}

		selected, alreadyValid, err := selectACMEChallenge(authz, acmeChallengeHTTP01)
		if err != nil {
			return nil, err
		}
		if alreadyValid {
			continue
		}

		keyAuthorization, err := obj.core.GetKeyAuthorization(selected.Token)
		if err != nil {
			return nil, err
		}

		challenge := acmeHTTP01Challenge{
			Attempt:      attempt,
			Domain:       authz.Identifier.Value,
			Token:        selected.Token,
			Path:         "/.well-known/acme-challenge/" + selected.Token,
			Body:         keyAuthorization,
			ChallengeURL: selected.URL,
		}
		challenges[challenge.key()] = challenge
	}

	return &acmeHTTP01OrderState{
		Attempt:        attempt,
		Domains:        append([]string(nil), domains...),
		KeyType:        string(obj.keyType),
		MustStaple:     request.MustStaple,
		PreferredChain: request.PreferredChain,
		OrderURL:       order.Location,
		FinalizeURL:    order.Finalize,
		PrivateKeyPEM:  normalizePEMString(string(certcrypto.PEMEncode(privateKey))),
		CSRPEM:         normalizePEMString(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}))),
		Challenges:     challenges,
	}, nil
}

func (obj *legoAcmeClient) BeginDNS01(request acmeDNS01IssueRequest) (*acmeDNS01OrderState, error) {
	domains, err := normalizeACMEDomains(request.Domains)
	if err != nil {
		return nil, err
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("no domains to obtain a certificate for")
	}

	order, privateKeyPEM, csrPEM, err := obj.beginChallengeOrder(domains, request.MustStaple)
	if err != nil {
		return nil, err
	}

	challenges := make(map[string]acmeDNS01Challenge, len(order.Authorizations))
	attempt := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	for _, authzURL := range order.Authorizations {
		authz, err := obj.core.Authorizations.Get(authzURL)
		if err != nil {
			return nil, err
		}

		selected, alreadyValid, err := selectACMEChallenge(authz, acmeChallengeDNS01)
		if err != nil {
			return nil, err
		}
		if alreadyValid {
			continue
		}

		keyAuthorization, err := obj.core.GetKeyAuthorization(selected.Token)
		if err != nil {
			return nil, err
		}

		info := legodns01.GetChallengeInfo(authz.Identifier.Value, keyAuthorization)
		challenge := acmeDNS01Challenge{
			Attempt:          attempt,
			Domain:           authz.Identifier.Value,
			Token:            selected.Token,
			KeyAuthorization: keyAuthorization,
			FQDN:             info.EffectiveFQDN,
			Value:            info.Value,
			ChallengeURL:     selected.URL,
		}
		challenges[challenge.key()] = challenge
	}

	return &acmeDNS01OrderState{
		Attempt:        attempt,
		Domains:        append([]string(nil), domains...),
		KeyType:        string(obj.keyType),
		MustStaple:     request.MustStaple,
		PreferredChain: request.PreferredChain,
		OrderURL:       order.Location,
		FinalizeURL:    order.Finalize,
		PrivateKeyPEM:  privateKeyPEM,
		CSRPEM:         csrPEM,
		Challenges:     challenges,
	}, nil
}

func (obj *legoAcmeClient) beginChallengeOrder(domains []string, mustStaple bool) (acme.ExtendedOrder, string, string, error) {
	order, err := obj.core.Orders.New(domains)
	if err != nil {
		return acme.ExtendedOrder{}, "", "", err
	}

	privateKey, err := certcrypto.GeneratePrivateKey(obj.keyType)
	if err != nil {
		return acme.ExtendedOrder{}, "", "", err
	}

	commonName := ""
	if len(domains[0]) <= 64 {
		commonName = domains[0]
	}

	san := []string{}
	if commonName != "" {
		san = append(san, commonName)
	}
	for _, domain := range domains {
		if domain != commonName {
			san = append(san, domain)
		}
	}

	csrDER, err := certcrypto.CreateCSR(privateKey, certcrypto.CSROptions{
		Domain:     commonName,
		SAN:        san,
		MustStaple: mustStaple,
	})
	if err != nil {
		return acme.ExtendedOrder{}, "", "", err
	}

	privateKeyPEM := normalizePEMString(string(certcrypto.PEMEncode(privateKey)))
	csrPEM := normalizePEMString(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})))
	return order, privateKeyPEM, csrPEM, nil
}

func selectACMEChallenge(authz acme.Authorization, challengeType string) (*acme.Challenge, bool, error) {
	if authz.Status == acme.StatusValid {
		return nil, true, nil
	}

	for _, challenge := range authz.Challenges {
		if challenge.Type != challengeType {
			continue
		}
		challenge := challenge
		return &challenge, false, nil
	}

	return nil, false, fmt.Errorf("authorization for %q did not offer a %s challenge", authz.Identifier.Value, challengeType)
}

func (obj *legoAcmeClient) CompleteHTTP01(orderState *acmeHTTP01OrderState) (*certificate.Resource, error) {
	if orderState == nil {
		return nil, fmt.Errorf("the HTTP-01 order state must not be nil")
	}
	if strings.TrimSpace(orderState.OrderURL) == "" {
		return nil, fmt.Errorf("the HTTP-01 order URL must not be empty")
	}
	if strings.TrimSpace(orderState.FinalizeURL) == "" {
		return nil, fmt.Errorf("the HTTP-01 finalize URL must not be empty")
	}

	challengeURLs := make([]string, 0, len(orderState.Challenges))
	for _, challenge := range orderState.Challenges {
		challengeURLs = append(challengeURLs, challenge.ChallengeURL)
	}

	return obj.completeChallengeOrder(
		"http-01",
		orderState.OrderURL,
		orderState.FinalizeURL,
		orderState.PreferredChain,
		orderState.PrivateKeyPEM,
		orderState.CSRPEM,
		orderState.Domains,
		challengeURLs,
	)
}

func (obj *legoAcmeClient) CompleteDNS01(orderState *acmeDNS01OrderState) (*certificate.Resource, error) {
	if orderState == nil {
		return nil, fmt.Errorf("the DNS-01 order state must not be nil")
	}
	if strings.TrimSpace(orderState.OrderURL) == "" {
		return nil, fmt.Errorf("the DNS-01 order URL must not be empty")
	}
	if strings.TrimSpace(orderState.FinalizeURL) == "" {
		return nil, fmt.Errorf("the DNS-01 finalize URL must not be empty")
	}

	challengeURLs := make([]string, 0, len(orderState.Challenges))
	for _, challenge := range orderState.Challenges {
		challengeURLs = append(challengeURLs, challenge.ChallengeURL)
	}

	return obj.completeChallengeOrder(
		"dns-01",
		orderState.OrderURL,
		orderState.FinalizeURL,
		orderState.PreferredChain,
		orderState.PrivateKeyPEM,
		orderState.CSRPEM,
		orderState.Domains,
		challengeURLs,
	)
}

func (obj *legoAcmeClient) completeChallengeOrder(challengeName, orderURL, finalizeURL, preferredChain, privateKeyPEM, csrPEM string, domains []string, challengeURLs []string) (*certificate.Resource, error) {
	for _, challengeURL := range challengeURLs {
		if _, err := obj.core.Challenges.New(challengeURL); err != nil {
			return nil, err
		}
	}

	var readyOrder acme.ExtendedOrder
	if err := wait.For(challengeName+" order readiness", 30*time.Second, 500*time.Millisecond, func() (bool, error) {
		order, err := obj.core.Orders.Get(orderURL)
		if err != nil {
			return false, err
		}
		switch order.Status {
		case acme.StatusReady, acme.StatusValid:
			readyOrder = order
			return true, nil
		case acme.StatusInvalid:
			return true, fmt.Errorf("invalid order: %w", order.Err())
		default:
			return false, nil
		}
	}); err != nil {
		return nil, err
	}

	if readyOrder.Status != acme.StatusValid {
		csrDER, err := decodeCSRPEM(csrPEM)
		if err != nil {
			return nil, err
		}

		readyOrder, err = obj.core.Orders.UpdateForCSR(finalizeURL, csrDER)
		if err != nil {
			return nil, err
		}
	}

	if readyOrder.Status != acme.StatusValid {
		if err := wait.For(challengeName+" certificate issuance", 30*time.Second, 500*time.Millisecond, func() (bool, error) {
			order, err := obj.core.Orders.Get(orderURL)
			if err != nil {
				return false, err
			}
			switch order.Status {
			case acme.StatusValid:
				readyOrder = order
				return true, nil
			case acme.StatusInvalid:
				return true, fmt.Errorf("invalid order: %w", order.Err())
			default:
				return false, nil
			}
		}); err != nil {
			return nil, err
		}
	}

	return obj.certificateResourceFromOrder(domains, preferredChain, privateKeyPEM, csrPEM, readyOrder)
}

func (obj *legoAcmeClient) certificateResourceFromOrder(domains []string, preferredChain, privateKeyPEM, csrPEM string, order acme.ExtendedOrder) (*certificate.Resource, error) {
	certs, err := obj.core.Certificates.GetAll(order.Certificate, true)
	if err != nil {
		return nil, err
	}

	selectedURL := order.Certificate
	selected := certs[selectedURL]
	if selected == nil {
		return nil, fmt.Errorf("certificate response did not include the selected order certificate")
	}

	if preferredChain != "" {
		for certURL, candidate := range certs {
			match, err := hasPreferredChain(candidate.Issuer, preferredChain)
			if err != nil {
				return nil, err
			}
			if match {
				selectedURL = certURL
				selected = candidate
				break
			}
		}
	}

	return &certificate.Resource{
		Domain:            domains[0],
		CertURL:           selectedURL,
		CertStableURL:     selectedURL,
		PrivateKey:        []byte(privateKeyPEM),
		Certificate:       selected.Cert,
		IssuerCertificate: selected.Issuer,
		CSR:               []byte(csrPEM),
	}, nil
}

type legoAcmeUser struct {
	email        string
	registration *registration.Resource
	privateKey   crypto.PrivateKey
}

func (obj *legoAcmeUser) GetEmail() string {
	return obj.email
}

func (obj *legoAcmeUser) GetRegistration() *registration.Resource {
	return obj.registration
}

func (obj *legoAcmeUser) GetPrivateKey() crypto.PrivateKey {
	return obj.privateKey
}

func (obj *AcmeRes) loadAccountData(ctx context.Context) (*acmeAccountData, error) {
	state, err := loadAcmeAccountSharedState(ctx, obj.init.World, obj.Account)
	if err != nil {
		return nil, err
	}
	if state == nil || !state.ready() {
		return nil, nil
	}
	return state.data()
}

func (obj *AcmeRes) planState(state *acmeStoredState, refresh bool, now time.Time, accountData *acmeAccountData) (*acmePlan, error) {
	plan := &acmePlan{}
	state = state.clone()

	desiredDomains, err := obj.desiredDomains()
	if err != nil {
		return nil, err
	}
	keyType, err := obj.certificateKeyType()
	if err != nil {
		return nil, err
	}

	switch obj.normalizedChallenge() {
	case acmeChallengeHTTP01:
		if state.DNS01 != nil {
			plan.needsApply = true
			plan.prepare = true
			plan.reason = "pending dns-01 order does not match requested challenge"
			return plan, nil
		}
		if state.HTTP01 != nil {
			if accountData != nil && state.DirectoryURL != "" && state.DirectoryURL != accountData.DirectoryURL {
				plan.needsApply = true
				plan.prepare = true
				plan.reason = "directory URL changed"
				return plan, nil
			}
			if !state.HTTP01.matches(desiredDomains, string(keyType), obj.MustStaple, obj.PreferredChain) {
				plan.needsApply = true
				plan.prepare = true
				plan.reason = "pending http-01 order configuration changed"
				return plan, nil
			}
			plan.needsApply = true
			plan.complete = true
			plan.reason = "http-01 order is pending"
			return plan, nil
		}

	case acmeChallengeDNS01:
		if state.HTTP01 != nil {
			plan.needsApply = true
			plan.prepare = true
			plan.reason = "pending http-01 order does not match requested challenge"
			return plan, nil
		}
		if state.DNS01 != nil {
			if accountData != nil && state.DirectoryURL != "" && state.DirectoryURL != accountData.DirectoryURL {
				plan.needsApply = true
				plan.prepare = true
				plan.reason = "directory URL changed"
				return plan, nil
			}
			if !state.DNS01.matches(desiredDomains, string(keyType), obj.MustStaple, obj.PreferredChain) {
				plan.needsApply = true
				plan.prepare = true
				plan.reason = "pending dns-01 order configuration changed"
				return plan, nil
			}
			plan.needsApply = true
			plan.complete = true
			plan.reason = "dns-01 order is pending"
			return plan, nil
		}

	default:
		return nil, fmt.Errorf("unsupported challenge: %s", obj.Challenge)
	}

	requestPrepare := func(reason string) (*acmePlan, error) {
		plan.needsApply = true
		plan.prepare = true
		plan.reason = reason
		return plan, nil
	}

	if !state.hasCertificateMaterial() {
		return requestPrepare("certificate is missing")
	}

	if accountData != nil && state.DirectoryURL != "" && state.DirectoryURL != accountData.DirectoryURL {
		return requestPrepare("directory URL changed")
	}
	if !stringSliceEqual(state.Domains, desiredDomains) {
		return requestPrepare("domains changed")
	}
	if !strings.EqualFold(state.KeyType, string(keyType)) {
		return requestPrepare("certificate key type changed")
	}

	leaf, err := state.leafCert()
	if err != nil {
		return requestPrepare("stored certificate could not be parsed")
	}

	if refresh {
		return requestPrepare("resource received a refresh")
	}
	if state.MustStaple != obj.MustStaple {
		return requestPrepare("must_staple changed")
	}
	if state.PreferredChain != obj.PreferredChain {
		return requestPrepare("preferred_chain changed")
	}
	if now.Add(obj.renewBefore()).After(leaf.NotAfter) {
		return requestPrepare("certificate is within the renewal window")
	}

	return plan, nil
}

func (obj *AcmeRes) reconcileState(state *acmeStoredState, plan *acmePlan, accountData *acmeAccountData) (*acmeStoredState, error) {
	next := state.clone()
	if accountData == nil {
		return obj.finalizeStateMetadata(next, nil)
	}

	client, err := obj.clientFactory(next, accountData)
	if err != nil {
		return nil, errwrap.Wrapf(err, "could not create ACME client")
	}

	switch obj.normalizedChallenge() {
	case acmeChallengeHTTP01:
		return obj.reconcileHTTP01State(next, accountData, client, plan)
	case acmeChallengeDNS01:
		return obj.reconcileDNS01State(next, accountData, client, plan)
	default:
		return nil, fmt.Errorf("unsupported challenge: %s", obj.Challenge)
	}
}

func (obj *AcmeRes) reconcileHTTP01State(next *acmeStoredState, accountData *acmeAccountData, client acmeClient, plan *acmePlan) (*acmeStoredState, error) {
	if plan.prepare {
		domains, err := obj.desiredDomains()
		if err != nil {
			return nil, err
		}
		orderState, err := client.BeginHTTP01(acmeHTTP01IssueRequest{
			Domains:        domains,
			MustStaple:     obj.MustStaple,
			PreferredChain: obj.PreferredChain,
		})
		if err != nil {
			return next, errwrap.Wrapf(err, "could not prepare http-01 order")
		}

		next.HTTP01 = orderState
		next.DNS01 = nil
		next.Domain = ""
		next.CertURL = ""
		next.CertStableURL = ""
		next.PrivateKeyPEM = ""
		next.CertificatePEM = ""
		next.IssuerCertificatePEM = ""
		next.CSRPEM = ""

		if err := obj.publishHTTP01Challenges(context.Background(), orderState); err != nil {
			return next, errwrap.Wrapf(err, "could not publish http-01 challenge state")
		}

		return obj.finalizeStateMetadata(next, accountData)
	}

	if !plan.complete || next.HTTP01 == nil {
		return obj.finalizeStateMetadata(next, accountData)
	}

	if len(next.HTTP01.Challenges) > 0 {
		if err := obj.publishHTTP01Challenges(context.Background(), next.HTTP01); err != nil {
			return next, errwrap.Wrapf(err, "could not publish http-01 challenge state")
		}

		if !obj.ready() {
			obj.init.Logf("waiting for ready before validating ACME HTTP-01 challenge")
			return obj.finalizeStateMetadata(next, accountData)
		}

		ready, err := obj.http01PresentationReady(context.Background(), next.HTTP01)
		if err != nil {
			next.HTTP01 = nil
			_ = obj.clearHTTP01Challenges(context.Background())
			_, _ = obj.finalizeStateMetadata(next, accountData)
			return next, errwrap.Wrapf(err, "http-01 solver presentation failed")
		}
		if !ready {
			obj.init.Logf("waiting for %s[%s] to present the HTTP-01 challenge", acmeHTTP01SolverKind, obj.normalizedSolver())
			return obj.finalizeStateMetadata(next, accountData)
		}
	} else {
		if err := obj.clearHTTP01Challenges(context.Background()); err != nil {
			return next, errwrap.Wrapf(err, "could not clear http-01 challenge state")
		}
	}

	res, err := client.CompleteHTTP01(next.HTTP01)
	if err != nil {
		next.HTTP01 = nil
		_ = obj.clearHTTP01Challenges(context.Background())
		_, _ = obj.finalizeStateMetadata(next, accountData)
		return next, errwrap.Wrapf(err, "could not complete http-01 order")
	}

	next.setCertificateResource(res)
	next.HTTP01 = nil
	if err := obj.clearHTTP01Challenges(context.Background()); err != nil {
		return next, errwrap.Wrapf(err, "could not clear http-01 challenge state")
	}

	return obj.finalizeStateMetadata(next, accountData)
}

func (obj *AcmeRes) reconcileDNS01State(next *acmeStoredState, accountData *acmeAccountData, client acmeClient, plan *acmePlan) (*acmeStoredState, error) {
	if plan.prepare {
		domains, err := obj.desiredDomains()
		if err != nil {
			return nil, err
		}
		orderState, err := client.BeginDNS01(acmeDNS01IssueRequest{
			Domains:        domains,
			MustStaple:     obj.MustStaple,
			PreferredChain: obj.PreferredChain,
		})
		if err != nil {
			return next, errwrap.Wrapf(err, "could not prepare dns-01 order")
		}

		next.DNS01 = orderState
		next.HTTP01 = nil
		next.Domain = ""
		next.CertURL = ""
		next.CertStableURL = ""
		next.PrivateKeyPEM = ""
		next.CertificatePEM = ""
		next.IssuerCertificatePEM = ""
		next.CSRPEM = ""

		if err := obj.publishDNS01Challenges(context.Background(), orderState); err != nil {
			return next, errwrap.Wrapf(err, "could not publish dns-01 challenge state")
		}

		return obj.finalizeStateMetadata(next, accountData)
	}

	if !plan.complete || next.DNS01 == nil {
		return obj.finalizeStateMetadata(next, accountData)
	}

	if len(next.DNS01.Challenges) > 0 {
		if err := obj.publishDNS01Challenges(context.Background(), next.DNS01); err != nil {
			return next, errwrap.Wrapf(err, "could not publish dns-01 challenge state")
		}

		if !obj.ready() {
			obj.init.Logf("waiting for ready before validating ACME DNS-01 challenge")
			return obj.finalizeStateMetadata(next, accountData)
		}

		ready, err := obj.dns01PresentationReady(context.Background(), next.DNS01)
		if err != nil {
			next.DNS01 = nil
			_ = obj.clearDNS01Challenges(context.Background())
			_, _ = obj.finalizeStateMetadata(next, accountData)
			return next, errwrap.Wrapf(err, "dns-01 solver presentation failed")
		}
		if !ready {
			obj.init.Logf("waiting for %s[%s] to present the DNS-01 challenge", acmeDNS01SolverKind, obj.normalizedSolver())
			return obj.finalizeStateMetadata(next, accountData)
		}
	} else {
		if err := obj.clearDNS01Challenges(context.Background()); err != nil {
			return next, errwrap.Wrapf(err, "could not clear dns-01 challenge state")
		}
	}

	res, err := client.CompleteDNS01(next.DNS01)
	if err != nil {
		next.DNS01 = nil
		_ = obj.clearDNS01Challenges(context.Background())
		_, _ = obj.finalizeStateMetadata(next, accountData)
		return next, errwrap.Wrapf(err, "could not complete dns-01 order")
	}

	next.setCertificateResource(res)
	next.DNS01 = nil
	if err := obj.clearDNS01Challenges(context.Background()); err != nil {
		return next, errwrap.Wrapf(err, "could not clear dns-01 challenge state")
	}

	return obj.finalizeStateMetadata(next, accountData)
}

func (obj *AcmeRes) finalizeStateMetadata(next *acmeStoredState, accountData *acmeAccountData) (*acmeStoredState, error) {
	next.Version = 1
	if accountData != nil {
		next.DirectoryURL = accountData.DirectoryURL
	}
	domains, err := obj.desiredDomains()
	if err != nil {
		return nil, err
	}
	next.Domains = domains
	keyType, err := obj.certificateKeyType()
	if err != nil {
		return nil, err
	}
	next.KeyType = string(keyType)
	next.MustStaple = obj.MustStaple
	next.PreferredChain = obj.PreferredChain
	return next, nil
}

func (obj *AcmeRes) publishHTTP01Challenges(ctx context.Context, orderState *acmeHTTP01OrderState) error {
	if obj.init == nil || obj.init.World == nil {
		return fmt.Errorf("the World API is required")
	}
	if orderState == nil {
		return storeAcmeHTTP01ChallengeState(ctx, obj.init.World, obj.normalizedSolver(), nil)
	}

	challenges := make(map[string]acmeHTTP01Challenge, len(orderState.Challenges))
	for key, challenge := range orderState.Challenges {
		challenges[key] = challenge
	}
	return storeAcmeHTTP01ChallengeState(ctx, obj.init.World, obj.normalizedSolver(), &acmeHTTP01ChallengeState{
		Challenges: challenges,
	})
}

func (obj *AcmeRes) clearHTTP01Challenges(ctx context.Context) error {
	if obj.init == nil || obj.init.World == nil {
		return nil
	}
	return storeAcmeHTTP01ChallengeState(ctx, obj.init.World, obj.normalizedSolver(), nil)
}

func (obj *AcmeRes) http01PresentationReady(ctx context.Context, orderState *acmeHTTP01OrderState) (bool, error) {
	states, err := loadAcmeHTTP01PresentationStates(ctx, obj.init.World, obj.normalizedSolver())
	if err != nil {
		return false, err
	}

	for hostname, state := range states {
		hostReady := len(orderState.Challenges) > 0
		for key, challenge := range orderState.Challenges {
			entry, exists := state.Entries[key]
			if !exists || entry.Attempt != challenge.Attempt {
				hostReady = false
				break
			}
			if entry.Error != "" {
				return false, fmt.Errorf("%s[%s] on host %q failed: %s", acmeHTTP01SolverKind, obj.normalizedSolver(), hostname, entry.Error)
			}
			if !entry.Ready {
				hostReady = false
				break
			}
		}
		if hostReady {
			return true, nil
		}
	}

	return false, nil
}

func (obj *AcmeRes) publishDNS01Challenges(ctx context.Context, orderState *acmeDNS01OrderState) error {
	if obj.init == nil || obj.init.World == nil {
		return fmt.Errorf("the World API is required")
	}
	if orderState == nil {
		return storeAcmeDNS01ChallengeState(ctx, obj.init.World, obj.normalizedSolver(), nil)
	}

	challenges := make(map[string]acmeDNS01Challenge, len(orderState.Challenges))
	for key, challenge := range orderState.Challenges {
		challenges[key] = challenge
	}
	return storeAcmeDNS01ChallengeState(ctx, obj.init.World, obj.normalizedSolver(), &acmeDNS01ChallengeState{
		Challenges: challenges,
	})
}

func (obj *AcmeRes) clearDNS01Challenges(ctx context.Context) error {
	if obj.init == nil || obj.init.World == nil {
		return nil
	}
	return storeAcmeDNS01ChallengeState(ctx, obj.init.World, obj.normalizedSolver(), nil)
}

func (obj *AcmeRes) dns01PresentationReady(ctx context.Context, orderState *acmeDNS01OrderState) (bool, error) {
	state, err := loadAcmeDNS01PresentationState(ctx, obj.init.World, obj.normalizedSolver())
	if err != nil {
		return false, err
	}
	if state == nil {
		return false, nil
	}

	ready := len(orderState.Challenges) > 0
	for key, challenge := range orderState.Challenges {
		entry, exists := state.Entries[key]
		if !exists || entry.Attempt != challenge.Attempt {
			ready = false
			break
		}
		if entry.Error != "" {
			return false, fmt.Errorf("%s[%s] failed: %s", acmeDNS01SolverKind, obj.normalizedSolver(), entry.Error)
		}
		if !entry.Ready {
			ready = false
			break
		}
	}
	return ready, nil
}

func (obj *AcmeRes) newLegoClient(_ *acmeStoredState, accountData *acmeAccountData) (acmeClient, error) {
	if accountData == nil {
		return nil, fmt.Errorf("account data is missing")
	}

	baseClient, err := newLegoBaseClient(accountData.DirectoryURL, "", accountData.PrivateKeyPEM, accountData.RegistrationURI)
	if err != nil {
		return nil, err
	}

	keyType, err := obj.certificateKeyType()
	if err != nil {
		return nil, err
	}
	baseClient.keyType = keyType

	return baseClient, nil
}

func (obj *AcmeRes) desiredDomains() ([]string, error) {
	domains := obj.Domains
	if len(domains) == 0 && obj.Name() != "" {
		domains = []string{obj.Name()}
	}
	result, err := normalizeACMEDomains(domains)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("the Domains field must not be empty")
	}
	return result, nil
}

func (obj *AcmeRes) normalizedChallenge() string {
	return strings.ToLower(strings.TrimSpace(obj.Challenge))
}

func (obj *AcmeRes) normalizedSolver() string {
	return strings.TrimSpace(obj.Solver)
}

func (obj *AcmeRes) certificateKeyType() (certcrypto.KeyType, error) {
	switch strings.ToLower(strings.TrimSpace(obj.KeyType)) {
	case "", "rsa2048":
		return certcrypto.RSA2048, nil
	case "rsa3072":
		return certcrypto.RSA3072, nil
	case "rsa4096":
		return certcrypto.RSA4096, nil
	case "rsa8192":
		return certcrypto.RSA8192, nil
	case "ec256", "p256":
		return certcrypto.EC256, nil
	case "ec384", "p384":
		return certcrypto.EC384, nil
	default:
		return "", fmt.Errorf("unsupported key type: %s", obj.KeyType)
	}
}

func (obj *AcmeRes) renewBefore() time.Duration {
	return time.Duration(obj.RenewBeforeDays) * 24 * time.Hour
}

func (obj *AcmeRes) ready() bool {
	if obj.Ready == nil {
		return true
	}
	return *obj.Ready
}

func (obj *AcmeRes) buildSends(state *acmeStoredState, plan *acmePlan) *AcmeSends {
	sends := &AcmeSends{
		Pending:       (plan != nil && plan.needsApply) || (state != nil && state.hasPendingChallenge()),
		HTTP01Pending: obj.normalizedChallenge() == acmeChallengeHTTP01 && ((plan != nil && plan.needsApply) || (state != nil && state.HTTP01 != nil)),
	}
	if state == nil || !state.hasCertificateMaterial() {
		return sends
	}

	fullChain := buildFullChain(state.CertificatePEM, state.IssuerCertificatePEM)
	leafPEM := firstCertificatePEM(state.CertificatePEM)
	notBefore, notAfter := certificateBounds(state.CertificatePEM)

	sends.Domain = state.Domain
	sends.CertURL = state.CertURL
	sends.CertStableURL = state.CertStableURL
	sends.PrivateKey = state.PrivateKeyPEM
	sends.Certificate = leafPEM
	sends.IssuerCertificate = state.IssuerCertificatePEM
	sends.FullChain = fullChain
	sends.NotBefore = notBefore
	sends.NotAfter = notAfter
	return sends
}

func (obj *AcmeRes) loadState() (*acmeStoredState, error) {
	data, err := os.ReadFile(obj.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	state := &acmeStoredState{}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, errwrap.Wrapf(err, "could not parse ACME state")
	}

	state.PrivateKeyPEM = normalizePEMString(state.PrivateKeyPEM)
	state.CertificatePEM = normalizePEMString(state.CertificatePEM)
	state.IssuerCertificatePEM = normalizePEMString(state.IssuerCertificatePEM)
	state.CSRPEM = normalizePEMString(state.CSRPEM)
	if state.HTTP01 != nil {
		state.HTTP01.PrivateKeyPEM = normalizePEMString(state.HTTP01.PrivateKeyPEM)
		state.HTTP01.CSRPEM = normalizePEMString(state.HTTP01.CSRPEM)
		if state.HTTP01.Challenges == nil {
			state.HTTP01.Challenges = map[string]acmeHTTP01Challenge{}
		}
	}
	if state.DNS01 != nil {
		state.DNS01.PrivateKeyPEM = normalizePEMString(state.DNS01.PrivateKeyPEM)
		state.DNS01.CSRPEM = normalizePEMString(state.DNS01.CSRPEM)
		if state.DNS01.Challenges == nil {
			state.DNS01.Challenges = map[string]acmeDNS01Challenge{}
		}
	}

	return state, nil
}

func (obj *AcmeRes) saveState(state *acmeStoredState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := obj.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, obj.statePath)
}

func (obj *AcmeRes) now() time.Time {
	return obj.nowFn().UTC()
}

func (obj *AcmeRes) updateSchedule(state *acmeStoredState, now time.Time) {
	var next *time.Time
	if state != nil && state.hasCertificateMaterial() {
		if leaf, err := state.leafCert(); err == nil {
			when := leaf.NotAfter.Add(-obj.renewBefore())
			if when.Before(now.Add(acmeImmediateRetryDelay)) {
				when = now.Add(acmeImmediateRetryDelay)
			}
			next = &when
		}
	}

	obj.scheduleMu.Lock()
	obj.scheduleAt = next
	obj.scheduleMu.Unlock()

	select {
	case obj.scheduleCh <- struct{}{}:
	default:
	}
}

func (obj *AcmeRes) requestImmediateRecheck() {
	select {
	case obj.recheckCh <- struct{}{}:
	default:
	}
}

func (obj *AcmeRes) nextScheduleTimer() *time.Timer {
	obj.scheduleMu.Lock()
	defer obj.scheduleMu.Unlock()

	if obj.scheduleAt == nil {
		return nil
	}

	delay := time.Until(*obj.scheduleAt)
	if delay < 0 {
		delay = 0
	}
	return time.NewTimer(delay)
}

func timerChan(timer *time.Timer) <-chan time.Time {
	if timer == nil {
		return nil
	}
	return timer.C
}

func normalizeACMEDomain(domain string) (string, error) {
	trimmed := strings.TrimSpace(domain)
	if trimmed == "" {
		return "", fmt.Errorf("domain must not be empty")
	}

	wildcard := false
	if strings.HasPrefix(trimmed, "*.") {
		wildcard = true
		trimmed = strings.TrimPrefix(trimmed, "*.")
		if trimmed == "" {
			return "", fmt.Errorf("wildcard domain must include a non-empty suffix")
		}
	}
	if strings.Contains(trimmed, "*") {
		return "", fmt.Errorf("domain contains an invalid wildcard")
	}

	normalized, err := acmeIDNAProfile.ToASCII(trimmed)
	if err != nil {
		return "", errwrap.Wrapf(err, "domain is not valid IDNA")
	}
	if normalized == "" {
		return "", fmt.Errorf("domain normalized to empty")
	}
	if wildcard {
		return "*." + normalized, nil
	}
	return normalized, nil
}

func normalizeACMEDomains(domains []string) ([]string, error) {
	result := []string{}
	for _, domain := range domains {
		normalized, err := normalizeACMEDomain(domain)
		if err != nil {
			return nil, fmt.Errorf("invalid domain %q: %w", domain, err)
		}
		if strInList(normalized, result) {
			continue
		}
		result = append(result, normalized)
	}
	return result, nil
}

func decodeCSRPEM(csrPEM string) ([]byte, error) {
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		return nil, fmt.Errorf("could not decode CSR PEM")
	}
	if block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("unexpected CSR block type: %s", block.Type)
	}
	return block.Bytes, nil
}

func hasPreferredChain(issuerPEM []byte, preferredChain string) (bool, error) {
	certs, err := certcrypto.ParsePEMBundle(issuerPEM)
	if err != nil {
		return false, err
	}
	topCert := certs[len(certs)-1]
	return topCert.Issuer.CommonName == preferredChain, nil
}

func buildFullChain(certificatePEM, issuerPEM string) string {
	certificatePEM = normalizePEMString(certificatePEM)
	issuerPEM = normalizePEMString(issuerPEM)

	if certificatePEM == "" {
		return ""
	}

	blocks := certificatePEMBlocks(certificatePEM)
	if len(blocks) > 1 || issuerPEM == "" {
		return certificatePEM
	}

	var buf bytes.Buffer
	buf.WriteString(certificatePEM)
	if !strings.HasSuffix(certificatePEM, "\n") {
		buf.WriteByte('\n')
	}
	buf.WriteString(issuerPEM)
	if !strings.HasSuffix(issuerPEM, "\n") {
		buf.WriteByte('\n')
	}
	return buf.String()
}

func firstCertificatePEM(certificatePEM string) string {
	blocks := certificatePEMBlocks(certificatePEM)
	if len(blocks) == 0 {
		return ""
	}
	return normalizePEMString(string(blocks[0]))
}

func certificatePEMBlocks(certificatePEM string) [][]byte {
	remaining := []byte(certificatePEM)
	blocks := [][]byte{}
	for {
		var block *pem.Block
		block, remaining = pem.Decode(remaining)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		blocks = append(blocks, pem.EncodeToMemory(block))
	}
	return blocks
}

func certificateBounds(certificatePEM string) (int64, int64) {
	leaf, err := certcrypto.ParsePEMBundle([]byte(certificatePEM))
	if err != nil || len(leaf) == 0 {
		return 0, 0
	}
	return leaf[0].NotBefore.Unix(), leaf[0].NotAfter.Unix()
}

func normalizePEMString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return value + "\n"
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func strInList(needle string, haystack []string) bool {
	for _, value := range haystack {
		if value == needle {
			return true
		}
	}
	return false
}
