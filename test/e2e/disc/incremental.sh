#!/usr/bin/env bash
# The incremental e2e scenario: sourced by run.sh. Commits a fixture as
# ref BASE, packs it to a dvd+r disc, mutates the source the way a real
# second commit would, commits it again as ref NEXT, packs the change
# alone to a second dvd+r disc, then restores both snapshots from the
# discs alone. Proves dedup across runs and that a later disc records an
# earlier one as a prerequisite. See run.sh for the shared scenario
# dispatch and lib.sh for build_binary, mount_populate and the media_*
# helpers, and chain.sh for chain_assert_missing_disc.
set -euo pipefail

# INCREMENTAL_BASE_BYTES and INCREMENTAL_ADD_BYTES size the base fixture
# and the second commit's added content. Both are overridable so a local
# run can use a small, fast fixture; the CI matrix cell uses the real
# defaults below, sized well under one dvd+r disc's usable capacity even
# after the base fixture and the added bytes are both counted.
INCREMENTAL_BASE_BYTES="${NOAHSARK_E2E_INCREMENTAL_BASE_BYTES:-1500000000}"
INCREMENTAL_ADD_BYTES="${NOAHSARK_E2E_INCREMENTAL_ADD_BYTES:-300000000}"
INCREMENTAL_SEED="${NOAHSARK_E2E_INCREMENTAL_SEED:-20260914}"

# incremental_new_objects COMMIT_OUTPUT prints the "new objects" count
# from one commit's stdout.
incremental_new_objects() {
	awk '/^new objects:/{print $3}' <<<"$1" | tr -d ','
}

scenario_incremental() {
	local work="$WORK/incremental"
	local repo="$work/repo" src="$work/src"
	local hashes_base="$work/base.hashes" plan="$work/plan.txt" hashes_next="$work/next.hashes"
	build_binary

	"$BIN" init --repo="$repo"

	local t0 t1
	t0=$(date +%s)
	run_tool ci-incremental-fixture gen "$src" "$INCREMENTAL_BASE_BYTES" "$INCREMENTAL_SEED" "$hashes_base" "$plan"
	t1=$(date +%s)
	log "incremental: base fixture generation took $((t1 - t0))s, $(du -sh "$src" | cut -f1)"

	local commit_out1 snap1 new1
	t0=$(date +%s)
	commit_out1="$("$BIN" commit --repo="$repo" --ref=BASE "$src")"
	t1=$(date +%s)
	echo "$commit_out1"
	snap1="$(awk '/^snapshot /{print $2}' <<<"$commit_out1")"
	new1="$(incremental_new_objects "$commit_out1")"
	log "incremental: commit BASE took $((t1 - t0))s, new objects: $new1"

	local tree1="$work/tree1" image1="$work/disc1.img" mnt1="$work/mnt1"
	t0=$(date +%s)
	# shellcheck disable=SC2046 # media_capacity_flags is a list of flags
	"$BIN" pack --repo="$repo" --ref=BASE $(media_capacity_flags "$FIXED_MEDIA") --out="$tree1"
	t1=$(date +%s)
	pack_rate_line "incremental: pack disc 1" "$INCREMENTAL_BASE_BYTES" "$t0" "$t1"
	local size1
	size1="$(du -sb "$tree1" | cut -f1)"
	log "incremental: disc 1 packed tree size: $size1 bytes"

	sudo "$BIN" image build --out="$image1" "--capacity=$(media_image_capacity "$FIXED_MEDIA")" "$tree1"
	mount_populate "$image1" "$tree1" "$mnt1"
	local verify_out1
	verify_out1="$("$BIN" verify --image="$mnt1")"
	echo "$verify_out1"
	if ! echo "$verify_out1" | grep -qE 'discs: 1$'; then
		fail "incremental: disc 1's DISCS table does not record exactly 1 disc"
	fi

	t0=$(date +%s)
	run_tool ci-incremental-fixture mutate "$src" "$INCREMENTAL_ADD_BYTES" "$INCREMENTAL_SEED" "$plan" "$hashes_base" "$hashes_next"
	t1=$(date +%s)
	log "incremental: mutate took $((t1 - t0))s, $(du -sh "$src" | cut -f1)"

	local commit_out2 snap2 new2
	t0=$(date +%s)
	commit_out2="$("$BIN" commit --repo="$repo" --ref=NEXT "$src")"
	t1=$(date +%s)
	echo "$commit_out2"
	snap2="$(awk '/^snapshot /{print $2}' <<<"$commit_out2")"
	new2="$(incremental_new_objects "$commit_out2")"
	log "incremental: commit NEXT took $((t1 - t0))s, new objects: $new2 (BASE had $new1)"

	# Unchanged files must dedup to zero new chunks, so NEXT's new
	# objects must fall well below BASE's: under 40%, the same margin
	# the packed disc size is checked against below.
	local new_limit=$((new1 * 40 / 100))
	if [ "$new2" -ge "$new_limit" ]; then
		fail "incremental: NEXT commit ($new2 new objects) is not under 40% of BASE's ($new1); dedup looks too weak"
	fi

	local tree2="$work/tree2" image2="$work/disc2.img" mnt2="$work/mnt2"
	t0=$(date +%s)
	# shellcheck disable=SC2046 # media_capacity_flags is a list of flags
	"$BIN" pack --repo="$repo" --ref=NEXT $(media_capacity_flags "$FIXED_MEDIA") --out="$tree2"
	t1=$(date +%s)
	pack_rate_line "incremental: pack disc 2" "$INCREMENTAL_ADD_BYTES" "$t0" "$t1"
	local size2
	size2="$(du -sb "$tree2" | cut -f1)"
	log "incremental: disc 2 packed tree size: $size2 bytes (disc 1 was $size1 bytes)"

	local size_limit=$((size1 * 40 / 100))
	if [ "$size2" -ge "$size_limit" ]; then
		fail "incremental: disc 2 size $size2 is not under 40% of disc 1's $size1"
	fi

	sudo "$BIN" image build --out="$image2" "--capacity=$(media_image_capacity "$FIXED_MEDIA")" "$tree2"
	mount_populate "$image2" "$tree2" "$mnt2"
	local verify_out2
	verify_out2="$("$BIN" verify --image="$mnt2")"
	echo "$verify_out2"
	if ! echo "$verify_out2" | grep -qE 'discs: 2$'; then
		fail "incremental: disc 2's DISCS table does not record 2 discs (expected disc 1 as a prerequisite)"
	fi

	# Proves the discs are the only source: restore never reads --repo,
	# but deleting it here matches how the other cells prove the local
	# cache and staging are only an accelerator.
	rm -rf "$repo"
	log "incremental: deleted repo (cache and staging) before restore"

	local restored_base="$work/restored-base"
	"$BIN" restore --disc="$mnt1" "$snap1" "$restored_base"
	run_tool ci-incremental-fixture check "$restored_base$src" "$hashes_base"
	log "incremental: BASE restored from disc 1 alone matches"

	local restored_next="$work/restored-next"
	"$BIN" restore --disc="$mnt1" --disc="$mnt2" "$snap2" "$restored_next"
	run_tool ci-incremental-fixture check "$restored_next$src" "$hashes_next"
	log "incremental: NEXT restored from both discs matches"

	chain_assert_missing_disc "$snap2" "$work/restored-next-missing" "$mnt1" "$mnt2"

	umount_if_mounted "$mnt1"
	umount_if_mounted "$mnt2"
	log "incremental PASS"
}
