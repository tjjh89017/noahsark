# Implementation decisions

Each entry records a reading of FORMAT.md chosen by the implementation. Each
entry is named by the FORMAT.md heading it reads.

## 11.1 INDEX

`container_len`, `body_crc32c` and `header_crc32c` are all derivable from
bytes the same call already lays out: `Index.Encode` computes and writes all
three, overwriting any value the caller set. `Index.Decode` verifies both
CRCs and refuses a mismatch. This does not change the bytes a correct writer
puts on disc; it only fixes which Go call computes them.

## 11.2 REFS and 11.3 DISCS

`RefsTable.Encode` and `DiscsTable.Encode` compute and write `body_crc32c`
and `header_crc32c` the same way, for the same reason. `Decode` for both
verifies both CRCs and refuses a mismatch.

## 10.3 Checksum column

`ChecksumRecord.Encode` computes and writes `header_crc32c` over bytes 0 to
15. `Decode` verifies it and refuses a mismatch.

## 6.1 Object kinds and the object header

`internal/object`'s writer compresses a chunk's payload, but always writes a
blob, a tree and a snapshot with `compression` 0. `Blob.Encode`,
`Tree.Encode` and `Snapshot.Encode` serialize typed fields straight into
their fixed offsets; they hold no opaque payload byte slot a generic
compressor could replace, unlike `Chunk.Payload`. Storing these three kinds
raw is always a valid outcome of the minimum-gain rule, so this does not
change what a reader accepts. Compressing them is future work, not a
disallowed one.

## 6.14 Snapshot

`internal/object`'s `Writer.Commit` takes one source directory and no parent
snapshot, so every commit it writes is a root snapshot: `generation` 1,
`parent` all zero, `parent_hash_algo` 0. Snapshot metadata TLVs (author,
host, message, exclude rules) are omitted, `meta_count` 0, because no
config or CLI layer exists yet to supply them at this layer. Parent
chaining and metadata belong to a later layer that already holds a
repository's snapshot history.

`source_flags` always carries `NO_SPARSE`: the writer never probes
`SEEK_HOLE`, so it never claims sparse detection happened.

## 10.5 Decode rule

`internal/fec`'s `Codec.Decode` takes an explicit set of surviving shards
and applies the base algebraic rule: it picks the `k` shards with the
lowest index and inverts `[I_k ; C]` restricted to those rows, per the
normative choice this heading states. It does not run the single-parity
retry loop itself, because that loop needs the checksum column and the
content ids INDEX maps into the stripe, and `internal/fec` takes byte
slices only, with no knowledge of INDEX or the checksum column's file
layout. The caller (the image package) is expected to try each erasure set
the retry loop names and call `Decode` again for each attempt. This does
not change which parity bytes a conforming writer produces or which stripe
a conforming reader accepts as decoded; it only fixes which package runs
the retry loop.

## 6.6 Tree entry fixed header and 6.7 Entry flags

`internal/object`'s writer always sets `CTIME_ABSENT` clear and
`ATIME_ABSENT` and `BTIME_ABSENT` set. This matches the Phase 1 defaults of
`metadata.ctime` true and `metadata.atime` and `metadata.btime` false; no
config layer exists yet at this layer to override them.

## 11.1 INDEX, Files table order and self-reference

Section 11.1 states Files table rows follow "FEC stream order", then lists
`checksum.bin`, every parity file and `RUN2.bin` as the explicit exceptions
that sit outside that order, appended after every stream row. Read
literally, that leaves `RUN.bin` (role 2) inside the row order the fixed
files follow, even though section 7.3 excludes `RUN.bin`'s own bytes from
the FEC stream. `internal/image` reads this as two different things
sharing one ordering rule: the Files table row order (used to place rows,
and read here as the row-placement order: INDEX, RUN, the first-run-only
fixed files, the catalog files, then every object row) and the FEC stream
(the bytes actually concatenated for parity, which is the same row order
with roles 2, 10, 11 and 12 skipped, exactly as section 11.1 states for
10, 11 and 12, extended here to 2 for the same reason section 7.3 gives).
This does not change any object's, `DISC.bin`'s, `REFS.bin`'s or
`DISCS.bin`'s bytes; it fixes only which row a reader expects at which
position, and the row order is stated in code and verified by round-trip
tests.

