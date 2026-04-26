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
	"fmt"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"golang.org/x/crypto/acme"
)

func init() {
	engine.RegisterResource(KindAcmeSolverHTTP01, func() engine.Res { return &AcmeSolverHTTP01Res{} })
}

// AcmeSolverHTTP01Res is a scheduled ACME issuer worker which solves http-01
// challenges by owning a temporary HTTP listener for each issuance attempt.
type AcmeSolverHTTP01Res struct {
	traits.Base

	init *engine.Init

	// Account is the acme:account resource name. The account resource publishes
	// its non-secret configuration into World under acme/account/<name>.
	Account string `lang:"account" yaml:"account"`
	// RequestNamespace is the World namespace containing certificate requests.
	RequestNamespace string `lang:"request_namespace" yaml:"request_namespace"`
	// Certificates are request names this solver may issue, or "*" for all
	// indexed requests in RequestNamespace.
	Certificates []string `lang:"certificates" yaml:"certificates"`

	// Listen is the public HTTP listen address. HTTP-01 validation requires TCP
	// port 80, so the default is :80.
	Listen string `lang:"listen" yaml:"listen"`
	// Hosts is the set of DNS identifiers this solver is allowed to answer for.
	Hosts []string `lang:"hosts" yaml:"hosts"`

	AttemptTTL          uint64 `lang:"attempt_ttl" yaml:"attempt_ttl"`
	PresentationTimeout uint64 `lang:"presentation_timeout" yaml:"presentation_timeout"`
	PresentationSettle  uint64 `lang:"presentation_settle" yaml:"presentation_settle"`
	Cooldown            uint64 `lang:"cooldown" yaml:"cooldown"`

	mu         *sync.RWMutex
	challenges map[string]string
	cooldowns  map[string]time.Time
}

// Default returns some sensible defaults for this resource.
func (obj *AcmeSolverHTTP01Res) Default() engine.Res {
	return &AcmeSolverHTTP01Res{
		RequestNamespace:    acmeDefaultRequestNamespace,
		Listen:              acmeDefaultHTTP01Listen,
		AttemptTTL:          acmeDefaultAttemptTTL,
		PresentationTimeout: acmeDefaultPresentationTimeout,
		PresentationSettle:  acmeDefaultPresentationSettle,
		Cooldown:            acmeDefaultCooldown,
	}
}

// Validate if the params passed in are valid data.
func (obj *AcmeSolverHTTP01Res) Validate() error {
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
	if len(obj.Hosts) == 0 {
		return fmt.Errorf("hosts must not be empty")
	}
	for _, host := range obj.Hosts {
		name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		if strings.HasPrefix(name, "*.") {
			return fmt.Errorf("hosts must not contain wildcard identifier %q", host)
		}
		if err := acmeValidateDNSIdentifier(name); err != nil {
			return err
		}
	}
	return nil
}

// Init initializes the resource.
func (obj *AcmeSolverHTTP01Res) Init(init *engine.Init) error {
	obj.init = init
	obj.mu = &sync.RWMutex{}
	obj.challenges = make(map[string]string)
	obj.cooldowns = make(map[string]time.Time)
	return nil
}

// Cleanup is run by the engine to clean up after the resource is done.
func (obj *AcmeSolverHTTP01Res) Cleanup() error { return nil }

// Watch is the primary listener for this resource and it outputs events.
func (obj *AcmeSolverHTTP01Res) Watch(ctx context.Context) error {
	return acmeSolverWatch(ctx, obj.init, obj.requestNamespace(), obj.Certificates)
}

func (obj *AcmeSolverHTTP01Res) requestNamespace() string {
	return acmeRequestNamespace(obj.RequestNamespace)
}

func (obj *AcmeSolverHTTP01Res) listen() string {
	if obj.Listen == "" {
		return acmeDefaultHTTP01Listen
	}
	return obj.Listen
}

func (obj *AcmeSolverHTTP01Res) attemptTTL() time.Duration {
	seconds := obj.AttemptTTL
	if seconds == 0 {
		seconds = acmeDefaultAttemptTTL
	}
	return time.Duration(seconds) * time.Second
}

func (obj *AcmeSolverHTTP01Res) presentationTimeout() time.Duration {
	seconds := obj.PresentationTimeout
	if seconds == 0 {
		seconds = acmeDefaultPresentationTimeout
	}
	return time.Duration(seconds) * time.Second
}

func (obj *AcmeSolverHTTP01Res) presentationSettle() time.Duration {
	return time.Duration(obj.PresentationSettle) * time.Second
}

func (obj *AcmeSolverHTTP01Res) cooldownDuration() time.Duration {
	seconds := obj.Cooldown
	if seconds == 0 {
		seconds = acmeDefaultCooldown
	}
	return time.Duration(seconds) * time.Second
}

func (obj *AcmeSolverHTTP01Res) cooldownUntil(namespace string) time.Time {
	return obj.cooldowns[namespace]
}

func (obj *AcmeSolverHTTP01Res) setCooldown(namespace string, until time.Time) {
	obj.cooldowns[namespace] = until
}

func (obj *AcmeSolverHTTP01Res) owner() string {
	return obj.init.Hostname + "/" + obj.Kind() + "[" + obj.Name() + "]"
}

