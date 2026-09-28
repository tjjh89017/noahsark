# Operator guide (draft)

This is a design draft for a new `noahsark`. It is the specification.
Code follows it later. Part 1 is the full cycle. Part 2 is a reference.
`noahsark COMMAND -h` lists all flags of a command. A line that starts
with `$` is a command you type. The lines below it are its output. A
block with no `$` is a set of lines to paste. `/srv/ark/repo` is the
repository, `/srv/data` is the source, `2026-09-14` is a ref, and
`/mnt/ark` is a disc mount point. Put your own values in their place.

## What it is

`noahsark` copies your files to write-once discs, so that a fire or a
theft or a ransom does not end your data. It never deletes data that no
verified disc holds, and it never needs the tool itself to read a disc
back.

The tool tracks one verified disc for each pack. When that disc is
verified and 7 days pass, `gc` frees the copy in the repository. From
then on, that disc is the only copy that the tool knows. One disc with
FEC off has no redundancy. A second copy, in a different building, is
your job. See "A second copy".

## Words

- **Item**: one piece of your data. Equal data is stored one time.
- **Snapshot**: the state of the source at one commit.
- **Ref**: your name for a snapshot. Use the date.
- **Staged**: committed and held in the repository, not yet on a disc.
- **Disc**: the content of one pack. Its copies share one uuid. The uuid
  is its exact name.
- **Copy**: one physical write-once disc that holds a disc. The tool
  records one verified copy. A second copy is yours to keep.
- **Disc root**: a directory that holds `NOAHSARK/`: a packed tree, or a
  mounted disc.

`status` shows the state of each disc in one more word: `packed`,
`burned`, `verified`, `on disc only`, `lost`, or, only while a lost
repository is being rebuilt, `missing`. After `verified` and `on disc
only`, it adds `, last check DATE` after a good check, `, last check
failed DATE` after a failed check, or `, not checked` after `disc
verified`.

## 1. Set up, one time

You need Go 1.27 or later, a DVD or Blu-ray writer, write-once media,
`dvd+rw-tools` 7.1-14 or later, and `udftools` 2.3 or later (`mkudffs`).
From `dvd+rw-tools` you use `growisofs` to burn a disc, and
`dvd+rw-mediainfo` to read the capacity of a blank disc. GNU
`ddrescue` copies a disc to an image. You need it only for a second
copy after `gc`. Build in the
source checkout, and install the binary in `/usr/local/bin`. `sudo` does
not search a directory in your home directory.

```
$ go build -o noahsark ./cmd/noahsark
$ sudo install -m 0755 noahsark /usr/local/bin/
$ sudo usermod -aG cdrom $USER      # then log in again
$ sudo mkdir -p /srv/ark/repo && sudo chown "$USER": /srv/ark/repo
$ cd /srv/ark/repo
$ noahsark init --source=/srv/data
initialized repository /srv/ark/repo
source: /srv/data
device: /dev/sr0
next: make the first backup now, run: noahsark commit
$ export NOAHSARK_REPO=/srv/ark/repo
```

`init` makes the current directory the repository. It writes the source
into the repository. You never edit the config file by hand. `init` does
not ask for a capacity: you give the capacity at each `pack`, because
each blank disc can differ. Give `--device=PATH` when your writer is not
`/dev/sr0`. `noahsark` prints this path in commands for you to run, and
never runs it itself.

`noahsark` finds the repository from the current directory and its
parents, or from `NOAHSARK_REPO`. Whether the guide keeps the `export`
line is not decided. See `open-questions.md`, "Find the repository".

## 2. Back up

Make the first commit right after `init`:

```
$ noahsark commit
snapshot 1b03c7e2a9f4
ref 2026-09-14 -> 1b03c7e2a9f4
new items: 8, existing items: 0
unstable: 0, skipped: 0
staged: 8 items, 3001350 bytes
next: noahsark status
```

Commit as often as you want. A later commit stages only new data. Pack
when the `staged:` bytes come near one disc, or at a fixed interval, for
example one month.

Then run `noahsark status` and do what it prints. `status` always ends
with one `next:` block, with real paths. The lines of a block are joined
with `&&`, so a failed line stops the lines after it. Two blocks have
a second part that is not joined: the `pack` line that you complete,
and the `disc lost` line for a `missing` disc. Paste the lines under
`next:` as they are.

