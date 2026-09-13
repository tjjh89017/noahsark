#!/usr/bin/env bash
# Loop-mounts a UDF image mkudffs built empty, copies a packed NOAHSARK
# tree into it, and hands ownership to the calling user. Needs root for
# the loop mount and the copy, so it runs under sudo; the chown lets
# later steps read and write the mount as the runner user. Shared by
# every CI step that needs a populated, writable mount, so the mount,
# copy and ownership rules are written once.
#
# Usage: mount-populate.sh IMAGE TREE_DIR MOUNTPOINT
set -euo pipefail

IMAGE="$1"
TREE_DIR="$2"
MNT="$3"

mkdir -p "$MNT"
sudo mount -o loop -t udf "$IMAGE" "$MNT"
sudo cp -a "$TREE_DIR"/NOAHSARK "$MNT"/
sudo chown -R "$(id -u):$(id -g)" "$MNT"/NOAHSARK
sync
