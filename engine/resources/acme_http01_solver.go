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
	"encoding/gob"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/go-acme/lego/v4/challenge/http01"
)

const (
	acmeHTTP01SolverKind       = "acme:solver:http01"
	acmeHTTP01ProviderTimeout  = 30 * time.Second
	acmeHTTP01ProviderWaitPoll = 250 * time.Millisecond
)

var acmeHTTP01LocalMu sync.Mutex

func init() {
	gob.Register(acmeHTTP01ChallengeState{})
	gob.Register(acmeHTTP01PresentationState{})

	engine.RegisterResource(acmeHTTP01SolverKind, func() engine.Res { return &AcmeHTTP01SolverRes{} })
}

type acmeLocalValueAPI interface {
	ValueGet(context.Context, string) (interface{}, error)
	ValueSet(context.Context, string, interface{}) error
	ValueWatch(context.Context, string) (chan struct{}, error)
}

type acmeHTTP01Challenge struct {
	Attempt string
	Domain  string
	Token   string
	Path    string
	Body    string
}

func (obj acmeHTTP01Challenge) key() string {
	return strings.Join([]string{
		obj.Attempt,
		normalizeHTTPHost(obj.Domain),
		obj.Token,
	}, "\x00")
}

type acmeHTTP01ChallengeState struct {
	Challenges map[string]acmeHTTP01Challenge
}

type acmeHTTP01PresentationEntry struct {
	Attempt string
	Domain  string
	Path    string
	Ready   bool
	Error   string
}

type acmeHTTP01PresentationState struct {
	Entries map[string]acmeHTTP01PresentationEntry
}

func acmeHTTP01ChallengeStateKey(solver string) string {
	return "acme-http01-challenges-" + url.QueryEscape(strings.TrimSpace(solver))
}

func acmeHTTP01PresentationStateKey(solver string) string {
	return "acme-http01-presentation-" + url.QueryEscape(strings.TrimSpace(solver))
}

func loadAcmeHTTP01ChallengeState(ctx context.Context, localAPI acmeLocalValueAPI, solver string) (*acmeHTTP01ChallengeState, error) {
	value, err := localAPI.ValueGet(ctx, acmeHTTP01ChallengeStateKey(solver))
	if err != nil {
		return nil, err
	}
	switch x := value.(type) {
	case nil:
		return &acmeHTTP01ChallengeState{Challenges: map[string]acmeHTTP01Challenge{}}, nil
	case acmeHTTP01ChallengeState:
		if x.Challenges == nil {
			x.Challenges = map[string]acmeHTTP01Challenge{}
		}
		return &x, nil
	case *acmeHTTP01ChallengeState:
		if x == nil {
			return &acmeHTTP01ChallengeState{Challenges: map[string]acmeHTTP01Challenge{}}, nil
		}
		clone := *x
		if clone.Challenges == nil {
			clone.Challenges = map[string]acmeHTTP01Challenge{}
		}
		return &clone, nil
	default:
		return nil, fmt.Errorf("unexpected HTTP-01 challenge state type: %T", value)
	}
}

func storeAcmeHTTP01ChallengeState(ctx context.Context, localAPI acmeLocalValueAPI, solver string, state *acmeHTTP01ChallengeState) error {
	if state == nil || len(state.Challenges) == 0 {
		return localAPI.ValueSet(ctx, acmeHTTP01ChallengeStateKey(solver), nil)
	}
	return localAPI.ValueSet(ctx, acmeHTTP01ChallengeStateKey(solver), *state)
}

func loadAcmeHTTP01PresentationState(ctx context.Context, localAPI acmeLocalValueAPI, solver string) (*acmeHTTP01PresentationState, error) {
	value, err := localAPI.ValueGet(ctx, acmeHTTP01PresentationStateKey(solver))
	if err != nil {
		return nil, err
	}
	switch x := value.(type) {
	case nil:
		return &acmeHTTP01PresentationState{Entries: map[string]acmeHTTP01PresentationEntry{}}, nil
	case acmeHTTP01PresentationState:
		if x.Entries == nil {
			x.Entries = map[string]acmeHTTP01PresentationEntry{}
		}
		return &x, nil
	case *acmeHTTP01PresentationState:
		if x == nil {
			return &acmeHTTP01PresentationState{Entries: map[string]acmeHTTP01PresentationEntry{}}, nil
		}
		clone := *x
		if clone.Entries == nil {
			clone.Entries = map[string]acmeHTTP01PresentationEntry{}
		}
		return &clone, nil
	default:
		return nil, fmt.Errorf("unexpected HTTP-01 presentation state type: %T", value)
	}
}

