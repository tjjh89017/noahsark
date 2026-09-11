# NoahsArk Specification

Version: 2.3 (format major 1)
Status: design specification. No implementation exists yet.
Reference implementation language: Go (informative, section 24).

---

## Table of contents

- 1. Overview
  - 1.1 What a disc contains
  - 1.2 Reading this document
- 2. Goals, non-goals, and priorities
  - 2.1 Goals
  - 2.2 Non-goals
  - 2.3 Platform tiers
  - 2.4 Priorities
  - 2.5 Implementation phases
  - 2.6 Hard constraints
  - 2.7 Performance and resource requirements
- 3. Architecture
  - 3.1 Component diagram
  - 3.2 Write data flow
  - 3.3 Restore data flow
  - 3.4 Heal data flow
  - 3.5 Component list
  - 3.6 Trust boundaries
  - 3.7 Repository layout and discovery
  - 3.8 Security summary
- 4. Binary format rules
  - 4.1 The ten rules
  - 4.2 Magic values
  - 4.3 Common object header
  - 4.4 Feature flag registry
  - 4.5 Strings
  - 4.6 Registries
  - 4.7 Version policy
  - 4.8 Endianness and alignment test
  - 4.9 Hash coverage per structure
  - 4.10 CRC coverage per structure
  - 4.11 Limits
- 5. Identity and hashing
  - 5.1 The content id rule
  - 5.2 Hash algorithms
  - 5.3 Digest fields in records
  - 5.4 Text form
  - 5.5 Fan-out on disc
  - 5.6 Hash epochs
  - 5.7 Reindex, the optional cross-algorithm table
- 6. Chunking
  - 6.1 Algorithm
  - 6.2 Cut point rule
  - 6.3 Profiles
  - 6.4 Profile recording and change
  - 6.5 Small files and bundles
  - 6.6 Sparse files
  - 6.7 Determinism
- 7. Compression
  - 7.1 Order of operations
  - 7.2 Header fields
  - 7.3 Algorithm and level
  - 7.4 Heuristic
  - 7.5 Compression and dedup
  - 7.6 What is never compressed
- 8. Object model
  - 8.1 Object kinds
  - 8.2 Object graph
  - 8.3 Chunk and bundle
  - 8.4 Chunklist
  - 8.5 Tree
  - 8.6 Hardlinks
  - 8.7 Snapshot
  - 8.8 Ref
  - 8.9 Reserved crypto fields
  - 8.10 Canonical ordering summary
- 9. Disc, run, and append model
  - 9.1 The physical disc
  - 9.2 Disc filesystem profile
  - 9.3 The run
  - 9.4 Disc and run layout on the medium
  - 9.5 Disc superblock
  - 9.6 Run header
  - 9.7 Run layout table
  - 9.8 Append model
  - 9.9 Closing a disc
  - 9.10 Fallbacks
  - 9.11 Spare area exhaustion and raw append (profile 1 only)
  - 9.12 Forced capacity
  - 9.13 Why true UDF multi-session is not possible with current tools
- 10. Disc filesystems and burning
  - 10.1 Profile 0, `oneshot` (default, Phase 1)
  - 10.2 Profile 1, `udf201-pow` (Phase 2)
  - 10.3 Profile 2, `iso9660v1-l4-pow` (Phase 3)
  - 10.4 Files at the volume root
  - 10.5 Fill order inside a run
  - 10.6 Name and path budget
  - 10.7 Burning is externalized
  - 10.8 Command templates
  - 10.9 Probe commands
  - 10.10 Capacity table and fill limit
  - 10.11 Reserved space
  - 10.12 Burner backends
  - 10.13 Tier-2 burners
  - 10.14 Verification checklist
- 11. FEC and self-healing
  - 11.1 Why the design is what it is
  - 11.2 Layout
  - 11.3 Burst tolerance
  - 11.4 Checksum column
  - 11.5 Header replication
  - 11.6 Cross-disc layers
  - 11.7 Heal order
  - 11.8 Verify and scrub
  - 11.9 Health metric and report
  - 11.10 Encoding cost and memory
- 12. Filters, manifests, and catalog
  - 12.1 The index-free promise
  - 12.2 Run filter
  - 12.3 Run manifest
  - 12.4 Prerequisite list
  - 12.5 Snapshot objects, snapshot table, ref table and run table
  - 12.6 Disc directory
  - 12.7 Catalog contents per run
  - 12.8 Dedup rule
  - 12.9 Connectivity check
- 13. Local cache
  - 13.1 Location and contents
  - 13.2 Rebuild levels
  - 13.3 Staleness
  - 13.4 Cache-less operation
- 14. Staging store
  - 14.1 Layout
  - 14.2 Object state machine
  - 14.3 State log format
  - 14.4 GC rules
  - 14.5 Concurrency and locking
  - 14.6 Local repository state: refs, the pending snapshot chain, and notes
- 15. Packing and locality
  - 15.1 Why locality wins over dedup
  - 15.2 Rules
  - 15.3 Algorithm
  - 15.4 Controlled duplication
  - 15.5 Duplication accounting
  - 15.6 Capacity budget
  - 15.7 Split threshold
  - 15.8 Consolidation
- 16. Commit flow
  - 16.1 Commit flow
  - 16.2 The quick check
  - 16.3 Direct mode
  - 16.4 Mirror mode
  - 16.5 Sources and excludes
  - 16.6 In-flight change detection
  - 16.7 Filesystem snapshots as the source
  - 16.8 Scheduling
  - 16.9 Future watch trigger
  - 16.10 Remote source roots: NFS and SMB
  - 16.11 Remote sources by commit bundle (Backlog)
  - 16.12 Deployment modes for a remote data host
- 17. Restore and the disc plan
  - 17.1 The planner
  - 17.2 Disc-major order
  - 17.3 Staging budget
  - 17.4 The plan file
  - 17.5 Time model
  - 17.6 Disc detection
  - 17.7 Multi-drive restore
  - 17.8 Restore pipeline
  - 17.9 Cache-less restore
- 18. File metadata and permissions
  - 18.1 The field set
  - 18.2 Encodings
  - 18.3 Ownership policy
  - 18.4 Restore order
  - 18.5 Cross-platform capability matrix
  - 18.6 Failure policy
  - 18.7 Non-root restore
  - 18.8 Safety: names and symlinks
  - 18.9 Flags and exit codes
  - 18.10 Unstable entries
  - 18.11 Loss report
- 19. CLI reference
  - 19.1 `noahsark init` (Phase 1)
  - 19.2 `noahsark commit` (Phase 1)
  - 19.3 `noahsark sync` (Phase 2)
  - 19.4 `noahsark catalog export` (Backlog)
  - 19.5 `noahsark import` (Backlog)
  - 19.6 `noahsark watch` (Phase 3)
  - 19.7 `noahsark pack` (Phase 1)
  - 19.8 `noahsark append` (Phase 2)
  - 19.9 `noahsark burn` (Phase 1)
  - 19.10 `noahsark close` (Phase 2)
  - 19.11 `noahsark verify` (Phase 1)
  - 19.12 `noahsark scrub` (Phase 1)
  - 19.13 `noahsark health` (Phase 1)
  - 19.14 `noahsark plan` (Phase 1)
  - 19.15 `noahsark restore` (Phase 1)
  - 19.16 `noahsark rebuild-cache` (Phase 1)
  - 19.17 `noahsark consolidate` (Phase 3)
  - 19.18 `noahsark reindex` (Phase 3)
  - 19.19 `noahsark gc` (Phase 1)
  - 19.20 `noahsark ls` (Phase 1)
  - 19.21 `noahsark log` (Phase 1)
  - 19.22 `noahsark disc` (Phase 1)
  - 19.23 `noahsark image` (Phase 1)
- 20. Configuration reference
  - 20.1 Identity and format
  - 20.2 Hashing and chunking
  - 20.3 Compression
  - 20.4 Disc and filesystem
  - 20.5 Burner
  - 20.6 FEC
  - 20.7 Filters and manifests
  - 20.8 Sources and excludes
  - 20.9 Locality and packing
  - 20.10 Metadata
  - 20.11 Staging and cache
  - 20.12 Restore
  - 20.13 Scrub
- 21. Format evolution and compatibility
  - 21.1 The two mechanisms
  - 21.2 Change matrix
  - 21.3 Rules for a writer
  - 21.4 Rules for a reader
  - 21.5 What a Phase 1 reader does with a Phase 3 disc
  - 21.6 Reader and writer conformance
  - 21.7 Reader and writer interop matrix
- 22. Failure modes and recovery matrix
- 23. Testing and CI
  - 23.1 Image-first principle
  - 23.2 Required tests
  - 23.3 Composite action
  - 23.4 Probe actions
  - 23.5 Manual physical checklist
  - 23.6 Manual probes
  - 23.7 Phase 1 conformance checklist
  - 23.8 Golden vectors
- 24. Implementation notes
- 25. Design changes from the previous specification
- 26. Glossary
- 27. Document change log
- Appendix A. Gear table
  - A.1 Status
  - A.2 Generation rule
  - A.3 Mask constants
  - A.4 If a published table is adopted instead
- Appendix B. Magic numbers and registry summary
  - B.1 Magic values
  - B.2 Registries
- Appendix C. Command reference for the burning host
  - C.1 Probe the drive and the medium
  - C.2 Build a UDF 2.01 image (profile 0 and profile 1)
  - C.3 Burn
  - C.4 Verify
  - C.5 Recover a damaged disc
  - C.6 Read back the LBA map
  - C.7 Tool versions to check at startup
- Appendix D. Rejected and superseded alternatives
- Appendix E. Evidence: why true UDF multi-session is not possible
- Appendix F. References

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

The unit of writing is the **run**. One run is one execution of one burn plan
(section 10.7). Under profile 0 that is one growisofs write. A disc holds
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

### 1.2 Reading this document

The document uses ASD-STE100 style. "Must" states a requirement. "Should"
states a recommendation. "May" states an option.

Every binary structure has a byte-offset table. All integers are little-endian.
Section 4 states the rules that every structure obeys. Section 26 is the
glossary. Appendix B lists every magic number and every registry.

Some blocks are marked **informative**. An informative block gives an example,
a suggested path, or an operating procedure. It is not a requirement. Every
block that is not marked informative is normative.

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
(section 8.8), and the snapshot table has a `flags` byte (section 12.5). No
structure changes when the command arrives. Local staging retention
(`staging.retain_after_clean`, section 14.4) is a different thing: it governs
the local copy of an object that a disc already holds.

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
feature bit that Phase 1 does not know, or a disc filesystem profile that
Phase 1 does not implement (section 21.5).

| Phase | Content |
|---:|---|
| **1** | Disc filesystem profile 0 only, as section 10.1 defines it. Sealing with `pack --close`. FastCDC chunking. Dedup with filters and manifests. Cache-less restore. Per-run Reed-Solomon parity and `verify --heal`. All media sizes. Forced capacity and forced reserve. The staging state machine and GC, with a mandatory `verify` before an object becomes CLEAN. Manual `commit` only, with no watcher. The disc-major restore plan. The Phase 1 metadata set: type, mode, uid, gid, names, mtime, ctime, symlink target and hardlink group. |
| **2** | The `sync` mirror wrapper over rsync. Extended metadata: extended attributes, POSIX ACLs, atime and birth time, Windows attributes. Disc filesystem profile 1: POW append on UDF with the image mirror and the block diff. The LBA stability check after every append. The `close` and `append` commands, and the `when_full` close policy. Repair runs. The raw append degraded mode. Spare area monitoring. |
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

### 2.7 Performance and resource requirements

This subsection collects the numeric targets that the rest of the document
states in place. Each row names its normative home. A target marked
**requirement** must hold. A target marked **budget** is a design goal that the
reference implementation meets; a slower implementation still conforms.

| Item | Target | Class | Home |
|---|---|---|---|
| Commit cost on an unchanged file | One `stat`. The file is never opened. | requirement | 16.2 |
| Commit cost on an unchanged directory | One tree id comparison. | requirement | 16.1 |
| Staging space for a commit | The change set, never a second copy of the source. | requirement | 16.2.1 |
| FEC encoder working set | 512 MiB to 1 GiB per band, at `fec.band_stripes` 2048. | budget | 11.10 |
| FEC encode time, 25 GB run | Under 2 minutes on one core. Never the bottleneck against a 4x burn. | budget | 11.10 |
| Parity overhead per run | `m / (k + 1 + m)` = 9.02 percent of the stripe. | requirement | 11.2 |
| Catalog cost per run | Under 0.11 percent of a 25 GB disc at 2,000 runs. | budget | 12.2, 12.7 |
| Catalog cap per run | `catalog.max_bytes`, default 512 MiB. | requirement | 12.7.1 |
| Manifest cost per run | 64 bytes per object, about 0.0015 percent of the disc. | requirement | 12.3 |
| Filter false-positive rate | 2^-16 per run. | requirement | 12.2 |
| UDF overhead per object, P4 | About 3.1 KiB, under 0.1 percent of a 25 GB disc. | budget | 6.3 |
| Duplication overhead per repository | Warn above 5 percent. | budget | 15.5 |
| Restore peak staging | `restore.staging_budget`, default 16 GiB. Above it the planner splits into passes. | requirement | 17.3 |
| Restore disc switches | One per disc in the plan, which is the minimum. | requirement | 17.2 |
| Restore read rate for planning | `restore.rate_mb_s`, default 20 MB/s. | budget | 17.5 |
| Restore fixed cost per switch | `restore.switch_seconds`, default 60 s. | budget | 17.5 |
| Scrub throughput | About 15 minutes per 25 GB disc, about 20 discs per drive-day. | budget | 11.8 |
| Library scrub cycle | Under 12 months. | budget | 11.8 |
| Cache rebuild, level 1 | One disc mount, a few seconds. | budget | 13.2 |
| Cache rebuild, level 3 | One mount per disc, about 0.3 s of reading each. | budget | 13.2 |
| Reindex table cost | About 72 bytes per object; 15 minutes per 25 GB disc to build. | budget | 5.7 |
| Profile 1 append cost | About one block per new object. | budget | 10.2.5 |
| Profile 2 append cost | The whole directory tree, about 107 MiB at 100,000 objects. | budget | 10.3.3 |

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
   -> write recovered objects into staging/heal/, state STAGED
   -> mark the damaged run degraded, list the lost ids
   -> next pack writes them again: into the next data run in Phase 1,
      or into a repair run on the damaged disc from Phase 2
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
- Names inside a tree object are untrusted. Section 18.8 states the validation.
- A symlink target is data. The restorer never traverses it.
- A source on an NFS or SMB mount is untrusted for metadata. The mount may
  synthesize uid, gid and mode. The snapshot records the source type so that a
  restore can warn (section 16.10).
- A commit bundle is untrusted. `import` verifies every content id before it
  enters staging (section 16.11).

### 3.7 Repository layout and discovery

A **repository** is one local directory. It holds everything that is not on a
disc and not in the cache:

| Path | Content | Normative |
|---|---|---|
| `<repo>/config` | The configuration file of section 20. It holds `repo.uuid`. | Yes. |
| `<repo>/lock` | The repository lock file of section 14.5. | Yes. |
| `<repo>/staging/` | The staging store of section 14, unless `staging.dir` moves it. | Yes, the role. The name is the default. |
| `<repo>/refs.bin` | The local ref log of section 14.6: the current value of every ref, and the head of the pending snapshot chain. Authoritative for a ref whose run is not yet CLEAN. | Yes. |
| `<repo>/notes.bin` | Operator notes per disc: shelf location and a local display label, keyed by `disc_uuid` (sections 14.6 and 19.22). Convenience data. Losing it loses no archive data. | Yes, the role. The name is the default. |
| `<repo>/probes/` | Results of the manual probes of section 23.6, one text file each. | Informative. |

`init` creates the directory, `config` with a fresh `repo.uuid`, an empty
`staging/` with an empty state log, an empty `refs.bin`, and `lock`. A directory is a repository
when it holds a readable `config` whose `repo.uuid` parses as a uuid.

Discovery, in order. The first hit wins:

1. `--repo=PATH`.
2. The environment variable `NOAHSARK_REPO`.
3. The current directory, then each ancestor up to the filesystem root,
   nearest first. The first directory that is a repository wins.

A command that finds no repository exits with code 2 and says so. `init`
refuses a directory that already is a repository, and refuses to create a
repository inside another one.

The cache (section 13) is never inside the repository, so that deleting the
cache and deleting the repository stay independent acts. Every disc of a
repository carries `repo_uuid`, so a repository that was lost is recreated by
`init --repo-uuid=<uuid>` followed by `rebuild-cache`; nothing in the
repository directory is a source of truth except the state log for objects
that are not yet CLEAN and the local ref log for refs whose run is not yet
CLEAN (section 14.6).

Section 14.6 is the normative home of the three local files that hold state
between a commit and the verify of the run that carries it: the local ref
log, the pending snapshot chain and the notes file. It gives their record
layouts and the resolution order that every command uses.

### 3.8 Security summary

**Threat model.** The adversary is accident, decay and a hostile input, not a
person with the disc in hand. NoahsArk defends against a damaged medium, a
substituted or reordered disc, a corrupt cache, a malformed structure, and a
crafted archive that tries to make the restorer write outside its target.
It does not defend against anyone who holds the physical disc.

What is **not** defended, stated plainly:

- **No encryption.** Every object payload is plaintext. Possession of a disc
  is access to every byte on it. The fields are reserved (section 8.9) and
  unused.
- **No signing and no authentication of origin.** Nothing proves who wrote a
  disc. A content id proves that bytes match a name; it proves nothing about
  the author. An adversary who can write a whole coherent disc set, with a
  matching superblock chain, can present it as the repository.
- **No access control.** There is no user model, no permission model and no
  audit trail. File system permissions on the repository and the cache are
  the only barrier, and the locks of section 14.5 are advisory, not a
  security boundary.
- **No secrecy of metadata.** Path names, sizes, times and ownership are
  stored in the clear inside tree objects, and `README.txt` on every disc
  explains how to read them.
- **No protection against a deliberate downgrade of the local machine.** The
  repository config, the state log and the local ref log are ordinary files.
  An attacker with write access to them can make the next commit drop data.
  The discs already written are unaffected.

What **is** defended, and by which invariant, is the table below. Every item
in it holds against malformed input as well as against decay, because every
structure is validated before any field of it is used.

Security in this document is a set of invariants, each stated once in the
section that owns it. This subsection joins them.

| Invariant | Section |
|---|---|
| Bytes from a disc, from the cache, from a bundle and from a source mount are untrusted until they verify. | 3.6 |
| A content id is verified after decompression on every read. There is no trusted path. | 5.1, 24 |
| A filter hit never drops data; only an exact manifest hit does. | 12.8 |
| A tree entry name is validated at parse time: never empty, `.`, `..`, or a name with `/`, `\` or NUL. | 8.5.8 |
| The restorer never builds a path string, never follows a symlink on the way to a target, and treats a symlink target as data. | 18.8 |
| Ownership and times are applied with `AT_SYMLINK_NOFOLLOW` semantics. | 18.3, 18.4 |
| An ACL is never translated between models silently; a translation never widens access. | 18.6 |
| A burn plan is validated before it is rendered or executed: every CRC, every seek alignment, every payload hash. | 10.7.1 |
| `burn --exec` runs only the printed commands and refuses an unpatched burner. | 10.7, 10.12 |
| Locks are advisory and are not a security boundary. | 14.5 |
| Encryption and signing are reserved, not implemented. | 8.9 |

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
   `version_major` (u16), then `version_minor` (u16). A **structure** is a
   file-level container or an object payload header. A record inside a
   container, a tree entry, a TLV, the bundle trailer, and the checksum
   sector header are **records**, not structures. A record begins as its own
   table states, and it carries a magic only where its table says so.
5. **Version policy.** A reader must refuse a structure with an unknown
   `version_major`. A reader must accept an unknown `version_minor` and must
   ignore fields that it does not know.
6. **Feature flags.** Every top-level structure carries `required_feat` (u64)
   and `optional_feat` (u64). A reader must refuse the structure if any unknown
   bit is set in `required_feat`. A reader must ignore unknown bits in
   `optional_feat`.
7. **Checksum last.** Every structure carries a checksum, and every checksum
   field lies after every byte that it covers. A structure has one of two
   shapes. A structure with no separate body ends with one CRC over every
   byte that precedes it. A container with a header and a body carries two
   CRCs at the end of its header: `header_crc32c` over the header bytes that
   precede it, and `body_crc32c` over the body that follows the header; the
   header is then written last, after the body is final. Section 4.10 states
   what each CRC covers, per structure. CRCs use CRC-32C (Castagnoli,
   polynomial 0x1EDC6F41, reflected, initial value 0xFFFFFFFF, final XOR
   0xFFFFFFFF). Objects use the full content hash instead of a body CRC; the
   content id is the checksum.
8. **Hashed pointers.** Every pointer carries the hash of its target. No
   unhashed reference exists. This rule comes from GEFS.
9. **String encoding.** A string is `encoding` (u8), then `reserved` (u8[3],
   zero), then `length` (u32) in bytes, then the bytes. Section 4.5 gives the
   table. Encoding 0 is UTF-8. Other encoding values are reserved. There is no
   NUL terminator. There is no normalization; a writer stores the bytes as it
   found them.
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

Bits 7 to 63 of the required half and bits 4 to 63 of the optional half are
reserved. A writer must set them to zero.

**Bits set by a version 1 writer.** A feature bit belongs to the structure
that carries it. A reader checks the bits of the structure it reads and no
other. The table lists every bit that a version 1 writer may set, the
structure that carries it, and the condition. A structure that the table does
not name, and a condition that does not hold, give a zero field.

| Structure | Bit | Set when |
|---|---|---|
| Common object header (section 4.3) | `FEAT_COMPRESSION` | `compression` is not 0. |
| Tree header (section 8.5) | `FEAT_CHUNKLIST` | Any entry sets `CONTENT_IS_CHUNKLIST`, or any TLV sets `SPILL_IS_CHUNKLIST`. |
| Tree header (section 8.5) | `FEAT_TLV_SPILL` | Any TLV sets `SPILLED`. |
| Run header (section 9.6) | `FEAT_FEC_RS8` | Always. Every version 1 run carries parity. |
| Run layout table (section 9.7) | `FEAT_FEC_RS8` | Always. |
| Manifest container (section 12.3) | `FEAT_BUNDLES` | The `"BNDL"` chunk holds at least one bundle id. |
| Manifest container (section 12.3) | `FEAT_FAN16` | `fanout_bits` is 16. |
| Catalog container (section 12.7.2) | `OPT_XLATE` | A cross-algorithm side table (section 5.7) was copied into the catalog. Phase 3. |
| Run header (section 9.6) | `OPT_DISC_PARITY` | `run_kind` is 3, a disc-close parity run. Phase 3. |

`FEAT_CRYPTO`, `OPT_BITMAPS` and `OPT_REVIDX` are never set by a version 1
writer. The `"BMAP"` and `"RIDX"` manifest chunks that the last two would
announce are reserved with no payload defined in version 1 (section 12.3). The golden files of test 1
(section 23.2) carry these values, and a reader refuses a structure whose
`required_feat` holds any other bit.

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
| 1 | `rs255-gf8` | GF(2^8) | 255 | Default. A stripe is `k + 1 + m = 255` sectors; the code is over the `k + m` data and parity shards (section 11.2). |
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
| 11 | `Mini BD SL 8 cm` | 3,804,288 | 7,791,181,824 |
| 12 | `Mini BD DL 8 cm` | 7,608,576 | 15,582,363,648 |

The tool must take the true capacity from `growisofs -F`. The tool must not
use a hardcoded number for a burn. The table is for planning and for labels.

### 4.7 Version policy

- `version_major` changes when an old reader would misread the structure.
- Exactly three structures carry a `header_len` field: the common object
  header (section 4.3), the tree header (section 8.5) and the tree entry
  (section 8.5.1). For those, `version_minor` changes when fields are
  appended and `header_len` grows. A reader computes the end of such a header
  from `header_len`, not from its own compiled size. It skips
  `header_len - known_len` bytes.
- Every other structure has a fixed size under its `version_major`. It grows
  only by a `version_major` bump. A `version_minor` bump on such a structure
  may only give meaning to a field that was reserved, and the new meaning must
  be one that a reader of the older minor may ignore, as rule 3 of section 4.1
  already requires for a reserved field.
- A feature that an old reader can ignore gets an `optional_feat` bit, not a
  version bump.
- A feature that an old reader must not ignore gets a `required_feat` bit.
- Section 21 states the full evolution rules for each algorithm.

### 4.8 Endianness and alignment test

Every implementation must ship a golden-file test per structure. The test
writes a structure with known values and compares the bytes to a checked-in
file. The test also reads the checked-in file and compares the fields. This
catches an accidental change of layout, of padding, or of endianness.

### 4.9 Hash coverage per structure

Every hash field that names another structure covers **all the bytes of that
structure as they lie on the medium**, with every CRC field already filled in.
No hash is computed over a structure with its CRC zeroed. The table lists
every such field.

| Field | In | Covers | Algorithm |
|---|---|---|---|
| `manifest_hash` | Run header (section 9.6) | Every byte of `manifest.bin`. | Run header `hash_algo`. |
| `filter_hash` | Run header | Every byte of `filter.bin`. | Run header `hash_algo`. |
| `layout_hash` | Run header | Every byte of `layout.bin`, after the records and both CRCs are final. | Run header `hash_algo`. |
| `catalog_hash` | Run header | Every byte of `catalog/CATALOG.bin`. | Run header `hash_algo`. |
| `prev_run_header_hash` | Run header | The 512 bytes of the previous run header, CRC included. | Run header `hash_algo`. |
| `prev_disc_super_hash` | Disc superblock (section 9.5) | The 2048 bytes of the previous disc's superblock, CRC included. | Superblock `hash_algo`. |
| `super_hash` | Disc directory record (section 12.6) | The 2048 bytes of that disc's superblock, CRC included. | Container `hash_algo`. |
| `run_header_hash` | Run table record (section 12.5.2) | The 512 bytes of that run's header, CRC included. | Container `hash_algo`. |
| `file_hash` | Catalog entry (section 12.7.2) | Every byte of the named catalog file. | Container `hash_algo`. |
| `content_id` | Extent record (section 9.7), fixed-name file | Every byte of the file. Zero for the roles that section 9.7 lists. | Layout `hash_algo`. |
| `payload_hash` | Burn step (section 10.7.1) | Every byte of the image file, or the sorted tree listing. | `hash.current`. |
| `catalog_hash` | Commit bundle header (section 16.11.1) | Every byte of the exported `CATALOG.bin`. | Bundle `hash_algo`. |
| Content id | Object file name | The uncompressed payload only (section 5.1). Never the object header. | Run header `hash_algo`. |
| Sector digest | Checksum sector (section 11.4) | The 2048 bytes of one data sector as they lie on the medium. | BLAKE3-256, the first 8 bytes of the 32-byte digest. |

A reader verifies a hash before it uses any field of the hashed structure,
and it verifies the CRC of that structure as well. The CRC catches a torn
write. The hash catches a substituted file.

### 4.10 CRC coverage per structure

Rule 7 of section 4.1 allows one CRC or a header CRC plus a body CRC. The
table states what each CRC covers. A structure that the table does not name
carries no CRC and is covered by its content id or by the CRC of its
container.

| Structure | Field | Covers |
|---|---|---|
| Common object header (section 4.3) | `header_crc32c` | Bytes 0 to 59. The payload is covered by the content id. |
| Bundle header (section 8.3) | `header_crc32c` | Bytes 0 to 59 of the bundle header. |
| Bundle trailer (section 8.3) | `trailer_crc32c` | Bytes 0 to 27 of the trailer. The chunk payloads and the index are covered by the bundle's content id. |
| Chunklist, tree and snapshot payloads (sections 8.4, 8.5, 8.7) | none | The content id covers the whole payload, records included. |
| Reindex container (section 5.7) | `header_crc32c`, `body_crc32c` | Bytes 0 to 59; every record. |
| Disc superblock (section 9.5) | `super_crc32c` | Bytes 0 to 2043. |
| Run header (section 9.6) | `header_crc32c` | Bytes 0 to 507. |
| Run layout table (section 9.7) | `header_crc32c`, `body_crc32c` | Bytes 0 to 107; every extent record. |
| Burn plan container (section 10.7.1) | `header_crc32c` | Bytes 0 to 251. |
| Burn step record (section 10.7.1) | `step_crc32c` | Bytes 0 to 507 of the step. |
| Checksum sector (section 11.4) | `header_crc32c` | Bytes 0 to 11. The digests are checked as section 11.4 states. |
| Filter container (section 12.2) | `header_crc32c`, `body_crc32c` | Bytes 0 to 75; the fingerprint array. |
| Manifest container (section 12.3) | `header_crc32c`, `body_crc32c` | Bytes 0 to 59; every byte from offset 64 to the end, that is the TOC, the sentinel and every chunk. |
| Simple table container (section 12.5.1) | `header_crc32c`, `body_crc32c` | Bytes 0 to 59; every record. |
| Catalog container (section 12.7.2) | `header_crc32c`, `body_crc32c` | Bytes 0 to 59; every entry. |
| State log (section 14.3) | `header_crc32c`, `record_crc32c` | Bytes 0 to 59 of the header; bytes 0 to 91 of each record. |
| Local ref log and notes file (section 14.6) | `header_crc32c`, `record_crc32c` | Bytes 0 to 59 of the header; every byte of each record before its CRC. |
| Commit bundle header (section 16.11.1) | `header_crc32c` | Bytes 0 to 251. |

### 4.11 Limits

Every limit that a structure fixes is listed here. A writer refuses an input
that exceeds a limit and names the limit. A reader refuses a structure that
exceeds a limit and names the limit.

| Item | Limit | Where it is fixed |
|---|---:|---|
| Digest length | 32 bytes | Section 5.3. A longer digest needs a new major version. |
| Tree entry name | 1 to 4095 bytes | `name_len`, section 8.5.1. |
| Tree entries per directory | 2^32 - 1 | `entry_count`, section 8.5. |
| Inline chunk ids per entry | writer 64, reader any | `chunklist.inline_max`, section 8.4. |
| Chunklist entries | 2^64 - 1 | `entry_count`, section 8.4. A level 1 chunklist is required only above the writer's chunklist size target. |
| Tree entry length | 2^32 - 1 bytes | `entry_len`, section 8.5.1. |
| TLV payload | 2^32 - 1 bytes; spilled above `tree.tlv_spill_threshold` | Sections 8.5.4 and 8.5.6. |
| Snapshot metadata TLVs | 65,535 per snapshot | `meta_count`, section 8.7. |
| Ref name | 1 to 40 bytes | Section 8.8. |
| Disc label | 64 bytes in the superblock, 48 bytes in the disc directory | Sections 9.5 and 12.6. |
| Run sequence number | 1 to 9,999,999,999 | The 10-digit run directory name, section 10.4. |
| Manifest fan-out | 256 or 65,536 entries | Section 12.3. |
| Objects per run | 2^64 - 1; a 16-bit fan-out above 1,000,000 | Section 12.3. |
| Burn plan steps | 65,535 | `step_count`, section 10.7.1. |
| Burn plan device hint | 64 bytes | Section 10.7.1. |
| Burn step source path | 256 bytes | Section 10.7.1. A longer staging path is an error. |
| Burn step auxiliary path | 184 bytes | Section 10.7.1. A longer path is an error. |
| Commit bundle host name | 64 bytes | Section 16.11.1. |
| Shelf note | 128 bytes | Section 14.6. |
| File size | 2^64 - 1 bytes | Every size field is u64, section 2.6. |
| Disc capacity | 2^64 - 1 sectors | Section 9.5. |
| FEC geometry | `k = 231`, `m = 23` | Section 11.2. |
| On-disc object name | 68 characters | Section 5.4. |
| Any on-disc name | 126 characters | Section 10.6. |
| Any on-disc path | under 220 characters | Section 10.6. |
| Host UDF name | 254 bytes | Section 10.1.3. |
| Host UDF path | 1023 bytes by the standard | Section 10.1.3. |
| Host ISO 9660 name | 207 bytes | Section 10.3.2. |

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

A digest field is always 32 bytes: the digest, left-aligned, zero-padded. The
field is 32 bytes even when the algorithm is shorter. A future 512-bit
algorithm needs a new record version, not a new field inside version 1.

The algorithm of a digest is stated by exactly one of two places:

1. the `hash_algo` and `digest_len` fields of the structure that contains the
   record. They apply to every digest in that structure, unless rule 2 applies;
2. a `hash_algo` field inside the record itself, where the table of that
   record lists one. It applies to the digests that the table names.

A structure never holds a digest whose algorithm neither place states. Section
5.6 lists the cross-epoch references, which are the only places that need rule
2. The reindex container of section 5.7 is the one structure whose header
names two algorithms; its record table says which field uses which.

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

Section 10.6 shows that 68 characters is safe under every UDF and Windows name
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
| 0, `oneshot` | 1, or optionally 2 (`objects/<d0><d1>/<d2><d3>/<id>`) | There is no append. |
| 1, `udf201-pow` | 1, or optionally 2 | A UDF append rewrites only the changed directories, so a second level costs nothing. |
| 2, `iso9660v1-l4-pow` | 1 only | Each ISO 9660 append rewrites every directory record. Two levels would create up to 65,536 directories and would cost about 107 MiB per append at 100,000 objects. |

Profile 0 and profile 1 allow two levels. Profile 2 allows one. This table is
the one normative statement of the rule; `fanout_levels` in the superblock
(section 9.5) and `fs.fanout_levels` (section 20.4) record the choice.

The disc superblock records the choice in `fanout_levels`. The longest object
path is `/NOAHSARK/objects/ab/<68>`, that is 89 characters at one level and 92
at two. Section 10.6 gives the budget.

### 5.6 Hash epochs

An **epoch** is a maximal run of runs that share one hash algorithm.

- The repository config holds `hash.current`. New objects use it.
- Every run header records the algorithm of the objects in that run.
- Every disc superblock records the algorithm of its first run. The
  superblock is written once and never updated (section 9.5); the newest run
  header names the current algorithm of the disc.
- Old discs keep their algorithm forever. Nothing rewrites them.

At an epoch boundary, dedup drops to zero by default. The same bytes hash to a
different id, so no new object can match an old one. The first backup after the
change rewrites the whole live data set. For a 2 TB archive that is about 80
BD-R 25 GB discs. This is the reason to choose the default hash carefully now
and to change it at most once per decade, and only for a break in the current
algorithm.

Restore is unaffected. Verification is unaffected. Each run is self-consistent
and names its own algorithm.

**Cross-epoch references.** The tree graph below one snapshot is single
algorithm: the root tree, every tree, every chunklist and every chunk id under
it use the `hash_algo` of the snapshot header. Only six fields may name an
object under another algorithm, and each one states its algorithm:

| Field | Where | Algorithm stated by |
|---|---|---|
| `parent` | Snapshot header (section 8.7) | `parent_hash_algo` in the same header. |
| `content_id` | Prerequisite record (section 12.4) | The run header of `run_seq`, which holds the object. |
| `snapshot_id`, `root_tree` | Snapshot table record (section 12.5) | `hash_algo` in the same record. |
| `parent_id` | Snapshot table record (section 12.5) | `parent_hash_algo` in the same record. |
| `snapshot_id` | Ref record (section 8.8) | `hash_algo` in the same record. |

Every other digest uses the algorithm of its containing structure.

### 5.7 Reindex, the optional cross-algorithm table

`noahsark reindex --to <algo>` reads old runs once and writes a side table.

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

The container is a cache file (section 13.1) and is Phase 3.

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

The pseudocode of section 6.2 is the normative definition of the cut points.
Two-byte rolling, and any other speed technique, is an optimization that must
give exactly the cut points of section 6.2; test 3 of section 23.2 proves it.

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

Three points fix the reading of the pseudocode.

1. `fp` is an unsigned 64-bit integer. The shift and the add both wrap modulo
   2^64. Bits shifted out of bit 63 are discarded.
2. `data[i]` is indexed from the **start of the current chunk**, not from the
   start of the file. `i` is therefore an offset inside the chunk, and `len`
   is the number of bytes left in the file from the chunk start.
3. `min` is the count of bytes that the chunker skips before it hashes
   anything. The loop starts at `i = min` and a cut returns `i + 1`, so the
   shortest chunk that a cut can produce is **`min + 1` bytes**. The
   pseudocode is the definition and must not be changed to make the minimum
   exactly `min`.

The chunker must not evaluate the hash before offset `min`. A file shorter than
`min` is exactly one chunk.

### 6.3 Profiles

`max = 4 * avg` and `min = avg / 4` in every profile. This is the FastCDC paper
ratio and it keeps normalization level 2 in its intended regime.

| Id | Name | min | avg | max | Objects per 25 GB run | Objects per 100 GB run | UDF overhead per 25 GB |
|---:|---|---:|---:|---:|---:|---:|---:|
| 1 | P3 | 512 KiB | 2 MiB | 8 MiB | ~11,933 | ~47,733 | ~36 MiB (0.15%) |
| 2 | **P4** | **1 MiB** | **4 MiB** | **16 MiB** | **~5,967** | **~23,867** | **~18 MiB (0.08%)** |
| 3 | P5 | 2 MiB | 8 MiB | 32 MiB | ~2,984 | ~11,934 | ~9 MiB (0.04%) |

The counts are `ceil(capacity_bytes / avg)` at the capacities of section 4.6,
which is the `objects_per_run` figure that section 10.11 uses.

**P4 is the default.** It gives about 6,000 objects on a 25 GB disc, which is
comfortable for a UDF directory tree, for a per-run manifest, and for a filter.
A 4 MiB chunk still catches the shifted-insert edits that a 16 MiB fixed chunk
misses.

The per-object UDF cost is about 3.1 KiB: one 2048-byte block for the File
Entry ICB, about 40 bytes plus the name for the File Identifier Descriptor in
the parent directory, and on average 1 KiB of tail padding to the block
boundary.

Restore seek cost, worst case, at 150 ms per seek and one seek per chunk:

| Profile | Seeks per GiB | Seek time per GiB |
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

The bundle rule has one form: **any chunk whose uncompressed payload is below
`bundle.threshold` (default 1 MiB) goes into a bundle** (section 8.3). A file
smaller than `min` is one chunk, so it goes into a bundle. A tail chunk below
the threshold goes into a bundle. A chunk at or above the threshold is stored
as its own file.

The reason is UDF overhead and seeks. One UDF File Entry costs 2,048 bytes. The
directory entry costs about 1,024 more. A 40 KB photo therefore pays 3.1 KiB of
overhead, which is 8 percent, and one seek on restore. A
directory of 30,000 photos would become 30,000 UDF files and 90 minutes of pure
seeking.

### 6.6 Sparse files

There is no extent table in the format for sparse regions.

- An all-zero region cuts into identical chunks of exactly `max` bytes. This
  is a property of the Gear table and the masks, not an assumption: the zero
  chunk golden vector of section 23.8 verifies it for every profile, and a
  writer relies on the vector, not on the statement. Those chunks dedup to one
  object in the repository.
- On restore, the restorer detects an all-zero chunk by scanning its bytes,
  never by comparing its id to a constant. It skips the write, or it punches a
  hole. The file becomes sparse again. Informative: on Linux the hole is
  punched with `fallocate(FALLOC_FL_PUNCH_HOLE)`.
- The backup may use `SEEK_HOLE` and `SEEK_DATA` to skip holes quickly. This is
  a speed optimization only. It must not change the object stream. A file read
  with and without the optimization must produce the same chunk ids.
- A `SPARSE` flag bit exists in the tree entry (section 8.5). It is a hint for
  the restorer. It is not a data structure.

The maximum-size zero chunk for profile P4 is 16 MiB of zero bytes. Its content
id under BLAKE3-256 and under SHA-256 is a golden vector (section 23.8). A
writer must not special-case it, and a restorer must not depend on it; it is an
ordinary object that dedup finds.

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
- The payload of a bundle object as a whole. Its common object header carries
  `compression` 0; the chunks inside it are compressed one by one
  (section 8.3).

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

**Kind 6 is never an object file.** A ref is a row in the ref table
(section 8.8), so it has no content id and no file of its own. The value 6
must never appear in the `kind` field of a manifest record (section 12.3), of
a layout extent record (section 9.7), or of a state log record
(section 14.3). A reader that finds it there refuses the record and names the
structure. The id stays reserved in the registry so that no other kind takes
it.

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

A **bundle** holds every chunk whose uncompressed payload is below
`bundle.threshold` (default 1 MiB), as section 6.5 states. The target bundle
size is `bundle.target_size` (default 64 MiB). A bundle is filled in path order,
which is the depth-first order of section 15.2. A bundle may span sibling
files and sibling directories; only the path order is required. Restoring a
photo folder then reads one or two contiguous bundles.

Bundle payload layout:

```
 [ common object header, kind = 2, compression = 0 ]
 [ bundle header, 64 bytes                    ]
 [ chunk payload 0 ][ chunk payload 1 ] ...    stored bytes only, no header
 [ index table: entry_count * 64 bytes         ]
 [ bundle trailer, 32 bytes                    ]
