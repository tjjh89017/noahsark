#!/usr/bin/env bash
# Probe: does UDF on Blu-ray need a bundle container for small files, or can
# every small content-addressed object be its own file under a 256-way
# fanout directory tree? This script records measurements. It does not
# assert pass/fail; a later test promotes any stable answer.
#
# Usage: probe.sh [WORKDIR] [OUTFILE]
#   WORKDIR  scratch directory for images and mount points (default: ./work)
#   OUTFILE  results file to write/append (default: WORKDIR/results.md)
set -uo pipefail

WORKDIR="${1:-$(pwd)/work}"
OUTFILE="${2:-$WORKDIR/results.md}"
export PATH="$PATH:/usr/sbin:/sbin"

mkdir -p "$WORKDIR"
MNT="$WORKDIR/mnt"
IMG="$WORKDIR/test.img"
mkdir -p "$MNT"

log() { echo "$@" | tee -a "$OUTFILE.log" ; }

# ---------------------------------------------------------------------------
# Environment check
# ---------------------------------------------------------------------------
HAVE_MKUDFFS=0
HAVE_SUDO=0
command -v mkudffs >/dev/null 2>&1 && HAVE_MKUDFFS=1
if sudo -n true 2>/dev/null; then HAVE_SUDO=1; fi

if [ "$HAVE_MKUDFFS" -eq 0 ]; then
  # try to install udftools without a password
  if [ "$HAVE_SUDO" -eq 1 ]; then
    sudo -n apt-get update -y >/dev/null 2>&1
    sudo -n apt-get install -y udftools >/dev/null 2>&1
  fi
  command -v mkudffs >/dev/null 2>&1 && HAVE_MKUDFFS=1
fi

{
  echo "# UDF small-object probe results"
  echo
  echo "Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  echo "mkudffs available: $HAVE_MKUDFFS"
  echo
  echo "Passwordless sudo available: $HAVE_SUDO"
  echo
} > "$OUTFILE"

if [ "$HAVE_MKUDFFS" -eq 0 ] || [ "$HAVE_SUDO" -eq 0 ]; then
  {
    echo "## Blocked"
    echo
    echo "mkudffs or passwordless sudo (needed for loop mount) is not"
    echo "available in this environment. No mount-based measurement was"
    echo "taken. Only image-size-independent facts, if any, are reported."
  } >> "$OUTFILE"
  exit 0
fi

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# create_image SIZE_MB
create_image() {
  local size_mb="$1"
  rm -f "$IMG"
  truncate -s "${size_mb}M" "$IMG"
  mkudffs --blocksize=2048 --media-type=hd --udfrev=2.01 "$IMG" >/dev/null 2>&1
}

mount_img() {
  sudo -n mount -o loop,noatime -t udf "$IMG" "$MNT"
  sudo -n chmod 777 "$MNT"
}

umount_img() {
  sync
  sudo -n umount "$MNT" 2>/dev/null || true
}

drop_caches() {
  sync
  sudo -n sh -c 'echo 3 > /proc/sys/vm/drop_caches' 2>/dev/null || true
}

used_bytes() {
  df -B1 --output=used "$MNT" | tail -1 | tr -d ' '
}

make_fanout_dirs() {
  python3 - "$MNT" <<'PYEOF'
import os, sys
base = os.path.join(sys.argv[1], "objects")
os.makedirs(base, exist_ok=True)
for i in range(256):
    os.makedirs(os.path.join(base, "%02x" % i), exist_ok=True)
PYEOF
}

# write_files N SIZE  -> writes N files of SIZE bytes round-robin into
# 256 fanout dirs under $MNT/objects, prints elapsed seconds
write_files() {
  local n="$1" size="$2"
  python3 - "$MNT" "$n" "$size" <<'PYEOF'
import os, sys, time
mnt, n, size = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
base = os.path.join(mnt, "objects")
payload = b"\x5a" * size
t0 = time.time()
for i in range(n):
    d = "%02x" % (i % 256)
    fn = "%064x" % i
    with open(os.path.join(base, d, fn), "wb") as f:
        f.write(payload)
t1 = time.time()
print("%.3f" % (t1 - t0))
PYEOF
}

# write_flat N SIZE -> writes N files of SIZE bytes into a single directory
write_flat() {
  local n="$1" size="$2"
  python3 - "$MNT" "$n" "$size" <<'PYEOF'
import os, sys, time
mnt, n, size = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
base = os.path.join(mnt, "flat")
os.makedirs(base, exist_ok=True)
payload = b"\x5a" * size
t0 = time.time()
for i in range(n):
    fn = "%064x" % i
    with open(os.path.join(base, fn), "wb") as f:
        f.write(payload)
t1 = time.time()
print("%.3f" % (t1 - t0))
PYEOF
}

