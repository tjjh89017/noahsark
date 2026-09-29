#!/usr/bin/env bash
# The chain e2e scenario: sourced by run.sh. Packs two large commits
# across three discs of different media, in one of two orders, then
# packs the rest on a fourth disc. It deletes the repository, recovers
# it from the four discs alone, and restores both commits from the
# discs. scenario_chain is the real, full-size scenario the CI matrix
# runs; scenario_chain_small is the same flow at a fast local scale,
# used by TestChainSmall. See run.sh for the shared scenario dispatch
# and lib.sh for the pack, image, mount, recover and restore helpers and
# the media_* helpers.
set -euo pipefail

# pack packs every staged object in the whole repository, from every
# commit; it takes what is staged and names no snapshot of its own. So
# this scenario commits two independent fixtures, A and then B, each its
# own snapshot, each sized to CHAIN_HALF_BYTES.
# pack's candidate order takes a snapshot that is packed in parts first,
# then the other snapshots, the oldest snapshot time first. A is older
# than B, thus pack takes all of A first, then B. A is far under the
# three discs' combined capacity, thus the three discs hold all of A and
# a part of B. After the third pack, status names B as packed in parts,
# and the rest of B stays staged. The fourth disc takes that rest.
#
# CHAIN_HALF_BYTES is scenario_chain's default size for each of A and B:
# together big enough that three discs (dvd+r, bd25, bd25 forced to
# 10GiB) cannot hold both. Data columns are 231 of every 255 FEC
# columns, so a disc's usable payload is about 90% of its raw capacity;
# at this size the three discs' usable capacity falls short of A+B by a
# few GiB, landing the rest of B in the 1 to 10 GiB band the scenario
# checks. Override with NOAHSARK_E2E_CHAIN_HALF_BYTES for a different
# full-size run.
#
# The three discs' usable capacity, FEC off, is about
# 4.68 + 24.97 + 10.70 = 40.35 GB. CI run 34786269740 packed two
# 20,500,000,000-byte fixtures (41.0 GB total) and left the remaining
# bytes at 660,645,722 (dvd-bd25-bd10) and 655,469,257
# (bd25-bd10-dvd): both below the 1 GiB floor, only ~0.66 GB short of
# it. The rest is 2*CHAIN_HALF_BYTES minus the usable capacity, so each
# extra byte on both fixtures adds twice itself to the rest. Raising
# CHAIN_HALF_BYTES by 2,000,000,000, to 22,500,000,000, adds about
# 4,000,000,000 bytes to that rest, for an expected ~4.66 GB (about
# 4.3 GiB): comfortably inside the 1-10 GiB band and near its middle,
# in both orders.
CHAIN_HALF_BYTES="${NOAHSARK_E2E_CHAIN_HALF_BYTES:-22500000000}"
CHAIN_SMALL_HALF_BYTES="${NOAHSARK_E2E_CHAIN_SMALL_HALF_BYTES:-200000000}"
CHAIN_SEED="${NOAHSARK_E2E_CHAIN_SEED:-20260914}"

# CHAIN_REST_MEDIA is the medium of the fourth disc of scenario_chain.
# Its usable payload, about 10.70 GB, holds a rest inside the band
# above. CHAIN_SMALL_REST_KIND is the same for scenario_chain_small: a
# local run left a rest of about 23 MB, and this kind holds about 40 MB.
CHAIN_REST_MEDIA="bd25-forced-10g"
CHAIN_SMALL_REST_KIND="bd10"

# CHAIN_REMAINING_BYTES is set by chain_pack_one as a side effect: the
# staged byte count from the "staged:" line of status after that pack.
CHAIN_REMAINING_BYTES=""

# CHAIN_SNAP, CHAIN_SRC, CHAIN_SAMPLE and CHAIN_FULL are set by
# chain_commit_fixture as a side effect: see its own comment.
CHAIN_SNAP=""
CHAIN_SRC=""
CHAIN_SAMPLE=""
CHAIN_FULL=""

