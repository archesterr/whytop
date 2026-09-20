package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/archesterr/whytop/internal/collect"
)

// Tree view's whole purpose is answering "what spawned all of these?", and
// the answer is only useful if you can see where one family ends and the
// next begins. Two things make that readable, and this file is both of them.
//
// The first is the guide: proper branch glyphs with continuation bars, so a
// child three levels down is visibly connected to its parent rather than
// merely indented near it. Indentation alone stops working at exactly the
// point it starts mattering — a container runtime with forty workers under
// three supervisors is a column of spaces you have to count.
//
// The second is kinship colour. On a box where the same process name appears
// thirty times, knowing which of those PIDs belong to the row under the
// cursor is the actual question, and it is one the eye can answer instantly
// if the rows are coloured and impossible if they are not.

type kinship int

const (
	kinNone   kinship = iota
	kinSelf           // the selected row
	kinChild          // somewhere under the selected row
	kinParent         // an ancestor of the selected row
)

// treeRow is the tree-specific part of drawing one process row.
type treeRow struct {
	guide string
	kin   kinship
}

// maxGuideDepth bounds how far the guide indents. Past a dozen levels the
// indent is wider than the command it is indenting, and the useful
// information — that this is deeply nested — is already conveyed.
const maxGuideDepth = 12

// treeRows builds the guide and kinship for every row of an already
// tree-ordered list. It relies on the one property that ordering
// guarantees: a process's descendants are exactly the rows that follow it
// until the depth returns to its own. That makes both answers a single
// forward pass instead of a second PID index that could disagree with the
// list being drawn.
func treeRows(list []collect.Proc, selIdx int) []treeRow {
	out := make([]treeRow, len(list))
	if len(list) == 0 {
		return out
	}

	// last[i]: row i is the final child at its depth, so its branch closes
	// with └ and no continuation bar is drawn beneath it.
	last := make([]bool, len(list))
	for i := range list {
		d := list[i].Depth
		last[i] = true
		for j := i + 1; j < len(list); j++ {
			if list[j].Depth < d {
				break
			}
			if list[j].Depth == d {
				last[i] = false
				break
			}
		}
	}

	var cont []bool // cont[d]: the current row at depth d has siblings still to come
	for i, p := range list {
		d := p.Depth
		if d > 0 {
			var b strings.Builder
			levels := d
			if levels > maxGuideDepth {
				levels = maxGuideDepth
			}
			for l := 1; l < levels; l++ {
				if l < len(cont) && cont[l] {
					b.WriteString("│ ")
				} else {
					b.WriteString("  ")
				}
			}
			if last[i] {
				b.WriteString("╰─")
			} else {
				b.WriteString("├─")
			}
			out[i].guide = b.String()
		}
		for len(cont) <= d {
			cont = append(cont, false)
		}
		cont[d] = !last[i]
	}

	if selIdx < 0 || selIdx >= len(list) {
		return out
	}
	out[selIdx].kin = kinSelf
	selDepth := list[selIdx].Depth
	for i := selIdx + 1; i < len(list) && list[i].Depth > selDepth; i++ {
		out[i].kin = kinChild
	}
	// Ancestors are found by walking back for each shallower depth in turn:
	// the first row above with a smaller depth is the parent, and so on up.
	want := selDepth - 1
	for i := selIdx - 1; i >= 0 && want >= 0; i-- {
		if list[i].Depth == want {
			out[i].kin = kinParent
			want--
		}
	}
	return out
}

// guideStyle colours the branch lines by kinship, which is what turns the
// guide from decoration into the answer: the selected process's own subtree
// is drawn in one colour and everything else recedes.
func (t treeRow) guideStyle() lipgloss.Style {
	switch t.kin {
	case kinChild, kinSelf:
		return stTreeKin
	case kinParent:
		return stTreeUp
	}
	return stTreeLine
}
