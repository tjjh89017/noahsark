# The backup routine

This guide describes the routine a person actually follows: a first
full backup, then a repeating cycle of commits and packs over months,
drills to prove restore still works, and what to do when a disc fails
or the repository directory is lost. Every command line here is one
the current `noahsark` binary accepts. Run `noahsark <command> -h` at
any point to see a command's own flags.

The examples use one source directory, `/srv/data`, one repository at
`/srv/noahsark/repo`, one drive at `/dev/sr0`, and dates starting
2026-09-14. Substitute your own paths.

You need a Blu-ray or DVD writer and write-once media (BD-R, DVD+R, or
DVD-R), `dvd+rw-tools` 7.1-14 or newer (`growisofs`,
`dvd+rw-mediainfo`), and `udftools` 2.3 or newer (`mkudffs`) if you use
`image build`; the binary checks the `mkudffs` version itself and
refuses an older one. Root is needed only to mount a filesystem:
`image build`, and mounting a burned disc to verify or restore it.
`commit`, `pack`, and burning with `growisofs` never need root.

## 1. Day one

Build the binary and create the repository:

```sh
go build -o noahsark ./cmd/noahsark
./noahsark init --repo=/srv/noahsark/repo --capacity=bd25
```

`--capacity` here only sets a fallback default; every `pack` below
passes its own `--capacity` explicitly. `init` writes
`/srv/noahsark/repo/config`, holding `repo.uuid` (generated once,
identifies this repository across every disc it ever burns) and
`staging.dir`.

Commit the source tree, with a ref named by date so `log` later shows
which commit corresponds to which day:

```sh
./noahsark commit --repo=/srv/noahsark/repo --ref=2026-09-14 /srv/data
```

Read the media's real capacity and pack the whole commit to it:

```sh
dvd+rw-mediainfo /dev/sr0 | grep 'Free Blocks'
./noahsark pack --repo=/srv/noahsark/repo --capacity=bd25 \
    --ref=2026-09-14 --label="2026-09-14 run1" --out=/srv/noahsark/plans/run1
```

If `pack` reports `remaining staged` objects and exits 1, the source
did not fit one disc; pack again with a new `--out` for a second disc
and repeat until it reports `remaining staged: 0 objects, 0 bytes` and
exits 0. Section 3 below has the same loop for a regular cycle.

Burn two identical discs from `/srv/noahsark/plans/run1` (section 3
below gives the exact command lines and the twin-A/twin-B naming), and
verify each one by mounting it and running `noahsark verify`. On each
disc's sleeve, in permanent marker, write:

- the label you gave `pack` (`2026-09-14 run1`)
- the run and disc number `pack` printed (`packed run 1 on disc 1`)
- the date
- which twin it is, `A` or `B`

`pack`'s internal disc uuid is not printed by any command in normal
use; it surfaces only inside a `restore` or `rebuild-cache` error
message naming a missing disc. The run/disc number and the label you
chose are what you write and look for by hand.

Take twin B off-site immediately: a second physical location, not a
second shelf in the same room, is what makes the pair a real backup.
Keep twin A near the drive for the next verify or restore drill.

## 2. The regular cycle

Pick an interval, for example weekly, and commit the same source
directories every time, with a fresh date as the ref:

```sh
./noahsark commit --repo=/srv/noahsark/repo --ref=2026-09-21 /srv/data
```

Read the summary `commit` prints:

```
snapshot <id>
ref 2026-09-21 -> <id>
new objects: 183, existing objects: 51420
unstable 2 branch=flagged
skipped 0
unstable: 2, skipped: 2
```

- **New objects** are freshly staged content; **existing objects** were
  already staged or packed and are only referenced again, not
  recopied.
- An **unstable** line names a file that changed while `commit` was
  reading it. It is still committed and flagged UNSTABLE in the tree,
  so nothing is lost, but its content may not match what the file
  holds now. If the count is more than the odd editor swap file,
  let the source settle and commit again later; `ls --unstable-only`
  (section 5) finds these entries on a packed disc.
- A **skipped** line names a path that was deleted between the
  directory listing and the read; it is simply left out of this
  snapshot. Nothing to do.

Do not pack every commit. Packing is expensive in media and drive
time; committing is not. Pack when either is true:

- the bytes staged since the last pack are close to one disc's usable
  size, or
- a fixed calendar interval has passed (for example, once a month)
  even if the next disc will be mostly empty.

This build has no separate command that reports staged bytes ahead of
a pack. `pack` itself is how you find out: it reports what it packed
and, if anything did not fit, how much remains staged. In practice,
track roughly how much new data you have committed (`du -sh` on what
changed since the last pack) and use that as the trigger, falling back
to the calendar interval either way.

