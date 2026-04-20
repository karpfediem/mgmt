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
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
)

func init() {
	engine.RegisterResource(KindNixClosure, func() engine.Res { return &NixClosureRes{} })
}

const (
	// KindNixClosure is the resource kind.
	KindNixClosure = "nix:closure"

	// NixClosureStatePresent declares that the requested store roots should be
	// locally materialized.
	NixClosureStatePresent = "present"

	// NixClosureModeVerify only verifies that desired paths are already valid.
	NixClosureModeVerify = "verify"

	// NixClosureModeSubstitute realizes paths with local builds disabled.
	NixClosureModeSubstitute = "substitute"

	// NixClosureModeRealise realizes paths with normal Nix behavior, including
	// local builds when derivations are available.
	NixClosureModeRealise = "realise"
)

// NixClosureRes ensures that one or more Nix store roots are valid in the
// local Nix store.
//
// This resource does not manage services, deployment bundles, or gc roots. It
// only expresses that certain store roots should be locally materialized.
type NixClosureRes struct {
	traits.Base
	traits.Edgeable
	traits.Refreshable

	init *engine.Init

	State string   `lang:"state" yaml:"state"`
	Paths []string `lang:"paths" yaml:"paths"`
	Drvs  []string `lang:"drvs" yaml:"drvs"`

	Mode          string `lang:"mode" yaml:"mode"`
	KeepGoing     bool   `lang:"keep_going" yaml:"keep_going"`
	IgnoreUnknown bool   `lang:"ignore_unknown" yaml:"ignore_unknown"`
	CheckContents bool   `lang:"check_contents" yaml:"check_contents"`

	MaxJobs        int    `lang:"max_jobs" yaml:"max_jobs"`
	Cores          int    `lang:"cores" yaml:"cores"`
	BuildTimeout   uint64 `lang:"build_timeout" yaml:"build_timeout"`
	MaxSilentTime  uint64 `lang:"max_silent_time" yaml:"max_silent_time"`
	CommandTimeout uint64 `lang:"command_timeout" yaml:"command_timeout"`

	NixStore   string            `lang:"nix_store" yaml:"nix_store"`
	StoreDir   string            `lang:"store_dir" yaml:"store_dir"`
	NixOptions map[string]string `lang:"nix_options" yaml:"nix_options"`
	Env        map[string]string `lang:"env" yaml:"env"`
}

// Default returns sensible defaults for a closure resource.
func (obj *NixClosureRes) Default() engine.Res {
	return &NixClosureRes{
		State:    NixClosureStatePresent,
		Mode:     NixClosureModeVerify,
		MaxJobs:  -1,
		Cores:    -1,
		NixStore: "nix-store",
		StoreDir: "/nix/store",
	}
}

// Validate checks whether the requested closure state is valid.
func (obj *NixClosureRes) Validate() error {
	if obj.State == "" {
		obj.State = NixClosureStatePresent
	}
	if obj.Mode == "" {
		obj.Mode = NixClosureModeVerify
	}
	if obj.NixStore == "" {
		obj.NixStore = "nix-store"
	}
	if obj.StoreDir == "" {
		obj.StoreDir = "/nix/store"
	}
	if obj.State != NixClosureStatePresent {
		return fmt.Errorf("state must be %q", NixClosureStatePresent)
	}

	switch obj.Mode {
	case NixClosureModeVerify, NixClosureModeSubstitute, NixClosureModeRealise:
	default:
		return fmt.Errorf("mode must be one of %q, %q, or %q", NixClosureModeVerify, NixClosureModeSubstitute, NixClosureModeRealise)
	}

	if obj.MaxJobs < -1 {
		return fmt.Errorf("max_jobs must be -1 or greater")
	}
	if obj.Cores < -1 {
		return fmt.Errorf("cores must be -1 or greater")
	}

	if !filepath.IsAbs(obj.StoreDir) {
		return fmt.Errorf("store_dir must be absolute")
	}
	obj.StoreDir = filepath.Clean(obj.StoreDir)

	paths := obj.explicitPaths()
	drvs := obj.explicitDrvs()
	if len(paths) == 0 && len(drvs) == 0 && obj.isStorePath(obj.Name()) {
		if strings.HasSuffix(obj.Name(), ".drv") {
			drvs = []string{obj.Name()}
		} else {
			paths = []string{obj.Name()}
		}
	}
	if len(paths) == 0 && len(drvs) == 0 {
		return fmt.Errorf("at least one path or drv must be specified, or the resource name must be a store path")
	}

	for _, path := range paths {
		if !obj.isStorePath(path) {
			return fmt.Errorf("path is not under store_dir: %s", path)
		}
		if strings.HasSuffix(path, ".drv") {
			return fmt.Errorf("derivation path belongs in drvs, not paths: %s", path)
		}
	}
	for _, drv := range drvs {
		if !obj.isStorePath(drv) {
			return fmt.Errorf("drv is not under store_dir: %s", drv)
		}
		if !strings.HasSuffix(drv, ".drv") {
			return fmt.Errorf("drv path must end in .drv: %s", drv)
		}
	}

	for key := range obj.NixOptions {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("nix_options contains an empty key")
		}
	}
	for key := range obj.Env {
		if strings.TrimSpace(key) == "" || strings.Contains(key, "=") {
			return fmt.Errorf("env contains invalid key: %q", key)
		}
	}

	return nil
}

