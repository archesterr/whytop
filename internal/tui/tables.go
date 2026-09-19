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
		return st.Background(colLine)
	}
	return st
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

	unitW := 16
	cmdW := w - (6 + 1 + 11 + 1 + 3 + 1 + 6 + 1 + 9 + 1 + 9 + 1 + 9 + 1 + unitW + 1)
	if cmdW < 12 {
		cmdW = 12
	}
	header := cell("PID", 6, true, stHeader) + " " + cell("USER", 11, false, stHeader) + " " + cell("ST", 3, false, stHeader) + " " +
		cell("CPU%", 6, true, stHeader) + " " + cell("MEM", 9, true, stHeader) + " " + cell("READ", 9, true, stHeader) + " " +
		cell("WRITE", 9, true, stHeader) + " " + cell("UNIT", unitW, false, stHeader) + " " + cell("COMMAND", cmdW, false, stHeader)

	var lines []string
	lines = append(lines, header)
	selKey := m.sel[tabProcs]
	for i, p := range list {
		if i >= h-1 {
			lines = append(lines, stFaint.Render(fmt.Sprintf("… %d more (narrow the filter to see them)", len(list)-i)))
			break
		}
		sel := strconv.Itoa(int(p.PID)) == selKey
		row := cell(strconv.Itoa(int(p.PID)), 6, true, withBG(stPlain, sel)) + " " +
			cell(p.User, 11, false, withBG(stMuted, sel)) + " " +
			cell(p.State, 3, false, withBG(stateStyle(p.State), sel)) + " " +
			cell(f1(p.CPU), 6, true, withBG(lvl(p.CPU, 50, 90), sel)) + " " +
			cell(bytesFmt(float64(p.RSS)), 9, true, withBG(stPlain, sel)) + " " +
			ioCell(p.IOHidden, p.ReadBps, 9, sel) + " " +
			ioCell(p.IOHidden, p.WriteBps, 9, sel) + " " +
			cell(unitName(p.Unit), unitW, false, withBG(stAccent, sel)) + " " +
			cell(cmdOf(p), cmdW, false, withBG(stMuted, sel))
		lines = append(lines, row)
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
	remoteW := w - (6 + 1 + 6 + 1 + 15 + 1 + 11 + 1 + 16 + 1 + 6 + 1)
	if remoteW < 10 {
		remoteW = 10
	}
	header := cell("PORT", 6, true, stHeader) + " " + cell("PROTO", 6, false, stHeader) + " " + cell("ADDRESS", 15, false, stHeader) + " " +
		cell("STATE", 11, false, stHeader) + " " + cell("PROCESS", 16, false, stHeader) + " " + cell("PID", 6, true, stHeader) + " " + cell("REMOTE", remoteW, false, stHeader)

	var lines []string
	lines = append(lines, header)
	selKey := m.sel[tabPorts]
	for i, c := range list {
		if i >= h-1 {
			lines = append(lines, stFaint.Render(fmt.Sprintf("… %d more (narrow the filter to see them)", len(list)-i)))
			break
		}
		sel := connKey(c) == selKey
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
		row := cell(strconv.Itoa(int(c.LPort)), 6, true, withBG(stPlain.Bold(true), sel)) + " " + cell(c.Proto, 6, false, withBG(stMuted, sel)) + " " +
			cell(addr, 15, false, withBG(addrStyle, sel)) + " " + cell(c.State, 11, false, withBG(stStyle, sel)) + " " + pad(name, 16, sel) + " " +
			cell(pidStr, 6, true, withBG(stPlain, sel)) + " " + cell(c.Remote, remoteW, false, withBG(stMuted, sel))
		lines = append(lines, row)
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
	// Split the tab's height budget between the two sections (each with a
	// section header + column header of its own), so a host with many disks
	// doesn't crowd the filesystems section off-screen or vice versa.
	half := max0(h/2 - 3)
	if half < 3 {
		half = 3
	}

	var b strings.Builder
	b.WriteString(stHeader.Render("BLOCK DEVICES") + "\n")
	if len(m.snap.Disks) == 0 {
		b.WriteString(stMuted.Render("No block devices.") + "\n")
	} else {
		b.WriteString(cell("DEVICE", 10, false, stHeader) + " " + cell("R/S", 7, true, stHeader) + " " + cell("W/S", 7, true, stHeader) + " " +
			cell("READ", 9, true, stHeader) + " " + cell("WRITE", 9, true, stHeader) + " " + cell("AWAIT", 8, true, stHeader) + " " + cell("UTIL%", 6, true, stHeader) + "\n")
		lines := capRows(m.snap.Disks, half, func(d collect.Disk) string {
			return cell(d.Name, 10, false, stPlain.Bold(true)) + " " + cell(f1(d.RIOPS), 7, true, stPlain) + " " + cell(f1(d.WIOPS), 7, true, stPlain) + " " +
				cell(rateFmt(d.RBps), 9, true, stPlain) + " " + cell(rateFmt(d.WBps), 9, true, stPlain) + " " +
				cell(f1(d.AwaitMs), 8, true, lvl(d.AwaitMs, 20, 100)) + " " + cell(f1(d.Util), 6, true, lvl(d.Util, 70, 90))
		})
		b.WriteString(strings.Join(lines, "\n") + "\n")
	}
	b.WriteString("\n" + stHeader.Render("FILESYSTEMS") + "\n")
	if len(m.snap.FS) == 0 {
		b.WriteString(stMuted.Render("No filesystems."))
	} else {
		b.WriteString(cell("MOUNT", 24, false, stHeader) + " " + cell("TYPE", 8, false, stHeader) + " " + cell("SIZE", 9, true, stHeader) + " " +
			cell("FREE", 9, true, stHeader) + " " + cell("USED%", 7, true, stHeader) + " " + cell("INODE%", 7, true, stHeader) + "\n")
		lines := capRows(m.snap.FS, half, func(f collect.FS) string {
			if f.Stale {
				return cell(f.Mount, 24, false, stPlain.Bold(true)) + " " + stCrit.Render("not responding — statfs is hanging (dead network mount?)")
			}
			return cell(f.Mount, 24, false, stPlain.Bold(true)) + " " + cell(f.Type, 8, false, stMuted) + " " +
				cell(bytesFmt(float64(f.Total)), 9, true, stPlain) + " " + cell(bytesFmt(float64(f.Free)), 9, true, stPlain) + " " +
				cell(f1(f.UsedPct), 7, true, lvl(f.UsedPct, 80, 90)) + " " + cell(f1(f.InodePct), 7, true, lvl(f.InodePct, 80, 90))
		})
		b.WriteString(strings.Join(lines, "\n"))
	}
	return b.String()
}

func (m model) renderNet(w, h int) string {
	var b strings.Builder
	t := m.snap.TCP
	b.WriteString(stHeader.Render("TCP") + "\n")
	if !t.Available {
		b.WriteString(stMuted.Render("TCP counters unavailable.") + "\n")
	} else {
		b.WriteString(fmt.Sprintf("established %s   new out %s/s   new in %s/s   retrans %s   resets %s/s   rx errs %s/s\n",
			stPlain.Bold(true).Render(strconv.FormatInt(t.Established, 10)),
			f1(t.ActivePs), f1(t.PassivePs),
			lvl(t.RetransPct, 1, 5).Render(f1(t.RetransPs)+"/s ("+fmt.Sprintf("%.2f%%", t.RetransPct)+")"),
			f1(t.ResetPs), lvl(t.InErrPs, 0.1, 10).Render(f1(t.InErrPs))))
	}
	b.WriteString("\n" + stHeader.Render("INTERFACES") + "\n")
	if len(m.snap.NICs) == 0 {
		b.WriteString(stMuted.Render("No interfaces."))
	} else {
		b.WriteString(cell("NAME", 12, false, stHeader) + " " + cell("RX", 9, true, stHeader) + " " + cell("TX", 9, true, stHeader) + " " +
			cell("PPS IN", 8, true, stHeader) + " " + cell("PPS OUT", 8, true, stHeader) + " " + cell("ERR/S", 7, true, stHeader) + " " + cell("DROP/S", 7, true, stHeader) + "\n")
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
			return cell(n.Name, 12, false, stPlain.Bold(true)) + " " + cell(rateFmt(n.RxBps), 9, true, stPlain) + " " + cell(rateFmt(n.TxBps), 9, true, stPlain) + " " +
				cell(f1(n.RxPps), 8, true, stPlain) + " " + cell(f1(n.TxPps), 8, true, stPlain) + " " + cell(f1(n.ErrPs), 7, true, errStyle) + " " + cell(f1(n.DropPs), 7, true, dropStyle)
		})
		b.WriteString(strings.Join(lines, "\n"))
	}
	return b.String()
}
