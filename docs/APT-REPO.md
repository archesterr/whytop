# The whytop apt repository

`apt install whytop`, without waiting for the Debian archive. Packages are
served from GitHub Pages, signed with your key, rebuilt from every release.

This is not a substitute for `docs/PACKAGING.md` — the official archive gives
you things this cannot, chiefly Debian's security team tracking your
dependencies and whytop reaching people who never heard of it. It is a
substitute for making people wait months.

## How it works

`.github/workflows/apt.yml` runs when a release is published:

1. Downloads every `.deb` from every published release.
2. Runs `packaging/apt/build-repo.sh`, which lays out `pool/` and `dists/`,
   writes the `Packages` and `Release` files, and signs `InRelease` and
   `Release.gpg`.
3. Publishes the public key and a landing page beside them.
4. **Installs whytop from the repository it just built, with real apt**, and
   runs the hardening and no-self-update tests against it.
5. Deploys to GitHub Pages.

Step 4 is the one worth keeping. A signature or layout mistake found there
costs one workflow run; found after deployment it costs every user an error on
every `apt update` until you notice.

The repository is rebuilt from scratch every time rather than appended to.
There is no database to drift from what is published: delete a release and it
leaves the repository on the next run, re-run the workflow and the output is
identical.

## Setting it up — once

### 1. Make a signing key

Its own key, not your personal one. If it leaks, you revoke a repository key
rather than your Debian and git identity.

```bash
export GNUPGHOME=$(mktemp -d)        # keep it out of your normal keyring
cat > /tmp/whytop-key <<'EOF'
%no-protection
Key-Type: eddsa
Key-Curve: ed25519
Key-Usage: sign
Name-Real: whytop repository signing key
Name-Email: arminney77@gmail.com
Expire-Date: 0
%commit
EOF
gpg --batch --gen-key /tmp/whytop-key
gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/{print $10; exit}'
```

`%no-protection` means no passphrase — required, because the workflow cannot
type one. That makes the exported private key a secret worth treating as one.

Ed25519, not RSA: smaller, and apt has supported it for years.

**Back the key up somewhere you will still have it in three years.** Losing it
means every existing installation starts failing `apt update`, and the only
fix is every user re-running the install block with a new key.

```bash
gpg --armor --export-secret-keys --output whytop-signing-key.asc <fingerprint>
# then: into your password manager, and off this machine
```

### 2. Give it to the workflow

```bash
gpg --armor --export-secret-keys <fingerprint> | gh secret set APT_GPG_PRIVATE_KEY
```

Or paste it into **Settings → Secrets and variables → Actions → New secret**,
named `APT_GPG_PRIVATE_KEY`. The workflow fails with an explanation rather
than publishing unsigned if it is missing.

### 3. Turn on Pages

**Settings → Pages → Source: GitHub Actions.** Not "deploy from a branch" —
the workflow deploys an artifact directly, and there is no `gh-pages` branch.

### 4. Run it

```bash
gh workflow run apt-repo
```

It runs on every published release after that. Use `workflow_dispatch` to
rebuild without cutting a release — after deleting an old release, say.

### 5. Check it from a machine that is not yours

```bash
docker run --rm -it debian:trixie bash -c '
  apt update -qq && apt install -y -qq curl ca-certificates
  install -d -m 0755 /usr/share/keyrings
  curl -fsSL https://archesterr.github.io/whytop/whytop-archive-keyring.gpg \
    > /usr/share/keyrings/whytop-archive-keyring.gpg
  cat > /etc/apt/sources.list.d/whytop.sources <<EOF
Types: deb
URIs: https://archesterr.github.io/whytop
Suites: stable
Components: main
Signed-By: /usr/share/keyrings/whytop-archive-keyring.gpg
EOF
  apt update && apt install -y whytop && whytop -version'
```

## What users run

The landing page at `https://archesterr.github.io/whytop` has the copy-paste
block. It binds the key to this repository with `Signed-By`, which matters: a
key added to `trusted.gpg.d` can vouch for **any** repository on the machine,
including one that later turns hostile.

Put the same block in the README so people find it without a detour.

## Building the repository yourself

Nothing here is GitHub-specific. `build-repo.sh` needs only `dpkg-dev` and
`gnupg`, and its output is a static directory any web server can serve:

```bash
packaging/apt/build-repo.sh --debs ./debs --out ./public --sign <fingerprint>
rsync -a --delete public/ you@yourserver:/var/www/apt/
```

Serve it over HTTPS. apt verifies signatures regardless, so plain HTTP is not
a forgery risk — but it does tell anyone watching the network exactly what
your servers run, which is not a thing to volunteer.

## Operating it

**Rotating the key.** Publish the new one alongside the old, sign with both
for a release or two if you can, and tell people to re-fetch. There is no way
to do this silently: every installation pins the key it was given.

**Removing a release.** Delete the GitHub release, re-run the workflow. Its
packages leave the repository, because the repository is a function of what
exists now.

**Keeping old versions.** `build-repo.sh` passes `-m` to `dpkg-scanpackages`,
so every version stays listed and `apt install whytop=3.2.1-1` keeps working.
That matters the day a release is bad and someone needs the previous one at
two in the morning.

**A release with no `.deb`.** The workflow fails rather than publishing an
empty repository. An apt source that resolves and offers nothing is harder to
diagnose than one that is not there — apt reports success and the package
simply cannot be found.

## Verified, not assumed

The builder was tested end to end before it shipped: four real packages across
two architectures and two versions, signed with a throwaway key, served over
HTTP, and consumed by real `apt` — `apt-get update` with no signature warning,
`apt-cache policy` listing both versions with 3.3.0-1 as the candidate,
`apt-get install` putting a working binary on the system, and `dpkg -S`
confirming dpkg owns it.

The same check runs in the workflow on every publish.
