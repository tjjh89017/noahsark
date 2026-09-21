#!/usr/bin/env bash
# The chain e2e scenario: sourced by run.sh. Packs one large commit
# across three discs of different media, in one of two orders, and
# checks that restore needs all three and gives the same result either
# way. scenario_chain is the real, full-size scenario the CI matrix
# runs; scenario_chain_small is the same flow at a fast local scale,
# used by TestChainSmall. See run.sh for the shared scenario dispatch
# and lib.sh for build_binary, mount_populate and the media_* helpers.
set -euo pipefail

# pack packs every staged object in the whole repository, from every
# commit, whichever --ref or --snapshot it is told to pack: the flag
# only picks which snapshot the packed run's REFS table names, not which
# objects are candidates. So this scenario commits two independent
# fixtures, A and B, each its own snapshot, each sized to CHAIN_HALF_BYTES.
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
# "remaining staged" byte count from that pack's own output.
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

# chain_media_pack_flags MEDIA prints the pack --capacity (and, for a
# forced media, --physical-capacity) flags for one real disc, at
# media_sectors' raw target: pack's own budget already reserves the
# filesystem overhead a run needs, in whole FEC stripes, so packing at
# the preset's full sector count leaves the run inside the real UDF
# image mkudffs builds at the same capacity. media_capacity_flags'
# preset names leave no room to compute a physical capacity for the
# forced case, so this builds the flags from media_sectors' numbers
# instead. --capacity refuses a bare number, so each count is given in
# whole binary kibibytes, two per sector.
chain_media_pack_flags() {
	local media="$1" target physical
	read -r target physical <<<"$(media_sectors "$media")"
	case "$media" in
	dvd+r | bd25) echo "--capacity=$((target * 2))KiB" ;;
	bd25-forced-10g) echo "--capacity=$((target * 2))KiB --physical-capacity=$((physical * 2))KiB" ;;
	*) fail "unknown chain media: $media" ;;
	esac
}

# chain_small_kind_flags KIND prints the pack flags for one of
# scenario_chain_small's three tiny, distinctly-sized stand-ins for
# dvd+r, bd25 and a forced-capacity BD: small, but shaped the same way
# (the third is a smaller logical capacity than its physical size).
chain_small_kind_flags() {
	case "$1" in
	dvd) echo "--capacity=180000KiB" ;;
	bd25) echo "--capacity=180000KiB" ;;
	bd10) echo "--capacity=40000KiB --physical-capacity=180000KiB" ;;
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

# chain_pack_one WORK REPO N PACKFLAGS builds and packs disc N, images
# it at the capacity its own DISC.bin carries, mounts, populates and
# verifies it, then
# unmounts, keeping the image but deleting the packed tree. It also frees
# the staged copy of each object on this disc. It fails unless pack exits
# 0 with a remaining-staged report: every disc in this scenario is sized
# so real objects remain after it, and leftover staged data is not a
# pack failure. It sets CHAIN_REMAINING_BYTES.
chain_pack_one() {
	local work="$1" repo="$2" n="$3" packflags="$4"
	local ddir="$work/disc$n"
	local tree="$ddir/tree" image="$ddir/run.img" mnt="$ddir/mnt" logf="$ddir/pack.log"
	mkdir -p "$ddir"

	local code
	set +e
	# --ref=A: pack requires a resolvable ref or --snapshot even though,
	# per this file's own top comment, the ref it is given never narrows
	# which objects it packs. Fixture A always exists, so it always
	# resolves.
	# shellcheck disable=SC2086 # packflags is a list of --capacity[=...] words
	"$BIN" pack --repo="$repo" --ref=A $packflags --out="$tree" >"$logf" 2>&1
	code=$?
	set -e
	cat "$logf"
	if [ "$code" -ne 0 ]; then
		fail "chain: pack disc $n: exit $code, want 0 (objects should remain staged)"
	fi
	if ! grep -q "remaining staged:" "$logf"; then
		fail "chain: pack disc $n: missing remaining-staged report"
	fi
	CHAIN_REMAINING_BYTES="$(grep -oE 'remaining staged: [0-9]+ objects, [0-9]+ bytes' "$logf" | grep -oE '[0-9]+ bytes' | grep -oE '[0-9]+')"
	log "chain: disc $n: $(grep 'remaining staged:' "$logf")"

	sudo "$BIN" image build --out="$image" "$tree"
	mount_populate "$image" "$tree" "$mnt"
	# Unmount whether verify passes or fails: a failure must not leave
	# the mount busy for the runner's own cleanup.
	set +e
	"$BIN" verify "$mnt"
	code=$?
	set -e
	umount_if_mounted "$mnt"
	if [ "$code" -ne 0 ]; then
		fail "chain: verify disc $n: exit $code"
	fi

	# Free the staged copy of each object on this disc. The tree uses the
	# same fan-out path as staging. A later pack never reads the staged
	# bytes of a packed object. The full staging copy plus three disc
	# images does not fit on a CI runner disk.
	if [ -d "$tree/NOAHSARK/objects" ]; then
		local f rel
		while IFS= read -r -d '' f; do
			rel="${f#"$tree/NOAHSARK/objects/"}"
			rm -f "$repo/staging/objects/$rel"
		done < <(find "$tree/NOAHSARK/objects" -type f -print0)
	fi

	rm -rf "$tree"
	df -h
}

