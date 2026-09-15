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

You need Go 1.27 or newer to build the binary (`go.mod` names this
version), a Blu-ray or DVD writer and write-once media (BD-R, DVD+R, or
DVD-R), `dvd+rw-tools` 7.1-14 or newer (`growisofs`,
`dvd+rw-mediainfo`), and `udftools` 2.3 or newer (`mkudffs`) if you use
`image build`; the binary checks the `mkudffs` version itself and
refuses an older one. Root is needed to loop-mount the image
`image build` populates, and to mount a burned disc to verify or
restore it. `commit`, `pack`, and burning with `growisofs` never need
root.

`growisofs` itself needs permission to open the drive device
(`/dev/sr0` in these examples): add your user to the `cdrom` group
(then log out and back in), or add a udev rule that grants it. Without
a device node at all, `growisofs` refuses with a line naming the
device, for example:

```
:-( "/dev/sr0=run.img": unexpected errno:No such file or directory
```

`No such file or directory` means the device node itself does not
exist; check the drive is connected and the node's actual name before
assuming the media is bad. A device node that exists, but that your
user has no permission to open (not yet in the `cdrom` group, or no
matching udev rule), instead reports:

```
:-( unable to open64("/dev/sr0",O_RDONLY): Permission denied
```

and `growisofs` exits with code 141. Fix the group membership or the
udev rule, log out and back in (or reboot), and try again. A device
that exists and that your user can open, but that holds no write-once
media, or media it does not recognize, instead reports something like:

```
:-( /dev/sr0: media is not recognized as recordable DVD: 0
```

That line means the drive was reached; load a blank BD-R, DVD+R, or
DVD-R and try again.

## 1. Day one

Build the binary and create the repository:

```sh
go build -o noahsark ./cmd/noahsark
./noahsark init --repo=/srv/noahsark/repo --capacity=bd25
```

`--capacity` here only sets a fallback default; every `pack` below
passes its own `--capacity` explicitly. `init` writes
`/srv/noahsark/repo/config`, holding `repo.uuid` (generated once,
identifies this repository across every disc it ever burns),
`staging.dir`, and `disc.force_capacity` (the sector count for
`--capacity=bd25` above). `pack` reads `disc.force_capacity` as its
own fallback whenever `--capacity` is left off its command line.

Every command below passes `--repo` explicitly, but it is not always
required. When `--repo` is omitted, the binary looks for a repository
in this order: the `NOAHSARK_REPO` environment variable, if set; else
the nearest ancestor of the current directory that holds a `config`
file, searching upward from the working directory. Set
`NOAHSARK_REPO=/srv/noahsark/repo` in your shell, or run commands from
inside `/srv/noahsark/repo`, to drop `--repo` from every command line
below.

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
verify each one by mounting it and running `noahsark verify`. Before
labelling, read the disc's own uuid, seq and label back from the
repository:

```sh
./noahsark disc list --repo=/srv/noahsark/repo
```

Each disc's line ends `objects=N packed=N clean=N`: `objects` is every
object the staging state machine still places on that disc (PACKED,
BURNED, CLEAN, GC-ELIGIBLE, or already DELETED from staging, since the
disc itself never loses the bytes); `packed` and `clean` break that
same total down by current state, so you can see at a glance whether a
disc's run has been marked burned and verified yet (`packed` still
nonzero) or has gone all the way to CLEAN.

On each disc's sleeve, in permanent marker, write:

- the first 8 characters of the uuid, the bare first column of each
  `disc list` line (not a labelled field; `seq=`, `label=` and the rest
  follow it)
- `seq` from that same line (the disc number, `0` for the first disc)
- the label you gave `pack` (`2026-09-14 run1`)
- the date
- which twin it is, `A` or `B`

