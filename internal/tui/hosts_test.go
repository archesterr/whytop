package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/remote"
)

// The single most dangerous thing this tool could do: a PID under the
// cursor belongs to the remote host, and every action runs on the machine
// whytop is running on. Killing with a remote PID would signal whatever
// process happens to hold that number locally.
func TestActionsAreRefusedWhileViewingARemoteHost(t *testing.T) {
	snap := portSnap()
	base := model{snap: snap, detail: &detailState{pid: 1, loaded: true, focus: focusFiles},
		remote: &remote.Client{}}
	for _, key := range []string{"x", "X", "r", "e", "t", "c"} {
		got, _ := base.handleDetailKey(keyRunes(key))
		gm := got.(model)
		if gm.confirm != nil {
			t.Errorf("%q opened a confirm prompt while viewing a remote host — it would act locally", key)
		}
		if !strings.Contains(gm.toast, "machine whytop is running on") {
			t.Errorf("%q should explain why it is disabled, got toast %q", key, gm.toast)
		}
	}
}

// And the footer must not advertise them, because a key that is listed and
// then refuses is a footer people stop reading.
func TestRemoteFooterOffersNoLocalActions(t *testing.T) {
	m := model{snap: portSnap(), width: 200, detail: &detailState{pid: 1, loaded: true},
		remote: &remote.Client{}}
	out := stripANSI(m.renderFooter(200))
	for _, gone := range []string{"stop", "kill", "restart", "edit unit"} {
		if strings.Contains(out, gone) {
			t.Errorf("remote footer still offers %q:\n%s", gone, out)
		}
	}
	local := model{snap: portSnap(), width: 200, detail: &detailState{pid: 1, loaded: true}}
	if out := stripANSI(local.renderFooter(200)); !strings.Contains(out, "stop") {
		t.Errorf("locally the actions should still be offered:\n%s", out)
	}
}

// Switching hosts has to drop everything that was about the old one. A
// selection or a locked order carried across would point at a PID on a
// different box.
func TestSwitchingHostsClearsPerHostState(t *testing.T) {
	m := model{snap: portSnap(), sel: "42", lockOrder: true,
		lockRank: map[int32]int{42: 0}, detail: &detailState{pid: 42}, findingSel: 3}
	m.resetForHost()
	if m.sel != "" || m.detail != nil || m.lockRank != nil || m.findingSel != 0 {
		t.Errorf("state survived a host switch: sel=%q detail=%v lockRank=%v findingSel=%d",
			m.sel, m.detail, m.lockRank, m.findingSel)
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in         string
		user, addr string
		port       int
		wantErr    bool
	}{
		{in: "web01", addr: "web01"},
		{in: "deploy@web01", user: "deploy", addr: "web01"},
		{in: "deploy@web01:2222", user: "deploy", addr: "web01", port: 2222},
		{in: "  web01  ", addr: "web01"},
		{in: "", wantErr: true},
		{in: "web01:notaport", wantErr: true},
		{in: "web01:99999", wantErr: true},
		{in: "user@", wantErr: true},
	}
	for _, c := range cases {
		h, err := parseTarget(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseTarget(%q) should have failed, got %+v", c.in, h)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseTarget(%q): %v", c.in, err)
			continue
		}
		if h.User != c.user || h.Addr != c.addr || h.Port != c.port {
			t.Errorf("parseTarget(%q) = %+v, want user=%q addr=%q port=%d", c.in, h, c.user, c.addr, c.port)
		}
	}
}

// whytop opens on the machine you are sitting on. Asking which host you
// meant before showing anything is how a tool gets closed again.
func TestOpensOnLocalhost(t *testing.T) {
	m := initialModel(Options{})
	if m.remote != nil || m.hosts != nil {
		t.Error("whytop should start on localhost with no host panel in the way")
	}
	if m.hostLabel() != "localhost" {
		t.Errorf("hostLabel = %q", m.hostLabel())
	}
}

// H opens the panel, and the panel always offers localhost to come back to.
func TestHostPanelAlwaysOffersLocalhost(t *testing.T) {
	m := model{snap: portSnap(), width: 100, height: 30}
	got, _ := m.handleListKey(keyRunes("H"))
	gm := got.(model)
	if gm.hosts == nil {
		t.Fatal("H did not open the host panel")
	}
	if len(gm.hosts.hosts) == 0 || gm.hosts.hosts[0].Name != "localhost" {
		t.Fatalf("localhost should be first in the list, got %+v", gm.hosts.hosts)
	}
	out := stripANSI(gm.renderHosts(100, 20))
	if !strings.Contains(out, "localhost") || !strings.Contains(out, "this machine") {
		t.Errorf("the panel should name localhost plainly:\n%s", out)
	}
	// Esc closes it without changing hosts.
	back, _ := gm.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if back.(model).hosts != nil {
		t.Error("esc should close the host panel")
	}
}

// A -host that fails has to say so. The error used to be stored in a field
// that only the host panel renders, and the panel is not open at startup —
// so the connection silently never happened.
func TestFailedConnectionIsVisible(t *testing.T) {
	m := model{snap: portSnap(), width: 100, height: 30}
	got, cmd := m.Update(connectedMsg{
		host: remote.Host{Name: "web01"},
		err:  errTest{"no SSH keys found"},
	})
	gm := got.(model)
	if gm.hosts == nil {
		t.Fatal("a failed connection should open the host panel, where the reason is shown")
	}
	if !strings.Contains(gm.hostErr, "web01") || !strings.Contains(gm.hostErr, "no SSH keys") {
		t.Errorf("hostErr = %q, want the host and the reason", gm.hostErr)
	}
	if cmd == nil {
		t.Error("a failed connection should also raise a toast")
	}
	out := stripANSI(gm.renderHosts(100, 20))
	if !strings.Contains(out, "no SSH keys") {
		t.Errorf("the panel should render the reason:\n%s", out)
	}
	// And it must not have switched hosts.
	if gm.remote != nil || gm.hostLabel() != "localhost" {
		t.Error("a failed connection must leave you where you were")
	}
}

type errTest struct{ s string }

func (e errTest) Error() string { return e.s }
