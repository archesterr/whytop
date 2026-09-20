package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// This file holds the colour decisions that are about *reading a table*
// rather than about one widget. The rule behind all of them: colour has to
// carry information, or it is noise that makes the real signal (a D-state
// process, a full disk) harder to find. Nothing here is decorative.

// cmdCell renders a command line in two tones: the path it was launched
// from recedes, the program name stands out, its arguments sit between the
// two. A process list is mostly long absolute paths, and the part you are
// actually scanning for — "is that nginx or node?" — is a dozen characters
// buried in the middle of them.
//
// It builds the styled string itself rather than going through cell(),
// because cell() paints one style over the whole value.
func cmdCell(s string, w int, sel bool) string {
	return cmdCellAt(s, treeRow{}, w, sel)
}

// cmdCellAt prefixes a row with its branch guide, which is what makes a
// forest readable as one. The guide is part of the cell rather than a
// separate column so it is cut with the text when the terminal is narrow,
// instead of pushing the command out of sight.
func cmdCellAt(s string, t treeRow, w int, sel bool) string {
	if w <= 0 {
		return ""
	}
	s = safeText(s)
	if t.guide != "" {
		// A deep branch can be wider than the column it is drawn in. The
		// guide is cut to fit rather than allowed to run past the cell:
		// overflowing here doesn't wrap, it shifts every column to the
		// right of COMMAND by however far the indent ran over.
		guide := t.guide
		if visLen(guide) > w {
			guide = string([]rune(guide)[:w])
		}
		out := withBG(t.guideStyle(), sel).Render(guide)
		rest := cmdCellAt(s, treeRow{}, max0(w-visLen(guide)), sel)
		return pad(out+rest, w, sel)
	}
	if len([]rune(s)) > w {
		s = truncate(s, w)
	}

	prog, args, _ := strings.Cut(s, " ")
	dir, base := "", prog
	if i := strings.LastIndex(prog, "/"); i >= 0 {
		dir, base = prog[:i+1], prog[i+1:]
	}

	out := ""
	if dir != "" {
		out += withBG(stFaint, sel).Render(dir)
	}
	out += withBG(stCmd, sel).Render(base)
	if args != "" {
		out += withBG(stCmdArgs, sel).Render(" " + args)
	}
	return pad(out, w, sel)
}

// userCell tells root apart from everyone else at a glance. On a server
// that's the distinction that decides whether a process is yours to kill
// without thinking about it, and reading the username character by
// character is exactly the kind of work colour can do for you.
func userCell(u string, w int, sel bool) string {
	st := stUser
	if u == "root" {
		st = stUserRoot
	}
	return cell(u, w, false, withBG(st, sel))
}

// memCell colours resident memory against the machine it's running on, not
// against a fixed number of bytes: 800 MiB is unremarkable on a 128 GiB host
// and most of the box on a 2 GiB VM, and the same figure shouldn't look
// equally urgent in both.
func memCell(rss uint64, total uint64, w int, sel bool) string {
	st := stPlain
	if total > 0 {
		switch pct := float64(rss) / float64(total) * 100; {
		case pct >= 20:
			st = stMemHigh
		case pct >= 5:
			st = stMemMid
		}
	}
	return cell(bytesFmt(float64(rss)), w, true, withBG(st, sel))
}

// gauge draws a proportional bar. The vitals row already prints every number
// it has; the bar is there so the shape of the machine's load registers
// before you've read any of them — which is the one thing htop's meters get
// unarguably right.
//
// The filled part carries the metric's own colour so the five cards stay
// distinguishable from each other, and takes the warn/crit colour once the
// value is actually worth looking at, so "this one is the problem" survives
// being glanced at rather than read.
func gauge(frac float64, w int, fill lipgloss.Style) string {
	if w <= 0 {
		return ""
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	n := int(frac*float64(w) + 0.5)
	if n == 0 && frac > 0 {
		n = 1 // a metric that is on at all should never render as empty
	}
	if n > w {
		n = w
	}
	return fill.Render(strings.Repeat("▇", n)) + stGaugeBg.Render(strings.Repeat("▁", w-n))
}