A `restore` or `rebuild-cache` error naming a missing disc also names
its full uuid; the 8-character prefix on the sleeve is enough to match
it back to `disc list`'s output. `disc burned`, though, takes the full
uuid, not the 8-character prefix; copy it whole from `disc list`.

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
unstable srv/data/incoming/upload.tmp branch=flagged
skipped srv/data/incoming/deleted-mid-scan.log
unstable: 1, skipped: 1
staged: 51603 objects, 24800000000 bytes
```

- **New objects** are freshly staged content; **existing objects** were
  already staged or packed and are only referenced again, not
  recopied. After a lost repository directory, `existing objects`
  counts packed content correctly again only once `rebuild-cache` has
  rebuilt the state log from the discs (section 6); before that, `pack`
  and `commit` have no record of what a disc already holds.
- An **unstable PATH** line names a file that changed while `commit`
  was reading it. It is still committed and flagged UNSTABLE in the
  tree, so nothing is lost, but its content may not match what the
  file holds now. If more than the odd file shows up here,
  let the source settle and commit again later; `ls --unstable-only`
  (section 8) finds these entries on a packed disc. `commit` exits 1
  when this count is nonzero, the same as a skipped path below; the
  data is still safely committed either way, only flagged or left out.
- A **skipped PATH** line names a path that was deleted between the
  directory listing and the read; it is simply left out of this
  snapshot. Nothing to do.

Commit as often as you like, each with its own dated `--ref`; a commit
is cheap. `pack` does not pack "whatever the ref points at": one `pack`
call packs every staged object of every snapshot not yet packed onto a
disc, and it carries onto that disc every ref that still points at one
of the snapshots it packs. So a `--ref` you gave to `commit` does not
need to be repeated on the matching `pack` call for its snapshot to be
packed; pass `--ref` to `pack` only when you want to name an extra ref
for the run (`--ref` moves no ref by itself; only `commit --ref=NAME`
does). If a ref is missing from a disc's history, run `log` against
the packed tree or a mounted disc to find the snapshot id it should
point at, and pack or restore by that id instead.

Do not pack every commit. Packing is expensive in media and drive
time; committing is not. Pack when either is true:

- the bytes staged since the last pack are close to one disc's usable
  size, or
- a fixed calendar interval has passed (for example, once a month)
  even if the next disc will be mostly empty.

The `staged:` line every `commit` prints is the number to check: it is
the repository-wide STAGED total, objects and bytes, waiting for the
next pack. Compare it against the target disc's usable size (the
`--capacity` preset you plan to pack with; README.md's "Disc capacity"
section has the sector and byte size of each preset). A commit made
but never packed before the state log is lost shows as staged 0 after
the loss: `rebuild-cache` restores only what a disc's own catalog
carries, and an unpacked commit was never on any disc. Committing the
same source again re-stages it and the `staged:` total is correct from
then on.
Between commits, or to check without committing anything, the same
total is the last line of:

```sh
./noahsark disc list --repo=/srv/noahsark/repo
```

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
./noahsark pack --repo=/srv/noahsark/repo --capacity=bd25 --ref=2026-09-21 \
    --physical-capacity=12180000 --label="2026-09-21 run2" --out=/srv/noahsark/plans/run2
```

Otherwise, pack the staged objects, naming the disc in the label:

```sh
./noahsark pack --repo=/srv/noahsark/repo --capacity=bd25 --ref=2026-09-21 \
    --label="2026-09-21 run2" --out=/srv/noahsark/plans/run2
```

Section 2 above already explains why `--ref=2026-09-21` on this `pack`
call is not what makes the 2026-09-21 commit get packed: `pack` with
no `--ref` and no `--snapshot` already packs every unpacked snapshot
and carries every ref pointing at one of them. This guide still writes
`--ref=2026-09-21` above, for clarity in the command line, not because
`pack` needs it to find that commit's snapshot. If the summary shows
`remaining staged` objects and exit code 1, this one disc was not
enough: pack again with a new `--out` (`run2b`, and so on), burn and
verify that disc too, and repeat until a pack exits 0. Every disc in
that group belongs to the same backup cycle.

