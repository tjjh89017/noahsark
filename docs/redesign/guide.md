# Operator guide (draft)

This is a design draft for a new `noahsark`. It is the specification.
Code follows it later. Part 1 is the full cycle, on about one page. Part 2
is a reference. `noahsark <command> -h` lists all flags of a command. A
line that starts with `$` is a command you type. The lines below it are
its output. `/srv/ark/repo` is the repository, `/srv/data` is the source,
`2026-09-14` is a ref, and `/mnt/ark` is a disc mount point. Put your own
values in their place.

## What it is

`noahsark` copies your files to write-once discs, so that a fire or a
theft or a ransom does not end your data. It never deletes your only
copy of anything, and it never needs the tool itself to read a disc back.
Two identical discs, in two places, are the backup; a third repair layer
is optional.

## Words

- **Item**: one piece of your data. Equal data is stored one time.
- **Snapshot**: the state of the source at one commit.
- **Ref**: your name for a snapshot. Use the date.
- **Staged**: committed and held in the repository, not yet on a disc.
- **Disc**: one physical write-once disc. The uuid is its exact name.
- **Disc root**: a directory that holds `NOAHSARK/`: a packed tree, or a
  mounted disc.
- **Copy**: one burn of a disc. Two copies of each disc are the backup.

`status` shows the state of each disc in one more word: `packed`,
`burned`, `verified 1/2`, `verified`, `lost`, `on disc only`, or, only
while a lost repository is being rebuilt, `missing`.

## 1. Set up, one time

You need Go 1.27 or later, a DVD or Blu-ray writer, write-once media,
`dvd+rw-tools` 7.1-14 or later (`growisofs`), and `udftools` 2.3 or later
(`mkudffs`). Build in the source checkout. Put the binary on your `PATH`.

```
$ go build -o noahsark ./cmd/noahsark
$ sudo usermod -aG cdrom $USER      # then log in again
$ noahsark init --repo=/srv/ark/repo --source=/srv/data --capacity=bd25
initialized repository /srv/ark/repo
source: /srv/data
capacity: bd25 (25025314816 bytes)
device: /dev/sr0
next: back up now, run: noahsark commit
$ export NOAHSARK_REPO=/srv/ark/repo
```

Put the `export` line in your shell profile. `init` writes the source and
the capacity into the repository. You never edit the config file by
hand. Give `--device=PATH` when your writer is not `/dev/sr0`; `noahsark`
prints this path in commands for you to run, and never runs it itself.

## 2. Back up

*... Days pass. You add, change, and delete files in `/srv/data`.*

```
$ noahsark commit
snapshot 12201b03...
ref 2026-09-14 -> 12201b03...
new items: 8, existing items: 0
unstable: 0, skipped: 0
staged: 8 items, 3001350 bytes
$ noahsark pack
packed disc 0 "2026-09-14 disc 0": 8 item(s), 3001350 bytes
uuid: 4a060bd4-ca9f-2d06-263e-b907483b8230
next: noahsark status
$ noahsark status
staged: 0 items, 0 bytes
disc 0 "2026-09-14 disc 0"  packed  4a060bd4-ca9f-2d06-263e-b907483b8230
next:
  sudo noahsark image build /srv/ark/repo/staging/plans/4a060bd4.../tree
  growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=/srv/ark/repo/staging/plans/4a060bd4.../tree.img
  noahsark disc burned 0
  sudo mkdir -p /mnt/ark && sudo mount /dev/sr0 /mnt/ark
  noahsark verify /mnt/ark
  sudo umount /mnt/ark
```

Every step is `commit`, then `pack`, then `noahsark status` and do what
it prints. `status` always ends with one `next:` block, with real paths,
that you can paste line by line. Commit as often as you want; a later
commit stages only new data. Pack when the `staged:` bytes come near one
disc, or at a fixed interval, for example one month.

Run each line of that block. `image build` and `growisofs` print their
own output; `disc burned` and `verify` print what changes in the
repository:

```
$ noahsark disc burned 0
disc 0 "2026-09-14 disc 0": marked burned, 8 item(s) marked
$ noahsark verify /mnt/ark
disc 0 "2026-09-14 disc 0": 8 items, ok
verify: copy 1 of 2 verified; verify the second copy before you free space
next: noahsark status
```

Run `status` again. The disc is now `verified 1/2`, and its `next:`
block has changed: it no longer builds an image or marks a burn, only
the second burn of the same `tree.img`, its mount, its verify, and its
unmount:

```
$ noahsark status
staged: 0 items, 0 bytes
disc 0 "2026-09-14 disc 0"  verified 1/2  4a060bd4-ca9f-2d06-263e-b907483b8230
next:
  growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=/srv/ark/repo/staging/plans/4a060bd4.../tree.img
  sudo mount /dev/sr0 /mnt/ark
  noahsark verify /mnt/ark
  sudo umount /mnt/ark
```

