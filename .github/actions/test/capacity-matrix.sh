#!/usr/bin/env bash
# Runs the noahsark binary through three real capacity cases: a DVD-size
# disc, a full BD-size disc, and a BD disc forced to a smaller limit than
# its physical capacity. Every case uses the real CLI, real mkudffs
# images, and a real loop mount. Shared by the composite test action so
# its steps stay short.
#
# Usage: capacity-matrix.sh ACTION_PATH WORK_DIR BIN_PATH
set -euo pipefail

ACTION_PATH="$1"
WORK="$2"
BIN="$3"

MOUNT_POPULATE="$ACTION_PATH/mount-populate.sh"
UMOUNT_IF_MOUNTED="$ACTION_PATH/umount-if-mounted.sh"

# Data set sizes, in MiB of file content. A run on this project's CI
# runner measured the parity encoder (BenchmarkEncodeStripe) at under
# 10 MB/s, single-threaded and serial per run; a 12 GB data set at that
# rate alone would need over 20 minutes to encode, before any other
# step. These sizes are scaled down from that measurement to keep the
# whole matrix well under the job's time budget, while every case stays
# above 1 GB and keeps the same relationships: SMALL_MB fits well under
# the DVD preset's real capacity; BD_MB fits well under the BD preset's
# real capacity but is kept above FORCED_LIMIT, so the same data set
# both packs at the full BD capacity and is refused at the forced
# limit; DVD_OVER_MB is kept clear of the DVD preset's real capacity
# either way, so pack refuses it regardless of FEC and filesystem
# overhead.
SMALL_MB=1200
BD_MB=3200
DVD_OVER_MB=5200
FORCED_LIMIT="2GB"

DVD_PRESET="dvd+r"
BD_PRESET="bd25"

# Fixed key and IV: the same bytes every run, so every case's fixture is
# byte-identical across runs. AES-CTR keystream bytes are uniformly
# distributed, so this doubles as an incompressible data generator: gzip,
# zstd and any other general-purpose compressor cannot shrink it.
FIXTURE_KEY="000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e"
FIXTURE_IV="000102030405060708090a0b0c0d0e0f"

log() {
	echo "[capacity-matrix] $*"
}

# gen_fixture writes BYTES deterministic, incompressible bytes to PATH.
# head closes its input once it has read enough bytes, so openssl exits
# on a broken pipe; pipefail must stay off around this one pipeline, or
# that expected SIGPIPE would end the matrix.
gen_fixture() {
	local path="$1" bytes="$2"
	mkdir -p "$(dirname "$path")"
	set +o pipefail
	openssl enc -aes-256-ctr -K "$FIXTURE_KEY" -iv "$FIXTURE_IV" -in /dev/zero 2>/dev/null \
		| head -c "$bytes" > "$path"
	set -o pipefail
}

# run_timed LABEL CMD... runs CMD, prints LABEL's wall time, and fails
# the matrix if CMD fails.
run_timed() {
	local label="$1"
	shift
	local start end
	start="$(date +%s)"
	"$@"
	end="$(date +%s)"
	log "$label: $((end - start))s"
}

# expect_refused LABEL CMD... runs CMD, expecting a nonzero exit and a
# stderr message naming the capacity. Fails the matrix otherwise.
expect_refused() {
	local label="$1" out
	shift
	local start end code
	start="$(date +%s)"
	set +e
	out="$("$@" 2>&1)"
	code=$?
	set -e
	end="$(date +%s)"
	log "$label: $((end - start))s, exit $code"
	if [ "$code" -eq 0 ]; then
		echo "$label: expected a nonzero exit, got 0" >&2
		echo "$out" >&2
		exit 1
	fi
	if ! echo "$out" | grep -qi "capacity"; then
		echo "$label: expected the refusal message to name the capacity" >&2
		echo "$out" >&2
		exit 1
	fi
	log "$label: refusal message: $(echo "$out" | tail -1)"
}

free_space() {
	log "free space before $1:"
	df -h "$WORK"
}

