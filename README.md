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

## Container

```bash
docker run --rm -it \
  --pid=host --network=host \
  --cap-add=SYS_PTRACE --cap-add=DAC_READ_SEARCH --cap-add=KILL \
  -v /run/systemd:/run/systemd:ro \
  -v /var/log/journal:/var/log/journal:ro \
  ghcr.io/archesterr/whytop
```

whytop watches the *host*, so a container that can only see itself is a container that shows you nothing. Each flag above buys a specific thing, and dropping one degrades that thing and nothing else:

| Flag | Without it |
|---|---|
| `-it` | No TTY, so no TUI at all — this one isn't optional |
| `--pid=host` | You see the container's own handful of processes instead of the host's |
| `--network=host` | Ports and per-process traffic are the container's namespace, not the host's |
| `--cap-add=SYS_PTRACE` | Other users' per-process I/O and open files read as `hidden` |
| `--cap-add=DAC_READ_SEARCH` | Same, for `/proc` entries the container user can't traverse |
| `--cap-add=KILL` | Stop and force-kill fail |
| `-v /run/systemd` | Unit status, restart and `e` (edit unit file) can't reach systemd |
| `-v /var/log/journal` | No journal, and OOM kills aren't detected |

Two things the image genuinely cannot do:

- **Filesystem usage is the container's, not the host's.** The disk and mount figures behind the status line come from the mounts whytop can see. Bind-mount what you care about, or run the binary on the host.
- **`c` (close a descriptor) needs gdb**, which isn't in the image — a debugger doesn't belong in a monitoring image. `t` (empty a file) works normally.

Restarting units additionally needs write access to systemd's socket (`-v /run/systemd/private`), which is effectively full control of the host's services from inside the container. That's deliberately not in the command above; add it only if you want that.

Build it yourself instead of pulling:

```bash
docker build -t whytop --build-arg VERSION=$(git describe --tags) .
```

## What you get

**One screen.** There are no tabs. Everything is the process list, because on a real box every question ends up being about a process anyway: what is eating the disk, what is holding port 8080, what is that unit doing. Splitting those across four tabs meant knowing which tab to be in before you knew what you were looking for.

| Column | Shows |
|---|---|
| PID, USER, ST | State, with D (blocked on I/O) and Z (zombie) highlighted |
| CPU%, MEM | Memory coloured against the size of the machine, not a fixed byte count |
| READ, WRITE | Block-layer bytes per second |
| PORT | The lowest port the process listens on, `+n` for the rest, `→n` when it isn't listening but has n connections open |
| NET↓, NET↑ | Per-process network throughput — see the caveat below |
| UNIT, COMMAND | The systemd unit, and the command line two-toned so the program's own name stands out of the path |

Every column sorts, by click or with `s`.

**The filter has a scope.** `/` opens it, `Tab` cycles what it searches — all, port, user, unit, command, pid, state — and the chip in the filter bar says which one you are in. Typing `443` in the `all` scope casts a wide net and will also match PIDs and command lines containing 443; `Tab` to `port` and it means the listening port and nothing else. The typed prefixes are the same thing: `port:443` is the `port` scope, and a typed prefix wins over the cycled one.

whytop uses the whole terminal, however wide it is — the extra columns go to `COMMAND`, since a truncated command line is the usual reason to want a wider window. As the terminal narrows, columns are given up in order (UNIT, then NET, then PORT) rather than squeezing COMMAND to an unreadable sliver.

### About NET↓ / NET↑

Linux does not simply hand you per-process network throughput. `/proc/<pid>/net/*` is per network *namespace*, not per process, so on an ordinary host it reports the whole machine's traffic for every PID — which is why most tools don't show this at all. Tools that do it properly either capture packets (nethogs) or read per-socket byte counters out of the kernel.

whytop takes the second route, via `ss`, because a monitoring tool has no business opening a packet capture. That means:

- **No `ss` (iproute2) on the host, or no root:** the columns disappear rather than showing zeros. A process whose traffic simply wasn't visible reads `?`, never `0 B/s` — those are different answers and only one of them is safe to act on.
- **TCP only.** UDP sockets carry no equivalent counters.

## Remote hosts