# chain_media_order ORDER prints the three real media presets, in order,
# ORDER names.
chain_media_order() {
	case "$1" in
	dvd-bd25-bd10) echo "dvd+r bd25 bd25-forced-10g" ;;
	bd25-bd10-dvd) echo "bd25 bd25-forced-10g dvd+r" ;;
	*) fail "unknown chain order: $1" ;;
	esac
}

# chain_media_pack_flags MEDIA prints the pack --capacity flag for one
# real disc, at media_sectors' raw target: pack's own budget already
# reserves the filesystem overhead a run needs, in whole FEC stripes,
# so packing at the preset's full sector count leaves the run inside
# the real UDF image mkudffs builds at the same capacity.
# media_capacity_flags' preset names leave no room to compute this from
# a preset name for the bd25-forced-10g case, so this builds the flag
# from media_sectors' number instead. --capacity refuses a bare number,
# so the count is given in whole binary kibibytes, two per sector.
chain_media_pack_flags() {
	local media="$1" target
	target="$(media_sectors "$media")"
	case "$media" in
	dvd+r | bd25 | bd25-forced-10g) echo "--capacity=$((target * 2))KiB" ;;
	*) fail "unknown chain media: $media" ;;
	esac
}

# chain_small_kind_flags KIND prints the pack flags for one of
# scenario_chain_small's three tiny, distinctly-sized stand-ins for
# dvd+r, bd25 and a third, smaller-capacity disc.
chain_small_kind_flags() {
	case "$1" in
	dvd) echo "--capacity=180000KiB" ;;
	bd25) echo "--capacity=180000KiB" ;;
	bd10) echo "--capacity=40000KiB" ;;
	*) fail "unknown chain-small kind: $1" ;;
	esac
}

# chain_small_order ORDER prints the three chain_small_kind_flags kinds,
# in order, matching chain_media_order's two orders.
chain_small_order() {
	case "$1" in
	dvd-bd25-bd10) echo "dvd bd25 bd10" ;;
	bd25-bd10-dvd) echo "bd25 bd10 dvd" ;;
	*) fail "unknown chain order: $1" ;;
	esac
}

# CHAIN_UUIDS holds the uuid of each disc that chain_pack_one packed, in
# pack order.
CHAIN_UUIDS=()

# chain_pack_one WORK REPO N PACKFLAGS builds and packs disc N, images
# it at the capacity its own DISC.bin carries, mounts the image
# read-only and verifies it, then unmounts, keeping the image but
# deleting the packed tree. gc then frees the staged copy of each item
# on this disc. It fails unless pack exits 0 with a packed-disc line and
# status then prints the staged line. Staged data that remains after a
# pack is not a pack failure. It sets CHAIN_REMAINING_BYTES from the
# "staged:" line of status, and adds the disc uuid to CHAIN_UUIDS.
chain_pack_one() {
	local work="$1" repo="$2" n="$3" packflags="$4"
	local ddir="$work/disc$n"
	local tree="$ddir/tree" image="$ddir/run.img" mnt="$ddir/mnt" logf="$ddir/pack.log"
	mkdir -p "$ddir"

	local code
	set +e
	# pack takes every pending ref; it names none of its own.
	# shellcheck disable=SC2086 # packflags is a list of --capacity[=...] words
	"$BIN" --repo="$repo" pack $packflags --out="$tree" >"$logf" 2>&1
	code=$?
	set -e
	cat "$logf"
	if [ "$code" -ne 0 ]; then
		fail "chain: pack disc $n: exit $code, want 0 (objects should remain staged)"
	fi
	if ! grep -q '^packed disc ' "$logf"; then
		fail "chain: pack disc $n: missing the packed-disc line"
	fi
	local uuid
	uuid="$(awk '/^uuid: /{print $2}' "$logf")"
	[ -n "$uuid" ] || fail "chain: pack disc $n: missing the uuid line"
	CHAIN_UUIDS+=("$uuid")
	local staged
	staged="$("$BIN" --repo="$repo" status | grep -E '^staged: [0-9]+ [a-z()]+, [0-9]+ bytes$')" ||
		fail "chain: status after pack disc $n: missing the staged line"
	CHAIN_REMAINING_BYTES="$(echo "$staged" | grep -oE '[0-9]+ bytes$' | grep -oE '^[0-9]+')"
	log "chain: disc $n: $staged"

	image_build "$repo" "$uuid" "$image"
	mount_ro "$image" "$mnt"
	verify_counted "$repo" "$mnt" "$uuid"
	umount_if_mounted "$mnt"

	# Free the chunk files in staging of each item on this disc. A later
	# pack never reads the chunk file of a packed chunk. The full staging
	# copy plus four disc images does not fit on a CI runner disk. gc
	# never frees the chunk file of a staged item, thus the rest of B
	# stays for the next pack. The metadata objects stay in the catalog.
	local gc_out
	gc_out="$("$BIN" --repo="$repo" gc --force-after=0d)"
	echo "$gc_out"
	grep -qE '^gc: freed [1-9][0-9]* item\(s\), ' <<<"$gc_out" ||
		fail "chain: gc after disc $n freed nothing"
	assert_disc_state "$repo" "$uuid" "on disc only, last check *"

	rm -rf "$tree"
	df -h
}

