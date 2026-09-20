package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/actions"
	"github.com/archesterr/whytop/internal/collect"
)

// A click on the chrome above the list must not be taken as a click on a
// row — the rows start at a fixed offset and everything above it is header.
func TestClickAboveTheListDoesNothing(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 100, height: 30}
	for _, row := range []int{0, 1, 2, 3, 5} {
		got, cmd := m.handleClick(4, row)
		if got.(model).sel != "" || cmd != nil {
			t.Errorf("a click on row %d selected %q", row, got.(model).sel)
		}
	}
}

// A click selects the row; clicking the row that is already selected opens
// it. Opening on the first click made the tree view unusable with a mouse:
// selecting a process is what marks out its descendants, and a panel thrown
// over the list on that same click hides the thing you selected it to see.
func TestClickRowSelectsThenOpens(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 100, height: 30}
	// rows are sorted by pid ascending: 1 (init), 42 (nginx), 43 (worker)
	got, cmd := m.handleClick(0, m.listFirstRow()+1) // second row -> PID 42
	gm := got.(model)
	if gm.sel != "42" {
		t.Errorf("clicking row 1 selected %q, want \"42\"", gm.sel)
	}
	if gm.detail != nil {
		t.Errorf("the first click on a row should only select it, got detail=%+v", gm.detail)
	}
	if cmd != nil {
		t.Error("selecting a row should not kick off any command")
	}

	got, cmd = gm.handleClick(0, m.listFirstRow()+1) // the same row again
	gm = got.(model)
	if gm.detail == nil || gm.detail.pid != 42 {
		t.Errorf("clicking the selected row should open its detail view, got detail=%+v", gm.detail)
	}
	if cmd == nil {
		t.Error("opening a process detail should kick off loadExtra/loadJournal commands")
	}
}

func TestClickRowOutOfRangeIsIgnored(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 100}
	got, _ := m.handleClick(0, m.listFirstRow()+50) // way past the 3 rows we have
	if got.(model).detail != nil {
		t.Error("clicking past the last row should not open anything")
	}
}

func TestWheelScrollMovesSelection(t *testing.T) {
	// Matches moveSel's existing arrow-key semantics (idx starts at 0, then
	// the delta is applied) — from nothing selected, one wheel-down lands
	// on row index 1, not row 0.
	m := model{snap: testSnap(), sortKey: "pid"}
	msg := tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress}
	got, _ := m.handleMouse(msg)
	gm := got.(model)
	if gm.sel != "42" {
		t.Errorf("first wheel-down should select row index 1 (PID 42), got %q", gm.sel)
	}
	got2, _ := gm.handleMouse(msg)
	if got2.(model).sel != "43" {
		t.Errorf("second wheel-down should move to the last row (PID 43), got %q", got2.(model).sel)
	}

	// Wheel-up from there should step back down toward row 0.
	up := tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}
	got3, _ := got2.(model).handleMouse(up)
	if got3.(model).sel != "42" {
		t.Errorf("wheel-up should move back to PID 42, got %q", got3.(model).sel)
	}
}

func TestMouseClickDuringConfirmIsIgnored(t *testing.T) {
	m := model{snap: testSnap(), confirm: &confirmState{prompt: "Force kill?"}}
	msg := tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: m.listFirstRow()}
	got, cmd := m.handleMouse(msg)
	gm := got.(model)
	if gm.confirm == nil {
		t.Error("a stray click must never dismiss or act on a destructive-action confirm prompt")
	}
	if cmd != nil {
		t.Error("a click during confirm should produce no command")
	}
}

func TestOOMPollShowsToastAndReschedules(t *testing.T) {
	m := model{}
	msg := oomPollMsg{kills: []actions.OOMKill{{PID: 4821, Name: "java"}}}
	got, cmd := m.Update(msg)
	gm := got.(model)
	if gm.toast == "" {
		t.Fatal("an OOM kill event should produce a visible toast, not silently update state")
	}
	if !strings.Contains(gm.toast, "java") || !strings.Contains(gm.toast, "4821") {
		t.Errorf("toast should name the killed process and PID: %q", gm.toast)
	}
	if cmd == nil {
		t.Error("Update should return commands (toast timer + next poll), got nil")
	}
}

func TestProcsTableDropsUnitColumnWhenNarrow(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid"}
	narrow := m.renderProcs(80, 20)
	if strings.Contains(strings.Split(narrow, "\n")[0], "UNIT") {
		t.Error("at 80 columns the Unit header should be dropped in favor of a readable Command column, matching the original web UI's own responsive behavior")
	}
	wide := m.renderProcs(160, 20)
	if !strings.Contains(strings.Split(wide, "\n")[0], "UNIT") {
		t.Error("at 160 columns there's room for the Unit column and it should be shown")
	}
}

// Device counters can say a disk is busy but never say which process is
// responsible. Sorting the one list by I/O is now that answer, and it has to
// rank a process actually blocked on I/O (state D) first even when its
// measured bytes/sec this tick is lower than a merely-active process — being
// stuck is exactly why it reports almost nothing.
func TestSortingByIORanksBlockedProcessFirst(t *testing.T) {
	snap := &collect.Snapshot{Procs: []collect.Proc{
		{PID: 1, Name: "busy", User: "root", State: "S", ReadBps: 5_000_000, Cmdline: "busy"},
		{PID: 2, Name: "stuck", User: "root", State: "D", ReadBps: 100, Cmdline: "stuck"},
		{PID: 3, Name: "idle", User: "root", State: "S", Cmdline: "idle"},
	}}
	snap.ByPID = map[int32]int{1: 0, 2: 1, 3: 2}
	m := model{snap: snap, sortKey: "io", sortDir: -1}
	rows := m.procRows()
	if len(rows) != 3 {
		t.Fatalf("expected all 3 processes, got %d", len(rows))
	}
	if rows[0].Name != "stuck" {
		t.Errorf("a D-state process should rank above a merely-busy one, got %q first", rows[0].Name)
	}
	if rows[1].Name != "busy" {
		t.Errorf("after the blocked one, the heaviest reader should follow, got %q", rows[1].Name)
	}
}

func TestOOMPollWithNoKillsStaysSilent(t *testing.T) {
	m := model{}
	got, _ := m.Update(oomPollMsg{kills: nil})
	if got.(model).toast != "" {
		t.Errorf("no OOM kills should mean no toast, got %q", got.(model).toast)
	}
}
