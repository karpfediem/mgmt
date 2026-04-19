package ssh

import (
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
`)
	systemConfig := decodeSSHConfig(t, `
Host mgmt-root
  HostName 198.51.100.10
  User admin
  Port 2222
  IdentityFile /tmp/system-key
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
}
