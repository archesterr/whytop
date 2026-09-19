package collect

import (
	"math"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
)

// Collector samples the system. Collect must not be called concurrently.
type Collector struct {
	// WantConns enables socket collection. It walks every /proc/<pid>/fd,
	// so it only runs while a view needs it.
	WantConns atomic.Bool

	boot     time.Time
	prevAt   time.Time
	prevCPU  *cpu.TimesStat
	prevProc map[procKey]procPrev
	prevDisk map[string]disk.IOCountersStat
	prevNIC  map[string]psnet.IOCountersStat
	prevTCP  map[string]int64
	users    map[uint32]string
	hung     map[string]bool
}

func New() *Collector {
	c := &Collector{
		prevProc: map[procKey]procPrev{},
		prevDisk: map[string]disk.IOCountersStat{},
		prevNIC:  map[string]psnet.IOCountersStat{},
		users:    map[uint32]string{},
		hung:     map[string]bool{},
	}
	if bt, err := host.BootTime(); err == nil {
		c.boot = time.Unix(int64(bt), 0)
	}
	return c
}

func (c *Collector) Collect() *Snapshot {
	now := time.Now()
	var elapsed float64
	if !c.prevAt.IsZero() {
		elapsed = now.Sub(c.prevAt).Seconds()
	}

	s := &Snapshot{At: now, Root: os.Geteuid() == 0, ByPID: map[int32]int{}}
	s.Host, _ = os.Hostname()
	if up, err := host.Uptime(); err == nil {
		s.Uptime = time.Duration(up) * time.Second
	}
	if l, err := load.Avg(); err == nil {
		s.Load1, s.Load5, s.Load15 = l.Load1, l.Load5, l.Load15
	}

	c.collectCPU(s)
	c.collectMem(s)
	collectPSI(s)
	c.collectProcs(s, elapsed)
	if c.WantConns.Load() {
		c.collectConns(s)
	}
	c.collectDisks(s, elapsed)
	c.collectFS(s)
	c.collectNet(s, elapsed)

	c.prevAt = now
	return s
}

func (c *Collector) collectCPU(s *Snapshot) {
	s.CPU.Cores, _ = cpu.Counts(true)
	ts, err := cpu.Times(false)
	if err != nil || len(ts) == 0 {
		return
	}
	cur := ts[0]
	if p := c.prevCPU; p != nil {
		total := cpuTotal(cur) - cpuTotal(*p)
		if total > 0 {
			pct := func(now, before float64) float64 { return (now - before) / total * 100 }
			s.CPU.User = pct(cur.User+cur.Nice, p.User+p.Nice)
			s.CPU.System = pct(cur.System+cur.Irq+cur.Softirq, p.System+p.Irq+p.Softirq)
			s.CPU.Iowait = pct(cur.Iowait, p.Iowait)
			s.CPU.Steal = pct(cur.Steal, p.Steal)
			s.CPU.Idle = pct(cur.Idle, p.Idle)
			s.CPU.Busy = 100 - s.CPU.Idle - s.CPU.Iowait
		}
	}
	c.prevCPU = &cur
}

// guest time is already included in user on Linux.
func cpuTotal(t cpu.TimesStat) float64 {
	return t.User + t.Nice + t.System + t.Idle + t.Iowait + t.Irq + t.Softirq + t.Steal
}

func (c *Collector) collectMem(s *Snapshot) {
	if v, err := mem.VirtualMemory(); err == nil {
		s.Mem.Total, s.Mem.Used, s.Mem.Available = v.Total, v.Used, v.Available
		s.Mem.Cached, s.Mem.Buffers, s.Mem.UsedPct = v.Cached, v.Buffers, finite(v.UsedPercent)
	}
	if sw, err := mem.SwapMemory(); err == nil {
		s.Mem.SwapTotal, s.Mem.SwapUsed, s.Mem.SwapPct = sw.Total, sw.Used, finite(sw.UsedPercent)
	}
}

