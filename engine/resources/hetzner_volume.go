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
	"maps"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func init() {
	engine.RegisterResource("hetzner:volume", func() engine.Res { return &HetznerVolumeRes{} })
}

// HetznerVolumeRes manages a Hetzner volume lifecycle.
type HetznerVolumeRes struct {
	traits.Base

	init *engine.Init

	APIToken         string            `lang:"apitoken"`
	State            string            `lang:"state"`
	Location         string            `lang:"location"`
	Size             int               `lang:"size"`
	Format           string            `lang:"format"`
	Labels           map[string]string `lang:"labels"`
	DeleteProtection bool              `lang:"deleteprotection"`

	WaitInterval uint32 `lang:"waitinterval"`
	WaitTimeout  uint32 `lang:"waittimeout"`

	client         *hcloud.Client
	actionWaiter   hetznerActionWaiter
	volumeClient   hetznerVolumeLifecycleClient
	locationClient hetznerLocationLookupClient

	volume *hcloud.Volume
}

// Default returns conservative defaults for this resource.
func (obj *HetznerVolumeRes) Default() engine.Res {
	return &HetznerVolumeRes{
		State:        HetznerStateExists,
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
}

// Validate checks whether the requested volume spec is valid.
func (obj *HetznerVolumeRes) Validate() error {
	if obj.APIToken == "" {
		return fmt.Errorf("empty token string")
	}
	if err := validateHetznerState(obj.State); err != nil {
		return err
	}
	if obj.State == HetznerStateExists {
		if obj.Location == "" {
			return fmt.Errorf("empty location")
		}
		if obj.Size <= 0 {
			return fmt.Errorf("invalid size: %d", obj.Size)
		}
	}
	switch obj.Format {
	case "", hcloud.VolumeFormatExt4, hcloud.VolumeFormatXFS:
	default:
		return fmt.Errorf("invalid format: %s", obj.Format)
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
func (obj *HetznerVolumeRes) Init(init *engine.Init) error {
	obj.init = init
	obj.client = newHetznerClient(init, obj.APIToken, obj.WaitInterval)
	obj.actionWaiter = &obj.client.Action
	obj.volumeClient = &obj.client.Volume
	obj.locationClient = &obj.client.Location
	return nil
}

// Cleanup removes authentication and client state from memory.
func (obj *HetznerVolumeRes) Cleanup() error {
	obj.APIToken = ""
	obj.client = nil
	obj.actionWaiter = nil
	obj.volumeClient = nil
	obj.locationClient = nil
	obj.volume = nil
	return nil
}

// Watch is not implemented for this resource, since the Hetzner API does not provide events.
func (obj *HetznerVolumeRes) Watch(context.Context) error {
	return fmt.Errorf("invalid Watch call: requires poll metaparam")
}

// CheckApply checks and applies volume lifecycle state.
func (obj *HetznerVolumeRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	if err := obj.getVolumeUpdate(ctx); err != nil {
		return false, errwrap.Wrapf(err, "getVolumeUpdate failed")
	}

	if obj.volume == nil {
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
		if obj.volume.Protection.Delete {
			if err := obj.setDeleteProtection(ctx, false); err != nil {
				return false, errwrap.Wrapf(err, "disable delete protection failed")
			}
			if err := obj.getVolumeUpdate(ctx); err != nil {
				return false, errwrap.Wrapf(err, "getVolumeUpdate after protection change failed")
			}
		}
		if err := obj.delete(ctx); err != nil {
			return false, errwrap.Wrapf(err, "delete failed")
		}
		return false, nil
	}

	if obj.volume.Location == nil || obj.volume.Location.Name != obj.Location {
		return false, fmt.Errorf("location differs and is not mutable")
	}
	if obj.Format != "" {
		if obj.volume.Format == nil || *obj.volume.Format != obj.Format {
			return false, fmt.Errorf("format differs and is not mutable")
		}
	}
	if obj.Size < obj.volume.Size {
		return false, fmt.Errorf("volume shrinking is not supported")
	}

	checkOK := true
	if !maps.Equal(obj.volume.Labels, obj.Labels) {
		if !apply {
			return false, nil
		}
		if err := obj.update(ctx); err != nil {
			return false, errwrap.Wrapf(err, "update failed")
		}
		checkOK = false
	}
	if obj.volume.Protection.Delete != obj.DeleteProtection {
		if !apply {
			return false, nil
		}
		if err := obj.setDeleteProtection(ctx, obj.DeleteProtection); err != nil {
			return false, errwrap.Wrapf(err, "set delete protection failed")
		}
		checkOK = false
	}
	if obj.Size > obj.volume.Size {
		if !apply {
			return false, nil
		}
		if err := obj.resize(ctx); err != nil {
			return false, errwrap.Wrapf(err, "resize failed")
		}
		checkOK = false
	}
	return checkOK, nil
}

func (obj *HetznerVolumeRes) create(ctx context.Context) error {
	location, err := hetznerLocationByName(ctx, obj.locationClient, obj.Location)
	if err != nil {
		return errwrap.Wrapf(err, "location lookup failed")
	}
	if location == nil {
		return fmt.Errorf("location not found: %s", obj.Location)
	}

	var format *string
	if obj.Format != "" {
		format = hcloud.Ptr(obj.Format)
	}

	result, _, err := obj.volumeClient.Create(ctx, hcloud.VolumeCreateOpts{
		Name:     obj.Name(),
		Size:     obj.Size,
		Location: location,
		Labels:   obj.Labels,
		Format:   format,
	})
	if err != nil {
		return err
	}
	obj.volume = result.Volume

	actions := []*hcloud.Action{}
	if result.Action != nil {
		actions = append(actions, result.Action)
	}
	actions = append(actions, result.NextActions...)
	if len(actions) == 0 {
		return nil
	}
	return errwrap.Wrapf(hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, actions...), "wait for create actions failed")
}

func (obj *HetznerVolumeRes) delete(ctx context.Context) error {
	_, err := obj.volumeClient.Delete(ctx, obj.volume)
	return err
}

func (obj *HetznerVolumeRes) update(ctx context.Context) error {
	volume, _, err := obj.volumeClient.Update(ctx, obj.volume, hcloud.VolumeUpdateOpts{
		Name:   obj.Name(),
		Labels: obj.Labels,
	})
	if err != nil {
		return err
	}
	obj.volume = volume
	return nil
}

func (obj *HetznerVolumeRes) setDeleteProtection(ctx context.Context, enabled bool) error {
	action, _, err := obj.volumeClient.ChangeProtection(ctx, obj.volume, hcloud.VolumeChangeProtectionOpts{
		Delete: hcloud.Ptr(enabled),
	})
	if err != nil {
		return err
	}
	return errwrap.Wrapf(hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action), "wait for change protection action failed")
}

func (obj *HetznerVolumeRes) resize(ctx context.Context) error {
	action, _, err := obj.volumeClient.Resize(ctx, obj.volume, obj.Size)
	if err != nil {
		return err
	}
	return errwrap.Wrapf(hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action), "wait for resize action failed")
}

