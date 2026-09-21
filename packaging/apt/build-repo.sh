#!/bin/sh
# Build a signed apt repository from a directory of .deb files.
#
# The whole repository is regenerated from the .debs every time, which is what
# makes this safe to run from CI: there is no database to keep in sync with
# what is published, no state to corrupt, and the output is a function of its
# input. reprepro and aptly are both better tools for a repository someone
# curates by hand; neither is better for one rebuilt by a workflow.
#
# Needs only dpkg-dev and gnupg. Deliberately not apt-ftparchive: that lives
# in apt-utils, which is not installed on a minimal Debian, and the Release
# file it writes is short enough to write here.
#
# Usage:
#   build-repo.sh --debs DIR --out DIR [--sign KEYID] [--suite NAME] [--origin NAME]

set -eu

DEBS=""
OUT=""
SIGN=""
SUITE="stable"
ORIGIN="whytop"
LABEL="whytop"
COMPONENT="main"
DESCRIPTION="whytop — live terminal UI for troubleshooting Linux servers"

usage() {
	sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
	exit "${1:-0}"
}

while [ $# -gt 0 ]; do
	case "$1" in
		--debs)   DEBS="$2"; shift 2 ;;
		--out)    OUT="$2"; shift 2 ;;
		--sign)   SIGN="$2"; shift 2 ;;
		--suite)  SUITE="$2"; shift 2 ;;
		--origin) ORIGIN="$2"; LABEL="$2"; shift 2 ;;
		-h|--help) usage 0 ;;
		*) echo "unknown argument: $1" >&2; usage 1 ;;
	esac
done

[ -n "$DEBS" ] && [ -n "$OUT" ] || { echo "--debs and --out are required" >&2; usage 1; }
[ -d "$DEBS" ] || { echo "no such directory: $DEBS" >&2; exit 1; }

command -v dpkg-scanpackages >/dev/null 2>&1 || {
	echo "dpkg-scanpackages is missing; install dpkg-dev" >&2; exit 1; }

# Refuse to build an empty repository. An apt source that resolves and offers
# nothing is harder to diagnose than one that is not there: apt reports
# success, and the package simply cannot be found.
count=$(find "$DEBS" -name '*.deb' -type f | wc -l)
[ "$count" -gt 0 ] || { echo "no .deb files in $DEBS" >&2; exit 1; }
echo "building from $count package(s)"

rm -rf "$OUT"
mkdir -p "$OUT/pool/$COMPONENT/w/whytop"
find "$DEBS" -name '*.deb' -type f -exec cp {} "$OUT/pool/$COMPONENT/w/whytop/" \;

cd "$OUT"

# Which architectures are actually present. Listing an architecture in Release
# that has no Packages file makes apt report a missing file on every update,
# on every machine, forever.
ARCHES=$(for f in pool/"$COMPONENT"/w/whytop/*.deb; do
	dpkg-deb -f "$f" Architecture
done | sort -u)
echo "architectures: $(echo "$ARCHES" | tr '\n' ' ')"

for arch in $ARCHES; do
	dir="dists/$SUITE/$COMPONENT/binary-$arch"
	mkdir -p "$dir"
	# -m keeps every version rather than only the newest, so a downgrade to a
	# known-good release stays possible without hunting for the .deb.
	dpkg-scanpackages -m --arch "$arch" pool /dev/null > "$dir/Packages" 2>/dev/null
	gzip -9cn "$dir/Packages" > "$dir/Packages.gz"
	# An empty Packages file means the --arch filter matched nothing, which
	# means this loop and dpkg-deb disagree about the architecture name.
	[ -s "$dir/Packages" ] || { echo "no packages for $arch" >&2; exit 1; }
	printf '  %s: %s package(s)\n' "$arch" "$(grep -c '^Package:' "$dir/Packages")"
done

# The Release file. Date must be RFC 2822 in UTC, or apt rejects it on
# machines in other timezones.
release="dists/$SUITE/Release"
{
	echo "Origin: $ORIGIN"
	echo "Label: $LABEL"
	echo "Suite: $SUITE"
	echo "Codename: $SUITE"
	echo "Architectures: $(echo "$ARCHES" | tr '\n' ' ' | sed 's/ *$//')"
	echo "Components: $COMPONENT"
	echo "Description: $DESCRIPTION"
	echo "Date: $(LC_ALL=C date -u '+%a, %d %b %Y %H:%M:%S UTC')"
} > "$release"

# The checksums apt verifies each index against. Paths are relative to the
# Release file's own directory.
for algo in MD5Sum:md5sum SHA256:sha256sum; do
	field=${algo%%:*}
	prog=${algo##*:}
	echo "$field:" >> "$release"
	(cd "dists/$SUITE" && find "$COMPONENT" -type f | sort | while read -r f; do
		printf ' %s %16d %s\n' "$($prog "$f" | cut -d' ' -f1)" "$(wc -c < "$f")" "$f"
	done) >> "$release"
done

if [ -n "$SIGN" ]; then
	# InRelease is the signed-in-place file apt prefers; Release.gpg is the
	# detached signature older clients look for. Both, because costing
	# nothing is the whole argument for shipping both.
	rm -f "dists/$SUITE/InRelease" "dists/$SUITE/Release.gpg"
	gpg --batch --yes --local-user "$SIGN" --clearsign -o "dists/$SUITE/InRelease" "$release"
	gpg --batch --yes --local-user "$SIGN" -abs -o "dists/$SUITE/Release.gpg" "$release"
	echo "signed with $SIGN"

	# Verify what was just written rather than assuming gpg did it. A
	# repository whose signature does not check out fails on every user's
	# machine at once, and this is the last moment it is cheap to notice.
	gpg --verify "dists/$SUITE/InRelease" >/dev/null 2>&1 || {
		echo "the InRelease signature does not verify" >&2; exit 1; }
	gpg --verify "dists/$SUITE/Release.gpg" "$release" >/dev/null 2>&1 || {
		echo "the detached signature does not verify" >&2; exit 1; }
	echo "signatures verified"
else
	echo "WARNING: unsigned repository — apt will refuse it unless every" >&2
	echo "         client passes [trusted=yes], which disables the check" >&2
	echo "         that makes a repository safe to add. Use --sign." >&2
fi

echo "repository built in $OUT"
