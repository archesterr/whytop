# whytop

Live troubleshooting for Linux servers and desktops. One static binary, two front ends: a browser UI, or a terminal UI that runs straight over SSH with no port, no token and no tunnel.
Either way: who owns a port, what a process is doing, what it spawned, and whether the box is starving on CPU, memory, disk or network. You can stop, kill or restart from the same screen.

No runtime dependencies, no CDN assets. Works air-gapped.

## Run

Web UI:

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

Terminal UI — for servers where opening a browser (even tunneled) is more friction than it's worth. Just SSH in and run it:

```bash
ssh server
sudo whytop -tui                    # runs entirely in the terminal, no network exposure at all
sudo whytop -tui -pid 1234          # opens a PID
sudo whytop -tui -port 443          # opens the process listening on 443
```

The terminal UI has no listening socket and needs no token — it's a local program reading `/proc` and driving the same signal/restart guards as the web UI, nothing more. It's the better default for a server you're already SSH'd into; reach for the web UI when you want the richer drill-down view or to share a live link with a teammate over a tunnel.

Root is needed to see other users' sockets, per-process disk I/O and open files, in both front ends.

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

Same keys in both front ends; the footer lists only the ones that work on the current screen.

| Where | Keys |
|---|---|
| Everywhere | `1`–`4` tabs, `p` pause |
| Processes, Ports | `↑` `↓` select, `Enter` open, `/` filter, `Esc` clear filter |
| Processes | `s` cycle sort |
| Ports | `a` all sockets / listening only |
| Process panel | `x` stop (SIGTERM), `X` force kill (SIGKILL), `r` restart unit, `l` reload journal, `Esc` close |

Every destructive action asks for confirmation.

## Security

- The terminal UI (`-tui`) opens no socket at all — it inherits whatever access the SSH session already has, nothing more.
- The web UI binds to `127.0.0.1` by default on a random port.
- Every web request needs the random token printed at startup; API calls send it in a header, which also blocks cross-site requests.
- The web UI's script is locked to the sha256 hash of the page's own inline `<script>` — arbitrary injected script can't run even if something else on the page were compromised. (`style-src` allows inline styles, since the page legitimately computes many of its own — bar widths, indentation — and CSP has no hash mechanism for that; there's no path here for untrusted data to reach a style attribute.)
- `X-Frame-Options`, `Cross-Origin-Opener-Policy`, `Cross-Origin-Resource-Policy` and a locked-down `Permissions-Policy` are set on every response.
- Server timeouts (read/write/idle) are set to resist slow-client style connection exhaustion.
- Both front ends refuse to signal PID 1, or whytop itself, or restart scopes and user sessions.
- The web UI logs every destructive action (signal, restart) and every rejected request with the source address, for audit.

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
