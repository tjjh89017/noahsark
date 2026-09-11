# NoahsArk design notes

Document version 3.1.

**Nothing in this document is normative.** It carries rationale, evidence,
research summaries, comparison tables, worked examples, estimates, the
glossary, the references, the implementation notes and the change log.
FORMAT.md holds every rule about on-disc bytes and reader behaviour.
OPERATIONS.md holds every rule about the CLI, the configuration, staging,
the burning workflow and restore. Where this document and either of those
two disagree, they win. A number here that also appears there is a copy for
convenience; the copy is never the definition.

## Table of contents

1. [Purpose and goals](#1-purpose-and-goals)
   - [1.1 Goals](#11-goals)
   - [1.2 Non-goals](#12-non-goals)
   - [1.3 Platform tiers](#13-platform-tiers)
   - [1.4 Priorities](#14-priorities)
   - [1.5 Implementation phases](#15-implementation-phases)
   - [1.6 Performance and resource targets](#16-performance-and-resource-targets)
   - [1.7 Threat model summary](#17-threat-model-summary)
   - [1.8 Architecture](#18-architecture)
2. [Design rationale by topic](#2-design-rationale-by-topic)
   - [2.1 Binary format rules](#21-binary-format-rules)
   - [2.2 Identity and hashing](#22-identity-and-hashing)
   - [2.3 Chunking](#23-chunking)
   - [2.4 Compression](#24-compression)
   - [2.5 Objects](#25-objects)
   - [2.6 Disc and run model](#26-disc-and-run-model)
   - [2.7 Filesystem profiles and the volume tree](#27-filesystem-profiles-and-the-volume-tree)
   - [2.8 Capacity and the reserve](#28-capacity-and-the-reserve)
   - [2.9 Forward error correction](#29-forward-error-correction)
   - [2.10 Filters, manifests and the catalog](#210-filters-manifests-and-the-catalog)
   - [2.11 Packing and locality](#211-packing-and-locality)
   - [2.12 Commit, scheduling and remote sources](#212-commit-scheduling-and-remote-sources)
   - [2.13 Restore](#213-restore)
   - [2.14 File metadata](#214-file-metadata)
   - [2.15 Evolution and conformance](#215-evolution-and-conformance)
   - [2.16 Failure modes](#216-failure-modes)
   - [2.17 Testing](#217-testing)
   - [2.18 Rejected and superseded alternatives](#218-rejected-and-superseded-alternatives)
   - [2.19 Design changes from the superseded design](#219-design-changes-from-the-superseded-design)
3. [Open judgement calls](#3-open-judgement-calls)
4. [Evidence and research](#4-evidence-and-research)
   - [4.1 Why true UDF multi-session is not possible](#41-why-true-udf-multi-session-is-not-possible)
   - [4.2 growisofs internals](#42-growisofs-internals)
   - [4.3 Debian patch history](#43-debian-patch-history)
   - [4.4 UDF revision support per operating system](#44-udf-revision-support-per-operating-system)
   - [4.5 UDF measured limits and reader issues](#45-udf-measured-limits-and-reader-issues)
   - [4.6 ISO 9660:1999 level 4 evidence](#46-iso-96601999-level-4-evidence)
   - [4.7 Blu-ray media facts and damage patterns](#47-blu-ray-media-facts-and-damage-patterns)
   - [4.8 dvdisaster comparison](#48-dvdisaster-comparison)
   - [4.9 Paper summaries](#49-paper-summaries)
5. [Worked examples](#5-worked-examples)
   - [5.1 Capacity estimator, BD-R SL 25 GB, profile 1](#51-capacity-estimator-bd-r-sl-25-gb-profile-1)
   - [5.2 The same disc under profile 0](#52-the-same-disc-under-profile-0)
   - [5.3 Capacity estimator, BD-R XL TL 100 GB](#53-capacity-estimator-bd-r-xl-tl-100-gb)
   - [5.4 The dry-run printout](#54-the-dry-run-printout)
   - [5.5 Disc and run layout on the medium](#55-disc-and-run-layout-on-the-medium)
   - [5.6 Append cost tables](#56-append-cost-tables)
   - [5.7 Seek tables](#57-seek-tables)
   - [5.8 Filter, manifest and table size tables](#58-filter-manifest-and-table-size-tables)
   - [5.9 Burst tolerance tables](#59-burst-tolerance-tables)
   - [5.10 Tree entry worked size example](#510-tree-entry-worked-size-example)
   - [5.11 Restore time examples](#511-restore-time-examples)
6. [Implementation notes](#6-implementation-notes)
7. [Scheduling and operations examples](#7-scheduling-and-operations-examples)
8. [Glossary](#8-glossary)
9. [References](#9-references)
10. [Change log](#10-change-log)
11. [Decision index](#11-decision-index)

---

## 1. Purpose and goals

NoahsArk is a backup system for write-once optical media. It writes
content-addressed objects to Blu-ray discs. It reads them back years later.

The system has five properties: content addressing, deduplication,
self-description, self-healing and locality. FORMAT.md section 1 states them
as the scope of the format.

The unit of writing is the **run**. One run is one execution of one burn plan.
Under profile 0 that is one growisofs write. A disc holds one or more runs. The
disc always has exactly one physical session. Section 4.1 of this document
gives the evidence for why true UDF multi-session is not possible with current
open source tools.

The system targets a 30-year archive. The format therefore prefers explicit
byte layouts over parsers, fixed-width records over variable-length records,
and plain files over databases. A human in 2050 must be able to read a disc
with a hex editor and the `README.txt` that the disc itself carries. That
single sentence is the source of most of the decisions in section 2.

The local index is an accelerator. It is never a source of truth. A user can
delete the whole local cache. The discs still answer every question.

### 1.1 Goals

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

### 1.2 Non-goals

1. No encryption in this version. The format reserves the fields; FORMAT.md
   section 6.19 holds the reservation.
2. No signing in this version. The format reserves the fields.
3. No repository access-control model. The discs are physical objects.
4. No network protocol. NoahsArk is a local tool.
5. No delta compression between objects. A broken delta chain on write-once
   media is unrepairable.
6. No ISO 9660 bridge, no Joliet, no Rock Ridge.
7. No Go UDF writer. The system uses mkudffs and the Linux kernel udf driver.
8. No mandatory database. SQLite and other engines are dependency risks on a
   30-year medium.
9. No snapshot retention and no expiry. Version 1 never removes a snapshot and
   never frees disc space.

**What `forget` would mean.** A retention policy on write-once media can only
delete a pointer. A future `forget` would append a ref record that drops a
name, or a snapshot table record that marks a snapshot superseded; it would
never erase an object, never shorten a parent chain, and never recover a
sector. Disc space is freed only by not burning a disc. The objects of a
forgotten snapshot stay readable by content id, and the connectivity check
still finds them, so a `forget` is reversible while the discs exist. The
format already carries what such a command needs: the ref table is a reflog
(FORMAT.md section 6.18), and the snapshot table has a `flags` byte
(FORMAT.md section 11.4). No structure changes when the command arrives.
Local staging retention (`staging.retain_after_clean`, OPERATIONS.md section
4.5) is a different thing: it governs the local copy of an object that a disc
already holds.

### 1.3 Platform tiers

| Tier | Platform | Read | Burn | Notes |
|---|---|---|---|---|
| 1 | Linux 2.6.26 and newer | Yes | Yes | Reference platform. |
| 2 | Windows Vista to 11 | Yes | Later | ImgBurn 2.5.8.0 is the burn candidate. |
| 2 | Windows XP | Yes | No | Reads UDF 2.01, which is its ceiling. |
| 2 | macOS 10.4 to 15 | Yes | Later | `hdiutil burn`, or dvd+rw-tools from Homebrew. |
| - | FreeBSD | Not supported | No | Its kernel reads UDF 1.50 only. |

FreeBSD is not a target. Its UDF ceiling is 1.50, so it cannot mount a UDF
2.01 volume. A user with FreeBSD-hosted data reads the disc on Linux, or reads
the raw device with `dd` and mounts the image on Linux. NoahsArk must not lower
its UDF revision to close this gap, because UDF 1.50 loses features that
Windows and macOS need.

Tier-2 burners are surveyed in section 2.7.

### 1.4 Priorities

OPERATIONS.md section 1 rule 1.8 is the rule: the priorities are ordered, and a
conflict between two of them is resolved by that order. The list below is a
copy for convenience and is never the definition.

1. Data durability. Never lose bytes.
2. Readability without the tool. A future reader must have a chance.
3. Restore usability. Fewest disc swaps, clearest plan.
4. Deduplication ratio.
5. Speed.
6. Media utilization.

This section explains why priority 4 sits below priority 3. A disc costs a
small amount of money. A disc swap costs a minute of human attention on every
future restore. The capping knobs of OPERATIONS.md section 8.2 turn that
ordering into numbers. Section 2.11 gives the arithmetic.

### 1.5 Implementation phases

The design follows KISS: the simplest disc model that works is the default,
and the harder models are later phases.

**The on-disc format is complete from Phase 1.** The run chain, the LBA
extents, the catalog copies, the manifests, the filters and the parity layout
are all present in Phase 1. Later phases add no format change. A Phase 3
reader reads a Phase 1 disc. A Phase 1 reader reads a Phase 3 disc, unless
that disc uses a feature bit that Phase 1 does not know, or a disc filesystem
profile that Phase 1 does not implement; FORMAT.md section 12.6 holds that
rule.

| Phase | Content |
|---:|---|
| **1** | Disc filesystem profile 0 only. Sealing with `pack --close`. FastCDC chunking. Dedup with filters and manifests. Cache-less restore. Per-run Reed-Solomon parity and `verify --heal`. All media sizes. Forced capacity and forced reserve. The staging state machine and GC, with a mandatory `verify` before an object becomes CLEAN. Manual `commit` only, with no watcher. The disc-major restore plan. The Phase 1 metadata set: type, mode, uid, gid, names, mtime, ctime, symlink target and hardlink group. |
| **2** | The `sync` mirror wrapper over rsync. Extended metadata: extended attributes, POSIX ACLs, atime and birth time, Windows attributes. Disc filesystem profile 1: POW append on UDF with the image mirror and the block diff. The LBA stability check after every append. The `close` and `append` commands, and the `when_full` close policy. Repair runs. The raw append degraded mode. Spare area monitoring. |
| **3** | Watch mode, the change-recording daemon. Disc-close parity. The cross-disc parity disc. Mirror bookkeeping. `consolidate`. Multi-drive restore. `reindex`. Disc filesystem profile 2, ISO 9660:1999 level 4 with `growisofs -M`. Burning on tier-2 operating systems. |
| **Backlog** | Commit bundles: the binary on the data host, `catalog export`, `commit --out`, `import`, and deployment mode C. Specified in full and reserved in the format, but not scheduled for any phase. |

**Backlog** means specified, reserved in the on-disc format, and not
scheduled. A backlog item may never be built. Nothing else waits for it, and
building it later changes no structure, because its format is already frozen.

OPERATIONS.md sections 16 and 17 tag every command, option and config key with
its phase, and OPERATIONS.md section 16.1 holds the refusal rule for a command
of a later phase.

The Phase 1 disc model is deliberately simple. A disc holds exactly one run.
The run is written once. Nothing is ever overwritten. The disc is left open, so
a later phase can add a repair run, extra parity, or the leftover space, with
no format change. Every hard problem of appending is therefore a Phase 2
problem, and a Phase 1 disc is restored by a Phase 3 reader with no special
case.

### 1.6 Performance and resource targets

Each row names the document that holds the rule. Nothing in this table is
itself a rule; the home column names the file that carries one. A row marked
**rule** describes a target that the named home states as a rule, and the home
is the only place that binds. A row marked **budget** describes a design goal
that the reference implementation meets and that no document makes a rule; a
slower implementation still conforms.

| Item | Target | Class | Home |
|---|---|---|---|
| Commit cost on an unchanged file | One `stat`. The file is never opened. | rule | OPERATIONS.md 7.2 |
| Commit cost on an unchanged directory | One tree id comparison. | rule | OPERATIONS.md 7.1 |
| Staging space for a commit | The change set, never a second copy of the source. | rule | OPERATIONS.md 7.2 |
| FEC encoder working set | 512 MiB to 1 GiB per band, at `fec.band_stripes` 2048. | budget | section 2.9 |
| FEC encode time, 25 GB run | Under 2 minutes on one core. Never the bottleneck against a 4x burn. | budget | section 2.9 |
| Parity overhead per run | `m / (k + 1 + m)` = 9.02 percent of the stripe. | rule | FORMAT.md 10.1 |
| Catalog cost per run | Under 0.16 percent of a 25 GB disc at 2,000 runs. | budget | FORMAT.md 11.1, 11.7 |
| Catalog cap per run | `catalog.max_bytes`, default 512 MiB. | rule | FORMAT.md 11.7 |
| Manifest cost per run | 64 bytes per object, about 0.0015 percent of the disc. | rule | FORMAT.md 11.2 |
| Filter false-positive rate | 2^-16 per run. | rule | FORMAT.md 11.1 |
| UDF overhead per object, P4 | About 3.1 KiB, under 0.1 percent of a 25 GB disc. | budget | FORMAT.md 4.3 |
| Duplication overhead per repository | Warn above 5 percent. | budget | OPERATIONS.md 8.4 |
| Restore peak staging | `restore.staging_budget`, default 16 GiB. Above it the planner splits into passes. | rule | OPERATIONS.md 14.3 |
| Restore disc switches | One per disc in the plan, which is the minimum. | rule | OPERATIONS.md 14.2 |
| Restore read rate for planning | `restore.rate_mb_s`, default 20 MB/s. | budget | OPERATIONS.md 14.5 |
| Restore fixed cost per switch | `restore.switch_seconds`, default 60 s. | budget | OPERATIONS.md 14.5 |
| Scrub throughput | About 15 minutes per 25 GB disc, about 20 discs per drive-day. | budget | OPERATIONS.md 13.3 |
| Library scrub cycle | Under 12 months. | budget | OPERATIONS.md 13.3 |
| Cache rebuild, level 1 | One disc mount, a few seconds. | budget | OPERATIONS.md 2.5 |
| Cache rebuild, level 3 | One mount per disc, about 0.3 s of reading each. | budget | OPERATIONS.md 2.5 |
| Reindex table cost | About 72 bytes per object; 15 minutes per 25 GB disc to build. | budget | FORMAT.md 3.7 |
| Profile 1 append cost | About one block per new object. | budget | section 5.6 |
| Profile 2 append cost | The whole directory tree, about 107 MiB at 100,000 objects. | budget | section 5.6 |

### 1.7 Threat model summary

The adversary is accident, decay and a hostile input, not a person with the
disc in hand. NoahsArk defends against a damaged medium, a substituted or
reordered disc, a corrupt cache, a malformed structure, and a crafted archive
that tries to make the restorer write outside its target. It does not defend
against anyone who holds the physical disc.

What is **not** defended, stated plainly:

- **No encryption.** Every object payload is plaintext. Possession of a disc
  is access to every byte on it. The fields are reserved and unused;
  FORMAT.md section 6.19 holds them.
- **No signing and no authentication of origin.** Nothing proves who wrote a
  disc. A content id proves that bytes match a name; it proves nothing about
  the author. An adversary who can write a whole coherent disc set, with a
  matching superblock chain, can present it as the repository.
- **No access control.** There is no user model, no permission model and no
  audit trail. File system permissions on the repository and the cache are
  the only barrier, and the locks of OPERATIONS.md section 6 are advisory,
  not a security boundary.
- **No secrecy of metadata.** Path names, sizes, times and ownership are
  stored in the clear inside tree objects, and `README.txt` on every disc
  explains how to read them.
- **No protection against a deliberate downgrade of the local machine.** The
  repository config, the state log and the local ref log are ordinary files.
  An attacker with write access to them can make the next commit drop data.
  The discs already written are unaffected.

What **is** defended is a set of invariants, each stated once in the section
that owns it. FORMAT.md section 2.11 is the joined table. Every item in it
holds against malformed input as well as against decay, because every
structure is validated before any field of it is used.

### 1.8 Architecture

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

The data flows themselves are operational and live in OPERATIONS.md section
2.8. The trust boundaries are normative and live in FORMAT.md section 2.11.

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

---

## 2. Design rationale by topic

The subsections follow the topic order of FORMAT.md. Each one names the
section that holds the rule, then gives the reasoning and the rejected
alternatives. Section 2.18 collects the alternatives that were rejected across
topics, with their evidence.

### 2.1 Binary format rules

Rule: FORMAT.md section 2.

Every structure is a fixed-width little-endian packed record with a version
header. The reason is the 30-year target. A packed record maps directly into
memory, needs no parser, and can be read with a hex editor and the byte-offset
table that the disc itself carries in `FORMAT.txt`.

Rejected: **JSON and SQLite on the disc.** JSON costs about four times the size
of a packed record, needs a parser, and cannot be binary-searched. A database
engine on a 30-year medium is a dependency risk: the reader in 2050 must have a
compatible engine, not just the bytes.

Rejected: **fixed-LBA raw structures.** An earlier draft placed the run header
at `m + 2` computed LBAs and found the newest run by scanning backwards from
the next writable address. It was superseded by the rule that every byte
NoahsArk writes is an ordinary file, which FORMAT.md section 7.2 states.
Hidden sectors are invisible to every operating system, they are lost when a
filesystem is rebuilt, and they make a disc look corrupt to any tool that is
not NoahsArk. The same radial spread is achieved by ordinary files, because
copy order equals LBA order: `RUN.bin` first, a header copy at the start of
each parity file, and `RUN2.bin` last.

**The endianness and alignment test.** FORMAT.md section 13 requires a
golden-file test per structure. The mechanics: the test writes a structure with
known values and compares the bytes to a checked-in file, and it also reads the
checked-in file and compares the fields. Both directions are needed. The first
catches an accidental change of layout, of padding, or of endianness; the
second catches a decoder that silently tolerates one.

### 2.2 Identity and hashing

Rule: FORMAT.md sections 3.1 to 3.7.

The default algorithm for new objects is BLAKE3-256. SHA-256 is a fully
supported alternative.

Reasons for BLAKE3 as the default:

- It runs 2 to 10 times faster than SHA-256 without SHA-NI.
- The gap is largest on the arm64 machines that people use for home archives.
- It scales across cores.
- Its internal Merkle tree localizes corruption inside a large chunk.

Reason to keep SHA-256: it is standardized in FIPS 180-4 and a future reader
can reproduce it with a shell one-liner and no library.

Rejected: **SHA-256-only ids with no agility mechanism**, as restic does. A
30-year archive will outlive at least one hash transition, and a bare untagged
digest gives a reader no way to fail loudly on an unknown algorithm. The
replacement is multihash ids with a registry, BLAKE3 as the default, and hash
epochs.

**The cost of an epoch change.** At an epoch boundary, dedup drops to zero by
default. The same bytes hash to a different id, so no new object can match an
old one. The first backup after the change rewrites the whole live data set.
For a 2 TB archive that is about 80 BD-R 25 GB discs. This is the reason to
choose the default hash carefully now and to change it at most once per decade,
and only for a break in the current algorithm. Restore is unaffected.
Verification is unaffected. Each run is self-consistent and names its own
algorithm. FORMAT.md section 3.6 holds the six fields that may cross an epoch.

Rejected: **a mandatory cross-algorithm hash mapping.** Git designed a
bidirectional SHA-1-to-SHA-256 translation layer and effectively never shipped
it, because the cost lands on every object and every lookup, and because
loose-object lookup is linear in the number of loose objects. It was rejected
here for the same reason, plus two that are specific to this system. Building
the table requires reading every chunk of every disc, about 20 hours for an
80-disc archive. And the benefit is a one-time dedup saving that dies after the
first post-epoch backup. The table survives as an explicit, optional,
rebuildable side table where correctness never depends on it; FORMAT.md
section 3.7 defines it.

Digests are never truncated. A digest is 256 bits. The 8-byte truncated digests
of the FEC checksum column are not content ids; they detect media decay only.
FORMAT.md section 10.3 holds that rule.

### 2.3 Chunking

Rule: FORMAT.md sections 4.1 to 4.9.

The chunker is FastCDC, 2020 variant, with a 64-bit Gear hash and
normalization level 2. The five techniques of FastCDC, in the order that
matters:

1. Gear rolling hash: `fp = (fp << 1) + Gear[byte]`. One shift, one lookup,
   one add per byte. No XOR-out is needed, because the shift pushes old bytes
   out.
2. Enhanced hash judgement: the mask spreads its one-bits over the high bits,
   which restores an effective window of about 48 bytes.
3. Cut-point skipping: the chunker never evaluates the hash inside the first
   `min` bytes of a chunk. It jumps the pointer to `min`.
4. Normalized chunking: `mask_s` before the average size, `mask_l` after it.
   NC level 2 adds 2 bits to `mask_s` and removes 2 bits from `mask_l`.
5. Two-byte rolling: the 2020 variant processes two bytes per step, which its
   authors present as giving identical cut points to the one-byte form. That
   claim is not adopted. FORMAT.md section 4.2 is the only definition of the
   cut points, and no two-byte variant of it is defined. Any speed
   optimization must give exactly those cut points to conform.

The Gear table and the mask constants are frozen. A changed table gives
different cut points and silently ends dedup against every existing disc.
FORMAT.md sections 4.8 and 4.9 hold them.

**Why P4 is the default.** `max = 4 * avg` and `min = avg / 4` in every
profile. That is the FastCDC paper ratio and it keeps normalization level 2 in
its intended regime. P4 gives about 6,000 objects on a 25 GB disc, which is
comfortable for a UDF directory tree, for a per-run manifest, and for a filter.
A 4 MiB chunk still catches the shifted-insert edits that a 16 MiB fixed chunk
misses. The per-object UDF cost is about 3.1 KiB: one 2048-byte block for the
File Entry ICB, about 40 bytes plus the name for the File Identifier
Descriptor in the parent directory, and on average 1 KiB of tail padding to the
block boundary. Section 5.7 holds the object-count and seek tables.

Rejected: **fixed 16 MiB chunking.** It is simple and predictable, and it
produces about 1,500 objects per 25 GB disc. A fixed cut point catches no
shifted-insert edit: a single byte inserted at the front of a file changes
every later block. FastCDC at P4 keeps UDF overhead under 0.1 percent and still
catches those edits.

Rejected: **casync-style catar stream chunking across files.** casync
serializes the whole tree into one canonical stream and then chunks that
stream, so small files deduplicate naturally as part of it. A random-access
restore of one small file then needs the stream index, and any metadata change
reshuffles the stream and therefore the cut points. On optical media, where the
goal is to read one directory in one linear pass, a reshuffling stream is the
wrong structure. Restic-style bundles give the same small-file win with stable,
independently addressable objects.

**Why small chunks are bundled.** FORMAT.md section 4.5 holds the threshold
rule. The reason is UDF overhead and seeks. One UDF File Entry costs 2,048
bytes. The directory entry costs about 1,024 more. A 40 KB photo therefore pays
3.1 KiB of overhead, which is 8 percent, and one seek on restore. A directory of
30,000 photos would become 30,000 UDF files and, at 150 ms per seek, 75 minutes
of pure seeking.

Rejected: **extent tables for sparse files.** An explicit map of data extents
per file adds a structure that must be kept consistent with the chunk list, and
it is unnecessary: an all-zero region produces identical maximum-size zero
chunks, which deduplicate to one object in the whole repository. The restorer
detects an all-zero chunk and punches a hole. `SEEK_HOLE` and `SEEK_DATA`
remain available as a read-speed optimization that does not change the format;
FORMAT.md section 4.6 holds the rule.

### 2.4 Compression

Rule: FORMAT.md sections 5.1 to 5.6.

Compression happens per chunk, after the content id is computed. That order is
what keeps compression out of identity: the same bytes give the same id whether
or not they were compressed, and a later change of encoder never breaks dedup.

**The minimum gain heuristic.** If compression saves less than
`compression.min_gain` of the chunk, the writer stores the chunk uncompressed.
The default is 5 percent. The reason is restore cost: a 2 percent gain costs a
decompression pass on every future restore and on every scrub, and it makes the
stored length unpredictable. The heuristic is applied per chunk, never per
file; FORMAT.md section 5.4 holds that.

Rejected: **whole-file compression before chunking.** It would give a better
ratio on some inputs, and it destroys deduplication completely: a one-byte
change near the start of a file changes every compressed byte after it, so
every chunk changes.

### 2.5 Objects

Rule: FORMAT.md section 6.

The object graph:

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
       |  |  +--> tree "docs"   (unchanged: prerequisite)  |
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

**Why metadata is inline in the tree entry.** The reason is read
amplification. A separate node object would cost one object read per file
instead of one per directory. On a medium with 100 ms seeks, that is the
difference between usable and unusable. A separate node also saves nothing on a
metadata-only change, because the parent tree changes either way. Section 5.10
gives the worked size example, and FORMAT.md section 6.6 gives the layout.

Rejected: **git-style metadata-free trees.** They push every POSIX field into a
second structure, which costs the read amplification above and gives nothing
back on write-once media.

Rejected: **per-file Merkle trees, BitTorrent v2 BEP 52 style.** An earlier
design built a 16 KiB-leaf Merkle tree per file. It is redundant. BLAKE3
already has an internal Merkle tree, so a corrupt region inside a chunk can be
localized without a second structure. The FEC checksum column detects
corruption per 2048-byte sector, which is finer than a 16 KiB leaf. Keeping a
third mechanism would add bytes and code for no additional detection.

Rejected: **shallow-clone-style partial graphs.** Git's shallow clone lists
commits to be treated as parentless and suppresses the complaint. Truncating
the graph and hoping is exactly wrong for a backup system: a snapshot must be
provably complete or provably incomplete. The partial-clone discipline was
adopted instead: a run declares that it is partial and lists its prerequisites
with the run that holds each, so "on another disc" is never indistinguishable
from "corrupt". FORMAT.md section 11.3 holds the prerequisite list.

### 2.6 Disc and run model

Rule: FORMAT.md section 7.

A disc holds one or more runs and exactly one physical session. Section 4.1
holds the evidence for why a second UDF session is not available. Profile 1
grows one volume in place instead of adding a UDF session, and variant 1b needs
an image mirror and a block diff. Profile 2 avoids UDF entirely and uses the
ISO 9660 merge that growisofs already supports.

The layout on the medium, with a worked example, is in section 5.5.

**What an append overwrites, and why it is safe.** NoahsArk's own structures
are append-only. The only blocks an append overwrites are the filesystem's own
metadata; FORMAT.md section 7.9 states which. Section 5.6 holds the block-count
tables. The consequence is the design's load-bearing fact: spare area
exhaustion never affects NoahsArk's own readability. It stops filesystem
directory updates only. The run chain, the layout tables and the objects are
all sequential writes past the next writable address, which need no spare
block. That is why raw append works; FORMAT.md section 7.13 defines it.

An overwritten block keeps its LBA. When that LBA lies inside an earlier run's
parity domain, the sector no longer matches the digest that the earlier run's
checksum column recorded. FORMAT.md section 10.6 states the rule: such a sector
is filesystem metadata, it is not covered by an extent record, its digest is
not compared, and the healer counts it as an erasure. The earlier run's parity
is never rewritten.

### 2.7 Filesystem profiles and the volume tree

Rule: FORMAT.md section 8, and OPERATIONS.md section 10 for the image build and
the burn paths.

**Why UDF 2.01.** The filesystem is pure UDF at revision 2.01, block size 2048.
There is no ISO 9660 bridge, no Joliet, and no Rock Ridge. A bridge gives two
independently built trees that can disagree, and only one of them gets
verified. Three reasons for 2.01:

1. It is the ceiling of Windows XP.
2. Linux reads and writes it, so the build side and the repair side stay
   symmetric.
3. `mkudffs` cannot build a UDF 2.50 Metadata Partition. Its manual states that
   it does not support revisions above 2.01 for non write-once media.

Section 4.4 holds the per-operating-system revision matrix. Revision 2.50 or
2.60 may be adopted when tooling supports it and cross-OS reads are verified.
Blu-ray *video* uses UDF 2.50. That is a video-application rule. It is not a
data-disc rule.

UDF is the only filesystem in this specification that gives lossless Unicode
names, unlimited depth, and POSIX metadata on Linux, Windows and macOS at the
same time. That is why it is the default, and it is the only correct choice if
user file names are ever written to the disc directly. Section 4.5 holds the
measured limits and the known reader issues.

**The profile 2 premise.** On-disc names belong to NoahsArk. They are
68-character lowercase hex from the charset `[0-9a-f]`. Long user names, deep
user paths, and every POSIX metadata field live inside tree objects, not on the
disc. The disc filesystem therefore needs no Unicode, no POSIX metadata, and no
long names. Rock Ridge and Joliet are forbidden, and the premise makes that
acceptable. Section 4.6 holds the measured limits, the append-cost measurement,
the OS readability matrix and the character-set analysis.

**Why profile 2 uses one fan-out level.** `genisoimage -M` rewrites the entire
directory tree and the path tables into the new session image, so the cost
grows with the object count, and it grows again with the fan-out, because every
directory costs at least one whole sector even when it is nearly empty. Two hex
levels create up to 65,536 directories, and at 2048 bytes each that floor alone
is 128 MiB per append, whatever the object count. One level caps the floor at
512 KiB. Section 5.6 holds the measured table.

**`FORMAT.txt`.** FORMAT.md section 8.5 holds the exact normative text and the
64 KiB cap. The generation rule below is informative, and it exists so that a
later minor version can extend the file without a second hand-written copy.
`FORMAT.txt` is byte-deterministic: two conforming writers of the same format
version produce the same bytes. The tool generates it from the same table
definitions that the encoder uses, so it cannot drift from the code. It holds
seven parts: format rules, registries, structures, magic values, chunking
constants, the filter query rule, and the checksum parameters.

1. The file is plain ASCII. Every line ends with one LF, 0x0A. The file ends
   with exactly one LF. No line has a trailing space. There is no byte order
   mark and no CR. No line is blank except where this rule puts one.
2. The file opens with one line `NoahsArk format major 1 minor <N>`, where
   `<N>` is the `version_minor` of the superblock in decimal, then one blank
   line. That `<N>` is the file's only substitution.
3. Each of the seven parts opens with a line holding its number, a period, a
   space and its title in capitals, then a line of `=` characters of the same
   length, then a blank line. The parts appear in the order above.
4. Each named block inside a part opens with a line holding its name, then a
   line of `-` characters of the same length, then a blank line. The blocks
   appear in the order that the list of that part gives, and no other order is
   permitted.
5. A table row is its cells separated by one tab byte, 0x09, with no padding
   and no leading or trailing tab. A numeric offset or size is decimal with no
   thousands separator. An offset or size written as an expression is written
   as that expression with every space removed. An empty cell is written as one
   `-`.
6. A byte-offset table is preceded by one header row
   `offset<TAB>size<TAB>type<TAB>name<TAB>meaning` and followed by one blank
   line. A registry table is preceded by a header row holding that table's own
   column titles and is followed by one blank line.
7. Every cell is the text of the source cell with each `\|` replaced by `|`,
   every `**` removed, every backtick removed, and every run of whitespace
   folded to one space, then trimmed. Nothing else is rewritten: a quoted
   string such as `"NAOB"` keeps its quotation marks, and a section reference
   keeps its number.
8. A magic value in part 4 is one line: the mnemonic, a tab, the four file
   bytes as two lowercase hex digits each separated by single spaces, a tab,
   and the u32 value as `0x` and eight lowercase hex digits.
9. A code block in part 5 or part 6 is copied line for line, with no change,
   and is followed by one blank line.
10. A constant in part 5 and part 7 is one line: the name, a tab, and the value
    as `0x` and lowercase hex for a mask, a polynomial or a mask-like word, and
    decimal otherwise.
11. The file is at most 64 KiB. A writer that would exceed the cap drops
    nothing; it is a defect in the writer, because the content is fixed by the
    format version.

A structure that is added in a later version is appended to the part 3 list. A
structure is never removed from the list while the major version holds.

**Tier-2 burners.** Tier-2 burning is not implemented in version 1.
`burn --print` already renders the command lines, so the remaining work is a
port, not a redesign.

| OS | Candidate | Note |
|---|---|---|
| Windows | ImgBurn 2.5.8.0, "Write image file to disc" | It writes the bytes that NoahsArk produces. It must never build the filesystem. |
| Windows | IMAPI2 | Reserved. Not evaluated. |
| macOS | `hdiutil burn <image>`, with `drutil status` for media info | Apple's burn engine is single-session only, which matches profiles 1 and 2. |
| macOS | dvd+rw-tools 7.1 from Homebrew | The same command lines transfer. |

### 2.8 Capacity and the reserve

Rule: FORMAT.md section 9 for the invariants, OPERATIONS.md section 9 for the
fill policy, the media capacity table, the reference estimator and the
overrides.

The estimator is a planning tool. Only its invariants bind. The heuristic
inputs below are the part that a later version may revisit; every one of them
appears in section 3 as an open judgement call.

- `disc.expected_runs` defaults by profile, because the profiles differ in how
  many runs a disc can receive. Profile 0 defaults to **2**: a profile 0 disc
  holds one data run at burn time, and a second slot is reserved for the repair
  run that a Phase 2 append may add later, so the catalog copy of that later run
  has somewhere to go. Reserving for 32 would still waste about 0.5 percent of
  the disc, so the default stops at 2. Profiles 1 and 2 default to 32, because
  the disc receives appends.
- `earlier_runs` is set equal to `expected_runs`. Every catalog copy carries
  every earlier run's filter, so `earlier_runs` names how many filter copies the
  estimate charges for. Setting the two equal prices every future catalog copy
  as if it were the disc's last one, which is the largest a catalog on that disc
  will ever be. It is therefore conservative rather than exact for the earlier
  copies.
- `filter_bytes` is `84 + ceil(n * 91 / 40)`: the 80-byte filter container
  header, the 4-byte body CRC, and 18.2 bits per key written as `91 / 40` bytes
  per key. It is a planning estimate, because the real body size comes from the
  sizing rule and is a little larger.
- `manifest_bytes` is one 64-byte record per object plus 4096 bytes for the
  header, the TOC, the fan-out and the small chunks.
- `table_bytes` is a planning reserve for the snapshot objects, the snapshot
  table record of each, and the ref table, run table and disc directory of one
  catalog copy. `catalog.expected_snapshots` defaults to 10,000, and
  `snapobj_bytes` is the 640-byte worst-case snapshot object payload;
  `expected_snapshots * (snapobj_bytes + 136)` prices the replicated snapshot
  objects plus their 136-byte snapshot table records, and it is the term that
  dominates `table_bytes` at scale and that the catalog cap remedy calls
  never-droppable. `catalog.table_reserve_bytes` is the residual planning
  reserve and defaults to 65,536.
- `alignment_padding` is `expected_runs * 16` sectors, which assumes exactly one
  32 KiB alignment loss per run.
- `objects_per_run` is a planning figure only; the packer never limits a run by
  it.
- `superblock_and_headers` uses the actual sizes of `README.txt` and
  `FORMAT.txt`. Both have a normative cap, 16 KiB and 64 KiB, so the term is
  never above `1 + 8 + 32 + expected_runs * 2`. The worked examples use the
  caps.
- `disc.spare_reserve_bytes` depends on `disc.spare`: 256 MiB under `min`, the
  maximum-capacity POW descriptor, and 512 MiB under `default`, the drive's
  default descriptor, which trades payload for a bigger spare area and
  therefore more future appends.

Under profile 2 the projected directory rewrite cost of section 5.6 is added to
`catalog_bytes_per_run`. The figure is large enough that it must never be
treated as noise.

The worked examples are in sections 5.1 to 5.4.

### 2.9 Forward error correction

Rule: FORMAT.md section 10.

**Why the design is what it is.** The drive already has a strong
error-correction layer. Blu-ray uses a two-code picket scheme inside every
64 KiB ECC cluster. The Long Distance Code is RS(248,216) over GF(2^8), with 32
parity symbols per codeword. The Burst Indication Subcode is RS(62,30), and its
bytes are sprinkled through the cluster. When two adjacent BIS bytes fail, the
drive marks the roughly 38 LDC bytes between them as erasures. Erasure decoding
doubles the correction power.

Two facts follow, and they decide the design:

1. The drive gives no partial credit. If decoding fails, the drive returns a
   hard read error for the whole sector or cluster. NoahsArk therefore sees
   erasures, not bit errors. That is exactly the model an erasure code wants.
2. The drive can also return silently wrong bytes. NoahsArk must add its own
   hashes to detect that case and to convert it into an erasure.

The dangerous physical failure is the contiguous run. A ring scratch, an
outer-edge degradation band, or a delamination bubble maps to one long
contiguous LBA interval, usually at a high LBA. Section 4.7 holds the damage
table. A radial scratch is harmless to any interleaved code. It hits thousands
of stripes with one sector each. The contiguous run is the case that the layout
must survive, and parity must therefore be spread across the whole radius, not
placed at the end of the disc.

**Why `k = 231`, `m = 23`.** Section 5.9 holds the burst tolerance and
configuration tables that compare four parity fractions. Read against the
damage table: 5 percent already covers a 1 mm ring scratch (736 MB) and a
1.5 mm outer band; 10 percent covers a 3 mm ring or a full 3 mm outer-edge band
on a 25 GB disc; 20 percent costs 1.86 GB of payload against the 10 percent
baseline and buys 1.86 GB more burst tolerance, and damage that large usually
means the disc is mechanically unreadable anyway. Ten percent is the knee of the
curve. That is why version 1 fixes `k = 231`, `m = 23`, and no other pair is
selectable.

Rejected: **per-object Reed-Solomon instead of per-run column RS.** Adding
parity to each object separately would allow a targeted repair with local
reads. It fails on four counts. It does not protect the filesystem metadata,
and metadata loss is what makes a disc unmountable. Its interleaving is poor: a
small object's parity sits near the object, so one burst destroys both. Its
overhead is uneven: a 4 KiB object with 10 percent parity still needs a whole
extra shard. And it does not work when the filesystem is unmountable, because
it needs the filesystem to find the object. The column layout protects
everything inside the parity domain, including the filesystem's own blocks, and
the layout table still allows a targeted repair by reading only the affected
columns.

Rejected: **two mirror discs as the only redundancy.** It costs 100 percent
overhead and cannot repair partial damage: it can only replace a whole object
from the other copy, and only while the other copy still mounts. A per-run
Reed-Solomon layer at 10 percent corrects a 2.26 GB contiguous burst on a 25 GB
disc, which covers a 3 mm ring scratch. Mirrors remain available as an optional
extra layer.

Rejected: **`mkudffs --spartable`.** A UDF sparing table is a filesystem-level
bad-block remap. It adds a second logical-to-physical indirection that breaks
the parity map: a sector that the sparing table moved is no longer where the
FEC layout says it is. Reed-Solomon already covers the failure that a sparing
table addresses, and it covers it better, because it works after the disc ages
rather than only at write time. Windows 7 and later and macOS both read sparing
tables, so this is a design choice and not a compatibility one.

**Cross-disc layers.**

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
decode cost. OPERATIONS.md section 13.4 holds the heal order.

Layer 4 has real operational cost: repair needs every disc of the group
mounted. It suits sealed archive sets burned as a complete group, with a group
size of 10 and one parity disc, or 20 and two. Mirror discs are an optional
extra layer, not the primary mechanism.

**Encoding cost and memory.** The normative outcome is only that parity must be
computable within bounded memory, never the whole run held at once. The band
size `S`, `fec.band_stripes`, and the figures below are one encoder's working
strategy for meeting that, not a requirement. Parity is computed in bands. A
band is a contiguous range of `S` stripes. The encoder reads the `S` sectors of
each of the 255 columns, encodes, and writes the parity.

- Holding all `m` parity columns in memory needs `m * L * 2048` bytes: 2.26 GB
  on a 25 GB disc at `m = 23`, and 9.0 GB on a 100 GB disc. That is too much.
- With `S = 2048` stripes, one band needs about 1 GiB. Choose `S` so that the
  working set is 512 MiB to 1 GiB.
- Total I/O stays close to one linear pass when the image is on disk.

FEC is never the bottleneck. Even at 300 MB/s on one core, encoding a 25 GB
image takes under two minutes against a burn that takes about an hour at 4x.

### 2.10 Filters, manifests and the catalog

Rule: FORMAT.md section 11.

**The index-free promise.** A local index is an accelerator. The discs answer
every question without it. The promise rests on three structures that every run
carries:

1. a **filter**, which answers "is this object probably in this run" and, when
   the answer is negative, proves absence;
2. a **manifest**, which answers exactly where an object is;
3. a **catalog**, which carries seven items: the complete snapshot objects of
   the whole repository, the filters of every earlier run, the manifests of the
   most recent runs, the snapshot table, the ref table, the run table, and the
   disc directory.

The prerequisite list, which says what a run does not contain and where it
lives, is part of the manifest. Every one of these is an ordinary file under
`/NOAHSARK/runs/<seq>/`, so a reader needs only the filesystem.

The newest disc is therefore a complete entry point. It tells a reader the
whole shape of the problem: every snapshot, every disc, and every object that
it is missing. It never says "I do not know".

Rejected: **a mandatory local index.** An earlier design required a SQLite
`index.db` that mapped every hash to a disc location. It makes a local file
load-bearing for an archive whose truth lives on shelves: losing the index would
make the discs unusable until a full rebuild. The replacement is per-run filters
and manifests plus a catalog on every disc, with the local cache demoted to an
accelerator. A CI test deletes the cache and restores from the newest image
alone.

Rejected: **a Bε tree for the cache index.** GEFS uses a Bε tree, which buffers
small updates in interior nodes and flushes them in batches. The keys here are
uniformly random hashes, so every insert flushes to a different subtree, and the
batching advantage largely disappears. The index is written in whole-run bulk
loads after a burn, not as a stream of small updates. It is read randomly and
constantly. It is fully rebuildable. A plain sorted array of fixed-width records
with a fan-out table, in the multi-pack-index shape, is merged by one linear
pass and searched by pure arithmetic. The Bε tree's advantage is one NoahsArk
does not need, and its cost is a much harder recovery story.

**Why BinaryFuse16.**

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
chunk. A Bloom filter is used only in memory, for the run that is being built,
where incremental insertion is genuinely required. A Bloom filter is never
written to a disc.

**About the filter size.** 18.2 bits per key is the asymptotic figure of the
paper. The real size is `2 * fingerprint_count + 84` bytes: 80 bytes of
container header, the u16 array, and the 4-byte body CRC. That is a little
above 18.2 bits per key at small `n`, because the size factor is larger there
and because the array is rounded up to whole segments. The planning estimate in
the reserve estimator uses 18.2 bits per key, and nothing on a disc depends on
it. Section 5.8 holds the size tables.

A reserved extension exists for very large repositories: filters for runs
`1 .. N-100` may be merged into decade-sized super filters, one filter over the
union of 100 runs' objects. A super-filter hit narrows the search to 100 runs.
It must be used only when the bundle exceeds 64 MiB. At the numbers in section
5.8 it never will.

**Manifest fan-out.** A writer must use a 16-bit fan-out, 65536 entries and
256 KiB, once a run holds more than 1,000,000 objects. It removes 8
binary-search steps and costs 256 KiB of sequential read. Optical seeks cost
about 100 ms each, so the trade is clear. FORMAT.md section 11.2 holds the
rule and the feature bit.

**The dedup rule.** FORMAT.md section 11.9 states it: never drop chunk data on
the strength of a filter; a filter hit is a hint to go read an exact manifest;
only an exact manifest hit permits dropping the data. The reason is the
asymmetry. Writing a duplicate chunk wastes a few megabytes. Dropping a needed
chunk writes a reference to an object that does not exist, onto write-once
media. That failure is silent, undetectable until a restore, and unrepairable.

The numbers make it concrete. At BinaryFuse16 with 2,000 runs, about 3 percent
of genuinely new chunks get a spurious hit. Without confirmation, roughly 3
percent of new data would be silently dropped from every backup.

### 2.11 Packing and locality

Rule: OPERATIONS.md section 8, and FORMAT.md section 8.6 for the fill order
that fixes LBAs.

**Why locality wins over dedup.** Read time is fixed by the size of the data.
The number of discs that a restore touches is what the design controls. Section
5.11 holds the worked comparison: 100 GB from 5 full discs costs about 1.8
hours with 5 minutes of overhead; the same 100 GB from 80 discs at 1.25 GB each
costs about 2.7 hours, of which 80 minutes is overhead. A disc costs a small
amount of money. A disc swap costs a minute of human attention on every future
restore. On removable media, locality wins.

**Capping.** Perfect dedup produces the worst possible restore. If a snapshot's
chunks are spread one per disc across 500 discs, restoring means 500 disc
swaps. NoahsArk therefore adopts capping, Lillibridge's technique from FAST
2013, with a run in place of a container. Their result: capping at 10 to 20
containers per 20 MB segment costs a few percent of dedup ratio and buys a 2x
to 6x restore speed-up. OPERATIONS.md section 8.2 holds the knobs and the
presets.

**The packing algorithm.** The rules of OPERATIONS.md section 8.1 and the knobs
of OPERATIONS.md section 8.2 are the requirement; any algorithm that satisfies them conforms.
The reference implementation uses two passes, plus a pre-pass.

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
order preserves locality perfectly and is a factor-2 approximation on bin
count. The hybrid takes the locality of the first and most of the utilization
of the second.

Do not chase the last percent. On a 25 GB disc, 3 percent waste is 750 MB and
costs pennies.

### 2.12 Commit, scheduling and remote sources

Rule: OPERATIONS.md section 7.

**The parent tree is the old copy.** rsync compares a source against an old
copy of the same data. NoahsArk keeps no old copy. The parent snapshot's trees
hold the size, the mtime and the ctime of every path, so they take the place of
rsync's old copy. That has one important consequence: the staging disk never
needs to hold a second copy of the source. It needs space only for the change
set.

Rejected: **a full rsync mirror of the source on the staging disk.** An earlier
form of mirror mode kept a complete second copy of the source and ran
`rsync -aHAX --delete` against it. It doubles the disk requirement for no gain,
because the parent snapshot's trees already are the old copy. `sync` now diffs
a stat listing against the parent tree and transfers only the change set with
`--files-from`.

Rejected: **including a file that changed while it was read.** Restic and borg
accept such a file and record a warning. The resulting chunk list describes a
state that never existed on the disk, and on write-once media that error is
permanent. NoahsArk reuses the last consistent version when one exists, stores a
new file with an `UNSTABLE` flag when none exists, reports the path, and retries
on the next commit.

**No built-in scheduler.** A backup tool that also schedules is two programs in
one, and every operating system already has a scheduler that is better tested.
`commit` is a batch job. Run it from cron or from a systemd timer. The
normative outcome is only this: `commit` is run periodically by some external
scheduler, and the exclusive repository lock of OPERATIONS.md section 6 is what
keeps two commits from running at once. Section 7 of this document holds the
unit examples. cron and systemd timers are already present, already tested, and
already monitored; `commit` is a batch job with a lock and clear exit codes,
which is all a scheduler needs from it.

Exit code 1, which means "some files were skipped", is normal on a live source.
A monitoring rule should alert on code 2 and on a rising unstable count, not on
code 1 alone.

**The future watch trigger.** Phase 1 has manual `commit` only. There is no
daemon. A watcher is Phase 3. It will record changed paths with `fsnotify` and
append them to a log, and `commit` will take the union of that log and the
quick check. It will never commit by itself, because a commit needs a stable
filesystem. Nothing has to change for it to arrive: `commit` is idempotent, so
a commit whose root tree equals the parent's root tree writes no new snapshot
object and moves no ref, unless `--force` is given. The watcher is therefore an
accelerator for the change scan, and it changes no format and no state.

Rejected: **real-time automatic commit in watch mode.** A daemon that commits
by itself cannot know when the source is quiescent, and a commit must run
against a stable filesystem.

**Deployment modes for a remote data host.** Three ways to back up a machine
that holds the data but is not the machine that holds the discs.

| | A: mount and commit | B: hybrid listing | C: commit bundle |
|---|---|---|---|
| Software on the data host | None | ssh and `find` | The NoahsArk binary |
| Network traffic | Every read file, plus the whole directory walk | The listing, plus the changed files | The listing is local; only new objects cross |
| Directory walk speed | Slow. One round trip per `stat` | Fast. One `find` on the data host | Fast. Local walk |
| Metadata fidelity | Limited by the mount | Limited by the mount for content, exact for the listing | Full: ctime, hardlinks, xattrs, ACLs |
| CPU location | The repository server | The repository server | The data host |
| Temporary space | None | The listing, a few MB | The bundle, the size of the change set |
| Phase | 1 | 2 | Backlog |

Mode A mounts the source over NFS and runs `commit` normally. Prefer NFS over
SMB: NFS keeps link counts, extended attributes and often ctime, and SMB keeps
few of them. OPERATIONS.md section 7.8 holds the behaviour table.

Mode B runs one command on the data host, over ssh, to produce a stat listing:

```bash
ssh nas "find /srv/data -printf '%y\t%s\t%T@\t%C@\t%m\t%U\t%G\t%D\t%i\t%n\t%p\n'"
```

`%D`, `%i` and `%n` are the device number, the inode number and the link count,
which the hardlink rule of FORMAT.md section 6.14 needs. The listing is diffed
against the parent snapshot's trees, exactly as `sync` does, and only the
changed files are read over the mount. It removes the per-file round trips of
the walk, which is what makes mode A slow on a large tree. It needs no binary on
the data host.

Mode C runs the binary on the data host and produces commit bundles. It is
Backlog, so it is not available today.

**Recommendation.** Use mode A. Move to mode B when the directory walk
dominates the commit time. Mode C is the answer when full metadata matters, or
when hardlinks, extended attributes or ACLs must survive, and it is the reason
the bundle format stays reserved.

Rejected: **a time-based transition from BURNED to CLEAN.** An earlier draft
let staging release an object after a retention period, whether or not the disc
had been read back. The burn is the one step that can fail silently on
write-once media. `verify` is now the only path to CLEAN, and `burn --exec`
runs it by default.

### 2.13 Restore

Rule: OPERATIONS.md section 14.

**The time model constants.** The formula in OPERATIONS.md section 14.5 is the
requirement. The constants below are the defaults that `restore.rate_mb_s` and
`restore.switch_seconds` replace.

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

The estimate is printed with every plan. It makes the cost of poor locality
visible and quantified.

**Multi-drive restore, Phase 3.** With `k` drives the goal changes from "fewest
discs" to "shortest makespan".

- Assign discs to drives by longest-processing-time-first list scheduling. LPT
  is a `4/3 - 1/(3k)` approximation for makespan, which is good enough.
- With a human in the loop, the human is the scarce resource. The useful
  pattern is pipelining: while drive 1 reads disc `i`, the human loads disc
  `i+1` into drive 2. Two drives remove almost all human wait from the critical
  path. Three or more help only when reads are slower than swaps.
- Do not assign two discs that complete the same file to different drives at
  very different times, or staging grows. Keep the plan order and hand each disc
  to whichever drive is free next.
- Report the per-drive queues in the plan, so the operator knows which disc goes
  into which drive.

### 2.14 File metadata

Rule: FORMAT.md section 6.6 for the field set and the encodings,
OPERATIONS.md section 15 for the restore policy.

The **format** defines every field from the start. The **implementation** is
phased:

| Field | Phase |
|---|---:|
| Entry type, mode, uid, gid, name, size | 1 |
| mtime | 1 |
| Symlink target | 1 |
| Hardlink group | 1 |
| User name and group name | 1 |
| ctime | 1 |
| atime, birth time | 2 |
| Extended attributes | 2 |
| POSIX ACL, access and default | 2 |
| Linux chattr flags | 2 |
| Windows attributes and security descriptor | 2 |
| NFSv4 ACL, BSD and macOS flags, NTFS alternate data streams | 3 |

A Phase 1 writer never emits a TLV that it does not implement. A Phase 1 reader
preserves and reports an unknown non-critical TLV, and does not apply it.

ctime is stored from Phase 1 because the quick check compares it. No platform
restores it; it is never applied, only stored.

The reason for inline metadata is read amplification; section 2.5 gives it.

**Why each encoding.**

| Item | Encoding | Reason |
|---|---|---|
| Time | `i64` seconds plus `u32` nanoseconds | A single `i64` of nanoseconds overflows on 2262-04-11 and cannot express dates before 1678. An archival format must outlive that. `struct timespec`, `utimensat` and Go's `time.Time` all use seconds plus nanoseconds, so there is no conversion and no rounding. |
| Nanoseconds | Always in `[0, 999999999]` | A negative time is a negative seconds value with a non-negative nanosecond part. |
| File type | `entry_type`, a u8 enum | Two encodings of the same fact, as in a combined `st_mode`, are a source of canonicalization bugs in a content-addressed format. |
| Mode | `u32`, low 12 bits | Permission bits only. The type is not here. |
| uid, gid | `u32`, `0xFFFFFFFF` means unknown | Numeric identity. |
| User and group name | UTF-8 TLV | Portable identity. |
| Symlink target | Raw bytes, TLV, critical | A Linux path is a byte string, not text. |
| POSIX ACL | Portable binary: count, then `{u16 tag, u16 perm, u32 id}` | The kernel `system.posix_acl_access` blob is architecture-specific and version-specific. The text form costs a parse on every restore. |
| Windows security descriptor | Opaque self-relative blob | Never parsed. Inheritance flags preserved exactly. |
| Hardlink identity | Repository-local `hardlink_group` id | Never an inode number. |

**Cross-platform capability matrix.** `Y` = applied. `~` = applied with loss or
approximation. `N` = dropped, and a warning is issued.

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
applied while owner and group become the restoring user. Treating the
descriptor as opaque must not mean carelessness: restic shipped a real bug in
which restored ACEs were always marked explicit instead of inherited.

### 2.15 Evolution and conformance

Rule: FORMAT.md sections 12.4 to 12.6.

The interop matrix of FORMAT.md section 12.6 is also the test matrix for
interop: **each cell is one CI case**, built by writing an image with one build
and reading it with another. `Y` means full use. `~` means partial use, with
the loss named. `N` means a clean refusal that names the reason. An empty
refusal, a silent skip and a misread are all defects.

The rule that every `N` cell shares: a refusal is loud, names the field and the
value, and never touches the bytes it refused. The rule that every `~` cell
shares: the data is read in full, and the loss is in the report.

### 2.16 Failure modes

Rule: OPERATIONS.md section 20 holds the recovery action for each failure. The
detection and loss columns below are the informative half.

| # | Failure | Detected by | Immediate effect | Data loss |
|---:|---|---|---|---|
| 1 | A burn fails midway | Non-zero exit from the burner, or a short read-back | The run is incomplete | None |
| 2 | Verify finds bad sectors | ddrescue mapfile, checksum column | The run is degraded | None while erasures are at or below `m` per stripe |
| 3 | Erasures exceed `m` in a stripe | RS decode refuses | Some objects are unreadable on this disc | None when another copy exists |
| 4 | A whole disc is lost or destroyed | The disc is missing from the inventory | Every object unique to it is missing | Objects unique to that disc |
| 5 | The newest disc is lost | The disc directory names a `disc_seq` that is absent | The catalog entry point is gone | Only the objects unique to the newest disc |
| 6 | The local cache is lost | The cache directory is absent | Dedup and planning are slower | None |
| 7 | The cache is wrong (uuid mismatch) | `disc_uuid` does not match the recorded `disc_seq` | The cache may give wrong answers | None |
| 8 | The state log is truncated by a crash | A record CRC fails | Some objects have an unknown state | None |
| 9 | An object moved after an append | The LBA read-back differs from the layout table | The layout table would be wrong | None |
| 10 | The spare area is exhausted | A write error, or the health report below `disc.min_spare_ratio` | Filesystem updates fail | None |
| 11 | The drive offers no POW | `GET CONFIGURATION` feature 0x38 absent | The disc cannot be appended | None |
| 12 | A filter false positive is unconfirmed | The manifest is unavailable | A chunk may be a duplicate | None. Space is wasted. |
| 12a | A file changes while it is read | The re-stat after the read | The parent entry is reused, or a new file is stored with the `UNSTABLE` flag | None. The last consistent version stays available. |
| 13 | A tree entry has an illegal name | Parse-time validation | The entry is refused | The one entry |
| 14 | An unknown critical TLV | The critical bit is set and the type is unknown | The entry is refused | None, once upgraded |
| 15 | A content id does not match after decompression | The verification step | The object is corrupt | None while parity or another copy exists |
| 16 | A restore runs out of staging space | The predicted peak against `restore.staging_budget` | The restore would stall | None |
| 17 | A required disc is missing at plan time | The inventory check | The plan is impossible | None |
| 18 | A snapshot references a missing object | The connectivity check | The snapshot is incomplete | The missing objects |
| 19 | The burner build is unpatched | The version check at startup | A burn would under-fill or fail to close | None |
| 20 | A disc is substituted | `prev_disc_super_hash` does not chain | The set ordering is wrong | None |
| 21 | Media generation goes out of production | Operator knowledge, health report | Future re-burns are impossible | None if done in time |
| 22 | A disc reaches 10 years | The health report | Rot risk is rising | None if done in time |

### 2.17 Testing

Rule: OPERATIONS.md section 22 holds the test list, and FORMAT.md section 13
holds the golden vectors.

**Image-first principle.** Every burn test uses an image file first.

1. Build the filesystem image.
2. Loop-mount it and verify.
3. Simulate an append by an image diff.
4. Simulate damage by overwriting sectors, then run `verify --heal`.
5. Simulate a lost cache.
6. Simulate a cross-append merge.

Physical burns are a manual checklist, not CI. A GitHub runner has no optical
drive. It does have `sudo`, so loop mounts work. The 59 required tests must all
run.

**The composite action.** This is the reference project's CI layout. The
normative outcome is only that every required test runs and that a physical
burn is never part of CI. Any CI system that runs the required tests conforms;
GitHub Actions is not required. Section 7 holds the workflow.

**Probes.** OPERATIONS.md section 22 holds the rule: the probe list is
normative, and each question in it must be answered before the feature that
depends on it ships. This document holds the probe list itself and the action
paths, which are informative. Any open question about tool behaviour becomes a
probe action: a small composite action that runs the experiment on an image
file and records the result as a job artifact. A probe is not a test. A test
asserts a known answer. A probe records an unknown one. When a probe answer
becomes stable, it moves into the test list with an assertion. Section 7 holds
the probe actions and the manual probes.

### 2.18 Rejected and superseded alternatives

**Superseded. Do not implement anything in this subsection.** It records what
was considered and rejected, with the reason and the evidence, so that a future
reader does not re-open a settled question. The alternatives that belong to one
topic are in that topic's subsection above. The remaining ones are here.

**ISO 9660 bridge with `genisoimage -udf`.** A bridge disc carries both an ISO
9660 tree and a UDF tree. It was rejected on measured evidence: `genisoimage
-udf` writes **UDF 1.02**, the oldest revision, with no VAT, no metadata
partition and no sparing. It is always a bridge, so two independently built
trees exist and can disagree, and only one gets verified after the burn. Which
tree wins is not under our control: Windows prefers UDF over CDFS, macOS
prefers the ISO side, and Linux depends on probe order, so two platforms read
different trees from the same disc. Thomas Schmitt also states that
multi-session with `genisoimage -udf` is "known to be problematic (or
impossible)", which would destroy the `-M` append path that is the only reason
to use ISO at all. Never pass `-udf`.

**Rock Ridge and Joliet.** Both were forbidden by decision, and the measured
evidence supports it. Rock Ridge is byte-transparent and carries POSIX
metadata, but **Windows ignores it completely**. Joliet is the only mechanism
Windows honours, and it is UCS-2, so every character above the Basic
Multilingual Plane is lost. Neither is needed, because NoahsArk's on-disc names
are 68-character lowercase hex and all real metadata lives inside tree objects.
The surprise that makes the ban acceptable is that ISO 9660:1999 level 4
preserves lowercase and long names with **zero** Rock Ridge bytes, measured as
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

**Descriptor-set-per-session UDF multi-session.** Writing a complete UDF
descriptor set per session, with a fresh anchor at `session_start + 256` and a
full directory tree that points back into earlier sessions, is spec-legal. It
was rejected for three reasons. First, no tool builds it: `mkudffs
--startblock` creates a new **empty** filesystem at an offset, measured as
`numfiles=0`, so NoahsArk would have to become a UDF writer. Second, the cost
per session is about 54 blocks of descriptors plus the entire rewritten tree,
which is about 205 MB per session at 100,000 objects, paid again every time.
Third, and decisively, **macOS sees only the first session** until the disc is
closed. The POW-growth design keeps `Number of Sessions: 1`, which sidesteps
the macOS limitation entirely.

**xorriso and libisofs.** xorriso is actively maintained and would have been
the natural choice. It was rejected on a direct source check: `grep -rniw udf`
over `libisofs-1.5.8.pl02/libisofs/` returns **zero** matches. The filesystem
writer contains no UDF code at all. The only `udf` references in the xorriso
tree are in the mkisofs argument-counting tables at `emulators.c:637` and
`:835`, which declare `-udf` as a zero-argument option to be parsed and
discarded. `xorriso -as mkisofs -udf` therefore produces a plain ISO 9660
image. As a raw image burner xorriso is capable, but that is the job growisofs
already does, so switching would buy nothing. cdrskin, which shares the same
libburn back end and does write raw images, is kept as the fallback burner
instead.

**pktcdvd packet writing.** Packet writing would have allowed ordinary
filesystem writes to optical media. It was rejected because it no longer
exists: `drivers/block/pktcdvd.c` is gone from torvalds/linux master, there is
no `CDROM_PKTCDVD` in `drivers/block/Kconfig`, and no module ships in the
Debian kernel used for testing. `pktsetup` and `cdrwtool` from udftools are
therefore dead ends.

**A built-in scheduler.** Section 2.12 holds the reasoning.

### 2.19 Design changes from the superseded design

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

## 3. Open judgement calls

Each row is a default that was chosen, not derived from a stated requirement.
A later version may revisit any of them. **No value here is changed from the
value that FORMAT.md or OPERATIONS.md records.** The "Disc bytes" column says
whether a change to the value changes what a writer puts on the medium;
FORMAT.md section 12.8 is the index that governs that column.

| Id | Key or constant | Current value | Defined in | Disc bytes | Note |
|---|---|---|---|---|---|
| OC001 | `disc.expected_runs` | 2 under profile 0, 32 under profiles 1 and 2 | OPERATIONS.md 9.5, 17.5 | Yes | Named a heuristic. 2 reserves for a hypothetical Phase 2 repair run; 32 is an unjustified round number. |
| OC002 | `earlier_runs` in the reference estimator | set equal to `expected_runs` | OPERATIONS.md 9.6 | Yes | Deliberately conservative, not exact. Prices every catalog copy as if it were the disc's last. |
| OC003 | `catalog.expected_snapshots` | 10,000 | OPERATIONS.md 17.8, 9.6 | Yes | A planning count with no derivation. It dominates `table_bytes` at scale. |
| OC004 | `catalog.table_reserve_bytes` | 65,536 | OPERATIONS.md 17.8, 9.6 | Yes | A residual planning reserve, round number. |
| OC005 | `snapobj_bytes` worst case | 640 bytes | FORMAT.md 11.4; OPERATIONS.md 9.6 | Yes | A size cap asserted, not derived from the snapshot header layout. |
| OC006 | `filter_bytes` planning figure | `84 + ceil(n * 91 / 40)`, that is 18.2 bits per key | OPERATIONS.md 9.6; FORMAT.md 11.1 | Yes | The asymptotic paper figure; the real body size from the sizing rule is a little larger. |
| OC007 | `manifest_bytes` header allowance | 4,096 bytes added to `64 * objects_per_run` | OPERATIONS.md 9.6 | Yes | Round allowance for header, TOC, fan-out and small chunks. |
| OC008 | `alignment_padding` | `expected_runs * 16` sectors | OPERATIONS.md 9.4 | Yes | Assumes exactly one 32 KiB alignment loss per run. |
| OC009 | `manifest.history_depth` | 8 | FORMAT.md 11.7; OPERATIONS.md 17.8 | Yes | Chosen so one disc yields the depth plus one manifests. Nothing fixes the value at 8. |
| OC010 | `catalog.max_bytes` | 512 MiB | OPERATIONS.md 17.8; FORMAT.md 11.7 | Yes | Round cap. |
| OC011 | `catalog.snapobj_pack_threshold` | 1,000 snapshots | OPERATIONS.md 17.8; FORMAT.md 11.4 | Yes | Round threshold for switching to `snapobj.bin`. |
| OC012 | `bundle.threshold` | 1 MiB | OPERATIONS.md 17.2; FORMAT.md 4.5 | Yes | Changes which chunks are bundled, so it changes disc bytes. Derived from a UDF overhead argument, not a requirement. |
| OC013 | `bundle.target_size` | 64 MiB | OPERATIONS.md 17.2; FORMAT.md 6.3 | Yes | Fixes bundle ids. No derivation given. |
| OC014 | `chunklist.inline_max` | 64 | OPERATIONS.md 17.2; FORMAT.md 6.4 | Yes | Explicitly a writer choice, not a format limit. |
| OC015 | `tree.tlv_spill_threshold` | 4 KiB | OPERATIONS.md 17.2; FORMAT.md 6.11 | Yes | Changes tree bytes. Round number. |
| OC016 | `chunker.profile` default | P4 (1 MiB / 4 MiB / 16 MiB) | FORMAT.md 4.3; OPERATIONS.md 17.2 | Yes | Argued from object counts and UDF overhead, not required. |
| OC017 | `hash.current` default | `blake3` | FORMAT.md 3.2; OPERATIONS.md 17.2 | Yes | A speed argument. SHA-256 is equally conforming. |
| OC018 | `compression.level` | zstd 3 | FORMAT.md 5.3; OPERATIONS.md 17.3 | Yes | "The balance point". |
| OC019 | `compression.min_gain` | 0.05 | FORMAT.md 5.4; OPERATIONS.md 17.3 | Yes | 5 percent chosen on a restore-cost argument. |
| OC020 | `k = 231`, `m = 23` | fixed for version 1 | FORMAT.md 10.1, 10.2 | Yes | Chosen as "the knee of the curve" at about 10 percent parity, from an informative comparison table. |
| OC021 | Checksum column digest width | first 8 bytes of BLAKE3-256 | FORMAT.md 10.3 | Yes | 2^-64 per sector asserted as sufficient for decay detection. |
| OC022 | `fec.band_stripes` | 2048, for a 512 MiB to 1 GiB working set | OPERATIONS.md 17.7; NOTES.md 2.9 | No | Explicitly one encoder's strategy, not a requirement. |
| OC023 | `fec.reburn_margin` | 0.50 | OPERATIONS.md 17.7, 13.5 | No | The 50 percent re-burn trigger is a round number. |
| OC024 | `fec.group_size` | 0, with 10 or 20 recommended | OPERATIONS.md 17.7 | No | Cross-disc group size is an operational guess. |
| OC025 | `disc.fill_ratio` | 0.95 | OPERATIONS.md 17.5, 9.1 | Yes | Based on "the outer 3 mm holds about 5 percent". |
| OC026 | `disc.min_fill` | 0.90 | OPERATIONS.md 17.5, 16.8 | No | Pack trigger threshold. |
| OC027 | `disc.max_wait` | 30 days | OPERATIONS.md 17.5, 16.8 | No | Pack trigger age. |
| OC028 | `disc.spare` default and `disc.spare_reserve_bytes` | `min`, 256 MiB; 512 MiB under `default` | OPERATIONS.md 17.5, 12.5, 9.4 | Yes | Approximate spare sizes ("about 256 MB", "about 512 MB"). |
| OC029 | `disc.min_spare_ratio` | 0.20 | OPERATIONS.md 17.5, 12.5 | No | Warning threshold, round number. |
| OC030 | `disc.close_policy` | `never` | OPERATIONS.md 12.2, 17.5 | No | A policy choice in favour of later appends. |
| OC031 | `fs.fanout_levels` | 1 | FORMAT.md 3.5; OPERATIONS.md 17.4 | Yes | Both 1 and 2 are acceptable; 1 is chosen. |
| OC032 | `manifest.fanout_bits` and the 16-bit switch point | 8, must be 16 above 1,000,000 objects in a run | OPERATIONS.md 17.8; FORMAT.md 11.2 | Yes | The switch point is a seek-cost argument. |
| OC033 | Filter seed search bound | at most 100 attempts, seeds 0..99 | FORMAT.md 11.1 | Yes | Round bound; failure is declared a writer defect. |
| OC034 | Filter geometry constants | `ln(n)/ln(3.33) + 2.25`, `0.875 + 0.25 * ln(1e6)/ln(n)`, cap 262144 | FORMAT.md 11.1 | Yes | Taken from the reference construction, binding only for the golden vector. |
| OC035 | `filter.rollup_threshold` | 64 MiB | OPERATIONS.md 17.8; FORMAT.md 11.1 | No | Reserved super-filter threshold that will never be reached at the projected sizes. |
| OC036 | `locality.max_source_runs` and preset | 8, preset `balanced` | OPERATIONS.md 8.2, 17.10 | Yes | From the capping paper's 10-to-20 range, halved without explanation. |
| OC037 | `locality.segment_size` | 1 GiB | OPERATIONS.md 8.2, 17.10 | Yes | Round segment size. |
| OC038 | `locality.rewrite_below_chunks` | 64 | OPERATIONS.md 8.2, 17.10 | Yes | Round threshold. |
| OC039 | `locality.max_duplicate_bytes_per_file` | 64 MiB | OPERATIONS.md 8.2, 17.10 | Yes | Round per-file cap. |
| OC040 | `locality.max_duplicate_ratio_per_file` | 0.05 | OPERATIONS.md 8.2, 17.10 | Yes | Round per-file ratio. |
| OC041 | `locality.disc_budget` | 0.03 | OPERATIONS.md 8.2, 17.10 | Yes | Round per-disc cap. |
| OC042 | Duplication overhead warning | above 5 percent | OPERATIONS.md 8.4 | No | A budget, not a requirement. |
| OC043 | `split.threshold` | 0.25 | OPERATIONS.md 8.6, 17.10 | Yes | "Wasting a quarter of a disc is cheaper" is an assertion. |
| OC044 | Consolidation triggers | 20 discs, spread 2.0, 8 hours, 5 years | OPERATIONS.md 8.7, 17.10 | No | All four are operational guesses. |
| OC045 | `staging.retain_after_clean` | 7 days | OPERATIONS.md 4.5, 17.12 | No | Round retention. |
| OC046 | `repo.lock_timeout` | 0 | OPERATIONS.md 6, 17.9 | No | Fail-at-once default. |
| OC047 | `restore.staging_budget` | 16 GiB | OPERATIONS.md 14.3, 17.13 | No | Round budget. |
| OC048 | `restore.rate_mb_s`, `restore.switch_seconds` | 20 MB/s, 60 s | OPERATIONS.md 14.5, 17.13 | No | Both marked estimated. The 60 s is a sum of estimated components. |
| OC049 | `restore.score` | `bytes` | OPERATIONS.md 14.1, 17.13 | No | Bytes chosen over object count. |
| OC050 | `scrub.first_check_hours` | 24 | OPERATIONS.md 13.3, 17.14 | No | Stated as a requirement, but the 24-hour figure itself is a judgement. |
| OC051 | `scrub.schedule` default | `3m,12m,then 12m to 5y,then 6m` | OPERATIONS.md 13.3, 17.14 | No | Explicitly "the recommended default". |
| OC052 | `scrub.degraded_interval`, `scrub.max_disc_age` | 3 months, 10 years | OPERATIONS.md 17.14, 13.5 | No | Round intervals. |
| OC053 | Library scrub cycle target | under 12 months | OPERATIONS.md 13.3; NOTES.md 1.6 | No | A budget. |
| OC054 | Health counter thresholds | LDC average below 13, BIS average below 15 | OPERATIONS.md 13.5 | No | Vendor-dependent figures. |
| OC055 | `source.mtime_slack` | 0 local, 2 s remote | OPERATIONS.md 7.8, 17.9 | No | Round slack for remote mounts. |
| OC056 | `source.checksum_every` | 30 days | OPERATIONS.md 7.5, 17.9 | No | Round rehash interval. |
| OC057 | `commit.retry_unstable` | 1 | OPERATIONS.md 7.6, 17.9 | No | One retry chosen arbitrarily. |
| OC058 | `burner.speed`, `burner.speed_mdisc` | 4, 2 | OPERATIONS.md 17.6 | No | Conservative speeds. |
| OC059 | `burn.reload_seconds` | 10 | OPERATIONS.md 17.6 | No | Tuning wait. |
| OC060 | `README.txt` and `FORMAT.txt` caps | 16 KiB and 64 KiB | FORMAT.md 8.4, 8.5 | Yes | Normative caps chosen with headroom, not derived. |
| OC061 | Gear seed string | `"noahsark/gear/v1"` | FORMAT.md 4.8 | Yes | An arbitrary but frozen 16-byte seed. |
| OC062 | Cauchy parameter choice | `x_j = k + j`, `y_i = i` | FORMAT.md 10.2 | Yes | One of many valid Cauchy assignments; frozen by fiat. |
| OC063 | `spread_mask` distribution rule | `1 << (63 - floor(j * 32 / n))`, high 32 bits only | FORMAT.md 4.9 | Yes | A specific spreading rule; other spreads would also restore the window. |
| OC064 | Append rewrite bound per earlier stripe | `floor(m / 2)` blocks | FORMAT.md 10.6 | Yes | Half of `m` chosen to keep a run above the `CRITICAL` line. |
| OC065 | Run directory zero padding | 10 decimal digits, so `run_seq` is capped at 9,999,999,999 | FORMAT.md 8.3, 2.10 | Yes | The width is a formatting choice that becomes a format limit. |
| OC066 | Ref name limit | 1 to 40 bytes | FORMAT.md 6.18, 2.10 | Yes | Declared part of the format without derivation. |
| OC067 | Disc label widths | 64 bytes in the superblock, 48 in the disc directory | FORMAT.md 7.5, 11.6, 2.10 | Yes | Two different widths for one label. |
| OC068 | Shelf note width | 128 bytes | OPERATIONS.md 3.4; FORMAT.md 2.10 | No | Round width. Local file only. |
| OC069 | Burn step path limits | `source_path` 256 bytes, `aux_path` 184 bytes | OPERATIONS.md 11.3; FORMAT.md 2.10 | No | Chosen to make the step record exactly 512 bytes. |
| OC070 | On-disc name and path caps | 126 characters, under 220 characters | FORMAT.md 8.7, 2.10 | Yes | 126 is half of a UDF limit; 220 is a Windows `MAX_PATH` margin. |
| OC071 | `init --scan-discs` safety gap | add at least 100 to both sequence numbers | OPERATIONS.md 16.2 | No | A recommendation with a round number. |
| OC072 | Filter false-positive target | 2^-16 per run, via BinaryFuse16 | FORMAT.md 11.1 | Yes | Chosen from a 2,000-run spurious-hit calculation. |
| OC073 | `restore.drives` | 1 | OPERATIONS.md 17.13 | No | Single-drive default; multi-drive is Phase 3. |
| OC074 | `metadata.atime`, `btime`, `xattr`, `acl`, `windows` | all false | OPERATIONS.md 17.11 | Yes | Off by default on a tree-churn argument. |
| OC075 | Object-count planning figure | `objects_per_run = ceil(capacity_forced * 2048 / chunk_avg)` | OPERATIONS.md 9.6 | Yes | A planning figure only; the packer never limits a run by it. |

---

## 4. Evidence and research

Every quotation in this section is reproduced as it was recorded. Every number
is as measured.

### 4.1 Why true UDF multi-session is not possible

This subsection records what was checked, where, and when. Re-open the question
only with newer evidence than this.

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

This same gate is what makes profile 2 work: an ISO 9660 volume passes it.

**Block 2: mkudffs cannot build a session that references an earlier session.**
`mkudffs --startblock` positions a new, empty filesystem at an offset. It does
not merge one. Section 2.18 holds the test and the source check.

**Block 3: the kernel cannot write a VAT volume.** A Virtual Allocation Table
is the UDF mechanism for write-once append. The Linux kernel forces read-only on
every write-once volume, so a VAT volume can never be populated on Linux.
Section 2.18 holds the kernel code and the mount test.

**xorriso is not a way out.** Its filesystem writer contains no UDF code.
Section 2.18 holds the source check.

**Conclusion.** No open source tool bridges "writes bytes" and "writes UDF".
mkudffs writes empty UDF volumes. The kernel writes UDF on media that accept
random sector writes. Every burner writes bytes.

Profile 1 uses the one path that remains: a POW-formatted BD-R **is** a medium
that accepts random sector writes, so the kernel udf driver can maintain the
volume. Variant 1a lets the kernel write the disc directly. Variant 1b keeps a
local mirror and pushes the changed blocks through growisofs. Neither adds a
session, so the disc keeps `Number of Sessions: 1`.

Profile 2 avoids UDF entirely and uses the ISO 9660 merge that genisoimage
already supports and that growisofs accepts as a `-M` image.

### 4.2 growisofs internals

This records where in the growisofs source each burn rule comes from, so that a
later reader can re-check it. OPERATIONS.md sections 11.7 and 11.8 hold the
rules themselves.

| Rule | Where it comes from |
|---|---|
| A seeking write's byte offset and length must be multiples of 32768. | `poor_mans_pwrite64` rejects any other value with `EINVAL`. |
| `seek:N` requires `N % 16 == 0`. | The `-use-the-force-luke=seek:` parser. |
| One command line covers every media size. | `get_2k_capacity()` computes `nwa + free_blocks` from `READ TRACK INFORMATION`; there is no layer logic and no hardcoded sector count. |
| A blank BD-R is formatted for POW unless `spare:none` is passed. | `bd_r_format()` forces the Format Subtype to SRM+POW. |
| A POW BD-R is treated as rewritable. | `poor_man_rewritable()` classes `profile == 0x41 && bdr_plus_pow` as rewritable. |
| Appends stay in one session. | `plusminus_r_C_parm()` takes `next_session` from the Next Writable Address and forces `prev_session = 0`. |

### 4.3 Debian patch history

The Debian packaging repository at salsa was checked at master `0d0cb25`
(2021-11-28). No patch in `debian/patches` touches `CD001`. No patch mentions
UDF. The changelog top entry is `7.1-15 UNRELEASED` (2019-11-04) and holds only
packaging housekeeping. There is no fork and no newer release. Upstream
dvd+rw-tools 7.1 was released on 2008-03-05 and is the last upstream release.

Upstream dvd+rw-tools 7.1 is broken for BD-R in two ways. The two distribution
patches that the burner version check requires:

| Patch | Bug | Effect without it |
|---|---|---|
| `ignore_pseudo_overwrite.patch`, 2011-03-07 | Debian #615978 | A POW-capable drive under-reports BD-R capacity and the disc cannot be filled. |
| `fix_burning_bd-r_discs.patch`, 2015-02-20 | Debian #713016 | A blank BD-R fails to close with `CLOSE SESSION failed with SK=5h/INVALID FIELD IN CDB`. |

Debian 7.1-14, Fedora 7.1-13 and Arch 7.1-13 carry both. OPERATIONS.md section
11.9 holds the version check.

### 4.4 UDF revision support per operating system

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

### 4.5 UDF measured limits and reader issues

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

Known reader issues:

- Linux sets `iocharset=utf8` by default. `/proc/mounts` shows
  `udf ro,relatime,iocharset=utf8` with no option given. On kernels older than
  5.4, pass `utf8`, not `iocharset=utf8`.
- `mount -o session=` defaults to the last session. Profile 1 has one session,
  so the option never matters.
- udisks2 and KDE mishandle multi-session BD-R auto-mount. Profile 1 has one
  session, so the problem does not arise. A recovery procedure must still use
  an explicit `mount -t udf -o ro /dev/sr0`, never desktop auto-mount.
- The kernel mounts any write-once (VAT) UDF volume read-only. Profile 1 never
  builds one.

### 4.6 ISO 9660:1999 level 4 evidence

Measured limits:

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

Levels 1, 2 and 3 are forbidden. They uppercase the name, truncate it to 30
characters, and append `.;1`. The measured on-disc bytes at level 1, 2 and 3
were `83F682CFA8CCFBB7641FB8AAAE3F73.;1`; at level 4 they were the full
68-character lowercase name.

A Linux mount hides this. The kernel `iso9660` driver lowercases names when
Rock Ridge is absent, so a level-1 image *displays* lowercase 30-character
names while the disc itself holds uppercase. Only a raw-byte check tells the
truth. Windows and macOS show the uppercase `NAME.;1` form. Any test of the
naming must therefore inspect the raw image bytes, not a mount.

**Measured append test.** The design was tested end to end on a tree of
68-character lowercase hex names, with 2,000 objects in batch 1 and 500 in
batch 2.

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

**OS readability.**

| OS | Level 4 long lowercase names | Deep directories with `-D` | Note |
|---|---|---|---|
| Linux 2.6 and newer | **Yes**, measured with 2,500 files | Yes | The `iso9660` driver lowercases only when Rock Ridge is absent and the name is uppercase on disc. |
| Windows 7 to 11 | **Unverified. Blocking.** | Expected yes | CDFS is documented for levels 1 and 2 only. Microsoft has never documented ISO 9660:1999 support. genisoimage itself warns that names above 31 characters "may cause buffer overflows in the OS". Probe 2 of section 7 must pass first. |
| macOS 10.5 to 15 | Likely yes | Yes | Apple's `cd9660` has read long ISO names for years. Unverified here. |
| FreeBSD | Yes | Yes | Not a target, but noted: FreeBSD reads a profile 2 disc and cannot read a profile 1 disc. |

If Windows truncates to 31 uppercase characters, profile 2 must not be used.
The correct response is to stay on profile 1, not to add a truncation fallback.

**Character sets.** There is no ISO 9660 option set that gives lossless UTF-8
names on every operating system without Rock Ridge or Joliet.

| Mechanism | Storage | Decoded correctly by |
|---|---|---|
| Level 4, no Rock Ridge, no Joliet | Raw bytes, passed through. No declared encoding. | Linux. Windows and macOS decode the bytes in a legacy code page and produce mojibake. |
| Rock Ridge | Raw bytes, plus POSIX metadata | Linux and macOS. Windows ignores Rock Ridge completely. |
| Joliet | UCS-2 big-endian | Windows, Linux, macOS. Characters above the BMP cannot be represented. |

This does not affect NoahsArk under the profile 2 premise, because on-disc
names are hex. It is the reason never to put raw user file names on an ISO 9660
disc, and it is the second reason that profile 1 is the default.

### 4.7 Blu-ray media facts and damage patterns

Blu-ray uses a two-code picket scheme inside every 64 KiB ECC cluster. The Long
Distance Code is RS(248,216) over GF(2^8), with 32 parity symbols per codeword.
The Burst Indication Subcode is RS(62,30), and its bytes are sprinkled through
the cluster. When two adjacent BIS bytes fail, the drive marks the roughly 38
LDC bytes between them as erasures. Erasure decoding doubles the correction
power.

| Damage pattern | LBA footprint | Size on a 25 GB BD |
|---|---|---|
| Radial scratch, 1 mm wide, full radius | ~106,000 hits of about 1 sector each | ~217 MB total, never more than a few sectors contiguous |
| Circumferential scratch, 1 mm radial width | one contiguous run | ~359,000 sectors, ~736 MB |
| Outer-edge ring, outermost 1 mm | one contiguous run at the highest LBAs | ~500,000 sectors, ~1.03 GB |
| Outer-edge degradation, outermost 5 mm | contiguous run at the end | ~4.7 GB |
| Fingerprint, 5 mm across | a few hundred short runs | 10 to 50 MB, scattered |
| Random cluster rot | isolated 64 KiB clusters | small |
| Delamination bubble | contiguous ring segment | 100 MB to several GB |

Media facts that the capacity table rests on. OPERATIONS.md section 9.2 holds
the table itself.

- Media type 10, `image`, has no fixed size. A file image takes the size that
  `pack --capacity` gives it.
- QL 128 GB exists as BD-R XL only. BD-RE XL stops at 100 GB.
- M-DISC BD is sold as SL 25 GB and DL 50 GB, with the same sector counts.
- A tool must never hardcode these numbers for a burn. It must use
  `growisofs -F`.

### 4.8 dvdisaster comparison

The column-and-stripe layout is similar to dvdisaster RS03. The code itself is
defined in FORMAT.md section 10.2 and nowhere else.

Never copy dvdisaster's hardcoded BD sizes, 11,826,176 and 23,652,352 sectors.
They are smaller than the real discs and would waste about 3 percent of every
disc.

### 4.9 Paper summaries

**FastCDC (Xia et al., IEEE TPDS 2020).** Content-defined chunking with a Gear
rolling hash, an enhanced hash judgement that spreads the mask bits over the
high bits to restore an effective window of about 48 bytes, cut-point skipping
over the first `min` bytes, normalized chunking that tightens the mask before
the average size and loosens it after, and a two-byte rolling variant. The
paper's `max = 4 * avg`, `min = avg / 4` ratio is what keeps normalization
level 2 in its intended regime. Section 2.3 states which parts are adopted and
which are not.

**Binary Fuse filters (Graf and Lemire, ACM JEA 2022).** A 3-wise fused-segment
construction that reaches about 18.2 bits per key at a 2^-16 false-positive
rate, against 23 bits per key for a Bloom filter at the same rate, that is 26
percent more. The construction is batch-only: the key set must be frozen. The
query is three probes into nearby segments. The peeling construction succeeds
on the first seed with probability above 0.99 at the size factor used here.
Section 2.10 states why the false-positive rate, not the size, drove the
choice.

**Capping (Lillibridge, Eshghi and Bhagwat, USENIX FAST 2013).** Inline
chunk-based deduplication spreads a backup's chunks over many containers, and
restore speed collapses. Their technique caps the number of source containers
per fixed-size segment of the backup stream, rewriting the chunks that would
come from beyond the cap. Their result: capping at 10 to 20 containers per
20 MB segment costs a few percent of dedup ratio and buys a 2x to 6x restore
speed-up. NoahsArk adopts it with a run in place of a container; OPERATIONS.md
section 8.2 holds the knobs.

**Reed-Solomon and Cauchy matrices.** Reed and Solomon, "Polynomial Codes over
Certain Finite Fields", J. SIAM 1960, gives the code. Blömer et al., ICSI
TR-95-048, 1995, gives the XOR-based erasure-resilient construction over a
Cauchy generator matrix that FORMAT.md section 10.2 pins.

---

## 5. Worked examples

Every number in this section is the number that was computed for the design.

### 5.1 Capacity estimator, BD-R SL 25 GB, profile 1

Inputs: capacity 12,219,392 sectors (25,025,314,816 bytes), no forced capacity,
**profile 1** with `expected_runs` 32, `earlier_runs` 32, `fill_ratio` 0.95,
`m` 23, a `spare:min` disc, P4 chunking (`chunk_avg` 4,194,304),
`manifest.history_depth` 8, `catalog.expected_snapshots` 10,000,
`catalog.table_reserve_bytes` 65,536, `README.txt` at its 16 KiB cap and
`FORMAT.txt` at its 64 KiB cap.

`catalog_growth`: `objects_per_run` = ceil(25,025,314,816 / 4,194,304) =
5,967; `filter_bytes` = 84 + ceil(5,967 x 91 / 40) = 13,659;
`manifest_bytes` = 64 x 5,967 + 4,096 = 385,984; `table_bytes` = 10,000 x
(640 + 136) + 65,536 = 7,825,536; `catalog_bytes_per_run` = 32 x 13,659 +
8 x 385,984 + 7,825,536 = 11,350,496; ceil(11,350,496 / 2048) = 5,543 sectors
per copy; 32 x 5,543 = 177,376.

| Term | Sectors | Bytes |
|---|---:|---:|
| `safety_margin` = ceil(12,219,392 x 0.05) | 610,970 | 1.25 GB |
| `superblock_and_headers` = 1 + 8 + 32 + 32 x 2 | 105 | 215 kB |
| `catalog_growth` = 32 x 5,543 | 177,376 | 363.3 MB |
| `spare_area` = 268,435,456 / 2048 | 131,072 | 268.4 MB |
| `alignment_padding` = 32 x 16 | 512 | 1.0 MB |
| `parity_headers` = `m` | 23 | 47 kB |
| `fixed_terms` | 920,058 | |
| `fec_region` = 12,219,392 - 920,058 | 11,299,334 | |
| `stripes` = floor(11,299,334 / 255) | 44,311 | |
| `fec_overhead` = 44,311 x 24 + 29 | 1,063,493 | 2.18 GB |
| **reserve_computed** = 920,058 + 1,063,493 | **1,983,551** | **4.06 GB** |
| **data_budget** = 44,311 x 231 | **10,235,841** | **20.96 GB** |
| **fill_limit_sectors** | **11,477,350** | **23.51 GB** |

The FEC region holds 44,311 whole stripes: 10,235,841 data sectors and
1,063,464 checksum and parity sectors, plus 29 sectors of an incomplete stripe
that stay unused. The 23 parity header sectors are in `fixed_terms`.
`fill_limit_sectors` is `12,219,392 - 610,970 - 131,072`, and it also equals
`10,235,841 + 105 + 177,376 + 1,063,493 + 512 + 23`. `reserve_computed` equals
`12,219,392 - 10,235,841`.

### 5.2 The same disc under profile 0

`expected_runs` and `earlier_runs` are both 2: `superblock_and_headers` =
1 + 8 + 32 + 2 x 2 = 45, `catalog_growth` = 10,686, `alignment_padding` =
2 x 16 = 32, `fixed_terms` = 752,828, `fec_region` = 11,466,564, `stripes` =
44,966, `fec_overhead` = 1,079,418, **reserve_computed** = 1,832,246 and
**data_budget** = 10,387,146 sectors (21.28 GB). `fill_limit_sectors` is
unchanged at 11,477,350, because it does not depend on `expected_runs`. Profile
0 therefore gives 151,305 more data sectors, that is 310 MB, than profile 1 on
the same medium.

### 5.3 Capacity estimator, BD-R XL TL 100 GB

Inputs: capacity 48,878,592 sectors (100,103,356,416 bytes), no forced
capacity, profile 1, same knobs, `earlier_runs` 32.

`catalog_growth`: `objects_per_run` = ceil(100,103,356,416 / 4,194,304) =
23,867; `filter_bytes` = 84 + ceil(23,867 x 91 / 40) = 54,382;
`manifest_bytes` = 64 x 23,867 + 4,096 = 1,531,584; `table_bytes` = 10,000 x
(640 + 136) + 65,536 = 7,825,536; `catalog_bytes_per_run` = 32 x 54,382 +
8 x 1,531,584 + 7,825,536 = 21,818,432; ceil(21,818,432 / 2048) = 10,654
sectors per copy; 32 x 10,654 = 340,928.

| Term | Sectors | Bytes |
|---|---:|---:|
| `safety_margin` = ceil(48,878,592 x 0.05) | 2,443,930 | 5.01 GB |
| `superblock_and_headers` | 105 | 215 kB |
| `catalog_growth` = 32 x 10,654 | 340,928 | 698.2 MB |
| `spare_area` | 131,072 | 268.4 MB |
| `alignment_padding` | 512 | 1.0 MB |
| `parity_headers` = `m` | 23 | 47 kB |
| `fixed_terms` | 2,916,570 | |
| `fec_region` = 48,878,592 - 2,916,570 | 45,962,022 | |
| `stripes` = floor(45,962,022 / 255) | 180,243 | |
| `fec_overhead` = 180,243 x 24 + 57 | 4,325,889 | 8.86 GB |
| **reserve_computed** = 2,916,570 + 4,325,889 | **7,242,459** | **14.83 GB** |
| **data_budget** = 180,243 x 231 | **41,636,133** | **85.27 GB** |
| **fill_limit_sectors** | **46,303,590** | **94.83 GB** |

`fill_limit_sectors` is `48,878,592 - 2,443,930 - 131,072`, and it also equals
`41,636,133 + 105 + 340,928 + 4,325,889 + 512 + 23`. `reserve_computed` equals
`48,878,592 - 41,636,133`.

### 5.4 The dry-run printout

`pack --dry-run` prints exactly these numbers before it commits to a run:

```
$ noahsark pack --dry-run --disc 12
disc              b21c7f90  seq 12  BD-R SL 25  profile udf201-pow (1b)
capacity reported 12,219,392 sectors   25,025,314,816 B
capacity forced   12,219,392 sectors   25,025,314,816 B
reserve computed   1,983,551 sectors    4,062,312,448 B
reserve forced             -                        -
reserve extra              -                        -
data budget       10,235,841 sectors   20,963,002,368 B
fill limit        11,477,350 sectors   23,505,612,800 B
data used so far   4,096,512 sectors    8,389,656,576 B
free for this run  6,139,329 sectors   12,573,345,792 B
staged objects         5,912           12,203,441,152 B
plan               one run, 5,912 objects, fits
```

### 5.5 Disc and run layout on the medium

```
 LBA 0                                                       capacity-1
 |                                                                    |
 +--- filesystem structures: inside run 1's parity domain ------------+
 |                                                                    |
 |  RUN 1                        RUN 2                  RUN 3         |
 |  [base=0, len=L1]             [base=B2, len=L2]      [base=B3,..]  |
 |                                                                    |
 |  +----------------------------------------------------------+      |
 |  | RUN.bin        run header, first file copied              |     |
 |  | DISC.bin, README.txt, FORMAT.txt   (first run only)       |     |
 |  | layout.bin, manifest.bin, filter.bin, catalog/            |     |
 |  | snapshots/ and trees/ objects, contiguous                 |     |
 |  | objects/  bundles and chunks, in path order               |     |
 |  | pad.bin        zero fill to lba_base + k*L                |     |
 |  |--------  end of the parity domain  -----------------------|     |
 |  | checksum.bin   the checksum column, L sectors             |     |
 |  | parity/p0232.bin .. parity/p0254.bin                      |     |
 |  |   (each parity file: one run header sector, then L)       |     |
 |  | RUN2.bin       run header copy, last file copied          |     |
 |  +----------------------------------------------------------+      |
 +--------------------------------------------------------------------+

 Example, BD-R SL 25 GB, capacity 12,219,392 sectors, fill ratio 0.95,
 spare:min (section 5.1 gives the arithmetic):
   fill limit = 11,477,350 sectors
   run 1    = LBA          0 .. 4,096,511      (4,096,512 sectors, 8.39 GB)
   run 2    = LBA  4,096,512 .. 8,192,511      (4,096,000 sectors)
   run 3    = LBA  8,192,512 .. 11,477,349     (3,284,838 sectors)
   reserve  = LBA 11,477,350 .. 12,219,391     (742,042 sectors: the safety
                                               margin and the POW spare)
```

The example is illustrative. Real run boundaries follow from the packer and
from the next writable address that the drive reports. The first run's parity
domain starts at LBA 0, so the anchor at LBA 256, the volume descriptors, the
integrity descriptor, the space bitmap and every directory block that lies
below `RUN.bin` are inside it. A later run's domain starts at its own
`RUN.bin`.

Under profile 2 the filesystem directory records of every earlier run are
rewritten inside the newest run, past the next writable address. Those bytes
are part of the newest run and are therefore covered by the newest run's
parity. The data extents of earlier runs are untouched and stay covered by
their own parity. Under profile 1 only the changed filesystem blocks are
rewritten, and they are rewritten **in place, at their old LBAs**, because a
UDF File Entry cannot move. Some of those LBAs lie inside an earlier run's
parity domain. FORMAT.md section 10.6 states how verify treats such a sector:
it is filesystem metadata, its digest is not compared, and the healer counts it
as an erasure. The parity of the earlier run is never recomputed.

### 5.6 Append cost tables

**What an append overwrites, by profile.**

| Profile | Overwritten per append |
|---|---|
| 1, UDF 2.01 | The File Entry of each directory on the path to a new object, the Logical Volume Integrity Descriptor, the space bitmap, and the anchor at 256 when the volume size changed. |
| 2, ISO 9660 level 4 | The whole directory tree and both path tables, rewritten into the new session. |

**Profile 1 block counts per append**, for one new object in each of `d`
distinct directories:

| Fan-out | Directories on the path | Directory blocks overwritten | LVID and bitmap | Total per append |
|---|---:|---:|---:|---:|
| One level, `/NOAHSARK/objects/ab/<name>` | 2 per object (`objects`, `objects/ab`) | 1 + min(d, 256) | 2 to 4 | `1 + min(d,256) + 2 to 4` blocks, at most **261 blocks (522 KiB)** |
| Two levels, `/NOAHSARK/objects/ab/cd/<name>` | 3 per object | 1 + 256 + min(d, 65536) | 2 to 4 | at most **65,797 blocks (128.5 MiB)** in the worst case, and about `1 + 2d + 4` in the common case where the new objects touch `d` leaf directories under `d` distinct first-level directories |

The one-level worst case is bounded by the 256 first-level directories. The
two-level worst case is bounded by 65,536 leaf directories, but it is reached
only when an append touches every leaf, which a path-ordered pack never does.

**Profile 1 per-append cost, by item.** Only the changed blocks are written.

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

The number of appends is effectively unbounded. The limit is the spare area
that defect management consumes and the size of the drive's defect list, not
the filesystem. Budget 512 MiB of spare and metadata reserve per disc.

**Profile 2 append cost**, measured with 1-byte files so that the figure is
pure metadata:

| Objects on disc | Directory bytes rewritten per append | Path table bytes | Cost per append |
|---:|---:|---:|---:|
| 2,000 | 8,650,752 | 42,250 | ~8 MiB |
| 10,000 | 19,589,120 | 95,620 | ~19 MiB |
| 100,000 | 107,282,432 | 516,130 | ~107 MiB |

The cost grows with the object count, because every directory record is
rewritten, and it grows again with the fan-out, because every directory costs
at least one whole sector even when it is nearly empty. The measured column
above is the object-count part: 50 times the objects cost 12 times the bytes,
because a bigger directory packs its records more densely. The fan-out part is
a floor: two hex levels create up to 65,536 directories, and at 2048 bytes each
that floor alone is 65,536 x 2048 = 134,217,728 bytes, that is 128 MiB per
append, whatever the object count. One level caps the floor at 256 x 2048, that
is 512 KiB.

At 100,000 objects and one fan-out level, one append costs about 107.8 MB, so
ten appends cost about 1.08 GB, which is **4.3 percent** of a 25 GB disc. At
10,000 objects ten appends cost about 0.8 percent. Under profile 2 the packer
must include the projected append cost in its capacity budget.

Under profile 2 the overwrite is the whole tree: about 19 MiB at 10,000 objects
and about 107 MiB at 100,000 objects.

### 5.7 Seek tables

Object counts and UDF overhead per chunker profile. The counts are
`ceil(capacity_bytes / avg)` at the capacities of the media registry, which is
the `objects_per_run` planning figure.

| Id | Name | min | avg | max | Objects per 25 GB run | Objects per 100 GB run | UDF overhead per 25 GB |
|---:|---|---:|---:|---:|---:|---:|---:|
| 1 | P3 | 512 KiB | 2 MiB | 8 MiB | ~11,933 | ~47,733 | ~36 MiB (0.15%) |
| 2 | **P4** | **1 MiB** | **4 MiB** | **16 MiB** | **~5,967** | **~23,867** | **~18 MiB (0.08%)** |
| 3 | P5 | 2 MiB | 8 MiB | 32 MiB | ~2,984 | ~11,934 | ~9 MiB (0.04%) |

Restore seek cost, worst case, at 150 ms per seek and one seek per chunk:

| Profile | Seeks per GiB | Seek time per GiB |
|---|---:|---:|
| P3 | 512 | 77 s |
| P4 | 256 | 38 s |
| P5 | 128 | 19 s |

The worst case does not happen for freshly written data, because the packer
lays objects out in file order. The column is the price of dedup against an
older disc. The capping knobs bound it.

### 5.8 Filter, manifest and table size tables

Filter sizes, at 18.2 bits per key plus the 84 bytes of header and body CRC:

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

Manifest sizes:

| Objects in the run | Manifest size | Share of that run's disc |
|---:|---:|---:|
| 6,000 (25 GB, P4) | 375 KiB | 0.0015% |
| 24,000 (100 GB, P4) | 1.46 MiB | 0.0015% |
| 48,000 (100 GB, P3) | 2.93 MiB | 0.0031% |

Snapshot replication, at 10,000 snapshots:

| Item | Per item | 10,000 items |
|---|---:|---:|
| Snapshot object payload, typical (header, three ids, times, a short tag list) | 320 B | 3.20 MB |
| Snapshot object payload, worst case allowed by the size cap | 640 B | 6.40 MB |
| UDF File Entry and directory overhead, one file each | 2,048 B | 20.48 MB |
| Snapshot table record | 136 B | 1.36 MB |

Typical total per run: 3.20 + 20.48 + 1.36 = **25.04 MB**, which is 0.10
percent of a 25 GB disc and 0.025 percent of a 100 GB disc. The filesystem
overhead dominates the payload. The `snapobj.bin` container form removes the
20.48 MB and leaves **4.56 MB** per run. At 10,000 snapshots and 2,000 runs,
the replicated history costs about 9 GB across the whole archive in container
form. That is under half of one disc for a complete, 2,000-fold redundant
history.

### 5.9 Burst tolerance tables

`L = ceil(data_span / k)`, which is close to `run_sectors / 255`. FORMAT.md
section 10.1 states the burst bound: the maximum correctable single burst is
`m * L` sectors, and the bound holds inside the data columns only. The two
tables compare the fixed `m = 23` with three other
values to show why it was chosen; only the `m = 23` column describes a version
1 disc. The table assumes the run covers the whole disc and rounds `L` to
`floor(run_sectors / 255)`.

| Media | L (sectors) | m=12 (5%) | m=23 (10%) | m=28 (12.5%) | m=42 (20%) |
|---|---:|---:|---:|---:|---:|
| BD 25 GB | 47,919 | 575,028 sec = 1.18 GB | 1,102,137 sec = **2.26 GB** | 1,341,732 sec = 2.75 GB | 2,012,598 sec = 4.12 GB |
| BD 50 GB | 95,838 | 1,150,056 sec = 2.36 GB | 2,204,274 sec = **4.51 GB** | 2,683,464 sec = 5.50 GB | 4,025,196 sec = 8.24 GB |
| BD 100 GB | 191,680 | 2,300,160 sec = 4.71 GB | 4,408,640 sec = **9.03 GB** | 5,367,040 sec = 10.99 GB | 8,050,560 sec = 16.49 GB |
| BD 128 GB | 245,101 | 2,941,212 sec = 6.02 GB | 5,637,323 sec = **11.55 GB** | 6,862,828 sec = 14.06 GB | 10,294,242 sec = 21.08 GB |

Configuration and payload, with `k + 1 + m = 255` sectors per stripe:

| Target | k | m | actual m/k | parity fraction m/255 | 25 GB payload | 50 GB | 100 GB | 128 GB |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| 5% | 242 | 12 | 4.96% | 4.71% | 23.75 GB | 47.50 GB | 95.00 GB | 121.47 GB |
| **10%** | **231** | **23** | **9.96%** | **9.02%** | **22.67 GB** | **45.34 GB** | **90.68 GB** | **115.95 GB** |
| 12.5% | 226 | 28 | 12.39% | 10.98% | 22.18 GB | 44.36 GB | 88.72 GB | 113.44 GB |
| 20% | 212 | 42 | 19.81% | 16.47% | 20.81 GB | 41.61 GB | 83.23 GB | 106.42 GB |

Section 2.9 gives the interpretation.

### 5.10 Tree entry worked size example

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

### 5.11 Restore time examples

Single drive, `restore.rate_mb_s` 20 and `restore.switch_seconds` 60:

- 100 GB restored from 5 full discs: `5 * (60 + 25000/20)` s = about 1.8 hours.
  The overhead is 5 minutes, which is negligible.
- 100 GB restored from 80 discs at 1.25 GB each: `80 * (60 + 62)` s = about 2.7
  hours. The overhead is 80 minutes, which is half the time.

---

## 6. Implementation notes

This section describes the reference implementation's language, source layout,
coding style and dependency choices. None of it is part of the on-disc format:
a conforming implementation may use any language, any project layout, any
comment style and any package set, or none. FORMAT.md section 12.4 states the
format-level requirements that do bind every implementation.

Two implementation rules are informative and belong here rather than in
FORMAT.md:

- **No hidden allocation in the hot path.** Chunking and hashing run over large
  files, so reuse buffers.
- **Concurrency.** Chunk and hash in parallel across files. Write the run image
  single-threaded, because copy order is LBA order.

The rules that do bind an implementation are: vendor the frozen tables; pin the
external tools; every read verifies; errors carry the id. FORMAT.md section
12.4 holds them.

**Vendored tables.** The Gear table and the mask constants are part of the
on-disc format. Vendor them. Do not import them from a dependency that could
change them. FORMAT.md sections 4.8 and 4.9 hold the generation rules.

**External tool version pinning.** Check the versions of `growisofs`,
`mkudffs`, `udfinfo`, `genisoimage` and `ddrescue` at startup. Refuse to burn
with an unpatched dvd+rw-tools build. Expect dvd+rw-tools 7.1-14 or newer and
udftools 2.3 or newer. Section 4.3 gives the two patches and the bugs they fix.
OPERATIONS.md section 11.9 holds the check itself.

**Language: Go.** The reference implementation is written in Go: the standard
library covers SHA-256, compression bindings are mature, and a single static
binary suits a recovery tool. The format is defined by FORMAT.md and by nothing
in Go.

**Comment policy.** Comments and commit messages in the reference
implementation must not cite section numbers of any specification document. A
comment carries only information that is related to the code beside it. Section
numbers drift as a document is edited; a comment that names one goes stale
silently.

**Coding style.** One Go definition per binary structure, with explicit encode
and decode functions. No reflection-based marshalling. No struct tags. The byte
layout is written out by hand, field by field, in the order of the table in
FORMAT.md.

**Golden files for every structure.** A test writes a structure with known
values and compares the bytes to a checked-in file, and reads that file back
and compares the fields.

**Project layout, advisory.** `cmd/noahsark` for the CLI, and `internal/`
packages for chunker, hash, object, tree, manifest, filter, fec, run, burn,
stage, plan, restore, cache and disc.

**Go packages.** Every dependency is vendored or pinned by hash. The packages
below implement the algorithms that FORMAT.md pins, and were used to validate
its parameters. **A package is a convenience, never the definition. The on-disc
format is defined by FORMAT.md, and a conforming implementation may use no
package at all.**

| Need | Package |
|---|---|
| Reed-Solomon `rs255-gf8` (FORMAT.md 10.2) | `github.com/klauspost/reedsolomon` |
| zstd | `github.com/klauspost/compress/zstd` |
| BLAKE3 | `lukechampine.com/blake3` or `github.com/zeebo/blake3` |
| BinaryFuse16 (FORMAT.md 11.1) | `github.com/FastFilter/xorfilter` |
| UDF File Entry parsing (OPERATIONS.md 10.3) | `github.com/mogaika/udf`, read-only |
| Watch mode (Phase 3) | `github.com/fsnotify/fsnotify` |
| CLI | any |

---

## 7. Scheduling and operations examples

Every example here is informative. A conforming deployment may use cron,
systemd, or any other scheduler, and any CI system.

**cron, daily at 02:00.**

```cron
0 2 * * *  /usr/local/bin/noahsark commit --repo /srv/ark -q -m "nightly"
```

**systemd, a service and a timer.**

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
keeps the commit out of the way of the live workload. Two commits must never
run at once on one repository; `commit` takes the exclusive repository lock and
exits with code 2 when it cannot get it within `repo.lock_timeout`.

**CI workflow.** Test steps live in a composite action at
`.github/actions/test/action.yml`. Workflows only call the composite action.
That keeps the pipeline definition in one place and lets a developer run the
same steps locally.

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

**Probe actions.** Each probe is a small composite action under
`.github/actions/probe-<topic>/` that runs the experiment on an image file and
writes a machine-readable result file as a job artifact.

| Probe | Question |
|---|---|
| `probe-udf-limits` | Name length and directory depth per UDF revision, on this kernel. |
| `probe-iso-names` | The raw on-disc bytes of a 68-character lowercase name at each ISO level. |
| `probe-copy-order` | Whether copy order equals LBA order, and the stride. |
| `probe-image-append` | Whether an image diff plus a seek write reproduces a full rebuild byte for byte. |
| `probe-mount-matrix` | Which UDF revisions mount on the runner's kernel, and read-write or read-only. |
| `probe-damage-heal` | A damage and heal round-trip at several burst sizes. |
| `probe-mkudffs-options` | The `udfinfo` output for every `--media-type`. |

**Manual probes.** These decide design questions that no image can answer. Each
must be run on real hardware and its result recorded in the repository.
OPERATIONS.md section 23 holds the manual physical checklist.

*Probe 1: kernel direct write to a POW BD-R (profile 1 variant 1a).*

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

*Probe 2: Windows reads ISO 9660:1999 level 4 long lowercase names.* Burn a
profile 2 disc with 68-character lowercase hex names. On Windows 10 and on
Windows 11, check that `dir` shows the full 68-character lowercase name and not
a 30-character uppercase truncation, and that `certutil -hashfile` on such a
file succeeds. This probe is **blocking**. Profile 2 must not be used in
production until it passes. If Windows truncates, the correct response is to
stay on profile 1, not to add a truncation fallback.

*Probe 3: Windows reads an open POW BD-R.* Burn one run with `spare:min` and no
`-dvd-compat`, so the disc stays open. Check that Windows 10 and 11 mount it,
list every file, and hash one object correctly.

*Probe 4: macOS reads an open POW BD-R.* The same disc as probe 3. Check that
macOS 15 mounts it, that `diskutil info` reports the expected filesystem, and
that the file count matches Linux.

*Probe 5: reading past the last written block.* On the same open disc, read
beyond the last written LBA. Record what the drive does: a hard error, zeros, or
a hang. The verifier must handle the answer, and the answer is drive-dependent.

*Probe 6: writing the tail region before the middle.* On an open POW disc, try
`growisofs -use-the-force-luke=seek:N,spare:min,tty -Z /dev/sr0=tail.bin` with
`N` near the end of the medium, while the middle is still unwritten. Question:
does the drive accept it? If yes, the first run can place the UDF tail anchors
immediately. If no, the tail anchors wait for an append or for a close.

---

## 8. Glossary

Each term is defined once. The same word is used everywhere for the same thing.
The section named against a term is its normative home.

| Term | Definition |
|---|---|
| **Append** | Adding a run to a disc that already holds one. Phase 2. OPERATIONS.md section 10.5 is the normative home. |
| **Metadata object** | A tree, a chunklist or a snapshot object: an object that holds references and no file content. Bit 2 of `extent_flags` and of `record_flags` marks one. |
| **Local ref log** | `<repo>/refs.bin`, the append-only log of ref values that OPERATIONS.md section 3.3 defines. |
| **Pending snapshot chain** | The snapshot objects in staging whose state is below CLEAN, reachable from the head named by the local ref log. |
| **Bundle** | An object that holds many small chunks plus an index. |
| **Burn plan** | The machine-readable file that `pack` writes and `burn` renders into command lines. |
| **Capping** | Bounding the number of older runs that a new run may reference, to bound the restore plan. |
| **Catalog** | The set of files under `runs/<seq>/catalog/` that every run carries: `CATALOG.bin`, every snapshot object, every earlier filter, recent manifests, the snapshot table, the ref table, the run table, and the disc directory. |
| **Checksum column** | The FEC column whose sector `i` holds an 8-byte digest of each of the `k` data sectors of stripe `i`. |
| **Commit bundle** | A directory of new objects plus a `BUNDLE.bin` header, produced by `commit --out` and consumed by `import`. Not the same thing as a bundle object. |
| **Direct mode** | A commit that walks the source itself and reads only changed files. |
| **Mirror mode** | A commit whose changed files are pulled into a mirror directory first, by `sync`. |
| **Unstable path** | A file whose size or mtime changed while it was being read. The parent entry is reused, or the content is stored with the `UNSTABLE` flag. The path is reported. |
| **Quick check** | The size, mtime and ctime comparison against the parent snapshot's tree entry. |
| **Chunk** | A content-defined slice of a file. The unit of deduplication. |
| **Chunklist** | An object that holds the ordered chunk ids of one large file. |
| **Column** | One of 255 ranges of `L` sectors: `k` data columns inside the parity domain, the checksum column, and `m` parity columns, each in its own file. |
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
| **Parity domain** | The contiguous LBA range of a run's `k` data columns, `[lba_base, lba_base + k*L)`, which the Reed-Solomon layer protects. It starts at LBA 0 for the first run of a disc. |
| **POW** | Pseudo-OverWrite. The BD-R format mode that makes the medium logically overwritable. |
| **Prerequisite** | An object that a run references but does not contain. |
| **Raw append** | The degraded mode that writes a run past the next writable address without updating the filesystem directory. |
| **Reserve** | The part of a disc's capacity that data must not use. |
| **Run** | One execution of one burn plan. The unit of packing, manifest, filter and parity. Its `run_seq` is never reused. |
| **Run table** | The catalog table that maps every `run_seq` to its disc, its LBA range and its run header hash. |
| **RS margin** | `m` minus the worst-stripe erasure count, as a percentage of `m`. The headline health metric. |
| **Shard** | One 2048-byte sector, as seen by the FEC layer. |
| **Snapshot** | An object that names a root tree, a parent and a generation. |
| **Spare area** | The reserved region that POW uses for logical overwrites. |
| **Staging** | The local store that holds objects between commit and CLEAN. |
| **Stripe** | The 255 shards, one per column, at the same offset inside their columns. |
| **Tree** | An object that describes one directory, with full metadata per entry. |
| **TLV** | A type-length-value record in a tree entry's extension area. |

---

## 9. References

This section names the external documents that the normative text relies on, so
that a reader can check a claim at its source. Where FORMAT.md and a reference
disagree, FORMAT.md wins for the on-disc format.

| Topic | Reference |
|---|---|
| FastCDC | W. Xia et al., "The Design of Fast Content-Defined Chunking for Data Deduplication Based Storage Systems", IEEE Transactions on Parallel and Distributed Systems, 2020. FORMAT.md sections 4.1 to 4.9. |
| BinaryFuse filters | T. M. Graf and D. Lemire, "Binary Fuse Filters: Fast and Smaller Than Xor Filters", ACM Journal of Experimental Algorithmics, 2022. FORMAT.md section 11.1. |
| Capping | M. Lillibridge, K. Eshghi and D. Bhagwat, "Improving Restore Speed for Backup Systems that Use Inline Chunk-Based Deduplication", USENIX FAST 2013. OPERATIONS.md section 8.2. |
| Reed-Solomon codes | I. S. Reed and G. Solomon, "Polynomial Codes over Certain Finite Fields", Journal of SIAM, 1960. FORMAT.md section 10.1. |
| Cauchy generator matrices | J. Blömer et al., "An XOR-Based Erasure-Resilient Coding Scheme", ICSI TR-95-048, 1995. FORMAT.md section 10.2. |
| dvdisaster RS03 | dvdisaster documentation, the RS03 codec. Informative comparison in section 4.8. |
| BLAKE3 | J. O'Connor, J.-P. Aumasson, S. Neves and Z. Wilcox-O'Hearn, "BLAKE3: one function, fast everywhere", 2020. FORMAT.md section 3.2. |
| SHA-256 | NIST FIPS 180-4, Secure Hash Standard. FORMAT.md section 3.2. |
| Multihash and multicodec | The multiformats specifications, multihash and the multicodec table. FORMAT.md sections 2.6 and 3.4. |
| CRC-32C | G. Castagnoli, S. Bräuer and M. Herrmann, "Optimization of Cyclic Redundancy-Check Codes with 24 and 32 Parity Bits", IEEE Transactions on Communications, 1993; parameters as used by RFC 3720 (iSCSI). FORMAT.md section 2.1. |
| zstd | RFC 8878, Zstandard Compression and the 'application/zstd' Media Type. FORMAT.md section 5. |
| UDF 2.01 | OSTA Universal Disk Format Specification, revision 2.01, and ECMA-167, 3rd edition. OPERATIONS.md sections 10.1 and 10.5. |
| ISO 9660:1999 | ISO 9660:1988 with the 1999 amendment (level 4), and ECMA-119. OPERATIONS.md section 10.6. |
| Blu-ray error correction | Blu-ray Disc Association, "White Paper Blu-ray Disc Format, 1.A Physical Format Specifications for BD-RE", the LDC and BIS picket code. Section 4.7. |
| dvd+rw-tools | growisofs and dvd+rw-mediainfo, upstream 7.1 with the Debian patch set. OPERATIONS.md sections 11.8 and 11.9, and sections 4.2 and 4.3. |
| udftools | mkudffs and udfinfo, 2.3 or later. OPERATIONS.md section 10.1. |
| GNU ddrescue | The ddrescue manual, the mapfile format. OPERATIONS.md sections 13.2 and 24. |
| Git multi-pack-index | Git technical documentation, "multi-pack-index format". FORMAT.md section 11.2 and OPERATIONS.md section 2.4. |
| Git partial clone | Git technical documentation, "partial clone", the promisor discipline. FORMAT.md section 11.3. |
| GEFS | O. Read, GEFS, a content-addressed filesystem for Plan 9: hashed pointers and one root per snapshot. FORMAT.md sections 2.1 and 6.15. |
| Duplicacy | The two-step fossil collection rule. OPERATIONS.md section 4.5. |
| Bacula | The bootstrap file. OPERATIONS.md section 14.4. |
| restic | The bundle (pack) shape and the Windows ACL inheritance defect. FORMAT.md section 6.3 and section 2.14. |
| age | The separation of encryption from signing that FORMAT.md section 6.19 reserves. |
| Linux kernel | `fs/udf/super.c` for the write-once read-only rule, and the `openat2` and `RESOLVE_*` flags that FORMAT.md section 6.12 and OPERATIONS.md section 15.6 rest on. |

---

## 10. Change log

The document version is independent of the format version. A document change
that alters a byte on a disc bumps the format version; FORMAT.md section 12.5
holds that rule. Every entry below is under format major 1.

The entries for versions 2.1 to 2.5 describe a single combined specification
document. They are reproduced as written, with the citations to that document's
own numbering removed, because those numbers no longer resolve. The substance
of each entry is unchanged.

| Document version | Change |
|---|---|
| 3.1 | Eleven contradictions, nine blocking gaps, six split defects and nine mechanical failures closed across the three documents. **Contradictions**: `fs_profile` records the writer's plan at the first burn and never gates an append, so profile 0 and profile 1 share one filesystem and a reader treats them identically; `notes.bin` is named as the source of a `degraded` or `withdrawn` judgement and the next run copies the folded `health` value into its disc directory record; ctime storage and user and group names are conditioned on `metadata.ctime` and `metadata.user_group_names`; the catalog cap drops the oldest manifest first; every literal 8 or 9 manifest count became `manifest.history_depth` and "the depth plus one"; `spare:default` gained its rendering and its `spare_area` case; `disc.force_reserve` replaces the computed reserve and the estimator selects between them; `source_type` 5 reads "Imported from a commit bundle" in both homes; `commit.retry_unstable` names the rule of the in-flight branch instead of a skip; and the checksum rule became a write-order rule with the run filter named as the one structure whose body CRC trails the body. **Gaps closed**: the `mkudffs` options, the sparing-table ban, the label source, the image length, the anchor LBAs and the used prefix are normative in FORMAT.md; `tool_version` is a u32 with a writer registry id in its high 8 bits, and conformance is judged on ids, structures and readability, never on compressed bytes; `snapobj.bin` members are laid out by ascending `content_id` with `compression` 0; withdrawing a run returns its PACKED records to STAGED; the writing run's own disc directory record carries `health` 6, `unverified, this disc`; every stored percentage is rounded down and clamped to 0 to 100; the ref table sort key gained `time_nsec` and `run_seq` and is total; every run header repeats the superblock's `fs_profile` and a reader refuses a mismatch; and `sources.one_file_system` and `sources.follow_symlinks` have FORMAT.md rows. **Splits repaired**: `extent_flags` bit 2 and the `"DUPS"` field order are defined in FORMAT.md; the six local magics, the local hash and CRC coverage, the host limits and the restore safety invariants moved to OPERATIONS.md; the priority order, the probe rule and the burst bound moved out of this document into OPERATIONS.md and FORMAT.md, leaving citations; and each rule that stood in two files now stands in one with a citation in the other. **Mechanical**: every stale section citation corrected, and Appendix A of FORMAT.md regenerated from FORMAT.md's own tables, so every meaning cell and every section number in `FORMAT.txt` matches the table it came from. `FORMAT.txt` stays format major 1 minor 0 and is 40,485 bytes in 825 lines. The `health` registry gained value 6 and the disc directory record's meaning cell with it; no other on-disc field changed width, offset or meaning. |
| 2.5 | Ten contradictions and seventeen blocking gaps closed, and the same-name fields, registry-to-CLI mismatches and state-machine gaps with them. **Close state**: the superblock is never updated, so a close performed by `close` is recorded in the closing run's header, in the new `run_flags` bit 0 `CLOSING_RUN` taken from the run header's reserved bytes, and in `state_flags` bit 0 of the disc directory; `sealed` now means only "burned sealed at first write", and `disc --close` is gone. **Run table**: the record of the run that carries the table holds a zero `run_header_hash` that the next copy fills in, and `run_status` and `run_header_hash` are named as the only two fields a later copy may complete. **Catalog snapshot objects**: `catalog/snapobj/<name>` is the complete object file, its name is verified by hashing the payload after decompression, and `file_hash` is the hash of the whole file and does not equal the name. **Times**: every `created_sec` and `first_burn_sec` is pack time, and the actual burn time lives only in the state log. **Layout records** are in copy order, which is LBA order, and a zero-length `pad.bin` record carries `start_lba` equal to `lba_base + data_span`. **Fill order** inside steps 5 and 6 made total, with the walk, the first-occurrence rule for shared chunks and bundles, and the rule that an object the target disc already holds is never written again. **Prerequisites** hold every directly referenced absent id and no transitive one, exclude a snapshot `parent`, and `"SRCR"` is the distinct run seqs of `"PREQ"`. **Withdrawn runs** keep their catalog copies but leave the dedup query, the prerequisite targets, the packer and the planner, and every affected local ref record returns to `run_seq` 0. **New exact rules**: the bundle boundary; a version 1 writer never writes a `level` 1 chunklist; the TLV spill loop, largest first, ties by lowest `tlv_type`; hardlink group membership by two entries with one device and inode inside the roots; the tree entry variable-area order; `"SPLT"` one record per other part, sorted by `chunklist_id` then `other_run_seq`; snapshot tag 5 holds the config and `--exclude` rules only, LF-terminated raw bytes; zstd frame parameters pinned, with the pinned encoder version named as the condition for byte identity; `payload_bytes`, `snapshot_count`, `total_size`, `used_sectors` and `disc_used_sectors` defined; the superblock chain and a consumed `disc_seq`; which catalog a reader trusts, by the five hashes that must verify. **`FORMAT.txt`**: its exact text for major 1 minor 0 is now normative, and FORMAT.md section 8.5 holds it, and the generation rule is informative and names the part titles, the eleven registries and their source tables, the thirty structure names, the six mask constant names and the cell rendering. **Same-name fields separated**: `extent_flags` and `record_flags` in place of two different `flags`; `container_len` in place of the layout and manifest `payload_len`; `note_label_len` in the notes record. **Registry and CLI**: burn plan `burner_backend` 4, IMAPI, marked reserved and never written and dropped from the JSON rendering; `append --raw` documented; `disc mark-degraded --health` added, which gives every `health` value a way in; `commit --from` spelled as the `commit` reference defines it; `disc mark-degraded` added to the exclusive-lock list; the undefined `watch.*`, `mirror.*` and `reindex.*` key prefixes removed from the phase table. **State machines**: BURNED to PACKED on a failed verify drawn in the object state machine, and the recovery arrow out of `degraded` drawn in the disc lifecycle. **Numbers**: the fill-limit example corrected to 11,477,350 sectors; `disc.spare_reserve_bytes` corrected to 256 MiB under `spare:min` and 512 MiB under `spare:default`; the `m = 28` BD 128 GB burst figure corrected to 14.06 GB; the loss report's `exit_code` set corrected to 0, 1, 2 or 3; the `README.txt` slot count corrected to nineteen, with every slot substituted including the six in part 7. Tests 50 to 59 added, and test 41 extended. |
| 2.4 | Reserve estimator: `catalog_bytes_per_run` now charges `earlier_runs * filter_bytes` instead of one filter, and `table_bytes` is derived from `catalog.expected_snapshots * (snapobj_bytes + 136) + catalog.table_reserve_bytes` instead of a flat constant; the catalog cap remedy stated as a formula; both worked examples, the dry-run printout, the catalog-cost figure and test 24 recomputed. `disc.expected_runs` defaults to 2 under profile 0, for the Phase 2 repair run's catalog copy. `disc.spare_reserve_bytes` depends on `disc.spare`: 256 MiB under `min`, 512 MiB under `default`. `fs_profile` stated to name the filesystem only; appendability comes from `sealed` and the drive's POW state. `body_crc32c` after the body is named as the filter container's exception to rule 7. The filter's `a`/`b` fingerprint rule made total, by lowest matching index. `README.txt`'s 16 KiB cap made normative, and the identity block's slot count corrected to thirteen. The catalog entry field renamed `catalog_role`, distinct from the layout table's `file_role`. Objects per run capped at 2^32-1, matching the `"FANO"` table. `repo.short_name`'s derivation stated. `pack`'s behavior on a staged set larger than one run stated. `disc mark-degraded` moved into a new `notes.bin` record type, local and independent of the cache. Manifest count restated as "8 plus the run's own, 9". Restore exit code 3 added to the 18.9 table. `image build` moved to the exclusive lock class. The photo-directory seek estimate corrected to 75 minutes. The snapshot header's `object_count` renamed `reachable_object_count`, and the snapshot table's mirrors it, to stop it being compared with the run header's physical `object_count`. Clarified: the local ref log's `sequence` order and the on-disc ref table's order govern different files; two-byte rolling is not a normative optimization; `data_budget` charges `data_span`; an unreadable checksum sector and a present-but-wrong digest are different cases; `disc.close_policy = always` is profile 0 only; manifest TOC offsets are 8-byte aligned; a snapshot object's bytes legitimately appear at two LBAs in one run. Go packages, project layout and coding style moved into informative reference-implementation notes. Informative markers and normative-outcome sentences added or strengthened for scheduling examples, the CI composite action, command templates, the FEC memory strategy, the filter construction steps and the restore-order syscalls. The broken local-cache table fixed by moving its prose below the table. The table of contents extended to every `####` heading. A sentence added naming the disc filesystem chapter as the append model's normative home. New sections added: a disc lifecycle state machine, an evolution rule for the JSON documents, a config-to-disc-bytes index, and a document status and governance paragraph. Golden vector files are published beside the specification. |
| 2.3 | `fec_overhead` is `fec_region - data_budget`; the `m` parity header sectors became the named fixed term `parity_headers`, and both worked examples, the dry-run printout and test 24 were recomputed. The reserved-space chapter split into invariants, a reference estimator and the two examples; `fill_limit_sectors` has one definition and one consequence, and the superblock records the values the writer used. `disc.expected_runs` defaults to 1 under profile 0. `superblock_and_headers` counts the real sizes of `README.txt` and `FORMAT.txt`, which gained normative caps. `filter_bytes` uses the 80-byte header plus the 4-byte body CRC. `data_span` covers steps 1 to 6 only, ends at the File Entry block of the last file of step 6, and `pad.bin` is `k*L - data_span` sectors. The exact text of `README.txt` and the generation rule of `FORMAT.txt`. The BinaryFuse16 construction, peeling order and seed search. The burn step tree listing serialization. `RUN.bin` and `RUN2.bin` are 2048-byte files. `disc_run_index` is u32 in the run header. The run table holds every burned run with a `run_status`, and withdrawal is explicit. `snapobj.bin` is a kind 2 bundle object. `"BMAP"` and `"RIDX"` are reserved with no payload in version 1. Kind 6 never appears in a manifest, layout or state log record. Plans mark an object located only by a filter as probable and the restore confirms it. `init --repo-uuid` requires the next sequence numbers or a disc scan. Retention stated as a version 1 non-goal, threat model, performance and resource requirements, what a partial `pack` leaves behind, reader and writer interop matrix. The burst bound qualified to the data columns and the parity retry bounded. Command templates made informative. Profile 2 append cost corrected to 4.3 percent. Objects per run, the Mini BD size and the FastCDC minimum-chunk reading corrected. `disc.spare` is Phase 1. Tests 41 to 49 added. |
| 2.0 | Complete redesign of the superseded design. Section 2.18 lists what it superseded and why. Section 2.19 summarizes it. |
| 2.2 | Reed-Solomon code defined exactly: GF(2^8) with 0x11D, a Cauchy generator matrix over the `k` data columns, the checksum column outside the code, and a printed `k = 3`, `m = 2` example. Local repository state: the local ref log, the pending snapshot chain and the notes file; commit resolves its parent from the local log first. ctime stored from Phase 1. Ref table sort key. Exact integer `catalog_growth` formula, both worked examples recomputed. 16-bit fan-out index defined. A `run_seq` is never reused. `pad.bin` always present. Profile 2 builds its images with genisoimage and burns them with growisofs. `verify --image` column location and the raw-LBA definition. Burn plan path limits. `scrub.schedule` grammar. Metadata object defined. Intermediate restore directories. Hardlink ids in mirror mode from the source listing. Unchanged commit writes nothing without `--force`. CRC rule restated with a coverage table. One CRITICAL rule. Feature-bit reservation corrected. `standalone` preset lifts the per-file caps. Run table membership stated. Normative-home table for burn topics. Security summary, limits, JSON field tables for the burn plan, the restore plan, the loss report and the health report, snapshot metadata tags in the registry summary, a references list. Tests 36 to 40 added. |
| 2.1 | Run table added to the catalog. Feature-bit assignment table and hash coverage table added. `header_len` limited to three structures; every other structure grows only by major version. Checksum column covers the data sectors of its own stripe. `k` and `m` fixed for version 1. First run's parity domain starts at LBA 0. One capacity formula with `data_budget` and `fill_limit_sectors`. Build passes with explicit placeholders and final order. Sealed path burns the full-size image. Bundle chunk payloads carry no object header. Exclude pattern language and complete root name encoding. `verify --mapfile`. Healed objects enter STAGED. Reader and writer conformance. Snapshot table record 136 bytes with a u64 `first_run_seq`. Shelf notes moved from the cache to the repository. |
| 3.0 | The single specification document split into three. **FORMAT.md** holds the on-disc bytes and the reader rules: the binary format rules, identity and hashing, chunking, compression, the object model, the disc and run model, the filesystem profiles and the volume tree, the capacity invariants, forward error correction, filters, manifests and the catalog, the reader and writer rules, the golden vectors, and the normative text of `FORMAT.txt`. **OPERATIONS.md** holds the workflow: the repository, staging and cache layouts, the local file formats, the staging state machine, refs and notes, concurrency and locking, commit, packing and locality, the capacity estimator, image building and burning, the burn plan, the disc lifecycle, verify, scrub and heal, restore, the metadata restore policy, the CLI reference, the configuration reference, the exit codes, the recovery actions, the conformance checklist, the test list and the burning-host commands. **NOTES.md**, this document, holds everything informative: the goals and phase plan, the design rationale by topic, the open judgement calls, the evidence and research, the worked examples, the implementation notes, the scheduling examples, the glossary, the references and this change log. No on-disc byte changed. No rule changed. Every rationale block, evidence block, measurement, worked example and estimate moved here unaltered, and every citation in it was rewritten to name FORMAT.md or OPERATIONS.md. |

---

## 11. Decision index

The thirteen decisions whose home is this document.

| Id | Decision | Section |
|---|---|---|
| D310 | Tier-2 burning is not implemented in version 1; `burn --print` already renders the command lines. | 2.7 |
| D356 | Parity must be computable within bounded memory; the whole run is never held at once. | 2.9 |
| D357 | Cross-disc layers 0 to 5 exist; layer 2, content-addressed re-fetch, is free and must be tried first. | 2.9 |
| D366 | A local index is an accelerator. The discs answer every question without it. | 2.10 |
| D505 | The parent snapshot's trees take the place of rsync's old copy, so the staging disk never needs a second copy of the source. | 2.12 |
| D523 | NoahsArk has no built-in scheduler. `commit` is a batch job run by an external scheduler, and the exclusive repository lock keeps two commits from running at once. | 2.12, 7 |
| D524 | Phase 1 has manual `commit` only. There is no daemon. A watcher never commits by itself. | 2.12 |
| D525 | `commit` is idempotent, so a watcher changes no format and no state. | 2.12 |
| D605 | Every burn test uses an image file first. Physical burns are a manual checklist, not CI. | 2.17 |
| D606 | The 59 required tests must all run. | 2.17 |
| D607 | A probe records an unknown answer; a test asserts a known one. A probe answer that becomes stable moves into the test list as a test. | 2.17, 7 |
| D609 | Probe 2 (Windows reads ISO 9660:1999 level 4 long lowercase names) is blocking; profile 2 must not be used in production until it passes. | 4.6, 7 |
| D618 | Chunk and hash in parallel across files. Write the run image single-threaded, because copy order is LBA order. | 6 |
