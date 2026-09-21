# Noah's Ark

Noah's Ark (`noahsark`) is a backup tool for write-once optical discs:
BD-R, DVD+R and DVD-R. It splits files into deduplicated,
content-addressed objects and writes them to self-describing discs. A
disc can be read back years later without the repository. Two identical
discs are the redundancy. Reed-Solomon parity is optional and off by
default.

## Quick start

This runs the full cycle on a directory. It needs no drive and no root.
Text in angle brackets is a value that you supply. `<REF>` is your name
for one commit; use the date, for example `2026-09-14`.

```sh
go build -o noahsark ./cmd/noahsark
./noahsark init --repo=<REPO> --source=<SOURCE>
echo "pack.capacity = bd25" >> <REPO>/config
export NOAHSARK_REPO=<REPO>
# ... work as usual in <SOURCE>, then commit
./noahsark commit --ref=<REF>
./noahsark pack --out=<DISC_DIR>
# ... with a real disc: build the image, burn it, mount it at <MOUNT>
./noahsark disc burned 0
./noahsark verify <DISC_DIR>      # a real disc: verify <MOUNT>
./noahsark verify <DISC_DIR>      # the second copy
./noahsark status
./noahsark restore <DISC_DIR> <REF> <RESTORE_DIR>
diff -rq <RESTORE_DIR><SOURCE> <SOURCE>
```

`status` prints what is staged, the state of each disc in one word, and
one `next:` line that names the action to do next. Run it at any stage.
`diff` prints no line when the restore is correct. Give `<SOURCE>` as an
absolute path: the restored tree holds the full source path.

To burn, verify and restore real discs, follow the
[operator guide](docs/guide.md).

## Documentation

- [docs/guide.md](docs/guide.md): the operator guide: the cycle on one
  page, then a reference for restore, gc, recovery, FEC and troubleshooting.
- [docs/decisions.md](docs/decisions.md): the decisions of the project,
  by topic: what, and why.
- [docs/fec-reference.md](docs/fec-reference.md): the Reed-Solomon code,
  written out by hand. The implementation uses
  `github.com/klauspost/reedsolomon`.
- [FORMAT.md](FORMAT.md): the authority for every on-disc byte.
- [OPERATIONS.md](OPERATIONS.md): the authority for host-side behaviour,
  the CLI and the configuration.
- [NOTES.md](NOTES.md): background and rationale.
- [test/e2e/disc/run.sh](test/e2e/disc/run.sh): the corrupt-and-heal
  experiment with `--fec`.

## License

Apache License, Version 2.0. See [LICENSE](LICENSE).