Load the second blank disc and run that block. Do not pack again and do
not run `disc burned` again; the second copy needs neither.

```
$ noahsark verify /mnt/ark
disc 0 "2026-09-14 disc 0": 8 items, ok
verify: 2 of 2 copies verified
$ noahsark status
staged: 0 items, 0 bytes
disc 0 "2026-09-14 disc 0"  verified  4a060bd4-ca9f-2d06-263e-b907483b8230
next: nothing to do
```

Write the disc number, the first 8 characters of the uuid, and `A` or `B`
on each sleeve. Store copy B in a different building.

## 3. Restore

`noahsark log` lists your snapshots. Do a restore drill now, and again
every few months.

**One file or one folder.**

```
$ noahsark ls --recursive 2026-09-14
srv/data/notes.txt
srv/data/photos/
srv/data/photos/2026-09-20.jpg
$ noahsark restore --include=srv/data/photos /mnt/ark 2026-09-14 /srv/drill
restored snapshot 12201b03... into /srv/drill
```

Copy a path straight out of `ls` into `--include`; the two use the same
text. Restore writes the content of the source root directly into
`OUT-DIR`, so `/srv/drill/notes.txt`, not a copy of `/srv/data` nested
inside it.

**Everything, with every disc mounted.**

```
$ noahsark restore /mnt/a /mnt/b 2026-09-14 /srv/restore
restored snapshot 12201b03... into /srv/restore
```

**One drive, several discs.** Give `--mount` and keep a second terminal
open to swap discs in.

```
$ noahsark restore --mount=/mnt/ark 2026-09-14 /srv/restore
disc 0 "2026-09-14 disc 0" (4a060bd4-...): 8 items, 3001350 bytes
totals: 1 disc, 8 items, 3001350 bytes
disc 0 "2026-09-14 disc 0": found
restored snapshot 12201b03... into /srv/restore
```

**After the computer is lost.** You have discs and a blank machine, no
repository. `restore --mount` with no repository builds one for you,
from the discs it reads, before it restores.

```
$ noahsark restore --mount=/mnt/ark 2026-09-14 /srv/restore
no repository given: building one at /srv/ark/repo from the discs you insert
disc 0 "2026-09-14 disc 0" (4a060bd4-...): found
disc 1 "2026-09-21 disc 1" (cb3bebe8-...): insert into /mnt/ark and press Enter
restored snapshot 12201b03... into /srv/restore
```

This asks for each disc one time, oldest first, and never asks twice.

## 4. When something goes wrong

| What you see | What to type |
|---|---|
| A burn fails, before `disc burned` ran | Discard the disc. Burn a new one from the same tree.img. |
| A burn fails, after `disc burned` ran | `noahsark disc burned --undo 0`, then burn a new disc, then `noahsark disc burned 0`. |
| The mark exists, the disc mounts, `verify` fails, and no copy of this disc has ever verified ok | `verify` removed the mark for you. Discard the disc, burn a new one from the same tree.img, run `noahsark disc burned 0`, then `noahsark verify`. |
| The mark exists, the disc does not mount, and no copy of this disc has ever verified ok | Same as above: the disc is bad, not merely unmounted. |
| A disc that already has one good copy fails a later `verify` | `verify` prints `disc 0 "2026-09-14 disc 0": bad; this copy is bad, the other copy is still good; burn a new copy, then verify it.` The good copy's mark stays; burn a new copy of the same disc, then `noahsark verify` it. |
| `image build`: refuses an existing image file | Add `--force` to rebuild it. |
| A disc is destroyed and you will never mount it again | `noahsark disc lost 0`. |
| `restore`: `missing disc(s)` | Give every disc it names, or use `--mount`. |
| `restore`: a path is already there | Add `--overwrite`, or restore into an empty directory. |
| Repository directory is gone, discs are not | `noahsark recover --repo=/srv/ark/repo --source=/srv/data /mnt/discs/*`, one call for every disc, in any order. |

A failed `verify` is honest about what changed. Before any copy of a
disc has verified ok, a failed `verify` removes the burn mark for you;
you do not run `disc burned --undo` yourself for that case. Once one
copy has verified ok, a failed `verify` never removes that record: the
good copy's mark stays, and only the new, bad copy needs a new burn.

### A disc is lost

```
$ noahsark disc lost 0
disc 0 "2026-09-14 disc 0": marked lost, 8 item(s) will be re-staged if the source still holds them
next: noahsark commit
$ noahsark commit
snapshot ...
new items: 8, existing items: 0
staged: 8 items, 3001350 bytes
next: noahsark pack
```

