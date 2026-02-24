# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**NoahsArk** is a content-addressable backup system for write-once Blu-ray optical media, designed to be implemented in Go. The project is currently in the **specification phase** with no code implementation yet.

**Key Characteristics:**
- Content-addressable storage inspired by Git (blob/tree/commit objects)
- Fixed-size 16 MiB chunking for sub-file deduplication
- Multi-session disc support (incremental burning)
- Disc-spanning for large files
- SHA-256 content addressing for all objects including commits
- No Forward Error Correction (uses duplicate disc copies for redundancy)

## Architecture Overview

### Object Model (Git-Inspired)

NoahsArk uses a four-tier object model where all objects are content-addressed by SHA-256:

1. **Chunk** (16 MiB fixed-size): Raw data bytes stored in `objects/`, named by chunk hash
2. **Blob**: File metadata stored in `metadata/`, contains chunk list, chunk hashes, Merkle root, size, and other file info
3. **Tree**: Directory structure with mode/type/hash/name entries, stored in `metadata/`
4. **Commit**: Snapshot with tree hash, parent commit hash, metadata, and changed blob list, stored in `metadata/`

**Critical Design Principle:** All objects including commits are named by their SHA-256 hash, forming immutable content-addressed storage. Commits point to parent commits via SHA-256 hash (not timestamp), creating a verifiable history chain.

**Storage Separation:** Raw chunk data is stored separately from structural metadata:
- **Rationale**: Chunks are large (16 MiB each) and can be GC'd after disc archival, while metadata (blobs, trees, commits) is small and kept permanently for history
- **Benefit**: Simpler garbage collection logic and clearer separation of concerns
- **Location**: `objects/` for raw chunk data, `metadata/` for blobs/trees/commits

### Repository Structure

```
.noahsark/
├── objects/XX/YY...        # Raw chunk data only (16 MiB blocks, named by hash)
├── metadata/XX/YY...       # Structural metadata (blobs, trees, commits)
├── staged/                 # Staged disc sessions awaiting burn
│   └── <disc_id>-s<N>/    # Per-session staging directories
├── discs/                  # Disc metadata JSON files
├── index.db                # SQLite global index (hash → disc location)
├── bloom.dat              # Bloom filter for quick dedup checks
├── config                 # Repository configuration
└── HEAD                   # Current commit SHA-256 hash

On-Disc Layout (UDF 2.50):
├── objects/XX/YY...       # Raw chunk data only (16 MiB blocks, named by hash)
├── metadata/XX/YY...      # Structural metadata (blobs, trees, commits)
├── s1/                    # Session 1 directory
│   ├── session.json       # Session 1 metadata
│   └── index.db           # Index snapshot (up to session 1)
├── s2/                    # Session 2 directory
│   ├── session.json       # Session 2 metadata
│   └── index.db           # Index snapshot (up to session 1+2)
└── s3/                    # Session 3 directory (if present)
    ├── session.json       # Session 3 metadata
    └── index.db           # Index snapshot (up to session 1+2+3)
```

### Multi-Session Workflow

NoahsArk supports incremental disc burning where a partially-filled disc can have additional sessions added until capacity is reached:

- **Session 1**: `growisofs -Z` (new disc)
- **Session 2+**: `growisofs -M` (append to existing)
- Each session adds new objects without duplicating existing ones
- Sessions tracked in disc metadata with status: open/full/closed/error

**Capacity Management:**
- Remaining capacity: `remaining = disc_capacity - sum(session.bytes) - final_session_reserve`
- Final session reserve: **1 GiB** (for UDF closing structures + safety margin)
- Disc transitions to "full" status when `remaining <= 0`
- This prevents overfilling and ensures space for UDF filesystem finalization

## Core Commands (from spec.md)

**Note:** These commands are defined in the specification but not yet implemented.

### Essential Commands

- `noahsark init` — Create `.noahsark/` structure
- `noahsark commit` — Chunk files, deduplicate, write objects, create commit
- `noahsark stage` — Select unstaged objects and prepare disc image directory
  - `--size=BD-25|BD-50|BD-100|BD-128|<custom>` — Disc capacity
  - `--disc=<disc_id>` — Continue filling existing disc (multi-session)
  - `--label=<text>` — Optional disc label