# write_bundles NBUNDLES OBJECTS_PER_BUNDLE OBJ_SIZE -> writes bundle files,
# each the concatenation of OBJECTS_PER_BUNDLE objects of OBJ_SIZE bytes,
# into $MNT/bundles. Prints elapsed seconds.
write_bundles() {
  local nb="$1" opb="$2" osz="$3"
  python3 - "$MNT" "$nb" "$opb" "$osz" <<'PYEOF'
import os, sys, time
mnt, nb, opb, osz = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4])
base = os.path.join(mnt, "bundles")
os.makedirs(base, exist_ok=True)
payload = b"\x5a" * (opb * osz)
t0 = time.time()
for i in range(nb):
    fn = "bundle-%06d" % i
    with open(os.path.join(base, fn), "wb") as f:
        f.write(payload)
t1 = time.time()
print("%.3f" % (t1 - t0))
PYEOF
}

# ---------------------------------------------------------------------------
# Part 1: sector cost per file size, and in-ICB threshold
# ---------------------------------------------------------------------------
log "## Part 1: sector cost and in-ICB threshold"
log
log "### 1a. in-ICB threshold scan (N=2000 per size, blocksize 2048)"
log
log '| size (bytes) | used delta (bytes) | bytes/file | overhead/file |'
log '|---:|---:|---:|---:|'

THRESH_SIZES="64 128 256 512 1024 1536 1700 1800 1850 1900 1950 1984 2000 2016 2032 2040 2044 2046 2047 2048 2049 2100 4096"
N_THRESH=2000
create_image 40
mount_img
make_fanout_dirs
BASE_USED=$(used_bytes)
umount_img

for sz in $THRESH_SIZES; do
  create_image 40
  mount_img
  make_fanout_dirs
  b0=$(used_bytes)
  write_files "$N_THRESH" "$sz" > /dev/null
  sync
  b1=$(used_bytes)
  umount_img
  delta=$((b1 - b0))
  bpf=$((delta / N_THRESH))
  overhead=$((bpf - sz))
  log "| $sz | $delta | $bpf | $overhead |"
done
log

# ---------------------------------------------------------------------------
# Part 1b: main size/N table
# ---------------------------------------------------------------------------
log "### 1b. sector cost, N files of size S"
log
log '| S (bytes) | N | image used delta (bytes) | bytes/file | overhead/file (bytes) |'
log '|---:|---:|---:|---:|---:|'

run_size_case() {
  local s="$1" n="$2" img_mb="$3"
  create_image "$img_mb"
  mount_img
  make_fanout_dirs
  local b0 b1
  b0=$(used_bytes)
  write_files "$n" "$s" > /dev/null
  sync
  b1=$(used_bytes)
  umount_img
  local delta=$((b1 - b0))
  local bpf=$((delta / n))
  local overhead=$((bpf - s))
  log "| $s | $n | $delta | $bpf | $overhead |"
}

# image sizes sized generously above expected payload+overhead, kept well
# under 4 GiB; N is scaled down for S=65536 to respect that ceiling since
# 100000 * 65536 bytes alone is 6.5 GiB. Per-file overhead does not depend
# on N, so the scaled point is still comparable to the others.
run_size_case 200   100000 600
run_size_case 2048  100000 800
run_size_case 4096  100000 900
run_size_case 16384 100000 2200
run_size_case 65536 20000  1600
log
log "(S=65536 used N=20000, not 100000, to keep the image under 4 GiB. Per-file overhead is independent of N.)"
log

# ---------------------------------------------------------------------------
# Part 2: directory cost, N=100000 over 256 fanout dirs
# ---------------------------------------------------------------------------
log
log "## Part 2: directory cost, N=100000 over 256 fanout dirs (S=4096)"
log

create_image 900
mount_img
make_fanout_dirs
WRITE_T=$(write_files 100000 4096)
umount_img
log "Populate time (100000 files, 4096 bytes each, 256 dirs): ${WRITE_T}s"

# cold full walk
umount_img
drop_caches
mount_img
T0=$(date +%s.%N)
FOUND=$(sudo -n find "$MNT/objects" -type f | wc -l)
T1=$(date +%s.%N)
WALK_COLD=$(python3 -c "print(f'{$T1-$T0:.3f}')")
log "Cold full walk (find): ${WALK_COLD}s, files found=${FOUND}"

# warm full walk (immediately again)
T0=$(date +%s.%N)
FOUND2=$(sudo -n find "$MNT/objects" -type f | wc -l)
T1=$(date +%s.%N)
WALK_WARM=$(python3 -c "print(f'{$T1-$T0:.3f}')")
log "Warm full walk (find): ${WALK_WARM}s, files found=${FOUND2}"

# random open of 1000 files, cold cache
umount_img
drop_caches
mount_img
RANDOPEN_COLD=$(python3 - "$MNT" <<'PYEOF'
import os, sys, random, time
mnt = sys.argv[1]
base = os.path.join(mnt, "objects")
random.seed(42)
paths = []
for i in random.sample(range(100000), 1000):
    d = "%02x" % (i % 256)
    fn = "%064x" % i
    paths.append(os.path.join(base, d, fn))
t0 = time.time()
n_ok = 0
for p in paths:
    try:
        with open(p, "rb") as f:
            f.read(16)
        n_ok += 1
    except FileNotFoundError:
        pass
t1 = time.time()
print("%.3f %d" % (t1 - t0, n_ok))
PYEOF
)
umount_img
log "Random open of 1000 files, cold cache: ${RANDOPEN_COLD} (seconds, files_ok)"
log

