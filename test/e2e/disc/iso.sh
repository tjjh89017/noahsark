#!/usr/bin/env bash
# The iso e2e scenario: sourced by run.sh. Packs a real commit at the
# dvd+r preset, burns the packed tree into a plain ISO 9660 image with
# genisoimage (falling back to xorriso when genisoimage is missing),
# loop-mounts it read-only, and runs the same verify, decoder and
# restore checks the other scenarios run against a UDF mount. It also
# builds a second, Joliet-only ISO to prove the docs/guide.md warning against
# Joliet: Joliet truncates the 68-character object names, so an object
# lookup on that mount must fail. A third image, plain ISO 9660 level 4
# with no Rock Ridge, proves the opposite case: this runner's
# genisoimage folds every fixed name to lowercase under that mode
# (NOAHSARK, README.txt, FORMAT.txt, DISC.bin and so on), and the
# readers still accept it.
#
# The main image in this scenario still uses the docs/guide.md
# "Burn the disc" directory alternative (Rock Ridge, not FORMAT.md's Phase 3
# profile 2): Rock Ridge keeps exact case, so iso_assert_fixed_files can
# compare the mount against the packed tree byte for byte. See lib.sh
# for build_binary and the media_* helpers, and assert.sh for
# assert_dirs_equal and assert_listing_matches.
set -euo pipefail

# ISO_CONTENT_BYTES sizes iso.sh's own fixture: a few hundred MiB of
# mixed-size files, well under dvd+r's real capacity, kept small so this
# cell stays fast.
ISO_CONTENT_BYTES="${NOAHSARK_E2E_ISO_BYTES:-300000000}"
ISO_SEED="${NOAHSARK_E2E_ISO_SEED:-20260914}"

# iso_mkisofs_cmd prints the genisoimage-compatible command this host
# has, and which flavor it is, as "TOOL FLAVOR". genisoimage is
# preferred; xorriso -as mkisofs is the fallback when it is missing.
iso_mkisofs_cmd() {
	if command -v genisoimage >/dev/null 2>&1; then
		echo "genisoimage genisoimage"
	elif command -v xorriso >/dev/null 2>&1; then
		echo "xorriso xorriso"
	else
		fail "iso: neither genisoimage nor xorriso is installed"
	fi
}

# iso_build_image TOOL FLAVOR OUT-ISO TREE-DIR EXTRA-ARGS... builds an
# ISO image from TREE-DIR with the given mkisofs-style arguments, using
# whichever tool iso_mkisofs_cmd picked.
iso_build_image() {
	local tool="$1" flavor="$2" out="$3" tree="$4"
	shift 4
	case "$flavor" in
	genisoimage) "$tool" "$@" -o "$out" "$tree" ;;
	xorriso) "$tool" -as mkisofs "$@" -o "$out" "$tree" ;;
	*) fail "iso: unknown mkisofs flavor: $flavor" ;;
	esac
}

# iso_gen_fixture SRC generates iso.sh's own source tree: the chain
# fixture generator at a small scale (mixed sizes, a directory tree,
# symlinks), plus one explicit empty file, since the generator never
# writes a zero-length file on its own.
iso_gen_fixture() {
	local src="$1"
	run_tool ci-chain-fixture gen "$src" "$ISO_CONTENT_BYTES" "$ISO_SEED" \
		"$src.sample.txt" "$src.full.txt"
	: >"$src/empty.txt"
}

