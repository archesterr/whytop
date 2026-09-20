package tui

import "strings"

// The help screen exists because the footer can only ever show the handful
// of keys that fit, and the answer to "what else can this do" should not be
// the README. It is laid out as two columns: what htop and top already call
// these things, and what is whytop's own — so someone who knows htop can
// skip the first column entirely and read only what is new.
type helpEntry struct{ keys, what string }

var helpFamiliar = []helpEntry{
	{"↑ ↓ PgUp PgDn", "move the cursor"},
	{"k  F9", "kill (SIGTERM, confirmed)"},
	{"K", "show / hide kernel threads"},
	{"P  M  T", "sort by CPU, memory, time"},
	{"<  >  F6", "previous / next sort column"},
	{"I  R", "invert the sort order"},
	{"t  F5", "tree view"},
	{"p", "full program path on / off"},
	{"u", "filter by user"},
	{"/  F3", "search"},
	{"l", "open files of this process"},
	{"h  ?  F1", "this help"},
	{"q  F10", "quit"},
}

var helpOwn = []helpEntry{
	{"Enter", "open the process panel"},
	{"g", "go to the next problem"},
	{"@", "hosts — watch another box over SSH"},
	{"L", "lock the row order"},
	{"Space", "pause / resume sampling"},
	{"Tab", "in the filter: change what it searches"},
	{"Esc", "clear the filter, or close a panel"},
}

var helpPanel = []helpEntry{
	{"Tab", "switch between the tree and open files"},
	{"x  X", "stop / force kill"},
	{"r", "restart the unit"},
	{"e", "edit the unit file"},
	{"j  f", "reload / follow the journal"},
	{"t  c", "empty a file / close a descriptor"},
}

func (m model) renderHelp(w int) string {
	var b strings.Builder
	col := w / 2
	if col < 30 {
		col = w
	}

	section := func(title string, rows []helpEntry, width int) []string {
		out := []string{stBoxTitle.Render(title)}
		for _, e := range rows {
			out = append(out, pad(stFooterKey.Render(" "+e.keys+" ")+" "+stMuted.Render(e.what), width, false))
		}
		return out
	}

	left := section("SAME AS htop / top", helpFamiliar, col)
	right := append(section("WHYTOP'S OWN", helpOwn, col), "", "")
	right = append(right, section("IN THE PROCESS PANEL", helpPanel, col)...)

	for i := 0; i < len(left) || i < len(right); i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if col == w {
			b.WriteString(pad(l, w, false) + "\n")
			continue
		}
		b.WriteString(pad(l, col, false) + r + "\n")
	}
	b.WriteString("\n" + stFaint.Render(truncate(
		"Keys follow htop and top wherever they have a name for something. Anything they don't is on a key neither of them uses.", w)))
	return b.String()
}