```

A chunk payload inside a bundle carries **no common object header**. The
index entry is its header: it holds the id, the offset, the stored and
uncompressed lengths and the compression id of that chunk. Each chunk is
compressed on its own, with the heuristic of section 7.4, so one bundle may
mix compressed and uncompressed chunks. The bundle's own common object header
carries `compression` 0, and its `payload_len` and `stored_len` are both the
length of the bundle payload from the bundle header to the end of the trailer.

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
chunk id to `(bundle, offset)` through the bundle table of section 12.3. A
reader that has the manifest locates the bundle file, reads its index once,
and then does one ranged read per chunk it needs; the manifest record holds no
stored length, so the index supplies it. A reader with no manifest reads the
bundle and uses its index in the same way.

A bundle must not hold a delta. A delta chain on write-once media is a
durability hazard: one bad bundle breaks the chain.

### 8.4 Chunklist

A file with more than `chunklist.inline_max` chunks (writer default 64)
references a chunklist object instead of an inline chunk array. Trees stay
small, and an unchanged large file costs one reference. The limit is a writer
choice, not a format limit: a reader accepts any inline count that
`content_len` states.

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
| 2 | `CTIME_ABSENT` | `ctime_sec` and `ctime_nsec` carry no information. A writer sets it only when the source reported no ctime (section 16.10). A Phase 1 writer stores ctime whenever the source reports one, because the quick check of section 16.2 compares it. |
| 3 | `BTIME_ABSENT` | `btime_sec` and `btime_nsec` carry no information. |
| 4 | `SPARSE` | The source file had holes. The restorer punches holes, as section 6.6 states. |
| 5 | `METADATA_PARTIAL` | The source read failed for at least one metadata field. |
| 6 | `CONTENT_IS_CHUNKLIST` | The content area holds one chunklist id, not chunk ids. |
| 7 | `UNSTABLE` | The file changed while it was being read, and no earlier consistent version existed. The content is one possible read of a moving file. Section 16.6 states when a writer sets it; section 18.10 states what a restore does with it. |

The byte is full. All eight bits are assigned and none is reserved. A new
per-entry fact therefore goes into a TLV (section 8.5.4), never into this
byte, and a critical new fact takes a TLV type in the reserved critical range
0x8000 to 0xBFFF so that an old reader refuses the entry instead of ignoring
the fact. Widening the field would change the entry layout and needs a new
`version_major`.

#### 8.5.3 Variable areas

| Area | Start | Length | Content |
|---|---|---|---|
| Name | `name_off` | `name_len` | Raw bytes of exactly one path component. |
| Content refs | `content_off` | `content_len` | See below. |
| Extension TLVs | `ext_off` | `ext_len` | TLV records, sorted. |

Content area by entry type:

| `entry_type` | `content_len` | Content |
|---|---|---|
| 1 regular, inline | `32 * chunk_count` | Chunk ids in file order. `chunk_count = content_len / 32`. A writer inlines at most `chunklist.inline_max` ids; a reader accepts any count. |
| 1 regular, chunklist | 32 | One chunklist id. `CONTENT_IS_CHUNKLIST` is set. |
| 1 regular, empty | 0 | No content area. |
| 2 directory | 32 | One tree id. |
| 3 symlink | 0 | The target is TLV `SYMLINK_TARGET`. |
| 4, 5, 6, 7 | 0 | No content. |

#### 8.5.4 Extension TLV record

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 2 | u16 | `tlv_type` | Registry, 8.5.5. |
| 2 | 2 | u16 | `tlv_flags` | bit0 `CRITICAL`, bit1 `SPILLED`, bit2 `SPILL_IS_CHUNKLIST`, bits 3 to 15 reserved. |
| 4 | 4 | u32 | `tlv_len` | Payload bytes, excluding this prefix and excluding padding. |
| 8 | `tlv_len` | u8[] | `payload` | The value, or the spill reference. |
| | pad | u8[] | | Zero bytes to the next 8-byte boundary. |

TLVs are sorted ascending by `tlv_type`, then by payload bytes. Canonical order
is mandatory: identical metadata must hash identically. A registered type
(0x0001 to 0xBFFF) appears at most once per entry. Only a vendor type (0xF000 to
0xFFFF) may repeat.

A reader that meets an unknown TLV with `CRITICAL` set must refuse the entry. A
reader that meets an unknown TLV without `CRITICAL` must keep it on copy and
must report it on restore.

When `SPILLED` is set, the payload is exactly 40 bytes: a 32-byte content id
and a u64 uncompressed length. The id names a chunk, or a chunklist when
`SPILL_IS_CHUNKLIST` is also set. The rule for spilling is in section 8.5.6.

#### 8.5.5 TLV type registry

| Type | Name | Critical | Payload |
|---:|---|---|---|
| 0x0001 | `SYMLINK_TARGET` | yes | Raw bytes. Mandatory when `entry_type` is 3. Never validated as UTF-8. |
| 0x0002 | `USER_NAME` | no | UTF-8 bytes. |
| 0x0003 | `GROUP_NAME` | no | UTF-8 bytes. |
| 0x0004 | `ROOT_PATH` | no | Raw bytes of a source root's absolute path. Only on an entry of the root tree (section 16.5). |
| 0x0010 | `XATTR` | no | `u32 count`, then `count` items of `{u32 name_len, u32 value_len, name, value}`. Each item as a whole is zero-padded to a multiple of 4 bytes after `value`; `name` and `value` are not padded separately. Sorted by name bytes. |
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
the writer spills the largest eligible payloads. An eligible payload is chunked
with the run's chunker profile, exactly like file content. When that gives one
chunk, the TLV holds the 40-byte spill reference to that chunk with `SPILLED`
set. When it gives more than one chunk, the writer stores a chunklist object
over them, and the TLV holds the spill reference to the chunklist with both
`SPILLED` and `SPILL_IS_CHUNKLIST` set. The u64 length is the uncompressed
payload length in both cases.

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

The id is derived once per source inode, by a rule that is stable across
commits:

```
hardlink_group = u64le( BLAKE3-256( repo_uuid || u64le(st_dev) || u64le(st_ino) )[0 .. 7] )
if hardlink_group == 0: hardlink_group = 1
```

`repo_uuid` is the 16-byte repository uuid. `st_dev` and `st_ino` are those
of the **source** file. In direct mode the walker reads them from the source.
In mirror mode (section 16.4) they come from the stat listing of the source,
never from the mirror copy, whose inodes belong to the staging disk. The id
is compared only inside one snapshot. It is stable, so an unchanged directory
keeps its tree hash from one commit to the next. It is not the raw inode
number, so it leaks no host state and it cannot be confused with an inode.
When the source does not report link counts (`NO_HARDLINKS`, section 8.7),
no group is formed.

When a stat listing reports link counts but no device or inode numbers, the
writer derives the id from the source path set of the group instead. The
group members are the mirror files that share one mirror inode, which
`rsync -H` preserves. The rule is:

```
paths          = the source paths of the group, sorted by raw bytes, each
                 followed by one NUL byte
hardlink_group = u64le( BLAKE3-256( repo_uuid || 0x01 || paths )[0 .. 7] )
if hardlink_group == 0: hardlink_group = 1
```

The `0x01` byte separates this rule from the device-and-inode rule, so the
two never collide. A path-set id changes when a member is added or removed;
the writer then re-emits every member of the group with the new id, so that
all members of one group in one snapshot carry one id. The snapshot records
which rule was used in `source_flags` bit 6, `HARDLINK_BY_PATH`.

Consequences:

1. Restoring one member of a group produces a correct regular file.
2. There is no dangling-link failure mode, unlike tar.
3. The tree does not depend on traversal order, so content addressing holds.
4. Dedup makes the repeated content reference free at the chunk level.

The restorer keeps a map from `hardlink_group` to the first restored
`(directory, name)`. On a second member it creates a hard link to that first
member, by directory descriptor and name and never by a path string
(section 18.8). On failure it writes the content again and records a
`hardlink_degraded` event. Informative: on Linux the link is made with
`linkat`.

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
| 120 | 8 | u64 | `object_count` | Distinct content ids reachable from `root_tree`: chunks, chunklists and trees, the root tree included. Bundles are not counted, and the snapshot itself is not counted. |
| 128 | 1 | u8 | `hash_algo` | Multicodec code of `root_tree` and of every id below it. |
| 129 | 1 | u8 | `chunker_profile` | Chunker profile id used to produce it. |
| 130 | 2 | u16 | `meta_count` | Number of TLV records that follow. |
| 132 | 1 | u8 | `source_type` | Where the source tree was read from. See below. |
| 133 | 1 | u8 | `source_flags` | What the source could not provide. See below. |
| 134 | 1 | u8 | `parent_hash_algo` | Multicodec code of `parent`. Equals `hash_algo` except across an epoch boundary. 0 for a root. |
| 135 | 1 | u8 | `reserved_u8` | Zero. |
| 136 | | | TLV records | `meta_count` records follow. |

Metadata TLV record:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 2 | u16 | `tag` | Snapshot metadata tag registry, Appendix B.2: 1 author, 2 host, 3 message, 4 source root, 5 exclude rules, 6 checksum commit. Tags 4 and 5 repeat, one per root, in root order. |
| 2 | 2 | u16 | `flags` | bit0 CRITICAL. |
| 4 | 4 | u32 | `len` | Payload bytes. |
| 8 | `len` | u8[] | `value` | UTF-8 for tags 1 to 3. Raw path bytes for tag 4. UTF-8 pattern lines for tag 5. Empty for tag 6. |
| | pad | | | Zero to the next 4-byte boundary. |

`source_type` records where the source tree was read from:

| Id | Name | Meaning |
|---:|---|---|
| 0 | `unknown` | Not recorded. A pre-1.0 writer. |
| 1 | `local` | A local filesystem. Full metadata fidelity. |
| 2 | `snapshot` | A filesystem snapshot of a local filesystem. |
| 3 | `nfs` | An NFS mount. |
| 4 | `smb` | An SMB or CIFS mount. |
| 5 | `bundle` | Imported from a commit bundle (section 16.11). The bundle writer's own source type is in its `BUNDLE.bin`. |

`source_flags` records what the source could not provide:

| Bit | Name | Meaning |
|---:|---|---|
| 0 | `NO_CTIME` | ctime was not trusted, so the quick check used size and mtime only. |
| 1 | `NO_HARDLINKS` | The source did not report link counts, so hardlink groups were not detected. |
| 2 | `NO_SPARSE` | `SEEK_HOLE` was unavailable, so holes were found by reading. |
| 3 | `SYNTHETIC_IDS` | uid, gid or mode may have been synthesized by the mount. |
| 4 | `CASE_INSENSITIVE` | The source did not distinguish names by case. |
| 5 | `MTIME_SLACK` | An mtime slack was applied in the quick check. |
| 6 | `HARDLINK_BY_PATH` | Hardlink group ids were derived from source path sets, not from device and inode numbers (section 8.6). |
| 7 | reserved | Zero. |

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
| 44 | 2 | u16 | `name_len` | Byte length of the name, 1 to 40. |
| 46 | 1 | u8 | `hash_algo` | Multicodec code of `snapshot_id`. |
| 47 | 1 | u8 | `reserved_u8` | Zero. |
| 48 | 40 | u8[40] | `name` | UTF-8, zero-padded. A name above 40 bytes is refused at commit time. The limit is part of the format. |
| 88 | 8 | u64 | `run_seq` | Run that recorded this value. |

The table is sorted by `name` bytes, unsigned, ascending, then by `time_sec`
ascending, then by `snapshot_id` bytes ascending (section 12.5.1). It is
append-only across runs. A reader takes the newest record for each name: the
highest `run_seq`, then the highest `time_sec`, then the highest `time_nsec`.
A record with `run_seq` 0 exists only in the local ref log of section 14.6,
never on a disc. `LATEST` is the reserved name for the newest snapshot. This is a reflog, not a ref: the whole history of the pointer
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

Every "ascending" in this table, and everywhere else in this document, is an
unsigned bytewise comparison: the first differing byte decides, and a
shorter string that is a prefix of a longer one sorts first.

Two identical directories must serialize to identical bytes. A writer that
breaks a rule in this table breaks dedup silently.

---

## 9. Disc, run, and append model

### 9.1 The physical disc

A physical disc has these properties:

- a `disc_uuid`, 16 bytes, generated once and never reused;
- a human label, printed on the disc;
- a `disc_seq`, u64, monotonic inside the repository. It is **0-based**: the
  first disc of a repository is disc 0. A run is numbered by `run_seq`, u64,
  monotonic inside the repository and **1-based**: the first run is run 1, so
  that a `run_seq` of 0 can mean "no run" in every structure that needs that
  sentinel;
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
The registry table in section 4.6 is the one normative list of profiles.
Appendix B repeats it. Section 10 specifies each profile in full: profile 0 in
section 10.1, profile 1 in section 10.2, profile 2 in section 10.3.

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

### 9.3 The run

A **run** is one execution of one burn plan (section 10.7). Under profile 0
and profile 1 variant 1b that is one or more growisofs writes; under
profile 1 variant 1a it is a mount, a copy and an unmount. It is the unit
of:

- packing;
- the manifest;
- the filter;
- the Reed-Solomon parity;
- the catalog copy.

A disc holds one or more runs. A run occupies a contiguous LBA range. A run is
self-contained: it carries its own header at its start and again at its end, its
own manifest, its own filter, its own layout table, and its own parity.

**FEC terms.** This table is a complete forward definition of every term that
sections 9.4 to 9.7 use. Section 11.2 repeats it with the diagram, the burst
bound and the encoding order, and section 11.2.1 defines the arithmetic of the
code. Nothing in sections 9.4 to 9.7 needs a term that is not here.

| Term | Meaning |
|---|---|
| `k`, `m` | The data column count and the parity column count. Version 1 fixes `k = 231` and `m = 23`, so `k + 1 + m = 255`. |
| `lba_base` | The first sector of the run's parity domain. For the first run of a disc it is **LBA 0**, so that the filesystem descriptors, the anchor at LBA 256 and every directory block below `RUN.bin` are protected. For every later run it is the first sector of that run's `RUN.bin`. |
| `data_span` | The number of sectors from `lba_base` to the **last sector that step 6 of section 10.5 occupies**, inclusive. Under profile 0 and profile 1 that last sector is the File Entry block of the last file of step 6, because a UDF File Entry follows its file data (section 10.1.8). Under profile 2 it is the last data sector of that file, because ISO 9660 keeps its directory records elsewhere. Steps 7 to 10, that is `pad.bin`, `checksum.bin`, the parity files and `RUN2.bin`, are outside `data_span`. |
| `L`, column length | The number of sectors in one column. `L = ceil(data_span / k)`. |
| Column | A range of `L` sectors. The `k` data columns are the LBA ranges `[lba_base + c*L, lba_base + (c+1)*L)` for `c = 0 .. k-1`. Column `k`, the checksum column, is the `L` data sectors of `checksum.bin`. Columns `k+1` to 254 are the `m` parity columns, each the `L` sectors that follow the header sector of one `parity/pNNNN.bin` file. |
| Parity domain | The `k` data columns together: the contiguous range `[lba_base, lba_base + k*L)`. Every sector in it is protected, whatever file or filesystem structure it belongs to. `pad.bin` fills the domain from the end of `data_span` to the end of the domain (section 10.5). |
| Stripe | Sector `i` of every column, for `i = 0 .. L-1`. A stripe is 255 sectors: `k` data, 1 checksum, `m` parity. The code covers the `k` data and the `m` parity sectors; the checksum sector is outside the code (section 11.4). |

The File Entry block of `pad.bin`, of `checksum.bin` and of every parity file
lies at or after `lba_base + k*L`, outside the domain. Those blocks are
filesystem metadata that the layout table makes unnecessary for a reader.

"Multi-session" in NoahsArk means "many runs on one disc". Under profile 1 and
profile 2 the disc always has exactly **one** physical session, because both
profiles grow one volume on a Pseudo-OverWrite formatted BD-R. That single fact
is what makes macOS read every appended object.

### 9.4 Disc and run layout on the medium

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
 |  |--------  end of the parity domain (section 11.2)  --------|     |
 |  | checksum.bin   the checksum column, L sectors             |     |
 |  | parity/p0232.bin .. parity/p0254.bin                      |     |
 |  |   (each parity file: one run header sector, then L)       |     |
 |  | RUN2.bin       run header copy, last file copied          |     |
 |  +----------------------------------------------------------+      |
 +--------------------------------------------------------------------+

 Example, BD-R SL 25 GB, capacity 12,219,392 sectors, fill ratio 0.95,
 spare:min (section 10.11.3 gives the arithmetic):
   fill limit = 11,346,278 sectors
   run 1    = LBA          0 .. 4,096,511      (4,096,512 sectors, 8.39 GB)
   run 2    = LBA  4,096,512 .. 8,192,511      (4,096,000 sectors)
   run 3    = LBA  8,192,512 .. 11,346,277     (3,153,766 sectors)
   reserve  = LBA 11,346,278 .. 12,219,391     (873,114 sectors: the safety
                                               margin and the POW spare)
```

The example is illustrative. Real run boundaries follow from the packer and
from the next writable address that the drive reports. The first run's
parity domain starts at LBA 0, so the anchor at LBA 256, the volume
descriptors, the integrity descriptor, the space bitmap and every directory
block that lies below `RUN.bin` are inside it. A later run's domain starts at
its own `RUN.bin`.

Under profile 2 the filesystem directory records of every earlier run are
rewritten inside the newest run, past the next writable address. Those bytes
are part of the newest run and are therefore covered by the newest run's
parity. The data extents of earlier runs are untouched and stay covered by
their own parity. Under profile 1 only the changed filesystem blocks are
rewritten, and they are rewritten **in place, at their old LBAs**, because a
UDF File Entry cannot move. Some of those LBAs lie inside an earlier run's
parity domain. Section 11.8 states how verify treats such a sector: it is
filesystem metadata, its digest is not compared, and the healer counts it as
an erasure. The parity of the earlier run is never recomputed.

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
| 72 | 8 | u64 | `fill_limit_sectors` | Number of sectors, counted from LBA 0, that the writer may use. No run, parity included, ends at or above this LBA. Section 10.11 is the normative home of this value, of `data_budget` and of the reserve; it gives the definition, the invariants and the reference estimator. |
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
| 227 | 1 | u8 | `sealed` | 1 when the disc was burned sealed: `spare:none` and `-dvd-compat` at its first and only write (section 10.1.5). 0 when the disc was left open. |
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

Section 10.11 defines `fill_limit_sectors`, `reserve_computed_sectors`,
`reserve_forced_sectors` and `reserve_extra_sectors`, and states which value
the writer records in each. The superblock records the values the writer
actually used, not a recomputation.

Every field above is decided before the first byte of user data is written. A
later append never touches the superblock. The algorithm, profile and
compression fields describe the first run; a later run records its own values
in its run header, and the newest run header is where a reader looks for the
current values.

The superblock needs no second copy of its own: it is written directly after
`RUN.bin` in the first run, so it lies inside the parity domain of the first
run (section 11.2), and the Reed-Solomon layer reconstructs it after local
damage. The run headers carry the disc uuid, the repository uuid and the
disc sequence number as well, so the identity of a disc survives even the loss
of `DISC.bin`.

### 9.6 Run header

The run header is 512 bytes. It is written as `RUN.bin` at the first sector of
the run, in the first sector of every parity file, and as `RUN2.bin` at the
end of the run. That is `m + 2` copies. Every copy is byte-identical.

Every copy occupies one whole 2048-byte sector: the header is bytes 0 to 511
of that sector, and bytes 512 to 2047 are zero. **The files `RUN.bin` and
`RUN2.bin` are therefore 2048 bytes long, not 512.** Their layout records
give `byte_len` 2048 and `sector_count` 1. The `prev_run_header_hash` of
section 4.9 and the `run_header_hash` of section 12.5.2 cover the 512 header
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
| 72 | 8 | u64 | `lba_base` | First LBA of the run's parity domain. 0 for the first run of a disc. The LBA of `RUN.bin` for every later run (section 9.3). |
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
| 107 | 1 | u8 | `fs_profile` | Disc filesystem profile id. |
| 108 | 4 | u32 | `chunk_min` | Bytes. |
| 112 | 4 | u32 | `chunk_avg` | Bytes. |
| 116 | 4 | u32 | `chunk_max` | Bytes. |
| 120 | 4 | u32 | `gear_table_id` | Gear table version. 1 in this specification. |
| 124 | 4 | u32 | `bundle_threshold` | Bytes. |
| 128 | 8 | u64 | `bundle_target` | Bytes. |
| 136 | 8 | u64 | `object_count` | Objects in this run. Equals `record_count` of the manifest. |
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
| 304 | 8 | u64 | `catalog_lba` | LBA of `catalog/CATALOG.bin` in this run (section 12.7.2). |
| 312 | 8 | u64 | `catalog_sectors` | Length of `CATALOG.bin` in sectors. |
| 320 | 32 | u8[32] | `catalog_hash` | Hash of the `CATALOG.bin` bytes. |
| 352 | 32 | u8[32] | `prev_run_header_hash` | Hash of the previous run header on this disc. Zero for the first run. |
| 384 | 8 | i64 | `created_sec` | Burn time, seconds. |
| 392 | 4 | u32 | `created_nsec` | Nanoseconds. |
| 396 | 4 | u32 | `source_run_count` | Number of run seqs this run references. |
| 400 | 8 | u64 | `snapshot_count` | Snapshots whose objects start in this run. |
| 408 | 8 | u64 | `prereq_count` | Prerequisite ids listed in the manifest. |
| 416 | 4 | u32 | `tool_version` | Writer version. |
| 420 | 1 | u8 | `run_kind` | 1 data run, 2 repair run (Phase 2), 3 disc-close parity run (Phase 3). 0 is invalid. Health is never stored here; it lives in the disc directory. |
| 421 | 1 | u8 | `session_start_sector_valid` | 1 when `session_start_sector` is meaningful. |
| 422 | 2 | u16 | `reserved_u16` | Zero. |
| 424 | 8 | u64 | `session_start_sector` | Value passed to `isoinfo -T` for this run under profile 2. Zero under profile 1. |
| 432 | 8 | u64 | `prev_run_header_lba` | LBA of the previous run header on this disc. Zero for the first run. |
| 440 | 8 | u64 | `disc_object_count` | Objects on this disc after this run. Cumulative. |
| 448 | 8 | u64 | `disc_used_sectors` | Sectors used on this disc after this run. Cumulative. |
| 456 | 4 | u32 | `disc_run_index` | Index of this run on this disc. 0 for the first run. Same width and same value as `disc_run_index` in the run table record (section 12.5.2). |
| 460 | 4 | u32 | `reserved_u32b` | Zero. |
| 464 | 8 | u64 | `checksum_lba` | First sector of the checksum column, that is the first data sector of `checksum.bin`. |
| 472 | 8 | u64 | `parity_lba` | First sector of parity column `k+1`, that is the second data sector of `parity/p0232.bin` at the default `k`. |
| 480 | 8 | u64 | `data_span` | Sectors from `lba_base` to the last sector that step 6 of section 10.5 occupies, inclusive, as section 9.3 defines it. `L = ceil(data_span / fec_k)`. |
| 488 | 20 | u8[20] | `reserved` | Zero. |
| 508 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 507. |

The set of referenced run seqs is stored in the manifest container, not in the
header, because its length varies. `source_run_count` bounds it, and section 15
bounds `source_run_count` by the capping knobs.

`column_sectors`, `checksum_lba`, `parity_lba` and `data_span` are the column
geometry. The run header therefore locates the data columns and the first two
column files by itself. The layout table locates every parity column
(section 9.7), and every parity file announces itself with the header copy in
its first sector.

#### 9.6.1 The run chain

Every run header points to the previous run header on the same disc, by hash and
by LBA. The chain is the mutable state of the disc: the list of its runs, the
object count, the used sectors, and the close state all come from it. Health
is not in the chain; it lives in the disc directory of the newest catalog
(section 12.6) and in the local health log.

A reader finds the newest run by listing the directory `/NOAHSARK/runs/` and
taking the highest `<seq>`. That is the only normal path. There is no scanning
and no fixed LBA.

Each run header also names the previous run header by hash and by LBA, so a
reader can walk the chain backwards and confirm that no run is missing.

The run table of section 12.5.2 agrees with the chain by construction: it
holds a record for every run that was burned, and a run that failed its
verify stays in the table with `run_status` 3 rather than disappearing from
it. A run that the chain names and the newest run table does not is a
damaged or substituted disc, not a withdrawal.

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
| One level, `/NOAHSARK/objects/ab/<name>` | 2 per object (`objects`, `objects/ab`) | 1 + min(d, 256) | 2 to 4 | `1 + min(d,256) + 2 to 4` blocks, at most **261 blocks (522 KiB)** |
| Two levels, `/NOAHSARK/objects/ab/cd/<name>` | 3 per object | 1 + 256 + min(d, 65536) | 2 to 4 | at most **65,797 blocks (128.5 MiB)** in the worst case, and about `1 + 2d + 4` in the common case where the new objects touch `d` leaf directories under `d` distinct first-level directories |

The one-level worst case is bounded by the 256 first-level directories. The
two-level worst case is bounded by 65,536 leaf directories, but it is reached
only when an append touches every leaf, which a path-ordered pack never does.

Under profile 2 the overwrite is the whole tree: about 19 MiB at 10,000 objects
and about 107 MiB at 100,000 objects (section 10.3.3).

An overwritten block keeps its LBA. When that LBA lies inside an earlier run's
parity domain, the sector no longer matches the digest that the earlier run's
checksum column recorded. Section 11.8 states the rule: such a sector is
filesystem metadata, it is not covered by an extent record, its digest is not
compared, and the healer counts it as an erasure. The earlier run's parity is
never rewritten.

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
| 44 | 2 | u16 | `shard_bytes` | 2048. |
| 46 | 1 | u8 | `fec_scheme` | FEC scheme registry. |
| 47 | 1 | u8 | `reserved_u8` | Zero. |
| 48 | 8 | u64 | `payload_len` | Total container length, for validation. |
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
| 112 | | | `records` | Sorted by `start_lba` ascending. |

The last sector of the parity domain is `lba_base + fec_k * column_sectors - 1`.
The header repeats the column geometry of the run header, so the layout table
alone is enough to run a repair.

Extent record, 64 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | Object id, or bundle id for a bundle extent. For a fixed-name file, the hash of the file bytes under `hash_algo`. All zero for file roles 1, 5, 10, 11 and 12: the header copies, `layout.bin` itself, `checksum.bin` and the parity files. Their bytes become final only after this table is written (section 10.5); each has its own CRC, and the parity covers them. |
| 32 | 8 | u64 | `start_lba` | Absolute LBA of the first sector. |
| 40 | 8 | u64 | `byte_len` | Bytes of the object header plus stored payload. |
| 48 | 4 | u32 | `sector_count` | Sectors this extent covers. |
| 52 | 4 | u32 | `byte_off` | Byte offset inside the first sector. |
| 56 | 2 | u16 | `extent_index` | 0 for the first extent of an object. |
| 58 | 2 | u16 | `flags` | bit0 last extent, bit1 duplicate for locality, bit2 metadata object, that is a tree, a chunklist or a snapshot object (section 26). |
| 60 | 1 | u8 | `kind` | Object kind registry. 0 for a fixed-name file. |
| 61 | 1 | u8 | `compression` | Compression id. 0 for a fixed-name file. |
| 62 | 1 | u8 | `file_role` | 0 object. 1 `RUN.bin`. 2 `DISC.bin`. 3 `README.txt`. 4 `FORMAT.txt`. 5 `layout.bin`. 6 `manifest.bin`. 7 `filter.bin`. 8 a catalog file. 9 `pad.bin`. 10 `checksum.bin`. 11 a parity file. 12 `RUN2.bin`. |
| 63 | 1 | u8 | `column_index` | For `file_role` 11, the parity column index `k+1 .. 254`. 0 otherwise. |

A fragmented file produces several records with the same `content_id` and
increasing `extent_index`. The record with `flags` bit 0 set is the last one.
A zero-length file, which only `pad.bin` can be (section 10.5), has exactly
one record with `start_lba` 0, `byte_len` 0, `sector_count` 0, `byte_off` 0
and `flags` bit 0 set.

The table covers **every file** of the run, not only the objects: `RUN.bin`,
`DISC.bin`, `README.txt` and `FORMAT.txt` in the first run, `layout.bin`,
`manifest.bin`, `filter.bin`, the catalog files, the objects, `pad.bin`,
`checksum.bin`, the parity files and `RUN2.bin`. Files appear in copy order,
which is LBA order. A parity file's record gives the LBA of its header sector;
its column starts one sector later.

A sector inside the parity domain that no extent record covers is filesystem
metadata or free space. It is protected by the parity like every other sector,
and section 11.8 states how verify treats it.

The writer builds the table by reading the LBA of every file back from the
finished image. The method is profile dependent and is stated in section 10.

#### 9.7.1 Raw-LBA reading (Phase 3)

When the filesystem directory is unreadable, a recovery tool works as follows:

1. Scan the first 64 MiB of the disc for the `"NADS"` magic and verify
   `super_crc32c`. That finds `/NOAHSARK/DISC.bin`, which the writer always
   places directly after `RUN.bin` in the first run.
2. Read the superblock, then read `first_run_lba` to find the first `RUN.bin`.
3. Read the run header, then read `layout.bin` at `layout_lba`.
4. Read every file by its extents. Copy order equals LBA order, so the files
   that were copied first sit first.
