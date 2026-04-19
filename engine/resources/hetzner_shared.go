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
	"slices"
	"time"

	"github.com/purpleidea/mgmt/engine"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type hetznerActionWaiter interface {
	WaitFor(ctx context.Context, actions ...*hcloud.Action) error
}

type hetznerLocationLookupClient interface {
	GetByName(ctx context.Context, name string) (*hcloud.Location, *hcloud.Response, error)
}

type hetznerServerNetworkClient interface {
	GetByName(ctx context.Context, name string) (*hcloud.Server, *hcloud.Response, error)
	AttachToNetwork(ctx context.Context, server *hcloud.Server, opts hcloud.ServerAttachToNetworkOpts) (*hcloud.Action, *hcloud.Response, error)
	DetachFromNetwork(ctx context.Context, server *hcloud.Server, opts hcloud.ServerDetachFromNetworkOpts) (*hcloud.Action, *hcloud.Response, error)
	ChangeAliasIPs(ctx context.Context, server *hcloud.Server, opts hcloud.ServerChangeAliasIPsOpts) (*hcloud.Action, *hcloud.Response, error)
}

type hetznerServerLookupClient interface {
	GetByName(ctx context.Context, name string) (*hcloud.Server, *hcloud.Response, error)
}

type hetznerNetworkLookupClient interface {
	GetByName(ctx context.Context, name string) (*hcloud.Network, *hcloud.Response, error)
}

type hetznerNetworkLifecycleClient interface {
	GetByName(ctx context.Context, name string) (*hcloud.Network, *hcloud.Response, error)
	Create(ctx context.Context, opts hcloud.NetworkCreateOpts) (*hcloud.Network, *hcloud.Response, error)
	Update(ctx context.Context, network *hcloud.Network, opts hcloud.NetworkUpdateOpts) (*hcloud.Network, *hcloud.Response, error)
	Delete(ctx context.Context, network *hcloud.Network) (*hcloud.Response, error)
	ChangeIPRange(ctx context.Context, network *hcloud.Network, opts hcloud.NetworkChangeIPRangeOpts) (*hcloud.Action, *hcloud.Response, error)
	AddSubnet(ctx context.Context, network *hcloud.Network, opts hcloud.NetworkAddSubnetOpts) (*hcloud.Action, *hcloud.Response, error)
	DeleteSubnet(ctx context.Context, network *hcloud.Network, opts hcloud.NetworkDeleteSubnetOpts) (*hcloud.Action, *hcloud.Response, error)
	AddRoute(ctx context.Context, network *hcloud.Network, opts hcloud.NetworkAddRouteOpts) (*hcloud.Action, *hcloud.Response, error)
	DeleteRoute(ctx context.Context, network *hcloud.Network, opts hcloud.NetworkDeleteRouteOpts) (*hcloud.Action, *hcloud.Response, error)
	ChangeProtection(ctx context.Context, network *hcloud.Network, opts hcloud.NetworkChangeProtectionOpts) (*hcloud.Action, *hcloud.Response, error)
}

type hetznerVolumeClient interface {
	GetByName(ctx context.Context, name string) (*hcloud.Volume, *hcloud.Response, error)
	Attach(ctx context.Context, volume *hcloud.Volume, server *hcloud.Server) (*hcloud.Action, *hcloud.Response, error)
	Detach(ctx context.Context, volume *hcloud.Volume) (*hcloud.Action, *hcloud.Response, error)
}

type hetznerVolumeLookupClient interface {
	GetByName(ctx context.Context, name string) (*hcloud.Volume, *hcloud.Response, error)
}

type hetznerVolumeLifecycleClient interface {
	GetByName(ctx context.Context, name string) (*hcloud.Volume, *hcloud.Response, error)
	Create(ctx context.Context, opts hcloud.VolumeCreateOpts) (hcloud.VolumeCreateResult, *hcloud.Response, error)
	Delete(ctx context.Context, volume *hcloud.Volume) (*hcloud.Response, error)
	Update(ctx context.Context, volume *hcloud.Volume, opts hcloud.VolumeUpdateOpts) (*hcloud.Volume, *hcloud.Response, error)
	ChangeProtection(ctx context.Context, volume *hcloud.Volume, opts hcloud.VolumeChangeProtectionOpts) (*hcloud.Action, *hcloud.Response, error)
	Resize(ctx context.Context, volume *hcloud.Volume, size int) (*hcloud.Action, *hcloud.Response, error)
}

func newHetznerClient(init *engine.Init, apiToken string, waitInterval uint32) *hcloud.Client {
	opts := []hcloud.ClientOption{
		hcloud.WithToken(apiToken),
	}
	if init != nil {
		opts = append(opts, hcloud.WithApplication(init.Program, init.Version))
	}
	if waitInterval > 0 {
		opts = append(opts, hcloud.WithPollOpts(hcloud.PollOpts{
			BackoffFunc: hcloud.ConstantBackoff(time.Duration(waitInterval) * time.Second),
		}))
	}
	return hcloud.NewClient(opts...)
}

func hetznerWaitForAction(ctx context.Context, timeout uint32, waiter hetznerActionWaiter, actions ...*hcloud.Action) error {
	waitCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	}
	defer cancel()
	return waiter.WaitFor(waitCtx, actions...)
}

func validateHetznerState(state string) error {
	switch state {
	case HetznerStateExists, HetznerStateAbsent:
		return nil
	default:
		return fmt.Errorf("invalid state: %s", state)
	}
}

func parseHetznerIP(value string) (net.IP, error) {
	if value == "" {
		return nil, nil
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return nil, fmt.Errorf("invalid ip: %s", value)
	}
	return ip, nil
}

func parseHetznerIPs(values []string) ([]net.IP, error) {
	ips := make([]net.IP, 0, len(values))
	for _, value := range values {
		ip, err := parseHetznerIP(value)
		if err != nil {
			return nil, err
		}
		if ip == nil {
			return nil, fmt.Errorf("invalid empty ip")
		}
		ips = append(ips, ip)
	}
	return ips, nil
}

func parseHetznerCIDR(value string) (*net.IPNet, error) {
	if value == "" {
		return nil, nil
	}
	_, cidr, err := net.ParseCIDR(value)
	if err != nil {
		return nil, fmt.Errorf("invalid cidr: %s", value)
	}
	return cidr, nil
}

func hetznerServerByName(ctx context.Context, client hetznerServerLookupClient, name string) (*hcloud.Server, error) {
	server, _, err := client.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	return server, nil
}

func hetznerNetworkByName(ctx context.Context, client hetznerNetworkLookupClient, name string) (*hcloud.Network, error) {
	network, _, err := client.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	return network, nil
}

func hetznerVolumeByName(ctx context.Context, client hetznerVolumeLookupClient, name string) (*hcloud.Volume, error) {
	volume, _, err := client.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	return volume, nil
}

func hetznerLocationByName(ctx context.Context, client hetznerLocationLookupClient, name string) (*hcloud.Location, error) {
	location, _, err := client.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	return location, nil
}

func hetznerIPListsEqual(a []net.IP, b []net.IP) bool {
	if len(a) != len(b) {
		return false
	}
	left := make([]string, 0, len(a))
	for _, ip := range a {
		left = append(left, ip.String())
	}
	right := make([]string, 0, len(b))
	for _, ip := range b {
		right = append(right, ip.String())
	}
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}

func hetznerStringListsEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := slices.Clone(a)
	right := slices.Clone(b)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}
