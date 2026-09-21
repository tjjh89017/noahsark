# Operator guide

Part 1 is the full cycle. Follow it in order. Part 2 is a reference.
`noahsark <command> -h` lists all flags of a command. The examples use
these values. Put your own values in their place:
`/srv/ark/repo` is the repository, `/srv/data` is the source that you back
up, `2026-09-14` is a ref, `/dev/sr0` is the drive, and `/mnt/ark` is its
mount point (`<MOUNT>`). A line that starts with `$` is a command. The
lines below it are its output. A line in italics that starts with `...`
shows that time passes.

# Part 1. The cycle

## Words

- **Object**: one piece of your data. Equal data is stored one time.
- **Snapshot**: the state of the source at one commit.
- **Ref**: your name for a snapshot. Use the date.
- **Staged**: committed and held in the repository, not yet on a disc.
- **Pack**: write staged objects into a disc root, ready to burn.
- **Disc root**: the directory that holds `NOAHSARK/`: a packed tree or a mounted disc.
- **Disc number** (seq), **label**: the names of a disc. The uuid is its exact name.
- **Packed, burned, verified**: the states of a disc. `status` shows them.
- **Local cache**: disc indexes in the repository, so `log` and `ls` need no disc.
- **Unstable file**: a file that changed while `commit` read it.
- **FEC**: optional repair data on the disc. It is off by default.

## 1. Set up, one time

You need Go 1.27 or later, a DVD or Blu-ray writer, write-once media,
`dvd+rw-tools` 7.1-14 or later (`growisofs`) and `udftools` 2.3 or later
(`mkudffs`). Build in the source checkout. Put the binary on your `PATH`.

```
$ go build -o noahsark ./cmd/noahsark
$ sudo usermod -aG cdrom $USER      # then log in again
$ noahsark init --repo=/srv/ark/repo --source=/srv/data
initialized repository /srv/ark/repo
staging: /srv/ark/repo/staging
source: /srv/data
$ echo "pack.capacity = bd25" >> /srv/ark/repo/config
$ export NOAHSARK_REPO=/srv/ark/repo
```

Put the `export` line in your shell profile. `noahsark` never runs `sudo`
and never mounts a disc. You do that.

## 2. Commit

*... Days pass. You add, change and delete files in `/srv/data`.*

```
$ noahsark commit --ref=2026-09-14
snapshot 122009a2...
ref 2026-09-14 -> 122009a2...
new objects: 8, existing objects: 0
unstable: 0, skipped: 0
excluded: 0 path(s)
staged: 8 objects, 3001470 bytes
```

`commit` stages a snapshot. It writes no disc. Commit as often as you
want: a later commit stages only the new data. `status` always ends with
the one action to do next.

*... Days pass. You edit one file and commit. Then you add photographs.*

```
$ noahsark commit --ref=2026-09-21
...
$ noahsark status
staged: 20 objects, 6004049 bytes
next: pack a disc, run: noahsark pack
```

## 3. Pack

Pack when the `staged:` bytes come near to the size of one disc, or when a
fixed interval is over, for example one month. Buy one type of media and
keep it. `pack.capacity` in the config holds its size:

| Preset | Media | Bytes |
|---|---|---:|
| `dvd+r` | DVD+R (`dvd-r` for DVD-R) | 4,700,372,992 |
| `bd25` | BD-R 25 GB | 25,025,314,816 |
| `bd50` | BD-R DL 50 GB (also `bd100`, `bd128`) | 50,050,629,632 |

`noahsark pack --dry-run` prints how many discs the staged data needs.

```
$ noahsark pack
packed disc 0 "2026-09-14 disc 0": 20 objects, 6004049 bytes
uuid: 4a060bd4-ca9f-2d06-263e-b907483b8230
tree: /srv/ark/repo/staging/plans/4a060bd4.../tree
next steps:
  sudo noahsark image build --out=/srv/ark/.../tree.img /srv/ark/.../tree
  growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=/srv/ark/.../tree.img
  noahsark disc burned 0
  noahsark verify <MOUNT>
remaining staged: 0 objects, 0 bytes
```