5. Follow the run chain forward: each run's `RUN2.bin` is the last file of that
   run, and the next run's `RUN.bin` follows it.

This path needs no filesystem code at all. It is Phase 3, because Phase 1 and
Phase 2 discs are read through the filesystem.

**Raw-LBA reading** means that the tool reads the physical device by LBA,
with no filesystem and no image file between them. Reading an image file by
byte offset, where sector `s` is the bytes `[s * 2048, s * 2048 + 2048)`, is
not raw-LBA reading. Phase 1 `verify --image` reads an image that way,
locates the run header and the layout table in it, and repairs from it
(section 11.8). A raw-append run (section 9.11) and a disc whose filesystem
directory is unreadable in the drive need this Phase 3 path.

### 9.8 Append model

Appending a run is a profile 1 and profile 2 mechanism. Section 10.2.2 is its
normative home: the POW medium mechanism, the two variants of profile 1, and
the LBA re-verification after every append. Profile 0 never appends.

### 9.9 Closing a disc

**A disc is never closed by default.** The config key is `disc.close_policy`.

| Value | Meaning |
|---|---|
| `never` | **Default. Phase 1.** No command closes the disc unless the user runs `close` explicitly. |
| `always` | Phase 1. A disc is sealed at its first and only burn: `spare:none`, `-dvd-compat`, no POW. Equivalent to `pack --close` on every new disc. |
| `when_full` | Phase 2. Profile 1 and 2 only. The run after which the free sectors below `fill_limit_sectors` (section 10.11) cannot hold another run, that is fewer than one catalog copy plus two header sectors plus one stripe of 255 sectors, also closes the disc. |

The `noahsark close` command exists from Phase 2 (section 19.10). It writes a
closing run, which is an append. Under `never` it is never automatic.

**Profile 0 under the default policy.** The disc is formatted for POW with
`spare:min`, one large run is written up to the data budget, `-dvd-compat` is
**not** passed, and the disc is left open. The disc can therefore receive a
profile 1 append later, once Phase 2 exists: a repair run, extra parity, or the
leftover space. No format change is needed at that point.

**Sealing a disc.** `pack --close`, or `disc.close_policy = always`, selects
`spare:none` and `-dvd-compat` instead. There is then no format step at all, no
spare area, no defect management, full capacity, and permanently stable LBAs.
The disc can never be appended. The choice is permanent. It is recorded in the
superblock field `sealed` and in `state_flags` bit 0 of the disc directory, so
a later tool never tries to append to it.

Section 10.1.5 is the normative home of the two profile 0 burn paths, with the
command lines, what each path writes, and the trade-off table. Section 9.9.1
below explains the tail anchors only.

#### 9.9.1 Tail anchors

`mkudffs` places UDF anchors at LBA 256, at `N - 256` and at `N` in the
full-size image. The **used prefix** of the image is LBA 0 up to and including
the last sector of `RUN2.bin`, rounded up to a multiple of 16 sectors.

**Open disc, the default.** The first run writes the used prefix and nothing
else (section 10.1.5). The tail anchors are not on
the disc yet. A disc with only the LBA 256 anchor still mounts, because that
anchor is mandatory in the standard, but three anchors are what every reader
expects on a premastered disc. The tail anchors reach the disc when an append
or `close` writes them (Phase 2). Whether a first burn may also write the tail
region early, with `growisofs -use-the-force-luke=seek:` before the middle is
written, is probe 6 of section 23.6, not a fact. If that probe passes on a
drive, the first burn plan may carry a second write step for the last 512
sectors of the image.

**Sealed disc, `pack --close`.** A sealed disc can never be appended, so its
tail anchors must be written at its only burn. `pack --close` therefore burns
the **full-size image** (section 10.1.5): the used prefix, the unused
middle as zero sectors, and the last 512 sectors with the tail anchors. The
zero region costs burn time and nothing else; `pack` runs only when the disc
is at least `disc.min_fill` full, so that time is bounded. Test 34 of
section 23.2 checks that the plan's single write step covers the full image
and that the image holds three anchors.

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

This subsection is the normative home of what `close` does. Section 19.10
is the command; section 10.2.2 is the append mechanism it uses.

When the user does close a disc with `noahsark close` (Phase 2), the writer
must:

1. Confirm that the superblock is already present. It is never rewritten.
2. Write a final catalog copy inside the closing run.
3. Write the tail anchors if they are not present.
4. Optionally add a disc-wide parity run over all data columns of all runs. The
   config key is `fec.disc_close_parity`. The default is false. Phase 3.
5. Write the closing run with `-dvd-compat`.
6. Verify on a second drive within 24 hours.

A sealed profile 0 disc (section 10.1.5) needs none of this. It was closed at
its only write.

Two manual probes cover the open-disc case: reading an open POW BD-R on Windows
and on macOS, and drive behaviour when a reader reads past the last written
block. Section 23.6 defines them.

### 9.10 Fallbacks

Section 10.12.1 is the normative fallback table. It maps each drive, medium,
kernel and tool condition to the profile and the burner backend to use.

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
   new run, exactly as usual. The LBA extents in the layout table, which the
   run header locates, therefore make every object readable.
4. The disc directory marks the disc `append-raw-only`. A reader finds the raw
   runs through the run header chain. A raw append writes its `RUN.bin` at
   `lba_base + run_sectors` of the previous run, rounded up to a multiple of 16
   sectors, so the previous header determines the next `RUN.bin` LBA. When
   that chain is lost too, the recovery scan of section 9.7.1 finds the raw
   runs.
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
   - the FEC layout. `L = ceil(data_span / k)` (section 11.2), and the whole
     run, parity included, must lie below `fill_limit_sectors`;
   - the reserve and the fill limit. Section 10.11 computes
     `data_budget` and `fill_limit_sectors` from `capacity_forced_sectors`.
5. `noahsark disc list` shows both the reported and the forced capacity.
6. The health report flags any disc whose forced capacity is below the reported
   capacity, so that the operator can see how much medium is deliberately
   unused.

### 9.13 Why true UDF multi-session is not possible with current tools

Profile 1 grows one volume in place instead of adding a UDF session, and
variant 1b needs an image mirror and a block diff. Appendix E holds the
evidence, verified against current source: growisofs cannot merge a UDF
session, mkudffs cannot build a session that references an earlier one, the
kernel cannot write a VAT volume, and xorriso has no UDF writer. Profile 1
uses the one path that remains: a POW-formatted BD-R accepts random sector
writes, so the kernel udf driver can maintain the volume. Profile 2 avoids
UDF entirely and uses the ISO 9660 merge that growisofs already supports.

---

## 10. Disc filesystems and burning

Section 10.1 specifies profile 0, which is the default and the whole Phase 1
disc model. Section 10.2 specifies profile 1, which is Phase 2. Section 10.3
specifies profile 2, which is Phase 3. Sections 10.4 to 10.6 are common to every
profile. Sections 10.7 to 10.14 are about the burn plan, the burner, and
verification.

A reader who only implements Phase 1 needs section 10.1 and sections 10.4
onward. Sections 10.2 and 10.3 are later phases.

Each burn-side topic has exactly one normative home. Every other mention is
a pointer to it.

| Topic | Normative home | Pointers |
|---|---|---|
| Profile 0 burn command lines, open and sealed | 10.1.5 | 9.9, 9.9.1, 10.8, C.3 |
| Append mechanism and LBA re-verification | 10.2.2 | 9.8, 26 |
| Profile 1 variant 1b write command line | 10.2.4 | 10.8, C.3 |
| Profile 2 build and burn command lines | 10.3.4 | 10.2.2, 10.8, C.3 |
| What `close` writes | 9.9.2 | 19.10, 10.7.1 |
| Command templates that `burn --print` renders | 10.8 | 20.5 |
| Burn plan container | 10.7.1 | 3.1, 19.9 |
| Fallback table | 10.12.1 | 9.10 |

### 10.1 Profile 0, `oneshot` (default, Phase 1)

Profile 0 writes **one large run per disc**. There is no append, no
next-writable-address handling, and no block diff. This is the whole Phase 1
disc model, and it is the default.

The filesystem is pure UDF 2.01, built exactly as in section 10.1.2. A
profile 0 disc is always UDF; a profile names exactly one filesystem
(section 9.2), and ISO 9660 belongs to profile 2 only. The superblock records
the revision in `fs_revision`.

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

Only four things in that command line are normative: `--media-type=hd`,
`--blocksize=2048`, `--udfrev=2.01`, the option order with `--utf8` first and
every override after `--media-type`, and the absence of `--spartable`. The
rest is an example: the label comes from `label.template` (section 20.8);
`--uid`, `--gid`, `--mode` and `--bootarea` are the reference
implementation's choices; the `truncate` size is the forced capacity rounded
down to 16 sectors, which section 9.12 requires, by whatever means.

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

Section 5.5 states the fan-out rule and its default. Profile 0 and profile 1
allow a second level; profile 2 does not.

#### 10.1.5 Burn

The burn is one growisofs call. Two variants exist, and this subsection is
their normative home: the command line and what each variant writes. Section
9.9 states the close policy that selects between them, and section 9.9.1
explains the tail anchors. Informative: `-speed=4`
in the command lines below is an example; `burner.speed` and
`burner.speed_mdisc` (section 20.5) set the real value.

**Default: POW-formatted and left open.**

```bash
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=run.bin
```

- `spare:min` formats the blank BD-R for Pseudo-OverWrite with the
  maximum-capacity descriptor, so the spare area is as small as the drive
  allows.
- `-dvd-compat` is **not** passed. The disc stays open.
- The run holds the whole payload up to the data budget.
- `run.bin` is the used prefix of the image, as section 9.9.1 defines it:
  LBA 0 up to the last sector of `RUN2.bin`, rounded up to a multiple of 16
  sectors. The UDF tail anchors at `N - 256` and `N` are part of the full-size
  image that `mkudffs` built, so they reach the disc only when a write covers
  them. On an open disc they are absent until an append or `close` writes
  them, or until a second write step that probe 6 of section 23.6 permits.
  The disc still mounts, because the anchor at LBA 256 is mandatory in the
  standard and is always present. NoahsArk never writes an anchor itself;
  anchors are `mkudffs`'s business.
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
- `run.bin` is the **full-size image**, not the used prefix, so the same call
  writes the used prefix, the zero middle and the tail anchors (section
  9.9.1). Nothing can be written to a sealed disc later, so nothing is left
  for later.
- The choice is permanent. The superblock records it in `sealed`, and the disc
  directory records it in `state_flags` bit 0.
- `disc.close_policy = always` selects this path for every new disc.

Profile 0 writes one run, so the LBA re-verification of section 10.2.2 is not
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

#### 10.1.6 LBA read-back

The primary method parses the UDF File Entry. It is the only exact method. It
works on an unmounted image, needs no root, and gives every extent of a
fragmented file.

- Allocation descriptors hold partition-relative block numbers.
- Add the Partition Descriptor start, which `udfinfo` prints as
  `start=... type=PSPACE`.
- In the measured image PSPACE started at 257 and the first object sat at
  absolute LBA 271, that is partition-relative 14.
- The parser needs read access only. Section 24 names a library.

`filefrag -e -v` is a cross-check only. It needs root, and on the tested kernel
it reported the extent count and the block count correctly but printed an empty
extent table. `udfinfo` gives volume-level layout only, never a per-file LBA.

#### 10.1.7 Known reader issues

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

#### 10.1.8 Placement order

The writer copies files into the mount **one at a time**, single-threaded, in
fill order. Copy order equals physical LBA order. This was measured. Five
1,000,000-byte files copied in order landed at LBA 271, 761, 1251, 1741 and
2231. The stride is a fixed 490 blocks. A 1,000,000-byte file needs 489 data
blocks, and the File Entry takes the 490th.

The writer must never use `cp -r` on a directory. The order would then follow
`readdir`, not the fill order.

### 10.2 Profile 1, `udf201-pow` (Phase 2)

Profile 1 is profile 0 plus append. A profile 1 disc is a profile 0 disc that
received more runs. Everything in section 10.1 applies to it unchanged.

#### 10.2.1 Filesystem

The filesystem, the image build, the limits, the fan-out, the LBA read-back,
the reader issues and the placement order are those of profile 0, sections
10.1.1 to 10.1.8. A profile 1 disc may use two fan-out levels, as a profile 0 disc may
(section 5.5), because a UDF append rewrites only the directories that
changed.

#### 10.2.2 Append mechanism

Both appendable profiles use the same medium mechanism: **Pseudo-OverWrite
growth on a formatted BD-R**. Only the filesystem step differs.

The medium mechanism, which growisofs implements (Appendix E names the
functions):

- growisofs formats any blank BD-R unless `spare:none` is passed, and it
  forces the Format Subtype to SRM+POW.
- growisofs treats a POW-formatted BD-R as rewritable, next to DVD+RW and
  BD-RE.
- An append takes `next_session` from the track's Next Writable Address and
  keeps `prev_session = 0`.
- The man page agrees: "volumes are grown within a single session" on Blu-ray.

The consequence is measurable: `dvd+rw-mediainfo` always reports
`Number of Sessions: 1`. The macOS "only the first session" limitation therefore
never engages.

| Step | Profile 1, variant 1a | Profile 1, variant 1b | Profile 2 |
|---|---|---|---|
| Format | `spare:min` on the first write | `spare:min` on the first write | `spare:min` on the first write |
| Build | `mkudffs`, then a mount of the disc itself | `mkudffs`, then a loop mount of an image mirror | `genisoimage`, run by NoahsArk, writes an image (section 10.3.4) |
| Append | Mount `/dev/sr0` read-write with the kernel udf driver, copy files in fill order (section 10.5) | Copy into the mirror, diff 32 KiB blocks, write each changed run with `growisofs -use-the-force-luke=seek:N,spare:min -Z` (section 10.2.4) | `genisoimage -C -M` builds the session image, then `growisofs -M /dev/sr0=image`, one call (section 10.3.4) |
| Burner used for the append | None. The kernel writes. | growisofs, one call per changed run | growisofs, one call |
| New code needed | None | Image mirror and block diff | The genisoimage driver and the placeholder rewrite |
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

#### 10.2.3 Append, variant 1a: kernel direct write

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

#### 10.2.4 Append, variant 1b: image mirror and block diff

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

#### 10.2.5 Per-append cost

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

### 10.3 Profile 2, `iso9660v1-l4-pow` (Phase 3)

Profile 2 exists for two cases:

1. The UDF append path is unavailable on the host.
2. A user wants genisoimage to manage the directory tree, with no UDF work on
   the NoahsArk side.

Profile 2 must not be used until probe 2 of section 23.6 passes.

#### 10.3.1 Premise

On-disc names belong to NoahsArk. They are 68-character lowercase hex from the
charset `[0-9a-f]`. Long user names, deep user paths, and every POSIX metadata
field live **inside tree objects**, not on the disc. The disc filesystem
therefore needs no Unicode, no POSIX metadata, and no long names.

Rock Ridge and Joliet are forbidden. The premise makes that acceptable.

#### 10.3.2 Standard, level, and limits

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

#### 10.3.3 Fan-out

Profile 2 uses **one** hex fan-out level only: `/NOAHSARK/objects/<ab>/<name>`,
256 directories.

The reason is the append cost. `genisoimage -M` rewrites the entire directory
tree and the path tables into the new session image. Measured, with 1-byte files so that the figure is pure metadata:

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
a floor: two hex levels create up to 65,536 directories, and at 2048 bytes
each that floor alone is 65,536 x 2048 = 134,217,728 bytes, that is 128 MiB per
append, whatever the object count. One level caps the floor at 256 x 2048, that
is 512 KiB. That is why profile 2 uses one level.

At 100,000 objects and one fan-out level, one append costs about 107.8 MB, so
ten appends cost about 1.08 GB, which is **4.3 percent** of a 25 GB disc. At
10,000 objects ten appends cost about 0.8 percent. Under profile 2 the packer
must include the projected append cost in its capacity budget (section 15.6),
and the figure is large enough that it must never be treated as noise.

#### 10.3.4 Build and burn command lines

This subsection is the normative home of the profile 2 command lines.
NoahsArk builds every ISO 9660 image itself with genisoimage and hands the
finished image to growisofs. growisofs never runs genisoimage on NoahsArk's
behalf, because the placeholder rewrite of section 10.5 needs the image bytes
before they are burned.

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
# First write on a blank BD-R. Build the image, then burn it.
# spare:min formats for POW.
genisoimage -iso-level 4 -D -l -allow-limited-size -no-limit-pathtables \
            -sort sortfile -V ARK-0001 -o first.iso /srv/ark/tree
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=first.iso

# Every later append. LAST and NEXT come from growisofs -F on the disc:
# LAST is the start sector of the existing session, 0 on a POW disc;
# NEXT is the next writable address in sectors. The mirror image is the
# disc content so far (section 10.2.4 keeps it).
genisoimage -iso-level 4 -D -l -allow-limited-size -no-limit-pathtables \
            -sort sortfile -V ARK-0001 -C ${LAST},${NEXT} -M mirror.iso \
            -o add.iso /srv/ark/tree
growisofs -speed=4 -use-the-force-luke=spare:min,tty -M /dev/sr0=add.iso

# Final append: close the disc.
growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:min,tty \
          -M /dev/sr0=add.iso
```

Informative: `-speed=4` and `ARK-0001` are examples (section 10.8). M-DISC
uses `-speed=2`. Every other flag is identical.

The image is what the burn plan names (section 10.7.1): step kind 2 builds
`first.iso` or `add.iso` from the tree with the `-sort` file as `aux_path`,
and step kind 1 or 3 writes it. The writer runs genisoimage twice per run:
once to learn the extents, once with the final placeholder bytes (section
10.5). Section 10.3.7 shows the measured `-C` and `-M` sequence.

#### 10.3.5 Placement order

`genisoimage -sort <file>` sets the LBA placement order. The file holds
`<path> <weight>` pairs, one per line. A higher weight is placed closer to the
start of the medium.

The writer emits one line per object, with strictly decreasing weights in the
fill order of section 10.5. The sort file for 100,000 objects is a large text
file and costs nothing at runtime. Unlike the copy-order trick of profile 1,
`-sort` is explicit and order-independent.

#### 10.3.6 LBA read-back

`isoinfo -l` prints the extent of every file. It needs no root and no mount.

```bash
isoinfo -i disc.iso -T "$SESSION_START" -l \
  | sed -n 's/.*\[ *\([0-9]*\) *[0-9]*\] *\([0-9a-f]\{68\}\).*/\2 \1/p'
```

`-T <sector>` selects the session to read. That is exactly what re-reading the
map after an append needs. The run header records the value in
`session_start_sector`.

This is simpler than the profile 1 method, which must parse UDF File Entries.

#### 10.3.7 Measured append test

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

#### 10.3.8 OS readability

| OS | Level 4 long lowercase names | Deep directories with `-D` | Note |
|---|---|---|---|
| Linux 2.6 and newer | **Yes**, measured with 2,500 files | Yes | The `iso9660` driver lowercases only when Rock Ridge is absent and the name is uppercase on disc. |
| Windows 7 to 11 | **Unverified. Blocking.** | Expected yes | CDFS is documented for levels 1 and 2 only. Microsoft has never documented ISO 9660:1999 support. genisoimage itself warns that names above 31 characters "may cause buffer overflows in the OS". Probe 2 of section 23.6 must pass first. |
| macOS 10.5 to 15 | Likely yes | Yes | Apple's `cd9660` has read long ISO names for years. Unverified here. |
| FreeBSD | Yes | Yes | Not a target, but noted: FreeBSD reads a profile 2 disc and cannot read a profile 1 disc. |

If Windows truncates to 31 uppercase characters, profile 2 must not be used. The
correct response is to stay on profile 1, not to add a truncation fallback.

#### 10.3.9 Character sets

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

### 10.4 Files at the volume root

Every byte that NoahsArk writes is an ordinary file. The layout is the same
under every profile.

| Path | Content | Written |
|---|---|---|
| `/NOAHSARK/DISC.bin` | Disc superblock (section 9.5). Immutable. | First run only, directly after `RUN.bin`. |
| `/NOAHSARK/README.txt` | Plain-text explanation of the format for a human (section 10.4.1). | First run only, after `DISC.bin`. |
| `/NOAHSARK/FORMAT.txt` | The byte-layout tables of every structure (section 10.4.2). | First run only, after `README.txt`. |
| `/NOAHSARK/runs/<seq>/RUN.bin` | The run header. | First file of its run. |
| `/NOAHSARK/runs/<seq>/layout.bin` | File order, LBA extents, column geometry, `k`, `m`. | With its run. |
| `/NOAHSARK/runs/<seq>/manifest.bin` | The manifest container, which holds the prerequisite list. | With its run. |
| `/NOAHSARK/runs/<seq>/filter.bin` | The run filter. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/CATALOG.bin` | The catalog container: the list and hash of every catalog file (section 12.7.2). | With its run, first file of the catalog. |
| `/NOAHSARK/runs/<seq>/catalog/filters/<seq>.bin` | Every earlier run's filter. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/manifests/<seq>.bin` | The previous 8 runs' manifests. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/snapobj/<name>` | The complete snapshot object of every snapshot, one file each, or one packed `snapobj.bin`. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/snapshots.bin` | The full snapshot table. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/refs.bin` | The ref table. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/discs.bin` | The disc directory. | With its run. |
| `/NOAHSARK/runs/<seq>/catalog/runs.bin` | The run table (section 12.5.2). | With its run. |
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

`<ab>` is the first two hex digits of the digest. `<name>` is the full
68-character multihash hex.

Every fixed name is short, uses the charset `[A-Za-z0-9._/-]`, and avoids every
Windows reserved name. Both filesystems store these names verbatim.

`README.txt` and `FORMAT.txt` are the files that a human in 2050 opens.
Together they must be complete enough to write a reader from.

#### 10.4.1 Content of README.txt

`README.txt` is the exact text below. It is plain ASCII, with LF line endings
and exactly one LF at the end of the file. No line has a trailing space. The
text is byte-identical on every disc of format major 1, apart from the twelve
substitution slots of the identity block.

A slot is written `{name}` below. The writer replaces the four bytes `{`, the
name and `}` with the value, and writes nothing else in its place. The
substitution rules are:

| Slot | Value |
|---|---|
| `{version_minor}` | The `version_minor` of the superblock, in decimal. |
| `{repo_uuid}`, `{disc_uuid}` | Hyphenated lowercase uuid text. |
| `{disc_seq}` | `disc_seq` in decimal, 0-based. |
| `{label}` | The `label` bytes of the superblock, as they are, with every byte outside 0x20 to 0x7E replaced by `?`. |
| `{media_type}` | The name from the media type registry of section 4.6. |
| `{fs_profile}` | The name from the disc filesystem profile registry of section 4.6. |
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

The README must not depend on the software. A reader who has only the README,
`FORMAT.txt` and a hex editor must be able to extract one file by hand.

#### 10.4.2 Content of FORMAT.txt

`FORMAT.txt` is plain ASCII, LF line endings. It is generated by the tool from
the same table definitions that the encoder uses, so it cannot drift from the
code. It holds, in this order:

1. The ten rules of section 4.1 and the string layout of section 4.5.
2. The registries of section 4.6: hash algorithms, compression, chunker
   profiles, filter types, FEC schemes, object kinds, disc filesystem
   profiles, media types, source types, tree TLV types, manifest chunk ids.
3. The byte-offset table of every structure and record, in this order:
   common object header, bundle header, bundle index entry, bundle trailer,
   chunklist header and entry, tree header, tree entry, TLV record, snapshot
   header and metadata TLV, ref record, disc superblock, run header, layout
   table header and extent record, manifest header, TOC entry and record,
   prerequisite record, bundle table entry, split record, filter container,
   catalog container header and entry, simple table container, snapshot
   table record, run table record, disc directory record, checksum sector
   header.
4. The magic values of Appendix B.1, with their file bytes.
5. The Gear table rule of Appendix A.2, the mask rule of Appendix A.3, and
   the six mask values.
6. The BinaryFuse16 query rule of section 12.2.
7. The CRC-32C parameters of section 4.1 rule 7.

Each table has the columns offset, size, type, name and meaning, exactly as in
this document. A structure that is added in a later version is appended to the
list. A structure is never removed from the list while the major version
holds.

**Generation rule.** `FORMAT.txt` is byte-deterministic: two conforming
writers of the same format version produce the same bytes. The rule is:

1. The file is plain ASCII. Every line ends with one LF, 0x0A. The file ends
   with exactly one LF. No line has a trailing space. There is no byte order
   mark and no CR.
2. The file opens with one line `NoahsArk format major 1 minor <N>`, where
   `<N>` is the `version_minor` of the superblock in decimal, then one blank
   line.
3. Each of the seven parts above opens with a line holding its number, a
   period, a space and its title, then a line of `=` characters of the same
   length, then a blank line. The parts appear in the order above. The
   sections inside part 3 appear in the order the list above gives, and no
   other order is permitted.
4. Each structure inside part 3 opens with a line holding its name exactly as
   this document's heading names it, then a line of `-` characters of the
   same length.
5. A table row is the five columns, in the order offset, size, type, name,
   meaning, separated by one tab byte, 0x09, with no padding and no leading
   or trailing tab. A numeric offset and size are decimal. A size that this
   document writes as an expression is written as that expression with no
   spaces. A name is written without backticks. A meaning is the text of this
   document's cell with every backtick removed and every run of whitespace
   folded to one space.
6. A table is preceded by one header row `offset<TAB>size<TAB>type<TAB>name<TAB>meaning`
   and followed by one blank line.
7. A registry table in part 2 uses the same tab-separated form, with the
   columns that this document's registry table has, in the same order.
8. A magic value in part 4 is one line: the mnemonic, a tab, the four file
   bytes as two lowercase hex digits each separated by single spaces, a tab,
   and the u32 value as `0x` and eight lowercase hex digits.
9. A constant in part 5 and part 7 is one line: the name, a tab, and the
   value as `0x` and lowercase hex for a mask or a polynomial, and decimal
   otherwise.
10. The file is at most 64 KiB. A writer that would exceed that drops nothing;
    it is a defect in the writer, because the content is fixed by the format
    version.

The tool generates the file from the same table definitions that the encoder
uses, so the file cannot drift from the code. Test 1 of section 23.2 compares
it byte for byte with a golden file.

### 10.5 Fill order inside a run

The writer places files in this order under every profile:

1. `RUN.bin`, the run header.
2. In the first run of a disc only: `DISC.bin`, `README.txt`, `FORMAT.txt`.
3. `layout.bin`, `manifest.bin`, `filter.bin`.
4. The catalog files, `CATALOG.bin` first.
5. Snapshot objects, then tree objects and chunklists, contiguous.
6. Bundles and chunks, in path order.
7. `pad.bin`, when the data columns need fill.
8. `checksum.bin`, the checksum column.
9. The parity files, in column order.
10. `RUN2.bin`, the run header copy.

Steps 1 to 7 are the data files. They form the parity domain, together with
every filesystem block and free sector between them. Steps 8 and 9 cover it.
Step 10 sits outside the domain, at the highest LBA of the run, so that a copy
of the header survives damage at either end.

**Where the File Entry blocks sit.** Under profile 0 and profile 1 the
filesystem writes the File Entry of a file directly after that file's data
(section 10.1.8), so an FE block is one more sector of the run at the LBA that
follows the file. The consequence for the span arithmetic is fixed by three
rules:

1. `data_span` ends at the last sector that step 6 occupies. Under profile 0
   and profile 1 that sector is the File Entry block of the last file of step
   6. Under profile 2 there is no per-file FE, so it is the last data sector
   of that file. Steps 7 to 10 are outside `data_span`.
2. The data sectors of `pad.bin`, which is step 7, fill the rest of the
   parity domain exactly. Its own File Entry block is therefore the first
   sector at or after `lba_base + k*L`, outside the domain.
3. The File Entry blocks of `checksum.bin` and of every parity file lie
   outside the domain too, because those files start at or after
   `lba_base + k*L`. They are filesystem metadata, not column sectors, and no
   column file counts its own FE block as part of its column.

A file whose content depends on an LBA, a hash or a size that is known only
after layout is a **placeholder** in the pass that places it: a file of its
final size, filled with zero bytes, and rewritten in place later. Every
placeholder has a known final size, because every such file is fixed-width
and its record count is known before layout.

**Invariants.** These five statements are the requirement. Any build
procedure that meets them conforms.

1. The extent of every file is fixed, and read back from the image, before
   any hash that names that file is computed. No file moves after its extent
   is read back.
2. `pad.bin` always exists. Its data sectors fill the parity domain exactly
   to `lba_base + k*L`, so its length in sectors is `k*L - data_span`, and it
   has zero length when that count is 0. A zero-length `pad.bin` still has
   one layout record (section 9.7).
3. The first data sector of `checksum.bin` is at or above `lba_base + k*L`,
   and every column file is contiguous.
4. `manifest.bin`, `DISC.bin`, `catalog/runs.bin`, `catalog/discs.bin`,
   `catalog/CATALOG.bin`, `layout.bin` and the run header become final in
   that order, each from values that are already final.
5. The checksum column and the parity are computed over the finished domain,
   which holds the final bytes of `RUN.bin` and of every other file in it.

The passes below are informative. They are the reference implementation's
way to meet the invariants.

1. Lay out steps 1 to 6. These files are placeholders: `RUN.bin`;
   `DISC.bin` in the first run; `layout.bin`; `manifest.bin`;
   `catalog/CATALOG.bin`; `catalog/runs.bin`; `catalog/discs.bin`. Every
   other file of steps 1 to 6 is written with its final bytes: `README.txt`,
   `FORMAT.txt`, `filter.bin`, every other catalog file, and every object.
   Read the extents back. `layout.bin` is sized for one extent record per
   file; if the read-back shows a fragmented file, repeat this pass with the
   larger record count.
2. Compute `data_span` as `end6 - lba_base + 1`, where `end6` is the last
   sector that step 6 occupies: the File Entry block of the last file of step
   6 under profile 0 and profile 1, and its last data sector under profile 2.
   Then `L = ceil(data_span / k)`.
3. Write `pad.bin` with `k*L - data_span` sectors of zero bytes, which places
   its last data sector at `lba_base + k*L - 1`. When that count is 0, write
   it with zero length. Its File Entry block then falls at or after
   `lba_base + k*L`, outside the domain.
4. Lay out steps 8 to 10 as placeholders: `checksum.bin` (`L` sectors), the
   parity files (`L + 1` sectors each), and `RUN2.bin`. Read the extents
   back. Every extent of the run is now known, and so are `run_sectors`,
   `checksum_lba` and `parity_lba`.
5. Check that the first data sector of `checksum.bin` is at or above
   `lba_base + k*L`, and that every column file is contiguous. A failure is a
   defect in the writer, not a recoverable condition.
6. Make the placeholders final, in this order. Each step needs only values
   that an earlier step made final:
   1. `manifest.bin`: it needs the object LBAs. Compute `manifest_hash`.
      `filter_hash` may be computed at any time, because `filter.bin` was
      final in pass 1.
   2. `DISC.bin`, first run only: it needs `first_run_lba`.
   3. `catalog/runs.bin` and `catalog/discs.bin`: they need `lba_base`,
      `run_sectors` and the used sectors of this disc after this run. The run
      table record of this run carries a zero `run_header_hash`
      (section 12.5.2).
   4. `catalog/CATALOG.bin`: it needs the hash of every catalog file.
      Compute `catalog_hash`.
   5. `layout.bin`: it needs every extent and the hash of every fixed-name
      file that section 9.7 does not zero. Compute `layout_hash`.
   6. The run header: it needs the column geometry and the four hashes
      above. Write it into `RUN.bin`, into `RUN2.bin` and into sector 0 of
      every parity file.
   7. The checksum column and the parity, over the finished domain, which
      now holds the final `RUN.bin`. Write them into `checksum.bin` and the
      parity files.

An in-place write changes the bytes of a file whose extents are already fixed,
so it never moves a file. Under profile 0 and profile 1 the image is a
loop-mounted file, and the in-place write is an ordinary write to the mounted
file. Under profile 2 NoahsArk runs `genisoimage` itself (section 10.3.4):
once over the tree with placeholders, to learn the extents from `isoinfo -l`,
then again over the tree with the final bytes. genisoimage is deterministic
for an unchanged tree shape and unchanged file sizes, so the second image has
the same extents; the writer checks that and treats a difference as a defect.

Under profile 0 and profile 1 the order is expressed as the copy order into
the mount. Under profile 2 it is expressed as `-sort` weights.

Metadata objects, that is trees, chunklists and snapshot objects, are placed
together on purpose. A connectivity check over one run then costs one seek
and one sequential read.

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

`pack` writes the burn plan as `burn.bin` and a human rendering as
`burn.json`. The binary file is authoritative. Informative: the default place
is `staging/plans/<run_seq>/`, as section 14.1 lists it.

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
| 6 | 1 | u8 | `step_kind` | 1 write image with `-Z`, 2 build an ISO 9660 image from a tree with genisoimage (profile 2), 3 write image with `-M` (profile 2 append), 4 mount read-write, 5 copy tree into the mount, 6 unmount, 7 close, 8 eject, 9 reload. |
| 7 | 1 | u8 | `flags` | bit0 add `-dvd-compat`, bit1 first write of the disc, bit2 metadata patch, bit3 the step needs an arbitrary seek. |
| 8 | 8 | u64 | `seek_lba` | Target LBA. Must be a multiple of 16. 0 when the step does not seek. |
| 16 | 8 | u64 | `byte_len` | Bytes to write. Must be a multiple of 32768 when the step seeks. |
| 24 | 32 | u8[32] | `payload_hash` | Hash of the image file for kinds 1 and 3, or of the sorted tree listing for kinds 2 and 5. The listing serialization is defined below. |
| 56 | 4 | u32 | `source_len` | Byte length of `source_path`. |
| 60 | 256 | u8[256] | `source_path` | Image file, or the directory tree to copy or build from. UTF-8, zero-padded. At most 256 bytes; see rule 7. |
| 316 | 4 | u32 | `aux_len` | Byte length of `aux_path`. |
| 320 | 184 | u8[184] | `aux_path` | For kind 2, the `-sort` file; for kind 3, the mirror image given to `-M`; for kind 4, the mount point. Empty otherwise. At most 184 bytes; see rule 7. |
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
   `pack --close` or `disc.close_policy = always` produced for the first and
   only write of a profile 0 disc (section 10.1.5), by a plan that
   `noahsark close` produced (Phase 2), or by `pack` under
   `disc.close_policy = when_full` (Phase 2). Under the default policy,
   `never`, no plan ever carries them.
7. A `source_path` above 256 bytes or an `aux_path` above 184 bytes is an
   error. The plan writer refuses to write the plan, `pack` exits with code
   2, and the message names the path and the limit. The path is never
   truncated and never stored in a side file. Informative: a short
   `staging.dir` keeps every plan path far below the limit.

**The sorted tree listing.** Step kinds 2 and 5 name a directory tree instead
of a file, so `payload_hash` covers a serialization of that tree. The
serialization is normative, because `burn --exec` must reproduce it byte for
byte to confirm the hash.

One line per regular file below `source_path`, and no line for anything else:
no directory, no symlink, no device node, no empty line and no header. A line
is, in order:

