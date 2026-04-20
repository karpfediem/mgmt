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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	legochallenge "github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	legodns "github.com/go-acme/lego/v4/providers/dns"
	"github.com/go-acme/lego/v4/registration"
)

func init() {
	engine.RegisterResource("acme", func() engine.Res { return &AcmeRes{} })
}

const (
	acmeStateFilename          = "state.json"
	acmeDefaultRenewBeforeDays = 30
	acmeDefaultHTTPPort        = 80
	acmeDefaultKeyType         = "rsa2048"
	acmeImmediateRetryDelay    = time.Minute
	acmeChallengeHTTP01        = "http-01"
	acmeChallengeDNS01         = "dns-01"
)

var acmeDNSProviderEnvMu sync.Mutex

// AcmeRes manages an ACME account and certificate lifecycle using an explicit
// ACME challenge type. The issued PEM material is exposed with send/recv so
// that resources such as file can consume it directly.
type AcmeRes struct {
	traits.Base
	traits.Refreshable
	traits.Sendable
	traits.Recvable

	init *engine.Init

	// Email is the ACME account contact email.
	Email string `lang:"email" yaml:"email"`

	// AcceptTOS must be true so that the ACME account can be registered.
	AcceptTOS bool `lang:"accept_tos" yaml:"accept_tos"`

	// Domains is the ordered list of domains for the requested certificate.
	// If omitted, the resource name is used as a single-domain certificate.
	Domains []string `lang:"domains" yaml:"domains"`

	// Challenge selects how the ACME authorization is solved.
	// Supported values are: http-01, dns-01.
	Challenge string `lang:"challenge" yaml:"challenge"`

	// DNSProvider selects the lego-backed DNS provider used when Challenge is
	// dns-01.
	DNSProvider string `lang:"dns_provider" yaml:"dns_provider"`

	// DNSEnv contains the provider environment variables passed through to the
	// lego DNS provider factory. Keys and validation semantics are owned by the
	// selected provider implementation.
	DNSEnv map[string]string `lang:"dns_env" yaml:"dns_env"`

	// DirectoryURL is the ACME directory endpoint.
	DirectoryURL string `lang:"directory_url" yaml:"directory_url"`

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

	// HTTPAddress is the interface address used by the embedded HTTP-01
	// challenge listener. The empty string means all interfaces.
	HTTPAddress string `lang:"http_address" yaml:"http_address"`

	// HTTPPort is the listen port used by the embedded HTTP-01 challenge
	// listener. ACME HTTP-01 validation normally requires external reachability
	// on TCP port 80.
	HTTPPort uint16 `lang:"http_port" yaml:"http_port"`

	// HTTPDelaySeconds delays challenge validation after the HTTP server starts.
	HTTPDelaySeconds uint16 `lang:"http_delay_seconds" yaml:"http_delay_seconds"`

	// HTTPProxyHeader changes which request header is used to validate the
	// incoming challenge hostname, for example X-Forwarded-Host.
	HTTPProxyHeader string `lang:"http_proxy_header" yaml:"http_proxy_header"`

	// HTTP01Ready optionally gates when an HTTP-01 challenge attempt may run.
	// A nil value means no external gate is in use.
	HTTP01Ready *bool `lang:"http01_ready" yaml:"http01_ready"`

	varDir    string
	statePath string

	nowFn         func() time.Time
	clientFactory func(*acmeStoredState) (acmeClient, error)

	scheduleMu sync.Mutex
	scheduleAt *time.Time
	scheduleCh chan struct{}
}

// Default returns some sensible defaults for this resource.
func (obj *AcmeRes) Default() engine.Res {
	return &AcmeRes{
		DirectoryURL:    lego.LEDirectoryProduction,
		KeyType:         acmeDefaultKeyType,
		RenewBeforeDays: acmeDefaultRenewBeforeDays,
		HTTPPort:        acmeDefaultHTTPPort,
	}
}

