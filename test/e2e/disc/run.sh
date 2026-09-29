#!/usr/bin/env bash
# Disc e2e scenarios: real mkudffs images, a real read-only loop mount,
# and the real noahsark binary. Needs root
# (loop mount) and udftools (mkudffs). See lib.sh for the shared setup
# and assert.sh for the shared assertions.
#
# Usage: run.sh SCENARIO [MEDIA] [ORDER] [EXTRAS]
#   SCENARIO  media | corrupt-heal | corrupt-parity | corrupt-max |
#             corrupt-over | fec | cli | iso | chain | chain-small |
#             lowmem | incremental | rebuild | lifecycle
#             fec runs corrupt-heal, corrupt-parity, corrupt-max and
#             corrupt-over in sequence, in one process: the CI matrix
#             folds the four corrupt-* cells into this one.
#   MEDIA     dvd+r | bd25 | bd25-forced-10g; required for media, unused
#             (and ignored) by every other scenario, which fixes its own
#             fixture at dvd+r's real sector counts. lowmem ignores it
#             too: it always runs the media/bd25 flow, under whatever
#             process memory limit the caller (the e2e action) applied.
#   ORDER     dvd-bd25-bd10 | bd25-bd10-dvd; required for chain and
#             chain-small, unused by every other scenario
#   EXTRAS    a comma-separated list of extra scenarios to run, in
#             order, after SCENARIO finishes, in the same process: the
#             CI matrix folds cli and iso into the media/dvd+r cell this
#             way. Only cli and iso are valid extras. Empty for every
#             other cell.
set -euo pipefail

HERE="$(CDPATH='' cd "$(dirname "$0")" && pwd)"
# shellcheck source=test/e2e/disc/lib.sh
. "$HERE/lib.sh"
# shellcheck source=test/e2e/disc/chain.sh
. "$HERE/chain.sh"
# shellcheck source=test/e2e/disc/iso.sh
. "$HERE/iso.sh"
# shellcheck source=test/e2e/disc/incremental.sh
. "$HERE/incremental.sh"
# shellcheck source=test/e2e/disc/rebuild.sh
. "$HERE/rebuild.sh"
# shellcheck source=test/e2e/disc/lifecycle.sh
. "$HERE/lifecycle.sh"

# FIXED_MEDIA is the media preset every scenario but media builds its
# fixture at: real, but small enough that fixture size never depends on
# it, so a fixed choice keeps those scenarios simple.
FIXED_MEDIA="dvd+r"

# build_fixture MEDIA WORK [CONTENT_BYTES] [FEC] builds a fixture disc at
# MEDIA's real sector counts, with CONTENT_BYTES of extra deterministic
# content when given, and prints "TREE_DIR IMAGE_PATH SRC_DIR". FEC, when
# non-empty, builds the run with Reed-Solomon FEC; a corrupt-and-heal
# scenario needs it, since there is nothing to heal without it.
build_fixture() {
	local media="$1" work="$2" content="${3:-}" fec="${4:-}" target
	target="$(media_sectors "$media")"
	local fecflag=()
	[ -n "$fec" ] && fecflag=(-fec)
	if [ -n "$content" ]; then
		run_tool ci-fixture "${fecflag[@]}" "$work" "$target" "$content"
	else
		run_tool ci-fixture "${fecflag[@]}" "$work" "$target"
	fi
}

# MULTI_STRIPE_BYTES is enough real content for at least two FEC stripes
# (one stripe holds 231*2048 bytes): scenarios that corrupt one stripe
# and check a different one need real data in both.
MULTI_STRIPE_BYTES=1200000

# pack_rate_line LABEL BYTES T0 T1 logs "LABEL took Xs, N MB/s", timing a
# pack call between the date +%s.%N timestamps T0 and T1 against BYTES of
# packed data.
pack_rate_line() {
	local label="$1" bytes="$2" t0="$3" t1="$4"
	awk -v label="$label" -v bytes="$bytes" -v t0="$t0" -v t1="$t1" \
		'BEGIN {
			d = t1 - t0
			if (d <= 0) d = 0.000001
			printf "[disc-e2e] %s took %.2fs, %.1f MB/s\n", label, d, bytes / 1000000 / d
		}'
}

# FIXTURE_REF is the ref name that ci-fixture gives its snapshot.
FIXTURE_REF="2026-09-13"

