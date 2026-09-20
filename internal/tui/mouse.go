package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// Row 4 (0-indexed) is always the tab bar: row 0 header, rows 1-2 the two
// physical lines of the vitals cards, row 3 the status line, row 4 tabs,
// row 5 the rule line under them — see View()'s layout, which renderTab's
// `avail := h-8` budget also assumes. Rows 6+ are the current tab's own
// header + data rows.
const (
	statusRow     = 3
	tabBarRow     = 4
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
	if m.tab == tabUnits {
		m.moveUnitSel(delta)
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
	if y == tabBarRow {
		return m.clickTab(x)
	}
	if y == listHeaderRow && m.tab == tabProcs {
		return m.clickHeader(x)
	}
	if (m.tab == tabProcs || m.tab == tabPorts) && y >= listFirstRow {
		return m.clickRow(y - listFirstRow)
	}
	if m.tab == tabUnits && y >= listFirstRow {
		return m.clickUnitRow(y - listFirstRow)
	}
	return m, nil
}

// clickHeader sorts by the clicked column, htop-style: a new column sorts the
// way that column is usually read (biggest-first for numbers, A-to-Z for
// text), and clicking the column you're already sorted by reverses it.
func (m model) clickHeader(x int) (tea.Model, tea.Cmd) {
	key := colAt(procColumns(m.contentW()), x)
	if key == "" {
		return m, nil
	}
	if key == m.sortKey {
		m.sortDir = -m.sortDir
		if m.sortDir == 0 {
			m.sortDir = -defaultSortDir(key)
		}
	} else {
		m.sortKey, m.sortDir = key, defaultSortDir(key)
	}
	return m, nil
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
	if idx < 0 || len(rows) == 0 {
		return *m, nil
	}
	// A click's row index is relative to what's currently drawn, which
	// (like the arrow keys) may be scrolled to follow the selection rather
	// than always starting at row 0 — see windowRows in tables.go.
	i := int(m.tab)
	selIdx := -1
	for j, r := range rows {
		if r.key == m.sel[i] {
			selIdx = j
			break
		}
	}
	start, end := windowRows(len(rows), selIdx, m.tabRowsBudget())
	target := start + idx
	if target < start || target >= end {
		return *m, nil
	}
	m.sel[i] = rows[target].key
	return m.openSelected()
}

func (m *model) clickUnitRow(idx int) (tea.Model, tea.Cmd) {
	if m.snap == nil || idx < 0 {
		return *m, nil
	}
	units := m.snap.Units
	selIdx := -1
	for j, u := range units {
		if u.Name == m.unitSel {
			selIdx = j
			break
		}
	}
	start, end := windowRows(len(units), selIdx, m.tabRowsBudget())
	target := start + idx
	if target < start || target >= end {
		return *m, nil
	}
	m.unitSel = units[target].Name
	return *m, nil
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
	for _, t := range []tab{tabProcs, tabPorts, tabDisks, tabNet, tabUnits} {
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
