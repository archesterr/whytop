package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/archesterr/whytop/internal/collect"
)

func ioCell(hidden bool, v float64, width int, selected bool) string {
	if hidden {
		return cell("hidden", width, true, withBG(stFaint, selected))
	}
	return cell(rateFmt(v), width, true, withBG(stPlain, selected))
}

// withBG carries a selected row's background onto an individual cell's own
// foreground style. A selected row is highlighted by giving every cell this
// background rather than by re-Render()ing the already-styled row string —
// styling text that contains embedded ANSI codes a second time corrupts the
// escape sequences instead of composing with them.
func withBG(st lipgloss.Style, selected bool) lipgloss.Style {
	if selected {
		return st.Background(colSelBg).Bold(true)
	}
	return st
}

// gutterW is the width of the "which row is selected" marker column that
// prefixes every table — a color-only highlight is easy to lose track of
// while arrowing through a long list, so every row also carries an explicit
// glyph that only the selected row has.
const gutterW = 2

func gutterCell(selected bool) string {
	if selected {
		return cell("▸", gutterW, false, stAccent.Bold(true))
	}
	return cell("", gutterW, false, stPlain)
}

// windowRows picks the [start,end) slice of a `total`-row list to actually
// draw so the row at selIdx stays inside a `budget`-row viewport. Without
// this, every table always drew rows [0,budget) regardless of selection —
// arrowing down past the bottom of the visible rows moved the selection
// state correctly but there was nothing left on screen still highlighted,
// so the cursor appeared to just vanish.
func windowRows(total, selIdx, budget int) (start, end int) {
	if budget < 1 {
		budget = 1
	}
	if total <= budget {
		return 0, total
	}
	dataRows := budget - 1 // reserve the last line for a scroll-position notice
	if dataRows < 1 {
		dataRows = 1
	}
	start = selIdx - dataRows/2
	if start < 0 {
		start = 0
	}
	if max := total - dataRows; start > max {
		start = max
	}
	end = start + dataRows
	if end > total {
		end = total
	}
	return start, end
}

func scrollNotice(total, start, end int) string {
	above, below := start, total-end
	switch {
	case above > 0 && below > 0:
		return fmt.Sprintf("↑ %d above · ↓ %d below — ↑↓ to scroll", above, below)
	case above > 0:
		return fmt.Sprintf("↑ %d above (bottom) — ↑↓ to scroll", above)
	default:
		return fmt.Sprintf("↓ %d more below — ↑↓ to scroll", below)
	}
}

func (m model) renderProcs(w, h int) string {
	list := m.procRows()
	if len(list) == 0 {
		msg := "No processes."
		if f := m.filter; f != "" {
			// A state filter that matches nothing is the normal outcome of
			// jumping to a problem that has since cleared, so it says that
			// rather than looking like a search that failed.
			if st, ok := strings.CutPrefix(f, "state:"); ok {
				msg = fmt.Sprintf("Nothing is in state %s right now — it may have cleared. Press esc to show everything.", strings.ToUpper(st))
			} else if port, ok := strings.CutPrefix(f, "port:"); ok {
				msg = fmt.Sprintf("Nothing is listening on port %s. Press esc to show everything.", port)
			} else {
				msg = fmt.Sprintf("No process matches %q. Press esc to clear the filter.", f)
			}
		}
		return stMuted.Render(msg)
	}

	cols := m.cols(w)

	// The sorted column is named in its own header rather than only in the
	// footer: an arrow on the column you're looking at is how every table in
	// every tool says "this is the order", and it's what makes the header
	// look clickable in the first place.
	headerCells := make([]string, 0, len(cols))
	for _, c := range cols {
		title, style := c.title, stHdrCell
		if c.key != "" && c.key == m.sortKey {
			arrow := "▾"
			if m.sortDir > 0 {
				arrow = "▴"
			}
			title, style = c.title+arrow, stHdrCellOn
		}
		headerCells = append(headerCells, centerCell(title, c.w, style))
	}
	header := tableHeader(w, headerCells...)

	var lines []string
	lines = append(lines, header)
	selKey := m.sel
	selIdx := -1
	for i, p := range list {
		if strconv.Itoa(int(p.PID)) == selKey {
			selIdx = i
			break
		}
	}
	start, end := windowRows(len(list), selIdx, h-1)
	for i := start; i < end; i++ {
		p := list[i]
		sel := i == selIdx
		// Built from the same column list the header is, so a column that
		// is dropped on a narrow terminal is dropped from both at once.
		rowCells := make([]string, 0, len(cols))
		for _, c := range cols {
			rowCells = append(rowCells, m.procCell(c, p, sel))
		}
		lines = append(lines, joinColsSel(sel, rowCells...))
	}
	if end < len(list) || start > 0 {
		lines = append(lines, stFaint.Render(scrollNotice(len(list), start, end)))
	}
	return strings.Join(lines, "\n")
}

