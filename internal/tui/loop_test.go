package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/collect"
)

// A successful kill asks for a fresh reading. It used to do that by starting
// a second refresh loop beside the first, and after a few kills whytop was
// sampling several times a second with every rate computed over a few
// milliseconds — every process at 0% CPU.
func TestAKillDoesNotStartASecondRefreshLoop(t *testing.T) {
	m := initialModel(Options{})
	old := m.loopGen()
	next, _ := m.Update(actionMsg{ok: true, text: "Sent SIGTERM"})
	m = next.(model)
	if m.loopGen() == old {
		t.Fatal("a refresh after an action should replace the loop, not add one")
	}

	// The old loop's reading arrives: it must neither land nor go on.
	snap := &collect.Snapshot{ByPID: map[int32]int{}, Procs: []collect.Proc{{PID: 1}}}
	next, cmd := m.Update(snapMsg{snap: snap, gen: old})
	if next.(model).snap == snap {
		t.Error("a reading from the replaced loop was shown")
	}
	if cmd != nil {
		t.Error("a reading from the replaced loop scheduled another tick, keeping two loops alive")
	}

	// The live loop's reading lands and carries on.
	next, cmd = m.Update(snapMsg{snap: snap, gen: m.loopGen()})
	if next.(model).snap != snap || cmd == nil {
		t.Error("the live loop's reading should be shown and schedule the next")
	}
}

func TestAReplacedLoopsPendingTickDoesNothing(t *testing.T) {
	m := initialModel(Options{})
	stale := m.collectCmd(0)
	m.refreshNow()
	if msg := stale(); msg != nil {
		t.Errorf("the replaced loop's tick still took a reading: %T", msg)
	}
}

func TestAFilterYouCommitSelectsWhatItFound(t *testing.T) {
	m := initialModel(Options{})
	m.snap = &collect.Snapshot{ByPID: map[int32]int{1: 0, 4121: 1},
		Procs: []collect.Proc{{PID: 1, Name: "init", Cmdline: "/sbin/init"}, {PID: 4121, Name: "api", Cmdline: "/usr/bin/api"}}}
	m.editing, m.filter = true, "pid:4121"
	out, _ := m.handleEditKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(model)
	if m.sel != "4121" {
		t.Fatalf("after /pid:4121 enter the one match should be selected so k can act on it; selected %q", m.sel)
	}
	out, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if c := out.(model).confirm; c == nil {
		t.Error("k straight after the filter should ask to kill 4121")
	}
}