## 3. Each pack

Read the blank disc's real free space:

```sh
dvd+rw-mediainfo /dev/sr0 | grep 'Free Blocks'
```

The `bd25`, `bd50`, `bd100`, `bd128`, `dvd+r` and `dvd-r` presets
already match the real, drive-reported sector counts of that media, so
`--capacity=bd25` needs no adjustment for a normal disc. If the Free
Blocks count comes back lower than the preset (a slightly short disc),
or you want to force a smaller target on purpose, pass that block
count as `--physical-capacity` instead, leaving `--capacity` as the
budget `pack` plans against:

```sh
./noahsark pack --repo=/srv/noahsark/repo --capacity=bd25 \
    --physical-capacity=12180000 --label="2026-09-21 run2" --out=/srv/noahsark/plans/run2
```

Otherwise, pack the staged objects, naming the disc in the label:

```sh
./noahsark pack --repo=/srv/noahsark/repo --capacity=bd25 \
    --label="2026-09-21 run2" --out=/srv/noahsark/plans/run2
```

`pack` with no `--ref` and no `--snapshot` packs whatever `LATEST`
points at, which is the most recent commit; pass `--ref=2026-09-21`
explicitly if other commits happened since. If the summary shows
`remaining staged` objects and exit code 1, this one disc was not
enough: pack again with a new `--out` (`run2b`, and so on), burn and
verify that disc too, and repeat until a pack exits 0. Every disc in
that group belongs to the same backup cycle.

Burn two identical discs from the packed tree. Path A builds a UDF
image first (needs root, checks the `mkudffs` version):

```sh
sudo ./noahsark image build --out=run2.img --capacity=bd25 /srv/noahsark/plans/run2
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=run2.img
```

Path B burns the packed folder directly, no image step; `growisofs`
calls `genisoimage` itself when given a directory:

```sh
growisofs -speed=4 -use-the-force-luke=spare:min,tty \
          -Z /dev/sr0 -R -iso-level 4 -V NOAHSARK /srv/noahsark/plans/run2
```

Use `-R -iso-level 4` (Rock Ridge), never `-J` (Joliet) alone: Joliet
truncates names at 64 characters, cutting off NoahsArk's 68-character
object file names. Neither command line above passes `-dvd-compat`:
the disc is left open, on purpose, for a later append; only an
explicit close seals it.

Load the second blank disc and run the exact same command line again,
from the same source (`run2.img` or the `run2` folder). The two discs
must carry identical bytes; that pair is the backup's redundancy, not
Reed-Solomon parity, which stays off unless a pack used `--fec`.

Verify each disc by mounting it and reading it back through the
filesystem:

```sh
sudo mkdir -p /mnt/noahsark
sudo mount /dev/sr0 /mnt/noahsark
./noahsark verify --image=/mnt/noahsark
sudo umount /mnt/noahsark
```

`verify: ok` means this disc reads back exactly what was packed. A
disc that fails to mount, or that `verify` reports a failure for, is
thrown away: burn a fresh replacement from the same source and verify
that one instead. There is no raw carving recovery in this build; a
disc that does not verify is not trusted.

Label both discs (the label text, run/disc number, date, and A or B),
and record the pack in a plain text log kept next to the repository,
for example `/srv/noahsark/discs.log`:

```
2026-09-21  run2  label="2026-09-21 run2"  twin A: shelf  twin B: offsite box 3
```

Take twin B off-site. Keep twin A where the next verify or restore
drill can reach it.

## 4. Keep the repository directory safe

`/srv/noahsark/repo` holds the config file (`repo.uuid`,
`staging.dir`) and the staging store (`staging/objects`,
`staging/snapshots`, and the state log that tracks which objects are
already packed onto which disc). None of it is needed to read a
backup back: `restore`, `verify`, `ls`, and `log` all read disc roots
directly and never open `--repo`. Losing this directory never loses
data already burned.

It does matter for the next `pack`: without the state log, `pack` has
no way to know an object is already sitting on disc 1, so a `commit`
and `pack` run from scratch after losing the repository directory
would stage and burn every object again, discs included, instead of
only the new bytes. Rebuild the state from the discs before packing
again:

```sh
./noahsark rebuild-cache --from-disc --repo=/srv/noahsark/repo \
    --disc=/mnt/run1 --disc=/mnt/run2
```

Mount every disc the repository has ever burned first, and give
`rebuild-cache` every one of them (`--discs-dir=/mnt/noahsark-discs`
works too, the same way `restore` accepts it). A disc left out makes
the rebuild partial: exit code 1, naming the missing disc's uuid.
Once `rebuild-cache` reports `ok`, `pack` dedups correctly again.

