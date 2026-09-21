# NoahsArk design notes

Document version 0.6.1.

**Nothing in this document is normative.** It carries the purpose, the
architecture, the rationale, the evidence, the implementation notes, the
glossary, the references and the change log. `FORMAT.md` holds every rule
about on-disc bytes. `OPERATIONS.md` holds every rule about host behaviour.
Where this document and either of those two disagree, they win.
`docs/decisions.md` holds the reasons behind the choices of the build.

## 1. Purpose and goals

NoahsArk is a backup tool for write-once optical media. It writes
content-addressed objects to Blu-ray and DVD discs. It reads them back years
later. The tool is for one person: commit, pack one run on one disc, burn two
identical copies, verify, store, restore.

The system targets a 30-year archive. The format therefore prefers explicit
byte layouts over parsers, fixed-width records over variable-length records,
and plain files over databases. A human in 2050 must be able to read a disc
with a hex editor and the `README.txt` and `FORMAT.txt` that the disc carries.
That sentence is the source of most of the decisions in this document.

The local cache is an accelerator. It is never a source of truth. A user can
delete the repository and the cache. The discs still answer every question.

### 1.1 Goals

1. Store a directory tree on write-once optical media without loss.
2. Restore that tree with its file metadata.
3. Deduplicate below the file level across all discs of a repository.
4. Survive the loss of one disc: two identical copies are the redundancy.
5. Survive the loss of the local machine. The discs alone are sufficient.
6. Keep the on-disc format readable by a person with no NoahsArk software.
7. Detect silent corruption at every read. Never use bad bytes.
8. Support a file of any size. One file can be larger than one disc.
9. Keep the peak memory independent of the data size.

### 1.2 Non-goals

1. No encryption, no signing and no access control. A person who holds a disc
   reads every byte on it. The repository lock is advisory.
2. No network protocol, no scheduler and no daemon. NoahsArk is a local tool.
3. No delta compression between objects. A broken delta chain on write-once
   media cannot be repaired.
4. No ISO 9660 bridge, no Joliet, no Rock Ridge, and no UDF writer in Go.
5. No database. An engine is a dependency risk on a 30-year medium.
6. No snapshot retention and no expiry. The tool never removes a snapshot. A
   write-once medium cannot free space.
7. No append to a disc. One disc holds one run.
8. No recovery from the raw medium. A disc that does not mount is dead.

### 1.3 Platform tiers

| Tier | Platform | Read | Burn | Notes |
|---|---|---|---|---|
| 1 | Linux | Yes | Yes | Reference platform. |
| 2 | Windows XP to 11, macOS 10.4 to 15 | Mounts the disc | No | Both mount UDF 2.01. `decoder.py` extracts a file. |
| - | FreeBSD | No | No | Its kernel reads UDF 1.50 only. |

NoahsArk must not lower its UDF revision for FreeBSD, because UDF 1.50 loses
features that Windows and macOS need.

### 1.4 What the design defends against

The adversary is accident, decay and a hostile input, not a person with the
disc in hand. NoahsArk defends against a damaged medium, a wrong disc in the
drive, a corrupt cache, a malformed structure, and a crafted archive that
tries to make `restore` write outside its target. FORMAT.md's "Trust
boundaries and safety invariants" is the list. OPERATIONS.md's "Name and
symlink safety" holds the `restore` rules.

## 2. Architecture

```
   source directory
        |  walk, exclude
        v
   COMMIT      chunker (FastCDC) -> SHA-256 -> zstd per chunk
        |      -> blob for each file -> tree for each directory -> snapshot
        v
   STAGING     <repo>/staging/: objects/, snapshots/, state.db, ledgers
        |      states: STAGED -> PACKED -> BURNED -> CLEAN -> ON-DISC
        |  pack: a prefix of a post-order walk that fits the capacity
        v
   DISC ROOT   /NOAHSARK: DISC.bin, README.txt, FORMAT.txt, decoder.py,
        |      runs/<seq>/ with RUN.bin, INDEX.bin, REFS, DISCS, parity;
        |      objects/ab/<id>, snapshots/<id>
        |  image build: mkudffs, loop mount, copy (root)
        v
   UDF IMAGE   tree.img
        |  the operator: growisofs, two times; then "disc burned"
        v
   DISCS       two identical copies, stored in different places
        |  the operator mounts; verify reads every object
        v
   VERIFY      BURNED -> CLEAN; gc frees staging after 2 verifies and 7 days
        |
        +--> LOCAL CACHE <repo>/cache/: INDEX, REFS, DISCS, snapshots,
        |    trees and blobs. Derived data only. recover builds it again.
        v
   RESTORE     all discs at once, or one drive with a disc swap
```

