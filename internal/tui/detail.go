package tui

import (
	"fmt"
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
	b.WriteString(title + "\n\n")

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
	if d.loaded && d.extra.OOMScore != "" {
		facts = append(facts, [2]string{"OOM score", d.extra.OOMScore + " (adjust " + d.extra.OOMAdj + ")"})
	}
	colW := w / 2
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
			line = pad(left, colW, false) + right
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + stMuted.Render("Command  ") + truncate(cmdOf(p), w-9) + "\n")

	restartLine := "restart: checking…"
	if d.loaded {
		restartLine = "restart: available"
		if d.restartBlocked != "" {
			restartLine = "restart: " + d.restartBlocked
		}
	}
	b.WriteString(stFaint.Render(restartLine) + "\n\n")

	treeH := (h - 16) / 2
	if treeH < 3 {
		treeH = 3
	}
	b.WriteString(stHeader.Render(fmt.Sprintf("PROCESS TREE (%d)", len(nodes))) + "\n")
	b.WriteString(m.renderTree(nodes, w, treeH) + "\n")

	logH := h - 16 - treeH
	if logH < 3 {
		logH = 3
	}
	b.WriteString("\n" + stHeader.Render("JOURNAL") + "\n")
	b.WriteString(renderJournal(d.journal, w, logH))

	return b.String()
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
	cmdW := w - (6 + 1 + 3 + 1 + 6 + 1 + 9 + 1 + 9 + 1) - 4
	if cmdW < 10 {
		cmdW = 10
	}
	header := cell("PID", 6, true, stHeader) + " " + cell("ST", 3, false, stHeader) + " " + cell("CPU%", 6, true, stHeader) + " " +
		cell("MEM", 9, true, stHeader) + " " + cell("I/O", 9, true, stHeader) + " " + cell("COMMAND", cmdW, false, stHeader)
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
		row := cell(strconv.Itoa(int(p.PID)), 6, true, withBG(stPlain, sel)) + " " + cell(p.State, 3, false, withBG(stateStyle(p.State), sel)) + " " +
			cell(f1(p.CPU), 6, true, withBG(lvl(p.CPU, 50, 90), sel)) + " " + cell(bytesFmt(float64(p.RSS)), 9, true, withBG(stPlain, sel)) + " " +
			ioCell(p.IOHidden, p.ReadBps+p.WriteBps, 9, sel) + " " + cell(name, cmdW, false, withBG(stMuted, sel))
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
