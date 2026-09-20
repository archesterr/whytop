package tui

import (
	"strings"
	"testing"

	"github.com/archesterr/whytop/internal/collect"
)

func portSnap() *collect.Snapshot {
	procs := []collect.Proc{
		{PID: 1, Name: "nginx", User: "root", State: "S", Cmdline: "nginx: master", Ports: []uint32{80, 443}, NetKnown: true, NetRxBps: 900, NetTxBps: 8000},
		{PID: 2, Name: "sshd", User: "root", State: "S", Cmdline: "/usr/sbin/sshd", Ports: []uint32{22}, NetKnown: true, NetRxBps: 10, NetTxBps: 20},
		{PID: 3, Name: "cron", User: "root", State: "S", Cmdline: "/usr/sbin/cron", NetKnown: true},
		{PID: 4, Name: "curl", User: "app", State: "S", Cmdline: "curl example.com", Estab: 2, NetKnown: true, NetRxBps: 500_000},
	}
	snap := &collect.Snapshot{Procs: procs, ByPID: map[int32]int{}, Mem: collect.Mem{Total: 16 << 30}}
	for i, p := range procs {
		snap.ByPID[p.PID] = i
	}
	return snap
}

// "who has port 8080" is the question the column exists for, so a bare
// number has to answer it — making people learn a prefix first would be a
// puzzle, not a filter.
func TestFilteringByPort(t *testing.T) {
	for _, f := range []string{"443", "port:443"} {
		m := model{snap: portSnap(), sortKey: "pid", sortDir: 1, filter: f}
		rows := m.procRows()
		if len(rows) != 1 || rows[0].Name != "nginx" {
			t.Errorf("filter %q matched %d rows, want just nginx: %+v", f, len(rows), rows)
		}
	}
}

// A port is matched as a whole number. 80 matching 8080 would make the
// filter useless on exactly the ports people care about.
func TestPortFilterDoesNotMatchSubstrings(t *testing.T) {
	snap := portSnap()
	snap.Procs[0].Ports = []uint32{8080}
	m := model{snap: snap, sortKey: "pid", filter: "port:80"}
	if rows := m.procRows(); len(rows) != 0 {
		t.Errorf("port:80 should not match a process listening on 8080, got %+v", rows)
	}
}

// Sorting by port has to put the processes that listen on something at the
// top in both directions — a screen of blanks above the rows you asked for
// is not a sort, it is a scroll.
func TestSortingByPortKeepsListenersTogether(t *testing.T) {
	for _, dir := range []int{1, -1} {
		m := model{snap: portSnap(), sortKey: "port", sortDir: dir}
		rows := m.procRows()
		if len(rows) != 4 {
			t.Fatalf("expected 4 rows, got %d", len(rows))
		}
		for i, p := range rows[:2] {
			if len(p.Ports) == 0 {
				t.Errorf("dir %d: row %d (%s) listens on nothing but sorted above one that does", dir, i, p.Name)
			}
		}
		for i, p := range rows[2:] {
			if len(p.Ports) != 0 {
				t.Errorf("dir %d: row %d (%s) listens on %v but sorted below one that listens on nothing", dir, i+2, p.Name, p.Ports)
			}
		}
		// Reversing the direction reverses the listeners, it does not bury
		// them: sshd:22 leads ascending, nginx:80 leads descending.
		want := "sshd"
		if dir < 0 {
			want = "nginx"
		}
		if rows[0].Name != want {
			t.Errorf("dir %d: first row is %q, want %q", dir, rows[0].Name, want)
		}
	}
}

func TestSortingByNetworkUsesBothDirections(t *testing.T) {
	m := model{snap: portSnap(), sortKey: "net", sortDir: -1}
	rows := m.procRows()
	if rows[0].Name != "curl" {
		t.Errorf("the heaviest talker should be first, got %q", rows[0].Name)
	}
	if rows[1].Name != "nginx" {
		t.Errorf("rx+tx should rank nginx second, got %q", rows[1].Name)
	}
}

// "No traffic" and "we could not measure it" are different answers, and
// printing 0 B/s for the second is a lie the operator would act on.
func TestUnmeasurableNetworkSaysSoRatherThanZero(t *testing.T) {
	snap := portSnap()
	m := model{snap: snap, sortKey: "pid", width: 200}
	if out := stripANSI(m.renderProcs(200, 10)); !strings.Contains(out, "NET↓") {
		t.Fatalf("with measurable traffic the network columns should be shown:\n%s", out)
	}

	// Nothing measurable at all: the columns are not worth their width.
	for i := range snap.Procs {
		snap.Procs[i].NetKnown = false
	}
	out := stripANSI(m.renderProcs(200, 10))
	if strings.Contains(out, "NET↓") {
		t.Errorf("with nothing measurable the network columns should be dropped:\n%s", out)
	}

	// One measurable process and one not: the column stays, and the
	// unmeasured row says so rather than claiming zero.
	snap.Procs[0].NetKnown = true
	out = stripANSI(m.renderProcs(200, 10))
	if !strings.Contains(out, "?") {
		t.Errorf("a process whose traffic wasn't visible should render ?, not 0:\n%s", out)
	}
}

// PORT is the last optional column to be given up: it is the reason the
// Ports tab is gone, and nothing else on the screen answers "who has 8080".
func TestPortColumnOutlivesTheOtherOptionalColumns(t *testing.T) {
	var lastWithPort, firstWithout int
	for w := 60; w <= 200; w++ {
		has := map[string]bool{}
		for _, c := range procColumns(w, true) {
			has[c.key] = true
		}
		if has["unit"] && !has["port"] {
			t.Fatalf("at w=%d UNIT survived but PORT did not", w)
		}
		if has["rx"] && !has["port"] {
			t.Fatalf("at w=%d NET survived but PORT did not", w)
		}
		if has["port"] {
			lastWithPort = w
		} else {
			firstWithout = w
		}
	}
	if lastWithPort == 0 || firstWithout == 0 {
		t.Fatalf("expected PORT to appear on wide terminals and be dropped on narrow ones (last with %d, first without %d)", lastWithPort, firstWithout)
	}
}
