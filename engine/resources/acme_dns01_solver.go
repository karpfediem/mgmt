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
	"os"
	"strings"
	"sync"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	legochallenge "github.com/go-acme/lego/v4/challenge"
	legodns "github.com/go-acme/lego/v4/providers/dns"
)

const acmeDNS01SolverKind = "acme:solver:dns01"

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

	providerFactory func(string, map[string]string) (legochallenge.Provider, error)
	provider        legochallenge.Provider

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

func (obj *AcmeDNS01SolverRes) syncDesiredState(ctx context.Context) error {
	state, err := loadAcmeDNS01ChallengeState(ctx, obj.init.World, obj.Name())
	if err != nil {
		return err
	}

	desired := map[string]acmeDNS01Challenge{}
	for key, challenge := range state.Challenges {
		desired[key] = challenge
	}

	obj.mutex.Lock()
	obj.desired = desired
	obj.mutex.Unlock()
	return nil
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

	if err := obj.syncDesiredState(ctx); err != nil {
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
			if err := obj.syncDesiredState(ctx); err != nil {
				return err
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
