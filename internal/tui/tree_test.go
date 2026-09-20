package tui

import (
	"strings"
	"testing"

	"github.com/archesterr/whytop/internal/collect"
)

// forest builds an already-tree-ordered list from (depth, pid) pairs, which
// is the shape treeRows is given by procRows.
func forest(pairs ...[2]int) []collect.Proc {
	out := make([]collect.Proc, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, collect.Proc{Depth: p[0], PID: int32(p[1])})
	}
	return out
}

// The guide has to close a branch with ╰ on the last child and carry a
// continuation bar past every ancestor that still has siblings to come.
// Getting the continuation wrong is what makes a deep tree read as a set of
// unrelated indented rows: the bars are the only thing joining a child three
// levels down to the parent it belongs to.
func TestTreeGuidesDrawContinuationBars(t *testing.T) {
	list := forest(
		[2]int{0, 1},  // init
		[2]int{1, 10}, //  ├─ a       (has a sibling below: 40)
		[2]int{2, 20}, //  │  ├─ a1
		[2]int{3, 30}, //  │  │  ╰─ a1x
		[2]int{2, 21}, //  │  ╰─ a2
		[2]int{1, 40}, //  ╰─ b
		[2]int{2, 41}, //     ╰─ b1
	)
	want := []string{
		"",
		"├─",
		"│ ├─",
		"│ │ ╰─",
		"│ ╰─",
		"╰─",
		"  ╰─",
	}
	rows := treeRows(list, -1)
	for i, w := range want {
		if rows[i].guide != w {
			t.Errorf("row %d (pid %d) guide = %q, want %q", i, list[i].PID, rows[i].guide, w)
		}
	}
}

// Kinship is the answer to "which of these PIDs belong to the row I
// selected". A descendant that is not marked, or a row from another branch
// that is, makes the colour actively misleading rather than merely absent.
func TestTreeKinshipMarksTheSelectedSubtreeOnly(t *testing.T) {
	list := forest(
		[2]int{0, 1},  // 0 init          ancestor
		[2]int{1, 10}, // 1  ├─ a         ancestor
		[2]int{2, 20}, // 2  │  ├─ SEL    selected
		[2]int{3, 30}, // 3  │  │  ╰─ x   child
		[2]int{4, 31}, // 4  │  │     ╰─  child (deeper still)
		[2]int{2, 21}, // 5  │  ╰─ a2     none: a sibling, not a descendant
		[2]int{1, 40}, // 6  ╰─ b         none
	)
	rows := treeRows(list, 2)
	want := []kinship{kinParent, kinParent, kinSelf, kinChild, kinChild, kinNone, kinNone}
	for i, w := range want {
		if rows[i].kin != w {
			t.Errorf("row %d (pid %d) kin = %v, want %v", i, list[i].PID, rows[i].kin, w)
		}
	}
}

// With nothing selected every row is unrelated — not, as an off-by-one in
// the ancestor walk would have it, the whole first branch.
func TestTreeKinshipWithNoSelection(t *testing.T) {
	list := forest([2]int{0, 1}, [2]int{1, 10}, [2]int{2, 20})
	for i, r := range treeRows(list, -1) {
		if r.kin != kinNone {
			t.Errorf("row %d is %v with nothing selected, want kinNone", i, r.kin)
		}
	}
}

// A guide is drawn into the COMMAND cell, so it spends columns the command
// itself would otherwise have. The cell must still come out exactly the
// width it was given, or every column to its right shifts.
func TestTreeGuideCellKeepsItsWidth(t *testing.T) {
	long := "/usr/lib/systemd/systemd-journald --some --long --argument --list"
	for _, depth := range []int{0, 1, 4, 20} {
		list := make([][2]int, 0, depth+1)
		for d := 0; d <= depth; d++ {
			list = append(list, [2]int{d, d + 1})
		}
		rows := treeRows(forest(list...), -1)
		guide := rows[len(rows)-1]
		for _, w := range []int{12, 20, 40, 90} {
			for _, sel := range []bool{false, true} {
				if got := visLen(cmdCellAt(long, guide, w, sel)); got != w {
					t.Errorf("depth %d width %d sel=%v: cell is %d columns", depth, w, sel, got)
				}
			}
		}
	}
}

// An indent that grows without bound would eventually be wider than the
// column it is drawn in, leaving no room for the command at all.
func TestTreeGuideStopsIndenting(t *testing.T) {
	list := make([][2]int, 0, 40)
	for d := 0; d < 40; d++ {
		list = append(list, [2]int{d, d + 1})
	}
	rows := treeRows(forest(list...), -1)
	last := rows[len(rows)-1].guide
	if n := len([]rune(last)); n > 2*maxGuideDepth {
		t.Errorf("a 40-deep row indents %d columns, want at most %d", n, 2*maxGuideDepth)
	}
	if !strings.HasSuffix(last, "╰─") && !strings.HasSuffix(last, "├─") {
		t.Errorf("guide %q does not end in a branch glyph", last)
	}
}

// Kernel threads under kthreadd are one enormous flat family. Selecting one
// of them must not mark the next two hundred rows as its descendants, which
// is what a kinship walk that keys on "depth >= selected" instead of
// "> selected" would do.
func TestTreeKinshipSiblingsAreNotDescendants(t *testing.T) {
	list := forest(
		[2]int{0, 2},
		[2]int{1, 3},
		[2]int{1, 4},
		[2]int{1, 5},
	)
	rows := treeRows(list, 2) // pid 4
	if rows[3].kin != kinNone {
		t.Errorf("the sibling below the selected row is %v, want kinNone", rows[3].kin)
	}
	if rows[1].kin != kinNone {
		t.Errorf("the sibling above the selected row is %v, want kinNone", rows[1].kin)
	}
	if rows[0].kin != kinParent {
		t.Errorf("kthreadd is %v, want kinParent", rows[0].kin)
	}
}