`pack` already printed a "next steps" block naming the exact `image
build`, `growisofs`, and `verify` command lines for this run; the
commands below are the same ones, spelled out. Burn two identical
discs from the packed tree. Path A builds a UDF image first (needs
root, checks the `mkudffs` version):

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

Tell the staging state machine the burn happened, then verify each disc
by mounting it and reading it back through the filesystem:

```sh
./noahsark disc burned --repo=/srv/noahsark/repo <disc uuid>

sudo mkdir -p /mnt/noahsark
sudo mount /dev/sr0 /mnt/noahsark
./noahsark verify --repo=/srv/noahsark/repo --image=/mnt/noahsark
sudo umount /mnt/noahsark
```

`disc burned` is the step that moves this run's objects from PACKED to
BURNED; `pack`'s own next-steps output already prints the exact command
line, uuid included. It has to be a separate, explicit step: `verify`
never assumes a tree it can read is a burned disc just because its uuid
is in the ledger, since `pack` writes the ledger before anyone burns
anything, and section 10 below has you loop-mount and verify the image
*before* burning it. Running `verify` without `disc burned` first still
checks the disc, but leaves every object PACKED, and prints which `disc
burned` command to run.

`verify: ok`, with no PACKED objects left over, means this disc reads
back exactly what was packed, and `--repo` has moved the run's objects
on to CLEAN, the first step toward freeing their staging copies later
(section 7 below). A disc that fails to mount, or that `verify` reports
a failure for, is thrown away: burn a fresh replacement from the same
source, run `disc burned` on the new uuid, and verify that one instead.
There is no raw carving recovery in this build; a disc that does not
verify is not trusted.

Read the new disc's uuid and seq back before labelling it:

```sh
./noahsark disc list --repo=/srv/noahsark/repo
```

Label both discs (the uuid prefix and seq from `disc list`, the label
text, the date, and A or B), and record the pack in a plain text log
kept next to the repository, for example `/srv/noahsark/discs.log`:

```
2026-09-21  disc c59ffe81 seq 1  label="2026-09-21 run2"  twin A: shelf  twin B: offsite box 3
```

Take twin B off-site. Keep twin A where the next verify or restore
drill can reach it.

## 4. After the burn

Once both twins are burned, the sequence for each is always the same:

1. Run `noahsark disc burned --repo=/srv/noahsark/repo <disc uuid>`,
   the exact command line `pack` printed. This moves the run's objects
   from PACKED to BURNED.
2. Mount it and run `noahsark verify --repo=/srv/noahsark/repo
   --image=<mount point>`, as above. Do this before the disc leaves the
   room. On success this moves the same objects on to CLEAN; see
   section 7 for what that starts.
3. Once it reads `verify: ok`, with no PACKED objects left over, label
   the sleeve from `disc list`'s output (uuid prefix, seq, label text,
   date, A or B).
4. Store the second copy (twin B) off-site. A shelf in the same
   building is not a second location.
5. Record the pack in the plain text log kept next to the repository.

The packed tree directory (`--out`, `/srv/noahsark/plans/run2` above)
is not needed to read the backup back once both twins verify: a disc
is self-describing, and restore, verify, ls, and log all read the disc
roots, never the staging tree. Keep it only if you expect to re-run
`image build` or re-burn from it soon; once both twins verify, deleting
it frees disk space without touching the repository's dedup state,
which lives in the staging state log, not in this directory.

## 5. Multi-disc backups and appending

One `pack` call fills at most one disc, up to `--capacity`. When the
staged bytes do not fit, `pack` exits 1 reporting `remaining staged`,
and the fix is another `pack` call with a fresh `--out`, producing a
second disc; section 3 above already loops this way for one backup
cycle. Across separate cycles, months apart, this looks the same:
every `pack` picks up where the last one for that ref left off, since
already-packed objects are never re-copied.