whytop opens on the machine you are sitting on. Press `H` for the host list, or start with `whytop -host deploy@web01`.

```
 HOSTS
● localhost                   this machine — no SSH
  db01                        postgres@10.0.0.20   (ssh config)
  web01                       deploy@10.0.0.11:2222   (ssh config)
```

Hosts come from `~/.ssh/config` (`-ssh-config`, or `c` in the panel, to read a different file), so the hosts you already maintain are simply there. `a` connects to one you type, as `host`, `user@host` or `user@host:port`.

**Nothing is installed on the remote host.** whytop reads its `/proc` over the SSH session — no agent, no uploaded binary, nothing written to the far end. It works on any box you can already SSH into.

- **Authentication is ssh-agent and keys only.** No passwords, so whytop never holds or stores a secret. If `ssh host` works, so does this.
- **Host keys are verified against `~/.ssh/known_hosts`,** never blindly accepted. An unknown host is refused with its fingerprint, because the fix is to check it yourself rather than have whytop skip the check.
- **Actions are disabled on remote hosts.** Stop, force-kill, restart, edit-unit and the file actions all run on the machine whytop is running on. A PID from a remote host would signal whatever process holds that number *here*, so those keys are refused by name and not even offered in the footer.
- **Some columns are local-only for now**: systemd unit attribution, container IDs, and a process's open files, sockets and journal. The remote collector reads raw `/proc` rather than going through gopsutil, and those need more than `/proc` alone. The panel says so rather than showing zeros.

Reading a host costs a handful of processes per refresh, not one per PID: the probe uses `tail -n +1` to read all of `/proc` in one go rather than forking `cat` a thousand times.

### The rest of the screen

Along the top: CPU, memory, I/O wait, load per core and PSI pressure, each with a gauge — plus a bar per core, because a 12-core box averaging 8% looks idle right up until you notice one core pinned at 100%, which is what a single-threaded bottleneck looks like from the outside. The header also names the distribution and kernel, so you can confirm which machine you are on before believing anything else.

Under them, a one-line verdict in plain words. Not `PSI io 22%`, but `⚠ 3 processes stuck waiting on disk · sda 98% busy · /var almost full (97%)`, or just `✓ Nothing obviously wrong right now`. You shouldn't need to already know that 0.7 load across 4 cores is fine but 20% I/O pressure is an emergency.

**Every finding is a link.** Click one, or press `g` to walk them, and the list narrows to the rows it's about — "3 processes stuck waiting on disk" filters to exactly those three, whether there's one or eight of them. `Esc` clears the filter again. Findings that are averages rather than live counts (I/O wait, PSI) sort the list instead of filtering it, since by the time you press `g` nothing may be blocked at that instant — sorting by I/O always has something to show, and it puts processes actually blocked on I/O above the ones merely doing a lot of it.

A red banner surfaces immediately if the kernel OOM-killed a process — no need to go digging through `dmesg`.

The process list hides kernel threads by default (`K` shows them). On an idle 4-core box those are ~90% of every PID on the system and never the thing you're troubleshooting.

**The process list can be pinned still.** Sorting by CPU is the right default and the reason the list squirms the moment you try to act on it: the row you're reaching for moves out from under the cursor on every refresh, because that's exactly what a busy process does. `L` locks the order — the numbers keep updating, the rows stay where you last saw them, and anything that starts afterwards appends at the bottom instead of shoving your row aside. Unlike `p` (pause), you're not left reading figures that have stopped being true. Change the sort while locked and it re-freezes on the new order. The footer always says which one you're in: `order: live` or `order: LOCKED`.

Opening a process shows its state, parent, CPU/memory/disk with children, its open files (path, fd, kind — not just a count against the limit), OOM score, container (if any), unit status and restart count, the full child tree, its own sockets, and the journal — which follows live, the way `journalctl -u <unit> -f` does, until you pause it with `f`. If systemd started it, `e` opens that unit's file in `$EDITOR` right there — and after you save and quit, asks whether to run `systemctl daemon-reload`. Stop/force-kill from the same screen acts on that process, so there's no separate "kill" control per open file or socket — they're all its own.

## Keys

The footer lists only the keys that do something on the current screen.

