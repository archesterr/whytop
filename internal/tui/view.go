package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// contentW is the width everything is rendered at: the whole terminal.
//
// This used to be capped at 132 columns and centred, on the reasoning that
// past that width a table's slack all lands in one column. That reasoning
// was right about the tables and wrong about the screen: on a wide terminal
// the result is a window with dead margins down both sides while htop beside
// it uses every column. The slack does land in one column — COMMAND — and
// that is the correct place for it, because a truncated command line is the
// single most common reason to want a wider terminal in the first place.
//
// padLeft survives as the hook mouse hit-testing subtracts, so a future
// offset can be introduced in one place rather than in every click handler.
func (m model) contentW() int {
	w := m.width
	if w <= 0 {
		w = 100
	}
	return w
}

func (m model) padLeft() int { return 0 }

func (m model) View() string {
	if m.quitting {
		return ""
	}
	w := m.contentW()
	h := m.height
	if h <= 0 {
		h = 30
	}
	if m.snap == nil {
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, stMuted.Render("Collecting the first sample…"))
	}

	var b strings.Builder
	b.WriteString(m.renderHeaderPanel(w))
	b.WriteString("\n")

	switch {
	case m.help:
		b.WriteString(m.boxed(w, h, "KEYS", "", m.renderHelp(boxInner(w))))
	case m.hosts != nil:
		b.WriteString(m.boxed(w, h, "HOSTS", m.hosts.configPath, m.renderHosts(boxInner(w), h)))
	case m.detail != nil:
		b.WriteString(m.boxed(w, h, "PROCESS", "esc closes", m.renderDetail(boxInner(w), h)))
	default:
		b.WriteString(m.renderList(w, h))
	}
	b.WriteString("\n")

	// Always emit the same total line count across frames. Two consecutive
	// live-refresh frames can legitimately differ in line count (the
	// process list shrinks by one row, say), and without this, bubbletea's
	// screen diff can leave a stale line from the taller previous frame
	// sitting under the new, shorter one — the same visual glitch as the
	// footer/table collision bug, but between ticks instead of within one
	// frame.
	//
	// The target is h-1, not h: writing content into every single row of
	// the terminal, including the very last one, means the next line feed
	// has nowhere to go but to scroll the whole alt-screen buffer up by
	// one row — which shifts every future frame's content up out of place.
	// Leaving the last row untouched is the margin that avoids it.
	target := h - 1
	if target < 1 {
		target = 1
	}
	// The footer is pinned to the last row rather than left floating right
	// under the content: a key bar sitting mid-screen with blank rows beneath
	// it reads as an unfinished layout. Anchored, short tabs look deliberate.
	lines := strings.Split(b.String(), "\n")
	if body := target - 1; len(lines) < body {
		lines = append(lines, make([]string, body-len(lines))...)
	} else if len(lines) > body {
		lines = lines[:body]
	}
	lines = append(lines, m.renderFooter(w))
	if pad := m.padLeft(); pad > 0 {
		margin := strings.Repeat(" ", pad)
		for i, l := range lines {
			lines[i] = margin + l
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) hostBadge() string {
	if m.remote == nil {
		return ""
	}
	return stRemote.Render(" ssh ") + " "
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func max3(a, b, c float64) float64 {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}

// listRowsBudget is the number of data rows renderProcs actually draws
// (column-header line already subtracted) — mouse click hit-testing needs
// the exact same number to translate a screen row back into a list index,
// since both are scrolled to follow the selection (windowRows).
func (m model) listRowsBudget() int {
	return m.listBudget(m.height) - 1
}

// listHeaderRow is the screen row the column header lands on, and
// listFirstRow the first data row: the panel, then the list's top border.
// Click hit-testing derives both from the same arithmetic the renderer
// uses, so growing the panel can never send clicks to the wrong row.
func (m model) listHeaderRow() int { return m.headerHeight() + 1 }
func (m model) listFirstRow() int  { return m.listHeaderRow() + 1 }

// statusRow is the verdict line inside the panel — the second-to-last row
// of it, above the bottom border.
func (m model) statusRow() int { return m.headerHeight() - 2 }

// renderList frames the process table. The frame is what makes a dense
// table read as one object instead of as loose rows of text, and it is
// where the count of what is being shown belongs — on the edge, next to
// the thing it counts.
func (m model) renderList(w, h int) string {
	rows := m.listBudget(h)
	title := "PROCESSES"
	right := fmt.Sprintf("%d of %d", len(m.procRows()), len(m.snap.Procs))
	body := strings.Split(m.renderProcs(boxInner(w), rows), "\n")
	// The table draws its column header, its rows and a scroll notice, and
	// on a terminal short enough that those come to more than the frame
	// can hold, the overflow would push the frame's own bottom border off
	// the screen and leave the box open.
	if len(body) > rows {
		body = body[:rows]
	}
	out := []string{boxTop(w, title, right)}
	for _, line := range body {
		out = append(out, boxLine(w, line))
	}
	// A box whose height follows its contents would move the footer every
	// time a process started, so the frame is always the same height.
	for i := len(body); i < rows; i++ {
		out = append(out, boxLine(w, ""))
	}
	out = append(out, boxBottom(w))
	return strings.Join(out, "\n")
}

// boxed frames a panel that is not the process list, so every full-screen
// view in whytop has the same outline — and the same height. A six-line
// panel that closed six lines down left the rest of the terminal empty with
// the footer stranded at the bottom of it, which reads as a half-drawn
// screen rather than as a panel.
func (m model) boxed(w, h int, title, right, content string) string {
	rows := m.listBudget(h)
	body := strings.Split(content, "\n")
	// Content taller than the frame is cut with a notice rather than left
	// to run past it. A panel that overflows doesn't merely lose its last
	// lines — it pushes its own bottom border off the screen, and the box
	// is then left open with the footer sitting inside it.
	if len(body) > rows {
		body = append(body[:max0(rows-1)],
			stFaint.Render("… more than fits — a taller terminal shows the rest"))
	}
	out := []string{boxTop(w, title, right)}
	for _, line := range body {
		out = append(out, boxLine(w, line))
	}
	for i := len(body); i < rows; i++ {
		out = append(out, boxLine(w, ""))
	}
	out = append(out, boxBottom(w))
	return strings.Join(out, "\n")
}

// listBudget is how many lines the table gets: everything except the fixed
// chrome above and below it. It is written once here and derived everywhere
// else, because a click's row index and the number of rows drawn have to
// come from the same arithmetic or they disagree by one and every click
// lands on the wrong row.
func (m model) listBudget(h int) int {
	// The panel above, the list's own two borders, the footer — and the
	// very last row of the terminal, which View deliberately leaves empty
	// (see the target arithmetic there). Forgetting that last row is not a
	// harmless rounding error: it made every frame one line too tall, and
	// the line that got cut was the bottom border, so every panel in the
	// program rendered with its frame left open at the bottom.
	chrome := m.headerHeight() + 2 + 1 + 1
	if avail := h - chrome; avail > 1 {
		return avail
	}
	// A terminal this short cannot show both, and the header has already
	// given up everything it can (see headerBodyCap). One row of list is
	// what is left; anything more would push the frame off the screen.
	return 1
}

func (m model) renderFooter(w int) string {
	var keys [][2]string
	switch {
	case m.upd != nil:
		return m.updatePrompt(w)
	case m.confirm != nil:
		style := stConfirm
		if m.confirm.danger {
			style = stDanger.Reverse(true).Padding(0, 1)
		}
		return style.Render(m.confirm.prompt)
	case m.toast != "":
		return m.toastStyle(m.toast)
	case m.editing:
		// The hint names what the current scope searches, because "port"
		// and "command" narrow in very different ways and the difference is
		// only obvious once it has already surprised you.
		scope, _ := m.filterQuery()
		keys = [][2]string{{"tab", "scope: " + scope.hint()}, {"enter", "apply"}, {"esc", "clear"}}
	case m.hosts != nil:
		if m.hosts.adding {
			// One input line does two jobs, told apart by a leading "@":
			// a host to connect to, or the ssh config to read hosts from.
			// The body labels which; the footer has to agree, or enter
			// promises to connect to a file path.
			action := "connect"
			if strings.HasPrefix(m.hosts.input, "@") {
				action = "use this config"
			}
			keys = [][2]string{{"enter", action}, {"esc", "cancel"}}
			break
		}
		keys = [][2]string{{"↑↓", "select"}, {"enter", "connect"}, {"a", "add host"},
			{"c", "ssh config"}, {"r", "reload"}, {"esc", "close"}}

	case m.detail != nil:
		// A process that has exited keeps its panel open — you asked to see
		// it, and being thrown back to the list the instant it died would
		// take the answer away with it. But none of the actions can do
		// anything to a PID that is gone, and they silently no-op: the same
		// rule as the remote host below applies, so the footer stops
		// offering them and says what happened instead.
		if _, alive := m.procByPID(m.detail.pid); !alive {
			keys = [][2]string{{"", "this process has exited"}, {"esc", "close"}}
			break
		}
		// The panel has two lists; the hints name whichever one has the
		// cursor, so the keys on offer are the ones that will actually fire.
		if m.detail.focus == focusFiles && m.remote == nil {
			keys = [][2]string{{"↑↓", "files"}, {"tab", "tree"}, {"t", "empty file"}, {"c", "close fd"}}
		} else {
			keys = [][2]string{{"↑↓", "tree"}, {"tab", "files"}, {"enter", "open"}}
		}
		// Actions act on the machine whytop runs on, so while a remote host
		// is being viewed they are not offered at all. A footer that lists
		// a key which then refuses is a footer people stop reading.
		if m.remote == nil {
			keys = append(keys, [2]string{"x", "stop"}, [2]string{"X", "kill"})
			if p, ok := m.procByPID(m.detail.pid); ok && p.Unit != "" && !p.UnitUser {
				keys = append(keys, [2]string{"e", "edit unit"})
			}
		}
		if m.remote == nil && (!m.detail.loaded || m.detail.restartBlocked == "") {
			keys = append(keys, [2]string{"r", "restart"})
		}
		follow := "f live-log: on"
		if !m.detail.follow {
			follow = "f live-log: off"
		}
		keys = append(keys, [2]string{"j", "journal"},
			[2]string{follow[:1], follow[2:]}, [2]string{"esc", "close"})
	case m.help:
		keys = [][2]string{{"h", "close help"}, {"q", "quit"}}

	default:
		keys = [][2]string{{"↑↓", "select"}, {"enter", "open"}, {"/", "search"}, {"k", "kill"}, {"t", "tree"}}
		// An active filter is the single most important thing to know about
		// what is on screen: every row you are not seeing is hidden by it,
		// and a list that silently shows a subset is how people end up
		// concluding a process is gone.
		if scope, q := m.filterQuery(); q != "" {
			keys = append([][2]string{{scope.String() + " " + truncate(q, 16), "esc clears"}}, keys...)
		}
		// Only offered when there's something to jump to — a key that does
		// nothing on a healthy box is a key people learn to ignore.
		if len(m.findings()) > 0 {
			keys = append([][2]string{{"g", "go to problem"}}, keys...)
		}
		// Not "sort: cpu" — the arrow in the column header already says
		// which column, and says it where you're looking.
		// The lock hint names the state it is in, not the state it would
		// switch to: an operator glancing down needs to know whether the
		// rows under them are moving, more than what L does next.
		lock := "order: live"
		if m.lockOrder {
			lock = "order: LOCKED"
		}
		keys = append(keys, [2]string{"<>", "sort"}, [2]string{"L", lock}, [2]string{"@", "hosts"},
			[2]string{"h", "help"}, [2]string{"q", "quit"})
	}
	// While the mouse belongs to the terminal, clicking a row and rolling
	// the wheel do nothing, and there is no other way to tell: the cursor
	// looks the same either way. The chip leads the footer so the state is
	// visible wherever you are, rather than only in the toast that has
	// since expired.
	if m.mouseOff {
		keys = append([][2]string{{"m", "mouse: yours"}}, keys...)
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, stFooterKey.Render(k[0])+stFooterTxt.Render(" "+k[1]))
	}
	// Keep only as many hints as actually fit: a narrow terminal with a long
	// key list (the default screen's has seven) can genuinely run past 80
	// columns, and losing the least essential trailing hint is better than
	// silently overflowing or wrapping onto a second line.
	gap := " " + colSep
	budget, kept := w, parts[:0:0]
	for i, p := range parts {
		add := visLen(p)
		if i > 0 {
			add += visLen(gap)
		}
		if budget-add < 0 {
			break
		}
		budget -= add
		kept = append(kept, p)
	}
	line := strings.Join(kept, gap)
	if m.editing {
		// The scope is a chip, not a word in a sentence: it is the thing you
		// change with Tab, so it has to look like a control rather than like
		// part of the label.
		scope, q := m.filterQuery()
		bar := stFilterChip.Render(" "+scope.String()+" ") +
			stAccent.Render(" ") + stPlain.Render(safeText(q)) + stMuted.Render("█")
		line = bar + "   " + line
	}
	return line
}

// centerCell renders a header label centered in its column — every data
// table's headers are centered while the data rows themselves keep
// right-aligned numbers and left-aligned text, which is what actually keeps
// a dense table scannable; centering the values too would make it much
// harder to compare numbers at a glance.
func centerCell(s string, width int, style lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	s = safeText(s)
	r := []rune(s)
	if len(r) > width {
		s = truncate(s, width)
		r = []rune(s)
	}
	out := style.Render(s)
	pad := width - len(r)
	if pad < 0 {
		pad = 0
	}
	left := pad / 2
	right := pad - left
	return strings.Repeat(" ", left) + out + strings.Repeat(" ", right)
}

func cell(s string, width int, right bool, style lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	s = safeText(s)
	r := []rune(s)
	if len(r) > width {
		if width > 1 {
			s = string(r[:width-1]) + "…"
		} else {
			s = string(r[:width])
		}
	}
	out := style.Render(s)
	pad := width - lipgloss.Width(s)
	if pad < 0 {
		pad = 0
	}
	sp := strings.Repeat(" ", pad)
	if right {
		return sp + out
	}
	return out + sp
}

var stPlain = lipgloss.NewStyle().Foreground(colText)
