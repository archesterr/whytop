package tui

import (
	"sort"
	"strconv"

	"github.com/archesterr/whytop/internal/collect"
)

func cmdOf(p collect.Proc) string {
	if p.Cmdline != "" {
		return p.Cmdline
	}
	return "[" + p.Name + "]"
}

// procRows returns the filtered, sorted process list for the Processes tab,
// in the same order the table renders (and the order Up/Down navigates).
func (m model) procRows() []collect.Proc {
	if m.snap == nil {
		return nil
	}
	list := make([]collect.Proc, 0, len(m.snap.Procs))
	for _, p := range m.snap.Procs {
		// Kernel threads are ~90% of the PIDs on an idle box and never the
		// thing being troubleshot, so they stay out of the way until asked
		// for. Without this the first screen is a wall of [kworker/…] at 0%.
		if !m.showKernel && p.Kernel() {
			continue
		}
		list = append(list, p)
	}

	if scope, q := m.filterQuery(); q != "" {
		out := list[:0]
		for _, p := range list {
			if scope.matches(p, q) {
				out = append(out, p)
			}
		}
		list = out
	}

	if m.tree {
		return treeOrder(list, m.sortKey, m.sortDir)
	}
	return m.order(list)
}

// treeOrder arranges the list as a forest: every process under the one that
// started it, each level sorted the way the flat list would be. This is
// htop's t, and it answers a question the flat list cannot — "what spawned
// all of these?" — which on a box full of identical worker processes is
// usually the only question worth asking.
//
// A process whose parent was filtered out is a root here. That keeps a
// filtered tree honest: showing an ancestor that does not match the filter
// would be inventing a row the operator did not ask for.
func treeOrder(list []collect.Proc, sortKey string, dir int) []collect.Proc {
	present := make(map[int32]bool, len(list))
	for _, p := range list {
		present[p.PID] = true
	}
	kids := map[int32][]collect.Proc{}
	var roots []collect.Proc
	for _, p := range list {
		if p.PPID > 0 && p.PPID != p.PID && present[p.PPID] {
			kids[p.PPID] = append(kids[p.PPID], p)
			continue
		}
		roots = append(roots, p)
	}
	sortProcs(roots, sortKey, dir)
	for pid := range kids {
		sortProcs(kids[pid], sortKey, dir)
	}

	out := make([]collect.Proc, 0, len(list))
	seen := make(map[int32]bool, len(list))
	var walk func(p collect.Proc, depth int)
	walk = func(p collect.Proc, depth int) {
		// A /proc read is not atomic, so a parent cycle is possible in
		// principle; without this guard it would be an infinite loop
		// rather than a wrong row.
		if seen[p.PID] {
			return
		}
		seen[p.PID] = true
		p.Depth = depth
		out = append(out, p)
		for _, c := range kids[p.PID] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	// Anything a cycle left unreachable is shown flat rather than dropped.
	// Two processes each claiming the other as parent have no root between
	// them, and a tree view that silently loses rows is worse than one that
	// draws an odd shape — the list is what people count processes in.
	if len(out) < len(list) {
		for _, p := range list {
			if !seen[p.PID] {
				walk(p, 0)
			}
		}
	}
	return out
}

// order applies the current sort — or, when the order is locked, replays the
// order the rows were in when it was locked.
//
// Sorting by CPU is the right default and the reason the list is unreadable
// the moment you try to act on it: the row you are reaching for moves out
// from under the cursor every refresh, because that is exactly what a busy
// process does. Locking freezes the arrangement without freezing the numbers
// — unlike pause (p), which stops collecting altogether and leaves you
// reading figures that are no longer true.
func (m model) order(list []collect.Proc) []collect.Proc {
	if !m.lockOrder || m.lockRank == nil {
		return sortProcs(list, m.sortKey, m.sortDir)
	}
	// Processes that started after the lock have no frozen position. They
	// sort among themselves in the normal order for the column and sit below
	// the frozen block, where they can be seen arriving instead of being
	// silently inserted into the middle of a list someone is reading.
	frozen := make([]collect.Proc, 0, len(list))
	fresh := make([]collect.Proc, 0, 8)
	for _, p := range list {
		if _, ok := m.lockRank[p.PID]; ok {
			frozen = append(frozen, p)
		} else {
			fresh = append(fresh, p)
		}
	}
	sort.SliceStable(frozen, func(i, j int) bool {
		return m.lockRank[frozen[i].PID] < m.lockRank[frozen[j].PID]
	})
	return append(frozen, sortProcs(fresh, m.sortKey, m.sortDir)...)
}

// relock re-captures the frozen order from what is on screen right now. It
// runs when the lock is turned on, and again whenever the operator changes
// the sort while locked — otherwise picking a new column would do nothing
// visible, and the lock would read as broken rather than as locked.
func (m *model) relock() {
	if !m.lockOrder {
		m.lockRank = nil
		return
	}
	m.lockRank = nil // so procRows below sorts live rather than replaying the old lock
	rows := m.procRows()
	rank := make(map[int32]int, len(rows))
	for i, p := range rows {
		rank[p.PID] = i
	}
	m.lockRank = rank
}

// hasPort reports whether the process listens on the given port. The port is
// matched as a whole number, so 80 does not match 8080.
func hasPort(p collect.Proc, want string) bool {
	n, err := strconv.ParseUint(want, 10, 32)
	if err != nil {
		return false
	}
	for _, port := range p.Ports {
		if uint64(port) == n {
			return true
		}
	}
	return false
}

func sortProcs(list []collect.Proc, sortKey string, dir int) []collect.Proc {
	if dir == 0 {
		dir = defaultSortDir(sortKey)
	}
	less := func(i, j int) bool {
		a, b := list[i], list[j]

		// Sorting by I/O ranks processes blocked on it above processes
		// merely doing a lot of it. A wedged process reports almost no
		// bytes per second — being stuck is precisely why — so by measured
		// throughput it sorts to the bottom, which is the opposite of what
		// someone sorting by I/O is looking for. This is what the Disks
		// tab's own process table used to do before there was one list.
		if sortKey == "io" || sortKey == "read" || sortKey == "write" {
			if ad, bd := a.State == "D", b.State == "D"; ad != bd {
				return ad
			}
		}

		// Sorting by port keeps every process that listens on something
		// above every process that doesn't, whichever direction the column
		// is sorted in. Reversing the order should reverse the ports, not
		// bury them under a screen of blank cells.
		if sortKey == "port" {
			al, bl := len(a.Ports) > 0, len(b.Ports) > 0
			if al != bl {
				return al
			}
			if !al {
				return a.PID < b.PID
			}
		}

		// Text columns compare as text; the rest compare as numbers. Sorting
		// USER or COMMAND numerically would be meaningless, and sorting MEM
		// alphabetically would put "9 KiB" above "80 GiB".
		if sx, sy, isText := sortText(sortKey, a, b); isText {
			if sx != sy {
				if dir < 0 {
					return sx > sy
				}
				return sx < sy
			}
			return a.PID < b.PID
		}

		var x, y float64
		switch sortKey {
		case "mem":
			x, y = float64(a.RSS), float64(b.RSS)
		case "read":
			x, y = a.ReadBps, b.ReadBps
		case "write":
			x, y = a.WriteBps, b.WriteBps
		case "io":
			x, y = a.ReadBps+a.WriteBps, b.ReadBps+b.WriteBps
		case "rx":
			x, y = a.NetRxBps, b.NetRxBps
		case "tx":
			x, y = a.NetTxBps, b.NetTxBps
		case "net":
			x, y = a.NetRxBps+a.NetTxBps, b.NetRxBps+b.NetTxBps
		case "port":
			x, y = portKey(a), portKey(b)
		case "time":
			// T in htop and top sorts by how long a process has been
			// running, so the oldest — and by default the newest — are
			// together rather than scattered.
			x, y = float64(a.Started.UnixNano()), float64(b.Started.UnixNano())
		case "pid":
			x, y = float64(a.PID), float64(b.PID)
		default: // cpu
			x, y = a.CPU, b.CPU
		}
		if x != y {
			if dir < 0 {
				return x > y
			}
			return x < y
		}
		return a.PID < b.PID
	}
	sort.SliceStable(list, less)
	return list
}

// portKey is a process's lowest listening port. Processes that listen on
// nothing never reach here — sortProcs separates them out first.
func portKey(p collect.Proc) float64 {
	if len(p.Ports) == 0 {
		return 0
	}
	return float64(p.Ports[0])
}

// sortText returns the two values to compare for a text column, and whether
// the key names a text column at all.
func sortText(key string, a, b collect.Proc) (x, y string, ok bool) {
	switch key {
	case "user":
		return a.User, b.User, true
	case "state":
		return a.State, b.State, true
	case "unit":
		return unitName(a.Unit), unitName(b.Unit), true
	case "command":
		return cmdOf(a), cmdOf(b), true
	}
	return "", "", false
}

// defaultSortDir picks the direction a column should sort the first time you
// pick it: biggest-first for the "who is using all the X" columns, A-to-Z for
// the rest.
func defaultSortDir(key string) int {
	if numericSort(key) {
		return -1
	}
	return 1
}
