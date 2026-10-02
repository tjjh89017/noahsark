#!/usr/bin/env bash
# Shared setup for the disc e2e suite (test/e2e/disc). Sourced by run.sh.
#
# Every scenario builds the noahsark binary once, works in a per-run
# WORK directory, and uses the same pack, image, mount, verify, recover
# and restore helpers. image build fills the image. The tool reads only
# a read-only mount. Mounting and corrupting a loop-mounted image both
# need root, so run.sh itself runs under sudo; functions here assume
# that.
set -euo pipefail

ROOT="$(CDPATH='' cd "$(dirname "$0")/../../.." && pwd)"
HERE="$(CDPATH='' cd "$(dirname "$0")" && pwd)"
# shellcheck source=test/e2e/disc/assert.sh
. "$HERE/assert.sh"

WORK="${NOAHSARK_E2E_WORKDIR:-$(mktemp -d)}"
BIN="$WORK/noahsark"

log() { echo "[disc-e2e] $*"; }
fail() { echo "[disc-e2e] FAIL: $*" >&2; exit 1; }

# require_number NAME VALUE fails unless VALUE is a whole number. A test
# such as [ "$x" -ge 1 ] with an empty or other text x is an error, and
# an error inside an if condition does not stop the script.
require_number() {
	[[ "$2" =~ ^[0-9]+$ ]] || fail "$1 is not a number: [$2]"
}

# build_binary sets BIN to a runnable noahsark binary: NOAHSARK_E2E_BIN
# when the caller (the e2e action) already built one outside sudo, else
# a binary this scenario builds itself.
build_binary() {
	if [ -n "${NOAHSARK_E2E_BIN:-}" ] && [ -x "$NOAHSARK_E2E_BIN" ]; then
		BIN="$NOAHSARK_E2E_BIN"
		return
	fi
	if [ ! -x "$BIN" ]; then
		log "building the noahsark binary"
		(cd "$ROOT" && go build -o "$BIN" ./cmd/noahsark)
	fi
}

# run_tool NAME [ARGS...] runs one of the disc e2e helper commands
# (test/e2e/disc/cmd/NAME): the prebuilt binary at NOAHSARK_E2E_TOOLDIR
# when the e2e action built one outside sudo, else `go run` against its
# source, for a caller running the suite directly.
run_tool() {
	local name="$1"
	shift
	if [ -n "${NOAHSARK_E2E_TOOLDIR:-}" ] && [ -x "$NOAHSARK_E2E_TOOLDIR/$name" ]; then
		"$NOAHSARK_E2E_TOOLDIR/$name" "$@"
	else
		go run "$ROOT/test/e2e/disc/cmd/$name" "$@"
	fi
}

# media_capacity_flags MEDIA prints the --capacity flag for that preset.
media_capacity_flags() {
	case "$1" in
	dvd+r) echo "--capacity=dvd+r" ;;
	bd25) echo "--capacity=bd25" ;;
	bd25-forced-10g) echo "--capacity=10GiB" ;;
	*) fail "unknown media preset: $1" ;;
	esac
}

# media_sectors MEDIA prints TARGET_SECTORS for ci-fixture, matching
# media_capacity_flags's preset.
media_sectors() {
	case "$1" in
	dvd+r) echo "2295104" ;;
	bd25) echo "12219392" ;;
	bd25-forced-10g) echo "5242880" ;;
	*) fail "unknown media preset: $1" ;;
	esac
}

# media_apparent_bytes MEDIA prints the byte size an empty image built
# for that preset must have. image build reads the length from the
# packed tree's own DISC.bin, so a forced media gives an image of the
# forced target, not of the physical capacity.
media_apparent_bytes() {
	case "$1" in
	dvd+r) echo 4700372992 ;;
	bd25) echo 25025314816 ;;
	bd25-forced-10g) echo 10737418240 ;;
	*) fail "unknown media preset: $1" ;;
	esac
}

# media_small_mb MEDIA prints a fixture size, in MiB, that packs well
# under that preset's capacity. Sized to keep one matrix cell's whole run
# under about 8 minutes.
media_small_mb() {
	case "$1" in
	dvd+r) echo 300 ;;
	bd25) echo 1200 ;;
	bd25-forced-10g) echo 300 ;;
	*) fail "unknown media preset: $1" ;;
	esac
}

# media_over_mb MEDIA prints a fixture size, in MiB, that exceeds that
# preset's usable capacity.
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
# files worth diffing by content, not just by size. It also writes an
# empty directory beside an empty file, and a file of eight zero bytes.
# Those three payloads collide unless the content id covers the object
# kind, so a restore of this tree proves the ids stay apart.
gen_small_tree() {
	local dir="$1"
	mkdir -p "$dir/sub" "$dir/adir"
	echo "content of a, for the disc e2e suite" >"$dir/a.txt"
	echo "content of b, also for the disc e2e suite, a bit longer than a" >"$dir/sub/b.txt"
	head -c 65536 /dev/urandom >"$dir/sub/c.bin"
	: >"$dir/zfile"
	head -c 8 /dev/zero >"$dir/zeros8.bin"
}

