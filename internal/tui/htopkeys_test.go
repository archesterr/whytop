package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Being nearly-but-not-quite htop is worse than being nothing like it: a key
// that does something *else* is how the wrong process gets killed. These pin
// the keys both tools have a name for.
func TestKeysMatchHtopAndTop(t *testing.T) {
	base := func() model {
		m := initialModel(Options{})
		m.snap = portSnap()
		m.sel = "1"
		m.width, m.height = 200, 40
		return m
	}

	t.Run("P M T sort by cpu, memory, time", func(t *testing.T) {
		for key, want := range map[string]string{"P": "cpu", "M": "mem", "T": "time"} {
			got, _ := base().handleListKey(keyRunes(key))
			if got.(model).sortKey != want {
				t.Errorf("%s sorted by %q, want %q", key, got.(model).sortKey, want)
			}
		}
	})

	t.Run("k kills the selected process", func(t *testing.T) {
		got, _ := base().handleListKey(keyRunes("k"))
		c := got.(model).confirm
		if c == nil {
			t.Fatal("k should ask before signalling")
		}
		if !strings.Contains(c.prompt, "SIGTERM") {
			t.Errorf("prompt should name the signal: %q", c.prompt)
		}
	})

	t.Run("K still toggles kernel threads", func(t *testing.T) {
		got, _ := base().handleListKey(keyRunes("K"))
		if !got.(model).showKernel {
			t.Error("K should show kernel threads, as in htop")
		}
	})

	t.Run("< and > move the sort column", func(t *testing.T) {
		m := base()
		fwd, _ := m.handleListKey(keyRunes(">"))
		if fwd.(model).sortKey == m.sortKey {
			t.Error("> should move to the next sort column")
		}
		back, _ := fwd.(model).handleListKey(keyRunes("<"))
		if back.(model).sortKey != m.sortKey {
			t.Errorf("< should come back to %q, got %q", m.sortKey, back.(model).sortKey)
		}
	})

	t.Run("I and R invert the order", func(t *testing.T) {
		for _, key := range []string{"I", "R"} {
			m := base()
			got, _ := m.handleListKey(keyRunes(key))
			if got.(model).sortDir != -m.sortDir {
				t.Errorf("%s should invert the sort order", key)
			}
		}
	})

	t.Run("u filters by user", func(t *testing.T) {
		got, _ := base().handleListKey(keyRunes("u"))
		gm := got.(model)
		if !gm.editing || gm.filterScope != scopeUser {
			t.Errorf("u should open the filter on the owner, got editing=%v scope=%v", gm.editing, gm.filterScope)
		}
	})

	t.Run("h and F1 open help", func(t *testing.T) {
		got, _ := base().handleListKey(keyRunes("h"))
		if !got.(model).help {
			t.Error("h should open help")
		}
		f1, _ := base().handleListKey(tea.KeyMsg{Type: tea.KeyF1})
		if !f1.(model).help {
			t.Error("F1 should open help")
		}
	})

	t.Run("q and F10 quit", func(t *testing.T) {
		if got, _ := base().handleListKey(keyRunes("q")); !got.(model).quitting {
			t.Error("q should quit")
		}
		if got, _ := base().handleListKey(tea.KeyMsg{Type: tea.KeyF10}); !got.(model).quitting {
			t.Error("F10 should quit")
		}
	})

	// The one that would be actively dangerous: j/k used to move the cursor,
	// and k is htop's kill. If both were bound, someone scrolling with vi
	// keys would signal a process.
	t.Run("k is not also cursor movement", func(t *testing.T) {
		m := base()
		got, _ := m.handleListKey(keyRunes("k"))
		if got.(model).sel != m.sel {
			t.Error("k must not move the cursor as well as kill")
		}
	})
}

// t is htop's tree, and it has to actually build one.
func TestTreeViewNestsChildrenUnderParents(t *testing.T) {
	snap := portSnap()
	snap.Procs[1].PPID = snap.Procs[0].PID // sshd under nginx, for the shape
	snap.Procs[3].PPID = snap.Procs[1].PID
	m := model{snap: snap, sortKey: "pid", sortDir: 1}

	got, _ := m.handleListKey(keyRunes("t"))
	gm := got.(model)
	if !gm.tree {
		t.Fatal("t should turn on tree view")
	}
	rows := gm.procRows()
	depth := map[string]int{}
	order := []string{}
	for _, p := range rows {
		depth[p.Name] = p.Depth
		order = append(order, p.Name)
	}
	if depth["nginx"] != 0 || depth["sshd"] != 1 || depth["curl"] != 2 {
		t.Errorf("depths wrong: %v (order %v)", depth, order)
	}
	// A child must come directly after its parent, not somewhere later.
	for i, name := range order {
		if name == "sshd" && order[i-1] != "nginx" {
			t.Errorf("sshd should follow nginx directly, got %v", order)
		}
	}
}

// A cycle in the parent links must not hang the UI. /proc is not read
// atomically, so it is possible in principle.
func TestTreeViewSurvivesAParentCycle(t *testing.T) {
	snap := portSnap()
	snap.Procs[0].PPID = snap.Procs[1].PID
	snap.Procs[1].PPID = snap.Procs[0].PID
	m := model{snap: snap, sortKey: "pid", sortDir: 1, tree: true}
	done := make(chan int, 1)
	go func() { done <- len(m.procRows()) }()
	select {
	case n := <-done:
		if n != len(snap.Procs) {
			t.Errorf("a cycle lost rows: got %d of %d", n, len(snap.Procs))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tree ordering hung on a parent cycle")
	}
}

// p is htop's program-path toggle: the whole path, or just what is running.
func TestProgramPathToggle(t *testing.T) {
	snap := portSnap()
	snap.Procs[1].Cmdline = "/usr/sbin/sshd -D"
	m := model{snap: snap, sortKey: "pid", fullPath: true}
	if got := m.commandOf(snap.Procs[1]); got != "/usr/sbin/sshd -D" {
		t.Errorf("by default the full command line shows, got %q", got)
	}
	off, _ := m.handleListKey(keyRunes("p"))
	if got := off.(model).commandOf(snap.Procs[1]); got != "sshd -D" {
		t.Errorf("p should strip the path, got %q", got)
	}
}
