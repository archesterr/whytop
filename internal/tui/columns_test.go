package tui

import (
	"strings"
	"testing"

	"github.com/archesterr/whytop/internal/collect"
)

// The header is only clickable-to-sort if the column a click lands on is the
// same column drawn above the rows. Both come from procColumns, and this is
// what proves they agree: walk the rendered header and check the title under
// each x maps back to the key colAt reports.
func TestHeaderClickMapsToTheColumnUnderIt(t *testing.T) {
	for _, w := range []int{80, 132} {
		cols := procColumns(w)
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
	m := model{snap: testSnap(), tab: tabProcs, width: 132, sortKey: "cpu", sortDir: -1}

	// x over the USER column.
	cols := procColumns(m.contentW())
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
	m := model{snap: testSnap(), tab: tabProcs, width: 132, sortKey: "pid", sortDir: 1}
	cols := procColumns(m.contentW())
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

// Everything is drawn at contentW and centred with padLeft; a click's screen
// column is translated by the same amount. If these disagree, every click on
// a wide terminal lands on the wrong column.
func TestCentringAndClickOffsetAgree(t *testing.T) {
	m := model{width: 200, height: 40}
	if got := m.contentW(); got != maxContentW {
		t.Errorf("contentW on a 200-column terminal = %d, want %d", got, maxContentW)
	}
	if got := m.padLeft(); got != (200-maxContentW)/2 {
		t.Errorf("padLeft = %d, want the content centred", got)
	}
	narrow := model{width: 90, height: 30}
	if narrow.contentW() != 90 || narrow.padLeft() != 0 {
		t.Errorf("a terminal narrower than the cap should not be padded: w=%d pad=%d",
			narrow.contentW(), narrow.padLeft())
	}
}

// A count in the tab bar has to be worth the space it takes. "5 disks" isn't
// — you see them the moment you open the tab — while failed units are the
// whole reason to go there.
func TestTabCountsOnlyCarryUsefulNumbers(t *testing.T) {
	snap := &collect.Snapshot{
		Procs: []collect.Proc{{PID: 1, Cmdline: "/sbin/init"}},
		Disks: []collect.Disk{{Name: "sda"}, {Name: "sdb"}},
		NICs:  []collect.NIC{{Name: "eth0"}},
		Units: []collect.Unit{{Name: "ok.service", Active: "active"}, {Name: "bad.service", Active: "failed"}},

		UnitsCollected: true,
	}
	snap.ByPID = map[int32]int{1: 0}
	m := model{snap: snap, sortKey: "pid"}
	counts := m.tabCounts()
	if counts[tabDisks] != -1 || counts[tabNet] != -1 {
		t.Errorf("Disks/Network should carry no count, got %d/%d", counts[tabDisks], counts[tabNet])
	}
	if counts[tabUnits] != 1 {
		t.Errorf("Units should count only failed units, got %d", counts[tabUnits])
	}
	if !m.tabAlerts()[tabUnits] {
		t.Error("a failed unit should flag its tab")
	}

	snap.Units = []collect.Unit{{Name: "ok.service", Active: "active"}}
	if got := m.tabCounts()[tabUnits]; got != -1 {
		t.Errorf("with nothing failed the Units tab should show no number, got %d", got)
	}
}
