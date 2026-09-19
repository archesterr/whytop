package tui

import "github.com/charmbracelet/lipgloss"

var (
	colBg     = lipgloss.Color("#1a1f27")
	colPanel  = lipgloss.Color("#212733")
	colLine   = lipgloss.Color("#313a4a")
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
)

var (
	stLogo      = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	stHost      = lipgloss.NewStyle().Bold(true).Foreground(colText)
	stMuted     = lipgloss.NewStyle().Foreground(colMuted)
	stFaint     = lipgloss.NewStyle().Foreground(colFaint)
	stOK        = lipgloss.NewStyle().Foreground(colOK)
	stWarn      = lipgloss.NewStyle().Foreground(colWarn)
	stCrit      = lipgloss.NewStyle().Foreground(colCrit)
	stAccent    = lipgloss.NewStyle().Foreground(colAccent)
	stBold      = lipgloss.NewStyle().Bold(true).Foreground(colText)
	stHeader    = lipgloss.NewStyle().Foreground(colMuted).Bold(true)
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
)

// colSep visibly separates table columns — a first-time user shouldn't have
// to guess where one column ends and the next begins from spacing alone.
// It's the same single-character width as the plain-space gap it replaces
// (sepW=1): every table's width budget was already tight against an
// 80-column terminal, the single most common size there is, so a wider
// separator isn't affordable without dropping a column somewhere.
const sepW = 1

var colSep = stFaint.Render("│")

func joinCols(cells ...string) string {
	return joinColsSel(false, cells...)
}

// joinColsSel joins cells with colSep, carrying a selected row's background
// onto the separators too — otherwise the highlight would show visible gaps
// between columns instead of one solid selected row.
func joinColsSel(sel bool, cells ...string) string {
	sep := colSep
	if sel {
		sep = withBG(stFaint, true).Render("│")
	}
	out := cells[0]
	for _, c := range cells[1:] {
		out += sep + c
	}
	return out
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