The `next steps:` block is step 4 with your paths filled in. If
`remaining staged` is above 0, the data did not fit one disc: complete
step 4 for this disc, then pack again.

## 4. Burn, mark, verify, second copy

Copy the first three lines from the `next steps:` block. `image build`
needs root because it loop-mounts the image file. The burn leaves the disc
open. Do not add `-dvd-compat`. The tool never concludes by itself that a
burn occurred: only `disc burned` records it. After the burn, eject the
disc and load it again, then mount and verify it.

```
$ sudo noahsark image build --out=/srv/ark/.../tree.img /srv/ark/.../tree
$ growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=/srv/ark/.../tree.img
$ noahsark disc burned 0
disc 0 2026-09-14 disc 0: marked burned, 20 objects
$ sudo mkdir -p /mnt/ark
$ sudo mount /dev/sr0 /mnt/ark
$ noahsark verify /mnt/ark
disc 0 "2026-09-14 disc 0": 20 objects, ok
verify: marked 20 object(s) CLEAN (disc 4a060bd4-ca9f-2d06-263e-b907483b8230)
verify: copy 1 of 2 verified; verify the second copy before gc
$ sudo umount /mnt/ark
```

If the burn fails, run `noahsark disc burned --undo 0`, then burn a new
disc. If the disc does not mount or `verify` fails, discard it and burn a
new one. Two identical discs are the redundancy of this backup. Load a second
blank disc and run the same `growisofs` line again. Do not pack again. Do
not run `disc burned` again. Mount and verify the second disc:

```
$ noahsark verify /mnt/ark
disc 0 "2026-09-14 disc 0": 20 objects, ok
verify: 2 of 2 copies verified
$ noahsark status
staged: 0 objects, 0 bytes
disc 0 "2026-09-14 disc 0"  verified  4a060bd4-ca9f-2d06-263e-b907483b8230
next: nothing to do
```

The states of a disc are `packed`, `burned`, `verified 1/2`, `verified`
and `on disc only`. `on disc only` is the normal last state: the disc holds
the data and `gc` has freed the staged copy.

## 5. Sleeve and storage

Write on each sleeve: the disc number, the label, the first 8 characters
of the uuid, the date, and `A` or `B`. Store copy B in a different
building. Keep a text file that lists each disc and its two locations.
After the two verifies, you can delete the `tree.img` file.

*... Weeks pass. You commit at each interval. When it is time, do steps 3
to 5 again. A new disc holds only the data that no earlier disc holds.
Thus a restore can need the earlier discs too. Keep all of them.*

## 6. Restore drill

Do this drill now, and again every few months. It is also the procedure
for a real loss. `noahsark log` lists the snapshots and their refs. Mount
the disc, then:

```
$ noahsark restore /mnt/ark 2026-09-21 /srv/drill
restored snapshot 12205fcd... into /srv/drill
$ diff -rq /srv/drill/srv/data /srv/data
```

The restored files are below `/srv/drill`, with the full source path.
`diff` prints no line when the restore is correct. If `restore` prints
`missing disc(s)`, the snapshot is on more than one disc: see "Restore".

# Part 2. Reference

## Commit: excludes and warnings

To exclude paths, add `--exclude=<PATTERN>` (repeatable), set
`sources.exclude` in the config, or put a `.noahsarkignore` file in the
source root, one pattern on each line. `*.tmp` or `node_modules` matches a
name at any depth. `/cache` is anchored at the source root. A trailing `/`
matches a directory only. There is no negation (`!`). `--one-file-system`
keeps `commit` off other mounted filesystems. An `unstable <PATH>` line
names a file that changed during the read: it is committed and flagged, so
commit again when the source is quiet.

## Restore

`restore`, `ls`, `log` and `verify` with a disc root need no repository.
If the repository is lost, run `noahsark log /mnt/ark` on the newest disc.
Give a ref, or the snapshot id from the first column of `log`.

