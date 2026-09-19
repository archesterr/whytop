package actions

import (
	"os"
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
