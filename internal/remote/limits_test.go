package remote

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ceilingsFixture is the probe's later sections, in the shape dash and mawk
// produce them — captured from a real run and trimmed. n advances every
// counter so two samples give rates.
func ceilingsFixture(n int) string {
	return fmt.Sprintf(`@@limits
==> /proc/sys/fs/file-nr <==
9600 0 10000

==> /proc/sys/net/netfilter/nf_conntrack_count <==
900

==> /proc/sys/net/netfilter/nf_conntrack_max <==
1000
@@snmp
Tcp: RtoAlgorithm ActiveOpens PassiveOpens CurrEstab InErrs OutSegs RetransSegs OutRsts
Tcp: 1 0 0 12 0 %d %d 0
TcpExt: SyncookiesSent ListenOverflows ListenDrops
TcpExt: 0 %d %d
@@listen
   0: 00000000:01BB 00000000:0000 0A 00000000:00000081 00:00000000 00000000     0        0 1
@@cgroups
4242 cpu,cpuacct /kubepods/pod-a/app
4242  /
1 cpu,cpuacct /
@@fds
4242 1000
1 50
@@cpustat
==> /sys/fs/cgroup/cpu,cpuacct/kubepods/pod-a/app/cpu.cfs_period_us <==
100000

==> /sys/fs/cgroup/cpu,cpuacct/kubepods/pod-a/app/cpu.cfs_quota_us <==
50000

==> /sys/fs/cgroup/cpu,cpuacct/kubepods/pod-a/app/cpu.stat <==
nr_periods %d
nr_throttled %d
throttled_time 1
@@proclimits
==> /proc/4242/limits <==
Limit                     Soft Limit           Hard Limit           Units
Max open files            1024                 4096                 files
@@end
`, 1000*n+1000, 10*n, 20*n, 20*n, 100*n, 80*n)
}

func TestTheCeilingsReachARemoteHost(t *testing.T) {
	c := &Client{}
	fixture := func(jiffies, n int) string {
		base := probeFixture(jiffies, 1000)
		base = strings.TrimSuffix(base, "@@end\n")
		return base + strings.TrimPrefix(ceilingsFixture(n), "")
	}
	c.build(fixture(100, 1), time.Now().Add(-2*time.Second))
	s := c.build(fixture(300, 3), time.Now())

	if s.Limits.FilesUsed != 9600 || s.Limits.FilesMax != 10000 {
		t.Errorf("file handles: %d of %d", s.Limits.FilesUsed, s.Limits.FilesMax)
	}
	if s.Limits.ConntrackUsed != 900 || s.Limits.ConntrackMax != 1000 {
		t.Errorf("conntrack: %d of %d", s.Limits.ConntrackUsed, s.Limits.ConntrackMax)
	}
	// 40 more drops over two seconds.
	if got := s.Limits.ListenOverflowPs; got < 19 || got > 21 {
		t.Errorf("listen drops: %.1f/s, want 20", got)
	}
	if len(s.Limits.FullListeners) != 1 || s.Limits.FullListeners[0] != 443 {
		t.Errorf("listeners with a queue: %v, want [443]", s.Limits.FullListeners)
	}
	if !s.TCP.Available || s.TCP.RetransPct != 1 {
		t.Errorf("tcp: available=%v retrans=%.2f%%, want 1%%", s.TCP.Available, s.TCP.RetransPct)
	}
	app := s.Procs[s.ByPID[4242]]
	if app.FDs != 1000 || app.FDLimit != 1024 {
		t.Errorf("app fds: %d of %d", app.FDs, app.FDLimit)
	}
	// 160 of 200 new periods throttled.
	if app.Throttled != 80 {
		t.Errorf("app throttled %.1f%%, want 80", app.Throttled)
	}
	if len(s.Throttles) != 1 || s.Throttles[0].Quota != 0.5 || s.Throttles[0].PID != 4242 {
		t.Errorf("throttles: %+v", s.Throttles)
	}
	if init := s.Procs[s.ByPID[1]]; init.Throttled != 0 || init.FDLimit != 0 {
		t.Errorf("the root cgroup has no quota to hit, and 50 fds needs no limit read: %+v", init)
	}
}

func TestRemoteSocketsReadLikeLocalOnes(t *testing.T) {
	c := &Client{}
	out := strings.Replace(probeFixture(100, 1000), "@@end", `CLOSE-WAIT 1 0 10.0.0.1:443 10.0.0.9:5555 users:(("app",pid=4242,fd=9))
ESTAB 0 0 10.0.0.1:443 10.0.0.8:4444 users:(("app",pid=4242,fd=8))
@@end`, 1)
	s := c.build(out, time.Now())
	states := map[string]string{}
	for _, cn := range s.Conns {
		states[cn.State] = cn.Remote
	}
	if states["CLOSE_WAIT"] != "10.0.0.9:5555" || states["ESTABLISHED"] != "10.0.0.8:4444" {
		t.Errorf("ss states and peers should read the way the local collector reports them: %v", states)
	}
	if r, ok := states["LISTEN"]; !ok || r != "" {
		t.Errorf("a listener has no peer: %v", states)
	}
}
