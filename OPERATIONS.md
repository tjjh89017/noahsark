# NoahsArk operations

**Document version 5.0.** Format major 1.

This document states what the NoahsArk build does on the host. `FORMAT.md` is
the authority for every byte on a disc. This document cites `FORMAT.md` by
heading text and never repeats an on-disc structure. It carries the byte
layout of a local file only, because those bytes never reach a disc.

The code is the truth. This document describes the build as it is. It does
not describe a feature that the build does not have. `docs/guide.md` is the
operator guide.

## 1. Scope and conventions

1. This document is the authority for host behaviour. `FORMAT.md` is the
   authority for disc bytes. Where the two touch, `FORMAT.md` wins.
2. The build refuses an unknown command, an unknown option and an unknown
   config key. It names the item and exits with code 2.
3. Every size is in bytes unless the text says sectors. A sector is 2048
   bytes.
4. The tool never removes a snapshot and never frees disc space.
5. Two identical discs are the primary redundancy. The operator burns each
   image two times and stores the two copies in different places. FEC is
   optional and off by default. A disc that does not mount counts as dead.
   There is no recovery by carving.
6. The tool never runs `sudo`. The tool never mounts a device. The operator
   mounts a disc and gives the mount point as a disc root. There is one
   exception: `image build` loop-mounts the image file that it builds, and it
   needs root for that.
7. The design priorities are ordered: data durability, readability without
   the tool, restore usability, deduplication, speed, media utilization.
8. Peak memory follows the chunk size and the stripe size. It never follows
   the data size.

## 2. Repository, staging and cache

### 2.1 The repository directory

A repository is one local directory. A directory is a repository when it
holds a `config` file.

| Path | Content |
|---|---|
| `<repo>/config` | The configuration file. It holds `repo.uuid`. |
| `<repo>/lock` | The repository lock file. |
| `<repo>/refs.txt` | The local refs. The first `commit` creates it. |
| `<repo>/staging/` | The staging store, unless `staging.dir` moves it. |
| `<repo>/cache/` | The local cache. The first command that fills it creates it. |

Every disc carries the `repo_uuid`. `recover` makes a lost repository again
from its discs. The repository is the source of truth only for the objects
that are not yet CLEAN and for the refs that no disc carries yet.

### 2.2 Repository discovery

The first hit wins: `--repo=PATH`; then the environment variable
`NOAHSARK_REPO`; then the current directory and each ancestor of it.

With no repository, `commit`, `pack`, `gc`, `status` and `disc burned` exit
with code 2. `ls`, `log` and `restore --mount` need the local cache, and exit
with code 1. `recover` creates the repository directory; it needs `--repo` or
`NOAHSARK_REPO` then. `verify` still checks the disc root and changes no
state.

### 2.3 Staging store layout

```
staging/
    objects/ab/<id>             chunk, blob and tree files that wait for a pack
    snapshots/<id>              snapshot objects
    plans/<disc-uuid>/tree      the disc root that pack writes by default
    plans/<disc-uuid>/tree.img  the image of the printed image build line
    state.db                    the state log
    discs.bin                   the disc ledger
    refslog.bin                 the ref ledger
```

`state.db`, `discs.bin`, `refslog.bin` and `<repo>/refs.txt` are the
authoritative local state. Keep staging on a local filesystem: the state log
depends on a durable append.

### 2.4 Local cache layout

The location is always `<repo>/cache/`. There is no override. Everything in
the cache comes from discs or from staging, and `recover` builds it again from
the discs. It holds no chunk data. `gc` never trims it.

| Item | Content |
|---|---|
| `discs/<disc-uuid>/INDEX.bin`, `REFS.bin`, `DISCS.bin` | Byte copies of the tables that FORMAT.md's "The run index and the catalog" defines. The key is the disc uuid, never `run_seq`. |
| `snapshots/<id>`, `trees/<id>`, `blobs/<id>` | A byte copy of each cached snapshot object, and of each tree and blob object that it reaches. |
| `state.txt` | For each snapshot id, whether its tree set is complete in the cache. |

`pack` writes the cache entries of the disc that it packs. `verify` with a
repository and `recover` write them from a disc.

`commit`, `pack`, `status`, `disc burned` and `verify` do not read the cache.
`restore`, `ls` and `log` with disc roots read the given discs alone, and need
no repository. `restore --mount`, and `ls` and `log` with no disc root, need
the cache: run `recover` first, one time for each disc.

## 3. Local file formats

No byte of these files reaches a disc. Every integer is little-endian, and
every CRC is CRC-32C.

### 3.1 State log record

`<staging>/state.db` is append-only. It has no header. It is a whole number of
fixed-width records, each 79 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u64 | `sequence` | Monotonic record number. It starts at 1. |
| 8 | 32 | u8[32] | `content_id` | The object. |
| 40 | 1 | u8 | `state` | 1 STAGED, 2 PACKED, 3 BURNED, 4 CLEAN, 5 ON-DISC. |
| 41 | 8 | u64 | `run_seq` | The run, when the state is PACKED or later. 0 otherwise. It is a label; the disc uuid is the key. |
| 49 | 16 | u8[16] | `disc_uuid` | The disc, when known. All zero otherwise. |
| 65 | 1 | u8 | `reason` | 0 normal, 1 burn undone, 2 verify failed. |
| 66 | 1 | u8 | `verify_count` | Successful verifies of the object. It stops at 255. |
| 67 | 8 | i64 | `clean_sec` | Unix time of the **first** successful verify. 0 before that verify. Every later record carries the value forward. |
| 75 | 4 | u32 | `record_crc32c` | CRC-32C over bytes 0 to 74. |

