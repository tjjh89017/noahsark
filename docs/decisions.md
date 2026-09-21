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
operation; this build keeps no carving reader. This differs from the
sector-boundary rule in FORMAT.md; the user will decide on a FORMAT.md
change.

## 10.1 Profile 0 image build: how the volume is populated

`mkudffs` only makes an empty UDF filesystem; populating it needs a loop
mount, which needs root. `internal/image`'s `MakeImage` runs `mkudffs`
itself (needs no root), then always loop-mounts the image, copies the
`NOAHSARK` tree in with Go's own `filepath.WalkDir`, and unmounts;
populating is never optional and never gated by an environment
variable. When the calling process is not root, `MakeImage` removes the
partial image and returns `ErrPopulateNeedsRoot` rather than shelling
out to `sudo` itself; `cmd/noahsark`'s `image build` turns that into a
message naming the exact `sudo noahsark image build ...` line to run
instead. This follows option (c) from the task's own list: a
from-scratch Go UDF writer was not attempted, since `udftools`'s own
`mkudffs` plus a root-only mount-and-copy step is the simplest path
that reaches conforming UDF bytes with no new binary format code to
maintain. The earlier design gated the copy step behind `NOAHSARK_CI`
and left a non-root build with an empty, unpopulated image; that let a
developer machine silently build a useless image, so it was dropped in
favour of always populating and refusing loudly when root is missing.

## 16. CLI reference

`cmd/noahsark` implements the Phase 1 command set: `init`, `commit`,
`pack`, `image build`, `verify`, `restore`, `ls`, `log`, `plan`,
`recover`, `status`, `disc burned` and `gc`. Every command
below keeps OPERATIONS.md's name; a flag is reduced or renamed only
when the Go packages this build calls have no way to honour it yet,
since no ref log, locality planner or burn plan exists in this build.
`disc burned` is not an OPERATIONS.md command; it is this build's
stand-in for the missing `burn` step, explained in "4. Staging state
machine" above. A staging state log (`internal/stage`) and a local
cache (`internal/cache`) do now exist, so `ls`, `log` and `plan`
resolve SNAPSHOT through the cache when no disc is given, and `disc
burned`, `verify` and `gc` drive the staging state machine; the
paragraphs below on those commands describe both paths.

`-h` and `--help` on any command exit 0 and print that command's
positionals and flags; they never count as a usage error. A flag must
come before a command's positional arguments; one placed after is
refused by name (`flags must come before positional arguments`)
instead of being silently read back as a positional string, since
Go's own `flag` package stops parsing flags at the first positional
argument. `--no-progress` and `--quiet`/`-q` are accepted by every
command, before or after the command name, and turn off the progress
line long-running commands write to stderr; progress is on by default
only when the real process stderr is a terminal.

A command name this build does not implement, and a flag no command
defines, are both refused by the standard library's own `flag` package
and command dispatch, exit code 2; neither carries a table naming a
phase or a "not yet in this build" reason, since a build that does not
implement a command or a flag says so the same way regardless of why.
An unknown config key is refused with one generic message naming the
key and the config file.

`init` accepts `--repo` and `--source`. Every other OPERATIONS.md
`init` flag (`--hash`, `--chunker`, `--fs-profile`, `--preset`,
`--repo-uuid`, `--next-run-seq`, `--next-disc-seq`, `--scan-discs`) is
not defined, so passing one is a plain usage error naming the flag:
the fixed decisions already collapse the first four to one value, and
the sequence-recovery flags have no multi-disc state yet to scan.
`init` writes a flat `key = value` config file, the
simplest format the standard library parses without a third-party
dependency, holding `repo.uuid`, `staging.dir` (section 17.1 and
17.5), and, when `--source` is given, `sources.root` (section 17.9):
every other Phase 1 key needs behaviour (hash choice, chunker profile,
excludes, locality, metadata policy) this build does not implement, so
the config loader refuses any other key by name rather than accept and
ignore it. `sources.root` is repeatable in OPERATIONS.md, since a
snapshot's root tree may hold one entry per source root; this build
stores only one, because `internal/object`'s `Writer.Commit` takes one
source directory (see the "6.14 Snapshot" entry above), so `init`
takes one `--source` flag, not a repeated one, and writes the path
`filepath.Abs` resolved at init time. Capacity is not among the
repository's own settings: a disc's capacity is locked in at that
disc's first write, not a value that holds for the whole repository,
so the config carries no default and every `pack` gives `--capacity`
on its own command line (section 17.12).

