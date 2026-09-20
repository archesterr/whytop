package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/collect"
)

// A box with several things wrong at once, including more than one process
// wedged on disk — the case the status line has to stay useful in.
func stuckSnap() *collect.Snapshot {
	procs := []collect.Proc{
		{PID: 10, Name: "idle", User: "root", State: "S", Cmdline: "/usr/bin/idle"},
		{PID: 11, Name: "stuck-a", User: "root", State: "D", Cmdline: "dd if=/dev/sda"},
		{PID: 12, Name: "stuck-b", User: "www", State: "D", Cmdline: "rsync /mnt/nfs"},
		{PID: 13, Name: "stuck-c", User: "www", State: "D", Cmdline: "tar cf - /mnt/nfs"},
		{PID: 14, Name: "hog", User: "root", State: "S", Cmdline: "java -Xmx8g", RSS: 8 << 30},
	}
	snap := &collect.Snapshot{
		Procs: procs, ByPID: map[int32]int{},
		CPU: collect.CPU{Cores: 4},
		FS:  []collect.FS{{Mount: "/var", UsedPct: 97}},
	}
	for i, p := range procs {
		snap.ByPID[p.PID] = i
	}
	return snap
}

// The whole point: the line said a process was stuck and there was no way to
// get to it. Jumping has to land on the actual processes, all of them.
func TestJumpingToBlockedProcessesShowsExactlyThem(t *testing.T) {
	m := model{snap: stuckSnap(), width: 132, sortKey: "cpu", sortDir: -1}

	got, _ := m.jumpToFinding()
	gm := got.(model)
	rows := gm.procRows()
	if len(rows) != 3 {
		t.Fatalf("expected the 3 blocked processes, got %d: %+v", len(rows), rows)
	}
	for _, p := range rows {
		if p.State != "D" {
			t.Errorf("a process that isn't blocked leaked into the filtered view: %+v", p)
		}
	}
	// The cursor lands on a row, so enter opens something straight away.
	if gm.sel == "" {
		t.Error("nothing was selected after the jump — enter would do nothing")
	}
	if gm.toast == "" || !strings.Contains(gm.toast, "stuck waiting on disk") {
		t.Errorf("the jump should say what it did, got %q", gm.toast)
	}
}

// Several problems at once: g walks them rather than dropping you on the
// worst one over and over.
func TestRepeatedJumpsWalkEveryFinding(t *testing.T) {
	m := model{snap: stuckSnap(), width: 132, sortKey: "cpu", sortDir: -1}
	found := m.findings()
	if len(found) < 2 {
		t.Fatalf("this fixture needs several findings, got %d", len(found))
	}

	seen := map[string]bool{}
	cur := m
	for range found {
		got, _ := cur.jumpToFinding()
		cur = got.(model)
		seen[stripANSI(cur.toast)] = true
	}
	if len(seen) != len(found) {
		t.Errorf("pressing g %d times reached %d distinct findings, want %d: %v",
			len(found), len(seen), len(found), seen)
	}

	// And it wraps rather than sticking at the end.
	got, _ := cur.jumpToFinding()
	if got.(model).toast == "" {
		t.Error("the cycle should wrap round to the first finding")
	}
}

// Filters are what make the jump readable, so esc has to be able to undo one
// without going through the filter editor first.
func TestEscClearsAJumpFilter(t *testing.T) {
	m := model{snap: stuckSnap(), width: 132, sortKey: "cpu", sortDir: -1}
	got, _ := m.jumpToFinding()
	gm := got.(model)
	if gm.filter == "" {
		t.Fatal("the jump to blocked processes should have left a filter")
	}
	back, _ := gm.handleListKey(tea.KeyMsg{Type: tea.KeyEsc})
	if back.(model).filter != "" {
		t.Error("esc should clear the filter a jump left behind")
	}
}

