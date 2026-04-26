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
	"crypto/tls"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"golang.org/x/crypto/acme"
)

func init() {
	engine.RegisterResource(KindAcmeSolverTLSALPN01, func() engine.Res { return &AcmeSolverTLSALPN01Res{} })
}

// AcmeSolverTLSALPN01Res is a scheduled ACME issuer worker which solves
// tls-alpn-01 challenges by owning a temporary TLS listener for each issuance
// attempt.
type AcmeSolverTLSALPN01Res struct {
	traits.Base

	init *engine.Init

	Account          string   `lang:"account" yaml:"account"`
	RequestNamespace string   `lang:"request_namespace" yaml:"request_namespace"`
	Certificates     []string `lang:"certificates" yaml:"certificates"`

	Listen      string   `lang:"listen" yaml:"listen"`
	ServerNames []string `lang:"server_names" yaml:"server_names"`

	AttemptTTL          uint64 `lang:"attempt_ttl" yaml:"attempt_ttl"`
	PresentationTimeout uint64 `lang:"presentation_timeout" yaml:"presentation_timeout"`
	PresentationSettle  uint64 `lang:"presentation_settle" yaml:"presentation_settle"`
	Cooldown            uint64 `lang:"cooldown" yaml:"cooldown"`

	mu                 *sync.RWMutex
	certificatesByName map[string]tls.Certificate
	cooldowns          map[string]time.Time
}

func (obj *AcmeSolverTLSALPN01Res) Default() engine.Res {
	return &AcmeSolverTLSALPN01Res{
		RequestNamespace:    acmeDefaultRequestNamespace,
		Listen:              acmeDefaultTLSALPN01Listen,
		AttemptTTL:          acmeDefaultAttemptTTL,
		PresentationTimeout: acmeDefaultPresentationTimeout,
		PresentationSettle:  acmeDefaultPresentationSettle,
		Cooldown:            acmeDefaultCooldown,
	}
}

func (obj *AcmeSolverTLSALPN01Res) Validate() error {
	if obj.Account == "" {
		return fmt.Errorf("account must not be empty")
	}
	if obj.requestNamespace() == "" {
		return fmt.Errorf("request_namespace must not be empty")
	}
	if len(obj.Certificates) == 0 {
		return fmt.Errorf("certificates must not be empty")
	}
	for _, name := range obj.Certificates {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("certificate name must not be empty")
		}
		if name != "*" && strings.Contains(name, "/") {
			return fmt.Errorf("certificate name %q must not contain slash", name)
		}
	}
	if obj.listen() == "" {
		return fmt.Errorf("listen must not be empty")
	}
	if len(obj.ServerNames) == 0 {
		return fmt.Errorf("server_names must not be empty")
	}
	for _, name := range obj.ServerNames {
		serverName := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
		if strings.HasPrefix(serverName, "*.") {
			return fmt.Errorf("server_names must not contain wildcard identifier %q", name)
		}
		if err := acmeValidateDNSIdentifier(serverName); err != nil {
			return err
		}
	}
	return nil
}

func (obj *AcmeSolverTLSALPN01Res) Init(init *engine.Init) error {
	obj.init = init
	obj.mu = &sync.RWMutex{}
	obj.certificatesByName = make(map[string]tls.Certificate)
	obj.cooldowns = make(map[string]time.Time)
	return nil
}

func (obj *AcmeSolverTLSALPN01Res) Cleanup() error { return nil }

func (obj *AcmeSolverTLSALPN01Res) Watch(ctx context.Context) error {
	return acmeSolverWatch(ctx, obj.init, obj.requestNamespace(), obj.Certificates)
}

func (obj *AcmeSolverTLSALPN01Res) requestNamespace() string {
	return acmeRequestNamespace(obj.RequestNamespace)
}

func (obj *AcmeSolverTLSALPN01Res) listen() string {
	if obj.Listen == "" {
		return acmeDefaultTLSALPN01Listen
	}
	return obj.Listen
}

