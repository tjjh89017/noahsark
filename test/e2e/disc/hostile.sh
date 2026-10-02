#!/usr/bin/env bash
# The hostile e2e scenario: sourced by run.sh. It commits a source tree
# with odd names, a deep path, many small files, symlinks, hard links,
# odd modes and owners, files at the chunk size limits, extreme
# modification times, special files and one file larger than a disc. It
# packs the tree across discs, builds and verifies each image, deletes
# the repository, recovers it from the discs, restores, and compares the
# result with the source: the content, the symlink targets, the modes,
# the owners and the modification times. See run.sh for the shared
# scenario dispatch and lib.sh for the pack, image, mount, recover and
# restore helpers.
set -euo pipefail

# HOSTILE_CAPACITY is the capacity of each disc. HOSTILE_BIG_BYTES is
# the size of the file that is larger than one disc.
HOSTILE_CAPACITY="--capacity=40000KiB"
HOSTILE_BIG_BYTES=$((48 * 1024 * 1024))

# hostile_bytes PATH BYTES IV writes BYTES incompressible bytes to PATH.
# Another IV gives other bytes, thus the files share no chunk.
hostile_bytes() {
	local path="$1" bytes="$2" iv="$3"
	set +o pipefail
	openssl enc -aes-256-ctr -K "$FIXTURE_KEY" -iv "$iv" -in /dev/zero 2>/dev/null \
		| head -c "$bytes" >"$path"
	set -o pipefail
}

# hostile_tree DIR writes the hostile source tree at DIR. It sets the
# modification times last, deepest first, because a new entry changes
# the time of its directory.
hostile_tree() {
	local src="$1" d i
	mkdir -p "$src"

	d="$src/names"
	mkdir -p "$d"
	printf 'space\n' >"$d/with space.txt"
	printf 'dash\n' >"$d/-leading-dash"
	printf 'newline\n' >"$d/new"$'\n'"line"
	printf 'tab\n' >"$d/tab"$'\t'"name"
	printf 'not utf-8\n' >"$d/bad-"$'\xff\xfe'"-utf8"
	printf 'long\n' >"$d/$(printf 'n%.0s' $(seq 1 255))"
	printf 'cjk\n' >"$d/漢字のファイル.txt"
	printf 'emoji\n' >"$d/😀-emoji-🎉.txt"
	# The same visible name, in NFC and in NFD: two names.
	printf 'nfc\n' >"$d/caf"$'\xc3\xa9'".txt"
	printf 'nfd\n' >"$d/cafe"$'\xcc\x81'".txt"
	printf 'nfc\n' >"$d/"$'\xea\xb0\x80'"-hangul-nfc"
	printf 'nfd\n' >"$d/"$'\xe1\x84\x80\xe1\x85\xa1'"-hangul-nfd"

	# A path near PATH_MAX: 19 directories of 200 bytes and a file of
	# 100 bytes, about 3900 bytes below the source root.
	d="$src/deep"
	local seg
	seg="$(printf 'd%.0s' $(seq 1 200))"
	for i in $(seq 1 19); do
		d="$d/$seg"
	done
	mkdir -p "$d"
	printf 'deep\n' >"$d/$(printf 'f%.0s' $(seq 1 100))"

	mkdir -p "$src/empty-dir"
	: >"$src/empty-file"

	d="$src/many"
	mkdir -p "$d"
	for i in $(seq 0 9999); do
		printf 'small file %d\n' "$i" >"$d/f$i"
	done

	d="$src/links"
	mkdir -p "$d/target-dir"
	printf 'target\n' >"$d/target-file"
	ln -s target-file "$d/to-file"
	ln -s target-dir "$d/to-dir"
	ln -s does-not-exist "$d/dangling"
	ln -s /nonexistent/absolute/target "$d/absolute"
	ln -s "../names/with space.txt" "$d/dot-dot"
	ln "$d/target-file" "$d/hard-link"

	d="$src/modes"
	mkdir -p "$d/read-only-dir" "$d/sticky-dir"
	printf 'in a read-only directory\n' >"$d/read-only-dir/inside"
	printf 'read-only\n' >"$d/read-only"
	printf 'setuid\n' >"$d/setuid"
	printf 'setgid\n' >"$d/setgid"
	printf 'no mode bits\n' >"$d/mode-0000"
	printf 'owner\n' >"$d/other-owner"
	chmod 0444 "$d/read-only"
	chmod 4755 "$d/setuid"
	chmod 2755 "$d/setgid"
	chmod 0000 "$d/mode-0000"
	chmod 1777 "$d/sticky-dir"
	chown 1234:5678 "$d/other-owner"

	d="$src/chunk-sizes"
	mkdir -p "$d"
	local size n=0
	for size in 1048576 4194304 16777216; do
		for i in -1 0 1; do
			n=$((n + 1))
			hostile_bytes "$d/size-$((size + i))" "$((size + i))" "$(printf '%032x' "$n")"
		done
	done
	head -c 16777216 /dev/zero >"$d/zeros-16MiB"

	hostile_bytes "$src/larger-than-a-disc" "$HOSTILE_BIG_BYTES" "$(printf '%032x' 99)"

	d="$src/special"
	mkdir -p "$d"
	mkfifo "$d/fifo"
	mknod "$d/char-device" c 1 3
	python3 -c 'import socket, sys; socket.socket(socket.AF_UNIX).bind(sys.argv[1])' "$d/socket"

	touch -d '1960-06-01 12:00:00.123456789 UTC' "$src/names/with space.txt"
	touch -d '2100-01-01 00:00:00.5 UTC' "$src/names/-leading-dash"
	touch -d '1969-12-31 23:59:59 UTC' "$src/empty-file"
	touch -h -d '1950-01-01 00:00:00 UTC' "$src/links/to-file"
	touch -d '2200-02-02 02:02:02 UTC' "$src/empty-dir"
	touch -d '1965-05-05 05:05:05 UTC' "$src/names"
	chmod 0555 "$src/modes/read-only-dir"
}