| Package | Responsibility |
|---|---|
| `internal/chunker` | FastCDC cut points, the vendored Gear table. |
| `internal/object` | The commit walk: chunks, blobs, trees, the snapshot, excludes. |
| `internal/format` | Every on-disc structure: one definition, encode, decode. |
| `internal/stage` | The state log and the five states. |
| `internal/image` | The disc root of one run, the capacity budget, the reader, the UDF image build. |
| `internal/fec` | Reed-Solomon parity, the checksum column, the stream mapping. |
| `internal/cache` | The local cache, keyed by disc uuid. |
| `internal/plan` | Which disc holds which chunk of a snapshot. |
| `internal/restore` | The restore walk, part files, metadata, safety, heal. |
| `internal/repolock` | The repository lock. |
| `cmd/noahsark` | The commands, the config, the `DISC` argument. |

OPERATIONS.md's "Staging state machine" and "Restore" hold the data flows.

## 3. Design rationale

Each subsection names the home of the rule and gives the reason.

### 3.1 Binary format

Rule: FORMAT.md's "Binary format rules".

Every structure is a fixed-width little-endian packed record behind a 32-byte
common header. A packed record needs no parser. A person can read it with a
hex editor and the offset table in `FORMAT.txt`.

There is one evolution rule: a reader refuses an unknown `version_major`.
Feature bit masks and a minor version said the same thing a second time, thus
they were removed. Until the first release the format is not frozen.

Every byte that NoahsArk writes is an ordinary file. A hidden sector is
invisible to every operating system and is lost when a disc is copied by file.

A reference decoder, `decoder.py`, is on every disc. A description of the
bytes does not prove that the description is enough. A person with a Python
interpreter and no network can extract and verify a file.

### 3.2 Identity and hashing

Rule: FORMAT.md's "Identity and hashing".

SHA-256 is the only algorithm. It is in the Go standard library, modern hosts
accelerate it, and a reader in 30 years knows it. The content id covers one
kind byte and the payload, thus an empty blob and an empty tree never share an
id, and a lookup needs no kind filter. A digest is never truncated. The 8-byte
digests of the checksum column are not content ids; they detect decay only.

### 3.3 Chunking

Rule: FORMAT.md's "Chunking".

The chunker is FastCDC with a 64-bit Gear hash and normalization level 2. It
has one parameter set: 1 MiB, 4 MiB, 16 MiB. `max = 4 * avg` and `min =
avg / 4` is the ratio of the FastCDC paper. A 25 GB disc then holds about
6,000 objects, which suits a UDF directory tree. A 4 MiB chunk still finds the
shifted-insert edits that a fixed cut point misses. The two-byte rolling form
of the 2020 paper is not adopted: FORMAT.md's "Cut point rule" is the only
definition.

The Gear table and the masks are vendored. A changed table gives other cut
points and silently ends dedup against every existing disc.

Each chunk is one file. A bundle of small chunks was measured and deferred;
"Small files on UDF" holds the numbers.

### 3.4 Compression

Rule: FORMAT.md's "Compression".

Compression runs for each chunk, after the content id is computed. The same
bytes give the same id whether or not they were compressed, and a later
encoder change never breaks dedup. A chunk that gains less than 5 percent is
stored raw: a small gain costs a decompression pass on every restore.
Whole-file compression before chunking destroys dedup: one changed byte
changes every compressed byte after it.

### 3.5 Objects

Rule: FORMAT.md's "Objects".

A snapshot names a root tree. A tree describes one directory with the full
metadata of each entry. A blob lists the chunk ids of one file. Every arrow
carries the hash of its target.

