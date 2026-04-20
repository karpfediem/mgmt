package resources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNixClosureResValidate(t *testing.T) {
	t.Parallel()

	res := &NixClosureRes{
		Paths:    []string{"/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-demo"},
		Mode:     NixClosureModeVerify,
		StoreDir: "/nix/store",
		MaxJobs:  -1,
		Cores:    -1,
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	bad := &NixClosureRes{
		Paths:    []string{"/tmp/not-store"},
		StoreDir: "/nix/store",
		MaxJobs:  -1,
		Cores:    -1,
	}
	if err := bad.Validate(); err == nil {
		t.Fatalf("expected invalid path to fail validation")
	}
}

func TestNixClosureResCheckApplyVerifyPresent(t *testing.T) {
	t.Parallel()

	fake := newFakeNixStore(t)
	root := "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-demo"
	fake.markValid(root)

	res := &NixClosureRes{
		Paths:    []string{root},
		Mode:     NixClosureModeVerify,
		NixStore: fake.path,
		StoreDir: "/nix/store",
		MaxJobs:  -1,
		Cores:    -1,
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected verify mode to report converged when path is already valid")
	}
}

func TestNixClosureResCheckApplySubstituteMaterializesPath(t *testing.T) {
	t.Parallel()

	fake := newFakeNixStore(t)
	root := "/nix/store/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-grafana"
	fake.addRealizeMapping(root, root)

	res := &NixClosureRes{
		Paths:          []string{root},
		Mode:           NixClosureModeSubstitute,
		KeepGoing:      true,
		NixStore:       fake.path,
		StoreDir:       "/nix/store",
		MaxJobs:        -1,
		Cores:          -1,
		CommandTimeout: 30,
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected first apply to report non-converged after materializing path")
	}

	checkOK, err = res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("second checkapply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected substituted path to be converged on second check")
	}
}

func TestNixClosureResCheckApplyRealiseDrvOutputs(t *testing.T) {
	t.Parallel()

	fake := newFakeNixStore(t)
	drv := "/nix/store/cccccccccccccccccccccccccccccccc-prometheus-local.drv"
	out := "/nix/store/dddddddddddddddddddddddddddddddd-prometheus-local"
	fake.addDrvOutputs(drv, out)
	fake.addRealizeMapping(drv, out)

	res := &NixClosureRes{
		Drvs:     []string{drv},
		Mode:     NixClosureModeRealise,
		NixStore: fake.path,
		StoreDir: "/nix/store",
		MaxJobs:  1,
		Cores:    1,
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected first apply to report non-converged after realising drv outputs")
	}

	checkOK, err = res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("second checkapply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected drv outputs to be converged after realisation")
	}
}

type fakeNixStore struct {
	path    string
	valid   string
	outputs string
	realize string
	log     string
}

func newFakeNixStore(t *testing.T) *fakeNixStore {
	t.Helper()
	dir := t.TempDir()
	fake := &fakeNixStore{
		path:    filepath.Join(dir, "nix-store"),
		valid:   filepath.Join(dir, "valid.txt"),
		outputs: filepath.Join(dir, "outputs.tsv"),
		realize: filepath.Join(dir, "realize.tsv"),
		log:     filepath.Join(dir, "calls.log"),
	}
	for _, file := range []string{fake.valid, fake.outputs, fake.realize, fake.log} {
		if err := os.WriteFile(file, nil, 0600); err != nil {
			t.Fatalf("failed to initialize %s: %v", file, err)
		}
	}

	script := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
VALID=%q
OUTPUTS=%q
REALIZE=%q
LOG=%q

printf "%%s\n" "$*" >> "$LOG"

has_valid() {
  local needle="$1"
  grep -Fxq "$needle" "$VALID"
}

mark_valid() {
  local value="$1"
  if ! has_valid "$value"; then
    printf "%%s\n" "$value" >> "$VALID"
  fi
}

query_outputs() {
  local drv="$1"
  local line
  line="$(awk -F '\t' -v drv="$drv" '$1 == drv { print $2 }' "$OUTPUTS")"
  if [[ -z "$line" ]]; then
    exit 1
  fi
  for output in $line; do
    printf "%%s\n" "$output"
  done
}

query_requisites() {
  local root
  for root in "$@"; do
    if ! has_valid "$root"; then
      exit 1
    fi
  done
  for root in "$@"; do
    printf "%%s\n" "$root"
  done
}

verify_paths() {
  local root
  for root in "$@"; do
    if ! has_valid "$root"; then
      exit 1
    fi
  done
}

realise() {
  local inputs=()
  while (($# > 0)); do
    case "$1" in
      --keep-going|--ignore-unknown)
        shift
        ;;
      --max-jobs|--cores|--timeout|--max-silent-time)
        shift 2
        ;;
      --option)
        shift 3
        ;;
      *)
        inputs+=("$1")
        shift
        ;;
    esac
  done

  local failures=0
  local input
  for input in "${inputs[@]}"; do
    local line
    line="$(awk -F '\t' -v input="$input" '$1 == input { print $2 }' "$REALIZE")"
    if [[ -z "$line" ]]; then
      failures=1
      continue
    fi
    for output in $line; do
      mark_valid "$output"
      printf "%%s\n" "$output"
    done
  done
  if [[ "$failures" -ne 0 ]]; then
    exit 1
  fi
}

case "${1-}" in
  --query)
    shift
    case "${1-}" in
      --outputs)
        shift
        query_outputs "$1"
        ;;
      --requisites)
        shift
        query_requisites "$@"
        ;;
      *)
        exit 1
        ;;
    esac
    ;;
  --verify-path)
    shift
    verify_paths "$@"
    ;;
  --realise)
    shift
    realise "$@"
    ;;
  *)
    exit 1
    ;;
esac
`, fake.valid, fake.outputs, fake.realize, fake.log)

	if err := os.WriteFile(fake.path, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake nix-store: %v", err)
	}
	return fake
}

func (f *fakeNixStore) markValid(path string) {
	appendUniqueLine(f.valid, path)
}

func (f *fakeNixStore) addDrvOutputs(drv string, outputs ...string) {
	appendMappingLine(f.outputs, drv, outputs...)
}

func (f *fakeNixStore) addRealizeMapping(input string, outputs ...string) {
	appendMappingLine(f.realize, input, outputs...)
}

func appendUniqueLine(path string, value string) {
	data, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(data), "\n") {
		if line == value {
			return
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "%s\n", value); err != nil {
		panic(err)
	}
}

func appendMappingLine(path string, key string, values ...string) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "%s\t%s\n", key, strings.Join(values, " ")); err != nil {
		panic(err)
	}
}
