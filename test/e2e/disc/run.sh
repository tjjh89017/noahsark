#!/usr/bin/env bash
# Disc e2e scenarios: real mkudffs images, a real loop mount, and, for
# the cli and capacity scenarios, the real noahsark binary. Needs root
# (loop mount) and udftools (mkudffs). See lib.sh for the shared setup
# and assert.sh for the shared assertions.
#
# Usage: run.sh SCENARIO MEDIA
#   SCENARIO  verify-restore | corrupt-heal | corrupt-parity | cli | capacity
#   MEDIA     dvd+r | bd25 | bd25-forced-10g
set -euo pipefail

HERE="$(CDPATH='' cd "$(dirname "$0")" && pwd)"
# shellcheck source=test/e2e/disc/lib.sh
. "$HERE/lib.sh"

CI_FIXTURE="$ROOT/test/e2e/disc/cmd/ci-fixture"
CI_LIST="$ROOT/test/e2e/disc/cmd/ci-list"
CI_CORRUPT="$ROOT/test/e2e/disc/cmd/ci-corrupt"
CI_HEAL="$ROOT/test/e2e/disc/cmd/ci-heal"
CI_RESTORE="$ROOT/test/e2e/disc/cmd/ci-restore"

# build_fixture MEDIA WORK builds a small fixture disc at MEDIA's real
# sector counts and prints "TREE_DIR IMAGE_PATH SRC_DIR".
build_fixture() {
	local media="$1" work="$2" target physical
	read -r target physical <<<"$(media_sectors "$media")"
	go run "$CI_FIXTURE" "$work" "$target" "$physical"
}

scenario_verify_restore() {
	local media="$1" work="$WORK/vr"
	local out tree image src mnt
	out="$(build_fixture "$media" "$work")"
	tree="$(sed -n '1p' <<<"$out")"
	image="$(sed -n '2p' <<<"$out")"
	src="$(sed -n '3p' <<<"$out")"
	mnt="$work/mnt"

	mount_populate "$image" "$tree" "$mnt"
	assert_listing_matches "$mnt" "$work"
	go run "$CI_RESTORE" "$mnt" "$work/restore"
	assert_dirs_equal "$work/restore$src" "$src"
	umount_if_mounted "$mnt"
	log "verify-restore/$media PASS"
}

scenario_corrupt_heal() {
	local media="$1" work="$WORK/ch"
	local out tree image src mnt
	out="$(build_fixture "$media" "$work")"
	tree="$(sed -n '1p' <<<"$out")"
	image="$(sed -n '2p' <<<"$out")"
	src="$(sed -n '3p' <<<"$out")"
	mnt="$work/mnt"

	mount_populate "$image" "$tree" "$mnt"
	go run "$CI_RESTORE" "$mnt" "$work/restore-before"
	assert_dirs_equal "$work/restore-before$src" "$src"

	# Column 0 and 1 are INDEX.bin itself and are never corrupted here:
	# Heal needs a readable INDEX.bin to find anything else to repair.
	go run "$CI_CORRUPT" "$mnt" 3:0 6:0
	go run "$CI_HEAL" "$mnt"

	go run "$CI_RESTORE" "$mnt" "$work/restore-after"
	assert_dirs_equal "$work/restore-after$src" "$src"
	umount_if_mounted "$mnt"
	log "corrupt-heal/$media PASS"
}

scenario_corrupt_parity() {
	local media="$1" work="$WORK/cp"
	local out tree image src mnt
	out="$(build_fixture "$media" "$work")"
	tree="$(sed -n '1p' <<<"$out")"
	image="$(sed -n '2p' <<<"$out")"
	src="$(sed -n '3p' <<<"$out")"
	mnt="$work/mnt"

	mount_populate "$image" "$tree" "$mnt"
	go run "$CI_RESTORE" "$mnt" "$work/restore-before"
	assert_dirs_equal "$work/restore-before$src" "$src"

	# Corrupt two parity columns of stripe 0; Heal must rebuild them from
	# the data columns and the remaining parity.
	go run "$CI_CORRUPT" "$mnt" p:0:0 p:1:0
	go run "$CI_HEAL" "$mnt"

	go run "$CI_RESTORE" "$mnt" "$work/restore-after"
	assert_dirs_equal "$work/restore-after$src" "$src"
	umount_if_mounted "$mnt"
	log "corrupt-parity/$media PASS"
}

