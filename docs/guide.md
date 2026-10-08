# Operator guide

This guide walks you through NoahsArk with one optical drive, from the
set-up to a restore after a lost computer. Follow it from the start to the
end. It shows the common paths only. `OPERATIONS.md` and `docs/states.md`
are the specification: they give every option, every message and every
edge case. `noahsark COMMAND -h` lists the options of a command.

A line that starts with `$` is a command that you type. The lines below it
are its output. A block with no `$` is a set of lines to paste. The
examples use these values. Put your own values in their place:

- `/srv/ark/repo` is the repository.
- `/srv/data` is the source that you back up.
- `/dev/sr0` is the drive.
- `/mnt/ark` is the mount point of a disc.
- `2026-09-14` is a ref.

## What the tool does

`noahsark` copies your files to write-once discs: BD-R, DVD+R and DVD-R.
Equal data is stored one time. Each disc carries the full description of
its format. Years later, the tool restores from the discs alone, without the
repository.

The tool counts one verified disc for each pack. When that disc is
verified, `gc` can free the copy in the repository. From then
on, that disc is the only copy that the tool knows. A second copy, in a
different building, is your job. See "A second copy".

`noahsark` never burns a disc, never mounts a disc and never runs `sudo`.
It prints the lines, and you run them.

## Words

- **Item**: one piece of your data. Equal data is stored one time.
- **Snapshot**: the state of the source at one commit.
- **Ref**: your name for a snapshot. By default, it is the date.
- **Staged**: committed and held in the repository, not yet on a disc.
- **Disc**: the content of one pack. The tool gives it a number, a label
  and a uuid. The uuid is its exact name.
- **Disc root**: a directory that holds `NOAHSARK/`: a packed tree in the
  repository, or a mounted disc.

`status` shows the state of each disc in one word or phrase:

| State | Meaning |
|---|---|
| `packed` | `pack` wrote the disc root. No burn is recorded. |
| `burned` | A burn is recorded. The disc is not verified yet. |
| `verified` | A good `verify` of the disc is recorded. The repository still keeps its data. |
| `on disc only` | `gc` freed the repository copy, or `recover` read the disc. |
| `lost` | You ran `disc lost`. The disc is out of the backup for good. |
| `missing` | Another disc names it during `recover`, and you did not give it yet. |

## Set up, one time

You need these:

- Go 1.27 or later, to build the tool.
- A DVD or Blu-ray writer, and write-once media.
- `dvd+rw-tools` 7.1-14 or later (Debian), or 7.1-13 or later (Fedora,
  Arch). You use `growisofs` to burn a disc and `dvd+rw-mediainfo` to read
  the capacity of a blank disc.
- `udftools` 2.3 or later. `image build` runs `mkudffs`.
- `eject`. Each block that `status` prints uses it.
- GNU `ddrescue` (the Debian package is `gddrescue`), to copy a disc to an
  image. You need it only for a second copy after `gc`.

Check the versions of the burn tools one time. `growisofs -version` does
not show the package revision, thus ask the package manager. On Fedora,
use `rpm -q`. On Arch, use `pacman -Q`.

```
$ dpkg-query -W dvd+rw-tools udftools
dvd+rw-tools	7.1-14+b2
udftools	2.3-2
```

Get the source, build the tool, and install it in `/usr/local/bin`.
`sudo` does not search a directory in your home directory.

```
$ git clone https://github.com/tjjh89017/noahsark.git && cd noahsark
$ go build -o noahsark ./cmd/noahsark
$ sudo install -m 0755 noahsark /usr/local/bin/
$ sudo usermod -aG cdrom $USER      # then log in again
```

Make the repository directory, go into it, and run `init`:

```
$ sudo mkdir -p /srv/ark/repo && sudo chown "$USER": /srv/ark/repo
$ cd /srv/ark/repo
$ noahsark init --source=/srv/data
initialized repository /srv/ark/repo
source: /srv/data
device: /dev/sr0
next: noahsark status
```

`init` makes the current directory the repository. It writes the source
root and the device into `config.yaml`. `init` does not ask for a
capacity: you give the capacity at each `pack`, because each blank disc
can differ.

With no `--source`, `init` prints no `source:` line.

The `device:` line names the drive that `noahsark` puts in the lines that
it prints for you. The tool never opens the device. When your writer is
not `/dev/sr0`, edit `pack.device` in `/srv/ark/repo/config.yaml`:

```yaml
pack:
  device: /dev/sr1
```

`noahsark` finds the repository from the current directory and its
parents. Run each command in `/srv/ark/repo`. In another directory, give
`--repo=/srv/ark/repo` before the command name.

**The repository and git.** You can keep the repository in git. `init`
writes a `.gitignore` file. Git then tracks the permanent part:
`config.yaml`, `state/` and `catalog/`. It ignores `lock` and `staging/`,
which hold only what `gc` frees. The tool never runs git. Never use git,
or any other tool, to put a file of `state/` or `catalog/` back to an old
version. The state logs are histories. An old version forgets events that
your discs already carry.

These uses of git are safe: commit `state/` and `catalog/` after a
command, push them to a remote as a copy, and clone them to read the
history. Do not check out an old commit in the repository. When `state/`
goes back, each command that changes state refuses with `state/ went back
to an older version`. Then check out the newest commit again.

## The normal cycle

### Commit

Make the first commit right after `init`. Before the first commit,
`status` says the same:

```
$ noahsark status
staged: 0 items, 0 bytes
next: noahsark commit
```

Run `commit`:

```
$ noahsark commit
snapshot 12201b03c7e2a9f483ca68be6227af2feb15f227485ed18aff8ecae99416a4bd6df3
ref 2026-09-14 -> 12201b03c7e2a9f483ca68be6227af2feb15f227485ed18aff8ecae99416a4bd6df3
new items: 8, existing items: 0
unstable: 0, skipped: 0
staged: 8 items, 3001350 bytes
next: noahsark status
```

`commit` prints the full snapshot id, so that a script can read it. The
other commands print a short id: the 12 characters after `1220`, as
`1b03c7e2a9f4`.

`commit` reads every file of the source and stages the new data. With no
`--ref`, it moves the ref named by the date of today. Give `--ref` for a
name of your own, and `-m` for a message that `log` shows. A ref name is 1
to 40 bytes of printable ASCII, with no space:

```
noahsark commit --ref=before-upgrade -m "before the OS upgrade"
```

Commit as often as you want. A later commit stages only new data. Pack
when the staged bytes come near the size of one disc, or at a fixed
interval, for example one month. To leave paths out, give
`--exclude=PATTERN`, or put the patterns in a `.noahsarkignore` file in
the source root. When the repository is inside the source, for example
with the source `$HOME`, `commit` leaves the repository and the staging
store out by itself and prints one `excluded` line for each.

### Run status, and paste its block

Each command that changes state ends with the line `next: noahsark status`.
That line is only a pointer. `status` is the one command that prints the
lines to run next. A command that changed nothing, because it refused or
because you answered no, prints no `next:` line. `restore`, `ls`, `log`,
`image build`, `--dry-run`, `verify --no-mark`, and a `verify` that is not
counted print none either.

`status` prints the staged total, one line for each snapshot that is not
complete on discs ("A snapshot packed in parts"), one line for each disc,
and one `next:` block. The block holds the lines to run next, with real paths. The lines
of a block are joined with `&&`, so a failed line stops the lines after
it. The unmount line follows a `;`, so the disc never stays mounted. Paste
the lines under `next:` as they are. A line that starts with `or` starts a
second method: paste the lines before it, or the lines after it, not both.

```
$ noahsark status
staged: 8 items, 3001350 bytes
snapshot 1b03c7e2a9f4: 8 items staged, not complete on discs; its snapshot object is not on a disc yet
next: load a blank disc, then run:
dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Free Blocks'
then paste this line, type the capacity, and press Enter:
noahsark pack --capacity=
```

The `snapshot` line is information. A new commit is not on a disc until
`pack` takes it, thus each commit gets this line until the next `pack`.

### Read the capacity, and pack

Load a blank disc, and paste the `dvd+rw-mediainfo` line:

```
$ dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Free Blocks'
 Mounted Media:         41h, BD-R SRM
 Free Blocks:           12219392*2KB
```

The tool does not know the capacity of a blank disc, so you type it.
`Mounted Media` names the media type. `Free Blocks` gives the size in
blocks of 2048 bytes. Type a preset name when `Free Blocks` is equal to
the preset or larger:

| Preset | Media | Blocks |
|---|---|---:|
| `dvd+r` | DVD+R 4.7 GB | 2,295,104 |
| `dvd-r` | DVD-R 4.7 GB | 2,298,496 |
| `bd25` | BD-R SL 25 GB | 12,219,392 |
| `bd50` | BD-R DL 50 GB | 24,438,784 |
| `bd100` | BD-R XL 100 GB | 48,878,592 |
| `bd128` | BD-R XL QL 128 GB | 62,500,864 |

When the drive shows fewer blocks than the preset, give a size with a
unit. Multiply the blocks by 2048 and round down. For example, 12088320
blocks are 24,756,879,360 bytes: type `24756MB`. A bare number without a
unit is refused.

```
$ noahsark pack --capacity=bd25
packed disc 0 "2026-09-14 disc 0": 8 item(s), 3001350 bytes
uuid: 4a060bd4-ca9f-2d06-263e-b907483b8230
next: noahsark status
```

`pack` fills one disc. Data that does not fit stays staged for the next
`pack`. The label of a disc is the newest ref, then the disc number.
With nothing staged, `pack` prints `pack: nothing staged`, then
`next: noahsark status`. To see how many discs the staged data needs, add
`--dry-run` before the pack. It writes nothing and prints no `next:`
line. Each line shows the number that the disc gets:

```
$ noahsark pack --capacity=bd25 --dry-run
disc 0: 8 items, 3001350 bytes
total: 1 discs, 8 items, 3001350 bytes
```

### A snapshot packed in parts

A snapshot that is larger than one disc goes on two or more discs. Each
`pack` takes the next part. The snapshot object itself goes on the disc
that holds the last part. Until that disc exists, `recover` from the discs
alone cannot find the snapshot. `status` names each snapshot that is not
complete on discs on a line of its own, a new commit too:

```
$ noahsark status
staged: 412 items, 1830221824 bytes
snapshot 1b03c7e2a9f4: 412 items staged, not complete on discs; its snapshot object is not on a disc yet
disc 0 "2026-09-14 disc 0"  on disc only, last check 2026-09-14  4a060bd4-ca9f-2d06-263e-b907483b8230
next: load a blank disc, then run:
dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Free Blocks'
then paste this line, type the capacity, and press Enter:
noahsark pack --capacity=
```

While a disc is `verified`, the block is `noahsark gc` first. The block
for the rest comes after `gc`.

`pack` takes the snapshots in the order of their time, the oldest first.
Thus the rest of such a snapshot goes before each newer snapshot. `pack`
does not wait for a full disc. You decide when to pack a small rest: pack
it now on a small disc, or commit more and pack later. The snapshot is safe
on discs only after its rest is packed and the disc is verified.

