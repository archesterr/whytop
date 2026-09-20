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
	switch msg.Type {
	case tea.KeyEsc:
		m.filter = ""
		m.editing = false
	case tea.KeyTab:
		// Cycling the scope re-labels what is already typed rather than
		// clearing it: you usually discover you wanted the port column
		// *after* typing the number.
		m.filterScope = (m.filterScope + 1) % numScopes
		if scope, rest, ok := scopeFromPrefix(m.filter); ok {
			_ = scope
			m.filter = rest // a typed prefix would override the cycled scope
		}
	case tea.KeyShiftTab:
		m.filterScope = (m.filterScope + numScopes - 1) % numScopes
		if _, rest, ok := scopeFromPrefix(m.filter); ok {
			m.filter = rest
		}
	case tea.KeyEnter:
		m.editing = false
	case tea.KeyBackspace:
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
		}
	case tea.KeyRunes:
		m.filter += string(msg.Runes)
	case tea.KeySpace:
		m.filter += " "
	}
	return m, nil
}

func (m model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "p":
		m.paused = !m.paused
		if !m.paused {
			return m, m.collectCmd(0)
		}
	case "/":
		m.editing = true
	case "s":
		i := 0
		for idx, k := range procSortCycle {
			if k == m.sortKey {
				i = idx
			}
		}
		m.sortKey = procSortCycle[(i+1)%len(procSortCycle)]
		m.sortDir = defaultSortDir(m.sortKey)
		m.relock()
	case "S":
		m.sortDir = -m.sortDir
		if m.sortDir == 0 {
			m.sortDir = -defaultSortDir(m.sortKey)
		}
		m.relock()
	case "L":
		m.lockOrder = !m.lockOrder
		m.relock()
		if m.lockOrder {
			return m.showToast("Order locked: rows stay put while their numbers change. L unlocks.", true)
		}
		return m.showToast("Order live again: rows re-sort as usage changes.", true)
	case "K":
		m.showKernel = !m.showKernel
		if m.showKernel {
			return m.showToast("Showing kernel threads ([kworker/…] and friends). K hides them again.", true)
		}
		return m.showToast("Kernel threads hidden. K shows them again.", true)
	case "up", "k":
		m.moveSel(-1)
	case "down", "j":
		m.moveSel(1)
	case "g":
		return m.jumpToFinding()
	case "esc":
		// A jump leaves a filter behind on purpose, so esc has to be able to
		// take it off again without opening the filter editor first.
		if m.filter != "" {
			m.filter = ""
			return m.showToast("Filter cleared.", true)
		}
	case "enter":
		return m.openSelected()
	}
	return m, nil
}

// rowKeys returns the ordered (key, pid) pairs for the list, built fresh
// from the same filter/sort state the table renders with.
func (m model) rowKeys() []rowRef {
	procs := m.procRows()
	out := make([]rowRef, len(procs))
	for i, p := range procs {
		out[i] = rowRef{key: strconv.Itoa(int(p.PID)), pid: p.PID}
	}
	return out
}

func (m *model) moveSel(delta int) {
	rows := m.rowKeys()
	if len(rows) == 0 {
		return
	}
	idx := 0
	for j, r := range rows {
		if r.key == m.sel {
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
	m.sel = rows[idx].key
}

// openSelected has a pointer receiver so it can mutate m in place (moveSel,
// m.detail), but always returns *m dereferenced — every tea.Model this
// package returns is a model value, never a *model, so callers (including
// tests) never have to care which internal helper produced it.
func (m *model) openSelected() (tea.Model, tea.Cmd) {
	rows := m.rowKeys()
	found := false
	var pid int32
	for _, r := range rows {
		if r.key == m.sel {
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
	m.detail = &detailState{pid: pid, follow: true}
	return *m, tea.Batch(m.loadExtraCmd(pid), m.loadJournalCmd(pid))
}

func (m model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.detail = nil
	case "tab":
		// Two navigable lists in one panel, so tab says which one the arrows
		// drive — and which one c/t act on.
		if m.detail.focus == focusTree {
			m.detail.focus = focusFiles
		} else {
			m.detail.focus = focusTree
		}
	case "up":
		m.detail.moveDetailSel(-1, len(m.currentTree()), len(m.openFiles()))
	case "down":
		m.detail.moveDetailSel(1, len(m.currentTree()), len(m.openFiles()))
	case "enter":
		nodes := m.currentTree()
		if m.detail.treeSel < len(nodes) {
			pid := nodes[m.detail.treeSel].PID
			if pid != m.detail.pid {
				m.detail = &detailState{pid: pid, follow: true}
				return m, tea.Batch(m.loadExtraCmd(pid), m.loadJournalCmd(pid))
			}
		}
	case "j":
		// "j" for journal, not "l" — the arrow keys already cover tree
		// navigation here, so the letter is free for the mnemonic it
		// actually matches instead of an arbitrary one.
		return m, m.loadJournalCmd(m.detail.pid)
	case "f":
		m.detail.follow = !m.detail.follow
		if m.detail.follow {
			return m, m.loadJournalCmd(m.detail.pid)
		}
	case "t":
		f, ok := m.selectedFile()
		if m.detail.focus != focusFiles || !ok {
			break
		}
		pid, fd, target := m.detail.pid, f.FD, f.Target
		m.confirm = &confirmState{
			prompt: fmt.Sprintf("Empty %s (fd %s)? The space comes back and the process keeps running. [y/N]", truncate(target, 48), fd),
			run:    func() tea.Cmd { return doTruncateFD(pid, fd, target) },
		}
	case "c":
		f, ok := m.selectedFile()
		if m.detail.focus != focusFiles || !ok {
			break
		}
		pid, fd, target := m.detail.pid, f.FD, f.Target
		m.confirm = &confirmState{
			danger: true,
			prompt: fmt.Sprintf("Close fd %s (%s)? The process is not told, and may fail or crash next time it uses it. [y/N]", fd, truncate(target, 40)),
			run:    func() tea.Cmd { return doCloseFD(pid, fd) },
		}
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
		pid, name, started := p.PID, p.Name, p.Started
		m.confirm = &confirmState{
			danger: danger,
			prompt: fmt.Sprintf("%s %s (PID %d)? [y/N]", verb, safeText(name), pid),
			run:    func() tea.Cmd { return doSignal(pid, sig, started) },
		}
	case "e":
		p, ok := m.procByPID(m.detail.pid)
		if !ok {
			break
		}
		// Editing a unit file from the process you were looking at is the
		// whole point: you find the process that is misbehaving, and the
		// thing that configures it is one key away rather than in another
		// terminal. Nothing that isn't a systemd process has a file to edit.
		if p.Unit == "" {
			return m.showToast("This process was not started by systemd, so it has no unit file to edit.", false)
		}
		if p.UnitUser {
			return m.showToast(safeText(unitName(p.Unit))+" is a user unit — edit it as its own user with systemctl --user edit.", false)
		}
		return m, m.editUnitCmd(p.Unit)
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
