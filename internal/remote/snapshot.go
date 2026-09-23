package remote

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/archesterr/whytop/internal/collect"
)

// prev is the previous sample's cumulative counters, kept so this tick can
// turn them into rates. Everything /proc reports is a total since boot, so
// a single sample can only ever say "this process has used 4 minutes of
// CPU", never "this process is using 80% right now".
type prev struct {
	at    time.Time
	cpu   cpuTimes
	cores []cpuTimes
	procs map[int32]prevProc

	tcp         map[string]int64
	listenDrops uint64
	haveListen  bool
	throttle    map[string][2]uint64 // cgroup -> periods, throttled
}

type prevProc struct {
	jiffies     uint64
	read, write uint64
	starttime   uint64
}

// build turns one probe's output into a snapshot, using the previous sample
// for every rate. The first sample after connecting has no rates — the CPU
// column reads 0 until the second tick, the same as top's first screen.
func (c *Client) build(out string, now time.Time) *collect.Snapshot {
	sec := sections(out)
	s := &collect.Snapshot{At: now, ByPID: map[int32]int{}}

	meta := strings.Split(strings.TrimSpace(sec["meta"]), "\n")
	pageSize := uint64(4096)
	if len(meta) > 0 {
		s.Host = strings.TrimSpace(meta[0])
	}
	if len(meta) > 1 {
		s.Kernel = strings.TrimSpace(meta[1])
	}
	if len(meta) > 2 {
		if n := atoiDefault(strings.TrimSpace(meta[2])); n > 0 {
			pageSize = n
		}
	}
	if len(meta) > 3 {
		s.OS = strings.Trim(strings.TrimSpace(meta[3]), `"`)
	}

	s.Load1, s.Load5, s.Load15 = parseLoad(sec["loadavg"])
	s.Uptime = parseUptime(sec["uptime"])
	parseMeminfo(sec["meminfo"], &s.Mem)
	parsePressure(tailFiles(sec["pressure"]), &s.PSI)

	agg, cores := parseCPU(sec["stat"])
	s.CPU.Cores = len(cores)
	p := c.prev
	elapsed := 0.0
	if p != nil && !p.at.IsZero() {
		elapsed = now.Sub(p.at).Seconds()
	}
	if p != nil {
		if total := float64(agg.total() - p.cpu.total()); total > 0 {
			pct := func(now, before uint64) float64 { return float64(now-before) / total * 100 }
			s.CPU.User = pct(agg.user+agg.nice, p.cpu.user+p.cpu.nice)
			s.CPU.System = pct(agg.system+agg.irq+agg.softirq, p.cpu.system+p.cpu.irq+p.cpu.softirq)
			s.CPU.Iowait = pct(agg.iowait, p.cpu.iowait)
			s.CPU.Steal = pct(agg.steal, p.cpu.steal)
			s.CPU.Idle = pct(agg.idle, p.cpu.idle)
			s.CPU.Busy = 100 - s.CPU.Idle - s.CPU.Iowait
		}
		if len(p.cores) == len(cores) {
			s.CPU.PerCore = make([]float64, len(cores))
			for i, cur := range cores {
				if total := float64(cur.total() - p.cores[i].total()); total > 0 {
					s.CPU.PerCore[i] = float64(cur.busy()-p.cores[i].busy()) / total * 100
				}
			}
		}
	}

	owners := parseOwners(sec["owners"])
	stats := tailFiles(sec["procstat"])
	cmds := tailFiles(sec["proccmd"])
	ios := tailFiles(sec["procio"])

	next := prev{at: now, cpu: agg, cores: cores, procs: make(map[int32]prevProc, len(stats))}
	ticks := float64(clockTicks)

	for path, body := range stats {
		pid := pidFromPath(path)
		if pid <= 0 {
			continue
		}
		ps, ok := parseProcStat(strings.TrimSpace(body))
		if !ok {
			continue
		}
		proc := collect.Proc{
			PID: pid, PPID: ps.ppid, Name: ps.name, State: ps.state,
			User:    owners[pid],
			Threads: ps.threads,
			RSS:     ps.rssPages * pageSize,
			Cmdline: parseCmdline(cmds["/proc/"+strconv.Itoa(int(pid))+"/cmdline"]),
		}
		if s.Mem.Total > 0 {
			proc.MemPct = float64(proc.RSS) / float64(s.Mem.Total) * 100
		}
		if s.Uptime > 0 {
			proc.Started = now.Add(-(s.Uptime - time.Duration(float64(ps.starttime)/ticks*float64(time.Second))))
		}

		cur := prevProc{jiffies: ps.utime + ps.stime, starttime: ps.starttime}
		ioBody, haveIO := ios["/proc/"+strconv.Itoa(int(pid))+"/io"]
		if haveIO {
			cur.read, cur.write, haveIO = parseProcIO(ioBody)
		}
		proc.IOHidden = !haveIO

		// A PID can be reused between ticks. starttime is the process's
		// identity token: if it differs, this is a different process that
		// happens to hold the same number, anddifferencing against the old one would
		// produce a spike out of nowhere.
		if was, ok := p.proc(pid); ok && was.starttime == ps.starttime && elapsed > 0 {
			proc.CPU = float64(cur.jiffies-was.jiffies) / ticks / elapsed * 100
			if haveIO {
				if cur.read >= was.read {
					proc.ReadBps = float64(cur.read-was.read) / elapsed
				}
				if cur.write >= was.write {
					proc.WriteBps = float64(cur.write-was.write) / elapsed
				}
			}
		}
		next.procs[pid] = cur
		s.Procs = append(s.Procs, proc)
	}

	sortProcs(s.Procs)
	for i, pr := range s.Procs {
		s.ByPID[pr.PID] = i
	}
	// Ports and per-process traffic come from the same ss reading here.
	// Local collection walks /proc/*/fd for ports instead, which is not
	// affordable over an SSH round trip per refresh.
	cur := collect.ParseSS(sec["sockets"])
	collect.ApplySockets(s.Procs, c.prevSock, cur, elapsed)
	s.ConnsCollected = cur != nil
	s.Conns = sockConns(cur)
	c.prevSock = cur
	c.limits(s, sec, p, &next, elapsed)
	c.prev = &next
	return s
}