**Several discs together.** Mount or copy each disc root into its own
directory below `/mnt/discs`, then run
`noahsark restore --discs-dir=/mnt/discs 2026-09-21 /srv/restore`. You can
also repeat `--disc=<MOUNT>` for each root.

**One drive.** This mode needs the repository. `--dry-run` lists the discs
and stops.

```
$ noahsark restore --mount=/mnt/ark --dry-run 2026-09-17 /srv/restore
disc 0 "2026-09-14 disc 0" (2d22d412-...): 1 objects, 11 bytes
disc 1 "disc 1" (cb3bebe8-...): 1 objects, 3000000 bytes
totals: 2 discs, 2 objects, 3000011 bytes
$ noahsark restore --mount=/mnt/ark --no-eject 2026-09-17 /srv/restore
...
insert disc 1 "disc 1" (uuid cb3bebe8-...) into /mnt/ark and press Enter
```

`restore` asks for each disc one time, in its own order. Keep a second
terminal open. While `restore` waits, run `sudo umount /mnt/ark` there,
change the disc, run `sudo mount /dev/sr0 /mnt/ark`, then press Enter in
the first terminal. A wrong disc gives `expected disc ..., found ...` and
the same prompt again. Without `--no-eject`, `restore` tries to unmount
and eject by itself. That fails for a user that is not root.

A stopped one-drive restore leaves hidden `.<NAME>.noahsark-part` files.
Run the same command again: it continues, and asks only for the discs that
it still needs. A name without `.noahsark-part` is always a complete file.

`restore` prints one report: a warning line for each path that it did not
restore, then `restored snapshot ...`, then a summary such as
`not restored: 1 existing path(s), 1 unsupported entry(ies)`.

- `FIFO not restored; ...`: an unsupported entry (FIFO, socket, device) is
  a warning only. The exit code stays 0.
- `a path is already here; pass --overwrite to replace it`, exit 1:
  restore into an empty directory, or add `--overwrite`.
- `--include=<PATH>` (repeatable) restores only that path. Get the paths
  from `noahsark ls --recursive 2026-09-21`. A `!` marks an unstable file.
- A damaged object: `restore` names the file, writes no bad data,
  continues, and exits 1. Use the second copy of the disc.

## Free disk space: gc

Run `noahsark gc --dry-run`, then `noahsark gc`. When it is too early, the
dry run prints `gc: nothing is eligible yet; earliest eligible date: ...`.
`gc` deletes the staged data of a disc only after two successful verifies
and 7 days after the first verify. The tool cannot tell the two copies
apart: it counts each successful `verify`. If you keep one copy only,
put `gc.min_verified_copies = 1` in the config. `--force-after=1h` shortens
the 7 days for one run, and asks for confirmation. It does not change the
verify count. Do not delete files in `staging` by hand.

## Recovery

**The repository is lost.** No burned data is lost. Do not run `init`.
Rebuild the state from the discs before the next pack:

```
$ noahsark recover --repo=/srv/ark/repo --discs-dir=/mnt/discs
recover: 3 disc(s) read, repo /srv/ark/repo
objects recorded: 19 on disc, 0 already known
discs known: 3, refs restored: 3
recover: ok
```

With one drive, run `noahsark recover --repo=/srv/ark/repo --disc=/mnt/ark`
one time for each disc, in any order. `rebuild is partial: disc ... not fed
yet`, exit 1, names a disc that you must still give. Give every disc, the
newest included. Then `status` shows each disc as `on disc only`. Do not
mark or verify the discs again. Put `sources.root = /srv/data` and
`pack.capacity` into the new config. A commit that was not packed before
the loss is gone: commit again.

**A disc is lost or bad.** Read from the other copy. Burn a new copy from
`tree.img` if you kept it. If the two copies are lost, `restore` names the
discs that it cannot find. Data that only those discs hold is lost.

For a complete new disc set, do part 1 with a new repository. Keep the
old discs.

## FEC

Two identical discs are the redundancy. FEC (Reed-Solomon parity) is an
option and is off by default. Add `--fec` to `pack`, or set
`fec.scheme = rs255-gf8` in the config. `--no-fec` overrides the config for
one pack. `pack` then prints `fec: on`. FEC uses approximately 9% of the
disc. `noahsark verify --heal --out=<DIR> <MOUNT>` writes a repaired disc
root into `<DIR>`. It refuses a disc without FEC: `has no FEC`.

