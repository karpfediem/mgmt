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
	"net"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func init() {
	engine.RegisterResource("hetzner:network", func() engine.Res { return &HetznerNetworkRes{} })
}

// HetznerNetworkSubnetSpec describes the desired subnet configuration.
type HetznerNetworkSubnetSpec struct {
	Type        string `lang:"type"`
	IPRange     string `lang:"iprange"`
	NetworkZone string `lang:"networkzone"`
	VSwitchID   int64  `lang:"vswitchid"`
}

// HetznerNetworkRouteSpec describes the desired route configuration.
type HetznerNetworkRouteSpec struct {
	Destination string `lang:"destination"`
	Gateway     string `lang:"gateway"`
}

// HetznerNetworkRes manages a Hetzner private network.
type HetznerNetworkRes struct {
	traits.Base

	init *engine.Init

	APIToken              string                     `lang:"apitoken"`
	State                 string                     `lang:"state"`
	IPRange               string                     `lang:"iprange"`
	Subnets               []HetznerNetworkSubnetSpec `lang:"subnets"`
	Routes                []HetznerNetworkRouteSpec  `lang:"routes"`
	Labels                map[string]string          `lang:"labels"`
	DeleteProtection      bool                       `lang:"deleteprotection"`
	ExposeRoutesToVSwitch bool                       `lang:"exposeroutestovswitch"`

	WaitInterval uint32 `lang:"waitinterval"`
	WaitTimeout  uint32 `lang:"waittimeout"`

	client        *hcloud.Client
	actionWaiter  hetznerActionWaiter
	networkClient hetznerNetworkLifecycleClient

	network *hcloud.Network
}