// Init runs startup code for this resource.
func (obj *NixClosureRes) Init(init *engine.Init) error {
	obj.init = init
	return nil
}

// Cleanup releases any cached state.
func (obj *NixClosureRes) Cleanup() error {
	return nil
}

// Watch emits an initial event and then waits for context cancellation. This
// resource expects poll metaparams when periodic re-checking is desired.
func (obj *NixClosureRes) Watch(ctx context.Context) error {
	if obj.init != nil && obj.init.Event != nil {
		if err := obj.init.Event(ctx); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return nil
}

// CheckApply checks and optionally realizes the desired closure roots.
func (obj *NixClosureRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	present, err := obj.closurePresent(ctx)
	if err != nil {
		return false, err
	}

	refresh := false
	if obj.init != nil && obj.init.Refresh != nil {
		refresh = obj.init.Refresh()
	}
	if present && !refresh {
		return true, nil
	}
	if !apply {
		return false, nil
	}
	if obj.Mode == NixClosureModeVerify {
		if present {
			return false, nil
		}
		return false, fmt.Errorf("nix closure is not present and mode is %q", NixClosureModeVerify)
	}

	realiseErr := obj.realise(ctx)
	present, err = obj.closurePresent(ctx)
	if err != nil {
		return false, err
	}
	if present {
		if realiseErr != nil && obj.init != nil && obj.init.Logf != nil {
			obj.init.Logf("nix-store --realise returned an error, but desired closure is now present: %v", realiseErr)
		}
		return false, nil
	}
	if realiseErr != nil {
		return false, realiseErr
	}
	return false, fmt.Errorf("nix closure is still not present after realisation")
}

// Cmp compares two NixClosureRes resources.
func (obj *NixClosureRes) Cmp(r engine.Res) error {
	res, ok := r.(*NixClosureRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}
	if obj.State != res.State {
		return fmt.Errorf("the State differs")
	}
	if !reflect.DeepEqual(obj.Paths, res.Paths) {
		return fmt.Errorf("the Paths differ")
	}
	if !reflect.DeepEqual(obj.Drvs, res.Drvs) {
		return fmt.Errorf("the Drvs differ")
	}
	if obj.Mode != res.Mode {
		return fmt.Errorf("the Mode differs")
	}
	if obj.KeepGoing != res.KeepGoing {
		return fmt.Errorf("the KeepGoing differs")
	}
	if obj.IgnoreUnknown != res.IgnoreUnknown {
		return fmt.Errorf("the IgnoreUnknown differs")
	}
	if obj.CheckContents != res.CheckContents {
		return fmt.Errorf("the CheckContents differs")
	}
	if obj.MaxJobs != res.MaxJobs {
		return fmt.Errorf("the MaxJobs differs")
	}
	if obj.Cores != res.Cores {
		return fmt.Errorf("the Cores differs")
	}
	if obj.BuildTimeout != res.BuildTimeout {
		return fmt.Errorf("the BuildTimeout differs")
	}
	if obj.MaxSilentTime != res.MaxSilentTime {
		return fmt.Errorf("the MaxSilentTime differs")
	}
	if obj.CommandTimeout != res.CommandTimeout {
		return fmt.Errorf("the CommandTimeout differs")
	}
	if obj.NixStore != res.NixStore {
		return fmt.Errorf("the NixStore differs")
	}
	if obj.StoreDir != res.StoreDir {
		return fmt.Errorf("the StoreDir differs")
	}
	if !reflect.DeepEqual(obj.NixOptions, res.NixOptions) {
		return fmt.Errorf("the NixOptions differ")
	}
	if !reflect.DeepEqual(obj.Env, res.Env) {
		return fmt.Errorf("the Env differs")
	}
	return nil
}

func (obj *NixClosureRes) explicitPaths() []string {
	return uniqueSorted(obj.Paths)
}

func (obj *NixClosureRes) explicitDrvs() []string {
	return uniqueSorted(obj.Drvs)
}

func (obj *NixClosureRes) desiredRoots(ctx context.Context) ([]string, bool, error) {
	if paths := obj.explicitPaths(); len(paths) > 0 {
		return paths, true, nil
	}
	if obj.isStorePath(obj.Name()) && !strings.HasSuffix(obj.Name(), ".drv") {
		return []string{obj.Name()}, true, nil
	}

	drvs := obj.explicitDrvs()
	if len(drvs) == 0 && obj.isStorePath(obj.Name()) && strings.HasSuffix(obj.Name(), ".drv") {
		drvs = []string{obj.Name()}
	}
	if len(drvs) == 0 {
		return nil, false, nil
	}

	var roots []string
	for _, drv := range drvs {
		outputs, ok, err := obj.queryDrvOutputs(ctx, drv)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		roots = append(roots, outputs...)
	}
	roots = uniqueSorted(roots)
	return roots, len(roots) > 0, nil
}

func (obj *NixClosureRes) realiseInputs() []string {
	inputs := []string{}
	inputs = append(inputs, obj.explicitPaths()...)
	inputs = append(inputs, obj.explicitDrvs()...)
	if len(inputs) == 0 && obj.isStorePath(obj.Name()) {
		inputs = append(inputs, obj.Name())
	}
	return uniqueSorted(inputs)
}

func (obj *NixClosureRes) closurePresent(ctx context.Context) (bool, error) {
	roots, known, err := obj.desiredRoots(ctx)
	if err != nil {
		return false, err
	}
	if !known || len(roots) == 0 {
		return false, nil
	}

	args := obj.nixArgs("--query", "--requisites")
	args = append(args, roots...)
	result, err := obj.runNix(ctx, args)
	if err != nil {
		return false, err
	}
	if result.ExitCode != 0 {
		if obj.init != nil && obj.init.Debug && obj.init.Logf != nil {
			obj.init.Logf("nix closure query failed: %s", trimForLog(result.Stderr))
		}
		return false, nil
	}

	closure := parseLines(result.Stdout)
	if len(closure) == 0 {
		return false, nil
	}
	if obj.CheckContents {
		for _, chunk := range chunks(closure, 256) {
			verifyArgs := obj.nixArgs("--verify-path")
			verifyArgs = append(verifyArgs, chunk...)
			verify, err := obj.runNix(ctx, verifyArgs)
			if err != nil {
				return false, err
			}
			if verify.ExitCode != 0 {
				if obj.init != nil && obj.init.Debug && obj.init.Logf != nil {
					obj.init.Logf("nix verify-path failed: %s", trimForLog(verify.Stderr))
				}
				return false, nil
			}
		}
	}
	return true, nil
}

func (obj *NixClosureRes) queryDrvOutputs(ctx context.Context, drv string) ([]string, bool, error) {
	args := obj.nixArgs("--query", "--outputs", drv)
	result, err := obj.runNix(ctx, args)
	if err != nil {
		return nil, false, err
	}
	if result.ExitCode != 0 {
		if obj.init != nil && obj.init.Debug && obj.init.Logf != nil {
			obj.init.Logf("nix drv output query failed for %s: %s", drv, trimForLog(result.Stderr))
		}
		return nil, false, nil
	}
	outputs := parseLines(result.Stdout)
	for _, output := range outputs {
		if !obj.isStorePath(output) {
			return nil, false, fmt.Errorf("nix-store returned non-store output for %s: %s", drv, output)
		}
		if strings.HasSuffix(output, ".drv") {
			return nil, false, fmt.Errorf("nix-store returned derivation as output for %s: %s", drv, output)
		}
	}
	return outputs, len(outputs) > 0, nil
}

func (obj *NixClosureRes) realise(ctx context.Context) error {
	inputs := obj.realiseInputs()
	if len(inputs) == 0 {
		return fmt.Errorf("no realisation inputs")
	}

	args := obj.nixArgs("--realise")
	if obj.KeepGoing {
		args = append(args, "--keep-going")
	}
	if obj.IgnoreUnknown {
		args = append(args, "--ignore-unknown")
	}

	switch obj.Mode {
	case NixClosureModeSubstitute:
		args = append(args, "--max-jobs", "0")
	case NixClosureModeRealise:
		if obj.MaxJobs >= 0 {
			args = append(args, "--max-jobs", strconv.Itoa(obj.MaxJobs))
		}
	default:
		return fmt.Errorf("mode %q cannot realise", obj.Mode)
	}
	if obj.Cores >= 0 {
		args = append(args, "--cores", strconv.Itoa(obj.Cores))
	}
	if obj.BuildTimeout > 0 {
		args = append(args, "--timeout", strconv.FormatUint(obj.BuildTimeout, 10))
	}
	if obj.MaxSilentTime > 0 {
		args = append(args, "--max-silent-time", strconv.FormatUint(obj.MaxSilentTime, 10))
	}
	args = append(args, inputs...)

	result, err := obj.runNix(ctx, args)
	if err != nil {
		return err
	}
	if obj.init != nil && obj.init.Debug && obj.init.Logf != nil {
		if result.Stdout != "" {
			obj.init.Logf("nix-store stdout: %s", trimForLog(result.Stdout))
		}
		if result.Stderr != "" {
			obj.init.Logf("nix-store stderr: %s", trimForLog(result.Stderr))
		}
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("nix-store --realise failed with exit code %d: %s", result.ExitCode, trimForLog(result.Stderr))
	}
	return nil
}

type nixRunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (obj *NixClosureRes) runNix(ctx context.Context, args []string) (*nixRunResult, error) {
	cmdCtx := ctx
	cancel := func() {}
	if obj.CommandTimeout > 0 {
		var timeoutCancel context.CancelFunc
		cmdCtx, timeoutCancel = context.WithTimeout(ctx, time.Duration(obj.CommandTimeout)*time.Second)
		cancel = timeoutCancel
	}
	defer cancel()

	cmdName := obj.NixStore
	if cmdName == "" {
		cmdName = "nix-store"
	}
	cmd := exec.CommandContext(cmdCtx, cmdName, args...)
	cmd.Env = mergeEnv(os.Environ(), obj.Env)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &nixRunResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if err == nil {
		return result, nil
	}
	if cmdCtx.Err() != nil {
		return result, cmdCtx.Err()
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return result, err
	}
	result.ExitCode = exitErr.ExitCode()
	return result, nil
}

func (obj *NixClosureRes) nixArgs(args ...string) []string {
	result := []string{}
	keys := []string{}
	for key := range obj.NixOptions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, "--option", key, obj.NixOptions[key])
	}
	result = append(result, args...)
	return result
}

func (obj *NixClosureRes) isStorePath(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	storeDir := filepath.Clean(obj.StoreDir)
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(storeDir, cleanPath)
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

func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		set[item] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func parseLines(value string) []string {
	return uniqueSorted(strings.Split(value, "\n"))
}

func chunks(in []string, size int) [][]string {
	if size <= 0 {
		panic("invalid chunk size")
	}
	var result [][]string
	for len(in) > 0 {
		n := size
		if len(in) < n {
			n = len(in)
		}
		result = append(result, in[:n])
		in = in[n:]
	}
	return result
}

func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	values := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		index := strings.Index(entry, "=")
		if index < 0 {
			continue
		}
		values[entry[:index]] = entry[index+1:]
	}
	for key, value := range overrides {
		values[key] = value
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func trimForLog(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 4096 {
		return value
	}
	return value[:4096] + "... <truncated>"
}
