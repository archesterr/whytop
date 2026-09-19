package tui

import (
	"strings"
	"testing"

	"github.com/archesterr/whytop/internal/collect"
)

// A host with no systemd is the normal case inside a container, and it used
// to leave the Units tab on "Reading systemd units…" forever — which reads as
// a hang rather than as an answer.
func TestUnitsTabSaysWhySystemdIsMissing(t *testing.T) {
	// Collected, but with a reason and no units: the container case.
	snap := &collect.Snapshot{UnitsCollected: true, UnitsErr: "No systemctl on this host."}
	out := stripANSI(model{snap: snap}.renderUnits(100, 10))
	if strings.Contains(out, "Reading systemd units") {
		t.Errorf("a host with no systemd should not look like it's still loading:\n%s", out)
	}
	if !strings.Contains(out, "No systemctl on this host.") {
		t.Errorf("the reason should be shown:\n%s", out)
	}
	if !strings.Contains(out, "container") {
		t.Errorf("the container case is common enough to point at:\n%s", out)
	}

	// Genuinely still loading: the waiting message is still right.
	pending := stripANSI(model{snap: &collect.Snapshot{}}.renderUnits(100, 10))
	if !strings.Contains(pending, "Reading systemd units") {
		t.Errorf("before the first collect it really is still loading:\n%s", pending)
	}
}
