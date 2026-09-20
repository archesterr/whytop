package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	colBg     = lipgloss.Color("#1a1f27")
	colPanel  = lipgloss.Color("#212733")
	colLine   = lipgloss.Color("#313a4a")
	colHdrBg  = lipgloss.Color("#2a3242")
	colSecBg  = lipgloss.Color("#36425c") // section bars sit above column headers
	colSelBg  = lipgloss.Color("#39507a")
	colText   = lipgloss.Color("#d9dee7")
	colMuted  = lipgloss.Color("#8b94a5")
	colFaint  = lipgloss.Color("#5d6677")
	colAccent = lipgloss.Color("#8fb3ff")
	colOK     = lipgloss.Color("#7cc79a")
	colWarn   = lipgloss.Color("#e2b75f")
	colCrit   = lipgloss.Color("#e5736f")
	colMem    = lipgloss.Color("#c49bf5")
	colIO     = lipgloss.Color("#4fd1c5")
	colLoad   = lipgloss.Color("#f28fb0")
	colPSI    = lipgloss.Color("#f2a35c")

	// Table-reading colours. Deliberately not htop's palette: htop paints
	// by data type (every number green, every path cyan), which looks
	// lively and tells you nothing. These are picked so that the thing you
	// are hunting for in a wall of rows — the program's own name, a
	// non-root owner, a process eating the box — is the thing that catches
	// the eye first.
	colCmd      = lipgloss.Color("#cfe0ff") // the program's own name
	colCmdArgs  = lipgloss.Color("#7c8699") // its arguments, one step back
	colUser     = lipgloss.Color("#6fc8bd") // an ordinary user
	colUserRoot = lipgloss.Color("#e39ab4") // root
	colMemMid   = lipgloss.Color("#b79ae0")
	colMemHigh  = lipgloss.Color("#d8a0ff")
	colGaugeBg  = lipgloss.Color("#39414f")
	colNet      = lipgloss.Color("#7fd6a8") // traffic moving
	colPort     = lipgloss.Color("#f0c674") // a port someone can connect to
	colCore     = lipgloss.Color("#6f9be0") // a core doing ordinary work
)

var (
	stLogo     = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	stHost     = lipgloss.NewStyle().Bold(true).Foreground(colText)
	stMuted    = lipgloss.NewStyle().Foreground(colMuted)
	stFaint    = lipgloss.NewStyle().Foreground(colFaint)
	stOK       = lipgloss.NewStyle().Foreground(colOK)
	stWarn     = lipgloss.NewStyle().Foreground(colWarn)
	stCrit     = lipgloss.NewStyle().Foreground(colCrit)
	stAccent   = lipgloss.NewStyle().Foreground(colAccent)
	stBold     = lipgloss.NewStyle().Bold(true).Foreground(colText)
	stHeader   = lipgloss.NewStyle().Foreground(colMuted).Bold(true)
	stHeaderOn = lipgloss.NewStyle().Foreground(colAccent).Bold(true) // the sorted column
	// Column headers sit on a solid bar (htop's idea) so a table reads as a
	// table at a glance instead of as loose rows of text.
	stHdrBar    = lipgloss.NewStyle().Foreground(colFaint).Background(colHdrBg)
	stHdrCell   = lipgloss.NewStyle().Foreground(colMuted).Bold(true).Background(colHdrBg)
	stHdrCellOn = lipgloss.NewStyle().Foreground(colAccent).Bold(true).Background(colHdrBg)
	// A section bar outranks a column-header bar, so it's the brighter of
	// the two: section > columns > rows, readable at a glance.
	stSection   = lipgloss.NewStyle().Foreground(colText).Bold(true).Background(colSecBg)
	stSectionOn = lipgloss.NewStyle().Foreground(colBg).Bold(true).Background(colAccent)
	stTabOn     = lipgloss.NewStyle().Bold(true).Foreground(colBg).Background(colAccent)
	stTabOnCnt  = lipgloss.NewStyle().Foreground(colBg).Background(colAccent)
	stTabOff    = lipgloss.NewStyle().Foreground(colMuted)
	stFooterKey = lipgloss.NewStyle().Bold(true).Foreground(colBg).Background(colMuted).Padding(0, 1)
	stFooterTxt = lipgloss.NewStyle().Foreground(colMuted)
	stDanger    = lipgloss.NewStyle().Bold(true).Foreground(colCrit)
	stConfirm   = lipgloss.NewStyle().Bold(true).Foreground(colBg).Background(colWarn).Padding(0, 1)
	stToastOK   = lipgloss.NewStyle().Bold(true).Foreground(colBg).Background(colOK).Padding(0, 1)
	stToastErr  = lipgloss.NewStyle().Bold(true).Foreground(colBg).Background(colCrit).Padding(0, 1)
	stBox       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colLine).Padding(0, 1)

	stCmd        = lipgloss.NewStyle().Foreground(colCmd).Bold(true)
	stCmdArgs    = lipgloss.NewStyle().Foreground(colCmdArgs)
	stUser       = lipgloss.NewStyle().Foreground(colUser)
	stUserRoot   = lipgloss.NewStyle().Foreground(colUserRoot)
	stMemMid     = lipgloss.NewStyle().Foreground(colMemMid)
	stMemHigh    = lipgloss.NewStyle().Foreground(colMemHigh).Bold(true)
	stGaugeBg    = lipgloss.NewStyle().Foreground(colGaugeBg)
	stNet        = lipgloss.NewStyle().Foreground(colNet)
	stPort       = lipgloss.NewStyle().Foreground(colPort).Bold(true)
	stCore       = lipgloss.NewStyle().Foreground(colCore)
	stFilterChip = lipgloss.NewStyle().Bold(true).Foreground(colBg).Background(colPort)
	stFilterOn   = lipgloss.NewStyle().Foreground(colPort)
	stRemote     = lipgloss.NewStyle().Bold(true).Foreground(colBg).Background(colIO)
)