### 3.2 Local refs

`<repo>/refs.txt` is a text file. Each line holds a ref name, one space, and
a snapshot id in text form. `commit` replaces the line of the ref that it
moves. `recover` writes the file from the REFS tables of the discs.

### 3.3 Ledgers

`<staging>/discs.bin` is the disc ledger: one row for each disc that `pack`
wrote or `recover` read. It uses the container of FORMAT.md's "DISCS". `pack`
takes the next `run_seq` and `disc_seq` from the maximum that the ledger
holds. A fresh repository starts at `run_seq` 1 and `disc_seq` 0.

`<staging>/refslog.bin` is the ref ledger: every ref record that a disc
already carries. It uses the container of FORMAT.md's "REFS".

## 4. Staging state machine

### 4.1 States and transitions

```
  commit          pack           disc burned          verify ok          gc
    |                                                                after 7 days and
    v                                                                2 verifies
 STAGED -------> PACKED ----------------> BURNED -----------> CLEAN -----------> ON-DISC
                    ^                        |
                    +------------------------+
                     disc burned --undo, or verify failed
```

ON-DISC means that a disc holds the object and staging holds no file for it.
It is the last state.

### 4.2 Transition rules

1. `commit` is the only entry point. It records every new object as STAGED
   before it moves the ref.
2. `pack` moves the objects that it puts on a disc from STAGED to PACKED.
   There is no transition from PACKED back to STAGED.
3. The tool never concludes by itself that a burn occurred. `disc burned` is
   the only transition from PACKED to BURNED. `verify` never moves a PACKED
   object: a disc root that passes `verify` can be an image that no one
   burned.
4. `verify` is the only transition from BURNED to CLEAN. There is no timer
   and no manual override.
5. A successful `verify` of a BURNED object moves it to CLEAN and sets its
   verify count to 1. A successful `verify` of a CLEAN object adds 1. The
   clean time is the time of the first successful verify, and a later verify
   does not change it.
6. The two identical copies of a disc carry the same disc uuid. The count
   counts successful verify passes, not distinct physical discs.
7. `disc burned --undo` moves the BURNED objects of a disc back to PACKED,
   with reason 1. It refuses a disc that has a CLEAN object.
8. A failed `verify` moves the BURNED objects of the disc back to PACKED, with
   reason 2. It leaves a CLEAN object alone. The disc root and the image stay
   on the local disk, thus the operator burns the same image on a new disc.
9. `gc` moves a CLEAN object to ON-DISC. `recover` records each object that
   it reads from a disc as ON-DISC, and leaves an object that the log already
   knows at its own state.

### 4.3 State log replay

The current state of an object is the newest record for that id. A reader
replays the log from the start. The build never compacts the log.

A partial record at the end of the file, or a last record with a bad CRC, is
what a crash during an append leaves. A command that holds the repository lock
cuts the file back to the last good record, prints one warning, and goes on. A
read-only command ignores the torn tail and never changes the file.

A bad record anywhere else is damage. The tool reports an error, names the
record, changes no byte of the file, and stops. An error from a write or from
the close of the log file stops the command.

### 4.4 GC rules

1. `gc` frees the staged file of a CLEAN object only. It also frees an orphan:
   a staged file whose object is already ON-DISC.
2. `gc` frees it only after 7 days have passed since the first successful
   verify. This period is fixed. `--force-after` shortens it for one run, and
   asks for a confirmation.
3. `gc` frees it only when its verify count is at least
   `gc.min_verified_copies`, default 2. No option passes by this count.
4. `gc` confirms, before it frees an object, that the cached `INDEX.bin` of
   the disc of the object's own record lists the object. When the cache does
   not hold that INDEX, `gc` leaves the object alone and reports it.
5. `gc` writes the ON-DISC record and flushes it to the disk before it
   unlinks the staged file. A crash between the two leaves an orphan.
6. `gc` removes `plans/<disc-uuid>/` when every object of that disc is
   ON-DISC. A `pack --out=DIR` outside staging writes no such directory, and
   `gc` never touches it.

## 5. Refs

A ref is a name for a snapshot. With no `--ref`, `commit` moves the ref
named by the local date of today, as `YYYY-MM-DD`. Two commits on one day
move that one name to the newer snapshot; `log` still reaches the older
snapshot. No ref name is reserved.

1. `commit` moves one ref in `refs.txt`, as its last step.
2. `pack` writes into the REFS table of the disc every ref of the ref ledger
   and every ref of `refs.txt` whose snapshot the ledger does not carry yet.
   Thus the newest disc names every ref of the repository.
3. `restore`, `ls` and `log` resolve a ref name from the cached REFS tables,
   or from the REFS tables of the given discs. The newest record of a name
   wins, as FORMAT.md's "Ref" states.
4. `recover` writes every ref that the fed discs carry into `refs.txt` and
   into the ref ledger.

Each snapshot is a root snapshot: the build writes no parent id. `log` orders
snapshots by their time.

## 6. Concurrency and locking

One process at a time writes the local state of a repository.

1. `<repo>/lock` is the lock file. A command that writes the state log, the
   staging store, the ledgers or the config takes a non-blocking exclusive
   advisory lock (`flock`) on it, and holds it until it exits. Those commands
   are `init`, `commit`, `pack`, `gc`, `disc burned`, `recover`, and `verify`
   when it has a repository.
2. A read-only command takes no lock: `ls`, `log`, `status`, `pack --dry-run`,
   `verify` with no repository, every mode of `restore`, and `image build`.
3. A command that cannot get the lock fails at once with exit code 1. The
   message names the lock file. It never waits.
4. There is no separate cache lock and no drive lock.

