#!/usr/bin/env bash
# Disc e2e scenarios: real mkudffs images, a real read-only loop mount,
# and the real noahsark binary. Needs root
# (loop mount) and udftools (mkudffs). See lib.sh for the shared setup
# and assert.sh for the shared assertions.
#
# Usage: run.sh SCENARIO [MEDIA] [ORDER] [EXTRAS]
#   SCENARIO  media | cli | iso | chain | chain-small | lowmem |
#             incremental | rebuild | lifecycle | damage | hostile |
#             cross-build | cross-restore
#   MEDIA     dvd+r | bd25 | bd25-forced-10g; required for media, unused
#             (and ignored) by every other scenario, which fixes its own
#             fixture at dvd+r's real sector counts. lowmem ignores it
#             too: it runs the media/bd25 flow at two sizes, under
#             whatever process memory limit the caller (the e2e action)
#             applied, and compares the peak memory of the two runs.
#   ORDER     dvd-bd25-bd10 | bd25-bd10-dvd; required for chain and
#             chain-small, unused by every other scenario
#   EXTRAS    a comma-separated list of extra scenarios to run, in
#             order, after SCENARIO finishes, in the same process: the
#             CI matrix folds cli and iso into the media/dvd+r cell this
#             way. Only cli and iso are valid extras. Empty for every
#             other cell.
#
# Environment:
#   NOAHSARK_E2E_DISC_SET    the disc set directory that cross-build
#                            writes and cross-restore reads
#   NOAHSARK_E2E_DAMAGE_SEED a seed that the damage scenario runs in
#                            place of a fresh one
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
# shellcheck source=test/e2e/disc/damage.sh
. "$HERE/damage.sh"
# shellcheck source=test/e2e/disc/hostile.sh
. "$HERE/hostile.sh"
# shellcheck source=test/e2e/disc/cross.sh
. "$HERE/cross.sh"

# FIXED_MEDIA is the media preset every scenario but media builds its
# fixture at: real, but small enough that fixture size never depends on
# it, so a fixed choice keeps those scenarios simple.
FIXED_MEDIA="dvd+r"

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
	items="$(run_tool ci-index-count "$mnt")"
	verify_counted "$repo" "$mnt" "$uuid" "$items"
	restore_loop "$repo" "$mnt" "$snap" "$restored"
	assert_dirs_equal "$restored" "$src"
	assert_empty_dir_restored "$restored" ""
	umount_if_mounted "$mnt"
	log "cli PASS"
}

# scenario_media MEDIA [SIZE_MB] [NAME] packs, images, mounts, verifies
# and restores a sample at MEDIA's real capacity, and, for a forced
# media, checks DISC's forced capacity fields through ci-disc-field.
# SIZE_MB replaces the sample size of MEDIA. NAME replaces the name of
# the work directory. An over-capacity commit does not refuse to pack
# outright: pack takes what fits onto this disc and leaves the remainder
# staged for the next one; the chain scenario covers that.
scenario_media() {
	local media="$1" size_override="${2:-}" name="${3:-media}"
	local work="$WORK/$name"
	local repo small_src tree image mnt restored uuid
	local capflag small_mb apparent
	repo="$work/repo"
	small_src="$work/small"
	tree="$work/tree"
	image="$work/run.img"
	mnt="$work/mnt"
	restored="$work/restored"

	build_binary
	small_mb="${size_override:-$(media_small_mb "$media")}"
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
	pack_disc "$repo" "$capflag" --out="$tree"
	t1=$(date +%s.%N)
	uuid="$PACKED_UUID"
	pack_rate_line "media/$media: pack" "$bytes" "$t0" "$t1"

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

# LOWMEM_SIZES_MB are the two sample sizes of the lowmem scenario. The
# larger one is the bd25 sample of the media scenario.
LOWMEM_SIZES_MB=(300 1200)

# LOWMEM_MEASURED are the subcommands whose peak resident set the lowmem
# scenario compares. image build is not one: its peak is the peak of
# mkudffs.
LOWMEM_MEASURED=(commit pack verify restore)

# lowmem_peak LOG SUB prints the largest peak in KiB that LOG records for
# the subcommand SUB, or 0.
lowmem_peak() {
	awk -v s="$2" '$1 == s && $2 > m { m = $2 } END { print m + 0 }' "$1"
}

# scenario_lowmem runs the bd25 media flow at each size of
# LOWMEM_SIZES_MB, under the memory limit that the caller applied. It
# records the peak resident set of each noahsark call with ci-peak-rss.
# It fails when the peak of a LOWMEM_MEASURED subcommand at the larger
# size exceeds the peak at the smaller size by more than 20 percent plus
# 64 MiB. Peak memory follows the chunk size, not the data size.
scenario_lowmem() {
	build_binary
	local real="$BIN" tool size wrapper sub small large limit
	local small_mb="${LOWMEM_SIZES_MB[0]}" large_mb="${LOWMEM_SIZES_MB[1]}"
	tool="${NOAHSARK_E2E_TOOLDIR:-}/ci-peak-rss"
	if [ ! -x "$tool" ]; then
		tool="$WORK/ci-peak-rss"
		(cd "$ROOT" && go build -o "$tool" ./test/e2e/disc/cmd/ci-peak-rss)
	fi

	local saved_bin="${NOAHSARK_E2E_BIN:-}"
	for size in "${LOWMEM_SIZES_MB[@]}"; do
		wrapper="$WORK/noahsark-rss-$size"
		: >"$WORK/rss-$size.txt"
		printf '#!/bin/sh\nexec "%s" "%s" "%s" "$@"\n' \
			"$tool" "$WORK/rss-$size.txt" "$real" >"$wrapper"
		chmod +x "$wrapper"
		NOAHSARK_E2E_BIN="$wrapper"
		scenario_media bd25 "$size" "lowmem-$size"
	done
	NOAHSARK_E2E_BIN="$saved_bin"
	BIN="$real"

	for sub in "${LOWMEM_MEASURED[@]}"; do
		small="$(lowmem_peak "$WORK/rss-$small_mb.txt" "$sub")"
		large="$(lowmem_peak "$WORK/rss-$large_mb.txt" "$sub")"
		require_number "lowmem: $sub peak at $small_mb MiB" "$small"
		require_number "lowmem: $sub peak at $large_mb MiB" "$large"
		[ "$small" -gt 0 ] || fail "lowmem: no $sub call was measured"
		limit=$((small * 120 / 100 + 64 * 1024))
		log "lowmem: $sub peak RSS $small KiB at $small_mb MiB, $large KiB at $large_mb MiB, limit $limit KiB"
		[ "$large" -le "$limit" ] ||
			fail "lowmem: $sub peak RSS grows with the data size: $large KiB > $limit KiB"
	done
	log "lowmem PASS"
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
		# The e2e action applies the memory limit to this whole script.
		# The scenario also compares the peak memory at two data sizes.
		scenario_lowmem
		;;
	incremental) scenario_incremental ;;
	rebuild) scenario_rebuild ;;
	lifecycle) scenario_lifecycle ;;
	damage) scenario_damage ;;
	hostile) scenario_hostile ;;
	cross-build) scenario_cross_build ;;
	cross-restore) scenario_cross_restore ;;
	*) fail "unknown scenario: $scenario" ;;
	esac

	run_extras "$extras"
}

main "$@"
