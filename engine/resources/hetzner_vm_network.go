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
	"net"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func init() {
	engine.RegisterResource("hetzner:vm_network", func() engine.Res { return &HetznerVMNetworkRes{} })
}

// HetznerVMNetworkRes manages a private network attachment on an existing Hetzner VM.
type HetznerVMNetworkRes struct {
	traits.Base

	init *engine.Init

	APIToken string   `lang:"apitoken"`
	State    string   `lang:"state"`
	Server   string   `lang:"server"`
	Network  string   `lang:"network"`
	IP       string   `lang:"ip"`
	AliasIPs []string `lang:"aliasips"`

	WaitInterval uint32 `lang:"waitinterval"`
	WaitTimeout  uint32 `lang:"waittimeout"`

	client        *hcloud.Client
	actionWaiter  hetznerActionWaiter
	serverClient  hetznerServerNetworkClient
	networkClient hetznerNetworkLookupClient

	server  *hcloud.Server
	network *hcloud.Network
}

// Default returns some conservative defaults for this resource.
func (obj *HetznerVMNetworkRes) Default() engine.Res {
	return &HetznerVMNetworkRes{
		State:        HetznerStateExists,
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
}

// Validate checks whether the requested attachment spec is valid.
func (obj *HetznerVMNetworkRes) Validate() error {
	if obj.APIToken == "" {
		return fmt.Errorf("empty token string")
	}
	if err := validateHetznerState(obj.State); err != nil {
		return err
	}
	if obj.Server == "" {
		return fmt.Errorf("empty server")
	}
	if obj.Network == "" {
		return fmt.Errorf("empty network")
	}
	if obj.MetaParams().Poll < HetznerPollLimit {
		return fmt.Errorf("invalid polling interval (minimum %d s)", HetznerPollLimit)
	}
	if obj.WaitInterval < HetznerWaitIntervalLimit {
		return fmt.Errorf("invalid wait interval (minimum %d)", HetznerWaitIntervalLimit)
	}
	if _, err := parseHetznerIP(obj.IP); err != nil {
		return err
	}
	if _, err := parseHetznerIPs(obj.AliasIPs); err != nil {
		return err
	}
	return nil
}

// Init runs startup code for this resource.
func (obj *HetznerVMNetworkRes) Init(init *engine.Init) error {
	obj.init = init
	obj.client = newHetznerClient(init, obj.APIToken, obj.WaitInterval)
	obj.actionWaiter = &obj.client.Action
	obj.serverClient = &obj.client.Server
	obj.networkClient = &obj.client.Network
	return nil
}

// Cleanup removes authentication and client state from memory.
func (obj *HetznerVMNetworkRes) Cleanup() error {
	obj.APIToken = ""
	obj.client = nil
	obj.actionWaiter = nil
	obj.serverClient = nil
	obj.networkClient = nil
	obj.server = nil
	obj.network = nil
	return nil
}

// Watch is not implemented for this resource, since the Hetzner API does not provide events.
func (obj *HetznerVMNetworkRes) Watch(context.Context) error {
	return fmt.Errorf("invalid Watch call: requires poll metaparam")
}

// CheckApply checks and applies VM private network attachment state.
func (obj *HetznerVMNetworkRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	if err := obj.getTargetUpdate(ctx); err != nil {
		return false, errwrap.Wrapf(err, "getTargetUpdate failed")
	}

	if obj.server == nil {
		if obj.State == HetznerStateAbsent {
			return true, nil
		}
		return false, fmt.Errorf("server not found: %s", obj.Server)
	}
	if obj.network == nil {
		if obj.State == HetznerStateAbsent {
			return true, nil
		}
		return false, fmt.Errorf("network not found: %s", obj.Network)
	}

	attachment := obj.server.PrivateNetFor(obj.network)
	if obj.State == HetznerStateAbsent {
		if attachment == nil {
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

	desiredIP, err := parseHetznerIP(obj.IP)
	if err != nil {
		return false, err
	}
	desiredAliases, err := parseHetznerIPs(obj.AliasIPs)
	if err != nil {
		return false, err
	}

	if attachment == nil {
		if !apply {
			return false, nil
		}
		if err := obj.attach(ctx, desiredIP, desiredAliases); err != nil {
			return false, errwrap.Wrapf(err, "attach failed")
		}
		return false, nil
	}

	if desiredIP != nil && !attachment.IP.Equal(desiredIP) {
		if !apply {
			return false, nil
		}
		if err := obj.detach(ctx); err != nil {
			return false, errwrap.Wrapf(err, "detach for ip change failed")
		}
		if err := obj.getTargetUpdate(ctx); err != nil {
			return false, errwrap.Wrapf(err, "getTargetUpdate after detach failed")
		}
		if err := obj.attach(ctx, desiredIP, desiredAliases); err != nil {
			return false, errwrap.Wrapf(err, "reattach for ip change failed")
		}
		return false, nil
	}

	if !hetznerIPListsEqual(attachment.Aliases, desiredAliases) {
		if !apply {
			return false, nil
		}
		if err := obj.changeAliasIPs(ctx, desiredAliases); err != nil {
			return false, errwrap.Wrapf(err, "changeAliasIPs failed")
		}
		return false, nil
	}

	return true, nil
}

func (obj *HetznerVMNetworkRes) getTargetUpdate(ctx context.Context) error {
	server, err := hetznerServerByName(ctx, obj.serverClient, obj.Server)
	if err != nil {
		return errwrap.Wrapf(err, "server lookup failed")
	}
	network, err := hetznerNetworkByName(ctx, obj.networkClient, obj.Network)
	if err != nil {
		return errwrap.Wrapf(err, "network lookup failed")
	}
	obj.server = server
	obj.network = network
	return nil
}

func (obj *HetznerVMNetworkRes) attach(ctx context.Context, ip net.IP, aliasIPs []net.IP) error {
	action, _, err := obj.serverClient.AttachToNetwork(ctx, obj.server, hcloud.ServerAttachToNetworkOpts{
		Network:  obj.network,
		IP:       ip,
		AliasIPs: aliasIPs,
	})
	if err != nil {
		return err
	}
	return errwrap.Wrapf(hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action), "wait for attach action failed")
}

func (obj *HetznerVMNetworkRes) detach(ctx context.Context) error {
	action, _, err := obj.serverClient.DetachFromNetwork(ctx, obj.server, hcloud.ServerDetachFromNetworkOpts{
		Network: obj.network,
	})
	if err != nil {
		return err
	}
	return errwrap.Wrapf(hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action), "wait for detach action failed")
}

func (obj *HetznerVMNetworkRes) changeAliasIPs(ctx context.Context, aliasIPs []net.IP) error {
	action, _, err := obj.serverClient.ChangeAliasIPs(ctx, obj.server, hcloud.ServerChangeAliasIPsOpts{
		Network:  obj.network,
		AliasIPs: aliasIPs,
	})
	if err != nil {
		return err
	}
	return errwrap.Wrapf(hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action), "wait for alias ip action failed")
}

// Cmp compares two resources.
func (obj *HetznerVMNetworkRes) Cmp(r engine.Res) error {
	res, ok := r.(*HetznerVMNetworkRes)
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
	if obj.Network != res.Network {
		return fmt.Errorf("network differs")
	}
	if obj.IP != res.IP {
		return fmt.Errorf("ip differs")
	}
	if !hetznerStringListsEqual(obj.AliasIPs, res.AliasIPs) {
		return fmt.Errorf("aliasips differ")
	}
	if obj.WaitInterval != res.WaitInterval {
		return fmt.Errorf("waitinterval differs")
	}
	if obj.WaitTimeout != res.WaitTimeout {
		return fmt.Errorf("waittimeout differs")
	}
	return nil
}