Every burn command this guide shows leaves the disc open (`spare:min`,
no `-dvd-compat`): the intent is that a disc can later be appended to
instead of always burning a new one. `append` is a Phase 2 command,
not in this build; there is no way to add another run to an
already-burned disc yet. Until `append` exists, a disc that needs more
data means another `pack` onto a fresh disc, as this section already
describes, not writing more onto an existing one.

## 6. Keep the repository directory safe

`/srv/noahsark/repo` holds the config file (`repo.uuid`,
`staging.dir`) and the staging store (`staging/objects`,
`staging/snapshots`, and the state log that tracks which objects are
already packed onto which disc). None of it is needed to read a backup
back given a disc root: `restore` given a disc root, `verify`, `ls
DISC-ROOT`, and `log DISC-ROOT` all read disc roots directly and never
open `--repo`. Losing this directory never loses data already burned.
(`restore --mount`, the single-drive mode, and `ls`, `log` or `plan`
with no disc given, do open the repository, since they resolve the
snapshot through its state instead.)

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
Once `rebuild-cache` reports `ok`, `pack` dedups correctly again. The
same command is also the fix for a smaller loss: a corrupted or
deleted state log alone, with the config file and `refs.txt` untouched,
since `rebuild-cache` rewrites the state log, the disc ledger, and the
local refs from the discs regardless of what survived.

With a single drive, mounting every disc at once is not possible: run
`rebuild-cache --from-disc --disc=<mount point>` once per disc instead,
swapping discs between runs, into the same `--repo`. Each run marks
that disc's own objects packed in the state log, so feed every disc for
the state log to end up complete. Feed the discs in any order; every
call merges into the ledger.

`ok` means every disc the discs fed so far know about has itself been
fed, not merely that its uuid showed up copied into some other disc's
own DISCS table. An older disc never knows about a disc burned after
it, so feeding only the oldest disc of a chain can print `ok` while
newer discs still exist and still need feeding; `ok` is not by itself
proof that every disc of the whole chain has been rebuilt. A run that
still has discs left to feed reports `rebuild is partial: disc <uuid>
(<label>) not fed yet`, one line per such disc, and exits 1. When in
doubt, compare `disc list`'s count and labels against the disc log or
the physical sleeves, rather than trust `ok` alone to mean the whole
chain is accounted for.

Replaying a disc's catalog through `rebuild-cache` never resets an
object past PACKED back to PACKED: an object the state log already
carries as BURNED, CLEAN, GC-ELIGIBLE or DELETED stays exactly there.
So after rebuilding a repository directory lost outright, `gc` still
works from the rebuilt state log alone, without repeating `disc
burned` and `verify` for discs already burned and verified before the
loss.

## 7. Disk space

Nothing in `staging/objects` is deleted just because it was packed onto
a disc. An object leaves the staging store only after this full cycle:

1. **Burn** both twins, as section 4 above already describes.
2. **Mark it burned**: `noahsark disc burned --repo=/srv/noahsark/repo
   <disc uuid>`. This moves the run's objects from PACKED to BURNED. It
   has to be a separate step from verify: a loop-mounted image checked
   before burning (section 10) has the same disc uuid already in the
   ledger, so verify cannot treat a ledger match alone as proof that a
   disc exists.
3. **Mount** the twin and run `noahsark verify --repo=/srv/noahsark/repo
   --image=<mount point>`. `--repo` is what lets verify update the
   staging state, not just report `verify: ok`; without it, verify
   still checks the disc, but changes nothing in staging. On success,
   every BURNED object of that run moves on to CLEAN; verify warns
   instead, naming the `disc burned` command, if any object is still
   PACKED.
