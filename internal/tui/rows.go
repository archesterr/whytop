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
	list := make([]collect.Proc, len(m.snap.Procs))
	copy(list, m.snap.Procs)

	f := strings.ToLower(strings.TrimSpace(m.filter[tabProcs]))
	if f != "" {
		out := list[:0]
		for _, p := range list {
			hay := strconv.Itoa(int(p.PID)) + " " + strings.ToLower(p.Name+" "+p.User+" "+unitName(p.Unit)+" "+p.Cmdline+" "+p.Container+" "+p.Runtime)
			if strconv.Itoa(int(p.PID)) == f || strings.Contains(hay, f) {
				out = append(out, p)
			}
		}
		list = out
	}

	dir := -1
	if m.sortKey == "pid" {
		dir = 1
	}
	less := func(i, j int) bool {
		a, b := list[i], list[j]
		var x, y float64
		switch m.sortKey {
		case "mem":
			x, y = float64(a.RSS), float64(b.RSS)
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