# chain_object_ids MOUNT prints the sorted list of content-addressed
# object ids physically present on that disc's tree.
chain_object_ids() {
	find "$1/NOAHSARK/objects" -type f -printf '%f\n' 2>/dev/null | sort
}

# chain_commit_fixture LABEL WORK REPO NAME HALF_BYTES SEED builds and
# commits one of the two fixtures chain_run packs, timing both steps.
# It sets CHAIN_SNAP, CHAIN_SRC, CHAIN_SAMPLE and CHAIN_FULL as a side
# effect instead of printing its result: it already prints a lot of its
# own diagnostic output, which a caller capturing this function's stdout
# would swallow along with the real result.
chain_commit_fixture() {
	local label="$1" work="$2" repo="$3" name="$4" half="$5" seed="$6"
	local src="$work/src-$name" sample="$work/sample-$name.txt" full="$work/full-$name.txt"

	local t0 t1
	t0=$(date +%s)
	run_tool ci-chain-fixture gen "$src" "$half" "$seed" "$sample" "$full"
	t1=$(date +%s)
	log "$label: fixture $name generation took $((t1 - t0))s"

	t0=$(date +%s)
	local commit_out
	commit_out="$("$BIN" --repo="$repo" commit --ref="$name" "$src")"
	t1=$(date +%s)
	echo "$commit_out"
	CHAIN_SNAP="$(awk '/^snapshot /{print $2}' <<<"$commit_out")"
	CHAIN_SRC="$src"
	CHAIN_SAMPLE="$sample"
	CHAIN_FULL="$full"
	local secs=$((t1 - t0))
	[ "$secs" -lt 1 ] && secs=1
	log "$label: commit $name took ${secs}s, $((half / secs / 1000000)) MB/s"
	log "$label: fixture $name: sample manifest $(wc -l <"$sample") lines, full manifest $(wc -l <"$full") lines"
}

# chain_assert_parts LABEL REPO PARTS DONE... fails unless the status of
# REPO names the snapshot PARTS as packed in parts, and names no snapshot
# of DONE. An empty PARTS asks for no snapshot line at all. Each id is
# the full text form; status names a snapshot by the 12 characters after
# the multihash prefix 1220.
chain_assert_parts() {
	local label="$1" repo="$2" parts="$3" out id
	shift 3
	out="$("$BIN" --repo="$repo" status)"
	echo "$out"
	if [ -n "$parts" ]; then
		grep -qxE "snapshot ${parts:4:12}: [0-9]+ items staged, not complete on discs; recover cannot find it from the discs alone" <<<"$out" ||
			fail "$label: status does not name snapshot ${parts:4:12} as packed in parts"
	elif grep -q '^snapshot ' <<<"$out"; then
		fail "$label: status names a snapshot as packed in parts, want none"
	fi
	for id in "$@"; do
		if grep -q "^snapshot ${id:4:12}: " <<<"$out"; then
			fail "$label: status names snapshot ${id:4:12} as packed in parts, want it complete on discs"
		fi
	done
}

