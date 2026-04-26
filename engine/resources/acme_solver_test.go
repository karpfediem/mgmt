package resources

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/purpleidea/mgmt/engine"
)

var acmeTestNotExist = errors.New("not found")

type acmeMemoryWorld struct {
	hostname string
	mu       sync.Mutex
	strings  map[string]string
	maps     map[string]map[string]string
}

func newAcmeMemoryWorld(hostname string) *acmeMemoryWorld {
	return &acmeMemoryWorld{
		hostname: hostname,
		strings:  make(map[string]string),
		maps:     make(map[string]map[string]string),
	}
}

func (obj *acmeMemoryWorld) Connect(context.Context, *engine.WorldInit) error { return nil }
func (obj *acmeMemoryWorld) Cleanup() error                                   { return nil }
func (obj *acmeMemoryWorld) URI() string                                      { return "" }
func (obj *acmeMemoryWorld) Fs(context.Context, string) (engine.Fs, error)    { return nil, nil }
func (obj *acmeMemoryWorld) WatchDeploy(context.Context) (chan error, error) {
	return make(chan error), nil
}
func (obj *acmeMemoryWorld) GetDeploy(context.Context, uint64) (string, error) { return "", nil }
func (obj *acmeMemoryWorld) GetMaxDeployID(context.Context) (uint64, error)    { return 0, nil }
func (obj *acmeMemoryWorld) AddDeploy(context.Context, uint64, string, string, *string) error {
	return nil
}
func (obj *acmeMemoryWorld) StrWatch(context.Context, string) (chan error, error) {
	return make(chan error), nil
}
func (obj *acmeMemoryWorld) StrIsNotExist(err error) bool {
	return errors.Is(err, acmeTestNotExist)
}
func (obj *acmeMemoryWorld) StrGet(ctx context.Context, namespace string) (string, error) {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	value, ok := obj.strings[namespace]
	if !ok {
		return "", acmeTestNotExist
	}
	return value, nil
}
func (obj *acmeMemoryWorld) StrSet(ctx context.Context, namespace, value string) error {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	obj.strings[namespace] = value
	return nil
}
func (obj *acmeMemoryWorld) StrDel(ctx context.Context, namespace string) error {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	delete(obj.strings, namespace)
	return nil
}
func (obj *acmeMemoryWorld) StrMapWatch(context.Context, string) (chan error, error) {
	return make(chan error), nil
}
func (obj *acmeMemoryWorld) StrMapGet(ctx context.Context, namespace string) (map[string]string, error) {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	out := make(map[string]string)
	for key, value := range obj.maps[namespace] {
		out[key] = value
	}
	return out, nil
}
func (obj *acmeMemoryWorld) StrMapSet(ctx context.Context, namespace, value string) error {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	if obj.maps[namespace] == nil {
		obj.maps[namespace] = make(map[string]string)
	}
	obj.maps[namespace][obj.hostname] = value
	return nil
}
func (obj *acmeMemoryWorld) StrMapDel(ctx context.Context, namespace string) error {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	delete(obj.maps[namespace], obj.hostname)
	return nil
}
func (obj *acmeMemoryWorld) ResWatch(context.Context, string) (chan error, error) {
	return make(chan error), nil
}
func (obj *acmeMemoryWorld) ResCollect(context.Context, []*engine.ResFilter) ([]*engine.ResOutput, error) {
	return nil, nil
}
func (obj *acmeMemoryWorld) ResExport(context.Context, []*engine.ResExport) (bool, error) {
	return true, nil
}
func (obj *acmeMemoryWorld) ResDelete(context.Context, []*engine.ResDelete) (bool, error) {
	return true, nil
}

func acmeTestInit() *engine.Init {
	return &engine.Init{
		Hostname: "test-host",
		Logf:     func(format string, v ...interface{}) {},
	}
}