# fec_setup WORK EXTRA_BYTES builds a ci-fixture disc with FEC, mounts
# its image read-only at WORK/mnt, recovers it into the repository
# WORK/repo, and restores it into WORK/restore-before. ci-fixture writes
# no repository, thus recover makes one from the disc alone. It sets
# FEC_IMAGE, FEC_SRC, FEC_MNT and FEC_REPO.
FEC_IMAGE=""
FEC_SRC=""
FEC_MNT=""
FEC_REPO=""
fec_setup() {
	local work="$1" extra="$2" out
	build_binary
	out="$(build_fixture "$FIXED_MEDIA" "$work" "$extra" 1)"
	FEC_IMAGE="$(sed -n '2p' <<<"$out")"
	FEC_SRC="$(sed -n '3p' <<<"$out")"
	FEC_MNT="$work/mnt"
	FEC_REPO="$work/repo"

	mount_ro "$FEC_IMAGE" "$FEC_MNT"
	recover_disc "$FEC_REPO" "$FEC_SRC" "$FEC_MNT" 0
	restore_loop "$FEC_REPO" "$FEC_MNT" "$FIXTURE_REF" "$work/restore-before"
	assert_dirs_equal "$work/restore-before" "$FEC_SRC"
}

# fec_damage CORRUPT-ARG... mounts the fixture image read-write, writes
# the damage with ci-corrupt, and mounts the image read-only again.
fec_damage() {
	umount_if_mounted "$FEC_MNT"
	mount_rw "$FEC_IMAGE" "$FEC_MNT"
	run_tool ci-corrupt "$FEC_MNT" "$@"
	umount_if_mounted "$FEC_MNT"
	mount_ro "$FEC_IMAGE" "$FEC_MNT"
}

# fec_heal_restore WORK heals the damaged disc at FEC_MNT into
# WORK/healed with verify --heal, checks that restore of the healed disc
# root gives the source back, and unmounts the disc.
fec_heal_restore() {
	local work="$1" healed="$1/healed"
	expect_exit 0 "healed " "$BIN" --repo="$FEC_REPO" verify --heal --out="$healed" "$FEC_MNT"
	grep -qE '^disc [0-9]+ ".*": [0-9]+ items, ok$' <<<"$EXPECT_OUT" ||
		fail "verify --heal: the healed disc root does not check ok"
	restore_loop "$FEC_REPO" "$healed" "$FIXTURE_REF" "$work/restore-after"
	assert_dirs_equal "$work/restore-after" "$FEC_SRC"
	umount_if_mounted "$FEC_MNT"
}

scenario_corrupt_heal() {
	local work="$WORK/ch"
	fec_setup "$work" ""

	# Column 0 and 1 are INDEX.bin itself and are never corrupted here:
	# Heal needs a readable INDEX.bin to find anything else to repair.
	fec_damage 3:0 6:0
	fec_heal_restore "$work"
	log "corrupt-heal PASS"
}

scenario_corrupt_parity() {
	local work="$WORK/cp"
	fec_setup "$work" ""

	# Corrupt two parity columns of stripe 0; Heal must rebuild them from
	# the data columns and the remaining parity.
	fec_damage p:0:0 p:1:0
	fec_heal_restore "$work"
	log "corrupt-parity PASS"
}

# scenario_corrupt_max corrupts exactly m=23 blocks of stripe 0: one data
# column (2, never 0 or 1, INDEX's own columns) plus 22 parity columns
# (1..22, never column 0: Heal needs it, uncorrupted, to fill the single
# data column's gap). m corrupted blocks in one stripe is Heal's stated
# limit; it must still recover every one of them.
scenario_corrupt_max() {
	local work="$WORK/cmax"
	fec_setup "$work" "$MULTI_STRIPE_BYTES"

	local args=("2:0")
	for j in $(seq 1 22); do
		args+=("p:$j:0")
	done
	fec_damage "${args[@]}"
	fec_heal_restore "$work"
	log "corrupt-max PASS"
}