After `disc lost` of a disc that held a part, the snapshot line says `the
discs alone cannot restore all of it`. The next `pack` takes the items of
the lost disc again.

When `pack` or `status` prints `warning: snapshot ID: cannot pack all of
it`, a staged item of that snapshot is damaged or missing. `pack` packs
the items that it can take. Do the repair that the warning names. For a
damaged chunk, the repair is: delete the file that the warning names,
commit the same source again, then pack.


### Build the image, burn, and verify

Run `status` again. It prints the block for the packed disc:

```
$ noahsark status
staged: 0 items, 0 bytes
disc 0 "2026-09-14 disc 0"  packed  4a060bd4-ca9f-2d06-263e-b907483b8230
next: load a blank disc, then run:
sudo noahsark --repo=/srv/ark/repo image build 0 &&
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=/srv/ark/repo/staging/plans/4a060bd4-ca9f-2d06-263e-b907483b8230/tree.img &&
eject /dev/sr0 && eject -t /dev/sr0 &&
sudo -v && sudo mkdir -p /mnt/ark && for i in $(seq 30); do sudo mount -o ro /dev/sr0 /mnt/ark 2>/dev/null && break; sleep 2; done && mountpoint /mnt/ark &&
noahsark verify /mnt/ark;
sudo umount /mnt/ark && eject /dev/sr0
or burn the folder directly; see the guide, "Burn the folder directly". Load a blank disc, then run:
noahsark verify /srv/ark/repo/staging/plans/4a060bd4-ca9f-2d06-263e-b907483b8230/tree &&
growisofs -Z /dev/sr0 -R -iso-level 4 -V NOAHSARK_0000 /srv/ark/repo/staging/plans/4a060bd4-ca9f-2d06-263e-b907483b8230/tree &&
eject /dev/sr0 && eject -t /dev/sr0 &&
sudo -v && sudo mkdir -p /mnt/ark && for i in $(seq 30); do sudo mount -o ro /dev/sr0 /mnt/ark 2>/dev/null && break; sleep 2; done && mountpoint /mnt/ark &&
noahsark verify /mnt/ark;
sudo umount /mnt/ark && eject /dev/sr0
```

The block holds two methods. Paste one of them, not both. The lines
from `or burn the folder directly` on are the second method: see "Burn
the folder directly". Load the blank disc, and paste the lines from
`sudo noahsark` to the first `eject /dev/sr0`. They do these steps:

1. `image build` makes a UDF image of the disc root. It needs root for a
   loop mount of the image, thus the line starts with `sudo`. It prints
   `built image FILE (N bytes)`.
2. `growisofs` burns the image. The disc stays open. For M-DISC media,
   change `-speed=4` to `-speed=2`.
3. The drive ejects the disc and loads it again, so that the read comes
   from the disc. `sudo -v` asks for your password again when the burn
   took longer than the `sudo` timeout. Thus the block can ask for the
   password two times: at `image build`, and after the burn. Then the
   block mounts the disc read-only. The drive needs some seconds to read
   the disc, thus the mount line tries again, up to 30 times, 2 seconds
   apart. `mountpoint` prints `/mnt/ark is a mountpoint` when the mount
   worked.
4. `verify` reads every item back and checks it. When the check is good,
   `verify` records the burn and marks the disc `verified`.
5. The block unmounts the disc and ejects it. The `;` after `verify`
   runs this line also when `verify` fails, so the disc never stays
   mounted. `verify` prints its own result.

A slot-load drive cannot load a disc by itself: `eject -t` fails and stops
the block. Push the disc in by hand, then paste the lines from `sudo -v`
to the end of the method.

When the disc does not mount in 60 seconds, `mountpoint` prints
`/mnt/ark is not a mountpoint`, and `verify` does not run. The burn can
still be good. See "The disc does not mount" in "When something goes
wrong".

`verify` prints what changed in the repository:

```
$ noahsark verify /mnt/ark
disc 0 "2026-09-14 disc 0": 8 items, ok
burn recorded; verified
next: noahsark status
```

The number counts the items on the disc. From the second disc on, it can
be larger than the number that `pack` printed: each disc also carries the
snapshots of the earlier discs.

Write the disc number, the first 8 characters of the uuid, and the storage
place on the sleeve of the disc. The tool keeps no shelf notes.

`status` now shows the verified disc. `gc` can free its data now, thus
`status` asks you to make the second copy first. It prints one `advice:`
line for each `verified` disc, also when the `next:` block is for
another disc:

```
$ noahsark status
staged: 0 items, 0 bytes
disc 0 "2026-09-14 disc 0"  verified, last check 2026-09-14  4a060bd4-ca9f-2d06-263e-b907483b8230
advice: burn a second copy of /srv/ark/repo/staging/plans/4a060bd4-ca9f-2d06-263e-b907483b8230/tree.img before gc; see the guide, "A second copy"
next: noahsark gc
```

*... Days pass. You add, change and delete files in `/srv/data`. Each
cycle is `commit`, then `status` and its `next:` block.*

## Burn the folder directly

The recommended method is the one above: `image build`, then a burn of the
image. Use it for long-term storage. The second method burns the disc
root folder directly. `growisofs` then makes an ISO 9660 volume with Rock
Ridge. It needs no `sudo` and no `mkudffs`.

Read these warnings first:

- Never pass `-J`. Joliet cuts long names, and `verify` of the disc fails.
- Never pass `-udf`. It adds a second directory tree, and the two trees
  can disagree.
- Never pass `-M`. One disc holds one run.
- There is no image to check before the burn. Verify the folder first.
- The reading of this disc on Windows and on macOS is not verified.

