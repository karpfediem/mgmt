package resources

import (
	"context"
	"fmt"
	"maps"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func init() {
	engine.RegisterResource("hetzner:vm_labels", func() engine.Res { return &HetznerVMLabelsRes{} })
}

// HetznerVMLabelsRes manages the full label map on an existing Hetzner VM.
type HetznerVMLabelsRes struct {
	traits.Base

	init *engine.Init

	APIToken string            `lang:"apitoken"`
	State    string            `lang:"state"`
	Server   string            `lang:"server"`
	Labels   map[string]string `lang:"labels"`

	WaitInterval uint32 `lang:"waitinterval"`
	WaitTimeout  uint32 `lang:"waittimeout"`

	client       *hcloud.Client
	serverClient hetznerServerLabelClient

	server *hcloud.Server
}

// Default returns some conservative defaults for this resource.
func (obj *HetznerVMLabelsRes) Default() engine.Res {
	return &HetznerVMLabelsRes{
		State:        HetznerStateExists,
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
}

// Validate checks whether the requested VM label state is valid.
func (obj *HetznerVMLabelsRes) Validate() error {
	if obj.APIToken == "" {
		return fmt.Errorf("empty token string")
	}
	if err := validateHetznerState(obj.State); err != nil {
		return err
	}
	if obj.Server == "" {
		return fmt.Errorf("empty server")
	}
	if obj.MetaParams().Poll < HetznerPollLimit {
		return fmt.Errorf("invalid polling interval (minimum %d s)", HetznerPollLimit)
	}
	if obj.WaitInterval < HetznerWaitIntervalLimit {
		return fmt.Errorf("invalid wait interval (minimum %d)", HetznerWaitIntervalLimit)
	}
	return nil
}

// Init runs startup code for this resource.
func (obj *HetznerVMLabelsRes) Init(init *engine.Init) error {
	obj.init = init
	obj.client = newHetznerClient(init, obj.APIToken, obj.WaitInterval)
	obj.serverClient = &obj.client.Server
	return nil
}

// Cleanup removes authentication and client state from memory.
func (obj *HetznerVMLabelsRes) Cleanup() error {
	obj.APIToken = ""
	obj.client = nil
	obj.serverClient = nil
	obj.server = nil
	return nil
}

// Watch is not implemented for this resource, since the Hetzner API does not provide events.
func (obj *HetznerVMLabelsRes) Watch(context.Context) error {
	return fmt.Errorf("invalid Watch call: requires poll metaparam")
}

// CheckApply checks and applies VM label state.
func (obj *HetznerVMLabelsRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	if err := obj.getTargetUpdate(ctx); err != nil {
		return false, errwrap.Wrapf(err, "getTargetUpdate failed")
	}

	if obj.server == nil {
		if obj.State == HetznerStateAbsent {
			return true, nil
		}
		return false, fmt.Errorf("server not found: %s", obj.Server)
	}

	desiredLabels := obj.Labels
	if obj.State == HetznerStateAbsent {
		desiredLabels = map[string]string{}
	}
	if maps.Equal(obj.server.Labels, desiredLabels) {
		return true, nil
	}
	if !apply {
		return false, nil
	}
	if err := obj.updateLabels(ctx, desiredLabels); err != nil {
		return false, errwrap.Wrapf(err, "updateLabels failed")
	}
	return false, nil
}

func (obj *HetznerVMLabelsRes) getTargetUpdate(ctx context.Context) error {
	server, err := hetznerServerByName(ctx, obj.serverClient, obj.Server)
	if err != nil {
		return errwrap.Wrapf(err, "server lookup failed")
	}
	obj.server = server
	return nil
}

func (obj *HetznerVMLabelsRes) updateLabels(ctx context.Context, labels map[string]string) error {
	_, _, err := obj.serverClient.Update(ctx, obj.server, hcloud.ServerUpdateOpts{
		Labels: labels,
	})
	return err
}

// Cmp compares two resources.
func (obj *HetznerVMLabelsRes) Cmp(r engine.Res) error {
	res, ok := r.(*HetznerVMLabelsRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.APIToken != res.APIToken {
		return fmt.Errorf("apitoken differs")
	}
	if obj.State != res.State {
		return fmt.Errorf("state differs")
	}
	if obj.Server != res.Server {
		return fmt.Errorf("server differs")
	}
	if !maps.Equal(obj.Labels, res.Labels) {
		return fmt.Errorf("labels differ")
	}
	if obj.WaitInterval != res.WaitInterval {
		return fmt.Errorf("waitinterval differs")
	}
	if obj.WaitTimeout != res.WaitTimeout {
		return fmt.Errorf("waittimeout differs")
	}
	return nil
}
