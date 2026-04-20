// Mgmt
// Copyright (C) James Shubin and the project contributors
// Written by James Shubin <james@shubin.ca> and the project contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// Additional permission under GNU GPL version 3 section 7
//
// If you modify this program, or any covered work, by linking or combining it
// with embedded mcl code and modules (and that the embedded mcl code and
// modules which link with this program, contain a copy of their source code in
// the authoritative form) containing parts covered by the terms of any other
// license, the licensors of this program grant you additional permission to
// convey the resulting work. Furthermore, the licensors of this program grant
// the original author, James Shubin, additional permission to update this
// additional permission if he deems it necessary to achieve the goals of this
// additional permission.

package resources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
)

func init() {
	engine.RegisterResource(KindNixGCRoot, func() engine.Res { return &NixGCRootRes{} })
}

const (
	// KindNixGCRoot is the resource kind.
	KindNixGCRoot = "nix:gcroot"

	// NixGCRootStateExists ensures the gcroot symlink exists.
	NixGCRootStateExists = "exists"

	// NixGCRootStateAbsent ensures the gcroot symlink does not exist.
	NixGCRootStateAbsent = "absent"
)

// NixGCRootRes manages a direct Nix gcroot symlink.
type NixGCRootRes struct {
	traits.Base
	traits.Edgeable

	init *engine.Init

	Path   string `lang:"path" yaml:"path"`
	Target string `lang:"target" yaml:"target"`
	State  string `lang:"state" yaml:"state"`
	Force  bool   `lang:"force" yaml:"force"`

	GCRootsDir string `lang:"gc_roots_dir" yaml:"gc_roots_dir"`
	StoreDir   string `lang:"store_dir" yaml:"store_dir"`
}

// Default returns sensible defaults for a gcroot resource.
func (obj *NixGCRootRes) Default() engine.Res {
	return &NixGCRootRes{
		State:      NixGCRootStateExists,
		GCRootsDir: "/nix/var/nix/gcroots",
		StoreDir:   "/nix/store",
	}
}

// Validate checks whether the requested gcroot state is valid.
func (obj *NixGCRootRes) Validate() error {
	if obj.State == "" {
		obj.State = NixGCRootStateExists
	}
	if obj.GCRootsDir == "" {
		obj.GCRootsDir = "/nix/var/nix/gcroots"
	}
	if obj.StoreDir == "" {
		obj.StoreDir = "/nix/store"
	}

	switch obj.State {
	case NixGCRootStateExists, NixGCRootStateAbsent:
	default:
		return fmt.Errorf("state must be %q or %q", NixGCRootStateExists, NixGCRootStateAbsent)
	}

	path := obj.getPath()
	if path == "" {
		return fmt.Errorf("path is empty")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute")
	}

	obj.GCRootsDir = filepath.Clean(obj.GCRootsDir)
	obj.StoreDir = filepath.Clean(obj.StoreDir)
	if !filepath.IsAbs(obj.GCRootsDir) {
		return fmt.Errorf("gc_roots_dir must be absolute")
	}
	if !filepath.IsAbs(obj.StoreDir) {
		return fmt.Errorf("store_dir must be absolute")
	}
	if !pathUnderDir(path, obj.GCRootsDir) {
		return fmt.Errorf("path must be under gc_roots_dir %s: %s", obj.GCRootsDir, path)
	}

	if obj.State == NixGCRootStateExists {
		if obj.Target == "" {
			return fmt.Errorf("target is required when state is %q", NixGCRootStateExists)
		}
		if !obj.isStorePath(obj.Target) {
			return fmt.Errorf("target is not under store_dir: %s", obj.Target)
		}
	}
	if obj.State == NixGCRootStateAbsent && obj.Target != "" && !obj.isStorePath(obj.Target) {
		return fmt.Errorf("target is not under store_dir: %s", obj.Target)
	}
	return nil
}

// Init runs startup code for this resource.
func (obj *NixGCRootRes) Init(init *engine.Init) error {
	obj.init = init
	return nil
}

// Cleanup releases any cached state.
func (obj *NixGCRootRes) Cleanup() error {
	return nil
}

