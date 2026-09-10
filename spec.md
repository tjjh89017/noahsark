# NoahsArk Specification

Version: 2.0 (format major 1)
Status: design specification. No implementation exists yet.
Language of implementation: Go.

---

## 1. Overview

NoahsArk is a backup system for write-once optical media. It writes
content-addressed objects to Blu-ray discs. It reads them back years later.

The system has five properties.

1. Content addressing. Every object is named by the hash of its bytes.
2. Deduplication. Content-defined chunking finds repeated data inside files.
3. Self-description. Every disc carries the tables that a reader needs.
4. Self-healing. Every run carries Reed-Solomon parity over its own sectors.
5. Locality. The packer keeps a file, and usually a directory, on one disc.

The local index is an accelerator. It is never a source of truth. A user can
delete the whole local cache. The discs still answer every question.

The unit of writing is the **run**. One run is one growisofs write. A disc holds
one or more runs. The disc always has exactly one physical UDF session. Section
9 explains why true UDF multi-session is not possible with current open source
tools, and gives the evidence.

The system targets a 30-year archive. The format therefore prefers explicit
byte layouts over parsers, fixed-width records over variable-length records, and
plain files over databases. A human in 2050 must be able to read a disc with a
hex editor and the README that the disc itself carries.

### 1.1 What a disc contains

A NoahsArk disc carries one directory tree. The filesystem is selected by the
disc filesystem profile (section 9.2). The default is pure UDF 2.01.

Everything that NoahsArk writes is an ordinary file inside that tree. There are
no hidden sectors, no fixed-LBA structures, and no raw areas outside the
filesystem.

```
/NOAHSARK/
    DISC.bin                     disc superblock, written once
    README.txt                   plain-text explanation for a human
    FORMAT.txt                   the byte-layout tables of every structure
    runs/<seq>/
        RUN.bin                  run header, the first file copied in the run
        layout.bin               file order, LBA extents, shard size, k, m, hashes
        manifest.bin             sorted object table with a fan-out
        filter.bin               BinaryFuse16 over this run's object ids
        catalog/                 snapshot objects, filters, manifests, tables
        parity/pNNNN.bin         one file per parity column
        RUN2.bin                 run header copy, the last file copied in the run
    objects/<ab>/<name>          chunks and bundles, shared by every run on the disc
    trees/<ab>/<name>            tree and chunklist objects
    snapshots/<name>             snapshot objects
```

`<ab>` is the first two hex digits of the digest. `<name>` is the full
68-character multihash hex.

### 1.2 Reading this document

The document uses ASD-STE100 style. "Must" states a requirement. "Should"
states a recommendation. "May" states an option.

Every binary structure has a byte-offset table. All integers are little-endian.
Section 4 states the rules that every structure obeys. Section 26 is the
glossary. Appendix B lists every magic number and every registry.

---

## 2. Goals, non-goals, and priorities

### 2.1 Goals

1. Store a directory tree on write-once optical media without loss.
2. Restore that tree with its file metadata on any supported platform.
3. Deduplicate at sub-file granularity across all discs of a repository.
4. Survive physical damage to a disc without loss of data.
5. Survive the loss of the local machine. Discs alone must be sufficient.
6. Bound the number of disc swaps that a restore needs.
7. Keep the on-disc format readable by a person with no NoahsArk software.
8. Detect silent corruption at every hop, never use bad bytes.
9. Support files of any size. A single file may be larger than one disc.
10. Support append. A partly filled disc must accept more data later.

### 2.2 Non-goals

1. No encryption in this version. The format reserves the fields (section 8.9).
2. No signing in this version. The format reserves the fields.
3. No repository access-control model. The discs are physical objects.
4. No network protocol. NoahsArk is a local tool.
5. No delta compression between objects. A broken delta chain on write-once
   media is unrepairable.
6. No ISO 9660 bridge, no Joliet, no Rock Ridge.
7. No Go UDF writer. The system uses mkudffs and the Linux kernel udf driver.
8. No mandatory database. SQLite and other engines are dependency risks on a
   30-year medium.

### 2.3 Platform tiers

| Tier | Platform | Read | Burn | Notes |
|---|---|---|---|---|
| 1 | Linux 2.6.26 and newer | Yes | Yes | Reference platform. |
| 2 | Windows Vista to 11 | Yes | Later | ImgBurn 2.5.8.0 is the burn candidate. |
| 2 | Windows XP | Yes | No | Reads UDF 2.01, which is its ceiling. |
| 2 | macOS 10.4 to 15 | Yes | Later | `hdiutil burn`, or dvd+rw-tools from Homebrew. |
| - | FreeBSD | Not supported | No | Its kernel reads UDF 1.50 only. |

FreeBSD is not a target. Its UDF ceiling is 1.50, so it cannot mount a UDF 2.01
volume. A user with FreeBSD-hosted data reads the disc on Linux, or reads the
raw device with `dd` and mounts the image on Linux. NoahsArk must not lower its
UDF revision to close this gap, because UDF 1.50 loses features that Windows and
macOS need.

### 2.4 Priorities

The priorities are ordered. A conflict is resolved by this order.

1. Data durability. Never lose bytes.
2. Readability without the tool. A future reader must have a chance.
3. Restore usability. Fewest disc swaps, clearest plan.
4. Deduplication ratio.
5. Speed.
6. Media utilization.

Priority 4 is below priority 3 on purpose. A disc costs a small amount of money.
A disc swap costs a minute of human attention on every future restore. Section
15 turns this priority into the capping knobs.

### 2.5 Implementation phases

The design follows KISS: the simplest disc model that works is the default, and
the harder models are later phases.

**The on-disc format is complete from Phase 1.** The run chain, the LBA extents,
the catalog copies, the manifests, the filters and the parity layout are all
present in Phase 1. Later phases add no format change. A Phase 3 reader reads a
Phase 1 disc. A Phase 1 reader reads a Phase 3 disc, unless that disc uses a
feature bit that Phase 1 does not know.

| Phase | Content |
|---:|---|
| **1** | Disc filesystem profile 0 only, as section 10.3 defines it. FastCDC chunking. Dedup with filters and manifests. Cache-less restore. Per-run Reed-Solomon parity and `verify --heal`. All media sizes. Forced capacity and forced reserve. The staging state machine and GC, with a mandatory `verify` before an object becomes CLEAN. Manual `commit` only, with no watcher. The disc-major restore plan. The Phase 1 metadata set: type, mode, uid, gid, names, mtime, symlink target and hardlink group. |
| **2** | The `sync` mirror wrapper over rsync. Extended metadata: extended attributes, POSIX ACLs, the remaining timestamps, Windows attributes. Disc filesystem profile 1: POW append on UDF with the image mirror and the block diff. The LBA stability check after every append. The never-close policy. The raw append degraded mode. Spare area monitoring. |
| **3** | Watch mode, the change-recording daemon. Disc-close parity. The cross-disc parity disc. Mirror bookkeeping. `consolidate`. Multi-drive restore. `reindex`. Disc filesystem profile 2, ISO 9660:1999 level 4 with `growisofs -M`. Burning on tier-2 operating systems. |

| **Backlog** | Commit bundles: the binary on the data host, `catalog export`, `commit --out`, `import`, and deployment mode C. Specified in full and reserved in the format, but not scheduled for any phase. |

**Backlog** means specified, reserved in the on-disc format, and not scheduled.
A backlog item may never be built. Nothing else waits for it, and building it
later changes no structure, because its format is already frozen here.

Sections 19 and 20 tag every command, option and config key with its phase.

The Phase 1 disc model is deliberately simple. A disc holds exactly one run. The
run is written once. Nothing is ever overwritten. The disc is left open, so a
later phase can add a repair run, extra parity, or the leftover space, with no
format change. Every hard problem of appending is therefore a Phase 2 problem,
and a Phase 1 disc is restored by a Phase 3 reader with no special case.

### 2.6 Hard constraints

- No source file size limit. All size fields are u64.
- The index is only an accelerator. Every command must work with the cache
  absent. CI must prove this.
- A burned run is immutable. Nothing rewrites it.
- A reader must refuse an unknown `version_major`. A reader must ignore an
  unknown `version_minor`.
- A reader must refuse an unknown bit in `required_feat`. A reader must ignore
  an unknown bit in `optional_feat`.

---

## 3. Architecture

### 3.1 Component diagram

```
   +---------------------------------------------------------------+
   |                       source filesystem                        |
   +-------------------------------+-------------------------------+
                                   | scan (mtime, size, inode)
                                   v
   +---------------------------------------------------------------+
   |  COMMIT ENGINE                                                |
   |    walker -> chunker (FastCDC) -> hasher (BLAKE3/SHA-256)     |
   |    -> dedup query (filters, manifests) -> compressor (zstd)   |
   |    -> tree builder -> snapshot writer                         |
   +-------------------------------+-------------------------------+
                                   | objects + state records
                                   v
   +---------------------------------------------------------------+
   |  STAGING STORE          staging/                              |
   |    objects/ab/cd/<id>   images/<disc_uuid>.img                |
   |    state.db (append-only log)  restore/   heal/               |
   |    states: STAGED -> PACKED -> BURNED -> CLEAN -> GC-ELIGIBLE |
   +-------------------------------+-------------------------------+
                                   | pack (locality rules)
                                   v
   +---------------------------------------------------------------+
   |  RUN BUILDER                                                  |
   |    order objects -> loop-mount image -> copy in parity order  |
   |    -> read back LBA extents -> build manifest, filter, layout |
   |    -> compute RS parity + checksum column -> run header x(m+2)|
   +-------------------------------+-------------------------------+
                                   | image diff -> aligned byte runs
                                   v
   +---------------------------------------------------------------+
   |  BURN PLAN (burn.bin + burn.json)                             |
   |    device hint, seek LBA, file, size, close flag, media type, |
   |    expected next writable address, expected disc uuid          |
   +-------------------------------+-------------------------------+
                                   | burn --print renders commands
                                   v
   +---------------------------------------------------------------+
   |  EXTERNAL BURNER (outside the program)                        |
   |    Linux growisofs (default) | Linux cdrskin | Windows ImgBurn|
   |    macOS hdiutil or dvd+rw-tools                               |
   |    burn --exec runs the printed commands, Linux only          |
   +-------------------------------+-------------------------------+
                                   | eject, reload
                                   v
   +---------------------------------------------------------------+
   |  VERIFIER      read back -> compare LBA map -> hash objects   |
   |                -> mark CLEAN -> health record                 |
   +-------------------------------+-------------------------------+
                                   |
              +--------------------+--------------------+
              v                                         v
   +---------------------+                  +-------------------------+
   |  LOCAL CACHE        |                  |  DISC LIBRARY           |
   |  ~/.cache/noahsark/ |<-- rebuild ------|  physical discs         |
   |  derived data only  |                  |  each self-describing   |
   +---------------------+                  +-------------------------+
```

### 3.2 Write data flow

```
 source -> commit -> staging -> pack -> run -> burn -> verify -> clean -> GC
```

1. **source**: the user names a directory. The walker reads it.
2. **commit**: the engine chunks files, hashes chunks, queries the dedup
   filters, compresses new chunks, writes trees and a snapshot object. All new
   objects enter staging in state STAGED.
3. **staging**: objects wait on local disk. The state log is authoritative for
   objects that are not yet CLEAN.
4. **pack**: the packer selects the objects for the next run. It applies the
   locality rules of section 15. Selected objects move to PACKED.
5. **run**: the run builder makes the UDF image content, computes parity, and
   emits the byte runs to write. It also writes the burn plan.
6. **burn**: the program renders the burn plan into command lines. An external
   burner writes the byte runs. Objects move to BURNED. Section 10.7 states the
   rule: the program never burns as a core function.
7. **verify**: the verifier ejects, reloads, reads back, and compares. Objects
   move to CLEAN.
8. **clean**: the retention timer starts.
9. **GC**: after the timer, objects become GC-ELIGIBLE and may be deleted.

GC never deletes an object that is not CLEAN.

### 3.3 Restore data flow

```
 snapshot id -> object set -> run map -> set cover -> disc plan (JSON)
   -> for each disc in plan order:
        detect disc -> read needed objects in LBA order -> staging/restore/
        -> assemble every file that is now complete -> free its staging space
   -> apply metadata in the fixed order -> deferred directory times
   -> loss report -> exit code
```

### 3.4 Heal data flow

```
 verify finds bad sectors (ddrescue mapfile = erasure list)
   -> RS decode inside the run
   -> if still missing: fetch the same content id from another run or disc
   -> if still missing: cross-disc parity group
   -> if still missing: mirror disc
   -> if still missing: original source path, if it exists
   -> write recovered objects into staging/heal/
   -> mark the damaged run degraded, list the lost ids
   -> next pack writes a repair run
```

### 3.5 Component list

| Component | Responsibility | Talks to |
|---|---|---|
| walker | Enumerate the source tree, local or on an NFS or SMB mount. | chunker, tree builder |
| chunker | FastCDC cut points. | hasher |
| hasher | Content ids. | dedup, staging |
| dedup | Filter query, manifest confirm. | cache, catalog |
| compressor | Per-chunk zstd. | staging |
| tree builder | Tree objects, TLVs, canonical order. | staging |
| staging store | Object files, state log, images. | all |
| packer | Run membership, locality, capping. | staging, cache |
| run builder | Image content, LBA map, manifest, filter. | mkudffs, kernel udf |
| FEC engine | RS parity, checksum column. | run builder |
| burn planner | Burn plan, command rendering. | config templates |
| burn runner | `burn --exec`, Linux only. | external burner |
| verifier | Read back, compare, health record. | drive, cache |
| planner | Set cover, disc order, time model. | cache, catalog |
| restorer | Disc-major read, assembly, metadata. | staging, target |
| healer | RS decode, re-fetch, repair run. | staging, cache |
| cache | Merged index, filters, manifests. | all readers |
| bundle writer | `commit --out`: new objects plus `BUNDLE.bin`, for a source machine that runs the binary. Backlog. | staging layout, catalog copy |
| bundle importer | `import`: verify, drop duplicates, enter STAGED. Backlog. | staging store |

### 3.6 Trust boundaries

- Bytes read from a disc are untrusted until the content id verifies.
- The local cache is untrusted. Every cache answer is confirmed against a
  manifest before it is used to drop data.
- Names inside a tree object are untrusted. Section 17.8 states the validation.
- A symlink target is data. The restorer never traverses it.
- A source on an NFS or SMB mount is untrusted for metadata. The mount may
  synthesize uid, gid and mode. The snapshot records the source type so that a
  restore can warn (section 18.10).
- A commit bundle is untrusted. `import` verifies every content id before it
  enters staging (section 18.11).

---

## 4. Binary format rules

These rules apply to every structure in this specification. A structure that
breaks a rule is a defect.

### 4.1 The ten rules

1. **Little-endian everywhere.** No exceptions. No big-endian field exists.
2. **Fixed-width types only.** `u8`, `u16`, `u32`, `u64`, `i32`, `i64`. No
   native `int`. No varint inside a fixed header.
3. **Packed with manual alignment.** No implicit padding exists. Every field
   sits at its natural alignment by explicit layout. Every gap is a named
   reserved field. A reserved field must be written as zero. A reader must
   ignore the value of a reserved field.
4. **Header order is fixed.** Every structure begins with `magic` (u32), then
   `version_major` (u16), then `version_minor` (u16).
5. **Version policy.** A reader must refuse a structure with an unknown
   `version_major`. A reader must accept an unknown `version_minor` and must
   ignore fields that it does not know.
6. **Feature flags.** Every top-level structure carries `required_feat` (u64)
   and `optional_feat` (u64). A reader must refuse the structure if any unknown
   bit is set in `required_feat`. A reader must ignore unknown bits in
   `optional_feat`.
7. **Checksum last.** Every structure ends with a checksum over the structure
   bytes that precede it. Small headers use CRC-32C (Castagnoli, polynomial
   0x1EDC6F41, reflected, initial value 0xFFFFFFFF, final XOR 0xFFFFFFFF).
   Objects use the full content hash instead; the content id is the checksum.
8. **Hashed pointers.** Every pointer carries the hash of its target. No
   unhashed reference exists. This rule comes from GEFS.
9. **String encoding.** A string is `encoding` (u8), then `length` (u32) in
   bytes, then the bytes. Encoding 0 is UTF-8. Other encoding values are
   reserved. There is no NUL terminator. There is no normalization; a writer
   stores the bytes as it found them.
10. **Documented layout.** Every structure has a byte-offset table with the
    columns offset, size, type, name, meaning.

### 4.2 Magic values

A magic value is four ASCII bytes. The bytes are legible in a hex dump. The
field is a u32 in little-endian order, so the first byte in the file is the
first character of the mnemonic. Appendix B lists every magic value.

Example: the object header magic is `"NAOB"`. The file bytes are
`4E 41 4F 42`. The u32 value is `0x424F414E`.

### 4.3 Common object header

Every stored object begins with this header. The header is 64 bytes.

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NAOB"`, 0x424F414E. |
| 4 | 2 | u16 | `version_major` | 1. Refuse if unknown. |
| 6 | 2 | u16 | `version_minor` | 0. Ignore if unknown. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 1 | u8 | `kind` | Object kind registry, section 8.1. |
| 25 | 1 | u8 | `hash_algo` | Multicodec code. 0x12 sha2-256, 0x1e blake3. |
| 26 | 1 | u8 | `digest_len` | Digest length in bytes. 32 in version 1. |
| 27 | 1 | u8 | `compression` | Compression registry. 0 none, 1 zstd, 2 lz4. |
| 28 | 1 | u8 | `crypto` | 0 plaintext. Other values reserved. |
| 29 | 1 | u8 | `reserved_u8` | Zero. |
| 30 | 2 | u16 | `header_len` | 64 in version 1. A reader skips the excess. |
| 32 | 8 | u64 | `payload_len` | Uncompressed payload length in bytes. |
| 40 | 8 | u64 | `stored_len` | Bytes on the medium after this header. |
| 48 | 8 | u64 | `reserved_u64` | Zero. |
| 56 | 4 | u32 | `reserved_u32` | Zero. |
| 60 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 59. |

The content id is the hash of the **uncompressed payload bytes only**. The
header is not hashed. Therefore recompression of an object never changes its
name. Section 5.1 states the rule in full.

### 4.4 Feature flag registry

Bits are assigned once and never reused.

| Bit | Half | Name | Meaning |
|---:|---|---|---|
| 0 | required | `FEAT_COMPRESSION` | The structure may hold compressed payloads. |
| 1 | required | `FEAT_BUNDLES` | Chunk ids may resolve into a bundle. |
| 2 | required | `FEAT_CHUNKLIST` | Trees may reference a chunklist object. |
| 3 | required | `FEAT_TLV_SPILL` | A TLV payload may reference a chunk. |
| 4 | required | `FEAT_CRYPTO` | Objects are encrypted. Not used in version 1. |
| 5 | required | `FEAT_FEC_RS8` | The run carries GF(2^8) Reed-Solomon parity. |
| 6 | required | `FEAT_FAN16` | The manifest uses a 65536-entry fan-out. |
| 0 | optional | `OPT_BITMAPS` | Per-snapshot reachability bitmaps are present. |
| 1 | optional | `OPT_REVIDX` | A reverse index by LBA is present. |
| 2 | optional | `OPT_XLATE` | A cross-algorithm side table is present. |
| 3 | optional | `OPT_DISC_PARITY` | A disc-wide parity run is present. |

Bits 7 to 63 of each half are reserved. A writer must set them to zero.

### 4.5 Strings

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 1 | u8 | `encoding` | 0 = UTF-8. Other values reserved. |
| 1 | 3 | u8[3] | `reserved` | Zero. Keeps `length` aligned. |
| 4 | 4 | u32 | `length` | Byte count of the string data. |
| 8 | `length` | u8[] | `data` | The bytes, as found. No terminator. |

A string field inside a fixed-width record is a `(offset, length)` pair into a
variable area of the same structure. Section 8 uses this form for names.

A string is never normalized. A file name on Linux is a byte string. The
system stores those bytes. It does not convert to NFC or NFD.

### 4.6 Registries

An id in a registry is assigned once. An id is never reused. An id is never
renumbered. Appendix B repeats these tables in one place.

**Hash algorithm registry.** The values are multicodec codes.

| Code | Name | Digest bytes | Status |
|---:|---|---:|---|
| 0x12 | `sha2-256` | 32 | Supported. Every implementation must read and write it. |
| 0x1e | `blake3` | 32 | Supported. Default for new objects. |
| 0x13 | `sha2-512` | 64 | Reserved. Not used in version 1. |
| 0xb220 | `blake2b-256` | 32 | Reserved. |

**Compression registry.**

| Id | Name | Status |
|---:|---|---|
| 0 | none | Mandatory. |
| 1 | zstd | Default. |
| 2 | lz4 | Optional. |
| 3-255 | reserved | Refuse. |

**Chunker profile registry.**

| Id | Name | min | avg | max | NC level |
|---:|---|---:|---:|---:|---:|
| 1 | `P3` | 512 KiB | 2 MiB | 8 MiB | 2 |
| 2 | `P4` | 1 MiB | 4 MiB | 16 MiB | 2 |
| 3 | `P5` | 2 MiB | 8 MiB | 32 MiB | 2 |
| 4-255 | reserved | | | | |

**Filter type registry.**

| Id | Name | Status |
|---:|---|---|
| 1 | `binaryfuse16` | Default for a run filter. |
| 2 | `binaryfuse8` | Reserved. |
| 3 | `bloom` | In-memory only. Never written to a disc. |

**FEC scheme registry.**

| Id | Name | Field | Shards | Status |
|---:|---|---|---:|---|
| 1 | `rs255-gf8` | GF(2^8) | 255 | Default. k + 1 + m = 255. |
| 2 | `rs-leopard-gf16` | GF(2^16) | up to 65536 | Reserved. |

**Object kind registry.** Section 8.1 repeats it with detail.

| Id | Name |
|---:|---|
| 1 | `chunk` |
| 2 | `bundle` |
| 3 | `chunklist` |
| 4 | `tree` |
| 5 | `snapshot` |
| 6 | `ref` |

**Disc filesystem profile registry.** The profile names the filesystem on the
disc and the append mechanism. Section 9.2 defines it. Everything above the
filesystem is independent of the profile.

| Id | Name | Filesystem | Append mechanism | Status |
|---:|---|---|---|---|
| 0 | `oneshot` | UDF 2.01. Phase 1 builds UDF 2.01 only. | None in Phase 1. The disc is POW-formatted and left open, so a Phase 2 append can still reach it. | **Default. Phase 1.** |
| 1 | `udf201-pow` | Pure UDF 2.01 | POW growth. Variant 1a: kernel direct write. Variant 1b: image mirror and 32 KiB block diff. | Phase 2. |
| 2 | `iso9660v1-l4-pow` | ISO 9660:1999 level 4, plain | growisofs `-Z` then `-M` on a POW BD-R, one session | Phase 3. |
| 3-255 | reserved | | | |

**Media type registry.**

| Id | Name | Sectors | Bytes |
|---:|---|---:|---:|
| 1 | `BD-R SL 25` | 12,219,392 | 25,025,314,816 |
| 2 | `BD-R DL 50` | 24,438,784 | 50,050,629,632 |
| 3 | `BD-R XL TL 100` | 48,878,592 | 100,103,356,416 |
| 4 | `BD-R XL QL 128` | 62,500,864 | 128,001,769,472 |
| 5 | `BD-RE SL 25` | 12,219,392 | 25,025,314,816 |
| 6 | `BD-RE DL 50` | 24,438,784 | 50,050,629,632 |
| 7 | `BD-RE XL TL 100` | 48,878,592 | 100,103,356,416 |
| 8 | `M-DISC BD SL 25` | 12,219,392 | 25,025,314,816 |
| 9 | `M-DISC BD DL 50` | 24,438,784 | 50,050,629,632 |
| 10 | `image` | variable | variable |

The tool must take the true capacity from `growisofs -F`. The tool must not
use a hardcoded number for a burn. The table is for planning and for labels.

### 4.7 Version policy

- `version_major` changes when an old reader would misread the structure.
- `version_minor` changes when fields are appended and `header_len` grows.
- A reader computes the end of a fixed header from `header_len`, not from its
  own compiled size. It skips `header_len - known_len` bytes.
- A feature that an old reader can ignore gets an `optional_feat` bit, not a
  version bump.
- A feature that an old reader must not ignore gets a `required_feat` bit.
- Section 21 states the full evolution rules for each algorithm.

### 4.8 Endianness and alignment test

Every implementation must ship a golden-file test per structure. The test
writes a structure with known values and compares the bytes to a checked-in
file. The test also reads the checked-in file and compares the fields. This
catches an accidental change of layout, of padding, or of endianness.

---

## 5. Identity and hashing

### 5.1 The content id rule

The content id of an object is the hash of the **uncompressed payload bytes**.
Nothing else enters the id.

- The object kind does not enter the id.
- The chunker profile does not enter the id.
- The compression algorithm does not enter the id.
- The object header does not enter the id.
- No salt and no key enter the id.

This rule has one consequence that the whole design depends on: a chunk written
under any chunker profile, with any compression, on any disc, stays a valid and
verifiable object forever. A parameter change can only reduce future dedup. It
can never invalidate old data.

A reader verifies an object by hashing the payload after decompression and by
comparing the result to the name. A mismatch is a hard error.

### 5.2 Hash algorithms

The default algorithm for new objects is **BLAKE3-256**. SHA-256 is a fully
supported alternative. Every implementation must read both. Every implementation
must be able to write both.

Reasons for BLAKE3 as the default:

- It runs 2 to 10 times faster than SHA-256 without SHA-NI.
- The gap is largest on the arm64 machines that people use for home archives.
- It scales across cores.
- Its internal Merkle tree localizes corruption inside a large chunk.

Reason to keep SHA-256: it is standardized in FIPS 180-4 and a future reader can
reproduce it with a shell one-liner and no library.

Digests are never truncated. A digest is 256 bits. The 8-byte truncated digests
of the FEC checksum column (section 11.4) are not content ids; they detect media
decay only.

### 5.3 Digest fields in records

Every record that holds a digest uses three fields:

| Size | Type | Name | Meaning |
|---:|---|---|---|
| 32 | u8[32] | `digest` | The digest, left-aligned, zero-padded. |
| 1 | u8 | `hash_algo` | Multicodec code. |
| 1 | u8 | `digest_len` | Significant bytes. 32 in version 1. |

The field is 32 bytes even when the algorithm is shorter. A future 512-bit
algorithm needs a new record version, not a new field inside version 1.

### 5.4 Text form

The text form of a content id is the lowercase hex of the multihash bytes.

```
multihash = <algorithm code varint> <digest length varint> <digest bytes>
```

For BLAKE3-256 the prefix bytes are `1e 20`. For SHA-256 they are `12 20`. The
text form is therefore 68 hex characters: 4 for the prefix and 64 for the
digest.

Example, BLAKE3-256:

```
1e20 3f1c0a9d4b7e2f5081c6a4d3e9b2f70a1c5d8e6b4a29f03d7e1b8c5a2f4d6e90
```

The charset is `[0-9a-f]` only. The text form is used for:

- file names on a disc;
- log lines and CLI output;
- the restore plan JSON;
- the health report.

Section 10.3 shows that 68 characters is safe under every UDF and Windows name
rule.

### 5.5 Fan-out on disc

Object files use a hex fan-out over the **digest**, not over the multihash
prefix. The prefix is constant inside one hash epoch and would therefore give no
fan-out at all.

The default layout is one directory level under a per-kind root:

```
/NOAHSARK/objects/<d0><d1>/<full 68-character text form>     chunks and bundles
/NOAHSARK/trees/<d0><d1>/<full 68-character text form>       trees and chunklists
/NOAHSARK/snapshots/<full 68-character text form>            snapshots
```

Splitting the roots by kind is what lets a connectivity check read every tree
without touching a chunk, and what keeps the metadata objects contiguous on the
medium.

`d0` and `d1` are the first two hex digits of the **digest**. The directory name
is those two characters. The file name is the full 68-character multihash hex.
The file name is never stripped, never shortened, and never split. A reader can
therefore identify an object from its file name alone, with no directory
context.

One level gives 256 directories. With 6,000 objects on a 25 GB disc, a directory
holds about 23 entries. With 100,000 objects it holds about 390. Two levels give
65,536 directories, so the same 100,000 objects give about 2 entries each, which
trades a shorter scan for many more directory blocks. Both are acceptable: a
directory of a few hundred entries is still a short linear scan.

| Profile | Levels allowed | Reason |
|---|---|---|
| 1, `udf201-pow` | 1, or optionally 2 (`objects/<d0><d1>/<d2><d3>/<id>`) | A UDF append rewrites only the changed directories, so a second level costs nothing. |
| 2, `iso9660v1-l4-pow` | 1 only | Each ISO 9660 append rewrites every directory record. Two levels would create up to 65,536 directories and would cost about 107 MiB per append at 100,000 objects. |
| 0, `oneshot` | 1, or optionally 2 | There is no append. |

The disc superblock records the choice in `fanout_levels`. The longest object
path is `/NOAHSARK/objects/ab/<68>`, that is 89 characters at one level and 92
at two. Section 10.6 gives the budget.

### 5.6 Hash epochs

An **epoch** is a maximal run of runs that share one hash algorithm.

- The repository config holds `hash.current`. New objects use it.
- Every run header records the algorithm of the objects in that run.
- Every disc superblock records the algorithm of its newest run.
- Old discs keep their algorithm forever. Nothing rewrites them.

At an epoch boundary, dedup drops to zero by default. The same bytes hash to a
different id, so no new object can match an old one. The first backup after the
change rewrites the whole live data set. For a 2 TB archive that is about 80
BD-R 25 GB discs. This is the reason to choose the default hash carefully now
and to change it at most once per decade, and only for a break in the current
algorithm.

Restore is unaffected. Verification is unaffected. Each run is self-consistent
and names its own algorithm.

### 5.7 Reindex, the optional cross-algorithm table

`noahsark reindex --to <algo>` reads old runs once and writes a side table.

Table record, 72 bytes, sorted ascending by `old_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `old_id` | Digest under the old algorithm. |
| 32 | 32 | u8[32] | `new_id` | Digest under the new algorithm. |
| 64 | 8 | u64 | `run_seq` | Run that holds the bytes. |

The container has the standard header with magic `"NAXL"` and a CRC-32C at the
end. The header also states the record size.

Rules:

1. The table is derived data. It lives in the local cache.
2. A writer may copy the table onto the next disc. A writer must not treat it
   as authoritative.
3. Correctness must never depend on the table. If the table is absent, the
   backup writes the data again.
4. Building the table needs a full read of the archive: about 15 minutes per
   25 GB disc at 27 MB/s.
5. The table costs about 72 bytes per object. Three million objects cost about
   216 MB.

---

## 6. Chunking

### 6.1 Algorithm

The chunker is **FastCDC, 2020 variant** (Xia et al., IEEE TPDS 2020), with a
64-bit Gear hash and normalization level 2.

The five techniques of FastCDC, in the order that matters:

1. Gear rolling hash: `fp = (fp << 1) + Gear[byte]`. One shift, one lookup, one
   add per byte. No XOR-out is needed, because the shift pushes old bytes out.
2. Enhanced hash judgement: the mask spreads its one-bits over the high bits,
   which restores an effective window of about 48 bytes.
3. Cut-point skipping: the chunker never evaluates the hash inside the first
   `min` bytes of a chunk. It jumps the pointer to `min`.
4. Normalized chunking: `mask_s` before the average size, `mask_l` after it.
   NC level 2 adds 2 bits to `mask_s` and removes 2 bits from `mask_l`.
5. Two-byte rolling: the 2020 variant processes two bytes per step at identical
   cut points.

The Gear table and the mask constants are frozen. They are part of the on-disc
format. Appendix A holds them. A changed table gives different cut points and
silently ends dedup against every existing disc.

### 6.2 Cut point rule

```
i     = min
fp    = 0
while i < len:
    fp = (fp << 1) + Gear[data[i]]
    if i < avg:
        if (fp & mask_s) == 0: return i + 1
    else:
        if (fp & mask_l) == 0: return i + 1
    i = i + 1
    if i >= max: return max
return len
```

The chunker must not evaluate the hash before offset `min`. A file shorter than
`min` is exactly one chunk.

### 6.3 Profiles

`max = 4 * avg` and `min = avg / 4` in every profile. This is the FastCDC paper
ratio and it keeps normalization level 2 in its intended regime.

| Id | Name | min | avg | max | Objects per 25 GB run | Objects per 100 GB run | UDF overhead per 25 GB |
|---:|---|---:|---:|---:|---:|---:|---:|
| 1 | P3 | 512 KiB | 2 MiB | 8 MiB | ~11,930 | ~47,700 | ~36 MiB (0.15%) |
| 2 | **P4** | **1 MiB** | **4 MiB** | **16 MiB** | **~5,965** | **~23,860** | **~18 MiB (0.08%)** |
| 3 | P5 | 2 MiB | 8 MiB | 32 MiB | ~2,980 | ~11,930 | ~9 MiB (0.04%) |

**P4 is the default.** It gives about 6,000 objects on a 25 GB disc, which is
comfortable for a UDF directory tree, for a per-run manifest, and for a filter.
A 4 MiB chunk still catches the shifted-insert edits that a 16 MiB fixed chunk
misses.

The per-object UDF cost is about 3.1 KiB: one 2048-byte block for the File
Entry ICB, about 40 bytes plus the name for the File Identifier Descriptor in
the parent directory, and on average 1 KiB of tail padding to the block
boundary.

Restore seek cost, worst case, at 150 ms per seek and one seek per chunk:

| Profile | Seeks per GB | Seek time per GB |
|---|---:|---:|
| P3 | 512 | 77 s |
| P4 | 256 | 38 s |
| P5 | 128 | 19 s |

The worst case does not happen for freshly written data, because section 15
lays objects out in file order. The column is the price of dedup against an
older disc. The capping knobs of section 15 bound it.

### 6.4 Profile recording and change

Every run header records the profile by **name and by full value**: id, min,
avg, max, NC level, Gear table id, bundle threshold, bundle target.

A reader never needs the profile. A reader follows content ids only. The record
exists for diagnosis and for a future migration.

To change the profile, the user sets `chunker.profile`. New runs use the new
profile. Old discs keep theirs. Dedup across the boundary drops to whatever the
two profiles happen to agree on. There is no rechunk operation, because discs
are write-once.

A writer must never change the Gear table or the mask constants under an
existing profile name.

### 6.5 Small files and bundles

A file smaller than `min` does not go through the chunker. It goes into a
bundle (section 8.3). A tail chunk smaller than 256 KiB must also go into a
bundle.

The reason is UDF overhead and seeks. One UDF File Entry costs 2,048 bytes. The
directory entry costs about 1,024 more. A 40 KB photo therefore pays 3.1 KiB of
overhead, which is 8 percent, and one seek on restore. A
directory of 30,000 photos would become 30,000 UDF files and 90 minutes of pure
seeking.

### 6.6 Sparse files

There is no extent table in the format for sparse regions.

- All-zero regions produce identical maximum-size zero chunks. Those chunks
  dedup to one object in the repository.
- On restore, the restorer detects an all-zero chunk. It skips the write, or it
  punches a hole with `fallocate(FALLOC_FL_PUNCH_HOLE)`. The file becomes sparse
  again.
- The backup may use `SEEK_HOLE` and `SEEK_DATA` to skip holes quickly. This is
  a speed optimization only. It must not change the object stream. A file read
  with and without the optimization must produce the same chunk ids.
- A `SPARSE` flag bit exists in the tree entry (section 8.5). It is a hint for
  the restorer. It is not a data structure.

The maximum-size zero chunk for profile P4 is 16 MiB of zero bytes. Its content
id under BLAKE3-256 and under SHA-256 must be recorded in the implementation as
a constant and tested. A writer must not special-case it; it is an ordinary
object that dedup finds.

### 6.7 Determinism

The chunker must be deterministic. The same input bytes must produce the same
cut points on every platform, with any buffer size, and with any read pattern.
CI must include golden vectors: an input file and the expected list of
`(offset, length, content id)` triples for each profile.

---

## 7. Compression

### 7.1 Order of operations

The order is fixed:

1. Chunk the stream.
2. Hash the uncompressed chunk bytes. This is the content id.
3. Compress the chunk bytes.
4. Write the object header, then the compressed bytes.

A writer must never compress a whole file before chunking. Whole-file
compression destroys the cut points and therefore destroys dedup.

### 7.2 Header fields

The common object header (section 4.3) records the result:

- `compression`: the algorithm id.
- `payload_len`: the uncompressed length.
- `stored_len`: the bytes actually written.

A reader decompresses `stored_len` bytes into `payload_len` bytes and then
verifies the content id. A length mismatch is a hard error.

### 7.3 Algorithm and level

The default algorithm is **zstd at level 3**. Level 3 is the balance point: it
is much faster than the burner and it gives most of the ratio of higher levels.

The config keys are `compression.algorithm` and `compression.level`.

### 7.4 Heuristic

If compression saves less than `compression.min_gain` of the chunk, the writer
stores the chunk with compression id 0.

The default of `compression.min_gain` is 5 percent. The reason is restore cost:
a 2 percent gain costs a decompression pass on every future restore and on every
scrub, and it makes the stored length unpredictable.

The writer applies the heuristic per chunk. It must not apply it per file.

### 7.5 Compression and dedup

Compression never affects a content id. Two writers with different compression
settings produce identical ids for identical data. A run may therefore hold a
mixture of compressed and uncompressed objects with the same ids as another run.

### 7.6 What is never compressed

- The disc superblock.
- Every run header copy.
- The run layout table.
- The manifest container.
- The filter.
- `README.txt`.

These structures must be readable by a forensic tool with no library. The bytes
they cost are negligible.

---

## 8. Object model

### 8.1 Object kinds

| Id | Kind | Payload | References | Stored as |
|---:|---|---|---|---|
| 1 | `chunk` | Opaque bytes. | None. | One file, or a slice of a bundle. |
| 2 | `bundle` | Concatenated small chunks plus an index. | The chunks it holds. | One file. |
| 3 | `chunklist` | Ordered chunk ids and lengths. | Chunks, and other chunklists. | One file. |
| 4 | `tree` | One directory. | Trees, chunklists, chunks. | One file. |
| 5 | `snapshot` | Root tree, parent, generation, text. | Root tree, parent snapshot. | One file. |
| 6 | `ref` | A named pointer to a snapshot. | A snapshot. | A row in the ref table. |

A chunk carries no reference. This is why a chunk is hash-agility friendly: its
bytes are identical under any hash algorithm.

### 8.2 Object graph

```
     ref "LATEST"
         |
         v
     snapshot #42 --parent--> snapshot #41 --parent--> ... --> snapshot #1
         |                     (object may live on an older run)
         | root_tree
         v
      tree "/"  ------------------------------------------+
       |  |  |                                            |
       |  |  +--> tree "docs"   (unchanged: prerequisite, 12.4) |
       |  |          |                                    |
       |  |          +--> chunk d1  (on run 7)            |
       |  |                                               |
       |  +-----> tree "src"                              |
       |             |                                    |
       |             +--> chunk s1 (in bundle B4)         |
       |             +--> chunk s2 (in bundle B4)         |
       |                                                  |
       +---------> chunklist CL7  "big.iso"               |
                      |                                   |
                      +--> chunk a1 (this run)            |
                      +--> chunk b2 (run 1: prerequisite) |
                      +--> chunk c3 (run 2: prerequisite) |
                                                          |
     Every arrow carries the hash of its target. <--------+
```

### 8.3 Chunk and bundle

A **chunk** object is the common header followed by the payload bytes.

A **bundle** holds chunks that are smaller than `bundle.threshold` (default
1 MiB). The target bundle size is `bundle.target_size` (default 64 MiB). A
bundle is filled in path order from one directory subtree, so that restoring a
photo folder reads one or two contiguous bundles.

Bundle payload layout:

```
 [ common object header, kind = 2 ]
 [ bundle header, 64 bytes                    ]
 [ chunk payload 0 ][ chunk payload 1 ] ...    each with its own compression
 [ index table: entry_count * 64 bytes         ]
 [ bundle trailer, 32 bytes                    ]
```

Bundle header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NABD"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 4 | u32 | `entry_count` | Number of chunks in this bundle. |
| 28 | 2 | u16 | `entry_size` | 64. A reader strides by this value. |
| 30 | 1 | u8 | `hash_algo` | Multicodec code of the entry ids. |
| 31 | 1 | u8 | `digest_len` | 32. |
| 32 | 8 | u64 | `index_off` | Offset of the index table from the bundle header. |
| 40 | 8 | u64 | `data_off` | Offset of the first chunk payload. |
| 48 | 8 | u64 | `data_len` | Total bytes of all chunk payloads. |
| 56 | 4 | u32 | `reserved_u32` | Zero. |
| 60 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 59. |

Bundle index entry, 64 bytes, sorted ascending by `content_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | Chunk id. |
| 32 | 8 | u64 | `offset` | Byte offset from `data_off`. |
| 40 | 8 | u64 | `stored_len` | Bytes stored for this chunk. |
| 48 | 8 | u64 | `payload_len` | Uncompressed bytes. |
| 56 | 1 | u8 | `compression` | Compression id for this chunk. |
| 57 | 1 | u8 | `hash_algo` | Multicodec code. |
| 58 | 1 | u8 | `digest_len` | 32. |
| 59 | 1 | u8 | `reserved_u8` | Zero. |
| 60 | 4 | u32 | `reserved_u32` | Zero. |

Bundle trailer, 32 bytes, at the end of the payload:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NABT"`. |
| 4 | 4 | u32 | `entry_count` | Repeat of the entry count. |
| 8 | 8 | u64 | `index_off` | Repeat of the index offset. |
| 16 | 8 | u64 | `payload_len` | Bundle payload length, for validation. |
| 24 | 4 | u32 | `reserved_u32` | Zero. |
| 28 | 4 | u32 | `trailer_crc32c` | CRC-32C over bytes 0 to 27. |

The trailer sits at the end so that a writer can stream a bundle. A reader
seeks to the last 32 bytes, reads the magic at its start, and then reads the
index. The magic is first and the checksum is last, as rule 7 of section 4.1
requires.

A bundle is itself an object. Its name is the hash of its own payload. A tree
entry references the **chunk id**, never the bundle id. The manifest maps the
chunk id to `(bundle id, offset, length)`. A reader that has the manifest does a
ranged read. A reader with no manifest reads the bundle and uses its index.

A bundle must not hold a delta. A delta chain on write-once media is a
durability hazard: one bad bundle breaks the chain.

### 8.4 Chunklist

A file with more than 64 chunks references a chunklist object instead of an
inline chunk array. Trees stay small, and an unchanged large file costs one
reference.

Chunklist payload header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NACL"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 8 | u64 | `entry_count` | Number of entries. |
| 32 | 8 | u64 | `total_size` | Sum of `length` over all entries. |
| 40 | 2 | u16 | `entry_size` | 48. |
| 42 | 1 | u8 | `hash_algo` | Multicodec code. |
| 43 | 1 | u8 | `digest_len` | 32. |
| 44 | 1 | u8 | `level` | 0 = entries are chunks. 1 = entries are chunklists. |
| 45 | 3 | u8[3] | `reserved` | Zero. |
| 48 | | | `entries` | `entry_count` records of 48 bytes. |

Chunklist entry, 48 bytes, in file order:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | Chunk id, or child chunklist id. |
| 32 | 8 | u64 | `length` | Uncompressed bytes this entry contributes. |
| 40 | 8 | u64 | `file_offset` | Offset of this entry inside the file. |

A `level` above 0 gives a tree of chunklists. A chunklist of 100,000 entries is
4.8 MB, which is acceptable, so level 1 is needed only for extreme files. A
reader must support both levels.

### 8.5 Tree

A tree object is one directory.

Tree payload header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NATR"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 4 | u32 | `entry_count` | Number of entries. |
| 28 | 2 | u16 | `header_len` | 40. |
| 30 | 1 | u8 | `hash_algo` | Multicodec code of the child ids. |
| 31 | 1 | u8 | `digest_len` | 32. |
| 32 | 8 | u64 | `payload_len` | Payload length, for validation. |
| 40 | | | `entries` | Entries, back to back, each self-delimiting. |

Entries are sorted by raw name bytes, ascending, with unsigned byte comparison.
A directory name compares as if a `/` byte were appended, which is Git's rule.
Sorting is mandatory. Without it, two identical directories produce different
hashes and dedup breaks.

#### 8.5.1 Tree entry fixed header, 112 bytes

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `entry_len` | Total entry length including all variable areas. Multiple of 8. |
| 4 | 2 | u16 | `header_len` | 112 in version 1. A reader skips the excess. |
| 6 | 1 | u8 | `entry_type` | 1 regular, 2 directory, 3 symlink, 4 chardev, 5 blockdev, 6 fifo, 7 socket. 0 is invalid. |
| 7 | 1 | u8 | `entry_flags` | See 8.5.2. |
| 8 | 8 | u64 | `size` | Regular files only. 0 otherwise. |
| 16 | 8 | u64 | `hardlink_group` | 0 = not a member of a hardlink group. |
| 24 | 8 | i64 | `mtime_sec` | Seconds since 1970-01-01 UTC. |
| 32 | 8 | i64 | `atime_sec` | Valid only when `ATIME_ABSENT` is clear. |
| 40 | 8 | i64 | `ctime_sec` | Valid only when `CTIME_ABSENT` is clear. |
| 48 | 8 | i64 | `btime_sec` | Valid only when `BTIME_ABSENT` is clear. |
| 56 | 4 | u32 | `mtime_nsec` | 0 to 999,999,999. |
| 60 | 4 | u32 | `atime_nsec` | Same range. |
| 64 | 4 | u32 | `ctime_nsec` | Same range. |
| 68 | 4 | u32 | `btime_nsec` | Same range. |
| 72 | 4 | u32 | `mode` | Low 12 bits are `rwxrwxrwx` plus setuid 04000, setgid 02000, sticky 01000. Bits 12 to 31 reserved, zero. The file type is not here. |
| 76 | 4 | u32 | `uid` | 0xFFFFFFFF means unknown. |
| 80 | 4 | u32 | `gid` | 0xFFFFFFFF means unknown. |
| 84 | 4 | u32 | `rdev_major` | Device entries only, else 0. |
| 88 | 4 | u32 | `rdev_minor` | Device entries only, else 0. |
| 92 | 4 | u32 | `content_off` | Offset to the content reference area. 0 when there is none. |
| 96 | 4 | u32 | `content_len` | Bytes in that area. See 8.5.3. |
| 100 | 4 | u32 | `ext_off` | Offset to the TLV area. 0 when `ext_len` is 0. |
| 104 | 4 | u32 | `ext_len` | Bytes in the TLV area, padding included. |
| 108 | 2 | u16 | `name_off` | Offset to the name bytes. 112 in version 1. |
| 110 | 2 | u16 | `name_len` | Name length in bytes, 1 to 4095. No terminator. |

All offsets are relative to the first byte of the entry. Every variable area
starts on an 8-byte boundary. The encoder writes zero padding between areas.
Padding is covered by `entry_len`.

#### 8.5.2 Entry flags

| Bit | Name | Meaning |
|---:|---|---|
| 0 | `HARDLINK_MEMBER` | The entry belongs to a hardlink group. Redundant with a non-zero `hardlink_group`, kept for a cheap test. |
| 1 | `ATIME_ABSENT` | `atime_sec` and `atime_nsec` carry no information. |
| 2 | `CTIME_ABSENT` | `ctime_sec` and `ctime_nsec` carry no information. |
| 3 | `BTIME_ABSENT` | `btime_sec` and `btime_nsec` carry no information. |
| 4 | `SPARSE` | The source file had holes. The restorer punches holes, as section 6.6 states. |
| 5 | `METADATA_PARTIAL` | The source read failed for at least one metadata field. |
| 6 | `CONTENT_IS_CHUNKLIST` | The content area holds one chunklist id, not chunk ids. |
| 7 | `UNSTABLE` | The file changed while it was being read, and no earlier consistent version existed. The content is one possible read of a moving file. See section 18.6. |

#### 8.5.3 Variable areas

| Area | Start | Length | Content |
|---|---|---|---|
| Name | `name_off` | `name_len` | Raw bytes of exactly one path component. |
| Content refs | `content_off` | `content_len` | See below. |
| Extension TLVs | `ext_off` | `ext_len` | TLV records, sorted. |

Content area by entry type:

| `entry_type` | `content_len` | Content |
|---|---|---|
| 1 regular, inline | `32 * chunk_count` | Chunk ids in file order, up to 64 ids. |
| 1 regular, chunklist | 32 | One chunklist id. `CONTENT_IS_CHUNKLIST` is set. |
| 1 regular, empty | 0 | No content area. |
| 2 directory | 32 | One tree id. |
| 3 symlink | 0 | The target is TLV `SYMLINK_TARGET`. |
| 4, 5, 6, 7 | 0 | No content. |

#### 8.5.4 Extension TLV record

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 2 | u16 | `tlv_type` | Registry, 8.5.5. |
| 2 | 2 | u16 | `tlv_flags` | bit0 `CRITICAL`, bit1 `SPILLED`, bits 2 to 15 reserved. |
| 4 | 4 | u32 | `tlv_len` | Payload bytes, excluding this prefix and excluding padding. |
| 8 | `tlv_len` | u8[] | `payload` | The value, or the spill reference. |
| | pad | u8[] | | Zero bytes to the next 8-byte boundary. |

TLVs are sorted ascending by `tlv_type`, then by payload bytes. Canonical order
is mandatory: identical metadata must hash identically.

A reader that meets an unknown TLV with `CRITICAL` set must refuse the entry. A
reader that meets an unknown TLV without `CRITICAL` must keep it on copy and
must report it on restore.

When `SPILLED` is set, the payload is exactly 40 bytes: a 32-byte chunk id and
a u64 uncompressed length. The rule for spilling is in section 8.5.6.

#### 8.5.5 TLV type registry

| Type | Name | Critical | Payload |
|---:|---|---|---|
| 0x0001 | `SYMLINK_TARGET` | yes | Raw bytes. Mandatory when `entry_type` is 3. Never validated as UTF-8. |
| 0x0002 | `USER_NAME` | no | UTF-8 bytes. |
| 0x0003 | `GROUP_NAME` | no | UTF-8 bytes. |
| 0x0010 | `XATTR` | no | `u32 count`, then `count` of `{u32 name_len, u32 value_len, name, value}`, each item padded to 4. Sorted by name bytes. |
| 0x0011 | `ACL_ACCESS` | no | `u32 count`, then `{u16 tag, u16 perm, u32 id}`. `tag`: 1 user_obj, 2 user, 3 group_obj, 4 group, 5 mask, 6 other. |
| 0x0012 | `ACL_DEFAULT` | no | Same shape. Directories only. |
| 0x0013 | `ACL_NFS4` | no | `u32 count`, then `{u16 type, u16 who_kind, u32 flags, u32 mask, u32 who_id}`. |
| 0x0020 | `LINUX_ATTR` | no | `u32` `FS_IOC_GETFLAGS` bitmask. |
| 0x0021 | `BSD_FLAGS` | no | `u32` `st_flags`. |
| 0x0030 | `WIN_ATTRS` | no | `u32` `FILE_ATTRIBUTE_*` bitmask. |
| 0x0031 | `WIN_SD` | no | Opaque self-relative `SECURITY_DESCRIPTOR`. Inheritance flags preserved exactly. |
| 0x0032 | `WIN_ADS` | no | `u32 count`, then `{u16 name_len_bytes, UTF-16LE name, u64 size, u32 hash_count, 32-byte ids}`. |
| 0x8000-0xBFFF | reserved critical | yes | Future critical extensions. |
| 0xF000-0xFFFF | vendor | no | Never critical. |

Types in the range 0x8000 to 0xBFFF are reserved for future critical
extensions. A reader can therefore decide "I must fail" from the number alone,
with no registry lookup.

POSIX ACLs are stored in the portable binary form above. A writer must not
store the Linux kernel `system.posix_acl_access` blob, because that blob is
architecture-specific and version-specific. A writer must not store the text
form, because that costs a parse on every restore.

macOS resource forks and Finder info are plain xattrs
(`com.apple.ResourceFork`, `com.apple.FinderInfo`). They use TLV `XATTR`. They
have no special field.

#### 8.5.6 Spill rule

If the total TLV area would exceed `tree.tlv_spill_threshold` (default 4 KiB),
the writer spills the largest eligible payloads. An eligible payload becomes
ordinary content chunks, and the TLV holds the 40-byte spill reference with
`SPILLED` set.

- Eligible for spill: `XATTR`, `ACL_ACCESS`, `ACL_DEFAULT`, `ACL_NFS4`,
  `WIN_SD`, `WIN_ADS`.
- Never spilled: name, `SYMLINK_TARGET`, `USER_NAME`, `GROUP_NAME`.

The rule keeps a directory of ordinary files in one contiguous read. A file
with a 64 KiB resource fork does not bloat its parent directory, and the fork
gets deduped, which matters when a whole subtree shares one large ACL.

#### 8.5.7 Worked size example

A regular file named `report.pdf`, one 4 MiB chunk, no extensions:

```
  0 .. 111   fixed header                112 bytes
112 .. 121   name "report.pdf"             10 bytes
122 .. 127   pad to an 8-byte boundary      6 bytes
128 .. 159   one chunk id                  32 bytes
entry_len = 160
```

The same file with a user name, a group name, and two small xattrs adds about
another 100 bytes.

#### 8.5.8 Name validation

A tree entry name is exactly one path component. The parser must reject an
entry whose name:

- is empty;
- is `.` or `..`;
- contains a `/` byte;
- contains a `\` byte;
- contains a NUL byte.

The rejection happens at parse time, not at write time. The whole
path-traversal class of bug therefore cannot reach the restorer. This is a
format invariant, not a heuristic.

A name is not required to be valid UTF-8. Linux file names are byte strings.

#### 8.5.9 What is never stored

- Inode numbers. They leak host state and make an unchanged tree change.
- Device ids of the source filesystem. Same reason.
- Link counts. They are derivable from the hardlink group, and storing them
  would make an entry change when an unrelated link appears elsewhere.

### 8.6 Hardlinks

Every member of a hardlink group carries the same `hardlink_group` id. Every
member carries the full content reference. No member is a master.

The id is `truncate64(BLAKE3(snapshot_salt || dev || ino))`, or a per-snapshot
counter. It is repository-local. It must not be the raw inode number.

Consequences:

1. Restoring one member of a group produces a correct regular file.
2. There is no dangling-link failure mode, unlike tar.
3. The tree does not depend on traversal order, so content addressing holds.
4. Dedup makes the repeated content reference free at the chunk level.

The restorer keeps a map from `hardlink_group` to the first restored
`(dirfd, name)`. On a second member it calls `linkat`. On failure it writes the
content again and records a `hardlink_degraded` event.

Directories never have a hardlink group.

### 8.7 Snapshot

A snapshot is one root pointer plus a generation number. This is the GEFS rule.

Snapshot payload header, 136 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NASN"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 32 | u8[32] | `root_tree` | Content id of the root tree. |
| 56 | 32 | u8[32] | `parent` | Content id of the parent snapshot. All zero for a root. |
| 88 | 8 | u64 | `generation` | 1 + parent generation. 1 for a root. |
| 96 | 8 | i64 | `time_sec` | Snapshot time, seconds. |
| 104 | 4 | u32 | `time_nsec` | Nanoseconds. |
| 108 | 4 | i32 | `tz_offset_sec` | Local zone offset at snapshot time. |
| 112 | 8 | u64 | `total_size` | Sum of uncompressed bytes reachable. For planning. |
| 120 | 8 | u64 | `object_count` | Objects reachable from this snapshot. |
| 128 | 1 | u8 | `hash_algo` | Multicodec code of every id in this snapshot. |
| 129 | 1 | u8 | `chunker_profile` | Chunker profile id used to produce it. |
| 130 | 2 | u16 | `meta_count` | Number of TLV records that follow. |
| 132 | 1 | u8 | `source_type` | Where the source tree was read from. See below. |
| 133 | 1 | u8 | `source_flags` | What the source could not provide. See below. |
| 134 | 2 | u16 | `reserved_u16` | Zero. |
| 136 | | | TLV records | `meta_count` records follow. |

Metadata TLV record:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 2 | u16 | `tag` | 1 author, 2 host, 3 message, 4 source path, 5 filter spec. |
| 2 | 2 | u16 | `flags` | bit0 CRITICAL. |
| 4 | 4 | u32 | `len` | Payload bytes. |
| 8 | `len` | u8[] | `value` | UTF-8 for tags 1 to 4. |
| | pad | | | Zero to the next 4-byte boundary. |

`source_type` records where the source tree was read from:

| Id | Name | Meaning |
|---:|---|---|
| 0 | `unknown` | Not recorded. A pre-1.0 writer. |
| 1 | `local` | A local filesystem. Full metadata fidelity. |
| 2 | `snapshot` | A filesystem snapshot of a local filesystem. |
| 3 | `nfs` | An NFS mount. |
| 4 | `smb` | An SMB or CIFS mount. |
| 5 | `bundle` | Imported from a commit bundle (section 18.11). The bundle writer's own source type is in its `BUNDLE.bin`. |

`source_flags` records what the source could not provide:

| Bit | Name | Meaning |
|---:|---|---|
| 0 | `NO_CTIME` | ctime was not trusted, so the quick check used size and mtime only. |
| 1 | `NO_HARDLINKS` | The source did not report link counts, so hardlink groups were not detected. |
| 2 | `NO_SPARSE` | `SEEK_HOLE` was unavailable, so holes were found by reading. |
| 3 | `SYNTHETIC_IDS` | uid, gid or mode may have been synthesized by the mount. |
| 4 | `CASE_INSENSITIVE` | The source did not distinguish names by case. |
| 5 | `MTIME_SLACK` | An mtime slack was applied in the quick check. |
| 6 to 7 | reserved | Zero. |

`restore` prints one warning per set bit, once, before it writes anything.

`generation` makes ancestry an integer comparison in the common case.
`object_count` and `total_size` let the planner report progress and check
completeness before any disc is read.

### 8.8 Ref

A ref is a named pointer to a snapshot. Refs live in the ref table of the
catalog (section 12.5), not in a separate object file, because a disc cannot be
mutated.

Ref record, 96 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `snapshot_id` | Content id of the snapshot. |
| 32 | 8 | i64 | `time_sec` | When the ref took this value. |
| 40 | 4 | u32 | `time_nsec` | Nanoseconds. |
| 44 | 4 | u32 | `name_len` | Byte length of the name. |
| 48 | 40 | u8[40] | `name` | UTF-8, zero-padded. Names above 40 bytes are refused. |
| 88 | 8 | u64 | `run_seq` | Run that recorded this value. |

The table is append-only across runs. A reader takes the newest record for each
name, by `run_seq` and then by time. `LATEST` is the reserved name for the
newest snapshot. This is a reflog, not a ref: the whole history of the pointer
is distributed over the runs in burn order.

### 8.9 Reserved crypto fields

Encryption and signing are deferred. The fields exist now, because a write-once
format cannot grow a field later.

- The common object header has a `crypto` byte at offset 28. Value 0 is
  plaintext. Other values name a future suite.
- The disc superblock has a 256-byte key-material region at offset 512. In
  version 1 the region is all zero.
- `required_feat` bit 4 is `FEAT_CRYPTO`. A version 1 writer must not set it.

A future encryption design follows the age separation. Chunk payloads get
authenticated encryption. Chunk ids become an HMAC of the plaintext, so dedup
survives. Signing stays a separate concern, as a detached Ed25519 signature over
the root snapshot id. None of this is part of version 1.

### 8.10 Canonical ordering summary

| Structure | Order key |
|---|---|
| Tree entries | Raw name bytes, ascending, directories compared with a trailing `/`. |
| TLV records | `tlv_type` ascending, then payload bytes ascending. |
| Xattr items inside a TLV | Name bytes ascending. |
| Chunklist entries | File offset ascending. |
| Bundle index entries | Content id ascending. |
| Manifest records | Content id ascending. |

Two identical directories must serialize to identical bytes. A writer that
breaks a rule in this table breaks dedup silently.

---

## 9. Disc, run, and append model

### 9.1 The physical disc

A physical disc has these properties:

- a `disc_uuid`, 16 bytes, generated once and never reused;
- a human label, printed on the disc;
- a `disc_seq`, u64, monotonic inside the repository;
- a media type from the registry of section 4.6;
- a capacity in sectors, taken from the drive;
- a disc filesystem profile id;
- the hash of the previous disc's superblock, which chains the set.

The chain proves the ordering of the set and detects a substituted disc. There
is no mutable commit point on write-once media, so the ordering must be provable
from the discs themselves.

### 9.2 Disc filesystem profile

A **disc filesystem profile** names the filesystem on the disc and the mechanism
that appends to it. The disc superblock records the profile id in `fs_profile`.
The registry is in section 4.6 and in Appendix B.

| Id | Name | Filesystem | Append | Status |
|---:|---|---|---|---|
| 0 | `oneshot` | UDF 2.01. Phase 1 builds UDF 2.01 only. | None. One large run. The disc stays open unless the user seals it. | **Default. Phase 1.** |
| 1 | `udf201-pow` | Pure UDF 2.01 | POW growth. Variant 1a: kernel direct write. Variant 1b: image mirror and block diff. | Phase 2. |
| 2 | `iso9660v1-l4-pow` | ISO 9660:1999 level 4, plain | `growisofs -M` on a POW BD-R | Phase 3. |

Several subsections below apply to profile 1 only. Each one is marked. These
features exist only there:

- next-writable-address handling;
- the block diff;
- the LBA re-verification after an append;
- the spare area budget;
- the raw append degraded mode;
- the never-close policy for a disc that receives more runs.

These items are identical across every profile:

- the object model and the object ids;
- the run structure and the run header;
- the manifest, the filter, and the catalog;
- the Reed-Solomon parity, which works over LBA ranges and never over files;
- the packer, the planner, and the restorer.

A profile defines exactly these seven items and nothing else:

1. The filesystem and its revision.
2. The image builder command and its options.
3. The name charset, the maximum name length, the maximum path length, and the
   fan-out depth.
4. The directory layout of fixed-name files at the volume root.
5. The append mechanism and the exact burn command lines.
6. The method that reads back the LBA extents of every object.
7. The cross-OS read matrix.

**Everything that NoahsArk writes is an ordinary file.** There are no hidden
sectors, no fixed-LBA structures, and no raw areas outside the filesystem. The
superblock, the run headers, the layout tables, the manifests, the filters, the
catalog copies, the parity columns and the objects are all regular files under
`/NOAHSARK/`.

The run layout table still records the LBA extents of every file (section 9.7),
so a run stays readable when the filesystem directory is damaged. That path is
Phase 3 and is described in section 9.7.1. It is a recovery path, not the normal
path.

Section 10 specifies each profile in full.

### 9.3 The run

A **run** is one write of the burn plan by an external burner. It is the unit
of:

- packing;
- the manifest;
- the filter;
- the Reed-Solomon parity;
- the catalog copy.

A disc holds one or more runs. A run occupies a contiguous LBA range. A run is
self-contained: it carries its own header at its start and again at its end, its
own manifest, its own filter, its own layout table, and its own parity.

"Multi-session" in NoahsArk means "many runs on one disc". Under profile 1 and
profile 2 the disc always has exactly **one** physical session, because both
profiles grow one volume on a Pseudo-OverWrite formatted BD-R. That single fact
is what makes macOS read every appended object.

### 9.4 Disc and run layout on the medium

```
 LBA 0                                                       capacity-1
 |                                                                    |
 +--- filesystem structures (profile dependent) ----------------------+
 |                                                                    |
 |  RUN 1                        RUN 2                  RUN 3         |
 |  [base=B1, len=L1]            [base=B2, len=L2]      [base=B3,..]  |
 |                                                                    |
 |  +----------------------------------------------------------+      |
 |  | RUN.bin        run header, first file copied              |     |
 |  | layout.bin, manifest.bin, filter.bin, catalog/            |     |
 |  | snapshots/ and trees/ objects, contiguous                 |     |
 |  | objects/  bundles and chunks, in path order               |     |
 |  |--------  end of the parity domain (section 11.2)  --------|     |
 |  | parity/p0000.bin .. parity/pNNNN.bin                      |     |
 |  |   (each parity file starts with a run header copy)        |     |
 |  | RUN2.bin       run header copy, last file copied          |     |
 |  +----------------------------------------------------------+      |
 +--------------------------------------------------------------------+

 Example, BD-R SL 25 GB, capacity 12,219,392 sectors, fill ratio 0.95:
   usable   = 11,608,422 sectors
   run 1    = LBA        512 .. 4,096,511      (4,096,000 sectors, 8.39 GB)
   run 2    = LBA  4,096,512 .. 8,192,511      (4,096,000 sectors)
   run 3    = LBA  8,192,512 .. 11,608,421     (3,415,910 sectors)
   reserve  = LBA 11,608,422 .. 12,219,391     (610,970 sectors, POW spare)
```

The example is illustrative. Real run boundaries follow from the packer and
from the next writable address that the drive reports.

Under profile 2 the filesystem directory records of every earlier run are
rewritten inside the newest run. Those bytes are part of the newest run and are
therefore covered by the newest run's parity. The data extents of earlier runs
are untouched and stay covered by their own parity. Under profile 1 only the
changed directory blocks are rewritten, and the writer places them inside the
newest run for the same reason.

### 9.5 Disc superblock

The superblock is 2048 bytes, exactly one sector. It is written **once**, in the
first run, as the ordinary file `/NOAHSARK/DISC.bin`. It is never updated.

The superblock holds only **immutable** facts. Mutable disc state comes from the
run chain (section 9.6.1). That is what makes every NoahsArk structure on a disc
append-only.

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NADS"`. |
| 4 | 2 | u16 | `version_major` | 1. Refuse if unknown. |
| 6 | 2 | u16 | `version_minor` | 0. Ignore if unknown. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 16 | u8[16] | `disc_uuid` | Unique for this physical disc. |
| 40 | 16 | u8[16] | `repo_uuid` | The repository this disc belongs to. |
| 56 | 8 | u64 | `disc_seq` | Monotonic position in the repository. |
| 64 | 8 | u64 | `capacity_sectors` | As reported by the drive at first write. |
| 72 | 8 | u64 | `fill_limit_sectors` | Highest LBA the writer may use. |
| 80 | 8 | u64 | `capacity_forced_sectors` | Forced capacity, section 9.12. Equals `capacity_sectors` when no override was given. |
| 88 | 8 | u64 | `first_run_lba` | LBA of the first run header. Immutable: the first run never moves. |
| 96 | 8 | u64 | `reserved_u64a` | Zero. |
| 104 | 8 | u64 | `reserved_u64b` | Zero. |
| 112 | 8 | u64 | `reserved_u64c` | Zero. |
| 120 | 8 | u64 | `reserved_u64d` | Zero. |
| 128 | 32 | u8[32] | `reserved_hash` | Zero. |
| 160 | 32 | u8[32] | `prev_disc_super_hash` | Hash of the previous disc's superblock. All zero for `disc_seq` 0. |
| 192 | 8 | i64 | `created_sec` | First write time, seconds. |
| 200 | 4 | u32 | `created_nsec` | Nanoseconds. |
| 204 | 4 | i32 | `tz_offset_sec` | Local zone offset at first write. |
| 208 | 1 | u8 | `media_type` | Media type registry. |
| 209 | 1 | u8 | `fs_profile` | Disc filesystem profile registry. |
| 210 | 1 | u8 | `hash_algo` | Multicodec code of the newest run. |
| 211 | 1 | u8 | `digest_len` | 32. |
| 212 | 1 | u8 | `chunker_profile` | Chunker profile of the newest run. |
| 213 | 1 | u8 | `compression` | Default compression of the newest run. |
| 214 | 1 | u8 | `fec_scheme` | FEC scheme registry. |
| 215 | 1 | u8 | `crypto` | 0 plaintext. |
| 216 | 2 | u16 | `sector_size` | 2048. |
| 218 | 2 | u16 | `fs_revision` | 0x0201 for UDF 2.01. 0x0004 for ISO 9660:1999 level 4. |
| 220 | 2 | u16 | `fec_k` | Data columns. |
| 222 | 2 | u16 | `fec_m` | Parity columns. |
| 224 | 1 | u8 | `fanout_levels` | 1 by default. 2 is allowed under profile 1 and profile 0 only. |
| 225 | 1 | u8 | `append_variant` | Profile 1 only. 1 = variant 1a, kernel direct write. 2 = variant 1b, image mirror and block diff. 0 elsewhere. |
| 226 | 1 | u8 | `capacity_is_forced` | 1 when `capacity_forced_sectors` is below `capacity_sectors`. |
| 227 | 1 | u8 | `reserved_u8` | Zero. |
| 228 | 4 | u32 | `label_len` | Byte length of the label. |
| 232 | 64 | u8[64] | `label` | UTF-8, zero-padded. |
| 296 | 4 | u32 | `tool_version` | Writer version, `major<<16 \| minor<<8 \| patch`. |
| 300 | 4 | u32 | `reserved_u32` | Zero. |
| 304 | 8 | u64 | `reserved_u64e` | Zero. Object counts are mutable and live in the run chain. |
| 312 | 8 | u64 | `reserved_u64f` | Zero. Used sectors are mutable and live in the run chain. |
| 320 | 8 | u64 | `reserve_computed_sectors` | The reserve that the formula of section 10.11 produced. |
| 328 | 8 | u64 | `reserve_forced_sectors` | `disc.force_reserve`, in sectors. 0 when unset. |
| 336 | 8 | u64 | `reserve_extra_sectors` | `disc.extra_reserve`, in sectors. 0 when unset. |
| 344 | 168 | u8[168] | `reserved_a` | Zero. |
| 512 | 256 | u8[256] | `key_material` | Reserved for encryption. All zero in version 1. |
| 768 | 1276 | u8[1276] | `reserved_b` | Zero. |
| 2044 | 4 | u32 | `super_crc32c` | CRC-32C over bytes 0 to 2043. |

Every field above is decided before the first byte of user data is written. A
later append never touches the superblock.

The superblock needs no second copy of its own: it lies inside the parity domain
of the first run (section 11.2), so the Reed-Solomon layer reconstructs it after
local damage. The run headers carry the disc uuid, the repository uuid and the
disc sequence number as well, so the identity of a disc survives even the loss
of `DISC.bin`.

### 9.6 Run header

The run header is 512 bytes. It is written at the first sector of the run, at
the first sector of every parity column, and at the last sector of the run. That
is `m + 2` copies. `layout.bin` records the LBA of every copy. A recovery tool
reads `layout.bin`, or scans for the `"NARH"` magic at sector alignment.

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NARH"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 16 | u8[16] | `disc_uuid` | The disc this run sits on. |
| 40 | 16 | u8[16] | `repo_uuid` | The repository. |
| 56 | 8 | u64 | `run_seq` | Monotonic run number in the repository. |
| 64 | 8 | u64 | `disc_seq` | Disc sequence number. |
| 72 | 8 | u64 | `lba_base` | First LBA of the run. |
| 80 | 8 | u64 | `run_sectors` | Length of the run in sectors. |
| 88 | 8 | u64 | `column_sectors` | `L`, the length of one column. |
| 96 | 2 | u16 | `fec_k` | Data columns. |
| 98 | 2 | u16 | `fec_m` | Parity columns. |
| 100 | 1 | u8 | `fec_scheme` | FEC scheme registry. |
| 101 | 1 | u8 | `checksum_column` | Column index of the checksum column. Equals `fec_k`. |
| 102 | 1 | u8 | `hash_algo` | Multicodec code of every id in this run. |
| 103 | 1 | u8 | `digest_len` | 32. |
| 104 | 1 | u8 | `chunker_profile` | Profile id. |
| 105 | 1 | u8 | `chunker_nc_level` | 2. |
| 106 | 1 | u8 | `compression` | Default compression id. |
| 107 | 1 | u8 | `fs_profile` | Disc filesystem profile id. |
| 108 | 4 | u32 | `chunk_min` | Bytes. |
| 112 | 4 | u32 | `chunk_avg` | Bytes. |
| 116 | 4 | u32 | `chunk_max` | Bytes. |
| 120 | 4 | u32 | `gear_table_id` | Gear table version. 1 in this specification. |
| 124 | 4 | u32 | `bundle_threshold` | Bytes. |
| 128 | 8 | u64 | `bundle_target` | Bytes. |
| 136 | 8 | u64 | `object_count` | Objects in this run. |
| 144 | 8 | u64 | `payload_bytes` | Stored object bytes in this run. |
| 152 | 8 | u64 | `duplicate_bytes` | Bytes written again for locality. |
| 160 | 8 | u64 | `manifest_lba` | LBA of the manifest container. |
| 168 | 8 | u64 | `manifest_sectors` | Length in sectors. |
| 176 | 32 | u8[32] | `manifest_hash` | Hash of the manifest bytes. |
| 208 | 8 | u64 | `filter_lba` | LBA of this run's filter. |
| 216 | 8 | u64 | `filter_sectors` | Length in sectors. |
| 224 | 32 | u8[32] | `filter_hash` | Hash of the filter bytes. |
| 256 | 8 | u64 | `layout_lba` | LBA of the layout table. |
| 264 | 8 | u64 | `layout_sectors` | Length in sectors. |
| 272 | 32 | u8[32] | `layout_hash` | Hash of the layout bytes. |
| 304 | 8 | u64 | `catalog_lba` | LBA of the catalog copy in this run. |
| 312 | 8 | u64 | `catalog_sectors` | Length in sectors. |
| 320 | 32 | u8[32] | `catalog_hash` | Hash of the catalog bytes. |
| 352 | 32 | u8[32] | `prev_run_header_hash` | Hash of the previous run header on this disc. Zero for the first run. |
| 384 | 8 | i64 | `created_sec` | Burn time, seconds. |
| 392 | 4 | u32 | `created_nsec` | Nanoseconds. |
| 396 | 4 | u32 | `source_run_count` | Number of run seqs this run references. |
| 400 | 8 | u64 | `snapshot_count` | Snapshots whose objects start in this run. |
| 408 | 8 | u64 | `prereq_count` | Prerequisite ids listed in the manifest. |
| 416 | 4 | u32 | `tool_version` | Writer version. |
| 420 | 1 | u8 | `status` | 1 open, 2 burned, 3 clean, 4 degraded. |
| 421 | 1 | u8 | `session_start_sector_valid` | 1 when `session_start_sector` is meaningful. |
| 422 | 2 | u16 | `reserved_u16` | Zero. |
| 424 | 8 | u64 | `session_start_sector` | Value passed to `isoinfo -T` for this run under profile 2. Zero under profile 1. |
| 432 | 8 | u64 | `prev_run_header_lba` | LBA of the previous run header on this disc. Zero for the first run. |
| 440 | 8 | u64 | `disc_object_count` | Objects on this disc after this run. Cumulative. |
| 448 | 8 | u64 | `disc_used_sectors` | Sectors used on this disc after this run. Cumulative. |
| 456 | 8 | u64 | `disc_run_index` | Index of this run on this disc. 0 for the first run. |
| 464 | 44 | u8[44] | `reserved` | Zero. |
| 508 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 507. |

The set of referenced run seqs is stored in the manifest container, not in the
header, because its length varies. `source_run_count` bounds it, and section 15
bounds `source_run_count` by the capping knobs.

#### 9.6.1 The run chain

Every run header points to the previous run header on the same disc, by hash and
by LBA. The chain is the mutable state of the disc: the run table, the object
count, the used sectors, the health, and the close state all come from it.

A reader finds the newest run by listing the directory `/NOAHSARK/runs/` and
taking the highest `<seq>`. That is the only normal path. There is no scanning
and no fixed LBA.

Each run header also names the previous run header by hash and by LBA, so a
reader can walk the chain backwards and confirm that no run is missing.

The recovery path, for a damaged filesystem directory, is in section 9.7.1. It
is Phase 3.

Every run carries **its own** copy of the catalog. An earlier copy is never
modified. A reader takes the catalog of the newest run it can find.

#### 9.6.2 Run header copies

The run header exists `m + 2` times inside its run, in three kinds of place:
`RUN.bin`, the first sector of every parity file, and `RUN2.bin`. Every copy is
an ordinary file or the first sector of one. Section 11.5 states the rule and the
radial spread that it gives.

#### 9.6.3 What is overwritten

NoahsArk's own structures are append-only. The only blocks that an append
overwrites are the filesystem's own metadata:

| Profile | Overwritten per append |
|---|---|
| 1, UDF 2.01 | The File Entry of each directory on the path to a new object, the Logical Volume Integrity Descriptor, the space bitmap, and the anchor at 256 when the volume size changed. |
| 2, ISO 9660 level 4 | The whole directory tree and both path tables, rewritten into the new session. |

Block counts per append under profile 1, for one new object in each of `d`
distinct directories:

| Fan-out | Directories on the path | Directory blocks overwritten | LVID and bitmap | Total per append |
|---|---:|---:|---:|---:|
| One level, `/NOAHSARK/objects/ab/<name>` | 2 per object (`objects`, `objects/ab`) | 1 + min(d, 256) | 2 to 4 | `3 + min(d,256) + 2` blocks, at most **263 blocks (526 KiB)** |
| Two levels, `/NOAHSARK/objects/ab/cd/<name>` | 3 per object | 1 + 256 + min(d, 65536) | 2 to 4 | at most **65,797 blocks (128.5 MiB)** in the worst case, and `3 + d + 3` in the common case where the new objects touch few leaf directories |

The one-level worst case is bounded by the 256 first-level directories. The
two-level worst case is bounded by 65,536 leaf directories, but it is reached
only when an append touches every leaf, which a path-ordered pack never does.

Under profile 2 the overwrite is the whole tree: about 19 MiB at 10,000 objects
and about 107 MiB at 100,000 objects (section 10.2.3).

**Consequence.** Spare area exhaustion never affects NoahsArk's own readability.
It stops filesystem directory updates only. The run chain, the layout tables and
the objects are all sequential writes past the next writable address, which need
no spare block. That is why raw append works.

### 9.7 Run layout table

The layout table records the LBA extents of every object in the run. It is what
turns "sector 4,712,003 is bad" into "object `<id>` is degraded". It is also
what makes a run readable with no filesystem.

Container header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NALY"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 8 | u64 | `run_seq` | The run described. |
| 32 | 8 | u64 | `record_count` | Number of extent records. |
| 40 | 2 | u16 | `record_size` | 64. |
| 42 | 1 | u8 | `hash_algo` | Multicodec code. |
| 43 | 1 | u8 | `digest_len` | 32. |
| 44 | 4 | u32 | `reserved_u32` | Zero. |
| 48 | 8 | u64 | `payload_len` | Total container length, for validation. |
| 56 | 4 | u32 | `body_crc32c` | CRC-32C over the records. |
| 60 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 59. |
| 64 | | | `records` | Sorted by `start_lba` ascending. |

Extent record, 64 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | Object id, or bundle id for a bundle extent. |
| 32 | 8 | u64 | `start_lba` | Absolute LBA of the first sector. |
| 40 | 8 | u64 | `byte_len` | Bytes of the object header plus stored payload. |
| 48 | 4 | u32 | `sector_count` | Sectors this extent covers. |
| 52 | 4 | u32 | `byte_off` | Byte offset inside the first sector. |
| 56 | 2 | u16 | `extent_index` | 0 for the first extent of an object. |
| 58 | 2 | u16 | `flags` | bit0 last extent, bit1 duplicate for locality, bit2 metadata object. |
| 60 | 1 | u8 | `kind` | Object kind registry. |
| 61 | 1 | u8 | `compression` | Compression id. |
| 62 | 2 | u16 | `reserved_u16` | Zero. |

A fragmented file produces several records with the same `content_id` and
increasing `extent_index`. The record with `flags` bit 0 set is the last one.

The table covers **every file** of the run, not only the objects: `RUN.bin`,
`layout.bin`, `manifest.bin`, `filter.bin`, the catalog copies, the objects, the
parity files and `RUN2.bin`. Files appear in copy order, which is LBA order.

The container header additionally records the parity geometry, so that the
layout table alone is enough to run a repair:

| Field | Meaning |
|---|---|
| `shard_bytes` | 2048. |
| `fec_k`, `fec_m` | Column counts. |
| `parity_base_lba` | First LBA of the parity domain. |
| `parity_end_lba` | Last LBA of the parity domain, inclusive. |

The writer builds the table by reading the LBA of every file back from the
finished image. The method is profile dependent and is stated in section 10.

#### 9.7.1 Raw-LBA reading (Phase 3)

When the filesystem directory is unreadable, a recovery tool works as follows:

1. Scan the first 64 MiB of the disc for the `"NADS"` magic and verify
   `super_crc32c`. That finds `/NOAHSARK/DISC.bin`, which the writer always
   places at the start of the first run.
2. Read the superblock, then read `first_run_lba` to find the first `RUN.bin`.
3. Read the run header, then read `layout.bin` at `layout_lba`.
4. Read every file by its extents. Copy order equals LBA order, so the files
   that were copied first sit first.
5. Follow the run chain forward: each run's `RUN2.bin` is the last file of that
   run, and the next run's `RUN.bin` follows it.

This path needs no filesystem code at all. It is Phase 3, because Phase 1 and
Phase 2 discs are read through the filesystem.

### 9.8 Append model (profile 1 and profile 2 only)

Both appendable profiles use the same medium mechanism: **Pseudo-OverWrite
growth on a formatted BD-R**. Only the filesystem step differs.

The medium mechanism, from the growisofs source:

- `bd_r_format()` formats any blank BD-R unless `spare:none` is passed, and it
  forces the Format Subtype to SRM+POW.
- `poor_man_rewritable()` classes `profile == 0x41 && bdr_plus_pow` as
  rewritable, next to DVD+RW and BD-RE.
- `plusminus_r_C_parm()` then takes `next_session` from the track's Next
  Writable Address and forces `prev_session = 0`.
- The man page agrees: "volumes are grown within a single session" on Blu-ray.

The consequence is measurable: `dvd+rw-mediainfo` always reports
`Number of Sessions: 1`. The macOS "only the first session" limitation therefore
never engages.

| Step | Profile 1, variant 1a | Profile 1, variant 1b | Profile 2 |
|---|---|---|---|
| Format | `spare:min` on the first write | `spare:min` on the first write | `spare:min` on the first write |
| Build | `mkudffs`, then a mount of the disc itself | `mkudffs`, then a loop mount of an image mirror | `genisoimage` inside growisofs |
| Append | Mount `/dev/sr0` read-write with the kernel udf driver, copy files in fill order (section 10.5) | Copy into the mirror, diff 32 KiB blocks, write each changed run with `growisofs -use-the-force-luke=seek:N,spare:min -Z` | `growisofs -M`, one command |
| Burner used for the append | None. The kernel writes. | growisofs, one call per changed run | growisofs, one call |
| New code needed | None | Image mirror and block diff | None |
| Per-append cost | About one block per new object | About one block per new object | Rewrites the whole directory tree |
| LBA read-back | Parse UDF File Entries | Parse UDF File Entries | `isoinfo -l -T <session>` |
| Risk | The `sr` block device may refuse writes. Manual probe required. | Proven mechanism, more moving parts | Windows long-name support unverified |

Both variants of profile 1 produce the same disc. A manual hardware probe
selects one (section 23.6). The superblock records the choice in
`append_variant`.

After every append, under either profile, the tool must read back the LBA
extents of every object and compare them with the recorded layout. POW implies
drive defect management, so a block may move. The tool must fail the append if
any object moved.

### 9.9 Closing a disc

**A disc is never closed by default.** The config key is `disc.close_policy`.

| Value | Meaning |
|---|---|
| `never` | **Default.** No command closes the disc unless the user runs `close` explicitly. |
| `always` | A disc is sealed at its first and only burn: `spare:none`, `-dvd-compat`, no POW. |
| `when_full` | Profile 1 and 2 only. The run that fills the disc past the fill limit also closes it. |
| `manual` | Same as `never`. The name exists so that a user can state the intent in the config. |

The `noahsark close` command always exists. Under `never` it is never automatic.

**Profile 0 under the default policy.** The disc is formatted for POW with
`spare:min`, one large run is written up to the data budget, `-dvd-compat` is
**not** passed, and the disc is left open. The disc can therefore receive a
profile 1 append later, once Phase 2 exists: a repair run, extra parity, or the
leftover space. No format change is needed at that point.

**Sealing a disc.** `pack --close`, or `disc.close_policy = always`, selects
`spare:none` and `-dvd-compat` instead. There is then no format step at all, no
spare area, no defect management, full capacity, and permanently stable LBAs.
The disc can never be appended. The choice is permanent and is recorded in the
superblock, in `state_flags` of the disc directory and in `capacity_is_forced`
accounting, so a later tool never tries to append to it.

Section 10.3 gives the full comparison of the two paths, with the command
lines.

#### 9.9.1 Tail anchors

`mkudffs` places UDF anchors at LBA 256, at `N - 256` and at `N` in the
full-size image. The first run therefore writes:

1. the whole used prefix of the image, and
2. the last 512 sectors of the image,

so that the tail anchors exist even on an open disc. A disc with only the
LBA 256 anchor still mounts, because that anchor is mandatory in the standard,
but three anchors are what every reader expects on a premastered disc.

Whether `growisofs -use-the-force-luke=seek:` accepts a write to the tail region
of a POW disc before the middle is written is **a probe**, not a fact. Section
23.6 defines it. If the drive refuses, the tail anchors wait for the first
append or for `close`, and the disc mounts on the LBA 256 anchor in the
meantime.

#### 9.9.2 What close does

**The format must not depend on a closed disc.** Every reader path works on an
open disc:

- A NoahsArk reader finds the superblock, then the run table, then the run
  headers and the LBA extents. It never needs a closed volume.
- A UDF reader uses the anchor at LBA 256, which the UDF standard makes
  mandatory. That anchor is written by `mkudffs` in the first run.
- An ISO 9660 reader uses the Primary Volume Descriptor at block 16, which is
  written by the first run.

Benefits of leaving a disc open:

1. A repair run can go onto the same disc later. A degraded run is then healed in
   place, on the medium that already holds most of the data.
2. An extra parity run can be added later, when the health report shows a
   shrinking RS margin.
3. No capacity is wasted. A disc that is 60 percent full stays available.

When the user does close a disc, the writer must:

1. Confirm that the superblock is already present. It is never rewritten.
2. Write a final catalog copy inside the closing run.
3. Write the tail anchors if they are not present.
4. Optionally add a disc-wide parity run over all data columns of all runs. The
   config key is `fec.disc_close_parity`. The default is false.
5. Write the closing run with `-dvd-compat`.
6. Verify on a second drive within 24 hours.

Two manual probes cover the open-disc case: reading an open POW BD-R on Windows
and on macOS, and drive behaviour when a reader reads past the last written
block. Section 23.6 defines them.

### 9.10 Fallbacks

| Condition | Fallback |
|---|---|
| The drive offers no POW feature (`GET CONFIGURATION` feature 0x38 absent). | Use profile 0: `spare:none`, one run, `-dvd-compat`. |
| `dvd+rw-mediainfo` reports `BD-R SRM` after a format attempt. | Same. POW is not available on this drive and medium. |
| The kernel refuses to mount `/dev/sr0` read-write on a POW BD-R (probe 1, section 23.6). | Use profile 1 variant 1b, the image mirror and the block diff. |
| The UDF append path is unavailable, or a user wants genisoimage-managed appends. | Use profile 2, after the Windows name check of probe 2 passes. |
| Windows truncates ISO 9660:1999 long names on real media (probe 2, section 23.6). | Profile 2 must not be used. Stay on profile 1. |
| growisofs fails, or the build is unpatched. | Use the `cdrskin` burner backend, and therefore profile 0. |
| An object moved after an append. | Abort the append. Keep the objects PACKED. Mark the run for re-burn. Report the moved ids. |
| The image mirror is lost (variant 1b). | Rebuild it with `ddrescue` from the disc. |

### 9.11 Spare area exhaustion and raw append (profile 1 only)

Pseudo-OverWrite works through the drive's spare area. Every logical overwrite
of an already written block consumes a spare block. The spare area is finite.

The failure mode is asymmetric, and this asymmetry is what makes the design
survive it:

- An **overwrite** of an already written block fails when the spare area is
  exhausted.
- A **write past the next writable address** still works, because that is an
  ordinary sequential write and needs no spare block.

Three measures follow.

**Measure 1: write each metadata block once per append.** The block diff of
variant 1b must produce one write per changed 32 KiB block, never several. A
writer that patched a directory block twice in one append would consume two
spare blocks for one logical change. The `disc.spare` config key selects the
spare area size at format time: `min` uses the maximum-capacity descriptor and
therefore the smallest spare area, and `default` uses the drive's default
descriptor, which reserves about 256 MB. `min` gives more payload. `default`
gives more appends. The value is chosen once, at format time, and cannot be
changed afterwards.

Profile 2 consumes spare much faster than profile 1, because `growisofs -M`
rewrites the whole directory tree and the path tables on every append. At
100,000 objects that is about 107 MiB of overwrites per append. Profile 1
rewrites about one block per new object. A user who expects many appends should
therefore prefer profile 1 or `disc.spare = default`.

**Measure 2: watch the spare area.** The health report reads the spare usage
from `dvd+rw-mediainfo`, or from `READ DISC STRUCTURE` when the drive exposes
the defect list, and reports the remaining fraction. Below
`disc.min_spare_ratio`, default 0.20, the tool warns and recommends no further
ordinary appends to that disc.

**Measure 3: raw append, a degraded mode.** When the spare area is exhausted, or
when an overwrite fails with a write error that the drive attributes to spare
exhaustion, the tool may still write new runs.

In raw append mode:

1. A new run is written past the next writable address, as a sequential write.
2. The filesystem directory is **not** updated. No overwrite happens, so no
   spare block is needed.
3. The run header, the manifest, the filter, and the layout table go inside the
   new run, exactly as usual. The LBA extents in the run header therefore make
   every object readable.
4. The disc directory marks the disc `append-raw-only`. A reader finds the raw
   runs through the run header chain. A raw append writes its `RUN.bin`
   immediately after the previous run, so the previous header names the next
   LBA. When that chain is lost too, the recovery scan of section 9.7.1 finds
   the raw runs.
5. Restore is unaffected. NoahsArk reads by LBA extents, not by filesystem path.
6. Other operating systems do **not** see the raw runs. The volume still mounts
   and still shows every object of the earlier runs. A user who mounts the disc
   sees an incomplete tree, and the health report says so.

Raw append is a degraded mode. The tool must warn every time it uses it, must
record the state in the disc directory, and must recommend a fresh disc.

### 9.12 Forced capacity

A user may cap the usable capacity of one disc below what the drive reports. The
CLI option is `pack --capacity <bytes|GiB>`. The config key is
`disc.force_capacity`.

The reason is a real failure mode: some multi-layer discs fail to burn beyond
the first layer. A user must be able to say "this 100 GB disc is used as 30 GB"
and keep the disc in service.

Rules:

1. The forced value is recorded in the superblock as `capacity_forced_sectors`,
   next to the reported `capacity_sectors`, and in the disc directory
   (section 12.6). Later appends and restore plans then use the same limit.
2. The forced value must be less than or equal to the reported capacity. A
   larger value is a hard error.
3. The forced value applies from the first write of the disc. It must not change
   afterwards. A later `pack` with a different `--capacity` for the same disc is
   a hard error.
4. Everything that consumes capacity uses the forced value. That is four
   consumers:
   - the packer, when it decides how much fits;
   - the image size under profile 1. That is the length passed to `truncate`
     before `mkudffs`;
   - the FEC layout. `L = floor(run_sectors / 255)`, and the run range must lie
     inside the forced capacity;
   - the fill ratio. `fill_limit_sectors = floor(capacity_forced_sectors *
     disc.fill_ratio)` minus the spare reserve.
5. `noahsark disc list` shows both the reported and the forced capacity.
6. The health report flags any disc whose forced capacity is below the reported
   capacity, so that the operator can see how much medium is deliberately
   unused.

### 9.13 Why true UDF multi-session is not possible with current tools

Profile 1 grows one volume in place instead of adding a UDF session, and
variant 1b needs an image mirror and a block diff. A reader will ask why. The
answer is that no tool can add a UDF session to write-once media. Three
independent blocks exist. Each was verified against current source.

**Block 1: growisofs cannot merge a UDF session.** The `-M` option reads block
16 of the existing volume and demands an ISO 9660 Primary Volume Descriptor:

```c
if (memcmp (saved_descriptors[0].type,"\1CD001",6))
    fprintf (stderr,":-( %s doesn't look like isofs...\n", in_device),
    exit(FATAL_START(EMEDIUMTYPE));
```

A pure UDF volume has no Primary Volume Descriptor at block 16, so `-M` exits.
The check runs twice, before the burn and after it. Past the check, `-M` does
not merge anything itself: it appends `-C` and `-M` to the genisoimage argument
vector. growisofs contains no filesystem writer of any kind.

The Debian packaging repository at salsa was checked at master `0d0cb25`
(2021-11-28). No patch in `debian/patches` touches `CD001`. No patch mentions
UDF. The changelog top entry is `7.1-15 UNRELEASED` (2019-11-04) and holds only
packaging housekeeping. There is no fork and no newer release. Upstream
dvd+rw-tools 7.1 was released on 2008-03-05 and is the last upstream release.

This same gate is what makes profile 2 work: an ISO 9660 volume passes it.

**Block 2: mkudffs cannot build a session that references an earlier session.**
`mkudffs --startblock` positions a new, empty filesystem at an offset. It does
not merge one. Appendix D holds the test and the source check.

**Block 3: the kernel cannot write a VAT volume.** A Virtual Allocation Table
is the UDF mechanism for write-once append. The Linux kernel forces read-only on
every write-once volume, so a VAT volume can never be populated on Linux.
Appendix D holds the kernel code and the mount test.

**xorriso is not a way out.** Its filesystem writer contains no UDF code.
Appendix D holds the source check.

**Conclusion.** No open source tool bridges "writes bytes" and "writes UDF".
mkudffs writes empty UDF volumes. The kernel writes UDF on media that accept
random sector writes. Every burner writes bytes.

Profile 1 uses the one path that remains: a POW-formatted BD-R **is** a medium
that accepts random sector writes, so the kernel udf driver can maintain the
volume. Variant 1a lets the kernel write the disc directly. Variant 1b keeps a
local mirror and pushes the changed blocks through growisofs. Neither adds a
session, so the disc keeps `Number of Sessions: 1`.

Profile 2 avoids UDF entirely and uses the ISO 9660 merge that growisofs already
supports.

---


---

## 10. Disc filesystems and burning

Section 10.3 specifies profile 0, which is the default and the whole Phase 1
disc model. Section 10.1 specifies profile 1, which is Phase 2. Section 10.2
specifies profile 2, which is Phase 3. Sections 10.4 to 10.6 are common to every
profile. Sections 10.7 to 10.14 are about the burn plan, the burner, and
verification.

A reader who only implements Phase 1 needs sections 10.1.1 to 10.1.4 for the
filesystem, section 10.3 for the burn, and sections 10.4 onward. The append
subsections 10.1.5 to 10.1.10 are Phase 2.

### 10.1 Profile 1, `udf201-pow` (Phase 2)

#### 10.1.1 Revision

The filesystem is pure UDF at revision **2.01**, block size 2048. There is no
ISO 9660 bridge, no Joliet, and no Rock Ridge. A bridge gives two independently
built trees that can disagree, and only one of them gets verified.

| OS | Reads up to | Writes up to | Note |
|---|---|---|---|
| Linux 2.6.26 and newer | 2.60 | 2.01 | The kernel reads 2.60 structures and writes 2.01. |
| Linux 2.4.6 to 2.6.25 | 2.01 | 2.01 | |
| Windows 2000 | 1.50 | none | Below the target set. |
| Windows XP | 2.01 | none | The oldest Windows still seen in the field. |
| Windows Vista to 11 | 2.60 | 2.50 | VAT and Sparing Table supported. |
| Mac OS X 10.4 | 2.01 | 2.01 | Mounts the plain build only. |
| macOS 10.5 to 15 | 2.60 | 2.50 | |
| NetBSD 5.0 and newer | 2.60 | 2.60 | Not a target. |
| OpenBSD 4.7 and newer | 2.60 | - | Not a target. |
| Solaris 7 and newer | 1.50 | 1.50 | Not a target. |
| AIX 5.2 and newer | 2.01 | 2.01 | Not a target. |
| FreeBSD | 1.50 | none | **Not supported.** Its kernel ceiling is 1.50. |
| Android | none | none | No UDF in AOSP. |

Reasons for 2.01:

1. It is the ceiling of Windows XP.
2. Linux reads and writes it, so the build side and the repair side stay
   symmetric.
3. `mkudffs` cannot build a UDF 2.50 Metadata Partition. Its manual states that
   it does not support revisions above 2.01 for non write-once media.

The revision is a config knob, `udf.revision`. The superblock records it in
`fs_revision`. Revision 2.50 or 2.60 may be adopted when tooling supports it and
cross-OS reads are verified.

Blu-ray *video* uses UDF 2.50. That is a video-application rule. It is not a
data-disc rule.

#### 10.1.2 Image build

```bash
set -eu
DEV=/dev/sr0
IMG=/var/tmp/noahsark.udf

eval "$(growisofs -F "$DEV")"          # sets next_session= and capacity=
BLOCKS=$(( capacity / 2048 ))
# A forced capacity caps the image. See section 9.12.
# The image is built at the forced capacity. The packer, not the image, stops
# at the data budget of section 10.11.
if [ -n "${FORCED_SECTORS:-}" ]; then BLOCKS=$(( FORCED_SECTORS )); fi
BLOCKS=$(( BLOCKS - BLOCKS % 16 ))     # 32 KiB alignment

rm -f "$IMG"; truncate -s $(( BLOCKS * 2048 )) "$IMG"

mkudffs --utf8 --media-type=hd --blocksize=2048 --udfrev=2.01 \
        --label=NOAHSARK-0001 --uid=0 --gid=0 --mode=0555 \
        --bootarea=erase "$IMG"
```

Rules that the command line encodes:

- `--media-type=hd`, never `bdr` and never `dvdr`. A `bdr` build makes a
  write-once VAT volume. That image does not mount at all until it is truncated
  to `(vatblock + 1) * 2048`, and even then it mounts read-only, so it can never
  be populated.
- `--blocksize=2048` always. With no option, `mkudffs` detects blocksize 512 on
  a plain file, and a 512-byte-block image is unreadable in an optical drive.
- `--utf8` first. `mkudffs` is order-sensitive. The encoding option must come
  first and every override must come after `--media-type`.
- Never `--spartable`. A sparing table adds a second logical-to-physical
  indirection that breaks the parity map, and Reed-Solomon already covers the
  failure that a sparing table addresses.

`udfinfo` on each media type, measured:

| `--media-type` | udfrev | accesstype | integrity | Anchors | Mounts read-write |
|---|---|---|---|---|---|
| `hd` | 2.01 | overwritable | closed | 256, N-256, N | **yes** |
| `dvdram` | 2.01 | overwritable | closed | 256, N-256, N | yes |
| `dvd` | 2.01 | readonly | closed | 256, N-256, N | no |
| `dvdr` | 2.01 | writeonce (VAT) | opened | 256 only | **no, mount fails** |
| `bdr` | 2.50 | writeonce (VAT) | opened | 256 only | **no, mount fails** |

Three anchors are what every reader expects on a premastered disc. Only `hd`
gives them.

#### 10.1.3 Limits

| Item | Limit | Evidence |
|---|---|---|
| Name field | 255 bytes; 254 bytes of name data after the CS0 compression id | UDF spec |
| Name in 8-bit CS0 | 254 characters | OSTA |
| Name in 16-bit CS0 | 127 characters | derived |
| Linux kernel limit | 254 UTF-8 bytes (`UDF_NAME_LEN`) | Measured: ASCII 254 passed, 255 failed; CJK 84 passed, 85 failed |
| Path length | 1023 bytes in the spec, not enforced by Linux | Measured: 1409 bytes worked |
| Directory depth | No limit | Measured: 300 levels |
| Maximum file size | 16 EiB | UDF spec |
| Files per directory | Bounded only by the directory file size | UDF spec |
| Reserved characters | NUL and `/` only | UDF spec |
| Case | Case-sensitive on disc; Windows and macOS present it case-insensitively | - |

UDF is the only filesystem in this specification that gives lossless Unicode
names, unlimited depth, and POSIX metadata on Linux, Windows and macOS at the
same time. That is why it is the default, and it is the only correct choice if
user file names are ever written to the disc directly.

#### 10.1.4 Fan-out

Section 5.5 states the fan-out rule and its default. Profile 1 also allows a
second level, because a UDF append rewrites only the directories that changed.

#### 10.1.5 Append, variant 1a: kernel direct write

A POW-formatted BD-R accepts random sector writes. The kernel udf driver can
therefore maintain the volume on the disc itself. The kernel documentation
states the condition: "dvd+rw drives and media support true random sector
writes, and so a udf filesystem on such devices can be directly mounted
read/write."

Steps:

1. Format the blank BD-R for POW with the first write (`spare:min`).
2. Mount the disc read-write:
   `mount -t udf -o rw /dev/sr0 /mnt/ark`.
3. Copy the new objects in fill order, one file at a time, single-threaded.
4. Unmount. The kernel flushes the directory blocks, the Logical Volume
   Integrity Descriptor, the space bitmap, and the anchors.
5. Eject, reload, and read back the LBA extents of every object.

Variant 1a uses no burner at all for the append. The first write still goes
through the burner, because the disc must be formatted and the initial volume
must be laid down.

**This variant is unproven on real hardware.** The kernel has no POW awareness,
and the `sr` block device may refuse writes. Section 23.6 defines probe 1, which
decides whether variant 1a is available. Until probe 1 passes on a given drive
model, the implementation must use variant 1b.

#### 10.1.6 Append, variant 1b: image mirror and block diff

1. **Keep an image mirror.** A sparse file of exactly the disc capacity lives at
   `staging/images/<disc_uuid>.img`. It is derived data and can be rebuilt from
   the disc with `ddrescue`.
2. **Edit the mirror.** Loop-mount it read-write, copy the new objects in fill
   order, one file at a time, and unmount.
3. **Diff.** Compare the mirror against the previous recorded image state.
   Compute the set of changed 32 KiB blocks.
4. **Write.** Write each changed, 32 KiB-aligned byte run with
   `growisofs -use-the-force-luke=seek:N,spare:min,tty -Z /dev/sr0=run.bin`,
   where `N` is a multiple of 16.
5. **Read back.** Compare the LBA extents of every object with the layout table.

```
   image mirror (sparse, = disc capacity)      physical disc
   +--------------------------------+          +--------------------------+
   | previous state (hashed blocks) |          | already burned           |
   +--------------------------------+          +--------------------------+
              | loop mount rw, copy new objects in fill order
              v
   +--------------------------------+
   | new state                      |
   +--------------------------------+
              | block diff, 32 KiB granularity
              v
   changed runs:  [ LBA 51200, 96 MiB ]  new object data
                  [ LBA   256,  32 KiB ] anchor and LVID
                  [ LBA  1024,  64 KiB ] changed directory File Entries
              | growisofs seek:N -Z, one call per run
              v
   +--------------------------------+
   | disc after append              |
   +--------------------------------+
              | read back LBA extents, compare
              v
   pass -> mark BURNED     fail -> abort, keep objects PACKED
```

Both variants produce the same disc bytes. A reader cannot tell them apart, and
does not need to.

#### 10.1.7 Per-append cost

Only the changed blocks are written:

| Item | Blocks per append |
|---|---|
| New object data | The payload, contiguous from the previous next writable address |
| File Entry per new object | 1 each |
| Directory File Entries on the path to each new object | 1 each, rewritten |
| Logical Volume Integrity Descriptor and space bitmap | 2 to 4 |
| The anchor at 256 | 1, only when the volume size changed |

For a depth-3 tree that is roughly **one block per new object plus a handful of
directory blocks per append**. The cost does not grow with the total object
count. That is the decisive advantage over profile 2.

The number of appends is effectively unbounded. The limit is the spare area that
defect management consumes and the size of the drive's defect list, not the
filesystem. Budget 512 MiB of spare and metadata reserve per disc.

#### 10.1.8 Placement order

The writer copies files into the mount **one at a time**, single-threaded, in
fill order. Copy order equals physical LBA order. This was measured. Five
1,000,000-byte files copied in order landed at LBA 271, 761, 1251, 1741 and
2231. The stride is a fixed 490 blocks. A 1,000,000-byte file needs 489 data
blocks, and the File Entry takes the 490th.

The writer must never use `cp -r` on a directory. The order would then follow
`readdir`, not the fill order.

#### 10.1.9 LBA read-back

The primary method parses the UDF File Entry. It is the only exact method. It
works on an unmounted image, needs no root, and gives every extent of a
fragmented file.

- Allocation descriptors hold partition-relative block numbers.
- Add the Partition Descriptor start, which `udfinfo` prints as
  `start=... type=PSPACE`.
- In the measured image PSPACE started at 257 and the first object sat at
  absolute LBA 271, that is partition-relative 14.
- In Go, `github.com/mogaika/udf` parses File Entries and ICBs. The library is
  read-only, which is exactly enough.

`filefrag -e -v` is a cross-check only. It needs root, and on the tested kernel
it reported the extent count and the block count correctly but printed an empty
extent table. `udfinfo` gives volume-level layout only, never a per-file LBA.

#### 10.1.10 Known reader issues

- Linux sets `iocharset=utf8` by default. `/proc/mounts` shows
  `udf ro,relatime,iocharset=utf8` with no option given. On kernels older than
  5.4, pass `utf8`, not `iocharset=utf8`.
- `mount -o session=` defaults to the last session. Profile 1 has one session,
  so the option never matters.
- udisks2 and KDE mishandle multi-session BD-R auto-mount. Profile 1 has one
  session, so the problem does not arise. A recovery procedure must still use an
  explicit `mount -t udf -o ro /dev/sr0`, never desktop auto-mount.
- The kernel mounts any write-once (VAT) UDF volume read-only. Profile 1 never
  builds one.

### 10.2 Profile 2, `iso9660v1-l4-pow` (Phase 3)

Profile 2 exists for two cases:

1. The UDF append path is unavailable on the host.
2. A user wants genisoimage to manage the appends, with no NoahsArk-side
   filesystem work at all.

Profile 2 must not be used until probe 2 of section 23.6 passes.

#### 10.2.1 Premise

On-disc names belong to NoahsArk. They are 68-character lowercase hex from the
charset `[0-9a-f]`. Long user names, deep user paths, and every POSIX metadata
field live **inside tree objects**, not on the disc. The disc filesystem
therefore needs no Unicode, no POSIX metadata, and no long names.

Rock Ridge and Joliet are forbidden. The premise makes that acceptable.

#### 10.2.2 Standard, level, and limits

The filesystem is ISO 9660:1999, also called level 4, plain. Measured limits:

| Item | Value | Evidence |
|---|---|---|
| File name length | Up to 207 bytes in one directory record | ISO 9660:1999 |
| Case | Lowercase is legal at level 4 | Measured: the raw on-disc bytes hold the 68-character lowercase name |
| Version suffix | None. Level 4 omits `;1` | Measured |
| Rock Ridge bytes | Zero | Measured: `Total rockridge attributes bytes: 0` |
| Directory depth | Unlimited with `-D` | Level 4 permits it. NoahsArk uses depth 3. |
| Path length | 1023 bytes in the path table | ISO 9660 |
| File size | 4 GiB - 1 per extent | Objects stay under 2 GiB, so this never binds. |
| Unicode | None. Raw bytes with no declared encoding. | Measured: Windows and macOS mis-decode non-ASCII. |

Levels 1, 2 and 3 are **forbidden**. They uppercase the name, truncate it to 30
characters, and append `.;1`. The measured on-disc bytes at level 1, 2 and 3
were `83F682CFA8CCFBB7641FB8AAAE3F73.;1`; at level 4 they were the full
68-character lowercase name.

A Linux mount hides this. The kernel `iso9660` driver lowercases names when Rock
Ridge is absent, so a level-1 image *displays* lowercase 30-character names
while the disc itself holds uppercase. Only a raw-byte check tells the truth.
Windows and macOS show the uppercase `NAME.;1` form. Any test of the naming must
therefore inspect the raw image bytes, not a mount.

#### 10.2.3 Fan-out

Profile 2 uses **one** hex fan-out level only: `/NOAHSARK/objects/<ab>/<name>`,
256 directories.

The reason is the append cost. `growisofs -M` hands the merge to genisoimage,
which rewrites the entire directory tree and the path tables into the new
session. Measured, with 1-byte files so that the figure is pure metadata:

| Objects on disc | Directory bytes rewritten per append | Path table bytes | Cost per append |
|---:|---:|---:|---:|
| 2,000 | 8,650,752 | 42,250 | ~8 MiB |
| 10,000 | 19,589,120 | 95,620 | ~19 MiB |
| 100,000 | 107,282,432 | 516,130 | ~107 MiB |

The cost is driven by the fan-out, not by the object count. Two hex levels
create up to 65,536 directories, and each directory occupies at least one
2048-byte sector, so 65,536 x 2048 = 134,217,728 bytes, that is 128 MiB. One level cuts
this by about two orders of magnitude.

At 100,000 objects and one fan-out level, ten appends cost roughly 1 percent of
a 25 GB disc. Under profile 2 the packer must include the projected append cost
in its capacity budget (section 15.6).

#### 10.2.4 Build and burn command lines

genisoimage runs inside growisofs. NoahsArk never calls genisoimage directly.

| Option | Effect |
|---|---|
| `-iso-level 4` | ISO 9660:1999. Long lowercase names, no `;1`, no depth cap. |
| `-D` | No deep relocation. Legal at level 4. |
| `-l` | Allow 31-character ISO names. A no-op at level 4, kept as a guard. |
| `-allow-limited-size` | Guard for a file above 4 GiB. NoahsArk never writes one. |
| `-no-limit-pathtables` | Lifts the path-table entry limit. Needed near 100,000 objects, where the path table reaches about 516 KB. |
| `-sort <file>` | LBA placement order. |
| `-V <label>` | Volume label. |

Forbidden options: `-R`, `-r`, `-J`, `-joliet-long`, `-udf`, and every level
below 4.

```bash
# First write on a blank BD-R. spare:min formats for POW.
growisofs -speed=4 -use-the-force-luke=spare:min,tty \
          -Z /dev/sr0 -iso-level 4 -D -l -allow-limited-size \
          -no-limit-pathtables -sort sortfile -V ARK-0001 /srv/ark/ob

# Every later append. -M grows the same session.
growisofs -speed=4 -use-the-force-luke=spare:min,tty \
          -M /dev/sr0 -iso-level 4 -D -l -allow-limited-size \
          -no-limit-pathtables -sort sortfile -V ARK-0001 /srv/ark/ob

# Final append: close the disc.
growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:min,tty \
          -M /dev/sr0 -iso-level 4 -D -l -allow-limited-size \
          -no-limit-pathtables -sort sortfile -V ARK-0001 /srv/ark/ob
```

M-DISC uses `-speed=2`. Every other flag is identical.

growisofs passes every unrecognised option straight through to genisoimage, so
`-sort` and the level options reach the builder unchanged.

#### 10.2.5 Placement order

`genisoimage -sort <file>` sets the LBA placement order. The file holds
`<path> <weight>` pairs, one per line. A higher weight is placed closer to the
start of the medium.

The writer emits one line per object, with strictly decreasing weights in the
fill order of section 10.5. The sort file for 100,000 objects is a large text
file and costs nothing at runtime. Unlike the copy-order trick of profile 1,
`-sort` is explicit and order-independent.

#### 10.2.6 LBA read-back

`isoinfo -l` prints the extent of every file. It needs no root and no mount.

```bash
isoinfo -i disc.iso -T "$SESSION_START" -l \
  | sed -n 's/.*\[ *\([0-9]*\) *[0-9]*\] *\([0-9a-f]\{68\}\).*/\2 \1/p'
```

`-T <sector>` selects the session to read. That is exactly what re-reading the
map after an append needs. The run header records the value in
`session_start_sector`.

This is simpler than the profile 1 method, which must parse UDF File Entries.

#### 10.2.7 Measured append test

The design was tested end to end on a tree of 68-character lowercase hex names,
with 2,000 objects in batch 1 and 500 in batch 2.

```
$ genisoimage -iso-level 4 -D -l -allow-limited-size -V ARK1 -o a.iso b1/ob
19138 extents written (37 MB)

$ NEXT=19152                       # 19138 rounded up to a multiple of 16
$ genisoimage -iso-level 4 -D -l -allow-limited-size -V ARK1 \
              -C 0,$NEXT -M a.iso -o add.iso b2/ob
38753 extents written (75 MB)

$ cp a.iso disc.iso; truncate -s $((NEXT*2048)) disc.iso; cat add.iso >> disc.iso
$ sudo mount -t iso9660 -o loop,ro,sbsector=$NEXT disc.iso /mnt/iso
$ find /mnt/iso -type f | wc -l
2500

$ join lba_before.txt lba_after.txt | awk '$2!=$3{c++} END{print "CHANGED LBAs: " c+0}'
CHANGED LBAs: 0
```

All 2,500 objects were visible after the append. All 2,000 batch-1 objects kept
their exact LBA. All 500 batch-2 objects landed at or above the next session
start. Mounting the first session instead showed exactly 2,000 files.

The mount needed `-t iso9660` explicitly. Autodetection tried `udf` first and
failed with `Unknown parameter 'sbsector'`.

#### 10.2.8 OS readability

| OS | Level 4 long lowercase names | Deep directories with `-D` | Note |
|---|---|---|---|
| Linux 2.6 and newer | **Yes**, measured with 2,500 files | Yes | The `iso9660` driver lowercases only when Rock Ridge is absent and the name is uppercase on disc. |
| Windows 7 to 11 | **Unverified. Blocking.** | Expected yes | CDFS is documented for levels 1 and 2 only. Microsoft has never documented ISO 9660:1999 support. genisoimage itself warns that names above 31 characters "may cause buffer overflows in the OS". Probe 2 of section 23.6 must pass first. |
| macOS 10.5 to 15 | Likely yes | Yes | Apple's `cd9660` has read long ISO names for years. Unverified here. |
| FreeBSD | Yes | Yes | Not a target, but noted: FreeBSD reads a profile 2 disc and cannot read a profile 1 disc. |

If Windows truncates to 31 uppercase characters, profile 2 must not be used. The
correct response is to stay on profile 1, not to add a truncation fallback.

#### 10.2.9 Character sets

There is **no** ISO 9660 option set that gives lossless UTF-8 names on every
operating system without Rock Ridge or Joliet.

| Mechanism | Storage | Decoded correctly by |
|---|---|---|
| Level 4, no Rock Ridge, no Joliet | Raw bytes, passed through. No declared encoding. | Linux. Windows and macOS decode the bytes in a legacy code page and produce mojibake. |
| Rock Ridge | Raw bytes, plus POSIX metadata | Linux and macOS. Windows ignores Rock Ridge completely. |
| Joliet | UCS-2 big-endian | Windows, Linux, macOS. Characters above the BMP cannot be represented. |

This does not affect NoahsArk under the profile 2 premise, because on-disc names
are hex. It is the reason never to put raw user file names on an ISO 9660 disc,
and it is the second reason that profile 1 is the default.

### 10.3 Profile 0, `oneshot` (default, Phase 1)

Profile 0 writes **one large run per disc**. There is no append, no
next-writable-address handling, and no block diff. This is the whole Phase 1
disc model, and it is the default.

The filesystem is pure UDF 2.01, built exactly as in section 10.1.2. Phase 1
builds UDF 2.01 and nothing else. The superblock records the filesystem, so a
later phase may write a profile 0 disc with ISO 9660:1999 level 4 instead.

Two variants exist.

**Default: POW-formatted and left open.**

```bash
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=run.bin
```

- `spare:min` formats the blank BD-R for Pseudo-OverWrite with the
  maximum-capacity descriptor, so the spare area is as small as the drive
  allows.
- `-dvd-compat` is **not** passed. The disc stays open.
- The run holds the whole payload up to the data budget.
- The burn covers the used prefix of the image. The UDF tail anchors at
  `N - 256` and `N` are part of the full-size image that `mkudffs` built, so
  they reach the disc only when the burn covers them. On an open disc they may
  therefore be absent until an append or `close` writes them. The disc still
  mounts, because the anchor at LBA 256 is mandatory in the standard and is
  always present. NoahsArk never writes an anchor itself; anchors are
  `mkudffs`'s business.
- A Phase 2 append can later add a repair run, extra parity, or the leftover
  space, with no format change.

**Sealed: `pack --close`.**

```bash
growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:none,tty \
          -Z /dev/sr0=run.bin
```

- `spare:none` skips `FORMAT UNIT` entirely. There is no spare area, no defect
  management, and full capacity.
- LBAs are stable forever. No block can ever be reallocated.
- `-dvd-compat` closes the disc.
- The choice is permanent and is recorded in the superblock.

Profile 0 writes one run, so the LBA re-verification of section 9.8 is not
needed. The tool still reads the disc back and checks every object, because
`verify` is always the program's job.

The trade-off, stated once:

| Option | Format | Defect management | LBA stability | Append |
|---|---|---|---|---|
| `spare:none` | None | Off | Stable | Impossible |
| `spare:min` | Maximum-capacity descriptor, SRM+POW | On | Blocks may move | Possible |

The two paths cost different things:

| | POW-formatted and open (default) | Sealed with `pack --close` |
|---|---|---|
| Format step | `FORMAT UNIT` with the maximum-capacity descriptor | None |
| Spare area | Minimum | None |
| Usable capacity | Slightly reduced by the spare area | Full |
| LBA stability | Stable while nothing is appended | Stable forever |
| Later repair run | Possible in Phase 2 | Impossible |
| Later extra parity | Possible in Phase 2 | Impossible |

Profile 1 and profile 2 choose `spare:min` and pay for it with the LBA read-back
check after every append. Profile 0 chooses `spare:min` too by default, but
never appends, so it never pays that cost; `pack --close` chooses `spare:none`.

A drive that offers no POW feature (`GET CONFIGURATION` feature 0x38 absent), or
that reports plain `BD-R SRM` after a format attempt, forces the sealed variant
of profile 0.

### 10.4 Files at the volume root

Every byte that NoahsArk writes is an ordinary file. The layout is the same
under every profile.

| Path | Content | Written |
|---|---|---|
| `/NOAHSARK/DISC.bin` | Disc superblock (section 9.5). Immutable. | First file of the first run. |
| `/NOAHSARK/README.txt` | Plain-text explanation of the format for a human. | With the first run. |
| `/NOAHSARK/FORMAT.txt` | The byte-layout tables of the superblock, the run header, the layout table, the manifest, the filter and the object header. | With the first run. |
| `/NOAHSARK/runs/<seq>/RUN.bin` | The run header. | First file of its run. |
| `/NOAHSARK/runs/<seq>/layout.bin` | File order, LBA extents, shard size, `k`, `m`, per-shard hashes. | With its run. |
| `/NOAHSARK/runs/<seq>/manifest.bin` | The manifest container. | With its run. |
| `/NOAHSARK/runs/<seq>/filter.bin` | The run filter. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/filters/<seq>.bin` | Every earlier run's filter. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/manifests/<seq>.bin` | The previous 8 runs' manifests. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/snapobj/<name>` | The complete snapshot object of every snapshot, one file each, or one packed `snapobj.bin`. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/snapshots.bin` | The full snapshot table. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/refs.bin` | The ref table. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/discs.bin` | The disc directory. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/prereq.bin` | The prerequisite list of this run. | With its run. |
| `/NOAHSARK/runs/<seq>/parity/pNNNN.bin` | One file per parity column. Each starts with a run header copy. | After every data file of its run. |
| `/NOAHSARK/runs/<seq>/RUN2.bin` | Run header copy. | Last file of its run. |
| `/NOAHSARK/objects/<ab>/<name>` | Chunks and bundles. Shared by every run on the disc. | In fill order. |
| `/NOAHSARK/trees/<ab>/<name>` | Tree and chunklist objects. | In fill order, before the chunks. |
| `/NOAHSARK/snapshots/<name>` | Snapshot objects. | In fill order, before the trees. |

`<seq>` is the run sequence number, zero-padded to 10 decimal digits, so that
lexical order equals numeric order. A reader must not sort run directories as
plain strings without the padding.

`<ab>` is the first two hex digits of the digest. `<name>` is the full
68-character multihash hex.

Every fixed name is short, uses the charset `[A-Za-z0-9._/-]`, and avoids every
Windows reserved name. Both filesystems store these names verbatim.

`README.txt` and `FORMAT.txt` are the files that a human in 2050 opens.
Together they must be complete enough to write a reader from.

### 10.5 Fill order inside a run

The writer places files in this order under every profile:

1. `RUN.bin`, the run header.
2. `layout.bin`, `manifest.bin`, `filter.bin`.
3. The catalog copies.
4. Snapshot objects, then tree objects and chunklists, contiguous.
5. Bundles and chunks, in path order.
6. The checksum column.
7. The parity files, in column order.
8. `RUN2.bin`, the run header copy.

Steps 1 to 6 are the parity domain. The parity files of step 7 cover it. Step 8
sits outside the domain, at the highest LBA of the run, so that a copy of the
header survives damage at either end.

`layout.bin` is written early but records the LBA of every later file. The
writer therefore builds the run image in two passes: it lays the files out,
reads the extents back, writes `layout.bin` into its reserved place, and then
computes the parity over the finished domain.

Under profile 1 the order is expressed as the copy order into the mount. Under
profile 2 it is expressed as `-sort` weights.

Metadata objects are placed together on purpose. A connectivity check over one
run then costs one seek and one sequential read.

### 10.6 Name and path budget

| Item | Limit | Reason |
|---|---|---|
| Object name | 68 characters, `[0-9a-f]` | The text form of the multihash. Never stripped. |
| Hard cap on any name | 126 characters | Satisfies 16-bit CS0 (127), 8-bit CS0 (254), the Linux UDF 254-byte rule, and the ISO level-4 207-byte rule at once. |
| Longest full path | under 220 characters | Windows `MAX_PATH` is 260 including the drive letter and the NUL. Long-path support needs Windows 10 1607, the registry key `LongPathsEnabled=1`, and an application manifest opt-in. Many tools never opt in. |
| Forbidden characters | `< > : " / \ | ? *`, control characters, a trailing space or dot, and `CON PRN AUX NUL COM1-9 LPT1-9` | Windows rejects them. UDF and ISO 9660 permit some of them. |

Measured path lengths: `/NOAHSARK/objects/ab/<68>` is 89 characters,
`/NOAHSARK/objects/ab/cd/<68>` is 92, and
`/NOAHSARK/runs/0000000042/catalog/manifests/0000000041.bin` is 58. All are far
below 220.

Never rely on case to distinguish two objects. Windows and macOS present the
volume case-insensitively.

### 10.7 Burning is externalized

The program does not burn as a core function. The reason is portability and
trust: the burner is platform-specific, it needs device privileges, and it is
the one step that a user may want to run by hand or on another machine.

The split is:

| Step | Owner | Command |
|---|---|---|
| Build the run image and the burn plan | The program | `noahsark pack` |
| Render the plan into command lines | The program | `noahsark burn --print` |
| Execute those command lines | An external burner | `noahsark burn --exec`, Linux only |
| Read the disc back and check every object | The program, always | `noahsark verify` |

`burn --print` writes command lines to standard output. It never touches a
device. It works on every platform. A user may pipe the output into a shell, may
copy it to another machine, or may run the commands by hand.

`burn --exec` exists only on Linux. It runs exactly the commands that
`burn --print` produced, in order, and nothing else. It must not build a command
line of its own.

`verify` always belongs to the program. Only the program knows the layout table,
the manifest, and the content ids.

Profile 1 variant 1a needs no burner for an append. The plan then holds mount,
copy and unmount steps instead of write steps, and `burn --exec` performs them
directly. The rule is unchanged: `--print` shows exactly what will happen.

#### 10.7.1 Burn plan container

`pack` writes `staging/plans/<run_seq>/burn.bin` and a human rendering at
`staging/plans/<run_seq>/burn.json`. The binary file is authoritative.

Container header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NABP"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 16 | u8[16] | `expected_disc_uuid` | The disc that must be in the drive. All zero for a blank disc. |
| 40 | 16 | u8[16] | `repo_uuid` | The repository. |
| 56 | 8 | u64 | `run_seq` | The run being burned. |
| 64 | 8 | u64 | `disc_seq` | The disc sequence number. |
| 72 | 8 | u64 | `expected_nwa_sectors` | Next writable address the drive must report. 0 for a first write. |
| 80 | 8 | u64 | `capacity_sectors` | Capacity the drive must report. |
| 88 | 1 | u8 | `media_type` | Media type registry. |
| 89 | 1 | u8 | `fs_profile` | Disc filesystem profile id. |
| 90 | 1 | u8 | `append_variant` | 1 or 2 under profile 1. 0 elsewhere. |
| 91 | 1 | u8 | `burner_backend` | 1 growisofs, 2 cdrskin, 3 ImgBurn, 4 IMAPI, 5 hdiutil, 6 kernel (variant 1a). |
| 92 | 1 | u8 | `close_disc` | 1 when this plan closes the disc. |
| 93 | 1 | u8 | `reserved_u8` | Zero. |
| 94 | 2 | u16 | `speed` | Burn speed multiplier. 4 for BD-R, 2 for M-DISC. |
| 96 | 2 | u16 | `step_count` | Number of step records. |
| 98 | 2 | u16 | `step_size` | 512. |
| 100 | 4 | u32 | `device_hint_len` | Byte length of the device hint. |
| 104 | 64 | u8[64] | `device_hint` | UTF-8, zero-padded. For example `/dev/sr0`. |
| 168 | 8 | i64 | `created_sec` | Plan creation time. |
| 176 | 4 | u32 | `created_nsec` | Nanoseconds. |
| 180 | 4 | u32 | `tool_version` | Writer version. |
| 184 | 64 | u8[64] | `reserved` | Zero. |
| 248 | 4 | u32 | `reserved_u32` | Zero. |
| 252 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 251. |
| 256 | | | `steps` | `step_count` records of 512 bytes. |

Burn step record, 512 bytes, in execution order:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NABS"`. |
| 4 | 2 | u16 | `step_index` | 0-based. |
| 6 | 1 | u8 | `step_kind` | 1 write image, 2 build and write from a tree, 3 append from a tree, 4 mount read-write, 5 copy tree into the mount, 6 unmount, 7 close, 8 eject, 9 reload. |
| 7 | 1 | u8 | `flags` | bit0 add `-dvd-compat`, bit1 first write of the disc, bit2 metadata patch, bit3 the step needs an arbitrary seek. |
| 8 | 8 | u64 | `seek_lba` | Target LBA. Must be a multiple of 16. 0 when the step does not seek. |
| 16 | 8 | u64 | `byte_len` | Bytes to write. Must be a multiple of 32768 when the step seeks. |
| 24 | 32 | u8[32] | `payload_hash` | Hash of the file, or of the sorted tree listing. |
| 56 | 4 | u32 | `source_len` | Byte length of `source_path`. |
| 60 | 256 | u8[256] | `source_path` | Image file, or the directory tree to copy or build from. UTF-8, zero-padded. |
| 316 | 4 | u32 | `aux_len` | Byte length of `aux_path`. |
| 320 | 184 | u8[184] | `aux_path` | The `-sort` file under profile 2, or the mount point under profile 1 variant 1a. Empty otherwise. |
| 504 | 4 | u32 | `reserved_u32` | Zero. |
| 508 | 4 | u32 | `step_crc32c` | CRC-32C over bytes 0 to 507. |

Rules:

1. `burn --print` must refuse a plan whose `header_crc32c` or any `step_crc32c`
   fails.
2. `burn --exec` must confirm `expected_nwa_sectors` and `capacity_sectors`
   against the drive before the first write. A mismatch is a hard error.
3. `burn --exec` must confirm `payload_hash` for every step before it writes.
4. A seeking step with `seek_lba` not a multiple of 16, or `byte_len` not a
   multiple of 32768, is a defect in the plan. Both `--print` and `--exec` must
   refuse it.
5. `burn --print` must refuse a seeking step under a backend that cannot express
   a seek, and must name the backend that is required.
6. `close_disc` and the `-dvd-compat` flag are set only by a plan that
   `noahsark close` produced, or by `pack` under
   `disc.close_policy = when_full`. Under the default policy, `never`, no plan
   ever carries them.

#### 10.7.2 JSON rendering

`burn.json` is a convenience for a human and for a script. It is derived from
`burn.bin` and is never authoritative.

```json
{
  "format": "noahsark-burn-plan",
  "version": 1,
  "repo_uuid": "6f1d2a44-9c33-4c5e-8b71-2f0a9e5d1c88",
  "disc_uuid": "b21c7f90-3d55-4a12-9e64-77c0a1b38e42",
  "disc_seq": 12,
  "run_seq": 41,
  "media_type": "BD-R SL 25",
  "fs_profile": "udf201-pow",
  "append_variant": "1b",
  "burner_backend": "growisofs",
  "device_hint": "/dev/sr0",
  "speed": 4,
  "close_disc": false,
  "expected_nwa_sectors": 4096512,
  "capacity_sectors": 12219392,
  "steps": [
    {
      "index": 0,
      "kind": "write_image",
      "seek_lba": 4096512,
      "byte_len": 8589934592,
      "source_path": "staging/plans/0000000041/run.bin",
      "payload_hash": "1e2049ab...",
      "dvd_compat": false
    },
    {
      "index": 1,
      "kind": "write_image",
      "seek_lba": 256,
      "byte_len": 32768,
      "source_path": "staging/plans/0000000041/patch-000256.bin",
      "payload_hash": "1e20b7c1...",
      "dvd_compat": false
    },
    { "index": 2, "kind": "eject" },
    { "index": 3, "kind": "reload" }
  ]
}
```

### 10.8 Command templates

`burn --print` renders each step through a template. The templates live in the
config file under `burner.template.<backend>.<kind>`. A user may override any
template.

| Backend | OS | Step | Template |
|---|---|---|---|
| `growisofs` | Linux | first write, profile 1 and 0 | `growisofs -speed={{.Speed}} -use-the-force-luke={{.SpareMode}},tty {{if .DvdCompat}}-dvd-compat {{end}}-Z {{.Device}}={{.SourcePath}}` |
| `growisofs` | Linux | seeking write, profile 1 variant 1b | `growisofs -speed={{.Speed}} -use-the-force-luke=seek:{{.SeekLBA}},spare:min,tty {{if .DvdCompat}}-dvd-compat {{end}}-Z {{.Device}}={{.SourcePath}}` |
| `kernel` | Linux | mount, copy, unmount, profile 1 variant 1a | `mount -t udf -o rw {{.Device}} {{.AuxPath}}` then one `cp` per object in fill order, then `umount {{.AuxPath}}` |
| `growisofs` | Linux | first write, profile 2 | `growisofs -speed={{.Speed}} -use-the-force-luke=spare:min,tty -Z {{.Device}} -iso-level 4 -D -l -allow-limited-size -no-limit-pathtables -sort {{.AuxPath}} -V {{.Label}} {{.SourcePath}}` |
| `growisofs` | Linux | append, profile 2 | `growisofs -speed={{.Speed}} -use-the-force-luke=spare:min,tty {{if .DvdCompat}}-dvd-compat {{end}}-M {{.Device}} -iso-level 4 -D -l -allow-limited-size -no-limit-pathtables -sort {{.AuxPath}} -V {{.Label}} {{.SourcePath}}` |
| `cdrskin` | Linux | write image | `cdrskin dev={{.Device}} speed={{.Speed}} -multi -tao {{.SourcePath}}` |
| `imgburn` | Windows | write image | `ImgBurn.exe /MODE WRITE /SRC "{{.SourcePath}}" /DEST {{.Device}} /SPEED {{.Speed}} /START /CLOSE` |
| `hdiutil` | macOS | write image | `hdiutil burn -device {{.Device}} -speed {{.Speed}} {{.SourcePath}}` |
| `dvd+rw-tools` | macOS | any | The Linux growisofs templates with the platform device name. |

`{{.SpareMode}}` is `spare:min` under profiles 1 and 2 and `spare:none` under
profile 0.

Every rendering also prints, as comments:

- the probe commands `growisofs -F {{.Device}}` and
  `dvd+rw-mediainfo {{.Device}}`;
- the expected next writable address and the expected capacity;
- the eject and reload line before verification;
- the exact `noahsark verify` command to run afterwards.

`cdrskin`, `ImgBurn` and `hdiutil` cannot write at an arbitrary LBA and cannot
build an ISO 9660 append. A plan that needs either therefore selects the
`growisofs` backend, or falls back to profile 0.

### 10.9 Probe commands

Capacity and the next writable address come from the drive:

```bash
eval "$(growisofs -F /dev/sr0)"        # prints next_session=<bytes> capacity=<bytes>
NWA=$(( next_session / 2048 ))
```

Media type comes from `dvd+rw-mediainfo`:

```bash
dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Number of Sessions|Next Writable Address|Free Blocks|Track Size'
```

The string `BD-R SRM+POW` means the disc is appendable in place. The string
`BD-R SRM` means it is not. That one string decides between an appendable
profile and profile 0.

Common rules for every burn:

- `seek:N` requires `N % 16 == 0`. growisofs rejects any other value.
- Both the byte offset and the byte length of a seeking write must be multiples
  of 32768. `poor_mans_pwrite64` rejects anything else with `EINVAL`.
- Never use `-overburn`. The overburn test compares the final progress against
  the drive-reported capacity, which is the correct number.
- Never let a drive use BD-R Random Recording Mode. growisofs refuses profile
  0x42 with `:-( mounted media[42] is not supported`.
- Under profile 1, gate every burn on
  `udfinfo <image> | grep -q '^integrity=closed'`.
- Eject and reload before every verification read. The kernel caches the old
  medium state.

### 10.10 Capacity table and fill limit

| Media | Sectors | Bytes | GiB | Layers |
|---|---:|---:|---:|---:|
| BD-R / BD-RE SL 25 GB | 12,219,392 | 25,025,314,816 | 23.31 | 1 |
| BD-R / BD-RE DL 50 GB | 24,438,784 | 50,050,629,632 | 46.61 | 2 |
| BD-R XL / BD-RE XL TL 100 GB | 48,878,592 | 100,103,356,416 | 93.23 | 3 |
| BD-R XL QL 128 GB | 62,500,864 | 128,001,769,472 | 119.21 | 4 |
| Mini BD SL 8 cm (media type 11) | 3,804,288 | 7,791,181,824 | 7.25 | 1 |
| Mini BD DL 8 cm (media type 12) | 7,608,576 | 15,582,363,648 | 14.51 | 2 |

Notes:

- Media type 10, `image`, has no fixed size. A file image takes the size that
  `pack --capacity` gives it.
- QL 128 GB exists as BD-R XL only. BD-RE XL stops at 100 GB.
- M-DISC BD is sold as SL 25 GB and DL 50 GB, with the same sector counts.
- A tool must never hardcode these numbers for a burn. It must use
  `growisofs -F`.
- Never copy dvdisaster's hardcoded BD sizes (11,826,176 and 23,652,352
  sectors). They are smaller than the real discs and would waste about 3 percent
  of every disc.

Fill policy:

- The packer uses the **forced capacity** (section 9.12), which equals the
  drive-reported capacity unless the user set `--capacity` or
  `disc.force_capacity`.
- `disc.fill_ratio` defaults to 0.95. The outer 3 mm of radius holds about
  5 percent of the disc and is the highest-risk region.
- `fill_limit_sectors = floor(capacity_forced_sectors * disc.fill_ratio)` minus
  the spare reserve.
- `disc.min_spare_ratio` defaults to 0.20. When the remaining POW spare area
  falls below that fraction, the tool warns and recommends no further appends.
- `disc.spare_reserve_bytes` defaults to 512 MiB on an appendable disc. POW
  defect management and filesystem metadata need it.
- Under profile 2 the packer must also reserve the projected directory rewrite
  cost of the remaining appends (section 10.2.3).
- One growisofs command line covers every media size. There is no layer logic
  and no hardcoded sector count in growisofs; `get_2k_capacity()` computes
  `nwa + free_blocks` from `READ TRACK INFORMATION`. BDXL needs a BDXL-capable
  drive and nothing else.

### 10.11 Reserved space

The packer computes a default reserve per disc. The reserve is the part of the
capacity that data must not use.

```
reserve_default =
      safety_margin                     # capacity_forced * (1 - disc.fill_ratio)
    + superblock_and_headers            # DISC.bin, README.txt, FORMAT.txt,
                                        #   plus RUN.bin and RUN2.bin per run
    + catalog_growth                    # filters and manifests for the expected
                                        #   number of future runs
    + spare_area                        # POW spare, when the profile appends
    + fec_parity                        # (m + 1) of every 255 sectors of the
                                        #   run, rounded up to a whole stripe
    + alignment_padding                 # up to 32 KiB per write
```

Terms:

| Term | Formula | Default input |
|---|---|---|
| `safety_margin` | `capacity_forced * (1 - disc.fill_ratio)` | `disc.fill_ratio` = 0.95 |
| `superblock_and_headers` | `3 sectors + expected_runs * 2 sectors` | `disc.expected_runs` = 32 |
| `catalog_growth` | `expected_runs * (filter_size + 8 * manifest_size + snapshot_objects + snapshot_table + disc_directory)` | See sections 12.2, 12.3 and 12.5 for the sizes |
| `spare_area` | `disc.spare_reserve_bytes`, or 0 under profile 0 | 512 MiB |
| `fec_parity` | `ceil(run_sectors / 255) * (m + 1)` | `m` = 23, so 9.41 percent of the run |
| `alignment_padding` | `expected_runs * 32 KiB` | - |

`superblock_and_headers` counts only `RUN.bin` and `RUN2.bin`. The other `m`
copies sit in the first sector of each parity file, and `fec_parity` already
counts those sectors.

`run_sectors` is the whole run, data plus parity. The packer solves the budget
in one pass: it subtracts every other term from the forced capacity, splits what
is left into whole stripes, and gives `k` of every 255 sectors to data.

Under profile 2 the packer adds the projected directory rewrite cost of the
remaining appends (section 10.2.3) to `catalog_growth`.

Overrides:

- `disc.force_reserve` replaces the computed reserve. It accepts bytes or a
  percentage of the forced capacity.
- `disc.extra_reserve` is added to the computed reserve. It accepts the same
  forms.
- Both are recorded in the superblock next to the computed value, so a later
  append uses the same budget.

The data budget is:

```
data_budget = capacity_forced - max(reserve_computed, reserve_forced) - reserve_extra
```

#### 10.11.1 Worked example, BD-R SL 25 GB

Inputs: capacity 12,219,392 sectors (25,025,314,816 bytes), no forced capacity,
`fill_ratio` 0.95, `expected_runs` 32, `m` 23, profile 1, P4 chunking with about
6,000 objects per run.

| Term | Sectors | Bytes |
|---|---:|---:|
| `safety_margin` | 610,970 | 1.25 GB |
| `superblock_and_headers` | 67 | 137 kB |
| `catalog_growth` (32 runs: filter 13.4 KiB, 8 manifests of 375 KiB, tables 0.6 MB) | 57,620 | 118.0 MB |
| `spare_area` | 262,144 | 536.9 MB |
| `fec_parity` (44,267 stripes x 24) | 1,062,408 | 2.18 GB |
| `alignment_padding` | 512 | 1.0 MB |
| **reserve_default** | **1,993,721** | **4.08 GB** |
| **data_budget** | **10,225,671** | **20.94 GB** |

The run occupies 11,288,079 sectors. That is 44,267 stripes. Data takes 231 of
every 255 sectors and parity takes 24, so 10,225,671 plus 1,062,408 gives the
run back exactly.

#### 10.11.2 Worked example, BD-R XL TL 100 GB

Inputs: capacity 48,878,592 sectors (100,103,356,416 bytes), no forced capacity,
same knobs, about 24,000 objects per run.

| Term | Sectors | Bytes |
|---|---:|---:|
| `safety_margin` | 2,443,930 | 5.01 GB |
| `superblock_and_headers` | 67 | 137 kB |
| `catalog_growth` (32 runs: filter 53.4 KiB, 8 manifests of 1.46 MiB, tables 0.6 MB) | 201,650 | 413.0 MB |
| `spare_area` | 262,144 | 536.9 MB |
| `fec_parity` (180,276 stripes x 24) | 4,326,624 | 8.86 GB |
| `alignment_padding` | 512 | 1.0 MB |
| **reserve_default** | **7,234,927** | **14.82 GB** |
| **data_budget** | **41,643,665** | **85.29 GB** |

`pack --dry-run` prints exactly these numbers before it commits to a run:

```
$ noahsark pack --dry-run --disc 12
disc              b21c7f90  seq 12  BD-R SL 25  profile udf201-pow (1b)
capacity reported 12,219,392 sectors   25,025,314,816 B
capacity forced   12,219,392 sectors   25,025,314,816 B
reserve computed   1,993,721 sectors    4,083,140,608 B
reserve forced             -                        -
reserve extra              -                        -
data budget       10,225,671 sectors   20,942,174,208 B
already used       4,096,512 sectors    8,389,656,576 B
free for this run  6,129,159 sectors   12,552,517,632 B
staged objects         5,912           12,203,441,152 B
plan               one run, 5,912 objects, fits
```

### 10.12 Burner backends

| Backend | Tool | Version requirement | Status |
|---|---|---|---|
| `growisofs` | dvd+rw-tools | Debian 7.1-14 or newer, Fedora 7.1-13 or newer, Arch 7.1-13 | Default. |
| `kernel` | Linux udf driver | Kernel 5.4 or newer | Profile 1 variant 1a appends only. |
| `cdrskin` | libburn | 1.5.8 or newer | Fallback, profile 0 only. |

The version requirement is not cosmetic. Upstream dvd+rw-tools 7.1 (2008-03-05)
is broken for BD-R in two ways, and both fixes are distro patches:

| Patch | Bug | Effect without it |
|---|---|---|
| `ignore_pseudo_overwrite.patch`, 2011-03-07 | Debian #615978 | A POW-capable drive under-reports BD-R capacity and the disc cannot be filled. |
| `fix_burning_bd-r_discs.patch`, 2015-02-20 | Debian #713016 | A blank BD-R fails to close with `CLOSE SESSION failed with SK=5h/INVALID FIELD IN CDB`. |

The tool must check the burner version at startup. `burn --print` must warn when
the version is unknown or unpatched. `burn --exec` must refuse.

xorriso is not used. libisofs has no UDF writer, and its ISO 9660 multi-session
support duplicates what growisofs already does.

### 10.13 Tier-2 burners

| OS | Candidate | Note |
|---|---|---|
| Windows | ImgBurn 2.5.8.0, "Write image file to disc" | It writes the bytes that NoahsArk produces. It must never build the filesystem. |
| Windows | IMAPI2 | Reserved. Not evaluated. |
| macOS | `hdiutil burn <image>`, with `drutil status` for media info | Apple's burn engine is single-session only, which matches profiles 1 and 2. |
| macOS | dvd+rw-tools 7.1 from Homebrew | The same command lines transfer. |

Tier-2 burning is not implemented in version 1. `burn --print` already renders
the command lines, so the remaining work is a port, not a redesign.

### 10.14 Verification checklist

Pre-burn, on the image or the staged tree:

1. Profile 1: `udfinfo IMG` shows `udfrev=2.01`, `blocksize=2048`,
   `integrity=closed`, `accesstype=overwritable`, and three `type=ANCHOR` lines
   at 256, N-256 and N.
2. Profile 2: `isoinfo -d -i IMG` reports `NO Joliet present` and
   `NO Rock Ridge present`. The build log reports
   `Total rockridge attributes bytes: 0`.
3. Profile 2: a raw-byte check finds the full 68-character lowercase name in the
   image: `strings -n 60 IMG | grep -m1 -E '^[0-9a-f]{68}$'`. A mount is not
   evidence.
4. `mount -t udf -o loop,ro IMG /mnt`, or `-t iso9660`, succeeds. `stat -f /mnt`
   shows `Namelen: 254` and `Block size: 2048` under profile 1.
5. `find /mnt -type f | wc -l` matches the object count plus the fixed files.
6. `find /mnt | awk '{print length($0)}' | sort -n | tail -1` is under 220.
7. `find /mnt | grep -P '[<>:"|?*\x00-\x1f]' | head` is empty.
8. The image size is a multiple of 32768 bytes.
9. The per-object LBA map is extracted and stored in the layout table.
10. `growisofs -dry-run ...` passes the overburn check.

Post-burn and cross-OS: section 23.5 holds the manual physical checklist. It
covers the eject and reload, the media-info check, the whole-image compare, the
LBA map compare, and the Windows, macOS and Windows XP mounts. Profile 2 also
needs probe 2 of section 23.6 to have passed on real media.

On damaged media, read with
`ddrescue -b 2048 -n -r3 /dev/sr0 rescued.img rescue.map`, then
`ddrescue -b 2048 -d -r3 /dev/sr0 rescued.img rescue.map`. Plain `dd` aborts on
the first error. `dd conv=noerror,sync` keeps offsets aligned but does not
retry; ddrescue does both and produces the erasure list that the FEC layer
needs.

---

## 11. FEC and self-healing

### 11.1 Why the design is what it is

The drive already has a strong error-correction layer. Blu-ray uses a two-code
picket scheme inside every 64 KiB ECC cluster. The Long Distance Code is
RS(248,216) over GF(2^8), with 32 parity symbols per codeword. The Burst
Indication Subcode is RS(62,30), and its bytes are sprinkled through the
cluster. When two
adjacent BIS bytes fail, the drive marks the roughly 38 LDC bytes between them
as erasures. Erasure decoding doubles the correction power.

Two facts follow, and they decide the design:

1. The drive gives no partial credit. If decoding fails, the drive returns a
   hard read error for the whole sector or cluster. NoahsArk therefore sees
   erasures, not bit errors. That is exactly the model an erasure code wants.
2. The drive can also return silently wrong bytes. NoahsArk must add its own
   hashes to detect that case and to convert it into an erasure.

The dangerous physical failure is the **contiguous run**. A ring scratch, an
outer-edge degradation band, or a delamination bubble maps to one long
contiguous LBA interval, usually at a high LBA.

| Damage pattern | LBA footprint | Size on a 25 GB BD |
|---|---|---|
| Radial scratch, 1 mm wide, full radius | ~106,000 hits of about 1 sector each | ~217 MB total, never more than a few sectors contiguous |
| Circumferential scratch, 1 mm radial width | one contiguous run | ~359,000 sectors, ~736 MB |
| Outer-edge ring, outermost 1 mm | one contiguous run at the highest LBAs | ~500,000 sectors, ~1.03 GB |
| Outer-edge degradation, outermost 5 mm | contiguous run at the end | ~4.7 GB |
| Fingerprint, 5 mm across | a few hundred short runs | 10 to 50 MB, scattered |
| Random cluster rot | isolated 64 KiB clusters | small |
| Delamination bubble | contiguous ring segment | 100 MB to several GB |

A radial scratch is harmless to any interleaved code. It hits thousands of
stripes with one sector each. The contiguous run is the case that the layout
must survive, and parity must therefore be spread across the whole radius, not
placed at the end of the disc.

### 11.2 Layout

The scheme follows dvdisaster RS03.

- The FEC scheme is `rs255-gf8`: Reed-Solomon over GF(2^8), 255 total shards,
  `klauspost/reedsolomon`.
- A **shard** is exactly one 2048-byte sector.
- A **stripe** is 255 shards: `k` data + 1 checksum + `m` parity.
- The default is `k = 231`, `m = 23`, which is `k + 1 + m = 255` and about
  10 percent parity relative to payload.
- The **parity domain** of a run is the contiguous LBA range from the first
  sector of `RUN.bin` to the last sector of the last data file, that is the last
  file written before the parity files. Filesystem metadata that lies inside
  that range is protected too, which is the whole reason to define the domain by
  LBA and not by file.
- The domain is split into 255 equal **columns** of `L` sectors, where
  `L = floor(domain_sectors / 255)`.
- Column `c` occupies LBA `[parity_base_lba + c*L, parity_base_lba + (c+1)*L)`.
- Columns `k+1 .. 254` are written as the ordinary files
  `runs/<seq>/parity/pNNNN.bin`, after every data file. `NNNN` is the column
  index in decimal, zero-padded to four digits.
- Stripe `i` is sector `i` of every column.
- Encoding is byte-column-wise. Take one byte from each of the `k + 1`
  information sectors, at the same byte offset. Produce `m` parity bytes at that
  byte offset in the `m` parity sectors. Repeat for all 2048 byte offsets.

```
  LBA ->  base                                                base + 255*L
          |----------|----------|-----   ...   -----|----------|
 column      c = 0      c = 1                          c = 254
          |          |          |                    |          |
 stripe 0 [ s0,0    ][ s0,1    ] ...                [ s0,254   ]
 stripe 1 [ s1,0    ][ s1,1    ] ...                [ s1,254   ]
   ...
 stripe L-1

   columns 0 .. k-1     : data
   column  k            : checksum column
   columns k+1 .. 254   : parity

   A contiguous burst of B sectors lies inside at most ceil(B/L)+1 columns
   and costs each affected stripe at most ceil(B/L) erasures.
   Therefore any single contiguous burst up to m*L sectors is correctable.
```

The interleave is what makes the scheme work: consecutive sectors on the disc
belong to consecutive stripes, so a burst spreads over many stripes with one
erasure each.

### 11.3 Burst tolerance

`L = floor(run_sectors / 255)`. Maximum correctable single burst is `m * L`
sectors. The table assumes the run covers the whole disc.

| Media | L (sectors) | m=12 (5%) | m=23 (10%) | m=28 (12.5%) | m=42 (20%) |
|---|---:|---:|---:|---:|---:|
| BD 25 GB | 47,919 | 575,028 sec = 1.18 GB | 1,102,137 sec = **2.26 GB** | 1,341,732 sec = 2.75 GB | 2,012,598 sec = 4.12 GB |
| BD 50 GB | 95,838 | 1,150,056 sec = 2.36 GB | 2,204,274 sec = **4.51 GB** | 2,683,464 sec = 5.50 GB | 4,025,196 sec = 8.24 GB |
| BD 100 GB | 191,680 | 2,300,160 sec = 4.71 GB | 4,408,640 sec = **9.03 GB** | 5,367,040 sec = 10.99 GB | 8,050,560 sec = 16.49 GB |
| BD 128 GB | 245,101 | 2,941,212 sec = 6.02 GB | 5,637,323 sec = **11.55 GB** | 6,862,828 sec = 14.05 GB | 10,294,242 sec = 21.08 GB |

Configuration and payload, with `k + 1 + m = 255`:

| Target | k | m | actual m/k | parity fraction m/255 | 25 GB payload | 50 GB | 100 GB | 128 GB |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| 5% | 242 | 12 | 4.96% | 4.71% | 23.75 GB | 47.50 GB | 95.00 GB | 121.47 GB |
| **10%** | **231** | **23** | **9.96%** | **9.02%** | **22.67 GB** | **45.34 GB** | **90.68 GB** | **115.95 GB** |
| 12.5% | 226 | 28 | 12.39% | 10.98% | 22.18 GB | 44.36 GB | 88.72 GB | 113.44 GB |
| 20% | 212 | 42 | 19.81% | 16.47% | 20.81 GB | 41.61 GB | 83.23 GB | 106.42 GB |

Interpretation against the damage table:

- 5 percent already covers a 1 mm ring scratch (736 MB) and a 1.5 mm outer band.
- 10 percent covers a 3 mm ring or a full 3 mm outer-edge band on a 25 GB disc.
- 20 percent costs 1.86 GB of payload against the 10 percent baseline of the
  table above, and it buys 1.86 GB more burst tolerance. Damage that large usually means the disc is mechanically
  unreadable anyway.
- **10 percent is the knee of the curve.** It is the default. 20 percent is
  available as a profile for a catalog-heavy disc.

### 11.4 Checksum column

Stripe `i` of the checksum column holds the digests of the data and parity
sectors of stripe `i + 1`.

- The digest is an 8-byte truncated BLAKE3 of the sector.
- `k + m = 254` digests at 8 bytes is 2032 bytes. That fits in a 2048-byte
  sector with 16 bytes of header.
- The collision probability per sector is 2^-64. This detects decay. The
  cryptographic guarantee comes from the object content id, not from here.
- The offset by one stripe means that the RS decode of stripe `i` recovers the
  checksums for stripe `i + 1`. The checksum column is therefore protected by
  the parity itself. This is the dvdisaster RS03 trick.

Checksum sector header, 16 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NACS"`. |
| 4 | 4 | u32 | `stripe_index` | The stripe whose digests follow, that is `i + 1`. |
| 8 | 2 | u16 | `digest_count` | `k + m`. 254 at the default k and m. |
| 10 | 1 | u8 | `digest_bytes` | 8. |
| 11 | 1 | u8 | `hash_algo` | 0x1e, BLAKE3. |
| 12 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 11. |
| 16 | 2032 | u8[2032] | `digests` | 254 digests, in column order 0 to 254 excluding the checksum column. |

Silent corruption is detected per sector, converted into an erasure, and
corrected with one parity symbol instead of two. This doubles the effective
correction power.

### 11.5 Header replication

The run header exists `m + 2` times, and every copy is an ordinary file or the
first sector of one:

1. `runs/<seq>/RUN.bin`, the first file copied in the run;
2. the first sector of every parity file `runs/<seq>/parity/pNNNN.bin`, which is
   `m` copies;
3. `runs/<seq>/RUN2.bin`, the last file copied in the run.

At `m = 23` that is 25 copies. Because copy order equals LBA order, copy 1 sits
at the lowest LBA of the run, copy 3 at the highest, and the parity copies are
spread between them. The radial spread is therefore the same as a fixed-LBA
scheme would give, with no hidden sectors. Two copies at the two ends alone
would be wrong: the end of the disc is the highest-risk region.

The parity geometry is derivable from the run header alone. `parity_base_lba`,
`parity_end_lba`, `k` and `m` determine every column boundary. A recovery tool
that has lost every header copy scans the raw disc for the `"NARH"` magic. If
that fails, it tries each candidate `m` and checks whether the checksum column
lands where the trial predicts.

### 11.6 Cross-disc layers

| Layer | Mechanism | Overhead | Covers |
|---:|---|---:|---|
| 0 | Drive LDC and BIS picket code | free | Bit errors, sub-millimetre scratches. |
| 1 | On-disc RS per run, 10 percent | 10% | Ring scratch, edge decay, up to 2.26 GB contiguous on a 25 GB disc. |
| 2 | Content-addressed re-fetch from any other run or disc | ~0% | Any object that happens to exist in two places. |
| 3 | Disc-close parity run over all runs of a disc | configurable | Damage that crosses run boundaries. |
| 4 | Parity disc, 1 per `fec.group_size` discs | 10% at group size 10 | Total loss of one disc in the group. |
| 5 | Mirror disc | 100% of the mirrored subset | Everything, for the highest-value subset. |

Layer 2 is free and must be tried first. Because objects are addressed by
content, any other holder of the same id supplies a byte-identical copy at zero
decode cost.

Layer 4 has real operational cost: repair needs every disc of the group
mounted. It suits sealed archive sets burned as a complete group, with a group
size of 10 and one parity disc, or 20 and two.

Mirror discs are an optional extra layer. They are not the primary mechanism.
The previous design used two identical discs as the only redundancy. That
scheme costs 100 percent and cannot repair partial damage; it can only replace a
whole object from the other copy, and only when the other copy still mounts.

### 11.7 Heal order

The healer tries the sources in this order. The order is cheapest and most
trustworthy first.

1. **On-disc RS parity** of the damaged run. Correct all erasures. Re-check the
   sector digests and the object content ids.
2. **Content-addressed re-fetch.** Query the catalog for another run or disc
   that holds the same content id. A content-addressed fetch is self-verifying.
3. **Cross-disc parity group**, if the disc belongs to one.
4. **Mirror disc**, if one exists.
5. **The original source path**, if it still exists and its content id matches.
6. **Give up.** Record the loss explicitly, with the object id, the file paths
   that reference it, and the affected snapshots.

Reconstructed objects are written into `staging/heal/`. The next pack puts them
into a **repair run**. The catalog marks the damaged run `degraded` and lists
the lost ids. The restore planner prefers a healthy run over a degraded one.

### 11.8 Verify and scrub

Verify reads the disc with ddrescue and uses the mapfile as the erasure list:

```bash
ddrescue -b 2048 -n -r1 /dev/sr0 /staging/disc.iso map.log
```

`-n` skips scraping on the first pass, so the pass is fast. Escalate to a full
`ddrescue -d -r3` run only when errors appear.

Section 19.8 lists the three verify levels and what each one reads.

Scrub schedule:

| Age | Interval | Depth |
|---|---|---|
| Within 24 hours of burning | once | Level 2, on a **second drive of a different model**. Do not file a disc until it passes on two drives. |
| Year 0 to 1 | at 3 months, then at 12 months | Level 2 |
| Year 1 to 5 | every 12 months | Level 2 on a rotating quarter of the library each quarter |
| Year 5 to 10 | every 6 months | Level 2 |
| Year 10 and beyond | every 6 months | Level 2, plus a migration plan |
| Any disc marked degraded | every 3 months | Level 2, and schedule a re-burn |
| After a flood, a heat event, or a move | immediately | Level 2 on the affected shelf |

A full library scrub cycle should complete in under 12 months. At about 15
minutes per 25 GB disc plus handling, one drive scrubs about 20 discs in an
8-hour day. A 1000-disc library therefore needs about 50 drive-days per year.

Bad burns, not aging, are the dominant cause of early failure. The 24-hour
second-drive check is the single most valuable item in the table.

### 11.9 Health metric and report

The headline metric is the **RS margin**: `m` minus the worst-stripe erasure
count, as a percentage of `m`.

Example: "this run has used 3 of its 23 parity symbols in the worst stripe,
87 percent margin left."

Triggers:

- Below 50 percent margin: re-burn the disc.
- Age above 10 years: re-burn regardless of health.
- The media generation goes out of production: migrate.

Status values: `HEALTHY`, `DEGRADED` (any sector needed RS correction),
`CRITICAL` (any stripe used more than half its parity), `FAILED` (any object is
unrecoverable).

Per-disc report fields:

| Group | Fields |
|---|---|
| Identity | disc uuid, label, burn date, media type, manufacturer id |
| Burn | the drive used, write speed, age |
| Scrub | last scrub date, next scrub due, status |
| Damage | sectors unreadable, sectors RS-corrected, RS margin |
| Objects | total, verified, repaired, lost |
| Drive counters | LDC rate, BIS rate, read-throughput trend |
| Capacity | reported capacity, forced capacity, open or closed |
| Spare | remaining spare area, defect list usage |

Two flags are raised in the per-disc report:

- The forced capacity is below the reported capacity (section 9.12). The report
  states how much medium is deliberately unused.
- The remaining POW spare area is below `disc.min_spare_ratio`, default
  20 percent. The tool then recommends no further appends to that disc. Spare
  exhaustion is what ends the life of an appendable disc, so the number must be
  visible long before it matters.

Per-library report fields:

| Group | Fields |
|---|---|
| Distribution | a histogram of RS margin, a count by status |
| Overdue | discs overdue for a scrub, the oldest unscrubbed disc |
| Risk | objects with a replication factor of 1, objects lost |
| Forecast | the projected re-burn workload for the next 12 months |
| Batch | a failure-rate trend by manufacturer id, so a bad batch is caught early |

Per-object report fields: content id, size, the runs and discs that hold it, the
replication factor, and the last verification time.

Health thresholds when the drive exposes the counters: an LDC average below 13
and a BIS average below 15 indicate a healthy disc. Twice those values is a
warning. Any uncorrectable read is critical. Only some drives expose these
counters, and only through vendor commands.

### 11.10 Encoding cost and memory

Parity is computed in **bands**. A band is a contiguous range of `S` stripes.
The encoder reads the `S` sectors of each of the 255 columns, encodes, and
writes the parity.

- Holding all `m` parity columns in memory needs `m * L * 2048` bytes: 2.26 GB
  on a 25 GB disc at `m = 23`, and 9.0 GB on a 100 GB disc. That is too much.
- With `S = 2048` stripes, one band needs about 1 GiB. Choose `S` so that the
  working set is 512 MiB to 1 GiB.
- Total I/O stays close to one linear pass when the image is on disk.

FEC is never the bottleneck. Even at 300 MB/s on one core, encoding a 25 GB
image takes under two minutes against a burn that takes about an hour at 4x.

---

## 12. Filters, manifests, and catalog

### 12.1 The index-free promise

A local index is an accelerator. The discs answer every question without it.

The promise rests on three structures that every run carries:

1. a **filter**, which answers "is this object probably in this run" and, when
   the answer is negative, proves absence;
2. a **manifest**, which answers exactly where an object is;
3. a **catalog**, which carries seven items: the complete snapshot objects of
   the whole repository, the filters of every earlier run, the manifests of the
   most recent runs, the snapshot table, the ref table, the disc directory, and
   the prerequisite list.

Every one of these is an ordinary file under
`/NOAHSARK/runs/<seq>/`, so a reader needs only the filesystem.

The newest disc is therefore a complete entry point. It tells a reader the whole
shape of the problem: every snapshot, every disc, and every object that it is
missing. It never says "I do not know".

### 12.2 Run filter

The filter type is **BinaryFuse16**, from `github.com/FastFilter/xorfilter`.

Reasons:

1. The object set of a run is frozen when the run is built. That is exactly the
   precondition of an xor or fuse filter. The one drawback of a batch filter, no
   incremental insert, costs nothing here.
2. It gives a false-positive rate of 2^-16, that is 0.0015 percent, at about
   18.2 bits per key. A Bloom filter at the same rate needs 23 bits per key,
   26 percent more.
3. Serialization is a fixed struct plus a byte array. It maps directly into
   memory.
4. A query is three probes into nearby segments, so querying thousands of
   filters in a row stays cache-friendly.

The false-positive rate, not the size, drives the choice. With 2,000 runs, a
genuinely new chunk gets `2000 * 2^-16 = 0.031` spurious hits, so about 3
percent of new chunks produce one spurious run hit. A Bloom filter at 1 percent
would give 20 spurious hits per new chunk, that is 20 disc mounts to write one
chunk.

Filter container:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NAFL"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 8 | u64 | `run_seq` | The run described. |
| 32 | 8 | u64 | `key_count` | Number of object ids in the filter. |
| 40 | 8 | u64 | `seed` | BinaryFuse seed. |
| 48 | 4 | u32 | `segment_length` | BinaryFuse parameter. |
| 52 | 4 | u32 | `segment_length_mask` | BinaryFuse parameter. |
| 56 | 4 | u32 | `segment_count` | BinaryFuse parameter. |
| 60 | 4 | u32 | `segment_count_length` | BinaryFuse parameter. |
| 64 | 8 | u64 | `fingerprint_count` | Number of u16 fingerprints. |
| 72 | 1 | u8 | `filter_type` | Filter type registry. 1 = binaryfuse16. |
| 73 | 1 | u8 | `hash_algo` | Multicodec code of the keys. |
| 74 | 2 | u16 | `reserved_u16` | Zero. |
| 76 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 75. |
| 80 | `2 * fingerprint_count` | u16[] | `fingerprints` | The filter body. |
| `80 + 2 * fingerprint_count` | 4 | u32 | `body_crc32c` | CRC-32C over the body bytes. |

A Bloom filter is used only in memory, for the run that is being built, where
incremental insertion is genuinely required. A Bloom filter is never written to
a disc.

Filter sizes, at 18.2 bits per key plus the 84-byte header:

| Run size | Objects (P4) | Filter size |
|---|---:|---:|
| 25 GB | ~6,000 | 13.4 KiB |
| 100 GB | ~24,000 | 53.4 KiB |

Cumulative filter bundle carried on the newest disc, 25 GB discs, profile P4:

| Runs | Total objects | Filter bundle | Share of a 25 GB disc |
|---:|---:|---:|---:|
| 100 | 600,000 | 1.31 MiB | 0.005% |
| 500 | 3,000,000 | 6.55 MiB | 0.027% |
| 2,000 | 12,000,000 | 26.2 MiB | 0.110% |
| 2,000 (100 GB discs) | 48,000,000 | 104.3 MiB | 0.109% |

The cost is negligible even at 2,000 runs. Carrying every filter is therefore
correct.

A reserved extension exists for very large repositories: filters for runs
`1 .. N-100` may be merged into decade-sized super filters, one filter over the
union of 100 runs' objects. A super-filter hit narrows the search to 100 runs.
This must be used only when the bundle exceeds 64 MiB. At the numbers above it
never will.

Each filter blob's hash is recorded in the catalog, so a silently corrupted copy
is detected instead of giving wrong answers.

### 12.3 Run manifest

The manifest is a chunked table-of-contents container, in the shape of Git's
multi-pack-index. A chunk id is 4 bytes and an offset is 8 bytes, and the
directory ends with a sentinel. An old reader skips a chunk id that it does not
know, so the format can grow without a version bump. That matters on write-once
media, where a file cannot be patched later.

Container header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NAMF"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 8 | u64 | `run_seq` | The run described. |
| 32 | 8 | u64 | `record_count` | Number of manifest records. |
| 40 | 4 | u32 | `chunk_count` | Number of TOC entries. |
| 44 | 2 | u16 | `record_size` | 64. |
| 46 | 1 | u8 | `hash_algo` | Multicodec code. |
| 47 | 1 | u8 | `fanout_bits` | 8 or 16. |
| 48 | 8 | u64 | `payload_len` | Container length, for validation. |
| 56 | 4 | u32 | `body_crc32c` | CRC-32C over every byte after the TOC. |
| 60 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 59. |
| 64 | `16 * (chunk_count + 1)` | | `toc` | TOC entries, then a sentinel. |

TOC entry, 16 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `chunk_id` | Four ASCII bytes. |
| 4 | 4 | u32 | `reserved_u32` | Zero. Keeps `offset` aligned. |
| 8 | 8 | u64 | `offset` | Byte offset of the chunk from the container start. |

The sentinel entry has `chunk_id` 0 and `offset` equal to the container length.

Chunk ids in version 1:

| Chunk id | Content |
|---|---|
| `"FANO"` | Fan-out table: 256 or 65536 cumulative u32 counts. |
| `"RECS"` | The sorted manifest records. |
| `"PREQ"` | The prerequisite list. |
| `"SRCR"` | The set of run seqs that this run references. |
| `"DUPS"` | Duplicate accounting: bytes and object counts. |
| `"BMAP"` | Per-snapshot reachability bitmaps. Optional, `OPT_BITMAPS`. |
| `"RIDX"` | Reverse index by LBA. Optional, `OPT_REVIDX`. |

Manifest record, 64 bytes, sorted ascending by `content_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | The object id. |
| 32 | 8 | u64 | `uncompressed_size` | Payload bytes after decompression. |
| 40 | 8 | u64 | `container` | Bundle id index, or the LBA of the object. |
| 48 | 8 | u64 | `offset` | Byte offset inside the container, or inside the sector. |
| 56 | 2 | u16 | `flags` | bit0 in a bundle, bit1 duplicate for locality, bit2 metadata object, bit3 spilled TLV payload. |
| 58 | 1 | u8 | `hash_algo` | Multicodec code. |
| 59 | 1 | u8 | `digest_len` | 32. |
| 60 | 1 | u8 | `kind` | Object kind registry. |
| 61 | 1 | u8 | `compression` | Compression id. |
| 62 | 2 | u16 | `reserved_u16` | Zero. |

A lookup is: read `fanout[b-1]` and `fanout[b]` for the first byte `b` of the
id, then binary search that slice. With fixed 64-byte records the search is pure
arithmetic. There is no parsing, and the container maps directly into memory.

Manifest sizes:

| Objects in the run | Manifest size | Share of a 25 GB disc |
|---:|---:|---:|
| 6,000 (25 GB, P4) | 375 KiB | 0.0016% |
| 24,000 (100 GB, P4) | 1.46 MiB | 0.0015% |
| 48,000 (100 GB, P3) | 2.93 MiB | 0.003% |

A writer must use a 16-bit fan-out (65536 entries, 256 KiB) once a run holds
more than 1,000,000 objects. It removes 8 binary-search steps. It costs 256 KiB
of sequential read. Optical seeks cost about 100 ms each, so the trade is clear.
The `FEAT_FAN16` required feature bit marks it.

### 12.4 Prerequisite list

The prerequisite list holds the object ids that this run's snapshots reference
but that this run does not contain, with the run seq that holds each.

Record, 48 bytes, sorted ascending by `content_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | The referenced object. |
| 32 | 8 | u64 | `run_seq` | The run that holds it. |
| 40 | 8 | u64 | `disc_seq` | The disc that holds that run. |

The list is the discipline that Git's partial clone calls the promisor marker: a
run declares that it is partial and says where the rest lives. "On another disc"
must never be indistinguishable from "corrupt".

The list is bounded, because the capping knobs of section 15 bound the number of
runs that one run may reference.

### 12.5 Snapshot objects, snapshot table and ref table

The catalog replicates the **complete snapshot objects** of every snapshot in
the repository, not only a table of their ids. A snapshot object is a few
hundred bytes, so the whole history costs little. A reader that finds one recent
disc therefore holds every snapshot object, and needs no other disc to list the
history, to name a root tree, or to walk a parent chain.

Tree objects are **not** replicated. A tree is large and there are many of them,
so replicating trees would cost as much as the data. Only the snapshot objects,
which name the root trees, are replicated.

Both tables below are also replicated in full on every run. They grow with the
size of the set, not with the size of the data, so replication is cheap and it
makes any recent disc a usable entry point.

Snapshot table record, 128 bytes, sorted by `generation` then by `snapshot_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `snapshot_id` | Content id of the snapshot object. |
| 32 | 32 | u8[32] | `parent_id` | Parent snapshot id. Zero for a root. |
| 64 | 32 | u8[32] | `root_tree` | Root tree id. |
| 96 | 8 | u64 | `generation` | 1 + parent generation. |
| 104 | 8 | i64 | `time_sec` | Snapshot time. |
| 112 | 8 | u64 | `object_count` | Objects reachable. |
| 120 | 4 | u32 | `first_run_seq` | The run that first held the snapshot object. |
| 124 | 4 | u32 | `flags` | bit0 the snapshot is complete on this set. |

#### 12.5.1 The simple table container

The snapshot table, the ref table and the disc directory share one container
shape. Header, 64 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NAST"` snapshot table, `"NARF"` ref table, `"NADD"` disc directory. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 16 | u8[16] | `repo_uuid` | The repository. |
| 40 | 8 | u64 | `record_count` | Records that follow. |
| 48 | 2 | u16 | `record_size` | 128 snapshot table, 64 ref table, 160 disc directory. |
| 50 | 1 | u8 | `hash_algo` | Multicodec code of every id in the records. |
| 51 | 1 | u8 | `digest_len` | 32. |
| 52 | 4 | u32 | `reserved_u32` | Zero. |
| 56 | 4 | u32 | `body_crc32c` | CRC-32C over the records. |
| 60 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 59. |
| 64 | `record_count * record_size` | | `records` | Sorted, fixed-width. |

Records are sorted, so a reader binary-searches them without an index. The
snapshot table uses the record of this section, the ref table the ref record of
section 8.8, and the disc directory the record of section 12.6.

The replicated snapshot objects live in `catalog/snapobj/<name>`, one ordinary
file per snapshot, named by the full multihash hex of the snapshot object. The
name is the content id, so a reader verifies each file without any other
structure.

Size math for 10,000 snapshots:

| Item | Per item | 10,000 items |
|---|---:|---:|
| Snapshot object payload, typical (header, three ids, times, a short tag list) | 320 B | 3.20 MB |
| Snapshot object payload, worst case allowed by the size cap | 640 B | 6.40 MB |
| UDF File Entry and directory overhead, one file each | 2,048 B | 20.48 MB |
| Snapshot table record | 128 B | 1.28 MB |

Typical total per run: 3.20 + 20.48 + 1.28 = **24.96 MB**, which is 0.10 percent
of a 25 GB disc and 0.025 percent of a 100 GB disc.

The filesystem overhead dominates the payload. A writer may therefore pack the
snapshot objects of the whole repository into one `catalog/snapobj.bin`
container with the bundle layout of section 8.3, which removes the 20.48 MB and
leaves **4.48 MB** per run. The container form is the default above
`catalog.snapobj_pack_threshold` snapshots, default 1,000.

At 10,000 snapshots and 2,000 runs, the replicated history costs about 9 GB
across the whole archive in container form. That is under half of one
disc for a complete, 2,000-fold redundant history.

### 12.6 Disc directory

The disc directory lists every disc of the repository.

Record, 160 bytes, sorted by `disc_seq`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 16 | u8[16] | `disc_uuid` | The disc. |
| 16 | 8 | u64 | `disc_seq` | Sequence number. |
| 24 | 32 | u8[32] | `super_hash` | Hash of that disc's superblock. |
| 56 | 8 | u64 | `capacity_sectors` | Reported capacity. |
| 64 | 8 | u64 | `capacity_forced_sectors` | Forced capacity, section 9.12. |
| 72 | 8 | u64 | `used_sectors` | Sectors written. |
| 80 | 8 | i64 | `first_burn_sec` | First burn time. |
| 88 | 8 | i64 | `last_verify_sec` | Last verification time. 0 when never verified. |
| 96 | 4 | u32 | `run_count` | Runs on the disc. |
| 100 | 1 | u8 | `media_type` | Media type registry. |
| 101 | 1 | u8 | `fs_profile` | Filesystem profile id. |
| 102 | 1 | u8 | `health` | 1 healthy, 2 degraded, 3 critical, 4 failed, 5 unknown. |
| 103 | 1 | u8 | `state_flags` | bit0 closed, bit1 append-raw-only, bit2 spare below the threshold, bit3 capacity forced. |
| 104 | 2 | u16 | `rs_margin_percent` | Worst-stripe margin, as a percentage of `m`. |
| 106 | 2 | u16 | `spare_remaining_percent` | Remaining POW spare, as a percentage. |
| 108 | 4 | u32 | `label_len` | Byte length of the label. |
| 112 | 48 | u8[48] | `label` | UTF-8, zero-padded. The first 48 bytes of the superblock label, which is the printed part. |

The container header is the simple table container of section 12.5.1, with
magic `"NADD"` and `record_size` 160.

Chaining `super_hash` proves the ordering of the set and detects a substituted
disc. A restore can name the disc that it needs by its human label, not by a
hash.

### 12.7 Catalog contents per run

Every run carries:

| Item | Scope | Why |
|---|---|---|
| This run's filter | This run | Membership. |
| Every earlier run's filter | The whole repository | A negative across all filters is a proof of absence. |
| This run's manifest | This run | Exact lookup. |
| The previous 8 runs' manifests | Recent history | Losing the newest disc must not lose the newest exact membership data. Warming a cache from one disc then yields 9 manifests. |
| Every snapshot object, complete | The whole repository | A reader lists, names and walks the whole history from one disc. |
| The full snapshot table | The whole repository | The sorted index over those objects. |
| The full ref table | The whole repository | The entry point for names. |
| The disc directory | The whole repository | The entry point for "which physical disc". |
| This run's prerequisite list | This run | What is missing and where it is. |
| This run's layout table | This run | LBA extents of every file, for FEC and for the Phase 3 recovery read. |

Tree objects are not in this list. A tree is reachable through its snapshot, and
replicating trees would cost as much as replicating the data.

The manifest history depth is `manifest.history_depth`, default 8. Eight
manifests cost about 2.93 MiB at 6,000 objects per run, at the 375 KiB per
manifest of section 12.3.

#### 12.7.1 Copy priority under the size cap

The catalog may not exceed `catalog.max_bytes`, default 1 percent of the disc
budget. When the full catalog would exceed the cap, the writer drops items in
reverse priority order:

| Priority | Item | Dropped when |
|---:|---|---|
| 1 | Every snapshot object | Never. |
| 2 | Every run filter | Never. |
| 3 | The disc directory | Never. |
| 4 | The snapshot table and the ref table | Never. |
| 5 | Recent manifests, newest first | The cap is reached. |

Priorities 1 to 4 are mandatory. They grow with the size of the set, not with
the size of the data, so they stay small. Only the manifest history is elastic:
the writer keeps as many recent manifests as the cap allows, and records the
number it kept in the catalog header. A run that keeps zero manifests is still
valid, because a filter negative is still a proof and the run's own manifest is
outside the catalog.

If priorities 1 to 4 alone exceed the cap, the writer does not drop them. It
raises the reserve instead and reports the new figure, because losing the
history would break the index-free promise.

Manifests are **not** replicated for the whole repository. At 2,000 runs the
cumulative manifest would be 2,000 x 375 KiB = 732 MiB. That is still only 3
percent of a disc, so it is possible, but it grows linearly with no rollup and
it duplicates what the local cache already holds.

### 12.8 Dedup rule

The rule is absolute:

> **Never drop chunk data on the strength of a filter. A filter hit is a hint to
> go read an exact manifest. Only an exact manifest hit permits dropping the
> data.**

If the manifest cannot be consulted, because the disc is not available or the
cache is cold, the writer writes the chunk again and logs the event.

The reason is the asymmetry. Writing a duplicate chunk wastes a few megabytes.
Dropping a needed chunk writes a reference to an object that does not exist,
onto write-once media. That failure is silent, undetectable until a restore, and
unrepairable.

The numbers make it concrete. At BinaryFuse16 with 2,000 runs, about 3 percent
of genuinely new chunks get a spurious hit. Without confirmation, roughly 3
percent of new data would be silently dropped from every backup.

The commit path is therefore:

1. Query the filter union. A negative is final: the chunk is new.
2. A positive must be confirmed by an exact manifest lookup.
3. An unconfirmed positive is recorded in a `pending-confirm` log, and the chunk
   is written again. The log tells the user exactly which discs, if mounted,
   would save how much space.

### 12.9 Connectivity check

The check proves that every object reachable from a snapshot exists somewhere.
It is separate from the byte-integrity check, exactly as
`git fsck --connectivity-only` is separate from a full fsck.

```
needed  := { root tree of S }
missing := { }
while needed is not empty:
    take id from needed
    locate id:
        a) in this run's manifest             -> read it
        b) in the cache's merged index        -> note the run, defer
        c) test every run's filter
             all negative -> MISSING, record it        (a proof)
             some positive -> candidate runs, defer
    if the object is a tree, chunklist or snapshot:
        read it and queue its children, grouped by run
    if the object is a chunk:
        membership alone is enough; do not read it
```

Key points:

- **Chunks are never read.** A chunk has no outgoing reference, so membership is
  the whole obligation. Chunks are more than 99 percent of the bytes. This is
  what makes the check affordable.
- Only trees, chunklists and snapshots are read. For a typical snapshot the
  metadata is well under 1 percent of the data.
- A filter negative across every run is a **proof** of absence. Filters have no
  false negatives. The check can therefore conclude "missing" with certainty
  from the cached filters alone, with no disc mounted.
- A filter positive is only a hint. Resolve it against that run's manifest.
- Group the deferred reads by run and process one run at a time. Each disc is
  mounted at most once. Metadata objects are contiguous inside a run
  (section 10.5), so one run costs one seek and one sequential read.

With the optional per-snapshot bitmaps, the check reduces to a counting
argument: for each run, OR its snapshot bitmap into an accumulator, then compare
the accumulated count with `object_count` in the snapshot record.

The report says, per object, "present on run X", "missing", or "declared
prerequisite".

---

## 13. Local cache

### 13.1 Location and contents

The cache lives at `$XDG_CACHE_HOME/noahsark/<repo-uuid>/`, and defaults to
`~/.cache/noahsark/<repo-uuid>/`. The `--cache-dir` flag overrides it.

The directory name says what it is. Everything inside is derived and
rebuildable.

| Item | Content |
|---|---|
| `index.bin` | Merged sorted index over all runs. The manifest record plus `run_seq`. Multi-pack-index shape. |
| `filters/<seq>.bin` | Copies of run filters. |
| `manifests/<seq>.bin` | Copies of run manifests, accumulated as discs are mounted. |
| `snapshots.bin` | The snapshot table. |
| `refs.bin` | The ref table. |
| `discs.bin` | The disc directory, plus local additions: shelf location, notes. |
| `health.log` | Per-disc verification history. |
| `xlate-<from>-<to>.bin` | Optional cross-algorithm side table (section 5.7). |
| `pending-confirm.log` | Unconfirmed filter hits, with the runs that would confirm them. |

`index.bin` uses the manifest record layout with an added `run_seq`, and a
256-entry or 65536-entry fan-out. Rebuilding it is a merge sort over the
per-run manifests. There is never a conflict to resolve, because a manifest is
immutable and a run seq is unique. That is the concrete meaning of "the cache is
only an accelerator".

**Never in the cache**: anything that is not on a disc. If losing the cache
loses data, the design is broken.

### 13.2 Rebuild levels

| Level | Minimum set | Gives | Cost |
|---:|---|---|---|
| 1 | The newest disc | The catalog: every run's filter, the snapshot table, the ref table, the disc directory, and the newest 8 manifests. Answers "which run probably holds X" for the whole repository. | One disc mount, a few seconds. |
| 2 | The discs a snapshot references | Everything needed to restore that snapshot. | The plan's disc count. |
| 3 | Every disc | The exact merged index, for maximum dedup on the next backup. | One mount per disc, about 0.3 s of reading each. |

Level 3 is lazy and incremental:

- Start from level 1.
- Copy a run's manifest into the cache the first time that disc is mounted for
  any reason.
- `noahsark rebuild-cache` asks for discs one at a time, newest first, and reads
  only manifests. Newest first matters, because recent runs hold the data most
  likely to be seen again.
- Because the newest disc also carries the previous 8 manifests, warming from
  one disc yields 9 exact manifests, not 1.

A full exact rebuild of a 500-disc repository is 500 disc swaps. That is a
human-time problem, not a machine-time problem. The data areas are never read.

### 13.3 Staleness

Staleness is detected by `disc_seq` and `disc_uuid`. It is never detected by
mtime and never by a time-to-live. The truth lives on shelves, so a clock is the
wrong instrument.

| State | Condition | Action |
|---|---|---|
| Current | The newest disc found matches the recorded `disc_seq` and `disc_uuid`. | Use the cache. |
| Stale | A newer disc exists. | Merge that disc's manifest and catalog. |
| Wrong | A disc's uuid does not match the cache's record for that seq. | Refuse the cache. Rebuild. This means a different repository or a re-burn. |
| Incomplete | A plan references a run with no cached manifest. | Ask for that disc and merge. |
| Version mismatch | The cache format version differs. | Delete and rebuild. Never migrate. |

Migration code is pure risk, because rebuilding is always possible by
definition.

### 13.4 Cache-less operation

Every command must work with the cache absent.

| Command | Behaviour with no cache |
|---|---|
| `commit` | Works. Dedup falls back to "write the chunk again" for every unconfirmed hit. The `pending-confirm` log records the cost. |
| `pack` | Works. Locality uses only the staged objects. |
| `plan` | Reads the catalog from the newest disc first, then plans. |
| `restore` | Same. Then reads the discs the plan names. |
| `verify` | Works from the disc alone. |
| `ls`, `log` | Read the snapshot table from the newest disc. |

CI must include a test that deletes the cache, rebuilds from the newest image
alone, and restores successfully.

---

## 14. Staging store

### 14.1 Layout

The staging store is a local directory inside the repository work area.

```
staging/
    objects/ab/cd/<id>          object files waiting to be packed. Two fan-out
                                levels, because a local filesystem handles deep
                                trees better than UDF does.
    images/<disc_uuid>.img      disc image mirrors (profile 1 variant 1b)
    plans/<run_seq>/            burn plan, run image, sort file, patches
    restore/                    restore assembly area
    heal/                       reconstructed objects
    mirror/                     sync mirror, cleared after commit (Phase 2)
    commitbundles/<name>/       imported commit bundles (Backlog)
    state.db                    append-only binary state log
```

`state.db` is the only authoritative local state. Everything else in staging is
either an object that also exists in the source, or derived data.

**Staging must be on a local filesystem, or on NFS with `sync` semantics.**
An SMB or CIFS mount is refused. The reasons are specific: `fsync` on SMB does
not reliably reach the server's stable storage, and sparse image files behave
inconsistently, so a disc image mirror can silently differ from what was
written. The tool reads the filesystem type of `staging.dir` at startup, through
`statfs` on Linux, and exits with code 2 and a named reason when the type is
`cifs` or `smb3`. `staging.allow_unsafe_fs` overrides the check, and the
override is recorded in the state log.

### 14.2 Object state machine

```
        commit
          |
          v
     +---------+   pack    +--------+   burn ok   +--------+
     | STAGED  |---------->| PACKED |------------>| BURNED |
     +---------+           +--------+             +--------+
          ^                    |                      |
          |  burn failed       |  verify failed       | verify ok. This is
          +--------------------+  (run marked         | mandatory, after an
          |                       for re-burn)        | eject and a reload.
          |                                           v
          |                                      +--------+
          |                                      | CLEAN  |
          |                                      +--------+
          |                                           |
          |                    retention timer passed |
          |                    (staging.retain_after_clean, default 7 days)
          |                                           v
          |                                   +---------------+
          |                                   | GC-ELIGIBLE   |
          |                                   +---------------+
          |                                           |
          |                                       gc  |
          |                                           v
          +---------------------------------------  deleted
```

Rules:

1. **`verify` is the only transition from BURNED to CLEAN.** There is no timer
   and no manual override. An object stays in staging until the disc that holds
   it has been read back and checked.
2. `burn --exec` runs `verify` after the burn by default
   (`burn.verify_after`, default true). It ejects and reloads the disc first, so
   the read comes from the medium and not from a cache. It uses a second drive
   only when one is present.
3. GC never deletes an object that is not CLEAN.
4. GC is a separate command. Phase 1 runs it manually.
5. A run that fails to burn returns its objects to STAGED.
6. A run that fails verify keeps its objects PACKED and marks the run for
   re-burn.
7. An imported bundle object enters the machine at STAGED, exactly like a
   locally chunked object. `commit` and `import` are the only entry points.
8. The state log is authoritative only for objects that are not yet CLEAN.
   Everything about a CLEAN object is derivable from the discs.

### 14.3 State log format

The log is append-only. It has a container header and fixed-width records.

Header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NASL"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 16 | u8[16] | `repo_uuid` | The repository. |
| 40 | 2 | u16 | `record_size` | 96. |
| 42 | 1 | u8 | `hash_algo` | Multicodec code. |
| 43 | 1 | u8 | `digest_len` | 32. |
| 44 | 4 | u32 | `reserved_u32a` | Zero. Keeps `created_sec` aligned. |
| 48 | 8 | i64 | `created_sec` | Log creation time. |
| 56 | 4 | u32 | `reserved_u32b` | Zero. |
| 60 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 59. |
| 64 | | | `records` | Records, in append order. |

Record, 96 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | The object. |
| 32 | 8 | i64 | `time_sec` | When the transition happened. |
| 40 | 4 | u32 | `time_nsec` | Nanoseconds. |
| 44 | 1 | u8 | `state` | 1 STAGED, 2 PACKED, 3 BURNED, 4 CLEAN, 5 GC-ELIGIBLE, 6 DELETED. |
| 45 | 1 | u8 | `kind` | Object kind registry. |
| 46 | 1 | u8 | `reason` | 0 normal, 1 burn failed, 2 verify failed, 3 healed, 4 duplicate for locality. |
| 47 | 1 | u8 | `compression` | Compression id. |
| 48 | 8 | u64 | `stored_len` | Bytes stored. |
| 56 | 8 | u64 | `run_seq` | The run, when the state is PACKED or later. 0 otherwise. |
| 64 | 16 | u8[16] | `disc_uuid` | The disc, when known. All zero otherwise. |
| 80 | 8 | u64 | `sequence` | Monotonic record number. |
| 88 | 4 | u32 | `reserved_u32` | Zero. |
| 92 | 4 | u32 | `record_crc32c` | CRC-32C over bytes 0 to 91. |

The current state of an object is the newest record for that id, by `sequence`.
A reader replays the log from the start. A writer may compact the log by
rewriting it with only the newest record per id, but only after every CLEAN
object has been dropped.

A record with a bad CRC ends the replay. Records after it are ignored, and the
tool reports a truncated log. That is the correct behaviour after a crash during
an append.

### 14.4 GC rules

1. An object may be deleted only in state GC-ELIGIBLE.
2. An object reaches GC-ELIGIBLE only after `staging.retain_after_clean` has
   passed since it reached CLEAN. The default is 7 days.
3. GC must confirm, before every delete, that the object is present in at least
   one run whose verification passed. The confirmation reads the manifest, not
   the cache index alone.
4. GC never deletes an image mirror of a disc that is still appendable.
5. GC never deletes a burn plan that has not reached CLEAN.
6. `gc --dry-run` prints what it would delete and how many bytes it would free.

The two-step rule, mark before act, is the same discipline that Duplicacy calls
two-step fossil collection. A disc can never be un-burned, so the tool must
never act before it has marked.

---

## 15. Packing and locality

### 15.1 Why locality wins over dedup

Read time is fixed by the size of the data. The number of discs that a restore
touches is what the design controls.

Worked example, single drive, `restore.rate_mb_s` 20 and
`restore.switch_seconds` 60:

- 100 GB restored from 5 full discs: `5 * (60 + 25000/20)` s = about 1.8 hours.
  The overhead is 5 minutes, which is negligible.
- 100 GB restored from 80 discs at 1.25 GB each: `80 * (60 + 62)` s = about 2.7
  hours. The overhead is 80 minutes, which is half the time.

A disc costs a small amount of money. A disc swap costs a minute of human
attention on every future restore. On removable media, locality wins.

### 15.2 Rules

The rules are ordered. A conflict is resolved by this order.

1. **A file's chunks go in one run.** The only exceptions are a file larger than
   the remaining capacity by more than `split.threshold` (default 25 percent of
   a disc), and a file larger than a whole disc.
2. **A directory's files go in one run** when the directory fits, in depth-first
   path order.
3. **Siblings stay adjacent.** Objects are written in depth-first path order
   inside a run, so a partial-directory restore is one linear read.
4. **Split only when forced.** Split at the tail: fill the current run, continue
   on the next. Record the split so the planner puts the two discs next to each
   other.
5. **Metadata is written first inside a run**, contiguous, and the catalog is
   replicated on every run.
6. **Never break rule 1 to gain a few percent of utilization.** A disc that is
   92 percent full and locality-clean beats a 99.5 percent full disc that splits
   four files.

### 15.3 Algorithm

Two passes, plus a pre-pass.

```
pre-pass:  files larger than one disc get their own run chain first.

pass 1:    walk the tree in depth-first path order.
           assign whole directories to the current run while they fit.
           this is next-fit at directory granularity.
           it gives the locality.

pass 2:    fill the tail of each run with first-fit-decreasing over the
           leftover files, in descending size, choosing leftovers from the
           directory nearest in path order to what is already in the run.
           this recovers most of the wasted capacity without scattering
           whole directories.
```

First-fit-decreasing alone uses at most `11/9 * OPT + 6/9` bins and is near
optimal for disc count, but it destroys path order completely. Next-fit by path
order preserves locality perfectly and is a factor-2 approximation on bin count.
The hybrid takes the locality of the first and most of the utilization of the
second.

Do not chase the last percent. On a 25 GB disc, 3 percent waste is 750 MB and
costs pennies.

### 15.4 Controlled duplication

Perfect dedup produces the worst possible restore. If a snapshot's chunks are
spread one per disc across 500 discs, restoring means 500 disc swaps.

NoahsArk therefore adopts capping, which is Lillibridge's technique from FAST
2013, with a run in place of a container. Their result: capping at 10 to 20
containers per 20 MB segment costs a few percent of dedup ratio and buys a 2x to
6x restore speed-up.

Knobs:

| Key | Default | Meaning |
|---|---|---|
| `locality.max_source_runs` | 8 | Per 1 GiB segment of the incoming stream, reference at most this many older runs. Rewrite the rest. |
| `locality.segment_size` | 1 GiB | The unit that capping is applied over. |
| `locality.rewrite_below_chunks` | 64 | Never keep a run as a source to save fewer than this many chunks from one segment. |
| `locality.max_duplicate_bytes_per_file` | 64 MiB | Per-file hard cap on rewritten bytes. |
| `locality.max_duplicate_ratio_per_file` | 0.05 | Per-file cap as a fraction of the file. |
| `locality.disc_budget` | 0.03 | Per disc, duplicated bytes must stay below this fraction of capacity. |

Algorithm, per segment:

1. Chunk the segment. For each chunk, query the filter union and confirm against
   a manifest. Build the map `run -> count of chunks this run could supply`.
2. Sort runs by count, descending. Keep the top `max_source_runs`. Drop any run
   that supplies fewer than `rewrite_below_chunks`.
3. Chunks available from a kept run are referenced, not written.
4. All other chunks are written into the new run, even though a copy exists
   elsewhere.
5. Record the exact set of referenced run seqs in the manifest chunk `"SRCR"`,
   and the count in the run header. The planner can then state up front which
   discs a restore needs.

Presets:

| Preset | `max_source_runs` | Effect |
|---|---:|---|
| `dedup` | unlimited | Maximum space saving. A restore may need every disc. |
| `balanced` | 8 | **Default.** A snapshot restores from at most about 9 runs per segment. Expect a few percent of dedup loss. |
| `locality` | 2 | A snapshot restores from at most 3 runs per segment. Good for one disc set per project. |
| `standalone` | 0 | No cross-run references at all. Every disc set is readable with no other disc. Costs the most media and gives the strongest durability story. |

`standalone` also makes the filter chain purely informational. It is a
legitimate archival choice and the design must not forbid it.

Note the second-order effect: controlled duplication puts an object on several
runs, which gives the restore planner real freedom (section 16.1). It is also
redundancy: an object that exists twice survives the loss of one disc.

### 15.5 Duplication accounting

Every run records, in the manifest chunk `"DUPS"`:

| Field | Meaning |
|---|---|
| `duplicated_bytes` | Bytes written again for locality. |
| `duplicated_objects` | Objects written again. |
| `unique_bytes` | Bytes that exist only in this run. |
| `dedup_saved_bytes` | Bytes not written because an older run supplies them. |

The run header repeats `duplicate_bytes`. Every manifest record for a duplicated
object sets the "duplicate for locality" flag.

The tool prints the duplication overhead after every burn, and keeps a running
repository figure. An overhead above 5 percent is a warning, not a silent cost:
it means that the chunk size or the packing order is wrong.

### 15.6 Capacity budget

The packer must fit a run inside the data budget. Section 10.11 gives the
formula, every term, and two worked examples. `free_now` is that budget minus
the sectors already used on the disc.

`pack --dry-run` prints the whole budget before anything is written.

A forced capacity (section 9.12) changes every number above. The packer must
read it from the superblock of the target disc, not from the drive.

### 15.7 Split threshold

A file is split across runs only when:

- it is larger than one whole disc; or
- it is larger than the remaining capacity by more than `split.threshold`,
  default 0.25 of a disc.

Otherwise the packer starts a new run for the file. Wasting a quarter of a disc
is cheaper than adding a disc to every future restore of that file.

When a split happens, the packer places the parts on discs that the plan will
order adjacently, and records the split in the chunklist and in the manifest.

### 15.8 Consolidation

Deduplication against old discs makes each new burn cheap and makes the restore
plan longer over time. After 100 discs, restoring the current snapshot may touch
60 of them.

Consolidation writes a fresh, self-contained set of discs that carries every
object the current snapshot needs, with no reference to older discs. A restore
of that snapshot then touches only the new set. Old discs are never erased; they
stay for older snapshots and for redundancy.

Triggers, evaluated after every burn:

| Trigger | Key | Default |
|---|---|---|
| The restore plan touches too many discs | `consolidate.max_plan_discs` | 20 |
| Spread ratio: plan discs divided by `ceil(snapshot_bytes / disc_capacity)` | `consolidate.max_spread_ratio` | 2.0 |
| Estimated restore time | `consolidate.max_restore_hours` | 8 |
| The oldest disc in the plan is too old | `consolidate.max_disc_age` | 5 years |
| A disc in the plan failed verification | - | always |

`noahsark health` reports the trigger metrics, so the user sees a consolidation
coming instead of being surprised by a 40-disc burn request.

---

## 16. Restore and the disc plan

### 16.1 The planner

Restore planning is minimum set cover, which is NP-hard. The planner does three
things in order.

**Step 1: unique-element reduction.** If an object exists on exactly one run,
that run's disc is in every valid plan. Add all such discs. Remove all objects
they cover. This usually leaves a very small residual problem, and for a
repository with no controlled duplication it leaves none at all.

**Step 2: greedy on the residual.**

```
P = mandatory_discs(N)
U = N minus covered(P)
while U is not empty:
    pick the disc d that maximizes score(d, U)
    P = P + d
    U = U minus S_d
return order(P)
```

The default score is the number of **bytes** newly covered, not the object
count, because bytes track read time. An object-count score is available as an
option.

Greedy returns at most `H(k) <= ln n + 1` times the optimum. Feige proved in
1998 that `(1 - alpha) ln n` approximation is impossible for any constant
`alpha > 0` unless P = NP. Greedy is therefore the right algorithm, and no
better one is worth seeking. An exact ILP solve is feasible at this size, but it
adds a dependency for no measurable gain.

**Step 3: tie-breaks.** Applied in this fixed order, so a plan is deterministic
and reproducible:

1. A disc that is already in a drive. Zero switch cost. Seed the plan with the
   loaded discs when they cover anything needed.
2. Disc health. Prefer a good last-verify result. Demote a disc with read errors
   or an old last-verify date. A disc that failed verify is a last resort, used
   only when it is mandatory.
3. Most remaining bytes covered.
4. Newer disc.
5. Lower `disc_seq`.

### 16.2 Disc-major order

```
for each disc in plan order:
    detect the disc
    read every needed object from it in one pass, sorted by LBA
    write those objects into staging/restore/
    for every file whose chunks are now all present:
        assemble it, write it to the target, free its staging space
    eject
```

The switch count equals the number of discs in the plan. That is the minimum
possible.

File-major order is the anti-pattern. If consecutive files live on different
discs, each file boundary can cost a switch, and the worst case is the number of
`(file, disc)` pairs.

```
   PLAN: disc 12, disc 7, disc 40
   +------------------------------------------------------------------+
   | disc 12  | read 3.2 GB in LBA order  | assemble A, B, C | eject   |
   +------------------------------------------------------------------+
   | disc 7   | read 1.1 GB in LBA order  | assemble D       | eject   |
   +------------------------------------------------------------------+
   | disc 40  | read 0.4 GB in LBA order  | assemble E, F    | eject   |
   +------------------------------------------------------------------+
             staging/restore/ holds only the chunks of files that
             are not yet complete
```

### 16.3 Staging budget

The worst-case staging use is the whole snapshot size, which happens when every
file has one chunk on the first disc and one on the last. The formal bound at
any moment is the sum of the already-fetched bytes of every incomplete file.

Four measures keep the peak small:

1. Enforce packing rule 1 at write time. This is the biggest lever. A file then
   completes during the pass over one disc.
2. Free per file, not per disc.
3. Order the plan discs by "number of files this disc completes, given the discs
   already visited", descending. This needs no extra reads.
4. Place a genuine multi-disc split so that its discs are adjacent in plan order.

`restore.staging_budget` declares the limit. If the predicted peak exceeds it,
the planner splits the restore into several passes and accepts re-visiting a
disc. The extra switches appear in the plan. A silent disk-full at hour three of
a restore is the worst possible failure.

### 16.4 The plan file

The plan is printed and persisted **before any read**. This is the equivalent of
Bacula's bootstrap file, which is the proven interaction for "which volumes do I
need".

The plan must fail up front when a required disc is missing from the inventory.
The operator must learn about a missing disc in second one, not in hour three.

```json
{
  "format": "noahsark-restore-plan",
  "version": 1,
  "repo_uuid": "6f1d2a44-9c33-4c5e-8b71-2f0a9e5d1c88",
  "snapshot": "1e2049ab7c...",
  "target": "/restore/2026-09",
  "files": 128401,
  "bytes": 107374182400,
  "objects": 262144,
  "peak_staging_bytes": 8589934592,
  "estimated_seconds": 6550,
  "switches": 5,
  "discs": [
    {
      "order": 0,
      "disc_uuid": "b21c7f90-3d55-4a12-9e64-77c0a1b38e42",
      "disc_seq": 12,
      "label": "2027-03 ARK 12",
      "shelf": "shelf 3, box B",
      "health": "healthy",
      "bytes_to_read": 21474836480,
      "objects_to_read": 5210,
      "files_completed": 41230,
      "estimated_seconds": 1134
    }
  ],
  "missing_discs": [],
  "degraded_runs": []
}
```

### 16.5 Time model

| Quantity | Value | Class |
|---|---|---|
| BD 1x | 36 Mbit/s = 4.5 MB/s | specified |
| BD 2x | 9 MB/s | specified |
| BD 4x | 18 MB/s | specified |
| BD 6x | 27 MB/s | specified |
| BD 8x | 36 MB/s | specified |
| BD 12x | 54 MB/s | specified |
| BD 16x | 72 MB/s | specified |
| Real sustained read | 0.5 to 0.8 of the rated peak, lower on inner tracks | estimated |
| Tray load and disc recognition | 10 to 25 s | estimated |
| Spin-up to first data | 3 to 10 s | estimated |
| Mount and read the catalog | 1 to 3 s | estimated |
| Eject and tray out | 3 to 8 s | estimated |
| Human swap | 15 to 60 s, use 30 s | estimated |
| **Total fixed cost per switch** | **about 60 s** | estimated |

```
t_disc(d) = t_swap + t_load + t_spinup + t_mount + bytes_d / rate_effective + t_eject
T_restore = sum over d in plan of t_disc(d)          # single drive
```

The estimate is printed with every plan. It makes the cost of poor locality
visible and quantified.

### 16.6 Disc detection

1. Read `/NOAHSARK/DISC.bin` and compare the `disc_uuid`. A
   file is format-independent, verifiable, and works on a loopback image.
2. The filesystem label is a hint for a human only. Labels are truncated and are
   not unique in practice.
3. Poll `CDROM_DRIVE_STATUS` at 1 Hz. It returns `CDS_NO_DISC`, `CDS_TRAY_OPEN`,
   `CDS_DRIVE_NOT_READY`, or `CDS_DISC_OK`. This always works and needs no
   daemon.
4. Subscribe to udev `change` events on `KERNEL=="sr*"` as the fast path. udev
   is lower latency but does not always report a removal.
5. After `CDS_DISC_OK`, wait for the device to settle, then mount read-only and
   read the superblock. Retry the mount a few times; a BD drive needs seconds to
   become ready.
6. Eject with `ioctl(fd, CDROMEJECT)` after unmounting. Offer `--no-eject` for
   slot-load and caddy drives.

**Do not prompt when the expected disc is detected.** Print one line, for
example "disc 47 of 112 detected, reading 3.2 GB", and continue. A confirmation
prompt on a 100-disc restore adds human latency to every switch.

Prompt only when the wrong disc is inserted, when the disc is unreadable, or
when the user passed `--interactive`.

### 16.7 Multi-drive restore

With `k` drives the goal changes from "fewest discs" to "shortest makespan".

- Assign discs to drives by longest-processing-time-first list scheduling. LPT
  is a `4/3 - 1/(3k)` approximation for makespan, which is good enough.
- With a human in the loop, the human is the scarce resource. The useful pattern
  is pipelining: while drive 1 reads disc `i`, the human loads disc `i+1` into
  drive 2. Two drives remove almost all human wait from the critical path. Three
  or more help only when reads are slower than swaps.
- Do not assign two discs that complete the same file to different drives at
  very different times, or staging grows. Keep the plan order and hand each disc
  to whichever drive is free next.
- Report the per-drive queues in the plan, so the operator knows which disc goes
  into which drive.

### 16.8 Restore pipeline

```
  snapshot id
      |
      v
  +----------------+   read snapshot, trees, chunklists (metadata only)
  | object set     |   from the cache or from the newest disc
  +----------------+
      |
      v
  +----------------+   filters -> candidate runs
  | run map        |   manifests -> exact (run, container, offset)
  +----------------+
      |
      v
  +----------------+   unique-element reduction, greedy, tie-breaks
  | disc plan      |   -> print and persist plan.json, fail on a missing disc
  +----------------+
      |
      v
  for each disc:
      detect -> read needed objects in LBA order -> staging/restore/
                     |
                     v
             verify each object's content id      <-- hard error on mismatch
                     |
                     v
             assemble every file that is complete
                     |
                     v
             create -> write -> xattr/ACL -> chown -> chmod -> flags -> times
                     |
                     v
             free the staging space of that file
      eject
      |
      v
  deferred pass: directory times, in reverse depth order
      |
      v
  loss report (JSON) + replay plan + exit code
```

### 16.9 Cache-less restore

With no cache, the restorer reads the catalog from the newest disc first. That
gives every filter, the snapshot table, the ref table, the disc directory, and
the newest 8 manifests. The planner then works normally.

If the newest disc is lost, the fallback reads every available disc's manifest
and rebuilds the catalog. That is slow but always possible. Both paths must
exist and both must be tested.

---

## 17. File metadata and permissions

### 17.1 The field set

Metadata lives inline in the tree entry, not in a separate node object. Section
8.5 gives the byte layout. This section states the policy.

Mandatory fields, always in the fixed header: entry type, mode, uid, gid, size,
mtime, name.

Optional fields, each a TLV:

| Group | Fields |
|---|---|
| Names | user name, group name |
| Times | atime, ctime, birth time |
| Extended | xattrs, POSIX ACL access and default, NFSv4 ACL |
| Flags | Linux chattr flags, BSD and macOS flags |
| Windows | attributes, security descriptor, NTFS alternate data streams |

The **format** defines every field from the start. The **implementation** is
phased:

| Field | Phase |
|---|---:|
| Entry type, mode, uid, gid, name, size | 1 |
| mtime | 1 |
| Symlink target | 1 |
| Hardlink group | 1 |
| User name and group name | 1 |
| atime, ctime, birth time | 2 |
| Extended attributes | 2 |
| POSIX ACL, access and default | 2 |
| Linux chattr flags | 2 |
| Windows attributes and security descriptor | 2 |
| NFSv4 ACL, BSD and macOS flags, NTFS alternate data streams | 3 |

A Phase 1 writer never emits a TLV that it does not implement. A Phase 1 reader
preserves and reports an unknown non-critical TLV, and does not apply it.

The reason for inline metadata is read amplification. A separate node object
would cost one object read per file instead of one per directory. On a medium
with 100 ms seeks, that is the difference between usable and unusable. A
separate node also saves nothing on a metadata-only change, because the parent
tree changes either way.

### 17.2 Encodings

| Item | Encoding | Reason |
|---|---|---|
| Time | `i64` seconds plus `u32` nanoseconds | A single `i64` of nanoseconds overflows on 2262-04-11 and cannot express dates before 1678. An archival format must outlive that. `struct timespec`, `utimensat` and Go's `time.Time` all use seconds plus nanoseconds, so there is no conversion and no rounding. |
| Nanoseconds | Always in `[0, 999999999]` | A negative time is a negative seconds value with a non-negative nanosecond part, which is Go's normalization. |
| File type | `entry_type`, a u8 enum | Two encodings of the same fact, as in a combined `st_mode`, are a source of canonicalization bugs in a content-addressed format. |
| Mode | `u32`, low 12 bits | Permission bits only. The type is not here. |
| uid, gid | `u32`, `0xFFFFFFFF` means unknown | Numeric identity. |
| User and group name | UTF-8 TLV | Portable identity. |
| Symlink target | Raw bytes, TLV, critical | A Linux path is a byte string, not text. |
| POSIX ACL | Portable binary: count, then `{u16 tag, u16 perm, u32 id}` | The kernel `system.posix_acl_access` blob is architecture-specific and version-specific. The text form costs a parse on every restore. |
| Windows security descriptor | Opaque self-relative blob | Never parsed. Inheritance flags preserved exactly. |
| Hardlink identity | Repository-local `hardlink_group` id | Never an inode number. |

### 17.3 Ownership policy

1. Always store both the numeric id and the name. The numeric id is mandatory.
   The name is optional and is omitted when the source has no name for the id.
2. On restore, resolve the stored name locally and use the resulting id.
3. When the name is absent or the lookup fails, fall back to the stored numeric
   id.
4. `--numeric-owner` skips step 2 and always uses the stored numeric ids. This
   is the correct mode for a bare-metal restore into a rescue environment and
   for restoring into a container image.
5. `--no-owner` skips ownership. Files get the invoking user's uid and gid. This
   is implied when the restore is not privileged.
6. A failure to apply ownership must never fail the entry. The restorer writes
   the file, records a `metadata_not_applied` event with the path, the field and
   the reason, and continues.
7. Ownership is applied with `fchownat(..., AT_SYMLINK_NOFOLLOW)`, never
   `chown`, so a symlink cannot redirect the change.

### 17.4 Restore order

The order per entry is fixed, and every step has a reason:

1. Create the object: `openat`, `mkdirat`, `symlinkat`, `mknodat`.
2. Write the content.
3. Set xattrs and ACLs.
4. `fchownat(AT_SYMLINK_NOFOLLOW)`. **chown clears setuid and setgid on Linux**,
   so it must come before chmod.
5. `fchmodat`. It must follow chown to restore setuid and setgid.
6. Set Linux chattr flags and BSD flags. Immutable and append-only block later
   writes, so they must come after every write.
7. `utimensat(AT_SYMLINK_NOFOLLOW)`. Every preceding step changes mtime, so
   times come last.
8. Directory times are applied in a **deferred second pass**, after all children
   are written, because writing a child updates the parent's mtime. The pass
   uses the retained directory descriptor with `futimens(dirfd)`, never a
   re-opened path.

### 17.5 Cross-platform capability matrix

`Y` = applied. `~` = applied with loss or approximation. `N` = dropped, and a
warning is issued.

| Field | Linux to Linux | Linux to macOS | Linux to Windows | macOS to Linux | Windows to Linux | Windows to Windows |
|---|---|---|---|---|---|---|
| type, mode bits | Y | Y | ~ (readonly bit only) | Y | ~ (synthesised) | Y |
| setuid, setgid, sticky | Y | Y | N | Y | N | N |
| uid, gid numeric | Y (root) | ~ (ids differ) | N | ~ | N | N |
| user and group name | Y | Y | ~ | Y | ~ | Y (via SID) |
| mtime | Y | Y | Y | Y | Y | Y |
| atime | Y | Y | Y | Y | Y | Y |
| ctime | N (never settable) | N | ~ (admin only) | N | N | ~ |
| birth time | N (Linux cannot set it) | Y | Y | Y | Y | Y |
| symlink target | Y | Y | ~ (needs SeCreateSymbolicLinkPrivilege or developer mode) | Y | ~ | Y |
| hardlink | Y | Y | Y (NTFS) | Y | Y | Y |
| device nodes | Y (root) | Y (root) | N | Y | N | N |
| fifo, socket | Y | Y | N | Y | N | N |
| user xattrs | Y | ~ (namespaces differ) | ~ (NTFS EAs) | ~ | ~ | Y |
| `security.*`, `trusted.*` xattrs | ~ (needs CAP_SYS_ADMIN) | N | N | N | N | N |
| POSIX ACL | Y | ~ (macOS has no POSIX.1e ACL) | ~ (lossy DACL translation) | N | N | N |
| NFSv4 ACL | ~ (NFSv4 and ZFS mounts only) | Y | ~ | Y | ~ | Y |
| Linux chattr flags | ~ (safe subset) | N | N | N | N | N |
| BSD and macOS flags | N | Y | ~ (UF_HIDDEN to FILE_ATTRIBUTE_HIDDEN) | Y | ~ | ~ |
| Windows attributes | ~ (readonly to the mode w bit) | ~ (hidden to UF_HIDDEN) | ~ | ~ | ~ | Y |
| NTFS alternate data streams | N | ~ (do not map to a resource fork) | ~ | N | N | Y |
| Windows security descriptor | N | N | N | N | N | Y (needs SeRestorePrivilege, SeSecurityPrivilege, SeTakeOwnershipPrivilege) |

Notes on the Windows column: a full security descriptor backup needs membership
of Backup Operators or administrator rights. Without those, only the current
user's owner, group and DACL are captured, and on restore only the DACL is
applied while owner and group become the restoring user. Treating the descriptor
as opaque must not mean carelessness: restic shipped a real bug in which
restored ACEs were always marked explicit instead of inherited.

### 17.6 Failure policy

1. Restore is **strict for data and best-effort for metadata**. A chunk that
   does not verify is a hard error. A metadata field that cannot be applied is a
   recorded event.
2. `--metadata-strict` turns every metadata failure into a hard error. It is for
   verification runs and for restores that must be bit-exact.
3. Cross-platform loss is reported once per field kind, with a count and a few
   example paths. A restore of a million files must not produce a million
   warnings.
4. **Never translate between ACL models silently.** If a POSIX ACL cannot be
   applied, drop it and report it. A lossy POSIX-to-DACL translation that widens
   access is a security bug. A translation, if it is ever offered, sits behind
   an explicit `--translate-acl` flag and must never grant more access than the
   source entry did.

### 17.7 Non-root restore

What fails without privileges:

| Operation | Error | Capability needed |
|---|---|---|
| `chown` or `lchown` to another uid | `EPERM` | `CAP_CHOWN` or root |
| `chown` to another gid | allowed only for the caller's groups | - |
| setuid and setgid bits | `chmod` succeeds, but the kernel clears setgid when the file's gid is not one of the caller's groups | - |
| `mknod` for a character or block device | `EPERM` | `CAP_MKNOD` |
| `security.*` and `trusted.*` xattrs | `EPERM` | `CAP_SYS_ADMIN` |
| `system.posix_acl_*` | allowed only when the caller owns the file | - |
| Immutable and append-only flags | `EPERM` | `CAP_LINUX_IMMUTABLE` |
| Writing into an unwritable directory | `EACCES` | This is a **data** error, not a metadata error |

Rules:

1. **Probe once at start.** Check `geteuid() == 0` and the effective capability
   set. Pre-select the metadata plan. Do not discover the same `EPERM` a million
   times.
2. Under an unprivileged plan, `--no-owner` is implied, device nodes are skipped
   with a per-entry record, and privileged xattr namespaces are skipped.
3. Print one clear line at the start: "restoring unprivileged; ownership, device
   nodes and privileged xattrs will not be applied".
4. Produce a machine-readable report at the end: `{path, field, reason, errno}`
   records plus a summary count by field kind.
5. Offer `--report-replay=FILE`, a plan that a privileged user can run
   afterwards to apply the deferred ownership and device nodes. This is what
   makes an unprivileged restore genuinely useful.
6. Never let an unprivileged restore silently produce a tree that looks
   complete. The exit status must be 1 and the summary must be printed.

### 17.8 Safety: names and symlinks

Two threat classes exist, and both have produced CVEs in comparable tools.

**Name traversal.** An entry named `..`, an absolute path, a Windows
drive-relative path such as `C:foo`, a UNC path, a name containing `/` or `\`, a
Windows reserved name, or NTFS stream syntax `name:stream`.

**Symlink redirection.** The archive holds `evil -> /etc`, then a later entry
`evil/passwd`. A naive restorer writes through the symlink and lands outside the
target. This works even when every individual name is harmless, and it is a
race even against a pre-check, because another process can plant the symlink
between the check and the open.

Defences, all mandatory:

1. **Validate at parse time.** Reject any entry whose name is empty, is `.` or
   `..`, contains `/`, `\`, or NUL. A tree entry name is one path component by
   definition, so this is a format invariant. Enforcing it at parse means the
   traversal class cannot reach the writer at all.
2. **Never build a path string and open it.** Walk with one directory file
   descriptor per level. Open each child with `openat(dirfd, name, ...)` and
   `mkdirat(dirfd, name, ...)`.
3. **`O_NOFOLLOW` on every open of a regular file**, plus `O_CLOEXEC`, plus
   `O_EXCL` on create. If the target exists and is a symlink, `openat` returns
   `ELOOP` instead of following it.
4. **`O_DIRECTORY | O_NOFOLLOW` when descending.** The check and the use are
   then the same syscall, which closes the race.
5. On Linux, use `openat2(2)` with `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS |
   RESOLVE_NO_MAGICLINKS` where available. That is the kernel-enforced version
   of the same rule. Fall back to the `openat` chain on older kernels.
6. Create symlinks with `symlinkat(target, dirfd, name)`. **Do not validate or
   rewrite the target.** A symlink pointing at `/etc/passwd` is legitimate
   content. Safety comes from never traversing it, not from censoring it.
7. With `--overwrite`, `unlinkat` the existing path first and then create. Never
   open an existing path for truncation; it may have been swapped for a symlink.
8. Create hardlinks with `linkat(dirfd, name, dirfd2, name2, 0)`, flags `0`,
   never `AT_SYMLINK_FOLLOW`.
9. On Windows, open with `FILE_FLAG_OPEN_REPARSE_POINT` so a planted junction is
   not traversed, and reject reserved device names and trailing dots or spaces.
10. Apply directory times in the deferred pass with the retained descriptor.

### 17.9 Flags and exit codes

Section 19.12 lists the `restore` flags that switch each metadata field off.
`--metadata-strict` turns every metadata failure into a hard error.

Exit codes:

| Code | Meaning |
|---:|---|
| 0 | Everything applied. |
| 1 | Data restored, with metadata loss. |
| 2 | Data restore failed. |

A script can therefore tell the cases apart.

### 17.10 Unstable entries

Section 18.6 states when a writer sets the `UNSTABLE` flag. This section states
what a restore does with it.

`restore` writes the file normally. It prints one warning line per unstable
entry, naming the path. An unstable entry alone sets exit code 1, not 2, because
the file was restored. `restore --strict-unstable` refuses to write such an
entry and reports it as missing. The loss report carries the paths under the
field name `content_unstable`.

### 17.11 Loss report

```json
{
  "format": "noahsark-metadata-report",
  "version": 1,
  "snapshot": "1e2049ab7c...",
  "target": "/restore/2026-09",
  "privileged": false,
  "summary": [
    { "field": "owner", "count": 128401, "reason": "EPERM", "examples": ["/etc/passwd", "/var/log/syslog"] },
    { "field": "device_node", "count": 12, "reason": "EPERM", "examples": ["/dev/null"] },
    { "field": "xattr.security", "count": 43, "reason": "EPERM", "examples": ["/usr/bin/ping"] },
    { "field": "content_unstable", "count": 2, "reason": "UNSTABLE", "examples": ["/var/log/app.log"] }
  ],
  "exit_code": 1
}
```

The replay plan holds the same records in a form that a privileged user can
apply: a list of `(path, field, value)` triples plus the commands that apply
them.

---

## 18. Commit flow

### 18.1 Commit flow

```
0. Choose direct mode (section 18.3) or mirror mode (section 18.4).
1. Resolve the source roots and the parent snapshot.
2. Build the candidate file list:
     a. full scan when --full-scan or --checksum is given;
     b. otherwise the quick check of section 18.2 against the parent
        snapshot's tree entry.
3. For every candidate file:
     a. chunk it with the configured profile;
     b. hash every chunk;
     c. query the filter union; confirm a positive against a manifest;
     d. compress and write every genuinely new chunk into staging;
     e. build the chunk list, or a chunklist object above 64 chunks.
4. For every directory, bottom-up:
     a. build the tree entries, sorted by name bytes;
     b. build the TLV areas, sorted by type;
     c. spill any TLV area above the threshold;
     d. hash and write the tree object.
5. Write the snapshot object with the root tree, the parent, the generation,
   and the metadata TLVs.
6. Append a ref record for the ref being moved, by default LATEST.
7. Record every new object in the state log as STAGED.
```

A commit normally runs against a live source. Section 18.6 states how a file
that changes during the read is detected and skipped. Section 18.7 states the
better answer, which is to commit a filesystem snapshot.

An unchanged file costs one tree entry and no chunk read. An unchanged directory
costs one hash comparison, because its tree id did not change.

### 18.2 The quick check

`commit` never writes to a source root. A source is read strictly read-only.
Every byte that NoahsArk creates goes into staging, at `staging.dir`, which may
be a separate disk.

The change test is the rsync quick check. For every file, the walker compares
three fields with the parent snapshot's tree entry:

| Field | Compared as |
|---|---|
| Size | Exact `u64` equality. |
| mtime | Seconds and nanoseconds, exact equality. |
| ctime | Seconds and nanoseconds, exact equality. |

The three-field form is the default for a local source. A remote source uses
size and mtime only (section 18.10), because ctime is not trustworthy there.
`source.quick_check` selects the form.

If all compared fields are equal, the file is unchanged. The walker reuses the entry's
chunk list and never opens the file. If any one differs, the file is read,
chunked and hashed again.

ctime is included because it changes on a metadata change and cannot be set by
an ordinary program, so it catches an edit that restored the mtime.

**Known blind spots.** The quick check is a heuristic, and the specification
states its limits rather than hiding them:

1. A change that keeps the size, the mtime and the ctime is missed. That needs a
   deliberate act, such as a raw device write or a clock rollback.
2. A filesystem with a coarse timestamp resolution can hide a same-second edit
   of the same size.
3. A restored-from-backup source can carry an old mtime with new content.

`commit --checksum` disables the quick check and rehashes every file. It is the
correct answer after any event that could produce case 1, 2 or 3. It costs a
full read of the source.

`commit --full-scan` also rehashes everything. `--checksum` is the accepted
spelling, and `--full-scan` is its alias.

#### 18.2.1 The parent tree is the old copy

rsync compares a source against an old copy of the same data. NoahsArk keeps no
old copy. **The parent snapshot's trees hold the size, the mtime and the ctime
of every path, so they take the place of rsync's old copy.**

That has one important consequence: the staging disk never needs to hold a
second copy of the source. It needs space only for the change set.

### 18.3 Direct mode

Direct mode is the Phase 1 default. The source is a local path.

```
1. Walk the source. For each path, stat it.
2. Compare size, mtime and ctime with the parent tree entry.
3. Read and chunk only the files that differ, or that are new.
4. Reuse the parent entry, unchanged, for every file that matches.
5. A path in the parent tree that the walk did not see is a deletion.
6. Write only new chunks into staging.
```

The source is opened read-only. Nothing is copied first.

### 18.4 Mirror mode

Mirror mode exists for a source that must not be held open for hours: a remote
machine over ssh, or a slow network share. It moves the read into one short
rsync transfer, and it pulls **only the changed files**.

```
noahsark sync /srv/data            # or  user@host:/srv/data
noahsark commit --from mirror --source-root /srv/data
```

The four steps of `sync`:

```
1. Obtain a stat listing of the source: path, type, size, mtime, ctime,
   mode, uid, gid.
     - local source:  a walk;
     - remote source: one ssh command that prints the same listing.
2. Diff the listing against the parent snapshot's trees.
     - present and equal in all three fields -> unchanged;
     - present and different, or absent from the parent -> changed;
     - present in the parent and absent from the listing -> deleted.
3. Write the changed paths to a file, then run
     rsync -aHAX --numeric-ids --files-from=<list> SOURCE staging/mirror/
4. Report the count and the byte size of the change set.
```

Then `commit --from mirror --source-root /srv/data`:

```
5. Read the changed files from staging/mirror/ and chunk them.
6. Reuse the parent tree entry, unchanged, for every unchanged path.
7. Apply the deletions from the listing diff.
8. Record /srv/data as the path in the snapshot, not the mirror path.
9. Clear staging/mirror/ after the objects reach STAGED.
```

`--delete` is **not** used, because the mirror is not a full copy. Deletions come
from the listing diff, which is authoritative.

| rsync option | Reason |
|---|---|
| `-a` | Archive mode: recursion, times, mode, owner, group, symlinks. |
| `-H` | Preserve hard links, which the tree model records as groups. |
| `-A` | Preserve POSIX ACLs. |
| `-X` | Preserve extended attributes. |
| `--numeric-ids` | Never map ids through the local name service. |
| `--files-from` | Transfer only the change set. |

The staging disk therefore needs space for the change set, not for the source.
A 4 TB source with 20 GB of daily change needs about 20 GB.

NoahsArk does not reimplement rsync. `sync` prints the exact command before it
runs it.

Mirror mode is Phase 2. Direct mode and the quick check are Phase 1.

### 18.5 Sources and excludes

A repository has one or more **source roots**. Each root is an absolute path.
The snapshot records every root and its exclude rules, so a restore knows what
the snapshot was meant to contain.

Defaults:

| Rule | Default | Reason |
|---|---|---|
| Multiple roots | Allowed | One repository can cover `/home` and `/srv`. |
| Root ordering | Sorted by path bytes | The snapshot must be deterministic. |
| Exclude syntax | gitignore-style patterns | It is familiar, and it is well specified. |
| Exclude sources | `config`, then `--exclude`, then a `.noahsarkignore` file in any directory | Local rules stay with the data. |
| Sources | Read-only. Never written. |
| Symlinks | Never followed | A followed symlink duplicates data and can leave the root. The link itself is stored. |
| Filesystem boundaries | Never crossed | A bind mount or a network mount would be pulled in silently. `sources.one_file_system`, default true. |
| Special files | Recorded by type, with no content | A device node or a socket has no bytes to store. |
| Unreadable file | Skipped, reported, exit code 1 | A backup must not fail silently and must not stop. |

An excluded path is not in the tree at all. The exclude rules are stored in the
snapshot as a TLV, so a later `ls` can explain why a file is absent.

### 18.6 In-flight change detection

A commit runs against a live source. A file may change while it is being read.
A chunk list built from such a file describes bytes that never existed together.

The rule is:

```
1. stat the file           -> (size_a, mtime_a, ctime_a)
2. read and chunk it
3. stat the file again     -> (size_b, mtime_b, ctime_b)
4. if size or mtime differ:
       mark the path "unstable"
       if the parent tree has an entry for this path:
             reuse the parent entry, unchanged
             discard the chunk list read in step 2
       else:
             keep the content as read
             set the UNSTABLE flag in the tree entry
       list the path in the commit report
       retry on the next commit
```

The two branches follow one rule: **a snapshot never holds torn content without
a flag that says so.**

For a file that already exists in the archive, the parent entry is the last
consistent version. Reusing it is always better than an inconsistent new one,
and the next commit picks the file up when it settles.

For a new file there is no earlier version. Dropping it would leave the path
absent with no record, which is worse than an inconsistent copy that is
labelled. The content is stored, and the entry carries the `UNSTABLE` flag of
section 8.5.2. `restore` warns on such an entry, and `ls` shows it.

`commit` exits with code 1 in both branches, and prints the count and the paths.

Continuous commits converge. A file that settles is read consistently on a later
commit, and the flagged version stays only in the snapshots that were taken
while the file moved.

A file that is unstable on many consecutive commits is a log file or a database.
The correct answer is a filesystem snapshot, not a longer retry.

Chunks that were already written to staging are kept in either branch. They are
content-addressed, so they cost nothing if the file settles, and GC removes them
if it does not.

### 18.7 Filesystem snapshots as the source

The recommended way to back up a live system is to commit a filesystem snapshot.
It removes in-flight changes entirely, because the snapshot does not change.

```bash
# LVM
lvcreate -L 10G -s -n snap /dev/vg0/data
mount -o ro /dev/vg0/snap /mnt/snap
noahsark commit --source /mnt/snap --source-root /srv/data
umount /mnt/snap && lvremove -f /dev/vg0/snap

# btrfs
btrfs subvolume snapshot -r /srv/data /srv/.snap-noahsark
noahsark commit --source /srv/.snap-noahsark --source-root /srv/data
btrfs subvolume delete /srv/.snap-noahsark

# ZFS
zfs snapshot tank/data@noahsark
noahsark commit --source /tank/data/.zfs/snapshot/noahsark --source-root /srv/data
zfs destroy tank/data@noahsark
```

`--source-root` records the original path in the snapshot, so a restore writes
to `/srv/data` and the temporary mount point never appears in the archive.

The quick check still works, because a filesystem snapshot preserves size, mtime
and ctime.

### 18.8 Scheduling

**NoahsArk has no built-in scheduler.** A backup tool that also schedules is two
programs in one, and every operating system already has a scheduler that is
better tested. `commit` is a batch job. Run it from cron or from a systemd
timer.

cron, daily at 02:00:

```cron
0 2 * * *  /usr/local/bin/noahsark commit --repo /srv/ark -q -m "nightly"
```

systemd, a service and a timer:

```ini
# /etc/systemd/system/noahsark-commit.service
[Unit]
Description=NoahsArk commit
After=local-fs.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/noahsark commit --repo /srv/ark -q -m nightly
Nice=10
IOSchedulingClass=idle
```

```ini
# /etc/systemd/system/noahsark-commit.timer
[Unit]
Description=NoahsArk nightly commit

[Timer]
OnCalendar=*-*-* 02:00:00
RandomizedDelaySec=30m
Persistent=true

[Install]
WantedBy=timers.target
```

`Persistent=true` runs a missed commit after a reboot. `IOSchedulingClass=idle`
keeps the commit out of the way of the live workload.

Two commits must never run at once on one repository. `commit` takes an
exclusive lock on the repository and exits with code 2 when it cannot get it.

Exit code 1, which means "some files were skipped", is normal on a live source.
A monitoring rule should alert on code 2 and on a rising unstable count, not on
code 1 alone.

### 18.9 Future watch trigger

Phase 1 has **manual `commit` only**. There is no daemon.

A watcher is Phase 3. It will record changed paths with `fsnotify` and append
them to a log, and `commit` will take the union of that log and the quick check.
It will never commit by itself, because a commit needs a stable filesystem.

Nothing has to change for it to arrive. `commit` is idempotent: committing an
unchanged tree produces the same snapshot root and writes no new object. The
watcher is therefore an accelerator for step 2 of the commit flow, and it
changes no format and no state.

### 18.10 Remote source roots: NFS and SMB

A source root may be an NFS or an SMB (CIFS) mount. That covers a NAS that
cannot run the binary. The mount is read like any other source, but it does not
provide everything a local filesystem provides. The specification states each
limit and the behaviour under it.

| Item | Local | NFS | SMB / CIFS | Behaviour |
|---|---|---|---|---|
| ctime | Reliable | Server-dependent | Not reliable | `source.quick_check` defaults to `size_mtime` for a remote root, `size_mtime_ctime` for a local one. `NO_CTIME` is set. |
| mtime granularity | 1 ns | 1 ns to 1 s | 1 s to 2 s, often rounded | A difference below `source.mtime_slack`, default 2 s, counts as equal. `MTIME_SLACK` is set. |
| `SEEK_HOLE` | Supported | Usually supported | Often unsupported | Fall back to reading the whole file. Zero chunks still deduplicate to one object, so the archive is unaffected. `NO_SPARSE` is set. |
| Link count | Exact | Usually exact | Often not exposed | Hardlink groups are not detected unless the mount reports `st_nlink` above 1. Each link becomes an independent file. `NO_HARDLINKS` is set. |
| uid, gid, mode | Real | Real with matching id maps | Often synthesized by the mount options | Recorded as seen. `SYNTHETIC_IDS` is set, and `restore` warns once. |
| Extended attributes and ACLs | Full | Partial | Rarely | Recorded when readable. Absent otherwise, and reported in the loss report. |
| Name case | Distinguished | Distinguished | Often not distinguished | Names are stored exactly as `readdir` returned them. Two names that differ only by case are both stored. `CASE_INSENSITIVE` is set, and a restore to a case-insensitive target warns about the collision. |
| In-flight change | Detected | Detected | Detected | The re-stat rule of section 18.6 is unchanged. |

Because the quick check is weaker on a remote root, a periodic full rehash is
required. `source.checksum_every`, default 30 days, makes `commit` behave as if
`--checksum` were given once the interval has passed. The snapshot records that
it was a checksum commit.

Every one of these facts is recorded in the snapshot's `source_type` and
`source_flags` fields (section 8.7), so a restore can explain what the archive
does and does not contain, years later, when the mount is gone.

Restoring **to** an NFS or SMB target follows the non-root metadata policy of
section 17.7: apply what the target accepts, report the rest, exit code 1.

### 18.11 Remote sources by commit bundle (Backlog)

A network protocol is a non-goal (section 2). A remote machine that can run the
binary does not need one. It exchanges directories instead, in the way that
`git bundle` exchanges commits.

The bundle path is better than an NFS or SMB mount whenever it is available.
The scan then runs locally on the machine that owns the data. ctime is reliable,
hardlinks are visible, and extended attributes are readable. Only new objects
cross the network.

```
                 server (repository)                source machine
                 -------------------                --------------
  1.  noahsark catalog export /tmp/cat
  2.                             --- rsync /tmp/cat --->
  3.                                                 noahsark commit /srv/data \
                                                        --catalog /tmp/cat \
                                                        --out /tmp/bundle
  4.                             <--- rsync /tmp/bundle ---
  5.  noahsark import /tmp/bundle
  6.  noahsark pack ; noahsark burn --exec
```

**Step 1, `catalog export`.** The server writes the current catalog as plain
files: every run filter, the recent manifests, every snapshot object, and the
disc directory. It is the same content that a run carries (section 12.7), in the
same formats.

**Step 3, `commit --out`.** The source machine walks its own filesystem, chunks
and hashes locally, and queries the exported filters. A filter negative is a
proof of absence, so the chunk is new and goes into the bundle. A filter
positive can only be confirmed when the exported catalog holds the matching
manifest; without it, the chunk is included. That wastes a little space and
never drops data, which is the rule of section 12.8.

The bundle directory uses the staging object layout plus one header file:

```
/tmp/bundle/
    BUNDLE.bin                  header, see below
    objects/<ab>/<name>         new chunks and bundles
    trees/<ab>/<name>           new trees and chunklists
    snapshots/<name>            the new snapshot object
```

**Step 5, `import`.** The server verifies the content id of every file, drops
every object it already has, enters the rest into the staging state machine as
STAGED, and records the snapshot and the ref move. Import is idempotent:
importing the same bundle twice changes nothing, because the second pass finds
every object present.

The bundle travels by rsync, ssh, scp, or a USB disk. Nothing in the format
depends on how it arrived.

#### 18.11.1 Bundle header

`BUNDLE.bin`, 256 bytes, magic `"NABN"`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NABN"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 16 | u8[16] | `repo_uuid` | The repository this bundle belongs to. Import refuses a mismatch. |
| 40 | 32 | u8[32] | `parent_snapshot` | The snapshot the source deduplicated against. All zero when none. |
| 72 | 32 | u8[32] | `snapshot_id` | The snapshot object this bundle carries. |
| 104 | 32 | u8[32] | `catalog_hash` | Hash of the exported catalog that was used. Import warns when it is older than the server's current catalog. |
| 136 | 8 | u64 | `object_count` | Files under `objects/`, `trees/` and `snapshots/`. |
| 144 | 8 | u64 | `total_bytes` | Sum of the stored object file sizes. |
| 152 | 8 | u64 | `uncompressed_bytes` | Sum of the payload sizes. |
| 160 | 8 | i64 | `created_sec` | Bundle creation time. |
| 168 | 8 | u64 | `filter_positive_unconfirmed` | Chunks included because no manifest was available to confirm a filter hit. |
| 176 | 1 | u8 | `hash_algo` | Multicodec code of every id in the bundle. |
| 177 | 1 | u8 | `chunker_profile` | Chunker profile id. |
| 178 | 1 | u8 | `source_type` | Source type of the machine that wrote the bundle (section 8.7). |
| 179 | 1 | u8 | `source_flags` | Source flags of that machine. |
| 180 | 4 | u32 | `host_len` | Byte length of `host`. |
| 184 | 64 | u8[64] | `host` | UTF-8 host name, zero-padded. Diagnostic only. |
| 248 | 4 | u32 | `reserved_u32` | Zero. |
| 252 | 4 | u32 | `header_crc32c` | CRC-32C of bytes 0 to 251. |

Commit bundles are **Backlog**: specified here and reserved in the format, but
not scheduled. `BUNDLE.bin` and the snapshot `source_type` value 5 are reserved
so that building this later changes no structure. NFS and SMB mounts
(section 18.10) stay supported, and they cover the case a NAS presents, because
a NAS cannot run the binary.

A future live protocol would only automate the transfer of a bundle. It would
change no on-disc structure.

### 18.12 Deployment modes for a remote data host

Three ways to back up a machine that holds the data but is not the machine that
holds the discs.

| | A: mount and commit | B: hybrid listing | C: commit bundle |
|---|---|---|---|
| Software on the data host | None | ssh and `find` | The NoahsArk binary |
| Network traffic | Every read file, plus the whole directory walk | The listing, plus the changed files | The listing is local; only new objects cross |
| Directory walk speed | Slow. One round trip per `stat` | Fast. One `find` on the data host | Fast. Local walk |
| Metadata fidelity | Limited by the mount (section 18.10) | Limited by the mount for content, exact for the listing | Full: ctime, hardlinks, xattrs, ACLs |
| CPU location | The repository server | The repository server | The data host |
| Temporary space | None | The listing, a few MB | The bundle, the size of the change set |
| Phase | 1 | 2 | Backlog |

Mode A mounts the source over NFS and runs `commit` normally. Prefer NFS over
SMB: NFS keeps link counts, extended attributes and often ctime, and SMB keeps
few of them.

Mode B runs one command on the data host, over ssh, to produce a stat listing:

```bash
ssh nas "find /srv/data -printf '%y\t%s\t%T@\t%C@\t%m\t%U\t%G\t%p\n'"
```

The listing is diffed against the parent snapshot's trees, exactly as `sync`
does (section 18.4), and only the changed files are read over the mount. It
removes the per-file round trips of the walk, which is what makes mode A slow on
a large tree. It needs no binary on the data host.

Mode C runs the binary on the data host and produces commit bundles
(section 18.11). It is Backlog, so it is not available today.

**Recommendation.** Use mode A. Move to mode B when the directory walk dominates
the commit time. Mode C is the answer when full metadata matters, or when
hardlinks, extended attributes or ACLs must survive, and it is the reason the
bundle format stays reserved.

---

## 19. CLI reference

Every command carries a phase tag. Phase 1 is the minimum viable tool. A Phase 1
build must refuse a Phase 2 or Phase 3 option with a clear message, not ignore
it.

| Command | Phase |
|---|---:|
| `init` | 1 |
| `commit` | 1 |
| `pack` | 1 |
| `burn --print`, `burn --exec` | 1 |
| `close` | 1 |
| `verify`, `verify --heal` | 1 |
| `scrub` | 1 |
| `health` | 1 |
| `plan` | 1 |
| `restore` | 1 |
| `rebuild-cache` | 1 |
| `gc` | 1 |
| `ls`, `log` | 1 |
| `disc list`, `disc label`, `disc mark-degraded` | 1 |
| `image build`, `image diff`, `image mount` | 1 (`image diff` is used by Phase 2) |
| `sync` | 2 |
| `catalog export` | Backlog |
| `import` | Backlog |
| `append` | 2 |
| `watch` | 3 |
| `consolidate` | 3 |
| `reindex` | 3 |

Every command accepts these global options:

| Option | Meaning |
|---|---|
| `--repo=PATH` | Repository root. Defaults to the discovered `.noahsark` directory. |
| `--cache-dir=PATH` | Override the cache location. |
| `--config=PATH` | Override the config file. |
| `--json` | Machine-readable output. |
| `-v`, `--verbose` | More output. |
| `-q`, `--quiet` | Errors only. |
| `--dry-run` | Compute and print, change nothing. Not every command supports it. |

Common exit codes:

| Code | Meaning |
|---:|---|
| 0 | Success. |
| 1 | Success with a warning, or partial success. |
| 2 | Failure. |
| 3 | A required disc or file is missing. |
| 4 | A precondition failed, for example an unpatched burner. |

### 19.1 `noahsark init` (Phase 1)

```
noahsark init [--repo=PATH] [--hash=blake3|sha256] [--chunker=P3|P4|P5]
              [--fs-profile=0|1|2] [--preset=dedup|balanced|locality|standalone]
```

Creates the repository: the config file, the staging store, the state log, and a
new `repo_uuid`. Writes the defaults of section 20.

Exit: 0 on success, 2 when the directory already holds a repository.

### 19.2 `noahsark commit` (Phase 1)

```
noahsark commit [SOURCE]... [-m MESSAGE] [--ref=NAME] [--checksum]
                [--exclude=PATTERN]... [--one-file-system=BOOL]
                [--source=PATH] [--source-root=PATH] [--from=PATH]
                [--copy-first] [--retry-unstable=N]
                [--out=DIR --catalog=DIR] [--source-type=TYPE]
```

Runs the commit flow of section 18.1. With no `SOURCE`, it uses the configured
source roots.

| Option | Meaning |
|---|---|
| `-m` | Commit message, stored as a snapshot TLV. |
| `--ref` | The ref to move. Default `LATEST`. |
| `--checksum` | Disable the quick check. Read and rehash every file. `--full-scan` is an alias. |
| `--from` | Read changed files from this mirror directory. Unchanged paths come from the parent tree. Phase 2. |
| `--source` | Read from this path, typically a filesystem snapshot mount. |
| `--source-root` | The original path to record in the snapshot. Use it with `--source` or `--from`. |
| `--copy-first` | Copy each changed file raw into staging, then chunk it there. Shortens the busy window on the source disk. Phase 2. |
| `--out` | Write a commit bundle to this directory instead of into staging. Backlog. |
| `--catalog` | The exported catalog to deduplicate against, with `--out`. Backlog. |
| `--source-type` | Override the detected source type recorded in the snapshot. |
| `--retry-unstable` | Re-read an unstable file up to N times before the rule of section 18.6 applies. Default 1. |
| `--exclude` | Skip matching paths. The patterns are recorded in the snapshot. |
| `--one-file-system` | Do not cross a mount point. Default true. |

Exit: 0 on success, 1 when some files could not be read or were unstable, 2 on
failure or when the repository lock is held.

The report lists every unstable path and says which branch was taken: `parent`
when the parent entry was reused, or `flagged` when new content was stored with
the `UNSTABLE` flag.

### 19.2a `noahsark sync` (Phase 2)

```
noahsark sync SOURCE [--mirror=PATH] [--ref=NAME] [--dry-run]
             [--list-only] [--rsync-arg=ARG]...
```

Pulls the change set of `SOURCE` into the mirror directory (section 18.4).
`SOURCE` may be a local path or an `rsync` or ssh remote.

| Option | Meaning |
|---|---|
| `--mirror` | Mirror directory. Default `sync.mirror_dir`, else `<staging>/mirror`. |
| `--ref` | The ref whose snapshot is the comparison base. Default `LATEST`. |
| `--list-only` | Print the changed-path list and the byte total. Transfer nothing. |
| `--dry-run` | Print the `rsync` command and the list size. Transfer nothing. |
| `--rsync-arg` | Pass an extra argument through to `rsync`. |

It prints the exact `rsync` command before it runs it.

Exit: 0 on success, 1 when some paths could not be listed, the `rsync` exit code
on a transfer failure, 2 when `rsync` is not installed.

### 19.2b `noahsark catalog export` (Backlog)

```
noahsark catalog export DIR [--manifests=N] [--json]
```

Writes the current catalog to `DIR` as plain files, for a source machine that
will run `commit --out` (section 18.11). `--manifests` sets how many recent
manifests to include, as section 12.7 defines them. Default
`manifest.history_depth`.

Exit: 0 on success, 2 on failure.

### 19.2c `noahsark import` (Backlog)

```
noahsark import BUNDLE_DIR [--ref=NAME] [--dry-run] [--keep]
```

Verifies and imports a commit bundle. Every object's content id is checked
before it enters staging. Objects already present are dropped. The snapshot is
recorded and the ref is moved.

| Option | Meaning |
|---|---|
| `--ref` | The ref to move. Default the ref named in the bundle, else `LATEST`. |
| `--dry-run` | Verify and report the counts. Import nothing. |
| `--keep` | Do not delete the bundle directory afterwards. |

Import is idempotent. Importing the same bundle twice imports nothing the second
time.

Exit: 0 on success, 1 when some objects were already present, 2 on a
verification failure or a repository uuid mismatch.

### 19.3 `noahsark watch` (Phase 3)

```
noahsark watch [SOURCE]... [--log=PATH]
```

Runs a change-recording daemon (section 18.9). It records changed paths and
never commits. It does not exist in Phase 1 or Phase 2.

Exit: 0 on a clean stop, 2 on failure.

### 19.4 `noahsark pack` (Phase 1)

```
noahsark pack [--disc=UUID] [--media=BD-R-25|BD-R-50|BD-R-100|BD-R-128|image]
              [--capacity=BYTES|GiB] [--reserve=BYTES|PERCENT]
              [--extra-reserve=BYTES|PERCENT] [--label=TEXT]
              [--preset=NAME] [--now] [--close] [--dry-run]
```

Selects objects for the next run, applies the locality rules of section 15,
builds the run image and the parity, and writes the burn plan.

**Trigger.** Under profile 0 a disc holds one run, so `pack` waits until a
disc's worth of data exists. It produces a run only when one of these holds:

- staging holds at least `disc.min_fill` of the data budget, default 0.90;
- the oldest STAGED object is older than `disc.max_wait`, default 30 days;
- `--now` is given.

Otherwise `pack` prints how much more data, or how much more time, is needed,
and exits 1.

| Option | Meaning |
|---|---|
| `--disc` | Continue an existing disc. Without it, `pack` starts a new disc. |
| `--media` | Media type for a new disc. |
| `--capacity` | Forced capacity for a new disc (section 9.12). It must be at or below the reported capacity. It cannot be changed later. |
| `--reserve` | `disc.force_reserve` for this disc. Replaces the computed reserve. |
| `--extra-reserve` | `disc.extra_reserve` for this disc. Added to the computed reserve. |
| `--label` | Human label, printed on the disc. |
| `--preset` | Locality preset for this run. |
| `--now` | Ignore `disc.min_fill` and `disc.max_wait`. |
| `--close` | Seal the disc: `spare:none` and `-dvd-compat`, no POW, full capacity, no later append. Permanent, recorded in the superblock. |
| `--dry-run` | Print the capacity budget of section 10.11.2 and stop. |

Exit: 0 on success, 1 when the run is smaller than requested, 2 on failure,
4 when a forced capacity conflicts with the recorded value.

### 19.5 `noahsark append` (Phase 2)

```
noahsark append --disc=UUID [pack options]
```

A convenience form of `pack --disc=UUID`. It refuses a disc that is closed, and
it refuses a disc marked `append-raw-only` unless `--raw` is given.

Exit: as `pack`. 4 when the disc is closed.

### 19.6 `noahsark burn` (Phase 1)

```
noahsark burn --run=SEQ (--print | --exec) [--verify | --no-verify]
              [--device=PATH]
              [--backend=growisofs|cdrskin|kernel|imgburn|hdiutil]
              [--speed=N] [--yes]
```

Renders or runs the burn plan of section 10.7.

| Option | Meaning |
|---|---|
| `--print` | Write the command lines to standard output. Touch no device. Available on every platform. |
| `--exec` | Run exactly those command lines. **Linux only.** |
| `--device` | Override the device hint in the plan. |
| `--backend` | Override the burner backend. |
| `--speed` | Override the speed. |
| `--yes` | Do not ask for confirmation before the first write. |
| `--verify` / `--no-verify` | Run `verify` after the burn. Default from `burn.verify_after`, which is true. |

Exactly one of `--print` and `--exec` must be given.

`--exec` ejects and reloads the disc after the burn, then runs `verify`. It uses
a second drive when one is configured and present. Only a successful `verify`
moves the run's objects from BURNED to CLEAN (section 14.2). With `--no-verify`
the objects stay BURNED, and staging is not released until `verify` runs later.

Exit codes:

| Code | Meaning |
|---:|---|
| 0 | Success. |
| 2 | The burn failed. |
| 3 | The expected disc is not in the drive. |
| 4 | The burner version is unknown or unpatched, or `--exec` ran on a platform other than Linux. |

### 19.7 `noahsark close` (Phase 1)

```
noahsark close --disc=UUID [--parity] [--print | --exec] [--yes]
```

Closes a disc. It writes a final closing run with a fresh catalog copy, the tail
anchors when they are missing, an optional disc-wide parity run, and the closing
write with `-dvd-compat`.

A disc is never closed automatically under the default `disc.close_policy` of
`never` (section 9.9).

| Option | Meaning |
|---|---|
| `--parity` | Add a disc-wide parity run over all data columns of all runs. |

Exit: 0 on success, 2 on failure, 4 when the disc is already closed.

### 19.8 `noahsark verify` (Phase 1)

```
noahsark verify [--disc=UUID] [--run=SEQ] [--image=PATH]
                [--level=catalog|connectivity|integrity]
                [--heal] [--drive=PATH] [--report=FILE]
```

Reads a disc or an image back and checks it. On success it moves the run's
objects to CLEAN.

| Option | Meaning |
|---|---|
| `--level=catalog` | Cache consistency only. Reads no disc. |
| `--level=connectivity` | Reads trees, chunklists, snapshots, filters and manifests. Under 1 percent of the bytes. |
| `--level=integrity` | Reads every sector and every object. The default for a disc. |
| `--heal` | Repair what is repairable, in the order of section 11.7. Write recovered objects into `staging/heal/`. |
| `--drive` | Use this drive. Use a second drive model for the 24-hour check. |
| `--report` | Write the health record as JSON. |

Exit: 0 clean, 1 repaired or degraded, 2 unrecoverable loss, 3 disc missing.

### 19.9 `noahsark scrub` (Phase 1)

```
noahsark scrub [--due] [--all] [--disc=UUID]... [--drive=PATH]
```

Runs `verify --level=integrity` over the discs that the schedule of section 11.8
selects. `--due` selects only overdue discs. `--all` selects every disc.

Exit: as `verify`, aggregated over the discs.

### 19.10 `noahsark health` (Phase 1)

```
noahsark health [--disc=UUID] [--library] [--object=ID] [--json]
```

Prints the health report of section 11.9: RS margin, status, spare area
remaining, forced capacity, open or closed, scrub dates, and the consolidation
trigger metrics of section 15.8.

Exit: 0 when every disc is healthy, 1 when any disc is degraded, 2 when any disc
is failed.

### 19.11 `noahsark plan` (Phase 1)

```
noahsark plan SNAPSHOT [--target=PATH] [--out=FILE] [--drives=N]
              [--staging-budget=BYTES] [--score=bytes|objects]
```

Computes the restore plan of section 16.4 and prints it. It writes the JSON plan
to `--out`. It reads nothing from a disc beyond the catalog.

Exit: 0 when the plan is complete, 3 when a required disc is missing from the
inventory.

### 19.12 `noahsark restore` (Phase 1)

```
noahsark restore SNAPSHOT TARGET [--plan=FILE] [--include=PATH]...
                 [--drives=N] [--staging-budget=BYTES] [--interactive]
                 [--no-eject] [--overwrite]
                 [--no-owner] [--numeric-owner] [--no-xattr]
                 [--xattr-exclude=PATTERN] [--no-acl] [--no-flags]
                 [--no-times] [--no-hardlinks] [--metadata-strict]
                 [--report=FILE] [--report-replay=FILE] [--strict-unstable]
```

Runs the restore pipeline of section 16.8. `--plan` resumes a persisted plan.

An entry with the `UNSTABLE` flag is restored, and a warning names the path.
`--strict-unstable` refuses to write such an entry and reports it as missing
(section 17.10).

Exit: 0 all applied, 1 data restored with metadata loss, 2 data restore failed,
3 a required disc is missing.

### 19.13 `noahsark rebuild-cache` (Phase 1)

```
noahsark rebuild-cache [--level=1|2|3] [--snapshot=ID] [--from-disc]
```

Rebuilds the local cache from discs, as in section 13.2. Level 1 needs the
newest disc alone. Level 3 asks for every disc, newest first, and reads only
manifests.

Exit: 0 on success, 1 when the rebuild is partial, 3 when a needed disc is
missing.

### 19.14 `noahsark consolidate` (Phase 3)

```
noahsark consolidate [--snapshot=ID] [--dry-run] [--media=TYPE]
```

Packs a fresh, self-contained disc set for a snapshot, with no reference to
older runs. `--dry-run` reports the disc count and the media cost.

Exit: 0 on success, 2 on failure.

### 19.15 `noahsark reindex` (Phase 3)

```
noahsark reindex --to=blake3|sha256 [--disc=UUID]... [--all]
```

Builds the optional cross-algorithm side table of section 5.7. It reads the data
areas of the named discs once.

Exit: 0 on success, 1 when some discs were not available, 2 on failure.

### 19.16 `noahsark gc` (Phase 1)

```
noahsark gc [--dry-run] [--force-after=DURATION]
```

Deletes staging objects that are GC-ELIGIBLE, under the rules of section 14.4.
`--force-after` shortens the retention for this run only and requires an
interactive confirmation.

Exit: 0 on success, 1 when nothing was eligible, 2 on failure.

### 19.17 `noahsark ls` (Phase 1)

```
noahsark ls SNAPSHOT [PATH] [--long] [--recursive] [--json] [--unstable-only]
```

Lists a snapshot's tree. `--long` prints mode, owner, size and mtime. It reads
tree objects only, never chunks.

An entry with the `UNSTABLE` flag is marked with a `!` in the first column, and
with `"unstable": true` under `--json`. `--unstable-only` lists just those
entries.

Exit: 0 on success, 3 when a needed tree object is unavailable.

### 19.18 `noahsark log` (Phase 1)

```
noahsark log [REF|SNAPSHOT] [--limit=N] [--json]
```

Walks the snapshot chain by parent pointer and prints the history. It reads the
snapshot table from the cache or from the newest disc.

Exit: 0 on success.

### 19.19 `noahsark disc` (Phase 1)

```
noahsark disc list [--json]
noahsark disc label UUID TEXT
noahsark disc mark-degraded UUID [--reason=TEXT]
```

`list` prints every disc: uuid, seq, label, shelf note, media type, filesystem
profile, reported capacity, forced capacity, used, run count, open or closed,
health, RS margin, spare remaining, and last verify date.

`label` sets the human label and the shelf note in the local disc directory. The
on-disc label is written at burn time and never changes.

`mark-degraded` records a manual health downgrade, for example after a physical
inspection.

Exit: 0 on success, 3 when the uuid is unknown.

### 19.20 `noahsark image` (Phase 1)

```
noahsark image build --run=SEQ --out=FILE
noahsark image diff --old=FILE --new=FILE [--out=FILE] [--block=32768]
noahsark image mount FILE MOUNTPOINT [--rw]
```

Test and development commands. `build` writes the run image to a file instead of
a device. `diff` computes the 32 KiB block diff of variant 1b and writes the
changed runs. `mount` loop-mounts an image.

These commands are what let the whole burn path run in CI with no drive.

Exit: 0 on success, 2 on failure.

---

## 20. Configuration reference

A key is Phase 1 unless the table names a later phase. A build must refuse an
unknown key, or a key of a later phase than it implements, with a clear message
that names the key and the phase.

Later-phase keys:

| Phase | Keys |
|---:|---|
| 2 | `commit.copy_first`, `sync.*`, `fs.append_variant`, `disc.spare`, `disc.min_spare_ratio`, `disc.close_policy`, `disc.allow_raw_append`, `disc.spare_reserve_bytes`, `metadata.xattr`, `metadata.acl`, `metadata.windows` |
| 3 | `fec.disc_close_parity`, `fec.group_size`, `consolidate.*`, `restore.drives` above 1, `watch.*`, `mirror.*`, `reindex.*` |
| Backlog | `commitbundle.*` |

The config file lives at `<repo>/config`. It is a plain text key-value file with
one `key = value` pair per line, `#` for a comment, and UTF-8 encoding. A CLI
option always overrides the file.

### 20.1 Identity and format

| Key | Default | Meaning |
|---|---|---|
| `repo.uuid` | generated | Repository uuid. Never changed. |
| `format.version_major` | 1 | On-disc format major version. |
| `format.version_minor` | 0 | On-disc format minor version. |

### 20.2 Hashing and chunking

| Key | Default | Meaning |
|---|---|---|
| `hash.current` | `blake3` | Algorithm for new objects. `blake3` or `sha256`. |
| `chunker.profile` | `P4` | Chunker profile: `P3`, `P4`, `P5`. |
| `chunker.gear_table_id` | 1 | Frozen Gear table version. Never change it under a profile name. |
| `bundle.threshold` | 1 MiB | Files below this size go into a bundle. |
| `bundle.target_size` | 64 MiB | Target bundle size. |
| `chunklist.inline_max` | 64 | Above this many chunks, a file references a chunklist object. |
| `tree.tlv_spill_threshold` | 4 KiB | A TLV area above this size spills into chunks. |

### 20.3 Compression

| Key | Default | Meaning |
|---|---|---|
| `compression.algorithm` | `zstd` | `none`, `zstd`, `lz4`. |
| `compression.level` | 3 | Algorithm level. |
| `compression.min_gain` | 0.05 | Store uncompressed when compression saves less than this fraction. |

### 20.4 Disc and filesystem

| Key | Default | Meaning |
|---|---|---|
| `fs.profile` | 0 | Disc filesystem profile: 0 one-shot (Phase 1, default), 1 UDF 2.01 POW (Phase 2), 2 ISO 9660:1999 level 4 POW (Phase 3). |
| `fs.append_variant` | `1b` | Phase 2. Profile 1 append variant: `1a` kernel direct write, `1b` image mirror and block diff. |
| `fs.fanout_levels` | 1 | Object fan-out depth. 2 is allowed under profile 1 and profile 0 only. |
| `udf.revision` | `2.01` | UDF revision for profile 1. |
| `disc.fill_ratio` | 0.95 | Fraction of the forced capacity that data may use. |
| `disc.min_fill` | 0.90 | `pack` triggers when staging fills this fraction of a disc's data budget. |
| `disc.max_wait` | 30 days | `pack` triggers when the oldest STAGED object is older than this, whatever the fill. |
| `disc.force_capacity` | unset | Cap the usable capacity of a disc below the reported value. Bytes or GiB. Recorded in the superblock. |
| `disc.force_reserve` | unset | Replace the computed reserve. Bytes or a percentage. |
| `disc.extra_reserve` | unset | Add to the computed reserve. Bytes or a percentage. |
| `disc.expected_runs` | 32 | Expected number of future runs, used by the catalog growth term of the reserve formula. |
| `disc.spare` | `min` | Spare area size at format time: `min` or `default`. `default` reserves about 256 MB and gives more appends. |
| `disc.spare_reserve_bytes` | 512 MiB | Reserve for POW spare and filesystem metadata on an appendable disc. |
| `disc.min_spare_ratio` | 0.20 | Warn and recommend no further appends below this remaining spare fraction. |
| `disc.close_policy` | `never` | `never`, `when_full`, `manual`, or `always`. See section 9.9. |
| `disc.allow_raw_append` | true | Allow the degraded raw append mode of section 9.11. |

### 20.5 Burner

| Key | Default | Meaning |
|---|---|---|
| `burner.backend` | `growisofs` | `growisofs`, `cdrskin`, `kernel`, `imgburn`, `hdiutil`. |
| `burner.device` | `/dev/sr0` | Default device hint in the burn plan. |
| `burner.speed` | 4 | Speed multiplier for BD-R. |
| `burner.speed_mdisc` | 2 | Speed multiplier for M-DISC. |
| `burner.require_patched` | true | Refuse `--exec` with an unknown or unpatched dvd+rw-tools build. |
| `burner.template.<backend>.<kind>` | see section 10.8 | Command template. A user may override any of them. |
| `burner.eject_after` | true | Eject after a successful write. |
| `burn.verify_after` | true | Run `verify` after `burn --exec`. Only `verify` moves objects to CLEAN. |
| `burn.verify_device` | unset | Second drive for the verification read. Used only when present. |
| `burn.reload_seconds` | 10 | Wait after the reload before the verification read. |

### 20.6 FEC

| Key | Default | Meaning |
|---|---|---|
| `fec.scheme` | `rs255-gf8` | FEC scheme registry. |
| `fec.k` | 231 | Data columns. `k + 1 + m` must equal 255. |
| `fec.m` | 23 | Parity columns. |
| `fec.band_stripes` | 2048 | Stripes per encoding band, for a working set near 1 GiB. |
| `fec.disc_close_parity` | false | Add a disc-wide parity run at close. |
| `fec.group_size` | 0 | Discs per cross-disc parity group. 0 disables it. Use 10 with one parity disc, or 20 with two. |
| `fec.reburn_margin` | 0.50 | Re-burn a disc below this RS margin. |

### 20.7 Filters and manifests

| Key | Default | Meaning |
|---|---|---|
| `filter.type` | `binaryfuse16` | Run filter type. |
| `manifest.fanout_bits` | 8 | 8 or 16. Use 16 above a few million objects per run. |
| `manifest.history_depth` | 8 | How many earlier runs' manifests every run carries. |
| `filter.rollup_threshold` | 64 MiB | Merge old filters into super filters above this bundle size. Reserved. |
| `catalog.max_bytes` | 512 MiB | Cap on one run's catalog. Only the manifest history is dropped to meet it. |
| `catalog.snapobj_pack_threshold` | 1000 | Above this snapshot count, pack the replicated snapshot objects into one container file. |

### 20.7a Sources and excludes

| Key | Default | Meaning |
|---|---|---|
| `sources.root` | unset, repeatable | An absolute source root. At least one is required. |
| `sources.exclude` | unset, repeatable | A gitignore-style exclude pattern. |
| `sources.ignore_file` | `.noahsarkignore` | Per-directory exclude file name. Empty disables it. |
| `sources.one_file_system` | true | Do not cross a mount point. |
| `sources.follow_symlinks` | false | Never true in Phase 1. A symlink is stored as a symlink. |
| `sources.skip_unreadable` | true | Skip and report an unreadable file. Exit code 1. |
| `sources.read_only` | true | Read-only. A source is never written. The key is reported, never set. |
| `source.quick_check` | `size_mtime_ctime` local, `size_mtime` remote | Fields compared against the parent tree entry. |
| `source.mtime_slack` | 2 s | An mtime difference below this counts as equal. Set 0 for a local root. |
| `source.checksum_every` | 30 days | Force a full rehash on a remote root after this interval. 0 disables it. |
| `source.type` | auto | Override the detected source type: `local`, `snapshot`, `nfs`, `smb`. |
| `source.allow_smb` | true | Allow an SMB mount as a source root. It is never allowed for staging. |
| `commit.quick_check` | true | Use the size, mtime and ctime comparison of section 18.2. |
| `commit.checksum` | false | Always rehash. Equivalent to `--checksum` on every commit. |
| `commit.restat_after_read` | true | In-flight change detection (section 18.6). Never set it to false on a live source. |
| `commit.retry_unstable` | 1 | Re-reads of an unstable file before it is skipped. |
| `commit.copy_first` | false | Copy changed files into staging before chunking. Phase 2. |
| `commit.lock_timeout` | 0 | Seconds to wait for the repository lock. 0 means fail at once. |
| `sync.rsync_path` | `rsync` | Path to the `rsync` binary. Phase 2. |
| `sync.rsync_args` | `-aHAX --numeric-ids` | Recommended option set for `sync`. `--files-from` is always added. Phase 2. |
| `sync.mirror_dir` | `<staging>/mirror` | Mirror root on the staging disk. Phase 2. |
| `sync.clear_after_commit` | true | Delete the mirror contents once the objects reach STAGED. Phase 2. |
| `sync.remote_stat_command` | built-in | The command used to obtain a stat listing from a remote source. Phase 2. |
| `label.template` | `<repo-short-name>-<seq:04d> <YYYY-MM>` | Physical label text, mirrored into the disc directory. |
| `repo.short_name` | from `repo.uuid` | Short name used in the label. Up to 16 characters. |
| `ref.max_name_bytes` | 40 | Longest ref name. The ref record is fixed width, so the limit is part of the format. |

### 20.8 Locality and packing

| Key | Default | Meaning |
|---|---|---|
| `locality.preset` | `balanced` | `dedup`, `balanced`, `locality`, `standalone`. |
| `locality.max_source_runs` | 8 | Capping bound per segment. |
| `locality.segment_size` | 1 GiB | Capping segment size. |
| `locality.rewrite_below_chunks` | 64 | Do not keep a source run that supplies fewer chunks than this. |
| `locality.max_duplicate_bytes_per_file` | 64 MiB | Per-file cap on rewritten bytes. |
| `locality.max_duplicate_ratio_per_file` | 0.05 | Per-file cap as a fraction. |
| `locality.disc_budget` | 0.03 | Per-disc cap on duplicated bytes. |
| `split.threshold` | 0.25 | Split a file only when it exceeds the remaining capacity by more than this fraction of a disc. |
| `consolidate.max_plan_discs` | 20 | Consolidation trigger. |
| `consolidate.max_spread_ratio` | 2.0 | Consolidation trigger. |
| `consolidate.max_restore_hours` | 8 | Consolidation trigger. |
| `consolidate.max_disc_age` | 5 years | Consolidation trigger. |

### 20.8a Metadata

| Key | Default | Meaning |
|---|---|---|
| `metadata.user_group_names` | true | Store user and group names beside the numeric ids. |
| `metadata.atime` | false | Storing atime makes almost every tree change on every commit. Phase 2. |
| `metadata.btime` | false | Store birth time when the source reports it. Linux cannot set it on restore, so it is informational there. Phase 2. |
| `metadata.xattr` | false | Phase 2. |
| `metadata.acl` | false | Phase 2. |
| `metadata.windows` | false | Windows attributes and security descriptors. Phase 2. |
| `restore.owner_policy` | `auto` | `auto`, `numeric`, `name`, `none`. See section 17.3. |

### 20.9 Staging and cache

| Key | Default | Meaning |
|---|---|---|
| `staging.dir` | `<repo>/staging` | Staging store location. |
| `staging.retain_after_clean` | 7 days | Retention before an object becomes GC-eligible. |
| `staging.budget_bytes` | unset | Warn when staging exceeds this size. |
| `staging.allow_unsafe_fs` | false | Allow staging on a filesystem that fails the startup check, such as SMB. Recorded in the state log. |
| `commitbundle.dir` | `<staging>/commitbundles` | Where `import` unpacks and where `commit --out` writes by default. Backlog. |
| `commitbundle.keep_after_import` | false | Keep the bundle directory after a successful import. Backlog. |
| `commitbundle.catalog_max_age` | 30 days | Warn when `commit --out` uses an exported catalog older than this. Backlog. |
| `cache.dir` | `~/.cache/noahsark/<repo-uuid>` | Local cache location. |
| `cache.format_version` | 1 | Delete and rebuild on a mismatch. |

### 20.10 Restore

| Key | Default | Meaning |
|---|---|---|
| `restore.staging_budget` | 16 GiB | Peak staging allowed. The planner falls back to multi-pass above it. |
| `restore.score` | `bytes` | Greedy score: `bytes` or `objects`. |
| `restore.drives` | 1 | Number of drives to plan for. |
| `restore.rate_mb_s` | 20 | Effective read rate for the time model. |
| `restore.switch_seconds` | 60 | Fixed cost per disc switch. |
| `restore.interactive` | false | Prompt on every disc, not only on a mismatch. |
| `restore.eject` | true | Eject after each disc. |

### 20.11 Scrub

| Key | Default | Meaning |
|---|---|---|
| `scrub.first_check_hours` | 24 | First full verify after burning, on a second drive. |
| `scrub.schedule` | `3m,12m,then 12m to 5y,then 6m` | The schedule of section 11.8. |
| `scrub.degraded_interval` | 3 months | Interval for a degraded disc. |
| `scrub.max_disc_age` | 10 years | Proactive re-burn age. |

---

## 21. Format evolution and compatibility

### 21.1 The two mechanisms

Every change uses one of two mechanisms.

| Mechanism | When | Old reader | New reader |
|---|---|---|---|
| A feature bit | A structure gains an item. | Ignores an `optional_feat` bit. Refuses a `required_feat` bit. | Uses the item. |
| A version bump | A structure changes shape. | Refuses an unknown `version_major`. Ignores an unknown `version_minor` and skips `header_len - known`. | Uses the new shape. |

A registry id is never reused and never renumbered. That is what lets a 2050
reader interpret a 2027 disc.

### 21.2 Change matrix

| Change | Mechanism | Old reader does | New reader does |
|---|---|---|---|
| **New hash algorithm** | New id in the hash registry. `hash.current` moves. | Refuses an object whose `hash_algo` it does not know, and says the code. Old discs stay readable. | Reads both. Writes the new default. Dedup across the boundary is zero unless the reindex table exists. |
| **Chunker profile change** | New id in the chunker registry. | Unaffected. A reader never needs the profile. | Uses the new profile for new runs. Old runs keep theirs. Dedup across the boundary falls. |
| **Gear table change** | New `gear_table_id` **and** a new profile name. | Unaffected. | Must never reuse an existing profile name with a different table. That would silently split the object space. |
| **New compression algorithm** | New id in the compression registry, plus `required_feat` bit `FEAT_COMPRESSION` semantics. | Refuses an object whose `compression` it does not know. The object is unreadable, not misread. | Reads it. |
| **FEC parameter change (k, m)** | Recorded per run in the run header. | Reads any `k` and `m`, because both are in the header. | Same. |
| **New FEC scheme** | New id in the FEC registry, plus a `required_feat` bit. | Refuses the run for repair, but still reads its objects through the filesystem. | Repairs it. |
| **UDF revision change** | `udf.revision` and `fs_revision` in the superblock. | Depends on the operating system, not on NoahsArk. | Same. |
| **Disc filesystem profile change** | New id in the profile registry. | Refuses a profile it does not know, and says the id. | Reads it. |
| **New object kind** | New id in the object kind registry, plus a `required_feat` bit on the containing structure. | Refuses the structure. | Reads it. |
| **New tree TLV** | New id in the TLV registry. Critical bit decides. | Refuses the entry when the critical bit is set. Preserves and reports the TLV otherwise. | Applies it. |
| **New manifest chunk** | New chunk id in the TOC. | Skips the unknown chunk id. | Reads it. |
| **Fan-out 8 to 16 bits** | `FEAT_FAN16` required bit and `fanout_bits`. | Refuses the manifest. | Reads it. |
| **Encryption** | `crypto` byte, `FEAT_CRYPTO` bit, key material region. | Refuses the object. | Decrypts it. |
| **Snapshot signature** | New snapshot TLV, non-critical. | Ignores it. | Verifies it. |

### 21.3 Rules for a writer

1. Never write a `required_feat` bit that the current major version does not
   define.
2. Never reuse a registry id.
3. Never change a frozen table under an existing name.
4. Record every parameter that a future diagnostic tool would need, even when a
   reader does not need it.
5. Never depend on a structure that a later version might change. Read the
   version and the `header_len` first.

### 21.4 Rules for a reader

1. Check the magic. Refuse on a mismatch.
2. Check `version_major`. Refuse on an unknown value, and print the value.
3. Read `header_len` and skip the excess. Never assume the compiled size.
4. Check `required_feat`. Refuse on an unknown bit, and print the bit number.
5. Ignore unknown `optional_feat` bits.
6. Verify the checksum before using any field.
7. Verify the content id after decompression.

### 21.5 What a Phase 1 reader does with a Phase 3 disc

The on-disc format is complete from Phase 1, so a Phase 3 disc uses the same
structures. A Phase 1 reader does four things:

- It reads every object, manifest, filter and catalog normally.
- It refuses a disc whose `fs_profile` is 2, because it does not implement ISO
  9660 reads.
- It ignores the optional bitmaps and the reverse index.
- It reports a cross-disc parity group as "not supported" instead of using it.

No data is lost in any of those cases.

---

## 22. Failure modes and recovery matrix

| # | Failure | Detected by | Immediate effect | Recovery | Data loss |
|---:|---|---|---|---|---|
| 1 | A burn fails midway | Non-zero exit from the burner, or a short read-back | The run is incomplete | Objects return to STAGED. Pack a new run onto a fresh disc. | None |
| 2 | Verify finds bad sectors | ddrescue mapfile, checksum column | The run is degraded | RS decode inside the run. | None while erasures are at or below `m` per stripe |
| 3 | Erasures exceed `m` in a stripe | RS decode refuses | Some objects are unreadable on this disc | Content-addressed re-fetch, then cross-disc parity, then mirror, then the source. | None when another copy exists |
| 4 | A whole disc is lost or destroyed | The disc is missing from the inventory | Every object unique to it is missing | Cross-disc parity group, mirror, or the source. Otherwise the loss is enumerable from the catalog. | Objects unique to that disc |
| 5 | The newest disc is lost | The disc directory names a `disc_seq` that is absent | The catalog entry point is gone | Read every other disc's catalog. The previous disc carries the state as of its own burn. | Only the objects unique to the newest disc |
| 6 | The local cache is lost | The cache directory is absent | Dedup and planning are slower | Rebuild from the newest disc, level 1. Then level 3 lazily. | None |
| 7 | The cache is wrong (uuid mismatch) | `disc_uuid` does not match the recorded `disc_seq` | The cache may give wrong answers | Refuse the cache. Rebuild. | None |
| 8 | The state log is truncated by a crash | A record CRC fails | Some objects have an unknown state | Replay up to the bad record. Re-scan staging. Objects with no record are treated as STAGED. | None |
| 9 | An object moved after an append | The LBA read-back differs from the layout table | The layout table would be wrong | Abort the append. Keep objects PACKED. Re-burn the run. | None |
| 10 | The spare area is exhausted | A write error, or the health report below `disc.min_spare_ratio` | Filesystem updates fail | Raw append mode, profile 1 and Phase 2 only (section 9.11). Otherwise a fresh disc. | None |
| 11 | The drive offers no POW | `GET CONFIGURATION` feature 0x38 absent | The disc cannot be appended | Use profile 0 sealed. | None |
| 12 | A filter false positive is unconfirmed | The manifest is unavailable | A chunk may be a duplicate | Write the chunk again. Log it in `pending-confirm`. Section 12.8 gives the rate. | None. Space is wasted. |
| 12a | A file changes while it is read | The re-stat after the read | The parent entry is reused, or a new file is stored with the `UNSTABLE` flag | It is reported as unstable and retried next commit. A filesystem snapshot removes the case. | None. The last consistent version stays available. |
| 13 | A tree entry has an illegal name | Parse-time validation | The entry is refused | Report the tree id and the entry index. The rest of the tree restores. | The one entry |
| 14 | An unknown critical TLV | The critical bit is set and the type is unknown | The entry is refused | Upgrade the tool. | None, once upgraded |
| 15 | A content id does not match after decompression | The verification step | The object is corrupt | Treat it as an erasure. Heal it. | None while parity or another copy exists |
| 16 | A restore runs out of staging space | The predicted peak against `restore.staging_budget` | The restore would stall | The planner splits into passes before it starts. | None |
| 17 | A required disc is missing at plan time | The inventory check | The plan is impossible | Fail before any read. Name the disc and its label. | None |
| 18 | A snapshot references a missing object | The connectivity check | The snapshot is incomplete | Report "missing" with a proof from the filters. Restore what exists. | The missing objects |
| 19 | The burner build is unpatched | The version check at startup | A burn would under-fill or fail to close | `burn --exec` refuses. `burn --print` warns. | None |
| 20 | A disc is substituted | `prev_disc_super_hash` does not chain | The set ordering is wrong | Refuse the disc. Report the expected and found uuid. | None |
| 21 | Media generation goes out of production | Operator knowledge, health report | Future re-burns are impossible | Migrate the library. | None if done in time |
| 22 | A disc reaches 10 years | The health report | Rot risk is rising | Proactive re-burn. | None if done in time |

---

## 23. Testing and CI

### 23.1 Image-first principle

Every burn test uses an image file first.

1. Build the filesystem image.
2. Loop-mount it and verify.
3. Simulate an append by an image diff.
4. Simulate damage by overwriting sectors, then run `verify --heal`.
5. Simulate a lost cache.
6. Simulate a cross-append merge.

Physical burns are a manual checklist, not CI. A GitHub runner has no optical
drive. It does have `sudo`, so loop mounts work.

### 23.2 Required tests

| # | Test | Proves |
|---:|---|---|
| 1 | Format round-trip for every structure, against byte-exact golden files | The layout never drifts |
| 2 | Chunker golden vectors for P3, P4 and P5 | The Gear table and the cut points are frozen |
| 3 | Chunker determinism across buffer sizes and read patterns | The chunker does not depend on I/O shape |
| 4 | Filter false-positive bound over a large synthetic key set | The filter meets 2^-16 |
| 5 | Manifest lookup: every present id found, no absent id found | The fan-out and the binary search are correct |
| 6 | RS reconstruct at exactly `m` erasures per stripe | The parity meets its bound |
| 7 | RS refusal at `m + 1` erasures | The tool never claims a false repair |
| 8 | Burst damage across column boundaries | The interleave works as designed |
| 9 | Checksum column locates a silently corrupted sector | Silent corruption becomes an erasure |
| 10 | Append LBA stability: every earlier object keeps its LBA | The append does not move data |
| 11 | Cache-less restore from the newest image alone | The index-free promise |
| 12 | Restore plan determinism: the same inputs give the same plan | The tie-break order is total |
| 13 | Metadata restore matrix as a non-root user | The loss report and the exit codes |
| 14 | Sparse round-trip: holes in, holes out | Zero chunks dedup and restore as holes |
| 15 | Compression heuristic: a low-gain chunk is stored uncompressed | The 5 percent rule |
| 16 | Tree canonical order: two identical directories hash identically | Dedup does not break |
| 17 | Name validation: every illegal name is refused at parse time | The traversal class is excluded |
| 18 | Symlink redirection attack: a planted symlink does not escape | `O_NOFOLLOW` and `openat2` are used |
| 19 | Hardlink group: restoring one member gives a correct file | No dangling links |
| 20 | State log replay after a truncated write | A crash does not corrupt the store |
| 21 | GC refuses to delete an object that is not CLEAN | The safety rule holds |
| 22 | Burn plan refusal on a bad CRC or a misaligned seek | The plan is validated |
| 23 | Forced capacity: the image, the budget and the FEC layout all shrink | The override reaches every consumer |
| 24 | Reserve formula: the printed budget matches the worked example | The arithmetic is right |
| 25 | Connectivity check reports a missing object with no disc mounted | A filter negative is a proof |
| 26 | Quick check: a file whose size, mtime and ctime match is never read | The commit cost is proportional to the change set |
| 27 | In-flight change: a file rewritten during the read gets the parent entry when one exists, and the `UNSTABLE` flag when it does not | No snapshot ever holds torn content without a flag |
| 28 | Mirror mode: `sync` transfers only the changed paths, and the snapshot equals the direct-mode snapshot | The mirror path never leaks into the archive |
| 29 | BURNED objects stay in staging until `verify` succeeds | The mandatory verify transition |
| 30 | Catalog cap: the manifest history shrinks and the snapshot objects stay | The copy priority holds |
| 31 | `UNSTABLE` round trip: the flag survives write, read, restore and `ls` | The flag is part of the format, not a log line |
| 32 | `pack --close` sealed path: no format step, full capacity, `-dvd-compat` in the plan | The Phase 1 sealed path is exercised |
| 33 | Profile 0 open image: the volume mounts with the anchor at LBA 256 alone | The tail-anchor limitation is real and tolerated |

### 23.3 Composite action

Test steps live in a composite action at `.github/actions/test/action.yml`.
Workflows only call the composite action. That keeps the pipeline definition in
one place and lets a developer run the same steps locally.

```yaml
# .github/workflows/ci.yml
name: ci
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: ./.github/actions/test
```

The composite action installs `udftools`, `dvd+rw-tools`, `genisoimage` and
`gddrescue`, builds the tool, runs the unit tests, then runs the image-level
tests that need `sudo` for loop mounts.

### 23.4 Probe actions

Any open question about tool behaviour becomes a **probe action**: a small
composite action under `.github/actions/probe-<topic>/` that runs the experiment
on an image file and records the result as a job artifact.

A probe is not a test. A test asserts a known answer. A probe records an unknown
one.

| Probe | Question |
|---|---|
| `probe-udf-limits` | Name length and directory depth per UDF revision, on this kernel. |
| `probe-iso-names` | The raw on-disc bytes of a 68-character lowercase name at each ISO level. |
| `probe-copy-order` | Whether copy order equals LBA order, and the stride. |
| `probe-image-append` | Whether an image diff plus a seek write reproduces a full rebuild byte for byte. |
| `probe-mount-matrix` | Which UDF revisions mount on the runner's kernel, and read-write or read-only. |
| `probe-damage-heal` | A damage and heal round-trip at several burst sizes. |
| `probe-mkudffs-options` | The `udfinfo` output for every `--media-type`. |

Each probe writes a machine-readable result file. When a probe answer becomes
stable, it moves into section 23.2 as a test with an assertion.

### 23.5 Manual physical checklist

These steps need a real drive and real media. They run once per release and
after any change to the burn path.

1. `dvd+rw-mediainfo` on a blank disc records the profile and the capacity.
2. A first burn completes and `growisofs -F` reports the expected next writable
   address afterwards.
3. Eject, reload, and read back. The whole image compares equal.
4. The LBA map read from the disc matches the recorded map.
5. Mount on Linux. The file count matches.
6. Mount on Windows 10 and 11. Explorer shows the tree. `certutil -hashfile`
   succeeds on one object from the first append and one from the last.
7. Mount on macOS 15. The file count matches.
8. Mount on Windows XP, profile 1 only. The volume mounts at UDF 2.01.
9. Verify on a second drive of a different model, within 24 hours.
10. M-DISC at `-speed=2` completes and verifies.
11. A BD-R XL 100 GB disc fills to the data budget with no capacity surprise.

### 23.6 Manual probes

These probes decide design questions that no image can answer. Each must be run
on real hardware and its result recorded in the repository.

**Probe 1: kernel direct write to a POW BD-R (profile 1 variant 1a).**

```bash
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=first-run.udf
eject /dev/sr0 && eject -t /dev/sr0 && sleep 10
dvd+rw-mediainfo /dev/sr0 | grep 'Mounted Media'      # expect BD-R SRM+POW
mount -t udf -o rw /dev/sr0 /mnt/ark
cp one-object /mnt/ark/NOAHSARK/objects/ab/<name>
umount /mnt/ark
eject /dev/sr0 && eject -t /dev/sr0 && sleep 10
mount -t udf -o ro /dev/sr0 /mnt/ark && ls /mnt/ark/NOAHSARK/objects/ab/
```

Question: does the `sr` block device accept the write, and does the object
appear after a reload? Record the drive model. If the answer is no, variant 1a
is unavailable on that drive and the implementation must use variant 1b.

**Probe 2: Windows reads ISO 9660:1999 level 4 long lowercase names.**

Burn a profile 2 disc with 68-character lowercase hex names. On Windows 10 and
on Windows 11, check that `dir` shows the full 68-character lowercase name and
not a 30-character uppercase truncation, and that `certutil -hashfile` on such a
file succeeds.

This probe is **blocking**. Profile 2 must not be used in production until it
passes. If Windows truncates, the correct response is to stay on profile 1, not
to add a truncation fallback.

**Probe 3: Windows reads an open POW BD-R.**

Burn one run with `spare:min` and no `-dvd-compat`, so the disc stays open.
Check that Windows 10 and 11 mount it, list every file, and hash one object
correctly.

**Probe 4: macOS reads an open POW BD-R.**

The same disc as probe 3. Check that macOS 15 mounts it, that
`diskutil info` reports the expected filesystem, and that the file count matches
Linux.

**Probe 5: reading past the last written block.**

On the same open disc, read beyond the last written LBA. Record what the drive
does: a hard error, zeros, or a hang. The verifier must handle the answer, and
the answer is drive-dependent.

**Probe 6: writing the tail region before the middle.**

On an open POW disc, try
`growisofs -use-the-force-luke=seek:N,spare:min,tty -Z /dev/sr0=tail.bin` with
`N` near the end of the medium, while the middle is still unwritten. Question:
does the drive accept it? If yes, the first run can place the UDF tail anchors
immediately. If no, the tail anchors wait for an append or for `close`
(section 9.9.1).

---

## 24. Implementation notes

1. **Language: Go.** The standard library covers SHA-256, compression bindings
   are mature, and a single static binary suits a recovery tool.
2. **Code must explain itself.** Comments and commit messages must not cite
   section numbers of this document. A comment carries only information that is
   related to the code beside it.
3. **Vendor the frozen tables.** The Gear table and the mask constants are part
   of the on-disc format. Vendor them. Do not import them from a dependency that
   could change them.
4. **Pin external tools.** Check the versions of `growisofs`, `mkudffs`,
   `udfinfo`, `genisoimage` and `ddrescue` at startup. Refuse to burn with an
   unpatched dvd+rw-tools build.
5. **One Go definition per binary structure**, with explicit encode and decode
   functions. No reflection-based marshalling. No struct tags. The byte layout
   is written out by hand, field by field, in the order of the table in this
   document.
6. **Golden files for every structure.** A test writes a structure with known
   values and compares the bytes to a checked-in file, and reads that file back
   and compares the fields.
7. **No hidden allocation in the hot path.** Chunking and hashing run over
   large files. Reuse buffers.
8. **Every read verifies.** A function that returns object bytes verifies the
   content id before it returns. There is no "trusted" path.
9. **Errors carry the id.** An error about an object names the object. An error
   about a run names the run seq and the disc uuid.
10. **Suggested layout.** `cmd/noahsark` for the CLI, and `internal/` packages
    for chunker, hash, object, tree, manifest, filter, fec, run, burn, stage,
    plan, restore, cache and disc. This is advisory.
11. **Dependencies, kept small.** `github.com/klauspost/reedsolomon`,
    `github.com/klauspost/compress/zstd`, `lukechampine.com/blake3` or
    `github.com/zeebo/blake3`, `github.com/FastFilter/xorfilter`,
    `github.com/mogaika/udf`, `github.com/fsnotify/fsnotify`, and a CLI library.
    Every one of them is vendored or pinned by hash.
12. **Concurrency.** Chunk and hash in parallel across files. Write the run
    image single-threaded, because copy order is LBA order.

---

## 25. Design changes from the previous specification

The previous design is superseded. Every rejected option, with the reason and
the evidence, is in **Appendix D**. The summary:

| Area | Old | New |
|---|---|---|
| Chunking | Fixed 16 MiB | FastCDC 2020, profile P4 |
| Identity | SHA-256 only | Multihash, BLAKE3 default, hash epochs |
| Redundancy | Two identical discs | Per-run Reed-Solomon, plus optional disc-close parity, parity disc and mirror |
| Index | Mandatory local SQLite index | Derived cache, per-run filters and manifests, the newest disc as the entry point |
| Filesystem | UDF 2.50 with an ISO bridge | Pure UDF 2.01, with ISO 9660:1999 level 4 as a fallback profile |
| Sessions | Real UDF multi-session | One physical session, many runs, POW growth |
| Restore | File-major | Disc-major, with a set-cover plan |
| Formats | JSON and SQLite | Fixed-width little-endian packed structures with version headers |
| Compression | None | Per chunk, with an algorithm id |
| Staging | A plain directory | A state machine with GC |
| Trees | Git-style, metadata-free | Full metadata inline, with TLV extensions |
| Merkle trees | Per-file BEP 52 trees | Not needed: BLAKE3 gives intra-chunk localization, and the FEC checksum column gives per-sector detection |
| Burning | The tool burns | The tool writes a burn plan; an external burner writes the disc |

---

## 26. Glossary

Each term is defined once. The same word is used everywhere for the same thing.

| Term | Definition |
|---|---|
| **Append** | Adding a run to a disc that already holds one. Phase 2. |
| **Bundle** | An object that holds many small chunks plus an index. |
| **Burn plan** | The machine-readable file that `pack` writes and `burn` renders into command lines. |
| **Capping** | Bounding the number of older runs that a new run may reference, to bound the restore plan. |
| **Catalog** | The set of tables that every run carries: filters, recent manifests, the snapshot table, the ref table, the disc directory, and the prerequisite list. |
| **Checksum column** | The FEC column that holds an 8-byte digest of every data and parity sector of the next stripe. |
| **Commit bundle** | A directory of new objects plus a `BUNDLE.bin` header, produced by `commit --out` and consumed by `import`. Not the same thing as a bundle object. |
| **Direct mode** | A commit that walks the source itself and reads only changed files. |
| **Mirror mode** | A commit whose changed files are pulled into a mirror directory first, by `sync`. |
| **Unstable path** | A file whose size or mtime changed while it was being read. The parent entry is reused, or the content is stored with the `UNSTABLE` flag. The path is reported. See section 18.6. |
| **Quick check** | The size, mtime and ctime comparison against the parent snapshot's tree entry. |
| **Chunk** | A content-defined slice of a file. The unit of deduplication. |
| **Chunklist** | An object that holds the ordered chunk ids of one large file. |
| **Column** | One of the 255 equal LBA ranges that a parity domain is split into. |
| **Consolidation** | Re-burning a snapshot to a fresh, self-contained disc set. |
| **Content id** | The hash of an object's uncompressed payload bytes. |
| **Disc filesystem profile** | The pair of a filesystem and an append mechanism, recorded in the superblock. |
| **Disc directory** | The catalog table that lists every disc of the repository. |
| **Epoch** | A maximal run of runs that share one hash algorithm. |
| **Fan-out** | The 256-entry or 65536-entry cumulative count table that starts a manifest lookup. Also the directory split of object paths. |
| **Filter** | A BinaryFuse16 approximate-membership structure over a run's object ids. |
| **Forced capacity** | A per-disc cap on usable capacity, below what the drive reports. |
| **Manifest** | The sorted, fan-out-indexed table that maps a content id to its location in a run. |
| **Multihash** | The self-describing id encoding: algorithm code, length, digest. |
| **Object** | Any content-addressed unit: chunk, bundle, chunklist, tree, snapshot. |
| **Parity domain** | The contiguous LBA range of a run that the Reed-Solomon layer protects. |
| **POW** | Pseudo-OverWrite. The BD-R format mode that makes the medium logically overwritable. |
| **Prerequisite** | An object that a run references but does not contain. |
| **Raw append** | The degraded mode that writes a run past the next writable address without updating the filesystem directory. |
| **Reserve** | The part of a disc's capacity that data must not use. |
| **Run** | One write of a burn plan. The unit of packing, manifest, filter and parity. |
| **RS margin** | `m` minus the worst-stripe erasure count, as a percentage of `m`. The headline health metric. |
| **Shard** | One 2048-byte sector, as seen by the FEC layer. |
| **Snapshot** | An object that names a root tree, a parent and a generation. |
| **Spare area** | The reserved region that POW uses for logical overwrites. |
| **Staging** | The local store that holds objects between commit and CLEAN. |
| **Stripe** | The 255 shards, one per column, at the same offset inside their columns. |
| **Tree** | An object that describes one directory, with full metadata per entry. |
| **TLV** | A type-length-value record in a tree entry's extension area. |

---

## Appendix A. Gear table

### A.1 Status

The 256 64-bit Gear constants are **not copied here from a published
implementation**. They are **defined by the rule in section A.2**, and the mask
values are defined by the formula in section A.3. Both rules are normative.

**The format is frozen by these rules.** The rule is the definition, so any two
implementations that follow it produce the same table, the same cut points, and
therefore objects that deduplicate against each other. A printed array would add
no information that the rule does not already fix.

The table is part of the on-disc format. A different table gives different cut
points, which silently ends deduplication against every existing disc. An
implementation must therefore generate the table exactly once, check it into the
source tree as a literal array, and never regenerate it.

### A.2 Generation rule

The table for `gear_table_id = 1` is defined by this rule:

```
seed = "noahsark/gear/v1"                      # 16 ASCII bytes, no terminator

for i in 0 .. 255:
    input      = seed || u8(i)                 # 17 bytes
    digest     = BLAKE3-256(input)             # 32 bytes
    Gear[i]    = little-endian u64 of digest[0 .. 7]
```

Properties of the rule:

1. It is deterministic. Any implementation reproduces the table from the seed
   string alone.
2. It needs no random number generator and no external file.
3. It uses a hash function that the specification already requires.
4. The output is uniform over the 64-bit range, which is what the Gear hash
   needs.

The implementation must ship the resulting 256 constants as a literal array. It
must also commit the BLAKE3-256 hash of that array as a golden vector, over the
2048 bytes of the 256 little-endian u64 values in index order. A test must
regenerate the table from the rule, compare it to the literal array, and compare
the hash to the golden vector. The rule is the authority. The literal array and
the golden vector make an accidental change impossible to miss.

### A.3 Mask constants

The masks are derived from the profile, not from the table. They are defined by
the normalization level 2 formula of FastCDC, Xia et al. 2020. For an average
chunk size of `2^b` bytes:

```
mask_s = spread_mask(b + 2)      # more one-bits: cutting is less likely
mask_l = spread_mask(b - 2)      # fewer one-bits: cutting is more likely
```

`spread_mask(n)` sets `n` bits, distributed over the high half of the 64-bit
word rather than the low bits. The low bits of a Gear hash depend only on the
last few bytes, so a low-bit mask would make the effective window tiny.

Frozen values for the three profiles:

| Profile | avg | b | `mask_s` (bits set) | `mask_l` (bits set) |
|---|---:|---:|---:|---:|
| P3 | 2 MiB | 21 | 23 | 19 |
| P4 | 4 MiB | 22 | 24 | 20 |
| P5 | 8 MiB | 23 | 25 | 21 |

The six 64-bit mask values follow from the bit counts in the table above and
from `spread_mask`. The implementation must compute them once, check them in as
literals, and cover them with the golden vectors of section 23.2.

**The on-disc format is frozen by section A.2 and this section.** The Gear table
and the masks are fully determined by the two rules, so no printed array is
needed to freeze them. An implementation must never change a table or a mask
under an existing `gear_table_id` or an existing profile name.

### A.4 If a published table is adopted instead

An implementation may instead adopt the Gear table of a named published FastCDC
implementation. If it does:

1. The specification must name the implementation, the exact version or commit,
   and the file and line where the table appears.
2. The table must be copied verbatim into the source tree.
3. `gear_table_id` must be a new id, not 1.
4. A new chunker profile name must be assigned, because the cut points differ.

Never change the table under an existing `gear_table_id`.

---

## Appendix B. Magic numbers and registry summary

### B.1 Magic values

Each magic is four ASCII bytes. The file bytes are the mnemonic in order. The
u32 value is the little-endian reading of those bytes.

| Mnemonic | u32 value | Structure | Section |
|---|---|---|---|
| `NAOB` | 0x424F414E | Common object header | 4.3 |
| `NADS` | 0x5344414E | Disc superblock | 9.5 |
| `NARH` | 0x4852414E | Run header | 9.6 |
| `NALY` | 0x594C414E | Run layout table | 9.7 |
| `NAMF` | 0x464D414E | Manifest container | 12.3 |
| `NAFL` | 0x4C46414E | Filter container | 12.2 |
| `NAST` | 0x5453414E | Snapshot table | 12.5 |
| `NARF` | 0x4652414E | Ref table | 12.5 |
| `NADD` | 0x4444414E | Disc directory | 12.6 |
| `NATR` | 0x5254414E | Tree object payload | 8.5 |
| `NASN` | 0x4E53414E | Snapshot object payload | 8.7 |
| `NACL` | 0x4C43414E | Chunklist object payload | 8.4 |
| `NABD` | 0x4442414E | Bundle header | 8.3 |
| `NABT` | 0x5442414E | Bundle trailer | 8.3 |
| `NACS` | 0x5343414E | Checksum column sector header | 11.4 |
| `NASL` | 0x4C53414E | Staging state log | 14.3 |
| `NAXL` | 0x4C58414E | Cross-algorithm side table | 5.7 |
| `NABN` | 0x4E42414E | Commit bundle header. Backlog, reserved. | 18.11.1 |
| `NABP` | 0x5042414E | Burn plan container | 10.7.1 |
| `NABS` | 0x5342414E | Burn plan step | 10.7.1 |

An implementation must compute these constants from the ASCII bytes, not copy
the hexadecimal column, and a test must assert that the two agree.

### B.2 Registries

**Hash algorithm** (multicodec codes)

| Code | Name | Digest bytes |
|---:|---|---:|
| 0x12 | sha2-256 | 32 |
| 0x1e | blake3 | 32 |
| 0x13 | sha2-512 (reserved) | 64 |
| 0xb220 | blake2b-256 (reserved) | 32 |

**Compression**

| Id | Name |
|---:|---|
| 0 | none |
| 1 | zstd |
| 2 | lz4 |

**Chunker profile**

| Id | Name | min | avg | max |
|---:|---|---:|---:|---:|
| 1 | P3 | 512 KiB | 2 MiB | 8 MiB |
| 2 | P4 | 1 MiB | 4 MiB | 16 MiB |
| 3 | P5 | 2 MiB | 8 MiB | 32 MiB |

**Filter type**

| Id | Name |
|---:|---|
| 1 | binaryfuse16 |
| 2 | binaryfuse8 (reserved) |
| 3 | bloom (memory only) |

**FEC scheme**

| Id | Name |
|---:|---|
| 1 | rs255-gf8 |
| 2 | rs-leopard-gf16 (reserved) |

**Object kind**

| Id | Name |
|---:|---|
| 1 | chunk |
| 2 | bundle |
| 3 | chunklist |
| 4 | tree |
| 5 | snapshot |
| 6 | ref |

**Disc filesystem profile**

| Id | Name | Phase |
|---:|---|---:|
| 0 | oneshot | 1 |
| 1 | udf201-pow | 2 |
| 2 | iso9660v1-l4-pow | 3 |

**Source type** (snapshot object, `source_type`)

| Id | Name | Note |
|---:|---|---|
| 0 | unknown | A pre-1.0 writer. |
| 1 | local | |
| 2 | snapshot | A filesystem snapshot of a local filesystem. |
| 3 | nfs | |
| 4 | smb | |
| 5 | bundle | Backlog, reserved. |

**Media type**

| Id | Name | Sectors |
|---:|---|---:|
| 1 | BD-R SL 25 | 12,219,392 |
| 2 | BD-R DL 50 | 24,438,784 |
| 3 | BD-R XL TL 100 | 48,878,592 |
| 4 | BD-R XL QL 128 | 62,500,864 |
| 5 | BD-RE SL 25 | 12,219,392 |
| 6 | BD-RE DL 50 | 24,438,784 |
| 7 | BD-RE XL TL 100 | 48,878,592 |
| 8 | M-DISC BD SL 25 | 12,219,392 |
| 9 | M-DISC BD DL 50 | 24,438,784 |
| 10 | image | variable |
| 11 | Mini BD SL 8 cm | 3,804,288 |
| 12 | Mini BD DL 8 cm | 7,608,576 |

**Tree TLV types**: see section 8.5.5.

**Manifest TOC chunk ids**: `FANO`, `RECS`, `PREQ`, `SRCR`, `DUPS`, `BMAP`,
`RIDX`. See section 12.3.

**Burn step kinds** (section 10.7.1)

| Id | Kind |
|---:|---|
| 1 | write image |
| 2 | build and write from a tree |
| 3 | append from a tree |
| 4 | mount read-write |
| 5 | copy tree into the mount |
| 6 | unmount |
| 7 | close |
| 8 | eject |
| 9 | reload |

**Object state**: 1 STAGED, 2 PACKED, 3 BURNED, 4 CLEAN, 5 GC-ELIGIBLE,
6 DELETED. See section 14.3.

---

## Appendix C. Command reference for the burning host

Every command in this appendix runs on the Linux host that holds the drive.

### C.1 Probe the drive and the medium

```bash
# Capacity and next writable address, in BYTES.
eval "$(growisofs -F /dev/sr0)"
echo "capacity=$capacity next_session=$next_session"
NWA=$(( next_session / 2048 ))

# Media type, session count, free blocks.
dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Number of Sessions|Next Writable Address|Free Blocks|Track Size'

# BD-R SRM+POW  -> formatted for Pseudo-OverWrite, appendable in place
# BD-R SRM      -> not formatted, sequential only
```

### C.2 Build a UDF 2.01 image (profile 0 and profile 1)

```bash
BLOCKS=$(( capacity / 2048 )); BLOCKS=$(( BLOCKS - BLOCKS % 16 ))
truncate -s $(( BLOCKS * 2048 )) run.udf

mkudffs --utf8 --media-type=hd --blocksize=2048 --udfrev=2.01 \
        --label=NOAHSARK-0001 --uid=0 --gid=0 --mode=0555 \
        --bootarea=erase run.udf

mount -t udf -o loop,rw run.udf /mnt/ark
#   copy files one at a time, in fill order
umount /mnt/ark

udfinfo run.udf | grep -q '^integrity=closed' || { echo "dirty image"; exit 1; }
udfinfo run.udf | grep -c 'type=ANCHOR'        # expect 3
```

### C.3 Burn

```bash
# Profile 0, default: POW-formatted, one run, disc left open.
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=run.udf

# Profile 0, sealed: no format, closed, full capacity, stable LBAs.
growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:none,tty \
          -Z /dev/sr0=run.udf

# Profile 1 append (Phase 2): one call per changed 32 KiB-aligned run.
growisofs -speed=4 -use-the-force-luke=seek:${NWA},spare:min,tty \
          -Z /dev/sr0=append.bin

# Profile 2 (Phase 3): first write, then append, then close.
growisofs -speed=4 -use-the-force-luke=spare:min,tty \
          -Z /dev/sr0 -iso-level 4 -D -l -allow-limited-size \
          -no-limit-pathtables -sort sortfile -V ARK-0001 /srv/ark
growisofs -speed=4 -use-the-force-luke=spare:min,tty \
          -M /dev/sr0 -iso-level 4 -D -l -allow-limited-size \
          -no-limit-pathtables -sort sortfile -V ARK-0001 /srv/ark
growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:min,tty \
          -M /dev/sr0 -iso-level 4 -D -l -allow-limited-size \
          -no-limit-pathtables -sort sortfile -V ARK-0001 /srv/ark

# M-DISC: replace -speed=4 with -speed=2 in any of the above.
# CI dry run: add -dry-run.
# Safety assertion when appending: add -C 16,${NWA}.
```

Never pass `-overburn`. Never pass `-M` on a UDF disc. Never let the drive use
BD-R Random Recording Mode.

### C.4 Verify

```bash
eject /dev/sr0 && eject -t /dev/sr0 && sleep 5

SIZE=$(stat -c%s run.udf)

# Exact compare.
dd if=/dev/sr0 bs=2048 count=$(( SIZE / 2048 )) iflag=direct status=progress \
   | cmp - run.udf && echo "VERIFY OK"

# Tolerant read: keeps offsets aligned across bad sectors.
dd if=/dev/sr0 of=readback.udf bs=2048 conv=noerror,sync \
   count=$(( SIZE / 2048 )) status=progress

# Filesystem level.
udfinfo /dev/sr0
mount -t udf -o ro /dev/sr0 /mnt/ark && find /mnt/ark -type f | wc -l
umount /mnt/ark

# The program's own check, which is the one that matters.
noahsark verify --disc <uuid> --level=integrity --drive /dev/sr1
```

### C.5 Recover a damaged disc

```bash
# Fast pass first: no scraping.
ddrescue -b 2048 -n -r1 /dev/sr0 rescued.img rescue.map

# Then the slow pass, resumable through the same map file.
ddrescue -b 2048 -d -r3 /dev/sr0 rescued.img rescue.map

# The map file is the erasure list for the FEC layer.
noahsark verify --image rescued.img --heal --report health.json
```

### C.6 Read back the LBA map

```bash
# Profile 2, ISO 9660: isoinfo prints every extent.
isoinfo -i disc.iso -T "$SESSION_START" -l \
  | sed -n 's/.*\[ *\([0-9]*\) *[0-9]*\] *\([0-9a-f]\{68\}\).*/\2 \1/p'

# Profile 0 and 1, UDF: the program parses File Entries.
noahsark image build --run <seq> --out run.udf   # then read the map internally

# Volume-level layout only, never per file.
udfinfo run.udf | grep -E 'start=|blocks=|type='
```

### C.7 Tool versions to check at startup

```bash
growisofs -version 2>&1 | head -2
dpkg -l dvd+rw-tools 2>/dev/null | tail -1     # section 10.12 gives the matrix
mkudffs --help 2>&1 | head -1                  # expect udftools 2.3 or newer
genisoimage -version                            # profile 2 only
ddrescue --version | head -1
```

An unpatched dvd+rw-tools 7.1 under-reports BD-R capacity and fails to close a
blank BD-R. `burn --exec` must refuse it.

---

## Appendix D. Rejected and superseded alternatives

**Superseded. Do not implement anything in this appendix.**

This appendix records what was considered and rejected, with the reason and the
evidence. It exists so that a future reader does not re-open a settled question.

**Fixed 16 MiB chunking.** The previous design cut every file into fixed 16 MiB
blocks. It is simple and predictable, and it produces about 1,500 objects per
25 GB disc. It was rejected because a fixed cut point catches no shifted-insert
edit: a single byte inserted at the front of a file changes every later block.
FastCDC at profile P4 gives about 6,000 objects per 25 GB disc, keeps UDF
overhead under 0.1 percent, and still catches the edits that a fixed 16 MiB
block misses. Rejected in favour of section 6.

**SHA-256-only ids.** The previous design hardcoded SHA-256 with no agility
mechanism, as restic does. It was rejected because a 30-year archive will
outlive at least one hash transition, and because a bare untagged digest gives a
reader no way to fail loudly on an unknown algorithm. The replacement is
multihash ids with a registry, BLAKE3 as the default, and hash epochs
(section 5). SHA-256 remains fully supported, so nothing is lost.

**Two mirror discs as the only redundancy.** The previous design burned two
identical discs and had no error correction. It was rejected because it costs
100 percent overhead and cannot repair partial damage: it can only replace a
whole object from the other copy, and only while the other copy still mounts. A
per-run Reed-Solomon layer at 10 percent corrects a 2.26 GB contiguous burst on
a 25 GB disc, which covers a 3 mm ring scratch. The layered plan of section 11.6
gives better protection for roughly one quarter of the media. Mirrors remain
available as an optional extra layer.

**A mandatory local index.** The previous design required a SQLite `index.db`
that mapped every hash to a disc location. It was rejected because it makes a
local file load-bearing for an archive whose truth lives on shelves: losing the
index would make the discs unusable until a full rebuild. The replacement is
per-run filters and manifests plus a catalog on every disc, with the local cache
demoted to an accelerator (sections 12 and 13). A CI test deletes the cache and
restores from the newest image alone.

**JSON and SQLite on-disc formats.** The previous design stored `session.json`,
`discs/<id>.json` and a per-session `index.db` on the disc. Both were rejected.
JSON costs about four times the size of a packed record, needs a parser, and
cannot be binary-searched. A database engine on a 30-year medium is a dependency
risk: the reader in 2050 must have a compatible engine, not just the bytes. The
replacement is fixed-width little-endian packed structures with version headers,
which map directly into memory (section 4).

**ISO 9660 bridge with `genisoimage -udf`.** A bridge disc carries both an ISO
9660 tree and a UDF tree. It was rejected on measured evidence: `genisoimage
-udf` writes **UDF 1.02**, the oldest revision, with no VAT, no metadata
partition and no sparing. It is always a bridge, so two independently built
trees exist and can disagree, and only one gets verified after the burn. Which
tree wins is not under our control: Windows prefers UDF over CDFS, macOS prefers
the ISO side, and Linux depends on probe order, so two platforms read different
trees from the same disc. Thomas Schmitt also states that multi-session with
`genisoimage -udf` is "known to be problematic (or impossible)", which would
destroy the `-M` append path that is the only reason to use ISO at all. Never
pass `-udf`.

**Rock Ridge and Joliet.** Both were forbidden by decision, and the measured
evidence supports it. Rock Ridge is byte-transparent and carries POSIX metadata,
but **Windows ignores it completely**. Joliet is the only mechanism Windows
honours, and it is UCS-2, so every character above the Basic Multilingual Plane
is lost. Neither is needed, because NoahsArk's on-disc names are 68-character
lowercase hex and all real metadata lives inside tree objects. The surprise that
makes the ban acceptable is that ISO 9660:1999 level 4 preserves lowercase and
long names with **zero** Rock Ridge bytes, measured as
`Total rockridge attributes bytes: 0`.

**True UDF multi-session with a VAT (`mkudffs --media-type=bdr`).** The Virtual
Allocation Table is the UDF mechanism for appending to write-once media. It was
rejected on measured evidence. A `-m bdr` image does not mount at all, because
the VAT must live in the last recorded block. Truncating the image to
`(vatblock + 1) * 2048` makes it mount **read-only**. The Linux kernel forces
read-only on every write-once volume at `fs/udf/super.c`:

```c
switch (le32_to_cpu(p->accessType)) {
case PD_ACCESS_TYPE_READ_ONLY:
case PD_ACCESS_TYPE_WRITE_ONCE:
case PD_ACCESS_TYPE_NONE:
        goto force_ro;
}
```

A VAT volume can therefore never be populated on Linux, at any kernel version.
`mkudffs --startblock` was tested too. It creates a new, empty filesystem at an
offset: `udfinfo --startblock=51200 ms.img` reports `numfiles=0`. libisofs 1.5.8
contains no UDF writer at all; `grep -rniw udf libisofs/` returns zero matches.
See section 9.13 for the conclusion.

**Descriptor-set-per-session UDF multi-session.** Writing a complete UDF
descriptor set per session, with a fresh anchor at `session_start + 256` and a
full directory tree that points back into earlier sessions, is spec-legal. It
was rejected for three reasons. First, no tool builds it: `mkudffs --startblock`
creates a new **empty** filesystem at an offset, measured as `numfiles=0`, so
NoahsArk would have to become a UDF writer. Second, the cost per session is
about 54 blocks of descriptors plus the entire rewritten tree, which is about
205 MB per session at 100,000 objects, paid again every time. Third, and
decisively, **macOS sees only the first session** until the disc is closed. The
POW-growth design of section 9.8 keeps `Number of Sessions: 1`, which sidesteps
the macOS limitation entirely.

**xorriso and libisofs.** xorriso is actively maintained and would have been the
natural choice. It was rejected on a direct source check: `grep -rniw udf` over
`libisofs-1.5.8.pl02/libisofs/` returns **zero** matches. The filesystem writer
contains no UDF code at all. The only `udf` references in the xorriso tree are
in the mkisofs argument-counting tables at `emulators.c:637` and `:835`, which
declare `-udf` as a zero-argument option to be parsed and discarded. `xorriso
-as mkisofs -udf` therefore produces a plain ISO 9660 image. As a raw image
burner xorriso is capable, but that is the job growisofs already does, so
switching would buy nothing. cdrskin, which shares the same libburn back end and
does write raw images, is kept as the fallback burner instead.

**pktcdvd packet writing.** Packet writing would have allowed ordinary
filesystem writes to optical media. It was rejected because it no longer exists:
`drivers/block/pktcdvd.c` is gone from torvalds/linux master, there is no
`CDROM_PKTCDVD` in `drivers/block/Kconfig`, and no module ships in the Debian
kernel used for testing. `pktsetup` and `cdrwtool` from udftools are therefore
dead ends.

**`mkudffs --spartable`.** A UDF sparing table is a filesystem-level bad-block
remap. It was rejected because it adds a second logical-to-physical indirection
that breaks the parity map: a sector that the sparing table moved is no longer
where the FEC layout says it is. Reed-Solomon already covers the failure that a
sparing table addresses, and it covers it better, because it works after the
disc ages rather than only at write time. Windows 7 and later and macOS both
read sparing tables, so this is a design choice and not a compatibility one.

**A Bε tree for the cache index.** GEFS uses a Bε tree, which buffers small
updates in interior nodes and flushes them in batches. It was rejected for the
cache index after an honest look at the workload. The keys are uniformly random
hashes, so every insert flushes to a different subtree. The batching advantage
largely disappears. The index is written in whole-run bulk loads after a burn,
not as a stream of small updates. It is read randomly and constantly. It is
fully rebuildable. A plain sorted array of fixed-width records with a
fan-out table, in the multi-pack-index shape, is merged by one linear pass and
searched by pure arithmetic. The Bε tree's advantage is one NoahsArk does not
need, and its cost is a much harder recovery story.

**casync-style catar stream chunking across files.** casync serializes the whole
tree into one canonical stream and then chunks that stream, so small files
deduplicate naturally as part of it. It was rejected because a random-access
restore of one small file then needs the stream index, and because any metadata
change reshuffles the stream and therefore the cut points. On optical media,
where the goal is to read one directory in one linear pass, a reshuffling stream
is the wrong structure. Restic-style bundles (section 8.3) give the same
small-file win with stable, independently addressable objects.

**Whole-file compression before chunking.** Compressing a file and then chunking
the compressed bytes would give a better ratio on some inputs. It was rejected
because it destroys deduplication completely: a one-byte change near the start
of a file changes every compressed byte after it, so every chunk changes.
Compression happens per chunk, after the content id is computed (section 7).

**Extent tables for sparse files.** The previous design considered an explicit
map of data extents per file. It was rejected for two reasons. It adds a structure that must be kept
consistent with the chunk list. And it is unnecessary: an all-zero region
produces identical maximum-size zero chunks, which deduplicate to one object in
the whole repository. The restorer detects an all-zero chunk and punches a
hole. `SEEK_HOLE` and `SEEK_DATA` remain available as a read-speed
optimization that does not change the format (section 6.6).

**Per-object Reed-Solomon instead of per-run column RS.** Adding parity to each
object separately would allow a targeted repair with local reads. It was
rejected on four counts. It does not protect the filesystem metadata, and
metadata loss is what makes a disc unmountable. Its interleaving is poor: a
small object's parity sits near the object, so one burst destroys both. Its
overhead is uneven: a 4 KiB object with 10 percent parity still needs a whole
extra shard. And it does not work when the filesystem is unmountable, because it
needs the filesystem to find the object. The column layout of section 11.2
protects everything inside the parity domain, including the filesystem's own
blocks, and the layout table still allows a targeted repair by reading only the
affected columns.

**A mandatory cross-algorithm hash mapping.** Git designed a bidirectional
SHA-1-to-SHA-256 translation layer and effectively never shipped it, because the
cost lands on every object and every lookup, and because loose-object lookup is
linear in the number of loose objects. It was rejected here for the same reason,
plus two that are specific to this system. Building the table requires reading
every chunk of every disc, about 20 hours for an 80-disc archive. And the
benefit is a one-time dedup saving that dies after the first post-epoch
backup. The table
survives as an explicit, optional, rebuildable side table where correctness never
depends on it (section 5.7).

**Shallow-clone-style partial graphs.** Git's shallow clone lists commits to be
treated as parentless and suppresses the complaint. It was rejected because
truncating the graph and hoping is exactly wrong for a backup system: a snapshot
must be provably complete or provably incomplete. The partial-clone discipline
was adopted instead: a run declares that it is partial and lists its
prerequisites with the run that holds each (section 12.4), so "on another disc"
is never indistinguishable from "corrupt".

**Fixed-LBA raw structures.** An earlier draft of this specification placed the
run header at `m + 2` computed LBAs and found the newest run by scanning
backwards from the next writable address. It was superseded by the rule that
every byte NoahsArk writes is an ordinary file (section 9.2). Hidden sectors are
invisible to every operating system, they are lost when a filesystem is rebuilt,
and they make a disc look corrupt to any tool that is not NoahsArk. The same
radial spread is achieved by ordinary files, because copy order equals LBA
order: `RUN.bin` first, a header copy at the start of each parity file, and
`RUN2.bin` last.

**Per-file Merkle trees, BitTorrent v2 BEP 52 style.** The previous design built
a 16 KiB-leaf Merkle tree per file. It was rejected as redundant. BLAKE3 already
has an internal Merkle tree, so a corrupt region inside a chunk can be localized
without a second structure. The FEC checksum column detects corruption per
2048-byte sector, which is finer than a 16 KiB leaf. Keeping a third mechanism
would add bytes and code for no additional detection.

**Real-time automatic commit in watch mode.** A daemon that commits by itself
was rejected because a commit must run against a stable filesystem, and a daemon
cannot know when the source is quiescent. Watch mode was also removed from Phase 1
entirely. It is an accelerator for the change scan, not a feature of the
archive. `commit` is idempotent, so the watcher can arrive in Phase 3 with no
format change and no state change.

**A built-in scheduler.** A daemon or a timer inside NoahsArk was rejected.
cron and systemd timers are already present, already tested, and already
monitored. `commit` is a batch job with a lock and clear exit codes, which is all
a scheduler needs from it.

**Including a file that changed while it was read.** Restic and borg accept such
a file and record a warning. It was rejected here because the resulting chunk
list describes a state that never existed on the disk, and on write-once media
that error is permanent. NoahsArk reuses the last consistent version when
one exists, stores a new file with an `UNSTABLE` flag when none exists, reports
the path, and retries on the next commit.

**A full rsync mirror of the source on the staging disk.** An earlier form of
mirror mode kept a complete second copy of the source and ran `rsync -aHAX
--delete` against it. It was rejected because it doubles the disk requirement for no gain. The parent
snapshot's trees already hold the size, the mtime and the ctime of every path.
They are the old copy that rsync would compare against. `sync` now diffs a stat listing against the parent tree and transfers
only the change set with `--files-from`.

**A time-based transition from BURNED to CLEAN.** An earlier draft let staging
release an object after a retention period, whether or not the disc had been
read back. It was rejected because the burn is the one step that can fail
silently on write-once media. `verify` is now the only path to CLEAN, and
`burn --exec` runs it by default.
