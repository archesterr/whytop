package remote

import (
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
	c.prevSock = cur
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
