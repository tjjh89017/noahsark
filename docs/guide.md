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
| `<LABEL>` | text that names one disc | `"2026-09-14 run1"` |
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
inside `<REPO>`.

## 2. Create the repository

```sh
noahsark init --repo=<REPO> --source=<SOURCE>
```

Do this one time. Expected result: `initialized repository <REPO>`, and
`<REPO>/config` holds `repo.uuid`, `staging.dir` and `sources.root`.

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

To commit a different directory one time, add it as an argument:
`noahsark commit --repo=<REPO> --ref=<REF> <SOURCE>`.

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
`noahsark disc list --repo=<REPO>` prints the same `staged:` line
without a commit.

*... The `staged:` bytes come near to the size of one disc, or a fixed
interval is over, for example one month. Pack now.*

## 4. Pack one disc

```sh
noahsark pack --repo=<REPO> --capacity=<CAPACITY> --label=<LABEL> --out=<DISC_DIR>
```

This selects the staged objects that fit one disc and writes the
complete disc root into `<DISC_DIR>`. `--capacity` is mandatory on every
pack. `<DISC_DIR>` must be empty or absent.

`<CAPACITY>` is a preset, a sector count (a number without a unit), or a
byte size such as `1GB` or `4GiB`. `G` is a power of 10 and `Gi` is a
power of 2.

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

If the block count is less than the preset, add
`--physical-capacity=<BLOCK_COUNT>` to the `pack` command. `pack` refuses a
`--capacity` above `--physical-capacity`.

Expected result: a line `packed run 1 on disc <seq> into <DISC_DIR>`, a
`next steps:` block with the commands of steps 5 to 7 filled in, and the
line `remaining staged: N objects, B bytes`.

- Exit code 0 and `remaining staged: 0 objects, 0 bytes`: all data is
  packed.
- Exit code 1 and a remaining count above 0, for example
  `remaining staged: 18 objects, 32003176 bytes`: the data did not fit.
  Do steps 5 to 9 for this disc. Then go to step 10.

`pack` always packs all staged snapshots and carries all their refs.
`--ref` on `pack` is optional.

## 5. Burn the disc

Build a UDF image, then burn it. `image build` needs root because it
loop-mounts the image.

```sh
sudo noahsark image build --out=<IMAGE> --capacity=<CAPACITY> <DISC_DIR>
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z <DEVICE>=<IMAGE>
```

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
`disc 0 2026-09-14 run1: marked burned, N objects`.

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

This reads every object back and checks it. Expected result: the lines
`verify: marked N object(s) CLEAN` and `verify: ok`, exit code 0.

- `verify: disc 0 is not marked burned`: do step 6, then verify again.
- A mount failure or a verify failure: discard the disc. Burn a new disc
  from the same `<IMAGE>` or `<DISC_DIR>` and verify it. There is no
  recovery of an unmountable disc.

## 8. Burn the second copy

Load a second blank disc. Run the same burn command from step 5 again,
with the same `<IMAGE>` or `<DISC_DIR>`. Then do step 7 for this disc.
Do not pack again. Do not do step 6 again.

Two identical discs are the redundancy of this backup.

## 9. Label and store

```sh
noahsark disc list --repo=<REPO>
```

Expected result, one line for each disc:

```
330db42b-893f-388c-6565-0eec93b1841b  seq=0  label="2026-09-14 run1"  ...  objects=7  packed=0  clean=7
```

`packed=0` shows that the disc is burned and verified. After `gc`
deletes the staged objects, `clean` also goes to 0. This is normal.

Write on each sleeve: the first 8 characters of the uuid, the `seq`, the
label, the date, and `A` or `B`. Store copy B in a different building.
Record the disc and the two locations in a text file near `<REPO>`.

When the two copies are verified, you can delete `<DISC_DIR>` and
`<IMAGE>`.

## 10. Next disc

*... The last pack left data: `remaining staged` was above 0.*