Row order also has a self-reference the section text does not resolve:
`INDEX.bin`'s own Files row needs `file_hash`, the hash of INDEX.bin's
whole bytes, but INDEX.bin's bytes are not final until every row,
including this one, is written. The same is true of `RUN.bin`'s row,
because `RUN.bin`'s content (`index_bytes`, `index_hash`) is only known
once INDEX.bin is final, and `RUN.bin` is written after INDEX per the fill
order in section 8.7. `internal/image` takes the simplest deterministic
reading: the Files rows for `INDEX.bin`, `RUN.bin`, `RUN2.bin`,
`checksum.bin` and every parity file carry `file_hash` all zero. Every
other row (`DISC.bin`, `REFERENCE/decoder.py`, `catalog/REFS.bin`,
`catalog/DISCS.bin`, every `catalog/snapobj` copy, and every object file)
carries the real sha256 of its whole encoded bytes, because those files
are fully known before INDEX is built. `RUN.bin`'s own `header_crc32c`
still lets a reader verify it directly; the Files table's `file_hash` for
that row is redundant with that check, not a reader's only way to verify
`RUN.bin`.

Row order for `catalog/snapobj` copies (role 9) and for object files (role
13) is not stated beyond "by file_hash ascending" for role 13.
`internal/image` sorts both groups by the sha256 of each file's own whole
bytes, ascending, and keeps that as the deterministic tie-break for role
9 too (ordered by the snapshot's own content id ascending, since a
snapshot's role-9 copy and its role-13 copy are byte-identical and a
content id is stable where a whole-file hash of role 9's copy is not
otherwise pinned by any other row).

## 8.2 Files at the volume root: README.txt and FORMAT.txt

`internal/image`'s `Build` writes `/NOAHSARK/README.txt` and
`/NOAHSARK/FORMAT.txt` at the volume root, directly after `DISC.bin` and
before `REFERENCE/decoder.py`, matching the fill order section 8.7
states. `FORMAT.txt` is checked in as `internal/image/format.txt`, copied
byte for byte from the Appendix A fenced text; a test compares it against
that text freshly extracted from `FORMAT.md` on every run, so the two
can never drift silently. `README.txt` is built from a template checked
in the same way, `internal/image/readme_template.txt`, with every slot
substituted at build time from the values `Build` writes into `DISC.bin`
and the run header. Both rows carry their real `sha256` in the Files
table and enter the FEC stream, as roles 4 and 5.

## 8.4 README.txt: {hash_algo} and {chunker_profile} source, and {label} scope

Section 8.4's substitution table says `{hash_algo}` and
`{chunker_profile}` come from the superblock's `hash_algo` and
`chunker_profile` fields, but the disc superblock (section 7.5) carries
no such fields: only the run header does. `internal/image` reads this as
the section 3 prose already states it, "hash algorithm of the first run"
and "chunker profile of the first run", and substitutes the values the
first run's header carries, `sha2-256` and `P4`, the only values a Phase
1 writer ever produces. This does not change any byte the superblock or
the run header carries; it only fixes which structure's field a reader
of this document should have named.

`{label}` substitutes the superblock's `label` bytes "as they are". A
Phase 1 writer never stores more than the caller's label text, zero-
padding the rest of the 64-byte field; `internal/image` substitutes only
the meaningful, non-padding bytes rather than the full 64-byte field,
since the padding is not part of the label and would otherwise read as a
run of `?` characters. This does not change `label` or `label_len` on
disc; it only fixes what a human-readable substitution should print.

## 22. Test list: corrupting the real image for the restore-heal-restore CI check

