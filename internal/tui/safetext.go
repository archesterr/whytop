package tui

import "strings"

// safeText strips control characters out of text that came from outside
// whytop — a process's own argv, a unit description, a journal line, the
// target of a file descriptor.
//
// Anyone who can start a process chooses its argv, and whytop renders argv
// straight into a root operator's terminal. Left alone, an "\x1b[2J" in a
// process name clears that operator's screen and everything after it can
// repaint whatever it likes: a convincing fake confirmation prompt, or rows
// positioned to hide the very process doing it. A carriage return alone is
// enough to overwrite the start of its own row. Escape sequences are the
// injection vector a terminal UI has in place of SQL, and the only safe
// assumption is that every string from /proc or from another command is
// hostile.
//
// This runs at the render boundary rather than at collection, because that's
// the single place every one of those strings has to pass through on its way
// to the terminal — a field sanitised at collection is only safe until
// someone adds a new field and forgets.
func safeText(s string) string {
	if strings.IndexFunc(s, isCtrlRune) < 0 {
		return s // overwhelmingly the common case: don't rebuild the string
	}
	return strings.Map(func(r rune) rune {
		if isCtrlRune(r) {
			return '·'
		}
		return r
	}, s)
}

// isCtrlRune covers C0 (which includes ESC, CR, LF and BEL), DEL, and the C1
// range that a number of terminals still act on when it arrives as UTF-8.
func isCtrlRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}
