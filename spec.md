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
     └─ Write commit object  (parent, date, root tree, new blobs list)
                                        │
                               .noahsark/HEAD updated

  noahsark stage [--size=BD-25|BD-50|BD-100|DVD|CD|<bytes>] [--disc=<disc_id>] [output-dir]
     │
     │  --size       target disc capacity (default: BD-25)
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
       → "Or:   noahsark stage --disc=<disc_id>  to continue this disc"
       → "Or:   noahsark stage --size=BD-25      to start a new disc"

  noahsark restore [commit] [src-path] [dest-path]
     └─ Global index lookup → locate chunks across discs → reassemble → verify Merkle
```

---

## 3. Object Model

All objects are stored content-addressably by SHA-256 under:

```
.noahsark/objects/<hash[:2]>/<hash[2:]>
```

(identical to the existing Python prototype, and to Git's layout)

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
parent <PARENT_SHA256_OR_ZEROS>
date <RFC3339>
tree <ROOT_TREE_SHA256>
hostname <HOSTNAME>
message <OPTIONAL_ONE_LINE_MESSAGE>
---
<blob_sha256> <relative_path>
<blob_sha256> <relative_path>
...
```

The section after `---` lists **only new or changed blobs** in this commit (incremental).
Unchanged files are implicit via the tree structure.

- **Metadata fields:**
- `parent`: RFC3339 timestamp of parent commit (empty or null if first commit)
- `tree`: SHA-256 hash of root tree object
- `blob_sha256`: SHA-256 hash of each blob in the incremental list

Named by: RFC3339 timestamp of the commit (filename-safe, e.g. 2026-02-24T12:00:00+08:00.json).

`HEAD` file: `.noahsark/HEAD` contains the current commit timestamp (RFC3339 + newline).

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


### 5.6 Bin-Packing Strategy

When staging, chunks are sorted by commit order and packed using a First Fit Decreasing
bin-packing approach:

1. Collect all chunks not yet allocated to a disc from `.noahsark/objects/`
2. Sort by type priority: commits and trees first (small, needed for restore), then blobs by file
3. Fill packs greedily until `pack_target_size` is reached
4. If a single chunk exceeds the pack size, it is split across consecutive packs (rare with 4 MiB max chunk)

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
├── HEAD                        # current commit timestamp (RFC3339 + newline)
├── commits/                    # linear commit history: <RFC3339>.json (one file per commit)
├── objects/                    # loose objects (blobs, trees, commits, chunks) — only unallocated objects
│   └── XX/YYYY...              # 2-char prefix / remainder
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

The `staged/` directories remain until manually cleaned with `noahsark stage gc`
(recommended after successful burn + verify).

### 7.2 Config (`config`)

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

### 7.3 Global Index (`.noahsark/index.db`)

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

