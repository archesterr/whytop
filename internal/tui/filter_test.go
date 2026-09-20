package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The complaint this exists for: typing 443 to find what serves it also
// matched every PID and command line containing 443. Narrowing the scope has
// to make the number mean the port and nothing else.
func TestPortScopeMatchesOnlyPorts(t *testing.T) {
	snap := portSnap()
	snap.Procs[2].Cmdline = "/usr/bin/backup --retain 443" // a 443 that is not a port
	snap.Procs[3].PID = 443                                // and a PID that is not a port either
	snap.ByPID = map[int32]int{1: 0, 2: 1, 3: 2, 443: 3}

	wide := model{snap: snap, sortKey: "pid", filter: "443"}
	if n := len(wide.procRows()); n != 3 {
		t.Errorf("the unscoped filter should cast a wide net, matched %d rows, want 3", n)
	}

	narrow := model{snap: snap, sortKey: "pid", filterScope: scopePort, filter: "443"}
	rows := narrow.procRows()
	if len(rows) != 1 || rows[0].Name != "nginx" {
		t.Fatalf("the port scope matched %d rows, want only nginx: %+v", len(rows), rows)
	}
}

// Tab cycles the scope, and it must re-label what is already typed rather
// than clearing it: you usually discover you wanted the port column after
// typing the number.
func TestTabCyclesScopeKeepingTheQuery(t *testing.T) {
	m := model{snap: portSnap(), sortKey: "pid", editing: true, filter: "443"}
	seen := map[string]bool{}
	for i := 0; i < int(numScopes); i++ {
		got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
		m = got.(model)
		if m.filter != "443" {
			t.Fatalf("cycling the scope lost the query: %q", m.filter)
		}
		seen[m.filterScope.String()] = true
	}
	for _, want := range []string{"all", "port", "user", "unit", "command", "pid", "state"} {
		if !seen[want] {
			t.Errorf("Tab never reached the %q scope", want)
		}
	}
}

// A typed prefix has to win over the cycled scope, or a status-line jump
// that sets "state:D" would be reinterpreted by whatever scope the operator
// last left the filter in.
func TestTypedPrefixOverridesTheCycledScope(t *testing.T) {
	m := model{snap: portSnap(), sortKey: "pid", filterScope: scopePort, filter: "state:S"}
	scope, q := m.filterQuery()
	if scope != scopeState || q != "s" {
		t.Errorf("filterQuery = (%v, %q), want the typed state: prefix to win", scope, q)
	}
	if n := len(m.procRows()); n != 4 {
		t.Errorf("state:S should match all four sleeping processes, got %d", n)
	}
}

// An empty query passes everything, including in a narrow scope: an empty
// port filter means "not filtering", not "processes with no ports".
func TestEmptyQueryInANarrowScopeFiltersNothing(t *testing.T) {
	m := model{snap: portSnap(), sortKey: "pid", filterScope: scopePort}
	if n := len(m.procRows()); n != 4 {
		t.Errorf("an empty port filter hid %d of 4 processes", 4-n)
	}
}

// Landing on an empty list has to read as an answer, in the terms of the
// filter that emptied it.
func TestEmptyMessageNamesTheScope(t *testing.T) {
	cases := []struct {
		scope filterScope
		q     string
		want  string
	}{
		{scopePort, "9999", "listening on port 9999"},
		{scopeState, "d", "state D"},
		{scopeUser, "nobody", "owner"},
		{scopeAll, "zzz", `"zzz"`},
	}
	for _, c := range cases {
		m := model{snap: portSnap(), filterScope: c.scope, filter: c.q}
		if got := m.emptyMessage(); !strings.Contains(got, c.want) {
			t.Errorf("scope %v: message %q does not mention %q", c.scope, got, c.want)
		}
	}
}

// A filter that is applied but not being typed still has to be visible:
// every row you are not seeing is hidden by it.
func TestActiveFilterIsVisibleInTheFooter(t *testing.T) {
	m := model{snap: portSnap(), sortKey: "pid", width: 200, filterScope: scopePort, filter: "443"}
	out := stripANSI(m.renderFooter(200))
	if !strings.Contains(out, "port 443") {
		t.Errorf("an active filter should be named in the footer:\n%s", out)
	}
	m.filter = ""
	if out := stripANSI(m.renderFooter(200)); strings.Contains(out, "esc clears") {
		t.Errorf("with no filter the footer should not offer to clear one:\n%s", out)
	}
}
