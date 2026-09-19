package collect

import (
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

	Procs []Proc
	ByPID map[int32]int `json:"-"` // PID -> index in Procs

	Conns          []Conn
	ConnsCollected bool

	Disks []Disk
	FS    []FS
	NICs  []NIC
	TCP   TCP
}

type CPU struct {
	Cores                                   int
	User, System, Iowait, Steal, Idle, Busy float64 // percent
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
	CPU               float64 // % of one core (top style)
	RSS               uint64
	MemPct            float64
	ReadBps, WriteBps float64 // block-layer bytes/s (root needed for other users)
	Threads           int32
	Started           time.Time
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