### 7.4 On-Disc Layout (UDF 2.50)

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
  "object_count": 12345
}
```

**manifest.json:** A copy of the global index at the time of burning, allowing the
repository to be reconstructed by scanning a single disc if the local `.noahsark/` is lost.
When present, `index.db` is a SQLite copy of the index for fast local lookups; `bloom.bin`
is optional but recommended to simplify import and recovery.

### Object lifecycle and allocation

NoahsArk treats `objects/` as the pool of unallocated, loose objects. When an object
is selected for inclusion on a disc session (during `noahsark stage`),
implementations MAY copy the object's file out of `.noahsark/objects/` into the
staged output under `.noahsark/staged/<disc_id>-s<session>/NOAHSARK/objects/`.

Rules:
- Allocation: once an object is recorded in the global index as stored on a disc,
  its local loose copy MAY be removed to avoid duplication; the authoritative
  location becomes the disc's `manifest.json` entry and the physical object file
  stored under `NOAHSARK/objects/` on that disc.
- Atomicity: copies and removals MUST be atomic where possible. Update the index
  to refer to the disc location only after the object file and the disc `manifest.json`
  are durably written.
- Burn completion: after a disc session is successfully burned and verified, the
  corresponding objects that were staged for that session SHOULD be removed from
  the local `.noahsark/objects/` pool. In steady-state (all data archived),
  `.noahsark/objects/` will be empty.
- Unallocated objects: any object not assigned to a disc remains in `.noahsark/objects/`.

This behavior keeps local storage minimal: objects that have been committed and
allocated for archival are represented by their pack entries on staged discs and in
the global index; the loose-object pool contains only items awaiting allocation.

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

noahsark stage [output-dir] [flags]
    Generate a disc image directory from unstaged objects (ready for growisofs).
    --size=<preset|bytes>   disc capacity: BD-25, BD-50, BD-100, DVD, CD, or raw bytes
                            default: BD-25
    --disc=<disc_id>        continue filling an existing open disc session
                            (omit to allocate a new disc_id: YYYYMMDD-NNN-TYPE)
    --label=<text>          optional label for new discs (for disc list display)

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
          noahsark stage --size=BD-25               # start a new disc

noahsark burn [staged-dir] [device]
    Wrapper around growisofs for easier burning with multi-session support.

    Session 1 (new disc):
      noahsark burn .noahsark/staged/20260224-001-BD25-s1/ /dev/sr0

      Executes:
        growisofs -Z /dev/sr0 \
          -udf \
          -allow-limited-size \
          -input-charset utf8 \
          -V '20260224-001-BD25' \
          .noahsark/staged/20260224-001-BD25-s1/NOAHSARK/

    Session 2+ (continue disc):
      noahsark burn .noahsark/staged/20260224-001-BD25-s2/ /dev/sr0

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
      -allow-limited-size   Allow large images (BD-50/BD-100 = 46GB/93GB)
      -input-charset utf8   Preserve Unicode filenames (UTF-8 support)
      -V                    Volume label (shown when mounted)
      -o                    Output ISO file path

    Use cases:
      - CI/CD testing without physical media
        - Verify objects and manifest before burning
      - Mount ISO locally to test restore:
          mount -o loop test.iso /mnt/test
          noahsark restore HEAD /path /mnt/test/NOAHSARK/objects/

noahsark stage gc [--keep-days=7]
    Clean up staged directories older than N days (default: 7).
    Only removes directories for discs with status=closed or status=full.

noahsark restore [commit] [src-path] [dest-path]
    Restore src-path as it existed at commit to dest-path.
    commit may be an RFC3339 timestamp (filename), a short prefix, or HEAD~N notation.

noahsark log [--count=20] [--oneline]
    Show commit history from HEAD.

noahsark verify [--disc=<disc_id>|--all]
    Verify CRC32 of all entries, chunk SHA-256 hashes, and Merkle roots.
    Can verify against a mounted disc or against the local staged ISO.

noahsark index consolidate
  Rebuild or optimize the repository `index.db` (import any ephemeral snapshots
  or disc-imported index data) and perform optional maintenance (VACUUM, reindex).

noahsark disc list [--status=open|full|closed|all]
    List all disc sessions with human-readable formatting:

    DISC ID              LABEL                    TYPE    USED / TOTAL      STATUS  SESSIONS
    20260224-001-BD25    Project Backup Q1        BD-25   12.4 / 23.3 GiB   open    1
    20260224-002-BD50    VM Images                BD-50   46.1 / 46.6 GiB   full    3
    20260310-001-BD25    Documents Archive        BD-25   23.3 / 23.3 GiB   closed  2

    --status filter (default: all)

noahsark disc close <disc_id>
    Mark an open disc as closed (will not be used for future iso runs).
    Updates status to "closed" even if space remains.

noahsark disc label <disc_id> <new_label>
    Update the label for an existing disc.

noahsark disc import [mount-point]
    Scan a mounted disc's manifest and pack headers; register into local index.

noahsark watch [source-dir] [--debounce=30s] [--auto-iso=false] [--size=BD-25]
    Start fsnotify daemon (see §8).
    --auto-iso: when pending objects exceed disc capacity, auto-run noahsark iso.

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
- [ ] `noahsark commit` — fixed-size chunking, CAS dedup (bloom + index), write objects, build tree, write commit, update HEAD
- [ ] `noahsark stage` — bin-pack chunks into pack files, generate disc image directory, disc session tracking
- [ ] `noahsark burn` — wrapper around growisofs with multi-session support (session detection)
- [ ] `noahsark test-burn` — generate ISO from staged dir using genisoimage (for testing)
- [ ] `noahsark restore` — lookup via global index, reassemble from chunks
- [ ] `noahsark log` — walk commit chain from HEAD
- [ ] `noahsark disc list` / `noahsark disc close` / `noahsark disc label` — disc session management
- [ ] `noahsark stage gc` — clean up old staged directories
- [ ] `noahsark cat-object` — debug tool
- [ ] Global index (JSON) + bloom filter
-- [ ] (no FEC in Phase 1)

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
