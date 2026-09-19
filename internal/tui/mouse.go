package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// Row 3 (0-indexed) is always the tab bar: row 0 header, rows 1-2 the two
// physical lines of the vitals cards, row 3 tabs — see View()'s layout,
// which renderTab's `avail := h-6` budget also assumes. Rows 4+ are the
// current tab's own header + data rows.
const (
	tabBarRow     = 3
	listHeaderRow = 4
	listFirstRow  = 5
)

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.confirm != nil {
		return m, nil // never let a stray click confirm a destructive action
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.wheelMove(-1)
	case tea.MouseButtonWheelDown:
		return m.wheelMove(1)
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		return m.handleClick(msg.X, msg.Y)
	}
	return m, nil
}

func (m model) wheelMove(delta int) (tea.Model, tea.Cmd) {
	if m.detail != nil {
		if delta < 0 && m.detail.treeSel > 0 {
			m.detail.treeSel--
		} else if delta > 0 {
			if nodes := m.currentTree(); m.detail.treeSel < len(nodes)-1 {
				m.detail.treeSel++
			}
		}
		return m, nil
	}
	m.moveSel(delta)
	return m, nil
}

func (m model) handleClick(x, y int) (tea.Model, tea.Cmd) {
	if m.detail != nil {
		// The detail view's layout shifts with its content (facts count,
		// container line, restart status), so row-accurate hit-testing
		// there would need to duplicate renderDetail's layout math and
		// drift out of sync with it. Esc/q close it; that's clearly listed
		// in the footer already, so clicks inside it are safely a no-op
		// rather than guessing.
		return m, nil
	}
	if m.editing {
		m.editing = false
	}

	if y == tabBarRow {
		return m.clickTab(x)
	}
	if (m.tab == tabProcs || m.tab == tabPorts) && y >= listFirstRow {
		return m.clickRow(y - listFirstRow)
	}
	return m, nil
}

func (m model) clickTab(x int) (tea.Model, tea.Cmd) {
	for _, r := range tabRegions(m.tabCounts()) {
		if x >= r.x0 && x < r.x1 {
			m.tab = r.t
			return m, nil
		}
	}
	return m, nil
}

func (m *model) clickRow(idx int) (tea.Model, tea.Cmd) {
	rows := m.rowKeys()
	if idx < 0 || idx >= len(rows) {
		return *m, nil
	}
	m.sel[int(m.tab)] = rows[idx].key
	return m.openSelected()
}

// tabRegion is a tab's horizontal span in the rendered tab bar, in terminal
// columns, plus the exact text renderTabs draws for it. Both rendering and
// click hit-testing are built from this single source so they can't drift
// out of sync with each other.
type tabRegion struct {
	t         tab
	x0, x1    int
	base      string // e.g. " 1 Processes "
	countText string // e.g. "42 ", or "" when not yet known
}

func tabRegions(counts map[tab]int) []tabRegion {
	var regions []tabRegion
	x := 0
	for _, t := range []tab{tabProcs, tabPorts, tabDisks, tabNet} {
		base := fmt.Sprintf(" %d %s ", int(t)+1, t.String())
		countText := ""
		if n, ok := counts[t]; ok && n >= 0 {
			countText = fmt.Sprintf("%d ", n)
		}
		width := len(base) + len(countText)
		regions = append(regions, tabRegion{t: t, x0: x, x1: x + width, base: base, countText: countText})
		x += width + 1 // the space joining tabs
	}
	return regions
}
