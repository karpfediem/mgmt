package resources

import (
	"context"
	"fmt"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type fakeHetznerServerLabelClient struct {
	server      *hcloud.Server
	err         error
	updateCalls []hcloud.ServerUpdateOpts
}

func (obj *fakeHetznerServerLabelClient) GetByName(_ context.Context, _ string) (*hcloud.Server, *hcloud.Response, error) {
	return obj.server, nil, obj.err
}

func (obj *fakeHetznerServerLabelClient) Update(_ context.Context, _ *hcloud.Server, opts hcloud.ServerUpdateOpts) (*hcloud.Server, *hcloud.Response, error) {
	obj.updateCalls = append(obj.updateCalls, opts)
	if obj.server != nil {
		obj.server.Labels = opts.Labels
	}
	return obj.server, nil, obj.err
}

func TestHetznerVMLabelsReconcile(t *testing.T) {
	serverClient := &fakeHetznerServerLabelClient{
		server: &hcloud.Server{
			ID:     1,
			Name:   "beta-nbg1-api-db",
			Labels: map[string]string{"role": "old"},
		},
	}

	res := &HetznerVMLabelsRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		Server:       "beta-nbg1-api-db",
		Labels:       map[string]string{"role": "api-db", "cluster": "beta"},
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: serverClient,
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected label reconcile to report non-converged on apply")
	}
	if len(serverClient.updateCalls) != 1 {
		t.Fatalf("expected one update call, got %d", len(serverClient.updateCalls))
	}
	if got := serverClient.server.Labels["role"]; got != "api-db" {
		t.Fatalf("unexpected role label: %s", got)
	}
	if got := serverClient.server.Labels["cluster"]; got != "beta" {
		t.Fatalf("unexpected cluster label: %s", got)
	}
}

func TestHetznerVMLabelsAbsentClearsLabels(t *testing.T) {
	serverClient := &fakeHetznerServerLabelClient{
		server: &hcloud.Server{
			ID:     1,
			Name:   "beta-nbg1-api-db",
			Labels: map[string]string{"role": "api-db"},
		},
	}

	res := &HetznerVMLabelsRes{
		APIToken:     "token",
		State:        HetznerStateAbsent,
		Server:       "beta-nbg1-api-db",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: serverClient,
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected absent reconcile to report non-converged on apply")
	}
	if len(serverClient.updateCalls) != 1 {
		t.Fatalf("expected one update call, got %d", len(serverClient.updateCalls))
	}
	if len(serverClient.server.Labels) != 0 {
		t.Fatalf("expected labels to be cleared, got %+v", serverClient.server.Labels)
	}
}

func TestHetznerVMLabelsAbsentWhenServerMissing(t *testing.T) {
	res := &HetznerVMLabelsRes{
		APIToken:     "token",
		State:        HetznerStateAbsent,
		Server:       "beta-nbg1-api-db",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: &fakeHetznerServerLabelClient{},
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected missing server to count as absent")
	}
}

func TestHetznerVMLabelsValidateRejectsMissingServer(t *testing.T) {
	res := &HetznerVMLabelsRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	if err := res.Validate(); err == nil {
		t.Fatalf("expected missing server validation error")
	}
}

func ExampleHetznerVMLabelsRes() {
	fmt.Println("hetzner:vm_labels")
	// Output: hetzner:vm_labels
}