```
$ noahsark status
staged: 8 items, 3001350 bytes
next: load a blank disc, then run:
  dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Free Blocks'
then paste this line, type the capacity, and press Enter:
  noahsark pack --capacity=
```

The tool does not know the capacity of a blank disc, so you type it.
`Mounted Media` names the media type. `Free Blocks` gives the size in
blocks of 2048 bytes. Type a media name, or a size with a unit (see
"Options"):

```
$ dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Free Blocks'
 Mounted Media:         41h, BD-R SRM
 Free Blocks:           12219392*2KB
$ noahsark pack --capacity=bd25
packed disc 0 "2026-09-14 disc 0": 8 item(s), 3001350 bytes
uuid: 4a060bd4-ca9f-2d06-263e-b907483b8230
next: noahsark status
```

When you gave the wrong capacity, undo the pack before you burn
anything, then pack again. The disc number is not used again. The new
pack gets the next number:

```
$ noahsark pack --undo 0
disc 0 "2026-09-14 disc 0": pack undone, 8 item(s) returned to staged
next: noahsark status
$ noahsark pack --capacity=dvd+r
packed disc 1 "2026-09-14 disc 1": 8 item(s), 3001350 bytes
uuid: 9c1e5f27-0b44-4d8e-a1f3-6d2b8e0c7a15
next: noahsark status
```

`pack --undo` works only for the newest disc, and only while it is
`packed`, that is, before any burn is recorded. It removes the disc root
in the repository. It keeps a disc root that you gave with `pack
--out`. The rest of this guide shows disc 0, packed with the right
capacity the first time.

### Burn and verify

You build one image and burn it to a blank disc. `status` prints the
block:

```
$ noahsark status
staged: 0 items, 0 bytes
disc 0 "2026-09-14 disc 0"  packed  4a060bd4-ca9f-2d06-263e-b907483b8230
next: load a blank disc, then run:
  sudo noahsark image build /srv/ark/repo/staging/plans/4a060bd4.../tree &&
  growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=/srv/ark/repo/staging/plans/4a060bd4.../tree.img &&
  eject /dev/sr0 && eject -t /dev/sr0 && sleep 5 &&
  sudo mkdir -p /mnt/ark && sudo mount -o ro /dev/sr0 /mnt/ark &&
  noahsark verify /mnt/ark &&
  sudo umount /mnt/ark && eject /dev/sr0
```

The block does these steps:

1. Build the image, and burn it. `image build` and `growisofs` print
   their own output.
2. Eject the disc and load it again, so that the read comes from the
   disc. Wait 5 seconds for the drive. Mount the disc read-only. A
   slot-load drive cannot load a disc by itself: `eject -t` fails and
   stops the block. Push the disc in by hand, then paste the lines
   after `sleep 5`.
3. Verify the disc. `verify` reads every item back. When the read is ok,
   `verify` records the burn and marks the disc `verified`. You do not
   run `disc burned` for it.
4. Unmount the disc, and eject it.

`verify` prints what changes in the repository:

```
$ noahsark verify /mnt/ark
disc 0 "2026-09-14 disc 0": 8 items, ok
burn recorded; verified
next: noahsark status
$ noahsark status
staged: 0 items, 0 bytes
disc 0 "2026-09-14 disc 0"  verified, last check 2026-09-14  4a060bd4-ca9f-2d06-263e-b907483b8230
advice: burn a second copy of /srv/ark/repo/staging/plans/4a060bd4.../tree.img before gc; see the guide, "A second copy"
next: nothing to do; gc can free disc 0 after 2026-09-21
```

Write the disc number, the first 8 characters of the uuid, and `A` on
the sleeve.

### Check a disc again

Run `noahsark verify` on a disc again at any time, for example one time
each year. A good check changes no state. It logs the date, and
`status` shows it as `last check`. When a check fails, see "When
something goes wrong".

### Undo a verify

`noahsark verify --undo 0` removes the verified record of disc 0, and
keeps its burn record. Use it when you verified a disc by mistake, or
when you see later that a `verified` disc is bad. It shows what changes,
and asks before it changes anything:

```
$ noahsark verify --undo 0
warning: disc 0 "2026-09-14 disc 0" (4a060bd4-ca9f-2d06-263e-b907483b8230): verified -> burned
the disc is no longer verified, and gc holds its data
Continue? [y/N] y
disc 0 "2026-09-14 disc 0": verified record removed; burn record kept
next: noahsark status
```

Only `y` or `yes` continues. Any other answer, or an empty line, prints
`nothing changed`, and the command exits 1. After a `y`, the disc is
`burned`, and `status` verifies it again. When the disc is bad, discard
it and run `noahsark disc burned --undo 0`. `status` then burns a new
disc from the same `tree.img`. `verify --undo` refuses a disc that is
`on disc only`, because `gc` already freed the repository copy.

### A second copy

`gc` frees the repository copy after one verified disc and 7 days. One
disc with FEC off has no redundancy: one bad spot can lose data. Keep a
second copy in a different building. The tool records nothing about
the second copy. It is your job.

Before `gc`, burn the same `tree.img` to a second blank disc, and check
it with `verify --no-mark`. Load a blank disc and paste:

```
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=/srv/ark/repo/staging/plans/4a060bd4.../tree.img &&
eject /dev/sr0 && eject -t /dev/sr0 && sleep 5 &&
sudo mount -o ro /dev/sr0 /mnt/ark &&
noahsark verify --no-mark /mnt/ark &&
sudo umount /mnt/ark && eject /dev/sr0
```

After `gc`, the `tree.img` is gone. Copy a good disc to an image, then
burn that image. Use the same lines to replace a copy that fails a
check, or a copy that is destroyed. Load the good disc and paste:

```
ddrescue -b 2048 -n -r1 /dev/sr0 ~/copy.img ~/copy.map && eject /dev/sr0
```

Then load a blank disc and paste:

```
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=$HOME/copy.img &&
eject /dev/sr0 && eject -t /dev/sr0 && sleep 5 &&
sudo mount -o ro /dev/sr0 /mnt/ark &&
noahsark verify --no-mark /mnt/ark &&
sudo umount /mnt/ark && eject /dev/sr0 &&
rm ~/copy.img ~/copy.map
```

Write `B` on the sleeve of the second copy. Store it in a different
building from copy `A`.

### Record a burn without a verify

`noahsark disc burned 0` records the burn and reads nothing. Use it when
you cannot verify now. `status` then shows `burned`, and its `next:`
block verifies the disc later. `noahsark disc burned --undo 0` removes a
burn record that `disc burned` made by mistake.

`verify --no-mark` checks a disc and records nothing. Use it for a
second copy, also before you verify the first disc. When the disc has
no burn record yet, it prints the `disc burned` line with the disc
number, so you never need to look the number up:

```
$ noahsark verify --no-mark /mnt/ark
disc 0 "2026-09-14 disc 0": 8 items, ok
not marked; to record this burn, run: noahsark disc burned 0
```

### Mark a disc verified without a check

`noahsark disc verified 0` marks a `burned` disc `verified` on your
word, and reads no disc. Use it only when you cannot read the disc on
this machine now, and you know that it is good. `gc` then frees the
repository copy after the wait time, as for a checked disc. It warns,
and asks before it changes anything:

```
$ noahsark disc verified 0
warning: disc 0 "2026-09-14 disc 0" (4a060bd4-ca9f-2d06-263e-b907483b8230): burned -> verified
the tool did not read this disc; gc frees the repository copy of its data after the wait time; if the disc is bad, that data is lost
Continue? [y/N] y
disc 0 "2026-09-14 disc 0": verified record added; not checked
next: noahsark status
```

Only `y` or `yes` continues. Any other answer, or an empty line, prints
`nothing changed`, and the command exits 1. `status` shows the disc as
`verified, not checked` until a good `verify` of the disc. `status`
never prints `disc verified` in a block. `noahsark verify --undo 0`
removes the record.

### Burn the folder directly

Not yet decided. `FORMAT.md` requires a pure UDF 2.01 volume built with
`mkudffs`. A `growisofs` burn of a folder builds an ISO 9660 volume with
a UDF bridge. See `open-questions.md`, "Burn the folder directly".

*... Days pass. You add, change, and delete files in `/srv/data`. Each
cycle is `commit`, then `status` and its `next:` block.*

## 3. Restore

