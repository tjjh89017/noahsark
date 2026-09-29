#!/usr/bin/env bash
# The chain e2e scenario: sourced by run.sh. Packs one large commit
# across three discs of different media, in one of two orders, and
# checks that restore needs all three and gives the same result either
# way. scenario_chain is the real, full-size scenario the CI matrix
# runs; scenario_chain_small is the same flow at a fast local scale,
# used by TestChainSmall. See run.sh for the shared scenario dispatch
# and lib.sh for the pack, image, mount and restore helpers and the
# media_* helpers.
set -euo pipefail

# pack packs every staged object in the whole repository, from every
# commit; it takes what is staged and names no snapshot of its own. So
# this scenario commits two independent fixtures, A and B, each its own
# snapshot, each sized to CHAIN_HALF_BYTES.
# pack's internal candidate order walks every commit in ascending
# snapshot-id order, one commit's whole tree before the next, so
# whichever of A or B has the lexicographically smaller id is the one
# packed first, and (being far under the three discs' combined
# capacity on its own) the one the three discs fully hold; that one is
# "the winner", and the only one this scenario restores and checks. The
# other is left the partly-packed remainder the scenario asserts on:
# real bytes staged for a real commit, just not this run's winner.
#
# CHAIN_HALF_BYTES is scenario_chain's default size for each of A and B:
# together big enough that three discs (dvd+r, bd25, bd25 forced to
# 10GiB) cannot hold both. Data columns are 231 of every 255 FEC
# columns, so a disc's usable payload is about 90% of its raw capacity;
# at this size the three discs' usable capacity falls short of A+B by a
# few GiB, landing the loser's leftover in the 1 to 10 GiB band the
# scenario checks. Override with NOAHSARK_E2E_CHAIN_HALF_BYTES for a
# different full-size run.
#
# The three discs' usable capacity, FEC off, is about
# 4.68 + 24.97 + 10.70 = 40.35 GB. CI run 34786269740 packed two
# 20,500,000,000-byte fixtures (41.0 GB total) and left the loser's
# remaining bytes at 660,645,722 (dvd-bd25-bd10) and 655,469,257
# (bd25-bd10-dvd): both below the 1 GiB floor, only ~0.66 GB short of
# it. The loser's leftover is 2*CHAIN_HALF_BYTES minus the usable
# capacity, so each extra byte on both fixtures adds twice itself to
# the leftover. Raising CHAIN_HALF_BYTES by 2,000,000,000, to
# 22,500,000,000, adds about 4,000,000,000 bytes to that leftover,
# for an expected ~4.66 GB (about 4.3 GiB): comfortably inside the
# 1-10 GiB band and near its middle, in both orders.
CHAIN_HALF_BYTES="${NOAHSARK_E2E_CHAIN_HALF_BYTES:-22500000000}"
CHAIN_SMALL_HALF_BYTES="${NOAHSARK_E2E_CHAIN_SMALL_HALF_BYTES:-200000000}"
CHAIN_SEED="${NOAHSARK_E2E_CHAIN_SEED:-20260914}"

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
# status then reports staged data: every disc in this scenario is sized
# so real objects remain after it, and leftover staged data is not a
# pack failure. It sets CHAIN_REMAINING_BYTES from the "staged:" line
# of status, and adds the disc uuid to CHAIN_UUIDS.
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
	# copy plus three disc images does not fit on a CI runner disk. The
	# metadata objects stay in the catalog.
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

# chain_run LABEL WORK HALF_BYTES ENFORCE_BAND K1 F1 K2 F2 K3 F3 is the
# flow every chain scenario shares: commit two independent fixtures (A
# and B, each HALF_BYTES), delete both sources, pack three discs (K
# name, F pack flags, one pair per disc), check the staged
# bytes, delete the chunk files of staging, list
# each disc's object ids, restore the winner (the fixture pack's
# candidate order packs first, so the one the three discs fully hold)
# from all three discs and check it against that fixture's manifests,
# then check a two-disc restore fails naming the missing disc. When
# ENFORCE_BAND is "yes" the remaining bytes after the third pack must
# fall in the 1 to 10 GiB band a full-size chain targets.
chain_run() {
	local label="$1" work="$2" half="$3" enforce_band="$4"
	shift 4
	local kinds=("$1" "$3" "$5") packflags=("$2" "$4" "$6")
	log "$label: disc order: ${kinds[0]}, ${kinds[1]}, ${kinds[2]}; two $half byte fixtures"

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

	# Whichever snapshot id sorts first packs first (see chain_run's own
	# comment), so it is the one guaranteed to fit fully in the three
	# discs; that is the winner this scenario restores and checks.
	local snap src sample full sorted_snaps
	sorted_snaps="$(printf '%s\n%s\n' "$snapA" "$snapB" | LC_ALL=C sort)"
	if [ "$(head -1 <<<"$sorted_snaps")" = "$snapA" ]; then
		snap="$snapA"
		src="$srcA"
		sample="$sampleA"
		full="$fullA"
		log "$label: winner is fixture A ($snapA)"
	else
		snap="$snapB"
		src="$srcB"
		sample="$sampleB"
		full="$fullB"
		log "$label: winner is fixture B ($snapB)"
	fi

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

	rm -rf "$repo/staging/chunks"
	log "$label: disk after deleting the chunk files of staging"
	df -h

	log "$label: object ids per disc:"
	for i in 1 2 3; do
		mount_ro "$work/disc$i/run.img" "$work/disc$i/mnt"
		log "$label: disc $i objects:"
		chain_object_ids "$work/disc$i/mnt"
		umount_if_mounted "$work/disc$i/mnt"
	done

	# restore reads the discs one at a time at one mount point, as with
	# one drive. restore_loop swaps the disc that restore asks for.
	local rmnt="$work/rmnt"
	local restored="$work/restored"
	restore_loop "$repo" "$rmnt" "$snap" "$restored"
	log "$label: restore asked for $RESTORE_SWAPS disc(s)"
	run_tool ci-chain-fixture check "$restored" "$sample" "$full"
	log "$label: restored sample and full manifest match"

	# The winner is whichever fixture's snapshot id sorts first, so pack's
	# candidate order selects it first too: its objects start filling
	# disc 1 from the very first pack call. Disc 1 is therefore always
	# among the discs it needs, whatever ENFORCE_BAND or the media sizes
	# do to how far past disc 1 it spreads; omitting disc 1 is the one
	# choice guaranteed to break its restore. The restore starts with
	# disc 2 at the mount point and must stop and ask for disc 1.
	disc_insert "${CHAIN_UUIDS[1]}" "$rmnt"
	restore_expect_missing "$repo" "$rmnt" "${CHAIN_UUIDS[0]}" "$snap" "$work/restored-missing"

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

	chain_run "chain/$order" "$work" "$CHAIN_HALF_BYTES" "yes" \
		"$media1" "$flags1" \
		"$media2" "$flags2" \
		"$media3" "$flags3"
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

	chain_run "chain-small/$order" "$work" "$CHAIN_SMALL_HALF_BYTES" "no" \
		"$k1" "$flags1" \
		"$k2" "$flags2" \
		"$k3" "$flags3"
}