1. the path of the file relative to `source_path`, as raw bytes, with `/` as
   the separator and no leading `/`;
2. one tab byte, 0x09;
3. the file size in bytes, in decimal ASCII, with no sign, no padding and no
   separator;
4. one tab byte;
5. the content id of the file bytes in the text form of section 5.4, that is
   68 lowercase hex characters under `hash.current`;
6. one line feed byte, 0x0A.

Lines are sorted ascending by the relative path bytes, unsigned, as section
8.10 defines ascending. The listing ends with the line feed of its last line
and has no trailing blank line. An empty tree serializes to zero bytes.
`payload_hash` is the hash of those bytes under `hash.current`.

#### 10.7.2 JSON rendering

`burn.json` is a convenience for a human and for a script. It is derived from
`burn.bin` and is never authoritative. Its fields are:

| Field | Type | Meaning |
|---|---|---|
| `format` | string | Always `noahsark-burn-plan`. |
| `version` | integer | 1. |
| `repo_uuid`, `disc_uuid` | string | Hyphenated lowercase uuid text. `disc_uuid` is all zero for a blank disc. |
| `disc_seq`, `run_seq` | integer | As in the container. |
| `media_type` | string | Media type registry name. |
| `fs_profile` | string | Profile registry name. |
| `append_variant` | string | `1a`, `1b`, or absent. |
| `burner_backend` | string | `growisofs`, `cdrskin`, `imgburn`, `imapi`, `hdiutil`, `kernel`. |
| `device_hint` | string | As in the container. |
| `speed` | integer | As in the container. |
| `close_disc` | boolean | As in the container. |
| `expected_nwa_sectors`, `capacity_sectors` | integer | As in the container. |
| `steps` | array of objects | One per step record, in order. |
| `steps[].index` | integer | `step_index`. |
| `steps[].kind` | string | `write_image`, `build_image`, `write_image_append`, `mount`, `copy`, `unmount`, `close`, `eject`, `reload`, for kinds 1 to 9. |
| `steps[].seek_lba`, `steps[].byte_len` | integer | Present for kinds 1 and 3. |
| `steps[].source_path`, `steps[].aux_path` | string | Present when non-empty. |
| `steps[].payload_hash` | string | Multihash text form. Present for kinds 1, 2, 3 and 5. |
| `steps[].dvd_compat` | boolean | `flags` bit 0. Present for kinds 1, 3 and 7. |

Informative example:

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

`burn --print` renders each step into a command line. What the rendered
command must do, and the set of values it must be able to use, are normative.
**The template mechanism and its syntax are informative.** The reference
implementation uses Go `text/template` and stores the templates in the config
file under `burner.template.<backend>.<kind>`, where a user may override any
of them; another implementation may render the same commands by any means and
still conform.

Normative: `burn --print` must render, for each backend and step kind, a
command line that performs exactly the action of section 10.1.5, 10.2.4 or
10.3.4 for that step, with these values and no others taken from the burn
plan and the config.

| Variable | Source | Used by |
|---|---|---|
| Device | `device_hint`, or `--device` | every write, mount and close step |
| Speed | `speed` in the plan, from `burner.speed` or `burner.speed_mdisc` | every write step |
| Spare mode | `spare:min`, or `spare:none` on the sealed first write of a profile 0 disc | every write step |
| dvd-compat | `flags` bit 0 of the step | write and close steps |
| Seek LBA | `seek_lba`, a multiple of 16 | a seeking write |
| Source path | `source_path` | write, build and copy steps |
| Auxiliary path | `aux_path`: the `-sort` file, the mirror image, or the mount point | build, append and mount steps |
| Label | `label.template` | a build step |
| Last session | the session start the plan records | a profile 2 append build |
| Output image | the image the build step produces | a profile 2 build |

A rendered command must carry no value that this table does not name, and
must never carry a value that the plan does not hold.

The table below is the reference implementation's template set. It is
**informative**: it shows one correct rendering, in Go `text/template` syntax.

| Backend | OS | Step | Template |
|---|---|---|---|
| `growisofs` | Linux | first write, profile 1 and 0 | `growisofs -speed={{.Speed}} -use-the-force-luke={{.SpareMode}},tty {{if .DvdCompat}}-dvd-compat {{end}}-Z {{.Device}}={{.SourcePath}}` |
| `growisofs` | Linux | seeking write, profile 1 variant 1b | `growisofs -speed={{.Speed}} -use-the-force-luke=seek:{{.SeekLBA}},spare:min,tty {{if .DvdCompat}}-dvd-compat {{end}}-Z {{.Device}}={{.SourcePath}}` |
| `kernel` | Linux | mount, copy, unmount, profile 1 variant 1a | `mount -t udf -o rw {{.Device}} {{.AuxPath}}` then one `cp` per object in fill order, then `umount {{.AuxPath}}` |
| `genisoimage` | Linux | build image, profile 2 (kind 2) | `genisoimage -iso-level 4 -D -l -allow-limited-size -no-limit-pathtables -sort {{.AuxPath}} -V {{.Label}} {{if .Append}}-C {{.LastSession}},{{.SeekLBA}} -M {{.MirrorImage}} {{end}}-o {{.OutputImage}} {{.SourcePath}}` |
| `growisofs` | Linux | first write, profile 2 (kind 1) | The profile 0 first-write template above. |
| `growisofs` | Linux | append, profile 2 (kind 3) | `growisofs -speed={{.Speed}} -use-the-force-luke=spare:min,tty {{if .DvdCompat}}-dvd-compat {{end}}-M {{.Device}}={{.SourcePath}}` |
| `cdrskin` | Linux | write image | `cdrskin dev={{.Device}} speed={{.Speed}} -multi -tao {{.SourcePath}}` |
| `imgburn` | Windows | write image | `ImgBurn.exe /MODE WRITE /SRC "{{.SourcePath}}" /DEST {{.Device}} /SPEED {{.Speed}} /START /CLOSE` |
| `hdiutil` | macOS | write image | `hdiutil burn -device {{.Device}} -speed {{.Speed}} {{.SourcePath}}` |
| `dvd+rw-tools` | macOS | any | The Linux growisofs templates with the platform device name. |

`{{.SpareMode}}` is `spare:min` under every profile by default. It is
`spare:none` only when the step has `close_disc` set on the first write of a
profile 0 disc, which is the sealed path of section 10.1.5. Informative:
`{{.Speed}}` comes from `burner.speed` or `burner.speed_mdisc`, and
`{{.Label}}` from `label.template`; the literal `-speed=4` and
`NOAHSARK-0001` values elsewhere in this document are examples.

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
  of 32768. Appendix E states where growisofs enforces it.
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
| Mini BD SL 8 cm (media type 11) | 3,804,288 | 7,791,181,824 | 7.26 | 1 |
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
- Section 10.11 turns the forced capacity into `data_budget` and
  `fill_limit_sectors`. Those two names are the only capacity figures that
  the packer, the superblock and the close policy use.
- `disc.min_spare_ratio` defaults to 0.20. When the remaining POW spare area
  falls below that fraction, the tool warns and recommends no further appends.
- `disc.spare_reserve_bytes` defaults to 512 MiB on every disc that is
  formatted with `spare:min`, which is every disc except a sealed one. POW
  defect management and filesystem metadata need it.
- Under profile 2 the packer must also reserve the projected directory rewrite
  cost of the remaining appends (section 10.3.3).
- One growisofs command line covers every media size. There is no layer logic
  and no hardcoded sector count in growisofs; it takes the capacity from the
  drive (Appendix E). BDXL needs a BDXL-capable drive and nothing else.

### 10.11 Reserved space

Three names carry the whole capacity policy, and nowhere else in this document
is a second formula given for them:

- **`data_budget`**: the sectors that object data and the runs' own tables may
  occupy on the disc, across all its runs. The packer fills up to it
  (section 15.6).
- **`fill_limit_sectors`**: the first LBA that no run may reach. The
  superblock records it (section 9.5).
- **`reserve`**: `capacity_forced - data_budget`, the part of the disc that
  data must not use.

#### 10.11.1 Invariants

These five statements are normative. Any writer that holds them conforms,
whatever arithmetic it used to choose the numbers.

1. No run, its parity and its header copies included, reaches
   `fill_limit_sectors`.
2. `fill_limit_sectors` is at most `capacity_forced`, and it is defined as
   `capacity_forced - safety_margin - spare_area`, where `safety_margin` is
   `ceil(capacity_forced * (1 - disc.fill_ratio))` and `spare_area` is
   `ceil(disc.spare_reserve_bytes / 2048)` on a `spare:min` disc and 0 on a
   sealed disc. That is the definition, and it holds whether or not an
   override is set.
3. `data_budget` is a whole number of stripes of `k` data sectors:
   `data_budget mod k == 0`.
4. `reserve` equals `capacity_forced - data_budget` by definition. The
   superblock records the values the writer actually used:
   `reserve_computed_sectors` is what the estimator of section 10.11.2
   produced, `reserve_forced_sectors` and `reserve_extra_sectors` are the
   overrides as given, and `fill_limit_sectors` is the value of rule 2.
   A reader takes `fill_limit_sectors` from the superblock and never
   recomputes it.
5. The sum of the sectors that every run of the disc occupies, plus the
   sectors that the disc still holds free below `fill_limit_sectors`, never
   exceeds `fill_limit_sectors`. A later append reads the recorded values and
   never derives new ones.

`data_budget` and `reserve` are a writer's choice under those invariants. The
inputs of the estimator below, `disc.expected_runs`, `alignment_padding`,
`catalog.table_reserve_bytes` and `manifest.history_depth`, are heuristics.
They are not part of the on-disc contract, and a reader never uses them.

#### 10.11.2 The reference estimator

This subsection is the **reference estimator**. A writer should use it. A
writer that uses another estimator must still hold every invariant of section
10.11.1, and must record what it used.

All arithmetic is in sectors. A byte value from the configuration is
converted with `ceil(bytes / 2048)`.

```
fixed_terms =
      safety_margin                     # ceil(capacity_forced * (1 - disc.fill_ratio))
    + superblock_and_headers            # DISC.bin, README.txt, FORMAT.txt,
                                        #   plus RUN.bin and RUN2.bin per run
    + catalog_growth                    # catalog copies for the expected
                                        #   number of future runs
    + spare_area                        # POW spare, on every spare:min disc
    + alignment_padding                 # up to 32 KiB per write
    + parity_headers                    # m, one header sector per parity file

fec_region        = capacity_forced - fixed_terms
stripes           = floor(fec_region / 255)
data_budget       = stripes * k
fec_overhead      = fec_region - data_budget               # checksum, parity,
                                                           #   partial stripe
reserve_computed  = fixed_terms + fec_overhead
reserve           = max(reserve_computed, reserve_forced) + reserve_extra
data_budget       = capacity_forced - reserve              # the same value
                                                           #   when no override
```

Terms:

| Term | Formula | Default input |
|---|---|---|
| `safety_margin` | `ceil(capacity_forced * (1 - disc.fill_ratio))` | `disc.fill_ratio` = 0.95 |
| `superblock_and_headers` | `1 + ceil(readme_bytes / 2048) + ceil(format_bytes / 2048) + expected_runs * 2` | See below. |
| `catalog_growth` | `expected_runs * ceil(catalog_bytes_per_run / 2048)`, see below | `disc.expected_runs` |
| `spare_area` | `ceil(disc.spare_reserve_bytes / 2048)` on a `spare:min` disc, 0 on a sealed disc (`spare:none`) | 512 MiB |
| `alignment_padding` | `expected_runs * 16` | - |
| `parity_headers` | `m` | `m` = 23 |
| `fec_overhead` | `fec_region - data_budget`, which is `stripes * (m + 1) + (fec_region - stripes * 255)` | The two parts are the checksum and parity sectors of every whole stripe, and the fewer than 255 sectors of an incomplete stripe, which stay unused. |

`superblock_and_headers` counts one sector for `DISC.bin`, the sectors of
`README.txt` and `FORMAT.txt`, and two sectors per expected run for `RUN.bin`
and `RUN2.bin`. The other `m` header copies sit in the first sector of each
parity file, and `parity_headers` counts those. `readme_bytes` and
`format_bytes` are the actual sizes of the two files the writer is about to
write. Both have a normative cap: `README.txt` is at most 16 KiB (section
10.4.1) and `FORMAT.txt` is at most 64 KiB (section 10.4.2), so the term is
never above `1 + 8 + 32 + expected_runs * 2`. The worked examples below use
the caps.

`disc.expected_runs` defaults by profile, because the profiles differ in how
many runs a disc can receive:

| Profile | Default `expected_runs` | Reason |
|---:|---:|---|
| 0, `oneshot` | **1** | A profile 0 disc holds exactly one run. Reserving for 32 would waste about 0.5 percent of the disc for runs that can never exist. |
| 1, `udf201-pow` | 32 | The disc receives appends. |
| 2, `iso9660v1-l4-pow` | 32 | The disc receives appends. |

`catalog_growth` is exact integer arithmetic over four named inputs. Every
input is an integer number of bytes.

```
objects_per_run       = ceil(capacity_forced * 2048 / chunk_avg)
filter_bytes          = 84 + ceil(objects_per_run * 91 / 40)
manifest_bytes        = 64 * objects_per_run + 4096
table_bytes           = catalog.table_reserve_bytes
catalog_bytes_per_run = filter_bytes
                      + manifest.history_depth * manifest_bytes
                      + table_bytes
catalog_growth        = expected_runs * ceil(catalog_bytes_per_run / 2048)
```

`chunk_avg` is the average chunk size of the chunker profile (section 6.3).
`filter_bytes` is the 80-byte filter container header, the 4-byte body CRC,
and 18.2 bits per key written as `91 / 40` bytes per key (section 12.2); it is
a planning estimate, because the real body size comes from the sizing rule of
section 12.2 and is a little larger. `manifest_bytes` is one 64-byte record
per object plus 4096 bytes for the header, the TOC, the fan-out and the small
chunks (section 12.3). `table_bytes` is a planning reserve for the snapshot
objects and the four tables of one catalog copy;
`catalog.table_reserve_bytes` (section 20.7) defaults to 614,400. Under
profile 2 the projected directory rewrite cost of section 10.3.3 is added to
`catalog_bytes_per_run`. `objects_per_run` is a planning figure only; the
packer never limits a run by it.

The packer solves the budget in one pass: it subtracts every fixed term from
the forced capacity, splits what is left into whole stripes, and gives `k` of
every 255 sectors to data. `data_budget` is therefore always a whole number of
stripes of `k` data sectors, which is invariant 3.

**Consequence, with no override.** When neither override is set,

```
fill_limit_sectors = data_budget + superblock_and_headers + catalog_growth
                   + fec_overhead + alignment_padding + parity_headers
```

That is a consequence of the estimator, not a second definition. Invariant 2
is the definition. With `disc.force_reserve` or `disc.extra_reserve` in force
the two sides differ, because the override moves `data_budget` without moving
`safety_margin` or `spare_area`; the definition then wins, and the estimator's
sum is smaller.

Overrides:

- `disc.force_reserve` replaces the computed reserve. It accepts bytes or a
  percentage of the forced capacity.
- `disc.extra_reserve` is added to the computed reserve. It accepts the same
  forms.
- Both are recorded in the superblock next to the computed value, so a later
  append uses the same budget. With an override in force, the packer rounds
  `data_budget` down to a whole number of stripes of `k` data sectors, so
  invariant 3 still holds.

#### 10.11.3 Worked example, BD-R SL 25 GB

Inputs: capacity 12,219,392 sectors (25,025,314,816 bytes), no forced capacity,
**profile 1** with `expected_runs` 32, `fill_ratio` 0.95, `m` 23, a `spare:min`
disc, P4 chunking (`chunk_avg` 4,194,304), `manifest.history_depth` 8,
`catalog.table_reserve_bytes` 614,400, `README.txt` at its 16 KiB cap and
`FORMAT.txt` at its 64 KiB cap.

`catalog_growth`: `objects_per_run` = ceil(25,025,314,816 / 4,194,304) =
5,967; `filter_bytes` = 84 + ceil(5,967 x 91 / 40) = 13,659;
`manifest_bytes` = 64 x 5,967 + 4,096 = 385,984; `catalog_bytes_per_run` =
13,659 + 8 x 385,984 + 614,400 = 3,715,931; ceil(3,715,931 / 2048) = 1,815
sectors per copy; 32 x 1,815 = 58,080.

| Term | Sectors | Bytes |
|---|---:|---:|
| `safety_margin` = ceil(12,219,392 x 0.05) | 610,970 | 1.25 GB |
| `superblock_and_headers` = 1 + 8 + 32 + 32 x 2 | 105 | 215 kB |
| `catalog_growth` = 32 x 1,815 | 58,080 | 118.9 MB |
| `spare_area` = 536,870,912 / 2048 | 262,144 | 536.9 MB |
| `alignment_padding` = 32 x 16 | 512 | 1.0 MB |
| `parity_headers` = `m` | 23 | 47 kB |
| `fixed_terms` | 931,834 | |
| `fec_region` = 12,219,392 - 931,834 | 11,287,558 | |
| `stripes` = floor(11,287,558 / 255) | 44,264 | |
| `fec_overhead` = 44,264 x 24 + 238 | 1,062,574 | 2.18 GB |
| **reserve_computed** = 931,834 + 1,062,574 | **1,994,408** | **4.08 GB** |
| **data_budget** = 44,264 x 231 | **10,224,984** | **20.94 GB** |
| **fill_limit_sectors** | **11,346,278** | **23.24 GB** |

The FEC region holds 44,264 whole stripes: 10,224,984 data sectors and
1,062,336 checksum and parity sectors, plus 238 sectors of an incomplete
stripe that stay unused. The 23 parity header sectors are in `fixed_terms`.
`fill_limit_sectors` is `12,219,392 - 610,970 - 262,144`, and it also equals
`10,224,984 + 105 + 58,080 + 1,062,574 + 512 + 23`. `reserve_computed` equals
`12,219,392 - 10,224,984`.

**The same disc under profile 0**, where `expected_runs` is 1:
`superblock_and_headers` = 1 + 8 + 32 + 2 = 43, `catalog_growth` = 1,815,
`alignment_padding` = 16, `fixed_terms` = 875,011, `fec_region` =
11,344,381, `stripes` = 44,487, `fec_overhead` = 1,067,884,
**reserve_computed** = 1,942,895 and **data_budget** = 10,276,497 sectors
(21.05 GB). `fill_limit_sectors` is unchanged at 11,346,278, because it does
not depend on `expected_runs`. Profile 0 therefore gives 51,513 more data
sectors, that is 105 MB, than profile 1 on the same medium.

#### 10.11.4 Worked example, BD-R XL TL 100 GB

Inputs: capacity 48,878,592 sectors (100,103,356,416 bytes), no forced capacity,
profile 1, same knobs.

`catalog_growth`: `objects_per_run` = ceil(100,103,356,416 / 4,194,304) =
23,867; `filter_bytes` = 84 + ceil(23,867 x 91 / 40) = 54,382;
`manifest_bytes` = 64 x 23,867 + 4,096 = 1,531,584; `catalog_bytes_per_run`
= 54,382 + 8 x 1,531,584 + 614,400 = 12,921,454; ceil(12,921,454 / 2048) =
6,310 sectors per copy; 32 x 6,310 = 201,920.

| Term | Sectors | Bytes |
|---|---:|---:|
| `safety_margin` = ceil(48,878,592 x 0.05) | 2,443,930 | 5.01 GB |
| `superblock_and_headers` | 105 | 215 kB |
| `catalog_growth` = 32 x 6,310 | 201,920 | 413.5 MB |
| `spare_area` | 262,144 | 536.9 MB |
| `alignment_padding` | 512 | 1.0 MB |
| `parity_headers` = `m` | 23 | 47 kB |
| `fixed_terms` | 2,908,634 | |
| `fec_region` = 48,878,592 - 2,908,634 | 45,969,958 | |
| `stripes` = floor(45,969,958 / 255) | 180,274 | |
| `fec_overhead` = 180,274 x 24 + 88 | 4,326,664 | 8.86 GB |
| **reserve_computed** = 2,908,634 + 4,326,664 | **7,235,298** | **14.82 GB** |
| **data_budget** = 180,274 x 231 | **41,643,294** | **85.29 GB** |
| **fill_limit_sectors** | **46,172,518** | **94.56 GB** |

`fill_limit_sectors` is `48,878,592 - 2,443,930 - 262,144`, and it also
equals `41,643,294 + 105 + 201,920 + 4,326,664 + 512 + 23`.
`reserve_computed` equals `48,878,592 - 41,643,294`.

`pack --dry-run` prints exactly these numbers before it commits to a run:

```
$ noahsark pack --dry-run --disc 12
disc              b21c7f90  seq 12  BD-R SL 25  profile udf201-pow (1b)
capacity reported 12,219,392 sectors   25,025,314,816 B
capacity forced   12,219,392 sectors   25,025,314,816 B
reserve computed   1,994,408 sectors    4,084,547,584 B
reserve forced             -                        -
reserve extra              -                        -
data budget       10,224,984 sectors   20,940,767,232 B
fill limit        11,346,278 sectors   23,237,177,344 B
data used so far   4,096,512 sectors    8,389,656,576 B
free for this run  6,128,472 sectors   12,551,110,656 B
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
is broken for BD-R in two ways, and both fixes are distribution patches:
without them a POW-capable drive under-reports BD-R capacity, and a blank
BD-R fails to close. Appendix E names the patches and the bug reports.

The tool must check the burner version at startup. `burn --print` must warn when
the version is unknown or unpatched. `burn --exec` must refuse.

xorriso is not used. libisofs has no UDF writer, and its ISO 9660 multi-session
support duplicates what growisofs already does.

#### 10.12.1 Fallbacks

| Condition | Fallback |
|---|---|
| The drive offers no POW feature (`GET CONFIGURATION` feature 0x38 absent). | Use profile 0 sealed: `spare:none`, one run, `-dvd-compat` (section 10.1.5). |
| `dvd+rw-mediainfo` reports `BD-R SRM` after a format attempt. | Same. POW is not available on this drive and medium. |
| The kernel refuses to mount `/dev/sr0` read-write on a POW BD-R (probe 1, section 23.6). | Use profile 1 variant 1b, the image mirror and the block diff. |
| The UDF append path is unavailable, or a user wants genisoimage-managed appends. | Use profile 2, after the Windows name check of probe 2 passes. |
| Windows truncates ISO 9660:1999 long names on real media (probe 2, section 23.6). | Profile 2 must not be used. Stay on profile 1. |
| growisofs fails, or the build is unpatched. | Use the `cdrskin` burner backend, and therefore profile 0 sealed. |
| An object moved after an append. | Abort the append. Keep the objects PACKED. Mark the run for re-burn. Report the moved ids. |
| The image mirror is lost (variant 1b). | Rebuild it with `ddrescue` from the disc. |
| A burn fails, or a verify fails. | The `run_seq` of the failed run is never reused. The re-burn is a new plan with the next `run_seq`; the old number is a hole in the run table (section 12.5.2). |

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

This section is informative. It gives the reasoning behind section 11.2.

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

Informative: the column-and-stripe layout is similar to dvdisaster RS03; the
code itself is defined here and nowhere else.

- The FEC scheme is `rs255-gf8`: a systematic erasure code over GF(2^8)
  with `k` information shards and `m` parity shards per stripe. Section
  11.2.1 defines the arithmetic and the generator matrix exactly. Any encoder
  that produces the same parity bytes for the same information bytes
  conforms.
- A **shard** is exactly one 2048-byte sector.
- A **stripe** is 255 shards: `k` data + 1 checksum + `m` parity. The code
  covers the `k` data shards and the `m` parity shards. The checksum shard is
  outside the code (section 11.4).
- Format version 1 **fixes** `k = 231` and `m = 23`, which is
  `k + 1 + m = 255` and about 10 percent parity relative to payload. A
  version 1 writer writes no other pair, and a version 1 reader refuses any
  other pair. The run header and the layout table still record both values,
  so that a later version can change them under a new `version_major`.
- `lba_base` is the first sector of the parity domain: LBA 0 for the first
  run of a disc, and the first sector of `RUN.bin` for every later run
  (section 9.3). `data_span` is the number of sectors from `lba_base` to the
  last sector that **step 6** of section 10.5 occupies, inclusive. Under
  profile 0 and profile 1 that last sector is the File Entry block of the last
  file of step 6, because a File Entry follows its file data (section 10.1.8);
  under profile 2 it is the last data sector of that file. Step 7, `pad.bin`,
  is not part of `data_span`: its data sectors fill the domain from the end of
  `data_span` to `lba_base + k*L`, so its length is `k*L - data_span`. Making
  `pad.bin` part of `data_span` would be circular, because its own length
  follows from `L`. The File Entry block of `pad.bin` lies at or after
  `lba_base + k*L`, outside the domain, and so do the File Entry blocks of
  `checksum.bin` and of every parity file; those blocks are filesystem
  metadata that the layout table makes unnecessary for a reader.
- The column length is `L = ceil(data_span / k)`.
- Data column `c`, for `c = 0 .. k-1`, occupies LBA
  `[lba_base + c*L, lba_base + (c+1)*L)`.
- The **parity domain** is the contiguous LBA range of the `k` data columns,
  `[lba_base, lba_base + k*L)`. Filesystem metadata and free sectors that lie
  inside that range are protected too, which is the whole reason to define
  the domain by LBA and not by file. The writer fills the tail of the domain
  with `pad.bin` (section 10.5), so the domain never overlaps a column file.
- Column `k`, the **checksum column**, is the `L` sectors of the file
  `runs/<seq>/checksum.bin`. Its first sector is `checksum_lba`, which is at
  or above `lba_base + k*L`.
- Column `c`, for `c = k+1 .. 254`, is sectors 1 to `L` of the file
  `runs/<seq>/parity/pNNNN.bin`, where `NNNN` is `c` in decimal, zero-padded
  to four digits. Sector 0 of that file is a run header copy. The file is
  `L + 1` sectors. `parity_lba` is the first column sector of column `k+1`.
- Every column file is contiguous. The layout table records the extent of
  each one, and the run header records `checksum_lba` and `parity_lba`.
- Stripe `i`, for `i = 0 .. L-1`, is sector `i` of every column.
- Encoding is byte-column-wise. Take one byte from each of the `k` data
  sectors of the stripe, at the same byte offset, in column order 0 to
  `k - 1`. Produce `m` parity bytes at that byte offset in the `m` parity
  sectors, column `k + 1` first. Repeat for all 2048 byte offsets.

```
  LBA ->  lba_base                                 lba_base + k*L
          |----------|----------|-----  ...  -----|          [checksum.bin] [p0232.bin] ... [p0254.bin]
 column      c = 0      c = 1               c = k-1   c = k        c = k+1        c = 254
          |          |          |                 |   |          | H|          |    | H|          |
 stripe 0 [ s0,0    ][ s0,1    ] ...             [ s0,k  ]      [ s0,k+1  ]     [ s0,254   ]
 stripe 1 [ s1,0    ][ s1,1    ] ...             [ s1,k  ]      [ s1,k+1  ]     [ s1,254   ]
   ...
 stripe L-1

   columns 0 .. k-1     : data, arithmetic from lba_base
   column  k            : checksum column, file checksum.bin
   columns k+1 .. 254   : parity, file pNNNN.bin, H = header sector
   H is not part of the column.

   A contiguous burst of B sectors inside the parity domain lies in at
   most ceil(B/L)+1 data columns and costs each affected stripe at most
   ceil(B/L) erasures. Therefore any single contiguous burst of up to
   m*L sectors that lies wholly inside the parity domain is correctable.
