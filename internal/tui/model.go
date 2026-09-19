// Package tui is whytop's terminal front end: it drives internal/collect and
// internal/actions directly, in-process — no HTTP layer, no browser, no
// token. Just SSH in and run it.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/actions"
	"github.com/archesterr/whytop/internal/collect"
)

type tab int

const (
	tabProcs tab = iota
	tabPorts
	tabDisks
	tabNet
)

func (t tab) String() string {
	return [...]string{"Processes", "Ports", "Disks", "Network"}[t]
}

// Options configures a Run.
type Options struct {
	Interval time.Duration
	Version  string
	PID      int
	Port     int
}

type confirmState struct {
	prompt string
	danger bool
	run    func() tea.Cmd
}

type detailState struct {
	pid            int32
	extra          collect.Extra
	unitStatus     map[string]string
	restartBlocked string
	loaded         bool
	treeSel        int
	journal        string
}

const toastTTL = 3 * time.Second

type snapMsg *collect.Snapshot
type extraMsg struct {
	pid            int32
	extra          collect.Extra
	unitStatus     map[string]string
	restartBlocked string
}
type actionMsg struct {
	ok      bool
	text    string
	openPID int32 // >0: open this PID's detail after a restart resolves a new main PID
}

// clearToastMsg carries the generation it was scheduled for, so an older
// toast's timer can't blank out a newer toast that replaced it before the
// old timer fired.
type clearToastMsg struct{ gen int }
type journalMsg struct {
	pid  int32
	text string
}

type model struct {
	col      *collect.Collector
	opt      Options
	snap     *collect.Snapshot
	paused   bool
	width    int
	height   int
	tab      tab
	sortKey  string
	sortDir  int
	filter   [2]string // indexed by tabProcs/tabPorts
	editing  bool
	allConns bool
	sel      [2]string // selected row key, indexed by tabProcs/tabPorts

	detail  *detailState
	confirm *confirmState

	toast      string
	toastStyle func(...string) string
	toastGen   int

	deepPID  int
	deepPort int

	quitting bool
}

type rowRef struct {
	key string
	pid int32
}

var procSortCycle = []string{"cpu", "mem", "io", "pid"}

func initialModel(opt Options) model {
	return model{
		col: collect.New(), opt: opt,
		sortKey: "cpu", sortDir: -1,
		deepPID: opt.PID, deepPort: opt.Port,
	}
}

// Run starts the terminal UI and blocks until the user quits.
func Run(ctx context.Context, opt Options) error {
	m := initialModel(opt)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return m.collectCmd(0)
}