# iso_assert_object_names MOUNT TREE fails unless every object file name
# under MOUNT's NOAHSARK/objects is a 68-character lowercase hex name
# (so plain ISO 9660 level 4 neither truncated nor case-folded it),
# unless every such name still sits under its fan-out directory (the
# first two hex digits of the digest, which starts after the 4-character
# sha2-256 multihash prefix "1220"), and unless the mount and the packed
# tree hold the same count of object files.
#
# find's own output is captured into a plain variable before any awk
# scan of it: an awk pattern that "exit"s after the first match would
# otherwise close a live pipe from find while find still has more lines
# queued to write, and find dies of SIGPIPE under this suite's pipefail,
# aborting the script before the match is ever checked.
iso_assert_object_names() {
	local mnt="$1" tree="$2" listing bad mismatched mnt_count tree_count
	listing="$(find "$mnt/NOAHSARK/objects" -type f -printf '%P\n')"
	bad="$(awk -F/ '$2 !~ /^[0-9a-f]{68}$/ { print; exit }' <<<"$listing")"
	if [ -n "$bad" ]; then
		fail "iso: object name on the mount is not a 68-character lowercase hex name: $bad"
	fi
	mismatched="$(awk -F/ 'substr($2, 5, 2) != $1 { print; exit }' <<<"$listing")"
	if [ -n "$mismatched" ]; then
		fail "iso: object on the mount is not nested under its fan-out directory: $mismatched"
	fi
	mnt_count="$(find "$mnt/NOAHSARK/objects" -type f | wc -l)"
	tree_count="$(find "$tree/NOAHSARK/objects" -type f | wc -l)"
	if [ "$mnt_count" -ne "$tree_count" ]; then
		fail "iso: object count on the mount ($mnt_count) does not match the packed tree ($tree_count)"
	fi
	log "iso: $mnt_count object files, all 68-character lowercase hex names, correctly nested"
}

# iso_assert_fixed_files MOUNT TREE fails unless README.txt, FORMAT.txt
# and REFERENCE/decoder.py exist on MOUNT and are byte-identical to
# TREE's copies.
iso_assert_fixed_files() {
	local mnt="$1" tree="$2" rel
	for rel in README.txt FORMAT.txt REFERENCE/decoder.py; do
		if ! cmp -s "$mnt/NOAHSARK/$rel" "$tree/NOAHSARK/$rel"; then
			fail "iso: NOAHSARK/$rel on the mount does not match the packed tree"
		fi
	done
	log "iso: README.txt, FORMAT.txt and REFERENCE/decoder.py match the packed tree"
}

# iso_plain_level4_check TOOL FLAVOR WORK TREE SRC SNAP builds a third
# image with plain ISO 9660 level 4 and no Rock Ridge: the exact
# genisoimage invocation that folds every fixed name to lowercase (see
# the file header). It mounts that image and checks that verify, the
# decoder listing diff and restore all still succeed against it, and
# prints the folded root directory name it found.
iso_plain_level4_check() {
	local tool="$1" flavor="$2" work="$3" tree="$4" src="$5" snap="$6"
	local plain_iso="$work/plain.iso" plain_mnt="$work/plain-mnt" plain_restored="$work/plain-restored"
	local root_name

	log "iso: $tool -iso-level 4 -V NOAHSARK-PLAIN -o $plain_iso $tree"
	iso_build_image "$tool" "$flavor" "$plain_iso" "$tree" -iso-level 4 -V NOAHSARK-PLAIN
	mkdir -p "$plain_mnt"
	sudo mount -t iso9660 -o loop,ro "$plain_iso" "$plain_mnt"

	root_name="$(find "$plain_mnt" -maxdepth 1 -iname noahsark -printf '%f\n')"
	if [ -z "$root_name" ]; then
		umount_if_mounted "$plain_mnt"
		fail "iso: plain level 4 image: no NOAHSARK root found, case-folded or not"
	fi
	log "iso: plain level 4 image folded the root to: $root_name"

	"$BIN" verify --image="$plain_mnt"
	assert_listing_matches "$plain_mnt" "$work"

	"$BIN" restore "$plain_mnt" "$snap" "$plain_restored"
	assert_dirs_equal "$plain_restored$src" "$src"

	umount_if_mounted "$plain_mnt"
	log "iso: plain level 4 (case-folded) image PASS"
}

