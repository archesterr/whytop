package tui

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/collect"
)

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm != nil {
		return m.handleConfirmKey(msg)
	}
	if m.editing {
		return m.handleEditKey(msg)
	}
	if msg.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}
	if m.detail != nil {
		return m.handleDetailKey(msg)
	}
	return m.handleListKey(msg)
}

func (m model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.confirm
	switch msg.String() {
	case "y", "Y", "enter":
		m.confirm = nil
		return m, c.run()
	case "n", "N", "esc":
		m.confirm = nil
	}
	return m, nil
}

func (m model) handleEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	i := int(m.tab)
	switch msg.Type {
	case tea.KeyEsc:
		m.filter[i] = ""
		m.editing = false
	case tea.KeyEnter:
		m.editing = false
	case tea.KeyBackspace:
		if s := m.filter[i]; s != "" {
			m.filter[i] = s[:len(s)-1]
		}
	case tea.KeyRunes:
		m.filter[i] += string(msg.Runes)
	case tea.KeySpace:
		m.filter[i] += " "
	}
	return m, nil
}

func (m model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "1":
		m.tab = tabProcs
	case "2":
		m.tab = tabPorts
	case "3":
		m.tab = tabDisks
	case "4":
		m.tab = tabNet
	case "p":
		m.paused = !m.paused
		if !m.paused {
			return m, m.collectCmd(0)
		}
	case "/":
		if m.tab == tabProcs || m.tab == tabPorts {
			m.editing = true
		}
	case "s":
		if m.tab == tabProcs {
			i := 0
			for idx, k := range procSortCycle {
				if k == m.sortKey {
					i = idx
				}
			}
			m.sortKey = procSortCycle[(i+1)%len(procSortCycle)]
		}
	case "a":
		if m.tab == tabPorts {
			m.allConns = !m.allConns
		}
	case "up", "k":
		m.moveSel(-1)
	case "down", "j":
		m.moveSel(1)
	case "enter":
		return m.openSelected()
	}
	return m, nil
}

// rowKeys returns the ordered (key, pid) pairs for the current tab's list,
// built fresh from the same filter/sort state the table renders with.
func (m model) rowKeys() []rowRef {
	switch m.tab {
	case tabProcs:
		procs := m.procRows()
		out := make([]rowRef, len(procs))
		for i, p := range procs {
			out[i] = rowRef{key: strconv.Itoa(int(p.PID)), pid: p.PID}
		}
		return out
	case tabPorts:
		conns := m.portRows()
		out := make([]rowRef, len(conns))
		for i, c := range conns {
			out[i] = rowRef{key: connKey(c), pid: c.PID}
		}
		return out
	default:
		return nil
	}
}

func (m *model) moveSel(delta int) {
	i := int(m.tab)
	if i > 1 {
		return
	}
	rows := m.rowKeys()
	if len(rows) == 0 {
		return
	}
	idx := 0
	for j, r := range rows {
		if r.key == m.sel[i] {
			idx = j
			break
		}
	}
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(rows) {
		idx = len(rows) - 1
	}
	m.sel[i] = rows[idx].key
}

// openSelected has a pointer receiver so it can mutate m in place (moveSel,
// m.detail), but always returns *m dereferenced — every tea.Model this
// package returns is a model value, never a *model, so callers (including
// tests) never have to care which internal helper produced it.
func (m *model) openSelected() (tea.Model, tea.Cmd) {
	i := int(m.tab)
	if i > 1 {
		return *m, nil
	}
	rows := m.rowKeys()
	found := false
	var pid int32
	for _, r := range rows {
		if r.key == m.sel[i] {
			pid, found = r.pid, true
			break
		}
	}
	if !found {
		// No row selected yet (fresh tab, cleared filter, ...): select
		// the first row instead of guessing what Enter should open.
		m.moveSel(0)
		return *m, nil
	}
	if pid <= 0 {
		return m.showToast("The owner of this socket is hidden. Run whytop with sudo.", false)
	}
	m.detail = &detailState{pid: pid}
	return *m, tea.Batch(m.loadExtraCmd(pid), m.loadJournalCmd(pid))
}

