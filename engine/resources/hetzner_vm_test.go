package resources

import (
	"context"
	"fmt"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type fakeHetznerServerLabelUpdateClient struct {
	updateCalls []hcloud.ServerUpdateOpts
	err         error
}

func (obj *fakeHetznerServerLabelUpdateClient) GetByName(_ context.Context, _ string) (*hcloud.Server, *hcloud.Response, error) {
	return nil, nil, obj.err
}

func (obj *fakeHetznerServerLabelUpdateClient) Update(_ context.Context, server *hcloud.Server, opts hcloud.ServerUpdateOpts) (*hcloud.Server, *hcloud.Response, error) {
	obj.updateCalls = append(obj.updateCalls, opts)
	if server != nil {
		server.Labels = opts.Labels
	}
	return server, nil, obj.err
}

type fakeHetznerServerProtectionUpdateClient struct {
	changeProtectionCalls []hcloud.ServerChangeProtectionOpts
	err                   error
}

func (obj *fakeHetznerServerProtectionUpdateClient) GetByName(_ context.Context, _ string) (*hcloud.Server, *hcloud.Response, error) {
	return nil, nil, obj.err
}

func (obj *fakeHetznerServerProtectionUpdateClient) ChangeProtection(_ context.Context, server *hcloud.Server, opts hcloud.ServerChangeProtectionOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.changeProtectionCalls = append(obj.changeProtectionCalls, opts)
	if server != nil {
		if opts.Delete != nil {
			server.Protection.Delete = *opts.Delete
		}
		if opts.Rebuild != nil {
			server.Protection.Rebuild = *opts.Rebuild
		}
	}
	return &hcloud.Action{ID: 5000 + int64(len(obj.changeProtectionCalls)), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

type fakeHetznerSSHKeyLookupClient struct {
	keys map[string]*hcloud.SSHKey
	err  error
}

func (obj *fakeHetznerSSHKeyLookupClient) GetByName(_ context.Context, name string) (*hcloud.SSHKey, *hcloud.Response, error) {
	if obj.err != nil {
		return nil, nil, obj.err
	}
	return obj.keys[name], nil, nil
}

func (obj *fakeHetznerSSHKeyLookupClient) All(_ context.Context) ([]*hcloud.SSHKey, error) {
	if obj.err != nil {
		return nil, obj.err
	}
	out := make([]*hcloud.SSHKey, 0, len(obj.keys))
	for _, key := range obj.keys {
		out = append(out, key)
	}
	return out, nil
}

func TestHetznerVMCheckApplyServerLabels(t *testing.T) {
	serverLabelClient := &fakeHetznerServerLabelUpdateClient{}
	res := &HetznerVMRes{
		State:             HetznerStateExists,
		Labels:            map[string]string{"role": "api-db", "cluster": "beta"},
		server:            &hcloud.Server{Name: "beta-nbg1-api-db", Labels: map[string]string{"role": "old"}},
		serverLabelClient: serverLabelClient,
		init:              testInit(),
	}

	checkOK, err := res.checkApplyServerLabels(context.Background(), true)
	if err != nil {
		t.Fatalf("checkApplyServerLabels failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected label update to report non-converged on apply")
	}
	if len(serverLabelClient.updateCalls) != 1 {
		t.Fatalf("expected one update call, got %d", len(serverLabelClient.updateCalls))
	}
	if got := res.server.Labels["role"]; got != "api-db" {
		t.Fatalf("unexpected role label: %s", got)
	}
	if got := res.server.Labels["cluster"]; got != "beta" {
		t.Fatalf("unexpected cluster label: %s", got)
	}
}

func TestHetznerVMCheckApplyServerProtection(t *testing.T) {
	serverProtectionClient := &fakeHetznerServerProtectionUpdateClient{}
	waiter := &fakeHetznerActionWaiter{}
	res := &HetznerVMRes{
		State:                  HetznerStateExists,
		DeleteProtection:       true,
		RebuildProtection:      true,
		server:                 &hcloud.Server{Name: "beta-nbg1-api-db", Protection: hcloud.ServerProtection{Delete: false, Rebuild: false}},
		serverProtectionClient: serverProtectionClient,
		actionWaiter:           waiter,
		init:                   testInit(),
		WaitTimeout:            HetznerWaitTimeoutDefault,
	}

	checkOK, err := res.checkApplyServerProtection(context.Background(), true)
	if err != nil {
		t.Fatalf("checkApplyServerProtection failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected protection update to report non-converged on apply")
	}
	if len(serverProtectionClient.changeProtectionCalls) != 1 {
		t.Fatalf("expected one protection call, got %d", len(serverProtectionClient.changeProtectionCalls))
	}
	if len(waiter.actions) != 1 {
		t.Fatalf("expected waiter to observe one action, got %d", len(waiter.actions))
	}
	if !res.server.Protection.Delete || !res.server.Protection.Rebuild {
		t.Fatalf("expected both protections enabled, got %+v", res.server.Protection)
	}
}

func TestHetznerVMCmpIgnoresLabelOrder(t *testing.T) {
	left := &HetznerVMRes{
		APIToken:          "token",
		State:             HetznerStateExists,
		AllowRebuild:      HetznerAllowRebuildError,
		ServerType:        "cx23",
		Datacenter:        "nbg1-dc3",
		Image:             "debian-13",
		SSHKeys:           []string{"beta-admin", "beta-breakglass"},
		Labels:            map[string]string{"cluster": "beta", "role": "cdn"},
		DeleteProtection:  true,
		RebuildProtection: true,
		ServerRescueSSHKeys: []string{
			"beta-breakglass",
			"beta-admin",
		},
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	right := &HetznerVMRes{
		APIToken:          "token",
		State:             HetznerStateExists,
		AllowRebuild:      HetznerAllowRebuildError,
		ServerType:        "cx23",
		Datacenter:        "nbg1-dc3",
		Image:             "debian-13",
		SSHKeys:           []string{"beta-breakglass", "beta-admin"},
		Labels:            map[string]string{"role": "cdn", "cluster": "beta"},
		DeleteProtection:  true,
		RebuildProtection: true,
		ServerRescueSSHKeys: []string{
			"beta-admin",
			"beta-breakglass",
		},
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	if err := left.Cmp(right); err != nil {
		t.Fatalf("Cmp failed: %v", err)
	}
}

func TestHetznerVMGetServerCreateKeysUsesNamedSubset(t *testing.T) {
	sshKeyClient := &fakeHetznerSSHKeyLookupClient{
		keys: map[string]*hcloud.SSHKey{
			"beta-admin":      {ID: 1, Name: "beta-admin"},
			"beta-breakglass": {ID: 2, Name: "beta-breakglass"},
			"ignored":         {ID: 3, Name: "ignored"},
		},
	}
	res := &HetznerVMRes{
		SSHKeys:      []string{"beta-admin", "beta-breakglass"},
		init:         testInit(),
		sshKeyClient: sshKeyClient,
	}

	keys, err := res.getServerCreateKeys(context.Background())
	if err != nil {
		t.Fatalf("getServerCreateKeys failed: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected two keys, got %d", len(keys))
	}
	if keys[0].Name != "beta-admin" || keys[1].Name != "beta-breakglass" {
		t.Fatalf("unexpected key order: %+v", keys)
	}
}

func ExampleHetznerVMRes_labelsAndProtection() {
	fmt.Println("hetzner:vm")
	// Output: hetzner:vm
}