```

The burst bound holds **inside the data columns only**. The parity domain is
contiguous, so a burst that lies wholly inside it spreads over the data
columns as the diagram shows. A burst that runs past `lba_base + k*L` also
destroys checksum sectors, parity sectors, or both, and those losses do not
interleave: the checksum column and the parity columns are laid out one after
another, so a burst of `B` sectors there removes `B` consecutive stripes' worth
of one column each, not one sector from many stripes. The consequences are
different for each:

- A burst that destroys `j` parity columns of a stripe leaves `m - j` parity
  symbols for that stripe, so the correctable data erasures fall to `m - j`.
- A burst that destroys checksum sectors loses only the digests of those
  stripes, and section 11.4 states what verify does then.

A burst that starts inside the data columns and ends in the parity columns is
therefore bounded by the smaller of the two effects, and the health report
gives the real worst-stripe margin rather than the bound.

The interleave is what makes the scheme work: consecutive sectors on the disc
belong to consecutive stripes, so a burst spreads over many stripes with one
erasure each. The header sectors and File Entry blocks between the column
files shift the column files by a few sectors; the bound above still holds,
because a burst that crosses a file boundary lands in at most one more column.

#### 11.2.1 The code

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
returns `0x53` and `0xA7`. The full-size golden vector of section 23.8 is
computed by the same rules at `k = 231`, `m = 23`.

### 11.3 Burst tolerance

`L = ceil(data_span / k)`, which is close to `run_sectors / 255`. Maximum
correctable single burst is `m * L` sectors.

The two tables below and the interpretation after them are informative. They
compare the fixed `m = 23` with three other values to show why it was chosen;
only the `m = 23` column describes a version 1 disc. The table assumes the
run covers the whole disc and rounds `L` to `floor(run_sectors / 255)`.

| Media | L (sectors) | m=12 (5%) | m=23 (10%) | m=28 (12.5%) | m=42 (20%) |
|---|---:|---:|---:|---:|---:|
| BD 25 GB | 47,919 | 575,028 sec = 1.18 GB | 1,102,137 sec = **2.26 GB** | 1,341,732 sec = 2.75 GB | 2,012,598 sec = 4.12 GB |
| BD 50 GB | 95,838 | 1,150,056 sec = 2.36 GB | 2,204,274 sec = **4.51 GB** | 2,683,464 sec = 5.50 GB | 4,025,196 sec = 8.24 GB |
| BD 100 GB | 191,680 | 2,300,160 sec = 4.71 GB | 4,408,640 sec = **9.03 GB** | 5,367,040 sec = 10.99 GB | 8,050,560 sec = 16.49 GB |
| BD 128 GB | 245,101 | 2,941,212 sec = 6.02 GB | 5,637,323 sec = **11.55 GB** | 6,862,828 sec = 14.05 GB | 10,294,242 sec = 21.08 GB |

Configuration and payload, with `k + 1 + m = 255` sectors per stripe:

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
- **10 percent is the knee of the curve.** That is why version 1 fixes
  `k = 231`, `m = 23`. No other pair is selectable in version 1.

### 11.4 Checksum column

Sector `i` of the checksum column holds the digests of the `k` **data**
sectors of stripe `i`, and of no other sector. There is no offset and no
wrap.

- The digest is the first 8 bytes of the 32-byte BLAKE3-256 digest of the
  sector, computed over the 2048 bytes as they lie on the medium.
- `k = 231` digests at 8 bytes is 1848 bytes. With the 16-byte header that is
  1864 bytes; the remaining 184 bytes of the sector are zero.
- The collision probability per sector is 2^-64. This detects decay. The
  cryptographic guarantee comes from the object content id, not from here.
- The checksum column is **outside** the Reed-Solomon code (section 11.2.1).
  The parity neither covers it nor reconstructs it. A checksum sector that
  the drive cannot read, or whose header CRC fails, loses the digests of its
  stripe and nothing else: verify then checks the data sectors of that stripe
  through the content ids of the objects that the layout table maps them to,
  and treats a sector of an object that fails its content id as an erasure.
  A sector that no extent record covers is then treated as readable.
- A silently wrong digest byte makes verify treat a good data sector as an
  erasure. Reconstruction returns the same bytes, the digest still
  mismatches, and the object's content id passes; verify then reports the
  checksum sector, not the data sector, as damaged. No data is lost.
- A parity sector has no digest. The parity sectors are used only during a
  reconstruction, and a silently wrong parity sector shows up there: the
  reconstructed data sector fails its digest, and the object that holds it
  fails its content id. A parity sector that the drive cannot read is an
  erasure from the start.

  The retry is bounded, and the bound is normative. Let `E` be the erasures
  the stripe already has. The decoder tries **each single parity sector of
  the stripe in turn** as one more erasure, which is at most `m - E`
  additional attempts, and it makes an attempt only while `E + 1 <= m`. An
  attempt succeeds when every reconstructed data sector matches its digest,
  or, with no usable checksum sector, when every object that the layout table
  maps into the stripe passes its content id. If no single-parity attempt
  decodes, the stripe is **undecodable** and the decoder says so. It must not
  try pairs or larger subsets: two wrong parity sectors in one stripe is
  beyond the design point, and searching for them would take
  `C(m, 2)` decodes and could return a wrong answer that passes no check.

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

Silent corruption of a data sector is detected per sector, converted into an
erasure, and corrected with one parity symbol instead of two. This doubles the
effective correction power.

### 11.5 Header replication

The run header exists `m + 2` times, and every copy is an ordinary file or the
first sector of one:

1. `runs/<seq>/RUN.bin`, the first file copied in the run;
2. sector 0 of every parity file `runs/<seq>/parity/pNNNN.bin`, which is `m`
   copies. That sector precedes the column and is not part of it;
3. `runs/<seq>/RUN2.bin`, the last file copied in the run.

At `m = 23` that is 25 copies. Because copy order equals LBA order, copy 1 sits
at the lowest LBA of the run, copy 3 at the highest, and the parity copies are
spread between them. The radial spread is therefore the same as a fixed-LBA
scheme would give, with no hidden sectors. Two copies at the two ends alone
would be wrong: the end of the disc is the highest-risk region.

The parity geometry is derivable from any header copy plus the layout table.
`lba_base`, `column_sectors`, `k` and `m` determine every data column
boundary; `checksum_lba` and `parity_lba` locate the first two column files;
the layout table locates every parity column. A recovery tool that has lost
the layout table scans the raw disc for the `"NARH"` magic: the copies that
lie between `checksum_lba` and the end of the run are, in LBA order, the
header sectors of parity columns `k+1 .. 254`, and each column starts one
sector after its header. A tool that has lost every header copy still knows
`k` and `m`, because version 1 fixes them; it finds `L` by looking for the
`"NACS"` magic of the first checksum sector.

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

Reconstructed objects are written into `staging/heal/` and enter the staging
state machine at STAGED with reason 3, `healed` (section 14.2). The next pack
writes them again like any other STAGED object. In Phase 1 that is the next
data run, on a new disc. From Phase 2, `pack --disc` may put them into a
**repair run** (`run_kind` 2) on the damaged disc itself. The catalog marks
the damaged run `degraded` and lists the lost ids. The restore planner
prefers a healthy run over a degraded one.

### 11.8 Verify and scrub

Verify reads the disc with ddrescue and uses the mapfile as the erasure list:

```bash
ddrescue -b 2048 -n -r1 /dev/sr0 /staging/disc.iso map.log
```

`-n` skips scraping on the first pass, so the pass is fast. Escalate to a full
`ddrescue -d -r3` run only when errors appear. When verify works on an image
file instead of a drive, `verify --image` takes the mapfile through
`--mapfile` (section 19.11); every sector that the map does not mark as
rescued is an erasure.

Section 19.11 lists the three verify levels and what each one reads.

**How verify locates the columns.** Verify needs the run header and the
layout table of every run it checks. On a mounted disc or a mountable image
it reads `runs/<seq>/RUN.bin` and `runs/<seq>/layout.bin` through the
filesystem. When the image does not mount, or a run directory is unreadable,
verify scans the image at sector alignment for the `"NARH"` magic, verifies
`header_crc32c` of every copy it finds, groups the copies by `run_seq` (all
copies of one run are byte-identical), and takes `layout_lba`,
`checksum_lba`, `parity_lba` and `column_sectors` from the header. It reads
`layout.bin` at `layout_lba` and takes the extent of `checksum.bin` and of
every parity file from the extent records with `file_role` 10 and 11. Every
read of an image is a byte-offset read: sector `s` is the bytes
`[s * 2048, s * 2048 + 2048)` of the image file. That is Phase 1 behaviour
and is not raw-LBA reading (section 9.7.1), which reads the physical device
and is Phase 3.

**Sectors that no extent record covers.** Inside a parity domain, a sector
that no extent record of the layout table covers is filesystem metadata or
free space. Verify does not compare such a sector with its recorded digest,
and never reports it as damage, because a later append under profile 1 may
have rewritten it in place (section 9.6.3). The healer treats such a sector as
an erasure when its digest does not match, so a repair never trusts stale
bytes. The status of a run follows the one rule of section 11.9: a run
whose worst stripe has an RS margin below 50 percent is `CRITICAL`, and
when uncovered sectors alone cause that, the health report names the append
that caused it. The packer bounds this: under profile 1 an append must not
rewrite more than `floor(m / 2)` blocks that fall into one stripe of any
earlier run, which the block diff checks against the earlier runs' layout
tables before it writes.

**Scrub schedule.** The table below is the default of `scrub.schedule`,
`scrub.first_check_hours` and `scrub.degraded_interval` (section 20.13),
which the operator may change. The 24-hour second-drive check is a
requirement; the rest is the recommended default.

| Age | Interval | Depth |
|---|---|---|
| Within 24 hours of burning | once | `integrity`, on a **second drive of a different model**. Do not file a disc until it passes on two drives. |
| Year 0 to 1 | at 3 months, then at 12 months | `integrity` |
| Year 1 to 5 | every 12 months | `integrity` on a rotating quarter of the library each quarter |
| Year 5 to 10 | every 6 months | `integrity` |
| Year 10 and beyond | every 6 months | `integrity`, plus a migration plan |
| Any disc marked degraded | every 3 months | `integrity`, and schedule a re-burn |
| After a flood, a heat event, or a move | immediately | `integrity` on the affected shelf |

Informative: a full library scrub cycle should complete in under 12 months.
At about 15 minutes per 25 GB disc plus handling, one drive scrubs about 20
discs in an 8-hour day. A 1000-disc library therefore needs about 50
drive-days per year.

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

**Report file.** `health --json` and `verify --report` write one JSON object
with these fields. A field whose value is unknown is absent, never null.

| Field | Type | Meaning |
|---|---|---|
| `format` | string | Always `noahsark-health-report`. |
| `version` | integer | 1. |
| `repo_uuid` | string | Hyphenated lowercase uuid. |
| `generated_sec` | integer | Report time, seconds since 1970-01-01 UTC. |
| `discs` | array of objects | One per disc in the report. |
| `discs[].disc_uuid` | string | The disc. |
| `discs[].disc_seq` | integer | 0-based. |
| `discs[].label`, `discs[].shelf` | string | On-disc label; shelf note from the notes file (section 14.6). |
| `discs[].media_type`, `discs[].fs_profile` | string | Registry names. |
| `discs[].burn_sec`, `discs[].last_scrub_sec`, `discs[].next_scrub_sec` | integer | Times. |
| `discs[].status` | string | `HEALTHY`, `DEGRADED`, `CRITICAL`, `FAILED`, `UNKNOWN`. |
| `discs[].rs_margin_percent` | integer | Worst stripe over every run of the disc. |
| `discs[].sectors_unreadable`, `discs[].sectors_corrected` | integer | Counts from the last scrub. |
| `discs[].objects_total`, `discs[].objects_verified`, `discs[].objects_repaired`, `discs[].objects_lost` | integer | Counts. |
| `discs[].capacity_sectors`, `discs[].capacity_forced_sectors`, `discs[].used_sectors` | integer | As in the disc directory. |
| `discs[].closed`, `discs[].append_raw_only`, `discs[].capacity_forced` | boolean | `state_flags` of the disc directory. |
| `discs[].spare_remaining_percent` | integer | As in the disc directory. |
| `discs[].ldc_average`, `discs[].bis_average` | number | Present only when the drive exposes them. |
| `discs[].runs` | array of objects | One per run: `run_seq`, `run_kind`, `status`, `rs_margin_percent`, `lost_ids` (array of multihash text). |
| `discs[].critical_cause_run_seq` | integer | The append that rewrote the uncovered sectors, when that is the cause (section 11.8). |
| `library` | object | `margin_histogram` (array of 11 integers, one per 10 percent band), `count_by_status` (object), `overdue_discs` (array of uuid), `oldest_unscrubbed_sec` (integer), `objects_replication_1` (integer), `objects_lost` (integer), `reburn_forecast_12m` (integer), `duplication_ratio` (number), `consolidation_triggers` (object of the keys of section 15.8 to booleans). |
| `objects` | array of objects | Present under `health --object`: `content_id`, `size`, `runs` (array of `run_seq`), `discs` (array of uuid), `replication`, `last_verified_sec`. |
| `manufacturer_trend` | array of objects | `manufacturer_id`, `discs`, `failed`, `degraded`. |

### 11.10 Encoding cost and memory

This section is informative. It gives a working-set target, not a
requirement.

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
   most recent runs, the snapshot table, the ref table, the run table, and the
   disc directory. Section 12.7.2 defines its container.

The prerequisite list, which says what a run does not contain and where it
lives, is part of the manifest (section 12.4). Every one of these is an
ordinary file under `/NOAHSARK/runs/<seq>/`, so a reader needs only the
filesystem.

The newest disc is therefore a complete entry point. It tells a reader the whole
shape of the problem: every snapshot, every disc, and every object that it is
missing. It never says "I do not know".

### 12.2 Run filter

The filter type is **BinaryFuse16** (Graf and Lemire, 2022), with 3-wise
fused segments and 16-bit fingerprints.

**Membership.** The filter holds the id of every object that the run stores
as an object of its own: every chunk, bundled chunks included under their own
chunk ids, every bundle, every chunklist, every tree, and every snapshot
object under `/NOAHSARK/snapshots/`. The catalog copies are not members:
earlier filters, earlier manifests, the replicated snapshot objects under
`catalog/snapobj/` and the tables. The manifest of section 12.3 has exactly
the same membership, one record per member.

The reasons and the size arithmetic below, up to the container table, are
informative.

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

**Key derivation.** The filter key of an object is the little-endian u64 of
bytes 0 to 7 of its digest. The multihash prefix does not enter the key. Two
objects that share their first 8 digest bytes share a key; that is a false
positive at rate 2^-64 per pair, which the manifest confirmation of section
12.8 absorbs.

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

**Construction.** The construction is normative, so that the golden vector of
section 23.8 is reproducible. `n` is the key count, and every key is the u64
of the key derivation above. Duplicate keys are removed first; `key_count`
records the number of distinct keys.

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

*Step 2, positions.* For a key `x` and the current seed, `h`, `f`, `h0`, `h1`
and `h2` are exactly the five values that the query rule above computes. The
three positions of `x` are `h0`, `h1` and `h2`, and its fingerprint is `f`.

*Step 3, peeling.* Build the count and the XOR accumulator of every position:
for each key add 1 to `count[p]` and XOR the key into `xorsum[p]`, for each of
its three positions. Then peel:

1. Push every position `p` with `count[p] == 1` onto a work list, in
   ascending `p` order.
2. Take the lowest position `p` from the work list. If `count[p]` is not 1,
   discard it and continue. Otherwise the key at `p` is `xorsum[p]`. Push the
   pair `(key, p)` onto the peel stack.
3. For each of that key's three positions `q`, subtract 1 from `count[q]` and
   XOR the key out of `xorsum[q]`. When `count[q]` becomes 1, append `q` to
   the work list.
4. Repeat from step 2 until the work list is empty.

The attempt **succeeds** when the peel stack holds all `n` keys. Taking the
lowest position first makes the stack order deterministic, which is what
makes the fingerprint array deterministic.

*Step 4, fingerprints.* Set every entry of `fingerprints` to 0. Then pop the
peel stack, last in first out. For the pair `(key, p)`, with the key's three
positions `h0`, `h1`, `h2` and its fingerprint `f`:

```
fingerprints[p] = f ^ fingerprints[a] ^ fingerprints[b]
```

where `a` and `b` are the two of `h0`, `h1`, `h2` that are not `p`, in that
order. When two of the three positions are equal, `a` and `b` are still the
other two entries of the triple, so a repeated position XORs itself out. The
query rule then returns true for every inserted key, by construction.

*Step 5, the seed search.* The writer starts at `seed` 0 and increments by 1
after every failed attempt. It makes at most **100 attempts**. When no seed in
`0 .. 99` peels, the writer does not write the filter: `pack` exits with code
2 and names the run and the key count. A failure at 100 attempts is a defect
in the writer or a pathological key set, not a recoverable condition; the
peeling construction succeeds on the first seed with probability above 0.99
at this size factor.

The writer records `seed`, `segment_length`, `segment_length_mask`,
`segment_count`, `segment_count_length` and `fingerprint_count` in the
container header. The reader takes them from there and never recomputes them,
so a later version may change step 1 without changing any reader.

A Bloom filter is used only in memory, for the run that is being built, where
incremental insertion is genuinely required. A Bloom filter is never written to
a disc.

The size tables and the super-filter note below are informative.

**About the size.** 18.2 bits per key is the asymptotic figure of the paper.
The real size is `2 * fingerprint_count + 84` bytes, which step 1 above fixes:
80 bytes of container header, the u16 array, and the 4-byte body CRC. That is
a little above 18.2 bits per key at small `n`, because `size_factor` is larger
there and because the array is rounded up to whole segments. The
`filter_bytes` term of section 10.11 uses 18.2 bits per key and is a planning
estimate only; nothing on a disc depends on it.

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

A reserved extension exists for very large repositories: filters for runs
`1 .. N-100` may be merged into decade-sized super filters, one filter over the
union of 100 runs' objects. A super-filter hit narrows the search to 100 runs.
This must be used only when the bundle exceeds 64 MiB. At the numbers above it
never will.

Each filter blob's hash is recorded in `CATALOG.bin` (section 12.7.2), so a
silently corrupted copy is detected instead of giving wrong answers.

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
| 56 | 4 | u32 | `body_crc32c` | CRC-32C over every byte from offset 64 to the end of the container: the TOC, the sentinel and every chunk. |
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
| `"FANO"` | Fan-out table: 256 or 65536 cumulative u32 counts. Mandatory. |
| `"RECS"` | The sorted manifest records. Mandatory. |
| `"BNDL"` | The bundle table: the ids of every bundle in this run, 32 bytes each, sorted ascending. Mandatory; zero length when the run holds no bundle. |
| `"PREQ"` | The prerequisite list (section 12.4). Authoritative. Mandatory; zero length when nothing is missing. |
| `"SRCR"` | The set of run seqs that this run references: u64 values, sorted ascending. Mandatory; zero length when the run references no other run. |
| `"DUPS"` | Duplicate accounting: four u64 values, in the order of section 15.5. Mandatory. |
| `"SPLT"` | Split records: files whose chunks continue on another run (section 15.7). Mandatory; zero length when no file is split. |
| `"BMAP"` | Per-snapshot reachability bitmaps. **Reserved. No payload is defined in version 1.** |
| `"RIDX"` | Reverse index by LBA. **Reserved. No payload is defined in version 1.** |

The seven mandatory chunks appear in every version 1 manifest, in the order
above, so a reader finds each one at a fixed TOC index. A zero-length chunk
has an `offset` equal to that of the next chunk.

`"BMAP"` and `"RIDX"` reserve a chunk id, an optional feature bit and a place
in the TOC order, and nothing else. Their payload layout is not defined in
version 1. **A version 1 writer must not emit either chunk**, and must never
set `OPT_BITMAPS` or `OPT_REVIDX`. A version 1 reader skips any chunk id it
does not know, as the TOC shape allows, so a later version can define both
without a version bump. The connectivity check of section 12.9 and the
planner of section 17.1 therefore never use them in version 1; the paragraphs
that mention the bitmaps describe the reserved future use.

**Membership.** The manifest has one record for every object that the run
stores as an object of its own, exactly the set that the filter holds
(section 12.2). A snapshot object is a member through its file under
`/NOAHSARK/snapshots/<name>`, and that is the LBA its record names; the copy
under `catalog/snapobj/` is not a member.

Manifest record, 64 bytes, sorted ascending by `content_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | The object id. |
| 32 | 8 | u64 | `uncompressed_size` | Payload bytes after decompression. |
| 40 | 8 | u64 | `container` | With `flags` bit0 set: the 0-based index of the bundle in the `"BNDL"` table. Otherwise: the absolute LBA of the first sector of the object's file. |
| 48 | 8 | u64 | `offset` | With `flags` bit0 set: the byte offset of the chunk's stored bytes from the first byte of the bundle file. Otherwise: the byte offset of the object header from the start of sector `container`, normally 0. |
| 56 | 2 | u16 | `flags` | bit0 in a bundle, bit1 duplicate for locality, bit2 metadata object, that is a tree, a chunklist or a snapshot object, bit3 spilled TLV payload. |
| 58 | 1 | u8 | `hash_algo` | Multicodec code. |
| 59 | 1 | u8 | `digest_len` | 32. |
| 60 | 1 | u8 | `kind` | Object kind registry. |
| 61 | 1 | u8 | `compression` | Compression id. |
| 62 | 2 | u16 | `reserved_u16` | Zero. |

A lookup is: read `fanout[b-1]` and `fanout[b]` for the fan-out index `b` of
the id, then binary search that slice. `fanout[-1]` is 0. With `fanout_bits`
8, `b` is byte 0 of the 32-byte `content_id`. With `fanout_bits` 16, `b` is
the big-endian u16 of bytes 0 and 1: `b = byte0 * 256 + byte1`. Big-endian
here is a rule of arithmetic, not a stored field; it makes the fan-out order
equal the unsigned bytewise record order. With fixed 64-byte records the
search is pure arithmetic. There is no parsing, and the container maps
directly into memory.

A chunk in a bundle resolves in two steps: its record gives the bundle index,
the `"BNDL"` table gives the bundle id, and the bundle's own record gives the
bundle's LBA. The stored length of the chunk comes from the bundle index
(section 8.3), which a reader loads once per bundle. For an object that is its
own file, the object header at `container` and `offset` gives the stored
length.

The bundle table entry is 32 bytes: the bundle's content id. The split record
is 48 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `chunklist_id` | The chunklist of the split file. |
| 32 | 8 | u64 | `other_run_seq` | The run that holds the other part. |
| 40 | 4 | u32 | `part_index` | 0-based part number of this run's part. |
| 44 | 4 | u32 | `part_count` | Total parts. |

Split records are sorted by `chunklist_id`, then by `part_index`.

Manifest sizes, informative:

| Objects in the run | Manifest size | Share of that run's disc |
|---:|---:|---:|
| 6,000 (25 GB, P4) | 375 KiB | 0.0015% |
| 24,000 (100 GB, P4) | 1.46 MiB | 0.0015% |
| 48,000 (100 GB, P3) | 2.93 MiB | 0.0031% |

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

The algorithm of `content_id` is the `hash_algo` of the run header of
`run_seq`, so a prerequisite may point across an epoch boundary. The list
lives in the manifest chunk `"PREQ"` and nowhere else.

The list is the discipline that Git's partial clone calls the promisor marker: a
run declares that it is partial and says where the rest lives. "On another disc"
must never be indistinguishable from "corrupt".

The list is bounded, because the capping knobs of section 15 bound the number of
runs that one run may reference.

### 12.5 Snapshot objects, snapshot table, ref table and run table

The catalog replicates the **complete snapshot objects** of every snapshot in
the repository, not only a table of their ids. A snapshot object is a few
hundred bytes, so the whole history costs little. A reader that finds one recent
disc therefore holds every snapshot object, and needs no other disc to list the
history, to name a root tree, or to walk a parent chain.

Tree objects are **not** replicated. A tree is large and there are many of them,
so replicating trees would cost as much as the data. Only the snapshot objects,
which name the root trees, are replicated.

The snapshot table, the ref table and the run table below, and the disc
directory of section 12.6, are also replicated in full on every run. They
grow with the size of the set, not with the size of the data, so replication
is cheap and it makes any recent disc a usable entry point.

Snapshot table record, 136 bytes, sorted by `generation` then by `snapshot_id`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `snapshot_id` | Content id of the snapshot object. |
| 32 | 32 | u8[32] | `parent_id` | Parent snapshot id. Zero for a root. |
| 64 | 32 | u8[32] | `root_tree` | Root tree id. |
| 96 | 8 | u64 | `generation` | 1 + parent generation. |
| 104 | 8 | i64 | `time_sec` | Snapshot time. |
| 112 | 8 | u64 | `object_count` | Objects reachable, as section 8.7 counts them. |
| 120 | 8 | u64 | `first_run_seq` | The run that first held the snapshot object. |
| 128 | 1 | u8 | `flags` | bit0 the snapshot is complete on this set: the connectivity check of section 12.9 found every reachable object in this run or in an earlier run when the packer wrote this table. Clear when the check was not run or found a missing object. |
| 129 | 1 | u8 | `hash_algo` | Multicodec code of `snapshot_id` and `root_tree`. |
| 130 | 1 | u8 | `parent_hash_algo` | Multicodec code of `parent_id`. 0 for a root. |
| 131 | 1 | u8 | `reserved_u8` | Zero. |
| 132 | 4 | u32 | `reserved_u32` | Zero. |

#### 12.5.1 The simple table container

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
| Ref table | `name` bytes ascending, unsigned, over `name_len` bytes; then `time_sec` ascending; then `snapshot_id` bytes ascending. Many records share one name. |
| Run table | `run_seq` ascending. |
| Disc directory | `disc_seq` ascending. |

The snapshot table uses the record of this section, the ref table the ref
record of section 8.8, the run table the record of section 12.5.2, and the
disc directory the record of section 12.6.

The replicated snapshot objects live in `catalog/snapobj/<name>`, one ordinary
file per snapshot, named by the full multihash hex of the snapshot object. The
name is the content id, so a reader verifies each file without any other
structure.

A writer may pack the snapshot objects of the whole repository into one
`catalog/snapobj.bin` container. The container form is the default above
`catalog.snapobj_pack_threshold` snapshots, default 1,000.

`snapobj.bin` is an ordinary **bundle object** (section 8.3): a common object
header with `kind` 2, then the bundle header, the payloads, the index and the
trailer, exactly as section 8.3 lays them out. Three rules make it
unambiguous:

1. The common object header is present, with `kind` 2, `compression` 0, and
   `payload_len` and `stored_len` both equal to the bundle payload length.
2. The `content_id` of each bundle index entry is the **content id of the
   snapshot object** whose payload that entry names. Its `payload_len` and
   `stored_len` are the snapshot object's payload length; `compression` is
   the compression of that payload inside this bundle.
3. The file is a catalog file with `file_role` 7 in `CATALOG.bin`, and it is
   **not** a member of the run's filter or manifest (sections 12.2 and 12.3).
   The bundle has a content id of its own, as every object does, but that id
   names nothing outside this file: the snapshot objects themselves are
   members through their files under `/NOAHSARK/snapshots/`.

A reader that wants one snapshot object reads the trailer, then the index,
then one ranged read, and verifies the bytes against the entry's
`content_id`, exactly as it would inside any other bundle.

The size math below is informative.

Size math for 10,000 snapshots:

| Item | Per item | 10,000 items |
|---|---:|---:|
| Snapshot object payload, typical (header, three ids, times, a short tag list) | 320 B | 3.20 MB |
| Snapshot object payload, worst case allowed by the size cap | 640 B | 6.40 MB |
| UDF File Entry and directory overhead, one file each | 2,048 B | 20.48 MB |
| Snapshot table record | 136 B | 1.36 MB |

Typical total per run: 3.20 + 20.48 + 1.36 = **25.04 MB**, which is 0.10 percent
of a 25 GB disc and 0.025 percent of a 100 GB disc.

The filesystem overhead dominates the payload. The `snapobj.bin` container
form removes the 20.48 MB and leaves **4.56 MB** per run.

At 10,000 snapshots and 2,000 runs, the replicated history costs about 9 GB
across the whole archive in container form. That is under half of one
disc for a complete, 2,000-fold redundant history.

#### 12.5.2 Run table

The run table lists every run of the repository and says on which disc, and
where on that disc, each run lies. It is what lets a reader, and the planner
of section 17.1, go from a `run_seq` in a manifest, a prerequisite record or a
`"SRCR"` chunk to a physical disc without mounting anything. It is replicated
in full on every run, like the other tables.

Record, 128 bytes, sorted by `run_seq`:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u64 | `run_seq` | The run, 1-based. |
| 8 | 8 | u64 | `disc_seq` | The disc that holds it, 0-based. |
| 16 | 16 | u8[16] | `disc_uuid` | That disc's uuid. |
| 32 | 8 | u64 | `lba_base` | As in the run header. |
| 40 | 8 | u64 | `run_sectors` | As in the run header. |
| 48 | 32 | u8[32] | `run_header_hash` | Hash of the 512 run header bytes (section 4.9), under the container's `hash_algo`. All zero in the record of the run that carries this table: that header is written after the table (section 10.5), and it is verified by its own CRC and by the next run's `prev_run_header_hash`. |
| 80 | 8 | i64 | `created_sec` | Burn time, as in the run header. |
| 88 | 4 | u32 | `disc_run_index` | Index of the run on its disc. 0 for the first run of a disc. |
| 92 | 1 | u8 | `run_kind` | As in the run header: 1 data, 2 repair, 3 disc-close parity. |
| 93 | 1 | u8 | `run_hash_algo` | Multicodec code of every object id in that run, copied from its run header. It names no digest inside this record. |
| 94 | 1 | u8 | `run_fs_profile` | Disc filesystem profile of that run's disc. |
| 95 | 1 | u8 | `run_status` | 1 verified, 2 burned but not yet verified, 3 withdrawn by the writer after a failed verify. 0 is invalid. |
| 96 | 8 | u64 | `object_count` | As in the run header. |
| 104 | 24 | u8[24] | `reserved` | Zero. |

The container header is the simple table container of section 12.5.1, with
magic `"NART"` and `record_size` 128.

**Membership.** The table holds one record for **every run that was burned**,
whatever its state, plus the record of the run that carries the table. No run
is ever omitted, so the table and the run chain of section 9.6.1 always agree
about which runs exist. `run_status` says what is known about each:

| `run_status` | Meaning | When the writer sets it |
|---:|---|---|
| 1 `verified` | The run was burned and `verify` passed. | After a successful `verify`. |
| 2 `unverified` | The run was burned, and `verify` has not passed yet. | For the run that carries the table, which is written before that run is burned (section 10.5) and therefore also carries a zero `run_header_hash`. Also for an earlier run that is burned but not yet verified. |
| 3 `withdrawn` | The writer explicitly withdrew the run after a failed verify. Its objects are on the medium but no reader may rely on them. | Only after `verify` failed and the writer decided to re-burn the data as a new run. |

**Withdrawal is explicit and is never inferred from absence.** A reader must
not treat a run as withdrawn because a later table omits it; a later table
never omits it. A `run_seq` is assigned by `pack` and is **never reused**: a
run whose burn or verify fails leaves its record in the table with
`run_status` 3, its objects go back to STAGED or stay PACKED (section 14.2),
and the re-burn is a new plan with the next `run_seq`. A run that was planned
but never written reaches no table at all, so its `run_seq` is a hole in the
sequence.

A record is appended once. A later copy of the table differs from an earlier
one only by the records added at its end and by `run_status` values that
moved from 2 to 1 or from 2 to 3. No other field of a record ever changes.
A reader that sees `run_header_hash` all zero in a record whose `run_status`
is 1 or 2 takes the hash from the run header itself, or from the next run's
`prev_run_header_hash`.

A reader that holds the run table and the disc directory knows, for every
object it can locate through a manifest, the disc to ask for, the label to
look for on the shelf, and the LBA range to read.

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
| The full run table | The whole repository | Maps every run seq to its disc and its LBA range (section 12.5.2). |
| The disc directory | The whole repository | The entry point for "which physical disc". |
| This run's prerequisite list, inside the manifest | This run | What is missing and where it is. |
| This run's layout table | This run | LBA extents of every file, for FEC and for the Phase 3 recovery read. |

This run's filter, this run's manifest with its prerequisite list, and this
run's layout table are the run's own files, outside `catalog/`. Tree objects
are not in this list. A tree is reachable through its snapshot, and
replicating trees would cost as much as replicating the data.

The manifest history depth is `manifest.history_depth`, default 8. Eight
manifests cost about 2.93 MiB at 6,000 objects per run, at the 375 KiB per
manifest of section 12.3.

#### 12.7.1 Copy priority under the size cap

The catalog may not exceed `catalog.max_bytes`, default 512 MiB. When the full
catalog would exceed the cap, the writer drops items in reverse priority
order:

| Priority | Item | Dropped when |
|---:|---|---|
| 1 | Every snapshot object | Never. |
| 2 | Every run filter | Never. |
| 3 | The disc directory and the run table | Never. |
| 4 | The snapshot table and the ref table | Never. |
| 5 | Recent manifests, newest first | The cap is reached. |

Priorities 1 to 4 are mandatory. They grow with the size of the set, not with
the size of the data, so they stay small. Only the manifest history is elastic:
the writer keeps as many recent manifests as the cap allows, and records the
number it kept in `manifests_kept` of `CATALOG.bin` (section 12.7.2). A run
that keeps zero manifests is still valid, because a filter negative is still a
proof and the run's own manifest is outside the catalog.

If priorities 1 to 4 alone exceed the cap, the writer does not drop them. It
raises `catalog_bytes_per_run` of section 10.11 to their size plus one
manifest, recomputes `catalog_growth`, and reports the new figure, because
losing the history would break the index-free promise. On a new disc that
value enters the superblock as part of `reserve_computed_sectors`. On an
existing disc the superblock never changes; the packer lowers the data
budget of the remaining runs by the difference and reports both figures.

Manifests are **not** replicated for the whole repository. At 2,000 runs the
cumulative manifest would be 2,000 x 375 KiB = 732 MiB. That is still only 3
percent of a disc, so it is possible, but it grows linearly with no rollup and
it duplicates what the local cache already holds.

#### 12.7.2 Catalog container

The catalog is a directory of files. `catalog/CATALOG.bin` is the one
structure that a reader opens first: it lists every other catalog file with
its hash and its length, so every catalog file is verified before it is used.
The run header's `catalog_lba`, `catalog_sectors` and `catalog_hash` name and
verify `CATALOG.bin` itself.

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
| 44 | 4 | u32 | `filters_kept` | Number of earlier filters carried. Equals the number of earlier runs. |
| 48 | 1 | u8 | `snapobj_form` | 1 one file per snapshot object under `snapobj/`. 2 one packed `snapobj.bin`. |
| 49 | 3 | u8[3] | `reserved` | Zero. |
| 52 | 4 | u32 | `reserved_u32` | Zero. |
| 56 | 4 | u32 | `body_crc32c` | CRC-32C over the entries. |
| 60 | 4 | u32 | `header_crc32c` | CRC-32C over bytes 0 to 59. |
| 64 | | | `entries` | `entry_count` records of 64 bytes, in the order below. |

Catalog entry, 64 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `file_hash` | Hash of the whole file's bytes. For a snapshot object file, this equals the digest in its name. |
| 32 | 1 | u8 | `file_role` | 1 `filters/<seq>.bin`. 2 `manifests/<seq>.bin`. 3 `snapshots.bin`. 4 `refs.bin`. 5 `discs.bin`. 6 `snapobj/<name>`. 7 `snapobj.bin`. 8 `runs.bin`. |
| 33 | 1 | u8 | `hash_algo` | Multicodec code of the digest in a `snapobj/<name>` file name. 0 for other roles. |
| 34 | 2 | u16 | `reserved_u16` | Zero. |
| 36 | 4 | u32 | `reserved_u32` | Zero. |
| 40 | 8 | u64 | `seq` | For roles 1 and 2, the run seq the file describes. 0 otherwise. |
| 48 | 8 | u64 | `byte_len` | Length of the file in bytes. |
| 56 | 8 | u64 | `reserved_u64` | Zero. |

Entries are ordered by `file_role` ascending, then by `seq` ascending, then by
`file_hash` ascending. The file order in the run (section 10.5) is the entry
order, so the catalog reads as one sequential pass.

A reader that finds a catalog file whose hash does not match its entry treats
that file as absent and takes the same file from an older run, or reports it.
It never uses the mismatched bytes.

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

Reserved: with per-snapshot bitmaps the check would reduce to a counting
argument, in which each run's snapshot bitmap is ORed into an accumulator and
the accumulated count is compared with `object_count` in the snapshot record.
The `"BMAP"` chunk that would carry them has no payload defined in version 1
(section 12.3), so a version 1 reader always uses the walk above.

The report says, per object, "present on run X", "missing", or "declared
prerequisite".

---

## 13. Local cache

### 13.1 Location and contents

Two rules are normative. Everything else in this subsection is informative.

1. **Everything in the cache is derived from discs and is rebuildable.** A
   command must behave the same, apart from speed, with the cache deleted
   (section 13.4).
2. **Never in the cache: anything whose loss loses archive data.** A cache
   file is a copy of disc data, or a log that the next `verify` or `commit`
   regenerates. If losing the cache loses data, the design is broken.
   Operator data such as shelf notes lives in the repository
   (`<repo>/notes.bin`, sections 3.7 and 14.6), never here.

Informative: the default location is `$XDG_CACHE_HOME/noahsark/<repo-uuid>/`,
which falls back to `~/.cache/noahsark/<repo-uuid>/`. The `--cache-dir` flag
and `cache.dir` override it. The layout below is the reference
implementation's; another layout that obeys the two rules conforms.

| Item | Content |
|---|---|
| `index.bin` | Merged sorted index over all runs. The manifest record plus `run_seq`. Multi-pack-index shape. |
| `filters/<seq>.bin` | Copies of run filters. |
| `manifests/<seq>.bin` | Copies of run manifests, accumulated as discs are mounted. |
| `snapshots.bin` | A copy of the snapshot table. |
| `refs.bin` | A copy of the ref table. |
| `runs.bin` | A copy of the run table. |
| `discs.bin` | A copy of the disc directory. |
| `health.log` | Per-disc verification history, derived from the discs' checksum columns and the health records that `verify` writes. The next scrub regenerates it, and the last verify date is also on disc in the disc directory. |

**The health of the newest disc lives only here until the next disc is
burned.** A disc's health record reaches a disc through the disc directory
(section 12.6), which the *next* run writes into its catalog, so the newest
disc carries no health record for itself. Losing the cache therefore loses
the newest disc's verification date and RS margin, and nothing else. That is
allowed by rule 2 of this section, because re-running `verify` or `scrub` on
that disc regenerates the record from the medium. A reader that finds no
health record for the newest disc reports `UNKNOWN` (section 11.9) and
recommends a scrub; it must not report `HEALTHY`.
| `xlate-<from>-<to>.bin` | Optional cross-algorithm side table (section 5.7). |
| `pending-confirm.log` | Unconfirmed filter hits, with the runs that would confirm them. The next `commit` regenerates it; losing it wastes space, never data. |

`index.bin` uses the manifest record layout with an added `run_seq`, and a
256-entry or 65536-entry fan-out. Rebuilding it is a merge sort over the
per-run manifests. There is never a conflict to resolve, because a manifest is
immutable and a run seq is unique. That is the concrete meaning of "the cache is
only an accelerator".

### 13.2 Rebuild levels

| Level | Minimum set | Gives | Cost |
|---:|---|---|---|
| 1 | The newest disc | The catalog: every run's filter, the snapshot table, the ref table, the run table, the disc directory, and the newest 8 manifests. Answers "which run probably holds X" and "which disc holds that run" for the whole repository. | One disc mount, a few seconds. |
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

The staging store is a local directory inside the repository (section 3.7).
The subdirectory names below are informative; an implementation may choose
others. The roles, `state.db`, and the filesystem rule are normative.

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

`state.db` and the local ref log `<repo>/refs.bin` (section 14.6) are the
only authoritative local state. Everything else in staging is either an
object that also exists in the source, or derived data.

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
5. A run that fails to burn returns its objects to STAGED, with reason 1.
6. A run that fails verify keeps its objects PACKED, with reason 2, and marks
   the run for re-burn.
   In both cases the failed `run_seq` is never reused (section 12.5.2): the
   next `pack` assigns the next `run_seq`, and the objects get a new PACKED
   record that names it.
7. An imported bundle object enters the machine at STAGED, exactly like a
   locally chunked object. `commit`, `import` and `verify --heal` are the
   only entry points.
8. The state log is authoritative only for objects that are not yet CLEAN.
   Everything about a CLEAN object is derivable from the discs.
9. A healed object enters the machine at STAGED. `verify --heal` writes its
   bytes into `staging/heal/` and appends the STAGED record with reason 3,
   `healed`, before it reports success. From there the object follows the
   normal path: the next `pack` selects it, and it reaches CLEAN only when
   the run that holds it again passes `verify`. An object that is still
   present in another run in a state at or beyond CLEAN is not healed; it is
   re-fetched from that run (section 11.7).

**What a partially completed `pack` leaves behind.** `pack` writes local
files only. It touches no disc, so an interrupted `pack` can never leave a
partial run on a medium. It leaves exactly three things, and the next `pack`
resolves all three:

1. **A `run_seq` that is consumed.** `pack` assigns the number before it
   builds anything, and the number is never reused (section 12.5.2). An
   interrupted `pack` therefore leaves a hole in the sequence. The hole is
   harmless: the run reaches no run table, because only a burned run does.
2. **A plan directory under `staging/plans/<run_seq>/`**, holding some subset
   of the run image, the sort file, the patches, `burn.bin` and `burn.json`.
   It is incomplete, and it is recognised as incomplete because `burn.bin` is
   written last and is the only authoritative file. A plan directory with no
   valid `burn.bin`, or whose `header_crc32c` or any `step_crc32c` fails, is
   a partial plan. `burn --print` and `burn --exec` refuse it (section
   10.7.1, rule 1). `gc` deletes a partial plan directory whose `run_seq`
   appears in no PACKED state log record; rule 5 of section 14.4 protects only
   a plan that has objects behind it.
3. **PACKED records in the state log**, for the objects the interrupted pass
   had already selected. Each names the consumed `run_seq`. The objects are
   still in `staging/objects/`, unchanged, because `pack` never moves or
   rewrites an object file.

The next `pack` starts by scanning the state log for PACKED records whose
`run_seq` belongs to no valid plan and to no burned run. It returns those
objects to STAGED with reason 1, deletes the partial plan directory, and
takes the next `run_seq`. Nothing is lost, because every object file is still
present and every object is content-addressed. The only cost is the wasted
image build. `pack --dry-run` never reaches state 2 or 3, because it writes
no plan and no state record.

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