// Default returns conservative defaults for this resource.
func (obj *HetznerNetworkRes) Default() engine.Res {
	return &HetznerNetworkRes{
		State:        HetznerStateExists,
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
}

// Validate checks whether the requested network spec is valid.
func (obj *HetznerNetworkRes) Validate() error {
	if obj.APIToken == "" {
		return fmt.Errorf("empty token string")
	}
	if err := validateHetznerState(obj.State); err != nil {
		return err
	}
	if obj.State == HetznerStateExists {
		if obj.IPRange == "" {
			return fmt.Errorf("empty iprange")
		}
		if _, err := parseHetznerCIDR(obj.IPRange); err != nil {
			return err
		}
	}
	if _, err := obj.desiredSubnets(); err != nil {
		return err
	}
	if _, err := obj.desiredRoutes(); err != nil {
		return err
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
func (obj *HetznerNetworkRes) Init(init *engine.Init) error {
	obj.init = init
	obj.client = newHetznerClient(init, obj.APIToken, obj.WaitInterval)
	obj.actionWaiter = &obj.client.Action
	obj.networkClient = &obj.client.Network
	return nil
}

// Cleanup removes authentication and client state from memory.
func (obj *HetznerNetworkRes) Cleanup() error {
	obj.APIToken = ""
	obj.client = nil
	obj.actionWaiter = nil
	obj.networkClient = nil
	obj.network = nil
	return nil
}

// Watch is not implemented for this resource, since the Hetzner API does not provide events.
func (obj *HetznerNetworkRes) Watch(context.Context) error {
	return fmt.Errorf("invalid Watch call: requires poll metaparam")
}

// CheckApply checks and applies network lifecycle state.
func (obj *HetznerNetworkRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	if err := obj.getNetworkUpdate(ctx); err != nil {
		return false, errwrap.Wrapf(err, "getNetworkUpdate failed")
	}

	if obj.network == nil {
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
		if obj.network.Protection.Delete {
			if err := obj.setDeleteProtection(ctx, false); err != nil {
				return false, errwrap.Wrapf(err, "disable delete protection failed")
			}
			if err := obj.getNetworkUpdate(ctx); err != nil {
				return false, errwrap.Wrapf(err, "getNetworkUpdate after protection change failed")
			}
		}
		if err := obj.delete(ctx); err != nil {
			return false, errwrap.Wrapf(err, "delete failed")
		}
		return false, nil
	}

	checkOK := true
	desiredIPRange, _ := parseHetznerCIDR(obj.IPRange)
	if hetznerCIDRString(obj.network.IPRange) != hetznerCIDRString(desiredIPRange) {
		if !apply {
			return false, nil
		}
		if err := obj.changeIPRange(ctx, desiredIPRange); err != nil {
			return false, errwrap.Wrapf(err, "change ip range failed")
		}
		checkOK = false
	}

	if !maps.Equal(obj.network.Labels, obj.Labels) || obj.network.ExposeRoutesToVSwitch != obj.ExposeRoutesToVSwitch {
		if !apply {
			return false, nil
		}
		if err := obj.update(ctx); err != nil {
			return false, errwrap.Wrapf(err, "update failed")
		}
		checkOK = false
	}

	subnetsOK, err := obj.applySubnets(ctx, apply)
	if err != nil {
		return false, errwrap.Wrapf(err, "applySubnets failed")
	}
	if !subnetsOK {
		checkOK = false
	}

	routesOK, err := obj.applyRoutes(ctx, apply)
	if err != nil {
		return false, errwrap.Wrapf(err, "applyRoutes failed")
	}
	if !routesOK {
		checkOK = false
	}

	if obj.network.Protection.Delete != obj.DeleteProtection {
		if !apply {
			return false, nil
		}
		if err := obj.setDeleteProtection(ctx, obj.DeleteProtection); err != nil {
			return false, errwrap.Wrapf(err, "set delete protection failed")
		}
		checkOK = false
	}

	return checkOK, nil
}

func (obj *HetznerNetworkRes) create(ctx context.Context) error {
	ipRange, err := parseHetznerCIDR(obj.IPRange)
	if err != nil {
		return err
	}
	subnets, err := obj.desiredSubnets()
	if err != nil {
		return err
	}
	routes, err := obj.desiredRoutes()
	if err != nil {
		return err
	}
	network, _, err := obj.networkClient.Create(ctx, hcloud.NetworkCreateOpts{
		Name:                  obj.Name(),
		IPRange:               ipRange,
		Subnets:               subnets,
		Routes:                routes,
		Labels:                obj.Labels,
		ExposeRoutesToVSwitch: obj.ExposeRoutesToVSwitch,
	})
	if err != nil {
		return err
	}
	obj.network = network
	return nil
}

func (obj *HetznerNetworkRes) delete(ctx context.Context) error {
	_, err := obj.networkClient.Delete(ctx, obj.network)
	return err
}

func (obj *HetznerNetworkRes) update(ctx context.Context) error {
	network, _, err := obj.networkClient.Update(ctx, obj.network, hcloud.NetworkUpdateOpts{
		Name:                  obj.Name(),
		Labels:                obj.Labels,
		ExposeRoutesToVSwitch: hcloud.Ptr(obj.ExposeRoutesToVSwitch),
	})
	if err != nil {
		return err
	}
	obj.network = network
	return nil
}

func (obj *HetznerNetworkRes) changeIPRange(ctx context.Context, ipRange *net.IPNet) error {
	action, _, err := obj.networkClient.ChangeIPRange(ctx, obj.network, hcloud.NetworkChangeIPRangeOpts{
		IPRange: ipRange,
	})
	if err != nil {
		return err
	}
	return errwrap.Wrapf(hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action), "wait for change ip range action failed")
}

func (obj *HetznerNetworkRes) setDeleteProtection(ctx context.Context, enabled bool) error {
	action, _, err := obj.networkClient.ChangeProtection(ctx, obj.network, hcloud.NetworkChangeProtectionOpts{
		Delete: hcloud.Ptr(enabled),
	})
	if err != nil {
		return err
	}
	return errwrap.Wrapf(hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action), "wait for change protection action failed")
}

func (obj *HetznerNetworkRes) applySubnets(ctx context.Context, apply bool) (bool, error) {
	desired, err := obj.desiredSubnets()
	if err != nil {
		return false, err
	}

	liveByKey := map[string]hcloud.NetworkSubnet{}
	for _, subnet := range obj.network.Subnets {
		liveByKey[hetznerNetworkSubnetKey(subnet)] = subnet
	}
	desiredByKey := map[string]hcloud.NetworkSubnet{}
	for _, subnet := range desired {
		desiredByKey[hetznerNetworkSubnetKey(subnet)] = subnet
	}

	checkOK := true
	for key, subnet := range liveByKey {
		if _, exists := desiredByKey[key]; exists {
			continue
		}
		if !apply {
			return false, nil
		}
		action, _, err := obj.networkClient.DeleteSubnet(ctx, obj.network, hcloud.NetworkDeleteSubnetOpts{
			Subnet: subnet,
		})
		if err != nil {
			return false, err
		}
		if err := hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action); err != nil {
			return false, errwrap.Wrapf(err, "wait for delete subnet action failed")
		}
		checkOK = false
	}
	for key, subnet := range desiredByKey {
		if _, exists := liveByKey[key]; exists {
			continue
		}
		if !apply {
			return false, nil
		}
		action, _, err := obj.networkClient.AddSubnet(ctx, obj.network, hcloud.NetworkAddSubnetOpts{
			Subnet: subnet,
		})
		if err != nil {
			return false, err
		}
		if err := hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action); err != nil {
			return false, errwrap.Wrapf(err, "wait for add subnet action failed")
		}
		checkOK = false
	}
	return checkOK, nil
}

