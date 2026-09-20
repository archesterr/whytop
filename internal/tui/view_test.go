package tui

import (
	"strings"
	"testing"

	"github.com/archesterr/whytop/internal/collect"
)

// Regression coverage for the column-separator addition: every table must
// still fit an 80-column terminal, the same bug class fixed earlier for the
// vitals row and header — a width budget that "mostly" accounts for the
// separators it adds is worse than one that doesn't add them at all.
func TestAllTablesFit80ColumnsWithSeparators(t *testing.T) {
	snap := &collect.Snapshot{
		Host: "myserver", CPU: collect.CPU{Cores: 4},
		Procs:          []collect.Proc{{PID: 1, Name: "init", User: "root", Unit: "init.scope", Cmdline: "/sbin/init"}},
		Conns:          []collect.Conn{{Proto: "tcp", LocalIP: "0.0.0.0", LPort: 22, State: "LISTEN", PID: 1}},
		ConnsCollected: true,
		Disks:          []collect.Disk{{Name: "sda"}},
		FS:             []collect.FS{{Mount: "/", Device: "/dev/sda1", Type: "ext4"}},
		NICs:           []collect.NIC{{Name: "eth0"}},
		TCP:            collect.TCP{Available: true},
	}
	m := model{snap: snap, sortKey: "cpu", width: 80, height: 30}
	byPID := map[int32]int{}
	for i, p := range snap.Procs {
		byPID[p.PID] = i
	}
	snap.ByPID = byPID

	checks := map[string]string{
		"procs":  m.renderProcs(80, 20),
		"footer": m.renderFooter(80),
	}
	for name, out := range checks {
		for _, line := range strings.Split(out, "\n") {
			if got := visLen(line); got > 80 {
				t.Errorf("%s: line overflows 80-col terminal: width=%d line=%q", name, got, line)
			}
		}
	}
}

// A standard 80x24 SSH terminal must never be overflowed by a fixed-layout
// row (one that isn't allowed to wrap without breaking the rest of the
// screen's line budget) — regression coverage for a real bug where the
// vitals row's minimum column width silently broke its own width budget on
// exactly this, the single most common terminal size there is.
func TestRenderVitalsFits80Columns(t *testing.T) {
	m := model{snap: &collect.Snapshot{
		CPU: collect.CPU{Cores: 4, Busy: 12.3, User: 8, System: 4},
		Mem: collect.Mem{Total: 16 << 30, Used: 4 << 30, UsedPct: 25},
	}}
	for _, line := range strings.Split(m.renderHeaderPanel(80), "\n") {
		if got := visLen(line); got > 80 {
			t.Errorf("vitals line overflows 80-col terminal: width=%d line=%q", got, line)
		}
	}
}

func TestRenderHeaderFitsNarrowTerminalWithLongHostname(t *testing.T) {
	m := model{snap: &collect.Snapshot{
		Host: strings.Repeat("very-long-hostname-", 5), // way over 40 chars
		CPU:  collect.CPU{Cores: 4},
		Root: true,
	}}
	for _, w := range []int{40, 80, 120} {
		m.width = w
		for i, line := range strings.Split(m.renderHeaderPanel(w), "\n") {
			if got := visLen(line); got > w {
				t.Errorf("header at width %d: line %d is %d columns: %q",
					w, i, got, stripANSI(line))
			}
		}
	}
}

// Pathologically small terminals aren't a realistic target, but they must
// not crash the program — a panic in View() takes down the whole session.
func TestRenderHeaderExtremeWidthsDontPanic(t *testing.T) {
	m := model{snap: &collect.Snapshot{Host: "myserver", CPU: collect.CPU{Cores: 4}, Root: true}}
	for _, w := range []int{-5, 0, 1, 2, 5, 10, 20} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("renderHeader(%d) panicked: %v", w, r)
				}
			}()
			m.renderHeaderPanel(w)
		}()
	}
}

