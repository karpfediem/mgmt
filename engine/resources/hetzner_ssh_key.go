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
	"encoding/base64"
	"fmt"
	"maps"
	"strings"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/crypto/ssh"
)

func init() {
	engine.RegisterResource("hetzner:ssh_key", func() engine.Res { return &HetznerSSHKeyRes{} })
}

// HetznerSSHKeyRes manages project SSH keys in Hetzner Cloud.
type HetznerSSHKeyRes struct {
	traits.Base

	init *engine.Init

	APIToken  string            `lang:"apitoken"`
	State     string            `lang:"state"`
	PublicKey string            `lang:"publickey"`
	Labels    map[string]string `lang:"labels"`

	WaitInterval uint32 `lang:"waitinterval"`

	client       *hcloud.Client
	sshKeyClient hetznerSSHKeyLifecycleClient

	sshKey *hcloud.SSHKey
}

// Default returns conservative defaults for this resource.
func (obj *HetznerSSHKeyRes) Default() engine.Res {
	return &HetznerSSHKeyRes{
		State:        HetznerStateExists,
		WaitInterval: HetznerWaitIntervalDefault,
	}
}

// Validate checks whether the requested SSH key spec is valid.
func (obj *HetznerSSHKeyRes) Validate() error {
	if obj.APIToken == "" {
		return fmt.Errorf("empty token string")
	}
	if err := validateHetznerState(obj.State); err != nil {
		return err
	}
	if obj.State == HetznerStateExists {
		if _, _, err := normalizeHetznerPublicKey(obj.PublicKey); err != nil {
			return errwrap.Wrapf(err, "invalid publickey")
		}
	}
	if obj.MetaParams().Poll < HetznerPollLimit {
		return fmt.Errorf("invalid polling interval (minimum %d s)", HetznerPollLimit)
	}
	if obj.WaitInterval < HetznerWaitIntervalLimit {
		return fmt.Errorf("invalid wait interval (minimum %d)", HetznerWaitIntervalLimit)
	}
	return nil
}

// Init runs startup code for this resource.
func (obj *HetznerSSHKeyRes) Init(init *engine.Init) error {
	obj.init = init
	obj.client = newHetznerClient(init, obj.APIToken, obj.WaitInterval)
	obj.sshKeyClient = &obj.client.SSHKey
	return nil
}

// Cleanup removes authentication and client state from memory.
func (obj *HetznerSSHKeyRes) Cleanup() error {
	obj.APIToken = ""
	obj.client = nil
	obj.sshKeyClient = nil
	obj.sshKey = nil
	return nil
}

// Watch is not implemented for this resource, since the Hetzner API does not provide events.
func (obj *HetznerSSHKeyRes) Watch(context.Context) error {
	return fmt.Errorf("invalid Watch call: requires poll metaparam")
}

// CheckApply checks and applies SSH key lifecycle state.
func (obj *HetznerSSHKeyRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	if err := obj.getSSHKeyUpdate(ctx); err != nil {
		return false, errwrap.Wrapf(err, "getSSHKeyUpdate failed")
	}

	if obj.sshKey == nil {
		if obj.State == HetznerStateAbsent {
			return true, nil
		}
		if !apply {
			return false, nil
		}
		if err := obj.create(ctx); err != nil {
			return false, errwrap.Wrapf(err, "create failed")
		}
		return false, nil
	}

	if obj.State == HetznerStateAbsent {
		if !apply {
			return false, nil
		}
		if err := obj.delete(ctx); err != nil {
			return false, errwrap.Wrapf(err, "delete failed")
		}
		return false, nil
	}

	_, desiredFingerprint, err := normalizeHetznerPublicKey(obj.PublicKey)
	if err != nil {
		return false, errwrap.Wrapf(err, "normalize desired publickey failed")
	}
	liveFingerprint := obj.sshKey.Fingerprint
	if liveFingerprint == "" {
		_, liveFingerprint, err = normalizeHetznerPublicKey(obj.sshKey.PublicKey)
		if err != nil {
			return false, errwrap.Wrapf(err, "normalize live publickey failed")
		}
	}
	if liveFingerprint != desiredFingerprint {
		return false, fmt.Errorf("publickey differs and is not mutable")
	}

	if maps.Equal(obj.sshKey.Labels, obj.Labels) {
		return true, nil
	}
	if !apply {
		return false, nil
	}
	if err := obj.update(ctx); err != nil {
		return false, errwrap.Wrapf(err, "update failed")
	}
	return false, nil
}

