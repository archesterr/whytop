package tui

import (
	"strconv"
	"strings"

	"github.com/archesterr/whytop/internal/collect"
)

// A filter without a scope has to guess what you meant, and it guesses wrong
// in exactly the case that matters: typing 443 to find out what is serving
// it also matches every PID, every memory figure and every command line with
// 443 anywhere in it. The scope makes the question explicit, and Tab cycles
// it while the filter is open.
type filterScope int

const (
	scopeAll filterScope = iota
	scopePort
	scopeUser
	scopeUnit
	scopeCommand
	scopePID
	scopeState
	numScopes
)

func (f filterScope) String() string {
	return [...]string{"all", "port", "user", "unit", "command", "pid", "state"}[f]
}

// prefix is the typed form of a scope, so that what the Tab key does and what
// "port:443" does are the same one mechanism rather than two that can drift.
func (f filterScope) prefix() string {
	if f == scopeAll {
		return ""
	}
	return f.String() + ":"
}

func scopeFromPrefix(s string) (filterScope, string, bool) {
	name, rest, ok := strings.Cut(s, ":")
	if !ok {
		return scopeAll, s, false
	}
	for f := scopeAll + 1; f < numScopes; f++ {
		if f.String() == name {
			return f, rest, true
		}
	}
	return scopeAll, s, false
}

// hint tells you what the current scope will do with what you type, because
// "port" and "command" narrow in very different ways and the difference is
// only obvious once you have already been surprised by it.
func (f filterScope) hint() string {
	switch f {
	case scopePort:
		return "listening port, exact"
	case scopeUser:
		return "owner"
	case scopeUnit:
		return "systemd unit"
	case scopeCommand:
		return "command line"
	case scopePID:
		return "process id, exact"
	case scopeState:
		return "state letter, exact"
	default:
		return "any column"
	}
}

// matches reports whether a process passes the filter. An empty query passes
// everything, including in a narrow scope — an empty port filter means "not
// filtering", not "processes with no ports".
func (s filterScope) matches(p collect.Proc, q string) bool {
	if q == "" {
		return true
	}
	switch s {
	case scopePort:
		return hasPort(p, q)
	case scopeUser:
		return strings.Contains(strings.ToLower(p.User), q)
	case scopeUnit:
		return strings.Contains(strings.ToLower(unitName(p.Unit)), q)
	case scopeCommand:
		return strings.Contains(strings.ToLower(cmdOf(p)), q)
	case scopePID:
		return strconv.Itoa(int(p.PID)) == q
	case scopeState:
		return strings.EqualFold(p.State, q)
	default:
		// The wide net, for when you don't yet know what you're looking for.
		// A bare number still matches ports here, because that is the most
		// common reason to type one.
		hay := strconv.Itoa(int(p.PID)) + " " +
			strings.ToLower(p.Name+" "+p.User+" "+unitName(p.Unit)+" "+p.Cmdline+" "+p.Container+" "+p.Runtime)
		return strconv.Itoa(int(p.PID)) == q || hasPort(p, q) || strings.Contains(hay, q)
	}
}

// filterQuery resolves the typed filter into a scope and a query. A typed
// prefix wins over the cycled scope, so a status-line jump that sets
// "state:D" narrows correctly whatever scope the operator last left the
// filter in.
func (m model) filterQuery() (filterScope, string) {
	raw := strings.ToLower(strings.TrimSpace(m.filter))
	if scope, rest, ok := scopeFromPrefix(raw); ok {
		return scope, strings.TrimSpace(rest)
	}
	return m.filterScope, raw
}

// filterLabel is what the filter bar shows while you're typing.
func (m model) filterLabel() string {
	scope, _ := m.filterQuery()
	return scope.String()
}

// emptyMessage explains an empty list in the terms of the filter that
// emptied it. Landing on nothing is a normal outcome of jumping to a problem
// that has since cleared, so it has to read as an answer and not as a search
// that failed.
func (m model) emptyMessage() string {
	scope, q := m.filterQuery()
	if q == "" {
		return "No processes."
	}
	switch scope {
	case scopePort:
		return "Nothing is listening on port " + safeText(q) + ". Press esc to show everything."
	case scopeState:
		return "Nothing is in state " + strings.ToUpper(safeText(q)) + " right now — it may have cleared. Press esc to show everything."
	case scopeAll:
		return "No process matches " + strconv.Quote(safeText(q)) + ". Press esc to clear the filter."
	default:
		return "No process matches " + strconv.Quote(safeText(q)) + " in " + scope.hint() + ". Press esc to clear the filter."
	}
}
