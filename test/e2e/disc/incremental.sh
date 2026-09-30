#!/usr/bin/env bash
# The incremental e2e scenario: sourced by run.sh. Commits a fixture as
# ref BASE, packs it to a dvd+r disc, mutates the source the way a real
# second commit would, commits it again as ref NEXT, packs the change
# alone to a second dvd+r disc, deletes the repository, recovers it from
# the two discs, then restores both snapshots. Proves dedup across runs
# and that a later disc records an earlier one as a prerequisite. See
# run.sh for the shared scenario dispatch and lib.sh for the pack,
# image, mount, recover and restore helpers.
set -euo pipefail

# INCREMENTAL_BASE_BYTES and INCREMENTAL_ADD_BYTES size the base fixture
# and the second commit's added content. Both are overridable so a local
# run can use a small, fast fixture; the CI matrix cell uses the real
# defaults below, sized well under one dvd+r disc's usable capacity even
# after the base fixture and the added bytes are both counted.
INCREMENTAL_BASE_BYTES="${NOAHSARK_E2E_INCREMENTAL_BASE_BYTES:-1500000000}"
INCREMENTAL_ADD_BYTES="${NOAHSARK_E2E_INCREMENTAL_ADD_BYTES:-300000000}"
INCREMENTAL_SEED="${NOAHSARK_E2E_INCREMENTAL_SEED:-20260914}"

# incremental_new_objects COMMIT_OUTPUT prints the "new items" count
# from one commit's stdout.
incremental_new_objects() {
	awk '/^new items:/{print $3}' <<<"$1" | tr -d ','
}