### 14.5 Concurrency and locking

One repository is used by one process at a time for every command that writes
local state. The rules below are the requirement; the system calls named in
them (`flock`, `O_EXCL`) are the Linux way to meet them and are informative,
as in sections 18.4 and 18.8.

1. **Repository lock.** `<repo>/lock` is the lock file. A command that writes
   the state log, the staging store, the config, or a burn plan takes an
   exclusive advisory lock on it (`flock(LOCK_EX)` on Linux) before it reads
   the state log, and holds it until it exits. Those commands are `init`,
   `commit`, `import`, `pack`, `append`, `burn --exec`, `close`, `verify`,
   `scrub`, `gc`, `restore`, `consolidate` and `disc label`.
2. **Read-only commands** take a shared lock (`LOCK_SH`) while they read the
   state log, and release it before they do anything slow. Those commands are
   `plan`, `ls`, `log`, `health`, `disc list`, `burn --print`, `image build`,
   `image diff`, `image mount`, `rebuild-cache` and `reindex`.
3. A command that cannot get its lock waits `repo.lock_timeout` seconds,
   default 0, and then exits with code 2 and a message that names the lock
   file and the holder's pid, which the holder writes into the file.
4. **Cache lock.** `<cache>/lock` guards the cache directory in the same way.
   `rebuild-cache` takes it exclusively. Every other command takes it shared
   while it reads the cache and exclusively for the moment it merges a new
   manifest.
5. **Drive lock.** A command that opens a drive for writing or for a
   verification read opens the device with `O_EXCL`. Two commands never share
   a drive.
6. `restore` and `verify` may run against a repository while another
   repository's command runs; locks are per repository.
7. The state log is appended under the exclusive lock only. A reader under a
   shared lock replays the log to the last valid record and ignores a partial
   tail, exactly as section 14.3 states for a crash.

The locks are advisory. They are not a security boundary.

### 14.6 Local repository state: refs, the pending snapshot chain, and notes

Three facts live only on the local machine between a commit and the verify
of the run that carries them: the current value of every ref, the chain of
snapshot objects that no verified run holds yet, and the operator's notes.
This section defines where they live and how a command resolves them.

**The local ref log.** `<repo>/refs.bin` is an append-only log with the
header of the state log (section 14.3), magic `"NALR"`, `record_size` 128,
and these records:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 96 | ref record | `ref` | The ref record of section 8.8, unchanged. `run_seq` is 0 while no run holds the snapshot; it is the run's `run_seq` from the moment `pack` puts the snapshot object into a run. |
| 96 | 8 | u64 | `sequence` | Monotonic record number. |
| 104 | 8 | u64 | `snapshot_generation` | `generation` of the snapshot named by `ref`. For a fast ancestry check. |
| 112 | 12 | u8[12] | `reserved` | Zero. |
| 124 | 4 | u32 | `record_crc32c` | CRC-32C over bytes 0 to 123. |

Rules:

1. `commit` appends one record per moved ref, with `run_seq` 0, after it has
   written the snapshot object into staging and recorded it STAGED.
2. `pack` copies every local record whose `run_seq` is 0, and whose snapshot
   object it puts into the run, into the run's `refs.bin` with `run_seq` set
   to the run, and appends the same record to the local log.
3. The current value of a ref is the newest local record for that name, by
   `sequence`. When the log has no record for the name, the value comes from
   the cache's copy of the ref table, and with no cache from the ref table
   of the newest disc. The order is local log, then cache, then discs.
4. A local record whose `run_seq` names a run that has reached CLEAN is
   derivable from the discs; a writer may drop it when it compacts the log,
   as section 14.3 allows for the state log. A record with `run_seq` 0, or
   naming a run that is not yet CLEAN, is authoritative and is never
   dropped.
5. A record with a bad CRC ends the replay, as in section 14.3.
6. `init --repo-uuid` on a recreated repository starts with an empty log;
   `rebuild-cache` then supplies every ref from the discs.

**The pending snapshot chain.** The chain is the set of snapshot objects in
staging whose state is below CLEAN. Its head for a ref is the snapshot named
by the newest local record. Every snapshot in the chain is reachable from a
head by `parent` pointers, and every `parent` of a chain member is either
another chain member or a snapshot that a verified run holds. `commit`
resolves its parent by rule 3 and reads the snapshot object from staging
when its state is below CLEAN, else from the cache or the discs. `log` and
`ls` read the chain from staging before they read the snapshot table. A
chain is never longer than the number of commits since the last verified
run.

**The notes file.** `<repo>/notes.bin` holds operator notes per disc. It is
an append-only log with the header of the state log, magic `"NANT"`,
`record_size` 256, and these records:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 16 | u8[16] | `disc_uuid` | The disc. |
| 16 | 8 | i64 | `time_sec` | When the note was written. |
| 24 | 4 | u32 | `time_nsec` | Nanoseconds. |
| 28 | 2 | u16 | `label_len` | Byte length of `label`, 0 to 64. |
| 30 | 2 | u16 | `shelf_len` | Byte length of `shelf`, 0 to 128. |
| 32 | 64 | u8[64] | `label` | Local display label, UTF-8, zero-padded. |
| 96 | 128 | u8[128] | `shelf` | Shelf note, UTF-8, zero-padded. |
| 224 | 8 | u64 | `sequence` | Monotonic record number. |
| 232 | 20 | u8[20] | `reserved` | Zero. |
| 252 | 4 | u32 | `record_crc32c` | CRC-32C over bytes 0 to 251. |

The newest record per `disc_uuid`, by `sequence`, is the current note.
`disc label` appends a record (section 19.22). The file is convenience data:
losing it loses no archive data, and nothing in it reaches a disc.

---

## 15. Packing and locality

In the data flow, commit (section 16) comes before packing. This section
comes first because it defines the terms that the commit flow uses: capping,
the split threshold and the duplication caps.

### 15.1 Why locality wins over dedup

This subsection is informative.

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

1. **A file's chunks go in one run.** The only exceptions are the two cases
   of section 15.7: a file larger than a whole disc, and a file that exceeds
   the remaining capacity by more than `split.threshold` of a disc.
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

This subsection is informative. The rules of section 15.2 and the knobs of
section 15.4 are the requirement; any algorithm that satisfies them
conforms. The reference implementation uses two passes, plus a pre-pass.

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
| `locality.max_source_runs` | 8 | Per segment of the incoming stream, reference at most this many older runs. Rewrite the rest. |
| `locality.segment_size` | 1 GiB | The unit that capping is applied over: a segment is the next `segment_size` bytes of file content in walk order, and it crosses file boundaries. The last segment of a commit may be shorter. |
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

The knobs are ranked. When they conflict, a higher rank wins:

1. The per-file caps, `max_duplicate_bytes_per_file` and
   `max_duplicate_ratio_per_file`. A file whose rewrite would exceed either
   cap keeps its cross-run references, even above `max_source_runs`.
2. `max_source_runs` and `rewrite_below_chunks`, per segment.
3. Rule 1 of section 15.2, which keeps a file's newly written chunks in one
   run. It never forces a rewrite that rank 1 forbids.
4. `disc_budget`, per disc. When the budget is reached, the packer stops
   rewriting and references older runs for the rest of the disc, and the
   health report says so.

Presets:

| Preset | `max_source_runs` | Effect |
|---|---:|---|
| `dedup` | unlimited | Maximum space saving. A restore may need every disc. |
| `balanced` | 8 | **Default.** A snapshot restores from at most about 9 runs per segment. Expect a few percent of dedup loss. |
| `locality` | 2 | A snapshot restores from at most 3 runs per segment. Good for one disc set per project. |
| `standalone` | 0 | No cross-run references at all. Every disc set is readable with no other disc. Costs the most media and gives the strongest durability story. |

`standalone` sets `max_source_runs` to 0 **and** lifts the three caps that
rank above it: `max_duplicate_bytes_per_file`,
`max_duplicate_ratio_per_file` and `disc_budget` become unlimited. Rank 1
and rank 4 then never keep a cross-run reference, so the preset's promise
holds. A user who sets `max_source_runs = 0` by hand without lifting the
caps gets rank 1 behaviour: a file above a cap keeps its references.
`standalone` also makes the filter chain purely informational. It is a
legitimate archival choice and the design must not forbid it.

Note the second-order effect: controlled duplication puts an object on several
runs, which gives the restore planner real freedom (section 17.1). It is also
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

A file is split across runs only when one of two conditions holds:

```
file_size > data_budget * 2048                                 # larger than a disc
file_size - free_now > split.threshold * data_budget * 2048    # too far over the rest
```

`free_now` is the free part of the data budget of the current disc in
bytes (section 15.6), `data_budget` is in sectors (section 10.11), and
`split.threshold` defaults to 0.25. Otherwise the packer starts a new run
for the file. Wasting a quarter of a disc
is cheaper than adding a disc to every future restore of that file.

When a split happens, the packer places the parts on discs that the plan will
order adjacently, and records the split in the manifest chunk `"SPLT"` of
every run that holds a part (section 12.3). The chunks of the other part are
also prerequisites of each run. The planner reads the split records to place
the two discs next to each other.

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

## 16. Commit flow

### 16.1 Commit flow

```
0. Choose direct mode (section 16.3) or mirror mode (section 16.4).
1. Resolve the source roots and the parent snapshot. The parent is the
   current value of the ref being moved, resolved in this order: the local
   ref log, then the cache's ref table, then the ref table of the newest
   disc (section 14.6). No parent means a root snapshot.
2. Build the candidate file list:
     a. full scan when --full-scan or --checksum is given;
     b. otherwise the quick check of section 16.2 against the parent
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
5. Compare the new root tree id with the parent's root tree id. When they
   are equal, and --force is not given, stop here: write no snapshot object,
   move no ref, and report "no change" with exit code 0. Otherwise write the
   snapshot object with the root tree, the parent, the generation, and the
   metadata TLVs.
6. Record every new object in the state log as STAGED.
7. Append a ref record with run_seq 0 to the local ref log for the ref
   being moved, by default LATEST (section 14.6).
```

A commit normally runs against a live source. Section 16.6 states how a file
that changes during the read is detected and skipped. Section 16.7 states the
better answer, which is to commit a filesystem snapshot.

An unchanged file costs one tree entry and no chunk read. An unchanged directory
costs one hash comparison, because its tree id did not change.

### 16.2 The quick check

`commit` never writes to a source root. A source is read strictly read-only.
Every byte that NoahsArk creates goes into staging, at `staging.dir`, which may
be a separate disk.

The change test is the rsync quick check. For every file, the walker compares
three fields with the parent snapshot's tree entry:

| Field | Compared as |
|---|---|
| Size | Exact `u64` equality. |
| mtime | Seconds and nanoseconds. Exact equality on a local root. On a remote root a difference below `source.mtime_slack` counts as equal (section 16.10). |
| ctime | Seconds and nanoseconds, exact equality. |

The three-field form is the default for a local source. A remote source uses
size and mtime only (section 16.10), because ctime is not trustworthy there.
`source.quick_check` selects the form, and `metadata.ctime = false` (section
20.10) also selects the two-field form, because a tree with no ctime cannot
be compared on it.

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

#### 16.2.1 The parent tree is the old copy

rsync compares a source against an old copy of the same data. NoahsArk keeps no
old copy. **The parent snapshot's trees hold the size, the mtime and the ctime
of every path, so they take the place of rsync's old copy.**

That has one important consequence: the staging disk never needs to hold a
second copy of the source. It needs space only for the change set.

### 16.3 Direct mode

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

### 16.4 Mirror mode

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
   mode, uid, gid, device number, inode number and link count.
     - local source:  a walk;
     - remote source: one ssh command that prints the same listing
       (section 16.12 shows the `find` form).
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
9. Derive every hardlink group id from the source device and inode numbers
   of the listing, never from the mirror copy (section 8.6). When the
   listing has link counts but no device or inode numbers, use the path-set
   rule of section 8.6 and set HARDLINK_BY_PATH.
10. Clear staging/mirror/ after the objects reach STAGED.
```

`--delete` is **not** used, because the mirror is not a full copy. Deletions come
from the listing diff, which is authoritative.

| rsync option | Reason |
|---|---|
| `-a` | Archive mode: recursion, times, mode, owner, group, symlinks. |
| `-H` | Preserve hard links inside the mirror, so that the members of a group share one mirror inode. The group id itself comes from the source listing (section 8.6). |
| `-A` | Preserve POSIX ACLs. |
| `-X` | Preserve extended attributes. |
| `--numeric-ids` | Never map ids through the local name service. |
| `--files-from` | Transfer only the change set. |

The staging disk therefore needs space for the change set, not for the source.
A 4 TB source with 20 GB of daily change needs about 20 GB.

NoahsArk does not reimplement rsync. `sync` prints the exact command before it
runs it.

Mirror mode is Phase 2. Direct mode and the quick check are Phase 1.

### 16.5 Sources and excludes

A repository has one or more **source roots**. Each root is an absolute path.
The snapshot records every root and its exclude rules, so a restore knows what
the snapshot was meant to contain.

Defaults:

| Rule | Default | Reason |
|---|---|---|
| Multiple roots | Allowed | One repository can cover `/home` and `/srv`. |
| Root ordering | Sorted by path bytes | The snapshot must be deterministic. |
| Exclude syntax | The pattern language of section 16.5.1 | It is gitignore-shaped, so it is familiar, and it is specified here in full, because trees are content-addressed. |
| Exclude sources | `config`, then `--exclude`, then a `.noahsarkignore` file in any directory | Local rules stay with the data. |
| Sources | Read-only. Never written. |
| Symlinks | Never followed | A followed symlink duplicates data and can leave the root. The link itself is stored. |
| Filesystem boundaries | Never crossed | A bind mount or a network mount would be pulled in silently. `sources.one_file_system`, default true. |
| Special files | Recorded by type, with no content | A device node or a socket has no bytes to store. |
| Unreadable file | Skipped, reported, exit code 1 | A backup must not fail silently and must not stop. |

An excluded path is not in the tree at all. The exclude rules are stored in the
snapshot as a TLV, so a later `ls` can explain why a file is absent.

**The root tree.** A snapshot has exactly one `root_tree`. It is a synthetic
directory that the writer builds; it does not correspond to any source
directory. It holds one entry per source root, in root order:

- `entry_type` is 2, directory. Its content is the tree id of the root's own
  directory.
- The name is the root's absolute path, encoded so that it is one path
  component that section 8.5.8 accepts. **Exactly four bytes are escaped and
  no others**: `/` becomes `%2F`, `\` becomes `%5C`, NUL becomes `%00`, and
  `%` becomes `%25`. The hex digits are uppercase. Every other byte, `.`
  included, is kept as it is. `/srv/data` becomes `%2Fsrv%2Fdata`, and `/x\y`
  becomes `%2Fx%5Cy`. Decoding replaces every `%XX` by its byte. The encoding
  is reversible, so two distinct roots never collide. An encoded name above
  4095 bytes, the `name_len` limit of section 8.5.1, is refused at commit
  time.
- A root path is absolute, so the encoded name always starts with `%2F` and is
  therefore never empty, never `.` and never `..`. The writer still checks it:
  a root whose encoded name would be empty, `.` or `..` is **refused at commit
  time**, with a message that names the root. A reader applies the same test
  and refuses the entry, because section 8.5.8 rejects those names at parse
  time whatever produced them.
- The TLV `ROOT_PATH` (0x0004) carries the raw path bytes, unencoded.
- mode, uid, gid and the times are those of the root directory itself.

The rule holds with one root as well, so every snapshot has the same shape.
`restore SNAPSHOT TARGET` creates `TARGET/<root path>` for each root, that is
`TARGET/srv/data`, and `restore --include` names paths below a root. The
intermediate directories on that path that no tree entry describes,
`TARGET/srv` in the example, are created with mode 0700, the invoking
user's uid and gid, and the restore time as mtime; an intermediate directory
that already exists is left as it is. The restore reports every intermediate
directory it created, once, in the loss report under the field
`intermediate_directory` with reason `SYNTHESIZED`, and that alone does not
change the exit code. The
synthetic root tree costs one small object per commit and changes only when a
root's own metadata or content changes.

#### 16.5.1 Exclude pattern language

Trees are content-addressed, so two writers with the same source and the
same rules must produce the same tree. The pattern language is therefore
normative.

**Rules and sources.** A rule is one pattern line. The rules come from three
sources, and their order is: every `sources.exclude` key in config order,
then every `--exclude` option in command-line order, then the
`.noahsarkignore` files on the path from the source root down to the
directory that holds the entry, root first. A rule in a deeper file comes
after a rule in a shallower one. Inside one file, rules are in line order.

**Lines.** A blank line and a line whose first byte is `#` are ignored. A
leading `\#` or `\!` is a literal `#` or `!`. Trailing spaces are ignored
unless the last one is escaped with `\`. Matching is on raw bytes, byte for
byte, case-sensitive, with no normalization.

**Anchoring.** A pattern that contains a `/` other than a trailing one is
anchored: it matches relative to the directory of the rule, which is the
source root for a config or `--exclude` rule and the directory that holds
the `.noahsarkignore` file otherwise. A leading `/` anchors a pattern in the
same way and is then removed. A pattern with no `/` other than a trailing one
matches the name of an entry at any depth below the rule's directory.

**Trailing slash.** A pattern that ends in `/` matches a directory only. The
`/` is then removed and the rest is matched as above. A pattern with no
trailing `/` matches an entry of any type.

**Wildcards.** `*` matches any run of zero or more bytes except `/`. `?`
matches exactly one byte except `/`. `[...]` matches one byte from the set,
with `-` for a range and a leading `!` for the complement. `**` has three
forms: a leading `**/` matches in every directory, so `**/foo` matches `foo`
at any depth; a trailing `/**` matches every entry below the named
directory; `/**/` in the middle matches zero or more directories. Any other
`**` is two `*`. A `\` escapes the next byte.

**Match order and negation.** A pattern whose first byte is `!` is a
negation. The rules are applied in the order above to the path of an entry,
relative to the rule's directory; the **last matching rule wins**. If it is
a negation the entry is included, otherwise it is excluded. An entry that no
rule matches is included. An excluded directory is not entered: nothing below
it is scanned, and no later negation can bring anything below it back. The
source root itself is never matched against any rule and cannot be excluded.

The rules that produced a tree are stored in the snapshot as TLV tag 5, in
the order above, so `ls` can explain why a path is absent.

### 16.6 In-flight change detection

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

### 16.7 Filesystem snapshots as the source

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

### 16.8 Scheduling

**NoahsArk has no built-in scheduler.** A backup tool that also schedules is two
programs in one, and every operating system already has a scheduler that is
better tested. `commit` is a batch job. Run it from cron or from a systemd
timer.

The two unit examples below are informative.

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

Two commits must never run at once on one repository. `commit` takes the
exclusive repository lock of section 14.5 and exits with code 2 when it cannot
get it within `repo.lock_timeout`.

Exit code 1, which means "some files were skipped", is normal on a live source.
A monitoring rule should alert on code 2 and on a rising unstable count, not on
code 1 alone.

### 16.9 Future watch trigger

Phase 1 has **manual `commit` only**. There is no daemon.

A watcher is Phase 3. It will record changed paths with `fsnotify` and append
them to a log, and `commit` will take the union of that log and the quick check.
It will never commit by itself, because a commit needs a stable filesystem.

Nothing has to change for it to arrive. `commit` is idempotent: a commit
whose root tree equals the parent's root tree writes no new snapshot object
and moves no ref, unless `--force` is given (section 16.1, step 5). The
watcher is therefore an accelerator for step 2 of the commit flow, and it
changes no format and no state.

### 16.10 Remote source roots: NFS and SMB

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
| In-flight change | Detected | Detected | Detected | The re-stat rule of section 16.6 is unchanged. |

Because the quick check is weaker on a remote root, a periodic full rehash is
required. `source.checksum_every`, default 30 days, makes `commit` behave as if
`--checksum` were given once the interval has passed. The snapshot records that
it was a checksum commit.

Every one of these facts is recorded in the snapshot's `source_type` and
`source_flags` fields (section 8.7), so a restore can explain what the archive
does and does not contain, years later, when the mount is gone.

Restoring **to** an NFS or SMB target follows the non-root metadata policy of
section 18.7: apply what the target accepts, report the rest, exit code 1.

### 16.11 Remote sources by commit bundle (Backlog)

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
files: every run filter, the recent manifests, every snapshot object, the
snapshot table, the ref table, the run table, and the disc directory. It is
the same content that a run carries (section 12.7), in the same formats.

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

#### 16.11.1 Bundle header

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
(section 16.10) stay supported, and they cover the case a NAS presents, because
a NAS cannot run the binary.

A future live protocol would only automate the transfer of a bundle. It would
change no on-disc structure.

### 16.12 Deployment modes for a remote data host

Three ways to back up a machine that holds the data but is not the machine that
holds the discs.

| | A: mount and commit | B: hybrid listing | C: commit bundle |
|---|---|---|---|
| Software on the data host | None | ssh and `find` | The NoahsArk binary |
| Network traffic | Every read file, plus the whole directory walk | The listing, plus the changed files | The listing is local; only new objects cross |
| Directory walk speed | Slow. One round trip per `stat` | Fast. One `find` on the data host | Fast. Local walk |
| Metadata fidelity | Limited by the mount (section 16.10) | Limited by the mount for content, exact for the listing | Full: ctime, hardlinks, xattrs, ACLs |
| CPU location | The repository server | The repository server | The data host |
| Temporary space | None | The listing, a few MB | The bundle, the size of the change set |
| Phase | 1 | 2 | Backlog |

Mode A mounts the source over NFS and runs `commit` normally. Prefer NFS over
SMB: NFS keeps link counts, extended attributes and often ctime, and SMB keeps
few of them.

Mode B runs one command on the data host, over ssh, to produce a stat listing:

```bash
ssh nas "find /srv/data -printf '%y\t%s\t%T@\t%C@\t%m\t%U\t%G\t%D\t%i\t%n\t%p\n'"
```

`%D`, `%i` and `%n` are the device number, the inode number and the link
count, which the hardlink rule of section 8.6 needs. The listing is diffed
against the parent snapshot's trees, exactly as `sync` does (section 16.4),
and only the changed files are read over the mount. It
removes the per-file round trips of the walk, which is what makes mode A slow on
a large tree. It needs no binary on the data host.

Mode C runs the binary on the data host and produces commit bundles
(section 16.11). It is Backlog, so it is not available today.

**Recommendation.** Use mode A. Move to mode B when the directory walk dominates
the commit time. Mode C is the answer when full metadata matters, or when
hardlinks, extended attributes or ACLs must survive, and it is the reason the
bundle format stays reserved.

---

## 17. Restore and the disc plan

### 17.1 The planner

Restore planning is minimum set cover, which is NP-hard.

**Inputs.** The planner takes the object set of the snapshot, the manifests
and filters that say which runs hold each object, and the **run table**
(section 12.5.2), which maps every `run_seq` to its `disc_seq` and
`disc_uuid`. The planner works in **discs**, not runs: every run of a disc is
available once the disc is in the drive, and run order does not imply disc
order, because `pack --disc` may add a run to an old disc after newer discs
exist. Without the run table the planner cannot run; it takes the table from
the cache or from the newest disc (section 17.9).

**Requirements.** Three things are normative: the plan accounts for every
needed object or fails up front (section 17.4); the plan is deterministic, so
the same inputs give the same plan (test 12); and the tie-breaks below are
applied in the stated order.

**Exact coverage and probable coverage.** `plan` reads nothing from a disc
beyond the catalog (section 19.14), and the catalog carries exact manifests
for only `manifest.history_depth` runs, default 8. For an older run the
planner has the run's filter, and a filter positive is a hint, not a proof
(section 12.8). Coverage is therefore of two kinds, and the plan says which
for every object:

| Kind | How the object was located | Strength |
|---|---|---|
| **exact** | A manifest record, from the cache or from a catalog copy. | The object is in that run at that LBA. |
| **probable** | A filter positive on a run whose manifest the planner does not have. | The object is very likely in that run. The false-positive rate is 2^-16 per run (section 12.2). |

An object for which every run's filter is negative is **missing**, and that is
a proof (section 12.9). A plan with any missing object fails up front.

A plan that holds a probable object is a valid plan, and the planner must not
refuse it. It marks the object probable, counts the probable objects per disc
and for the whole plan, and prints both (section 17.4). **The restore confirms
a probable object against that run's manifest when the disc is in the drive**,
which is the first thing it reads from that disc. A confirmation that fails
means the object is not on that disc: the restorer re-plans over the remaining
runs, adds the disc that a manifest or a filter then names, and reports the
added disc. That is the one case in which a restore visits a disc that the
printed plan did not name, and the plan says up front that it may happen by
printing a non-zero probable count.

**The cache removes the case.** When the local cache holds every run's
manifest, which is rebuild level 3 (section 13.2), every object is located
exactly and the probable count is 0. A plan with a probable count of 0 names
every disc the restore will ever ask for.

**Algorithm.** The three steps below are informative. Any algorithm that
meets the requirements conforms.

*Step 1: unique-element reduction.* If every run that holds an object lies
on one disc, that disc is in every valid plan. Add all such discs. Remove all
objects they cover. This usually leaves a very small residual problem, and
for a repository with no controlled duplication it leaves none at all.

*Step 2: greedy on the residual.*

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
option. Greedy returns at most `H(k) <= ln n + 1` times the optimum, and a
better approximation ratio is not achievable in polynomial time, so greedy
is the reference choice.

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

### 17.2 Disc-major order

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

### 17.3 Staging budget

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

### 17.4 The plan file

The plan is printed and persisted **before any read**. This is the equivalent of
Bacula's bootstrap file, which is the proven interaction for "which volumes do I
need".

The plan must fail up front when a required disc is missing from the inventory.
The operator must learn about a missing disc in second one, not in hour three.

The plan states its coverage as section 17.1 defines it: `objects_exact` and
`objects_probable` for the whole plan, and `objects_probable` per disc. A
non-zero probable count is the plan saying that a later disc may be added
during the restore. It is not a failure.

The plan is one JSON object with these fields:

| Field | Type | Meaning |
|---|---|---|
| `format` | string | Always `noahsark-restore-plan`. |
| `version` | integer | 1. |
| `repo_uuid` | string | Hyphenated lowercase uuid. |
| `snapshot` | string | Multihash text form of the snapshot. |
| `target` | string | The restore target path. |
| `include` | array of strings | The `--include` paths, absent when none. |
| `files`, `objects` | integer | Files and distinct objects to restore. |
| `objects_exact` | integer | Objects located through a manifest record (section 17.1). |
| `objects_probable` | integer | Objects located only through a filter positive. 0 means the plan names every disc the restore will ask for. |
| `bytes` | integer | Uncompressed bytes to restore. |
| `peak_staging_bytes` | integer | Predicted peak of `staging/restore/`. |
| `estimated_seconds` | integer | Section 17.5. |
| `switches` | integer | Number of disc changes. |
| `passes` | integer | 1, or more when the staging budget forces several passes (section 17.3). |
| `discs` | array of objects | In plan order. |
| `discs[].order` | integer | 0-based position. |
| `discs[].pass` | integer | 0-based pass. |
| `discs[].drive` | integer | 0-based drive under `--drives` above 1. |
| `discs[].disc_uuid` | string | The disc. |
| `discs[].disc_seq` | integer | 0-based. |
| `discs[].label`, `discs[].shelf` | string | On-disc label; shelf note (section 14.6), absent when none. |
| `discs[].health` | string | `healthy`, `degraded`, `critical`, `failed`, `unknown`. |
| `discs[].runs` | array of integers | The `run_seq` values to read on this disc. |
| `discs[].bytes_to_read`, `discs[].objects_to_read`, `discs[].files_completed` | integer | Counts. |
| `discs[].objects_probable` | integer | Of `objects_to_read`, how many were located only through a filter positive. Confirmed against the manifest when the disc is read (section 17.1). |
| `discs[].estimated_seconds` | integer | Section 17.5. |
| `missing_discs` | array of objects | `disc_uuid`, `disc_seq`, `label`, `objects` (integer). Non-empty means the plan failed. |
| `degraded_runs` | array of objects | `run_seq`, `disc_uuid`, `lost_ids` (array of multihash text). |

Informative example:

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
  "objects_exact": 262144,
  "objects_probable": 0,
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
      "objects_probable": 0,
      "files_completed": 41230,
      "estimated_seconds": 1134
    }
  ],
  "missing_discs": [],
  "degraded_runs": []
}
```

### 17.5 Time model

The formula below is the requirement. The table is the set of default
constants, which `restore.rate_mb_s` and `restore.switch_seconds` (section
20.12) replace; it is informative.

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

### 17.6 Disc detection

1. Read `/NOAHSARK/DISC.bin` and compare the `disc_uuid`. A
   file is format-independent, verifiable, and works on a loopback image.
2. The filesystem label is a hint for a human only. Labels are truncated and are
   not unique in practice.
3. Informative: on Linux, poll `CDROM_DRIVE_STATUS` about once a second. It
   returns `CDS_NO_DISC`, `CDS_TRAY_OPEN`, `CDS_DRIVE_NOT_READY`, or
   `CDS_DISC_OK`, and needs no daemon.
4. Informative: udev `change` events on `KERNEL=="sr*"` are a lower-latency
   path, but do not always report a removal. Any detection method that ends in
   step 5 conforms.
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

### 17.7 Multi-drive restore

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

### 17.8 Restore pipeline

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
      detect -> read this disc's manifests -> confirm every probable object
             -> read needed objects in LBA order -> staging/restore/
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

### 17.9 Cache-less restore

With no cache, the restorer reads the catalog from the newest disc first. That
gives every filter, the snapshot table, the ref table, the run table, the disc
directory, and the newest 8 manifests. The planner then works normally.

With the catalog alone the planner has exact manifests for the newest
`manifest.history_depth` runs and filters for the rest, so an older object is
located as probable and confirmed at read time (section 17.1). Warming the
cache to level 3 first removes every probable object and shortens the plan.

If the newest disc is lost, the fallback reads every available disc's manifest
and rebuilds the catalog. That is slow but always possible. Both paths must
exist and both must be tested.

---

## 18. File metadata and permissions

### 18.1 The field set

Metadata lives inline in the tree entry, not in a separate node object. Section
8.5 gives the byte layout. This section states the policy.

Mandatory fields, always in the fixed header: entry type, mode, uid, gid, size,
mtime, name.

Optional fields in the fixed header, each with an `ABSENT` flag: atime, ctime,
birth time.

Optional fields, each a TLV:

| Group | Fields |
|---|---|
| Names | user name, group name |
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
| ctime | 1 |
| atime, birth time | 2 |
| Extended attributes | 2 |
| POSIX ACL, access and default | 2 |
| Linux chattr flags | 2 |
| Windows attributes and security descriptor | 2 |
| NFSv4 ACL, BSD and macOS flags, NTFS alternate data streams | 3 |

A Phase 1 writer never emits a TLV that it does not implement. A Phase 1 reader
preserves and reports an unknown non-critical TLV, and does not apply it.

ctime is stored from Phase 1 because the quick check of section 16.2
compares it. No platform restores it (section 18.5); it is never applied,
only stored.

The reason for inline metadata is read amplification. A separate node object
would cost one object read per file instead of one per directory. On a medium
with 100 ms seeks, that is the difference between usable and unusable. A
separate node also saves nothing on a metadata-only change, because the parent
tree changes either way.

### 18.2 Encodings

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

### 18.3 Ownership policy

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

### 18.4 Restore order

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
   uses the directory handle that was retained from step 1, never a re-opened
   path. Informative: on Linux that is `futimens(dirfd)`.

The system calls named in this list are the Linux way to meet each step and
are informative. The order of the steps, and the reason for each, is the
requirement.

### 18.5 Cross-platform capability matrix

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

### 18.6 Failure policy

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

### 18.7 Non-root restore

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

### 18.8 Safety: names and symlinks

Two threat classes exist, and both have produced CVEs in comparable tools.

**Name traversal.** An entry named `..`, an absolute path, a Windows
drive-relative path such as `C:foo`, a UNC path, a name containing `/` or `\`, a
Windows reserved name, or NTFS stream syntax `name:stream`.

**Symlink redirection.** The archive holds `evil -> /etc`, then a later entry
`evil/passwd`. A naive restorer writes through the symlink and lands outside the
target. This works even when every individual name is harmless, and it is a
race even against a pre-check, because another process can plant the symlink
between the check and the open.

Defences, all mandatory. Each item states an invariant, which is the
requirement, and names the Linux system calls that meet it, which are
informative; another platform meets the same invariant with its own calls.
The invariants are: a name is validated before it is used; a path string is
never built and then opened; a symlink is never followed on the way to a
target; the check that a path component is safe and the use of that
component are one operation, so that nothing can change between them; a
symlink target is stored and restored as data.

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

### 18.9 Flags and exit codes

Section 19.15 lists the `restore` flags that switch each metadata field off.
`--metadata-strict` turns every metadata failure into a hard error.

Exit codes:

| Code | Meaning |
|---:|---|
| 0 | Everything applied. |
| 1 | Data restored, with metadata loss. |
| 2 | Data restore failed. |

A script can therefore tell the cases apart.

### 18.10 Unstable entries

Section 16.6 states when a writer sets the `UNSTABLE` flag. This section states
what a restore does with it.

`restore` writes the file normally. It prints one warning line per unstable
entry, naming the path. An unstable entry alone sets exit code 1, not 2, because
the file was restored. `restore --strict-unstable` refuses to write such an
entry and reports it as missing. The loss report carries the paths under the
field name `content_unstable`.

### 18.11 Loss report

The loss report is one JSON object with these fields:

| Field | Type | Meaning |
|---|---|---|
| `format` | string | Always `noahsark-metadata-report`. |
| `version` | integer | 1. |
| `snapshot` | string | Multihash text form. |
| `target` | string | The restore target. |
| `privileged` | boolean | Whether the restore ran with root or the needed capabilities. |
| `source_flags` | array of strings | The names of the set `source_flags` bits of the snapshot (section 8.7). |
| `summary` | array of objects | One per field kind. |
| `summary[].field` | string | `owner`, `group`, `mode`, `setuid_setgid`, `device_node`, `fifo_socket`, `symlink`, `hardlink_degraded`, `xattr.user`, `xattr.security`, `xattr.trusted`, `acl`, `flags`, `times`, `windows_attrs`, `windows_sd`, `ads`, `unknown_tlv`, `content_unstable`, `intermediate_directory`. |
| `summary[].count` | integer | Entries affected. |
| `summary[].reason` | string | An errno name, `UNSTABLE`, `UNSUPPORTED`, `SYNTHESIZED`, or `DROPPED`. |
| `summary[].examples` | array of strings | Up to 10 paths. |
| `entries` | array of objects | Present under `-v`: every `{path, field, reason, errno}` record. |
| `exit_code` | integer | 0, 1 or 2 (section 18.9). |

Informative example:

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
| `close` | 2 |
| `watch` | 3 |
| `consolidate` | 3 |
| `reindex` | 3 |

Every command accepts these global options:

| Option | Meaning |
|---|---|
| `--repo=PATH` | Repository root. Defaults to the discovered repository of section 3.7. |
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
              [--repo-uuid=UUID] [--next-run-seq=N] [--next-disc-seq=N]
              [--scan-discs]
```

