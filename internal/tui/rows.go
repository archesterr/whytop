package tui

import (
	"sort"
	"strconv"
	"strings"

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

	f := strings.ToLower(strings.TrimSpace(m.filter[tabProcs]))
	if f != "" {
		// "state:D" matches the state column exactly rather than as free
		// text, which is what lets the status line narrow the list to the
		// processes a finding is actually about — searching for "d" would
		// match half the command lines on the box.
		if want, ok := strings.CutPrefix(f, "state:"); ok {
			out := list[:0]
			for _, p := range list {
				if strings.EqualFold(p.State, want) {
					out = append(out, p)
				}
			}
			return sortProcs(out, m.sortKey, m.sortDir)
		}
		out := list[:0]
		for _, p := range list {
			hay := strconv.Itoa(int(p.PID)) + " " + strings.ToLower(p.Name+" "+p.User+" "+unitName(p.Unit)+" "+p.Cmdline+" "+p.Container+" "+p.Runtime)
			if strconv.Itoa(int(p.PID)) == f || strings.Contains(hay, f) {
				out = append(out, p)
			}
		}
		list = out
	}

	return sortProcs(list, m.sortKey, m.sortDir)
}

func sortProcs(list []collect.Proc, sortKey string, dir int) []collect.Proc {
	if dir == 0 {
		dir = defaultSortDir(sortKey)
	}
	less := func(i, j int) bool {
		a, b := list[i], list[j]

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

// portRows returns the filtered, sorted socket list for the Ports tab.
func (m model) portRows() []collect.Conn {
	if m.snap == nil || !m.snap.ConnsCollected {
		return nil
	}
	var list []collect.Conn
	for _, c := range m.snap.Conns {
		if m.allConns || c.Listening() {
			list = append(list, c)
		}
	}
	f := strings.ToLower(strings.TrimSpace(m.filter[tabPorts]))
	isNum := f != "" && strings.IndexFunc(f, func(r rune) bool { return r < '0' || r > '9' }) == -1
	if f != "" {
		out := list[:0]
		for _, c := range list {
			if isNum {
				if strconv.Itoa(int(c.LPort)) == f || strconv.Itoa(int(c.PID)) == f || strings.HasSuffix(c.Remote, ":"+f) {
					out = append(out, c)
				}
				continue
			}
			name, unit := "", ""
			if p, ok := m.procByPID(c.PID); ok {
				name, unit = p.Name, unitName(p.Unit)
			}
			hay := strings.ToLower(c.Proto + " " + c.LocalIP + " " + c.State + " " + c.Remote + " " + name + " " + unit)
			if strings.Contains(hay, f) {
				out = append(out, c)
			}
		}
		list = out
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].LPort != list[j].LPort {
			return list[i].LPort < list[j].LPort
		}
		if list[i].Proto != list[j].Proto {
			return list[i].Proto < list[j].Proto
		}
		return list[i].PID < list[j].PID
	})
	return list
}

func connKey(c collect.Conn) string {
	return c.Proto + "|" + c.LocalIP + "|" + strconv.Itoa(int(c.LPort)) + "|" + c.Remote + "|" + strconv.Itoa(int(c.PID))
}
