package tui

import (
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/actions"
	"github.com/archesterr/whytop/internal/collect"
)

// startEditMsg carries the *exec.Cmd for tea.ExecProcess to run once
// UnitFragmentPath has resolved (a blocking systemctl call, so it happens in
// the tea.Cmd goroutine, not here).
type startEditMsg struct {
	cmd  *exec.Cmd
	unit string
}

type editDoneMsg struct {
	err  error
	unit string
}

func (m *model) moveUnitSel(delta int) {
	if m.snap == nil {
		return
	}
	units := m.snap.Units
	if len(units) == 0 {
		return
	}
	idx := 0
	for i, u := range units {
		if u.Name == m.unitSel {
			idx = i
			break
		}
	}
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(units) {
		idx = len(units) - 1
	}
	m.unitSel = units[idx].Name
}

func (m model) selectedUnit() (collect.Unit, bool) {
	if m.snap == nil {
		return collect.Unit{}, false
	}
	for _, u := range m.snap.Units {
		if u.Name == m.unitSel {
			return u, true
		}
	}
	return collect.Unit{}, false
}

// editUnitCmd resolves the unit's on-disk file and launches $EDITOR on it,
// suspending the TUI for the duration (tea.ExecProcess) the same way a
// terminal editor normally takes over the screen. Editing never runs
// `systemctl daemon-reload` on its own — see editDoneMsg's confirm prompt.
func (m model) editUnitCmd(unit string) tea.Cmd {
	return func() tea.Msg {
		path, err := actions.UnitFragmentPath(unit)
		if err != nil {
			return actionMsg{ok: false, text: err.Error()}
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		fields := strings.Fields(editor)
		if len(fields) == 0 {
			fields = []string{"vi"}
		}
		cmd := exec.Command(fields[0], append(fields[1:], path)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return startEditMsg{cmd: cmd, unit: unit}
	}
}

func doDaemonReload() tea.Cmd {
	return func() tea.Msg {
		if err := actions.DaemonReload(); err != nil {
			return actionMsg{ok: false, text: err.Error()}
		}
		return actionMsg{ok: true, text: "systemd daemon reloaded"}
	}
}

// unitMemory totals the resident memory of every process belonging to each
// unit. The processes are already collected with the unit they belong to, so
// this costs nothing — where asking systemd for MemoryCurrent would mean a
// `systemctl show` round-trip per unit, hundreds of them on a normal host.
func (m model) unitMemory() map[string]uint64 {
	out := map[string]uint64{}
	if m.snap == nil {
		return out
	}
	for _, p := range m.snap.Procs {
		if p.Unit != "" {
			out[p.Unit] += p.RSS
		}
	}
	return out
}

// memCell renders a unit's memory, distinguishing "nothing running" from
// "running but using no memory" — a stopped unit has no processes at all,
// and printing 0 B for it would read as a measurement rather than an absence.
func memCell(mem map[string]uint64, unit string, w int, sel bool) string {
	rss, ok := mem[unit]
	if !ok {
		return cell("–", w, true, withBG(stFaint, sel))
	}
	return cell(bytesFmt(float64(rss)), w, true, withBG(stPlain, sel))
}

// renderUnits lists systemd service units — none of top, htop or iotop know
// systemd exists at all, so seeing (and here, editing) a unit's state means
// leaving the monitoring tool entirely for `systemctl`/`systemd-edit` in a
// separate terminal.
func (m model) renderUnits(w, h int) string {
	if m.snap == nil || !m.snap.UnitsCollected {
		return stMuted.Render("Reading systemd units…")
	}
	units := m.snap.Units
	if len(units) == 0 {
		return stMuted.Render("No service units.")
	}

	mem := m.unitMemory()

	// Description is the first thing to go on a narrow terminal — it repeats
	// what the unit name already says far more often than it adds anything,
	// and the unit name is what you came to read.
	nameW, loadW, activeW, subW, memW := 32, 8, 10, 10, 9
	fixed := gutterW + loadW + activeW + subW + memW
	descW := w - fixed - nameW - 6*sepW
	showDesc := descW >= 16
	if !showDesc {
		nameW = max0(w - fixed - 5*sepW)
		if nameW < 12 {
			nameW = 12
		}
	}

	headerCells := []string{hdrCell("", gutterW, stHdrCell), hdrCell("UNIT", nameW, stHdrCell), hdrCell("LOAD", loadW, stHdrCell),
		hdrCell("ACTIVE", activeW, stHdrCell), hdrCell("SUB", subW, stHdrCell), hdrCell("MEM", memW, stHdrCell)}
	if showDesc {
		headerCells = append(headerCells, hdrCell("DESCRIPTION", descW, stHdrCell))
	}
	header := tableHeader(w, headerCells...)

	selIdx := -1
	for i, u := range units {
		if u.Name == m.unitSel {
			selIdx = i
			break
		}
	}
	start, end := windowRows(len(units), selIdx, h-1)
	lines := []string{header}
	for i := start; i < end; i++ {
		u := units[i]
		sel := i == selIdx
		activeStyle := stMuted
		switch u.Active {
		case "active":
			activeStyle = stOK
		case "failed":
			activeStyle = stCrit
		case "activating", "reloading":
			activeStyle = stWarn
		}
		rowCells := []string{gutterCell(sel),
			cell(u.Name, nameW, false, withBG(stPlain.Bold(true), sel)),
			cell(u.Load, loadW, false, withBG(stMuted, sel)),
			cell(u.Active, activeW, false, withBG(activeStyle, sel)),
			cell(u.Sub, subW, false, withBG(stMuted, sel)),
			memCell(mem, u.Name, memW, sel)}
		if showDesc {
			rowCells = append(rowCells, cell(u.Description, descW, false, withBG(stMuted, sel)))
		}
		lines = append(lines, joinColsSel(sel, rowCells...))
	}
	if end < len(units) || start > 0 {
		lines = append(lines, stFaint.Render(scrollNotice(len(units), start, end)))
	}
	return strings.Join(lines, "\n")
}
