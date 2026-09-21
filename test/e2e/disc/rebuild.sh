#!/usr/bin/env bash
# The recover e2e scenario: sourced by run.sh. Proves a lost
# repository directory is fully recoverable from discs alone: log, ls
# and restore already work with no repository, and recover
# restores the state log, the disc ledger and the refs, so a later pack
# dedups against what the discs already hold instead of burning
# everything again. See run.sh for the shared scenario dispatch and
# lib.sh for build_binary, mount_populate and the media_* helpers, and
# chain.sh for chain_assert_missing_disc.
set -euo pipefail

# REBUILD_BASE_BYTES and REBUILD_ADD_BYTES size the base fixture and the
# second commit's added content. Both are overridable so a local run can
# use a small, fast fixture; the CI matrix cell uses the real defaults
# below, well under one dvd+r disc's usable capacity.
REBUILD_BASE_BYTES="${NOAHSARK_E2E_REBUILD_BASE_BYTES:-1000000000}"
REBUILD_ADD_BYTES="${NOAHSARK_E2E_REBUILD_ADD_BYTES:-100000000}"
REBUILD_SEED="${NOAHSARK_E2E_REBUILD_SEED:-20260914}"

scenario_rebuild() {
	local work="$WORK/rebuild"
	local repo="$work/repo" src="$work/src"
	local hashes_base="$work/base.hashes" plan="$work/plan.txt" hashes_next="$work/next.hashes"
	build_binary

	"$BIN" init --repo="$repo"

	local t0 t1
	t0=$(date +%s)
	run_tool ci-incremental-fixture gen "$src" "$REBUILD_BASE_BYTES" "$REBUILD_SEED" "$hashes_base" "$plan"
	t1=$(date +%s)
	log "rebuild: base fixture generation took $((t1 - t0))s, $(du -sh "$src" | cut -f1)"

	local commit_out1 snap1
	t0=$(date +%s)
	commit_out1="$("$BIN" commit --repo="$repo" --ref=BASE "$src")"
	t1=$(date +%s)
	echo "$commit_out1"
	snap1="$(awk '/^snapshot /{print $2}' <<<"$commit_out1")"
	log "rebuild: commit BASE took $((t1 - t0))s"

	local tree1="$work/tree1" image1="$work/disc1.img" mnt1="$work/mnt1"
	t0=$(date +%s)
	# shellcheck disable=SC2046 # media_capacity_flags is a list of flags
	"$BIN" pack --repo="$repo" --ref=BASE $(media_capacity_flags "$FIXED_MEDIA") --out="$tree1"
	t1=$(date +%s)
	pack_rate_line "rebuild: pack disc 1" "$REBUILD_BASE_BYTES" "$t0" "$t1"
	local size1
	size1="$(du -sb "$tree1" | cut -f1)"
	log "rebuild: disc 1 packed tree size: $size1 bytes"

	sudo "$BIN" image build --out="$image1" "--capacity=$(media_image_capacity "$FIXED_MEDIA")" "$tree1"
	mount_populate "$image1" "$tree1" "$mnt1"
	"$BIN" verify "$mnt1"

	# Losing the whole repository directory: config, staging objects and
	# the state log all go together, the same way the incremental
	# scenario proves discs are the only source for restore.
	rm -rf "$repo"
	log "rebuild: deleted the whole repository directory"

	# log, ls --recursive and restore must all work with no repository:
	# they read the disc's own catalog, never --repo.
	local log_out
	log_out="$("$BIN" log --disc="$mnt1")"
	echo "$log_out"
	if ! echo "$log_out" | grep -qF "$snap1"; then
		fail "rebuild: log with no repository did not list snapshot $snap1"
	fi

	local ls_out
	ls_out="$("$BIN" ls --disc="$mnt1" --recursive "$snap1")"
	if [ -z "$ls_out" ]; then
		fail "rebuild: ls --recursive with no repository printed nothing"
	fi
	log "rebuild: ls --recursive with no repository listed $(echo "$ls_out" | wc -l) entries"

	local restored_whole="$work/restored-whole"
	"$BIN" restore --disc="$mnt1" "$snap1" "$restored_whole"
	assert_dirs_equal "$restored_whole$src" "$src"
	log "rebuild: whole-snapshot restore with no repository matches"

	# One single file and one whole directory via --include, still with
	# no repository.
	local one_file one_dir
	one_file="$(find "$src" -maxdepth 1 -type f -name 'large-*' -print -quit)"
	one_dir="$(find "$src" -mindepth 1 -maxdepth 1 -type d -print -quit)"
	[ -n "$one_file" ] || fail "rebuild: no top-level large-* file found in the fixture"
	[ -n "$one_dir" ] || fail "rebuild: no top-level directory found in the fixture"

	local restored_file="$work/restored-file"
	"$BIN" restore --disc="$mnt1" "--include=${one_file#/}" "$snap1" "$restored_file"
	diff -q "$restored_file$one_file" "$one_file" || fail "rebuild: --include restore of $one_file does not match"

	local restored_dir="$work/restored-dir"
	"$BIN" restore --disc="$mnt1" "--include=${one_dir#/}" "$snap1" "$restored_dir"
	assert_dirs_equal "$restored_dir$one_dir" "$one_dir"
	log "rebuild: --include restore of one file and one directory matches"

	# recover with disc 1 alone: exit 0, and the state
	# log's on-disc count must equal disc 1's own INDEX object count.
	"$BIN" recover --repo="$repo" --disc="$mnt1"
	local index_count1 ondisc_count1
	index_count1="$(run_tool ci-index-count "$mnt1")"
	ondisc_count1="$(run_tool ci-state-count "$repo/staging")"
	if [ "$ondisc_count1" != "$index_count1" ]; then
		fail "rebuild: on-disc count $ondisc_count1 does not equal disc 1 INDEX object count $index_count1"
	fi
	log "rebuild: recover from disc 1 recorded $ondisc_count1 on-disc objects, matching INDEX"

	# Change the source: about REBUILD_ADD_BYTES of new files, a rewritten
	# file and a few appended files (ci-incremental-fixture's mutate),
	# commit as NEXT and pack the change alone to a second dvd+r disc.
	t0=$(date +%s)
	run_tool ci-incremental-fixture mutate "$src" "$REBUILD_ADD_BYTES" "$REBUILD_SEED" "$plan" "$hashes_base" "$hashes_next"
	t1=$(date +%s)
	log "rebuild: mutate took $((t1 - t0))s, $(du -sh "$src" | cut -f1)"

	local commit_out2 snap2
	commit_out2="$("$BIN" commit --repo="$repo" --ref=NEXT "$src")"
	echo "$commit_out2"
	snap2="$(awk '/^snapshot /{print $2}' <<<"$commit_out2")"

	local tree2="$work/tree2" image2="$work/disc2.img" mnt2="$work/mnt2"
	t0=$(date +%s)
	# shellcheck disable=SC2046 # media_capacity_flags is a list of flags
	"$BIN" pack --repo="$repo" --ref=NEXT $(media_capacity_flags "$FIXED_MEDIA") --out="$tree2"
	t1=$(date +%s)
	pack_rate_line "rebuild: pack disc 2" "$REBUILD_ADD_BYTES" "$t0" "$t1"
	local size2
	size2="$(du -sb "$tree2" | cut -f1)"
	log "rebuild: disc 2 packed tree size: $size2 bytes (disc 1 was $size1 bytes)"

	local size_limit=$((size1 * 25 / 100))
	if [ "$size2" -ge "$size_limit" ]; then
		fail "rebuild: disc 2 size $size2 is not under 25% of disc 1's $size1; recover did not prevent re-packing disc 1's content"
	fi

	sudo "$BIN" image build --out="$image2" "--capacity=$(media_image_capacity "$FIXED_MEDIA")" "$tree2"
	mount_populate "$image2" "$tree2" "$mnt2"
	local verify_out2
	verify_out2="$("$BIN" verify "$mnt2")"
	echo "$verify_out2"
	if ! echo "$verify_out2" | grep -qE 'discs: 2$'; then
		fail "rebuild: disc 2's DISCS table does not record 2 discs (expected disc 1 as a prerequisite)"
	fi

	# disc 2 needs disc 1: a restore with only disc 2 must fail, naming
	# disc 1's uuid, proving disc 2's DISCS/Prereqs really do name it.
	chain_assert_missing_disc "$snap2" "$work/restored-next-missing-disc1" "$mnt1" "$mnt2"

	local restored_next="$work/restored-next"
	"$BIN" restore --disc="$mnt1" --disc="$mnt2" "$snap2" "$restored_next"
	run_tool ci-incremental-fixture check "$restored_next$src" "$hashes_next"
	log "rebuild: NEXT restored from both discs matches"

	# recover again, with both discs: exit 0, same on-disc count as
	# after the first rebuild plus disc 2's own new objects.
	"$BIN" recover --repo="$repo" --disc="$mnt1" --disc="$mnt2"
	local index_count2 ondisc_count2 want_count2
	index_count2="$(run_tool ci-index-count "$mnt2")"
	ondisc_count2="$(run_tool ci-state-count "$repo/staging")"
	want_count2=$((index_count1 + index_count2))
	if [ "$ondisc_count2" != "$want_count2" ]; then
		fail "rebuild: on-disc count after 2-disc rebuild is $ondisc_count2, want $want_count2 (disc 1 + disc 2 INDEX object counts)"
	fi

	# A repeat rebuild from the same two discs must be idempotent.
	"$BIN" recover --repo="$repo" --disc="$mnt1" --disc="$mnt2"
	local ondisc_count3
	ondisc_count3="$(run_tool ci-state-count "$repo/staging")"
	if [ "$ondisc_count3" != "$ondisc_count2" ]; then
		fail "rebuild: on-disc count changed on a repeat 2-disc rebuild: $ondisc_count2 then $ondisc_count3"
	fi
	log "rebuild: 2-disc rebuild is idempotent at $ondisc_count2 on-disc objects"

	# recover with only disc 2: exit 1, naming disc 1's uuid.
	rm -rf "$repo"
	local uuid1
	uuid1="$(run_tool ci-disc-uuid "$mnt1")"
	local rebuild_out rebuild_code
	set +e
	rebuild_out="$("$BIN" recover --repo="$repo" --disc="$mnt2" 2>&1)"
	rebuild_code=$?
	set -e
	echo "$rebuild_out"
	if [ "$rebuild_code" -ne 1 ]; then
		fail "rebuild: recover with only disc 2 exited $rebuild_code, want 1"
	fi
	if ! echo "$rebuild_out" | grep -qF "$uuid1"; then
		fail "rebuild: recover-with-disc-2-only did not name the missing disc 1 ($uuid1)"
	fi
	log "rebuild: recover with only disc 2 refused as expected, naming disc $uuid1"

	umount_if_mounted "$mnt1"
	umount_if_mounted "$mnt2"
	log "rebuild PASS"
}
