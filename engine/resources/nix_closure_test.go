package resources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/purpleidea/mgmt/engine"
)

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "nix-store" {
		os.Exit(runFakeNixStore(os.Args[1:]))
	}
	os.Exit(m.Run())
}

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

func TestNixClosureResValidateRealizeAlias(t *testing.T) {
	t.Parallel()

	res := &NixClosureRes{
		Paths:    []string{"/nix/store/abababababababababababababababab-demo"},
		Mode:     NixClosureModeRealize,
		StoreDir: "/nix/store",
		MaxJobs:  -1,
		Cores:    -1,
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if res.Mode != NixClosureModeRealise {
		t.Fatalf("expected realize alias to canonicalize to %q, got %q", NixClosureModeRealise, res.Mode)
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

func TestNixClosureResCheckApplyRefreshOnlyChecksValidRoots(t *testing.T) {
	t.Parallel()

	fake := newFakeNixStore(t)
	root := "/nix/store/12121212121212121212121212121212-refresh-root"
	fake.markValid(root)
	fake.addRealizeMapping(root, root)

	res := &NixClosureRes{
		Paths:    []string{root},
		Mode:     NixClosureModeSubstitute,
		NixStore: fake.path,
		StoreDir: "/nix/store",
		MaxJobs:  -1,
		Cores:    -1,
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if err := res.Init(&engine.Init{Refresh: func() bool { return true }}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected refresh to report converged when roots are already valid")
	}
	if fake.called("--realise") {
		t.Fatalf("refresh should not force realisation when roots are already valid; calls: %v", fake.calls())
	}
	if !fake.called("--query --hash " + root) {
		t.Fatalf("expected a cheap root hash query; calls: %v", fake.calls())
	}
}

func TestNixClosureResCheckApplyCheckContentsQueriesClosure(t *testing.T) {
	t.Parallel()

	fake := newFakeNixStore(t)
	root := "/nix/store/34343434343434343434343434343434-check-contents"
	fake.markValid(root)

	res := &NixClosureRes{
		Paths:         []string{root},
		Mode:          NixClosureModeVerify,
		CheckContents: true,
		NixStore:      fake.path,
		StoreDir:      "/nix/store",
		MaxJobs:       -1,
		Cores:         -1,
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected content check to report converged when root and closure are valid")
	}
	if !fake.called("--query --requisites " + root) {
		t.Fatalf("expected full closure query when check_contents is enabled; calls: %v", fake.calls())
	}
	if !fake.called("--verify-path " + root) {
		t.Fatalf("expected content verification when check_contents is enabled; calls: %v", fake.calls())
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

func TestNixClosureResCheckApplyVerifyMixedInputsMissingDrvOutputNotConverged(t *testing.T) {
	t.Parallel()

	fake := newFakeNixStore(t)
	path := "/nix/store/eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee-static-assets"
	drv := "/nix/store/ffffffffffffffffffffffffffffffff-app.drv"
	out := "/nix/store/11111111111111111111111111111111-app"
	fake.markValid(path)
	fake.addDrvOutputs(drv, out)

	res := &NixClosureRes{
		Paths:    []string{path},
		Drvs:     []string{drv},
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
	if checkOK {
		t.Fatalf("expected mixed path+drv inputs to stay non-converged when the drv output is absent")
	}
}

func TestNixClosureResCheckApplyVerifyMixedInputsPresent(t *testing.T) {
	t.Parallel()

	fake := newFakeNixStore(t)
	path := "/nix/store/22222222222222222222222222222222-static-assets"
	drv := "/nix/store/33333333333333333333333333333333-app.drv"
	out := "/nix/store/44444444444444444444444444444444-app"
	fake.markValid(path)
	fake.markValid(out)
	fake.addDrvOutputs(drv, out)

	res := &NixClosureRes{
		Paths:    []string{path},
		Drvs:     []string{drv},
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
		t.Fatalf("expected mixed path+drv inputs to converge when both roots are present")
	}
}

func TestNixClosureResCheckApplyVerifyFallsBackToName(t *testing.T) {
	t.Parallel()

	t.Run("path", func(t *testing.T) {
		t.Parallel()

		fake := newFakeNixStore(t)
		root := "/nix/store/55555555555555555555555555555555-name-fallback"
		fake.markValid(root)

		res := &NixClosureRes{
			Mode:     NixClosureModeVerify,
			NixStore: fake.path,
			StoreDir: "/nix/store",
			MaxJobs:  -1,
			Cores:    -1,
		}
		res.SetName(root)
		if err := res.Validate(); err != nil {
			t.Fatalf("validate failed: %v", err)
		}

		checkOK, err := res.CheckApply(context.Background(), false)
		if err != nil {
			t.Fatalf("checkapply failed: %v", err)
		}
		if !checkOK {
			t.Fatalf("expected store-path resource name to act as the fallback root")
		}
	})

	t.Run("drv", func(t *testing.T) {
		t.Parallel()

		fake := newFakeNixStore(t)
		drv := "/nix/store/66666666666666666666666666666666-name-fallback.drv"
		out := "/nix/store/77777777777777777777777777777777-name-fallback"
		fake.markValid(out)
		fake.addDrvOutputs(drv, out)

		res := &NixClosureRes{
			Mode:     NixClosureModeVerify,
			NixStore: fake.path,
			StoreDir: "/nix/store",
			MaxJobs:  -1,
			Cores:    -1,
		}
		res.SetName(drv)
		if err := res.Validate(); err != nil {
			t.Fatalf("validate failed: %v", err)
		}

		checkOK, err := res.CheckApply(context.Background(), false)
		if err != nil {
			t.Fatalf("checkapply failed: %v", err)
		}
		if !checkOK {
			t.Fatalf("expected derivation resource name to fall back through its resolved outputs")
		}
	})
}

func TestNixClosureResCheckApplyVerifyExplicitInputsSuppressNameFallback(t *testing.T) {
	t.Parallel()

	fake := newFakeNixStore(t)
	nameRoot := "/nix/store/88888888888888888888888888888888-name-root"
	drv := "/nix/store/99999999999999999999999999999999-explicit.drv"
	out := "/nix/store/abababababababababababababababab-explicit"
	fake.markValid(nameRoot)
	fake.addDrvOutputs(drv, out)

	res := &NixClosureRes{
		Drvs:     []string{drv},
		Mode:     NixClosureModeVerify,
		NixStore: fake.path,
		StoreDir: "/nix/store",
		MaxJobs:  -1,
		Cores:    -1,
	}
	res.SetName(nameRoot)
	if err := res.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected explicit drvs to suppress name fallback when their outputs are still absent")
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

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("failed to locate test binary: %v", err)
	}
	if err := os.Symlink(exe, fake.path); err != nil {
		t.Fatalf("failed to create fake nix-store: %v", err)
	}
	return fake
}

func runFakeNixStore(args []string) int {
	dir := filepath.Dir(os.Args[0])
	fake := fakeNixStore{
		valid:   filepath.Join(dir, "valid.txt"),
		outputs: filepath.Join(dir, "outputs.tsv"),
		realize: filepath.Join(dir, "realize.tsv"),
		log:     filepath.Join(dir, "calls.log"),
	}
	appendLine(fake.log, strings.Join(args, " "))

	args = stripFakeNixOptions(args)
	if len(args) == 0 {
		return 1
	}

	switch args[0] {
	case "--query":
		return runFakeNixQuery(fake, args[1:])
	case "--verify-path":
		for _, root := range args[1:] {
			if !fake.hasValid(root) {
				return 1
			}
		}
		return 0
	case "--realise":
		return runFakeNixRealise(fake, args[1:])
	default:
		return 1
	}
}

func stripFakeNixOptions(args []string) []string {
	for len(args) >= 3 && args[0] == "--option" {
		args = args[3:]
	}
	return args
}

func runFakeNixQuery(fake fakeNixStore, args []string) int {
	if len(args) == 0 {
		return 1
	}
	switch args[0] {
	case "--hash":
		for _, root := range args[1:] {
			if !fake.hasValid(root) {
				return 1
			}
		}
		for _, root := range args[1:] {
			fmt.Println("sha256:" + filepath.Base(root))
		}
		return 0
	case "--outputs":
		if len(args) != 2 {
			return 1
		}
		outputs, ok := fake.mapping(fake.outputs, args[1])
		if !ok {
			return 1
		}
		for _, output := range outputs {
			fmt.Println(output)
		}
		return 0
	case "--requisites":
		for _, root := range args[1:] {
			if !fake.hasValid(root) {
				return 1
			}
		}
		for _, root := range args[1:] {
			fmt.Println(root)
		}
		return 0
	default:
		return 1
	}
}

func runFakeNixRealise(fake fakeNixStore, args []string) int {
	inputs := []string{}
	for len(args) > 0 {
		switch args[0] {
		case "--keep-going", "--ignore-unknown":
			args = args[1:]
		case "--max-jobs", "--cores", "--timeout", "--max-silent-time":
			if len(args) < 2 {
				return 1
			}
			args = args[2:]
		case "--option":
			if len(args) < 3 {
				return 1
			}
			args = args[3:]
		default:
			inputs = append(inputs, args[0])
			args = args[1:]
		}
	}

	failures := false
	for _, input := range inputs {
		outputs, ok := fake.mapping(fake.realize, input)
		if !ok {
			failures = true
			continue
		}
		for _, output := range outputs {
			fake.markValid(output)
			fmt.Println(output)
		}
	}
	if failures {
		return 1
	}
	return 0
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

func (f *fakeNixStore) calls() []string {
	data, _ := os.ReadFile(f.log)
	return nixParseLines(string(data))
}

func (f *fakeNixStore) called(needle string) bool {
	for _, call := range f.calls() {
		if strings.Contains(call, needle) {
			return true
		}
	}
	return false
}

func (f *fakeNixStore) hasValid(path string) bool {
	data, _ := os.ReadFile(f.valid)
	for _, line := range strings.Split(string(data), "\n") {
		if line == path {
			return true
		}
	}
	return false
}

func (f *fakeNixStore) mapping(path string, key string) ([]string, bool) {
	data, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || parts[0] != key {
			continue
		}
		return strings.Fields(parts[1]), true
	}
	return nil, false
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

func appendLine(path string, value string) {
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
