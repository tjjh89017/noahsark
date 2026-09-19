# Noah's Ark

Noah's Ark (`noahsark`) is a backup tool for write-once optical discs:
BD-R, DVD+R and DVD-R. It splits files into deduplicated,
content-addressed objects and writes them to self-describing discs. A
disc can be read back years later without the repository. Two identical
discs are the redundancy. Reed-Solomon parity is optional and off by
default.

## Quick start

This runs a full cycle on a directory. It needs no drive and no root.
Text in angle brackets is a value that you supply.

```sh
go build -o noahsark ./cmd/noahsark
./noahsark init --repo=<REPO> --source=<SOURCE>
# ... work as usual in <SOURCE>, then commit
./noahsark commit --repo=<REPO> --ref=<REF>
./noahsark pack --repo=<REPO> --capacity=1GB --out=<DISC_DIR>
./noahsark verify <DISC_DIR>
./noahsark restore <DISC_DIR> <REF> <RESTORE_DIR>
diff -rq <RESTORE_DIR><SOURCE> <SOURCE>
```

`diff` prints no line when the restore is correct. Give `<SOURCE>` to
`diff` as an absolute path: the restored tree holds the full source path.

To burn, verify and restore real discs, follow the
[operator guide](docs/guide.md).

## Documentation

- [docs/guide.md](docs/guide.md): the operator guide, from
  install to restore, disc capacities, FEC, recovery and troubleshooting.
- [docs/decisions.md](docs/decisions.md): the choices this
  implementation made where the design left a detail open.
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
