#!/usr/bin/env bash
# Disc e2e scenarios: real mkudffs images, a real loop mount, and, for
# the cli and media scenarios, the real noahsark binary. Needs root
# (loop mount) and udftools (mkudffs). See lib.sh for the shared setup
# and assert.sh for the shared assertions.
#
# Usage: run.sh SCENARIO [MEDIA]
#   SCENARIO  media | corrupt-heal | corrupt-parity | corrupt-max |
#             corrupt-over | cli
#   MEDIA     dvd+r | bd25 | bd25-forced-10g; required for media, unused
#             (and ignored) by every other scenario, which fixes its own
#             fixture at dvd+r's real sector counts
set -euo pipefail

HERE="$(CDPATH='' cd "$(dirname "$0")" && pwd)"
# shellcheck source=test/e2e/disc/lib.sh
. "$HERE/lib.sh"

# FIXED_MEDIA is the media preset every scenario but media builds its
# fixture at: real, but small enough that fixture size never depends on
# it, so a fixed choice keeps those scenarios simple.
FIXED_MEDIA="dvd+r"

# build_fixture MEDIA WORK [CONTENT_BYTES] builds a fixture disc at
# MEDIA's real sector counts, with CONTENT_BYTES of extra deterministic
# content when given, and prints "TREE_DIR IMAGE_PATH SRC_DIR".
build_fixture() {
	local media="$1" work="$2" content="${3:-}" target physical
	read -r target physical <<<"$(media_sectors "$media")"
	if [ -n "$content" ]; then
		run_tool ci-fixture "$work" "$target" "$physical" "$content"
	else
		run_tool ci-fixture "$work" "$target" "$physical"
	fi
}

# MULTI_STRIPE_BYTES is enough real content for at least two FEC stripes
# (one stripe holds 231*2048 bytes): scenarios that corrupt one stripe
# and check a different one need real data in both.
MULTI_STRIPE_BYTES=1200000

scenario_corrupt_heal() {
	local work="$WORK/ch"
	local out tree image src mnt
	out="$(build_fixture "$FIXED_MEDIA" "$work")"
	tree="$(sed -n '1p' <<<"$out")"
	image="$(sed -n '2p' <<<"$out")"
	src="$(sed -n '3p' <<<"$out")"
	mnt="$work/mnt"

	mount_populate "$image" "$tree" "$mnt"
	run_tool ci-restore "$mnt" "$work/restore-before"
	assert_dirs_equal "$work/restore-before$src" "$src"

	# Column 0 and 1 are INDEX.bin itself and are never corrupted here:
	# Heal needs a readable INDEX.bin to find anything else to repair.
	run_tool ci-corrupt "$mnt" 3:0 6:0
	run_tool ci-heal "$mnt"

	run_tool ci-restore "$mnt" "$work/restore-after"
	assert_dirs_equal "$work/restore-after$src" "$src"
	umount_if_mounted "$mnt"
	log "corrupt-heal PASS"
}

scenario_corrupt_parity() {
	local work="$WORK/cp"
	local out tree image src mnt
	out="$(build_fixture "$FIXED_MEDIA" "$work")"
	tree="$(sed -n '1p' <<<"$out")"
	image="$(sed -n '2p' <<<"$out")"
	src="$(sed -n '3p' <<<"$out")"
	mnt="$work/mnt"

	mount_populate "$image" "$tree" "$mnt"
	run_tool ci-restore "$mnt" "$work/restore-before"
	assert_dirs_equal "$work/restore-before$src" "$src"

	# Corrupt two parity columns of stripe 0; Heal must rebuild them from
	# the data columns and the remaining parity.
	run_tool ci-corrupt "$mnt" p:0:0 p:1:0
	run_tool ci-heal "$mnt"

	run_tool ci-restore "$mnt" "$work/restore-after"
	assert_dirs_equal "$work/restore-after$src" "$src"
	umount_if_mounted "$mnt"
	log "corrupt-parity PASS"
}

# scenario_corrupt_max corrupts exactly m=23 blocks of stripe 0: one data
# column (2, never 0 or 1, INDEX's own columns) plus 22 parity columns
# (1..22, never column 0: Heal needs it, uncorrupted, to fill the single
# data column's gap). m corrupted blocks in one stripe is Heal's stated
# limit; it must still recover every one of them.
scenario_corrupt_max() {
	local work="$WORK/cmax"
	local out tree image src mnt
	out="$(build_fixture "$FIXED_MEDIA" "$work" "$MULTI_STRIPE_BYTES")"
	tree="$(sed -n '1p' <<<"$out")"
	image="$(sed -n '2p' <<<"$out")"
	src="$(sed -n '3p' <<<"$out")"
	mnt="$work/mnt"

	mount_populate "$image" "$tree" "$mnt"
	run_tool ci-restore "$mnt" "$work/restore-before"
	assert_dirs_equal "$work/restore-before$src" "$src"

	local args=("2:0")
	for j in $(seq 1 22); do
		args+=("p:$j:0")
	done
	run_tool ci-corrupt "$mnt" "${args[@]}"
	run_tool ci-heal "$mnt"

	run_tool ci-restore "$mnt" "$work/restore-after"
	assert_dirs_equal "$work/restore-after$src" "$src"
	umount_if_mounted "$mnt"
	log "corrupt-max PASS"
}

