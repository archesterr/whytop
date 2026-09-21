package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The journal is copied whole, not as much of it as happened to fit on
// screen. The line that explains a failure is rarely the last one, and a
// "copy" that silently gave you the visible tail would be worse than none.
func TestCopyingTheJournalTakesTheWholeBuffer(t *testing.T) {
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, "Sep 21 10:00:00 web-07 nginx[1234]: line "+strings.Repeat("x", i%40))
	}
	full := strings.Join(lines, "\n")

	m := model{snap: testSnap(), sortKey: "cpu", width: 120, height: 30,
		detail: &detailState{pid: 1, loaded: true, journal: full, follow: true}}

	// What the panel draws is a fraction of it — that is the whole problem.
	drawn := stripANSI(m.renderDetail(120, 30))
	if strings.Count(drawn, "nginx[1234]") >= len(lines) {
		t.Fatal("the panel drew the entire journal, so this test is not testing anything")
	}

	got, cmd := m.Update(runes("y"))
	if cmd == nil {
		t.Fatal("y produced no command, so nothing was copied")
	}
	if n := countLines(full); n != len(lines) {
		t.Errorf("counted %d lines, want %d", n, len(lines))
	}
	// Following is paused, because a live log that scrolls on while you read
	// what you just copied is why you wanted it in the clipboard.
	if got.(model).detail.follow {
		t.Error("copying the journal left the live log running")
	}
	if !strings.Contains(stripANSI(got.(model).renderFooter(120)), "Copied 200 lines") {
		t.Errorf("the toast does not say what was copied: %s", stripANSI(got.(model).renderFooter(120)))
	}
}

// An empty journal says so instead of silently copying nothing, which is
// indistinguishable from the clipboard not working at all.
func TestCopyingAnEmptyJournalSaysSo(t *testing.T) {
	for _, j := range []string{"", "   \n  \n"} {
		m := model{snap: testSnap(), sortKey: "cpu", width: 120, height: 30,
			detail: &detailState{pid: 1, loaded: true, journal: j, follow: true}}
		got, _ := m.Update(runes("y"))
		foot := stripANSI(got.(model).renderFooter(120))
		if !strings.Contains(foot, "Nothing in the journal") {
			t.Errorf("copying %q said: %s", j, foot)
		}
		if !got.(model).detail.follow {
			t.Error("a failed copy should not also turn the live log off")
		}
	}
}

func TestCountLines(t *testing.T) {
	cases := map[string]int{"": 0, "\n": 0, "one": 1, "one\n": 1, "one\ntwo": 2, "one\ntwo\n": 2}
	for in, want := range cases {
		if got := countLines(in); got != want {
			t.Errorf("countLines(%q) = %d, want %d", in, got, want)
		}
	}
}

// Releasing the mouse is what makes native selection work anywhere on the
// screen, so it has to work from every view — including the panels where
// the text people want is a mount path or a PID rather than a log line.
func TestMouseCanBeHandedBackFromAnywhere(t *testing.T) {
	views := map[string]model{
		"the process list": {},
		"a process panel":  {detail: &detailState{pid: 1, loaded: true}},
		"the help":         {help: true},
	}
	for name, m := range views {
		m.snap, m.sortKey, m.width, m.height = testSnap(), "cpu", 120, 40

		got, cmd := m.Update(runes("m"))
		if !got.(model).mouseOff {
			t.Errorf("m did not release the mouse from %s", name)
		}
		if cmd == nil {
			t.Errorf("m released the mouse from %s without telling the terminal", name)
		}
		if !strings.Contains(stripANSI(got.(model).renderFooter(120)), "Mouse released") {
			t.Errorf("releasing the mouse from %s said nothing", name)
		}

		// And back again.
		back, cmd := got.(model).Update(runes("m"))
		if back.(model).mouseOff {
			t.Errorf("m did not take the mouse back from %s", name)
		}
		if cmd == nil {
			t.Errorf("taking the mouse back from %s did not re-enable reporting", name)
		}
	}
}

// m is a letter, and the filter is where letters are text. Typing "memcached"
// must not hand the terminal its mouse back halfway through.
func TestMIsTextWhileTypingAFilter(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "cpu", width: 120, height: 40, editing: true}
	got, _ := m.Update(runes("m"))
	if got.(model).mouseOff {
		t.Error("typing m into the filter released the mouse")
	}
	if got.(model).filter != "m" {
		t.Errorf("the filter is %q after typing m", got.(model).filter)
	}
}

// Both halves of the answer are findable without reading the source: the
// help screen is where someone looks after discovering they cannot select.
func TestHelpExplainsHowToCopy(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "cpu", width: 140, height: 40, help: true}
	out := stripANSI(m.View())
	for _, want := range []string{"select and copy", "copy the whole journal"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help never mentions %q", want)
		}
	}
}

// The OSC 52 sequence is a control sequence, not content: it must go
// straight to the terminal rather than through the renderer, which would
// measure and truncate it to the width of a box.
func TestClipboardCommandProducesNoFrameContent(t *testing.T) {
	if msg := copyToClipboard("some journal text")(); msg != nil {
		t.Errorf("the copy command returned %v into the frame", msg)
	}
	if cmd := copyToClipboard(""); cmd() != nil {
		t.Error("copying nothing produced a message")
	}
	var _ tea.Cmd = copyToClipboard("x")
}