// Whole-frame fuzz: drive View() itself across a grid of terminal sizes,
// including tiny and empty snapshots, and just assert it never panics. This
// is the cheapest way to catch a slice-bounds or negative-width crash
// anywhere in the render chain without hand-writing a case for each one.
func TestViewNeverPanicsAcrossSizes(t *testing.T) {
	snaps := []*collect.Snapshot{
		{}, // zero-value snapshot: no procs, no disks, nothing collected yet
		{Host: "h", CPU: collect.CPU{Cores: 1}, Procs: []collect.Proc{{PID: 1, Name: "init"}}},
	}
	for _, snap := range snaps {
		for _, w := range []int{0, 1, 10, 40, 80, 200} {
			for _, h := range []int{0, 1, 5, 24, 100} {
				m := model{snap: snap, width: w, height: h, sortKey: "cpu"}
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("View() panicked at w=%d h=%d: %v", w, h, r)
						}
					}()
					m.View()
				}()
			}
		}
	}
}

// A container host routinely has dozens to hundreds of veth interfaces —

func TestRenderVitalsNoWrapWithBlockedProcesses(t *testing.T) {
	m := model{snap: &collect.Snapshot{
		CPU:   collect.CPU{Cores: 2, Iowait: 5},
		Procs: []collect.Proc{{PID: 1, State: "D"}, {PID: 2, State: "D"}},
	}}
	for _, line := range strings.Split(m.renderHeaderPanel(80), "\n") {
		if got := visLen(line); got > 80 {
			t.Errorf("vitals line with D-state processes overflows: width=%d", got)
		}
	}
}

// Regression coverage for a real race: two toasts shown within toastTTL of
// each other used to let the OLDER toast's already-scheduled clear timer
// blank out the NEWER toast early, since both timers just set m.toast = "".
// This drives the generation-matching logic directly (clearToastMsg{gen})
// rather than through the real tea.Tick timer, which isn't the thing under
// test and would otherwise make this test block for real wall-clock time.
func TestToastGenerationPreventsEarlyClear(t *testing.T) {
	m := &model{}
	m.showToast("first", true)
	oldGen := m.toastGen
	m.showToast("second", false)
	if m.toast != "second" {
		t.Fatalf("toast = %q, want %q", m.toast, "second")
	}

	m2, _ := m.Update(clearToastMsg{gen: oldGen})
	got := m2.(model)
	if got.toast != "second" {
		t.Errorf("an older toast's clear timer wiped a newer toast: toast=%q, want %q still showing", got.toast, "second")
	}

	m3, _ := got.Update(clearToastMsg{gen: got.toastGen})
	if m3.(model).toast != "" {
		t.Errorf("the current toast's own clear timer should have cleared it, toast=%q", m3.(model).toast)
	}
}

// Every frame the program draws must close the boxes it opens, in every
// view and at every size. A panel taller than the space it was given used
// to push its own bottom border off the screen, which left the box open
// with the footer sitting inside it — and because the footer is pinned to
// the last row, it looked like a rendering glitch rather than a layout bug.
func TestEveryFrameClosesItsBoxes(t *testing.T) {
	snap := testSnap()
	views := map[string]func(m *model){
		"list":   func(m *model) {},
		"help":   func(m *model) { m.help = true },
		"tree":   func(m *model) { m.tree = true },
		"detail": func(m *model) { m.detail = &detailState{pid: 42} },
		"hosts":  func(m *model) { m.hosts = &hostPanel{} },
	}
	for name, setup := range views {
		for _, w := range []int{60, 80, 100, 150, 200} {
			for _, h := range []int{10, 14, 16, 20, 24, 30, 50} {
				m := model{snap: snap, sortKey: "cpu", width: w, height: h}
				setup(&m)
				lines := strings.Split(m.View(), "\n")
				opens, closes := 0, 0
				for _, l := range lines {
					switch {
					case strings.HasPrefix(stripANSI(l), boxTL):
						opens++
					case strings.HasPrefix(stripANSI(l), boxBL):
						closes++
					}
				}
				if opens != closes {
					t.Errorf("%s at %dx%d: opened %d boxes, closed %d", name, w, h, opens, closes)
				}
				if len(lines) > h {
					t.Errorf("%s at %dx%d: drew %d lines, more than the terminal has", name, w, h, len(lines))
				}
			}
		}
	}
}