Do steps 4 to 9 again now, with a new `<DISC_DIR>`, `<IMAGE>` and
`<LABEL>`. In the example, the second pack prints
`packed run 2 on disc 1` and `remaining staged: 0 objects, 0 bytes`.
Continue until `pack` exits with code 0. You can use a different
`--capacity` for each disc.

*... Weeks pass. The discs are in storage. You work as usual and commit
at each interval (step 3).*

When it is time to pack again, do steps 4 to 9. Each new disc holds
only the objects that no earlier disc holds. Thus a restore needs the
earlier discs too.

This build cannot append to a burned disc. `append` is a later-phase
command. More data always goes on a new disc. To seal a disc against
later appends, add `--close` to `pack`. Then use the burn command that
`pack` prints. See OPERATIONS.md, "Disc lifecycle, closing and
appending".

## 11. Restore

*... Months later, a file is lost, or you replace the machine.*

Also do a restore drill every few months. Restore some paths and compare them
with the source. `restore`, `ls`, `log` and `verify` with a disc root do
not need `<REPO>`.

### Find the snapshot and the discs

```sh
noahsark log --repo=<REPO>
noahsark plan --repo=<REPO> <REF>
```

`log` lists the snapshots, newest first, with their refs. `plan` lists
the discs that the restore needs: uuid, label, objects and bytes. The two
commands read the local cache, not a disc. If `<REPO>` is lost, use
`noahsark log <MOUNT>` on the newest disc. Use a `<REF>` or a snapshot id
where a command takes a snapshot.

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
closed. Add `--staging-budget=<SIZE>` to limit the temporary disk space.

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

- To restore only some paths, add `--include=<PATH>` one or more times.
  `plan` takes the same flag. Get the paths from
  `noahsark ls --recursive --repo=<REPO> <REF>`.
- `restore` does not replace a path that exists, of any kind: a file, a
  directory or a symlink. It leaves the path as it is and prints
  `skipped N existing path(s)`. The exit code is 1. Add `--overwrite`
  to replace them. `restore` never follows a symlink that it finds in
  `<RESTORE_DIR>`, so it never writes outside that directory.
- `restore` never deletes a directory tree, even with `--overwrite`. If
  a non-empty directory stands where a symlink or a file must go, it
  prints
  `noahsark: restore: warning: <PATH>: a directory that is not empty is in the way; restore does not remove it`,
  leaves that directory as it is, counts it as skipped, and continues
  with the rest of the walk. The summary line then reads
  `skipped N existing path(s); --overwrite could not replace them; see the warning(s) above`.
- `restore` does not restore a device node, a FIFO or a socket. It
  names each one on a warning line and prints
  `not restored: N unsupported entry(ies); a device node, FIFO or socket needs a later phase`.
  Every other file is restored, and an unsupported entry alone does not
  change the exit code.
- `resumed: N file(s) already restored` counts the files that were
  already correct.
- `restore` applies mode, times and, only when it runs as root, owner to
  every restored path. A field that fails to apply prints
  `noahsark: restore: warning: <PATH>: <FIELD> not applied: <ERROR>`,
  up to 20 lines, then one line with the remaining count. The summary
  line reads `metadata not applied: N field(s); see the warning(s)
  above`, and the exit code is 1. A restore that does not run as root
  never attempts owner at all, so it never prints an owner warning and
  never loses exit code 0 to it.
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

`gc` deletes staged objects that are verified (CLEAN) and older than
`staging.retain_after_clean`, 7 days by default. Expected result of the
dry run: the totals that `gc` would delete, or
`gc: nothing is eligible yet` with the earliest date. A real `gc` exits
with code 0 after it deletes objects, 1 if no object was eligible, and 2
on a failure.

- `--force-after=<DURATION>`, for example `1h`, shortens the retention
  for one run. It asks for confirmation. Add `--yes` in a script.
- `--keep-snapshots=<N>` also trims the local cache to the newest N
  snapshots. By default, `gc` does not trim the cache.
  `rebuild-cache` restores trimmed data.
- Do not delete files in `<REPO>/staging` by hand.