func storeAcmeHTTP01PresentationState(ctx context.Context, localAPI acmeLocalValueAPI, solver string, state *acmeHTTP01PresentationState) error {
	if state == nil || len(state.Entries) == 0 {
		return localAPI.ValueSet(ctx, acmeHTTP01PresentationStateKey(solver), nil)
	}
	return localAPI.ValueSet(ctx, acmeHTTP01PresentationStateKey(solver), *state)
}

func mutateAcmeHTTP01ChallengeState(ctx context.Context, localAPI acmeLocalValueAPI, solver string, fn func(*acmeHTTP01ChallengeState) error) error {
	acmeHTTP01LocalMu.Lock()
	defer acmeHTTP01LocalMu.Unlock()

	state, err := loadAcmeHTTP01ChallengeState(ctx, localAPI, solver)
	if err != nil {
		return err
	}
	if err := fn(state); err != nil {
		return err
	}
	return storeAcmeHTTP01ChallengeState(ctx, localAPI, solver, state)
}

type acmeHTTP01Provider struct {
	localAPI    acmeLocalValueAPI
	solver      string
	attempt     string
	waitTimeout time.Duration
}

func newAcmeHTTP01Provider(localAPI acmeLocalValueAPI, solver string) (*acmeHTTP01Provider, error) {
	if localAPI == nil {
		return nil, fmt.Errorf("the Local API is required for the explicit http-01 solver")
	}
	solver = strings.TrimSpace(solver)
	if solver == "" {
		return nil, fmt.Errorf("the solver name must not be empty")
	}
	return &acmeHTTP01Provider{
		localAPI:    localAPI,
		solver:      solver,
		attempt:     fmt.Sprintf("%d", time.Now().UTC().UnixNano()),
		waitTimeout: acmeHTTP01ProviderTimeout,
	}, nil
}

func (obj *acmeHTTP01Provider) Present(domain, token, keyAuth string) error {
	challenge := acmeHTTP01Challenge{
		Attempt: obj.attempt,
		Domain:  normalizeHTTPHost(domain),
		Token:   token,
		Path:    http01.ChallengePath(token),
		Body:    keyAuth,
	}

	ctx := context.Background()
	if err := mutateAcmeHTTP01ChallengeState(ctx, obj.localAPI, obj.solver, func(state *acmeHTTP01ChallengeState) error {
		if state.Challenges == nil {
			state.Challenges = map[string]acmeHTTP01Challenge{}
		}
		state.Challenges[challenge.key()] = challenge
		return nil
	}); err != nil {
		return err
	}

	if err := obj.waitUntilReady(challenge); err != nil {
		_ = obj.CleanUp(domain, token, keyAuth)
		return err
	}

	return nil
}

