#!/usr/bin/env bash
# The recover e2e scenario: sourced by run.sh. Proves a lost
# repository directory is fully recoverable from discs alone: recover
# runs one time for each disc and restores the catalog, the state log,
# the disc ledger and the refs. log, ls and restore then work, and a
# later pack dedups against what the discs already hold instead of
# burning everything again. See run.sh for the shared scenario dispatch
# and lib.sh for the pack, image, mount, recover and restore helpers.
set -euo pipefail

# REBUILD_BASE_BYTES and REBUILD_ADD_BYTES size the base fixture and the
# second commit's added content. Both are overridable so a local run can
# use a small, fast fixture; the CI matrix cell uses the real defaults
# below, well under one dvd+r disc's usable capacity.
REBUILD_BASE_BYTES="${NOAHSARK_E2E_REBUILD_BASE_BYTES:-1000000000}"
REBUILD_ADD_BYTES="${NOAHSARK_E2E_REBUILD_ADD_BYTES:-100000000}"
REBUILD_SEED="${NOAHSARK_E2E_REBUILD_SEED:-20260914}"

# rebuild_assert_log_ls REPO SNAP LABEL fails unless log lists the
# snapshot SNAP and ls --recursive lists its entries, both from the
# catalog of REPO.
rebuild_assert_log_ls() {
	local repo="$1" snap="$2" label="$3" log_out ls_out
	log_out="$("$BIN" --repo="$repo" log)"
	echo "$log_out"
	if ! grep -qF "${snap:4:12}" <<<"$log_out"; then
		fail "rebuild: $label: log did not list snapshot $snap"
	fi
	ls_out="$("$BIN" --repo="$repo" ls --recursive "$snap")"
	if [ -z "$ls_out" ]; then
		fail "rebuild: $label: ls --recursive printed nothing"
	fi
	log "rebuild: $label: ls --recursive listed $(echo "$ls_out" | wc -l) entries"
}

