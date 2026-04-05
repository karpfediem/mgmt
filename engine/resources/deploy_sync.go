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
	"path"
	"path/filepath"
	"strings"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util"
	"github.com/purpleidea/mgmt/util/errwrap"
	"github.com/purpleidea/mgmt/util/recwatch"
)

const (
	// KindDeploySync is the kind string used to identify this resource.
	KindDeploySync = "deploy:sync"
)

func init() {
	engine.RegisterResource(KindDeploySync, func() engine.Res { return &DeploySync{} })
}

// DeploySync syncs a file or directory tree from the current deploy filesystem
// to a destination on the host filesystem. Directory syncs copy contents in an
// rsync-like fashion. Source symlinks are currently skipped so they can be
// modeled explicitly by separate resources when needed.
type DeploySync struct {
	traits.Base
	traits.GraphQueryable

	init *engine.Init

	// Path is the destination path on the host. It defaults to the resource
	// name if not specified.
	Path string `lang:"path" yaml:"path"`

	// Source is the absolute path inside the deploy filesystem to sync from.
	Source string `lang:"source" yaml:"source"`

	// Recurse must be true when syncing a source directory.
	Recurse bool `lang:"recurse" yaml:"recurse"`

	// Force allows replacing a file with a directory or vice-versa.
	Force bool `lang:"force" yaml:"force"`

	// Purge removes unmanaged files from a destination directory.
	Purge bool `lang:"purge" yaml:"purge"`
}

// getPath returns the actual path to use for this resource.
func (obj *DeploySync) getPath() string {
	if obj.Path == "" {
		return obj.Name()
	}
	return obj.Path
}

// Default returns some sensible defaults for this resource.
func (obj *DeploySync) Default() engine.Res {
	return &DeploySync{}
}

// Validate reports any problems with the struct definition.
func (obj *DeploySync) Validate() error {
	if obj.getPath() == "" {
		return fmt.Errorf("path is empty")
	}
	if !strings.HasPrefix(obj.getPath(), "/") {
		return fmt.Errorf("path must be absolute")
	}
	if obj.Source == "" {
		return fmt.Errorf("source is empty")
	}
	if !strings.HasPrefix(obj.Source, "/") {
		return fmt.Errorf("source must be absolute")
	}

	srcIsDir := strings.HasSuffix(obj.Source, "/")
	dstIsDir := strings.HasSuffix(obj.getPath(), "/")
	if srcIsDir != dstIsDir {
		return fmt.Errorf("the path and source must either both be dirs or both not be")
	}
	if srcIsDir && !obj.Recurse {
		return fmt.Errorf("you'll want to Recurse when you have a source dir to copy")
	}
	if !srcIsDir && obj.Recurse {
		return fmt.Errorf("you can't recurse when copying a single file")
	}
	if obj.Purge && !dstIsDir {
		return fmt.Errorf("purge requires a destination directory")
	}
	if obj.Purge && !obj.Recurse {
		return fmt.Errorf("you'll want to Recurse when you have a Purge to do")
	}

	return nil
}

// Init runs some startup code for this resource.
func (obj *DeploySync) Init(init *engine.Init) error {
	obj.init = init
	return nil
}

// Cleanup is run by the engine to clean up after the resource is done.
func (obj *DeploySync) Cleanup() error {
	return nil
}

// Watch listens for destination filesystem changes and new deploys.
func (obj *DeploySync) Watch(ctx context.Context) error {
	recWatcher, err := recwatch.NewRecWatcher(obj.getPath(), obj.Recurse)
	if err != nil {
		return err
	}
	defer recWatcher.Close()

	deployWatcher, err := obj.init.World.WatchDeploy(ctx)
	if err != nil {
		return errwrap.Wrapf(err, "can't watch deploys")
	}

	obj.init.Running()

	for {
		select {
		case event, ok := <-recWatcher.Events():
			if !ok {
				return fmt.Errorf("unexpected close")
			}
			if err := event.Error; err != nil {
				return errwrap.Wrapf(err, "unknown %s watcher error", obj)
			}
			if obj.init.Debug {
				obj.init.Logf("event(%s): %v", event.Body.Name, event.Body.Op)
			}

		case err, ok := <-deployWatcher:
			if !ok {
				return fmt.Errorf("unexpected deploy watcher close")
			}
			if err != nil {
				return errwrap.Wrapf(err, "deploy watcher error")
			}
			if obj.init.Debug {
				obj.init.Logf("deploy event")
			}

		case <-ctx.Done():
			return nil
		}

		obj.init.Event()
	}
}

func (obj *DeploySync) fileHelper(dst string) *FileRes {
	helper := &FileRes{
		Path:    dst,
		State:   FileStateExists,
		Force:   obj.Force,
		Recurse: obj.Recurse,
	}
	helper.SetKind(KindFile)
	helper.init = obj.init
	return helper
}