- `noahsark burn` — Wrapper around growisofs for burning
  - `--mark-archived` — Close disc and allow GC of local objects
- `noahsark test-burn` — Generate ISO for testing without physical disc
- `noahsark restore` — Reassemble files from disc(s)
- `noahsark log` — Show commit history via parent pointers
- `noahsark gc` — Delete local objects safely archived on discs
- `noahsark verify` — Verify disc/ISO integrity
  - `--level=quick|full` — Verification thoroughness
- `noahsark disc list|close|label|import` — Disc management

### Watch Mode (Phase 3)
- `noahsark watch` — fsnotify daemon with auto-commit/auto-stage

## Implementation Phases

### Phase 1 — Core (MVP)
- All basic commands: init, commit, stage, burn, test-burn, restore, log, gc
- SQLite global index + bloom filter
- Content-addressed commits with parent pointers
- Multi-session support

### Phase 2 — Integrity & Recovery
- Full verification support (quick/full modes)
- Disc import from mounted media
- Optional disc piece layer (BitTorrent v2-inspired)

### Phase 3 — Watch & Optimization
- fsnotify daemon with auto-stage
- Index consolidation and optimization
- Optional zstd chunk compression

## Key Technical Details

### Chunking Strategy
- **Fixed 16 MiB chunks** (16777216 bytes) — not configurable
- Last chunk uses actual remaining bytes (no padding)
- Simple, predictable, optimal for large files and slow optical media
- Trade-off: Lower dedup ratio vs content-defined chunking, but much simpler

### Merkle Tree Integrity
- Per-file Merkle tree (BitTorrent v2 BEP 52 style)
- 16 KiB leaves (1024 leaves per 16 MiB chunk)
- SHA-256 throughout
- Single-pass implementation: compute Merkle leaves (16 KiB) and chunk hashes (16 MiB) simultaneously

### Object Encoding
**Metadata objects (blobs, trees, commits):**
- Encoding: UTF-8 text files (no BOM)
- Line endings: Unix-style LF (`\n`)
- Full Unicode support for filenames, paths, and commit messages

**Chunk objects:**
- Raw binary data (no text encoding)

**Tree entry format:**
```
<mode> <type> <hash> <filename>
```
- Parsing: Split on space for first 3 fields, everything after 3rd space is filename
- Filenames: UTF-8, may contain spaces (cannot contain `\n` or null bytes)
- Examples: `my file.txt`, `文档.pdf` fully supported

**Commit blob list format:**
```
<blob_sha256> <relative_path>
```
- Parsing: Split on first space for hash (64 chars), everything after is path
- Paths: UTF-8, may contain spaces (cannot contain `\n` or null bytes)

### Disc Capacity Presets
- BD-25: 23.28 GiB usable (after UDF overhead + 5% safety margin)
- BD-50: 46.55 GiB
- BD-100: 93.11 GiB
- BD-128: 118.86 GiB
- Custom: `<number><unit>` (e.g., 20G, 500M, 1T)

### Content-Addressed Commits (Critical Design Change)
**Old design:** Commits named by RFC3339 timestamp
**New design:** Commits named by SHA-256 hash of commit content

This enables:
- Verification of entire history chain
- Immutable commits (changing any field changes the hash)
- Distributed trust without central authority
- Automatic deduplication of identical commits

### Cross-Disc Scenarios

1. **Multiple commits per disc**: Multi-session burning, each session references a commit
2. **One commit spanning multiple discs**: Large backups distributed across discs
3. **Single file exceeding disc capacity**: Chunks automatically span multiple discs

## Development Guidelines

### When Implementing

**File Organization:**
- Consider using a `cmd/` directory for CLI entry points (cobra commands)
- Consider `pkg/` or `internal/` for core libraries (chunking, objects, index, disc management)
- Follow Go project layout conventions

**Core Components to Implement:**
- Object storage engine with separated storage:
  - Raw chunk data → `.noahsark/objects/XX/YY...`
  - Metadata objects (blobs/trees/commits) → `.noahsark/metadata/XX/YY...`
- Chunker (fixed 16 MiB with simultaneous Merkle tree computation)
- SQLite index management with bloom filter
- Commit chain traversal (parent pointer walking)
- Disc session state machine (open → full/closed)
- growisofs wrapper with multi-session detection (-Z vs -M)