scenario_incremental() {
	local work="$WORK/incremental"
	local repo="$work/repo" src="$work/src"
	local hashes_base="$work/base.hashes" plan="$work/plan.txt" hashes_next="$work/next.hashes"
	build_binary

	(mkdir -p "$repo" && cd "$repo" && "$BIN" init)

	local t0 t1
	t0=$(date +%s)
	run_tool ci-incremental-fixture gen "$src" "$INCREMENTAL_BASE_BYTES" "$INCREMENTAL_SEED" "$hashes_base" "$plan"
	t1=$(date +%s)
	log "incremental: base fixture generation took $((t1 - t0))s, $(du -sh "$src" | cut -f1)"

	local commit_out1 snap1 new1
	t0=$(date +%s)
	commit_out1="$("$BIN" --repo="$repo" commit --ref=BASE "$src")"
	t1=$(date +%s)
	echo "$commit_out1"
	snap1="$(awk '/^snapshot /{print $2}' <<<"$commit_out1")"
	new1="$(incremental_new_objects "$commit_out1")"
	require_number "incremental: new items of commit BASE" "$new1"
	log "incremental: commit BASE took $((t1 - t0))s, new objects: $new1"

	local tree1="$work/tree1" image1="$work/disc1.img" mnt1="$work/mnt1" uuid1
	t0=$(date +%s)
	# shellcheck disable=SC2046 # media_capacity_flags is a list of flags
	pack_disc "$repo" $(media_capacity_flags "$FIXED_MEDIA") --out="$tree1"
	t1=$(date +%s)
	uuid1="$PACKED_UUID"
	pack_rate_line "incremental: pack disc 1" "$INCREMENTAL_BASE_BYTES" "$t0" "$t1"
	local size1
	size1="$(du -sb "$tree1" | cut -f1)"
	require_number "incremental: disc 1 packed tree size" "$size1"
	log "incremental: disc 1 packed tree size: $size1 bytes"

	image_build "$repo" "$uuid1" "$image1"
	mount_ro "$image1" "$mnt1"
	verify_counted "$repo" "$mnt1" "$uuid1"
	# verify's own output does not carry the DISCS row count; read it
	# straight from DISCS.bin with the same Go reader verify uses.
	local field_out1
	field_out1="$(run_tool ci-disc-field "$mnt1")"
	echo "$field_out1"
	if ! grep -qE 'discs: 1$' <<<"$field_out1"; then
		fail "incremental: disc 1's DISCS table does not record exactly 1 disc"
	fi

	t0=$(date +%s)
	run_tool ci-incremental-fixture mutate "$src" "$INCREMENTAL_ADD_BYTES" "$INCREMENTAL_SEED" "$plan" "$hashes_base" "$hashes_next"
	t1=$(date +%s)
	log "incremental: mutate took $((t1 - t0))s, $(du -sh "$src" | cut -f1)"

	local commit_out2 snap2 new2
	t0=$(date +%s)
	commit_out2="$("$BIN" --repo="$repo" commit --ref=NEXT "$src")"
	t1=$(date +%s)
	echo "$commit_out2"
	snap2="$(awk '/^snapshot /{print $2}' <<<"$commit_out2")"
	new2="$(incremental_new_objects "$commit_out2")"
	require_number "incremental: new items of commit NEXT" "$new2"
	log "incremental: commit NEXT took $((t1 - t0))s, new objects: $new2 (BASE had $new1)"

	# Unchanged files must dedup to zero new chunks, so NEXT's new
	# objects must fall well below BASE's: under 40%, the same margin
	# the packed disc size is checked against below.
	local new_limit=$((new1 * 40 / 100))
	if [ "$new2" -ge "$new_limit" ]; then
		fail "incremental: NEXT commit ($new2 new objects) is not under 40% of BASE's ($new1); dedup looks too weak"
	fi

	local tree2="$work/tree2" image2="$work/disc2.img" mnt2="$work/mnt2" uuid2
	t0=$(date +%s)
	# shellcheck disable=SC2046 # media_capacity_flags is a list of flags
	pack_disc "$repo" $(media_capacity_flags "$FIXED_MEDIA") --out="$tree2"
	t1=$(date +%s)
	uuid2="$PACKED_UUID"
	pack_rate_line "incremental: pack disc 2" "$INCREMENTAL_ADD_BYTES" "$t0" "$t1"
	local size2
	size2="$(du -sb "$tree2" | cut -f1)"
	require_number "incremental: disc 2 packed tree size" "$size2"
	log "incremental: disc 2 packed tree size: $size2 bytes (disc 1 was $size1 bytes)"

	local size_limit=$((size1 * 40 / 100))
	if [ "$size2" -ge "$size_limit" ]; then
		fail "incremental: disc 2 size $size2 is not under 40% of disc 1's $size1"
	fi

	image_build "$repo" "$uuid2" "$image2"
	mount_ro "$image2" "$mnt2"
	verify_counted "$repo" "$mnt2" "$uuid2"
	local field_out2
	field_out2="$(run_tool ci-disc-field "$mnt2")"
	echo "$field_out2"
	if ! grep -qE 'discs: 2$' <<<"$field_out2"; then
		fail "incremental: disc 2's DISCS table does not record 2 discs (expected disc 1 as a prerequisite)"
	fi

	# Proves the discs are the only source: the repository goes, and
	# recover builds it again from the two discs, one call for each disc.
	# The chunk files of staging are not on the discs, thus restore reads
	# every chunk from a disc.
	rm -rf "$repo"
	log "incremental: deleted repo (catalog and staging) before restore"
	recover_disc "$repo" "$src" "$mnt1" 0
	recover_disc "$repo" "$src" "$mnt2" 0
	umount_if_mounted "$mnt1"
	umount_if_mounted "$mnt2"

	# restore reads each disc at rmnt, one mount at a time.
	local rmnt="$work/rmnt"
	local restored_base="$work/restored-base"
	disc_insert "$uuid1" "$rmnt"
	restore_loop "$repo" "$rmnt" "$snap1" "$restored_base"
	if [ "$RESTORE_SWAPS" -ne 0 ]; then
		fail "incremental: BASE restore asked for $RESTORE_SWAPS more disc(s), want disc 1 alone"
	fi
	run_tool ci-incremental-fixture check "$restored_base" "$hashes_base"
	log "incremental: BASE restored from disc 1 alone matches"

	local restored_next="$work/restored-next"
	restore_loop "$repo" "$rmnt" "$snap2" "$restored_next"
	run_tool ci-incremental-fixture check "$restored_next" "$hashes_next"
	log "incremental: NEXT restored from both discs matches"

	# NEXT needs disc 1: a restore that never gets disc 1 must stop and
	# ask for it, after it read disc 2.
	disc_insert "$uuid2" "$rmnt"
	restore_expect_missing "$repo" "$rmnt" "$uuid1" "$snap2" "$work/restored-next-missing"

	umount_if_mounted "$rmnt"
	log "incremental PASS"
}
