package remote

import (
	"fmt"
	"strings"

	"github.com/archesterr/whytop/internal/collect"
)

type procView struct{ collect.Proc }

func newMem() *collect.Mem { return &collect.Mem{} }

// probeFixture is one probe's worth of output in the exact shape the remote
// shell produces it, so the parser is tested against the format it will
// actually meet rather than against a tidied-up version of it.
func probeFixture(appJiffies, appReadBytes int) string {
	pad := strings.Repeat("0 ", 30)
	return fmt.Sprintf(`Welcome to Ubuntu
@@meta
web01
6.8.0-40-generic
4096
Ubuntu 24.04.1 LTS
@@stat
cpu  1000 10 500 90000 300 5 20 0 0 0
cpu0 500 5 250 45000 150 2 10 0 0 0
cpu1 500 5 250 45000 150 3 10 0 0 0
intr 12345
@@meminfo
MemTotal:       32819752 kB
MemFree:         1000000 kB
MemAvailable:   20000000 kB
Buffers:          500000 kB
Cached:          8000000 kB
SwapTotal:             0 kB
SwapFree:              0 kB
@@loadavg
0.50 0.40 0.30 2/900 12345
@@uptime
86400.00 340000.00
@@pressure
==> /proc/pressure/cpu <==
some avg10=1.20 avg60=0.80 avg300=0.40 total=123456

==> /proc/pressure/io <==
some avg10=4.00 avg60=2.00 avg300=1.00 total=99999
full avg10=2.00 avg60=1.00 avg300=0.50 total=88888
@@owners
dr-xr-xr-x 9 root     root     0 Sep 20 13:00 /proc/1
dr-xr-xr-x 9 www-data www-data 0 Sep 20 13:00 /proc/4242
@@procstat
==> /proc/1/stat <==
1 (systemd) S 0 1 1 0 -1 4194560 900 0 0 0 50 20 0 0 20 0 1 0 5 170000000 3800 %s

==> /proc/4242/stat <==
4242 (app) S 1 4242 4242 0 -1 4194304 1234 0 0 0 %d 0 0 0 20 0 9 0 987654 123456789 5000 %s
@@proccmd
==> /proc/1/cmdline <==
/sbin/init%ssplash
==> /proc/4242/cmdline <==
/usr/bin/app%s--serve
@@procio
==> /proc/4242/io <==
rchar: 999
read_bytes: %d
write_bytes: 0
@@sockets
LISTEN 0 4096 0.0.0.0:443 0.0.0.0:* users:(("app",pid=4242,fd=7))
	 cubic cwnd:10
@@end
`, pad, appJiffies, pad, "\x00", "\x00", appReadBytes)
}
