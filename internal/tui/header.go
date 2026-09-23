package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/archesterr/whytop/internal/collect"
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
	// What is left of the panel's share of the screen once its fixed rows
	// — the meters, the rule under the core band, and the frame's own four
	// — have had theirs. Cores that do not fit are packed tighter, and only
	// then counted as hidden.
	n := h*headerShare(h)/100 - 10
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

// headerShare is the percentage of the screen the panel may spend, and it
// falls as the window gets shorter.
//
// A share that is right on a full-screen window is wrong on a small one:
// half of forty-four rows still leaves a usable process list, and half of
// twenty-four leaves five rows of processes under a panel that has taken
// everything else. The panel is the better use of height only while there
// is height to spare; below that the list is what the tool is for.
// It ramps rather than steps. A share that jumps at a threshold means the
// panel can take more rows than the window just gained: growing a terminal
// from 28 rows to 30 showed one process fewer, which is the opposite of
// what making a window bigger is for.
func headerShare(h int) int {
	n := 40 + (h - 24)
	if n < 40 {
		return 40
	}
	if n > 55 {
		return 55
	}
	return n
}

// coreGrid is a chosen layout for the per-core meters: how wide each bar is,
// whether each carries its own percentage, and which cores land on which row.
type coreGrid struct {
	barW   int
	cell   int // the whole meter's width, bar and label and percentage
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
	maxCoreBar = 64
	corePctW   = 5 // " 100%"
)

// coreLayout picks the grid for n cores in a band paneW columns wide.
//
// Two cores to a row is the shape htop uses and the shape people read the
// machine in: an eight-core box is four rows of two, not one long line of
// eight. It is also what gives the panel its height — a band one row tall
// is a status line, and the top of the screen is where the machine is
// supposed to be legible from across a desk.
//
// More cores per row only when the rows would otherwise run past what the
// terminal can spare, and the bar itself is capped: past about forty
// columns a bar stops reading as a measurement and becomes a rule — and the
// percentage then sits at the cell's right edge rather than trailing the bar,
// so the numbers line up in columns down the band.
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
		widest := (paneW + 1) / (minCell + 1) // most cores a row can hold at all
		if widest < 1 {
			return coreGrid{}, false
		}
		for perRow := coreCols; perRow <= widest; perRow++ {
			if perRow > n {
				perRow = n
			}
			if (n+perRow-1)/perRow > maxRows {
				continue
			}
			cell := (paneW+1)/perRow - 1
			barW := cell - fixed
			if barW > maxCoreBar {
				barW = maxCoreBar
			}
			return m.fillRows(coreGrid{barW: barW, cell: cell, digits: digits, pct: pct}, perRow, n, maxRows), true
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
	cell := (paneW+1)/perRow - 1
	return m.fillRows(coreGrid{barW: cell - digits - 2, cell: cell, digits: digits}, perRow, n, maxRows)
}

// coreCols is how many cores a row holds when there is room for the choice:
// htop's two.
const coreCols = 2

// cellW is the width of one core's meter as laid out.
func (g coreGrid) cellW() int {
	if g.cell > 0 {
		return g.cell
	}
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
	// The meters column carries bars as well as words, so with three
	// columns it takes a share from the other two. Splitting equally is
	// what left every meter's breakdown ending in an ellipsis while the
	// facts beside it sat in half-empty columns.
	if n == 3 {
		out[0] = each + meterColBonus
		out[1] = each - meterColBonus/2
	}
	out[n-1] = inner - sum(out[:n-1]) - colSepW*(n-1)
	return out
}

const meterColBonus = 8

func sum(ns []int) int {
	t := 0
	for _, n := range ns {
		t += n
	}
	return t
}

// headerBody renders the panel's interior, already divided and padded to the
// exact inner width. The renderer and headerHeight both go through it, so
// they cannot disagree about how tall the panel is — and a panel that draws
// a different number of rows than it claims sends every click to the wrong
// row.
// headerLine is one interior row of the panel. A rule is a row like any
// other so that the height the panel claims and the height it draws come
// from the same list — the arithmetic that keeps every click on the right
// row does not get a second code path to drift out of step with.
type headerLine struct {
	text string
	rule bool
}

