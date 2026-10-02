#!/usr/bin/env bash
# RUN ONLY BEFORE THE FIRST TAG. After the first tag, the fixtures of
# this directory are frozen: nobody makes them again, and
# TestFormat1FixturesUnchanged fails when a file changes.
#
# make.sh makes the frozen format-1 disc-root fixtures again with the
# noahsark binary of this source tree. For each fixture it writes the
# disc root of each disc, one expected-REF.txt for each ref, and at the
# end SHA256SUMS. Run it from the repository root:
#
#   bash cmd/noahsark/testdata/format1/make.sh
#
# An expected-REF.txt line is "PATH<TAB>d<TAB>MODE<TAB>MTIME" for a
# directory, "PATH<TAB>f<TAB>MODE<TAB>MTIME<TAB>SIZE<TAB>SHA256" for a
# file and "PATH<TAB>l<TAB>TARGET" for a symlink, sorted by bytes.
set -euo pipefail

here="$(CDPATH='' cd "$(dirname "$0")" && pwd)"
w="$(mktemp -d)"
trap 'rm -rf "$w"' EXIT
go build -o "$w/noahsark" ./cmd/noahsark
N="$w/noahsark"

# expected SRC prints the expected lines of the source tree SRC.
expected() {
	local src="$1" p
	(
		cd "$src"
		find . -mindepth 1 | LC_ALL=C sort | while IFS= read -r p; do
			p="${p#./}"
			if [ -L "$p" ]; then
				printf '%s\tl\t%s\n' "$p" "$(readlink "$p")"
			elif [ -d "$p" ]; then
				printf '%s\td\t%s\t%s\n' "$p" "$(stat -c %a "$p")" "$(stat -c %Y "$p")"
			else
				printf '%s\tf\t%s\t%s\t%s\t%s\n' "$p" "$(stat -c %a "$p")" "$(stat -c %Y "$p")" \
					"$(stat -c %s "$p")" "$(sha256sum <"$p" | cut -c 1-64)"
			fi
		done
	) | LC_ALL=C sort
}

# settle SRC gives every entry of SRC but a symlink a fixed mode and a
# fixed modification time, the deepest first.
settle() {
	local src="$1"
	find "$src" -mindepth 1 -type f -exec chmod 0644 {} +
	find "$src" -mindepth 1 -type d -exec chmod 0755 {} +
	find "$src" -mindepth 1 ! -type l -printf '%d\t%p\n' | LC_ALL=C sort -rn | cut -f2- |
		while IFS= read -r p; do touch -d @1726272000 "$p"; done
}

# disc NAME TREE copies the disc root TREE to the fixture NAME.
disc() {
	mkdir -p "$here/$1"
	cp -a "$2/NOAHSARK" "$here/$1/NOAHSARK"
}

rm -rf "$here/one-file" "$here/two-files" "$here/two-discs" "$here/SHA256SUMS"
export TZ=UTC

# one-file: one small file.
src="$w/one-file/src" repo="$w/one-file/repo"
mkdir -p "$src" "$repo"
printf 'hello from the one-file fixture\n' >"$src/hello.txt"
settle "$src"
(cd "$repo" && "$N" init >/dev/null)
"$N" --repo="$repo" commit --ref=ONE "$src" >/dev/null
"$N" --repo="$repo" pack --capacity=64MiB --out="$w/one-file/tree" >/dev/null
disc one-file/disc "$w/one-file/tree"
expected "$src" >"$here/one-file/expected-ONE.txt"

# two-files: two files in a subdirectory, an empty file, an empty
# directory and a symlink.
src="$w/two-files/src" repo="$w/two-files/repo"
mkdir -p "$src/sub" "$src/empty-dir" "$repo"
printf 'hello from the two-files fixture\n' >"$src/sub/hello.txt"
printf 'second file content here\n' >"$src/sub/second.txt"
: >"$src/empty"
ln -s sub/hello.txt "$src/link"
settle "$src"
(cd "$repo" && "$N" init >/dev/null)
"$N" --repo="$repo" commit --ref=TWO "$src" >/dev/null
"$N" --repo="$repo" pack --capacity=64MiB --out="$w/two-files/tree" >/dev/null
disc two-files/disc "$w/two-files/tree"
expected "$src" >"$here/two-files/expected-TWO.txt"

# two-discs: snapshot first on disc0; snapshot second adds a file on
# disc1, and needs the sub tree and the chunk of one.txt of disc0.
src="$w/two-discs/src" repo="$w/two-discs/repo"
mkdir -p "$src/sub" "$repo"
printf 'first disc file\n' >"$src/sub/one.txt"
settle "$src"
(cd "$repo" && "$N" init >/dev/null)
"$N" --repo="$repo" commit --ref=first "$src" >/dev/null
"$N" --repo="$repo" pack --capacity=64MiB --out="$w/two-discs/tree0" >/dev/null
expected "$src" >"$w/expected-first.txt"
printf 'second disc file\n' >"$src/two.txt"
settle "$src"
"$N" --repo="$repo" commit --ref=second "$src" >/dev/null
"$N" --repo="$repo" pack --capacity=64MiB --out="$w/two-discs/tree1" >/dev/null
disc two-discs/disc0 "$w/two-discs/tree0"
disc two-discs/disc1 "$w/two-discs/tree1"
cp "$w/expected-first.txt" "$here/two-discs/expected-first.txt"
expected "$src" >"$here/two-discs/expected-second.txt"

(cd "$here" && find one-file two-files two-discs -type f | LC_ALL=C sort | xargs sha256sum) >"$here/SHA256SUMS"
