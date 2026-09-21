package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/collect"
)

// Selecting text with the mouse is the thing whytop takes away by asking
// for mouse reporting, so the gesture that gives it back has to be the one
// people already make when they want to copy: right-click.
func TestRightClickHandsTheMouseToTheTerminal(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 100, height: 30}
	got, cmd := m.handleMouse(tea.MouseMsg{
		Button: tea.MouseButtonRight, Action: tea.MouseActionPress, X: 10, Y: 12,
	})
	gm := got.(model)
	if !gm.mouseOff {
		t.Fatal("right-click did not release the mouse")
	}
	if cmd == nil {
		t.Fatal("right-click released the mouse without telling the terminal")
	}
	// The toast has to name the gesture: nothing on screen moves when
	// reporting stops, so the handover is otherwise silent.
	if !strings.Contains(gm.toast, "right-click to copy") {
		t.Errorf("the release said: %q", gm.toast)
	}
}

// A button reports twice. If the release toggled too, the mouse would be
// handed over and taken straight back before the finger left the button.
func TestOnlyTheRightClickPressReleasesTheMouse(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 100, height: 30}
	for _, action := range []tea.MouseAction{tea.MouseActionRelease, tea.MouseActionMotion} {
		got, cmd := m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonRight, Action: action})
		if got.(model).mouseOff || cmd != nil {
			t.Errorf("a right-button %v toggled mouse reporting", action)
		}
	}
}

// Whatever a right-click lands on, it must not also act on it: a click that
// both released the mouse and opened the row under it would be a very
// surprising way to copy a PID.
func TestRightClickDoesNotActOnWhatItLandsOn(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 100, height: 30}
	got, _ := m.handleMouse(tea.MouseMsg{
		Button: tea.MouseButtonRight, Action: tea.MouseActionPress,
		X: 4, Y: m.listFirstRow() + 1,
	})
	gm := got.(model)
	if gm.sel != "" || gm.detail != nil {
		t.Errorf("a right-click selected %q / opened %+v", gm.sel, gm.detail)
	}
	if gm.sortKey != "pid" {
		t.Errorf("a right-click changed the sort to %q", gm.sortKey)
	}
}

// A confirmation is the one place a stray click must not reach — including
// the one that would otherwise pull the mouse out from under the answer.
func TestRightClickIsIgnoredWhileConfirming(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 100, height: 30,
		confirm: &confirmState{prompt: "Kill 42? [y/N]"}}
	got, _ := m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonRight, Action: tea.MouseActionPress})
	if got.(model).mouseOff {
		t.Error("a right-click released the mouse out from under a confirmation")
	}
}

// Releasing the mouse must not depend on the terminal having obeyed. A
// multiplexer reporting on its own account, or one that swallows the
// disable, would otherwise leave clicks sorting columns out from under a
// drag the operator believes is a selection.
func TestNoMouseEventIsActedOnOnceReleased(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", sortDir: 1, width: 100, height: 30, mouseOff: true}
	for _, msg := range []tea.MouseMsg{
		{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 6, Y: m.listHeaderRow()},
		{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 6, Y: m.listFirstRow()},
		{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress, X: 6, Y: m.listFirstRow()},
		{Button: tea.MouseButtonRight, Action: tea.MouseActionPress, X: 6, Y: m.listFirstRow()},
	} {
		got, cmd := m.handleMouse(msg)
		gm := got.(model)
		if gm.sortKey != "pid" || gm.sortDir != 1 || gm.sel != "" || gm.detail != nil || cmd != nil {
			t.Errorf("%v was acted on after the mouse was released: sort=%s/%d sel=%q",
				msg.Button, gm.sortKey, gm.sortDir, gm.sel)
		}
		if !gm.mouseOff {
			t.Errorf("%v took the mouse back", msg.Button)
		}
	}
}

// Once the mouse is the terminal's, clicking a row and rolling the wheel do
// nothing and nothing on screen says why. The footer chip is the only
// standing signal — the toast expires.
func TestTheFooterSaysWhoHasTheMouse(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 100, height: 30}
	if out := stripANSI(m.renderFooter(200)); strings.Contains(out, "mouse") {
		t.Errorf("the footer mentions the mouse while whytop still has it: %s", out)
	}
	got, _ := m.toggleMouse()
	gm := got.(model)
	// The toast owns the footer while it is up; the chip is what is left
	// once it expires, which is the state this test is about.
	gm.toast = ""
	if out := stripANSI(gm.renderFooter(200)); !strings.Contains(out, "mouse: yours") {
		t.Errorf("the footer does not say the mouse was released: %s", out)
	}
	// And in the panel too, which is where most of the copying happens.
	gm.detail = &detailState{pid: 42}
	if out := stripANSI(gm.renderFooter(200)); !strings.Contains(out, "mouse: yours") {
		t.Errorf("the panel footer does not say the mouse was released: %s", out)
	}
}

// m is the way back, from wherever you are.
func TestMTakesTheMouseBack(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 100, height: 30, mouseOff: true}
	got, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	gm := got.(model)
	if gm.mouseOff {
		t.Fatal("m did not take the mouse back")
	}
	if cmd == nil {
		t.Fatal("m took the mouse back without telling the terminal")
	}
	if !strings.Contains(gm.toast, "clicks select rows") {
		t.Errorf("taking the mouse back said: %q", gm.toast)
	}
}

