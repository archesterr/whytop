package remote

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DefaultConfigPath is ~/.ssh/config, the file ssh itself reads. Reading it
// means the hosts someone has already set up are simply there — being asked
// to retype a host list you have maintained for years is the fastest way to
// make a tool feel like it doesn't belong on your machine.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "config")
}

// LoadConfig reads an ssh_config and returns the hosts it names.
//
// This reads the subset of the format whytop can act on — HostName, User,
// Port, IdentityFile — and deliberately does not pretend to implement the
// rest. A Host entry using ProxyJump, Match blocks or anything else it
// doesn't understand is still listed, with whatever it did understand,
// rather than being hidden: a host that appears and then fails to connect
// with a clear error is more useful than one that silently isn't there.
//
// Patterns (Host *.example.com) are skipped, since they name no single
// host to connect to.
func LoadConfig(path string) ([]Host, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var hosts []Host
	var cur []*Host
	add := func(h *Host) {
		if h.Addr == "" {
			h.Addr = h.Name
		}
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val := splitConfigLine(line)
		switch strings.ToLower(key) {
		case "host":
			for _, h := range cur {
				add(h)
				hosts = append(hosts, *h)
			}
			cur = nil
			for _, name := range strings.Fields(val) {
				// A pattern names a class of hosts, not one to connect to.
				if strings.ContainsAny(name, "*?!") {
					continue
				}
				cur = append(cur, &Host{Name: name, FromConfig: true})
			}
		case "hostname":
			for _, h := range cur {
				h.Addr = val
			}
		case "user":
			for _, h := range cur {
				h.User = val
			}
		case "port":
			if n, err := strconv.Atoi(val); err == nil {
				for _, h := range cur {
					h.Port = n
				}
			}
		case "identityfile":
			for _, h := range cur {
				if h.IdentityFile == "" { // ssh uses the first that works; so do we
					h.IdentityFile = expandHome(val)
				}
			}
		}
	}
	for _, h := range cur {
		add(h)
		hosts = append(hosts, *h)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].Name < hosts[j].Name })
	return hosts, nil
}

// splitConfigLine handles both "Key value" and "Key=value", which
// ssh_config accepts interchangeably.
func splitConfigLine(line string) (key, val string) {
	if i := strings.IndexAny(line, " \t="); i >= 0 {
		return line[:i], strings.TrimSpace(strings.TrimLeft(line[i:], " \t="))
	}
	return line, ""
}