`noahsark log` lists your snapshots. Do a restore drill now, and again
every few months.

`restore` takes its flags first, then the snapshot, then zero or more
paths inside the snapshot, then the destination last. Give each mounted
disc with `--disc`. A path follows the `rsync` rule for a trailing
slash:

- `srv/data/photos` makes the directory `DEST/photos`.
- `srv/data/photos/` puts the content of `photos` directly into `DEST`.

With no path, `restore` writes the content of the source root directly
into `DEST`: `/srv/restore/notes.txt`, not a copy of `/srv/data` nested
inside it.

**One file or one folder.**

```
$ noahsark ls -R 2026-09-14
srv/data/notes.txt
srv/data/photos/
srv/data/photos/2026-09-20.jpg
$ noahsark restore --disc=/mnt/ark 2026-09-14 srv/data/photos /srv/drill
restored snapshot 1b03c7e2a9f4 into /srv/drill
```

`ls -R` lists every path of the snapshot, in each directory down to the
last level. Copy a path straight out of `ls` into the `restore` line. The two use
the same text. This example makes `/srv/drill/photos/2026-09-20.jpg`.
Whether `ls` prints paths from the source root (`photos/`) in place of
`srv/data/photos/` is not decided. See `open-questions.md`, "Paths that
ls prints".

**Everything, with every disc mounted.**

```
$ noahsark restore --disc=/mnt/a --disc=/mnt/b 2026-09-14 /srv/restore
restored snapshot 1b03c7e2a9f4 into /srv/restore
```

**One drive, several discs.** Give `--mount` and keep a second terminal
open to swap discs in.

```
$ noahsark restore --mount=/mnt/ark 2026-09-14 /srv/restore
disc 0 "2026-09-14 disc 0" (4a060bd4-...): 8 items, 3001350 bytes
totals: 1 disc, 8 items, 3001350 bytes
disc 0 "2026-09-14 disc 0": found
restored snapshot 1b03c7e2a9f4 into /srv/restore
```

**After the computer is lost.** You have discs and a blank machine, no
repository. The draft lets `restore --mount` with no repository build
one from the discs it reads, before it restores. This rule is not
decided. See `open-questions.md`, "Recover and restore --mount".

```
$ noahsark restore --mount=/mnt/ark 2026-09-14 /srv/restore
no repository given: building one at /srv/ark/repo from the discs you insert
disc 0 "2026-09-14 disc 0" (4a060bd4-...): found
disc 1 "2026-09-21 disc 1" (cb3bebe8-...): insert into /mnt/ark and press Enter
restored snapshot 1b03c7e2a9f4 into /srv/restore
```

This asks for each disc one time, oldest first, and never asks twice.

## 4. When something goes wrong

| What you see | What to type |
|---|---|
| You packed with the wrong capacity, and no burn is recorded | `noahsark pack --undo 0` for the newest disc, then `noahsark status` and pack again. The new pack gets the next disc number. |
| A burn fails, and no burn is recorded | Discard the disc. When `growisofs` says that the image does not fit, the capacity was too large: run `noahsark pack --undo 0`, then pack again. Else run `noahsark status` and paste its block with a new blank disc. |
| A burn fails, after you ran `disc burned` | `noahsark disc burned --undo 0`, then `noahsark status` and its block with a new blank disc. |
| The disc mounts, `verify` fails, and the disc was not yet `verified` | `verify` recorded nothing, and removed a `disc burned` record if one was there. Discard the disc. `noahsark status` burns a new one from the same `tree.img`. |
| The disc does not mount, and it was not yet `verified` | The disc is bad. Discard it. If you ran `disc burned` for it, run `noahsark disc burned --undo 0`. Then `noahsark status`. |
| A `verified` disc fails a later `verify`, before `gc` | `verify` prints `disc 0 "2026-09-14 disc 0": bad; this disc is bad; verified record removed; gc holds the data`. The disc is `burned` again, and `gc` holds the data. Discard it. `noahsark status` burns a new disc from the same `tree.img`. |
| You verified a disc by mistake, or you see later that a `verified` disc is bad, before `gc` | `noahsark verify --undo 0`, and answer `y`. The disc is `burned` again. When the disc is bad, discard it, run `noahsark disc burned --undo 0`, then `noahsark status`. |
| An `on disc only` disc fails a later `verify` | The repository copy is already freed. Copy the disc now, while it still reads, or use your second copy. See "A second copy". When no copy can be read, run `noahsark disc lost 0 && noahsark commit`. |
| You cannot read a `burned` disc on this machine now, and you know that it is good | `noahsark disc verified 0`, and answer `y`. Verify the disc later, when you can. |
| `image build`: refuses an existing image file | Add `--force` to build it again. |
| Every copy of a disc is destroyed, and you will never mount one again | `noahsark disc lost 0`. |
| You find a disc that you marked lost | `noahsark disc lost --undo 0`, and answer `y`. A disc that was `verified` is `burned` again. Paste the block that it prints to verify the disc. |
| `status` says that a disc has no disc root | The disc was lost and found, and then its check failed, or its burn record was removed. No new disc can be burned from it. Discard it, and paste the block: `noahsark disc lost 0`. |
| `restore`: `missing disc(s)` | Give every disc it names with `--disc`, or use `--mount`. |
| `restore`: a path is already there | Add `--overwrite`, or restore into an empty directory. |
| Repository directory is gone, discs are not | `noahsark --repo=/srv/ark/repo recover --source=/srv/data /mnt/ark`. With one drive, run it one time for each disc, in any order. With several drives, give every mounted disc in one call. |