// procCell renders one column of one process row.
func (m model) procCell(c procCol, p collect.Proc, sel bool) string {
	switch c.key {
	case "":
		return gutterCell(sel)
	case "pid":
		return cell(strconv.Itoa(int(p.PID)), c.w, true, withBG(stMuted, sel))
	case "user":
		return userCell(p.User, c.w, sel)
	case "state":
		return cell(p.State, c.w, false, withBG(stateStyle(p.State), sel))
	case "cpu":
		return cell(f1(p.CPU), c.w, true, withBG(lvl(p.CPU, 50, 90), sel))
	case "mem":
		return memCell(p.RSS, m.snap.Mem.Total, c.w, sel)
	case "read":
		return ioCell(p.IOHidden, p.ReadBps, c.w, sel)
	case "write":
		return ioCell(p.IOHidden, p.WriteBps, c.w, sel)
	case "rx":
		return netCell(p.NetKnown, p.NetRxBps, c.w, sel)
	case "tx":
		return netCell(p.NetKnown, p.NetTxBps, c.w, sel)
	case "port":
		return portCell(p, c.w, sel)
	case "unit":
		return cell(unitName(p.Unit), c.w, false, withBG(stAccent, sel))
	default:
		return cmdCell(cmdOf(p), c.w, sel)
	}
}

// netCell distinguishes "no traffic" from "we could not measure it". Those
// are different answers, and printing 0 B/s for the second one is a lie the
// operator would act on — see collect/netrate.go for when it happens.
func netCell(known bool, v float64, w int, sel bool) string {
	if !known {
		return cell("?", w, true, withBG(stFaint, sel))
	}
	st := stPlain
	if v > 0 {
		st = stNet
	}
	return cell(rateFmt(v), w, true, withBG(st, sel))
}

// portCell shows the lowest port a process listens on, and how many more it
// has. A process listening on nothing gets a blank rather than a dash: on a
// full screen of processes most listen on nothing, and a column of dashes is
// just noise drawn over the answer.
func portCell(p collect.Proc, w int, sel bool) string {
	if len(p.Ports) == 0 {
		if p.Estab > 0 {
			// Not listening, but talking to something — worth telling apart
			// from a process with no sockets at all.
			return cell("→"+strconv.Itoa(p.Estab), w, true, withBG(stFaint, sel))
		}
		return cell("", w, true, withBG(stPlain, sel))
	}
	return cell(p.PortList(), w, true, withBG(stPort, sel))
}

// pad pads an already-styled string (rendered separately from cell()) to a
// visible width without re-truncating it. The fill spaces carry the same
// selection background as the content so a highlighted row has no gaps.
func pad(rendered string, width int, selected bool) string {
	plainLen := width
	n := max0(plainLen - visLen(rendered))
	fill := strings.Repeat(" ", n)
	if selected && n > 0 {
		fill = withBG(stPlain, true).Render(fill)
	}
	return rendered + fill
}

// truncateANSI cuts an already-styled string to w visible columns with the
// escape sequences left intact. truncate() counts runes, so on styled text it
// slices through the middle of an escape sequence and corrupts the colour of
// everything after it.
func truncateANSI(s string, w int) string {
	if visLen(s) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	var b strings.Builder
	vis, inEsc := 0, false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
			b.WriteRune(r)
		case inEsc:
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
		case vis < w-1:
			b.WriteRune(r)
			vis++
		}
	}
	b.WriteString("…\x1b[0m")
	return b.String()
}

func visLen(s string) int {
	// rough visible length: strip ANSI escape sequences.
	n, inEsc := 0, false
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
		n++
	}
	return n
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func truncate(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

// capRows returns at most max rows plus, if truncated, an "N more" notice —
// the same overflow handling renderProcs/renderPorts use. Without this,
// tabs with an unbounded row source (many block devices, many mounts, many
// interfaces — routine on a container host) can print far more lines than
// the terminal has, pushing the footer and its key hints off-screen.
func capRows[T any](rows []T, maxRows int, render func(T) string) []string {
	if maxRows < 1 {
		maxRows = 1
	}
	lines := make([]string, 0, maxRows+1)
	for i, r := range rows {
		if i >= maxRows {
			lines = append(lines, stFaint.Render(fmt.Sprintf("… %d more", len(rows)-i)))
			break
		}
		lines = append(lines, render(r))
	}
	return lines
}
