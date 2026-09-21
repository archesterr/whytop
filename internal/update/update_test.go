package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// whytop's version string is spelled three different ways depending on who
// built it, and an updater that compares them as strings offers a release
// as an upgrade to itself. Every row here is a spelling that actually
// reaches this code.
func TestVersionComparison(t *testing.T) {
	cases := []struct {
		cur, rel string
		want     bool
		why      string
	}{
		{"v3.2.0", "v3.2.1", true, "a patch release"},
		{"3.2.0", "v3.2.1", true, "goreleaser drops the v, the tag keeps it"},
		{"v3.2.1", "v3.2.1", false, "the version it is already running"},
		{"v3.2.1", "v3.2.0", false, "an older release must never be offered"},
		{"v3.9.0", "v3.10.0", true, "10 is after 9, which a string compare gets backwards"},
		{"v3.2.1", "v4.0.0", true, "a major release"},
		{"v3.2", "v3.2.1", true, "a two-part version is 3.2.0"},
		{"3.2.1-1ubuntu2", "v3.2.1", false, "a distribution revision is the same upstream release"},
		{"3.2.1-1", "v3.2.2", true, "and it still compares against later ones"},
		{"v3.3.0-rc1", "v3.3.0", true, "the release supersedes its own candidate"},
		{"v3.3.0", "v3.3.0-rc1", false, "but the candidate never supersedes the release"},
		{"dev", "v3.2.1", false, "a dev build is not a release and is not updated"},
		{"v3.2.1-4-gabc1234", "v3.3.0", false, "a build past a tag is newer than anything published"},
		{"v3.2.1-dirty", "v3.3.0", false, "and so is a dirty tree"},
		{"", "v3.2.1", false, "an unset version tells us nothing"},
		{"v3.2.1", "", false, "and neither does an unset release"},
		{"v3.2.1", "not-a-version", false, "garbage is never an upgrade"},
	}
	for _, c := range cases {
		if got := IsNewer(c.cur, c.rel); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v — %s", c.cur, c.rel, got, c.want, c.why)
		}
	}
}

// "No" has to mean no across restarts. A prompt that comes back every time
// whytop starts is one people dismiss without reading, which is exactly the
// reflex you do not want on a program that can replace its own binary.
func TestDecisionsSurviveARestart(t *testing.T) {
	now := time.Now()
	var s State

	if !s.Due("v3.3.0", now) {
		t.Error("a fresh install should be offered an update")
	}

	s.Skipped = "v3.3.0"
	if s.Due("v3.3.0", now) {
		t.Error("a version the operator declined was offered again")
	}
	if s.Due("3.3.0", now) {
		t.Error("declining v3.3.0 did not cover the same release spelled without the v")
	}
	if !s.Due("v3.4.0", now) {
		t.Error("declining one release must not silence every release after it")
	}

	s = State{RemindAfter: now.Add(RemindInterval)}
	if s.Due("v3.3.0", now) {
		t.Error("a postponed prompt came back immediately")
	}
	if !s.Due("v3.3.0", now.Add(RemindInterval+time.Minute)) {
		t.Error("a postponed prompt never came back")
	}
}

func TestStateRoundTrips(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	want := State{Skipped: "v3.3.0", RemindAfter: time.Now().Add(time.Hour).Round(time.Second)}
	if err := SaveState(want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got := LoadState()
	if got.Skipped != want.Skipped || !got.RemindAfter.Equal(want.RemindAfter) {
		t.Errorf("loaded %+v, saved %+v", got, want)
	}
}

// A corrupt state file must not be fatal and must not be permanent. The
// worst it may cost is one extra prompt.
func TestCorruptStateIsIgnored(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "whytop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "whytop", "update.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := LoadState(); s.Skipped != "" {
		t.Errorf("a corrupt state file produced %+v", s)
	}
	if !LoadState().Due("v3.3.0", time.Now()) {
		t.Error("a corrupt state file suppressed the prompt permanently")
	}
}

// The environment variables people set on machines that must not phone home.
func TestUpdateCheckCanBeTurnedOff(t *testing.T) {
	for _, k := range []string{"WHYTOP_NO_UPDATE_CHECK", "NO_UPDATE_CHECK", "DO_NOT_TRACK"} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, "1")
			if !Disabled() {
				t.Errorf("%s=1 did not disable the update check", k)
			}
		})
	}
	t.Run("unset", func(t *testing.T) {
		for _, k := range []string{"WHYTOP_NO_UPDATE_CHECK", "NO_UPDATE_CHECK", "DO_NOT_TRACK"} {
			t.Setenv(k, "")
		}
		if Disabled() {
			t.Error("the update check is off with nothing set")
		}
	})
	t.Run("explicitly off", func(t *testing.T) {
		t.Setenv("DO_NOT_TRACK", "0")
		if Disabled() {
			t.Error("DO_NOT_TRACK=0 should mean no, not yes")
		}
	})
}

