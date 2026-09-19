# whytop

Live troubleshooting for Linux servers, in your terminal. One static binary, run straight over SSH: who owns a port, what a process is doing, what it spawned, and whether the box is starving on CPU, memory, disk or network. You can stop, kill or restart from the same screen.

No runtime dependencies, no network exposure, no browser. Works air-gapped.

## Why not just top/htop/iotop?

Those tools show CPU and memory well and haven't needed to change much in 20+ years. whytop exists for the questions they were never built to answer:

- **Which container is this?** On a Docker/Kubernetes host, `htop` shows a wall of PIDs with no indication of which belongs to which container — [a widely requested, still-open gap](https://github.com/htop-dev/htop/issues/1036). whytop tags every containerized process with its short container ID and runtime (docker/containerd/cri-o/podman), and you can filter by it directly.
- **Did the OOM killer just eat my process?** Every other tool leaves you to separately dig through `dmesg`/`journalctl -k` after the fact. whytop watches the kernel log continuously and surfaces a kill the moment it happens.
- **Why is it actually slow — CPU, memory, or I/O?** PSI (pressure stall information) is the kernel's own modern answer to that question, and none of `top`/`htop`/`iotop` show it. whytop does, always visible.
- **What's listening on this port, and is it reachable from the network?** That's `ss`/`netstat` territory, not `top`'s. whytop flags wildcard binds (`0.0.0.0`) right in the list.
- **What are this process's sockets, right now?** `iotop` is disk-only and needs root plus a kernel accounting flag most distros don't enable by default. whytop shows a process's own sockets, disk I/O, and its whole child tree together, and can stop/restart it without leaving the screen.

The honest tradeoff: whytop is new and far less battle-tested than a tool that's shipped on every Linux box for two decades. It's built for the specific job of "something on this server is wrong, show me why," not as a `top` replacement for routine day-to-day glancing.

## Run

```bash
ssh server
sudo whytop                # just run it
sudo whytop -pid 1234      # opens a PID
sudo whytop -port 443      # opens the process listening on 443
```

whytop opens no socket and needs no token — it's a local program that reads `/proc` and signals processes directly, nothing more. Root is needed to see other users' sockets, per-process disk I/O and open files. The version running is always shown in the top-left corner (`sudo whytop -version` prints it and exits, for scripting).

## What you get

| Tab | Shows |
|---|---|
| Processes | CPU, memory, disk read/write per second, state (D and Z highlighted), systemd unit, command. Sort by any column |
| Ports | Listening sockets with owning process and unit. Wildcard binds flagged. Established, time-wait, close-wait counts |
| Disks | IOPS, throughput, await, queue depth, utilization per device — plus the processes actually driving those numbers right now, a process stuck in D-state (blocked on I/O) always ranked first. Filesystem and inode usage. Hung network mounts flagged |
| Network | Per-interface traffic, errors, drops. TCP retransmits, resets, new connections |

Always visible: CPU, memory, I/O wait, load per core, and PSI pressure. A red banner surfaces immediately if the kernel OOM-killed a process — no need to go digging through `dmesg`.

Opening a process shows its state, parent, CPU/memory/disk with children, open files against the limit, OOM score, container (if any), unit status and restart count, the full child tree, its own sockets, and the journal.

## Keys

The footer lists only the keys that work on the current screen.

| Where | Keys |
|---|---|
| Everywhere | `1`–`4` tabs, `p` pause |
| Processes, Ports | `↑` `↓` select, `Enter` open, `/` filter, `Esc` clear filter |
| Processes | `s` cycle sort |
| Ports | `a` all sockets / listening only |
| Process panel | `x` stop (SIGTERM), `X` force kill (SIGKILL), `r` restart unit, `l` reload journal, `Esc` close |

Every destructive action asks for confirmation. The mouse works too: click a tab to switch, click a row to open it, scroll to move the selection.

## Security

- No listening socket, ever — whytop inherits whatever access the shell it's run from already has, nothing more.
- Refuses to signal PID 1, or itself, or restart scopes and user sessions.

Root is only needed for full visibility (other users' sockets, disk I/O, open files). Prefer capabilities over full root where your kernel supports it:

```bash
sudo setcap cap_sys_ptrace,cap_dac_read_search,cap_sys_admin+ep ./whytop
```

## Build

```bash
make build      # bin/whytop, static
make run        # build and run with sudo
```

Releases: push a `v*` tag; GitHub Actions runs goreleaser (tar.gz, deb, rpm).

## License

MIT