4. **Wait** out `staging.retain_after_clean` (7 days by default). A
   CLEAN object is not deletable yet; the retention period is the
   window for a mistake to surface before the only copy on disk is the
   one on the discs themselves. The config value is a whole number of
   days with a `d` suffix (`7d`), or any duration `time.ParseDuration`
   accepts (`1h`); to try this whole cycle without actually waiting a
   week, set `staging.retain_after_clean = 0d` in the config, burn and
   verify a disc, and its objects are eligible immediately.
5. **Run `gc`**:

   ```sh
   noahsark gc --repo=/srv/noahsark/repo
   ```

   `gc` deletes every object that has stayed CLEAN past the retention
   period, after confirming each one is really present in its run's
   catalog; an object whose run's catalog is not in the local cache is
   left alone and counted separately, never deleted on a guess. Run
   `noahsark gc --repo=/srv/noahsark/repo --dry-run` first to see what
   it would free without deleting anything: `--dry-run` always exits 0,
   printing `gc: nothing is eligible yet` and the earliest date some
   object reaches the retention period when nothing is eligible yet. A
   real `gc` run exits 1 when nothing was eligible to delete, 2 on
   failure, and 0 once it deletes something.

   `--force-after=DURATION` shortens the retention to `DURATION` for
   this one `gc` run only, ignoring `staging.retain_after_clean`,
   useful when space is short and you are willing to accept a shorter
   window before the next real disaster: `gc --force-after=1h`. It asks
   for confirmation on stderr first (`delete N object(s), B bytes?
   [y/N]`), read from stdin, unless `--dry-run` is also given; it
   refuses outright, rather than guessing, when stdin is not a terminal
   and `--yes` is not given.

Check the staging store's size at any point with:

```sh
du -sh /srv/noahsark/repo/staging
```

If space runs out before a disc's retention period has passed, do not
delete files out of `staging/objects` or `staging/snapshots` by hand:
`pack`, `commit` and `gc` all depend on the state log matching what is
actually there. The safe way to start fresh is a new repository
directory (section 11 below shows this for a changed source); burn a
full new backup into it, and keep or discard the old repository's
staging store once its discs are no longer being packed further.

`gc` also trims the local cache (`~/.cache/noahsark/<repo-uuid>/` by
default), which is a separate, smaller amount of space from staging:
pass `--keep-snapshots=N` to keep only the newest N snapshots' trees
and blobs in the cache, or leave it out (or set `cache.snapshot_depth`
in the config) to keep the cache as is. Nothing about trimming the
cache touches a disc or the staging store: `rebuild-cache --from-disc`
always restores whatever the trim dropped.

## 8. Restore drill every few months

Do this on a schedule, not only after real data loss, so a drive or
format problem is found while the source is still around to compare
against.

An incremental disc alone cannot restore a snapshot: a disc packed
after the first one holds only the objects new or changed since the
last pack, and leans on earlier discs for everything unchanged. Mount
every disc of the chain before this drill, one directory per disc
under a common parent (or copy each disc's `NOAHSARK` directory into
its own subdirectory of one folder on local disk, if you would rather
not keep several drives or discs mounted at once), then pass that
parent with `--discs-dir`, or repeat `--disc=PATH` once per disc:

```sh
sudo mkdir -p /mnt/noahsark-discs/run1 /mnt/noahsark-discs/run2
sudo mount /dev/sr0 /mnt/noahsark-discs/run1
# swap discs, mount the next one at /mnt/noahsark-discs/run2, and so on
```

If you mount (or pass) fewer discs than a command needs, it fails with
`missing disc(s)`, and the error names the exact disc, by uuid, that is
missing; section 13's troubleshooting entry for that message says how
to match the uuid back to a disc.

List every snapshot across the whole chain:

```sh
./noahsark log --discs-dir=/mnt/noahsark-discs
```