func collectPSI(s *Snapshot) {
	cpuSome, _, ok := readPSI("cpu")
	if !ok {
		return
	}
	s.PSI.Available = true
	s.PSI.CPUSome = cpuSome
	s.PSI.MemSome, s.PSI.MemFull, _ = readPSI("memory")
	s.PSI.IOSome, s.PSI.IOFull, _ = readPSI("io")
}

func (c *Collector) collectConns(s *Snapshot) {
	cs, err := psnet.Connections("inet")
	if err != nil {
		return
	}
	s.ConnsCollected = true
	s.Conns = make([]Conn, 0, len(cs))
	for _, cn := range cs {
		proto := "tcp"
		if cn.Type == syscall.SOCK_DGRAM {
			proto = "udp"
		}
		if cn.Family == syscall.AF_INET6 {
			proto += "6"
		}
		cc := Conn{Proto: proto, LocalIP: cn.Laddr.IP, LPort: cn.Laddr.Port, State: cn.Status, PID: cn.Pid}
		if cn.Raddr.IP != "" && cn.Raddr.Port != 0 {
			cc.Remote = net.JoinHostPort(cn.Raddr.IP, strconv.Itoa(int(cn.Raddr.Port)))
		}
		if strings.HasPrefix(proto, "udp") {
			cc.State = "UNCONN"
			if cc.Remote != "" {
				cc.State = "CONNECTED"
			}
		}
		s.Conns = append(s.Conns, cc)
	}
}

func (c *Collector) collectDisks(s *Snapshot, elapsed float64) {
	m, err := disk.IOCounters()
	if err != nil {
		return
	}
	next := make(map[string]disk.IOCountersStat, len(m))
	for name, cur := range m {
		if skipDev(name) {
			continue
		}
		next[name] = cur
		d := Disk{Name: name, Label: cur.Label, InFlight: cur.IopsInProgress}
		if p, ok := c.prevDisk[name]; ok && elapsed > 0 {
			rc := float64(sub(cur.ReadCount, p.ReadCount))
			wc := float64(sub(cur.WriteCount, p.WriteCount))
			ms := elapsed * 1000
			d.RIOPS, d.WIOPS = rc/elapsed, wc/elapsed
			d.RBps = rate(cur.ReadBytes, p.ReadBytes, elapsed)
			d.WBps = rate(cur.WriteBytes, p.WriteBytes, elapsed)
			d.Util = math.Min(float64(sub(cur.IoTime, p.IoTime))/ms*100, 100)
			d.Queue = float64(sub(cur.WeightedIO, p.WeightedIO)) / ms
			if rc+wc > 0 {
				d.AwaitMs = float64(sub(cur.ReadTime, p.ReadTime)+sub(cur.WriteTime, p.WriteTime)) / (rc + wc)
			}
		}
		s.Disks = append(s.Disks, d)
	}
	c.prevDisk = next
	sort.Slice(s.Disks, func(i, j int) bool { return s.Disks[i].Name < s.Disks[j].Name })
}