# chain_run LABEL WORK HALF_BYTES ENFORCE_BAND K1 F1 K2 F2 K3 F3 K4 F4 is
# the flow every chain scenario shares: commit two independent fixtures
# (A, then B, each HALF_BYTES), delete both sources, pack three discs (K
# name, F pack flags, one pair per disc), and check the staged bytes and
# that status names B as packed in parts. Then pack the rest of B on the
# fourth disc, list each disc's object ids, delete the repository,
# recover it from the four discs, restore A and B from the discs and
# check each against its fixture's manifests, and check that a restore
# of A without disc 1 stops and asks for disc 1. When ENFORCE_BAND is
# "yes" the remaining bytes after the third pack must fall in the 1 to
# 10 GiB band a full-size chain targets.
chain_run() {
	local label="$1" work="$2" half="$3" enforce_band="$4"
	shift 4
	local kinds=("$1" "$3" "$5" "$7") packflags=("$2" "$4" "$6" "$8")
	log "$label: disc order: ${kinds[0]}, ${kinds[1]}, ${kinds[2]}, then ${kinds[3]} for the rest; two $half byte fixtures"

	build_binary
	local repo="$work/repo"

	log "$label: disk before fixture generation"
	df -h

	(mkdir -p "$repo" && cd "$repo" && "$BIN" init)

	local snapA srcA sampleA fullA snapB srcB sampleB fullB
	chain_commit_fixture "$label" "$work" "$repo" A "$half" "$CHAIN_SEED"
	snapA="$CHAIN_SNAP"
	srcA="$CHAIN_SRC"
	sampleA="$CHAIN_SAMPLE"
	fullA="$CHAIN_FULL"
	chain_commit_fixture "$label" "$work" "$repo" B "$half" "$((CHAIN_SEED + 1))"
	snapB="$CHAIN_SNAP"
	srcB="$CHAIN_SRC"
	sampleB="$CHAIN_SAMPLE"
	fullB="$CHAIN_FULL"
	df -h

	rm -rf "$srcA" "$srcB"
	log "$label: disk after deleting both sources"
	df -h

	CHAIN_UUIDS=()
	local i
	for i in 1 2 3; do
		chain_pack_one "$work" "$repo" "$i" "${packflags[$((i - 1))]}"
	done

	local remaining_gib=$((CHAIN_REMAINING_BYTES / 1073741824))
	log "$label: remaining after third pack: $CHAIN_REMAINING_BYTES bytes (~${remaining_gib} GiB)"
	if [ "$CHAIN_REMAINING_BYTES" -le 0 ]; then
		fail "$label: no objects remained after the third pack"
	fi
	if [ "$enforce_band" = "yes" ]; then
		if [ "$CHAIN_REMAINING_BYTES" -le 1073741824 ]; then
			fail "$label: remaining bytes $CHAIN_REMAINING_BYTES is not above 1 GiB"
		fi
		if [ "$CHAIN_REMAINING_BYTES" -ge 10737418240 ]; then
			fail "$label: remaining bytes $CHAIN_REMAINING_BYTES is not below 10 GiB"
		fi
	fi

	# A is the older snapshot, thus pack takes A first: the three discs
	# hold all of A, and a part of B. status names B, and not A.
	chain_assert_parts "$label" "$repo" "$snapB" "$snapA"

	chain_pack_one "$work" "$repo" 4 "${packflags[3]}"
	log "$label: remaining after fourth pack: $CHAIN_REMAINING_BYTES bytes"
	[ "$CHAIN_REMAINING_BYTES" -eq 0 ] ||
		fail "$label: $CHAIN_REMAINING_BYTES bytes remained after the fourth pack, want 0"
	chain_assert_parts "$label" "$repo" "" "$snapA" "$snapB"

	log "$label: object ids per disc:"
	for i in 1 2 3 4; do
		mount_ro "$work/disc$i/run.img" "$work/disc$i/mnt"
		log "$label: disc $i objects:"
		chain_object_ids "$work/disc$i/mnt"
		umount_if_mounted "$work/disc$i/mnt"
	done

	# Lose the repository with its staging directory. recover builds it
	# again from the four discs alone.
	rm -rf "$repo"
	log "$label: disk after deleting the repository"
	df -h
	for i in 1 2 3 4; do
		mount_ro "$work/disc$i/run.img" "$work/disc$i/mnt"
		recover_disc "$repo" "$srcA" "$work/disc$i/mnt" 0
		umount_if_mounted "$work/disc$i/mnt"
	done

	# restore reads the discs one at a time at one mount point, as with
	# one drive. restore_loop swaps the disc that restore asks for.
	local rmnt="$work/rmnt"
	local restored="$work/restored"
	restore_loop "$repo" "$rmnt" "$snapA" "$restored"
	log "$label: restore of A asked for $RESTORE_SWAPS disc(s)"
	run_tool ci-chain-fixture check "$restored" "$sampleA" "$fullA"
	log "$label: A: restored sample and full manifest match"
	rm -rf "$restored"

	# pack takes A first, thus A's objects start filling disc 1 from the
	# very first pack call. Disc 1 is therefore always among the discs it
	# needs, whatever ENFORCE_BAND or the media sizes do to how far past
	# disc 1 it spreads; omitting disc 1 is the one choice guaranteed to
	# break its restore. The restore starts with disc 2 at the mount point
	# and must stop and ask for disc 1.
	disc_insert "${CHAIN_UUIDS[1]}" "$rmnt"
	restore_expect_missing "$repo" "$rmnt" "${CHAIN_UUIDS[0]}" "$snapA" "$work/restored-missing"
	rm -rf "$work/restored-missing"

	restore_loop "$repo" "$rmnt" "$snapB" "$restored"
	log "$label: restore of B asked for $RESTORE_SWAPS disc(s)"
	run_tool ci-chain-fixture check "$restored" "$sampleB" "$fullB"
	log "$label: B: restored sample and full manifest match"
	rm -rf "$restored"

	umount_if_mounted "$rmnt"
	log "$label: disk after restore"
	df -h
	log "$label PASS"
}