`status` prints the lines of this method after the block of a `packed`
disc, from the line `or burn the folder directly` on. Load a blank disc,
and paste them. They hold the real path, the volume label, and
`-dvd-compat` for a disc that you packed with `--close`. This section
explains each line.

The disc root of disc 0 is the folder
`/srv/ark/repo/staging/plans/4a060bd4-ca9f-2d06-263e-b907483b8230/tree`.
The path holds the uuid that `status` shows. For a `pack --out` disc, the
disc root is the `--out` directory.

First, the lines verify the folder. A folder is not a disc, thus this
check records nothing:

```
$ noahsark verify /srv/ark/repo/staging/plans/4a060bd4-ca9f-2d06-263e-b907483b8230/tree
disc 0 "2026-09-14 disc 0": 8 items, ok
not counted: this is not a disc (inside the repository)
```

Then they burn the folder:

```
growisofs -Z /dev/sr0 -R -iso-level 4 -V NOAHSARK_0000 /srv/ark/repo/staging/plans/4a060bd4-ca9f-2d06-263e-b907483b8230/tree
```

`-V` gives the volume label that your computer shows for the disc: `NOAHSARK_`
and the disc number with at least 4 digits. `image build` writes the same
label. For disc 7, give `-V NOAHSARK_0007`.

For a disc that you packed with `--close`, the line starts with
`growisofs -dvd-compat`. After the burn, the steps are the same as for
the image:

```
eject /dev/sr0 && eject -t /dev/sr0 &&
sudo -v && sudo mkdir -p /mnt/ark && for i in $(seq 30); do sudo mount -o ro /dev/sr0 /mnt/ark 2>/dev/null && break; sleep 2; done && mountpoint /mnt/ark &&
noahsark verify /mnt/ark;
sudo umount /mnt/ark && eject /dev/sr0
```

`verify` records the burn and marks the disc `verified`, as for the image.
The `advice:` line of `status` then says `copy disc 0 before gc`, because
no image exists.

## A second copy

`gc` frees the repository copy after one verified disc. One disc has no
redundancy: one bad spot can lose data. Keep a
second copy in a different building. The tool records nothing about the
second copy. It is your job.

**Before `gc`**, burn the same image to a second blank disc, and check the
disc with `verify --no-mark`. Load a blank disc, and paste:

```
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=/srv/ark/repo/staging/plans/4a060bd4-ca9f-2d06-263e-b907483b8230/tree.img &&
eject /dev/sr0 && eject -t /dev/sr0 &&
sudo -v && sudo mkdir -p /mnt/ark && for i in $(seq 30); do sudo mount -o ro /dev/sr0 /mnt/ark 2>/dev/null && break; sleep 2; done && mountpoint /mnt/ark &&
noahsark verify --no-mark /mnt/ark;
sudo umount /mnt/ark && eject /dev/sr0
```

`verify --no-mark` checks every byte and writes nothing:

```
$ noahsark verify --no-mark /mnt/ark
disc 0 "2026-09-14 disc 0": 8 items, ok
not marked
```

For a disc that you burned from the folder, burn the folder again with the
`growisofs` line of "Burn the folder directly".

**After `gc`**, the image and the folder are gone. Copy a good disc to an
image, then burn that image. Use the same lines to replace a copy that
fails a check, or a copy that is destroyed. Load the good disc, and paste:

```
ddrescue -b 2048 -n -r1 /dev/sr0 ~/copy.img ~/copy.map && eject /dev/sr0
```

A host with no optical drive cannot mount a disc. A loop mount gives a
disc image, such as a ddrescue copy or a test image, to `verify`,
`recover` or `restore`. With a real disc, mount the disc, not an image.
Mount the image file read-only. `--no-mark` keeps the check of an image
out of the records:

```
sudo mkdir -p /mnt/ark && sudo mount -o loop,ro ~/copy.img /mnt/ark &&
noahsark verify --no-mark /mnt/ark;
sudo umount /mnt/ark
```

For `recover`, use the same mount, and replace the `verify` line with
`noahsark recover --source=/srv/data --disc=/mnt/ark`.

Then load a blank disc, and paste:

```
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=$HOME/copy.img &&
eject /dev/sr0 && eject -t /dev/sr0 &&
sudo -v && sudo mkdir -p /mnt/ark && for i in $(seq 30); do sudo mount -o ro /dev/sr0 /mnt/ark 2>/dev/null && break; sleep 2; done && mountpoint /mnt/ark &&
noahsark verify --no-mark /mnt/ark && rm ~/copy.img ~/copy.map;
sudo umount /mnt/ark && eject /dev/sr0
```

When the check fails, the copy stays in `~/copy.img`. Burn it again on a
new blank disc.

Write the same number and uuid on the sleeve of the second copy, and a
`B`. Store it in a different building from the first disc.

## Check an old disc

Verify each disc again from time to time, for example one time each year.
Load the disc, and paste:

```
sudo mkdir -p /mnt/ark && sudo mount -o ro /dev/sr0 /mnt/ark &&
noahsark verify /mnt/ark;
sudo umount /mnt/ark && eject /dev/sr0
```

A good check changes no state. It logs the date:

```
$ noahsark verify /mnt/ark
disc 0 "2026-09-14 disc 0": 8 items, ok
check logged
next: noahsark status
```

For a disc that is still `verified`, the second line is `already
verified; check logged`. `status` shows the date as `last check DATE`.
When a check fails, see "When something goes wrong".

## Free the staging space

`gc` frees the staged data of a `verified` disc. It also
removes the disc root and the image of that disc. It never removes a file
of `state/` or `catalog/`, thus `log` and `ls` still show every snapshot.
Burn your second copy before this step. See "A second copy".

