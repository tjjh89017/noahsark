#!/usr/bin/env bash
# Unmounts MOUNTPOINT when it is a mountpoint, otherwise does nothing.
# Shared by every CI step that cleans up a mount this action made.
#
# Usage: umount-if-mounted.sh MOUNTPOINT
set -euo pipefail

MNT="$1"
if mountpoint -q "$MNT"; then
  sudo umount "$MNT"
fi
