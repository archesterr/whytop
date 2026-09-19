package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/actions"
)

func TestClickTabSwitchesTab(t *testing.T) {
	m := model{snap: testSnap(), tab: tabProcs, width: 100}
	regions := tabRegions(m.tabCounts())
	var portsX int
	for _, r := range regions {
		if r.t == tabPorts {
			portsX = r.x0
		}
	}
	got, _ := m.handleClick(portsX, tabBarRow)
	if got.(model).tab != tabPorts {
		t.Errorf("clicking the Ports tab region left tab = %v, want tabPorts", got.(model).tab)
	}
}

func TestClickOutsideAnyTabDoesNothing(t *testing.T) {
	m := model{snap: testSnap(), tab: tabProcs, width: 100}
	got, _ := m.handleClick(9999, tabBarRow)
	if got.(model).tab != tabProcs {
		t.Errorf("click far outside any tab region changed tab to %v", got.(model).tab)
	}
}

func TestClickRowSelectsAndOpensProcess(t *testing.T) {
	m := model{snap: testSnap(), tab: tabProcs, sortKey: "pid", width: 100, height: 30}
	// rows are sorted by pid ascending: 1 (init), 42 (nginx), 43 (worker)
	got, cmd := m.handleClick(0, listFirstRow+1) // second row -> PID 42
	gm := got.(model)
	if gm.sel[tabProcs] != "42" {
		t.Errorf("clicking row 1 selected %q, want \"42\"", gm.sel[tabProcs])
	}
	if gm.detail == nil || gm.detail.pid != 42 {
		t.Errorf("clicking a row should open its detail view, got detail=%+v", gm.detail)
	}
	if cmd == nil {
		t.Error("opening a process detail should kick off loadExtra/loadJournal commands")
	}
}

func TestClickRowOutOfRangeIsIgnored(t *testing.T) {
	m := model{snap: testSnap(), tab: tabProcs, sortKey: "pid", width: 100}
	got, _ := m.handleClick(0, listFirstRow+50) // way past the 3 rows we have
	if got.(model).detail != nil {
		t.Error("clicking past the last row should not open anything")
	}
}

func TestWheelScrollMovesSelection(t *testing.T) {
	// Matches moveSel's existing arrow-key semantics (idx starts at 0, then
	// the delta is applied) — from nothing selected, one wheel-down lands
	// on row index 1, not row 0.
	m := model{snap: testSnap(), tab: tabProcs, sortKey: "pid"}
	msg := tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress}
	got, _ := m.handleMouse(msg)
	gm := got.(model)
	if gm.sel[tabProcs] != "42" {
		t.Errorf("first wheel-down should select row index 1 (PID 42), got %q", gm.sel[tabProcs])
	}
	got2, _ := gm.handleMouse(msg)
	if got2.(model).sel[tabProcs] != "43" {
		t.Errorf("second wheel-down should move to the last row (PID 43), got %q", got2.(model).sel[tabProcs])
	}

	// Wheel-up from there should step back down toward row 0.
	up := tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress}
	got3, _ := got2.(model).handleMouse(up)
	if got3.(model).sel[tabProcs] != "42" {
		t.Errorf("wheel-up should move back to PID 42, got %q", got3.(model).sel[tabProcs])
	}
}

func TestMouseClickDuringConfirmIsIgnored(t *testing.T) {
	m := model{snap: testSnap(), tab: tabProcs, confirm: &confirmState{prompt: "Force kill?"}}
	msg := tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: tabBarRow}
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

func TestOOMPollWithNoKillsStaysSilent(t *testing.T) {
	m := model{}
	got, _ := m.Update(oomPollMsg{kills: nil})
	if got.(model).toast != "" {
		t.Errorf("no OOM kills should mean no toast, got %q", got.(model).toast)
	}
}
