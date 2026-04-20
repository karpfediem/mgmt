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
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/go-acme/lego/v4/acme/api"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

const acmeAccountStateFilename = "state.json"

func init() {
	engine.RegisterResource("acme:account", func() engine.Res { return &AcmeAccountRes{} })
}

type acmeAccountData struct {
	DirectoryURL    string `json:"directory_url"`
	PrivateKeyPEM   string `json:"private_key_pem"`
	RegistrationURI string `json:"registration_uri"`
}

func (obj *acmeAccountData) normalize() {
	if obj == nil {
		return
	}
	obj.DirectoryURL = strings.TrimSpace(obj.DirectoryURL)
	obj.PrivateKeyPEM = normalizePEMString(obj.PrivateKeyPEM)
	obj.RegistrationURI = strings.TrimSpace(obj.RegistrationURI)
}

func (obj *acmeAccountData) validate() error {
	if obj == nil {
		return fmt.Errorf("account data is nil")
	}
	if obj.DirectoryURL == "" {
		return fmt.Errorf("account data directory URL must not be empty")
	}
	if obj.PrivateKeyPEM == "" {
		return fmt.Errorf("account data private key must not be empty")
	}
	if obj.RegistrationURI == "" {
		return fmt.Errorf("account data registration URI must not be empty")
	}
	return nil
}

func encodeAcmeAccountData(data *acmeAccountData) (string, error) {
	if data == nil {
		return "", nil
	}
	clone := *data
	clone.normalize()
	if err := clone.validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(&clone)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func decodeAcmeAccountData(data string) (*acmeAccountData, error) {
	data = strings.TrimSpace(data)
	if data == "" {
		return nil, nil
	}
	account := &acmeAccountData{}
	if err := json.Unmarshal([]byte(data), account); err != nil {
		return nil, err
	}
	account.normalize()
	if err := account.validate(); err != nil {
		return nil, err
	}
	return account, nil
}

type acmeAccountStoredState struct {
	Version         int    `json:"version"`
	Email           string `json:"email"`
	DirectoryURL    string `json:"directory_url"`
	PrivateKeyPEM   string `json:"private_key_pem"`
	RegistrationURI string `json:"registration_uri"`
}

func (obj *acmeAccountStoredState) clone() *acmeAccountStoredState {
	if obj == nil {
		return &acmeAccountStoredState{}
	}
	clone := *obj
	return &clone
}

func (obj *acmeAccountStoredState) normalize() {
	if obj == nil {
		return
	}
	obj.Email = strings.TrimSpace(obj.Email)
	obj.DirectoryURL = strings.TrimSpace(obj.DirectoryURL)
	obj.PrivateKeyPEM = normalizePEMString(obj.PrivateKeyPEM)
	obj.RegistrationURI = strings.TrimSpace(obj.RegistrationURI)
}

func (obj *acmeAccountStoredState) ready() bool {
	return obj != nil &&
		strings.TrimSpace(obj.DirectoryURL) != "" &&
		normalizePEMString(obj.PrivateKeyPEM) != "" &&
		strings.TrimSpace(obj.RegistrationURI) != ""
}

func (obj *acmeAccountStoredState) data() (*acmeAccountData, error) {
	if obj == nil || !obj.ready() {
		return nil, nil
	}
	account := &acmeAccountData{
		DirectoryURL:    obj.DirectoryURL,
		PrivateKeyPEM:   obj.PrivateKeyPEM,
		RegistrationURI: obj.RegistrationURI,
	}
	account.normalize()
	return account, account.validate()
}

type acmeAccountPlan struct {
	needsApply        bool
	emailChanged      bool
	publishSharedOnly bool
	reason            string
}

// AcmeAccountRes manages the ACME account identity and registration state.
type AcmeAccountRes struct {
	traits.Base

	init *engine.Init

	// Email is the ACME account contact email.
	Email string `lang:"email" yaml:"email"`

	// AcceptTOS must be true so that the ACME account can be registered.
	AcceptTOS bool `lang:"accept_tos" yaml:"accept_tos"`

	// DirectoryURL is the ACME directory endpoint.
	DirectoryURL string `lang:"directory_url" yaml:"directory_url"`

	varDir    string
	statePath string

	clientFactory func(*acmeAccountStoredState) (acmeClient, error)
}

// Default returns some sensible defaults for this resource.
func (obj *AcmeAccountRes) Default() engine.Res {
	return &AcmeAccountRes{
		DirectoryURL: lego.LEDirectoryProduction,
	}
}

// Validate if the params passed in are valid data.
func (obj *AcmeAccountRes) Validate() error {
	if !obj.AcceptTOS {
		return fmt.Errorf("the AcceptTOS field must be true")
	}
	if strings.TrimSpace(obj.DirectoryURL) == "" {
		return fmt.Errorf("the DirectoryURL field must not be empty")
	}
	return nil
}

// Init runs some startup code for this resource.
func (obj *AcmeAccountRes) Init(init *engine.Init) error {
	obj.init = init

	dir, err := obj.init.VarDir("")
	if err != nil {
		return errwrap.Wrapf(err, "could not get VarDir in Init()")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errwrap.Wrapf(err, "could not create VarDir")
	}

	obj.varDir = dir
	obj.statePath = path.Join(dir, acmeAccountStateFilename)

	if obj.clientFactory == nil {
		obj.clientFactory = obj.newLegoAccountClient
	}

	return nil
}

// Cleanup is run by the engine to clean up after the resource is done.
func (obj *AcmeAccountRes) Cleanup() error {
	return nil
}

// Watch is the primary listener for this resource and it outputs events.
func (obj *AcmeAccountRes) Watch(ctx context.Context) error {
	ch, err := obj.init.World.StrWatch(ctx, acmeAccountStateKey(obj.Name()))
	if err != nil {
		return err
	}

	if err := obj.init.Event(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-ch:
			if !ok {
				return nil
			}
			if err != nil {
				return err
			}
		}

		if err := obj.init.Event(ctx); err != nil {
			return err
		}
	}
}