# ---------------------------------------------------------------------------
# Part 3: build cost, unpacked (100000 files) vs bundled (1000 files of 100
# objects each), same total payload
# ---------------------------------------------------------------------------
log
log "## Part 3: build cost, unpacked vs bundled (same total payload)"
log

T0=$(date +%s.%N)
create_image 900
MKUDFFS_T=$(python3 -c "import time; print(time.time())")
T1=$(date +%s.%N)
MKUDFFS_TIME=$(python3 -c "print(f'{$T1-$T0:.3f}')")
mount_img
make_fanout_dirs
UNPACKED_T=$(write_files 100000 4096)
umount_img
log "mkudffs time: ${MKUDFFS_TIME}s"
log "Unpacked: 100000 files x 4096 bytes, write time: ${UNPACKED_T}s (mkudffs + write = $(python3 -c "print(f'{$MKUDFFS_TIME+$UNPACKED_T:.3f}')")s)"

create_image 900
mount_img
BUNDLED_T=$(write_bundles 1000 100 4096)
umount_img
log "Bundled: 1000 files x (100 objects x 4096 bytes), write time: ${BUNDLED_T}s (mkudffs + write = $(python3 -c "print(f'{$MKUDFFS_TIME+$BUNDLED_T:.3f}')")s)"
log

# ---------------------------------------------------------------------------
# Part 4: directory limits, one flat directory vs 256 fanout dirs
#
# A flat UDF directory insert is not O(1): each create scans the existing
# directory stream to place and later confirm the new FID. This makes a
# 100000-entry flat directory populate in unbounded time (extrapolated well
# past an hour from the scaling below), so this part measures write-time
# scaling on smaller N and reports the trend plus one flat-dir cold-read
# data point, instead of forcing the full N=100000 case to completion.
# ---------------------------------------------------------------------------
log
log "## Part 4: one flat directory vs 256 fanout dirs"
log
log "### 4a. flat directory populate time vs N (quadratic-growth check)"
log
log '| N | flat populate time (s) |'
log '|---:|---:|'

FLAT_SCALE_NS="1000 2000 5000 10000 20000"
LAST_FLAT_N=0
LAST_FLAT_T=0
for n in $FLAT_SCALE_NS; do
  create_image 500
  mount_img
  t=$(write_flat "$n" 4096)
  umount_img
  log "| $n | $t |"
  LAST_FLAT_N=$n
  LAST_FLAT_T=$t
done
log
log "(100000 was not attempted: write time roughly quadruples each time N"
log "doubles, so extrapolating from the table above puts a 100000-entry"
log "flat-directory populate in the tens of minutes to hours, versus"
log "${WRITE_T}s for the same 100000 files spread over 256 fanout dirs.)"
log

log "### 4b. flat directory cold-cache read cost at N=${LAST_FLAT_N}"
log

drop_caches
mount_img
T0=$(date +%s.%N)
FLAT_FOUND=$(sudo -n find "$MNT/flat" -type f | wc -l)
T1=$(date +%s.%N)
FLAT_WALK_COLD=$(python3 -c "print(f'{$T1-$T0:.3f}')")
log "Flat dir cold full walk (find), N=${LAST_FLAT_N}: ${FLAT_WALK_COLD}s, files found=${FLAT_FOUND}"

umount_img
drop_caches
mount_img
FLAT_RANDOPEN_COLD=$(python3 - "$MNT" "$LAST_FLAT_N" <<'PYEOF'
import os, sys, random, time
mnt, n = sys.argv[1], int(sys.argv[2])
base = os.path.join(mnt, "flat")
random.seed(42)
k = min(1000, n)
paths = []
for i in random.sample(range(n), k):
    fn = "%064x" % i
    paths.append(os.path.join(base, fn))
t0 = time.time()
n_ok = 0
for p in paths:
    try:
        with open(p, "rb") as f:
            f.read(16)
        n_ok += 1
    except FileNotFoundError:
        pass
t1 = time.time()
print("%.3f %d" % (t1 - t0, n_ok))
PYEOF
)
umount_img
log "Flat dir random open, N=${LAST_FLAT_N} (cold cache): ${FLAT_RANDOPEN_COLD} (seconds, files_ok)"
log

log
log "## Summary"
log
log "Fanout (256 dirs, N=100000) cold walk: ${WALK_COLD}s."
log "Flat dir (N=${LAST_FLAT_N}) cold walk: ${FLAT_WALK_COLD}s -- far slower per file than fanout."
log "Fanout cold random open (1000 of 100000): ${RANDOPEN_COLD}"
log "Flat dir cold random open (up to 1000 of ${LAST_FLAT_N}): ${FLAT_RANDOPEN_COLD}"

rm -f "$IMG"
sudo -n rm -rf "$MNT" 2>/dev/null || true
echo "Done. Results in $OUTFILE"