func (obj *AcmeSolverTLSALPN01Res) attemptTTL() time.Duration {
	seconds := obj.AttemptTTL
	if seconds == 0 {
		seconds = acmeDefaultAttemptTTL
	}
	return time.Duration(seconds) * time.Second
}

func (obj *AcmeSolverTLSALPN01Res) presentationTimeout() time.Duration {
	seconds := obj.PresentationTimeout
	if seconds == 0 {
		seconds = acmeDefaultPresentationTimeout
	}
	return time.Duration(seconds) * time.Second
}

func (obj *AcmeSolverTLSALPN01Res) presentationSettle() time.Duration {
	return time.Duration(obj.PresentationSettle) * time.Second
}

func (obj *AcmeSolverTLSALPN01Res) cooldownDuration() time.Duration {
	seconds := obj.Cooldown
	if seconds == 0 {
		seconds = acmeDefaultCooldown
	}
	return time.Duration(seconds) * time.Second
}

func (obj *AcmeSolverTLSALPN01Res) cooldownUntil(namespace string) time.Time {
	return obj.cooldowns[namespace]
}

func (obj *AcmeSolverTLSALPN01Res) setCooldown(namespace string, until time.Time) {
	obj.cooldowns[namespace] = until
}

func (obj *AcmeSolverTLSALPN01Res) owner() string {
	return obj.init.Hostname + "/" + obj.Kind() + "[" + obj.Name() + "]"
}

func (obj *AcmeSolverTLSALPN01Res) challengeType() string { return acmeChallengeTLSALPN01 }

func (obj *AcmeSolverTLSALPN01Res) serverNameSet() map[string]struct{} {
	out := make(map[string]struct{})
	for _, name := range obj.ServerNames {
		out[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))] = struct{}{}
	}
	return out
}

func (obj *AcmeSolverTLSALPN01Res) canSolve(spec *acmeCertSpec) (bool, error) {
	serverNames := obj.serverNameSet()
	for _, domain := range spec.Domains {
		if strings.HasPrefix(domain, "*.") {
			return false, nil
		}
		if _, ok := serverNames[domain]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func (obj *AcmeSolverTLSALPN01Res) prepare(ctx context.Context) (func(context.Context) error, bool, error) {
	ln, err := net.Listen("tcp", obj.listen())
	if err != nil {
		obj.init.Logf("tls-alpn-01 solver %s cannot bind %s: %v", obj.Name(), obj.listen(), err)
		return nil, false, nil
	}
	obj.mu.Lock()
	obj.certificatesByName = make(map[string]tls.Certificate)
	obj.mu.Unlock()

	config := &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"acme-tls/1"},
		GetCertificate: obj.getCertificate,
	}
	tlsListener := tls.NewListener(ln, config)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := tlsListener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				if tlsConn, ok := conn.(*tls.Conn); ok {
					_ = tlsConn.Handshake()
				}
			}(conn)
		}
	}()
	cleanup := func(ctx context.Context) error {
		_ = ln.Close()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return cleanup, true, nil
}

func (obj *AcmeSolverTLSALPN01Res) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	hasACMEALPN := false
	for _, proto := range hello.SupportedProtos {
		if proto == "acme-tls/1" {
			hasACMEALPN = true
			break
		}
	}
	if !hasACMEALPN {
		return nil, fmt.Errorf("client did not offer acme-tls/1")
	}
	serverName := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	if serverName == "" {
		return nil, fmt.Errorf("client did not send SNI")
	}
	obj.mu.RLock()
	cert, ok := obj.certificatesByName[serverName]
	obj.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no tls-alpn-01 certificate for %s", serverName)
	}
	return &cert, nil
}

