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
// desk.
//
// It is laid out as a band of per-core meters across the full width, and
// under it a row of columns divided by rules: the machine-wide meters, the
// numbers that move, and the facts that don't. Everything in the columns
// carries a label, which is the whole difference between this and the run of
// middle-dotted words it replaces — six things fitted into one row is how
// you make all six unreadable.
//
// The panel is worth its height. It is the part of the screen people
// actually look at, and rows spent here are worth more than rows spent on a
// process list that already scrolls.

// headerHeight is how many screen rows the panel occupies. Every other
// height budget and every click's row is derived from it, so it is computed
// from the body the renderer will actually draw rather than from a formula
// kept in step by hand — a panel that draws a different number of rows than
// it claims sends every click to the wrong place.
func (m model) headerHeight() int {
	// The border, the rule, the verdict line, the bottom border.
	return len(m.headerBody(boxInner(m.contentW()))) + 4
}

// maxCoreRows caps what the per-core grid may spend. The grid is the best
// thing on the screen on a box with a single-threaded bottleneck, but a
// 96-core machine would otherwise take the whole terminal, so it is bounded
// by a share of the screen's height rather than by a constant: a tall
// terminal can afford to show every core, and a 24-row SSH session can't.
func (m model) maxCoreRows() int {
	h := m.height
	if h <= 0 {
		h = 30
	}
	// What is left of half the screen once the panel's fixed rows — three
	// meters, the identity line, and the frame's own four — have had
	// theirs. The panel is worth height, but a machine with ninety-six
	// cores must not push the process list off the bottom of a 24-row SSH
	// session; the cores that don't fit are counted instead.
	n := h/2 - 8
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

// coreGrid is a chosen layout for the per-core meters: how wide each bar is,
// whether each carries its own percentage, and which cores land on which row.
type coreGrid struct {
	barW   int
	digits int
	pct    bool
	rows   [][]int // core indexes
	hidden int
}

// Bounds on one core's meter. A bar under four columns can't show anything
// but "busy or not", and past forty it has stopped being a measurement and
// become a rule across the screen.
const (
	minCoreBar = 4
	maxCoreBar = 40
	corePctW   = 5 // " 100%"
)

// coreLayout picks the grid for n cores in a band paneW columns wide.
//
// It chooses the fewest rows that will hold every core, and then spends all
// the width that leaves on the bars themselves, rather than packing the
// cores tightly and leaving the rest of the band empty. That is what makes
// a four-core box look like four wide meters and a sixty-four-core box look
// like a grid, from the same code and without a special case for either.
func (m model) coreLayout(paneW int) coreGrid {
	n := len(m.snap.CPU.PerCore)
	if n == 0 || paneW < 10 {
		return coreGrid{}
	}
	digits := len(strconv.Itoa(max0(n - 1)))
	if digits < 1 {
		digits = 1
	}
	maxRows := m.maxCoreRows()

	fit := func(pct bool) (coreGrid, bool) {
		pctW := 0
		if pct {
			pctW = corePctW
		}
		fixed := digits + 2 + pctW // "12" + "▕▏" + the percentage
		minCell := fixed + minCoreBar
		for rows := 1; rows <= maxRows; rows++ {
			perRow := (n + rows - 1) / rows
			if (paneW+1)/(minCell+1) < perRow {
				continue
			}
			barW := (paneW+1)/perRow - 1 - fixed
			if barW > maxCoreBar {
				barW = maxCoreBar
			}
			g := coreGrid{barW: barW, digits: digits, pct: pct}
			return m.fillRows(g, perRow, n, maxRows), true
		}
		return coreGrid{}, false
	}
	// The percentage beside each bar is worth real width — it is the
	// difference between "that core looks busy" and "that core is at 97%" —
	// so it is given up only when the cores would not otherwise fit.
	if g, ok := fit(true); ok {
		return g
	}
	if g, ok := fit(false); ok {
		return g
	}
	// More cores than the band can hold at any size: pack them at the
	// minimum and say how many are left over.
	perRow := (paneW + 1) / (digits + 2 + minCoreBar + 1)
	if perRow < 1 {
		perRow = 1
	}
	barW := (paneW+1)/perRow - 1 - digits - 2
	return m.fillRows(coreGrid{barW: barW, digits: digits}, perRow, n, maxRows)
}

// cellW is the width of one core's meter as laid out.
func (g coreGrid) cellW() int {
	w := g.digits + 2 + g.barW
	if g.pct {
		w += corePctW
	}
	return w
}

func (m model) fillRows(g coreGrid, perRow, n, maxRows int) coreGrid {
	if g.barW < 1 {
		g.barW = 1
	}
	shown := n
	if lim := perRow * maxRows; shown > lim {
		shown = lim
	}
	for start := 0; start < shown; start += perRow {
		end := start + perRow
		if end > shown {
			end = shown
		}
		row := make([]int, 0, end-start)
		for i := start; i < end; i++ {
			row = append(row, i)
		}
		g.rows = append(g.rows, row)
	}
	g.hidden = n - shown
	return g
}

// The panel's interior is a band of per-core meters across the full width,
// and under it a row of columns: the machine-wide meters, the numbers that
// move, and the facts that don't. Every value in the columns carries a
// label, which is the whole difference between this and the row of
// middle-dotted words it replaces.
const (
	colSepW    = 3 // " │ "
	factLabelW = 8 // the label column inside a fact line
	// A column narrower than this cannot hold a meter and its breakdown,
	// and splitting into one more column than the width supports is how
	// every value in the panel ends up truncated with an ellipsis.
	minColW   = 34
	threeColW = 3*45 + 2*colSepW
)

// headerCols decides how many columns the lower half is divided into and how
// wide each one is. The last column takes the rounding slack so the row ends
// exactly on the frame.
func headerCols(inner int) []int {
	n := 1
	switch {
	case inner >= threeColW:
		n = 3
	case inner >= 2*minColW+colSepW:
		n = 2
	}
	if n == 1 {
		return []int{inner}
	}
	each := (inner - colSepW*(n-1)) / n
	out := make([]int, n)
	for i := range out {
		out[i] = each
	}
	out[n-1] = inner - (each+colSepW)*(n-1)
	return out
}

// headerBody renders the panel's interior, already divided and padded to the
// exact inner width. The renderer and headerHeight both go through it, so
// they cannot disagree about how tall the panel is — and a panel that draws
// a different number of rows than it claims sends every click to the wrong
// row.
func (m model) headerBody(inner int) []string {
	if m.snap == nil || inner < 4 {
		return nil
	}
	widths := headerCols(inner)
	out := m.coreLines(inner)

	var groups [][]string
	switch len(widths) {
	case 3:
		groups = [][]string{m.meterLines(widths[0]), m.loadLines(widths[1]), m.identLines(widths[2])}
	case 2:
		groups = [][]string{m.meterLines(widths[0]), m.loadLines(widths[1])}
	default:
		groups = [][]string{append(m.meterLines(widths[0]), m.loadLines(widths[0])...)}
	}

	rows := 0
	for _, g := range groups {
		if len(g) > rows {
			rows = len(g)
		}
	}
	sep := " " + stBox2.Render(boxV) + " "
	for i := 0; i < rows; i++ {
		cells := make([]string, 0, len(groups))
		for gi, g := range groups {
			line := ""
			if i < len(g) {
				line = g[i]
			}
			cells = append(cells, pad(truncateANSI(line, widths[gi]), widths[gi], false))
		}
		out = append(out, strings.Join(cells, sep))
	}
	// With fewer than three columns the identity of the machine — uptime,
	// distribution, kernel — is one line across the full width instead of a
	// column of its own. It is the least volatile thing on the panel, so it
	// is the thing that can afford to be read rather than scanned.
	if len(widths) < 3 {
		out = append(out, m.identRow(inner))
	}
	// On a terminal too short to hold both, the panel gives way rather than
	// the list: a process list squeezed to nothing is a monitor that has
	// stopped monitoring. Rows go from the bottom, which is where the least
	// urgent of them are.
	if cap := m.headerBodyCap(); len(out) > cap {
		out = out[:max0(cap)]
	}
	return out
}

// headerBodyCap is how many interior rows the panel may draw and still
// leave the list three rows, its two borders, the footer, and the terminal's
// reserved last row.
func (m model) headerBodyCap() int {
	h := m.height
	if h <= 0 {
		h = 30
	}
	return h - 11
}

// ruleJunctions is where the column dividers meet the rule below them, in
// absolute screen columns.
func ruleJunctions(widths []int) []int {
	if len(widths) < 2 {
		return nil
	}
	var at []int
	off := 0
	for i := 0; i < len(widths)-1; i++ {
		off += widths[i]
		at = append(at, boxInset+off+1)
		off += colSepW
	}
	return at
}

func (m model) renderHeaderPanel(w int) string {
	s := m.snap
	inner := boxInner(w)

	// The title bar carries identity and nothing else: which tool, which
	// machine. Everything that used to be crowded in beside it — the
	// distribution, the kernel, the uptime, the core and task counts — is
	// now a labelled line in the facts pane, where it is read rather than
	// decoded.
	title := "whytop"
	if m.opt.Version != "" {
		title += " " + m.opt.Version
	}
	if m.remote != nil {
		title += "  ssh " + safeText(m.hostLabel())
	}
	title += "  " + safeText(s.Host)

	clock := time.Now().Format("15:04:05")
	right := "● live  " + clock
	if m.paused {
		right = "❚❚ paused  " + clock
	}

	lines := []string{boxTop(w, title, right)}
	body := m.headerBody(inner)
	for _, l := range body {
		lines = append(lines, boxLine(w, l))
	}
	// The rule closes every column at once, with a junction wherever a
	// divider meets it — the detail that makes a split panel look built
	// rather than merely overlaid.
	if at := ruleJunctions(headerCols(inner)); len(at) > 0 && len(body) > 0 {
		lines = append(lines, boxRuleAt(w, at...))
	} else {
		lines = append(lines, boxRule(w))
	}
	lines = append(lines, boxLine(w, m.renderStatus(inner)))
	lines = append(lines, boxBottom(w))
	return strings.Join(lines, "\n")
}

// meterLines draws the three machine-wide meters in a fixed column layout:
// label, bar, percentage, then the breakdown behind it. They share one
// layout so the eye learns it once, and they are aligned so the three
// percentages can be compared without reading the labels.
func (m model) meterLines(w int) []string {
	s := m.snap
	cpuSub := fmt.Sprintf("us %s  sy %s  wa %s", f1(s.CPU.User), f1(s.CPU.System), f1(s.CPU.Iowait))
	if s.CPU.Steal >= 1 {
		cpuSub += "  st " + f1(s.CPU.Steal)
	}
	memSub := fmt.Sprintf("%s of %s", bytesFmt(float64(s.Mem.Used)), bytesFmt(float64(s.Mem.Total)))

	swapPct, swapSub := 0.0, "none configured"
	swapStyle := stFaint
	if s.Mem.SwapTotal > 0 {
		swapPct = s.Mem.SwapPct
		swapSub = fmt.Sprintf("%s of %s", bytesFmt(float64(s.Mem.SwapUsed)), bytesFmt(float64(s.Mem.SwapTotal)))
		// Swap in use is worth noticing long before it is full: a box that
		// has started swapping is already slower than its numbers suggest.
		swapStyle = lvl(swapPct, 1, 25)
	}
	// One bar width for all three, chosen by the longest breakdown, so the
	// three bars start and end in the same columns and the three
	// percentages line up. Three meters whose bars are three different
	// lengths is three measurements you have to read one at a time, which
	// is the opposite of what a meter is for.
	barW := meterBarW(w, cpuSub, memSub, swapSub)
	return []string{
		meter(w, barW, "CPU", f1(s.CPU.Busy)+"%", lvl(s.CPU.Busy, 70, 90), stAccent, s.CPU.Busy/100, cpuSub),
		meter(w, barW, "MEM", f1(s.Mem.UsedPct)+"%", lvl(s.Mem.UsedPct, 80, 92),
			lipgloss.NewStyle().Foreground(colMem), s.Mem.UsedPct/100, memSub),
		meter(w, barW, "SWP", f1(swapPct)+"%", swapStyle,
			lipgloss.NewStyle().Foreground(colLoad), swapPct/100, swapSub),
	}
}

// meterBarW is the bar width the meters share: what is left once the widest
// of their breakdowns has its room.
//
// The bar outranks the breakdown when they compete. "us 1.0 sy 0.7 wa 0" is
// worth having, but the bar is the reason you can tell at a glance that the
// machine is fine, and a meter with no meter in it is a label and a number.
// So the bar keeps a floor and the breakdown is what gets cut.
func meterBarW(w int, subs ...string) int {
	longest := 0
	for _, s := range subs {
		if n := visLen(s); n > longest {
			longest = n
		}
	}
	barW := w - meterLabelW - meterValW - longest - 5
	switch {
	case barW > 24:
		barW = 24
	case barW < minMeterBar:
		barW = minMeterBar
	}
	// Narrower than this and there is no room for a bar at all, so the
	// numbers get the whole line.
	if w-meterLabelW-meterValW-5-barW < 0 {
		return 0
	}
	return barW
}

const minMeterBar = 8

const (
	meterLabelW = 4
	meterValW   = 6
)

// meter renders one machine-wide measurement in fixed columns: label, bar,
// percentage, then the breakdown behind it.
func meter(w, barW int, label, value string, valStyle, hue lipgloss.Style, frac float64, sub string) string {
	const labelW, valW = meterLabelW, meterValW
	// The bar takes what is left after the words, within reason: past about
	// twenty-four columns a bar stops reading as a measurement and starts
	// reading as a rule.
	//
	// Getting the bar's width wrong doesn't overflow the frame — boxLine
	// cuts the line to fit — it silently eats the end of the breakdown,
	// which is the part that says what the percentage is made of.
	out := withPad(stLabel.Render(label), labelW)
	if barW >= minMeterBar {
		fill := hue
		if fg := valStyle.GetForeground(); fg == colWarn || fg == colCrit {
			fill = valStyle
		}
		out += stBox2.Render("▕") + gauge(frac, barW, fill) + stBox2.Render("▏") + " "
	}
	out += lpad(valStyle.Bold(true).Render(value), valW)
	// Whatever the bar left over. truncate() before styling, never after:
	// cutting already-styled text slices through the escape sequences.
	if room := max0(w - visLen(out) - 2); sub != "" && room > 0 {
		out += "  " + stMuted.Render(truncate(sub, room))
	}
	return out
}

// loadLines is the column of numbers that move but have no bar: how much
// work is queued, and whether the machine is waiting on disk rather than
// doing it.
func (m model) loadLines(w int) []string {
	s := m.snap
	var out []string
	add := func(label, value string) {
		out = append(out, withPad(stLabel.Render(label), factLabelW)+truncateANSI(value, max0(w-factLabelW)))
	}

	threads, running, blocked := 0, 0, 0
	for _, p := range s.Procs {
		threads += int(p.Threads)
		switch p.State {
		case "R":
			running++
		case "D":
			blocked++
		}
	}
	tasks := stBig.Render(strconv.Itoa(len(s.Procs))) + stMuted.Render(" tasks  ") +
		stPlain.Render(strconv.Itoa(threads)) + stMuted.Render(" thr  ") +
		stOK.Render(strconv.Itoa(running)) + stMuted.Render(" run")
	if blocked > 0 {
		// Blocked tasks are the most actionable number on the panel: they
		// mean something is wedged on I/O right now, not merely busy.
		tasks += "  " + stCrit.Bold(true).Render(strconv.Itoa(blocked)) + stCrit.Render(" blocked")
	}
	add("Tasks", tasks)

	cores := float64(s.CPU.Cores)
	if cores < 1 {
		cores = 1
	}
	per := s.Load1 / cores
	add("Load", fmt.Sprintf("%s %s %s  %s",
		lvl(per, 0.7, 1).Bold(true).Render(fmt.Sprintf("%.2f", s.Load1)),
		stPlain.Render(fmt.Sprintf("%.2f", s.Load5)),
		stPlain.Render(fmt.Sprintf("%.2f", s.Load15)),
		stMuted.Render(fmt.Sprintf("%.2f per core", per))))

	// I/O wait and pressure answer the same question from two directions —
	// "is this machine waiting on disk rather than working" — so they read
	// as one line.
	io := lvl(s.CPU.Iowait, 3, 10).Render(f1(s.CPU.Iowait)+"%") + stMuted.Render(" iowait")
	if s.PSI.Available {
		io += "   " + lvl(s.PSI.IOSome, 10, 30).Render(f1(s.PSI.IOSome)) + stMuted.Render(" psi io")
	}
	add("Disk", io)
	return out
}

// identLines is what is simply true about the machine: how long it has been
// up, what it runs, and whether this view of it is complete.
func (m model) identLines(w int) []string {
	s := m.snap
	var out []string
	add := func(label, value string) {
		out = append(out, withPad(stLabel.Render(label), factLabelW)+truncateANSI(value, max0(w-factLabelW)))
	}
	add("Uptime", stPlain.Render(dur(s.Uptime))+stMuted.Render("  ·  "+strconv.Itoa(s.CPU.Cores)+" cores"))
	add("OS", stPlain.Render(safeText(s.OS)))
	add("Kernel", stMuted.Render(safeText(s.Kernel)))
	// Without root, per-process I/O for other users' processes reads as
	// "hidden" rather than as zero. That is a property of the view, not of
	// the machine, so it belongs with the facts about the view.
	if !s.Root && m.remote == nil {
		add("Access", stWarn.Render("limited")+stMuted.Render(" — sudo for full I/O"))
	}
	return out
}

// identRow is identLines folded onto one full-width line, for a terminal too
// narrow to give it a column of its own.
func (m model) identRow(w int) string {
	s := m.snap
	parts := []string{stLabel.Render("Uptime ") + stPlain.Render(dur(s.Uptime)),
		stLabel.Render("Cores ") + stPlain.Render(strconv.Itoa(s.CPU.Cores))}
	if s.OS != "" {
		parts = append(parts, stLabel.Render("OS ")+stPlain.Render(safeText(s.OS)))
	}
	if s.Kernel != "" {
		parts = append(parts, stLabel.Render("Kernel ")+stMuted.Render(safeText(s.Kernel)))
	}
	if !s.Root && m.remote == nil {
		parts = append(parts, stWarn.Render("limited — sudo for full I/O"))
	}
	return truncateANSI(strings.Join(parts, stFaint.Render("  ·  ")), w)
}

// coreLines lays the per-core meters out in a grid. A 12-core box averaging
// 8% looks idle right up until you notice one core pinned at 100% — a
// single-threaded bottleneck — and the average in the CPU meter is exactly
// the statistic that hides it.
func (m model) coreLines(w int) []string {
	cores := m.snap.CPU.PerCore
	g := m.coreLayout(w)
	if len(g.rows) == 0 {
		return nil
	}
	var rows []string
	hidden := g.hidden
	for r, idxs := range g.rows {
		// A grid that leaves cores out has to say so, and the notice needs
		// room of its own: appended to a row that already fills the band it
		// was simply cut off, and the panel then claimed to be showing a
		// whole machine it was showing two thirds of.
		notice := ""
		if r == len(g.rows)-1 && hidden > 0 {
			for {
				notice = fmt.Sprintf("  +%d more", hidden)
				if len(idxs) == 0 || g.cellW()*len(idxs)+len(idxs)-1+len(notice) <= w {
					break
				}
				idxs = idxs[:len(idxs)-1]
				hidden++
			}
		}
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
			b.WriteString(stFaint.Render(lpadPlain(strconv.Itoa(i), g.digits)))
			b.WriteString(stBox2.Render("▕"))
			b.WriteString(gauge(v/100, g.barW, fill))
			b.WriteString(stBox2.Render("▏"))
			if g.pct {
				b.WriteString(lpad(fill.Render(strconv.Itoa(int(v+0.5))+"%"), corePctW))
			}
		}
		if notice != "" {
			b.WriteString(stFaint.Render(notice))
		}
		rows = append(rows, truncateANSI(b.String(), w))
	}
	return rows
}

// withPad right-pads an already-styled string to a visible width, and lpad
// left-pads one. Both exist because the styled strings here already carry
// escape sequences, which lipgloss's own padding would re-style rather than
// compose with.
func withPad(s string, w int) string { return pad(truncateANSI(s, w), w, false) }

func lpad(s string, w int) string {
	if n := max0(w - visLen(s)); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

func lpadPlain(s string, w int) string {
	if n := max0(w - len(s)); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}
