# NoahsArk Specification

> Go rewrite of the Blu-ray optical disc archival system.
> Incorporates design strengths from Git, Restic, Casync, Bup, BitTorrent v2, and Borg.

---

## 1. Overview

NoahsArk is a content-addressable backup system designed for write-once Blu-ray optical
media. It provides:

- **Incremental backup** — unchanged data is never re-written (content-addressed dedup)
- **Sub-file deduplication** — fixed-size chunking (16 MiB) with blob-level indexing
- **Pack files (removed)** — NoahsArk no longer stores `.pack` bundle files on disc; instead
  each disc stores the selected content-addressed objects directly under `NOAHSARK/objects/`.
- **Disc image generation** — outputs a directory ready for burning (UDF-compatible layout)
- **Multi-session support** — partially-filled discs can be continued in future burn sessions
- **Disc-spanning** — files larger than one disc split across multiple discs automatically
- **Point-in-time restore** — full commit chain (Git-like) enables any snapshot retrieval
- **Real-time change detection** — fsnotify (inotify on Linux) watches source directories
- **Hierarchical integrity** — per-file Merkle trees (BitTorrent v2 style)
- **Duplicate discs for redundancy** — no FEC; burn two identical copies of each disc for simple redundancy
- **Pure file operations** — no filesystem-specific features required

---

## 2. High-Level Architecture

```
Source files
     │
     ▼  fsnotify (real-time) OR manual invocation
  noahsark commit
     │
     ├─ For each file:
     │   ├─ Fixed-size 16 MiB chunking (single-pass with Merkle tree computation)
     │   │   ├─ Simultaneously compute: 16 KiB Merkle leaves + 16 MiB chunk hashes
     │   │   ├─ Global index check: chunk already exists?
     │   │   │   yes → skip (dedup)
     │   │   │    no → write loose chunk to .noahsark/objects/XX/YY...
     │   │   └─ Compute Merkle root from leaves
     │   └─ Create blob object (size, chunks list, merkle_root)
     │       └─ Write to .noahsark/metadata/XX/YY... (named by blob content hash)
     │
     ├─ Build tree objects (directory hierarchy)
     │   └─ Write to .noahsark/metadata/XX/YY...
     │
     └─ Write commit object (tree, parent, author, committer, hostname, message)
                │
                ├─ Read current HEAD → get parent commit SHA-256
                ├─ Create commit content with parent pointer
                ├─ Calculate commit SHA-256 = SHA-256("commit " + size + "\0" + content)
                ├─ Write commit to .noahsark/objects/XX/YY... (content-addressed)
                └─ Update .noahsark/HEAD with new commit SHA-256

  noahsark stage [--size=BD-25|BD-50|BD-100|BD-128|<size>] [--disc=<disc_id>] [output-dir]
     │
     │  --size       target disc capacity (default: BD-25)
     │              presets: BD-25, BD-50, BD-100, BD-128
     │              custom: <number><unit> (e.g., 20G, 500M, 1T)
     │              units: K, M, G, T (base-2: KiB, MiB, GiB, TiB)
     │  --disc       resume a partially-filled disc session (optional)
     │               if omitted, a new disc_id is allocated
     │
     ├─ Load disc session state (remaining capacity, already-staged objects)
     ├─ Select unstaged objects up to remaining capacity
     ├─ Copy selected objects to staging directory
     ├─ Update global index with disc locations
     ├─ Write disc.json + manifest
     ├─ Output: output-dir/  (UDF-compatible directory layout, ready for growisofs)
     └─ Report:
          Packed:    12.4 GiB
          Remaining: 10.9 GiB  (disc <disc_id> still has space)
          Pending:    3.2 GiB  (unstaged objects waiting for next stage)

       → "Burn: growisofs -Z /dev/sr0 -udf -V '20260224-001-BD25' output-dir/"
       → "Or:   noahsark stage --disc=<disc_id>     to continue filling this disc (session N)"
       → "Or:   noahsark stage --size=BD-25      to allocate and start a new disc"
       → "Or:   noahsark stage --size=50G        to allocate custom-sized disc"
       
     `noahsark stage` may be run repeatedly: each invocation will continue
     assigning objects into the active disc session (or allocate the next
     disc if the previous one is full). All staged sessions are written to
     `.noahsark/staged/<disc_id>-s<session>/` and remain there until burned
     with `noahsark burn` or removed with `noahsark stage gc`.

  noahsark restore [commit] [src-path] [dest-path]
     └─ Global index lookup → locate chunks across discs → reassemble → verify Merkle
```

---

## 3. Object Model

**Design principle: Complete content-addressing (Git-inspired)**

All objects — including commits — are stored content-addressably by SHA-256.
This provides:
- **Automatic deduplication**: identical content → same hash → stored once
- **Integrity verification**: any corruption changes the hash
- **Distributed trust**: no central authority needed
- **Immutability**: objects never change after creation

Object storage layout:
```
.noahsark/objects/<hash[:2]>/<hash[2:]>
```

All objects (chunks, blobs, trees, commits) use the same storage structure.
SHA-256 provides 256-bit collision resistance (~2^128 security level after birthday paradox).

**Key difference from timestamp-based design:**
- ❌ Old: Commits named by RFC3339 timestamp, parent = timestamp
- ✅ New: Commits named by SHA-256, parent = SHA-256 hash
- **Why**: Content-addressing enables verification of entire history chain and
  ensures commits are truly immutable (changing any field changes the hash)

**Encoding specification:**

All metadata objects (blobs, trees, commits) are stored as UTF-8 encoded text files:
- **Character encoding**: UTF-8 (no BOM)
- **Line endings**: Unix-style LF (`\n`)
- **Chunk objects**: Raw binary data (no text encoding)

This ensures full Unicode support for filenames, paths, and commit messages across all platforms.

### 3.1 Chunk

The leaf-level dedup unit. Raw bytes of a file segment.

```
<raw bytes>  (optionally zstd-compressed in Phase 2+)
```

**Size:**
- Full chunks: exactly 16777216 bytes (16 MiB)
- Last chunk of a file: 1 to 16777216 bytes (actual remaining file data, **no padding**)

**Naming:**
- SHA-256 hash of the **actual uncompressed bytes** (no padding added before hashing)

**Important:** Chunks themselves do NOT store metadata (size, hash algorithm).
All metadata is stored in the blob object that references them (see §3.2).

### 3.2 Blob

Represents a single file. Lists the ordered chunks that compose it.

```
blob
size <DECIMAL_BYTES>
chunks <COUNT>
<chunk_sha256> <offset> <length>
<chunk_sha256> <offset> <length>
...
merkle_root <SHA256_HEX>
```

**Fields:**
- `size`: total file size in bytes
- `chunks`: number of chunks (for quick validation)
- `chunk_sha256`: SHA-256 hash of chunk data (hex, 64 chars)
- `offset`: byte position in the original file (always `chunk_index × 16777216`)
- `length`: actual byte length of this chunk (16777216 for full chunks, less for last chunk)
- `merkle_root`: SHA-256 root of the per-file Merkle tree (see §6)

**Implementation note:**
- Merkle trees are retained. Implementations SHOULD compute Merkle leaves
  (16 KiB) and chunk hashes (16 MiB) in the same single-file scan to avoid
  extra I/O: as the file is streamed, hash each 16 KiB block for the Merkle
  layer and concurrently accumulate bytes to compute each 16 MiB chunk hash.
- Mapping: leaf index = floor(byte_offset / 16384). Blob `chunk` entries map
  to byte ranges; this mapping enables verifying any chunk either by direct
  chunk hash or by reconstructing the corresponding leaves and verifying the
  Merkle root.

**Example (42 MiB file):**
```
blob
size 44040192
chunks 3
f5a7d6ac...579c 0 16777216
71d8cfc9...234d 16777216 16777216
e2875c46...2d37 33554432 10485760
merkle_root a1b2c3d4...7890
```

**Important:** The `length` field records the **actual chunk size**.
- Chunks 0-1: `length = 16777216` (full 16 MiB)
- Chunk 2: `length = 10485760` (10 MiB, no padding)

Named by: SHA-256 of this blob metadata text.

### 3.3 Tree

Represents a directory.

```
tree
<mode> <type> <object_sha256> <name>
<mode> <type> <object_sha256> <name>
...
```

**Entry format (space-separated):**
- `mode`: POSIX permission bits (decimal ASCII, e.g., 100644)
- `type`: one of `blob`, `tree`, `link`
- `object_sha256`: SHA-256 hash of the referenced object (hex, 64 chars)
- `name`: filename or subdirectory name (UTF-8, everything after the 3rd space)