# DISC_IMAGE maps the uuid of a disc to the path of its image file.
# image_build adds an entry. disc_insert reads it when restore asks for
# a disc.
declare -A DISC_IMAGE=()

# PACKED_UUID and PACKED_SEQ are set by pack_disc: the uuid and the
# number of the disc that the last pack made.
PACKED_UUID=""
PACKED_SEQ=""

# RESTORE_SWAPS is set by restore_loop: the number of discs that it
# mounted before restore exited 0. RESTORE_OUT is the output of the
# last restore.
RESTORE_SWAPS=0
RESTORE_OUT=""

# pack_disc REPO PACK-OPTION... runs pack and prints its output. It
# fails when pack exits nonzero or packs no disc. It sets PACKED_UUID
# and PACKED_SEQ.
pack_disc() {
	local repo="$1" out
	shift
	out="$("$BIN" --repo="$repo" pack "$@")"
	echo "$out"
	grep -q '^packed disc ' <<<"$out" || fail "pack: no packed-disc line"
	PACKED_SEQ="$(awk '/^packed disc /{print $3}' <<<"$out")"
	PACKED_UUID="$(awk '/^uuid: /{print $2}' <<<"$out")"
	[ -n "$PACKED_UUID" ] || fail "pack: no uuid line"
}

# image_build REPO UUID IMAGE builds the image of the disc UUID with
# image build, moves the image to IMAGE, and adds IMAGE to DISC_IMAGE.
image_build() {
	local repo="$1" uuid="$2" image="$3"
	sudo "$BIN" --repo="$repo" image build "$uuid"
	mv "$repo/staging/plans/$uuid/tree.img" "$image"
	DISC_IMAGE[$uuid]="$image"
}

# mount_ro IMAGE MOUNTPOINT loop-mounts a UDF image read-only. The tool
# counts a read-only mount point outside the repository as a disc.
mount_ro() {
	local image="$1" mnt="$2"
	mkdir -p "$mnt"
	sudo mount -o ro,loop -t udf "$image" "$mnt"
}

# mount_rw IMAGE MOUNTPOINT loop-mounts a UDF image read-write. Use it
# only to write damage into the image. Unmount it and mount it again
# with mount_ro before the tool reads it.
mount_rw() {
	local image="$1" mnt="$2"
	mkdir -p "$mnt"
	sudo mount -o loop -t udf "$image" "$mnt"
}

# disc_insert UUID MOUNTPOINT mounts the image of the disc UUID
# read-only at MOUNTPOINT, in place of the disc that is there.
disc_insert() {
	local uuid="$1" mnt="$2"
	[ -n "${DISC_IMAGE[$uuid]:-}" ] || fail "disc_insert: no image of disc $uuid is known"
	umount_if_mounted "$mnt"
	mount_ro "${DISC_IMAGE[$uuid]}" "$mnt"
	log "disc $uuid inserted at $mnt"
}

# restore_asked_uuid OUTPUT prints the uuid of the disc that a restore
# output asks to insert, or nothing.
restore_asked_uuid() {
	sed -n 's/^restore: insert disc .*(\([0-9a-f-]*\)) into .* and run restore again$/\1/p' <<<"$1"
}

# restore_loop REPO DIR SNAPSHOT [PATH...] DEST runs restore with the
# disc at DIR. When restore asks for another disc, it mounts the image of
# that disc at DIR and runs the same restore again, until restore exits
# 0. It sets RESTORE_SWAPS and RESTORE_OUT.
restore_loop() {
	local repo="$1" dir="$2" out code uuid
	shift 2
	RESTORE_SWAPS=0
	while true; do
		set +e
		out="$("$BIN" --repo="$repo" restore --disc="$dir" "$@" 2>&1 </dev/null)"
		code=$?
		set -e
		echo "$out"
		RESTORE_OUT="$out"
		if [ "$code" -eq 0 ]; then
			return 0
		fi
		uuid="$(restore_asked_uuid "$out")"
		[ -n "$uuid" ] || fail "restore: exit $code, and it asks for no disc"
		RESTORE_SWAPS=$((RESTORE_SWAPS + 1))
		[ "$RESTORE_SWAPS" -le 10 ] || fail "restore: asked for a disc more than 10 times"
		disc_insert "$uuid" "$dir"
	done
}

# restore_expect_missing REPO DIR MISSING_UUID SNAPSHOT [PATH...] DEST
# runs restore as restore_loop does, but it never mounts the disc
# MISSING_UUID. It fails unless restore stops and asks for that disc.
restore_expect_missing() {
	local repo="$1" dir="$2" missing="$3" out code uuid rounds=0
	shift 3
	while true; do
		set +e
		out="$("$BIN" --repo="$repo" restore --disc="$dir" "$@" 2>&1 </dev/null)"
		code=$?
		set -e
		echo "$out"
		if [ "$code" -eq 0 ]; then
			fail "restore without disc $missing exited 0, want 1"
		fi
		uuid="$(restore_asked_uuid "$out")"
		[ -n "$uuid" ] || fail "restore without disc $missing: exit $code, and it asks for no disc"
		if [ "$uuid" = "$missing" ]; then
			log "restore without disc $missing stopped and asked for it, as expected"
			return 0
		fi
		rounds=$((rounds + 1))
		[ "$rounds" -le 10 ] || fail "restore: asked for a disc more than 10 times"
		disc_insert "$uuid" "$dir"
	done
}