func (obj *acmeHTTP01Provider) waitUntilReady(challenge acmeHTTP01Challenge) error {
	ctx, cancel := context.WithTimeout(context.Background(), obj.waitTimeout)
	defer cancel()

	ch, err := obj.localAPI.ValueWatch(ctx, acmeHTTP01PresentationStateKey(obj.solver))
	if err != nil {
		return err
	}

	for {
		state, err := loadAcmeHTTP01PresentationState(ctx, obj.localAPI, obj.solver)
		if err != nil {
			return err
		}
		if entry, exists := state.Entries[challenge.key()]; exists {
			if entry.Error != "" {
				return fmt.Errorf("%s", entry.Error)
			}
			if entry.Ready {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for %s[%s] to present %s", acmeHTTP01SolverKind, obj.solver, challenge.Path)
		case <-ch:
		case <-time.After(acmeHTTP01ProviderWaitPoll):
		}
	}
}

func (obj *acmeHTTP01Provider) CleanUp(domain, token, keyAuth string) error {
	challenge := acmeHTTP01Challenge{
		Attempt: obj.attempt,
		Domain:  normalizeHTTPHost(domain),
		Token:   token,
		Path:    http01.ChallengePath(token),
		Body:    keyAuth,
	}

	return mutateAcmeHTTP01ChallengeState(context.Background(), obj.localAPI, obj.solver, func(state *acmeHTTP01ChallengeState) error {
		delete(state.Challenges, challenge.key())
		if len(state.Challenges) == 0 {
			state.Challenges = nil
		}
		return nil
	})
}

// AcmeHTTP01SolverRes presents http-01 ACME challenge material through an
// explicit http:server grouping.
type AcmeHTTP01SolverRes struct {
	traits.Base
	traits.Edgeable
	traits.Groupable
	traits.Sendable
	traits.GraphQueryable

	init *engine.Init

	// Server is the name of the http server resource to group this into.
	Server string `lang:"server" yaml:"server"`

	// Hosts optionally restricts which challenge hostnames this solver will
	// present.
	Hosts []string `lang:"hosts" yaml:"hosts"`

	mutex        *sync.RWMutex
	challenges   map[string]acmeHTTP01Challenge
	presentation map[string]acmeHTTP01PresentationEntry
}

var _ HTTPServerGroupableRes = &AcmeHTTP01SolverRes{}

// Default returns some sensible defaults for this resource.
func (obj *AcmeHTTP01SolverRes) Default() engine.Res {
	return &AcmeHTTP01SolverRes{}
}

// ParentName is used to limit which resources autogroup into this one.
func (obj *AcmeHTTP01SolverRes) ParentName() string {
	return obj.Server
}

func (obj *AcmeHTTP01SolverRes) normalizedHosts() []string {
	result := make([]string, 0, len(obj.Hosts))
	seen := map[string]struct{}{}
	for _, host := range obj.Hosts {
		host = normalizeHTTPHost(host)
		if host == "" {
			continue
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		result = append(result, host)
	}
	return result
}

func (obj *AcmeHTTP01SolverRes) acceptsChallenge(challenge acmeHTTP01Challenge) bool {
	hosts := obj.normalizedHosts()
	if len(hosts) == 0 {
		return true
	}
	for _, host := range hosts {
		if normalizeHTTPHost(challenge.Domain) == host {
			return true
		}
	}
	return false
}

func (obj *AcmeHTTP01SolverRes) activeChallenge(host, requestPath string) (acmeHTTP01Challenge, bool) {
	obj.mutex.RLock()
	defer obj.mutex.RUnlock()

	host = normalizeHTTPHost(host)
	for _, challenge := range obj.challenges {
		if !obj.acceptsChallenge(challenge) {
			continue
		}
		if normalizeHTTPHost(challenge.Domain) != host {
			continue
		}
		if challenge.Path != requestPath {
			continue
		}
		return challenge, true
	}
	return acmeHTTP01Challenge{}, false
}

// AcceptHTTP determines whether we will respond to this request.
func (obj *AcmeHTTP01SolverRes) AcceptHTTP(req *http.Request) error {
	if req.Method != http.MethodGet {
		return fmt.Errorf("unhandled method")
	}
	if _, exists := obj.activeChallenge(req.Host, req.URL.Path); !exists {
		return fmt.Errorf("unhandled challenge")
	}
	return nil
}

// ServeHTTP is the standard HTTP handler that will be used here.
func (obj *AcmeHTTP01SolverRes) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	challenge, exists := obj.activeChallenge(req.Host, req.URL.Path)
	if !exists {
		http.NotFound(w, req)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(challenge.Body))
}

// Validate checks if the resource data structure was populated correctly.
func (obj *AcmeHTTP01SolverRes) Validate() error {
	for _, host := range obj.Hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			return fmt.Errorf("the Hosts field must not contain empty values")
		}
		if strings.Contains(host, "/") {
			return fmt.Errorf("the Hosts field must not contain paths")
		}
		if normalizeHTTPHost(host) == "" {
			return fmt.Errorf("the Hosts field contains an invalid hostname: %q", host)
		}
	}
	return nil
}

