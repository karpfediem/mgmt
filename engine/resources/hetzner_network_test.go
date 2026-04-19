package resources

import (
	"context"
	"net"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type fakeHetznerNetworkLifecycleClient struct {
	network *hcloud.Network
	err     error

	createCalls           []hcloud.NetworkCreateOpts
	updateCalls           []hcloud.NetworkUpdateOpts
	deleteCalls           int
	changeIPRangeCalls    []hcloud.NetworkChangeIPRangeOpts
	addSubnetCalls        []hcloud.NetworkAddSubnetOpts
	deleteSubnetCalls     []hcloud.NetworkDeleteSubnetOpts
	addRouteCalls         []hcloud.NetworkAddRouteOpts
	deleteRouteCalls      []hcloud.NetworkDeleteRouteOpts
	changeProtectionCalls []hcloud.NetworkChangeProtectionOpts
}

func (obj *fakeHetznerNetworkLifecycleClient) GetByName(_ context.Context, _ string) (*hcloud.Network, *hcloud.Response, error) {
	return obj.network, nil, obj.err
}

func (obj *fakeHetznerNetworkLifecycleClient) Create(_ context.Context, opts hcloud.NetworkCreateOpts) (*hcloud.Network, *hcloud.Response, error) {
	obj.createCalls = append(obj.createCalls, opts)
	obj.network = &hcloud.Network{
		ID:                    100,
		Name:                  opts.Name,
		IPRange:               opts.IPRange,
		Subnets:               append([]hcloud.NetworkSubnet(nil), opts.Subnets...),
		Routes:                append([]hcloud.NetworkRoute(nil), opts.Routes...),
		Labels:                opts.Labels,
		ExposeRoutesToVSwitch: opts.ExposeRoutesToVSwitch,
	}
	return obj.network, nil, obj.err
}

func (obj *fakeHetznerNetworkLifecycleClient) Update(_ context.Context, _ *hcloud.Network, opts hcloud.NetworkUpdateOpts) (*hcloud.Network, *hcloud.Response, error) {
	obj.updateCalls = append(obj.updateCalls, opts)
	if obj.network != nil {
		obj.network.Name = opts.Name
		obj.network.Labels = opts.Labels
		if opts.ExposeRoutesToVSwitch != nil {
			obj.network.ExposeRoutesToVSwitch = *opts.ExposeRoutesToVSwitch
		}
	}
	return obj.network, nil, obj.err
}

func (obj *fakeHetznerNetworkLifecycleClient) Delete(_ context.Context, _ *hcloud.Network) (*hcloud.Response, error) {
	obj.deleteCalls++
	obj.network = nil
	return nil, obj.err
}

func (obj *fakeHetznerNetworkLifecycleClient) ChangeIPRange(_ context.Context, _ *hcloud.Network, opts hcloud.NetworkChangeIPRangeOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.changeIPRangeCalls = append(obj.changeIPRangeCalls, opts)
	if obj.network != nil {
		obj.network.IPRange = opts.IPRange
	}
	return &hcloud.Action{ID: 1000, Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func (obj *fakeHetznerNetworkLifecycleClient) AddSubnet(_ context.Context, _ *hcloud.Network, opts hcloud.NetworkAddSubnetOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.addSubnetCalls = append(obj.addSubnetCalls, opts)
	if obj.network != nil {
		obj.network.Subnets = append(obj.network.Subnets, opts.Subnet)
	}
	return &hcloud.Action{ID: 1001 + int64(len(obj.addSubnetCalls)), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func (obj *fakeHetznerNetworkLifecycleClient) DeleteSubnet(_ context.Context, _ *hcloud.Network, opts hcloud.NetworkDeleteSubnetOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.deleteSubnetCalls = append(obj.deleteSubnetCalls, opts)
	if obj.network != nil {
		filtered := make([]hcloud.NetworkSubnet, 0, len(obj.network.Subnets))
		for _, subnet := range obj.network.Subnets {
			if hetznerNetworkSubnetKey(subnet) == hetznerNetworkSubnetKey(opts.Subnet) {
				continue
			}
			filtered = append(filtered, subnet)
		}
		obj.network.Subnets = filtered
	}
	return &hcloud.Action{ID: 1101 + int64(len(obj.deleteSubnetCalls)), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func (obj *fakeHetznerNetworkLifecycleClient) AddRoute(_ context.Context, _ *hcloud.Network, opts hcloud.NetworkAddRouteOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.addRouteCalls = append(obj.addRouteCalls, opts)
	if obj.network != nil {
		obj.network.Routes = append(obj.network.Routes, opts.Route)
	}
	return &hcloud.Action{ID: 1201 + int64(len(obj.addRouteCalls)), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func (obj *fakeHetznerNetworkLifecycleClient) DeleteRoute(_ context.Context, _ *hcloud.Network, opts hcloud.NetworkDeleteRouteOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.deleteRouteCalls = append(obj.deleteRouteCalls, opts)
	if obj.network != nil {
		filtered := make([]hcloud.NetworkRoute, 0, len(obj.network.Routes))
		for _, route := range obj.network.Routes {
			if hetznerNetworkRouteKey(route) == hetznerNetworkRouteKey(opts.Route) {
				continue
			}
			filtered = append(filtered, route)
		}
		obj.network.Routes = filtered
	}
	return &hcloud.Action{ID: 1301 + int64(len(obj.deleteRouteCalls)), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func (obj *fakeHetznerNetworkLifecycleClient) ChangeProtection(_ context.Context, _ *hcloud.Network, opts hcloud.NetworkChangeProtectionOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.changeProtectionCalls = append(obj.changeProtectionCalls, opts)
	if obj.network != nil && opts.Delete != nil {
		obj.network.Protection.Delete = *opts.Delete
	}
	return &hcloud.Action{ID: 1401 + int64(len(obj.changeProtectionCalls)), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func TestHetznerNetworkCreate(t *testing.T) {
	client := &fakeHetznerNetworkLifecycleClient{}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerNetworkRes{
		APIToken:              "token",
		State:                 HetznerStateExists,
		IPRange:               "10.0.0.0/16",
		Subnets:               []HetznerNetworkSubnetSpec{{Type: string(hcloud.NetworkSubnetTypeCloud), IPRange: "10.0.1.0/24", NetworkZone: string(hcloud.NetworkZoneEUCentral)}},
		Routes:                []HetznerNetworkRouteSpec{{Destination: "10.1.0.0/16", Gateway: "10.0.1.1"}},
		Labels:                map[string]string{"env": "beta"},
		ExposeRoutesToVSwitch: true,
		WaitInterval:          HetznerWaitIntervalDefault,
		WaitTimeout:           HetznerWaitTimeoutDefault,
		networkClient:         client,
		actionWaiter:          waiter,
	}
	res.SetName("private")

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected create to report non-converged on apply")
	}
	if len(client.createCalls) != 1 {
		t.Fatalf("expected one create call, got %d", len(client.createCalls))
	}
	if got := client.createCalls[0].Name; got != "private" {
		t.Fatalf("unexpected create name: %s", got)
	}
	if got := client.createCalls[0].IPRange.String(); got != "10.0.0.0/16" {
		t.Fatalf("unexpected create ip range: %s", got)
	}
	if len(client.createCalls[0].Subnets) != 1 {
		t.Fatalf("expected one subnet, got %d", len(client.createCalls[0].Subnets))
	}
	if len(client.createCalls[0].Routes) != 1 {
		t.Fatalf("expected one route, got %d", len(client.createCalls[0].Routes))
	}
	if len(waiter.actions) != 0 {
		t.Fatalf("expected no waiter actions on create, got %d", len(waiter.actions))
	}
}

func TestHetznerNetworkReconcile(t *testing.T) {
	client := &fakeHetznerNetworkLifecycleClient{
		network: &hcloud.Network{
			ID:      100,
			Name:    "private",
			IPRange: mustParseCIDR(t, "10.2.0.0/16"),
			Subnets: []hcloud.NetworkSubnet{mustNetworkSubnet(t, string(hcloud.NetworkSubnetTypeCloud), "10.2.1.0/24", string(hcloud.NetworkZoneEUCentral))},
			Routes:  []hcloud.NetworkRoute{mustNetworkRoute(t, "10.9.0.0/16", "10.2.1.1")},
			Labels:  map[string]string{"env": "old"},
			Protection: hcloud.NetworkProtection{
				Delete: false,
			},
			ExposeRoutesToVSwitch: false,
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerNetworkRes{
		APIToken:              "token",
		State:                 HetznerStateExists,
		IPRange:               "10.0.0.0/16",
		Subnets:               []HetznerNetworkSubnetSpec{{Type: string(hcloud.NetworkSubnetTypeCloud), IPRange: "10.0.1.0/24", NetworkZone: string(hcloud.NetworkZoneEUCentral)}},
		Routes:                []HetznerNetworkRouteSpec{{Destination: "10.1.0.0/16", Gateway: "10.0.1.1"}},
		Labels:                map[string]string{"env": "beta"},
		DeleteProtection:      true,
		ExposeRoutesToVSwitch: true,
		WaitInterval:          HetznerWaitIntervalDefault,
		WaitTimeout:           HetznerWaitTimeoutDefault,
		networkClient:         client,
		actionWaiter:          waiter,
	}
	res.SetName("private")

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected reconcile to report non-converged on apply")
	}
	if len(client.changeIPRangeCalls) != 1 {
		t.Fatalf("expected one ip range change, got %d", len(client.changeIPRangeCalls))
	}
	if len(client.updateCalls) != 1 {
		t.Fatalf("expected one update call, got %d", len(client.updateCalls))
	}
	if len(client.deleteSubnetCalls) != 1 || len(client.addSubnetCalls) != 1 {
		t.Fatalf("expected one subnet delete and add, got %d delete / %d add", len(client.deleteSubnetCalls), len(client.addSubnetCalls))
	}
	if len(client.deleteRouteCalls) != 1 || len(client.addRouteCalls) != 1 {
		t.Fatalf("expected one route delete and add, got %d delete / %d add", len(client.deleteRouteCalls), len(client.addRouteCalls))
	}
	if len(client.changeProtectionCalls) != 1 {
		t.Fatalf("expected one protection change, got %d", len(client.changeProtectionCalls))
	}
	if got := len(waiter.actions); got != 6 {
		t.Fatalf("expected waiter to observe six actions, got %d", got)
	}
	if got := client.network.IPRange.String(); got != "10.0.0.0/16" {
		t.Fatalf("unexpected reconciled ip range: %s", got)
	}
	if !client.network.Protection.Delete {
		t.Fatalf("expected delete protection to be enabled")
	}
}

func TestHetznerNetworkAbsentDisablesProtection(t *testing.T) {
	client := &fakeHetznerNetworkLifecycleClient{
		network: &hcloud.Network{
			ID:   100,
			Name: "private",
			Protection: hcloud.NetworkProtection{
				Delete: true,
			},
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerNetworkRes{
		APIToken:      "token",
		State:         HetznerStateAbsent,
		WaitInterval:  HetznerWaitIntervalDefault,
		WaitTimeout:   HetznerWaitTimeoutDefault,
		networkClient: client,
		actionWaiter:  waiter,
	}
	res.SetName("private")

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected delete to report non-converged on apply")
	}
	if len(client.changeProtectionCalls) != 1 {
		t.Fatalf("expected one protection change, got %d", len(client.changeProtectionCalls))
	}
	if client.deleteCalls != 1 {
		t.Fatalf("expected one delete call, got %d", client.deleteCalls)
	}
	if got := len(waiter.actions); got != 1 {
		t.Fatalf("expected waiter to observe one action, got %d", got)
	}
}

func TestHetznerNetworkValidateRejectsEmptyIPRange(t *testing.T) {
	res := &HetznerNetworkRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	if err := res.Validate(); err == nil {
		t.Fatalf("expected empty iprange validation error")
	}
}

func mustParseCIDR(t *testing.T, value string) *net.IPNet {
	t.Helper()
	cidr, err := parseHetznerCIDR(value)
	if err != nil {
		t.Fatalf("parseHetznerCIDR(%q) failed: %v", value, err)
	}
	return cidr
}

func mustNetworkSubnet(t *testing.T, subnetType string, ipRange string, networkZone string) hcloud.NetworkSubnet {
	t.Helper()
	subnet, err := hetznerNetworkSubnetFromSpec(HetznerNetworkSubnetSpec{
		Type:        subnetType,
		IPRange:     ipRange,
		NetworkZone: networkZone,
	})
	if err != nil {
		t.Fatalf("hetznerNetworkSubnetFromSpec failed: %v", err)
	}
	return subnet
}

func mustNetworkRoute(t *testing.T, destination string, gateway string) hcloud.NetworkRoute {
	t.Helper()
	route, err := hetznerNetworkRouteFromSpec(HetznerNetworkRouteSpec{
		Destination: destination,
		Gateway:     gateway,
	})
	if err != nil {
		t.Fatalf("hetznerNetworkRouteFromSpec failed: %v", err)
	}
	return route
}
