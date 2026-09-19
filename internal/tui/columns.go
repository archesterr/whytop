package tui

// procCol is one column of the Processes table. The renderer, the clickable
// header and the sort comparator are all built from this one list, so a
// column you click can never drift from the column drawn above the rows —
// the same reason tabRegions exists for the tab bar.
type procCol struct {
	key   string // sort key; "" for the selection gutter, which isn't sortable
	title string
	w     int
	right bool // numeric column: right-align its values
}

// numericSort reports whether a column sorts biggest-first by default.
// Clicking "MEM" should put the memory hogs on top; clicking "USER" should
// read alphabetically. Guessing wrong here means every user's first click
// shows them the least interesting end of the list.
func numericSort(key string) bool {
	switch key {
	case "cpu", "mem", "read", "write", "io":
		return true
	}
	return false
}

const procUnitW = 16

// procColumns lays out the Processes table for a given width. The Unit column
// is the first to go on a narrow terminal, rather than squeezing Command down
// to an unreadable sliver to keep every column present.
func procColumns(w int) []procCol {
	cols := []procCol{
		{key: "", title: "", w: gutterW},
		{key: "pid", title: "PID", w: 6, right: true},
		{key: "user", title: "USER", w: 11},
		{key: "state", title: "ST", w: 3},
		{key: "cpu", title: "CPU%", w: 6, right: true},
		{key: "mem", title: "MEM", w: 9, right: true},
		{key: "read", title: "READ", w: 9, right: true},
		{key: "write", title: "WRITE", w: 9, right: true},
	}
	fixed := 0
	for _, c := range cols {
		fixed += c.w
	}

	// One separator between every pair of columns: n columns, n-1 gaps.
	cmdW := w - fixed - procUnitW - (len(cols)+1)*sepW
	if showUnit := cmdW >= 18; showUnit {
		cols = append(cols, procCol{key: "unit", title: "UNIT", w: procUnitW})
	} else {
		cmdW = w - fixed - len(cols)*sepW
	}
	if cmdW < 12 {
		cmdW = 12
	}
	return append(cols, procCol{key: "command", title: "COMMAND", w: cmdW})
}

// nameColW sizes a table's leading name column: it takes the width left over
// after the value columns, but never more than max.
//
// Without the cap, a lone `w - everythingElse` puts all of a wide terminal's
// slack into one cell — "eth0" rendered in 66 columns. Spreading that slack
// across the value columns instead is no better: right-aligned numbers end up
// marooned at the far edge of cells three times wider than their contents.
// Capping lets the table simply end, leaving a margin, which is what the
// block-devices table has always done and the one that actually reads well.
func nameColW(w, max int, vals []int) int {
	fixed := 0
	for _, v := range vals {
		fixed += v
	}
	n := w - fixed - len(vals)*sepW
	if n > max {
		return max
	}
	return n
}

// colAt maps a click's x position to the column under it. Returns "" when the
// click landed on a separator, the gutter, or past the last column.
func colAt(cols []procCol, x int) string {
	at := 0
	for _, c := range cols {
		if x >= at && x < at+c.w {
			return c.key
		}
		at += c.w + sepW
	}
	return ""
}
