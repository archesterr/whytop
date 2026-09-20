package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// The top of the screen is the part you read without meaning to: it has to
// answer "is this machine in trouble, and which machine is it" from across a
// desk. The previous version crammed five labels, five numbers, five gauges
// and eight cores into three unframed lines, which put a lot of words in a
// small space and grouped none of them.
//
// This is one framed panel with the facts on its edge and the measurements
// inside, divided into fields. Height is spent deliberately: the panel is
// the thing people look at, and two more rows of it are worth more than two
// more rows of a process list that already scrolls.

// headerHeight is the number of screen rows the panel occupies: top border,
// the measurement row, the core grid, the rule, the verdict, bottom border.
// Every other height budget and every click's row is derived from it, so a
// panel that draws a different number of rows than it claims would send
// every click to the wrong place — which is what the test pins.
func (m model) headerHeight() int {
	return 5 + len(m.coreLayout().rows)
}

// maxCoreRows caps what the core grid may spend. The panel is worth height,
// but not unboundedly: a 96-core machine on an 80-column terminal would
// otherwise take nineteen rows and leave no room for the process list it
// exists to introduce.
const maxCoreRows = 4

// coreGrid is a chosen layout for the per-core meters: how wide each bar is,
// how many fit per row, and which rows result.
type coreGrid struct {
	barW   int
	perRow int
	rows   [][]int // core indexes
	hidden int
}

// coreLayout picks the widest bar that still fits every core within
// maxCoreRows, and only then starts leaving cores out. Bars shrink before
// cores disappear, because knowing that core 63 is pinned matters more than
// how precisely its bar is drawn.
func (m model) coreLayout() coreGrid {
	n := len(m.snap.CPU.PerCore)
	if n == 0 {
		return coreGrid{}
	}
	inner := boxInner(m.contentW())
	digits := len(strconv.Itoa(n - 1))
	if digits < 1 {
		digits = 1
	}
	best := coreGrid{barW: 2, perRow: 1}
	for barW := 8; barW >= 2; barW-- {
		cell := digits + barW + 3 // label + ▕bar▏ + the space between cells
		perRow := inner / cell
		if perRow < 1 {
			continue
		}
		if rows := (n + perRow - 1) / perRow; rows <= maxCoreRows {
			best = coreGrid{barW: barW, perRow: perRow}
			break
		}
		best = coreGrid{barW: barW, perRow: perRow} // the tightest so far
	}
	shown := n
	if max := best.perRow * maxCoreRows; shown > max {
		shown = max
	}
	for start := 0; start < shown; start += best.perRow {
		end := start + best.perRow
		if end > shown {
			end = shown
		}
		row := make([]int, 0, end-start)
		for i := start; i < end; i++ {
			row = append(row, i)
		}
		best.rows = append(best.rows, row)
	}
	best.hidden = n - shown
	return best
}

func (m model) renderHeaderPanel(w int) string {
	s := m.snap
	var lines []string

	// The title bar carries identity — which machine, which build — because
	// those never change while you watch, and anything that never changes is
	// noise once it is inside the panel with the numbers that do.
	logo := "whytop"
	if m.opt.Version != "" {
		logo += " " + m.opt.Version
	}
	ident := []string{safeText(s.Host)}
	if s.OS != "" {
		os := s.OS
		if s.Kernel != "" {
			os += " (" + s.Kernel + ")"
		}
		ident = append(ident, safeText(os))
	}
	ident = append(ident, fmt.Sprintf("up %s", dur(s.Uptime)),
		fmt.Sprintf("%d cores", s.CPU.Cores), fmt.Sprintf("%d tasks", len(s.Procs)))
	title := logo
	if m.remote != nil {
		title += "  ssh " + safeText(m.hostLabel())
	}
	title += "  " + strings.Join(ident, " · ")

	clock := time.Now().Format("15:04:05")
	state := "● live"
	if m.paused {
		state = "❚❚ paused"
	}
	right := state + "  " + clock
	if !s.Root && m.remote == nil {
		right = "limited view — run with sudo   " + right
	}
	lines = append(lines, boxTop(w, title, right))

	inner := boxInner(w)
	lines = append(lines, boxLine(w, columns(inner, m.cpuField(), m.memField(), m.loadField())))
	for _, row := range m.coreRowsRendered(inner) {
		lines = append(lines, boxLine(w, row))
	}
	_ = inner
	lines = append(lines, boxRule(w))
	lines = append(lines, boxLine(w, m.renderStatus(inner)))
	lines = append(lines, boxBottom(w))
	return strings.Join(lines, "\n")
}

