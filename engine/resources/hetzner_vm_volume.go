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

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func init() {
	engine.RegisterResource("hetzner:vm_volume", func() engine.Res { return &HetznerVMVolumeRes{} })
}

// HetznerVMVolumeRes manages a volume attachment on an existing Hetzner VM.
type HetznerVMVolumeRes struct {
	traits.Base

	init *engine.Init

	APIToken string `lang:"apitoken"`
	State    string `lang:"state"`
	Server   string `lang:"server"`
	Volume   string `lang:"volume"`

	WaitInterval uint32 `lang:"waitinterval"`
	WaitTimeout  uint32 `lang:"waittimeout"`

	client       *hcloud.Client
	actionWaiter hetznerActionWaiter
	serverClient hetznerServerLookupClient
	volumeClient hetznerVolumeClient

	server *hcloud.Server
	volume *hcloud.Volume
}

// Default returns some conservative defaults for this resource.
func (obj *HetznerVMVolumeRes) Default() engine.Res {
	return &HetznerVMVolumeRes{
		State:        HetznerStateExists,
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
}

// Validate checks whether the requested attachment spec is valid.
func (obj *HetznerVMVolumeRes) Validate() error {
	if obj.APIToken == "" {
		return fmt.Errorf("empty token string")
	}
	if err := validateHetznerState(obj.State); err != nil {
		return err
	}
	if obj.Server == "" {
		return fmt.Errorf("empty server")
	}
	if obj.Volume == "" {
		return fmt.Errorf("empty volume")
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
func (obj *HetznerVMVolumeRes) Init(init *engine.Init) error {
	obj.init = init
	obj.client = newHetznerClient(init, obj.APIToken, obj.WaitInterval)
	obj.actionWaiter = &obj.client.Action
	obj.serverClient = &obj.client.Server
	obj.volumeClient = &obj.client.Volume
	return nil
}

// Cleanup removes authentication and client state from memory.
func (obj *HetznerVMVolumeRes) Cleanup() error {
	obj.APIToken = ""
	obj.client = nil
	obj.actionWaiter = nil
	obj.serverClient = nil
	obj.volumeClient = nil
	obj.server = nil
	obj.volume = nil
	return nil
}

// Watch is not implemented for this resource, since the Hetzner API does not provide events.
func (obj *HetznerVMVolumeRes) Watch(context.Context) error {
	return fmt.Errorf("invalid Watch call: requires poll metaparam")
}

// CheckApply checks and applies VM volume attachment state.
func (obj *HetznerVMVolumeRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	if err := obj.getTargetUpdate(ctx); err != nil {
		return false, errwrap.Wrapf(err, "getTargetUpdate failed")
	}

	if obj.server == nil {
		if obj.State == HetznerStateAbsent {
			return true, nil
		}
		return false, fmt.Errorf("server not found: %s", obj.Server)
	}
	if obj.volume == nil {
		if obj.State == HetznerStateAbsent {
			return true, nil
		}
		return false, fmt.Errorf("volume not found: %s", obj.Volume)
	}

	if obj.State == HetznerStateAbsent {
		if obj.volume.Server == nil {
			return true, nil
		}
		if !apply {
			return false, nil
		}
		if err := obj.detach(ctx); err != nil {
			return false, errwrap.Wrapf(err, "detach failed")
		}
		return false, nil
	}

	if obj.volume.Server != nil && obj.volume.Server.ID == obj.server.ID {
		return true, nil
	}
	if !apply {
		return false, nil
	}

	if obj.volume.Server != nil && obj.volume.Server.ID != obj.server.ID {
		if err := obj.detach(ctx); err != nil {
			return false, errwrap.Wrapf(err, "detach from previous server failed")
		}
		if err := obj.getTargetUpdate(ctx); err != nil {
			return false, errwrap.Wrapf(err, "getTargetUpdate after detach failed")
		}
	}

	if err := obj.attach(ctx); err != nil {
		return false, errwrap.Wrapf(err, "attach failed")
	}
	return false, nil
}

func (obj *HetznerVMVolumeRes) getTargetUpdate(ctx context.Context) error {
	server, err := hetznerServerByName(ctx, obj.serverClient, obj.Server)
	if err != nil {
		return errwrap.Wrapf(err, "server lookup failed")
	}
	volume, err := hetznerVolumeByName(ctx, obj.volumeClient, obj.Volume)
	if err != nil {
		return errwrap.Wrapf(err, "volume lookup failed")
	}
	obj.server = server
	obj.volume = volume
	return nil
}

func (obj *HetznerVMVolumeRes) attach(ctx context.Context) error {
	action, _, err := obj.volumeClient.Attach(ctx, obj.volume, obj.server)
	if err != nil {
		return err
	}
	return errwrap.Wrapf(hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action), "wait for attach action failed")
}

func (obj *HetznerVMVolumeRes) detach(ctx context.Context) error {
	action, _, err := obj.volumeClient.Detach(ctx, obj.volume)
	if err != nil {
		return err
	}
	return errwrap.Wrapf(hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action), "wait for detach action failed")
}

// Cmp compares two resources.
func (obj *HetznerVMVolumeRes) Cmp(r engine.Res) error {
	res, ok := r.(*HetznerVMVolumeRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.APIToken != res.APIToken {
		return fmt.Errorf("apitoken differs")
	}
	if obj.State != res.State {
		return fmt.Errorf("state differs")
	}
	if obj.Server != res.Server {
		return fmt.Errorf("server differs")
	}
	if obj.Volume != res.Volume {
		return fmt.Errorf("volume differs")
	}
	if obj.WaitInterval != res.WaitInterval {
		return fmt.Errorf("waitinterval differs")
	}
	if obj.WaitTimeout != res.WaitTimeout {
		return fmt.Errorf("waittimeout differs")
	}
	return nil
}
