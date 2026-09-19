package collect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// USER_HZ is 100 on every mainstream Linux build.
const clkTck = 100

var (
	pageSize  = uint64(os.Getpagesize())
	errFormat = errors.New("unexpected /proc format")
)

type procKey struct {
	pid   int32
	start uint64 // guards against PID reuse
}

type procPrev struct {
	ticks  uint64
	rb, wb uint64
	ioOK   bool
}

type rawStat struct {
	ppid         int32
	comm, state  string
	utime, stime uint64
	threads      int32
	start        uint64
	rss          uint64
}

func (c *Collector) collectProcs(s *Snapshot, elapsed float64) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	next := make(map[procKey]procPrev, len(ents))
	s.Procs = make([]Proc, 0, len(ents))

	for _, e := range ents {
		n, err := strconv.ParseInt(e.Name(), 10, 32)
		if err != nil {
			continue
		}
		pid := int32(n)
		st, err := readStat(pid)
		if err != nil {
			continue // exited mid-scan
		}

		p := Proc{
			PID:     pid,
			PPID:    st.ppid,
			Name:    st.comm,
			State:   st.state,
			RSS:     st.rss,
			Threads: st.threads,
			Started: c.boot.Add(time.Duration(st.start) * (time.Second / clkTck)),
			Cmdline: readCmdline(pid),
			User:    c.userOf(pid),
		}
		cg := cgroupPath(pid)
		p.Unit, p.UnitUser = unitOf(cg)
		p.Container, p.Runtime = containerOf(cg)
		if s.Mem.Total > 0 {
			p.MemPct = float64(st.rss) / float64(s.Mem.Total) * 100
		}

		cur := procPrev{ticks: st.utime + st.stime}
		cur.rb, cur.wb, cur.ioOK = readIO(pid)
		p.IOHidden = !cur.ioOK
		key := procKey{pid: pid, start: st.start}
		if prev, ok := c.prevProc[key]; ok && elapsed > 0 {
			p.CPU = float64(sub(cur.ticks, prev.ticks)) / clkTck / elapsed * 100
			if cur.ioOK && prev.ioOK {
				p.ReadBps = float64(sub(cur.rb, prev.rb)) / elapsed
				p.WriteBps = float64(sub(cur.wb, prev.wb)) / elapsed
			}
		}
		next[key] = cur

		s.ByPID[pid] = len(s.Procs)
		s.Procs = append(s.Procs, p)
	}
	c.prevProc = next
}

func readStat(pid int32) (rawStat, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return rawStat{}, err
	}
	str := string(b)
	l := strings.IndexByte(str, '(')
	r := strings.LastIndexByte(str, ')') // comm may contain ')' or spaces
	if l < 0 || r < l || r+2 > len(str) {
		return rawStat{}, errFormat
	}
	rs := rawStat{comm: str[l+1 : r]}
	f := strings.Fields(str[r+2:]) // f[i] == stat field i+3
	if len(f) < 22 {
		return rs, errFormat
	}
	rs.state = f[0]
	ppid, _ := strconv.ParseInt(f[1], 10, 32)
	rs.ppid = int32(ppid)
	rs.utime, _ = strconv.ParseUint(f[11], 10, 64)
	rs.stime, _ = strconv.ParseUint(f[12], 10, 64)
	th, _ := strconv.ParseInt(f[17], 10, 32)
	rs.threads = int32(th)
	rs.start, _ = strconv.ParseUint(f[19], 10, 64)
	pages, _ := strconv.ParseUint(f[21], 10, 64)
	rs.rss = pages * pageSize
	return rs, nil
}

func readCmdline(pid int32) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(b) == 0 {
		return ""
	}
	if len(b) > 1024 {
		b = b[:1024]
	}
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
}

// readIO returns block-layer read/write bytes. Needs root for other users;
// ok is false when the file couldn't be read, so callers can tell "unknown"
// (permission denied) apart from "genuinely zero".
func readIO(pid int32) (rb, wb uint64, ok bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid))
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, cut := strings.Cut(line, ": ")
		if !cut {
			continue
		}
		switch k {
		case "read_bytes":
			rb, _ = strconv.ParseUint(v, 10, 64)
		case "write_bytes":
			wb, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	return rb, wb, true
}

