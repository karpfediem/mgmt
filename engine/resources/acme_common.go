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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"golang.org/x/crypto/acme"
)

const (
	KindAcmeAccount         = "acme:account"
	KindAcmeCertificate     = "acme:certificate"
	KindAcmeSolverHTTP01    = "acme:solver:http01"
	KindAcmeSolverDNS01     = "acme:solver:dns01"
	KindAcmeSolverTLSALPN01 = "acme:solver:tlsalpn01"

	acmeVersion = 1

	acmeDefaultKeyAlgorithm        = "ecdsa-p256"
	acmeDefaultPrivateKeyMode      = "plaintext"
	acmeDefaultRenewBefore         = uint64(30 * 24 * 60 * 60)
	acmeDefaultRequestNamespace    = "acme/cert-requests/default"
	acmeDefaultPropagation         = uint64(600)
	acmeDefaultPollInterval        = uint64(10)
	acmeDefaultAttemptTTL          = uint64(300)
	acmeDefaultPresentationTimeout = uint64(120)
	acmeDefaultPresentationSettle  = uint64(0)
	acmeDefaultCooldown            = uint64(600)
	acmeDefaultHTTP01Listen        = ":80"
	acmeDefaultTLSALPN01Listen     = ":443"

	acmeChallengeHTTP01    = "http-01"
	acmeChallengeDNS01     = "dns-01"
	acmeChallengeTLSALPN01 = "tls-alpn-01"

	acmeAttemptPhasePresenting = "presenting"
)

type acmeCertSpec struct {
	Version        int      `json:"version"`
	Domains        []string `json:"domains"`
	KeyAlgorithm   string   `json:"key_algorithm"`
	RenewBefore    uint64   `json:"renew_before"`
	PrivateKeyMode string   `json:"private_key_mode"`
	SpecDigest     string   `json:"spec_digest"`
}

type acmePrivateKeyBundle struct {
	Mode string `json:"mode"`
	PEM  string `json:"pem"`
}

type acmeCertBundle struct {
	Version      int                  `json:"version"`
	Namespace    string               `json:"namespace"`
	SpecDigest   string               `json:"spec_digest"`
	BundleDigest string               `json:"bundle_digest"`
	Generation   uint64               `json:"generation"`
	Domains      []string             `json:"domains"`
	NotBefore    string               `json:"not_before"`
	NotAfter     string               `json:"not_after"`
	PrivateKey   acmePrivateKeyBundle `json:"private_key"`
	CertPEM      string               `json:"cert_pem"`
	ChainPEM     string               `json:"chain_pem"`
	FullchainPEM string               `json:"fullchain_pem"`
}

type acmeCertCurrent struct {
	Version      int    `json:"version"`
	SpecDigest   string `json:"spec_digest"`
	BundleDigest string `json:"bundle_digest"`
	Generation   uint64 `json:"generation"`
	NotBefore    string `json:"not_before"`
	NotAfter     string `json:"not_after"`
}

type acmeAccountInfo struct {
	Version              int      `json:"version"`
	Directory            string   `json:"directory"`
	Contact              []string `json:"contact"`
	Key                  string   `json:"key"`
	KeyAlgorithm         string   `json:"key_algorithm"`
	CacheDir             string   `json:"cache_dir"`
	TermsOfServiceAgreed bool     `json:"terms_of_service_agreed"`
	AccountURI           string   `json:"account_uri"`
	EABKid               string   `json:"eab_kid"`
	EABHMACKeyFile       string   `json:"eab_hmac_key_file"`
}

type acmeRequestIndexEntry struct {
	Version      int      `json:"version"`
	Certificates []string `json:"certificates"`
}

type acmeCertificateRequest struct {
	Name       string
	Namespace  string
	Spec       *acmeCertSpec
	Generation uint64
}