// A packaged copy must never replace itself: dpkg owns that file, would
// overwrite the replacement on the next upgrade, and Debian Policy forbids
// it outright. This is the check that keeps whytop eligible for the archive.
func TestPackagedCopiesRefuseToSelfUpdate(t *testing.T) {
	for _, in := range []Install{
		{Method: Packaged, Manager: "apt", Path: "/usr/bin/whytop"},
		{Method: Container, Manager: "container image", Path: "/usr/bin/whytop"},
	} {
		if in.CanSelfUpdate() {
			t.Errorf("a %s copy offered to replace its own binary", in.Manager)
		}
		err := Apply(t.Context(), Release{Tag: "v9.9.9"}, in)
		if err == nil {
			t.Fatalf("Apply on a %s copy returned no error", in.Manager)
		}
		if !strings.Contains(err.Error(), in.Manager) {
			t.Errorf("the refusal does not name who owns the binary: %v", err)
		}
	}
	for _, in := range []Install{{Method: SelfManaged}, {Method: Unknown}} {
		if !in.CanSelfUpdate() {
			t.Errorf("a %v copy refused to update itself", in.Method)
		}
	}
}

// goreleaser names the archives; this code has to ask for the same name it
// publishes, and the two live in different files that nothing else connects.
func TestArchiveNameMatchesGoreleaserTemplate(t *testing.T) {
	cfg, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Skip("no goreleaser config to check against")
	}
	const want = `name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"`
	if !strings.Contains(string(cfg), want) {
		t.Errorf("the archive template changed; Release.ArchiveName must change with it.\nwant a line containing: %s", want)
	}
	name := Release{Version: "3.3.0"}.ArchiveName()
	if !strings.HasPrefix(name, "whytop_3.3.0_") || !strings.HasSuffix(name, ".tar.gz") {
		t.Errorf("ArchiveName() = %q", name)
	}
}

func tarball(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	want := []byte("\x7fELF pretend binary")
	got, err := extractBinary(tarball(t, "whytop", want))
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted %q, want %q", got, want)
	}

	// An archive that does not contain whytop is refused rather than
	// producing whatever else happened to be in it.
	if _, err := extractBinary(tarball(t, "README.md", []byte("hello"))); err == nil {
		t.Error("an archive with no whytop in it extracted something anyway")
	}
	// A path traversal in the archive has nowhere to go: extractBinary
	// returns bytes and never touches the filesystem, and replace() writes
	// only to the path of the binary already running. The archive's own
	// names never decide where anything lands.
	if _, err := extractBinary(tarball(t, "../../etc/cron.d/whytop", []byte("payload"))); err == nil {
		if _, statErr := os.Stat("/etc/cron.d/whytop"); statErr == nil {
			t.Fatal("extracting an archive wrote a file outside the target path")
		}
	}
	if _, err := extractBinary([]byte("not a gzip stream")); err == nil {
		t.Error("a non-archive extracted without error")
	}
	if _, err := extractBinary(tarball(t, "whytop", nil)); err == nil {
		t.Error("an empty binary was accepted")
	}
}

// The checksum is the only thing standing between a release download and
// root on the machine, so the parser has to be exact about which line it
// trusts.
func TestChecksumLookup(t *testing.T) {
	sum := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	sums := fmt.Sprintf("%s  whytop_3.3.0_linux_arm64.tar.gz\n%s  whytop_3.3.0_linux_amd64.tar.gz\n", other, sum)

	got, err := checksumFor(sums, "whytop_3.3.0_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("checksumFor: %v", err)
	}
	if got != sum {
		t.Errorf("got the wrong line's checksum: %s", got)
	}
	if _, err := checksumFor(sums, "whytop_3.3.0_linux_riscv64.tar.gz"); err == nil {
		t.Error("a file that is not listed returned a checksum anyway")
	}
	if _, err := checksumFor("deadbeef  whytop_3.3.0_linux_amd64.tar.gz\n", "whytop_3.3.0_linux_amd64.tar.gz"); err == nil {
		t.Error("a truncated hash was accepted")
	}
}

// A release with no checksums file is not installed. Convenience is not
// worth installing an unverified binary over a tool that runs as root.
func TestUnverifiableReleaseIsRefused(t *testing.T) {
	rel := Release{
		Tag: "v9.9.9", Version: "9.9.9",
		Assets: map[string]string{},
	}
	rel.Assets[rel.ArchiveName()] = "https://example.invalid/whytop.tar.gz"
	err := Apply(t.Context(), rel, Install{Method: SelfManaged, Path: "/nonexistent/whytop"})
	if err == nil || !strings.Contains(err.Error(), "checksums") {
		t.Errorf("Apply without checksums.txt returned %v, want a refusal naming checksums", err)
	}
}