func (c *Collector) userOf(pid int32) string {
	fi, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	if err != nil {
		return ""
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	now := time.Now()
	if ent, ok := c.users[st.Uid]; ok && now.Sub(ent.at) < userCacheTTL {
		return ent.name
	}
	name := strconv.FormatUint(uint64(st.Uid), 10)
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	} else if n := getentUser(name); n != "" {
		name = n // LDAP/SSSD users are not in /etc/passwd
	}
	c.users[st.Uid] = userEnt{name: name, at: now}
	return name
}

func getentUser(uid string) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "getent", "passwd", uid).Output()
	if err != nil {
		return ""
	}
	name, _, _ := strings.Cut(string(out), ":")
	return strings.TrimSpace(name)
}

func cgroupPath(pid int32) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return ""
	}
	var legacy string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			return parts[2] // cgroup v2
		}
		if parts[1] == "name=systemd" {
			legacy = parts[2] // cgroup v1
		}
	}
	return legacy
}

var containerRe = regexp.MustCompile(`(?:^|[-/])(docker|libpod|cri-containerd|crio)-([0-9a-f]{64})\.scope$|/docker/([0-9a-f]{64})(?:$|/)`)

// containerOf extracts a short container ID and runtime name from a cgroup
// path, e.g.:
//
//	/system.slice/docker-4f8b...64hex....scope        -> (4f8b8b8b8b8b, docker)
//	/kubepods.slice/.../cri-containerd-9a2c...64hex.scope -> (9a2c9a2c9a2c, containerd)
//	/machine.slice/libpod-<64hex>.scope               -> (..., podman)
//	/docker/<64hex>                                   -> (..., docker) (cgroup v1)
//
// top and htop show every containerized process as an indistinguishable PID
// among hundreds of others on the host, with nothing tying it back to the
// container that owns it — this is one of the most consistently requested,
// unaddressed gaps against htop for anyone running Docker or Kubernetes.
func containerOf(path string) (id, runtime string) {
	m := containerRe.FindStringSubmatch(path)
	if m == nil {
		return "", ""
	}
	full := m[2]
	rt := m[1]
	if full == "" {
		full = m[3]
		rt = "docker"
	}
	switch rt {
	case "cri-containerd":
		rt = "containerd"
	case "crio":
		rt = "cri-o"
	case "libpod":
		rt = "podman"
	}
	return full[:12], rt
}

// unitOf returns the most specific systemd unit in a cgroup path, e.g.
// /system.slice/nginx.service                         -> nginx.service
// /kubepods.slice/.../cri-containerd-<id>.scope      -> cri-containerd-<id>.scope
// /user.slice/.../user@1000.service/app.slice/x.scope -> x.scope (user unit)
func unitOf(path string) (string, bool) {
	segs := strings.Split(path, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		s := segs[i]
		if strings.HasSuffix(s, ".service") || strings.HasSuffix(s, ".scope") {
			return s, strings.Contains(path, "/user@") && !strings.HasPrefix(s, "user@")
		}
	}
	return "", false
}

// Extra holds details that are too expensive to read for every process.
type Extra struct {
	Exe, Cwd, Cgroup string
	FDs              int // -1 when unreadable
	FDLimit          string
	OOMScore, OOMAdj string
}

func ProcExtra(pid int32) Extra {
	base := fmt.Sprintf("/proc/%d", pid)
	e := Extra{FDs: -1, Cgroup: cgroupPath(pid)}
	e.Exe, _ = os.Readlink(base + "/exe")
	e.Cwd, _ = os.Readlink(base + "/cwd")
	if d, err := os.Open(base + "/fd"); err == nil {
		if names, err := d.Readdirnames(-1); err == nil {
			e.FDs = len(names)
		}
		d.Close()
	}
	if b, err := os.ReadFile(base + "/limits"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "Max open files") {
				if f := strings.Fields(line); len(f) >= 5 {
					e.FDLimit = f[3] // soft limit
				}
			}
		}
	}
	e.OOMScore = readTrim(base + "/oom_score")
	e.OOMAdj = readTrim(base + "/oom_score_adj")
	return e
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readPSI parses /proc/pressure/<res> avg10 values.
func readPSI(res string) (some, full float64, ok bool) {
	b, err := os.ReadFile("/proc/pressure/" + res)
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, found := strings.CutPrefix(f[1], "avg10=")
		if !found {
			continue
		}
		x, _ := strconv.ParseFloat(v, 64)
		switch f[0] {
		case "some":
			some = x
		case "full":
			full = x
		}
	}
	return some, full, true
}
