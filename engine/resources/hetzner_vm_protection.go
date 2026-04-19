package resources

import (
	"context"
	"fmt"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func init() {
	engine.RegisterResource("hetzner:vm_protection", func() engine.Res { return &HetznerVMProtectionRes{} })
}

// HetznerVMProtectionRes manages delete and rebuild protection on an existing Hetzner VM.
type HetznerVMProtectionRes struct {
	traits.Base

	init *engine.Init

	APIToken          string `lang:"apitoken"`
	State             string `lang:"state"`
	Server            string `lang:"server"`
	DeleteProtection  bool   `lang:"deleteprotection"`
	RebuildProtection bool   `lang:"rebuildprotection"`

	WaitInterval uint32 `lang:"waitinterval"`
	WaitTimeout  uint32 `lang:"waittimeout"`

	client       *hcloud.Client
	actionWaiter hetznerActionWaiter
	serverClient hetznerServerProtectionClient

	server *hcloud.Server
}

// Default returns some conservative defaults for this resource.
func (obj *HetznerVMProtectionRes) Default() engine.Res {
	return &HetznerVMProtectionRes{
		State:        HetznerStateExists,
		WaitInterval: HetznerWaitIntervalDefault,
		WaitTimeout:  HetznerWaitTimeoutDefault,
	}
}

// Validate checks whether the requested VM protection state is valid.
func (obj *HetznerVMProtectionRes) Validate() error {
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
func (obj *HetznerVMProtectionRes) Init(init *engine.Init) error {
	obj.init = init
	obj.client = newHetznerClient(init, obj.APIToken, obj.WaitInterval)
	obj.actionWaiter = &obj.client.Action
	obj.serverClient = &obj.client.Server
	return nil
}

// Cleanup removes authentication and client state from memory.
func (obj *HetznerVMProtectionRes) Cleanup() error {
	obj.APIToken = ""
	obj.client = nil
	obj.actionWaiter = nil
	obj.serverClient = nil
	obj.server = nil
	return nil
}

// Watch is not implemented for this resource, since the Hetzner API does not provide events.
func (obj *HetznerVMProtectionRes) Watch(context.Context) error {
	return fmt.Errorf("invalid Watch call: requires poll metaparam")
}

// CheckApply checks and applies VM protection state.
func (obj *HetznerVMProtectionRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	if err := obj.getTargetUpdate(ctx); err != nil {
		return false, errwrap.Wrapf(err, "getTargetUpdate failed")
	}

	if obj.server == nil {
		if obj.State == HetznerStateAbsent {
			return true, nil
		}
		return false, fmt.Errorf("server not found: %s", obj.Server)
	}

	desiredDeleteProtection := obj.DeleteProtection
	desiredRebuildProtection := obj.RebuildProtection
	if obj.State == HetznerStateAbsent {
		desiredDeleteProtection = false
		desiredRebuildProtection = false
	}
	if obj.server.Protection.Delete == desiredDeleteProtection && obj.server.Protection.Rebuild == desiredRebuildProtection {
		return true, nil
	}
	if !apply {
		return false, nil
	}
	if err := obj.setProtection(ctx, desiredDeleteProtection, desiredRebuildProtection); err != nil {
		return false, errwrap.Wrapf(err, "setProtection failed")
	}
	return false, nil
}

func (obj *HetznerVMProtectionRes) getTargetUpdate(ctx context.Context) error {
	server, err := hetznerServerByName(ctx, obj.serverClient, obj.Server)
	if err != nil {
		return errwrap.Wrapf(err, "server lookup failed")
	}
	obj.server = server
	return nil
}

func (obj *HetznerVMProtectionRes) setProtection(ctx context.Context, deleteProtection bool, rebuildProtection bool) error {
	action, _, err := obj.serverClient.ChangeProtection(ctx, obj.server, hcloud.ServerChangeProtectionOpts{
		Delete:  &deleteProtection,
		Rebuild: &rebuildProtection,
	})
	if err != nil {
		return err
	}
	return errwrap.Wrapf(hetznerWaitForAction(ctx, obj.WaitTimeout, obj.actionWaiter, action), "wait for change protection action failed")
}

// Cmp compares two resources.
func (obj *HetznerVMProtectionRes) Cmp(r engine.Res) error {
	res, ok := r.(*HetznerVMProtectionRes)
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
	if obj.DeleteProtection != res.DeleteProtection {
		return fmt.Errorf("deleteprotection differs")
	}
	if obj.RebuildProtection != res.RebuildProtection {
		return fmt.Errorf("rebuildprotection differs")
	}
	if obj.WaitInterval != res.WaitInterval {
		return fmt.Errorf("waitinterval differs")
	}
	if obj.WaitTimeout != res.WaitTimeout {
		return fmt.Errorf("waittimeout differs")
	}
	return nil
}
