package cli

import (
	"testing"

	etcdfs "github.com/purpleidea/mgmt/etcd/fs"
	"github.com/purpleidea/mgmt/lib"

	"github.com/google/uuid"
)

func TestDeployFsURI(t *testing.T) {
	id := uint64(42)
	uniqueid := uuid.MustParse("12345678-1234-1234-1234-1234567890ab")

	got := deployFsURI(id, uniqueid)
	want := etcdfs.Scheme + "://" + lib.MetadataPrefix + "/deploy/42-12345678-1234-1234-1234-1234567890ab"
	if got != want {
		t.Fatalf("unexpected deploy fs uri: got %q, want %q", got, want)
	}
}
