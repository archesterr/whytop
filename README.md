# whytop

Live troubleshooting for Linux servers, in your terminal. One static binary, run straight over SSH: who owns a port, what a process is doing, what it spawned, and whether the box is starving on CPU, memory, disk or network. You can stop, kill or restart from the same screen.

No runtime dependencies, no network exposure, no browser. Works air-gapped.

## Run

```bash
ssh server
sudo whytop                # just run it
sudo whytop -pid 1234      # opens a PID
sudo whytop -port 443      # opens the process listening on 443
```

whytop opens no socket and needs no token — it's a local program that reads `/proc` and signals processes directly, nothing more. Root is needed to see other users' sockets, per-process disk I/O and open files.

## What you get

| Tab | Shows |
|---|---|
| Processes | CPU, memory, disk read/write per second, state (D and Z highlighted), systemd unit, command. Sort by any column |
| Ports | Listening sockets with owning process and unit. Wildcard binds flagged. Established, time-wait, close-wait counts |
| Disks | IOPS, throughput, await, queue, utilization per device. Filesystem and inode usage. Hung network mounts flagged |
| Network | Per-interface traffic, errors, drops. TCP retransmits, resets, new connections |

Always visible: CPU, memory, I/O wait, load per core, and PSI pressure.

Opening a process shows its state, parent, CPU/memory/disk with children, open files against the limit, OOM score, unit status and restart count, the full child tree, and the journal.

## Keys

The footer lists only the keys that work on the current screen.

| Where | Keys |
|---|---|
| Everywhere | `1`–`4` tabs, `p` pause |
| Processes, Ports | `↑` `↓` select, `Enter` open, `/` filter, `Esc` clear filter |
| Processes | `s` cycle sort |
| Ports | `a` all sockets / listening only |
| Process panel | `x` stop (SIGTERM), `X` force kill (SIGKILL), `r` restart unit, `l` reload journal, `Esc` close |

Every destructive action asks for confirmation.

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