**Parsing rule:** Split each line on space to extract the first 3 fields (mode, type, hash). Everything after the 3rd space is the filename, which may contain spaces and UTF-8 characters.

**Filename restrictions:** Filenames cannot contain newline (`\n`) or null bytes. All other UTF-8 characters (including spaces, Chinese, Japanese, emoji, etc.) are supported.

Tree entries include POSIX-like mode bits (Git-style) so permissions are
tracked as part of the tree object. Changing permissions will change the
tree SHA, just like changing file contents or renaming entries.

`mode` field (decimal ASCII) semantics:
- `040000` — tree (directory)
- `100644` — regular non-executable file
- `100755` — regular executable file
- `120000` — symlink

Examples:
```
tree
100644 blob a1b2c3d4... README.md
100755 blob f6e7d8c9... install.sh
100644 blob 1a2b3c4d... my document.txt
100644 blob 2b3c4d5e... 文档.pdf
040000 tree 9f8e7d6c... src
120000 link 5a4b3c2d... link-to-config
```

Note: `my document.txt` (with space) and `文档.pdf` (UTF-8 Chinese) are fully supported.

Named by: SHA-256 of this tree metadata text.

**Symlinks:**

Symlink objects are stored as separate objects containing the link target:
```
link
<target_string>
```

Where `<target_string>` is the literal symlink target path (UTF-8, e.g., `../config/app.conf`).

The link object is named by SHA-256 of this content and referenced in tree entries with mode `120000`.

### 3.4 Commit

```
commit
tree <ROOT_TREE_SHA256>
parent <PARENT_COMMIT_SHA256>
author <NAME> <EMAIL> <RFC3339_TIMESTAMP>
committer <NAME> <EMAIL> <RFC3339_TIMESTAMP>
hostname <HOSTNAME>

<COMMIT_MESSAGE>
---
<blob_sha256> <relative_path>
<blob_sha256> <relative_path>
...
```

**Format (Git-inspired, content-addressed):**
- `tree`: SHA-256 hash of root tree object (required)
- `parent`: SHA-256 hash of parent commit (empty if first commit)
- `author`: who created the backup + when
- `committer`: who committed it + when (usually same as author)
- `hostname`: machine hostname for tracking backup source
- `<COMMIT_MESSAGE>`: optional multi-line commit message (UTF-8, after blank line)

The section after `---` lists **only new or changed blobs** in this commit (incremental).
Unchanged files are implicit via the tree structure.

**Blob list format:**
```
<blob_sha256> <relative_path>
```
- `blob_sha256`: 64-char hex SHA-256 hash
- `relative_path`: UTF-8 path, everything after the first space (may contain spaces)

**Parsing rule:** Split each blob list line on the first space to extract the hash (64 chars). Everything after the first space is the relative path, which may contain spaces and UTF-8 characters.

**Path restrictions:** Paths cannot contain newline (`\n`) or null bytes.

**Naming (content-addressed):**
- Commit SHA-256 = `SHA-256("commit " + size + "\0" + commit_content)`
- This is the **fundamental change**: commits are named by their content hash, not timestamp
- Stored at: `.noahsark/objects/XX/YYYY...` (same as all other objects)

**Parent pointer:**
- Always contains parent commit's **SHA-256 hash** (64-char hex)
- Empty or `0000...` for the initial commit
- Forms a linear chain: commit → parent → grandparent → ...

**Example:**
```
commit
tree a1b2c3d4e5f6789...
parent f6e5d4c3b2a1098...
author John Doe <john@example.com> 2026-02-24T12:00:00+08:00
committer John Doe <john@example.com> 2026-02-24T12:00:00+08:00
hostname laptop-2024

Backup after system upgrade
---
d4e5f6a7b8c9012... /home/user/documents/file.txt
c3d4e5f6a7b8901... /home/user/photos/img.jpg
a1b2c3d4e5f6789... /home/user/documents/my report.pdf
e5f6a7b8c9d0123... /home/user/文档/照片.jpg
```

Note: Paths with spaces (`my report.pdf`) and UTF-8 characters (`文档/照片.jpg`) are fully supported.

This commit would be named by its SHA-256 hash, e.g.:
`.noahsark/objects/a1/b2c3d4e5f6789...`

**HEAD reference:**
`.noahsark/HEAD` contains the current commit's SHA-256 hash (64-char hex + newline).

---

## 4. Chunking: Fixed-size 16 MiB

Files are split into **fixed 16 MiB (16777216 bytes)** chunks.

```
File (42 MiB):
  [chunk 0: 16 MiB] [chunk 1: 16 MiB] [chunk 2: 10 MiB]
                                       ↑ last chunk: actual size, no padding
```

**Properties:**
- **Fixed size:** 16777216 bytes (16 MiB) for all full chunks
- **Last chunk:** actual remaining bytes (no padding to 16 MiB)
- **chunk_hash** = SHA-256(actual chunk bytes)
- **Simple & fast** — no rolling hash computation, no padding overhead
- **Predictable** — chunk count = ceil(filesize / 16777216)
- **Easy restore** — byte offset X → chunk index = floor(X / 16777216)
- **Random access** — can seek to any chunk without reading prior chunks

**Padding policy: NONE**
- Last chunk stores **only actual file bytes** (e.g., 10 MiB for a 42 MiB file)
- Hash computed on actual data (no zero padding or artificial fill)
- Blob object records actual chunk length in the `length` field
- Saves storage space (no padding, no wasted bytes)

**Example:**
```
File: 42 MiB (44040192 bytes)
  chunk 0: bytes 0-16777215   (16 MiB)  → SHA-256(16 MiB data)
  chunk 1: bytes 16777216-33554431 (16 MiB) → SHA-256(16 MiB data)
  chunk 2: bytes 33554432-44040191 (10 MiB) → SHA-256(10 MiB data)
                                     ↑ NO padding
```

**Why 16 MiB?**
- ✅ **Small index** — 10 TB → ~655k chunks vs 10M chunks (1 MiB)
- ✅ **Fast restore** — fewer seeks on slow optical media (~150ms seek time)
- ✅ **Memory efficient** — 16 MiB per chunk still manageable
- ✅ **Optimal for large files** — VM images, videos, database dumps
- ✅ **Acceptable for small files** — 10 MB file = 1 chunk (no padding waste)

**Trade-offs:**
- ✅ Simple implementation, fast processing
- ✅ Minimal index overhead
- ✅ No padding overhead (saves space)
- ✅ No configuration complexity (one size fits all)
- ❌ Lower deduplication ratio for modified files (vs content-defined chunking)

**Design decision:** Chunk size is **fixed at 16 MiB and not configurable**.
This eliminates complexity and ensures all NoahsArk repositories are compatible.


## 5. Staging Strategy and Multi-Session Support

### 5.1 Basic Staging

When staging objects for disc burning, NoahsArk selects unstaged objects and organizes
them into disc sessions using the following approach:

1. Collect all objects not yet allocated to a disc from `.noahsark/objects/`
2. Sort by type priority: commits and trees first (small, needed for restore), then blobs and chunks
3. Select objects greedily until the disc's remaining capacity is reached
4. Copy selected objects to `.noahsark/staged/<disc_id>-s<session>/NOAHSARK/objects/` preserving the hash-based directory structure
5. Update the global index to record which objects are on which disc session

**Note:** Since chunks are fixed at 16 MiB and discs are typically 23+ GiB (BD-25) or larger,
individual chunks will never exceed disc capacity. Files larger than one disc are
handled by spreading their chunks across multiple disc sessions.

### 5.2 Multi-Session Support (Incremental Disc Burning)

**Goal**: Fill a disc gradually over multiple burn operations (sessions) until capacity is reached.

#### Session Lifecycle

```
Session 1: New disc (growisofs -Z)
  → Disc status: open, used: 12 GB / 23 GB
  → Can add more sessions

Session 2: Continue disc (growisofs -M)
  → Disc status: open, used: 18 GB / 23 GB
  → Can add more sessions

Session 3: Continue disc (growisofs -M)
  → Disc status: full, used: 23 GB / 23 GB
  → No more sessions possible

(Optional) Manual close:
  noahsark disc close <disc_id>
  → Disc status: closed (even if space remains)
```

#### Multi-Session Workflow

