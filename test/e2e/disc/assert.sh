#!/usr/bin/env bash
# Shared assertions for the disc e2e suite (test/e2e/disc). Sourced by
# lib.sh, before lib.sh's own functions are defined. These assertions
# call lib.sh's log, fail and run_tool only when they run.
set -euo pipefail

# assert_dirs_equal GOT WANT fails unless GOT and WANT hold the same
# files with the same content.
assert_dirs_equal() {
	local got="$1" want="$2"
	if ! diff -rq "$got" "$want"; then
		fail "restored tree at $got does not match the source at $want"
	fi
}

# assert_empty_dir_restored ROOT SRC fails unless the empty directory
# gen_small_tree wrote is a directory under the restored tree.
assert_empty_dir_restored() {
	local root="$1" src="$2"
	if [ ! -d "$root$src/adir" ]; then
		fail "restored tree at $root$src has no adir/ directory"
	fi
}

# assert_listing_matches MOUNT WORK diffs the Go reader's snapshot
# listing against reference/decoder.py's, over the newest snapshot on a
# mounted disc tree at MOUNT.
assert_listing_matches() {
	local mnt="$1" work="$2" snap
	run_tool ci-list "$mnt" >"$work/go-list.txt"
	# ci-list names the snapshot it walked, by the digest alone; the
	# decoder takes the multihash text form, which adds the 1220 prefix.
	snap="1220$(awk '/^# snapshot /{print $3}' "$work/go-list.txt")"
	python3 "$ROOT/reference/decoder.py" summary "$mnt"
	python3 "$ROOT/reference/decoder.py" list "$mnt" --snapshot "$snap" >"$work/py-list.txt"
	python3 "$ROOT/reference/decoder.py" verify "$mnt"

	grep -v '^#' "$work/go-list.txt" | sort >"$work/go-paths.sorted.txt"
	grep -v '^#' "$work/py-list.txt" | awk '{print $NF}' | sort >"$work/py-paths.sorted.txt"
	if ! diff -u "$work/py-paths.sorted.txt" "$work/go-paths.sorted.txt"; then
		fail "decoder.py and the Go reader disagree on the snapshot listing"
	fi
}

# assert_refused LABEL CMD... runs CMD, expecting a nonzero exit and a
# stderr message naming the capacity.
assert_refused() {
	local label="$1" out code
	shift
	set +e
	out="$("$@" 2>&1)"
	code=$?
	set -e
	if [ "$code" -eq 0 ]; then
		echo "$out" >&2
		fail "$label: expected a nonzero exit, got 0"
	fi
	if ! grep -qi "capacity" <<<"$out"; then
		echo "$out" >&2
		fail "$label: expected the refusal message to name the capacity"
	fi
	log "$label: refused as expected: $(echo "$out" | tail -1)"
}

# expect_exit CODE TEXT CMD... runs CMD with no terminal on standard
# input. It fails unless CMD exits CODE and its output holds the fixed
# text TEXT. It sets EXPECT_OUT to the output.
EXPECT_OUT=""
expect_exit() {
	local want="$1" text="$2" code
	shift 2
	set +e
	EXPECT_OUT="$("$@" 2>&1 </dev/null)"
	code=$?
	set -e
	echo "$EXPECT_OUT"
	if [ "$code" -ne "$want" ]; then
		fail "$*: exit $code, want $want"
	fi
	if ! grep -qF -- "$text" <<<"$EXPECT_OUT"; then
		fail "$*: the output does not hold: $text"
	fi
}

# assert_sparse IMAGE WANT_BYTES fails unless IMAGE's apparent size is
# WANT_BYTES and its real disk use stays a small fraction of that.
assert_sparse() {
	local image="$1" want_bytes="$2" apparent real limit
	apparent="$(stat --format='%s' "$image")"
	real="$(du --block-size=1 "$image" | cut -f1)"
	if [ "$apparent" -ne "$want_bytes" ]; then
		fail "image $image: apparent size $apparent, want $want_bytes"
	fi
	limit=$((apparent / 10))
	if [ "$real" -ge "$limit" ]; then
		fail "image $image: real use $real is not far below apparent size $apparent; the image is not sparse"
	fi
}