// A release that has no build for this machine says so, instead of failing
// somewhere further in with a less obvious message.
func TestMissingBuildForThisMachine(t *testing.T) {
	rel := Release{Tag: "v9.9.9", Version: "9.9.9", Assets: map[string]string{"checksums.txt": "https://example.invalid/c"}}
	err := Apply(t.Context(), rel, Install{Method: SelfManaged, Path: "/nonexistent/whytop"})
	if err == nil || !strings.Contains(err.Error(), "no build for this machine") {
		t.Errorf("Apply returned %v, want a message about there being no build", err)
	}
}

// The swap is atomic and preserves the binary's mode: whytop is very often
// setuid-adjacent in people's heads and always needs to stay executable.
func TestReplaceIsAtomicAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "whytop")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replace(path, []byte("new")); err != nil {
		t.Fatalf("replace: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new" {
		t.Errorf("binary is %q after the swap", b)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode is %v after the swap, want 0755", info.Mode().Perm())
	}
	// Nothing left behind: a directory that accumulates .whytop-update-*
	// files every run is a bug people find months later.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Errorf("replace left %d files in the directory, want 1", len(ents))
	}
}

func TestChecksumMismatchIsDetected(t *testing.T) {
	body := tarball(t, "whytop", []byte("binary"))
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) == strings.Repeat("0", 64) {
		t.Fatal("implausible")
	}
	// The mismatch path is exercised through checksumFor + the comparison in
	// Apply; this pins that a differing byte changes the digest, which is
	// the property the comparison relies on.
	other := tarball(t, "whytop", []byte("binary "))
	if sha256.Sum256(other) == sum {
		t.Error("two different archives hashed the same")
	}
}

// The dpkg query is the whole basis for standing down on a packaged copy,
// so it is checked against a real package database rather than a mock: a
// query that silently never matches would turn every apt-installed copy
// back into a self-updating one, which is the outcome this is here to
// prevent.
func TestPackageOwnerRecognisesARealPackagedFile(t *testing.T) {
	if _, err := exec.LookPath("dpkg-query"); err != nil {
		t.Skip("no dpkg on this machine")
	}
	// /usr/bin/env belongs to coreutils on every Debian-family system.
	const packaged = "/usr/bin/env"
	if _, err := os.Stat(packaged); err != nil {
		t.Skip("no " + packaged + " to ask about")
	}
	pkg, mgr, upgrade, ok := packageOwner(t.Context(), packaged)
	if !ok {
		t.Fatalf("dpkg owns %s but packageOwner said it was unmanaged", packaged)
	}
	if mgr != "apt" {
		t.Errorf("manager is %q, want apt", mgr)
	}
	if !strings.Contains(pkg, packaged) {
		t.Errorf("the owning package line is %q", pkg)
	}
	if !strings.Contains(upgrade, "apt") || !strings.Contains(upgrade, "whytop") {
		t.Errorf("the upgrade command is %q, which does not name apt and whytop", upgrade)
	}

	// And a file no package owns is not claimed. A false positive here
	// would silently disable self-update for everyone who built from source.
	tmp := filepath.Join(t.TempDir(), "whytop")
	if err := os.WriteFile(tmp, []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := packageOwner(t.Context(), tmp); ok {
		t.Errorf("%s is owned by no package but was claimed as packaged", tmp)
	}
}

// A distribution build must contain no path that reaches the network. The
// runtime check in Detect already refuses to replace a packaged binary, but
// a maintainer should not have to take a runtime check's word for it, and
// debian/rules sets this at link time.
func TestABuildCanHaveTheCheckCompiledOut(t *testing.T) {
	for _, k := range []string{"WHYTOP_NO_UPDATE_CHECK", "NO_UPDATE_CHECK", "DO_NOT_TRACK"} {
		t.Setenv(k, "")
	}
	if Disabled() {
		t.Fatal("the check is off before anything turned it off")
	}
	old := BuiltWithoutUpdateCheck
	t.Cleanup(func() { BuiltWithoutUpdateCheck = old })

	BuiltWithoutUpdateCheck = "1"
	if !Disabled() {
		t.Error("a build with the check compiled out still checks")
	}
	// Spelled the way a linker flag might reasonably be given it.
	for _, v := range []string{"yes", "true", "on"} {
		BuiltWithoutUpdateCheck = v
		if !Disabled() {
			t.Errorf("BuiltWithoutUpdateCheck=%q did not disable the check", v)
		}
	}
	for _, v := range []string{"", "0", "false", "False"} {
		BuiltWithoutUpdateCheck = v
		if Disabled() {
			t.Errorf("BuiltWithoutUpdateCheck=%q disabled the check", v)
		}
	}
}