```bash
# Session 1: First burn (new disc)
noahsark stage --size=BD-25
# → Allocates new disc: 20260224-001-BD25
# → Creates: .noahsark/staged/20260224-001-BD25-s1/
# → Packed: 12 GB, Remaining: 11 GB

noahsark burn 20260224-001-BD25-s1 /dev/sr0
# → Burns session 1 with: growisofs -Z /dev/sr0 ...
# → Updates discs/20260224-001-BD25.json: session 1 burned

# Session 2: Continue same disc (weeks later)
noahsark commit -m "More backups"
noahsark stage --disc=20260224-001-BD25
# → Reuses disc: 20260224-001-BD25
# → Creates: .noahsark/staged/20260224-001-BD25-s2/
# → Packed: 6 GB, Remaining: 5 GB

noahsark burn 20260224-001-BD25-s2 /dev/sr0
# → Burns session 2 with: growisofs -M /dev/sr0 ...
# → Updates discs/20260224-001-BD25.json: session 2 burned

# Session 3: Final session (disc nearly full)
noahsark commit -m "Final batch"
noahsark stage --disc=20260224-001-BD25
# → Creates: .noahsark/staged/20260224-001-BD25-s3/
# → Packed: 5 GB, Remaining: 0 GB
# → Disc marked as "full"

noahsark burn 20260224-001-BD25-s3 /dev/sr0 --mark-archived
# → Burns final session
# → Disc status: full (automatically closed)
```

#### No-Overwrite Guarantee

**Critical safety rule**: Objects are **never duplicated** within the same disc.

When staging for an existing disc (`--disc=<disc_id>`):
1. Load disc metadata: `.noahsark/discs/<disc_id>.json`
2. Read all previous sessions: which objects are already on this disc
3. **Filter out** objects already on disc from selection
4. Only stage objects **not yet on this disc**
5. This prevents:
   - ❌ Same object in multiple sessions on one disc
   - ❌ Wasted disc space
   - ❌ Index conflicts

**Implementation note**: The global index tracks `(object_hash, disc_id, session)` tuples.
Before staging, query: `SELECT session FROM index WHERE hash=? AND disc_id=?`
If result exists → skip this object for this disc.

#### Session Metadata Tracking

Each session is tracked in two places:

**1. Disc metadata** (`.noahsark/discs/<disc_id>.json`):
```json
{
  "disc_id": "20260224-001-BD25",
  "label": "Project Backup Q1",
  "type": "BD-25",
  "capacity": 25025314816,
  "status": "open",
  "sessions": [
    {
      "session": 1,
      "created": "2026-02-24T12:00:00+08:00",
      "commit": "a1b2c3d4e5f6...",
      "objects": 5432,
      "bytes": 12884901888,
      "burned": true,
      "burned_at": "2026-02-24T12:30:00+08:00"
    },
    {
      "session": 2,
      "created": "2026-03-01T09:00:00+08:00",
      "commit": "f6e5d4c3b2a1...",
      "objects": 2345,
      "bytes": 6442450944,
      "burned": true,
      "burned_at": "2026-03-01T09:15:00+08:00"
    },
    {
      "session": 3,
      "created": "2026-03-10T14:00:00+08:00",
      "commit": "b2c3d4e5f6a7...",
      "objects": 1890,
      "bytes": 5368709120,
      "burned": false,
      "burned_at": null
    }
  ],
  "total_objects": 9667,
  "total_bytes": 24696061952,
  "remaining_bytes": 329252864
}
```

**2. On-disc session metadata** (`NOAHSARK/disc.json` on each session):
```json
{
  "disc_id": "20260224-001-BD25",
  "label": "Project Backup Q1",
  "type": "BD-25",
  "session_id": 2,
  "created": "2026-03-01T09:00:00+08:00",
  "commit": "f6e5d4c3b2a1...",
  "object_count": 2345,
  "session_bytes": 6442450944,
  "cumulative_bytes": 19327352832,
  "previous_sessions": [1]
}
```

#### Session Status States

| Status | Meaning | Can Stage? | Can Burn? | Can GC? |
|--------|---------|------------|-----------|---------|
| **open** | Has remaining capacity | ✅ Yes | ✅ Yes | ❌ No |
| **full** | Reached capacity limit | ❌ No | ❌ No | ✅ Yes |
| **closed** | Manually closed by user | ❌ No | ❌ No | ✅ Yes |
| **error** | Burn failed, needs retry | ✅ Yes (same session) | ✅ Yes | ❌ No |

#### UDF Multi-Session Technical Details

**growisofs behavior**:
- `-Z device`: Create new filesystem (session 1)
  - Writes UDF anchor at beginning
  - Creates new volume descriptor sequence
  - First session is always -Z

- `-M device`: Append to existing filesystem (session 2+)
  - Reads existing UDF metadata
  - Appends new files/directories
  - Updates anchor and file set descriptors
  - **Does NOT overwrite existing data**
  - Each session is write-once, append-only

**Important**: UDF multi-session is append-only. Once session N is burned,
its data is immutable. Session N+1 adds new data without touching session N.

#### Capacity Management

Remaining capacity calculation:
```
remaining = disc_capacity - sum(session.bytes for session in sessions) - safety_margin
```

Safety margin accounts for:
- UDF session linking overhead (~10-50 MB per session)
- File descriptor overhead
- Anchor volume descriptor updates
- Conservative: reserve 100 MB for sessions 2+

**Example (BD-25, 23.28 GiB usable)**:
```
Session 1: 12.0 GB → Remaining: 11.28 GB
Session 2:  6.0 GB → Remaining:  5.28 GB (minus 100 MB overhead = 5.18 GB)
Session 3:  5.0 GB → Remaining:  0.18 GB (too small, disc marked "full")
```

#### Multi-Session Best Practices

1. **Plan session sizes**: Aim for 2-4 sessions per disc (not 20+ tiny sessions)
2. **Session overhead**: Each session adds ~10-50 MB UDF overhead
3. **Burn verification**: Verify each session before adding next
4. **Keep discs accessible**: Need physical disc to add more sessions
5. **Close when done**: Manually close disc if you won't add more sessions

### 5.3 Cross-Disc Scenarios

NoahsArk handles three common cross-disc scenarios:

#### Scenario 1: Multiple Commits per Disc (Multi-Session)

**Situation**: A disc with multiple sessions, each containing objects from different commits.

```
Disc: 20260224-001-BD25
├─ Session 1 (12 GB): commit a1b2c3d4 (2026-02-24)
├─ Session 2 (6 GB):  commit f6e5d4c3 (2026-03-01)
└─ Session 3 (5 GB):  commit b2c3d4e5 (2026-03-10)
```

**How it works**:
- Each session references its commit SHA-256 in `disc.json`
- Global index tracks: `(object_hash, disc_id, session, commit)`
- Restoring commit a1b2c3d4 reads only from session 1
- Restoring commit b2c3d4e5 may need sessions 1+2+3 (parent chain)

**Example workflow**:
```bash
# Week 1: First backup
noahsark commit -m "Week 1 backup"
noahsark stage --size=BD-25
noahsark burn 20260224-001-BD25-s1 /dev/sr0
# → Disc has 11 GB remaining

# Week 2: Add more to same disc
noahsark commit -m "Week 2 backup"
noahsark stage --disc=20260224-001-BD25
noahsark burn 20260224-001-BD25-s2 /dev/sr0
# → Disc has 5 GB remaining

# Week 3: Final session
noahsark commit -m "Week 3 backup"
noahsark stage --disc=20260224-001-BD25
noahsark burn 20260224-001-BD25-s3 /dev/sr0
# → Disc full
```

#### Scenario 2: One Commit Spanning Multiple Discs

**Situation**: A single commit has too many objects to fit on one disc.

```
Commit: a1b2c3d4 (50 GB of new data)
├─ Disc 1: 23 GB of chunks/blobs
├─ Disc 2: 23 GB of chunks/blobs
└─ Disc 3: 4 GB of chunks/blobs
```

**How it works**:
- All objects reference the same commit SHA-256
- Global index distributes objects across discs:
  ```
  chunk_001 → disc-001, session 1, commit a1b2c3d4
  chunk_002 → disc-001, session 1, commit a1b2c3d4
  chunk_003 → disc-002, session 1, commit a1b2c3d4
  ...
  ```
- Restoring requires: "Insert disc 1, then disc 2, then disc 3"

