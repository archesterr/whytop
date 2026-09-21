// Package packaging holds no code. It exists so that the Debian packaging in
// debian/ can be checked against the source it packages by `go test ./...`,
// which is what contributors and CI already run.
//
// The failure this prevents is specific and expensive. Someone adds an import,
// the Go build is happy, the pull request is merged — and the next person to
// run dpkg-buildpackage finds a missing build dependency. In the good case
// that person is you. In the bad case it is the Debian Developer who agreed to
// sponsor the upload, spending their afternoon on a mistake a test could have
// caught in a second.
package packaging

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// debianName maps a Go module path to the source package Debian gives it, by
// the Go Packaging Policy's naming rules: the host with dots replaced by
// dashes, then the path, then -dev. Two hosts are special: github.com is
// shortened to github, and golang.org drops its "org" — golang.org/x/crypto
// is golang-golang-x-crypto-dev. A major-version suffix is part of the module
// path but not of the package name.
func debianName(mod string) string {
	p := strings.TrimSuffix(mod, "/")
	for _, v := range []string{"/v2", "/v3", "/v4", "/v5", "/v6"} {
		p = strings.TrimSuffix(p, v)
	}
	parts := strings.Split(p, "/")
	host := strings.ReplaceAll(parts[0], ".", "-")
	switch host {
	case "github-com":
		host = "github"
	case "golang-org":
		// golang.org/x/crypto is golang-golang-x-crypto-dev in Debian, not
		// golang-golang-org-x-crypto-dev. The "org" is dropped. Getting
		// this wrong does not fail loudly: it names a package that does not
		// exist, apt says it cannot satisfy the build dependency, and the
		// obvious reading is that Debian has not packaged x/crypto — which
		// it has, and has for years. Caught by the workflow that asks apt
		// what the suite actually has.
		host = "golang"
	}
	rest := strings.Join(parts[1:], "-")
	return strings.ToLower("golang-" + host + "-" + rest + "-dev")
}

func TestDebianNaming(t *testing.T) {
	cases := map[string]string{
		"github.com/muesli/termenv":           "golang-github-muesli-termenv-dev",
		"github.com/shirou/gopsutil/v4":       "golang-github-shirou-gopsutil-dev",
		"github.com/charmbracelet/x/cellbuf":  "golang-github-charmbracelet-x-cellbuf-dev",
		"github.com/aymanbagabas/go-osc52/v2": "golang-github-aymanbagabas-go-osc52-dev",
		// The "org" is dropped for golang.org/x/*, which is not a rule you
		// would guess — these are the two that were wrong in debian/control.
		"golang.org/x/crypto": "golang-golang-x-crypto-dev",
		"golang.org/x/sys":    "golang-golang-x-sys-dev",
		"golang.org/x/text":   "golang-golang-x-text-dev",
	}
	for mod, want := range cases {
		if got := debianName(mod); got != want {
			t.Errorf("debianName(%q) = %q, want %q", mod, got, want)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// buildDepends reads the build dependencies out of debian/control.
func buildDepends(t *testing.T, root string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "debian", "control"))
	if err != nil {
		t.Skipf("no debian/control: %v", err)
	}
	out := map[string]bool{}
	inField := false
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, "Build-Depends:"):
			inField = true
			line = strings.TrimPrefix(line, "Build-Depends:")
		case inField && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")):
			// a continuation line
		default:
			inField = false
			continue
		}
		for _, dep := range strings.Split(line, ",") {
			// Strip a version constraint: "debhelper-compat (= 13)".
			if i := strings.Index(dep, "("); i >= 0 {
				dep = dep[:i]
			}
			if dep = strings.TrimSpace(dep); dep != "" {
				out[dep] = true
			}
		}
	}
	return out
}

// linkedModules asks the toolchain which modules actually end up in the
// binary — not what go.mod lists, which includes modules reached only by
// other modules' tests and which Debian would not need.
func linkedModules(t *testing.T, root string) []string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "whytop")
	build := exec.Command("go", "build", "-o", bin, "./cmd/whytop")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build to inspect dependencies: %v\n%s", err, out)
	}
	info, err := exec.Command("go", "version", "-m", bin).Output()
	if err != nil {
		t.Skipf("go version -m: %v", err)
	}
	var mods []string
	for _, line := range strings.Split(string(info), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "dep" {
			mods = append(mods, f[1])
		}
	}
	if len(mods) == 0 {
		t.Skip("no dependency information in the binary")
	}
	return mods
}

// Every module linked into whytop must have its Debian library package in
// Build-Depends, or the package does not build in the archive.
func TestEveryDependencyIsAlsoADebianBuildDependency(t *testing.T) {
	root := repoRoot(t)
	deps := buildDepends(t, root)

	for _, mod := range linkedModules(t, root) {
		want := debianName(mod)
		if !deps[want] {
			t.Errorf("%s is linked into whytop but debian/control does not build-depend on %s.\n"+
				"Add it to Build-Depends, and check the package exists:\n"+
				"    apt-cache show %s", mod, want, want)
		}
	}
}

// And the other way: a build dependency nothing needs is one the archive
// installs on every builder for no reason, and one more package whose
// removal from Debian would block whytop.
func TestNoUnusedDebianBuildDependencies(t *testing.T) {
	root := repoRoot(t)
	deps := buildDepends(t, root)

	needed := map[string]bool{
		// Not Go libraries: the toolchain and helpers dh needs.
		"debhelper-compat": true,
		"dh-golang":        true,
		"golang-any":       true,
	}
	for _, mod := range linkedModules(t, root) {
		needed[debianName(mod)] = true
	}
	for dep := range deps {
		if !needed[dep] {
			t.Errorf("debian/control build-depends on %s, which nothing in whytop links.\n"+
				"Remove it, or say in a comment why the build needs it.", dep)
		}
	}
}

// Policy requires a manual page for every program in /usr/bin, and
// debian/whytop.manpages is what installs it. A renamed or moved page would
// otherwise fail at lintian time rather than here.
func TestTheManualPageIsShipped(t *testing.T) {
	root := repoRoot(t)
	listed, err := os.ReadFile(filepath.Join(root, "debian", "whytop.manpages"))
	if err != nil {
		t.Skipf("no debian/whytop.manpages: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(listed)), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, line)); err != nil {
			t.Errorf("debian/whytop.manpages lists %s, which does not exist", line)
		}
	}
}

// The version debian/changelog declares has to be a version the updater's own
// comparison understands, because a Debian revision reaches that code as the
// running version. 3.3.0-1 must read as the same upstream release as v3.3.0.
func TestTheChangelogVersionIsOneWeCanCompare(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "debian", "changelog"))
	if err != nil {
		t.Skipf("no debian/changelog: %v", err)
	}
	first := strings.SplitN(string(b), "\n", 2)[0]
	open, close := strings.Index(first, "("), strings.Index(first, ")")
	if open < 0 || close < open {
		t.Fatalf("cannot read a version out of %q", first)
	}
	ver := first[open+1 : close]
	if !strings.Contains(ver, "-") {
		t.Errorf("changelog version %q has no Debian revision", ver)
	}
	if strings.HasPrefix(ver, "v") {
		t.Errorf("changelog version %q starts with v; Debian versions do not", ver)
	}
}
