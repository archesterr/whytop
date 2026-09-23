package collect

import "testing"

func TestCPUCgroupFindsTheCPUController(t *testing.T) {
	v1 := "4:memory:/a\n3:cpu,cpuacct:/kubepods/pod1/abc\n0::/"
	if p, v2 := cpuCgroup([]byte(v1)); p != "/kubepods/pod1/abc" || v2 {
		t.Errorf("v1: got %q v2=%v", p, v2)
	}
	if p, v2 := cpuCgroup([]byte("0::/system.slice/nginx.service\n")); p != "/system.slice/nginx.service" || !v2 {
		t.Errorf("v2: got %q v2=%v", p, v2)
	}
}

func TestTCPExtCountsListenDropsOnce(t *testing.T) {
	in := "TcpExt: SyncookiesSent ListenOverflows ListenDrops\nTcpExt: 0 40 42\nIpExt: A\nIpExt: 1\n"
	if got := tcpExt(in); got != 42 {
		t.Errorf("got %d, want 42: ListenDrops already includes the overflows", got)
	}
	if got := tcpExt("garbage"); got != 0 {
		t.Errorf("got %d from garbage", got)
	}
}

func TestFDLimitReadsTheSoftLimit(t *testing.T) {
	in := "Limit                     Soft Limit           Hard Limit           Units\nMax open files            1024                 524288               files\n"
	if got := fdLimit(in); got != 1024 {
		t.Errorf("got %d", got)
	}
	if got := fdLimit("Max open files            unlimited            unlimited            files\n"); got != 0 {
		t.Errorf("unlimited read as %d, want 0 (no limit)", got)
	}
}