The lock is advisory. It is not a security boundary.

## 7. Commit

### 7.1 Commit flow

1. Resolve the source root: the `SOURCE` argument, else `sources.root`.
2. Walk the source. Leave out each excluded path.
3. For each regular file: chunk it, hash each chunk with SHA-256, and compress
   each chunk with zstd. Write a chunk into staging only when the state log
   does not know its id. Write the blob object that lists the chunks.
4. For each directory, bottom-up: write the tree object.
5. Write the snapshot object, with the root tree, the time and the `-m`
   message.
6. Record every new object in the state log as STAGED.
7. Move the ref in `refs.txt`.

FORMAT.md's "Objects", "Chunking" and "Compression" give the shapes and the
constants. No config key changes them. Only two config keys change a disc
byte: `fec.scheme`, and `pack.capacity`, which `DISC.bin` records.

`commit` reads every file of the source on every run. An unchanged file gives
the same chunk ids, thus `commit` writes no new chunk for it. NoahsArk has no
scheduler and no daemon.

### 7.2 Source policy

| Rule | Behaviour |
|---|---|
| Source roots | One source root for each commit. It is opened read-only. |
| Symlinks | Never followed. The link itself is stored. |
| FIFO, socket, device node | Recorded by type, with no content. `commit` warns about each one. |
| Unreadable or vanished file | Skipped and reported. The snapshot is still written. Exit code 1. |
| Mount points | Crossed by default. With `--one-file-system`, a directory on another device stays in the tree as an empty directory, and `commit` prints one line for it. |
| Owner | The build records mode, mtime, uid and gid, and the user name and the group name TLVs when the host names the ids. |

FORMAT.md's "The root tree" gives the root name encoding. `restore SNAPSHOT
OUT-DIR` creates `OUT-DIR/<root path>`.

### 7.3 In-flight change detection

`commit` stats a file, reads and chunks it, and stats it again. When the size
or the mtime differs, it reads the file again, one time. When the file still
differs, `commit` stores the content that it read last, sets the `UNSTABLE`
flag of FORMAT.md's "Entry flags" on the entry, and prints `unstable PATH
branch=flagged`. The exit code is then 1. This detection always runs. A
read-only filesystem snapshot (btrfs, LVM or ZFS) as the source removes
in-flight changes.

### 7.4 Excludes

`commit --exclude=PATTERN` (repeatable) and a `.noahsarkignore` file in the
source root together name every path that `commit` leaves out. There is no
negation, thus order never matters.

- One pattern on each line. `#` starts a comment. An empty line is ignored.
- A pattern with no `/` matches a name at every depth (`*.tmp`).
- A pattern with a `/` is anchored at the source root (`/cache`, `build/out`).
- A trailing `/` matches a directory only. A leading `./` is stripped.
- `*` and `?` do not cross `/`. `**` crosses directories. `[abc]` classes
  work as in Go's `path.Match`.
- A pattern that starts with `!` is an error, with exit code 2.

A matched directory is not walked. `commit` prints `excluded: N path(s)`.
`.noahsarkignore` itself is backed up. The snapshot does not store the rules.

## 8. Packing and locality

### 8.1 Packing rules

1. `pack` fills exactly one run for each invocation, and one run goes on one
   disc. Data that does not fit stays STAGED for the next `pack`.
2. `pack` always packs from the full STAGED pool. It has no option that
   selects objects.
3. `pack` walks the tree of every staged snapshot in post-order: the chunks
   of a file, then its blob, then the tree of its directory. It takes the
   longest prefix of that order that passes "The budget formula". Thus the
   chunks of a file and the files of a directory stay together.
4. A prefix is dependency-closed: a child that the prefix does not hold is
   already on an earlier disc. `pack` records that disc in the Prereqs table
   of INDEX.
5. Every run carries REFS, DISCS and every snapshot object that staging
   holds, as FORMAT.md's "Catalog contents per run" states.
6. `pack` refuses a capacity that holds not one object, with exit code 2.

### 8.2 Integrity checks and durable recording

`pack` checks the content id of every staged object that it selects. It
checks a chunk while it copies the chunk into the disc root. When an object
fails, `pack` removes the part-written disc root and records nothing. The
operator runs `commit` again, then `pack` again.

`pack` syncs every file and every directory of the disc root after the write.
It marks the objects PACKED and saves the ledgers only after that sync. An
interrupted `pack` leaves only a part-written disc root. `pack` refuses an
`--out` directory that holds files.

### 8.3 Dry run

`pack --dry-run` answers "how many discs does the staged data need?". It
writes nothing, uses no sequence number and takes no lock. It prints one
`disc N: objects, bytes` line for each disc and a `total:` line. The numbers
are an estimate.

## 9. Capacity budget

### 9.1 Capacity

The operator gives the capacity with `pack --capacity`, or one time in the
config with `pack.capacity`. `pack` reads no drive. With neither, `pack`
refuses. The value is a preset name, or a size with a decimal unit (`k`, `M`,
`G`, `T`, `kB`, `MB`, `GB`, `TB`) or a binary unit (`Ki`, `Mi`, `Gi`, `Ti`,
`KiB`, `MiB`, `GiB`, `TiB`). `G` is not `Gi`. A bare number is a usage error.

| Preset | Media | Sectors | Bytes |
|---|---|---:|---:|
| `dvd+r` | DVD+R 4.7 GB | 2,295,104 | 4,700,372,992 |
| `dvd-r` | DVD-R 4.7 GB | 2,298,496 | 4,707,319,808 |
| `bd25` | BD-R / BD-RE SL 25 GB | 12,219,392 | 25,025,314,816 |
| `bd50` | BD-R / BD-RE DL 50 GB | 24,438,784 | 50,050,629,632 |
| `bd100` | BD-R XL / BD-RE XL TL 100 GB | 48,878,592 | 100,103,356,416 |
| `bd128` | BD-R XL QL 128 GB | 62,500,864 | 128,001,769,472 |

