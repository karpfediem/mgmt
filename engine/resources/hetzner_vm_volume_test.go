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

func TestHetznerVMVolumeAbsentWhenVolumeUnattached(t *testing.T) {
	serverClient := &fakeHetznerServerLookupClient{
		server: &hcloud.Server{ID: 1, Name: "api-db"},
	}
	volumeClient := &fakeHetznerVolumeClient{
		volume: &hcloud.Volume{ID: 2, Name: "dolt-data"},
	}

	res := &HetznerVMVolumeRes{
		APIToken:     "token",
		State:        HetznerStateAbsent,
		Server:       "api-db",
		Volume:       "dolt-data",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: serverClient,
		volumeClient: volumeClient,
		actionWaiter: &fakeHetznerActionWaiter{},
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected unattached volume to be converged for absent state")
	}
	if volumeClient.detachCalls != 0 {
		t.Fatalf("expected no detach calls, got %d", volumeClient.detachCalls)
	}
}

func TestHetznerVMVolumeAbsentWhenAttachedToConfiguredServer(t *testing.T) {
	server := &hcloud.Server{ID: 1, Name: "api-db"}
	serverClient := &fakeHetznerServerLookupClient{server: server}
	volumeClient := &fakeHetznerVolumeClient{
		volume: &hcloud.Volume{
			ID:     2,
			Name:   "dolt-data",
			Server: server,
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerVMVolumeRes{
		APIToken:     "token",
		State:        HetznerStateAbsent,
		Server:       "api-db",
		Volume:       "dolt-data",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: serverClient,
		volumeClient: volumeClient,
		actionWaiter: waiter,
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("CheckApply(false) failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected attached target volume to report non-converged before detach")
	}
	if volumeClient.detachCalls != 0 {
		t.Fatalf("expected no detach calls during dry run, got %d", volumeClient.detachCalls)
	}

	checkOK, err = res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply(true) failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected detach apply to report non-converged")
	}
	if volumeClient.detachCalls != 1 {
		t.Fatalf("expected one detach call, got %d", volumeClient.detachCalls)
	}
	if len(waiter.actions) != 1 {
		t.Fatalf("expected waiter to observe one action, got %d", len(waiter.actions))
	}
}

func TestHetznerVMVolumeAbsentWhenAttachedToDifferentServer(t *testing.T) {
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

	res := &HetznerVMVolumeRes{
		APIToken:     "token",
		State:        HetznerStateAbsent,
		Server:       "api-db",
		Volume:       "dolt-data",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: serverClient,
		volumeClient: volumeClient,
		actionWaiter: &fakeHetznerActionWaiter{},
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("CheckApply(false) failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected attachment to a different server to be converged for absent state")
	}

	checkOK, err = res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply(true) failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected apply to remain converged when volume is attached elsewhere")
	}
	if volumeClient.detachCalls != 0 {
		t.Fatalf("expected no detach calls, got %d", volumeClient.detachCalls)
	}
}

func TestHetznerVMVolumePresentWhenAttachedToConfiguredServer(t *testing.T) {
	server := &hcloud.Server{ID: 1, Name: "api-db"}
	serverClient := &fakeHetznerServerLookupClient{server: server}
	volumeClient := &fakeHetznerVolumeClient{
		volume: &hcloud.Volume{
			ID:     2,
			Name:   "dolt-data",
			Server: server,
		},
	}

	res := &HetznerVMVolumeRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		Server:       "api-db",
		Volume:       "dolt-data",
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		serverClient: serverClient,
		volumeClient: volumeClient,
		actionWaiter: &fakeHetznerActionWaiter{},
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected present state to converge when already attached to configured server")
	}
	if volumeClient.attachCalls != 0 {
		t.Fatalf("expected no attach calls, got %d", volumeClient.attachCalls)
	}
	if volumeClient.detachCalls != 0 {
		t.Fatalf("expected no detach calls, got %d", volumeClient.detachCalls)
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