func (m model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.detail = nil
	case "up", "k":
		if m.detail.treeSel > 0 {
			m.detail.treeSel--
		}
	case "down", "j":
		nodes := m.currentTree()
		if m.detail.treeSel < len(nodes)-1 {
			m.detail.treeSel++
		}
	case "enter":
		nodes := m.currentTree()
		if m.detail.treeSel < len(nodes) {
			pid := nodes[m.detail.treeSel].PID
			if pid != m.detail.pid {
				m.detail = &detailState{pid: pid}
				return m, tea.Batch(m.loadExtraCmd(pid), m.loadJournalCmd(pid))
			}
		}
	case "l":
		return m, m.loadJournalCmd(m.detail.pid)
	case "x", "X":
		p, ok := m.procByPID(m.detail.pid)
		if !ok {
			break
		}
		sig, verb := "TERM", "Stop"
		danger := false
		if msg.String() == "X" {
			sig, verb, danger = "KILL", "Force kill", true
		}
		pid := p.PID
		name := p.Name
		m.confirm = &confirmState{
			danger: danger,
			prompt: fmt.Sprintf("%s %s (PID %d)? [y/N]", verb, name, pid),
			run:    func() tea.Cmd { return doSignal(pid, sig) },
		}
	case "r":
		p, ok := m.procByPID(m.detail.pid)
		if !ok {
			break
		}
		if !m.detail.loaded {
			// Still waiting on the async CanRestart check — say so instead
			// of either silently doing nothing or offering a confirm we
			// might have to reject after the fact.
			return m.showToast("Still checking whether this process can be restarted…", false)
		}
		if m.detail.restartBlocked != "" {
			return m.showToast(m.detail.restartBlocked, false)
		}
		unit := p.Unit
		pid := p.PID
		m.confirm = &confirmState{
			prompt: fmt.Sprintf("Restart %s? Every process in the unit stops and starts again. [y/N]", unitName(unit)),
			run:    func() tea.Cmd { return doRestart(unit, pid) },
		}
	}
	return m, nil
}

// showToast sets a toast message with an auto-clear timer, for feedback that
// doesn't come from an async action (e.g. an immediate validation failure).
func (m *model) showToast(text string, ok bool) (tea.Model, tea.Cmd) {
	return m.showToastFor(text, ok, toastTTL)
}

func (m *model) showToastFor(text string, ok bool, ttl time.Duration) (tea.Model, tea.Cmd) {
	m.toast = text
	if ok {
		m.toastStyle = stToastOK.Render
	} else {
		m.toastStyle = stToastErr.Render
	}
	m.toastGen++
	gen := m.toastGen
	return *m, tea.Tick(ttl, func(time.Time) tea.Msg { return clearToastMsg{gen: gen} })
}

// currentTree returns the process (with its descendants) rooted at the
// process currently shown in the drawer, in the same depth-first order the
// tree table renders.
func (m model) currentTree() []collect.Proc {
	if m.snap == nil || m.detail == nil {
		return nil
	}
	root, ok := m.procByPID(m.detail.pid)
	if !ok {
		return nil
	}
	kids := map[int32][]collect.Proc{}
	for _, p := range m.snap.Procs {
		if p.PID == p.PPID {
			continue
		}
		kids[p.PPID] = append(kids[p.PPID], p)
	}
	for pid := range kids {
		sort.Slice(kids[pid], func(i, j int) bool { return kids[pid][i].PID < kids[pid][j].PID })
	}
	var out []collect.Proc
	var walk func(p collect.Proc)
	walk = func(p collect.Proc) {
		if len(out) >= 2000 {
			return
		}
		out = append(out, p)
		for _, c := range kids[p.PID] {
			walk(c)
		}
	}
	walk(root)
	return out
}
