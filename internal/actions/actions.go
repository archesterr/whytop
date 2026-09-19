package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func Signal(pid int32, sig syscall.Signal) error {
	switch {
	case pid <= 1:
		return fmt.Errorf("refusing to signal PID %d", pid)
	case int(pid) == os.Getpid():
		return errors.New("refusing to signal whytop itself")
	}
	if err := syscall.Kill(int(pid), sig); err != nil {
		return fmt.Errorf("kill %d: %w", pid, err)
	}
	return nil
}

func CanRestart(unit string, user bool) error {
	switch {
	case unit == "":
		return errors.New("process is not part of a systemd unit")
	case user:
		return fmt.Errorf("%s is a user unit: run 'systemctl --user restart' as that user", unit)
	case strings.HasPrefix(unit, "user@"):
		return fmt.Errorf("refusing to restart session manager %s", unit)
	case !strings.HasSuffix(unit, ".service"):
		return fmt.Errorf("%s is a scope (container/session), not restartable", unit)
	}
	return nil
}

func RestartUnit(unit string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "restart", unit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart %s: %v %s", unit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// UnitFragmentPath returns the on-disk unit file for a systemd unit, so it
// can be opened in an editor — a transient or kernel-generated unit (no
// FragmentPath) can't be edited this way, which is reported as an error
// rather than silently opening an empty file.
func UnitFragmentPath(unit string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", unit, "--no-pager", "-p", "FragmentPath").Output()
	if err != nil {
		return "", fmt.Errorf("systemctl show %s: %w", unit, err)
	}
	_, path, _ := strings.Cut(strings.TrimSpace(string(out)), "=")
	if path == "" {
		return "", fmt.Errorf("%s has no unit file on disk (transient or kernel-generated)", unit)
	}
	return path, nil
}

// DaemonReload runs `systemctl daemon-reload` — required after editing a
// unit file for the change to take effect, and never run automatically:
// the caller always confirms with the user first.
func DaemonReload() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "daemon-reload").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl daemon-reload: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func UnitStatus(unit string) map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "systemctl", "show", unit, "--no-pager",
		"-p", "ActiveState", "-p", "SubState", "-p", "MainPID",
		"-p", "NRestarts", "-p", "MemoryCurrent").Output()
	m := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			m[k] = v
		}
	}
	return m
}

// OOMKill is one kernel out-of-memory kill event.
type OOMKill struct {
	PID  int32
	Name string
}

var oomRe = regexp.MustCompile(`[Kk]illed process (\d+) \(([^)]+)\)`)

// RecentOOMKills scans the kernel log for OOM-killer events since the given
// time. top, htop and iotop don't surface this at all — the only way to
// find out a process was OOM-killed is to separately dig through dmesg or
// journalctl -k after the fact, disconnected from whatever monitoring
// session was open when it happened.
func RecentOOMKills(since time.Time) []OOMKill {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", "-k", "--no-pager", "-o", "cat",
		"--since", since.Format("2006-01-02 15:04:05"),
		"-g", "Out of memory|Killed process").Output()
	if err != nil {
		return nil
	}
	var kills []OOMKill
	for _, line := range strings.Split(string(out), "\n") {
		m := oomRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		pid, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		kills = append(kills, OOMKill{PID: int32(pid), Name: m[2]})
	}
	return kills
}

// Journal returns the last lines for a service, or for the PID when the
// process is not in a service (scopes, containers, shells).
func Journal(unit string, user bool, pid int32, lines int) string {
	args := []string{"--no-pager", "-o", "short-iso", "-n", strconv.Itoa(lines)}
	switch {
	case strings.HasSuffix(unit, ".service") && user:
		args = append(args, "_SYSTEMD_USER_UNIT="+unit)
	case strings.HasSuffix(unit, ".service"):
		args = append(args, "-u", unit)
	default:
		args = append(args, fmt.Sprintf("_PID=%d", pid))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	switch {
	case err != nil && text == "":
		return "journalctl: " + err.Error()
	case text == "" || text == "-- No entries --":
		return "no journal entries"
	}
	return text
}

// TruncateFD empties a file a process still holds open, without closing it.
//
// This is the fix for the classic "df says the disk is full but du can't find
// anything to delete": a file that was unlinked while a process still had it
// open, whose space the kernel won't reclaim until the last descriptor on it
// goes away. Truncating through /proc/<pid>/fd/<n> returns the space straight
// away and leaves the descriptor valid, so the process keeps running. It is
// much safer than closing the descriptor, and it is what you actually want
// nine times out of ten.
func TruncateFD(pid int32, fd string) error {
	if err := checkFDTarget(pid, fd); err != nil {
		return err
	}
	path := fmt.Sprintf("/proc/%d/fd/%s", pid, fd)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("truncate %s: %w", path, err)
	}
	return f.Close()
}

// CloseFD closes a file descriptor inside a running process.
//
// The kernel deliberately offers no syscall for "close descriptor N in
// process P": the owning process has no idea the descriptor vanished and will
// get EBADF the next time it touches it, which can wedge it, crash it, or
// corrupt whatever it was midway through writing. The only way to do it is to
// attach a debugger and call close() in the target's own context, which is
// what this shells out to gdb for. Callers must confirm with the user first,
// and should offer TruncateFD instead wherever it would do.
func CloseFD(pid int32, fd string) error {
	if err := checkFDTarget(pid, fd); err != nil {
		return err
	}
	if _, err := exec.LookPath("gdb"); err != nil {
		return errors.New("closing a descriptor in a running process needs gdb installed — the kernel has no syscall for it")
	}
	n, _ := strconv.Atoi(fd)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gdb", "-p", strconv.Itoa(int(pid)), "--batch",
		"-ex", fmt.Sprintf("call (int)close(%d)", n)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gdb close(%d) on PID %d: %v", n, pid, err)
	}
	if strings.Contains(string(out), "ptrace:") {
		return errors.New("gdb could not attach — needs root, or /proc/sys/kernel/yama/ptrace_scope is restricting it")
	}
	return nil
}

// checkFDTarget refuses the descriptors that must never be touched: PID 1's,
// whytop's own (closing those breaks the tool doing the closing), and
// anything that isn't a plain descriptor number.
func checkFDTarget(pid int32, fd string) error {
	switch {
	case pid <= 1:
		return fmt.Errorf("refusing to touch a descriptor of PID %d", pid)
	case int(pid) == os.Getpid():
		return errors.New("refusing to touch whytop's own descriptors")
	}
	if n, err := strconv.Atoi(fd); err != nil || n < 0 {
		return fmt.Errorf("not a descriptor number: %q", fd)
	}
	return nil
}