# scenario_corrupt_over corrupts m+1=24 data blocks of stripe 0 (columns
# 2..25, never 0 or 1), one more than Heal's parity can recover. Heal
# must fail for that stripe, with a message naming the stripe, and exit
# nonzero, and it must leave every other stripe untouched: this fixture
# is large enough that stripe 1 also carries real data.
scenario_corrupt_over() {
	local work="$WORK/cover"
	local out tree image src mnt
	out="$(build_fixture "$FIXED_MEDIA" "$work" "$MULTI_STRIPE_BYTES")"
	tree="$(sed -n '1p' <<<"$out")"
	image="$(sed -n '2p' <<<"$out")"
	src="$(sed -n '3p' <<<"$out")"
	mnt="$work/mnt"

	mount_populate "$image" "$tree" "$mnt"

	local before after
	before="$(run_tool ci-corrupt "$mnt" peek:2:1)"

	local args=()
	for c in $(seq 2 25); do
		args+=("$c:0")
	done
	run_tool ci-corrupt "$mnt" "${args[@]}"

	local heal_out code
	set +e
	heal_out="$(run_tool ci-heal "$mnt" 2>&1)"
	code=$?
	set -e
	echo "$heal_out"
	if [ "$code" -eq 0 ]; then
		fail "corrupt-over: expected ci-heal to fail, it exited 0"
	fi
	if ! echo "$heal_out" | grep -qi "stripe 0"; then
		fail "corrupt-over: expected the failure to name stripe 0"
	fi

	after="$(run_tool ci-corrupt "$mnt" peek:2:1)"
	if [ "$before" != "$after" ]; then
		fail "corrupt-over: stripe 1 changed after the failed heal of stripe 0: before [$before] after [$after]"
	fi
	umount_if_mounted "$mnt"
	log "corrupt-over PASS"
}

scenario_cli() {
	local work="$WORK/cli"
	local repo src tree image mnt commit_out snap restored
	repo="$work/repo"
	src="$work/src"
	tree="$work/tree"
	image="$work/run.img"
	mnt="$work/mnt"
	restored="$work/restored"

	build_binary
	gen_small_tree "$src"

	"$BIN" init --repo="$repo" "$(media_init_capacity "$FIXED_MEDIA")"
	commit_out="$("$BIN" commit --repo="$repo" "$src")"
	echo "$commit_out"
	snap="$(awk '/^snapshot /{print $2}' <<<"$commit_out")"

	# shellcheck disable=SC2046 # media_capacity_flags is a list of flags
	"$BIN" pack --repo="$repo" $(media_capacity_flags "$FIXED_MEDIA") --out="$tree"
	"$BIN" image build --out="$image" "--capacity=$(media_image_capacity "$FIXED_MEDIA")" "$tree"

	mount_populate "$image" "$tree" "$mnt"
	assert_listing_matches "$mnt" "$work"
	"$BIN" verify --image="$mnt"
	"$BIN" restore "$mnt" "$snap" "$restored"
	assert_dirs_equal "$restored$src" "$src"
	umount_if_mounted "$mnt"
	log "cli PASS"
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

# scenario_media packs, images, mounts, verifies and restores a sample at
# MEDIA's real capacity; checks that an over-size pack is refused at that
# capacity; and, for a forced media, checks DISC's forced capacity
# fields through verify's output.
scenario_media() {
	local media="$1" work="$WORK/media"
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
	assert_refused "media/$media: pack over-size" \
		"$BIN" pack --repo="$repo" --ref=OVER "$capflag" $physflag --out="$work/over-tree"

	"$BIN" image build --out="$image" --capacity="$(media_image_capacity "$media")" "$tree"
	assert_sparse "$image" "$apparent"

	mount_populate "$image" "$tree" "$mnt"
	local verify_out
	verify_out="$("$BIN" verify --image="$mnt")"
	echo "$verify_out"
	if [ "$media" = "bd25-forced-10g" ]; then
		echo "$verify_out" | grep -qE 'forced 5242880 sectors, capacity_is_forced=1' \
			|| fail "media/$media: verify did not report the forced capacity fields"
	fi
	"$BIN" restore "$mnt" "$snap" "$restored"
	assert_dirs_equal "$restored$small_src" "$small_src"
	umount_if_mounted "$mnt"
	log "media/$media PASS"
}

main() {
	local scenario="${1:?usage: run.sh SCENARIO [MEDIA]}"
	local media="${2:-}"
	case "$scenario" in
	media)
		[ -n "$media" ] || fail "the media scenario needs a MEDIA argument"
		scenario_media "$media"
		;;
	corrupt-heal) scenario_corrupt_heal ;;
	corrupt-parity) scenario_corrupt_parity ;;
	corrupt-max) scenario_corrupt_max ;;
	corrupt-over) scenario_corrupt_over ;;
	cli) scenario_cli ;;
	*) fail "unknown scenario: $scenario" ;;
	esac
}

main "$@"