// CheckApply ensures the local ACME account state is present.
func (obj *AcmeAccountRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	state, err := obj.currentState(ctx)
	if err != nil {
		return false, errwrap.Wrapf(err, "could not load ACME account state")
	}
	if state == nil {
		state = &acmeAccountStoredState{}
	}

	sharedState, err := loadAcmeAccountSharedState(ctx, obj.init.World, obj.Name())
	if err != nil {
		return false, errwrap.Wrapf(err, "could not load shared ACME account state")
	}

	plan := obj.planState(state, sharedState)
	if plan.needsApply && !apply {
		return false, nil
	}

	if plan.needsApply {
		next := state.clone()
		if !plan.publishSharedOnly {
			client, err := obj.clientFactory(state.clone())
			if err != nil {
				return false, errwrap.Wrapf(err, "could not create ACME account client")
			}

			reg, err := client.EnsureRegistration(plan.emailChanged, obj.AcceptTOS)
			if err != nil {
				return false, errwrap.Wrapf(err, "could not reconcile ACME account registration")
			}

			next.Version = 1
			next.Email = obj.Email
			next.DirectoryURL = obj.DirectoryURL
			next.PrivateKeyPEM = normalizePEMString(client.AccountKeyPEM())
			next.RegistrationURI = reg.URI
			next.normalize()
		}

		if err := obj.saveState(next); err != nil {
			return false, errwrap.Wrapf(err, "could not save ACME account state")
		}
		if err := storeAcmeAccountSharedState(ctx, obj.init.World, obj.Name(), next); err != nil {
			return false, errwrap.Wrapf(err, "could not publish shared ACME account state")
		}
		state = next
	}

	if plan.needsApply {
		return false, nil
	}
	return true, nil
}