A drive can report fewer sectors than the preset. The operator checks the
blank disc with `dvd+rw-mediainfo` and gives the smaller size. A capacity
below the real size of the medium is valid. `pack` records the value that it
used as `capacity_sectors`, as FORMAT.md's "Disc superblock" states. The
packer, the image length and the FEC layout all use this one value. The media
type in `DISC.bin` follows the preset, else `BD-R-SL-25`. It is informational.

### 9.2 The budget formula

A run fits when this holds, in bytes:

```
stream_bytes + run_header_copies + checksum_bytes + parity_bytes
    + filesystem_overhead  <=  capacity_sectors * 2048
```

`checksum_bytes` and `parity_bytes` are zero when FEC is off. With FEC they
follow FORMAT.md's "Parity layout". `filesystem_overhead` is an estimate of
the UDF metadata: the space bitmap, 4 MiB, two blocks for each file, two
blocks for each directory, and 0.1 percent of the capacity. It changes no
disc byte.

## 10. Disc filesystems and image building

The build writes one run on one UDF disc. FORMAT.md's "The UDF volume" is the
home of the volume rules. `image build` does these steps.

1. Check the `mkudffs` version ("Tool version check").
2. Refuse to go on when it is not root. It prints the exact `sudo noahsark
   image build ...` line to run.
3. Read the capacity from the `DISC.bin` of the disc root. Make a sparse
   image file of that length.
4. Run `mkudffs --utf8 --media-type=hd --blocksize=2048 --udfrev=2.01 --uid=0
   --gid=0 --mode=0555 --bootarea=erase FILE`.
5. Loop-mount the image file, copy the `NOAHSARK` tree into it, and unmount.

Never use `--media-type=bdr` or `dvdr`. Both make a write-once VAT volume,
which cannot be populated. `image build` removes the partial image when a step
fails.

## 11. Burning

### 11.1 Burning is externalized

The program does not burn. It has no `burn` command.

| Step | Owner | Command |
|---|---|---|
| Write the disc root | The program | `noahsark pack` |
| Build the UDF image | The program, as root | `noahsark image build` |
| Burn the image, two times | The operator | the `growisofs` line that `pack` prints |
| Record that the burn occurred | The operator | `noahsark disc burned` |
| Mount the disc | The operator | `mount` |
| Check every object on the disc | The program | `noahsark verify` |

`pack` prints the exact command line of each step, with `/dev/sr0` and speed
4. The operator edits the device and the speed. Use speed 2 for M-DISC.

**Default: left open.**

```bash
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=tree.img
```

`spare:min` formats a blank BD-R with the smallest spare area. `-dvd-compat`
is not passed. The disc stays open.

**Sealed: `pack --close`.**

```bash
growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:none,tty \
          -Z /dev/sr0=tree.img
```

`spare:none` skips the format step, thus there is no defect management.
`-dvd-compat` closes the disc. The choice is permanent.

Never use `-overburn`. Never pass `-M` on a UDF disc. Eject and load the disc
again before the verify, so that the read comes from the medium.

### 11.2 Tool version check

| Tool | Package | Version requirement |
|---|---|---|
| `growisofs` | dvd+rw-tools | Debian 7.1-14 or newer, Fedora 7.1-13 or newer, Arch 7.1-13 |
| `mkudffs` | udftools | 2.3 or newer |

`image build` reads the `mkudffs` version and refuses an older one. The tool
never runs `growisofs`, thus the operator checks that version before the first
burn. "Burning-host command reference" gives the commands.

## 12. Disc lifecycle and closing

A disc is blank, then open after the default burn, or sealed after the burn
that `pack --close` prints. `--close` changes only the printed burn command.
A disc is never closed by default, and there is no config key for it. Every
reader path works on an open disc. The repository does not track the close
state.

A disc is good or is discarded. The operator discards a disc that fails
`verify` and burns the same image on a new disc.

`run_seq` and `disc_seq` are labels for the human. The host assigns them from
local state, thus after a lost repository two discs can carry the same number.
The tool finds a disc by its uuid. One disc holds one run, thus the disc uuid
identifies the run too. The operator writes the label and the storage place on
the sleeve of each disc. The tool keeps no shelf notes.

## 13. Verify and heal

`verify` reads a disc root: the mount point of a disc, the mount point of a
loop-mounted image, or a packed disc root. It reads `DISC.bin`, the run
header, `INDEX.bin` and every object through the filesystem. It checks every
file that INDEX lists against its recorded hash, and every object against its
content id. When the run has FEC, it checks the checksum column and the parity
too.

With a repository, `verify` refuses a disc whose uuid the disc ledger does not
hold. On success it moves the BURNED objects of the disc to CLEAN, adds 1 to
the count of each CLEAN object, and writes the cache entries of the disc. On
failure it moves the BURNED objects back to PACKED. The operator verifies both
copies, the second one in a different drive when one is available.

The operator tries the heal sources in this order.

1. **The second identical disc.** Restore from the good copy. Burn a new copy
   from the kept image, or from an image that `ddrescue` reads from the good
   copy.
2. **On-disc RS parity**, when the run has FEC. `verify --heal --out=DIR`
   writes the healed disc root into `DIR`, and then checks `DIR`. `--heal`
   refuses a run that has no FEC.
3. **Another disc that holds the same content id.** `restore` with all the
   discs finds it through INDEX.
