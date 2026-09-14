# NoahsArk

NoahsArk is a backup system for write-once Blu-ray optical media. It
writes content-addressed, deduplicated objects to discs, using
content-defined chunking and self-describing on-disc tables, and reads
them back years later. Reed-Solomon parity is available per run but off
by default: see "FEC: off by default" below for why, and how to turn
it on.

## Running the restore and heal experiment

Build the CLI, then commit a source tree, pack it, and build a UDF
image. This experiment corrupts and heals a run, so it needs `--fec`:
a run with no FEC (the default) has no parity for `heal` to repair from.

```sh
go build -o noahsark ./cmd/noahsark
./noahsark init --repo=repo --capacity=25GB
./noahsark commit --repo=repo /path/to/source
./noahsark pack --repo=repo --capacity=25GB --fec --out=tree
./noahsark image build --out=run.img --capacity=25GB tree
```

Mounting a UDF image and corrupting its blocks both need root, which
the CLI never assumes, so the rest of the experiment uses the e2e-only
tools under `test/e2e/disc/cmd`, the same way `test/e2e/disc/run.sh`
does:

```sh
sudo mount -o loop -t udf run.img /mnt/noahsark
sudo chown -R "$(id -u):$(id -g)" /mnt/noahsark

./noahsark restore /mnt/noahsark SNAPSHOT-ID restore-before
go run ./test/e2e/disc/cmd/ci-corrupt /mnt/noahsark 3:0 6:0
go run ./test/e2e/disc/cmd/ci-heal /mnt/noahsark
./noahsark verify --image=/mnt/noahsark
./noahsark restore /mnt/noahsark SNAPSHOT-ID restore-after

diff -rq restore-before/path/to/source /path/to/source
diff -rq restore-after/path/to/source /path/to/source

sudo umount /mnt/noahsark
```

Both restores should match the original source: healing repairs the
corrupted blocks before the second restore reads them.

`image build` needs root today: it loop-mounts the image it builds to
copy the packed tree in. The default burn line stays open for later
appends and never passes `-dvd-compat`:

```sh
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=run.img
```

## Burning without UDF

`pack` and `image build` are both provided, but `pack` alone already
gives a folder that is the complete disc root. If you do not want the
UDF image, burn that folder with any tool you trust, for example:

```sh
genisoimage -R -iso-level 4 -V NOAHSARK -o run.iso tree
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=run.iso
```

or burn straight from the folder, since growisofs calls genisoimage or
mkisofs itself when given a directory instead of an image:

```sh
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0 -R -iso-level 4 -V NOAHSARK tree
```

or a GUI burner: point it at the `tree` folder and burn a data disc
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
./noahsark commit --repo=repo /path/to/big/source
./noahsark pack --repo=repo --capacity=dvd+r --out=tree0
./noahsark pack --repo=repo --capacity=bd25  --out=tree1
./noahsark pack --repo=repo --capacity=10GB  --out=tree2
```

`pack` exits 0 once nothing is left staged, and 1 while objects remain;
either way it prints the remaining object count and byte total.

## Finding a snapshot to restore

`log` and `ls` read the same disc roots `restore` and `verify` do, so a
snapshot id or a path can be found before running a restore.

`log` lists the snapshots the given discs know, newest first:

```sh
./noahsark log tree0
```

`ls` lists a snapshot's tree, given its id or a ref name from `log`:

```sh
./noahsark ls tree0 SNAPSHOT-ID
```

`ls --recursive` walks the whole tree and prints each entry's full
path, in the same form `restore --include` takes:

```sh
./noahsark ls --recursive tree0 SNAPSHOT-ID
```

A path copied from that output restores just that path:

```sh
./noahsark restore tree0 --include=srv/data/etc SNAPSHOT-ID restored
```

`restore` reads from one disc root by default:

```sh
./noahsark restore tree0 SNAPSHOT-ID restored
```

A snapshot that spans several discs restores by repeating `--disc`, or
by naming a directory whose immediate subdirectories are mounted disc
roots with `--discs-dir`:

```sh
./noahsark restore --disc=tree0 --disc=tree1 --disc=tree2 SNAPSHOT-ID restored
./noahsark restore --discs-dir=/mnt/noahsark-discs SNAPSHOT-ID restored
```

A disc root missing from the list fails the restore with an error
naming that disc's uuid and the objects on it the restore needed.

`--include=PATH`, repeatable, restores only the named snapshot-relative
paths (the source root's path plus the entry path within it) instead of
the whole snapshot; a path naming a directory restores everything under
it. A multi-disc restore then asks only for the objects those paths
need, so a disc holding none of them can stay out of the drive:

```sh
./noahsark restore tree0 --include=srv/data/etc SNAPSHOT-ID restored
```

## Losing the repository directory

`restore`, `verify`, `ls` and `log` never read `--repo`: they read the
disc roots given to them, so losing the repository directory never
loses the archive, and never blocks a restore.

The repository directory does matter to `pack`: it holds the state log
that lets a later `pack` skip objects an earlier disc already carries.
Losing it, then packing again from the same source, would burn every
object a second time. `rebuild-cache --from-disc` rebuilds that state
from the discs themselves, so the next `pack` dedups correctly again:

```sh
./noahsark rebuild-cache --from-disc --repo=repo --disc=tree0 --disc=tree1
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
dvd+rw-mediainfo /dev/sr0 | grep 'Free Blocks'
```

Pass that block count straight to `--capacity` as a sector count.

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
./noahsark pack --repo=repo --capacity=25GB --fec --out=tree
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
checksum column and parity, rounded down to whole stripes. `heal`
refuses a run with no FEC and says so; `verify` still checks every
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
