package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Where a click lands is derived from the same arithmetic the renderer
// uses — see listHeaderRow, listFirstRow and statusRow in view.go. They
// used to be constants, which was fine until the header panel changed
// height and every click silently addressed the row above the one under
// the pointer.

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.confirm != nil {
		return m, nil // never let a stray click confirm a destructive action
	}
	// Once the mouse has been handed to the terminal, whytop ignores mouse
	// events outright rather than trusting the terminal to have stopped
	// sending them. Nothing guarantees it did: DECSET 1002/1003 can be
	// swallowed by a multiplexer reporting on its own account, and a click
	// that sorted a column out from under a drag the operator thinks is a
	// selection would be indistinguishable from a bug. The state whytop
	// shows in the footer is the state whytop honours.
	if m.mouseOff {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonRight:
		// Right-click is what people press when they want to copy, so that
		// is what it does: it stops whytop asking for mouse events, which
		// is the only thing standing between them and the terminal's own
		// selection and copy. Only the press — a button reports twice, and
		// releasing after the handover must not toggle it straight back.
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		return m.releaseMouse()
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

	if y == m.statusRow() {
		return m.clickFinding(x - boxInset)
	}
	if y == m.listHeaderRow() {
		return m.clickHeader(x - boxInset)
	}
	if y >= m.listFirstRow() {
		return m.clickRow(y - m.listFirstRow())
	}
	return m, nil
}

// clickHeader sorts by the clicked column, htop-style: a new column sorts the
// way that column is usually read (biggest-first for numbers, A-to-Z for
// text), and clicking the column you're already sorted by reverses it.
func (m *model) clickHeader(x int) (tea.Model, tea.Cmd) {
	key := colAt(m.cols(boxInner(m.contentW())), x)
	if key == "" {
		return *m, nil
	}
	m.resetScroll()
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
	regions, _ := statusLayout(m.findings(), boxInner(m.contentW()))
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
	start, end := m.window(len(rows), selIdx, m.listRowsBudget())
	target := start + idx
	if target < start || target >= end {
		return *m, nil
	}
	// A click selects; clicking the row that is already selected opens it.
	// Opening on the first click is how the tree view became unusable with
	// a mouse: selecting a process is what highlights its descendants, and
	// if that same click throws a panel over the list you can never see the
	// thing selecting it was for. It is also what every file manager does,
	// and what Enter already does from the keyboard.
	if m.sel == rows[target].key {
		return m.openSelected()
	}
	m.sel, m.selIdx = rows[target].key, target
	return *m, nil
}