A failed `verify` is honest about what changed. It removes one record:
the verified record first, else the burn record. The disc state goes
down one step, and `gc` holds the data. You do not run `disc burned
--undo` yourself for a disc that failed `verify`.

### Undo

| Step | Undo |
|---|---|
| `commit` | None. A snapshot stays. `gc` frees only what a verified disc holds. |
| `pack` | `noahsark pack --undo SEQ`, for the newest disc while it is `packed`. |
| `image build` | None needed. `--force` builds the image again. |
| `disc burned` | `noahsark disc burned --undo SEQ`, while the disc is `burned`. |
| `verify` | `noahsark verify --undo SEQ`, while the disc is `verified`. It asks `Continue? [y/N]`. |
| `disc verified` | `noahsark verify --undo SEQ`. It removes the verified record, whatever made it: `verify` or `disc verified`. |
| `disc lost` | `noahsark disc lost --undo SEQ`, when the disc was `verified`, `on disc only`, or `missing` before `disc lost`. A `verified` disc comes back as `burned`, and needs a new `verify`. It asks `Continue? [y/N]`. |
| `gc` | None. It frees only data that a verified disc holds. |

### A disc is lost

```
$ noahsark disc lost 0
disc 0 "2026-09-14 disc 0": marked lost; 8 item(s) need a new commit
next: noahsark commit
$ noahsark commit
snapshot 5e9a02d41c7b
ref 2026-10-12 -> 5e9a02d41c7b
new items: 8, existing items: 0
unstable: 0, skipped: 0
staged: 8 items, 3001350 bytes
next: noahsark status
```

`disc lost` never deletes a record. It tells the tool to stop trusting
this disc. An item whose staged file still exists goes back to staged
at once, and the next `pack` takes it again. Only an item that `gc`
already freed needs the new `commit`. That `commit` reads the source
again and stages each such item that the source still holds, as it does
for a changed file.

### A lost disc is found

`noahsark disc lost --undo 0` makes the tool trust disc 0 again after
you find it. It shows what changes, and asks before it changes
anything. In this example, disc 0 was `verified` when you marked it
lost:

```
$ noahsark disc lost --undo 0
warning: disc 0 "2026-09-14 disc 0" (4a060bd4-ca9f-2d06-263e-b907483b8230): lost -> burned
the tool trusts this disc again only after a good check; you must run verify on it
Continue? [y/N] y
disc 0 "2026-09-14 disc 0": lost mark removed; 8 item(s) back on this disc; verify it now
next: load disc 0, then run:
  eject /dev/sr0 && eject -t /dev/sr0 && sleep 5 &&
  sudo mkdir -p /mnt/ark && sudo mount -o ro /dev/sr0 /mnt/ark &&
  noahsark verify /mnt/ark &&
  sudo umount /mnt/ark && eject /dev/sr0
```

Only `y` or `yes` continues. Any other answer, or an empty line, prints
`nothing changed`, and the command exits 1.

