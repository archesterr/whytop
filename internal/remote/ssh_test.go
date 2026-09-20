package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Everything above this is parser tests against captured output. This one
// drives the real client — dial, authenticate, verify the host key, open a
// session, run the probe, read it back — against an SSH server running in
// the test process. Without it the whole remote path is only tested from
// the string-parsing inwards, and connecting is where the things that
// actually go wrong live.
func TestSnapshotOverARealSSHSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "") // force the key-file path, not a stray agent
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)

	clientKey := writeClientKey(t, home)
	hostSigner := mustSigner(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	// known_hosts must contain the server's key, because the client refuses
	// unknown hosts — see hostKeyCallback, which is never
	// InsecureIgnoreHostKey.
	line := knownHostsLine("127.0.0.1", port, hostSigner.PublicKey())
	os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line), 0o600)

	go serveOnce(t, ln, hostSigner, clientKey.PublicKey())

	c, err := Dial(Host{Name: "test", Addr: "127.0.0.1", Port: port, User: "tester"}, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	snap, err := c.Snapshot(5 * time.Second)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Host != "web01" {
		t.Errorf("host = %q, want web01", snap.Host)
	}
	if len(snap.Procs) != 2 {
		t.Fatalf("got %d processes, want 2", len(snap.Procs))
	}
	if snap.Mem.Total == 0 {
		t.Error("memory was not read")
	}
	if !snap.PSI.Available {
		t.Error("pressure was not read")
	}
}

// An unknown host must be refused, and the error has to say what to do
// about it. Silently trusting any key would make every one of these
// sessions interceptable, and "it's only monitoring" is not a defence —
// the session carries your credentials.
func TestUnknownHostKeyIsRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	writeClientKey(t, home)
	os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(""), 0o600)

	hostSigner := mustSigner(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	go serveOnce(t, ln, hostSigner, nil)

	_, err := Dial(Host{Addr: "127.0.0.1", Port: port, User: "tester"}, 5*time.Second)
	if err == nil {
		t.Fatal("an unknown host key was accepted")
	}
	if !strings.Contains(err.Error(), "known_hosts") {
		t.Errorf("error should point at known_hosts, got: %v", err)
	}
	if !strings.Contains(err.Error(), "SHA256:") {
		t.Errorf("error should quote the fingerprint so it can be checked, got: %v", err)
	}
}

func writeClientKey(t *testing.T, home string) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".ssh", "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(b), 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func mustSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func knownHostsLine(host string, port int, key ssh.PublicKey) string {
	return fmt.Sprintf("[%s]:%d %s %s\n", host, port, key.Type(),
		strings.TrimSpace(strings.SplitN(string(ssh.MarshalAuthorizedKey(key)), " ", 3)[1]))
}

// serveOnce accepts one connection, accepts any public key, and answers an
// exec request with the probe fixture — the same bytes a real host's /proc
// would produce.
func serveOnce(t *testing.T, ln net.Listener, hostKey ssh.Signer, _ ssh.PublicKey) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(hostKey)
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return // the host-key test never gets this far, which is the point
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "no")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			for req := range creqs {
				if req.Type == "exec" {
					req.Reply(true, nil)
					ch.Write([]byte(probeFixture(100, 1000)))
					ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
					ch.Close()
					continue
				}
				req.Reply(false, nil)
			}
		}()
	}
}
