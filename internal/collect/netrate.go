package collect

import (
	"bufio"
	"context"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// attachSockets folds the socket list into the processes that own it, so the
// process table can answer "what is listening on 8080" without anyone
// leaving for `ss -tulpn`.
func (c *Collector) attachSockets(s *Snapshot) {
	if !s.ConnsCollected || len(s.Procs) == 0 {
		return
	}
	ports := map[int32]map[uint32]bool{}
	estab := map[int32]int{}
	for _, cn := range s.Conns {
		if cn.PID <= 0 {
			continue
		}
		if cn.Listening() {
			if ports[cn.PID] == nil {
				ports[cn.PID] = map[uint32]bool{}
			}
			ports[cn.PID][cn.LPort] = true
			continue
		}
		if cn.State == "ESTABLISHED" {
			estab[cn.PID]++
		}
	}
	for i := range s.Procs {
		pid := s.Procs[i].PID
		if set := ports[pid]; len(set) > 0 {
			list := make([]uint32, 0, len(set))
			for p := range set {
				list = append(list, p)
			}
			sort.Slice(list, func(a, b int) bool { return list[a] < list[b] })
			s.Procs[i].Ports = list
		}
		s.Procs[i].Estab = estab[pid]
	}
}

// Per-process network throughput is the one number in this tool that Linux
// does not simply hand you. /proc/<pid>/net/* is per network namespace, not
// per process: on an ordinary host every process shares one namespace, so
// those files report the whole machine's traffic for every PID. Tools that
// do it properly either capture packets (nethogs) or read per-socket byte
// counters out of the kernel's TCP info.
//
// This takes the second route, via `ss`, because a monitoring tool has no
// business opening a packet capture. Each TCP socket carries cumulative
// bytes_sent/bytes_received; summing those per PID and differencing across
// ticks gives a real rate rather than an estimate.
//
// The counters are per socket and cumulative for that socket's lifetime, so
// the difference has to be taken per socket and only for sockets present in
// both samples — summing per PID and differencing the sums would read as a
// large negative spike every time a connection closed.
type sockBytes struct {
	pid      int32
	rx, tx   uint64
	haveInfo bool
	// state and lport let a caller that has no other source of socket
	// information — the remote collector, which cannot walk /proc/*/fd
	// over SSH — get the PORT column out of the same reading.
	state string
	lport uint32
}

// SockStat is one socket as `ss` reports it, exported for the remote
// collector. Local collection gets ports from /proc/*/fd instead and only
// needs ss for the byte counters.
type SockStat = sockBytes

// ParseSS exposes the `ss -tinpHa` parser. It is shared rather than
// reimplemented because the output format is the fiddly part, and two
// parsers for one format is two places for it to be wrong.
func ParseSS(out string) map[string]SockStat { return parseSS(out) }

// PID and byte counters for one socket.
func (s SockStat) PID() int32              { return s.pid }
func (s SockStat) Bytes() (uint64, uint64) { return s.rx, s.tx }

// ApplySockets folds a reading of ss into the process list: listening ports,
// established-connection counts, and — when a previous reading is given —
// per-process throughput. elapsed of zero means there is no previous sample
// to difference against, so only the ports are filled in.
func ApplySockets(procs []Proc, before, now map[string]SockStat, elapsed float64) {
	ports := map[int32]map[uint32]bool{}
	estab := map[int32]int{}
	rx := map[int32]float64{}
	tx := map[int32]float64{}
	owned := map[int32]bool{}

	for key, cur := range now {
		if cur.pid > 0 {
			owned[cur.pid] = true
		}
		switch {
		case cur.state == "LISTEN":
			if cur.pid > 0 && cur.lport > 0 {
				if ports[cur.pid] == nil {
					ports[cur.pid] = map[uint32]bool{}
				}
				ports[cur.pid][cur.lport] = true
			}
		case cur.state == "ESTAB":
			if cur.pid > 0 {
				estab[cur.pid]++
			}
		}
		if elapsed <= 0 || cur.pid <= 0 {
			continue
		}
		// Differenced per socket, never per PID: summing per PID and
		// differencing the sums reads as a large negative spike every time
		// a connection closes.
		was, ok := before[key]
		if !ok || was.pid != cur.pid {
			continue
		}
		if cur.rx >= was.rx {
			rx[cur.pid] += float64(cur.rx-was.rx) / elapsed
		}
		if cur.tx >= was.tx {
			tx[cur.pid] += float64(cur.tx-was.tx) / elapsed
		}
	}

	for i := range procs {
		pid := procs[i].PID
		if set := ports[pid]; len(set) > 0 {
			list := make([]uint32, 0, len(set))
			for p := range set {
				list = append(list, p)
			}
			sort.Slice(list, func(a, b int) bool { return list[a] < list[b] })
			procs[i].Ports = list
		}
		procs[i].Estab = estab[pid]
		procs[i].NetRxBps, procs[i].NetTxBps = rx[pid], tx[pid]
		procs[i].NetKnown = owned[pid]
	}
}

func (c *Collector) collectNetRates(s *Snapshot, elapsed float64) {
	cur := ssSockets()
	if cur == nil {
		c.prevSock = nil
		return
	}
	prev := c.prevSock
	c.prevSock = cur
	if prev == nil || elapsed <= 0 {
		// The first sample establishes a baseline; there is no rate yet.
		// Processes are still marked known so the column reads "0" rather
		// than "unavailable" for a single tick.
		c.markNetKnown(s, cur)
		return
	}
	rx := map[int32]float64{}
	tx := map[int32]float64{}
	for key, now := range cur {
		before, ok := prev[key]
		if !ok || before.pid != now.pid {
			continue // a new socket, or the key was reused by another process
		}
		if now.rx >= before.rx {
			rx[now.pid] += float64(now.rx-before.rx) / elapsed
		}
		if now.tx >= before.tx {
			tx[now.pid] += float64(now.tx-before.tx) / elapsed
		}
	}
	for i := range s.Procs {
		pid := s.Procs[i].PID
		s.Procs[i].NetRxBps, s.Procs[i].NetTxBps = rx[pid], tx[pid]
	}
	c.markNetKnown(s, cur)
}

// markNetKnown flags the processes ss could actually see. Without root, ss
// reports sockets but not who owns them, and a column that prints 0 B/s for
// a process whose traffic simply wasn't visible is worse than one that says
// it doesn't know.
func (c *Collector) markNetKnown(s *Snapshot, cur map[string]sockBytes) {
	owned := map[int32]bool{}
	for _, v := range cur {
		if v.pid > 0 {
			owned[v.pid] = true
		}
	}
	for i := range s.Procs {
		s.Procs[i].NetKnown = owned[s.Procs[i].PID]
	}
}

// ssSockets returns each socket's cumulative byte counters keyed by the socket
// itself. It returns nil when ss isn't installed, which is the honest answer
// on a minimal container image.
func ssSockets() map[string]sockBytes {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// -t tcp, -i socket info (the byte counters), -p owning process,
	// -n numeric, -H no header, -a including listeners.
	out, err := exec.CommandContext(ctx, "ss", "-tinpHa").Output()
	if err != nil {
		return nil
	}
	return parseSS(string(out))
}

// parseSS reads `ss -tinpHa` output. Every socket is one record spanning two
// lines: the address/state/users line, then an indented line of TCP info
// holding the byte counters. Anything it can't make sense of is skipped
// rather than guessed at.
func parseSS(out string) map[string]sockBytes {
	res := map[string]sockBytes{}
	var key string
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			// Continuation: the TCP info for the socket named above it.
			if key == "" {
				continue
			}
			v := res[key]
			if n, ok := ssField(line, "bytes_received:"); ok {
				v.rx, v.haveInfo = n, true
			}
			if n, ok := ssField(line, "bytes_sent:"); ok {
				v.tx, v.haveInfo = n, true
			}
			res[key] = v
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 {
			key = ""
			continue
		}
		// state recvq sendq local peer [users:(...)]
		key = f[0] + "|" + f[3] + "|" + f[4]
		res[key] = sockBytes{pid: ssPID(line), state: f[0], lport: ssPort(f[3])}
	}
	if len(res) == 0 {
		return nil
	}
	return res
}

// ssField pulls one `name:<number>` value out of a TCP-info line. The line
// is a flat list of space-separated tokens, some of which carry commas or
// further colons, so it matches the token rather than searching the string.
func ssField(line, name string) (uint64, bool) {
	for _, tok := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(tok, name); ok {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return 0, false
			}
			return n, true
		}
	}
	return 0, false
}

// ssPort pulls the port out of an address ss prints, which may be
// "0.0.0.0:22", "[::]:22" or "10.0.0.1:6443" — always after the last colon.
func ssPort(addr string) uint32 {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return 0
	}
	n, err := strconv.ParseUint(addr[i+1:], 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}

// ssPID pulls the owning PID out of `users:(("nginx",pid=1234,fd=6))`. A
// socket can list several processes when it's shared across a fork; the
// first is the one the row is attributed to, the same choice ss itself
// displays first.
func ssPID(line string) int32 {
	i := strings.Index(line, "pid=")
	if i < 0 {
		return 0
	}
	rest := line[i+4:]
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end == 0 {
		return 0
	}
	if end < 0 {
		end = len(rest)
	}
	n, err := strconv.ParseInt(rest[:end], 10, 32)
	if err != nil {
		return 0
	}
	return int32(n)
}