// Cmp compares two resources and returns an error if they are not equivalent.
func (obj *AcmeAccountRes) Cmp(r engine.Res) error {
	res, ok := r.(*AcmeAccountRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}

	if obj.Email != res.Email {
		return fmt.Errorf("the Email field differs")
	}
	if obj.AcceptTOS != res.AcceptTOS {
		return fmt.Errorf("the AcceptTOS field differs")
	}
	if obj.DirectoryURL != res.DirectoryURL {
		return fmt.Errorf("the DirectoryURL field differs")
	}

	return nil
}

func (obj *AcmeAccountRes) planState(state, sharedState *acmeAccountStoredState) *acmeAccountPlan {
	plan := &acmeAccountPlan{
		emailChanged: state.Email != obj.Email,
	}

	if !state.ready() {
		plan.needsApply = true
		plan.reason = "account registration is missing"
		return plan
	}
	if state.DirectoryURL != obj.DirectoryURL {
		plan.needsApply = true
		plan.reason = "directory URL changed"
		return plan
	}
	if plan.emailChanged {
		plan.needsApply = true
		plan.reason = "email changed"
		return plan
	}
	if sharedState == nil || !sharedState.ready() {
		plan.needsApply = true
		plan.publishSharedOnly = true
		plan.reason = "shared account state is missing"
		return plan
	}
	if !acmeAccountStoredStateEqual(state, sharedState) {
		plan.needsApply = true
		plan.publishSharedOnly = true
		plan.reason = "shared account state is out of sync"
		return plan
	}

	return plan
}

func (obj *AcmeAccountRes) loadState() (*acmeAccountStoredState, error) {
	data, err := os.ReadFile(obj.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	state := &acmeAccountStoredState{}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, errwrap.Wrapf(err, "could not parse ACME account state")
	}
	state.normalize()
	return state, nil
}

func (obj *AcmeAccountRes) saveState(state *acmeAccountStoredState) error {
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

func (obj *AcmeAccountRes) currentState(ctx context.Context) (*acmeAccountStoredState, error) {
	sharedState, err := loadAcmeAccountSharedState(ctx, obj.init.World, obj.Name())
	if err != nil {
		return nil, err
	}
	if sharedState != nil && sharedState.ready() {
		return sharedState, nil
	}
	return obj.loadState()
}

func acmeAccountStoredStateEqual(a, b *acmeAccountStoredState) bool {
	if a == nil || b == nil {
		return a == b
	}

	left := a.clone()
	right := b.clone()
	left.normalize()
	right.normalize()

	return left.Email == right.Email &&
		left.DirectoryURL == right.DirectoryURL &&
		left.PrivateKeyPEM == right.PrivateKeyPEM &&
		left.RegistrationURI == right.RegistrationURI
}

func (obj *AcmeAccountRes) newLegoAccountClient(state *acmeAccountStoredState) (acmeClient, error) {
	keyPEM := ""
	registrationURI := ""
	if state != nil {
		keyPEM = state.PrivateKeyPEM
		if state.DirectoryURL == "" || state.DirectoryURL == obj.DirectoryURL {
			registrationURI = state.RegistrationURI
		}
	}

	return newLegoBaseClient(obj.DirectoryURL, obj.Email, keyPEM, registrationURI)
}

func newLegoBaseClient(directoryURL, email, keyPEM, registrationURI string) (*legoAcmeClient, error) {
	keyPEM = normalizePEMString(keyPEM)
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
	if strings.TrimSpace(registrationURI) != "" {
		reg = &registration.Resource{URI: strings.TrimSpace(registrationURI)}
	}

	user := &legoAcmeUser{
		email:        email,
		registration: reg,
		privateKey:   privateKey,
	}

	config := lego.NewConfig(user)
	config.CADirURL = directoryURL

	kid := ""
	if reg != nil {
		kid = reg.URI
	}

	core, err := api.New(config.HTTPClient, config.UserAgent, config.CADirURL, kid, privateKey)
	if err != nil {
		return nil, err
	}

	return &legoAcmeClient{
		core:          core,
		registration:  registration.NewRegistrar(core, user),
		accountKeyPEM: keyPEM,
		user:          user,
	}, nil
}
