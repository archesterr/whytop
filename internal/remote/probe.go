package remote

// The probe reads the remote host's /proc over the SSH session. Nothing is
// installed, uploaded or left behind — the decision was that whytop must
// work on any box you can SSH into without writing anything to it.
//
// The cost that buys is fork count. The obvious shell loop
//
//	for d in /proc/[0-9]*; do cat $d/stat; tr '\0' ' ' < $d/cmdline; done
//
// forks twice per process — about a thousand processes' worth of fork+exec
// on a busy host, every refresh, which is a measurable load all by itself.
// `tail -n +1` takes every file in one process and prints a
// "==> path <==" header before each, so the whole of /proc costs a handful
// of forks instead.
//
// NUL-separated cmdlines come back raw and are split on the Go side. Doing
// that remotely would mean `tr` per process, and awk's handling of NUL
// bytes differs between gawk and mawk — the default on Debian — which is
// exactly the kind of thing that works on the host you tested and not on
// the one you needed it on.
const probeScript = `
echo '@@meta'
cat /proc/sys/kernel/hostname 2>/dev/null
uname -r 2>/dev/null
getconf PAGESIZE 2>/dev/null
. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME" || echo ''
echo '@@stat'
cat /proc/stat 2>/dev/null
echo '@@meminfo'
cat /proc/meminfo 2>/dev/null
echo '@@loadavg'
cat /proc/loadavg 2>/dev/null
echo '@@uptime'
cat /proc/uptime 2>/dev/null
echo '@@pressure'
tail -n +1 /proc/pressure/cpu /proc/pressure/memory /proc/pressure/io 2>/dev/null
echo '@@owners'
ls -ld /proc/[0-9]* 2>/dev/null
echo '@@procstat'
tail -n +1 /proc/[0-9]*/stat 2>/dev/null
echo '@@proccmd'
tail -n +1 /proc/[0-9]*/cmdline 2>/dev/null
echo '@@procio'
tail -n +1 /proc/[0-9]*/io 2>/dev/null
echo '@@sockets'
ss -tinpHa 2>/dev/null
echo '@@end'
`
