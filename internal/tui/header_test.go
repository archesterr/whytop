package tui

import (
	"strings"
	"testing"

	"github.com/archesterr/whytop/internal/collect"
)

func headerSnap(cores int) *collect.Snapshot {
	per := make([]float64, cores)
	for i := range per {
		per[i] = float64(i%100) + 0.5
	}
	return &collect.Snapshot{
		Host: "web-07", OS: "Ubuntu 24.04.1 LTS", Kernel: "6.8.0-45-generic",
		Root:  true,
		CPU:   collect.CPU{Cores: cores, PerCore: per, Busy: 42.5, User: 30, System: 10, Iowait: 2.5},
		Mem:   collect.Mem{Total: 64 << 30, Used: 20 << 30, UsedPct: 31.2, SwapTotal: 8 << 30, SwapUsed: 1 << 30, SwapPct: 12.5},
		Load1: 3.5, Load5: 2.9, Load15: 2.1,
		Procs: []collect.Proc{{PID: 1, Name: "init", State: "S", Threads: 1}},
	}
}

// The panel's height is what every click's row and every other height
// budget is derived from. If it claims a height it does not draw, the
// column headers move and every click lands on the wrong row — which is
// exactly the bug this pins, across the machine shapes that change the
// panel's height: core count, terminal width, terminal height.
func TestHeaderHeightMatchesWhatItDraws(t *testing.T) {
	for _, cores := range []int{1, 2, 4, 8, 16, 32, 64, 96, 128} {
		for _, w := range []int{60, 80, 100, 110, 132, 160, 200, 300} {
			for _, h := range []int{20, 24, 30, 40, 60} {
				m := model{snap: headerSnap(cores), width: w, height: h}
				got := len(strings.Split(m.renderHeaderPanel(w), "\n"))
				if want := m.headerHeight(); got != want {
					t.Fatalf("cores=%d w=%d h=%d: drew %d rows, headerHeight() says %d",
						cores, w, h, got, want)
				}
			}
		}
	}
}

// Every line of the panel must be exactly the terminal's width: a short one
// leaves the frame open on the right, and a long one wraps and pushes the
// whole screen down a row.
func TestHeaderPanelLinesAreExactlyTheWidth(t *testing.T) {
	for _, cores := range []int{1, 4, 12, 64, 96} {
		for _, w := range []int{40, 60, 80, 100, 110, 132, 200} {
			m := model{snap: headerSnap(cores), width: w, height: 40}
			for i, line := range strings.Split(m.renderHeaderPanel(w), "\n") {
				if got := visLen(line); got != w {
					t.Errorf("cores=%d w=%d: line %d is %d columns, want %d: %q",
						cores, w, i, got, w, stripANSI(line))
				}
			}
		}
	}
}

// The panel is worth height — it is the part of the screen people look at
// — but not the whole screen: a 128-core box must still leave a usable
// process list on an ordinary terminal.
func TestHeaderLeavesRoomForTheList(t *testing.T) {
	for _, cores := range []int{4, 64, 128, 256} {
		for _, h := range []int{24, 30, 40, 60} {
			m := model{snap: headerSnap(cores), width: 120, height: h}
			if got := m.headerHeight(); got > h*5/9 {
				t.Errorf("cores=%d h=%d: the panel takes %d of %d rows", cores, h, got, h)
			}
		}
	}
}

// Cores that don't fit are counted, not dropped silently: "+12 more" is the
// difference between a grid that is showing you part of the machine and one
// that appears to be showing you all of it.
func TestHeaderSaysHowManyCoresItHid(t *testing.T) {
	m := model{snap: headerSnap(256), width: 80, height: 24}
	g := m.coreLayout(boxInner(80))
	shown := 0
	for _, r := range g.rows {
		shown += len(r)
	}
	if shown+g.hidden != 256 {
		t.Fatalf("%d cores shown + %d hidden != 256", shown, g.hidden)
	}
	if g.hidden > 0 && !strings.Contains(stripANSI(m.renderHeaderPanel(80)), "more") {
		t.Errorf("%d cores are hidden but the panel doesn't say so", g.hidden)
	}
}

// The core grid is two cores to a row — htop's shape, and the reason the
// panel has any height at all. One long line of eight cores is a status
// line, not a panel you read the machine from.
func TestCoreGridUsesTwoColumns(t *testing.T) {
	for _, cores := range []int{2, 4, 8, 12, 16} {
		for _, w := range []int{100, 150, 200} {
			m := model{snap: headerSnap(cores), width: w, height: 44}
			g := m.coreLayout(boxInner(w))
			if len(g.rows) == 0 {
				t.Fatalf("cores=%d w=%d: no core band drawn", cores, w)
			}
			if got := len(g.rows[0]); got != coreCols {
				t.Errorf("cores=%d w=%d: %d cores on the first row, want %d",
					cores, w, got, coreCols)
			}
			if want := (cores + coreCols - 1) / coreCols; len(g.rows) != want {
				t.Errorf("cores=%d w=%d: %d rows, want %d", cores, w, len(g.rows), want)
			}
		}
	}
}

