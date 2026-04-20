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
	"fmt"
	"path"
	"testing"

	"github.com/purpleidea/mgmt/engine"

	"github.com/go-acme/lego/v4/registration"
)

func fakeAcmeAccountInit(t *testing.T, world *fakeWorld) *engine.Init {
	t.Helper()

	tmpdir := fmt.Sprintf("%s/", t.TempDir())
	if world == nil {
		world = newFakeWorld("account-host")
	}

	return &engine.Init{
		Hostname: "account-host",
		Event:    func(context.Context) error { return nil },
		VarDir: func(p string) (string, error) {
			return path.Join(tmpdir, p), nil
		},
		World: world,
		Logf:  func(string, ...interface{}) {},
	}
}

func TestAcmeAccountCheckApplyRegistersAndPublishesSharedState(t *testing.T) {
	world := newFakeWorld("account-host")
	client := &fakeAcmeClient{
		accountKeyPEM: "ACCOUNT\n",
		registration:  &registration.Resource{URI: "https://example.test/acct/1"},
	}

	res := &AcmeAccountRes{
		Email:        "ops@example.com",
		AcceptTOS:    true,
		DirectoryURL: "https://example.test/directory",
	}
	res.SetName("public-ca")
	if err := res.Init(fakeAcmeAccountInit(t, world)); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	res.clientFactory = func(state *acmeAccountStoredState) (acmeClient, error) {
		return client, nil
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected checkOK to be false after creating account state")
	}
	if client.ensureCalls != 1 {
		t.Fatalf("expected one EnsureRegistration call, got %d", client.ensureCalls)
	}

	sharedState, err := loadAcmeAccountSharedState(context.Background(), world, res.Name())
	if err != nil {
		t.Fatalf("loadAcmeAccountSharedState failed: %v", err)
	}
	if sharedState == nil || !sharedState.ready() {
		t.Fatalf("expected shared account state to be ready")
	}
	if sharedState.DirectoryURL != "https://example.test/directory" {
		t.Fatalf("unexpected directory URL: %q", sharedState.DirectoryURL)
	}
	if sharedState.PrivateKeyPEM != "ACCOUNT\n" {
		t.Fatalf("unexpected private key payload")
	}
	if sharedState.RegistrationURI != "https://example.test/acct/1" {
		t.Fatalf("unexpected registration URI: %q", sharedState.RegistrationURI)
	}
}
