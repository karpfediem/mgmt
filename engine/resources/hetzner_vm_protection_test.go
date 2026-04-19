package resources

import (
	"context"
	"fmt"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type fakeHetznerServerProtectionClient struct {
	server                *hcloud.Server
	err                   error
	changeProtectionCalls []hcloud.ServerChangeProtectionOpts
}

func (obj *fakeHetznerServerProtectionClient) GetByName(_ context.Context, _ string) (*hcloud.Server, *hcloud.Response, error) {
	return obj.server, nil, obj.err
}

func (obj *fakeHetznerServerProtectionClient) ChangeProtection(_ context.Context, _ *hcloud.Server, opts hcloud.ServerChangeProtectionOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.changeProtectionCalls = append(obj.changeProtectionCalls, opts)
	if obj.server != nil {
		if opts.Delete != nil {
			obj.server.Protection.Delete = *opts.Delete
		}
		if opts.Rebuild != nil {
			obj.server.Protection.Rebuild = *opts.Rebuild
		}
	}
	return &hcloud.Action{ID: 4000 + int64(len(obj.changeProtectionCalls)), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func TestHetznerVMProtectionReconcile(t *testing.T) {
	serverClient := &fakeHetznerServerProtectionClient{
		server: &hcloud.Server{
			ID:   1,
			Name: "beta-nbg1-api-db",
			Protection: hcloud.ServerProtection{
				Delete:  false,
				Rebuild: false,
			},
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerVMProtectionRes{
		APIToken:          "token",
		State:             HetznerStateExists,
		Server:            "beta-nbg1-api-db",
		DeleteProtection:  true,
		RebuildProtection: true,
		WaitInterval:      HetznerWaitIntervalDefault,
		WaitTimeout:       HetznerWaitTimeoutDefault,
		serverClient:      serverClient,
		actionWaiter:      waiter,
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected protection reconcile to report non-converged on apply")
	}
	if len(serverClient.changeProtectionCalls) != 1 {
		t.Fatalf("expected one protection call, got %d", len(serverClient.changeProtectionCalls))
	}
	if len(waiter.actions) != 1 {
		t.Fatalf("expected waiter to observe one action, got %d", len(waiter.actions))
	}
	if !serverClient.server.Protection.Delete || !serverClient.server.Protection.Rebuild {
		t.Fatalf("expected both protections to be enabled, got %+v", serverClient.server.Protection)
	}
}

func TestHetznerVMProtectionAbsentDisablesProtection(t *testing.T) {
	serverClient := &fakeHetznerServerProtectionClient{
		server: &hcloud.Server{
			ID:   1,
			Name: "beta-nbg1-api-db",
			Protection: hcloud.ServerProtection{
				Delete:  true,
				Rebuild: true,
			},
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerVMProtectionRes{
		APIToken:     "token",
		State:        HetznerStateAbsent,
		Server:       "beta-nbg1-api-db",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: serverClient,
		actionWaiter: waiter,
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected absent protection reconcile to report non-converged on apply")
	}
	if len(serverClient.changeProtectionCalls) != 1 {
		t.Fatalf("expected one protection call, got %d", len(serverClient.changeProtectionCalls))
	}
	if len(waiter.actions) != 1 {
		t.Fatalf("expected waiter to observe one action, got %d", len(waiter.actions))
	}
	if serverClient.server.Protection.Delete || serverClient.server.Protection.Rebuild {
		t.Fatalf("expected both protections to be disabled, got %+v", serverClient.server.Protection)
	}
}

func TestHetznerVMProtectionAbsentWhenServerMissing(t *testing.T) {
	res := &HetznerVMProtectionRes{
		APIToken:     "token",
		State:        HetznerStateAbsent,
		Server:       "beta-nbg1-api-db",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: &fakeHetznerServerProtectionClient{},
		actionWaiter: &fakeHetznerActionWaiter{},
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected missing server to count as absent")
	}
}

func TestHetznerVMProtectionValidateRejectsMissingServer(t *testing.T) {
	res := &HetznerVMProtectionRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	if err := res.Validate(); err == nil {
		t.Fatalf("expected missing server validation error")
	}
}

func ExampleHetznerVMProtectionRes() {
	fmt.Println("hetzner:vm_protection")
	// Output: hetzner:vm_protection
}