# scenario_chain ORDER is the full-size chain scenario the CI matrix
# runs, over the real dvd+r, bd25 and bd25-forced-10g media.
scenario_chain() {
	local order="${1:?scenario_chain needs an ORDER}"
	local work="$WORK/chain-$order"
	local media1 media2 media3
	read -r media1 media2 media3 <<<"$(chain_media_order "$order")"

	local flags1 flags2 flags3
	flags1="$(chain_media_pack_flags "$media1")"
	flags2="$(chain_media_pack_flags "$media2")"
	flags3="$(chain_media_pack_flags "$media3")"

	local flags4
	flags4="$(chain_media_pack_flags "$CHAIN_REST_MEDIA")"

	chain_run "chain/$order" "$work" "$CHAIN_HALF_BYTES" "yes" \
		"$media1" "$flags1" \
		"$media2" "$flags2" \
		"$media3" "$flags3" \
		"$CHAIN_REST_MEDIA" "$flags4"
}

# scenario_chain_small ORDER runs the same flow at a fast local scale,
# with tiny forced capacities standing in for the real media presets, so
# the chain flow gets exercised on every push without an hours-long run.
scenario_chain_small() {
	local order="${1:?scenario_chain_small needs an ORDER}"
	local work="$WORK/chain-small-$order"
	local k1 k2 k3
	read -r k1 k2 k3 <<<"$(chain_small_order "$order")"

	local flags1 flags2 flags3
	flags1="$(chain_small_kind_flags "$k1")"
	flags2="$(chain_small_kind_flags "$k2")"
	flags3="$(chain_small_kind_flags "$k3")"

	local flags4
	flags4="$(chain_small_kind_flags "$CHAIN_SMALL_REST_KIND")"

	chain_run "chain-small/$order" "$work" "$CHAIN_SMALL_HALF_BYTES" "no" \
		"$k1" "$flags1" \
		"$k2" "$flags2" \
		"$k3" "$flags3" \
		"$CHAIN_SMALL_REST_KIND" "$flags4"
}