// Validate if the params passed in are valid data.
func (obj *AcmeRes) Validate() error {
	if !obj.AcceptTOS {
		return fmt.Errorf("the AcceptTOS field must be true")
	}
	switch obj.normalizedChallenge() {
	case acmeChallengeHTTP01:
	case acmeChallengeDNS01:
	default:
		return fmt.Errorf("the Challenge field must be one of %q or %q", acmeChallengeHTTP01, acmeChallengeDNS01)
	}
	if obj.DirectoryURL == "" {
		return fmt.Errorf("the DirectoryURL field must not be empty")
	}
	if obj.normalizedChallenge() == acmeChallengeHTTP01 && obj.HTTPPort == 0 {
		return fmt.Errorf("the HTTPPort field must be greater than zero")
	}
	if err := obj.validateDNSChallengeConfig(); err != nil {
		return err
	}
	if _, err := obj.certificateKeyType(); err != nil {
		return err
	}
	if len(obj.desiredDomains()) == 0 {
		return fmt.Errorf("the Domains field must not be empty")
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

	return nil
}

// Cleanup is run by the engine to clean up after the resource is done.
func (obj *AcmeRes) Cleanup() error {
	return nil
}

// Watch is the primary listener for this resource and it outputs events.
func (obj *AcmeRes) Watch(ctx context.Context) error {
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
	_ = ctx

	refresh := obj.init.Refresh()
	now := obj.now()

	state, err := obj.loadState()
	if err != nil {
		return false, errwrap.Wrapf(err, "could not load ACME state")
	}
	if state == nil {
		state = &acmeStoredState{}
	}

	plan, err := obj.planState(state, refresh, now)
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
		if !obj.http01Ready() {
			obj.init.Logf("waiting for http01_ready before attempting ACME HTTP-01 challenge")
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
		nextState, err := obj.reconcileState(state, plan)
		if err != nil {
			return false, err
		}
		state = nextState
		if err := obj.saveState(state); err != nil {
			return false, errwrap.Wrapf(err, "could not save ACME state")
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

	if obj.Email != res.Email {
		return fmt.Errorf("the Email field differs")
	}
	if obj.AcceptTOS != res.AcceptTOS {
		return fmt.Errorf("the AcceptTOS field differs")
	}
	if !reflect.DeepEqual(obj.desiredDomains(), res.desiredDomains()) {
		return fmt.Errorf("the Domains field differs")
	}
	if obj.normalizedChallenge() != res.normalizedChallenge() {
		return fmt.Errorf("the Challenge field differs")
	}
	if obj.normalizedDNSProvider() != res.normalizedDNSProvider() {
		return fmt.Errorf("the DNSProvider field differs")
	}
	if !reflect.DeepEqual(obj.normalizedDNSEnv(), res.normalizedDNSEnv()) {
		return fmt.Errorf("the DNSEnv field differs")
	}
	if obj.DirectoryURL != res.DirectoryURL {
		return fmt.Errorf("the DirectoryURL field differs")
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
	if obj.HTTPAddress != res.HTTPAddress {
		return fmt.Errorf("the HTTPAddress field differs")
	}
	if obj.HTTPPort != res.HTTPPort {
		return fmt.Errorf("the HTTPPort field differs")
	}
	if obj.HTTPDelaySeconds != res.HTTPDelaySeconds {
		return fmt.Errorf("the HTTPDelaySeconds field differs")
	}
	if obj.HTTPProxyHeader != res.HTTPProxyHeader {
		return fmt.Errorf("the HTTPProxyHeader field differs")
	}
	if !reflect.DeepEqual(obj.HTTP01Ready, res.HTTP01Ready) {
		return fmt.Errorf("the HTTP01Ready field differs")
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
	needsApply    bool
	updateAccount bool
	renew         bool
	reissue       bool
	reason        string
	emailChanged  bool
}

type acmeStoredState struct {
	Version              int                    `json:"version"`
	Email                string                 `json:"email"`
	DirectoryURL         string                 `json:"directory_url"`
	Domains              []string               `json:"domains"`
	KeyType              string                 `json:"key_type"`
	MustStaple           bool                   `json:"must_staple"`
	PreferredChain       string                 `json:"preferred_chain"`
	AccountPrivateKeyPEM string                 `json:"account_private_key_pem"`
	Registration         *registration.Resource `json:"registration,omitempty"`
	Domain               string                 `json:"domain"`
	CertURL              string                 `json:"cert_url"`
	CertStableURL        string                 `json:"cert_stable_url"`
	PrivateKeyPEM        string                 `json:"private_key_pem"`
	CertificatePEM       string                 `json:"certificate_pem"`
	IssuerCertificatePEM string                 `json:"issuer_certificate_pem"`
	CSRPEM               string                 `json:"csr_pem"`
}

func (obj *acmeStoredState) clone() *acmeStoredState {
	if obj == nil {
		return &acmeStoredState{}
	}
	clone := *obj
	clone.Domains = append([]string(nil), obj.Domains...)
	return &clone
}

func (obj *acmeStoredState) hasCertificateMaterial() bool {
	return strings.TrimSpace(obj.PrivateKeyPEM) != "" && strings.TrimSpace(obj.CertificatePEM) != ""
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
	Obtain(certificate.ObtainRequest) (*certificate.Resource, error)
	Renew(certificate.Resource, *certificate.RenewOptions) (*certificate.Resource, error)
}

type legoAcmeClient struct {
	client        *lego.Client
	accountKeyPEM string
	user          *legoAcmeUser
}

func (obj *legoAcmeClient) AccountKeyPEM() string {
	return obj.accountKeyPEM
}

func (obj *legoAcmeClient) EnsureRegistration(emailChanged, acceptTOS bool) (*registration.Resource, error) {
	opts := registration.RegisterOptions{TermsOfServiceAgreed: acceptTOS}
	if obj.user.GetRegistration() == nil {
		reg, err := obj.client.Registration.Register(opts)
		if err != nil {
			return nil, err
		}
		obj.user.registration = reg
		return reg, nil
	}
	if emailChanged {
		reg, err := obj.client.Registration.UpdateRegistration(opts)
		if err != nil {
			return nil, err
		}
		obj.user.registration = reg
		return reg, nil
	}
	return obj.user.GetRegistration(), nil
}

func (obj *legoAcmeClient) Obtain(request certificate.ObtainRequest) (*certificate.Resource, error) {
	return obj.client.Certificate.Obtain(request)
}

func (obj *legoAcmeClient) Renew(certRes certificate.Resource, opts *certificate.RenewOptions) (*certificate.Resource, error) {
	return obj.client.Certificate.RenewWithOptions(certRes, opts)
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

func (obj *AcmeRes) planState(state *acmeStoredState, refresh bool, now time.Time) (*acmePlan, error) {
	plan := &acmePlan{}
	state = state.clone()

	desiredDomains := obj.desiredDomains()
	keyType, err := obj.certificateKeyType()
	if err != nil {
		return nil, err
	}

	plan.emailChanged = state.Email != obj.Email
	plan.updateAccount = state.Registration == nil || plan.emailChanged

	if !state.hasCertificateMaterial() {
		plan.needsApply = true
		plan.reissue = true
		plan.reason = "certificate is missing"
		return plan, nil
	}

	if state.DirectoryURL != "" && state.DirectoryURL != obj.DirectoryURL {
		plan.needsApply = true
		plan.reissue = true
		plan.reason = "directory URL changed"
		return plan, nil
	}
	if !stringSliceEqual(state.Domains, desiredDomains) {
		plan.needsApply = true
		plan.reissue = true
		plan.reason = "domains changed"
		return plan, nil
	}
	if !strings.EqualFold(state.KeyType, string(keyType)) {
		plan.needsApply = true
		plan.reissue = true
		plan.reason = "certificate key type changed"
		return plan, nil
	}

	leaf, err := state.leafCert()
	if err != nil {
		plan.needsApply = true
		plan.reissue = true
		plan.reason = "stored certificate could not be parsed"
		return plan, nil
	}

	if refresh {
		plan.needsApply = true
		plan.renew = true
		plan.reason = "resource received a refresh"
		return plan, nil
	}
	if state.MustStaple != obj.MustStaple {
		plan.needsApply = true
		plan.renew = true
		plan.reason = "must_staple changed"
		return plan, nil
	}
	if state.PreferredChain != obj.PreferredChain {
		plan.needsApply = true
		plan.renew = true
		plan.reason = "preferred_chain changed"
		return plan, nil
	}
	if now.Add(obj.renewBefore()).After(leaf.NotAfter) {
		plan.needsApply = true
		plan.renew = true
		plan.reason = "certificate is within the renewal window"
		return plan, nil
	}
	if plan.updateAccount {
		plan.needsApply = true
		plan.reason = "account registration needs to be updated"
	}

	return plan, nil
}

func (obj *AcmeRes) reconcileState(state *acmeStoredState, plan *acmePlan) (*acmeStoredState, error) {
	next := state.clone()

	client, err := obj.clientFactory(next)
	if err != nil {
		return nil, errwrap.Wrapf(err, "could not create ACME client")
	}

	next.AccountPrivateKeyPEM = normalizePEMString(client.AccountKeyPEM())

	if plan.updateAccount || next.Registration == nil {
		reg, err := client.EnsureRegistration(plan.emailChanged, obj.AcceptTOS)
		if err != nil {
			return nil, errwrap.Wrapf(err, "could not register ACME account")
		}
		next.Registration = reg
	}

	switch {
	case plan.reissue:
		request := certificate.ObtainRequest{
			Domains:        obj.desiredDomains(),
			MustStaple:     obj.MustStaple,
			Bundle:         true,
			PreferredChain: obj.PreferredChain,
		}
		res, err := client.Obtain(request)
		if err != nil {
			return nil, errwrap.Wrapf(err, "could not obtain certificate")
		}
		next.setCertificateResource(res)

	case plan.renew:
		options := &certificate.RenewOptions{
			Bundle:         true,
			PreferredChain: obj.PreferredChain,
			MustStaple:     obj.MustStaple,
		}
		res, err := client.Renew(next.certResource(), options)
		if err != nil {
			return nil, errwrap.Wrapf(err, "could not renew certificate")
		}
		next.setCertificateResource(res)
	}

	next.Version = 1
	next.Email = obj.Email
	next.DirectoryURL = obj.DirectoryURL
	next.Domains = obj.desiredDomains()
	keyType, err := obj.certificateKeyType()
	if err != nil {
		return nil, err
	}
	next.KeyType = string(keyType)
	next.MustStaple = obj.MustStaple
	next.PreferredChain = obj.PreferredChain

	return next, nil
}

func (obj *AcmeRes) newLegoClient(state *acmeStoredState) (acmeClient, error) {
	keyPEM := normalizePEMString(state.AccountPrivateKeyPEM)
	if keyPEM == "" {
		privateKey, err := certcrypto.GeneratePrivateKey(certcrypto.EC256)
		if err != nil {
			return nil, errwrap.Wrapf(err, "could not generate ACME account private key")
		}
		keyPEM = normalizePEMString(string(certcrypto.PEMEncode(privateKey)))
	}

	privateKey, err := certcrypto.ParsePEMPrivateKey([]byte(keyPEM))
	if err != nil {
		return nil, errwrap.Wrapf(err, "could not parse ACME account private key")
	}

	var reg *registration.Resource
	if state.DirectoryURL == "" || state.DirectoryURL == obj.DirectoryURL {
		reg = state.Registration
	}

	user := &legoAcmeUser{
		email:        obj.Email,
		registration: reg,
		privateKey:   privateKey,
	}

	config := lego.NewConfig(user)
	config.CADirURL = obj.DirectoryURL
	keyType, err := obj.certificateKeyType()
	if err != nil {
		return nil, err
	}
	config.Certificate.KeyType = keyType

	client, err := lego.NewClient(config)
	if err != nil {
		return nil, err
	}

	switch obj.normalizedChallenge() {
	case acmeChallengeHTTP01:
		provider := http01.NewProviderServer(obj.HTTPAddress, strconv.Itoa(int(obj.HTTPPort)))
		if obj.HTTPProxyHeader != "" {
			provider.SetProxyHeader(obj.HTTPProxyHeader)
		}

		options := []http01.ChallengeOption{}
		if obj.HTTPDelaySeconds > 0 {
			options = append(options, http01.SetDelay(time.Duration(obj.HTTPDelaySeconds)*time.Second))
		}
		if err := client.Challenge.SetHTTP01Provider(provider, options...); err != nil {
			return nil, err
		}

	case acmeChallengeDNS01:
		provider, err := obj.newDNSChallengeProvider()
		if err != nil {
			return nil, err
		}
		if err := client.Challenge.SetDNS01Provider(provider); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unsupported challenge: %s", obj.Challenge)
	}

	return &legoAcmeClient{
		client:        client,
		accountKeyPEM: keyPEM,
		user:          user,
	}, nil
}

func (obj *AcmeRes) desiredDomains() []string {
	domains := obj.Domains
	if len(domains) == 0 && obj.Name() != "" {
		domains = []string{obj.Name()}
	}

	result := []string{}
	for _, domain := range domains {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			continue
		}
		if !strInList(domain, result) {
			result = append(result, domain)
		}
	}
	return result
}

func (obj *AcmeRes) normalizedChallenge() string {
	return strings.ToLower(strings.TrimSpace(obj.Challenge))
}

func (obj *AcmeRes) normalizedDNSProvider() string {
	return strings.ToLower(strings.TrimSpace(obj.DNSProvider))
}

func (obj *AcmeRes) normalizedDNSEnv() map[string]string {
	if len(obj.DNSEnv) == 0 {
		return nil
	}

	out := make(map[string]string, len(obj.DNSEnv))
	for key, value := range obj.DNSEnv {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (obj *AcmeRes) validateDNSChallengeConfig() error {
	if obj.normalizedChallenge() != acmeChallengeDNS01 {
		if obj.normalizedDNSProvider() != "" {
			return fmt.Errorf("the DNSProvider field is only valid when Challenge is %q", acmeChallengeDNS01)
		}
		if len(obj.normalizedDNSEnv()) > 0 {
			return fmt.Errorf("the DNSEnv field is only valid when Challenge is %q", acmeChallengeDNS01)
		}
		return nil
	}

	if obj.normalizedDNSProvider() == "" {
		return fmt.Errorf("the DNSProvider field must not be empty when Challenge is %q", acmeChallengeDNS01)
	}
	if len(obj.normalizedDNSEnv()) == 0 {
		return fmt.Errorf("the DNSEnv field must not be empty when Challenge is %q", acmeChallengeDNS01)
	}
	for key := range obj.normalizedDNSEnv() {
		if strings.Contains(key, "=") {
			return fmt.Errorf("invalid DNSEnv key: %q", key)
		}
	}
	if _, err := obj.newDNSChallengeProvider(); err != nil {
		return errwrap.Wrapf(err, "invalid DNS challenge configuration")
	}
	return nil
}

func (obj *AcmeRes) newDNSChallengeProvider() (legochallenge.Provider, error) {
	var provider legochallenge.Provider
	err := obj.withDNSEnv(func() error {
		var err error
		provider, err = legodns.NewDNSChallengeProviderByName(obj.normalizedDNSProvider())
		return err
	})
	if err != nil {
		return nil, err
	}
	return provider, nil
}

func (obj *AcmeRes) withDNSEnv(fn func() error) error {
	env := obj.normalizedDNSEnv()
	if len(env) == 0 {
		return fn()
	}

	acmeDNSProviderEnvMu.Lock()
	defer acmeDNSProviderEnvMu.Unlock()

	previous := make(map[string]*string, len(env))
	for key, value := range env {
		if current, exists := os.LookupEnv(key); exists {
			copy := current
			previous[key] = &copy
		} else {
			previous[key] = nil
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}

	defer func() {
		for key, value := range previous {
			if value == nil {
				_ = os.Unsetenv(key)
				continue
			}
			_ = os.Setenv(key, *value)
		}
	}()

	return fn()
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

func (obj *AcmeRes) http01Ready() bool {
	if obj.normalizedChallenge() != acmeChallengeHTTP01 {
		return true
	}
	if obj.HTTP01Ready == nil {
		return true
	}
	return *obj.HTTP01Ready
}

func (obj *AcmeRes) buildSends(state *acmeStoredState, plan *acmePlan) *AcmeSends {
	sends := &AcmeSends{
		Pending:       plan != nil && plan.needsApply,
		HTTP01Pending: plan != nil && plan.needsApply && obj.normalizedChallenge() == acmeChallengeHTTP01,
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

	state.AccountPrivateKeyPEM = normalizePEMString(state.AccountPrivateKeyPEM)
	state.PrivateKeyPEM = normalizePEMString(state.PrivateKeyPEM)
	state.CertificatePEM = normalizePEMString(state.CertificatePEM)
	state.IssuerCertificatePEM = normalizePEMString(state.IssuerCertificatePEM)
	state.CSRPEM = normalizePEMString(state.CSRPEM)

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