See OPERATIONS.md, "Staging state machine".

## 13. Recovery

### The repository directory is lost

*... The disk that holds `<REPO>` fails. Only the discs remain.*

No burned data is lost. Rebuild the state before the next pack.
Otherwise `pack` writes all objects again. Do not run `init` first.

```sh
noahsark rebuild-cache --repo=<REPO> --discs-dir=<DISCS_DIR>
```

With one drive, run the command one time for each disc, in any order:

```sh
noahsark rebuild-cache --repo=<REPO> --disc=<MOUNT>
```

Expected result: `rebuild-cache: ok`, exit code 0. The message
`rebuild is partial: disc <uuid> (<label>) not fed yet` with exit code 1
names a disc that you must still feed. An old disc does not know the
newer discs. Thus compare `noahsark disc list --repo=<REPO>` with your
disc record before you trust `ok`.

Feed every disc, the newest one included, before you `pack` again. On
`ok`, `rebuild-cache` warns on stderr which disc it treats as the
newest fed and which `run_seq` and `disc_seq` the next `pack` assigns.
If the true newest disc was never fed, `pack` reuses its numbers; if
that disc is lost for good, the disc uuid still tells the two runs apart.

Then, for each disc, do steps 6 and 7 again. The rebuilt state does not
know that a disc was burned or verified. Add `sources.root = <SOURCE>`
to `<REPO>/config`, or give `<SOURCE>` on each `commit`. A commit that
was not packed before the loss is gone. Commit again.

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
  for `pack` and `image build`, then loop-mount the image as in step 5.
  `image build` writes a file of the full capacity.
- **FEC.** Reed-Solomon parity is off by default. Add `--fec` to `pack`,
  or set `fec.scheme = rs255-gf8` in `<REPO>/config`. `--no-fec`
  overrides the config for one pack. FEC uses approximately 9.4% of the
  disc. `noahsark verify --heal <MOUNT>` repairs a run that has FEC. It
  refuses a run without FEC. See OPERATIONS.md, "Verify, scrub and heal".
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
| `verify`: disc `is not in repository <REPO>` | Make sure that `--repo` names the repository that packed the disc. If it does, run `rebuild-cache --disc=<MOUNT>`. |
| `pack`: `remaining staged`, exit code 1 | The data did not fit. Do step 10. |
| `restore --overwrite`: `warning: <PATH>: a directory that is not empty is in the way; restore does not remove it` | A directory holds the path of a symlink or a file in the snapshot. `restore` never deletes a directory tree; it skips `<PATH>` and continues. Move or remove that directory, then restore again to replace it. |
| `restore`: `warning: <PATH>: <FIELD> not applied: <ERROR>`, exit code 1 | The file's data restored, but its mode, times or owner did not. Fix the cause (often a permission problem) and restore again with `--overwrite`. Owner never appears here for a non-root restore: it is not attempted at all. |
| `restore`: `missing disc(s)`, exit code 3 | The message lists each disc by uuid. Find the disc by the uuid prefix on its sleeve. Restore again with that disc included. |
| `restore`: `the snapshot's root tree is not on the provided disc(s)` | Give more discs of the set, the newest discs included. |
| `restore`: `object(s) not found on any provided disc` | A newer disc is absent. Give more discs of the set, the newest discs included. |
| `restore` or `ls`: ref `is not on the provided disc(s)` | A newer disc holds the ref. Give more discs. |
| `is neither a snapshot id nor a known ref name` | The local cache does not know the name. Run `log` to list the names. |
| `log`: `roots: (none)` | The root tree is on a disc that you did not give, or `gc` trimmed it from the cache. Give all discs, or run `rebuild-cache`. |
| `plan`: `cache: no run is cached yet` | Run `rebuild-cache` with a disc, then plan again. |
| `no noahsark repository found` | Give `--repo=<REPO>` or set `NOAHSARK_REPO`. |
| `repository lock <REPO>/lock is held by pid <PID>` | Wait for the other noahsark command to end. |
