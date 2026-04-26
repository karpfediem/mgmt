package resources

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNixGCRootResValidate(t *testing.T) {
	t.Parallel()

	res := &NixGCRootRes{
		Path:       "/nix/var/nix/gcroots/mgmt/demo",
		Target:     "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-demo",
		State:      NixGCRootStateExists,
		GCRootsDir: "/nix/var/nix/gcroots",
		StoreDir:   "/nix/store",
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	bad := &NixGCRootRes{
		Path:       "/tmp/demo",
		Target:     "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-demo",
		State:      NixGCRootStateExists,
		GCRootsDir: "/nix/var/nix/gcroots",
		StoreDir:   "/nix/store",
	}
	if err := bad.Validate(); err == nil {
		t.Fatalf("expected invalid gcroot path to fail validation")
	}
}

func TestNixGCRootResCheckApplyCreatesSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gcroots := filepath.Join(dir, "gcroots")
	store := filepath.Join(dir, "store")
	target := filepath.Join(store, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-demo")
	path := filepath.Join(gcroots, "mgmt", "demo-current")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatalf("failed to create store dir: %v", err)
	}
	if err := os.WriteFile(target, []byte("demo"), 0644); err != nil {
		t.Fatalf("failed to create target: %v", err)
	}

	res := &NixGCRootRes{
		Path:       path,
		Target:     target,
		State:      NixGCRootStateExists,
		Force:      true,
		GCRootsDir: gcroots,
		StoreDir:   store,
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected first apply to report non-converged after creating gcroot")
	}

	linkTarget, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("failed to read gcroot symlink: %v", err)
	}
	if linkTarget != target {
		t.Fatalf("unexpected gcroot target: %s", linkTarget)
	}
}

func TestNixGCRootResCheckApplyRejectsDanglingTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gcroots := filepath.Join(dir, "gcroots")
	store := filepath.Join(dir, "store")
	target := filepath.Join(store, "cccccccccccccccccccccccccccccccc-demo")
	path := filepath.Join(gcroots, "mgmt", "demo-current")

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create parent: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("failed to create seed symlink: %v", err)
	}

	res := &NixGCRootRes{
		Path:       path,
		Target:     target,
		State:      NixGCRootStateExists,
		Force:      true,
		GCRootsDir: gcroots,
		StoreDir:   store,
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("checkapply check failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected dangling gcroot target to be non-converged")
	}

	checkOK, err = res.CheckApply(context.Background(), true)
	if err == nil {
		t.Fatalf("expected apply to reject dangling gcroot target")
	}
	if checkOK {
		t.Fatalf("expected failed apply to report non-converged")
	}
}

func TestNixGCRootResCheckApplyRemovesSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gcroots := filepath.Join(dir, "gcroots")
	path := filepath.Join(gcroots, "mgmt", "demo-current")
	target := "/nix/store/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-demo"

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create parent: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("failed to create seed symlink: %v", err)
	}

	res := &NixGCRootRes{
		Path:       path,
		Target:     target,
		State:      NixGCRootStateAbsent,
		Force:      true,
		GCRootsDir: gcroots,
		StoreDir:   "/nix/store",
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected first apply to report non-converged after removing gcroot")
	}

	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected gcroot to be absent, got err=%v", err)
	}
}
