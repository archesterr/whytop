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
	"sync"
	"syscall"
	"time"
)

func Signal(pid int32, sig syscall.Signal, expectStart time.Time) error {
	switch {
	case pid <= 1:
		return fmt.Errorf("refusing to signal PID %d", pid)
	case int(pid) == os.Getpid():
		return errors.New("refusing to signal whytop itself")
	}
	if err := stillSameProcess(pid, expectStart); err != nil {
		return err
	}
	audit("send %v to PID %d", sig, pid)
	if err := syscall.Kill(int(pid), sig); err != nil {
		return fmt.Errorf("kill %d: %w", pid, err)
	}
	return nil
}

// validUnit guards every unit name before it becomes a command-line
// argument. Nothing here runs through a shell, so there is no command
// injection to worry about — but a name beginning with "-" is read by
// systemctl as a flag rather than a unit, and unit names reach whytop from
// cgroup paths and systemctl output rather than from a human typing them.
// systemd's own charset for unit names is narrow, so matching it exactly
// costs nothing and closes the question. It allows the "@" of a template
// instance and the "\x2d" style escapes that appear in device and mount
// units.
var unitRe = regexp.MustCompile(`^[A-Za-z0-9:_.@\\-]{1,240}\.[a-z]+$`)

func validUnit(unit string) error {
	switch {
	case unit == "":
		return errors.New("no unit")
	case len(unit) > 256:
		return errors.New("unit name is implausibly long")
	case strings.HasPrefix(unit, "-"):
		return fmt.Errorf("refusing unit name %q: systemctl would read it as an option", unit)
	case !unitRe.MatchString(unit):
		return fmt.Errorf("refusing unit name %q: not a valid systemd unit name", unit)
	}
	return nil
}

func CanRestart(unit string, user bool) error {
	if err := validUnit(unit); err != nil && unit != "" {
		return err
	}
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
	if err := validUnit(unit); err != nil {
		return err
	}
	audit("restart unit %s", unit)
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
	if err := validUnit(unit); err != nil {
		return "", err
	}
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
	audit("systemctl daemon-reload")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "daemon-reload").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl daemon-reload: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func UnitStatus(unit string) map[string]string {
	if validUnit(unit) != nil {
		return map[string]string{}
	}
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
	if lines < 1 || lines > 10000 {
		lines = 100
	}
	args := []string{"--no-pager", "-o", "short-iso", "-n", strconv.Itoa(lines)}
	if unit != "" && validUnit(unit) != nil {
		unit = "" // fall back to the PID filter rather than passing it on
	}
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
func TruncateFD(pid int32, fd, expectTarget string) error {
	if err := checkFDTarget(pid, fd); err != nil {
		return err
	}
	path := fmt.Sprintf("/proc/%d/fd/%s", pid, fd)

	// The descriptor must still point where it did when the operator was
	// shown it and said yes. /proc/<pid>/fd/<n> is a live view of what the
	// process has open right now, and it is free to close and reopen that
	// number in between — so without this the thing truncated is not
	// necessarily the thing that was confirmed.
	if expectTarget != "" {
		switch target, err := os.Readlink(path); {
		case err != nil:
			return fmt.Errorf("descriptor %s of PID %d is gone", fd, pid)
		case target != expectTarget:
			return fmt.Errorf("descriptor %s of PID %d now points at %s, not %s — nothing was changed", fd, pid, target, expectTarget)
		}
	}

	if err := ownerCouldWrite(pid, path); err != nil {
		return err
	}
	audit("empty descriptor %s of PID %d (%s)", fd, pid, expectTarget)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("truncate %s: %w", path, err)
	}
	return f.Close()
}