`commit` accepts an optional source path, `--ref`, and `-m` (a message
stored on the snapshot). With no source path on the command line,
`commit` reads the `sources.root` `init --source` stored; a path given
on the command line overrides it; neither present is a usage error
naming both ways to supply one. More than one source path on the
command line is still refused, matching the single root this build
stores and commits. Every other OPERATIONS.md `commit` flag (`--from`,
`--copy-first`, `--out`, `--catalog`, `--checksum`/`--full-scan`,
`--force`, `--source`, `--source-root`, `--exclude`,
`--one-file-system`, `--source-type`, `--retry-unstable`) is not
defined, so passing one is a plain usage error naming the flag: the
quick check, metadata TLVs, and exclude rules they need do not exist
in `internal/object`'s `Writer` (see the "6.14 Snapshot" entry above).
`commit`'s own `--source`
flag (a filesystem snapshot mount) is unrelated to `init --source`,
which only seeds the config; the two are never confused because they
belong to different commands. Commit records the new
snapshot under the given ref (default `LATEST`, so a commit with no
`--ref` moves `LATEST`; a commit with `--ref=NAME` moves only `NAME`,
never `LATEST`) in a flat local ref file, `<repo>/refs.txt`, standing in
for the local ref log of section 2.1 and 5.1, since no ref history or
state log exists in this build. `commit` exits 1 when any file was
unstable or skipped, matching OPERATIONS.md's exit code table; the data
is still committed and safe either way, only flagged or left out of
this one snapshot.

`pack` cannot select objects by `disc.min_fill` or `disc.max_wait`,
because `pack` does not read the staging state log to age objects by.
It instead takes
the snapshot(s) to place explicitly, by `--ref` (resolved through the
local ref file) or repeated `--snapshot`. With neither given, `pack`
does not default to `LATEST`: a repository whose every commit names its
own `--ref` (a date, say) never creates a `LATEST` ref at all, and
`pack` must not fail looking for one. Instead it carries forward every
ref not yet moved onto a run (see `addPendingRefs`, and section 8
below), the same set a `--ref`ed pack's own carry-forward step already
adds; `LATEST` is used only as a last resort, when that leaves nothing
pending and `LATEST` itself resolves.
`--capacity` accepts an integer
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

A bare number is refused. It reads as a byte count, it once meant
sectors, and the two are a factor of 2048 apart with nothing in the
output to say which one was taken. The error lists the presets and shows
a size example.

`--capacity` falls back to the config key `pack.capacity`, so a
repository that always burns one medium needs no capacity flag; a pack
of a different medium still gives `--capacity` on the command line.
`--physical-capacity` takes the same forms and sets the disc's physical capacity,
`capacity_sectors` in the superblock, separately from `--capacity`,
which sets the forced limit, `capacity_forced_sectors`; it defaults to
`--capacity`, so a pack that does not force a smaller limit than the
physical disc needs only `--capacity`. `--disc` is refused by name: it
means continuing an existing disc, Phase 2 append, which
`internal/image`'s `Build` does not support. `--reserve`,
`--extra-reserve`, `--preset`, `--now` and `--dry-run` are not defined,
since `Build` has no such options.

`--label` sets the disc's on-disc label text. `pack` has no `--media`
flag: the media type DISC.bin records is always derived, the type of
the matching `--capacity` preset, else `BD-R-SL-25`; a byte-size or
raw-sector `--capacity` never names a real medium to record, so there
was never a case where an explicit `--media` chose something the
preset derivation could not. `--out` sets the packed tree directory; it defaults to
`<repo>/staging/plans/<disc uuid>/tree` and is refused when it already
holds files, so `pack` never overwrites another run's tree by
accident. `--fec` and `--no-fec` override `fec.scheme` for one pack and
are mutually exclusive. `--close` changes only the burn command line
`pack` prints (`spare:none` and `-dvd-compat` instead of `spare:min`
and no `-dvd-compat`): this build does not burn or track disc state, so
`--close` has no other effect. `pack` refuses outright, exit 1, when
nothing is left staged to pack, since a run with nothing in it burns
no useful bytes.

`pack` ends by printing a "next steps" block: the exact `sudo noahsark
image build`, `growisofs`, and `noahsark verify` command lines for the
run it just packed, so a user need not compose them by hand. The
`growisofs` line always follows FORMAT.md's and OPERATIONS.md's
open-by-default rule, `spare:min` and no `-dvd-compat`, unless `--close`
was given.

`image build` takes the packed tree directory directly, in place of
OPERATIONS.md's `--run=SEQ`, because no run-sequence state exists to
resolve a run number against; the tree directory is what `pack --out`
already printed. `--capacity` is required for the same reason `pack`
requires it: `MakeImage` needs an explicit sector length, and there is
no stored run capacity to default to. Populating the image it builds
needs a loop mount, which needs root; `image build` never calls `sudo`
itself, so when the calling process is not root it removes the partial
image and prints the exact `sudo noahsark image build ...` line to run
instead, rather than leaving an unpopulated image behind or silently
elevating itself.

