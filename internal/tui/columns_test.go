package tui

import (
	"strings"
	"testing"
)

// The header is only clickable-to-sort if the column a click lands on is the
// same column drawn above the rows. Both come from procColumns, and this is
// what proves they agree: walk the rendered header and check the title under
// each x maps back to the key colAt reports.
func TestHeaderClickMapsToTheColumnUnderIt(t *testing.T) {
	for _, w := range []int{80, 132} {
		cols := procColumns(w, true)
		at := 0
		for _, c := range cols {
			mid := at + c.w/2
			if got := colAt(cols, mid); got != c.key {
				t.Errorf("w=%d: click at x=%d is over %q but resolved to %q", w, mid, c.key, got)
			}
			at += c.w + sepW
		}
		if got := colAt(cols, 100000); got != "" {
			t.Errorf("w=%d: click past the last column resolved to %q", w, got)
		}
	}
}

func TestClickingHeaderSortsAndReversesOnSecondClick(t *testing.T) {
	m := model{snap: testSnap(), width: 132, sortKey: "cpu", sortDir: -1}

	// x over the USER column.
	cols := m.cols(m.contentW())
	at, userX := 0, -1
	for _, c := range cols {
		if c.key == "user" {
			userX = at + 1
		}
		at += c.w + sepW
	}
	if userX < 0 {
		t.Fatal("no USER column in the layout")
	}

	got, _ := m.clickHeader(userX)
	gm := got.(model)
	if gm.sortKey != "user" {
		t.Fatalf("clicking USER sorted by %q", gm.sortKey)
	}
	if gm.sortDir != 1 {
		t.Errorf("a text column should sort A-to-Z first, got dir %d", gm.sortDir)
	}

	got2, _ := gm.clickHeader(userX)
	if got2.(model).sortDir != -1 {
		t.Errorf("clicking the sorted column again should reverse it, got dir %d", got2.(model).sortDir)
	}
}

func TestClickingNumericHeaderSortsBiggestFirst(t *testing.T) {
	m := model{snap: testSnap(), width: 132, sortKey: "pid", sortDir: 1}
	cols := m.cols(m.contentW())
	at, memX := 0, -1
	for _, c := range cols {
		if c.key == "mem" {
			memX = at + 1
		}
		at += c.w + sepW
	}
	got, _ := m.clickHeader(memX)
	gm := got.(model)
	if gm.sortKey != "mem" || gm.sortDir != -1 {
		t.Errorf("clicking MEM gave key=%q dir=%d, want mem/-1 (biggest first)", gm.sortKey, gm.sortDir)
	}
	list := gm.procRows()
	for i := 1; i < len(list); i++ {
		if list[i-1].RSS < list[i].RSS {
			t.Fatalf("rows not sorted by memory descending: %+v", list)
		}
	}
}

func TestSortByTextColumnIsAlphabetical(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "user", sortDir: 1}
	list := m.procRows()
	for i := 1; i < len(list); i++ {
		if list[i-1].User > list[i].User {
			t.Fatalf("USER sort is not alphabetical: %+v", list)
		}
	}
}

// The sorted column has to say so where the user is looking, not only in the
// footer — that arrow is the whole affordance that makes the header look
// clickable.
func TestSortedColumnHeaderShowsDirectionArrow(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "mem", sortDir: -1, width: 132}
	head := stripANSI(strings.Split(m.renderProcs(132, 20), "\n")[0])
	if !strings.Contains(head, "MEM▾") {
		t.Errorf("descending MEM sort should mark its header, got %q", head)
	}
	m.sortDir = 1
	head = stripANSI(strings.Split(m.renderProcs(132, 20), "\n")[0])
	if !strings.Contains(head, "MEM▴") {
		t.Errorf("ascending MEM sort should flip the arrow, got %q", head)
	}
}

// The name column takes leftover width but stays capped: the alternative is
// "eth0" rendered in a 66-character cell on a full-screen terminal.
func TestNameColumnIsCappedOnWideTerminals(t *testing.T) {
	vals := []int{9, 9, 8, 8, 7, 7}
	if got := nameColW(132, 24, vals); got != 24 {
		t.Errorf("name column on a 132-column terminal = %d, want it capped at 24", got)
	}
	// Narrow: it gets whatever is actually left, not the cap.
	narrow := nameColW(70, 24, vals)
	want := 70 - (9 + 9 + 8 + 8 + 7 + 7) - 6*sepW
	if narrow != want {
		t.Errorf("name column on a 70-column terminal = %d, want %d", narrow, want)
	}
	if narrow >= 24 {
		t.Errorf("a narrow terminal should not reach the cap, got %d", narrow)
	}
}

// The UI uses the whole terminal, and a click's screen column is the column
// the renderer laid out. If contentW and padLeft ever disagree again, every
// click on a wide terminal lands on the wrong column.
func TestFullWidthAndClickOffsetAgree(t *testing.T) {
	for _, w := range []int{80, 132, 200, 400} {
		m := model{width: w, height: 40}
		if got := m.contentW(); got != w {
			t.Errorf("contentW on a %d-column terminal = %d, want the whole terminal", w, got)
		}
		if got := m.padLeft(); got != 0 {
			t.Errorf("padLeft on a %d-column terminal = %d, want 0", w, got)
		}
	}
}

// Full width means the process table actually reaches the right edge — the
// whole point of dropping the 132-column cap. COMMAND takes the slack.
func TestProcTableFillsAWideTerminal(t *testing.T) {
	for _, w := range []int{80, 190, 300} {
		total := 0
		for i, c := range procColumns(w, true) {
			total += c.w
			if i > 0 {
				total += sepW
			}
		}
		if total != w {
			t.Errorf("columns at width %d sum to %d — the table does not fill the terminal", w, total)
		}
	}
}