func (obj *DeploySync) ensureDir(ctx context.Context, apply bool, filesystem engine.Fs, src, dst string) (bool, error) {
	srcInfo, err := filesystem.Stat(path.Clean(src))
	if err != nil {
		return false, errwrap.Wrapf(err, "can't stat source dir `%s`", src)
	}
	if !srcInfo.IsDir() {
		return false, fmt.Errorf("source dir is not a directory: %s", src)
	}

	dstInfo, err := os.Stat(dst)
	if err != nil && !os.IsNotExist(err) {
		return false, errwrap.Wrapf(err, "can't stat destination dir `%s`", dst)
	}
	if err == nil && dstInfo.IsDir() {
		return true, nil
	}
	if err == nil && !dstInfo.IsDir() && !obj.Force {
		return false, fmt.Errorf("can't force file into dir: %s", dst)
	}
	if !apply {
		return false, nil
	}

	cleanDst := path.Clean(dst)
	if cleanDst == "" || cleanDst == "/" {
		return false, fmt.Errorf("don't want to remove root")
	}
	if err == nil && !dstInfo.IsDir() {
		obj.init.Logf("removing (force): %s", cleanDst)
		if err := os.RemoveAll(cleanDst); err != nil {
			return false, err
		}
	}

	obj.init.Logf("mkdir -p -m %s", srcInfo.Mode())
	if err := os.MkdirAll(cleanDst, srcInfo.Mode()); err != nil {
		return false, err
	}
	return false, nil
}

func readDeployDir(filesystem engine.Fs, p string) ([]FileInfo, error) {
	if !strings.HasSuffix(p, "/") {
		return nil, fmt.Errorf("path must be a directory")
	}
	output := []FileInfo{}
	files, err := filesystem.ReadDir(path.Clean(p))
	if os.IsNotExist(err) {
		return output, err
	}
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		abs := p + file.Name()
		if file.IsDir() {
			abs += "/"
		}
		rel, err := filepath.Rel(p, abs)
		if err != nil {
			return nil, errwrap.Wrapf(err, "unhandled error in readDeployDir")
		}
		if file.IsDir() {
			rel += "/"
		}

		output = append(output, FileInfo{
			FileInfo: file,
			AbsPath:  abs,
			RelPath:  rel,
		})
	}
	return output, nil
}

func (obj *DeploySync) syncCheckApply(ctx context.Context, apply bool, filesystem engine.Fs, src, dst string, excludes []string) (bool, error) {
	if obj.init.Debug {
		obj.init.Logf("deploy sync: %s -> %s", src, dst)
	}
	if src == "" || dst == "" {
		return false, fmt.Errorf("the src and dst must not be empty")
	}

	checkOK := true

	srcIsDir := strings.HasSuffix(src, "/")
	dstIsDir := strings.HasSuffix(dst, "/")
	if srcIsDir != dstIsDir {
		return false, fmt.Errorf("the src and dst must be both either files or directories")
	}

	if !srcIsDir && !dstIsDir {
		fin, err := filesystem.Open(src)
		if err != nil {
			return false, errwrap.Wrapf(err, "can't open source `%s`", src)
		}
		defer fin.Close()

		_, checkOK, err := obj.fileHelper(dst).fileCheckApply(ctx, apply, fin, dst, "")
		return checkOK, err
	}

	if c, err := obj.ensureDir(ctx, apply, filesystem, src, dst); err != nil {
		return false, err
	} else if !c {
		checkOK = false
	}
	if !apply && !checkOK {
		return false, nil
	}

	srcFiles, err := readDeployDir(filesystem, src)
	if os.IsNotExist(err) {
		return false, fmt.Errorf("source dir does not exist: %s", src)
	}
	if err != nil {
		return false, err
	}

	filteredSrc := []FileInfo{}
	for _, fileInfo := range srcFiles {
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			if obj.init.Debug {
				obj.init.Logf("skipping source symlink: %s", fileInfo.AbsPath)
			}
			continue
		}
		if !fileInfo.IsDir() && !fileInfo.Mode().IsRegular() {
			return false, fmt.Errorf("can't sync non-regular deploy path: %s (%q)", fileInfo.AbsPath, fileInfo.Mode())
		}
		filteredSrc = append(filteredSrc, fileInfo)
	}

	smartSrc := mapPaths(filteredSrc)
	if obj.init.Debug {
		obj.init.Logf("srcFiles: %v", printFiles(smartSrc))
	}

	dstFiles, err := ReadDir(dst)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	smartDst := mapPaths(dstFiles)
	if obj.init.Debug {
		obj.init.Logf("dstFiles: %v", printFiles(smartDst))
	}

	for relPath, fileInfo := range smartSrc {
		absSrc := fileInfo.AbsPath
		absDst := dst + relPath

		if _, exists := smartDst[relPath]; !exists && fileInfo.IsDir() {
			if !apply {
				return false, nil
			}

			relPathFile := strings.TrimSuffix(relPath, "/")
			if _, ok := smartDst[relPathFile]; ok {
				absCleanDst := path.Clean(absDst)
				if !obj.Force {
					return false, fmt.Errorf("can't force file into dir: %s", absCleanDst)
				}
				if absCleanDst == "" || absCleanDst == "/" {
					return false, fmt.Errorf("don't want to remove root")
				}
				obj.init.Logf("removing (force): %s", absCleanDst)
				if err := os.Remove(absCleanDst); err != nil {
					return false, err
				}
				delete(smartDst, relPathFile)
			}

			if obj.init.Debug {
				obj.init.Logf("mkdir -m %s '%s'", fileInfo.Mode(), absDst)
			}
			if err := os.Mkdir(absDst, fileInfo.Mode()); err != nil {
				return false, err
			}
			checkOK = false
		}

		if obj.Recurse {
			if c, err := obj.syncCheckApply(ctx, apply, filesystem, absSrc, absDst, excludes); err != nil {
				return false, errwrap.Wrapf(err, "recurse failed")
			} else if !c {
				checkOK = false
			}
		}
		if !apply && !checkOK {
			return false, nil
		}
		delete(smartDst, relPath)
	}

	if !obj.Purge {
		return checkOK, nil
	}
	if !apply && len(smartDst) > 0 {
		return false, nil
	}

	isExcluded := func(p string) bool {
		for _, x := range excludes {
			if util.HasPathPrefix(x, p) {
				return true
			}
		}
		return false
	}

	for _, fileInfo := range smartDst {
		absDst := fileInfo.AbsPath
		absCleanDst := path.Clean(absDst)
		if absCleanDst == "" || absCleanDst == "/" {
			return false, fmt.Errorf("don't want to remove root")
		}
		if isExcluded(absDst) {
			continue
		}
		obj.init.Logf("removing: %s", absCleanDst)
		if apply {
			if err := os.RemoveAll(absCleanDst); err != nil {
				return false, err
			}
			checkOK = false
		}
	}

	return checkOK, nil
}