**Example workflow**:
```bash
# Large backup (50 GB)
noahsark commit -m "Huge backup"

# Stage for disc 1
noahsark stage --size=BD-25
# → Packed: 23 GB, Pending: 27 GB, Disc: 20260224-001-BD25

noahsark burn 20260224-001-BD25-s1 /dev/sr0 --mark-archived

# Stage for disc 2 (automatically allocates new disc)
noahsark stage --size=BD-25
# → Packed: 23 GB, Pending: 4 GB, Disc: 20260224-002-BD25

noahsark burn 20260224-002-BD25-s1 /dev/sr0 --mark-archived

# Stage for disc 3
noahsark stage --size=BD-25
# → Packed: 4 GB, Pending: 0 GB, Disc: 20260224-003-BD25

noahsark burn 20260224-003-BD25-s1 /dev/sr0 --mark-archived

# All objects for commit a1b2c3d4 now on discs 1-3
```

**Restore workflow**:
```bash
noahsark restore a1b2c3d4 /restore/path

# NoahsArk will prompt:
# "Insert disc: 20260224-001-BD25"
# (user inserts disc 1)
# "Reading 23 GB from disc 1..."
#
# "Insert disc: 20260224-002-BD25"
# (user inserts disc 2)
# "Reading 23 GB from disc 2..."
#
# "Insert disc: 20260224-003-BD25"
# (user inserts disc 3)
# "Reading 4 GB from disc 3..."
#
# "Restore complete: 50 GB"
```

#### Scenario 3: Single File Exceeding Disc Capacity

**Situation**: A single file (e.g., 50 GB video or VM image) larger than BD-25 (23 GB).

```
File: /data/large-vm.qcow2 (50 GB)
├─ Chunk 0-1399 (22.4 GB) → Disc 1
├─ Chunk 1400-2799 (22.4 GB) → Disc 2
└─ Chunk 2800-3124 (5.2 GB) → Disc 3
```

**How it works**:
- File is chunked into 16 MiB pieces (50 GB ≈ 3,125 chunks)
- Blob object lists all 3,125 chunk hashes
- Chunks distributed across multiple discs by staging algorithm
- Index tracks each chunk location:
  ```
  chunk_hash_0000 → disc-001
  chunk_hash_0001 → disc-001
  ...
  chunk_hash_1400 → disc-002
  ...
  chunk_hash_2800 → disc-003
  ```

**Example workflow**:
```bash
# Add 50 GB file
noahsark commit -m "Add large VM image"

# Stage automatically splits across discs
noahsark stage --size=BD-25
# → Disc 1: chunks 0-1399

noahsark stage --size=BD-25
# → Disc 2: chunks 1400-2799

noahsark stage --size=BD-25
# → Disc 3: chunks 2800-3124

# Burn all three discs
noahsark burn <disc-1> /dev/sr0 --mark-archived
noahsark burn <disc-2> /dev/sr0 --mark-archived
noahsark burn <disc-3> /dev/sr0 --mark-archived
```

**Restore workflow**:
```bash
noahsark restore <commit> /data/large-vm.qcow2 /restore/

# NoahsArk reassembles file:
# 1. Load blob object: 3,125 chunks listed
# 2. Query index for each chunk location
# 3. Group by disc: chunks 0-1399 (disc 1), 1400-2799 (disc 2), etc.
# 4. Prompt for discs in order
# 5. Stream chunks from disc → reassemble file
# 6. Verify Merkle root after complete
```

**Important notes**:
- **No file size limit**: Files can be arbitrarily large
- **Chunk-level deduplication**: Identical 16 MiB chunks stored once
- **Merkle verification**: Entire file verified after reassembly
- **Disc order**: Chunks requested in optimal disc order (minimize disc swaps)

#### Cross-Disc Index Queries

Global index schema supports cross-disc queries:

```sql
-- Find all discs needed for a commit
SELECT DISTINCT disc_id FROM index_entries
WHERE hash IN (SELECT blob_hash FROM commit WHERE commit_hash = ?);

-- Find all discs needed for a file
SELECT DISTINCT disc_id FROM index_entries
WHERE hash IN (SELECT chunk_hash FROM blob WHERE blob_hash = ?);

-- Count objects per disc
SELECT disc_id, COUNT(*) FROM index_entries GROUP BY disc_id;
```

#### Staging Algorithm for Cross-Disc Commits

When staging a large commit across multiple discs:

1. **Collect unstaged objects** from `.noahsark/objects/`
2. **Sort by priority**: commits/trees first, then blobs, then chunks
3. **Greedy bin-packing**: fill current disc until capacity reached
4. **Allocate next disc**: if objects remain, allocate new disc_id
5. **Repeat**: until all objects staged
6. **Update index**: record `(object_hash, disc_id, session)` for each object

**Result**: Commit objects naturally distributed across discs based on capacity.

#### Restore Prompts and Disc Swapping

When restoring data spanning multiple discs, NoahsArk provides clear prompts:

```
Restoring commit: a1b2c3d4e5f6...
Date: 2026-02-24 12:00:00
Files: 1,234 files, 50 GB

Required discs:
  1. 20260224-001-BD25 (23 GB, 1,432 chunks)
  2. 20260224-002-BD25 (23 GB, 1,432 chunks)
  3. 20260224-003-BD25 (4 GB, 261 chunks)

Insert disc 1/3: 20260224-001-BD25
Press Enter when ready...

[User inserts disc]

Reading from disc 1... [████████████████] 23 GB / 23 GB

Insert disc 2/3: 20260224-002-BD25
Press Enter when ready...

[continues...]
```

**Optimization**: NoahsArk orders chunk requests to minimize disc swaps:
- Read all needed chunks from disc 1 before requesting disc 2
- Never asks for same disc twice (reads everything needed in one pass)

**Disc capacity considerations:**
- Nominal capacities assume perfect media, but real-world discs vary
- UDF filesystem overhead: ~1-2% for metadata (anchor descriptors, file sets, ICBs)
- Safety margin: 3-5% reserved to avoid write errors near disc edge
- NoahsArk uses conservative targets accounting for both factors

---

## 6. Per-File Merkle Tree (BitTorrent v2 Style)

Each file gets a Merkle tree for hierarchical integrity verification.

### Construction

- **Leaf size**: 16 KiB blocks, SHA-256 of each block
- **Tree**: binary tree; `internal_hash = SHA-256(left || right)`
 - **Leaf size**: 16 KiB blocks, SHA-256 of each block
 - **Tree**: binary tree; `internal_hash = SHA-256(left || right)`
 - **Padding**: BEP52 (BitTorrent v2) style implicit zero-hash padding is the
   canonical default. In BEP52, missing leaves up to the next power-of-two are
   treated as the deterministic zero-hash values derived from SHA-256; these
   implicit zero-hash nodes do not need to be physically stored on disc.
 - **Root**: stored as `merkle_root` in the blob object (hex, 64 chars)

Implementation note:
- Implementations SHOULD compute 16 KiB leaf hashes and 16 MiB chunk hashes in
  a single streaming pass: as the file is read, hash each 16 KiB block for the
  Merkle layer and concurrently accumulate bytes to compute each 16 MiB chunk
  hash. When generating/validating proofs, reconstruct implicit zero-hash
  contributions per BEP52 as required.

### Verification Levels

1. **Full file**: verify all leaves → root matches blob's `merkle_root`
2. **Single block**: verify any 16 KiB block without reading the entire file
3. **Disc-level verification** (Phase 2+): verify hashes stored in disc metadata without needing other discs

This enables restoring and verifying files at multiple granularities, from individual
blocks up to full files.

---

## 7. Repository Layout

### 7.1 Local Index (`.noahsark/`)

```
.noahsark/
├── config                      # JSON repository configuration
├── HEAD                        # current commit SHA-256 (64-char hex + newline)
├── objects/                    # content-addressed objects (blobs, trees, commits, chunks)
│   └── XX/YYYY...              # 2-char prefix / remainder (all objects, including commits)
├── index.db                    # SQLite global index (hash → disc_id + object_path + length, ...)
├── bloom.bin                   # optional bloom filter for fast dedup check
├── discs/                      # disc session registry
│   ├── 20260224-001-BD25.json  # disc metadata: label, sessions, capacity, status
│   ├── 20260224-002-BD50.json
│   └── 20260310-001-BD25.json
└── staged/                     # staged disc image directories (ready to burn)
    ├── 20260224-001-BD25-s1/
    │   └── NOAHSARK/           # UDF directory tree
    │       ├── disc.json
    │       ├── manifest.json
    │       └── objects/
    └── 20260224-001-BD25-s2/
        └── NOAHSARK/
```

**Key changes from timestamp-based design:**
- ❌ Removed `commits/` directory — commits are now stored in `objects/` like all other objects
- ✅ `HEAD` now contains commit SHA-256 instead of timestamp
- ✅ All objects (including commits) are content-addressed and stored uniformly
- ✅ Commit history is traversed via parent pointers, not directory listings

