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

	nameW, loadW, activeW, subW := 32, 8, 10, 10
	descW := w - (gutterW + nameW + loadW + activeW + subW) - 5*sepW
	if descW < 12 {
		descW = 12
	}
	header := tableHeader(w, centerCell("", gutterW, stHdrCell), centerCell("UNIT", nameW, stHdrCell), centerCell("LOAD", loadW, stHdrCell),
		centerCell("ACTIVE", activeW, stHdrCell), centerCell("SUB", subW, stHdrCell), centerCell("DESCRIPTION", descW, stHdrCell))

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
		row := joinColsSel(sel, gutterCell(sel),
			cell(u.Name, nameW, false, withBG(stPlain.Bold(true), sel)),
			cell(u.Load, loadW, false, withBG(stMuted, sel)),
			cell(u.Active, activeW, false, withBG(activeStyle, sel)),
			cell(u.Sub, subW, false, withBG(stMuted, sel)),
			cell(u.Description, descW, false, withBG(stMuted, sel)))
		lines = append(lines, row)
	}
	if end < len(units) || start > 0 {
		lines = append(lines, stFaint.Render(scrollNotice(len(units), start, end)))
	}
	return strings.Join(lines, "\n")
}
