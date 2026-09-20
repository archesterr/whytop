package remote

import (
	"strconv"
	"strings"
	"time"

	"github.com/archesterr/whytop/internal/collect"
)

// section splits the probe's output on its @@markers. Anything before the
// first marker is noise from the login shell (motd, profile chatter), which
// is exactly why the markers exist.
func sections(out string) map[string]string {
	res := map[string]string{}
	name := ""
	var b strings.Builder
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "@@") {
			if name != "" {
				res[name] = b.String()
			}
			name, b = strings.TrimPrefix(line, "@@"), strings.Builder{}
			continue
		}
		if name != "" {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if name != "" {
		res[name] = b.String()
	}
	return res
}

// tailFiles splits `tail -n +1` output back into its files. tail prints a
// "==> path <==" header before each file, and separates them with a blank
// line — but only between files, so the blank line belongs to the previous
// file's content and is trimmed rather than counted.
func tailFiles(s string) map[string]string {
	res := map[string]string{}
	path := ""
	var b strings.Builder
	flush := func() {
		if path != "" {
			res[path] = strings.TrimRight(b.String(), "\n")
		}
		b = strings.Builder{}
	}
	for _, line := range strings.Split(s, "\n") {
		if p, ok := strings.CutPrefix(line, "==> "); ok {
			if p, ok := strings.CutSuffix(p, " <=="); ok {
				flush()
				path = p
				continue
			}
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	flush()
	return res
}

// pidFromPath pulls the PID out of "/proc/1234/stat".
func pidFromPath(p string) int32 {
	rest, ok := strings.CutPrefix(p, "/proc/")
	if !ok {
		return 0
	}
	num, _, _ := strings.Cut(rest, "/")
	n, err := strconv.ParseInt(num, 10, 32)
	if err != nil {
		return 0
	}
	return int32(n)
}

// procStat parses one /proc/<pid>/stat line.
//
// Field 2 is the executable name in parentheses and may itself contain
// spaces and parentheses — "(Web Content)" is a real one — so the line is
// split at the *last* ')' rather than by whitespace. Getting this wrong
// shifts every field after it, which shows up as nonsense CPU times rather
// than as an error.
type procStat struct {
	name      string
	state     string
	ppid      int32
	utime     uint64
	stime     uint64
	threads   int32
	starttime uint64
	rssPages  uint64
}

func parseProcStat(line string) (procStat, bool) {
	var ps procStat
	open := strings.IndexByte(line, '(')
	close := strings.LastIndexByte(line, ')')
	if open < 0 || close < open {
		return ps, false
	}
	ps.name = line[open+1 : close]
	f := strings.Fields(line[close+1:])
	// f[0] is field 3 (state), so field N is f[N-3].
	if len(f) < 22 {
		return ps, false
	}
	at := func(n int) string { return f[n-3] }
	ps.state = at(3)
	ps.ppid = int32(atoiDefault(at(4)))
	ps.utime = atoiDefault(at(14))
	ps.stime = atoiDefault(at(15))
	ps.threads = int32(atoiDefault(at(20)))
	ps.starttime = atoiDefault(at(22))
	if len(f) >= 22 {
		ps.rssPages = atoiDefault(at(24))
	}
	return ps, true
}

func atoiDefault(s string) uint64 {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// owners maps a PID to the username that owns its /proc entry, from
// `ls -ld`. Reading ownership this way means no /etc/passwd lookup and no
// second round trip — the remote `ls` has already resolved the name.
func parseOwners(s string) map[int32]string {
	res := map[int32]string{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 9 {
			continue
		}
		if pid := pidFromPath(f[len(f)-1]); pid > 0 {
			res[pid] = f[2]
		}
	}
	return res
}

// cmdline turns a NUL-separated /proc/<pid>/cmdline into a command line. A
// kernel thread's is empty, which is how Proc.Kernel tells them apart.
func parseCmdline(s string) string {
	s = strings.TrimRight(s, "\x00")
	return strings.TrimSpace(strings.ReplaceAll(s, "\x00", " "))
}

func parseMeminfo(s string, m *collect.Mem) {
	get := map[string]uint64{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		get[k] = atoiDefault(f[0]) * 1024 // meminfo is in kB
	}
	m.Total, m.Available = get["MemTotal"], get["MemAvailable"]
	m.Cached, m.Buffers = get["Cached"], get["Buffers"]
	if m.Total > 0 {
		used := m.Total - m.Available
		if m.Available == 0 { // very old kernels have no MemAvailable
			used = m.Total - get["MemFree"] - get["Cached"] - get["Buffers"]
		}
		m.Used = used
		m.UsedPct = float64(used) / float64(m.Total) * 100
	}
	m.SwapTotal = get["SwapTotal"]
	if m.SwapTotal > 0 {
		m.SwapUsed = m.SwapTotal - get["SwapFree"]
		m.SwapPct = float64(m.SwapUsed) / float64(m.SwapTotal) * 100
	}
}

// cpuTimes is one /proc/stat cpu line's fields, in jiffies.
type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (c cpuTimes) total() uint64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

func (c cpuTimes) busy() uint64 { return c.total() - c.idle - c.iowait }

// parseCPU returns the aggregate line and the per-core lines, in order.
func parseCPU(s string) (cpuTimes, []cpuTimes) {
	var agg cpuTimes
	var cores []cpuTimes
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		t := cpuTimes{
			user: atoiDefault(f[1]), nice: atoiDefault(f[2]), system: atoiDefault(f[3]),
			idle: atoiDefault(f[4]), iowait: atoiDefault(f[5]), irq: atoiDefault(f[6]),
			softirq: atoiDefault(f[7]),
		}
		if len(f) > 8 {
			t.steal = atoiDefault(f[8])
		}
		if f[0] == "cpu" {
			agg = t
			continue
		}
		cores = append(cores, t)
	}
	return agg, cores
}

func parsePressure(files map[string]string, psi *collect.PSI) {
	some := func(body string) float64 {
		for _, line := range strings.Split(body, "\n") {
			if !strings.HasPrefix(line, "some") {
				continue
			}
			for _, tok := range strings.Fields(line) {
				if v, ok := strings.CutPrefix(tok, "avg10="); ok {
					f, _ := strconv.ParseFloat(v, 64)
					return f
				}
			}
		}
		return 0
	}
	if body, ok := files["/proc/pressure/cpu"]; ok {
		psi.Available = true
		psi.CPUSome = some(body)
	}
	if body, ok := files["/proc/pressure/memory"]; ok {
		psi.Available = true
		psi.MemSome = some(body)
	}
	if body, ok := files["/proc/pressure/io"]; ok {
		psi.Available = true
		psi.IOSome = some(body)
	}
}

// parseProcIO pulls the cumulative read/write byte counters. These are
// readable only for your own processes unless you are root, which is why a
// non-root remote session shows most rows as hidden rather than as zero.
func parseProcIO(body string) (read, write uint64, ok bool) {
	for _, line := range strings.Split(body, "\n") {
		k, v, cut := strings.Cut(line, ":")
		if !cut {
			continue
		}
		n := atoiDefault(strings.TrimSpace(v))
		switch k {
		case "read_bytes":
			read, ok = n, true
		case "write_bytes":
			write, ok = n, true
		}
	}
	return read, write, ok
}

func parseLoad(s string) (l1, l5, l15 float64) {
	f := strings.Fields(s)
	if len(f) < 3 {
		return 0, 0, 0
	}
	l1, _ = strconv.ParseFloat(f[0], 64)
	l5, _ = strconv.ParseFloat(f[1], 64)
	l15, _ = strconv.ParseFloat(f[2], 64)
	return l1, l5, l15
}

func parseUptime(s string) time.Duration {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	secs, _ := strconv.ParseFloat(f[0], 64)
	return time.Duration(secs) * time.Second
}
