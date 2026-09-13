# NoahsArk on-disc format

Format major version 1. Document version 0.4.4.

This document defines every byte that NoahsArk writes onto a disc and every
rule a reader applies to those bytes. It covers the binary conventions, object
identity, chunking, compression, every object kind, the disc and run
structures, the filesystem layout, forward error correction, the run index
and catalog tables, and the checks a reader performs. Everything in
this document is normative. A second implementation that follows it produces
byte-identical discs from the same inputs and makes identical accept and
reject decisions on the same bytes. Commands, configuration, workflow,
rationale and local state live in other documents and are not repeated here.

## Table of contents

- [1. Scope and conventions](#1-scope-and-conventions)
- [2. Binary format rules](#2-binary-format-rules)
  [2.1](#21-the-format-rules) · [2.2](#22-magic-values) · [2.3](#23-common-header) · [2.4](#24-strings) · [2.5](#25-registries) · [2.6](#26-version-policy) · [2.7](#27-hash-coverage-per-structure) · [2.8](#28-crc-coverage-per-structure) · [2.9](#29-limits) · [2.10](#210-trust-boundaries-and-safety-invariants)
- [3. Identity and hashing](#3-identity-and-hashing)
  [3.1](#31-the-content-id-rule) · [3.2](#32-hash-algorithms) · [3.3](#33-digest-fields-in-records) · [3.4](#34-text-form) · [3.5](#35-fan-out-on-disc) · [3.6](#36-hash-epochs-and-cross-epoch-references)
- [4. Chunking](#4-chunking)
  [4.1](#41-algorithm) · [4.2](#42-cut-point-rule) · [4.3](#43-chunker-profiles) · [4.4](#44-profile-recording-and-change) · [4.6](#46-zero-regions-and-sparse-files) · [4.7](#47-determinism) · [4.8](#48-gear-table) · [4.9](#49-mask-constants)
- [5. Compression](#5-compression)
  [5.1](#51-order-of-operations) · [5.2](#52-header-fields) · [5.3](#53-algorithm-and-frame-parameters) · [5.4](#54-minimum-gain) · [5.5](#55-compression-and-identity) · [5.6](#56-what-is-never-compressed)
- [6. Objects](#6-objects)
  [6.1](#61-object-kinds-and-the-object-header) · [6.2](#62-chunk) · [6.4](#64-blob) · [6.5](#65-tree) · [6.6](#66-tree-entry-fixed-header) · [6.7](#67-entry-flags) · [6.8](#68-variable-areas-and-the-content-area) · [6.9](#69-extension-tlv-record) · [6.10](#610-tlv-type-registry) · [6.11](#611-name-validation) · [6.12](#612-what-is-never-stored) · [6.13](#613-hardlinks) · [6.14](#614-snapshot) · [6.15](#615-the-root-tree) · [6.16](#616-exclude-pattern-language) · [6.17](#617-ref) · [6.18](#618-reserved-crypto-fields) · [6.19](#619-canonical-ordering)
- [7. Disc and run model](#7-disc-and-run-model)
  [7.1](#71-the-physical-disc) · [7.2](#72-disc-filesystem-profile) · [7.3](#73-the-run-and-the-fec-terms) · [7.4](#74-what-the-parity-does-and-does-not-cover) · [7.5](#75-disc-superblock) · [7.6](#76-run-header) · [7.7](#77-the-run-chain) · [7.8](#78-run-header-copies) · [7.9](#79-what-an-append-overwrites) · [7.10](#710-recovery-by-carving) · [7.11](#711-close-state) · [7.12](#712-raw-append) · [7.13](#713-forced-capacity) · [7.14](#714-how-a-lifecycle-state-is-recorded)
- [8. Filesystem profiles and the volume tree](#8-filesystem-profiles-and-the-volume-tree)
  [8.1](#81-profiles-a-reader-must-know) · [8.2](#82-files-at-the-volume-root) · [8.3](#83-run-directory-naming) · [8.4](#84-readmetxt) · [8.5](#85-formattxt) · [8.6](#86-reference-decoder) · [8.7](#87-fill-order-inside-a-run) · [8.8](#88-name-and-path-budget)
- [9. Capacity invariants](#9-capacity-invariants)
- [10. Forward error correction](#10-forward-error-correction)
  [10.1](#101-parity-layout) · [10.2](#102-the-code) · [10.3](#103-checksum-column) · [10.4](#104-header-replication-and-parity-files) · [10.5](#105-decode-rule) · [10.6](#106-the-append-bound) · [10.7](#107-health-status-values)
- [11. The run index and the catalog](#11-the-run-index-and-the-catalog)
  [11.1](#111-index) · [11.2](#112-refs) · [11.3](#113-discs) · [11.4](#114-catalog-contents-per-run) · [11.5](#115-dedup-rule) · [11.6](#116-proof-of-absence-and-coverage)
- [12. Reader and writer rules](#12-reader-and-writer-rules)
  [12.1](#121-reader-procedure) · [12.2](#122-writer-rules) · [12.3](#123-which-catalog-a-reader-trusts) · [12.4](#124-conformance) · [12.5](#125-change-mechanisms) · [12.6](#126-cross-version-and-cross-phase-reading) · [12.7](#127-refusal-and-partial-reading) · [12.8](#128-settings-that-change-disc-bytes)
- [13. Golden vectors](#13-golden-vectors)
- [Appendix A. FORMAT.txt, format major 1 minor 0](#appendix-a-formattxt-format-major-1-minor-0)
- [Appendix B. Decision index](#appendix-b-decision-index)

## 1. Scope and conventions

A NoahsArk disc carries one directory tree. Every byte NoahsArk writes is an
ordinary file inside that tree. There are no hidden sectors, no fixed-address
structures and no raw areas outside the filesystem.

```
/NOAHSARK/
    DISC.bin                     disc superblock, written once
    README.txt                   plain-text explanation for a human
    FORMAT.txt                   the byte-layout tables of every structure
    runs/<seq>/
        RUN.bin                  run header, the first file copied in the run
        INDEX.bin                file order, object table, prerequisites
        catalog/                 REFS.bin, DISCS.bin, snapshot objects
        checksum.bin             the checksum column of the FEC stream
        parity/pNNNN.bin         one file per parity column
        RUN2.bin                 run header copy, the last file copied in the run
    objects/<ab>/<name>          chunks, blobs and trees, shared by every run on the disc
    snapshots/<name>             snapshot objects
```

`<ab>` is the first two hex digits of the digest. `<name>` is the full
68-character multihash hex (section 3.5).

A run is the unit of packing, of the object index, of the Reed-Solomon parity
and of the catalog copy.

A burned run is immutable. Nothing rewrites it. Format version 1 never removes
a snapshot and never frees disc space on a burned disc. There is no retention
and no expiry, so no structure carries a deletion record, and every reserved
field stays reserved.

Every sentence in this document is a rule. There are no informative blocks.

---

## 2. Binary format rules

### 2.1 The format rules

1. Every integer is little-endian. No big-endian field exists.
2. Only fixed-width types are used: u8, u16, u32, u64, i32, i64. No varint
   appears inside a fixed header.
3. Every structure is packed with manual alignment. Every gap is a named
   reserved field. A writer writes zero into every reserved field and every
   padding byte. A reader does not interpret a reserved field and does not
   reject a nonzero value in one. A golden test checks that the writer wrote
   zero into every reserved field and every padding byte.
4. Every structure begins with the 32-byte common header of section 2.3.
5. A structure is a file-level container or an object payload header. A
   record inside a structure, a tree entry, a TLV, an INDEX
   table row, a REFS or DISCS row, is a record, not a structure. A record
   never carries the common header. A record carries a magic only where its
   own table states one.
6. A reader refuses an unknown `version_major`. A reader accepts an unknown
   `version_minor` and ignores the fields it does not know.
7. A `version_minor` bump may only append fields to the end of the fixed
   body, and an old reader must be able to read the structure correctly
   while ignoring them. Any change an old reader cannot ignore is a
   `version_major` bump, and an old reader refuses it.
8. A checksum covers only bytes the writer finalized before computing it. A
   structure with no separate body ends with one CRC over every byte before
   it. A container with a header and a body carries `header_crc32c` and
   `body_crc32c` at the end of its header: `body_crc32c` sits in the header
   and covers the body. The body becomes final first, then the header is
   written last, so each CRC covers bytes that were already final.
9. CRCs are CRC-32C: polynomial 0x1EDC6F41, reflected, initial value
   0xFFFFFFFF, final XOR 0xFFFFFFFF.
10. Objects use the full content hash in place of a body CRC. The content id
    is the checksum of the payload.
11. Every pointer carries the hash of its target. No unhashed reference
    exists.
12. A string is `encoding` (u8), `reserved` (u8[3], zero), `length` (u32) in
    bytes, then the bytes. Encoding 0 is UTF-8. There is no NUL terminator and
    no normalization.
13. Every structure has a byte-offset table with the columns offset, size,
    type, name and meaning.
14. Every file that the format writes begins on a sector boundary of the
    medium. A structure's fixed header carries enough length information to
    find the structure's own end without reading anything past it, so that a
    tool can find a structure by scanning the raw medium for the project
    magic, section 2.2.

The CRC-32C parameters and their check value:

From rule 9, verbatim:

> CRCs use CRC-32C (Castagnoli, polynomial 0x1EDC6F41,
> reflected, initial value 0xFFFFFFFF, final XOR 0xFFFFFFFF).

From Appendix A, part 6 CHECKSUM PARAMETERS, verbatim (tab-separated):

```
6. CHECKSUM PARAMETERS
======================

CRC32C_POLYNOMIAL	0x1edc6f41
CRC32C_REFLECTED	1
CRC32C_INIT	0xffffffff
CRC32C_XOROUT	0xffffffff
BLOCK_BYTES	2048
FEC_K	231
FEC_M	23
```

Check value, from the golden vector table of section 13, verbatim:

| CRC-32C | The 9-byte string `123456789`. | `0xE3069283`. |

### 2.2 Magic values

There is one project magic and one magic-kind field. The project magic is the
8 ASCII bytes `"NOAHSARK"`. Every structure's common header carries it in
`magic_project` (section 2.3). A reader compares all 8 bytes.

`magic_kind` is 8 ASCII bytes, the kind name, zero-padded when the name is
shorter than 8 bytes. A reader compares all 8 bytes, padding included: a
mismatch in a trailing zero byte is as final as a mismatch in a letter.

| `magic_kind` (padded to 8 bytes) | Structure | Section |
|---|---|---|
| `CHUNK\0\0\0` | Chunk object | 6.2 |
| `BLOB\0\0\0\0` | Blob object | 6.4 |
| `TREE\0\0\0\0` | Tree object | 6.5 |
| `SNAPSHOT` | Snapshot object | 6.14 |
| `BUNDLE\0\0` | Bundle container | reserved for a later version; a Phase 1 writer never emits it; a reader refuses it |
| `DISC\0\0\0\0` | Disc superblock | 7.5 |
| `RUN\0\0\0\0\0` | Run header | 7.6 |
| `INDEX\0\0\0` | Run index | 11.1 |
| `CHECKSUM` | Checksum column record | 10.3 |
| `REFS\0\0\0\0` | Ref table | 11.2 |
| `DISCS\0\0\0` | Disc directory table | 11.3 |

An implementation computes the eight bytes from the ASCII name and never
copies a hexadecimal column. A test asserts the two agree.

A parity file carries one RUN header copy in its block 0 and no other
structure of its own; a reader identifies it by that copy's `magic_kind`
`RUN\0\0\0\0\0` and by the fill-order position section 8.7 and section 10.1
state.

Six further magics name host-only structures whose bytes never reach a disc:
`NASL`, `NABN`, `NABP`, `NABS`, `NALR` and `NANT`. The operations document
holds them and their layouts. They are assigned once, like every value above,
and no value in either list is ever reused.

### 2.3 Common header

Purpose: the 32-byte header that begins every structure of this document. It
carries identity and versioning in one place, so that a tool recovering a
damaged disc can scan the raw medium for the project magic and read enough
to find the structure's own end.

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u8[8] | `magic_project` | ASCII `"NOAHSARK"`. |
| 8 | 8 | u8[8] | `magic_kind` | ASCII kind name, zero-padded to 8 bytes, section 2.2. Compared as all 8 bytes. |
| 16 | 2 | u16 | `version_major` | Refuse an unknown value. |
| 18 | 2 | u16 | `version_minor` | Accept an unknown value; read `min(header_len, known length)`. |
| 20 | 2 | u16 | `header_len` | Common header plus the fixed body. |
| 22 | 2 | u16 | `reserved_u16` | Zero. |
| 24 | 8 | u64 | `reserved_u64` | Zero. |

Field rules. `header_len` is the common header plus the structure's fixed
body, that is at least 32. A reader computes the end of the fixed body from
`header_len`, never from its own compiled size, and skips `header_len -
known_len` bytes when `header_len` is larger than the reader knows.

Evolution rule. A `version_minor` bump may only append fields to the end of
the fixed body, and an old reader must be able to read the structure
correctly while ignoring them. Any change an old reader cannot ignore is a
`version_major` bump, and an old reader refuses it. This is the whole
compatibility mechanism: a minor bump duplicates nothing a major bump does
not already cover, and a major bump duplicates nothing a minor bump does not
already cover.

A record inside a structure never carries this header.

Every object file is the common header, then the 32-byte object header of
section 6.1, then the kind body, then variable data. An object payload no
longer carries its own magic or version fields; those live once, in the
common header.

Hash and CRC coverage, and reader checks, are stated once per structure in
sections 2.7, 2.8 and 12.1; this section states the shape only.

### 2.4 Strings

Purpose: a variable-length text field inside a structure.

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 1 | u8 | `encoding` | 0 = UTF-8. Other values reserved. |
| 1 | 3 | u8[3] | `reserved` | Zero. Keeps `length` aligned. |
| 4 | 4 | u32 | `length` | Byte count of the string data. |
| 8 | `length` | u8[] | `data` | The bytes, as found. No terminator. |

### 2.5 Registries

A registry id is assigned once. It is never reused and never renumbered.

**Hash algorithm registry.** The values are multicodec codes.

| Code | Name | Digest bytes | Status |
|---:|---|---:|---|
| 0x12 | `sha2-256` | 32 | Phase 1 writes it. Every reader must read it. |
| 0x1e | `blake3` | 32 | Reserved for a later version. A Phase 1 writer never emits it. |
| 0x13 | `sha2-512` | 64 | Reserved. Not used in version 1. |
| 0x1020 | `sha2-512-256` | 32 | Reserved for a later version. A Phase 1 writer never emits it. |
| 0xb220 | `blake2b-256` | 32 | Reserved. |

A digest stays 32 bytes for every algorithm in this table. A future algorithm
whose digest is longer needs a new major version.

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

**Object kind registry.** Section 6.1 repeats it with detail.

| Id | Name |
|---:|---|
| 1 | `chunk` |
| 2 | `blob` |
| 3 | `tree` |
| 4 | `snapshot` |

A ref is a row of the REFS table, section 11.2. It is not an object kind and
carries no id in this registry.

**File role registry.** Section 11.1 states the full table; this is the
registry index. Ids 1 to 14 name the roles a Files row in INDEX may hold: the
files of a run, not the catalog tables' own kinds, which section 2.2's
`magic_kind` list already names.

**Disc filesystem profile registry.** Section 7.2 states what a profile fixes.

| Id | Name | Filesystem | Append mechanism | Status |
|---:|---|---|---|---|
| 0 | `oneshot` | UDF 2.01. Phase 1 builds UDF 2.01 only. | None in Phase 1. The disc is POW-formatted and left open, so a Phase 2 append can still reach it. | **Default. Phase 1.** |
| 1-255 | reserved | | | |

**Media type registry.**

| Id | Name | Status |
|---:|---|---|
| 1 | `BD-R SL 25 GB` | **Default. Phase 1.** |
| 2 | `BD-R DL 50 GB` | Phase 1. |
| 3 | `BD-R XL 100 GB` | Phase 1. |
| 4 | `BD-R XL 128 GB` | Phase 1. |
| 5-255 | reserved | |

`media_type` is informational. A reader never rejects a value it does not
know, and no rule depends on it. Capacity comes from `capacity_sectors` and
`capacity_forced_sectors`.

**FEC scheme registry.**

| Id | Name | Field | Shards | Status |
|---:|---|---|---:|---|
| 0 | `none` | - | - | **Default. Phase 1.** No FEC. A run with this scheme carries no checksum column and no parity files (section 10.8). |
| 1 | `rs255-gf8` | GF(2^8) | 255 | Phase 1. A stripe is `k + 1 + m = 255` blocks; the code is over the `k + m` data and parity shards (section 10.1). |
| 2-255 | reserved | | | |

### 2.6 Version policy

Three version numbers are independent of one another: the document version
that heads this file, the program version of the software that reads and
writes the format, and each structure's own `version_major` and
`version_minor` in its common header. A bump to one never implies a bump to
another.

Every structure's `version_major` is 1 and `version_minor` is 0 as this
document stands. Until the document reaches version 1.0.0 the on-disc format
is not frozen: a structure's layout may change without a version bump, and a
disc burned under a pre-1.0.0 document version carries no compatibility
promise to a later one. NOTES.md's change log states what changed at each
document version.

`version_major` changes when an old reader would misread the structure. A
reader computes the end of a structure's fixed body from `header_len`
(section 2.3), not from its compiled size, and skips `header_len -
known_len` bytes.

Every structure has a fixed body size under its `version_major`, growing only
by a major bump. A minor bump may only append fields to the end of the fixed
body, giving meaning to what was reserved space, and an old reader must be
able to read the structure correctly while ignoring the appended fields. Any
change an old reader cannot ignore is a major bump.

`tool_version` (sections 7.5 and 7.6) is informational only. It never gates
what a reader accepts; a reader never refuses a structure on the strength of
its value.

### 2.7 Hash coverage per structure

Every hash field that names another structure covers all bytes of that
structure as they lie on the medium, with every CRC already filled in. No hash
is computed over a structure whose CRC is zeroed.

| Field | In | Covers | Algorithm |
|---|---|---|---|
| `index_hash` | Run header (section 7.6) | Every byte of `INDEX.bin`. | Run header `hash_algo`. |
| `prev_run_hash` | Run header | The 512 bytes of the previous run header, CRC included. | Run header `hash_algo`. |
| `prev_disc_super_hash` | Disc superblock (section 7.5) | The 2048 bytes of the previous disc's superblock, CRC included. | Superblock `hash_algo`. |
| `run_hash` | DISCS row (section 11.3) | The 512 bytes of that run's `RUN.bin`, CRC included. | Table `hash_algo`. |
| `content_id` | Object file name | The uncompressed payload only (section 3.1). Never the header. | Run's `hash_algo`. |
| `content_id` | INDEX Objects row (section 11.1), Prereqs row | The referenced object, under the run's `hash_algo`. | Run's `hash_algo`. |
| `file_hash` | INDEX Files row (section 11.1) | Every byte of the named file. | Run's `hash_algo`. |
| Block digest | Checksum block (section 10.3) | The 2048 bytes of one data block as they lie in the FEC stream. | SHA-256, the first 8 bytes of the 32-byte digest. |

Host-only structures carry hash fields of their own. None of those bytes
reaches a disc, and the operations document holds their coverage table.

### 2.8 CRC coverage per structure

| Structure | Field | Covers |
|---|---|---|
| Object header (section 6.1) | `header_crc32c` | The common header plus the object header. |
| Blob, tree and snapshot payloads (sections 6.4, 6.5, 6.14) | none | The content id covers the whole payload, records included. |
| Disc superblock (section 7.5) | `super_crc32c` | Every byte before the field. |
| Run header (section 7.6) | `header_crc32c` | Every byte before the field. |
| INDEX (section 11.1) | `body_crc32c`, `header_crc32c` | Every table row; the common header plus the fixed body. |
| Checksum block header (section 10.3) | `header_crc32c` | The fixed header. The digests are checked as section 10.3 states. |
| REFS (section 11.2) | `header_crc32c`, `body_crc32c` | The common header plus the fixed body; every row. |
| DISCS (section 11.3) | `header_crc32c`, `body_crc32c` | The common header plus the fixed body; every row. |

Host-only structures carry CRC fields of their own. None of those bytes
reaches a disc, and the operations document holds their coverage table.

### 2.9 Limits

A writer refuses an input that exceeds a limit of this table and names the
limit. A reader refuses a structure that exceeds one and names the limit.

| Item | Limit | Where it is fixed |
|---|---:|---|
| Digest length | 32 bytes | Section 3.3. A longer digest needs a new major version. |
| Tree entry name | 1 to 4095 bytes | `name_len`, section 6.6. |
| Tree entries per directory | 2^32 - 1 | `entry_count`, section 6.5. |
| Blob entries | 2^64 - 1 | `entry_count`, section 6.4. |
| Tree entry length | 2^32 - 1 bytes | `entry_len`, section 6.6. |
| TLV payload | Always inline. At most `entry_len` minus the 112-byte fixed header minus the TLV's own 8-byte prefix, that is at most `2^32 - 1 - 112 - 8` bytes (4,294,967,175), and far less once the name, the content area and any other TLV of the entry are counted. | Section 6.9. |
| Snapshot metadata TLVs | 65,535 per snapshot | `meta_count`, section 6.14. |
| Ref name | 1 to 40 bytes | Section 6.17. |
| Disc label | 64 bytes, in the superblock and in the DISCS row alike | Sections 7.5 and 11.3. |
| Run sequence number | 1 to 9,999,999,999 | The 10-digit run directory name, section 8.3. |
| Objects per run | 2^32 - 1 | `object_count`, section 11.1, is u32, so this is the limit INDEX can index; a writer refuses to pack a run past it. |
| File size | 2^64 - 1 bytes | Every size field is u64, section 2.1. |
| Disc capacity | 2^64 - 1 sectors, a physical medium unit (section 2.5). | Section 7.5. |
| FEC geometry | `k = 231`, `m = 23` | Section 10.1. |
| On-disc object name | 68 characters | Section 3.4. |
| Any on-disc name | 126 characters | Section 8.8. |
| Any on-disc path | under 220 characters | Section 8.8. |

The host limits of the burn plan, the commit bundle, the shelf note and the
host filesystems are not in this table. The operations document holds them.

### 2.10 Trust boundaries and safety invariants

- Bytes read from a disc are untrusted until the content id verifies.
- A local index is untrusted. Every cache answer is confirmed against INDEX
  before it is used to drop data.
- Names inside a tree object are untrusted.
- A symlink target is data. A restorer never traverses it.
- A source on an NFS or SMB mount is untrusted for metadata. The snapshot
  records the source type so that a restore can warn.
- The restore safety invariants are host behaviour. The operations document
  states them.
- Encryption, signing, access control and metadata secrecy are not defended in
  version 1.

---
## 3. Identity and hashing

### 3.1 The content id rule

The content id of an object is the hash of its uncompressed payload bytes.
Nothing else enters the id. The object kind, the chunker profile, the
compression algorithm, the object header, a salt and a key never enter the id.

A reader verifies an object by hashing the payload after decompression and
comparing the digest with the name. A mismatch is a hard error.

### 3.2 Hash algorithms

SHA-256 is the only algorithm this version writes.

Digests are never truncated. A digest is 256 bits. The 8-byte digests of the
checksum column are not content ids.

### 3.3 Digest fields in records

A digest field is always 32 bytes: the digest, left-aligned, zero-padded,
whatever the algorithm length.

The algorithm of a digest is stated by the containing structure's `hash_algo`
and `digest_len`, or by a `hash_algo` field inside the record where its table
lists one. A structure never holds a digest whose algorithm neither place
states.

### 3.4 Text form

The text form of a content id is the lowercase hex of the multihash bytes:
algorithm code varint, digest length varint, digest bytes. It is 68 hex
characters over the charset `[0-9a-f]`.

### 3.5 Fan-out on disc

Object files use a hex fan-out over the digest, not over the multihash prefix.
`d0` and `d1` are the first two hex digits of the digest.

```
/NOAHSARK/objects/<d0><d1>/<full 68-character text form>     chunks, blobs, trees
/NOAHSARK/snapshots/<full 68-character text form>            snapshots
```

There are two object roots: `objects/` for chunks, blobs and trees,
and `snapshots/` for snapshots. A snapshot is split out on its own because a
reader locates one by name through REFS before it holds any other object.

The file name is the full 68-character multihash hex. It is never stripped,
never shortened and never split.

Fan-out is one level by default. Two levels are allowed under filesystem
profile 0 and profile 1 only, as `objects/<d0><d1>/<d2><d3>/<id>`. Profile 2
allows one level only.

The superblock records the choice in `fanout_levels`.

Metadata objects, that is blobs, trees and snapshots, are written before data
objects, that is chunks, inside a run (section 8.7). That
ordering is a writer rule about fill order, not a rule about which directory
a kind lives in: every non-snapshot object shares the one `objects/` root.

### 3.6 Hash epochs and cross-epoch references

An epoch is a maximal run of runs that share one hash algorithm. The
configured current algorithm names the algorithm for new objects.

Every run header records the algorithm of the objects in that run. Every disc
superblock records the algorithm of its first run. Old discs keep their
algorithm forever. Nothing rewrites them.

The tree graph below one snapshot is single-algorithm: the root tree, every
tree, every blob and every chunk id below it use the `hash_algo` of the
snapshot header. Only the three fields below may name an object under another
algorithm, and each states its algorithm.

| Field | Where | Algorithm stated by |
|---|---|---|
| `parent` | Snapshot payload header | `parent_hash_algo` in the same header. |
| `content_id` | INDEX Prereqs row | The run header of `run_seq`, which holds the object. |
| `snapshot_id` | Ref record | `hash_algo` in the same record. |

Every other digest uses the algorithm of its containing structure. A table
that maps an object id under an old algorithm to the id of the same bytes
under a new algorithm is a later-version structure; when a later document
version defines it, it gets its own `magic_kind`.

---

## 4. Chunking

### 4.1 Algorithm

The chunker is FastCDC, 2020 variant, with a 64-bit Gear hash and
normalization level 2.

The one-byte-per-step pseudocode of section 4.2 is the only normative
definition of the cut points. This document defines no two-byte variant. Any
speed optimization must give exactly the cut points of section 4.2.

The Gear table and the mask constants are frozen. They are part of the on-disc
format.

### 4.2 Cut point rule

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

1. `fp` is an unsigned 64-bit integer. The shift and the add wrap modulo 2^64.
   Bits shifted out of bit 63 are discarded.
2. `data[i]` is indexed from the start of the current chunk, not from the start
   of the file. `len` is the number of bytes left in the file from the chunk
   start.
3. The loop starts at `i = min` and a cut returns `i + 1`, so the shortest
   chunk a cut can produce is `min + 1` bytes. The pseudocode must not be
   changed to make the minimum exactly `min`.
4. The chunker must not evaluate the hash before offset `min`. A file shorter
   than `min` is exactly one chunk.

### 4.3 Chunker profiles

`max = 4 * avg` and `min = avg / 4` in every profile.

**Chunker profile registry.**

| Id | Name | min | avg | max | NC level |
|---:|---|---:|---:|---:|---:|
| 1 | `P3` | 512 KiB | 2 MiB | 8 MiB | 2 |
| 2 | `P4` | 1 MiB | 4 MiB | 16 MiB | 2 |
| 3 | `P5` | 2 MiB | 8 MiB | 32 MiB | 2 |
| 4-255 | reserved | | | | |

P4 is the default chunker profile.

### 4.4 Profile recording and change

Every run header records the profile by name and by full value: id, min, avg,
max, NC level and Gear table id.

A reader never needs the profile. A reader follows content ids only.

A writer must never change the Gear table or the mask constants under an
existing profile name. A Gear table change needs a new `gear_table_id` and a
new profile name.

There is no rechunk operation. New runs use a new profile. Old discs keep
theirs.

### 4.6 Zero regions and sparse files

There is no extent table in the format for sparse regions.

An all-zero region cuts into identical chunks of exactly `max` bytes. A writer
relies on the zero-chunk golden vector for this property, not on the statement.

A restorer detects an all-zero chunk by scanning its bytes, never by comparing
its id to a constant.

`SEEK_HOLE` and `SEEK_DATA` are a speed optimization only. They must not change
the object stream. A file read with and without the optimization must produce
the same chunk ids.

The `SPARSE` flag of a tree entry is a hint for the restorer. It is not a data
structure.

A writer must not special-case the maximum-size zero chunk, and a restorer must
not depend on it.

### 4.7 Determinism

The chunker must be deterministic. The same input bytes give the same cut
points on every platform, with any buffer size and any read pattern.

### 4.8 Gear table

The table for `gear_table_id = 1` is defined by this rule:

```
seed = "noahsark/gear/v1"                      # 16 ASCII bytes, no terminator

for i in 0 .. 255:
    input      = seed || u8(i)                 # 17 bytes
    digest     = SHA-256(input)                # 32 bytes
    Gear[i]    = little-endian u64 of digest[0 .. 7]
```

An implementation generates the table exactly once, checks it into the source
tree as a literal array, and never regenerates it from a dependency.

An implementation may instead adopt the Gear table of a named published FastCDC
implementation. It must then name the implementation, the exact version or
commit, and the file and line where the table appears; copy the table verbatim
into the source tree; assign a new `gear_table_id` that is not 1; and assign a
new chunker profile name, because the cut points differ. The table is never
changed under an existing `gear_table_id`.

### 4.9 Mask constants

The masks are derived from the profile, not from the table. They are defined by
the normalization level 2 formula of FastCDC, Xia et al. 2020. For an average
chunk size of `2^b` bytes:

```
mask_s = spread_mask(b + 2)      # more one-bits: cutting is less likely
mask_l = spread_mask(b - 2)      # fewer one-bits: cutting is more likely
```

`spread_mask(n)` sets `n` bits, distributed evenly over the high 32 bits of
the 64-bit word and never in the low 32 bits. The low bits of a Gear hash
depend only on the last few bytes, so a low-bit mask would make the effective
window tiny. The rule is:

```
spread_mask(n):                       # 1 <= n <= 32
    mask = 0
    for j in 0 .. n-1:
        mask |= 1 << (63 - floor(j * 32 / n))
    return mask
```

The `n` bit positions are distinct because `32 / n >= 1`. Bit 63 is always
set.

Frozen values for the three profiles:

| Profile | avg | b | `mask_s` bits | `mask_s` value | `mask_l` bits | `mask_l` value |
|---|---:|---:|---:|---|---:|---|
| P3 | 2 MiB | 21 | 23 | `0xEEDDBB7600000000` | 19 | `0xD6B5AD6A00000000` |
| P4 | 4 MiB | 22 | 24 | `0xEEEEEEEE00000000` | 20 | `0xDADADADA00000000` |
| P5 | 8 MiB | 23 | 25 | `0xF7BBDDEE00000000` | 21 | `0xDB6DB6DA00000000` |

The six values follow from the rule. The rule is the authority; the printed
values let a reader check an implementation by eye. The implementation must
compute them once, check them in as literals, and cover them with the golden
vectors of section 13.

---

## 5. Compression

### 5.1 Order of operations

The order is fixed:

1. Chunk the stream.
2. Hash the uncompressed chunk bytes. That digest is the content id.
3. Compress the chunk bytes.
4. Write the object header, then the compressed bytes.

A writer must never compress a whole file before chunking.

### 5.2 Header fields

The common object header records the result: `compression` is the algorithm id,
`payload_len` is the uncompressed length, `stored_len` is the number of bytes
written.

A reader decompresses `stored_len` bytes into `payload_len` bytes and then
verifies the content id. A length mismatch is a hard error.

### 5.3 Algorithm and frame parameters

The default algorithm is zstd at level 3.

**Frame parameters.** The level alone does not fix the stored bytes, so the
frame parameters are fixed here as well. A writer emits one zstd frame per
payload, with:

- the single-segment flag set, so the frame carries no window descriptor and a
  decoder allocates the whole content size;
- the frame content size present in the frame header, equal to `payload_len`;
- no frame checksum, because the content id already covers the payload;
- no dictionary, so `Dictionary_ID` is absent;
- the encoder's default window for the configured level, which the
  single-segment flag then makes equal to the content size;
- no skippable frame before or after it.

A reader must accept any valid zstd frame, whatever its parameters, because an
object stays valid forever. A writer must emit the parameters above.

Byte identity of a compressed payload additionally requires the **same
writer**: the same `tool_version` registry id and the same `tool_version`
version value (section 7.5). A later encoder may produce different, equally
valid bytes at the same level. This never changes a content id, because the id
is the hash of the uncompressed payload (section 3.1). It does change
`stored_len`, and therefore the byte offset of every object after it in the
run's FEC stream, so a golden vector that names compressed bytes also names
the `tool_version` that produced them (section 13).

Conformance is judged on ids, structures and readability, never on compressed
bytes. Two writers with different `tool_version` values that produce the same
ids, the same structures and a readable disc both conform.

### 5.4 Minimum gain

If compression saves less than the configured minimum gain of the chunk, the
writer stores the chunk with compression id 0.

The writer applies the heuristic per chunk. It must not apply it per file.

### 5.5 Compression and identity

Compression never affects a content id. Two writers with different compression
settings produce identical ids for identical data.

### 5.6 What is never compressed

- The disc superblock.
- Every run header copy.
- INDEX.
- REFS and DISCS.
- `README.txt`.

---

## 6. Objects

### 6.1 Object kinds and the object header

Purpose: every stored object begins with the 32-byte common header of section
2.3, naming `magic_kind` as `CHUNK`, `BLOB`, `TREE` or `SNAPSHOT`, then this
32-byte object header, then the kind body, then any variable data. An object
file therefore carries no magic or version fields of its own beyond the
common header: one object file is common header, object header, kind body,
variable data.

| Id | Kind | Payload | References | Stored as |
|---:|---|---|---|---|
| 1 | `chunk` | Opaque bytes. | None. | One file. |
| 2 | `blob` | Ordered chunk ids and lengths. | Chunks, and other blobs. | One file. |
| 3 | `tree` | One directory. | Trees, blobs, chunks. | One file. |
| 4 | `snapshot` | Root tree, parent, generation, text. | Root tree, parent snapshot. | One file. |

A ref is a named pointer to a snapshot. It is a row of the REFS table
(section 11.2), never an object file and never a value of `kind`. A reader
that finds a `kind` value outside 1 to 4 refuses the record and names the
structure.

A chunk carries no reference.

Object header, 32 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 1 | u8 | `kind` | Object kind registry, this section. |
| 1 | 1 | u8 | `hash_algo` | Multicodec code. 0x12 sha2-256 in version 1. |
| 2 | 1 | u8 | `digest_len` | Digest length in bytes. 32 in version 1. |
| 3 | 1 | u8 | `compression` | Compression registry. 0 none, 1 zstd, 2 lz4. |
| 4 | 1 | u8 | `crypto` | 0 plaintext. Other values reserved. |
| 5 | 1 | u8 | `reserved_u8` | Zero. |
| 6 | 2 | u16 | `reserved_u16` | Zero. Keeps `payload_len` aligned. |
| 8 | 8 | u64 | `payload_len` | Uncompressed payload length in bytes. |
| 16 | 8 | u64 | `stored_len` | Bytes on the medium after this header. |
| 24 | 4 | u32 | `header_crc32c` | CRC-32C over the common header and bytes 0 to 23 of this header. |
| 28 | 4 | u32 | `reserved_u32` | Zero. |

Field rules. `kind` takes a value from the object kind registry, 1 to 4.
`digest_len` is 32 in version 1. `payload_len` is the uncompressed length.
`stored_len` is the number of bytes on the medium after the common header and
the object header.

Hash and CRC coverage. `header_crc32c` covers the common header, bytes 0 to
31, and bytes 0 to 23 of the object header. The payload is covered by the
content id, which is the hash of the uncompressed payload alone and never of
either header.

Reader checks. Check `magic_project` and `magic_kind`. Check `version_major`.
Read `header_len` (section 2.3). Verify
`header_crc32c` before using any field. Read `stored_len` bytes, decompress
them into exactly `payload_len` bytes, hash the result and compare it with
the digest in the file name.

### 6.2 Chunk

Purpose: opaque file content bytes, addressed by the hash of those bytes.

A chunk object is the common header, the object header, then the payload
bytes.

Reader checks. Verify the header CRC, decompress `stored_len` into
`payload_len` bytes, and verify the content id.

### 6.4 Blob

Purpose: the ordered chunk ids of one file, held outside the tree entry.

Every regular file, small files included, is stored as one blob object. There
is no inline form. The blob holds the file's chunk ids in file order, however
many there are, including zero entries for an empty file.

Blob payload, after the common header and the object header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u64 | `entry_count` | Number of entries. |
| 8 | 8 | u64 | `total_size` | Sum of `length` over all entries. |
| 16 | 2 | u16 | `entry_size` | 48. |
| 18 | 1 | u8 | `hash_algo` | Multicodec code. |
| 19 | 1 | u8 | `digest_len` | 32. |
| 20 | 1 | u8 | `level` | 0 = entries are chunks. 1 = entries are blobs. |
| 21 | 3 | u8[3] | `reserved` | Zero. |
| 24 | | | `entries` | `entry_count` records of 48 bytes. |

Blob entry, 48 bytes, in file order:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | Chunk id, or child blob id. |
| 32 | 8 | u64 | `length` | Uncompressed bytes this entry contributes. |
| 40 | 8 | u64 | `file_offset` | Offset of this entry inside the file. |

Field rules. Entries are in file order, keyed by ascending `file_offset`.
`entry_size` is 48. `total_size` is the sum of `length` over all entries.

A version 1 writer never writes a `level` 1 blob. A version 1 reader must
accept and follow `level` 1. A reader refuses a `level` above 1 and names the
value.

Reader checks. Verify the content id of the object before using any entry.
Refuse a `level` above 1.

### 6.5 Tree

Purpose: one directory, with one entry per child.

Tree payload, after the common header and the object header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `entry_count` | Number of entries. |
| 4 | 4 | u32 | `reserved_u32` | Zero. Keeps `entries` 8-byte aligned. |
| 8 | | | `entries` | Entries, back to back, each self-delimiting. |

Field rules. Entries follow back to back, each self-delimiting through its
own `entry_len`. Metadata lives inline in the tree entry, not in a separate
node object.

Tree entries are sorted by raw name bytes, ascending, unsigned. A directory
name compares as if a `/` byte were appended. Sorting is mandatory.

Reader checks. Verify the content id. Validate every entry name at parse time
by section 6.11.

### 6.6 Tree entry fixed header

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `entry_len` | Total entry length including all variable areas. Multiple of 8. |
| 4 | 2 | u16 | `header_len` | 112 in version 1. A reader skips the excess. |
| 6 | 1 | u8 | `entry_type` | 1 regular, 2 directory, 3 symlink, 4 chardev, 5 blockdev, 6 fifo, 7 socket. 0 is invalid. |
| 7 | 1 | u8 | `entry_flags` | See section 6.7. |
| 8 | 8 | u64 | `size` | Regular files only. 0 otherwise. |
| 16 | 8 | u64 | `hardlink_group` | Reserved for a later phase. A Phase 1 writer writes 0. See section 6.13. |
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
| 96 | 4 | u32 | `content_len` | Bytes in that area. See section 6.8. |
| 100 | 4 | u32 | `ext_off` | Offset to the TLV area. 0 when `ext_len` is 0. |
| 104 | 4 | u32 | `ext_len` | Bytes in the TLV area, padding included. |
| 108 | 2 | u16 | `name_off` | Offset to the name bytes. 112 in version 1. |
| 110 | 2 | u16 | `name_len` | Name length in bytes, 1 to 4095. No terminator. |

Field rules. `header_len` is 112 in version 1, and a reader skips the excess.
All offsets are relative to the first byte of the entry.

The mandatory fields are `entry_type`, `mode`, `uid`, `gid`, `size`,
`mtime_sec`, `mtime_nsec` and the name. The optional time fields carry an
`ABSENT` flag: `atime`, `ctime` and `btime`.

Every `_sec` time field is `i64`, seconds. Every `_nsec` field is `u32`,
nanoseconds in [0, 999999999].

File type is `entry_type`, a u8 enum. `mode` is a u32 holding permission bits
only. The type is not in the mode.

`uid` and `gid` are u32, with `0xFFFFFFFF` meaning unknown.

### 6.7 Entry flags

| Bit | Name | Meaning |
|---:|---|---|
| 0 | `HARDLINK_MEMBER` | Reserved for a later phase. A Phase 1 writer clears this bit. See section 6.13. |
| 1 | `ATIME_ABSENT` | `atime_sec` and `atime_nsec` carry no information. |
| 2 | `CTIME_ABSENT` | `ctime_sec` and `ctime_nsec` carry no information. A writer sets it when the `metadata.ctime` configuration key is false, and when that key is true and the source reported no ctime. |
| 3 | `BTIME_ABSENT` | `btime_sec` and `btime_nsec` carry no information. |
| 4 | `SPARSE` | The source file had holes. The restorer punches holes, as section 4.6 states. |
| 5 | `METADATA_PARTIAL` | The source read failed for at least one metadata field. |
| 6 | reserved | Zero. A version 1 writer never sets this bit. |
| 7 | `UNSTABLE` | The file changed while it was being read, and no earlier consistent version existed. The content is one possible read of a moving file. The operations document states when a writer sets it and what a restore does with it. |

The `entry_flags` byte is full. A new per-entry fact goes into a TLV, never
into this byte. A critical new fact takes a TLV type in the reserved critical
range 0x8000 to 0xBFFF.

ctime storage is conditioned on one configuration key. A writer stores ctime
when `metadata.ctime` is true and the source reports one, and sets
`CTIME_ABSENT` otherwise. The operations document holds the key and its
default.

`UNSTABLE` is set when the file changed while it was being read and no earlier
consistent version existed. A snapshot never holds torn content without a flag
that says so. When the parent snapshot holds an entry for the path, the writer
reuses that entry and discards the new chunk list; otherwise it keeps the new
content and sets `UNSTABLE`.

### 6.8 Variable areas and the content area

| Area | Start | Length | Content |
|---|---|---|---|
| Name | `name_off` | `name_len` | Raw bytes of exactly one path component. |
| Content refs | `content_off` | `content_len` | See below. |
| Extension TLVs | `ext_off` | `ext_len` | TLV records, sorted. |

The areas appear in that order, each 8-byte aligned, with zero padding
between them and after the last one, as section 6.6 states.

Content area by entry type:

| `entry_type` | `content_len` | Content |
|---|---|---|
| 1 regular | 32 | One blob id. The blob holds the file's chunk ids, section 6.4, with zero entries for an empty file. There is no inline form. |
| 2 directory | 32 | One tree id. |
| 3 symlink | 0 | The target is TLV `SYMLINK_TARGET`. |
| 4, 5, 6, 7 | 0 | No content. |

The order of the variable areas is fixed: the 112-byte fixed header, the name,
zero padding to the next 8-byte boundary, the content reference area, zero
padding to the next 8-byte boundary, the TLV area, zero padding to `entry_len`.
`name_off` is therefore 112, `content_off` is the name's end rounded up to 8,
and `ext_off` is the content area's end rounded up to 8. An area of length 0 is
absent, occupies no bytes and has offset field 0.

`entry_len` is the whole entry rounded up to a multiple of 8. Every padding
byte is zero.

A reader takes each area from its offset and length fields and must not assume
the order. A writer must produce the fixed order.

### 6.9 Extension TLV record

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 2 | u16 | `tlv_type` | Registry, section 6.10. |
| 2 | 2 | u16 | `tlv_flags` | bit0 `CRITICAL`. Bits 1 and 2 reserved: earlier document versions gave them to a spill mechanism, since removed; a TLV payload is always inline. Bits 3 to 15 reserved, zero. |
| 4 | 4 | u32 | `tlv_len` | Payload bytes, excluding this prefix and excluding padding. |
| 8 | `tlv_len` | u8[] | `payload` | The value, always inline. |
| | pad | u8[] | | Zero bytes to the next 8-byte boundary. |

TLVs are sorted ascending by `tlv_type`, then by payload bytes. Canonical order
is mandatory.

A registered TLV type, 0x0001 to 0xBFFF, appears at most once per entry. Only a
vendor type, 0xF000 to 0xFFFF, may repeat.

A reader that meets an unknown TLV with `CRITICAL` set must refuse the entry. A
reader that meets an unknown non-critical TLV must keep it on copy and report
it on restore. A writer never emits a TLV that it does not implement.

A TLV payload is always inline; there is no spill form. `tlv_len`, and
therefore a TLV's payload, is bounded by what the entry itself can hold: the
entry's own `entry_len` is a u32, so a TLV payload can be at most
`entry_len` minus the 112-byte fixed header minus this record's own 8-byte
prefix, which is at most `2^32 - 1 - 112 - 8`, that is 4,294,967,175 bytes,
and in practice far less once the name, the content area and any other TLV
of the same entry are counted.

### 6.10 TLV type registry

| Type | Name | Critical | Payload |
|---:|---|---|---|
| 0x0001 | `SYMLINK_TARGET` | yes | Raw bytes. Mandatory when `entry_type` is 3. Never validated as UTF-8. |
| 0x0002 | `USER_NAME` | no | UTF-8 bytes. |
| 0x0003 | `GROUP_NAME` | no | UTF-8 bytes. |
| 0x0004 | `ROOT_PATH` | no | Raw bytes of a source root's absolute path. Only on an entry of the root tree; source roots are defined in the operations document. |
| 0x0010 | `XATTR` | no | Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a `version_minor` bump. A Phase 1 reader treats it as an unknown TLV. |
| 0x0011 | `ACL_ACCESS` | no | Reserved. Same rule as `XATTR`. |
| 0x0012 | `ACL_DEFAULT` | no | Reserved. Same rule as `XATTR`. |
| 0x0013 | `ACL_NFS4` | no | Reserved. Same rule as `XATTR`. |
| 0x0020 | `LINUX_ATTR` | no | Reserved. Same rule as `XATTR`. Item layout: `u32` `FS_IOC_GETFLAGS` bitmask, when a later phase defines it. |
| 0x0021 | `BSD_FLAGS` | no | Reserved. Same rule as `XATTR`. Item layout: `u32` `st_flags`, when a later phase defines it. |
| 0x0030 | `WIN_ATTRS` | no | Reserved. Same rule as `XATTR`. Item layout: `u32` `FILE_ATTRIBUTE_*` bitmask, when a later phase defines it. |
| 0x0031 | `WIN_SD` | no | Reserved. Same rule as `XATTR`. |
| 0x0032 | `WIN_ADS` | no | Reserved. Same rule as `XATTR`. |
| 0x8000-0xBFFF | reserved critical | yes | Future critical extensions. |
| 0xF000-0xFFFF | vendor | no | Never critical. |

Phase 1 stores Unix permissions only: `mode`, `uid` and `gid` in the tree
entry fixed header. `XATTR`, `ACL_ACCESS`, `ACL_DEFAULT`, `ACL_NFS4`,
`LINUX_ATTR`, `BSD_FLAGS`, `WIN_ATTRS`, `WIN_SD` and `WIN_ADS` are reserved
TLV types that carry extended attributes, file flags or an access control
list. A Phase 1 reader does not implement any of their item layouts, so it
applies the unknown-TLV rule above to each: keep it on copy and report it on
restore.

`SYMLINK_TARGET` is mandatory when `entry_type` is 3 and is never validated as
UTF-8.

### 6.11 Name validation

A tree entry name is exactly one path component. The parser must reject at
parse time a name that is empty, that is `.` or `..`, or that contains a `/`, a
`\` or a NUL byte.

A name is not required to be valid UTF-8.

### 6.12 What is never stored

Inode numbers, source filesystem device ids and link counts are never stored.

### 6.13 Hardlinks

Phase 1 stores every hardlinked path as an independent tree entry. Content
dedup already stores the shared data once, so each entry carries its own full
content reference. A Phase 1 writer writes `hardlink_group` as 0 on every
entry, and clears `HARDLINK_MEMBER`. A Phase 1 reader treats an entry as an
independent file whenever it finds `hardlink_group` 0, and it treats an entry
as an independent file even if it finds a non-zero value: Phase 1 does not
group entries.

`hardlink_group` and `HARDLINK_MEMBER` stay reserved for a later phase. A
later phase may define a non-zero `hardlink_group` and a grouping rule, with a
`version_minor` bump.

Restore in Phase 1 does not recreate the source's hardlinks. Each stored entry
is restored as its own file, with its own inode. Link identity is not
preserved.

### 6.14 Snapshot

Purpose: one root tree pointer, a parent pointer, a generation number and the
snapshot's own metadata.

Snapshot payload, after the common header and the object header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `root_tree` | Content id of the root tree. |
| 32 | 32 | u8[32] | `parent` | Content id of the parent snapshot. All zero for a root. |
| 64 | 8 | u64 | `generation` | 1 + parent generation. 1 for a root. |
| 72 | 8 | i64 | `time_sec` | Snapshot time, seconds. |
| 80 | 4 | u32 | `time_nsec` | Nanoseconds. |
| 84 | 4 | i32 | `tz_offset_sec` | Local zone offset at snapshot time. |
| 88 | 8 | u64 | `total_size` | Sum of `payload_len` over the distinct objects that `reachable_object_count` counts, that is over the distinct chunks, blobs and trees reachable from `root_tree`. A chunk that several files share is counted once. It is not the sum of the file sizes. For planning. |
| 96 | 8 | u64 | `reachable_object_count` | Distinct content ids reachable from `root_tree`: chunks, blobs and trees, the root tree included. The snapshot itself is not counted. This is a logical reachability count; it is not comparable to INDEX's `object_count` (section 11.1), which is a physical count. |
| 104 | 1 | u8 | `hash_algo` | Multicodec code of `root_tree` and of every id below it. |
| 105 | 1 | u8 | `chunker_profile` | Chunker profile id used to produce it. |
| 106 | 2 | u16 | `meta_count` | Number of TLV records that follow. |
| 108 | 1 | u8 | `source_type` | Where the source tree was read from. See below. |
| 109 | 1 | u8 | `source_flags` | What the source could not provide. See below. |
| 110 | 1 | u8 | `parent_hash_algo` | Multicodec code of `parent`. Equals `hash_algo` except across an epoch boundary. 0 for a root. |
| 111 | 1 | u8 | `reserved_u8` | Zero. |
| 112 | | | TLV records | `meta_count` records follow. |

Field rules. `generation` is 1 + the parent generation, and 1 for a root.
`parent` is all zero for a root, and `parent_hash_algo` is 0 for a root.

`total_size` is the sum of `payload_len` over the distinct objects that
`reachable_object_count` counts. It is not the sum of the file sizes.

`reachable_object_count` counts distinct content ids reachable from
`root_tree`: chunks, blobs and trees, the root tree included. The snapshot
itself is not counted. It is a logical count and is not comparable to
INDEX's physical `object_count`.

Metadata TLV record:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 2 | u16 | `tag` | Snapshot metadata tag registry, section 6.14: 1 author, 2 host, 3 message, 4 source root, 5 exclude rules, 6 checksum commit. Tags 4 and 5 repeat, one per root, in root order. |
| 2 | 2 | u16 | `flags` | bit0 CRITICAL. |
| 4 | 4 | u32 | `len` | Payload bytes. |
| 8 | `len` | u8[] | `value` | UTF-8 for tags 1 to 3. Raw path bytes for tag 4. Pattern lines for tag 5, as below. Empty for tag 6. |
| | pad | | | Zero to the next 4-byte boundary. |

The snapshot metadata tag registry:

| Tag | Name | Value | Repeats |
|---:|---|---|---|
| 1 | author | UTF-8. | No. |
| 2 | host | UTF-8 host name. | No. |
| 3 | message | UTF-8, from `commit -m`. | No. |
| 4 | source root | Raw path bytes of one source root. | One per root, in root order. |
| 5 | exclude rules | UTF-8 pattern lines of section 6.16, one per line, in rule order, for the root of the preceding tag 4. | One per root, in root order. |
| 6 | checksum commit | Empty. Present when the commit rehashed every file. | No. |
| 7 to 0x7FFF | reserved | | |
| 0x8000 to 0xBFFF | reserved critical | A reader refuses an unknown tag in this range. | |
| 0xF000 to 0xFFFF | vendor | Never critical. | May repeat. |

Tags 4 and 5 repeat, one per root, in root order.

Tag 5 holds exactly two of the three rule sources of section 6.16: every
configured exclude key in configuration order, then every command-line exclude
option in command-line order. An ignore-file rule is never recorded. Each rule
is written as its raw pattern bytes with no normalization and no UTF-8
validation, each followed by one LF byte, 0x0A. The last rule carries its LF
too. A root with no rule gives `len` 0.

`source_type` records where the source tree was read from:

| Id | Name | Meaning |
|---:|---|---|
| 0 | `unknown` | Not recorded. A pre-1.0 writer. |
| 1 | `local` | A local filesystem. Full metadata fidelity. |
| 2 | `snapshot` | A filesystem snapshot of a local filesystem. |
| 3 | `nfs` | An NFS mount. |
| 4 | `smb` | An SMB or CIFS mount. |
| 5 | `bundle` | Imported from a commit bundle (see the operations document). The bundle writer's own source type is in its `BUNDLE.bin`. |

`source_flags` records what the source could not provide:

| Bit | Name | Meaning |
|---:|---|---|
| 0 | `NO_CTIME` | ctime was not trusted, so the quick check used size and mtime only. |
| 1 | reserved | Zero in Phase 1. Phase 1 does not detect hardlink groups; see section 6.13. |
| 2 | `NO_SPARSE` | `SEEK_HOLE` was unavailable, so holes were found by reading. |
| 3 | `SYNTHETIC_IDS` | uid, gid or mode may have been synthesized by the mount. |
| 4 | `CASE_INSENSITIVE` | The source did not distinguish names by case. |
| 5 | `MTIME_SLACK` | An mtime slack was applied in the quick check. |
| 6 | reserved | Zero in Phase 1. See section 6.13. |
| 7 | reserved | Zero. |

A remote source root records its limits in `source_type` and `source_flags`. A
restore prints one warning per set `source_flags` bit, once, before it writes
anything.

Reader checks. Verify the content id. Refuse an unknown metadata tag in the
critical range 0x8000 to 0xBFFF.

### 6.15 The root tree

A snapshot has exactly one synthetic `root_tree`. It holds one entry per source
root, in root order. Roots are ordered by path bytes ascending.

A root entry has `entry_type` 2, directory, and its content area holds the tree
id of the root's own directory. The entry's mode, uid, gid and times are those
of the root directory itself. TLV `ROOT_PATH` carries the raw path bytes,
unencoded.

The entry name is the root's absolute path, encoded so that it is one path
component that section 6.11 accepts. Exactly four bytes are escaped and no
others: `/` becomes `%2F`, `\` becomes `%5C`, NUL becomes `%00`, and `%`
becomes `%25`. The hex digits are uppercase. Every other byte, `.` included, is
kept as it is. Decoding replaces every `%XX` by its byte.

A root whose encoded name would be empty, `.` or `..` is refused at commit
time. An encoded name above 4095 bytes is refused.

### 6.16 Exclude pattern language

Trees are content-addressed, so the pattern language is normative.

Rules and sources. A rule is one pattern line. The rules come from three
sources, in this order: every configured exclude key in configuration order,
then every command-line exclude option in command-line order, then the ignore
files on the path from the source root down to the directory that holds the
entry, root first. A rule in a deeper file comes after a rule in a shallower
one. Inside one file, rules are in line order.

Lines. A blank line and a line whose first byte is `#` are ignored. A leading
`\#` or `\!` is a literal `#` or `!`. Trailing spaces are ignored unless the
last one is escaped with `\`. Matching is on raw bytes, byte for byte,
case-sensitive, with no normalization.

Anchoring. A pattern that contains a `/` other than a trailing one is anchored:
it matches relative to the directory of the rule, which is the source root for
a configuration or command-line rule and the directory that holds the ignore
file otherwise. A leading `/` anchors a pattern in the same way and is then
removed. A pattern with no `/` other than a trailing one matches the name of an
entry at any depth below the rule's directory.

Trailing slash. A pattern that ends in `/` matches a directory only. The `/` is
then removed and the rest is matched as above. A pattern with no trailing `/`
matches an entry of any type.

Wildcards. `*` matches any run of zero or more bytes except `/`. `?` matches
exactly one byte except `/`. `[...]` matches one byte from the set, with `-`
for a range and a leading `!` for the complement. `**` has three forms: a
leading `**/` matches in every directory; a trailing `/**` matches every entry
below the named directory; `/**/` in the middle matches zero or more
directories. Any other `**` is two `*`. A `\` escapes the next byte.

Match order and negation. A pattern whose first byte is `!` is a negation. The
rules are applied in the order above to the path of an entry, relative to the
rule's directory, and the last matching rule wins. If it is a negation the
entry is included, otherwise it is excluded. An entry that no rule matches is
included. An excluded directory is not entered: nothing below it is scanned,
and no later negation can bring anything below it back. The source root itself
is never matched against any rule and cannot be excluded.

### 6.17 Ref

Purpose: a named pointer to a snapshot, stored as a row in the REFS table
(section 11.2), never as a separate object file.

Ref record, 96 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `snapshot_id` | Content id of the snapshot. |
| 32 | 8 | i64 | `time_sec` | When the ref took this value. |
| 40 | 4 | u32 | `time_nsec` | Nanoseconds. |
| 44 | 2 | u16 | `name_len` | Byte length of the name, 1 to 40. |
| 46 | 1 | u8 | `hash_algo` | Multicodec code of `snapshot_id`. |
| 47 | 1 | u8 | `reserved_u8` | Zero. |
| 48 | 40 | u8[40] | `name` | UTF-8, zero-padded. A name above 40 bytes is refused at commit time. The limit is part of the format. |
| 88 | 8 | u64 | `run_seq` | Run that recorded this value. |

Field rules. The table is sorted by `name` bytes ascending, then `time_sec`
ascending, then `time_nsec` ascending, then `run_seq` ascending, then
`snapshot_id` bytes ascending, and it is append-only across runs. Those five
keys are total: no two records of one table share all five.

A reader takes the newest on-disc ref record for each name: the highest
`run_seq`, then the highest `time_sec`, then the highest `time_nsec`.

A ref record with `run_seq` 0 never appears on a disc.

`LATEST` is the reserved ref name for the newest snapshot.

A ref name above 40 bytes is refused at commit time. The limit is part of the
format.

### 6.18 Reserved crypto fields

Encryption and signing are reserved and unused in version 1. The `crypto` byte
of the object header is 0. The disc superblock's reserved region is all zero;
a later version places key material there, at an offset that version
defines.

### 6.19 Canonical ordering

Two identical directories must serialize to identical bytes. The ordering table
below is mandatory.

| Structure | Order key |
|---|---|
| Tree entries | Raw name bytes, ascending, directories compared with a trailing `/`. |
| Tree entry variable areas | Name, then content refs, then TLVs, each 8-byte aligned, then zero padding to `entry_len` (section 6.6). |
| TLV records | `tlv_type` ascending, then payload bytes ascending. |
| Xattr items inside a TLV | Name bytes ascending. |
| Blob entries | File offset ascending. |
| INDEX Objects rows | Content id ascending. |

Every "ascending" in this table, and everywhere else in this document, is an
unsigned bytewise comparison: the first differing byte decides, and a
shorter string that is a prefix of a longer one sorts first.

---
## 7. Disc and run model

### 7.1 The physical disc

A physical disc has a `disc_uuid` of 16 bytes, generated once and never reused;
a human label; a `disc_seq`; a media type; a capacity in sectors taken from the
drive; a disc filesystem profile id; and the hash of the previous disc's
superblock.

`disc_seq` is 0-based and monotonic inside the repository. `run_seq` is 1-based
and monotonic inside the repository, so a `run_seq` of 0 can mean "no run" in
every structure that needs that sentinel.

A `disc_seq` is consumed before anything is built, and it is never given to
another disc. A hole in the sequence is harmless.

### 7.2 Disc filesystem profile

A disc filesystem profile names the filesystem on the disc and the mechanism
that appends to it. The superblock records the id in `fs_profile`, which
records the writer's plan at the first burn and never changes afterwards.

Profile 0 and profile 1 use the same filesystem, so a reader treats them
identically. It reads both with one code path and never branches on the value.

Whether a disc can receive a run is read from three facts: the superblock
`sealed` flag; the close state of the run chain, that is `run_flags` bit 0 of
the newest run header and `state_flags` bit 0 of the newest DISCS row; and the
drive's POW spare state. Any one of them saying closed is enough. `fs_profile`
never enters that decision.

A profile defines exactly six items: the filesystem and its revision; the
image builder command and its options; the name and path limits and the fan-out
depth; the directory layout of fixed-name files; the append mechanism and the
burn command lines; and the cross-OS read matrix.

These items are identical under every profile: the object model and the object
ids, the run structure and the run header, INDEX, the Reed-Solomon parity,
which works over the run's file stream and never over a fixed medium address,
and the packer, the planner and the restorer.

These items exist under profile 1 only: append growth handling and the
never-close policy for a disc that receives more runs. There is no block diff
and no address re-verification, because the format carries no medium address
of its own: an append is safe exactly because the parity covers the files
INDEX lists, never the medium positions those files happen to occupy, and a
damaged or lost filesystem directory is recovered by carving, section 7.10.

Everything NoahsArk writes is an ordinary file under `/NOAHSARK/`. There are no
hidden sectors, no fixed-address structures and no raw areas outside the
filesystem.

### 7.3 The run and the FEC terms

A run is one execution of one burn plan. It is the unit of packing, of the
object index, of the Reed-Solomon parity and of the catalog copy.

A run is self-contained: it carries its own header at its start and again at
its end, its own INDEX, and its own parity.

The disc always has exactly one physical session.

**FEC terms.** This table is a complete forward definition of every term that
sections 7.5 to 7.14 use. Section 10.1 repeats it with the burst bound and the
encoding order. Nothing in sections 7.5 to 7.14 needs a term that is not here.

| Term | Meaning |
|---|---|
| `k`, `m` | The data column count and the parity column count. Version 1 fixes `k = 231` and `m = 23`, so `k + 1 + m = 255`. |
| FEC stream | The run's data files, in the file order INDEX lists them (section 11.1), each file's bytes padded with zero bytes up to a multiple of 2048, concatenated in that order. INDEX is the first file of the stream. `RUN.bin`, `RUN2.bin`, `checksum.bin` and every parity file are outside it. |
| `stream_bytes`, `stream_blocks` | The byte length of the FEC stream, and that length divided by 2048, rounded up: the number of 2048-byte blocks in the stream. |
| `L`, `column_blocks` | The number of blocks in one column. `L = ceil(stream_blocks / k)`. |
| Column | A range of `L` blocks of the FEC stream. Data column `c` is blocks `[c*L, (c+1)*L)` of the stream, for `c = 0 .. k-1`, the last column zero-padded past `stream_blocks` when `stream_blocks` is not a multiple of `k*L`. Column `k`, the checksum column, is the `L` blocks of `runs/<seq>/checksum.bin`. Columns `k+1` to 254 are the `m` parity columns, each the `L` blocks that follow the header block of one `parity/pNNNN.bin` file. |
| Stripe | Block `i` of every column, for `i = 0 .. L-1`. A stripe is 255 blocks: `k` data, 1 checksum, `m` parity. The code covers the `k` data and the `m` parity blocks; the checksum block is outside the code (section 10.3). |

The FEC stream has no relationship to where its bytes physically sit on the
medium. A block index is a position inside the stream, never a medium address.

### 7.4 What the parity does and does not cover

The parity covers exactly the bytes of the FEC stream: the run's data files,
in INDEX's file order, zero-padded per file to a 2048 boundary. Filesystem
metadata, such as a UDF directory record or File Entry block, is never part of
the stream and is never covered by the parity, under any profile.

This is what makes an append safe with no medium-address bookkeeping. An
append changes only the filesystem's own metadata (section 7.9); it never
rewrites a data file's bytes once that file is part of a burned run's stream,
so it never touches anything the parity of that run protects, whatever
address the filesystem happens to place that metadata at.

Under profile 2 the filesystem directory records of every earlier run are
rewritten inside the newest run's own filesystem update. Those bytes are
plain filesystem metadata, not part of any run's FEC stream, so no run's
parity is affected by rewriting them.

### 7.5 Disc superblock

Purpose: the immutable facts of one physical disc, written once as
`/NOAHSARK/DISC.bin` in the first run and never updated.

The superblock is 2048 bytes, exactly one sector. It is written **once**, in
the first run, as the ordinary file `/NOAHSARK/DISC.bin`. It is never
updated.

The superblock holds only **immutable** facts. Mutable disc state comes from
the run chain (section 7.7) and from DISCS (section 11.3). That is what makes
every NoahsArk structure on a disc append-only.

Fixed body, after the 32-byte common header (`magic_kind` `DISC`):

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 32 | 16 | u8[16] | `disc_uuid` | Unique for this physical disc. |
| 48 | 16 | u8[16] | `repo_uuid` | The repository this disc belongs to. |
| 64 | 8 | u64 | `disc_seq` | Monotonic position in the repository. |
| 72 | 8 | u64 | `capacity_sectors` | As reported by the drive at first write. |
| 80 | 8 | u64 | `capacity_forced_sectors` | Forced capacity, section 7.13. Equals `capacity_sectors` when no override was given. |
| 88 | 32 | u8[32] | `prev_disc_super_hash` | Hash of the superblock of the highest `disc_seq` below this one of which the writer holds a verified copy. All zero when there is none, which includes `disc_seq` 0 and the first disc of a repository recreated by `init --repo-uuid`. |
| 120 | 8 | i64 | `created_sec` | Pack time of the first run, seconds: the moment `pack` finalized that run's image. Not the burn time, which is unknown when these bytes are hashed (section 7.6). |
| 128 | 4 | u32 | `created_nsec` | Nanoseconds. |
| 132 | 4 | i32 | `tz_offset_sec` | Local zone offset at that pack time. |
| 136 | 1 | u8 | `media_type` | Media type registry. Informational only; never gates reading. |
| 137 | 1 | u8 | `fs_profile` | Disc filesystem profile registry. |
| 138 | 1 | u8 | `fanout_levels` | 1 by default. 2 is allowed under profile 1 and profile 0 only. |
| 139 | 1 | u8 | `capacity_is_forced` | 1 when `capacity_forced_sectors` is below `capacity_sectors`. |
| 140 | 1 | u8 | `sealed` | 1 when the disc was burned sealed at its first write: `spare:none` and `-dvd-compat` at its first and only write (see the operations document). 0 when the disc was left open. It says nothing about a later `noahsark close`, because the superblock is never updated; the close state of an open disc lives in the run chain and DISCS (sections 7.7 and 11.3). |
| 141 | 3 | u8[3] | `reserved_u8` | Zero. Keeps `label_len` aligned. |
| 144 | 4 | u32 | `label_len` | Byte length of the label. |
| 148 | 64 | u8[64] | `label` | UTF-8, zero-padded. |
| 212 | 4 | u32 | `tool_version` | Writer registry id in the high 8 bits, writer-defined version in the low 24 bits. Informational only; never gates reading (section 2.6). |
| 216 | 4 | u32 | `reserved_u32` | Zero. |
| 220 | 1824 | u8[1824] | `reserved` | Zero. |
| 2044 | 4 | u32 | `super_crc32c` | CRC-32C over bytes 0 to 2043. |

Field rules. The superblock holds only immutable facts. Mutable disc state
comes from the run chain and from DISCS. Every per-run parameter, that is
the hash algorithm, the chunker profile, the compression default, the FEC
scheme and geometry, and the sector size, lives in the run header (section
7.6) only; the superblock never repeats it, so there is nothing for a run
header to override.

`prev_disc_super_hash` names the superblock of the highest `disc_seq` below
this one of which the writer holds a verified copy. It is all zero when the
writer holds none.

`tool_version` is a u32. Its high 8 bits are a writer registry id, and its low
24 bits are a version value that the named writer defines for itself. Registry
id 1 is the reference implementation. Registry id 0 is invalid. Ids 2 to 255
are assigned once, on request, and are never reused. A reader never interprets
the low 24 bits of a writer it does not know; it prints the pair. The run
header records the same value for the run that wrote it (section 7.6).

The superblock needs no second copy. It lies inside the first run's FEC
stream, and the run headers carry the disc uuid, the repository uuid and the
disc sequence.

Reader checks. Check `magic_project`, `magic_kind` and `version_major`.
Verify `super_crc32c` before using any field. Check the
superblock chain against the DISCS table of the newest catalog the reader
holds. Any combination other than the accepted cases is a chain failure:
refuse the disc and report the expected and the found value. A reader with no
DISCS table cannot check the chain, says so, and does not refuse the disc on
that ground.

### 7.6 Run header

Purpose: the identity, geometry, counts and pointers of one run.

The run header is 512 bytes: the 32-byte common header (`magic_kind` `RUN`)
plus a 480-byte fixed body. It is written as `RUN.bin` at the first file of
the run, in the first block of every parity file, and as `RUN2.bin` at the
end of the run. That is `m + 2` copies. Every copy is byte-identical.

Every copy occupies one whole 2048-byte sector: the header is bytes 0 to 511
of that sector, and bytes 512 to 2047 are zero. **The files `RUN.bin` and
`RUN2.bin` are therefore 2048 bytes long, not 512.** Their INDEX Files row
gives `byte_len` 2048. `prev_run_hash` (section 2.7) and `run_hash` in DISCS
(section 11.3) cover the 512 header bytes only, never the 1536 zero bytes.

A recovery tool that has lost INDEX scans the raw medium for the project
magic and the `magic_kind` `RUN`, section 7.10.

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 32 | 16 | u8[16] | `disc_uuid` | The disc this run sits on. |
| 48 | 16 | u8[16] | `repo_uuid` | The repository. |
| 64 | 8 | u64 | `run_seq` | Monotonic run number in the repository, 1-based. |
| 72 | 8 | u64 | `disc_seq` | Disc sequence number, 0-based. |
| 80 | 2 | u16 | `fec_k` | Data columns. 231 in version 1. |
| 82 | 2 | u16 | `fec_m` | Parity columns. 23 in version 1. |
| 84 | 1 | u8 | `fec_scheme` | FEC scheme registry. |
| 85 | 1 | u8 | `hash_algo` | Multicodec code of every id in this run. Phase 1 writes 0x12. |
| 86 | 1 | u8 | `chunker_profile` | Profile id. |
| 87 | 1 | u8 | `compression` | Default compression id. |
| 88 | 1 | u8 | `fs_profile` | Disc filesystem profile id. |
| 89 | 1 | u8 | `run_kind` | 1 data run, 2 repair run (Phase 2), 3 disc-close parity run (Phase 3). 0 is invalid. Health is never stored here; it lives in DISCS. |
| 90 | 1 | u8 | `run_flags` | Run header flags. Bit 0 `CLOSING_RUN`: this run closed the disc, so no later run is possible on it (section 7.11). Bits 1 to 7 reserved, zero. |
| 91 | 1 | u8 | `reserved_u8` | Zero. |
| 92 | 4 | u32 | `reserved_u32a` | Zero. Keeps `index_bytes` aligned. |
| 96 | 8 | u64 | `index_bytes` | Byte length of `INDEX.bin`. |
| 104 | 32 | u8[32] | `index_hash` | Hash of `INDEX.bin`'s bytes. |
| 136 | 8 | u64 | `stream_bytes` | Byte length of the FEC stream, section 7.3. |
| 144 | 32 | u8[32] | `prev_run_hash` | Hash of the previous run header on this disc. Zero for the first run. |
| 176 | 8 | i64 | `created_sec` | Pack time, seconds: the moment `pack` finalized this run's image. Never the burn time, which is unknown when these bytes are hashed; the actual burn time lives only in the local state log; see the operations document. |
| 184 | 4 | u32 | `created_nsec` | Nanoseconds. |
| 188 | 4 | u32 | `tool_version` | Writer registry id in the high 8 bits, writer-defined version in the low 24 bits, as in the superblock (section 7.5). Informational only. |
| 192 | 8 | u64 | `disc_object_count` | Objects on this disc after this run. Cumulative. |
| 200 | 4 | u32 | `disc_run_index` | Index of this run on this disc. 0 for the first run. |
| 204 | 4 | u32 | `reserved_u32b` | Zero. |
| 208 | 296 | u8[296] | `reserved` | Zero. |
| 504 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 503. |
| 508 | 4 | u8[4] | `reserved_final` | Zero. |

Field rules. `run_flags` bit 0 is `CLOSING_RUN`. Bits 1 to 7 are reserved and
written as zero.

`fs_profile` equals the disc superblock's `fs_profile` in every run header on
the disc, the first run's and every appended run's alike. A reader that finds
a run header whose `fs_profile` differs from the superblock's refuses the
run, names both values and names the run seq.

`created_sec` is pack time, never burn time.

A reader derives the FEC geometry from `stream_bytes`, `fec_k` and `fec_m`:
`stream_blocks` is `stream_bytes` divided by 2048, rounded up, and `L`,
section 7.3, is `stream_blocks` divided by `fec_k`, rounded up. Neither
`stream_blocks` nor `L` is stored; INDEX's `file_count` and `object_count`
give the counts a reader needs beyond the geometry.

`index_bytes` and `index_hash` let a reader locate and verify `INDEX.bin`
with no filesystem: `INDEX.bin` is always the first file of the FEC stream
(section 7.3), so its bytes start at the first byte of the stream and run for
`index_bytes`.

Reader checks. Check `magic_project`, `magic_kind` and `version_major`.
Verify `header_crc32c`. Verify `index_hash` against the
bytes of `INDEX.bin`.

### 7.7 The run chain

Every run header points to the previous run header on the same disc, by hash.
The chain is the mutable state of the disc: the list of its runs, the object
count and the close state all come from it. Health is never in the chain.

A reader finds the newest run by listing `/NOAHSARK/runs/` and taking the
highest `<seq>`. That is the only normal path. There is no scanning on the
normal path.

A reader walks the chain backwards by `prev_run_hash`, reading each disc's
run directories, and confirms that no run is missing.

Every run carries its own copy of the catalog. An earlier copy is never
modified.

Withdrawal is known only from a later DISCS table or from local state. A
reader must not infer a withdrawal from a run's own disc, and must not
conclude anything from the absence of a record, because DISCS never omits a
burned run.

### 7.8 Run header copies

The run header exists `m + 2` times inside its run: `RUN.bin` first, block 0
of every parity file, and `RUN2.bin` last. Every copy is byte-identical. The
header block of a parity file precedes the column and is not part of it.

Every run header copy occupies one whole 2048-byte sector: bytes 0 to 511 are
the header and bytes 512 to 2047 are zero. `RUN.bin` and `RUN2.bin` are
2048-byte files.

### 7.9 What an append overwrites

A burned run is immutable. NoahsArk's own structures are append-only. An
append overwrites only the filesystem's own metadata blocks: directory
records, allocation descriptors, File Entries and the like. It never
rewrites a data file's bytes.

Because the parity of every run covers only that run's FEC stream, and never
a filesystem metadata block (section 7.4), an append needs no rewrite budget
and no re-verification of any earlier run's parity: it changes zero bytes
that any run's FEC stream covers, whatever the filesystem does to reach its
new state.

### 7.10 Recovery by carving

Carving is the recovery path when a disc's own filesystem directory is
damaged or unreadable, so that a reader cannot list `/NOAHSARK/runs/` to find
the newest run.

Every structure's common header carries the project magic, the kind, and
`header_len`, and every structure carries enough length information in its
own fixed body to find its end without reading anything past it (section
2.1, rule 14). That is what makes carving possible: a tool scans the raw
medium for the 8-byte project magic `"NOAHSARK"`, reads the common header
that follows, and reads that structure's own length fields to cut the whole
structure out.

The carving path is:

1. Scan the medium for `"NOAHSARK"`. At each hit, read the 32-byte common
   header and check `magic_kind` against section 2.2.
2. For a `DISC` hit, verify `super_crc32c` and read the superblock whole.
3. For a `RUN` hit, verify `header_crc32c`, then read `INDEX.bin` by its
   stated length starting immediately after the run's other framing files,
   or by a further scan for the `INDEX` `magic_kind`.
4. Once INDEX is recovered, read every file it lists by the length INDEX
   records for it, in the order INDEX gives, and verify each against its
   `file_hash` or, for an object, its content id.
5. Follow the run chain forward from `prev_run_hash` in each recovered run
   header.

A reader that already has a working filesystem directory never needs
carving; it lists `/NOAHSARK/runs/` and reads files by name and length as
normal. Carving is the fallback when that directory cannot be read.

### 7.11 Close state

The format must not depend on a closed disc. Every reader path works on an
open disc.

A closing run records the close in two places: `run_flags` bit 0 of its own
run header, and `state_flags` bit 0 of this disc's row in DISCS, in its own
catalog. It does not set the superblock `sealed` flag, because the superblock
is never updated.

A reader learns that a disc is closed from any of three places: the newest
run header, the newest DISCS row for the disc, or the superblock `sealed`
flag for a disc that was sealed at its first write. Any one saying yes is
enough, and a writer then refuses an append.

### 7.12 Raw append

In raw append mode the filesystem directory is not updated. The run header
and INDEX go inside the new run, exactly as usual, so every object stays
findable by carving even when the filesystem directory that names it was
never written.

DISCS marks the disc `append-raw-only` through `state_flags` bit 1.

### 7.13 Forced capacity

A forced capacity is recorded in the superblock as `capacity_forced_sectors`,
next to the reported `capacity_sectors`, and in DISCS. It must be at or below
the reported capacity. It applies from the first write of the disc and must
not change afterwards. `capacity_is_forced` is 1 when
`capacity_forced_sectors` is below `capacity_sectors`, and DISCS sets
`state_flags` bit 3.

A writer sets `capacity_forced_sectors` to the limit the run was packed for,
and a burner refuses a disc whose physical capacity is below that limit.

### 7.14 How a lifecycle state is recorded

| State | Where it is recorded |
|---|---|
| `blank` | Nowhere. No NoahsArk structure exists on the medium. |
| `POW-formatted` | Nowhere on the medium. The spare choice is fixed at format time and is not a field. |
| `open` | The superblock exists with `sealed` 0, and no run header on the disc sets `run_flags` bit 0. |
| `appended` | The disc holds more than one run. `disc_run_index` of the newest run header is above 0, and DISCS holds more than one row for the disc's `disc_uuid`. |
| `sealed` | Either the superblock `sealed` flag is 1, or the newest run header sets `run_flags` bit 0 and the newest DISCS row sets `state_flags` bit 0. |
| `degraded` | `health` 2 or 3 in the newest DISCS row. |
| `withdrawn` | `health` 4 in the newest DISCS row. |

The `health` byte of a DISCS row is a copied value, not the original
judgement. The operator's judgement is host state, and the operations
document states where it originates and when a later run copies the folded
value into its own DISCS row. This document states only what the row holds.

Every transition except the recovery out of `degraded` is one-way.

---
## 8. Filesystem profiles and the volume tree

### 8.1 Profiles a reader must know

Profile 0, `oneshot`, plans one run at the first burn, with no append
handling at that burn. It is the default.

`fs_profile` records the writer's plan at the first burn and never changes. It
does not decide whether the disc can receive a later run: the superblock
`sealed` flag and the drive's POW spare state decide that, and section 7.2
states the rule. A reader treats `fs_profile` 0 and 1 identically.

The profile 0 and profile 1 filesystem is pure UDF at revision 2.01, block size
2048. There is no ISO 9660 bridge, no Joliet and no Rock Ridge. `fs_revision`
is 0x0201.

Profile 1 is profile 0 plus append and uses exactly the same on-disc
structures.

**The UDF volume under profile 0 and profile 1.** The image is built with
`mkudffs`, and these options are normative: `--media-type=hd`,
`--blocksize=2048`, `--udfrev=2.01`, `--uid=0`, `--gid=0`, `--mode=0555`,
`--bootarea=erase`, and no sparing table, that is no `--spartable`. The label
comes from the writer. A sparing table is forbidden because it adds a second
logical-to-physical indirection.

The image length is the forced capacity rounded down to a multiple of 16
sectors.

`mkudffs` places the UDF anchors at fixed, standard-mandated positions of the
image. NoahsArk never writes an anchor itself.

An open disc receives only the bytes NoahsArk actually wrote, so it still
mounts, because the mandatory anchor near the start of the volume is part of
that used prefix. The tail anchors reach the disc when an append or a close
writes them. A sealed disc receives the full-size image at its one burn.

The exact UDF metadata bytes depend on the `mkudffs` version. Two writers that
hold every rule above still differ inside those bytes when their `mkudffs`
versions differ. The run header records the writer and its tool versions in
`tool_version` (section 7.6). A golden vector therefore covers the files
NoahsArk itself writes and the parity computed over the actual FEC stream,
never the UDF metadata bytes.

Every file the format writes begins on a 2048-byte sector boundary. UDF's
in-ICB embedding of a small file's data inside its File Entry block would
break that rule, so the image builder must be configured, or the kernel
driver must behave, so that no NoahsArk file is embedded that way; a writer
pads a file that would otherwise be embedded up to 2048 bytes as a fallback.
**Probe.** Whether `mkudffs`, as configured above, ever produces in-ICB
embedding for a NoahsArk-sized file is not yet confirmed against every
`udftools` version this project supports; the testing and CI section tracks
this as a probe until the answer is stable enough to promote to a test.

Profile 2 uses ISO 9660:1999 level 4, plain. Levels 1, 2 and 3 are forbidden.
Rock Ridge and Joliet are forbidden. `fs_revision` is 0x0004. Profile 2 uses
one hex fan-out level only.

### 8.2 Files at the volume root

Every byte that NoahsArk writes is an ordinary file. The layout is the same
under every profile.

| Path | Content | Written |
|---|---|---|
| `/NOAHSARK/DISC.bin` | Disc superblock (section 7.5). Immutable. | First run only, directly after `RUN.bin`. |
| `/NOAHSARK/README.txt` | Plain-text explanation of the format for a human (section 8.4). | First run only, after `DISC.bin`. |
| `/NOAHSARK/FORMAT.txt` | The byte-layout tables of every structure (section 8.5). | First run only, after `README.txt`. |
| `/NOAHSARK/REFERENCE/decoder.py` | A standalone Python 3 reference decoder (section 8.6). | First run only, after `FORMAT.txt`. |
| `/NOAHSARK/runs/<seq>/RUN.bin` | The run header. | First file of its run. |
| `/NOAHSARK/runs/<seq>/INDEX.bin` | File order, the Objects table, the Prereqs table (section 11.1). | With its run, first file of the FEC stream. |
| `/NOAHSARK/runs/<seq>/catalog/REFS.bin` | The ref table (section 11.2). | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/DISCS.bin` | The disc directory table (section 11.3). | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/snapobj/<name>` | The complete snapshot object of every snapshot, one file each. | With its run. |
| `/NOAHSARK/runs/<seq>/checksum.bin` | The checksum column, `L` blocks. | Last file of the FEC stream. |
| `/NOAHSARK/runs/<seq>/parity/pNNNN.bin` | One file per parity column, `L + 1` blocks. Block 0 is a run header copy; blocks 1 to `L` are the column. | After `checksum.bin`, in column order. Outside the FEC stream. |
| `/NOAHSARK/runs/<seq>/RUN2.bin` | Run header copy. | Last file of its run. Outside the FEC stream. |
| `/NOAHSARK/objects/<ab>/<name>` | Chunks, blobs and trees. Shared by every run on the disc. | In fill order, inside the FEC stream. |
| `/NOAHSARK/snapshots/<name>` | Snapshot objects. | In fill order, before every other object, inside the FEC stream. |

`<seq>` is the run sequence number, zero-padded to 10 decimal digits, so that
lexical order equals numeric order. A reader must not sort run directories as
plain strings without the padding.

`objects/` and `snapshots/` are shared by every run on the disc.

Every fixed name is short, uses the charset `[A-Za-z0-9._/-]`, and avoids every
Windows reserved name.

### 8.3 Run directory naming

`<seq>` is the run sequence number, zero-padded to 10 decimal digits, so
lexical order equals numeric order. A reader must not sort run directories as
unpadded strings.

### 8.4 README.txt

Purpose: the plain-text explanation that lets a reader with a hex editor and no
NoahsArk software extract one file from the disc.

`README.txt` is the exact text below. It is plain ASCII, with LF line endings
and exactly one LF at the end of the file. No line has a trailing space. The
text is byte-identical on every disc of format major 1, apart from its
substitution slots. `README.txt` is at most 16 KiB.

A slot is written `{name}` below. The writer replaces the bytes `{`, the name
and `}` with the value, and writes nothing else in its place. **Every slot in
the text is substituted, wherever it appears.** The substitution rules are:

| Slot | Value |
|---|---|
| `{version_minor}` | The `version_minor` of the superblock, in decimal. |
| `{repo_uuid}`, `{disc_uuid}` | Hyphenated lowercase uuid text. |
| `{disc_seq}` | `disc_seq` in decimal, 0-based. |
| `{label}` | The `label` bytes of the superblock, as they are, with every byte outside 0x20 to 0x7E replaced by `?`. |
| `{media_type}` | The name from the media type registry of section 2.5. |
| `{fs_profile}` | The name from the disc filesystem profile registry of section 2.5. |
| `{hash_algo}` | `sha2-256`, from the superblock `hash_algo`. |
| `{chunker_profile}` | `P3`, `P4` or `P5`, from the superblock `chunker_profile`. |
| `{created}` | `created_sec` and `tz_offset_sec` of the superblock, as `YYYY-MM-DDTHH:MM:SS+HH:MM`. |
| `{fanout_levels}` | 1 or 2, in decimal. |
| `{fec_k}`, `{fec_m}` | 231 and 23 in version 1, in decimal. |

The text:

```
NoahsArk backup disc
====================

1. WHAT THIS DISC IS
--------------------
This disc holds part of a NoahsArk backup repository. The on-disc format is
major version 1, minor version {version_minor}. Everything that NoahsArk
wrote is an ordinary file under the directory /NOAHSARK/. There are no hidden
sectors and no raw areas outside this filesystem. Every structure starts
with the 8-byte text "NOAHSARK" and an 8-byte kind name, is little-endian and
packed, and ends with a checksum.

2. IDENTITY
-----------
repository uuid: {repo_uuid}
disc uuid: {disc_uuid}
disc sequence: {disc_seq}
label: {label}
media type: {media_type}
filesystem profile: {fs_profile}
hash algorithm of the first run: {hash_algo}
chunker profile of the first run: {chunker_profile}
first write time: {created}
object fan-out levels: {fanout_levels}
parity geometry: k={fec_k} data columns, m={fec_m} parity columns

3. HOW TO FIND THINGS
---------------------
/NOAHSARK/DISC.bin              disc superblock, written once
/NOAHSARK/README.txt            this file
/NOAHSARK/FORMAT.txt            the byte layout of every structure
/NOAHSARK/REFERENCE/decoder.py  a standalone Python 3 reference decoder
/NOAHSARK/runs/<seq>/RUN.bin    run header, first file of the run
/NOAHSARK/runs/<seq>/INDEX.bin  file order and the object table
/NOAHSARK/runs/<seq>/catalog/   tables copied from the whole repository
/NOAHSARK/runs/<seq>/checksum.bin   per-block digests
/NOAHSARK/runs/<seq>/parity/        one file per parity column
/NOAHSARK/runs/<seq>/RUN2.bin   run header copy, last file of the run
/NOAHSARK/objects/<ab>/<name>   chunks, blobs, trees
/NOAHSARK/snapshots/<name>      snapshot objects

<seq> is the run number, ten decimal digits, zero padded. The run directory
with the highest number is the newest run, and its catalog/ directory is the
newest catalog. Read that one.

4. HOW AN OBJECT IS NAMED
-------------------------
The name of an object is the hash of its uncompressed payload bytes and
nothing else. The kind, the chunker profile, the compression and the object
header do not enter the name. The name on disc is the lowercase hex of the
multihash: two prefix bytes then the digest, following `hash_algo`. 1220
means SHA-256, the prefix on every object this version writes, so the name
is 68 hex characters. <ab> is the first two hex
characters of the digest, which is characters 5 and 6 of the file name.

5. HOW TO READ AN OBJECT
------------------------
An object file starts with a 32-byte common header, then a 32-byte object
header. In the object header, at byte offset 3 of that 32-byte header, is
the compression id: 0 means none and 1 means zstd. At offset 8 is
payload_len, a little-endian unsigned 64-bit number. At offset 16 is
stored_len. Skip the 64 header bytes total, take the next stored_len bytes,
decompress them with the named algorithm into exactly payload_len bytes,
hash the result with the algorithm the name declares, and compare that
digest with the digest in the name. They must be equal. If they are not, the
bytes are damaged; see part 6.

6. HOW TO WALK A SNAPSHOT
-------------------------
Read catalog/REFS.bin, which is a table of named pointers, and take the
newest record for the name LATEST. It gives a snapshot id. Read that
snapshot object. Its header names a root tree id. Read that tree object: it
is a list of directory entries, each with a name, the POSIX metadata, and
either a tree id for a subdirectory or a blob id for a file. Read the blob
object: it holds the ordered chunk ids of that file. Concatenate the chunk
payloads in order and the file is restored.

7. HOW TO REPAIR
----------------
Each run carries Reed-Solomon parity over its own data files, concatenated
in the order INDEX lists them: the FEC stream. The stream is cut into
{fec_k} equal columns of L blocks each; L is in the run header. Stripe i is
block i of every column. checksum.bin is one more column: its block i holds
an 8-byte digest of each of the {fec_k} data blocks of stripe i, so a
damaged block can be found. The {fec_m} files under parity/ are the parity
columns; block 0 of each is a copy of the run header and the column starts
one block later. Any {fec_k} of the {fec_k} data plus {fec_m} parity blocks
of one stripe reconstruct the rest. FORMAT.txt gives the field arithmetic.
If the filesystem directory itself is damaged, scan the medium for the text
"NOAHSARK" to carve out the run header and INDEX by hand; FORMAT.txt gives
the length fields needed to do this.

8. WHERE THE BYTE LAYOUTS ARE
-----------------------------
FORMAT.txt in this directory holds the offset, size, type, name and meaning
of every field of every structure, the registries, the magic values and the
chunking constants. This file and that file together are enough to extract
one file from this disc by hand, with a hex editor and no NoahsArk software.
REFERENCE/decoder.py in this directory is a runnable Python 3 program that
does the same extraction in code: it parses DISC.bin, RUN.bin, INDEX.bin and
every object header, verifies content ids, walks a snapshot and prints the
listing.

9. THE FORMAT RULES
--------------------
1. Every integer is little-endian. No big-endian field exists.
2. Every type is fixed width: u8, u16, u32, u64, i32, i64.
3. Every structure is packed, and every gap is a named reserved field of
   zero bytes.
4. Every structure starts with an 8-byte project magic, an 8-byte kind name,
   then version_major and version_minor.
5. A reader refuses an unknown version_major and ignores an unknown
   version_minor.
6. A version_minor bump only appends fields an old reader can ignore; a
   version_major bump changes shape and an old reader refuses it.
7. Every checksum lies after every byte it covers. Checksums are CRC-32C,
   polynomial 0x1EDC6F41, reflected, init 0xFFFFFFFF, final xor 0xFFFFFFFF.
8. Every pointer to another structure carries the hash of that structure.
9. A string is encoding, three zero bytes, a 32-bit length, then the bytes.
   Encoding 0 is UTF-8. There is no terminator and no normalization.
10. Every structure has a byte-offset table, and FORMAT.txt holds it.
```

### 8.5 FORMAT.txt

`FORMAT.txt` is the exact normative text of Appendix A for major 1 minor 0. A
writer writes those bytes and no others, apart from the minor version slot
`<N>` on the first line.

`FORMAT.txt` is plain ASCII with LF line endings, tab bytes as the only column
separator, and at most 64 KiB. Section 13 states the exact major 1 minor 0
byte and line count. A writer that produces a different length for minor 0
has a defect.

`FORMAT.txt` holds six parts in a fixed order: FORMAT RULES, REGISTRIES with
ten registries in a stated order, STRUCTURES with the named structures in a
stated order, MAGIC VALUES, CHUNKING CONSTANTS, CHECKSUM PARAMETERS.

A structure added in a later version is appended to the part 3 list and is
never removed while the major version holds.

### 8.6 Reference decoder

Purpose: a runnable check on this document, carried on the disc itself, so
that a reader with a Python interpreter and no NoahsArk software can extract
and verify a file without transcribing FORMAT.txt by hand.

Every disc carries `/NOAHSARK/REFERENCE/decoder.py`: a single-file Python 3
program that uses only the standard library, plus, for zstd payloads, the
`compression.zstd` module where the interpreter provides it or the `zstd`
command-line tool otherwise. It parses DISC.bin, a run's RUN.bin and
INDEX.bin, and every object's common header and object header; it verifies
every SHA-256 content id; it walks a snapshot's tree from `catalog/REFS.bin`;
and it prints the resulting listing.

`decoder.py` is a fixed file, checked into the NoahsArk source repository and
versioned with this document. A writer copies it byte for byte from that
checked-in file; it is never generated or altered per repository or per
disc. Its INDEX file role is 14 (section 11.1), and it is at most 64 KiB.

### 8.7 Fill order inside a run

The writer places files in this order under every profile:

1. `RUN.bin`, the run header.
2. In the first run of a disc only: `DISC.bin`, `README.txt`, `FORMAT.txt`,
   `REFERENCE/decoder.py`.
3. `INDEX.bin`.
4. The catalog files, in the entry order of section 11.1's Files table.
5. Snapshot objects, then tree and blob objects, contiguous.
6. Chunks, in path order.
7. `checksum.bin`, the checksum column.
8. The parity files, in column order.
9. `RUN2.bin`, the run header copy.

Steps 1 to 6 are the FEC stream, in the order INDEX lists them (section 7.3).
Steps 7 and 8 cover it. Step 9 sits outside the stream, at the end of the
run, so that a copy of the header survives damage at either end.

The order inside steps 5 and 6 is total. Two conforming writers with the same
object set produce the same order.

The walk. The run's snapshots are ordered by `generation` ascending, then by
snapshot id bytes ascending. For each snapshot in that order, the writer walks
its tree graph in pre-order, in tree-entry order, descending into a
subdirectory entry before it moves to the next entry. This one walk defines the
order of both steps. Objects are written in depth-first path order inside a
run, so siblings stay adjacent.

Step 5 emits, in walk order:

1. the snapshot object, before anything below it;
2. each tree object at the moment the walk reaches it, before the targets of
   its entries;
3. each regular file's blob object, directly after the tree entry that
   references it.

Step 6 emits, for each regular-file entry in entry order, that file's chunks
in the order its blob object lists them.

An object is emitted once, at its first occurrence in the walk.

An object that this run does not store, because it is a prerequisite or
because the target disc already holds it, is not emitted and does not move
the position of anything else.

An object that the target disc already holds is never written again. The
packer treats it as present, gives it no INDEX Objects row in this run, and
references the earlier run as a prerequisite. A duplicate written for
locality is written only onto a different disc.

The writer copies files into the mount one at a time, single-threaded, in
fill order.

Invariants:

1. INDEX is finalized before the checksum column and the parity are
   computed, and it lists the exact byte length of every file that
   precedes it in the FEC stream, so the FEC stream is fully determined
   before the checksum column or the parity is computed.
2. Every column file is contiguous inside itself, block 0 of a parity file
   the run header copy and blocks 1 to `L` its share of the parity.
3. `RUN.bin`, `DISC.bin`, `catalog/DISCS.bin`, `INDEX.bin` and the run header
   become final in that order, each from values already final.
4. The checksum column and the parity are computed over the finished FEC
   stream, which holds the final bytes of `RUN.bin` and of every other file
   in it.

Metadata objects, that is trees, blobs and snapshot objects, are placed
together, so a connectivity check over one run costs one seek and one
sequential read. This is a fill-order rule; every non-snapshot object still
shares the one `objects/` directory (section 3.5).

### 8.8 Name and path budget

| Item | Limit |
|---|---|
| Object name | 68 characters, charset `[0-9a-f]`. Never stripped. |
| Any on-disc name | at most 126 characters |
| Any on-disc path | under 220 characters |
| Forbidden characters in a name | `< > : " / \ \| ? *`, control characters, a trailing space, a trailing dot, and the reserved device names `CON PRN AUX NUL COM1-9 LPT1-9` |

Never rely on case to distinguish two objects.

---
## 9. Capacity invariants

Three names carry the whole capacity policy. No second formula for them
exists. None of the three is an on-disc field: capacity planning is host
state, computed before a burn and recorded in the local state log, because
the format no longer carries a medium address of its own for a run to be
measured against. The operations document is the normative home of the
estimator; this section states the invariants a writer must hold.

- `data_budget`: the bytes that object data and a run's own framing files may
  occupy on the disc, across all its runs. `data_budget` charges the sum of
  every run's `stream_bytes` (section 7.6) plus the header copies and the
  parity of every run.
- `fill_limit`: the byte position, counted from the start of the medium, that
  no run's last byte may reach. A writer computes it before packing a run and
  checks the plan against it; it is never read back from the disc.
- `reserve`: `capacity_forced - data_budget`.

Four invariants are normative. Any writer that holds them conforms, whatever
arithmetic it used to choose the numbers.

1. No run, its parity and its header copies included, reaches `fill_limit`.
2. `fill_limit` is at most `capacity_forced` (section 7.5), and it is defined
   as `capacity_forced - safety_margin - spare_area`, where `safety_margin`
   is `ceil(capacity_forced * (1 - fill_ratio))` and `spare_area` is
   `ceil(spare_reserve_bytes / 2048)` on a `spare:min` disc, the same
   expression on a `spare:default` disc with that mode's larger
   `spare_reserve_bytes`, and 0 on a sealed disc. That is the definition, and
   it holds whether or not an override is set.
3. `data_budget` is a whole number of stripes of `k` data blocks per run:
   every run's `stream_blocks mod k == 0` once its stream is padded to a
   whole number of columns.
4. The sum of the bytes that every run of the disc occupies, plus the bytes
   the disc still holds free below `fill_limit`, never exceeds `fill_limit`.
   A later append recomputes the estimate from the disc's recorded runs; it
   never trusts a stale on-disc value, because none exists.

The inputs of a writer's reserve estimator are heuristics. They are not part
of the on-disc contract, and a reader never uses them.

---

## 10. Forward error correction

FEC is optional per run: `fec_scheme` 0, `none`, writes no checksum column and
no parity, and `fec_scheme` 1, `rs255-gf8`, writes both, as this section
describes. Section 10.8 states the scheme 0 rules; sections 10.1 to 10.7
describe scheme 1.

### 10.1 Parity layout

The FEC scheme is `rs255-gf8`: a systematic erasure code over GF(2^8) with `k`
information shards and `m` parity shards per stripe.

A shard is exactly one 2048-byte block of the FEC stream (section 7.3), or,
for the checksum and parity columns, one block of the file that carries that
column.

A stripe is 255 shards: `k` data, 1 checksum and `m` parity. The code covers
the `k` data and the `m` parity shards. The checksum shard is outside the
code.

Format version 1 fixes `k = 231` and `m = 23`. A version 1 writer writes no
other pair and a version 1 reader refuses any other pair. Both values are still
recorded, in the run header.

`L = ceil(stream_blocks / k)`, where `stream_blocks` is the FEC stream's
length in 2048-byte blocks, zero-padded per file as section 7.3 states.

Data column `c` is blocks `[c*L, (c+1)*L)` of the FEC stream, for
`c = 0 .. k-1`, the last column zero-padded past `stream_blocks` when
`stream_blocks` is not a multiple of `k*L`.

Column `k`, the checksum column, is the `L` blocks of `runs/<seq>/checksum.bin`.

Column `c` for `c = k+1 .. 254` is blocks 1 to `L` of
`runs/<seq>/parity/pNNNN.bin`, where `NNNN` is `c` in decimal zero-padded to
four digits. Block 0 of that file is a run header copy. The file is `L + 1`
blocks.

Every column file is contiguous. INDEX records the byte length of each, and
the run header records `stream_bytes`; a reader derives `stream_blocks` and
`column_blocks` from it and from `fec_k` by the formulas of section 7.6.

Stripe `i` is block `i` of every column, for `i = 0 .. L-1`.

Encoding is byte-column-wise: take one byte from each of the `k` data blocks
at the same byte offset, in column order 0 to `k-1`, and produce `m` parity
bytes at that offset, column `k+1` first, for all 2048 byte offsets.

**The burst bound.** The maximum correctable single burst is `m * L` blocks
of the FEC stream. A burst longer than `m * L` blocks puts more than `m`
erasures into one stripe, which section 10.5 refuses to decode.

### 10.2 The code

**Field.** GF(2^8) with the polynomial `x^8 + x^4 + x^3 + x^2 + 1`, 0x11D.
A byte is a field element. Addition is XOR. Multiplication is carry-less
polynomial multiplication reduced modulo 0x11D:

```
mul(a, b):
    r = 0
    while b != 0:
        if b & 1: r = r ^ a
        a = a << 1
        if a & 0x100: a = a ^ 0x11D
        b = b >> 1
    return r                                  # r fits in 8 bits
inv(a): the unique x with mul(a, x) == 1, for a != 0
div(a, b) = mul(a, inv(b))
```

**Shards.** The information shards are the `k` data columns of the stripe,
in column order `i = 0 .. k-1`. The parity shards are the `m` parity
columns, `j = 0 .. m-1`, where parity shard `j` is column `k + 1 + j`.

**Generator matrix.** The code is systematic. Parity shard `j` is a linear
combination of the `k` information shards with the coefficients of row `j`
of an `m x k` Cauchy matrix `C`:

```
x_j = k + j                 for j = 0 .. m-1       # 231 .. 253 in version 1
y_i = i                     for i = 0 .. k-1       #   0 .. 230 in version 1
C[j][i] = inv(x_j ^ y_i)                            # 1 / (x_j + y_i) in GF(2^8)
```

Every `x_j` differs from every `y_i`, so no denominator is zero, and every
square submatrix of `[I_k ; C]` is invertible. That is what lets any `k`
surviving shards of a stripe, data or parity, determine the missing ones.

**Encoding.** For every byte offset `t` in `0 .. 2047`:

```
p_j[t] = XOR over i = 0 .. k-1 of mul(C[j][i], d_i[t])
```

where `d_i` is data shard `i` and `p_j` is parity shard `j`.

**Decoding.** Take the `k` rows of the `(k + m) x k` matrix `[I_k ; C]`
that correspond to `k` surviving shards, invert that `k x k` matrix over the
field, and multiply it by the surviving shard bytes at each offset. The
result is every data shard; the missing parity shards are then re-encoded.
A stripe with more than `m` erasures is not decodable, and the decoder must
say so (test 7). The ordering of the shards is the only implementation
freedom, and this subsection fixes it.

**Worked example, `k = 3`, `m = 2`.** The geometry is not a version 1
geometry; the example exists so that an implementer can test the arithmetic
by hand. With `k = 3`, `x_0 = 3`, `x_1 = 4`, and `y = (0, 1, 2)`:

| Inverse | Value |
|---|---|
| `inv(1)` | 0x01 |
| `inv(2)` | 0x8E |
| `inv(3)` | 0xF4 |
| `inv(4)` | 0x47 |
| `inv(5)` | 0xA7 |
| `inv(6)` | 0x7A |

```
C[0] = ( inv(3^0), inv(3^1), inv(3^2) ) = ( 0xF4, 0x8E, 0x01 )
C[1] = ( inv(4^0), inv(4^1), inv(4^2) ) = ( 0x47, 0xA7, 0x7A )
```

One byte offset, with data bytes `d = (0x53, 0xA7, 0x0C)`:

```
p_0 = mul(0xF4,0x53) ^ mul(0x8E,0xA7) ^ mul(0x01,0x0C)
    = 0x31 ^ 0xDD ^ 0x0C = 0xE0
p_1 = mul(0x47,0x53) ^ mul(0xA7,0xA7) ^ mul(0x7A,0x0C)
    = 0xDD ^ 0x72 ^ 0x02 = 0xAD
```

Recovery check: with `d_0` and `d_1` lost, the surviving shards are `d_2`,
`p_0` and `p_1`. Solving the three equations above for `d_0` and `d_1`
returns `0x53` and `0xA7`. The full-size golden vector of section 13 is
computed by the same rules at `k = 231`, `m = 23`.

The ordering of the shards is the only implementation freedom, and this section
fixes it. Any encoder that produces the same parity bytes for the same
information bytes conforms.

### 10.3 Checksum column

Purpose: an 8-byte digest of every data block of a stripe, so a damaged block
can be located before the code is applied.

Block `i` of the checksum column holds the digests of the `k` data blocks of
stripe `i`, and of no other block. There is no offset and no wrap.

The digest is the first 8 bytes of the 32-byte SHA-256 digest of the
block, computed over the 2048 bytes of the FEC stream at that position.

Every block of `checksum.bin` is a self-delimiting record of 2048 bytes,
repeated `L` times; it carries the 8-byte `magic_kind` `CHECKSUM` as a record
magic (section 2.1, rule 5) but not the rest of the 32-byte common header,
because a block is a record of the checksum column, not a structure of its
own:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u8[8] | `magic_kind` | ASCII `"CHECKSUM"`. |
| 8 | 4 | u32 | `stripe_index` | `i`, the stripe whose data digests follow. Equals the block's own index inside the column. |
| 12 | 2 | u16 | `digest_count` | `k`. 231 in version 1. |
| 14 | 1 | u8 | `digest_bytes` | 8. |
| 15 | 1 | u8 | `hash_algo` | 0x12, sha2-256. |
| 16 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 15. |
| 20 | 1848 | u8[1848] | `digests` | `k` digests, in data column order 0 to `k - 1`. |
| 1868 | 180 | u8[180] | `reserved` | Zero. |

Field rules. `digest_count` equals `k`. `digest_bytes` is 8. The digests are in
data column order 0 to `k - 1`.

The checksum column is outside the Reed-Solomon code. The parity neither covers
it nor reconstructs it.

Reader checks. Check the magic and verify `header_crc32c`. When a checksum
block is unreadable or its header CRC fails, check that stripe's data blocks
through the content ids of the objects INDEX maps them to, and treat a
failing block as an erasure. A block that no INDEX Objects or Files row
covers is then treated as readable.

A silently wrong digest makes a verifier treat a good data block as an
erasure. Reconstruction returns the same bytes and the content id passes, and
the verifier reports the checksum block as damaged. No data is lost.

A parity block has no digest. An unreadable parity block is an erasure from
the start.

### 10.4 Header replication and parity files

The run header exists `m + 2` times: `RUN.bin` first, block 0 of every parity
file, and `RUN2.bin` last. The header block of a parity file precedes the
column and is not part of it.

The parity geometry is derivable from any header copy: `stream_bytes`,
`fec_k` and `fec_m` determine every column boundary through the formulas of
section 7.6. A tool that lost every header copy still knows `k` and `m`,
because version 1 fixes them, and finds `L` by the `CHECKSUM` `magic_kind`
of the first checksum block.

### 10.5 Decode rule

Take the `k` rows of `[I_k ; C]` that correspond to `k` surviving shards,
invert that `k x k` matrix over the field, and multiply it by the surviving
shard bytes at each byte offset. The result is every data shard. The missing
parity shards are then re-encoded.

When more than `k` blocks of a stripe are present, the decoder uses the `k`
present blocks with the lowest column index. This choice is normative, so
that healing the same damaged stripe always reproduces the same output.

A stripe with more than `m` erasures is not decodable, and the decoder must say
so.

The single-parity retry is bounded and normative. Let `E` be the erasures the
stripe already has. The decoder tries each single parity shard of the stripe
in turn as one more erasure, which is at most `m - E` attempts, and it makes an
attempt only while `E + 1 <= m`. An attempt succeeds when every reconstructed
data block matches its digest, or, with no usable checksum block, when every
object that INDEX maps into the stripe passes its content id. If no
single-parity attempt decodes, the stripe is undecodable. The decoder must not
try pairs or larger subsets.

When the run's filesystem directory cannot be read at all, the decoder first
recovers `RUN.bin` and `INDEX.bin` by carving (section 7.10), then proceeds
as above using the geometry and the object map that carving recovered.

### 10.6 The append bound

A NoahsArk run's parity covers only that run's FEC stream (section 7.4). An
append never rewrites a byte of any run's FEC stream, so an append needs no
rewrite budget against an earlier run's parity and no re-verification of it:
the two are structurally disjoint.

### 10.7 Health status values

The headline health metric is the RS margin: `m` minus the worst-stripe erasure
count, as a percentage of `m`. DISCS records it in `rs_margin_percent`.

Every percentage this document stores is rounded down to a whole percent and
clamped to the range 0 to 100. That rule covers `rs_margin_percent`.

Status values are `HEALTHY`, `DEGRADED` when any block needed RS correction,
`CRITICAL` when any stripe used more than half its parity, `FAILED` when any
object is unrecoverable, and `UNKNOWN` where no record exists.

What a local cache reports for a disc that carries no health record of its own
is host behaviour. The operations document states it.

### 10.8 Scheme 0: no FEC

A run whose `fec_scheme` is 0, `none`, carries no `checksum.bin` file and no
parity files. Its INDEX carries no Files rows for roles 10 (`checksum.bin`)
and 11 (a parity file), and its Objects and Prereqs tables are unaffected.
`RUN.bin` and `RUN2.bin` still exist: the run header replication rule of
section 10.4 still gives two copies, `RUN.bin` first and `RUN2.bin` last,
just with no parity file copies between them.

The stream definition of section 7.3 still applies to the file order: INDEX
lists every stream file in the same fill order whether or not the run carries
FEC, so a reader that only needs the file order, not the parity, reads a
scheme 0 run the same way.

A reader verifies a scheme 0 run by checking every object's content id
(section 12.1) and every Files row's `file_hash` against the bytes on disc;
it performs no checksum-column or parity check, because none exists. Heal
refuses a scheme 0 run and reports that the run has no FEC, naming the run
seq; it repairs nothing, because there is no parity to repair from.

---
## 11. The run index and the catalog

### 11.1 INDEX

Purpose: the one structure a reader opens first inside a run. It lists every
file the run wrote, in FEC stream order; it locates and verifies every
object the run stores; and it names every object the run references but does
not store.

INDEX replaces the earlier manifest, filter, layout table and catalog
container in one structure. There is no membership filter in this format: a
reader proves absence by consulting the INDEX Objects table of every run a
catalog lists (section 11.6).

Fixed body, after the 32-byte common header (`magic_kind` `INDEX`):

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 32 | 8 | u64 | `run_seq` | The run this INDEX describes. |
| 40 | 4 | u32 | `file_count` | Rows in the Files table. |
| 44 | 4 | u32 | `object_count` | Rows in the Objects table. |
| 48 | 4 | u32 | `prereq_count` | Rows in the Prereqs table. |
| 52 | 2 | u16 | `file_record_size` | 48. |
| 54 | 2 | u16 | `object_record_size` | 72. |
| 56 | 2 | u16 | `prereq_record_size` | 40. |
| 58 | 1 | u8 | `hash_algo` | Multicodec code of every id in this run. |
| 59 | 1 | u8 | `digest_len` | 32. |
| 60 | 4 | u8[4] | `reserved` | Zero. Keeps `container_len` aligned. |
| 64 | 8 | u64 | `container_len` | Total length of this container in bytes, for validation. |
| 72 | 4 | u32 | `body_crc32c` | CRC-32C over the Files table, the Objects table and the Prereqs table. |
| 76 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 75. |
| 80 | `file_count * 48` | | `files` | The Files table. |
| | `object_count * 72` | | `objects` | The Objects table, sorted ascending by `content_id`. A reader binary-searches it directly; the table needs no separate lookup index. |
| | `prereq_count * 40` | | `prereqs` | The Prereqs table, sorted ascending by `content_id`. |

**Files table**, 48 bytes per row:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `file_hash` | Hash of the whole file's bytes, under `hash_algo`. For an object file this is not its content id, which is the hash of the payload alone (section 3.1); it is the whole-file integrity hash. |
| 32 | 8 | u64 | `byte_len` | Length of the file in bytes. |
| 40 | 1 | u8 | `role` | File role registry, below. |
| 41 | 7 | u8[7] | `reserved` | Zero. |

File role registry:

| Id | Role |
|---:|---|
| 0 | reserved |
| 1 | `INDEX.bin`, this file |
| 2 | `RUN.bin` |
| 3 | `DISC.bin` |
| 4 | `README.txt` |
| 5 | `FORMAT.txt` |
| 6 | reserved. Never assigned again: earlier document versions gave this id to a run-wide membership filter, since removed. |
| 7 | `catalog/REFS.bin` |
| 8 | `catalog/DISCS.bin` |
| 9 | a snapshot object copy under `catalog/snapobj/`, loose or packed |
| 10 | `checksum.bin` |
| 11 | a parity file |
| 12 | `RUN2.bin` |
| 13 | an object file under `/NOAHSARK/objects/` or `/NOAHSARK/snapshots/`. Reserved for a later version: a container's role stays 13 too. |
| 14 | `/NOAHSARK/REFERENCE/decoder.py` |

Field rules. Rows are in FEC stream order (section 7.3): `INDEX.bin` itself
first, then the fixed-name files by role in the order the table above lists,
then every object file, role 13, by `file_hash` ascending. Role 14,
`REFERENCE/decoder.py`, is a first-run-only fixed file like roles 3 to 5 and
sits with them in fill order, despite its higher role number: a role id is
assigned once and never renumbered, so a role added after role 13 was
registered still takes the next free id. This
order is authoritative: it is the order the writer laid the files in, and a
reader relies on it instead of sorting.

`checksum.bin`, every parity file and `RUN2.bin` sit outside the FEC stream
(section 7.3); their rows still appear in the Files table, describing files
of the run, but not in stream order relative to the stream's own rows —
they follow every stream row, in the fixed order roles 10, 11, 12 give.

A run whose `fec_scheme` is 0, `none` (section 10.8), carries no role 10 or
role 11 rows: there is no `checksum.bin` and no parity file. `RUN2.bin`,
role 12, still appears.

File names are not stored. A reader derives a fixed-name file's path from its
role, and an object file's name from the content id or the
`file_hash`, section 3.5.

**Objects table**, 72 bytes per row, sorted ascending by `content_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | The object id. |
| 32 | 4 | u32 | `file_index` | 0-based row index into the Files table. In this version, always the object's own file. |
| 36 | 4 | u32 | `reserved` | Zero. |
| 40 | 8 | u64 | `offset` | In this version, always 0: an object's stored payload bytes start at the fixed offset just after its own file's common header and object header (section 6.1). A later version's container gives this field the byte offset of a member's payload inside `file_index`'s file. |
| 48 | 8 | u64 | `stored_len` | Bytes stored at `offset`. |
| 56 | 8 | u64 | `payload_len` | Uncompressed payload bytes. |
| 64 | 1 | u8 | `kind` | Object kind registry, section 6.1. |
| 65 | 1 | u8 | `compression` | Compression id. |
| 66 | 2 | u16 | `flags` | bit0 duplicate for locality, bit1 metadata object, that is a tree, a blob or a snapshot object. Bits 2 to 15 reserved, zero. |
| 68 | 4 | u32 | `reserved` | Zero. |

A row is self-sufficient: `file_index`, `offset`, `stored_len`,
`payload_len` and `compression` are enough to read and verify the object
directly from its own file.

**Prereqs table**, 40 bytes per row, sorted ascending by `content_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | An object this run references but does not store. |
| 32 | 8 | u64 | `run_seq` | The run that stores it. |

Prerequisite membership is by direct reference, not by reachability: every id
that a tree, a blob or a snapshot object stored in this run references
directly, and that this run does not store as an object of its own. The list
is one edge deep. A snapshot's `parent` id is never a prerequisite, because
every catalog carries the complete snapshot object of every snapshot.

An object that an earlier run of the same disc stores is a prerequisite like
any other, with its own `run_seq`. A prerequisite never names a run whose
`run_status` is 3, withdrawn (section 11.3).

The algorithm of a prerequisite `content_id` is the `hash_algo` of the run
header of that `run_seq`.

Hash and CRC coverage. `header_crc32c` covers bytes 0 to 75. `body_crc32c`
covers every table row. The run header records `index_hash` over every byte
of `INDEX.bin`.

Reader checks. Check `magic_project`, `magic_kind` and `version_major`.
Verify both CRCs. Refuse an Objects row whose `kind` is
outside 1 to 4.

### 11.2 REFS

Purpose: the repository-wide table of named pointers to snapshots.

REFS and DISCS (section 11.3) share one container shape. Fixed body, after
the 32-byte common header (`magic_kind` `REFS` or `DISCS`):

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 32 | 16 | u8[16] | `repo_uuid` | The repository. |
| 48 | 8 | u64 | `record_count` | Records that follow. |
| 56 | 2 | u16 | `record_size` | 96 for REFS, 176 for DISCS. |
| 58 | 1 | u8 | `hash_algo` | Multicodec code of a digest in the records that has no `hash_algo` of its own. For DISCS, the algorithm of `run_hash`. |
| 59 | 1 | u8 | `digest_len` | 32. |
| 60 | 4 | u32 | `reserved` | Zero. |
| 64 | 4 | u32 | `body_crc32c` | CRC-32C over the records. |
| 68 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 67. |
| 72 | `record_count * record_size` | | `records` | Sorted, fixed-width. |

REFS uses the ref record of section 6.17, 96 bytes, sorted by `name` bytes
ascending, then `time_sec` ascending, then `time_nsec` ascending, then
`run_seq` ascending, then `snapshot_id` bytes ascending: the same order and
the same total-key argument section 6.17 states.

REFS is replicated in full on every run, so a reader finds every name from
the newest catalog alone.

Reader checks. Check the magic and `version_major`. Verify
both CRCs. Binary-search the records by the sort key.

### 11.3 DISCS

Purpose: one row per run that was burned, merging what earlier document
versions split into a run table and a disc directory. It carries every run's
disc, geometry summary and verification status, and it chains each disc's
superblock hash so a reader can detect a substituted disc.

DISCS row, 176 bytes, sorted by `run_seq`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u64 | `run_seq` | The run, 1-based. |
| 8 | 8 | u64 | `disc_seq` | The disc that holds it, 0-based. |
| 16 | 16 | u8[16] | `disc_uuid` | That disc's uuid. |
| 32 | 32 | u8[32] | `run_hash` | Hash of the 512 bytes of that run's `RUN.bin` (section 2.7). All zero in the row of the run that **carries** this table: that header is written after the table (section 8.7), so its hash is not yet known. The next copy of the table, written by the next run, fills the value in. |
| 64 | 8 | i64 | `created_sec` | Pack time of the run, as in the run header (section 7.6). Not the burn time. |
| 72 | 8 | i64 | `last_verify_sec` | Last verification time of the disc. 0 when never verified. |
| 80 | 8 | u64 | `capacity_sectors` | Reported capacity of the disc, a physical medium unit. |
| 88 | 8 | u64 | `used_sectors` | Sectors used on the disc after this run, a physical medium unit taken from the burn, not from any on-disc address field. |
| 96 | 1 | u8 | `run_status` | 1 verified, 2 burned but not yet verified, 3 withdrawn by the writer after a failed verify. 0 is invalid. |
| 97 | 1 | u8 | `health` | 1 healthy, 2 degraded, 3 critical, 4 failed, 5 unknown, 6 unverified, this disc. |
| 98 | 2 | u16 | `rs_margin_percent` | Worst-stripe margin, as a percentage of `m`, rounded down and clamped to 0 to 100. |
| 100 | 2 | u16 | `label_len` | Byte length of the label. |
| 102 | 64 | u8[64] | `label` | UTF-8, zero-padded. The disc's label, the same 64 bytes the superblock carries (section 7.5). |
| 166 | 1 | u8 | `state_flags` | bit0 closed, bit1 append-raw-only, bit2 spare below the threshold, bit3 capacity forced. Bits 4 to 7 reserved, zero. |
| 167 | 1 | u8 | `reserved` | Zero. Keeps `capacity_forced_sectors` aligned. |
| 168 | 8 | u64 | `capacity_forced_sectors` | Forced capacity, section 7.13. |

Field rules. DISCS holds one row for every run that was burned, whatever its
state, plus the row of the run that carries the table. No run is ever
omitted.

`run_status` 0 is invalid. The row of the run that carries the table holds a
zero `run_hash` and `run_status` 2. The next copy fills them in.

A later copy of DISCS differs from an earlier one only by rows added at its
end and by two fields of an existing row: `run_status`, from 2 to 1 or from 2
to 3 and never back, and `run_hash`, from zero to the real hash and never
back and never to a different value. Any other difference is damage.

A reader that sees `run_hash` all zero takes the hash from the run header
itself or from the next run's `prev_run_hash`, and must not treat the zero
as a verification failure.

A reader that holds two copies of DISCS prefers the one whose container lies
in the higher `run_seq`.

Withdrawal is explicit and is never inferred from absence. A `run_seq` is
never reused. A failed run leaves its row with `run_status` 3, and the
re-burn is a new run with the next `run_seq`. A withdrawn run never confirms
a dedup query, is never a prerequisite target, is never selected as a source
run, and is ignored by a restore planner.

`health` 6, `unverified, this disc`, is the value a run writes into the row
of the disc it is being written onto, since no verification of that disc
exists yet when it is written. A later run of the repository replaces the
row with the measured values. A reader treats `health` 6 like `health` 5: it
reports the disc as not yet verified and never as healthy.

`used_sectors` and `capacity_sectors` describe the physical medium (section
2.6); neither is derived from an on-disc address field, because the format
carries none.

Hash and CRC coverage. `header_crc32c` covers bytes 0 to 67 of the fixed
body. `body_crc32c` covers the rows. `run_hash` covers the 512 bytes of that
run's `RUN.bin`, CRC included, under the container's `hash_algo`.

Reader checks. Verify both container CRCs. Check each disc's chain of
superblock hashes where the reader holds more than one disc's DISCS rows.

### 11.4 Catalog contents per run

Every run carries INDEX, and a catalog of REFS, DISCS and the snapshot
objects.

| Item | Scope | Why |
|---|---|---|
| This run's INDEX | This run | File order, exact object lookup, prerequisites. |
| Every snapshot object, complete | The whole repository | A reader lists, names and walks the whole history from one disc. |
| REFS | The whole repository | The entry point for names. |
| DISCS | The whole repository | Maps every run to its disc, and every disc to its chain (section 11.3). |

REFS, DISCS and the snapshot objects are replicated in full on every run.
There is no manifest history and no filter history to carry: proof of
absence (section 11.6) works from every run's own INDEX, which every disc
already holds a copy path to through DISCS.

`catalog/snapobj/<name>` is the complete object file, byte for byte as
`/NOAHSARK/snapshots/<name>` holds it, including the common header and the
object header.

### 11.5 Dedup rule

Never drop chunk data on the strength of a hint. Only an exact INDEX Objects
lookup permits dropping the data.

If a run's INDEX cannot be consulted, the writer writes the chunk again and
logs the event.

Withdrawn runs take no part in a dedup query.

### 11.6 Proof of absence and coverage

There is no membership filter in this format. Proof of absence comes
directly from the INDEX Objects tables of the runs the catalog's DISCS table
lists: an object is missing when its content id is absent from every run's
Objects table, checked exactly, not from a run's Prereqs table alone.

In a connectivity check chunks are never read. Membership alone is the whole
obligation. Only trees, blobs and snapshots are read.

Coverage of an object is exact whenever a manifest, that is an INDEX Objects
table, is available for the run that should hold it; a reader that lacks a
given run's INDEX (because that run's disc is not in the drive) states the
object as missing evidence, not as missing data, and does not fail a plan on
that ground alone. A reader states which for every object.

An object for which every reachable run's Objects table is negative is
missing, and that is a proof. A plan with any missing object fails up front.

A plan that depends on a run whose INDEX the reader does not yet hold is
valid and must not be refused; the reader confirms that run's objects
against its own INDEX when the disc is in the drive, which is the first
thing the reader reads from that disc.

---
## 12. Reader and writer rules

### 12.1 Reader procedure

A reader applies these steps in order, for every structure:

1. Check `magic_project` and `magic_kind`. Refuse on a mismatch.
2. Check `version_major`. Refuse an unknown value and print the value.
3. Read `header_len` from the common header and skip the excess. Never assume
   the compiled size.
4. Verify the checksum before using any field.
5. Verify the content id after decompression, for an object.

A reader verifies the hash and the CRC of a structure before it uses any field
of it.

Every object's content id is verified after it is read. A mismatch is a hard
error. Every function that returns object bytes verifies the content id before
it returns. There is no trusted path.

### 12.2 Writer rules

1. Never reuse a registry id.
2. Never change a frozen table under an existing name.
3. Record every parameter that a future diagnostic tool would need, even when a
   reader does not need it.
4. Never depend on a structure that a later version might change. Read the
   version first, and the `header_len`.
5. Write zero into every reserved field and every padding byte.

### 12.3 Which catalog a reader trusts

A reader takes the catalog of the newest run on the disc that verifies. A run
verifies when both of these pass: the `header_crc32c` of a run header copy the
reader could read, and `index_hash`, checked against the bytes of
`INDEX.bin`.

A run that fails either check is skipped, whatever its `run_seq`, and the
reader steps back along the chain to the previous run and reports the run it
skipped.

A local index is an accelerator only. Everything in it is derived from discs
and is rebuildable. Every command behaves the same, apart from speed, with the
local index deleted.

### 12.4 Conformance

A conforming reader of format major 1:

1. reads every structure of this document at `version_major` 1 and any
   `version_minor`, by the rules of section 12.1;
2. reads objects whose `hash_algo` is `sha2-256`, and refuses an object
   whose `hash_algo` it does not implement, naming the code; it reads
   compression ids 0 and 1, may refuse id 2, `lz4`, and must then name the
   id;
3. reads a disc of profile 0 and of profile 1, which share one filesystem; it
   may refuse profile 2 and must then name the profile;
4. finds every object through the filesystem, the run headers, INDEX and the
   catalog, with no local index and no disc other than the ones the plan
   names, and by carving (section 7.10) when the filesystem directory is
   damaged;
5. verifies every CRC, every hash of section 2.7 and every content id before it
   uses the bytes;
6. repairs a run with `k = 231`, `m = 23`, or says that it cannot repair;
7. refuses an unknown `version_major`, an unknown registry id in a field it
   must interpret, and a critical TLV it does not know, and says which.

A conforming writer of format major 1:

1. writes every structure exactly as its byte-offset table states, with
   `version_major` 1, `version_minor` 0 and reserved fields zero;
2. writes SHA-256 content ids over the uncompressed payload, and FastCDC cut
   points by section 4.2;
3. writes every run with `k = 231`, `m = 23`, the checksum column of section
   10.3, `m + 2` header copies, the fill order of section 8.7 and the catalog
   copies of section 11.4;
4. writes every byte as an ordinary file under `/NOAHSARK/`, on a sector
   boundary, and never rewrites a burned run;
5. writes only a disc filesystem profile that it implements, and never a
   reserved id, bit or value.

### 12.5 Change mechanisms

Every change uses one mechanism: a version bump. A minor bump appends fields
an old reader can ignore; a major bump changes shape and an old reader
refuses it, printing `version_major` (section 2.3).

A registry id is never reused and never renumbered. A new value in an
existing registry field needs no version bump at all: an old reader already
refuses a registry id it does not know, in the field it was already reading.

| Change | Mechanism | Old reader does | New reader does |
|---|---|---|---|
| New hash algorithm | New id in the hash registry. | Refuses an object whose `hash_algo` it does not know, and says the code. Old discs stay readable. | Reads `sha2-256`. Refuses an object whose `hash_algo` it does not implement, and says the code. Writes `sha2-256`. |
| Chunker profile change | New id in the chunker registry. | Unaffected. A reader never needs the profile. | Uses the new profile for new runs. Old runs keep theirs. |
| Gear table change | New `gear_table_id` and a new profile name. | Unaffected. | Must never reuse an existing profile name with a different table. |
| New compression algorithm | New id in the compression registry. | Refuses an object whose `compression` it does not know. The object is unreadable, not misread. | Reads it. |
| FEC parameter change | A new `version_major` of the run header. It already records `k` and `m`. | Refuses the run for repair, because version 1 accepts no pair other than 231 and 23. Still reads the objects through the filesystem or by carving. | Reads the recorded pair. |
| New FEC scheme | New id in the FEC scheme registry. | Refuses the run for repair, but still reads its objects. | Repairs it. |
| Filesystem revision change | `fs_revision` in the superblock. | Depends on the operating system, not on NoahsArk. | Same. |
| Disc filesystem profile change | New id in the profile registry. | Refuses a profile it does not know, and says the id. | Reads it. |
| New object kind | New id in the object kind registry. | Refuses the structure, since `kind` is already outside 1 to 4. | Reads it. |
| New tree TLV | New id in the TLV registry. The critical bit decides. | Refuses the entry when the critical bit is set. Preserves and reports the TLV otherwise. | Applies it. |
| Encryption | Non-zero `crypto` byte, reserved in version 1. | Refuses the object. | Decrypts it. |
| Snapshot signature | New snapshot TLV, non-critical. | Ignores it. | Verifies it. |

### 12.6 Cross-version and cross-phase reading

`Y` means full use. `~` means partial use with the loss named. `N` means a
clean refusal that names the reason.

A reader that implements profile 0 and profile 1 only:

- reads every object and every catalog table normally;
- refuses a disc whose `fs_profile` is 2 and names the id;
- reports a cross-disc parity group as not supported;
- reports `run_kind` 3, a disc-close parity run, as not supported, and still
  reads every object of that run;
- reads a raw-append disc's objects by carving (section 7.10) when the
  filesystem directory does not show them.

No data is lost in any of those cases.

By format version:

| Writer | Reader of major 1 | Reader of a later major |
|---|---|---|
| Major 1, minor 0 | Y | Y. It reads the fixed sizes of major 1. |
| Major 1, minor above 0, appended fields only | Y. It skips `header_len - known`, and ignores fields it does not know elsewhere. | Y |
| A later major | N. Refuses and prints `version_major`. | Y |

By algorithm and registry id:

| Written with | Reader that knows it | Reader that does not |
|---|---|---|
| SHA-256 ids | Y. Mandatory for a conforming reader. | Not possible at major 1. |
| A new hash algorithm id | Y | N. Refuses the object and names the multicodec code. Older discs stay readable. |
| compression 0 or 1 | Y | Not possible at major 1. |
| compression 2, `lz4` | Y | N, permitted. Refuses the object and names the id. |
| A new compression id | Y | N. Refuses the object and names the id. |
| A new chunker profile | Y | Y. A reader never needs the profile. |
| A non-critical unknown tree TLV | Y | ~ Preserves it on copy, reports it on restore, does not apply it. |
| A critical unknown tree TLV, 0x8000 to 0xBFFF | Y | N. Refuses the entry and names the type. |
| `k`, `m` other than 231, 23 | Y | N for repair; still reads every object. |
| A new FEC scheme id | Y | N for repair; still reads every object. |

### 12.7 Refusal and partial reading

Every refusal is loud, names the field and the value, and never touches the
bytes it refused. Every partial case reads the data in full and puts the loss
in the report. An empty refusal, a silent skip and a misread are all defects.

An error about an object names the object. An error about a run names the run
seq and the disc uuid.

### 12.8 Settings that change disc bytes

The settings below change what a writer puts on the medium. A change to any of
them, after a repository holds discs, is a new epoch or a new profile, never a
silent in-place change. An omission from this index is a defect in the index,
not license to change a setting silently.

| Setting | What it changes on disc |
|---|---|
| Format version, `format.version_major` and `format.version_minor` | The `version_major` and `version_minor` of every structure a writer emits (section 2.6). |
| Current hash algorithm | The multihash algorithm of every new object id. |
| Chunker profile | The cut points of every new chunk, hence the chunk boundaries in every new tree and blob. |
| Gear table id | The Gear table version, which changes every cut point under a profile name. |
| Compression algorithm and level | The stored bytes of every new chunk payload. |
| Compression minimum gain | Whether a chunk is stored compressed or raw. |
| Filesystem profile | The disc filesystem. Fixed per disc at its first burn. |
| Fan-out levels | The object path depth under `objects/`. |
| Filesystem revision | The revision the image builder writes. |
| Close policy, `disc.close_policy` | Whether the first burn seals the disc, hence the superblock `sealed` byte, the `spare_area` term and the tail anchors (sections 7.5, 8.1 and 7.11). |
| Spare mode and spare reserve bytes | The `spare_area` term, hence `reserve` and `data_budget`. Fixed per disc at format time. |
| Expected runs per disc | `catalog_growth`, hence `reserve` and `data_budget`. |
| Fill ratio | `safety_margin`, hence `reserve` and `data_budget`. |
| Forced capacity, `disc.force_capacity` | `capacity_forced_sectors` in the superblock and in DISCS, hence every run's budget on the disc (section 7.13). |
| FEC scheme and geometry, `fec.scheme`, `fec.k` and `fec.m` | The stripe shape, the column count, the parity file set and the checksum column, hence the FEC stream layout of every run (sections 10.1 and 10.2). Version 1 fixes `k` 231 and `m` 23 and refuses any other value. |
| Optional metadata switches | Which optional metadata fields are present in every new tree entry. |
| Source type, `source.type` | The `source_type` and `source_flags` bytes of every new snapshot payload, hence its content id (section 6.14). |
| Exclude rules, `sources.exclude` and `sources.ignore_file` | Which paths the walk keeps, and the exclude-rule bytes that snapshot metadata tag 5 stores (sections 6.16 and 6.14). |
| Mount-point crossing, `sources.one_file_system` | Whether the walk descends into a directory that lies on another filesystem. It decides which entries exist in every new tree, and therefore every tree id, every snapshot id and the object set of the run. |
| Symlink following, `sources.follow_symlinks` | Whether the walk follows a symlink and stores the target's content, or stores the link itself as an `entry_type` 3 symlink entry (section 6.6). It decides the entry type, the size and the content reference of every affected entry, and therefore every tree id above it. |
| Label template and repository short name | The label text in the superblock, in DISCS and in `README.txt`. |
| Locality and split settings | Which chunks are rewritten instead of cross-referenced, hence the duplicated bytes on disc. |

---
## 13. Golden vectors

Every implementation ships a golden-file test per structure: write known values
and compare bytes, then read the file back and compare fields.

A golden vector is a checked-in input and its expected output. The expected
values are not printed in this document, apart from the printed `k = 3`,
`m = 2` parity example of section 10.2 and the CRC-32C check value of section
2.1. A vector file is named by the structure and the version. A vector is never
changed under a version; a format change adds a vector.

The Gear table and the mask constants are vendored: they are generated exactly
once, checked in as a literal array, and never regenerated from a dependency.
The golden vectors cover them.

| Vector | Input |
|---|---|
| Gear table | The rule of section 4.8 |
| Masks | The rule of section 4.9 |
| Cut points, per profile | A fixed 256 MiB pseudo-random file, generated from a stated seed by a stated generator, and a fixed 3 MiB file |
| Zero chunk | 16 MiB, 8 MiB and 32 MiB of zero bytes |
| Multihash text form | One digest under sha2-256 |
| Common header and object header | One chunk of stated bytes, stored with zstd level 3 under the frame parameters of section 5.3 and a named `tool_version`, and stored uncompressed |
| Blob | 100 stated chunk ids and lengths |
| Tree | A directory with a regular file, addressed through its blob object, a subdirectory, a symlink with its TLV target, two entries sharing one source inode and stored as independent entries, a device node, and one xattr TLV, with stated metadata |
| Snapshot | A stated root tree, parent, generation, times and TLVs |
| Ref record | A stated name, snapshot id, time and run seq |
| Disc superblock | Stated identity, capacity and profile values |
| Run header | Stated geometry and counts |
| INDEX | Ten stated Files rows, ten stated Objects rows, and two stated Prereqs rows across one source run |
| README.txt | The identity values of the disc superblock vector |
| FORMAT.txt | Format major 1, minor 0. The major 1 minor 0 text is 21,718 bytes long in 533 lines; a writer that produces a different length for minor 0 has a defect. |
| Burn step tree listing | A stated tree of five files, one in a subdirectory |
| REFS and DISCS | Two stated rows of each table |
| Checksum block | The 231 stated data blocks of one stripe |
| Parity | A stated stripe of `k` data blocks, plus recovery after `m` erasures |
| Local ref log and notes file | Two stated records of each |
| State log | Three stated records, plus a truncated-replay result |
| Burn plan | A stated plan with two steps |
| Root tree name encoding | `/srv/data`, `/a%b/c`, `/x\y` |
| Exclude patterns | A stated tree and a stated rule set |
| CRC-32C | The 9-byte string `123456789` |

---

## Appendix A. FORMAT.txt, format major 1 minor 0

The text below is the exact content of `/NOAHSARK/FORMAT.txt`. A writer writes
these bytes and no others. The first line is the one substitution slot: it
carries the `version_minor` of the superblock in decimal, which is 0 here.

Section 13 states the exact byte and line count.

```
NoahsArk format major 1 minor 0

1. FORMAT RULES
===============

1. Every integer is little-endian. No big-endian field exists.
2. Only fixed-width types are used: u8, u16, u32, u64, i32, i64. No varint appears inside a fixed header.
3. Every structure is packed with manual alignment. Every gap is a named reserved field. A writer writes zero into every reserved field and every padding byte. A reader does not interpret a reserved field and does not reject a nonzero value in one. A golden test checks that the writer wrote zero into every reserved field and every padding byte.
4. Every structure begins with the 32-byte common header.
5. A structure is a file-level container or an object payload header. A record inside a structure, a tree entry, a TLV, an INDEX table row, a REFS or DISCS row, is a record, not a structure. A record never carries the common header. A record carries a magic only where its own table states one.
6. A reader refuses an unknown version_major. A reader accepts an unknown version_minor and ignores the fields it does not know.
7. A version_minor bump may only append fields to the end of the fixed body, and an old reader must be able to read the structure correctly while ignoring them. Any change an old reader cannot ignore is a version_major bump, and an old reader refuses it.
8. A checksum covers only bytes the writer finalized before computing it. A structure with no separate body ends with one CRC over every byte before it. A container with a header and a body carries header_crc32c and body_crc32c at the end of its header: body_crc32c sits in the header and covers the body. The body becomes final first, then the header is written last, so each CRC covers bytes that were already final.
9. CRCs are CRC-32C: polynomial 0x1EDC6F41, reflected, initial value 0xFFFFFFFF, final XOR 0xFFFFFFFF.
10. Objects use the full content hash in place of a body CRC. The content id is the checksum of the payload.
11. Every pointer carries the hash of its target. No unhashed reference exists.
12. A string is encoding (u8), reserved (u8[3], zero), length (u32) in bytes, then the bytes. Encoding 0 is UTF-8. There is no NUL terminator and no normalization.
13. Every structure has a byte-offset table with the columns offset, size, type, name and meaning.
14. Every file that the format writes begins on a sector boundary of the medium. A structure's fixed header carries enough length information to find the structure's own end without reading anything past it, so that a tool can find a structure by scanning the raw medium for the project magic.

String layout
-------------

offset	size	type	name	meaning
0	1	u8	encoding	0 = UTF-8. Other values reserved.
1	3	u8[3]	reserved	Zero. Keeps length aligned.
4	4	u32	length	Byte count of the string data.
8	length	u8[]	data	The bytes, as found. No terminator.

2. REGISTRIES
=============

Hash algorithm registry
-----------------------

Code	Name	Digest bytes	Status
0x12	sha2-256	32	Phase 1 writes it. Every reader must read it.
0x1e	blake3	32	Reserved for a later version. A Phase 1 writer never emits it.
0x13	sha2-512	64	Reserved. Not used in version 1.
0x1020	sha2-512-256	32	Reserved for a later version. A Phase 1 writer never emits it.
0xb220	blake2b-256	32	Reserved.

Compression registry
--------------------

Id	Name	Status
0	none	Mandatory.
1	zstd	Default.
2	lz4	Optional.
3-255	reserved	Refuse.

Chunker profile registry
------------------------

Id	Name	min	avg	max	NC level
1	P3	512 KiB	2 MiB	8 MiB	2
2	P4	1 MiB	4 MiB	16 MiB	2
3	P5	2 MiB	8 MiB	32 MiB	2
4-255	reserved	-	-	-	-

FEC scheme registry
-------------------

Id	Name	Field	Shards	Status
1	rs255-gf8	GF(2^8)	255	Default. Phase 1. A stripe is k + 1 + m = 255 blocks; the code is over the k + m data and parity shards.
2-255	reserved	-	-	-

Object kind registry
--------------------

Id	Name
1	chunk
2	blob
3	tree
4	snapshot

A ref is a row of the REFS table. It is not an object kind and carries no id in this registry.

File role registry
-------------------

Id	Role
0	reserved
1	INDEX.bin, this file
2	RUN.bin
3	DISC.bin
4	README.txt
5	FORMAT.txt
6	reserved. Never assigned again: earlier document versions gave this id to a run-wide membership filter, since removed.
7	catalog/REFS.bin
8	catalog/DISCS.bin
9	a snapshot object copy under catalog/snapobj/, loose or packed
10	checksum.bin
11	a parity file
12	RUN2.bin
13	an object file under /NOAHSARK/objects/ or /NOAHSARK/snapshots/. Reserved for a later version: a container's role stays 13 too.
14	REFERENCE/decoder.py

Disc filesystem profile registry
--------------------------------

Id	Name	Filesystem	Append mechanism	Status
0	oneshot	UDF 2.01. Phase 1 builds UDF 2.01 only.	None in Phase 1. The disc is POW-formatted and left open, so a Phase 2 append can still reach it.	Default. Phase 1.
1-255	reserved	-	-	-

Media type registry
-------------------

Id	Name	Status
1	BD-R SL 25 GB	Default. Phase 1.
2	BD-R DL 50 GB	Phase 1.
3	BD-R XL 100 GB	Phase 1.
4	BD-R XL 128 GB	Phase 1.
5-255	reserved	-

media_type is informational. A reader never rejects a value it does not know,
and no rule depends on it. Capacity comes from capacity_sectors and
capacity_forced_sectors.

Source type registry
--------------------

Id	Name	Meaning
0	unknown	Not recorded. A pre-1.0 writer.
1	local	A local filesystem. Full metadata fidelity.
2	snapshot	A filesystem snapshot of a local filesystem.
3	nfs	An NFS mount.
4	smb	An SMB or CIFS mount.
5	bundle	Imported from a commit bundle (see the operations document). The bundle writer's own source type is in its BUNDLE.bin.

Tree TLV type registry
----------------------

Type	Name	Critical	Payload
0x0001	SYMLINK_TARGET	yes	Raw bytes. Mandatory when entry_type is 3. Never validated as UTF-8.
0x0002	USER_NAME	no	UTF-8 bytes.
0x0003	GROUP_NAME	no	UTF-8 bytes.
0x0004	ROOT_PATH	no	Raw bytes of a source root's absolute path. Only on an entry of the root tree; source roots are defined in the operations document.
0x0010	XATTR	no	Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a version_minor bump. A Phase 1 reader treats it as an unknown TLV.
0x0011	ACL_ACCESS	no	Reserved. Same rule as XATTR.
0x0012	ACL_DEFAULT	no	Reserved. Same rule as XATTR.
0x0013	ACL_NFS4	no	Reserved. Same rule as XATTR.
0x0020	LINUX_ATTR	no	Reserved. Same rule as XATTR.
0x0021	BSD_FLAGS	no	Reserved. Same rule as XATTR.
0x0030	WIN_ATTRS	no	Reserved. Same rule as XATTR.
0x0031	WIN_SD	no	Reserved. Same rule as XATTR.
0x0032	WIN_ADS	no	Reserved. Same rule as XATTR.
0x8000-0xBFFF	reserved critical	yes	Future critical extensions.
0xF000-0xFFFF	vendor	no	Never critical.

3. STRUCTURES
=============

Common header
-------------

offset	size	type	name	meaning
0	8	u8[8]	magic_project	ASCII "NOAHSARK".
8	8	u8[8]	magic_kind	ASCII kind name, zero-padded to 8 bytes. Compared as all 8 bytes.
16	2	u16	version_major	Refuse an unknown value.
18	2	u16	version_minor	Accept an unknown value; read min(header_len, known length).
20	2	u16	header_len	Common header plus the fixed body.
22	2	u16	reserved_u16	Zero.
24	8	u64	reserved_u64	Zero.

Object header
-------------

offset	size	type	name	meaning
0	1	u8	kind	Object kind registry.
1	1	u8	hash_algo	Multicodec code. 0x12 sha2-256 in version 1.
2	1	u8	digest_len	Digest length in bytes. 32 in version 1.
3	1	u8	compression	Compression registry. 0 none, 1 zstd, 2 lz4.
4	1	u8	crypto	0 plaintext. Other values reserved.
5	1	u8	reserved_u8	Zero.
6	2	u16	reserved_u16	Zero. Keeps payload_len aligned.
8	8	u64	payload_len	Uncompressed payload length in bytes.
16	8	u64	stored_len	Bytes on the medium after this header.
24	4	u32	header_crc32c	CRC-32C over the common header and bytes 0 to 23 of this header.
28	4	u32	reserved_u32	Zero.

Blob payload header
--------------------

offset	size	type	name	meaning
0	8	u64	entry_count	Number of entries.
8	8	u64	total_size	Sum of length over all entries.
16	2	u16	entry_size	48.
18	1	u8	hash_algo	Multicodec code.
19	1	u8	digest_len	32.
20	1	u8	level	0 = entries are chunks. 1 = entries are blobs.
21	3	u8[3]	reserved	Zero.
24	-	-	entries	entry_count records of 48 bytes.

Blob entry
----------

offset	size	type	name	meaning
0	32	u8[32]	content_id	Chunk id, or child blob id.
32	8	u64	length	Uncompressed bytes this entry contributes.
40	8	u64	file_offset	Offset of this entry inside the file.

Tree payload header
--------------------

offset	size	type	name	meaning
0	4	u32	entry_count	Number of entries.
4	4	u32	reserved_u32	Zero. Keeps entries 8-byte aligned.
8	-	-	entries	Entries, back to back, each self-delimiting.

Tree entry
----------

offset	size	type	name	meaning
0	4	u32	entry_len	Total entry length including all variable areas. Multiple of 8.
4	2	u16	header_len	112 in version 1. A reader skips the excess.
6	1	u8	entry_type	1 regular, 2 directory, 3 symlink, 4 chardev, 5 blockdev, 6 fifo, 7 socket. 0 is invalid.
7	1	u8	entry_flags	See entry flags below.
8	8	u64	size	Regular files only. 0 otherwise.
16	8	u64	hardlink_group	Reserved for a later phase. A Phase 1 writer writes 0.
24	8	i64	mtime_sec	Seconds since 1970-01-01 UTC.
32	8	i64	atime_sec	Valid only when ATIME_ABSENT is clear.
40	8	i64	ctime_sec	Valid only when CTIME_ABSENT is clear.
48	8	i64	btime_sec	Valid only when BTIME_ABSENT is clear.
56	4	u32	mtime_nsec	0 to 999,999,999.
60	4	u32	atime_nsec	Same range.
64	4	u32	ctime_nsec	Same range.
68	4	u32	btime_nsec	Same range.
72	4	u32	mode	Low 12 bits are rwxrwxrwx plus setuid 04000, setgid 02000, sticky 01000. Bits 12 to 31 reserved, zero. The file type is not here.
76	4	u32	uid	0xFFFFFFFF means unknown.
80	4	u32	gid	0xFFFFFFFF means unknown.
84	4	u32	rdev_major	Device entries only, else 0.
88	4	u32	rdev_minor	Device entries only, else 0.
92	4	u32	content_off	Offset to the content reference area. 0 when there is none.
96	4	u32	content_len	Bytes in that area.
100	4	u32	ext_off	Offset to the TLV area. 0 when ext_len is 0.
104	4	u32	ext_len	Bytes in the TLV area, padding included.
108	2	u16	name_off	Offset to the name bytes. 112 in version 1.
110	2	u16	name_len	Name length in bytes, 1 to 4095. No terminator.

Entry flags
-----------

Bit	Name	Meaning
0	HARDLINK_MEMBER	Reserved for a later phase. A Phase 1 writer clears this bit.
1	ATIME_ABSENT	atime_sec and atime_nsec carry no information.
2	CTIME_ABSENT	ctime_sec and ctime_nsec carry no information.
3	BTIME_ABSENT	btime_sec and btime_nsec carry no information.
4	SPARSE	The source file had holes.
5	METADATA_PARTIAL	The source read failed for at least one metadata field.
6	reserved	Zero. A version 1 writer never sets this bit.
7	UNSTABLE	The file changed while it was being read, and no earlier consistent version existed.

Tree extension TLV record
--------------------------

offset	size	type	name	meaning
0	2	u16	tlv_type	Tree TLV type registry.
2	2	u16	tlv_flags	bit0 CRITICAL. Bits 1 and 2 reserved (an earlier spill mechanism, removed). Bits 3 to 15 reserved.
4	4	u32	tlv_len	Payload bytes, excluding this prefix and excluding padding.
8	tlv_len	u8[]	payload	The value, always inline.
-	pad	u8[]	-	Zero bytes to the next 8-byte boundary.

Snapshot payload
----------------

offset	size	type	name	meaning
0	32	u8[32]	root_tree	Content id of the root tree.
32	32	u8[32]	parent	Content id of the parent snapshot. All zero for a root.
64	8	u64	generation	1 + parent generation. 1 for a root.
72	8	i64	time_sec	Snapshot time, seconds.
80	4	u32	time_nsec	Nanoseconds.
84	4	i32	tz_offset_sec	Local zone offset at snapshot time.
88	8	u64	total_size	Sum of payload_len over the distinct objects that reachable_object_count counts.
96	8	u64	reachable_object_count	Distinct content ids reachable from root_tree: chunks, blobs and trees, the root tree included.
104	1	u8	hash_algo	Multicodec code of root_tree and of every id below it.
105	1	u8	chunker_profile	Chunker profile id used to produce it.
106	2	u16	meta_count	Number of TLV records that follow.
108	1	u8	source_type	Where the source tree was read from.
109	1	u8	source_flags	What the source could not provide.
110	1	u8	parent_hash_algo	Multicodec code of parent. 0 for a root.
111	1	u8	reserved_u8	Zero.
112	-	-	TLV records	meta_count records follow.

Snapshot metadata TLV record
-----------------------------

offset	size	type	name	meaning
0	2	u16	tag	Snapshot metadata tag registry: 1 author, 2 host, 3 message, 4 source root, 5 exclude rules, 6 checksum commit.
2	2	u16	flags	bit0 CRITICAL.
4	4	u32	len	Payload bytes.
8	len	u8[]	value	UTF-8 for tags 1 to 3. Raw path bytes for tag 4. Pattern lines for tag 5. Empty for tag 6.
-	pad	-	-	Zero to the next 4-byte boundary.

Ref record
----------

offset	size	type	name	meaning
0	32	u8[32]	snapshot_id	Content id of the snapshot.
32	8	i64	time_sec	When the ref took this value.
40	4	u32	time_nsec	Nanoseconds.
44	2	u16	name_len	Byte length of the name, 1 to 40.
46	1	u8	hash_algo	Multicodec code of snapshot_id.
47	1	u8	reserved_u8	Zero.
48	40	u8[40]	name	UTF-8, zero-padded. A name above 40 bytes is refused at commit time.
88	8	u64	run_seq	Run that recorded this value.

Disc superblock
----------------

offset	size	type	name	meaning
0-31	32	-	common header	magic_kind "DISC".
32	16	u8[16]	disc_uuid	Unique for this physical disc.
48	16	u8[16]	repo_uuid	The repository this disc belongs to.
64	8	u64	disc_seq	Monotonic position in the repository.
72	8	u64	capacity_sectors	As reported by the drive at first write.
80	8	u64	capacity_forced_sectors	Forced capacity. Equals capacity_sectors when no override was given.
88	32	u8[32]	prev_disc_super_hash	Hash of the previous verified superblock. All zero when there is none.
120	8	i64	created_sec	Pack time of the first run, seconds.
128	4	u32	created_nsec	Nanoseconds.
132	4	i32	tz_offset_sec	Local zone offset at that pack time.
136	1	u8	media_type	Media type registry. Informational only; never gates reading.
137	1	u8	fs_profile	Disc filesystem profile registry.
138	1	u8	fanout_levels	1 by default. 2 is allowed under profile 1 and profile 0 only.
139	1	u8	capacity_is_forced	1 when capacity_forced_sectors is below capacity_sectors.
140	1	u8	sealed	1 when the disc was burned sealed at its first write. 0 when left open.
141	3	u8[3]	reserved_u8	Zero. Keeps label_len aligned.
144	4	u32	label_len	Byte length of the label.
148	64	u8[64]	label	UTF-8, zero-padded.
212	4	u32	tool_version	Writer registry id in the high 8 bits, writer-defined version in the low 24 bits. Informational only.
216	4	u32	reserved_u32	Zero.
220	1824	u8[1824]	reserved	Zero.
2044	4	u32	super_crc32c	CRC-32C over bytes 0 to 2043.

Run header
----------

offset	size	type	name	meaning
0-31	32	-	common header	magic_kind "RUN".
32	16	u8[16]	disc_uuid	The disc this run sits on.
48	16	u8[16]	repo_uuid	The repository.
64	8	u64	run_seq	Monotonic run number in the repository, 1-based.
72	8	u64	disc_seq	Disc sequence number, 0-based.
80	2	u16	fec_k	Data columns. 231 in version 1.
82	2	u16	fec_m	Parity columns. 23 in version 1.
84	1	u8	fec_scheme	FEC scheme registry.
85	1	u8	hash_algo	Multicodec code of every id in this run. Phase 1 writes 0x12.
86	1	u8	chunker_profile	Profile id.
87	1	u8	compression	Default compression id.
88	1	u8	fs_profile	Disc filesystem profile id.
89	1	u8	run_kind	1 data run, 2 repair run, 3 disc-close parity run. 0 is invalid.
90	1	u8	run_flags	Bit 0 CLOSING_RUN. Bits 1 to 7 reserved, zero.
91	1	u8	reserved_u8	Zero.
92	4	u32	reserved_u32a	Zero. Keeps index_bytes aligned.
96	8	u64	index_bytes	Byte length of INDEX.bin.
104	32	u8[32]	index_hash	Hash of INDEX.bin's bytes.
136	8	u64	stream_bytes	Byte length of the FEC stream.
144	32	u8[32]	prev_run_hash	Hash of the previous run header on this disc. Zero for the first run.
176	8	i64	created_sec	Pack time, seconds.
184	4	u32	created_nsec	Nanoseconds.
188	4	u32	tool_version	Writer registry id in the high 8 bits, writer-defined version in the low 24 bits. Informational only.
192	8	u64	disc_object_count	Objects on this disc after this run. Cumulative.
200	4	u32	disc_run_index	Index of this run on this disc. 0 for the first run.
204	4	u32	reserved_u32b	Zero.
208	296	u8[296]	reserved	Zero.
504	4	u32	header_crc32c	CRC-32C over bytes 0 to 503.
508	4	u8[4]	reserved_final	Zero.

INDEX header
------------

offset	size	type	name	meaning
0-31	32	-	common header	magic_kind "INDEX".
32	8	u64	run_seq	The run this INDEX describes.
40	4	u32	file_count	Rows in the Files table.
44	4	u32	object_count	Rows in the Objects table.
48	4	u32	prereq_count	Rows in the Prereqs table.
52	2	u16	file_record_size	48.
54	2	u16	object_record_size	72.
56	2	u16	prereq_record_size	40.
58	1	u8	hash_algo	Multicodec code of every id in this run.
59	1	u8	digest_len	32.
60	4	u8[4]	reserved	Zero. Keeps container_len aligned.
64	8	u64	container_len	Total length of this container in bytes.
72	4	u32	body_crc32c	CRC-32C over the Files table, the Objects table and the Prereqs table.
76	4	u32	header_crc32c	CRC-32C over bytes 0 to 75.
80	-	-	files	The Files table.
-	-	-	objects	The Objects table, sorted ascending by content_id.
-	-	-	prereqs	The Prereqs table.

INDEX Files row
----------------

offset	size	type	name	meaning
0	32	u8[32]	file_hash	Hash of the whole file's bytes.
32	8	u64	byte_len	Length of the file in bytes.
40	1	u8	role	File role registry.
41	7	u8[7]	reserved	Zero.

INDEX Objects row
-------------------

offset	size	type	name	meaning
0	32	u8[32]	content_id	The object id.
32	4	u32	file_index	0-based row index into the Files table. In this version, always the object's own file.
36	4	u32	reserved	Zero.
40	8	u64	offset	In this version, always 0. A later version's container gives this field a member's payload offset.
48	8	u64	stored_len	Bytes stored at offset.
56	8	u64	payload_len	Uncompressed payload bytes.
64	1	u8	kind	Object kind registry.
65	1	u8	compression	Compression id.
66	2	u16	flags	bit0 duplicate for locality, bit1 metadata object. Bits 2 to 15 reserved, zero.
68	4	u32	reserved	Zero.

INDEX Prereqs row
--------------------

offset	size	type	name	meaning
0	32	u8[32]	content_id	An object this run references but does not store.
32	8	u64	run_seq	The run that stores it.

Catalog table header (REFS, DISCS)
-------------------------------------

offset	size	type	name	meaning
0-31	32	-	common header	magic_kind "REFS" or "DISCS".
32	16	u8[16]	repo_uuid	The repository.
48	8	u64	record_count	Records that follow.
56	2	u16	record_size	96 for REFS, 176 for DISCS.
58	1	u8	hash_algo	Multicodec code of a digest in the records that has no hash_algo of its own.
59	1	u8	digest_len	32.
60	4	u32	reserved	Zero.
64	4	u32	body_crc32c	CRC-32C over the records.
68	4	u32	header_crc32c	CRC-32C over bytes 0 to 67.
72	-	-	records	Sorted, fixed-width.

DISCS row
---------

offset	size	type	name	meaning
0	8	u64	run_seq	The run, 1-based.
8	8	u64	disc_seq	The disc that holds it, 0-based.
16	16	u8[16]	disc_uuid	That disc's uuid.
32	32	u8[32]	run_hash	Hash of the 512 bytes of that run's RUN.bin.
64	8	i64	created_sec	Pack time of the run.
72	8	i64	last_verify_sec	Last verification time of the disc. 0 when never verified.
80	8	u64	capacity_sectors	Reported capacity of the disc.
88	8	u64	used_sectors	Sectors used on the disc after this run.
96	1	u8	run_status	1 verified, 2 unverified, 3 withdrawn. 0 is invalid.
97	1	u8	health	1 healthy, 2 degraded, 3 critical, 4 failed, 5 unknown, 6 unverified, this disc.
98	2	u16	rs_margin_percent	Worst-stripe margin, as a percentage of m.
100	2	u16	label_len	Byte length of the label.
102	64	u8[64]	label	UTF-8, zero-padded.
166	1	u8	state_flags	bit0 closed, bit1 append-raw-only, bit2 spare below the threshold, bit3 capacity forced.
167	1	u8	reserved	Zero. Keeps capacity_forced_sectors aligned.
168	8	u64	capacity_forced_sectors	Forced capacity.

Checksum block record
-----------------------

offset	size	type	name	meaning
0	8	u8[8]	magic_kind	ASCII "CHECKSUM".
8	4	u32	stripe_index	i, the stripe whose data digests follow.
12	2	u16	digest_count	k. 231 in version 1.
14	1	u8	digest_bytes	8.
15	1	u8	hash_algo	0x12, sha2-256.
16	4	u32	header_crc32c	CRC-32C over bytes 0 to 15.
20	1848	u8[1848]	digests	k digests, in data column order 0 to k - 1.
1868	180	u8[180]	reserved	Zero.

4. MAGIC VALUES
===============

magic_kind (8 ASCII bytes, zero-padded)	Structure
CHUNK\0\0\0	Chunk object
BLOB\0\0\0\0	Blob object
TREE\0\0\0\0	Tree object
SNAPSHOT	Snapshot object
BUNDLE\0\0	Reserved for a later version; a Phase 1 writer never emits it; a reader refuses it
DISC\0\0\0\0	Disc superblock
RUN\0\0\0\0\0	Run header
INDEX\0\0\0	Run index
CHECKSUM	Checksum column record magic
REFS\0\0\0\0	Ref table
DISCS\0\0\0	Disc directory table

magic_project is the 8 ASCII bytes "NOAHSARK", the same for every structure.

5. CHUNKING CONSTANTS
======================

Gear table generation rule
--------------------------

seed = "noahsark/gear/v1"                      # 16 ASCII bytes, no terminator

for i in 0 .. 255:
    input      = seed || u8(i)                 # 17 bytes
    digest     = SHA-256(input)                # 32 bytes
    Gear[i]    = little-endian u64 of digest[0 .. 7]

Mask generation rule
--------------------

mask_s = spread_mask(b + 2)      # more one-bits: cutting is less likely
mask_l = spread_mask(b - 2)      # fewer one-bits: cutting is more likely

spread_mask(n):                       # 1 <= n <= 32
    mask = 0
    for j in 0 .. n-1:
        mask |= 1 << (63 - floor(j * 32 / n))
    return mask

Mask constants
--------------

P3_MASK_S	0xeeddbb7600000000
P3_MASK_L	0xd6b5ad6a00000000
P4_MASK_S	0xeeeeeeee00000000
P4_MASK_L	0xdadadada00000000
P5_MASK_S	0xf7bbddee00000000
P5_MASK_L	0xdb6db6da00000000

6. CHECKSUM PARAMETERS
======================

CRC32C_POLYNOMIAL	0x1edc6f41
CRC32C_REFLECTED	1
CRC32C_INIT	0xffffffff
CRC32C_XOROUT	0xffffffff
BLOCK_BYTES	2048
FEC_K	231
FEC_M	23
```

---

## Appendix B. Decision index

This index lists every design decision of the source ledger and the section of
this document that carries it. A dash means the decision belongs to the
operations or the notes document and is not in this document. Each cell is one
decision id followed by its section number.

| Id | Sec | Id | Sec | Id | Sec | Id | Sec | Id | Sec | Id | Sec |
|---|---|---|---|---|---|---|---|---|---|---|---|
| D001 | 2.1 | D002 | 2.1 | D003 | 2.1 | D004 | 2.1 | D005 | 2.1 | D006 | 2.1 |
| D007 | 2.1 | D008 | 2.1 | D009 | 2.1 | D010 | 2.1 | D011 | 2.1 | D012 | 2.1 |
| D013 | 2.1 | D014 | 2.1 | D015 | 2.2 | D016 | 2.3 | D017 | - | D018 | - |
| D019 | - | D020 | - | D021 | 2.5 | D022 | 2.6 | D023 | 2.6 | D024 | 2.6 |
| D025 | 2.6 | D026 | 13 | D027 | 2.7 | D028 | 12.1 | D029 | 2.9 | D030 | 3.1 |
| D031 | 3.1 | D032 | 3.1 | D033 | 3.2 | D034 | 3.2 | D035 | 3.3 | D036 | 3.3 |
| D037 | 3.4 | D038 | 3.5 | D039 | 3.5 | D040 | 3.5 | D041 | 3.5 | D042 | 3.5 |
| D043 | 3.6 | D044 | 3.6 | D045 | 3.6 | D046 | 3.6 | D047 | 3.6 | D048 | 4.1 |
| D049 | 4.1 | D050 | 4.2 | D051 | 4.2 | D052 | 4.2 | D053 | 4.2 | D054 | 4.3 |
| D055 | 4.3 | D056 | 4.1 | D057 | 4.4 | D058 | 4.4 | D059 | 4.4 | D060 | 4.4 |
| D061 | 4.5 | D062 | 4.6 | D063 | 4.6 | D064 | 4.6 | D065 | 4.6 | D066 | 4.6 |
| D067 | 4.6 | D068 | 4.7 | D069 | 5.1 | D070 | 5.1 | D071 | 5.2 | D072 | 5.3 |
| D073 | 5.3 | D074 | 5.3 | D075 | 5.3 | D076 | 5.4 | D077 | 5.4 | D078 | 5.5 |
| D079 | 5.6 | D080 | 6.1 | D081 | 6.1 | D082 | 6.1 | D083 | 6.2 | D084 | 6.4 |
| D085 | 6.4 | D086 | 6.4 | D087 | 6.3 | D088 | 6.3 | D089 | 6.3 | D090 | 6.3 |
| D091 | 6.3 | D092 | 6.3 | D093 | 6.3 | D094 | 6.3 | D095 | 6.3 | D096 | 6.3 |
| D097 | 6.3 | D098 | 6.3 | D099 | 6.3 | D100 | 6.3 | D101 | 6.5 | D102 | 6.5 |
| D103 | 6.8 | D104 | 6.8 | D105 | 6.8 | D106 | 6.8 | D107 | 6.7 | D108 | 6.7 |
| D109 | 6.9 | D110 | 6.9 | D111 | 6.9 | D112 | 6.9 | D113 | - | D114 | - |
| D115 | - | D116 | 6.11 | D117 | 6.11 | D118 | 6.11 | D119 | 6.10 | D120 | 6.10 |
| D121 | 6.10 | D122 | 6.6 | D123 | 6.6 | D124 | 6.9 | D125 | 6.6 | D126 | 6.6 |
| D127 | 6.6 | D128 | - | D129 | - | D130 | - | D131 | - | D132 | - |
| D133 | - | D134 | - | D135 | - | D136 | - | D137 | 2.10 | D138 | - |
| D139 | - | D140 | - | D141 | 6.14 | D142 | 6.14 | D143 | 6.14 | D144 | 6.14 |
| D145 | 6.14 | D146 | 6.14 | D147 | 6.14 | D148 | 6.14 | D149 | 6.14 | D150 | 6.17 |
| D151 | 6.17 | D152 | 6.17 | D153 | 6.17 | D154 | 6.17 | D155 | 6.17 | D156 | 6.18 |
| D157 | 6.19 | D158 | 6.19 | D159 | 7.1 | D160 | 7.1 | D161 | 7.1 | D162 | 7.2 |
| D163 | 7.2 | D164 | 7.2 | D165 | 7.2 | D166 | 7.5 | D167 | 7.5 | D168 | 7.5 |
| D169 | 7.5 | D170 | 7.5 | D171 | 7.5 | D172 | 7.5 | D173 | - | D174 | - |
| D175 | 7.12 | D176 | - | D177 | - | D178 | - | D179 | - | D180 | - |
| D181 | - | D182 | 7.12 | D183 | - | D184 | 7.2 | D185 | - | D186 | - |
| D187 | - | D188 | 7.13 | D189 | - | D190 | 7.14 | D191 | - | D192 | 7.14 |
| D193 | - | D194 | 7.3 | D195 | 7.3 | D196 | 7.3 | D197 | 7.3 | D198 | 7.3 |
| D199 | 7.3 | D200 | 7.6 | D201 | 7.6 | D202 | 7.6 | D203 | 7.6 | D204 | 7.6 |
| D205 | 7.6 | D206 | 7.6 | D207 | 7.6 | D208 | 7.6 | D209 | 7.7 | D210 | 7.7 |
| D211 | 7.7 | D212 | 7.7 | D213 | 7.7 | D214 | 7.9 | D215 | 7.9 | D216 | 7.9 |
| D217 | 8.3 | D218 | 7.10 | D219 | 7.10 | D220 | 7.10 | D221 | 7.10 | D222 | 7.10 |
| D223 | 7.10 | D224 | 7.10 | D225 | 7.10 | D226 | 7.10 | D227 | 7.10 | D228 | 7.10 |
| D229 | 7.11 | D230 | 7.11 | D231 | 8.1 | D232 | 8.1 | D233 | - | D234 | - |
| D235 | - | D236 | - | D237 | 8.7 | D238 | - | D239 | - | D240 | - |
| D241 | - | D242 | - | D243 | 7.5 | D244 | - | D245 | - | D246 | 8.1 |
| D247 | - | D248 | 3.5 | D249 | - | D250 | - | D251 | - | D252 | - |
| D253 | - | D254 | 8.2 | D255 | 8.2 | D256 | 8.2 | D257 | 8.4 | D258 | 8.4 |
| D259 | 8.5 | D260 | 8.5 | D261 | 8.5 | D262 | 8.5 | D263 | 8.8 | D264 | 8.8 |
| D265 | 8.8 | D266 | 8.7 | D267 | 8.7 | D268 | 8.7 | D269 | 8.7 | D270 | 8.7 |
| D271 | 8.7 | D272 | 8.7 | D273 | 8.7 | D274 | 8.7 | D275 | 8.7 | D276 | 8.7 |
| D277 | 8.7 | D278 | 8.7 | D279 | 8.7 | D280 | 8.7 | D281 | 8.7 | D282 | 8.7 |
| D283 | - | D284 | 8.7 | D285 | - | D286 | - | D287 | - | D288 | - |
| D289 | - | D290 | - | D291 | - | D292 | - | D293 | - | D294 | - |
| D295 | - | D296 | - | D297 | - | D298 | - | D299 | - | D300 | - |
| D301 | - | D302 | - | D303 | - | D304 | - | D305 | - | D306 | - |
| D307 | - | D308 | - | D309 | - | D310 | - | D311 | - | D312 | 9 |
| D313 | 9 | D314 | 9 | D315 | 9 | D316 | 9 | D317 | 9 | D318 | 9 |
| D319 | 9 | D320 | - | D321 | - | D322 | - | D323 | - | D324 | 10.1 |
| D325 | 10.1 | D326 | 10.1 | D327 | 10.1 | D328 | 10.1 | D329 | 10.1 | D330 | 10.1 |
| D331 | 10.1 | D332 | 10.1 | D333 | 10.1 | D334 | 10.1 | D335 | 10.1 | D336 | 10.2 |
| D337 | 10.2 | D338 | 10.2 | D339 | 10.2 | D340 | 10.2 | D341 | 10.2 | D342 | 10.1 |
| D343 | 10.4 | D344 | 10.4 | D345 | - | D346 | - | D347 | - | D348 | - |
| D349 | 7.11 | D350 | 10.6 | D351 | - | D352 | 10.7 | D353 | 10.7 | D354 | 10.7 |
| D355 | - | D356 | - | D357 | - | D358 | 10.3 | D359 | 10.3 | D360 | 10.3 |
| D361 | 10.3 | D362 | 10.3 | D363 | 10.3 | D364 | 10.5 | D365 | 10.3 | D366 | - |
| D367 | 11.3 | D368 | 11.1 | D369 | 11.1 | D370 | 11.1 | D371 | 11.1 | D372 | 11.1 |
| D373 | 11.1 | D374 | 11.1 | D375 | 11.1 | D376 | 11.1 | D377 | 11.1 | D378 | 11.1 |
| D379 | 11.1 | D380 | 11.1 | D381 | 11.1 | D382 | 11.1 | D383 | 11.2 | D384 | 11.2 |
| D385 | 11.2 | D386 | 11.2 | D387 | 11.2 | D388 | 11.2 | D389 | 11.2 | D390 | 11.2 |
| D391 | 11.2 | D392 | 11.3 | D393 | 11.3 | D394 | 11.3 | D395 | 11.3 | D396 | 11.3 |
| D397 | 11.3 | D398 | 11.3 | D399 | 11.4 | D400 | 11.4 | D401 | 11.4 | D402 | 11.4 |
| D403 | 11.4 | D404 | 11.4 | D405 | 11.4 | D406 | 11.4 | D407 | 11.5 | D408 | 11.5 |
| D409 | 11.5 | D410 | 11.5 | D411 | 11.5 | D412 | 11.5 | D413 | 11.5 | D414 | 11.5 |
| D415 | 11.5 | D416 | 11.5 | D417 | 11.6 | D418 | 11.6 | D419 | 11.6 | D420 | 11.3 |
| D421 | 11.3 | D422 | 11.3 | D423 | 11.3 | D424 | 11.3 | D425 | 11.3 | D426 | 11.3 |
| D427 | 11.3 | D428 | 11.4 | D429 | 11.4 | D430 | 11.4 | D431 | 11.6 | D432 | 11.6 |
| D433 | 11.6 | D434 | 11.6 | D435 | 12.3 | D436 | - | D437 | - | D438 | - |
| D439 | - | D440 | - | D441 | - | D442 | - | D443 | - | D444 | - |
| D445 | - | D446 | - | D447 | - | D448 | - | D449 | - | D450 | - |
| D451 | - | D452 | - | D453 | - | D454 | - | D455 | - | D456 | - |
| D457 | - | D458 | - | D459 | - | D460 | - | D461 | - | D462 | - |
| D463 | - | D464 | - | D465 | - | D466 | - | D467 | - | D468 | - |
| D469 | - | D470 | - | D471 | - | D472 | - | D473 | - | D474 | - |
| D475 | - | D476 | - | D477 | - | D478 | - | D479 | - | D480 | - |
| D481 | - | D482 | - | D483 | 8.7 | D484 | - | D485 | - | D486 | - |
| D487 | - | D488 | - | D489 | - | D490 | - | D491 | 11.2 | D492 | - |
| D493 | 11.2 | D494 | - | D495 | - | D496 | - | D497 | - | D498 | - |
| D499 | - | D500 | - | D501 | - | D502 | - | D503 | - | D504 | - |
| D505 | - | D506 | 6.7 | D507 | 6.7 | D508 | - | D509 | - | D510 | - |
| D511 | - | D512 | 6.15 | D513 | 6.15 | D514 | 6.15 | D515 | 6.15 | D516 | - |
| D517 | 6.16 | D518 | 6.16 | D519 | 6.16 | D520 | - | D521 | - | D522 | - |
| D523 | - | D524 | - | D525 | - | D526 | 6.14 | D527 | - | D528 | - |
| D529 | - | D530 | - | D531 | - | D532 | - | D533 | 11.6 | D534 | 11.6 |
| D535 | 11.6 | D536 | - | D537 | - | D538 | - | D539 | - | D540 | - |
| D541 | - | D542 | - | D543 | - | D544 | - | D545 | - | D546 | 12.1 |
| D547 | - | D548 | - | D549 | - | D550 | - | D551 | - | D552 | - |
| D553 | - | D554 | - | D555 | - | D556 | - | D557 | - | D558 | - |
| D559 | - | D560 | - | D561 | - | D562 | - | D563 | - | D564 | - |
| D565 | - | D566 | - | D567 | - | D568 | - | D569 | - | D570 | - |
| D571 | - | D572 | - | D573 | - | D574 | - | D575 | - | D576 | - |
| D577 | - | D578 | - | D579 | 12.8 | D580 | 12.8 | D581 | 12.5 | D582 | 12.5 |
| D583 | 12.5 | D584 | 12.2 | D585 | 12.1 | D586 | 12.6 | D587 | 12.4 | D588 | 12.4 |
| D589 | 12.7 | D590 | 12.4 | D591 | - | D592 | - | D593 | - | D594 | 2.10 |
| D595 | 2.10 | D596 | 2.10 | D597 | 2.10 | D598 | 2.10 | D599 | - | D600 | 2.10 |
| D601 | - | D602 | - | D603 | 1 | D604 | - | D605 | - | D606 | - |
| D607 | - | D608 | - | D609 | - | D610 | - | D611 | 13 | D612 | 13 |
| D613 | - | D614 | 4.8 | D615 | - | D616 | 12.1 | D617 | 12.7 | D618 | - |

Decisions listed: 618. Carried by this document: 386. Not in this document: 232.