After a `y`, a disc that was `verified` is `burned`, not `verified`.
Nobody checked the disc after you found it, so `gc` frees nothing of it
until a good `verify`. An item that went back to staged, and that no
later `pack` took, belongs to this disc again. An item that a later
`pack` took stays on its new disc too. The same data on two discs does
no harm. Paste the block. A good `verify` marks the disc `verified`, as
for any burned disc. `status` also shows the disc as `burned`, and its
block gives the same lines. When the check fails, see "When something
goes wrong".

A disc that was `on disc only` goes back to `on disc only`. The
repository holds none of its data, so `gc` has nothing to free. An item
that the `commit` after `disc lost` staged again stays staged, and the
next `pack` takes it. The command prints the same block. There, the
`verify` is a check that changes no state.

`disc lost --undo` refuses a disc that was `packed` or `burned` when
you marked it lost. `disc lost` removed its disc root, and the next
`pack` takes its items. Discard that disc.

### A disc is missing during recover

You rebuild a lost repository ("After the computer is lost", or
`recover` directly). Another disc's tables name a disc that you did not
give yet:

```
$ noahsark --repo=/srv/ark/repo recover --source=/srv/data /mnt/ark
recover: disc 1 "2026-09-21 disc 1" (cb3bebe8-ca9f-2d06-263e-b907483b8230) named by another disc, not yet given
$ noahsark status
staged: 0 items, 0 bytes
disc 0 "2026-09-14 disc 0"  on disc only  4a060bd4-ca9f-2d06-263e-b907483b8230
disc 1 "2026-09-21 disc 1"  missing  cb3bebe8-ca9f-2d06-263e-b907483b8230
next: load disc 1 "2026-09-21 disc 1", then run:
  sudo mkdir -p /mnt/ark && sudo mount -o ro /dev/sr0 /mnt/ark &&
  noahsark recover --source=/srv/data /mnt/ark &&
  sudo umount /mnt/ark
or, when disc 1 is gone for good, run:
  noahsark disc lost 1
```

Give disc 1 to `recover` when you find it. If it is gone for good, run
`noahsark disc lost 1`. The next `commit` stages again what only it
held. `commit` and `pack` refuse to run while a disc is `missing`, so
that their staged counts never disagree with `status`.

## 5. Free space

```
$ noahsark gc --dry-run
gc: would free 8 item(s), 3001350 bytes; disc 0 is verified, 7 days old
$ noahsark gc
gc: freed 8 item(s), 3001350 bytes
```

`gc` frees a staged item after its disc is `verified` and 7 days pass.
It also removes the disc root and `tree.img` of that disc. After `gc`,
the verified disc is the only copy that the tool knows. Burn your
second copy before this step. See "A second copy". `gc` never frees data
that no verified disc holds, and the cache it reads to confirm that is
never trimmed. `gc --force-after=1h` shortens the 7-day wait for this
one run.

## 6. Options

- **FEC.** Off by default. FEC is a choice for each disc: add `--fec`
  to the `pack` line, after you type the capacity. `pack --fec` adds
  repair data to this disc only:

  ```
  $ noahsark pack --capacity=bd25 --fec
  ```

  Two discs of one repository can differ: one with FEC, one without.
  The repair data uses part of the capacity: in each stripe of 255
  blocks, 231 hold data, and 24 hold the checksum and the parity. Thus a
  disc with FEC holds less data. `status` shows `fec` on the line of
  each disc that has FEC:

  ```
  disc 2 "2026-10-12 disc 2"  packed  fec  0d7f3a61-5b2e-4c19-8e44-2a9c6b1f0e73
  ```

  The disc itself records its FEC setting, so `verify` needs no
  setting from you. `verify --heal --out=DIR` rebuilds a disc root from
  a damaged disc that has FEC. It refuses a disc without FEC. Burn `DIR`
  to a new disc, then run a plain `verify` of that disc. A healed tree
  on the hard disk is never a verified disc.
- **Close.** `pack --close` makes the burn line that `status` prints
  seal the disc. The repository stores this choice. A sealed disc is
  permanent. The default burn stays open.
