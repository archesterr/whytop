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
	tabUnits
)

const numTabs = 5

func (t tab) String() string {
	return [...]string{"Processes", "Ports", "Disks", "Network", "Units"}[t]
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

type detailFocus int

const (
	focusTree detailFocus = iota
	focusFiles
)

type detailState struct {
	pid            int32
	extra          collect.Extra
	unitStatus     map[string]string
	restartBlocked string
	loaded         bool
	treeSel        int
	// focus decides which of the panel's two lists the arrow keys drive, and
	// which one the descriptor actions apply to.
	focus   detailFocus
	fileSel int
	journal string
	// follow re-reads the journal on every refresh tick, which is what
	// `journalctl -u <unit> -f` gives you at a shell. It's on by default:
	// you open a process's panel to watch what it's doing, and a log that
	// silently stopped updating is worse than no log at all.
	follow bool
}

const toastTTL = 3 * time.Second
const oomToastTTL = 12 * time.Second // an OOM kill matters more than a routine action result

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
type oomPollMsg struct {
	kills []actions.OOMKill
	at    time.Time
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
	// showKernel reveals kernel threads in the process list. Off by default:
	// see Proc.Kernel.
	showKernel bool
	sel        [2]string // selected row key, indexed by tabProcs/tabPorts
	unitSel    string    // selected unit name, for the Units tab

	detail  *detailState
	confirm *confirmState

	toast      string
	toastStyle func(...string) string
	toastGen   int

	deepPID  int
	deepPort int

	lastOOMCheck time.Time

	quitting bool
}

type rowRef struct {
	key string
	pid int32
}

// procSortCycle is what the s key steps through — the same columns the
// header exposes to a click, in the order you'd reach for them.
var procSortCycle = []string{"cpu", "mem", "read", "write", "pid", "user", "state", "command"}

func initialModel(opt Options) model {
	return model{
		col: collect.New(), opt: opt,
		sortKey: "cpu", sortDir: -1,
		deepPID: opt.PID, deepPort: opt.Port,
		lastOOMCheck: time.Now(),
	}
}

// Run starts the terminal UI and blocks until the user quits.
func Run(ctx context.Context, opt Options) error {
	m := initialModel(opt)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.collectCmd(0), m.pollOOMCmd(0))
}

const oomPollInterval = 5 * time.Second

// pollOOMCmd checks the kernel log for OOM-kill events since the last poll.
// top, htop and iotop don't surface this at all — finding out a process was
// killed for memory pressure means separately digging through dmesg or
// journalctl -k after the fact.
func (m model) pollOOMCmd(after time.Duration) tea.Cmd {
	since := m.lastOOMCheck
	return tea.Tick(after, func(time.Time) tea.Msg {
		return oomPollMsg{kills: actions.RecentOOMKills(since), at: time.Now()}
	})
}

func (m model) collectCmd(after time.Duration) tea.Cmd {
	col := m.col
	wantConns := m.tab == tabPorts || m.detail != nil || m.deepPort > 0
	wantUnits := m.tab == tabUnits
	return tea.Tick(after, func(time.Time) tea.Msg {
		col.WantConns.Store(wantConns)
		col.WantUnits.Store(wantUnits)
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
			if m.detail.follow {
				cmds = append(cmds, m.loadJournalCmd(m.detail.pid))
			}
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

	case oomPollMsg:
		m.lastOOMCheck = msg.at
		next := m.pollOOMCmd(oomPollInterval)
		if len(msg.kills) == 0 {
			return m, next
		}
		k := msg.kills[len(msg.kills)-1]
		text := fmt.Sprintf("⚠ OOM killer killed %s (PID %d)", k.Name, k.PID)
		if len(msg.kills) > 1 {
			text = fmt.Sprintf("⚠ OOM killer killed %d processes, most recently %s (PID %d)", len(msg.kills), k.Name, k.PID)
		}
		_, toastCmd := m.showToastFor(text, false, oomToastTTL)
		return m, tea.Batch(toastCmd, next)

	case actionMsg:
		_, toastCmd := m.showToast(msg.text, msg.ok)
		cmds := []tea.Cmd{toastCmd}
		if msg.ok {
			cmds = append(cmds, m.collectCmd(0))
		}
		if msg.openPID > 0 {
			m.detail = &detailState{pid: msg.openPID, follow: true}
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

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case startEditMsg:
		return m, tea.ExecProcess(msg.cmd, func(err error) tea.Msg {
			return editDoneMsg{err: err, unit: msg.unit}
		})

	case editDoneMsg:
		if msg.err != nil {
			_, cmd := m.showToast("Editor exited with an error: "+msg.err.Error(), false)
			return m, cmd
		}
		m.confirm = &confirmState{
			prompt: fmt.Sprintf("Reload the systemd daemon to apply changes to %s? [y/N]", msg.unit),
			run:    func() tea.Cmd { return doDaemonReload() },
		}
		return m, nil
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
		m.detail = &detailState{pid: pid, follow: true}
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
				m.detail = &detailState{pid: c.PID, follow: true}
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