Creates the repository of section 3.7: the config file, the staging store, the
state log, the lock file, and a new `repo_uuid`. Writes the defaults of
section 20. `--repo-uuid` reuses the uuid of an existing disc set, for a
repository that is recreated after the loss of the machine.

**Recovering the sequence numbers.** `run_seq` is never reused (section
12.5.2) and `disc_seq` is monotonic (section 9.1). A recreated repository must
therefore not restart either number, and the discs it can see may not hold the
highest values: the newest disc may be the one that was lost. `--repo-uuid`
consequently requires one of two forms, and refuses to create the repository
without one:

1. `--next-run-seq=N` and `--next-disc-seq=M`, both given. The operator states
   the numbers, from a label, a printed plan or a health report. `init`
   records them and uses them for the next `pack`.
2. `--scan-discs`. `init` asks for every available disc, reads each
   superblock and each newest run header, and takes `max(disc_seq) + 1` and
   `max(run_seq) + 1`. It then **warns** that a disc it did not see may hold a
   higher number, and that a reused number would make two different runs share
   one `run_seq` in the run table. It recommends a safety gap: the operator
   should add at least 100 to both numbers with form 1 unless every disc of
   the set was present in the scan.

`init` records the chosen values in the config as `repo.next_run_seq` and
`repo.next_disc_seq`, and `pack` takes the next number from there and
increments it. A fresh repository, with no `--repo-uuid`, starts at
`run_seq` 1 and `disc_seq` 0 and needs neither option.

Exit: 0 on success, 2 when the directory already holds a repository, or when
`--repo-uuid` was given with neither form above.

### 19.2 `noahsark commit` (Phase 1)

```
noahsark commit [SOURCE]... [-m MESSAGE] [--ref=NAME] [--checksum] [--force]
                [--exclude=PATTERN]... [--one-file-system=BOOL]
                [--source=PATH] [--source-root=PATH] [--from=PATH]
                [--copy-first] [--retry-unstable=N]
                [--out=DIR --catalog=DIR] [--source-type=TYPE]
```

Runs the commit flow of section 16.1. With no `SOURCE`, it uses the configured
source roots.

| Option | Meaning |
|---|---|
| `-m` | Commit message, stored as a snapshot TLV. |
| `--ref` | The ref to move. Default `LATEST`. |
| `--checksum` | Disable the quick check. Read and rehash every file. `--full-scan` is an alias. |
| `--force` | Write a snapshot object and move the ref even when the root tree equals the parent's root tree (section 16.1, step 5). |
| `--from` | Read changed files from this mirror directory. Unchanged paths come from the parent tree. Phase 2. |
| `--source` | Read from this path, typically a filesystem snapshot mount. |
| `--source-root` | The original path to record in the snapshot. Use it with `--source` or `--from`. |
| `--copy-first` | Copy each changed file raw into staging, then chunk it there. Shortens the busy window on the source disk. Phase 2. |
| `--out` | Write a commit bundle to this directory instead of into staging. Backlog. |
| `--catalog` | The exported catalog to deduplicate against, with `--out`. Backlog. |
| `--source-type` | Override the detected source type recorded in the snapshot. |
| `--retry-unstable` | Re-read an unstable file up to N times before the rule of section 16.6 applies. Default 1. |
| `--exclude` | Skip matching paths. The patterns are recorded in the snapshot. |
| `--one-file-system` | Do not cross a mount point. Default true. |

Exit: 0 on success, also when the tree is unchanged and no snapshot was
written; 1 when some files could not be read or were unstable; 2 on failure
or when the repository lock is held.

The report lists every unstable path and says which branch was taken: `parent`
when the parent entry was reused, or `flagged` when new content was stored with
the `UNSTABLE` flag.

### 19.3 `noahsark sync` (Phase 2)

```
noahsark sync SOURCE [--mirror=PATH] [--ref=NAME] [--dry-run]
             [--list-only] [--rsync-arg=ARG]...
```

Pulls the change set of `SOURCE` into the mirror directory (section 16.4).
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

### 19.4 `noahsark catalog export` (Backlog)

```
noahsark catalog export DIR [--manifests=N] [--json]
```

Writes the current catalog to `DIR` as plain files, for a source machine that
will run `commit --out` (section 16.11). `--manifests` sets how many recent
manifests to include, as section 12.7 defines them. Default
`manifest.history_depth`.

Exit: 0 on success, 2 on failure.

### 19.5 `noahsark import` (Backlog)

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

### 19.6 `noahsark watch` (Phase 3)

```
noahsark watch [SOURCE]... [--log=PATH]
```

Runs a change-recording daemon (section 16.9). It records changed paths and
never commits. It does not exist in Phase 1 or Phase 2.

Exit: 0 on a clean stop, 2 on failure.

### 19.7 `noahsark pack` (Phase 1)

```
noahsark pack [--disc=UUID] [--media=NAME|ID]
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
| `--media` | Media type for a new disc: any name, or the numeric id, from the media type registry of section 4.6, for example `BD-R SL 25`, `M-DISC BD DL 50`, `BD-RE SL 25`, `Mini BD SL 8 cm`, `image`, or `8`. A space in a name may be written as `-`. Default `disc.media`. |
| `--capacity` | Forced capacity for a new disc (section 9.12). A plain integer is bytes; an integer followed by `KiB`, `MiB` or `GiB` is scaled by 2^10, 2^20 or 2^30. It must be at or below the reported capacity. It cannot be changed later. |
| `--reserve` | `disc.force_reserve` for this disc. Replaces the computed reserve. |
| `--extra-reserve` | `disc.extra_reserve` for this disc. Added to the computed reserve. |
| `--label` | Human label, printed on the disc. |
| `--preset` | Locality preset for this run. |
| `--now` | Ignore `disc.min_fill` and `disc.max_wait`. |
| `--close` | Seal the disc: `spare:none` and `-dvd-compat`, no POW, full capacity, no later append. Permanent, recorded in the superblock field `sealed`. Profile 0 only. |
| `--dry-run` | Print the capacity budget of section 10.11 and stop. |

Exit: 0 on success, 1 when the run is smaller than requested, 2 on failure,
4 when a forced capacity conflicts with the recorded value.

### 19.8 `noahsark append` (Phase 2)

```
noahsark append --disc=UUID [pack options]
```

A convenience form of `pack --disc=UUID`. It refuses a disc that is closed, and
it refuses a disc marked `append-raw-only` unless `--raw` is given.

Exit: as `pack`. 4 when the disc is closed.

### 19.9 `noahsark burn` (Phase 1)

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

### 19.10 `noahsark close` (Phase 2)

```
noahsark close --disc=UUID [--parity] [--print | --exec] [--yes]
```

Closes an open disc by appending a closing run (section 9.9.2): a fresh
catalog copy, the tail anchors when they are missing, an optional disc-wide
parity run, and the closing write with `-dvd-compat`. It is an append, so it
is Phase 2. The Phase 1 way to close a disc is `pack --close` at the disc's
only write.

A disc is never closed automatically under the default `disc.close_policy` of
`never` (section 9.9).

| Option | Meaning |
|---|---|
| `--parity` | Add a disc-wide parity run over all data columns of all runs. Phase 3. |

Exit: 0 on success, 2 on failure, 4 when the disc is already closed or sealed.

### 19.11 `noahsark verify` (Phase 1)

```
noahsark verify [--disc=UUID] [--run=SEQ] [--image=PATH [--mapfile=PATH]]
                [--level=catalog|connectivity|integrity]
                [--heal] [--drive=PATH] [--report=FILE]
```

Reads a disc or an image back and checks it. On success it moves the run's
objects to CLEAN.

| Option | Meaning |
|---|---|
| `--image` | Verify an image file instead of a drive. |
| `--mapfile` | The ddrescue mapfile that produced `--image` (sections 11.8 and C.5). Every sector that the map does not mark `+` (rescued) is an erasure. Without it every sector of the image is treated as readable, and only the digests and the content ids find damage. Only valid with `--image`. |
| `--level=catalog` | Cache consistency only. Reads no disc. |
| `--level=connectivity` | Reads trees, chunklists, snapshots, filters and manifests. Under 1 percent of the bytes. |
| `--level=integrity` | Reads every sector and every object. The default for a disc. |
| `--heal` | Repair what is repairable, in the order of section 11.7. Write recovered objects into `staging/heal/`. |
| `--drive` | Use this drive. Use a second drive model for the 24-hour check. |
| `--report` | Write the health record as JSON. |

Exit: 0 clean, 1 repaired or degraded, 2 unrecoverable loss, 3 disc missing.

### 19.12 `noahsark scrub` (Phase 1)

```
noahsark scrub [--due] [--all] [--disc=UUID]... [--drive=PATH]
```

Runs `verify --level=integrity` over the discs that the schedule of section 11.8
selects. `--due` selects only overdue discs. `--all` selects every disc.

Exit: as `verify`, aggregated over the discs.

### 19.13 `noahsark health` (Phase 1)

```
noahsark health [--disc=UUID] [--library] [--object=ID] [--json]
```

Prints the health report of section 11.9: RS margin, status, spare area
remaining, forced capacity, open or closed, scrub dates, and the consolidation
trigger metrics of section 15.8.

Exit: 0 when every disc is healthy, 1 when any disc is degraded, 2 when any disc
is failed.

### 19.14 `noahsark plan` (Phase 1)

```
noahsark plan SNAPSHOT [--target=PATH] [--out=FILE] [--drives=N]
              [--staging-budget=BYTES] [--score=bytes|objects]
```

Computes the restore plan of section 17.4 and prints it. It writes the JSON plan
to `--out`. It reads nothing from a disc beyond the catalog, so an object that
lies in a run older than `manifest.history_depth` may be located only by a
filter positive. The plan marks such an object **probable** and prints the
count (section 17.1). `restore` confirms every probable object against the
run's manifest when the disc is in the drive, and adds a disc when a
confirmation fails.

Exit: 0 when the plan accounts for every object, 1 when the plan holds at
least one probable object, 3 when a required disc is missing from the
inventory or a filter negative proves an object absent from every run.

### 19.15 `noahsark restore` (Phase 1)

```
noahsark restore SNAPSHOT TARGET [--plan=FILE] [--include=PATH]...
                 [--drives=N] [--staging-budget=BYTES] [--interactive]
                 [--no-eject] [--overwrite]
                 [--no-owner] [--numeric-owner] [--no-xattr]
                 [--xattr-exclude=PATTERN] [--no-acl] [--no-flags]
                 [--no-times] [--no-hardlinks] [--metadata-strict]
                 [--report=FILE] [--report-replay=FILE] [--strict-unstable]
```

Runs the restore pipeline of section 17.8. `--plan` resumes a persisted plan.

An entry with the `UNSTABLE` flag is restored, and a warning names the path.
`--strict-unstable` refuses to write such an entry and reports it as missing
(section 18.10).

An intermediate directory between `TARGET` and a source root that no tree
entry describes is created with mode 0700, the invoking user's ownership and
the restore time, and is reported (section 16.5).

Exit: 0 all applied, 1 data restored with metadata loss, 2 data restore failed,
3 a required disc is missing.

### 19.16 `noahsark rebuild-cache` (Phase 1)

```
noahsark rebuild-cache [--level=1|2|3] [--snapshot=ID] [--from-disc]
```

Rebuilds the local cache from discs, as in section 13.2. Level 1 needs the
newest disc alone. Level 3 asks for every disc, newest first, and reads only
manifests.

Exit: 0 on success, 1 when the rebuild is partial, 3 when a needed disc is
missing.

### 19.17 `noahsark consolidate` (Phase 3)

```
noahsark consolidate [--snapshot=ID] [--dry-run] [--media=TYPE]
```

Packs a fresh, self-contained disc set for a snapshot, with no reference to
older runs. `--dry-run` reports the disc count and the media cost.

Exit: 0 on success, 2 on failure.

### 19.18 `noahsark reindex` (Phase 3)

```
noahsark reindex --to=blake3|sha256 [--disc=UUID]... [--all]
```

Builds the optional cross-algorithm side table of section 5.7. It reads the data
areas of the named discs once.

Exit: 0 on success, 1 when some discs were not available, 2 on failure.

### 19.19 `noahsark gc` (Phase 1)

```
noahsark gc [--dry-run] [--force-after=DURATION]
```

Deletes staging objects that are GC-ELIGIBLE, under the rules of section 14.4.
`--force-after` shortens the retention for this run only and requires an
interactive confirmation.

Exit: 0 on success, 1 when nothing was eligible, 2 on failure.

### 19.20 `noahsark ls` (Phase 1)

```
noahsark ls SNAPSHOT [PATH] [--long] [--recursive] [--json] [--unstable-only]
```

Lists a snapshot's tree. `--long` prints mode, owner, size and mtime. It reads
tree objects only, never chunks.

An entry with the `UNSTABLE` flag is marked with a `!` in the first column, and
with `"unstable": true` under `--json`. `--unstable-only` lists just those
entries.

Exit: 0 on success, 3 when a needed tree object is unavailable.

### 19.21 `noahsark log` (Phase 1)

```
noahsark log [REF|SNAPSHOT] [--limit=N] [--json]
```

Walks the snapshot chain by parent pointer and prints the history. It reads the
snapshot table from the cache or from the newest disc.

Exit: 0 on success.

### 19.22 `noahsark disc` (Phase 1)

```
noahsark disc list [--json]
noahsark disc label UUID TEXT
noahsark disc mark-degraded UUID [--reason=TEXT]
```

`list` prints every disc: uuid, seq, label, shelf note, media type, filesystem
profile, reported capacity, forced capacity, used, run count, open or closed,
health, RS margin, spare remaining, and last verify date.

`label` appends a record to `<repo>/notes.bin` (section 14.6) with a local
display label and the shelf note for a disc. The on-disc label is written at burn time and
never changes, and the cache holds only copies of disc data (section 13.1),
so neither is touched. `list` and the restore plan show the shelf note next
to the on-disc label.

`mark-degraded` records a manual health downgrade, for example after a physical
inspection.

Exit: 0 on success, 3 when the uuid is unknown.

### 19.23 `noahsark image` (Phase 1)

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
| 2 | `commit.copy_first`, `sync.*`, `fs.append_variant`, `disc.min_spare_ratio`, `disc.close_policy = when_full`, `disc.allow_raw_append`, `metadata.atime`, `metadata.btime`, `metadata.xattr`, `metadata.acl`, `metadata.windows` |
| 3 | `fec.disc_close_parity`, `fec.group_size`, `consolidate.*`, `restore.drives` above 1, `watch.*`, `mirror.*`, `reindex.*` |
| Backlog | `commitbundle.*` |

The config file lives at `<repo>/config`. It is a plain text key-value file with
one `key = value` pair per line, `#` for a comment, and UTF-8 encoding. A CLI
option always overrides the file.

A key marked **tuning** changes speed, memory or waiting time only. It has no
effect on any byte that reaches a disc, and its value is not recorded in any
structure.

### 20.1 Identity and format

| Key | Default | Meaning |
|---|---|---|
| `repo.uuid` | generated | Repository uuid. Never changed. |
| `repo.next_run_seq` | 1 | The `run_seq` that the next `pack` assigns. 1-based, never reused. `init --repo-uuid` sets it (section 19.1). |
| `repo.next_disc_seq` | 0 | The `disc_seq` that the next new disc takes. 0-based. `init --repo-uuid` sets it. |
| `format.version_major` | 1 | On-disc format major version. |
| `format.version_minor` | 0 | On-disc format minor version. |

### 20.2 Hashing and chunking

| Key | Default | Meaning |
|---|---|---|
| `hash.current` | `blake3` | Algorithm for new objects. `blake3` or `sha256`. |
| `chunker.profile` | `P4` | Chunker profile: `P3`, `P4`, `P5`. |
| `chunker.gear_table_id` | 1 | Frozen Gear table version. Never change it under a profile name. |
| `bundle.threshold` | 1 MiB | A chunk whose uncompressed payload is below this size goes into a bundle (section 6.5). |
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
| `fs.fanout_levels` | 1 | Object fan-out depth. 2 is allowed under profile 0 and profile 1 only (section 5.5). |
| `udf.revision` | `2.01` | UDF revision for profile 1. |
| `disc.fill_ratio` | 0.95 | Fraction of the forced capacity that data may use. |
| `disc.min_fill` | 0.90 | `pack` triggers when staging fills this fraction of a disc's data budget. |
| `disc.max_wait` | 30 days | `pack` triggers when the oldest STAGED object is older than this, whatever the fill. |
| `disc.force_capacity` | unset | Cap the usable capacity of a disc below the reported value. Bytes or GiB. Recorded in the superblock. |
| `disc.force_reserve` | unset | Replace the computed reserve. Bytes or a percentage. |
| `disc.extra_reserve` | unset | Add to the computed reserve. Bytes or a percentage. |
| `disc.expected_runs` | 1 under profile 0, 32 under profiles 1 and 2 | Expected number of future runs, used by the reserve estimator of section 10.11.2. A profile 0 disc holds one run, so reserving for 32 would waste about 0.5 percent of the disc. |
| `disc.spare` | `min` | Spare area size at format time: `min` or `default`. `default` reserves about 256 MB and gives more appends. It is chosen at format time, which is the Phase 1 first burn (section 9.11), so the key is Phase 1 even though only a Phase 2 append benefits from `default`. |
| `disc.spare_reserve_bytes` | 512 MiB | Reserve for POW spare and filesystem metadata on every `spare:min` disc, which is every disc except a sealed one (section 10.11). |
| `disc.media` | `BD-R SL 25` | Default media type for a new disc. Any name or id from the media type registry of section 4.6. |
| `disc.min_spare_ratio` | 0.20 | Warn and recommend no further appends below this remaining spare fraction. |
| `disc.close_policy` | `never` | `never` or `always` (Phase 1), `when_full` (Phase 2). See section 9.9. |
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
| `burn.reload_seconds` | 10 | Tuning. Wait after the reload before the verification read. |

### 20.6 FEC

| Key | Default | Meaning |
|---|---|---|
| `fec.scheme` | `rs255-gf8` | FEC scheme registry. Only value 1 exists in version 1. |
| `fec.k` | - | Reserved for a later format version. Version 1 fixes `k = 231` (section 11.2). A version 1 build refuses the key. |
| `fec.m` | - | Reserved for a later format version. Version 1 fixes `m = 23`. A version 1 build refuses the key. |
| `fec.band_stripes` | 2048 | Tuning. Stripes per encoding band, for a working set near 1 GiB (section 11.10). |
| `fec.disc_close_parity` | false | Add a disc-wide parity run at close. |
| `fec.group_size` | 0 | Discs per cross-disc parity group. 0 disables it. Use 10 with one parity disc, or 20 with two. |
| `fec.reburn_margin` | 0.50 | Re-burn a disc below this RS margin. |

### 20.7 Filters and manifests

| Key | Default | Meaning |
|---|---|---|
| `filter.type` | `binaryfuse16` | Run filter type. |
| `manifest.fanout_bits` | 8 | 8 or 16. A writer must use 16 above 1,000,000 objects in one run (section 12.3). |
| `manifest.history_depth` | 8 | How many earlier runs' manifests every run carries. |
| `filter.rollup_threshold` | 64 MiB | Merge old filters into super filters above this bundle size. Reserved. |
| `catalog.max_bytes` | 512 MiB | Cap on one run's catalog (section 12.7.1). Only the manifest history is dropped to meet it. |
| `catalog.table_reserve_bytes` | 614400 | Planning reserve, in bytes, for the snapshot objects and the four tables of one catalog copy; the `table_bytes` input of `catalog_growth` (section 10.11). |
| `catalog.snapobj_pack_threshold` | 1000 | Above this snapshot count, pack the replicated snapshot objects into one container file. |

### 20.8 Sources and excludes

| Key | Default | Meaning |
|---|---|---|
| `sources.root` | unset, repeatable | An absolute source root. At least one is required. |
| `sources.exclude` | unset, repeatable | An exclude pattern in the language of section 16.5.1. |
| `sources.ignore_file` | `.noahsarkignore` | Per-directory exclude file name. Empty disables it. |
| `sources.one_file_system` | true | Do not cross a mount point. |
| `sources.follow_symlinks` | false | Never true in Phase 1. A symlink is stored as a symlink. |
| `sources.skip_unreadable` | true | Skip and report an unreadable file. Exit code 1. |
| `sources.read_only` | true | Read-only. A source is never written. The key is reported, never set. |
| `source.quick_check` | `size_mtime_ctime` local, `size_mtime` remote | Fields compared against the parent tree entry. |
| `source.mtime_slack` | 0 local, 2 s remote | An mtime difference below this counts as equal in the quick check. A local root uses 0 unless the key is set. |
| `source.checksum_every` | 30 days | Force a full rehash on a remote root after this interval. 0 disables it. |
| `source.type` | auto | Override the detected source type: `local`, `snapshot`, `nfs`, `smb`. |
| `source.allow_smb` | true | Allow an SMB mount as a source root. It is never allowed for staging. |
| `commit.checksum` | false | Always rehash. Equivalent to `--checksum` on every commit. The quick check itself is selected by `source.quick_check` above. |
| `commit.restat_after_read` | true | In-flight change detection (section 16.6). Never set it to false on a live source. |
| `commit.retry_unstable` | 1 | Re-reads of an unstable file before it is skipped. |
| `commit.copy_first` | false | Copy changed files into staging before chunking. Phase 2. |
| `repo.lock_timeout` | 0 | Seconds to wait for the repository lock (section 14.5). 0 means fail at once. |
| `sync.rsync_path` | `rsync` | Path to the `rsync` binary. Phase 2. |
| `sync.rsync_args` | `-aHAX --numeric-ids` | Recommended option set for `sync`. `--files-from` is always added. Phase 2. |
| `sync.mirror_dir` | `<staging>/mirror` | Mirror root on the staging disk. Phase 2. |
| `sync.clear_after_commit` | true | Delete the mirror contents once the objects reach STAGED. Phase 2. |
| `sync.remote_stat_command` | built-in | The command used to obtain a stat listing from a remote source. Phase 2. |
| `label.template` | `<repo-short-name>-<seq:04d> <YYYY-MM>` | Physical label text, mirrored into the disc directory. `<seq:04d>` is `disc_seq`, 0-based, zero-padded to four digits; `<YYYY-MM>` is the first write month in local time. |
| `repo.short_name` | from `repo.uuid` | Short name used in the label. Up to 16 characters. |

### 20.9 Locality and packing

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

### 20.10 Metadata

| Key | Default | Meaning |
|---|---|---|
| `metadata.user_group_names` | true | Store user and group names beside the numeric ids. |
| `metadata.ctime` | true | Store ctime when the source reports it. The quick check of section 16.2 compares it. Setting it to false selects the `size_mtime` quick check. Phase 1. |
| `metadata.atime` | false | Storing atime makes almost every tree change on every commit. Phase 2. |
| `metadata.btime` | false | Store birth time when the source reports it. Linux cannot set it on restore, so it is informational there. Phase 2. |
| `metadata.xattr` | false | Phase 2. |
| `metadata.acl` | false | Phase 2. |
| `metadata.windows` | false | Windows attributes and security descriptors. Phase 2. |
| `restore.owner_policy` | `auto` | `auto`, `numeric`, `name`, `none`. See section 18.3. |

### 20.11 Staging and cache

| Key | Default | Meaning |
|---|---|---|
| `staging.dir` | `<repo>/staging` | Staging store location. |
| `staging.retain_after_clean` | 7 days | Retention before an object becomes GC-eligible. |
| `staging.budget_bytes` | unset | Warn when staging exceeds this size. |
| `staging.allow_unsafe_fs` | false | Allow staging on a filesystem that fails the startup check, such as SMB. Recorded in the state log. |
| `commitbundle.dir` | `<staging>/commitbundles` | Where `import` unpacks and where `commit --out` writes by default. Backlog. |
| `commitbundle.keep_after_import` | false | Keep the bundle directory after a successful import. Backlog. |
| `commitbundle.catalog_max_age` | 30 days | Warn when `commit --out` uses an exported catalog older than this. Backlog. |
| `cache.dir` | see section 13.1 | Local cache location. |
| `cache.format_version` | 1 | Delete and rebuild on a mismatch. |

### 20.12 Restore

| Key | Default | Meaning |
|---|---|---|
| `restore.staging_budget` | 16 GiB | Peak staging allowed. The planner falls back to multi-pass above it. |
| `restore.score` | `bytes` | Greedy score: `bytes` or `objects`. |
| `restore.drives` | 1 | Number of drives to plan for. |
| `restore.rate_mb_s` | 20 | Effective read rate for the time model. |
| `restore.switch_seconds` | 60 | Fixed cost per disc switch. |
| `restore.interactive` | false | Prompt on every disc, not only on a mismatch. |
| `restore.eject` | true | Eject after each disc. |

### 20.13 Scrub

| Key | Default | Meaning |
|---|---|---|
| `scrub.first_check_hours` | 24 | First full verify after burning, on a second drive. |
| `scrub.schedule` | `3m,12m,then 12m to 5y,then 6m` | The schedule of section 11.8, in the grammar below. |
| `scrub.degraded_interval` | 3 months | Interval for a degraded disc. |
| `scrub.max_disc_age` | 10 years | Proactive re-burn age. |

`scrub.schedule` grammar, in EBNF. Ages are measured from the burn time of
the disc. Spaces are allowed around every token.

```
schedule = phase { "," phase } ;
phase    = point | interval ;
point    = duration ;                            (* one scrub at this age *)
interval = "then" duration [ "to" duration ] ;   (* every d1 until age d2 *)
duration = digits unit ;
digits   = digit { digit } ;
unit     = "d" | "w" | "m" | "y" ;               (* days, weeks, months, years *)
```

A `point` schedules one scrub at that age. An `interval` schedules a scrub
every `d1` from the end of the previous phase until age `d2`; an interval
with no `to` runs until `scrub.max_disc_age`. Phases are in ascending age
order; a schedule that is not is refused. A month is 30 days and a year is
365 days for this purpose. The default reads: at 3 months, at 12 months,
then every 12 months until age 5 years, then every 6 months.

---

## 21. Format evolution and compatibility

### 21.1 The two mechanisms

Every change uses one of two mechanisms.

| Mechanism | When | Old reader | New reader |
|---|---|---|---|
| A feature bit | A structure gains an item. | Ignores an `optional_feat` bit. Refuses a `required_feat` bit. | Uses the item. |
| A version bump | A structure changes shape. | Refuses an unknown `version_major`. Ignores an unknown `version_minor`; for the three structures that carry `header_len` (section 4.7) it skips `header_len - known`. | Uses the new shape. |

A registry id is never reused and never renumbered. That is what lets a 2050
reader interpret a 2027 disc.

### 21.2 Change matrix

| Change | Mechanism | Old reader does | New reader does |
|---|---|---|---|
| **New hash algorithm** | New id in the hash registry. `hash.current` moves. | Refuses an object whose `hash_algo` it does not know, and says the code. Old discs stay readable. | Reads both. Writes the new default. Dedup across the boundary is zero unless the reindex table exists. |
| **Chunker profile change** | New id in the chunker registry. | Unaffected. A reader never needs the profile. | Uses the new profile for new runs. Old runs keep theirs. Dedup across the boundary falls. |
| **Gear table change** | New `gear_table_id` **and** a new profile name. | Unaffected. | Must never reuse an existing profile name with a different table. That would silently split the object space. |
| **New compression algorithm** | New id in the compression registry, plus `required_feat` bit `FEAT_COMPRESSION` semantics. | Refuses an object whose `compression` it does not know. The object is unreadable, not misread. | Reads it. |
| **FEC parameter change (k, m)** | A new `version_major` of the run header and the layout table. Both already record `k` and `m`. | Refuses the run for repair, because version 1 fixes `k = 231`, `m = 23` and it accepts no other pair. Still reads the objects through the filesystem. | Reads the recorded pair. |
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
   version first, and the `header_len` where the structure carries one.
6. Set the feature bits of section 4.4 exactly as its table states, and no
   other.

### 21.4 Rules for a reader

1. Check the magic. Refuse on a mismatch.
2. Check `version_major`. Refuse on an unknown value, and print the value.
3. For the common object header, the tree header and the tree entry, read
   `header_len` and skip the excess. Never assume the compiled size. For every
   other structure, use the fixed size of its `version_major`; it has no
   `header_len` (section 4.7).
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

### 21.6 Reader and writer conformance

The on-disc format has two roles. A program may claim one or both.

**A conforming reader** of format major 1:

1. reads every structure of this document at `version_major` 1 and any
   `version_minor`, by the rules of section 21.4;
2. reads objects under both hash algorithms of section 4.6, and under
   compression ids 0 and 1; it may refuse id 2 (`lz4`), and must then name
   the id;
3. reads a disc of profile 0 and of profile 1, which share one filesystem; it
   may refuse profile 2 and must then name the profile;
4. finds every object through the filesystem, the run headers, the manifests,
   the filters and the catalog, with no local cache and no other disc than
   the ones the plan names;
5. verifies every CRC, every hash of section 4.9 and every content id before
   it uses the bytes;
6. repairs a run with `k = 231`, `m = 23`, or says that it cannot repair;
7. refuses an unknown `version_major`, an unknown `required_feat` bit, an
   unknown registry id in a field it must interpret, and a critical TLV it
   does not know, and says which.

**A conforming writer** of format major 1:

1. writes every structure exactly as its byte-offset table states, with
   `version_major` 1, `version_minor` 0, reserved fields zero, and the
   feature bits of section 4.4 and no other;
2. writes BLAKE3-256 or SHA-256 content ids over the uncompressed payload,
   FastCDC cut points by section 6.2, and bundles by section 8.3;
3. writes every run with `k = 231`, `m = 23`, the checksum column of section
   11.4, `m + 2` header copies, the fill order of section 10.5 and the
   catalog copies of section 12.7;
4. writes every byte as an ordinary file under `/NOAHSARK/` and never
   rewrites a burned run;
5. writes only a disc filesystem profile that it implements, and never a
   reserved id, bit or value.

Section 23.7 is the checklist for a Phase 1 build, which is both a reader and
a writer.

### 21.7 Reader and writer interop matrix

The matrix states, for every pair of writer and reader, what the reader does.
It is the test matrix for interop: each cell is one CI case, built by writing
an image with one build and reading it with another. `Y` means full use.
`~` means partial use, with the loss named. `N` means a clean refusal that
names the reason. An empty refusal, a silent skip and a misread are all
defects.

**By phase**, at format major 1. Every phase writes the same structures
(section 2.5), so the differences are the profile and the optional items.

| Writer | Reader Phase 1 | Reader Phase 2 | Reader Phase 3 |
|---|---|---|---|
| Phase 1, profile 0 | Y | Y | Y |
| Phase 2, profile 0 or 1, one run | Y | Y | Y |
| Phase 2, profile 1, appended | Y. Every run is read through the filesystem. | Y | Y |
| Phase 2, profile 1, raw append | ~ Reads the runs the filesystem shows. Reports the raw runs as unreachable, because the recovery read of section 9.7.1 is Phase 3. | ~ same | Y |
| Phase 3, profile 2 | N. Refuses `fs_profile` 2 and names the id (section 21.5). | N, same | Y |
| Phase 3, disc-close parity run | ~ Reads every object. Reports `run_kind` 3 and `OPT_DISC_PARITY` as not supported. | ~ same | Y |
| Phase 3, cross-disc parity group | ~ Reads every object. Reports the group as not supported. | ~ same | Y |

**By format version**, under the rules of section 21.1.

| Writer | Reader of major 1 | Reader of a later major |
|---|---|---|
| Major 1, minor 0 | Y | Y. It reads the fixed sizes of major 1. |
| Major 1, minor above 0, no new `required_feat` bit | Y. It skips `header_len - known` in the three structures that carry `header_len`, and ignores fields it does not know elsewhere (section 4.7). | Y |
| Major 1 with an unknown `required_feat` bit | N. Refuses the structure and prints the bit number. | Y when the bit is known to it. |
| Major 1 with an unknown `optional_feat` bit | Y, ignoring the bit. | Y |
| A later major | N. Refuses and prints `version_major`. | Y |

**By algorithm and registry id.**

| Written with | Reader that knows it | Reader that does not |
|---|---|---|
| BLAKE3-256 or SHA-256 ids | Y. Both are mandatory for a conforming reader (section 21.6). | Not possible at major 1. |
| A new hash algorithm id | Y | N. Refuses the object and names the multicodec code. Older discs stay readable. |
| compression 0 or 1 | Y | Not possible at major 1. |
| compression 2, `lz4` | Y | N, permitted. Refuses the object and names the id (section 21.6). |
| A new compression id | Y | N. Refuses the object and names the id. |
| A new chunker profile | Y | Y. A reader never needs the profile. |
| A new manifest TOC chunk id | Y | Y, skipping the chunk. |
| `FEAT_FAN16` | Y | N. Refuses the manifest. |
| A non-critical unknown tree TLV | Y | ~ Preserves it on copy, reports it on restore, does not apply it. |
| A critical unknown tree TLV, 0x8000 to 0xBFFF | Y | N. Refuses the entry and names the type. |
| `k`, `m` other than 231, 23 | Y | N for repair; still reads every object through the filesystem. |
| A new FEC scheme id | Y | N for repair; still reads every object through the filesystem. |

The rule that every `N` cell shares: a refusal is loud, names the field and
the value, and never touches the bytes it refused. The rule that every `~`
cell shares: the data is read in full, and the loss is in the report.

---

## 22. Failure modes and recovery matrix

