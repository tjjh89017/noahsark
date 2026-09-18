# NoahsArk

NoahsArk is a backup system for write-once Blu-ray optical media. It
writes content-addressed, deduplicated objects to discs, using
content-defined chunking and self-describing on-disc tables, and reads
them back years later. Reed-Solomon parity is available per run but off
by default: see "FEC: off by default" below for why, and how to turn
it on.

`docs/walkthrough.md` walks the actual backup routine end to end, on
real discs: first backup, the regular commit-and-pack cycle, burning
and verifying a twin pair, restore drills, and recovery from a lost
repository directory or a failed disc.

In every command example below and under `docs/`, text in angle
brackets, such as `<REPO>` or `<SOURCE>`, is a value you supply.
Everything else is typed exactly as shown.

## Running the restore and heal experiment

Build the CLI, then commit a source tree, pack it, and build a UDF
image. This experiment corrupts and heals a run, so it needs `--fec`:
a run with no FEC (the default) has no parity for `verify --heal` to
repair from.

This experiment needs the source tree, not just the built binary. It
calls `go run ./test/e2e/disc/cmd/ci-corrupt` and `ci-heal` directly,
two test-only tools that live under `test/e2e/disc/cmd`, and it must
run from a checkout of this repository.

The experiment assumes a fresh repository, packed once, so the whole
snapshot fits on this one disc: the `restore` calls below give only
`<MOUNT>`, the single disc root. On an incremental repository,
where the snapshot spans more than one disc, that same restore command
asks for the earlier discs too; give it every disc root instead
(`--disc=` repeated, or `--discs-dir=`), the same as
`docs/walkthrough.md` section 8 describes.

```sh
go build -o noahsark ./cmd/noahsark
./noahsark init --repo=<REPO> --source=<SOURCE>
./noahsark commit --repo=<REPO>
./noahsark pack --repo=<REPO> --capacity=<CAPACITY> --fec --out=<DISC_DIR>
sudo ./noahsark image build --out=<IMAGE> --capacity=<CAPACITY> <DISC_DIR>
```

Mounting a UDF image and corrupting its blocks both need root, which
the CLI never assumes, so the rest of the experiment uses the e2e-only
tools under `test/e2e/disc/cmd`, the same way `test/e2e/disc/run.sh`
does:

```sh
sudo mount -o loop -t udf <IMAGE> <MOUNT>
sudo chown -R "$(id -u):$(id -g)" <MOUNT>

./noahsark restore <MOUNT> <SNAPSHOT_ID> <RESTORE_DIR>
go run ./test/e2e/disc/cmd/ci-corrupt <MOUNT> 3:0 6:0
go run ./test/e2e/disc/cmd/ci-heal <MOUNT>
./noahsark verify --image=<MOUNT>
./noahsark restore <MOUNT> <SNAPSHOT_ID> <RESTORE_DIR>

diff -rq <RESTORE_DIR><SOURCE> <SOURCE>
diff -rq <RESTORE_DIR><SOURCE> <SOURCE>

sudo umount <MOUNT>
```

Both restores should match the original source: healing repairs the
corrupted blocks before the second restore reads them.

`image build` needs root today: it loop-mounts the image it builds to
copy the packed tree in. The default burn line stays open for later
appends and never passes `-dvd-compat`:

```sh
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z <DEVICE>=<IMAGE>
```

## Burning without UDF

`pack` and `image build` are both provided, but `pack` alone already
gives a folder that is the complete disc root. If you do not want the
UDF image, burn that folder with any tool you trust, for example:

```sh
genisoimage -R -iso-level 4 -V NOAHSARK -o <IMAGE> <DISC_DIR>
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z <DEVICE>=<IMAGE>
```

or burn straight from the folder, since growisofs calls genisoimage or
mkisofs itself when given a directory instead of an image:

```sh
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z <DEVICE> -R -iso-level 4 -V NOAHSARK <DISC_DIR>
```

or a GUI burner: point it at the `<DISC_DIR>` folder and burn a data disc
from it, choosing ISO 9660 or UDF as the tool offers.

Use `-R -iso-level 4` (Rock Ridge), never `-J` (Joliet) alone: Joliet
truncates names at 64 characters, which cuts off NoahsArk's
68-character object file names.

