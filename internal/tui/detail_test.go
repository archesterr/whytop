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
	m.sel = "10"
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

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// The panel has two navigable lists, so the arrows have to drive the focused
// one and the descriptor actions have to apply to the row under the cursor.
func TestDetailFocusSwitchesWhichListTheArrowsDrive(t *testing.T) {
	snap := detailSnap()
	d := &detailState{pid: 10, loaded: true, extra: collect.Extra{FDs: 3, OpenFiles: []collect.OpenFile{
		{FD: "0", Target: "/dev/null", Kind: "file"},
		{FD: "1", Target: "/var/log/app.log (deleted)", Kind: "deleted"},
		{FD: "2", Target: "socket:[1]", Kind: "socket"},
	}}}
	m := model{snap: snap, width: 120, detail: d}

	// Tree has focus to begin with: down moves the tree, not the files.
	m2, _ := m.handleDetailKey(tea.KeyMsg{Type: tea.KeyDown})
	if m2.(model).detail.treeSel != 1 || m2.(model).detail.fileSel != 0 {
		t.Errorf("with the tree focused, down should move the tree: tree=%d files=%d",
			m2.(model).detail.treeSel, m2.(model).detail.fileSel)
	}

	// Tab moves focus to the files; now down moves the file cursor.
	m3, _ := m.handleDetailKey(tea.KeyMsg{Type: tea.KeyTab})
	if m3.(model).detail.focus != focusFiles {
		t.Fatal("tab should move focus to the open files")
	}
	m4, _ := m3.(model).handleDetailKey(tea.KeyMsg{Type: tea.KeyDown})
	if m4.(model).detail.fileSel != 1 {
		t.Errorf("with the files focused, down should move the file cursor, got %d", m4.(model).detail.fileSel)
	}
	if f, ok := m4.(model).selectedFile(); !ok || f.FD != "1" {
		t.Errorf("selected file should be the row under the cursor, got %+v", f)
	}
}

// Both descriptor actions are destructive enough that they must never fire
// straight off a keypress, and never at all while the tree has focus.
func TestFileActionsNeedFocusAndAlwaysConfirm(t *testing.T) {
	d := &detailState{pid: 10, loaded: true, extra: collect.Extra{FDs: 1, OpenFiles: []collect.OpenFile{
		{FD: "7", Target: "/var/log/big.log (deleted)", Kind: "deleted"},
	}}}
	m := model{snap: detailSnap(), width: 120, detail: d}

	for _, key := range []string{"c", "t"} {
		got, cmd := m.handleDetailKey(keyRunes(key))
		if got.(model).confirm != nil || cmd != nil {
			t.Errorf("%q acted while the tree had focus", key)
		}
	}

	m.detail.focus = focusFiles
	for _, key := range []string{"c", "t"} {
		got, cmd := m.handleDetailKey(keyRunes(key))
		gm := got.(model)
		if gm.confirm == nil {
			t.Fatalf("%q should ask before touching a descriptor", key)
		}
		if cmd != nil {
			t.Errorf("%q should not act until the confirm is answered", key)
		}
		if !strings.Contains(gm.confirm.prompt, "7") {
			t.Errorf("%q prompt should name the descriptor: %q", key, gm.confirm.prompt)
		}
	}

	// Closing is the dangerous one of the two and is marked as such.
	got, _ := m.handleDetailKey(keyRunes("c"))
	if !got.(model).confirm.danger {
		t.Error("closing a descriptor should use the danger confirm styling")
	}
	got, _ = m.handleDetailKey(keyRunes("t"))
	if got.(model).confirm.danger {
		t.Error("truncating leaves the process running and shouldn't be styled as dangerous")
	}
}

// A process that exits keeps its panel open — you asked to see it, and
// being thrown back to the list the instant it died would take the answer
// away with it. But x, X, r and j can do nothing to a PID that is gone and
// silently no-op, so the footer must stop offering them: a footer that
// lists a key which then refuses is a footer people stop reading, which is
// the same rule the remote-host case already follows.
func TestThePanelOfADeadProcessOffersNothingItCannotDo(t *testing.T) {
	m := model{snap: testSnap(), width: 140, height: 40,
		detail: &detailState{pid: 99999, loaded: true}} // never in the snapshot
	foot := stripANSI(m.renderFooter(200))
	for _, gone := range []string{"stop", "kill", "restart", "journal", "live-log", "empty file", "close fd"} {
		if strings.Contains(foot, gone) {
			t.Errorf("the footer still offers %q for a process that has exited: %s", gone, foot)
		}
	}
	for _, want := range []string{"exited", "esc"} {
		if !strings.Contains(foot, want) {
			t.Errorf("the footer does not mention %q: %s", want, foot)
		}
	}
	// And the panel itself still explains where the process went.
	if body := stripANSI(m.renderDetail(140, 30)); !strings.Contains(body, "no longer exists") {
		t.Errorf("the panel does not say the process is gone: %s", body)
	}
}

// A live process keeps every action it can actually perform.
func TestThePanelOfALiveProcessKeepsItsActions(t *testing.T) {
	m := model{snap: testSnap(), width: 140, height: 40,
		detail: &detailState{pid: 42, loaded: true}}
	foot := stripANSI(m.renderFooter(200))
	for _, want := range []string{"stop", "kill", "journal"} {
		if !strings.Contains(foot, want) {
			t.Errorf("a live process lost %q from its footer: %s", want, foot)
		}
	}
	if strings.Contains(foot, "exited") {
		t.Errorf("a live process was called exited: %s", foot)
	}
}