func (obj *HetznerVolumeRes) getVolumeUpdate(ctx context.Context) error {
	volume, err := hetznerVolumeByName(ctx, obj.volumeClient, obj.Name())
	if err != nil {
		return err
	}
	obj.volume = volume
	return nil
}

// Cmp compares two resources.
func (obj *HetznerVolumeRes) Cmp(r engine.Res) error {
	res, ok := r.(*HetznerVolumeRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.APIToken != res.APIToken {
		return fmt.Errorf("apitoken differs")
	}
	if obj.State != res.State {
		return fmt.Errorf("state differs")
	}
	if obj.Location != res.Location {
		return fmt.Errorf("location differs")
	}
	if obj.Size != res.Size {
		return fmt.Errorf("size differs")
	}
	if obj.Format != res.Format {
		return fmt.Errorf("format differs")
	}
	if !maps.Equal(obj.Labels, res.Labels) {
		return fmt.Errorf("labels differ")
	}
	if obj.DeleteProtection != res.DeleteProtection {
		return fmt.Errorf("deleteprotection differs")
	}
	if obj.WaitInterval != res.WaitInterval {
		return fmt.Errorf("waitinterval differs")
	}
	if obj.WaitTimeout != res.WaitTimeout {
		return fmt.Errorf("waittimeout differs")
	}
	return nil
}