`log` prints, for each snapshot, its id, time, and the refs pointing at
it (`refs: 2026-09-21`). `ls`, `log` and `restore` all accept that
date-named ref directly, in place of the snapshot's own id, so the
ref from section 1 or 2's `commit --ref=...` is enough on its own; no
need to read an id off `log`'s output first. A line reading
`roots: (none)` does not mean the snapshot is empty: it means that
snapshot's root tree object lives on a disc not given to this `log`
call, so pass every disc of the chain, as above, before reading
anything into `roots: (none)`. `log` with no disc given, reading the
local cache, prints the same `roots: (none)` for a snapshot `gc
--keep-snapshots=N` has since trimmed out of the cache; `rebuild-cache
--from-disc` restores it.

List that snapshot's tree, and check for anything still flagged
UNSTABLE from a commit that ran while a file was mid-write. Flags
come before the snapshot argument, not after:

```sh
./noahsark ls --recursive --discs-dir=/mnt/noahsark-discs 2026-09-21
./noahsark ls --recursive --unstable-only --discs-dir=/mnt/noahsark-discs 2026-09-21
```

### Plan a restore before you fetch the discs

`plan` answers "which discs do I need" before you mount anything: it
reads the local cache the earlier `pack` and `rebuild-cache` calls left
behind, never a disc, so run it first, from wherever the repository
lives:

```sh
./noahsark plan 2026-09-21
```

This prints one line per disc the restore would read, ordered by bytes
needed from that disc, largest first, with each disc's uuid, label,
object count and bytes, then a totals line. The single-drive
disc-swap restore below prompts for discs in this same order. If
nothing has ever been packed or rebuilt into
this machine's local cache, `plan` fails instead with `cache: no run is
cached yet; run pack, or rebuild-cache --from-disc, first`: `plan`
never reads a disc itself, so pack once from this repository, or run
`rebuild-cache --from-disc` against a disc you have on hand (section 6
above), before planning a restore on a machine that has never packed
anything.

Narrow it to the same paths you plan to restore
with `--include`, the same flag `restore` takes:

```sh
./noahsark plan --include=srv/data/ledger.csv \
    --include=srv/data/photos/2026 2026-09-21
