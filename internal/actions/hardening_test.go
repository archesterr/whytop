package actions

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Unit names arrive from cgroup paths and systemctl output, not from a human
// typing them, and become argv for systemctl. Nothing runs through a shell,
// so the risk isn't command injection — it's a name that systemctl reads as
// an option instead of a unit.
func TestUnitNamesAreValidated(t *testing.T) {
	good := []string{
		"nginx.service", "user@1000.service", "systemd-udevd.service",
		"dbus.socket", "dev-disk-by\\x2duuid.device",
	}
	for _, u := range good {
		if err := validUnit(u); err != nil {
			t.Errorf("validUnit(%q) rejected a real unit name: %v", u, err)
		}
	}

	bad := []string{
		"--user", "-H", "", "no-extension",
		"nginx.service\nrm -rf /", "nginx.service;reboot", "a b.service",
		strings.Repeat("x", 300) + ".service",
	}
	for _, u := range bad {
		if err := validUnit(u); err == nil {
			t.Errorf("validUnit(%q) accepted a name it should refuse", u)
		}
	}
}

func TestPrivilegedCallsRefuseBadUnitNames(t *testing.T) {
	const evil = "--version"
	if err := RestartUnit(evil); err == nil {
		t.Error("RestartUnit accepted an option-shaped unit name")
	}
	if _, err := UnitFragmentPath(evil); err == nil {
		t.Error("UnitFragmentPath accepted an option-shaped unit name")
	}
	if got := UnitStatus(evil); len(got) != 0 {
		t.Errorf("UnitStatus ran with an option-shaped unit name: %v", got)
	}
	// Journal falls back to the PID filter rather than passing it on.
	if out := Journal(evil, false, int32(os.Getpid()), 1); strings.Contains(out, "Usage") {
		t.Error("Journal passed an option-shaped unit name to journalctl")
	}
}

// Between drawing a row and confirming a kill, a PID can be recycled. The
// start time is what distinguishes the process the operator chose from
// whatever inherited its number.
func TestSignalRefusesARecycledPID(t *testing.T) {
	self := int32(os.Getpid())
	start, err := ProcStart(self)
	if err != nil {
		t.Fatalf("ProcStart on our own PID failed: %v", err)
	}
	if time.Since(start) < 0 || time.Since(start) > 24*time.Hour {
		t.Errorf("ProcStart returned an implausible start time: %v", start)
	}

	// Signal 0 with the true start time gets past the identity check (and
	// is then refused for being whytop itself, which is the next guard).
	if err := Signal(self, 0, start); err == nil || !strings.Contains(err.Error(), "itself") {
		t.Errorf("with a matching start time, expected the self-signal guard, got %v", err)
	}

	// A start time from a different process must not pass.
	wrong := start.Add(-time.Hour)
	err = Signal(999999, 0, wrong)
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("a stale PID should be reported as gone, got %v", err)
	}
}

// A descriptor can be closed and reopened between the confirm and the act,
// so the target is re-checked against what was actually shown.
func TestTruncateRefusesWhenTheDescriptorMoved(t *testing.T) {
	// Descriptor 1 exists in every process; the point is that the target it
	// actually points at will not match the one claimed here.
	err := TruncateFD(int32(os.Getpid()), "1", "/definitely/not/where/this/points")
	if err == nil {
		t.Fatal("truncate proceeded even though the descriptor pointed elsewhere")
	}
	if !strings.Contains(err.Error(), "now points at") && !strings.Contains(err.Error(), "whytop's own") {
		t.Errorf("expected a moved-descriptor or self refusal, got %v", err)
	}
}