func (obj *HetznerNetworkRes) applyRoutes(ctx context.Context, apply bool) (bool, error) {
	desired, err := obj.desiredRoutes()
	if err != nil {
		return false, err
	}

	liveByKey := map[string]hcloud.NetworkRoute{}
	for _, route := range obj.network.Routes {
		liveByKey[hetznerNetworkRouteKey(route)] = route
	}
	desiredByKey := map[string]hcloud.NetworkRoute{}
	for _, route := range desired {
		desiredByKey[hetznerNetworkRouteKey(route)] = route
	}

	checkOK := true
	for key, route := range liveByKey {
		if _, exists := desiredByKey[key]; exists {
			continue
		}
		if !apply {
			return false, nil
		}
		action, _, err := obj.networkClient.DeleteRoute(ctx, obj.network, hcloud.NetworkDeleteRouteOpts{
			Route: route,
		})
		if err != nil {
			return false, err
		}
		if err := hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action); err != nil {
			return false, errwrap.Wrapf(err, "wait for delete route action failed")
		}
		checkOK = false
	}
	for key, route := range desiredByKey {
		if _, exists := liveByKey[key]; exists {
			continue
		}
		if !apply {
			return false, nil
		}
		action, _, err := obj.networkClient.AddRoute(ctx, obj.network, hcloud.NetworkAddRouteOpts{
			Route: route,
		})
		if err != nil {
			return false, err
		}
		if err := hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action); err != nil {
			return false, errwrap.Wrapf(err, "wait for add route action failed")
		}
		checkOK = false
	}
	return checkOK, nil
}

func (obj *HetznerNetworkRes) desiredSubnets() ([]hcloud.NetworkSubnet, error) {
	subnets := make([]hcloud.NetworkSubnet, 0, len(obj.Subnets))
	seen := map[string]struct{}{}
	for _, spec := range obj.Subnets {
		subnet, err := hetznerNetworkSubnetFromSpec(spec)
		if err != nil {
			return nil, err
		}
		key := hetznerNetworkSubnetKey(subnet)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate subnet spec: %s", key)
		}
		seen[key] = struct{}{}
		subnets = append(subnets, subnet)
	}
	return subnets, nil
}

func (obj *HetznerNetworkRes) desiredRoutes() ([]hcloud.NetworkRoute, error) {
	routes := make([]hcloud.NetworkRoute, 0, len(obj.Routes))
	seen := map[string]struct{}{}
	for _, spec := range obj.Routes {
		route, err := hetznerNetworkRouteFromSpec(spec)
		if err != nil {
			return nil, err
		}
		key := hetznerNetworkRouteKey(route)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate route spec: %s", key)
		}
		seen[key] = struct{}{}
		routes = append(routes, route)
	}
	return routes, nil
}

func (obj *HetznerNetworkRes) getNetworkUpdate(ctx context.Context) error {
	network, err := hetznerNetworkByName(ctx, obj.networkClient, obj.Name())
	if err != nil {
		return err
	}
	obj.network = network
	return nil
}