// colSep visibly separates table columns — a first-time user shouldn't have
// to guess where one column ends and the next begins from spacing alone.
// It's the same single-character width as the plain-space gap it replaces
// (sepW=1): every table's width budget was already tight against an
// 80-column terminal, the single most common size there is, so a wider
// separator isn't affordable without dropping a column somewhere.
const sepW = 1

var colSep = stFaint.Render("│")

func joinColsWith(sep string, cells ...string) string {
	if len(cells) == 0 {
		return ""
	}
	out := cells[0]
	for _, c := range cells[1:] {
		out += sep + c
	}
	return out
}

func joinCols(cells ...string) string {
	return joinColsSel(false, cells...)
}

// hdrCell renders one column header: centred over the narrow value columns,
// left-aligned over wide text columns.
//
// Centring everything is what stranded "COMMAND" and "TARGET" in the middle
// of a 90-column field with a void on either side — a header belongs over the
// start of the data it names, and wide columns hold left-aligned text.
func hdrCell(title string, w int, style lipgloss.Style) string {
	if w >= 24 {
		return cell(title, w, false, style)
	}
	return centerCell(title, w, style)
}

// sectionBar titles a block with a full-width bar. It's the line that
// separates one section from the next: without it the detail view is one long
// column of text with bare labels dropped into it, and nothing tells you
// where the process tree ends and the open files begin.
func sectionBar(w int, title string) string {
	bar := stSection.Render(" " + title + " ")
	if n := max0(w - visLen(bar)); n > 0 {
		bar += stSection.Render(strings.Repeat(" ", n))
	}
	return bar
}

// focusBar is a section bar that shows whether its list is the one the arrow
// keys are driving. A panel with two navigable lists has to say which one has
// the cursor, or the keys feel broken.
func focusBar(w int, title string, focused bool) string {
	if !focused {
		return sectionBar(w, title)
	}
	bar := stSectionOn.Render(" ▸ " + title + " ")
	if n := max0(w - visLen(bar)); n > 0 {
		bar += stSectionOn.Render(strings.Repeat(" ", n))
	}
	return bar
}

// tableHeader draws a column header as one solid bar across the table, the
// way htop does. It separates the header from the data without spending a
// whole row on a rule line — on a 24-row SSH terminal every row a table
// gives up is a row of actual data you can't see.
func tableHeader(w int, cells ...string) string {
	row := joinColsWith(stHdrBar.Render("│"), cells...)
	if n := max0(w - visLen(row)); n > 0 {
		row += stHdrBar.Render(strings.Repeat(" ", n))
	}
	return row
}

// joinColsSel joins cells with colSep, carrying a selected row's background
// onto the separators too — otherwise the highlight would show visible gaps
// between columns instead of one solid selected row.
func joinColsSel(sel bool, cells ...string) string {
	sep := colSep
	if sel {
		sep = withBG(stFaint, true).Render("│")
	}
	return joinColsWith(sep, cells...)
}

// lvl returns a style for a value against warn/crit thresholds.
func lvl(v, warn, crit float64) lipgloss.Style {
	switch {
	case v >= crit:
		return stCrit
	case v >= warn:
		return stWarn
	default:
		return lipgloss.NewStyle().Foreground(colText)
	}
}

func stateStyle(s string) lipgloss.Style {
	switch s {
	case "R":
		return stOK
	case "D":
		return stCrit
	case "Z", "T", "t":
		return stWarn
	default:
		return stMuted
	}
}
