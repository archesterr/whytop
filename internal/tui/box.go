package tui

import "strings"

// Box drawing, written here rather than taken from lipgloss's Border, because
// every line in this UI is assembled from strings that already contain ANSI
// escapes — styled cells, gauges, two-tone command lines. lipgloss's layout
// measures and re-styles what it is given, and re-styling text that already
// carries escapes is what corrupted the colours the last three times it was
// tried. These helpers only ever pad and cut with the ANSI-aware primitives
// the tables already use.

const (
	boxTL, boxTR, boxBL, boxBR = "╭", "╮", "╰", "╯"
	boxH, boxV                 = "─", "│"
	boxVL, boxVR               = "├", "┤"
	boxTDown, boxBUp           = "┬", "┴"
)

// boxInset is how far a boxed line's content sits from the frame's left
// edge: the border, then a space. Mouse hit-testing inside a box subtracts
// it, so the renderer and the click handler can't disagree about where a
// column starts.
const boxInset = 2

// boxTop draws a box's top edge with a title let into it, the way a fieldset
// reads: "╭─ CPU ────╮". A title is a label for what is inside, so it
// belongs on the edge rather than spending a whole row of its own.
func boxTop(w int, title, right string) string {
	if w < 4 {
		return strings.Repeat(boxH, max0(w))
	}
	// Both labels are budgeted against the width rather than trusted to
	// fit. A hostname is arbitrary text from the machine, and a title that
	// overruns doesn't wrap — it pushes the frame's corner off the line and
	// every row below it looks broken.
	//
	// The right-hand label goes first when there isn't room for both: it
	// carries the clock and the live/paused state, which the rest of the
	// screen also shows, where the title carries which machine this is.
	const minFill = 2
	used := 2 + 1 // "╭─" and "╯"
	tail := ""
	if right != "" {
		r := " " + right + " "
		if used+visLen(r)+minFill+4 <= w {
			tail = stBoxTitle.Render(r) + stBox2.Render(boxH)
			used += visLen(r) + 1
		}
	}
	left := stBox2.Render(boxTL + boxH)
	if title != "" {
		budget := max0(w - used - minFill - 2)
		t := truncate(title, budget)
		if t != "" {
			left += stBoxTitle.Render(" " + t + " ")
			used += visLen(t) + 2
		}
	}
	return left + stBox2.Render(strings.Repeat(boxH, max0(w-used))) + tail + stBox2.Render(boxTR)
}

func boxBottom(w int) string {
	if w < 2 {
		return ""
	}
	return stBox2.Render(boxBL + strings.Repeat(boxH, w-2) + boxBR)
}

// boxRule is an interior divider, for separating a box's sections without
// closing and reopening it.
func boxRule(w int) string {
	if w < 2 {
		return ""
	}
	return stBox2.Render(boxVL + strings.Repeat(boxH, w-2) + boxVR)
}

// boxLine wraps one line of already-styled content in the box's sides,
// padding or cutting it to fit exactly. Content wider than the box is cut
// rather than wrapped: a box whose height depends on its contents would
// shift every row below it as the numbers change.
func boxLine(w int, content string) string {
	inner := max0(w - 4)
	body := truncateANSI(content, inner)
	body = pad(body, inner, false)
	return stBox2.Render(boxV) + " " + body + " " + stBox2.Render(boxV)
}

// boxInner is the usable width inside a box of width w.
func boxInner(w int) int { return max0(w - 4) }

// columns lays out several already-styled cells across a width with a
// vertical rule between them, which is what makes a row of unrelated
// numbers read as separate facts rather than as one run of text.
func columns(total int, cells ...string) string {
	if len(cells) == 0 {
		return ""
	}
	sep := " " + stBox2.Render(boxV) + " "
	sepW := visLen(sep)
	each := (total - sepW*(len(cells)-1)) / len(cells)
	if each < 4 {
		each = 4
	}
	out := make([]string, 0, len(cells))
	for i, c := range cells {
		w := each
		if i == len(cells)-1 {
			// The last column takes the rounding slack, so the row ends
			// exactly on the box's edge instead of a column short.
			w = max0(total - (each+sepW)*(len(cells)-1))
		}
		out = append(out, pad(truncateANSI(c, w), w, false))
	}
	return strings.Join(out, sep)
}
