package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/actions"
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
	title := stBold.Render(safeText(p.Name)) + stMuted.Render(fmt.Sprintf("  PID %d", p.PID))
	if p.Unit != "" {
		title += "  " + stAccent.Render(safeText(unitName(p.Unit)))
	}
	b.WriteString(title + "\n")

	parent := "–"
	if pp, ok := m.procByPID(p.PPID); ok {
		parent = fmt.Sprintf("%s (%d)", safeText(pp.Name), p.PPID)
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
		{"User", safeText(p.User)},
		{"Parent", parent},
		{"Started", ago(p.Started) + " ago"},
		{"CPU", f1(p.CPU) + "%" + withKids(f1(cpu)+"%")},
		{"Memory", bytesFmt(float64(p.RSS)) + withKids(bytesFmt(rss))},
		{"Disk read", ioCellText(p.IOHidden, p.ReadBps) + withKids(rateFmt(rd))},
		{"Disk write", ioCellText(p.IOHidden, p.WriteBps) + withKids(rateFmt(wr))},
		{"Threads", strconv.Itoa(int(p.Threads))},
		{"Open files", fds},
		{"Unit", safeText(unitName(p.Unit) + unitStatus)},
	}
	if p.Container != "" {
		facts = append(facts, [2]string{"Container", stAccent.Render(safeText(p.Container)) + " " + stMuted.Render(safeText(p.Runtime))})
	}
	// Only when it is happening: a CPU% that looks modest next to a
	// throttled cgroup is the process waiting for quota, not idle.
	if p.Throttled >= 1 {
		facts = append(facts, [2]string{"Throttled", stWarn.Render(fmt.Sprintf("%.0f%% of CPU periods", p.Throttled))})
	}
	if d.loaded && d.extra.OOMScore != "" {
		facts = append(facts, [2]string{"OOM score", d.extra.OOMScore + " (adjust " + d.extra.OOMAdj + ")"})
	}
	// Three columns where there's room, two otherwise. A fact is a short
	// label and a short value, so two columns on a wide terminal left half
	// the row empty and pushed the panel taller than it needed to be.
	nCol := 2
	if w >= 110 {
		nCol = 3
	}
	// The separator is padded (" │ ") rather than bare: two facts butted up
	// against a single line read as one run of text, which is the thing the
	// columns exist to prevent.
	factSep := " " + stBox2.Render(boxV) + " "
	colW := (w - (nCol-1)*visLen(factSep)) / nCol
	if colW < 24 {
		colW, nCol = w, 1
	}
	for i := 0; i < len(facts); i += nCol {
		var cells []string
		for j := i; j < i+nCol && j < len(facts); j++ {
			// pad(), not cell(): the value already carries other styles'
			// ANSI codes (the coloured state, "with children" annotations),
			// and re-styling text that already contains styling corrupts
			// the escape sequences instead of composing with them.
			fact := stMuted.Render(pad2(facts[j][0], 11)) + " " + facts[j][1]
			cells = append(cells, pad(truncateANSI(fact, colW), colW, false))
		}
		b.WriteString(joinColsWith(factSep, cells...) + "\n")
	}
	b.WriteString(stMuted.Render("Command  ") + truncate(safeText(cmdOf(p)), w-9) + "\n")

	restartLine := "restart: checking…"
	if d.loaded {
		restartLine = "restart: available"
		if d.restartBlocked != "" {
			restartLine = "restart: " + d.restartBlocked
		}
	}
	b.WriteString(stFaint.Render(restartLine) + "\n")

	// Five shares of whatever's left: the tree takes two (it's usually what
	// you came here for), the other three sections one each.
	//
	// The shares are divided, never floored up: flooring each section at
	// three rows made them add up to more than the panel had on a 26-row
	// terminal, and the overflow came off the bottom — so the JOURNAL bar
	// rendered with nothing at all underneath it.
	bottom := max0(h - 14)
	treeH := bottom * 2 / 5
	sockH := bottom / 5
	filesH := bottom / 5
	logH := max0(bottom - treeH - sockH - filesH)
	// Two rows is the floor that still shows a column header and one row.
	for _, v := range []*int{&treeH, &sockH, &filesH, &logH} {
		if *v < 2 {
			*v = 2
		}
	}

	b.WriteString(focusBar(w, fmt.Sprintf("PROCESS TREE (%d)", len(nodes)), d.focus == focusTree) + "\n")
	b.WriteString(m.renderTree(nodes, w, treeH) + "\n")

	b.WriteString(sectionBar(w, "SOCKETS") + "\n")
	b.WriteString(m.renderSockets(nodes, w, sockH) + "\n")

	// x/X below stop/force-kill this same process — the one holding every
	// file listed here, so no separate kill control is needed per row.
	filesHeader := "OPEN FILES"
	if d.loaded && d.extra.FDs >= 0 {
		filesHeader = fmt.Sprintf("OPEN FILES (%d)", d.extra.FDs)
	}
	b.WriteString(focusBar(w, filesHeader, d.focus == focusFiles) + "\n")
	b.WriteString(m.renderOpenFiles(w, filesH) + "\n")

	journalTitle := "JOURNAL"
	if d.follow {
		journalTitle += "  ● live"
	} else {
		journalTitle += "  paused (f)"
	}
	b.WriteString(sectionBar(w, journalTitle) + "\n")
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
	header := tableHeader(w, hdrCell("PORT", 6, stHdrCell), hdrCell("PROTO", 6, stHdrCell),
		hdrCell("STATE", 12, stHdrCell), hdrCell("REMOTE", remoteW, stHdrCell))
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

