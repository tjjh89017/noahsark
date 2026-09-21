# Operator guide

Follow the steps in order. Each step gives the command, its purpose and
the expected result. Run `noahsark <command> -h` to see all flags of a
command. For more detail, read the OPERATIONS.md headings that this
guide names.

Text in angle brackets, such as `<REPO>`, is a value that you supply.
Type all other text as shown.

A line in italics that starts with `...` shows that time passes. It
tells you what occurs before the subsequent step.

| Placeholder | Meaning | Example |
|---|---|---|
| `<REPO>` | repository directory | `/srv/noahsark/repo` |
| `<SOURCE>` | directory to back up | `/srv/data` |
| `<REF>` | name for one commit; use the date | `2026-09-14` |
| `<LABEL>` | text that names one disc | `"2026-09-14 disc 0"` |
| `<CAPACITY>` | disc size; see step 4 | `bd25` |
| `<DISC_DIR>` | new directory for one packed disc | `/srv/noahsark/disc0` |
| `<IMAGE>` | image file to build | `/srv/noahsark/disc0.img` |
| `<DEVICE>` | optical drive | `/dev/sr0` |
| `<MOUNT>` | mount point of the drive | `/mnt/noahsark` |
| `<DISC>` | one disc: its `seq`, uuid, uuid prefix or label | `0` |

## 1. Install

You need:

- Go 1.27 or later, to build the binary.
- A DVD or Blu-ray writer and write-once media (BD-R, DVD+R or DVD-R).
- `dvd+rw-tools` 7.1-14 or later (`growisofs`, `dvd+rw-mediainfo`).
- `udftools` 2.3 or later (`mkudffs`). `image build` refuses an older
  version.
- Optional: `eject`. Without it, open the tray by hand during a
  single-drive restore.

```sh
go build -o noahsark ./cmd/noahsark
```

Put the binary on your `PATH`. Add your user to the `cdrom` group, then
log in again, so that `growisofs` can open `<DEVICE>`.

`noahsark` never calls `sudo`. You run `sudo` yourself for `image build`,
`mount` and `umount`. All other commands run as your user.

To omit `--repo` from every command, set `NOAHSARK_REPO=<REPO>` or work
inside `<REPO>`. This guide shows `--repo` on each command. With
`NOAHSARK_REPO` set, leave it out.

## 2. Create the repository

```sh
noahsark init --repo=<REPO> --source=<SOURCE>
```

Do this one time. Expected result: `initialized repository <REPO>`, and
`<REPO>/config` holds `repo.uuid`, `staging.dir` and `sources.root`.

Add the size of your media to the config, one time:

```sh
echo "pack.capacity = bd25" >> <REPO>/config
```

Then `pack` needs no `--capacity`. Step 4 lists the values this key takes.

*... Work as usual. Add, change and delete files in `<SOURCE>`.*

## 3. Commit

```sh
noahsark commit --repo=<REPO> --ref=<REF>
```

This reads `<SOURCE>` and stages a snapshot. It writes no disc. Expected
result:

```
snapshot <id>
ref 2026-09-14 -> <id>
new objects: 7, existing objects: 0
unstable: 0, skipped: 0
staged: 7 objects, 3001388 bytes
```

- `staged:` is the total that waits for the next pack.
- An `unstable <PATH>` line names a file that changed during the read.
  The file is committed and flagged. Commit again when the source is
  quiet. Exit code 1 means that a path was unstable or skipped; the
  snapshot is still committed.
- A `skipped <PATH>: <REASON>` line names a file or directory that
  vanished during the scan, or that an open, read or permission error
  blocked. The rest of the tree still commits. Fix the reason (for
  example, restore read permission) and commit again to include it.
- A `warning: object <ID> was staged but corrupt; rewritten` line names
  a staged object file that existed but did not hold the right bytes
  (for example, truncated by an earlier crash). Commit rewrote it; no
  action is necessary.
