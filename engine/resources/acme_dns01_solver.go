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
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	legochallenge "github.com/go-acme/lego/v4/challenge"
	legodns01 "github.com/go-acme/lego/v4/challenge/dns01"
	legodns "github.com/go-acme/lego/v4/providers/dns"
	"github.com/miekg/dns"
)

const acmeDNS01SolverKind = "acme:solver:dns01"

const (
	acmeDNSDefaultPropagationTimeout = 60 * time.Second
	acmeDNSDefaultPollingInterval    = 2 * time.Second
	acmeDNSQueryTimeout              = 10 * time.Second
	acmeDNSResolvConf                = "/etc/resolv.conf"
)

func init() {
	engine.RegisterResource(acmeDNS01SolverKind, func() engine.Res { return &AcmeDNS01SolverRes{} })
}

var acmeDNSProviderEnvMu sync.Mutex

// AcmeDNS01SolverRes presents dns-01 ACME challenge material through a
// lego-backed DNS provider.
type AcmeDNS01SolverRes struct {
	traits.Base
	traits.Sendable

	init *engine.Init

	// Provider selects the lego-backed DNS provider used to present the TXT
	// records for active challenges.
	Provider string `lang:"provider" yaml:"provider"`

	// Env contains provider environment variables passed through to the lego
	// DNS provider factory.
	Env map[string]string `lang:"env" yaml:"env"`

	providerFactory    func(string, map[string]string) (legochallenge.Provider, error)
	provider           legochallenge.Provider
	waitForPropagation func(context.Context, acmeDNS01Challenge) error

	mutex        *sync.RWMutex
	desired      map[string]acmeDNS01Challenge
	active       map[string]acmeDNS01Challenge
	presentation map[string]acmeDNS01PresentationEntry
}

// Default returns some sensible defaults for this resource.
func (obj *AcmeDNS01SolverRes) Default() engine.Res {
	return &AcmeDNS01SolverRes{}
}

func (obj *AcmeDNS01SolverRes) normalizedProvider() string {
	return strings.ToLower(strings.TrimSpace(obj.Provider))
}

