# Getting whytop into Debian

This is the route to the official Debian archive, what is already done, and
what only you can do. Ubuntu follows from Debian automatically, so this is the
same document for both.

Read the [Debian New Maintainers' Guide][nmg], [Debian Policy][policy] and the
[Go Packaging Policy][gopolicy] alongside it. Where this document and Policy
disagree, Policy is right.

[nmg]: https://www.debian.org/doc/manuals/maint-guide/
[policy]: https://www.debian.org/doc/debian-policy/
[gopolicy]: https://go-team.pages.debian.net/packaging.html

## The short version

whytop is in an unusually good position for a Go program. **All 23 modules it
links are already packaged in Debian**, so there is no dependency work — which
is normally the part that takes months and stops people.

What is left is the ordinary path: file an ITP, get the packaging reviewed,
find a Debian Developer to sponsor the upload, and wait out the NEW queue.

## What is already in the repository

| File | What it is for |
|---|---|
| `debian/control` | Source and binary package, with every Go library as an explicit build dependency |
| `debian/rules` | dh-golang build: PIE, `-trimpath`, update check compiled out |
| `debian/copyright` | DEP-5, licence text verified to match `LICENSE` byte for byte |
| `debian/changelog` | `3.3.0-1 UNRELEASED` — the ITP bug number goes in `Closes:` |
| `debian/watch` | So `uscan` sees new upstream tags |
| `debian/tests/` | autopkgtest: smoke, no-self-update, hardening |
| `debian/salsa-ci.yml` | Salsa's shared pipeline — lintian, piuparts, reprotest, blhc |
| `debian/README.Debian` | Tells the administrator that apt owns updates, and what `setcap` costs |
| `debian/upstream/metadata` | Bug tracker and repository, which lintian wants |
| `man/whytop.1` | Policy requires a manual page for every binary in `/usr/bin` |
| `.github/workflows/debian.yml` | Builds the package and runs lintian on every pull request |

Two upstream changes were made for this, both in the previous release:

- A packaged binary refuses at runtime to replace itself, naming the package
  manager instead.
- `internal/update.BuiltWithoutUpdateCheck`, set by `debian/rules`, removes the
  check from the binary entirely. Verified by A/B: a source build tagged
  `v1.0.0` prompts about v3.2.1 after ~48 seconds; the same build with the flag
  set runs for 73 seconds, prompts nothing, and writes no state file.

## Steps

### 1. Decide you are the maintainer

Someone has to answer bug reports in Debian's BTS for as long as whytop is in
the archive. That is a real commitment and it is the first question a sponsor
will ask. If you would rather someone else carried it, say so in the ITP and
ask for a co-maintainer.

### 2. File the ITP

An ITP ("Intent To Package") is a wishlist bug against `wnpp`. It is how the
project finds out you are working on this, and it is what stops two people
packaging the same thing.

```bash
sudo apt install reportbug devscripts
reportbug --email you@example.com wnpp
```

Choose **ITP**. The subject must read exactly:

```
ITP: whytop -- live terminal UI for troubleshooting Linux servers
```

The body needs package name, version (3.3.0), upstream author, URL, licence
(Expat/MIT), programming language (Go), and the long description — take it from
`debian/control`. Add a paragraph on why Debian should carry it; "it is like
htop but explains why" is a better answer than a feature list.

You will get a bug number back. **Put it in `debian/changelog`** in place of
`#NNNNNN`.

### 3. Join the Go team

Go packages in Debian are team-maintained. Do not do this alone:

- Request an account on [salsa.debian.org](https://salsa.debian.org).
- Ask to join [go-team](https://salsa.debian.org/go-team) — the
  `debian-go@lists.debian.org` list is where to ask.
- Push to `salsa.debian.org/go-team/packages/whytop`; `debian/control` already
  points its `Vcs-*` fields there.

Subscribe to `debian-go@lists.debian.org` and say what you are doing. The team
is the fastest route to a sponsor, because they already read Go packaging.

It is worth running `dh-make-golang` over the import path once and diffing its
output against `debian/`, as a check on conventions this document may have got
wrong:

```bash
sudo apt install dh-make-golang
dh-make-golang github.com/archesterr/whytop
```

### 4. Build it, and make lintian silent

```bash
sudo apt install build-essential devscripts dh-golang golang-any lintian equivs
sudo mk-build-deps -i -t 'apt-get -y --no-install-recommends' debian/control
dpkg-buildpackage -us -uc -b
lintian --info --display-info --pedantic ../whytop_*.changes
```

Aim for silence at `--pedantic`, not merely no errors. Every warning you leave
is a question a sponsor has to ask you about. An override belongs in
`debian/source/lintian-overrides` with a comment saying why — and a sponsor
will read that comment, so a real reason is needed.

`.github/workflows/debian.yml` runs exactly this on every pull request, with
`--fail-on warning,error`, so it should not drift.

### 5. Run the tests the archive runs

```bash
sudo apt install autopkgtest piuparts reprotest
autopkgtest ../whytop_*.deb -- null          # or: -- lxc debian/sid
sudo piuparts ../whytop_*.deb                # install / upgrade / purge leaves nothing
reprotest --vary=-build_path 'dpkg-buildpackage -us -uc -b' ../whytop_*.deb
```

The build is already reproducible: two builds from different source
directories produce a byte-identical binary, because `debian/rules` passes
`-trimpath`.

Pushing to Salsa runs all of these in the shared pipeline, which is what a
sponsor will look at first.

### 6. Make an upstream release a sponsor can verify

The archive wants a tarball matching a signed tag, not a GitHub convenience
download:

```bash
git tag -s v3.3.0 -m 'v3.3.0'        # signed, not -a
git push origin v3.3.0
```

Then `gbp import-orig --uscan --pristine-tar`. `debian/gbp.conf` is set up for
`debian/sid` and `upstream/latest` branches with pristine-tar.

If you have no OpenPGP key yet, make one now — you need it for this, for
mentors.debian.net, and eventually for Debian Maintainer status.

### 7. Find a sponsor

You cannot upload to Debian yourself yet. A Debian Developer must, and they are
putting their name on it. Three routes, in order of how well they work here:

1. **The Go team.** Ask on `debian-go@lists.debian.org`, with a link to your
   Salsa merge request and a green pipeline.
2. **An RFS bug.** Upload to [mentors.debian.net][mentors], then file
   `reportbug sponsorship-requests`.
3. **debian-mentors@lists.debian.org**, for review before either.

[mentors]: https://mentors.debian.net

Expect review comments, and expect some of them to be about things this
document told you to do. Fix them; do not argue the first round.

### 8. The NEW queue

A package never in Debian goes through the NEW queue, where the FTP team review
the licensing by hand. **This takes weeks to months** and there is no way to
speed it up. `debian/copyright` being exactly right is the thing that makes it
shorter rather than longer.

After it clears, whytop is in unstable. It migrates to testing after about ten
days with no release-critical bugs, and appears in the next stable release.
Ubuntu imports it from Debian automatically.

## Hardening, and what CIS actually covers

**CIS Benchmarks do not certify packages.** They are configuration baselines
for an operating system or platform — the Debian Linux Benchmark tells you to
set `kernel.randomize_va_space`, mount `/tmp` with `nodev`, configure auditd.
There is no CIS benchmark a program can pass, and any claim that whytop "is CIS
compliant" would be meaningless.

What is real is in two parts.

### whytop as a package on a hardened host

These are checked by `debian/tests/hardening`, which runs under autopkgtest and
in the pull request CI:

| Property | Why | Checked |
|---|---|---|
| Position-independent executable | ASLR, which the CIS guidance on `randomize_va_space` assumes | `readelf -h` says `DYN` |
| Not setuid or setgid | Privilege is the administrator's to grant, per run | mode has no `s` bit |
| Ships no file capabilities | A package that shipped `cap_sys_admin` would grant it to every installation | `getcap` is empty |
| No daemon, no listening socket, no unit | Nothing to attack when nobody is running it | no `.service` in the package |
| Reproducible | The binary can be shown to come from this source | two builds, byte-identical |
| No network access at all | An archive package should not phone home | A/B verified, above |

Two more that are properties of the program rather than the package, already
true and documented in the README's security section: every privileged action
is logged to syslog under `AUTHPRIV` naming `SUDO_USER`, which is what a CIS
audit requirement about privileged command logging is actually asking for; and
nothing runs through a shell.

### whytop's own privileges

`setcap cap_sys_ptrace,cap_dac_read_search,cap_sys_admin+ep` is documented in
the manual page and `README.Debian` as the administrator's choice, not the
package's default. Be straight with reviewers about `cap_sys_admin` being
broad — a hardened host may reasonably decline it and run whytop unprivileged
with some columns reading "needs root".

### Standards that do apply to a project

If you want a badge that means something, these are the ones that fit:

- **[OpenSSF Best Practices][openssf]** — a self-certification questionnaire.
  whytop already meets most of it: licence, version control, tests, static
  analysis, no known vulnerabilities, a documented security posture.
- **[OpenSSF Scorecard][scorecard]** — automated, and would currently flag
  branch protection and pinned GitHub Actions.
- **Reproducible Builds** — already true; `reprotest` in step 5 proves it.

[openssf]: https://www.bestpractices.dev/
[scorecard]: https://github.com/ossf/scorecard

## Faster routes, if the archive is too slow

The archive is months. These are days, and they are not mutually exclusive
with it:

- **Your own apt repository** — `aptly` or `reprepro` over the `.deb`
  goreleaser already builds, signed with your key, served over HTTPS. Users add
  one line to `sources.list`. This is an afternoon.
- **An Ubuntu PPA** — Launchpad builds from the same `debian/` directory. No
  sponsor, no NEW queue, no dependency packaging.

Both give people `apt install whytop` today. The official archive gives
something neither does: whytop on a machine whose operator never heard of it,
and Debian's security team tracking its dependencies. That is worth the wait,
but it is not a reason to make people wait for it.