- A `warning: <PATH>: FIFO, no content is backed up` line names a FIFO, a
  socket or a device node in the source. The name stays in the snapshot,
  the content does not, and `restore` does not create the path again.
  `commit` prints 20 such lines at most, then the count. The line
  `special files: N` gives the total. The exit code stays 0.

To commit a different directory one time, add it as an argument:
`noahsark commit --repo=<REPO> --ref=<REF> <SOURCE>`.

To leave paths out of the backup, add `--exclude=<PATTERN>` (repeatable),
set `sources.exclude` in the config, or put a `.noahsarkignore` file in
the source root. The pattern language is small and gitignore-style: one
pattern on each line, `*.tmp` or `node_modules` matches a name at any
depth, `/cache` or `build/out` is anchored at the source root, a
trailing `/` matches a directory only, and `**` crosses directories.
Negation (`!`) is not supported. For example:

```
# .noahsarkignore
node_modules/
*.tmp
/build/out
```

`commit` prints how many paths the excludes kept out. `--one-file-system`
keeps the walk off any other mounted filesystem; the mount point itself
still appears in the snapshot, as an empty directory.

*... Three days pass. You edit one small file. Commit again with a new
`<REF>`.*

```
ref 2026-09-17 -> <id>
new objects: 5, existing objects: 2
staged: 12 objects, 3002580 bytes
```

The commit added few bytes. This is too little for a disc. Do not pack.

*... Four more days pass. You add a large file. Commit again.*

```
ref 2026-09-21 -> <id>
new objects: 5, existing objects: 4
staged: 17 objects, 6003916 bytes
```

Commit as often as you want. One pack takes all the commits that wait.
`noahsark status --repo=<REPO>` prints the same `staged:` line without a
commit, and tells you what to do next.

*... The `staged:` bytes come near to the size of one disc, or a fixed
interval is over, for example one month. Pack now.*

## 4. Pack one disc

```sh
noahsark pack --repo=<REPO>
```

This selects the staged objects that fit one disc and writes the
complete disc root into `<REPO>/staging/plans/<disc uuid>/tree`. Add
`--out=<DISC_DIR>` to choose the directory; it must be empty or absent.
Add `--label=<LABEL>` to choose the label. Add `--capacity=<CAPACITY>`
for one disc of a different size than `pack.capacity`.

`<CAPACITY>` is a preset or a byte size such as `1GB` or `4GiB`. `G` is a
power of 10 and `Gi` is a power of 2. A number without a unit is refused:
give a unit.

| Preset | Media | Bytes |
|---|---|---:|
| `dvd+r` | DVD+R | 4,700,372,992 |
| `dvd-r` | DVD-R | 4,707,319,808 |
| `bd25` | BD-R 25 GB | 25,025,314,816 |
| `bd50` | BD-R DL 50 GB | 50,050,629,632 |
| `bd100` | BD-R XL 100 GB | 100,103,356,416 |
| `bd128` | BD-R XL 128 GB | 128,001,769,472 |

To read the real size of a blank disc:

```sh
dvd+rw-mediainfo <DEVICE> | grep 'Free Blocks'
```

If the block count is less than the preset, multiply it by 2048 and give
that byte size to `--physical-capacity`. `pack` refuses a `--capacity`
above `--physical-capacity`.

To find out how many discs to buy before burning anything, run
`noahsark pack --repo=<REPO> --capacity=<CAPACITY> --dry-run`. It prints
the object count and bytes for each predicted disc, and a total, without
writing anything.

Expected result:

```
packed disc 0 "2026-09-21 disc 0": 7 objects, 3001388 bytes
uuid: 330db42b-893f-388c-6565-0eec93b1841b
tree: /srv/noahsark/repo/staging/plans/330db42b.../tree
next steps:
  sudo noahsark image build --out=<IMAGE> <DISC_DIR>
  growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=<IMAGE>
  noahsark disc burned 0
  noahsark verify <MOUNT>
remaining staged: 0 objects, 0 bytes
```

