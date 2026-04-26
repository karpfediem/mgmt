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
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
)

func init() {
	engine.RegisterResource(KindAcmeAccount, func() engine.Res { return &AcmeAccountRes{} })
}

// AcmeAccountRes ensures that a local ACME account key exists and that the ACME
// account can be registered or recovered. It publishes non-secret account
// configuration into trusted World storage so that issuer resources can load the
// account by name without exchanging data over Send/Recv.
type AcmeAccountRes struct {
	traits.Base

	init *engine.Init

	// Directory is the ACME directory URL.
	Directory string `lang:"directory" yaml:"directory"`
	// Contact contains ACME contact URIs, usually mailto: addresses.
	Contact []string `lang:"contact" yaml:"contact"`

	// Key is the local ACME account private key path.
	Key string `lang:"key" yaml:"key"`
	// KeyAlgorithm is used when Key does not exist yet.
	KeyAlgorithm string `lang:"key_algorithm" yaml:"key_algorithm"`

	// CacheDir is reserved for local account metadata. The
	// canonical account metadata is published through World.
	CacheDir string `lang:"cache_dir" yaml:"cache_dir"`

	// TermsOfServiceAgreed explicitly permits account registration when the CA
	// requires ToS agreement.
	TermsOfServiceAgreed bool `lang:"terms_of_service_agreed" yaml:"terms_of_service_agreed"`

	// EABKid and EABHMACKeyFile enable RFC 8555 external account binding.
	EABKid         string `lang:"eab_kid" yaml:"eab_kid"`
	EABHMACKeyFile string `lang:"eab_hmac_key_file" yaml:"eab_hmac_key_file"`
}

// Default returns some sensible defaults for this resource.
func (obj *AcmeAccountRes) Default() engine.Res {
	return &AcmeAccountRes{
		KeyAlgorithm: acmeDefaultKeyAlgorithm,
	}
}

// Validate if the params passed in are valid data.
func (obj *AcmeAccountRes) Validate() error {
	if obj.Directory == "" {
		return fmt.Errorf("directory must not be empty")
	}
	parsed, err := url.Parse(obj.Directory)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("directory must be an absolute URL")
	}
	if obj.Key == "" {
		return fmt.Errorf("key must not be empty")
	}
	if !filepath.IsAbs(obj.Key) {
		return fmt.Errorf("key must be an absolute path")
	}
	if obj.CacheDir != "" && !filepath.IsAbs(obj.CacheDir) {
		return fmt.Errorf("cache_dir must be an absolute path")
	}
	if err := acmeValidateKeyAlgorithm(obj.keyAlgorithm()); err != nil {
		return err
	}
	if (obj.EABKid == "") != (obj.EABHMACKeyFile == "") {
		return fmt.Errorf("eab_kid and eab_hmac_key_file must either both be set or both be empty")
	}
	if obj.EABHMACKeyFile != "" && !filepath.IsAbs(obj.EABHMACKeyFile) {
		return fmt.Errorf("eab_hmac_key_file must be an absolute path")
	}
	for _, contact := range obj.Contact {
		if _, err := url.Parse(contact); err != nil {
			return fmt.Errorf("invalid contact URI %q: %w", contact, err)
		}
	}
	return nil
}

// Init initializes the resource.
func (obj *AcmeAccountRes) Init(init *engine.Init) error {
	obj.init = init
	return nil
}

// Cleanup is run by the engine to clean up after the resource is done.
func (obj *AcmeAccountRes) Cleanup() error { return nil }

// Watch is the primary listener for this resource and it outputs events.
func (obj *AcmeAccountRes) Watch(ctx context.Context) error {
	return acmeWatchMany(ctx, obj.init, []string{acmeAccountNamespace(obj.Name())}, 60*time.Second)
}

func (obj *AcmeAccountRes) keyAlgorithm() string {
	if obj.KeyAlgorithm == "" {
		return acmeDefaultKeyAlgorithm
	}
	return obj.KeyAlgorithm
}

func (obj *AcmeAccountRes) cacheDir() string {
	if obj.CacheDir != "" {
		return obj.CacheDir
	}
	return filepath.Dir(obj.Key)
}