4. **The original source path**, if it still exists. `commit` stores it again.

## 14. Restore

`restore` has two ways to take the discs. The two share one write path, one
report and one set of safety rules.

### 14.1 All discs at once

The operator gives one or more `DISC-ROOT` arguments, or `--discs-dir`. This
mode needs no repository and no cache. It resolves a ref name from the REFS
tables of the given discs, and finds each object through their INDEX files.
If the newest disc is lost, each other disc carries the catalog as of its own
pack. A file that a damaged or a missing object cuts short loses its part
file, and the report names the file. No path with the final name is left.

### 14.2 Disc swap, one drive

`restore --mount=DIR` reads one disc at a time from `DIR`. It needs the
repository and the local cache.

**The plan.** `restore` reads the snapshot, the trees and the blobs from the
cache. It finds the disc of each chunk through the cached INDEX files and
DISCS tables. A chunk that no cached INDEX lists is missing; `restore` then
fails before it reads a disc, and names `recover` as the fix. `restore` prints
the plan first: one `disc SEQ "LABEL" (UUID): N objects, B bytes` line for
each disc, then a `totals:` line. `--dry-run` prints the plan and stops. The
loop asks for the discs in `disc_seq` order, except that a disc that is
already in the drive and still needed is read first.

**Disc detection.** For each disc of the plan, `restore` reads
`NOAHSARK/DISC.bin` below `DIR` and compares the `disc_uuid`.

1. The expected disc: `restore` prints `disc SEQ LABEL: found` and goes on.
2. An unreadable `DISC.bin`: `restore` tries 3 more times with a short pause,
   then prompts.
3. A wrong disc: `restore` names the expected and the found disc, then
   prompts: `insert disc SEQ "LABEL" (uuid UUID) into DIR and press Enter`.
4. `restore` never unmounts and never ejects. The operator swaps the disc in
   a second terminal, mounts it at `DIR`, and presses Enter.

For each disc, `restore` walks the tree of the snapshot one time and writes
each chunk of this disc into the part file of its own file. A file whose
chunks lie on two discs is a normal case. `restore` copies each byte one time.
Nothing that it holds in memory grows with the size of the snapshot.

### 14.3 The part file and resume

`restore` writes the bytes of a file into a hidden part file in the directory
of the file, named `.<name>.noahsark-part`. When the snapshot holds a file of
that name itself, `restore` adds a number to the suffix. The final size is set
one time, so that a file with a hole keeps the hole.

The final name appears one time, when the last chunk has landed: `restore`
links the part file to the final name, then unlinks the part file. A link
fails when the name already exists, thus the no-overwrite rule holds with no
race. With `--overwrite` the path in the way is unlinked first. A filesystem
that has no hard link falls back to a check and a rename.

At each open of a part file, `restore` checks each chunk that is already there
against its content id, and skips the good ones. A killed disc-swap run leaves
its part files. The next run of the same `restore` completes them, and asks
only for the discs that it still needs. `restore` never deletes a part file
that it did not write.

`restore` checks the content id of every object after it reads it. An object
that does not verify fails the one file that needs it. `restore` writes no bad
data, goes on, and reports at the end ("Failure policy").

## 15. Metadata restore policy

FORMAT.md's "Tree entry fixed header" gives the metadata fields.

### 15.1 What restore applies

1. For a file and a directory: the owner first, then mode, then mtime. A
   `chown` clears the setuid and the setgid bits, and the `chmod` that follows
   puts them back. The owner is applied only when `restore` runs as root.
   `restore` applies the uid and the gid, never the name TLVs.
2. For a symlink: the target, as data, never rewritten. As root, the owner,
   with a no-follow `lchown`. The mode and the times of a symlink are not
   restored.
3. Directory metadata is applied in a last pass, deepest directory first.
4. A `restore` that does not run as root does not attempt ownership, and
   prints no owner warning. The exit code stays 0. To restore ownership, run
   `restore` again as root with `--overwrite`.
5. A directory between `OUT-DIR` and the source root that no tree entry
   describes is created with mode 0755.
6. Each hardlinked source path is restored as an independent file, as
   FORMAT.md's "Hardlinks" states.
7. `restore` does not create a FIFO, a socket or a device node.
8. `restore` does not read the `UNSTABLE` flag. `ls` marks such an entry with
   `!`.

### 15.2 Failure policy

`restore` is strict for data and best-effort for metadata. It has one report.
Each problem carries the path, a kind and the reason.

| Kind | Meaning | Exit code |
|---|---|---:|
| existing path | The path is already there and `--overwrite` was not given. `restore` left it as found. | 1 |
| `--overwrite` could not replace | A directory that holds entries stood where a file or a symlink must go. `restore` never removes a directory tree. | 1 |
| unsupported entry | A device node, a FIFO or a socket. | 0 |
| metadata field | A `mode`, `times` or `owner` field that would not apply. | 1 |
| file not restored | A bad object, or a write that failed. | 1 |

`restore` prints one line for each problem, 20 lines at most, then the count
of the rest, then one summary line that counts each kind.

### 15.3 Name and symlink safety