scenario_cli() {
	local media="$1" work="$WORK/cli"
	local capflags imgflag repo src tree image mnt commit_out snap restored
	capflags="$(media_capacity_flags "$media")"
	imgflag="--capacity=$(media_image_capacity "$media")"
	repo="$work/repo"
	src="$work/src"
	tree="$work/tree"
	image="$work/run.img"
	mnt="$work/mnt"
	restored="$work/restored"

	build_binary
	gen_small_tree "$src"

	"$BIN" init --repo="$repo" "$(media_init_capacity "$media")"
	commit_out="$("$BIN" commit --repo="$repo" "$src")"
	echo "$commit_out"
	snap="$(awk '/^snapshot /{print $2}' <<<"$commit_out")"

	# shellcheck disable=SC2086
	"$BIN" pack --repo="$repo" $capflags --out="$tree"
	"$BIN" image build --out="$image" "$imgflag" "$tree"

	mount_populate "$image" "$tree" "$mnt"
	"$BIN" verify --image="$mnt"
	"$BIN" restore "$mnt" "$snap" "$restored"
	assert_dirs_equal "$restored$src" "$src"
	umount_if_mounted "$mnt"
	log "cli/$media PASS"
}

# media_image_capacity MEDIA prints the real physical sector preset an
# image build should use: the preset name itself for an unforced media,
# or the physical preset for a media whose logical capacity is forced.
media_image_capacity() {
	case "$1" in
	dvd+r) echo "dvd+r" ;;
	bd25 | bd25-forced-10g) echo "bd25" ;;
	*) fail "unknown media preset: $1" ;;
	esac
}

scenario_capacity() {
	local media="$1" work="$WORK/cap"
	local repo small_src over_src tree image mnt restored
	local capflag physflag small_mb over_mb apparent
	repo="$work/repo"
	small_src="$work/small"
	over_src="$work/over"
	tree="$work/tree"
	image="$work/run.img"
	mnt="$work/mnt"
	restored="$work/restored"

	build_binary
	small_mb="$(media_small_mb "$media")"
	over_mb="$(media_over_mb "$media")"
	apparent="$(media_apparent_bytes "$media")"

	gen_fixture "$small_src/data.bin" "$((small_mb * 1024 * 1024))"
	gen_fixture "$over_src/data.bin" "$((over_mb * 1024 * 1024))"

	case "$media" in
	bd25-forced-10g)
		capflag="--capacity=10GiB"
		physflag="--physical-capacity=bd25"
		;;
	*)
		capflag="--capacity=$media"
		physflag=""
		;;
	esac

	"$BIN" init --repo="$repo" "$(media_init_capacity "$media")"

	local commit_out snap
	commit_out="$("$BIN" commit --repo="$repo" --ref=SMALL "$small_src")"
	echo "$commit_out"
	snap="$(awk '/^snapshot /{print $2}' <<<"$commit_out")"
	"$BIN" commit --repo="$repo" --ref=OVER "$over_src"

	"$BIN" pack --repo="$repo" --ref=SMALL "$capflag" $physflag --out="$tree"
	assert_refused "capacity/$media: pack over-size" \
		"$BIN" pack --repo="$repo" --ref=OVER "$capflag" $physflag --out="$work/over-tree"

	"$BIN" image build --out="$image" --capacity="$(media_image_capacity "$media")" "$tree"
	assert_sparse "$image" "$apparent"

	mount_populate "$image" "$tree" "$mnt"
	local verify_out
	verify_out="$("$BIN" verify --image="$mnt")"
	echo "$verify_out"
	if [ "$media" = "bd25-forced-10g" ]; then
		echo "$verify_out" | grep -qE 'forced 5242880 sectors, capacity_is_forced=1' \
			|| fail "capacity/$media: verify did not report the forced capacity fields"
	fi
	"$BIN" restore "$mnt" "$snap" "$restored"
	assert_dirs_equal "$restored$small_src" "$small_src"
	umount_if_mounted "$mnt"
	log "capacity/$media PASS"
}

main() {
	local scenario="${1:?usage: run.sh SCENARIO MEDIA}"
	local media="${2:?usage: run.sh SCENARIO MEDIA}"
	case "$scenario" in
	verify-restore) scenario_verify_restore "$media" ;;
	corrupt-heal) scenario_corrupt_heal "$media" ;;
	corrupt-parity) scenario_corrupt_parity "$media" ;;
	cli) scenario_cli "$media" ;;
	capacity) scenario_capacity "$media" ;;
	*) fail "unknown scenario: $scenario" ;;
	esac
}

main "$@"