```

Go fetch and mount exactly the discs the plan named, under
`--discs-dir` as above, before running `restore`. If the cache itself
is incomplete for this snapshot, `plan` says so and names
`rebuild-cache --from-disc` as the fix, the same message `ls` and
`log` give; run it from whichever disc the message names, then plan
again. `--out=FILE` writes the same plan as JSON, useful for a script
that mounts discs on its own.

### One drive: let restore ask for each disc

With only one optical drive, mounting every disc of a chain at once is
not possible. Give `restore` a single `--mount=DIR` instead of
`--discs-dir` or `--disc`, and leave off the disc root entirely: it
builds the same plan `plan` would, from the local cache, prints it, and
then works through the plan one disc at a time, the way an old game
installer asks for the next volume.

```sh
sudo mkdir -p /mnt/noahsark-drive
sudo mount /dev/sr0 /mnt/noahsark-drive
./noahsark restore --mount=/mnt/noahsark-drive 2026-09-21 /tmp/restore-drill
```

For each disc, `restore` checks `/mnt/noahsark-drive/NOAHSARK/DISC.bin`
against the plan. When the right disc is already mounted, it prints one
line and moves on:

```
disc 0 2026-09-14 run1: found
```

Otherwise it prompts on stderr and waits for a line on stdin:

```
insert disc 1 "2026-09-21 run2" (uuid 85f302d6-b864-478f-fbb4-dd02f0d78674) into /mnt/noahsark-drive and press Enter
```

Unmount the current disc, put in the one the prompt names, mount it
again at the same `--mount` directory, and press Enter. If the wrong
disc goes in, `restore` says so and prompts again:

```
expected disc 85f302d6-b864-478f-fbb4-dd02f0d78674 (2026-09-21 run2), found 34d8de68-32f1-c77e-2219-37fb1133b882 (2026-09-14 run1)
```

`restore` unmounts and ejects the drive itself after each disc, unless
`--no-eject` is given; `--interactive` prompts before every disc, even
one already correctly mounted, useful when handling discs by hand makes
that reassurance worth the extra keypress.

If the session is interrupted partway through (a crashed terminal, a
closed laptop lid), objects already read sit in
`staging/restore/<snapshot-id>/` inside the repository directory.
Running the same `restore` command again picks up where it left off,
printing one line at the start naming what the spool already holds:

```
resuming: 12 object(s) already spooled
```

That `resuming:` line prints only when a spool from an earlier,
interrupted run actually exists; a fresh restore of a snapshot never
attempted before starts silently, with no such line.

and only prompts for whichever disc the plan still needs. At the end,
it also prints how many files that spool let it skip re-reading:

```
resumed: 4 file(s) already restored
```

Pass `--plan=FILE` with a plan `plan --out=FILE` already wrote, in
place of letting `restore` build its own: useful when a script plans
once, on a machine with the cache handy, and hands the plan file to
whoever runs the actual restore. `--plan` takes no SNAPSHOT of its
own; the plan file already names it:

```sh
./noahsark restore --plan=/tmp/2026-09-21.plan.json --mount=/mnt/noahsark-drive /tmp/restore-drill
```

`restore --plan` refuses a plan file written for a different
repository, or naming a snapshot this repository's cache does not
know, rather than guessing.

`--staging-budget=SIZE` caps how much of `staging/restore/` a
disc-swap restore ever uses at once, overriding `restore.staging_budget`
for this run; it takes the same units as `--capacity` (`4GiB`, `500MB`,
or a plain byte count). When one disc's share would go over the
budget, `restore` reads it in more than one pass, printing `pass 1/2`
and so on, assembling and freeing whatever files complete between
passes, without prompting again for the same disc. A file whose own
chunks alone are bigger than the budget is refused up front, naming the
file, before any disc is read.

Restore a few paths, not the whole snapshot, to a scratch directory:

```sh
./noahsark restore --include=srv/data/ledger.csv \
    --include=srv/data/photos/2026 \
    --discs-dir=/mnt/noahsark-discs 2026-09-21 /tmp/restore-drill