| # | Failure | Detected by | Immediate effect | Recovery | Data loss |
|---:|---|---|---|---|---|
| 1 | A burn fails midway | Non-zero exit from the burner, or a short read-back | The run is incomplete | Objects return to STAGED. Pack a new run, with a new `run_seq`, onto a fresh disc. | None |
| 2 | Verify finds bad sectors | ddrescue mapfile, checksum column | The run is degraded | RS decode inside the run. | None while erasures are at or below `m` per stripe |
| 3 | Erasures exceed `m` in a stripe | RS decode refuses | Some objects are unreadable on this disc | Content-addressed re-fetch, then cross-disc parity, then mirror, then the source. | None when another copy exists |
| 4 | A whole disc is lost or destroyed | The disc is missing from the inventory | Every object unique to it is missing | Cross-disc parity group, mirror, or the source. Otherwise the loss is enumerable from the catalog. | Objects unique to that disc |
| 5 | The newest disc is lost | The disc directory names a `disc_seq` that is absent | The catalog entry point is gone | Read every other disc's catalog. The previous disc carries the state as of its own burn. | Only the objects unique to the newest disc |
| 6 | The local cache is lost | The cache directory is absent | Dedup and planning are slower | Rebuild from the newest disc, level 1. Then level 3 lazily. | None |
| 7 | The cache is wrong (uuid mismatch) | `disc_uuid` does not match the recorded `disc_seq` | The cache may give wrong answers | Refuse the cache. Rebuild. | None |
| 8 | The state log is truncated by a crash | A record CRC fails | Some objects have an unknown state | Replay up to the bad record. Re-scan staging. Objects with no record are treated as STAGED. | None |
| 9 | An object moved after an append | The LBA read-back differs from the layout table | The layout table would be wrong | Abort the append. Keep objects PACKED. Re-burn as a new run with a new `run_seq`. | None |
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
| 18 | Symlink redirection attack: a planted symlink does not escape | The invariants of section 18.8 hold |
| 19 | Hardlink group: restoring one member gives a correct file | No dangling links |
| 20 | State log replay after a truncated write | A crash does not corrupt the store |
| 21 | GC refuses to delete an object that is not CLEAN | The safety rule holds |
| 22 | Burn plan refusal on a bad CRC or a misaligned seek | The plan is validated |
| 23 | Forced capacity: the image, the budget and the FEC layout all shrink | The override reaches every consumer |
| 24 | Reserve estimator: with the stated inputs, `catalog_growth` is 58,080 sectors at 25 GB and 201,920 sectors at 100 GB, the printed budget matches every line of the worked examples of sections 10.11.3 and 10.11.4, `reserve_computed` equals `capacity_forced - data_budget`, `fill_limit_sectors` equals both `capacity_forced - safety_margin - spare_area` and `data_budget + superblock_and_headers + catalog_growth + fec_overhead + alignment_padding + parity_headers`, `data_budget` is a whole number of stripes of `k` data sectors, and the same disc under profile 0 gives `data_budget` 10,276,497 | The arithmetic is right, and the two identities of section 10.11.1 hold |
| 25 | Connectivity check reports a missing object with no disc mounted | A filter negative is a proof |
| 26 | Quick check: a file whose size, mtime and ctime match is never read | The commit cost is proportional to the change set |
| 27 | In-flight change: a file rewritten during the read gets the parent entry when one exists, and the `UNSTABLE` flag when it does not | No snapshot ever holds torn content without a flag |
| 28 | Mirror mode: `sync` transfers only the changed paths, and the snapshot equals the direct-mode snapshot | The mirror path never leaks into the archive |
| 29 | BURNED objects stay in staging until `verify` succeeds | The mandatory verify transition |
| 30 | Catalog cap: the manifest history shrinks and the snapshot objects stay | The copy priority holds |
| 31 | `UNSTABLE` round trip: the flag survives write, read, restore and `ls` | The flag is part of the format, not a log line |
| 32 | `pack --close` sealed path: no format step, full capacity, `-dvd-compat` in the plan | The Phase 1 sealed path is exercised |
| 33 | Profile 0 open image: the volume mounts with the anchor at LBA 256 alone | The tail-anchor limitation is real and tolerated |
| 34 | `pack --close` image: the plan holds one write step whose `byte_len` equals the full-size image, and `udfinfo` finds three `type=ANCHOR` lines in that image | A sealed disc gets its tail anchors from the same growisofs call |
| 35 | Run table: after three runs on two discs, with the third run appended to the first disc, the planner names the right disc for every run | Run order does not imply disc order |
| 36 | Dedup confirmation: a filter hit with no manifest available writes the chunk again and logs it in `pending-confirm`; a confirmed hit drops it | Data is never dropped on a filter alone |
| 37 | Capping: `max_source_runs` bounds the referenced runs per segment, the per-file caps outrank it, `disc_budget` stops rewriting, and the `standalone` preset produces no cross-run reference | The knobs apply in the rank of section 15.4 |
| 38 | Unchanged commit: a second commit of an unchanged tree writes no snapshot object and moves no ref; `--force` writes one | Commit is idempotent |
| 39 | Local ref log: the parent of a commit resolves from the local log before the cache, and a re-created repository recovers every ref from the discs | The pending chain is authoritative until CLEAN |
| 40 | RS worked example: the `k = 3`, `m = 2` bytes of section 11.2.1 encode and decode as printed | The field and the matrix are the ones specified |
| 41 | `pad.bin` length: `k*L - data_span` fills the domain exactly, the File Entry block of `pad.bin` falls at or after `lba_base + k*L`, and a run whose `data_span` is already a multiple of `k` gets a zero-length `pad.bin` | The span arithmetic accounts for the File Entry blocks (sections 9.3, 10.5) |
| 42 | Filter construction: the geometry, the peeling order and the seed search of section 12.2 reproduce the golden filter bytes from the 1,000 stated keys | The filter is byte-reproducible, not writer-defined |
| 43 | `README.txt` and `FORMAT.txt` are byte-identical to the golden files after substitution, LF only, one trailing LF, and within their caps | Two writers produce the same disc (sections 10.4.1, 10.4.2) |
| 44 | Burn step listing: the sorted tree listing of section 10.7.1 reproduces `payload_hash` for a kind 2 and a kind 5 step | `burn --exec` can confirm a tree step |
| 45 | Run table status: a failed verify leaves the record with `run_status` 3, a later table still holds every burned run, and a reader never infers a withdrawal from absence | The table and the run chain agree (sections 9.6.1, 12.5.2) |
| 46 | Probable coverage: a plan built from the catalog alone marks an object in an old run probable, and the restore confirms it against that run's manifest when the disc is inserted | The plan is honest about what it proved (section 17.1) |
| 47 | Partial `pack`: an interrupted `pack` leaves no valid `burn.bin`, the next `pack` returns the objects to STAGED, deletes the plan directory and takes the next `run_seq` | An interrupted pack loses nothing (section 14.2) |
| 48 | A version 1 writer emits no `"BMAP"` and no `"RIDX"` chunk and sets neither optional bit; a reader skips an unknown chunk id | The reserved chunks stay reserved (section 12.3) |
| 49 | Kind 6 is refused in a manifest record, a layout extent record and a state log record | A ref is never an object file (section 8.1) |

### 23.3 Composite action

This section is informative. It describes the reference project's CI layout.

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

The probe list is normative: each question must be answered before the
feature that depends on it ships. The action paths are informative.

Any open question about tool behaviour becomes a **probe action**: a small
composite action, under `.github/actions/probe-<topic>/` in the reference
project, that runs the experiment on an image file and records the result as
a job artifact.

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

### 23.7 Phase 1 conformance checklist

A build claims Phase 1 conformance only when every item below holds. Each item
names the section that defines it and the test of section 23.2 that proves it.

| # | Item | Section | Test |
|---:|---|---|---|
| 1 | Every structure encodes and decodes to the byte-offset table, little-endian, with the checksum last. | 4 | 1 |
| 2 | Unknown `version_major` and unknown `required_feat` bits are refused; unknown `version_minor` and `optional_feat` bits are ignored. | 4.1, 21.4 | 1 |
| 3 | Content ids are BLAKE3-256 and SHA-256 multihashes of the uncompressed payload; both are read and written. | 5 | 1, 2 |
| 4 | FastCDC P4 with the Gear table of Appendix A.2 and the masks of Appendix A.3 gives the golden cut points. P3 and P5 are available. | 6, A | 2, 3 |
| 5 | Every chunk below `bundle.threshold` goes into a bundle. | 6.5, 8.3 | 1, 16 |
| 6 | zstd per chunk after hashing, with the 5 percent rule. | 7 | 15 |
| 7 | Trees, chunklists, snapshots and refs are canonical: two identical inputs give identical bytes. | 8.10 | 16 |
| 8 | Names are validated at parse time. Restore holds the invariants of section 18.8. | 8.5.8, 18.8 | 17, 18 |
| 9 | The root tree is synthetic, one entry per source root, with `ROOT_PATH`. | 16.5 | 1 |
| 10 | A profile 0 disc is one run, UDF 2.01 from `mkudffs --media-type=hd`, POW `spare:min`, open by default; `pack --close` seals it and burns the full-size image. | 10.1 | 32, 33, 34 |
| 11 | The fill order and the build passes of section 10.5 hold and `layout.bin` matches the read-back extents. | 10.5, 10.1.6 | 10, 23 |
| 12 | Every run carries `k = 231`, `m = 23` parity over its parity domain, which starts at LBA 0 for the first run, a checksum column whose sector `i` holds the data digests of stripe `i`, and `m + 2` header copies. | 11 | 6, 7, 8, 9 |
| 13 | Every run carries its filter, its manifest with the seven mandatory chunks `FANO`, `RECS`, `BNDL`, `PREQ`, `SRCR`, `DUPS` and `SPLT`, and its catalog with `CATALOG.bin` and the run table. | 12 | 4, 5, 30, 35 |
| 14 | The filter query rule of section 12.2 accepts every inserted key. | 12.2 | 4 |
| 15 | A filter hit is confirmed against a manifest before data is dropped. | 12.8 | 36 |
| 16 | Every command works with the cache deleted; restore works from the newest image alone. | 13.4, 17.9 | 11 |
| 17 | The state machine holds: `verify` is the only path to CLEAN, GC deletes only GC-ELIGIBLE. | 14.2, 14.4 | 20, 21, 29 |
| 18 | The repository lock is taken as section 14.5 states. | 14.5 | - |
| 19 | The packer applies rule 1 and the capping knobs in the rank of section 15.4. | 15 | 37 |
| 20 | The restore plan is deterministic, disc-major, printed before any read, and fails up front on a missing disc. | 17 | 12 |
| 21 | Phase 1 metadata is stored and restored in the order of section 18.4; the loss report and exit codes hold. | 18 | 13, 19 |
| 22 | The quick check, the in-flight rule and the unchanged-commit rule hold. | 16.2, 16.6, 16.1 | 26, 27, 31, 38 |
| 22a | The local ref log and the pending snapshot chain hold. | 14.6 | 39 |
| 23 | Later-phase commands, options and config keys are refused with a message that names the phase. | 19, 20 | - |
| 24 | `burn --print` never touches a device; `burn --exec` runs only the printed commands, on Linux only, and refuses an unpatched burner. | 10.7, 10.12 | 22 |
| 25 | `README.txt` is byte-identical to the text of section 10.4.1 after substitution, and `FORMAT.txt` obeys the generation rule of section 10.4.2. Both are hashed into `layout.bin` and covered by the parity. | 10.4 | 1, 43 |
| 26 | Every golden vector of section 23.8 passes. | 23.8 | 1, 2 |

### 23.8 Golden vectors

A golden vector is a checked-in input and its expected output. The values are
not printed in this document; the reference implementation computes them once
from the rules and checks them in, and every other implementation must
reproduce them. The vectors are:

| Vector | Input | Expected output |
|---|---|---|
| Gear table | The rule of Appendix A.2. | The 256 u64 values, and the BLAKE3-256 of their 2048 little-endian bytes. |
| Masks | The rule of Appendix A.3. | The six values printed in Appendix A.3. |
| Cut points, per profile | A fixed 256 MiB pseudo-random file, generated from a stated seed by a stated generator, and a fixed 3 MiB file. | The `(offset, length, content id)` triples for P3, P4 and P5, under BLAKE3-256 and under SHA-256. |
| Zero chunk | 16 MiB, 8 MiB and 32 MiB of zero bytes. | The content id under both algorithms. |
| Multihash text form | One digest under each algorithm. | The 68-character text form. |
| Common object header | One chunk of stated bytes, stored with zstd level 3 and with no compression. | The 64 header bytes and the object file bytes. |
| Bundle | Three stated small chunks. | The bundle payload: header, chunk payloads, index, trailer; and the bundle id. |
| Chunklist | 100 stated chunk ids and lengths. | The chunklist payload and its id. |
| Tree | A directory with a regular file, a subdirectory, a symlink, a hardlink pair, a device node, one xattr, and one spilled TLV, with stated metadata. | The tree payload and its id. Entries sorted, TLVs sorted, offsets aligned. |
| Snapshot | A stated root tree, parent, generation, times and TLVs. | The snapshot payload and its id. |
| Ref record | A stated name, snapshot id, time and run seq. | The 96 record bytes. |
| Disc superblock | Stated identity, capacity, profile and reserve values. | The 2048 bytes. |
| Run header | Stated geometry and counts. | The 512 bytes, and the CRC. |
| Layout table | Ten stated extents, including a parity file and `pad.bin`. | The container bytes. |
| Manifest | Ten stated records, two in a bundle, one split, two prerequisites, one source run. | The container bytes with `FANO`, `RECS`, `BNDL`, `PREQ`, `SRCR`, `DUPS` and `SPLT`. |
| Filter | 1,000 stated keys. | The geometry of step 1 of section 12.2, the seed that the search of step 5 finds, the container bytes, and the query result for those keys and for 1,000 absent keys. |
| README.txt | The identity values of the disc superblock vector. | The file bytes, after the substitution rule of section 10.4.1. |
| FORMAT.txt | Format major 1, minor 0. | The file bytes, under the generation rule of section 10.4.2. |
| Burn step tree listing | A stated tree of five files, one in a subdirectory. | The listing bytes and the `payload_hash` (section 10.7.1). |
| Catalog container | Five stated entries. | The container bytes. |
| Simple tables | Two stated records of each of the snapshot table, the ref table, the run table and the disc directory. | The container bytes. |
| Checksum sector | The 231 stated data sectors of one stripe. | The 2048 bytes of the checksum sector. |
| Parity | A stated stripe of `k` data sectors. | The `m` parity sectors under the code of section 11.2.1, and the recovery of the stripe after `m` stated erasures. The `k = 3`, `m = 2` example of section 11.2.1 is the one vector that is printed in this document. |
| Local ref log and notes file | Two stated records of each. | The file bytes (section 14.6). |
| State log | Three stated records. | The log bytes, and the replay result after the last record is truncated. |
| Burn plan | A stated plan with two steps. | The container bytes and the rendered command lines. |
| Hardlink group id | A stated `repo_uuid`, `st_dev` and `st_ino`. | The u64. |
| Root tree name encoding | The paths `/srv/data`, `/a%b/c` and `/x\y`. | `%2Fsrv%2Fdata`, `%2Fa%25b%2Fc` and `%2Fx%5Cy`. |
| Exclude patterns | A stated tree and a stated rule set with anchored, unanchored, negated, `**` and trailing-slash rules. | The set of paths that the tree holds. |
| CRC-32C | The 9-byte string `123456789`. | `0xE3069283`. |

A vector file is named by the structure and the version. A vector is never
changed under a version; a format change adds a vector.

---

## 24. Implementation notes

Items 1, 2, 5, 6, 7, 10 and 11 are informative; items 2, 5 and 6 are the
reference project's house style. The other items are requirements on any
implementation that claims conformance (section 21.6).

1. **Language: Go, informative.** The reference implementation is written in
   Go: the standard library covers SHA-256, compression bindings are mature,
   and a single static binary suits a recovery tool. The format is defined
   by this document and by nothing in Go; a conforming implementation may use
   any language.
2. **Code must explain itself, informative.** Comments and commit messages must not cite
   section numbers of this document. A comment carries only information that is
   related to the code beside it.
3. **Vendor the frozen tables.** The Gear table and the mask constants are part
   of the on-disc format. Vendor them. Do not import them from a dependency that
   could change them.
4. **Pin external tools.** Check the versions of `growisofs`, `mkudffs`,
   `udfinfo`, `genisoimage` and `ddrescue` at startup. Refuse to burn with an
   unpatched dvd+rw-tools build.
5. **One Go definition per binary structure, informative**, with explicit encode and decode
   functions. No reflection-based marshalling. No struct tags. The byte layout
   is written out by hand, field by field, in the order of the table in this
   document.
6. **Golden files for every structure, informative.** A test writes a structure with known
   values and compares the bytes to a checked-in file, and reads that file back
   and compares the fields.
7. **No hidden allocation in the hot path.** Informative: chunking and
   hashing run over large files, so reuse buffers.
8. **Every read verifies.** A function that returns object bytes verifies the
   content id before it returns. There is no "trusted" path.
9. **Errors carry the id.** An error about an object names the object. An error
   about a run names the run seq and the disc uuid.
10. **Suggested layout.** `cmd/noahsark` for the CLI, and `internal/` packages
    for chunker, hash, object, tree, manifest, filter, fec, run, burn, stage,
    plan, restore, cache and disc. This is advisory.
11. **Dependencies, kept small.** Every dependency is vendored or pinned by
    hash. Informative: these Go packages implement the algorithms that this
    document pins, and were used to validate its parameters.

    | Need | Package |
    |---|---|
    | Reed-Solomon `rs255-gf8` (section 11.2) | `github.com/klauspost/reedsolomon` |
    | zstd | `github.com/klauspost/compress/zstd` |
    | BLAKE3 | `lukechampine.com/blake3` or `github.com/zeebo/blake3` |
    | BinaryFuse16 (section 12.2) | `github.com/FastFilter/xorfilter` |
    | UDF File Entry parsing (section 10.1.6) | `github.com/mogaika/udf`, read-only |
    | Watch mode (Phase 3) | `github.com/fsnotify/fsnotify` |
    | CLI | any |

    A package is a convenience, never the definition. The on-disc format is
    defined by this document, and a conforming implementation may use no
    package at all.
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
| **Append** | Adding a run to a disc that already holds one. Phase 2. Section 10.2.2 is the normative home. |
| **Metadata object** | A tree, a chunklist or a snapshot object: an object that holds references and no file content. Bit 2 of the layout extent flags and of the manifest record flags marks one. |
| **Local ref log** | `<repo>/refs.bin`, the append-only log of ref values that section 14.6 defines. |
| **Pending snapshot chain** | The snapshot objects in staging whose state is below CLEAN, reachable from the head named by the local ref log. |
| **Bundle** | An object that holds many small chunks plus an index. |
| **Burn plan** | The machine-readable file that `pack` writes and `burn` renders into command lines. |
| **Capping** | Bounding the number of older runs that a new run may reference, to bound the restore plan. |
| **Catalog** | The set of files under `runs/<seq>/catalog/` that every run carries: `CATALOG.bin`, every snapshot object, every earlier filter, recent manifests, the snapshot table, the ref table, the run table, and the disc directory. |
| **Checksum column** | The FEC column whose sector `i` holds an 8-byte digest of each of the `k` data sectors of stripe `i`. |
| **Commit bundle** | A directory of new objects plus a `BUNDLE.bin` header, produced by `commit --out` and consumed by `import`. Not the same thing as a bundle object. |
| **Direct mode** | A commit that walks the source itself and reads only changed files. |
| **Mirror mode** | A commit whose changed files are pulled into a mirror directory first, by `sync`. |
| **Unstable path** | A file whose size or mtime changed while it was being read. The parent entry is reused, or the content is stored with the `UNSTABLE` flag. The path is reported. See sections 16.6 and 18.10. |
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
| **Run table** | The catalog table that maps every `run_seq` to its disc, its LBA range and its run header hash (section 12.5.2). |
| **RS margin** | `m` minus the worst-stripe erasure count, as a percentage of `m`. The headline health metric. |
| **Shard** | One 2048-byte sector, as seen by the FEC layer. |
| **Snapshot** | An object that names a root tree, a parent and a generation. |
| **Spare area** | The reserved region that POW uses for logical overwrites. |
| **Staging** | The local store that holds objects between commit and CLEAN. |
| **Stripe** | The 255 shards, one per column, at the same offset inside their columns. |
| **Tree** | An object that describes one directory, with full metadata per entry. |
| **TLV** | A type-length-value record in a tree entry's extension area. |

---

## 27. Document change log

The document version is independent of the format version. A document
change that alters a byte on a disc bumps the format version as section 21
states; every entry below is under format major 1.

| Document version | Change |
|---|---|
| 2.3 | `fec_overhead` is `fec_region - data_budget`; the `m` parity header sectors became the named fixed term `parity_headers`, and both worked examples, the dry-run printout and test 24 were recomputed (section 10.11). Section 10.11 split into invariants, a reference estimator and the two examples; `fill_limit_sectors` has one definition and one consequence, and the superblock records the values the writer used. `disc.expected_runs` defaults to 1 under profile 0. `superblock_and_headers` counts the real sizes of `README.txt` and `FORMAT.txt`, which gained normative caps. `filter_bytes` uses the 80-byte header plus the 4-byte body CRC. `data_span` covers steps 1 to 6 only, ends at the File Entry block of the last file of step 6, and `pad.bin` is `k*L - data_span` sectors (sections 9.3, 10.5, 11.2). The exact text of `README.txt` and the generation rule of `FORMAT.txt` (sections 10.4.1, 10.4.2). The BinaryFuse16 construction, peeling order and seed search (section 12.2). The burn step tree listing serialization (section 10.7.1). `RUN.bin` and `RUN2.bin` are 2048-byte files (section 9.6). `disc_run_index` is u32 in the run header. The run table holds every burned run with a `run_status`, and withdrawal is explicit (section 12.5.2). `snapobj.bin` is a kind 2 bundle object (section 12.5.1). `"BMAP"` and `"RIDX"` are reserved with no payload in version 1 (section 12.3). Kind 6 never appears in a manifest, layout or state log record (section 8.1). Plans mark an object located only by a filter as probable and the restore confirms it (sections 17.1, 17.4, 19.14). `init --repo-uuid` requires the next sequence numbers or a disc scan (section 19.1). Retention stated as a version 1 non-goal (section 2.2), threat model (section 3.8), performance and resource requirements (section 2.7), what a partial `pack` leaves behind (section 14.2), reader and writer interop matrix (section 21.7). The burst bound qualified to the data columns and the parity retry bounded (sections 11.2, 11.4). Command templates made informative (section 10.8). Profile 2 append cost corrected to 4.3 percent (section 10.3.3). Objects per run, the Mini BD size and the FastCDC minimum-chunk reading corrected (sections 6.2, 6.3, 10.10). `disc.spare` is Phase 1 (section 20.4). Tests 41 to 49 added. |
| 2.0 | Complete redesign of the previous specification. Appendix D lists what it superseded and why. Section 25 summarizes it. |
| 2.2 | Reed-Solomon code defined exactly: GF(2^8) with 0x11D, a Cauchy generator matrix over the `k` data columns, the checksum column outside the code, and a printed `k = 3`, `m = 2` example (section 11.2.1). Local repository state: the local ref log, the pending snapshot chain and the notes file (section 14.6); commit resolves its parent from the local log first (section 16.1). ctime stored from Phase 1 (sections 2.5, 18.1, 20.10). Ref table sort key (section 12.5.1). Exact integer `catalog_growth` formula, both worked examples recomputed (section 10.11). 16-bit fan-out index defined (section 12.3). A `run_seq` is never reused (sections 12.5.2, 10.12.1, 14.2). `pad.bin` always present (sections 10.4, 10.5). Profile 2 builds its images with genisoimage and burns them with growisofs (sections 10.3.4, 10.5, 10.8). `verify --image` column location and the raw-LBA definition (sections 11.8, 9.7.1). Burn plan path limits (section 10.7.1). `scrub.schedule` grammar (section 20.13). Metadata object defined. Intermediate restore directories (section 16.5). Hardlink ids in mirror mode from the source listing (section 8.6). Unchanged commit writes nothing without `--force` (section 16.1). CRC rule restated with a coverage table (sections 4.1, 4.10). One CRITICAL rule (section 11.9). Feature-bit reservation corrected (section 4.4). `standalone` preset lifts the per-file caps (section 15.4). Run table membership stated (section 12.5.2). Normative-home table for burn topics (section 10). Security summary (section 3.8), limits (section 4.11), JSON field tables for the burn plan, the restore plan, the loss report and the health report, snapshot metadata tags in Appendix B, references in Appendix F. Tests 36 to 40 added. |
| 2.1 | Run table added to the catalog (section 12.5.2). Feature-bit assignment table (section 4.4) and hash coverage table (section 4.9) added. `header_len` limited to three structures; every other structure grows only by major version (section 4.7). Checksum column covers the data sectors of its own stripe (section 11.4). `k` and `m` fixed for version 1 (section 11.2). First run's parity domain starts at LBA 0 (section 9.3). One capacity formula with `data_budget` and `fill_limit_sectors` (section 10.11). Build passes with explicit placeholders and final order (section 10.5). Sealed path burns the full-size image (section 9.9.1). Bundle chunk payloads carry no object header (section 8.3). Exclude pattern language (section 16.5.1) and complete root name encoding (section 16.5). `verify --mapfile` (section 19.11). Healed objects enter STAGED (section 14.2). Reader and writer conformance (section 21.6). Snapshot table record 136 bytes with a u64 `first_run_seq` (section 12.5). Shelf notes moved from the cache to the repository (sections 3.7, 13.1). |

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
vectors of section 23.8.

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
| `NART` | 0x5452414E | Run table | 12.5.2 |
| `NADD` | 0x4444414E | Disc directory | 12.6 |
| `NACT` | 0x5443414E | Catalog container | 12.7.2 |
| `NATR` | 0x5254414E | Tree object payload | 8.5 |
| `NASN` | 0x4E53414E | Snapshot object payload | 8.7 |
| `NACL` | 0x4C43414E | Chunklist object payload | 8.4 |
| `NABD` | 0x4442414E | Bundle header | 8.3 |
| `NABT` | 0x5442414E | Bundle trailer | 8.3 |
| `NACS` | 0x5343414E | Checksum column sector header | 11.4 |
| `NASL` | 0x4C53414E | Staging state log | 14.3 |
| `NAXL` | 0x4C58414E | Cross-algorithm side table | 5.7 |
| `NABN` | 0x4E42414E | Commit bundle header. Backlog, reserved. | 16.11.1 |
| `NABP` | 0x5042414E | Burn plan container | 10.7.1 |
| `NABS` | 0x5342414E | Burn plan step | 10.7.1 |
| `NALR` | 0x524C414E | Local ref log | 14.6 |
| `NANT` | 0x544E414E | Notes file | 14.6 |

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

**Snapshot metadata TLV tags** (section 8.7)

| Tag | Name | Value | Repeats |
|---:|---|---|---|
| 1 | author | UTF-8. | No. |
| 2 | host | UTF-8 host name. | No. |
| 3 | message | UTF-8, from `commit -m`. | No. |
| 4 | source root | Raw path bytes of one source root. | One per root, in root order. |
| 5 | exclude rules | UTF-8 pattern lines of section 16.5.1, one per line, in rule order, for the root of the preceding tag 4. | One per root, in root order. |
| 6 | checksum commit | Empty. Present when the commit rehashed every file. | No. |
| 7 to 0x7FFF | reserved | | |
| 0x8000 to 0xBFFF | reserved critical | A reader refuses an unknown tag in this range. | |
| 0xF000 to 0xFFFF | vendor | Never critical. | May repeat. |

**Source flags** (section 8.7): bit 0 `NO_CTIME`, bit 1 `NO_HARDLINKS`,
bit 2 `NO_SPARSE`, bit 3 `SYNTHETIC_IDS`, bit 4 `CASE_INSENSITIVE`,
bit 5 `MTIME_SLACK`, bit 6 `HARDLINK_BY_PATH`.

**Manifest TOC chunk ids**: `FANO`, `RECS`, `BNDL`, `PREQ`, `SRCR`, `DUPS`,
`SPLT` (mandatory, in this order), then `BMAP` and `RIDX` (optional). See
section 12.3.

**Burn step kinds** (section 10.7.1)

| Id | Kind |
|---:|---|
| 1 | write image with `-Z` |
| 2 | build an ISO 9660 image from a tree |
| 3 | write image with `-M` |
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

Section 10.1.2 holds the image build, with the four normative options and the
reason for each. It is not repeated here. Run that script, then check the
result:

```bash
mount -t udf -o loop,rw run.udf /mnt/ark
#   copy files one at a time, in fill order (section 10.5)
umount /mnt/ark

udfinfo run.udf | grep -q '^integrity=closed' || { echo "dirty image"; exit 1; }
udfinfo run.udf | grep -c 'type=ANCHOR'        # expect 3
```

### C.3 Burn

This appendix holds no burn command line of its own. The normative command
lines are:

| Case | Section |
|---|---|
| Profile 0, open (default) and sealed | 10.1.5 |
| Profile 1 variant 1b, one seeking write per changed 32 KiB-aligned run | 10.2.4 |
| Profile 2, build with genisoimage, first write, append and close | 10.3.4 |
| The templates that `burn --print` renders | 10.8 |

Informative notes that apply to every case: M-DISC replaces `-speed=4` with
`-speed=2`; a CI dry run adds `-dry-run`; a seeking append may add
`-C 16,${NWA}` as a safety assertion; `-speed=4` and the labels in the
examples are placeholders that `burner.speed` and `label.template` decide.

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
noahsark verify --image rescued.img --mapfile rescue.map --heal --report health.json
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
dpkg -l dvd+rw-tools 2>/dev/null | tail -1     # section 10.12 and Appendix E
mkudffs --help 2>&1 | head -1                  # expect udftools 2.3 or newer
genisoimage -version                            # profile 2 only
ddrescue --version | head -1
```

An unpatched dvd+rw-tools 7.1 under-reports BD-R capacity and fails to close a
blank BD-R. `burn --exec` must refuse it.

---

## Appendix D. Rejected and superseded alternatives

**Superseded. Do not implement anything in this appendix.**

This appendix is informative. It records what was considered and rejected,
with the reason and the evidence. It exists so that a future reader does not
re-open a settled question.

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
See Appendix E for the conclusion.

**Descriptor-set-per-session UDF multi-session.** Writing a complete UDF
descriptor set per session, with a fresh anchor at `session_start + 256` and a
full directory tree that points back into earlier sessions, is spec-legal. It
was rejected for three reasons. First, no tool builds it: `mkudffs --startblock`
creates a new **empty** filesystem at an offset, measured as `numfiles=0`, so
NoahsArk would have to become a UDF writer. Second, the cost per session is
about 54 blocks of descriptors plus the entire rewritten tree, which is about
205 MB per session at 100,000 objects, paid again every time. Third, and
decisively, **macOS sees only the first session** until the disc is closed. The
POW-growth design of section 10.2.2 keeps `Number of Sessions: 1`, which sidesteps
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

---

## Appendix E. Evidence: why true UDF multi-session is not possible

This appendix is the evidence for section 9.13. It records what was checked,
where, and when. It is informative for an implementer and normative for a
reader who wants to re-open the question: re-open it only with newer
evidence than this.

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

Profile 2 avoids UDF entirely and uses the ISO 9660 merge that genisoimage
already supports and that growisofs accepts as a `-M` image.

**growisofs internals that the burn rules rest on.** This block is
informative. It records where in the growisofs source each rule of section
10.9 and section 10.10 comes from, so that a later reader can re-check it.

| Rule | Where it comes from |
|---|---|
| A seeking write's byte offset and length must be multiples of 32768. | `poor_mans_pwrite64` rejects any other value with `EINVAL`. |
| `seek:N` requires `N % 16 == 0`. | The `-use-the-force-luke=seek:` parser. |
| One command line covers every media size. | `get_2k_capacity()` computes `nwa + free_blocks` from `READ TRACK INFORMATION`; there is no layer logic and no hardcoded sector count. |
| A blank BD-R is formatted for POW unless `spare:none` is passed. | `bd_r_format()` forces the Format Subtype to SRM+POW. |
| A POW BD-R is treated as rewritable. | `poor_man_rewritable()` classes `profile == 0x41 && bdr_plus_pow` as rewritable. |
| Appends stay in one session. | `plusminus_r_C_parm()` takes `next_session` from the Next Writable Address and forces `prev_session = 0`. |

**The two distribution patches that section 10.12 requires.** Upstream
dvd+rw-tools 7.1 (2008-03-05) is broken for BD-R in two ways:

| Patch | Bug | Effect without it |
|---|---|---|
| `ignore_pseudo_overwrite.patch`, 2011-03-07 | Debian #615978 | A POW-capable drive under-reports BD-R capacity and the disc cannot be filled. |
| `fix_burning_bd-r_discs.patch`, 2015-02-20 | Debian #713016 | A blank BD-R fails to close with `CLOSE SESSION failed with SK=5h/INVALID FIELD IN CDB`. |

Debian 7.1-14, Fedora 7.1-13 and Arch 7.1-13 carry both.

---

## Appendix F. References

This appendix is informative. It names the external documents that the
normative text relies on, so that a reader can check a claim at its source.
Where this document and a reference disagree, this document wins for the
on-disc format.

| Topic | Reference |
|---|---|
| FastCDC | W. Xia et al., "The Design of Fast Content-Defined Chunking for Data Deduplication Based Storage Systems", IEEE Transactions on Parallel and Distributed Systems, 2020. Sections 6 and Appendix A. |
| BinaryFuse filters | T. M. Graf and D. Lemire, "Binary Fuse Filters: Fast and Smaller Than Xor Filters", ACM Journal of Experimental Algorithmics, 2022. Section 12.2. |
| Capping | M. Lillibridge, K. Eshghi and D. Bhagwat, "Improving Restore Speed for Backup Systems that Use Inline Chunk-Based Deduplication", USENIX FAST 2013. Section 15.4. |
| Reed-Solomon codes | I. S. Reed and G. Solomon, "Polynomial Codes over Certain Finite Fields", Journal of SIAM, 1960. Section 11.2. |
| Cauchy generator matrices | J. Blömer et al., "An XOR-Based Erasure-Resilient Coding Scheme", ICSI TR-95-048, 1995. Section 11.2.1. |
| dvdisaster RS03 | dvdisaster documentation, the RS03 codec. Informative comparison in section 11.2. |
| BLAKE3 | J. O'Connor, J.-P. Aumasson, S. Neves and Z. Wilcox-O'Hearn, "BLAKE3: one function, fast everywhere", 2020. Section 5.2. |
| SHA-256 | NIST FIPS 180-4, Secure Hash Standard. Section 5.2. |
| Multihash and multicodec | The multiformats specifications, multihash and the multicodec table. Sections 4.6 and 5.4. |
| CRC-32C | G. Castagnoli, S. Bräuer and M. Herrmann, "Optimization of Cyclic Redundancy-Check Codes with 24 and 32 Parity Bits", IEEE Transactions on Communications, 1993; parameters as used by RFC 3720 (iSCSI). Section 4.1. |
| zstd | RFC 8878, Zstandard Compression and the 'application/zstd' Media Type. Section 7. |
| UDF 2.01 | OSTA Universal Disk Format Specification, revision 2.01, and ECMA-167, 3rd edition. Sections 10.1 and 10.2. |
| ISO 9660:1999 | ISO 9660:1988 with the 1999 amendment (level 4), and ECMA-119. Section 10.3. |
| Blu-ray error correction | Blu-ray Disc Association, "White Paper Blu-ray Disc Format, 1.A Physical Format Specifications for BD-RE", the LDC and BIS picket code. Section 11.1. |
| dvd+rw-tools | growisofs and dvd+rw-mediainfo, upstream 7.1 with the Debian patch set. Sections 10.9, 10.12 and Appendix E. |
| udftools | mkudffs and udfinfo, 2.3 or later. Section 10.1.2. |
| GNU ddrescue | The ddrescue manual, the mapfile format. Sections 11.8 and C.5. |
| Git multi-pack-index | Git technical documentation, "multi-pack-index format". Sections 12.3 and 13.1. |
| Git partial clone | Git technical documentation, "partial clone", the promisor discipline. Section 12.4. |
| GEFS | O. Read, GEFS, a content-addressed filesystem for Plan 9: hashed pointers and one root per snapshot. Sections 4.1 and 8.7. |
| Duplicacy | The two-step fossil collection rule. Section 14.4. |
| Bacula | The bootstrap file. Section 17.4. |
| restic | The bundle (pack) shape and the Windows ACL inheritance defect. Sections 8.3 and 18.5. |
| age | The separation of encryption from signing that section 8.9 reserves. |
| Linux kernel | `fs/udf/super.c` for the write-once read-only rule, and the `openat2` and `RESOLVE_*` flags of section 18.8. |
