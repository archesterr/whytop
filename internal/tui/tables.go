package tui

import (
	"fmt"
	"sort"
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
		if m.filter[tabProcs] != "" {
			msg = fmt.Sprintf("No process matches %q.", m.filter[tabProcs])
		}
		return stMuted.Render(msg)
	}

	// The Unit column is the first to go on a narrow terminal — matching the
	// original web UI's own responsive behavior (it hid the same column
	// below 980px) rather than squeezing Command down to an unreadable
	// sliver just to keep every column present.
	unitW := 16
	noUnitFixed, noUnitGaps := gutterW+6+11+3+6+9+9+9, 8
	withUnitFixed, withUnitGaps := noUnitFixed+unitW, 9
	cmdWWithUnit := w - withUnitFixed - withUnitGaps*sepW
	showUnit := cmdWWithUnit >= 18
	cmdW := cmdWWithUnit
	if !showUnit {
		cmdW = w - noUnitFixed - noUnitGaps*sepW
	}
	if cmdW < 12 {
		cmdW = 12
	}

	headerCells := []string{centerCell("", gutterW, stHeader), centerCell("PID", 6, stHeader), centerCell("USER", 11, stHeader), centerCell("ST", 3, stHeader),
		centerCell("CPU%", 6, stHeader), centerCell("MEM", 9, stHeader), centerCell("READ", 9, stHeader), centerCell("WRITE", 9, stHeader)}
	if showUnit {
		headerCells = append(headerCells, centerCell("UNIT", unitW, stHeader))
	}
	headerCells = append(headerCells, centerCell("COMMAND", cmdW, stHeader))
	header := joinCols(headerCells...)

	var lines []string
	lines = append(lines, header)
	selKey := m.sel[tabProcs]
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
		rowCells := []string{
			gutterCell(sel),
			cell(strconv.Itoa(int(p.PID)), 6, true, withBG(stPlain, sel)),
			cell(p.User, 11, false, withBG(stMuted, sel)),
			cell(p.State, 3, false, withBG(stateStyle(p.State), sel)),
			cell(f1(p.CPU), 6, true, withBG(lvl(p.CPU, 50, 90), sel)),
			cell(bytesFmt(float64(p.RSS)), 9, true, withBG(stPlain, sel)),
			ioCell(p.IOHidden, p.ReadBps, 9, sel),
			ioCell(p.IOHidden, p.WriteBps, 9, sel),
		}
		if showUnit {
			rowCells = append(rowCells, cell(unitName(p.Unit), unitW, false, withBG(stAccent, sel)))
		}
		rowCells = append(rowCells, cell(cmdOf(p), cmdW, false, withBG(stMuted, sel)))
		lines = append(lines, joinColsSel(sel, rowCells...))
	}
	if end < len(list) || start > 0 {
		lines = append(lines, stFaint.Render(scrollNotice(len(list), start, end)))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderPorts(w, h int) string {
	if m.snap == nil || !m.snap.ConnsCollected {
		return stMuted.Render("Reading sockets…")
	}
	list := m.portRows()
	if len(list) == 0 {
		msg := "No listening sockets."
		if m.filter[tabPorts] != "" {
			msg = fmt.Sprintf("No socket matches %q.", m.filter[tabPorts])
		}
		return stMuted.Render(msg)
	}
	remoteW := w - (gutterW + 6 + 6 + 15 + 11 + 16 + 6) - 7*sepW
	if remoteW < 10 {
		remoteW = 10
	}
	header := joinCols(centerCell("", gutterW, stHeader), centerCell("PORT", 6, stHeader), centerCell("PROTO", 6, stHeader), centerCell("ADDRESS", 15, stHeader),
		centerCell("STATE", 11, stHeader), centerCell("PROCESS", 16, stHeader), centerCell("PID", 6, stHeader), centerCell("REMOTE", remoteW, stHeader))

	var lines []string
	lines = append(lines, header)
	selKey := m.sel[tabPorts]
	selIdx := -1
	for i, c := range list {
		if connKey(c) == selKey {
			selIdx = i
			break
		}
	}
	start, end := windowRows(len(list), selIdx, h-1)
	for i := start; i < end; i++ {
		c := list[i]
		sel := i == selIdx
		proc, ok := m.procByPID(c.PID)
		name := withBG(stFaint, sel).Render("hidden")
		if ok {
			name = withBG(stPlain.Bold(true), sel).Render(truncate(proc.Name, 16))
		}
		addrStyle := stPlain
		addr := c.LocalIP
		switch {
		case addr == "" || addr == "0.0.0.0" || addr == "::":
			addrStyle = stWarn
			if addr == "" {
				addr = "*"
			}
		case strings.HasPrefix(addr, "127.") || addr == "::1":
			addrStyle = stOK
		}
		stStyle := stPlain
		if c.State == "CLOSE_WAIT" {
			stStyle = stCrit
		} else if c.State == "LISTEN" {
			stStyle = stOK
		}
		pidStr := "–"
		if c.PID > 0 {
			pidStr = strconv.Itoa(int(c.PID))
		}
		row := joinColsSel(sel, gutterCell(sel),
			cell(strconv.Itoa(int(c.LPort)), 6, true, withBG(stPlain.Bold(true), sel)), cell(c.Proto, 6, false, withBG(stMuted, sel)),
			cell(addr, 15, false, withBG(addrStyle, sel)), cell(c.State, 11, false, withBG(stStyle, sel)), pad(name, 16, sel),
			cell(pidStr, 6, true, withBG(stPlain, sel)), cell(c.Remote, remoteW, false, withBG(stMuted, sel)))
		lines = append(lines, row)
	}
	if end < len(list) || start > 0 {
		lines = append(lines, stFaint.Render(scrollNotice(len(list), start, end)))
	}
	return strings.Join(lines, "\n")
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

func (m model) renderDisks(w, h int) string {
	// A fixed slice goes to "who is actually driving this number" — the one
	// question raw device counters can never answer on their own — and the
	// other two sections split what's left, same as before.
	procH := 8
	if h < 24 {
		procH = 6
	}
	rest := max0(h - procH)
	half := max0(rest/2 - 3)
	if half < 3 {
		half = 3
	}

	var b strings.Builder
	b.WriteString(stHeader.Render("BLOCK DEVICES") + "\n")
	if len(m.snap.Disks) == 0 {
		b.WriteString(stMuted.Render("No block devices.") + "\n")
	} else {
		b.WriteString(joinCols(centerCell("DEVICE", 10, stHeader), centerCell("R/S", 7, stHeader), centerCell("W/S", 7, stHeader),
			centerCell("READ", 9, stHeader), centerCell("WRITE", 9, stHeader), centerCell("AWAIT", 8, stHeader),
			centerCell("QUEUE", 6, stHeader), centerCell("UTIL%", 6, stHeader)) + "\n")
		lines := capRows(m.snap.Disks, half, func(d collect.Disk) string {
			return joinCols(cell(d.Name, 10, false, stPlain.Bold(true)), cell(f1(d.RIOPS), 7, true, stPlain), cell(f1(d.WIOPS), 7, true, stPlain),
				cell(rateFmt(d.RBps), 9, true, stPlain), cell(rateFmt(d.WBps), 9, true, stPlain),
				cell(f1(d.AwaitMs), 8, true, lvl(d.AwaitMs, 20, 100)), cell(f1(d.Queue), 6, true, lvl(d.Queue, 1, 4)),
				cell(f1(d.Util), 6, true, lvl(d.Util, 70, 90)))
		})
		b.WriteString(strings.Join(lines, "\n") + "\n")
	}

	b.WriteString("\n" + stHeader.Render("TOP PROCESSES BY DISK I/O") + "\n")
	b.WriteString(m.renderDiskProcs(w, procH) + "\n")

	b.WriteString("\n" + stHeader.Render("FILESYSTEMS") + "\n")
	if len(m.snap.FS) == 0 {
		b.WriteString(stMuted.Render("No filesystems."))
	} else {
		mountW := w - (8 + 9 + 9 + 7 + 7) - 5*sepW
		if mountW < 10 {
			mountW = 10
		}
		b.WriteString(joinCols(centerCell("MOUNT", mountW, stHeader), centerCell("TYPE", 8, stHeader), centerCell("SIZE", 9, stHeader),
			centerCell("FREE", 9, stHeader), centerCell("USED%", 7, stHeader), centerCell("INODE%", 7, stHeader)) + "\n")
		lines := capRows(m.snap.FS, half, func(f collect.FS) string {
			if f.Stale {
				return cell(f.Mount, mountW, false, stPlain.Bold(true)) + colSep + stCrit.Render(truncate("not responding — statfs is hanging (dead network mount?)", w-mountW-sepW))
			}
			return joinCols(cell(f.Mount, mountW, false, stPlain.Bold(true)), cell(f.Type, 8, false, stMuted),
				cell(bytesFmt(float64(f.Total)), 9, true, stPlain), cell(bytesFmt(float64(f.Free)), 9, true, stPlain),
				cell(f1(f.UsedPct), 7, true, lvl(f.UsedPct, 80, 90)), cell(f1(f.InodePct), 7, true, lvl(f.InodePct, 80, 90)))
		})
		b.WriteString(strings.Join(lines, "\n"))
	}
	return b.String()
}

// renderDiskProcs answers the question device counters alone never can: not
// just "this disk is busy" but which process is doing it. A process that's
// actually blocked on I/O right now (state D) always sorts to the top, even
// if its measured bytes/sec this particular tick happens to be low — that's
// the one worth looking at. Below that, everything with nonzero read+write
// this tick, ranked by total throughput.
func (m model) renderDiskProcs(w, h int) string {
	procs := make([]collect.Proc, 0)
	for _, p := range m.snap.Procs {
		if p.State == "D" || p.ReadBps+p.WriteBps > 0 {
			procs = append(procs, p)
		}
	}
	if len(procs) == 0 {
		return stMuted.Render("No process disk activity right now.")
	}
	sort.Slice(procs, func(i, j int) bool {
		di, dj := procs[i].State == "D", procs[j].State == "D"
		if di != dj {
			return di
		}
		return procs[i].ReadBps+procs[i].WriteBps > procs[j].ReadBps+procs[j].WriteBps
	})
	cmdW := w - (6 + 11 + 3 + 9 + 9) - 4*sepW
	if cmdW < 12 {
		cmdW = 12
	}
	header := joinCols(centerCell("PID", 6, stHeader), centerCell("USER", 11, stHeader), centerCell("ST", 3, stHeader),
		centerCell("READ", 9, stHeader), centerCell("WRITE", 9, stHeader), centerCell("COMMAND", cmdW, stHeader))
	lines := capRows(procs, h-1, func(p collect.Proc) string {
		return joinCols(cell(strconv.Itoa(int(p.PID)), 6, true, stPlain), cell(p.User, 11, false, stMuted),
			cell(p.State, 3, false, stateStyle(p.State)), ioCell(p.IOHidden, p.ReadBps, 9, false), ioCell(p.IOHidden, p.WriteBps, 9, false),
			cell(cmdOf(p), cmdW, false, stMuted))
	})
	return header + "\n" + strings.Join(lines, "\n")
}

func (m model) renderNet(w, h int) string {
	var b strings.Builder
	t := m.snap.TCP
	b.WriteString(stHeader.Render("TCP") + "\n")
	if !t.Available {
		b.WriteString(stMuted.Render("TCP counters unavailable.") + "\n")
	} else {
		// Short labels and single-char separators, not three-space gaps: the
		// old wording ("established", "new out", "rx errs") plus literal
		// triple-spacing overflowed an 80-column terminal on its own, before
		// any column-separator changes.
		b.WriteString(joinCols(
			"estab "+stPlain.Bold(true).Render(strconv.FormatInt(t.Established, 10)),
			"out "+f1(t.ActivePs)+"/s",
			"in "+f1(t.PassivePs)+"/s",
			"retrans "+lvl(t.RetransPct, 1, 5).Render(f1(t.RetransPs)+"/s"),
			"resets "+f1(t.ResetPs)+"/s",
			"rx-err "+lvl(t.InErrPs, 0.1, 10).Render(f1(t.InErrPs)),
		) + "\n")
	}
	b.WriteString("\n" + stHeader.Render("INTERFACES") + "\n")
	if len(m.snap.NICs) == 0 {
		b.WriteString(stMuted.Render("No interfaces."))
	} else {
		nameW := w - (9 + 9 + 8 + 8 + 7 + 7) - 6*sepW
		if nameW < 8 {
			nameW = 8
		}
		b.WriteString(joinCols(centerCell("NAME", nameW, stHeader), centerCell("RX", 9, stHeader), centerCell("TX", 9, stHeader),
			centerCell("PPS IN", 8, stHeader), centerCell("PPS OUT", 8, stHeader), centerCell("ERR/S", 7, stHeader), centerCell("DROP/S", 7, stHeader)) + "\n")
		// A container host can have dozens to hundreds of veth interfaces —
		// cap the list against the tab's height budget like every other
		// table does, instead of printing an unbounded interface list.
		maxRows := max0(h - 6)
		if maxRows < 3 {
			maxRows = 3
		}
		lines := capRows(m.snap.NICs, maxRows, func(n collect.NIC) string {
			errStyle, dropStyle := stPlain, stPlain
			if n.ErrPs > 0 {
				errStyle = stCrit
			}
			if n.DropPs > 0 {
				dropStyle = stWarn
			}
			return joinCols(cell(n.Name, nameW, false, stPlain.Bold(true)), cell(rateFmt(n.RxBps), 9, true, stPlain), cell(rateFmt(n.TxBps), 9, true, stPlain),
				cell(f1(n.RxPps), 8, true, stPlain), cell(f1(n.TxPps), 8, true, stPlain), cell(f1(n.ErrPs), 7, true, errStyle), cell(f1(n.DropPs), 7, true, dropStyle))
		})
		b.WriteString(strings.Join(lines, "\n"))
	}
	return b.String()
}
