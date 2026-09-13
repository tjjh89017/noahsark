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

Sparse file handling (`SEEK_HOLE` probing, the tree entry `SPARSE` flag,
hole punching on restore) is deferred to Phase 2. In Phase 1, a hole is
ordinary zero data: the writer never probes `SEEK_HOLE`, so `source_flags`
always carries `NO_SPARSE`, and it never claims sparse detection happened.

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
numeric UDF overhead budget. The first version of this estimate, one
2048-byte sector per file plus a fixed 1 MiB base cost, proved too
optimistic: on a real dvd+r image, pack selected a run that left only
about 140 sectors of unmodelled slack, and populating the built image
with `cp -a` failed with "No space left on device". A smaller pack on
the same image, with about 70,000 sectors of slack, populated fine.

`EstimateFilesystemOverhead` now charges four terms, checked against a
real mkudffs UDF 2.01 image, loop-mounted and measured with `df`:

- A fixed base: the space bitmap (one bit per partition sector, rounded
  up to whole blocks) plus a flat 4 MiB margin for the partition
  reservations, the anchors, and allocation descriptors this estimate
  does not itemise.
- Two blocks per file: a File Entry plus a share of its parent
  directory's FID space. A real image measured 4096 bytes (2 blocks)
  per file once the object fanout directories already exist, rising a
  little past that as directories grow past their first block; the flat
  2-block charge plus the margin below covers the difference.
- Two blocks per directory, bounded by the run tree's own layout: at
  most one directory per `objects/<ab>` fanout prefix (256 of them)
  plus the tree's fixed top-level directories, whatever the file count.
- A proportional margin of 0.1% of capacity, for costs that scale with
  the volume rather than the file count.

At dvd+r capacity the fixed 4 MiB margin plus the proportional 0.1% term
alone add up to about 8.5 MiB of slack beyond the itemised bitmap,
per-file and per-directory costs, guarded by a test so a regression back
toward a thin margin fails. This is still an estimate used only to
refuse an over-target pack before spending time building it; it does not
change any on-disc byte.

## 8.1 Profiles a reader must know: keeping files out of the ICB

Small files may be embedded in-ICB by the UDF driver. The writer does not
pad and sets no mount option. NoahsArk works at the file level and never
depends on how a filesystem stores a file. A disc whose filesystem cannot
be mounted counts as lost. Recovery by carving is not a supported
operation, even though a carving reader exists in `internal/format`. This
differs from the sector-boundary rule in FORMAT.md; the user will decide
on a FORMAT.md change.

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
`--capacity` accepts a bare integer as a sector count, an integer
suffixed `GiB`/`MiB`/`KiB` (binary) or `GB`/`MB`/`KB` (decimal, the
marketing convention optical media capacities like "25GB" are named
in), converted to whole sectors at FORMAT.md's 2048-byte sector size,
rounding up, or a preset name for the real, drive-reported sector count
of common write-once media, since a marketing size is not the real
capacity a drive reports:

| Preset | Media | Sectors | Bytes |
|---|---|---:|---:|
| `dvd+r` | DVD+R | 2,295,104 | 4,700,372,992 |
| `dvd-r` | DVD-R | 2,298,496 | 4,707,319,808 |
| `bd25` | BD-R, 25 GB | 12,219,392 | 25,025,314,816 |
| `bd50` | BD-R DL, 50 GB | 24,438,784 | 50,050,629,632 |
| `bd100` | BD-R XL, 100 GB | 48,878,592 | 100,103,356,416 |
| `bd128` | BD-R XL, 128 GB | 62,500,864 | 128,001,769,472 |

`--capacity` falls back to the config's `disc.force_capacity` and
refuses to run with neither set, per the fixed decision that every pack
takes a capacity. `--physical-capacity` takes the same forms (sectors,
a preset, or a byte size) and sets the disc's physical capacity,
`capacity_sectors` in the superblock, separately from `--capacity`,
which sets the forced limit, `capacity_forced_sectors`; it defaults to
`--capacity`, so a pack that does not force a smaller limit than the
physical disc needs only `--capacity`. `--disc` is refused by name: it
means continuing an existing disc, Phase 2 append, which
`internal/image`'s `Build` does not support. `--reserve`,
`--extra-reserve`, `--preset`, `--now`, `--close` and `--dry-run` are
not defined, since `Build` has no such options.

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

