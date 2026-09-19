package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/archesterr/whytop/internal/collect"
)

func (m model) renderDetail(w, h int) string {
	p, ok := m.procByPID(m.detail.pid)
	if !ok {
		return stMuted.Render(fmt.Sprintf("PID %d no longer exists. It exited, was killed, or its unit started it again under a new PID.", m.detail.pid))
	}
	d := m.detail
	nodes := m.currentTree()

	var cpu, rss, rd, wr float64
	for _, q := range nodes {
		cpu += q.CPU
		rss += float64(q.RSS)
		rd += q.ReadBps
		wr += q.WriteBps
	}
	withKids := func(v string) string {
		if len(nodes) > 1 {
			return stMuted.Render(" (with children " + v + ")")
		}
		return ""
	}

	var b strings.Builder
	title := stBold.Render(p.Name) + stMuted.Render(fmt.Sprintf("  PID %d", p.PID))
	if p.Unit != "" {
		title += "  " + stAccent.Render(unitName(p.Unit))
	}
	b.WriteString(title + "\n")

	parent := "–"
	if pp, ok := m.procByPID(p.PPID); ok {
		parent = fmt.Sprintf("%s (%d)", pp.Name, p.PPID)
	} else if p.PPID > 0 {
		parent = strconv.Itoa(int(p.PPID))
	}

	fds := "…"
	if d.loaded {
		if d.extra.FDs < 0 {
			fds = "needs root"
		} else if lim, err := strconv.Atoi(d.extra.FDLimit); err == nil && lim > 0 {
			fds = fmt.Sprintf("%d of %d", d.extra.FDs, lim)
		} else {
			fds = strconv.Itoa(d.extra.FDs)
		}
	}
	unitStatus := ""
	if d.unitStatus != nil && d.unitStatus["ActiveState"] != "" {
		unitStatus = fmt.Sprintf("  [%s/%s]", d.unitStatus["ActiveState"], d.unitStatus["SubState"])
	}

	facts := [][2]string{
		{"State", stateStyle(p.State).Render(p.State) + " " + stateDesc(p.State)},
		{"User", p.User},
		{"Parent", parent},
		{"Started", ago(p.Started) + " ago"},
		{"CPU", f1(p.CPU) + "%" + withKids(f1(cpu)+"%")},
		{"Memory", bytesFmt(float64(p.RSS)) + withKids(bytesFmt(rss))},
		{"Disk read", ioCellText(p.IOHidden, p.ReadBps) + withKids(rateFmt(rd))},
		{"Disk write", ioCellText(p.IOHidden, p.WriteBps) + withKids(rateFmt(wr))},
		{"Threads", strconv.Itoa(int(p.Threads))},
		{"Open files", fds},
		{"Unit", unitName(p.Unit) + unitStatus},
	}
	if p.Container != "" {
		facts = append(facts, [2]string{"Container", stAccent.Render(p.Container) + " " + stMuted.Render(p.Runtime)})
	}
	if d.loaded && d.extra.OOMScore != "" {
		facts = append(facts, [2]string{"OOM score", d.extra.OOMScore + " (adjust " + d.extra.OOMAdj + ")"})
	}
	colW := (w - sepW) / 2
	if colW < 24 {
		colW = 24
	}
	for i := 0; i < len(facts); i += 2 {
		left := stMuted.Render(pad2(facts[i][0], 12)) + " " + facts[i][1]
		line := left
		if i+1 < len(facts) {
			right := stMuted.Render(pad2(facts[i+1][0], 12)) + " " + facts[i+1][1]
			// pad(), not cell(): left already carries other cells' ANSI
			// codes (the colored state pill, "with children" annotations),
			// and re-styling text that already contains styling corrupts
			// the escape sequences instead of composing with them.
			line = pad(left, colW, false) + colSep + right
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(stMuted.Render("Command  ") + truncate(cmdOf(p), w-9) + "\n")

	restartLine := "restart: checking…"
	if d.loaded {
		restartLine = "restart: available"
		if d.restartBlocked != "" {
			restartLine = "restart: " + d.restartBlocked
		}
	}
	b.WriteString(stFaint.Render(restartLine) + "\n")

	// Five units: tree gets two (it's usually what you came here for), the
	// other three sections one each.
	bottom := max0(h - 14)
	unit := bottom / 5
	if unit < 3 {
		unit = 3
	}
	treeH := 2 * unit
	sockH := unit
	filesH := unit
	logH := max0(bottom - treeH - sockH - filesH)
	if logH < 3 {
		logH = 3
	}

	b.WriteString(stHeader.Render(fmt.Sprintf("PROCESS TREE (%d)", len(nodes))) + "\n")
	b.WriteString(m.renderTree(nodes, w, treeH) + "\n")

	b.WriteString(stHeader.Render("SOCKETS") + "\n")
	b.WriteString(m.renderSockets(nodes, w, sockH) + "\n")

	// x/X below stop/force-kill this same process — the one holding every
	// file listed here, so no separate kill control is needed per row.
	filesHeader := "OPEN FILES"
	if d.loaded && d.extra.FDs >= 0 {
		filesHeader = fmt.Sprintf("OPEN FILES (%d)", d.extra.FDs)
	}
	b.WriteString(stHeader.Render(filesHeader) + "\n")
	b.WriteString(m.renderOpenFiles(w, filesH) + "\n")

	b.WriteString(stHeader.Render("JOURNAL") + "\n")
	b.WriteString(renderJournal(d.journal, w, logH))

	return b.String()
}

// renderSockets lists the listening/established sockets owned by the
// process or any of its children — data whytop already collects for this
// view but, until now, never displayed. htop/top show nothing about a
// process's network activity at all; iotop is disk-only.
func (m model) renderSockets(nodes []collect.Proc, w, h int) string {
	s := m.snap
	if !s.ConnsCollected {
		return stMuted.Render("Reading sockets…")
	}
	pids := make(map[int32]bool, len(nodes))
	for _, n := range nodes {
		pids[n.PID] = true
	}
	var conns []collect.Conn
	for _, c := range s.Conns {
		if pids[c.PID] {
			conns = append(conns, c)
		}
	}
	if len(conns) == 0 {
		msg := "No network sockets."
		if !s.Root {
			msg = "No sockets visible. Run whytop with sudo to see sockets of other users."
		}
		return stMuted.Render(msg)
	}
	sort.Slice(conns, func(i, j int) bool {
		li, lj := conns[i].Listening(), conns[j].Listening()
		if li != lj {
			return li
		}
		return conns[i].LPort < conns[j].LPort
	})
	remoteW := max0(w - (6 + 6 + 12) - 3*sepW)
	header := joinCols(centerCell("PORT", 6, stHeader), centerCell("PROTO", 6, stHeader),
		centerCell("STATE", 12, stHeader), centerCell("REMOTE", remoteW, stHeader))
	lines := capRows(conns, h-1, func(c collect.Conn) string {
		stStyle := stPlain
		if c.State == "LISTEN" {
			stStyle = stOK
		} else if c.State == "CLOSE_WAIT" {
			stStyle = stCrit
		}
		return joinCols(cell(strconv.Itoa(int(c.LPort)), 6, true, stPlain.Bold(true)), cell(c.Proto, 6, false, stMuted),
			cell(c.State, 12, false, stStyle), cell(c.Remote, remoteW, false, stMuted))
	})
	return header + "\n" + strings.Join(lines, "\n")
}

// renderOpenFiles lists what this process actually has open right now — the
// direct answer to "what files is this touching," which neither top, htop
// nor iotop show at all (lsof/ls -l /proc/<pid>/fd is the usual answer, a
// separate shell round-trip away). Real files sort first, then pipes,
// sockets (already detailed in SOCKETS above), and anonymous kernel fds
// (eventfd/timerfd/etc.) last.
var fdKindRank = map[string]int{"file": 0, "deleted": 0, "pipe": 1, "socket": 2, "anon": 3}

func (m model) renderOpenFiles(w, h int) string {
	d := m.detail
	if !d.loaded {
		return stMuted.Render("Loading…")
	}
	if len(d.extra.OpenFiles) == 0 {
		if d.extra.FDs < 0 {
			return stMuted.Render("Open files hidden. Run whytop with sudo.")
		}
		return stMuted.Render("No open files.")
	}
	files := make([]collect.OpenFile, len(d.extra.OpenFiles))
	copy(files, d.extra.OpenFiles)
	sort.SliceStable(files, func(i, j int) bool { return fdKindRank[files[i].Kind] < fdKindRank[files[j].Kind] })

	fdW, kindW := 4, 8
	targetW := max0(w - fdW - kindW - 2*sepW)
	if targetW < 10 {
		targetW = 10
	}
	header := joinCols(centerCell("FD", fdW, stHeader), centerCell("KIND", kindW, stHeader), centerCell("TARGET", targetW, stHeader))
	lines := capRows(files, h-1, func(f collect.OpenFile) string {
		style := stMuted
		if f.Kind == "deleted" {
			style = stWarn
		} else if f.Kind == "file" {
			style = stPlain
		}
		return joinCols(cell(f.FD, fdW, true, stFaint), cell(f.Kind, kindW, false, stFaint), cell(f.Target, targetW, false, style))
	})
	return header + "\n" + strings.Join(lines, "\n")
}

func ioCellText(hidden bool, v float64) string {
	if hidden {
		return stFaint.Render("hidden")
	}
	return rateFmt(v)
}

func stateDesc(s string) string {
	names := map[string]string{"R": "running", "S": "sleeping", "D": "waiting on I/O", "Z": "zombie", "T": "stopped", "t": "traced", "I": "idle"}
	return stMuted.Render(names[s])
}

func pad2(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func (m model) renderTree(nodes []collect.Proc, w, h int) string {
	if len(nodes) == 0 {
		return stMuted.Render("(no data)")
	}
	// depth per node, matching currentTree's DFS order.
	depth := make([]int, len(nodes))
	byPID := map[int32]int{}
	for i, p := range nodes {
		byPID[p.PID] = i
	}
	for i, p := range nodes {
		if i == 0 {
			continue
		}
		if pi, ok := byPID[p.PPID]; ok {
			depth[i] = depth[pi] + 1
		}
	}
	cmdW := w - (6 + 3 + 6 + 9 + 9) - 5*sepW - 4
	if cmdW < 10 {
		cmdW = 10
	}
	header := joinCols(centerCell("PID", 6, stHeader), centerCell("ST", 3, stHeader), centerCell("CPU%", 6, stHeader),
		centerCell("MEM", 9, stHeader), centerCell("I/O", 9, stHeader), centerCell("COMMAND", cmdW, stHeader))
	var lines []string
	lines = append(lines, header)
	for i, p := range nodes {
		if i >= h-1 {
			lines = append(lines, stFaint.Render(fmt.Sprintf("… %d more", len(nodes)-i)))
			break
		}
		indent := strings.Repeat("  ", depth[i])
		branch := ""
		if depth[i] > 0 {
			branch = "└ "
		}
		name := truncate(indent+branch+cmdOf(p), cmdW)
		sel := i == m.detail.treeSel
		row := joinColsSel(sel,
			cell(strconv.Itoa(int(p.PID)), 6, true, withBG(stPlain, sel)), cell(p.State, 3, false, withBG(stateStyle(p.State), sel)),
			cell(f1(p.CPU), 6, true, withBG(lvl(p.CPU, 50, 90), sel)), cell(bytesFmt(float64(p.RSS)), 9, true, withBG(stPlain, sel)),
			ioCell(p.IOHidden, p.ReadBps+p.WriteBps, 9, sel), cell(name, cmdW, false, withBG(stMuted, sel)))
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n")
}

func renderJournal(text string, w, h int) string {
	if text == "" {
		return stMuted.Render("Loading…")
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for i, l := range lines {
		lines[i] = truncate(l, w)
	}
	return stMuted.Render(strings.Join(lines, "\n"))
}
