# NoahsArk on-disc format

Format major version 1. Document version 0.5.2.

This document defines every byte that NoahsArk writes onto a disc and every
rule a reader applies to those bytes. It covers the binary conventions, object
identity, chunking, compression, every object kind, the disc and run
structures, the filesystem layout, forward error correction, the run index
and catalog tables, and the checks a reader performs. The rules of this
document are normative. A second implementation that follows it produces
byte-identical discs from the same inputs and makes identical accept and
reject decisions on the same bytes. Commands, configuration, workflow,
rationale and local state live in other documents and are not repeated here.

A writer puts this document on every disc as `/NOAHSARK/FORMAT.txt`, byte for
byte (section 8.5). The copy on a disc is the document version that wrote the
disc, so it describes that disc exactly. This document is plain ASCII.

## Table of contents

- [1. Scope and conventions](#1-scope-and-conventions)
- [2. Binary format rules](#2-binary-format-rules)
  [2.1](#21-the-format-rules) | [2.2](#22-magic-values) | [2.3](#23-common-header) | [2.4](#24-strings) | [2.5](#25-registries) | [2.6](#26-version-policy) | [2.7](#27-hash-coverage-per-structure) | [2.8](#28-crc-coverage-per-structure) | [2.9](#29-limits) | [2.10](#210-trust-boundaries-and-safety-invariants)
- [3. Identity and hashing](#3-identity-and-hashing)
  [3.1](#31-the-content-id-rule) | [3.2](#32-hash-algorithms) | [3.3](#33-digest-fields-in-records) | [3.4](#34-text-form) | [3.5](#35-fan-out-on-disc)
- [4. Chunking](#4-chunking)
  [4.1](#41-algorithm) | [4.2](#42-cut-point-rule) | [4.3](#43-chunker-parameters) | [4.4](#44-zero-regions-and-sparse-files) | [4.5](#45-determinism) | [4.6](#46-gear-table) | [4.7](#47-mask-constants)
- [5. Compression](#5-compression)
  [5.1](#51-order-of-operations) | [5.2](#52-header-fields) | [5.3](#53-algorithm-and-frame-parameters) | [5.4](#54-minimum-gain) | [5.5](#55-compression-and-identity) | [5.6](#56-what-is-never-compressed)
- [6. Objects](#6-objects)
  [6.1](#61-object-kinds-and-the-object-header) | [6.2](#62-chunk) | [6.3](#63-blob) | [6.4](#64-tree) | [6.5](#65-tree-entry-fixed-header) | [6.6](#66-entry-flags) | [6.7](#67-variable-areas-and-the-content-area) | [6.8](#68-extension-tlv-record) | [6.9](#69-tlv-type-registry) | [6.10](#610-name-validation) | [6.11](#611-what-is-never-stored) | [6.12](#612-hardlinks) | [6.13](#613-snapshot) | [6.14](#614-the-root-tree) | [6.15](#615-ref) | [6.16](#616-canonical-ordering)
- [7. Disc and run model](#7-disc-and-run-model)
  [7.1](#71-the-physical-disc) | [7.2](#72-the-run-and-the-fec-terms) | [7.3](#73-what-the-parity-does-and-does-not-cover) | [7.4](#74-disc-superblock) | [7.5](#75-run-header) | [7.6](#76-run-header-copies)
- [8. Filesystem and the volume tree](#8-filesystem-and-the-volume-tree)
  [8.1](#81-the-udf-volume) | [8.2](#82-files-at-the-volume-root) | [8.3](#83-run-directory-naming) | [8.4](#84-readmetxt) | [8.5](#85-formattxt) | [8.6](#86-reference-decoder) | [8.7](#87-fill-order-inside-a-run) | [8.8](#88-name-and-path-budget)
- [9. Forward error correction](#9-forward-error-correction)
  [9.1](#91-parity-layout) | [9.2](#92-the-code) | [9.3](#93-checksum-column) | [9.4](#94-header-replication-and-parity-files) | [9.5](#95-decode-rule) | [9.6](#96-scheme-0-no-fec)
- [10. The run index and the catalog](#10-the-run-index-and-the-catalog)
  [10.1](#101-index) | [10.2](#102-refs) | [10.3](#103-discs) | [10.4](#104-catalog-contents-per-run) | [10.5](#105-dedup-rule) | [10.6](#106-proof-of-absence-and-coverage)
- [11. Reader and writer rules](#11-reader-and-writer-rules)
  [11.1](#111-reader-procedure) | [11.2](#112-writer-rules) | [11.3](#113-which-catalog-a-reader-trusts) | [11.4](#114-conformance) | [11.5](#115-change-mechanisms) | [11.6](#116-cross-version-and-unknown-value-reading) | [11.7](#117-refusal-and-partial-reading)
- [12. Golden vectors](#12-golden-vectors)

## 1. Scope and conventions

A NoahsArk disc carries one directory tree. Every byte NoahsArk writes is an
ordinary file inside that tree. There are no hidden sectors, no fixed-address
structures and no raw areas outside the filesystem.

```
/NOAHSARK/
    DISC.bin                     disc superblock
    README.txt                   plain-text explanation for a human
    FORMAT.txt                   this document
    REFERENCE/decoder.py         a standalone Python 3 reference decoder
    runs/<seq>/
        RUN.bin                  run header
        INDEX.bin                file order, object table, prerequisites
        catalog/REFS.bin         named pointers to snapshots
        catalog/DISCS.bin        the disc directory table
        checksum.bin             the checksum column of the FEC stream
        parity/pNNNN.bin         one file per parity column
        RUN2.bin                 run header copy
    objects/<ab>/<name>          chunks, blobs and trees
    snapshots/<name>             snapshot objects
```

`<ab>` is the first two hex digits of the digest. `<name>` is the full
68-character multihash hex (section 3.5).

A run is the unit of packing, of the object index, of the Reed-Solomon parity
and of the catalog copy. One disc holds one run.

A burned disc is immutable. Nothing rewrites it and nothing adds to it. Format
version 1 never removes a snapshot and never frees disc space on a burned
disc. There is no retention and no expiry, so no structure carries a deletion
record.

---

## 2. Binary format rules

### 2.1 The format rules

1. Every integer is little-endian. No big-endian field exists.
2. Only fixed-width types are used: u8, u16, u32, u64, i32, i64. No varint
   appears inside a fixed header.
3. Every structure is packed with manual alignment. Every gap is a named
   reserved field. A writer writes zero into every reserved field, every
   reserved bit and every padding byte. A reader does not interpret a
   reserved field, a reserved bit or a padding byte, and does not reject a
   nonzero value in one. A golden test checks that the writer wrote zero into
   every reserved field and every padding byte.
4. Every structure begins with the 32-byte common header of section 2.3.
5. A structure is a file-level container or an object payload header. A
   record inside a structure, a tree entry, a TLV, an INDEX
   table row, a REFS or DISCS row, is a record, not a structure. A record
   never carries the common header. A record carries a magic only where its
   own table states one. Every record has the fixed size that its table
   states. No structure stores a record size.
6. A reader refuses an unknown `version_major`. There is no minor version.
7. A later writer adds a field in one of two ways only: it gives meaning to
   reserved space, or it appends the field to the end of the fixed part and
   enlarges `header_len` (section 2.3). A reader of today reads both forms
   correctly, by rule 3 and by the `header_len` rule. Any other change is a
   `version_major` bump, and an old reader refuses it.
8. A checksum covers only bytes the writer finalized before computing it.
   The object header, the disc superblock, the run header and the checksum
   block carry one CRC each, over the bytes before the CRC field. INDEX, REFS
   and DISCS carry no CRC: a SHA-256 hash in another structure covers every
   byte of each (section 2.7).
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

The CRC-32C check value is `0xE3069283` for the 9-byte string `123456789`.

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
| `DISC\0\0\0\0` | Disc superblock | 7.5 |
| `RUN\0\0\0\0\0` | Run header | 7.6 |
| `INDEX\0\0\0` | Run index | 11.1 |
| `CHECKSUM` | Checksum column record | 10.3 |
| `REFS\0\0\0\0` | Ref table | 11.2 |
| `DISCS\0\0\0` | Disc directory table | 11.3 |

An implementation computes the eight bytes from the ASCII name and never
copies a hexadecimal column. A test asserts the two agree.

A parity file carries no magic and no header. It holds the blocks of one
parity column and nothing else (section 9.1). A reader identifies it by its
name.

### 2.3 Common header

Purpose: the 32-byte header that begins every structure of this document. It
carries identity and versioning in one place.

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u8[8] | `magic_project` | ASCII `"NOAHSARK"`. |
| 8 | 8 | u8[8] | `magic_kind` | ASCII kind name, zero-padded to 8 bytes, section 2.2. Compared as all 8 bytes. |
| 16 | 2 | u16 | `version_major` | 1. Refuse an unknown value. |
| 18 | 2 | u16 | `reserved_u16a` | Zero. |
| 20 | 2 | u16 | `header_len` | The length of the fixed part, see below. |
| 22 | 2 | u16 | `reserved_u16b` | Zero. |
| 24 | 8 | u64 | `reserved_u64` | Zero. |

Total: 32 bytes.

**`header_len`.** One definition holds for every structure. `header_len` is
the offset, from byte 0 of the structure, of the first byte after the fixed
part. The fixed part is the common header plus every fixed field of the
structure's own table, CRC and reserved fields included. The variable part,
that is the rows, the entries, the TLV records or the chunk bytes, starts at
offset `header_len`.

For an object file, the fixed part is the common header, the 32-byte object
header of section 6.1 and the fixed body of the kind. The object header ends
at offset 64, and the stored bytes start there. The fixed body of the kind is
the first `header_len - 64` bytes of the uncompressed payload, and the
entries or TLV records start at offset `header_len - 64` of the uncompressed
payload.

The writer of this version writes these values:

| Structure | `header_len` | What it counts |
|---|---:|---|
| Chunk | 64 | The common header and the object header. |
| Blob | 72 | The common header, the object header and the 8-byte blob body. |
| Tree | 72 | The common header, the object header and the 8-byte tree body. |
| Snapshot | 176 | The common header, the object header and the 112-byte snapshot body. |
| Disc superblock | 2048 | The whole superblock, `super_crc32c` included. |
| Run header | 512 | The whole header, `header_crc32c` and `reserved_final` included. |
| INDEX | 56 | The common header and the 24-byte fixed body. |
| REFS, DISCS | 56 | The common header and the 24-byte fixed body. |

A reader obeys `header_len`. Let `known_len` be the value of the table above
for the structure.

- When `header_len` equals `known_len`, the reader reads the structure as its
  table states.
- When `header_len` is above `known_len`, the reader reads the fields it
  knows at their stated offsets, ignores the bytes from `known_len` to
  `header_len`, and takes the variable part from offset `header_len`. It
  never takes the variable part from its own compiled size.
- When `header_len` is below `known_len`, the reader refuses the structure
  and names both values.

A CRC field keeps the offset that its table states, and it covers the bytes
before that offset, whatever the value of `header_len`. Bytes that a larger
`header_len` adds lie after the CRC field. In an object they are payload
bytes, and the content id covers them. A chunk has no fixed body: its
`header_len` is 64, and a reader refuses a chunk with another value, because
every payload byte of a chunk is file content. The disc superblock and the
run header are files of a fixed size, 2048 and 512 bytes. A writer of format
major 1 never enlarges them, and a later field goes into their reserved
space.

A record inside a structure never carries this header.

Every object file is the common header, then the 32-byte object header of
section 6.1, then the kind body, then variable data. An object payload
carries no magic or version fields of its own; those live once, in the
common header.

Hash and CRC coverage, and reader checks, are stated once per structure in
sections 2.7, 2.8 and 11.1; this section states the shape only.

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
| 0x12 | `sha2-256` | 32 | The only algorithm. Every reader must read it. |

A `hash_algo` field is one byte, so a code in this registry is at most 0xFF.
Two structures hold a `hash_algo` field: the run header (section 7.5) and the
object header (section 6.1). Every digest field of this document is 32 bytes.
A future algorithm whose digest is longer, or whose code is above 0xFF, needs
a new major version.

**Compression registry.**

| Id | Name | Status |
|---:|---|---|
| 0 | none | Mandatory. |
| 1 | zstd | Default. |
| 2-255 | reserved | A reader refuses the object and names the id. |

**Object kind registry.** Section 6.1 repeats it with detail.

| Id | Name |
|---:|---|
| 1 | `chunk` |
| 2 | `blob` |
| 3 | `tree` |
| 4 | `snapshot` |

A ref is a row of the REFS table, section 10.2. It is not an object kind and
carries no id in this registry.

**File role registry.** Section 11.1 states the full table; this is the
registry index. The ids name the roles a Files row in INDEX may hold: the
files of a run.

**FEC scheme registry.**

| Id | Name | Field | Shards | Status |
|---:|---|---|---:|---|
| 0 | `none` | - | - | **Default.** No FEC. A run with this scheme carries no checksum column and no parity files (section 9.6). |
| 1 | `rs255-gf8` | GF(2^8) | 255 | A stripe is `k + 1 + m = 255` blocks; the code is over the `k + m` data and parity shards (section 9.1). |
| 2-255 | reserved | | | |

### 2.6 Version policy

Three version numbers are independent of one another: the document version
that heads this file, the program version of the software that reads and
writes the format, and each structure's own `version_major` in its common
header. A bump to one never implies a bump to another.

Every structure's `version_major` is 1 as this document stands. There is no
minor version. Until the document reaches version 1.0.0 the on-disc format
is not frozen: a structure's layout may change without a version bump, and a
disc burned under a pre-1.0.0 document version carries no compatibility
promise to a later one. Such a disc carries its own `FORMAT.txt` and its own
reference decoder, and those two files describe it exactly. NOTES.md's change
log states what changed at each document version.

The whole compatibility mechanism is this:

1. A reader refuses a structure whose `version_major` it does not know, and
   prints the value.
2. A writer writes zero into reserved space, and a reader ignores reserved
   space (section 2.1, rule 3). A later writer can therefore give meaning to
   reserved space, and a reader of today still reads the structure.
3. A reader obeys `header_len` (section 2.3). A later writer can therefore
   append a field to the fixed part of a structure, and a reader of today
   still finds the variable part.
4. A registry id that a reader does not know, in a field that the reader
   must interpret, is refused by name (section 11.5).
5. Any change that a reader of today would misread is a `version_major`
   bump.

`tool_version` (sections 7.4 and 7.5) is informational only. It never gates
what a reader accepts; a reader never refuses a structure on the strength of
its value.

### 2.7 Hash coverage per structure

Every hash field that names another structure covers all bytes of that
structure as they lie on the medium, with every CRC already filled in. Every
hash of this table is SHA-256, the run header's `hash_algo`.

| Field | In | Covers |
|---|---|---|
| `index_hash` | Run header (section 7.5) | Every byte of `INDEX.bin`. |
| `file_hash` | INDEX Files row (section 10.1) | Every byte of the named file, for the roles that section 10.1 lists. This is what protects `DISC.bin`, `README.txt`, `FORMAT.txt`, `decoder.py`, `REFS.bin` and `DISCS.bin`. |
| `run_hash` | DISCS row (section 10.3) | The 512 bytes of that run's `RUN.bin`, CRC included. |
| `content_id` | Object file name | The uncompressed payload only (section 3.1). Never the header. |
| `content_id` | INDEX Objects row and Prereqs row (section 10.1) | The referenced object, as above. |
| Block digest | Checksum block (section 9.3) | The 2048 bytes of one data block as they lie in the FEC stream. The first 8 bytes of the 32-byte SHA-256 digest. |

The chain of trust of one disc is: the run header CRC proves the run header;
`index_hash` in the run header proves `INDEX.bin`; a `file_hash` in INDEX
proves each fixed-name file; the file name of an object proves its payload.

Host-only structures carry hash fields of their own. None of those bytes
reaches a disc, and the operations document holds their coverage table.

### 2.8 CRC coverage per structure

| Structure | Field | Covers |
|---|---|---|
| Object header (section 6.1) | `header_crc32c` | The common header plus bytes 0 to 23 of the object header, that is bytes 0 to 55 of the file. |
| Blob, tree and snapshot payloads (sections 6.3, 6.4, 6.13) | none | The content id covers the whole payload, records included. |
| Disc superblock (section 7.4) | `super_crc32c` | Bytes 0 to 2043. |
| Run header (section 7.5) | `header_crc32c` | Bytes 0 to 503. |
| Checksum block (section 9.3) | `header_crc32c` | Bytes 0 to 15 of the block. The digests are checked as section 9.3 states. |
| INDEX, REFS, DISCS (sections 10.1 to 11.3) | none | `index_hash` covers INDEX. The `file_hash` of their Files rows covers REFS and DISCS. |

Host-only structures carry CRC fields of their own. None of those bytes
reaches a disc, and the operations document holds their coverage table.

### 2.9 Limits

A writer refuses an input that exceeds a limit of this table and names the
limit. A reader refuses a structure that exceeds one and names the limit.

| Item | Limit | Where it is fixed |
|---|---:|---|
| Digest length | 32 bytes | Section 3.3. A longer digest needs a new major version. |
| Tree entry name | 1 to 4095 bytes | `name_len`, section 6.5. |
| Tree entries per directory | 2^32 - 1 | `entry_count`, section 6.4. |
| Blob entries | 2^64 - 1 | `entry_count`, section 6.3. |
| Tree entry length | 2^32 - 1 bytes | `entry_len`, section 6.5. |
| TLV payload | Always inline. At most `entry_len` minus the 72-byte fixed header minus the TLV's own 8-byte prefix, that is at most `2^32 - 1 - 72 - 8` bytes (4,294,967,215), and far less once the name, the content area and any other TLV of the entry are counted. | Section 6.9. |
| Snapshot metadata TLVs | 65,535 per snapshot | `meta_count`, section 6.13. |
| Ref name | 1 to 40 bytes | Section 6.17. |
| Disc label | 64 bytes, in the superblock and in the DISCS row alike | Sections 7.5 and 11.3. |
| Run sequence number | 1 to 9,999,999,999 | The 10-digit run directory name, section 8.3. |
| Objects per run | 2^32 - 1 | `object_count`, section 10.1, is u32, so this is the limit INDEX can index; a writer refuses to pack a run past it. |
| File size | 2^64 - 1 bytes | Every size field is u64, section 2.1. |
| Disc capacity | 2^64 - 1 sectors of 2048 bytes, a physical medium unit. | Section 7.5. |
| FEC geometry | `k = 231`, `m = 23` | Section 10.1. |
| On-disc object name | 68 characters | Section 3.4. |
| Any on-disc name | 126 characters | Section 8.8. |
| Any on-disc path | under 220 characters | Section 8.8. |

The host limits of the burn plan, the shelf note and the host filesystems
are not in this table. The operations document holds them.

### 2.10 Trust boundaries and safety invariants

- Bytes read from a disc are untrusted until the content id verifies.
- A local index is untrusted. Every cache answer is confirmed against INDEX
  before it is used to drop data.
- Names inside a tree object are untrusted.
- A symlink target is data. A restorer never traverses it.
- The restore safety invariants are host behaviour. The operations document
  states them.
- Encryption, signing, access control and metadata secrecy are not defended in
  version 1.

---
## 3. Identity and hashing

### 3.1 The content id rule

The content id of an object is the hash of one kind byte followed by the
object's uncompressed payload bytes:

```
content_id = SHA-256( kind_byte || payload )
```

The rule is the same for all four kinds: chunk, blob, tree and snapshot.

`kind_byte` is one byte. It holds the object's value of `kind` from the
object kind registry of section 6.1, the same value the object header's
`kind` field holds: 1 for a chunk, 2 for a blob, 3 for a tree, 4 for a
snapshot. The byte is not length-prefixed and no separator follows it.

`payload` is the object's uncompressed payload, exactly as `payload_len`
of the object header counts it: every byte of the object file after the
common header and the object header, decompressed. For a chunk the payload
is the opaque file content bytes. For a blob it is `entry_count` and the
entries. For a tree it is `entry_count`, `reserved_u32` and the entries. For
a snapshot it is the snapshot body and its metadata records. Neither header
enters the id. The chunker parameters, the compression algorithm, a salt and
a key never enter the id.

A reader verifies an object by hashing the kind byte and the payload after
decompression, then comparing the digest with the name. A mismatch is a hard
error. The reader takes the kind byte from the object header's `kind` field,
which the header CRC covers and which must agree with `magic_kind`.

Ids of different kinds never compare equal by construction. Two objects of
different kinds hash different first bytes, so no chunk id equals a blob id,
a tree id or a snapshot id, whatever the payload bytes are. Dedup therefore
compares like with like: a chunk whose bytes equal a blob's payload gets its
own id and its own file.

Worked example. An empty file and an empty directory both have a payload of
eight zero bytes. The empty file's blob holds `entry_count` 0, which is
`00 00 00 00 00 00 00 00`. The empty directory's tree holds `entry_count` 0
and `reserved_u32` 0, which is the same eight bytes. The kind byte separates
them:

| Object | Hashed bytes (hex) | Content id digest (hex) |
|---|---|---|
| Blob of an empty file | `02` `0000000000000000` | `4322fd2bc0a137d1375b37b3b2e2b4715b3d3dd7ca9682438d4fea0f8437fad3` |
| Tree of an empty directory | `03` `0000000000000000` | `dc4c8669df128318c5790c414c870cc76c585268552851e78d3ee8604dbec0e3` |

A chunk of those same eight zero bytes hashes `01` first, so its digest is
`a536aa3cede6ea3c1f3e0357c3c60e0f216a8c89b853df13b29daa8f85065dfb`. The text
forms of the three ids take the `1220` multihash prefix of section 3.4.

### 3.2 Hash algorithms

SHA-256 is the only algorithm of this version. The run header and every
object header state it in `hash_algo`, as the code 0x12.

Digests are never truncated. A digest is 256 bits. The 8-byte digests of the
checksum column are not content ids.

### 3.3 Digest fields in records

A digest field is always 32 bytes and holds one SHA-256 digest. No record
and no table header carries an algorithm field of its own: the run header's
`hash_algo` states the algorithm of every digest on the disc. All zero in a
digest field means "none" where the field's table says so.

### 3.4 Text form

The text form of a content id is the lowercase hex of the multihash bytes:
algorithm code varint, digest length varint, digest bytes. For SHA-256 the
two prefix bytes are 0x12 and 0x20, so the text form starts with `1220`. It
is 68 hex characters over the charset `[0-9a-f]`.

### 3.5 Fan-out on disc

Object files use a hex fan-out over the digest, not over the multihash prefix.
`d0` and `d1` are the first two hex digits of the digest, that is characters
5 and 6 of the text form.

```
/NOAHSARK/objects/<d0><d1>/<full 68-character text form>     chunks, blobs, trees
/NOAHSARK/snapshots/<full 68-character text form>            snapshots
```

There are two object roots: `objects/` for chunks, blobs and trees,
and `snapshots/` for snapshots. A snapshot is split out on its own because a
reader locates one by name through REFS before it holds any other object.

The file name is the full 68-character multihash hex. It is never stripped,
never shortened and never split.

Fan-out is always one level. No field records it.

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

### 4.3 Chunker parameters

Format major 1 has one set of chunker parameters. `max = 4 * avg` and
`min = avg / 4`.

| min | avg | max | Normalization level |
|---:|---:|---:|---:|
| 1 MiB (1,048,576) | 4 MiB (4,194,304) | 16 MiB (16,777,216) | 2 |

No structure on the disc records the chunker parameters, the Gear table or
the masks. A reader never needs them. A reader follows content ids only, and
it restores a file from chunks of any length.

A writer must never change the parameters, the Gear table or the mask
constants under format major 1, because the same bytes must give the same
chunk ids on every disc of a repository. There is no rechunk operation.

### 4.4 Zero regions and sparse files

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

### 4.5 Determinism

The chunker must be deterministic. The same input bytes give the same cut
points on every platform, with any buffer size and any read pattern.

### 4.6 Gear table

The Gear table is defined by this rule:

```
seed = "noahsark/gear/v1"                      # 16 ASCII bytes, no terminator

for i in 0 .. 255:
    input      = seed || u8(i)                 # 17 bytes
    digest     = SHA-256(input)                # 32 bytes
    Gear[i]    = little-endian u64 of digest[0 .. 7]
```

An implementation generates the table exactly once, checks it into the source
tree as a literal array, and never regenerates it from a dependency.

### 4.7 Mask constants

The masks are derived from the average chunk size, not from the Gear table.
They are defined by the normalization level 2 formula of FastCDC, Xia et al.
2020. For an average chunk size of `2^b` bytes:

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

Frozen values:

| avg | b | `mask_s` bits | `mask_s` value | `mask_l` bits | `mask_l` value |
|---:|---:|---:|---|---:|---|
| 4 MiB | 22 | 24 | `0xEEEEEEEE00000000` | 20 | `0xDADADADA00000000` |

The two values follow from the rule. The rule is the authority; the printed
values let a reader check an implementation by eye. The implementation must
compute them once, check them in as literals, and cover them with the golden
vectors of section 12.

---

## 5. Compression

### 5.1 Order of operations

The order is fixed:

1. Chunk the stream.
2. Hash the chunk kind byte and the uncompressed chunk bytes. That digest is
   the content id.
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

**Normative reference.** RFC 8878, "Zstandard Compression and the
'application/zstd' Media Type", describes the frame format. A reader of this
format implements the Zstandard frame of that document: the frame header,
the block structure, and the three block types (raw, RLE and compressed)
with the Literals and Sequences sections of a compressed block. A reader
needs nothing else from that document. It needs no dictionary support,
because a writer of this format uses no dictionary and writes no
`Dictionary_ID`. It needs no skippable frame support, because a writer
writes none. It needs no frame checksum support, because a writer writes
none, though a reader must still skip one if a foreign frame carries it. A
writer emits one frame per payload, so a reader needs no multi-frame
concatenation either.

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
version value (section 7.4). A later encoder may produce different, equally
valid bytes at the same level. This never changes a content id, because the
id is the hash of the kind byte and the uncompressed payload (section 3.1).
It does change
`stored_len`, and therefore the byte offset of every object after it in the
run's FEC stream, so a golden vector that names compressed bytes also names
the `tool_version` that produced them (section 12).

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
| 2 | `blob` | Ordered chunk ids and lengths. | Chunks. | One file. |
| 3 | `tree` | One directory. | Trees and blobs. | One file. |
| 4 | `snapshot` | Root tree, parent, time, text. | Root tree, parent snapshot. | One file. |

A ref is a named pointer to a snapshot. It is a row of the REFS table
(section 10.2), never an object file and never a value of `kind`. A reader
that finds a `kind` value outside 1 to 4 refuses the record and names the
structure.

A chunk carries no reference.

Object header, 32 bytes, at bytes 32 to 63 of the file:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 1 | u8 | `kind` | Object kind registry, this section. |
| 1 | 1 | u8 | `hash_algo` | Multicodec code. 0x12 sha2-256. |
| 2 | 1 | u8 | `reserved_u8` | Zero. |
| 3 | 1 | u8 | `compression` | Compression registry. 0 none, 1 zstd. |
| 4 | 4 | u8[4] | `reserved_a` | Zero. Keeps `payload_len` aligned. |
| 8 | 8 | u64 | `payload_len` | Uncompressed payload length in bytes. |
| 16 | 8 | u64 | `stored_len` | Bytes on the medium after this header. |
| 24 | 4 | u32 | `header_crc32c` | CRC-32C over the common header and bytes 0 to 23 of this header. |
| 28 | 4 | u32 | `reserved_u32` | Zero. |

Total: 32 bytes.

Field rules. `kind` takes a value from the object kind registry, 1 to 4, and
agrees with `magic_kind`. `payload_len` is the uncompressed length of the
payload. The payload is the kind body plus the variable data: every byte of
the object except the two headers. `stored_len` is the number of bytes on the
medium after the common header and the object header, so the file is
`64 + stored_len` bytes long. With `compression` 0, `stored_len` equals
`payload_len`.

Hash and CRC coverage. `header_crc32c` covers the common header, bytes 0 to
31, and bytes 0 to 23 of the object header. The payload is covered by the
content id, which is the hash of the kind byte and the uncompressed payload
and never of either header. The kind byte is the value of `kind` (section
3.1).

Reader checks. Check `magic_project` and `magic_kind`. Check `version_major`.
Read `header_len` (section 2.3). Verify `header_crc32c` before using any
field. Refuse a `hash_algo` other than 0x12 and a `compression` other than 0
or 1, and name the value. Read `stored_len` bytes from offset 64, decompress
them into exactly `payload_len` bytes, hash the value of `kind` as one byte
followed by the result, and compare that digest with the digest in the file
name.

### 6.2 Chunk

Purpose: opaque file content bytes, addressed by the hash of the chunk kind
byte and those bytes.

A chunk object is the common header, the object header, then the payload
bytes. `header_len` is 64.

Reader checks. Verify the header CRC, decompress `stored_len` into
`payload_len` bytes, and verify the content id.

### 6.3 Blob

Purpose: the ordered chunk ids of one file, held outside the tree entry.

Every regular file, small files included, is stored as one blob object. There
is no inline form. The blob holds the file's chunk ids in file order, however
many there are, including zero entries for an empty file.

Blob payload, after the common header and the object header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u64 | `entry_count` | Number of entries. |
| 8 | | | `entries` | `entry_count` records of 40 bytes. |

Fixed body: 8 bytes. `header_len` is 72.

Blob entry, 40 bytes, in file order:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | Chunk id. |
| 32 | 8 | u64 | `length` | Uncompressed bytes of this chunk, that is its `payload_len`. |

Total: 40 bytes.

Field rules. Entries are in file order. An entry stores no file offset. The
offset of entry `n` inside the file is the sum of `length` over entries 0 to
`n - 1`, and entry 0 has offset 0. The file size is the sum of `length` over
all entries, and it equals `size` of the tree entry that names the blob. A
`length` of 0 never appears.

A blob has one level. Every entry names a chunk, and no entry names another
blob. A writer puts every chunk of a file into one blob, however large the
file is: `entry_count` is u64, and a blob has no size limit of its own. With
chunks of 1 MiB to 16 MiB, a file of 1 TiB needs at most about one million
entries, which is a blob of about 40 MiB. An object is never split across
discs, so the blob of a file must fit on one disc.

Reader checks. Verify the content id of the object before using any entry.
Check that `header_len - 64 + entry_count * 40` equals `payload_len`. Check
that the `payload_len` of each chunk equals the `length` of its entry.

### 6.4 Tree

Purpose: one directory, with one entry per child.

Tree payload, after the common header and the object header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `entry_count` | Number of entries. |
| 4 | 4 | u32 | `reserved_u32` | Zero. Keeps `entries` 8-byte aligned. |
| 8 | | | `entries` | Entries, back to back, each self-delimiting. |

Fixed body: 8 bytes. `header_len` is 72.

Field rules. Entries follow back to back, each self-delimiting through its
own `entry_len`. Metadata lives inline in the tree entry, not in a separate
node object.

Tree entries are sorted by raw name bytes, ascending, unsigned. A directory
name compares as if a `/` byte were appended. Sorting is mandatory.

Reader checks. Verify the content id. Validate every entry name at parse time
by section 6.10.

### 6.5 Tree entry fixed header

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `entry_len` | Total entry length including all variable areas. Multiple of 8. |
| 4 | 1 | u8 | `entry_type` | 1 regular, 2 directory, 3 symlink, 4 chardev, 5 blockdev, 6 fifo, 7 socket. 0 is invalid. |
| 5 | 1 | u8 | `entry_flags` | See section 6.6. |
| 6 | 2 | u16 | `name_len` | Name length in bytes, 1 to 4095. No terminator. |
| 8 | 8 | u64 | `size` | Regular files only. 0 otherwise. |
| 16 | 8 | i64 | `mtime_sec` | Seconds since 1970-01-01 UTC. |
| 24 | 8 | i64 | `ctime_sec` | Valid only when `CTIME_ABSENT` is clear. 0 otherwise. |
| 32 | 4 | u32 | `mtime_nsec` | 0 to 999,999,999. |
| 36 | 4 | u32 | `ctime_nsec` | Same range. 0 when `CTIME_ABSENT` is set. |
| 40 | 4 | u32 | `mode` | Low 12 bits are `rwxrwxrwx` plus setuid 04000, setgid 02000, sticky 01000. Bits 12 to 31 reserved, zero. The file type is not here. |
| 44 | 4 | u32 | `uid` | 0xFFFFFFFF means unknown. |
| 48 | 4 | u32 | `gid` | 0xFFFFFFFF means unknown. |
| 52 | 4 | u32 | `rdev_major` | Device entries only, else 0. |
| 56 | 4 | u32 | `rdev_minor` | Device entries only, else 0. |
| 60 | 4 | u32 | `content_len` | Bytes in the content reference area. See section 6.7. |
| 64 | 4 | u32 | `ext_len` | Bytes in the TLV area, padding included. Multiple of 8. |
| 68 | 4 | u32 | `reserved_u32` | Zero. |

Total: 72 bytes.

Field rules. The fixed header is 72 bytes. It stores no length of its own and
no offset of an area: each area sits at the place that section 6.7 derives.
A new per-entry fact goes into `reserved_u32` or into a TLV.

The mandatory fields are `entry_type`, `mode`, `uid`, `gid`, `size`,
`mtime_sec`, `mtime_nsec` and the name. ctime is optional and carries the
`CTIME_ABSENT` flag. Access time and birth time are not stored.

Every `_sec` time field is `i64`, seconds. Every `_nsec` field is `u32`,
nanoseconds in [0, 999999999].

File type is `entry_type`, a u8 enum. `mode` is a u32 holding permission bits
only. The type is not in the mode.

`uid` and `gid` are u32, with `0xFFFFFFFF` meaning unknown.

### 6.6 Entry flags

| Bit | Name | Meaning |
|---:|---|---|
| 0 | reserved | Zero. |
| 1 | reserved | Zero. |
| 2 | `CTIME_ABSENT` | `ctime_sec` and `ctime_nsec` carry no information. A writer sets it when the `metadata.ctime` configuration key is false, and when that key is true and the source reported no ctime. |
| 3 | reserved | Zero. |
| 4 | `SPARSE` | The source file had holes. The restorer punches holes, as section 4.4 states. |
| 5 | `METADATA_PARTIAL` | The source read failed for at least one metadata field. |
| 6 | reserved | Zero. |
| 7 | `UNSTABLE` | The file changed while it was being read, and no earlier consistent version existed. The content is one possible read of a moving file. The operations document states when a writer sets it and what a restore does with it. |

A critical new per-entry fact takes a TLV type in the reserved critical range
0x8000 to 0xBFFF, never a reserved bit of this byte, because a reader ignores
a reserved bit.

ctime storage is conditioned on one configuration key. A writer stores ctime
when `metadata.ctime` is true and the source reports one, and sets
`CTIME_ABSENT` otherwise. The operations document holds the key and its
default.

`UNSTABLE` is set when the file changed while it was being read and no earlier
consistent version existed. A snapshot never holds torn content without a flag
that says so. When the parent snapshot holds an entry for the path, the writer
reuses that entry and discards the new chunk list; otherwise it keeps the new
content and sets `UNSTABLE`.

### 6.7 Variable areas and the content area

The areas follow the 72-byte fixed header in a fixed order. Every offset
below is relative to the first byte of the entry. `align8(x)` is `x` rounded
up to a multiple of 8.

| Area | Start | Length | Content |
|---|---|---|---|
| Name | 72 | `name_len` | Raw bytes of exactly one path component. |
| Content refs | `content_off = align8(72 + name_len)` | `content_len` | See below. |
| Extension TLVs | `ext_off = align8(content_off + content_len)` | `ext_len` | TLV records, sorted. |

`entry_len = align8(ext_off + ext_len)`. Every padding byte is zero. A reader
derives the three places from `name_len`, `content_len` and `ext_len`, and
refuses an entry whose stored `entry_len` differs from the derived value. An
area of length 0 occupies no bytes.

Content area by entry type:

| `entry_type` | `content_len` | Content |
|---|---|---|
| 1 regular | 32 | One blob id. The blob holds the file's chunk ids, section 6.3, with zero entries for an empty file. There is no inline form. |
| 2 directory | 32 | One tree id. |
| 3 symlink | 0 | The target is TLV `SYMLINK_TARGET`. |
| 4, 5, 6, 7 | 0 | No content. |

A reader refuses an entry whose `content_len` differs from this table.

Worked example. A regular file named `a.txt` (5 bytes) with no TLV:
the name is bytes 72 to 76, bytes 77 to 79 are zero, `content_off` is 80,
the blob id is bytes 80 to 111, `ext_off` is 112, `ext_len` is 0 and
`entry_len` is 112.

### 6.8 Extension TLV record

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 2 | u16 | `tlv_type` | Registry, section 6.9. |
| 2 | 2 | u16 | `tlv_flags` | bit0 `CRITICAL`. Bits 1 to 15 reserved, zero. |
| 4 | 4 | u32 | `tlv_len` | Payload bytes, excluding this prefix and excluding padding. |
| 8 | `tlv_len` | u8[] | `payload` | The value, always inline. |
| | pad | u8[] | | Zero bytes to the next 8-byte boundary. |

One record is `align8(8 + tlv_len)` bytes. `ext_len` is the sum over the
records of the entry.

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
`entry_len` minus the 72-byte fixed header minus this record's own 8-byte
prefix, which is at most `2^32 - 1 - 72 - 8`, that is 4,294,967,215 bytes,
and in practice far less once the name, the content area and any other TLV
of the same entry are counted.

### 6.9 TLV type registry

| Type | Name | Critical | Payload |
|---:|---|---|---|
| 0x0001 | `SYMLINK_TARGET` | yes | Raw bytes. Mandatory when `entry_type` is 3. Never validated as UTF-8. |
| 0x0002 | `USER_NAME` | no | UTF-8 bytes. |
| 0x0003 | `GROUP_NAME` | no | UTF-8 bytes. |
| 0x0004-0x7FFF | unassigned | no | A reader applies the unknown-TLV rule of section 6.8. |
| 0x8000-0xBFFF | reserved critical | yes | Future critical extensions. |
| 0xF000-0xFFFF | vendor | no | Never critical. |

This version stores Unix permissions only: `mode`, `uid` and `gid` in the
tree entry fixed header. It stores no extended attribute, no access control
list and no file flag.

`SYMLINK_TARGET` is mandatory when `entry_type` is 3 and is never validated as
UTF-8.

### 6.10 Name validation

A tree entry name is exactly one path component. The parser must reject at
parse time a name that is empty, that is `.` or `..`, or that contains a `/`, a
`\` or a NUL byte.

A name is not required to be valid UTF-8.

### 6.11 What is never stored

Inode numbers, source filesystem device ids, link counts, access times and
birth times are never stored.

### 6.12 Hardlinks

Every hardlinked path is stored as an independent tree entry. Content dedup
already stores the shared data once, so each entry carries its own full
content reference. No field groups the entries.

Restore does not recreate the source's hardlinks. Each stored entry is
restored as its own file, with its own inode. Link identity is not preserved.

### 6.13 Snapshot

Purpose: one root tree pointer, a parent pointer, the snapshot time and the
snapshot's own metadata.

Snapshot payload, after the common header and the object header:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `root_tree` | Content id of the root tree. |
| 32 | 32 | u8[32] | `parent` | Content id of the parent snapshot. All zero when the snapshot has no parent. |
| 64 | 8 | u64 | `reserved_u64a` | Zero. |
| 72 | 8 | i64 | `time_sec` | Snapshot time, seconds since 1970-01-01 UTC. |
| 80 | 4 | u32 | `time_nsec` | Nanoseconds. |
| 84 | 4 | i32 | `tz_offset_sec` | Local zone offset at snapshot time, seconds east of UTC. |
| 88 | 8 | u64 | `total_size` | Sum of `payload_len` over the distinct chunks, blobs and trees reachable from `root_tree`, the root tree included. A chunk that several files share is counted once. It is not the sum of the file sizes. For planning. |
| 96 | 8 | u64 | `reserved_u64b` | Zero. |
| 104 | 2 | u16 | `reserved_u16` | Zero. |
| 106 | 2 | u16 | `meta_count` | Number of TLV records that follow. |
| 108 | 4 | u32 | `reserved_u32` | Zero. |
| 112 | | | TLV records | `meta_count` records follow. |

Fixed body: 112 bytes. `header_len` is 176.

Field rules. `parent` is all zero when the snapshot has no parent. A reader
uses `parent` to show history only. It never needs the parent to restore a
snapshot.

Metadata TLV record:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 2 | u16 | `tag` | Snapshot metadata tag registry, below. |
| 2 | 2 | u16 | `flags` | bit0 CRITICAL. Bits 1 to 15 reserved, zero. |
| 4 | 4 | u32 | `len` | Payload bytes. |
| 8 | `len` | u8[] | `value` | The value, as the registry states. |
| | pad | | | Zero to the next 4-byte boundary. |

Records are in ascending `tag` order. Records with the same tag keep the
order that the registry states.

The snapshot metadata tag registry:

| Tag | Name | Value | Repeats |
|---:|---|---|---|
| 1 | author | UTF-8. | No. |
| 2 | host | UTF-8 host name. | No. |
| 3 | message | UTF-8, from `commit -m`. | No. |
| 4 | reserved | Never written. The source root path is the name of the root tree entry, section 6.14. | |
| 5 | exclude rules | Exclude pattern lines, one per line, in rule order, for one source root. The pattern language is host behaviour; the operations document states it. A reader never interprets the value. | One per root, in the entry order of the root tree. |
| 6 | checksum commit | Empty. Present when the commit rehashed every file. | No. |
| 7 to 0x7FFF | reserved | | |
| 0x8000 to 0xBFFF | reserved critical | A reader refuses an unknown tag in this range. | |
| 0xF000 to 0xFFFF | vendor | Never critical. | May repeat. |

Tag 5 holds exactly two of the three rule sources of the exclude pattern
language: every configured exclude key in configuration order, then every command-line exclude
option in command-line order. An ignore-file rule is never recorded. Each rule
is written as its raw pattern bytes with no normalization and no UTF-8
validation, each followed by one LF byte, 0x0A. The last rule carries its LF
too. A root with no rule gives `len` 0. A snapshot holds either no tag 5
record or exactly one per root.

Reader checks. Verify the content id. Refuse an unknown metadata tag in the
critical range 0x8000 to 0xBFFF.

### 6.14 The root tree

A snapshot has exactly one synthetic `root_tree`. It is a tree object like any
other, and it holds one entry per source root, in the tree entry order of
section 6.4.

A root entry has `entry_type` 2, directory, and its content area holds the tree
id of the root's own directory. The entry's mode, uid, gid and times are those
of the root directory itself.

The source root path is stored one time: as the name of the root entry. The
name is the root's absolute path, encoded so that it is one path component
that section 6.10 accepts. No TLV and no snapshot metadata record repeats the
path.

**The escape rule.** The writer goes over the path bytes one by one. Exactly
four byte values are escaped and no others:

| Path byte | Written as | Bytes written |
|---|---|---|
| `/` (0x2F) | `%2F` | 0x25 0x32 0x46 |
| `\` (0x5C) | `%5C` | 0x25 0x35 0x43 |
| NUL (0x00) | `%00` | 0x25 0x30 0x30 |
| `%` (0x25) | `%25` | 0x25 0x32 0x35 |

The hex digits are uppercase. Every other byte, `.` included, is kept as it
is. The path is not normalized in any other way, and a trailing `/` of the
path, if the writer was given one, is escaped like any other `/`.

To decode, a reader goes over the name bytes one by one and replaces every
`%` and the two hexadecimal digits after it by the one byte that the digits
name. A reader accepts a lowercase digit. When a `%` is not followed by two
hexadecimal digits, the reader uses the whole name undecoded and reports it.

Example 1. The path `/srv/data`:

```
path bytes (9):   2F 73 72 76 2F 64 61 74 61
name       (13):  %2Fsrv%2Fdata
name bytes (13):  25 32 46 73 72 76 25 32 46 64 61 74 61
```

Example 2. The path `/a%b/c`:

```
path bytes (6):   2F 61 25 62 2F 63
name       (12):  %2Fa%25b%2Fc
name bytes (12):  25 32 46 61 25 32 35 62 25 32 46 63
```

A root whose encoded name would be empty, `.` or `..` is refused at commit
time. An encoded name above 4095 bytes is refused.

### 6.15 Ref

Purpose: a named pointer to a snapshot, stored as a row in the REFS table
(section 10.2), never as a separate object file.

Ref record, 88 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `snapshot_id` | Content id of the snapshot. |
| 32 | 8 | i64 | `time_sec` | When the ref took this value, seconds since 1970-01-01 UTC. |
| 40 | 4 | u32 | `time_nsec` | Nanoseconds, 0 to 999,999,999. |
| 44 | 2 | u16 | `name_len` | Byte length of the name, 1 to 40. |
| 46 | 2 | u16 | `reserved_u16` | Zero. |
| 48 | 40 | u8[40] | `name` | UTF-8, zero-padded. No NUL byte inside the name. A name above 40 bytes is refused at commit time. The limit is part of the format. |

Total: 88 bytes.

Field rules. A record stores no run number. The table is sorted by these four
keys, in this order:

1. the `name_len` bytes of `name`, ascending, as unsigned bytes (section
   6.16);
2. `time_sec`, ascending, as a signed number;
3. `time_nsec`, ascending;
4. the 32 bytes of `snapshot_id`, ascending, as unsigned bytes.

The four keys are total: a writer never writes two records that share all
four. When it merges the records of earlier runs with its own, it keeps one
copy of two equal records. The table only grows from one run to the next.

**The newest ref.** For one name, the newest record is the one with the
highest `time_sec`; among those, the one with the highest `time_nsec`; among
those, the one whose `snapshot_id` bytes are the highest, compared as
unsigned bytes. This is the last record of that name in table order. A
reader takes the newest record as the value of the name.

No ref name is reserved. A reader that wants the newest state of the
repository takes the record with the highest time over all names, by the rule
above. To pick an older state, it takes a record by its name and time.

### 6.16 Canonical ordering

Two identical directories must serialize to identical bytes. The ordering table
below is mandatory.

| Structure | Order key |
|---|---|
| Tree entries | Raw name bytes, ascending, directories compared with a trailing `/`. |
| Tree entry variable areas | Name, then content refs, then TLVs, each 8-byte aligned, then zero padding to `entry_len` (section 6.7). |
| TLV records | `tlv_type` ascending, then payload bytes ascending. |
| Blob entries | File order. |
| INDEX Objects rows and Prereqs rows | Content id ascending. |
| Ref records | Section 6.17. |
| DISCS rows | `run_seq` ascending, then `disc_uuid` bytes ascending. |

Every "ascending" in this table, and everywhere else in this document, is an
unsigned bytewise comparison: the first differing byte decides, and a
shorter string that is a prefix of a longer one sorts first. The one
exception is a numeric field, which compares as a number.

---
## 7. Disc and run model

### 7.1 The physical disc

A physical disc has a `disc_uuid` of 16 bytes, generated once and never reused;
a human label; a `disc_seq`; and a capacity in sectors.

`disc_seq` is 0-based and monotonic inside the repository. `run_seq` is 1-based
and monotonic inside the repository.

A `disc_seq` is consumed before anything is built, and it is never given to
another disc. A hole in the sequence is harmless.

The `disc_uuid` is the identity of a disc. `run_seq` and `disc_seq` are
counters of one repository state. When a repository is rebuilt after a loss,
a new disc can receive a `run_seq` that a lost disc already carries. Every
reference from one disc to another therefore names the `disc_uuid`, never the
`run_seq` (section 10.1, the Prereqs table).

One disc holds one run. A writer writes the whole `/NOAHSARK/` tree in one
burn and never adds to it.

### 7.2 The run and the FEC terms

A run is one execution of one burn plan. It is the unit of packing, of the
object index, of the Reed-Solomon parity and of the catalog copy.

A run is self-contained: it carries its own header in two files, its own
INDEX, and, when `fec_scheme` is 1, its own parity.

**FEC terms.** This table is a complete forward definition of every term that
sections 7.4 to 7.8 use. Section 10.1 repeats it with the burst bound and the
encoding order.

| Term | Meaning |
|---|---|
| `k`, `m` | The data column count and the parity column count. Version 1 fixes `k = 231` and `m = 23`, so `k + 1 + m = 255`. |
| Stream file | A file whose Files row in INDEX has a role inside the FEC stream (section 10.1, the file role registry). `RUN.bin`, `RUN2.bin`, `checksum.bin` and every parity file are not stream files. |
| FEC stream | The stream files, in the order of their Files rows in INDEX, each file's bytes padded with zero bytes up to a multiple of 2048, concatenated in that order. `INDEX.bin` is the first file of the stream. An empty file adds no block. |
| `stream_bytes`, `stream_blocks` | The byte length of the FEC stream, padding included, so a multiple of 2048; and that length divided by 2048: the number of 2048-byte blocks in the stream. |
| `L`, `column_blocks` | The number of blocks in one column. `L = ceil(stream_blocks / k)`. |
| Column | A range of `L` blocks of the FEC stream. Data column `c` is blocks `[c*L, (c+1)*L)` of the stream, for `c = 0 .. k-1`. A block at or past `stream_blocks` is 2048 zero bytes. Column `k`, the checksum column, is the `L` blocks of `runs/<seq>/checksum.bin`. Columns `k+1` to 254 are the `m` parity columns, each the `L` blocks of one `parity/pNNNN.bin` file. |
| Stripe | Block `i` of every column, for `i = 0 .. L-1`. A stripe is 255 blocks: `k` data, 1 checksum, `m` parity. The code covers the `k` data and the `m` parity blocks; the checksum block is outside the code (section 9.3). |

A reader finds the first stream block of a stream file this way: go over the
Files rows in order, skip the rows that are not stream files, and add
`ceil(byte_len / 2048)` for each stream file before the wanted one.

The FEC stream has no relationship to where its bytes physically sit on the
medium. A block index is a position inside the stream, never a medium address.

### 7.3 What the parity does and does not cover

The parity covers exactly the bytes of the FEC stream: the run's stream
files, in INDEX's file order, zero-padded per file to a 2048 boundary.
Filesystem metadata, such as a UDF directory record or File Entry block, is
never part of the stream and is never covered by the parity. The parity
therefore does not depend on where the filesystem places a file, or on
whether UDF embeds a small file inside its File Entry.

### 7.4 Disc superblock

Purpose: the immutable facts of one physical disc, written as
`/NOAHSARK/DISC.bin`.

The superblock is 2048 bytes, exactly one sector. It is the ordinary file
`/NOAHSARK/DISC.bin`.

Fixed body, after the 32-byte common header (`magic_kind` `DISC`):

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 32 | 16 | u8[16] | `disc_uuid` | Unique for this physical disc. |
| 48 | 16 | u8[16] | `repo_uuid` | The repository this disc belongs to. |
| 64 | 8 | u64 | `disc_seq` | Monotonic position in the repository, 0-based. |
| 72 | 8 | u64 | `capacity_sectors` | The capacity that the writer packed this disc for, in sectors of 2048 bytes. |
| 80 | 40 | u8[40] | `reserved_a` | Zero. |
| 120 | 8 | i64 | `created_sec` | Pack time of the run, seconds since 1970-01-01 UTC: the moment `pack` finalized the image. Not the burn time, which is unknown when these bytes are hashed (section 7.5). |
| 128 | 4 | u32 | `created_nsec` | Nanoseconds. |
| 132 | 4 | i32 | `tz_offset_sec` | Local zone offset at that pack time, seconds east of UTC. |
| 136 | 8 | u8[8] | `reserved_b` | Zero. Keeps `label_len` aligned. |
| 144 | 4 | u32 | `label_len` | Byte length of the label, 0 to 64. |
| 148 | 64 | u8[64] | `label` | UTF-8, zero-padded. |
| 212 | 4 | u32 | `tool_version` | Writer registry id in the high 8 bits, writer-defined version in the low 24 bits. Informational only; never gates reading (section 2.6). |
| 216 | 1828 | u8[1828] | `reserved_c` | Zero. |
| 2044 | 4 | u32 | `super_crc32c` | CRC-32C over bytes 0 to 2043. |

Total: 2048 bytes. `header_len` is 2048.

Field rules. The superblock holds only facts of the disc. Every per-run
parameter, that is the hash algorithm and the FEC scheme and geometry, lives
in the run header (section 7.5) only; the superblock never repeats it.

`capacity_sectors` is the one capacity field. It is the limit that the run
was packed for. The capacity that a drive reports for the medium is not
stored.

`tool_version` is a u32. Its high 8 bits are a writer registry id, and its low
24 bits are a version value that the named writer defines for itself. Registry
id 1 is the reference implementation. Registry id 0 is invalid. Ids 2 to 255
are assigned once, on request, and are never reused. A reader never interprets
the low 24 bits of a writer it does not know; it prints the pair. The run
header records the same value (section 7.5).

The superblock needs no second copy. It lies inside the FEC stream, and the
run header carries the disc uuid, the repository uuid and the disc sequence.

Hash and CRC coverage. `super_crc32c` covers bytes 0 to 2043. The `file_hash`
of the role 3 Files row in INDEX covers all 2048 bytes.

Reader checks. Check `magic_project`, `magic_kind` and `version_major`.
Verify `super_crc32c` before using any field. Check that `disc_uuid`,
`repo_uuid` and `disc_seq` equal those of the run header.

### 7.5 Run header

Purpose: the identity, geometry and pointers of one run. It is the root of
trust of the disc.

The run header is 512 bytes: the 32-byte common header (`magic_kind` `RUN`)
plus a 480-byte fixed body. It is written two times, as `RUN.bin` and as
`RUN2.bin`. **Each of the two files is exactly 512 bytes long.** The two
files are byte-identical.

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 32 | 16 | u8[16] | `disc_uuid` | The disc this run sits on. |
| 48 | 16 | u8[16] | `repo_uuid` | The repository. |
| 64 | 8 | u64 | `run_seq` | Run number in the repository, 1-based. It equals `<seq>` of the run directory. |
| 72 | 8 | u64 | `disc_seq` | Disc sequence number, 0-based. |
| 80 | 2 | u16 | `fec_k` | Data columns. 231 when `fec_scheme` is 1, 0 when it is 0. |
| 82 | 2 | u16 | `fec_m` | Parity columns. 23 when `fec_scheme` is 1, 0 when it is 0. |
| 84 | 1 | u8 | `fec_scheme` | FEC scheme registry. |
| 85 | 1 | u8 | `hash_algo` | Multicodec code of every digest on this disc. 0x12. |
| 86 | 10 | u8[10] | `reserved_a` | Zero. Keeps `index_bytes` aligned. |
| 96 | 8 | u64 | `index_bytes` | Byte length of `INDEX.bin`. |
| 104 | 32 | u8[32] | `index_hash` | Hash of `INDEX.bin`'s bytes. |
| 136 | 8 | u64 | `stream_bytes` | Byte length of the FEC stream, section 7.2. A multiple of 2048. |
| 144 | 32 | u8[32] | `reserved_b` | Zero. |
| 176 | 8 | i64 | `created_sec` | Pack time, seconds since 1970-01-01 UTC: the moment `pack` finalized this run's image. Never the burn time, which is unknown when these bytes are hashed; the actual burn time lives only in the local state log; see the operations document. |
| 184 | 4 | u32 | `created_nsec` | Nanoseconds. |
| 188 | 4 | u32 | `tool_version` | Writer registry id in the high 8 bits, writer-defined version in the low 24 bits, as in the superblock (section 7.4). Informational only. |
| 192 | 312 | u8[312] | `reserved_c` | Zero. |
| 504 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 503. |
| 508 | 4 | u8[4] | `reserved_final` | Zero. |

Total: 512 bytes. `header_len` is 512.

Field rules. `created_sec` is pack time, never burn time. It equals
`created_sec` of the superblock.

`fec_k` and `fec_m` are 231 and 23 when `fec_scheme` is 1. Both are 0 when
`fec_scheme` is 0, and a reader then derives no geometry. A reader refuses a
`fec_scheme` 1 run with any other pair for repair, and still reads its
objects.

The run header records no chunker parameter and no compression default. A
reader needs neither: every object header states its own compression.

A reader derives the FEC geometry from `stream_bytes`, `fec_k` and `fec_m`:
`stream_blocks` is `stream_bytes` divided by 2048, and `L`, section 7.2, is
`stream_blocks` divided by `fec_k`, rounded up. Neither `stream_blocks` nor
`L` is stored. `checksum.bin` and every parity file are `L * 2048` bytes
long.

`index_bytes` and `index_hash` let a reader verify `INDEX.bin` before it
uses any row. `INDEX.bin` is always the first file of the FEC stream
(section 7.2).

Reader checks. Check `magic_project`, `magic_kind` and `version_major`.
Verify `header_crc32c`. Refuse a `hash_algo` other than 0x12 and name the
value. Verify `index_hash` against the bytes of `INDEX.bin`.

### 7.6 Run header copies

The run header exists two times: `RUN.bin` and `RUN2.bin`. Both files are
512 bytes and byte-identical. Both are outside the FEC stream, because the
header holds `index_hash` and `stream_bytes`, which are final only after the
stream is. Two copies exist so that one survives damage to the other. A
writer lays `RUN.bin` near the start of the run and `RUN2.bin` at its end
(section 8.7).

A reader reads `RUN.bin`. When that file is unreadable or its checks fail,
the reader reads `RUN2.bin` and applies the same checks.

---
## 8. Filesystem and the volume tree

### 8.1 The UDF volume

The filesystem of a disc is pure UDF at revision 2.01, block size 2048. There
is no ISO 9660 bridge, no Joliet and no Rock Ridge. The format stores no
filesystem field; the UDF volume itself states its revision.

The image is built with
`mkudffs`, and these options are normative: `--media-type=hd`,
`--blocksize=2048`, `--udfrev=2.01`, `--uid=0`, `--gid=0`, `--mode=0555`,
`--bootarea=erase`, and no sparing table, that is no `--spartable`. The label
comes from the writer. A sparing table is forbidden because it adds a second
logical-to-physical indirection.

The image length is `capacity_sectors` (section 7.4) rounded down to a
multiple of 16 sectors.

`mkudffs` places the UDF anchors at fixed, standard-mandated positions of the
image. NoahsArk never writes an anchor itself.

How much of the image a burner sends to the medium is host behaviour. The
operations document states it. The mandatory anchor near the start of the
volume is part of the used prefix of the image, so a disc that received the
used prefix only still mounts.

The exact UDF metadata bytes depend on the `mkudffs` version. Two writers that
hold every rule above still differ inside those bytes when their `mkudffs`
versions differ. The run header records the writer in `tool_version`
(section 7.5). A golden vector therefore covers the files
NoahsArk itself writes and the parity computed over the actual FEC stream,
never the UDF metadata bytes.

UDF may embed the data of a small file inside its File Entry block. The
format allows that. No rule of this document depends on the medium address of
a file, a writer adds no padding to prevent the embedding, and a reader reads
every file by name through the filesystem. A disc whose filesystem does not
mount counts as lost: this format defines no recovery from the raw medium.

### 8.2 Files at the volume root

Every byte that NoahsArk writes is an ordinary file.

| Path | Content | FEC stream |
|---|---|---|
| `/NOAHSARK/DISC.bin` | Disc superblock (section 7.4). | Inside. |
| `/NOAHSARK/README.txt` | Plain-text explanation of the format for a human (section 8.4). | Inside. |
| `/NOAHSARK/FORMAT.txt` | This document (section 8.5). | Inside. |
| `/NOAHSARK/REFERENCE/decoder.py` | A standalone Python 3 reference decoder (section 8.6). | Inside. |
| `/NOAHSARK/runs/<seq>/RUN.bin` | The run header, 512 bytes. | Outside. |
| `/NOAHSARK/runs/<seq>/INDEX.bin` | File order, the Objects table, the Prereqs table (section 10.1). | Inside, the first file. |
| `/NOAHSARK/runs/<seq>/catalog/REFS.bin` | The ref table (section 10.2). | Inside. |
| `/NOAHSARK/runs/<seq>/catalog/DISCS.bin` | The disc directory table (section 10.3). | Inside. |
| `/NOAHSARK/runs/<seq>/checksum.bin` | The checksum column, `L` blocks. Only when `fec_scheme` is 1. | Outside. |
| `/NOAHSARK/runs/<seq>/parity/pNNNN.bin` | One file per parity column, `L` blocks. Only when `fec_scheme` is 1. | Outside. |
| `/NOAHSARK/runs/<seq>/RUN2.bin` | Run header copy, 512 bytes. | Outside. |
| `/NOAHSARK/objects/<ab>/<name>` | Chunks, blobs and trees. | Inside, in the order of INDEX. |
| `/NOAHSARK/snapshots/<name>` | Snapshot objects: every snapshot of the repository (section 10.4). | Inside, in the order of INDEX. |

Every fixed name is short, uses the charset `[A-Za-z0-9._/-]`, and avoids every
Windows reserved name.

### 8.3 Run directory naming

`<seq>` is the run sequence number, zero-padded to 10 decimal digits. A disc
holds exactly one run directory. A reader lists `/NOAHSARK/runs/` and takes
it.

### 8.4 README.txt

Purpose: the plain-text explanation that tells a reader with no NoahsArk
software what the disc is and where the full format definition lies.

`README.txt` is the exact text below. It is plain ASCII, with LF line endings
and exactly one LF at the end of the file. No line has a trailing space. The
text is byte-identical on every disc written under this document version,
apart from its substitution slots. `README.txt` is at most 16 KiB.

A slot is written `{name}` below. The writer replaces the bytes `{`, the name
and `}` with the value, and writes nothing else in its place. **Every slot in
the text is substituted, wherever it appears.** The substitution rules are:

| Slot | Value |
|---|---|
| `{repo_uuid}`, `{disc_uuid}` | Hyphenated lowercase uuid text. |
| `{disc_seq}` | `disc_seq` in decimal, 0-based. |
| `{label}` | The `label` bytes of the superblock, as they are, with every byte outside 0x20 to 0x7E replaced by `?`. |
| `{hash_algo}` | The constant `sha2-256`, the name of the `hash_algo` in the run header. |
| `{created}` | `created_sec` and `tz_offset_sec` of the superblock, as `YYYY-MM-DDTHH:MM:SS+HH:MM`, with `-` in place of `+` for a negative offset. |
| `{fec_k}`, `{fec_m}` | 231 and 23, in decimal. The two slots appear inside `{parity_repair}` alone, so a run with `fec_scheme` 0 never writes them. |
| `{parity_identity}` | With `fec_scheme` 1, the text `parity geometry: k={fec_k} data columns, m={fec_m} parity columns`. With `fec_scheme` 0, the text `parity: none`. |
| `{parity_files}` | With `fec_scheme` 1, the two lines that name `checksum.bin` and `parity/`, which follow the text. With `fec_scheme` 0, the empty value. |
| `{parity_repair}` | The repair paragraph of part 7, in the form for the run's `fec_scheme`. Both forms follow the text. |

The text:

```
NoahsArk backup disc
====================

1. WHAT THIS DISC IS
--------------------
This disc holds part of a NoahsArk backup repository. The on-disc format is
major version 1. Everything that NoahsArk wrote is an ordinary file under the
directory /NOAHSARK/. There are no hidden sectors and no raw areas outside
this filesystem. Every structure starts with the 8-byte text "NOAHSARK" and
an 8-byte kind name, and is little-endian and packed.

2. IDENTITY
-----------
repository uuid: {repo_uuid}
disc uuid: {disc_uuid}
disc sequence: {disc_seq}
label: {label}
hash algorithm: {hash_algo}
pack time: {created}
{parity_identity}

3. HOW TO FIND THINGS
---------------------
/NOAHSARK/DISC.bin              disc superblock
/NOAHSARK/README.txt            this file
/NOAHSARK/FORMAT.txt            the full definition of the format
/NOAHSARK/REFERENCE/decoder.py  a standalone Python 3 reference decoder
/NOAHSARK/runs/<seq>/RUN.bin    run header, 512 bytes
/NOAHSARK/runs/<seq>/INDEX.bin  file order and the object table
/NOAHSARK/runs/<seq>/catalog/   REFS.bin and DISCS.bin
{parity_files}
/NOAHSARK/runs/<seq>/RUN2.bin   run header copy
/NOAHSARK/objects/<ab>/<name>   chunks, blobs, trees
/NOAHSARK/snapshots/<name>      snapshot objects

<seq> is the run number, ten decimal digits, zero padded. A disc holds one
run directory.

4. HOW AN OBJECT IS NAMED
-------------------------
The name of an object is the hash of one kind byte and then the object's
uncompressed payload bytes. Nothing else enters the name. The kind byte is 1
for a chunk, 2 for a blob, 3 for a tree and 4 for a snapshot, the same value
the object header holds at its offset 0. Two objects of different kinds never
share a name, even when their payload bytes are equal: an empty file and an
empty directory are the common case. The compression and the header bytes do
not enter the name. The name on disc is the lowercase hex of the multihash:
two prefix bytes, then the digest. 1220 means SHA-256, the prefix on every
object of this disc, so the name is 68 hex characters. <ab> is the first two
hex characters of the digest, which is characters 5 and 6 of the file name.

5. HOW TO READ AN OBJECT
------------------------
An object file starts with a 32-byte common header, then a 32-byte object
header. In the object header, at byte offset 0 of that 32-byte header, is
the kind byte. At offset 3 is the compression id: 0 means none and 1 means
zstd. At offset 8 is payload_len, a little-endian unsigned 64-bit number. At
offset 16 is stored_len. Skip the 64 header bytes total, take the next
stored_len bytes, decompress them with the named algorithm into exactly
payload_len bytes, hash the kind byte and then the result with SHA-256, and
compare that digest with the digest in the name. They must be equal. If they
are not, the bytes are damaged; see part 7.

6. HOW TO WALK A SNAPSHOT
-------------------------
Read catalog/REFS.bin, which is a table of named pointers, and take the
record with the highest time. To pick an older state, take a record by its
name and time. The record gives a snapshot id. Read that snapshot object.
Its body names a root tree id.
Read that tree object: it is a list of directory entries, each with a name,
the POSIX metadata, and either a tree id for a subdirectory or a blob id for
a file. Read the blob object: it holds the ordered chunk ids of that file.
Concatenate the chunk payloads in order and the file is restored. An object
that is not on this disc is on another disc of the repository; INDEX.bin
names that disc by its uuid, and catalog/DISCS.bin gives its label.

7. HOW TO REPAIR
----------------
{parity_repair}

8. WHERE THE BYTE LAYOUTS ARE
-----------------------------
FORMAT.txt in this directory is the full format document. It holds the
offset, size, type, name and meaning of every field of every structure, the
registries, the magic values, the chunking constants and the Reed-Solomon
definition. It is enough to extract every file from this disc, and to repair
a damaged disc that has parity, with no NoahsArk software.
REFERENCE/decoder.py in this directory is a runnable Python 3 program that
does the extraction in code: it parses DISC.bin, RUN.bin, INDEX.bin and
every object header, verifies content ids, walks a snapshot and prints the
listing. It verifies and it restores. It does not repair: the forward error
correction part of FORMAT.txt is the full recipe for a repair. Its restore,
verify and list commands take more than one disc root: mount every disc of
the repository and name each root on the one command line, because a
snapshot can span several discs.

9. THE FORMAT RULES
--------------------
1. Every integer is little-endian. No big-endian field exists.
2. Every type is fixed width: u8, u16, u32, u64, i32, i64.
3. Every structure is packed, and every gap is a named reserved field. A
   writer writes zero there, and a reader ignores it.
4. Every structure starts with an 8-byte project magic, an 8-byte kind name,
   then version_major and header_len.
5. A reader refuses an unknown version_major. There is no minor version.
6. header_len is the offset of the first byte after the fixed part of a
   structure. A reader obeys it and skips fixed bytes it does not know.
7. Checksums are CRC-32C, polynomial 0x1EDC6F41, reflected, init
   0xFFFFFFFF, final xor 0xFFFFFFFF. A checksum lies after the bytes it
   covers.
8. Every pointer to another structure carries the hash of that structure.
9. A string is encoding, three zero bytes, a 32-bit length, then the bytes.
   Encoding 0 is UTF-8. There is no terminator and no normalization.
10. Every structure has a byte-offset table, and FORMAT.txt holds it.
```

A slot that stands alone on a line takes the empty value when its rule says
so. The writer then removes that line, its LF included, and writes no blank
line in its place. `{parity_files}` is the only such slot.

The value of `{parity_files}` under `fec_scheme` 1 is these two lines, the
second with no LF after it, because the slot's own line ends the value:

```
/NOAHSARK/runs/<seq>/checksum.bin   per-block digests of the data blocks
/NOAHSARK/runs/<seq>/parity/        one file per parity column
```

The value of `{parity_repair}` under `fec_scheme` 1:

```
The run carries Reed-Solomon parity over its own data files, concatenated in
the order INDEX.bin lists them, each padded to 2048 bytes: the FEC stream.
The stream is cut into {fec_k} equal columns of L blocks of 2048 bytes each;
FORMAT.txt says how to derive L from the run header. Stripe i is block i of
every column. checksum.bin is one more column: its block i holds an 8-byte
digest of each of the {fec_k} data blocks of stripe i, so a damaged block can
be found. The {fec_m} files under parity/ are the parity columns. Any
{fec_k} of the {fec_k} data plus {fec_m} parity blocks of one stripe
reconstruct the rest. The forward error correction part of FORMAT.txt gives
the field arithmetic, the matrix and a worked example. It is the full recipe
for a repair.
```

The value of `{parity_repair}` under `fec_scheme` 0:

```
This disc carries no parity: the run directory holds no checksum.bin and no
parity/ directory. A damaged byte on this disc cannot be repaired from this
disc. Read the object from the second copy of this disc, or from another
disc of the repository that holds the same object.
```

### 8.5 FORMAT.txt

`/NOAHSARK/FORMAT.txt` is this document, byte for byte: the file `FORMAT.md`
of the NoahsArk source tree at the version that built the writer. A writer
embeds that file and writes its bytes and no others. There is no second,
shorter text. A test compares the embedded bytes with `FORMAT.md` and fails
on any difference.

`FORMAT.txt` is plain ASCII with LF line endings. It has no size limit of its
own. Its INDEX file role is 5 (section 10.1), and the `file_hash` of that
row covers it.

The file is Markdown. A reader needs no Markdown software: a table row is one
line with `|` between the columns, and a code block lies between two lines
of three backquotes.

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
it prints the resulting listing; and it restores a snapshot into a
directory. It does not repair a damaged disc: section 9 is the recipe for a
repair. Its restore, verify and list commands take more than one disc root,
because a snapshot can span several discs.

`decoder.py` is a fixed file, checked into the NoahsArk source repository and
versioned with this document. A writer copies it byte for byte from that
checked-in file; it is never generated or altered per repository or per
disc. Its INDEX file role is 14 (section 10.1), and it is at most 64 KiB.

### 8.7 Fill order inside a run

The Files table of INDEX is the authority for the file order of a run. The
writer lists the files in this order:

1. `INDEX.bin`.
2. `RUN.bin`, the run header.
3. `DISC.bin`, `README.txt`, `FORMAT.txt`, `REFERENCE/decoder.py`.
4. `catalog/REFS.bin`, then `catalog/DISCS.bin`.
5. Every object file, snapshots, trees, blobs and chunks alike, by content
   id ascending. This is the row order of the Objects table of INDEX.
6. `checksum.bin`, the checksum column, when `fec_scheme` is 1.
7. The parity files, in column order, when `fec_scheme` is 1.
8. `RUN2.bin`, the run header copy.

The FEC stream is steps 1 and 3 to 5, in that order: `INDEX.bin` first, then
every other file of those steps (section 7.2). `RUN.bin`, `checksum.bin`, the
parity files and `RUN2.bin` are outside the stream.

The order is total. Two conforming writers with the same file set produce the
same order. The order follows hashes, so it does not keep the files of one
directory together.

The position of a file on the medium is not part of the format. The image
builder decides it.

An object is listed once.

A chunk, a blob or a tree that another disc of the repository already holds
need not be written again. The packer then gives it no Objects row in this
run, and lists it in the Prereqs table when an object of this run references
it (section 10.1). Every snapshot object is written onto every disc (section
10.4).

Invariants:

1. `DISC.bin`, `catalog/DISCS.bin`, `INDEX.bin` and the run header become
   final in that order, each from values already final. INDEX holds the
   `file_hash` of every fixed-name stream file, and the run header holds
   `index_hash`.
2. The rows of `INDEX.bin`, `RUN.bin`, `RUN2.bin`, `checksum.bin`, the
   parity files and every object file carry an all-zero `file_hash` (section
   10.1).
3. The FEC stream is fully determined before the checksum column or the
   parity is computed.
4. Every column file is contiguous inside itself and holds `L` blocks.

### 8.8 Name and path budget

| Item | Limit |
|---|---|
| Object name | 68 characters, charset `[0-9a-f]`. Never stripped. |
| Any on-disc name | at most 126 characters |
| Any on-disc path | under 220 characters |
| Forbidden characters in a name | `< > : " / \ \| ? *`, control characters, a trailing space, a trailing dot, and the reserved device names `CON PRN AUX NUL COM1-9 LPT1-9` |

Never rely on case to distinguish two objects.

---
## 9. Forward error correction

FEC is optional per run: `fec_scheme` 0, `none`, writes no checksum column and
no parity, and `fec_scheme` 1, `rs255-gf8`, writes both, as this section
describes. The section "Scheme 0: no FEC" states the scheme 0 rules; every
other subsection describes scheme 1.

### 9.1 Parity layout

The FEC scheme is `rs255-gf8`: a systematic erasure code over GF(2^8) with `k`
information shards and `m` parity shards per stripe.

A shard is exactly one 2048-byte block of the FEC stream (section 7.2), or,
for the checksum and parity columns, one block of the file that carries that
column.

A stripe is 255 shards: `k` data, 1 checksum and `m` parity. The code covers
the `k` data and the `m` parity shards. The checksum shard is outside the
code.

Format version 1 fixes `k = 231` and `m = 23`. A version 1 writer writes no
other pair and a version 1 reader refuses any other pair. Both values are still
recorded, in the run header.

`stream_blocks = stream_bytes / 2048`, and
`L = ceil(stream_blocks / k)`. The FEC stream is the stream files in INDEX
order, zero-padded per file to a multiple of 2048, as section 7.2 states.

Data column `c` is blocks `[c*L, (c+1)*L)` of the FEC stream, for
`c = 0 .. k-1`. A block at or past `stream_blocks` is 2048 zero bytes. Such
a block exists in the last column or columns whenever `stream_blocks` is
below `k*L`.

Column `k`, the checksum column, is the `L` blocks of `runs/<seq>/checksum.bin`.

Column `c` for `c = k+1 .. 254` is the `L` blocks of
`runs/<seq>/parity/pNNNN.bin`, where `NNNN` is `c` in decimal zero-padded to
four digits: `p0232.bin` to `p0254.bin`. Block `i` of the column is bytes
`i*2048` to `i*2048 + 2047` of the file. The file is exactly `L` blocks,
with no header.

Every column file is contiguous. INDEX records the byte length of each, and
the run header records `stream_bytes`; a reader derives `stream_blocks` and
`L` from it and from `fec_k` by the formulas of section 7.5.

Stripe `i` is block `i` of every column, for `i = 0 .. L-1`.

Encoding is byte-column-wise: take one byte from each of the `k` data blocks
at the same byte offset, in column order 0 to `k-1`, and produce `m` parity
bytes at that offset, column `k+1` first, for all 2048 byte offsets.

**The burst bound.** The maximum correctable single burst is `m * L` blocks
of the FEC stream. A burst longer than `m * L` blocks puts more than `m`
erasures into one stripe, which section 9.5 refuses to decode.

### 9.2 The code

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
say so. The ordering of the shards is the only implementation
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
returns `0x53` and `0xA7`. The full-size golden vector of section 12 is
computed by the same rules at `k = 231`, `m = 23`.

The ordering of the shards is the only implementation freedom, and this section
fixes it. Any encoder that produces the same parity bytes for the same
information bytes conforms.

### 9.3 Checksum column

Purpose: an 8-byte digest of every data block of a stripe, so a damaged block
can be located before the code is applied.

Block `i` of the checksum column holds the digests of the `k` data blocks of
stripe `i`, and of no other block. There is no offset and no wrap.

The digest is the first 8 bytes of the 32-byte SHA-256 digest of the
block, computed over the 2048 bytes of the FEC stream at that position. The
digest of a block at or past `stream_blocks` is the digest of 2048 zero
bytes.

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
| 14 | 2 | u16 | `reserved_u16` | Zero. |
| 16 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 15. |
| 20 | 1848 | u8[1848] | `digests` | `k` digests of 8 bytes each, in data column order 0 to `k - 1`. |
| 1868 | 180 | u8[180] | `reserved` | Zero. |

Total: 2048 bytes.

Field rules. `digest_count` equals `k`. A digest is always 8 bytes and always
SHA-256. The digest of data column `c` is bytes `20 + 8*c` to `27 + 8*c` of
the block.

The checksum column is outside the Reed-Solomon code. The parity neither covers
it nor reconstructs it.

Reader checks. Check the magic and verify `header_crc32c`. When a checksum
block is unreadable or its header CRC fails, check that stripe's data blocks
through the content ids of the object files that INDEX maps them to, and
through the `file_hash` of the fixed-name files, and treat a failing block as
an erasure. A block that no Files row covers is 2048 zero bytes.

A silently wrong digest makes a verifier treat a good data block as an
erasure. Reconstruction returns the same bytes and the content id passes, and
the verifier reports the checksum block as damaged. No data is lost.

A parity block has no digest. An unreadable parity block is an erasure from
the start.

### 9.4 Header replication and parity files

The run header exists two times: `RUN.bin` and `RUN2.bin` (section 7.6). A
parity file holds its column and nothing else.

The parity geometry is derivable from either header copy: `stream_bytes`,
`fec_k` and `fec_m` determine every column boundary through the formulas of
section 7.5.

### 9.5 Decode rule

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

### 9.6 Scheme 0: no FEC

A run whose `fec_scheme` is 0, `none`, carries no `checksum.bin` file and no
parity files. Its INDEX carries no Files rows for roles 10 (`checksum.bin`)
and 11 (a parity file), and its Objects and Prereqs tables are unaffected.
`RUN.bin` and `RUN2.bin` still exist.

The stream definition of section 7.2 still applies to the file order: INDEX
lists every stream file in the same fill order whether or not the run carries
FEC, and the run header still records `stream_bytes`.

A reader verifies a scheme 0 run by checking every object's content id
(section 11.1) and the `file_hash` of every fixed-name Files row against the
bytes on disc; it performs no checksum-column or parity check, because none
exists. Heal refuses a scheme 0 run and reports that the run has no FEC,
naming the run seq; it repairs nothing, because there is no parity to repair
from.

---
## 10. The run index and the catalog

### 10.1 INDEX

Purpose: the one structure a reader opens first inside a run. It lists every
file the run wrote, in FEC stream order; it names every object the run
stores; and it names every object the run references but does not store.

There is no membership filter in this format: a reader proves absence by
consulting the INDEX Objects table of every disc a catalog lists (section
10.6).

Fixed body, after the 32-byte common header (`magic_kind` `INDEX`):

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 32 | 8 | u64 | `run_seq` | The run this INDEX describes. |
| 40 | 4 | u32 | `file_count` | Rows in the Files table. |
| 44 | 4 | u32 | `object_count` | Rows in the Objects table. |
| 48 | 4 | u32 | `prereq_count` | Rows in the Prereqs table. |
| 52 | 4 | u32 | `reserved_u32` | Zero. Keeps the tables 8-byte aligned. |
| 56 | `file_count * 48` | | `files` | The Files table. |
| | `object_count * 40` | | `objects` | The Objects table, sorted ascending by `content_id`. A reader binary-searches it directly; the table needs no separate lookup index. |
| | `prereq_count * 48` | | `prereqs` | The Prereqs table, sorted ascending by `content_id`. |

Fixed part: 56 bytes. `header_len` is 56. The Files table starts at offset
`header_len`, the Objects table at `header_len + file_count * 48`, and the
Prereqs table at `header_len + file_count * 48 + object_count * 40`. The
file ends after the last Prereqs row: `index_bytes` of the run header equals
`header_len + file_count * 48 + object_count * 40 + prereq_count * 48`, and
a reader refuses an INDEX whose length differs.

**Files table**, 48 bytes per row:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `file_hash` | SHA-256 of the whole file's bytes, for roles 3, 4, 5, 7, 8 and 14. All zero for every other role. |
| 32 | 8 | u64 | `byte_len` | Length of the file in bytes. |
| 40 | 1 | u8 | `role` | File role registry, below. |
| 41 | 7 | u8[7] | `reserved` | Zero. |

Total: 48 bytes.

File role registry:

| Id | Role | In the FEC stream | `file_hash` |
|---:|---|---|---|
| 0 | reserved | | |
| 1 | `INDEX.bin`, this file | Yes, the first file | Zero. `index_hash` of the run header covers it. |
| 2 | `RUN.bin` | No | Zero. Its own CRC covers it. |
| 3 | `DISC.bin` | Yes | Set. |
| 4 | `README.txt` | Yes | Set. |
| 5 | `FORMAT.txt` | Yes | Set. |
| 6 | reserved | | |
| 7 | `catalog/REFS.bin` | Yes | Set. |
| 8 | `catalog/DISCS.bin` | Yes | Set. |
| 9 | reserved | | |
| 10 | `checksum.bin` | No | Zero. |
| 11 | a parity file | No | Zero. |
| 12 | `RUN2.bin` | No | Zero. Its own CRC covers it. |
| 13 | an object file under `/NOAHSARK/objects/` or `/NOAHSARK/snapshots/` | Yes | Zero. The content id and the object header CRC cover it. |
| 14 | `/NOAHSARK/REFERENCE/decoder.py` | Yes | Set. |

Field rules. The rows are in the fill order of section 8.7: role 1, role 2,
roles 3, 4, 5 and 14, roles 7 and 8, every role 13 row, role 10, the role 11
rows in column order, role 12. Role 14 sits with roles 3 to 5 despite its
higher number: a role id is assigned once and never renumbered. This order
is authoritative: it is the order the writer laid the files in, and a reader
relies on it and never sorts.

The FEC stream (section 7.2) is the rows whose role is in the stream, in row
order. The rows of roles 2, 10, 11 and 12 describe files of the run that are
outside the stream, and a reader skips them when it adds up stream blocks.

A run whose `fec_scheme` is 0, `none` (section 9.6), carries no role 10 or
role 11 rows: there is no `checksum.bin` and no parity file. `RUN2.bin`,
role 12, still appears.

File names are not stored. A reader derives a fixed-name file's path from its
role, a parity file's name from its position among the role 11 rows (the
first is column `k + 1`), and an object file's name from the Objects table,
as follows.

The role 13 rows and the Objects rows pair by position. The number of role
13 rows equals `object_count`. The role 13 rows are in ascending `content_id`
order, the order of the Objects table. The `j`-th role 13 row, counted from
0, describes the file of Objects row `j`. The path of that file follows from
`content_id` and `kind` by section 3.5: `snapshots/` for kind 4, `objects/`
for kinds 1 to 3.

**Objects table**, 40 bytes per row, sorted ascending by `content_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | The object id. The 32 digest bytes, without the multihash prefix. |
| 32 | 1 | u8 | `kind` | Object kind registry, section 6.1. |
| 33 | 7 | u8[7] | `reserved` | Zero. |

Total: 40 bytes.

A row holds exactly what a reader needs to find the object: the id gives the
file name, and `kind` gives the directory. One object is one file. The
lengths and the compression of an object are in its own object header
(section 6.1), and the `byte_len` of its Files row equals `64 + stored_len`.
No `content_id` appears two times.

**Prereqs table**, 48 bytes per row, sorted ascending by `content_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | An object this run references but does not store. |
| 32 | 16 | u8[16] | `disc_uuid` | The `disc_uuid` of a disc that stores it. |

Total: 48 bytes.

Prerequisite membership is by direct reference, not by reachability. The
list is one edge deep. It holds every id that a tree, a blob or a new
snapshot of this run references directly, and that this run does not store
as an object of its own. A new snapshot is one that no earlier disc of the
repository carries. A snapshot that the run carries over from an earlier disc
(section 10.4) adds no row, and a snapshot's `parent` id is never listed. No
`content_id` appears two times.

The row names the disc by `disc_uuid`, never by a run number, because a
`run_seq` can repeat after a repository is rebuilt (section 7.1). When
several discs store the object, the writer names one of them; the choice is
host behaviour. A reader finds the label of the disc in DISCS (section
10.3). A reader accepts the object from any disc whose INDEX lists it, not
only from the named one.

Hash and CRC coverage. INDEX carries no CRC. The run header records
`index_bytes` and `index_hash` over every byte of `INDEX.bin`.

Reader checks. Check `magic_project`, `magic_kind` and `version_major`.
Verify `index_hash` and `index_bytes` from the run header before using any
row. Check that `run_seq` equals that of the run header. Check the file
length as above. Check that the count of role 13 rows equals `object_count`.
Refuse an Objects row whose `kind` is outside 1 to 4.

### 10.2 REFS

Purpose: the repository-wide table of named pointers to snapshots.

REFS and DISCS (section 10.3) share one container shape. Fixed body, after
the 32-byte common header (`magic_kind` `REFS` or `DISCS`):

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 32 | 16 | u8[16] | `repo_uuid` | The repository. |
| 48 | 8 | u64 | `record_count` | Records that follow. |
| 56 | `record_count * record size` | | `records` | Sorted, fixed-width. The record size is 88 for REFS and 176 for DISCS. |

Fixed part: 56 bytes. `header_len` is 56. The records start at offset
`header_len`. The file is `header_len + record_count * 88` bytes for REFS and
`header_len + record_count * 176` bytes for DISCS, and a reader refuses a
file whose length differs.

REFS uses the ref record of section 6.15, 88 bytes, in the sort order that
section 6.15 states.

REFS is carried in full on every disc, so a reader finds every name that the
repository held at pack time from one disc.

Hash and CRC coverage. REFS carries no CRC. The `file_hash` of its Files row,
role 7, covers every byte of `REFS.bin`.

Reader checks. Check the magic and `version_major`. Verify the `file_hash`
of the role 7 Files row against the bytes of `REFS.bin`. Check that
`repo_uuid` equals that of the run header. Find the newest record of a name
by the rule of section 6.15.

### 10.3 DISCS

Purpose: one row per disc that the repository knew at pack time, the disc
that carries the table included. It gives a reader the label to ask for when
an object is on another disc.

DISCS row, 176 bytes, sorted by `run_seq` ascending, then by `disc_uuid`
bytes ascending:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u64 | `run_seq` | The run on that disc, 1-based. |
| 8 | 8 | u64 | `disc_seq` | The disc sequence number, 0-based. |
| 16 | 16 | u8[16] | `disc_uuid` | That disc's uuid. The identity of the row. |
| 32 | 32 | u8[32] | `run_hash` | SHA-256 of the 512 bytes of that disc's `RUN.bin` (section 2.7). All zero in the row of the disc that **carries** this table: that header is written after the table (section 8.7), so its hash is not yet known. A table on a later disc holds the real value. |
| 64 | 8 | i64 | `created_sec` | Pack time of the run, as in the run header (section 7.5). Not the burn time. |
| 72 | 8 | u64 | `reserved_u64a` | Zero. |
| 80 | 8 | u64 | `capacity_sectors` | `capacity_sectors` of that disc's superblock (section 7.4). |
| 88 | 8 | u64 | `reserved_u64b` | Zero. |
| 96 | 4 | u32 | `reserved_u32` | Zero. |
| 100 | 2 | u16 | `label_len` | Byte length of the label, 0 to 64. |
| 102 | 64 | u8[64] | `label` | UTF-8, zero-padded. The disc's label, the same 64 bytes the superblock carries (section 7.4). |
| 166 | 10 | u8[10] | `reserved` | Zero. |

Total: 176 bytes.

Field rules. DISCS holds one row for every disc that the writer's repository
state lists, plus the row of the disc that carries the table. No `disc_uuid`
appears two times. Two rows can share a `run_seq`, after a repository was
rebuilt (section 7.1); a reader therefore looks a disc up by `disc_uuid` and
never by `run_seq`.

The row records no health, no verification result and no state of the disc.
The state of a write-once copy cannot be on that copy; it is host state, and
the operations document holds it.

A reader that sees `run_hash` all zero takes no hash from the row, and must
not treat the zero as a verification failure. A reader that holds the
`RUN.bin` of a disc and a nonzero `run_hash` for the same `disc_uuid` from
another disc compares the two, and reports a mismatch as a substituted or
damaged disc.

A reader that holds two copies of DISCS prefers the one from the disc whose
run header has the later `created_sec`, then `created_nsec`.

Hash and CRC coverage. DISCS carries no CRC. The `file_hash` of its Files
row, role 8, covers every byte of `DISCS.bin`. `run_hash` covers the 512
bytes of that disc's `RUN.bin`, CRC included.

Reader checks. Check the magic and `version_major`. Verify the `file_hash`
of the role 8 Files row against the bytes of `DISCS.bin`. Check that
`repo_uuid` equals that of the run header.

### 10.4 Catalog contents per run

Every run carries INDEX, a catalog of REFS and DISCS, and every snapshot
object of the repository.

| Item | Scope | Why |
|---|---|---|
| This run's INDEX | This run | File order, exact object lookup, prerequisites. |
| Every snapshot object, complete | The whole repository | A reader lists, names and walks the whole history from one disc. |
| REFS | The whole repository | The entry point for names. |
| DISCS | The whole repository | Maps every `disc_uuid` to its label. |

REFS, DISCS and the snapshot objects are carried in full on every disc.

A snapshot object has one place on a disc: `/NOAHSARK/snapshots/<name>`.
There is no second copy under `catalog/`. A snapshot of an earlier run is an
ordinary object file of this run: it has a role 13 Files row and an Objects
row with `kind` 4, and it lies in the FEC stream. The dedup rule of section
10.5 never drops a snapshot object.

### 10.5 Dedup rule

Never drop chunk data on the strength of a hint. Only an exact INDEX Objects
lookup permits dropping the data.

If a disc's INDEX cannot be consulted, the writer writes the chunk again and
logs the event.

A lookup compares content ids alone and needs no kind filter. The content id
covers the object kind (section 3.1), so ids of different kinds never compare
equal, and an INDEX hit always names an object of the kind the caller asked
for.

### 10.6 Proof of absence and coverage

There is no membership filter in this format. Proof of absence comes
directly from the INDEX Objects tables of the discs the catalog's DISCS table
lists: an object is missing when its content id is absent from every disc's
Objects table, checked exactly, not from a Prereqs table alone.

In a connectivity check chunks are never read. Membership alone is the whole
obligation. Only trees, blobs and snapshots are read.

Coverage of an object is exact whenever the INDEX Objects table is available
for the disc that should hold it; a reader that lacks a given disc's INDEX
(because that disc is not in the drive) states the object as missing
evidence, not as missing data, and does not fail a plan on that ground
alone. A reader states which for every object.

An object for which every reachable disc's Objects table is negative is
missing, and that is a proof. A plan with any missing object fails up front.

A plan that depends on a disc whose INDEX the reader does not yet hold is
valid and must not be refused; the reader confirms that disc's objects
against its own INDEX when the disc is in the drive, which is the first
thing the reader reads from that disc.

---
## 11. Reader and writer rules

### 11.1 Reader procedure

A reader applies these steps in order, for every structure:

1. Check `magic_project` and `magic_kind`. Refuse on a mismatch.
2. Check `version_major`. Refuse an unknown value and print the value.
3. Read `header_len` from the common header and obey it (section 2.3). Never
   assume the compiled size.
4. Verify the CRC, or the hash that another structure holds for this one,
   before using any field (sections 2.7 and 2.8).
5. Verify the content id after decompression, for an object: hash the kind
   byte and the payload.
6. Ignore every reserved field, every reserved bit and every padding byte.

For one disc, the order is:

1. List `/NOAHSARK/runs/` and take the run directory.
2. Read `RUN.bin`, or `RUN2.bin` when `RUN.bin` fails, and verify its CRC.
3. Read `INDEX.bin` and verify `index_bytes` and `index_hash`.
4. Verify `DISC.bin`, `catalog/REFS.bin` and `catalog/DISCS.bin` against
   their `file_hash` in INDEX.
5. Take the newest record of the wanted ref name from REFS (section 6.15),
   read that snapshot from `/NOAHSARK/snapshots/`, and walk the root tree,
   the trees, the blobs and the chunks. For an object that the Objects table
   of this disc does not list, look the id up in the Prereqs table, find the
   label of the named disc in DISCS, and ask for that disc.
6. When a file of a `fec_scheme` 1 run is unreadable or fails its check,
   repair it by sections 9.1 to 10.5.

Every object's content id is verified after it is read. A mismatch is a hard
error. Every function that returns object bytes verifies the content id before
it returns. There is no trusted path.

### 11.2 Writer rules

1. Never reuse a registry id.
2. Never change a frozen table under an existing name.
3. Never depend on a structure that a later version might change. Read the
   version first, and the `header_len`.
4. Write zero into every reserved field, every reserved bit and every padding
   byte.

### 11.3 Which catalog a reader trusts

A disc holds one run, and a reader takes the catalog of that run. A run
verifies when both of these pass: the `header_crc32c` of a run header copy the
reader could read, and `index_hash`, checked against the bytes of
`INDEX.bin`.

A writer of this version never writes a second run directory. A reader that
finds more than one takes the verified run with the highest `<seq>` and
reports the others.

Among several discs of one repository, the newest catalog is the one whose
run header has the latest `created_sec`, then `created_nsec`.

A local index is an accelerator only. Everything in it is derived from discs
and is rebuildable. Every command behaves the same, apart from speed, with the
local index deleted.

### 11.4 Conformance

A conforming reader of format major 1:

1. reads every structure of this document at `version_major` 1, by the rules
   of section 11.1;
2. reads objects whose `hash_algo` is `sha2-256`, and refuses an object
   whose `hash_algo` it does not implement, naming the code; it reads
   compression ids 0 and 1 and refuses any other id, naming it;
3. finds every object through the filesystem, the run header, INDEX and the
   catalog, with no local index and no disc other than the ones the plan
   names; a disc whose filesystem does not mount counts as lost;
4. verifies every CRC, every hash of section 2.7 and every content id before it
   uses the bytes;
5. repairs a `fec_scheme` 1 run with `k = 231`, `m = 23`, or says that it
   cannot repair; the reference decoder holds no Reed-Solomon code and says
   so; a `fec_scheme` 0 run has nothing to repair from;
6. refuses an unknown `version_major`, an unknown registry id in a field it
   must interpret, and a critical TLV it does not know, and says which;
7. ignores a nonzero reserved field, reserved bit or padding byte, and obeys
   a `header_len` above the value it knows.

A conforming writer of format major 1:

1. writes every structure exactly as its byte-offset table states, with
   `version_major` 1, the `header_len` of section 2.3 and reserved fields
   zero;
2. writes SHA-256 content ids over the kind byte and the uncompressed
   payload, and FastCDC cut points by section 4.2;
3. writes one run per disc, with `RUN.bin`, `RUN2.bin`, the file order of
   section 8.7 and the catalog of section 10.4; a `fec_scheme` 1 run also
   carries `k = 231`, `m = 23`, the checksum column of section 9.3 and the
   `m` parity files; a `fec_scheme` 0 run carries no checksum column and no
   parity;
4. writes every byte as an ordinary file under `/NOAHSARK/`, adds no padding
   for alignment on the medium, and never rewrites or extends a burned disc;
5. never writes a reserved id, bit or value.

### 11.5 Change mechanisms

A later writer has three mechanisms, and no other: a new registry id, a new
field in reserved space or behind a larger `header_len`, and a
`version_major` bump (section 2.6).

A registry id is never reused and never renumbered. A new value in an
existing registry field needs no version bump at all: an old reader already
refuses a registry id it does not know, in the field it was already reading.

| Change | Mechanism | Old reader does | New reader does |
|---|---|---|---|
| New hash algorithm | New id in the hash registry, for a digest of 32 bytes. | Refuses the run and every object whose `hash_algo` it does not know, and says the code. Old discs stay readable. | Reads both. |
| Chunker parameter or Gear table change | None on the disc. No structure records them. | Unaffected. A reader never needs them. | Unaffected. |
| New compression algorithm | New id in the compression registry. | Refuses an object whose `compression` it does not know. The object is unreadable, not misread. | Reads it. |
| FEC parameter change | None. The run header already records `k` and `m`. | Refuses the run for repair, because version 1 accepts no pair other than 231 and 23. Still reads the objects through the filesystem. | Reads the recorded pair. |
| New FEC scheme | New id in the FEC scheme registry. | Refuses the run for repair, but still reads its objects. | Repairs it. |
| New object kind | New id in the object kind registry. | Refuses the structure, since `kind` is already outside 1 to 4. | Reads it. |
| New tree TLV | New id in the TLV registry. The critical bit decides. | Refuses the entry when the critical bit is set. Preserves and reports the TLV otherwise. | Applies it. |
| New informational field | Reserved space, or the end of the fixed part with a larger `header_len`. | Ignores it. | Reads it. |
| Snapshot signature | New snapshot TLV, non-critical. | Ignores it. | Verifies it. |

### 11.6 Cross-version and unknown-value reading

`Y` means full use. `~` means partial use with the loss named. `N` means a
clean refusal that names the reason.

By format version:

| Writer | Reader of major 1 | Reader of a later major |
|---|---|---|
| Major 1 | Y | Y. It reads the fixed sizes of major 1. |
| Major 1, with a field in reserved space or a larger `header_len` | Y. It ignores the reserved space and skips `header_len - known_len` bytes. | Y |
| A later major | N. Refuses and prints `version_major`. | Y |

By algorithm and registry id:

| Written with | Reader that knows it | Reader that does not |
|---|---|---|
| SHA-256 ids | Y. Mandatory for a conforming reader. | Not possible at major 1. |
| A new hash algorithm id | Y | N. Refuses the object and names the multicodec code. Older discs stay readable. |
| compression 0 or 1 | Y | Not possible at major 1. |
| A new compression id | Y | N. Refuses the object and names the id. |
| A non-critical unknown tree TLV | Y | ~ Preserves it on copy, reports it on restore, does not apply it. |
| A critical unknown tree TLV, 0x8000 to 0xBFFF | Y | N. Refuses the entry and names the type. |
| `k`, `m` other than 231, 23 | Y | N for repair; still reads every object. |
| A new FEC scheme id | Y | N for repair; still reads every object. |

### 11.7 Refusal and partial reading

Every refusal is loud, names the field and the value, and never touches the
bytes it refused. Every partial case reads the data in full and puts the loss
in the report. An empty refusal, a silent skip and a misread are all defects.

An error about an object names the object. An error about a run names the run
seq and the disc uuid.

---
## 12. Golden vectors

Every implementation ships a golden-file test per structure: write known values
and compare bytes, then read the file back and compare fields. The same test
checks that every reserved field and every padding byte of the written file
is zero.

A golden vector is a checked-in input and its expected output. The expected
values are not printed in this document, apart from the printed `k = 3`,
`m = 2` parity example of section 9.2, the two root name examples of section
6.14 and the CRC-32C check value of section 2.1. A vector file is named by
the structure and the version. A vector is never changed under a frozen
version; a format change adds a vector.

The Gear table and the mask constants are vendored: they are generated exactly
once, checked in as a literal array, and never regenerated from a dependency.
The golden vectors cover them.

| Vector | Input |
|---|---|
| Gear table | The rule of section 4.6 |
| Masks | The rule of section 4.7 |
| Cut points | A fixed 256 MiB pseudo-random file, generated from a stated seed by a stated generator, and a fixed 3 MiB file |
| Zero chunk | 16 MiB, 8 MiB and 32 MiB of zero bytes |
| Multihash text form | One digest under sha2-256 |
| Common header and object header | One chunk of stated bytes, stored with zstd level 3 under the frame parameters of section 5.3 and a named `tool_version`, and stored uncompressed |
| Blob | 100 stated chunk ids and lengths |
| Tree | A directory with a regular file, addressed through its blob object, a subdirectory, a symlink with its TLV target, two entries sharing one source inode and stored as independent entries, a device node, and one unknown non-critical TLV, with stated metadata |
| Snapshot | A stated root tree, parent, times and TLVs |
| Ref record | A stated name, snapshot id and time |
| Disc superblock | Stated identity and capacity values |
| Run header | Stated identity and geometry |
| INDEX | Ten stated Files rows, of which four have role 13, four stated Objects rows, and two stated Prereqs rows that name two discs |
| README.txt | The identity values of the disc superblock vector |
| FORMAT.txt | The embedded bytes equal `FORMAT.md` byte for byte (section 8.5) |
| REFS and DISCS | Two stated rows of each table |
| Enlarged `header_len` | One file per structure kind except chunk, disc superblock and run header, with `header_len` 8 above the value of section 2.3 and 8 nonzero bytes in the gap. A reader must decode the same fields as from the plain vector. |
| Nonzero reserved bytes | One file per structure with a nonzero byte in every reserved field, and correct CRCs and ids. A reader must decode the same fields as from the plain vector. |
| Checksum block | The 231 stated data blocks of one stripe |
| Parity | A stated stripe of `k` data blocks, plus recovery after `m` erasures |
| Root tree name encoding | `/srv/data`, `/a%b/c`, `/x\y` |
| CRC-32C | The 9-byte string `123456789` |
