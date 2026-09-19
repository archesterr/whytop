package tui

import (
	"fmt"
	"sort"
	"unicode/utf8"
)

// finding is one thing that looks wrong right now, phrased the way someone
// who isn't already a kernel engineer would say it.
//
// The vitals row above answers "what are the numbers"; this answers "is
// anything actually wrong", which is the question people open the tool with.
// Knowing that 0.7 load across 4 cores is fine but 20% I/O pressure is an
// emergency is exactly the experience a first-time user doesn't have yet.
type finding struct {
	crit bool
	text string
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (m model) findings() []finding {
	s := m.snap
	if s == nil {
		return nil
	}
	var out []finding
	add := func(crit bool, format string, args ...any) {
		out = append(out, finding{crit: crit, text: fmt.Sprintf(format, args...)})
	}

	// Processes stuck in uninterruptible sleep are the most actionable
	// signal on the whole screen: something is wedged on I/O right now.
	blocked, zombies := 0, 0
	for _, p := range s.Procs {
		switch p.State {
		case "D":
			blocked++
		case "Z":
			zombies++
		}
	}
	if blocked > 0 {
		add(blocked >= 3, "%d %s stuck waiting on disk", blocked, plural(blocked, "process", "processes"))
	}
	if zombies >= 10 {
		add(false, "%d zombie processes", zombies)
	}

	if s.Mem.UsedPct >= 92 {
		add(true, "memory almost full (%s%%)", f1(s.Mem.UsedPct))
	} else if s.Mem.UsedPct >= 80 {
		add(false, "memory filling up (%s%%)", f1(s.Mem.UsedPct))
	}
	if s.Mem.SwapTotal > 0 && s.Mem.SwapPct >= 20 {
		add(s.Mem.SwapPct >= 50, "swapping (%s%% of swap used)", f1(s.Mem.SwapPct))
	}

	if s.CPU.Busy >= 95 {
		add(true, "CPU saturated (%s%%)", f1(s.CPU.Busy))
	} else if s.CPU.Busy >= 85 {
		add(false, "CPU busy (%s%%)", f1(s.CPU.Busy))
	}
	if s.CPU.Iowait >= 10 {
		add(true, "high I/O wait (%s%%)", f1(s.CPU.Iowait))
	} else if s.CPU.Iowait >= 3 {
		add(false, "I/O wait climbing (%s%%)", f1(s.CPU.Iowait))
	}
	if s.CPU.Steal >= 10 {
		add(false, "hypervisor stealing %s%% of CPU", f1(s.CPU.Steal))
	}

	cores := float64(s.CPU.Cores)
	if cores < 1 {
		cores = 1
	}
	if per := s.Load1 / cores; per >= 2 {
		add(true, "load %.2f on %d cores", s.Load1, s.CPU.Cores)
	} else if per >= 1 {
		add(false, "load %.2f on %d cores", s.Load1, s.CPU.Cores)
	}

	if s.PSI.Available {
		if s.PSI.IOSome >= 20 {
			add(true, "disk pressure high (%s%%)", f1(s.PSI.IOSome))
		}
		if s.PSI.MemSome >= 10 {
			add(true, "memory pressure (%s%%)", f1(s.PSI.MemSome))
		}
	}

	for _, d := range s.Disks {
		if d.Util >= 95 {
			add(true, "%s %s%% busy", d.Name, f1(d.Util))
		}
		if d.AwaitMs >= 100 {
			add(true, "%s slow (%sms per I/O)", d.Name, f1(d.AwaitMs))
		}
	}

	for _, fs := range s.FS {
		if fs.Stale {
			add(true, "%s not responding", fs.Mount)
			continue
		}
		if fs.UsedPct >= 95 {
			add(true, "%s almost full (%s%%)", fs.Mount, f1(fs.UsedPct))
		} else if fs.UsedPct >= 85 {
			add(false, "%s filling up (%s%%)", fs.Mount, f1(fs.UsedPct))
		}
		if fs.InodePct >= 90 {
			add(true, "%s out of inodes (%s%%)", fs.Mount, f1(fs.InodePct))
		}
	}

	for _, n := range s.NICs {
		if n.ErrPs > 0 {
			add(false, "%s errors", n.Name)
		}
		if n.DropPs >= 1 {
			add(false, "%s dropping packets", n.Name)
		}
	}
	if s.TCP.Available && s.TCP.RetransPct >= 5 {
		add(false, "TCP retransmits %s%%", f1(s.TCP.RetransPct))
	}

	// Critical first, order within each severity preserved (roughly
	// most-actionable first, by the order they're added above).
	sort.SliceStable(out, func(i, j int) bool { return out[i].crit && !out[j].crit })
	return out
}

// renderStatus draws the one-line verdict under the vitals row. It fits
// whatever width it's given: findings that don't fit collapse into a
// "+N more" count rather than wrapping onto a second line, which would
// break every other row's height budget.
func (m model) renderStatus(w int) string {
	if w < 1 {
		return ""
	}
	found := m.findings()
	if len(found) == 0 {
		// Truncate the plain text and style it after, never the other way
		// round: truncate() counts runes, so cutting already-styled text
		// slices through the escape sequences instead of the words.
		return stOK.Render("✓") + stMuted.Render(truncate(" Nothing obviously wrong right now", w-1))
	}

	markStyle := stWarn
	if found[0].crit {
		markStyle = stCrit
	}
	line := markStyle.Bold(true).Render("⚠")
	if w < 2 {
		return line // the marker alone still says "look closer"
	}
	used, shown := 1, 0
	for i, f := range found {
		style := stWarn
		if f.crit {
			style = stCrit
		}
		cost := 1 + utf8.RuneCountInString(f.text) // leading space or " · " minus one
		if i > 0 {
			cost += 2
		}
		// Never spend the room the "+N more" tail will need: the count of
		// what didn't fit must not itself be the thing that overflows.
		tail := 0
		if rest := len(found) - i; rest > 1 {
			tail = utf8.RuneCountInString(fmt.Sprintf(" +%d more", rest-1))
		}
		if used+cost+tail > w {
			break
		}
		if i > 0 {
			line += stFaint.Render(" · ")
		} else {
			line += " "
		}
		line += style.Render(f.text)
		used += cost
		shown++
	}
	if rest := len(found) - shown; rest > 0 {
		suffix := fmt.Sprintf(" +%d more", rest)
		if used+utf8.RuneCountInString(suffix) <= w {
			line += stFaint.Render(suffix)
		}
	}
	return line
}
