#!/usr/bin/env bash
# The cross-runner e2e scenarios: sourced by run.sh. cross-build packs a
# small set of discs, builds and verifies their images, and writes the
# images and the SHA-256 list of the source tree into the directory
# NOAHSARK_E2E_DISC_SET. cross-restore runs on another machine. It gets
# only that directory: no repository and no source tree. It recovers a
# repository from the images, restores the snapshot, and checks the
# result against the SHA-256 list. See run.sh for the shared scenario
# dispatch and lib.sh for the pack, image, mount, recover and restore
# helpers.
set -euo pipefail

# CROSS_CONTENT_BYTES sizes the source tree, and CROSS_CAPACITY each
# disc: two or three discs, and a disc set of some tens of MB.
CROSS_CONTENT_BYTES="${NOAHSARK_E2E_CROSS_CONTENT_BYTES:-20000000}"
CROSS_CAPACITY="--capacity=12000KiB"

# CROSS_REF names the snapshot. The refs come back with recover.
CROSS_REF="CROSS"

# cross_set_dir prints the disc set directory, and fails when it is not
# given.
cross_set_dir() {
	[ -n "${NOAHSARK_E2E_DISC_SET:-}" ] || fail "cross: NOAHSARK_E2E_DISC_SET is required"
	echo "$NOAHSARK_E2E_DISC_SET"
}

scenario_cross_build() {
	local set work="$WORK/cross-build"
	set="$(cross_set_dir)"
	local repo="$work/repo" src="$work/src" mnt="$work/mnt" out
	build_binary
	mkdir -p "$work" "$set"

	run_tool ci-incremental-fixture gen "$src" "$CROSS_CONTENT_BYTES" 20261002 "$work/hashes" "$work/plan"
	(cd "$src" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum) >"$set/source.sha256"

	(mkdir -p "$repo" && cd "$repo" && "$BIN" init)
	out="$("$BIN" --repo="$repo" commit --ref="$CROSS_REF" "$src")"
	echo "$out"
	local n=0
	while true; do
		pack_disc "$repo" "$CROSS_CAPACITY"
		image_build "$repo" "$PACKED_UUID" "$work/disc$n.img"
		mount_ro "$work/disc$n.img" "$mnt"
		verify_counted "$repo" "$mnt" "$PACKED_UUID"
		umount_if_mounted "$mnt"
		cp --sparse=always "$work/disc$n.img" "$set/disc$n.img"
		n=$((n + 1))
		[ "$n" -le 10 ] || fail "cross-build: more than 10 discs"
		out="$("$BIN" --repo="$repo" status)"
		if grep -q '^staged: 0 items, ' <<<"$out"; then
			break
		fi
	done
	[ "$n" -ge 2 ] || fail "cross-build: the set has $n disc, want 2 or more"
	ls -ls "$set"
	log "cross-build PASS: $n discs in $set"
}

scenario_cross_restore() {
	local set work="$WORK/cross-restore"
	set="$(cross_set_dir)"
	local repo="$work/repo" mnt="$work/mnt" rmnt="$work/rmnt" out uuid image
	build_binary
	mkdir -p "$work"
	[ -s "$set/source.sha256" ] || fail "cross-restore: $set holds no source.sha256"

	local images
	images="$(find "$set" -maxdepth 1 -name 'disc*.img' | LC_ALL=C sort)"
	[ -n "$images" ] || fail "cross-restore: $set holds no disc image"
	while IFS= read -r image; do
		mount_ro "$image" "$mnt"
		# With no repository, verify names the disc by its uuid.
		out="$("$BIN" verify "$mnt" 2>&1)"
		echo "$out"
		uuid="$(sed -n 's/^disc \([0-9a-f-]*\) ".*": [0-9]* items, ok$/\1/p' <<<"$out")"
		[ -n "$uuid" ] || fail "cross-restore: verify of $image gave no ok line"
		DISC_IMAGE[$uuid]="$image"
		# The source path is only stored in config.yaml; it does not exist here.
		recover_disc "$repo" "$work/src" "$mnt" 0
		umount_if_mounted "$mnt"
	done <<<"$images"

	local restored="$work/restored"
	restore_loop "$repo" "$rmnt" "$CROSS_REF" "$restored"
	umount_if_mounted "$rmnt"

	(cd "$restored" && sha256sum --quiet -c "$set/source.sha256") ||
		fail "cross-restore: the restored tree does not match the SHA-256 list"
	local want got
	want="$(cut -c 67- "$set/source.sha256")"
	got="$(cd "$restored" && find . -type f | LC_ALL=C sort)"
	[ "$got" = "$want" ] || fail "cross-restore: the restored tree has missing or extra files"
	[ -z "$(cd "$restored" && find . ! -type f ! -type d)" ] ||
		fail "cross-restore: the restored tree holds an entry that is not a file or a directory"
	log "cross-restore PASS: $(grep -c . <<<"$want") files match the SHA-256 list"
}
