package tui

import (
	"fmt"
	"strings"

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
		b.WriteString(m.renderTab(w, h))
	}

	b.WriteString(m.renderFooter(w))
	return b.String()
}

func (m model) renderHeader(w int) string {
	s := m.snap
	left := stLogo.Render("whytop") + "  " + stHost.Render(s.Host) + "  " +
		stMuted.Render(fmt.Sprintf("up %s · %d cores · %d processes", dur(s.Uptime), s.CPU.Cores, len(s.Procs)))
	if !s.Root {
		left += "  " + stWarn.Render("limited view: run with sudo")
	}
	status := stOK.Render("● live")
	if m.paused {
		status = stWarn.Render("● paused")
	}
	right := status
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
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
	colW := (w - 4) / 5
	if colW < 16 {
		colW = 16
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
	names := []tab{tabProcs, tabPorts, tabDisks, tabNet}
	counts := m.tabCounts()
	for _, t := range names {
		base := fmt.Sprintf(" %d %s ", int(t)+1, t.String())
		style := stTabOff
		if t == m.tab {
			style = stTabOn
		}
		// Render each segment from plain text only — wrapping a string that
		// already contains another segment's ANSI codes in a second
		// .Render() call corrupts the escape sequences.
		part := style.Render(base)
		if counts[t] >= 0 {
			part += stFaint.Render(fmt.Sprintf("%d ", counts[t]))
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
	return map[tab]int{tabProcs: len(s.Procs), tabPorts: listen, tabDisks: len(s.Disks), tabNet: len(s.NICs)}
}

func (m model) renderTab(w, h int) string {
	avail := h - 6 // header + vitals + tabs + footer
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
	default:
		return m.renderNet(w, avail)
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
		keys = [][2]string{{"↑↓", "tree"}, {"enter", "open"}, {"x", "stop"}, {"X", "kill"}, {"r", "restart"}, {"l", "journal"}, {"esc", "close"}}
	default:
		keys = [][2]string{{"1-4", "tabs"}, {"↑↓", "select"}, {"enter", "open"}}
		if m.tab == tabProcs || m.tab == tabPorts {
			keys = append(keys, [2]string{"/", "filter"})
		}
		if m.tab == tabProcs {
			keys = append(keys, [2]string{"s", "sort: " + m.sortKey})
		}
		if m.tab == tabPorts {
			keys = append(keys, [2]string{"a", "all sockets"})
		}
		keys = append(keys, [2]string{"p", "pause"}, [2]string{"q", "quit"})
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, stFooterKey.Render(k[0])+stFooterTxt.Render(" "+k[1]))
	}
	line := strings.Join(parts, "  ")
	if m.editing {
		i := int(m.tab)
		line = stAccent.Render("filter: ") + m.filter[i] + stMuted.Render("█") + "   " + line
	}
	return line
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