// Clicking a finding goes where that finding points, which means the drawn
// position and the hit-test have to agree.
func TestClickingAFindingGoesWhereItPoints(t *testing.T) {
	m := model{snap: stuckSnap(), width: 132, sortKey: "cpu", sortDir: -1}
	regions, _ := statusLayout(m.findings(), m.contentW())
	if len(regions) == 0 {
		t.Fatal("no findings were laid out to click")
	}

	// The rendered line must actually contain each region's text at its
	// claimed offset, or the hit-test is pointing at nothing.
	plain := stripANSI(m.renderStatus(m.contentW()))
	for _, r := range regions {
		if r.x1 > len([]rune(plain)) {
			t.Fatalf("region %q claims columns %d-%d but the line is %d wide", r.f.text, r.x0, r.x1, len([]rune(plain)))
		}
		if at := string([]rune(plain)[r.x0:r.x1]); at != r.f.text {
			t.Errorf("column %d holds %q, but the hit-test there resolves to %q", r.x0, at, r.f.text)
		}
	}

	got, _ := m.clickFinding(regions[0].x0)
	if got.(model).filter != regions[0].f.to.filter {
		t.Errorf("clicking %q left filter %q, want %q", regions[0].f.text, got.(model).filter, regions[0].f.to.filter)
	}

	// A click on the gap between findings does nothing rather than guessing.
	before := m
	after, _ := m.clickFinding(0)
	if after.(model).filter != before.filter || after.(model).sortKey != before.sortKey {
		t.Error("a click on the ⚠ marker, not on a finding, should do nothing")
	}
}

// state: has to match the column exactly — a plain text search for "d" would
// match half the command lines on the box.
func TestStateFilterMatchesTheColumnNotTheText(t *testing.T) {
	m := model{snap: stuckSnap(), sortKey: "pid", sortDir: 1}
	m.filter = "state:D"
	for _, p := range m.procRows() {
		if p.State != "D" {
			t.Errorf("state:D matched a process in state %q: %+v", p.State, p)
		}
	}
	// "dd if=/dev/sda" contains d's; a text filter still behaves as before.
	m.filter = "dd"
	rows := m.procRows()
	if len(rows) != 1 || rows[0].PID != 11 {
		t.Errorf("plain text filtering changed behaviour: %+v", rows)
	}
}

// I/O wait and PSI are averages over a window: by the time you press g there
// may be nothing in D at all. Sending those to a state:D filter strands the
// operator on an empty list, so they go to the devices instead.
func TestAveragedFindingsDoNotJumpToALiveStateFilter(t *testing.T) {
	m := model{snap: &collect.Snapshot{
		CPU:  collect.CPU{Cores: 4, Iowait: 12},
		PSI:  collect.PSI{Available: true, IOSome: 25},
		Mem:  collect.Mem{Total: 16 << 30},
		Host: "h",
	}}
	found := m.findings()
	if len(found) == 0 {
		t.Fatal("expected I/O wait and PSI findings")
	}
	for _, f := range found {
		if strings.Contains(f.text, "I/O wait") || strings.Contains(f.text, "disk pressure") {
			if f.to.filter != "" {
				t.Errorf("%q jumps to filter %q — an average can't promise a process is blocked right now",
					f.text, f.to.filter)
			}
			// It sorts instead: the heaviest I/O processes are always
			// something to show, where a D-state filter may match nothing.
			if f.to.sortKey != "io" {
				t.Errorf("%q should sort by I/O, got sortKey %q", f.text, f.to.sortKey)
			}
		}
	}
}

// Landing on an empty filtered list is a normal outcome, so it has to read as
// an answer rather than as a failed search.
func TestEmptyStateFilterExplainsItself(t *testing.T) {
	m := model{snap: stuckSnap(), width: 120, sortKey: "pid"}
	m.filter = "state:R"
	out := stripANSI(m.renderProcs(120, 10))
	if !strings.Contains(out, "may have cleared") || !strings.Contains(out, "esc") {
		t.Errorf("an empty state filter should explain itself and offer a way out, got %q", out)
	}
}
