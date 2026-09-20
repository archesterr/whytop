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
	if m.help {
		switch msg.String() {
		case "q":
			m.quitting = true
			return m, tea.Quit
		default:
			m.help = false
		}
		return m, nil
	}
	if m.hosts != nil {
		return m.handleHostKey(msg)
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

// The key map follows htop and top wherever they have a name for something,
// because the people who will use this already have those keys in their
// fingers: k kills, K hides kernel threads, P/M/T sort by CPU/memory/time,
// < and > move the sort column, u filters by user, t is the tree, p toggles
// the full program path, h is help. Being nearly-but-not-quite htop is worse
// than being nothing like it — a key that does something *else* is how you
// kill the wrong process.
//
// Only what htop and top have no equivalent for gets a key of whytop's own,
// and those are deliberately keys neither tool binds: @ for hosts, g to jump
// to the problem, L to lock the row order, space to pause.
func (m model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyF1:
		m.help = !m.help
		return m, nil
	case tea.KeyF3:
		m.editing = true
		return m, nil
	case tea.KeyF5:
		return m.toggleTree()
	case tea.KeyF6:
		return m.cycleSort(1)
	case tea.KeyF9:
		return m.killSelected()
	case tea.KeyF10:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyPgUp:
		m.moveSel(-m.listRowsBudget())
		return m, nil
	case tea.KeyPgDown:
		m.moveSel(m.listRowsBudget())
		return m, nil
	case tea.KeyHome:
		m.moveSel(-1 << 30)
		return m, nil
	case tea.KeyEnd:
		m.moveSel(1 << 30)
		return m, nil
	}

	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "h", "?":
		m.help = !m.help

	// --- htop / top ---
	case "k":
		return m.killSelected()
	case "K":
		m.showKernel = !m.showKernel
		if m.showKernel {
			return m.showToast("Showing kernel threads ([kworker/…] and friends). K hides them again.", true)
		}
		return m.showToast("Kernel threads hidden. K shows them again.", true)
	case "P":
		return m.sortBy("cpu")
	case "M":
		return m.sortBy("mem")
	case "T":
		return m.sortBy("time")
	case ">":
		return m.cycleSort(1)
	case "<":
		return m.cycleSort(-1)
	case "I", "R":
		m.sortDir = -m.sortDir
		if m.sortDir == 0 {
			m.sortDir = -defaultSortDir(m.sortKey)
		}
		m.relock()
	case "t":
		return m.toggleTree()
	case "p":
		m.fullPath = !m.fullPath
		if m.fullPath {
			return m.showToast("Showing the full program path. p shows just the program again.", true)
		}
		return m.showToast("Showing the program name without its path. p shows the full path.", true)
	case "u":
		// htop and top both open a filter on the owner here.
		m.editing, m.filterScope, m.filter = true, scopeUser, ""
	case "/":
		m.editing = true
	case "l":
		// htop runs lsof on the selected process; whytop already shows a
		// process's open files, so l opens that panel on them directly.
		mm, cmd := m.openSelected()
		if d := mm.(model).detail; d != nil {
			d.focus = focusFiles
		}
		return mm, cmd

	// --- whytop's own, on keys htop and top leave free ---
	case "@":
		return m.openHosts()
	case "g":
		return m.jumpToFinding()
	case "L":
		m.lockOrder = !m.lockOrder
		m.relock()
		if m.lockOrder {
			return m.showToast("Order locked: rows stay put while their numbers change. L unlocks.", true)
		}
		return m.showToast("Order live again: rows re-sort as usage changes.", true)
	case " ":
		m.paused = !m.paused
		if !m.paused {
			return m, m.collectCmd(0)
		}

	// --- navigation ---
	case "up":
		m.moveSel(-1)
	case "down":
		m.moveSel(1)
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

// sortBy is what P, M and T do in htop and top: jump straight to a column,
// in the direction that column is worth reading.
func (m *model) sortBy(key string) (tea.Model, tea.Cmd) {
	m.sortKey, m.sortDir = key, defaultSortDir(key)
	m.relock()
	return *m, nil
}

// cycleSort steps the sort column, which is what < and > do in both tools.
func (m *model) cycleSort(delta int) (tea.Model, tea.Cmd) {
	i := 0
	for idx, k := range procSortCycle {
		if k == m.sortKey {
			i = idx
		}
	}
	i = (i + delta + len(procSortCycle)) % len(procSortCycle)
	return m.sortBy(procSortCycle[i])
}

func (m *model) toggleTree() (tea.Model, tea.Cmd) {
	m.tree = !m.tree
	m.relock()
	if m.tree {
		return m.showToast("Tree view: processes under the ones that started them. t goes back to a flat list.", true)
	}
	return m.showToast("Flat list. t shows the process tree.", true)
}

// killSelected is htop's k and top's k: signal the process under the cursor.
// It confirms first, and it carries the start time the row was drawn from so
// the signal lands on that process or on nothing — see actions.Signal.
func (m *model) killSelected() (tea.Model, tea.Cmd) {
	if mm, cmd, blocked := m.localOnly("Killing a process"); blocked {
		return mm, cmd
	}
	rows := m.procRows()
	var p collect.Proc
	found := false
	for _, r := range rows {
		if strconv.Itoa(int(r.PID)) == m.sel {
			p, found = r, true
			break
		}
	}
	if !found {
		return m.showToast("No process selected — use ↑↓ first.", false)
	}
	pid, name, started := p.PID, p.Name, p.Started
	m.confirm = &confirmState{
		prompt: fmt.Sprintf("Send SIGTERM to %s (PID %d)? Shift-K force-kills instead. [y/N]", safeText(name), pid),
		run:    func() tea.Cmd { return doSignal(pid, "TERM", started) },
	}
	return *m, nil
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

// localOnly refuses an action that would run on the wrong machine.
//
// Every action whytop can take — signals, systemctl, truncating a
// descriptor — runs through internal/actions, which acts on the machine
// whytop is running on. While a remote host is being viewed, the PID under
// the cursor belongs to that host, and running a kill with it locally would
// signal whatever process happens to hold that number here. That is not a
// missing feature to be papered over with a best effort; it is the single
// most dangerous thing this tool could do, so it is refused by name.
func (m *model) localOnly(what string) (tea.Model, tea.Cmd, bool) {
	if m.remote == nil {
		return *m, nil, false
	}
	mm, cmd := m.showToast(what+" acts on the machine whytop is running on, so it is disabled while viewing "+
		m.hostLabel()+". Press H to come back to localhost.", false)
	return mm, cmd, true
}

func (m model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "x", "X", "r", "e", "t", "c":
		// Guarded together rather than one by one: a new action added
		// below should have to opt *out* of this check, not remember to
		// opt in.
		if mm, cmd, blocked := m.localOnly(actionName(msg.String())); blocked {
			return mm, cmd
		}
	}
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

func actionName(key string) string {
	switch key {
	case "x":
		return "Stopping a process"
	case "X":
		return "Force-killing a process"
	case "r":
		return "Restarting a unit"
	case "e":
		return "Editing a unit file"
	case "t":
		return "Emptying a file"
	default:
		return "Closing a descriptor"
	}
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
