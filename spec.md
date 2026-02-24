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
- **Hierarchical integrity** — per-file Merkle trees (BitTorrent v2 style) + CRC32 per entry
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
    ├─ Fixed-size 16 MiB chunking  →  bloom filter check  →  global index check
     │                              │ new chunk?
     │                            yes → write loose object to .noahsark/objects/
     │                             no → skip (dedup)
     ├─ Build tree objects   (directory hierarchy)
     └─ Write commit object  (tree, parent, author, committer, hostname, message)
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
    ├─ Load disc session state (remaining capacity, already-packed objects)
    ├─ Bin-pack unstaged chunks up to remaining capacity
     ├─ Compute per-file Merkle trees → embed in blob objects
     ├─ Generate pack index (fanout table + sorted hash entries)
     ├─ Write manifest + global index snapshot
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
disc_layer <DISC_CAPACITY_BYTES>
<disc_piece_hash_0>
<disc_piece_hash_1>
...
```

**Fields:**
- `size`: total file size in bytes
- `chunks`: number of chunks (for quick validation)
- `chunk_sha256`: SHA-256 hash of chunk data (hex, 64 chars)
- `offset`: byte position in the original file (always `chunk_index × 16777216`)
- `length`: actual byte length of this chunk (16777216 for full chunks, less for last chunk)
- `merkle_root`: SHA-256 root of the per-file Merkle tree (see §6)

**Disc piece layer (optional, BitTorrent v2-inspired):**
- `disc_layer`: capacity in bytes (e.g., 25025314816 for BD-25)
- `<disc_piece_hash_N>`: SHA-256 hash covering one disc's worth of chunk data
- Enables verification of data on a single disc without needing other discs
- Similar to BitTorrent v2's piece layer, but aligned to disc boundaries
- **Phase 2+ feature**: May be omitted in Phase 1 implementations

Implementation note:
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

- `type`: one of `blob`, `tree`, `link`
- `object_sha256`: SHA-256 hash of the referenced object (hex, 64 chars)
- `name`: filename or subdirectory name

Tree entries include POSIX-like mode bits (Git-style) so permissions are
tracked as part of the tree object. Changing mode/ownership/ACLs will change
the tree SHA, just like changing file contents or renaming entries.

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
040000 tree 9f8e7d6c... src
120000 link 5a4b3c2d... link-to-config
```

Optional ownership and extended attributes:
- `uid`/`gid`: optional integer owner/group stored as a separate `attrs` block
  inside the same tree object. This keeps the line-oriented tree compact while
  allowing ownership metadata to be recorded when required.
- `xattrs`/`acl`: optional JSON blob (UTF-8) stored in an `attrs` section for
  each entry when needed (base64-encoded or raw JSON; implementations may
  choose a canonical serialization).

`attrs` block format (optional, follows the tree entries):
```
attrs
<name> uid <UID>
<name> gid <GID>
<name> acl <BASE64_JSON>
...
```

Notes:
- If no `attrs` block is present, the repository is treated as permission-agnostic
  (only mode bits recorded). This preserves small tree sizes for common cases.
- Converters/importers should populate `mode` at commit time; `uid`/`gid` and
  ACLs are optional and primarily useful for POSIX-preserving archives.
- Any change to `mode`, `attrs` or the entry target (`object_sha256`) changes
  the tree content and therefore its SHA-256 name.

Symlinks are stored as:
```
link <sha256_of_link_target_string> <name>
```

Named by: SHA-256 of the tree content.

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
- `<COMMIT_MESSAGE>`: optional multi-line commit message (after blank line)

The section after `---` lists **only new or changed blobs** in this commit (incremental).
Unchanged files are implicit via the tree structure.

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
```

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
- Saves storage space (no wasted bytes in packs)

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


## 5. Staging Strategy

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
2. **Single disc**: verify the "piece layer" hashes (one hash per pack-worth of data)
3. **Single block**: verify any 16 KiB block without reading the entire file

This enables restoring and verifying a single disc independently without needing
all other discs to be present.

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
- `object_path` is used for direct-object storage on discs (preferred). For
  historical compatibility `offset`/`pack_id` may exist but are unused for
  direct-object layout and SHOULD be NULL.
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
      Writes a directory tree (UDF-compatible layout) with packs, indices, manifest.
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

    Session 1 (new disc):
      noahsark burn .noahsark/staged/20260224-001-BD25-s1/ /dev/sr0

      Executes:
        growisofs -Z /dev/sr0 \
          -udf \
          -allow-limited-size \
          -input-charset utf8 \
          -V '20260224-001-BD25' \
          .noahsark/staged/20260224-001-BD25-s1/NOAHSARK/

      Note: -allow-limited-size is required for BD-50/BD-100/BD-128 (bypasses ISO 9660 size limits)

    Session 2+ (continue disc):
      noahsark burn .noahsark/staged/20260224-001-BD25-s2/ /dev/sr0

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
      - Mount ISO locally to test restore:
          mount -o loop test.iso /mnt/test
          noahsark restore HEAD /path /mnt/test/NOAHSARK/objects/

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

noahsark verify [--disc=<disc_id>|--all]
    Verify CRC32 of all entries, chunk SHA-256 hashes, and Merkle roots.
    Can verify against a mounted disc or against the local staged ISO.

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
    Scan a mounted disc's manifest and pack headers; register into local index.

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
| Standard library | SHA-256, CRC32, JSON, file I/O, UUID generation |

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

- [ ] `noahsark verify` — CRC32 + SHA-256 + Merkle verification (against local ISO or mounted disc)
- [ ] `noahsark disc import` — scan mounted disc, register into index
- [ ] Per-file Merkle tree embedded in blob objects
- [ ] On-disc manifest copy (for disaster recovery without local index)
- [ ] On-disc manifest copy (for disaster recovery without local index)

### Phase 3 — Watch & Optimization

- [ ] `noahsark watch` — fsnotify daemon with debounce and `--auto-iso`
- [ ] `noahsark index consolidate` — rebuild/optimize `index.db` (import snapshots, VACUUM/reindex)
- [ ] Multi-disc index (`.midx`-style) for fast cross-disc lookup
- [ ] Zstd compression for chunks

---

## 13. Data Integrity Guarantees

| Layer | Mechanism |
|-------|-----------|
| Entry level | CRC32 per pack entry |
| Chunk level | SHA-256 of chunk content verified on read |
| File level | Merkle root verified after reassembly |
| Disc level | Pack SHA-256 in index verified on disc scan |
| Cross-disc | Duplicate discs (multiple copies) |

---

## 14. Design Inspirations

| Feature | Inspired By |
|---------|------------|
| blob / tree / commit object model | Git |
| Pack file with trailing header | Restic |
| Global index (hash → pack + offset) | Restic |
| (removed) | |
| Chunk store as CAS directory | Casync |
| Write-directly-to-pack (no loose repack) | Bup |
| Per-file Merkle tree | BitTorrent v2 (BEP 52) |
| Manifest copy on every disc | Borg |
| Append-only segment files | Borg |
| fsnotify inotify watcher | fsnotify library |
| (none) | |