## 5. Restore drill every few months

Do this on a schedule, not only after real data loss, so a drive or
format problem is found while the source is still around to compare
against.

Mount one disc from the pair and list its snapshots:

```sh
sudo mount /dev/sr0 /mnt/noahsark
./noahsark log /mnt/noahsark
```

`log` prints, for each snapshot, its id, time, and the refs pointing at
it (`refs: 2026-09-21`). `ls` and `log` accept that date-named ref
directly, but `restore` only takes the snapshot's own id, so read the
id off the matching line:

```sh
SNAP=$(./noahsark log /mnt/noahsark | grep 'refs: 2026-09-21' | awk '{print $1}')
```

List that snapshot's tree, and check for anything still flagged
UNSTABLE from a commit that ran while a file was mid-write. Flags
come before the disc root and the snapshot argument, not after:

```sh
./noahsark ls --recursive /mnt/noahsark 2026-09-21
./noahsark ls --recursive --unstable-only /mnt/noahsark 2026-09-21
```

Restore a few paths, not the whole snapshot, to a scratch directory:

```sh
./noahsark restore --include=srv/data/ledger.csv \
    --include=srv/data/photos/2026 /mnt/noahsark "$SNAP" /tmp/restore-drill
```

Compare against the live source:

```sh
diff -rq /tmp/restore-drill/srv/data/ledger.csv /srv/data/ledger.csv
sha256sum /tmp/restore-drill/srv/data/ledger.csv /srv/data/ledger.csv
```

No differences means this disc, this snapshot, and the include-path
syntax all still work together.

Every year or so, or before you would actually need to, run a full
restore with every disc mounted, to prove the whole chain still works
end to end:

```sh
sudo mkdir -p /mnt/noahsark-discs/run1 /mnt/noahsark-discs/run2
sudo mount /dev/sr0 /mnt/noahsark-discs/run1
# swap discs, mount the next one at /mnt/noahsark-discs/run2, and so on
SNAP=$(./noahsark log --discs-dir=/mnt/noahsark-discs | grep 'refs: 2026-09-21' | awk '{print $1}')
./noahsark restore --discs-dir=/mnt/noahsark-discs "$SNAP" /tmp/restore-full
diff -rq /tmp/restore-full/srv/data /srv/data
```

## 6. When the source changes a lot

Over enough cycles, a source directory drifts: files are deleted, and
old objects that no snapshot still needs sit packed on early discs
anyway, since NoahsArk never rewrites a disc once burned. Consolidating
a spread-out backup onto a fresh, self-contained disc set is not part
of this build; `consolidate` is a later-phase command the binary
refuses outright.

Today's answer, when a source has changed enough that you want a
clean, self-contained set again, is plain: start a new repository and
run a new full backup, the same way section 1 did.

```sh
./noahsark init --repo=/srv/noahsark/repo-2027 --capacity=bd25
./noahsark commit --repo=/srv/noahsark/repo-2027 --ref=2027-01-04 /srv/data
```

Keep the old repository's discs; do not discard the old backup just
because a new one started. Label the new pair's discs so it is clear
they belong to a different repository (a different `repo.uuid`), for
example `"2027-01-04 repo2 run1"`.

## 7. Troubleshooting

- **`mkudffs` version refused.** `image build` prints the version it
  found and the minimum it requires (udftools 2.3). Upgrade
  `udftools`, or burn with path B in section 3, which never calls
  `mkudffs`.
- **A disc will not mount.** Treat it as lost outright: this build has
  no carving recovery to fall back on. Throw it away, burn a fresh
  replacement from the same packed tree, and use its twin in the
  meantime.
- **`verify` reports a checksum mismatch or fails.** Same as a failed
  mount: throw the disc away, burn a fresh replacement, and verify
  that one before trusting it.
- **`pack` reports `remaining staged` and exits 1.** The source did
  not fit the given capacity. Pack again with a fresh `--out`
  directory for another disc in the same cycle; loop pack, burn,
  verify until a pack exits 0.
- **`commit` prints `unstable` lines.** A file changed while it was
  being read, so its committed content may not match the file now. It
  is still safely committed and flagged; let the source settle and
  commit again, or find it later with `ls --unstable-only` on the
  packed disc.
- **A restore says `missing disc(s)`.** It names each needed disc by
  uuid and how many objects it holds. Mount that disc (match it by the
  run/disc number and label on its sleeve, from your text log) and
  restore again with `--disc` or `--discs-dir` including it.