# scenario_corrupt_over corrupts m+1=24 data blocks of stripe 0 (columns
# 2..25, never 0 or 1), one more than Heal's parity can recover. verify
# --heal must fail for that stripe, with a message naming the stripe,
# and exit 1. It must leave every other stripe of the healed copy
# untouched: this fixture is large enough that stripe 1 also carries
# real data.
scenario_corrupt_over() {
	local work="$WORK/cover"
	fec_setup "$work" "$MULTI_STRIPE_BYTES"

	local before after
	before="$(run_tool ci-corrupt "$FEC_MNT" peek:2:1)"

	local args=()
	for c in $(seq 2 25); do
		args+=("$c:0")
	done
	fec_damage "${args[@]}"

	expect_exit 1 "stripe 0" "$BIN" --repo="$FEC_REPO" verify --heal --out="$work/healed" "$FEC_MNT"
	grep -qE '^disc [0-9]+ ".*": bad; cannot heal; ' <<<"$EXPECT_OUT" ||
		fail "corrupt-over: no cannot-heal line"

	after="$(run_tool ci-corrupt "$work/healed" peek:2:1)"
	if [ "$before" != "$after" ]; then
		fail "corrupt-over: stripe 1 changed after the failed heal of stripe 0: before [$before] after [$after]"
	fi
	umount_if_mounted "$FEC_MNT"
	log "corrupt-over PASS"
}

scenario_cli() {
	local work="$WORK/cli"
	local repo src tree image mnt commit_out snap restored uuid items
	repo="$work/repo"
	src="$work/src"
	tree="$work/tree"
	image="$work/run.img"
	mnt="$work/mnt"
	restored="$work/restored"

	build_binary
	gen_small_tree "$src"

	(mkdir -p "$repo" && cd "$repo" && "$BIN" init)
	commit_out="$("$BIN" --repo="$repo" commit "$src")"
	echo "$commit_out"
	snap="$(awk '/^snapshot /{print $2}' <<<"$commit_out")"

	# shellcheck disable=SC2046 # media_capacity_flags is a list of flags
	pack_disc "$repo" $(media_capacity_flags "$FIXED_MEDIA") --out="$tree"
	uuid="$PACKED_UUID"
	image_build "$repo" "$uuid" "$image"

	mount_ro "$image" "$mnt"
	assert_listing_matches "$mnt" "$work"
	items="$(run_tool ci-index-count "$mnt")"
	verify_counted "$repo" "$mnt" "$uuid" "$items"
	restore_loop "$repo" "$mnt" "$snap" "$restored"
	assert_dirs_equal "$restored" "$src"
	assert_empty_dir_restored "$restored" ""
	umount_if_mounted "$mnt"
	log "cli PASS"
}

# scenario_media packs, images, mounts, verifies and restores a sample at
# MEDIA's real capacity, and, for a forced media, checks DISC's forced
# capacity fields through ci-disc-field. An over-capacity commit does
# not refuse to pack outright: pack takes what fits onto this disc and
# leaves the remainder staged for the next one; the chain scenario
# covers that. FEC, when non-empty, packs with --fec; lowmem uses this
# to keep FEC on, every other caller leaves it at the default, off.
scenario_media() {
	local media="$1" fec="${2:-}" work="$WORK/media"
	local repo small_src small_src2 tree image mnt restored uuid
	local capflag small_mb apparent
	local packfec=()
	[ -n "$fec" ] && packfec=(--fec)
	repo="$work/repo"
	small_src="$work/small"
	small_src2="$work/small2"
	tree="$work/tree"
	image="$work/run.img"
	mnt="$work/mnt"
	restored="$work/restored"

	build_binary
	small_mb="$(media_small_mb "$media")"
	apparent="$(media_apparent_bytes "$media")"

	gen_fixture "$small_src/data.bin" "$((small_mb * 1024 * 1024))"

	case "$media" in
	bd25-forced-10g)
		capflag="--capacity=10GiB"
		;;
	*)
		capflag="--capacity=$media"
		;;
	esac

	(mkdir -p "$repo" && cd "$repo" && "$BIN" init)

	local commit_out snap
	commit_out="$("$BIN" --repo="$repo" commit --ref=SMALL "$small_src")"
	echo "$commit_out"
	snap="$(awk '/^snapshot /{print $2}' <<<"$commit_out")"

	local bytes t0 t1
	bytes=$((small_mb * 1024 * 1024))

	t0=$(date +%s.%N)
	pack_disc "$repo" "$capflag" "${packfec[@]}" --out="$tree"
	t1=$(date +%s.%N)
	uuid="$PACKED_UUID"
	if [ -n "$fec" ]; then
		pack_rate_line "media/$media: pack (fec on)" "$bytes" "$t0" "$t1"
	else
		pack_rate_line "media/$media: pack (fec off)" "$bytes" "$t0" "$t1"

		# A second, --fec pack, timed the same way, into its own output
		# directory, so its timing does not touch the tree image build
		# continues with. It packs a second, same-sized fixture under
		# its own ref, not ref SMALL again: the first pack already
		# moved ref SMALL's objects to state PACKED, so a second pack
		# of the same ref finds nothing left to pack. Both the second
		# fixture and tree_fec are deleted right after, so this timing
		# run does not add to the cell's disk use. The second disc
		# stays packed; nothing below reads it.
		local tree_fec parity_dir checksum_file commit_out2
		tree_fec="$work/tree-fec"
		gen_fixture2 "$small_src2/data.bin" "$bytes"
		commit_out2="$("$BIN" --repo="$repo" commit --ref=SMALL2 "$small_src2")"
		echo "$commit_out2"

		t0=$(date +%s.%N)
		pack_disc "$repo" "$capflag" --fec --out="$tree_fec"
		t1=$(date +%s.%N)
		pack_rate_line "media/$media: pack (fec on)" "$bytes" "$t0" "$t1"

		parity_dir="$(find "$tree_fec" -type d -name parity -print -quit)"
		checksum_file="$(find "$tree_fec" -type f -name checksum.bin -print -quit)"
		log "media/$media: parity size $(du -sh "$parity_dir" | cut -f1), checksum size $(du -sh "$checksum_file" | cut -f1)"
		rm -rf "$tree_fec" "$small_src2"
	fi

	image_build "$repo" "$uuid" "$image"
	assert_sparse "$image" "$apparent"

	mount_ro "$image" "$mnt"
	verify_counted "$repo" "$mnt" "$uuid"
	if [ "$media" = "bd25-forced-10g" ]; then
		# The superblock keeps one capacity: the limit the run was
		# packed for. Read it straight from DISC.bin with the same Go
		# reader verify uses, through the ci-only helper.
		local field_out
		field_out="$(run_tool ci-disc-field "$mnt")"
		grep -qE 'capacity 5242880 sectors' <<<"$field_out" \
			|| fail "media/$media: DISC.bin did not report the packed-for capacity"
	fi
	restore_loop "$repo" "$mnt" "$snap" "$restored"
	assert_dirs_equal "$restored" "$small_src"
	umount_if_mounted "$mnt"
	log "media/$media PASS"
}

