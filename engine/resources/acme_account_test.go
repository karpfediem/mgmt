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

func fakeAcmeAccountInit(t *testing.T) (*engine.Init, func() *AcmeAccountSends) {
	t.Helper()

	tmpdir := fmt.Sprintf("%s/", t.TempDir())
	sends := &AcmeAccountSends{}

	return &engine.Init{
			Event: func(ctx context.Context) error {
				return nil
			},
			Send: func(st interface{}) error {
				x, ok := st.(*AcmeAccountSends)
				if !ok {
					return fmt.Errorf("unexpected send payload: %T", st)
				}
				*sends = *x
				return nil
			},
			VarDir: func(p string) (string, error) {
				return path.Join(tmpdir, p), nil
			},
			Logf: func(string, ...interface{}) {},
		}, func() *AcmeAccountSends {
			copy := *sends
			return &copy
		}
}

func TestAcmeAccountCheckApplyRegistersAndSendsData(t *testing.T) {
	client := &fakeAcmeClient{
		accountKeyPEM: "ACCOUNT\n",
		registration:  &registration.Resource{URI: "https://example.test/acct/1"},
	}

	res := &AcmeAccountRes{
		Email:        "ops@example.com",
		AcceptTOS:    true,
		DirectoryURL: "https://example.test/directory",
	}
	init, sent := fakeAcmeAccountInit(t)
	if err := res.Init(init); err != nil {
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

	payload := sent()
	if !payload.Ready {
		t.Fatalf("expected account payload to be ready")
	}

	accountData, err := decodeAcmeAccountData(payload.Data)
	if err != nil {
		t.Fatalf("decodeAcmeAccountData failed: %v", err)
	}
	if accountData.DirectoryURL != "https://example.test/directory" {
		t.Fatalf("unexpected directory URL: %q", accountData.DirectoryURL)
	}
	if accountData.PrivateKeyPEM != "ACCOUNT\n" {
		t.Fatalf("unexpected private key payload")
	}
	if accountData.RegistrationURI != "https://example.test/acct/1" {
		t.Fatalf("unexpected registration URI: %q", accountData.RegistrationURI)
	}
}
