package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/archesterr/whytop/internal/update"
)

func offered(tag string, in update.Install) *updateState {
	return &updateState{rel: update.Release{Tag: tag, Version: strings.TrimPrefix(tag, "v")}, install: in}
}

func selfManaged() update.Install {
	return update.Install{Method: update.SelfManaged, Path: "/usr/local/bin/whytop"}
}

func packaged() update.Install {
	return update.Install{Method: update.Packaged, Manager: "apt", Path: "/usr/bin/whytop",
		Upgrade: "sudo apt update && sudo apt install --only-upgrade whytop"}
}

// The prompt offers exactly the three answers it promises, and each of them
// is a key that works. A button nobody can press is decoration.
func TestUpdatePromptOffersThreeAnswers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := model{snap: testSnap(), sortKey: "cpu", width: 140, height: 40, upd: offered("v9.9.9", selfManaged())}

	foot := stripANSI(m.renderFooter(140))
	for _, want := range []string{"[y] yes", "[n] no", "[l] remind me later", "v9.9.9"} {
		if !strings.Contains(foot, want) {
			t.Errorf("the prompt does not offer %q: %s", want, foot)
		}
	}

	// Yes starts the download and says so rather than going quiet.
	got, cmd := m.Update(runes("y"))
	if cmd == nil {
		t.Error("answering yes did nothing")
	}
	if u := got.(model).upd; u == nil || !u.busy {
		t.Error("answering yes did not put the prompt into its downloading state")
	} else if !strings.Contains(stripANSI(got.(model).renderFooter(140)), "Downloading") {
		t.Error("the footer does not say a download is underway")
	}

	// No dismisses it, and stays dismissed for that release. Each answer
	// gets its own config directory: they all write the same file, and a test
	// that shares one is really asserting the order it ran them in.
	t.Run("no", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		got, _ := m.Update(runes("n"))
		if got.(model).upd != nil {
			t.Error("answering no left the prompt on screen")
		}
		if st := update.LoadState(); st.Skipped != "v9.9.9" {
			t.Errorf("answering no recorded %+v, want v9.9.9 skipped", st)
		}
	})

	t.Run("later", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		before := time.Now()
		got, _ := m.Update(runes("l"))
		if got.(model).upd != nil {
			t.Error("answering later left the prompt on screen")
		}
		st := update.LoadState()
		if st.RemindAfter.Before(before.Add(update.RemindInterval - time.Minute)) {
			t.Errorf("later set the reminder to %v, want about %v from now", st.RemindAfter, update.RemindInterval)
		}
		if st.Skipped != "" {
			t.Error("later must not also skip the release — that is what no is for")
		}
	})
}

// Esc is how every other transient thing in whytop goes away, so it means
// later rather than nothing.
func TestEscapePostponesTheUpdate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := model{snap: testSnap(), sortKey: "cpu", width: 140, height: 40, upd: offered("v9.9.9", selfManaged())}
	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got.(model).upd != nil {
		t.Error("esc did not dismiss the prompt")
	}
	if update.LoadState().RemindAfter.IsZero() {
		t.Error("esc dismissed the prompt without postponing it, so it would return on the next launch")
	}
}

// An unsolicited prompt must not take the keyboard hostage. Every key that
// is not one of its three answers does what it always does.
func TestTheUpdatePromptDoesNotSwallowTheKeyboard(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := model{snap: testSnap(), sortKey: "cpu", width: 140, height: 40, upd: offered("v9.9.9", selfManaged())}
	got, _ := m.Update(runes("t"))
	if !got.(model).tree {
		t.Error("t did not toggle the tree view while the update prompt was up")
	}
	if got.(model).upd == nil {
		t.Error("an unrelated key dismissed the update prompt")
	}
}

// This is the one that keeps whytop eligible for the Debian archive. A copy
// apt installed must be told to use apt, not offered a self-update: policy
// forbids a package writing outside dpkg's knowledge, and dpkg would revert
// the replacement on the next upgrade anyway.
func TestAPackagedCopyIsToldToUseItsPackageManager(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := model{snap: testSnap(), sortKey: "cpu", width: 160, height: 40, upd: offered("v9.9.9", packaged())}

	foot := stripANSI(m.renderFooter(160))
	if !strings.Contains(foot, "apt") {
		t.Errorf("a packaged copy is not told how to upgrade: %s", foot)
	}
	if strings.Contains(foot, "[y] yes") {
		t.Errorf("a packaged copy was offered a self-update: %s", foot)
	}

	// And if they press y anyway, nothing replaces the binary.
	got, _ := m.Update(runes("y"))
	if u := got.(model).upd; u != nil && u.busy {
		t.Fatal("a packaged copy started downloading a replacement for itself")
	}
}

// The prompt is one line in a fixed-width footer like every other line.
func TestUpdatePromptFitsTheFooter(t *testing.T) {
	for _, in := range []update.Install{selfManaged(), packaged()} {
		for _, w := range []int{60, 80, 100, 132, 200} {
			m := model{snap: testSnap(), sortKey: "cpu", width: w, height: 30, upd: offered("v9.9.9", in)}
			if got := visLen(m.renderFooter(w)); got > w {
				t.Errorf("w=%d %v: the prompt is %d columns: %q", w, in.Method, got, stripANSI(m.renderFooter(w)))
			}
		}
	}
}

// Interrupting a confirm with an update prompt would put "Update now?" where
// "Kill PID 1234?" was, under a cursor already moving towards y.
func TestTheUpdatePromptWaitsForABusyScreen(t *testing.T) {
	old := updateRetryDelay
	updateRetryDelay = time.Millisecond
	t.Cleanup(func() { updateRetryDelay = old })

	busy := map[string]model{
		"a confirm is up":         {confirm: &confirmState{prompt: "Kill it? [y/N]"}},
		"a filter is being typed": {editing: true},
		"a process panel is open": {detail: &detailState{pid: 1}},
		"the help is open":        {help: true},
	}
	for name, m := range busy {
		m.snap, m.sortKey, m.width, m.height = testSnap(), "cpu", 140, 40
		got, cmd := m.Update(updateFoundMsg{rel: update.Release{Tag: "v9.9.9"}, install: selfManaged()})
		if got.(model).upd != nil {
			t.Errorf("the update prompt appeared while %s", name)
		}
		if cmd == nil {
			t.Fatalf("the prompt was dropped rather than retried while %s", name)
		}
		// And the retry must re-deliver the finding, not run another
		// check — a fresh check is refused by its own rate limit, so
		// re-checking here loses the prompt for half a day.
		if _, ok := cmd().(updateFoundMsg); !ok {
			t.Errorf("the retry while %s does not carry the release it already found", name)
		}
	}

	// Over the plain list, it appears.
	m := model{snap: testSnap(), sortKey: "cpu", width: 140, height: 40}
	got, _ := m.Update(updateFoundMsg{rel: update.Release{Tag: "v9.9.9"}, install: selfManaged()})
	if got.(model).upd == nil {
		t.Error("the update prompt never appeared over an idle process list")
	}
}
