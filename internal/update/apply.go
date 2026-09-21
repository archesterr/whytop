package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Replacing the binary a machine's operators reach for when something is
// wrong is not a routine download, and this file is written accordingly.
//
// Three things are checked before anything on disk moves. The archive's
// SHA-256 must match the checksums file published with the release, so a
// truncated or tampered download cannot be installed. The extracted file
// must be a plain file under a bounded size, so an archive cannot write
// through a symlink or a .. path into somewhere else. And the replacement
// is written beside the current binary and renamed over it, so the swap is
// atomic — whytop is either the old version or the new one, never a
// half-written file that no longer runs.

// maxBinary bounds what will be extracted. whytop is a few megabytes; the
// limit exists so a hostile or corrupt archive cannot fill the filesystem
// that whytop's own users are usually trying to diagnose.
const maxBinary = 64 << 20

// Apply downloads the release archive, verifies it, and replaces the running
// binary. The caller must have established that this copy is self-managed;
// see Install.CanSelfUpdate.
func Apply(ctx context.Context, rel Release, in Install) error {
	if !in.CanSelfUpdate() {
		return fmt.Errorf("this copy is managed by %s", in.Manager)
	}
	if in.Path == "" {
		return errors.New("cannot locate the running binary")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	name := rel.ArchiveName()
	archiveURL, ok := rel.Assets[name]
	if !ok {
		return fmt.Errorf("release %s has no build for this machine (%s)", rel.Tag, name)
	}
	sumsURL, ok := rel.Assets["checksums.txt"]
	if !ok {
		// Refusing is the right answer. An unverified binary installed over
		// a monitoring tool that routinely runs as root is not a tradeoff
		// worth making to save someone a manual download.
		return errors.New("release has no checksums.txt; refusing to install an unverified binary")
	}

	sums, err := fetch(ctx, sumsURL, 1<<20)
	if err != nil {
		return fmt.Errorf("checksums: %w", err)
	}
	want, err := checksumFor(string(sums), name)
	if err != nil {
		return err
	}

	archive, err := fetch(ctx, archiveURL, maxBinary)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return errors.New("the downloaded archive does not match its published checksum")
	}

	bin, err := extractBinary(archive)
	if err != nil {
		return err
	}
	return replace(in.Path, bin)
}

func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "whytop")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	// LimitReader with one byte of headroom, so an oversized body is
	// detected rather than silently truncated into a checksum mismatch that
	// reads as corruption.
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than the %d byte limit", url, limit)
	}
	return b, nil
}

// checksumFor pulls one file's hash out of goreleaser's checksums.txt,
// which is the sha256sum format: "<hex>  <name>" per line.
func checksumFor(sums, name string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == name {
			if len(f[0]) != 64 {
				return "", fmt.Errorf("checksum for %s is malformed", name)
			}
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt does not list %s", name)
}

// extractBinary pulls whytop out of the release tarball. It reads the entry
// named whytop and nothing else: the archive's own paths are never used to
// decide where anything lands, so a crafted name like ../../etc/cron.d/x has
// nowhere to go.
func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("archive is not gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != "whytop" {
			continue
		}
		if h.Size > maxBinary {
			return nil, errors.New("the binary in the archive is implausibly large")
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxBinary))
		if err != nil {
			return nil, err
		}
		if len(b) == 0 {
			return nil, errors.New("the binary in the archive is empty")
		}
		return b, nil
	}
	return nil, errors.New("the archive does not contain a whytop binary")
}

// replace swaps the new binary in atomically. The temporary file is created
// in the same directory on purpose: rename is only atomic within a
// filesystem, and /tmp is very often a different one.
func replace(path string, bin []byte) error {
	dir := filepath.Dir(path)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".whytop-update-*")
	if err != nil {
		// Almost always "permission denied" on /usr/local/bin as a non-root
		// user, which is worth saying plainly rather than as an errno.
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	// Flush to disk before the rename. Without this, a machine that loses
	// power between the two has a directory entry pointing at a file whose
	// contents never arrived — and whytop is a tool people install on the
	// machines they least want to be unbootable.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