`verify DISC-ROOT` takes a mounted disc path or an unpacked NOAHSARK
tree positionally, the root `internal/image`'s `Read` and
`internal/restore`'s `Heal` already accept, rather than OPERATIONS.md's
raw image file plus `--mapfile` and `--image`: mounting an image file
needs root, which `verify` never assumes, so it takes an
already-mounted path (or a plain packed tree, for testing with no
mount at all) instead of mounting one itself, and takes exactly one,
so there is no separate `--image` form of the same argument to keep in
sync. `--out` stays, since `--heal --out=DIR` needs it to write the
healed copy somewhere other than in place. `--level`, `--drive`,
`--report`, `--disc`, `--run` and `--mapfile` are not defined, since no
drive or repository state exists for them to select among.

`restore` takes `DISC-ROOT SNAPSHOT OUT-DIR` positionally, in place of
OPERATIONS.md's `restore SNAPSHOT TARGET`, because resolving `SNAPSHOT`
through a repository's catalog and cache needs both, and neither exists
in this build; the caller instead names the disc root directly, the
same root `internal/restore`'s `Restore` already takes. SNAPSHOT itself
accepts a ref name as well as a snapshot id, resolved against the given
discs' REFS table the same way `ls` and `log` resolve it. `--no-xattr`
and `--no-acl` are Phase 2 and refused by name; `--translate-acl` is
Phase 2 and refused by name. `--include` (repeatable, a snapshot-relative
path, restoring everything under it when it names a directory) and
`--overwrite` are implemented: `restore` otherwise leaves an existing
path alone rather than overwrite it, reports `skipped N existing
path(s)` and exits 1 when any were left alone, so a repeated restore
never silently overwrites unless asked. `--plan`, `--staging-budget`,
`--interactive` and `--no-eject` are now defined, for the disc-swap
mode's `--mount` and `--plan` resume. Every other restore flag
(`--drives`, `--no-owner`, `--numeric-owner`, `--no-flags`,
`--no-times`, `--no-hardlinks`, `--metadata-strict`, `--report`,
`--report-replay`, `--strict-unstable`) is Phase 1 but not defined,
since `Restore` takes no such option today.