// openFiles is the list in the order it's drawn. Both the renderer and the
// descriptor actions read it, so the row under the cursor is always the
// descriptor that gets acted on.
func (m model) openFiles() []collect.OpenFile {
	if m.detail == nil {
		return nil
	}
	files := make([]collect.OpenFile, len(m.detail.extra.OpenFiles))
	copy(files, m.detail.extra.OpenFiles)
	sort.SliceStable(files, func(i, j int) bool { return fdKindRank[files[i].Kind] < fdKindRank[files[j].Kind] })
	return files
}

func (m model) selectedFile() (collect.OpenFile, bool) {
	files := m.openFiles()
	if m.detail == nil || m.detail.fileSel < 0 || m.detail.fileSel >= len(files) {
		return collect.OpenFile{}, false
	}
	return files[m.detail.fileSel], true
}

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
	files := m.openFiles()

	fdW, kindW := 4, 8
	targetW := max0(w - gutterW - fdW - kindW - 3*sepW)
	if targetW < 10 {
		targetW = 10
	}
	header := tableHeader(w, hdrCell("", gutterW, stHdrCell), hdrCell("FD", fdW, stHdrCell),
		hdrCell("KIND", kindW, stHdrCell), hdrCell("TARGET", targetW, stHdrCell))

	selIdx := -1
	if d.focus == focusFiles {
		selIdx = d.fileSel
	}
	start, end := windowRows(len(files), selIdx, h-1)
	lines := []string{header}
	for i := start; i < end; i++ {
		f := files[i]
		sel := i == selIdx
		style := stMuted
		if f.Kind == "deleted" {
			style = stWarn // deleted but still open: this is what eats a disk
		} else if f.Kind == "file" {
			style = stPlain
		}
		lines = append(lines, joinColsSel(sel, gutterCell(sel, kinNone),
			cell(f.FD, fdW, true, withBG(stFaint, sel)), cell(f.Kind, kindW, false, withBG(stFaint, sel)),
			cell(f.Target, targetW, false, withBG(style, sel))))
	}
	if end < len(files) || start > 0 {
		lines = append(lines, stFaint.Render(scrollNotice(len(files), start, end)))
	}
	return strings.Join(lines, "\n")
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
	header := tableHeader(w, hdrCell("PID", 6, stHdrCell), hdrCell("ST", 3, stHdrCell), hdrCell("CPU%", 6, stHdrCell),
		hdrCell("MEM", 9, stHdrCell), hdrCell("I/O", 9, stHdrCell), hdrCell("COMMAND", cmdW, stHdrCell))
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
		// Scrubbed per line, after the split: the newlines are this text's
		// structure, everything else in it is a log message someone else
		// wrote and is not to be trusted with the terminal.
		lines[i] = truncate(safeText(l), w)
	}
	return stMuted.Render(strings.Join(lines, "\n"))
}

// moveDetailSel moves whichever of the panel's two lists currently has focus.
func (d *detailState) moveDetailSel(delta, tree, files int) {
	sel, n := &d.treeSel, tree
	if d.focus == focusFiles {
		sel, n = &d.fileSel, files
	}
	if n == 0 {
		*sel = 0
		return
	}
	*sel += delta
	if *sel < 0 {
		*sel = 0
	}
	if *sel > n-1 {
		*sel = n - 1
	}
}

func doTruncateFD(pid int32, fd, target string) tea.Cmd {
	return func() tea.Msg {
		// Asked before the truncate, because afterwards the descriptor
		// may already be gone.
		appends := actions.FDAppends(pid, fd)
		if err := actions.TruncateFD(pid, fd, target); err != nil {
			return actionMsg{ok: false, text: err.Error()}
		}
		note := ""
		if !appends {
			// See actions.FDAppends: the space is back, but this writer
			// resumes at the offset it held and leaves a hole behind it,
			// so the next person to run ls sees the old size and concludes
			// nothing happened.
			note = " (the writer does not append, so the file goes sparse: df and du show the space back, ls -l will still show the old size)"
		}
		return actionMsg{ok: true, text: fmt.Sprintf("Emptied fd %s of PID %d — space reclaimed, process untouched%s", fd, pid, note)}
	}
}

func doCloseFD(pid int32, fd string) tea.Cmd {
	return func() tea.Msg {
		if err := actions.CloseFD(pid, fd); err != nil {
			return actionMsg{ok: false, text: err.Error()}
		}
		return actionMsg{ok: true, text: fmt.Sprintf("Closed fd %s in PID %d", fd, pid)}
	}
}