The `next steps:` block gives the commands of steps 5 to 7, filled in.
Run them in that order. With `--repo` on the `pack` command, the block
repeats `--repo` on each line.

The default label is the name of the newest ref on the disc and the disc
number, for example `2026-09-21 disc 0`.

- `remaining staged: 0 objects, 0 bytes`: all data is packed.
- A remaining count above 0, for example
  `remaining staged: 18 objects, 32003176 bytes`: the data did not fit one
  disc. This is normal, and the exit code is still 0. Do steps 5 to 9 for
  this disc. Then go to step 10.
- `capacity ... holds not one object`, exit code 2: the capacity is too
  small for even the smallest staged object. The message names that object
  and the capacity to use instead.
- `staged <KIND> <ID> is damaged; run commit again ...`, exit code 1: a
  staged object file does not hold the right bytes. Commit again, then
  pack again.

`pack` always packs all staged snapshots and carries all their refs.
`--ref` on `pack` is optional.

## 5. Burn the disc

Build a UDF image, then burn it. `image build` needs root because it
loop-mounts the image.

```sh
sudo noahsark image build --out=<IMAGE> <DISC_DIR>
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z <DEVICE>=<IMAGE>
```

`image build` takes the image length from the `DISC.bin` of `<DISC_DIR>`.
You never repeat the capacity.

`image build` refuses an existing `<IMAGE>`. Add `--force` to replace
it. The burn command leaves the disc open. Do not add `-dvd-compat`.

Optional check before the burn: loop-mount the image and verify it.

```sh
sudo mount -o loop -t udf <IMAGE> <MOUNT>
noahsark verify <MOUNT>
sudo umount <MOUNT>
```

Alternative without an image and without root: burn the directory.

```sh
growisofs -speed=4 -use-the-force-luke=spare:min,tty \
    -Z <DEVICE> -R -iso-level 4 -V NOAHSARK <DISC_DIR>
```

Always use `-R -iso-level 4`. Do not use `-J` alone: Joliet cuts the
68-character object file names. With this alternative, you cannot verify
an image before the burn. Restore and verify read the disc the same way.

## 6. Mark the disc burned

```sh
noahsark disc burned --repo=<REPO> <DISC>
```

This tells the repository that the burn occurred. The tool never
concludes this by itself. Expected result:
`disc 0 2026-09-21 disc 0: marked burned, N objects`.

If the burn was bad, undo the mark:
`noahsark disc burned --repo=<REPO> --undo <DISC>`. The tool refuses the
undo after a successful verify.

## 7. Verify the disc

```sh
sudo mkdir -p <MOUNT>
sudo mount <DEVICE> <MOUNT>
noahsark verify --repo=<REPO> <MOUNT>
sudo umount <MOUNT>
```

This reads every object back and checks it. Expected result:

```
disc 0 "2026-09-21 disc 0": 7 objects, ok
verify: marked 7 object(s) CLEAN (disc 330db42b-...)
verify: copy 1 of 2 verified; verify the second copy before gc
```

Exit code 0.

- `verify: disc 0 is not marked burned`: do step 6, then verify again.
- A mount failure or a verify failure: discard the disc. Burn a new disc
  from the same `<IMAGE>` or `<DISC_DIR>` and verify it. There is no
  recovery of an unmountable disc.

## 8. Burn the second copy

Load a second blank disc. Run the same burn command from step 5 again,
with the same `<IMAGE>` or `<DISC_DIR>`. Then do step 7 for this disc.
Do not pack again. Do not do step 6 again.

Two identical discs are the redundancy of this backup. The second verify
is necessary: `gc` frees the staged data only after two successful
verifies. Expected result of this second verify:
`verify: 2 of 2 copies verified`.

