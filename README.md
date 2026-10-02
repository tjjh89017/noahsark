# Noah's Ark

Noah's Ark (`noahsark`) is a backup tool for write-once optical discs:
BD-R, DVD+R and DVD-R. It splits files into deduplicated,
content-addressed objects and writes them to self-describing discs. A
disc can be read back years later without the repository. The tool counts
one verified disc for each pack; a second copy is the job of the operator.
The tool writes no repair data: the second copy of a disc is the repair.

There are two methods to write a disc root to a disc. The recommended
method is `image build`, which makes a UDF image, and then a burn of the
image. The second method burns the disc root folder directly as ISO 9660
with `growisofs`.

## Quick start

This runs the full cycle on a directory. It needs no drive and no root.
Text in angle brackets is a value that you supply. `<REF>` is your name
for one commit; by default it is the date, for example `2026-09-14`.

```sh
go install ./cmd/noahsark          # into $(go env GOPATH)/bin; put it on PATH
mkdir -p <REPO> && cd <REPO>
noahsark init --source=<SOURCE>
# ... work as usual in <SOURCE>, then commit
noahsark commit --ref=<REF> -m "first backup"
noahsark pack --capacity=bd25 --out=<DISC_DIR>
noahsark verify <DISC_DIR>         # a real disc: verify <MOUNT>
noahsark status
noahsark restore --disc=<DISC_DIR> <REF> <RESTORE_DIR>
diff -r <SOURCE> <RESTORE_DIR>
```

`status` prints what is staged, the state of each disc, and one `next:`
block with the lines to run next. Run it at any stage. `verify` of a
directory checks every byte, but it prints `not counted: this is not a
disc`: only a read-only mount of a disc counts. `restore` writes the
content of the source root into `<RESTORE_DIR>`, thus `diff` prints no
line when the restore is correct.

To burn, verify and restore real discs, follow the
[operator guide](docs/guide.md).

## Documentation

- [docs/guide.md](docs/guide.md): the operator guide: a walk-through for
  one drive, from the set-up to a restore after a lost computer.
- [FORMAT.md](FORMAT.md): the specification of every on-disc byte.
- [OPERATIONS.md](OPERATIONS.md): the specification of host-side
  behaviour, the CLI and the configuration.
- [docs/states.md](docs/states.md): the specification of the item and
  disc states, the state x event table, and the `next:` blocks of
  `status`.
- [docs/decisions.md](docs/decisions.md): the decisions of the project,
  by topic: what, and why.
- [NOTES.md](NOTES.md): background and rationale.

## License

Apache License, Version 2.0. See [LICENSE](LICENSE).