// CheckApply checks the resource state and applies the resource if the bool
// input is true. It returns error info and if the state check passed or not.
func (obj *DeploySync) CheckApply(ctx context.Context, apply bool) (bool, error) {
	uri := obj.init.World.URI()
	filesystem, err := obj.init.World.Fs(uri)
	if err != nil {
		return false, errwrap.Wrapf(err, "can't load code from file system `%s`", uri)
	}

	excludes := []string{}
	if obj.Purge {
		graph, err := obj.init.FilteredGraph()
		if err != nil {
			return false, errwrap.Wrapf(err, "can't read filtered graph")
		}
		for _, vertex := range graph.Vertices() {
			res, ok := vertex.(engine.Res)
			if !ok {
				return false, fmt.Errorf("not a Res")
			}
			if res.Kind() == obj.Kind() && res.Name() == obj.Name() {
				continue
			}

			var p string
			switch res.Kind() {
			case KindFile:
				fileRes, ok := res.(*FileRes)
				if !ok {
					return false, fmt.Errorf("not a FileRes")
				}
				p = fileRes.getPath()

			case KindDeploySync:
				deploySyncRes, ok := res.(*DeploySync)
				if !ok {
					return false, fmt.Errorf("not a DeploySync")
				}
				p = deploySyncRes.getPath()

			default:
				continue
			}

			if !util.HasPathPrefix(p, obj.getPath()) {
				continue
			}
			excludes = append(excludes, p)
		}
	}
	if obj.init.Debug {
		obj.init.Logf("excludes: %+v", excludes)
	}

	return obj.syncCheckApply(ctx, apply, filesystem, obj.Source, obj.getPath(), excludes)
}

// Cmp compares two resources and returns an error if they are not equivalent.
func (obj *DeploySync) Cmp(r engine.Res) error {
	res, ok := r.(*DeploySync)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}

	if obj.getPath() != res.getPath() {
		return fmt.Errorf("the Path differs")
	}
	if obj.Source != res.Source {
		return fmt.Errorf("the Source differs")
	}
	if obj.Recurse != res.Recurse {
		return fmt.Errorf("the Recurse option differs")
	}
	if obj.Force != res.Force {
		return fmt.Errorf("the Force option differs")
	}
	if obj.Purge != res.Purge {
		return fmt.Errorf("the Purge option differs")
	}

	return nil
}

// GraphQueryAllowed returns nil if you're allowed to query the graph.
func (obj *DeploySync) GraphQueryAllowed(opts ...engine.GraphQueryableOption) error {
	options := &engine.GraphQueryableOptions{}
	options.Apply(opts...)
	if options.Kind != KindFile && options.Kind != KindDeploySync {
		return fmt.Errorf("only file and deploy:sync resources can access my information")
	}
	return nil
}
