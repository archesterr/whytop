// Package tui is whytop's terminal front end: it drives internal/collect and
// internal/actions directly, in-process — no HTTP layer, no browser, no
// token. Just SSH in and run it.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/actions"
	"github.com/archesterr/whytop/internal/collect"
)

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

// snapMsg is one reading, tagged with the refresh loop that took it.
type snapMsg struct {
	snap *collect.Snapshot
	gen  int64
}

// collectLoop keeps exactly one refresh loop alive.
//
// Each reading schedules the next, so anything that wants a reading *now*
// — a kill that succeeded, a host just connected, unpausing — used to start
// a second chain beside the first, and nothing ever stopped the first.
// Two loops sample milliseconds apart, so every rate is computed over a few
// milliseconds and reads 0 or nonsense; the collection cost doubles with
// every kill; and Collector.Collect, which must not run concurrently, did.
//
// gen names the live loop: starting a new one bumps it, and a tick or a
// reading from an older loop is dropped where it lands. mu makes sure
// that, even so, two readings are never taken at once — a stale loop may
// already be halfway through one when the new loop starts.
//
// It is a pointer because model is copied on every update and all copies
// must agree on which loop is live.
type collectLoop struct {
	mu  sync.Mutex
	gen atomic.Int64
}
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
	col     *collect.Collector
	opt     Options
	snap    *collect.Snapshot
	paused  bool
	width   int
	height  int
	sortKey string
	sortDir int
	filter  string
	// filterScope is the column the filter searches when the query carries
	// no prefix of its own — cycled with Tab while the filter is open.
	filterScope filterScope
	editing     bool
	// showKernel reveals kernel threads in the process list. Off by default:
	// see Proc.Kernel.
	showKernel bool
	sel        string // selected row key
	// top is the first visible row of the process list. It is a position
	// the operator moved to, not a function of the selection, so that a
	// list being read holds still while the cursor moves through it.
	top int
	// selIdx is the row the cursor was last on, by position. m.sel holds
	// its PID, which is the right thing to follow while the process lives
	// and nothing at all once it exits — see reanchorSel.
	selIdx int

	// findingLast is the key of the finding g last jumped to, so the next
	// press lands on the one after it. A key rather than an index, because
	// the findings list is rebuilt from a fresh sample every press — see
	// jumpToFinding.
	findingLast string

	// help shows the key map. tree orders the list as a forest, the way
	// htop's t does. fullPath is htop's p: the whole path, or just the
	// program that is running.
	help     bool
	tree     bool
	fullPath bool

	// mouseOff means the operator has handed the mouse back to the terminal
	// so they can select and copy text. See toggleMouse.
	mouseOff bool

	// loop is the one live refresh loop; see collectLoop.
	loop *collectLoop

	// filterFromJump marks a filter that g or a click on a finding put
	// there, rather than one the operator typed. The two look the same on
	// screen and differ under "/": see handleListKey.
	filterFromJump bool

	// lockOrder freezes the process list's row order. See lockRank.
	lockOrder bool
	// lockRank is the position every PID held when the order was locked, so
	// a row stays where the operator last saw it even as its CPU or memory
	// moves under it. Processes that appear afterwards aren't in the map and
	// sort below the frozen block, in the normal order for the column.
	lockRank map[int32]int

	detail  *detailState
	confirm *confirmState
	// upd is a release worth offering, once the background check has found
	// one. Nil the rest of the time, which is almost always.
	upd *updateState

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
var procSortCycle = []string{"cpu", "mem", "read", "write", "net", "port", "time", "pid", "user", "state", "command"}

func initialModel(opt Options) model {
	return model{
		col: collect.New(), opt: opt,
		sortKey: "cpu", sortDir: -1,
		// htop shows the whole command line by default and p strips the
		// path; whytop matches that rather than inventing its own default.
		fullPath: true,
		deepPID:  opt.PID, deepPort: opt.Port,
		lastOOMCheck: time.Now(),
		loop:         &collectLoop{},
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
	return tea.Batch(m.collectCmd(0), m.pollOOMCmd(0), checkUpdateCmd(m.opt.Version, updateCheckDelay))
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
	col, loop := m.col, m.loop
	gen := m.loopGen()
	return tea.Tick(after, func(time.Time) tea.Msg {
		if loop != nil {
			if loop.gen.Load() != gen {
				return nil // a newer loop has taken over; this one ends here
			}
			loop.mu.Lock()
			defer loop.mu.Unlock()
		}
		return snapMsg{snap: col.Collect(), gen: gen}
	})
}

// refreshNow replaces the refresh loop with one that reads immediately.
// Anything that wants fresh numbers now calls this, never collectCmd(0),
// which would run a second loop beside the one already going.
func (m model) refreshNow() tea.Cmd {
	if m.loop != nil {
		m.loop.gen.Add(1)
	}
	return m.collectCmd(0)
}

func (m model) loopGen() int64 {
	if m.loop == nil {
		return 0
	}
	return m.loop.gen.Load()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Every frame is laid out to the exact width and height, so a
		// resize invalidates all of it. bubbletea's renderer only repaints
		// the lines it believes have changed, and on a window that just got
		// smaller the lines it leaves alone are the ones that were too wide
		// for it — they stay on screen, wrapped, under the new frame. A
		// clear forces the next frame to be painted from nothing.
		m.top = 0
		return m, tea.ClearScreen

	case snapMsg:
		if msg.gen != m.loopGen() {
			// From a loop that has been replaced — possibly one reading a
			// host that has since been left. Its numbers are not this
			// screen's, and scheduling its next tick would revive it.
			return m, nil
		}
		m.snap = msg.snap
		m.reanchorSel()
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
			cmds = append(cmds, m.refreshNow())
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

	case updateFoundMsg:
		// Only over the process list. A prompt that lands on top of a
		// confirm, a filter being typed or an open detail panel interrupts
		// work that was already underway, which is the one thing an
		// unsolicited prompt must never do.
		if m.confirm != nil || m.editing || m.detail != nil || m.help {
			// The same finding again in a minute, not another check. A
			// fresh check would be refused by its own rate limit — it has
			// just recorded that it ran — so re-checking here means the
			// prompt is dropped for twelve hours every time the screen
			// happened to be busy when it arrived.
			return m, retryUpdatePromptCmd(msg, updateRetryDelay)
		}
		m.upd = &updateState{rel: msg.rel, install: msg.install}
		return m, nil

	case updateDoneMsg:
		m.upd = nil
		if msg.err != nil {
			return m, tea.Batch(func() tea.Msg {
				return actionMsg{ok: false, text: "Update failed: " + msg.err.Error()}
			})
		}
		_, cmd := m.showToastFor("Updated to "+msg.tag+" — restart whytop to run it", true, oomToastTTL)
		return m, cmd

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
		m.filter = "port:" + strconv.Itoa(port)
		m.resetScroll()
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

// doSignal carries the start time the operator's row was drawn from, so the
// signal lands on that process or on nothing — see actions.Signal.
func doSignal(pid int32, sig string, started time.Time) tea.Cmd {
	return func() tea.Msg {
		s, ok := map[string]syscall.Signal{"TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL}[sig]
		if !ok {
			return actionMsg{ok: false, text: "unknown signal " + sig}
		}
		if err := actions.Signal(pid, s, started); err != nil {
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