The two copies carry the same disc uuid, thus the tool counts successful
verify passes and cannot see which physical disc you put in the drive.

Keep one copy only? Then put `gc.min_verified_copies = 1` in
`<REPO>/config`. One verify is then sufficient for `gc`.

## 9. Label and store

```sh
noahsark status --repo=<REPO>
```

Expected result:

```
staged: 0 objects, 0 bytes
disc 0 "2026-09-21 disc 0"  verified  330db42b-893f-388c-6565-0eec93b1841b
next: nothing to do
```

Each disc gets one word: `packed`, `burned`, `verified 1/2`, `verified`
or `on disc only`. The `next:` line names the one action to take next.
`verified` means that both copies passed `verify`. After `gc` frees the
staged files, the word becomes `on disc only`: the disc holds every
object and staging holds no file for them. This is normal, and it is what
a recovered disc shows too.

For the exact counts, add `--json`.

Write on each sleeve: the first 8 characters of the uuid, the `seq`, the
label, the date, and `A` or `B`. Store copy B in a different building.
Record the disc and the two locations in a text file near `<REPO>`.

When the two copies are verified, you can delete `<DISC_DIR>` and
`<IMAGE>`.

## 10. Next disc

*... The last pack left data: `remaining staged` was above 0.*

Do steps 4 to 9 again now, with a new `<IMAGE>`. In the example, the
second pack prints `packed disc 1 "2026-09-21 disc 1"` and
`remaining staged: 0 objects, 0 bytes`. Continue until
`remaining staged: 0 objects, 0 bytes`. You can give a different
`--capacity` for each disc.

*... Weeks pass. The discs are in storage. You work as usual and commit
at each interval (step 3).*

When it is time to pack again, do steps 4 to 9. Each new disc holds
only the objects that no earlier disc holds. Thus a restore needs the
earlier discs too.

This build cannot append to a burned disc. `append` is a later-phase
command. More data always goes on a new disc. To seal a disc against
later appends, add `--close` to `pack`. Then use the burn command that
`pack` prints. See OPERATIONS.md, "Disc lifecycle and closing".

## 11. Restore

*... Months later, a file is lost, or you replace the machine.*

Also do a restore drill every few months. Restore some paths and compare them
with the source. `restore`, `ls`, `log` and `verify` with a disc root do
not need `<REPO>`.

### Find the snapshot and the discs

```sh
noahsark log --repo=<REPO>
noahsark restore --repo=<REPO> --mount=<MOUNT> --dry-run <REF> <RESTORE_DIR>
```

`log` lists the snapshots, newest first, with their refs. The first
column is the full snapshot id. `restore` and `ls` take that id in place
of a `<REF>`. `restore --dry-run` lists the discs that the restore needs,
by disc number: the number, the label, the uuid and the objects. It reads
the local cache, not a disc, and writes nothing. It needs `--mount`: with
every disc mounted together instead, there is no disc order to preview.
If `<REPO>` is lost, use `noahsark log <MOUNT>` on the newest disc. Use a
`<REF>` or a snapshot id where a command takes a snapshot.

### One drive

```sh
sudo mount <DEVICE> <MOUNT>
noahsark restore --repo=<REPO> --mount=<MOUNT> <REF> <RESTORE_DIR>
```

This mode needs `<REPO>`. `restore` prints the plan, reads the mounted
disc, then ejects it. For each subsequent disc it prints:

```
insert disc 1 "2026-09-21 run2" (uuid 85f302d6-...) into <MOUNT> and press Enter
```

Load that disc, mount it at `<MOUNT>` and press Enter. A wrong disc
gives `expected disc ..., found ...` and the same prompt again. If the
session stops, run the same command again. It continues and asks only
for the discs that it still needs. Add `--no-eject` to keep the tray
closed.

### All discs mounted

Mount each disc, or copy each disc root, into its own directory below
`<DISCS_DIR>`.