func (obj *HetznerSSHKeyRes) create(ctx context.Context) error {
	publicKey, _, err := normalizeHetznerPublicKey(obj.PublicKey)
	if err != nil {
		return errwrap.Wrapf(err, "normalize publickey failed")
	}
	sshKey, _, err := obj.sshKeyClient.Create(ctx, hcloud.SSHKeyCreateOpts{
		Name:      obj.Name(),
		PublicKey: publicKey,
		Labels:    obj.Labels,
	})
	if err != nil {
		return err
	}
	obj.sshKey = sshKey
	return nil
}

func (obj *HetznerSSHKeyRes) update(ctx context.Context) error {
	sshKey, _, err := obj.sshKeyClient.Update(ctx, obj.sshKey, hcloud.SSHKeyUpdateOpts{
		Name:   obj.Name(),
		Labels: obj.Labels,
	})
	if err != nil {
		return err
	}
	obj.sshKey = sshKey
	return nil
}

func (obj *HetznerSSHKeyRes) delete(ctx context.Context) error {
	_, err := obj.sshKeyClient.Delete(ctx, obj.sshKey)
	return err
}

func (obj *HetznerSSHKeyRes) getSSHKeyUpdate(ctx context.Context) error {
	sshKey, err := hetznerSSHKeyByName(ctx, obj.sshKeyClient, obj.Name())
	if err != nil {
		return errwrap.Wrapf(err, "ssh key lookup failed")
	}
	obj.sshKey = sshKey
	return nil
}

// Cmp compares two resource structs. Returns nil if the comparison holds true.
func (obj *HetznerSSHKeyRes) Cmp(r engine.Res) error {
	if obj == nil && r == nil {
		return nil
	}
	if (obj == nil) != (r == nil) {
		return fmt.Errorf("one resource is empty")
	}
	res, ok := r.(*HetznerSSHKeyRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.APIToken != res.APIToken {
		return fmt.Errorf("apitoken differs")
	}
	if obj.State != res.State {
		return fmt.Errorf("state differs")
	}
	if obj.State != HetznerStateAbsent {
		_, leftFingerprint, err := normalizeHetznerPublicKey(obj.PublicKey)
		if err != nil {
			return errwrap.Wrapf(err, "normalize left publickey failed")
		}
		_, rightFingerprint, err := normalizeHetznerPublicKey(res.PublicKey)
		if err != nil {
			return errwrap.Wrapf(err, "normalize right publickey failed")
		}
		if leftFingerprint != rightFingerprint {
			return fmt.Errorf("publickey differs")
		}
	}
	if !maps.Equal(obj.Labels, res.Labels) {
		return fmt.Errorf("labels differ")
	}
	if obj.WaitInterval != res.WaitInterval {
		return fmt.Errorf("waitinterval differs")
	}
	return nil
}

func normalizeHetznerPublicKey(value string) (string, string, error) {
	line := strings.TrimSpace(value)
	if line == "" {
		return "", "", fmt.Errorf("empty public key")
	}
	pubKey, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return "", "", err
	}
	if len(options) > 0 {
		return "", "", fmt.Errorf("public key options are not supported")
	}
	if len(rest) > 0 {
		return "", "", fmt.Errorf("leftover data in public key")
	}
	normalized := fmt.Sprintf("%s %s", pubKey.Type(), base64.StdEncoding.EncodeToString(pubKey.Marshal()))
	if comment != "" {
		normalized += " " + comment
	}
	return normalized, ssh.FingerprintLegacyMD5(pubKey), nil
}
