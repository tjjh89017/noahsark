#!/usr/bin/env bash
# The lifecycle e2e scenario: sourced by run.sh. It walks discs through
# the disc states with the real commands and a read-only loop mount:
# pack, image build, verify, disc verified, verify --undo, gc, disc
# lost, disc lost --undo and pack --undo. After each step it checks the
# state that status shows. It also checks the confirmations with no
# terminal, with --yes and with --force-yes, and the refusals that need
# no extra disc. See run.sh for the shared scenario dispatch and lib.sh
# for the pack, image, mount, verify and restore helpers.
set -euo pipefail

scenario_lifecycle() {
	local work="$WORK/lifecycle"
	local repo="$work/repo" src="$work/src" image="$work/disc0.img" mnt="$work/mnt"
	local out snap uuid0 uuid1 uuid2 uuid3 items0
	build_binary
	gen_small_tree "$src"

	(mkdir -p "$repo" && cd "$repo" && "$BIN" init)
	out="$("$BIN" --repo="$repo" commit --ref=LIFE "$src")"
	echo "$out"
	snap="$(awk '/^snapshot /{print $2}' <<<"$out")"

	local n=("$BIN" --repo="$repo")
	local yes=("$BIN" --repo="$repo" --yes)
	local force=("$BIN" --repo="$repo" --force-yes)

	# Disc 0: packed.
	pack_disc "$repo" --capacity=dvd+r
	uuid0="$PACKED_UUID"
	[ "$PACKED_SEQ" = 0 ] || fail "lifecycle: the first disc is disc $PACKED_SEQ, want disc 0"
	assert_disc_state "$repo" "$uuid0" "packed"

	# A packed disc refuses these commands, and none of them asks.
	expect_exit 1 "disc 0 has no burn record" "${force[@]}" disc verified 0
	expect_exit 1 "disc 0 has no verified record" "${yes[@]}" verify --undo 0
	expect_exit 1 "disc 0 is not marked lost" "${yes[@]}" disc lost --undo 0
	expect_exit 1 "disc 0 has no burn record" "${yes[@]}" disc burned --undo 0
	expect_exit 0 "gc: disc 0: not verified;" "${n[@]}" gc
	expect_exit 2 "no disc matches 7" "${n[@]}" image build 7
	assert_disc_state "$repo" "$uuid0" "packed"

	# The packed tree is not a disc: verify checks it and records nothing.
	expect_exit 0 "not counted: this is not a disc" "${n[@]}" verify "$repo/staging/plans/$uuid0/tree"
	assert_disc_state "$repo" "$uuid0" "packed"

	# image build writes the image once. A second build needs --force.
	expect_exit 0 "built image " sudo "$BIN" --repo="$repo" image build 0
	expect_exit 1 "exists; add --force to build it again" sudo "$BIN" --repo="$repo" image build 0
	mv "$repo/staging/plans/$uuid0/tree.img" "$image"
	DISC_IMAGE[$uuid0]="$image"
	assert_disc_state "$repo" "$uuid0" "packed"

	# A read-write mount is not a counted mount.
	mount_rw "$image" "$mnt"
	expect_exit 0 "not counted: this is not a disc" "${n[@]}" verify "$mnt"
	umount_if_mounted "$mnt"
	assert_disc_state "$repo" "$uuid0" "packed"

	# verify --no-mark of a read-only mount records nothing.
	mount_ro "$image" "$mnt"
	expect_exit 0 "not marked; to record this burn, run: noahsark disc burned 0" "${n[@]}" verify --no-mark "$mnt"
	assert_disc_state "$repo" "$uuid0" "packed"

	# A counted verify of a packed disc records the burn and the check.
	items0="$(run_tool ci-index-count "$mnt")"
	verify_counted "$repo" "$mnt" "$uuid0" "$items0"
	grep -qx 'burn recorded; verified' <<<"$VERIFY_OUT" ||
		fail "lifecycle: the first verify did not record the burn"

	# gc keeps the 7-day wait.
	expect_exit 0 "gc: disc 0: too soon; " "${n[@]}" gc
	expect_exit 1 "disc 0 is already verified" "${n[@]}" disc burned 0
	assert_disc_state "$repo" "$uuid0" "verified, last check *"

	# verify --undo asks an ordinary confirmation.
	expect_exit 1 "nothing changed" "${n[@]}" verify --undo 0
	assert_disc_state "$repo" "$uuid0" "verified, last check *"
	expect_exit 0 "verified record removed; burn record kept" "${yes[@]}" verify --undo 0
	assert_disc_state "$repo" "$uuid0" "burned"

	# disc verified asks a critical confirmation: --yes is not enough.
	expect_exit 1 "nothing changed; disc verified needs --force-yes" "${yes[@]}" disc verified 0
	assert_disc_state "$repo" "$uuid0" "burned"
	expect_exit 0 "verified record added; not checked" "${force[@]}" disc verified 0
	assert_disc_state "$repo" "$uuid0" "verified, not checked"

	# disc verified then gc keeps the 7-day wait too.
	expect_exit 0 "gc: disc 0: too soon; " "${n[@]}" gc
	assert_disc_state "$repo" "$uuid0" "verified, not checked"

	# A good verify replaces "not checked" with the date of the check.
	verify_counted "$repo" "$mnt" "$uuid0" "$items0"
	grep -qx 'already verified; check logged' <<<"$VERIFY_OUT" ||
		fail "lifecycle: the verify of a verified disc did not log the check"

	# gc with a shorter wait frees the staged copy.
	expect_exit 0 "gc: freed " "${n[@]}" gc --force-after=0d
	grep -qE '^gc: freed [1-9][0-9]* item\(s\), ' <<<"$EXPECT_OUT" ||
		fail "lifecycle: gc --force-after=0d freed nothing"
	assert_disc_state "$repo" "$uuid0" "on disc only, last check *"
	[ ! -e "$repo/staging/plans/$uuid0" ] ||
		fail "lifecycle: gc left the plan directory of disc 0"

	# A good verify of an on disc only disc only logs the check.
	expect_exit 0 "items, ok" "${n[@]}" verify "$mnt"
	grep -qx 'check logged' <<<"$EXPECT_OUT" ||
		fail "lifecycle: the verify of an on disc only disc did not log the check"
	assert_disc_state "$repo" "$uuid0" "on disc only, last check *"

	# An on disc only disc refuses these commands.
	expect_exit 1 "disc 0 is on disc only; gc already freed the staged copy" "${yes[@]}" verify --undo 0
	expect_exit 1 "disc 0 is no longer packed; pack cannot be undone" "${yes[@]}" pack --undo 0
	expect_exit 1 "disc 0 is already verified" "${n[@]}" disc burned 0

	# The disc alone holds the data now: restore reads it.
	restore_loop "$repo" "$mnt" "$snap" "$work/restored"
	[ "$RESTORE_SWAPS" -eq 0 ] || fail "lifecycle: restore asked for another disc"
	assert_dirs_equal "$work/restored" "$src"
	assert_empty_dir_restored "$work/restored" ""

	# disc lost asks a critical confirmation.
	expect_exit 1 "nothing changed; disc lost needs --force-yes" "${yes[@]}" disc lost 0
	assert_disc_state "$repo" "$uuid0" "on disc only, last check *"
	expect_exit 0 "marked lost; " "${force[@]}" disc lost 0
	grep -qE 'marked lost; [1-9][0-9]* item\(s\) need a new commit$' <<<"$EXPECT_OUT" ||
		fail "lifecycle: disc lost of an on disc only disc did not name its items"
	assert_disc_state "$repo" "$uuid0" "lost"
	expect_exit 1 "disc 0 is marked lost" "${n[@]}" verify "$mnt"
	expect_exit 1 "disc 0 is already marked lost" "${force[@]}" disc lost 0

	# disc lost --undo asks an ordinary confirmation.
	expect_exit 1 "nothing changed" "${n[@]}" disc lost --undo 0
	assert_disc_state "$repo" "$uuid0" "lost"
	expect_exit 0 "lost mark removed; " "${yes[@]}" disc lost --undo 0
	assert_disc_state "$repo" "$uuid0" "on disc only*"
	expect_exit 0 "items, ok" "${n[@]}" verify "$mnt"
	grep -qx 'check logged' <<<"$EXPECT_OUT" ||
		fail "lifecycle: the verify of the found disc did not log the check"
	assert_disc_state "$repo" "$uuid0" "on disc only, last check *"
	umount_if_mounted "$mnt"

	# Disc 1: pack --undo asks an ordinary confirmation.
	echo "a second file, for disc 1" >"$src/second.txt"
	expect_exit 0 "snapshot " "${n[@]}" commit --ref=LIFE2 "$src"
	pack_disc "$repo" --capacity=dvd+r
	uuid1="$PACKED_UUID"
	[ "$PACKED_SEQ" = 1 ] || fail "lifecycle: the second disc is disc $PACKED_SEQ, want disc 1"
	assert_disc_state "$repo" "$uuid1" "packed"
	expect_exit 1 "nothing changed" "${n[@]}" pack --undo 1
	assert_disc_state "$repo" "$uuid1" "packed"
	expect_exit 0 "pack undone, " "${yes[@]}" pack --undo 1
	[ -z "$(disc_state "$repo" "$uuid1")" ] ||
		fail "lifecycle: status still shows the undone disc 1"
	expect_exit 2 "no disc matches 1" "${yes[@]}" pack --undo 1
	out="$("$BIN" --repo="$repo" status)"
	echo "$out"
	grep -qE '^staged: [1-9][0-9]* items, ' <<<"$out" ||
		fail "lifecycle: pack --undo did not return the items to staged"

	# The number of an undone disc is not used again.
	pack_disc "$repo" --capacity=dvd+r
	uuid2="$PACKED_UUID"
	[ "$PACKED_SEQ" = 2 ] || fail "lifecycle: the disc after the undone disc 1 is disc $PACKED_SEQ, want disc 2"

	# pack --undo takes only the newest disc.
	echo "a third file, for disc 3" >"$src/third.txt"
	expect_exit 0 "snapshot " "${n[@]}" commit --ref=LIFE3 "$src"
	pack_disc "$repo" --capacity=dvd+r
	uuid3="$PACKED_UUID"
	expect_exit 1 "disc 2 is not the newest disc; pack cannot be undone" "${yes[@]}" pack --undo 2
	assert_disc_state "$repo" "$uuid2" "packed"

	# disc lost of a packed disc returns its items to staged. Its disc
	# root is gone, thus disc lost --undo refuses it.
	expect_exit 0 "item(s) returned to staged" "${force[@]}" disc lost 2
	assert_disc_state "$repo" "$uuid2" "lost"
	[ ! -e "$repo/staging/plans/$uuid2" ] ||
		fail "lifecycle: disc lost left the plan directory of disc 2"
	expect_exit 1 "disc 2 had no verified record when it was marked lost" "${yes[@]}" disc lost --undo 2
	assert_disc_state "$repo" "$uuid2" "lost"
	assert_disc_state "$repo" "$uuid3" "packed"
	assert_disc_state "$repo" "$uuid0" "on disc only, last check *"

	"$BIN" --repo="$repo" status
	log "lifecycle PASS"
}