func (m model) headerBody(inner int) []headerLine {
	if m.snap == nil || inner < 4 {
		return nil
	}
	widths := headerCols(inner)
	var out []headerLine
	for _, l := range m.coreLines(inner) {
		out = append(out, headerLine{text: l})
	}
	// The band of cores and the columns below it are two different things
	// being measured, so a rule goes between them rather than a blank row.
	if len(out) > 0 {
		out = append(out, headerLine{rule: true})
	}

	var groups [][]string
	switch len(widths) {
	case 3:
		groups = [][]string{m.meterLines(widths[0]), m.loadLines(widths[1]), m.identLines(widths[2])}
	case 2:
		groups = [][]string{m.meterLines(widths[0]), m.loadLines(widths[1])}
	default:
		// Stacked in one column, the rows that get cut on a short window
		// are the last ones, so the order is the order of what matters:
		// the three machine meters, then the load figures, and the disk
		// and network meters last. A full disk still reaches the verdict
		// line; a machine's load has nowhere else to appear.
		one := m.meterLines(widths[0])
		if len(one) > 3 {
			one = append(one[:3:3], append(m.loadLines(widths[0]), one[3:]...)...)
		} else {
			one = append(one, m.loadLines(widths[0])...)
		}
		groups = [][]string{one}
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
		out = append(out, headerLine{text: strings.Join(cells, sep)})
	}
	// Without a column of its own, the identity of the machine — uptime,
	// distribution, kernel — folds onto one line instead of taking four. It
	// is the least volatile thing on the panel, so it is what can afford to
	// be crowded.
	if len(widths) < 3 {
		out = append(out, headerLine{text: m.identRow(inner)})
	}
	// On a terminal too short to hold both, the panel gives way rather than
	// the list: a process list squeezed to nothing is a monitor that has
	// stopped monitoring. Rows go from the bottom, where the least urgent
	// of them are — and never leave a rule as the last row.
	if cap := m.headerBodyCap(); len(out) > cap {
		out = out[:max0(cap)]
		for len(out) > 0 && out[len(out)-1].rule {
			out = out[:len(out)-1]
		}
	}
	return out
}

