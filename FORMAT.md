# NoahsArk on-disc format

Format major version 1. Document version 3.2.

This document defines every byte that NoahsArk writes onto a disc and every
rule a reader applies to those bytes. It covers the binary conventions, object
identity, chunking, compression, every object kind, the disc and run
structures, the filesystem layout, forward error correction, the filters,
manifests and catalog tables, and the checks a reader performs. Everything in
this document is normative. A second implementation that follows it produces
byte-identical discs from the same inputs and makes identical accept and
reject decisions on the same bytes. Commands, configuration, workflow,
rationale and local state live in other documents and are not repeated here.

## Table of contents

- [1. Scope and conventions](#1-scope-and-conventions)
- [2. Binary format rules](#2-binary-format-rules)
  [2.1](#21-the-format-rules) · [2.2](#22-magic-values) · [2.3](#23-common-object-header) · [2.4](#24-feature-flag-registry) · [2.5](#25-strings) · [2.6](#26-registries) · [2.7](#27-version-policy) · [2.8](#28-hash-coverage-per-structure) · [2.9](#29-crc-coverage-per-structure) · [2.10](#210-limits) · [2.11](#211-trust-boundaries-and-safety-invariants)
- [3. Identity and hashing](#3-identity-and-hashing)
  [3.1](#31-the-content-id-rule) · [3.2](#32-hash-algorithms) · [3.3](#33-digest-fields-in-records) · [3.4](#34-text-form) · [3.5](#35-fan-out-on-disc) · [3.6](#36-hash-epochs-and-cross-epoch-references) · [3.7](#37-cross-algorithm-side-table)
- [4. Chunking](#4-chunking)
  [4.1](#41-algorithm) · [4.2](#42-cut-point-rule) · [4.3](#43-chunker-profiles) · [4.4](#44-profile-recording-and-change) · [4.5](#45-bundle-threshold) · [4.6](#46-zero-regions-and-sparse-files) · [4.7](#47-determinism) · [4.8](#48-gear-table) · [4.9](#49-mask-constants)
- [5. Compression](#5-compression)
  [5.1](#51-order-of-operations) · [5.2](#52-header-fields) · [5.3](#53-algorithm-and-frame-parameters) · [5.4](#54-minimum-gain) · [5.5](#55-compression-and-identity) · [5.6](#56-what-is-never-compressed)
- [6. Objects](#6-objects)
  [6.1](#61-object-kinds) · [6.2](#62-chunk) · [6.3](#63-bundle) · [6.4](#64-chunklist) · [6.5](#65-tree) · [6.6](#66-tree-entry-fixed-header) · [6.7](#67-entry-flags) · [6.8](#68-variable-areas-and-the-content-area) · [6.9](#69-extension-tlv-record) · [6.10](#610-tlv-type-registry) · [6.11](#611-spill-rule) · [6.12](#612-name-validation) · [6.13](#613-what-is-never-stored) · [6.14](#614-hardlinks) · [6.15](#615-snapshot) · [6.16](#616-the-root-tree) · [6.17](#617-exclude-pattern-language) · [6.18](#618-ref) · [6.19](#619-reserved-crypto-fields) · [6.20](#620-canonical-ordering)
- [7. Disc and run model](#7-disc-and-run-model)
  [7.1](#71-the-physical-disc) · [7.2](#72-disc-filesystem-profile) · [7.3](#73-the-run-and-the-fec-terms) · [7.4](#74-layout-on-the-medium) · [7.5](#75-disc-superblock) · [7.6](#76-run-header) · [7.7](#77-the-run-chain) · [7.8](#78-run-header-copies) · [7.9](#79-what-an-append-overwrites) · [7.10](#710-run-layout-table) · [7.11](#711-raw-lba-reading) · [7.12](#712-close-state) · [7.13](#713-raw-append) · [7.14](#714-forced-capacity) · [7.15](#715-how-a-lifecycle-state-is-recorded)
- [8. Filesystem profiles and the volume tree](#8-filesystem-profiles-and-the-volume-tree)
  [8.1](#81-profiles-a-reader-must-know) · [8.2](#82-files-at-the-volume-root) · [8.3](#83-run-directory-naming) · [8.4](#84-readmetxt) · [8.5](#85-formattxt) · [8.6](#86-fill-order-inside-a-run) · [8.7](#87-name-and-path-budget)
- [9. Capacity invariants](#9-capacity-invariants)
- [10. Forward error correction](#10-forward-error-correction)
  [10.1](#101-parity-layout) · [10.2](#102-the-code) · [10.3](#103-checksum-column) · [10.4](#104-header-replication-and-parity-files) · [10.5](#105-decode-rule) · [10.6](#106-uncovered-sectors-and-the-append-bound) · [10.7](#107-health-status-values)
- [11. Filters, manifests and the catalog](#11-filters-manifests-and-the-catalog)
  [11.1](#111-run-filter) · [11.2](#112-run-manifest) · [11.3](#113-prerequisite-list) · [11.4](#114-snapshot-objects-and-the-simple-tables) · [11.5](#115-run-table) · [11.6](#116-disc-directory) · [11.7](#117-catalog-contents-per-run) · [11.8](#118-catalog-container) · [11.9](#119-dedup-rule) · [11.10](#1110-proof-of-absence-and-coverage)
- [12. Reader and writer rules](#12-reader-and-writer-rules)
  [12.1](#121-reader-procedure) · [12.2](#122-writer-rules) · [12.3](#123-which-catalog-a-reader-trusts) · [12.4](#124-conformance) · [12.5](#125-change-mechanisms) · [12.6](#126-cross-version-and-cross-phase-reading) · [12.7](#127-refusal-and-partial-reading) · [12.8](#128-settings-that-change-disc-bytes)
- [13. Golden vectors](#13-golden-vectors)
- [Appendix A. FORMAT.txt, format major 1 minor 0](#appendix-a-formattxt-format-major-1-minor-0)
- [Appendix B. Decision index](#appendix-b-decision-index)

## 1. Scope and conventions

A NoahsArk disc carries one directory tree. Every byte NoahsArk writes is an
ordinary file inside that tree. There are no hidden sectors, no fixed-LBA
structures and no raw areas outside the filesystem.

```
/NOAHSARK/
    DISC.bin                     disc superblock, written once
    README.txt                   plain-text explanation for a human
    FORMAT.txt                   the byte-layout tables of every structure
    runs/<seq>/
        RUN.bin                  run header, the first file copied in the run
        layout.bin               file order, LBA extents, column geometry, k, m
        manifest.bin             sorted object table with a fan-out
        filter.bin               BinaryFuse16 over this run's object ids
        catalog/                 CATALOG.bin, snapshot objects, filters,
                                 manifests, tables
        pad.bin                  zero fill to the end of the data columns
        checksum.bin             the checksum column
        parity/pNNNN.bin         one file per parity column
        RUN2.bin                 run header copy, the last file copied in the run
    objects/<ab>/<name>          chunks and bundles, shared by every run on the disc
    trees/<ab>/<name>            tree and chunklist objects
    snapshots/<name>             snapshot objects
```

`<ab>` is the first two hex digits of the digest. `<name>` is the full
68-character multihash hex.

A run is the unit of packing, of the manifest, of the filter, of the
Reed-Solomon parity and of the catalog copy.

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
   reserved field. A writer writes a reserved field as zero. A reader ignores
   the value of a reserved field.
4. Every structure begins with `magic` (u32), then `version_major` (u16), then
   `version_minor` (u16).
5. A structure is a file-level container or an object payload header. A record
   inside a container, a tree entry, a TLV, the bundle trailer and the checksum
   sector header are records, not structures. A record carries a magic only
   where its own table states one.
6. A reader refuses an unknown `version_major`. A reader accepts an unknown
   `version_minor` and ignores the fields it does not know.
7. Every top-level structure carries `required_feat` (u64) and `optional_feat`
   (u64). A reader refuses an unknown `required_feat` bit. A reader ignores an
   unknown `optional_feat` bit.
8. A checksum covers only bytes the writer finalized before computing it. A
   structure with no separate body ends with one CRC over every byte before
   it. A container with a header and a body carries `header_crc32c` and
   `body_crc32c` at the end of its header: `body_crc32c` sits in the header
   and covers the body. The body becomes final first, then the header is
   written last, so each CRC covers bytes that were already final.
9. The run filter is the one structure whose body CRC trails the body. It
   writes `header_crc32c` inside the fixed header and `body_crc32c` after the
   variable-length fingerprint array, because that array's length is not known
   until the array is sized.
10. CRCs are CRC-32C: polynomial 0x1EDC6F41, reflected, initial value
    0xFFFFFFFF, final XOR 0xFFFFFFFF.
11. Objects use the full content hash in place of a body CRC. The content id is
    the checksum of the payload.
12. Every pointer carries the hash of its target. No unhashed reference exists.
13. A string is `encoding` (u8), `reserved` (u8[3], zero), `length` (u32) in
    bytes, then the bytes. Encoding 0 is UTF-8. There is no NUL terminator and
    no normalization.
14. Every structure has a byte-offset table with the columns offset, size,
    type, name and meaning.

The CRC-32C parameters and their check value:

From section 2.1 rule 10, verbatim:

> CRCs use CRC-32C (Castagnoli, polynomial 0x1EDC6F41,
> reflected, initial value 0xFFFFFFFF, final XOR 0xFFFFFFFF).

From Appendix A, part 7 CHECKSUM PARAMETERS, verbatim (tab-separated):

```
7. CHECKSUM PARAMETERS
======================

CRC32C_POLYNOMIAL	0x1edc6f41
CRC32C_REFLECTED	1
CRC32C_INIT	0xffffffff
CRC32C_XOROUT	0xffffffff
SECTOR_BYTES	2048
FEC_K	231
FEC_M	23
```

Check value, from the golden vector table of section 13, verbatim:

| CRC-32C | The 9-byte string `123456789`. | `0xE3069283`. |


### 2.2 Magic values

A magic value is four ASCII bytes. A reader reads it as a little-endian u32, so
the first byte of the file is the first character of the mnemonic. An
implementation computes the constant from the ASCII bytes and never copies the
hexadecimal column.

Each magic is four ASCII bytes. The file bytes are the mnemonic in order. The
u32 value is the little-endian reading of those bytes.

| Mnemonic | u32 value | Structure | Section |
|---|---|---|---|
| `NAOB` | 0x424F414E | Common object header | 2.3 |
| `NADS` | 0x5344414E | Disc superblock | 7.5 |
| `NARH` | 0x4852414E | Run header | 7.6 |
| `NALY` | 0x594C414E | Run layout table | 7.10 |
| `NAMF` | 0x464D414E | Manifest container | 11.2 |
| `NAFL` | 0x4C46414E | Filter container | 11.1 |
| `NAST` | 0x5453414E | Snapshot table | 11.4 |
| `NARF` | 0x4652414E | Ref table | 6.18 |
| `NART` | 0x5452414E | Run table | 11.5 |
| `NADD` | 0x4444414E | Disc directory | 11.6 |
| `NACT` | 0x5443414E | Catalog container | 11.8 |
| `NATR` | 0x5254414E | Tree object payload | 6.5 |
| `NASN` | 0x4E53414E | Snapshot object payload | 6.15 |
| `NACL` | 0x4C43414E | Chunklist object payload | 6.4 |
| `NABD` | 0x4442414E | Bundle header | 6.3 |
| `NABT` | 0x5442414E | Bundle trailer | 6.3 |
| `NACS` | 0x5343414E | Checksum column sector header | 10.3 |
| `NAXL` | 0x4C58414E | Cross-algorithm side table | 3.7 |

An implementation must compute these constants from the ASCII bytes, not copy
the hexadecimal column, and a test must assert that the two agree.

Six further four-byte magics name host-only structures whose bytes never reach
a disc: `NASL`, `NABN`, `NABP`, `NABS`, `NALR` and `NANT`. The operations
document holds them and their layouts. They are assigned once, like every value
above, and no value in either list is ever reused.

### 2.3 Common object header

Purpose: every stored object begins with this 64-byte header, which names the
kind, the digest algorithm, the compression and the two lengths.

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NAOB"`, 0x424F414E. |
| 4 | 2 | u16 | `version_major` | 1. Refuse if unknown. |
| 6 | 2 | u16 | `version_minor` | 0. Ignore if unknown. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 1 | u8 | `kind` | Object kind registry, section 6.1. |
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

Field rules. `kind` takes a value from the object kind registry. `digest_len`
is 32 in version 1. `header_len` is 64 in version 1, and a reader computes the
end of the header from `header_len` and skips `header_len - 64` bytes.
`payload_len` is the uncompressed length. `stored_len` is the number of bytes
on the medium after the header.

Hash and CRC coverage. `header_crc32c` covers bytes 0 to 59. The payload is
covered by the content id, which is the hash of the uncompressed payload alone
and never of the header.

Reader checks. Check the magic. Check `version_major`. Read `header_len`.
Check `required_feat`. Verify `header_crc32c` before using any field. Read
`stored_len` bytes, decompress them into exactly `payload_len` bytes, hash the
result and compare it with the digest in the file name.

### 2.4 Feature flag registry

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

Bits 7 to 63 of the required half and bits 4 to 63 of the optional half are
reserved. A writer must set them to zero.

A feature bit belongs to the structure that carries it. A reader checks only
the bits of the structure it is reading. A feature bit is assigned once and is
never reused.

A version 1 writer sets exactly these bits and no other:

| Structure | Bit | Set when |
|---|---|---|
| Common object header (section 2.3) | `FEAT_COMPRESSION` | `compression` is not 0. |
| Tree header (section 6.5) | `FEAT_CHUNKLIST` | Any entry sets `CONTENT_IS_CHUNKLIST`, or any TLV sets `SPILL_IS_CHUNKLIST`. |
| Tree header (section 6.5) | `FEAT_TLV_SPILL` | Any TLV sets `SPILLED`. |
| Run header (section 7.6) | `FEAT_FEC_RS8` | Always. Every version 1 run carries parity. |
| Run layout table (section 7.10) | `FEAT_FEC_RS8` | Always. |
| Manifest container (section 11.2) | `FEAT_BUNDLES` | The `"BNDL"` chunk holds at least one bundle id. |
| Manifest container (section 11.2) | `FEAT_FAN16` | `fanout_bits` is 16. |
| Catalog container (section 11.8) | `OPT_XLATE` | A cross-algorithm side table (section 3.7) was copied into the catalog. Phase 3. |
| Run header (section 7.6) | `OPT_DISC_PARITY` | `run_kind` is 3, a disc-close parity run. Phase 3. |

`FEAT_CRYPTO`, `OPT_BITMAPS` and `OPT_REVIDX` are never set by a version 1
writer.

### 2.5 Strings

Purpose: a variable-length text field inside a structure.

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 1 | u8 | `encoding` | 0 = UTF-8. Other values reserved. |
| 1 | 3 | u8[3] | `reserved` | Zero. Keeps `length` aligned. |
| 4 | 4 | u32 | `length` | Byte count of the string data. |
| 8 | `length` | u8[] | `data` | The bytes, as found. No terminator. |

### 2.6 Registries

A registry id is assigned once. It is never reused and never renumbered.

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
| 1 | `rs255-gf8` | GF(2^8) | 255 | Default. A stripe is `k + 1 + m = 255` sectors; the code is over the `k + m` data and parity shards (section 10.1). |
| 2 | `rs-leopard-gf16` | GF(2^16) | up to 65536 | Reserved. |

**Object kind registry.** Section 6.1 repeats it with detail.

| Id | Name |
|---:|---|
| 1 | `chunk` |
| 2 | `bundle` |
| 3 | `chunklist` |
| 4 | `tree` |
| 5 | `snapshot` |
| 6 | `ref` |

**Disc filesystem profile registry.** Section 7.2 states what a profile fixes.

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
| 11 | `Mini BD SL 8 cm` | 3,804,288 | 7,791,181,824 |
| 12 | `Mini BD DL 8 cm` | 7,608,576 | 15,582,363,648 |

The tool must take the true capacity from `growisofs -F`. The tool must not
use a hardcoded number for a burn. The table is for planning and for labels.

### 2.7 Version policy

`version_major` changes when an old reader would misread the structure.

Exactly three structures carry `header_len`: the common object header, the tree
header and the tree entry. A reader computes the end of such a header from
`header_len`, not from its compiled size, and skips `header_len - known_len`
bytes.

Every other structure has a fixed size under its `version_major` and grows only
by a major bump. A minor bump on such a structure may only give meaning to a
reserved field.

A feature an old reader can ignore takes an `optional_feat` bit. A feature an
old reader must not ignore takes a `required_feat` bit.

### 2.8 Hash coverage per structure

Every hash field that names another structure covers all bytes of that
structure as they lie on the medium, with every CRC already filled in. No hash
is computed over a structure whose CRC is zeroed.

| Field | In | Covers | Algorithm |
|---|---|---|---|
| `manifest_hash` | Run header (section 7.6) | Every byte of `manifest.bin`. | Run header `hash_algo`. |
| `filter_hash` | Run header | Every byte of `filter.bin`. | Run header `hash_algo`. |
| `layout_hash` | Run header | Every byte of `layout.bin`, after the records and both CRCs are final. | Run header `hash_algo`. |
| `catalog_hash` | Run header | Every byte of `catalog/CATALOG.bin`. | Run header `hash_algo`. |
| `prev_run_header_hash` | Run header | The 512 bytes of the previous run header, CRC included. | Run header `hash_algo`. |
| `prev_disc_super_hash` | Disc superblock (section 7.5) | The 2048 bytes of the previous disc's superblock, CRC included. | Superblock `hash_algo`. |
| `super_hash` | Disc directory record (section 11.6) | The 2048 bytes of that disc's superblock, CRC included. | Container `hash_algo`. |
| `run_header_hash` | Run table record (section 11.5) | The 512 bytes of that run's header, CRC included. | Container `hash_algo`. |
| `file_hash` | Catalog entry (section 11.8) | Every byte of the named catalog file. | Container `hash_algo`. |
| `content_id` | Extent record (section 7.10), fixed-name file | Every byte of the file. Zero for the roles that section 7.10 lists. | Layout `hash_algo`. |
| Content id | Object file name | The uncompressed payload only (section 3.1). Never the object header. | Run header `hash_algo`. |
| Sector digest | Checksum sector (section 10.3) | The 2048 bytes of one data sector as they lie on the medium. | BLAKE3-256, the first 8 bytes of the 32-byte digest. |

Host-only structures carry hash fields of their own. None of those bytes reaches
a disc, and the operations document holds their coverage table.

### 2.9 CRC coverage per structure

| Structure | Field | Covers |
|---|---|---|
| Common object header (section 2.3) | `header_crc32c` | Bytes 0 to 59. The payload is covered by the content id. |
| Bundle header (section 6.3) | `header_crc32c` | Bytes 0 to 59 of the bundle header. |
| Bundle trailer (section 6.3) | `trailer_crc32c` | Bytes 0 to 27 of the trailer. The chunk payloads and the index are covered by the bundle's content id. |
| Chunklist, tree and snapshot payloads (sections 6.4, 6.5, 6.15) | none | The content id covers the whole payload, records included. |
| Reindex container (section 3.7) | `header_crc32c`, `body_crc32c` | Bytes 0 to 59; every record. |
| Disc superblock (section 7.5) | `super_crc32c` | Bytes 0 to 2043. |
| Run header (section 7.6) | `header_crc32c` | Bytes 0 to 507. |
| Run layout table (section 7.10) | `header_crc32c`, `body_crc32c` | Bytes 0 to 107; every extent record. |
| Checksum sector (section 10.3) | `header_crc32c` | Bytes 0 to 11. The digests are checked as section 10.3 states. |
| Filter container (section 11.1) | `header_crc32c`, `body_crc32c` | Bytes 0 to 75; the fingerprint array. |
| Manifest container (section 11.2) | `header_crc32c`, `body_crc32c` | Bytes 0 to 59; every byte from offset 64 to the end, that is the TOC, the sentinel and every chunk. |
| Simple table container (section 11.4) | `header_crc32c`, `body_crc32c` | Bytes 0 to 59; every record. |
| Catalog container (section 11.8) | `header_crc32c`, `body_crc32c` | Bytes 0 to 59; every entry. |

Host-only structures carry CRC fields of their own. None of those bytes reaches
a disc, and the operations document holds their coverage table.

### 2.10 Limits

A writer refuses an input that exceeds a limit of this table and names the
limit. A reader refuses a structure that exceeds one and names the limit.

| Item | Limit | Where it is fixed |
|---|---:|---|
| Digest length | 32 bytes | Section 3.3. A longer digest needs a new major version. |
| Tree entry name | 1 to 4095 bytes | `name_len`, section 6.6. |
| Tree entries per directory | 2^32 - 1 | `entry_count`, section 6.5. |
| Inline chunk ids per entry | writer 64, reader any | `chunklist.inline_max`, section 6.4. |
| Chunklist entries | 2^64 - 1 | `entry_count`, section 6.4. A version 1 writer never writes a `level` 1 chunklist. |
| Tree entry length | 2^32 - 1 bytes | `entry_len`, section 6.6. |
| TLV payload | 2^32 - 1 bytes; spilled above `tree.tlv_spill_threshold` | Sections 6.9 and 6.11. |
| Snapshot metadata TLVs | 65,535 per snapshot | `meta_count`, section 6.15. |
| Ref name | 1 to 40 bytes | Section 6.18. |
| Disc label | 64 bytes in the superblock, 48 bytes in the disc directory | Sections 7.5 and 11.6. |
| Run sequence number | 1 to 9,999,999,999 | The 10-digit run directory name, section 8.3. |
| Manifest fan-out | 256 or 65,536 entries | Section 11.2. |
| Objects per run | 2^32 - 1 | `record_count`, section 11.2. The `"FANO"` fan-out table holds cumulative u32 counts, so this is the limit the manifest can index; a writer refuses to pack a run past it. |
| File size | 2^64 - 1 bytes | Every size field is u64, section 2.1. |
| Disc capacity | 2^64 - 1 sectors | Section 7.5. |
| FEC geometry | `k = 231`, `m = 23` | Section 10.1. |
| On-disc object name | 68 characters | Section 3.4. |
| Any on-disc name | 126 characters | Section 8.7. |
| Any on-disc path | under 220 characters | Section 8.7. |

The host limits of the burn plan, the commit bundle, the shelf note and the
host filesystems are not in this table. The operations document holds them.

The disc label has two widths. The superblock holds 64 bytes. The disc
directory record holds 48 bytes, which are the first 48 bytes of the superblock
label. Both widths are as their tables state.

### 2.11 Trust boundaries and safety invariants

- Bytes read from a disc are untrusted until the content id verifies.
- A local index is untrusted. Every cache answer is confirmed against a
  manifest before it is used to drop data.
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

The default algorithm for new objects is BLAKE3-256. SHA-256 is fully
supported. Every implementation must read and write both.

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
/NOAHSARK/objects/<d0><d1>/<full 68-character text form>     chunks and bundles
/NOAHSARK/trees/<d0><d1>/<full 68-character text form>       trees and chunklists
/NOAHSARK/snapshots/<full 68-character text form>            snapshots
```

Object roots are split by kind: `objects/` for chunks and bundles, `trees/` for
trees and chunklists, `snapshots/` for snapshots.

The file name is the full 68-character multihash hex. It is never stripped,
never shortened and never split.

Fan-out is one level by default. Two levels are allowed under filesystem
profile 0 and profile 1 only, as `objects/<d0><d1>/<d2><d3>/<id>`. Profile 2
allows one level only.

The superblock records the choice in `fanout_levels`.

### 3.6 Hash epochs and cross-epoch references

An epoch is a maximal run of runs that share one hash algorithm. The
configured current algorithm names the algorithm for new objects.

Every run header records the algorithm of the objects in that run. Every disc
superblock records the algorithm of its first run. Old discs keep their
algorithm forever. Nothing rewrites them.

The tree graph below one snapshot is single-algorithm: the root tree, every
tree, every chunklist and every chunk id below it use the `hash_algo` of the
snapshot header. Only the six fields below may name an object under another
algorithm, and each states its algorithm.

| Field | Where | Algorithm stated by |
|---|---|---|
| `parent` | Snapshot payload header | `parent_hash_algo` in the same header. |
| `content_id` | Prerequisite record | The run header of `run_seq`, which holds the object. |
| `snapshot_id` | Snapshot table record | `hash_algo` in the same record. |
| `root_tree` | Snapshot table record | `hash_algo` in the same record. |
| `parent_id` | Snapshot table record | `parent_hash_algo` in the same record. |
| `snapshot_id` | Ref record | `hash_algo` in the same record. |

Every other digest uses the algorithm of its containing structure.

### 3.7 Cross-algorithm side table

Purpose: an optional table that maps an object id under an old algorithm to the
id of the same bytes under a new algorithm.

The table is derived data. It is never authoritative, and correctness never
depends on it. A writer may copy it onto a disc, and a catalog that carries it
sets `OPT_XLATE`.

Container header, 64 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NAXL"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 16 | u8[16] | `repo_uuid` | The repository. |
| 40 | 8 | u64 | `record_count` | Records that follow. |
| 48 | 2 | u16 | `record_size` | 72. |
| 50 | 1 | u8 | `old_hash_algo` | Multicodec code of `old_id` in every record. |
| 51 | 1 | u8 | `new_hash_algo` | Multicodec code of `new_id` in every record. |
| 52 | 1 | u8 | `digest_len` | 32. |
| 53 | 3 | u8[3] | `reserved` | Zero. |
| 56 | 4 | u32 | `body_crc32c` | CRC-32C over the records. |
| 60 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 59. |
| 64 | `record_count * 72` | | `records` | Sorted ascending by `old_id`. |

Table record, 72 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `old_id` | Digest under `old_hash_algo`. |
| 32 | 32 | u8[32] | `new_id` | Digest under `new_hash_algo`. |
| 64 | 8 | u64 | `run_seq` | Run that holds the bytes. |

Field rules. Records are sorted ascending by `old_id`. `record_size` is 72.

Hash and CRC coverage. `body_crc32c` covers the records. `header_crc32c`
covers bytes 0 to 59.

Reader checks. Verify both CRCs. Treat every row as a hint only.

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
max, NC level, Gear table id, bundle threshold and bundle target.

A reader never needs the profile. A reader follows content ids only.

A writer must never change the Gear table or the mask constants under an
existing profile name. A Gear table change needs a new `gear_table_id` and a
new profile name.

There is no rechunk operation. New runs use a new profile. Old discs keep
theirs.

### 4.5 Bundle threshold

Any chunk whose uncompressed payload is below the configured bundle threshold
goes into a bundle. A chunk at or above the threshold is stored as its own
file.

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
    digest     = BLAKE3-256(input)             # 32 bytes
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
`stored_len`, and therefore every LBA after the object, so a golden vector that
names compressed bytes also names the `tool_version` that produced them
(section 13).

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
- The run layout table.
- The manifest container.
- The filter.
- `README.txt`.
- The payload of a bundle object as a whole.

---

## 6. Objects

### 6.1 Object kinds

| Id | Kind | Payload | References | Stored as |
|---:|---|---|---|---|
| 1 | `chunk` | Opaque bytes. | None. | One file, or a slice of a bundle. |
| 2 | `bundle` | Concatenated small chunks plus an index. | The chunks it holds. | One file. |
| 3 | `chunklist` | Ordered chunk ids and lengths. | Chunks, and other chunklists. | One file. |
| 4 | `tree` | One directory. | Trees, chunklists, chunks. | One file. |
| 5 | `snapshot` | Root tree, parent, generation, text. | Root tree, parent snapshot. | One file. |
| 6 | `ref` | A named pointer to a snapshot. | A snapshot. | A row in the ref table. |

Kind 6 is never an object file. The value 6 must never appear in the `kind`
field of a manifest record or a layout extent record. A reader that finds it
refuses the record and names the structure.

A chunk carries no reference.

### 6.2 Chunk

Purpose: opaque file content bytes, addressed by the hash of those bytes.

A chunk object is the common object header followed by the payload bytes.

Hash and CRC coverage. `header_crc32c` covers bytes 0 to 59 of the header. The
content id covers the uncompressed payload.

Reader checks. Verify the header CRC, decompress `stored_len` into
`payload_len` bytes, and verify the content id.

### 6.3 Bundle

Purpose: one object file that holds many small chunk payloads with an index, so
that a directory of small files costs few filesystem entries and few seeks.

```
 [ common object header, kind = 2, compression = 0 ]
 [ bundle header, 64 bytes                    ]
 [ chunk payload 0 ][ chunk payload 1 ] ...    stored bytes only, no header
 [ index table: entry_count * 64 bytes         ]
 [ bundle trailer, 32 bytes                    ]
```

A chunk payload inside a bundle carries no common object header. The index
entry is its header. Each chunk inside a bundle is compressed on its own by the
rule of section 5.4, so one bundle may mix compressed and uncompressed chunks.

The bundle's own common object header carries `compression` 0, and
`payload_len` and `stored_len` both equal the bundle payload length from the
bundle header to the end of the trailer.

A bundle is filled in path order, which is the depth-first order of section
8.6. A bundle may span sibling files and sibling directories.

The writer closes the open bundle before the next chunk when adding that
chunk's `stored_len` would make `data_len` exceed the configured bundle target
size, and then opens a new bundle for that chunk. Nothing else closes a bundle:
not a file boundary, not a directory boundary, not the chunk count. The last
bundle of a run closes when the run's chunk stream ends. A bundle holding
exactly one chunk whose `stored_len` already exceeds the target is legal.

`data_len` counts stored bytes, not uncompressed bytes, and excludes the bundle
header, the index and the trailer.

A bundle must not hold a delta.

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

Field rules. Bundle index entries are sorted ascending by `content_id`.
`entry_size` is 64 and a reader strides by it. `index_off` is the offset of the
index table from the first byte of the bundle header. `data_off` is the offset
of the first chunk payload. `offset` in an index entry is measured from
`data_off`.

Hash and CRC coverage. The bundle header `header_crc32c` covers bytes 0 to 59
of the bundle header. The trailer `trailer_crc32c` covers bytes 0 to 27 of the
trailer. The chunk payloads and the index are covered by the bundle's content
id.

Reader checks. Seek to the last 32 bytes, read the trailer magic, verify the
trailer CRC, then read the index at `index_off`. Verify the bundle header CRC.
Verify each chunk payload against the `content_id` of its index entry after
decompressing `stored_len` bytes into `payload_len` bytes.

A tree entry references the chunk id, never the bundle id. The manifest maps
the chunk id to a bundle and an offset through the `"BNDL"` table. The manifest
record holds no stored length for a bundled chunk; the bundle index supplies
it.

### 6.4 Chunklist

Purpose: the ordered chunk ids of one file, held outside the tree entry.

A file with more than the configured inline maximum of chunks references a
chunklist object instead of an inline chunk array. That limit is a writer
choice. A reader accepts any inline count that `content_len` states.

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

Field rules. Entries are in file order, keyed by ascending `file_offset`.
`entry_size` is 48. `total_size` is the sum of `length` over all entries.

A version 1 writer never writes a `level` 1 chunklist. A version 1 reader must
accept and follow `level` 1. A reader refuses a `level` above 1 and names the
value.

Hash and CRC coverage. The chunklist payload carries no CRC. The content id
covers the whole payload, records included.

Reader checks. Check the magic and `version_major`. Check `required_feat`.
Verify the content id of the object before using any entry. Refuse a `level`
above 1.

### 6.5 Tree

Purpose: one directory, with one entry per child.

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

Field rules. `header_len` is 40 in version 1, and a reader skips the excess.
Entries follow back to back, each self-delimiting through its own `entry_len`.
Metadata lives inline in the tree entry, not in a separate node object.

Tree entries are sorted by raw name bytes, ascending, unsigned. A directory
name compares as if a `/` byte were appended. Sorting is mandatory.

Hash and CRC coverage. The tree payload carries no CRC. The content id covers
the whole payload, entries included.

Reader checks. Check the magic and `version_major`. Read `header_len` and skip
the excess. Check `required_feat`. Verify the content id. Validate every entry
name at parse time by section 6.12.

### 6.6 Tree entry fixed header

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `entry_len` | Total entry length including all variable areas. Multiple of 8. |
| 4 | 2 | u16 | `header_len` | 112 in version 1. A reader skips the excess. |
| 6 | 1 | u8 | `entry_type` | 1 regular, 2 directory, 3 symlink, 4 chardev, 5 blockdev, 6 fifo, 7 socket. 0 is invalid. |
| 7 | 1 | u8 | `entry_flags` | See section 6.7. |
| 8 | 8 | u64 | `size` | Regular files only. 0 otherwise. |
| 16 | 8 | u64 | `hardlink_group` | Reserved for a later phase. A Phase 1 writer writes 0. See section 6.14. |
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

Time is stored as `i64` seconds plus `u32` nanoseconds, with nanoseconds always
in [0, 999999999].

File type is `entry_type`, a u8 enum. `mode` is a u32 holding permission bits
only. The type is not in the mode.

`uid` and `gid` are u32, with `0xFFFFFFFF` meaning unknown.

### 6.7 Entry flags

| Bit | Name | Meaning |
|---:|---|---|
| 0 | `HARDLINK_MEMBER` | Reserved for a later phase. A Phase 1 writer clears this bit. See section 6.14. |
| 1 | `ATIME_ABSENT` | `atime_sec` and `atime_nsec` carry no information. |
| 2 | `CTIME_ABSENT` | `ctime_sec` and `ctime_nsec` carry no information. A writer sets it when the `metadata.ctime` configuration key is false, and when that key is true and the source reported no ctime. |
| 3 | `BTIME_ABSENT` | `btime_sec` and `btime_nsec` carry no information. |
| 4 | `SPARSE` | The source file had holes. The restorer punches holes, as section 4.6 states. |
| 5 | `METADATA_PARTIAL` | The source read failed for at least one metadata field. |
| 6 | `CONTENT_IS_CHUNKLIST` | The content area holds one chunklist id, not chunk ids. |
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
| 1 regular, inline | `32 * chunk_count` | Chunk ids in file order. `chunk_count = content_len / 32`. A writer inlines at most `chunklist.inline_max` ids; a reader accepts any count. |
| 1 regular, chunklist | 32 | One chunklist id. `CONTENT_IS_CHUNKLIST` is set. |
| 1 regular, empty | 0 | No content area. |
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
| 2 | 2 | u16 | `tlv_flags` | bit0 `CRITICAL`, bit1 `SPILLED`, bit2 `SPILL_IS_CHUNKLIST`, bits 3 to 15 reserved. |
| 4 | 4 | u32 | `tlv_len` | Payload bytes, excluding this prefix and excluding padding. |
| 8 | `tlv_len` | u8[] | `payload` | The value, or the spill reference. |
| | pad | u8[] | | Zero bytes to the next 8-byte boundary. |

TLVs are sorted ascending by `tlv_type`, then by payload bytes. Canonical order
is mandatory.

A registered TLV type, 0x0001 to 0xBFFF, appears at most once per entry. Only a
vendor type, 0xF000 to 0xFFFF, may repeat.

A reader that meets an unknown TLV with `CRITICAL` set must refuse the entry. A
reader that meets an unknown non-critical TLV must keep it on copy and report
it on restore. A writer never emits a TLV that it does not implement.

When `SPILLED` is set the payload is exactly 40 bytes: a 32-byte content id and
a u64 uncompressed length. The id names a chunk, or a chunklist when
`SPILL_IS_CHUNKLIST` is also set.

### 6.10 TLV type registry

| Type | Name | Critical | Payload |
|---:|---|---|---|
| 0x0001 | `SYMLINK_TARGET` | yes | Raw bytes. Mandatory when `entry_type` is 3. Never validated as UTF-8. |
| 0x0002 | `USER_NAME` | no | UTF-8 bytes. |
| 0x0003 | `GROUP_NAME` | no | UTF-8 bytes. |
| 0x0004 | `ROOT_PATH` | no | Raw bytes of a source root's absolute path. Only on an entry of the root tree; source roots are defined in the operations document. |
| 0x0010 | `XATTR` | no | Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a `version_minor` bump. A Phase 1 reader treats it as an unknown TLV. |
| 0x0011 | `ACL_ACCESS` | no | Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a `version_minor` bump. A Phase 1 reader treats it as an unknown TLV. |
| 0x0012 | `ACL_DEFAULT` | no | Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a `version_minor` bump. A Phase 1 reader treats it as an unknown TLV. |
| 0x0013 | `ACL_NFS4` | no | Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a `version_minor` bump. A Phase 1 reader treats it as an unknown TLV. |
| 0x0020 | `LINUX_ATTR` | no | `u32` `FS_IOC_GETFLAGS` bitmask. |
| 0x0021 | `BSD_FLAGS` | no | `u32` `st_flags`. |
| 0x0030 | `WIN_ATTRS` | no | `u32` `FILE_ATTRIBUTE_*` bitmask. |
| 0x0031 | `WIN_SD` | no | Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a `version_minor` bump. A Phase 1 reader treats it as an unknown TLV. |
| 0x0032 | `WIN_ADS` | no | Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a `version_minor` bump. A Phase 1 reader treats it as an unknown TLV. |
| 0x8000-0xBFFF | reserved critical | yes | Future critical extensions. |
| 0xF000-0xFFFF | vendor | no | Never critical. |

Phase 1 stores Unix permissions only: `mode`, `uid` and `gid` in the tree
entry fixed header. `XATTR`, `ACL_ACCESS`, `ACL_DEFAULT`, `ACL_NFS4`, `WIN_SD`
and `WIN_ADS` are reserved TLV types that carry extended attributes or an
access control list. A Phase 1 reader does not implement any of their item
layouts, so it applies the unknown-TLV rule above to each: keep it on copy and
report it on restore.

`SYMLINK_TARGET` is mandatory when `entry_type` is 3 and is never validated as
UTF-8.

### 6.11 Spill rule

If the total TLV area would exceed the configured spill threshold, the writer
spills eligible payloads one at a time:

1. Measure the TLV area as it would be written, every record's 8-byte prefix
   and its padding included. Stop when the measure is at or below the
   threshold.
2. Otherwise take the one eligible payload with the largest `tlv_len`. A tie
   breaks by the lowest `tlv_type`, then by the lowest payload bytes ascending.
3. Spill that one payload, which replaces its payload with the 40-byte
   reference, and repeat from step 1.

The loop ends when the area is at or below the threshold or when no eligible
payload is left.

An eligible payload is chunked with the run's chunker profile. One chunk gives
a chunk spill reference. More than one chunk gives a chunklist object, and the
TLV sets `SPILL_IS_CHUNKLIST` as well as `SPILLED`. The u64 length is the
uncompressed payload length in both cases.

`XATTR`, `ACL_ACCESS`, `ACL_DEFAULT`, `ACL_NFS4`, `WIN_SD` and `WIN_ADS` are
reserved in Phase 1, so a Phase 1 writer never spills them; a later phase
defines their spill eligibility with their item layout. Never spilled: the
name, `SYMLINK_TARGET`, `USER_NAME`, `GROUP_NAME`.

### 6.12 Name validation

A tree entry name is exactly one path component. The parser must reject at
parse time a name that is empty, that is `.` or `..`, or that contains a `/`, a
`\` or a NUL byte.

A name is not required to be valid UTF-8.

### 6.13 What is never stored

Inode numbers, source filesystem device ids and link counts are never stored.

### 6.14 Hardlinks

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


### 6.15 Snapshot

Purpose: one root tree pointer, a parent pointer, a generation number and the
snapshot's own metadata.

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
| 112 | 8 | u64 | `total_size` | Sum of `payload_len` over the distinct objects that `reachable_object_count` counts, that is over the distinct chunks, chunklists and trees reachable from `root_tree`. A chunk that several files share is counted once. It is not the sum of the file sizes. For planning. |
| 120 | 8 | u64 | `reachable_object_count` | Distinct content ids reachable from `root_tree`: chunks, chunklists and trees, the root tree included. Bundles are not counted, and the snapshot itself is not counted. This is a logical reachability count; it is not comparable to the run header's `object_count` (section 7.6), which is a physical count that includes bundles. |
| 128 | 1 | u8 | `hash_algo` | Multicodec code of `root_tree` and of every id below it. |
| 129 | 1 | u8 | `chunker_profile` | Chunker profile id used to produce it. |
| 130 | 2 | u16 | `meta_count` | Number of TLV records that follow. |
| 132 | 1 | u8 | `source_type` | Where the source tree was read from. See below. |
| 133 | 1 | u8 | `source_flags` | What the source could not provide. See below. |
| 134 | 1 | u8 | `parent_hash_algo` | Multicodec code of `parent`. Equals `hash_algo` except across an epoch boundary. 0 for a root. |
| 135 | 1 | u8 | `reserved_u8` | Zero. |
| 136 | | | TLV records | `meta_count` records follow. |

Field rules. `generation` is 1 + the parent generation, and 1 for a root.
`parent` is all zero for a root, and `parent_hash_algo` is 0 for a root.

`total_size` is the sum of `payload_len` over the distinct objects that
`reachable_object_count` counts. It is not the sum of the file sizes.

`reachable_object_count` counts distinct content ids reachable from
`root_tree`: chunks, chunklists and trees, the root tree included. Bundles are
not counted and the snapshot itself is not counted. It is a logical count and
is not comparable to the run header's physical `object_count`.

Metadata TLV record:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 2 | u16 | `tag` | Snapshot metadata tag registry, section 6.15: 1 author, 2 host, 3 message, 4 source root, 5 exclude rules, 6 checksum commit. Tags 4 and 5 repeat, one per root, in root order. |
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
| 5 | exclude rules | UTF-8 pattern lines of section 6.17, one per line, in rule order, for the root of the preceding tag 4. | One per root, in root order. |
| 6 | checksum commit | Empty. Present when the commit rehashed every file. | No. |
| 7 to 0x7FFF | reserved | | |
| 0x8000 to 0xBFFF | reserved critical | A reader refuses an unknown tag in this range. | |
| 0xF000 to 0xFFFF | vendor | Never critical. | May repeat. |

Tags 4 and 5 repeat, one per root, in root order.

Tag 5 holds exactly two of the three rule sources of section 6.17: every
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
| 1 | reserved | Zero in Phase 1. Phase 1 does not detect hardlink groups; see section 6.14. |
| 2 | `NO_SPARSE` | `SEEK_HOLE` was unavailable, so holes were found by reading. |
| 3 | `SYNTHETIC_IDS` | uid, gid or mode may have been synthesized by the mount. |
| 4 | `CASE_INSENSITIVE` | The source did not distinguish names by case. |
| 5 | `MTIME_SLACK` | An mtime slack was applied in the quick check. |
| 6 | reserved | Zero in Phase 1. See section 6.14. |
| 7 | reserved | Zero. |

A remote source root records its limits in `source_type` and `source_flags`. A
restore prints one warning per set `source_flags` bit, once, before it writes
anything.

Hash and CRC coverage. The snapshot payload carries no CRC. The content id
covers the whole payload, the TLV records included.

Reader checks. Check the magic and `version_major`. Check `required_feat`.
Verify the content id. Refuse an unknown metadata tag in the critical range
0x8000 to 0xBFFF.

### 6.16 The root tree

A snapshot has exactly one synthetic `root_tree`. It holds one entry per source
root, in root order. Roots are ordered by path bytes ascending.

A root entry has `entry_type` 2, directory, and its content area holds the tree
id of the root's own directory. The entry's mode, uid, gid and times are those
of the root directory itself. TLV `ROOT_PATH` carries the raw path bytes,
unencoded.

The entry name is the root's absolute path, encoded so that it is one path
component that section 6.12 accepts. Exactly four bytes are escaped and no
others: `/` becomes `%2F`, `\` becomes `%5C`, NUL becomes `%00`, and `%`
becomes `%25`. The hex digits are uppercase. Every other byte, `.` included, is
kept as it is. Decoding replaces every `%XX` by its byte.

A root whose encoded name would be empty, `.` or `..` is refused at commit
time. An encoded name above 4095 bytes is refused.

### 6.17 Exclude pattern language

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

### 6.18 Ref

Purpose: a named pointer to a snapshot, stored as a row in the ref table of the
catalog, never as a separate object file.

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

### 6.19 Reserved crypto fields

Encryption and signing are reserved and unused in version 1. The `crypto` byte
of the common object header is 0. The 256-byte key-material region of the disc
superblock is all zero. `FEAT_CRYPTO` is never set.

### 6.20 Canonical ordering

Two identical directories must serialize to identical bytes. The ordering table
below is mandatory.

| Structure | Order key |
|---|---|
| Tree entries | Raw name bytes, ascending, directories compared with a trailing `/`. |
| Tree entry variable areas | Name, then content refs, then TLVs, each 8-byte aligned, then zero padding to `entry_len` (section 6.6). |
| TLV records | `tlv_type` ascending, then payload bytes ascending. |
| Xattr items inside a TLV | Name bytes ascending. |
| Chunklist entries | File offset ascending. |
| Bundle index entries | Content id ascending. |
| Manifest records | Content id ascending. |

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
the newest run header and `state_flags` bit 0 of the newest disc directory
record; and the drive's POW spare state. Any one of them saying closed is
enough. `fs_profile` never enters that decision.

A profile defines exactly seven items: the filesystem and its revision; the
image builder command and its options; the name and path limits and the fan-out
depth; the directory layout of fixed-name files; the append mechanism and the
burn command lines; the LBA read-back method; and the cross-OS read matrix.

These items are identical under every profile: the object model and the object
ids, the run structure and the run header, the manifest, the filter and the
catalog, the Reed-Solomon parity, which works over LBA ranges and never over
files, and the packer, the planner and the restorer.

These items exist under profile 1 only: next-writable-address handling, the
block diff, the LBA re-verification after an append, the spare area budget, the
raw append degraded mode, and the never-close policy for a disc that receives
more runs.

Everything NoahsArk writes is an ordinary file under `/NOAHSARK/`. There are no
hidden sectors, no fixed-LBA structures and no raw areas outside the
filesystem.

### 7.3 The run and the FEC terms

A run is one execution of one burn plan. It is the unit of packing, of the
manifest, of the filter, of the Reed-Solomon parity and of the catalog copy.

A run occupies a contiguous LBA range and is self-contained: it carries its own
header at its start and again at its end, its own manifest, its own filter, its
own layout table and its own parity.

The disc always has exactly one physical session.

**FEC terms.** This table is a complete forward definition of every term that
sections 7.4 to 7.10 use. Section 10.1 repeats it with the burst bound and the
encoding order, and section 10.2 defines the arithmetic of the code. Nothing in
sections 7.4 to 7.10 needs a term that is not here.

| Term | Meaning |
|---|---|
| `k`, `m` | The data column count and the parity column count. Version 1 fixes `k = 231` and `m = 23`, so `k + 1 + m = 255`. |
| `lba_base` | The first sector of the run's parity domain. For the first run of a disc it is **LBA 0**, so that the filesystem descriptors, the anchor at LBA 256 and every directory block below `RUN.bin` are protected. For every later run it is the first sector of that run's `RUN.bin`. |
| `data_span` | The number of sectors from `lba_base` to the **last sector that step 6 of section 8.6 occupies**, inclusive. Under profile 0 and profile 1 that last sector is the File Entry block of the last file of step 6, because a UDF File Entry follows its file data (see the operations document). Under profile 2 it is the last data sector of that file, because ISO 9660 keeps its directory records elsewhere. Steps 7 to 10, that is `pad.bin`, `checksum.bin`, the parity files and `RUN2.bin`, are outside `data_span`. |
| `L`, column length | The number of sectors in one column. `L = ceil(data_span / k)`. |
| Column | A range of `L` sectors. The `k` data columns are the LBA ranges `[lba_base + c*L, lba_base + (c+1)*L)` for `c = 0 .. k-1`. Column `k`, the checksum column, is the `L` data sectors of `checksum.bin`. Columns `k+1` to 254 are the `m` parity columns, each the `L` sectors that follow the header sector of one `parity/pNNNN.bin` file. |
| Parity domain | The `k` data columns together: the contiguous range `[lba_base, lba_base + k*L)`. Every sector in it is protected, whatever file or filesystem structure it belongs to. `pad.bin` fills the domain from the end of `data_span` to the end of the domain (section 8.6). |
| Stripe | Sector `i` of every column, for `i = 0 .. L-1`. A stripe is 255 sectors: `k` data, 1 checksum, `m` parity. The code covers the `k` data and the `m` parity sectors; the checksum sector is outside the code (section 10.3). |

The File Entry block of `pad.bin`, of `checksum.bin` and of every parity file
lies at or after `lba_base + k*L`, outside the domain. Those blocks are
filesystem metadata that the layout table makes unnecessary for a reader.

Under profile 0 and profile 1 the last sector of step 6 is the File Entry block
of the last file of step 6. Under profile 2 it is the last data sector of that
file.

### 7.4 Layout on the medium

The first run's parity domain starts at LBA 0, so the filesystem descriptors,
the anchor at LBA 256, the integrity descriptor, the space bitmap and every
directory block that lies below `RUN.bin` are inside it. A later run's domain
starts at its own `RUN.bin`.

Under profile 2 the filesystem directory records of every earlier run are
rewritten inside the newest run, past the next writable address. Those bytes
are part of the newest run and are covered by the newest run's parity. The data
extents of earlier runs are untouched and stay covered by their own parity.

Under profile 1 only the changed filesystem blocks are rewritten, and they are
rewritten in place, at their old LBAs, because a UDF File Entry cannot move.
Section 10.6 states how a reader treats such a sector.

### 7.5 Disc superblock

Purpose: the immutable facts of one physical disc, written once as
`/NOAHSARK/DISC.bin` in the first run and never updated.

The superblock is 2048 bytes, exactly one sector. It is written **once**, in the
first run, as the ordinary file `/NOAHSARK/DISC.bin`. It is never updated.

The superblock holds only **immutable** facts. Mutable disc state comes from the
run chain (section 7.7). That is what makes every NoahsArk structure on a disc
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
| 72 | 8 | u64 | `fill_limit_sectors` | Number of sectors, counted from LBA 0, that the writer may use. No run, parity included, ends at or above this LBA. The operations document is the normative home of this value, of `data_budget` and of the reserve; it gives the definition, the invariants and the reference estimator. |
| 80 | 8 | u64 | `capacity_forced_sectors` | Forced capacity, section 7.14. Equals `capacity_sectors` when no override was given. |
| 88 | 8 | u64 | `first_run_lba` | LBA of the first run header. Immutable: the first run never moves. |
| 96 | 8 | u64 | `reserved_u64a` | Zero. |
| 104 | 8 | u64 | `reserved_u64b` | Zero. |
| 112 | 8 | u64 | `reserved_u64c` | Zero. |
| 120 | 8 | u64 | `reserved_u64d` | Zero. |
| 128 | 32 | u8[32] | `reserved_hash` | Zero. |
| 160 | 32 | u8[32] | `prev_disc_super_hash` | Hash of the superblock of the highest `disc_seq` below this one of which the writer holds a verified copy. All zero when there is none, which includes `disc_seq` 0 and the first disc of a repository recreated by `init --repo-uuid` (section 7.5, the superblock chain). |
| 192 | 8 | i64 | `created_sec` | Pack time of the first run, seconds: the moment `pack` finalized that run's image. Not the burn time, which is unknown when these bytes are hashed (section 7.6). |
| 200 | 4 | u32 | `created_nsec` | Nanoseconds. |
| 204 | 4 | i32 | `tz_offset_sec` | Local zone offset at that pack time. |
| 208 | 1 | u8 | `media_type` | Media type registry. |
| 209 | 1 | u8 | `fs_profile` | Disc filesystem profile registry. |
| 210 | 1 | u8 | `hash_algo` | Multicodec code of the first run, and of `prev_disc_super_hash`. |
| 211 | 1 | u8 | `digest_len` | 32. |
| 212 | 1 | u8 | `chunker_profile` | Chunker profile of the first run. |
| 213 | 1 | u8 | `compression` | Default compression of the first run. |
| 214 | 1 | u8 | `fec_scheme` | FEC scheme registry. |
| 215 | 1 | u8 | `crypto` | 0 plaintext. |
| 216 | 2 | u16 | `sector_size` | 2048. |
| 218 | 2 | u16 | `fs_revision` | 0x0201 for UDF 2.01. 0x0004 for ISO 9660:1999 level 4. |
| 220 | 2 | u16 | `fec_k` | Data columns. 231 in version 1. |
| 222 | 2 | u16 | `fec_m` | Parity columns. 23 in version 1. |
| 224 | 1 | u8 | `fanout_levels` | 1 by default. 2 is allowed under profile 1 and profile 0 only. |
| 225 | 1 | u8 | `append_variant` | Profile 1 only. 1 = variant 1a, kernel direct write. 2 = variant 1b, image mirror and block diff. 0 elsewhere. |
| 226 | 1 | u8 | `capacity_is_forced` | 1 when `capacity_forced_sectors` is below `capacity_sectors`. |
| 227 | 1 | u8 | `sealed` | 1 when the disc was burned sealed at its first write: `spare:none` and `-dvd-compat` at its first and only write (see the operations document). 0 when the disc was left open. It says nothing about a later `noahsark close`, because the superblock is never updated; the close state of an open disc lives in the run chain and the disc directory (sections 7.7 and 11.6). |
| 228 | 4 | u32 | `label_len` | Byte length of the label. |
| 232 | 64 | u8[64] | `label` | UTF-8, zero-padded. |
| 296 | 4 | u32 | `tool_version` | Writer registry id in the high 8 bits, writer-defined version in the low 24 bits. |
| 300 | 4 | u32 | `reserved_u32` | Zero. |
| 304 | 8 | u64 | `reserved_u64e` | Zero. Object counts are mutable and live in the run chain. |
| 312 | 8 | u64 | `reserved_u64f` | Zero. Used sectors are mutable and live in the run chain. |
| 320 | 8 | u64 | `reserve_computed_sectors` | The reserve that the formula in the operations document produced. |
| 328 | 8 | u64 | `reserve_forced_sectors` | `disc.force_reserve`, in sectors. 0 when unset. |
| 336 | 8 | u64 | `reserve_extra_sectors` | `disc.extra_reserve`, in sectors. 0 when unset. |
| 344 | 168 | u8[168] | `reserved_a` | Zero. |
| 512 | 256 | u8[256] | `key_material` | Reserved for encryption. All zero in version 1. |
| 768 | 1276 | u8[1276] | `reserved_b` | Zero. |
| 2044 | 4 | u32 | `super_crc32c` | CRC-32C over bytes 0 to 2043. |

Field rules. The superblock holds only immutable facts. Mutable disc state
comes from the run chain.

`prev_disc_super_hash` names the superblock of the highest `disc_seq` below
this one of which the writer holds a verified copy. It is all zero when the
writer holds none.

The superblock's `hash_algo`, `chunker_profile` and `compression` fields
describe the first run. A reader takes current values from the newest run
header.

`append_variant` records the profile 1 append variant. Both variants produce
the same disc bytes, and a reader cannot tell them apart.

`tool_version` is a u32. Its high 8 bits are a writer registry id, and its low
24 bits are a version value that the named writer defines for itself. Registry
id 1 is the reference implementation. Registry id 0 is invalid. Ids 2 to 255
are assigned once, on request, and are never reused. A reader never interprets
the low 24 bits of a writer it does not know; it prints the pair. The run
header records the same value for the run that wrote it (section 7.6).

The superblock needs no second copy. It lies inside the first run's parity
domain, and the run headers carry the disc uuid, the repository uuid and the
disc sequence.

Hash and CRC coverage. `super_crc32c` covers bytes 0 to 2043.

Reader checks. Check the magic, `version_major` and `required_feat`. Verify
`super_crc32c` before using any field. Check the superblock chain against the
disc directory of the newest catalog the reader holds. Any combination other
than the three accepted cases is a chain failure: refuse the disc and report
the expected and the found value. A reader with no disc directory cannot check
the chain, says so, and does not refuse the disc on that ground.

### 7.6 Run header

Purpose: the identity, geometry, counts and table pointers of one run.

The run header is 512 bytes. It is written as `RUN.bin` at the first sector of
the run, in the first sector of every parity file, and as `RUN2.bin` at the
end of the run. That is `m + 2` copies. Every copy is byte-identical.

Every copy occupies one whole 2048-byte sector: the header is bytes 0 to 511
of that sector, and bytes 512 to 2047 are zero. **The files `RUN.bin` and
`RUN2.bin` are therefore 2048 bytes long, not 512.** Their layout records
give `byte_len` 2048 and `sector_count` 1. The `prev_run_header_hash` of
section 2.8 and the `run_header_hash` of section 11.5 cover the 512 header
bytes only, never the 1536 zero bytes.

`layout.bin` records the LBA of every copy. A recovery tool reads
`layout.bin`, or scans for the `"NARH"` magic at sector alignment.

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NARH"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 16 | u8[16] | `disc_uuid` | The disc this run sits on. |
| 40 | 16 | u8[16] | `repo_uuid` | The repository. |
| 56 | 8 | u64 | `run_seq` | Monotonic run number in the repository, 1-based. |
| 64 | 8 | u64 | `disc_seq` | Disc sequence number, 0-based. |
| 72 | 8 | u64 | `lba_base` | First LBA of the run's parity domain. 0 for the first run of a disc. The LBA of `RUN.bin` for every later run (section 7.3). |
| 80 | 8 | u64 | `run_sectors` | Length of the run in sectors, from `lba_base` to the last sector of `RUN2.bin`, inclusive. |
| 88 | 8 | u64 | `column_sectors` | `L`, the length of one column. |
| 96 | 2 | u16 | `fec_k` | Data columns. 231 in version 1. |
| 98 | 2 | u16 | `fec_m` | Parity columns. 23 in version 1. |
| 100 | 1 | u8 | `fec_scheme` | FEC scheme registry. |
| 101 | 1 | u8 | `checksum_column` | Column index of the checksum column. Equals `fec_k`. |
| 102 | 1 | u8 | `hash_algo` | Multicodec code of every id in this run. |
| 103 | 1 | u8 | `digest_len` | 32. |
| 104 | 1 | u8 | `chunker_profile` | Profile id. |
| 105 | 1 | u8 | `chunker_nc_level` | 2. |
| 106 | 1 | u8 | `compression` | Default compression id. |
| 107 | 1 | u8 | `fs_profile` | Disc filesystem profile id. Equals the superblock's `fs_profile`. |
| 108 | 4 | u32 | `chunk_min` | Bytes. |
| 112 | 4 | u32 | `chunk_avg` | Bytes. |
| 116 | 4 | u32 | `chunk_max` | Bytes. |
| 120 | 4 | u32 | `gear_table_id` | Gear table version. 1 in this specification. |
| 124 | 4 | u32 | `bundle_threshold` | Bytes. |
| 128 | 8 | u64 | `bundle_target` | Bytes. |
| 136 | 8 | u64 | `object_count` | Objects in this run. Equals `record_count` of the manifest. |
| 144 | 8 | u64 | `payload_bytes` | Stored object bytes in this run: the sum of `byte_len` over the extent records of `layout.bin` whose `file_role` is 0. Each extent's `byte_len` is the bytes that extent carries, so a fragmented object's extents sum to its header plus its whole stored payload, counted once, and a bundle is counted once, not per chunk. |
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
| 304 | 8 | u64 | `catalog_lba` | LBA of `catalog/CATALOG.bin` in this run (section 11.8). |
| 312 | 8 | u64 | `catalog_sectors` | Length of `CATALOG.bin` in sectors. |
| 320 | 32 | u8[32] | `catalog_hash` | Hash of the `CATALOG.bin` bytes. |
| 352 | 32 | u8[32] | `prev_run_header_hash` | Hash of the previous run header on this disc. Zero for the first run. |
| 384 | 8 | i64 | `created_sec` | Pack time, seconds: the moment `pack` finalized this run's image. Never the burn time, which is unknown when these bytes are hashed; the actual burn time lives only in the local state log; see the operations document. |
| 392 | 4 | u32 | `created_nsec` | Nanoseconds. |
| 396 | 4 | u32 | `source_run_count` | Number of run seqs this run references. |
| 400 | 8 | u64 | `snapshot_count` | Snapshot objects that this run stores under `/NOAHSARK/snapshots/`. The replicated copies under `catalog/snapobj/` are not counted, so the value equals the number of manifest records with `kind` 5. |
| 408 | 8 | u64 | `prereq_count` | Prerequisite ids listed in the manifest. |
| 416 | 4 | u32 | `tool_version` | Writer registry id in the high 8 bits, writer-defined version in the low 24 bits, as in the superblock (section 7.5). |
| 420 | 1 | u8 | `run_kind` | 1 data run, 2 repair run (Phase 2), 3 disc-close parity run (Phase 3). 0 is invalid. Health is never stored here; it lives in the disc directory. |
| 421 | 1 | u8 | `session_start_sector_valid` | 1 when `session_start_sector` is meaningful. |
| 422 | 1 | u8 | `run_flags` | Run header flags. Bit 0 `CLOSING_RUN`: this run closed the disc, so no later run is possible on it (section 7.12). Bits 1 to 7 reserved, zero. |
| 423 | 1 | u8 | `reserved_u8` | Zero. |
| 424 | 8 | u64 | `session_start_sector` | Value passed to `isoinfo -T` for this run under profile 2. Zero under profile 1. |
| 432 | 8 | u64 | `prev_run_header_lba` | LBA of the previous run header on this disc. Zero for the first run. |
| 440 | 8 | u64 | `disc_object_count` | Objects on this disc after this run. Cumulative. |
| 448 | 8 | u64 | `disc_used_sectors` | Sectors used on this disc after this run: `lba_base + run_sectors` of this run, which is the first sector above everything the disc holds. It is not the sum of `run_sectors` over the runs, and it is not the drive's next writable address, which is rounded up to 16 sectors. |
| 456 | 4 | u32 | `disc_run_index` | Index of this run on this disc. 0 for the first run. Same width and same value as `disc_run_index` in the run table record (section 11.5). |
| 460 | 4 | u32 | `reserved_u32b` | Zero. |
| 464 | 8 | u64 | `checksum_lba` | First sector of the checksum column, that is the first data sector of `checksum.bin`. |
| 472 | 8 | u64 | `parity_lba` | First sector of parity column `k+1`, that is the second data sector of `parity/p0232.bin` at the default `k`. |
| 480 | 8 | u64 | `data_span` | Sectors from `lba_base` to the last sector that step 6 of section 8.6 occupies, inclusive, as section 7.3 defines it. `L = ceil(data_span / fec_k)`. |
| 488 | 20 | u8[20] | `reserved` | Zero. |
| 508 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 507. |

Field rules. `lba_base` is LBA 0 for the first run of a disc, and the first
sector of that run's `RUN.bin` for every later run.

`run_flags` bit 0 is `CLOSING_RUN`. Bits 1 to 7 are reserved and written as
zero.

`fs_profile` equals the superblock's `fs_profile` in every run header on the
disc, the first run's and every appended run's alike. A reader that finds a run
header whose `fs_profile` differs from the superblock's refuses the run, names
both values and names the run seq.

`created_sec` is pack time, never burn time.

`payload_bytes` is the sum of `byte_len` over the layout extent records whose
`file_role` is 0. Each extent's `byte_len` is the bytes that extent carries,
so summing a fragmented object's extents yields its header plus its whole
stored payload, counted once over all its extents; a bundle is counted once.

`snapshot_count` counts only snapshot objects under `/NOAHSARK/snapshots/`, so
it equals the number of manifest records with `kind` 5.

`disc_used_sectors` is `lba_base + run_sectors` of this run. It is not the sum
of `run_sectors` over the runs and not the drive's next writable address.

The set of referenced run seqs lives in the manifest, not in the run header.
`source_run_count` bounds it.

Hash and CRC coverage. `header_crc32c` covers bytes 0 to 507.
`prev_run_header_hash` and the run table's `run_header_hash` cover the 512
header bytes only, never the 1536 zero bytes of the sector.

Reader checks. Check the magic, `version_major` and `required_feat`. Verify
`header_crc32c`. Verify `layout_hash`, `manifest_hash`, `filter_hash` and
`catalog_hash` against the bytes of the files they name.

### 7.7 The run chain

Every run header points to the previous run header on the same disc, by hash
and by LBA. The chain is the mutable state of the disc: the list of its runs,
the object count, the used sectors and the close state all come from it. Health
is never in the chain.

A reader finds the newest run by listing `/NOAHSARK/runs/` and taking the
highest `<seq>`. That is the only normal path. There is no scanning and no
fixed LBA on the normal path.

A reader walks the chain backwards by `prev_run_header_hash` and
`prev_run_header_lba` and confirms that no run is missing.

Every run carries its own copy of the catalog. An earlier copy is never
modified.

Withdrawal is known only from a later run table or from local state. A reader
must not infer a withdrawal from a run's own disc, and must not conclude
anything from the absence of a record, because no run table ever omits a burned
run.

### 7.8 Run header copies

The run header exists `m + 2` times inside its run: `RUN.bin` first, sector 0
of every parity file, and `RUN2.bin` last. Every copy is byte-identical. The
header sector of a parity file precedes the column and is not part of it.

Every run header copy occupies one whole 2048-byte sector: bytes 0 to 511 are
the header and bytes 512 to 2047 are zero. `RUN.bin` and `RUN2.bin` are
2048-byte files, and their layout records give `byte_len` 2048 and
`sector_count` 1.

### 7.9 What an append overwrites

A burned run is immutable. NoahsArk's own structures are append-only. An append
overwrites only the filesystem's own metadata blocks.

An overwritten block keeps its LBA. A sector inside an earlier run's parity
domain that no longer matches its recorded digest is filesystem metadata: its
digest is not compared, and a healer counts it as an erasure. The earlier run's
parity is never rewritten.

### 7.10 Run layout table

Purpose: the LBA extents of every file of the run, which turns a bad sector
into a named object and makes a run readable with no filesystem.

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
| 44 | 2 | u16 | `shard_bytes` | 2048. |
| 46 | 1 | u8 | `fec_scheme` | FEC scheme registry. |
| 47 | 1 | u8 | `reserved_u8` | Zero. |
| 48 | 8 | u64 | `container_len` | Total length of this container in bytes, for validation. It is the container, not an object payload; section 2.3's `payload_len` is a different field with a different meaning. |
| 56 | 8 | u64 | `lba_base` | First sector of the parity domain, as in the run header. |
| 64 | 8 | u64 | `column_sectors` | `L`. |
| 72 | 8 | u64 | `data_span` | As in the run header. |
| 80 | 8 | u64 | `checksum_lba` | First sector of the checksum column. |
| 88 | 8 | u64 | `parity_lba` | First sector of parity column `k+1`. |
| 96 | 2 | u16 | `fec_k` | Data columns. |
| 98 | 2 | u16 | `fec_m` | Parity columns. |
| 100 | 4 | u32 | `reserved_u32` | Zero. |
| 104 | 4 | u32 | `body_crc32c` | CRC-32C over the records. |
| 108 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 107. |
| 112 | | | `records` | In copy order, which is LBA order. See below. |

Extent record, 64 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | Object id, or bundle id for a bundle extent. For a fixed-name file, the hash of the file bytes under `hash_algo`. All zero for file roles 1, 5, 10, 11 and 12: the header copies, `layout.bin` itself, `checksum.bin` and the parity files. Their bytes become final only after this table is written (section 8.6); each has its own CRC, and the parity covers them. |
| 32 | 8 | u64 | `start_lba` | Absolute LBA of the first sector. |
| 40 | 8 | u64 | `byte_len` | Bytes this extent carries: object header plus stored payload for the first extent of an object, stored payload alone for a later extent. |
| 48 | 4 | u32 | `sector_count` | Sectors this extent covers. |
| 52 | 4 | u32 | `byte_off` | Byte offset inside the first sector. |
| 56 | 2 | u16 | `extent_index` | 0 for the first extent of an object. |
| 58 | 2 | u16 | `extent_flags` | bit0 last extent of this object, bit1 duplicate for locality, bit2 metadata object, that is an object that holds references and no file content: a tree, a chunklist or a snapshot object. Bits 3 to 15 reserved, zero. The name is not `flags`: the manifest record's `record_flags` (section 11.2) is a different field with a different bit 0. |
| 60 | 1 | u8 | `kind` | Object kind registry. 0 for a fixed-name file. |
| 61 | 1 | u8 | `compression` | Compression id. 0 for a fixed-name file. |
| 62 | 1 | u8 | `file_role` | 0 object. 1 `RUN.bin`. 2 `DISC.bin`. 3 `README.txt`. 4 `FORMAT.txt`. 5 `layout.bin`. 6 `manifest.bin`. 7 `filter.bin`. 8 a catalog file. 9 `pad.bin`. 10 `checksum.bin`. 11 a parity file. 12 `RUN2.bin`. |
| 63 | 1 | u8 | `column_index` | For `file_role` 11, the parity column index `k+1 .. 254`. 0 otherwise. |

Field rules. Layout records are in copy order, which is the fill order of
section 8.6 and is LBA order. A reader may rely on ascending `start_lba` and on
ascending `extent_index` within one `content_id`, and must not sort the records
itself.

A fragmented file produces several records with the same `content_id` and
increasing `extent_index`. The record with `extent_flags` bit 0 set is the
last. A reader reconstructs the object's bytes by reading these records in
increasing `extent_index` order and concatenating their `byte_len` spans; the
first extent's `byte_len` includes the object header, and each later extent's
`byte_len` is stored payload only.

`content_id` is all zero for file roles 1, 5, 10, 11 and 12, whose bytes become
final only after the table is written.

A zero-length `pad.bin` record carries `start_lba` equal to
`lba_base + data_span`, `sector_count` 0, `byte_len` 0, `byte_off` 0,
`extent_index` 0 and `extent_flags` bit 0 set, and still sorts between the last
file of step 6 and `checksum.bin`.

A parity file's record gives the LBA of its header sector. Its column starts one
sector later.

A sector inside the parity domain that no extent record covers is filesystem
metadata or free space. It is protected by the parity, and a verifier does not
compare it with a recorded digest.

The layout header repeats the column geometry of the run header, so the layout
table alone is enough to run a repair.

`extent_flags` and `record_flags` are different fields with different bit 0
meanings and must not be confused. `container_len` in the layout and manifest
containers is the container length in bytes, and is distinct from the object
header's `payload_len`.

The writer builds the layout table by reading the LBA of every file back from
the finished image.

Hash and CRC coverage. `body_crc32c` covers the records. `header_crc32c` covers
bytes 0 to 107. `content_id` in an extent record covers every byte of the named
file, under the container's `hash_algo`.

Reader checks. Verify both CRCs before using any record. Refuse a record whose
`kind` is 6.

### 7.11 Raw-LBA reading

Raw-LBA reading means reading the physical device by LBA with no filesystem
between. Reading an image file by byte offset is not raw-LBA reading.

Every read of an image is a byte-offset read: sector `s` is the bytes
`[s * 2048, s * 2048 + 2048)`.

The recovery path is:

1. Scan the first 64 MiB for the `"NADS"` magic and verify `super_crc32c`. The
   writer always places `DISC.bin` directly after `RUN.bin` in the first run.
2. Read the superblock, then read `first_run_lba` to find the first `RUN.bin`.
3. Read the run header, then read `layout.bin` at `layout_lba`.
4. Read every file by its extents. Copy order equals LBA order.
5. Follow the run chain forward: each run's `RUN2.bin` is the last file of that
   run, and the next run's `RUN.bin` follows it.

### 7.12 Close state

The format must not depend on a closed disc. Every reader path works on an open
disc.

A closing run records the close in two places: `run_flags` bit 0 of its own run
header, and `state_flags` bit 0 of this disc's record in the disc directory of
its own catalog. It does not set the superblock `sealed` flag, because the
superblock is never updated.

A reader learns that a disc is closed from any of three places: the newest run
header, the newest disc directory record for the disc, or the superblock
`sealed` flag for a disc that was sealed at its first write. Any one saying yes
is enough, and a writer then refuses an append.

### 7.13 Raw append

In raw append mode the filesystem directory is not updated. The run header, the
manifest, the filter and the layout table go inside the new run, exactly as
usual, so the LBA extents make every object readable.

A raw append writes its `RUN.bin` at `lba_base + run_sectors` of the previous
run, rounded up to a multiple of 16 sectors, so the previous header determines
the next `RUN.bin` LBA.

The disc directory marks the disc `append-raw-only` through `state_flags`
bit 1.

### 7.14 Forced capacity

A forced capacity is recorded in the superblock as `capacity_forced_sectors`,
next to the reported `capacity_sectors`, and in the disc directory. It must be
at or below the reported capacity. It applies from the first write of the disc
and must not change afterwards. `capacity_is_forced` is 1 when
`capacity_forced_sectors` is below `capacity_sectors`, and the disc directory
sets `state_flags` bit 3.

### 7.15 How a lifecycle state is recorded

| State | Where it is recorded |
|---|---|
| `blank` | Nowhere. No NoahsArk structure exists on the medium. |
| `POW-formatted` | Nowhere on the medium. The spare choice is fixed at format time and is not a field. |
| `open` | The superblock exists with `sealed` 0, and no run header on the disc sets `run_flags` bit 0. |
| `appended` | The disc holds more than one run. `disc_run_index` of the newest run header is above 0 and `run_count` in the disc directory is above 1. |
| `sealed` | Either the superblock `sealed` flag is 1, or the newest run header sets `run_flags` bit 0 and the newest disc directory record sets `state_flags` bit 0. |
| `degraded` | `health` 2 or 3 in the newest disc directory record. |
| `withdrawn` | `health` 4 in the newest disc directory record. |

The `health` byte of a disc directory record is a copied value, not the
original judgement. The operator's judgement is host state, and the operations
document states where it originates and when a later run copies the folded
value into its own disc directory record. This document states only what the
record holds.

Every transition except the recovery out of `degraded` is one-way.


---

## 8. Filesystem profiles and the volume tree

### 8.1 Profiles a reader must know

Profile 0, `oneshot`, plans one run at the first burn, with no
next-writable-address handling and no block diff at that burn. It is the
default.

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
logical-to-physical indirection that breaks the parity map.

The image length is the forced capacity rounded down to a multiple of 16
sectors.

`mkudffs` places the UDF anchors at LBA 256, at `N - 256` and at `N` in that
full-size image, where `N` is the last sector of the image. NoahsArk never
writes an anchor itself.

The used prefix of the image is LBA 0 up to and including the last sector of
`RUN2.bin`, rounded up to a multiple of 16 sectors. An open disc receives the
used prefix only, so it holds the anchor at LBA 256 and no tail anchor, and it
still mounts, because that anchor is mandatory in the standard. The tail
anchors reach the disc when an append or a close writes them. A sealed disc
receives the full-size image at its one burn: the used prefix, the unused
middle as zero sectors, and the tail anchors.

The exact UDF metadata bytes depend on the `mkudffs` version. Two writers that
hold every rule above still differ inside those bytes when their `mkudffs`
versions differ. The run header records the writer and its tool versions in
`tool_version` (section 7.6). A golden vector therefore covers the files
NoahsArk itself writes and the parity computed over the actual image, never the
UDF metadata bytes.

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
| `/NOAHSARK/runs/<seq>/RUN.bin` | The run header. | First file of its run. |
| `/NOAHSARK/runs/<seq>/layout.bin` | File order, LBA extents, column geometry, `k`, `m`. | With its run. |
| `/NOAHSARK/runs/<seq>/manifest.bin` | The manifest container, which holds the prerequisite list. | With its run. |
| `/NOAHSARK/runs/<seq>/filter.bin` | The run filter. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/CATALOG.bin` | The catalog container: the list and hash of every catalog file (section 11.8). | With its run, first file of the catalog. |
| `/NOAHSARK/runs/<seq>/catalog/filters/<seq>.bin` | Every earlier run's filter. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/manifests/<seq>.bin` | The previous `manifest.history_depth` runs' manifests. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/snapobj/<name>` | The complete snapshot object of every snapshot, one file each, or one packed `snapobj.bin`. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/snapshots.bin` | The full snapshot table. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/refs.bin` | The ref table. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/discs.bin` | The disc directory. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/runs.bin` | The run table (section 11.5). | With its run. |
| `/NOAHSARK/runs/<seq>/pad.bin` | Zero bytes that fill the data columns to `lba_base + k*L`. Always present; zero length when no fill is needed. | Last data file of its run. |
| `/NOAHSARK/runs/<seq>/checksum.bin` | The checksum column, `L` sectors. | After `pad.bin`. |
| `/NOAHSARK/runs/<seq>/parity/pNNNN.bin` | One file per parity column, `L + 1` sectors. Sector 0 is a run header copy; sectors 1 to `L` are the column. | After `checksum.bin`, in column order. |
| `/NOAHSARK/runs/<seq>/RUN2.bin` | Run header copy. | Last file of its run. |
| `/NOAHSARK/objects/<ab>/<name>` | Chunks and bundles. Shared by every run on the disc. | In fill order. |
| `/NOAHSARK/trees/<ab>/<name>` | Tree and chunklist objects. | In fill order, before the chunks. |
| `/NOAHSARK/snapshots/<name>` | Snapshot objects. | In fill order, before the trees. |

`<seq>` is the run sequence number, zero-padded to 10 decimal digits, so that
lexical order equals numeric order. A reader must not sort run directories as
plain strings without the padding.

`objects/`, `trees/` and `snapshots/` are shared by every run on the disc.

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
substitution slots. `README.txt` is at most 16 KiB; the reserve estimator
described in the operations document charges the whole cap.

A slot is written `{name}` below. The writer replaces the bytes `{`, the name
and `}` with the value, and writes nothing else in its place. **Every slot in
the text is substituted, wherever it appears.** There are **nineteen** of
them: one in part 1, `{version_minor}`; twelve in the identity block of part
2; and six in part 7, where `{fec_k}` appears four times and `{fec_m}` twice.
A writer that substituted only the identity block would leave six literal
`{fec_k}` and `{fec_m}` strings on the disc, and its `README.txt` would differ
from every other writer's. The substitution rules are:

| Slot | Value |
|---|---|
| `{version_minor}` | The `version_minor` of the superblock, in decimal. |
| `{repo_uuid}`, `{disc_uuid}` | Hyphenated lowercase uuid text. |
| `{disc_seq}` | `disc_seq` in decimal, 0-based. |
| `{label}` | The `label` bytes of the superblock, as they are, with every byte outside 0x20 to 0x7E replaced by `?`. |
| `{media_type}` | The name from the media type registry of section 2.6. |
| `{fs_profile}` | The name from the disc filesystem profile registry of section 2.6. |
| `{hash_algo}` | `blake3` or `sha2-256`, from the superblock `hash_algo`. |
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
sectors, no fixed-LBA structures and no raw areas outside this filesystem.
Every file is fixed-width, little-endian and packed. Every structure starts
with a four-byte magic and ends with a checksum.

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
/NOAHSARK/runs/<seq>/RUN.bin    run header, first file of the run
/NOAHSARK/runs/<seq>/layout.bin file order, LBA extents, column geometry
/NOAHSARK/runs/<seq>/manifest.bin   object table for that run
/NOAHSARK/runs/<seq>/filter.bin     membership filter for that run
/NOAHSARK/runs/<seq>/catalog/       tables copied from the whole repository
/NOAHSARK/runs/<seq>/pad.bin        zero fill to the end of the data columns
/NOAHSARK/runs/<seq>/checksum.bin   per-sector digests
/NOAHSARK/runs/<seq>/parity/        one file per parity column
/NOAHSARK/runs/<seq>/RUN2.bin   run header copy, last file of the run
/NOAHSARK/objects/<ab>/<name>   chunks and bundles
/NOAHSARK/trees/<ab>/<name>     tree and chunklist objects
/NOAHSARK/snapshots/<name>      snapshot objects

<seq> is the run number, ten decimal digits, zero padded. The run directory
with the highest number is the newest run, and its catalog/ directory is the
newest catalog. Read that one.

4. HOW AN OBJECT IS NAMED
-------------------------
The name of an object is the hash of its uncompressed payload bytes and
nothing else. The kind, the chunker profile, the compression and the object
header do not enter the name. The name on disc is the lowercase hex of the
multihash: two prefix bytes then the digest. 1e20 means BLAKE3-256 and 1220
means SHA-256, so the name is 68 hex characters. <ab> is the first two hex
characters of the digest, which is characters 5 and 6 of the file name.

5. HOW TO READ AN OBJECT
------------------------
An object file starts with a 64-byte header. In it, at byte offset 27, is the
compression id: 0 means none and 1 means zstd. At offset 32 is payload_len, a
little-endian unsigned 64-bit number. At offset 40 is stored_len. Skip the
64 header bytes, take the next stored_len bytes, decompress them with the
named algorithm into exactly payload_len bytes, hash the result with the
algorithm the name declares, and compare that digest with the digest in the
name. They must be equal. If they are not, the bytes are damaged; see part 7.

6. HOW TO WALK A SNAPSHOT
-------------------------
Read catalog/refs.bin, which is a table of named pointers, and take the
newest record for the name LATEST. It gives a snapshot id. Read that
snapshot object. Its header names a root tree id. Read that tree object: it
is a list of directory entries, each with a name, the POSIX metadata, and
either a tree id for a subdirectory or a list of chunk ids for a file. A file
with many chunks names one chunklist object instead, which holds the ordered
chunk ids. Concatenate the chunk payloads in order and the file is restored.
A small chunk may live inside a bundle object; catalog and manifest say
which bundle and at which byte offset.

7. HOW TO REPAIR
----------------
Each run carries Reed-Solomon parity over a contiguous range of sectors, the
parity domain, which starts at LBA 0 for the first run of the disc. The
domain is cut into {fec_k} equal columns of L sectors each; L is in the run
header. Stripe i is sector i of every column. checksum.bin is one more
column: its sector i holds an 8-byte digest of each of the {fec_k} data
sectors of stripe i, so a bad sector can be found. The {fec_m} files under
parity/ are the parity columns; sector 0 of each is a copy of the run header
and the column starts one sector later. Any {fec_k} of the {fec_k} data plus
{fec_m} parity sectors of one stripe reconstruct the rest. FORMAT.txt gives
the field arithmetic.

8. WHERE THE BYTE LAYOUTS ARE
-----------------------------
FORMAT.txt in this directory holds the offset, size, type, name and meaning
of every field of every structure, the registries, the magic values and the
chunking constants. This file and that file together are enough to extract
one file from this disc by hand, with a hex editor and no NoahsArk software.

9. THE TEN FORMAT RULES
-----------------------
1. Every integer is little-endian. No big-endian field exists.
2. Every type is fixed width: u8, u16, u32, u64, i32, i64.
3. Every structure is packed, and every gap is a named reserved field of
   zero bytes.
4. Every structure starts with magic, then version_major, then version_minor.
5. A reader refuses an unknown version_major and ignores an unknown
   version_minor.
6. A reader refuses an unknown bit in required_feat and ignores an unknown
   bit in optional_feat.
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
separator, and at most 64 KiB. The major 1 minor 0 text is 41,097 bytes long in
825 lines. A writer that produces a different length for minor 0 has a defect.

`FORMAT.txt` holds seven parts in a fixed order: FORMAT RULES, REGISTRIES with
eleven registries in a stated order, STRUCTURES with thirty named structures in
a stated order, MAGIC VALUES, CHUNKING CONSTANTS, FILTER QUERY RULE, CHECKSUM
PARAMETERS.

A structure added in a later version is appended to the part 3 list and is
never removed while the major version holds.

### 8.6 Fill order inside a run

The writer places files in this order under every profile:

1. `RUN.bin`, the run header.
2. In the first run of a disc only: `DISC.bin`, `README.txt`, `FORMAT.txt`.
3. `layout.bin`, `manifest.bin`, `filter.bin`.
4. The catalog files, `CATALOG.bin` first, then the rest in the entry order of
   section 11.8.
5. Snapshot objects, then tree objects and chunklists, contiguous.
6. Bundles and chunks, in path order.
7. `pad.bin`, always, with zero length when the data columns need no fill.
8. `checksum.bin`, the checksum column.
9. The parity files, in column order.
10. `RUN2.bin`, the run header copy.

Steps 1 to 7 are the data files. They form the parity domain, together with
every filesystem block and free sector between them. Steps 8 and 9 cover it.
Step 10 sits outside the domain, at the highest LBA of the run, so that a copy
of the header survives damage at either end.

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
3. each chunklist object directly after the tree entry that references it, and
   a chunklist that a spilled TLV references directly after the entry that
   holds that TLV.

Step 6 emits, for each regular-file entry in entry order, that file's chunks in
file order, that is in the order of its inline chunk id array or of its
chunklist's entries. Chunks that a spilled TLV added come after the file's
content chunks, in TLV order.

An object is emitted once, at its first occurrence in the walk. A bundle is
placed at the position of its first chunk, and the whole bundle file is written
there.

An object that this run does not store, because it is a prerequisite or because
the target disc already holds it, is not emitted and does not move the position
of anything else.

An object that the target disc already holds is never written again. The packer
treats it as present, gives it no manifest record in this run, and references
the earlier run as a prerequisite. A duplicate written for locality is written
only onto a different disc.

A file whose content depends on an LBA, a hash or a size known only after
layout is a placeholder: a file of its final size filled with zero bytes,
rewritten in place later. Every placeholder has a known final size.

The writer copies files into the mount one at a time, single-threaded, in fill
order. Copy order equals physical LBA order.

Where the File Entry blocks sit:

1. `data_span` ends at the last sector that step 6 occupies. Under profile 0
   and profile 1 that sector is the File Entry block of the last file of step 6.
   Under profile 2 it is the last data sector of that file. Steps 7 to 10 are
   outside `data_span`.
2. The data sectors of `pad.bin` fill the rest of the parity domain exactly, so
   its own File Entry block is the first sector at or after `lba_base + k*L`,
   outside the domain.
3. The File Entry blocks of `checksum.bin` and of every parity file lie outside
   the domain too. No column file counts its own File Entry block as part of
   its column.

Invariants:

1. The extent of every file is fixed and read back from the image before any
   hash that names that file is computed. No file moves after its extent is
   read back.
2. `pad.bin` always exists. Its data sectors fill the parity domain exactly to
   `lba_base + k*L`, so its length in sectors is `k*L - data_span`, and zero
   when that count is 0. A zero-length `pad.bin` still has one layout record.
3. The first data sector of `checksum.bin` is at or above `lba_base + k*L`, and
   every column file is contiguous.
4. `manifest.bin`, `DISC.bin`, `catalog/runs.bin`, `catalog/discs.bin`,
   `catalog/CATALOG.bin`, `layout.bin` and the run header become final in that
   order, each from values already final.
5. The checksum column and the parity are computed over the finished domain,
   which holds the final bytes of `RUN.bin` and of every other file in it.

Metadata objects, that is trees, chunklists and snapshot objects, are placed
together, so a connectivity check over one run costs one seek and one
sequential read.

### 8.7 Name and path budget

| Item | Limit |
|---|---|
| Object name | 68 characters, charset `[0-9a-f]`. Never stripped. |
| Any on-disc name | at most 126 characters |
| Any on-disc path | under 220 characters |
| Forbidden characters in a name | `< > : " / \ \| ? *`, control characters, a trailing space, a trailing dot, and the reserved device names `CON PRN AUX NUL COM1-9 LPT1-9` |

Never rely on case to distinguish two objects.

---

## 9. Capacity invariants

Three names carry the whole capacity policy. No second formula for them exists.

- `data_budget`: the sectors that object data and the runs' own tables may
  occupy on the disc, across all its runs. `data_budget` charges `data_span`:
  every sector from the start of the run's data to the last data sector before
  `pad.bin`, File Entry blocks and free sectors inside the domain included.
- `fill_limit_sectors`: the first LBA that no run may reach. The superblock
  records it.
- `reserve`: `capacity_forced - data_budget`.

Five invariants are normative. Any writer that holds them conforms, whatever
arithmetic it used to choose the numbers.

1. No run, its parity and its header copies included, reaches
   `fill_limit_sectors`.
2. `fill_limit_sectors` is at most `capacity_forced`, and it is defined as
   `capacity_forced - safety_margin - spare_area`, where `safety_margin` is
   `ceil(capacity_forced * (1 - fill_ratio))` and `spare_area` is
   `ceil(spare_reserve_bytes / 2048)` on a `spare:min` disc, the same
   expression on a `spare:default` disc with that mode's larger
   `spare_reserve_bytes`, and 0 on a sealed disc. That is the definition, and
   it holds whether or not an override is set.
3. `data_budget` is a whole number of stripes of `k` data sectors:
   `data_budget mod k == 0`.
4. `reserve` equals `capacity_forced - data_budget` by definition. The
   superblock records the values the writer used: `reserve_computed_sectors` is
   what the writer's estimator produced, `reserve_forced_sectors` and
   `reserve_extra_sectors` are the overrides as given, and
   `fill_limit_sectors` is the value of invariant 2. A reader takes
   `fill_limit_sectors` from the superblock and never recomputes it.
5. The sum of the sectors that every run of the disc occupies, plus the sectors
   the disc still holds free below `fill_limit_sectors`, never exceeds
   `fill_limit_sectors`. A later append reads the recorded values and never
   derives new ones.

The inputs of a writer's reserve estimator are heuristics. They are not part of
the on-disc contract, and a reader never uses them.

---

## 10. Forward error correction

### 10.1 Parity layout

The FEC scheme is `rs255-gf8`: a systematic erasure code over GF(2^8) with `k`
information shards and `m` parity shards per stripe.

A shard is exactly one 2048-byte sector.

A stripe is 255 shards: `k` data, 1 checksum and `m` parity. The code covers
the `k` data and the `m` parity shards. The checksum shard is outside the code.

Format version 1 fixes `k = 231` and `m = 23`. A version 1 writer writes no
other pair and a version 1 reader refuses any other pair. Both values are still
recorded, in the run header and in the layout header.

`L = ceil(data_span / k)`.

Data column `c` occupies LBA `[lba_base + c*L, lba_base + (c+1)*L)` for
`c = 0 .. k-1`.

The parity domain is the contiguous LBA range `[lba_base, lba_base + k*L)`.
Filesystem metadata and free sectors inside it are protected too.

Column `k`, the checksum column, is the `L` sectors of
`runs/<seq>/checksum.bin`, starting at `checksum_lba`, which is at or above
`lba_base + k*L`.

Column `c` for `c = k+1 .. 254` is sectors 1 to `L` of
`runs/<seq>/parity/pNNNN.bin`, where `NNNN` is `c` in decimal zero-padded to
four digits. Sector 0 of that file is a run header copy. The file is `L + 1`
sectors.

Every column file is contiguous. The layout table records the extent of each,
and the run header records `checksum_lba` and `parity_lba`.

Stripe `i` is sector `i` of every column, for `i = 0 .. L-1`.

Encoding is byte-column-wise: take one byte from each of the `k` data sectors
at the same byte offset, in column order 0 to `k-1`, and produce `m` parity
bytes at that offset, column `k+1` first, for all 2048 byte offsets.

**The burst bound.** The maximum correctable single burst is `m * L` sectors,
and the bound holds inside the data columns only. A burst longer than `m * L`
sectors puts more than `m` erasures into one stripe, which section 10.5 refuses
to decode.

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

Purpose: an 8-byte digest of every data sector of a stripe, so a damaged sector
can be located before the code is applied.

Sector `i` of the checksum column holds the digests of the `k` data sectors of
stripe `i`, and of no other sector. There is no offset and no wrap.

The digest is the first 8 bytes of the 32-byte BLAKE3-256 digest of the sector,
computed over the 2048 bytes as they lie on the medium.

Checksum sector header, 16 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NACS"`. |
| 4 | 4 | u32 | `stripe_index` | `i`, the stripe whose data digests follow. Equals the sector's own index inside the column. |
| 8 | 2 | u16 | `digest_count` | `k`. 231 in version 1. |
| 10 | 1 | u8 | `digest_bytes` | 8. |
| 11 | 1 | u8 | `hash_algo` | 0x1e, BLAKE3. |
| 12 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 11. |
| 16 | 1848 | u8[1848] | `digests` | `k` digests, in data column order 0 to `k - 1`. |
| 1864 | 184 | u8[184] | `reserved` | Zero. |

Field rules. `digest_count` equals `k`. `digest_bytes` is 8. The digests are in
data column order 0 to `k - 1`. The 184 bytes after the 1864-byte header and
digest area are zero.

The checksum column is outside the Reed-Solomon code. The parity neither covers
it nor reconstructs it.

Hash and CRC coverage. `header_crc32c` covers bytes 0 to 11. Each 8-byte digest
covers the 2048 bytes of one data sector.

Reader checks. Check the magic and verify `header_crc32c`. When a checksum
sector is unreadable or its header CRC fails, check that stripe's data sectors
through the content ids of the objects the layout table maps them to, and treat
a failing sector as an erasure. A sector that no extent record covers is then
treated as readable.

A silently wrong digest makes a verifier treat a good data sector as an
erasure. Reconstruction returns the same bytes and the content id passes, and
the verifier reports the checksum sector as damaged. No data is lost.

A parity sector has no digest. An unreadable parity sector is an erasure from
the start.

### 10.4 Header replication and parity files

The run header exists `m + 2` times: `RUN.bin` first, sector 0 of every parity
file, and `RUN2.bin` last. The header sector of a parity file precedes the
column and is not part of it.

The parity geometry is derivable from any header copy plus the layout table:
`lba_base`, `column_sectors`, `fec_k` and `fec_m` determine every data column
boundary, `checksum_lba` and `parity_lba` locate the first two column files,
and the layout table locates every parity column. A tool that lost every header
copy still knows `k` and `m`, because version 1 fixes them, and finds `L` by
the `"NACS"` magic of the first checksum sector.

### 10.5 Decode rule

Take the `k` rows of `[I_k ; C]` that correspond to `k` surviving shards,
invert that `k x k` matrix over the field, and multiply it by the surviving
shard bytes at each byte offset. The result is every data shard. The missing
parity shards are then re-encoded.

A stripe with more than `m` erasures is not decodable, and the decoder must say
so.

The single-parity retry is bounded and normative. Let `E` be the erasures the
stripe already has. The decoder tries each single parity sector of the stripe
in turn as one more erasure, which is at most `m - E` attempts, and it makes an
attempt only while `E + 1 <= m`. An attempt succeeds when every reconstructed
data sector matches its digest, or, with no usable checksum sector, when every
object that the layout table maps into the stripe passes its content id. If no
single-parity attempt decodes, the stripe is undecodable. The decoder must not
try pairs or larger subsets.

### 10.6 Uncovered sectors and the append bound

A sector inside the parity domain that no extent record covers is filesystem
metadata or free space. Its digest is not compared, and a healer counts a
mismatching one as an erasure. The earlier run's parity is never rewritten.

Under profile 1 an append must not rewrite more than `floor(m / 2)` blocks that
fall into one stripe of any earlier run. The block diff checks this against the
earlier runs' layout tables before it writes.

### 10.7 Health status values

The headline health metric is the RS margin: `m` minus the worst-stripe erasure
count, as a percentage of `m`. The disc directory records it in
`rs_margin_percent`.

Every percentage this document stores is rounded down to a whole percent and
clamped to the range 0 to 100. That rule covers `rs_margin_percent` and
`spare_remaining_percent` (section 11.6).

Status values are `HEALTHY`, `DEGRADED` when any sector needed RS correction,
`CRITICAL` when any stripe used more than half its parity, `FAILED` when any
object is unrecoverable, and `UNKNOWN` where no record exists.

What a local cache reports for a disc that carries no health record of its own
is host behaviour. The operations document states it.


---

## 11. Filters, manifests and the catalog

### 11.1 Run filter

Purpose: a membership filter over every object the run stores, so that a
negative answer across every run is a proof of absence.

The filter type is BinaryFuse16 with 3-wise fused segments and 16-bit
fingerprints.

Filter membership is every object the run stores as an object of its own: every
chunk, bundled chunks under their own ids, every bundle, every chunklist, every
tree, and every snapshot object under `/NOAHSARK/snapshots/`. Catalog copies
are not members.

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

**Key derivation.** The filter key of an object is the little-endian u64 of
bytes 0 to 7 of its digest. The multihash prefix does not enter the key. Two
objects that share their first 8 digest bytes share a key; that is a false
positive at rate 2^-64 per pair, which the manifest confirmation of section
11.9 absorbs.

**Query rule.** The query is normative. A filter conforms when this function
returns true for every key that was inserted.

```
mix(h)   : h ^= h >> 33; h *= 0xff51afd7ed558ccd; h ^= h >> 33;
           h *= 0xc4ceb9fe1a85ec53; h ^= h >> 33; return h       # 64-bit, wrapping
contains(key):
    h  = mix(key + seed)                                      # wrapping add
    f  = u16(h ^ (h >> 32))
    h0 = u32( (h * segment_count_length) >> 64 )              # high half of the 128-bit product
    h1 = h0 + segment_length
    h2 = h1 + segment_length
    h1 = h1 ^ (u32(h >> 18) & segment_length_mask)
    h2 = h2 ^ (u32(h)       & segment_length_mask)
    return f == fingerprints[h0] ^ fingerprints[h1] ^ fingerprints[h2]
```

`fingerprints` is the u16 array of the container body, indexed from 0.
`segment_length` is a power of two, `segment_length_mask = segment_length - 1`,
`segment_count_length = segment_count * segment_length`, and
`fingerprint_count = (segment_count + 2) * segment_length`. The reader takes
every parameter from the container header. It never recomputes them.

Construction. Only the query rule binds every reader and writer: a reader takes
every parameter from the container header and never recomputes them, so any
filter body that passes the query rule for its own `fingerprints` is a
conforming filter. Steps 1 to 5 below are the reference construction, and they
are normative for golden-vector conformance. `n` is the key count. Duplicate
keys are removed first, and `key_count` records the number of distinct keys.

*Step 1, geometry.* For `n` at or above 2:

```
segment_length      = min( 1 << floor( ln(n) / ln(3.33) + 2.25 ), 262144 )
size_factor         = max( 1.125, 0.875 + 0.25 * ln(1000000) / ln(n) )
capacity            = round( n * size_factor )              # half up
segment_count       = ceil( capacity / segment_length ) - 1
if segment_count < 1:  segment_count = 1
fingerprint_count   = (segment_count + 2) * segment_length
segment_count       = ceil( fingerprint_count / segment_length ) - 2
if segment_count < 1:  segment_count = 1
fingerprint_count   = (segment_count + 2) * segment_length
segment_length_mask = segment_length - 1
segment_count_length= segment_count * segment_length
```

For `n` of 0 or 1 the writer sets `segment_length` to 4, `segment_count` to 1
and `fingerprint_count` to 12. The two logarithms are natural logarithms
evaluated in IEEE 754 binary64; the flooring, the rounding and the ceilings
make the result an integer that any conforming implementation reproduces.

Step 2, positions. For a key `x` and the current seed, `h`, `f`, `h0`, `h1` and
`h2` are exactly the five values that the query rule computes. The three
positions of `x` are `h0`, `h1` and `h2`, and its fingerprint is `f`.

Step 3, peeling. Build the count and the XOR accumulator of every position: for
each key add 1 to `count[p]` and XOR the key into `xorsum[p]`, for each of its
three positions. Then peel:

1. Push every position `p` with `count[p] == 1` onto a work list, in ascending
   `p` order.
2. Take the lowest position `p` from the work list. If `count[p]` is not 1,
   discard it and continue. Otherwise the key at `p` is `xorsum[p]`. Push the
   pair `(key, p)` onto the peel stack.
3. For each of that key's three positions `q`, subtract 1 from `count[q]` and
   XOR the key out of `xorsum[q]`. When `count[q]` becomes 1, append `q` to the
   work list.
4. Repeat from step 2 until the work list is empty.

The attempt succeeds when the peel stack holds all `n` keys. Taking the lowest
position first makes the stack order deterministic, which makes the fingerprint
array deterministic.

Step 4, fingerprints. Set every entry of `fingerprints` to 0. Then pop the peel
stack, last in first out. For the pair `(key, p)`, with the key's three
positions held as the indexed triple `(h0, h1, h2)` and its fingerprint `f`,
find the lowest index `i` in `{0, 1, 2}` with `(h0, h1, h2)[i] == p`, and let
`a` and `b` be the triple's two other indices in ascending index order:

```
fingerprints[p] = f ^ fingerprints[(h0, h1, h2)[a]] ^ fingerprints[(h0, h1, h2)[b]]
```

The rule is total, because it always picks the lowest matching index for `i`
and keeps the triple's other two positions, by index, for `a` and `b`. When a
position value repeats across `a` and `b`, the two slots are the same and the
value XORs itself out.

Step 5, the seed search. The writer starts at `seed` 0 and increments by 1
after every failed attempt. It makes at most 100 attempts. When no seed in
`0 .. 99` peels, the writer does not write the filter, exits with code 2, and
names the run and the key count.

A Bloom filter is used only in memory and is never written to a disc.

Hash and CRC coverage. `header_crc32c` covers bytes 0 to 75. `body_crc32c`
covers the fingerprint array. Each filter blob's hash is recorded in
`CATALOG.bin`, and the run header records this run's filter hash.

Reader checks. Check the magic, `version_major` and `required_feat`. Verify
`header_crc32c` and `body_crc32c`. Take every parameter from the header. Treat
a positive as a hint and a negative as a proof.

### 11.2 Run manifest

Purpose: the exact object table of one run, plus the prerequisite list, the
bundle table, the source-run set, the duplication counters and the split
records.

The manifest is a chunked table-of-contents container: a 4-byte chunk id, an
8-byte offset, and a sentinel at the end. An old reader skips an unknown chunk
id.

The manifest has exactly the same membership as the filter, one record per
member.

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
| 48 | 8 | u64 | `container_len` | Total length of this container in bytes, for validation. It is the container, not an object payload; section 2.3's `payload_len` is a different field with a different meaning. |
| 56 | 4 | u32 | `body_crc32c` | CRC-32C over every byte from offset 64 to the end of the container: the TOC, the sentinel and every chunk. |
| 60 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 59. |
| 64 | `16 * (chunk_count + 1)` | | `toc` | TOC entries, then a sentinel. |

TOC entry, 16 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `chunk_id` | Four ASCII bytes. |
| 4 | 4 | u32 | `reserved_u32` | Zero. Keeps `offset` aligned. |
| 8 | 8 | u64 | `offset` | Byte offset of the chunk from the container start. |

The sentinel entry has `chunk_id` 0 and `offset` equal to `container_len`.
Every `offset`, the sentinel's included, is a multiple of 8: the container
"maps directly into memory" only when every u64 field of every chunk lands on
an 8-byte boundary, so a writer pads a chunk's end with zero bytes up to the
next multiple of 8 before it starts the next chunk.

Chunk ids in version 1:

| Chunk id | Content |
|---|---|
| `"FANO"` | Fan-out table: 256 or 65536 cumulative u32 counts. Mandatory. |
| `"RECS"` | The sorted manifest records. Mandatory. |
| `"BNDL"` | The bundle table: the ids of every bundle in this run, 32 bytes each, sorted ascending. Mandatory; zero length when the run holds no bundle. |
| `"PREQ"` | The prerequisite list (section 11.3). Authoritative. Mandatory; zero length when nothing is missing. |
| `"SRCR"` | The set of run seqs that this run references: u64 values, sorted ascending. Mandatory; zero length when the run references no other run. |
| `"DUPS"` | Duplicate accounting: four u64 values, in the field order that this section gives below. Mandatory. |
| `"SPLT"` | Split records: files whose chunks continue on another run; see the operations document. Mandatory; zero length when no file is split. |
| `"BMAP"` | Per-snapshot reachability bitmaps. **Reserved. No payload is defined in version 1.** |
| `"RIDX"` | Reverse index by LBA. **Reserved. No payload is defined in version 1.** |

The seven mandatory chunks appear in every version 1 manifest, in the order
above, so a reader finds each one at a fixed TOC index. A zero-length chunk
has an `offset` equal to that of the next chunk.

`"BMAP"` and `"RIDX"` reserve a chunk id, an optional feature bit and a TOC
position, and nothing else. A version 1 writer must not emit either chunk and
must never set `OPT_BITMAPS` or `OPT_REVIDX`.

Manifest record, 64 bytes, sorted ascending by `content_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | The object id. |
| 32 | 8 | u64 | `uncompressed_size` | Payload bytes after decompression. |
| 40 | 8 | u64 | `container` | With `record_flags` bit0 set: the 0-based index of the bundle in the `"BNDL"` table. Otherwise: the absolute LBA of the first sector of the object's file. |
| 48 | 8 | u64 | `offset` | With `record_flags` bit0 set: the byte offset of the chunk's stored bytes from the first byte of the bundle file. Otherwise: the byte offset of the object header from the start of sector `container`, normally 0. |
| 56 | 2 | u16 | `record_flags` | bit0 the object lies inside a bundle, bit1 duplicate for locality, bit2 metadata object, that is a tree, a chunklist or a snapshot object, bit3 spilled TLV payload. Bits 4 to 15 reserved, zero. The name is not `flags`: the layout extent record's `extent_flags` (section 7.10) is a different field with a different bit 0. |
| 58 | 1 | u8 | `hash_algo` | Multicodec code. |
| 59 | 1 | u8 | `digest_len` | 32. |
| 60 | 1 | u8 | `kind` | Object kind registry. |
| 61 | 1 | u8 | `compression` | Compression id. |
| 62 | 2 | u16 | `reserved_u16` | Zero. |

Field rules. Manifest records are sorted ascending by `content_id`.

A lookup reads `fanout[b-1]` and `fanout[b]` and binary-searches that slice.
`fanout[-1]` is 0. With `fanout_bits` 8, `b` is byte 0 of the `content_id`.
With `fanout_bits` 16, `b` is `byte0 * 256 + byte1`.

A writer must use a 16-bit fan-out once a run holds more than 1,000,000
objects, and marks it with `FEAT_FAN16`.

A snapshot object's bytes legitimately appear at two LBAs in one run. Only the
copy under `/NOAHSARK/snapshots/` has a manifest record. A reader must not
treat the second copy under `catalog/snapobj/` as an inconsistency.

A reader that finds `kind` 6 in a manifest record refuses the record and names
the structure.

Bundle table entry, 32 bytes, in the `"BNDL"` chunk:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `bundle_id` | Content id of one bundle that this run stores. |

Split record, 48 bytes, in the `"SPLT"` chunk:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `chunklist_id` | The chunklist of the split file. |
| 32 | 8 | u64 | `other_run_seq` | One other run that holds a part of this file. |
| 40 | 4 | u32 | `part_index` | 0-based part number of **this** run's part. |
| 44 | 4 | u32 | `part_count` | Total parts of the file. |

A file split over `part_count` runs gives each of those runs `part_count - 1`
split records, one for every other part. Split records are sorted by
`chunklist_id` ascending, then `other_run_seq` ascending. The chunks of the
other part are prerequisites of each run.

The `"DUPS"` chunk holds exactly four u64 values, in this order and no other.
This table is the one definition of that order.

| Order | Field | Meaning |
|---:|---|---|
| 1 | `duplicated_bytes` | Bytes written again for locality. |
| 2 | `duplicated_objects` | Objects written again. |
| 3 | `unique_bytes` | Bytes that exist only in this run. |
| 4 | `dedup_saved_bytes` | Bytes not written because an older run supplies them. |

The chunk is therefore 32 bytes long. The run header repeats
`duplicated_bytes` in `duplicate_bytes`. Every duplicated object's manifest
record sets `record_flags` bit 1.

Hash and CRC coverage. `header_crc32c` covers bytes 0 to 59. `body_crc32c`
covers every byte from offset 64 to the end of the container, that is the TOC,
the sentinel and every chunk. The run header records the manifest hash over
every byte of `manifest.bin`.

Reader checks. Check the magic, `version_major` and `required_feat`. Verify
both CRCs. Skip an unknown chunk id. Refuse the manifest when `FEAT_FAN16` is
set and the reader does not implement it.

### 11.3 Prerequisite list

Purpose: the object ids that this run references but does not store, with the
run seq and disc seq that hold each.

Prerequisite membership is by direct reference, not by reachability. `"PREQ"`
holds every id that a tree, a chunklist or a snapshot object stored in this run
references directly, and that this run does not store as an object of its own.
The list is one edge deep.

A snapshot's `parent` id is never a prerequisite, because every catalog carries
the complete snapshot object of every snapshot.

A `root_tree` id that a stored snapshot names is a prerequisite when this run
does not store that tree.

An object that an earlier run of the same disc stores is a prerequisite like
any other, with its own `run_seq`.

`"SRCR"` is exactly the set of distinct `run_seq` values that appear in
`"PREQ"`, sorted ascending. `source_run_count` in the run header is its size,
and `prereq_count` is the number of `"PREQ"` records.

A prerequisite never names a run whose `run_status` is 3.

The algorithm of a prerequisite `content_id` is the `hash_algo` of the run
header of `run_seq`. The list lives in `"PREQ"` and nowhere else.

Record, 48 bytes, sorted ascending by `content_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | The referenced object. |
| 32 | 8 | u64 | `run_seq` | The run that holds it. |
| 40 | 8 | u64 | `disc_seq` | The disc that holds that run. |

### 11.4 Snapshot objects and the simple tables

The catalog replicates the complete snapshot objects of every snapshot in the
repository. Tree objects are not replicated.

The snapshot table, the ref table, the run table and the disc directory are
replicated in full on every run.

The snapshot table, the ref table, the run table and the disc directory share
one container shape. Header, 64 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NAST"` snapshot table, `"NARF"` ref table, `"NART"` run table, `"NADD"` disc directory. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 16 | u8[16] | `repo_uuid` | The repository. |
| 40 | 8 | u64 | `record_count` | Records that follow. |
| 48 | 2 | u16 | `record_size` | 136 snapshot table, 96 ref table, 128 run table, 160 disc directory. |
| 50 | 1 | u8 | `hash_algo` | Multicodec code of every digest in the records that has no `hash_algo` of its own. For the run table, the algorithm of `run_header_hash`. For the disc directory, the algorithm of `super_hash`. |
| 51 | 1 | u8 | `digest_len` | 32. |
| 52 | 4 | u32 | `reserved_u32` | Zero. |
| 56 | 4 | u32 | `body_crc32c` | CRC-32C over the records. |
| 60 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 59. |
| 64 | `record_count * record_size` | | `records` | Sorted, fixed-width. |

Records are sorted, so a reader binary-searches them without an index. The
sort key of each table:

| Table | Sort key, in order |
|---|---|
| Snapshot table | `generation` ascending, then `snapshot_id` bytes ascending. |
| Ref table | `name` bytes ascending, unsigned, over `name_len` bytes; then `time_sec` ascending; then `time_nsec` ascending; then `run_seq` ascending; then `snapshot_id` bytes ascending. Many records share one name, and the five keys together are total (section 6.18). |
| Run table | `run_seq` ascending. |
| Disc directory | `disc_seq` ascending. |

The snapshot table uses the record of this section, the ref table the ref
record of section 6.18, the run table the record of section 11.5, and the
disc directory the record of section 11.6.

Snapshot table record, 136 bytes, sorted by `generation` then by `snapshot_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `snapshot_id` | Content id of the snapshot object. |
| 32 | 32 | u8[32] | `parent_id` | Parent snapshot id. Zero for a root. |
| 64 | 32 | u8[32] | `root_tree` | Root tree id. |
| 96 | 8 | u64 | `generation` | 1 + parent generation. |
| 104 | 8 | i64 | `time_sec` | Snapshot time. |
| 112 | 8 | u64 | `reachable_object_count` | Objects reachable, as section 6.15 counts them. |
| 120 | 8 | u64 | `first_run_seq` | The run that first held the snapshot object. |
| 128 | 1 | u8 | `flags` | bit0 the snapshot is complete on this set: the connectivity check, defined in the operations document, found every reachable object in this run or in an earlier run when the packer wrote this table. Clear when the check was not run or found a missing object. |
| 129 | 1 | u8 | `hash_algo` | Multicodec code of `snapshot_id` and `root_tree`. |
| 130 | 1 | u8 | `parent_hash_algo` | Multicodec code of `parent_id`. 0 for a root. |
| 131 | 1 | u8 | `reserved_u8` | Zero. |
| 132 | 4 | u32 | `reserved_u32` | Zero. |

`catalog/snapobj/<name>` is the complete object file, byte for byte as
`/NOAHSARK/snapshots/<name>` holds it, including the 64-byte common object
header.

Two digests describe a replicated snapshot object. The name is the content id
of the uncompressed payload. `file_hash` in `CATALOG.bin` is the hash of the
whole file and does not equal the name.

A writer may pack the snapshot objects into one `catalog/snapobj.bin`.
`snapobj.bin` is an ordinary bundle object with `kind` 2 and `compression` 0,
and each index entry's `content_id` is the content id of the snapshot object
whose payload that entry names. It has `catalog_role` 7 and is not a member of
the run's filter or manifest. `CATALOG.bin` records the form in `snapobj_form`.

The members of `snapobj.bin` are laid out in ascending `content_id` order, which
is the order of its own index entries, and every index entry carries
`compression` 0. Section 6.3's path order does not apply, because a snapshot
object has no path. Two conforming writers with the same snapshot set therefore
produce the same `snapobj.bin` bytes.

Hash and CRC coverage. In every simple table container `header_crc32c` covers
bytes 0 to 59 and `body_crc32c` covers the records.

Reader checks. Check the magic against the expected table. Check
`version_major` and `required_feat`. Verify both CRCs. Binary-search the
records by the sort key of the table.

### 11.5 Run table

Purpose: the record of every run that was burned, with its disc, its LBA range
and its verification status.

Record, 128 bytes, sorted by `run_seq`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u64 | `run_seq` | The run, 1-based. |
| 8 | 8 | u64 | `disc_seq` | The disc that holds it, 0-based. |
| 16 | 16 | u8[16] | `disc_uuid` | That disc's uuid. |
| 32 | 8 | u64 | `lba_base` | As in the run header. |
| 40 | 8 | u64 | `run_sectors` | As in the run header. |
| 48 | 32 | u8[32] | `run_header_hash` | Hash of the 512 run header bytes (section 2.8), under the container's `hash_algo`. All zero in the record of the run that **carries** this table: that header is written after the table (section 8.6), so its hash is not yet known. The next copy of the table, written by the next run, fills the value in. Every other record of this table carries the real hash. |
| 80 | 8 | i64 | `created_sec` | Pack time, as in the run header (section 7.6). Not the burn time. |
| 88 | 4 | u32 | `disc_run_index` | Index of the run on its disc. 0 for the first run of a disc. |
| 92 | 1 | u8 | `run_kind` | As in the run header: 1 data, 2 repair, 3 disc-close parity. |
| 93 | 1 | u8 | `run_hash_algo` | Multicodec code of every object id in that run, copied from its run header. It names no digest inside this record. |
| 94 | 1 | u8 | `run_fs_profile` | Disc filesystem profile of that run's disc. |
| 95 | 1 | u8 | `run_status` | 1 verified, 2 burned but not yet verified, 3 withdrawn by the writer after a failed verify. 0 is invalid. |
| 96 | 8 | u64 | `object_count` | As in the run header. |
| 104 | 24 | u8[24] | `reserved` | Zero. |

The container header is the simple table container of section 11.4, with
magic `"NART"` and `record_size` 128.

| `run_status` | Meaning | When the writer sets it |
|---:|---|---|
| 1 `verified` | The run was burned and `verify` passed. | After a successful `verify`. |
| 2 `unverified` | The run was burned, and `verify` has not passed yet. | For the run that carries the table, which is written before that run is burned (section 8.6) and therefore also carries a zero `run_header_hash`. Also for an earlier run that is burned but not yet verified. |
| 3 `withdrawn` | The writer explicitly withdrew the run after a failed verify. Its objects are on the medium but no reader may rely on them. | Only after `verify` failed and the writer decided to re-burn the data as a new run. |

Field rules. The run table holds one record for every run that was burned,
whatever its state, plus the record of the run that carries the table. No run
is ever omitted.

`run_status` 0 is invalid.

The record of the run that carries the table holds a zero `run_header_hash` and
`run_status` 2. The next copy fills them in.

A later copy of the run table differs from an earlier one only by records added
at its end and by two fields of an existing record: `run_status`, from 2 to 1
or from 2 to 3 and never back, and `run_header_hash`, from zero to the real
hash and never back and never to a different value. Any other difference is
damage.

A reader that sees `run_header_hash` all zero takes the hash from the run
header itself or from the next run's `prev_run_header_hash`, and must not treat
the zero as a verification failure.

A reader that holds two copies of the run table prefers the one whose container
lies in the higher `run_seq`.

Withdrawal is explicit and is never inferred from absence. A `run_seq` is never
reused. A failed run leaves its record with `run_status` 3, and the re-burn is a
new run with the next `run_seq`.

A withdrawn run keeps its filter and manifest copies in later catalogs, and
`filters_kept` still counts it.

A withdrawn run never confirms a dedup query, is never a prerequisite target,
is never selected as a source run, and is ignored by a restore planner.

Every local ref record whose `run_seq` names a withdrawn run returns to
`run_seq` 0 by a newly appended local record. No on-disc structure changes.

### 11.6 Disc directory

Purpose: the list of every disc of the repository, with the chained superblock
hash that proves the ordering of the set and detects a substituted disc.

The disc directory lists every disc of the repository.

Record, 160 bytes, sorted by `disc_seq`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 16 | u8[16] | `disc_uuid` | The disc. |
| 16 | 8 | u64 | `disc_seq` | Sequence number. |
| 24 | 32 | u8[32] | `super_hash` | Hash of that disc's superblock. |
| 56 | 8 | u64 | `capacity_sectors` | Reported capacity. |
| 64 | 8 | u64 | `capacity_forced_sectors` | Forced capacity, section 7.14. |
| 72 | 8 | u64 | `used_sectors` | Sectors used on the disc: `lba_base + run_sectors` of its newest run, the same value that run's `disc_used_sectors` holds (section 7.6). Not a sum over the runs, and not the drive's next writable address. |
| 80 | 8 | i64 | `first_burn_sec` | Pack time of the disc's first run, the same value that disc's superblock holds in `created_sec` (section 7.5). Not the burn time, which is unknown when these bytes are hashed; the actual burn time lives only in the local state log (section 7.6; see the operations document). |
| 88 | 8 | i64 | `last_verify_sec` | Last verification time. 0 when never verified. |
| 96 | 4 | u32 | `run_count` | Runs on the disc. |
| 100 | 1 | u8 | `media_type` | Media type registry. |
| 101 | 1 | u8 | `fs_profile` | Filesystem profile id. |
| 102 | 1 | u8 | `health` | 1 healthy, 2 degraded, 3 critical, 4 failed, 5 unknown, 6 unverified, this disc. |
| 103 | 1 | u8 | `state_flags` | bit0 closed, bit1 append-raw-only, bit2 spare below the threshold, bit3 capacity forced. |
| 104 | 2 | u16 | `rs_margin_percent` | Worst-stripe margin, as a percentage of `m`, rounded down and clamped to 0 to 100. |
| 106 | 2 | u16 | `spare_remaining_percent` | Remaining POW spare, as a percentage, rounded down and clamped to 0 to 100. |
| 108 | 4 | u32 | `label_len` | Byte length of the label. |
| 112 | 48 | u8[48] | `label` | UTF-8, zero-padded. The first 48 bytes of the superblock label, which is the printed part. |

The container header is the simple table container of section 11.4, with
magic `"NADD"` and `record_size` 160.

Field rules. `used_sectors` is `lba_base + run_sectors` of the newest run. It
is not a sum over runs and not the drive's next writable address.

`first_burn_sec` is the pack time of the disc's first run, the same value as
the superblock's `created_sec`.

`label` holds the first 48 bytes of the superblock label, which is the printed
part. The superblock label is 64 bytes. Both widths are as their tables state.

`health` 6, `unverified, this disc`, is the value a run writes into the record
of the disc it is being written onto. That run is written before its own disc is
burned, so no verification of that disc exists yet and no other value is honest.
The writing run sets that record's `health` to 6, its `rs_margin_percent` to
100, its `last_verify_sec` to 0, and its `spare_remaining_percent` to the value
the writer knows from the format: 100 on a disc formatted for Pseudo-OverWrite
and 0 on a sealed disc, which has no spare area. A later run of the repository
replaces the record with the measured values. A reader treats `health` 6 like
`health` 5: it reports the disc as not yet verified and never as healthy.

`rs_margin_percent` and `spare_remaining_percent` are whole percents. Each is
rounded down and clamped to the range 0 to 100 (section 10.7).

Hash and CRC coverage. `super_hash` covers the 2048 bytes of that disc's
superblock, CRC included, under the container's `hash_algo`.

Reader checks. Verify both container CRCs. Check each `super_hash` against the
superblock of the disc in the drive when that disc is present.

### 11.7 Catalog contents per run

Every run carries a filter, a manifest and a catalog. The prerequisite list is
part of the manifest.

Every run carries:

| Item | Scope | Why |
|---|---|---|
| This run's filter | This run | Membership. |
| Every earlier run's filter | The whole repository | A negative across all filters is a proof of absence. |
| This run's manifest | This run | Exact lookup. |
| The previous `manifest.history_depth` runs' manifests | Recent history | Losing the newest disc must not lose the newest exact membership data. Warming a cache from one disc then yields the depth plus one manifests. |
| Every snapshot object, complete | The whole repository | A reader lists, names and walks the whole history from one disc. |
| The full snapshot table | The whole repository | The sorted index over those objects. |
| The full ref table | The whole repository | The entry point for names. |
| The full run table | The whole repository | Maps every run seq to its disc and its LBA range (section 11.5). |
| The disc directory | The whole repository | The entry point for "which physical disc". |
| This run's prerequisite list, inside the manifest | This run | What is missing and where it is. |
| This run's layout table | This run | LBA extents of every file, for FEC and for the Phase 3 recovery read. |

The catalog may not exceed `catalog.max_bytes`, default 512 MiB. When the full
catalog would exceed the cap, the writer drops items in reverse priority
order:

| Priority | Item | Dropped when |
|---:|---|---|
| 1 | Every snapshot object | Never. |
| 2 | Every run filter | Never. |
| 3 | The disc directory and the run table | Never. |
| 4 | The snapshot table and the ref table | Never. |
| 5 | Recent manifests, oldest first | The cap is reached. |

Priorities 1 to 4 are never dropped. Only the manifest history is elastic. The
writer drops the oldest manifest first, then the next oldest, and stops as soon
as the catalog fits the cap, so the newest manifests are the ones it keeps. The
writer records the number of manifests it kept in `manifests_kept`. A run that
keeps zero manifests is still valid.

Manifests are not replicated for the whole repository.

### 11.8 Catalog container

Purpose: the one structure a reader opens first in a run's catalog. It lists
every other catalog file with its hash and its length.

Container header, 64 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 4 | u32 | `magic` | `"NACT"`. |
| 4 | 2 | u16 | `version_major` | 1. |
| 6 | 2 | u16 | `version_minor` | 0. |
| 8 | 8 | u64 | `required_feat` | Refuse on an unknown bit. |
| 16 | 8 | u64 | `optional_feat` | Ignore an unknown bit. |
| 24 | 8 | u64 | `run_seq` | The run that carries this catalog. |
| 32 | 4 | u32 | `entry_count` | Number of entries. |
| 36 | 2 | u16 | `entry_size` | 64. |
| 38 | 1 | u8 | `hash_algo` | Multicodec code of `file_hash` in every entry. |
| 39 | 1 | u8 | `digest_len` | 32. |
| 40 | 4 | u32 | `manifests_kept` | Number of earlier manifests carried. 0 to `manifest.history_depth`. |
| 44 | 4 | u32 | `filters_kept` | Number of earlier filters carried. Equals the number of earlier runs of the repository, a withdrawn run included (section 11.5). |
| 48 | 1 | u8 | `snapobj_form` | 1 one file per snapshot object under `snapobj/`. 2 one packed `snapobj.bin`. |
| 49 | 3 | u8[3] | `reserved` | Zero. |
| 52 | 4 | u32 | `reserved_u32` | Zero. |
| 56 | 4 | u32 | `body_crc32c` | CRC-32C over the entries. |
| 60 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 59. |
| 64 | | | `entries` | `entry_count` records of 64 bytes, in the entry order that section 11.8 states. |

Catalog entry, 64 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `file_hash` | Hash of the **whole file's bytes**, from its first byte to its last, under the container's `hash_algo`. For a `snapobj/<name>` file that is the object header plus the stored payload, so it does **not** equal the digest in the file's name, which is the hash of the uncompressed payload alone (sections 3.1 and 11.4). A reader checks `file_hash` before it parses the file, and the name after it has decompressed the payload. |
| 32 | 1 | u8 | `catalog_role` | 1 `filters/<seq>.bin`. 2 `manifests/<seq>.bin`. 3 `snapshots.bin`. 4 `refs.bin`. 5 `discs.bin`. 6 `snapobj/<name>`. 7 `snapobj.bin`. 8 `runs.bin`. |
| 33 | 1 | u8 | `hash_algo` | Multicodec code of the digest in a `snapobj/<name>` file name. 0 for other roles. |
| 34 | 2 | u16 | `reserved_u16` | Zero. |
| 36 | 4 | u32 | `reserved_u32` | Zero. |
| 40 | 8 | u64 | `seq` | For roles 1 and 2, the run seq the file describes. 0 otherwise. |
| 48 | 8 | u64 | `byte_len` | Length of the file in bytes. |
| 56 | 8 | u64 | `reserved_u64` | Zero. |

`catalog_role` names a position in this table's own registry. It is not the
`file_role` field of the run layout table (section 7.10): the two fields
share no registry, and an id such as 8 means something different in each.
A reader that holds a catalog entry and a layout extent for the same file
takes the role from the table it is reading, never from the other one.

Field rules. Catalog entries are ordered by `catalog_role` ascending, then
`seq` ascending, then `file_hash` ascending. That entry order is the file order
in the run.

`catalog_role` and the layout table's `file_role` share no registry. A reader
takes the role from the table it is reading.

Hash and CRC coverage. `header_crc32c` covers bytes 0 to 59. `body_crc32c`
covers the entries. `file_hash` covers every byte of the named catalog file.
The run header names and verifies `CATALOG.bin` itself through `catalog_hash`.

Reader checks. Verify both CRCs. Verify `file_hash` before parsing any catalog
file. A reader that finds a catalog file whose hash does not match its entry
treats that file as absent and takes it from an older run, or reports it. It
never uses the mismatched bytes.

### 11.9 Dedup rule

Never drop chunk data on the strength of a filter. A filter hit is a hint to go
read an exact manifest. Only an exact manifest hit permits dropping the data.

If the manifest cannot be consulted, the writer writes the chunk again and logs
the event.

Withdrawn runs take no part in the filter union, in the manifest confirmation,
or in the unconfirmed-hit step.

### 11.10 Proof of absence and coverage

A filter negative across every run is a proof of absence. Filters have no false
negatives.

In a connectivity check chunks are never read. Membership alone is the whole
obligation. Only trees, chunklists and snapshots are read.

A version 1 reader always uses the walk. The reserved bitmap counting argument
is not available.

Coverage of an object is exact when a manifest record locates it, and probable
when a filter is positive on a run whose manifest the reader lacks. A reader
states which for every object.

An object for which every run's filter is negative is missing, and that is a
proof. A plan with any missing object fails up front.

A plan that holds a probable object is valid and must not be refused. The
reader counts probable objects per disc and for the whole set. A probable
object is confirmed against that run's manifest when the disc is in the drive,
which is the first thing the reader reads from that disc.


---

## 12. Reader and writer rules

### 12.1 Reader procedure

A reader applies these steps in order, for every structure:

1. Check the magic. Refuse on a mismatch.
2. Check `version_major`. Refuse an unknown value and print the value.
3. For the common object header, the tree header and the tree entry, read
   `header_len` and skip the excess. Never assume the compiled size. For every
   other structure, use the fixed size of its `version_major`.
4. Check `required_feat`. Refuse an unknown bit and print the bit number.
5. Ignore unknown `optional_feat` bits.
6. Verify the checksum before using any field.
7. Verify the content id after decompression.

A reader verifies the hash and the CRC of a structure before it uses any field
of it.

Every object's content id is verified after it is read. A mismatch is a hard
error. Every function that returns object bytes verifies the content id before
it returns. There is no trusted path.

### 12.2 Writer rules

1. Never write a `required_feat` bit that the current major version does not
   define.
2. Never reuse a registry id.
3. Never change a frozen table under an existing name.
4. Record every parameter that a future diagnostic tool would need, even when a
   reader does not need it.
5. Never depend on a structure that a later version might change. Read the
   version first, and the `header_len` where the structure carries one.
6. Set exactly the feature bits of section 2.4 and no other.

### 12.3 Which catalog a reader trusts

A reader takes the catalog of the newest run on the disc that verifies. A run
verifies when all five of these pass: the `header_crc32c` of a run header copy
the reader could read, and the `layout_hash`, `manifest_hash`, `filter_hash`
and `catalog_hash` that the header records, each checked against the bytes of
the file it names.

A run that fails any of the five is skipped, whatever its `run_seq`, and the
reader steps back along the chain to the previous run and reports the run it
skipped.

A local index is an accelerator only. Everything in it is derived from discs
and is rebuildable. Every command behaves the same, apart from speed, with the
local index deleted.

### 12.4 Conformance

A conforming reader of format major 1:

1. reads every structure of this document at `version_major` 1 and any
   `version_minor`, by the rules of section 12.1;
2. reads objects under both hash algorithms of section 3.2, and under
   compression ids 0 and 1; it may refuse id 2, `lz4`, and must then name the
   id;
3. reads a disc of profile 0 and of profile 1, which share one filesystem; it
   may refuse profile 2 and must then name the profile;
4. finds every object through the filesystem, the run headers, the manifests,
   the filters and the catalog, with no local index and no disc other than the
   ones the plan names;
5. verifies every CRC, every hash of section 2.8 and every content id before it
   uses the bytes;
6. repairs a run with `k = 231`, `m = 23`, or says that it cannot repair;
7. refuses an unknown `version_major`, an unknown `required_feat` bit, an
   unknown registry id in a field it must interpret, and a critical TLV it does
   not know, and says which.

A conforming writer of format major 1:

1. writes every structure exactly as its byte-offset table states, with
   `version_major` 1, `version_minor` 0, reserved fields zero, and the feature
   bits of section 2.4 and no other;
2. writes BLAKE3-256 or SHA-256 content ids over the uncompressed payload,
   FastCDC cut points by section 4.2, and bundles by section 6.3;
3. writes every run with `k = 231`, `m = 23`, the checksum column of section
   10.3, `m + 2` header copies, the fill order of section 8.6 and the catalog
   copies of section 11.7;
4. writes every byte as an ordinary file under `/NOAHSARK/` and never rewrites
   a burned run;
5. writes only a disc filesystem profile that it implements, and never a
   reserved id, bit or value.

### 12.5 Change mechanisms

Every change uses one of two mechanisms.

| Mechanism | When | Old reader | New reader |
|---|---|---|---|
| A feature bit | A structure gains an item. | Ignores an `optional_feat` bit. Refuses a `required_feat` bit. | Uses the item. |
| A version bump | A structure changes shape. | Refuses an unknown `version_major`. Ignores an unknown `version_minor`; for the three structures that carry `header_len` it skips `header_len - known`. | Uses the new shape. |

A registry id is never reused and never renumbered.

| Change | Mechanism | Old reader does | New reader does |
|---|---|---|---|
| New hash algorithm | New id in the hash registry. | Refuses an object whose `hash_algo` it does not know, and says the code. Old discs stay readable. | Reads both. Writes the new default. |
| Chunker profile change | New id in the chunker registry. | Unaffected. A reader never needs the profile. | Uses the new profile for new runs. Old runs keep theirs. |
| Gear table change | New `gear_table_id` and a new profile name. | Unaffected. | Must never reuse an existing profile name with a different table. |
| New compression algorithm | New id in the compression registry. | Refuses an object whose `compression` it does not know. The object is unreadable, not misread. | Reads it. |
| FEC parameter change | A new `version_major` of the run header and the layout table. Both already record `k` and `m`. | Refuses the run for repair, because version 1 accepts no pair other than 231 and 23. Still reads the objects through the filesystem. | Reads the recorded pair. |
| New FEC scheme | New id in the FEC registry, plus a `required_feat` bit. | Refuses the run for repair, but still reads its objects through the filesystem. | Repairs it. |
| Filesystem revision change | `fs_revision` in the superblock. | Depends on the operating system, not on NoahsArk. | Same. |
| Disc filesystem profile change | New id in the profile registry. | Refuses a profile it does not know, and says the id. | Reads it. |
| New object kind | New id in the object kind registry, plus a `required_feat` bit on the containing structure. | Refuses the structure. | Reads it. |
| New tree TLV | New id in the TLV registry. The critical bit decides. | Refuses the entry when the critical bit is set. Preserves and reports the TLV otherwise. | Applies it. |
| New manifest chunk | New chunk id in the TOC. | Skips the unknown chunk id. | Reads it. |
| Fan-out 8 to 16 bits | `FEAT_FAN16` required bit and `fanout_bits`. | Refuses the manifest. | Reads it. |
| Encryption | `crypto` byte, `FEAT_CRYPTO` bit, key material region. | Refuses the object. | Decrypts it. |
| Snapshot signature | New snapshot TLV, non-critical. | Ignores it. | Verifies it. |

### 12.6 Cross-version and cross-phase reading

`Y` means full use. `~` means partial use with the loss named. `N` means a
clean refusal that names the reason.

A reader that implements profile 0 and profile 1 only:

- reads every object, manifest, filter and catalog normally;
- refuses a disc whose `fs_profile` is 2 and names the id;
- ignores the optional bitmaps and the reverse index;
- reports a cross-disc parity group as not supported;
- reports `run_kind` 3 and `OPT_DISC_PARITY` as not supported, and still reads
  every object of that run;
- reads a raw-append disc only as far as the filesystem shows, and reports the
  raw runs as unreachable unless it implements the recovery read of section
  7.11.

No data is lost in any of those cases.

By format version:

| Writer | Reader of major 1 | Reader of a later major |
|---|---|---|
| Major 1, minor 0 | Y | Y. It reads the fixed sizes of major 1. |
| Major 1, minor above 0, no new `required_feat` bit | Y. It skips `header_len - known` in the three structures that carry `header_len`, and ignores fields it does not know elsewhere. | Y |
| Major 1 with an unknown `required_feat` bit | N. Refuses the structure and prints the bit number. | Y when the bit is known to it. |
| Major 1 with an unknown `optional_feat` bit | Y, ignoring the bit. | Y |
| A later major | N. Refuses and prints `version_major`. | Y |

By algorithm and registry id:

| Written with | Reader that knows it | Reader that does not |
|---|---|---|
| BLAKE3-256 or SHA-256 ids | Y. Both are mandatory for a conforming reader. | Not possible at major 1. |
| A new hash algorithm id | Y | N. Refuses the object and names the multicodec code. Older discs stay readable. |
| compression 0 or 1 | Y | Not possible at major 1. |
| compression 2, `lz4` | Y | N, permitted. Refuses the object and names the id. |
| A new compression id | Y | N. Refuses the object and names the id. |
| A new chunker profile | Y | Y. A reader never needs the profile. |
| A new manifest TOC chunk id | Y | Y, skipping the chunk. |
| `FEAT_FAN16` | Y | N. Refuses the manifest. |
| A non-critical unknown tree TLV | Y | ~ Preserves it on copy, reports it on restore, does not apply it. |
| A critical unknown tree TLV, 0x8000 to 0xBFFF | Y | N. Refuses the entry and names the type. |
| `k`, `m` other than 231, 23 | Y | N for repair; still reads every object through the filesystem. |
| A new FEC scheme id | Y | N for repair; still reads every object through the filesystem. |

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
| Format version, `format.version_major` and `format.version_minor` | The `version_major` and `version_minor` of every structure a writer emits (section 2.7). |
| Current hash algorithm | The multihash algorithm of every new object id. |
| Chunker profile | The cut points of every new chunk, hence the chunk boundaries in every new tree and chunklist. |
| Gear table id | The Gear table version, which changes every cut point under a profile name. |
| Bundle threshold | Which chunks are bundled instead of stored as their own object file. |
| Bundle target size | The size, and therefore the member count, of every new bundle. |
| Inline chunk maximum | Whether a file's chunk ids are inline in its tree entry or spill to a chunklist object. |
| TLV spill threshold | Whether a tree entry's TLV area is inline or spills into chunks. |
| Compression algorithm and level | The stored bytes of every new chunk payload. |
| Compression minimum gain | Whether a chunk is stored compressed or raw. |
| Filesystem profile | The disc filesystem. Fixed per disc at its first burn. |
| Fan-out levels | The object path depth under `objects/` and `trees/`. |
| Filesystem revision | The revision the image builder writes. |
| Close policy, `disc.close_policy` | Whether the first burn seals the disc, hence the superblock `sealed` byte, the `spare_area` term and the tail anchors (sections 7.5, 8.1 and 7.12). |
| Spare mode and spare reserve bytes | The `spare_area` term, hence `reserve` and `data_budget`. Fixed per disc at format time. |
| Expected runs per disc | `catalog_growth`, hence `reserve` and `data_budget`. |
| Fill ratio | `safety_margin`, hence `reserve` and `data_budget`. |
| Forced reserve and extra reserve | `reserve` and `data_budget` directly, and both are recorded in the superblock. |
| Forced capacity, `disc.force_capacity` | `capacity_forced_sectors` and `fill_limit_sectors` in the superblock, hence every run length on the disc (section 7.14). |
| FEC scheme and geometry, `fec.scheme`, `fec.k`, `fec.m` and `fec.disc_close_parity` | The stripe shape, the column count, the parity file set and the checksum column, hence every LBA of a run (sections 10.1 and 10.2). Version 1 fixes `k` 231 and `m` 23 and refuses any other value. |
| Filter type | The run filter's on-disc shape. Only `binaryfuse16` exists in version 1. |
| Manifest fan-out bits | The width of the `"FANO"` table's entries. |
| Manifest history depth | How many earlier manifests every catalog copy carries. |
| Catalog size cap | How much manifest history a catalog keeps before it drops the oldest. |
| Expected snapshots and table reserve bytes | The reserve estimator's table term, hence `reserve` and `data_budget`. |
| Snapshot-object pack threshold | Whether snapshot objects are replicated as loose files or as one `snapobj.bin`. |
| Optional metadata switches | Which optional metadata fields are present in every new tree entry. |
| Source type, `source.type` | The `source_type` and `source_flags` bytes of every new snapshot payload, hence its content id (section 6.15). |
| Exclude rules, `sources.exclude` and `sources.ignore_file` | Which paths the walk keeps, and the exclude-rule bytes that snapshot metadata tag 5 stores (sections 6.17 and 6.15). |
| Mount-point crossing, `sources.one_file_system` | Whether the walk descends into a directory that lies on another filesystem. It decides which entries exist in every new tree, and therefore every tree id, every snapshot id and the object set of the run. |
| Symlink following, `sources.follow_symlinks` | Whether the walk follows a symlink and stores the target's content, or stores the link itself as an `entry_type` 3 symlink entry (section 6.6). It decides the entry type, the size and the content reference of every affected entry, and therefore every tree id above it. |
| Label template and repository short name | The label text in the superblock, the disc directory and `README.txt`. |
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
| Multihash text form | One digest under each algorithm |
| Common object header | One chunk of stated bytes, stored with zstd level 3 under the frame parameters of section 5.3 and a named `tool_version`, and stored uncompressed |
| Bundle | Three stated small chunks |
| Chunklist | 100 stated chunk ids and lengths |
| Tree | A directory with a regular file, a subdirectory, a symlink, two entries sharing one source inode and stored as independent entries, a device node, one xattr and one spilled TLV, with stated metadata |
| Snapshot | A stated root tree, parent, generation, times and TLVs |
| Ref record | A stated name, snapshot id, time and run seq |
| Disc superblock | Stated identity, capacity, profile and reserve values |
| Run header | Stated geometry and counts |
| Layout table | Ten stated extents, including a parity file and `pad.bin` |
| Manifest | Ten stated records, two in a bundle, one split, two prerequisites, one source run |
| Filter | 1,000 stated keys |
| README.txt | The identity values of the disc superblock vector |
| FORMAT.txt | Format major 1, minor 0 |
| Burn step tree listing | A stated tree of five files, one in a subdirectory |
| Catalog container | Five stated entries |
| Simple tables | Two stated records of each of the four tables |
| Checksum sector | The 231 stated data sectors of one stripe |
| Parity | A stated stripe of `k` data sectors, plus recovery after `m` erasures |
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

The text is 41,097 bytes long in 825 lines, as section 8.5 states.

```
NoahsArk format major 1 minor 0

1. FORMAT RULES
===============

1. Every integer is little-endian. No big-endian field exists.
2. Only fixed-width types are used: u8, u16, u32, u64, i32, i64. No varint appears inside a fixed header.
3. Every structure is packed with manual alignment. Every gap is a named reserved field. A writer writes a reserved field as zero. A reader ignores the value of a reserved field.
4. Every structure begins with magic (u32), then version_major (u16), then version_minor (u16).
5. A structure is a file-level container or an object payload header. A record inside a container, a tree entry, a TLV, the bundle trailer and the checksum sector header are records, not structures. A record carries a magic only where its own table states one.
6. A reader refuses an unknown version_major. A reader accepts an unknown version_minor and ignores the fields it does not know.
7. Every top-level structure carries required_feat (u64) and optional_feat (u64). A reader refuses an unknown required_feat bit. A reader ignores an unknown optional_feat bit.
8. A checksum covers only bytes the writer finalized before computing it. A structure with no separate body ends with one CRC over every byte before it. A container with a header and a body carries header_crc32c and body_crc32c at the end of its header: body_crc32c sits in the header and covers the body. The body becomes final first, then the header is written last, so each CRC covers bytes that were already final.
9. The run filter is the one structure whose body CRC trails the body. It writes header_crc32c inside the fixed header and body_crc32c after the variable-length fingerprint array, because that array's length is not known until the array is sized.
10. CRCs are CRC-32C: polynomial 0x1EDC6F41, reflected, initial value 0xFFFFFFFF, final XOR 0xFFFFFFFF.
11. Objects use the full content hash in place of a body CRC. The content id is the checksum of the payload.
12. Every pointer carries the hash of its target. No unhashed reference exists.
13. A string is encoding (u8), reserved (u8[3], zero), length (u32) in bytes, then the bytes. Encoding 0 is UTF-8. There is no NUL terminator and no normalization.
14. Every structure has a byte-offset table with the columns offset, size, type, name and meaning.

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
0x12	sha2-256	32	Supported. Every implementation must read and write it.
0x1e	blake3	32	Supported. Default for new objects.
0x13	sha2-512	64	Reserved. Not used in version 1.
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

Filter type registry
--------------------

Id	Name	Status
1	binaryfuse16	Default for a run filter.
2	binaryfuse8	Reserved.
3	bloom	In-memory only. Never written to a disc.

FEC scheme registry
-------------------

Id	Name	Field	Shards	Status
1	rs255-gf8	GF(2^8)	255	Default. A stripe is k + 1 + m = 255 sectors; the code is over the k + m data and parity shards (section 10.1).
2	rs-leopard-gf16	GF(2^16)	up to 65536	Reserved.

Object kind registry
--------------------

Id	Name
1	chunk
2	bundle
3	chunklist
4	tree
5	snapshot
6	ref

Disc filesystem profile registry
--------------------------------

Id	Name	Filesystem	Append mechanism	Status
0	oneshot	UDF 2.01. Phase 1 builds UDF 2.01 only.	None in Phase 1. The disc is POW-formatted and left open, so a Phase 2 append can still reach it.	Default. Phase 1.
1	udf201-pow	Pure UDF 2.01	POW growth. Variant 1a: kernel direct write. Variant 1b: image mirror and 32 KiB block diff.	Phase 2.
2	iso9660v1-l4-pow	ISO 9660:1999 level 4, plain	growisofs -Z then -M on a POW BD-R, one session	Phase 3.
3-255	reserved	-	-	-

Media type registry
-------------------

Id	Name	Sectors	Bytes
1	BD-R SL 25	12,219,392	25,025,314,816
2	BD-R DL 50	24,438,784	50,050,629,632
3	BD-R XL TL 100	48,878,592	100,103,356,416
4	BD-R XL QL 128	62,500,864	128,001,769,472
5	BD-RE SL 25	12,219,392	25,025,314,816
6	BD-RE DL 50	24,438,784	50,050,629,632
7	BD-RE XL TL 100	48,878,592	100,103,356,416
8	M-DISC BD SL 25	12,219,392	25,025,314,816
9	M-DISC BD DL 50	24,438,784	50,050,629,632
10	image	variable	variable
11	Mini BD SL 8 cm	3,804,288	7,791,181,824
12	Mini BD DL 8 cm	7,608,576	15,582,363,648

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
0x0011	ACL_ACCESS	no	Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a version_minor bump. A Phase 1 reader treats it as an unknown TLV.
0x0012	ACL_DEFAULT	no	Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a version_minor bump. A Phase 1 reader treats it as an unknown TLV.
0x0013	ACL_NFS4	no	Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a version_minor bump. A Phase 1 reader treats it as an unknown TLV.
0x0020	LINUX_ATTR	no	u32 FS_IOC_GETFLAGS bitmask.
0x0021	BSD_FLAGS	no	u32 st_flags.
0x0030	WIN_ATTRS	no	u32 FILE_ATTRIBUTE_* bitmask.
0x0031	WIN_SD	no	Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a version_minor bump. A Phase 1 reader treats it as an unknown TLV.
0x0032	WIN_ADS	no	Reserved. A Phase 1 writer does not emit this type. Its item layout is defined in a later phase, with a version_minor bump. A Phase 1 reader treats it as an unknown TLV.
0x8000-0xBFFF	reserved critical	yes	Future critical extensions.
0xF000-0xFFFF	vendor	no	Never critical.

Manifest chunk id registry
--------------------------

Chunk id	Content
"FANO"	Fan-out table: 256 or 65536 cumulative u32 counts. Mandatory.
"RECS"	The sorted manifest records. Mandatory.
"BNDL"	The bundle table: the ids of every bundle in this run, 32 bytes each, sorted ascending. Mandatory; zero length when the run holds no bundle.
"PREQ"	The prerequisite list (section 11.3). Authoritative. Mandatory; zero length when nothing is missing.
"SRCR"	The set of run seqs that this run references: u64 values, sorted ascending. Mandatory; zero length when the run references no other run.
"DUPS"	Duplicate accounting: four u64 values, in the field order that this section gives below. Mandatory.
"SPLT"	Split records: files whose chunks continue on another run; see the operations document. Mandatory; zero length when no file is split.
"BMAP"	Per-snapshot reachability bitmaps. Reserved. No payload is defined in version 1.
"RIDX"	Reverse index by LBA. Reserved. No payload is defined in version 1.

3. STRUCTURES
=============

Common object header
--------------------

offset	size	type	name	meaning
0	4	u32	magic	"NAOB", 0x424F414E.
4	2	u16	version_major	1. Refuse if unknown.
6	2	u16	version_minor	0. Ignore if unknown.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	1	u8	kind	Object kind registry, section 6.1.
25	1	u8	hash_algo	Multicodec code. 0x12 sha2-256, 0x1e blake3.
26	1	u8	digest_len	Digest length in bytes. 32 in version 1.
27	1	u8	compression	Compression registry. 0 none, 1 zstd, 2 lz4.
28	1	u8	crypto	0 plaintext. Other values reserved.
29	1	u8	reserved_u8	Zero.
30	2	u16	header_len	64 in version 1. A reader skips the excess.
32	8	u64	payload_len	Uncompressed payload length in bytes.
40	8	u64	stored_len	Bytes on the medium after this header.
48	8	u64	reserved_u64	Zero.
56	4	u32	reserved_u32	Zero.
60	4	u32	header_crc32c	CRC-32C over bytes 0 to 59.

Bundle header
-------------

offset	size	type	name	meaning
0	4	u32	magic	"NABD".
4	2	u16	version_major	1.
6	2	u16	version_minor	0.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	4	u32	entry_count	Number of chunks in this bundle.
28	2	u16	entry_size	64. A reader strides by this value.
30	1	u8	hash_algo	Multicodec code of the entry ids.
31	1	u8	digest_len	32.
32	8	u64	index_off	Offset of the index table from the bundle header.
40	8	u64	data_off	Offset of the first chunk payload.
48	8	u64	data_len	Total bytes of all chunk payloads.
56	4	u32	reserved_u32	Zero.
60	4	u32	header_crc32c	CRC-32C over bytes 0 to 59.

Bundle index entry
------------------

offset	size	type	name	meaning
0	32	u8[32]	content_id	Chunk id.
32	8	u64	offset	Byte offset from data_off.
40	8	u64	stored_len	Bytes stored for this chunk.
48	8	u64	payload_len	Uncompressed bytes.
56	1	u8	compression	Compression id for this chunk.
57	1	u8	hash_algo	Multicodec code.
58	1	u8	digest_len	32.
59	1	u8	reserved_u8	Zero.
60	4	u32	reserved_u32	Zero.

Bundle trailer
--------------

offset	size	type	name	meaning
0	4	u32	magic	"NABT".
4	4	u32	entry_count	Repeat of the entry count.
8	8	u64	index_off	Repeat of the index offset.
16	8	u64	payload_len	Bundle payload length, for validation.
24	4	u32	reserved_u32	Zero.
28	4	u32	trailer_crc32c	CRC-32C over bytes 0 to 27.

Chunklist header
----------------

offset	size	type	name	meaning
0	4	u32	magic	"NACL".
4	2	u16	version_major	1.
6	2	u16	version_minor	0.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	8	u64	entry_count	Number of entries.
32	8	u64	total_size	Sum of length over all entries.
40	2	u16	entry_size	48.
42	1	u8	hash_algo	Multicodec code.
43	1	u8	digest_len	32.
44	1	u8	level	0 = entries are chunks. 1 = entries are chunklists.
45	3	u8[3]	reserved	Zero.
48	-	-	entries	entry_count records of 48 bytes.

Chunklist entry
---------------

offset	size	type	name	meaning
0	32	u8[32]	content_id	Chunk id, or child chunklist id.
32	8	u64	length	Uncompressed bytes this entry contributes.
40	8	u64	file_offset	Offset of this entry inside the file.

Tree header
-----------

offset	size	type	name	meaning
0	4	u32	magic	"NATR".
4	2	u16	version_major	1.
6	2	u16	version_minor	0.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	4	u32	entry_count	Number of entries.
28	2	u16	header_len	40.
30	1	u8	hash_algo	Multicodec code of the child ids.
31	1	u8	digest_len	32.
32	8	u64	payload_len	Payload length, for validation.
40	-	-	entries	Entries, back to back, each self-delimiting.

Tree entry
----------

offset	size	type	name	meaning
0	4	u32	entry_len	Total entry length including all variable areas. Multiple of 8.
4	2	u16	header_len	112 in version 1. A reader skips the excess.
6	1	u8	entry_type	1 regular, 2 directory, 3 symlink, 4 chardev, 5 blockdev, 6 fifo, 7 socket. 0 is invalid.
7	1	u8	entry_flags	See section 6.7.
8	8	u64	size	Regular files only. 0 otherwise.
16	8	u64	hardlink_group	Reserved for a later phase. A Phase 1 writer writes 0. See section 6.14.
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
96	4	u32	content_len	Bytes in that area. See section 6.8.
100	4	u32	ext_off	Offset to the TLV area. 0 when ext_len is 0.
104	4	u32	ext_len	Bytes in the TLV area, padding included.
108	2	u16	name_off	Offset to the name bytes. 112 in version 1.
110	2	u16	name_len	Name length in bytes, 1 to 4095. No terminator.

Tree extension TLV record
-------------------------

offset	size	type	name	meaning
0	2	u16	tlv_type	Registry, section 6.10.
2	2	u16	tlv_flags	bit0 CRITICAL, bit1 SPILLED, bit2 SPILL_IS_CHUNKLIST, bits 3 to 15 reserved.
4	4	u32	tlv_len	Payload bytes, excluding this prefix and excluding padding.
8	tlv_len	u8[]	payload	The value, or the spill reference.
-	pad	u8[]	-	Zero bytes to the next 8-byte boundary.

Snapshot header
---------------

offset	size	type	name	meaning
0	4	u32	magic	"NASN".
4	2	u16	version_major	1.
6	2	u16	version_minor	0.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	32	u8[32]	root_tree	Content id of the root tree.
56	32	u8[32]	parent	Content id of the parent snapshot. All zero for a root.
88	8	u64	generation	1 + parent generation. 1 for a root.
96	8	i64	time_sec	Snapshot time, seconds.
104	4	u32	time_nsec	Nanoseconds.
108	4	i32	tz_offset_sec	Local zone offset at snapshot time.
112	8	u64	total_size	Sum of payload_len over the distinct objects that reachable_object_count counts, that is over the distinct chunks, chunklists and trees reachable from root_tree. A chunk that several files share is counted once. It is not the sum of the file sizes. For planning.
120	8	u64	reachable_object_count	Distinct content ids reachable from root_tree: chunks, chunklists and trees, the root tree included. Bundles are not counted, and the snapshot itself is not counted. This is a logical reachability count; it is not comparable to the run header's object_count (section 7.6), which is a physical count that includes bundles.
128	1	u8	hash_algo	Multicodec code of root_tree and of every id below it.
129	1	u8	chunker_profile	Chunker profile id used to produce it.
130	2	u16	meta_count	Number of TLV records that follow.
132	1	u8	source_type	Where the source tree was read from. See below.
133	1	u8	source_flags	What the source could not provide. See below.
134	1	u8	parent_hash_algo	Multicodec code of parent. Equals hash_algo except across an epoch boundary. 0 for a root.
135	1	u8	reserved_u8	Zero.
136	-	-	TLV records	meta_count records follow.

Snapshot metadata TLV record
----------------------------

offset	size	type	name	meaning
0	2	u16	tag	Snapshot metadata tag registry, section 6.15: 1 author, 2 host, 3 message, 4 source root, 5 exclude rules, 6 checksum commit. Tags 4 and 5 repeat, one per root, in root order.
2	2	u16	flags	bit0 CRITICAL.
4	4	u32	len	Payload bytes.
8	len	u8[]	value	UTF-8 for tags 1 to 3. Raw path bytes for tag 4. Pattern lines for tag 5, as below. Empty for tag 6.
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
48	40	u8[40]	name	UTF-8, zero-padded. A name above 40 bytes is refused at commit time. The limit is part of the format.
88	8	u64	run_seq	Run that recorded this value.

Disc superblock
---------------

offset	size	type	name	meaning
0	4	u32	magic	"NADS".
4	2	u16	version_major	1. Refuse if unknown.
6	2	u16	version_minor	0. Ignore if unknown.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	16	u8[16]	disc_uuid	Unique for this physical disc.
40	16	u8[16]	repo_uuid	The repository this disc belongs to.
56	8	u64	disc_seq	Monotonic position in the repository.
64	8	u64	capacity_sectors	As reported by the drive at first write.
72	8	u64	fill_limit_sectors	Number of sectors, counted from LBA 0, that the writer may use. No run, parity included, ends at or above this LBA. The operations document is the normative home of this value, of data_budget and of the reserve; it gives the definition, the invariants and the reference estimator.
80	8	u64	capacity_forced_sectors	Forced capacity, section 7.14. Equals capacity_sectors when no override was given.
88	8	u64	first_run_lba	LBA of the first run header. Immutable: the first run never moves.
96	8	u64	reserved_u64a	Zero.
104	8	u64	reserved_u64b	Zero.
112	8	u64	reserved_u64c	Zero.
120	8	u64	reserved_u64d	Zero.
128	32	u8[32]	reserved_hash	Zero.
160	32	u8[32]	prev_disc_super_hash	Hash of the superblock of the highest disc_seq below this one of which the writer holds a verified copy. All zero when there is none, which includes disc_seq 0 and the first disc of a repository recreated by init --repo-uuid (section 7.5, the superblock chain).
192	8	i64	created_sec	Pack time of the first run, seconds: the moment pack finalized that run's image. Not the burn time, which is unknown when these bytes are hashed (section 7.6).
200	4	u32	created_nsec	Nanoseconds.
204	4	i32	tz_offset_sec	Local zone offset at that pack time.
208	1	u8	media_type	Media type registry.
209	1	u8	fs_profile	Disc filesystem profile registry.
210	1	u8	hash_algo	Multicodec code of the first run, and of prev_disc_super_hash.
211	1	u8	digest_len	32.
212	1	u8	chunker_profile	Chunker profile of the first run.
213	1	u8	compression	Default compression of the first run.
214	1	u8	fec_scheme	FEC scheme registry.
215	1	u8	crypto	0 plaintext.
216	2	u16	sector_size	2048.
218	2	u16	fs_revision	0x0201 for UDF 2.01. 0x0004 for ISO 9660:1999 level 4.
220	2	u16	fec_k	Data columns. 231 in version 1.
222	2	u16	fec_m	Parity columns. 23 in version 1.
224	1	u8	fanout_levels	1 by default. 2 is allowed under profile 1 and profile 0 only.
225	1	u8	append_variant	Profile 1 only. 1 = variant 1a, kernel direct write. 2 = variant 1b, image mirror and block diff. 0 elsewhere.
226	1	u8	capacity_is_forced	1 when capacity_forced_sectors is below capacity_sectors.
227	1	u8	sealed	1 when the disc was burned sealed at its first write: spare:none and -dvd-compat at its first and only write (see the operations document). 0 when the disc was left open. It says nothing about a later noahsark close, because the superblock is never updated; the close state of an open disc lives in the run chain and the disc directory (sections 7.7 and 11.6).
228	4	u32	label_len	Byte length of the label.
232	64	u8[64]	label	UTF-8, zero-padded.
296	4	u32	tool_version	Writer registry id in the high 8 bits, writer-defined version in the low 24 bits.
300	4	u32	reserved_u32	Zero.
304	8	u64	reserved_u64e	Zero. Object counts are mutable and live in the run chain.
312	8	u64	reserved_u64f	Zero. Used sectors are mutable and live in the run chain.
320	8	u64	reserve_computed_sectors	The reserve that the formula in the operations document produced.
328	8	u64	reserve_forced_sectors	disc.force_reserve, in sectors. 0 when unset.
336	8	u64	reserve_extra_sectors	disc.extra_reserve, in sectors. 0 when unset.
344	168	u8[168]	reserved_a	Zero.
512	256	u8[256]	key_material	Reserved for encryption. All zero in version 1.
768	1276	u8[1276]	reserved_b	Zero.
2044	4	u32	super_crc32c	CRC-32C over bytes 0 to 2043.

Run header
----------

offset	size	type	name	meaning
0	4	u32	magic	"NARH".
4	2	u16	version_major	1.
6	2	u16	version_minor	0.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	16	u8[16]	disc_uuid	The disc this run sits on.
40	16	u8[16]	repo_uuid	The repository.
56	8	u64	run_seq	Monotonic run number in the repository, 1-based.
64	8	u64	disc_seq	Disc sequence number, 0-based.
72	8	u64	lba_base	First LBA of the run's parity domain. 0 for the first run of a disc. The LBA of RUN.bin for every later run (section 7.3).
80	8	u64	run_sectors	Length of the run in sectors, from lba_base to the last sector of RUN2.bin, inclusive.
88	8	u64	column_sectors	L, the length of one column.
96	2	u16	fec_k	Data columns. 231 in version 1.
98	2	u16	fec_m	Parity columns. 23 in version 1.
100	1	u8	fec_scheme	FEC scheme registry.
101	1	u8	checksum_column	Column index of the checksum column. Equals fec_k.
102	1	u8	hash_algo	Multicodec code of every id in this run.
103	1	u8	digest_len	32.
104	1	u8	chunker_profile	Profile id.
105	1	u8	chunker_nc_level	2.
106	1	u8	compression	Default compression id.
107	1	u8	fs_profile	Disc filesystem profile id. Equals the superblock's fs_profile.
108	4	u32	chunk_min	Bytes.
112	4	u32	chunk_avg	Bytes.
116	4	u32	chunk_max	Bytes.
120	4	u32	gear_table_id	Gear table version. 1 in this specification.
124	4	u32	bundle_threshold	Bytes.
128	8	u64	bundle_target	Bytes.
136	8	u64	object_count	Objects in this run. Equals record_count of the manifest.
144	8	u64	payload_bytes	Stored object bytes in this run: the sum of byte_len over the extent records of layout.bin whose file_role is 0. Each extent's byte_len is the bytes that extent carries, so a fragmented object's extents sum to its header plus its whole stored payload, counted once, and a bundle is counted once, not per chunk.
152	8	u64	duplicate_bytes	Bytes written again for locality.
160	8	u64	manifest_lba	LBA of the manifest container.
168	8	u64	manifest_sectors	Length in sectors.
176	32	u8[32]	manifest_hash	Hash of the manifest bytes.
208	8	u64	filter_lba	LBA of this run's filter.
216	8	u64	filter_sectors	Length in sectors.
224	32	u8[32]	filter_hash	Hash of the filter bytes.
256	8	u64	layout_lba	LBA of the layout table.
264	8	u64	layout_sectors	Length in sectors.
272	32	u8[32]	layout_hash	Hash of the layout bytes.
304	8	u64	catalog_lba	LBA of catalog/CATALOG.bin in this run (section 11.8).
312	8	u64	catalog_sectors	Length of CATALOG.bin in sectors.
320	32	u8[32]	catalog_hash	Hash of the CATALOG.bin bytes.
352	32	u8[32]	prev_run_header_hash	Hash of the previous run header on this disc. Zero for the first run.
384	8	i64	created_sec	Pack time, seconds: the moment pack finalized this run's image. Never the burn time, which is unknown when these bytes are hashed; the actual burn time lives only in the local state log; see the operations document.
392	4	u32	created_nsec	Nanoseconds.
396	4	u32	source_run_count	Number of run seqs this run references.
400	8	u64	snapshot_count	Snapshot objects that this run stores under /NOAHSARK/snapshots/. The replicated copies under catalog/snapobj/ are not counted, so the value equals the number of manifest records with kind 5.
408	8	u64	prereq_count	Prerequisite ids listed in the manifest.
416	4	u32	tool_version	Writer registry id in the high 8 bits, writer-defined version in the low 24 bits, as in the superblock (section 7.5).
420	1	u8	run_kind	1 data run, 2 repair run (Phase 2), 3 disc-close parity run (Phase 3). 0 is invalid. Health is never stored here; it lives in the disc directory.
421	1	u8	session_start_sector_valid	1 when session_start_sector is meaningful.
422	1	u8	run_flags	Run header flags. Bit 0 CLOSING_RUN: this run closed the disc, so no later run is possible on it (section 7.12). Bits 1 to 7 reserved, zero.
423	1	u8	reserved_u8	Zero.
424	8	u64	session_start_sector	Value passed to isoinfo -T for this run under profile 2. Zero under profile 1.
432	8	u64	prev_run_header_lba	LBA of the previous run header on this disc. Zero for the first run.
440	8	u64	disc_object_count	Objects on this disc after this run. Cumulative.
448	8	u64	disc_used_sectors	Sectors used on this disc after this run: lba_base + run_sectors of this run, which is the first sector above everything the disc holds. It is not the sum of run_sectors over the runs, and it is not the drive's next writable address, which is rounded up to 16 sectors.
456	4	u32	disc_run_index	Index of this run on this disc. 0 for the first run. Same width and same value as disc_run_index in the run table record (section 11.5).
460	4	u32	reserved_u32b	Zero.
464	8	u64	checksum_lba	First sector of the checksum column, that is the first data sector of checksum.bin.
472	8	u64	parity_lba	First sector of parity column k+1, that is the second data sector of parity/p0232.bin at the default k.
480	8	u64	data_span	Sectors from lba_base to the last sector that step 6 of section 8.6 occupies, inclusive, as section 7.3 defines it. L = ceil(data_span / fec_k).
488	20	u8[20]	reserved	Zero.
508	4	u32	header_crc32c	CRC-32C over bytes 0 to 507.

Layout table header
-------------------

offset	size	type	name	meaning
0	4	u32	magic	"NALY".
4	2	u16	version_major	1.
6	2	u16	version_minor	0.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	8	u64	run_seq	The run described.
32	8	u64	record_count	Number of extent records.
40	2	u16	record_size	64.
42	1	u8	hash_algo	Multicodec code.
43	1	u8	digest_len	32.
44	2	u16	shard_bytes	2048.
46	1	u8	fec_scheme	FEC scheme registry.
47	1	u8	reserved_u8	Zero.
48	8	u64	container_len	Total length of this container in bytes, for validation. It is the container, not an object payload; section 2.3's payload_len is a different field with a different meaning.
56	8	u64	lba_base	First sector of the parity domain, as in the run header.
64	8	u64	column_sectors	L.
72	8	u64	data_span	As in the run header.
80	8	u64	checksum_lba	First sector of the checksum column.
88	8	u64	parity_lba	First sector of parity column k+1.
96	2	u16	fec_k	Data columns.
98	2	u16	fec_m	Parity columns.
100	4	u32	reserved_u32	Zero.
104	4	u32	body_crc32c	CRC-32C over the records.
108	4	u32	header_crc32c	CRC-32C over bytes 0 to 107.
112	-	-	records	In copy order, which is LBA order. See below.

Layout extent record
--------------------

offset	size	type	name	meaning
0	32	u8[32]	content_id	Object id, or bundle id for a bundle extent. For a fixed-name file, the hash of the file bytes under hash_algo. All zero for file roles 1, 5, 10, 11 and 12: the header copies, layout.bin itself, checksum.bin and the parity files. Their bytes become final only after this table is written (section 8.6); each has its own CRC, and the parity covers them.
32	8	u64	start_lba	Absolute LBA of the first sector.
40	8	u64	byte_len	Bytes this extent carries: object header plus stored payload for the first extent of an object, stored payload alone for a later extent.
48	4	u32	sector_count	Sectors this extent covers.
52	4	u32	byte_off	Byte offset inside the first sector.
56	2	u16	extent_index	0 for the first extent of an object.
58	2	u16	extent_flags	bit0 last extent of this object, bit1 duplicate for locality, bit2 metadata object, that is an object that holds references and no file content: a tree, a chunklist or a snapshot object. Bits 3 to 15 reserved, zero. The name is not flags: the manifest record's record_flags (section 11.2) is a different field with a different bit 0.
60	1	u8	kind	Object kind registry. 0 for a fixed-name file.
61	1	u8	compression	Compression id. 0 for a fixed-name file.
62	1	u8	file_role	0 object. 1 RUN.bin. 2 DISC.bin. 3 README.txt. 4 FORMAT.txt. 5 layout.bin. 6 manifest.bin. 7 filter.bin. 8 a catalog file. 9 pad.bin. 10 checksum.bin. 11 a parity file. 12 RUN2.bin.
63	1	u8	column_index	For file_role 11, the parity column index k+1 .. 254. 0 otherwise.

Manifest header
---------------

offset	size	type	name	meaning
0	4	u32	magic	"NAMF".
4	2	u16	version_major	1.
6	2	u16	version_minor	0.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	8	u64	run_seq	The run described.
32	8	u64	record_count	Number of manifest records.
40	4	u32	chunk_count	Number of TOC entries.
44	2	u16	record_size	64.
46	1	u8	hash_algo	Multicodec code.
47	1	u8	fanout_bits	8 or 16.
48	8	u64	container_len	Total length of this container in bytes, for validation. It is the container, not an object payload; section 2.3's payload_len is a different field with a different meaning.
56	4	u32	body_crc32c	CRC-32C over every byte from offset 64 to the end of the container: the TOC, the sentinel and every chunk.
60	4	u32	header_crc32c	CRC-32C over bytes 0 to 59.
64	16*(chunk_count+1)	-	toc	TOC entries, then a sentinel.

Manifest TOC entry
------------------

offset	size	type	name	meaning
0	4	u32	chunk_id	Four ASCII bytes.
4	4	u32	reserved_u32	Zero. Keeps offset aligned.
8	8	u64	offset	Byte offset of the chunk from the container start.

Manifest record
---------------

offset	size	type	name	meaning
0	32	u8[32]	content_id	The object id.
32	8	u64	uncompressed_size	Payload bytes after decompression.
40	8	u64	container	With record_flags bit0 set: the 0-based index of the bundle in the "BNDL" table. Otherwise: the absolute LBA of the first sector of the object's file.
48	8	u64	offset	With record_flags bit0 set: the byte offset of the chunk's stored bytes from the first byte of the bundle file. Otherwise: the byte offset of the object header from the start of sector container, normally 0.
56	2	u16	record_flags	bit0 the object lies inside a bundle, bit1 duplicate for locality, bit2 metadata object, that is a tree, a chunklist or a snapshot object, bit3 spilled TLV payload. Bits 4 to 15 reserved, zero. The name is not flags: the layout extent record's extent_flags (section 7.10) is a different field with a different bit 0.
58	1	u8	hash_algo	Multicodec code.
59	1	u8	digest_len	32.
60	1	u8	kind	Object kind registry.
61	1	u8	compression	Compression id.
62	2	u16	reserved_u16	Zero.

Prerequisite record
-------------------

offset	size	type	name	meaning
0	32	u8[32]	content_id	The referenced object.
32	8	u64	run_seq	The run that holds it.
40	8	u64	disc_seq	The disc that holds that run.

Bundle table entry
------------------

offset	size	type	name	meaning
0	32	u8[32]	bundle_id	Content id of one bundle that this run stores.

Split record
------------

offset	size	type	name	meaning
0	32	u8[32]	chunklist_id	The chunklist of the split file.
32	8	u64	other_run_seq	One other run that holds a part of this file.
40	4	u32	part_index	0-based part number of this run's part.
44	4	u32	part_count	Total parts of the file.

Filter container
----------------

offset	size	type	name	meaning
0	4	u32	magic	"NAFL".
4	2	u16	version_major	1.
6	2	u16	version_minor	0.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	8	u64	run_seq	The run described.
32	8	u64	key_count	Number of object ids in the filter.
40	8	u64	seed	BinaryFuse seed.
48	4	u32	segment_length	BinaryFuse parameter.
52	4	u32	segment_length_mask	BinaryFuse parameter.
56	4	u32	segment_count	BinaryFuse parameter.
60	4	u32	segment_count_length	BinaryFuse parameter.
64	8	u64	fingerprint_count	Number of u16 fingerprints.
72	1	u8	filter_type	Filter type registry. 1 = binaryfuse16.
73	1	u8	hash_algo	Multicodec code of the keys.
74	2	u16	reserved_u16	Zero.
76	4	u32	header_crc32c	CRC-32C over bytes 0 to 75.
80	2*fingerprint_count	u16[]	fingerprints	The filter body.
80+2*fingerprint_count	4	u32	body_crc32c	CRC-32C over the body bytes.

Catalog container header
------------------------

offset	size	type	name	meaning
0	4	u32	magic	"NACT".
4	2	u16	version_major	1.
6	2	u16	version_minor	0.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	8	u64	run_seq	The run that carries this catalog.
32	4	u32	entry_count	Number of entries.
36	2	u16	entry_size	64.
38	1	u8	hash_algo	Multicodec code of file_hash in every entry.
39	1	u8	digest_len	32.
40	4	u32	manifests_kept	Number of earlier manifests carried. 0 to manifest.history_depth.
44	4	u32	filters_kept	Number of earlier filters carried. Equals the number of earlier runs of the repository, a withdrawn run included (section 11.5).
48	1	u8	snapobj_form	1 one file per snapshot object under snapobj/. 2 one packed snapobj.bin.
49	3	u8[3]	reserved	Zero.
52	4	u32	reserved_u32	Zero.
56	4	u32	body_crc32c	CRC-32C over the entries.
60	4	u32	header_crc32c	CRC-32C over bytes 0 to 59.
64	-	-	entries	entry_count records of 64 bytes, in the entry order that section 11.8 states.

Catalog entry
-------------

offset	size	type	name	meaning
0	32	u8[32]	file_hash	Hash of the whole file's bytes, from its first byte to its last, under the container's hash_algo. For a snapobj/<name> file that is the object header plus the stored payload, so it does not equal the digest in the file's name, which is the hash of the uncompressed payload alone (sections 3.1 and 11.4). A reader checks file_hash before it parses the file, and the name after it has decompressed the payload.
32	1	u8	catalog_role	1 filters/<seq>.bin. 2 manifests/<seq>.bin. 3 snapshots.bin. 4 refs.bin. 5 discs.bin. 6 snapobj/<name>. 7 snapobj.bin. 8 runs.bin.
33	1	u8	hash_algo	Multicodec code of the digest in a snapobj/<name> file name. 0 for other roles.
34	2	u16	reserved_u16	Zero.
36	4	u32	reserved_u32	Zero.
40	8	u64	seq	For roles 1 and 2, the run seq the file describes. 0 otherwise.
48	8	u64	byte_len	Length of the file in bytes.
56	8	u64	reserved_u64	Zero.

Simple table container header
-----------------------------

offset	size	type	name	meaning
0	4	u32	magic	"NAST" snapshot table, "NARF" ref table, "NART" run table, "NADD" disc directory.
4	2	u16	version_major	1.
6	2	u16	version_minor	0.
8	8	u64	required_feat	Refuse on an unknown bit.
16	8	u64	optional_feat	Ignore an unknown bit.
24	16	u8[16]	repo_uuid	The repository.
40	8	u64	record_count	Records that follow.
48	2	u16	record_size	136 snapshot table, 96 ref table, 128 run table, 160 disc directory.
50	1	u8	hash_algo	Multicodec code of every digest in the records that has no hash_algo of its own. For the run table, the algorithm of run_header_hash. For the disc directory, the algorithm of super_hash.
51	1	u8	digest_len	32.
52	4	u32	reserved_u32	Zero.
56	4	u32	body_crc32c	CRC-32C over the records.
60	4	u32	header_crc32c	CRC-32C over bytes 0 to 59.
64	record_count*record_size	-	records	Sorted, fixed-width.

Snapshot table record
---------------------

offset	size	type	name	meaning
0	32	u8[32]	snapshot_id	Content id of the snapshot object.
32	32	u8[32]	parent_id	Parent snapshot id. Zero for a root.
64	32	u8[32]	root_tree	Root tree id.
96	8	u64	generation	1 + parent generation.
104	8	i64	time_sec	Snapshot time.
112	8	u64	reachable_object_count	Objects reachable, as section 6.15 counts them.
120	8	u64	first_run_seq	The run that first held the snapshot object.
128	1	u8	flags	bit0 the snapshot is complete on this set: the connectivity check, defined in the operations document, found every reachable object in this run or in an earlier run when the packer wrote this table. Clear when the check was not run or found a missing object.
129	1	u8	hash_algo	Multicodec code of snapshot_id and root_tree.
130	1	u8	parent_hash_algo	Multicodec code of parent_id. 0 for a root.
131	1	u8	reserved_u8	Zero.
132	4	u32	reserved_u32	Zero.

Run table record
----------------

offset	size	type	name	meaning
0	8	u64	run_seq	The run, 1-based.
8	8	u64	disc_seq	The disc that holds it, 0-based.
16	16	u8[16]	disc_uuid	That disc's uuid.
32	8	u64	lba_base	As in the run header.
40	8	u64	run_sectors	As in the run header.
48	32	u8[32]	run_header_hash	Hash of the 512 run header bytes (section 2.8), under the container's hash_algo. All zero in the record of the run that carries this table: that header is written after the table (section 8.6), so its hash is not yet known. The next copy of the table, written by the next run, fills the value in. Every other record of this table carries the real hash.
80	8	i64	created_sec	Pack time, as in the run header (section 7.6). Not the burn time.
88	4	u32	disc_run_index	Index of the run on its disc. 0 for the first run of a disc.
92	1	u8	run_kind	As in the run header: 1 data, 2 repair, 3 disc-close parity.
93	1	u8	run_hash_algo	Multicodec code of every object id in that run, copied from its run header. It names no digest inside this record.
94	1	u8	run_fs_profile	Disc filesystem profile of that run's disc.
95	1	u8	run_status	1 verified, 2 burned but not yet verified, 3 withdrawn by the writer after a failed verify. 0 is invalid.
96	8	u64	object_count	As in the run header.
104	24	u8[24]	reserved	Zero.

Disc directory record
---------------------

offset	size	type	name	meaning
0	16	u8[16]	disc_uuid	The disc.
16	8	u64	disc_seq	Sequence number.
24	32	u8[32]	super_hash	Hash of that disc's superblock.
56	8	u64	capacity_sectors	Reported capacity.
64	8	u64	capacity_forced_sectors	Forced capacity, section 7.14.
72	8	u64	used_sectors	Sectors used on the disc: lba_base + run_sectors of its newest run, the same value that run's disc_used_sectors holds (section 7.6). Not a sum over the runs, and not the drive's next writable address.
80	8	i64	first_burn_sec	Pack time of the disc's first run, the same value that disc's superblock holds in created_sec (section 7.5). Not the burn time, which is unknown when these bytes are hashed; the actual burn time lives only in the local state log (section 7.6; see the operations document).
88	8	i64	last_verify_sec	Last verification time. 0 when never verified.
96	4	u32	run_count	Runs on the disc.
100	1	u8	media_type	Media type registry.
101	1	u8	fs_profile	Filesystem profile id.
102	1	u8	health	1 healthy, 2 degraded, 3 critical, 4 failed, 5 unknown, 6 unverified, this disc.
103	1	u8	state_flags	bit0 closed, bit1 append-raw-only, bit2 spare below the threshold, bit3 capacity forced.
104	2	u16	rs_margin_percent	Worst-stripe margin, as a percentage of m, rounded down and clamped to 0 to 100.
106	2	u16	spare_remaining_percent	Remaining POW spare, as a percentage, rounded down and clamped to 0 to 100.
108	4	u32	label_len	Byte length of the label.
112	48	u8[48]	label	UTF-8, zero-padded. The first 48 bytes of the superblock label, which is the printed part.

Checksum sector header
----------------------

offset	size	type	name	meaning
0	4	u32	magic	"NACS".
4	4	u32	stripe_index	i, the stripe whose data digests follow. Equals the sector's own index inside the column.
8	2	u16	digest_count	k. 231 in version 1.
10	1	u8	digest_bytes	8.
11	1	u8	hash_algo	0x1e, BLAKE3.
12	4	u32	header_crc32c	CRC-32C over bytes 0 to 11.
16	1848	u8[1848]	digests	k digests, in data column order 0 to k - 1.
1864	184	u8[184]	reserved	Zero.

4. MAGIC VALUES
===============

NAOB	4e 41 4f 42	0x424f414e
NADS	4e 41 44 53	0x5344414e
NARH	4e 41 52 48	0x4852414e
NALY	4e 41 4c 59	0x594c414e
NAMF	4e 41 4d 46	0x464d414e
NAFL	4e 41 46 4c	0x4c46414e
NAST	4e 41 53 54	0x5453414e
NARF	4e 41 52 46	0x4652414e
NART	4e 41 52 54	0x5452414e
NADD	4e 41 44 44	0x4444414e
NACT	4e 41 43 54	0x5443414e
NATR	4e 41 54 52	0x5254414e
NASN	4e 41 53 4e	0x4e53414e
NACL	4e 41 43 4c	0x4c43414e
NABD	4e 41 42 44	0x4442414e
NABT	4e 41 42 54	0x5442414e
NACS	4e 41 43 53	0x5343414e
NAXL	4e 41 58 4c	0x4c58414e

5. CHUNKING CONSTANTS
=====================

Gear table generation rule
--------------------------

seed = "noahsark/gear/v1"                      # 16 ASCII bytes, no terminator

for i in 0 .. 255:
    input      = seed || u8(i)                 # 17 bytes
    digest     = BLAKE3-256(input)             # 32 bytes
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

6. FILTER QUERY RULE
====================

mix(h)   : h ^= h >> 33; h *= 0xff51afd7ed558ccd; h ^= h >> 33;
           h *= 0xc4ceb9fe1a85ec53; h ^= h >> 33; return h       # 64-bit, wrapping
contains(key):
    h  = mix(key + seed)                                      # wrapping add
    f  = u16(h ^ (h >> 32))
    h0 = u32( (h * segment_count_length) >> 64 )              # high half of the 128-bit product
    h1 = h0 + segment_length
    h2 = h1 + segment_length
    h1 = h1 ^ (u32(h >> 18) & segment_length_mask)
    h2 = h2 ^ (u32(h)       & segment_length_mask)
    return f == fingerprints[h0] ^ fingerprints[h1] ^ fingerprints[h2]

7. CHECKSUM PARAMETERS
======================

CRC32C_POLYNOMIAL	0x1edc6f41
CRC32C_REFLECTED	1
CRC32C_INIT	0xffffffff
CRC32C_XOROUT	0xffffffff
SECTOR_BYTES	2048
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
| D013 | 2.1 | D014 | 2.1 | D015 | 2.2 | D016 | 2.3 | D017 | 2.4 | D018 | 2.4 |
| D019 | 2.4 | D020 | 2.4 | D021 | 2.6 | D022 | 2.7 | D023 | 2.7 | D024 | 2.7 |
| D025 | 2.7 | D026 | 13 | D027 | 2.8 | D028 | 12.1 | D029 | 2.10 | D030 | 3.1 |
| D031 | 3.1 | D032 | 3.1 | D033 | 3.2 | D034 | 3.2 | D035 | 3.3 | D036 | 3.3 |
| D037 | 3.4 | D038 | 3.5 | D039 | 3.5 | D040 | 3.5 | D041 | 3.5 | D042 | 3.5 |
| D043 | 3.6 | D044 | 3.6 | D045 | 3.6 | D046 | 3.6 | D047 | 3.7 | D048 | 4.1 |
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
| D109 | 6.9 | D110 | 6.9 | D111 | 6.9 | D112 | 6.9 | D113 | 6.11 | D114 | 6.11 |
| D115 | 6.11 | D116 | 6.12 | D117 | 6.12 | D118 | 6.13 | D119 | 6.10 | D120 | 6.10 |
| D121 | 6.10 | D122 | 6.6 | D123 | 6.6 | D124 | 6.9 | D125 | 6.6 | D126 | 6.6 |
| D127 | 6.6 | D128 | - | D129 | - | D130 | - | D131 | - | D132 | - |
| D133 | - | D134 | - | D135 | - | D136 | - | D137 | 2.11 | D138 | - |
| D139 | - | D140 | - | D141 | 6.15 | D142 | 6.15 | D143 | 6.15 | D144 | 6.15 |
| D145 | 6.15 | D146 | 6.15 | D147 | 6.15 | D148 | 6.15 | D149 | 6.15 | D150 | 6.18 |
| D151 | 6.18 | D152 | 6.18 | D153 | 6.18 | D154 | 6.18 | D155 | 6.18 | D156 | 6.19 |
| D157 | 6.20 | D158 | 6.20 | D159 | 7.1 | D160 | 7.1 | D161 | 7.1 | D162 | 7.2 |
| D163 | 7.2 | D164 | 7.2 | D165 | 7.2 | D166 | 7.5 | D167 | 7.5 | D168 | 7.5 |
| D169 | 7.5 | D170 | 7.5 | D171 | 7.5 | D172 | 7.5 | D173 | - | D174 | - |
| D175 | 7.12 | D176 | - | D177 | - | D178 | - | D179 | - | D180 | - |
| D181 | - | D182 | 7.12 | D183 | - | D184 | 7.2 | D185 | - | D186 | - |
| D187 | - | D188 | 7.13 | D189 | - | D190 | 7.14 | D191 | - | D192 | 7.15 |
| D193 | - | D194 | 7.3 | D195 | 7.3 | D196 | 7.3 | D197 | 7.3 | D198 | 7.3 |
| D199 | 7.3 | D200 | 7.6 | D201 | 7.6 | D202 | 7.6 | D203 | 7.6 | D204 | 7.6 |
| D205 | 7.6 | D206 | 7.6 | D207 | 7.6 | D208 | 7.6 | D209 | 7.7 | D210 | 7.7 |
| D211 | 7.7 | D212 | 7.7 | D213 | 7.7 | D214 | 7.9 | D215 | 7.9 | D216 | 7.9 |
| D217 | 8.3 | D218 | 7.10 | D219 | 7.10 | D220 | 7.10 | D221 | 7.10 | D222 | 7.10 |
| D223 | 7.10 | D224 | 7.10 | D225 | 7.10 | D226 | 7.10 | D227 | 7.10 | D228 | 7.10 |
| D229 | 7.11 | D230 | 7.11 | D231 | 8.1 | D232 | 8.1 | D233 | - | D234 | - |
| D235 | - | D236 | - | D237 | 8.6 | D238 | - | D239 | - | D240 | - |
| D241 | - | D242 | - | D243 | 7.5 | D244 | - | D245 | - | D246 | 8.1 |
| D247 | - | D248 | 3.5 | D249 | - | D250 | - | D251 | - | D252 | - |
| D253 | - | D254 | 8.2 | D255 | 8.2 | D256 | 8.2 | D257 | 8.4 | D258 | 8.4 |
| D259 | 8.5 | D260 | 8.5 | D261 | 8.5 | D262 | 8.5 | D263 | 8.7 | D264 | 8.7 |
| D265 | 8.7 | D266 | 8.6 | D267 | 8.6 | D268 | 8.6 | D269 | 8.6 | D270 | 8.6 |
| D271 | 8.6 | D272 | 8.6 | D273 | 8.6 | D274 | 8.6 | D275 | 8.6 | D276 | 8.6 |
| D277 | 8.6 | D278 | 8.6 | D279 | 8.6 | D280 | 8.6 | D281 | 8.6 | D282 | 8.6 |
| D283 | - | D284 | 8.6 | D285 | - | D286 | - | D287 | - | D288 | - |
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
| D367 | 11.7 | D368 | 11.1 | D369 | 11.1 | D370 | 11.1 | D371 | 11.1 | D372 | 11.1 |
| D373 | 11.1 | D374 | 11.1 | D375 | 11.1 | D376 | 11.1 | D377 | 11.1 | D378 | 11.1 |
| D379 | 11.1 | D380 | 11.1 | D381 | 11.1 | D382 | 11.1 | D383 | 11.2 | D384 | 11.2 |
| D385 | 11.2 | D386 | 11.2 | D387 | 11.2 | D388 | 11.2 | D389 | 11.2 | D390 | 11.2 |
| D391 | 11.2 | D392 | 11.3 | D393 | 11.3 | D394 | 11.3 | D395 | 11.3 | D396 | 11.3 |
| D397 | 11.3 | D398 | 11.3 | D399 | 11.4 | D400 | 11.4 | D401 | 11.4 | D402 | 11.4 |
| D403 | 11.4 | D404 | 11.4 | D405 | 11.4 | D406 | 11.4 | D407 | 11.5 | D408 | 11.5 |
| D409 | 11.5 | D410 | 11.5 | D411 | 11.5 | D412 | 11.5 | D413 | 11.5 | D414 | 11.5 |
| D415 | 11.5 | D416 | 11.5 | D417 | 11.6 | D418 | 11.6 | D419 | 11.6 | D420 | 11.7 |
| D421 | 11.7 | D422 | 11.7 | D423 | 11.7 | D424 | 11.8 | D425 | 11.8 | D426 | 11.8 |
| D427 | 11.8 | D428 | 11.9 | D429 | 11.9 | D430 | 11.9 | D431 | 11.10 | D432 | 11.10 |
| D433 | 11.10 | D434 | 11.10 | D435 | 12.3 | D436 | - | D437 | - | D438 | - |
| D439 | - | D440 | - | D441 | - | D442 | - | D443 | - | D444 | - |
| D445 | - | D446 | - | D447 | - | D448 | - | D449 | - | D450 | - |
| D451 | - | D452 | - | D453 | - | D454 | - | D455 | - | D456 | - |
| D457 | - | D458 | - | D459 | - | D460 | - | D461 | - | D462 | - |
| D463 | - | D464 | - | D465 | - | D466 | - | D467 | - | D468 | - |
| D469 | - | D470 | - | D471 | - | D472 | - | D473 | - | D474 | - |
| D475 | - | D476 | - | D477 | - | D478 | - | D479 | - | D480 | - |
| D481 | - | D482 | - | D483 | 8.6 | D484 | - | D485 | - | D486 | - |
| D487 | - | D488 | - | D489 | - | D490 | - | D491 | 11.2 | D492 | - |
| D493 | 11.2 | D494 | - | D495 | - | D496 | - | D497 | - | D498 | - |
| D499 | - | D500 | - | D501 | - | D502 | - | D503 | - | D504 | - |
| D505 | - | D506 | 6.7 | D507 | 6.7 | D508 | - | D509 | - | D510 | - |
| D511 | - | D512 | 6.16 | D513 | 6.16 | D514 | 6.16 | D515 | 6.16 | D516 | - |
| D517 | 6.17 | D518 | 6.17 | D519 | 6.17 | D520 | - | D521 | - | D522 | - |
| D523 | - | D524 | - | D525 | - | D526 | 6.15 | D527 | - | D528 | - |
| D529 | - | D530 | - | D531 | - | D532 | - | D533 | 11.10 | D534 | 11.10 |
| D535 | 11.10 | D536 | - | D537 | - | D538 | - | D539 | - | D540 | - |
| D541 | - | D542 | - | D543 | - | D544 | - | D545 | - | D546 | 12.1 |
| D547 | - | D548 | - | D549 | - | D550 | - | D551 | - | D552 | - |
| D553 | - | D554 | - | D555 | - | D556 | - | D557 | - | D558 | - |
| D559 | - | D560 | - | D561 | - | D562 | - | D563 | - | D564 | - |
| D565 | - | D566 | - | D567 | - | D568 | - | D569 | - | D570 | - |
| D571 | - | D572 | - | D573 | - | D574 | - | D575 | - | D576 | - |
| D577 | - | D578 | - | D579 | 12.8 | D580 | 12.8 | D581 | 12.5 | D582 | 12.5 |
| D583 | 12.5 | D584 | 12.2 | D585 | 12.1 | D586 | 12.6 | D587 | 12.4 | D588 | 12.4 |
| D589 | 12.7 | D590 | 12.4 | D591 | - | D592 | - | D593 | - | D594 | 2.11 |
| D595 | 2.11 | D596 | 2.11 | D597 | 2.11 | D598 | 2.11 | D599 | - | D600 | 2.11 |
| D601 | - | D602 | - | D603 | 1 | D604 | - | D605 | - | D606 | - |
| D607 | - | D608 | - | D609 | - | D610 | - | D611 | 13 | D612 | 13 |
| D613 | - | D614 | 4.8 | D615 | - | D616 | 12.1 | D617 | 12.7 | D618 | - |

Decisions listed: 618. Carried by this document: 390. Not in this document: 228.