// Watch emits an initial event and then waits for cancellation.
func (obj *NixGCRootRes) Watch(ctx context.Context) error {
	if obj.init != nil && obj.init.Event != nil {
		if err := obj.init.Event(ctx); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return nil
}

// CheckApply checks and optionally applies gcroot state.
func (obj *NixGCRootRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	_ = ctx
	ok, err := obj.stateOK()
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}
	if !apply {
		return false, nil
	}

	switch obj.State {
	case NixGCRootStateExists:
		if err := obj.applyExists(); err != nil {
			return false, err
		}
	case NixGCRootStateAbsent:
		if err := obj.applyAbsent(); err != nil {
			return false, err
		}
	default:
		return false, fmt.Errorf("invalid state: %s", obj.State)
	}
	return false, nil
}

// Cmp compares two NixGCRootRes resources.
func (obj *NixGCRootRes) Cmp(r engine.Res) error {
	res, ok := r.(*NixGCRootRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.Path != res.Path {
		return fmt.Errorf("the Path differs")
	}
	if obj.Target != res.Target {
		return fmt.Errorf("the Target differs")
	}
	if obj.State != res.State {
		return fmt.Errorf("the State differs")
	}
	if obj.Force != res.Force {
		return fmt.Errorf("the Force differs")
	}
	if obj.GCRootsDir != res.GCRootsDir {
		return fmt.Errorf("the GCRootsDir differs")
	}
	if obj.StoreDir != res.StoreDir {
		return fmt.Errorf("the StoreDir differs")
	}
	return nil
}

func (obj *NixGCRootRes) getPath() string {
	if obj.Path != "" {
		return obj.Path
	}
	return obj.Name()
}

func (obj *NixGCRootRes) stateOK() (bool, error) {
	path := obj.getPath()
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return obj.State == NixGCRootStateAbsent, nil
		}
		return false, err
	}

	if obj.State == NixGCRootStateAbsent {
		if obj.Target == "" {
			return false, nil
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if obj.Force {
				return false, nil
			}
			return false, fmt.Errorf("path exists and is not a symlink: %s", path)
		}
		target, err := os.Readlink(path)
		if err != nil {
			return false, err
		}
		if target == obj.Target {
			return false, nil
		}
		if obj.Force {
			return false, nil
		}
		return false, fmt.Errorf("path points to a different target: %s -> %s", path, target)
	}

	if info.Mode()&os.ModeSymlink == 0 {
		if obj.Force {
			return false, nil
		}
		return false, fmt.Errorf("path exists and is not a symlink: %s", path)
	}
	target, err := os.Readlink(path)
	if err != nil {
		return false, err
	}
	return target == obj.Target, nil
}

func (obj *NixGCRootRes) applyExists() error {
	path := obj.getPath()

	info, err := os.Lstat(path)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("refusing to replace directory: %s", path)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if target == obj.Target {
				return nil
			}
		}
		if !obj.Force {
			return fmt.Errorf("path exists with a different target; set force to replace: %s", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, ".mgmt-nix-gcroot-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Remove(tmpName); err != nil {
		return err
	}
	if err := os.Symlink(obj.Target, tmpName); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func (obj *NixGCRootRes) applyAbsent() error {
	path := obj.getPath()
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("refusing to remove directory: %s", path)
	}
	if info.Mode()&os.ModeSymlink != 0 && obj.Target != "" {
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if target != obj.Target && !obj.Force {
			return fmt.Errorf("path points to a different target; set force to remove: %s -> %s", path, target)
		}
	}
	if info.Mode()&os.ModeSymlink == 0 && !obj.Force {
		return fmt.Errorf("path exists and is not a symlink; set force to remove: %s", path)
	}
	return os.Remove(path)
}

func (obj *NixGCRootRes) isStorePath(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	return pathUnderDir(path, obj.StoreDir)
}

func pathUnderDir(path string, dir string) bool {
	cleanDir := filepath.Clean(dir)
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil {
		return false
	}
	if rel == "." || rel == "" {
		return false
	}
	if strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return false
	}
	return true
}