```sh
noahsark restore --discs-dir=<DISCS_DIR> <REF> <RESTORE_DIR>
```

You can also repeat `--disc=<MOUNT>` for each disc. If the snapshot is
on one disc only, `noahsark restore <MOUNT> <REF> <RESTORE_DIR>` is
sufficient.

### Result

Expected result: `restored snapshot <id> into <RESTORE_DIR>`, exit
code 0. The files are below `<RESTORE_DIR><SOURCE>`. Compare them:

```sh
diff -rq <RESTORE_DIR><SOURCE> <SOURCE>
```

`restore` has one report. It names each path that it did not restore on
one warning line, then prints one summary line:

```
noahsark: restore: warning: <PATH>: a path is already here; pass --overwrite to replace it
noahsark: restore: warning: <PATH>: FIFO not restored; this build restores a file, a directory or a symlink only
restored snapshot <id> into <RESTORE_DIR>
not restored: 1 existing path(s), 1 unsupported entry(ies); see the warning(s) above
```

`restore` prints 20 warning lines at most, then one line with the count
of the rest. The summary line counts each kind.

- To restore only some paths, add `--include=<PATH>` one or more times.
  Get the paths from `noahsark ls --recursive --repo=<REPO> <REF>`.
- `restore` does not replace a path that exists, of any kind: a file, a
  directory or a symlink. It leaves the path as it is and names it.
  The exit code is 1. Add `--overwrite` to replace them. `restore`
  never follows a symlink that it finds in `<RESTORE_DIR>`, so it never
  writes outside that directory.
- `restore` never deletes a directory tree, even with `--overwrite`. If
  a non-empty directory stands where a symlink or a file must go, it
  prints
  `noahsark: restore: warning: <PATH>: symlink not created: a directory that is not empty is in the way; restore does not remove it`,
  leaves that directory as it is, and continues with the rest of the
  walk. The exit code is 1.
- `restore` does not restore a device node, a FIFO or a socket. It
  names each one by its kind, `FIFO`, `socket` or `device`, never by a
  number. Every other file is restored, and an unsupported entry alone
  does not change the exit code.
- `resumed: N file(s) already restored` counts the files that were
  already correct.
- A file that `restore` cannot write, because an object on the disc does
  not verify or because the write failed, is a failure: `restore` names
  the file, writes no bad data into it, goes on to the next file, and
  exits 1.
- `restore` applies mode, times and, only when it runs as root, owner to
  every restored path. A field that fails to apply prints
  `noahsark: restore: warning: <PATH>: <FIELD> not applied: <ERROR>`,
  and the exit code is 1. A restore that does not run as root never
  attempts owner at all, so it never prints an owner warning and never
  loses exit code 0 to it.
- `noahsark ls --recursive --unstable-only ...` lists the files that a
  commit flagged as unstable. A `!` in the first column marks them.

See OPERATIONS.md, "Restore".

## 12. Free disk space

*... After many commits, the disk that holds `<REPO>/staging` becomes
full.*

```sh
noahsark gc --repo=<REPO> --dry-run
noahsark gc --repo=<REPO>
```

`gc` deletes staged objects that are verified (CLEAN), verified
`gc.min_verified_copies` times (2 by default), and older than
`staging.retain_after_clean`, 7 days by default. The retention counts
from the first verify. Expected result of the dry run: the totals that
`gc` would delete, or `gc: nothing is eligible yet` with the earliest
date. A real `gc` exits with code 0, also when no object was eligible,
and 1 when it could not delete a staged file.

`gc` prints one line for each disc it holds objects back for:

```
gc: disc 330db42b-893f-388c-6565-0eec93b1841b: 1 of 2 copies verified; 42 object(s) held; verify the second copy
```

Verify the second copy (step 8), then run `gc` again.

- `--force-after=<DURATION>`, for example `1h`, shortens the retention
  for one run. It does not pass by the verify count. It asks for
  confirmation. In a script, pipe the answer in:
  `echo y | noahsark gc --repo=<REPO> --force-after=1h`.