// Init runs some startup code for this resource.
func (obj *AcmeHTTP01SolverRes) Init(init *engine.Init) error {
	obj.init = init
	if obj.init.Local == nil {
		return fmt.Errorf("the Local API is required")
	}
	obj.mutex = &sync.RWMutex{}
	obj.challenges = map[string]acmeHTTP01Challenge{}
	obj.presentation = map[string]acmeHTTP01PresentationEntry{}
	return nil
}

// Cleanup is run by the engine to clean up after the resource is done.
func (obj *AcmeHTTP01SolverRes) Cleanup() error {
	return nil
}

func (obj *AcmeHTTP01SolverRes) syncLocalState(ctx context.Context) error {
	state, err := loadAcmeHTTP01ChallengeState(ctx, obj.init.Local, obj.Name())
	if err != nil {
		return err
	}

	challenges := map[string]acmeHTTP01Challenge{}
	for key, challenge := range state.Challenges {
		challenges[key] = challenge
	}

	presentation := map[string]acmeHTTP01PresentationEntry{}
	for key, challenge := range challenges {
		entry := acmeHTTP01PresentationEntry{
			Attempt: challenge.Attempt,
			Domain:  challenge.Domain,
			Path:    challenge.Path,
			Ready:   true,
		}
		if !obj.acceptsChallenge(challenge) {
			entry.Ready = false
			entry.Error = fmt.Sprintf("%s[%s] does not handle host %q", acmeHTTP01SolverKind, obj.Name(), challenge.Domain)
		}
		presentation[key] = entry
	}

	if err := storeAcmeHTTP01PresentationState(ctx, obj.init.Local, obj.Name(), &acmeHTTP01PresentationState{Entries: presentation}); err != nil {
		return errwrap.Wrapf(err, "could not store HTTP-01 presentation state")
	}

	obj.mutex.Lock()
	obj.challenges = challenges
	obj.presentation = presentation
	obj.mutex.Unlock()

	return nil
}

// Watch is the primary listener for this resource and it outputs events.
func (obj *AcmeHTTP01SolverRes) Watch(ctx context.Context) error {
	ch, err := obj.init.Local.ValueWatch(ctx, acmeHTTP01ChallengeStateKey(obj.Name()))
	if err != nil {
		return err
	}

	if err := obj.syncLocalState(ctx); err != nil {
		return err
	}
	if err := obj.init.Event(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-ch:
			if err := obj.syncLocalState(ctx); err != nil {
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

// AcmeHTTP01SolverSends is the struct of data which is sent after a successful
// Apply.
type AcmeHTTP01SolverSends struct {
	Pending        bool   `lang:"pending"`
	Ready          bool   `lang:"ready"`
	ChallengeCount int64  `lang:"challenge_count"`
	Error          string `lang:"error"`
}

// Sends represents the default struct of values we can send using Send/Recv.
func (obj *AcmeHTTP01SolverRes) Sends() interface{} {
	return &AcmeHTTP01SolverSends{}
}

// CheckApply syncs the send/recv view of the solver.
func (obj *AcmeHTTP01SolverRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	_ = ctx
	_ = apply

	obj.mutex.RLock()
	challengeCount := len(obj.challenges)
	ready := challengeCount > 0
	var firstError string
	for _, entry := range obj.presentation {
		if entry.Error != "" {
			if firstError == "" {
				firstError = entry.Error
			}
			ready = false
		}
	}
	obj.mutex.RUnlock()

	if err := obj.init.Send(&AcmeHTTP01SolverSends{
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
func (obj *AcmeHTTP01SolverRes) Cmp(r engine.Res) error {
	res, ok := r.(*AcmeHTTP01SolverRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}

	if obj.Server != res.Server {
		return fmt.Errorf("the Server field differs")
	}
	if !stringSliceEqual(obj.normalizedHosts(), res.normalizedHosts()) {
		return fmt.Errorf("the Hosts field differs")
	}

	return nil
}
