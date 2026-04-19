package resources

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/purpleidea/mgmt/engine"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type fakeHetznerActionWaiter struct {
	actions []*hcloud.Action
	err     error
}

func (obj *fakeHetznerActionWaiter) WaitFor(_ context.Context, actions ...*hcloud.Action) error {
	obj.actions = append(obj.actions, actions...)
	return obj.err
}

type fakeHetznerServerNetworkClient struct {
	server           *hcloud.Server
	err              error
	attachCalls      []hcloud.ServerAttachToNetworkOpts
	detachCalls      []hcloud.ServerDetachFromNetworkOpts
	changeAliasCalls []hcloud.ServerChangeAliasIPsOpts
}

func (obj *fakeHetznerServerNetworkClient) GetByName(_ context.Context, _ string) (*hcloud.Server, *hcloud.Response, error) {
	return obj.server, nil, obj.err
}

func (obj *fakeHetznerServerNetworkClient) AttachToNetwork(_ context.Context, _ *hcloud.Server, opts hcloud.ServerAttachToNetworkOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.attachCalls = append(obj.attachCalls, opts)
	return &hcloud.Action{ID: int64(len(obj.attachCalls) + 10), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func (obj *fakeHetznerServerNetworkClient) DetachFromNetwork(_ context.Context, _ *hcloud.Server, opts hcloud.ServerDetachFromNetworkOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.detachCalls = append(obj.detachCalls, opts)
	return &hcloud.Action{ID: int64(len(obj.detachCalls) + 20), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func (obj *fakeHetznerServerNetworkClient) ChangeAliasIPs(_ context.Context, _ *hcloud.Server, opts hcloud.ServerChangeAliasIPsOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.changeAliasCalls = append(obj.changeAliasCalls, opts)
	return &hcloud.Action{ID: int64(len(obj.changeAliasCalls) + 30), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

type fakeHetznerNetworkLookupClient struct {
	network *hcloud.Network
	err     error
}

func (obj *fakeHetznerNetworkLookupClient) GetByName(_ context.Context, _ string) (*hcloud.Network, *hcloud.Response, error) {
	return obj.network, nil, obj.err
}

func testInit() *engine.Init {
	return &engine.Init{
		Program: "test",
		Version: "test",
		Logf:    func(string, ...interface{}) {},
	}
}

func TestHetznerVMNetworkAttach(t *testing.T) {
	serverClient := &fakeHetznerServerNetworkClient{
		server: &hcloud.Server{ID: 1, Name: "api-db"},
	}
	networkClient := &fakeHetznerNetworkLookupClient{
		network: &hcloud.Network{ID: 2, Name: "private"},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerVMNetworkRes{
		APIToken:      "token",
		State:         HetznerStateExists,
		Server:        "api-db",
		Network:       "private",
		IP:            "10.0.0.10",
		AliasIPs:      []string{"10.0.0.11"},
		WaitInterval:  HetznerWaitIntervalDefault,
		WaitTimeout:   HetznerWaitTimeoutDefault,
		serverClient:  serverClient,
		networkClient: networkClient,
		actionWaiter:  waiter,
	}
	if err := res.Init(testInit()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	res.serverClient = serverClient
	res.networkClient = networkClient
	res.actionWaiter = waiter

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected attachment change to report non-converged on apply")
	}
	if len(serverClient.attachCalls) != 1 {
		t.Fatalf("expected exactly one attach call, got %d", len(serverClient.attachCalls))
	}
	if got := serverClient.attachCalls[0].IP.String(); got != "10.0.0.10" {
		t.Fatalf("unexpected attach ip: %s", got)
	}
	if len(serverClient.attachCalls[0].AliasIPs) != 1 || serverClient.attachCalls[0].AliasIPs[0].String() != "10.0.0.11" {
		t.Fatalf("unexpected alias ips: %+v", serverClient.attachCalls[0].AliasIPs)
	}
	if len(waiter.actions) != 1 {
		t.Fatalf("expected waiter to observe one action, got %d", len(waiter.actions))
	}
}

func TestHetznerVMNetworkChangeAliasIPs(t *testing.T) {
	network := &hcloud.Network{ID: 2, Name: "private"}
	serverClient := &fakeHetznerServerNetworkClient{
		server: &hcloud.Server{
			ID:   1,
			Name: "api-db",
			PrivateNet: []hcloud.ServerPrivateNet{
				{
					Network: network,
					IP:      net.ParseIP("10.0.0.10"),
					Aliases: []net.IP{net.ParseIP("10.0.0.99")},
				},
			},
		},
	}
	networkClient := &fakeHetznerNetworkLookupClient{network: network}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerVMNetworkRes{
		APIToken:      "token",
		State:         HetznerStateExists,
		Server:        "api-db",
		Network:       "private",
		IP:            "10.0.0.10",
		AliasIPs:      []string{"10.0.0.11"},
		WaitInterval:  HetznerWaitIntervalDefault,
		WaitTimeout:   HetznerWaitTimeoutDefault,
		serverClient:  serverClient,
		networkClient: networkClient,
		actionWaiter:  waiter,
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected alias update to report non-converged on apply")
	}
	if len(serverClient.changeAliasCalls) != 1 {
		t.Fatalf("expected one alias update call, got %d", len(serverClient.changeAliasCalls))
	}
	if len(waiter.actions) != 1 {
		t.Fatalf("expected waiter to observe one action, got %d", len(waiter.actions))
	}
}

func TestHetznerVMNetworkAbsentWhenServerMissing(t *testing.T) {
	res := &HetznerVMNetworkRes{
		APIToken:      "token",
		State:         HetznerStateAbsent,
		Server:        "api-db",
		Network:       "private",
		WaitInterval:  HetznerWaitIntervalDefault,
		WaitTimeout:   HetznerWaitTimeoutDefault,
		serverClient:  &fakeHetznerServerNetworkClient{},
		networkClient: &fakeHetznerNetworkLookupClient{},
		actionWaiter:  &fakeHetznerActionWaiter{},
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected missing targets to count as absent attachment")
	}
}

func TestHetznerVMNetworkValidateRejectsBadIP(t *testing.T) {
	res := &HetznerVMNetworkRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		Server:       "api-db",
		Network:      "private",
		IP:           "not-an-ip",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	if err := res.Validate(); err == nil {
		t.Fatalf("expected invalid ip validation error")
	}
}

func TestHetznerVMNetworkCmpIgnoresAliasOrder(t *testing.T) {
	left := &HetznerVMNetworkRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		Server:       "api-db",
		Network:      "private",
		AliasIPs:     []string{"10.0.0.11", "10.0.0.12"},
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	right := &HetznerVMNetworkRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		Server:       "api-db",
		Network:      "private",
		AliasIPs:     []string{"10.0.0.12", "10.0.0.11"},
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	if err := left.Cmp(right); err != nil {
		t.Fatalf("Cmp failed: %v", err)
	}
}

func TestHetznerParseIPsRejectsEmptyElement(t *testing.T) {
	if _, err := parseHetznerIPs([]string{""}); err == nil {
		t.Fatalf("expected empty ip element to fail")
	}
}

func ExampleHetznerVMNetworkRes() {
	fmt.Println("hetzner:vm_network")
	// Output: hetzner:vm_network
}