Run `noahsark gc --dry-run` first. It shows what `gc` would free, and
frees nothing:

```
$ noahsark gc --dry-run
gc: would free 8 item(s), 9441280 bytes
```

The bytes count the disk space of the staged data, the disc root and the
image. The image is a sparse file: it counts the blocks that it uses, not
the capacity of the disc.

Then run `gc`. It asks no question.

```
$ noahsark gc
gc: freed 8 item(s), 9441280 bytes
next: noahsark status
```

`gc` keeps the data of a disc that is `packed` or `burned`: that disc is
not verified yet. `gc --dry-run` and `gc` print one line for each such
disc, after the `freed` line. For example, while disc 1 waits for its
burn:

```
$ noahsark gc
gc: freed 8 item(s), 9441280 bytes
gc: disc 1: not verified; 4 item(s) held
next: noahsark status
```

Verify disc 1, then run `gc` again.

After `gc`, the disc is `on disc only`. When `gc` has nothing to free,
it prints `gc: freed 0 item(s), 0 bytes` and no `next:` line: nothing
changed.

## Look at the history

`log` lists every snapshot, newest first. `ls` lists the entries of a
snapshot. Both read the repository only. You need no disc for them.

The output is made for a program to read. Each line is one record, and a
tab separates the fields. `log` prints the snapshot id, the time in UTC,
the refs (`-` for none), the source path and the message:

```
$ noahsark log
5e9a02d41c7b	2026-10-12T09:15:02Z	2026-10-12	/srv/data	photos of the trip
1b03c7e2a9f4	2026-09-14T08:30:00Z	2026-09-14	/srv/data	-
```

`ls` prints the mode, the type, the size in bytes, the modification time
in UTC and the path. The path is relative to the source root. Without
`-R`, `ls` lists one level. With `-R`, it lists every level:

```
$ noahsark ls -R 2026-09-14
0644	file	1204	2026-09-10T17:02:11Z	notes.txt
0755	dir	0	2026-09-12T08:00:00Z	photos
0644	file	2998146	2026-09-12T07:59:40Z	photos/2026-09-12.jpg
```

A `SNAPSHOT` argument is a ref name, a full snapshot id, or the start of a
short id, as `log` prints it, without `1220`. A ref name wins over a
start of an id. When a start of an id matches more than one snapshot, the
tool lists each full id and exits with code 2. Give more characters. You
can copy a path from `ls` into a `restore` line.

## Restore

Do a restore drill now, and again every few months. `restore` needs the
repository. It plans from the repository, and reads the data from the
discs, one disc at a time, at one mount point.

The syntax is `restore [OPTIONS] --disc=DIR SNAPSHOT [PATH...] DEST`. With
no `PATH`, `restore` writes the content of the source root into `DEST`:
`/srv/restore/notes.txt`, not `/srv/restore/srv/data/notes.txt`.

### See the discs that you need

You must be able to write in `DEST`. Make it first when its parent
directory is not yours. Mount any disc of the repository, and ask for the
plan:

```
$ sudo mkdir -p /srv/restore && sudo chown "$USER": /srv/restore
$ sudo mkdir -p /mnt/ark && sudo mount -o ro /dev/sr0 /mnt/ark
$ noahsark restore --dry-run --disc=/mnt/ark 2026-10-12 /srv/restore
disc 0 "2026-09-14 disc 0" (4a060bd4-ca9f-2d06-263e-b907483b8230): 2 items, 2999478 bytes
disc 1 "2026-10-12 disc 1" (cb3bebe8-5d21-4f07-9a6e-0c4d2b7f9e11): 4 items, 3002699 bytes
totals: 2 discs, 6 items, 6002177 bytes
```

Get the discs from the shelf.

### Restore with one drive

Open a second terminal for the disc swap. In the first terminal, run the
same line without `--dry-run`:

```
$ noahsark restore --disc=/mnt/ark 2026-10-12 /srv/restore
disc 0 "2026-09-14 disc 0" (4a060bd4-ca9f-2d06-263e-b907483b8230): 2 items, 2999478 bytes
disc 1 "2026-10-12 disc 1" (cb3bebe8-5d21-4f07-9a6e-0c4d2b7f9e11): 4 items, 3002699 bytes
totals: 2 discs, 6 items, 6002177 bytes
disc 0 "2026-09-14 disc 0": found
expected disc 1 "2026-10-12 disc 1" (cb3bebe8-5d21-4f07-9a6e-0c4d2b7f9e11), found disc 0 "2026-09-14 disc 0" (4a060bd4-ca9f-2d06-263e-b907483b8230)
insert disc 1 "2026-10-12 disc 1" (cb3bebe8-5d21-4f07-9a6e-0c4d2b7f9e11) into /mnt/ark and press Enter
```

`restore` reads the disc that is at `/mnt/ark` first. Then it asks for the
next disc and waits. In the second terminal, unmount the disc and eject
it:

```
sudo umount /mnt/ark && eject /dev/sr0
```

Put the named disc in the drive. Then load it and mount it:

```
eject -t /dev/sr0 && for i in $(seq 30); do sudo mount -o ro /dev/sr0 /mnt/ark 2>/dev/null && break; sleep 2; done && mountpoint /mnt/ark
```

Press Enter in the first terminal. `restore` goes on:

```
disc 1 "2026-10-12 disc 1": found
restored snapshot 5e9a02d41c7b into /srv/restore
```

Without `--overwrite`, `restore` never replaces a path that is already
there. `restore` never unmounts and never ejects a disc.

### Restore one path

Give a path from `ls` before `DEST`. A path follows the `rsync` rule for a
trailing slash:

- `photos` makes the directory `/srv/drill/photos`.
- `photos/` puts the content of `photos` directly into `/srv/drill`.

Make `/srv/drill` first, as `/srv/restore`.

```
$ noahsark restore --disc=/mnt/ark 2026-09-14 photos /srv/drill
disc 0 "2026-09-14 disc 0" (4a060bd4-ca9f-2d06-263e-b907483b8230): 1 items, 2998210 bytes
totals: 1 discs, 1 items, 2998210 bytes
disc 0 "2026-09-14 disc 0": found
restored snapshot 1b03c7e2a9f4 into /srv/drill
```

The plan lines keep the plural form also for 1, as `1 items`, so that a
script can read them.

### A damaged disc

When a disc is damaged, `restore` names each file that it cannot restore,
writes no wrong data, and exits with code 1:

```
noahsark: restore: warning: /srv/restore/photos/a.jpg: 1220ca2c95829e1f0b7d4a6c3e58f2a1d907b6c4e2f81a3d5c7b9e0f1a2b3c4d5e6f: content id does not verify
noahsark: restore: warning: not restored: 1 file(s) not restored; see the warning(s) above
restored snapshot 5e9a02d41c7b into /srv/restore
```

Mount your second copy of the same disc at the same mount point, and run
the same command again. `restore` skips the files that it already
restored, completes the others from the second copy, and exits with
code 0. See "A second copy".

### After a stop

When `restore` stops, run the same command again. A kill, a power cut, or
an end of input at the question stops it. `restore` keeps the part of
each file that it wrote, in a hidden file `.NAME.noahsark-part`. The next
run checks those parts, skips the files that are complete, and asks only
for the discs that it still needs.

## After the computer is lost

You have the discs and a new computer, and no repository. First make the
repository again from the discs with `recover`. Then restore. `restore`
never builds a repository.

Install the tool as in "Set up, one time". Do not run `init`. Make the
repository directory, as for `init`. Load any disc of the repository,
mount it, and run `recover`. `recover` fills the empty directory, or
creates it when you can write in its parent. `--source` is the source
root for your next commits.

```
$ sudo mkdir -p /srv/ark/repo && sudo chown "$USER": /srv/ark/repo
$ sudo mkdir -p /mnt/ark && sudo mount -o ro /dev/sr0 /mnt/ark
$ noahsark --repo=/srv/ark/repo recover --source=/srv/data --disc=/mnt/ark
recover: read disc 1 "2026-10-12 disc 1"
recover: disc 0 "2026-09-14 disc 0" (4a060bd4-ca9f-2d06-263e-b907483b8230) named by another disc, not yet given
next: noahsark status
$ sudo umount /mnt/ark && eject /dev/sr0
```

The first line names the disc that `recover` read. Each disc names the
discs before it. A disc that another disc names, and
that you did not give yet, is `missing`. `recover` then exits with code 1.
That is normal: give each disc one time, in any order. Go into the
repository, and let `status` name the next disc:

```
$ cd /srv/ark/repo
$ noahsark status
staged: 0 items, 0 bytes
disc 0 "2026-09-14 disc 0"  missing  4a060bd4-ca9f-2d06-263e-b907483b8230
disc 1 "2026-10-12 disc 1"  on disc only  cb3bebe8-5d21-4f07-9a6e-0c4d2b7f9e11
next: load disc 0 "2026-09-14 disc 0", then run:
sudo mkdir -p /mnt/ark && sudo mount -o ro /dev/sr0 /mnt/ark &&
noahsark recover --source=/srv/data --disc=/mnt/ark;
sudo umount /mnt/ark && eject /dev/sr0
or, when disc 0 is gone for good, run:
noahsark disc lost 0
```

Load disc 0, and paste the first part of the block. The `;` after
`recover` unmounts and ejects the disc also when `recover` exits with code
1, as it does while another disc is still `missing`. Read the `recover`
line above the unmount to see the result. A disc that you give a second
time prints `already known`, and `recover` still exits with code 1 while
another disc is `missing`. When every disc is given, the line starts with
`recover: ok`, and `status` shows no `missing` disc.
Then mount a disc, and restore as in "Restore".

**A missing disc** is a disc that you cannot find now. `commit` and
`pack` refuse to run while a disc is `missing`. When you find the disc,
give it to `recover`. When it is gone for good, run `noahsark disc lost
0`. It asks a question: answer `y`. The data that only that disc held is
gone. Your next `commit` stages again what the source still holds.

**A damaged disc.** `recover` keeps each object that it can read. It
prints `recover: damaged: ID` for each object that fails its check, and
exits with code 1. Copy the disc now, or use your second copy, and give
the copy to `recover`. "A second copy" shows the copy to an image file,
and the loop mount that gives the image to `recover`.

## When something goes wrong