func (m model) collectCmd(after time.Duration) tea.Cmd {
	col := m.col
	wantConns := m.tab == tabPorts || m.detail != nil || m.deepPort > 0
	return tea.Tick(after, func(time.Time) tea.Msg {
		col.WantConns.Store(wantConns)
		return snapMsg(col.Collect())
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case snapMsg:
		m.snap = msg
		var cmds []tea.Cmd
		if dc := m.resolveDeepLink(); dc != nil {
			cmds = append(cmds, dc)
		}
		if m.detail != nil {
			cmds = append(cmds, m.loadExtraCmd(m.detail.pid))
		}
		wait := m.opt.Interval
		if wait <= 0 {
			wait = 2 * time.Second
		}
		if m.paused {
			return m, tea.Batch(cmds...)
		}
		cmds = append(cmds, m.collectCmd(wait))
		return m, tea.Batch(cmds...)

	case journalMsg:
		if m.detail != nil && m.detail.pid == msg.pid {
			m.detail.journal = msg.text
		}
		return m, nil

	case extraMsg:
		if m.detail != nil && m.detail.pid == msg.pid {
			m.detail.extra = msg.extra
			m.detail.unitStatus = msg.unitStatus
			m.detail.restartBlocked = msg.restartBlocked
			m.detail.loaded = true
		}
		return m, nil

	case actionMsg:
		_, toastCmd := m.showToast(msg.text, msg.ok)
		cmds := []tea.Cmd{toastCmd}
		if msg.ok {
			cmds = append(cmds, m.collectCmd(0))
		}
		if msg.openPID > 0 {
			m.detail = &detailState{pid: msg.openPID}
			cmds = append(cmds, m.loadExtraCmd(msg.openPID), m.loadJournalCmd(msg.openPID))
		}
		return m, tea.Batch(cmds...)

	case clearToastMsg:
		if msg.gen == m.toastGen {
			m.toast = ""
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// resolveDeepLink honors -pid/-port on the first snapshot that can satisfy it.
func (m *model) resolveDeepLink() tea.Cmd {
	switch {
	case m.deepPID > 0:
		pid := int32(m.deepPID)
		m.deepPID = 0
		if _, ok := m.procByPID(pid); !ok {
			_, cmd := m.showToast(fmt.Sprintf("No process with PID %d", pid), false)
			return cmd
		}
		m.detail = &detailState{pid: pid}
		return tea.Batch(m.loadExtraCmd(pid), m.loadJournalCmd(pid))

	case m.deepPort > 0:
		port := m.deepPort
		m.deepPort = 0
		if m.snap == nil || !m.snap.ConnsCollected {
			m.deepPort = port // try again once sockets are collected
			return nil
		}
		for _, c := range m.snap.Conns {
			if int(c.LPort) == port && c.Listening() && c.PID > 0 {
				m.detail = &detailState{pid: c.PID}
				return tea.Batch(m.loadExtraCmd(c.PID), m.loadJournalCmd(c.PID))
			}
		}
		m.tab = tabPorts
		m.filter[tabPorts] = strconv.Itoa(port)
		_, cmd := m.showToast(fmt.Sprintf("Nothing visible is listening on port %d", port), false)
		return cmd
	}
	return nil
}

func (m model) loadExtraCmd(pid int32) tea.Cmd {
	return func() tea.Msg {
		extra := collect.ProcExtra(pid)
		p, ok := m.procByPID(pid)
		out := extraMsg{pid: pid, extra: extra}
		if ok {
			if err := actions.CanRestart(p.Unit, p.UnitUser); err != nil {
				out.restartBlocked = err.Error()
			}
			if p.Unit != "" && !p.UnitUser && len(p.Unit) > 8 && p.Unit[len(p.Unit)-8:] == ".service" {
				out.unitStatus = actions.UnitStatus(p.Unit)
			}
		}
		return out
	}
}

func (m model) loadJournalCmd(pid int32) tea.Cmd {
	p, _ := m.procByPID(pid)
	unit, user := p.Unit, p.UnitUser
	return func() tea.Msg {
		return journalMsg{pid: pid, text: actions.Journal(unit, user, pid, 100)}
	}
}

func (m model) procByPID(pid int32) (collect.Proc, bool) {
	if m.snap == nil {
		return collect.Proc{}, false
	}
	if i, ok := m.snap.ByPID[pid]; ok {
		return m.snap.Procs[i], true
	}
	return collect.Proc{}, false
}

func doSignal(pid int32, sig string) tea.Cmd {
	return func() tea.Msg {
		s, ok := map[string]syscall.Signal{"TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL}[sig]
		if !ok {
			return actionMsg{ok: false, text: "unknown signal " + sig}
		}
		if err := actions.Signal(pid, s); err != nil {
			return actionMsg{ok: false, text: err.Error()}
		}
		return actionMsg{ok: true, text: fmt.Sprintf("%s sent to PID %d", sig, pid)}
	}
}

func doRestart(unit string, pid int32) tea.Cmd {
	return func() tea.Msg {
		if err := actions.RestartUnit(unit); err != nil {
			return actionMsg{ok: false, text: err.Error()}
		}
		st := actions.UnitStatus(unit)
		var mainPID int32
		fmt.Sscanf(st["MainPID"], "%d", &mainPID)
		if mainPID <= 0 {
			mainPID = pid
		}
		return actionMsg{ok: true, text: "Restarted " + unit, openPID: mainPID}
	}
}