// More cores than the rows can hold means more per row, not cores silently
// left out — the grid only starts hiding once even the narrowest cell will
// not fit.
func TestCoreGridPacksRatherThanHidesWhenItCan(t *testing.T) {
	m := model{snap: headerSnap(64), width: 200, height: 44}
	g := m.coreLayout(boxInner(200))
	if g.hidden != 0 {
		t.Errorf("64 cores at 200x44 hid %d of them", g.hidden)
	}
	if len(g.rows) > m.maxCoreRows() {
		t.Errorf("grid is %d rows, over the %d budget", len(g.rows), m.maxCoreRows())
	}
}

// A swap meter is only worth its row when the number moves, but a machine
// with no swap still has to say so rather than leave a gap where a meter
// was — "none configured" is an answer, a blank line is not.
func TestSwapMeterSaysWhenThereIsNoSwap(t *testing.T) {
	s := headerSnap(4)
	s.Mem.SwapTotal, s.Mem.SwapUsed, s.Mem.SwapPct = 0, 0, 0
	m := model{snap: s, width: 120, height: 40}
	if !strings.Contains(stripANSI(m.renderHeaderPanel(120)), "none configured") {
		t.Error("a machine with no swap should say so in the SWP meter")
	}
}

// Every meter keeps its bar: at 80 columns the breakdown behind the
// percentage is what gets cut, never the bar, because the bar is the reason
// the panel can be read without being read.
func TestMetersKeepTheirBarWhenNarrow(t *testing.T) {
	for _, w := range []int{80, 100, 132, 200} {
		m := model{snap: headerSnap(4), width: w, height: 30}
		out := stripANSI(m.renderHeaderPanel(w))
		for _, label := range []string{"CPU", "MEM", "SWP"} {
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(line, "│ "+label+" ") || strings.HasPrefix(strings.TrimSpace(line), "│ "+label) {
					if !strings.Contains(line, "▕") {
						t.Errorf("w=%d: the %s meter lost its bar: %q", w, label, line)
					}
				}
			}
		}
	}
}

// Pathologically small terminals aren't a target, but they must not panic:
// a panic in View takes down the whole session.
func TestHeaderExtremeSizesDontPanic(t *testing.T) {
	for _, cores := range []int{0, 1, 300} {
		for _, w := range []int{-5, 0, 1, 2, 5, 10, 20, 39} {
			for _, h := range []int{0, 1, 5, 24} {
				m := model{snap: headerSnap(cores), width: w, height: h}
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("cores=%d w=%d h=%d panicked: %v", cores, w, h, r)
						}
					}()
					m.renderHeaderPanel(w)
					m.headerHeight()
				}()
			}
		}
	}
}

// The panel earns its height by carrying what a troubleshooter opens the
// tool to find out. A full disk takes a machine down as surely as a full
// memory, and nothing else on this screen would have mentioned it.
func TestHeaderShowsDiskAndNetwork(t *testing.T) {
	s := headerSnap(4)
	s.FS = []collect.FS{
		{Mount: "/", Total: 100 << 30, Used: 40 << 30, Free: 60 << 30, UsedPct: 40},
		{Mount: "/var/lib/containers", Total: 50 << 30, Used: 47 << 30, Free: 3 << 30, UsedPct: 94},
	}
	s.NICs = []collect.NIC{{Name: "lo", RxBps: 1 << 30}, {Name: "eth0", RxBps: 1 << 20, TxBps: 2 << 20}}
	m := model{snap: s, width: 160, height: 44}
	out := stripANSI(m.renderHeaderPanel(160))

	if !strings.Contains(out, "DISK") || !strings.Contains(out, "94") {
		t.Errorf("the fullest mount should be metered, got:\n%s", out)
	}
	if strings.Contains(out, "/ ") || !strings.Contains(out, "containers") {
		t.Error("the DISK meter should name the fullest mount, not the first one")
	}
	if !strings.Contains(out, "Net") {
		t.Error("aggregate network throughput should be on the panel")
	}
	// Loopback is excluded: traffic a process sends to itself is not
	// traffic the machine is carrying, and 1 GiB/s of it would read as a
	// saturated uplink.
	if strings.Contains(out, "1.0 GiB/s") {
		t.Error("loopback traffic is being counted in the network total")
	}
}

// A stale mount — a dead NFS server — reports nothing believable, and
// showing it as 0% full is worse than not showing it.
func TestHeaderSkipsStaleMounts(t *testing.T) {
	s := headerSnap(4)
	s.FS = []collect.FS{
		{Mount: "/nfs", Stale: true, Total: 1 << 40, UsedPct: 99},
		{Mount: "/", Total: 100 << 30, Used: 40 << 30, Free: 60 << 30, UsedPct: 40},
	}
	m := model{snap: s, width: 160, height: 44}
	_, sub, pct, ok := m.fullestFS()
	if !ok {
		t.Fatal("no mount metered at all")
	}
	if strings.Contains(sub, "nfs") || pct != 40 {
		t.Errorf("the DISK meter picked the stale mount: %q at %v%%", sub, pct)
	}
	// It is still worth saying out loud, just as "not responding" rather
	// than as a fullness percentage nobody should believe.
	if !strings.Contains(stripANSI(m.renderHeaderPanel(160)), "not responding") {
		t.Error("a stale mount should still be reported on the verdict line")
	}
}
