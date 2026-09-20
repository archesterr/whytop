package tui

// procCol is one column of the process table. The renderer, the clickable
// header and the sort comparator are all built from this one list, so a
// column you click can never drift from the column drawn above the rows.
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
	case "cpu", "mem", "read", "write", "io", "net", "rx", "tx", "port":
		return true
	}
	return false
}

// Optional columns in the order they are drawn, each with the order it is
// given up in as the terminal narrows (1 goes first).
//
// Everything here has to earn its width against COMMAND, which is what
// people are usually reading. PORT survives longest of the four: it is the
// reason the Ports tab is gone, and "who has 8080" is a question you cannot
// answer any other way from this screen.
var procOptional = []struct {
	col  procCol
	drop int
}{
	{procCol{key: "port", title: "PORT", w: 7, right: true}, 4},
	{procCol{key: "rx", title: "NET↓", w: 9, right: true}, 3},
	{procCol{key: "tx", title: "NET↑", w: 9, right: true}, 2},
	{procCol{key: "unit", title: "UNIT", w: 16}, 1},
}

// procColumns lays out the process table for a given width.
//
// Every column but COMMAND has a fixed width; COMMAND takes whatever is
// left, which is why the table fills the terminal exactly. When there isn't
// enough left for COMMAND to be readable, optional columns are dropped in
// the order above rather than squeezing it to an unusable sliver.
// netCols says whether the network columns are worth their width: on a host
// where per-process traffic can't be measured at all they would be two
// columns of "?", and the space belongs to COMMAND instead.
func (m model) netCols() bool {
	if m.snap == nil {
		return false
	}
	for _, p := range m.snap.Procs {
		if p.NetKnown {
			return true
		}
	}
	return false
}

// cols is procColumns for this model — the single place that decides which
// columns exist, so the header, the rows and click hit-testing can't
// disagree about it.
func (m model) cols(w int) []procCol {
	return procColumns(w, m.netCols())
}

func procColumns(w int, net bool) []procCol {
	base := []procCol{
		{key: "", title: "", w: gutterW},
		{key: "pid", title: "PID", w: 6, right: true},
		{key: "user", title: "USER", w: 11},
		{key: "state", title: "ST", w: 3},
		{key: "cpu", title: "CPU%", w: 6, right: true},
		{key: "mem", title: "MEM", w: 9, right: true},
		{key: "read", title: "READ", w: 9, right: true},
		{key: "write", title: "WRITE", w: 9, right: true},
	}

	// Try the full set, then give up one optional column at a time until
	// COMMAND has room to be worth reading.
	const minCmdW = 18
	for give := 0; give <= len(procOptional); give++ {
		cols := append([]procCol(nil), base...)
		for _, o := range procOptional {
			if o.drop <= give {
				continue
			}
			if !net && (o.col.key == "rx" || o.col.key == "tx") {
				continue
			}
			cols = append(cols, o.col)
		}
		fixed := 0
		for _, c := range cols {
			fixed += c.w
		}
		cmdW := w - fixed - len(cols)*sepW
		if cmdW >= minCmdW || give == len(procOptional) {
			if cmdW < 12 {
				cmdW = 12
			}
			return append(cols, procCol{key: "command", title: "COMMAND", w: cmdW})
		}
	}
	return nil // unreachable: the loop always returns on its last iteration
}

// nameColW sizes a table's leading name column: it takes the width left over
// after the value columns, but never more than max.
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
