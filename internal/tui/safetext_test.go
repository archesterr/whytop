package tui

import (
	"strings"
	"testing"

	"github.com/archesterr/whytop/internal/collect"
)

// Anyone who can start a process chooses its argv, and whytop draws argv into
// a root operator's terminal. Escape sequences there are this tool's
// equivalent of an injection: they can clear the screen, paint a convincing
// fake prompt, or hide the row doing it.
func TestUntrustedTextCannotDriveTheTerminal(t *testing.T) {
	evil := "\x1b[2J\x1b[H\x1b[41m FAKE PROMPT \x1b[0m\rhidden\x07"
	procs := []collect.Proc{{PID: 1234, Name: evil, User: "nobody", Cmdline: evil}}
	snap := &collect.Snapshot{Procs: procs, ByPID: map[int32]int{1234: 0}, Host: evil}
	m := model{snap: snap, sortKey: "pid", width: 132}

	for name, out := range map[string]string{
		"process table": m.renderProcs(132, 10),
		"header":        m.renderHeaderPanel(132),
		"detail":        model{snap: snap, width: 132, detail: &detailState{pid: 1234, loaded: true, journal: evil}}.renderDetail(132, 30),
	} {
		for _, bad := range []string{"\x1b", "\r", "\x07"} {
			// Our own styling emits ESC, so only the untrusted payload's
			// telltale sequences are checked for.
			if bad == "\x1b" {
				if strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x1b[41m") {
					t.Errorf("%s: a process's argv reached the terminal as live escape sequences", name)
				}
				continue
			}
			if strings.Contains(out, bad) {
				t.Errorf("%s: untrusted text emitted %q into the terminal", name, bad)
			}
		}
	}
}

func TestSafeTextKeepsRealTextIntact(t *testing.T) {
	for _, s := range []string{"/usr/sbin/nginx -g daemon off;", "systemd-udevd", "café ☕ 日本語", ""} {
		if got := safeText(s); got != s {
			t.Errorf("safeText(%q) altered legitimate text to %q", s, got)
		}
	}
	if got := safeText("a\x1bb\rc"); got != "a·b·c" {
		t.Errorf("safeText did not neutralise controls: %q", got)
	}
}