Metadata is inline in the tree entry. A separate node object would cost one
object read for each file, not one for each directory. On a medium with
100 ms seeks that is the difference between usable and unusable.

### 3.6 Disc and run

Rule: FORMAT.md's "Disc and run model".

One disc holds one run, written in one burn. Nothing is ever overwritten. "Why
true UDF multi-session is not possible" holds the evidence that no open source
tool can append a UDF session. The disc uuid is the identity of a disc. The
sequence numbers are labels, because a rebuilt repository can give a number a
second time.

### 3.7 Filesystem

Rule: FORMAT.md's "The UDF volume".

The filesystem is pure UDF 2.01, block size 2048. A bridge disc carries two
trees that can disagree, and only one of them gets verified. Three reasons for
2.01: it is the ceiling of Windows XP; Linux reads and writes it; `mkudffs`
cannot build the Metadata Partition of UDF 2.50. Blu-ray video uses UDF 2.50,
but that is a rule for video discs only. `mkudffs --spartable` is forbidden: a
sparing table adds a second logical-to-physical indirection.

On-disc names are NoahsArk's own: lower-case hex and a few fixed names. User
names, deep paths and POSIX metadata live inside tree objects.

### 3.8 Forward error correction

Rule: FORMAT.md's "Forward error correction". FEC is optional and off by
default; `docs/decisions.md` holds that decision.

The drive returns a hard read error for a sector that it cannot decode, thus
NoahsArk sees erasures. The drive can also return wrong bytes; the checksum
column turns that case into an erasure. The dangerous damage is the contiguous
run: a ring scratch or an outer-edge band. A radial scratch hits thousands of
stripes with one block each and is harmless to an interleaved code.

`k = 231`, `m = 23` gives 9 percent parity. Ten percent is the knee of the
curve: it covers a 3 mm ring scratch on a 25 GB disc, and damage far beyond
that usually means a disc that no drive reads. Per-object parity was rejected:
its parity sits next to its object, thus one burst destroys both. Parity is
computed one stripe at a time, never with the whole run in memory.

### 3.9 The run index and the catalog

Rule: FORMAT.md's "The run index and the catalog".

Each run carries its INDEX, and the REFS, the DISCS and every snapshot object
of the whole repository. Thus any one disc lists the whole history, and the
newest disc names every ref. There is no membership filter: only an exact
INDEX lookup permits the writer to drop a chunk. A run lists its
prerequisites with the disc that holds each, so "on another disc" is never
confused with "corrupt".

### 3.10 Packing and restore

Rule: OPERATIONS.md's "Packing and locality" and "Restore".

A disc is cheap. A disc swap costs a minute of human attention on every
future restore. `pack` therefore keeps the chunks of a file and the files of a
directory together, and splits only at the tail of a run. `restore` reads each
disc one time. The remedy for a restore plan that touches too many discs is a
new repository and a new disc set.

### 3.11 Rejected alternatives

Do not implement anything in this list. Each line gives the reason.

- **JSON or SQLite on the disc.** A parser and an engine are dependencies that
  the reader in 2050 must still have.
- **Fixed 16 MiB chunks.** One inserted byte changes every later chunk.
- **casync-style stream chunking across files.** A metadata change moves the
  cut points, and a single-file restore needs the stream index.
- **Git-style trees with no metadata.** One more object read for each file.
- **Per-file Merkle trees.** The checksum column already finds damage at
  2048 bytes.
- **Shallow partial graphs.** A snapshot must be provably complete or provably
  incomplete; the Prereqs table says which.
- **A hash agility layer with id translation.** Git designed one and did not
  ship it. One algorithm is enough until it breaks.
- **ISO 9660 bridge (`genisoimage -udf`).** It writes UDF 1.02, and the two
  trees can disagree. Windows prefers UDF and macOS prefers ISO.
- **Rock Ridge and Joliet.** Windows ignores Rock Ridge. Joliet is UCS-2.
- **A VAT volume (`mkudffs --media-type=bdr`).** The Linux kernel mounts a
  write-once volume read-only, thus nothing can fill it.
- **A UDF descriptor set for each session.** No tool builds it, it rewrites
  the whole tree each time, and macOS sees the first session only.
- **xorriso.** libisofs 1.5.8 holds no UDF writer; `-udf` is parsed and
  discarded.