`restore` reports a metadata failure (a mode, times or owner field that
Chmod, Chtimes or Chown could not apply) as one warning line per field,
capped at 20 lines with the rest folded into a count line, plus a
`metadata not applied: N field(s)` summary and exit code 1, matching
OPERATIONS.md's `metadata_not_applied` event and its restore exit code
table. It has no JSON loss report yet: OPERATIONS.md's `{path, field,
reason, errno}` records, `--report`, `--report-replay` and
`--metadata-strict` stay unimplemented, listed above with the other
not-yet-defined restore flags. Owner is attempted only when the restore
runs as root; an unprivileged restore skips it outright rather than
attempting and reporting `EPERM`, matching the design's rule that
`--no-owner` is implied when the restore is not privileged. A symlink
entry gets owner only, through a no-follow `Lchown`; it gets no Chmod or
Chtimes, since both would follow the link onto its target, and this
build has no no-follow time call without adding `golang.org/x/sys` as a
direct dependency.

`ls`, `log` and `plan` resolve SNAPSHOT through `internal/cache` when
no disc root, `--disc` or `--discs-dir` is given: `looksLikeDiscRoot`
tells a `DISC-ROOT` positional apart from a snapshot id or ref name by
testing whether the argument is an existing directory, since a disc
root always is one and the other two never are in ordinary use. `ls`
and `log` keep their old disc-reading path unchanged when a disc is
named; `plan` reads the cache only, matching OPERATIONS.md's "reads
nothing from a disc beyond the catalog." All three report the same
incomplete-cache message and exit code 1 through the shared
`reportSourceError`/`formatIncompleteError` helpers, resolving the
disc to insert through `cache.LocateObject` and `cache.DiscRow`
where a cached disc's INDEX or DISCS table allows it.

`plan` groups every object a restore of SNAPSHOT (or of `--include`'s
paths alone) would need by the disc that holds it, walking cached tree
and blob objects; a blob the cache does not hold still counts as one
object, since blob caching only covers what pack or recover
processed after it was added, and its own absence is not, by itself,
an incomplete cache the way a missing tree is. `--staging-budget` is
now defined. `--drives`, `--score` and `--target` are not defined,
since no multi-drive planner or byte-vs-object scoring exists yet, and
`plan` records no restore target. The JSON `--out`
writes covers `discs[]`'s `order`, `disc_uuid`, `disc_seq`, `label`,
`objects_to_read` and `bytes_to_read`, `missing_discs`,
`peak_staging_bytes` (the largest single object of known size),
`switches` and `passes`; every other "14.4 The plan file" field
(`objects_exact`, `objects_probable`, `estimated_seconds`,
`degraded_runs` and the rest) needs a filter, a time model or a health
report this build does not have.

None of these reductions change any byte a conforming writer puts on a
disc or a conforming reader accepts; they change only which command-line
surface reaches the same Go calls the rest of this implementation
already exposes.

`status` reads the local disc ledger (`discs.bin`, the same rows a
DISCS table carries) and the staging state log, since no catalog exists
in this build either. It folds each disc's runs together and prints one
line per disc_uuid: the seq and the label of the newest run, one word
for the disc's state, and the uuid. It prints the same staged total line
`commit` prints, then one `next:` line naming the action to take next.
A counter answers a question the operator did not ask; the state word
and the `next:` line answer the one they did. `--json` keeps the exact
numbers: the object counts, the capacity, the used bytes and the verify
count.
`disc label` and `disc mark-degraded` need a `notes.bin` this build does
not keep, so both are refused with a clear message rather than silently
doing nothing.

The on-disc DISCS row a run carries for itself always writes
`used_sectors` zero, the same way it leaves `run_hash` zero: the run's
own final size is not known until the run is written. The local ledger
row, built after the run is written, carries the real value, so every
later run's copy of DISCS (and `status`) sees it from the next pack
on.

Burning the folder `pack` produces directly, without running `image
build`, is a documented, supported use: see docs/guide.md, "Burn
the disc".

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
walker), or that an open, read or readdir error blocks (for example
`EACCES` or `EIO`), is a different case from an in-flight content
change: nothing was read, so there is no content to flag `UNSTABLE`.
`Writer` reports such a path in `Summary.Skipped`, with the error text
as the reason, and continues the commit without it, matching the source
policy table's "unreadable file: skipped and reported, exit code 1"
rule; `commit` prints a `skipped PATH: REASON` line for each and folds
the count into the same nonzero-exit check. A read that fails partway
through a file drops that file's entry entirely rather than staging a
blob over truncated content; any chunk already written for it stays in
staging as an orphan, and gc reclaims it like any other object nothing
references. Only an error on the source root itself, or an error from
the staging store (a write, a rename, or the state log), still aborts
the commit and returns a hard error, since a failed destination write
is never something to paper over.

## 4. Staging state machine

There are five states: STAGED, PACKED, BURNED, CLEAN and ON-DISC.
ON-DISC is the last one. It means a disc holds the object and staging
holds no file for it. `gc` records it before it unlinks a staged file,
and `recover` records it for every object it reads from a disc's
own catalog. One state covers both, because both say the same thing:
the bytes are on a disc and nowhere else here. A separate DELETED state
would say no more, and a rebuilt object was never deleted.

`internal/stage` uses the simplest deterministic record: fixed-width,
79 bytes (sequence, content id, state, run_seq, disc_uuid, reason,
verify_count, clean_sec, crc32c), append-only, one record per
transition. OPERATIONS.md's state log record table gives the same
layout. The `verify_count` byte counts the successful verifies of the
object, so `gc` can hold the staged bytes until
`gc.min_verified_copies` copies read back. `clean_sec` is the unix time
of the first verify; every later record copies the value forward, so
the retention counts from the first verify and a second verify never
restarts it. One file carries it all: there is no companion file. The
newest record per content id, by file order (equivalently by
`sequence`), is that object's current state. `EnsureStaged` never
overwrites an existing record, so a re-commit of already-packed content
can never resurrect it to STAGED. `disc burned`'s own moment is not
recorded: nothing in this build ever reads it back.

The torn-tail rule runs one time, at open. A partial record at the end
of the file, or a last record with a bad CRC, is a crash during an
append: `Open` cuts the file back to the last good record and reports
the cut, and every command prints one warning for it. A bad record with
good records after it is damage: `Open` reports an error, names the
record and changes no byte, because dropping good records without
saying so would hide a real fault. Cutting at open, rather than before
the next append, removes the whole append-time repair path.

`cmd_commit` calls `internal/image.CollectReachable` after a commit and
marks every object it returns STAGED, rather than having `Writer` itself
own state.db; this keeps the object writer free of a staging-state
dependency, at the cost of re-walking the tree once per commit.

`commit` prints a `staged: N objects, B bytes` line after its own
summary, the repository-wide STAGED total from `image.StagedTotals`, so
the "pack when staged data nears one disc" rule of OPERATIONS.md's
packing guidance has a number to check against without waiting for a
`pack` to report it. `status` (below) prints the same line.

This build has no `burn` or `close` command: the operator burns with
`growisofs` by hand, following the command `pack` prints. Something
still has to tell the staging state machine that the burn happened, and
it cannot be a disc-uuid check inside `verify`: the guide has an
operator loop-mount and verify the image before burning it, to catch a
build problem early, and that loop-mounted tree's disc uuid is already
in the ledger, because `pack` writes the ledger, not a burn step. A
verify that treated a ledger match alone as proof of burning would mark
that pre-burn image CLEAN, and `gc` would later delete staging objects
for a disc that was never actually written.

`noahsark disc burned [--undo] UUID [UUID...]` is the explicit step
that closes this gap. It moves every PACKED object of each named disc's
runs to BURNED, and records the burn time (see above). `pack`'s
next-steps block prints it between the `growisofs` line and the
`verify` line, so the ordinary flow always runs it right after the
physical burn. `--undo` reverses it, moving BURNED objects back to
PACKED with the burn-failed reason, for a burn that turned out bad
before anyone got as far as `verify`.

`verify` never moves PACKED to BURNED itself. When `--repo` resolves to
a repository and DISC.bin's uuid matches a row in its disc ledger, a
passing verify moves the run's BURNED objects to CLEAN and leaves any
PACKED object of that run alone, printing a line naming the `disc
burned --repo=<repo>` command to run when one remains PACKED, after the
`verify: ok` line rather than ahead of it when no object was BURNED
this pass; a failing verify moves BURNED objects back to PACKED with
the verify-failed reason, unchanged from before. The `marked N
object(s) CLEAN` line itself prints only when N is at least 1, since a
disc already fully CLEAN, or one still fully PACKED, has nothing to
report there. A verify against a tree whose disc uuid the ledger has
never seen at all, or run with no `--repo`, changes no staging state.

`gc [--dry-run] [--force-after=DURATION]` implements the GC rules: it
frees the staged file of a CLEAN object once
`staging.retain_after_clean` has passed since its clean time, after
confirming the object's presence in the cached INDEX of the disc the
state log says holds it; an object whose disc is not cached is left
alone and reported separately, never deleted on trust. The ON-DISC
record goes to the disk before the staged file is unlinked: that one
append syncs before it closes, so a crash can never take the record
away and leave the bytes gone. A crash the other way round leaves an
orphan, a staged file whose object is already ON-DISC, and the next
`gc` run frees it. No other append syncs, because `commit` writes one
record per object and a sync per object would set its pace; a lost tail
there only replays as an object still STAGED, which the next `pack`
heals. `gc` never trims the local cache: the cache is an accelerator,
it costs little, and `recover` is the only tool needed to get it
back.

`--force-after=DURATION` substitutes DURATION for
`staging.retain_after_clean` for this one run, using the same duration
syntax (a whole number of days with a `d` suffix, or anything
`time.ParseDuration` accepts). `gc` computes what it would delete under
that shortened window exactly as it always does (`gcPlanStagingObjects`,
shared with the ordinary path), then, unless `--dry-run` was also
given, prints the confirmation OPERATIONS.md's CLI reference names,
`delete N object(s), B bytes? [y/N]`, on stderr and reads one line from
stdin. `--dry-run` skips the confirmation outright: it changes nothing
either way, so there is nothing for the operator to approve. There is
no flag to skip the confirmation: a script pipes the answer in
(`echo y | noahsark gc --force-after=1h`), which is one explicit act,
and a killed session with no stdin reads an empty line and deletes
nothing. Answering anything but `y` or
`yes` deletes nothing and exits 2, the same code as the refusal, since
both leave `gc` having done nothing the operator did not ask for.

OPERATIONS.md's own exit codes for `gc` (16.20) are 0 on success, 1
when nothing was eligible, 2 on failure, with no separate case for
`--dry-run`. A dry run only reports what a real run would do; it never
changes anything, so failing to find something to delete is not a
`--dry-run` failure the way it is a real run's. `--dry-run` always
exits 0, printing `gc: nothing is eligible yet` and the earliest date
some CLEAN object reaches `staging.retain_after_clean`, when nothing is
eligible and every candidate's run is cached (an object skipped because
its run is not cached prints that separate line instead, since
"nothing is eligible" would misstate why nothing was deleted). A real
`gc` run keeps exit 1 for that case, matching OPERATIONS.md.

A staging object at PACKED, BURNED, CLEAN or ON-DISC all name an
object a disc already holds; only STAGED does not.
`stage.State.OnDisc()` names this test once, so `pack`'s two "is this
object already on a disc" checks, `cmd_commit`'s `Known` callback, and
`status`'s on-disc object count all agree with each other. Before
this existed, both of `pack`'s checks compared against PACKED alone: an
object `disc burned` and `verify` had already moved to BURNED or CLEAN
looked unpacked again to the next `pack`, which copied it a second time
and rebound it, with `MarkPacked`, to the new disc, silently losing
cross-disc dedup for every object a burn-and-verify cycle had already
completed.

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

`pack` checks that every staged object really holds the content its
name promises, so corruption in the staging store cannot reach a disc.
Where the check runs follows from what each kind costs to read. A tree,
a blob and a snapshot are small and the selection walk decodes them
anyway, so they are checked there. A chunk is the bulk of the data and
the walk needs nothing out of it, so it is checked while the run copies
it into the output tree: the payload streams through a decompressor
into a hash in the same pass that writes it, so the check adds no read
and holds no more than one chunk's decoder window. A length that
differs from the length selection sized the object by is the same fault
as a payload that hashes to another id. `pack` still reads a chunk once
more, to hash the staged file for its `INDEX` row; that hash orders the
role 13 rows, so it must be known before the run is laid out.

A chunk that fails this check fails after part of the run is already
written. `pack` then removes the whole `NOAHSARK` tree it wrote under
`--out` and returns. Nothing past the write runs, so no object is
recorded PACKED, no ledger is saved, and the run and disc sequence
numbers stay free for the next `pack`. Parity is computed over the same
stream and is removed with the rest.

`pack` flushes what it wrote before it records anything. One pass at
the end of the write syncs every file and every directory of the run
tree and returns the first error it meets. Only then does `pack` mark
its objects PACKED and save the ledgers, so the state log can never
claim a run the local disk does not hold. The flush is one pass rather
than one per file as each file closes: with FEC on, the measured pack
of 512 MiB took 13.5 s that way against 22.1 s per file. The flush is
not free either way, since it waits for bytes an unflushed `pack` only
left to background writeback: the same 512 MiB packs in 5.3 s with no
flush at all.

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

## 10. Forward error correction, FEC scheme registry value 0

The user's decision: FEC becomes optional and is off by default. The
primary redundancy is burning two identical discs; FEC is a reserve
feature. FORMAT.md's FEC scheme registry gains id 0, `none`, now the
default; id 1, `rs255-gf8`, is unchanged and still the only scheme that
writes a checksum column and parity. The existing Reed-Solomon
implementation is untouched by this change: `internal/fec` and
`internal/image`'s scheme 1 path produce the same bytes as before.

`internal/image`'s `BuildOptions` and `PackOptions` gain `FECEnabled
bool`, false by default, read by both `Build` and `Pack` through a
shared `appendFECRows`/`writeRunTree` pair that either lays out the
checksum and parity rows and computes them to disk (scheme 1), or skips
both entirely (scheme 0): the two writers' row order and byte output
for a scheme 1 run are unchanged, since that code path is untouched,
only reached through the same call it always was.

`cmd/noahsark`'s config gains `fec.scheme` (Phase 1, values `none` and
`rs255-gf8`, default `none`), read as `repoConfig.FECEnabled`; `pack`
gains `--fec` and `--no-fec`, which override the config for one run and
refuse to be given together. `pack` prints which mode it used and the
stream-block budget that mode consumed.

`internal/image.DataBudgetBlocksNoFEC` is the scheme 0 capacity rule:
every usable sector after the filesystem overhead estimate, with no
stripe rounding and no share given to a checksum column or parity,
against `DataBudgetBlocks`'s existing whole-stripe rule for scheme 1.
Both are covered by tests in `internal/image`.

`image.Read` skips the parity-header and checksum/parity recomputation
checks when `run.FECScheme` is not `rs255-gf8`, and verifies every
object's content id and every Files row's file hash either way, per
FORMAT.md's new "10.8 Scheme 0: no FEC" subsection. `restore.Heal`
reads the run header first and refuses a non-`rs255-gf8` run with an
error naming the run seq, before opening any FEC file.

The default was confirmed after the pack optimization. The checksum
digest pass is fused into object placement; parity still reads the
placed stream a second time because the column-major stream layout
scatters one stripe's blocks across the whole run. Measured on the CI
runner, media/bd25 cell, 1.26 GB fixture:

| Mode | Before | After |
|---|---|---|
| FEC off | 5.38 s, 234 MB/s | 1.94 s, 648 MB/s |
| FEC on | 11.94 s, 105 MB/s | 4.21 s, 299 MB/s |

Under the 512 MB lowmem cap FEC on runs at 87 MB/s. A 25 GB disc packs
in about 40 s without FEC and about 85 s with it. The user kept the
default off: two identical discs are the primary redundancy, FEC costs
9% of capacity, and small hosts pay three times the pack time.

`reference/decoder.py`'s `cmd_verify` never implemented a checksum-column
or parity check of its own; it already conformed to the scheme 0 rule by
construction. Its `parse_run` was missing a `fec_scheme` key in the
returned dict, filled in here since a scheme-aware reader needs it. A
checked-in fixture at `reference/testdata/scheme0-fixture`, one small
run built with `FECEnabled: false`, backs a decoder test asserting
`cmd_verify` passes and no `checksum.bin` or `parity/` exists.

Test/e2e coverage: `corrupt-heal`, `corrupt-parity`, `corrupt-max`,
`corrupt-over` and `lowmem` all need FEC on to have anything to
corrupt and heal, or, for `lowmem`, to exercise the stripe-at-a-time
memory strategy that scenario checks; `test/e2e/disc/cmd/ci-fixture`
gained a `-fec` flag and `run.sh`'s `build_fixture` and `scenario_media`
pass it through for those scenarios only. `cli`, `media` and `chain`
run at the new default, off, unchanged.

## 8.2 Files at the volume root, and 8.3 Run directory naming: case-insensitive reading

Some burners fold every on-disc name to lowercase: plain ISO 9660
level 4 with no Rock Ridge is the common case (verified with this
host's `genisoimage`; `NOAHSARK` becomes `noahsark`, `README.txt`
becomes `readme.txt`, `RUN.bin` becomes `run.bin`, and so on), and other
burners or tools may fold names the same way. Object file names and
their two-hex-digit fan-out directories are unaffected: they are
already lowercase hex, so folding changes nothing there.

`internal/image.NameCache` resolves one fixed name inside a directory:
the exact name first, then a case-insensitive match against that
directory's own listing, cached per directory for the caller's whole
`Read`, list, restore or heal call. Every fixed name FORMAT.md's volume
root and run directory sections define is resolved this way in
`internal/image` (`reader.go`, `listwalk.go`, `walk.go`) and
`internal/restore` (`restore.go`, `heal.go`); object and fan-out
directory names stay exact-match. `reference/decoder.py` carries the
same `NameCache` and the same rule. Writers are unaffected: NoahsArk
still writes every fixed name in the exact case FORMAT.md defines; only
reading tolerates a burner's own folding.

This reading has a bearing on the later profile 2 filesystem work: a
profile that relies on FAT-family case-insensitivity, or on a burner
that folds names, can reuse this same tolerance instead of a new rule.

## 14. Restore, single-drive disc swap

With no `DISC-ROOT`, `--disc` or `--discs-dir`, and exactly `SNAPSHOT`
and `OUT-DIR` left over, `restore` resolves `SNAPSHOT` through the
local cache and builds the same plan `plan` prints, by sharing
`internal/plan` (moved out of `cmd/noahsark/cmd_plan.go` so both
commands call the same planner). It then walks the plan's discs in
order, one at a time, prompting the operator between them: the
single-drive shape OPERATIONS.md's disc-major order describes, driven
like an old multi-volume installer instead of needing every disc
mounted at once.

`internal/restore.BuildManifest` reads every tree and blob the
restore needs straight from the cache: `CheckComplete` already proved
every tree is cached, and a blob is cached for any snapshot `pack` or
`recover` has touched since blob caching was added. Only chunk
payloads still need a disc, so a mounted disc is read for its assigned
chunk objects alone (`internal/restore.ReadChunkFromRoot`, the same
canonical `objects/<fanout>/<id>` path `Restore` and `RestoreMulti`
already use); every directory and symlink is created immediately, and
regular files wait in a `pendingFile` list, keyed by the chunk ids they
still need, until every chunk has arrived. A chunk shared by several
files (dedup) stays spooled until the last file needing it is written,
then is deleted; a blob the cache does not hold is a hard error in this
mode, since there is no path yet to fetch a blob object from a mounted
disc mid-walk.

Spooled chunk payloads live flat under
`staging/restore/<snapshot-id>/objects/<id>`, keyed by content id alone
(no run or disc structure), since a chunk's payload is content-addressed
already. On start, `restore` marks every object already sitting there
as spooled and tries to finish any file that completes as a result,
printing `resuming: N object(s) already spooled`; a disc every one of
whose assigned chunks is already resolved (by an earlier interrupted
run, or by `--include` narrowing the manifest so that disc holds
nothing the manifest still needs) is skipped with no detection and no
prompt.

Disc detection (`cmd/noahsark`'s `detectDisc`) reads
`--mount`'s `NOAHSARK/DISC.bin` and compares its uuid: a match prints
`disc <seq> <label>: found` and moves on with no prompt, unless
`--interactive` is set, in which case it prompts once and accepts the
next matching read. A mismatch reports the expected and found uuid and
label (the found label comes from the cache's own `DISCS` table, when
that disc is one the cache already knows) and prompts again. An
unreadable `DISC.bin` (drive still settling, or nothing mounted yet) is
retried a few times with a short pause before it prompts. `--mount` has
no config default: OPERATIONS.md's configuration reference names no
`restore.mount` key, so the flag is required in this mode.

`restore.staging_budget`, or `--staging-budget` (same unit suffixes as
`--capacity`, parsed by the new, shared `parseByteSize`), bounds
`staging/restore/`'s peak size, section 14.3's staging budget. The
disc-swap loop tracks a running spool-bytes total itself (seeded from
whatever `resumeSpool` finds already on disk) rather than statting the
directory on every write: `Manifest.WriteReady` now also returns the
bytes it just freed, so the total moves by exactly what was written and
freed, no re-scan needed. `internal/plan.ComputePasses` buckets each
disc's chunk objects, in the plan's own order, into passes of at most
budget bytes; it is a conservative, cache-only estimate (it assumes no
freeing until a whole pass finishes, since `plan` builds no per-file
manifest), so a real restore, which frees a file's chunks the moment
that file is complete, may need fewer passes than predicted but never
more. Both `plan` and `restore` print the resulting `passes` and
`peak_staging_bytes`; `restore`'s own loop prints `pass N/M` between
passes on the same disc, with no re-detection and no new prompt, since
the disc never left the drive.

Before any disc is read, `restore` also checks every pending file's own
chunk total against the budget (`Manifest.FileExceedingBudget`): no
split of one file's chunks across passes can keep it under a budget
smaller than the file itself, so this is refused up front, naming the
file and the budget, at exit code 2, rather than discovered mid-restore
after some other disc has already been read.

`restore --plan=FILE` resumes a plan `plan --out=FILE` wrote, instead
of building one: it takes the file's `snapshot`, `include` and disc
order (matched back to a freshly built `internal/plan.Result` by disc
uuid, so the actual object-to-disc assignment still comes from the
current cache, only the order is pinned) rather than recomputing them.
The positional argument becomes `OUT-DIR` alone; `SNAPSHOT` would be
redundant with the plan file and `--include` is refused alongside
`--plan` for the same reason, one source of truth for what gets
restored. The plan file's `repo_uuid` (hyphenated lowercase, matching
`internal/plan.UUIDText`) and `created` (RFC 3339, from a package-level
clock a test can replace) are new plan-JSON fields, added so a resumed
plan can be checked against the repository it is resumed into: a
`repo_uuid` mismatch, or a `snapshot` the cache does not have complete,
is refused by name rather than silently replanning against the wrong
repository.

`ejectDrive`'s permission hint used to string-match `umount`'s own
stderr for "permission denied" or "must be superuser", which is
locale- and version-dependent output to key behavior on. It now checks
only `umount`'s exit status together with `os.Geteuid() != 0`: an
`umount` failure while not running as root is treated as the
permission problem, and the one informational line (pointing at sudo
or `--no-eject`) still prints at most once per restore; `umount`'s own
output is instead folded into the generic warning for every other
failure, so it is not lost, just no longer parsed.

A destination file that already exists, with `--overwrite` not given,
is not automatically a conflict in this mode: `internal/restore.Manifest`
checks it against the tree entry it must match, either by size and
mtime (the way `applyMetadata` leaves a file this restore wrote itself)
or by hashing dest's bytes at each blob entry's own offset and length
and comparing against that entry's content id, which needs no disc
access. A match counts as resumed, not skipped, so a rerun after a
killed session reports `resumed: N file(s) already restored` and exits
0 once nothing else is wrong; only a genuine mismatch still counts as
skipped and keeps the exit-1, `--overwrite`-to-replace behavior. Once
every file is written and the run finishes, the now-empty
`staging/restore/<snapshot-id>/` directory is removed.

## 6. Concurrency and locking

`internal/repolock` implements the repository lock: a non-blocking
exclusive `flock` on `<repo>/lock`, and nothing else. It never treats
the lock file's existence as the lock, and never removes it, so a
competing `open` always locks the same inode. A command that cannot get
its lock prints `repository lock <path> is held; another noahsark
command runs on this repository` and exits 1, matching OPERATIONS.md's
rule.

Every state-writing command this build has takes the lock before it
opens the state log: `init` (on the directory it just created),
`commit`, `pack`, `gc` (`--dry-run` included, since it still replays the
log to report what it would delete), `disc burned`, `recover` and
`restore`'s single-drive disc-swap mode. `verify` takes it only when
`--repo` resolves to a repository; with no `--repo` it never touches any
repository's state, the same reasoning that already applies to `image
build`, and to `ls` and `log` reading straight from a disc instead of
the cache. `plan`, `ls`, `log` and `status` take no lock at all.

`status` is the one lock-free command that still opens the state
log, so it uses `stage.OpenReadOnly` instead of `stage.Open`: both
replay the log the same way and cut the same torn tail from the
in-memory result, but only `stage.Open` (used by the exclusive lock
holders) also truncates the file on disk. A lock-free `status` that
raced a concurrent append could otherwise observe a torn tail that is
really an append still in progress, and truncating it would corrupt the
writer's work; `stage.OpenReadOnly` leaves the file untouched, so this
can never happen. `restore`'s all-discs-at-once mode never resolves a
repository at all in this build, so it takes no lock, the same as
`verify` and `image build` with no `--repo`.

## 16. CLI reference, `init --next-run-seq`, `--next-disc-seq` and `--scan-discs`

`pack` now takes the next `run_seq` and `disc_seq` from the disc
ledger's highest recorded numbers, not from its row count, so a ledger
`recover` rebuilt with a gap (a disc known only through a
sibling's DISCS table, never itself fed) cannot hand out a number a
known disc already carries. This build still cannot know the numbers
of a disc that both the repository and the disc itself are lost
together: nothing surviving names it. `init --next-run-seq`,
`--next-disc-seq` and `--scan-discs`, OPERATIONS.md's own answer to
that case, are not in this build yet, matching "16. CLI reference"
above. Instead, `recover` warns on stderr, on every successful
run, which disc it treats as the newest fed and which numbers the next
`pack` assigns, so the operator can feed the true newest disc first
and catch the gap before it is baked into a new run. `recover`
also refuses outright, before writing anything, when a disc it is fed
would take a `run_seq` or `disc_seq` the ledger already has under a
different disc uuid: the two discs' uuids differ even when their
sequence numbers now collide, so the refusal cannot merge them by
mistake, but nothing prevents the collision itself without the
Backlog options above.