The CI check that proves `internal/restore` works against a real UDF
image, not only an unpacked tree, corrupts the image itself: it loop-
mounts the image read-write, as the existing mount step already does to
populate it, then writes the corrupted bytes directly into the files
under that mount, at the exact file and offset `internal/restore`'s own
stream-layout logic resolves for a chosen `(column, stripe)` pair. A
write through a read-write loop mount lands on the image file's own
sectors, so this is corruption of the image, not a stand-in for it; nothing
about the check depends on an unpacked tree. `ci-corrupt` picks columns 3
and 6 of the fixture's one stripe, `README.txt` and `FORMAT.txt`,
skipping columns 0 and 1, `INDEX.bin` itself: `internal/restore`'s Heal
needs a readable `INDEX.bin` to resolve the stream layout in the first
place, so healing `INDEX.bin`'s own bytes is out of scope for this
implementation (see the `file_index` decision above), and this CI check
does not exercise it.

## 11.1 INDEX, Objects table: resolving an object row's file by file_index

`internal/image`'s `StreamFiles`, used by both `Read`'s parity
verification and `internal/restore`'s `Heal`, used to resolve an object
row's (role 13) file path by sorting every candidate file under
`objects/` and `snapshots/` by its own current `sha256` and matching
that order position by position against the run's object rows, the same
rule Build uses to order those rows at write time. That match only holds
while every object file is intact: corrupting one file's bytes changes
its `sha256` and so its sort position, which silently reassigns every
row from that point on to the wrong file. The Objects table already
carries `file_index`, "0-based row index into the Files table" (section
11.1), naming each object's own row directly and requiring no hash of
the file's current bytes at all. `StreamFiles` now builds an object's
path from its `content_id` and reads `file_index` to place it, so
resolving an object row's file no longer depends on that file being
undamaged, which `internal/restore`'s `Heal` needs before it can even
find the block to repair. A snapobj row (role 9) carries no such field
in this version, so the sha256-sort match, and its intact-file
assumption, still applies there; this does not change any byte on disc
in either case, only how a reader locates the file a row describes.

## 8.1 Profiles a reader must know: filesystem overhead estimate

FORMAT.md gives the `mkudffs` options and the anchor placement rule but no
numeric UDF overhead budget. `internal/image`'s `CheckCapacity` takes the
simplest deterministic reading available from the project's own
measurements: the `probe-udf-small-files` action found small-file overhead
close to one logical block per file. `EstimateFilesystemOverhead` charges
one 2048-byte sector per file plus a fixed 1 MiB base cost for the volume
and partition descriptors, the anchors and the space bitmap. This is an
estimate used only to refuse an over-target pack before spending time
building it; it does not change any on-disc byte.

## 10.1 Profile 0 image build: how the volume is populated

`mkudffs` only makes an empty UDF filesystem; populating it needs a loop
mount, which needs root. `internal/image`'s `MakeImage` runs `mkudffs`
itself (needs no root), then, only when the `NOAHSARK_CI` environment
variable is set, shells out to `populate.sh` through `sudo` to loop-mount
the image, copy the `NOAHSARK` tree in, and unmount. Outside CI,
`MakeImage` builds the empty image and returns, so a build on a developer
machine with no root still exercises the `mkudffs` step and never blocks.
This follows option (c) from the task's own list turned into (a): a
from-scratch Go UDF writer was not attempted, since `udftools`'s own
`mkudffs` plus a root-only mount-and-copy step, run in CI where `sudo` is
available, is the simplest path that reaches conforming UDF bytes with no
new binary format code to maintain.

## 16. CLI reference

`cmd/noahsark` implements the Phase 1 command set: `init`, `commit`,
`pack`, `image build`, `verify` and `restore`. Every command below keeps
OPERATIONS.md's name; a flag is reduced or renamed only when the Go
packages this build calls have no way to honour it yet, since no state
log, ref log, catalog, cache, locality planner or burn plan exists in
this build.

