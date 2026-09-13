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
CHAIN_HALF_BYTES="${NOAHSARK_E2E_CHAIN_HALF_BYTES:-20500000000}"
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

# chain_small_kind_flags KIND prints "PACKFLAGS" then "IMAGECAP" for one
# of scenario_chain_small's three tiny, distinctly-sized stand-ins for
# dvd+r, bd25 and a forced-capacity BD: small, but shaped the same way
# (the third is a smaller logical capacity than its physical size).
chain_small_kind_flags() {
	case "$1" in
	dvd)
		echo "--capacity=90000"
		echo "90000"
		;;
	bd25)
		echo "--capacity=90000"
		echo "90000"
		;;
	bd10)
		echo "--capacity=20000 --physical-capacity=90000"
		echo "90000"
		;;
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

# chain_pack_one WORK REPO N PACKFLAGS IMAGECAP builds and packs disc N,
# images it at IMAGECAP, mounts, populates and verifies it, then
# unmounts, keeping the image but deleting the packed tree. It fails
# unless pack exits 1 with a remaining-staged report: every disc in this
# scenario is sized so real objects remain after it. It sets
# CHAIN_REMAINING_BYTES.
chain_pack_one() {
	local work="$1" repo="$2" n="$3" packflags="$4" imagecap="$5"
	local ddir="$work/disc$n"
	local tree="$ddir/tree" image="$ddir/run.img" mnt="$ddir/mnt" logf="$ddir/pack.log"
	mkdir -p "$ddir"

	local code
	set +e
	# shellcheck disable=SC2086 # packflags is a list of --capacity[=...] words
	"$BIN" pack --repo="$repo" $packflags --out="$tree" >"$logf" 2>&1
	code=$?
	set -e
	cat "$logf"
	if [ "$code" -ne 1 ]; then
		fail "chain: pack disc $n: exit $code, want 1 (objects should remain staged)"
	fi
	if ! grep -q "remaining staged:" "$logf"; then
		fail "chain: pack disc $n: missing remaining-staged report"
	fi
	CHAIN_REMAINING_BYTES="$(grep -oE 'remaining staged: [0-9]+ objects, [0-9]+ bytes' "$logf" | grep -oE '[0-9]+ bytes' | grep -oE '[0-9]+')"
	log "chain: disc $n: $(grep 'remaining staged:' "$logf")"

	"$BIN" image build --out="$image" "--capacity=$imagecap" "$tree"
	mount_populate "$image" "$tree" "$mnt"
	"$BIN" verify --image="$mnt"
	umount_if_mounted "$mnt"
	rm -rf "$tree"
	df -h
}

# chain_object_ids MOUNT prints the sorted list of content-addressed
# object ids physically present on that disc's tree.
chain_object_ids() {
	find "$1/NOAHSARK/objects" -type f -printf '%f\n' 2>/dev/null | sort
}

# chain_assert_missing_disc SNAP OUT DISC... restores from the given
# disc roots, expecting a nonzero exit and a message naming a missing
# disc's uuid.
chain_assert_missing_disc() {
	local snap="$1" out="$2"
	shift 2
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
	if ! echo "$result" | grep -qE 'disc [0-9a-f-]+ holds [0-9]+ needed'; then
		fail "chain: restore-with-a-disc-missing did not name the missing disc's uuid"
	fi
	log "chain: missing-disc restore refused as expected: $(echo "$result" | grep -oE 'disc [0-9a-f-]+ holds [0-9]+ needed[^"]*' | head -1)"
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

# chain_run LABEL WORK HALF_BYTES ENFORCE_BAND K1 F1 I1 K2 F2 I2 K3 F3 I3
# is the flow every chain scenario shares: commit two independent
# fixtures (A and B, each HALF_BYTES), delete both sources, pack three
# discs (K name, F pack flags, I image capacity, one triple per disc),
# check the remaining-staged bytes, delete the staging objects, list
# each disc's object ids, restore the winner (the fixture pack's
# candidate order packs first, so the one the three discs fully hold)
# from all three discs and check it against that fixture's manifests,
# then check a two-disc restore fails naming the missing disc. When
# ENFORCE_BAND is "yes" the remaining bytes after the third pack must
# fall in the 1 to 10 GiB band a full-size chain targets.
chain_run() {
	local label="$1" work="$2" half="$3" enforce_band="$4"
	shift 4
	local kinds=("$1" "$4" "$7") packflags=("$2" "$5" "$8") imagecaps=("$3" "$6" "$9")
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
		chain_pack_one "$work" "$repo" "$i" "${packflags[$((i - 1))]}" "${imagecaps[$((i - 1))]}"
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
	# choice guaranteed to break its restore.
	chain_assert_missing_disc "$snap" "$work/restored-missing" "${discroots[1]}" "${discroots[2]}"

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

	local flags1 flags2 flags3 cap1 cap2 cap3
	flags1="$(media_capacity_flags "$media1")"
	cap1="$(media_image_capacity "$media1")"
	flags2="$(media_capacity_flags "$media2")"
	cap2="$(media_image_capacity "$media2")"
	flags3="$(media_capacity_flags "$media3")"
	cap3="$(media_image_capacity "$media3")"

	chain_run "chain/$order" "$work" "$CHAIN_HALF_BYTES" "yes" \
		"$media1" "$flags1" "$cap1" \
		"$media2" "$flags2" "$cap2" \
		"$media3" "$flags3" "$cap3"
}

# scenario_chain_small ORDER runs the same flow at a fast local scale,
# with tiny forced capacities standing in for the real media presets, so
# the chain flow gets exercised on every push without an hours-long run.
scenario_chain_small() {
	local order="${1:?scenario_chain_small needs an ORDER}"
	local work="$WORK/chain-small-$order"
	local k1 k2 k3
	read -r k1 k2 k3 <<<"$(chain_small_order "$order")"

	local out1 out2 out3 flags1 cap1 flags2 cap2 flags3 cap3
	out1="$(chain_small_kind_flags "$k1")"
	flags1="$(sed -n '1p' <<<"$out1")"
	cap1="$(sed -n '2p' <<<"$out1")"
	out2="$(chain_small_kind_flags "$k2")"
	flags2="$(sed -n '1p' <<<"$out2")"
	cap2="$(sed -n '2p' <<<"$out2")"
	out3="$(chain_small_kind_flags "$k3")"
	flags3="$(sed -n '1p' <<<"$out3")"
	cap3="$(sed -n '2p' <<<"$out3")"

	chain_run "chain-small/$order" "$work" "$CHAIN_SMALL_HALF_BYTES" "no" \
		"$k1" "$flags1" "$cap1" \
		"$k2" "$flags2" "$cap2" \
		"$k3" "$flags3" "$cap3"
}