func acmeTestSpec(t *testing.T, domains ...string) *acmeCertSpec {
	t.Helper()
	spec, err := acmeBuildSpec(domains, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestAcmeCertificateCheckDoesNotWrite(t *testing.T) {
	world := newAcmeMemoryWorld("test-host")
	init := acmeTestInit()
	init.World = world
	res := &AcmeCertificateRes{
		Domains: []string{"example.com"},
	}
	res.SetKind(KindAcmeCertificate)
	res.SetName("test-cert")
	if err := res.Init(init); err != nil {
		t.Fatal(err)
	}

	checkOK, err := res.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if checkOK {
		t.Fatalf("certificate unexpectedly checked clean before writing request")
	}
	if len(world.strings) != 0 {
		t.Fatalf("check wrote string keys: %+v", world.strings)
	}
	if len(world.maps) != 0 {
		t.Fatalf("check wrote map keys: %+v", world.maps)
	}

	checkOK, err = res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if checkOK {
		t.Fatalf("first apply unexpectedly checked clean")
	}
	if len(world.maps[acmeRequestIndexKey(res.requestNamespace())]) == 0 {
		t.Fatalf("apply did not index certificate request")
	}
	if _, ok := world.strings[acmeSpecKey(res.namespace())]; ok {
		t.Fatalf("first apply wrote spec before index was converged")
	}

	checkOK, err = res.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if checkOK {
		t.Fatalf("second apply unexpectedly checked clean before bundle exists")
	}
	if _, ok := world.strings[acmeSpecKey(res.namespace())]; !ok {
		t.Fatalf("second apply did not write spec")
	}
}

func TestAcmeListenPort(t *testing.T) {
	for _, test := range []struct {
		address string
		port    int
	}{
		{":80", 80},
		{":443", 443},
		{"127.0.0.1:5002", 5002},
		{"localhost:https", 0},
	} {
		if got := acmeListenPort(test.address); got != test.port {
			t.Fatalf("acmeListenPort(%q) = %d, expected %d", test.address, got, test.port)
		}
	}
}

func TestAcmeHTTP01Eligibility(t *testing.T) {
	res := &AcmeSolverHTTP01Res{Hosts: []string{"example.com", "www.example.com"}}
	for _, test := range []struct {
		name    string
		spec    *acmeCertSpec
		want    bool
		wantErr bool
	}{
		{
			name: "all hosts allowed",
			spec: acmeTestSpec(t, "example.com", "www.example.com"),
			want: true,
		},
		{
			name: "wildcard skipped",
			spec: acmeTestSpec(t, "*.example.com"),
		},
		{
			name: "unknown host skipped",
			spec: acmeTestSpec(t, "api.example.com"),
		},
	} {
		got, err := res.canSolve(test.spec)
		if (err != nil) != test.wantErr {
			t.Fatalf("%s: err = %v, wantErr = %t", test.name, err, test.wantErr)
		}
		if got != test.want {
			t.Fatalf("%s: canSolve = %t, expected %t", test.name, got, test.want)
		}
	}
}

func TestAcmeTLSALPN01Eligibility(t *testing.T) {
	res := &AcmeSolverTLSALPN01Res{ServerNames: []string{"example.com", "www.example.com"}}
	for _, test := range []struct {
		name string
		spec *acmeCertSpec
		want bool
	}{
		{
			name: "all names allowed",
			spec: acmeTestSpec(t, "example.com", "www.example.com"),
			want: true,
		},
		{
			name: "wildcard skipped",
			spec: acmeTestSpec(t, "*.example.com"),
		},
		{
			name: "unknown name skipped",
			spec: acmeTestSpec(t, "api.example.com"),
		},
	} {
		got, err := res.canSolve(test.spec)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", test.name, err)
		}
		if got != test.want {
			t.Fatalf("%s: canSolve = %t, expected %t", test.name, got, test.want)
		}
	}
}

func TestAcmeSelfPresentingSolversSkipBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	address := ln.Addr().String()

	httpRes := &AcmeSolverHTTP01Res{Listen: address}
	if err := httpRes.Init(acmeTestInit()); err != nil {
		t.Fatal(err)
	}
	cleanup, eligible, err := httpRes.prepare(context.Background())
	if err != nil {
		t.Fatalf("http-01 prepare returned unexpected error: %v", err)
	}
	if eligible {
		t.Fatalf("http-01 solver was eligible with busy listen address %s", address)
	}
	if cleanup != nil {
		t.Fatalf("http-01 solver returned cleanup for ineligible listen address")
	}

	tlsRes := &AcmeSolverTLSALPN01Res{Listen: address}
	if err := tlsRes.Init(acmeTestInit()); err != nil {
		t.Fatal(err)
	}
	cleanup, eligible, err = tlsRes.prepare(context.Background())
	if err != nil {
		t.Fatalf("tls-alpn-01 prepare returned unexpected error: %v", err)
	}
	if eligible {
		t.Fatalf("tls-alpn-01 solver was eligible with busy listen address %s", address)
	}
	if cleanup != nil {
		t.Fatalf("tls-alpn-01 solver returned cleanup for ineligible listen address")
	}
}

func TestAcmeSelfPresentingSolverCleanup(t *testing.T) {
	httpRes := &AcmeSolverHTTP01Res{Listen: "127.0.0.1:0"}
	if err := httpRes.Init(acmeTestInit()); err != nil {
		t.Fatal(err)
	}
	cleanup, eligible, err := httpRes.prepare(context.Background())
	if err != nil {
		t.Fatalf("http-01 prepare returned unexpected error: %v", err)
	}
	if !eligible {
		t.Fatalf("http-01 solver was ineligible on an ephemeral listen address")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cleanup(ctx); err != nil {
		t.Fatalf("http-01 cleanup failed: %v", err)
	}

	tlsRes := &AcmeSolverTLSALPN01Res{Listen: "127.0.0.1:0"}
	if err := tlsRes.Init(acmeTestInit()); err != nil {
		t.Fatal(err)
	}
	cleanup, eligible, err = tlsRes.prepare(context.Background())
	if err != nil {
		t.Fatalf("tls-alpn-01 prepare returned unexpected error: %v", err)
	}
	if !eligible {
		t.Fatalf("tls-alpn-01 solver was ineligible on an ephemeral listen address")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cleanup(ctx); err != nil {
		t.Fatalf("tls-alpn-01 cleanup failed: %v", err)
	}
}
