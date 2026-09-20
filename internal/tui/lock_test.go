package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/collect"
)

func lockSnap(cpus ...float64) *collect.Snapshot {
	var procs []collect.Proc
	for i, c := range cpus {
		procs = append(procs, collect.Proc{
			PID: int32(100 + i), Name: "app", User: "root", State: "S",
			Cmdline: "/usr/bin/app", CPU: c,
		})
	}
	snap := &collect.Snapshot{Procs: procs, ByPID: map[int32]int{}}
	for i, p := range procs {
		snap.ByPID[p.PID] = i
	}
	return snap
}

func pids(list []collect.Proc) []int32 {
	out := make([]int32, len(list))
	for i, p := range list {
		out[i] = p.PID
	}
	return out
}

func sameOrder(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The whole point of the lock: the numbers keep updating, the rows don't
// move. Without it, reaching for the row you were looking at is a race with
// the next refresh — and on a busy box you lose.
func TestLockedOrderSurvivesChangingUsage(t *testing.T) {
	m := model{snap: lockSnap(90, 50, 10), sortKey: "cpu", sortDir: -1}
	before := pids(m.procRows()) // 100, 101, 102
	if !sameOrder(before, []int32{100, 101, 102}) {
		t.Fatalf("unsorted baseline: %v", before)
	}

	locked, _ := m.handleListKey(keyRunes("L"))
	m = locked.(model)
	if !m.lockOrder {
		t.Fatal("L should lock the order")
	}

	// The load inverts completely under the lock.
	m.snap = lockSnap(1, 2, 99)
	if got := pids(m.procRows()); !sameOrder(got, before) {
		t.Errorf("locked rows moved when usage changed: %v, want %v", got, before)
	}

	// And unlocking lets them find their new places.
	unlocked, _ := m.handleListKey(keyRunes("L"))
	if got := pids(unlocked.(model).procRows()); !sameOrder(got, []int32{102, 101, 100}) {
		t.Errorf("unlocking should re-sort by live usage, got %v", got)
	}
}

// A process that starts while the order is locked still has to be visible —
// it just can't shove the rows someone is reading out of position.
func TestProcessesStartedUnderTheLockGoToTheBottom(t *testing.T) {
	m := model{snap: lockSnap(90, 50), sortKey: "cpu", sortDir: -1, lockOrder: true}
	m.relock()

	snap := lockSnap(90, 50, 100) // the newcomer is the busiest thing on the box
	m.snap = snap
	if got := pids(m.procRows()); !sameOrder(got, []int32{100, 101, 102}) {
		t.Errorf("a new process should append under the frozen rows, got %v", got)
	}
}

// Sorting while locked has to do something, or the lock reads as broken.
func TestChangingSortWhileLockedRefreezesTheNewOrder(t *testing.T) {
	m := model{snap: lockSnap(90, 50, 10), sortKey: "cpu", sortDir: -1, lockOrder: true}
	m.relock()

	flipped, _ := m.handleListKey(keyRunes("S")) // reverse the direction
	fm := flipped.(model)
	if got := pids(fm.procRows()); !sameOrder(got, []int32{102, 101, 100}) {
		t.Errorf("reversing the sort under the lock should re-order once, got %v", got)
	}
	// ...and then hold that new order against changing usage.
	fm.snap = lockSnap(1, 1, 1)
	if got := pids(fm.procRows()); !sameOrder(got, []int32{102, 101, 100}) {
		t.Errorf("the re-frozen order should hold, got %v", got)
	}
}

// The footer has to say which state the list is in. "L order" alone doesn't
// tell you whether the rows under you are moving.
func TestFooterNamesTheLockState(t *testing.T) {
	m := model{snap: lockSnap(1), sortKey: "cpu", tab: tabProcs, width: 200}
	if out := stripANSI(m.renderFooter(200)); !strings.Contains(out, "order: live") {
		t.Errorf("an unlocked list should say so:\n%s", out)
	}
	m.lockOrder = true
	if out := stripANSI(m.renderFooter(200)); !strings.Contains(out, "order: LOCKED") {
		t.Errorf("a locked list should say so:\n%s", out)
	}
}

// The Units tab is gone; unit editing moved to the process that belongs to
// the unit. Pressing e on a process systemd didn't start has to explain
// itself rather than silently doing nothing.
func TestEditUnitFromProcessDetail(t *testing.T) {
	snap := detailSnap()
	m := model{snap: snap, detail: &detailState{pid: 10, loaded: true}}
	if _, cmd := m.handleDetailKey(keyRunes("e")); cmd == nil {
		t.Error("e on a systemd-managed process should start the editor")
	}

	snap.Procs[0].Unit = ""
	got, _ := m.handleDetailKey(keyRunes("e"))
	if !strings.Contains(got.(model).toast, "not started by systemd") {
		t.Errorf("e on a non-systemd process should explain why nothing opened, got %q", got.(model).toast)
	}
}

func TestUnitsTabIsGone(t *testing.T) {
	if numTabs != 4 {
		t.Fatalf("numTabs = %d, want 4", numTabs)
	}
	if n := len(tabRegions(map[tab]int{})); n != 4 {
		t.Errorf("tab bar draws %d tabs, want 4", n)
	}
	m := model{snap: lockSnap(1), sortKey: "cpu", tab: tabProcs}
	for i := 0; i < numTabs+1; i++ {
		next, _ := m.handleListKey(tea.KeyMsg{Type: tea.KeyTab})
		m = next.(model) // must not panic on tab.String()
		_ = m.tab.String()
	}
}
