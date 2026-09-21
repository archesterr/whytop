package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// State is what "remind me later" and "no" have to survive a restart for.
// Without it, "no" means "no, until you open whytop again in ten seconds",
// which trains people to stop reading the prompt.
type State struct {
	// Skipped is a version the operator said no to. They are not asked
	// about it again; a later release asks afresh, because "not this one"
	// is not "never again".
	Skipped string `json:"skipped,omitempty"`
	// RemindAfter is when a postponed prompt may come back.
	RemindAfter time.Time `json:"remind_after,omitempty"`
	// LastCheck rate-limits the network call itself.
	LastCheck time.Time `json:"last_check,omitempty"`
}

// RemindInterval is how long "remind me later" defers for. A day is the
// span that makes it a different working session rather than a different
// coffee, which is the difference between postponing and dismissing.
const RemindInterval = 24 * time.Hour

// CheckInterval bounds how often whytop asks GitHub anything at all.
const CheckInterval = 12 * time.Hour

func statePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "whytop", "update.json"), nil
}

// LoadState reads the saved state. A missing or unreadable file is not an
// error worth surfacing — it means the defaults, which is asking.
func LoadState() State {
	p, err := statePath()
	if err != nil {
		return State{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}
	}
	return s
}

// SaveState persists a decision. It is written through a temporary file in
// the same directory so a crash mid-write cannot leave behind a truncated
// file that then fails to parse forever after.
func SaveState(s State) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".update-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Due reports whether a release should be offered, given what the operator
// has already said about it.
func (s State) Due(rel string, now time.Time) bool {
	if rel == "" {
		return false
	}
	// A skipped version stays skipped, but only that one. IsNewer is what
	// distinguishes "the release they declined" from "one published since",
	// so a plain string match is not enough: skipping 3.3.0 must not hide
	// 3.4.0, and must still hide 3.3.0 when the tag is spelled differently.
	if s.Skipped != "" && !IsNewer(s.Skipped, rel) {
		return false
	}
	return !now.Before(s.RemindAfter)
}