// y used to copy the journal over OSC 52. It is gone: selecting the lines
// you want beats a key that copies a buffer you cannot see, and a stale
// binding that silently does nothing is worse than no binding.
func TestYNoLongerCopiesTheJournal(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid", width: 100, height: 30,
		detail: &detailState{pid: 42, follow: true, journal: "boom\nagain\n"}}
	got, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	gm := got.(model)
	if cmd != nil {
		t.Error("y still runs a command in the panel")
	}
	if gm.toast != "" {
		t.Errorf("y still says something: %q", gm.toast)
	}
	// Pausing the live log was a side effect of y; it has no business
	// happening now that the key does nothing.
	if !gm.detail.follow {
		t.Error("y turned the live log off")
	}
	if foot := stripANSI(gm.renderFooter(200)); strings.Contains(foot, "copy log") {
		t.Errorf("the footer still offers the copy key: %s", foot)
	}
}

// The help is where someone goes when the mouse will not select. It has to
// name both ways out, and must not name the key that no longer exists.
func TestHelpExplainsHowToSelectText(t *testing.T) {
	m := model{snap: testSnap(), width: 100, height: 40, help: true}
	out := stripANSI(m.renderHelp(100))
	for _, want := range []string{"right-click", "select and copy"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help does not mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "clipboard") {
		t.Errorf("the help still offers the clipboard key:\n%s", out)
	}
}

// g leaves a filter behind that the operator never typed — "state:D", put
// there by jumping to the processes stuck on disk. Opening a search on top
// of it used to append to it: the first keystroke landed on the end of a
// word nobody wrote, in a scope nobody chose, and the search then matched
// nothing with no way to see why.
func TestSearchStartsCleanAfterAJump(t *testing.T) {
	// A box with processes stuck on disk: the finding whose jump applies a
	// state filter, which is the one this is about.
	snap := testSnap()
	for i := range snap.Procs {
		snap.Procs[i].State = "D"
	}
	m := model{snap: snap, sortKey: "cpu", width: 100, height: 30}
	got, _ := m.jumpToFinding()
	gm := got.(model)
	if gm.filter == "" {
		t.Fatalf("jumping to a blocked-process finding applied no filter (findings: %+v)", m.findings())
	}
	if !gm.filterFromJump {
		t.Fatal("a jump's filter is not marked as one")
	}
	got, _ = gm.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	gm = got.(model)
	if gm.filter != "" {
		t.Errorf("the search opened onto the jump's filter %q", gm.filter)
	}
	if gm.filterScope != scopeAll {
		t.Errorf("the search opened in the jump's scope %v, not every column", gm.filterScope)
	}
	if !gm.editing {
		t.Error("/ did not open the filter editor")
	}
}

// A filter the operator typed is theirs, and re-opening it to narrow it
// further is what / is for. Only a jump's filter is thrown away.
func TestSearchKeepsAFilterYouTyped(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "cpu", width: 100, height: 30}
	got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	for _, r := range "ngin" {
		got, _ = got.(model).handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	gm := got.(model)
	if gm.filter != "ngin" {
		t.Fatalf("typing built the filter %q", gm.filter)
	}
	got, _ = gm.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got, _ = got.(model).handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if gm2 := got.(model); gm2.filter != "ngin" {
		t.Errorf("re-opening the search threw away the typed filter: %q", gm2.filter)
	}
}

// A confirmation to destroy something has one job: let the operator
// recognise what they picked. The list shows the command line; p.Name is
// comm, and the two differ whenever argv[0] was set to something else, an
// interpreter is running a script, or a thread was renamed. Naming comm
// made it possible to read "whytop-victim-kill 9000" in the list and be
// asked "Force kill bash?" about that same row.
func TestAKillConfirmationNamesWhatTheRowNamed(t *testing.T) {
	cases := []struct {
		name string
		p    collect.Proc
		want string
	}{
		{"argv[0] renamed", collect.Proc{PID: 9, Name: "sleep", Cmdline: "whytop-victim-kill 9000"},
			"whytop-victim-kill (sleep)"},
		{"interpreter", collect.Proc{PID: 9, Name: "python3", Cmdline: "/usr/bin/python3 filler.py"},
			"/usr/bin/python3"},
		{"punctuation is not a different name", collect.Proc{PID: 9, Name: "nginx", Cmdline: "nginx: master process"},
			"nginx:"},
		{"comm is capped at 15 chars by the kernel", collect.Proc{PID: 9, Name: "systemd-journal", Cmdline: "/usr/lib/systemd/systemd-journald"},
			"/usr/lib/systemd/systemd-journald"},
		{"kernel thread, no cmdline", collect.Proc{PID: 9, Name: "kworker/0:1"}, "kworker/0:1"},
	}
	for _, c := range cases {
		if got := killLabel(c.p); got != c.want {
			t.Errorf("%s: killLabel = %q, want %q", c.name, got, c.want)
		}
	}
}

// And the prompt that reaches the screen has to carry it, not just the
// helper — both the list's k and the panel's x/X.
func TestBothKillPromptsUseThatName(t *testing.T) {
	snap := testSnap()
	snap.Procs[1].Name = "sleep" // nginx's row, running under a renamed argv[0]
	snap.Procs[1].Cmdline = "whytop-victim-kill 9000"
	m := model{snap: snap, sortKey: "pid", width: 140, height: 30, sel: "42"}

	got, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	gm := got.(model)
	if gm.confirm == nil {
		t.Fatal("k did not ask for confirmation")
	}
	if !strings.Contains(gm.confirm.prompt, "whytop-victim-kill") {
		t.Errorf("the list's kill prompt says: %s", gm.confirm.prompt)
	}

	m.detail = &detailState{pid: 42}
	got, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	gm = got.(model)
	if gm.confirm == nil {
		t.Fatal("X did not ask for confirmation")
	}
	if !strings.Contains(gm.confirm.prompt, "whytop-victim-kill") {
		t.Errorf("the panel's force-kill prompt says: %s", gm.confirm.prompt)
	}
}
