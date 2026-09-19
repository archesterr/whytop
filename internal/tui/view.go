package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m model) View() string {
	if m.quitting {
		return ""
	}
	w := m.width
	if w <= 0 {
		w = 100
	}
	h := m.height
	if h <= 0 {
		h = 30
	}
	if m.snap == nil {
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, stMuted.Render("Collecting the first sample…"))
	}

	var b strings.Builder
	b.WriteString(m.renderHeader(w))
	b.WriteString("\n")
	b.WriteString(m.renderVitals(w))
	b.WriteString("\n")

	if m.detail != nil {
		b.WriteString(m.renderDetail(w, h))
	} else {
		b.WriteString(m.renderTabs(w))
		b.WriteString("\n")
		b.WriteString(hrule(w))
		b.WriteString("\n")
		b.WriteString(m.renderTab(w, h))
	}
	b.WriteString("\n")

	b.WriteString(m.renderFooter(w))

	// Always emit the same total line count across frames. Two consecutive
	// live-refresh frames can legitimately differ in line count (the
	// process list shrinks by one row, say), and without this, bubbletea's
	// screen diff can leave a stale line from the taller previous frame
	// sitting under the new, shorter one — the same visual glitch as the
	// footer/table collision bug, but between ticks instead of within one
	// frame.
	//
	// The target is h-1, not h: writing content into every single row of
	// the terminal, including the very last one, means the next line feed
	// has nowhere to go but to scroll the whole alt-screen buffer up by
	// one row — which shifts every future frame's content up out of place.
	// Leaving the last row untouched is the margin that avoids it.
	target := h - 1
	if target < 1 {
		target = 1
	}
	lines := strings.Split(b.String(), "\n")
	if len(lines) < target {
		lines = append(lines, make([]string, target-len(lines))...)
	} else if len(lines) > target {
		lines = lines[:target]
	}
	return strings.Join(lines, "\n")
}

