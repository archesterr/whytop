package tui

import (
	"fmt"
	"sort"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
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
	// to is where this finding lives. A line that tells you a process is
	// wedged on disk and then can't take you to it is only half an answer —
	// the operator still has to work out which tab, which sort, and which of
	// two hundred rows. Every finding carries its own destination so the
	// answer is one keystroke or one click away.
	to jump
}

// jump is what a finding is about, expressed as the filter and sort that
// narrow the process list down to the rows in question.
//
// There is one list now, so every finding lands in it. That is a better
// answer than the tabs gave, not a worse one: "high I/O wait" used to open a
// tab of block devices and leave you to work out which process was behind
// them, and now it puts the heaviest writers on top.
type jump struct {
	filter  string
	sortKey string
	sortDir int
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
	// Device and mount names reach this line straight from the system, so
	// the finished sentence is scrubbed like any other untrusted text.
	add := func(crit bool, to jump, format string, args ...any) {
		out = append(out, finding{crit: crit, to: to, text: safeText(fmt.Sprintf(format, args...))})
	}
	// The destinations. Blocked and zombie processes filter the list down to
	// exactly those, which is what makes the answer readable when there are
	// eight of them rather than one.
	//
	// Only those two filter on live state. I/O wait and PSI are averages
	// over a window, so by the time you press g nothing may be in D at all —
	// filtering on it would strand you on an empty list. Those sort instead,
	// which always has something to show: the processes doing the most of
	// whatever the finding is about.
	var (
		toBlocked = jump{filter: "state:D", sortKey: "state", sortDir: 1}
		toZombies = jump{filter: "state:Z", sortKey: "state", sortDir: 1}
		toMem     = jump{sortKey: "mem", sortDir: -1}
		toCPU     = jump{sortKey: "cpu", sortDir: -1}
		toIO      = jump{sortKey: "io", sortDir: -1}
		toNet     = jump{sortKey: "net", sortDir: -1}
	)

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
		add(blocked >= 3, toBlocked, "%d %s stuck waiting on disk", blocked, plural(blocked, "process", "processes"))
	}
	if zombies >= 10 {
		add(false, toZombies, "%d zombie processes", zombies)
	}

	if s.Mem.UsedPct >= 92 {
		add(true, toMem, "memory almost full (%s%%)", f1(s.Mem.UsedPct))
	} else if s.Mem.UsedPct >= 80 {
		add(false, toMem, "memory filling up (%s%%)", f1(s.Mem.UsedPct))
	}
	if s.Mem.SwapTotal > 0 && s.Mem.SwapPct >= 20 {
		add(s.Mem.SwapPct >= 50, toMem, "swapping (%s%% of swap used)", f1(s.Mem.SwapPct))
	}

	if s.CPU.Busy >= 95 {
		add(true, toCPU, "CPU saturated (%s%%)", f1(s.CPU.Busy))
	} else if s.CPU.Busy >= 85 {
		add(false, toCPU, "CPU busy (%s%%)", f1(s.CPU.Busy))
	}
	if s.CPU.Iowait >= 10 {
		add(true, toIO, "high I/O wait (%s%%)", f1(s.CPU.Iowait))
	} else if s.CPU.Iowait >= 3 {
		add(false, toIO, "I/O wait climbing (%s%%)", f1(s.CPU.Iowait))
	}
	if s.CPU.Steal >= 10 {
		add(false, toCPU, "hypervisor stealing %s%% of CPU", f1(s.CPU.Steal))
	}

	cores := float64(s.CPU.Cores)
	if cores < 1 {
		cores = 1
	}
	if per := s.Load1 / cores; per >= 2 {
		add(true, toCPU, "load %.2f on %d cores", s.Load1, s.CPU.Cores)
	} else if per >= 1 {
		add(false, toCPU, "load %.2f on %d cores", s.Load1, s.CPU.Cores)
	}

	if s.PSI.Available {
		if s.PSI.IOSome >= 20 {
			add(true, toIO, "disk pressure high (%s%%)", f1(s.PSI.IOSome))
		}
		if s.PSI.MemSome >= 10 {
			add(true, toMem, "memory pressure (%s%%)", f1(s.PSI.MemSome))
		}
	}

	for _, d := range s.Disks {
		if d.Util >= 95 {
			add(true, toIO, "%s %s%% busy", d.Name, f1(d.Util))
		}
		if d.AwaitMs >= 100 {
			add(true, toIO, "%s slow (%sms per I/O)", d.Name, f1(d.AwaitMs))
		}
	}

	for _, fs := range s.FS {
		if fs.Stale {
			add(true, toIO, "%s not responding", fs.Mount)
			continue
		}
		if fs.UsedPct >= 95 {
			add(true, toIO, "%s almost full (%s%%)", fs.Mount, f1(fs.UsedPct))
		} else if fs.UsedPct >= 85 {
			add(false, toIO, "%s filling up (%s%%)", fs.Mount, f1(fs.UsedPct))
		}
		if fs.InodePct >= 90 {
			add(true, toIO, "%s out of inodes (%s%%)", fs.Mount, f1(fs.InodePct))
		}
	}

	for _, n := range s.NICs {
		if n.ErrPs > 0 {
			add(false, toNet, "%s errors", n.Name)
		}
		if n.DropPs >= 1 {
			add(false, toNet, "%s dropping packets", n.Name)
		}
	}
	if s.TCP.Available && s.TCP.RetransPct >= 5 {
		add(false, toNet, "TCP retransmits %s%%", f1(s.TCP.RetransPct))
	}

	// Critical first, order within each severity preserved (roughly
	// most-actionable first, by the order they're added above).
	sort.SliceStable(out, func(i, j int) bool { return out[i].crit && !out[j].crit })
	return out
}

