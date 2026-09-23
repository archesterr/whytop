package collect

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// This file collects the limits a box runs into during an incident that
// top, htop, iotop and nethogs cannot see at all, because each one is a
// ceiling rather than a usage figure:
//
//   - CPU throttling. A container with a 0.5-core quota shows 50% CPU in top
//     and looks healthy while it spends most of every 100ms period frozen.
//     The kernel counts the periods it was throttled in; nobody shows them.
//   - File descriptors. "Too many open files" takes a service down with
//     plenty of CPU and memory left, and nothing on screen predicted it.
//   - The listen queue. A backend too slow to accept() drops new connections
//     in the kernel; clients see timeouts, the process sees nothing wrong.
//   - The conntrack table. When it fills, the kernel drops packets for new
//     flows on the whole box and says so only in dmesg.

// Throttle is one cgroup's CPU throttling over the last interval.
type Throttle struct {
	Cgroup string
	// Label is what the operator knows the group as: its unit, its
	// container, or the first process in it.
	Label string
	PID   int32
	// Pct is the share of scheduling periods in which the group ran out of
	// quota — the same ratio Kubernetes' CPUThrottlingHigh alert uses.
	Pct float64
	// Quota is the limit in cores, 0 when it could not be read.
	Quota float64
}

// Limits is the box-wide half of the above.
type Limits struct {
	FilesUsed, FilesMax         uint64
	ConntrackUsed, ConntrackMax uint64
	// ListenOverflowPs is connections per second dropped because a
	// listening socket's accept queue was full.
	ListenOverflowPs float64
	// FullListeners are the listening ports with connections waiting to be
	// accepted, deepest queue first — where the drops above are happening.
	FullListeners []uint32
}

type cpuStat struct{ periods, throttled uint64 }

// cpuCgroup returns the cgroup that holds a process's CPU controller: the
// unified path on v2, the cpu hierarchy's on v1.
func cpuCgroup(b []byte) (string, bool) {
	var v2 string
	var have bool
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		for _, c := range strings.Split(parts[1], ",") {
			if c == "cpu" {
				return parts[2], false
			}
		}
		if parts[0] == "0" && parts[1] == "" {
			v2, have = parts[2], true
		}
	}
	return v2, have
}

// cgroupDirs are where a cgroup's files live, most specific first.
func cgroupDirs(path string, v2 bool) []string {
	if v2 {
		return []string{filepath.Join("/sys/fs/cgroup", path)}
	}
	return []string{
		filepath.Join("/sys/fs/cgroup/cpu,cpuacct", path),
		filepath.Join("/sys/fs/cgroup/cpu", path),
	}
}

func readCPUStat(dir string) (cpuStat, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "cpu.stat"))
	if err != nil {
		return cpuStat{}, false
	}
	var s cpuStat
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(line, " ")
		n, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		switch k {
		case "nr_periods":
			s.periods = n
		case "nr_throttled":
			s.throttled = n
		}
	}
	return s, true
}

// readQuota returns the CPU limit in cores, 0 for none.
func readQuota(dir string, v2 bool) float64 {
	if v2 {
		b, err := os.ReadFile(filepath.Join(dir, "cpu.max"))
		if err != nil {
			return 0
		}
		f := strings.Fields(string(b))
		if len(f) != 2 || f[0] == "max" {
			return 0
		}
		q, _ := strconv.ParseFloat(f[0], 64)
		p, _ := strconv.ParseFloat(f[1], 64)
		if p <= 0 {
			return 0
		}
		return q / p
	}
	q := readInt(filepath.Join(dir, "cpu.cfs_quota_us"))
	p := readInt(filepath.Join(dir, "cpu.cfs_period_us"))
	if q <= 0 || p <= 0 {
		return 0
	}
	return float64(q) / float64(p)
}