func (obj *AcmeSolverTLSALPN01Res) present(ctx context.Context, req *acmeCertificateRequest, client *acme.Client, authz *acme.Authorization, chal *acme.Challenge) (func(context.Context) error, error) {
	if authz.Wildcard || strings.HasPrefix(authz.Identifier.Value, "*.") {
		return nil, fmt.Errorf("tls-alpn-01 cannot solve wildcard authorization for %s", authz.Identifier.Value)
	}
	domain := strings.ToLower(strings.TrimSuffix(authz.Identifier.Value, "."))
	cert, err := client.TLSALPN01ChallengeCert(chal.Token, domain)
	if err != nil {
		return nil, err
	}
	obj.mu.Lock()
	obj.certificatesByName[domain] = cert
	obj.mu.Unlock()
	record := &acmeAttemptRecord{
		Domain: domain,
		Port:   acmeListenPort(obj.listen()),
	}
	if err := acmePublishAttempt(ctx, obj.init, req, obj, record); err != nil {
		obj.mu.Lock()
		delete(obj.certificatesByName, domain)
		obj.mu.Unlock()
		return nil, err
	}
	cleanup := func(ctx context.Context) error {
		obj.mu.Lock()
		delete(obj.certificatesByName, domain)
		obj.mu.Unlock()
		return acmeClearAttempt(ctx, obj.init, req.Namespace)
	}
	if settle := obj.presentationSettle(); settle > 0 {
		select {
		case <-time.After(settle):
		case <-ctx.Done():
			_ = cleanup(context.Background())
			return nil, ctx.Err()
		}
	}
	return cleanup, nil
}

func (obj *AcmeSolverTLSALPN01Res) CheckApply(ctx context.Context, apply bool) (bool, error) {
	return acmeSolverCheckApply(ctx, obj.init, obj.Account, obj.requestNamespace(), obj.Certificates, obj, apply)
}

func (obj *AcmeSolverTLSALPN01Res) Cmp(r engine.Res) error {
	res, ok := r.(*AcmeSolverTLSALPN01Res)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.Account != res.Account {
		return fmt.Errorf("the Account differs")
	}
	if obj.requestNamespace() != res.requestNamespace() {
		return fmt.Errorf("the RequestNamespace differs")
	}
	if !reflect.DeepEqual(obj.Certificates, res.Certificates) {
		return fmt.Errorf("the Certificates differs")
	}
	if obj.listen() != res.listen() {
		return fmt.Errorf("the Listen differs")
	}
	if !reflect.DeepEqual(obj.ServerNames, res.ServerNames) {
		return fmt.Errorf("the ServerNames differs")
	}
	if obj.attemptTTL() != res.attemptTTL() {
		return fmt.Errorf("the AttemptTTL differs")
	}
	if obj.presentationTimeout() != res.presentationTimeout() {
		return fmt.Errorf("the PresentationTimeout differs")
	}
	if obj.presentationSettle() != res.presentationSettle() {
		return fmt.Errorf("the PresentationSettle differs")
	}
	if obj.cooldownDuration() != res.cooldownDuration() {
		return fmt.Errorf("the Cooldown differs")
	}
	return nil
}

type AcmeSolverTLSALPN01UID struct {
	engine.BaseUID
	name string
}

func (obj *AcmeSolverTLSALPN01Res) UIDs() []engine.ResUID {
	x := &AcmeSolverTLSALPN01UID{
		BaseUID: engine.BaseUID{Name: obj.Name(), Kind: obj.Kind()},
		name:    obj.Name(),
	}
	return []engine.ResUID{x}
}

func (obj *AcmeSolverTLSALPN01Res) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawRes AcmeSolverTLSALPN01Res
	def := obj.Default()
	res, ok := def.(*AcmeSolverTLSALPN01Res)
	if !ok {
		return fmt.Errorf("could not convert to AcmeSolverTLSALPN01Res")
	}
	raw := rawRes(*res)
	if err := unmarshal(&raw); err != nil {
		return err
	}
	*obj = AcmeSolverTLSALPN01Res(raw)
	return nil
}