`disc lost` never deletes a record. It tells the tool: stop trusting this
disc. The next `commit` reads the source again and re-stages any item
that only this disc held, exactly as it would for a changed file.

### A disc is missing during recover

You are rebuilding a lost repository (section "After the computer is
lost", or `recover` directly) and one disc that another disc's tables
name has not been given yet:

```
$ noahsark recover --repo=/srv/ark/repo --source=/srv/data /mnt/ark
recover: disc cb3bebe8-ca9f-2d06-263e-b907483b8230 (2026-09-21 disc 1) named by another disc, not yet given
$ noahsark status
staged: 0 items, 0 bytes
disc 0 "2026-09-14 disc 0"  on disc only  4a060bd4-ca9f-2d06-263e-b907483b8230
disc 1 "2026-09-21 disc 1"  missing  cb3bebe8-ca9f-2d06-263e-b907483b8230
next: give disc 1 to noahsark recover, or run: noahsark disc lost 1
```

Give disc 1 to `recover` when you find it. If it is truly gone, run
`noahsark disc lost 1`; the next `commit` re-stages what only it held.
`commit` and `pack` refuse to run while a disc is `missing`, so that
their staged counts never disagree with `status`.

## 5. Free space

```
$ noahsark gc --dry-run
gc: would free 8 item(s), 3001350 bytes; disc 0 is verified 2/2, 7 days old
$ noahsark gc
gc: freed 8 item(s), 3001350 bytes
```

`gc` frees a staged item only after both copies of its disc verify ok and
7 days pass. It never frees data that no disc has confirmed yet, and the
cache it reads to confirm that is never trimmed. `gc --force-after=1h`
shortens the 7-day wait for this one run; it still asks you to confirm
before it deletes anything.

## 6. Options

- **FEC.** Off by default. Two discs are the redundancy. Set
  `fec.scheme = rs255-gf8` in the config to add repair data on every
  pack, about 9% of the disc, so `verify --heal --out=DIR` can rebuild a
  disc root from a damaged copy. Burn `DIR` to a new disc, then run a
  plain `verify` of that disc; healing on the hard disk is never a
  counted copy.
- **Close.** `pack --close` prints a burn line that seals the disc. This
  is permanent. The default burn stays open.
- **Capacity.** `dvd+r`, `dvd-r`, `bd25`, `bd50`, `bd100`, `bd128`, or a
  size with a unit, for example `23GiB`.
- **Pack output.** `pack --out=DIR` writes the disc root into `DIR`
  instead of the repository. Free space (`gc`) never looks inside a
  directory outside the repository, so a `DIR` of your own is yours to
  keep or delete.
- **Excludes.** `commit --exclude=PATTERN`, repeatable, or a
  `.noahsarkignore` file in the source root.
- **Rehearsal without a drive.** Use the packed `tree` directory in place
  of a mount point everywhere in this guide. To test the image file too:
  `sudo mount -o loop -t udf tree.img /mnt/ark`, then `noahsark verify
  /mnt/ark`. A verify of the packed `tree` still checks every byte, but
  it prints `not counted: this is not a disc` and never advances a copy
  count. A loop mount of the built image is a real, read-only mount, so
  it does count. Throw away a rehearsal repository (`init` a scratch one
  under `/tmp`) once you trust the drill.

## Command reference

| Command | Flags | What it does |
|---|---|---|
| `init` | `--repo` `--source` `--capacity` `--device` | Create a repository. Writes source, capacity, and device once. |
| `commit` | `--ref` `-m` `--exclude` `--one-file-system` | Stage the source as a new snapshot. |
| `pack` | `--capacity` `--out` `--close` `--dry-run` | Write staged items into a disc root, ready to burn. |
| `image build` | `--out` `--force` | Build a UDF image from a disc root. `--out` defaults to `TREE-DIR` with `.img`. |
| `disc burned` | `--undo` | Record that a burn happened, or undo that record. |
| `disc lost` | none | Declare a disc gone for good; re-stage what only it held. |
| `verify` | `--heal` `--out` | Check a disc root, or repair one with FEC into `--out`. |
| `status` | none | Print staged totals, each disc's state, and the one `next:` block to run. |
| `gc` | `--dry-run` `--force-after` | Free staged copies of items that are safely on disc. |
| `restore` | `--include` `--overwrite` `--mount` `--dry-run` | Write a snapshot's files into `OUT-DIR`. |
| `recover` | `--source` | Rebuild a lost repository from discs. Always give `--source`; the capacity comes from the discs. |
| `ls` | `--recursive` `--long` | List the paths of a snapshot, pasteable into `--include`. |
| `log` | none | List snapshots and refs, newest first. |

Global: `--repo=PATH`, `-q` / `--quiet` (no progress line), `-h` (help),
`--version`.
