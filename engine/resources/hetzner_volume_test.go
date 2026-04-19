package resources

import (
	"context"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type fakeHetznerLocationLookupClient struct {
	location *hcloud.Location
	err      error
}

func (obj *fakeHetznerLocationLookupClient) GetByName(_ context.Context, _ string) (*hcloud.Location, *hcloud.Response, error) {
	return obj.location, nil, obj.err
}

type fakeHetznerVolumeLifecycleClient struct {
	volume *hcloud.Volume
	err    error

	createCalls           []hcloud.VolumeCreateOpts
	deleteCalls           int
	updateCalls           []hcloud.VolumeUpdateOpts
	changeProtectionCalls []hcloud.VolumeChangeProtectionOpts
	resizeCalls           []int
}

func (obj *fakeHetznerVolumeLifecycleClient) GetByName(_ context.Context, _ string) (*hcloud.Volume, *hcloud.Response, error) {
	return obj.volume, nil, obj.err
}

func (obj *fakeHetznerVolumeLifecycleClient) Create(_ context.Context, opts hcloud.VolumeCreateOpts) (hcloud.VolumeCreateResult, *hcloud.Response, error) {
	obj.createCalls = append(obj.createCalls, opts)
	obj.volume = &hcloud.Volume{
		ID:       200,
		Name:     opts.Name,
		Size:     opts.Size,
		Location: opts.Location,
		Labels:   opts.Labels,
		Format:   opts.Format,
	}
	return hcloud.VolumeCreateResult{
		Volume:      obj.volume,
		Action:      &hcloud.Action{ID: 2000, Status: hcloud.ActionStatusSuccess},
		NextActions: []*hcloud.Action{{ID: 2001, Status: hcloud.ActionStatusSuccess}},
	}, nil, obj.err
}

func (obj *fakeHetznerVolumeLifecycleClient) Delete(_ context.Context, _ *hcloud.Volume) (*hcloud.Response, error) {
	obj.deleteCalls++
	obj.volume = nil
	return nil, obj.err
}

func (obj *fakeHetznerVolumeLifecycleClient) Update(_ context.Context, _ *hcloud.Volume, opts hcloud.VolumeUpdateOpts) (*hcloud.Volume, *hcloud.Response, error) {
	obj.updateCalls = append(obj.updateCalls, opts)
	if obj.volume != nil {
		obj.volume.Name = opts.Name
		obj.volume.Labels = opts.Labels
	}
	return obj.volume, nil, obj.err
}

func (obj *fakeHetznerVolumeLifecycleClient) ChangeProtection(_ context.Context, _ *hcloud.Volume, opts hcloud.VolumeChangeProtectionOpts) (*hcloud.Action, *hcloud.Response, error) {
	obj.changeProtectionCalls = append(obj.changeProtectionCalls, opts)
	if obj.volume != nil && opts.Delete != nil {
		obj.volume.Protection.Delete = *opts.Delete
	}
	return &hcloud.Action{ID: 2100 + int64(len(obj.changeProtectionCalls)), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func (obj *fakeHetznerVolumeLifecycleClient) Resize(_ context.Context, _ *hcloud.Volume, size int) (*hcloud.Action, *hcloud.Response, error) {
	obj.resizeCalls = append(obj.resizeCalls, size)
	if obj.volume != nil {
		obj.volume.Size = size
	}
	return &hcloud.Action{ID: 2200 + int64(len(obj.resizeCalls)), Status: hcloud.ActionStatusSuccess}, nil, obj.err
}

func TestHetznerVolumeCreate(t *testing.T) {
	locationClient := &fakeHetznerLocationLookupClient{
		location: &hcloud.Location{Name: "nbg1"},
	}
	volumeClient := &fakeHetznerVolumeLifecycleClient{}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerVolumeRes{
		APIToken:       "token",
		State:          HetznerStateExists,
		Location:       "nbg1",
		Size:           10,
		Format:         hcloud.VolumeFormatExt4,
		Labels:         map[string]string{"role": "db"},
		WaitInterval:   HetznerWaitIntervalDefault,
		WaitTimeout:    HetznerWaitTimeoutDefault,
		locationClient: locationClient,
		volumeClient:   volumeClient,
		actionWaiter:   waiter,
	}
	res.SetName("dolt-data")

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected create to report non-converged on apply")
	}
	if len(volumeClient.createCalls) != 1 {
		t.Fatalf("expected one create call, got %d", len(volumeClient.createCalls))
	}
	if got := volumeClient.createCalls[0].Name; got != "dolt-data" {
		t.Fatalf("unexpected create name: %s", got)
	}
	if volumeClient.createCalls[0].Format == nil || *volumeClient.createCalls[0].Format != hcloud.VolumeFormatExt4 {
		t.Fatalf("unexpected create format: %+v", volumeClient.createCalls[0].Format)
	}
	if got := len(waiter.actions); got != 2 {
		t.Fatalf("expected waiter to observe two create actions, got %d", got)
	}
}

func TestHetznerVolumeReconcile(t *testing.T) {
	format := hcloud.VolumeFormatExt4
	volumeClient := &fakeHetznerVolumeLifecycleClient{
		volume: &hcloud.Volume{
			ID:       200,
			Name:     "dolt-data",
			Size:     10,
			Location: &hcloud.Location{Name: "nbg1"},
			Labels:   map[string]string{"role": "old"},
			Format:   &format,
			Protection: hcloud.VolumeProtection{
				Delete: false,
			},
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerVolumeRes{
		APIToken:         "token",
		State:            HetznerStateExists,
		Location:         "nbg1",
		Size:             20,
		Format:           hcloud.VolumeFormatExt4,
		Labels:           map[string]string{"role": "db"},
		DeleteProtection: true,
		WaitInterval:     HetznerWaitIntervalDefault,
		WaitTimeout:      HetznerWaitTimeoutDefault,
		volumeClient:     volumeClient,
		actionWaiter:     waiter,
	}
	res.SetName("dolt-data")

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected reconcile to report non-converged on apply")
	}
	if len(volumeClient.updateCalls) != 1 {
		t.Fatalf("expected one update call, got %d", len(volumeClient.updateCalls))
	}
	if len(volumeClient.changeProtectionCalls) != 1 {
		t.Fatalf("expected one protection call, got %d", len(volumeClient.changeProtectionCalls))
	}
	if len(volumeClient.resizeCalls) != 1 || volumeClient.resizeCalls[0] != 20 {
		t.Fatalf("expected one resize to 20, got %+v", volumeClient.resizeCalls)
	}
	if got := len(waiter.actions); got != 2 {
		t.Fatalf("expected waiter to observe two actions, got %d", got)
	}
	if volumeClient.volume.Size != 20 {
		t.Fatalf("expected volume size to be updated, got %d", volumeClient.volume.Size)
	}
	if !volumeClient.volume.Protection.Delete {
		t.Fatalf("expected delete protection to be enabled")
	}
}

func TestHetznerVolumeAbsentDisablesProtection(t *testing.T) {
	format := hcloud.VolumeFormatExt4
	volumeClient := &fakeHetznerVolumeLifecycleClient{
		volume: &hcloud.Volume{
			ID:       200,
			Name:     "dolt-data",
			Size:     10,
			Location: &hcloud.Location{Name: "nbg1"},
			Format:   &format,
			Protection: hcloud.VolumeProtection{
				Delete: true,
			},
		},
	}
	waiter := &fakeHetznerActionWaiter{}

	res := &HetznerVolumeRes{
		APIToken:     "token",
		State:        HetznerStateAbsent,
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
		volumeClient: volumeClient,
		actionWaiter: waiter,
	}
	res.SetName("dolt-data")

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("CheckApply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected delete to report non-converged on apply")
	}
	if len(volumeClient.changeProtectionCalls) != 1 {
		t.Fatalf("expected one protection call, got %d", len(volumeClient.changeProtectionCalls))
	}
	if volumeClient.deleteCalls != 1 {
		t.Fatalf("expected one delete call, got %d", volumeClient.deleteCalls)
	}
	if got := len(waiter.actions); got != 1 {
		t.Fatalf("expected waiter to observe one action, got %d", got)
	}
}

func TestHetznerVolumeValidateRejectsMissingLocation(t *testing.T) {
	res := &HetznerVolumeRes{
		APIToken:     "token",
		State:        HetznerStateExists,
		Size:         10,
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
	if err := res.Validate(); err == nil {
		t.Fatalf("expected missing location validation error")
	}
}