func readInt(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

// collectThrottle samples cpu.stat once per distinct cgroup and marks every
// process in a throttled one.
func (c *Collector) collectThrottle(s *Snapshot) {
	next := map[string]cpuStat{}
	pct := map[string]float64{}
	for i := range s.Procs {
		p := &s.Procs[i]
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", p.PID))
		if err != nil {
			continue
		}
		path, v2 := cpuCgroup(b)
		if path == "" || path == "/" {
			continue // the root group has no quota to run out of
		}
		key := path
		if v, seen := pct[key]; seen {
			p.Throttled = v
			continue
		}
		pct[key] = 0
		for _, dir := range cgroupDirs(path, v2) {
			cur, ok := readCPUStat(dir)
			if !ok {
				continue
			}
			next[key] = cur
			prev, had := c.prevThrottle[key]
			if had && cur.periods > prev.periods {
				v := float64(sub(cur.throttled, prev.throttled)) / float64(cur.periods-prev.periods) * 100
				pct[key] = v
				p.Throttled = v
				if v > 0 {
					s.Throttles = append(s.Throttles, Throttle{Cgroup: path, Label: throttleLabel(p), PID: p.PID, Pct: v, Quota: readQuota(dir, v2)})
				}
			}
			break
		}
	}
	c.prevThrottle = next
}

// collectLimits reads the box-wide ceilings.
func (c *Collector) collectLimits(s *Snapshot, elapsed float64) {
	if f := strings.Fields(readString("/proc/sys/fs/file-nr")); len(f) == 3 {
		used, _ := strconv.ParseUint(f[0], 10, 64)
		free, _ := strconv.ParseUint(f[1], 10, 64)
		s.Limits.FilesUsed = used - min(free, used)
		s.Limits.FilesMax, _ = strconv.ParseUint(f[2], 10, 64)
	}
	s.Limits.ConntrackUsed = uint64(max(0, readInt("/proc/sys/net/netfilter/nf_conntrack_count")))
	s.Limits.ConntrackMax = uint64(max(0, readInt("/proc/sys/net/netfilter/nf_conntrack_max")))

	cur := tcpExt(readString("/proc/net/netstat"))
	if prev, ok := c.prevListenDrop, c.havePrevListen; ok && elapsed > 0 && cur >= prev {
		s.Limits.ListenOverflowPs = float64(cur-prev) / elapsed
	}
	c.prevListenDrop, c.havePrevListen = cur, true
	s.Limits.FullListeners = fullListeners(readString("/proc/net/tcp") + readString("/proc/net/tcp6"))
}

// fullListeners reads the accept queues out of /proc/net/tcp. For a socket
// in LISTEN (state 0A) the rx_queue field is how many connections have
// completed the handshake and are waiting for the program to accept() them.
// The backlog they are measured against is not in this file (ss gets it
// over netlink), so this cannot say "full" — but a healthy server accepts
// as fast as connections arrive and shows 0. When the kernel is dropping at
// a listener, that listener has the deepest queue, so they are returned
// deepest first.
func fullListeners(table string) []uint32 {
	type q struct {
		port  uint32
		depth uint64
	}
	var qs []q
	seen := map[uint32]bool{}
	for _, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[3] != "0A" {
			continue
		}
		_, rxs, ok := strings.Cut(f[4], ":")
		if !ok {
			continue
		}
		rx, _ := strconv.ParseUint(rxs, 16, 64)
		if rx == 0 {
			continue
		}
		i := strings.LastIndexByte(f[1], ':')
		if i < 0 {
			continue
		}
		port, err := strconv.ParseUint(f[1][i+1:], 16, 32)
		if err != nil || seen[uint32(port)] {
			continue
		}
		seen[uint32(port)] = true
		qs = append(qs, q{uint32(port), rx})
	}
	sort.SliceStable(qs, func(i, j int) bool { return qs[i].depth > qs[j].depth })
	out := make([]uint32, len(qs))
	for i, x := range qs {
		out[i] = x.port
	}
	return out
}

func readString(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// tcpExt returns the kernel's count of connections dropped at a listening
// socket. ListenDrops already includes ListenOverflows, so the larger of the
// two is the total rather than their sum.
func tcpExt(netstat string) uint64 {
	lines := strings.Split(netstat, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "TcpExt:") || !strings.HasPrefix(lines[i+1], "TcpExt:") {
			continue
		}
		keys, vals := strings.Fields(lines[i]), strings.Fields(lines[i+1])
		var overflows, drops uint64
		for j := 1; j < len(keys) && j < len(vals); j++ {
			n, _ := strconv.ParseUint(vals[j], 10, 64)
			switch keys[j] {
			case "ListenOverflows":
				overflows = n
			case "ListenDrops":
				drops = n
			}
		}
		return max(overflows, drops)
	}
	return 0
}

// collectFDs counts each process's open descriptors, and reads its limit
// only when the count is high enough that the limit could matter — the
// default soft limit is 1024, so below 512 nothing is near it.
func collectFDs(s *Snapshot) {
	for i := range s.Procs {
		p := &s.Procs[i]
		d, err := os.Open(fmt.Sprintf("/proc/%d/fd", p.PID))
		if err != nil {
			continue
		}
		names, _ := d.Readdirnames(-1)
		d.Close()
		p.FDs = len(names)
		if p.FDs >= 512 {
			p.FDLimit = fdLimit(readString(fmt.Sprintf("/proc/%d/limits", p.PID)))
		}
	}
}

func fdLimit(limits string) int {
	for _, line := range strings.Split(limits, "\n") {
		rest, ok := strings.CutPrefix(line, "Max open files")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			return 0
		}
		n, _ := strconv.Atoi(f[0]) // "unlimited" reads as 0, no limit
		return n
	}
	return 0
}

func throttleLabel(p *Proc) string {
	switch {
	case p.Unit != "":
		return p.Unit
	case p.Container != "":
		return p.Runtime + " " + p.Container
	}
	return fmt.Sprintf("%s[%d]", p.Name, p.PID)
}
