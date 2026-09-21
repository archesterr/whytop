package actions

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
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
			if err := Signal(c.pid, syscall.SIGTERM, time.Time{}); err == nil {
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

// A descriptor action reaches into a running process, so the guards matter
// more than the happy path: PID 1 and whytop's own descriptors are never
// valid targets, and a descriptor is always a number.
func TestFDActionsRefuseDangerousTargets(t *testing.T) {
	self := int32(os.Getpid())
	cases := []struct {
		name string
		pid  int32
		fd   string
	}{
		{"init", 1, "3"},
		{"kernel", 0, "3"},
		{"whytop itself", self, "3"},
		{"not a number", 4242, "abc"},
		{"negative", 4242, "-1"},
	}
	for _, c := range cases {
		if err := CloseFD(c.pid, c.fd); err == nil {
			t.Errorf("CloseFD accepted %s (pid=%d fd=%q)", c.name, c.pid, c.fd)
		}
		if err := TruncateFD(c.pid, c.fd, ""); err == nil {
			t.Errorf("TruncateFD accepted %s (pid=%d fd=%q)", c.name, c.pid, c.fd)
		}
	}
}

// Truncating frees the blocks either way, but what the operator sees
// afterwards depends on how the writer opened the file: an appender's next
// write starts at zero, a plain writer's resumes at the offset it held and
// leaves a hole, so ls -l keeps reporting the old size long after the space
// came back. whytop has to know which, to say which.
func TestFDAppendsReadsTheOpenFlags(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name  string
		flags int
		want  bool
	}{
		{"append", os.O_WRONLY | os.O_CREATE | os.O_APPEND, true},
		{"plain write", os.O_WRONLY | os.O_CREATE, false},
		{"read-write, no append", os.O_RDWR | os.O_CREATE, false},
	} {
		f, err := os.OpenFile(filepath.Join(dir, c.name), c.flags, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if got := FDAppends(int32(os.Getpid()), strconv.Itoa(int(f.Fd()))); got != c.want {
			t.Errorf("%s: FDAppends = %v, want %v", c.name, got, c.want)
		}
		f.Close()
	}
}

// A descriptor that is gone, or a kernel without fdinfo, must not produce a
// warning about sparseness that nobody can act on.
func TestFDAppendsSaysNothingWhenItCannotTell(t *testing.T) {
	if !FDAppends(int32(os.Getpid()), "99999") {
		t.Error("a missing descriptor produced a sparse-file warning")
	}
}