# check_sparse asserts that IMAGE's apparent size equals WANT_BYTES and
# that its real disk use is a small fraction of that, proving mkudffs and
# the empty file it is written into stay sparse.
check_sparse() {
	local image="$1" want_bytes="$2"
	local apparent real
	apparent="$(stat --format='%s' "$image")"
	real="$(du --block-size=1 "$image" | cut -f1)"
	log "image $image: apparent $apparent bytes, real $real bytes"
	if [ "$apparent" -ne "$want_bytes" ]; then
		echo "image $image: apparent size $apparent, want $want_bytes" >&2
		exit 1
	fi
	# A tenth of the apparent size is a generous margin: an empty UDF
	# image only carries its descriptors and anchors, a small, roughly
	# fixed cost far below any GB-scale capacity.
	local limit=$((apparent / 10))
	if [ "$real" -ge "$limit" ]; then
		echo "image $image: real use $real is not far below apparent size $apparent; the image is not sparse" >&2
		exit 1
	fi
}

cleanup_mount() {
	local mnt="$1"
	"$UMOUNT_IF_MOUNTED" "$mnt" || true
	sudo losetup -D 2>/dev/null || true
}

measure_fec_throughput() {
	log "measuring parity encode throughput"
	local out repo_root
	repo_root="$(cd "$ACTION_PATH/../../.." && pwd)"
	out="$(cd "$repo_root" && go test -run='^$' -bench=BenchmarkEncodeStripe -benchtime=1s ./internal/fec/)"
	echo "$out"
	local mbps
	mbps="$(echo "$out" | grep -oE '[0-9.]+ MB/s' | grep -oE '[0-9.]+' || true)"
	log "parity encode throughput: ${mbps:-unknown} MB/s"
}

# case_dvd: a small data set packs and images at the DVD preset's real
# capacity, and a larger data set is refused. Mount the small image,
# verify, restore, compare.
case_dvd() {
	log "case 1: DVD (${DVD_PRESET})"
	free_space "case 1"

	local repo="$WORK/cap1-repo"
	local small_src="$WORK/cap1-small"
	local over_src="$WORK/cap1-over"
	local tree="$WORK/cap1-tree"
	local image="$WORK/cap1.img"
	local mnt="$WORK/cap1-mnt"
	local restored="$WORK/cap1-restored"

	gen_fixture "$small_src/data.bin" "$((SMALL_MB * 1024 * 1024))"
	gen_fixture "$over_src/data.bin" "$((DVD_OVER_MB * 1024 * 1024))"

	run_timed "case 1: init" "$BIN" init --repo="$repo" --capacity="$DVD_PRESET"

	local commit_out snap
	commit_out="$("$BIN" commit --repo="$repo" --ref=SMALL "$small_src")"
	echo "$commit_out"
	snap="$(awk '/^snapshot /{print $2}' <<<"$commit_out")"
	run_timed "case 1: commit over-size" "$BIN" commit --repo="$repo" --ref=OVER "$over_src"

	run_timed "case 1: pack (small, fits)" \
		"$BIN" pack --repo="$repo" --ref=SMALL --capacity="$DVD_PRESET" --out="$tree"
	expect_refused "case 1: pack (over-size, refused)" \
		"$BIN" pack --repo="$repo" --ref=OVER --capacity="$DVD_PRESET" --out="$WORK/cap1-over-tree"

	run_timed "case 1: image build" "$BIN" image build --out="$image" --capacity="$DVD_PRESET" "$tree"
	check_sparse "$image" 4700372992

	"$MOUNT_POPULATE" "$image" "$tree" "$mnt"
	"$BIN" verify --image="$mnt"
	"$BIN" restore "$mnt" "$snap" "$restored"
	diff -rq "$restored$small_src" "$small_src"
	cleanup_mount "$mnt"

	rm -rf "$repo" "$small_src" "$over_src" "$tree" "$WORK/cap1-over-tree" "$image" "$mnt" "$restored"
	free_space "case 1 (after cleanup)"
}