func (obj *AcmeAccountRes) desiredInfo(accountURI string) *acmeAccountInfo {
	return &acmeAccountInfo{
		Version:              acmeVersion,
		Directory:            obj.Directory,
		Contact:              append([]string{}, obj.Contact...),
		Key:                  obj.Key,
		KeyAlgorithm:         obj.keyAlgorithm(),
		CacheDir:             obj.cacheDir(),
		TermsOfServiceAgreed: obj.TermsOfServiceAgreed,
		AccountURI:           accountURI,
		EABKid:               obj.EABKid,
		EABHMACKeyFile:       obj.EABHMACKeyFile,
	}
}

func acmeAccountInfoConfigEqual(a, b *acmeAccountInfo) bool {
	if a == nil || b == nil {
		return a == b
	}
	copyA := *a
	copyB := *b
	copyA.AccountURI = ""
	copyB.AccountURI = ""
	return reflect.DeepEqual(copyA, copyB)
}

// CheckApply method for resource.
func (obj *AcmeAccountRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	infoKey := acmeAccountNamespace(obj.Name())

	var existing acmeAccountInfo
	exists, err := acmeWorldReadJSON(ctx, obj.init.World, infoKey, &existing)
	if err != nil {
		return false, err
	}

	_, _, keyExists, err := acmeLoadOrCreatePrivateKey(obj.Key, obj.keyAlgorithm(), false, 0600)
	if err != nil {
		return false, err
	}

	desired := obj.desiredInfo(existing.AccountURI)
	if keyExists && exists && existing.AccountURI != "" && acmeAccountInfoConfigEqual(&existing, desired) {
		return true, nil
	}

	if !apply {
		return false, nil
	}

	if _, _, _, err := acmeLoadOrCreatePrivateKey(obj.Key, obj.keyAlgorithm(), true, 0600); err != nil {
		return false, err
	}
	if err := os.MkdirAll(obj.cacheDir(), 0700); err != nil {
		return false, err
	}

	info := obj.desiredInfo(existing.AccountURI)
	_, acct, err := acmeEnsureAccount(ctx, info)
	if err != nil {
		return false, err
	}
	if acct != nil && acct.URI != "" {
		info.AccountURI = acct.URI
	}
	if _, err := acmeWorldWriteJSON(ctx, obj.init.World, infoKey, info); err != nil {
		return false, err
	}
	obj.init.Logf("ACME account info published at %s", infoKey)
	return false, nil
}

// Cmp compares two resources and returns an error if they are not equivalent.
func (obj *AcmeAccountRes) Cmp(r engine.Res) error {
	res, ok := r.(*AcmeAccountRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.Directory != res.Directory {
		return fmt.Errorf("the Directory differs")
	}
	if !reflect.DeepEqual(obj.Contact, res.Contact) {
		return fmt.Errorf("the Contact differs")
	}
	if obj.Key != res.Key {
		return fmt.Errorf("the Key differs")
	}
	if obj.keyAlgorithm() != res.keyAlgorithm() {
		return fmt.Errorf("the KeyAlgorithm differs")
	}
	if obj.cacheDir() != res.cacheDir() {
		return fmt.Errorf("the CacheDir differs")
	}
	if obj.TermsOfServiceAgreed != res.TermsOfServiceAgreed {
		return fmt.Errorf("the TermsOfServiceAgreed differs")
	}
	if obj.EABKid != res.EABKid {
		return fmt.Errorf("the EABKid differs")
	}
	if obj.EABHMACKeyFile != res.EABHMACKeyFile {
		return fmt.Errorf("the EABHMACKeyFile differs")
	}
	return nil
}

// AcmeAccountUID is the UID struct for AcmeAccountRes.
type AcmeAccountUID struct {
	engine.BaseUID
	name string
}

// UIDs includes all params to make a unique identification of this object.
func (obj *AcmeAccountRes) UIDs() []engine.ResUID {
	x := &AcmeAccountUID{
		BaseUID: engine.BaseUID{Name: obj.Name(), Kind: obj.Kind()},
		name:    obj.Name(),
	}
	return []engine.ResUID{x}
}

// UnmarshalYAML is the custom unmarshal handler for this struct.
func (obj *AcmeAccountRes) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawRes AcmeAccountRes
	def := obj.Default()
	res, ok := def.(*AcmeAccountRes)
	if !ok {
		return fmt.Errorf("could not convert to AcmeAccountRes")
	}
	raw := rawRes(*res)
	if err := unmarshal(&raw); err != nil {
		return err
	}
	*obj = AcmeAccountRes(raw)
	return nil
}
