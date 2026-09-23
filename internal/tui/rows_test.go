package tui

import (
	"testing"

	"github.com/archesterr/whytop/internal/collect"
)

func testSnap() *collect.Snapshot {
	// Every fixture carries a Cmdline: a process without one is a kernel
	// thread (Proc.Kernel), which the process list hides by default.
	procs := []collect.Proc{
		{PID: 1, Name: "init", User: "root", CPU: 0, RSS: 100, Cmdline: "/sbin/init"},
		{PID: 42, Name: "nginx", User: "www", CPU: 12.5, RSS: 5000, Unit: "nginx.service", Cmdline: "nginx: master process"},
		{PID: 43, Name: "worker", User: "www", CPU: 55.0, RSS: 2000, Cmdline: "nginx: worker"},
	}
	byPID := map[int32]int{}
	for i, p := range procs {
		byPID[p.PID] = i
	}
	return &collect.Snapshot{Procs: procs, ByPID: byPID}
}

func TestProcRowsSortByCPU(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "cpu"}
	list := m.procRows()
	if len(list) != 3 {
		t.Fatalf("got %d rows, want 3", len(list))
	}
	if list[0].PID != 43 || list[1].PID != 42 || list[2].PID != 1 {
		t.Errorf("sort by cpu descending wrong order: %+v", list)
	}
}

func TestProcRowsSortByPIDAscending(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid"}
	list := m.procRows()
	if list[0].PID != 1 || list[1].PID != 42 || list[2].PID != 43 {
		t.Errorf("sort by pid ascending wrong order: %+v", list)
	}
}

func TestProcRowsFilterByName(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "cpu"}
	m.filter = "nginx"
	list := m.procRows()
	if len(list) != 2 {
		t.Fatalf("got %d rows matching 'nginx', want 2 (name match + cmdline match)", len(list))
	}
	for _, p := range list {
		if p.PID == 1 {
			t.Errorf("filter leaked non-matching process: %+v", p)
		}
	}
}

func TestProcRowsFilterByPID(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "cpu"}
	m.filter = "42"
	list := m.procRows()
	if len(list) != 1 || list[0].PID != 42 {
		t.Errorf("exact PID filter got %+v, want just PID 42", list)
	}
}

func TestCellTruncatesAndPads(t *testing.T) {
	got := cell("hello", 8, false, stPlain)
	if visLen(got) != 8 {
		t.Errorf("cell width = %d, want 8 (got %q)", visLen(got), got)
	}
	got = cell("a-very-long-name", 6, false, stPlain)
	if visLen(got) != 6 {
		t.Errorf("truncated cell width = %d, want 6 (got %q)", visLen(got), got)
	}
}

func TestUnitNameUnescapesSystemd(t *testing.T) {
	if got := unitName(`app\x2dworker.service`); got != "app-worker.service" {
		t.Errorf("unitName = %q, want %q", got, "app-worker.service")
	}
}

// The selection is a PID, which is what makes the cursor follow a process
// as the list re-sorts underneath it. The cost is that a process which
// exits takes the cursor with it: nothing matches, no row is drawn
// selected, and Enter and k have nothing to act on.
//
// That is worst exactly where it matters most. g jumps to the processes
// stuck on disk, and those rows are short-lived — the dd that was wedged a
// second ago has finished and another has taken its place — so the jump
// lands on the problem and leaves no cursor to act on it.
func TestTheCursorSurvivesTheProcessUnderItExiting(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 140, height: 40} // PIDs 1, 42, 43
	m.moveSel(1)                                                         // onto PID 42, the middle row
	if m.sel != "42" {
		t.Fatalf("the cursor started on %q, want 42", m.sel)
	}

	// PID 42 exits. The cursor must not vanish with it.
	gone := testSnap()
	gone.Procs = []collect.Proc{gone.Procs[0], gone.Procs[2]} // 1 and 43
	gone.ByPID = map[int32]int{1: 0, 43: 1}
	m.snap = gone
	m.reanchorSel()

	if m.sel == "" {
		t.Fatal("the cursor vanished when the process under it exited")
	}
	if m.sel == "42" {
		t.Error("the cursor is still on a process that no longer exists")
	}
	// It holds its position in the list, which is what every list does when
	// a row is removed under it: index 1 is now PID 43.
	if m.sel != "43" {
		t.Errorf("the cursor moved to %q; the row at its position is now 43", m.sel)
	}
}

// And the re-anchor has to happen on the sample that removes the process,
// not only when a key is pressed — the whole point is that no key is.
func TestANewSampleReanchorsTheCursor(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 140, height: 40}
	m.moveSel(1)
	gone := testSnap()
	gone.Procs = []collect.Proc{gone.Procs[0], gone.Procs[2]}
	gone.ByPID = map[int32]int{1: 0, 43: 1}

	got, _ := m.Update(snapMsg{snap: gone})
	if sel := got.(model).sel; sel != "43" {
		t.Errorf("after the sample that removed PID 42 the cursor is on %q, want 43", sel)
	}
}

// A process that is still there keeps the cursor, wherever it has sorted to.
func TestTheCursorFollowsAProcessThatIsStillRunning(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 140, height: 40}
	m.moveSel(1)
	if m.sel != "42" {
		t.Fatalf("the cursor started on %q, want 42", m.sel)
	}
	m.sortKey = "cpu" // the same processes, in a different order
	m.reanchorSel()
	if m.sel != "42" {
		t.Errorf("the cursor left PID 42 for %q after a re-sort", m.sel)
	}
}

// The last row exiting must not index past the end.
func TestTheCursorOnTheLastRowWhenItExits(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 140, height: 40}
	m.moveSel(1 << 30) // End: onto PID 43, the last row
	if m.sel != "43" {
		t.Fatalf("End put the cursor on %q, want 43", m.sel)
	}
	only := testSnap()
	only.Procs = only.Procs[:1] // just PID 1
	only.ByPID = map[int32]int{1: 0}
	m.snap = only
	m.reanchorSel()
	if m.sel != "1" {
		t.Errorf("the cursor is on %q after the list shrank to one row", m.sel)
	}
}

// An empty list has nothing to select, and must not claim otherwise.
func TestTheCursorOnAnEmptyList(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 140, height: 40}
	m.moveSel(1)
	m.snap = &collect.Snapshot{}
	m.reanchorSel()
	if m.sel != "" {
		t.Errorf("an empty list still reports %q selected", m.sel)
	}
}
