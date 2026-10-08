#!/usr/bin/env bash
# The damage e2e scenario: sourced by run.sh. It packs one small disc
# with real content and keeps its image as the second copy. For each
# seed it copies the image, flips random bytes of the files below
# NOAHSARK/ in the copy, and checks three things. verify finds the
# damage and names a damaged file. restore from the damaged copy writes
# no wrong data, and exits nonzero when a file is not restored. restore
# from the second copy into the same destination then completes the
# tree and exits 0. See run.sh for the shared scenario dispatch and
# lib.sh for the pack, image, mount, verify and restore helpers.
set -euo pipefail

# DAMAGE_KNOWN_SEEDS run on each run, after the fresh seed.
DAMAGE_KNOWN_SEEDS=(1 2 3 5 8 13 21 34 20261002 4294967295)

# DAMAGE_MAX_FLIPS is the largest number of bytes that one seed flips.
DAMAGE_MAX_FLIPS="${NOAHSARK_E2E_DAMAGE_MAX_FLIPS:-4}"

# DAMAGE_CONTENT_BYTES sizes the source tree, and DAMAGE_CAPACITY the
# disc: small, so that each seed takes seconds.
DAMAGE_CONTENT_BYTES="${NOAHSARK_E2E_DAMAGE_CONTENT_BYTES:-12000000}"
DAMAGE_CAPACITY="--capacity=40000KiB"

# DAMAGE_RNG is the state of the random number generator of one seed.
# damage_rng_next sets DAMAGE_RNG_OUT.
DAMAGE_RNG=0
DAMAGE_RNG_OUT=0

# damage_rng_seed SEED starts the generator at SEED.
damage_rng_seed() {
	DAMAGE_RNG=$(($1 ^ 0x5DEECE66D))
	damage_rng_next 1
}

# damage_rng_next N sets DAMAGE_RNG_OUT to a number from 0 to N-1. The
# generator is a 64-bit linear congruential generator. It takes the high
# 31 bits of the state.
damage_rng_next() {
	DAMAGE_RNG=$((DAMAGE_RNG * 6364136223846793005 + 1442695040888963407))
	DAMAGE_RNG_OUT=$((((DAMAGE_RNG >> 33) & 0x7fffffff) % $1))
}

# damage_targets ROOT prints the files below ROOT/NOAHSARK that the
# damage can hit, one group for each kind of file, one line for each
# group: the kind, then the paths of its files, separated by tabs. A
# seed takes a group first, so that each table has a fair chance. The
# objects group comes four times: it holds the chunks that restore
# reads.
damage_targets() {
	local base="$1/NOAHSARK" kind
	for kind in objects objects objects objects snapshots INDEX.bin REFS.bin DISCS.bin DISC.bin RUN.bin RUN2.bin README.txt FORMAT.txt; do
		local files
		case "$kind" in
		objects | snapshots) files="$(find "$base/$kind" -type f | LC_ALL=C sort)" ;;
		*) files="$(find "$base" -type f -name "$kind" | LC_ALL=C sort)" ;;
		esac
		[ -n "$files" ] || fail "damage: the disc root has no $kind"
		printf '%s' "$kind"
		while IFS= read -r f; do
			printf '\t%s' "$f"
		done <<<"$files"
		printf '\n'
	done
}

# DAMAGE_HIT is set by damage_write: one line for each flipped byte,
# as "PATH OFFSET".
DAMAGE_HIT=""

# damage_write SEED ROOT flips 1 to DAMAGE_MAX_FLIPS distinct bytes of
# the files below ROOT/NOAHSARK, chosen from SEED. It sets DAMAGE_HIT.
damage_write() {
	local seed="$1" root="$2" groups count i
	groups="$(damage_targets "$root")"
	local -a group_lines
	mapfile -t group_lines <<<"$groups"
	damage_rng_seed "$seed"
	damage_rng_next "$DAMAGE_MAX_FLIPS"
	count=$((DAMAGE_RNG_OUT + 1))
	DAMAGE_HIT=""
	i=0
	while [ "$i" -lt "$count" ]; do
		damage_rng_next "${#group_lines[@]}"
		local -a fields
		IFS=$'\t' read -ra fields <<<"${group_lines[$DAMAGE_RNG_OUT]}"
		damage_rng_next "$((${#fields[@]} - 1))"
		local path="${fields[$((DAMAGE_RNG_OUT + 1))]}" size off
		size="$(stat --format='%s' "$path")"
		require_number "damage: size of $path" "$size"
		[ "$size" -gt 0 ] || continue
		damage_rng_next "$size"
		off="$DAMAGE_RNG_OUT"
		# A second flip of the same byte would undo the first.
		if grep -qxF "$path $off" <<<"$DAMAGE_HIT"; then
			continue
		fi
		run_tool ci-corrupt "$path" "$off"
		DAMAGE_HIT+="$path $off"$'\n'
		i=$((i + 1))
	done
}

# damage_assert_written SRC DEST fails unless each regular file below
# DEST is a complete file of the same path below SRC with the same
# bytes. A part file is not a written file. It prints the number of
# files that SRC holds and DEST does not hold complete.
damage_assert_written() {
	local src="$1" dest="$2" files rel missing=0
	files="$(cd "$dest" && find . -type f ! -name '.*.noahsark-part' | LC_ALL=C sort)"
	while IFS= read -r rel; do
		[ -n "$rel" ] || continue
		[ -f "$src/$rel" ] || fail "damage: restore wrote $rel, which the source does not hold"
		cmp -s "$src/$rel" "$dest/$rel" || fail "damage: restore wrote wrong data to $rel"
	done <<<"$files"
	files="$(cd "$src" && find . -type f | LC_ALL=C sort)"
	while IFS= read -r rel; do
		[ -f "$dest/$rel" ] || missing=$((missing + 1))
	done <<<"$files"
	echo "$missing"
}

