# NoahsArk

NoahsArk is a backup system for write-once Blu-ray optical media. It
writes content-addressed, deduplicated objects to discs with
Reed-Solomon parity, using content-defined chunking and self-describing
on-disc tables, and reads them back years later, healing a damaged run
from that parity.

## Running the restore and heal experiment

Build the CLI, then commit a source tree, pack it, and build a UDF
image:

```sh
go build -o noahsark ./cmd/noahsark
./noahsark init --repo=repo --capacity=25GB
./noahsark commit --repo=repo /path/to/source
./noahsark pack --repo=repo --capacity=25GB --out=tree
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

## Documentation

`FORMAT.md` is the authority for every on-disc byte. `OPERATIONS.md` is
the authority for host-side behaviour: the CLI, configuration, staging,
packing and restore. `NOTES.md` is informative background and rationale.
`docs/decisions.md` records every reading this implementation chose
where those documents left a detail open.

## License

Apache License, Version 2.0. See `LICENSE`.
