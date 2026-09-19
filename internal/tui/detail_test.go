package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/collect"
)

func detailSnap() *collect.Snapshot {
	procs := []collect.Proc{
		{PID: 10, Name: "nginx", User: "www", State: "S", Cmdline: "nginx: master", RSS: 5 << 20, Unit: "nginx.service"},
		{PID: 11, Name: "worker", User: "www", State: "S", Cmdline: "nginx: worker", PPID: 10, RSS: 3 << 20, Unit: "nginx.service"},
	}
	snap := &collect.Snapshot{Procs: procs, ByPID: map[int32]int{}, CPU: collect.CPU{Cores: 4}}
	for i, p := range procs {
		snap.ByPID[p.PID] = i
	}
	return snap
}

// Every section of the detail panel has to actually render on a short
// terminal. Flooring each section's height independently made them sum to
// more than the panel had, and the overflow came off the bottom — so the
// JOURNAL bar appeared with nothing underneath it.
func TestDetailSectionsAllRenderOnAShortTerminal(t *testing.T) {
	m := model{snap: detailSnap(), width: 80, height: 26,
		detail: &detailState{pid: 10, loaded: true, follow: true, journal: "some log line"}}
	out := stripANSI(m.renderDetail(80, 26-5))
	for _, want := range []string{"PROCESS TREE", "SOCKETS", "OPEN FILES", "JOURNAL"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s section missing from the panel:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "some log line") {
		t.Errorf("the JOURNAL bar rendered with no journal under it:\n%s", out)
	}
}

func TestDetailNeverOverflowsItsWidth(t *testing.T) {
	m := model{snap: detailSnap(), detail: &detailState{pid: 10, loaded: true, follow: true}}
	for _, w := range []int{80, 110, 132} {
		m.width = w
		for _, line := range strings.Split(m.renderDetail(w, 30), "\n") {
			if got := visLen(line); got > w {
				t.Errorf("detail line overflows w=%d at %d columns: %q", w, got, stripANSI(line))
			}
		}
	}
}

// Wide terminals get three fact columns, narrow ones two: at two columns a
// 132-wide panel left half of every fact row empty and made the panel taller
// than it needed to be.
func TestFactsGridUsesThreeColumnsWhenThereIsRoom(t *testing.T) {
	m := model{snap: detailSnap(), detail: &detailState{pid: 10, loaded: true}}
	wide := stripANSI(m.renderDetail(132, 40))
	narrow := stripANSI(m.renderDetail(80, 40))
	countSeps := func(out string) int {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "State") {
				return strings.Count(l, "│")
			}
		}
		return -1
	}
	if got := countSeps(wide); got != 2 {
		t.Errorf("wide panel should lay facts out in 3 columns (2 separators), got %d", got)
	}
	if got := countSeps(narrow); got != 1 {
		t.Errorf("narrow panel should fall back to 2 columns (1 separator), got %d", got)
	}
}

// The journal follows by default — you open a process's panel to watch what
// it's doing, and a log that silently stopped updating is worse than none.
func TestJournalFollowsByDefaultAndSaysSo(t *testing.T) {
	m := model{snap: detailSnap(), sortKey: "pid", width: 100}
	m.sel[tabProcs] = "10"
	got, _ := m.openSelected()
	gm := got.(model)
	if gm.detail == nil || !gm.detail.follow {
		t.Fatal("opening a process should start following its journal")
	}
	if out := stripANSI(gm.renderDetail(100, 30)); !strings.Contains(out, "● live") {
		t.Errorf("a following journal should say so in its section bar:\n%s", out)
	}

	// f toggles it off, and the panel says that too.
	off, _ := gm.handleDetailKey(keyRunes("f"))
	om := off.(model)
	if om.detail.follow {
		t.Error("f should stop the journal following")
	}
	if out := stripANSI(om.renderDetail(100, 30)); !strings.Contains(out, "paused") {
		t.Errorf("a paused journal should say so:\n%s", out)
	}
}

// A unit's memory is the memory of its processes — free to compute, since
// they're already collected with the unit they belong to.
func TestUnitMemorySumsItsProcesses(t *testing.T) {
	m := model{snap: detailSnap()}
	mem := m.unitMemory()
	if got := mem["nginx.service"]; got != 8<<20 {
		t.Errorf("nginx.service memory = %d, want the 8 MiB its two processes use", got)
	}
	if _, ok := mem["stopped.service"]; ok {
		t.Error("a unit with no processes should have no entry, so it can render as – not 0 B")
	}
}

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}