func (p *prev) proc(pid int32) (prevProc, bool) {
	if p == nil {
		return prevProc{}, false
	}
	v, ok := p.procs[pid]
	return v, ok
}

// clockTicks is USER_HZ, which is 100 on every Linux architecture whytop
// can reach over SSH. It is fixed rather than read from the remote because
// getconf CLK_TCK would be one more fork per refresh for a constant.
const clockTicks = 100

func sortProcs(procs []collect.Proc) {
	// Ordering is the TUI's job; this only needs to be deterministic so
	// ByPID indices are stable between renders of the same snapshot.
	for i := 1; i < len(procs); i++ {
		for j := i; j > 0 && procs[j].PID < procs[j-1].PID; j-- {
			procs[j], procs[j-1] = procs[j-1], procs[j]
		}
	}
}

// sockConns turns the ss reading into the connection list that the
// findings, the header and a process's socket panel read. ss spells states
// its own way (ESTAB, CLOSE-WAIT); they are respelled the way the local
// collector reports them, so nothing downstream needs to know which
// collector a snapshot came from. The peer address is the last field of
// the key ParseSS builds.
func sockConns(socks map[string]collect.SockStat) []collect.Conn {
	out := make([]collect.Conn, 0, len(socks))
	for key, sk := range socks {
		state := strings.ReplaceAll(sk.State(), "-", "_")
		if state == "ESTAB" {
			state = "ESTABLISHED"
		}
		peer := key[strings.LastIndexByte(key, '|')+1:]
		if state == "LISTEN" {
			peer = ""
		}
		out = append(out, collect.Conn{Proto: "tcp", LPort: sk.LPort(), Remote: peer, State: state, PID: sk.PID()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LPort != out[j].LPort {
			return out[i].LPort < out[j].LPort
		}
		return out[i].Remote < out[j].Remote
	})
	return out
}

// limits fills in the ceilings from the probe's later sections: the same
// figures the local collector reads, parsed by the same code.
func (c *Client) limits(s *collect.Snapshot, sec map[string]string, p *prev, next *prev, elapsed float64) {
	lim := tailFiles(sec["limits"])
	s.Limits.FilesUsed, s.Limits.FilesMax, _ = collect.ParseFileNr(lim["/proc/sys/fs/file-nr"])
	s.Limits.ConntrackUsed = atoiDefault(strings.TrimSpace(lim["/proc/sys/net/netfilter/nf_conntrack_count"]))
	s.Limits.ConntrackMax = atoiDefault(strings.TrimSpace(lim["/proc/sys/net/netfilter/nf_conntrack_max"]))

	if tcp := collect.ParseSNMPTcp(sec["snmp"]); tcp != nil {
		var before map[string]int64
		if p != nil {
			before = p.tcp
		}
		collect.FillTCP(&s.TCP, tcp, before, elapsed)
		next.tcp = tcp
	}
	drops := collect.ListenDrops(sec["snmp"])
	if p != nil && p.haveListen && elapsed > 0 && drops >= p.listenDrops {
		s.Limits.ListenOverflowPs = float64(drops-p.listenDrops) / elapsed
	}
	next.listenDrops, next.haveListen = drops, true
	s.Limits.FullListeners = collect.FullListeners(sec["listen"])

	// Open descriptors per process, and the limit for the ones with enough
	// of them for it to matter.
	limits := tailFiles(sec["proclimits"])
	for _, line := range strings.Split(sec["fds"], "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid := int32(atoiDefault(f[0]))
		i, ok := s.ByPID[pid]
		if !ok {
			continue
		}
		s.Procs[i].FDs = int(atoiDefault(f[1]))
		if body, ok := limits["/proc/"+f[0]+"/limits"]; ok {
			s.Procs[i].FDLimit = collect.FDLimit(body)
		}
	}

	// CPU throttling: each process's cpu cgroup, and that group's counters.
	groups := map[int32][2]string{} // pid -> path, controller
	for _, line := range strings.Split(sec["cgroups"], "\n") {
		f := strings.SplitN(line, " ", 3)
		if len(f) != 3 {
			continue
		}
		pid := int32(atoiDefault(f[0]))
		// The cpu controller's line wins over the unified one on a hybrid
		// host: v1's cpu hierarchy is where the quota is enforced.
		if cur, ok := groups[pid]; ok && cur[1] != "" {
			continue
		}
		groups[pid] = [2]string{f[2], f[1]}
	}
	stat := tailFiles(sec["cpustat"])
	next.throttle = map[string][2]uint64{}
	seen := map[string]float64{}
	for i := range s.Procs {
		pr := &s.Procs[i]
		g, ok := groups[pr.PID]
		if !ok || g[0] == "/" || g[0] == "" {
			continue
		}
		if v, done := seen[g[0]]; done {
			pr.Throttled = v
			continue
		}
		seen[g[0]] = 0
		dirs := []string{"/sys/fs/cgroup" + g[0]}
		if g[1] != "" {
			dirs = []string{"/sys/fs/cgroup/cpu,cpuacct" + g[0], "/sys/fs/cgroup/cpu" + g[0]}
		}
		for _, d := range dirs {
			body, ok := stat[d+"/cpu.stat"]
			if !ok {
				continue
			}
			periods, throttled := collect.ParseCPUStat(body)
			next.throttle[g[0]] = [2]uint64{periods, throttled}
			if p == nil {
				break
			}
			was, had := p.throttle[g[0]]
			v, ok := collect.ThrottlePct(was[0], was[1], periods, throttled)
			if !had || !ok {
				break
			}
			seen[g[0]], pr.Throttled = v, v
			if v > 0 {
				quota := collect.QuotaV2(stat[d+"/cpu.max"])
				if g[1] != "" {
					quota = collect.QuotaV1(stat[d+"/cpu.cfs_quota_us"], stat[d+"/cpu.cfs_period_us"])
				}
				s.Throttles = append(s.Throttles, collect.Throttle{Cgroup: g[0], Label: collect.ThrottleLabel(pr), PID: pr.PID, Pct: v, Quota: quota})
			}
			break
		}
	}
}