Burning the folder `pack` produces directly, without running `image
build`, is a documented, supported use: see README.md, "Burning
without UDF".

## 7.6 In-flight change detection

Phase 1 has no parent snapshot: `internal/object`'s `Writer.Commit` always
writes a root snapshot, with no prior tree to compare against. The branch
rule of section 6.7 that reuses the parent entry therefore never applies
in this build; every unstable file takes the other branch, "flagged":
the writer keeps the content it read and sets the `UNSTABLE` entry flag.
`Writer` restats a regular file before and after reading it, controlled
by a `RestatAfterRead` option (default true, matching
`commit.restat_after_read`) and a `RetryUnstable` option (default 1,
matching `commit.retry_unstable`). `cmd/noahsark`'s `commit` reads both
keys from the config file when present and passes them through
unchanged; both are Phase 1 keys, so no refusal applies. `commit` prints
one `unstable PATH branch=flagged` line per flagged path, then the
count, and exits 1 when the count is nonzero, matching the exit code
table's "some files could not be read, or were unstable" rule.

A path that vanishes between being listed and being opened or stat'd
(deleted, renamed, or replaced by a broken symlink out from under the
walker) is a different case from an in-flight content change: nothing
was read, so there is no content to flag `UNSTABLE`. `Writer` reports
such a path in `Summary.Skipped` and continues the commit without it,
matching the source policy table's "unreadable file: skipped and
reported, exit code 1" rule; `commit` prints a `skipped PATH` line for
each and folds the count into the same nonzero-exit check. A read that
fails for any other reason (permission denied, an I/O error) still
aborts the commit and returns an error, since that is not a vanished
path and not a reason to keep partial or torn content.

## 4. Staging state machine

OPERATIONS.md names the states (STAGED, PACKED, BURNED, CLEAN,
GC-ELIGIBLE, DELETED) and the transitions, and gives `state.db` an
append-only role, but no byte layout. This build has no burn step and no
verify-after-burn step, so it implements only the Phase 1 part of the
machine: STAGED at commit, PACKED once a run includes an object, and the
run and disc that hold it. BURNED, CLEAN, GC-ELIGIBLE and DELETED, and
every GC rule, are out of scope until a burn command exists.

`internal/stage` picks the simplest deterministic record: fixed-width,
70 bytes (sequence, content id, state, run_seq, disc_uuid, reason,
crc32c), append-only, one record per transition. A reader replays from
the start and stops at the first record whose CRC fails, exactly
matching the "truncated log" rule; a record after a bad one is ignored.
The newest record per content id, by file order (equivalently by
`sequence`), is that object's current state. `EnsureStaged` never
overwrites an existing record, so a re-commit of already-packed content
can never resurrect it to STAGED.

`cmd_commit` calls `internal/image.CollectReachable` after a commit and
marks every object it returns STAGED, rather than having `Writer` itself
own state.db; this keeps the object writer free of a staging-state
dependency, at the cost of re-walking the tree once per commit.

## 8. Packing and locality, and 11.1 INDEX Prereqs

`pack` no longer requires the caller to name which snapshot to pack in
full; it always processes the whole STAGED pool across every snapshot
the repository has ever committed, since FORMAT.md requires every
snapshot object on every disc regardless of any other object's state.
`--ref`/`--snapshot` still choose only which named refs this run's
`REFS` table carries.