- `gc` never trims the local cache.
- `gc` records an object ON-DISC, and flushes that record, before it
  unlinks the staged file. If the machine stops between the two, the
  next `gc` frees the file that was left behind.
- Do not delete files in `<REPO>/staging` by hand.

See OPERATIONS.md, "Staging state machine".

## 13. Recovery

### The repository directory is lost

*... The disk that holds `<REPO>` fails. Only the discs remain.*

No burned data is lost. Rebuild the state before the next pack.
Otherwise `pack` writes all objects again. Do not run `init` first.

```sh
noahsark recover --repo=<REPO> --discs-dir=<DISCS_DIR>
```

With one drive, run the command one time for each disc, in any order:

```sh
noahsark recover --repo=<REPO> --disc=<MOUNT>
```

Expected result: `recover: ok`, exit code 0. The message
`recover is partial: disc <seq> "<label>" (<uuid>) not fed yet` with exit
code 1 names a disc that you must still feed. An old disc does not know
the newer discs. Thus compare `noahsark status --repo=<REPO>` with your
disc record before you trust `ok`.

Feed every disc, the newest one included, before you `pack` again. If
the true newest disc was never fed, the next `pack` reuses its `seq`.
That is a cosmetic duplicate only: the tool finds a disc by its uuid.
Give the uuid, or a uuid prefix, when two discs share a `seq`.

A recovered disc comes back as ON-DISC: the disc holds its objects, and
staging holds no file for them. Do not run steps 6 and 7 again for such
a disc. `status` prints `on disc only` for it. Add
`sources.root = <SOURCE>` to `<REPO>/config`, or give `<SOURCE>` on each
`commit`. A commit that was not packed before the loss is gone. Commit
again.

### A disc is lost or bad

- One copy is lost: read from the other copy. Burn a new copy from
  `<DISC_DIR>` or `<IMAGE>` if you kept it.
- The two copies are lost: restore the data that the other discs hold.
  `restore` names the objects that it cannot find.

See OPERATIONS.md, "Failure and recovery actions".

### Start a new disc set

The tool never rewrites a burned disc. When the source has changed very
much and you want a complete new set, do steps 2 to 9 with a new
`<REPO>`. Keep the old discs. `consolidate` is a later-phase command.

## 14. Options

- **Rehearsal without a drive.** README.md, "Quick start", runs the full
  cycle on a directory. To include the image path, use `--capacity=1GB`
  for `pack`, then loop-mount the image as in step 5. `image build`
  writes a file of the full capacity.
- **FEC.** Reed-Solomon parity is off by default. Add `--fec` to `pack`,
  or set `fec.scheme = rs255-gf8` in `<REPO>/config`. `--no-fec`
  overrides the config for one pack. FEC uses approximately 9.4% of the
  disc. `noahsark verify --heal <MOUNT>` repairs a run that has FEC. It
  refuses a run without FEC. See OPERATIONS.md, "Verify and heal".
- **Progress.** Long commands print a progress line to stderr. Use
  `--quiet` or `--no-progress` to stop it.
- **Upgrades.** A newer build reads the discs of an older build. A build
  refuses, by name, a format version that it does not know.

## 15. Troubleshooting