# iso_joliet_negative_check TOOL FLAVOR WORK TREE builds a second,
# Joliet-only ISO (no Rock Ridge, no -iso-level 4), mounts it, and
# checks that the documented Joliet pitfall is real: an object name is
# truncated, or verify fails against that mount.
iso_joliet_negative_check() {
	local tool="$1" flavor="$2" work="$3" tree="$4"
	local joliet_iso="$work/joliet.iso" joliet_mnt="$work/joliet-mnt"
	iso_build_image "$tool" "$flavor" "$joliet_iso" "$tree" -J -V NOAHSARK-JOLIET
	mkdir -p "$joliet_mnt"
	sudo mount -t iso9660 -o loop,ro "$joliet_iso" "$joliet_mnt"

	# Disable errexit for the whole checked block: a failure in any of
	# these commands must still reach the umount_if_mounted call below,
	# the same way chain_pack_one guards its own verify call.
	set +e
	# find's output is captured before the awk scan, not piped straight
	# into it: an "exit"-on-match awk would otherwise close the pipe
	# while find still has lines queued, and find dies of SIGPIPE.
	local listing truncated
	listing="$(find "$joliet_mnt/NOAHSARK/objects" -type f -printf '%f\n' 2>/dev/null)"
	truncated="$(awk 'length($0) != 68 { print; exit }' <<<"$listing")"

	local verify_code=0
	"$BIN" verify --image="$joliet_mnt" >"$work/joliet-verify.log" 2>&1
	verify_code=$?
	set -e

	umount_if_mounted "$joliet_mnt"

	if [ -n "$truncated" ]; then
		log "iso: Joliet truncated an object name as expected, first: $truncated"
	elif [ "$verify_code" -ne 0 ]; then
		log "iso: Joliet-only image failed verify as expected"
		cat "$work/joliet-verify.log"
	else
		fail "iso: Joliet-only image neither truncated an object name nor failed verify"
	fi
}

scenario_iso() {
	local work="$WORK/iso"
	local repo src tree image mnt commit_out snap restored
	local tool flavor
	repo="$work/repo"
	src="$work/src"
	tree="$work/tree"
	image="$work/disc.iso"
	mnt="$work/mnt"
	restored="$work/restored"

	build_binary
	read -r tool flavor <<<"$(iso_mkisofs_cmd)"
	log "iso: using $flavor to build ISO images"

	local t0 t1
	t0=$(date +%s)
	iso_gen_fixture "$src"
	t1=$(date +%s)
	log "iso: fixture generation took $((t1 - t0))s"
	df -h

	"$BIN" init --repo="$repo"
	commit_out="$("$BIN" commit --repo="$repo" "$src")"
	echo "$commit_out"
	snap="$(awk '/^snapshot /{print $2}' <<<"$commit_out")"

	t0=$(date +%s)
	# shellcheck disable=SC2046 # media_capacity_flags is a list of flags
	"$BIN" pack --repo="$repo" $(media_capacity_flags "$FIXED_MEDIA") --out="$tree"
	t1=$(date +%s)
	log "iso: pack took $((t1 - t0))s"
	df -h

	t0=$(date +%s)
	log "iso: $tool -R -iso-level 4 -V NOAHSARK-TEST -o $image $tree"
	iso_build_image "$tool" "$flavor" "$image" "$tree" -R -iso-level 4 -V NOAHSARK-TEST
	t1=$(date +%s)
	log "iso: ISO build took $((t1 - t0))s"
	df -h

	mkdir -p "$mnt"
	sudo mount -t iso9660 -o loop,ro "$image" "$mnt"

	iso_assert_object_names "$mnt" "$tree"
	iso_assert_fixed_files "$mnt" "$tree"

	"$BIN" verify --image="$mnt"
	assert_listing_matches "$mnt" "$work"

	t0=$(date +%s)
	"$BIN" restore "$mnt" "$snap" "$restored"
	t1=$(date +%s)
	log "iso: restore took $((t1 - t0))s"
	assert_dirs_equal "$restored$src" "$src"

	umount_if_mounted "$mnt"

	iso_joliet_negative_check "$tool" "$flavor" "$work" "$tree"

	iso_plain_level4_check "$tool" "$flavor" "$work" "$tree" "$src" "$snap"

	df -h
	log "iso PASS"
}
