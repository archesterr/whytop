package update

import (
	"strconv"
	"strings"
)

// whytop's own version string arrives from three different places and they
// do not agree on spelling. The Makefile uses `git describe`, which gives
// "v3.2.1" and "v3.2.1-4-gabc1234" on an untagged build. goreleaser sets
// {{.Version}}, which drops the v and gives "3.2.1". A distribution package
// appends its own revision: "3.2.1-1ubuntu2". Comparing any of those as
// strings is how a release announces itself as an update to itself.
type version struct {
	nums []int
	// pre is a pre-release suffix — "rc1" in 3.3.0-rc1. A version that has
	// one sorts *below* the same version without it, per semver, which is
	// what keeps a release candidate from being offered over the release.
	pre string
	// dirty marks a build that is not a release at all: a commit past a tag,
	// or a working tree with changes in it. Those are never updated, because
	// whatever is running is newer than anything published.
	dirty bool
	ok    bool
}

func parseVersion(s string) version {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "whytop")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" || s == "dev" {
		return version{dirty: true}
	}
	// `git describe` marks distance from the tag and a dirty tree the same
	// way it always has: -N-g<sha> and -dirty.
	if strings.HasSuffix(s, "-dirty") {
		return version{dirty: true}
	}
	if i := strings.LastIndex(s, "-g"); i > 0 {
		// -4-gabc1234: four commits past the tag. Only treat it as a build
		// marker if what sits between the dashes is a count.
		if j := strings.LastIndex(s[:i], "-"); j >= 0 {
			if _, err := strconv.Atoi(s[j+1 : i]); err == nil {
				return version{dirty: true}
			}
		}
	}

	var v version
	core := s
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		core, v.pre = s[:i], s[i+1:]
		if strings.HasPrefix(s[i:], "+") {
			v.pre = "" // build metadata is not a pre-release and is ignored
		}
	}
	// A Debian revision ("3.2.1-1ubuntu2") looks exactly like a pre-release
	// suffix and means the opposite — the same upstream, packaged again. It
	// is dropped rather than allowed to sort the version below itself.
	if v.pre != "" && v.pre[0] >= '0' && v.pre[0] <= '9' {
		v.pre = ""
	}
	for _, part := range strings.Split(core, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return version{}
		}
		v.nums = append(v.nums, n)
	}
	if len(v.nums) == 0 {
		return version{}
	}
	v.ok = true
	return v
}

// newer reports whether b is a later release than a. Anything it cannot
// parse, and any build that is not itself a release, answers false: the
// update prompt exists to offer a specific upgrade, and offering one on a
// version nobody can compare is worse than staying quiet.
func newer(a, b version) bool {
	if !a.ok || !b.ok || a.dirty || b.dirty {
		return false
	}
	for i := 0; i < len(a.nums) || i < len(b.nums); i++ {
		x, y := at(a.nums, i), at(b.nums, i)
		if x != y {
			return y > x
		}
	}
	switch {
	case a.pre == b.pre:
		return false
	case a.pre == "": // a is the release, b the candidate for it
		return false
	case b.pre == "": // b is the release of the candidate a
		return true
	}
	return b.pre > a.pre
}

func at(n []int, i int) int {
	if i < len(n) {
		return n[i]
	}
	return 0
}

// IsNewer reports whether released is a later version than current.
func IsNewer(current, released string) bool {
	return newer(parseVersion(current), parseVersion(released))
}
