package engine_test

import (
	"testing"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/resources"
)

func TestResCopyClonesReversibleMeta(t *testing.T) {
	res := &resources.HetznerFirewallRes{}
	res.SetName("example")
	res.SetKind("hetzner:firewall")
	res.SetReversibleMeta(&engine.ReversibleMeta{
		Overwrite: true,
	})

	copiedRaw, err := engine.ResCopy(res)
	if err != nil {
		t.Fatalf("ResCopy failed: %v", err)
	}

	copied, ok := copiedRaw.(engine.ReversibleRes)
	if !ok {
		t.Fatalf("copied resource is not reversible: %T", copiedRaw)
	}

	if copied.ReversibleMeta() == res.ReversibleMeta() {
		t.Fatalf("ResCopy reused the ReversibleMeta pointer")
	}

	copied.ReversibleMeta().Reversal = true
	if res.ReversibleMeta().Reversal {
		t.Fatalf("mutating the copied reversal flag changed the original resource")
	}
}