**The keys are htop's and top's wherever they have a name for something.** Being nearly-but-not-quite htop is worse than being nothing like it — a key that does something *else* is how the wrong process gets killed. `h` (or `F1`) shows the whole map in the tool.

| Key | Does | Same as |
|---|---|---|
| `↑` `↓` `PgUp` `PgDn` `Home` `End` | move the cursor | htop, top |
| `k` `F9` | kill — SIGTERM, confirmed | htop, top |
| `K` | show / hide kernel threads | htop |
| `P` `M` `T` | sort by CPU, memory, time | htop, top |
| `<` `>` `F6` | previous / next sort column | htop, top |
| `I` `R` | invert the sort order | htop, top |
| `t` `F5` | tree view | htop |
| `p` | full program path on / off | htop |
| `u` | filter by user | htop, top |
| `/` `F3` | search | htop |
| `l` | open files of this process | htop (`lsof`) |
| `h` `?` `F1` | help | htop, top |
| `q` `F10` | quit | htop, top |

Whytop has a few things those tools don't, and they sit on keys neither of them binds:

| Key | Does |
|---|---|
| `Enter` | open the process panel |
| `g` | go to the next problem |
| `@` | hosts — watch another box over SSH |
| `L` | lock the row order |
| `Space` | pause / resume sampling |
| `m` | release the mouse, so the terminal can select and copy text |
| `Tab` | in the filter: change what it searches |
| `Esc` | clear the filter, or close a panel |

| In the process panel | Keys |
|---|---|
| | `Tab` switch between the tree and open files, `x` stop, `X` force kill, `r` restart unit, `e` edit its unit file, `j` reload journal, `y` copy the journal, `f` pause/resume the live journal, `t` empty a file, `c` close a descriptor, `Esc` close |
| In the hosts panel | `↑` `↓` select, `Enter` connect, `a` add a host, `c` change ssh config, `r` reload, `Esc` close |
| Process panel | `Tab` switch between the process tree and open files, `x` stop (SIGTERM), `X` force kill (SIGKILL), `r` restart unit, `e` edit its unit file (asks to `daemon-reload` after), `j` reload journal, `f` pause/resume the live journal, `Esc` close |
| Open files | `t` empty the file (reclaims its space, process keeps running), `c` close the descriptor |

Every destructive action asks for confirmation. The mouse works too: click a tab to switch, click a column header to sort by it (click again to reverse), click a row to open it, scroll to move the selection.

### Copying text out

While whytop is asking the terminal for mouse events — which is what makes clicking a row and scrolling the wheel work — the terminal hands your drags to whytop instead of painting a selection. So the gesture that copies a log line everywhere else does nothing here. Two ways out, for two different jobs:

- **`y` in the process panel copies the whole journal.** Not the dozen lines that fit on screen: everything `journalctl` returned, which is usually where the line that explains the failure actually is. It travels by OSC 52, so it lands on the clipboard of the terminal *in front of you* even when whytop is running on a server three SSH hops away. Following is paused at the same time, so the log stops moving while you read what you copied. A few terminals need OSC 52 turned on — it is `set -g set-clipboard on` in tmux, and `Allow applications in terminal to copy to clipboard` or similar elsewhere.
- **`m` hands the mouse back to the terminal**, for everything else: a PID out of the list, a mount path out of the header, half a command line. Native selection works again everywhere on the screen; whytop's own clicking and wheel scrolling stop until you press `m` again. It says so when you do, both ways.

## Updates

whytop checks once every twelve hours whether a newer release exists, forty-five seconds after it starts, and asks: **yes**, **no**, or **remind me later**. `no` means that release is never offered again; `later` brings it back tomorrow. Both are written to `~/.config/whytop/update.json`, so neither answer is forgotten on the next launch. The prompt never appears over a confirmation, a filter you are typing, or an open panel.

Accepting downloads the release archive, **verifies its SHA-256 against the `checksums.txt` published with the release**, and replaces the binary by an atomic rename. A release without a checksums file is refused rather than installed — this is a program that routinely runs as root, and the convenience is not worth an unverified binary.

