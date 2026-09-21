package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
)

// You cannot select text in whytop with the mouse, and the reason is not a
// bug so much as a consequence nobody chose: whytop asks the terminal for
// mouse reporting so that clicking a row selects it and the wheel scrolls
// the list. While that is on, the terminal hands every drag to whytop
// instead of painting a selection, so the gesture that copies a journal
// line everywhere else does nothing here.
//
// There are two honest fixes and this file is both, because they solve
// different halves of the problem.
//
// Copying the journal outright (y) is the better one, and not only as a
// workaround: it copies the whole buffer journalctl returned, not the dozen
// lines that fit on screen, and it keeps working when whytop is running on
// a server three SSH hops away — OSC 52 hands the text to the terminal
// emulator in front of the operator, which is the machine whose clipboard
// they actually want it on.
//
// Turning mouse reporting off (F10) is the other, for everything OSC 52
// cannot cover: a PID out of the process list, a mount path out of the
// header, half a command line. It gives the terminal back, and says so.

// copyToClipboard writes an OSC 52 sequence to the terminal. There is no
// error to report and no way to confirm it landed — the terminal either
// honours the sequence or ignores it, and never answers either way, which
// is why the toast says what was sent rather than claiming success.
func copyToClipboard(s string) tea.Cmd {
	return func() tea.Msg {
		if s == "" {
			return nil
		}
		// Written straight to the terminal rather than returned into the
		// frame: it is a control sequence, not content, and anything that
		// goes through the renderer would be measured, padded and truncated
		// to the width of a box.
		//
		// /dev/tty rather than stdout or stderr. stdout belongs to
		// bubbletea, which is painting frames on it from another goroutine,
		// and either stream may have been redirected — `whytop 2>err.log`
		// is a completely ordinary thing to type, and it would send the
		// clipboard into the log file. /dev/tty is the controlling terminal
		// whatever the streams are doing.
		w, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		if err != nil {
			// No controlling terminal: whytop is being driven by something
			// other than a person, and there is no clipboard to write to.
			return nil
		}
		defer w.Close()
		osc52.New(s).WriteTo(w)
		return nil
	}
}

// clipboardNote is what the toast says. Some terminals need the feature
// turned on explicitly, and a copy that silently did nothing is worse than
// one that says where to look — so the toast names the mechanism.
func clipboardNote(what string, lines int) string {
	unit := "lines"
	if lines == 1 {
		unit = "line"
	}
	return fmt.Sprintf("Copied %d %s of %s to the clipboard (OSC 52 — some terminals need it enabled)", lines, unit, what)
}

// countLines is what the toast reports, so it counts what was actually
// copied rather than what was on screen.
func countLines(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