1. `restore` refuses an entry whose name is empty, is `.` or `..`, or contains
   `/`, `\` or NUL.
2. `restore` never follows a symlink in the output directory. It checks each
   path component with `lstat` before it goes below it.
3. `restore` opens an output file with no-follow.
4. With `--overwrite`, `restore` unlinks the existing path first and then
   creates the new one. It never opens an existing path for truncation.
5. A symlink target is stored and restored as data.

## 16. CLI reference

### 16.1 Commands and global options

The commands are `init`, `commit`, `pack`, `image build`, `verify`, `restore`,
`ls`, `log`, `status`, `recover`, `disc burned` and `gc`. Each command prints
its syntax and its options with `-h`, and exits with code 0.

- Put every option before the positional arguments. The build refuses an
  option that comes after a positional argument, and names it.
- `--repo=PATH` names the repository ("Repository discovery"). Give it after
  the command name. Every command but `image build` takes it.
- A long command writes a progress line to standard error, only when standard
  error is a terminal. `--no-progress`, `--quiet` and `-q` turn it off.
- A `DISC` argument names one disc of the repository: the `disc_seq`, the full
  uuid, a uuid prefix, or the exact label. A command refuses a value that
  matches no disc or more than one disc, and lists the candidates.
- A `DISC-ROOT` argument and `--mount=DIR` name a directory: the mount point
  of a disc, or a copy of a disc root. `--discs-dir=DIR` names a directory
  whose immediate subdirectories are disc roots.

### 16.2 Syntax

```
noahsark init    [--repo=PATH] [--source=PATH]
noahsark commit  [--repo=PATH] [--ref=NAME] [-m MESSAGE] [--exclude=PATTERN]...
                 [--one-file-system] [SOURCE]
noahsark pack    [--repo=PATH] [--capacity=SIZE] [--label=TEXT] [--out=DIR]
                 [--fec] [--close] [--dry-run]
noahsark image build --out=FILE [--force] TREE-DIR
noahsark disc burned [--repo=PATH] [--undo] DISC [DISC...]
noahsark verify  [--repo=DIR] [--heal --out=DIR] DISC-ROOT
noahsark status  [--repo=PATH]
noahsark gc      [--repo=PATH] [--dry-run] [--force-after=DURATION]
noahsark restore [--include=PATH]... [--overwrite] DISC-ROOT... SNAPSHOT OUT-DIR
noahsark restore [--include=PATH]... [--overwrite]
                 --discs-dir=DIR SNAPSHOT OUT-DIR
noahsark restore [--repo=PATH] [--include=PATH]... [--overwrite] --mount=DIR
                 [--dry-run] SNAPSHOT OUT-DIR
noahsark recover [--repo=PATH] [DISC-ROOT... | --discs-dir=DIR]
noahsark ls      [--repo=PATH] [--long] [--recursive]
                 [--discs-dir=DIR] [DISC-ROOT...] SNAPSHOT [PATH]