**Testing Strategy:**
- Test with `test-burn` command (ISO generation) before physical burning
- Verify workflows: stage → test-burn → mount ISO → verify → restore
- Unit test object serialization/deserialization
- Test multi-session scenarios and capacity management
- Test cross-disc scenarios (files spanning multiple discs)

**Critical Safety Rules:**
- Never duplicate objects within the same disc across sessions
- Never delete chunks from `.noahsark/objects/` unless verified on burned disc
- Metadata objects in `.noahsark/metadata/` should persist (blobs/trees/commits are small)
- Always verify disc capacity before staging
- Preserve content-addressing integrity (hash must match content)
- Commit SHA-256 must be computed from full commit content

**Storage Organization:**
- **objects/**: Raw chunk data only (16 MiB blocks) — can be GC'd after archival
- **metadata/**: Structural metadata (blobs listing chunks, trees, commits) — typically kept permanently for history

**Git Workflow:**
- Always use `git commit -s` to sign-off commits (adds Signed-off-by line)
- When completing a batch of changes, commit with sign-off
- Example: `git commit -s -m "spec: add UTF-8 encoding specification"`
- Sign-off certifies the Developer Certificate of Origin (DCO)

### Specification Reference

The `spec.md` file is the **primary reference** for design decisions. When implementing:

1. Read relevant sections of spec.md thoroughly before coding
2. Follow the exact object formats specified (blob, tree, commit)
3. Implement the SHA-256 naming scheme precisely
4. Honor the fixed 16 MiB chunk size (not configurable)
5. Follow the multi-session workflow exactly as specified

**Note on Storage Layout:** This CLAUDE.md reflects an updated design decision:
- **spec.md**: Stores all objects in `objects/` directory
- **Updated design**: Separates `objects/` (raw chunks only) from `metadata/` (blobs/trees/commits)
- When implementing, use the separated storage layout documented in this file
- The spec.md should be updated to reflect this change

### Common Operations

**Creating a backup:**
```bash
noahsark commit -m "Initial backup"
noahsark stage --size=BD-25 --label="Backup Q1 2026"
noahsark test-burn .noahsark/staged/<disc_id>-s1/ test.iso  # Optional: test first
noahsark burn <disc_id> /dev/sr0 --mark-archived
```

**Multi-session continuation:**
```bash
noahsark commit -m "Weekly backup"
noahsark stage --disc=<existing_disc_id>  # Continue same disc
noahsark burn <disc_id> /dev/sr0
```

**Restore:**
```bash
noahsark log                           # Find commit hash
noahsark restore <commit_hash> / /restore/path
# System will prompt for required discs
```

## Dependencies (from spec.md §11)

- `github.com/fsnotify/fsnotify` — File system watching
- `github.com/klauspost/compress/zstd` — Optional chunk compression (Phase 2+)
- `github.com/spf13/cobra` — CLI framework
- `github.com/schollz/progressbar/v3` — Progress display
- Go standard library for SHA-256, JSON, SQLite, file I/O

## Design Inspirations

- **Git**: Content-addressed objects, commit/tree/blob model, parent pointers
- **Restic**: Global index for object locations
- **Casync**: Chunk store as CAS directory
- **BitTorrent v2 (BEP 52)**: Per-file Merkle trees with 16 KiB leaves
- **Borg**: Manifest copy on every disc, append-only multi-session
- **UDF**: Multi-session append-only disc format

## Important Notes

- **No FEC in Phase 1**: Redundancy via duplicate disc copies, not Forward Error Correction
- **No pack files**: Objects stored directly in hash-based directory structure (simplified from earlier Git-like pack design)
- **Fixed chunk size**: 16 MiB is not configurable to ensure repository compatibility
- **Commit history is immutable**: Parent pointers form a tamper-evident chain
- **Multi-session is append-only**: Once burned, session data is immutable

## Project Status

**Current state:** Specification only (no implementation)

When beginning implementation:
1. Start with Phase 1 MVP commands
2. Focus on correct object model implementation first
3. Implement commit chain with proper SHA-256 content addressing
4. Test thoroughly with `test-burn` and ISO mounting before physical burning
5. Ensure multi-session logic prevents duplicate objects on same disc