// field renders one measurement: a label, the number big enough to read at a
// glance, a gauge, and the breakdown behind it. Every field has the same
// shape, so the eye learns one layout instead of five.
func field(w int, label string, value string, valStyle, hue lipgloss.Style, frac float64, sub string) string {
	head := stLabel.Render(label) + "  " + valStyle.Bold(true).Render(value)
	used := visLen(head)
	// The gauge takes what is left after the words, within reason: past
	// about twenty columns a bar stops reading as a measurement and starts
	// reading as a rule.
	barW := w - used - visLen(sub) - 3
	if barW > 20 {
		barW = 20
	}
	if barW >= 6 {
		fill := hue
		if fg := valStyle.GetForeground(); fg == colWarn || fg == colCrit {
			fill = valStyle
		}
		head += "  " + gauge(frac, barW, fill)
	}
	if sub != "" {
		head += "  " + stMuted.Render(sub)
	}
	return head
}

func (m model) cpuField() string {
	c := m.snap.CPU
	sub := fmt.Sprintf("us %s  sy %s  wa %s", f1(c.User), f1(c.System), f1(c.Iowait))
	if c.Steal >= 1 {
		sub += "  st " + f1(c.Steal)
	}
	return field(m.fieldW(), "CPU", f1(c.Busy)+"%", lvl(c.Busy, 70, 90), stAccent, c.Busy/100, sub)
}

func (m model) memField() string {
	mem := m.snap.Mem
	sub := fmt.Sprintf("%s / %s", bytesFmt(float64(mem.Used)), bytesFmt(float64(mem.Total)))
	if mem.SwapTotal > 0 && mem.SwapUsed > 0 {
		sub += "  swap " + bytesFmt(float64(mem.SwapUsed))
	}
	return field(m.fieldW(), "MEM", f1(mem.UsedPct)+"%", lvl(mem.UsedPct, 80, 92),
		lipgloss.NewStyle().Foreground(colMem), mem.UsedPct/100, sub)
}

// loadField carries load, blocked tasks and pressure together: they are the
// three ways a machine says "there is more work than I can do", and reading
// them as one field is closer to how you actually think about it.
func (m model) loadField() string {
	s := m.snap
	cores := float64(s.CPU.Cores)
	if cores < 1 {
		cores = 1
	}
	per := s.Load1 / cores
	blocked := 0
	for _, p := range s.Procs {
		if p.State == "D" {
			blocked++
		}
	}
	sub := fmt.Sprintf("%.2f %.2f  %d blocked", s.Load5, s.Load15, blocked)
	if s.PSI.Available {
		sub += "  psi io " + f1(s.PSI.IOSome)
	}
	return field(m.fieldW(), "LOAD", fmt.Sprintf("%.2f", s.Load1), lvl(per, 0.7, 1),
		lipgloss.NewStyle().Foreground(colLoad), per, sub)
}

func (m model) fieldW() int {
	return boxInner(m.contentW()) / 3
}

// coreRowsRendered lays the per-core meters out in a grid. A 12-core box
// averaging 8% looks idle right up until you notice one core pinned at 100%
// — a single-threaded bottleneck — and the average in the CPU field is
// exactly the statistic that hides it.
func (m model) coreRowsRendered(inner int) []string {
	cores := m.snap.CPU.PerCore
	g := m.coreLayout()
	if len(g.rows) == 0 {
		return nil
	}
	digits := len(strconv.Itoa(max0(len(cores) - 1)))
	var rows []string
	for r, idxs := range g.rows {
		var b strings.Builder
		for n, i := range idxs {
			v := cores[i]
			// Per-core thresholds sit higher than the machine-wide ones:
			// one core at 90% is normal on any box doing work, where the
			// whole machine at 90% is not.
			fill := stCore
			switch {
			case v >= 95:
				fill = stCrit
			case v >= 80:
				fill = stWarn
			}
			if n > 0 {
				b.WriteString(" ")
			}
			b.WriteString(stFaint.Render(pad2(strconv.Itoa(i), digits)))
			b.WriteString(stBox2.Render("▕"))
			b.WriteString(gauge(v/100, g.barW, fill))
			b.WriteString(stBox2.Render("▏"))
		}
		if r == len(g.rows)-1 && g.hidden > 0 {
			b.WriteString(stFaint.Render(fmt.Sprintf("  +%d more cores", g.hidden)))
		}
		rows = append(rows, truncateANSI(b.String(), inner))
	}
	return rows
}
