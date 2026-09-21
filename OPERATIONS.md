# NoahsArk operations

**Document version 3.3.** Format major 1.

This document defines everything a NoahsArk implementation does on the host
that is not an on-disc byte. It covers the local repository and its files, the
staging state machine, the commit walker, the packer, the capacity budget, the
image build and the burn commands, verify and heal, the restore planner and
the restore procedure, the disc lifecycle, locking, the command-line
interface, the configuration keys, the exit codes and the test list.

`FORMAT.md` is the on-disc format document. It defines every byte that reaches
a disc and every rule a reader follows. This document cites `FORMAT.md` for
every on-disc structure and never redefines one. Where a local structure has
its own byte layout, such as the state log, this document carries that layout,
because those bytes never reach a disc.

A rule in this document is normative unless a paragraph or a block is marked
**informative**. An informative block describes one way to reach a normative
outcome; another way that reaches the same outcome conforms.

Section numbers have gaps. A deleted section keeps its number unused, so that
the numbers of the other sections do not move.

---

## Table of contents

1. [Scope and conventions](#1-scope-and-conventions)
2. [Repository, staging and cache](#2-repository-staging-and-cache)
3. [Local file formats](#3-local-file-formats)
4. [Staging state machine](#4-staging-state-machine)
5. [Refs and the pending snapshot chain](#5-refs-and-the-pending-snapshot-chain)
6. [Concurrency and locking](#6-concurrency-and-locking)
7. [Commit](#7-commit)
8. [Packing and locality](#8-packing-and-locality)
9. [Capacity budget](#9-capacity-budget)
10. [Disc filesystems and image building](#10-disc-filesystems-and-image-building)
11. [Burning](#11-burning)
12. [Disc lifecycle and closing](#12-disc-lifecycle-and-closing)
13. [Verify and heal](#13-verify-and-heal)
14. [Restore](#14-restore)
15. [Metadata restore policy](#15-metadata-restore-policy)
16. [CLI reference](#16-cli-reference)
17. [Configuration reference](#17-configuration-reference)
19. [Exit code registry](#19-exit-code-registry)
20. [Failure and recovery actions](#20-failure-and-recovery-actions)
22. [Test list](#22-test-list)
23. [Manual physical checklist](#23-manual-physical-checklist)
24. [Burning-host command reference](#24-burning-host-command-reference)

---

## 1. Scope and conventions

1.1 This document is normative for host behaviour. `FORMAT.md` is normative
for disc bytes. Where the two touch, `FORMAT.md` wins.

1.2 This document cites `FORMAT.md` by heading text, never by section number.
A bare "section N.M" names a section of this document.

1.3 A build must refuse an unknown command, an unknown option and an unknown
config key. It names the item and exits with code 2. It must not ignore it.

1.4 Every integer size in this document is in bytes unless the text says
sectors. A sector is 2048 bytes.

1.5 "Ascending" is an unsigned bytewise comparison, as FORMAT.md's "Canonical
ordering" defines it.

1.6 Version 1 never removes a snapshot and never frees disc space. There is no
retention and no expiry.

1.7 The design priorities are ordered. A conflict between two of them is
resolved by this order, and no other order applies.

1. Data durability. Never lose bytes.
2. Readability without the tool. A future reader must have a chance.
3. Restore usability. Fewest disc swaps, clearest plan.
4. Deduplication ratio.
5. Speed.
6. Media utilization.

1.8 Two identical discs are the primary redundancy. The operator burns each
image two times and stores the two copies in different places. FEC is optional
and off by default. A disc that does not mount counts as dead; there is no
recovery by carving.

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
| `<repo>/probes/` | Results of the manual probes, one text file each. | Informative. |

`init` creates the directory, `config` with a fresh `repo.uuid`, an empty
`staging/` with an empty state log, an empty `refs.bin`, and `lock`. A
directory is a repository when it holds a readable `config` whose `repo.uuid`
parses as a uuid.

The cache is never inside the repository, so that deleting the cache and
deleting the repository stay independent acts.

Every disc of a repository carries `repo_uuid`. A lost repository is therefore
recreated by `recover`, which takes `repo.uuid` from the
discs. Nothing in the repository directory is a source of truth except the
state log for objects that are not yet CLEAN and the local ref log for refs
whose run is not yet CLEAN.

### 2.2 Repository discovery

Discovery runs in this order. The first hit wins.

1. `--repo=PATH`.
2. The environment variable `NOAHSARK_REPO`.
3. The current directory, then each ancestor up to the filesystem root,
   nearest first. The first directory that is a repository wins.

A command that needs a repository and finds none reports it and refuses to
run. `init`, `commit`, `pack`, `gc` and `disc` treat a missing repository as
a usage error and exit with code 2. `ls`, `log`, `plan` and `restore`'s
disc-swap mode read through the local cache instead, so there a missing
repository is a failure at run time, code 1. `recover` creates the
repository directory instead of failing when it finds none. `verify` with no
repository still checks the disc tree; it only skips updating staging state.
Section 19 gives the full convention.

`init` refuses a directory that already is a repository. `init` refuses to
create a repository inside another one.

### 2.3 Staging store layout

The staging store is a local directory inside the repository. The
subdirectory names below are **informative**; an implementation may choose
others. The roles, the state log and the filesystem rule are normative.

```
staging/
    objects/ab/cd/<id>          object files waiting to be packed
    plans/<disc_uuid>/tree      the disc root that pack writes by default
    restore/<snapshot-id>/      restore spool of the disc-swap mode
    state.db                    append-only binary state log
```

`state.db` and the local ref log `<repo>/refs.bin` are the only authoritative
local state. Everything else in staging is either an object that also exists
in the source, or derived data.

Staging must be on a local filesystem, or on NFS with `sync` semantics. An SMB
or CIFS mount is not safe for staging, because the state log depends on a
durable append.

### 2.4 Local cache layout

Two rules are normative. First, everything in the cache is derived from discs
and is rebuildable: a command must behave the same, apart from speed, with the
cache deleted. Second, never put in the cache anything whose loss loses archive
data. The cache is never inside the repository.

Informative: the default location is `$XDG_CACHE_HOME/noahsark/<repo-uuid>/`,
which falls back to `~/.cache/noahsark/<repo-uuid>/`. `cache.dir` overrides it.
The layout below is one conforming layout.

| Item | Content |
|---|---|
| `discs/<disc-uuid>/INDEX.bin`, `REFS.bin`, `DISCS.bin` | Byte copies of the index and the catalog of the disc with this uuid. One disc holds one run, thus the disc uuid identifies the run too. The key is the disc uuid, never `run_seq`: the host assigns `run_seq` from local state, and after a lost repository two discs can carry the same number. `pack` writes the three files for the disc it packs. `recover` writes them from a disc. FORMAT.md's "The run index and the catalog" defines the three files. |
| `snapshots/<id>` | A byte copy of every cached snapshot object. |
| `trees/<id>`, `blobs/<id>` | A byte copy of every tree object and every blob object reachable from a cached snapshot: from staging when `pack` writes it, from the objects of a disc when `recover` writes it. |
| `state.txt` | Per snapshot id, whether its tree set is complete in the cache. |

A cache whose `cache.format_version` differs from the build is deleted and
rebuilt. It is never migrated.

A disc carries no measured health record of itself. The DISCS row that a run
writes for its own disc holds `health` 6, `unverified, this disc`, as
FORMAT.md's "DISCS" defines it. A reader reports such a disc as not yet
verified. It must not report it as healthy.

### 2.7 Cache-less operation

Every command must work with the cache absent, after `recover`
or with the disc roots given on the command line.

| Command | Behaviour with no cache |
|---|---|
| `commit` | Works. With no cached INDEX, no exact lookup is possible, thus `commit` stages each chunk again. |
| `pack` | Works. It packs the staged objects. |
| `plan`, `restore --mount` | Need the cache. The operator runs `recover` first. |
| `restore` with disc roots | Works from the given discs alone. It needs no repository. |
| `verify` | Works from the disc root alone. |
| `ls`, `log` | With a disc root, read the discs. With none, need the cache. |

CI must include a test that deletes the cache and restores successfully from
the disc images alone. That is the "cache-less restore" test of section 22.

### 2.8 Data flows

**Write.**

```
 source -> commit -> staging -> pack -> disc root -> image build
        -> burn (operator) -> disc burned -> verify -> clean -> GC
```

`commit` chunks, hashes, deduplicates, compresses and writes objects into
staging as STAGED. `pack` selects the objects for the next run, writes the disc
root and the parity when FEC is on, prints the next commands, and moves the
objects to PACKED. `image build` makes the UDF image. The operator burns the
image with the printed `growisofs` line, then runs `disc burned`, and the
objects move to BURNED. The operator mounts the disc. `verify` reads every
object back and compares, and the objects move to CLEAN. The retention timer
then starts, and `gc` may free the staged files once it has passed. `gc` frees
the staged file of a CLEAN object only.

**Restore.**

```
 snapshot id -> object set -> run map from INDEX -> disc plan
   -> for each disc in plan order:
        detect disc -> read needed objects -> staging/restore/
        -> assemble every file that is now complete -> free its staging space
   -> apply metadata in the fixed order -> deferred directory times
   -> warnings -> exit code
```

**Heal.** Section 13.4 gives the heal order.

---

## 3. Local file formats

Every structure in this section lives on the host. None of its bytes reaches a
disc. Every integer is little-endian, and every CRC is CRC-32C, with the
parameters of FORMAT.md's "The format rules".

### 3.1 Log container header

The local ref log carries this header. The state log carries no header:
it is records alone, from byte 0.

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

`<staging>/state.db` holds the staging state log. It is append-only. It has
no header. It is a whole number of fixed-width records, each 79 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u64 | `sequence` | Monotonic record number. It starts at 1. |
| 8 | 32 | u8[32] | `content_id` | The object. |
| 40 | 1 | u8 | `state` | 1 STAGED, 2 PACKED, 3 BURNED, 4 CLEAN, 5 ON-DISC. |
| 41 | 8 | u64 | `run_seq` | The run, when the state is PACKED or later. 0 otherwise. It is a label; the disc uuid is the key. |
| 49 | 16 | u8[16] | `disc_uuid` | The disc, when known. All zero otherwise. |
| 65 | 1 | u8 | `reason` | 0 normal, 1 burn failed, 2 verify failed, 3 healed, 4 duplicate for locality. |
| 66 | 1 | u8 | `verify_count` | Successful verifies of the object. It stops at 255. |
| 67 | 8 | i64 | `clean_sec` | Unix time of the **first** successful verify. 0 before that verify. Every later record carries the value forward. |
| 75 | 4 | u32 | `record_crc32c` | CRC-32C over bytes 0 to 74. |

The record holds no object kind, length or transition time. The run index
holds the kind and the length. The burn time is not recorded: nothing reads
it back.

### 3.3 Local ref log record

**The local ref log.** `<repo>/refs.bin` is an append-only log with the
container header of section 3.1, magic `"NALR"`, `record_size` 128,
and these records:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 96 | ref record | `ref` | The ref record of FORMAT.md's "Ref", unchanged. `run_seq` is 0 while no run holds the snapshot; it is the run's `run_seq` from the moment `pack` puts the snapshot object into a run. |
| 96 | 8 | u64 | `sequence` | Monotonic record number. |
| 104 | 8 | u64 | `snapshot_generation` | `generation` of the snapshot named by `ref`. For a fast ancestry check. |
| 112 | 12 | u8[12] | `reserved` | Zero. |
| 124 | 4 | u32 | `record_crc32c` | CRC-32C over bytes 0 to 123. |

Section 5.1 gives the resolution rules. A
record with `run_seq` 0 exists only here, never on a disc.

### 3.5 Cache index

A build may keep a merged index over the Objects tables of every cached
`INDEX.bin`, with the `run_seq` added to each row.

The merged index is derived data. A reader never treats it as authoritative.
A local index is an accelerator; the discs answer every question without it.

---

## 4. Staging state machine

### 4.1 States and transitions

The object states are STAGED, PACKED, BURNED, CLEAN and ON-DISC.

```
        commit
          |
          v
     +---------+   pack    +--------+   disc burned   +--------+
     | STAGED  |---------->| PACKED |---------------->| BURNED |
     +---------+           +--------+                 +--------+
                               ^                          |
                               |  disc burned --undo,     |
                               |  or verify failed        |
                               +--------------------------+
                                                          |
                                     verify ok, on the    |
                                     disc that the        |
                                     operator mounted     v
                                                     +--------+
                                                     | CLEAN  |
                                                     +--------+
                                                          |
                                        gc, once the        |
                                        retention timer     |
                                        has passed          |
                         (staging.retain_after_clean, default 7 days)
                                                          v
                                                  +---------------+
                                                  |    ON-DISC    |
                                                  +---------------+
```

ON-DISC means a disc holds the object and staging holds no file for it. It
is the last state. `gc` records it before it unlinks the staged file.
`recover` records it for every object it reads from a disc's own
catalog, because such an object was never staged here.

There are exactly two arrows back, and both go from BURNED to PACKED: a failed
`verify`, and `disc burned --undo`. The packed disc root and the image stay on
the local disk, thus the operator burns the same image again.

CLEAN has one arrow to itself: a `verify` of an object that is already CLEAN.
It adds 1 to that object's verify count. The operator verifies the second copy
this way, and `gc` frees an object only at `gc.min_verified_copies` verifies.

### 4.2 Transition rules

1. `verify` is the only transition from BURNED to CLEAN. There is no timer and
   no manual override. An object stays in staging until the disc that holds it
   has been read back and checked.
2. Each object counts its successful verifies. A successful `verify` of a
   BURNED object moves it to CLEAN and sets the count to 1. A successful
   `verify` of an object that is already CLEAN keeps it CLEAN and adds 1 to
   the count. The count stops at 255. The ON-DISC record carries the count
   forward.
3. The clean time is the time of the first successful verify. A later verify
   does not change it. Thus the retention period counts from the first verify.
4. The two identical copies of a disc carry the same disc uuid. The tool
   cannot tell one copy from the other. The count counts successful verify
   passes, not distinct physical discs.
5. The tool never concludes by itself that a burn occurred. `disc burned` is
   the only transition from PACKED to BURNED. The operator runs it after the
   burn. `verify` never moves a PACKED object: a disc root that passes
   `verify` can be an image that no one burned.
6. `gc` frees the staged file of a CLEAN object only. It also frees an
   orphan: a staged file whose object is already ON-DISC.
7. GC is a separate command. The operator runs it manually.
8. `disc burned --undo` moves the BURNED objects of a disc back to PACKED,
   with reason 1, after a bad burn. It refuses a disc that has a CLEAN object.
9. A run that fails verify moves its objects from BURNED back to PACKED, with
   reason 2. The writer appends one PACKED record with reason 2 per object of
   that run. A failed verify leaves a CLEAN object alone and does not change
   its count. The disc root and the image of the run stay on the local disk,
   thus the operator burns the same image on a new disc and verifies it.
10. `commit` is the only entry point. A new object enters the machine at
    STAGED. `recover` records the objects of a fed disc as ON-DISC,
    and leaves an object the log already knows at its own state. An ON-DISC
    object needs no `disc burned` and no `verify`: the burn already
    happened, and staging holds nothing to protect.
11. The state log is authoritative only for objects that are not yet CLEAN.
    Everything about a CLEAN or ON-DISC object is derivable from the discs.
12. There is no transition from PACKED back to STAGED. `verify --heal` repairs
    a disc root and writes no staged object.

### 4.3 State log replay

The current state of an object is the newest record for that id, by
`sequence`. A reader replays the log from the start.

A writer may compact the log by rewriting it with only the newest record per
id, but only after every CLEAN object has been dropped.

There is one torn-tail rule, and it runs at open. A partial record at the end
of the file, or a last record with a bad CRC, is what a crash during an
append leaves. The tool cuts the file back to the last good record, prints
one warning, and goes on. The next append then lands where a replay can
reach it.

A bad record anywhere else is damage, not a torn tail, because good records
follow it. The tool reports an error, names the record, changes no byte of
the file, and stops. It never drops the good records behind the damage
without saying so.

Each append to the state log reports an error from the write or from the
close of the log file. The command then stops and does not act on that
record.

### 4.4 What a partial `pack` leaves behind

`pack` writes local files only. It touches no disc, so an interrupted `pack`
can never leave a partial run on a medium.

`pack` records an object as PACKED, and saves the disc and ref ledgers, only
after the run tree is complete and synced (section 8.8). An interrupted `pack`
therefore leaves one thing: a part-written disc root under `--out`. It leaves
no PACKED record, and the run and disc sequence numbers stay free. The object
files in staging are unchanged, because `pack` never moves or rewrites one.

The next `pack` refuses an `--out` directory that holds files. The operator
deletes the part-written directory and runs `pack` again.

### 4.5 GC rules

1. `gc` frees the staged file of a CLEAN object only.
2. `gc` frees it only after `staging.retain_after_clean` has
   passed since it first reached CLEAN.
3. `gc` frees it only when its verify count is at least
   `gc.min_verified_copies`, default 2. Two identical discs are the
   redundancy, thus `gc` holds the staged data until the second copy passes
   `verify`. `--force-after` shortens the retention period only. It never
   passes by this count. An operator who keeps one copy only sets
   `gc.min_verified_copies = 1`.
4. `gc` must confirm, before it frees a CLEAN object, that the object is
   present in at least one run whose verification passed. The confirmation is
   an exact lookup in the cached `INDEX.bin` of that run, found by the disc
   uuid of the object's own record. `gc` leaves an object alone, and reports
   it, when the cache does not hold that INDEX. An orphan needs no
   confirmation: its own ON-DISC record already carries the answer.
5. `gc --dry-run` prints what it would delete and how many bytes it would free.
6. `gc` writes the ON-DISC record and flushes it to the disk before it
   unlinks the staged file. A flush error stops `gc` before the unlink, so a
   crash can never take the record away and leave the staged file gone. A
   crash between the two leaves an orphan: a staged file whose object is
   already ON-DISC. The next `gc` run unlinks such an orphan. `gc` never
   unlinks before the record is durable.
7. `gc` never trims the local cache.
8. `gc` prints one line for each disc that holds objects back, in the dry run
   and in the real run:
   `gc: disc UUID: C of N copies verified; K object(s) held; verify the second copy`.

---

## 5. Refs and the pending snapshot chain

### 5.1 Ref resolution

1. `commit` appends one record per moved ref, with `run_seq` 0, after it has
   written the snapshot object into staging and recorded it STAGED.
2. `pack` carries forward, unchanged, the current record for every ref name
   the repository already knows, keeping each record's own `run_seq`. It also
   copies every local record whose `run_seq` is 0, and whose snapshot object
   it puts into the run, with `run_seq` set to the run, and appends the same
   record to the local log. The run's `refs.bin` holds the union of both
   groups, one record per name, so a reader with only the newest disc still
   finds every ref FORMAT.md's run index and catalog section promises.
3. The current value of a ref resolves in the order local log, then cache, then
   discs. Inside the local log the newest record is the highest `sequence`.
   Inside an on-disc `refs.bin`, or the cache's copy of it, the newest record
   is the one FORMAT.md's "Ref" names. A lookup applies exactly one of the
   two orders, chosen by the file it is reading.
4. A local record whose `run_seq` names a run that has reached CLEAN is
   derivable from the discs, and a writer may drop it when it compacts the log.
   A record with `run_seq` 0, or one naming a run that is not yet CLEAN, is
   authoritative and is never dropped.
5. A record with a bad CRC ends the replay, as in section 4.3.
6. `recover` on a recreated repository supplies every ref
   from the discs.

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

---

## 6. Concurrency and locking

One repository is used by one process at a time for every command that writes
local state. The rules below are the requirement. The system calls named in
them are **informative**; they are the Linux way to meet the rule.

1. **Repository lock.** `<repo>/lock` is the lock file. A command that writes
   the state log, the staging store, the ledgers or the config takes a
   non-blocking exclusive advisory lock on it before it reads the state log,
   and holds it until it exits. Those commands are `init`, `commit`, `pack`,
   `gc`, `disc burned`, `recover`, the disc-swap mode of `restore`, and
   `verify` when it has a repository.
2. A read-only command takes no lock: `plan`, `ls`, `log`, `status`,
   `verify` with no repository, `restore` with disc roots, and `image build`.
3. A command that cannot get its lock fails at once: it exits with code 1,
   with a message that names the lock file and says that another noahsark
   command runs on this repository. It never waits.
4. There is no separate cache lock and no drive lock. The repository lock
   guards the cache, and the tool opens no drive for writing.
5. Locks are per repository.
6. The state log is appended under the exclusive lock only. A read-only
   command that opens the state log while a write is in progress replays it
   to the last valid record and ignores a partial tail, the same as any
   other torn tail, but it never truncates the file: only the exclusive
   holder of a repository ever writes to `state.db`, so a read-only open
   must leave a tail it cannot yet tell from a crash exactly as it found it.

The lock is advisory. It is not a security boundary.

---

## 7. Commit

### 7.1 Commit flow

```
1. Resolve the source root and the parent snapshot. The parent is the
   current value of the ref being moved, resolved in this order: the local
   ref log, then the cache's copy of REFS, then the REFS of the given discs
   (section 5.1). No parent means a root snapshot.
2. Build the candidate file list with the quick check of section 7.2 against
   the parent snapshot's tree entry.
3. For every candidate file: chunk it with the configured profile; hash every
   chunk; look up the chunk id in staging and in the INDEX Objects tables of
   the cache; compress and write every genuinely new chunk into staging;
   build the blob object that lists the chunks of the file.
4. For every directory, bottom-up: build the tree entries sorted by name
   bytes; build the TLV areas sorted by type; hash and write the tree object.
5. Compare the new root tree id with the parent's root tree id. When they
   are equal, stop here: write no snapshot object, move no ref, and report
   "no change" with exit code 0. Otherwise write the snapshot object with
   the root tree, the parent, the generation, and the metadata TLVs.
6. Record every new object in the state log as STAGED.
7. Append a ref record with run_seq 0 to the local ref log for the ref
   being moved, by default LATEST (section 5.1).
```

The shape of every object the flow writes is in FORMAT.md's "Objects". Only an
exact INDEX lookup permits dropping chunk data, as FORMAT.md's "Dedup rule"
states.

`commit` is idempotent. Running it twice on an unchanged source writes no
second snapshot object.

NoahsArk has no built-in scheduler and no daemon. `commit` is a batch job that
the operator or an external scheduler runs, and the exclusive repository lock
of section 6 keeps two commits from running at once.

### 7.2 The quick check

`commit` never writes to a source root. A source is read strictly read-only.
Every byte that NoahsArk creates goes into staging.

The walker compares these fields with the parent snapshot's tree entry.

| Field | Compared as |
|---|---|
| Size | Exact `u64` equality. |
| mtime | Seconds and nanoseconds, exact equality. |
| ctime | Seconds and nanoseconds, exact equality. |

If all compared fields are equal, the file is unchanged. The walker reuses the
entry's content reference and never opens the file. If any one differs, the
file is read, chunked and hashed again.

### 7.3 Direct mode

The source is a local path. `commit` reads it directly.

```
1. Walk the source. For each path, stat it.
2. Compare size, mtime and ctime with the parent tree entry.
3. Read and chunk only the files that differ, or that are new.
4. Reuse the parent entry, unchanged, for every file that matches.
5. A path in the parent tree that the walk did not see is a deletion.
6. Write only new chunks into staging.
```

The source is opened read-only. Nothing is copied first.

### 7.5 Source policy

| Rule | Default |
|---|---|
| Source roots | One source root for each commit. |
| Sources | Read-only, never written. |
| Symlinks | Never followed. The link itself is stored. |
| Special files | Recorded by type, with no content. |
| Unreadable file | Skipped and reported, exit code 1. |

The shape of the synthetic root tree and the root name encoding are in
FORMAT.md's "The root tree". `restore SNAPSHOT TARGET` creates
`TARGET/<root path>` for each root.

An intermediate directory between `TARGET` and a source root that no tree entry
describes is created with mode 0700, the invoking user's uid and gid, and the
restore time as mtime. An intermediate directory that already exists is left as
it is.

### 7.6 In-flight change detection

```
1. stat the file           -> (size_a, mtime_a, ctime_a)
2. read and chunk it
3. stat the file again     -> (size_b, mtime_b, ctime_b)
4. if size or mtime differ:
       mark the path "unstable"
       apply the branch rule of FORMAT.md's "Entry flags", which chooses
             between reusing the parent entry and storing the new
             content with the UNSTABLE flag set
       list the path in the commit report
       retry on the next commit
```

FORMAT.md's "Entry flags" owns the branch rule and the tree bytes it produces.
This section owns the detection, the retries and the report.

`commit` exits with code 1 in both branches, and prints the count and the paths.
The report says which branch was taken: `parent` when the parent entry was
reused, `flagged` when new content was stored with the `UNSTABLE` flag.

Chunks already written to staging are kept in either branch. They are
content-addressed, so they cost nothing if the file settles.

`commit.restat_after_read` selects this detection. It must never be set false
on a live source.

`commit.retry_unstable` sets how many times an unstable file is re-read before
the rule applies.

### 7.7 Filesystem snapshots as the source

Committing a read-only filesystem snapshot (btrfs, LVM or ZFS) removes
in-flight changes entirely. The quick check still works, because a filesystem
snapshot preserves size, mtime and ctime. The snapshot records the path that
`commit` read. The planned `--source-root` option of section 7.10 records the
original path instead.

### 7.10 Planned, not built

These options are design text. The build does not have them, and it refuses
each one as an unknown option. They are the only planned `commit` options.

- `commit --exclude=PATTERN`, repeatable, the config key `sources.exclude`,
  repeatable, and a per-directory exclude file, named by `sources.ignore_file`
  with the default `.noahsarkignore`. The pattern language and the order of
  the three rule sources are in FORMAT.md's "Exclude pattern language". An
  excluded path is not in the tree at all. The exclude rules are stored in the
  snapshot as a TLV, so a later `ls` can explain why a file is absent.
- `commit --one-file-system`, and the config key `sources.one_file_system`
  with the default true. The walker does not cross a mount point.
- `commit --checksum`, and the config key `commit.checksum`. It disables the
  quick check: `commit` reads and hashes every file again.
- `commit --source-root=PATH`. It records `PATH` in the snapshot as the root
  path, in place of the path that `commit` read. With a filesystem snapshot
  mounted at a temporary path, a restore then writes to the original path.

---

## 8. Packing and locality

### 8.1 Packing rules

The rules are ordered. A conflict is resolved by this order.

1. **A file's chunks go in one run.** The only exception is a file that does
   not fit in the space that remains: its chunks continue on the next run.
2. **A directory's files go in one run** when the directory fits, in
   depth-first path order.
3. **Siblings stay adjacent.** Objects are written in depth-first path order
   inside a run. FORMAT.md's "Fill order inside a run" states that order in
   full and makes it total.
4. **Split only when forced.** Split at the tail: fill the current run,
   continue on the next.
5. **Metadata is written first inside a run**, contiguous, and the catalog is
   replicated on every run.

`pack` fills exactly one run per invocation, and it starts a new disc for each
run. A staged set larger than one run leaves the remainder STAGED for the next
invocation. A multi-disc pack is repeated invocations, never one invocation
that spans discs.

`pack` always packs from the full STAGED pool, across every snapshot of the
repository.

Informative: the reference packer walks the tree of every snapshot in
post-order, children before the parent, and keeps the STAGED objects of that
order. It takes the longest prefix that passes the capacity check of section
9.3. A prefix is always dependency-closed: a direct child that the prefix does
not hold is already PACKED on an earlier run, and `pack` records that run in
the Prereqs table of INDEX.

### 8.5 Capacity budget

The packer must fit a run inside the capacity that `--capacity` gives. Section
9.3 gives the formula. `pack` refuses, with exit code 2, a capacity too small
for one run.

### 8.8 Integrity checks and durable recording

`pack` checks the content id of every staged object it selects, so a corrupt
staged file cannot reach a disc. It checks a chunk while it copies the
chunk's bytes into the run tree.

When a chunk fails this check, `pack` removes the part-written run tree
under `--out`. It records no object as PACKED, saves no ledger, and uses no
run or disc sequence number for that attempt. The operator runs `commit`
again to rewrite the corrupt staged object, then runs `pack` again.

`pack` syncs every file and every directory of the run tree it wrote, in one
pass, once the write is complete. It marks the run's objects PACKED and
saves the disc and ref ledgers only after that sync pass succeeds, so the
state log never claims a run the local disk does not hold. A sync error is a
failure at run time: `pack` records nothing, and the run and disc sequence
numbers stay free for the next `pack`.

### 8.9 Planned, not built

This option is design text. The build does not have it, and it refuses it as
an unknown option. It is the only planned `pack` option.

- `pack --dry-run`. It selects the objects and prints the capacity budget, the
  objects and bytes of the run, and the objects and bytes that stay STAGED. It
  writes no disc root and no state record, and it uses no sequence number. It
  answers the question "how many discs does the staged data need?".

---

## 9. Capacity budget

FORMAT.md's "Capacity invariants" carries the capacity names and the
invariants that every writer must hold. This section is the budget a writer
uses to choose the numbers.

### 9.1 Fill policy

- The operator gives the capacity, on the command line with `pack --capacity`
  or once in the config with `pack.capacity`. `pack` reads
  no drive.
- `--capacity` takes a preset name of section 9.2, or a byte
  size. The preset table is normative.
- `--physical-capacity` gives the capacity of the medium when `--capacity`
  forces a smaller limit (section 12.6).
- One growisofs command line covers every media size.

### 9.2 Media capacity table

FORMAT.md's "Registries" carries the media type registry. The table below is
normative for the `--capacity` presets.

| Preset | Media | Sectors | Bytes |
|---|---|---:|---:|
| `dvd+r` | DVD+R 4.7 GB | 2,295,104 | 4,700,372,992 |
| `dvd-r` | DVD-R 4.7 GB | 2,298,496 | 4,707,319,808 |
| `bd25` | BD-R / BD-RE SL 25 GB | 12,219,392 | 25,025,314,816 |
| `bd50` | BD-R / BD-RE DL 50 GB | 24,438,784 | 50,050,629,632 |
| `bd100` | BD-R XL / BD-RE XL TL 100 GB | 48,878,592 | 100,103,356,416 |
| `bd128` | BD-R XL QL 128 GB | 62,500,864 | 128,001,769,472 |

Notes:

- A file image takes the size that `--capacity` gives it.
- QL 128 GB exists as BD-R XL only. BD-RE XL stops at 100 GB.
- M-DISC BD is sold as SL 25 GB and DL 50 GB, with the same sector counts.
- A drive can report fewer sectors than the preset. The operator checks the
  blank disc with `dvd+rw-mediainfo` (section 11.8) and gives the smaller
  sector count as `--capacity`.
- Never copy dvdisaster's hardcoded BD sizes (11,826,176 and 23,652,352
  sectors). They are smaller than the real discs and would waste about 3 percent
  of every disc.

### 9.3 The budget formula

A run fits when this holds, in bytes:

```
stream_bytes + run_header_copies + checksum_bytes + parity_bytes
    + filesystem_overhead  <=  capacity_sectors * 2048
```

`checksum_bytes` and `parity_bytes` are zero when `fec.scheme` is `none`, which
is the default. With `rs255-gf8` they follow FORMAT.md's "Parity layout".
`filesystem_overhead` is an estimate of the UDF metadata: a fixed base, two
blocks for each file, two blocks for each directory, and a margin of 0.1
percent of the capacity. The estimate is a heuristic. It changes no disc byte.

---

## 10. Disc filesystems and image building

FORMAT.md's "Profiles a reader must know" names the filesystem profiles. The
build writes profile 0 only: one run on one UDF disc. This section is the
writer's side: how the image is built and burned.

### 10.1 Profile 0 image build

FORMAT.md's "Profiles a reader must know" is the normative home of the profile
0 volume: the filesystem and its revision, the `mkudffs` options
`--media-type=hd`, `--blocksize=2048`, `--udfrev=2.01`, `--uid=0`, `--gid=0`,
`--mode=0555` and `--bootarea=erase`, the absence of a sparing table, the label
from the writer, the image length, the anchor positions and the used prefix.
This section adds only the host's side: the option order with `--utf8` first
and every override after `--media-type`, and the checks below. Any build that
reaches those bytes conforms, whatever tool or script produces it.

- Never use `--media-type=bdr` or `dvdr`. Both make a write-once VAT volume,
  which cannot be populated.
- The exact UDF metadata bytes depend on the `mkudffs` version, so `image
  build` checks that version first (section 11.9).

`image build` runs `mkudffs`, loop-mounts the image file, copies the disc root
into it, and unmounts. The loop mount needs root. This is the one mount that
the tool does: it mounts an image file, never a device. `image build` never
runs `sudo`. When it is not root, it removes the partial image and prints the
exact `sudo noahsark image build ...` line to run.

The script below is **informative**.

```bash
BLOCKS=12219392                        # the --capacity value, in sectors
BLOCKS=$(( BLOCKS - BLOCKS % 16 ))     # 32 KiB alignment
truncate -s $(( BLOCKS * 2048 )) run.udf
mkudffs --utf8 --media-type=hd --blocksize=2048 --udfrev=2.01 \
        --label=NOAHSARK-0001 --uid=0 --gid=0 --mode=0555 \
        --bootarea=erase run.udf
```

### 10.2 Profile 0 burn paths

The burn is one growisofs call, which the operator runs. `pack` prints the
line. Two variants exist. Section 12.2 states the close policy that selects
between them.

**Default: POW-formatted and left open.**

```bash
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=run.bin
```

- `spare:min` formats the blank BD-R for Pseudo-OverWrite with the
  maximum-capacity descriptor, so the spare area is as small as the drive
  allows.
- `-dvd-compat` is not passed. The disc stays open.
- NoahsArk never writes a UDF anchor itself. Anchors are `mkudffs`'s business.

**Sealed: `pack --close`.**

```bash
growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:none,tty \
          -Z /dev/sr0=run.bin
```

- `spare:none` skips the format step entirely. There is no spare area and no
  defect management, capacity is full, and LBAs are stable forever.
- `-dvd-compat` closes the disc. The choice is permanent.
- `run.bin` is the full-size image, so the same call writes the used prefix,
  the zero middle and the tail anchors.

Informative: `-speed=4` above is an example. Use speed 2 for M-DISC.

A drive that offers no POW feature, or that reports plain `BD-R SRM` after a
format attempt, forces the sealed variant.

### 10.4 Placement order

The writer copies files into the loop-mounted image one at a time,
single-threaded, in fill order. Copy order equals physical LBA order.

The writer must never use `cp -r` on a directory. The order would then follow
`readdir`, not the fill order.

FORMAT.md's "Fill order inside a run" gives the fill order itself.

---

## 11. Burning

### 11.1 Burning is externalized

The program does not burn. It has no `burn` command and writes no burn plan.

| Step | Owner | Command |
|---|---|---|
| Write the disc root of the run | The program | `noahsark pack` |
| Build the UDF image | The program, as root | `noahsark image build` |
| Burn the image | The operator | the `growisofs` line that `pack` prints |
| Record that the burn occurred | The operator | `noahsark disc burned` |
| Mount the disc | The operator | `mount` |
| Read the disc back and check every object | The program | `noahsark verify` |

`pack` prints the exact `image build`, `growisofs`, `disc burned` and `verify`
command lines for the run. The operator runs them. The printed burn command
leaves the disc open. Only `pack --close` prints the sealed burn command.

The tool never concludes by itself that a burn occurred. `disc burned` is the
only transition from PACKED to BURNED.

### 11.8 Probe commands

The operator checks the blank disc before the burn.

```bash
dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Number of Sessions|Next Writable Address|Free Blocks|Track Size'
```

`BD-R SRM+POW` means the disc is formatted for Pseudo-OverWrite. `BD-R SRM`
means it is not.

Common rules for every burn:

- Never use `-overburn`.
- Never let a drive use BD-R Random Recording Mode.
- Never pass `-M` on a UDF disc.
- Eject and reload before the verification read, so the read comes from the
  medium and not from a cache.

### 11.9 Tool version check

| Tool | Package | Version requirement |
|---|---|---|
| `growisofs` | dvd+rw-tools | Debian 7.1-14 or newer, Fedora 7.1-13 or newer, Arch 7.1-13 |
| `mkudffs` | udftools | 2.3 or newer |

`image build` checks the `mkudffs` version before it builds. The operator
checks the `growisofs` version before the first burn. Section 24 gives the
commands.

xorriso is not used.

---

## 12. Disc lifecycle and closing

### 12.1 Lifecycle states

FORMAT.md's "How a lifecycle state is recorded" states where each state is
recorded. The build uses these states.

| State | Meaning | Entered by |
|---|---|---|
| `blank` | The medium as it comes from the manufacturer. No NoahsArk state exists. | The disc's manufacturing. |
| `open` | The disc holds a filesystem and one run, and is not sealed. | The default burn of section 10.2. |
| `sealed` | The disc is closed: no later write is possible. Permanent. | The sealed burn that `pack --close` prints. `--close` changes only the printed burn command; the repository does not track the close state. |

A disc is good or is discarded. A disc that fails `verify` is discarded, and
the operator burns the same image on a new disc.

`run_seq` and `disc_seq` are labels for the human. The host assigns them from
local state, thus after a lost repository two discs can carry the same number.
The tool finds a disc by its uuid. One disc holds one run, thus the disc uuid
identifies the run too.

### 12.2 Close policy

A disc is never closed by default. There is no config key for the close
policy. Only an explicit `pack --close` seals a disc.

**The default.** The disc is formatted for POW with `spare:min`, one run is
written, `-dvd-compat` is not passed, and the disc is left open.

**Sealing a disc.** `pack --close` selects `spare:none` and `-dvd-compat`
instead: no format step, no spare area, no defect management, full capacity,
permanently stable LBAs. The choice is permanent.

The format must not depend on a closed disc. Every reader path works on an
open disc.

Section 10.2 gives the two command lines.

### 12.6 Forced capacity

A user may cap the usable capacity of one disc below the capacity of the
medium. The CLI option is `pack --capacity`. The config key `pack.capacity`
holds the default for a repository that always uses one medium.

`pack` reads no drive, thus `pack --physical-capacity` gives the capacity of
the medium. It defaults to the `--capacity` value. A `--capacity` value below
`--physical-capacity` is a forced capacity: `pack` records it as forced in
the superblock and in the DISC table. `pack` refuses a `--capacity` above
`--physical-capacity`.

FORMAT.md's "Forced capacity" gives the superblock fields. The rules on the
host side are:

1. The forced value must be at or below the physical capacity. A larger value
   is a hard error.
2. Everything that consumes capacity uses the forced value: the packer, when
   it decides how much fits; the image size, which is the length the image
   file is truncated to before `mkudffs`; and the FEC layout, when FEC is on.
3. `status --json` shows the capacity of each disc.

---

## 13. Verify and heal

### 13.2 Verify procedure

`verify` reads a disc root: the mount point of a disc, the mount point of a
loop-mounted image, or a packed disc root. The operator mounts the disc. The
tool never mounts a device.

`verify` reads the run header, `INDEX.bin` and every object through the
filesystem. It checks every file that INDEX lists against its recorded hash,
and every object against its content id.

A disc that does not mount fails verify. There is no scan of the raw medium
and no recovery by carving. The operator discards the disc and burns the same
image on a new disc.

On a successful verify the BURNED objects of the run move to CLEAN, and each
one gets verify count 1. An object of the run that is already CLEAN stays
CLEAN, and its count goes up by 1. `verify` prints the count of the run, for
example `verify: copy 1 of 2 verified; verify the second copy before gc`.

The operator verifies both copies. The second verify is not optional: `gc`
frees the staged data only at `gc.min_verified_copies` verifies.

Recommendation: eject and reload the disc before the verify, and verify the
second copy in a different drive when one is available.

### 13.4 Heal order

The operator tries the sources in this order.

1. **The second identical disc.** It is the primary redundancy. Restore from
   copy B, and burn a new copy from it or from the kept image.
2. **On-disc RS parity**, when the run has FEC. `verify --heal --out=DIR`
   corrects the erasures of a copy of the disc root, then checks the sector
   digests and the object content ids again. `--heal` refuses a run that has
   no FEC.
3. **Another run that holds the same content id.** `restore` with all the
   discs finds it through INDEX.
4. **The original source path**, if it still exists. A new `commit` in a new
   repository stores it again.
5. **Give up.** `restore` names each object that it cannot find.

---

## 14. Restore

### 14.1 The planner

Restore planning is minimum set cover. The planner works in discs, not runs:
every run of a disc is available once the disc is in the drive, and run order
does not imply disc order.

**Inputs.** The planner takes the object set of the snapshot, the INDEX
Objects tables that say which runs hold each object, and the DISCS table,
which maps every `run_seq` to its `disc_seq` and `disc_uuid`. It takes them
from the cache, or from the given discs. There is no membership filter:
every lookup is an exact lookup in INDEX, as FORMAT.md's "Proof of absence and
coverage" states.

The planner drops every run whose `run_status` is 3 before it starts.

**Requirements.** Three things are normative: the plan accounts for every
needed object or fails up front; the plan is deterministic, so the same inputs
give the same plan; and the tie-breaks below are applied in the stated order.

**Coverage.** An object whose content id is absent from the Objects table of
every run is missing, and that is a proof. A plan with any missing object
fails up front. When the cache does not hold the INDEX of a run, the planner
says so and names `recover` as the fix.

**Algorithm.** The two steps below are **informative**. Any algorithm that
meets the requirements conforms.

```
Step 1: unique-element reduction. If every run that holds an object lies on
  one disc, that disc is in every valid plan. Add all such discs and remove
  every object they cover.
Step 2: greedy on the residual. P = mandatory_discs(N); U = N minus
  covered(P); while U is not empty, pick the disc d that covers the most
  bytes of U, add d to P, and remove S_d from U; return order(P).
```

**Tie-breaks**, applied in this fixed order:

1. Most remaining bytes covered.
2. Newer disc.
3. Lower `disc_seq`.

### 14.2 Disc-major order

```
for each disc in plan order:
    detect the disc
    read every needed object from it in one pass
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

The plan is one JSON object with these fields.

| Field | Type | Meaning |
|---|---|---|
| `format` | string | Always `noahsark-restore-plan`. |
| `version` | integer | 1. |
| `repo_uuid` | string | Hyphenated lowercase uuid. |
| `snapshot` | string | Multihash text form of the snapshot. |
| `include` | array of strings | The `--include` paths, absent when none. |
| `files`, `objects` | integer | Files and distinct objects to restore. |
| `bytes` | integer | Uncompressed bytes to restore. |
| `peak_staging_bytes` | integer | Predicted peak of `staging/restore/`. |
| `switches` | integer | Number of disc changes. |
| `passes` | integer | 1, or more when the staging budget forces several passes. |
| `discs` | array of objects | In plan order. |
| `discs[].order`, `discs[].pass` | integer | 0-based position in the plan, and 0-based pass. |
| `discs[].disc_uuid` | string | The disc. |
| `discs[].disc_seq` | integer | 0-based. |
| `discs[].label` | string | On-disc label. |
| `discs[].bytes_to_read`, `discs[].objects_to_read` | integer | Counts. |
| `missing_discs` | array of objects | `disc_uuid`, `disc_seq`, `label`, `objects` (integer). Non-empty means the plan failed. |

A field may be added later. A reader ignores a field it does not know.

### 14.6 Disc detection

The operator mounts each disc at the `--mount` directory. The tool never
mounts a device.

1. Read `/NOAHSARK/DISC.bin` below the `--mount` directory and compare the
   `disc_uuid`.
2. The filesystem label is a hint for a human only.
3. An unreadable `DISC.bin` means that the drive still settles or that nothing
   is mounted yet. Retry the read a few times with a short pause, then prompt.
4. Eject after each disc. `--no-eject` controls this.

Do not prompt when the expected disc is detected. Print one line and continue.

Prompt only when the wrong disc is inserted, when the disc is unreadable, or
when `--interactive` is set.

### 14.7 Restore pipeline

```
snapshot id
 -> object set: read the snapshot, trees and blobs (metadata only),
      from the cache or from the given discs
 -> run map: the INDEX Objects tables give the exact run of each object
 -> disc plan: unique-element reduction, greedy, tie-breaks; print and
      persist the plan; fail on a missing disc
 -> for each disc:
      detect -> read needed objects into staging/restore/
      -> verify each object's content id (a mismatch fails that file)
      -> assemble every file that is complete
      -> create, write, chown, chmod, times
      -> free the staging space of that file -> eject
 -> deferred pass: directory times, in reverse depth order
 -> one report: the problem lines, then one summary line + exit code
```

Every object's content id is verified after it is read. An object that does not
verify fails the one file that needs it: `restore` names that file, does not
write bad data into it, and goes on to the next file. The exit code is then 1.
Directory times are applied in a deferred pass in reverse depth order at the
end of the restore.

### 14.8 Cache-less restore

With no cache and no repository, the operator gives the disc roots: one
`DISC-ROOT`, `--disc` for each disc, or `--discs-dir`. `restore` resolves a
ref name from the REFS of the given discs. Every run carries REFS, DISCS and
every snapshot object of the repository at its burn time, as FORMAT.md's
"Catalog contents per run" states, thus the newest disc names every disc that
a restore needs.

If the newest disc is lost, each other disc carries the state as of its own
burn. Both paths must exist and both must be tested.

The disc-swap mode needs the cache. After a lost repository, the operator runs
`recover` one time for each disc, then restores.

---

## 15. Metadata restore policy

FORMAT.md's "Tree entry fixed header" gives the metadata field set and its
encodings. This section is the restore policy. The metadata set is type, mode,
uid, gid, mtime and the symlink target.

### 15.1 Ownership

1. The writer always stores the numeric uid and gid.
2. A restore that runs as root applies the stored numeric ids.
3. A restore that does not run as root does not attempt ownership. Files get
   the invoking user's uid and gid.
4. A failure to apply ownership must never fail the entry. The restorer writes
   the file, prints a warning with the path, the field and the reason, and
   continues.
5. Ownership is applied with `AT_SYMLINK_NOFOLLOW` semantics, never plain
   `chown`, so a symlink cannot redirect the change.

### 15.2 Restore order

The order per entry is fixed, and every step has a reason. That order and
reason is the requirement. The system calls named at each step are
**informative**.

1. Create the object: `openat`, `mkdirat`, `symlinkat`.
2. Write the content.
3. `fchownat(AT_SYMLINK_NOFOLLOW)`. chown clears setuid and setgid on Linux, so
   it must come before chmod.
4. `fchmodat`. It must follow chown to restore setuid and setgid.
5. `utimensat(AT_SYMLINK_NOFOLLOW)`. Every preceding step changes mtime, so
   times come last.
6. Directory times are applied in a deferred second pass, after all children are
   written, because writing a child updates the parent's mtime.

### 15.3 Hardlinks

FORMAT.md's "Hardlinks" gives the rule. Every hardlinked path is stored as an
independent tree entry, and the restorer creates an independent file for each
one. Link identity is not preserved: two files that were one inode on the
source become two separate inodes after restore. Content dedup already stores
the shared data once, so no disc space is lost.

### 15.4 Failure policy

`restore` has one report. Each problem in it carries the path, a kind and the
reason. The kinds and their effect on the exit code:

| Kind | Meaning | Exit code |
|---|---|---:|
| existing path | The path is already there and `--overwrite` was not given. `restore` left it exactly as found. | 1 |
| `--overwrite` could not replace | A non-empty directory stood where a file or a symlink must go. `restore` never removes a directory tree, thus it left the path as found. | 1 |
| unsupported entry | A device node, a FIFO or a socket. This build does not restore one. | 0 |
| metadata field | A `mode`, `times` or `owner` field that would not apply to a path that `restore` had already written. | 1 |
| file not restored | A bad object, or a write that failed. `restore` goes on to the next file. | 1 |

1. Restore is strict for data and best-effort for metadata. An object that does
   not verify fails the one file that needs it, and never writes bad data. A
   metadata field that cannot be applied is a warning.
2. Every problem gets one line, naming the path and the reason. `restore`
   prints 20 lines at most, then the count of the problems it does not name,
   then one summary line that counts each kind.
3. An unsupported entry alone never changes the exit code. Every other kind
   sets exit code 1. A restore does not stop at the first problem; it walks
   the whole snapshot and reports at the end.

### 15.5 Non-root restore

1. Probe privileges once at start: check the effective user id.
2. A restore that does not run as root does not attempt ownership. It prints
   no owner warning, and the missing ownership alone does not change the exit
   code.
3. To restore ownership, the operator runs the restore again as root with
   `--overwrite`.

### 15.6 Name and symlink safety

Five invariants bind every restorer. This section is their only home; they are
host behaviour and no byte of them reaches a disc.

1. A name is validated before use.
2. A path string is never built and opened.
3. A symlink is never followed on the way to a target.
4. The check and the use of a path component are one operation.
5. A symlink target is stored and restored as data and is never rewritten.

The system calls below are **informative**; another platform meets the same
invariant with its own calls.

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
   open an existing path for truncation. `restore` never removes a directory
   tree recursively: when the unlink fails, most often because a non-empty
   directory stands where a symlink or a file must go, the path is left
   exactly as found, reported on a warning line that names it, and the walk
   continues.
5. Apply directory times in the deferred pass.

### 15.7 Unstable entries

`restore` writes an `UNSTABLE` entry normally. It prints one warning line per
unstable entry, naming the path. An unstable entry alone sets exit code 1,
because the file was restored but its content is not certain.

`ls` marks an `UNSTABLE` entry with `!`.

### 15.8 Restore exit codes

Exit codes. Section 15.4's table gives the kind of each problem; this is the
same rule, read by code. There is no fourth code: a missing disc is a failure
at run time, code 1.

| Code | Meaning |
|---:|---|
| 0 | Everything applied. A device node, a FIFO or a socket in the snapshot alone does not change this: it is outside the scope of a restore, not a failure, and is reported on a warning line. |
| 1 | A failure at run time: data restored with metadata loss, an existing path left alone without `--overwrite`, a path `--overwrite` could not replace because a non-empty directory stood in its way, a file that did not restore, or a required disc that is missing. |
| 2 | A usage error, or a refused option. An empty or unknown `SNAPSHOT` argument is one. |

---

## 16. CLI reference

### 16.1 Commands and global options

The commands are `init`, `commit`, `pack`, `verify`, `plan`, `restore`,
`recover`, `gc`, `ls`, `log`, `status`, `disc burned` and
`image build`. A build refuses an unknown command and an unknown option by
name and exits with code 2.

Each command prints its exact syntax with `-h`. `docs/guide.md` is the operator
guide.

Put every option before the positional arguments of a command. A build refuses
an option that comes after a positional argument, and names it. `-h` and
`--help` print the syntax and the options of a command and exit with code 0.

The tool never runs `sudo`. The tool never mounts a device. The operator
mounts a disc and gives the mount point as a disc root. There is one
exception to "never mounts": `image build` loop-mounts the image file that it
builds, and it needs root for that (section 10.1).

These options are common.

| Option | Meaning |
|---|---|
| `--repo=PATH` | Repository root. Defaults to the discovered repository of section 2.2. Give it after the command name. |
| `--json` | Machine-readable output. On `ls`, `log` and `status` only. |
| `-q`, `--quiet` | Errors only. No progress line. |
| `--progress` | Write the progress line of a long command to standard error, also when standard error is not a terminal. |
| `--no-progress` | Write no progress line. `--progress` and `--no-progress` together are an error. |

Without `--progress` and `--no-progress`, the progress line is on only when
standard error is a terminal. `--progress`, `--no-progress` and `--quiet` can
come before or after the command name.

The config key `cache.dir` moves the cache.

A `DISC` argument names one disc of the repository. It is the `disc_seq`, the
full uuid, a uuid prefix, or the exact label. A command refuses a value that
matches no disc, and a value that matches more than one disc. The refusal
lists the candidates. Two discs can carry the same `disc_seq`; the operator
then gives the uuid, or a uuid prefix, in place of the number.

A `DISC-ROOT` argument, a `--disc=ROOT` option and a `--mount=DIR` option name
a directory: the mount point of a disc, or a copy of a disc root.
`--discs-dir=DIR` names a directory whose immediate subdirectories are disc
roots.

Section 19 is the exit code registry.

### 16.2 `init`

```
noahsark init [--repo=PATH] [--source=PATH]
```

Creates the repository of section 2.1. `--repo` defaults to the current
directory. `init` writes `repo.uuid` and `staging.dir` to the config file. It
writes `staging.dir` relative to the repository directory. It prints the
repository path, the staging path and, with `--source`, the source path.

`init` writes no capacity. A repository sets `pack.capacity` in the config,
or each `pack` gives `--capacity`.

| Option | Meaning |
|---|---|
| `--source` | The source root. `init` makes the path absolute and stores it as `sources.root`. `commit` reads it when no `SOURCE` is given. |

A fresh repository starts at `run_seq` 1 and `disc_seq` 0. `pack` takes the
next numbers from the maximum that the disc ledger holds.
`recover` recreates a lost repository from its discs; do not
run `init` first.

Exit: 0 on success; 1 when a file could not be written; 2 on a usage error, or
when the directory already holds a repository.

### 16.3 `commit`

```
noahsark commit [--repo=PATH] [--ref=NAME] [-m MESSAGE] [SOURCE]
```

Runs the commit flow of section 7.1. With no `SOURCE`, it uses the configured
source root, `sources.root`. A `SOURCE` on the command line overrides the
config for that commit. With neither, `commit` refuses, and names the two ways
to give a source. `commit` takes one `SOURCE` at most.

`commit` prints the snapshot id, the ref that moved, the counts of new and
existing objects, the counts of unstable and skipped paths, and the total that
is STAGED. It prints one `unstable` line for each unstable path and one
`skipped` line for each path that vanished during the scan.

`commit` also warns about each FIFO, socket and device node in the source:
`warning: PATH: KIND, no content is backed up`. It prints 20 such lines at
most, then one line with the count of the rest, then the line
`special files: N`. Such a path carries no content on the disc, and `restore`
does not create it again. The operator hears this at commit time, where the
source is still there to act on. A special file alone never changes the exit
code.

| Option | Meaning |
|---|---|
| `-m` | Commit message, stored as a snapshot TLV. |
| `--ref` | The ref to move. Default `LATEST`. |

The config key `commit.retry_unstable` sets the retry count. Section 7.10
lists the planned options.

Exit: 0 on success, also when the tree is unchanged and no snapshot was
written. 1 on a failure at run time, also when some files could not be read or
were unstable and the snapshot is committed all the same, or when the
repository lock is held. 2 for a usage error or a refused option.

The report lists every unstable path and says which branch was taken.

### 16.8 `pack`

```
noahsark pack [--repo=PATH] [--ref=NAME | --snapshot=ID...] [--capacity=SIZE]
              [--physical-capacity=SIZE] [--label=TEXT]
              [--out=DIR] [--fec | --no-fec] [--close]
```

Selects objects for the next run under the rules of section 8, and writes the
complete disc root of the run into the `--out` directory, with the parity when
FEC is on.

It prints the line `packed disc SEQ "LABEL": N objects, B bytes`, then the
`uuid:` and `tree:` lines, then a `next steps:` block, then the line
`remaining staged: N objects, B bytes`. The `next steps:` block holds the
exact `image build`, `growisofs`, `disc burned` and `verify` command lines for
the disc. It repeats `--repo` only when the operator gave `--repo`. The
`growisofs` line leaves the disc open, unless `--close` was given.

One run goes on one disc in this build, thus the operator output names the
disc and never the run.

A capacity smaller than the staged data is normal: `pack` writes the objects
that fit and leaves the rest STAGED for the next disc. `pack` refuses only a
capacity that holds not one object. The refusal names the smallest staged
object and its size, then the capacity to use instead.

`pack` always packs from the full STAGED pool and carries every pending ref.
`--ref` and `--snapshot` only add named refs to the ref table of the run. They
are mutually exclusive.

`pack` packs when at least one object is STAGED. It fills exactly one run per
invocation, and it starts a new disc for each run.

| Option | Meaning |
|---|---|
| `--ref` | An extra ref name to carry onto the disc. `pack` carries every pending ref without it. |
| `--snapshot` | A snapshot id whose refs the run carries. Repeatable. |
| `--capacity` | Target capacity of the disc (section 12.6). Default: the config key `pack.capacity`. With neither, `pack` refuses and names both ways. The value is a preset name (`dvd+r`, `dvd-r`, `bd25`, `bd50`, `bd100`, `bd128`), or a byte size with a decimal unit (`k`, `M`, `G`, `T`, `kB`, `MB`, `GB`, `TB`) or a binary unit (`Ki`, `Mi`, `Gi`, `Ti`, `KiB`, `MiB`, `GiB`, `TiB`). `G` is not `Gi`. A bare number is a usage error: it reads as bytes and once meant sectors. The media type recorded in `DISC.bin` follows the `--capacity` preset, else `BD-R-SL-25`; it is informational only. |
| `--physical-capacity` | The physical capacity of the disc, in the same forms. Default: the `--capacity` value. Give it only when `--capacity` forces a smaller limit than the disc has, or when the drive reports fewer sectors than the preset. |
| `--label` | Human label, printed on the disc. Default: the name of the newest ref this disc carries, then `disc SEQ`, for example `2026-09-21 disc 0`. The newest ref is the one whose snapshot was committed last. |
| `--out` | The directory that receives the disc root. It must be empty or absent. Default `<repo>/staging/plans/<disc uuid>/tree`. |
| `--fec` | Write the Reed-Solomon checksum column and parity for this run. Overrides `fec.scheme`. |
| `--no-fec` | Write no FEC for this run. Overrides `fec.scheme`. `--fec` and `--no-fec` together are an error. |
| `--close` | Seal the disc: `spare:none` and `-dvd-compat`, no POW, full capacity. `--close` changes only the burn command that `pack` prints. |

FEC is optional and off by default. Two identical discs are the primary
redundancy.

Section 8.9 lists the planned option.

Exit: 0 on success, whether or not objects stay STAGED for the next disc:
that is not a failure, the disc was packed correctly, and the `remaining
staged:` line says how much waits. 1 on a failure at run time, also when
nothing was STAGED to pack. 2 for a usage error, a refused option, an
`--out` directory that holds files, or a capacity too small for the run.

### 16.12 `verify`

```
noahsark verify [--repo=DIR] [--heal [--out=DIR]] DISC-ROOT
```

Reads a disc root back and checks it. On success it moves the run's BURNED
objects to CLEAN, and adds 1 to the verify count of every CLEAN object of the
run.

`DISC-ROOT` is the mount point of a disc or of a loop-mounted image, or a
packed disc root. It is the one required positional argument. `verify`
never mounts anything. It always reads every object back.

`verify` moves BURNED objects to CLEAN, and adds 1 to the count of the CLEAN
objects of the run. It moves no other object. It never moves a PACKED object
to BURNED: a disc root that passes, such as a loop-mounted image before the
burn, does not prove that a burn occurred. When a PACKED object of the run
remains, `verify` prints the `disc burned` command to run, after the
`verify: ok` line. A failed verify moves the BURNED objects of the run back to
PACKED and leaves the CLEAN objects alone, as "Transition rules" says.

On success `verify` prints the line `disc SEQ "LABEL": N objects, ok`, then
the state lines and the copy count line. It prints no capacity, no run header
copy count, no stream block count and no stripe count: none of those name an
action.

With a repository, `verify` refuses a disc whose uuid the disc list of that
repository does not hold. The message names `--repo` and
`recover --disc` as the two fixes. With no repository, `verify`
checks the disc root and changes no state.

On success it prints `verify: marked N object(s) CLEAN` when N is at least 1,
then one line for the verify count of the run, for example
`verify: copy 1 of 2 verified; verify the second copy before gc` or
`verify: 2 of 2 copies verified`, then `verify: ok`.

The two copies of a disc carry the same disc uuid. `verify` counts successful
passes. Two verifies of the same physical disc count as two.

| Option | Meaning |
|---|---|
| `--repo` | The repository whose staging state to update. "Repository discovery" gives the search order. |
| `--heal` | Repair the disc root with the Reed-Solomon parity of the run before the check. It refuses a run that has no FEC. |
| `--out` | With `--heal`, write the healed disc root into this directory, not in place. |

Exit: 0 clean. 1 on a failure at run time: the check or the heal failed, or
the repository does not know the disc. 2 for a usage error or a refused
option.

### 16.15 `plan`

```
noahsark plan [--repo=PATH] [--include=PATH]... [--out=FILE]
              [--staging-budget=SIZE] SNAPSHOT
```

Computes the restore plan of section 14.4 and prints it. It reads the local
cache only, and it takes no disc. `SNAPSHOT` is a snapshot id or a ref name.

The printed plan lists each disc that the restore needs: the disc number, the
label, the uuid, the objects and the bytes to read. The list follows the disc
number, then the uuid: the operator looks a disc up by the number on its
sleeve. The pass split and the peak staging bytes appear only when a staging
budget was set, by `--staging-budget` or by `restore.staging_budget`.

An object the plan cannot place is reported on a `missing:` line. The line
names the disc uuid when a cached INDEX names the disc that holds the object.
It says `disc unknown` when no cached INDEX names that disc.

| Option | Meaning |
|---|---|
| `--include` | Plan only this snapshot-relative path. When it names a directory, plan everything below it. Repeatable. |
| `--out` | Write the JSON plan to this file. `restore --plan` reads it. |
| `--staging-budget` | Peak staging allowed. Overrides `restore.staging_budget`. The value takes the unit suffixes of `pack --capacity`. |

Exit: 0 when the plan accounts for every object. 1 on a failure at run time,
also when the cache is incomplete for the snapshot, when an object has no
disc the cache knows, or when nothing is cached yet. 2 for a usage error, an
unknown snapshot id or ref name, or when one file alone needs more staging
than the budget.

### 16.16 `restore`

```
noahsark restore [--include=PATH]... [--overwrite] DISC-ROOT SNAPSHOT OUT-DIR
noahsark restore [--include=PATH]... [--overwrite]
                 (--disc=ROOT... | --discs-dir=DIR) SNAPSHOT OUT-DIR
noahsark restore [--repo=PATH] [--include=PATH]... [--overwrite] --mount=DIR
                 [--no-eject] [--interactive] [--staging-budget=SIZE]
                 SNAPSHOT OUT-DIR
noahsark restore [--repo=PATH] --plan=FILE --mount=DIR [--overwrite]
                 [--no-eject] [--interactive] [--staging-budget=SIZE] OUT-DIR
```

Runs the restore pipeline of section 14.7. `SNAPSHOT` is a snapshot id, as
`log` prints it in its first column, or a ref name. `OUT-DIR` receives the
restored tree. An empty `SNAPSHOT` is refused by name, never quoted back as an
empty ref. In the first form, a `DISC-ROOT` that is not a directory is refused
by name too.

There are two modes.

- **All discs at once.** The first two forms read every given disc root
  together. They need no repository. They resolve a ref name from the ref
  tables of the given discs. One `DISC-ROOT` is sufficient when one disc holds
  the full snapshot.
- **Disc swap, one drive.** The `--mount` forms need the repository. They
  resolve `SNAPSHOT` through the local cache, print the plan, and read one disc
  at a time from `DIR`. Between discs, `restore` ejects the disc, names the
  subsequent disc by `disc_seq`, label and uuid, and waits for Enter. The
  operator mounts each disc at `DIR`. A wrong disc gives the expected and the
  found disc, then the same prompt. A repeated command continues, and asks only
  for the discs that it still needs. `--mount` does not go together with a
  `DISC-ROOT`, `--disc` or `--discs-dir`.

`restore` leaves an existing path alone unless `--overwrite` is given. It
reports in one form, whichever mode it ran in:

```
noahsark: restore: warning: <PATH>: <REASON>     one line per problem
noahsark: restore: warning: N more problem(s) not shown
restored snapshot <ID> into <OUT-DIR>
resumed: N file(s) already restored             when any path was resumed
not restored: N existing path(s), N unsupported entry(ies); see the warning(s) above
```

The problem lines go to stderr, the result lines to stdout. A problem line
names the path and the reason; a path this build cannot restore reads `FIFO`,
`socket` or `device`, never an entry type number. `restore` prints 20 problem
lines at most, then one line with the count of the rest. The last line is one
summary line that counts each kind of section 15.4's table; `restore` prints
it only when it met a problem.

| Option | Meaning |
|---|---|
| `--disc` | A disc root to read. Repeatable. |
| `--discs-dir` | A directory whose immediate subdirectories are disc roots. |
| `--plan` | Resume a persisted plan that `plan --out` wrote. It fixes the snapshot, thus this form takes no `SNAPSHOT`. It requires `--mount`. |
| `--include` | Restore only these paths. Repeatable. A path is relative to the snapshot. A directory includes everything below it. |
| `--staging-budget` | Peak staging allowed. Overrides `restore.staging_budget`. |
| `--interactive` | Prompt on every disc, not only on a mismatch. |
| `--mount` | The directory a single drive is mounted at, for the one-drive disc-swap mode: no `DISC-ROOT`, `--disc`, or `--discs-dir`, one disc read at a time, with a prompt between discs. Required in that mode; there is no config default. |
| `--no-eject` | Do not eject after each disc. |
| `--overwrite` | Unlink an existing path first and then create it. |

Exit: 0 when everything applied, and always for an unsupported entry (a
device node, a FIFO or a socket): `restore` still names each one on a
warning line, but that alone never changes the exit code. 1 on a failure at
run time, also when `restore` left an existing path alone without
`--overwrite`, when a file did not restore, when a metadata field was not
applied, or when a required disc is missing: not among the given discs, or
absent from the cache the disc-swap mode reads through; `restore` names the
missing disc by uuid. 2 for a usage error or a refused option, an empty or
unknown `SNAPSHOT` included.

### 16.17 `recover`

```
noahsark recover [--repo=PATH] [--disc=ROOT]... [--discs-dir=DIR]
```

Rebuilds the repository state from discs. It also rebuilds the repository state
that the discs can prove: the config, the disc list, the refs and the state
log. It creates the repository directory when it is absent, thus it recovers a
lost repository. It merges into the state that exists. With one drive, the
operator runs it one time for each disc, in any order. Two discs that carry the
same sequence number are accepted: the disc uuid tells them apart.

`recover` records the objects of a fed disc as ON-DISC: the disc holds
them, and staging holds no file for them. Do not run `disc burned` or
`verify` again for such a disc. There is nothing left in staging for them to
protect.

It prints `recover: ok` when every disc that the fed discs name was fed.
Otherwise it prints one
`recover is partial: disc SEQ "LABEL" (UUID) not fed yet` line for each such
disc.

| Option | Meaning |
|---|---|
| `--disc` | A disc root to read. Repeatable. |
| `--discs-dir` | A directory whose immediate subdirectories are disc roots. |

Exit: 0 on success. 1 on a failure at run time, also when the rebuild is
partial, when the given discs do not share one `repo_uuid`, or when no
usable disc was given. 2 on a usage error.

### 16.20 `gc`

```
noahsark gc [--repo=PATH] [--dry-run] [--force-after=DURATION]
```

Frees the staged files of CLEAN objects, under the GC rules.
It leaves an object alone, and reports it, when the disc that holds the object
is not in the local cache. It confirms the object in the cached INDEX of that
one disc, found by the disc uuid of the object's own state record.

`gc` holds an object whose verify count is below `gc.min_verified_copies`,
default 2. It prints one line for each disc that holds objects back:
`gc: disc UUID: C of N copies verified; K object(s) held; verify the second copy`.
It prints that line in the dry run and in the real run. No option passes by
this count; `gc.min_verified_copies` is the only control.

`gc` never trims the local cache.

| Option | Meaning |
|---|---|
| `--dry-run` | Print the totals that `gc` would delete, one line for each disc, and delete nothing. When nothing is eligible, print `gc: nothing is eligible yet` and the earliest date at which an object becomes eligible. It asks for no confirmation. |
| `--force-after` | Shorten the retention for this run only. It does not change the verify count rule. It requires a confirmation: `gc` prints `delete N object(s), B bytes? [y/N]` on standard error and deletes only on `y` or `yes`. Any other answer, an empty line and a closed standard input all mean no. A script answers with a pipe: `echo y \| noahsark gc --force-after=1h`. `DURATION` is a whole number of days with a `d` suffix, or a Go duration such as `1h`. |

Exit: 0 on success, always for `--dry-run`, and when nothing was eligible;
"nothing eligible" is not a failure. 1 on a failure at run time, also when a
staged file could not be unlinked, naming its path and the error, and when
the `--force-after` confirmation was refused or not possible. 2 on a usage
error.

### 16.21 `ls`

```
noahsark ls [--repo=PATH] [--long] [--recursive] [--json] [--unstable-only]
            [--disc=ROOT]... [--discs-dir=DIR] [DISC-ROOT] SNAPSHOT [PATH]
```

Lists a snapshot's tree. It reads tree objects only, never chunks. `SNAPSHOT`
is a snapshot id or a ref name.

With no disc given, `ls` resolves `SNAPSHOT` through the local cache. With a
`DISC-ROOT`, `--disc` or `--discs-dir`, it reads the discs and needs no
repository. A first argument that is an existing directory is a `DISC-ROOT`.

| Option | Meaning |
|---|---|
| `--disc` | A disc root to read. Repeatable. |
| `--discs-dir` | A directory whose immediate subdirectories are disc roots. |
| `--json` | Print the entries as a JSON array. |
| `--long` | Print mode, owner, size and mtime. |
| `--recursive` | Descend into subdirectories. |
| `--unstable-only` | List just the `UNSTABLE` entries. |

An `UNSTABLE` entry is marked with `!` in the first column, and with
`"unstable": true` under `--json`. Every other line has a space in the first
column.

Exit: 0 on success. 1 on a failure at run time, also when a needed tree
object is unavailable: the cache is incomplete for the snapshot, or a
required disc was not given. 2 on a usage error.

### 16.22 `log`

```
noahsark log [--repo=PATH] [--limit=N] [--json] [--disc=ROOT]...
             [--discs-dir=DIR] [DISC-ROOT] [REF|SNAPSHOT]
```

Prints the snapshot history. It reads the pending chain of section 5.2, then
the snapshot objects of the catalog.

`log` with no `REF` or `SNAPSHOT` lists every snapshot, newest first: id,
time, refs, root paths, object count and size. With a `REF` or a `SNAPSHOT`,
it prints the details of that one snapshot. The disc rules of `ls` apply: no
disc reads the local cache, and a `DISC-ROOT`, `--disc` or `--discs-dir` reads
the discs.

| Option | Meaning |
|---|---|
| `--disc` | A disc root to read. Repeatable. |
| `--discs-dir` | A directory whose immediate subdirectories are disc roots. |
| `--json` | Print JSON. |
| `--limit` | Print at most N entries. 0 means no limit. |

Exit: 0 on success. 1 on a failure at run time, also when the cache is
incomplete for the snapshot, or a required disc was not given. 2 on a usage
error.

### 16.23 `status`

```
noahsark status [--repo=PATH] [--json]
```

Prints what the operator must know and nothing else:

1. The line `staged: N objects, B bytes`: what waits for the next `pack`.
2. One line for each disc: `disc SEQ "LABEL"  STATE  UUID`. `STATE` is one
   word: `packed`, `burned`, `verified C/N`, `verified` or `on disc only`.
   `C` is the lowest verify count of the CLEAN objects of the disc, and `N`
   is `gc.min_verified_copies`.
3. One `next:` line that names the one action to take next, in the order of
   the disc cycle: burn a packed disc, verify a burned disc, verify the
   second copy of a disc, pack a disc, or `next: nothing to do`.

`--json` keeps the exact numbers the one-word state leaves out: the object
counts, the capacity, the used bytes, the verify count and
`gc.min_verified_copies`.

Exit: 0 on success. 1 on a failure at run time. 2 for a usage error.

### 16.23a `disc`

```
noahsark disc burned [--repo=PATH] [--undo] DISC [DISC...]
```

Give the options after the subcommand.

`burned` tells the repository that the operator burned each named `DISC`. It
moves every PACKED object of the runs of that disc to BURNED, and records the
burn time. It is the only transition from PACKED to BURNED. The tool never
concludes by itself that a burn occurred. It prints
`disc SEQ LABEL: marked burned, N objects`, or
`disc SEQ LABEL: already burned, 0 objects to mark`. A second copy of the same
disc needs no second `disc burned`.

`burned --undo` moves the BURNED objects of the disc back to PACKED, for a burn
that was bad. It refuses a disc that has a CLEAN object: a verified disc does
not go back to PACKED.

The operator writes the label and the storage place on the sleeve of each
disc, and keeps a text file near the repository. The tool keeps no shelf
notes.

Exit: 0 on success. 1 on a failure at run time, also when `burned --undo`
names a disc that already has a CLEAN object. 2 for a usage error, a refused
option, or when a `DISC` argument matches no disc or more than one disc.

### 16.24 `image`

```
noahsark image build --out=FILE [--force] TREE-DIR
```

`image build` builds a UDF image from `TREE-DIR`, the disc root that
`pack --out` wrote. The image length is the target capacity that `pack`
already wrote into the `DISC.bin` of that tree. There is no `--capacity`
option: a capacity repeated by hand can differ from the one the run was
packed for, and an image of the wrong length is not the disc `pack` planned. It checks the `mkudffs`
version first. It needs root, because it loop-mounts the image file to fill
it. It never runs `sudo`: when it is not root, it removes the partial image
and prints the exact `sudo noahsark image build ...` line to run. It prints
`built image FILE (N bytes)`. It also lets the whole burn path run in CI
with no drive.

| Option | Meaning |
|---|---|
| `--force` | Replace `--out` when it exists. Without it, `image build` refuses an existing file. |
| `--out` | Where to write the image. |

Exit: 0 on success. 1 on a failure at run time, also when `image build` is
not root. 2 for a usage error, a refused option, or an existing `--out`
without `--force`.

---

## 17. Configuration reference

The config file lives at `<repo>/config`. It is a plain text key-value file
with one `key = value` pair per line, `#` for a comment, and UTF-8 encoding. A
CLI option always overrides the file.

A build must refuse an unknown key with a clear message that names the key.

The build reads these keys: `repo.uuid`, `staging.dir`, `sources.root`,
`commit.restat_after_read`, `commit.retry_unstable`, `fec.scheme`,
`pack.capacity`, `cache.dir`,
`cache.format_version`, `restore.staging_budget`,
`staging.retain_after_clean` and `gc.min_verified_copies`. It refuses each
other key of the tables below. Those keys name the values that the build
holds as constants. The second pass of GitHub issue #26 decides which of
them stay.

A key whose "changes disc bytes" column says no is tuning: it changes speed,
memory or waiting time only, it affects no byte that reaches a disc, and its
value is recorded in no structure.

A change to any key whose column says yes, after a repository holds discs, is
treated as a new epoch or a new profile, never as a silent in-place change.
This section is the index that rule depends on. An omission from it is a defect
in this table, not license to change a key silently. FORMAT.md's "Settings
that change disc bytes" carries the same index from the reader's side.

Every key appears exactly once, in exactly one table below.

### 17.1 Identity and format

| Key | Type | Default | Changes disc bytes | Meaning |
|---|---|---|---|---|
| `repo.uuid` | uuid | generated | no | Repository uuid. Never changed. |
| `format.version_major` | integer | 1 | yes | On-disc format major version. |
| `format.version_minor` | integer | 0 | yes | On-disc format minor version. |

### 17.2 Hashing and chunking

| Key | Type | Default | Changes disc bytes | Meaning |
|---|---|---|---|---|
| `hash.current` | enum | `blake3` | yes | Algorithm for new objects: `blake3` or `sha256`. A change starts a new hash epoch. |
| `chunker.profile` | enum | `P4` | yes | Chunker profile: `P3`, `P4` or `P5`. |
| `chunker.gear_table_id` | integer | 1 | yes | Frozen Gear table version. Never change it under a profile name. |

### 17.3 Compression

| Key | Type | Default | Changes disc bytes | Meaning |
|---|---|---|---|---|
| `compression.algorithm` | enum | `zstd` | yes | `none`, `zstd` or `lz4`. |
| `compression.level` | integer | 3 | yes | Algorithm level. |
| `compression.min_gain` | fraction | 0.05 | yes | Store uncompressed when compression saves less than this fraction. |

### 17.4 Filesystem

| Key | Type | Default | Changes disc bytes | Meaning |
|---|---|---|---|---|
| `fs.profile` | integer | 0 | yes | Disc filesystem profile. The build writes profile 0, one-shot UDF, only. Fixed per disc at its first burn. |
| `fs.fanout_levels` | integer | 1 | yes | Object fan-out depth. |
| `udf.revision` | string | `2.01` | yes | UDF revision `mkudffs` writes. |

### 17.7 FEC

| Key | Type | Default | Changes disc bytes | Meaning |
|---|---|---|---|---|
| `fec.scheme` | enum | `none` | yes | FEC scheme registry: `none` (0) or `rs255-gf8` (1). FEC is optional and off by default. `pack --fec` and `pack --no-fec` override the key for one run. |
| `fec.k` | integer | refused | yes | Reserved for a later format version. A version 1 build refuses the key. |
| `fec.m` | integer | refused | yes | Reserved for a later format version. A version 1 build refuses the key. |
| `fec.band_stripes` | integer | 2048 | no | Stripes per encoding band. |

### 17.9 Sources and excludes

| Key | Type | Default | Changes disc bytes | Meaning |
|---|---|---|---|---|
| `sources.root` | path | unset | no | The absolute source root. `commit` uses it when no `SOURCE` is given. |
| `sources.exclude` | pattern, repeatable | unset | yes | An exclude pattern in the language of FORMAT.md's "Exclude pattern language". Planned, not built (section 7.10). |
| `sources.ignore_file` | string | `.noahsarkignore` | yes | Per-directory exclude file name. Empty disables it. Planned, not built (section 7.10). |
| `sources.one_file_system` | boolean | true | yes | Do not cross a mount point. Planned, not built (section 7.10). |
| `commit.checksum` | boolean | false | no | Always rehash. Equivalent to `--checksum` on every commit. Planned, not built (section 7.10). |
| `commit.restat_after_read` | boolean | true | no | In-flight change detection. Never set it false on a live source. |
| `commit.retry_unstable` | integer | 1 | no | Re-reads of an unstable file before the rule of section 7.6 applies. |

### 17.12 Staging and cache

| Key | Type | Default | Changes disc bytes | Meaning |
|---|---|---|---|---|
| `staging.dir` | path | `<repo>/staging` | no | Staging store location. A relative path is relative to the repository directory. `init` writes `staging`. |
| `staging.retain_after_clean` | duration | 7 days | no | Retention before an object becomes GC-eligible. It counts from the first successful verify. |
| `gc.min_verified_copies` | integer | 2 | no | Successful verifies an object needs before `gc` may delete it. The default holds the staged data until the second identical disc passes `verify`. A value below 1 is a config error. An operator who keeps one copy only sets 1. |
| `cache.dir` | path | see section 2.4 | no | Local cache location. |
| `cache.format_version` | integer | 1 | no | Delete and rebuild on a mismatch. |

### 17.12a Packing

| Key | Type | Default | Changes disc bytes | Meaning |
|---|---|---|---|---|
| `pack.capacity` | preset or size | unset | no | The target capacity `pack` uses when its own command line gives no `--capacity`. It takes the forms of `pack --capacity`, so a bare number is a config error. |

### 17.13 Restore

| Key | Type | Default | Changes disc bytes | Meaning |
|---|---|---|---|---|
| `restore.staging_budget` | bytes | 16 GiB | no | Peak staging allowed. The planner falls back to multi-pass above it. |

---

## 19. Exit code registry

This is the one registry of exit codes. Every command uses exactly these
three codes; there is no per-command special code and no fourth code.

| Code | Meaning |
|---:|---|
| 0 | Success. A partial outcome that needed no operator action, such as `gc` finding nothing eligible, is still success. |
| 1 | A failure at run time: a read or write failed, a required disc or object is missing, a repository lock is held, or a partial success still needs the operator's attention (an unstable file, a skipped restore path, a metadata field not applied). |
| 2 | A usage error: a bad option, a bad argument, a bad config value, or an unknown command. |

The exit line of each command in the CLI reference states which of its own
conditions fall under code 1 and which fall under code 2; no command departs
from this table.

Section 15.8 gives the restore metadata exit codes, which are the same set.

Every refusal is loud, names the field and the value, and never touches the
bytes it refused. Every partial case reads the data in full and reports the
loss on warning lines. An error about an object names the object. An error about a run
names the run seq and the disc uuid.

---

## 20. Failure and recovery actions

Two identical discs are the primary redundancy. Copy A and copy B hold the
same image.

| # | Failure | Recovery action |
|---:|---|---|
| 1 | A burn fails midway | Discard the disc. Burn the same image on a new disc with the `growisofs` line that `pack` printed. If `disc burned` already ran, run `disc burned --undo DISC`, then `disc burned DISC` after the good burn. |
| 2 | `verify` fails, or the disc does not mount | Discard the disc. Burn the same image on a new disc, then run `verify` on it. There is no recovery of a disc that does not mount. |
| 3 | One copy of a disc is lost or bad later | Read from the other copy: `restore` with its disc root. Burn a new copy from the kept image, or from an image that `ddrescue` reads from the good copy. When the run has FEC, `verify --heal --out=DIR` can repair a copy of the bad disc root. |
| 4 | The two copies of a disc are lost | `restore` with the other discs restores what they hold and names each object that it cannot find. Then `commit` the source into a new repository. |
| 5 | The newest disc is lost | Each other disc carries the catalog as of its own burn. `restore`, `ls` and `log` work with the discs that remain. |
| 6 | The local cache is lost or wrong | `recover`, one time for each disc. |
| 7 | The repository directory is lost | `recover --repo=<new>`, one time for each disc. Do not run `init` first. The discs come back as ON-DISC; no `disc burned` and no `verify` follow. |
| 8 | The state log is truncated by a crash | At open, the tool cuts the torn tail, prints one warning and goes on. Run the interrupted command again. A bad record in the middle of the log is damage, not a torn tail: the tool reports it as an error and changes nothing. |
| 9 | `pack` stops: a staged object fails its content id check, or a sync error occurs | `pack` records nothing, and the sequence numbers stay free. For a corrupt staged object, run `commit` again. Delete the part-written `--out` directory. Run `pack` again. |
| 10 | A sync error in `gc` | `gc` stops before the delete. Run `gc` again. |
| 11 | A file changes while `commit` reads it | The parent entry is reused, or the new content is stored with the `UNSTABLE` flag. `commit` reports the path. Run `commit` again later. |
| 12 | A required disc is missing at restore time | `restore` fails before any read and names the disc by uuid. Find the disc, or its second copy, and run `restore` again. |
| 13 | A wrong disc is in the drive during a disc-swap restore | `restore` names the expected and the found disc and prompts again. |
| 14 | An unknown critical TLV or an unknown format version | The tool refuses the entry or the disc. Upgrade the tool. |

---

## 22. Test list

A probe is not a test: a test asserts a known answer, a probe records an
unknown one. A probe answer that becomes stable moves into the test list below
as a test. The notes document holds the probe list.

Every test below must run. A test marked FORMAT proves a rule of `FORMAT.md`;
a test marked OPS proves a rule of this document. Every burn test uses an image
file first. Physical burns are a manual checklist, not CI. The test numbers
have gaps, because a deleted test keeps its number unused.

| # | Test | Where |
|---:|---|---|
| 1 | Format round-trip for every structure, against byte-exact golden files | FORMAT |
| 2 | Chunker golden vectors for P3, P4 and P5 | FORMAT |
| 3 | Chunker determinism across buffer sizes and read patterns | FORMAT |
| 5 | INDEX lookup: every present id found, no absent id found | FORMAT |
| 6 | RS reconstruct at exactly `m` erasures per stripe | FORMAT |
| 7 | RS refusal at `m + 1` erasures | FORMAT |
| 8 | Burst damage across column boundaries | FORMAT |
| 9 | Checksum column locates a silently corrupted sector | FORMAT |
| 11 | Cache-less restore: delete the cache, restore from the disc images alone | OPS |
| 12 | Restore plan determinism | OPS |
| 13 | Metadata restore as a non-root user: no owner attempt, exit code 0 | OPS |
| 14 | Sparse round-trip: holes in, holes out | FORMAT |
| 15 | Compression heuristic: a low-gain chunk is stored uncompressed | FORMAT |
| 16 | Tree canonical order: two identical directories hash identically | FORMAT |
| 17 | Name validation: every illegal name is refused at parse time | FORMAT |
| 18 | Symlink redirection attack: a planted symlink does not escape | OPS |
| 19 | Hardlinked source paths restore as independent, correct files; dedup stores the data once | OPS |
| 20 | State log replay after a truncated write | OPS |
| 21 | GC refuses to delete an object that is not CLEAN | OPS |
| 23 | Forced capacity: the image, the budget and the FEC layout all shrink | OPS |
| 24 | Capacity budget: a run above the budget is refused; FEC off charges no parity | OPS |
| 26 | Quick check: a matching file is never read | OPS |
| 27 | In-flight change: parent entry reused, else `UNSTABLE` set | OPS |
| 29 | BURNED objects stay in staging until `verify` succeeds; only `disc burned` moves PACKED to BURNED | OPS |
| 31 | `UNSTABLE` round trip through write, read, restore and `ls` | FORMAT |
| 32 | `pack --close` prints the sealed burn line: `spare:none`, `-dvd-compat` | OPS |
| 33 | Profile 0 open image mounts with the anchor at LBA 256 alone | OPS |
| 35 | DISCS: the planner names the right disc when run order is not disc order | OPS |
| 36 | Dedup: only an exact INDEX hit drops a chunk | FORMAT |
| 38 | Unchanged commit writes no snapshot object | OPS |
| 39 | Local ref log: parent resolves locally first; a rebuilt repository recovers every ref | OPS |
| 40 | RS worked example: the `k = 3`, `m = 2` bytes encode and decode as printed | FORMAT |
| 43 | `README.txt` and `FORMAT.txt` byte-identical to the golden files | FORMAT |
| 47 | Partial `pack`: no PACKED record and no used sequence number; the next `pack` succeeds | OPS |
| 50 | Fill order: two writers produce the same order | FORMAT |
| 53 | Tree entry layout: area order, alignment and padding | FORMAT |
| 55 | `hardlink_group` is always 0 and `HARDLINK_MEMBER` always clear | FORMAT |
| 59 | `FORMAT.txt` equals the normative text byte for byte | FORMAT |

---

## 23. Manual physical checklist

These steps need a real drive and real media. They run once per release and
after any change to the burn path.

1. `dvd+rw-mediainfo` on a blank disc records the profile and the capacity.
2. A first burn completes with the `growisofs` line that `pack` printed.
3. Eject, reload, mount, and run `noahsark verify` on the mount point.
4. Mount on Linux. The file count matches.
5. Mount on Windows 10 and 11. Explorer shows the tree. Hash one object.
6. Mount on macOS 15. The file count matches.
7. Recommended: verify the second copy in a second drive of a different
   model.
8. M-DISC at speed 2 completes and verifies. A BD-R XL 100 GB disc fills to
   the capacity budget with no capacity surprise.

---

## 24. Burning-host command reference

Every command in this section is **informative**. It runs on the Linux host
that holds the drive. The normative command lines are in section 10.2.

```bash
# Probe the drive and the medium.
dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Number of Sessions|Next Writable Address'
# Check a built UDF image before the burn.
udfinfo run.udf | grep -q '^integrity=closed' || { echo "dirty image"; exit 1; }
# Verify after the burn. The operator mounts the disc; the tool never does.
eject /dev/sr0 && eject -t /dev/sr0 && sleep 5
noahsark disc burned <DISC>
sudo mount /dev/sr0 /mnt/ark
noahsark verify /mnt/ark
sudo umount /mnt/ark
# Copy a good disc to an image, to burn a new copy.
ddrescue -b 2048 -n -r1 /dev/sr0 copy.img copy.map
# Tool versions to check: dvd+rw-tools 7.1-14 or newer and udftools 2.3
# or newer.
growisofs -version 2>&1 | head -2
mkudffs --help 2>&1 | head -1
```

M-DISC replaces speed 4 with speed 2.
