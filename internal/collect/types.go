package collect

import (
	"strconv"
	"strings"
	"time"
)

// Snapshot is one immutable sample of the whole system.
type Snapshot struct {
	At     time.Time
	Host   string
	Root   bool
	Uptime time.Duration
	Load1  float64
	Load5  float64
	Load15 float64

	CPU CPU
	Mem Mem
	PSI PSI

	// OS is the distribution's own name for itself, plus the kernel it's
	// running. On a fleet of look-alike boxes it's the first thing you want
	// confirmed before you believe anything else on the screen.
	OS     string
	Kernel string

	Procs []Proc
	ByPID map[int32]int `json:"-"` // PID -> index in Procs

	Conns          []Conn
	ConnsCollected bool

	Disks []Disk
	FS    []FS
	NICs  []NIC
	TCP   TCP

	// Throttles and Limits are local-only: see limits.go.
	Throttles []Throttle
	Limits    Limits
}

type CPU struct {
	Cores                                   int
	User, System, Iowait, Steal, Idle, Busy float64 // percent
	// PerCore is each core's busy percentage. A 12-core box averaging 8%
	// looks idle right up until you notice one core pinned at 100% — which
	// is what a single-threaded bottleneck looks like, and the average is
	// exactly the statistic that hides it.
	PerCore []float64
}

type Mem struct {
	Total, Used, Available, Cached, Buffers uint64
	UsedPct                                 float64
	SwapTotal, SwapUsed                     uint64
	SwapPct                                 float64
}

// PSI holds /proc/pressure avg10 values (% of wall time stalled).
type PSI struct {
	Available        bool
	CPUSome          float64
	MemSome, MemFull float64
	IOSome, IOFull   float64
}

type Proc struct {
	PID, PPID         int32
	Name, Cmdline     string
	User              string
	State             string  // raw state char: R S D Z T I ...
	Unit              string  // most specific systemd unit (.service / .scope)
	UnitUser          bool    // unit lives under a user@.service manager
	Container         string  // short container ID (12 chars), empty if not containerized
	Runtime           string  // docker / containerd / cri-o / podman
	CPU               float64 // % of one core (top style)
	RSS               uint64
	MemPct            float64
	ReadBps, WriteBps float64 // block-layer bytes/s (root needed for other users)
	IOHidden          bool    // true when /proc/<pid>/io was unreadable (not root, other user)
	Threads           int32
	// Throttled is the share of CPU periods the process's cgroup spent out
	// of quota; FDs its open descriptors and FDLimit their soft limit (0
	// when unread or unlimited).
	Throttled    float64
	FDs, FDLimit int
	Started      time.Time

	// Ports are the ports this process is listening on, and Estab is how
	// many connections it currently has established. Answering "what is
	// listening on 8080" used to mean leaving for `ss -tulpn`.
	Ports []uint32
	Estab int
	// NetRxBps/NetTxBps are per-process network throughput, and NetKnown
	// says whether they were measurable at all — see collect/netrate.go for
	// why that is not a given on Linux.
	NetRxBps, NetTxBps float64
	NetKnown           bool

	// Depth is how far under its parent a row sits in tree view. It is set
	// by the renderer's ordering, not by collection — the same process is
	// at a different depth depending on what the filter left visible.
	Depth int
}

// PortList renders the listening ports for a table cell, newest concern
// first: a process listening on three ports shows the lowest (most likely
// to be the service port) and says how many more there are.
func (p Proc) PortList() string {
	switch len(p.Ports) {
	case 0:
		return ""
	case 1:
		return strconv.FormatUint(uint64(p.Ports[0]), 10)
	}
	out := strconv.FormatUint(uint64(p.Ports[0]), 10)
	return out + "+" + strconv.Itoa(len(p.Ports)-1)
}

// Kernel reports whether this is a kernel thread ([kworker/…], [ksoftirqd/…]
// and friends) rather than a real userspace process. Kernel threads have no
// command line at all, which is how ps/htop tell them apart too. A zombie
// also has an empty command line but is very much worth seeing, so it never
// counts as one.
//
// On a quiet 4-core box these are ~90% of every PID on the system, so a
// process list that doesn't separate them out is mostly noise.
func (p Proc) Kernel() bool {
	return p.Cmdline == "" && p.State != "Z"
}

type Conn struct {
	Proto   string // tcp tcp6 udp udp6
	LocalIP string
	LPort   uint32
	Remote  string
	State   string
	PID     int32 // 0 = unknown (not root)
}

func (c Conn) Listening() bool {
	return c.State == "LISTEN" || (strings.HasPrefix(c.Proto, "udp") && c.Remote == "")
}

type Disk struct {
	Name, Label  string
	RIOPS, WIOPS float64
	RBps, WBps   float64
	AwaitMs      float64
	Queue        float64 // avg queue size (aqu-sz)
	Util         float64 // %util
	InFlight     uint64
}

type FS struct {
	Mount, Device, Type string
	Total, Used, Free   uint64
	UsedPct, InodePct   float64
	Stale               bool // statfs hung (dead NFS etc.)
}

type NIC struct {
	Name                string
	RxBps, TxBps        float64
	RxPps, TxPps        float64
	ErrPs, DropPs       float64
	ErrTotal, DropTotal uint64
}

type TCP struct {
	Available             bool
	Established           int64
	ActivePs, PassivePs   float64
	RetransPs, RetransPct float64
	InErrPs, ResetPs      float64
}