| What you see | What to do |
|---|---|
| You packed with the wrong capacity, and nothing is burned | `noahsark pack --undo 0` for the newest disc, then `noahsark status`, and pack again. See "Undo a step". |
| A burn fails midway | Discard the disc. When `growisofs` says that the image does not fit, run `noahsark pack --undo 0` and pack again. Else run `noahsark status` and paste its block with a new blank disc. |
| The disc does not mount: `mountpoint` prints `/mnt/ark is not a mountpoint` | The burn can still be good: some drives need more than a minute to read a new disc. Do not burn the disc again yet. When the burn finished with no error, run `noahsark disc burned 0`, wait a minute, then run `noahsark status` and paste its block: it mounts and verifies the disc. Discard the disc only when it still does not mount. Then, if you ran `disc burned` for it, run `noahsark disc burned --undo 0`, and `noahsark status`. |
| `verify` of a new disc prints `bad; this disc is bad; burn record removed` or `bad; this disc is bad; no record to remove` | Discard the disc. `noahsark status` prints the block that burns a new disc from the kept disc root. |
| `verify` of a `verified` disc prints `bad; this disc is bad; verified record removed; gc holds the data` | The disc is `burned` again, and `gc` holds its data. Discard it. `noahsark status` prints the block that burns a new disc. |
| `verify` of an `on disc only` disc prints `bad; the staged copy is already freed; ...` | Copy the disc now, while it still reads, or use your second copy. See "A second copy". When no copy can be read, run `noahsark disc lost 0 && noahsark commit`. |
| `cannot read the disc: no DISC.bin under ...; is the disc mounted at /mnt/ark?` | Nothing is mounted at `/mnt/ark`, or the wrong directory is given. Mount the disc read-only, and run the command again. For an image file, see the loop mount in "A second copy". |
| `status` says that a disc has no disc root | No new disc can be burned from it. Discard the disc, and paste the `noahsark disc lost 0` line. The next `pack` takes its items. |
| Every copy of a disc is destroyed | `noahsark disc lost 0`, and answer `y`. See "A lost disc". |
| You find a disc that you marked lost | `noahsark disc lost --undo 0`, and answer `y`. Then run `noahsark status`, and paste its block. It verifies the disc, or it recovers a disc that was `missing`. |
| `status` prints `next: disc 0: an earlier disc lost stopped before it wrote the records of its items; ...` | A command stopped between its disc event and the records of its items. Paste the `noahsark gc` line. `gc` writes the records first. It also frees the data of each verified disc, as a normal `gc` does. |
| `status` prints `warning: staging directory DIR does not exist` | The volume of the staging store is not mounted, or `staging.dir` in `config.yaml` names a wrong path. Mount the volume, or correct the path. Only when the staging store is gone for good, run `mkdir -p DIR`; `status` then names each `packed` disc with `disc lost`. The warning comes only while staged or packed data needs the directory. |
| `restore` prints `expected disc 1 ... found disc 0 ...` | The wrong disc is in the drive. Mount the named disc, or its second copy, at the same mount point, and press Enter. |
| `restore` stops between two discs | Mount the named disc, and run the same `restore` again. It resumes. |
| `restore` reports that a path is already there | Add `--overwrite`, or restore into an empty directory. |
| `restore` prints `restore: N item(s) have no disc known to the catalog; run recover with more discs` | The repository does not know which disc holds some data. `restore` restores every other file, names each file that it cannot restore, and exits with code 1. Give each disc that you still hold to `recover`, then run the same `restore` again. |
| `restore`, `ls` or `log` prints `snapshot ID is partial; run recover with more discs` | `recover` did not read every disc of that snapshot yet. Give each disc that you still hold to `recover`, then run the same command again. |
| `no repository; run recover first, one time for each disc` | See "After the computer is lost". |
| `no repository; give --repo, or run noahsark init for a new repository, or noahsark recover for a lost one` | Go into the repository, or give `--repo`. After the computer is lost, do not run `init`: see "After the computer is lost". |
| `image build`: `FILE exists; add --force to build it again` | Add `--force` after `image build`. |
| `repository lock ... is held` | Another `noahsark` command runs on this repository. Wait for it. |
| `nothing changed` | You answered no to the question. Run the command again, and answer `y`. |
| `commit` prints `unstable PATH` or `skipped PATH` | The snapshot is written. A file changed or could not be read: run `commit` again later. A name that holds `\` cannot be stored: rename the file, then run `commit` again. The line shows a `\` as `\\` and a newline as `\n`, as `ls` does. |
| `commit`: `ref name "NAME" is not valid: a ref name is 1 to 40 bytes of printable ASCII, with no space` | Nothing is written. Give a `--ref` of 1 to 40 bytes of printable ASCII, with no space. |

A failed `verify` removes one record: the verified record first, else the
burn record. The state of the disc goes down one step, and `gc` holds the
data. Do not run `disc burned --undo` yourself for a disc that failed
`verify`.

### Undo a step

Each undo command shows what changes, and asks before it changes
anything:

```
$ noahsark pack --undo 1
warning: disc 1 "2026-10-12 disc 1" (cb3bebe8-5d21-4f07-9a6e-0c4d2b7f9e11): packed -> undone
the items return to staged; the disc number 1 is not used again
Continue? [y/N] y
disc 1 "2026-10-12 disc 1": pack undone, 12 item(s) returned to staged
next: noahsark status
```

The answers `y` and `yes` continue, in any letter case. Spaces around the
answer are ignored. Every other answer, an empty line, and the end of input
print `nothing changed`, and the command exits with code 1.

| Step | Undo |
|---|---|
| `commit` | None. A snapshot stays. |
| `pack` | `noahsark pack --undo SEQ`, for the newest disc while it is `packed`. The disc number is not used again. The next pack gets the next number. |
| `image build` | None needed. `--force` builds the image again. |
| `disc burned` | `noahsark disc burned --undo SEQ`, while the disc is `burned`. |
| `verify` | `noahsark verify --undo SEQ`, while the disc is `verified`. The disc is `burned` again, and `gc` holds its data. |
| `disc verified` | `noahsark verify --undo SEQ`. |
| `disc lost` | `noahsark disc lost --undo SEQ`, when the disc was `verified`, `on disc only` or `missing` before `disc lost`. A `verified` disc comes back as `burned`, and needs a new `verify`. |
| `gc` | None. It frees only data that a verified disc holds. |

`pack --undo` removes the disc root in the repository. For a `pack --out`
disc, it keeps the directory and prints `disc root DIR kept; delete it
yourself`.

### A lost disc

`disc lost` tells the tool to stop trusting a disc. It asks a critical
question, because the data that only this disc held is then gone:

```
$ noahsark disc lost 0
warning: disc 0 "2026-09-14 disc 0" (4a060bd4-ca9f-2d06-263e-b907483b8230): on disc only -> lost
the tool stops trusting this disc
Continue? [y/N] y
disc 0 "2026-09-14 disc 0": marked lost; 5 item(s) returned to staged; 3 item(s) need a new commit
next: noahsark status
```

For a disc that `gc` did not free yet, the items return to staged at
once, and the next `pack` takes them. For an `on disc only` disc, the
repository still holds the snapshots, the trees and the file lists: they
return to staged at once. The file data is gone with the disc. Run
`commit`. It stages again the data that the source still holds. Until you
commit, the `next:` block of `status` gives the line:

```
$ noahsark status
staged: 5 items, 1503 bytes
snapshot 3f1c09ab2d77: 5 items staged, not complete on discs; its snapshot object is not on a disc yet
lost: 3 items; only a lost disc holds them
disc 0 "2026-09-14 disc 0"  lost  4a060bd4-ca9f-2d06-263e-b907483b8230
next: disc 0 is lost; a new commit stages what the source still holds; run:
noahsark commit
```

The `lost:` line counts the data that no disc and no staged copy holds. It
stays until a commit stages that data again. When the source no longer
holds a file, its data stays lost, and the line stays. Then an old
snapshot needs data that only the lost disc held. `restore` restores every
file that the other discs hold, names each file that it cannot restore,
and exits with code 1. `restore --dry-run` of a snapshot shows the lost
disc in its plan, with ` (lost)` at the end of the line. The same holds
for a disc that was `missing` when you marked it lost.

### Record a burn or a verified disc without a check

`noahsark disc burned 0` records a burn and reads no disc. Use it when you
cannot verify now. `status` then shows `burned`, and its block verifies
the disc later.

`noahsark disc verified 0` marks a `burned` disc `verified` on your word,
and reads no disc. `gc` can then free the repository copy at once.
If the disc is bad, that data is lost. The command asks a critical
question. `status` shows the disc as `verified, not checked` until a good
`verify`.

## Options

**Close.** `pack --close` makes the `growisofs` line of `status` seal the
disc with `-dvd-compat`. A sealed disc can take no more data. The choice
is permanent. The default burn leaves the disc open, and every reader
reads an open disc.

**Pack output.** `pack --out=DIR` writes the disc root into `DIR` in place
of the repository. `DIR` must be empty or absent. `gc` and `pack --undo`
never remove `DIR`: it is yours to keep or delete.

## Scripts

Every command works in a script.

- A question gets no answer from a script. With no terminal on standard
  input, the answer is no: the command prints `nothing changed` and exits
  with code 1.
- `--yes` answers an ordinary question: `pack --undo`, `disc burned
  --undo`, `verify --undo` and `disc lost --undo`. `--force-yes` answers
  every question, also the critical ones of `disc verified` and `disc
  lost`. Give them before the command name: `noahsark --yes pack --undo
  1`.
- `-q` turns off the progress line.
- With no terminal, `restore` does not wait for a disc. It prints
  `restore: insert disc SEQ "LABEL" (UUID) into DIR and run restore
  again`, and exits with code 1. The script mounts that disc and runs the
  same `restore` again.
- `ls` and `log` print one record on each line, with the fields separated
  by a tab.

Every command exits with one of three codes:

| Code | Meaning |
|---:|---|
| 0 | Success. |
| 1 | A failure at run time, or a state that refuses the command, or the answer no. |
| 2 | A usage error: a bad option or argument, or no repository. |

## Command reference

`OPERATIONS.md`, "CLI reference", gives the full syntax. `noahsark COMMAND
-h` prints the options of a command. A global option (`--repo`, `-q`,
`--yes`, `--force-yes`) comes before the command name. A command option
comes after the command name, or after the subcommand word in a group.

| Command | What it does |
|---|---|
| `init --source=PATH` | Make the current directory a repository. |
| `commit` | Stage the source as a new snapshot, and move a ref. |
| `pack --capacity=SIZE` | Write the staged items into a disc root for one disc. |
| `pack --undo DISC` | Return the items of the newest packed disc to staged. |
| `image build DISC` | Make the UDF image of a disc root. Run it with `sudo`. |
| `verify DISC-ROOT` | Check every item of a disc. A good check of a mounted disc marks it `verified`. |
| `verify --undo DISC` | Remove the verified record of a disc. |
| `disc burned DISC` | Record a burn without a check. `--undo` removes it. |
| `disc verified DISC` | Mark a burned disc `verified` without a check. |
| `disc lost DISC` | Stop trusting a disc that is gone. `--undo` trusts a found disc again. |
| `status` | Print the staged total, the state of each disc, and the `next:` block. |
| `gc` | Free the staged data of verified discs. |
| `log` | List the snapshots, newest first. |
| `ls SNAPSHOT [PATH]` | List the entries of a snapshot. |
| `restore --disc=DIR SNAPSHOT [PATH...] DEST` | Write the files of a snapshot into `DEST`, one disc at a time. |
| `recover --source=PATH --disc=DIR` | Make a lost repository again from one disc. Run it for each disc. |

A `DISC` argument is the disc number, the full uuid, or the start of the
uuid. A value of 1 to 7 decimal digits is always a disc number. A value of 8
or more digits, or a value with a letter or a hyphen, is a uuid or the start
of a uuid.