# chain_object_ids MOUNT prints the sorted list of content-addressed
# object ids physically present on that disc's tree.
chain_object_ids() {
	find "$1/NOAHSARK/objects" -type f -printf '%f\n' 2>/dev/null | sort
}

# chain_assert_missing_disc SNAP OUT MISSING_DISC_ROOT DISC... restores
# from the given disc roots (MISSING_DISC_ROOT itself left out),
# expecting a nonzero exit and a message naming MISSING_DISC_ROOT's uuid.
# The message can take either form the restore package prints: a
# Prereqs-named "disc UUID holds N needed" line, or, when no provided
# disc's INDEX or Prereqs names the missing objects, the DISCS-table
# candidate line; either way the omitted disc's uuid must appear.
chain_assert_missing_disc() {
	local snap="$1" out="$2" missing_root="$3"
	shift 3
	local missing_uuid
	missing_uuid="$(run_tool ci-disc-uuid "$missing_root")"
	local args=(restore)
	local d
	for d in "$@"; do
		args+=("--disc=$d")
	done
	args+=("$snap" "$out")
	local result code
	set +e
	result="$("$BIN" "${args[@]}" 2>&1)"
	code=$?
	set -e
	echo "$result"
	if [ "$code" -eq 0 ]; then
		fail "chain: restore with a disc missing exited 0, want nonzero"
	fi
	if ! echo "$result" | grep -qF "$missing_uuid"; then
		fail "chain: restore-with-a-disc-missing did not name the missing disc's uuid ($missing_uuid)"
	fi
	log "chain: missing-disc restore refused as expected, naming disc $missing_uuid: $(echo "$result" | grep -F "$missing_uuid" | head -1)"
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
	commit_out="$("$BIN" commit --repo="$repo" --ref="$name" "$src")"
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
# name, F pack flags, one pair per disc), check the remaining-staged
# bytes, delete the staging objects, list
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

	"$BIN" init --repo="$repo"

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
	local snap src sample full
	if [ "$(printf '%s\n%s\n' "$snapA" "$snapB" | LC_ALL=C sort | head -1)" = "$snapA" ]; then
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

	local discroots=()
	local i
	for i in 1 2 3; do
		chain_pack_one "$work" "$repo" "$i" "${packflags[$((i - 1))]}"
		discroots+=("$work/disc$i/mnt")
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

	rm -rf "$repo/staging/objects"
	log "$label: disk after deleting staging objects"
	df -h

	log "$label: object ids per disc:"
	for i in 1 2 3; do
		sudo mount -o loop -t udf "$work/disc$i/run.img" "$work/disc$i/mnt"
		log "$label: disc $i objects:"
		chain_object_ids "$work/disc$i/mnt"
	done

	local restored="$work/restored"
	"$BIN" restore --disc="${discroots[0]}" --disc="${discroots[1]}" --disc="${discroots[2]}" "$snap" "$restored"
	run_tool ci-chain-fixture check "$restored$src" "$sample" "$full"
	log "$label: restored sample and full manifest match"

	# The winner is whichever fixture's snapshot id sorts first, so pack's
	# candidate order selects it first too: its objects start filling
	# disc 1 from the very first pack call. Disc 1 is therefore always
	# among the discs it needs, whatever ENFORCE_BAND or the media sizes
	# do to how far past disc 1 it spreads; omitting disc 1 is the one
	# choice guaranteed to break its restore. In this scenario the winner
	# can fit entirely on disc 1, so discs 2 and 3 never name it in
	# Prereqs; the restore package then falls back to disc 2 or 3's DISCS
	# table to name disc 1 as a candidate instead.
	chain_assert_missing_disc "$snap" "$work/restored-missing" "${discroots[0]}" "${discroots[1]}" "${discroots[2]}"

	for r in "${discroots[@]}"; do
		umount_if_mounted "$r"
	done
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