type acmeAttemptRecord struct {
	Version       int    `json:"version"`
	Certificate   string `json:"certificate"`
	AttemptID     string `json:"attempt_id"`
	Owner         string `json:"owner"`
	Phase         string `json:"phase"`
	ChallengeType string `json:"challenge_type"`
	ExpiresAt     int64  `json:"expires_at"`
	Port          int    `json:"port,omitempty"`
	Domain        string `json:"domain,omitempty"`
	DNSZone       string `json:"dns_zone,omitempty"`
	DNSName       string `json:"dns_name,omitempty"`
	DNSValue      string `json:"dns_value,omitempty"`
}

type acmePresenter interface {
	challengeType() string
	canSolve(spec *acmeCertSpec) (bool, error)
	prepare(ctx context.Context) (func(context.Context) error, bool, error)
	present(ctx context.Context, req *acmeCertificateRequest, client *acme.Client, authz *acme.Authorization, chal *acme.Challenge) (func(context.Context) error, error)
	attemptTTL() time.Duration
	presentationTimeout() time.Duration
	cooldownDuration() time.Duration
	presentationSettle() time.Duration
	cooldownUntil(namespace string) time.Time
	setCooldown(namespace string, until time.Time)
	owner() string
}

func acmeRequestNamespace(namespace string) string {
	ns := strings.TrimSpace(namespace)
	if ns != "" {
		return strings.TrimRight(ns, "/")
	}
	return acmeDefaultRequestNamespace
}

func acmeCertificateNamespace(name, requestNamespace string) string {
	return acmeRequestNamespace(requestNamespace) + "/" + name
}

func acmeLegacyCertificateNamespace(name, namespace string) string {
	ns := strings.TrimSpace(namespace)
	if ns != "" {
		return strings.TrimRight(ns, "/")
	}
	return acmeCertificateNamespace(name, "")
}

func acmeAccountNamespace(name string) string {
	return "acme/account/" + name
}

func acmeSpecKey(namespace string) string {
	return strings.TrimRight(namespace, "/") + "/spec"
}

func acmeCurrentKey(namespace string) string {
	return strings.TrimRight(namespace, "/") + "/current"
}

func acmeBundleKey(namespace, digest string) string {
	return strings.TrimRight(namespace, "/") + "/bundles/" + digest
}

func acmeAttemptKey(namespace string) string {
	return strings.TrimRight(namespace, "/") + "/attempt"
}

func acmeRequestIndexKey(requestNamespace string) string {
	return acmeRequestNamespace(requestNamespace) + "/index"
}

func acmeCanonicalDomains(domains []string) ([]string, error) {
	seen := make(map[string]struct{})
	out := []string{}
	for _, domain := range domains {
		d := strings.ToLower(strings.TrimSpace(domain))
		if d == "" {
			return nil, fmt.Errorf("domain must not be empty")
		}
		if strings.HasSuffix(d, ".") {
			d = strings.TrimSuffix(d, ".")
		}
		if err := acmeValidateDNSIdentifier(d); err != nil {
			return nil, err
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

func acmeValidateDNSIdentifier(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain must not be empty")
	}
	base := domain
	if strings.HasPrefix(base, "*.") {
		base = strings.TrimPrefix(base, "*.")
		if base == "" {
			return fmt.Errorf("wildcard domain %q is invalid", domain)
		}
	}
	if strings.Contains(base, "*") {
		return fmt.Errorf("domain %q contains wildcard outside the leftmost label", domain)
	}
	if strings.HasPrefix(base, ".") || strings.HasSuffix(base, ".") {
		return fmt.Errorf("domain %q must not start or end with a dot", domain)
	}
	if len(base) > 253 {
		return fmt.Errorf("domain %q is too long", domain)
	}
	labels := strings.Split(base, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("domain %q contains an empty label", domain)
		}
		if len(label) > 63 {
			return fmt.Errorf("domain %q contains label %q longer than 63 bytes", domain, label)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("domain %q contains label %q with leading or trailing hyphen", domain, label)
		}
		for _, r := range label {
			if r >= 'a' && r <= 'z' {
				continue
			}
			if r >= '0' && r <= '9' {
				continue
			}
			if r == '-' {
				continue
			}
			return fmt.Errorf("domain %q contains invalid rune %q", domain, r)
		}
	}
	return nil
}

func acmeBuildSpec(domains []string, keyAlgorithm string, renewBefore uint64, privateKeyMode string) (*acmeCertSpec, error) {
	canon, err := acmeCanonicalDomains(domains)
	if err != nil {
		return nil, err
	}
	if len(canon) == 0 {
		return nil, fmt.Errorf("domains must not be empty")
	}
	alg := keyAlgorithm
	if alg == "" {
		alg = acmeDefaultKeyAlgorithm
	}
	if err := acmeValidateKeyAlgorithm(alg); err != nil {
		return nil, err
	}
	rb := renewBefore
	if rb == 0 {
		rb = acmeDefaultRenewBefore
	}
	mode := privateKeyMode
	if mode == "" {
		mode = acmeDefaultPrivateKeyMode
	}
	if mode != acmeDefaultPrivateKeyMode {
		return nil, fmt.Errorf("unsupported private_key_mode %q; only %q is implemented", mode, acmeDefaultPrivateKeyMode)
	}
	spec := &acmeCertSpec{
		Version:        acmeVersion,
		Domains:        canon,
		KeyAlgorithm:   alg,
		RenewBefore:    rb,
		PrivateKeyMode: mode,
	}
	digest, err := acmeJSONDigest(spec)
	if err != nil {
		return nil, err
	}
	spec.SpecDigest = digest
	return spec, nil
}

func acmeJSONDigest(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:]), nil
}

