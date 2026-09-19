# whytop

Live troubleshooting for Linux servers and desktops, in your browser.
One static binary starts a small web UI on a random local port: who owns a port, what a process is doing, what it spawned, and whether the box is starving on CPU, memory, disk or network. You can stop, kill or restart from the same screen.

No runtime dependencies, no CDN assets. Works air-gapped.

## Run

```bash
sudo whytop                         # prints http://127.0.0.1:<random>/?t=<token>
sudo whytop -port 443               # opens the process listening on 443
sudo whytop -pid 1234               # opens a PID
sudo whytop -listen 0.0.0.0:9090    # reachable from the network (token still required)
```

Remote server, safest option — keep the default loopback bind and tunnel:

```bash
ssh -L 43127:127.0.0.1:43127 server   # use the port whytop printed
```

Root is needed to see other users' sockets, per-process disk I/O and open files.

## What you get

| Tab | Shows |
|---|---|
| Processes | CPU, memory, disk read/write per second, state (D and Z highlighted), systemd unit, command. Sort by any column |
| Ports | Listening sockets with owning process and unit. Wildcard binds flagged. Established, time-wait, close-wait counts |
| Disks | IOPS, throughput, await, queue, utilization per device. Filesystem and inode usage. Hung network mounts flagged |
| Network | Per-interface traffic, errors, drops. TCP retransmits, resets, new connections |

Always visible: CPU, memory, I/O wait, load per core, and PSI pressure, each with a live sparkline.

Opening a process shows its state, parent, CPU/memory/disk with children, open files against the limit, OOM score, unit status and restart count, cgroup, the full child tree, its sockets, and the journal.

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

- Binds to `127.0.0.1` by default on a random port.
- Every request needs the random token printed at startup; API calls send it in a header, which also blocks cross-site requests.
- Strict CSP built from hashes of the page's own inline script/style (no `unsafe-inline`), no external resources, no referrer.
- `X-Frame-Options`, `Cross-Origin-Opener-Policy`, `Cross-Origin-Resource-Policy` and a locked-down `Permissions-Policy` are set on every response.
- Server timeouts (read/write/idle) are set to resist slow-client style connection exhaustion.
- Refuses to signal PID 1, itself, or restart scopes and user sessions.
- Every destructive action (signal, restart) and every rejected request is written to the server's log with the source address, for audit.

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
