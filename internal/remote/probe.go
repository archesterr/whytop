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
//
// The sections after @@sockets are the ceilings — file handles, conntrack,
// the listen queue, CPU quota, open files per process — and keep to the
// same rule: every file is read by one tail, cat, grep or find for the
// whole host, never one process per PID. The awk filters run on the
// output of tail rather than on /proc directly because mawk gives up on
// a file that vanishes between the glob and the open, and on a busy host
// some always do. set -f comes late because the /proc globs above need
// globbing and the cgroup paths below must not get it.
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
echo '@@limits'
tail -n +1 /proc/sys/fs/file-nr /proc/sys/net/netfilter/nf_conntrack_count /proc/sys/net/netfilter/nf_conntrack_max 2>/dev/null
echo '@@snmp'
cat /proc/net/snmp /proc/net/netstat 2>/dev/null
echo '@@listen'
grep -h ' 0A ' /proc/net/tcp /proc/net/tcp6 2>/dev/null
cg=$(tail -n +1 /proc/[0-9]*/cgroup 2>/dev/null | awk -F: '
  /^==> / { split($0, a, "/"); pid = a[3]; next }
  ($1 == "0" && $2 == "") || $2 ~ /(^|,)cpu(,|$)/ { print pid " " $2 " " $3 }')
echo '@@cgroups'
printf '%s\n' "$cg"
fds=$(find /proc/[0-9]*/fd -mindepth 1 -maxdepth 1 2>/dev/null | awk -F/ '{ c[$3]++ } END { for (p in c) print p, c[p] }')
echo '@@fds'
printf '%s\n' "$fds"
set -f
echo '@@cpustat'
files=$(printf '%s\n' "$cg" | awk '$3 != "/" && $3 != "" {
  if ($2 == "") { d = "/sys/fs/cgroup" $3; print d "/cpu.stat"; print d "/cpu.max" }
  else { for (i = 0; i < 2; i++) { d = (i ? "/sys/fs/cgroup/cpu" : "/sys/fs/cgroup/cpu,cpuacct") $3
    print d "/cpu.stat"; print d "/cpu.cfs_quota_us"; print d "/cpu.cfs_period_us" } } }' | sort -u)
[ -n "$files" ] && tail -n +1 $files 2>/dev/null
echo '@@proclimits'
files=$(printf '%s\n' "$fds" | awk '$2 >= 512 { print "/proc/" $1 "/limits" }')
[ -n "$files" ] && tail -n +1 $files 2>/dev/null
echo '@@end'
`