noahsark log     [--repo=PATH] [--discs-dir=DIR] [DISC-ROOT...] [REF|SNAPSHOT]
```

### 16.3 Options

| Command | Option | Meaning |
|---|---|---|
| `init` | `--repo` | The directory to create. Default: the current directory. |
| `init` | `--source` | The source root. `init` stores the absolute path as `sources.root`. |
| `commit` | `-m` | The commit message, stored on the snapshot. |
| `commit` | `--ref` | The ref to move. Default: the local date of today, `YYYY-MM-DD`. |
| `commit` | `--exclude` | An exclude pattern ("Excludes"). Repeatable. |
| `commit` | `--one-file-system` | Do not cross a mount point. |
| `pack` | `--capacity` | The target capacity ("Capacity"). Default: `pack.capacity`. |
| `pack` | `--label` | The human label. Default: the name of the newest ref, then `disc SEQ`, for example `2026-09-21 disc 0`. |
| `pack` | `--out` | The directory that receives the disc root. It must be empty or absent. Default `<staging.dir>/plans/<disc uuid>/tree`. |
| `pack` | `--fec` | Write FEC for this run. It overrides `fec.scheme`. |
| `pack` | `--close` | Print the sealed burn command. Nothing else changes. |
| `pack` | `--dry-run` | Predict the disc count and stop ("Dry run"). |
| `image build` | `--out` | Where to write the image. Required. |
| `image build` | `--force` | Replace `--out` when it exists. |
| `disc burned` | `--undo` | Move the BURNED objects of the disc back to PACKED. |
| `verify` | `--repo` | The repository whose staging state to update. |
| `verify` | `--heal` | Repair the disc root with the parity of the run. It requires `--out`. |
| `verify` | `--out` | With `--heal`, the directory that receives the healed disc root. |
| `gc` | `--dry-run` | Print what `gc` would delete, and delete nothing. |
| `gc` | `--force-after` | Shorten the 7-day retention for this run only, after a confirmation. `DURATION` is a whole number of days with a `d` suffix, or a Go duration such as `1h`. |
| `restore`, `recover`, `ls`, `log` | `--discs-dir` | A directory whose immediate subdirectories are disc roots. |
| `restore` | `--include` | Restore only this path, relative to the snapshot. Repeatable. |
| `restore` | `--overwrite` | Unlink an existing path first and then create it. |
| `restore` | `--mount` | The directory where the one drive is mounted. |
| `restore` | `--dry-run` | Print the plan and stop. It needs `--mount`. |
| `ls` | `--long` | Print mode, owner, size and mtime. |
| `ls` | `--recursive` | Descend into subdirectories. |

### 16.4 Command notes

**`init`** writes `repo.uuid` and `staging.dir = staging`. It writes no
capacity. It exits with code 2 when the directory already is a repository. Do
not run `init` to recover a lost repository.

**`commit`** prints the snapshot id, the ref, the counts of new and existing
objects, one line for each unstable, skipped or special path, and the STAGED
total. Exit: 1 when a file was skipped or unstable; the snapshot is committed
all the same. A special file never changes the exit code.

**`pack`** prints `packed disc SEQ "LABEL": N object(s) on the disc, B bytes`,
the `uuid:` and `tree:` lines, a `next steps:` block with the exact `image
build`, `growisofs`, `disc burned` and `verify` lines, then `remaining staged:
N objects, B bytes`. With nothing to pack, it prints the reason and exits 0.
Exit: 2 for a missing capacity, an `--out` directory that holds files, or a
capacity too small for one object.

**`image build`** takes the image length from the `DISC.bin` of `TREE-DIR`.
It prints `built image FILE (N bytes)`. Exit: 1 when it is not root.

**`disc burned`** moves every PACKED object of each named disc to BURNED. The
second copy of the same disc needs no second `disc burned`. `--undo` refuses a
disc that has a CLEAN object, with exit code 1.

**`verify`** prints `disc SEQ "LABEL": N objects, ok`, then, with a
repository, `verify: N object(s) verified on disc SEQ "LABEL" (UUID)` and
`verify: copy 1 of 2 verified; verify the second copy before gc`, or `verify:
2 of 2 copies verified`. When a PACKED object of the disc remains, it prints the `disc
burned` command to run. Exit: 1 when the check or the heal failed, or when the
repository does not know the disc. 2 for `--heal` with no `--out`.

**`status`** prints `staged: N objects, B bytes`, one `disc SEQ "LABEL"  STATE
UUID` line for each disc, and one `next:` line with the one action to take.
`STATE` is `not fed`, `packed`, `burned`, `verified C/N`, `verified` or `on
disc only`. `C` is the lowest verify count of the CLEAN objects of the disc,
and `N` is `gc.min_verified_copies`.

**`gc`** prints `gc: staging: deleted N staged object(s), B bytes` and `gc:
plans: deleted N disc plan directory(ies), B bytes`. When nothing is eligible,
it prints the earliest eligible date. It prints one line for each disc that
holds objects back for the verify count. `--force-after` prints `delete N
object(s), B bytes? [y/N]` and deletes only on `y` or `yes`. Exit: 0 when
nothing was eligible. 1 when a staged file could not be unlinked, and when the
confirmation was refused.

**`restore`** takes a snapshot id, as `log` prints it, or a ref name.
`--mount` does not go together with a `DISC-ROOT` or `--discs-dir`. It prints
the problem lines as `noahsark: restore: warning: PATH: REASON`, then
`restored snapshot ID into OUT-DIR`, then `resumed: N file(s) already
restored` when a file was resumed. Exit: see "Failure policy". 1 also when a
needed disc or object is missing.

**`recover`** builds again, from discs, the config, the disc ledger, the refs,
the state log and the local cache. It creates the repository directory when it
is absent, and merges into the state that exists. With one drive, the operator
runs it one time for each disc, in any order. It refuses discs that do not
share one `repo_uuid`. It records the objects of a fed disc as ON-DISC; do not
run `disc burned` or `verify` again for such a disc. It prints `recover: ok`,
or one `rebuild is partial: disc UUID (LABEL) not fed yet` line for each disc
that a fed disc names and that was not fed, and then exits with code 1.

**`ls`** lists the tree of a snapshot. **`log`** lists every snapshot, newest
first; with a `REF` or a `SNAPSHOT`, it prints the details of that one. With
no disc given, both read the staging store first and the local cache second,
so a snapshot lists before the first `pack`. With disc roots, both read the
discs and need no repository. Every leading argument that is an existing
directory is a `DISC-ROOT`.

## 17. Configuration reference

The config file is `<repo>/config`. It is a plain text file with one
`key = value` pair on each line and `#` for a comment. A CLI option overrides
the file. These are all the keys.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `repo.uuid` | 32 hex digits | generated by `init` | The repository uuid. Never change it. |
| `staging.dir` | path | `staging` | The staging store. A relative path is relative to the repository directory. |
| `sources.root` | path | unset | The source root. `commit` uses it when no `SOURCE` is given. |
| `fec.scheme` | `none` or `rs255-gf8` | `none` | Whether `pack` writes FEC. |
| `pack.capacity` | preset or size | unset | The capacity that `pack` uses with no `--capacity`. |
| `gc.min_verified_copies` | integer, 1 or more | 2 | The successful verifies that an object needs before `gc` frees it. |

## 18. Exit code registry

Every command uses exactly these three codes.

| Code | Meaning |
|---:|---|
| 0 | Success. An outcome that needs no operator action, such as `gc` with nothing eligible, is success. |
| 1 | A failure at run time: a read or a write failed, a needed disc or object is missing, the repository lock is held, or a partial success needs the attention of the operator. |
| 2 | A usage error: a bad option, a bad argument, a bad config value, an unknown command, or no repository for a command that writes one. |

## 19. Failure and recovery actions

