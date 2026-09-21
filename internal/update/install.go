package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// How whytop got onto this machine decides what an update even means, and
// getting that wrong is not a cosmetic problem.
//
// A binary that belongs to a distribution package must never replace itself.
// Debian Policy is explicit that a package may not install or modify files
// outside the package manager's knowledge, and dpkg would in any case
// overwrite the replacement on the next upgrade, silently reverting it —
// or refuse, having found the file's checksum changed underneath it. Since
// the point of this project is to end up in the official archive, the
// self-updater has to be the part that stands down there, and say so.
//
// So the answer is not "update or don't". It is "who is responsible for
// updating this copy", and for a packaged copy the answer is apt.
type Method int

const (
	// Unknown covers a build from source, a `go install`, and anything else
	// nobody has claimed. Treated as self-managed.
	Unknown Method = iota
	// SelfManaged is a binary the operator put somewhere themselves,
	// typically /usr/local/bin from a release tarball. whytop may replace it.
	SelfManaged
	// Packaged is a file some package manager owns. whytop must not touch
	// it; the operator upgrades through that package manager.
	Packaged
	// Container is a binary inside an image, where the filesystem is a
	// layer and a replacement lasts until the container is recreated.
	Container
)

// Install describes how this copy of whytop is managed.
type Install struct {
	Method Method
	// Path is the running binary, resolved through symlinks.
	Path string
	// Manager names who owns it — "apt", "dnf", "docker" — for the message
	// that tells the operator how to upgrade instead.
	Manager string
	// Upgrade is the command to run, when someone else owns this copy.
	Upgrade string
}

// CanSelfUpdate reports whether whytop may replace its own binary.
func (i Install) CanSelfUpdate() bool {
	return i.Method == SelfManaged || i.Method == Unknown
}

// Detect works out who manages the running binary. It runs the package
// manager's query tools, which are read-only, and gives up quickly: this
// runs behind an update check nobody is waiting on, and a slow or wedged
// dpkg database must not delay it.
func Detect(ctx context.Context) Install {
	in := Install{Method: Unknown}
	exe, err := os.Executable()
	if err != nil {
		return in
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	in.Path = exe

	// A container is worth catching before the package query, because the
	// image very often *did* install whytop from a .deb — the correct answer
	// there is still "pull a new image", not "run apt".
	if inContainer() {
		in.Method, in.Manager, in.Upgrade = Container, "container image", "docker pull ghcr.io/archesterr/whytop:latest"
		return in
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if owner, mgr, upgrade, ok := packageOwner(ctx, exe); ok {
		in.Method, in.Manager, in.Upgrade = Packaged, mgr, upgrade
		_ = owner
		return in
	}
	in.Method = SelfManaged
	return in
}

// packageOwner asks each package manager present whether it owns the file.
// "Present" matters: dpkg-query on an RPM box is not installed, and running
// it would only produce a confusing error.
func packageOwner(ctx context.Context, path string) (pkg, mgr, upgrade string, ok bool) {
	if runtime.GOOS != "linux" {
		return "", "", "", false
	}
	type query struct {
		bin     string
		args    []string
		mgr     string
		upgrade string
	}
	for _, q := range []query{
		{"dpkg-query", []string{"-S", path}, "apt", "sudo apt update && sudo apt install --only-upgrade whytop"},
		{"rpm", []string{"-qf", path}, "dnf", "sudo dnf upgrade whytop"},
		{"pacman", []string{"-Qo", path}, "pacman", "sudo pacman -Syu whytop"},
	} {
		exe, err := exec.LookPath(q.bin)
		if err != nil {
			continue
		}
		out, err := exec.CommandContext(ctx, exe, q.args...).Output()
		if err != nil {
			continue // "no path found matching" exits non-zero, which is the answer
		}
		if line := strings.TrimSpace(string(out)); line != "" {
			return line, q.mgr, q.upgrade, true
		}
	}
	return "", "", "", false
}

// inContainer looks for the markers a container runtime leaves behind.
// /.dockerenv is Docker's, /run/.containerenv is Podman's, and the
// container= environment variable is what systemd-nspawn and Podman set.
func inContainer() bool {
	if os.Getenv("container") != "" {
		return true
	}
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
