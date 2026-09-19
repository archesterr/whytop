package tui

import (
	"testing"

	"github.com/archesterr/whytop/internal/collect"
)

func testSnap() *collect.Snapshot {
	// Every fixture carries a Cmdline: a process without one is a kernel
	// thread (Proc.Kernel), which the process list hides by default.
	procs := []collect.Proc{
		{PID: 1, Name: "init", User: "root", CPU: 0, RSS: 100, Cmdline: "/sbin/init"},
		{PID: 42, Name: "nginx", User: "www", CPU: 12.5, RSS: 5000, Unit: "nginx.service", Cmdline: "nginx: master process"},
		{PID: 43, Name: "worker", User: "www", CPU: 55.0, RSS: 2000, Cmdline: "nginx: worker"},
	}
	byPID := map[int32]int{}
	for i, p := range procs {
		byPID[p.PID] = i
	}
	return &collect.Snapshot{Procs: procs, ByPID: byPID}
}

func TestProcRowsSortByCPU(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "cpu"}
	list := m.procRows()
	if len(list) != 3 {
		t.Fatalf("got %d rows, want 3", len(list))
	}
	if list[0].PID != 43 || list[1].PID != 42 || list[2].PID != 1 {
		t.Errorf("sort by cpu descending wrong order: %+v", list)
	}
}

func TestProcRowsSortByPIDAscending(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "pid"}
	list := m.procRows()
	if list[0].PID != 1 || list[1].PID != 42 || list[2].PID != 43 {
		t.Errorf("sort by pid ascending wrong order: %+v", list)
	}
}

func TestProcRowsFilterByName(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "cpu"}
	m.filter[tabProcs] = "nginx"
	list := m.procRows()
	if len(list) != 2 {
		t.Fatalf("got %d rows matching 'nginx', want 2 (name match + cmdline match)", len(list))
	}
	for _, p := range list {
		if p.PID == 1 {
			t.Errorf("filter leaked non-matching process: %+v", p)
		}
	}
}

func TestProcRowsFilterByPID(t *testing.T) {
	m := model{snap: testSnap(), sortKey: "cpu"}
	m.filter[tabProcs] = "42"
	list := m.procRows()
	if len(list) != 1 || list[0].PID != 42 {
		t.Errorf("exact PID filter got %+v, want just PID 42", list)
	}
}

func TestCellTruncatesAndPads(t *testing.T) {
	got := cell("hello", 8, false, stPlain)
	if visLen(got) != 8 {
		t.Errorf("cell width = %d, want 8 (got %q)", visLen(got), got)
	}
	got = cell("a-very-long-name", 6, false, stPlain)
	if visLen(got) != 6 {
		t.Errorf("truncated cell width = %d, want 6 (got %q)", visLen(got), got)
	}
}

func TestUnitNameUnescapesSystemd(t *testing.T) {
	if got := unitName(`app\x2dworker.service`); got != "app-worker.service" {
		t.Errorf("unitName = %q, want %q", got, "app-worker.service")
	}
}