The `staged/` directories remain until manually cleaned with `noahsark stage gc`
(recommended after successful burn + verify).

### 7.2 Disc Capacity Calculation

NoahsArk uses conservative disc capacity targets to account for real-world variability
and filesystem overhead.

#### Nominal vs Usable Capacity

| Media Type | Nominal | Actual Bytes | UDF Overhead | Safety Margin | Usable Target |
|------------|---------|--------------|--------------|---------------|---------------|
| **BD-25**  | 25 GB   | 25,025,314,816 | ~1.5% (375 MB) | 5% (1.25 GB) | **23.28 GiB** |
| **BD-50**  | 50 GB   | 50,050,629,632 | ~1.5% (750 MB) | 5% (2.50 GB) | **46.55 GiB** |
| **BD-100** | 100 GB  | 100,103,356,416 | ~1.5% (1.5 GB) | 5% (5.00 GB) | **93.11 GiB** |
| **BD-128** | 128 GB  | 128,000,000,000 | ~1.5% (1.9 GB) | 5% (6.40 GB) | **118.86 GiB** |

**Why conservative targets?**
1. **UDF filesystem overhead** (~1-2%):
   - Anchor volume descriptor (multiple sectors)
   - File set descriptors and ICBs (Information Control Blocks)
   - Directory structures and file entries
   - Metadata partition and allocation tables

2. **Safety margin** (3-5%):
   - Manufacturing tolerances: actual disc capacity varies ±2%
   - Write strategy overhead: some drives reserve space for calibration
   - Edge effects: outer tracks may be less reliable on cheap media
   - Multi-session overhead: session linking requires additional descriptors

3. **Real-world experience**:
   - Burning to 100% capacity often fails on cheaper media
   - 95% capacity is a safer target across drive/media combinations
   - Better to underestimate than fail a 2-hour BD-100 burn

**Custom sizes**: When using `--size=<N>G`, the specified size is used as-is
(user is responsible for safety margin).

### 7.3 Config (`config`)

```json
{
  "repo_id":           "<uuid4>",
  "version":           1,
  "chunk_size":        16777216,
  "hash_algo":         "sha256",
  "merkle_padding":   "bep52",    
  "compression":       "none"
}
```

**Configuration fields:**
- `chunk_size`: **fixed at 16777216 (16 MiB)** — recorded but not configurable
- `hash_algo`: **fixed at "sha256"** — recorded but not configurable
- `compression`: "none" (Phase 1), "zstd" (Phase 2+)
-- `fec_*`: removed — NoahsArk does not use FEC by default. Prefer duplicating discs for redundancy.

**Design rationale:**
Both `chunk_size` and `hash_algo` are **immutable** after repository initialization.
This eliminates complexity while maintaining forward compatibility:

- ✅ All NoahsArk repositories use the same chunk size (16 MiB)
- ✅ All NoahsArk repositories use SHA-256 hashing
- ✅ Simple implementation (no per-blob metadata tracking)
- ✅ Guaranteed interoperability across all NoahsArk instances
- ✅ Config records the values for documentation and future-proofing

**If migration is needed:**
If chunk size or hash algorithm must change (e.g., SHA-256 is compromised):
1. Create a new repository with new settings
2. Use `noahsark migrate old-repo/ new-repo/` to re-chunk all data
3. Burn new optical discs with the migrated repository
4. Old repository remains readable by older NoahsArk versions

### 7.4 Global Index (`.noahsark/index.db`)

The global index is stored as a single SQLite database `index.db` (in the
local repository `.noahsark/index.db` or on-disc as `NOAHSARK/index.db`). This
file provides fast, ACID-backed lookups for object locations.

Example SQLite schema (DDL):

```sql
-- index.db schema: user_version = 1
CREATE TABLE index_entries (
  hash TEXT PRIMARY KEY,       -- SHA-256 hex (64 chars)
  type TEXT NOT NULL,         -- chunk|blob|tree|commit
  disc_id TEXT,               -- which disc contains the object
  object_path TEXT,           -- path on disc (e.g. NOAHSARK/objects/XX/YYYY...)
  offset INTEGER,             -- reserved (NULL for direct-object storage)
  length INTEGER NOT NULL,    -- byte length of stored object
  created TEXT,               -- RFC3339 when this index entry was recorded
  merkle_padding TEXT         -- merkle rule used when applicable (e.g. 'bep52')
);
CREATE INDEX IF NOT EXISTS idx_type ON index_entries(type);
CREATE INDEX IF NOT EXISTS idx_disc ON index_entries(disc_id);
PRAGMA user_version = 1;
```

Notes:
- `object_path` points to the object file on disc: `NOAHSARK/objects/XX/YYYY...`
- `offset` field is reserved (NULL) for direct-object storage.
- `merkle_padding` records the per-repository Merkle padding rule (see config).
- `index.db` replaces per-hash JSON index snapshots; keep `bloom.bin` for a
  compact probabilistic negative lookup if desired.

Operations:
- `noahsark index consolidate` now updates or rebuilds `index.db` (importing
  any ephemeral snapshots or disc-imported index data) and performs optional
  optimizations (VACUUM, reindex) for fast lookups.

Bloom filter (`bloom.bin`): optional file used for very-fast negative lookups
before touching `index.db`.

### 7.5 On-Disc Layout (UDF 2.50)

```
NOAHSARK/
├── disc.json                   # this disc's metadata: disc_id, label, session info
├── manifest.json               # snapshot of global index (disaster recovery)
├── index.db                    # SQLite global index (fast local lookup)
├── bloom.bin                   # optional bloom filter for fast negative lookups
├── objects/                    # content-addressed object files stored by hash prefix
│   └── XX/YYYY...
└── (no FEC files)              # FEC not used; duplicate discs recommended
```

**disc.json** example:
```json
{
  "disc_id": "20260224-001-BD25",
  "label": "Project Backup - 2026 Q1",
  "type": "BD-25",
  "session_id": 1,
  "created": "2026-02-24T12:00:00+08:00",
  "commit": "a1b2c3d4e5f6789...",
  "object_count": 12345
}
```

**Key fields:**
- `commit`: SHA-256 of the latest commit included on this disc
  - Used for tracking which snapshot this disc represents
  - Enables partial restore from a single disc
  - Replaces timestamp-based commit tracking

**Phase 2+ optional field:**
- `piece_layer`: (Optional) Disc-level verification hashes for per-disc integrity checking
  - Not stored in blob objects (blobs are content-addressed and disc-independent)
  - Computed at staging/burn time when disc capacity and contents are known
  - Example: `{"capacity_bytes": 25025314816, "pieces": [{"sha256": "...", "objects": ["hash1", "hash2"]}, ...]}`
  - Enables verification of a single disc without needing other discs or full files

**manifest.json:** A copy of the global index at the time of burning, allowing the
repository to be reconstructed by scanning a single disc if the local `.noahsark/` is lost.
When present, `index.db` is a SQLite copy of the index for fast local lookups; `bloom.bin`
is optional but recommended to simplify import and recovery.

### Object lifecycle and allocation

NoahsArk manages object lifecycle in three states: **loose** → **staged** → **archived**.

#### Object States

1. **Loose** (`.noahsark/objects/`):
   - Newly created objects from `noahsark commit`
   - Not yet allocated to any disc
   - Available for deduplication
   - Taking up local disk space

2. **Staged** (`.noahsark/staged/<disc_id>-s<N>/NOAHSARK/objects/`):
   - Copied from loose objects during `noahsark stage`
   - Ready for burning but not yet on physical media
   - Still consuming local disk space (loose + staged copy)

3. **Archived** (on physical disc):
   - Successfully burned and verified on optical media
   - Tracked in global index with `disc_id` location
   - **Loose objects can now be safely deleted**

#### Lifecycle Workflow

```
noahsark commit
  → Creates loose objects in .noahsark/objects/

noahsark stage --disc=<disc_id>
  → Copies objects to .noahsark/staged/<disc_id>-s<N>/
  → Loose objects remain (not deleted yet)

noahsark burn <staged-dir> /dev/sr0
  → Burns staged directory to physical disc
  → Verifies written data

noahsark burn --mark-archived <staged-dir>
  → Updates index: marks objects as archived on disc
  → Enables GC to delete loose objects

noahsark gc
  → Scans .noahsark/objects/
  → For each object: check if archived on any disc
  → If archived: mark for deletion
  → If not archived: keep (needed for recovery)
  → Optionally: delete staged/ directories for burned discs
```

#### Safe Deletion Rules

