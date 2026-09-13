#!/usr/bin/env bash
# Populates a UDF image mkudffs already created with the NOAHSARK tree a
# Go image.Build call laid out as ordinary files. mkudffs makes only an
# empty filesystem; this script loop-mounts it, copies the tree in file
# by file, and unmounts. It needs root for the loop mount, so the caller
# runs it through sudo, and only under CI.
#
# Every regular file copied prints "COPIED <bytes>" to stdout, so the
# caller can report populate progress as each file lands, instead of
# waiting for one opaque cp to finish.
#
# Usage: populate.sh IMAGE TREE_DIR
set -euo pipefail

IMAGE="$1"
TREE_DIR="$2"
MNT="$(mktemp -d)"

cleanup() {
  umount "$MNT" 2>/dev/null || true
  rmdir "$MNT" 2>/dev/null || true
}
trap cleanup EXIT

mount -o loop -t udf "$IMAGE" "$MNT"

SRC="$TREE_DIR/NOAHSARK"
DEST="$MNT/NOAHSARK"

# Directories first, so every file's and symlink's parent already exists.
find "$SRC" -type d | while IFS= read -r d; do
  mkdir -p "$DEST${d#"$SRC"}"
done

# Regular files, one cp per file, each followed by a progress line.
find "$SRC" -type f | while IFS= read -r f; do
  cp -p "$f" "$DEST${f#"$SRC"}"
  echo "COPIED $(stat -c%s "$f")"
done

# Symlinks, recreated verbatim.
find "$SRC" -type l | while IFS= read -r l; do
  cp -P "$l" "$DEST${l#"$SRC"}"
done

sync