```

Compare against the live source:

```sh
diff -rq /tmp/restore-drill/srv/data/ledger.csv /srv/data/ledger.csv
sha256sum /tmp/restore-drill/srv/data/ledger.csv /srv/data/ledger.csv
```

No differences means this disc chain, this snapshot, and the
include-path syntax all still work together.

Every year or so, or before you would actually need to, run the same
kind of restore for the whole snapshot instead of a few paths, to
prove the whole chain still works end to end:

```sh
./noahsark restore --discs-dir=/mnt/noahsark-discs 2026-09-21 /tmp/restore-full
diff -rq /tmp/restore-full/srv/data /srv/data
```

A single-disc repository (section 1's first backup, before any second
pack) is the one case where `restore DISC-ROOT SNAPSHOT OUT-DIR`, with
one disc root and no `--discs-dir`, is already enough on its own.

## 9. When a disc is lost

OPERATIONS.md's "Failure and recovery actions" table gives the general
rule; here is what it means in this build, where the redundancy is two
identical discs, not Reed-Solomon parity or a cross-disc parity group
(both later-phase features):

- **One of the twins is lost or destroyed.** Read from its surviving
  twin. Burn a fresh replacement from the same packed tree (keep it
  around for this, or re-run `pack` with the same `--ref` if the tree
  is gone; a fresh `pack` still packs the same snapshot, just under a
  new disc uuid) and treat that as the new twin B.
- **The newest disc of a multi-disc backup is lost, twin included.**
  Every earlier disc still carries its own catalog as of its own burn,
  and an earlier disc's `INDEX` already names the objects a later disc
  depends on as prerequisites. Restore what the surviving discs cover;
  what is missing is exactly what the lost disc alone held, and
  `restore` names it if you try to restore a snapshot that needed it.
- **A restore or `rebuild-cache` cannot find a disc it needs.** The
  error names that disc by its full uuid, and how many objects it
  holds, the same as the troubleshooting entry below. Match the uuid
  against your sleeve labels or your text log.
- **The repository directory itself is gone, on top of a lost disc.**
  Run `rebuild-cache --from-disc` against every disc you still have, as
  in section 6. It reports the rebuild as partial and names the uuid of
  any disc its own DISCS table still expects but that you could not
  provide.

## 10. No physical drive: testing without one

Every step up to burning can be tested with no drive and no media at
all, which is useful before buying a drive, or to rehearse a restore in
CI:

```sh
./noahsark init --repo=repo --capacity=bd25
./noahsark commit --repo=repo --ref=2026-09-14 /srv/data
./noahsark pack --repo=repo --capacity=bd25 --ref=2026-09-14 --out=tree
./noahsark verify --image=tree
./noahsark restore tree 2026-09-14 restored
diff -rq restored/srv/data /srv/data
```

`verify --image` and `restore DISC-ROOT` both accept the packed tree
directory `pack --out` produces directly: neither needs a UDF
filesystem or a mount. To also exercise the UDF image path, add `sudo
noahsark image build --out=tree.img --capacity=bd25 tree` (root, for
the loop mount, but still no drive), then loop-mount `tree.img` and
`verify --image=` the mount point, as section 3 already shows for a
real burn. Only the `growisofs` line itself needs a real drive.
`image build` writes a real file of the full `--capacity`, though, so
for a rehearsal pass `--capacity=1GB` to both `pack` and `image build`
instead of a media preset like `bd25`, unless the point of the
rehearsal is specifically to check a 25 GB image.

## 11. When the source changes a lot

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

## 12. Upgrading

A newer `noahsark` build reads every disc an older build wrote.
FORMAT.md's reader and writer rules make every version change
additive or clean: a minor version bump only appends fields an old
reader already knows to skip, and a major version bump is refused
outright, naming the version it will not read, rather than misread. A
disc written with an algorithm or table id your current build predates
is refused the same way, naming the unknown code, rather than silently
misreading it. So restoring from discs a newer or older `noahsark`
build burned is always either a full, correct read, or a clean,
named refusal; it is never a silent misread.

## 13. Troubleshooting

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
- **A restore says `missing disc(s)`.** It names each needed disc, one
  per indented line, by uuid and how many objects it holds. Mount that
  disc (match it by the uuid prefix and seq on its sleeve, from your
  text log, or by running `disc list` against the repository if it is
  still reachable) and restore again with `--disc` or `--discs-dir`
  including it. Section 9 covers what to do when the missing disc
  cannot be found at all.
- **A restore says `the snapshot's root tree is not on the provided
  disc(s)`.** None of the discs given so far hold the snapshot's own
  root tree object, so no disc can even be named by uuid yet; mount
  more of the chain (or all of it) and try again.
- **A restore says `object(s) not found on any provided disc and named
  by no provided disc's INDEX`.** This is what an older disc alone
  looks like when a newer disc that actually stores the needed objects
  is missing: an older disc's own INDEX and Prereqs tables cannot name
  a disc burned after it, so restore cannot even point at a uuid.
  Mount more of the chain, newest discs included, and try again.
- **`growisofs` prints `unexpected errno:No such file or directory`.**
  The device node (`/dev/sr0`) does not exist: check the drive is
  connected and that you named the right device.
- **`growisofs` prints `unable to open64(...): Permission denied` and
  exits 141.** The device node exists, but your user cannot open it:
  add your user to the `cdrom` group, or add a udev rule, per the
  permissions note near the top of this guide, then log out and back
  in and try again.
- **`growisofs` prints `media is not recognized as recordable DVD`.**
  It did reach the drive; load a blank BD-R, DVD+R, or DVD-R and try
  again.