- **pktcdvd packet writing.** The driver is gone from the Linux kernel.
- **Append, consolidation, a scrub schedule, remote sources, commit bundles, a
  burn command.** Each was specified one time and cut. The tool is for one
  person with one local source.

## 4. Evidence

Every number is as measured. Open a question again only with newer evidence.

### 4.1 Why true UDF multi-session is not possible

Three independent blocks exist. Each was checked against the source.

1. **growisofs cannot merge a UDF session.** `-M` reads block 16 of the volume
   and demands the ISO 9660 descriptor `"\1CD001"`. A pure UDF volume has
   none, thus `-M` exits. growisofs holds no filesystem writer.
2. **mkudffs cannot build a session that names an earlier one.** `mkudffs
   --startblock` makes a new, empty filesystem at an offset: `udfinfo` reports
   `numfiles=0`.
3. **The kernel cannot write a VAT volume.** `fs/udf/super.c` forces read-only
   for `PD_ACCESS_TYPE_WRITE_ONCE`. A `--media-type=bdr` image mounts
   read-only, and only after a truncate to the VAT block.

No open source tool bridges "writes bytes" and "writes UDF". `mkudffs` writes
empty volumes, the kernel writes UDF on media that accept random writes, and
every burner writes bytes. Thus `image build` uses `--media-type=hd` and a
loop mount, and one disc holds one run.

### 4.2 dvd+rw-tools

Upstream dvd+rw-tools 7.1 (2008) is the last release, and it is broken for
BD-R in two ways. OPERATIONS.md's "Tool version check" requires the patched
packages: Debian 7.1-14, Fedora 7.1-13, Arch 7.1-13.

| Patch | Bug | Effect without it |
|---|---|---|
| `ignore_pseudo_overwrite.patch` | Debian #615978 | A drive reports too little BD-R capacity, and the disc cannot be filled. |
| `fix_burning_bd-r_discs.patch` | Debian #713016 | A blank BD-R fails to close: `CLOSE SESSION failed with SK=5h/INVALID FIELD IN CDB`. |

growisofs formats a blank BD-R with a spare area unless `spare:none` is
passed. It computes the capacity from `READ TRACK INFORMATION`, thus one
command line serves every media size.

### 4.3 UDF revision support for each operating system

| OS | Reads up to | Note |
|---|---|---|
| Linux 2.6.26 and newer | 2.60 | The kernel writes 2.01. |
| Windows XP | 2.01 | The oldest Windows still in the field. |
| Windows Vista to 11 | 2.60 | |
| Mac OS X 10.4 | 2.01 | |
| macOS 10.5 to 15 | 2.60 | |
| FreeBSD | 1.50 | Not supported. |
| Android | none | No UDF in AOSP. |

### 4.4 UDF measured limits

| Item | Limit | Evidence |
|---|---|---|
| Linux name length | 254 UTF-8 bytes | Measured: ASCII 254 passed, 255 failed; CJK 84 passed, 85 failed. |
| Path length | 1023 bytes in the spec, not enforced by Linux | Measured: 1409 bytes worked. |
| Directory depth | No limit | Measured: 300 levels. |
| Case | Case-sensitive on disc | Windows and macOS present it without case. |

Use an explicit `mount -t udf -o ro /dev/sr0`, never a desktop auto-mount. On
a kernel older than 5.4, pass `utf8`, not `iocharset=utf8`.

A disc root that a burner writes as plain ISO 9660 level 4 still reads: the
Linux driver folds the fixed names to lower case, and the reader accepts that.
Joliet does not read, because it cuts the 68-character object names.

### 4.5 Small files on UDF

Measured on a loop-mounted UDF 2.01 image (`mkudffs`, udftools 2.3). The
kernel embeds a file below about 1,800 bytes inside its File Entry. A larger
file costs about 2 KiB of File Entry, whatever its size. 100,000 small files
under 256 fan-out directories took about 19 s to copy in; one flat directory
of 100,000 entries showed quadratic cost. The capacity budget charges two
blocks for each file from these numbers. A bundle container stays deferred
until a real source wastes a large share of a disc this way.

### 4.6 Blu-ray media facts and damage patterns