- **Capacity.** `pack --capacity` takes `dvd+r`, `dvd-r`, `bd25`, `bd50`,
  `bd100`, `bd128`, or a size with a unit, for example `23GiB`.
  `dvd+rw-mediainfo /dev/sr0` shows the `Free Blocks` of a blank disc.
  Each block is 2048 bytes.
- **Pack output.** `pack --out=DIR` writes the disc root into `DIR`
  instead of the repository. `gc` and `pack --undo` never touch a
  directory outside the repository, so a `DIR` of your own is yours to
  keep or delete.
- **Excludes.** `commit --exclude=PATTERN`, repeatable, or a
  `.noahsarkignore` file in the source root.

## Command reference

```
noahsark [GLOBAL-OPTIONS] COMMAND [SUBCOMMAND...] [COMMAND-OPTIONS] [ARGUMENTS]
```

A global option comes before the command name. A command option comes
after the command name and before the arguments. In a command group,
the command options come after the last subcommand word:
`noahsark disc burned --undo 0`. An option in the wrong position is a
usage error. The tool exits 2 and names the correct position. `-h` is
the one exception: it works before the command name and after it.

### Global options

| Option | What it does |
|---|---|
| `--repo=PATH` | Name the repository. Without it, the tool uses `NOAHSARK_REPO`, then the current directory and its parents. `init` refuses it: `init` makes the current directory the repository. |
| `-q`, `--quiet` | Print no progress line. |
| `-h` | Print help and exit. `noahsark -h` lists the commands and the groups. `noahsark COMMAND -h` and `noahsark -h COMMAND` print the help of that command, or the subcommands of a group. |
| `--version` | Print the version and exit. |

### Commands

| Command | Command options | What it does |
|---|---|---|
| `init` | `--source` `--device` | Make the current directory a repository. Writes source and device once. |
| `commit` | `--ref` `-m` `--exclude` `--one-file-system` | Stage the source as a new snapshot. |
| `pack` | `--capacity` `--out` `--close` `--fec` `--dry-run` `--undo` | Write staged items into a disc root, ready to burn. `--capacity` is required. `--fec` adds repair data to this disc. `--undo SEQ` returns the newest packed disc's items to staged. |
| `verify` | `--no-mark` `--heal` `--out` `--undo` | Check a disc root. A good check of a mounted disc marks it `verified`, or logs the check when it is already `verified`. `--no-mark` records nothing. `--heal` repairs a disc that has FEC into `--out`. `--undo SEQ` removes the verified record, after a `y` answer. |
| `status` | none | Print staged totals, each disc's state, and the one `next:` block to run. |
| `gc` | `--dry-run` `--force-after` | Free staged copies of items that a verified disc holds. |
| `restore` | `--disc` `--mount` `--overwrite` `--dry-run` | `restore [COMMAND-OPTIONS] SNAPSHOT [PATH...] DEST`: write a snapshot's files into `DEST`. |
| `recover` | `--source` | Rebuild a lost repository from discs. Always give `--source`. |
| `ls` | `-R` / `--recursive` `--long` | List the paths of a snapshot, pasteable as `restore` paths. |
| `log` | none | List snapshots and refs, newest first. |

### Command groups

A group is a command that has subcommands. A group has no options of
its own, other than `-h`. `noahsark disc -h` lists the subcommands of
`disc`, one line for each. `noahsark disc burned -h` lists the options
of `disc burned`. A group with no subcommand, or with an unknown one,
is a usage error: the tool exits 2 and lists the subcommands.

**`disc`**

| Subcommand | Command options | What it does |
|---|---|---|
| `disc burned` | `--undo` | Record a burn without a verify, or undo that record. |
| `disc verified` | none | Mark a `burned` disc `verified` without a check, after a `y` answer. |
| `disc lost` | `--undo` | Declare a disc gone for good. Staged items go back to staged. Freed items wait for a `commit`. `--undo SEQ` trusts a found disc again, after a `y` answer. |

**`image`**

| Subcommand | Command options | What it does |
|---|---|---|
| `image build` | `--out` `--force` | Build a UDF image from a disc root. `--out` defaults to `TREE-DIR` with `.img`. |

Two points of this reference are not decided. See `open-questions.md`:
"Flags with no guide sentence" for some flags of `commit`, `pack`,
`image build`, `restore` and `ls`, and "Recover and restore --mount"
for the disc arguments of `recover`.