func (obj *AcmeDNS01SolverRes) normalizedEnv() map[string]string {
	if len(obj.Env) == 0 {
		return nil
	}

	out := make(map[string]string, len(obj.Env))
	for key, value := range obj.Env {
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

// Validate checks if the resource data structure was populated correctly.
func (obj *AcmeDNS01SolverRes) Validate() error {
	if obj.normalizedProvider() == "" {
		return fmt.Errorf("the Provider field must not be empty")
	}
	for key := range obj.normalizedEnv() {
		if strings.Contains(key, "=") {
			return fmt.Errorf("invalid Env key: %q", key)
		}
	}
	if _, err := newLegoDNSChallengeProvider(obj.normalizedProvider(), obj.normalizedEnv()); err != nil {
		return errwrap.Wrapf(err, "invalid DNS challenge configuration")
	}
	return nil
}

// Init runs some startup code for this resource.
func (obj *AcmeDNS01SolverRes) Init(init *engine.Init) error {
	obj.init = init
	if obj.init.World == nil {
		return fmt.Errorf("the World API is required")
	}

	if obj.providerFactory == nil {
		obj.providerFactory = newLegoDNSChallengeProvider
	}

	provider, err := obj.providerFactory(obj.normalizedProvider(), obj.normalizedEnv())
	if err != nil {
		return errwrap.Wrapf(err, "could not create DNS challenge provider")
	}
	obj.provider = provider
	if obj.waitForPropagation == nil {
		obj.waitForPropagation = obj.waitForDNSPropagation
	}
	obj.mutex = &sync.RWMutex{}
	obj.desired = map[string]acmeDNS01Challenge{}
	obj.active = map[string]acmeDNS01Challenge{}
	obj.presentation = map[string]acmeDNS01PresentationEntry{}
	return nil
}

// Cleanup is run by the engine to clean up after the resource is done.
func (obj *AcmeDNS01SolverRes) Cleanup() error {
	if obj.init == nil || obj.init.World == nil {
		return nil
	}

	active := obj.activeSnapshot()
	for _, challenge := range active {
		if err := obj.provider.CleanUp(challenge.Domain, challenge.Token, challenge.KeyAuthorization); err != nil {
			return errwrap.Wrapf(err, "could not clean up dns-01 challenge for %q", challenge.Domain)
		}
	}
	return storeAcmeDNS01PresentationState(context.Background(), obj.init.World, obj.Name(), nil)
}

func (obj *AcmeDNS01SolverRes) desiredSnapshot() map[string]acmeDNS01Challenge {
	obj.mutex.RLock()
	defer obj.mutex.RUnlock()

	out := make(map[string]acmeDNS01Challenge, len(obj.desired))
	for key, challenge := range obj.desired {
		out[key] = challenge
	}
	return out
}

func (obj *AcmeDNS01SolverRes) activeSnapshot() map[string]acmeDNS01Challenge {
	obj.mutex.RLock()
	defer obj.mutex.RUnlock()

	out := make(map[string]acmeDNS01Challenge, len(obj.active))
	for key, challenge := range obj.active {
		out[key] = challenge
	}
	return out
}

func (obj *AcmeDNS01SolverRes) presentationSnapshot() map[string]acmeDNS01PresentationEntry {
	obj.mutex.RLock()
	defer obj.mutex.RUnlock()

	out := make(map[string]acmeDNS01PresentationEntry, len(obj.presentation))
	for key, entry := range obj.presentation {
		out[key] = entry
	}
	return out
}

func (obj *AcmeDNS01SolverRes) syncDesiredState(ctx context.Context) (bool, error) {
	currentDesired := obj.desiredSnapshot()

	state, err := loadAcmeDNS01ChallengeState(ctx, obj.init.World, obj.Name())
	if err != nil {
		return false, err
	}

	desired := map[string]acmeDNS01Challenge{}
	for key, challenge := range state.Challenges {
		desired[key] = challenge
	}

	obj.mutex.Lock()
	obj.desired = desired
	obj.mutex.Unlock()
	return !reflect.DeepEqual(currentDesired, desired), nil
}

func (obj *AcmeDNS01SolverRes) reconcile(ctx context.Context) error {
	desired := obj.desiredSnapshot()
	active := obj.activeSnapshot()

	nextActive := map[string]acmeDNS01Challenge{}
	nextPresentation := map[string]acmeDNS01PresentationEntry{}

	for key, challenge := range active {
		if _, exists := desired[key]; exists {
			continue
		}
		if err := obj.provider.CleanUp(challenge.Domain, challenge.Token, challenge.KeyAuthorization); err != nil {
			return errwrap.Wrapf(err, "could not clean up dns-01 challenge for %q", challenge.Domain)
		}
	}

	for key, challenge := range desired {
		entry := acmeDNS01PresentationEntry{
			Attempt: challenge.Attempt,
			Domain:  challenge.Domain,
			FQDN:    challenge.FQDN,
			Value:   challenge.Value,
		}

		if current, exists := active[key]; exists && current == challenge {
			nextActive[key] = challenge
			entry.Ready = true
			nextPresentation[key] = entry
			continue
		}

		if err := obj.provider.Present(challenge.Domain, challenge.Token, challenge.KeyAuthorization); err != nil {
			entry.Error = err.Error()
			nextPresentation[key] = entry
			continue
		}
		if err := obj.waitForPropagation(ctx, challenge); err != nil {
			entry.Error = err.Error()
			nextPresentation[key] = entry
			continue
		}

		nextActive[key] = challenge
		entry.Ready = true
		nextPresentation[key] = entry
	}

	if err := storeAcmeDNS01PresentationState(ctx, obj.init.World, obj.Name(), &acmeDNS01PresentationState{
		Entries: nextPresentation,
	}); err != nil {
		return errwrap.Wrapf(err, "could not store DNS-01 presentation state")
	}

	obj.mutex.Lock()
	obj.active = nextActive
	obj.presentation = nextPresentation
	obj.mutex.Unlock()
	return nil
}

// Watch is the primary listener for this resource and it outputs events.
func (obj *AcmeDNS01SolverRes) Watch(ctx context.Context) error {
	ch, err := obj.init.World.StrWatch(ctx, acmeDNS01ChallengeStateKey(obj.Name()))
	if err != nil {
		return err
	}

	if _, err := obj.syncDesiredState(ctx); err != nil {
		return err
	}
	if err := obj.init.Event(ctx); err != nil {
		return err
	}

	for {
		select {
		case err, ok := <-ch:
			if !ok {
				return nil
			}
			if err != nil {
				return err
			}
			changed, err := obj.syncDesiredState(ctx)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}

		case <-ctx.Done():
			return nil
		}

		if err := obj.init.Event(ctx); err != nil {
			return err
		}
	}
}

// AcmeDNS01SolverSends is the struct of data which is sent after a successful
// Apply.
type AcmeDNS01SolverSends struct {
	Pending        bool   `lang:"pending"`
	Ready          bool   `lang:"ready"`
	ChallengeCount int64  `lang:"challenge_count"`
	Error          string `lang:"error"`
}

// Sends represents the default struct of values we can send using Send/Recv.
func (obj *AcmeDNS01SolverRes) Sends() interface{} {
	return &AcmeDNS01SolverSends{}
}

// CheckApply reconciles DNS-01 presentation with the desired world state.
func (obj *AcmeDNS01SolverRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	if apply {
		if err := obj.reconcile(ctx); err != nil {
			return false, err
		}
	}

	desired := obj.desiredSnapshot()
	presentation := obj.presentationSnapshot()
	challengeCount := len(desired)
	ready := challengeCount > 0
	var firstError string
	for key := range desired {
		entry, exists := presentation[key]
		if !exists || !entry.Ready {
			ready = false
		}
		if exists && entry.Error != "" {
			if firstError == "" {
				firstError = entry.Error
			}
			ready = false
		}
	}

	if err := obj.init.Send(&AcmeDNS01SolverSends{
		Pending:        challengeCount > 0,
		Ready:          ready,
		ChallengeCount: int64(challengeCount),
		Error:          firstError,
	}); err != nil {
		return false, err
	}

	return challengeCount == 0, nil
}

// Cmp compares two resources and returns an error if they are not equivalent.
func (obj *AcmeDNS01SolverRes) Cmp(r engine.Res) error {
	res, ok := r.(*AcmeDNS01SolverRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}

	if obj.normalizedProvider() != res.normalizedProvider() {
		return fmt.Errorf("the Provider field differs")
	}
	if !mapsEqual(obj.normalizedEnv(), res.normalizedEnv()) {
		return fmt.Errorf("the Env field differs")
	}

	return nil
}

func newLegoDNSChallengeProvider(provider string, env map[string]string) (legochallenge.Provider, error) {
	var result legochallenge.Provider
	err := withLegoDNSEnv(env, func() error {
		var err error
		result, err = legodns.NewDNSChallengeProviderByName(provider)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (obj *AcmeDNS01SolverRes) waitForDNSPropagation(ctx context.Context, challenge acmeDNS01Challenge) error {
	timeout, interval := acmeDNSDefaultPropagationTimeout, acmeDNSDefaultPollingInterval
	if provider, ok := obj.provider.(legochallenge.ProviderTimeout); ok {
		timeout, interval = provider.Timeout()
	}

	return acmeWaitForDNSPropagation(ctx, challenge, timeout, interval, acmeDNS01TXTVisible)
}

func acmeWaitForDuration(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	timer := time.NewTimer(duration)
	defer acmeStopTimer(timer)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func acmeStopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func acmeWaitForDNSPropagation(ctx context.Context, challenge acmeDNS01Challenge, timeout, interval time.Duration, dnsTXTVisible func(string, string, []string) (bool, error)) error {
	if err := acmeWaitForDuration(ctx, interval); err != nil {
		return err
	}

	recursiveNameservers := acmeDNSRecursiveNameservers()
	timeoutTimer := time.NewTimer(timeout)
	defer acmeStopTimer(timeoutTimer)

	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		select {
		case <-timeoutTimer.C:
			return acmeDNSPropagationTimeoutError(lastErr)
		default:
		}

		stop, err := dnsTXTVisible(challenge.FQDN, challenge.Value, recursiveNameservers)
		if stop {
			return err
		}
		if err != nil {
			lastErr = err
		}

		intervalTimer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			acmeStopTimer(intervalTimer)
			return ctx.Err()
		case <-timeoutTimer.C:
			acmeStopTimer(intervalTimer)
			return acmeDNSPropagationTimeoutError(lastErr)
		case <-intervalTimer.C:
		}
	}
}

func acmeDNSPropagationTimeoutError(lastErr error) error {
	if lastErr == nil {
		return fmt.Errorf("dns-01 propagation: time limit exceeded")
	}

	return fmt.Errorf("dns-01 propagation: time limit exceeded: last error: %w", lastErr)
}

func acmeDNSRecursiveNameservers() []string {
	config, err := dns.ClientConfigFromFile(acmeDNSResolvConf)
	if err == nil && len(config.Servers) > 0 {
		return legodns01.ParseNameservers(config.Servers)
	}
	return []string{
		"google-public-dns-a.google.com:53",
		"google-public-dns-b.google.com:53",
	}
}

func acmeDNS01TXTVisible(fqdn, value string, recursiveNameservers []string) (bool, error) {
	zone, err := legodns01.FindZoneByFqdnCustom(fqdn, recursiveNameservers)
	if err != nil {
		return false, err
	}

	msg, err := acmeDNSQuery(zone, dns.TypeNS, recursiveNameservers, true)
	if err != nil {
		return false, err
	}

	authoritative := []string{}
	for _, answer := range msg.Answer {
		nsRecord, ok := answer.(*dns.NS)
		if !ok {
			continue
		}
		authoritative = append(authoritative, strings.ToLower(nsRecord.Ns))
	}
	if len(authoritative) == 0 {
		return false, fmt.Errorf("could not determine authoritative nameservers for %s", fqdn)
	}

	for _, nameserver := range authoritative {
		server := net.JoinHostPort(nameserver, "53")
		response, err := acmeDNSQuery(fqdn, dns.TypeTXT, []string{server}, false)
		if err != nil {
			return false, err
		}
		if response.Rcode != dns.RcodeSuccess {
			return false, fmt.Errorf("NS %s returned %s for %s", server, dns.RcodeToString[response.Rcode], fqdn)
		}

		found := false
		records := []string{}
		for _, answer := range response.Answer {
			txt, ok := answer.(*dns.TXT)
			if !ok {
				continue
			}
			record := strings.Join(txt.Txt, "")
			records = append(records, record)
			if record == value {
				found = true
				break
			}
		}
		if !found {
			return false, fmt.Errorf("NS %s did not return the expected TXT record [fqdn: %s, value: %s]: %s", server, fqdn, value, strings.Join(records, " ,"))
		}
	}

	return true, nil
}

func acmeDNSQuery(fqdn string, rtype uint16, nameservers []string, recursive bool) (*dns.Msg, error) {
	msg := new(dns.Msg)
	msg.SetQuestion(fqdn, rtype)
	msg.SetEdns0(4096, false)
	if !recursive {
		msg.RecursionDesired = false
	}

	if len(nameservers) == 0 {
		return nil, fmt.Errorf("empty list of nameservers")
	}

	var result *dns.Msg
	var err error
	var errs error
	for _, nameserver := range nameservers {
		result, err = acmeDNSSendQuery(msg, nameserver)
		if err == nil && len(result.Answer) > 0 {
			break
		}
		errs = errors.Join(errs, err)
	}
	if err != nil {
		return result, errs
	}

	return result, nil
}

func acmeDNSSendQuery(msg *dns.Msg, nameserver string) (*dns.Msg, error) {
	udp := &dns.Client{Net: "udp", Timeout: acmeDNSQueryTimeout}
	response, _, err := udp.Exchange(msg, nameserver)
	if response != nil && response.Truncated {
		tcp := &dns.Client{Net: "tcp", Timeout: acmeDNSQueryTimeout}
		response, _, err = tcp.Exchange(msg, nameserver)
	}
	if err != nil {
		return response, fmt.Errorf("DNS call error [ns=%s]: %w", nameserver, err)
	}
	return response, nil
}

func withLegoDNSEnv(env map[string]string, fn func() error) error {
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

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
