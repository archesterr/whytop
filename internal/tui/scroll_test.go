package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// runes builds the key message for a typed character, which is how every
// key in the list and the filter editor arrives.
func runes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// The list opens on its first process. It used to open centred on the
// selection, which meant the first thing on screen was the middle of the
// process list with a third of it already scrolled off above — and the row
// you were told was "248 of 449" was nowhere near the top.
func TestListStartsAtTheFirstRow(t *testing.T) {
	m := model{}
	start, end := m.window(400, -1, 20)
	if start != 0 {
		t.Errorf("an unselected list starts at row %d, want 0", start)
	}
	if end != 19 {
		t.Errorf("drew rows [0,%d), want 19 data rows and one for the notice", end)
	}
	// And with the first row selected, which is where a fresh selection lands.
	if start, _ = m.window(400, 0, 20); start != 0 {
		t.Errorf("selecting the first row scrolled to %d", start)
	}
}

// Walking down a long list must not drag the rows with the cursor: the list
// holds still until the cursor reaches the bottom edge, and then moves by
// exactly one row per keystroke.
func TestCursorMovesBeforeTheListDoes(t *testing.T) {
	const total, budget = 400, 21 // 20 data rows
	m := model{}
	for idx := 0; idx < 20; idx++ {
		m.top = clampTop(m.top, idx, total, 20)
		if start, _ := m.window(total, idx, budget); start != 0 {
			t.Fatalf("the list scrolled to %d while the cursor was still on row %d", start, idx)
		}
	}
	// Row 20 is one past the bottom: now it scrolls, by one.
	m.top = clampTop(m.top, 20, total, 20)
	start, end := m.window(total, 20, budget)
	if start != 1 || end != 21 {
		t.Errorf("stepping off the bottom drew [%d,%d), want [1,21)", start, end)
	}
}

// Walking back up scrolls the other way, also one row at a time. A viewport
// with no memory can only express "at the top" or "selection glued to the
// bottom", which drags every row up the screen with the cursor.
func TestScrollingBackUpMovesOneRowAtATime(t *testing.T) {
	const total, dataRows = 400, 20
	m := model{top: 100}
	// The cursor sits mid-screen; moving it up inside the viewport moves
	// nothing.
	for _, idx := range []int{115, 110, 105, 100} {
		m.top = clampTop(m.top, idx, total, dataRows)
		if m.top != 100 {
			t.Fatalf("the list moved to %d while the cursor was on row %d, inside it", m.top, idx)
		}
	}
	// One more step is off the top edge.
	m.top = clampTop(m.top, 99, total, dataRows)
	if m.top != 99 {
		t.Errorf("stepping off the top scrolled to %d, want 99", m.top)
	}
}

// The end of the list is the end: the viewport stops with the last row on
// screen rather than scrolling into empty space past it.
func TestScrollStopsAtTheEnd(t *testing.T) {
	const total, dataRows = 50, 20
	if got := clampTop(1000, 49, total, dataRows); got != total-dataRows {
		t.Errorf("scrolled past the end to %d, want %d", got, total-dataRows)
	}
	if got := clampTop(45, -1, total, dataRows); got != total-dataRows {
		t.Errorf("a stale position past the end settled at %d, want %d", got, total-dataRows)
	}
}

// A list shorter than the viewport never scrolls at all.
func TestShortListNeverScrolls(t *testing.T) {
	m := model{top: 7}
	start, end := m.window(5, 4, 20)
	if start != 0 || end != 5 {
		t.Errorf("a 5-row list in a 20-row viewport drew [%d,%d), want [0,5)", start, end)
	}
}

// Everything that changes which rows exist sends the list back to the top.
// A scroll position four hundred rows into an unfiltered list means nothing
// once a filter has narrowed it to eight.
func TestChangingTheRowSetResetsTheScroll(t *testing.T) {
	changes := map[string]func(m model) model{
		"sort": func(m model) model { out, _ := m.sortBy("mem"); return out.(model) },
		"tree": func(m model) model { out, _ := m.toggleTree(); return out.(model) },
		"kernel threads": func(m model) model {
			out, _ := m.handleListKey(runes("K"))
			return out.(model)
		},
		"invert": func(m model) model {
			out, _ := m.handleListKey(runes("I"))
			return out.(model)
		},
		"typing a filter": func(m model) model {
			m.editing = true
			out, _ := m.handleEditKey(runes("n"))
			return out.(model)
		},
		"another host": func(m model) model { m.resetForHost(); return m },
	}
	for name, change := range changes {
		m := model{snap: testSnap(), sortKey: "cpu", width: 120, height: 40, top: 300}
		if got := change(m); got.top != 0 {
			t.Errorf("%s left the list scrolled to row %d", name, got.top)
		}
	}
}

// A window that just got smaller has to be laid out for the size it is now.
// Every row of every frame is budgeted against the width and height, so a
// resize invalidates all of it, and the process list is what the remaining
// height is for.
func TestShrinkingTheWindowRelaysOutTheWholeFrame(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "cpu", width: 200, height: 50, top: 40}

	got, cmd := m.Update(tea.WindowSizeMsg{Width: 90, Height: 26})
	sm := got.(model)
	if sm.width != 90 || sm.height != 26 {
		t.Fatalf("size is %dx%d after the resize, want 90x26", sm.width, sm.height)
	}
	// bubbletea's renderer only repaints the lines it believes changed, and
	// after a shrink the lines it leaves alone are the ones too wide to fit.
	if cmd == nil {
		t.Error("a resize should force a repaint, not a diff against the old frame")
	}
	// A scroll position forty rows into a list that now shows twelve is not
	// a position the operator chose, it is left over from a bigger window.
	if sm.top != 0 {
		t.Errorf("the list is still scrolled to row %d after the resize", sm.top)
	}
	for i, line := range strings.Split(sm.View(), "\n") {
		if got := visLen(line); got > 90 {
			t.Errorf("line %d is %d columns in a 90-column window: %q", i, got, stripANSI(line))
		}
	}
}

// The panel's appetite scales with the window. Half of forty-four rows still
// leaves a usable list; half of twenty-four leaves five rows of processes
// under a panel that has taken everything else.
func TestShortWindowsGetMoreOfTheListAndLessOfThePanel(t *testing.T) {
	prev, prevList := 0, 0
	for _, h := range []int{20, 24, 28, 30, 40, 50} {
		// A machine with enough cores that the panel would happily fill
		// whatever it is given, which is the case the share exists for.
		m := model{snap: headerSnap(16), sortKey: "cpu", width: 120, height: h}
		panel, list := m.headerHeight(), m.listBudget(h)
		if panel > h/2 {
			t.Errorf("h=%d: the panel takes %d rows, over half the window", h, panel)
		}
		if list < 3 {
			t.Errorf("h=%d: the list is down to %d rows", h, list)
		}
		// Neither half of the screen may grow when the window shrinks.
		if panel < prev {
			t.Errorf("h=%d: the panel grew to %d rows on a shorter window than %d", h, panel, prev)
		}
		if list < prevList {
			t.Errorf("h=%d: the list shrank to %d rows on a taller window (was %d)", h, list, prevList)
		}
		prev, prevList = panel, list
	}
}