Selection order is a post-order (children before parent) walk of every
repository snapshot's tree: a directory's chunks, then its file blobs,
then its own tree object, then the snapshot object last. A straight
prefix of this order, filtered to STAGED objects, is always
dependency-closed: any object a prefix includes has every one of its
direct children either also in the prefix or already PACKED on an
earlier run (never STAGED-and-excluded), because a staged child cannot
occur after its parent in post order. `pack` greedily grows this prefix
while a trial `CheckCapacity` still passes, and stops at the first
object that would not fit; nothing past that point is tried, since a
prefix cut is the only shape locality asks for ("keep together where
possible"), not a bin-packing search over subsets.

Because the selected set is always dependency-closed, Prereqs
construction is exact and needs no search: for every selected tree,
blob or snapshot, a direct child absent from the selected set is
necessarily already PACKED (by construction), and its recorded run_seq
from the state log is the Prereqs row's `run_seq`.

## 11.3 DISCS and 12. Disc lifecycle, closing and appending

This build keeps a local ledger of every disc it has packed,
`<repo>/staging/discs.bin`, reusing `DISCS.bin`'s own container format
unchanged. There is no burn or read-back step to recover this
information from a drive, so the ledger is the authoritative source for
DISCS's earlier rows the next `pack` call writes. Unlike a real disc's
copy, the ledger's own row for a finished disc always carries the real
`run_hash` immediately (computed from that disc's own `RUN.bin` right
after it is built) rather than staying zero until a later run fills it
in; FORMAT.md's state-transition rule for `run_hash` (zero, then filled,
never changed) still holds for every row this build ever writes to an
actual disc tree, since a disc's own row is always written zero and the
ledger's filled value is only ever copied forward from the next disc
onward.

Phase 1 keeps one run per disc, so `run_seq` and `disc_seq` are derived
directly from the ledger's length: `run_seq` is the ledger's row count
plus one, `disc_seq` equals the row count. `--disc` (continuing an
existing disc) stays refused, unchanged from the existing reduction.

## 14. Restore, spanning discs

`internal/restore.RestoreMulti` takes several disc roots and looks up
each needed object directly by its canonical on-disc path on every
provided root; a snapshot object is also looked for under each
provided run's `catalog/snapobj`, since that copy is replicated on
every disc while the canonical `/NOAHSARK/snapshots/<id>` copy exists
only on the one disc that packed it. When an object is on none of the
provided roots, `RestoreMulti` resolves the disc that must hold it from
whichever provided run's `INDEX` names it (its own Objects row, or a
Prereqs row pointing at it) plus that run's `DISCS` table, and keeps
walking every other reachable branch instead of stopping at the first
miss, so one `*MissingDiscError` at the end names every missing disc's
uuid and every object needed from it. `cmd/noahsark`'s `restore` keeps
its single positional `DISC-ROOT` form; a multi-disc restore instead
repeats `--disc`, or names `--discs-dir`, a directory whose immediate
subdirectories are disc roots.

## 10. Forward error correction

`internal/fec.Codec` encodes and decodes with the
`github.com/klauspost/reedsolomon` backend, built with
`reedsolomon.WithCauchyMatrix()` for the configured k and m. That option
is required and must never change: klauspost's default matrix (a
Vandermonde matrix) does not match the Cauchy matrix FORMAT.md's rule
defines, so switching away from `WithCauchyMatrix()` would silently
change every disc's parity bytes. Before adopting the library, a
cross-check confirmed the backend produces byte-identical parity to a
pure Go implementation of FORMAT.md's GF(2^8) arithmetic for k=231,
m=23 over several hundred random stripes, and recovers identically for
random erasure sets up to m=23; measured on the development machine,
the backend ran at 792 MB/s (SSSE3) against the pure Go arithmetic's
11.7 MB/s. `docs/fec-reference.md` writes up that arithmetic by hand,
so an implementer who does not want the library can still reproduce
the parity bytes.

The pure Go reference implementation was then removed from
`internal/fec` on the user's decision, now that `docs/fec-reference.md`
records it and the cross-check has passed. The klauspost/reedsolomon
library is the sole implementation from here on; the worked example
from FORMAT.md (k=3, m=2, p0=0xE0, p1=0xAD) stays as a test in
`internal/fec` to confirm the library still matches the spec.