An object in `.noahsark/objects/` can be deleted if:
1. ✅ It exists on at least one burned and verified disc
2. ✅ The disc is marked as `status: closed` (will not be re-burned)
3. ✅ The global index records the disc location

An object MUST NOT be deleted if:
- ❌ It's only in `staged/` (not yet burned)
- ❌ The disc is marked as `status: open` (may be re-staged)
- ❌ Verification failed (data may be corrupt)

#### GC Command

```bash
noahsark gc [--dry-run] [--aggressive]

Options:
  --dry-run       Show what would be deleted without deleting
  --aggressive    Also delete staged/ directories for closed discs

Behavior:
  1. Scan all loose objects in .noahsark/objects/
  2. For each object:
     a. Query index: is this object on any disc?
     b. Check disc status: is the disc closed?
     c. If yes to both: safe to delete
  3. Report space to be reclaimed
  4. Delete loose objects (unless --dry-run)
  5. If --aggressive: delete staged/ dirs for closed discs

Safety:
  - Never deletes objects needed for open discs
  - Never deletes objects not yet on any disc
  - Always checks index before deletion
```

#### Example Workflow

```bash
# Create backup
noahsark commit -m "Backup 2026-02-24"
# → 50 GB of new objects in .noahsark/objects/

# Stage for disc
noahsark stage --size=BD-50
# → 50 GB copied to .noahsark/staged/20260224-001-BD50-s1/
# → Still 50 GB in .noahsark/objects/ (100 GB total used)

# Burn to disc
noahsark burn 20260224-001-BD50-s1 /dev/sr0
# → Disc successfully burned and verified
# → Index updated: objects now on disc-001

# Mark as archived (optional explicit step)
noahsark burn --mark-archived 20260224-001-BD50-s1
# → Disc marked as closed in discs/20260224-001-BD50.json

# Reclaim space
noahsark gc
# → Deletes 50 GB from .noahsark/objects/ (safe: on disc)
# → 50 GB remains in .noahsark/staged/ (can be deleted with --aggressive)

noahsark gc --aggressive
# → Also deletes .noahsark/staged/20260224-001-BD50-s1/ (50 GB freed)
# → Total: 100 GB reclaimed, only disc copy remains
```

#### Recovery Safety

Even after GC, objects can be recovered:
```bash
noahsark import /mnt/disc-001
# → Scans NOAHSARK/objects/ on mounted disc
# → Updates index with object locations
# → Objects can now be restored from disc
```

This lifecycle management ensures:
- ✅ Local disk usage is minimized after burning
- ✅ Objects are never deleted until safely on disc
- ✅ Recovery is always possible from discs alone
- ✅ User controls when to reclaim space (explicit GC)

---

## 8. fsnotify / inotify Watcher

`noahsark watch` runs as a daemon to auto-commit changes.

```
noahsark watch [source-dir] [--debounce=30s] [--auto-stage=false] [--disc=BD-25]
```

### Behavior

1. Register fsnotify watcher on source dir; recursively add all subdirectories
2. Handle `Create`, `Write`, `Remove`, `Rename` events
3. New subdirectories: automatically add to watcher
4. **Debounce**: collect events for `--debounce` duration before triggering commit
5. On trigger: run incremental `noahsark commit` for changed paths
6. If `--auto-stage` and staging area reaches disc capacity: automatically run `noahsark stage`
7. Log all events and commits to `.noahsark/watch.log`

### Limitations

- Non-recursive natively; subdirs added manually on creation
- Does not watch NFS/SMB mounts
- Linux: subject to `/proc/sys/fs/inotify/max_user_watches` limit

---

## 9. CLI Commands