scenario_rebuild() {
	local work="$WORK/rebuild"
	local repo="$work/repo" src="$work/src"
	local hashes_base="$work/base.hashes" plan="$work/plan.txt" hashes_next="$work/next.hashes"
	# restore reads each disc at rmnt. mnt1 and mnt2 hold the two discs
	# for verify and recover.
	local rmnt="$work/rmnt"
	build_binary

	(mkdir -p "$repo" && cd "$repo" && "$BIN" init)

	local t0 t1
	t0=$(date +%s)
	run_tool ci-incremental-fixture gen "$src" "$REBUILD_BASE_BYTES" "$REBUILD_SEED" "$hashes_base" "$plan"
	t1=$(date +%s)
	log "rebuild: base fixture generation took $((t1 - t0))s, $(du -sh "$src" | cut -f1)"

	local commit_out1 snap1
	t0=$(date +%s)
	commit_out1="$("$BIN" --repo="$repo" commit --ref=BASE "$src")"
	t1=$(date +%s)
	echo "$commit_out1"
	snap1="$(awk '/^snapshot /{print $2}' <<<"$commit_out1")"
	log "rebuild: commit BASE took $((t1 - t0))s"

	local tree1="$work/tree1" image1="$work/disc1.img" mnt1="$work/mnt1" uuid1
	t0=$(date +%s)
	# shellcheck disable=SC2046 # media_capacity_flags is a list of flags
	pack_disc "$repo" $(media_capacity_flags "$FIXED_MEDIA") --out="$tree1"
	t1=$(date +%s)
	uuid1="$PACKED_UUID"
	pack_rate_line "rebuild: pack disc 1" "$REBUILD_BASE_BYTES" "$t0" "$t1"
	local size1
	size1="$(du -sb "$tree1" | cut -f1)"
	log "rebuild: disc 1 packed tree size: $size1 bytes"

	image_build "$repo" "$uuid1" "$image1"
	mount_ro "$image1" "$mnt1"
	local index_count1
	index_count1="$(run_tool ci-index-count "$mnt1")"
	verify_counted "$repo" "$mnt1" "$uuid1" "$index_count1"

	# log and ls read the catalog of the repository.
	rebuild_assert_log_ls "$repo" "$snap1" "before the loss"

	# Losing the whole repository directory: config, staging, catalog and
	# the state log all go together.
	rm -rf "$repo"
	log "rebuild: deleted the whole repository directory"

	# recover with disc 1 alone: exit 0, and the state log's on-disc
	# count must equal disc 1's own INDEX object count.
	recover_disc "$repo" "$src" "$mnt1" 0
	grep -qx 'recover: ok' <<<"$RECOVER_OUT" || fail "rebuild: recover of disc 1 did not print recover: ok"
	local ondisc_count1
	ondisc_count1="$(run_tool ci-state-count "$repo/state")"
	if [ "$ondisc_count1" != "$index_count1" ]; then
		fail "rebuild: on-disc count $ondisc_count1 does not equal disc 1 INDEX object count $index_count1"
	fi
	log "rebuild: recover from disc 1 recorded $ondisc_count1 on-disc objects, matching INDEX"

	rebuild_assert_log_ls "$repo" "$snap1" "after recover"

	# restore reads the recovered catalog and the disc. The disc goes to
	# rmnt, one mount at a time.
	umount_if_mounted "$mnt1"
	local restored_whole="$work/restored-whole"
	restore_loop "$repo" "$rmnt" "$snap1" "$restored_whole"
	assert_dirs_equal "$restored_whole" "$src"
	log "rebuild: whole-snapshot restore after recover matches"

	# One single file and one whole directory, named by PATH arguments
	# relative to the source root.
	local one_file one_dir rel_file rel_dir
	one_file="$(find "$src" -maxdepth 1 -type f -name 'large-*' -print -quit)"
	one_dir="$(find "$src" -mindepth 1 -maxdepth 1 -type d -print -quit)"
	[ -n "$one_file" ] || fail "rebuild: no top-level large-* file found in the fixture"
	[ -n "$one_dir" ] || fail "rebuild: no top-level directory found in the fixture"
	rel_file="${one_file#"$src"/}"
	rel_dir="${one_dir#"$src"/}"

	local restored_file="$work/restored-file"
	restore_loop "$repo" "$rmnt" "$snap1" "$rel_file" "$restored_file"
	diff -q "$restored_file/$rel_file" "$one_file" || fail "rebuild: PATH restore of $one_file does not match"

	local restored_dir="$work/restored-dir"
	restore_loop "$repo" "$rmnt" "$snap1" "$rel_dir" "$restored_dir"
	assert_dirs_equal "$restored_dir/$rel_dir" "$one_dir"
	log "rebuild: PATH restore of one file and one directory matches"
	umount_if_mounted "$rmnt"

	# Change the source: about REBUILD_ADD_BYTES of new files, a rewritten
	# file and a few appended files (ci-incremental-fixture's mutate),
	# commit as NEXT and pack the change alone to a second dvd+r disc.
	t0=$(date +%s)
	run_tool ci-incremental-fixture mutate "$src" "$REBUILD_ADD_BYTES" "$REBUILD_SEED" "$plan" "$hashes_base" "$hashes_next"
	t1=$(date +%s)
	log "rebuild: mutate took $((t1 - t0))s, $(du -sh "$src" | cut -f1)"

	local commit_out2 snap2
	commit_out2="$("$BIN" --repo="$repo" commit --ref=NEXT "$src")"
	echo "$commit_out2"
	snap2="$(awk '/^snapshot /{print $2}' <<<"$commit_out2")"

	local tree2="$work/tree2" image2="$work/disc2.img" mnt2="$work/mnt2" uuid2
	t0=$(date +%s)
	# shellcheck disable=SC2046 # media_capacity_flags is a list of flags
	pack_disc "$repo" $(media_capacity_flags "$FIXED_MEDIA") --out="$tree2"
	t1=$(date +%s)
	uuid2="$PACKED_UUID"
	pack_rate_line "rebuild: pack disc 2" "$REBUILD_ADD_BYTES" "$t0" "$t1"
	local size2
	size2="$(du -sb "$tree2" | cut -f1)"
	log "rebuild: disc 2 packed tree size: $size2 bytes (disc 1 was $size1 bytes)"

	local size_limit=$((size1 * 25 / 100))
	if [ "$size2" -ge "$size_limit" ]; then
		fail "rebuild: disc 2 size $size2 is not under 25% of disc 1's $size1; recover did not prevent re-packing disc 1's content"
	fi

	image_build "$repo" "$uuid2" "$image2"
	mount_ro "$image2" "$mnt2"
	verify_counted "$repo" "$mnt2" "$uuid2"
	# verify's own output does not carry the DISCS row count; read it
	# straight from DISCS.bin with the same Go reader verify uses.
	local field_out2
	field_out2="$(run_tool ci-disc-field "$mnt2")"
	echo "$field_out2"
	if ! grep -qE 'discs: 2$' <<<"$field_out2"; then
		fail "rebuild: disc 2's DISCS table does not record 2 discs (expected disc 1 as a prerequisite)"
	fi
	umount_if_mounted "$mnt2"

	# NEXT needs disc 1: a restore that never gets disc 1 must stop and
	# ask for it, after it read disc 2.
	disc_insert "$uuid2" "$rmnt"
	restore_expect_missing "$repo" "$rmnt" "$uuid1" "$snap2" "$work/restored-next-missing-disc1"

	local restored_next="$work/restored-next"
	restore_loop "$repo" "$rmnt" "$snap2" "$restored_next"
	run_tool ci-incremental-fixture check "$restored_next" "$hashes_next"
	log "rebuild: NEXT restored from both discs matches"
	umount_if_mounted "$rmnt"

	# Lose the repository again, and recover it from both discs: exit 0,
	# and the on-disc count equals the distinct objects that the two
	# discs list.
	rm -rf "$repo"
	mount_ro "$image1" "$mnt1"
	mount_ro "$image2" "$mnt2"
	recover_disc "$repo" "$src" "$mnt1" 0
	recover_disc "$repo" "$src" "$mnt2" 0
	# Both discs carry the snapshot BASE, thus count distinct objects.
	local ondisc_count2 want_count2
	ondisc_count2="$(run_tool ci-state-count "$repo/state")"
	want_count2="$(run_tool ci-index-count "$mnt1" "$mnt2")"
	if [ "$ondisc_count2" != "$want_count2" ]; then
		fail "rebuild: on-disc count after 2-disc rebuild is $ondisc_count2, want $want_count2 (distinct objects of the disc 1 and disc 2 INDEX)"
	fi

	# A repeat recover from the same two discs must be idempotent.
	recover_disc "$repo" "$src" "$mnt1" 0
	grep -q '^recover: ok; disc .* already known$' <<<"$RECOVER_OUT" ||
		fail "rebuild: a repeat recover of disc 1 did not report the disc as known"
	recover_disc "$repo" "$src" "$mnt2" 0
	local ondisc_count3
	ondisc_count3="$(run_tool ci-state-count "$repo/state")"
	if [ "$ondisc_count3" != "$ondisc_count2" ]; then
		fail "rebuild: on-disc count changed on a repeat 2-disc rebuild: $ondisc_count2 then $ondisc_count3"
	fi
	log "rebuild: 2-disc rebuild is idempotent at $ondisc_count2 on-disc objects"

	# recover with only disc 2: exit 1, naming disc 1's uuid.
	rm -rf "$repo"
	local disc_uuid1
	disc_uuid1="$(run_tool ci-disc-uuid "$mnt1")"
	if [ "$disc_uuid1" != "$uuid1" ]; then
		fail "rebuild: disc 1 carries uuid $disc_uuid1, pack printed $uuid1"
	fi
	recover_disc "$repo" "$src" "$mnt2" 1
	if ! grep -qF "$uuid1" <<<"$RECOVER_OUT"; then
		fail "rebuild: recover-with-disc-2-only did not name the missing disc 1 ($uuid1)"
	fi
	log "rebuild: recover with only disc 2 refused as expected, naming disc $uuid1"

	umount_if_mounted "$mnt1"
	umount_if_mounted "$mnt2"
	log "rebuild PASS"
}