// headerBodyCap is how many interior rows the panel may draw and still
// leave the list three rows, its two borders, the footer, and the terminal's
// reserved last row.
// headerBodyCap is how many interior rows the panel may draw: whichever is
// smaller of what its share of the screen allows and what leaves the list
// three rows, its two borders, the footer, and the terminal's reserved last
// row.
//
// On a short window the share binds first, and the panel shows fewer rows
// rather than showing all of them and leaving five rows of processes
// underneath. Rows go from the bottom, so a cramped panel keeps the CPU and
// memory meters and gives up the disk and network ones — which the verdict
// line still speaks up about when they are worth knowing.
func (m model) headerBodyCap() int {
	h := m.height
	if h <= 0 {
		h = 30
	}
	byList := h - 11
	if byShare := h*headerShare(h)/100 - 4; byShare < byList {
		return byShare
	}
	return byList
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
	at := ruleJunctions(headerCols(inner))
	for _, l := range body {
		if l.rule {
			lines = append(lines, boxRuleAt(w, at...))
			continue
		}
		lines = append(lines, boxLine(w, l.text))
	}
	// The rule closes every column at once, with a junction wherever a
	// divider meets it — the detail that makes a split panel look built
	// rather than merely overlaid.
	if len(at) > 0 && len(body) > 0 {
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
	fsLabel, fsSub, fsPct, fsOK := m.fullestFS()
	// The bar width is set by the three fixed-format breakdowns, not by the
	// filesystem's — a mount path is arbitrary length, and letting it into
	// this measurement means one long path on one machine shrinks every
	// bar on the panel. The path is truncated instead.
	barW := meterBarW(w, cpuSub, memSub, swapSub)
	out := []string{
		meter(w, barW, "CPU", f1(s.CPU.Busy)+"%", lvl(s.CPU.Busy, 70, 90), stAccent, s.CPU.Busy/100, cpuSub),
		meter(w, barW, "MEM", f1(s.Mem.UsedPct)+"%", lvl(s.Mem.UsedPct, 80, 92),
			lipgloss.NewStyle().Foreground(colMem), s.Mem.UsedPct/100, memSub),
		meter(w, barW, "SWP", f1(swapPct)+"%", swapStyle,
			lipgloss.NewStyle().Foreground(colLoad), swapPct/100, swapSub),
	}
	// A full disk takes a machine down as surely as a full memory, and it
	// is the one of the two nothing else on this screen would have told
	// you about. The fullest mount is the one worth the row.
	if fsOK {
		out = append(out, meter(w, barW, fsLabel, f1(fsPct)+"%", lvl(fsPct, 80, 92),
			lipgloss.NewStyle().Foreground(colIO), fsPct/100, fsSub))
	}
	return out
}

// fullestFS is the mount closest to full, which is the only one that can
// matter in a single line. A stale mount (a dead NFS server) reports
// nothing believable, so it is left out rather than shown as 0%.
func (m model) fullestFS() (label, sub string, pct float64, ok bool) {
	var worst *collect.FS
	for i := range m.snap.FS {
		f := &m.snap.FS[i]
		if f.Stale || f.Total == 0 {
			continue
		}
		if worst == nil || f.UsedPct > worst.UsedPct {
			worst = f
		}
	}
	if worst == nil {
		return "", "", 0, false
	}
	label = "DISK"
	sub = fmt.Sprintf("%s  %s free", shortMount(worst.Mount), bytesFmt(float64(worst.Free)))
	return label, sub, worst.UsedPct, true
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
	meterLabelW = 5
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
	add("I/O", io)

	// Aggregate throughput, loopback excluded: traffic a process sends to
	// itself is not traffic the machine is carrying, and counting it makes
	// a busy local database look like a saturated uplink.
	var rx, tx float64
	nics := 0
	for _, n := range s.NICs {
		if n.Name == "lo" {
			continue
		}
		rx += n.RxBps
		tx += n.TxBps
		nics++
	}
	if nics > 0 {
		add("Net", stNet.Render("↓ "+rateFmt(rx))+stMuted.Render("   ")+
			stNet.Render("↑ "+rateFmt(tx)))
	}
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
	// Uptime, cores, distribution and kernel are what you confirm once, not
	// what you watch, so they share two rows. The rows that freed up go to
	// ceilings — file handles and TCP state — which top has never shown
	// and which are what actually take a service down while CPU and memory
	// look fine.
	add("Uptime", stPlain.Render(dur(s.Uptime))+stMuted.Render(fmt.Sprintf("  %d cores", s.CPU.Cores)))
	sys := safeText(s.OS)
	if s.Kernel != "" {
		sys += stMuted.Render("  " + safeText(s.Kernel))
	}
	add("System", sys)
	if l := s.Limits; l.FilesMax > 0 {
		pct := float64(l.FilesUsed) / float64(l.FilesMax) * 100
		add("Files", lvl(pct, 70, 90).Render(countFmt(l.FilesUsed))+stMuted.Render(" of "+countFmt(l.FilesMax)+" handles"))
	}
	if s.TCP.Available {
		closeWait := 0
		for _, c := range s.Conns {
			if c.State == "CLOSE_WAIT" {
				closeWait++
			}
		}
		tcp := stPlain.Render(strconv.FormatInt(s.TCP.Established, 10)) + stMuted.Render(" estab")
		if closeWait > 0 {
			tcp += "  " + lvl(float64(closeWait), 50, 500).Render(strconv.Itoa(closeWait)) + stMuted.Render(" close-wait")
		}
		// Retransmits only when there are some: "0% retrans" is a phrase
		// to read past on every glance at a healthy box.
		if s.TCP.RetransPct >= 1 {
			tcp += "  " + lvl(s.TCP.RetransPct, 2, 5).Render(f1(s.TCP.RetransPct)+"%") + stMuted.Render(" retrans")
		}
		if s.Limits.ListenOverflowPs >= 1 {
			tcp += "  " + stCrit.Render(fmt.Sprintf("%.0f", s.Limits.ListenOverflowPs)) + stMuted.Render(" drop/s")
		}
		add("TCP", tcp)
	}
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

// shortMount names a mount by its last component. A container host's mounts
// run to sixty characters of path that differ only at the end, which is the
// end worth showing.
func shortMount(mount string) string {
	m := safeText(mount)
	if len(m) <= 16 {
		return m
	}
	if i := strings.LastIndex(m, "/"); i > 0 {
		return "…" + m[i:]
	}
	return truncate(m, 16)
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
			// The bar is capped, but the meter is not: the percentage sits
			// at the cell's right edge, so on a wide terminal the numbers
			// line up in columns down the band instead of trailing each
			// bar at a different offset. It is what htop's core meters do,
			// and the reason a wide band reads as a grid rather than as a
			// row of bars with an empty half beside it.
			meter := stFaint.Render(lpadPlain(strconv.Itoa(i), g.digits)) +
				stBox2.Render("▕") + gauge(v/100, g.barW, fill) + stBox2.Render("▏")
			if g.pct {
				room := max0(g.cellW() - visLen(meter))
				if room < corePctW {
					room = corePctW
				}
				meter += lpad(fill.Render(strconv.Itoa(int(v+0.5))+"%"), room)
			}
			b.WriteString(meter)
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

// countFmt shortens a count for a narrow cell: 1234 is "1.2k", 1645588 "1.6M".
func countFmt(n uint64) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 10_000:
		return strconv.FormatUint(n/1000, 10) + "k"
	case n >= 1000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e3), ".0") + "k"
	}
	return strconv.FormatUint(n, 10)
}
