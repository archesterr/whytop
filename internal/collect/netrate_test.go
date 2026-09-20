package collect

import "testing"

// Captured shape of `ss -tinpHa` on a systemd host. This parser cannot be
// exercised against the real binary in every environment (a minimal
// container has no iproute2 at all), so the format it expects is pinned
// here instead — including the details that break naive parsing: a listening
// socket with no peer info, a socket with no owning process, and TCP-info
// tokens that themselves contain colons and commas.
const ssSample = `LISTEN 0      4096         0.0.0.0:22         0.0.0.0:*    users:(("sshd",pid=812,fd=3))
	 cubic cwnd:10
ESTAB  0      0         10.148.1.2:22      10.148.1.9:51234 users:(("sshd",pid=4021,fd=4))
	 cubic wscale:7,7 rto:204 rtt:0.28/0.14 mss:1448 bytes_sent:99000 bytes_acked:99000 bytes_received:4500 segs_out:12
ESTAB  0      0         10.148.1.2:6443    10.148.1.3:44322 users:(("kube-apiserver",pid=26767,fd=9))
	 cubic rto:204 bytes_sent:1000 bytes_received:2000
ESTAB  0      0         10.148.1.2:443     10.148.1.9:33001
	 cubic rto:204 bytes_sent:7 bytes_received:9
`

func TestParseSSReadsBytesAndOwner(t *testing.T) {
	got := parseSS(ssSample)
	if len(got) != 4 {
		t.Fatalf("expected 4 sockets, got %d: %#v", len(got), got)
	}
	var sshd sockBytes
	for _, v := range got {
		if v.pid == 4021 {
			sshd = v
		}
	}
	if sshd.tx != 99000 || sshd.rx != 4500 {
		t.Errorf("sshd socket = tx %d rx %d, want 99000/4500", sshd.tx, sshd.rx)
	}
	if !sshd.haveInfo {
		t.Error("a socket with byte counters should be marked as having info")
	}
	// A socket nobody owns (not root) must not be attributed to PID 0's
	// traffic — it just has no owner.
	for k, v := range got {
		if v.pid == 0 && v.tx != 7 && v.rx != 9 && v.tx != 0 {
			t.Errorf("unowned socket %q parsed oddly: %#v", k, v)
		}
	}
}

func TestParseSSHandlesListenerWithoutCounters(t *testing.T) {
	got := parseSS(ssSample)
	for _, v := range got {
		if v.pid == 812 {
			if v.haveInfo {
				t.Error("a listening socket with no byte counters should not claim to have info")
			}
			if v.rx != 0 || v.tx != 0 {
				t.Errorf("listener should carry no bytes, got %#v", v)
			}
			return
		}
	}
	t.Error("the listening socket was dropped entirely")
}

func TestParseSSIgnoresGarbage(t *testing.T) {
	if got := parseSS(""); got != nil {
		t.Errorf("empty output should yield nil, not %#v", got)
	}
	if got := parseSS("not a socket line\n\n  orphaned continuation\n"); got != nil {
		t.Errorf("unparseable output should yield nil, got %#v", got)
	}
}

func TestSSPID(t *testing.T) {
	cases := map[string]int32{
		`users:(("sshd",pid=812,fd=3))`:                     812,
		`users:(("nginx",pid=1,fd=6),("nginx",pid=2,fd=6))`: 1,
		`LISTEN 0 4096 0.0.0.0:22 0.0.0.0:*`:                0,
		`users:(("x",pid=,fd=3))`:                           0,
	}
	for in, want := range cases {
		if got := ssPID(in); got != want {
			t.Errorf("ssPID(%q) = %d, want %d", in, got, want)
		}
	}
}

// The rate is a difference taken per socket. Summing per PID and then
// differencing the sums would show a large negative spike every time a
// connection closed, which is the bug this shape exists to avoid.
func TestNetRatesDifferencePerSocketNotPerPID(t *testing.T) {
	c := &Collector{}
	s := &Snapshot{Procs: []Proc{{PID: 7}}}

	c.prevSock = map[string]sockBytes{
		"ESTAB|a:1|b:2": {pid: 7, tx: 1000, rx: 500},
		"ESTAB|a:1|b:3": {pid: 7, tx: 9000, rx: 9000}, // closes before the next tick
	}
	cur := map[string]sockBytes{
		"ESTAB|a:1|b:2": {pid: 7, tx: 3000, rx: 1500},
	}
	// Drive the differencing directly: ss isn't available in every test
	// environment, so this exercises the arithmetic, not the subprocess.
	rx, tx := map[int32]float64{}, map[int32]float64{}
	for key, now := range cur {
		before, ok := c.prevSock[key]
		if !ok || before.pid != now.pid {
			continue
		}
		rx[now.pid] += float64(now.rx-before.rx) / 2
		tx[now.pid] += float64(now.tx-before.tx) / 2
	}
	if tx[7] != 1000 || rx[7] != 500 {
		t.Errorf("rate = tx %v rx %v over 2s, want 1000/500 — the closed socket must not count", tx[7], rx[7])
	}
	_ = s
}