| # | Failure, and the message | Recovery action |
|---:|---|---|
| 1 | A burn fails midway | Discard the disc. Burn the same image on a new disc. If `disc burned` already ran, run `disc burned --undo DISC`, then `disc burned DISC` after the good burn. |
| 2 | `verify: DISC failed; the burn mark is removed; N object(s) returned to packed` | Discard the disc. Burn a new disc from the same image, run `disc burned`, then `verify`. |
| 3 | `verify: disc SEQ is not marked burned; run: noahsark disc burned SEQ` | Run the printed command, then `verify` again. |
| 4 | `verify`: `disc UUID (LABEL) is not in repository PATH` | Give the right `--repo`, or run `recover` to add the disc. |
| 5 | One copy of a disc is lost or bad later | Read from the other copy. Burn a new copy from the kept image, or from a `ddrescue` image of the good copy. With FEC, `verify --heal --out=DIR` can repair a copy of the bad disc root. |
| 6 | The two copies of a disc are lost | `restore` with the other discs restores what they hold and names each file that it cannot restore. |
| 7 | `cache: no disc is cached yet; run pack, or recover, first` | `recover`, one time for each disc. |
| 8 | The repository directory is lost | `recover --repo=<new>`, one time for each disc. Do not run `init` first. |
| 9 | `rebuild is partial: disc UUID (LABEL) not fed yet` | Run `recover` with that disc. |
| 10 | `the state log's tail was truncated; ...` | A crash left a torn tail. Run the interrupted command again. For a bad record in the middle of the log, restore `state.db` from a copy, or run `recover` into a new repository. |
| 11 | `repository lock PATH is held; another noahsark command runs on this repository` | Wait for the other command. |
| 12 | `pack` stops: a staged object fails its check, or a sync error occurs | `pack` records nothing. Run `commit` again, then `pack` again. |
| 13 | `pack`: `capacity ... holds not one object` | Give a larger `--capacity`. |
| 14 | `pack`: `no capacity: pass --capacity, or put pack.capacity in PATH` | Do one of the two. |
| 15 | `image build` needs root for the loop mount | Run the printed `sudo` line. |
| 16 | `gc: DISC: C of N copies verified; K object(s) held; verify the second copy` | Verify the second copy. For one copy only, set `gc.min_verified_copies = 1`. |
| 17 | `gc: N object(s) skipped: their disc's INDEX is not cached` | Run `verify` or `recover` with that disc, then `gc`. |
| 18 | `commit`: `unstable PATH branch=flagged`, or `skipped PATH: REASON` | The snapshot is written. Run `commit` again later. |
| 19 | `restore`: `N object(s) have no run known to the cache; run recover with more discs` | Run `recover` with each disc, then `restore`. |
| 20 | `restore`: `expected DISC, found DISC` | Mount the right disc, or its second copy, and press Enter. |
| 21 | A disc-swap restore is killed between two discs | Run the same `restore` again. It completes the part files. |
| 22 | An unknown format version on a disc | The tool refuses the disc. Use a newer tool. |

## 20. Test list

Every burn test uses an image file. `.github/workflows/ci.yml` runs the
composite actions `lint`, `unit` and `e2e` under `.github/actions/`.

**Unit tests** (`go test ./...`), by package:

- `internal/format`: each on-disc structure against a byte-exact golden file;
  canonical tree order; name validation.
- `internal/chunker`: golden cut points across buffer sizes; the Gear table.
- `internal/fec`: repair at exactly `m` erasures, refusal at `m + 1`, burst
  damage, the checksum column, the printed worked example.
- `internal/object`: the commit walk, compression, excludes, one file system,
  the `UNSTABLE` flag, unreadable and vanished files.
- `internal/stage`: the golden state record, replay, the torn tail, a bad
  record in the middle, a close error.
- `internal/image`: packing order, the capacity budget, FEC on and off, the
  dry run, the ledgers, `README.txt` and `FORMAT.txt` against the golden text,
  bounded memory, the UDF image build.
- `internal/cache`, `internal/repolock`: the cache by disc uuid; the lock.
- `internal/restore`: a planted symlink, no overwrite by default, part files
  and resume, a file on two discs, `--include`, metadata as root and not as
  root, hardlinks, special files, case-folded names, heal, bounded memory.
- `cmd/noahsark`: each command: options, output, exit codes, the state
  transitions, the `gc` rules, `recover`, the `DISC` argument.

**The 7 e2e cells** (`test/e2e/disc`, real `mkudffs` images and a real loop
mount):

| Cell | What it proves |
|---|---|
| `media/dvd+r` | The full cycle on a DVD+R size image, with the reference decoder. Then the command-line cycle, and a disc root burned as ISO 9660. |
| `media/bd25-forced-10g` | A 25 GB medium, packed at a 10 GB `--capacity`. |
| `chain/dvd-bd25-bd10` | About 40 GB across three discs. Restore needs all three. A missing disc is named. Data that does not fit stays STAGED. |
| `lowmem` | The 25 GB flow under a memory limit: peak memory does not grow with the data size. |
| `incremental` | A second commit packs only the change. Both snapshots restore from the discs alone. |
| `rebuild` | The repository and the cache are deleted. `log`, `ls` and `restore` work from the discs. `recover` brings back the state, and a later `pack` deduplicates against the discs. |
| `fec` | With `--fec`: heal a corrupt stripe, heal corrupt parity, heal at the maximum damage, refuse above the maximum. |

## 21. Manual physical checklist

These steps need a real drive and real media. Do them before a release and
after a change to the burn path.

1. `dvd+rw-mediainfo` on a blank disc shows the media type and the capacity.
2. A burn completes with the `growisofs` line that `pack` printed.
3. Eject, load, mount, and run `noahsark verify` on the mount point.
4. Burn and verify the second copy, in a second drive when one is available.
5. `noahsark restore` from the mounted disc gives the source tree back.
6. The disc mounts on Windows and on macOS, and `README.txt` is readable.

## 22. Burning-host command reference

```bash
# Check the tool versions: dvd+rw-tools 7.1-14 or newer, udftools 2.3 or newer.
growisofs -version 2>&1 | head -2
mkudffs 2>&1 | head -1
# Look at the blank disc.
dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Free Blocks|Track Size'
# Burn: use the growisofs line that pack printed. Burn it two times.
# After the burn: load the disc again, mark the burn, mount, verify.
eject /dev/sr0 && eject -t /dev/sr0 && sleep 5
noahsark disc burned <DISC>
sudo mount -o ro /dev/sr0 /mnt/ark
noahsark verify /mnt/ark
sudo umount /mnt/ark
# Copy a good disc to an image, to burn a new copy.
ddrescue -b 2048 -n -r1 /dev/sr0 copy.img copy.map
```
