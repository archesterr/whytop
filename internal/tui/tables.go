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

// gutterCell draws the "which row is selected" marker, and — in tree view —
// whether this row is part of the selected process's family. A colour-only
// highlight is easy to lose track of while arrowing through a long list, and
// a subtree that scrolls past the selected row would otherwise lose the one
// cue that said it belonged to it.
func gutterCell(selected bool, kin kinship) string {
	if selected {
		return cell("▸", gutterW, false, stAccent.Bold(true))
	}
	switch kin {
	case kinChild:
		return cell("┃", gutterW, false, stTreeKin)
	case kinParent:
		return cell("┊", gutterW, false, stTreeUp)
	}
	return cell("", gutterW, false, stPlain)
}

// windowRows picks the [start,end) slice of a `total`-row list to draw so
// the row at selIdx stays inside a `budget`-row viewport, for the small
// lists inside the process panel, which have no scroll position of their
// own to remember. The list starts at its first row and only moves once the
// cursor would leave the viewport.
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
	start = clampTop(0, selIdx, total, dataRows)
	end = start + dataRows
	if end > total {
		end = total
	}
	return start, end
}

// clampTop moves a scroll position the least it can to keep the selected row
// on screen — down when the cursor walks off the bottom, up when it walks off
// the top, and not at all otherwise.
//
// The list used to centre the selection instead, which meant the first thing
// you saw on opening whytop was the middle of the process list with a third
// of it scrolled off above, and every press of an arrow key shifted every row
// on screen. A list you are reading should hold still; what moves is the
// cursor, until it reaches an edge.
func clampTop(top, selIdx, total, dataRows int) int {
	if dataRows < 1 {
		dataRows = 1
	}
	if max := total - dataRows; top > max {
		top = max
	}
	if top < 0 {
		top = 0
	}
	if selIdx < 0 {
		return top
	}
	if selIdx < top {
		return selIdx
	}
	if selIdx >= top+dataRows {
		return max0(selIdx - dataRows + 1)
	}
	return top
}

// window is the process list's own viewport, which — unlike the panels'
// lists — remembers where it is scrolled to. Without that memory the only
// scroll positions expressible are "the top" and "the selection glued to
// the bottom edge", and walking back up a long list drags every row with
// the cursor instead of letting it climb the screen.
func (m model) window(total, selIdx, budget int) (start, end int) {
	if budget < 1 {
		budget = 1
	}
	if total <= budget {
		return 0, total
	}
	dataRows := budget - 1
	if dataRows < 1 {
		dataRows = 1
	}
	start = clampTop(m.top, selIdx, total, dataRows)
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
		return stMuted.Render(m.emptyMessage())
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
	// The tree's guides and the kinship of every row to the selected one
	// are computed over the whole list, not the visible window: a subtree
	// that starts above the fold is still the selected process's subtree.
	var tr []treeRow
	if m.tree {
		tr = treeRows(list, selIdx)
	}

	start, end := m.window(len(list), selIdx, h-1)
	for i := start; i < end; i++ {
		p := list[i]
		sel := i == selIdx
		// Built from the same column list the header is, so a column that
		// is dropped on a narrow terminal is dropped from both at once.
		t := treeRow{}
		if tr != nil {
			t = tr[i]
		}
		rowCells := make([]string, 0, len(cols))
		for _, c := range cols {
			rowCells = append(rowCells, m.procCell(c, p, sel, t))
		}
		lines = append(lines, joinColsSel(sel, rowCells...))
	}
	if end < len(list) || start > 0 {
		lines = append(lines, stFaint.Render(scrollNotice(len(list), start, end)))
	}
	return strings.Join(lines, "\n")
}

// commandOf is what the COMMAND column shows: the whole command line, or —
// htop's p — just the program that is running, without the path it lives
// at. On a box full of /usr/lib/very/long/paths the second is what you are
// actually reading.
func (m model) commandOf(p collect.Proc) string {
	cmd := cmdOf(p)
	if m.fullPath {
		return cmd
	}
	prog, args, _ := strings.Cut(cmd, " ")
	if i := strings.LastIndex(prog, "/"); i >= 0 {
		prog = prog[i+1:]
	}
	if args == "" {
		return prog
	}
	return prog + " " + args
}

// procCell renders one column of one process row.
func (m model) procCell(c procCol, p collect.Proc, sel bool, t treeRow) string {
	switch c.key {
	case "":
		return gutterCell(sel, t.kin)
	case "pid":
		// In tree view the PID is where kinship is most worth saying: "which
		// of these thirty PIDs are the ones under the row I selected" is the
		// question the tree exists to answer.
		st := stMuted
		switch t.kin {
		case kinChild:
			st = stTreeKin
		case kinParent:
			st = stTreeUp
		}
		return cell(strconv.Itoa(int(p.PID)), c.w, true, withBG(st, sel))
	case "user":
		return userCell(p.User, c.w, sel)
	case "state":
		return cell(p.State, c.w, false, withBG(stateStyle(p.State), sel))
	case "cpu":
		// A throttled process's CPU% is capped by its quota, not by how
		// much work it has: 19% can mean "starving". It is drawn as
		// critical, whatever the number, with a mark that says why.
		if p.Throttled >= 25 {
			return cell("⏸"+f1(p.CPU), c.w, true, withBG(stCrit, sel))
		}
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
		return cmdCellAt(m.commandOf(p), t, c.w, sel)
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
