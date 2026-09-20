package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/archesterr/whytop/internal/collect"
)

// lipgloss strips every colour when it can't see a TTY, which `go test` is.
// A test about what the styling actually does has to turn it back on, or it
// asserts against plain text and passes no matter what the code paints.
func withColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// Every cell in this file builds its own ANSI string instead of going
// through cell(), so each one has to be held to the width contract cell()
// enforces for free. A cell that overruns by one column shifts every column
// to its right on that row only, which looks like corrupted data.
func TestPaintedCellsRespectTheirWidth(t *testing.T) {
	withColor(t) // width has to hold with the escape sequences present, too
	long := "/usr/local/bin/environment-manager task-run --stdin --session cse_0162 --mode resume"
	for _, w := range []int{1, 2, 8, 20, 40, 120} {
		for _, sel := range []bool{false, true} {
			if got := visLen(cmdCell(long, w, sel)); got != w {
				t.Errorf("cmdCell(w=%d, sel=%v) = %d columns", w, sel, got)
			}
			if got := visLen(userCell("systemd-resolve", w, sel)); got != w {
				t.Errorf("userCell(w=%d, sel=%v) = %d columns", w, sel, got)
			}
			if got := visLen(memCell(900<<20, 16<<30, w, sel)); got != w {
				t.Errorf("memCell(w=%d, sel=%v) = %d columns", w, sel, got)
			}
		}
		if got := visLen(gauge(0.42, w, stOK)); got != w {
			t.Errorf("gauge(w=%d) = %d columns", w, got)
		}
	}
}

// The point of the two-tone command is that the program's name is styled
// differently from the path it sits in and from its arguments. If they all
// collapse to one style the column is back to being a wall of grey text.
func TestCmdCellSeparatesProgramFromPathAndArgs(t *testing.T) {
	withColor(t)
	out := cmdCell("/usr/sbin/nginx -g daemon off;", 60, false)
	if stripANSI(out) != pad2("/usr/sbin/nginx -g daemon off;", 60) {
		t.Errorf("the visible text changed: %q", stripANSI(out))
	}
	dir := strings.Index(out, "/usr/sbin/")
	prog := strings.Index(out, "nginx")
	args := strings.Index(out, "-g daemon")
	if dir < 0 || prog < 0 || args < 0 {
		t.Fatalf("expected all three parts present: %q", out)
	}
	// Each part must be preceded by its own escape sequence rather than
	// inheriting the previous one.
	for _, at := range []int{prog, args} {
		if !strings.Contains(out[:at], "\x1b[") {
			t.Errorf("part at %d carries no style of its own: %q", at, out)
		}
	}
	if strings.Count(out, "\x1b[") < 3 {
		t.Errorf("expected three distinct styles, got %q", out)
	}
}

// A gauge is only useful if a small non-zero value is visibly different from
// zero — "0.4% busy" and "idle" must not draw the same bar.
func TestGaugeShowsSomethingForASmallValue(t *testing.T) {
	if strings.Contains(stripANSI(gauge(0, 10, stOK)), "▇") {
		t.Error("an idle metric should draw an empty bar")
	}
	if !strings.Contains(stripANSI(gauge(0.004, 10, stOK)), "▇") {
		t.Error("a metric that is on at all should draw at least one block")
	}
	if n := strings.Count(stripANSI(gauge(2.5, 10, stOK)), "▇"); n != 10 {
		t.Errorf("an over-100%% value should clamp to a full bar, got %d blocks", n)
	}
}

// The vitals row is two physical lines with a fixed budget; the gauges are
// drawn inside that budget, not on top of it.
func TestVitalsRowNeverOverflows(t *testing.T) {
	withColor(t)
	snap := &collect.Snapshot{
		CPU:   collect.CPU{Cores: 12, Busy: 99.9, User: 80, System: 19.9, Iowait: 44},
		Mem:   collect.Mem{Total: 31 << 30, Used: 30 << 30, UsedPct: 96.8},
		PSI:   collect.PSI{Available: true, CPUSome: 88.8, MemSome: 12, IOSome: 99.9},
		Load1: 87.65,
	}
	m := model{snap: snap}
	for _, w := range []int{80, 100, 132, 190, 400} {
		for _, line := range strings.Split(m.renderVitals(w), "\n") {
			if got := visLen(line); got > w {
				t.Errorf("vitals at w=%d: line is %d columns: %q", w, got, stripANSI(line))
			}
		}
	}
}
