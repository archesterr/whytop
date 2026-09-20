package remote

import (
	"os"
	"path/filepath"
	"testing"
)

// Reading the operator's existing ssh_config is the point: being asked to
// retype a host list you have maintained for years is the fastest way to
// make a tool feel like it doesn't belong on your machine.
func TestLoadConfigReadsTheUsefulSubset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	os.WriteFile(path, []byte(`
# a comment
Host *
  ServerAliveInterval 60

Host web01 web01.internal
  HostName 10.0.0.11
  User deploy
  Port 2222
  IdentityFile ~/.ssh/deploy_ed25519

Host db01
    HostName=10.0.0.20

Host *.staging
  User nobody

Host jump-only
  ProxyJump bastion
`), 0o600)

	hosts, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Host{}
	for _, h := range hosts {
		byName[h.Name] = h
	}

	// A pattern names a class of hosts, not one to connect to.
	if _, ok := byName["*"]; ok {
		t.Error("Host * should not be offered as a host")
	}
	if _, ok := byName["*.staging"]; ok {
		t.Error("a wildcard host should not be offered")
	}

	web := byName["web01"]
	if web.Addr != "10.0.0.11" || web.User != "deploy" || web.Port != 2222 {
		t.Errorf("web01 = %+v", web)
	}
	if web.IdentityFile == "" || filepath.Base(web.IdentityFile) != "deploy_ed25519" {
		t.Errorf("identity file = %q, want the ~ expanded", web.IdentityFile)
	}
	// Several names on one Host line are several hosts.
	if byName["web01.internal"].Addr != "10.0.0.11" {
		t.Errorf("the second alias lost its settings: %+v", byName["web01.internal"])
	}
	// Key=value is as valid as "Key value".
	if byName["db01"].Addr != "10.0.0.20" {
		t.Errorf("db01 = %+v", byName["db01"])
	}
	// A host using directives we don't implement is still listed, with
	// what we did understand — appearing and then failing with a clear
	// error beats silently not being there.
	j, ok := byName["jump-only"]
	if !ok {
		t.Fatal("a host using ProxyJump should still be listed")
	}
	if j.Addr != "jump-only" {
		t.Errorf("a host with no HostName should default to its own name, got %q", j.Addr)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("a missing config should report an error, not an empty list")
	}
}