| Message | Action |
|---|---|
| `growisofs`: `unexpected errno:No such file or directory` | `<DEVICE>` does not exist. Check the drive connection and the device name. |
| `growisofs`: `unable to open64(...): Permission denied`, exit code 141 | Add your user to the `cdrom` group. Log in again. |
| `growisofs`: `media is not recognized as recordable DVD` | Load a blank BD-R, DVD+R or DVD-R. |
| `image build` refuses the `mkudffs` version | Upgrade `udftools` to 2.3 or later, or burn the directory (step 5, alternative). |
| A disc does not mount, or `verify` fails | Discard the disc. Burn a new copy and verify it. Use the other copy until then. |
| `verify`: disc `is not in repository <REPO>` | Make sure that `--repo` names the repository that packed the disc. If it does, run `recover --disc=<MOUNT>`. |
| `pack`: `capacity: "7500000" has no unit` | A capacity needs a unit or a preset name. Use `bd25`, or a size such as `25GB` or `10GiB`. |
| `pack`: `capacity ... holds not one object`, exit code 2 | The capacity is too small for even the smallest staged object. The message names that object and the capacity to use. |
| `pack`: `staged <KIND> <ID> is damaged` | A staged object file does not hold the right bytes. Run `commit` again, then `pack` again. |
| `pack`: `no capacity` | Set `pack.capacity` in `<REPO>/config`, or give `--capacity`. |
| `gc`: `C of 2 copies verified; N object(s) held` | Only one copy passed `verify`. Burn and verify the second copy (step 8), then run `gc` again. With one copy only, set `gc.min_verified_copies = 1` in `<REPO>/config`. |
| `pack`: `remaining staged`, exit code 0 | The disc packed correctly; the data that did not fit waits for the next disc. Do step 10. |
| `restore --overwrite`: `warning: <PATH>: symlink not created: a directory that is not empty is in the way; restore does not remove it` | A directory holds the path of a symlink or a file in the snapshot. `restore` never deletes a directory tree; it skips `<PATH>` and continues. Move or remove that directory, then restore again to replace it. |
| `restore`: `warning: <PATH>: a path is already here; pass --overwrite to replace it`, exit code 1 | `<RESTORE_DIR>` already holds that path. Restore into an empty directory, or add `--overwrite`. |
| `restore`: `<PATH>: <OBJECT>: content id does not verify`, exit code 1 | The object on the disc is damaged. `restore` writes no bad data and continues with the next file. Use the second copy of the disc, or `verify --heal` when the run has FEC. |
| `restore`: `warning: <PATH>: <FIELD> not applied: <ERROR>`, exit code 1 | The file's data restored, but its mode, times or owner did not. Fix the cause (often a permission problem) and restore again with `--overwrite`. Owner never appears here for a non-root restore: it is not attempted at all. |
| `restore`: `missing disc(s)`, exit code 1 | The message lists each disc by its number, its label and its uuid. Find that disc. Restore again with it included. |
| `restore`: `the snapshot's root tree is not on the provided disc(s)` | Give more discs of the set, the newest discs included. |
| `restore`: `object(s) not found on any provided disc` | A newer disc is absent. Give more discs of the set, the newest discs included. |
| `restore` or `ls`: ref `is not on the provided disc(s)` | A newer disc holds the ref. Give more discs. |
| `is neither a snapshot id nor a known ref name` | The local cache does not know the name. Run `log` to list the names. |
| `no snapshot given; name a ref, or a snapshot id from noahsark log`, exit code 2 | The `<SNAPSHOT>` argument is empty, often an unset shell variable. Give a ref name, or the snapshot id from the first column of `log`. |
| `restore`: `no such disc root: <PATH>` | The first argument of `restore <DISC-ROOT> <SNAPSHOT> <RESTORE_DIR>` must be a mounted disc or an unpacked disc directory. Check the path. |
| `log`: `roots: (none)` | The root tree is on a disc that you did not give. Give all discs, or run `recover`. |
| `restore --dry-run`: `cache: no disc is cached yet` | Run `recover` with a disc, then try `--dry-run` again. |
| `<DISC>`: `matches more than one disc` | Two discs carry the same `seq`. Give the uuid, or the first 8 characters of it, from the list in the message. |
| `no noahsark repository found` | Give `--repo=<REPO>` or set `NOAHSARK_REPO`. |
| `repository lock <REPO>/lock is held; another noahsark command runs on this repository`, exit code 1 | Wait for the other noahsark command to end, then run the command again. |