Blu-ray uses a picket code inside every 64 KiB ECC cluster: the Long Distance
Code RS(248,216) and the Burst Indication Subcode RS(62,30). A failed cluster
is a hard read error.

| Damage pattern | LBA footprint | Size on a 25 GB BD |
|---|---|---|
| Radial scratch, 1 mm wide | ~106,000 hits of about 1 sector each | ~217 MB, never contiguous |
| Ring scratch, 1 mm radial width | one contiguous run | ~736 MB |
| Outer-edge ring, outermost 1 mm | one contiguous run at the highest LBAs | ~1.03 GB |
| Outer-edge degradation, outermost 5 mm | contiguous run at the end | ~4.7 GB |
| Fingerprint, 5 mm across | a few hundred short runs | 10 to 50 MB |
| Delamination bubble | contiguous ring segment | 100 MB to several GB |

The capacity presets of OPERATIONS.md's "Capacity" are the real sector counts
of the media. Never copy dvdisaster's BD sizes, 11,826,176 and 23,652,352
sectors: they waste about 3 percent of every disc. QL 128 GB exists as BD-R XL
only. M-DISC BD has the same sector counts as SL and DL.

## 5. Implementation notes

None of this is part of the on-disc format. FORMAT.md's "Conformance" states
what binds every implementation.

- **Language: Go.** The standard library covers SHA-256 and CRC-32C, and one
  static binary suits a recovery tool. The dependencies are
  `github.com/klauspost/compress` for zstd and
  `github.com/klauspost/reedsolomon` for the parity. A package is a
  convenience, never the definition.
- **One Go definition for each on-disc structure**, with explicit encode and
  decode functions. No reflection and no struct tags. The byte layout is
  written by hand, field by field, in the order of the table in FORMAT.md.
- **A golden file for every structure.** The test encodes known values and
  compares the bytes to a checked-in file; it also decodes that file and
  compares the fields. The first direction finds a changed layout. The second
  finds a decoder that tolerates one.
- **Vendored tables.** The Gear table and the masks are part of the format.
  Never import them from a dependency.
- **Bounded memory.** Reuse buffers in the chunk and hash path. Stream every
  object. Compute parity one stripe at a time.
- **Every read verifies.** An error names the content id or the path.
- **Comments.** A comment and a commit message never cite a section number.
  Numbers drift; name the heading or describe the rule.

## 6. Glossary

| Term | Definition |
|---|---|
| **Blob** | The object that lists the chunk ids of one file, in order. |
| **Catalog** | The REFS table, the DISCS table and every snapshot object, which each run carries for the whole repository. |
| **Checksum column** | The FEC column that holds an 8-byte digest of each data block of its stripe. |
| **Chunk** | A content-defined slice of a file. The unit of deduplication. |
| **Content id** | SHA-256 over the kind byte and the uncompressed payload of an object. |
| **Disc ledger** | `<staging>/discs.bin`: one row for each disc that the repository knows. |
| **Disc root** | A directory that holds `NOAHSARK/`: a mounted disc, a mounted image, or the directory that `pack` wrote. |
| **Disc uuid** | The identity of a disc. The two identical copies share it. |
| **FEC stream** | The stream files of a run, each padded to 2048 bytes, in INDEX order. |
| **INDEX** | The table of a run that lists its files, its objects and its prerequisites. |
| **Local cache** | `<repo>/cache/`: copies of INDEX, REFS, DISCS, snapshots, trees and blobs. Derived data only. |
| **Object** | A chunk, a blob, a tree or a snapshot. |
| **Part file** | `.<name>.noahsark-part`: the file that `restore` writes before it gives the final name. |
| **Prerequisite** | An object that a run references and that an earlier disc holds. |
| **Ref** | A name for a snapshot. The default name is the date of the commit. |
| **Repository** | The local directory with `config`, `lock`, `refs.txt`, `staging/` and `cache/`. |
| **Run** | What one `pack` writes. One run goes on one disc. |
| **Snapshot** | The object that names a root tree, a time and a message. |
| **Staging** | The local store that holds objects between `commit` and `gc`. |
| **State log** | `<staging>/state.db`: the append-only record of each object's state. |
| **Stripe** | Block `i` of every column: 231 data, 1 checksum, 23 parity. |
| **TLV** | A type-length-value record in the extension area of a tree entry. |
| **Tree** | The object that describes one directory, with the metadata of each entry. |
| **Unstable** | A file whose size or mtime changed while `commit` read it. The entry carries the `UNSTABLE` flag. |