**If your copy came from a package manager, whytop does not update itself.** It says a newer version exists and gives you the `apt`/`dnf`/`pacman` command instead. That is not timidity: Debian Policy forbids a package modifying files outside dpkg's knowledge, dpkg would revert the replacement on the next upgrade anyway, and a self-updating binary is a blocker for getting into a distribution archive at all. The same applies inside a container image, where the answer is to pull a new image.

To turn the check off entirely:

```bash
whytop -no-update-check           # this run
export WHYTOP_NO_UPDATE_CHECK=1   # always
```

`NO_UPDATE_CHECK` and `DO_NOT_TRACK` are honoured too. It is the only network call whytop ever makes, it sends nothing but the request, and it is off on any machine that has already said it does not phone home.

## Security

whytop runs as root and can kill processes, restart units, edit unit files and empty a running process's files, so it's built on one rule: **whytop must never let anyone do something through it that they could not do themselves.**

- No listening socket, ever — whytop inherits whatever access the shell it's run from already has, nothing more.
- **Nothing runs through a shell.** Every external call is `exec` with an argument list, and unit names — which reach whytop from cgroup paths and `systemctl` output, not from a human typing them — are validated against systemd's own character set before they become arguments. A name like `--version` is refused rather than read by `systemctl` as an option.
- **Untrusted text can't drive your terminal.** A process's argv, a unit description, a journal line and a descriptor's target are all chosen by someone else and drawn into a root operator's terminal. Control characters in them are stripped at the render boundary, so an `ESC[2J` in a process name shows up as visible `·[2J` instead of clearing your screen and repainting a convincing fake prompt.
- **Signals can't hit a recycled PID.** Between drawing a row and confirming a kill, a process can exit and the kernel can hand its number to something else. Every signal re-checks the target's start time and refuses if it changed.
- **Emptying a file is checked against the descriptor's owner, not against root.** Opening `/proc/<pid>/fd/<n>` re-opens the target with *whytop's* credentials — so without a check, any local user holding a read-only descriptor on a root-owned file could have root empty it for them. whytop applies the permission check the kernel would have applied to the process's own user, and refuses anything that isn't a regular file. The descriptor is also re-read at the moment of action and must still point where it did when you confirmed.
- **Privileged actions are audited.** Every signal, restart, `daemon-reload`, file-empty and descriptor-close is logged to syslog under `AUTHPRIV`, naming the `SUDO_USER` behind it. Read-only browsing is never logged — the point is the changes.
- Refuses to signal PID 1, or itself, or restart scopes and user sessions — and the same goes for touching their open descriptors.
- `t` (empty a file) is the fix for "df says full, du finds nothing": a deleted file whose space the kernel won't reclaim while something still holds it open. It returns the space and leaves the descriptor valid, so the process keeps running.
- `c` (close a descriptor) is the blunt one. The kernel has no syscall for closing someone else's descriptor, so whytop attaches gdb and calls `close()` in the target's own context. The process is never told, and will get `EBADF` the next time it touches that descriptor — it may fail or crash. Prefer `t`.
- Editing a unit file needs write access to it (root, normally) and never runs `daemon-reload` on its own — it always asks first.

Root is only needed for full visibility (other users' sockets, disk I/O, open files). Prefer capabilities over full root where your kernel supports it:

```bash
sudo setcap cap_sys_ptrace,cap_dac_read_search,cap_sys_admin+ep ./whytop
```

## Packaging

Debian packaging lives in `debian/`: the package builds with `dh-golang` against Debian's own Go libraries, as a position-independent executable, reproducibly, with the update check removed at build time so the packaged binary makes no network connection at all. `docs/PACKAGING.md` is the route into the Debian archive, what hardening actually applies (and why CIS benchmarks do not certify a package), and the faster alternatives if the archive is too slow.

```bash
sudo mk-build-deps -i -t 'apt-get -y --no-install-recommends' debian/control
dpkg-buildpackage -us -uc -b
lintian --info --display-info --pedantic ../whytop_*.changes
```

All 23 Go modules whytop links are already packaged in Debian, so there is no dependency work to do first — which is the part that normally stops a Go program getting in.

## Build

```bash
make build      # bin/whytop, static
make run        # build and run with sudo
```

Releases: push a `v*` tag; GitHub Actions runs goreleaser (tar.gz, deb, rpm).

## License

MIT