func (m model) renderHeader(w int) string {
	s := m.snap
	status := stOK.Render("● live") + "  " + stAccent.Render(time.Now().Format("15:04:05"))
	if m.paused {
		status = stWarn.Render("● paused") + "  " + stAccent.Render(time.Now().Format("15:04:05"))
	}

	warnNote := ""
	if !s.Root {
		warnNote = "  limited view: run with sudo"
	}
	logo := "whytop"
	if m.opt.Version != "" {
		logo += " " + m.opt.Version
	}
	// A long hostname or a narrow terminal (an 80-column SSH default is
	// common) can't be allowed to overflow: unlike the tab bar, this row's
	// budget assumption (renderTab's avail := h-6) breaks if the header
	// wraps onto a second physical line. Every variable-length piece's
	// budget is derived from w itself, not a fixed constant — a fixed cap
	// still overflows once w drops below it.
	reserved := len(logo) + 2 + 2 + len(warnNote) + lipgloss.Width(status) + 1
	avail := max0(w - reserved)
	hostBudget := avail
	if hostBudget > 40 {
		hostBudget = 40 // don't let a long hostname alone hog a wide terminal
	}
	host := truncate(s.Host, hostBudget)
	metaBudget := max0(avail - lipgloss.Width(host) - 2)
	meta := truncate(fmt.Sprintf("up %s · %d cores · %d processes", dur(s.Uptime), s.CPU.Cores, len(s.Procs)), metaBudget)

	left := stLogo.Render(logo) + "  " + stHost.Render(host) + "  " + stMuted.Render(meta)
	if warnNote != "" {
		left += stWarn.Render(warnNote)
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(status)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + status
}

func (m model) renderVitals(w int) string {
	s := m.snap
	c, mem := s.CPU, s.Mem
	cores := c.Cores
	if cores == 0 {
		cores = 1
	}
	dstate := 0
	for _, p := range s.Procs {
		if p.State == "D" {
			dstate++
		}
	}
	ioSub := "nothing blocked on I/O"
	if dstate > 0 {
		ioSub = stCrit.Render(fmt.Sprintf("%d blocked on I/O", dstate))
	}
	psiVal, psiSub := "–", "unsupported"
	psiStyle := stMuted
	if s.PSI.Available {
		worst := max3(s.PSI.CPUSome, s.PSI.MemSome, s.PSI.IOSome)
		psiVal = f1(worst) + "%"
		psiSub = fmt.Sprintf("cpu %s mem %s io %s", f1(s.PSI.CPUSome), f1(s.PSI.MemSome), f1(s.PSI.IOSome))
		psiStyle = lvl(worst, 5, 20)
	}

	vitals := []struct {
		label string
		style lipgloss.Style
		value string
		sub   string
	}{
		{"CPU", lvl(c.Busy, 70, 90), f1(c.Busy) + "%", fmt.Sprintf("user %s sys %s", f1(c.User), f1(c.System))},
		{"MEM", lvl(mem.UsedPct, 80, 92), f1(mem.UsedPct) + "%", fmt.Sprintf("%s / %s", bytesFmt(float64(mem.Used)), bytesFmt(float64(mem.Total)))},
		{"I/O WAIT", lvl(c.Iowait, 3, 10), f1(c.Iowait) + "%", stripANSI(ioSub)},
		{"LOAD", lvl(s.Load1/float64(cores), 0.7, 1), fmt.Sprintf("%.2f", s.Load1), fmt.Sprintf("%.2f per core", s.Load1/float64(cores))},
		{"PSI", psiStyle, psiVal, psiSub},
	}
	// No hard minimum here beyond what keeps labels legible: an 80-column
	// terminal (the standard SSH default) only leaves room for colW=15, and
	// a floor above that would silently overflow the row on exactly the
	// most common terminal width there is.
	colW := (w - 4) / 5
	if colW < 10 {
		colW = 10
	}

	// Build exactly two physical lines per row (label, value+sub) with our
	// own truncation/padding — letting lipgloss auto-wrap on Width() instead
	// would silently spill a cell onto a third line and misalign every
	// column after it.
	var line1, line2 []string
	for _, v := range vitals {
		line1 = append(line1, pad(stHeader.Render(v.label), colW, false))
		budget := max0(colW - len(v.value) - 2)
		line2 = append(line2, pad(v.style.Bold(true).Render(v.value)+"  "+stMuted.Render(truncate(v.sub, budget)), colW, false))
	}
	return strings.Join(line1, " ") + "\n" + strings.Join(line2, " ")
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func max3(a, b, c float64) float64 {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}

func (m model) renderTabs(w int) string {
	var parts []string
	for _, r := range tabRegions(m.tabCounts()) {
		// Render each segment from plain text only — wrapping a string that
		// already contains another segment's ANSI codes in a second
		// .Render() call corrupts the escape sequences.
		if r.t == m.tab {
			// Active tab reads as a solid pill, k9s/lazydocker-style, instead
			// of a bare underline — a first-time user spots "where am I"
			// instantly instead of having to notice an underline.
			part := stTabOn.Render(r.base)
			if r.countText != "" {
				part += stTabOnCnt.Render(r.countText)
			}
			parts = append(parts, part)
			continue
		}
		part := stTabOff.Render(r.base)
		if r.countText != "" {
			part += stFaint.Render(r.countText)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

func (m model) tabCounts() map[tab]int {
	s := m.snap
	listen := -1
	if s.ConnsCollected {
		listen = 0
		for _, c := range s.Conns {
			if c.Listening() {
				listen++
			}
		}
	}
	units := -1
	if s.UnitsCollected {
		units = len(s.Units)
	}
	return map[tab]int{tabProcs: len(s.Procs), tabPorts: listen, tabDisks: len(s.Disks), tabNet: len(s.NICs), tabUnits: units}
}

// hrule draws a thin horizontal rule under the tab bar, separating chrome
// from data — the same visual cue k9s/lazydocker use to make the screen read
// as distinct panels instead of one undifferentiated block of text.
func hrule(w int) string {
	if w < 1 {
		w = 1
	}
	return stFaint.Render(strings.Repeat("─", w))
}

// tabRowsBudget is the number of data rows renderProcs/renderPorts actually
// draw (header line already subtracted) — mouse click hit-testing needs the
// exact same number to translate a screen row back into a list index,
// since both are scrolled to follow the selection (windowRows).
func (m model) tabRowsBudget() int {
	avail := m.height - 7
	if avail < 3 {
		avail = 3
	}
	return avail - 1
}

func (m model) renderTab(w, h int) string {
	avail := h - 7 // header + vitals + tabs + rule + footer
	if avail < 3 {
		avail = 3
	}
	switch m.tab {
	case tabProcs:
		return m.renderProcs(w, avail)
	case tabPorts:
		return m.renderPorts(w, avail)
	case tabDisks:
		return m.renderDisks(w, avail)
	case tabNet:
		return m.renderNet(w, avail)
	default:
		return m.renderUnits(w, avail)
	}
}

func (m model) renderFooter(w int) string {
	var keys [][2]string
	switch {
	case m.confirm != nil:
		style := stConfirm
		if m.confirm.danger {
			style = stDanger.Reverse(true).Padding(0, 1)
		}
		return style.Render(m.confirm.prompt)
	case m.toast != "":
		return m.toastStyle(m.toast)
	case m.editing:
		keys = [][2]string{{"enter", "apply"}, {"esc", "clear"}}
	case m.detail != nil:
		keys = [][2]string{{"↑↓", "tree"}, {"enter", "open"}, {"x", "stop"}, {"X", "kill"}}
		if !m.detail.loaded || m.detail.restartBlocked == "" {
			keys = append(keys, [2]string{"r", "restart"})
		}
		keys = append(keys, [2]string{"j", "journal"}, [2]string{"esc", "close"})
	default:
		keys = [][2]string{{"1-5/←→", "tabs"}, {"↑↓", "select"}}
		if m.tab != tabUnits {
			keys = append(keys, [2]string{"enter", "open"})
		}
		if m.tab == tabProcs || m.tab == tabPorts {
			keys = append(keys, [2]string{"/", "filter"})
		}
		if m.tab == tabProcs {
			keys = append(keys, [2]string{"s", "sort: " + m.sortKey})
		}
		if m.tab == tabPorts {
			keys = append(keys, [2]string{"a", "all sockets"})
		}
		if m.tab == tabUnits {
			keys = append(keys, [2]string{"e", "edit unit"})
		}
		keys = append(keys, [2]string{"p", "pause"}, [2]string{"q", "quit"})
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, stFooterKey.Render(k[0])+stFooterTxt.Render(" "+k[1]))
	}
	// Keep only as many hints as actually fit: a narrow terminal with a long
	// key list (the default screen's has seven) can genuinely run past 80
	// columns, and losing the least essential trailing hint is better than
	// silently overflowing or wrapping onto a second line.
	gap := " " + colSep
	budget, kept := w, parts[:0:0]
	for i, p := range parts {
		add := visLen(p)
		if i > 0 {
			add += visLen(gap)
		}
		if budget-add < 0 {
			break
		}
		budget -= add
		kept = append(kept, p)
	}
	line := strings.Join(kept, gap)
	if m.editing {
		i := int(m.tab)
		line = stAccent.Render("filter: ") + m.filter[i] + stMuted.Render("█") + "   " + line
	}
	return line
}

// centerCell renders a header label centered in its column — every data
// table's headers are centered while the data rows themselves keep
// right-aligned numbers and left-aligned text, which is what actually keeps
// a dense table scannable; centering the values too would make it much
// harder to compare numbers at a glance.
func centerCell(s string, width int, style lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > width {
		s = truncate(s, width)
		r = []rune(s)
	}
	out := style.Render(s)
	pad := width - len(r)
	if pad < 0 {
		pad = 0
	}
	left := pad / 2
	right := pad - left
	return strings.Repeat(" ", left) + out + strings.Repeat(" ", right)
}

func cell(s string, width int, right bool, style lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > width {
		if width > 1 {
			s = string(r[:width-1]) + "…"
		} else {
			s = string(r[:width])
		}
	}
	out := style.Render(s)
	pad := width - lipgloss.Width(s)
	if pad < 0 {
		pad = 0
	}
	sp := strings.Repeat(" ", pad)
	if right {
		return sp + out
	}
	return out + sp
}

var stPlain = lipgloss.NewStyle().Foreground(colText)
