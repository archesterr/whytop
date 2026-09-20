package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// The layout is fixed at the top of the screen, so a click's row says what
// it landed on: row 0 the header, rows 1-3 the three physical lines of the
// vitals block, row 4 the status line, row 5 the rule under it, row 6 the
// column header, rows 7+ the data. renderList's height budget assumes the
// same shape — see View().
const (
	statusRow     = 4
	listHeaderRow = 6
	listFirstRow  = 7
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
		// The UI is centred on a wide terminal, so a click's screen column
		// is offset from the column the renderer laid out.
		return m.handleClick(msg.X-m.padLeft(), msg.Y)
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

	if y == statusRow {
		return m.clickFinding(x)
	}
	if y == listHeaderRow {
		return m.clickHeader(x)
	}
	if y >= listFirstRow {
		return m.clickRow(y - listFirstRow)
	}
	return m, nil
}

// clickHeader sorts by the clicked column, htop-style: a new column sorts the
// way that column is usually read (biggest-first for numbers, A-to-Z for
// text), and clicking the column you're already sorted by reverses it.
func (m *model) clickHeader(x int) (tea.Model, tea.Cmd) {
	key := colAt(m.cols(m.contentW()), x)
	if key == "" {
		return *m, nil
	}
	if key == m.sortKey {
		m.sortDir = -m.sortDir
		if m.sortDir == 0 {
			m.sortDir = -defaultSortDir(key)
		}
	} else {
		m.sortKey, m.sortDir = key, defaultSortDir(key)
	}
	m.relock()
	return *m, nil
}

// clickFinding makes the status line what it looks like: a link. Clicking a
// finding goes to the rows it is about.
func (m *model) clickFinding(x int) (tea.Model, tea.Cmd) {
	regions, _ := statusLayout(m.findings(), m.contentW())
	for _, r := range regions {
		if x >= r.x0 && x < r.x1 {
			return m.applyJump(r.f)
		}
	}
	return *m, nil
}

func (m *model) clickRow(idx int) (tea.Model, tea.Cmd) {
	rows := m.rowKeys()
	if idx < 0 || len(rows) == 0 {
		return *m, nil
	}
	// A click's row index is relative to what's currently drawn, which
	// (like the arrow keys) may be scrolled to follow the selection rather
	// than always starting at row 0 — see windowRows in tables.go.
	selIdx := -1
	for j, r := range rows {
		if r.key == m.sel {
			selIdx = j
			break
		}
	}
	start, end := windowRows(len(rows), selIdx, m.listRowsBudget())
	target := start + idx
	if target < start || target >= end {
		return *m, nil
	}
	m.sel = rows[target].key
	return m.openSelected()
}