```
noahsark init [dir]
    Create .noahsark/ in dir (default: cwd). Generate repo UUID and gear seed.

noahsark commit [dir]
    Chunk files in dir, dedup against index, write objects, create commit, update HEAD.
    -m, --message   optional commit message

    Process:
      1. Scan directory and compute file hashes
      2. Chunk changed files (16 MiB fixed-size)
      3. Dedup: bloom filter check → index check → skip if exists
      4. Write new chunks/blobs/trees to .noahsark/objects/
      5. Read HEAD → get parent commit SHA-256
      6. Create commit object with parent pointer
      7. Calculate commit SHA-256 (content-addressed)
      8. Write commit to .noahsark/objects/XX/YY...
      9. Update HEAD with new commit SHA-256

    The commit creates a snapshot of the entire directory tree, with parent
    pointer forming a linear history chain.

noahsark stage [output-dir] [flags]
    Generate a disc image directory from unstaged objects (ready for growisofs).
    --size=<preset|size>    disc capacity presets or custom size
                            presets: BD-25, BD-50, BD-100, BD-128 (default: BD-25)
                            custom: <number><unit> (e.g., 20G, 500M, 1T)
                            units: K, M, G, T (base-2: KiB, MiB, GiB, TiB)

    Preset capacities (usable space after UDF overhead + 5% safety margin):
      BD-25:  23.28 GiB  (25 GB nominal, single-layer)
      BD-50:  46.55 GiB  (50 GB nominal, dual-layer)
      BD-100: 93.11 GiB  (100 GB nominal, triple-layer BDXL)
      BD-128: 118.86 GiB (128 GB nominal, quad-layer BDXL)

    Examples:
      noahsark stage --size=BD-25        # Use BD-25 preset (23.28 GiB)
      noahsark stage --size=20G          # Custom 20 GiB
      noahsark stage --size=500M         # Custom 500 MiB (testing)
      noahsark stage --size=1T           # Custom 1 TiB (future large media)
    --disc=<disc_id>        continue filling an existing open disc session
                            (omit to allocate a new disc_id: YYYYMMDD-NNN-TYPE)
    --label=<text>          optional label for new discs (for disc list display)

    Behavior:
      `noahsark stage` can be executed multiple times. Each run will continue
      packing unstaged objects into the specified `--disc` session (or create
      a new disc if none specified). Staged output directories accumulate on
      disk and wait for an explicit burn. Use `noahsark burn <staged-dir>` to
      burn a specific staged session to physical media.

    Auto-generated output dir: .noahsark/staged/<disc_id>-s<session>/
    Example: .noahsark/staged/20260224-001-BD25-s1/

    Output:
      Writes a directory tree (UDF-compatible layout) with objects, disc.json, manifest.
      Prints a summary:

        Created disc: 20260224-001-BD25
        Label:        "Project Backup - 2026 Q1"
        Output dir:   .noahsark/staged/20260224-001-BD25-s1/

        Packed:       12.4 GiB  (session 1)
        Remaining:    10.9 GiB  → disc is still open
        Pending:       3.2 GiB  unstaged objects remain

        Physical label to write on disc:
        ┌────────────────────────────────┐
        │ 20260224-001-BD25              │
        │ Project Backup - 2026 Q1       │
        │ Session 1                      │
        └────────────────────────────────┘

        Burn command (session 1):
          growisofs -Z /dev/sr0 -udf -udf-unicode -V '20260224-001-BD25' \
            .noahsark/staged/20260224-001-BD25-s1/

        Next steps:
          noahsark stage --disc=20260224-001-BD25   # continue this disc (session 2)
          noahsark stage --size=BD-25               # start a new BD-25 disc
          noahsark stage --size=BD-128              # start a new BD-128 disc
          noahsark stage --size=100G                # start a custom-sized disc

noahsark burn [staged-dir|disc_id] [device] [--mark-archived]
  Wrapper around growisofs for easier burning with multi-session support.

  --mark-archived    After successful burn and verify, mark disc as closed
                     and update index so objects can be GC'd

    Session 1 (new disc, fresh burn):
      noahsark burn .noahsark/staged/20260224-001-BD25-s1/ /dev/sr0

      Executes:
        growisofs -Z /dev/sr0 \
          -udf \
          -allow-limited-size \
          -input-charset utf8 \
          -V '20260224-001-BD25' \
          .noahsark/staged/20260224-001-BD25-s1/NOAHSARK/

      -Z: Create new UDF filesystem (session 1 only)
      Note: -allow-limited-size is required for BD-50/BD-100/BD-128 (bypasses ISO 9660 size limits)

    Session 2+ (append to existing disc):
      noahsark burn .noahsark/staged/20260224-001-BD25-s2/ /dev/sr0

      Detects session number from directory name (s2, s3, etc.)

    You may also pass a `disc_id` instead of the staged-dir; the CLI will
    locate the matching staged session directory (most-recent open session)
    and burn that session. This makes it easy to invoke `noahsark burn <disc_id>`
    when you prefer identifying discs by ID instead of path.

      Executes:
        growisofs -M /dev/sr0 \
          -udf \
          -allow-limited-size \
          -input-charset utf8 \
          .noahsark/staged/20260224-001-BD25-s2/NOAHSARK/

    Options explained:
      -udf                  UDF 2.50 filesystem (Blu-ray standard, all modern OS support)
      -allow-limited-size   Allow BD-50/BD-100 sized images (bypass ISO 9660 limits)
      -input-charset utf8   Unicode filenames (UTF-8 support for all languages)
      -V                    Volume label (disc ID shown when mounted)

    Automatically detects session number from directory name.
    Verifies disc capacity before burning.

noahsark test-burn [staged-dir] [output.iso]
    Generate an ISO file from a staged directory for testing/verification.
    Does NOT require a physical optical drive.

    Example:
      noahsark test-burn .noahsark/staged/20260224-001-BD25-s1/ test.iso

      Executes:
        genisoimage \
          -udf \
          -allow-limited-size \
          -input-charset utf8 \
          -V '20260224-001-BD25' \
          -o test.iso \
          .noahsark/staged/20260224-001-BD25-s1/NOAHSARK/

    Options explained:
      -udf                  UDF 2.50 filesystem (Blu-ray standard, 255-char filenames)
      -allow-limited-size   Allow large images (BD-50/BD-100/BD-128 up to 128GB)
      -input-charset utf8   Preserve Unicode filenames (UTF-8 support)
      -V                    Volume label (shown when mounted)
      -o                    Output ISO file path

    Use cases:
      - CI/CD testing without physical media
      - Verify objects and manifest before burning
      - Test restore workflow without wasting a disc
      - Validate disc image size fits target media

    Complete testing workflow:

      1. Stage objects for disc:
         noahsark stage --size=BD-25
         → Creates .noahsark/staged/20260224-001-BD25-s1/

      2. Generate test ISO:
         noahsark test-burn .noahsark/staged/20260224-001-BD25-s1/ test.iso
         → Creates test.iso (can be large, e.g., 23 GB for BD-25)

      3. Mount ISO for testing:
         sudo mkdir -p /mnt/test-disc
         sudo mount -o loop,ro test.iso /mnt/test-disc
         → ISO mounted at /mnt/test-disc/

      4. Verify disc contents:
         ls -lh /mnt/test-disc/NOAHSARK/
         cat /mnt/test-disc/NOAHSARK/disc.json
         → Check disc.json, objects/, index.db, etc.

      5. Test restore from ISO:
         noahsark restore <commit-sha> /path/to/restore
         → NoahsArk will read objects from mounted ISO via index

      6. Verify restored files:
         diff -r /original/path /path/to/restore
         → Should be identical

      7. Unmount ISO:
         sudo umount /mnt/test-disc

      8. If all tests pass, burn to real disc:
         noahsark burn 20260224-001-BD25-s1 /dev/sr0 --mark-archived

    Notes:
      - ISO size matches staged directory size
      - Mount as read-only (-o ro) to simulate real optical disc
      - NoahsArk can read directly from mounted ISO via index
      - Testing with ISO is much faster than burning and reading real disc

noahsark gc [--dry-run] [--aggressive]
    Garbage collect loose objects that are safely archived on discs.
    --dry-run       Show what would be deleted without actually deleting
    --aggressive    Also delete staged/ directories for closed/full discs

    Safety rules:
      - Only deletes objects that exist on burned & verified discs
      - Only deletes from discs with status=closed or status=full
      - Never deletes objects still needed for open discs
      - Always checks index before deletion

    Use this after burning discs to reclaim local disk space.

noahsark restore [commit] [src-path] [dest-path]
    Restore src-path as it existed at commit to dest-path.
    commit may be:
      - Full SHA-256 hash (64 chars)
      - Short prefix (min 8 chars, must be unique)
      - HEAD~N notation (N commits back from HEAD)

noahsark log [--count=20] [--oneline]
    Show commit history from HEAD.
    Traverses parent pointers backwards: HEAD → parent → grandparent → ...

    Output format:
      <commit_sha256_short> <author> <date> <message>

    Example:
      a1b2c3d4  John Doe  2026-02-24 12:00:00  Backup after upgrade
      f6e5d4c3  John Doe  2026-02-23 18:30:00  Daily backup
      ...

noahsark verify [--disc=<disc_id>|--iso=<path>|--all] [--level=quick|full]
    Hierarchical verification of disc contents or ISO images.

    Options:
      --disc=<disc_id>    Verify specific disc (must be mounted or in staged/)
      --iso=<path>        Verify specific ISO file (will mount temporarily)
      --all               Verify all available discs
      --level=quick       Quick: verify index + disc.json only (default)
      --level=full        Full: verify all object SHA-256 hashes + Merkle roots

    Verification levels:

      Quick verification (fast, ~seconds):
        1. Check disc.json format and integrity
        2. Check index.db/manifest.json format
        3. Verify object count matches disc.json
        4. Spot-check: random sample of 10 objects

      Full verification (slow, ~minutes for BD-25):
        1. All quick verification steps
        2. Verify SHA-256 of every object against filename
        3. For each blob: verify all chunk hashes
        4. For each blob: recompute and verify Merkle root
        5. For each commit: verify parent pointer chain
        6. Report any corruption or mismatches

    Examples:

      # Quick verify staged ISO before burning
      noahsark verify --iso=test.iso --level=quick

      # Full verify after burning to ensure disc is good
      sudo mount /dev/sr0 /mnt/disc
      noahsark verify --disc=20260224-001-BD25 --level=full
      sudo umount /mnt/disc

      # Verify ISO file thoroughly before burning
      noahsark verify --iso=test.iso --level=full

      # Quick check all mounted discs
      noahsark verify --all --level=quick

    Output format:
      Verifying disc: 20260224-001-BD25
      ✓ disc.json: valid
      ✓ index.db: 12,345 objects
      ✓ Object count: matches
      ✓ Sample verification: 10/10 objects OK

      [Full mode only:]
      ✓ Chunks: 11,645/11,645 verified
      ✓ Blobs: 456/456 verified
      ✓ Merkle roots: 456/456 verified
      ✓ Commits: 10/10 verified

      Result: PASS (no errors)

    Exit codes:
      0  - All checks passed
      1  - Verification failed (corruption detected)
      2  - Disc/ISO not found or not readable

noahsark index consolidate
  Rebuild or optimize the repository `index.db` (import any ephemeral snapshots
  or disc-imported index data) and perform optional maintenance (VACUUM, reindex).

noahsark disc list [--status=open|full|closed|all]
    List all disc sessions with human-readable formatting:

    DISC ID              LABEL                    TYPE     USED / TOTAL       STATUS  SESSIONS
    20260224-001-BD25    Project Backup Q1        BD-25    12.4 / 23.3 GiB    open    1
    20260224-002-BD50    VM Images                BD-50    46.1 / 46.6 GiB    full    3
    20260310-001-BD100   Large Dataset            BD-100   85.2 / 93.1 GiB    open    2
    20260315-001-BD128   Archive 2026 Q1          BD-128  110.5 / 118.9 GiB   closed  4

    --status filter (default: all)

noahsark disc close <disc_id>
    Mark an open disc as closed (will not be used for future iso runs).
    Updates status to "closed" even if space remains.

noahsark disc label <disc_id> <new_label>
    Update the label for an existing disc.

noahsark disc import [mount-point]
    Scan a mounted disc's manifest and objects; register into local index.

noahsark watch [source-dir] [--debounce=30s] [--auto-stage=false] [--size=BD-25]
    Start fsnotify daemon (see §8).
    --size: disc capacity for auto-stage (presets: BD-25, BD-50, BD-100, BD-128, or custom: 20G)
    --auto-stage: when pending objects exceed disc capacity, auto-run noahsark stage.

noahsark cat-object [hash]
    Print the decoded content of any object (debug).
```

---

## 10. Redundancy Strategy

NoahsArk does not use Forward Error Correction (FEC) by default. Instead, for
cross-disc redundancy and simple recovery, the recommended operational practice
is to burn two identical copies of each disc. This keeps the on-disc layout and
indexing simple and avoids introducing FEC complexity in Phase 1.

If you later decide to explore FEC-based strategies, they can be added as an
optional Phase 2 extension, but they are out of scope for the current design.

---

## 11. Go Dependencies

| Module | Purpose |
|--------|---------|
| `github.com/fsnotify/fsnotify` | Cross-platform file system watching (inotify/kqueue) |
| `github.com/klauspost/compress/zstd` | Phase 2+: Per-chunk zstd compression |
| `github.com/spf13/cobra` | CLI argument parsing and subcommand routing |
| `github.com/schollz/progressbar/v3` | Progress display for long-running operations |
| Standard library | SHA-256, JSON, file I/O, UUID generation, SQLite |