scenario_hostile() {
	local work="$WORK/hostile"
	local repo="$work/repo" src="$work/src" mnt="$work/mnt" rmnt="$work/rmnt"
	local out snap
	build_binary
	mkdir -p "$work"

	local t0 t1
	t0=$(date +%s)
	hostile_tree "$src"
	t1=$(date +%s)
	log "hostile: the source tree took $((t1 - t0))s, $(du -sh "$src" | cut -f1)"

	(mkdir -p "$repo" && cd "$repo" && "$BIN" init)
	out="$("$BIN" --repo="$repo" commit "$src")"
	echo "$out" | tail -20
	snap="$(awk '/^snapshot /{print $2}' <<<"$out")"
	grep -qx 'unstable: 0, skipped: 0' <<<"$out" || fail "hostile: commit skipped a path"
	grep -q '^special files: 3; ' <<<"$out" || fail "hostile: commit did not name the 3 special files"

	# Pack until nothing is staged. Each disc gets an image, a read-only
	# mount and a counted verify.
	local -a uuids=()
	local n=0
	while true; do
		pack_disc "$repo" "$HOSTILE_CAPACITY"
		uuids+=("$PACKED_UUID")
		image_build "$repo" "$PACKED_UUID" "$work/disc$n.img"
		mount_ro "$work/disc$n.img" "$mnt"
		verify_counted "$repo" "$mnt" "$PACKED_UUID"
		umount_if_mounted "$mnt"
		n=$((n + 1))
		[ "$n" -le 10 ] || fail "hostile: more than 10 discs"
		out="$("$BIN" --repo="$repo" status)"
		if grep -q '^staged: 0 items, ' <<<"$out"; then
			break
		fi
	done
	[ "${#uuids[@]}" -ge 2 ] || fail "hostile: the tree fits one disc; the large file must span discs"
	log "hostile: packed ${#uuids[@]} discs"

	# The file larger than a disc needs more than one disc.
	out="$("$BIN" --repo="$repo" restore --dry-run --disc="$rmnt" "$snap" larger-than-a-disc "$work/dry")"
	echo "$out"
	local big_discs
	big_discs="$(grep -c '^disc ' <<<"$out")"
	[ "$big_discs" -ge 2 ] || fail "hostile: the file larger than a disc is on $big_discs disc(s)"

	rm -rf "$repo"
	log "hostile: deleted the repository"
	local i
	for i in "${!uuids[@]}"; do
		mount_ro "$work/disc$i.img" "$mnt"
		recover_disc "$repo" "$src" "$mnt" 0
		umount_if_mounted "$mnt"
	done

	local restored="$work/restored"
	restore_loop "$repo" "$rmnt" "$snap" "$restored"
	umount_if_mounted "$rmnt"
	grep -qF 'not restored: 3 unsupported entry(ies)' <<<"$RESTORE_OUT" ||
		fail "hostile: restore did not report the 3 special files as unsupported entries"
	run_tool ci-tree-compare "$src" "$restored"
	log "hostile PASS"
}
