#!/usr/bin/env bash
# Shared setup for the disc e2e suite (test/e2e/disc). Sourced by run.sh.
#
# Every scenario builds the noahsark binary once, works in a per-run
# WORK directory, and uses the same mount, populate and unmount helpers
# a UDF image needs everywhere in this suite. Mounting and corrupting a
# loop-mounted image both need root, so run.sh itself runs under sudo;
# functions here assume that.
set -euo pipefail

ROOT="$(CDPATH='' cd "$(dirname "$0")/../../.." && pwd)"
HERE="$(CDPATH='' cd "$(dirname "$0")" && pwd)"
# shellcheck source=test/e2e/disc/assert.sh
. "$HERE/assert.sh"

WORK="${NOAHSARK_E2E_WORKDIR:-$(mktemp -d)}"
BIN="$WORK/noahsark"

log() { echo "[disc-e2e] $*"; }
fail() { echo "[disc-e2e] FAIL: $*" >&2; exit 1; }

build_binary() {
	if [ ! -x "$BIN" ]; then
		log "building the noahsark binary"
		(cd "$ROOT" && go build -o "$BIN" ./cmd/noahsark)
	fi
}

# media_capacity_flags MEDIA prints the --capacity and, when the media
# forces a smaller limit than its physical size, the --physical-capacity
# flags for that preset.
media_capacity_flags() {
	case "$1" in
	dvd+r) echo "--capacity=dvd+r" ;;
	bd25) echo "--capacity=bd25" ;;
	bd25-forced-10g) echo "--capacity=10GiB --physical-capacity=bd25" ;;
	*) fail "unknown media preset: $1" ;;
	esac
}

# media_init_capacity MEDIA prints the single --capacity flag "noahsark
# init" takes; init has no --physical-capacity flag, unlike pack.
media_init_capacity() {
	case "$1" in
	dvd+r) echo "--capacity=dvd+r" ;;
	bd25) echo "--capacity=bd25" ;;
	bd25-forced-10g) echo "--capacity=10GiB" ;;
	*) fail "unknown media preset: $1" ;;
	esac
}

# media_sectors MEDIA prints "TARGET_SECTORS PHYSICAL_SECTORS" for
# ci-fixture, matching media_capacity_flags's preset.
media_sectors() {
	case "$1" in
	dvd+r) echo "2295104 2295104" ;;
	bd25) echo "12219392 12219392" ;;
	bd25-forced-10g) echo "5242880 12219392" ;;
	*) fail "unknown media preset: $1" ;;
	esac
}

# media_apparent_bytes MEDIA prints the real, drive-reported byte size an
# empty image built at that preset's physical capacity must have.
media_apparent_bytes() {
	case "$1" in
	dvd+r) echo 4700372992 ;;
	bd25 | bd25-forced-10g) echo 25025314816 ;;
	*) fail "unknown media preset: $1" ;;
	esac
}

# media_small_mb MEDIA prints a fixture size, in MiB, that packs well
# under that preset's capacity. Sized to keep one matrix cell's whole run
# under about 8 minutes, given the parity encoder's measured throughput.
media_small_mb() {
	case "$1" in
	dvd+r) echo 300 ;;
	bd25) echo 1200 ;;
	bd25-forced-10g) echo 300 ;;
	*) fail "unknown media preset: $1" ;;
	esac
}

# media_over_mb MEDIA prints a fixture size, in MiB, that exceeds that
# preset's usable capacity. pack refuses this data before the slow parity
# encode step runs, so this can be large without pushing a cell over its
# time budget.
media_over_mb() {
	case "$1" in
	dvd+r) echo 4900 ;;
	bd25) echo 24000 ;;
	bd25-forced-10g) echo 10500 ;;
	*) fail "unknown media preset: $1" ;;
	esac
}

# Fixed key and IV: the same bytes every run, so a fixture is
# byte-identical across runs and, being AES-CTR keystream, incompressible.
FIXTURE_KEY="000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e"
FIXTURE_IV="000102030405060708090a0b0c0d0e0f"

# gen_fixture PATH BYTES writes BYTES deterministic, incompressible bytes
# to PATH.
gen_fixture() {
	local path="$1" bytes="$2"
	mkdir -p "$(dirname "$path")"
	set +o pipefail
	openssl enc -aes-256-ctr -K "$FIXTURE_KEY" -iv "$FIXTURE_IV" -in /dev/zero 2>/dev/null \
		| head -c "$bytes" >"$path"
	set -o pipefail
}

# gen_small_tree DIR writes a small, varied source tree at DIR: text
# files worth diffing by content, not just by size.
gen_small_tree() {
	local dir="$1"
	mkdir -p "$dir/sub"
	echo "content of a, for the disc e2e suite" >"$dir/a.txt"
	echo "content of b, also for the disc e2e suite, a bit longer than a" >"$dir/sub/b.txt"
	head -c 65536 /dev/urandom >"$dir/sub/c.bin"
}

# mount_populate IMAGE TREE_DIR MOUNTPOINT loop-mounts a UDF image mkudffs
# built empty, copies a packed NOAHSARK tree into it, and hands ownership
# to the calling user.
mount_populate() {
	local image="$1" tree_dir="$2" mnt="$3"
	mkdir -p "$mnt"
	sudo mount -o loop -t udf "$image" "$mnt"
	sudo cp -a "$tree_dir"/NOAHSARK "$mnt"/
	sudo chown -R "$(id -u):$(id -g)" "$mnt"/NOAHSARK
	sync
}

# umount_if_mounted MOUNTPOINT unmounts it when it is a mountpoint,
# otherwise does nothing.
umount_if_mounted() {
	local mnt="$1"
	if mountpoint -q "$mnt" 2>/dev/null; then
		sudo umount "$mnt"
	fi
}

cleanup_work() {
	for m in "$WORK"/*mnt; do
		[ -d "$m" ] && umount_if_mounted "$m"
	done
	sudo losetup -D 2>/dev/null || true
	if [ -z "${NOAHSARK_E2E_KEEP_WORKDIR:-}" ]; then
		rm -rf "$WORK"
	fi
}
trap cleanup_work EXIT
