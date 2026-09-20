package remote

import (
	"strings"
	"testing"
	"time"
)

// /proc/<pid>/stat's second field is the executable name in parentheses and
// may itself contain spaces and parentheses — "(Web Content)" is a real one
// on any box running Firefox. Splitting on whitespace shifts every field
// after it, which shows up as nonsense CPU times rather than as an error,
// so this is the parser's most important case.
func TestParseProcStatHandlesNamesWithSpacesAndParens(t *testing.T) {
	line := "4242 (Web Content) S 4200 4242 4242 0 -1 4194304 1234 0 0 0 " +
		"700 300 0 0 20 0 27 0 987654 123456789 5000 " +
		strings.Repeat("0 ", 30)
	ps, ok := parseProcStat(line)
	if !ok {
		t.Fatal("failed to parse")
	}
	if ps.name != "Web Content" {
		t.Errorf("name = %q, want %q", ps.name, "Web Content")
	}
	if ps.state != "S" {
		t.Errorf("state = %q, want S", ps.state)
	}
	if ps.ppid != 4200 {
		t.Errorf("ppid = %d, want 4200", ps.ppid)
	}
	if ps.utime != 700 || ps.stime != 300 {
		t.Errorf("cpu times = %d/%d, want 700/300", ps.utime, ps.stime)
	}
	if ps.threads != 27 {
		t.Errorf("threads = %d, want 27", ps.threads)
	}
	if ps.starttime != 987654 {
		t.Errorf("starttime = %d, want 987654", ps.starttime)
	}
	if ps.rssPages != 5000 {
		t.Errorf("rss = %d pages, want 5000", ps.rssPages)
	}
}

// `tail -n +1` is how the probe reads a thousand files in one process. Its
// framing is the whole contract, so it is pinned here.
func TestTailFilesSplitsOnHeaders(t *testing.T) {
	out := "==> /proc/1/stat <==\n1 (systemd) S 0 1\n\n==> /proc/2/stat <==\n2 (kthreadd) S 0 0\n"
	got := tailFiles(out)
	if len(got) != 2 {
		t.Fatalf("got %d files, want 2: %#v", len(got), got)
	}
	if got["/proc/1/stat"] != "1 (systemd) S 0 1" {
		t.Errorf("first file = %q", got["/proc/1/stat"])
	}
	if got["/proc/2/stat"] != "2 (kthreadd) S 0 0" {
		t.Errorf("second file = %q", got["/proc/2/stat"])
	}
}

// A login shell prints motd, profile chatter and warnings before the probe
// ever runs. Everything before the first marker has to be discarded, or the
// first section is whatever the server's banner happened to say.
func TestSectionsIgnoreLoginNoise(t *testing.T) {
	out := "Welcome to Ubuntu 24.04 LTS\n*** System restart required ***\n@@loadavg\n0.50 0.40 0.30 1/900 12345\n@@end\n"
	sec := sections(out)
	if _, ok := sec["loadavg"]; !ok {
		t.Fatalf("no loadavg section: %#v", sec)
	}
	l1, l5, l15 := parseLoad(sec["loadavg"])
	if l1 != 0.5 || l5 != 0.4 || l15 != 0.3 {
		t.Errorf("load = %v/%v/%v, want 0.5/0.4/0.3", l1, l5, l15)
	}
}

func TestParseOwnersReadsUsernameFromLsOutput(t *testing.T) {
	out := "dr-xr-xr-x 9 root     root 0 Sep 20 13:00 /proc/1\n" +
		"dr-xr-xr-x 9 www-data www-data 0 Sep 20 13:00 /proc/4242\n"
	got := parseOwners(out)
	if got[1] != "root" {
		t.Errorf("pid 1 owner = %q, want root", got[1])
	}
	if got[4242] != "www-data" {
		t.Errorf("pid 4242 owner = %q, want www-data", got[4242])
	}
}

func TestParseCmdlineJoinsNULSeparatedArgs(t *testing.T) {
	if got := parseCmdline("nginx\x00-g\x00daemon off;\x00"); got != "nginx -g daemon off;" {
		t.Errorf("cmdline = %q", got)
	}
	// A kernel thread has an empty cmdline, which is how it's told apart
	// from a userspace process.
	if got := parseCmdline(""); got != "" {
		t.Errorf("empty cmdline = %q, want empty", got)
	}
}

func TestParseMeminfo(t *testing.T) {
	var m struct{ done bool }
	_ = m
	body := "MemTotal:       32819752 kB\nMemFree:         1000000 kB\nMemAvailable:   20000000 kB\n" +
		"Buffers:          500000 kB\nCached:          8000000 kB\nSwapTotal:       2097148 kB\nSwapFree:        2097148 kB\n"
	var mem = newMem()
	parseMeminfo(body, mem)
	if mem.Total != 32819752*1024 {
		t.Errorf("total = %d", mem.Total)
	}
	if mem.Used != (32819752-20000000)*1024 {
		t.Errorf("used = %d, want total-available", mem.Used)
	}
	if mem.SwapPct != 0 {
		t.Errorf("swap unused should be 0%%, got %v", mem.SwapPct)
	}
}

// A whole probe, end to end, with two samples so the rates are exercised.
func TestBuildProducesRatesFromTwoSamples(t *testing.T) {
	c := &Client{}
	first := c.build(probeFixture(100, 1000), time.Now().Add(-2*time.Second))
	if len(first.Procs) != 2 {
		t.Fatalf("expected 2 processes, got %d", len(first.Procs))
	}
	// The first sample has no previous one to difference against, so there
	// is no rate yet — the same as top's first screen.
	if first.Procs[0].CPU != 0 {
		t.Errorf("first sample should report no CPU rate, got %v", first.Procs[0].CPU)
	}

	second := c.build(probeFixture(300, 5000), time.Now())
	var app *procView
	for i := range second.Procs {
		if second.Procs[i].PID == 4242 {
			app = &procView{second.Procs[i]}
		}
	}
	if app == nil {
		t.Fatal("pid 4242 missing from the second sample")
	}
	// 200 extra jiffies over ~2s at 100Hz is ~100% of one core.
	if app.CPU < 90 || app.CPU > 110 {
		t.Errorf("CPU = %v%%, want about 100%%", app.CPU)
	}
	if app.Name != "app" || app.User != "www-data" {
		t.Errorf("identity lost: %+v", app.Proc)
	}
	if app.Cmdline != "/usr/bin/app --serve" {
		t.Errorf("cmdline = %q", app.Cmdline)
	}
	if second.Host != "web01" || second.Kernel != "6.8.0-40-generic" {
		t.Errorf("meta = %q / %q", second.Host, second.Kernel)
	}
	if second.OS != "Ubuntu 24.04.1 LTS" {
		t.Errorf("os = %q", second.OS)
	}
	if len(app.Ports) != 1 || app.Ports[0] != 443 {
		t.Errorf("ports = %v, want [443]", app.Ports)
	}
}

// PID reuse: the same number, a different process. starttime is the
// identity token, and differencing against the old one would invent a
// spike out of nowhere.
func TestBuildIgnoresAReusedPID(t *testing.T) {
	c := &Client{}
	c.build(probeFixture(100, 1000), time.Now().Add(-2*time.Second))
	reused := strings.Replace(probeFixture(999999, 5000), "987654", "111111", 1)
	s := c.build(reused, time.Now())
	for _, p := range s.Procs {
		if p.PID == 4242 && p.CPU != 0 {
			t.Errorf("a reused PID should report no rate, got %v%%", p.CPU)
		}
	}
}
