package resources

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/purpleidea/mgmt/engine"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type fakeHetznerFirewallLifecycleClient struct {
	firewall      *hcloud.Firewall
	err           error
	createCalls   []hcloud.FirewallCreateOpts
	updateCalls   []hcloud.FirewallUpdateOpts
	setRulesCalls []hcloud.FirewallSetRulesOpts
	applyCalls    [][]hcloud.FirewallResource
	removeCalls   [][]hcloud.FirewallResource
	deleteCalls   int
}

func (obj *fakeHetznerFirewallLifecycleClient) GetByName(_ context.Context, _ string) (*hcloud.Firewall, *hcloud.Response, error) {
	return obj.firewall, nil, obj.err
}

func (obj *fakeHetznerFirewallLifecycleClient) Create(_ context.Context, opts hcloud.FirewallCreateOpts) (hcloud.FirewallCreateResult, *hcloud.Response, error) {
	obj.createCalls = append(obj.createCalls, opts)
	if obj.err != nil {
		return hcloud.FirewallCreateResult{}, nil, obj.err
	}
	obj.firewall = &hcloud.Firewall{
		ID:        1,
		Name:      opts.Name,
		Labels:    opts.Labels,
		Rules:     slices.Clone(opts.Rules),
		AppliedTo: slices.Clone(opts.ApplyTo),
	}
	return hcloud.FirewallCreateResult{
		Firewall: obj.firewall,
		Actions:  []*hcloud.Action{{ID: 100, Status: hcloud.ActionStatusSuccess}},
	}, nil, nil
}

func (obj *fakeHetznerFirewallLifecycleClient) Update(_ context.Context, firewall *hcloud.Firewall, opts hcloud.FirewallUpdateOpts) (*hcloud.Firewall, *hcloud.Response, error) {
	obj.updateCalls = append(obj.updateCalls, opts)
	if obj.err != nil {
		return nil, nil, obj.err
	}
	firewall.Labels = opts.Labels
	obj.firewall = firewall
	return firewall, nil, nil
}

func (obj *fakeHetznerFirewallLifecycleClient) Delete(_ context.Context, _ *hcloud.Firewall) (*hcloud.Response, error) {
	obj.deleteCalls++
	if obj.firewall != nil && len(obj.firewall.AppliedTo) > 0 {
		return nil, fmt.Errorf("firewall still in use")
	}
	obj.firewall = nil
	return nil, obj.err
}

func (obj *fakeHetznerFirewallLifecycleClient) SetRules(_ context.Context, firewall *hcloud.Firewall, opts hcloud.FirewallSetRulesOpts) ([]*hcloud.Action, *hcloud.Response, error) {
	obj.setRulesCalls = append(obj.setRulesCalls, opts)
	if obj.err != nil {
		return nil, nil, obj.err
	}
	firewall.Rules = slices.Clone(opts.Rules)
	obj.firewall = firewall
	return []*hcloud.Action{{ID: 200, Status: hcloud.ActionStatusSuccess}}, nil, nil
}

func (obj *fakeHetznerFirewallLifecycleClient) ApplyResources(_ context.Context, firewall *hcloud.Firewall, resources []hcloud.FirewallResource) ([]*hcloud.Action, *hcloud.Response, error) {
	obj.applyCalls = append(obj.applyCalls, slices.Clone(resources))
	if obj.err != nil {
		return nil, nil, obj.err
	}
	firewall.AppliedTo = append(firewall.AppliedTo, resources...)
	obj.firewall = firewall
	return []*hcloud.Action{{ID: 300, Status: hcloud.ActionStatusSuccess}}, nil, nil
}

func (obj *fakeHetznerFirewallLifecycleClient) RemoveResources(_ context.Context, firewall *hcloud.Firewall, resources []hcloud.FirewallResource) ([]*hcloud.Action, *hcloud.Response, error) {
	obj.removeCalls = append(obj.removeCalls, slices.Clone(resources))
	if obj.err != nil {
		return nil, nil, obj.err
	}
	removeMap := hetznerFirewallApplyToMap(resources)
	kept := make([]hcloud.FirewallResource, 0, len(firewall.AppliedTo))
	for _, resource := range firewall.AppliedTo {
		if _, exists := removeMap[hetznerFirewallApplyToKey(resource)]; exists {
			continue
		}
		kept = append(kept, resource)
	}
	firewall.AppliedTo = kept
	obj.firewall = firewall
	return []*hcloud.Action{{ID: 400, Status: hcloud.ActionStatusSuccess}}, nil, nil
}