# damage_one_seed WORK REPO SRC SNAP UUID IMAGE SEED runs the checks of
# one seed against a damaged copy of IMAGE, then restores from IMAGE.
damage_one_seed() {
	local work="$1" repo="$2" src="$3" snap="$4" uuid="$5" image="$6" seed="$7"
	local dir="$work/seed-$seed" code out
	local copy="$dir/damaged.img" mnt="$dir/mnt" dest="$dir/restored"
	mkdir -p "$dir"
	cp --sparse=always "$image" "$copy"

	mount_rw "$copy" "$mnt"
	damage_write "$seed" "$mnt"
	umount_if_mounted "$mnt"
	log "damage: seed $seed flipped $(grep -c . <<<"$DAMAGE_HIT") byte(s):"
	echo "$DAMAGE_HIT"

	mount_ro "$copy" "$mnt"
	set +e
	out="$("$BIN" --repo="$repo" verify "$mnt" 2>&1)"
	code=$?
	set -e
	echo "$out"
	[ "$code" -ne 0 ] || fail "damage: seed $seed: verify of the damaged copy exited 0"
	grep -qE '^disc [0-9]+ ".*": bad; |: cannot read the disc: ' <<<"$out" ||
		fail "damage: seed $seed: verify did not report the disc bad"
	local named=0 path
	while read -r path _; do
		[ -n "$path" ] || continue
		if grep -qF "$(basename "$path")" <<<"$out"; then
			named=1
		fi
	done <<<"$DAMAGE_HIT"
	[ "$named" -eq 1 ] || fail "damage: seed $seed: verify named no damaged file"

	# The staging store holds every chunk. Move it aside, thus restore
	# reads every chunk from the damaged copy.
	staging_aside "$repo"
	set +e
	out="$("$BIN" --repo="$repo" restore --disc="$mnt" "$snap" "$dest" 2>&1 </dev/null)"
	code=$?
	set -e
	echo "$out"
	local missing
	missing="$(damage_assert_written "$src" "$dest")"
	require_number "damage: seed $seed: files not restored" "$missing"
	if [ "$missing" -gt 0 ] && [ "$code" -eq 0 ]; then
		fail "damage: seed $seed: restore from the damaged copy left $missing file(s) not restored, and exited 0"
	fi
	if [ "$code" -eq 0 ]; then
		assert_dirs_equal "$dest" "$src"
	fi
	log "damage: seed $seed: restore from the damaged copy exited $code; $missing file(s) not restored; no wrong data"
	umount_if_mounted "$mnt"

	# The second copy: the same disc uuid, no damage.
	mount_ro "$image" "$mnt"
	set +e
	out="$("$BIN" --repo="$repo" restore --disc="$mnt" "$snap" "$dest" 2>&1 </dev/null)"
	code=$?
	set -e
	echo "$out"
	staging_back "$repo"
	[ "$code" -eq 0 ] || fail "damage: seed $seed: restore from the second copy exited $code, want 0"
	assert_dirs_equal "$dest" "$src"
	verify_counted "$repo" "$mnt" "$uuid"
	umount_if_mounted "$mnt"
	rm -rf "$dir"
	log "damage: seed $seed PASS"
}

scenario_damage() {
	local work="$WORK/damage"
	local repo="$work/repo" src="$work/src" image="$work/disc0.img" mnt="$work/mnt"
	local out snap uuid
	build_binary
	mkdir -p "$work"

	run_tool ci-incremental-fixture gen "$src/data" "$DAMAGE_CONTENT_BYTES" 20261002 "$work/hashes" "$work/plan"
	gen_small_tree "$src/small"

	(mkdir -p "$repo" && cd "$repo" && "$BIN" init)
	out="$("$BIN" --repo="$repo" commit "$src")"
	echo "$out"
	snap="$(awk '/^snapshot /{print $2}' <<<"$out")"
	pack_disc "$repo" "$DAMAGE_CAPACITY"
	uuid="$PACKED_UUID"
	image_build "$repo" "$uuid" "$image"
	mount_ro "$image" "$mnt"
	verify_counted "$repo" "$mnt" "$uuid"
	umount_if_mounted "$mnt"

	local fresh
	if [ -n "${NOAHSARK_E2E_DAMAGE_SEED:-}" ]; then
		fresh="$NOAHSARK_E2E_DAMAGE_SEED"
		log "damage: seed $fresh from NOAHSARK_E2E_DAMAGE_SEED"
	else
		fresh="$(od -An -N4 -tu4 /dev/urandom | tr -d ' ')"
		log "damage: fresh seed $fresh; NOAHSARK_E2E_DAMAGE_SEED=$fresh runs it again"
	fi
	require_number "damage: seed" "$fresh"

	local seed
	for seed in "$fresh" "${DAMAGE_KNOWN_SEEDS[@]}"; do
		damage_one_seed "$work" "$repo" "$src" "$snap" "$uuid" "$image" "$seed"
	done
	log "damage PASS"
}
