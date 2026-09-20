package tui

import (
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/actions"
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