func (obj *AcmeSolverHTTP01Res) hostSet() map[string]struct{} {
	out := make(map[string]struct{})
	for _, host := range obj.Hosts {
		out[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))] = struct{}{}
	}
	return out
}

func (obj *AcmeSolverHTTP01Res) challengeType() string { return acmeChallengeHTTP01 }

func (obj *AcmeSolverHTTP01Res) prepare(ctx context.Context) (func(context.Context) error, bool, error) {
	ln, err := net.Listen("tcp", obj.listen())
	if err != nil {
		obj.init.Logf("http-01 solver %s cannot bind %s: %v", obj.Name(), obj.listen(), err)
		return nil, false, nil
	}
	obj.mu.Lock()
	obj.challenges = make(map[string]string)
	obj.mu.Unlock()
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/acme-challenge/", obj.serveChallenge)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			obj.init.Logf("http-01 solver %s listener stopped: %v", obj.Name(), err)
		}
	}()
	cleanup := func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		err := server.Shutdown(shutdownCtx)
		select {
		case <-done:
		case <-shutdownCtx.Done():
			if err != nil {
				return err
			}
			return shutdownCtx.Err()
		}
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
	return cleanup, true, nil
}

func (obj *AcmeSolverHTTP01Res) serveChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")
	if token == "" || strings.Contains(token, "/") || strings.Contains(token, "..") {
		http.NotFound(w, r)
		return
	}
	obj.mu.RLock()
	body, ok := obj.challenges[token]
	obj.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(body))
}

func (obj *AcmeSolverHTTP01Res) canSolve(spec *acmeCertSpec) (bool, error) {
	hosts := obj.hostSet()
	for _, domain := range spec.Domains {
		if strings.HasPrefix(domain, "*.") {
			return false, nil
		}
		if _, ok := hosts[domain]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func (obj *AcmeSolverHTTP01Res) present(ctx context.Context, req *acmeCertificateRequest, client *acme.Client, authz *acme.Authorization, chal *acme.Challenge) (func(context.Context) error, error) {
	if authz.Wildcard || strings.HasPrefix(authz.Identifier.Value, "*.") {
		return nil, fmt.Errorf("http-01 cannot solve wildcard authorization for %s", authz.Identifier.Value)
	}
	response, err := client.HTTP01ChallengeResponse(chal.Token)
	if err != nil {
		return nil, err
	}
	path := client.HTTP01ChallengePath(chal.Token)
	if !strings.HasPrefix(path, "/.well-known/acme-challenge/") {
		return nil, fmt.Errorf("ACME http-01 path %q does not match expected prefix", path)
	}
	tokenName := strings.TrimPrefix(path, "/.well-known/acme-challenge/")
	if tokenName == "" || strings.Contains(tokenName, "/") || strings.Contains(tokenName, "..") {
		return nil, fmt.Errorf("invalid http-01 token path %q", tokenName)
	}
	obj.mu.Lock()
	obj.challenges[tokenName] = response
	obj.mu.Unlock()
	port := acmeListenPort(obj.listen())
	record := &acmeAttemptRecord{
		Domain: strings.ToLower(strings.TrimSuffix(authz.Identifier.Value, ".")),
		Port:   port,
	}
	if err := acmePublishAttempt(ctx, obj.init, req, obj, record); err != nil {
		obj.mu.Lock()
		delete(obj.challenges, tokenName)
		obj.mu.Unlock()
		return nil, err
	}
	cleanup := func(ctx context.Context) error {
		obj.mu.Lock()
		delete(obj.challenges, tokenName)
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

// CheckApply method for resource.
func (obj *AcmeSolverHTTP01Res) CheckApply(ctx context.Context, apply bool) (bool, error) {
	return acmeSolverCheckApply(ctx, obj.init, obj.Account, obj.requestNamespace(), obj.Certificates, obj, apply)
}

// Cmp compares two resources and returns an error if they are not equivalent.
func (obj *AcmeSolverHTTP01Res) Cmp(r engine.Res) error {
	res, ok := r.(*AcmeSolverHTTP01Res)
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
	if !reflect.DeepEqual(obj.Hosts, res.Hosts) {
		return fmt.Errorf("the Hosts differs")
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

// AcmeSolverHTTP01UID is the UID struct for AcmeSolverHTTP01Res.
type AcmeSolverHTTP01UID struct {
	engine.BaseUID
	name string
}

// UIDs includes all params to make a unique identification of this object.
func (obj *AcmeSolverHTTP01Res) UIDs() []engine.ResUID {
	x := &AcmeSolverHTTP01UID{
		BaseUID: engine.BaseUID{Name: obj.Name(), Kind: obj.Kind()},
		name:    obj.Name(),
	}
	return []engine.ResUID{x}
}

// UnmarshalYAML is the custom unmarshal handler for this struct.
func (obj *AcmeSolverHTTP01Res) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawRes AcmeSolverHTTP01Res
	def := obj.Default()
	res, ok := def.(*AcmeSolverHTTP01Res)
	if !ok {
		return fmt.Errorf("could not convert to AcmeSolverHTTP01Res")
	}
	raw := rawRes(*res)
	if err := unmarshal(&raw); err != nil {
		return err
	}
	*obj = AcmeSolverHTTP01Res(raw)
	return nil
}