NoahsArk reads any filesystem the host can mount, so restore and
verify work the same on a disc burned this way. What you lose: the
image cannot be verified before burning, the mirror kept for later
scrubbing, and conformance to the UDF profile FORMAT.md describes.
NoahsArk's readers also accept a fixed name folded to lowercase, so a
plain ISO 9660 level 4 image with no Rock Ridge (some burners' default)
reads correctly too, not only the Rock Ridge command shown above.

## Packing a disc sequence and restoring across discs

`commit` stages every object once. Each `pack` call then selects as
many STAGED objects as fit the given capacity, in one run, and reports
what is still staged afterward. Running `pack` again, against the same
repository, packs the next run: objects already packed onto an earlier
disc are never copied again, and the new run's `INDEX` names them as
prerequisites of the earlier disc instead.

```sh
./noahsark commit --repo=<REPO> <SOURCE>
./noahsark pack --repo=<REPO> --capacity=dvd+r --out=<DISC_DIR_1>
./noahsark pack --repo=<REPO> --capacity=bd25  --out=<DISC_DIR_2>
./noahsark pack --repo=<REPO> --capacity=<CAPACITY> --out=<DISC_DIR_3>
```

`<SOURCE>` on the `commit` command line overrides the source root
`init --source` stored in the config; drop it to use the configured
root instead.

`pack` exits 0 once nothing is left staged, and 1 while objects remain;
either way it prints the remaining object count and byte total.

## Finding a snapshot to restore

`log` and `ls` read the same disc roots `restore` and `verify` do, so a
snapshot id or a path can be found before running a restore.

`log` lists the snapshots the given discs know, newest first:

```sh
./noahsark log <DISC_DIR>
```

`ls` lists a snapshot's tree, given its id or a ref name from `log`:

```sh
./noahsark ls <DISC_DIR> <SNAPSHOT_ID>
```

`ls --recursive` walks the whole tree and prints each entry's full
path, in the same form `restore --include` takes:

```sh
./noahsark ls --recursive <DISC_DIR> <SNAPSHOT_ID>
```

A path copied from that output restores just that path:

```sh
./noahsark restore --include=<PATH> <DISC_DIR> <SNAPSHOT_ID> <RESTORE_DIR>
```

`restore` accepts that same snapshot id or ref name in place of
`<SNAPSHOT_ID>`, resolved the same way `ls` and `log` resolve it. It
reads from one disc root by default:

```sh
./noahsark restore <DISC_DIR> <SNAPSHOT_ID> <RESTORE_DIR>
```

A snapshot that spans several discs restores by repeating `--disc`, or
by naming a directory whose immediate subdirectories are mounted disc
roots with `--discs-dir`:

```sh
./noahsark restore --disc=<DISC_DIR_1> --disc=<DISC_DIR_2> --disc=<DISC_DIR_3> <SNAPSHOT_ID> <RESTORE_DIR>
./noahsark restore --discs-dir=<DISCS_DIR> <SNAPSHOT_ID> <RESTORE_DIR>
```

A disc root missing from the list fails the restore with an error
naming that disc's uuid and the objects on it the restore needed.

`--include=<PATH>`, repeatable, restores only the named snapshot-relative
paths (the source root's path plus the entry path within it) instead of
the whole snapshot; a path naming a directory restores everything under
it. A multi-disc restore then asks only for the objects those paths
need, so a disc holding none of them can stay out of the drive:

```sh
./noahsark restore --include=<PATH> <DISC_DIR> <SNAPSHOT_ID> <RESTORE_DIR>
```

## Losing the repository directory

`restore` given a disc root, `ls <DISC_DIR>` and `log <DISC_DIR>` never
read `--repo`: they read the disc roots given to them, so losing the
repository directory never loses the archive. `verify` is the same.
`restore --mount` (the single-drive, disc-swap mode) and `ls`, `log` or
`plan` with no disc do need the repository: they resolve the snapshot
through its state, not through a disc root on the command line.

The repository directory does matter to `pack`: it holds the state log
that lets a later `pack` skip objects an earlier disc already carries.
Losing it, then packing again from the same source, would burn every
object a second time. `rebuild-cache --from-disc` rebuilds that state
from the discs themselves, so the next `pack` dedups correctly again:

```sh
./noahsark rebuild-cache --from-disc --repo=<REPO> --disc=<DISC_DIR_1> --disc=<DISC_DIR_2>
```

Give it every disc the repository has burned; a disc left out makes the
rebuild partial, reported as exit 1 naming the missing disc's uuid.
`rebuild-cache` also accepts `--discs-dir`, the same way `restore` does.

## Disc capacity

Marketing sizes are not the real capacity. A "25 GB" BD-R actually holds
25,025,314,816 bytes, not 25,000,000,000. `--capacity` accepts a preset
name for the real, drive-reported sector count of common write-once
media, in addition to a sector count or a byte size like `25GB`:

| Preset | Media | Sectors | Bytes |
|---|---|---:|---:|
| `dvd+r` | DVD+R | 2,295,104 | 4,700,372,992 |
| `dvd-r` | DVD-R | 2,298,496 | 4,707,319,808 |
| `bd25` | BD-R, 25 GB | 12,219,392 | 25,025,314,816 |
| `bd50` | BD-R DL, 50 GB | 24,438,784 | 50,050,629,632 |
| `bd100` | BD-R XL, 100 GB | 48,878,592 | 100,103,356,416 |
| `bd128` | BD-R XL, 128 GB | 62,500,864 | 128,001,769,472 |

To read the real capacity of a specific disc from a drive, use
`dvd+rw-mediainfo` and its `Free Blocks` line:

```sh
dvd+rw-mediainfo <DEVICE> | grep 'Free Blocks'
```

Pass that block count straight to `--capacity` as a sector count.

`--capacity` also takes a plain byte size, with a decimal or a binary
unit suffix, matched case-insensitively. A decimal suffix (`k`, `M`,
`G`, `T`, or `kB`, `MB`, `GB`, `TB`) is a power of 10, the convention
optical media is marketed in. A binary suffix (`Ki`, `Mi`, `Gi`, `Ti`,
or `KiB`, `MiB`, `GiB`, `TiB`) is a power of 2. `G` is not `Gi`:

| Input | Bytes |
|---|---:|
| `25G` or `25GB` | 25,000,000,000 |
| `25Gi` or `25GiB` | 26,843,545,600 |
| `4T` or `4TB` | 4,000,000,000,000 |
| `4Ti` or `4TiB` | 4,398,046,511,104 |
| `512M` or `512MB` | 512,000,000 |
| `512Mi` or `512MiB` | 536,870,912 |

A bare number with no suffix is a sector count, not bytes. A preset
name like `bd25` still names that disc's exact real sector count from
the table above, not a value derived by rounding a marketing size.

## Progress output

`commit`, `pack`, `image build`, `verify` and `restore` each print a
progress line to stderr while they run: bytes done, the total, a
percentage, a throughput and an ETA, or, where no total is known ahead
of time, bytes and throughput alone. On a terminal the line rewrites in
place; piped to a file or CI log, it prints a full line every few
seconds instead. Progress is on by default; pass `--no-progress` or
`--quiet` (`-q`) to turn it off, or `--progress` to force it on when
stderr is not a terminal.

## FEC: off by default

`pack`'s Reed-Solomon checksum column and parity are off by default
(`fec.scheme = none`, format registry value 0). The primary redundancy
this project relies on is burning two identical discs, not parity on
one disc: FEC is a reserve feature you opt into, not a default cost on
every disc.

Turn it on per pack with `--fec`:

```sh
./noahsark pack --repo=<REPO> --capacity=<CAPACITY> --fec --out=<DISC_DIR>
```

or set it for every pack in the repository config:

```
fec.scheme = rs255-gf8
```

`--no-fec` overrides a repository default of `rs255-gf8` back to none
for one pack. `pack` prints which mode it used and the sector budget
that mode gave the run, for example `fec: off, budget used: 6094696
stream blocks`.

Capacity effect: a run with FEC off gives every usable sector, after
the filesystem overhead estimate, to data, with no stripe rounding and
no share held back for a checksum column or parity; a run with FEC on
gives up `(m + 1) / (k + m + 1)`, about 9.4%, of that space to the
checksum column and parity, rounded down to whole stripes. There is
no separate `heal` command; `verify --heal` refuses a run with no FEC
and says so; `verify` still checks every
object's content id and every file's hash either way.

## Dependencies

Forward error correction encodes and decodes with the
`github.com/klauspost/reedsolomon` library; `docs/fec-reference.md`
documents the code by hand for an implementer who does not want the
library.

## Documentation

`FORMAT.md` is the authority for every on-disc byte. `OPERATIONS.md` is
the authority for host-side behaviour: the CLI, configuration, staging,
packing and restore. `NOTES.md` is informative background and rationale.
`docs/decisions.md` records every reading this implementation chose
where those documents left a detail open.

## License

Apache License, Version 2.0. See `LICENSE`.
