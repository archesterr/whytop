package actions

import (
	"os"
	"strconv"
	"syscall"
	"testing"
)

func TestSignalRefusesUnsafeTargets(t *testing.T) {
	cases := []struct {
		name string
		pid  int32
	}{
		{"pid 1 (init)", 1},
		{"pid 0", 0},
		{"negative pid", -5},
		{"self", int32(os.Getpid())},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Signal(c.pid, syscall.SIGTERM); err == nil {
				t.Fatalf("Signal(%d) = nil, want refusal", c.pid)
			}
		})
	}
}

func TestOOMRegexParsesRealKernelMessageVariants(t *testing.T) {
	cases := []struct {
		line     string
		wantPID  int32
		wantName string
	}{
		{"Out of memory: Killed process 12345 (nginx) total-vm:123456kB, anon-rss:98765kB", 12345, "nginx"},
		{"Memory cgroup out of memory: Killed process 987 (java) score 1000 or sacrifice child", 987, "java"},
		{"oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=/,mems_allowed=0,oom_memcg=/,task_memcg=/,task=python3,pid=42,uid=0", 0, ""}, // no "Killed process N (name)" here, not matched by design
	}
	for _, c := range cases {
		m := oomRe.FindStringSubmatch(c.line)
		if c.wantPID == 0 {
			if m != nil {
				t.Errorf("line %q: expected no match, got %v", c.line, m)
			}
			continue
		}
		if m == nil {
			t.Fatalf("line %q: expected a match, got none", c.line)
		}
		pid, _ := strconv.Atoi(m[1])
		if int32(pid) != c.wantPID || m[2] != c.wantName {
			t.Errorf("line %q: got pid=%d name=%q, want pid=%d name=%q", c.line, pid, m[2], c.wantPID, c.wantName)
		}
	}
}

func TestCanRestart(t *testing.T) {
	cases := []struct {
		name    string
		unit    string
		isUser  bool
		wantErr bool
	}{
		{"no unit", "", false, true},
		{"user unit", "app.service", true, true},
		{"session manager", "user@1000.service", false, true},
		{"scope, not a service", "session-3.scope", false, true},
		{"restartable system service", "nginx.service", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CanRestart(c.unit, c.isUser)
			if (err != nil) != c.wantErr {
				t.Fatalf("CanRestart(%q, %v) = %v, wantErr %v", c.unit, c.isUser, err, c.wantErr)
			}
		})
	}
}