## 7. References

| Topic | Reference |
|---|---|
| FastCDC | W. Xia et al., "The Design of Fast Content-Defined Chunking for Data Deduplication Based Storage Systems", IEEE TPDS, 2020. |
| Reed-Solomon codes | I. S. Reed and G. Solomon, "Polynomial Codes over Certain Finite Fields", J. SIAM, 1960. |
| Cauchy generator matrices | J. Blömer et al., "An XOR-Based Erasure-Resilient Coding Scheme", ICSI TR-95-048, 1995. |
| SHA-256 | NIST FIPS 180-4, Secure Hash Standard. |
| CRC-32C | G. Castagnoli et al., IEEE Transactions on Communications, 1993; parameters as in RFC 3720. |
| zstd | RFC 8878, Zstandard Compression. |
| UDF 2.01 | OSTA Universal Disk Format Specification, revision 2.01, and ECMA-167, 3rd edition. |
| Blu-ray error correction | Blu-ray Disc Association, "White Paper Blu-ray Disc Format, 1.A Physical Format Specifications for BD-RE". |
| dvd+rw-tools | growisofs and dvd+rw-mediainfo, upstream 7.1 with the Debian patch set. |
| udftools | mkudffs and udfinfo, 2.3 or later. |
| GNU ddrescue | The ddrescue manual. |
| dvdisaster | The RS03 codec, a similar column-and-stripe layout. |
| Linux kernel | `fs/udf/super.c`, the write-once read-only rule. |

## 8. Change log

FORMAT.md cites this change log. Until the first release the format is not
frozen, and a disc carries its own `FORMAT.txt` and `decoder.py`. The git
history holds the detail of each version; this table holds one line for each
version that changed what an operator or a reader sees.

| Document version | Change |
|---|---|
| 0.6.2 | `restore`, `ls`, `log` and `recover` lose `--discs-dir`; give a shell glob such as `/mnt/discs/*` as `DISC-ROOT` arguments instead. No on-disc format change. |
| 0.6.1 | `verify` checks header_crc32c and file_hash the same way `restore` does, closing the gap where a disc could read clean and then fail restore. `verify --heal` no longer counts a healed directory as a verified copy. No on-disc format change. |
| 0.6.0 | The documents are cut to the size of the tool. No behaviour changes. |
| 0.5.4 | `pack` loses the flags that select objects, and the config keeps six keys. |
| 0.5.3 | The local cache moves into `<repo>/cache/`. |
| 0.5.2 | The content id covers the kind byte. The default ref is the date of today; `LATEST` is deleted. FORMAT.md is renumbered. |
| 0.5.1 | `restore` has one write path, through a part file, in both modes. |
| 0.5.0 | One format revision before the first tag: no minor version, smaller headers, 24 cuts. |
| 0.4.28 | `commit` records the real uid and gid; `restore` applies owner, then mode, then times. |
| 0.4.26 | The disc-swap restore writes with no spool. |
| 0.4.24 | Excludes, `--one-file-system` and `pack --dry-run`. |
| 0.4.21 | Exit codes are 0, 1 and 2 for every command. |
| 0.4.20 | One non-blocking exclusive repository lock. |
| 0.4.18 | Five staging states and one state file. |
| 0.4.16 | The cache and the state log key a disc by its uuid. |
| 0.4.15 | `gc` waits for two verified copies. |
| 0.4.13 | OPERATIONS.md describes the build only: no append, no burn command, no capacity estimator. |
| 0.4.4 | FEC is optional and off by default. |
| 0.4.3 | Feature bits removed. A reference decoder is on every disc. |
| 0.4.2 | SHA-256 is the only hash. |
| 0.4.1 | The bundle container is deferred; each object is one file. |
| 0.4.0 | FORMAT.md redesigned around one common header. |
| 3.0 and before | One combined specification, then three documents. Superseded in full. |