---

## 12. Implementation Phases

### Phase 1 — Core (MVP)

- [ ] `noahsark init` — create `.noahsark/` structure, generate config
- [ ] `noahsark commit` — fixed-size chunking, CAS dedup (bloom + index), write objects, build tree, write commit (SHA-256 named), update HEAD
- [ ] `noahsark stage` — select objects for disc, copy to staged directory, disc session tracking
- [ ] `noahsark burn` — wrapper around growisofs with multi-session support + --mark-archived
- [ ] `noahsark test-burn` — generate ISO from staged dir using genisoimage (for testing)
- [ ] `noahsark restore` — lookup via global index, reassemble from chunks, support SHA-256 commit refs
- [ ] `noahsark log` — walk commit chain from HEAD via parent pointers
- [ ] `noahsark gc` — garbage collect loose objects safely archived on discs
- [ ] `noahsark disc list` / `noahsark disc close` / `noahsark disc label` — disc session management
- [ ] `noahsark cat-object` — debug tool (works with all object types including commits)
- [ ] Global index (SQLite) + bloom filter
- [ ] Content-addressed commits (SHA-256 naming, parent pointers)
- [ ] (no FEC in Phase 1)

### Phase 2 — Integrity & Recovery

- [ ] `noahsark verify` — full verification with quick/full modes (ISO or mounted disc)
- [ ] `noahsark disc import` — scan mounted disc, register into index
- [ ] Disc piece layer (stored in disc metadata for per-disc verification, optional)

### Phase 3 — Watch & Optimization

- [ ] `noahsark watch` — fsnotify daemon with debounce and `--auto-stage`
- [ ] `noahsark index consolidate` — rebuild/optimize `index.db` (VACUUM, reindex, defrag)
- [ ] Zstd compression for chunks (optional per-chunk compression)

---

## 13. Data Integrity Guarantees

| Layer | Mechanism |
|-------|-----------|
| Object level | SHA-256 filename matches content hash |
| Chunk level | SHA-256 of chunk content verified on read |
| File level | Merkle root verified after reassembly |
| Disc level | All object hashes verified during disc import |
| Cross-disc | Duplicate discs (multiple copies) |

---

## 14. Testing and Verification Workflow

NoahsArk provides comprehensive testing capabilities to validate disc images
before burning expensive optical media.

### 14.1 Complete Test Workflow

```bash
# 1. Create a backup commit
noahsark commit -m "Test backup"

# 2. Stage objects for a test disc
noahsark stage --size=BD-25
# → Output: .noahsark/staged/20260224-001-BD25-s1/

# 3. Generate test ISO (no physical drive needed)
noahsark test-burn .noahsark/staged/20260224-001-BD25-s1/ test.iso
# → Creates test.iso (~23 GB for BD-25)

# 4. Quick verification of ISO
noahsark verify --iso=test.iso --level=quick
# → Verifies disc.json, index, spot-checks objects

# 5. Optional: Full verification (thorough but slow)
noahsark verify --iso=test.iso --level=full
# → Verifies all SHA-256 hashes and Merkle roots

# 6. Mount ISO for restore testing
sudo mkdir -p /mnt/test-disc
sudo mount -o loop,ro test.iso /mnt/test-disc

# 7. Test restore from ISO
noahsark restore HEAD /tmp/restore-test
# → NoahsArk reads objects from /mnt/test-disc/NOAHSARK/objects/

# 8. Verify restored data
diff -r /original/data /tmp/restore-test
# → Should be identical

# 9. Unmount ISO
sudo umount /mnt/test-disc

# 10. If all tests pass, burn to real disc
noahsark burn 20260224-001-BD25-s1 /dev/sr0 --mark-archived

# 11. Verify physical disc after burning
sudo mount /dev/sr0 /mnt/disc
noahsark verify --disc=20260224-001-BD25 --level=full
sudo umount /mnt/disc

# 12. Clean up local storage after successful burn
noahsark gc --aggressive
# → Deletes loose objects and staged/ directory
```

### 14.2 CI/CD Integration

For automated testing in continuous integration:

```yaml
# Example: GitHub Actions workflow
name: NoahsArk Backup Test

on: [push]

jobs:
  test-backup:
    runs-on: ubuntu-latest
    steps:
      - name: Install NoahsArk
        run: |
          go install github.com/user/noahsark@latest
          sudo apt-get install -y genisoimage

      - name: Initialize repository
        run: noahsark init

      - name: Create test backup
        run: noahsark commit -m "CI test backup"

      - name: Stage for disc
        run: noahsark stage --size=BD-25

      - name: Generate test ISO
        run: |
          noahsark test-burn \
            .noahsark/staged/*/  \
            test.iso

      - name: Verify ISO
        run: noahsark verify --iso=test.iso --level=full

      - name: Mount and test restore
        run: |
          sudo mkdir -p /mnt/test
          sudo mount -o loop,ro test.iso /mnt/test
          noahsark restore HEAD /tmp/restore
          sudo umount /mnt/test

      - name: Compare data
        run: diff -r original/ /tmp/restore

      - name: Upload ISO artifact
        uses: actions/upload-artifact@v3
        with:
          name: backup-iso
          path: test.iso
```

### 14.3 Verification Scenarios

#### Scenario 1: Pre-burn Validation
**Goal**: Ensure staged data is correct before wasting a disc.

```bash
noahsark stage --size=BD-25
noahsark test-burn .noahsark/staged/*/  test.iso
noahsark verify --iso=test.iso --level=full
# If PASS → safe to burn
# If FAIL → fix issues and re-stage
```

#### Scenario 2: Post-burn Verification
**Goal**: Confirm physical disc was burned correctly.

```bash
noahsark burn 20260224-001-BD25-s1 /dev/sr0
sudo mount /dev/sr0 /mnt/disc
noahsark verify --disc=20260224-001-BD25 --level=full
sudo umount /mnt/disc
# If PASS → disc is good, can GC local objects
# If FAIL → re-burn on new disc
```

#### Scenario 3: Periodic Disc Health Check
**Goal**: Detect bit rot or disc degradation over time.

```bash
# Mount old disc
sudo mount /dev/sr0 /mnt/disc

# Verify integrity
noahsark verify --disc=20260310-001-BD25 --level=full

# Check specific files
noahsark restore <commit> /tmp/restore
diff -r /mnt/disc/NOAHSARK/objects/ /tmp/restore/

sudo umount /mnt/disc
```

#### Scenario 4: Restore Rehearsal
**Goal**: Practice restore procedure before actual disaster.

```bash
# Simulate disaster: delete local .noahsark/
rm -rf .noahsark/

# Reinitialize
noahsark init

# Import from disc
sudo mount /dev/sr0 /mnt/disc
noahsark import /mnt/disc

# Restore data
noahsark restore <commit> /restore/path

# Verify
diff -r /original/data /restore/path
```

### 14.4 Performance Benchmarks

Typical verification times (BD-25 ~23 GB):

| Operation | Mode | Time | Notes |
|-----------|------|------|-------|
| test-burn | - | ~2-3 min | Generate ISO from staged/ |
| verify | quick | ~5-10 sec | Check metadata + spot-check 10 objects |
| verify | full | ~5-10 min | Verify all SHA-256 + Merkle roots |
| mount ISO | - | <1 sec | Mount as loop device |
| restore | single file | ~10-30 sec | Depends on file size |
| restore | full commit | ~10-30 min | Depends on data size |

**Note**: ISO operations are much faster than real optical drives:
- ISO read: ~500 MB/s (disk speed)
- BD read: ~10-30 MB/s (optical speed, 50x slower)

Testing with ISO before burning saves significant time.

## 15. Design Inspirations

| Feature | Inspired By |
|---------|------------|
| Content-addressed object model (blob/tree/commit) | Git |
| SHA-256 commit naming with parent pointers | Git |
| Direct object storage (no pack files) | Git (loose objects) |
| Global index (hash → disc location) | Restic, Git |
| Chunk store as CAS directory | Casync |
| Per-file Merkle tree (16 KiB leaves) | BitTorrent v2 (BEP 52) |
| Disc piece layer concept | BitTorrent v2 (piece layers) |
| Manifest copy on every disc | Borg |
| Append-only multi-session | Borg, UDF |
| fsnotify inotify watcher | fsnotify library |
| Fixed-size chunking | Simple and predictable |