# disc_state REPO UUID prints the state field of the status line of the
# disc UUID, or nothing when status shows no such disc.
disc_state() {
	local repo="$1" uuid="$2" out
	out="$("$BIN" --repo="$repo" status)"
	awk -F'  ' -v u="$uuid" '/^disc / && $NF == u { print $2 }' <<<"$out"
}

# assert_disc_state REPO UUID PATTERN fails unless status shows the disc
# UUID in a state that matches the shell pattern PATTERN.
assert_disc_state() {
	local repo="$1" uuid="$2" pattern="$3" state
	state="$(disc_state "$repo" "$uuid")"
	# shellcheck disable=SC2254 # PATTERN is a shell pattern on purpose
	case "$state" in
	$pattern) log "status: disc $uuid: $state" ;;
	*)
		"$BIN" --repo="$repo" status >&2 || true
		fail "status: disc $uuid: state [$state], want [$pattern]"
		;;
	esac
}

# VERIFY_OUT is set by verify_counted: the output of its verify.
VERIFY_OUT=""

# verify_counted REPO MOUNTPOINT UUID [ITEMS] verifies the read-only
# mount MOUNTPOINT with the repository REPO. It fails unless verify
# exits 0, counts the check, and status then shows the disc UUID as
# verified. With ITEMS, the ok line must give ITEMS items. It sets
# VERIFY_OUT.
verify_counted() {
	local repo="$1" mnt="$2" uuid="$3" items="${4:-}" code
	set +e
	VERIFY_OUT="$("$BIN" --repo="$repo" verify "$mnt" 2>&1)"
	code=$?
	set -e
	echo "$VERIFY_OUT"
	[ "$code" -eq 0 ] || fail "verify $mnt: exit $code, want 0"
	grep -qE '^disc [0-9]+ ".*": [0-9]+ items, ok$' <<<"$VERIFY_OUT" ||
		fail "verify $mnt: no ok line"
	if [ -n "$items" ]; then
		grep -qE "^disc [0-9]+ \".*\": $items items, ok\$" <<<"$VERIFY_OUT" ||
			fail "verify $mnt: the ok line does not give $items items"
	fi
	grep -qxE 'burn recorded; verified|verified|already verified; check logged' <<<"$VERIFY_OUT" ||
		fail "verify $mnt: the check is not counted"
	assert_disc_state "$repo" "$uuid" "verified, last check *"
}

# RECOVER_OUT is set by recover_disc: the output of its recover.
RECOVER_OUT=""

# recover_disc REPO SOURCE MOUNTPOINT WANT_EXIT runs recover of the disc
# at the read-only mount MOUNTPOINT into REPO. It fails unless recover
# exits WANT_EXIT. It sets RECOVER_OUT.
recover_disc() {
	local repo="$1" source="$2" mnt="$3" want="$4" code
	set +e
	RECOVER_OUT="$("$BIN" --repo="$repo" recover --source="$source" --disc="$mnt" 2>&1)"
	code=$?
	set -e
	echo "$RECOVER_OUT"
	[ "$code" -eq "$want" ] || fail "recover of $mnt: exit $code, want $want"
}

# umount_if_mounted MOUNTPOINT unmounts it when it is a mountpoint,
# otherwise does nothing.
umount_if_mounted() {
	local mnt="$1"
	if mountpoint -q "$mnt" 2>/dev/null; then
		sudo umount "$mnt"
	fi
}

# cleanup_work_nested_mounts unmounts every mount point under $WORK at
# any depth, deepest first, so a mount a scenario nested below its own
# *mnt directory (for example iso.sh's joliet-mnt, or a mount left
# behind by a killed pipeline) does not survive to block rm -rf.
cleanup_work_nested_mounts() {
	command -v findmnt >/dev/null 2>&1 || return 0
	local mounts
	mounts="$(findmnt -rn -o TARGET 2>/dev/null \
		| awk -v w="$WORK/" 'index($0, w) == 1 { print gsub(/\//, "&") "\t" $0 }' \
		| sort -t "$(printf '\t')" -k1,1rn \
		| cut -f2-)"
	[ -n "$mounts" ] || return 0
	local m
	while IFS= read -r m; do
		umount_if_mounted "$m"
	done <<<"$mounts"
}

cleanup_work() {
	cleanup_work_nested_mounts
	for m in "$WORK"/*mnt; do
		[ -d "$m" ] && umount_if_mounted "$m"
	done
	sudo losetup -D 2>/dev/null || true
	if [ -z "${NOAHSARK_E2E_KEEP_WORKDIR:-}" ]; then
		rm -rf "$WORK"
	fi
}
trap cleanup_work EXIT