func acmeBundleDigest(bundle *acmeCertBundle) (string, error) {
	copy := *bundle
	copy.BundleDigest = ""
	return acmeJSONDigest(copy)
}

func acmeWorldGet(ctx context.Context, world engine.StrWorld, key string) (string, bool, error) {
	value, err := world.StrGet(ctx, key)
	if err != nil {
		if world.StrIsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

func acmeWorldReadJSON(ctx context.Context, world engine.StrWorld, key string, out interface{}) (bool, error) {
	value, exists, err := acmeWorldGet(ctx, world, key)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if err := json.Unmarshal([]byte(value), out); err != nil {
		return true, fmt.Errorf("could not decode world key %s: %w", key, err)
	}
	return true, nil
}

func acmeWorldWriteJSON(ctx context.Context, world engine.StrWorld, key string, value interface{}) (bool, error) {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return false, err
	}
	existing, exists, err := acmeWorldGet(ctx, world, key)
	if err != nil {
		return false, err
	}
	if exists && existing == string(b) {
		return true, nil
	}
	if err := world.StrSet(ctx, key, string(b)); err != nil {
		return false, err
	}
	return false, nil
}

func acmeWorldDelete(ctx context.Context, world engine.StrWorld, key string) error {
	if _, exists, err := acmeWorldGet(ctx, world, key); err != nil {
		return err
	} else if !exists {
		return nil
	}
	return world.StrDel(ctx, key)
}

func acmeWatchMany(ctx context.Context, init *engine.Init, keys []string, interval time.Duration) error {
	return acmeWatchWorld(ctx, init, keys, nil, interval)
}

func acmeWatchWorld(ctx context.Context, init *engine.Init, keys []string, mapKeys []string, interval time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if interval <= 0 {
		interval = 30 * time.Second
	}

	errs := make(chan error, len(keys)+len(mapKeys)+1)
	events := make(chan struct{}, len(keys)+len(mapKeys)+1)
	for _, key := range keys {
		if key == "" {
			continue
		}
		ch, err := init.World.StrWatch(ctx, key)
		if err != nil {
			return err
		}
		go func(key string, ch chan error) {
			for {
				select {
				case err, ok := <-ch:
					if !ok {
						return
					}
					if err != nil {
						errs <- fmt.Errorf("world watch %s: %w", key, err)
						return
					}
					select {
					case events <- struct{}{}:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(key, ch)
	}
	for _, key := range mapKeys {
		if key == "" {
			continue
		}
		ch, err := init.World.StrMapWatch(ctx, key)
		if err != nil {
			return err
		}
		go func(key string, ch chan error) {
			for {
				select {
				case err, ok := <-ch:
					if !ok {
						return
					}
					if err != nil {
						errs <- fmt.Errorf("world map watch %s: %w", key, err)
						return
					}
					select {
					case events <- struct{}{}:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(key, ch)
	}

	if err := init.Event(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case err := <-errs:
			return err
		case <-events:
			if err := init.Event(ctx); err != nil {
				return err
			}
		case <-ticker.C:
			if err := init.Event(ctx); err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func acmeRegisterCertificateRequest(ctx context.Context, init *engine.Init, requestNamespace, certificateName string) (bool, error) {
	key := acmeRequestIndexKey(requestNamespace)
	values, err := init.World.StrMapGet(ctx, key)
	if err != nil {
		return false, err
	}
	entry := &acmeRequestIndexEntry{Version: acmeVersion}
	if raw := values[init.Hostname]; raw != "" {
		if err := json.Unmarshal([]byte(raw), entry); err != nil {
			return false, fmt.Errorf("could not decode request index %s for host %s: %w", key, init.Hostname, err)
		}
		if entry.Version != acmeVersion {
			return false, fmt.Errorf("unsupported request index version %d", entry.Version)
		}
	}
	seen := make(map[string]struct{})
	out := []string{}
	for _, name := range entry.Certificates {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if _, ok := seen[certificateName]; !ok {
		out = append(out, certificateName)
	}
	sort.Strings(out)
	if reflect.DeepEqual(out, entry.Certificates) {
		return true, nil
	}
	next := &acmeRequestIndexEntry{
		Version:      acmeVersion,
		Certificates: out,
	}
	b, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	if err := init.World.StrMapSet(ctx, key, string(b)); err != nil {
		return false, err
	}
	return false, nil
}

func acmeCertificateRequestIndexed(ctx context.Context, init *engine.Init, requestNamespace, certificateName string) (bool, error) {
	values, err := init.World.StrMapGet(ctx, acmeRequestIndexKey(requestNamespace))
	if err != nil {
		return false, err
	}
	raw := values[init.Hostname]
	if raw == "" {
		return false, nil
	}
	var entry acmeRequestIndexEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		return false, fmt.Errorf("could not decode request index for host %s: %w", init.Hostname, err)
	}
	if entry.Version != acmeVersion {
		return false, fmt.Errorf("unsupported request index version %d", entry.Version)
	}
	for _, name := range entry.Certificates {
		if strings.TrimSpace(name) == certificateName {
			return true, nil
		}
	}
	return false, nil
}

func acmeResolveCertificateNames(ctx context.Context, init *engine.Init, requestNamespace string, certificates []string) ([]string, error) {
	if len(certificates) == 0 {
		return nil, fmt.Errorf("certificates must not be empty")
	}
	seen := make(map[string]struct{})
	names := []string{}
	add := func(name string) error {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("certificate name must not be empty")
		}
		if strings.Contains(name, "/") {
			return fmt.Errorf("certificate name %q must not contain slash", name)
		}
		if _, ok := seen[name]; ok {
			return nil
		}
		seen[name] = struct{}{}
		names = append(names, name)
		return nil
	}
	for _, certificate := range certificates {
		if strings.TrimSpace(certificate) != "*" {
			if err := add(certificate); err != nil {
				return nil, err
			}
			continue
		}
		values, err := init.World.StrMapGet(ctx, acmeRequestIndexKey(requestNamespace))
		if err != nil {
			return nil, err
		}
		hosts := make([]string, 0, len(values))
		for host := range values {
			hosts = append(hosts, host)
		}
		sort.Strings(hosts)
		for _, host := range hosts {
			var entry acmeRequestIndexEntry
			if err := json.Unmarshal([]byte(values[host]), &entry); err != nil {
				return nil, fmt.Errorf("could not decode request index for host %s: %w", host, err)
			}
			if entry.Version != acmeVersion {
				return nil, fmt.Errorf("unsupported request index version %d", entry.Version)
			}
			for _, name := range entry.Certificates {
				if err := add(name); err != nil {
					return nil, err
				}
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func acmeNewAttemptID() (string, error) {
	var randomBytes [6]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(randomBytes[:]), nil
}

func acmePublishAttempt(ctx context.Context, init *engine.Init, req *acmeCertificateRequest, presenter acmePresenter, record *acmeAttemptRecord) error {
	attemptID, err := acmeNewAttemptID()
	if err != nil {
		return err
	}
	ttl := presenter.attemptTTL()
	if ttl <= 0 {
		ttl = time.Duration(acmeDefaultAttemptTTL) * time.Second
	}
	record.Version = acmeVersion
	record.Certificate = req.Name
	record.AttemptID = attemptID
	record.Owner = presenter.owner()
	record.Phase = acmeAttemptPhasePresenting
	record.ChallengeType = presenter.challengeType()
	record.ExpiresAt = time.Now().Add(ttl).Unix()
	if _, err := acmeWorldWriteJSON(ctx, init.World, acmeAttemptKey(req.Namespace), record); err != nil {
		return err
	}
	return nil
}

func acmeClearAttempt(ctx context.Context, init *engine.Init, namespace string) error {
	return acmeWorldDelete(ctx, init.World, acmeAttemptKey(namespace))
}

func acmeListenPort(address string) int {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		if strings.HasPrefix(address, ":") {
			port = strings.TrimPrefix(address, ":")
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return n
}

func acmeValidateKeyAlgorithm(alg string) error {
	switch alg {
	case "ecdsa-p256", "ecdsa-p384", "rsa-2048", "rsa-3072", "rsa-4096":
		return nil
	default:
		return fmt.Errorf("unsupported key_algorithm %q", alg)
	}
}

func acmeGenerateSigner(alg string) (crypto.Signer, error) {
	switch alg {
	case "", "ecdsa-p256":
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "ecdsa-p384":
		return ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "rsa-2048":
		return rsa.GenerateKey(rand.Reader, 2048)
	case "rsa-3072":
		return rsa.GenerateKey(rand.Reader, 3072)
	case "rsa-4096":
		return rsa.GenerateKey(rand.Reader, 4096)
	default:
		return nil, fmt.Errorf("unsupported key_algorithm %q", alg)
	}
}

func acmeMarshalPrivateKeyPEM(key crypto.Signer) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func acmeParsePrivateKeyPEM(data []byte) (crypto.Signer, error) {
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		switch block.Type {
		case "PRIVATE KEY":
			key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			return acmeSignerFromParsed(key)
		case "EC PRIVATE KEY":
			key, err := x509.ParseECPrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			return key, nil
		case "RSA PRIVATE KEY":
			key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			return key, nil
		}
	}
	return nil, fmt.Errorf("no supported private key PEM block found")
}

func acmeSignerFromParsed(key interface{}) (crypto.Signer, error) {
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("parsed private key does not implement crypto.Signer")
	}
	switch signer.Public().(type) {
	case *rsa.PublicKey, *ecdsa.PublicKey:
		return signer, nil
	default:
		return nil, acme.ErrUnsupportedKey
	}
}

func acmeLoadOrCreatePrivateKey(path string, alg string, create bool, mode os.FileMode) (crypto.Signer, []byte, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		signer, err := acmeParsePrivateKeyPEM(data)
		return signer, data, true, err
	}
	if !os.IsNotExist(err) {
		return nil, nil, false, err
	}
	if !create {
		return nil, nil, false, nil
	}
	signer, err := acmeGenerateSigner(alg)
	if err != nil {
		return nil, nil, false, err
	}
	pemData, err := acmeMarshalPrivateKeyPEM(signer)
	if err != nil {
		return nil, nil, false, err
	}
	if err := acmeAtomicWriteFile(path, pemData, mode); err != nil {
		return nil, nil, false, err
	}
	return signer, pemData, true, nil
}

func acmeAtomicWriteFile(path string, data []byte, mode os.FileMode) error {
	if path == "" {
		return fmt.Errorf("path must not be empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mgmt-acme-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func acmeFileEqual(path string, data []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return bytes.Equal(existing, data), nil
}

func acmeParseCertificatesPEM(data []byte) ([]*x509.Certificate, error) {
	certs := []*x509.Certificate{}
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificates found")
	}
	return certs, nil
}

func acmeLeafFromBundle(bundle *acmeCertBundle) (*x509.Certificate, crypto.Signer, error) {
	certs, err := acmeParseCertificatesPEM([]byte(bundle.CertPEM))
	if err != nil {
		return nil, nil, err
	}
	signer, err := acmeParsePrivateKeyPEM([]byte(bundle.PrivateKey.PEM))
	if err != nil {
		return nil, nil, err
	}
	return certs[0], signer, nil
}

func acmePublicKeysEqual(a, b interface{}) bool {
	return reflect.DeepEqual(a, b)
}

func acmeCertMatchesSpec(cert *x509.Certificate, spec *acmeCertSpec) error {
	actual, err := acmeCanonicalDomains(cert.DNSNames)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, spec.Domains) {
		return fmt.Errorf("certificate DNSNames %v differ from desired domains %v", actual, spec.Domains)
	}
	now := time.Now()
	if now.Before(cert.NotBefore) {
		return fmt.Errorf("certificate is not valid before %s", cert.NotBefore.Format(time.RFC3339))
	}
	if !now.Before(cert.NotAfter) {
		return fmt.Errorf("certificate expired at %s", cert.NotAfter.Format(time.RFC3339))
	}
	if !now.Add(time.Duration(spec.RenewBefore) * time.Second).Before(cert.NotAfter) {
		return fmt.Errorf("certificate is inside renew_before window")
	}
	return nil
}

func acmeBundleUsable(bundle *acmeCertBundle, spec *acmeCertSpec) error {
	if bundle == nil {
		return fmt.Errorf("bundle is nil")
	}
	if bundle.Version != acmeVersion {
		return fmt.Errorf("unsupported bundle version %d", bundle.Version)
	}
	if bundle.SpecDigest != spec.SpecDigest {
		return fmt.Errorf("bundle spec digest differs")
	}
	if bundle.PrivateKey.Mode != acmeDefaultPrivateKeyMode {
		return fmt.Errorf("unsupported private key mode %q", bundle.PrivateKey.Mode)
	}
	cert, signer, err := acmeLeafFromBundle(bundle)
	if err != nil {
		return err
	}
	if !acmePublicKeysEqual(cert.PublicKey, signer.Public()) {
		return fmt.Errorf("certificate public key does not match private key")
	}
	return acmeCertMatchesSpec(cert, spec)
}

func acmePEMFromDER(der [][]byte) (certPEM string, chainPEM string, fullchainPEM string, leaf *x509.Certificate, err error) {
	if len(der) == 0 {
		return "", "", "", nil, fmt.Errorf("CA returned no certificates")
	}
	var certBuf bytes.Buffer
	var chainBuf bytes.Buffer
	var fullBuf bytes.Buffer
	for idx, certDER := range der {
		block := &pem.Block{Type: "CERTIFICATE", Bytes: certDER}
		if idx == 0 {
			if err := pem.Encode(&certBuf, block); err != nil {
				return "", "", "", nil, err
			}
		}
		if idx > 0 {
			if err := pem.Encode(&chainBuf, block); err != nil {
				return "", "", "", nil, err
			}
		}
		if err := pem.Encode(&fullBuf, block); err != nil {
			return "", "", "", nil, err
		}
	}
	leaf, err = x509.ParseCertificate(der[0])
	if err != nil {
		return "", "", "", nil, err
	}
	return certBuf.String(), chainBuf.String(), fullBuf.String(), leaf, nil
}

func acmeCreateCSRDER(signer crypto.Signer, domains []string) ([]byte, error) {
	tmpl := &x509.CertificateRequest{DNSNames: domains}
	if len(domains) > 0 && !strings.HasPrefix(domains[0], "*.") {
		tmpl.Subject.CommonName = domains[0]
	}
	return x509.CreateCertificateRequest(rand.Reader, tmpl, signer)
}

func acmeClientFromAccountInfo(info *acmeAccountInfo) (*acme.Client, crypto.Signer, error) {
	if info == nil {
		return nil, nil, fmt.Errorf("account info is nil")
	}
	if info.Directory == "" {
		return nil, nil, fmt.Errorf("account directory is empty")
	}
	if info.Key == "" {
		return nil, nil, fmt.Errorf("account key path is empty")
	}
	data, err := os.ReadFile(info.Key)
	if err != nil {
		return nil, nil, err
	}
	signer, err := acmeParsePrivateKeyPEM(data)
	if err != nil {
		return nil, nil, err
	}
	client := &acme.Client{
		Key:          signer,
		DirectoryURL: info.Directory,
		UserAgent:    "mgmt-acme",
	}
	if info.AccountURI != "" {
		client.KID = acme.KeyID(info.AccountURI)
	}
	return client, signer, nil
}

func acmeEnsureAccount(ctx context.Context, info *acmeAccountInfo) (*acme.Client, *acme.Account, error) {
	client, _, err := acmeClientFromAccountInfo(info)
	if err != nil {
		return nil, nil, err
	}
	acct, err := client.GetReg(ctx, "")
	if err == nil {
		return client, acct, nil
	}
	if err != acme.ErrNoAccount {
		return nil, nil, err
	}
	if !info.TermsOfServiceAgreed {
		return nil, nil, fmt.Errorf("ACME account does not exist and terms_of_service_agreed is false")
	}
	newAcct := &acme.Account{Contact: info.Contact}
	if info.EABKid != "" || info.EABHMACKeyFile != "" {
		if info.EABKid == "" || info.EABHMACKeyFile == "" {
			return nil, nil, fmt.Errorf("both eab_kid and eab_hmac_key_file must be set for external account binding")
		}
		key, err := os.ReadFile(info.EABHMACKeyFile)
		if err != nil {
			return nil, nil, err
		}
		newAcct.ExternalAccountBinding = &acme.ExternalAccountBinding{KID: info.EABKid, Key: bytes.TrimSpace(key)}
	}
	acct, err = client.Register(ctx, newAcct, func(tosURL string) bool {
		return info.TermsOfServiceAgreed
	})
	if err != nil {
		return nil, nil, err
	}
	if acct.URI != "" {
		client.KID = acme.KeyID(acct.URI)
	}
	return client, acct, nil
}

func acmeResolver(servers []string) *net.Resolver {
	if len(servers) == 0 {
		return net.DefaultResolver
	}
	server := servers[0]
	if !strings.Contains(server, ":") {
		server += ":53"
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := &net.Dialer{}
			return d.DialContext(ctx, network, server)
		},
	}
}

func acmeWaitForTXT(ctx context.Context, name, value string, resolvers []string, timeout, interval time.Duration) error {
	if timeout <= 0 {
		timeout = time.Duration(acmeDefaultPropagation) * time.Second
	}
	if interval <= 0 {
		interval = time.Duration(acmeDefaultPollInterval) * time.Second
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resolver := acmeResolver(resolvers)
	for {
		txts, err := resolver.LookupTXT(deadlineCtx, name)
		if err == nil {
			for _, txt := range txts {
				if txt == value {
					return nil
				}
			}
		}
		select {
		case <-time.After(interval):
		case <-deadlineCtx.Done():
			return fmt.Errorf("TXT record %s did not propagate expected value before timeout", name)
		}
	}
}