// ownerCouldWrite refuses to truncate anything the process's own user could
// not have truncated themselves.
//
// Opening /proc/<pid>/fd/<n> re-opens the *target file* with whytop's
// credentials, not the descriptor's. Running as root that turns any
// descriptor a local user happens to hold into a root write: a user who
// opens a root-owned, world-readable file read-only — /etc/passwd, say — and
// then asks whytop to empty it gets exactly the privilege escalation this
// tool must never hand out. So the check applied is the one the kernel would
// have applied to the descriptor's owner, not to us.
func ownerCouldWrite(pid int32, fdPath string) error {
	var st syscall.Stat_t
	if err := syscall.Stat(fdPath, &st); err != nil {
		return fmt.Errorf("cannot inspect the descriptor's target: %w", err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return errors.New("only a regular file can be emptied — this descriptor points at a device, pipe, socket or directory")
	}
	uid, err := procUID(pid)
	if err != nil {
		return err
	}
	if uid == 0 {
		return nil // already root's own process; nothing is being escalated
	}
	// Deliberately conservative: owner-writable or world-writable only.
	// Checking group access would need the process's full supplementary
	// group list, and wrongly refusing a group-writable file is a far better
	// failure than truncating one its owner could never have touched.
	if (st.Uid == uid && st.Mode&0o200 != 0) || st.Mode&0o002 != 0 {
		return nil
	}
	return fmt.Errorf("refusing: PID %d runs as uid %d, which has no write access to that file — emptying it through whytop would be an escalation", pid, uid)
}

// procUID reads a process's effective UID from the kernel rather than taking
// it from the caller, so it can't be spoofed by anything on screen.
func procUID(pid int32) (uint32, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, fmt.Errorf("cannot read the owner of PID %d: %w", pid, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		if f := strings.Fields(line); len(f) >= 3 {
			n, err := strconv.ParseUint(f[2], 10, 32) // effective UID
			if err == nil {
				return uint32(n), nil
			}
		}
		break
	}
	return 0, fmt.Errorf("cannot determine the owner of PID %d", pid)
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
	audit("close descriptor %s of PID %d via gdb", fd, pid)
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

// stillSameProcess refuses to act on a PID that has been recycled since the
// operator looked at it.
//
// Between whytop drawing a row and the operator confirming a kill, the
// process can exit and the kernel can hand that number to something else —
// on a box churning through short-lived processes that window is not
// theoretical. Signalling then hits whatever inherited the number, which is
// the worst possible outcome for a tool whose whole job is to be sure about
// what it's touching. A process's start time is the standard identity token
// for this: it never changes, and a new process with the same PID will have
// a different one.
func stillSameProcess(pid int32, expectStart time.Time) error {
	if expectStart.IsZero() {
		return nil // caller has no reference point; nothing to compare against
	}
	got, err := ProcStart(pid)
	if err != nil {
		return fmt.Errorf("PID %d is gone", pid)
	}
	// Whole seconds: the start time is derived from boot time plus clock
	// ticks, so the two readings can differ in the sub-second remainder
	// without being different processes.
	if got.Unix() != expectStart.Unix() {
		return fmt.Errorf("PID %d is no longer the process you selected — it exited and the number was reused. Nothing was signalled", pid)
	}
	return nil
}

// ProcStart reads a process's start time from /proc/<pid>/stat field 22,
// which counts clock ticks since boot.
func ProcStart(pid int32) (time.Time, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return time.Time{}, err
	}
	// The comm field is parenthesised and may itself contain spaces and
	// brackets, so fields are counted from after the final ')'.
	line := string(b)
	i := strings.LastIndexByte(line, ')')
	if i < 0 {
		return time.Time{}, errors.New("unexpected /proc stat format")
	}
	f := strings.Fields(line[i+1:])
	if len(f) < 20 {
		return time.Time{}, errors.New("unexpected /proc stat format")
	}
	ticks, err := strconv.ParseInt(f[19], 10, 64) // field 22 overall
	if err != nil {
		return time.Time{}, err
	}
	return bootTime().Add(time.Duration(ticks/100) * time.Second), nil
}

var (
	bootOnce sync.Once
	boot     time.Time
)

func bootTime() time.Time {
	bootOnce.Do(func() {
		b, err := os.ReadFile("/proc/stat")
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "btime "); ok {
				if secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
					boot = time.Unix(secs, 0)
				}
				return
			}
		}
	})
	return boot
}
