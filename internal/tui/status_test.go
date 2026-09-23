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

// On a terminal too narrow for even one finding, the status line used to
// say "⚠ +1 more" — a count of the things it was more than, with none of
// them shown. What is wrong with the machine is the whole point of the
// line; how many other things are also wrong is not.
func TestANarrowStatusLineStillSaysWhatIsWrong(t *testing.T) {
	found := []finding{
		{text: "/opt/claude-code filling up (90.5%)", crit: true},
		{text: "CPU saturated (100.0%)"},
	}
	for _, w := range []int{20, 30, 36, 39} {
		regions, hidden := statusLayout(found, w)
		if len(regions) == 0 {
			t.Errorf("w=%d: nothing drawn at all (hidden=%d)", w, hidden)
			continue
		}
		if !strings.HasPrefix(found[0].text, strings.TrimSuffix(regions[0].label, "…")) {
			t.Errorf("w=%d: drew %q, which is not the start of %q", w, regions[0].label, found[0].text)
		}
		if got := visLen(regions[0].label); got > w-2 {
			t.Errorf("w=%d: the label is %d wide, past the %d columns there are", w, got, w-2)
		}
		// And the whole rendered line must still fit.
		if got := visLen(renderStatusFor(found, w)); got > w {
			t.Errorf("w=%d: the status line rendered %d columns wide", w, got)
		}
	}
}

// A width with no room for anything readable draws the marker alone rather
// than a word cut to two letters.
func TestAHopelesslyNarrowStatusLineDrawsTheMarkerOnly(t *testing.T) {
	found := []finding{{text: "/opt/claude-code filling up (90.5%)"}}
	if regions, _ := statusLayout(found, 6); len(regions) != 0 {
		t.Errorf("6 columns drew %q", regions[0].label)
	}
}

// g steps to the next problem. It used to remember its place as an index
// into the findings list — but that list is rebuilt from a fresh sample on
// every press, so a machine that stops having processes stuck on disk
// between two presses loses a finding, everything after it shifts down, and
// the saved index lands on one already visited while skipping the one it
// should have reached. That is exactly the machine people press g on: a
// steady box has nothing to step through.
func TestJumpStepsToTheNextProblemWhenTheListChangesUnderIt(t *testing.T) {
	// Three findings: blocked processes, CPU, load.
	snap := testSnap()
	for i := range snap.Procs {
		snap.Procs[i].State = "D"
	}
	snap.CPU.Busy, snap.CPU.Cores, snap.Load1 = 99, 4, 12
	m := model{snap: snap, sortKey: "cpu", width: 140, height: 40}

	found := m.findings()
	if len(found) < 3 {
		t.Fatalf("the fixture produced %d findings, want at least 3: %+v", len(found), found)
	}
	if found[0].key != "blocked" {
		t.Fatalf("the fixture's first finding is %q, not the blocked one: %+v", found[0].key, found)
	}
	got, _ := m.jumpToFinding()
	m = got.(model)
	if m.findingLast != "blocked" {
		t.Fatalf("the first press landed on %q, want the blocked processes", m.findingLast)
	}

	// Now the blocked processes clear — a machine recovering — and that
	// finding drops off the front, shifting everything after it down one.
	recovered := testSnap()
	recovered.CPU.Busy, recovered.CPU.Cores, recovered.Load1 = 99, 4, 12
	m.snap = recovered
	after := m.findings()
	if len(after) < 2 || after[0].key != "cpu" {
		t.Fatalf("after recovering, the findings are %+v", after)
	}

	// The next press must land on the finding that now follows the one it
	// was on — "cpu". Remembering index 1 would land on "load" and skip it.
	got, _ = m.jumpToFinding()
	if got.(model).findingLast != "cpu" {
		t.Errorf("g skipped to %q; the finding after the blocked one is now \"cpu\"",
			got.(model).findingLast)
	}
}

// And with the list steady, g walks every finding once and comes back round
// rather than sticking or skipping.
func TestJumpWalksEveryFindingInTurn(t *testing.T) {
	snap := testSnap()
	for i := range snap.Procs {
		snap.Procs[i].State = "D"
	}
	snap.CPU.Busy, snap.CPU.Cores, snap.Load1 = 99, 4, 12
	m := model{snap: snap, sortKey: "cpu", width: 140, height: 40}
	n := len(m.findings())
	if n < 2 {
		t.Fatalf("the fixture produced %d findings, want at least 2", n)
	}

	seen := map[string]int{}
	for i := 0; i < n; i++ {
		got, _ := m.jumpToFinding()
		m = got.(model)
		seen[m.findingLast]++
	}
	if len(seen) != n {
		t.Errorf("%d presses visited %d of %d findings: %v", n, len(seen), n, seen)
	}
	for k, c := range seen {
		if c != 1 {
			t.Errorf("g landed on %q %d times in one pass", k, c)
		}
	}
}

func TestCeilingsTopNeverShowsAreFindings(t *testing.T) {
	s := &collect.Snapshot{ByPID: map[int32]int{}}
	s.Procs = []collect.Proc{
		{PID: 10, Name: "api", FDs: 1000, FDLimit: 1024},
		{PID: 11, Name: "leaky"},
	}
	s.ByPID[10], s.ByPID[11] = 0, 1
	s.Throttles = []collect.Throttle{{Cgroup: "/k/pod", Label: "payments.service", PID: 10, Pct: 80, Quota: 0.5}}
	s.Limits = collect.Limits{FilesUsed: 96, FilesMax: 100, ConntrackUsed: 85, ConntrackMax: 100, ListenOverflowPs: 12}
	for i := 0; i < 60; i++ {
		s.Conns = append(s.Conns, collect.Conn{Proto: "tcp", State: "CLOSE_WAIT", PID: 11})
	}
	want := map[string]string{
		"throttle:/k/pod": "payments.service throttled 80% of 0.5-core limit",
		"fd:10":           "api[10] 1000/1024 open files",
		"fd-sys":          "file handles 96% used",
		"conntrack":       "conntrack 85% full",
		"listen-drop":     "12 conn/s dropped, listen queue full",
		"close-wait:11":   "leaky[11] 60 sockets in CLOSE_WAIT",
	}
	got := map[string]finding{}
	for _, f := range (model{snap: s}).findings() {
		got[f.key] = f
	}
	for k, text := range want {
		if got[k].text != text {
			t.Errorf("%s: got %q, want %q", k, got[k].text, text)
		}
	}
	if got["fd:10"].to.filter != "pid:10" {
		t.Errorf("g on the fd finding should land on the process, got filter %q", got["fd:10"].to.filter)
	}
}