# run_extras EXTRAS runs a comma-separated list of extra scenarios, in
# order, after the cell's primary scenario. See this file's usage
# comment for which scenario names are valid extras.
run_extras() {
	local extras="$1"
	[ -n "$extras" ] || return 0
	local extra
	IFS=',' read -ra extra_list <<<"$extras"
	for extra in "${extra_list[@]}"; do
		case "$extra" in
		cli) scenario_cli ;;
		iso) scenario_iso ;;
		"") ;;
		*) fail "unknown extra scenario: $extra" ;;
		esac
	done
}

main() {
	local scenario="${1:?usage: run.sh SCENARIO [MEDIA] [ORDER] [EXTRAS]}"
	local media="${2:-}"
	local order="${3:-}"
	local extras="${4:-}"
	case "$scenario" in
	media)
		[ -n "$media" ] || fail "the media scenario needs a MEDIA argument"
		scenario_media "$media"
		;;
	corrupt-heal) scenario_corrupt_heal ;;
	corrupt-parity) scenario_corrupt_parity ;;
	corrupt-max) scenario_corrupt_max ;;
	corrupt-over) scenario_corrupt_over ;;
	fec)
		# One cell, four corrupt-and-heal checks in sequence: FEC is
		# optional and off by default, so this whole cell exists only to
		# cover that path when a run does turn it on.
		scenario_corrupt_heal
		scenario_corrupt_parity
		scenario_corrupt_max
		scenario_corrupt_over
		;;
	cli) scenario_cli ;;
	iso) scenario_iso ;;
	chain)
		[ -n "$order" ] || fail "the chain scenario needs an ORDER argument"
		scenario_chain "$order"
		;;
	chain-small)
		[ -n "$order" ] || fail "the chain-small scenario needs an ORDER argument"
		scenario_chain_small "$order"
		;;
	lowmem)
		# The memory bound is enforced on the process from outside (the
		# e2e action wraps this whole script), not by anything in here;
		# this scenario just picks a real, non-trivial flow to run under
		# that limit. It keeps FEC on, since FEC's own stripe-at-a-time
		# memory strategy is exactly what this scenario means to check.
		scenario_media "bd25" 1
		;;
	incremental) scenario_incremental ;;
	rebuild) scenario_rebuild ;;
	lifecycle) scenario_lifecycle ;;
	*) fail "unknown scenario: $scenario" ;;
	esac

	run_extras "$extras"
}

main "$@"
