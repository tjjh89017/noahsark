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
the CLI never assumes, so the rest of the experiment uses the CI-only
tools under `internal/image/cmd` and `internal/restore/cmd`, the same
way `.github/actions/test/action.yml` does:

```sh
sudo mount -o loop -t udf run.img /mnt/noahsark
sudo chown -R "$(id -u):$(id -g)" /mnt/noahsark

./noahsark restore /mnt/noahsark SNAPSHOT-ID restore-before
go run ./internal/restore/cmd/ci-corrupt /mnt/noahsark 3:0 6:0
go run ./internal/restore/cmd/ci-heal /mnt/noahsark
./noahsark verify --image=/mnt/noahsark
./noahsark restore /mnt/noahsark SNAPSHOT-ID restore-after

diff -rq restore-before/path/to/source /path/to/source
diff -rq restore-after/path/to/source /path/to/source

sudo umount /mnt/noahsark
```

Both restores should match the original source: healing repairs the
corrupted blocks before the second restore reads them.

## Documentation

`FORMAT.md` is the authority for every on-disc byte. `OPERATIONS.md` is
the authority for host-side behaviour: the CLI, configuration, staging,
packing and restore. `NOTES.md` is informative background and rationale.
`docs/decisions.md` records every reading this implementation chose
where those documents left a detail open.

## License

Apache License, Version 2.0. See `LICENSE`.
