package resources

import (
	"context"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type fakeHetznerServerLookupClient struct {
	server *hcloud.Server
	err    error
}

func (obj *fakeHetznerServerLookupClient) GetByName(_ context.Context, _ string) (*hcloud.Server, *hcloud.Response, error) {
	return obj.server, nil, obj.err
}

type fakeHetznerVolumeClient struct {
	volume      *hcloud.Volume
	err         error
	attachCalls int
	detachCalls int
}

func (obj *fakeHetznerVolumeClient) GetByName(_ context.Context, _ string) (*hcloud.Volume, *hcloud.Response, error) {
	return obj.volume, nil, obj.err
}

func (obj *fakeHetznerVolumeClient) Attach(_ context.Context, volume *hcloud.Volume, server *hcloud.Server) (*hcloud.Action, *hcloud.Response, error) {
	obj.attachCalls++
	obj.volume = &hcloud.Volume{
		ID:     volume.ID,
		Name:   volume.Name,
		Server: server,
	}
	return &hcloud.Action{ID: 100, Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func (obj *fakeHetznerVolumeClient) Detach(_ context.Context, volume *hcloud.Volume) (*hcloud.Action, *hcloud.Response, error) {
	obj.detachCalls++
	obj.volume = &hcloud.Volume{
		ID:   volume.ID,
		Name: volume.Name,
	}
	return &hcloud.Action{ID: 200, Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func TestHetznerVMVolumeAttach(t *testing.T) {
	serverClient := &fakeHetznerServerLookupClient{
		server: &hcloud.Server{ID: 1, Name: "api-db"},
	}
	volumeClient := &fakeHetznerVolumeClient{
		volume: &hcloud.Volume{ID: 2, Name: "dolt-data"},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerVMVolumeRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		Server:       "api-db",
		Volume:       "dolt-data",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: serverClient,
		volumeClient: volumeClient,
		actionWaiter: waiter,
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected attach to report non-converged on apply")
	}
	if volumeClient.attachCalls != 1 {
		t.Fatalf("expected one attach call, got %d", volumeClient.attachCalls)
	}
	if len(waiter.actions) != 1 {
		t.Fatalf("expected waiter to observe one action, got %d", len(waiter.actions))
	}
}

func TestHetznerVMVolumeReattachFromDifferentServer(t *testing.T) {
	serverClient := &fakeHetznerServerLookupClient{
		server: &hcloud.Server{ID: 1, Name: "api-db"},
	}
	volumeClient := &fakeHetznerVolumeClient{
		volume: &hcloud.Volume{
			ID:   2,
			Name: "dolt-data",
			Server: &hcloud.Server{
				ID:   99,
				Name: "old-node",
			},
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerVMVolumeRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		Server:       "api-db",
		Volume:       "dolt-data",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: serverClient,
		volumeClient: volumeClient,
		actionWaiter: waiter,
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected reattach to report non-converged on apply")
	}
	if volumeClient.detachCalls != 1 {
		t.Fatalf("expected one detach call, got %d", volumeClient.detachCalls)
	}
	if volumeClient.attachCalls != 1 {
		t.Fatalf("expected one attach call, got %d", volumeClient.attachCalls)
	}
	if len(waiter.actions) != 2 {
		t.Fatalf("expected waiter to observe two actions, got %d", len(waiter.actions))
	}
}

func TestHetznerVMVolumeAbsentWhenVolumeMissing(t *testing.T) {
	res := &HetznerVMVolumeRes{
		APIToken:     "token",
		State:        HetznerStateAbsent,
		Server:       "api-db",
		Volume:       "dolt-data",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: &fakeHetznerServerLookupClient{},
		volumeClient: &fakeHetznerVolumeClient{},
		actionWaiter: &fakeHetznerActionWaiter{},
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected missing targets to count as absent attachment")
	}
}

func TestHetznerVMVolumeCmp(t *testing.T) {
	left := &HetznerVMVolumeRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		Server:       "api-db",
		Volume:       "dolt-data",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	right := &HetznerVMVolumeRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		Server:       "api-db",
		Volume:       "dolt-data",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	if err := left.Cmp(right); err != nil {
		t.Fatalf("Cmp failed: %v", err)
	}
}
