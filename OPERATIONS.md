# NoahsArk operations

**Document version 3.0.** Format major 1.

This document defines everything a NoahsArk implementation does on the host
that is not an on-disc byte. It covers the local repository and its files, the
staging state machine, the commit walker, the packer and the locality rules,
the capacity estimator, the image build and the burn workflow, verify, scrub
and heal, the restore planner and the restore procedure, the disc lifecycle,
locking, the command-line interface, the configuration keys, the JSON
outputs, the exit codes, the conformance checklist and the test list.

`FORMAT.md` is the on-disc format document. It defines every byte that reaches
a disc and every rule a reader follows. This document cites `FORMAT.md` for
every on-disc structure and never redefines one. Where a local structure has
its own byte layout, such as the burn plan or the state log, this document
carries that layout, because those bytes never reach a disc.

A rule in this document is normative unless a paragraph or a block is marked
**informative**. An informative block describes one way to reach a normative
outcome; another way that reaches the same outcome conforms.

---

## Table of contents

1. [Scope and conventions](#1-scope-and-conventions)
2. [Repository, staging and cache](#2-repository-staging-and-cache)
3. [Local file formats](#3-local-file-formats)
4. [Staging state machine](#4-staging-state-machine)
5. [Refs, the pending snapshot chain and notes](#5-refs-the-pending-snapshot-chain-and-notes)
6. [Concurrency and locking](#6-concurrency-and-locking)
7. [Commit](#7-commit)
8. [Packing and locality](#8-packing-and-locality)
9. [Capacity estimator](#9-capacity-estimator)
10. [Disc filesystems and image building](#10-disc-filesystems-and-image-building)
11. [Burn plan and burning](#11-burn-plan-and-burning)
12. [Disc lifecycle, closing and appending](#12-disc-lifecycle-closing-and-appending)
13. [Verify, scrub and heal](#13-verify-scrub-and-heal)
14. [Restore](#14-restore)
15. [Metadata restore policy](#15-metadata-restore-policy)
16. [CLI reference](#16-cli-reference)
17. [Configuration reference](#17-configuration-reference)
18. [JSON document evolution](#18-json-document-evolution)
19. [Exit code registry](#19-exit-code-registry)
20. [Failure and recovery actions](#20-failure-and-recovery-actions)
21. [Conformance checklist](#21-conformance-checklist)
22. [Test list](#22-test-list)
23. [Manual physical checklist](#23-manual-physical-checklist)
24. [Burning-host command reference](#24-burning-host-command-reference)
- [Appendix A. Decision index](#appendix-a-decision-index)

---

## 1. Scope and conventions

1.1 This document is normative for host behaviour. `FORMAT.md` is normative
for disc bytes. Where the two touch, `FORMAT.md` wins.

1.2 A citation of the form "FORMAT.md section N.M" names a section of the
on-disc format document. A bare "section N.M" names a section of this
document.

1.3 Every command carries a phase tag. A Phase 1 build must refuse a Phase 2
or a Phase 3 option with a clear message. It must not ignore it.

1.4 A build must refuse an unknown config key, and a key of a later phase than
it implements, with a message that names the key and the phase.

1.5 Every integer size in this document is in bytes unless the text says
sectors. A sector is 2048 bytes.

1.6 "Ascending" is an unsigned bytewise comparison, as FORMAT.md section 6.20
defines it.

1.7 Version 1 never removes a snapshot and never frees disc space. There is no
retention and no expiry.

---

## 2. Repository, staging and cache

### 2.1 The repository directory

A repository is one local directory. It holds everything that is not on a disc
and not in the cache.

| Path | Content | Normative |
|---|---|---|
| `<repo>/config` | The configuration file of section 17. It holds `repo.uuid`. | Yes. |
| `<repo>/lock` | The repository lock file of section 6. | Yes. |
| `<repo>/staging/` | The staging store of section 2.3, unless `staging.dir` moves it. | Yes, the role. The name is the default. |
| `<repo>/refs.bin` | The local ref log of section 3.3. Authoritative for a ref whose run is not yet CLEAN. | Yes. |
| `<repo>/notes.bin` | Operator notes per disc, keyed by `disc_uuid` (section 3.4). | Yes, the role. The name is the default. |
| `<repo>/probes/` | Results of the manual probes, one text file each. | Informative. |

`init` creates the directory, `config` with a fresh `repo.uuid`, an empty
`staging/` with an empty state log, an empty `refs.bin`, and `lock`. A
directory is a repository when it holds a readable `config` whose `repo.uuid`
parses as a uuid.

The cache is never inside the repository, so that deleting the cache and
deleting the repository stay independent acts.

Every disc of a repository carries `repo_uuid`. A lost repository is therefore
recreated by `init --repo-uuid=<uuid>` followed by `rebuild-cache`. Nothing in
the repository directory is a source of truth except the state log for objects
that are not yet CLEAN and the local ref log for refs whose run is not yet
CLEAN.

### 2.2 Repository discovery

Discovery runs in this order. The first hit wins.

1. `--repo=PATH`.
2. The environment variable `NOAHSARK_REPO`.
3. The current directory, then each ancestor up to the filesystem root,
   nearest first. The first directory that is a repository wins.

A command that finds no repository exits with code 2 and says so.

`init` refuses a directory that already is a repository. `init` refuses to
create a repository inside another one.

### 2.3 Staging store layout

The staging store is a local directory inside the repository. The
subdirectory names below are **informative**; an implementation may choose
others. The roles, the state log and the filesystem rule are normative.

```
staging/
    objects/ab/cd/<id>          object files waiting to be packed
    images/<disc_uuid>.img      disc image mirrors (profile 1 variant 1b)
    plans/<run_seq>/            burn plan, run image, sort file, patches
    restore/                    restore assembly area
    heal/                       reconstructed objects
    mirror/                     sync mirror, cleared after commit (Phase 2)
    commitbundles/<name>/       imported commit bundles (Backlog)
    state.db                    append-only binary state log
```

`state.db` and the local ref log `<repo>/refs.bin` are the only authoritative
local state. Everything else in staging is either an object that also exists
in the source, or derived data.

Staging must be on a local filesystem, or on NFS with `sync` semantics. An SMB
or CIFS mount is refused. The tool reads the filesystem type of `staging.dir`
at startup and exits with code 2 and a named reason when the type is `cifs` or
`smb3`. `staging.allow_unsafe_fs` overrides the check, and the override is
recorded in the state log.

### 2.4 Local cache layout

Two rules are normative. First, everything in the cache is derived from discs
and is rebuildable: a command must behave the same, apart from speed, with the
cache deleted. Second, never put in the cache anything whose loss loses archive
data; operator data such as shelf notes lives in the repository. The cache is
never inside the repository.

Informative: the default location is `$XDG_CACHE_HOME/noahsark/<repo-uuid>/`,
which falls back to `~/.cache/noahsark/<repo-uuid>/`. `--cache-dir` and
`cache.dir` override it. The layout below is one conforming layout.

| Item | Content |
|---|---|
| `index.bin` | Merged sorted index over all runs. The manifest record plus `run_seq`. |
| `filters/<seq>.bin` | Copies of run filters. |
| `manifests/<seq>.bin` | Copies of run manifests, accumulated as discs are mounted. |
| `snapshots.bin` | A copy of the snapshot table. |
| `refs.bin` | A copy of the ref table. |
| `runs.bin` | A copy of the run table. |
| `discs.bin` | A copy of the disc directory. |
| `health.log` | Per-disc verification history. The next scrub regenerates it. |
| `xlate-<from>-<to>.bin` | Optional cross-algorithm side table (section 3.6). |
| `pending-confirm.log` | Unconfirmed filter hits, with the runs that would confirm them. The next `commit` regenerates it. |

The health record of the newest disc lives only in the cache until the next
disc is burned. A disc's health record reaches a disc through the disc
directory, which the next run writes into its catalog, so the newest disc
carries no health record for itself. Losing the cache therefore loses the
newest disc's verification date and RS margin, and nothing else.

A reader that finds no health record for the newest disc reports `UNKNOWN` and
recommends a scrub. It must not report `HEALTHY`.

### 2.5 Cache rebuild levels

| Level | Minimum set | Gives | Cost |
|---:|---|---|---|
| 1 | The newest disc | The catalog: every run's filter, the snapshot table, the ref table, the run table, the disc directory, and the catalog's 8 manifests plus the run's own, that is 9. | One disc mount. |
| 2 | The discs a snapshot references | Everything needed to restore that snapshot. | The plan's disc count. |
| 3 | Every disc | The exact merged index, for maximum dedup on the next backup. | One mount per disc. |

Level 3 is lazy and incremental. A run's manifest is copied into the cache the
first time that disc is mounted for any reason. `rebuild-cache` asks for discs
one at a time, newest first, and reads only manifests; the data areas are never
read.

`index.bin` uses the manifest record layout of FORMAT.md section 11.2 with an
added `run_seq` and a 256-entry or 65536-entry fan-out. Rebuilding it is a
merge sort over the per-run manifests, with no conflict to resolve.

### 2.6 Cache staleness

Staleness is detected by `disc_seq` and `disc_uuid`. It is never detected by
mtime and never by a time-to-live.

| State | Condition | Action |
|---|---|---|
| Current | The newest disc found matches the recorded `disc_seq` and `disc_uuid`. | Use the cache. |
| Stale | A newer disc exists. | Merge that disc's manifest and catalog. |
| Wrong | A disc's uuid does not match the cache's record for that seq. | Refuse the cache. Rebuild. |
| Incomplete | A plan references a run with no cached manifest. | Ask for that disc and merge. |
| Version mismatch | `cache.format_version` differs. | Delete and rebuild. Never migrate. |

### 2.7 Cache-less operation

Every command must work with the cache absent.

| Command | Behaviour with no cache |
|---|---|
| `commit` | Works. Dedup falls back to writing the chunk again for every unconfirmed hit. `pending-confirm.log` records the cost. |
| `pack` | Works. Locality uses only the staged objects. |
| `plan` | Reads the catalog from the newest disc first, then plans. |
| `restore` | Same. Then reads the discs the plan names. |
| `verify` | Works from the disc alone. |
| `ls`, `log` | Read the snapshot table from the newest disc. |

CI must include a test that deletes the cache, rebuilds from the newest image
alone, and restores successfully. That is test 11 of section 22.

### 2.8 Data flows

**Write.**

```
 source -> commit -> staging -> pack -> run -> burn -> verify -> clean -> GC
```

`commit` chunks, hashes, deduplicates, compresses and writes objects into
staging as STAGED. `pack` selects the objects for the next run, applies the
locality rules of section 8, builds the image and the parity, writes the burn
plan, and moves the objects to PACKED. `burn` renders the plan, an external
burner writes the bytes, and the objects move to BURNED. `verify` ejects,
reloads, reads back and compares, and the objects move to CLEAN. The retention
timer then starts, and GC may delete the objects once they are GC-ELIGIBLE. GC
never deletes an object that is not CLEAN.

**Restore.**

```
 snapshot id -> object set -> run map -> set cover -> disc plan (JSON)
   -> for each disc in plan order:
        detect disc -> read needed objects in LBA order -> staging/restore/
        -> assemble every file that is now complete -> free its staging space
   -> apply metadata in the fixed order -> deferred directory times
   -> loss report -> exit code
```

**Heal.** Section 13.4 gives the heal order. Recovered objects are written into
`staging/heal/` and re-enter the state machine at STAGED, so the next `pack`
writes them again.

---
## 3. Local file formats

Every structure in this section lives on the host. None of its bytes reaches a
disc. Every integer is little-endian, as FORMAT.md section 2.1 requires. Every
CRC is CRC-32C with the parameters of FORMAT.md section 2.1.

The burn plan container and the burn step record are in section 11.2 and
section 11.3, beside the rules that validate them.

### 3.1 State log header

`<staging>/state.db` holds the staging state log.

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

### 3.2 State log record

Record, 96 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 32 | u8[32] | `content_id` | The object. |
| 32 | 8 | i64 | `time_sec` | When the transition happened. For a BURNED record this is the **actual burn time**, and it is the only place the archive records it: no on-disc structure can, because every on-disc structure that would hold it is final and hashed before the burn (FORMAT.md section 7.6). |
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

### 3.3 Local ref log record

**The local ref log.** `<repo>/refs.bin` is an append-only log with the
header of the state log (section 3.1), magic `"NALR"`, `record_size` 128,
and these records:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 96 | ref record | `ref` | The ref record of FORMAT.md section 6.18, unchanged. `run_seq` is 0 while no run holds the snapshot; it is the run's `run_seq` from the moment `pack` puts the snapshot object into a run. |
| 96 | 8 | u64 | `sequence` | Monotonic record number. |
| 104 | 8 | u64 | `snapshot_generation` | `generation` of the snapshot named by `ref`. For a fast ancestry check. |
| 112 | 12 | u8[12] | `reserved` | Zero. |
| 124 | 4 | u32 | `record_crc32c` | CRC-32C over bytes 0 to 123. |

The container header is the state log header of section 3.1 with `magic`
`"NALR"` and `record_size` 128. Section 5.1 gives the resolution rules. A
record with `run_seq` 0 exists only here, never on a disc.

### 3.4 Notes file record

**The notes file.** `<repo>/notes.bin` holds operator notes per disc. It is
an append-only log with the header of the state log, magic `"NANT"`,
`record_size` 256, and these records:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 16 | u8[16] | `disc_uuid` | The disc. |
| 16 | 8 | i64 | `time_sec` | When the note was written. |
| 24 | 4 | u32 | `time_nsec` | Nanoseconds. |
| 28 | 2 | u16 | `note_label_len` | Byte length of `label`, 0 to 64. It is u16, not the u32 `label_len` of the superblock (FORMAT.md section 7.5) and of the disc directory (FORMAT.md section 11.6), and it measures a different, local label; the name differs so that the two are never confused. |
| 30 | 2 | u16 | `shelf_len` | Byte length of `shelf`, 0 to 128. |
| 32 | 64 | u8[64] | `label` | Local display label, UTF-8, zero-padded. Unused, `note_label_len` 0, for `record_type` 1. |
| 96 | 128 | u8[128] | `shelf` | Shelf note, UTF-8, zero-padded, for `record_type` 0. The downgrade reason, UTF-8 in the first `shelf_len` bytes, for `record_type` 1. |
| 224 | 8 | u64 | `sequence` | Monotonic record number. |
| 232 | 1 | u8 | `record_type` | 0 label and shelf note. 1 manual health downgrade. |
| 233 | 1 | u8 | `health` | New `health` value for `record_type` 1, from the disc directory registry of FORMAT.md section 11.6: 1 healthy, 2 degraded, 3 critical, 4 failed, 5 unknown. `disc mark-degraded --health` supplies it, and its default is 2 (section 16.23). Zero for `record_type` 0. |
| 234 | 18 | u8[18] | `reserved` | Zero. |
| 252 | 4 | u32 | `record_crc32c` | CRC-32C over bytes 0 to 251. |

The container header is the state log header of section 3.1 with `magic`
`"NANT"` and `record_size` 256. Section 5.3 gives the rules.

### 3.5 Cache index

`index.bin` is the cache's merged index. It uses the manifest record layout of
FORMAT.md section 11.2 with an added `run_seq` field, and a 256-entry or
65536-entry fan-out under the same rule as the manifest's `"FANO"` chunk.

`index.bin` is derived data. A reader never treats it as authoritative, and
confirms every answer against a manifest before it drops data. A local index is
an accelerator; the discs answer every question without it.

### 3.6 Cross-algorithm side table

The reindex container and its record layout are in FORMAT.md section 3.7.

The table is derived data. It lives in the local cache as
`xlate-<from>-<to>.bin`. `reindex` builds it. A writer may copy the table onto
a disc, and must not treat it as authoritative. Correctness never depends on
it; when the table is absent, the backup writes the data again.

Building the table needs a full read of the archive.

### 3.7 Commit bundle header

A commit bundle directory uses the staging object layout plus `BUNDLE.bin`.

```
/tmp/bundle/
    BUNDLE.bin                  header, see below
    objects/<ab>/<name>         new chunks and bundles
    trees/<ab>/<name>           new trees and chunklists
    snapshots/<name>            the new snapshot object
```

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
| 178 | 1 | u8 | `source_type` | Source type of the machine that wrote the bundle (FORMAT.md section 6.15). |
| 179 | 1 | u8 | `source_flags` | Source flags of that machine. |
| 180 | 4 | u32 | `host_len` | Byte length of `host`. |
| 184 | 64 | u8[64] | `host` | UTF-8 host name, zero-padded. Diagnostic only. |
| 248 | 4 | u32 | `reserved_u32` | Zero. |
| 252 | 4 | u32 | `header_crc32c` | CRC-32C of bytes 0 to 251. |

A commit bundle is untrusted. `import` verifies every content id before an
object enters staging, drops every object already present, and is idempotent.
`import` refuses a `repo_uuid` mismatch.

---

## 4. Staging state machine

### 4.1 States and transitions

The object states are STAGED, PACKED, BURNED, CLEAN, GC-ELIGIBLE and DELETED.

```
        commit
          |
          v
     +---------+   pack    +--------+   burn ok   +--------+
     | STAGED  |---------->| PACKED |------------>| BURNED |
     +---------+           +--------+             +--------+
          ^                    |  ^                   |  |
          |  burn failed       |  |  verify failed,   |  |
          +--------------------+  +-------------------+  |
          |                       |  reason 2: the run   |
          |                       |  is marked for       | verify ok. This is
          |                       |  re-burn and its     | mandatory, after an
          |                       |  objects go back     | eject and a reload.
          |                       |  to PACKED.          |
          |                                              v
          |                                         +--------+
          |                                         | CLEAN  |
          |                                         +--------+
          |                                              |
          |                       retention timer passed |
          |                    (staging.retain_after_clean, default 7 days)
          |                                              v
          |                                      +---------------+
          |                                      | GC-ELIGIBLE   |
          |                                      +---------------+
          |                                              |
          |                                          gc  |
          |                                              v
          +------------------------------------------  deleted

There are exactly two arrows out of a failure, and the diagram holds both:
BURNED to PACKED on a failed verify, and PACKED to STAGED on a failed burn.
```

### 4.2 Transition rules

1. `verify` is the only transition from BURNED to CLEAN. There is no timer and
   no manual override. An object stays in staging until the disc that holds it
   has been read back and checked.
2. `burn --exec` runs `verify` after the burn by default, under
   `burn.verify_after`. It ejects and reloads the disc first, so the read comes
   from the medium and not from a cache. It uses a second drive only when one
   is present.
3. GC never deletes an object that is not CLEAN.
4. GC is a separate command. Phase 1 runs it manually.
5. A run that fails to burn returns its objects to STAGED, with reason 1.
6. A run that fails verify moves its objects from BURNED back to PACKED, with
   reason 2, and marks the run for re-burn. The writer appends one PACKED
   record with reason 2 per object of that run. It also withdraws the run in
   the next run table it writes, with `run_status` 3, and appends a local ref
   record with `run_seq` 0 for every ref that the withdrawn run carried, so
   that the re-burn re-emits it. In both cases the failed `run_seq` is never
   reused.
7. An imported bundle object enters the machine at STAGED, exactly like a
   locally chunked object. `commit`, `import` and `verify --heal` are the only
   entry points.
8. The state log is authoritative only for objects that are not yet CLEAN.
   Everything about a CLEAN object is derivable from the discs.
9. A healed object enters the machine at STAGED with reason 3. `verify --heal`
   writes its bytes into `staging/heal/` and appends the STAGED record before
   it reports success. The object reaches CLEAN only when the run that holds it
   again passes `verify`. An object still present in another run in a state at
   or beyond CLEAN is re-fetched from that run, not healed.

### 4.3 State log replay

The current state of an object is the newest record for that id, by
`sequence`. A reader replays the log from the start.

A writer may compact the log by rewriting it with only the newest record per
id, but only after every CLEAN object has been dropped.

A record with a bad CRC ends the replay. Records after it are ignored, and the
tool reports a truncated log. That is the correct behaviour after a crash
during an append.

An object with no record after a truncated replay is treated as STAGED.

### 4.4 What a partial `pack` leaves behind

`pack` writes local files only. It touches no disc, so an interrupted `pack`
can never leave a partial run on a medium. It leaves exactly three things.

1. A consumed `run_seq`. `pack` assigns the number before it builds anything,
   and the number is never reused. The hole in the sequence is harmless,
   because only a burned run reaches a run table.
2. A plan directory under `staging/plans/<run_seq>/`. `burn.bin` is written
   last and is the only authoritative plan file, so a plan directory with no
   valid `burn.bin`, or one whose `header_crc32c` or any `step_crc32c` fails,
   is a partial plan. `burn --print` and `burn --exec` refuse it.
3. PACKED records in the state log, each naming the consumed `run_seq`. The
   object files are unchanged, because `pack` never moves or rewrites one.

The next `pack` scans the state log for PACKED records whose `run_seq` belongs
to no valid plan and to no burned run. It returns those objects to STAGED with
reason 1, deletes the partial plan directory, and takes the next `run_seq`.

`pack --dry-run` writes no plan and no state record.

### 4.5 GC rules

1. An object may be deleted only in state GC-ELIGIBLE.
2. An object reaches GC-ELIGIBLE only after `staging.retain_after_clean` has
   passed since it reached CLEAN.
3. GC must confirm, before every delete, that the object is present in at least
   one run whose verification passed. The confirmation reads the manifest, not
   the cache index alone.
4. GC never deletes an image mirror of a disc that is still appendable.
5. GC never deletes a burn plan that has not reached CLEAN. GC does delete a
   partial plan directory whose `run_seq` appears in no PACKED record.
6. `gc --dry-run` prints what it would delete and how many bytes it would free.

---

## 5. Refs, the pending snapshot chain and notes

### 5.1 Ref resolution

1. `commit` appends one record per moved ref, with `run_seq` 0, after it has
   written the snapshot object into staging and recorded it STAGED.
2. `pack` copies every local record whose `run_seq` is 0, and whose snapshot
   object it puts into the run, into the run's `refs.bin` with `run_seq` set to
   the run, and appends the same record to the local log.
3. The current value of a ref resolves in the order local log, then cache, then
   discs. Inside the local log the newest record is the highest `sequence`.
   Inside an on-disc `refs.bin`, or the cache's copy of it, the newest record
   is the one FORMAT.md section 6.18 names. A lookup applies exactly one of the
   two orders, chosen by the file it is reading.
4. A local record whose `run_seq` names a run that has reached CLEAN is
   derivable from the discs, and a writer may drop it when it compacts the log.
   A record with `run_seq` 0, or one naming a run that is not yet CLEAN, is
   authoritative and is never dropped.
5. When a run fails its verify and the writer withdraws it, the writer appends
   one new record per ref that the withdrawn run's `refs.bin` carried, with the
   same name, snapshot id and time, and `run_seq` 0. Rule 2 then copies those
   refs into the next run. The old record is not edited; rule 3 takes the
   newest record by `sequence`, so the `run_seq` 0 record wins.
6. A record with a bad CRC ends the replay, as in section 4.3.
7. `init --repo-uuid` on a recreated repository starts with an empty log.
   `rebuild-cache` then supplies every ref from the discs.

### 5.2 The pending snapshot chain

The pending snapshot chain is the set of snapshot objects in staging whose
state is below CLEAN. Its head for a ref is the snapshot named by the newest
local record.

Every `parent` of a chain member is either another chain member or a snapshot
that a verified run holds.

`commit` resolves its parent by rule 3 of section 5.1, and reads the snapshot
object from staging when its state is below CLEAN, else from the cache or the
discs. `log` and `ls` read the chain from staging before they read the
snapshot table.

### 5.3 Notes file rules

The newest record per `disc_uuid`, by `sequence`, is the current note,
independently for each `record_type`. The newest `record_type` 0 record gives
the label and the shelf note. The newest `record_type` 1 record, when one
exists, gives the disc's manually recorded health.

`disc label` appends a `record_type` 0 record. `disc mark-degraded` appends a
`record_type` 1 record.

`notes.bin` is local repository state. It is never rebuilt from the discs, and
nothing in it reaches a disc. A `record_type` 1 note therefore survives a cache
deletion.

A reader that computes disc health folds in the newest `record_type` 1 note
when one is present, in addition to the health it derives from the disc
directory and the checksum columns.

Losing the `record_type` 0 records loses no archive data.

---

## 6. Concurrency and locking

One repository is used by one process at a time for every command that writes
local state. The rules below are the requirement. The system calls named in
them are **informative**; they are the Linux way to meet the rule.

1. **Repository lock.** `<repo>/lock` is the lock file. A command that writes
   the state log, the staging store, the config, or a burn plan takes an
   exclusive advisory lock on it before it reads the state log, and holds it
   until it exits. Those commands are `init`, `commit`, `import`, `pack`,
   `append`, `burn --exec`, `close`, `verify`, `scrub`, `gc`, `restore`,
   `consolidate`, `disc label`, `disc mark-degraded` and `image build`.
2. **Shared lock.** A read-only command takes a shared lock while it reads the
   state log, and releases it before it does anything slow. Those commands are
   `plan`, `ls`, `log`, `health`, `disc list`, `burn --print`, `image diff`,
   `image mount`, `rebuild-cache` and `reindex`.
3. A command that cannot get its lock waits `repo.lock_timeout` seconds and
   then exits with code 2, with a message that names the lock file and the
   holder's pid. The holder writes its pid into the file.
4. **Cache lock.** `<cache>/lock` guards the cache directory in the same way.
   `rebuild-cache` takes it exclusively. Every other command takes it shared
   while it reads the cache, and exclusively for the moment it merges a new
   manifest.
5. **Drive lock.** A command that opens a drive for writing, or for a
   verification read, opens the device exclusively. Two commands never share a
   drive.
6. Locks are per repository. `restore` and `verify` may run against one
   repository while another repository's command runs.
7. The state log is appended under the exclusive lock only. A reader under a
   shared lock replays the log to the last valid record and ignores a partial
   tail.

The locks are advisory. They are not a security boundary.

---

## 7. Commit

### 7.1 Commit flow

```
0. Choose direct mode (section 7.3) or mirror mode (section 7.4).
1. Resolve the source roots and the parent snapshot. The parent is the
   current value of the ref being moved, resolved in this order: the local
   ref log, then the cache's ref table, then the ref table of the newest
   disc (section 5.1). No parent means a root snapshot.
2. Build the candidate file list: a full scan when --full-scan or --checksum
   is given, otherwise the quick check of section 7.2 against the parent
   snapshot's tree entry.
3. For every candidate file: chunk it with the configured profile; hash every
   chunk; query the filter union and confirm a positive against a manifest;
   compress and write every genuinely new chunk into staging; build the chunk
   list, or a chunklist object above chunklist.inline_max chunks.
4. For every directory, bottom-up: build the tree entries sorted by name
   bytes; build the TLV areas sorted by type; spill any TLV area above the
   threshold; hash and write the tree object.
5. Compare the new root tree id with the parent's root tree id. When they
   are equal, and --force is not given, stop here: write no snapshot object,
   move no ref, and report "no change" with exit code 0. Otherwise write the
   snapshot object with the root tree, the parent, the generation, and the
   metadata TLVs.
6. Record every new object in the state log as STAGED.
7. Append a ref record with run_seq 0 to the local ref log for the ref
   being moved, by default LATEST (section 5.1).
```

The shape of every object the flow writes is in FORMAT.md section 6.

`commit` is idempotent. Running it twice on an unchanged source writes no
second snapshot object.

NoahsArk has no built-in scheduler. `commit` is a batch job run by an external
scheduler, and the exclusive repository lock of section 6 keeps two commits
from running at once.

Phase 1 has manual `commit` only. There is no daemon. A watcher never commits
by itself.

### 7.2 The quick check

`commit` never writes to a source root. A source is read strictly read-only.
Every byte that NoahsArk creates goes into staging.

The walker compares these fields with the parent snapshot's tree entry.

| Field | Compared as |
|---|---|
| Size | Exact `u64` equality. |
| mtime | Seconds and nanoseconds. Exact equality on a local root. On a remote root a difference below `source.mtime_slack` counts as equal. |
| ctime | Seconds and nanoseconds, exact equality. |

If all compared fields are equal, the file is unchanged. The walker reuses the
entry's chunk list and never opens the file. If any one differs, the file is
read, chunked and hashed again.

The three-field form is the default for a local source. A remote source uses
size and mtime only. `source.quick_check` selects the form.
`metadata.ctime = false` also selects the two-field form, because a tree with
no ctime cannot be compared on it.

On a remote root, an mtime difference below `source.mtime_slack` counts as
equal, and the snapshot's `MTIME_SLACK` flag is set.

`commit --checksum`, alias `--full-scan`, disables the quick check and rehashes
every file.

### 7.3 Direct mode

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

### 7.4 Mirror mode

Mirror mode is Phase 2. It exists for a source that must not be held open for
hours.

`sync` runs four steps.

```
1. Obtain a stat listing of the source: path, type, size, mtime, ctime,
   mode, uid, gid, device number, inode number and link count.
2. Diff the listing against the parent snapshot's trees. Equal in all
   compared fields is unchanged; different or absent from the parent is
   changed; present in the parent and absent from the listing is deleted.
3. Write the changed paths to a file, then run
     rsync -aHAX --numeric-ids --files-from=<list> SOURCE staging/mirror/
4. Report the count and the byte size of the change set.
```

`commit --from=<mirror path> --source-root=<original path>` then runs.

```
5. Read the changed files from staging/mirror/ and chunk them.
6. Reuse the parent tree entry, unchanged, for every unchanged path.
7. Apply the deletions from the listing diff.
8. Record the original source root path in the snapshot, not the mirror path.
9. Derive every hardlink group id from the source device and inode numbers
   of the listing, never from the mirror copy.
10. Clear staging/mirror/ after the objects reach STAGED.
```

`--delete` is never used, because the mirror is not a full copy. Deletions come
from the listing diff, which is authoritative.

`sync` prints the exact `rsync` command before it runs it.

| `rsync` option | Reason |
|---|---|
| `-a` | Archive mode: recursion, times, mode, owner, group, symlinks. |
| `-H` | Preserve hard links inside the mirror. |
| `-A` | Preserve POSIX ACLs. |
| `-X` | Preserve extended attributes. |
| `--numeric-ids` | Never map ids through the local name service. |
| `--files-from` | Transfer only the change set. |

### 7.5 Source policy

| Rule | Default |
|---|---|
| Multiple roots | Allowed. |
| Root ordering | Sorted by path bytes. |
| Exclude syntax | The pattern language of FORMAT.md section 6.17. |
| Exclude sources | `sources.exclude` in config order, then `--exclude` in command-line order, then a `.noahsarkignore` file in any directory. |
| Sources | Read-only, never written. |
| Symlinks | Never followed. The link itself is stored. |
| Filesystem boundaries | Never crossed, under `sources.one_file_system`. |
| Special files | Recorded by type, with no content. |
| Unreadable file | Skipped and reported, exit code 1. |

An excluded path is not in the tree at all. The exclude rules are stored in the
snapshot as a TLV, so a later `ls` can explain why a file is absent.

The shape of the synthetic root tree and the root name encoding are in
FORMAT.md section 6.16. `restore SNAPSHOT TARGET` creates `TARGET/<root path>`
for each root.

An intermediate directory between `TARGET` and a source root that no tree entry
describes is created with mode 0700, the invoking user's uid and gid, and the
restore time as mtime. An intermediate directory that already exists is left as
it is. The restore reports every intermediate directory it created, once, in
the loss report under `intermediate_directory` with reason `SYNTHESIZED`, and
that alone does not change the exit code.

### 7.6 In-flight change detection

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

The two branches follow one rule: a snapshot never holds torn content without a
flag that says so.

`commit` exits with code 1 in both branches, and prints the count and the paths.
The report says which branch was taken: `parent` when the parent entry was
reused, `flagged` when new content was stored with the `UNSTABLE` flag.

Chunks already written to staging are kept in either branch. They are
content-addressed, so they cost nothing if the file settles, and GC removes
them if it does not.

`commit.restat_after_read` selects this detection. It must never be set false
on a live source.

`commit.retry_unstable` sets how many times an unstable file is re-read before
the rule applies.

### 7.7 Filesystem snapshots as the source

Committing a filesystem snapshot removes in-flight changes entirely. The
examples below are **informative**.

```bash
btrfs subvolume snapshot -r /srv/data /srv/.snap-noahsark
noahsark commit --source /srv/.snap-noahsark --source-root /srv/data
btrfs subvolume delete /srv/.snap-noahsark
```

LVM and ZFS follow the same shape: create a read-only snapshot, mount it,
commit it with `--source` and `--source-root`, then remove it.

`--source-root` records the original path in the snapshot, so a restore writes
to the original path and the temporary mount point never appears in the
archive. The same rule holds in mirror mode.

The quick check still works, because a filesystem snapshot preserves size,
mtime and ctime.

### 7.8 Remote source roots

A source root may be an NFS or an SMB mount. A source on such a mount is
untrusted for metadata.

| Item | Local | NFS | SMB / CIFS | Behaviour |
|---|---|---|---|---|
| ctime | Reliable | Server-dependent | Not reliable | `source.quick_check` defaults to `size_mtime` for a remote root. `NO_CTIME` is set. |
| mtime granularity | 1 ns | 1 ns to 1 s | 1 s to 2 s | A difference below `source.mtime_slack` counts as equal. `MTIME_SLACK` is set. |
| Hole detection | Supported | Usually supported | Often unsupported | Fall back to reading the whole file. `NO_SPARSE` is set. |
| Link count | Exact | Usually exact | Often not exposed | A mount that exposes neither link counts nor inode numbers detects no group. `NO_HARDLINKS` is set. |
| uid, gid, mode | Real | Real with matching id maps | Often synthesized | Recorded as seen. `SYNTHETIC_IDS` is set, and `restore` warns once. |
| Extended attributes and ACLs | Full | Partial | Rarely | Recorded when readable. Absent otherwise, and reported in the loss report. |
| Name case | Distinguished | Distinguished | Often not distinguished | Names are stored as `readdir` returned them. `CASE_INSENSITIVE` is set. |
| In-flight change | Detected | Detected | Detected | The rule of section 7.6 is unchanged. |

`source.checksum_every` forces a periodic full rehash on a remote root. Once
the interval has passed, `commit` behaves as if `--checksum` were given, and
the snapshot records that it was a checksum commit.

Every one of these facts is recorded in the snapshot's `source_type` and
`source_flags` fields, whose registries are in FORMAT.md section 6.15.

`source.allow_smb` allows an SMB mount as a source root. It is never allowed
for staging.

Restoring to an NFS or SMB target follows the non-root metadata policy of
section 15.5.

### 7.9 Commit bundles

Commit bundles are a Backlog item. A remote machine that can run the binary
exchanges directories instead of using a network protocol.

The server runs `catalog export`, the exported catalog travels to the source
machine, the source machine runs `commit --catalog ... --out ...`, the bundle
travels back, and the server runs `import`, then `pack` and `burn`. The bundle
travels by rsync, ssh, scp or a USB disk. Nothing in the format depends on how
it arrived.

`catalog export` writes the current catalog as plain files: every run filter,
the recent manifests, every snapshot object, the snapshot table, the ref table,
the run table and the disc directory. It is the same content that a run
carries, in the same formats.

`commit --out` walks the source machine's own filesystem, chunks and hashes
locally, and queries the exported filters. A filter negative is a proof of
absence, so the chunk is new and goes into the bundle. A filter positive can
only be confirmed when the exported catalog holds the matching manifest;
without it, the chunk is included. The count of such chunks goes into
`filter_positive_unconfirmed` of `BUNDLE.bin`.

`import` verifies the content id of every file, drops every object it already
has, enters the rest into the staging state machine as STAGED, and records the
snapshot and the ref move. Import is idempotent.

Section 3.7 gives the bundle header layout.

---

## 8. Packing and locality

### 8.1 Packing rules

The rules are ordered. A conflict is resolved by this order.

1. **A file's chunks go in one run.** The only exceptions are the two cases of
   section 8.6.
2. **A directory's files go in one run** when the directory fits, in
   depth-first path order.
3. **Siblings stay adjacent.** Objects are written in depth-first path order
   inside a run, so a partial-directory restore is one linear read. FORMAT.md
   section 8.6 states that order in full and makes it total.
4. **Split only when forced.** Split at the tail: fill the current run,
   continue on the next. Record the split so the planner puts the two discs
   next to each other.
5. **Metadata is written first inside a run**, contiguous, and the catalog is
   replicated on every run.
6. **Never break rule 1 to gain a few percent of utilization.**

`pack` fills exactly one run per invocation. A staged set larger than one run
leaves the remainder STAGED for the next invocation. A multi-disc pack is
repeated invocations, never one invocation that spans discs.

Informative: the reference packer uses a pre-pass that gives a file larger than
one disc its own run chain, a first pass that assigns whole directories to the
current run in depth-first path order while they fit, and a second pass that
fills the tail of each run with first-fit-decreasing over the leftover files,
choosing leftovers from the directory nearest in path order.

### 8.2 Controlled duplication

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

1. Chunk the segment. For each chunk, query the filter union and confirm
   against a manifest. Build the map `run -> count of chunks this run could
   supply`.
2. Sort runs by count, descending. Keep the top `locality.max_source_runs`.
   Drop any run that supplies fewer than `locality.rewrite_below_chunks`.
3. Chunks available from a kept run are referenced, not written.
4. All other chunks are written into the new run, even though a copy exists
   elsewhere.
5. Record the exact set of referenced run seqs in the manifest chunk `"SRCR"`,
   and the count in the run header.

A withdrawn run is removed before step 1 builds the map, so it can neither be
kept nor suppress a rewrite.

The knobs are ranked. When they conflict, a higher rank wins.

1. The per-file caps, `locality.max_duplicate_bytes_per_file` and
   `locality.max_duplicate_ratio_per_file`. A file whose rewrite would exceed
   either cap keeps its cross-run references, even above
   `locality.max_source_runs`.
2. `locality.max_source_runs` and `locality.rewrite_below_chunks`, per segment.
3. Rule 1 of section 8.1. It never forces a rewrite that rank 1 forbids.
4. `locality.disc_budget`, per disc. When the budget is reached, the packer
   stops rewriting and references older runs for the rest of the disc, and the
   health report says so.

Controlled duplication writes an object again only onto a different disc.
FORMAT.md section 8.6 states that an object the target disc already holds is
never written again.

### 8.3 Locality presets

Presets:

| Preset | `max_source_runs` | Effect |
|---|---:|---|
| `dedup` | unlimited | Maximum space saving. A restore may need every disc. |
| `balanced` | 8 | **Default.** A snapshot restores from at most about 9 runs per segment. Expect a few percent of dedup loss. |
| `locality` | 2 | A snapshot restores from at most 3 runs per segment. Good for one disc set per project. |
| `standalone` | 0 | No cross-run references at all. Every disc set is readable with no other disc. Costs the most media and gives the strongest durability story. |

`standalone` sets `locality.max_source_runs` to 0 and lifts the three caps that
rank above it: `locality.max_duplicate_bytes_per_file`,
`locality.max_duplicate_ratio_per_file` and `locality.disc_budget` become
unlimited. A user who sets `locality.max_source_runs = 0` by hand without
lifting the caps gets rank 1 behaviour: a file above a cap keeps its
references.

### 8.4 Duplication accounting

Every run records, in the manifest chunk `"DUPS"`:

| Field | Meaning |
|---|---|
| `duplicated_bytes` | Bytes written again for locality. |
| `duplicated_objects` | Objects written again. |
| `unique_bytes` | Bytes that exist only in this run. |
| `dedup_saved_bytes` | Bytes not written because an older run supplies them. |

The fields are written in that order. The run header repeats
`duplicate_bytes`, and every manifest record for a duplicated object sets
`record_flags` bit 1. FORMAT.md section 11.2 gives the records.

The tool prints the duplication overhead after every burn and keeps a running
repository figure. An overhead above 5 percent is a warning.

### 8.5 Capacity budget

The packer must fit a run inside the data budget. Section 9 gives the formula
and every term. `free_now` is that budget minus the sectors already used on the
disc, in bytes.

`pack --dry-run` prints the whole budget before anything is written.

The packer must read a forced capacity from the superblock of the target disc,
not from the drive.

Under profile 2 the packer must include the projected append cost of section
10.6 in its capacity budget.

### 8.6 Split threshold

A file is split across runs only when one of two conditions holds:

```
file_size > data_budget * 2048                                 # larger than a disc
file_size - free_now > split.threshold * data_budget * 2048    # too far over the rest
```

`free_now` is the free part of the data budget of the current disc in
bytes (section 8.5), `data_budget` is in sectors (section 9), and
`split.threshold` defaults to 0.25. Otherwise the packer starts a new run
for the file.

When a split happens, the packer places the parts on discs that the plan will
order adjacently, and records the split in the manifest chunk `"SPLT"` of every
run that holds a part. The chunks of the other part are prerequisites of each
run. FORMAT.md section 11.2 gives the split record.

### 8.7 Consolidation

Consolidation writes a fresh, self-contained set of discs that carries every
object the current snapshot needs, with no reference to older discs. Old discs
are never erased.

Triggers, evaluated after every burn:

| Trigger | Key | Default |
|---|---|---|
| The restore plan touches too many discs | `consolidate.max_plan_discs` | 20 |
| Spread ratio: plan discs divided by `ceil(snapshot_bytes / disc_capacity)` | `consolidate.max_spread_ratio` | 2.0 |
| Estimated restore time | `consolidate.max_restore_hours` | 8 |
| The oldest disc in the plan is too old | `consolidate.max_disc_age` | 5 years |
| A disc in the plan failed verification | - | always |

`health` reports the trigger metrics, so a consolidation is visible before it
is needed.

---

## 9. Capacity estimator

FORMAT.md section 9 carries the three capacity names, `data_budget`,
`fill_limit_sectors` and `reserve`, and the five invariants that every writer
must hold. This section is the estimator a writer runs to choose the numbers.

A writer that uses another estimator must still hold every invariant of
FORMAT.md section 9, and must record what it used.

### 9.1 Fill policy

- The packer uses the forced capacity of section 12.6, which equals the
  drive-reported capacity unless the user set `--capacity` or
  `disc.force_capacity`.
- `disc.fill_ratio` fixes the safety margin.
- `data_budget` and `fill_limit_sectors` are the only capacity figures that the
  packer, the superblock and the close policy use.
- When the remaining POW spare area falls below `disc.min_spare_ratio`, the
  tool warns and recommends no further ordinary appends to that disc.
- `disc.spare_reserve_bytes` follows `disc.spare`. It is reserved on every disc
  formatted for POW, which is every disc except a sealed one.
- Under profile 2 the packer also reserves the projected directory rewrite cost
  of the remaining appends.
- One growisofs command line covers every media size. Capacity comes from the
  drive, never from a hardcoded number.

### 9.2 Media capacity table

FORMAT.md section 2.6 carries the media type registry. The table below is the
planning view of the same sizes.

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

### 9.3 The reference estimator

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

### 9.4 Estimator terms

Terms:

| Term | Formula | Default input |
|---|---|---|
| `safety_margin` | `ceil(capacity_forced * (1 - disc.fill_ratio))` | `disc.fill_ratio` = 0.95 |
| `superblock_and_headers` | `1 + ceil(readme_bytes / 2048) + ceil(format_bytes / 2048) + expected_runs * 2` | See below. |
| `catalog_growth` | `expected_runs * ceil(catalog_bytes_per_run / 2048)`, see below | `disc.expected_runs` |
| `spare_area` | `ceil(disc.spare_reserve_bytes / 2048)` on a `spare:min` disc, 0 on a sealed disc (`spare:none`) | 256 MiB when `disc.spare` = `min`, 512 MiB when `disc.spare` = `default` |
| `alignment_padding` | `expected_runs * 16` | - |
| `parity_headers` | `m` | `m` = 23 |
| `fec_overhead` | `fec_region - data_budget`, which is `stripes * (m + 1) + (fec_region - stripes * 255)` | The two parts are the checksum and parity sectors of every whole stripe, and the fewer than 255 sectors of an incomplete stripe, which stay unused. |

`superblock_and_headers` counts one sector for `DISC.bin`, the sectors of
`README.txt` and `FORMAT.txt`, and two sectors per expected run for `RUN.bin`
and `RUN2.bin`. The other `m` header copies sit in the first sector of each
parity file, and `parity_headers` counts those. `readme_bytes` and
`format_bytes` are the actual sizes of the two files the writer is about to
write. Both have a normative cap: `README.txt` is at most 16 KiB (FORMAT.md section 8.4) and `FORMAT.txt` is at most 64 KiB (FORMAT.md section 8.5), so the term is
never above `1 + 8 + 32 + expected_runs * 2`. The worked examples below use
the caps.

### 9.5 `disc.expected_runs` by profile

`disc.expected_runs` defaults by profile, because the profiles differ in how
many runs a disc can receive:

| Profile | Default `expected_runs` | Reason |
|---:|---:|---|
| 0, `oneshot` | **2** | A profile 0 disc holds one data run at burn time. A second slot is reserved for the repair run that a Phase 2 append may add later (sections 12.2 and 13.4), so the catalog copy of that later run has somewhere to go. Reserving for 32 would still waste about 0.5 percent of the disc, so the default stops at 2. |
| 1, `udf201-pow` | 32 | The disc receives appends. |
| 2, `iso9660v1-l4-pow` | 32 | The disc receives appends. |

### 9.6 `catalog_growth`

`catalog_growth` is exact integer arithmetic over five named inputs. Every
input is an integer number of bytes.

```
objects_per_run       = ceil(capacity_forced * 2048 / chunk_avg)
filter_bytes          = 84 + ceil(objects_per_run * 91 / 40)
manifest_bytes        = 64 * objects_per_run + 4096
table_bytes           = catalog.expected_snapshots * (snapobj_bytes + 136)
                      + catalog.table_reserve_bytes
catalog_bytes_per_run = earlier_runs * filter_bytes
                      + manifest.history_depth * manifest_bytes
                      + table_bytes
catalog_growth        = expected_runs * ceil(catalog_bytes_per_run / 2048)
```

`chunk_avg` is the average chunk size of the chunker profile of FORMAT.md
section 4.3. `earlier_runs` is set to `disc.expected_runs`. `objects_per_run`
is a planning figure only; the packer never limits a run by it.

Under profile 2 the projected directory rewrite cost of section 10.6 is added
to `catalog_bytes_per_run`.

The packer solves the budget in one pass: it subtracts every fixed term from
the forced capacity, splits what is left into whole stripes, and gives `k` of
every 255 sectors to data. `data_budget` is therefore always a whole number of
stripes of `k` data sectors.

### 9.7 Recompute after the catalog cap remedy

**Recomputing after the catalog cap remedy.** When the mandatory items of a run's
catalog (priorities 1 to 4 of FORMAT.md section 11.7) alone exceed `catalog.max_bytes`,
the writer raises its working `catalog_bytes_per_run` to that mandatory size
plus one manifest, and applies the difference as an extra reserve on every
remaining run of the disc:

```
extra_bytes_per_run  = new_catalog_bytes_per_run - catalog_bytes_per_run
extra_sectors_total  = ceil(extra_bytes_per_run / 2048) * remaining_runs
data_budget           = data_budget - extra_sectors_total
```

`remaining_runs` is `disc.expected_runs` minus the runs already burned on
that disc. On a new disc the raised `catalog_bytes_per_run` enters
`catalog_growth`, and therefore `reserve_computed_sectors`, before the first
run is burned. On an existing disc the superblock's recorded values never
change; the packer applies `extra_sectors_total` to the data budget of the
run it is about to write, and reports both the old and the new figure.

### 9.8 Consequence with no override

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

### 9.9 Overrides

- `disc.force_reserve` replaces the computed reserve. It accepts bytes or a
  percentage of the forced capacity.
- `disc.extra_reserve` is added to the computed reserve. It accepts the same
  forms.
- Both are recorded in the superblock next to the computed value, so a later
  append uses the same budget.
- With an override in force, the packer rounds `data_budget` down to a whole
  number of stripes of `k` data sectors.

---

## 10. Disc filesystems and image building

FORMAT.md section 8.1 names the three filesystem profiles a reader must know.
This section is the writer's side: how each image is built and burned.

### 10.1 Profile 0 image build

The profile 0 and profile 1 filesystem is pure UDF at revision 2.01, block
size 2048. There is no ISO 9660 bridge, no Joliet and no Rock Ridge.

Four things in the image build are normative: `--media-type=hd`,
`--blocksize=2048`, `--udfrev=2.01`, the option order with `--utf8` first and
every override after `--media-type`, and the absence of `--spartable`. Any
build that reaches them conforms, whatever tool or script produces it.

- Never use `--media-type=bdr` or `dvdr`. Both make a write-once VAT volume,
  which cannot be populated.
- Never use `--spartable`. A sparing table adds a second logical-to-physical
  indirection that breaks the parity map.

The image is built at the forced capacity rounded down to a multiple of 16
sectors.

The script below is **informative**.

```bash
eval "$(growisofs -F /dev/sr0)"        # sets next_session= and capacity=
BLOCKS=$(( ${FORCED_SECTORS:-$(( capacity / 2048 ))} ))
BLOCKS=$(( BLOCKS - BLOCKS % 16 ))     # 32 KiB alignment
truncate -s $(( BLOCKS * 2048 )) run.udf
mkudffs --utf8 --media-type=hd --blocksize=2048 --udfrev=2.01 \
        --label=NOAHSARK-0001 --uid=0 --gid=0 --mode=0555 \
        --bootarea=erase run.udf
```

The label comes from `label.template`. The remaining options are the reference
implementation's choices.

### 10.2 Profile 0 burn paths

The burn is one growisofs call. Two variants exist. Section 12.2 states the
close policy that selects between them.

**Default: POW-formatted and left open.**

```bash
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=run.bin
```

- `spare:min` formats the blank BD-R for Pseudo-OverWrite with the
  maximum-capacity descriptor, so the spare area is as small as the drive
  allows.
- `-dvd-compat` is not passed. The disc stays open.
- `run.bin` is the used prefix of the image, as section 12.3 defines it, and
  holds the whole payload up to the data budget.
- NoahsArk never writes a UDF anchor itself. Anchors are `mkudffs`'s business.

**Sealed: `pack --close`.**

```bash
growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:none,tty \
          -Z /dev/sr0=run.bin
```

- `spare:none` skips the format step entirely. There is no spare area and no
  defect management, capacity is full, and LBAs are stable forever.
- `-dvd-compat` closes the disc. The choice is permanent.
- `run.bin` is the full-size image, not the used prefix, so the same call
  writes the used prefix, the zero middle and the tail anchors.
- `disc.close_policy = always` selects this path for every new disc.

Informative: `-speed=4` above is an example. `burner.speed` and
`burner.speed_mdisc` set the real value.

| Option | Format | Defect management | LBA stability | Append |
|---|---|---|---|---|
| `spare:none` | None | Off | Stable | Impossible |
| `spare:min` | Maximum-capacity descriptor, SRM+POW | On | Blocks may move | Possible |

Profile 0 writes one run, so the LBA re-verification of section 10.5 is not
needed. The tool still reads the disc back and checks every object.

A drive that offers no POW feature, or that reports plain `BD-R SRM` after a
format attempt, forces the sealed variant of profile 0.

### 10.3 LBA read-back

Under profile 0 and profile 1 the primary method parses the UDF File Entry. It
is the only exact method. It works on an unmounted image, needs no root, and
gives every extent of a fragmented file.

- Allocation descriptors hold partition-relative block numbers.
- Add the Partition Descriptor start, which `udfinfo` prints as
  `start=... type=PSPACE`.
- The parser needs read access only.

`filefrag` is a cross-check only. `udfinfo` gives volume-level layout only,
never a per-file LBA.

Under profile 2 the read-back uses `isoinfo -l` with `-T <sector>`, and the run
header records the value in `session_start_sector`.

### 10.4 Placement order

The writer copies files into the mount one at a time, single-threaded, in fill
order. Copy order equals physical LBA order.

The writer must never use `cp -r` on a directory. The order would then follow
`readdir`, not the fill order.

FORMAT.md section 8.6 gives the fill order itself.

Informative: the reference implementation builds a run in two passes. The first
pass copies placeholder files of their final size to learn every extent. The
second pass rewrites each placeholder in place with its final bytes, in the
order FORMAT.md section 8.6 fixes. No file moves between the passes.

Informative: chunk and hash in parallel across files, and write the run image
single-threaded, because copy order is LBA order.

### 10.5 Profile 1 append

Profile 1 is Phase 2. It is profile 0 plus append, and it uses exactly the same
on-disc structures. Everything in sections 10.1 to 10.4 applies unchanged.

Both appendable profiles use Pseudo-OverWrite growth on a formatted BD-R. Only
the filesystem step differs.

| Step | Variant 1a | Variant 1b | Profile 2 |
|---|---|---|---|
| Format | `spare:min` on the first write | `spare:min` on the first write | `spare:min` on the first write |
| Build | `mkudffs`, then a mount of the disc itself | `mkudffs`, then a loop mount of an image mirror | `genisoimage`, run by NoahsArk |
| Append | Mount the device read-write with the kernel udf driver, copy files in fill order | Copy into the mirror, diff 32 KiB blocks, write each changed run with a seeking write | `genisoimage -C -M` builds the session image, then one growisofs `-M` call |
| Burner used for the append | None. The kernel writes. | growisofs, one call per changed run | growisofs, one call |
| LBA read-back | Parse UDF File Entries | Parse UDF File Entries | `isoinfo -l -T <session>` |

Both variants produce the same disc bytes. A reader cannot tell them apart. The
superblock records the choice in `append_variant`.

After every append the tool must read back the LBA extents of every object and
compare them with the recorded layout. The tool must fail the append if any
object moved.

Until probe 1 passes on a given drive model, the implementation must use
variant 1b.

**Variant 1b**, five normative steps:

1. Keep an image mirror. A sparse file of exactly the disc capacity lives at
   `staging/images/<disc_uuid>.img`. It is derived data and can be rebuilt from
   the disc with `ddrescue`.
2. Edit the mirror. Loop-mount it read-write, copy the new objects in fill
   order, one file at a time, and unmount.
3. Diff. Compare the mirror against the previous recorded image state. Compute
   the set of changed 32 KiB blocks.
4. Write. Write each changed, 32 KiB-aligned byte run with a seeking write
   whose LBA is a multiple of 16.
5. Read back. Compare the LBA extents of every object with the layout table.

The block diff must produce one write per changed 32 KiB block, never several.
A writer that patched a directory block twice in one append would consume two
spare blocks for one logical change.

Informative: one conforming rendering of step 4 is
`growisofs -use-the-force-luke=seek:N,spare:min,tty -Z /dev/sr0=run.bin`.

An append must not rewrite more than `floor(m / 2)` blocks that fall into one
stripe of any earlier run. The block diff checks this against the earlier runs'
layout tables before it writes. FORMAT.md section 10.6 states the bound.

### 10.6 Profile 2 build and burn

Profile 2 is Phase 3. It uses ISO 9660:1999 level 4, plain. Levels 1, 2 and 3
are forbidden. Rock Ridge and Joliet are forbidden.

Profile 2 must not be used until probe 2 passes: Windows must read ISO
9660:1999 level 4 long lowercase names on real media.

Profile 2 uses one hex fan-out level only.

NoahsArk builds every ISO 9660 image itself with genisoimage and hands the
finished image to growisofs. growisofs never runs genisoimage on NoahsArk's
behalf, because the placeholder rewrite needs the image bytes before they are
burned. The writer runs genisoimage twice per run: once with placeholders to
learn the extents, then again with the final bytes. A difference in extents is
a defect.

| Option | Effect |
|---|---|
| `-iso-level 4` | ISO 9660:1999. Long lowercase names, no `;1`, no depth cap. |
| `-D` | No deep relocation. Legal at level 4. |
| `-l` | Allow 31-character ISO names. A no-op at level 4, kept as a guard. |
| `-allow-limited-size` | Guard for a file above 4 GiB. NoahsArk never writes one. |
| `-no-limit-pathtables` | Lifts the path-table entry limit. |
| `-sort <file>` | LBA placement order. |
| `-V <label>` | Volume label. |

Forbidden options: `-R`, `-r`, `-J`, `-joliet-long`, `-udf`, and every level
below 4.

The script below is **informative** in its speed, label and file-name choices.

```bash
GOPT="-iso-level 4 -D -l -allow-limited-size -no-limit-pathtables"
# First write on a blank BD-R. Build the image, then burn it.
genisoimage $GOPT -sort sortfile -V ARK-0001 -o first.iso /srv/ark/tree
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=first.iso
# Every later append. LAST and NEXT come from growisofs -F on the disc.
genisoimage $GOPT -sort sortfile -V ARK-0001 -C ${LAST},${NEXT} \
            -M mirror.iso -o add.iso /srv/ark/tree
growisofs -speed=4 -use-the-force-luke=spare:min,tty -M /dev/sr0=add.iso
# Final append adds -dvd-compat to close the disc.
```

**Placement order.** `genisoimage -sort <file>` sets the LBA placement order.
The file holds `<path> <weight>` pairs, one per line. A higher weight is placed
closer to the start of the medium. The writer emits one line per object, with
strictly decreasing weights, in the fill order of FORMAT.md section 8.6.

`genisoimage -M` rewrites the entire directory tree and the path tables into
the new session image, so the append cost grows with the object count. The
packer must include the projected append cost in its capacity budget, and must
never treat it as noise.

---

## 11. Burn plan and burning

### 11.1 Burning is externalized

The program does not burn as a core function.

| Step | Owner | Command |
|---|---|---|
| Build the run image and the burn plan | The program | `noahsark pack` |
| Render the plan into command lines | The program | `noahsark burn --print` |
| Execute those command lines | An external burner | `noahsark burn --exec`, Linux only |
| Read the disc back and check every object | The program, always | `noahsark verify` |

`burn --print` writes command lines to standard output. It never touches a
device, and it works on every platform. `burn --exec` exists only on Linux; it
runs exactly the commands that `burn --print` produced, in order, and nothing
else. It must not build a command line of its own.

`verify` always belongs to the program. Only the program knows the layout
table, the manifest and the content ids.

Profile 1 variant 1a needs no burner for an append. The plan then holds mount,
copy and unmount steps instead of write steps.

`pack` writes the plan as `burn.bin` and a human rendering as `burn.json`. The
binary file is authoritative. Informative: the default place is
`staging/plans/<run_seq>/`.

### 11.2 Burn plan container header

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
| 91 | 1 | u8 | `burner_backend` | 1 growisofs, 2 cdrskin, 3 ImgBurn, 4 IMAPI (**reserved**; no `--backend` value and no `burner.backend` value selects it, and a version 1 writer never writes it), 5 hdiutil, 6 kernel (variant 1a). |
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

### 11.3 Burn step record

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

### 11.4 Plan validation rules

1. `burn --print` must refuse a plan whose `header_crc32c` or any
   `step_crc32c` fails.
2. `burn --exec` must confirm `expected_nwa_sectors` and `capacity_sectors`
   against the drive before the first write. A mismatch is a hard error.
3. `burn --exec` must confirm `payload_hash` for every step before it writes.
4. A seeking step with `seek_lba` not a multiple of 16, or `byte_len` not a
   multiple of 32768, is a defect in the plan. Both `--print` and `--exec` must
   refuse it.
5. `burn --print` must refuse a seeking step under a backend that cannot
   express a seek, and must name the backend that is required.
6. `close_disc` and the `-dvd-compat` flag are set only by a plan that
   `pack --close` or `disc.close_policy = always` produced for the first and
   only write of a profile 0 disc, by a plan that `close` produced, or by
   `pack` under `disc.close_policy = when_full`. Under the default policy,
   `never`, no plan ever carries them.
7. A `source_path` above 256 bytes or an `aux_path` above 184 bytes is an
   error. The plan writer refuses to write the plan, `pack` exits with code 2,
   and the message names the path and the limit. The path is never truncated
   and never stored in a side file. Informative: a short `staging.dir` keeps
   every plan path far below the limit.

A burn plan is validated before it is rendered or executed: every CRC, every
seek alignment, every payload hash.

Burn plan `burner_backend` id 4, IMAPI, is reserved. A version 1 writer never
writes it, no `--backend` value and no `burner.backend` value selects it, and
the JSON rendering never names it.

### 11.5 The sorted tree listing

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
5. the content id of the file bytes in the text form of FORMAT.md section 3.4, that is
   68 lowercase hex characters under `hash.current`;
6. one line feed byte, 0x0A.

Lines are sorted ascending by the relative path bytes, unsigned, as section
8.10 defines ascending. The listing ends with the line feed of its last line
and has no trailing blank line. An empty tree serializes to zero bytes.
`payload_hash` is the hash of those bytes under `hash.current`.

### 11.6 JSON rendering

`burn.json` is derived from `burn.bin` and is never authoritative.

| Field | Type | Meaning |
|---|---|---|
| `format` | string | Always `noahsark-burn-plan`. |
| `version` | integer | 1. |
| `repo_uuid`, `disc_uuid` | string | Hyphenated lowercase uuid text. `disc_uuid` is all zero for a blank disc. |
| `disc_seq`, `run_seq` | integer | As in the container. |
| `media_type`, `fs_profile` | string | Media type and profile registry names. |
| `append_variant` | string | `1a`, `1b`, or absent. |
| `burner_backend` | string | `growisofs`, `cdrskin`, `imgburn`, `hdiutil`, `kernel`, for the values 1, 2, 3, 5 and 6. Value 4 is reserved and never written, so the rendering never produces a name for it. |
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

### 11.7 Command templates

What a rendered command must do, and the set of values it may use, are
normative. The template mechanism and its syntax are **informative**.

`burn --print` must render, for each backend and step kind, a command line that
performs exactly the action of section 10.2, 10.5 or 10.6 for that step, with
these values and no others taken from the burn plan and the config.

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

A rendered command must carry no value that this table does not name, and must
never carry a value that the plan does not hold.

The spare mode is `spare:min` under every profile by default. It is
`spare:none` only when the step has `close_disc` set on the first write of a
profile 0 disc.

Every rendering also prints, as comments:

- the probe commands for the next writable address and the media info;
- the expected next writable address and the expected capacity;
- the eject and reload line before verification;
- the exact `noahsark verify` command to run afterwards.

The table below is the reference implementation's template set. It is
**informative**: it shows one correct rendering, in Go `text/template` syntax.

| Backend | OS | Step | Template |
|---|---|---|---|
| `growisofs` | Linux | first write, profile 0, 1 and 2 | `growisofs -speed={{.Speed}} -use-the-force-luke={{.SpareMode}},tty {{if .DvdCompat}}-dvd-compat {{end}}-Z {{.Device}}={{.SourcePath}}` |
| `growisofs` | Linux | seeking write, profile 1 variant 1b | `growisofs -speed={{.Speed}} -use-the-force-luke=seek:{{.SeekLBA}},spare:min,tty {{if .DvdCompat}}-dvd-compat {{end}}-Z {{.Device}}={{.SourcePath}}` |
| `growisofs` | Linux | append, profile 2 (kind 3) | `growisofs -speed={{.Speed}} -use-the-force-luke=spare:min,tty {{if .DvdCompat}}-dvd-compat {{end}}-M {{.Device}}={{.SourcePath}}` |
| `genisoimage` | Linux | build image, profile 2 (kind 2) | `genisoimage -iso-level 4 -D -l -allow-limited-size -no-limit-pathtables -sort {{.AuxPath}} -V {{.Label}} {{if .Append}}-C {{.LastSession}},{{.SeekLBA}} -M {{.MirrorImage}} {{end}}-o {{.OutputImage}} {{.SourcePath}}` |
| `kernel` | Linux | mount, copy, unmount, profile 1 variant 1a | `mount -t udf -o rw {{.Device}} {{.AuxPath}}`, then one `cp` per object in fill order, then `umount {{.AuxPath}}` |
| `cdrskin` | Linux | write image | `cdrskin dev={{.Device}} speed={{.Speed}} -multi -tao {{.SourcePath}}` |
| `imgburn` / `hdiutil` | Windows / macOS | write image | `ImgBurn.exe /MODE WRITE /SRC "{{.SourcePath}}" /DEST {{.Device}} /SPEED {{.Speed}} /START /CLOSE`, and `hdiutil burn -device {{.Device}} -speed {{.Speed}} {{.SourcePath}}` |

`cdrskin`, ImgBurn and `hdiutil` cannot write at an arbitrary LBA and cannot
build an ISO 9660 append. A plan that needs either therefore selects the
`growisofs` backend, or falls back to profile 0.

Burning on tier-2 operating systems is not implemented in version 1.
`burn --print` already renders the command lines.

### 11.8 Probe commands

Capacity and the next writable address come from the drive, never from a
hardcoded number.

```bash
eval "$(growisofs -F /dev/sr0)"        # prints next_session=<bytes> capacity=<bytes>
NWA=$(( next_session / 2048 ))
```

Media type comes from `dvd+rw-mediainfo`.

```bash
dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Number of Sessions|Next Writable Address|Free Blocks|Track Size'
```

`BD-R SRM+POW` means the disc is appendable in place. `BD-R SRM` means it is
not. That one string decides between an appendable profile and profile 0.

Common rules for every burn:

- A seeking write requires an LBA that is a multiple of 16.
- Both the byte offset and the byte length of a seeking write must be multiples
  of 32768.
- Never use `-overburn`.
- Never let a drive use BD-R Random Recording Mode.
- Never pass `-M` on a UDF disc.
- Under profile 1, gate every burn on `udfinfo <image>` reporting
  `integrity=closed`.
- Eject and reload before every verification read.

### 11.9 Burner backends and version check

| Backend | Tool | Version requirement | Status |
|---|---|---|---|
| `growisofs` | dvd+rw-tools | Debian 7.1-14 or newer, Fedora 7.1-13 or newer, Arch 7.1-13 | Default. |
| `kernel` | Linux udf driver | Kernel 5.4 or newer | Profile 1 variant 1a appends only. |
| `cdrskin` | libburn | 1.5.8 or newer | Fallback, profile 0 only. |

The tool must check the burner version at startup. `burn --print` must warn
when the version is unknown or unpatched. `burn --exec` must refuse.

The tool pins and checks these external tool versions at startup:
`growisofs`, `mkudffs`, `udfinfo`, `genisoimage` and `ddrescue`. Section 24
gives the commands.

xorriso is not used.

### 11.10 Fallbacks

This table is the normative fallback table.

| Condition | Fallback |
|---|---|
| The drive offers no POW feature. | Use profile 0 sealed: `spare:none`, one run, `-dvd-compat`. |
| `dvd+rw-mediainfo` reports `BD-R SRM` after a format attempt. | Same. |
| The kernel refuses to mount the device read-write on a POW BD-R. | Use profile 1 variant 1b, the image mirror and the block diff. |
| The UDF append path is unavailable, or a user wants genisoimage-managed appends. | Use profile 2, after the Windows name check passes. |
| Windows truncates ISO 9660:1999 long names on real media. | Profile 2 must not be used. Stay on profile 1. |
| growisofs fails, or the build is unpatched. | Use the `cdrskin` burner backend, and therefore profile 0 sealed. |
| An object moved after an append. | Abort the append. Keep the objects PACKED. Mark the run for re-burn. Report the moved ids. |
| The image mirror is lost. | Rebuild it with `ddrescue` from the disc. |
| A burn fails, or a verify fails. | The `run_seq` of the failed run is never reused. The re-burn is a new plan with the next `run_seq`. |

### 11.11 Pre-burn verification checklist

Pre-burn, on the image or the staged tree:

1. Profile 1: `udfinfo IMG` shows `udfrev=2.01`, `blocksize=2048`,
   `integrity=closed`, `accesstype=overwritable`, and three `type=ANCHOR` lines.
2. Profile 2: `isoinfo -d -i IMG` reports `NO Joliet present` and `NO Rock
   Ridge present`.
3. Profile 2: a raw-byte check finds the full 68-character lowercase name in
   the image. A mount is not evidence.
4. A read-only loop mount of the image succeeds, and the file count under the
   mount matches the object count plus the fixed files.
5. The longest path under the mount is under 220 characters.
6. No name under the mount holds a forbidden character.
7. The image size is a multiple of 32768 bytes.
8. The per-object LBA map is extracted and stored in the layout table.
9. A dry-run burn passes the overburn check.

Post-burn and cross-OS checks are in section 23.

On damaged media, read with ddrescue in two passes: a fast pass with no
scraping, then a slow pass through the same map file. Plain `dd` must not be
used. The ddrescue mapfile is the erasure list for the FEC layer.

---

## 12. Disc lifecycle, closing and appending

### 12.1 Lifecycle states

FORMAT.md section 7.15 states where each state is recorded. The table below is
the state set and what enters each state.

| State | Meaning | Entered by |
|---|---|---|
| `blank` | The medium as it comes from the manufacturer. No NoahsArk state exists. | The disc's manufacturing. |
| `POW-formatted` | The medium has a Pseudo-OverWrite spare area, `spare:min` or `spare:default`. No filesystem or data exists yet. | The first `pack` / `burn --exec` of a profile 0 or profile 1 disc, before that same burn's image is written (section 10.2). Profile 2 skips this state; it has no POW spare area. |
| `open` | The disc holds a filesystem and at least one run, is not sealed, and has not yet received an append. | The same first burn as `POW-formatted`, when `disc.close_policy` is not `always` and `pack --close` was not given. |
| `appended` | The disc has received at least one append run after its first. Still not sealed; may receive more appends. | `pack` or `append` against an existing `--disc` (profile 1 or profile 2 only; section 10.5). Appending again re-enters this same state. |
| `sealed` | The disc is closed: no later run is possible. Permanent, and recorded in the run chain and the disc directory, never by a change to the superblock. A disc sealed at its first write also has `sealed` 1 in its superblock; a disc closed later has `sealed` 0 there and `run_flags` bit 0 set in its newest run header (FORMAT.md sections 7.5, 7.6 and 7.12). | `pack --close` on a new disc, or `noahsark close` (Phase 2) on an `open` or `appended` disc, or `close_policy = always` on the first burn. |
| `degraded` | The library has a manual or a scrub-derived reason to distrust the disc, short of writing it off. Not permanent; a later `disc mark-degraded --health=...`, or a passing `verify`, supersedes the record and returns the disc to the state its run chain describes. | `disc mark-degraded --health=degraded` or `--health=critical` (section 16.23), or the health computation of section 13.5 when a `verify` or `scrub` finds the disc `CRITICAL` or `FAILED`. Reachable from `open`, `appended` or `sealed`; a sealed disc can still degrade physically. |
| `withdrawn` | The operator has given up on the disc: it is excluded from restore planning and from future appends, though its objects remain wherever another copy also has them. | `disc mark-degraded --health=failed`, which records `health` 4, the terminal case. This is an operator judgment recorded in `<repo>/notes.bin` (section 3.4), not a burn; it never touches the medium. |

Every transition except the recovery out of `degraded` is one-way.

`withdrawn` is an operator judgment recorded in `<repo>/notes.bin`. It never
touches the medium.

### 12.2 Close policy

A disc is never closed by default. The config key is `disc.close_policy`.

| Value | Meaning |
|---|---|
| `never` | Default. Phase 1. No command closes the disc unless the user runs `close` explicitly. |
| `always` | Phase 1. Profile 0 only; refused under profile 1 or profile 2. A disc is sealed at its first and only burn: `spare:none`, `-dvd-compat`, no POW. |
| `when_full` | Phase 2. Profile 1 and 2 only. The run after which the free sectors below `fill_limit_sectors` cannot hold another run also closes the disc. |

A writer refuses `disc.close_policy = always` under `fs.profile` 1 or 2.

**Profile 0 under the default policy.** The disc is formatted for POW with
`spare:min`, one large run is written up to the data budget, `-dvd-compat` is
not passed, and the disc is left open. It can therefore receive a profile 1
append later, with no format change.

**Sealing a disc.** `pack --close`, or `disc.close_policy = always`, selects
`spare:none` and `-dvd-compat` instead: no format step, no spare area, no
defect management, full capacity, permanently stable LBAs, and no later
append. The choice is permanent.

Section 10.2 gives the two command lines.

### 12.3 Tail anchors

`mkudffs` places UDF anchors at LBA 256, at `N - 256` and at `N` in the
full-size image. The used prefix of the image is LBA 0 up to and including the
last sector of `RUN2.bin`, rounded up to a multiple of 16 sectors.

**Open disc, the default.** The first run writes the used prefix only. The tail
anchors are not on the disc yet. A disc with only the LBA 256 anchor still
mounts, because that anchor is mandatory in the standard. The tail anchors
reach the disc when an append or `close` writes them.

**Sealed disc.** `pack --close` burns the full-size image: the used prefix, the
unused middle as zero sectors, and the last 512 sectors with the tail anchors.
A sealed disc can never be appended, so its tail anchors must be written at its
only burn.

### 12.4 What close does

The format must not depend on a closed disc. Every reader path works on an open
disc.

`close` is Phase 2. It writes a closing run, which is an append. The writer
must:

1. Confirm that the superblock is already present. It is never rewritten, so
   the close is not recorded there.
2. Set `run_flags` bit 0, `CLOSING_RUN`, in the closing run's header.
3. Set `state_flags` bit 0, closed, in this disc's record of the disc directory
   that the closing run writes into its own catalog.
4. Write a final catalog copy inside the closing run.
5. Write the tail anchors if they are not present.
6. Optionally add a disc-wide parity run over all data columns of all runs,
   under `fec.disc_close_parity`. Phase 3.
7. Write the closing run with `-dvd-compat`, then verify on a second drive
   within 24 hours.

FORMAT.md section 7.12 states the three places a reader learns that a disc is
closed. Any one of them saying yes is enough, and a writer refuses the append.

A sealed profile 0 disc needs none of this. It was closed at its only write.

### 12.5 Spare area and raw append

Pseudo-OverWrite works through the drive's spare area, which is finite. An
overwrite of an already written block fails when the spare area is exhausted. A
write past the next writable address still works.

Three measures follow.

**Measure 1: write each metadata block once per append.** Section 10.5 states
the block-diff rule. `disc.spare` selects the spare area size at format time
and cannot be changed afterwards.

**Measure 2: watch the spare area.** The health report gives the remaining
fraction. Below `disc.min_spare_ratio` the tool warns and recommends no further
ordinary appends.

**Measure 3: raw append, a degraded mode.** When the spare area is exhausted,
or when an overwrite fails with a write error that the drive attributes to
spare exhaustion, the tool may still write new runs.

In raw append mode:

1. A new run is written past the next writable address, as a sequential write.
2. The filesystem directory is not updated, so no spare block is needed.
3. The run header, the manifest, the filter and the layout table go inside the
   new run, exactly as usual.
4. The disc directory marks the disc `append-raw-only`. FORMAT.md section 7.13
   gives the LBA rule for a raw run.
5. Restore is unaffected. NoahsArk reads by LBA extents, not by filesystem
   path. Other operating systems do not see the raw runs.

Raw append is a degraded mode. The tool must warn every time it uses it, must
record the state in the disc directory, and must recommend a fresh disc.
`append` refuses a disc marked `append-raw-only` unless `--raw` is given and
`disc.allow_raw_append` is true.

### 12.6 Forced capacity

A user may cap the usable capacity of one disc below what the drive reports.
The CLI option is `pack --capacity`. The config key is `disc.force_capacity`.

FORMAT.md section 7.14 gives the superblock fields. The rules on the host side
are:

1. The forced value must be at or below the reported capacity. A larger value
   is a hard error.
2. The forced value applies from the first write of the disc. It must not
   change afterwards. A later `pack` with a different `--capacity` for the same
   disc is a hard error.
3. Everything that consumes capacity uses the forced value. That is four
   consumers: the packer, when it decides how much fits; the image size, which
   is the length the image file is truncated to before `mkudffs`; the FEC
   layout, because the whole run must lie below `fill_limit_sectors`; and the
   reserve and fill limit of section 9.
4. `disc list` shows both the reported and the forced capacity.
5. The health report flags any disc whose forced capacity is below the reported
   capacity.

---

## 13. Verify, scrub and heal

### 13.1 Verify levels

| Level | What it reads |
|---|---|
| `catalog` | Cache consistency only. Reads no disc. |
| `connectivity` | Trees, chunklists, snapshots, filters and manifests. |
| `integrity` | Every sector and every object. The default for a disc. |

### 13.2 Verify procedure

Verify reads the disc with ddrescue and uses the mapfile as the erasure list. A
fast pass with no scraping runs first. A full pass runs only when errors
appear.

`verify --image --mapfile` treats every sector that the map does not mark
rescued as an erasure. Without the mapfile every sector of the image is treated
as readable, and only the digests and the content ids find damage.

**How verify locates the columns.** Verify needs the run header and the layout
table of every run it checks. On a mounted disc or a mountable image it reads
`runs/<seq>/RUN.bin` and `runs/<seq>/layout.bin` through the filesystem. When
the image does not mount, or a run directory is unreadable, verify scans the
image at sector alignment for the `"NARH"` magic, verifies `header_crc32c` of
every copy it finds, groups the copies by `run_seq`, and takes the geometry
from the header. It then reads `layout.bin` at `layout_lba` and takes the
extent of `checksum.bin` and of every parity file from the extent records.

Every read of an image is a byte-offset read: sector `s` is the bytes
`[s * 2048, s * 2048 + 2048)` of the image file.

A sector inside a parity domain that no extent record covers is filesystem
metadata or free space. FORMAT.md section 10.6 states that verify does not
compare such a sector with its recorded digest.

On a successful verify the run's objects move from BURNED to CLEAN.

The 24-hour second-drive check is a requirement. Do not file a disc until it
passes on two drives of different models.

### 13.3 Scrub schedule

`scrub` runs `verify --level=integrity` over the discs that the schedule
selects. The table below is the default of `scrub.schedule`,
`scrub.first_check_hours` and `scrub.degraded_interval`, which the operator may
change. The 24-hour second-drive check is a requirement; the rest is the
recommended default.

| Age | Interval | Depth |
|---|---|---|
| Within 24 hours of burning | once | `integrity`, on a second drive of a different model. |
| Year 0 to 1 | at 3 months, then at 12 months | `integrity` |
| Year 1 to 5 | every 12 months | `integrity` on a rotating quarter of the library each quarter |
| Year 5 to 10 | every 6 months | `integrity` |
| Year 10 and beyond | every 6 months | `integrity`, plus a migration plan |
| Any disc marked degraded | every 3 months | `integrity`, and schedule a re-burn |
| After a flood, a heat event, or a move | immediately | `integrity` on the affected shelf |

`scrub.schedule` follows the grammar of section 17.14. Phases must be in
ascending age order, and a schedule that is not is refused.

### 13.4 Heal order

The healer tries the sources in this order.

1. **On-disc RS parity** of the damaged run. Correct all erasures, then
   re-check the sector digests and the object content ids.
2. **Content-addressed re-fetch.** Query the catalog for another run or disc
   that holds the same content id. This layer is free and must be tried before
   any layer below it.
3. **Cross-disc parity group**, if the disc belongs to one.
4. **Mirror disc**, if one exists.
5. **The original source path**, if it still exists and its content id matches.
6. **Give up.** Record the loss explicitly, with the object id, the file paths
   that reference it, and the affected snapshots.

Reconstructed objects are written into `staging/heal/` and enter the staging
state machine at STAGED with reason 3. The catalog marks the damaged run
degraded and lists the lost ids. The restore planner prefers a healthy run over
a degraded one.

### 13.5 Health report

The headline metric is the RS margin: `m` minus the worst-stripe erasure count,
as a percentage of `m`.

FORMAT.md section 10.7 defines the status values `HEALTHY`, `DEGRADED`,
`CRITICAL`, `FAILED` and `UNKNOWN`.

Re-burn a disc below `fec.reburn_margin`, and re-burn it above
`scrub.max_disc_age` regardless of health. Migrate the library when the media
generation goes out of production.

Two flags are raised in the per-disc report: a forced capacity below the
reported capacity, and a remaining POW spare area below
`disc.min_spare_ratio`.

Health thresholds, when the drive exposes the counters: an LDC average below 13
and a BIS average below 15 indicate a healthy disc. Twice those values is a
warning. Any uncorrectable read is critical.

`health --json` and `verify --report` write one JSON object with these fields.
A field whose value is unknown is absent, never null.

| Field | Type | Meaning |
|---|---|---|
| `format` | string | Always `noahsark-health-report`. |
| `version` | integer | 1. |
| `repo_uuid` | string | Hyphenated lowercase uuid. |
| `generated_sec` | integer | Report time, seconds since 1970-01-01 UTC. |
| `discs` | array of objects | One per disc in the report. |
| `discs[].disc_uuid`, `discs[].label`, `discs[].shelf` | string | The disc; its on-disc label; its shelf note from the notes file. |
| `discs[].disc_seq` | integer | 0-based. |
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
| `discs[].critical_cause_run_seq` | integer | The append that rewrote the uncovered sectors, when that is the cause. |
| `library` | object | `margin_histogram` (array of 11 integers, one per 10 percent band), `count_by_status` (object), `overdue_discs` (array of uuid), `oldest_unscrubbed_sec` (integer), `objects_replication_1` (integer), `objects_lost` (integer), `reburn_forecast_12m` (integer), `duplication_ratio` (number), `consolidation_triggers` (object of the keys of section 8.7 to booleans). |
| `objects` | array of objects | Present under `health --object`: `content_id`, `size`, `runs` (array of `run_seq`), `discs` (array of uuid), `replication`, `last_verified_sec`. |
| `manufacturer_trend` | array of objects | `manufacturer_id`, `discs`, `failed`, `degraded`. |

### 13.6 Connectivity check

The check proves that every object reachable from a snapshot exists somewhere.

```
needed := { root tree of S }
while needed is not empty:
    take id from needed and locate it:
        a) in this run's manifest         -> read it
        b) in the cache's merged index    -> note the run, defer
        c) test every run's filter: all negative -> MISSING (a proof);
           some positive -> candidate runs, defer
    a tree, chunklist or snapshot: read it, queue its children by run
    a chunk: membership alone is enough; do not read it
```

Chunks are never read. A chunk has no outgoing reference, so membership is the
whole obligation. Only trees, chunklists and snapshots are read.

A filter negative across every run is a proof of absence, as FORMAT.md section
11.10 states. The check can therefore conclude "missing" with certainty from
the cached filters alone, with no disc mounted.

A filter positive is only a hint. Resolve it against that run's manifest.

Deferred reads are grouped by run and processed one run at a time, so each disc
is mounted at most once.

A version 1 reader always uses the walk above. The reserved bitmap counting
argument is not available.

The report says, per object, "present on run X", "missing", or "declared
prerequisite".

---

## 14. Restore

### 14.1 The planner

Restore planning is minimum set cover. The planner works in discs, not runs:
every run of a disc is available once the disc is in the drive, and run order
does not imply disc order.

**Inputs.** The planner takes the object set of the snapshot, the manifests and
filters that say which runs hold each object, and the run table, which maps
every `run_seq` to its `disc_seq` and `disc_uuid`. Without the run table the
planner cannot run; it takes the table from the cache or from the newest disc.

The planner drops every run whose `run_status` is 3 before it starts, with its
filter and its manifest.

**Requirements.** Three things are normative: the plan accounts for every
needed object or fails up front; the plan is deterministic, so the same inputs
give the same plan; and the tie-breaks below are applied in the stated order.

**Coverage.** FORMAT.md section 11.10 defines exact coverage, probable coverage
and the proof of absence. The plan says which kind applies to every object.

A plan with any missing object fails up front. A plan that holds a probable
object is a valid plan, and the planner must not refuse it: it counts the
probable objects per disc and for the whole plan, and prints both.

The restore confirms a probable object against that run's manifest when the
disc is in the drive, which is the first thing it reads from that disc. A
confirmation that fails means the object is not on that disc: the restorer
re-plans over the remaining runs, adds the disc that a manifest or a filter
then names, and reports the added disc.

**Algorithm.** The two steps below are **informative**. Any algorithm that
meets the requirements conforms.

```
Step 1: unique-element reduction. If every run that holds an object lies on
  one disc, that disc is in every valid plan. Add all such discs and remove
  every object they cover.
Step 2: greedy on the residual. P = mandatory_discs(N); U = N minus
  covered(P); while U is not empty, pick the disc d that maximizes
  score(d, U), add d to P, and remove S_d from U; return order(P).
```

The default score is the number of bytes newly covered. An object-count score
is available through `restore.score` and `--score`.

**Tie-breaks**, applied in this fixed order:

1. A disc that is already in a drive.
2. Disc health. A disc that failed verify is a last resort, used only when it
   is mandatory.
3. Most remaining bytes covered.
4. Newer disc.
5. Lower `disc_seq`.

### 14.2 Disc-major order

```
for each disc in plan order:
    detect the disc
    read every needed object from it in one pass, sorted by LBA
    write those objects into staging/restore/
    for every file whose chunks are now all present:
        assemble it, write it to the target, free its staging space
    eject
```

The switch count equals the number of discs in the plan.

File-major order is forbidden as an anti-pattern. If consecutive files live on
different discs, each file boundary can cost a switch.

### 14.3 Staging budget

`restore.staging_budget` declares the limit. If the predicted peak exceeds it,
the planner splits the restore into several passes and accepts re-visiting a
disc. The extra switches appear in the plan.

Four measures keep the peak small:

1. Enforce packing rule 1 at write time.
2. Free per file, not per disc.
3. Order the plan discs by the number of files each disc completes, given the
   discs already visited, descending.
4. Place a multi-disc split so that its discs are adjacent in plan order.

### 14.4 The plan file

The plan is printed and persisted before any read. The plan must fail up front
when a required disc is missing from the inventory.

The plan states its coverage: `objects_exact` and `objects_probable` for the
whole plan, and `objects_probable` per disc. A non-zero probable count is the
plan saying that a later disc may be added during the restore. It is not a
failure.

The plan is one JSON object with these fields.

| Field | Type | Meaning |
|---|---|---|
| `format` | string | Always `noahsark-restore-plan`. |
| `version` | integer | 1. |
| `repo_uuid` | string | Hyphenated lowercase uuid. |
| `snapshot` | string | Multihash text form of the snapshot. |
| `target` | string | The restore target path. |
| `include` | array of strings | The `--include` paths, absent when none. |
| `files`, `objects` | integer | Files and distinct objects to restore. |
| `objects_exact` | integer | Objects located through a manifest record. |
| `objects_probable` | integer | Objects located only through a filter positive. 0 means the plan names every disc the restore will ask for. |
| `bytes` | integer | Uncompressed bytes to restore. |
| `peak_staging_bytes` | integer | Predicted peak of `staging/restore/`. |
| `estimated_seconds` | integer | From the model of section 14.5. |
| `switches` | integer | Number of disc changes. |
| `passes` | integer | 1, or more when the staging budget forces several passes. |
| `discs` | array of objects | In plan order. |
| `discs[].order`, `discs[].pass`, `discs[].drive` | integer | 0-based position in the plan, 0-based pass, and 0-based drive under `--drives` above 1. |
| `discs[].disc_uuid` | string | The disc. |
| `discs[].disc_seq` | integer | 0-based. |
| `discs[].label`, `discs[].shelf` | string | On-disc label; shelf note, absent when none. |
| `discs[].health` | string | `healthy`, `degraded`, `critical`, `failed`, `unknown`. |
| `discs[].runs` | array of integers | The `run_seq` values to read on this disc. |
| `discs[].bytes_to_read`, `discs[].objects_to_read`, `discs[].files_completed` | integer | Counts. |
| `discs[].objects_probable` | integer | Of `objects_to_read`, how many were located only through a filter positive. |
| `discs[].estimated_seconds` | integer | From the model of section 14.5. |
| `missing_discs` | array of objects | `disc_uuid`, `disc_seq`, `label`, `objects` (integer). Non-empty means the plan failed. |
| `degraded_runs` | array of objects | `run_seq`, `disc_uuid`, `lost_ids` (array of multihash text). |

### 14.5 Time model

```
t_disc(d) = t_swap + t_load + t_spinup + t_mount + bytes_d / rate_effective + t_eject
T_restore = sum over d in plan of t_disc(d)          # single drive
```

`rate_effective` comes from `restore.rate_mb_s`, and the fixed cost per switch
from `restore.switch_seconds`. The estimate is printed with every plan.

### 14.6 Disc detection

1. Read `/NOAHSARK/DISC.bin` and compare the `disc_uuid`.
2. The filesystem label is a hint for a human only.
3. After the drive reports a disc, wait for the device to settle, then mount
   read-only and read the superblock. Retry the mount a few times.
4. Eject after unmounting. `--no-eject` and `restore.eject` control this.

Informative: on Linux, poll the drive status about once a second; udev change
events are a lower-latency path, but do not always report a removal. Any
detection method that ends in step 3 conforms.

Do not prompt when the expected disc is detected. Print one line and continue.

Prompt only when the wrong disc is inserted, when the disc is unreadable, or
when `--interactive` or `restore.interactive` is set.

### 14.7 Restore pipeline

```
snapshot id
 -> object set: read the snapshot, trees and chunklists (metadata only),
      from the cache or from the newest disc
 -> run map: filters give candidate runs; manifests give the exact
      (run, container, offset)
 -> disc plan: unique-element reduction, greedy, tie-breaks; print and
      persist the plan; fail on a missing disc
 -> for each disc:
      detect -> read this disc's manifests -> confirm every probable object
      -> read needed objects in LBA order into staging/restore/
      -> verify each object's content id (hard error on mismatch)
      -> assemble every file that is complete
      -> create, write, xattr and ACL, chown, chmod, flags, times
      -> free the staging space of that file -> eject
 -> deferred pass: directory times, in reverse depth order
 -> loss report (JSON) + replay plan + exit code
```

Every object's content id is verified after it is read. A mismatch is a hard
error. Directory times are applied in a deferred pass in reverse depth order at
the end of the restore. `restore` prints one warning per set `source_flags`
bit, once, before it writes anything.

### 14.8 Cache-less restore

With no cache, the restorer reads the catalog from the newest disc first. That
gives every filter, the snapshot table, the ref table, the run table, the disc
directory, and the catalog's 8 manifests plus the run's own, that is 9. The
planner then works normally.

With the catalog alone the planner has exact manifests for the newest
`manifest.history_depth` runs and filters for the rest, so an older object is
located as probable and confirmed at read time. Warming the cache to level 3
first removes every probable object.

If the newest disc is lost, the fallback reads every available disc's manifest
and rebuilds the catalog. Both paths must exist and both must be tested.

---

## 15. Metadata restore policy

FORMAT.md section 6.6 gives the metadata field set and its encodings. This
section is the restore policy.

### 15.1 Ownership

1. The writer always stores both the numeric id and the name. The numeric id is
   mandatory. The name is optional and is omitted when the source has no name
   for the id.
2. On restore, resolve the stored name locally and use the resulting id.
3. When the name is absent or the lookup fails, fall back to the stored numeric
   id.
4. `--numeric-owner` skips step 2 and always uses the stored numeric ids.
5. `--no-owner` skips ownership. Files get the invoking user's uid and gid.
   This is implied when the restore is not privileged.
6. A failure to apply ownership must never fail the entry. The restorer writes
   the file, records a `metadata_not_applied` event with the path, the field
   and the reason, and continues.
7. Ownership is applied with `AT_SYMLINK_NOFOLLOW` semantics, never plain
   `chown`, so a symlink cannot redirect the change.

`restore.owner_policy` selects the default among `auto`, `numeric`, `name` and
`none`.

### 15.2 Restore order

The order per entry is fixed, and every step has a reason. That order and
reason is the requirement. The system calls named at each step are
**informative**.

1. Create the object: `openat`, `mkdirat`, `symlinkat`, `mknodat`.
2. Write the content.
3. Set xattrs and ACLs.
4. `fchownat(AT_SYMLINK_NOFOLLOW)`. chown clears setuid and setgid on Linux, so
   it must come before chmod.
5. `fchmodat`. It must follow chown to restore setuid and setgid.
6. Set Linux chattr flags and BSD flags. Immutable and append-only block later
   writes, so they must come after every write.
7. `utimensat(AT_SYMLINK_NOFOLLOW)`. Every preceding step changes mtime, so
   times come last.
8. Directory times are applied in a deferred second pass, after all children are
   written, because writing a child updates the parent's mtime. The pass uses
   the directory handle retained from step 1, never a re-opened path.

### 15.3 Hardlinks

FORMAT.md section 6.14 gives the group id rules and the membership rules.

The restorer keeps a map from `hardlink_group` to the first restored
`(directory, name)`. On a second member it creates a hard link to that first
member, by directory descriptor and name, never by a path string. On failure it
writes the content again and records a `hardlink_degraded` event.

Hard links are created with flags that do not follow a symlink.

In mirror mode the hardlink group ids come from the source listing's device and
inode numbers, never from the mirror copy.

### 15.4 Failure policy

1. Restore is strict for data and best-effort for metadata. A chunk that does
   not verify is a hard error. A metadata field that cannot be applied is a
   recorded event.
2. `--metadata-strict` turns every metadata failure into a hard error.
3. Cross-platform loss is reported once per field kind, with a count and a few
   example paths.
4. Never translate between ACL models silently. If a POSIX ACL cannot be
   applied, drop it and report it. A translation, if it is ever offered, sits
   behind an explicit flag and must never grant more access than the source
   entry did.

### 15.5 Non-root restore

1. Probe privileges once at start. Check the effective user id and the
   effective capability set, and pre-select the metadata plan.
2. Under an unprivileged plan, `--no-owner` is implied, and device nodes and
   privileged xattr namespaces are skipped with a per-entry record.
3. Print one clear line at the start that names what will not be applied.
4. Produce a machine-readable report at the end: `{path, field, reason, errno}`
   records plus a summary count by field kind.
5. Offer `--report-replay=FILE`, a plan that a privileged user can run
   afterwards to apply the deferred ownership and device nodes.
6. Never let an unprivileged restore silently produce a tree that looks
   complete. The exit status must be 1 and the summary must be printed.

### 15.6 Name and symlink safety

FORMAT.md section 2.11 states the five invariants. The system calls below are
**informative**; another platform meets the same invariant with its own calls.

1. Validate at parse time. Reject any entry whose name is empty, is `.` or
   `..`, or contains `/`, `\` or NUL.
2. Never build a path string and open it. Walk with one directory file
   descriptor per level, and open each child with `openat` or `mkdirat`. Use
   `O_NOFOLLOW`, `O_CLOEXEC` and `O_EXCL` on every open of a regular file, and
   `O_DIRECTORY | O_NOFOLLOW` when descending, so the check and the use are one
   syscall. On Linux, use `openat2` with `RESOLVE_BENEATH |
   RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS` where available.
3. Create symlinks with `symlinkat`. Do not validate or rewrite the target.
4. With `--overwrite`, unlink the existing path first and then create. Never
   open an existing path for truncation.
5. Create hardlinks with `linkat` and flags 0, never `AT_SYMLINK_FOLLOW`.
6. On Windows, open with `FILE_FLAG_OPEN_REPARSE_POINT`, and reject reserved
   device names and trailing dots or spaces.
7. Apply directory times in the deferred pass with the retained descriptor.

### 15.7 Unstable entries

`restore` writes an `UNSTABLE` entry normally. It prints one warning line per
unstable entry, naming the path. An unstable entry alone sets exit code 1, not
2, because the file was restored.

`restore --strict-unstable` refuses to write such an entry and reports it as
missing.

The loss report carries the paths under the field name `content_unstable`.

`ls` marks an `UNSTABLE` entry with `!`.

### 15.8 Restore exit codes

Exit codes:

| Code | Meaning |
|---:|---|
| 0 | Everything applied. |
| 1 | Data restored, with metadata loss. |
| 2 | Data restore failed. |
| 3 | A required disc is missing (section 16.16). |

### 15.9 Loss report

The loss report is one JSON object with these fields:

| Field | Type | Meaning |
|---|---|---|
| `format` | string | Always `noahsark-metadata-report`. |
| `version` | integer | 1. |
| `snapshot` | string | Multihash text form. |
| `target` | string | The restore target. |
| `privileged` | boolean | Whether the restore ran with root or the needed capabilities. |
| `source_flags` | array of strings | The names of the set `source_flags` bits of the snapshot (FORMAT.md section 6.15). |
| `summary` | array of objects | One per field kind. |
| `summary[].field` | string | `owner`, `group`, `mode`, `setuid_setgid`, `device_node`, `fifo_socket`, `symlink`, `hardlink_degraded`, `xattr.user`, `xattr.security`, `xattr.trusted`, `acl`, `flags`, `times`, `windows_attrs`, `windows_sd`, `ads`, `unknown_tlv`, `content_unstable`, `intermediate_directory`. |
| `summary[].count` | integer | Entries affected. |
| `summary[].reason` | string | An errno name, `UNSTABLE`, `UNSUPPORTED`, `SYNTHESIZED`, or `DROPPED`. |
| `summary[].examples` | array of strings | Up to 10 paths. |
| `entries` | array of objects | Present under `-v`: every `{path, field, reason, errno}` record. |
| `exit_code` | integer | 0, 1, 2 or 3, the full set of section 15.8. A restore that stopped because a required disc is missing writes the report with `exit_code` 3. |

The replay plan holds the same records in a form that a privileged user can
apply: a list of `(path, field, value)` triples plus the commands that apply
them.

---

## 16. CLI reference

### 16.1 Commands, phases and global options

Every command carries a phase tag. A Phase 1 build must refuse a Phase 2 or
Phase 3 option with a clear message, not ignore it.

Phase 1 provides `init`, `commit`, `pack`, `burn`, `verify`, `scrub`,
`health`, `plan`, `restore`, `rebuild-cache`, `gc`, `ls`, `log`, `disc` and
`image`. Phase 2 adds `sync`, `append` and `close`. Phase 3 adds `watch`,
`consolidate` and `reindex`. `catalog export` and `import` are Backlog. Each
command's own heading below repeats its phase.

Every command accepts these global options.

| Option | Meaning |
|---|---|
| `--repo=PATH` | Repository root. Defaults to the discovered repository of section 2.2. |
| `--cache-dir=PATH` | Override the cache location. |
| `--config=PATH` | Override the config file. |
| `--json` | Machine-readable output. |
| `-v`, `--verbose` | More output. |
| `-q`, `--quiet` | Errors only. |
| `--dry-run` | Compute and print, change nothing. Not every command supports it. |

Section 19 is the exit code registry.

### 16.2 `init` (Phase 1)

```
noahsark init [--repo=PATH] [--hash=blake3|sha256] [--chunker=P3|P4|P5]
              [--fs-profile=0|1|2] [--preset=dedup|balanced|locality|standalone]
              [--repo-uuid=UUID] [--next-run-seq=N] [--next-disc-seq=N]
              [--scan-discs]
```

Creates the repository of section 2.1.

| Option | Meaning |
|---|---|
| `--hash` | Initial value of `hash.current`. |
| `--chunker` | Initial value of `chunker.profile`. |
| `--fs-profile` | Initial value of `fs.profile`. |
| `--preset` | Initial value of `locality.preset`. |
| `--repo-uuid` | Reuse the uuid of an existing disc set, for a repository recreated after the loss of the machine. |
| `--next-run-seq` | The `run_seq` the next `pack` assigns. |
| `--next-disc-seq` | The `disc_seq` the next new disc takes. |
| `--scan-discs` | Read every available disc and derive both sequence numbers. |

`--repo-uuid` requires either `--next-run-seq` and `--next-disc-seq` together,
or `--scan-discs`, and refuses to create the repository without one.

`--scan-discs` takes `max(disc_seq) + 1` and `max(run_seq) + 1`. It warns that
a disc it did not see may hold a higher number, and that a reused number would
make two different runs share one `run_seq`. It recommends adding at least 100
to both numbers unless every disc of the set was present in the scan.

`init` records the chosen values as `repo.next_run_seq` and
`repo.next_disc_seq`. `pack` takes the next number from there and increments
it. A fresh repository starts at `run_seq` 1 and `disc_seq` 0.

Exit: 0 on success; 2 when the directory already holds a repository, or when
`--repo-uuid` was given with neither form.

### 16.3 `commit` (Phase 1)

```
noahsark commit [SOURCE]... [-m MESSAGE] [--ref=NAME] [--checksum] [--force]
                [--exclude=PATTERN]... [--one-file-system=BOOL]
                [--source=PATH] [--source-root=PATH] [--from=PATH]
                [--copy-first] [--retry-unstable=N]
                [--out=DIR --catalog=DIR] [--source-type=TYPE]
```

Runs the commit flow of section 7.1. With no `SOURCE`, it uses the configured
source roots.

| Option | Meaning |
|---|---|
| `-m` | Commit message, stored as a snapshot TLV. |
| `--ref` | The ref to move. Default `LATEST`. |
| `--checksum` | Disable the quick check. Read and rehash every file. `--full-scan` is an alias. |
| `--full-scan` | Alias of `--checksum`. |
| `--force` | Write a snapshot object and move the ref even when the root tree equals the parent's root tree. |
| `--from` | Read changed files from this mirror directory. Phase 2. |
| `--source` | Read from this path, typically a filesystem snapshot mount. |
| `--source-root` | The original path to record in the snapshot. |
| `--copy-first` | Copy each changed file raw into staging, then chunk it there. Phase 2. |
| `--out` | Write a commit bundle to this directory instead of into staging. Backlog. |
| `--catalog` | The exported catalog to deduplicate against, with `--out`. Backlog. |
| `--source-type` | Override the detected source type recorded in the snapshot. |
| `--retry-unstable` | Re-read an unstable file up to N times before the rule of section 7.6 applies. |
| `--exclude` | Skip matching paths. The patterns are recorded in the snapshot. |
| `--one-file-system` | Do not cross a mount point. |

Exit: 0 on success, also when the tree is unchanged and no snapshot was
written; 1 when some files could not be read or were unstable; 2 on failure or
when the repository lock is held.

The report lists every unstable path and says which branch was taken.

### 16.4 `sync` (Phase 2)

```
noahsark sync SOURCE [--mirror=PATH] [--ref=NAME] [--dry-run]
             [--list-only] [--rsync-arg=ARG]...
```

Pulls the change set of `SOURCE` into the mirror directory (section 7.4).

| Option | Meaning |
|---|---|
| `--mirror` | Mirror directory. Default `sync.mirror_dir`. |
| `--ref` | The ref whose snapshot is the comparison base. Default `LATEST`. |
| `--list-only` | Print the changed-path list and the byte total. Transfer nothing. |
| `--rsync-arg` | Pass an extra argument through to `rsync`. |

Exit: 0 on success; 1 when some paths could not be listed; the `rsync` exit
code on a transfer failure; 2 when `rsync` is not installed.

### 16.5 `catalog export` (Backlog)

```
noahsark catalog export DIR [--manifests=N] [--json]
```

Writes the current catalog to `DIR` as plain files (section 7.9).

| Option | Meaning |
|---|---|
| `--manifests` | How many recent manifests to include. Default `manifest.history_depth`. |

Exit: 0 on success, 2 on failure.

### 16.6 `import` (Backlog)

```
noahsark import BUNDLE_DIR [--ref=NAME] [--dry-run] [--keep]
```

Verifies and imports a commit bundle (section 7.9).

| Option | Meaning |
|---|---|
| `--ref` | The ref to move. Default the ref named in the bundle, else `LATEST`. |
| `--keep` | Do not delete the bundle directory afterwards. |

Exit: 0 on success; 1 when some objects were already present; 2 on a
verification failure or a repository uuid mismatch.

### 16.7 `watch` (Phase 3)

```
noahsark watch [SOURCE]... [--log=PATH]
```

Runs a change-recording daemon. It records changed paths and never commits.

| Option | Meaning |
|---|---|
| `--log` | Where to write the change log. |

Exit: 0 on a clean stop, 2 on failure.

### 16.8 `pack` (Phase 1)

```
noahsark pack [--disc=UUID] [--media=NAME|ID]
              [--capacity=BYTES|GiB] [--reserve=BYTES|PERCENT]
              [--extra-reserve=BYTES|PERCENT] [--label=TEXT]
              [--preset=NAME] [--now] [--close] [--dry-run]
```

Selects objects for the next run, applies the locality rules of section 8,
builds the run image and the parity, and writes the burn plan.

`pack` produces a run only when staging holds at least `disc.min_fill` of the
data budget, or the oldest STAGED object is older than `disc.max_wait`, or
`--now` is given. Otherwise it prints how much more data, or how much more
time, is needed, and exits 1.

`pack` fills exactly one run per invocation.

| Option | Meaning |
|---|---|
| `--disc` | Continue an existing disc. Without it, `pack` starts a new disc. |
| `--media` | Media type for a new disc: any name, or the numeric id, from the media type registry of FORMAT.md section 2.6. A space in a name may be written as `-`. Default `disc.media`. |
| `--capacity` | Forced capacity for a new disc (section 12.6). A plain integer is bytes; an integer followed by `KiB`, `MiB` or `GiB` is scaled. |
| `--reserve` | `disc.force_reserve` for this disc. |
| `--extra-reserve` | `disc.extra_reserve` for this disc. |
| `--label` | Human label, printed on the disc. |
| `--preset` | Locality preset for this run. |
| `--now` | Ignore `disc.min_fill` and `disc.max_wait`. |
| `--close` | Seal the disc: `spare:none` and `-dvd-compat`, no POW, full capacity, no later append. Profile 0 only. |

Exit: 0 on success; 1 when the run is smaller than requested; 2 on failure;
4 when a forced capacity conflicts with the recorded value.

### 16.9 `append` (Phase 2)

```
noahsark append --disc=UUID [--raw] [pack options]
```

A convenience form of `pack --disc=UUID`. It refuses a closed disc, and it
refuses a disc marked `append-raw-only` unless `--raw` is given.

| Option | Meaning |
|---|---|
| `--raw` | Allow the degraded raw append mode of section 12.5. `disc.allow_raw_append` must also be true. |

Exit: as `pack`. 4 when the disc is closed.

### 16.10 `burn` (Phase 1)

```
noahsark burn --run=SEQ (--print | --exec) [--verify | --no-verify]
              [--device=PATH]
              [--backend=growisofs|cdrskin|kernel|imgburn|hdiutil]
              [--speed=N] [--yes]
```

Renders or runs the burn plan of section 11.

| Option | Meaning |
|---|---|
| `--run` | The run to burn. |
| `--print` | Write the command lines to standard output. Touch no device. Available on every platform. |
| `--exec` | Run exactly those command lines. Linux only. |
| `--device` | Override the device hint in the plan. |
| `--backend` | Override the burner backend. |
| `--speed` | Override the speed. |
| `--yes` | Do not ask for confirmation before the first write. |
| `--verify` | Run `verify` after the burn. |
| `--no-verify` | Do not run `verify` after the burn. Objects stay BURNED and staging is not released until `verify` runs later. |

Exactly one of `--print` and `--exec` must be given.

`--exec` ejects and reloads the disc after the burn, then runs `verify`. It
uses a second drive when `burn.verify_device` names one and it is present.

| Code | Meaning |
|---:|---|
| 0 | Success. |
| 2 | The burn failed. |
| 3 | The expected disc is not in the drive. |
| 4 | The burner version is unknown or unpatched, or `--exec` ran on a platform other than Linux. |

### 16.11 `close` (Phase 2)

```
noahsark close --disc=UUID [--parity] [--print | --exec] [--yes]
```

Closes an open disc by appending a closing run (section 12.4).

| Option | Meaning |
|---|---|
| `--disc` | The disc to close. |
| `--parity` | Add a disc-wide parity run over all data columns of all runs. Phase 3. |

Exit: 0 on success, 2 on failure, 4 when the disc is already closed or sealed.

### 16.12 `verify` (Phase 1)

```
noahsark verify [--disc=UUID] [--run=SEQ] [--image=PATH [--mapfile=PATH]]
                [--level=catalog|connectivity|integrity]
                [--heal] [--drive=PATH] [--report=FILE]
```

Reads a disc or an image back and checks it. On success it moves the run's
objects to CLEAN.

| Option | Meaning |
|---|---|
| `--disc` | Verify this disc. |
| `--run` | Verify this run only. |
| `--image` | Verify an image file instead of a drive. |
| `--mapfile` | The ddrescue mapfile that produced `--image`. Every sector the map does not mark rescued is an erasure. Only valid with `--image`. |
| `--level` | `catalog`, `connectivity` or `integrity`. Section 13.1. |
| `--heal` | Repair what is repairable, in the order of section 13.4. Write recovered objects into `staging/heal/`. |
| `--drive` | Use this drive. Use a second drive model for the 24-hour check. |
| `--report` | Write the health record as JSON. |

Exit: 0 clean, 1 repaired or degraded, 2 unrecoverable loss, 3 disc missing.

### 16.13 `scrub` (Phase 1)

```
noahsark scrub [--due] [--all] [--disc=UUID]... [--drive=PATH]
```

Runs `verify --level=integrity` over the discs that the schedule of section
13.3 selects.

| Option | Meaning |
|---|---|
| `--due` | Select only overdue discs. |
| `--all` | Select every disc. |
| `--disc` | Select this disc. Repeatable. |
| `--drive` | Use this drive. |

Exit: as `verify`, aggregated over the discs.

### 16.14 `health` (Phase 1)

```
noahsark health [--disc=UUID] [--library] [--object=ID] [--json]
```

Prints the health report of section 13.5.

| Option | Meaning |
|---|---|
| `--disc` | Report on this disc. |
| `--library` | Report the per-library fields. |
| `--object` | Report the per-object fields for this content id. |

Exit: 0 when every disc is healthy, 1 when any disc is degraded, 2 when any
disc is failed.

### 16.15 `plan` (Phase 1)

```
noahsark plan SNAPSHOT [--target=PATH] [--out=FILE] [--drives=N]
              [--staging-budget=BYTES] [--score=bytes|objects]
```

Computes the restore plan of section 14.4 and prints it. It reads nothing from
a disc beyond the catalog.

| Option | Meaning |
|---|---|
| `--target` | The restore target path to record in the plan. |
| `--out` | Write the JSON plan to this file. |
| `--drives` | Number of drives to plan for. |
| `--staging-budget` | Peak staging allowed. |
| `--score` | Greedy score: `bytes` or `objects`. |

Exit: 0 when the plan accounts for every object; 1 when the plan holds at least
one probable object; 3 when a required disc is missing from the inventory or a
filter negative proves an object absent from every run.

### 16.16 `restore` (Phase 1)

```
noahsark restore SNAPSHOT TARGET [--plan=FILE] [--include=PATH]...
                 [--drives=N] [--staging-budget=BYTES] [--interactive]
                 [--no-eject] [--overwrite]
                 [--no-owner] [--numeric-owner] [--no-xattr]
                 [--xattr-exclude=PATTERN] [--no-acl] [--no-flags]
                 [--no-times] [--no-hardlinks] [--metadata-strict]
                 [--report=FILE] [--report-replay=FILE] [--strict-unstable]
```

Runs the restore pipeline of section 14.7.

| Option | Meaning |
|---|---|
| `--plan` | Resume a persisted plan. |
| `--include` | Restore only these paths. Repeatable. |
| `--drives` | Number of drives to use. |
| `--staging-budget` | Peak staging allowed. |
| `--interactive` | Prompt on every disc, not only on a mismatch. |
| `--no-eject` | Do not eject after each disc. |
| `--overwrite` | Unlink an existing path first and then create it. |
| `--no-owner`, `--no-xattr`, `--no-acl`, `--no-flags`, `--no-times` | Skip that metadata field. |
| `--numeric-owner` | Always use the stored numeric ids. |
| `--xattr-exclude` | Skip extended attributes whose name matches. |
| `--no-hardlinks` | Write each hardlink member as its own file. |
| `--metadata-strict` | Turn every metadata failure into a hard error. |
| `--translate-acl` | Translate between ACL models. Never grants more access than the source entry did. |
| `--report` | Write the loss report of section 15.9 to this file. |
| `--report-replay` | Write a replay plan a privileged user can run afterwards. |
| `--strict-unstable` | Refuse to write an `UNSTABLE` entry and report it as missing. |

Exit: 0 all applied, 1 data restored with metadata loss, 2 data restore failed,
3 a required disc is missing.

### 16.17 `rebuild-cache` (Phase 1)

```
noahsark rebuild-cache [--level=1|2|3] [--snapshot=ID] [--from-disc]
```

Rebuilds the local cache from discs, as in section 2.5.

| Option | Meaning |
|---|---|
| `--level` | 1, 2 or 3. |
| `--snapshot` | The snapshot that level 2 covers. |
| `--from-disc` | Read from a disc even when the cache looks current. |

Exit: 0 on success, 1 when the rebuild is partial, 3 when a needed disc is
missing.

### 16.18 `consolidate` (Phase 3)

```
noahsark consolidate [--snapshot=ID] [--dry-run] [--media=TYPE]
```

Packs a fresh, self-contained disc set for a snapshot (section 8.7).

| Option | Meaning |
|---|---|
| `--snapshot` | The snapshot to consolidate. |
| `--media` | Media type for the new set. |

Exit: 0 on success, 2 on failure.

### 16.19 `reindex` (Phase 3)

```
noahsark reindex --to=blake3|sha256 [--disc=UUID]... [--all]
```

Builds the optional cross-algorithm side table of section 3.6.

| Option | Meaning |
|---|---|
| `--to` | The target algorithm. |
| `--disc` | Read this disc. Repeatable. |
| `--all` | Read every disc. |

Exit: 0 on success, 1 when some discs were not available, 2 on failure.

### 16.20 `gc` (Phase 1)

```
noahsark gc [--dry-run] [--force-after=DURATION]
```

Deletes staging objects that are GC-ELIGIBLE, under the rules of section 4.5.

| Option | Meaning |
|---|---|
| `--force-after` | Shorten the retention for this run only. It requires an interactive confirmation. |

Exit: 0 on success, 1 when nothing was eligible, 2 on failure.

### 16.21 `ls` (Phase 1)

```
noahsark ls SNAPSHOT [PATH] [--long] [--recursive] [--json] [--unstable-only]
```

Lists a snapshot's tree. It reads tree objects only, never chunks.

| Option | Meaning |
|---|---|
| `--long` | Print mode, owner, size and mtime. |
| `--recursive` | Descend into subdirectories. |
| `--unstable-only` | List just the `UNSTABLE` entries. |

An `UNSTABLE` entry is marked with `!` in the first column, and with
`"unstable": true` under `--json`.

Exit: 0 on success, 3 when a needed tree object is unavailable.

### 16.22 `log` (Phase 1)

```
noahsark log [REF|SNAPSHOT] [--limit=N] [--json]
```

Walks the snapshot chain by parent pointer and prints the history. It reads
the pending chain of section 5.2, then the snapshot table.

| Option | Meaning |
|---|---|
| `--limit` | Print at most N entries. |

Exit: 0 on success.

### 16.23 `disc` (Phase 1)

```
noahsark disc list [--json]
noahsark disc label UUID TEXT
noahsark disc mark-degraded UUID [--health=VALUE] [--reason=TEXT]
```

`list` prints every disc: uuid, seq, label, shelf note, media type, filesystem
profile, reported capacity, forced capacity, used, run count, open or closed,
health, RS margin, spare remaining, and last verify date.

`label` appends a `record_type` 0 note to `<repo>/notes.bin`. The on-disc label
is written at burn time and never changes.

`mark-degraded` appends a `record_type` 1 note with `--reason` as the downgrade
reason.

| Option | Meaning |
|---|---|
| `--health` | The `health` value to record: `healthy` (1), `degraded` (2), `critical` (3), `failed` (4) or `unknown` (5). Default `degraded`. `failed` withdraws the disc, and `healthy` returns it to the state its run chain describes. |
| `--reason` | Free text, stored in the record's `shelf` field. Required for `--health=failed`. |

`disc mark-degraded --health` is the only way to set a `health` value by hand,
so every disc lifecycle state that needs one is reachable from the CLI.

Exit: 0 on success, 3 when the uuid is unknown.

### 16.24 `image` (Phase 1)

```
noahsark image build --run=SEQ --out=FILE
noahsark image diff --old=FILE --new=FILE [--out=FILE] [--block=32768]
noahsark image mount FILE MOUNTPOINT [--rw]
```

Test and development commands. They let the whole burn path run in CI with no
drive.

| Option | Meaning |
|---|---|
| `--run` | The run whose image to build. |
| `--out` | Where to write the image, or the diff. |
| `--old` | The previous image. |
| `--new` | The new image. |
| `--block` | Diff block size in bytes. |
| `--rw` | Loop-mount read-write. |

Exit: 0 on success, 2 on failure.

---

## 17. Configuration reference

The config file lives at `<repo>/config`. It is a plain text key-value file
with one `key = value` pair per line, `#` for a comment, and UTF-8 encoding. A
CLI option always overrides the file.

A build must refuse an unknown key, and a key of a later phase than it
implements, with a clear message that names the key and the phase.

A key whose "changes disc bytes" column says no is tuning: it changes speed,
memory or waiting time only, it affects no byte that reaches a disc, and its
value is recorded in no structure.

A change to any key whose column says yes, after a repository holds discs, is
treated as a new epoch or a new profile, never as a silent in-place change.
This section is the index that rule depends on. An omission from it is a defect
in this table, not license to change a key silently. FORMAT.md section 12.8
carries the same index from the reader's side.

Every key appears exactly once, in exactly one table below.

### 17.1 Identity and format

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `repo.uuid` | uuid | generated | 1 | no | Repository uuid. Never changed. |
| `repo.next_run_seq` | integer | 1 | 1 | no | The `run_seq` that the next `pack` assigns. 1-based, never reused. |
| `repo.next_disc_seq` | integer | 0 | 1 | no | The `disc_seq` that the next new disc takes. 0-based. |
| `format.version_major` | integer | 1 | 1 | yes | On-disc format major version. |
| `format.version_minor` | integer | 0 | 1 | yes | On-disc format minor version. |

### 17.2 Hashing and chunking

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `hash.current` | enum | `blake3` | 1 | yes | Algorithm for new objects: `blake3` or `sha256`. A change starts a new hash epoch. |
| `chunker.profile` | enum | `P4` | 1 | yes | Chunker profile: `P3`, `P4` or `P5`. |
| `chunker.gear_table_id` | integer | 1 | 1 | yes | Frozen Gear table version. Never change it under a profile name. |
| `bundle.threshold` | bytes | 1 MiB | 1 | yes | A chunk whose uncompressed payload is below this size goes into a bundle. |
| `bundle.target_size` | bytes | 64 MiB | 1 | yes | Target bundle size. |
| `chunklist.inline_max` | integer | 64 | 1 | yes | Above this many chunks, a file references a chunklist object. |
| `tree.tlv_spill_threshold` | bytes | 4 KiB | 1 | yes | A TLV area above this size spills into chunks. |

### 17.3 Compression

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `compression.algorithm` | enum | `zstd` | 1 | yes | `none`, `zstd` or `lz4`. |
| `compression.level` | integer | 3 | 1 | yes | Algorithm level. |
| `compression.min_gain` | fraction | 0.05 | 1 | yes | Store uncompressed when compression saves less than this fraction. |

### 17.4 Filesystem

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `fs.profile` | integer | 0 | 1 | yes | Disc filesystem profile: 0 one-shot, 1 UDF POW (Phase 2), 2 ISO 9660 POW (Phase 3). Fixed per disc at its first burn. |
| `fs.append_variant` | enum | `1b` | 2 | no | Profile 1 append variant: `1a` kernel direct write, `1b` image mirror and block diff. |
| `fs.fanout_levels` | integer | 1 | 1 | yes | Object fan-out depth. 2 is allowed under profile 0 and profile 1 only. |
| `udf.revision` | string | `2.01` | 1 | yes | UDF revision `mkudffs` writes. |

### 17.5 Disc

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `disc.fill_ratio` | fraction | 0.95 | 1 | yes | Fraction of the forced capacity that data may use. |
| `disc.min_fill` | fraction | 0.90 | 1 | no | `pack` triggers when staging fills this fraction of a disc's data budget. |
| `disc.max_wait` | duration | 30 days | 1 | no | `pack` triggers when the oldest STAGED object is older than this. |
| `disc.force_capacity` | bytes | unset | 1 | yes | Cap the usable capacity of a disc below the reported value. |
| `disc.force_reserve` | bytes or percent | unset | 1 | yes | Replace the computed reserve. |
| `disc.extra_reserve` | bytes or percent | unset | 1 | yes | Add to the computed reserve. |
| `disc.expected_runs` | integer | 2 under profile 0, 32 under profiles 1 and 2 | 1 | yes | Expected number of future runs, used by the estimator of section 9.3. |
| `disc.spare` | enum | `min` | 1 | yes | Spare area size at format time: `min` or `default`. Fixed at format time. |
| `disc.spare_reserve_bytes` | bytes | 256 MiB under `min`, 512 MiB under `default` | 1 | yes | Reserve for POW spare and filesystem metadata on every `spare:min` disc. |
| `disc.media` | string | `BD-R SL 25` | 1 | no | Default media type for a new disc. Any name or id from the media type registry. |
| `disc.min_spare_ratio` | fraction | 0.20 | 2 | no | Warn and recommend no further appends below this remaining spare fraction. |
| `disc.close_policy` | enum | `never` | 1 | yes | `never` or `always` (Phase 1), `when_full` (Phase 2). |
| `disc.allow_raw_append` | boolean | true | 2 | no | Allow the degraded raw append mode of section 12.5. |

### 17.6 Burner

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `burner.backend` | enum | `growisofs` | 1 | no | `growisofs`, `cdrskin`, `kernel`, `imgburn` or `hdiutil`. |
| `burner.device` | path | `/dev/sr0` | 1 | no | Default device hint in the burn plan. |
| `burner.speed` | integer | 4 | 1 | no | Speed multiplier for BD-R. |
| `burner.speed_mdisc` | integer | 2 | 1 | no | Speed multiplier for M-DISC. |
| `burner.require_patched` | boolean | true | 1 | no | Refuse `burn --exec` with an unknown or unpatched dvd+rw-tools build. |
| `burner.template.<backend>.<kind>` | string | see section 11.7 | 1 | no | Command template. A user may override any of them. |
| `burner.eject_after` | boolean | true | 1 | no | Eject after a successful write. |
| `burn.verify_after` | boolean | true | 1 | no | Run `verify` after `burn --exec`. |
| `burn.verify_device` | path | unset | 1 | no | Second drive for the verification read. Used only when present. |
| `burn.reload_seconds` | integer | 10 | 1 | no | Wait after the reload before the verification read. |

### 17.7 FEC

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `fec.scheme` | enum | `rs255-gf8` | 1 | yes | FEC scheme registry. Only value 1 exists in version 1. |
| `fec.k` | integer | refused | 1 | yes | Reserved for a later format version. A version 1 build refuses the key. |
| `fec.m` | integer | refused | 1 | yes | Reserved for a later format version. A version 1 build refuses the key. |
| `fec.band_stripes` | integer | 2048 | 1 | no | Stripes per encoding band. |
| `fec.disc_close_parity` | boolean | false | 3 | yes | Add a disc-wide parity run at close. |
| `fec.group_size` | integer | 0 | 3 | no | Discs per cross-disc parity group. 0 disables it. |
| `fec.reburn_margin` | fraction | 0.50 | 1 | no | Re-burn a disc below this RS margin. |

### 17.8 Filters, manifests and catalog

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `filter.type` | enum | `binaryfuse16` | 1 | yes | Run filter type. |
| `filter.rollup_threshold` | bytes | 64 MiB | 1 | no | Merge old filters into super filters above this bundle size. Reserved. |
| `manifest.fanout_bits` | integer | 8 | 1 | yes | 8 or 16. A writer must use 16 above 1,000,000 objects in one run. |
| `manifest.history_depth` | integer | 8 | 1 | yes | How many earlier runs' manifests every run carries. |
| `catalog.max_bytes` | bytes | 512 MiB | 1 | yes | Cap on one run's catalog. Only the manifest history is dropped to meet it. |
| `catalog.expected_snapshots` | integer | 10000 | 1 | yes | Planning count of repository snapshots, used to size `table_bytes`. |
| `catalog.table_reserve_bytes` | bytes | 65536 | 1 | yes | Planning reserve for the ref table, the run table and the disc directory of one catalog copy. |
| `catalog.snapobj_pack_threshold` | integer | 1000 | 1 | yes | Above this snapshot count, pack the replicated snapshot objects into one container file. |

### 17.9 Sources and excludes

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `sources.root` | path, repeatable | unset | 1 | no | An absolute source root. At least one is required. |
| `sources.exclude` | pattern, repeatable | unset | 1 | yes | An exclude pattern in the language of FORMAT.md section 6.17. |
| `sources.ignore_file` | string | `.noahsarkignore` | 1 | yes | Per-directory exclude file name. Empty disables it. |
| `sources.one_file_system` | boolean | true | 1 | yes | Do not cross a mount point. |
| `sources.follow_symlinks` | boolean | false | 1 | yes | Never true in Phase 1. A symlink is stored as a symlink. |
| `sources.skip_unreadable` | boolean | true | 1 | no | Skip and report an unreadable file. Exit code 1. |
| `sources.read_only` | boolean | true | 1 | no | Read-only. A source is never written. The key is reported, never set. |
| `source.quick_check` | enum | `size_mtime_ctime` local, `size_mtime` remote | 1 | no | Fields compared against the parent tree entry. |
| `source.mtime_slack` | duration | 0 local, 2 s remote | 1 | no | An mtime difference below this counts as equal in the quick check. |
| `source.checksum_every` | duration | 30 days | 1 | no | Force a full rehash on a remote root after this interval. 0 disables it. |
| `source.type` | enum | auto | 1 | yes | Override the detected source type: `local`, `snapshot`, `nfs` or `smb`. |
| `source.allow_smb` | boolean | true | 1 | no | Allow an SMB mount as a source root. Never allowed for staging. |
| `commit.checksum` | boolean | false | 1 | no | Always rehash. Equivalent to `--checksum` on every commit. |
| `commit.restat_after_read` | boolean | true | 1 | no | In-flight change detection. Never set it false on a live source. |
| `commit.retry_unstable` | integer | 1 | 1 | no | Re-reads of an unstable file before it is skipped. |
| `commit.copy_first` | boolean | false | 2 | no | Copy changed files into staging before chunking. |
| `repo.lock_timeout` | integer | 0 | 1 | no | Seconds to wait for the repository lock. 0 means fail at once. |
| `sync.rsync_path` | path | `rsync` | 2 | no | Path to the `rsync` binary. |
| `sync.rsync_args` | string | `-aHAX --numeric-ids` | 2 | no | Option set for `sync`. `--files-from` is always added. |
| `sync.mirror_dir` | path | `<staging>/mirror` | 2 | no | Mirror root on the staging disk. |
| `sync.clear_after_commit` | boolean | true | 2 | no | Delete the mirror contents once the objects reach STAGED. |
| `sync.remote_stat_command` | string | built-in | 2 | no | The command used to obtain a stat listing from a remote source. |
| `label.template` | string | `<repo-short-name>-<seq:04d> <YYYY-MM>` | 1 | yes | Physical label text. `<seq:04d>` is `disc_seq` zero-padded to four digits; `<YYYY-MM>` is the first write month in local time. |
| `repo.short_name` | string | from `repo.uuid` | 1 | yes | Short name used in the label: the first 16 characters of `repo.uuid` as lowercase hex with the hyphens removed. |

### 17.10 Locality and packing

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `locality.preset` | enum | `balanced` | 1 | yes | `dedup`, `balanced`, `locality` or `standalone`. |
| `locality.max_source_runs` | integer | 8 | 1 | yes | Capping bound per segment. |
| `locality.segment_size` | bytes | 1 GiB | 1 | yes | Capping segment size. |
| `locality.rewrite_below_chunks` | integer | 64 | 1 | yes | Do not keep a source run that supplies fewer chunks than this. |
| `locality.max_duplicate_bytes_per_file` | bytes | 64 MiB | 1 | yes | Per-file cap on rewritten bytes. |
| `locality.max_duplicate_ratio_per_file` | fraction | 0.05 | 1 | yes | Per-file cap as a fraction. |
| `locality.disc_budget` | fraction | 0.03 | 1 | yes | Per-disc cap on duplicated bytes. |
| `split.threshold` | fraction | 0.25 | 1 | yes | Split a file only when it exceeds the remaining capacity by more than this fraction of a disc. |
| `consolidate.max_plan_discs` | integer | 20 | 3 | no | Consolidation trigger. |
| `consolidate.max_spread_ratio` | number | 2.0 | 3 | no | Consolidation trigger. |
| `consolidate.max_restore_hours` | integer | 8 | 3 | no | Consolidation trigger. |
| `consolidate.max_disc_age` | duration | 5 years | 3 | no | Consolidation trigger. |

### 17.11 Metadata

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `metadata.user_group_names` | boolean | true | 1 | yes | Store user and group names beside the numeric ids. |
| `metadata.ctime` | boolean | true | 1 | yes | Store ctime when the source reports it. False selects the `size_mtime` quick check. |
| `metadata.atime` | boolean | false | 2 | yes | Store access time. |
| `metadata.btime` | boolean | false | 2 | yes | Store birth time when the source reports it. |
| `metadata.xattr` | boolean | false | 2 | yes | Store extended attributes. |
| `metadata.acl` | boolean | false | 2 | yes | Store POSIX and NFSv4 ACLs. |
| `metadata.windows` | boolean | false | 2 | yes | Store Windows attributes and security descriptors. |
| `restore.owner_policy` | enum | `auto` | 1 | no | `auto`, `numeric`, `name` or `none`. See section 15.1. |

### 17.12 Staging and cache

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `staging.dir` | path | `<repo>/staging` | 1 | no | Staging store location. |
| `staging.retain_after_clean` | duration | 7 days | 1 | no | Retention before an object becomes GC-eligible. |
| `staging.budget_bytes` | bytes | unset | 1 | no | Warn when staging exceeds this size. |
| `staging.allow_unsafe_fs` | boolean | false | 1 | no | Allow staging on a filesystem that fails the startup check. Recorded in the state log. |
| `commitbundle.dir` | path | `<staging>/commitbundles` | Backlog | no | Where `import` unpacks and where `commit --out` writes by default. |
| `commitbundle.keep_after_import` | boolean | false | Backlog | no | Keep the bundle directory after a successful import. |
| `commitbundle.catalog_max_age` | duration | 30 days | Backlog | no | Warn when `commit --out` uses an older exported catalog. |
| `cache.dir` | path | see section 2.4 | 1 | no | Local cache location. |
| `cache.format_version` | integer | 1 | 1 | no | Delete and rebuild on a mismatch. |

### 17.13 Restore

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `restore.staging_budget` | bytes | 16 GiB | 1 | no | Peak staging allowed. The planner falls back to multi-pass above it. |
| `restore.score` | enum | `bytes` | 1 | no | Greedy score: `bytes` or `objects`. |
| `restore.drives` | integer | 1 | 1 | no | Number of drives to plan for. Above 1 is Phase 3. |
| `restore.rate_mb_s` | number | 20 | 1 | no | Effective read rate for the time model. |
| `restore.switch_seconds` | integer | 60 | 1 | no | Fixed cost per disc switch. |
| `restore.interactive` | boolean | false | 1 | no | Prompt on every disc, not only on a mismatch. |
| `restore.eject` | boolean | true | 1 | no | Eject after each disc. |

### 17.14 Scrub

| Key | Type | Default | Phase | Changes disc bytes | Meaning |
|---|---|---|---:|---|---|
| `scrub.first_check_hours` | integer | 24 | 1 | no | First full verify after burning, on a second drive. |
| `scrub.schedule` | string | `3m,12m,then 12m to 5y,then 6m` | 1 | no | The schedule of section 13.3, in the grammar below. |
| `scrub.degraded_interval` | duration | 3 months | 1 | no | Interval for a degraded disc. |
| `scrub.max_disc_age` | duration | 10 years | 1 | no | Proactive re-burn age. |

`scrub.schedule` grammar, in EBNF. Ages are measured from the burn time of the
disc. Spaces are allowed around every token.

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
every `d1` from the end of the previous phase until age `d2`. An interval with
no `to` runs until `scrub.max_disc_age`. Phases must be in ascending age order,
and a schedule that is not is refused. A month is 30 days and a year is 365
days for this purpose.

---

## 18. JSON document evolution

The burn plan rendering of section 11.6, the health report of section 13.5, the
restore plan of section 14.4, the loss report of section 15.9, and every other
`"version": 1` JSON document this specification defines follow one rule,
because none of them is part of the on-disc contract.

1. A field may be added freely. A reader ignores a field it does not know.
2. A field is never removed and never retyped while `version` stays the same.
   Emptying a field or making it always absent is also a removal.
3. Removing a field, retyping it, or changing what an existing field means
   bumps `version`. A reader refuses a `version` above the highest it knows,
   and names the value.
4. `version` is a plain integer, never a major and minor pair. These documents
   carry no partial-compatibility promise.

In a JSON report, a field whose value is unknown is absent, never null.

---

## 19. Exit code registry

This is the one registry of exit codes.

Common exit codes:

| Code | Meaning |
|---:|---|
| 0 | Success. |
| 1 | Success with a warning, or partial success. |
| 2 | Failure. |
| 3 | A required disc or file is missing. |
| 4 | A precondition failed, for example an unpatched burner. |

Command-specific meanings that narrow the common set:

| Command | Code | Meaning |
|---|---:|---|
| `init` | 2 | The directory already holds a repository, or `--repo-uuid` was given with neither sequence form. |
| `commit` | 1 | Some files could not be read, or were unstable. |
| `pack` | 1 | The run is smaller than requested, or a trigger was not met. |
| `pack` | 4 | A forced capacity conflicts with the recorded value. |
| `append` | 4 | The disc is closed. |
| `burn` | 3 | The expected disc is not in the drive. |
| `burn` | 4 | The burner version is unknown or unpatched, or `--exec` ran on a platform other than Linux. |
| `close` | 4 | The disc is already closed or sealed. |
| `verify`, `scrub` | 1, 2 | Repaired or degraded; unrecoverable loss. |
| `health` | 1, 2 | Any disc is degraded; any disc is failed. |
| `plan` | 1, 3 | The plan holds at least one probable object; a required disc is missing, or a filter negative proves an object absent from every run. |
| `restore` | 1, 2 | Data restored with metadata loss; data restore failed. |
| `rebuild-cache`, `reindex`, `sync` | 1 | The rebuild is partial; some discs were not available; some paths could not be listed. |
| `import` | 1, 2 | Some objects were already present; a verification failure or a repository uuid mismatch. |
| `gc` | 1 | Nothing was eligible. |
| `ls` | 3 | A needed tree object is unavailable. |
| `disc` | 3 | The uuid is unknown. |

Section 15.8 gives the restore metadata exit codes, which are the same set.

Every refusal is loud, names the field and the value, and never touches the
bytes it refused. Every partial case reads the data in full and puts the loss
in the report. An error about an object names the object. An error about a run
names the run seq and the disc uuid.

---

## 20. Failure and recovery actions

| # | Failure | Recovery action |
|---:|---|---|
| 1 | A burn fails midway | Objects return to STAGED. Pack a new run, with a new `run_seq`, onto a fresh disc. |
| 2 | Verify finds bad sectors | RS decode inside the run. |
| 3 | Erasures exceed `m` in a stripe | Content-addressed re-fetch, then cross-disc parity, then mirror, then the source. |
| 4 | A whole disc is lost or destroyed | Cross-disc parity group, mirror, or the source. Otherwise the loss is enumerable from the catalog. |
| 5 | The newest disc is lost | Read every other disc's catalog. The previous disc carries the state as of its own burn. |
| 6 | The local cache is lost | Rebuild from the newest disc, level 1. Then level 3 lazily. |
| 7 | The cache is wrong | Refuse the cache. Rebuild. |
| 8 | The state log is truncated by a crash | Replay up to the bad record. Re-scan staging. Objects with no record are treated as STAGED. |
| 9 | An object moved after an append | Abort the append. Keep objects PACKED. Mark the run for re-burn and report the moved ids. Re-burn as a new run with a new `run_seq`. |
| 10 | The spare area is exhausted | Raw append mode, profile 1 and Phase 2 only. Otherwise a fresh disc. |
| 11 | The drive offers no POW | Use profile 0 sealed. |
| 12 | A filter false positive is unconfirmed | Write the chunk again. Log it in `pending-confirm.log`. |
| 12a | A file changes while it is read | The parent entry is reused, or a new file is stored with the `UNSTABLE` flag. Report it and retry next commit. |
| 13 | A tree entry has an illegal name | Report the tree id and the entry index. The rest of the tree restores. |
| 14 | An unknown critical TLV | Refuse the entry. Upgrade the tool. |
| 15 | A content id does not match after decompression | Treat it as an erasure. Heal it. |
| 16 | A restore runs out of staging space | The planner splits into passes before it starts. |
| 17 | A required disc is missing at plan time | Fail before any read. Name the disc and its label. |
| 18 | A snapshot references a missing object | Report "missing" with a proof from the filters. Restore what exists. |
| 19 | The burner build is unpatched | `burn --exec` refuses. `burn --print` warns. |
| 20 | A disc is substituted | Refuse the disc. Report the expected and found value. |
| 21 | Media generation goes out of production | Migrate the library. |
| 22 | A disc reaches `scrub.max_disc_age` | Proactive re-burn. |

---

## 21. Conformance checklist

A build claims Phase 1 conformance only when every item below holds. An item
marked FORMAT is proved in `FORMAT.md` and is listed here so that the checklist
is complete.

| # | Item | Where |
|---:|---|---|
| 1 | Every structure encodes and decodes to its byte-offset table, little-endian, checksum last. | FORMAT |
| 2 | Unknown major and required bits refused; unknown minor and optional bits ignored. | FORMAT |
| 3 | Content ids are BLAKE3-256 and SHA-256 multihashes of the uncompressed payload. | FORMAT |
| 4 | FastCDC P4 with the frozen Gear table and masks gives the golden cut points; P3 and P5 available. | FORMAT |
| 5 | Every chunk below `bundle.threshold` goes into a bundle. | FORMAT |
| 6 | zstd per chunk after hashing, with the `compression.min_gain` rule. | FORMAT |
| 7 | Trees, chunklists, snapshots and refs are canonical. | FORMAT |
| 8 | Names are validated at parse time; restore holds the invariants of section 15.6. | FORMAT and section 15.6 |
| 9 | The root tree is synthetic, one entry per source root, with `ROOT_PATH`. | FORMAT |
| 10 | A profile 0 disc is one run, UDF 2.01, POW `spare:min`, open by default; `pack --close` seals it and burns the full-size image. | Sections 10.1, 10.2, 12.2 |
| 11 | The fill order and the build passes hold; `layout.bin` matches the read-back extents. | FORMAT and sections 10.3, 10.4 |
| 12 | Every run carries `k = 231`, `m = 23` parity, the checksum column and `m + 2` header copies. | FORMAT |
| 13 | Every run carries its filter, its manifest with the seven mandatory chunks, and its catalog. | FORMAT |
| 14 | The filter query rule accepts every inserted key. | FORMAT |
| 15 | A filter hit is confirmed against a manifest before data is dropped. | FORMAT |
| 16 | Every command works with the cache deleted; restore works from the newest image alone. | Sections 2.7, 14.8 |
| 17 | `verify` is the only path to CLEAN; GC deletes only GC-ELIGIBLE. | Sections 4.2, 4.5 |
| 18 | The repository lock is taken as section 6 states. | Section 6 |
| 19 | The packer applies rule 1 and the capping knobs in rank order. | Sections 8.1, 8.2 |
| 20 | The restore plan is deterministic, disc-major, printed before any read, and fails up front. | Sections 14.1, 14.2, 14.4 |
| 21 | Phase 1 metadata is stored and restored in order; the loss report and exit codes hold. | Section 15 |
| 22 | The quick check, the in-flight rule and the unchanged-commit rule hold. | Sections 7.1, 7.2, 7.6 |
| 22a | The local ref log and the pending snapshot chain hold. | Sections 5.1, 5.2 |
| 23 | Later-phase commands, options and config keys are refused with a message that names the phase. | Sections 16.1, 17 |
| 24 | `burn --print` touches no device; `burn --exec` is Linux-only and refuses an unpatched burner. | Sections 11.1, 11.9 |
| 25 | `README.txt` and `FORMAT.txt` are byte-identical, hashed into `layout.bin`, covered by parity. | FORMAT |
| 26 | Every golden vector passes. | FORMAT |

---

## 22. Test list

Every test below must run. A test marked FORMAT proves a rule of `FORMAT.md`;
a test marked OPS proves a rule of this document. Every burn test uses an image
file first. Physical burns are a manual checklist, not CI.

| # | Test | Where | # | Test | Where | # | Test | Where |
|---|---|---|---|---|---|---|---|---|
| 1 | Format round-trip for every structure, against byte-exact golden files | FORMAT | 2 | Chunker golden vectors for P3, P4 and P5 | FORMAT | 3 | Chunker determinism across buffer sizes and read patterns | FORMAT |
| 4 | Filter false-positive bound over a large synthetic key set | FORMAT | 5 | Manifest lookup: every present id found, no absent id found | FORMAT | 6 | RS reconstruct at exactly `m` erasures per stripe | FORMAT |
| 7 | RS refusal at `m + 1` erasures | FORMAT | 8 | Burst damage across column boundaries | FORMAT | 9 | Checksum column locates a silently corrupted sector | FORMAT |
| 10 | Append LBA stability: every earlier object keeps its LBA | OPS | 11 | Cache-less restore from the newest image alone | OPS | 12 | Restore plan determinism | OPS |
| 13 | Metadata restore matrix as a non-root user | OPS | 14 | Sparse round-trip: holes in, holes out | FORMAT | 15 | Compression heuristic: a low-gain chunk is stored uncompressed | FORMAT |
| 16 | Tree canonical order: two identical directories hash identically | FORMAT | 17 | Name validation: every illegal name is refused at parse time | FORMAT | 18 | Symlink redirection attack: a planted symlink does not escape | OPS |
| 19 | Hardlink group: restoring one member gives a correct file | OPS | 20 | State log replay after a truncated write | OPS | 21 | GC refuses to delete an object that is not CLEAN | OPS |
| 22 | Burn plan refusal on a bad CRC or a misaligned seek | OPS | 23 | Forced capacity: the image, the budget and the FEC layout all shrink | OPS | 24 | Reserve estimator: the worked examples and the invariant identities | OPS |
| 25 | Connectivity check reports a missing object with no disc mounted | OPS | 26 | Quick check: a matching file is never read | OPS | 27 | In-flight change: parent entry reused, else `UNSTABLE` set | OPS |
| 28 | Mirror mode: `sync` transfers only changed paths and the snapshot matches direct mode | OPS | 29 | BURNED objects stay in staging until `verify` succeeds | OPS | 30 | Catalog cap: the manifest history shrinks and the snapshot objects stay | FORMAT |
| 31 | `UNSTABLE` round trip through write, read, restore and `ls` | FORMAT | 32 | `pack --close` sealed path: no format step, full capacity, `-dvd-compat` | OPS | 33 | Profile 0 open image mounts with the anchor at LBA 256 alone | OPS |
| 34 | `pack --close` image: one write step of the full-size image, three anchors | OPS | 35 | Run table: the planner names the right disc when run order is not disc order | OPS | 36 | Dedup confirmation: an unconfirmed hit rewrites and logs; a confirmed hit drops | FORMAT |
| 37 | Capping: the knobs apply in the rank of section 8.2 | OPS | 38 | Unchanged commit writes no snapshot object; `--force` writes one | OPS | 39 | Local ref log: parent resolves locally first; a recreated repository recovers every ref | OPS |
| 40 | RS worked example: the `k = 3`, `m = 2` bytes encode and decode as printed | FORMAT | 41 | `pad.bin` length, File Entry placement, and the zero-length record's `start_lba` | FORMAT | 42 | Filter construction reproduces the golden filter bytes from 1,000 keys | FORMAT |
| 43 | `README.txt` and `FORMAT.txt` byte-identical to the golden files | FORMAT | 44 | Burn step listing reproduces `payload_hash` for a kind 2 and a kind 5 step | FORMAT | 45 | Run table status: a failed verify leaves `run_status` 3; no run is omitted | FORMAT |
| 46 | Probable coverage: marked in the plan, confirmed at read time | OPS | 47 | Partial `pack`: no valid `burn.bin`; the next `pack` recovers | OPS | 48 | A version 1 writer emits no `"BMAP"` and no `"RIDX"`; a reader skips unknown chunk ids | FORMAT |
| 49 | Kind 6 is refused in a manifest, layout and state log record | FORMAT | 50 | Fill order: two writers produce the same step 5 and step 6 order | FORMAT | 51 | Prerequisites: one edge deep; `"SRCR"` equals the distinct run seqs of `"PREQ"` | FORMAT |
| 52 | Bundle boundary: the close rule is exact and reproducible | FORMAT | 53 | Tree entry layout: area order, alignment and padding | FORMAT | 54 | TLV spill: largest first, ties by lowest `tlv_type`, loop stops at the threshold | FORMAT |
| 55 | Hardlink membership depends on the snapshot alone | FORMAT | 56 | Close state: `run_flags` bit 0 and `state_flags` bit 0 set, `sealed` unchanged, append refused | FORMAT | 57 | Withdrawn run: catalog copies stay; dedup, planner, prerequisites and refs exclude it | FORMAT |
| 58 | Run table completion: only `run_status` and `run_header_hash` ever change | FORMAT | 59 | `FORMAT.txt` equals the normative text byte for byte | FORMAT |  |  |  |

---

## 23. Manual physical checklist

These steps need a real drive and real media. They run once per release and
after any change to the burn path.

1. `dvd+rw-mediainfo` on a blank disc records the profile and the capacity.
2. A first burn completes and `growisofs -F` reports the expected next writable
   address afterwards.
3. Eject, reload, and read back. The whole image compares equal.
4. The LBA map read from the disc matches the recorded map.
5. Mount on Linux. The file count matches.
6. Mount on Windows 10 and 11. Explorer shows the tree. Hash one object from
   the first append and one from the last.
7. Mount on macOS 15. The file count matches.
8. Mount on Windows XP, profile 1 only. The volume mounts at UDF 2.01.
9. Verify on a second drive of a different model, within 24 hours.
10. M-DISC at speed 2 completes and verifies. A BD-R XL 100 GB disc fills to
    the data budget with no capacity surprise.

---

## 24. Burning-host command reference

Every command in this section is **informative**. It runs on the Linux host
that holds the drive. The normative command lines are in sections 10.2, 10.5,
10.6 and 11.7.

```bash
# Probe the drive and the medium.
eval "$(growisofs -F /dev/sr0)"; NWA=$(( next_session / 2048 ))
dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Number of Sessions|Next Writable Address'
# Check a built UDF image before the burn.
udfinfo run.udf | grep -q '^integrity=closed' || { echo "dirty image"; exit 1; }
udfinfo run.udf | grep -c 'type=ANCHOR'        # expect 3
# Verify after the burn.
eject /dev/sr0 && eject -t /dev/sr0 && sleep 5
noahsark verify --disc <uuid> --level=integrity --drive /dev/sr1
# Recover a damaged disc. The map file is the erasure list for the FEC layer.
ddrescue -b 2048 -n -r1 /dev/sr0 rescued.img rescue.map
ddrescue -b 2048 -d -r3 /dev/sr0 rescued.img rescue.map
noahsark verify --image rescued.img --mapfile rescue.map --heal --report health.json
# Read back the LBA map under profile 2.
isoinfo -i disc.iso -T "$SESSION_START" -l
# Tool versions to check at startup: growisofs, mkudffs, udfinfo,
# genisoimage and ddrescue. Expect dvd+rw-tools 7.1-14 or newer and
# udftools 2.3 or newer.
growisofs -version 2>&1 | head -2
mkudffs --help 2>&1 | head -1
```

M-DISC replaces speed 4 with speed 2. A CI dry run adds a dry-run flag.

---

## Appendix A. Decision index

This index lists every design decision of the source ledger and where it is
carried. A cell reading "FORMAT.md section N" names a section of the on-disc
format document. A bare section number names a section of this document. "notes"
means the decision is rationale, evidence, an estimate, a scheduling note or an
implementation note, and belongs to neither normative document.

| Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where | Id | Where |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| D001 | FORMAT.md section 2.1 | D002 | FORMAT.md section 2.1 | D003 | FORMAT.md section 2.1 | D004 | FORMAT.md section 2.1 | D005 | FORMAT.md section 2.1 | D006 | FORMAT.md section 2.1 | D007 | FORMAT.md section 2.1 | D008 | FORMAT.md section 2.1 | D009 | FORMAT.md section 2.1 | D010 | FORMAT.md section 2.1 | D011 | FORMAT.md section 2.1 | D012 | FORMAT.md section 2.1 | D013 | FORMAT.md section 2.1 | D014 | FORMAT.md section 2.1 | D015 | FORMAT.md section 2.2 | D016 | FORMAT.md section 2.3 | D017 | FORMAT.md section 2.4 | D018 | FORMAT.md section 2.4 | D019 | FORMAT.md section 2.4 | D020 | FORMAT.md section 2.4 | D021 | FORMAT.md section 2.6 | D022 | FORMAT.md section 2.7 | D023 | FORMAT.md section 2.7 | D024 | FORMAT.md section 2.7 | D025 | FORMAT.md section 2.7 | D026 | FORMAT.md section 13 |
| D027 | FORMAT.md section 2.8 | D028 | FORMAT.md section 12.1 | D029 | FORMAT.md section 2.10 | D030 | FORMAT.md section 3.1 | D031 | FORMAT.md section 3.1 | D032 | FORMAT.md section 3.1 | D033 | FORMAT.md section 3.2 | D034 | FORMAT.md section 3.2 | D035 | FORMAT.md section 3.3 | D036 | FORMAT.md section 3.3 | D037 | FORMAT.md section 3.4 | D038 | FORMAT.md section 3.5 | D039 | FORMAT.md section 3.5 | D040 | FORMAT.md section 3.5 | D041 | FORMAT.md section 3.5 | D042 | FORMAT.md section 3.5 | D043 | FORMAT.md section 3.6 | D044 | FORMAT.md section 3.6 | D045 | FORMAT.md section 3.6 | D046 | FORMAT.md section 3.6 | D047 | FORMAT.md section 3.7 | D048 | FORMAT.md section 4.1 | D049 | FORMAT.md section 4.1 | D050 | FORMAT.md section 4.2 | D051 | FORMAT.md section 4.2 | D052 | FORMAT.md section 4.2 |
| D053 | FORMAT.md section 4.2 | D054 | FORMAT.md section 4.3 | D055 | FORMAT.md section 4.3 | D056 | FORMAT.md section 4.1 | D057 | FORMAT.md section 4.4 | D058 | FORMAT.md section 4.4 | D059 | FORMAT.md section 4.4 | D060 | FORMAT.md section 4.4 | D061 | FORMAT.md section 4.5 | D062 | FORMAT.md section 4.6 | D063 | FORMAT.md section 4.6 | D064 | FORMAT.md section 4.6 | D065 | FORMAT.md section 4.6 | D066 | FORMAT.md section 4.6 | D067 | FORMAT.md section 4.6 | D068 | FORMAT.md section 4.7 | D069 | FORMAT.md section 5.1 | D070 | FORMAT.md section 5.1 | D071 | FORMAT.md section 5.2 | D072 | FORMAT.md section 5.3 | D073 | FORMAT.md section 5.3 | D074 | FORMAT.md section 5.3 | D075 | FORMAT.md section 5.3 | D076 | FORMAT.md section 5.4 | D077 | FORMAT.md section 5.4 | D078 | FORMAT.md section 5.5 |
| D079 | FORMAT.md section 5.6 | D080 | FORMAT.md section 6.1 | D081 | FORMAT.md section 6.1 | D082 | FORMAT.md section 6.1 | D083 | FORMAT.md section 6.2 | D084 | FORMAT.md section 6.4 | D085 | FORMAT.md section 6.4 | D086 | FORMAT.md section 6.4 | D087 | FORMAT.md section 6.3 | D088 | FORMAT.md section 6.3 | D089 | FORMAT.md section 6.3 | D090 | FORMAT.md section 6.3 | D091 | FORMAT.md section 6.3 | D092 | FORMAT.md section 6.3 | D093 | FORMAT.md section 6.3 | D094 | FORMAT.md section 6.3 | D095 | FORMAT.md section 6.3 | D096 | FORMAT.md section 6.3 | D097 | FORMAT.md section 6.3 | D098 | FORMAT.md section 6.3 | D099 | FORMAT.md section 6.3 | D100 | FORMAT.md section 6.3 | D101 | FORMAT.md section 6.5 | D102 | FORMAT.md section 6.5 | D103 | FORMAT.md section 6.8 | D104 | FORMAT.md section 6.8 |
| D105 | FORMAT.md section 6.8 | D106 | FORMAT.md section 6.8 | D107 | FORMAT.md section 6.7 | D108 | FORMAT.md section 6.7 | D109 | FORMAT.md section 6.9 | D110 | FORMAT.md section 6.9 | D111 | FORMAT.md section 6.9 | D112 | FORMAT.md section 6.9 | D113 | FORMAT.md section 6.11 | D114 | FORMAT.md section 6.11 | D115 | FORMAT.md section 6.11 | D116 | FORMAT.md section 6.12 | D117 | FORMAT.md section 6.12 | D118 | FORMAT.md section 6.13 | D119 | FORMAT.md section 6.10 | D120 | FORMAT.md section 6.10 | D121 | FORMAT.md section 6.10 | D122 | FORMAT.md section 6.6 | D123 | FORMAT.md section 6.6 | D124 | FORMAT.md section 6.9 | D125 | FORMAT.md section 6.6 | D126 | FORMAT.md section 6.6 | D127 | FORMAT.md section 6.6 | D128 | 15.1 | D129 | 15.1 | D130 | 15.1 |
| D131 | 15.2 | D132 | 15.2 | D133 | 15.4 | D134 | 15.4 | D135 | 15.4 | D136 | 15.5 | D137 | FORMAT.md section 2.11 | D138 | 15.6 | D139 | 15.3 | D140 | 15.7 | D141 | FORMAT.md section 6.15 | D142 | FORMAT.md section 6.15 | D143 | FORMAT.md section 6.15 | D144 | FORMAT.md section 6.15 | D145 | FORMAT.md section 6.15 | D146 | FORMAT.md section 6.15 | D147 | FORMAT.md section 6.15 | D148 | FORMAT.md section 6.15 | D149 | FORMAT.md section 6.15 | D150 | FORMAT.md section 6.18 | D151 | FORMAT.md section 6.18 | D152 | FORMAT.md section 6.18 | D153 | FORMAT.md section 6.18 | D154 | FORMAT.md section 6.18 | D155 | FORMAT.md section 6.18 | D156 | FORMAT.md section 6.19 |
| D157 | FORMAT.md section 6.20 | D158 | FORMAT.md section 6.20 | D159 | FORMAT.md section 7.1 | D160 | FORMAT.md section 7.1 | D161 | FORMAT.md section 7.1 | D162 | FORMAT.md section 7.2 | D163 | FORMAT.md section 7.2 | D164 | FORMAT.md section 7.2 | D165 | FORMAT.md section 7.2 | D166 | FORMAT.md section 7.5 | D167 | FORMAT.md section 7.5 | D168 | FORMAT.md section 7.5 | D169 | FORMAT.md section 7.5 | D170 | FORMAT.md section 7.5 | D171 | FORMAT.md section 7.5 | D172 | FORMAT.md section 7.5 | D173 | 12.2 | D174 | 12.2 | D175 | FORMAT.md section 7.12 | D176 | 12.2 | D177 | 12.2 | D178 | 12.3 | D179 | 12.3 | D180 | 12.3 | D181 | 10.2 | D182 | FORMAT.md section 7.12 |
| D183 | 12.4 | D184 | FORMAT.md section 7.2 | D185 | 12.5 | D186 | 10.5 | D187 | 12.5 | D188 | FORMAT.md section 7.13 | D189 | 12.5 | D190 | FORMAT.md section 7.14 | D191 | 12.6 | D192 | FORMAT.md section 7.15 | D193 | 12.1 | D194 | FORMAT.md section 7.3 | D195 | FORMAT.md section 7.3 | D196 | FORMAT.md section 7.3 | D197 | FORMAT.md section 7.3 | D198 | FORMAT.md section 7.3 | D199 | FORMAT.md section 7.3 | D200 | FORMAT.md section 7.6 | D201 | FORMAT.md section 7.6 | D202 | FORMAT.md section 7.6 | D203 | FORMAT.md section 7.6 | D204 | FORMAT.md section 7.6 | D205 | FORMAT.md section 7.6 | D206 | FORMAT.md section 7.6 | D207 | FORMAT.md section 7.6 | D208 | FORMAT.md section 7.6 |
| D209 | FORMAT.md section 7.7 | D210 | FORMAT.md section 7.7 | D211 | FORMAT.md section 7.7 | D212 | FORMAT.md section 7.7 | D213 | FORMAT.md section 7.7 | D214 | FORMAT.md section 7.9 | D215 | FORMAT.md section 7.9 | D216 | FORMAT.md section 7.9 | D217 | FORMAT.md section 8.3 | D218 | FORMAT.md section 7.10 | D219 | FORMAT.md section 7.10 | D220 | FORMAT.md section 7.10 | D221 | FORMAT.md section 7.10 | D222 | FORMAT.md section 7.10 | D223 | FORMAT.md section 7.10 | D224 | FORMAT.md section 7.10 | D225 | FORMAT.md section 7.10 | D226 | FORMAT.md section 7.10 | D227 | FORMAT.md section 7.10 | D228 | FORMAT.md section 7.10 | D229 | FORMAT.md section 7.11 | D230 | FORMAT.md section 7.11 | D231 | FORMAT.md section 8.1 | D232 | FORMAT.md section 8.1 | D233 | 10.1 | D234 | 10.1 |
| D235 | 10.1 | D236 | 10.1 | D237 | FORMAT.md section 8.6 | D238 | 10.4 | D239 | 10.3 | D240 | 10.5 | D241 | 10.5 | D242 | 10.5 | D243 | FORMAT.md section 7.5 | D244 | 10.5 | D245 | 10.5 | D246 | FORMAT.md section 8.1 | D247 | 10.6 | D248 | FORMAT.md section 3.5 | D249 | 10.6 | D250 | 10.6 | D251 | 10.6 | D252 | 10.3 | D253 | 8.5 | D254 | FORMAT.md section 8.2 | D255 | FORMAT.md section 8.2 | D256 | FORMAT.md section 8.2 | D257 | FORMAT.md section 8.4 | D258 | FORMAT.md section 8.4 | D259 | FORMAT.md section 8.5 | D260 | FORMAT.md section 8.5 |
| D261 | FORMAT.md section 8.5 | D262 | FORMAT.md section 8.5 | D263 | FORMAT.md section 8.7 | D264 | FORMAT.md section 8.7 | D265 | FORMAT.md section 8.7 | D266 | FORMAT.md section 8.6 | D267 | FORMAT.md section 8.6 | D268 | FORMAT.md section 8.6 | D269 | FORMAT.md section 8.6 | D270 | FORMAT.md section 8.6 | D271 | FORMAT.md section 8.6 | D272 | FORMAT.md section 8.6 | D273 | FORMAT.md section 8.6 | D274 | FORMAT.md section 8.6 | D275 | FORMAT.md section 8.6 | D276 | FORMAT.md section 8.6 | D277 | FORMAT.md section 8.6 | D278 | FORMAT.md section 8.6 | D279 | FORMAT.md section 8.6 | D280 | FORMAT.md section 8.6 | D281 | FORMAT.md section 8.6 | D282 | FORMAT.md section 8.6 | D283 | 10.4 | D284 | FORMAT.md section 8.6 | D285 | 11.1 | D286 | 11.1 |
| D287 | 11.1 | D288 | 11.1 | D289 | 11.4 | D290 | 11.4 | D291 | 11.4 | D292 | 11.4 | D293 | 11.4 | D294 | 11.4 | D295 | 11.4 | D296 | 11.5 | D297 | 11.6 | D298 | 11.4 | D299 | 11.7 | D300 | 11.7 | D301 | 11.7 | D302 | 11.7 | D303 | 11.8 | D304 | 11.8 | D305 | 11.8 | D306 | 11.8 | D307 | 9.1 | D308 | 11.9 | D309 | 11.9 | D310 | notes | D311 | 11.11 | D312 | FORMAT.md section 9 |
| D313 | FORMAT.md section 9 | D314 | FORMAT.md section 9 | D315 | FORMAT.md section 9 | D316 | FORMAT.md section 9 | D317 | FORMAT.md section 9 | D318 | FORMAT.md section 9 | D319 | FORMAT.md section 9 | D320 | 9 | D321 | 9.3 | D322 | 9.9 | D323 | 9.7 | D324 | FORMAT.md section 10.1 | D325 | FORMAT.md section 10.1 | D326 | FORMAT.md section 10.1 | D327 | FORMAT.md section 10.1 | D328 | FORMAT.md section 10.1 | D329 | FORMAT.md section 10.1 | D330 | FORMAT.md section 10.1 | D331 | FORMAT.md section 10.1 | D332 | FORMAT.md section 10.1 | D333 | FORMAT.md section 10.1 | D334 | FORMAT.md section 10.1 | D335 | FORMAT.md section 10.1 | D336 | FORMAT.md section 10.2 | D337 | FORMAT.md section 10.2 | D338 | FORMAT.md section 10.2 |
| D339 | FORMAT.md section 10.2 | D340 | FORMAT.md section 10.2 | D341 | FORMAT.md section 10.2 | D342 | notes | D343 | FORMAT.md section 10.4 | D344 | FORMAT.md section 10.4 | D345 | 13.4 | D346 | 13.4 | D347 | 13.4 | D348 | 13.2 | D349 | FORMAT.md section 7.11 | D350 | FORMAT.md section 10.6 | D351 | 13.2 | D352 | FORMAT.md section 10.7 | D353 | FORMAT.md section 10.7 | D354 | FORMAT.md section 10.7 | D355 | 13.5 | D356 | notes | D357 | notes | D358 | FORMAT.md section 10.3 | D359 | FORMAT.md section 10.3 | D360 | FORMAT.md section 10.3 | D361 | FORMAT.md section 10.3 | D362 | FORMAT.md section 10.3 | D363 | FORMAT.md section 10.3 | D364 | FORMAT.md section 10.5 |
| D365 | FORMAT.md section 10.3 | D366 | notes | D367 | FORMAT.md section 11.7 | D368 | FORMAT.md section 11.1 | D369 | FORMAT.md section 11.1 | D370 | FORMAT.md section 11.1 | D371 | FORMAT.md section 11.1 | D372 | FORMAT.md section 11.1 | D373 | FORMAT.md section 11.1 | D374 | FORMAT.md section 11.1 | D375 | FORMAT.md section 11.1 | D376 | FORMAT.md section 11.1 | D377 | FORMAT.md section 11.1 | D378 | FORMAT.md section 11.1 | D379 | FORMAT.md section 11.1 | D380 | FORMAT.md section 11.1 | D381 | FORMAT.md section 11.1 | D382 | FORMAT.md section 11.1 | D383 | FORMAT.md section 11.2 | D384 | FORMAT.md section 11.2 | D385 | FORMAT.md section 11.2 | D386 | FORMAT.md section 11.2 | D387 | FORMAT.md section 11.2 | D388 | FORMAT.md section 11.2 | D389 | FORMAT.md section 11.2 | D390 | FORMAT.md section 11.2 |
| D391 | FORMAT.md section 11.2 | D392 | FORMAT.md section 11.3 | D393 | FORMAT.md section 11.3 | D394 | FORMAT.md section 11.3 | D395 | FORMAT.md section 11.3 | D396 | FORMAT.md section 11.3 | D397 | FORMAT.md section 11.3 | D398 | FORMAT.md section 11.3 | D399 | FORMAT.md section 11.4 | D400 | FORMAT.md section 11.4 | D401 | FORMAT.md section 11.4 | D402 | FORMAT.md section 11.4 | D403 | FORMAT.md section 11.4 | D404 | FORMAT.md section 11.4 | D405 | FORMAT.md section 11.4 | D406 | FORMAT.md section 11.4 | D407 | FORMAT.md section 11.5 | D408 | FORMAT.md section 11.5 | D409 | FORMAT.md section 11.5 | D410 | FORMAT.md section 11.5 | D411 | FORMAT.md section 11.5 | D412 | FORMAT.md section 11.5 | D413 | FORMAT.md section 11.5 | D414 | FORMAT.md section 11.5 | D415 | FORMAT.md section 11.5 | D416 | FORMAT.md section 11.5 |
| D417 | FORMAT.md section 11.6 | D418 | FORMAT.md section 11.6 | D419 | FORMAT.md section 11.6 | D420 | FORMAT.md section 11.7 | D421 | FORMAT.md section 11.7 | D422 | FORMAT.md section 11.7 | D423 | FORMAT.md section 11.7 | D424 | FORMAT.md section 11.8 | D425 | FORMAT.md section 11.8 | D426 | FORMAT.md section 11.8 | D427 | FORMAT.md section 11.8 | D428 | FORMAT.md section 11.9 | D429 | FORMAT.md section 11.9 | D430 | FORMAT.md section 11.9 | D431 | FORMAT.md section 11.10 | D432 | FORMAT.md section 11.10 | D433 | FORMAT.md section 11.10 | D434 | FORMAT.md section 11.10 | D435 | FORMAT.md section 12.3 | D436 | 2.4 | D437 | 2.4 | D438 | 2.4 | D439 | 2.5 | D440 | 2.5 | D441 | 2.6 | D442 | 2.6 |
| D443 | 2.3 | D444 | 2.3 | D445 | 2.3 | D446 | 4.1 | D447 | 4.2 | D448 | 4.2 | D449 | 4.2 | D450 | 4.2 | D451 | 4.2 | D452 | 4.2 | D453 | 4.2 | D454 | 4.2 | D455 | 4.4 | D456 | 4.4 | D457 | 4.4 | D458 | 3.1 | D459 | 4.3 | D460 | 4.3 | D461 | 4.5 | D462 | 4.5 | D463 | 4.5 | D464 | 6 | D465 | 6 | D466 | 6 | D467 | 6 | D468 | 3.3 |
| D469 | 5.1 | D470 | 5.1 | D471 | 5.1 | D472 | 5.1 | D473 | 5.2 | D474 | 3.4 | D475 | 3.4 | D476 | 5.3 | D477 | 5.3 | D478 | 2.1 | D479 | 2.2 | D480 | 2.2 | D481 | 8.1 | D482 | 8.1 | D483 | FORMAT.md section 8.6 | D484 | 8.1 | D485 | 8.1 | D486 | 8.1 | D487 | 8.2 | D488 | 8.2 | D489 | 8.3 | D490 | 8.2 | D491 | FORMAT.md section 11.2 | D492 | 8.6 | D493 | FORMAT.md section 11.2 | D494 | 8.5 |
| D495 | 8.7 | D496 | 8.7 | D497 | 7.1 | D498 | 7.1 | D499 | 7.1 | D500 | 7.2 | D501 | 7.2 | D502 | 7.2 | D503 | 7.2 | D504 | 7.2 | D505 | notes | D506 | FORMAT.md section 6.7 | D507 | FORMAT.md section 6.7 | D508 | 7.6 | D509 | 7.6 | D510 | 7.5 | D511 | 7.5 | D512 | FORMAT.md section 6.16 | D513 | FORMAT.md section 6.16 | D514 | FORMAT.md section 6.16 | D515 | FORMAT.md section 6.16 | D516 | 7.5 | D517 | FORMAT.md section 6.17 | D518 | FORMAT.md section 6.17 | D519 | FORMAT.md section 6.17 | D520 | 7.4 |
| D521 | 15.3 | D522 | 7.7 | D523 | notes | D524 | notes | D525 | notes | D526 | FORMAT.md section 6.15 | D527 | 7.9 | D528 | 7.9 | D529 | 14.1 | D530 | 14.1 | D531 | 14.1 | D532 | 14.1 | D533 | FORMAT.md section 11.10 | D534 | FORMAT.md section 11.10 | D535 | FORMAT.md section 11.10 | D536 | 14.1 | D537 | 14.1 | D538 | 14.1 | D539 | 14.2 | D540 | 14.2 | D541 | 14.3 | D542 | 14.4 | D543 | 14.5 | D544 | 14.6 | D545 | 14.6 | D546 | FORMAT.md section 12.1 |
| D547 | 14.7 | D548 | 14.8 | D549 | 16.1 | D550 | 16.1 | D551 | 19 | D552 | 16.2 | D553 | 16.2 | D554 | 16.2 | D555 | 16.8 | D556 | 16.8 | D557 | 16.10 | D558 | 16.10 | D559 | 13.1 | D560 | 16.12 | D561 | 16.9 | D562 | 16.23 | D563 | 16.23 | D564 | 16.15 | D565 | 16.20 | D566 | 16.21 | D567 | 16.24 | D568 | 17 | D569 | 17 | D570 | 17 | D571 | 17.1 | D572 | 17.7 |
| D573 | 17.9 | D574 | 17.9 | D575 | 17.9 | D576 | 17.9 | D577 | 17.9 | D578 | 17.14 | D579 | FORMAT.md section 12.8 | D580 | FORMAT.md section 12.8 | D581 | FORMAT.md section 12.5 | D582 | FORMAT.md section 12.5 | D583 | FORMAT.md section 12.5 | D584 | FORMAT.md section 12.2 | D585 | FORMAT.md section 12.1 | D586 | FORMAT.md section 12.6 | D587 | FORMAT.md section 12.4 | D588 | FORMAT.md section 12.4 | D589 | FORMAT.md section 12.7 | D590 | FORMAT.md section 12.4 | D591 | 18 | D592 | 18 | D593 | 20 | D594 | FORMAT.md section 2.11 | D595 | FORMAT.md section 2.11 | D596 | FORMAT.md section 2.11 | D597 | FORMAT.md section 2.11 | D598 | FORMAT.md section 2.11 |
| D599 | 11.4 | D600 | FORMAT.md section 2.11 | D601 | 11.10 | D602 | 11.10 | D603 | FORMAT.md section 1 | D604 | notes | D605 | notes | D606 | notes | D607 | notes | D608 | notes | D609 | notes | D610 | 21 | D611 | FORMAT.md section 13 | D612 | FORMAT.md section 13 | D613 | 2.7 | D614 | FORMAT.md section 4.8 | D615 | 11.9 | D616 | FORMAT.md section 12.1 | D617 | FORMAT.md section 12.7 | D618 | notes |  |  |  |  |  |  |  |  |  |  |  |  |

Decisions listed: 618. Carried by this document: 212. Carried by `FORMAT.md`:
390. Marked notes: 16. Every decision is carried by exactly one of the two
documents, or marked notes.
