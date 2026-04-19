package ssh

import (
	"reflect"
	"strings"
	"testing"

	sshconfig "github.com/kevinburke/ssh_config"
)

func decodeSSHConfig(t *testing.T, data string) *sshconfig.Config {
	t.Helper()

	config, err := sshconfig.Decode(strings.NewReader(data))
	if err != nil {
		t.Fatalf("failed to decode config: %v", err)
	}
	return config
}

func TestResolveSSHConfigValuesUserOverridesSystem(t *testing.T) {
	userConfig := decodeSSHConfig(t, `
Host mgmt-root
  HostName 203.0.113.10
  User root
  IdentityFile /tmp/user-key
  UserKnownHostsFile /tmp/user-known_hosts /tmp/user-known_hosts-2
  IdentitiesOnly yes
`)
	systemConfig := decodeSSHConfig(t, `
Host mgmt-root
  HostName 198.51.100.10
  User admin
  Port 2222
  IdentityFile /tmp/system-key
  GlobalKnownHostsFile /tmp/system-known_hosts
`)

	values, err := resolveSSHConfigValues("mgmt-root", userConfig, systemConfig)
	if err != nil {
		t.Fatalf("resolveSSHConfigValues returned error: %v", err)
	}

	if values.HostName != "203.0.113.10" {
		t.Fatalf("unexpected hostname: %q", values.HostName)
	}
	if values.User != "root" {
		t.Fatalf("unexpected user: %q", values.User)
	}
	if values.Port != "2222" {
		t.Fatalf("unexpected port: %q", values.Port)
	}
	if len(values.IdentityFiles) != 1 || values.IdentityFiles[0] != "/tmp/user-key" {
		t.Fatalf("unexpected identity files: %#v", values.IdentityFiles)
	}
	if !reflect.DeepEqual(values.UserKnownHostsFiles, []string{"/tmp/user-known_hosts", "/tmp/user-known_hosts-2"}) {
		t.Fatalf("unexpected user known_hosts files: %#v", values.UserKnownHostsFiles)
	}
	if !reflect.DeepEqual(values.GlobalKnownHostsFiles, []string{"/tmp/system-known_hosts"}) {
		t.Fatalf("unexpected global known_hosts files: %#v", values.GlobalKnownHostsFiles)
	}
	if !values.IdentitiesOnly {
		t.Fatal("expected IdentitiesOnly to be true")
	}
}

func TestResolveSSHConfigValuesUsesSystemFallback(t *testing.T) {
	userConfig := decodeSSHConfig(t, `
Host mgmt-root
  User root
`)
	systemConfig := decodeSSHConfig(t, `
Host mgmt-root
  HostName 198.51.100.10
  Port 2222
`)

	values, err := resolveSSHConfigValues("mgmt-root", userConfig, systemConfig)
	if err != nil {
		t.Fatalf("resolveSSHConfigValues returned error: %v", err)
	}

	if values.HostName != "198.51.100.10" {
		t.Fatalf("unexpected hostname: %q", values.HostName)
	}
	if values.User != "root" {
		t.Fatalf("unexpected user: %q", values.User)
	}
	if values.Port != "2222" {
		t.Fatalf("unexpected port: %q", values.Port)
	}
	if len(values.IdentityFiles) != 0 {
		t.Fatalf("unexpected identity files: %#v", values.IdentityFiles)
	}
	if len(values.UserKnownHostsFiles) != 0 {
		t.Fatalf("unexpected user known_hosts files: %#v", values.UserKnownHostsFiles)
	}
	if len(values.GlobalKnownHostsFiles) != 0 {
		t.Fatalf("unexpected global known_hosts files: %#v", values.GlobalKnownHostsFiles)
	}
	if values.IdentitiesOnly {
		t.Fatal("expected IdentitiesOnly to default to false")
	}
}

func TestSSHConfigValuesKnownHostsCandidatesUseDefaults(t *testing.T) {
	values := sshConfigValues{}

	candidates := values.knownHostsCandidates()
	expected := []string{
		"~/.ssh/known_hosts",
		"~/.ssh/known_hosts2",
		"/etc/ssh/ssh_known_hosts",
		"/etc/ssh/ssh_known_hosts2",
	}

	if !reflect.DeepEqual(candidates, expected) {
		t.Fatalf("unexpected known_hosts candidates: %#v", candidates)
	}
}

func TestSSHConfigValuesAllowDefaultKeyFallback(t *testing.T) {
	tests := []struct {
		name   string
		values sshConfigValues
		want   bool
	}{
		{
			name:   "fallback allowed when identities_only is false",
			values: sshConfigValues{},
			want:   true,
		},
		{
			name: "fallback blocked when identities_only uses configured identities",
			values: sshConfigValues{
				IdentitiesOnly: true,
				IdentityFiles:  []string{"/tmp/user-key"},
			},
			want: false,
		},
		{
			name: "fallback allowed when identities_only has no configured identities",
			values: sshConfigValues{
				IdentitiesOnly: true,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.values.allowDefaultKeyFallback(); got != tt.want {
				t.Fatalf("allowDefaultKeyFallback() = %t, want %t", got, tt.want)
			}
		})
	}
}