`init` accepts only `--repo` and `--capacity`. `--hash`, `--chunker`,
`--fs-profile` and `--preset` choose among alternatives the fixed
decisions already collapse to one value; `--repo-uuid`,
`--next-run-seq`, `--next-disc-seq` and `--scan-discs` recover sequence
numbers from existing discs, which no multi-disc state exists yet to
scan. `init` writes a flat `key = value` config file, the simplest
format the standard library parses without a third-party dependency,
holding only `repo.uuid`, `staging.dir` and `disc.force_capacity`
(section 17.1, 17.5 and 17.12): every other Phase 1 key needs behaviour
(hash choice, chunker profile, excludes, locality, metadata policy)
this build does not implement, so the config loader refuses any other
key by name rather than accept and ignore it.

`commit` accepts a source path and `--ref`. `--from` and `--copy-first`
are Phase 2 and refused by name; `--out` and `--catalog` are Backlog and
refused by name. `-m`, `--checksum`/`--full-scan`, `--force`,
`--source`, `--source-root`, `--exclude`, `--one-file-system`,
`--source-type` and `--retry-unstable` are Phase 1 but need the quick
check, metadata TLVs, or exclude rules `internal/object`'s `Writer`
does not implement (see the "6.14 Snapshot" entry above); they are not
defined, so passing one is a plain usage error naming the flag. Commit
records the new snapshot under the given ref (default `LATEST`) in a
flat local ref file, `<repo>/refs.txt`, standing in for the local ref
log of section 2.1 and 5.1, since no ref history or state log exists in
this build.

`pack` cannot select objects by `disc.min_fill` or `disc.max_wait`,
because no staging state log exists to age objects in. It instead takes
the snapshot(s) to place explicitly, by `--ref` (default `LATEST`,
resolved through the local ref file) or repeated `--snapshot`.
`--capacity` accepts a bare integer as a sector count, or an integer
suffixed `GiB`/`MiB`/`KiB` (binary) or `GB`/`MB`/`KB` (decimal, the
marketing convention optical media capacities like "25GB" are named
in), converted to whole sectors at FORMAT.md's 2048-byte sector size,
rounding up; it falls back to the config's `disc.force_capacity` and
refuses to run with neither set, per the fixed decision that every pack
takes a capacity. `--disc` is refused by name: it means continuing an
existing disc, Phase 2 append, which `internal/image`'s `Build` does
not support. `--reserve`, `--extra-reserve`, `--preset`, `--now`,
`--close` and `--dry-run` are not defined, since `Build` has no such
options.

`image build` takes the packed tree directory directly, in place of
OPERATIONS.md's `--run=SEQ`, because no run-sequence state exists to
resolve a run number against; the tree directory is what `pack --out`
already printed. `--capacity` is required for the same reason `pack`
requires it: `MakeImage` needs an explicit sector length, and there is
no stored run capacity to default to.

`verify --image=PATH` repurposes `--image` to mean a mounted disc path
or an unpacked NOAHSARK tree, the root `internal/image`'s `Read` and
`internal/restore`'s `Heal` already accept, rather than a raw image
file plus `--mapfile`: mounting an image file needs root, which this
build never assumes outside CI. `--level`, `--drive`, `--report`,
`--disc` and `--run` are not defined, since no drive or repository
state exists for them to select among.

`restore` takes `DISC-ROOT SNAPSHOT OUT-DIR` positionally, in place of
OPERATIONS.md's `restore SNAPSHOT TARGET`, because resolving `SNAPSHOT`
through a repository's catalog and cache needs both, and neither exists
in this build; the caller instead names the disc root directly, the
same root `internal/restore`'s `Restore` already takes. `--no-xattr`
and `--no-acl` are Phase 2 and refused by name; `--translate-acl` is
Phase 2 and refused by name. Every other restore flag
(`--plan`, `--include`, `--drives`, `--staging-budget`, `--interactive`,
`--no-eject`, `--overwrite`, `--no-owner`, `--numeric-owner`,
`--no-flags`, `--no-times`, `--no-hardlinks`, `--metadata-strict`,
`--report`, `--report-replay`, `--strict-unstable`) is Phase 1 but not
defined, since `Restore` takes no such option today.

None of these reductions change any byte a conforming writer puts on a
disc or a conforming reader accepts; they change only which command-line
surface reaches the same Go calls the rest of this implementation
already exposes.