## Image build, rehearsal and other options

- `image build` takes the image size from the packed tree. It refuses an
  existing image file. Add `--force` to replace it.
- **Rehearsal without a drive.** Use the packed `tree` directory in the
  place of `/mnt/ark` in part 1. README.md, "Quick start", does this. To
  test the image too, run `sudo mount -o loop -t udf <IMAGE> /mnt/ark`,
  then `noahsark verify /mnt/ark`. The image file has the full disc size.
- `pack --out=<DIR>` writes the tree into `<DIR>`. `--label=<TEXT>` sets
  the label. `--capacity=<SIZE>` sets the size for one pack: a preset, or a
  size with a unit such as `23GiB`. A number without a unit is an error.
- **Burn the directory, without an image and without root.** This writes
  ISO 9660 with Rock Ridge, not UDF. `verify` and `restore` read it the
  same way. Always use `-R -iso-level 4`. Joliet (`-J`) cuts the names.
  `growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0 -R -iso-level 4 -V NOAHSARK <TREE>`
- `pack --close` prints a burn line that seals the disc. It is permanent.

## Troubleshooting

Exit codes: 0 is success, 1 is a failure at run time, 2 is a usage error.

| Message | Action |
|---|---|
| `no noahsark repository found` | Set `NOAHSARK_REPO`, or give `--repo`. |
| `repository lock ... is held` | Wait for the other `noahsark` command to end. |
| `pack`: `no capacity` | Put `pack.capacity = bd25` in the config, or give `--capacity`. |
| `capacity: "7500000" has no unit` | Give a preset, or a size with a unit such as `25GB`. All commands refuse this config. |
| `pack`: `capacity ... holds not one object` | The capacity is too small. The message names the capacity to use. |
| `pack`: `staged chunk <ID> is damaged; run commit again ...` | A staged file is corrupt. Run `commit` again, then `pack` again. |
| `commit`: `warning: <PATH>: FIFO, no content is backed up` | Normal for a FIFO, a socket or a device: no content, exit 0. Exclude the path to stop the warning. |
| `commit`: `skipped: 1`, exit 1 | A file vanished, or a permission stopped the read. The rest is committed. Fix the cause and commit again. |
| `commit`: `no SOURCE given and no source root in the config` | Put `sources.root = /srv/data` in the config, or give the source as an argument. |
| `verify`: `disc 0 is not marked burned; run: noahsark disc burned 0` | The disc is good, but the repository does not know the burn. Run that command, then `verify` again. |
| `disc burned --undo`: `is verified (CLEAN) and cannot be returned to packed` | A verified disc stays verified. No action. |
| `matches no disc` or `matches more than one disc` | Give the disc number, or the first 8 characters of the uuid, from the list in the message. |
| A disc does not mount, or `verify` fails | Discard the disc. Burn a new copy and verify it. Use the other copy until then. |
| `gc`: `1 of 2 copies verified; N object(s) held` | Verify the second copy (step 4), then run `gc` again. |
| `restore`: `missing disc(s)`, exit 1 | The message lists each disc. Give all of them with `--disc` or `--discs-dir`, or use `--mount`. |
| `ref ... is not on the provided disc(s)` | A newer disc holds the ref. Give the newest disc too. |
| `restore`: `umount ... failed; run restore with sudo, or pass --no-eject` | Add `--no-eject`, and unmount in the second terminal. |
| `restore`: `stdin closed while waiting for the next disc` | Run `restore` in a terminal, not in a pipe. Run it again to continue. |
| `image build`: `mkudffs: ... executable file not found` | Install `udftools` 2.3 or later. |
| `growisofs`: `unable to open64(...): Permission denied` | Add your user to the `cdrom` group. Log in again. |
| `growisofs`: `media is not recognized as recordable DVD` | Load a blank BD-R, DVD+R or DVD-R. |