type fakeHetznerNamedServerLookupClient struct {
	servers map[string]*hcloud.Server
	err     error
}

func (obj *fakeHetznerNamedServerLookupClient) GetByName(_ context.Context, name string) (*hcloud.Server, *hcloud.Response, error) {
	if obj.err != nil {
		return nil, nil, obj.err
	}
	return obj.servers[name], nil, nil
}

func TestHetznerFirewallCreate(t *testing.T) {
	firewallClient := &fakeHetznerFirewallLifecycleClient{}
	serverClient := &fakeHetznerNamedServerLookupClient{
		servers: map[string]*hcloud.Server{
			"beta-api": {ID: 5, Name: "beta-api"},
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerFirewallRes{
		APIToken: "token",
		Rules: []HetznerFirewallRuleSpec{
			{
				Direction: "in",
				SourceIPs: []string{"0.0.0.0/0"},
				Protocol:  "tcp",
				Port:      "22",
			},
		},
		ApplyTo: []HetznerFirewallApplyToSpec{
			{Type: "server", Server: "beta-api"},
			{Type: "label_selector", LabelSelector: "cluster=beta"},
		},
		Labels:         map[string]string{"cluster": "beta"},
		WaitInterval:   HetznerWaitIntervalDefault,
		WaitTimeout:    HetznerWaitTimeoutDefault,
		firewallClient: firewallClient,
		serverClient:   serverClient,
		actionWaiter:   waiter,
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected create to report non-converged on apply")
	}
	if len(firewallClient.createCalls) != 1 {
		t.Fatalf("expected one create call, got %d", len(firewallClient.createCalls))
	}
	call := firewallClient.createCalls[0]
	if call.Name != res.Name() {
		t.Fatalf("unexpected create name: %s", call.Name)
	}
	if len(call.ApplyTo) != 2 {
		t.Fatalf("expected two apply_to resources, got %d", len(call.ApplyTo))
	}
	if len(waiter.actions) != 1 {
		t.Fatalf("expected waiter to observe one action, got %d", len(waiter.actions))
	}
}

func TestHetznerFirewallSetRules(t *testing.T) {
	firewallClient := &fakeHetznerFirewallLifecycleClient{
		firewall: &hcloud.Firewall{
			ID:     1,
			Name:   "beta-public",
			Labels: map[string]string{"cluster": "beta"},
			Rules: []hcloud.FirewallRule{
				{
					Direction: hcloud.FirewallRuleDirectionIn,
					Protocol:  hcloud.FirewallRuleProtocolTCP,
					Port:      ptrString("80"),
				},
			},
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerFirewallRes{
		APIToken: "token",
		Rules: []HetznerFirewallRuleSpec{
			{
				Direction: "in",
				SourceIPs: []string{"0.0.0.0/0"},
				Protocol:  "tcp",
				Port:      "443",
			},
		},
		Labels:         map[string]string{"cluster": "beta"},
		WaitInterval:   HetznerWaitIntervalDefault,
		WaitTimeout:    HetznerWaitTimeoutDefault,
		firewallClient: firewallClient,
		serverClient:   &fakeHetznerNamedServerLookupClient{},
		actionWaiter:   waiter,
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected set rules to report non-converged on apply")
	}
	if len(firewallClient.setRulesCalls) != 1 {
		t.Fatalf("expected one set rules call, got %d", len(firewallClient.setRulesCalls))
	}
	if len(waiter.actions) != 1 {
		t.Fatalf("expected waiter to observe one action, got %d", len(waiter.actions))
	}
}

func TestHetznerFirewallReconcileApplyTo(t *testing.T) {
	firewallClient := &fakeHetznerFirewallLifecycleClient{
		firewall: &hcloud.Firewall{
			ID:   1,
			Name: "beta-public",
			AppliedTo: []hcloud.FirewallResource{
				{
					Type:   hcloud.FirewallResourceTypeServer,
					Server: &hcloud.FirewallResourceServer{ID: 1},
				},
			},
		},
	}
	serverClient := &fakeHetznerNamedServerLookupClient{
		servers: map[string]*hcloud.Server{
			"beta-cdn": {ID: 2, Name: "beta-cdn"},
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerFirewallRes{
		APIToken: "token",
		ApplyTo: []HetznerFirewallApplyToSpec{
			{Type: "server", Server: "beta-cdn"},
			{Type: "label_selector", LabelSelector: "role=cdn"},
		},
		WaitInterval:   HetznerWaitIntervalDefault,
		WaitTimeout:    HetznerWaitTimeoutDefault,
		firewallClient: firewallClient,
		serverClient:   serverClient,
		actionWaiter:   waiter,
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected apply_to reconciliation to report non-converged on apply")
	}
	if len(firewallClient.removeCalls) != 1 {
		t.Fatalf("expected one remove call, got %d", len(firewallClient.removeCalls))
	}
	if len(firewallClient.applyCalls) != 1 {
		t.Fatalf("expected one apply call, got %d", len(firewallClient.applyCalls))
	}
	if len(waiter.actions) != 2 {
		t.Fatalf("expected waiter to observe two actions, got %d", len(waiter.actions))
	}
}

func TestHetznerFirewallReverseDeleteDetachesBeforeDelete(t *testing.T) {
	firewallClient := &fakeHetznerFirewallLifecycleClient{
		firewall: &hcloud.Firewall{
			ID:   1,
			Name: "beta-http01",
			AppliedTo: []hcloud.FirewallResource{
				{
					Type:   hcloud.FirewallResourceTypeServer,
					Server: &hcloud.FirewallResourceServer{ID: 7},
				},
				{
					Type:          hcloud.FirewallResourceTypeLabelSelector,
					LabelSelector: &hcloud.FirewallResourceLabelSelector{Selector: "hostname=beta-api"},
				},
			},
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerFirewallRes{
		APIToken:       "token",
		WaitInterval:   HetznerWaitIntervalDefault,
		WaitTimeout:    HetznerWaitTimeoutDefault,
		firewallClient: firewallClient,
		serverClient:   &fakeHetznerNamedServerLookupClient{},
		actionWaiter:   waiter,
	}
	res.SetReversibleMeta(&engine.ReversibleMeta{Reversal: true})

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected reverse delete to report non-converged on apply")
	}
	if len(firewallClient.removeCalls) != 1 {
		t.Fatalf("expected one remove call, got %d", len(firewallClient.removeCalls))
	}
	if firewallClient.deleteCalls != 1 {
		t.Fatalf("expected one delete call, got %d", firewallClient.deleteCalls)
	}
	if len(waiter.actions) != 1 {
		t.Fatalf("expected waiter to observe one remove action, got %d", len(waiter.actions))
	}
}

func TestHetznerFirewallReversibleMetaDefaultsOverwrite(t *testing.T) {
	res := &HetznerFirewallRes{}

	meta := res.ReversibleMeta()
	if !meta.Overwrite {
		t.Fatalf("expected reverse overwrite to default to true")
	}

	meta.Disabled = false
	res.SetReversibleMeta(meta)

	if !res.ReversibleMeta().Overwrite {
		t.Fatalf("expected reverse overwrite to remain true after meta update")
	}
}

func TestHetznerFirewallCmpIgnoresApplyToOrder(t *testing.T) {
	left := &HetznerFirewallRes{
		APIToken: "token",
		Rules: []HetznerFirewallRuleSpec{
			{Direction: "in", SourceIPs: []string{"0.0.0.0/0"}, Protocol: "tcp", Port: "22"},
		},
		ApplyTo: []HetznerFirewallApplyToSpec{
			{Type: "server", Server: "beta-api"},
			{Type: "label_selector", LabelSelector: "cluster=beta"},
		},
		Labels:       map[string]string{"cluster": "beta", "role": "edge"},
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	right := &HetznerFirewallRes{
		APIToken: "token",
		Rules: []HetznerFirewallRuleSpec{
			{Direction: "in", SourceIPs: []string{"0.0.0.0/0"}, Protocol: "tcp", Port: "22"},
		},
		ApplyTo: []HetznerFirewallApplyToSpec{
			{Type: "label_selector", LabelSelector: "cluster=beta"},
			{Type: "server", Server: "beta-api"},
		},
		Labels:       map[string]string{"role": "edge", "cluster": "beta"},
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	if err := left.Cmp(right); err != nil {
		t.Fatalf("Cmp failed: %v", err)
	}
}

func ExampleHetznerFirewallRes() {
	fmt.Println("hetzner:firewall")
	// Output: hetzner:firewall
}

func ptrString(value string) *string {
	return &value
}