// Cmp compares two resources.
func (obj *HetznerNetworkRes) Cmp(r engine.Res) error {
	res, ok := r.(*HetznerNetworkRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.APIToken != res.APIToken {
		return fmt.Errorf("apitoken differs")
	}
	if obj.State != res.State {
		return fmt.Errorf("state differs")
	}
	if obj.IPRange != res.IPRange {
		return fmt.Errorf("iprange differs")
	}
	if !maps.Equal(obj.Labels, res.Labels) {
		return fmt.Errorf("labels differ")
	}
	if obj.DeleteProtection != res.DeleteProtection {
		return fmt.Errorf("deleteprotection differs")
	}
	if obj.ExposeRoutesToVSwitch != res.ExposeRoutesToVSwitch {
		return fmt.Errorf("exposeroutestovswitch differs")
	}
	if !hetznerSubnetSpecListsEqual(obj.Subnets, res.Subnets) {
		return fmt.Errorf("subnets differ")
	}
	if !hetznerRouteSpecListsEqual(obj.Routes, res.Routes) {
		return fmt.Errorf("routes differ")
	}
	if obj.WaitInterval != res.WaitInterval {
		return fmt.Errorf("waitinterval differs")
	}
	if obj.WaitTimeout != res.WaitTimeout {
		return fmt.Errorf("waittimeout differs")
	}
	return nil
}

func hetznerNetworkSubnetFromSpec(spec HetznerNetworkSubnetSpec) (hcloud.NetworkSubnet, error) {
	ipRange, err := parseHetznerCIDR(spec.IPRange)
	if err != nil {
		return hcloud.NetworkSubnet{}, err
	}
	if ipRange == nil {
		return hcloud.NetworkSubnet{}, fmt.Errorf("invalid empty subnet iprange")
	}
	subnet := hcloud.NetworkSubnet{
		Type:        hcloud.NetworkSubnetType(spec.Type),
		IPRange:     ipRange,
		NetworkZone: hcloud.NetworkZone(spec.NetworkZone),
		VSwitchID:   spec.VSwitchID,
	}
	switch subnet.Type {
	case hcloud.NetworkSubnetTypeCloud, hcloud.NetworkSubnetTypeServer, hcloud.NetworkSubnetTypeVSwitch:
	default:
		return hcloud.NetworkSubnet{}, fmt.Errorf("invalid subnet type: %s", spec.Type)
	}
	switch subnet.NetworkZone {
	case hcloud.NetworkZoneEUCentral, hcloud.NetworkZoneUSEast, hcloud.NetworkZoneUSWest, hcloud.NetworkZoneAPSouthEast:
	default:
		return hcloud.NetworkSubnet{}, fmt.Errorf("invalid network zone: %s", spec.NetworkZone)
	}
	return subnet, nil
}

func hetznerNetworkRouteFromSpec(spec HetznerNetworkRouteSpec) (hcloud.NetworkRoute, error) {
	destination, err := parseHetznerCIDR(spec.Destination)
	if err != nil {
		return hcloud.NetworkRoute{}, err
	}
	if destination == nil {
		return hcloud.NetworkRoute{}, fmt.Errorf("invalid empty destination")
	}
	gateway, err := parseHetznerIP(spec.Gateway)
	if err != nil {
		return hcloud.NetworkRoute{}, err
	}
	if gateway == nil {
		return hcloud.NetworkRoute{}, fmt.Errorf("invalid empty gateway")
	}
	return hcloud.NetworkRoute{
		Destination: destination,
		Gateway:     gateway,
	}, nil
}

func hetznerNetworkSubnetKey(subnet hcloud.NetworkSubnet) string {
	return fmt.Sprintf("%s|%s|%s|%d", subnet.Type, subnet.NetworkZone, hetznerCIDRString(subnet.IPRange), subnet.VSwitchID)
}

func hetznerNetworkRouteKey(route hcloud.NetworkRoute) string {
	return fmt.Sprintf("%s|%s", hetznerCIDRString(route.Destination), route.Gateway.String())
}

func hetznerCIDRString(cidr *net.IPNet) string {
	if cidr == nil {
		return ""
	}
	return cidr.String()
}

func hetznerSubnetSpecListsEqual(left []HetznerNetworkSubnetSpec, right []HetznerNetworkSubnetSpec) bool {
	if len(left) != len(right) {
		return false
	}
	leftMap := map[string]struct{}{}
	for _, spec := range left {
		subnet, err := hetznerNetworkSubnetFromSpec(spec)
		if err != nil {
			return false
		}
		leftMap[hetznerNetworkSubnetKey(subnet)] = struct{}{}
	}
	for _, spec := range right {
		subnet, err := hetznerNetworkSubnetFromSpec(spec)
		if err != nil {
			return false
		}
		if _, exists := leftMap[hetznerNetworkSubnetKey(subnet)]; !exists {
			return false
		}
	}
	return true
}

func hetznerRouteSpecListsEqual(left []HetznerNetworkRouteSpec, right []HetznerNetworkRouteSpec) bool {
	if len(left) != len(right) {
		return false
	}
	leftMap := map[string]struct{}{}
	for _, spec := range left {
		route, err := hetznerNetworkRouteFromSpec(spec)
		if err != nil {
			return false
		}
		leftMap[hetznerNetworkRouteKey(route)] = struct{}{}
	}
	for _, spec := range right {
		route, err := hetznerNetworkRouteFromSpec(spec)
		if err != nil {
			return false
		}
		if _, exists := leftMap[hetznerNetworkRouteKey(route)]; !exists {
			return false
		}
	}
	return true
}