func skipDev(name string) bool {
	for _, p := range []string{"loop", "ram", "fd", "sr"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

var skipFSTypes = map[string]bool{
	"squashfs": true, "overlay": true, "tmpfs": true, "devtmpfs": true, "proc": true,
	"sysfs": true, "cgroup": true, "cgroup2": true, "nsfs": true, "tracefs": true,
	"debugfs": true, "securityfs": true, "pstore": true, "bpf": true, "configfs": true,
	"fusectl": true, "mqueue": true, "hugetlbfs": true, "autofs": true, "binfmt_misc": true,
	"efivarfs": true, "ramfs": true, "devpts": true,
}

// bind mounts created by container runtimes flood the list on k8s nodes.
func skipMount(m string) bool {
	for _, p := range []string{"/var/lib/kubelet/", "/run/containerd/", "/var/lib/docker/", "/var/lib/containerd/", "/snap/"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

func (c *Collector) collectFS(s *Snapshot) {
	parts, err := disk.Partitions(false)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, p := range parts {
		if skipFSTypes[p.Fstype] || seen[p.Mountpoint] || skipMount(p.Mountpoint) {
			continue
		}
		seen[p.Mountpoint] = true
		f := FS{Mount: p.Mountpoint, Device: p.Device, Type: p.Fstype}
		if c.hung[p.Mountpoint] {
			f.Stale = true
		} else if u, timedOut := usageWithTimeout(p.Mountpoint, time.Second); timedOut {
			c.hung[p.Mountpoint] = true // never block on it again
			f.Stale = true
		} else if u != nil {
			f.Total, f.Used, f.Free = u.Total, u.Used, u.Free
			f.UsedPct, f.InodePct = finite(u.UsedPercent), finite(u.InodesUsedPercent)
		}
		s.FS = append(s.FS, f)
	}
	sort.Slice(s.FS, func(i, j int) bool { return s.FS[i].Mount < s.FS[j].Mount })
}

func usageWithTimeout(path string, d time.Duration) (*disk.UsageStat, bool) {
	ch := make(chan *disk.UsageStat, 1)
	go func() {
		u, err := disk.Usage(path)
		if err != nil {
			u = nil
		}
		ch <- u
	}()
	select {
	case u := <-ch:
		return u, false
	case <-time.After(d):
		return nil, true
	}
}

func (c *Collector) collectNet(s *Snapshot, elapsed float64) {
	if ifs, err := psnet.IOCounters(true); err == nil {
		next := make(map[string]psnet.IOCountersStat, len(ifs))
		for _, cur := range ifs {
			next[cur.Name] = cur
			n := NIC{Name: cur.Name, ErrTotal: cur.Errin + cur.Errout, DropTotal: cur.Dropin + cur.Dropout}
			if p, ok := c.prevNIC[cur.Name]; ok && elapsed > 0 {
				n.RxBps = rate(cur.BytesRecv, p.BytesRecv, elapsed)
				n.TxBps = rate(cur.BytesSent, p.BytesSent, elapsed)
				n.RxPps = rate(cur.PacketsRecv, p.PacketsRecv, elapsed)
				n.TxPps = rate(cur.PacketsSent, p.PacketsSent, elapsed)
				n.ErrPs = rate(cur.Errin+cur.Errout, p.Errin+p.Errout, elapsed)
				n.DropPs = rate(cur.Dropin+cur.Dropout, p.Dropin+p.Dropout, elapsed)
			}
			s.NICs = append(s.NICs, n)
		}
		c.prevNIC = next
		sort.SliceStable(s.NICs, func(i, j int) bool {
			x := s.NICs[i].RxBps + s.NICs[i].TxBps
			y := s.NICs[j].RxBps + s.NICs[j].TxBps
			if x != y {
				return x > y
			}
			return s.NICs[i].Name < s.NICs[j].Name
		})
	}

	pc, err := psnet.ProtoCounters([]string{"tcp"})
	if err != nil || len(pc) == 0 {
		return
	}
	cur := pc[0].Stats
	s.TCP.Available = true
	s.TCP.Established = cur["CurrEstab"]
	if p := c.prevTCP; p != nil && elapsed > 0 {
		delta := func(k string) float64 {
			v := cur[k] - p[k]
			if v < 0 {
				v = 0
			}
			return float64(v)
		}
		s.TCP.ActivePs = delta("ActiveOpens") / elapsed
		s.TCP.PassivePs = delta("PassiveOpens") / elapsed
		s.TCP.RetransPs = delta("RetransSegs") / elapsed
		s.TCP.InErrPs = delta("InErrs") / elapsed
		s.TCP.ResetPs = delta("OutRsts") / elapsed
		if out := delta("OutSegs"); out > 0 {
			s.TCP.RetransPct = delta("RetransSegs") / out * 100
		}
	}
	c.prevTCP = cur
}

func sub(a, b uint64) uint64 {
	if a < b {
		return 0 // counter reset / wrap
	}
	return a - b
}

// finite keeps NaN/Inf out of the JSON encoder.
func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func rate(cur, prev uint64, elapsed float64) float64 {
	return float64(sub(cur, prev)) / elapsed
}
