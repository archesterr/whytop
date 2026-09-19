package collect

import "testing"

func TestUnitOf(t *testing.T) {
	cases := []struct {
		path     string
		wantUnit string
		wantUser bool
	}{
		{"/system.slice/nginx.service", "nginx.service", false},
		{"/kubepods.slice/kubepods-burstable.slice/cri-containerd-abc123.scope", "cri-containerd-abc123.scope", false},
		{"/user.slice/user-1000.slice/user@1000.service/app.slice/app-x.scope", "app-x.scope", true},
		{"/init.scope", "init.scope", false},
		{"", "", false},
	}
	for _, c := range cases {
		unit, isUser := unitOf(c.path)
		if unit != c.wantUnit || isUser != c.wantUser {
			t.Errorf("unitOf(%q) = (%q, %v), want (%q, %v)", c.path, unit, isUser, c.wantUnit, c.wantUser)
		}
	}
}

func TestContainerOf(t *testing.T) {
	const id = "4f8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b" // 64 hex chars
	cases := []struct {
		path        string
		wantID      string
		wantRuntime string
	}{
		{"/system.slice/docker-" + id + ".scope", id[:12], "docker"},
		{"/kubepods.slice/kubepods-burstable.slice/cri-containerd-" + id + ".scope", id[:12], "containerd"},
		{"/machine.slice/libpod-" + id + ".scope", id[:12], "podman"},
		{"/system.slice/crio-" + id + ".scope", id[:12], "cri-o"},
		{"/docker/" + id, id[:12], "docker"},
		{"/system.slice/nginx.service", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		gotID, gotRT := containerOf(c.path)
		if gotID != c.wantID || gotRT != c.wantRuntime {
			t.Errorf("containerOf(%q) = (%q, %q), want (%q, %q)", c.path, gotID, gotRT, c.wantID, c.wantRuntime)
		}
	}
}

func TestSub(t *testing.T) {
	if got := sub(10, 4); got != 6 {
		t.Errorf("sub(10,4) = %d, want 6", got)
	}
	if got := sub(4, 10); got != 0 {
		t.Errorf("sub(4,10) = %d, want 0 (counter reset/wrap must not underflow)", got)
	}
}

func TestRate(t *testing.T) {
	if got := rate(110, 100, 2); got != 5 {
		t.Errorf("rate(110,100,2) = %v, want 5", got)
	}
	if got := rate(50, 100, 2); got != 0 {
		t.Errorf("rate(50,100,2) = %v, want 0 on counter reset", got)
	}
}

func TestFinite(t *testing.T) {
	zero := 0.0
	if got := finite(zero / zero); got != 0 { // NaN
		t.Errorf("finite(NaN) = %v, want 0", got)
	}
	one := 1.0
	if got := finite(one / zero); got != 0 { // +Inf
		t.Errorf("finite(+Inf) = %v, want 0", got)
	}
	if got := finite(42.5); got != 42.5 {
		t.Errorf("finite(42.5) = %v, want 42.5", got)
	}
}

func TestSkipDevAndMount(t *testing.T) {
	for _, d := range []string{"loop0", "ram1", "fd3", "sr0"} {
		if !skipDev(d) {
			t.Errorf("skipDev(%q) = false, want true", d)
		}
	}
	if skipDev("sda") {
		t.Errorf("skipDev(sda) = true, want false")
	}
	if !skipMount("/var/lib/docker/overlay2/abc") {
		t.Errorf("skipMount(docker path) = false, want true")
	}
	if skipMount("/home") {
		t.Errorf("skipMount(/home) = true, want false")
	}
}
