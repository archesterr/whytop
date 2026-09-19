package tui

import (
	"strings"
	"testing"

	"github.com/archesterr/whytop/internal/collect"
)

// ~90% of the PIDs on an idle box are kernel threads, so a process list that
// shows them by default is mostly noise on the one screen people look at
// first. They stay hidden until K asks for them.
func TestProcRowsHidesKernelThreadsByDefault(t *testing.T) {
	procs := []collect.Proc{
		{PID: 1, Name: "systemd", Cmdline: "/sbin/init"},
		{PID: 2, Name: "kthreadd"},
		{PID: 12, Name: "kworker/0:1"},
		{PID: 99, Name: "nginx", Cmdline: "nginx: worker"},
	}
	snap := &collect.Snapshot{Procs: procs, ByPID: map[int32]int{}}
	for i, p := range procs {
		snap.ByPID[p.PID] = i
	}

	m := model{snap: snap, sortKey: "pid"}
	got := m.procRows()
	if len(got) != 2 {
		t.Fatalf("default view returned %d rows, want 2 (kernel threads hidden): %+v", len(got), got)
	}
	for _, p := range got {
		if p.Kernel() {
			t.Errorf("kernel thread %s leaked into the default process list", p.Name)
		}
	}

	m.showKernel = true
	if got := m.procRows(); len(got) != 4 {
		t.Errorf("with showKernel set, got %d rows, want all 4", len(got))
	}
}

// A zombie has no command line either, but it's a real userspace process and
// one of the more interesting things you can find — it must never be swept up
// with the kernel threads.
func TestZombieIsNeverTreatedAsKernelThread(t *testing.T) {
	zombie := collect.Proc{PID: 77, Name: "defunct-child", State: "Z"}
	if zombie.Kernel() {
		t.Error("a zombie process was classified as a kernel thread")
	}
	snap := &collect.Snapshot{Procs: []collect.Proc{zombie}, ByPID: map[int32]int{77: 0}}
	m := model{snap: snap, sortKey: "pid"}
	if got := m.procRows(); len(got) != 1 {
		t.Errorf("zombie was hidden from the default process list: got %d rows", len(got))
	}
}

func TestFindingsQuietOnAHealthyBox(t *testing.T) {
	m := model{snap: &collect.Snapshot{
		CPU:   collect.CPU{Cores: 4, Busy: 5, Iowait: 0},
		Mem:   collect.Mem{Total: 16 << 30, Used: 2 << 30, UsedPct: 12},
		Load1: 0.2,
		FS:    []collect.FS{{Mount: "/", UsedPct: 20, InodePct: 3}},
	}}
	if f := m.findings(); len(f) != 0 {
		t.Errorf("healthy box reported problems: %+v", f)
	}
	if got := m.renderStatus(80); !strings.Contains(stripANSI(got), "Nothing obviously wrong") {
		t.Errorf("healthy box status line = %q", stripANSI(got))
	}
}

func TestFindingsRankCriticalFirstAndReadPlainly(t *testing.T) {
	m := model{snap: &collect.Snapshot{
		CPU:   collect.CPU{Cores: 4, Busy: 20, Iowait: 15},
		Mem:   collect.Mem{Total: 16 << 30, Used: 15 << 30, UsedPct: 94},
		Load1: 1.0,
		Procs: []collect.Proc{{PID: 1, State: "D"}, {PID: 2, State: "D"}, {PID: 3, State: "D"}},
		FS:    []collect.FS{{Mount: "/var", UsedPct: 97}},
	}}
	found := m.findings()
	if len(found) == 0 {
		t.Fatal("a box with 94% memory, 15% iowait, 3 blocked processes and a full disk reported nothing")
	}
	if !found[0].crit {
		t.Errorf("most severe finding should sort first, got %+v", found[0])
	}
	all := ""
	for _, f := range found {
		all += f.text + " | "
	}
	for _, want := range []string{"stuck waiting on disk", "memory almost full", "/var almost full"} {
		if !strings.Contains(all, want) {
			t.Errorf("expected a plain-language finding containing %q, got: %s", want, all)
		}
	}
}

// The status line shares the same fixed-height layout as every other row: if
// it wraps, every budget below it shifts by one.
func TestRenderStatusNeverOverflows(t *testing.T) {
	var fs []collect.FS
	for i := 0; i < 20; i++ {
		fs = append(fs, collect.FS{Mount: "/very/long/mount/point/number/" + strings.Repeat("x", 20), UsedPct: 99})
	}
	m := model{snap: &collect.Snapshot{CPU: collect.CPU{Cores: 4}, FS: fs}}
	for _, w := range []int{1, 20, 40, 80, 200} {
		got := visLen(m.renderStatus(w))
		if got > w {
			t.Errorf("status line at width %d rendered %d visible columns", w, got)
		}
	}
}