// statusRegion is one finding as drawn, with the columns it occupies. The
// renderer and the click handler are both built from this list so a click can
// never land on a different finding than the one under the pointer.
type statusRegion struct {
	f finding
	// label is what is drawn, which is f.text except on a terminal too
	// narrow to hold even one finding — see statusLayout.
	label  string
	x0, x1 int
}

// statusLayout decides which findings fit in w columns and where each one
// sits. Whatever doesn't fit becomes the "+N more" count — still reachable
// from the keyboard, which is why g cycles through all of them rather than
// only the drawn ones.
func statusLayout(found []finding, w int) (regions []statusRegion, hidden int) {
	if len(found) == 0 || w < 2 {
		return nil, len(found)
	}
	used := 1 // the ⚠ marker
	for i, f := range found {
		lead := 1 // the space after the marker
		if i > 0 {
			lead = 3 // " · "
		}
		text := utf8.RuneCountInString(f.text)
		// Never spend the room the "+N more" tail will need: the count of
		// what didn't fit must not itself be the thing that overflows.
		tail := 0
		if rest := len(found) - i; rest > 1 {
			tail = utf8.RuneCountInString(fmt.Sprintf(" +%d more", rest-1))
		}
		if used+lead+text+tail > w {
			break
		}
		regions = append(regions, statusRegion{f: f, label: f.text, x0: used + lead, x1: used + lead + text})
		used += lead + text
	}
	// Nothing fit. Rather than a bare "⚠ +1 more" — a count of the things
	// it is more than, with none of them shown — draw as much of the first
	// finding as there is room for. On a 40-column terminal what is wrong
	// with the machine is the whole point of the line; how many other
	// things are also wrong is not.
	if len(regions) == 0 {
		room := w - 2 // the marker and the space after it
		if room >= 8 {
			label := truncate(found[0].text, room)
			regions = append(regions, statusRegion{
				f: found[0], label: label,
				x0: 2, x1: 2 + utf8.RuneCountInString(label),
			})
		}
	}
	return regions, len(found) - len(regions)
}

// renderStatus draws the one-line verdict under the vitals row. It fits
// whatever width it's given: findings that don't fit collapse into a
// "+N more" count rather than wrapping onto a second line, which would
// break every other row's height budget.
func (m model) renderStatus(w int) string {
	return renderStatusFor(m.findings(), w)
}

// renderStatusFor is renderStatus with the findings already in hand, so the
// line can be laid out against a given set of them directly.
func renderStatusFor(found []finding, w int) string {
	if w < 1 {
		return ""
	}
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

	regions, hidden := statusLayout(found, w)
	used := 1
	for i, r := range regions {
		if i > 0 {
			line += stFaint.Render(" · ")
			used += 3
		} else {
			line += " "
			used++
		}
		style := stWarn
		if r.f.crit {
			style = stCrit
		}
		// Underlined because it is a link: this text goes somewhere, and
		// nothing else on the screen would tell you that.
		line += style.Underline(true).Render(r.label)
		used += utf8.RuneCountInString(r.label)
	}
	if hidden > 0 {
		suffix := fmt.Sprintf(" +%d more", hidden)
		if used+utf8.RuneCountInString(suffix) <= w {
			line += stFaint.Render(suffix)
		}
	}
	return line
}

// applyJump moves to wherever a finding lives and says what it did, because
// a screen that silently rearranges itself is worse than one that doesn't
// move at all.
func (m *model) applyJump(f finding) (tea.Model, tea.Cmd) {
	t := f.to
	m.detail = nil
	m.editing = false
	m.filter = t.filter
	m.filterFromJump = t.filter != ""
	m.resetScroll()
	// A filter for blocked processes is useless if the kernel threads
	// they're waiting behind are hidden — D state is exactly where a
	// kernel thread is worth seeing.
	if t.filter != "" {
		m.showKernel = true
	}
	if t.sortKey != "" {
		m.sortKey, m.sortDir = t.sortKey, t.sortDir
		m.relock() // an explicit re-sort re-freezes a locked order
	}
	m.sel = "" // let the first matching row take the cursor
	m.moveSel(0)
	hint := "  (esc clears the filter)"
	if t.filter == "" {
		hint = "  (sorted by what the finding is about)"
	}
	return m.showToast("Showing: "+f.text+hint, true)
}

// jumpToFinding steps through the findings one press at a time. With several
// problems at once the operator wants to walk them, not be dropped on the
// worst one over and over.
func (m *model) jumpToFinding() (tea.Model, tea.Cmd) {
	found := m.findings()
	if len(found) == 0 {
		return m.showToast("Nothing wrong to jump to.", true)
	}
	if m.findingSel >= len(found) {
		m.findingSel = 0
	}
	f := found[m.findingSel]
	m.findingSel = (m.findingSel + 1) % len(found)
	return m.applyJump(f)
}
