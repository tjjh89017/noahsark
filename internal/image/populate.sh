#!/usr/bin/env bash
# Populates a UDF image mkudffs already created with the NOAHSARK tree a
# Go image.Build call laid out as ordinary files. mkudffs makes only an
# empty filesystem; this script loop-mounts it, copies the tree in, and
# unmounts. It needs root for the loop mount, so the caller runs it
# through sudo, and only under CI.
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

# noadinicb stops the kernel udf driver from embedding a small file's data
# inside its File Entry block, so every file keeps its own sector-aligned
# data extent, matching the sector-boundary rule every file the format
# writes must follow.
mount -o loop,noadinicb -t udf "$IMAGE" "$MNT"
cp -a "$TREE_DIR"/NOAHSARK "$MNT"/
sync
