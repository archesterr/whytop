package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/update"
)

// The update prompt is the one piece of whytop that interrupts you to ask
// for something, so it is held to three rules.
//
// It asks once. "No" and "remind me later" are both written to disk, because
// a prompt that reappears every launch is one people dismiss by reflex —
// and reflex-dismissing a prompt that can replace a root-run binary is a
// habit worth not teaching.
//
// It never interrupts work in progress. The check runs a minute after start
// and the prompt only appears over the process list, never over an open
// detail panel, a confirm, or a filter someone is typing.
//
// And it does not offer what it cannot do. On a copy that apt owns, the
// prompt is not a prompt at all: it says a newer version exists and gives
// the apt command, because a self-updating binary in a distribution package
// is both forbidden by policy and futile — dpkg reverts it on the next
// upgrade.

// updateState is a release worth telling the operator about.
type updateState struct {
	rel     update.Release
	install update.Install
	// busy marks the window between accepting and the download finishing,
	// so the footer says something is happening rather than going blank for
	// however long a release archive takes on a slow link.
	busy bool
}

// updateFoundMsg carries the result of the background check. A check that
// found nothing, failed, or was turned off produces no message at all:
// whytop is a troubleshooting tool, and "could not reach GitHub" is not a
// finding about the machine being troubleshot.
type updateFoundMsg struct {
	rel     update.Release
	install update.Install
}

type updateDoneMsg struct {
	tag string
	err error
}

// updateRetryDelay is how long a finding waits when the screen was busy
// when it arrived. A variable rather than a constant so tests can ask for
// the retry without waiting out a real minute.
var updateRetryDelay = time.Minute

// updateCheckDelay keeps the check off the startup path. Someone who opened
// whytop because a machine is on fire should see the machine first.
const updateCheckDelay = 45 * time.Second

func checkUpdateCmd(current string, after time.Duration) tea.Cmd {
	return tea.Tick(after, func(time.Time) tea.Msg {
		if update.Disabled() {
			return nil
		}
		st := update.LoadState()
		if time.Since(st.LastCheck) < update.CheckInterval {
			return nil
		}
		rel, err := update.Check(context.Background())
		if err != nil {
			return nil
		}
		st.LastCheck = time.Now()
		_ = update.SaveState(st)

		if !update.IsNewer(current, rel.Tag) || !st.Due(rel.Tag, time.Now()) {
			return nil
		}
		return updateFoundMsg{rel: rel, install: update.Detect(context.Background())}
	})
}

// retryUpdatePromptCmd re-delivers a finding the UI was too busy to show.
func retryUpdatePromptCmd(msg updateFoundMsg, after time.Duration) tea.Cmd {
	return tea.Tick(after, func(time.Time) tea.Msg { return msg })
}

func applyUpdateCmd(rel update.Release, in update.Install) tea.Cmd {
	return func() tea.Msg {
		return updateDoneMsg{tag: rel.Tag, err: update.Apply(context.Background(), rel, in)}
	}
}

// answerUpdate handles the three answers. Yes and no are self-explanatory;
// later is the one that earns the prompt its keep, because the honest answer
// to "update now?" on a box someone is mid-incident on is usually "not now".
func (m model) answerUpdate(answer rune) (tea.Model, tea.Cmd) {
	u := m.upd
	if u == nil || u.busy {
		return m, nil
	}
	st := update.LoadState()
	switch answer {
	case 'y':
		if !u.install.CanSelfUpdate() {
			// There is nothing to accept: the only useful thing whytop can
			// do is name the command that does work, and get out of the way.
			m.upd = nil
			return m.showToastFor("Run: "+u.install.Upgrade, true, oomToastTTL)
		}
		// A copy, not a write through the pointer. model is passed by
		// value everywhere in this program, so mutating what it points at
		// changes every copy of the model that still holds it — including
		// ones bubbletea has already moved past. Here that meant a prompt
		// that had been answered once could never be answered again.
		busy := *u
		busy.busy = true
		m.upd = &busy
		return m, applyUpdateCmd(u.rel, u.install)
	case 'n':
		st.Skipped = u.rel.Tag
		_ = update.SaveState(st)
		m.upd = nil
		return m, nil
	case 'l':
		st.RemindAfter = time.Now().Add(update.RemindInterval)
		_ = update.SaveState(st)
		m.upd = nil
		return m, nil
	}
	return m, nil
}

// updatePrompt is the footer line. The three answers are spelled out as
// keys because that is what they are — a row of words that look like buttons
// but only respond to a mouse is worse than either on its own.
func (m model) updatePrompt(w int) string {
	u := m.upd
	if u == nil {
		return ""
	}
	if u.busy {
		return stConfirm.Render(fmt.Sprintf("Downloading %s and verifying its checksum…", u.rel.Tag))
	}
	if !u.install.CanSelfUpdate() {
		// Not a question. Anything that reads as one here would be offering
		// something whytop must refuse to do.
		line := fmt.Sprintf("whytop %s is available — this copy is managed by %s: %s",
			u.rel.Tag, u.install.Manager, u.install.Upgrade)
		return fitPrompt(line, stMuted.Render("[n] dismiss  [l] later"), w)
	}
	head := fmt.Sprintf("whytop %s is available (you have %s). Update now?", u.rel.Tag, displayVersion(m.opt.Version))
	btns := stOK.Render("[y] yes") + "  " + stMuted.Render("[n] no") + "  " + stAccent.Render("[l] remind me later")
	return fitPrompt(head, btns, w)
}

// fitPrompt lays out one footer line as a message and the answers to it,
// inside exactly w columns.
//
// The answers are measured first and the message takes what is left, because
// a truncated question is still a question but a truncated set of answers is
// a prompt you cannot reply to. stConfirm pads by a column on each side, and
// forgetting that is how this line ran eight columns past a 132-wide footer
// while every test that measured the *list* still passed.
func fitPrompt(msg, answers string, w int) string {
	const promptPad = 2 // stConfirm's Padding(0, 1)
	room := max0(w - visLen(answers) - 1 - promptPad)
	if room == 0 {
		// No room for the message at all: the answers are what matters.
		return truncateANSI(answers, w)
	}
	return stConfirm.Render(truncate(msg, room)) + " " + answers
}

// displayVersion keeps the prompt honest about a build that is not a
// release: "you have dev" is more useful than a blank.
func displayVersion(v string) string {
	if v = strings.TrimSpace(v); v == "" {
		return "an unversioned build"
	}
	return v
}
