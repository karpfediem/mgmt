package resources

import (
	"context"
	"fmt"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

const testHetznerPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBL0g0H0L+Y4P0C5A4f6+8yV2g3r1iN3H0tS5f6mQ3dL test@example"
const otherHetznerPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIM8tK7dYy9M7lXWQ0/WfJg8KXjF1sE0wS3rZ2K5nV4p8 other@example"

type fakeHetznerSSHKeyLifecycleClient struct {
	sshKey       *hcloud.SSHKey
	err          error
	createCalls  []hcloud.SSHKeyCreateOpts
	updateCalls  []hcloud.SSHKeyUpdateOpts
	deleteCalled bool
}

func (obj *fakeHetznerSSHKeyLifecycleClient) GetByName(_ context.Context, _ string) (*hcloud.SSHKey, *hcloud.Response, error) {
	return obj.sshKey, nil, obj.err
}

func (obj *fakeHetznerSSHKeyLifecycleClient) All(_ context.Context) ([]*hcloud.SSHKey, error) {
	if obj.sshKey == nil {
		return nil, obj.err
	}
	return []*hcloud.SSHKey{obj.sshKey}, obj.err
}

func (obj *fakeHetznerSSHKeyLifecycleClient) Create(_ context.Context, opts hcloud.SSHKeyCreateOpts) (*hcloud.SSHKey, *hcloud.Response, error) {
	obj.createCalls = append(obj.createCalls, opts)
	if obj.err != nil {
		return nil, nil, obj.err
	}
	_, fingerprint, err := normalizeHetznerPublicKey(opts.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	obj.sshKey = &hcloud.SSHKey{
		ID:          1,
		Name:        opts.Name,
		PublicKey:   opts.PublicKey,
		Fingerprint: fingerprint,
		Labels:      opts.Labels,
	}
	return obj.sshKey, nil, nil
}

func (obj *fakeHetznerSSHKeyLifecycleClient) Update(_ context.Context, sshKey *hcloud.SSHKey, opts hcloud.SSHKeyUpdateOpts) (*hcloud.SSHKey, *hcloud.Response, error) {
	obj.updateCalls = append(obj.updateCalls, opts)
	if obj.err != nil {
		return nil, nil, obj.err
	}
	if sshKey != nil {
		sshKey.Name = opts.Name
		sshKey.Labels = opts.Labels
	}
	obj.sshKey = sshKey
	return sshKey, nil, nil
}

func (obj *fakeHetznerSSHKeyLifecycleClient) Delete(_ context.Context, _ *hcloud.SSHKey) (*hcloud.Response, error) {
	obj.deleteCalled = true
	obj.sshKey = nil
	return nil, obj.err
}

func TestHetznerSSHKeyCreate(t *testing.T) {
	client := &fakeHetznerSSHKeyLifecycleClient{}
	res := &HetznerSSHKeyRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		PublicKey:    testHetznerPublicKey,
		Labels:       map[string]string{"cluster": "beta"},
		WaitInterval: HetznerWaitIntervalDefault,
		sshKeyClient: client,
	}

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
	if got := client.createCalls[0].Name; got != res.Name() {
		t.Fatalf("unexpected create name: %s", got)
	}
}

func TestHetznerSSHKeyUpdateLabels(t *testing.T) {
	normalized, fingerprint, err := normalizeHetznerPublicKey(testHetznerPublicKey)
	if err != nil {
		t.Fatalf("normalizeHetznerPublicKey failed: %v", err)
	}
	client := &fakeHetznerSSHKeyLifecycleClient{
		sshKey: &hcloud.SSHKey{
			ID:          1,
			Name:        "beta-admin",
			PublicKey:   normalized,
			Fingerprint: fingerprint,
			Labels:      map[string]string{"cluster": "old"},
		},
	}
	res := &HetznerSSHKeyRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		PublicKey:    testHetznerPublicKey,
		Labels:       map[string]string{"cluster": "beta"},
		WaitInterval: HetznerWaitIntervalDefault,
		sshKeyClient: client,
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected update to report non-converged on apply")
	}
	if len(client.updateCalls) != 1 {
		t.Fatalf("expected one update call, got %d", len(client.updateCalls))
	}
	if got := client.sshKey.Labels["cluster"]; got != "beta" {
		t.Fatalf("unexpected updated label: %s", got)
	}
}

func TestHetznerSSHKeyRejectsPublicKeyRotation(t *testing.T) {
	normalized, fingerprint, err := normalizeHetznerPublicKey(testHetznerPublicKey)
	if err != nil {
		t.Fatalf("normalizeHetznerPublicKey failed: %v", err)
	}
	client := &fakeHetznerSSHKeyLifecycleClient{
		sshKey: &hcloud.SSHKey{
			ID:          1,
			Name:        "beta-admin",
			PublicKey:   normalized,
			Fingerprint: fingerprint,
		},
	}
	res := &HetznerSSHKeyRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		PublicKey:    otherHetznerPublicKey,
		WaitInterval: HetznerWaitIntervalDefault,
		sshKeyClient: client,
	}

	_, err = res.CheckApply(context.Background(), true)
	if err == nil {
		t.Fatalf("expected immutable public key error")
	}
	if got := err.Error(); got != "publickey differs and is not mutable" {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestHetznerSSHKeyCmpIgnoresLabelOrder(t *testing.T) {
	left := &HetznerSSHKeyRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		PublicKey:    testHetznerPublicKey,
		Labels:       map[string]string{"cluster": "beta", "role": "deploy"},
		WaitInterval: HetznerWaitIntervalDefault,
	}
	right := &HetznerSSHKeyRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		PublicKey:    testHetznerPublicKey,
		Labels:       map[string]string{"role": "deploy", "cluster": "beta"},
		WaitInterval: HetznerWaitIntervalDefault,
	}
	if err := left.Cmp(right); err != nil {
		t.Fatalf("Cmp failed: %v", err)
	}
}

func ExampleHetznerSSHKeyRes() {
	fmt.Println("hetzner:ssh_key")
	// Output: hetzner:ssh_key
}
