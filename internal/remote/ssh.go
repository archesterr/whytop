// Package remote reads a remote host's /proc over SSH.
//
// Nothing is installed on, uploaded to, or left behind on the remote host:
// whytop works on any box you can already SSH into, and a monitoring tool
// that writes an executable into /tmp on every server you point it at is a
// different kind of tool than this one. The cost is that the remote
// collector is a second implementation reading raw /proc rather than
// gopsutil, so a few fields local hosts have (container IDs, systemd unit
// attribution, block-device stats) are not available remotely yet — the
// columns say so rather than showing zeros.
package remote

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/archesterr/whytop/internal/collect"
)

// Host is one SSH destination, as configured rather than as connected.
type Host struct {
	// Name is what the operator calls it — an ssh_config Host alias, or
	// just the address when they typed one in.
	Name string
	Addr string
	Port int
	User string
	// IdentityFile is an explicit private key. Empty means "whatever the
	// agent and the usual key paths offer", which is what ssh itself does.
	IdentityFile string
	// FromConfig marks hosts read out of an ssh_config, so the UI can say
	// where a host came from and not offer to delete one it doesn't own.
	FromConfig bool
}

func (h Host) addr() string {
	port := h.Port
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(h.Addr, strconv.Itoa(port))
}

// Label is how the host is named on screen.
func (h Host) Label() string {
	if h.Name != "" {
		return h.Name
	}
	return h.Addr
}

// Client is a live connection to one host. It is not safe for concurrent
// use: Snapshot keeps the previous sample to turn /proc's cumulative
// counters into rates, so two callers would differ against each other's.
type Client struct {
	host     Host
	conn     *ssh.Client
	prev     *prev
	prevSock map[string]collect.SockStat

	mu     sync.Mutex
	closed bool
}

// Dial connects and authenticates. Password authentication is deliberately
// not supported: keys and the agent cover every host worth monitoring, and
// the alternative is whytop holding a password in memory — or worse, on
// disk — for every box on the list.
func Dial(h Host, timeout time.Duration) (*Client, error) {
	auths, err := authMethods(h)
	if err != nil {
		return nil, err
	}
	if len(auths) == 0 {
		return nil, errors.New("no SSH keys found: start ssh-agent, or set an identity file for this host")
	}
	hk, err := hostKeyCallback()
	if err != nil {
		return nil, err
	}
	user := h.User
	if user == "" {
		user = os.Getenv("USER")
	}
	conn, err := ssh.Dial("tcp", h.addr(), &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: hk,
		Timeout:         timeout,
	})
	if err != nil {
		return nil, err
	}
	return &Client{host: h, conn: conn}, nil
}

func (c *Client) Host() Host { return c.host }

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.conn.Close()
}

// Snapshot runs one probe and parses it. It opens a fresh session per tick:
// the TCP connection and the key exchange are already paid for, so a
// session is one channel-open, and a persistent shell would mean framing
// and recovering from a remote command that hangs.
func (c *Client) Snapshot(timeout time.Duration) (*collect.Snapshot, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	var out, errOut bytes.Buffer
	sess.Stdout, sess.Stderr = &out, &errOut

	done := make(chan error, 1)
	if err := sess.Start(probeScript); err != nil {
		return nil, err
	}
	go func() { done <- sess.Wait() }()

	select {
	case err := <-done:
		// A non-zero exit is expected and fine: the probe reads files that
		// may not exist (/proc/pressure on an old kernel) and the last
		// command's status is whatever that happened to be. What matters is
		// whether the output arrived.
		if err != nil && out.Len() == 0 {
			return nil, fmt.Errorf("probe failed: %w: %s", err, firstLine(errOut.String()))
		}
	case <-time.After(timeout):
		sess.Signal(ssh.SIGKILL)
		return nil, errors.New("timed out waiting for the remote host")
	}

	body := out.String()
	if !bytes.Contains(out.Bytes(), []byte("@@end")) {
		return nil, errors.New("the remote host returned a truncated reading")
	}
	return c.build(body, time.Now()), nil
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

// authMethods offers the agent first, then explicit and conventional key
// files — the same order and the same places ssh itself looks, so a host
// that works with `ssh host` works here without extra configuration.
func authMethods(h Host) ([]ssh.AuthMethod, error) {
	var auths []ssh.AuthMethod
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}
	paths := []string{h.IdentityFile}
	if h.IdentityFile == "" {
		home, _ := os.UserHomeDir()
		for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			paths = append(paths, filepath.Join(home, ".ssh", name))
		}
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		key, err := os.ReadFile(expandHome(p))
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			// An encrypted key without an agent can't be used, and saying
			// so beats a bare "permission denied" from the far end.
			if _, ok := err.(*ssh.PassphraseMissingError); ok && h.IdentityFile == p {
				return nil, fmt.Errorf("%s needs a passphrase: add it with ssh-add and try again", p)
			}
			continue
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	return auths, nil
}

// hostKeyCallback verifies against ~/.ssh/known_hosts.
//
// It is never InsecureIgnoreHostKey. whytop connects to production boxes
// and reads their process tables; accepting any key silently would make
// every one of those sessions trivially interceptable, and "it's only
// monitoring" is not a reason — the session carries your credentials.
// An unknown host is refused with the same advice ssh gives, because the
// fix is to verify the fingerprint yourself, not to have whytop skip it.
func hostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".ssh", "known_hosts")
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w — connect once with ssh first so the host key is recorded", path, err)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if err := cb(hostname, remote, key); err != nil {
			var ke *knownhosts.KeyError
			if errors.As(err, &ke) && len(ke.Want) == 0 {
				return fmt.Errorf("host key for %s is not in known_hosts (%s). Connect once with ssh, check the fingerprint, then retry",
					hostname, ssh.FingerprintSHA256(key))
			}
			return err
		}
		return nil
	}, nil
}

func expandHome(p string) string {
	if len(p) > 1 && p[0] == '~' && (p[1] == '/' || p[1] == os.PathSeparator) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