# case_bd: a data set of about BD_MB packs and images at the BD preset's
# real capacity. Check the empty image is sparse. Mount, verify, restore,
# compare.
case_bd() {
	log "case 2: BD (${BD_PRESET})"
	free_space "case 2"

	local repo="$WORK/cap2-repo"
	local src="$WORK/cap2-data"
	local tree="$WORK/cap2-tree"
	local image="$WORK/cap2.img"
	local mnt="$WORK/cap2-mnt"
	local restored="$WORK/cap2-restored"

	gen_fixture "$src/data.bin" "$((BD_MB * 1024 * 1024))"

	run_timed "case 2: init" "$BIN" init --repo="$repo" --capacity="$BD_PRESET"

	local commit_out snap
	commit_out="$("$BIN" commit --repo="$repo" "$src")"
	echo "$commit_out"
	snap="$(awk '/^snapshot /{print $2}' <<<"$commit_out")"

	run_timed "case 2: pack" "$BIN" pack --repo="$repo" --capacity="$BD_PRESET" --out="$tree"
	run_timed "case 2: image build" "$BIN" image build --out="$image" --capacity="$BD_PRESET" "$tree"
	check_sparse "$image" 25025314816

	"$MOUNT_POPULATE" "$image" "$tree" "$mnt"
	"$BIN" verify --image="$mnt"
	"$BIN" restore "$mnt" "$snap" "$restored"
	diff -rq "$restored$src" "$src"
	cleanup_mount "$mnt"

	rm -rf "$repo" "$tree" "$image" "$mnt" "$restored"
	# src is kept on disk: case_bd_forced reuses this same big fixture
	# instead of generating another BD_MB data set.
	free_space "case 2 (after cleanup)"
}

# case_bd_forced: the BD_MB data set is refused at a FORCED_LIMIT forced
# capacity. A small data set packs with the BD preset's physical capacity
# and the forced limit. Mount the image and check DISC's capacity fields
# through verify's output.
case_bd_forced() {
	log "case 3: BD (${BD_PRESET}) forced to ${FORCED_LIMIT}"
	free_space "case 3"

	local repo="$WORK/cap3-repo"
	local big_src="$WORK/cap2-data"
	local small_src="$WORK/cap3-small"
	local tree="$WORK/cap3-tree"
	local image="$WORK/cap3.img"
	local mnt="$WORK/cap3-mnt"
	local restored="$WORK/cap3-restored"

	if [ ! -f "$big_src/data.bin" ]; then
		gen_fixture "$big_src/data.bin" "$((BD_MB * 1024 * 1024))"
	fi
	gen_fixture "$small_src/data.bin" "$((SMALL_MB * 1024 * 1024))"

	run_timed "case 3: init" "$BIN" init --repo="$repo" --capacity="$BD_PRESET"

	run_timed "case 3: commit big" "$BIN" commit --repo="$repo" --ref=BIG "$big_src"
	local commit_out snap
	commit_out="$("$BIN" commit --repo="$repo" --ref=SMALL "$small_src")"
	echo "$commit_out"
	snap="$(awk '/^snapshot /{print $2}' <<<"$commit_out")"

	expect_refused "case 3: pack big (refused at forced limit)" \
		"$BIN" pack --repo="$repo" --ref=BIG --capacity="$FORCED_LIMIT" --physical-capacity="$BD_PRESET" --out="$WORK/cap3-big-tree"

	run_timed "case 3: pack small (physical capacity, forced limit)" \
		"$BIN" pack --repo="$repo" --ref=SMALL --capacity="$FORCED_LIMIT" --physical-capacity="$BD_PRESET" --out="$tree"

	run_timed "case 3: image build" "$BIN" image build --out="$image" --capacity="$BD_PRESET" "$tree"
	check_sparse "$image" 25025314816

	"$MOUNT_POPULATE" "$image" "$tree" "$mnt"
	local verify_out
	verify_out="$("$BIN" verify --image="$mnt")"
	echo "$verify_out"
	echo "$verify_out" | grep -qE 'disc capacity: 12219392 sectors, forced 976563 sectors, capacity_is_forced=1' \
		|| { echo "case 3: verify did not report the expected forced capacity fields" >&2; exit 1; }
	"$BIN" restore "$mnt" "$snap" "$restored"
	diff -rq "$restored$small_src" "$small_src"
	cleanup_mount "$mnt"

	rm -rf "$repo" "$big_src" "$small_src" "$tree" "$WORK/cap3-big-tree" "$image" "$mnt" "$restored"
	free_space "case 3 (after cleanup)"
}

main() {
	local start end
	mkdir -p "$WORK"
	start="$(date +%s)"
	measure_fec_throughput
	case_dvd
	case_bd
	case_bd_forced
	end="$(date +%s)"
	log "capacity matrix total: $((end - start))s"
}

main "$@"
