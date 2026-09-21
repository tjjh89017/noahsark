# NoahsArk operations

**Document version 4.0.** Format major 1.

This document states what the NoahsArk build does on the host. It covers the
repository and its files, the staging state machine, commit, pack, the
capacity budget, the image build and the burn commands, verify and heal,
restore, locking, the command-line interface, the configuration keys, the
exit codes and the tests.

`FORMAT.md` is the on-disc format document. It defines every byte that reaches
a disc and every rule a reader follows. This document cites `FORMAT.md` by
heading text and never redefines an on-disc structure. Where a local structure
has its own byte layout, such as the state log, this document carries that
layout, because those bytes never reach a disc.

The code is the truth. This document describes the build as it is. It does
not describe a feature that the build does not have. `NOTES.md`'s "2.21 Later
ideas" lists ideas that are not built.

Section numbers have gaps. A deleted section keeps its number unused, so that
the numbers of the other sections do not move.

## 1. Scope and conventions

1. This document is the authority for host behaviour. `FORMAT.md` is the
   authority for disc bytes. Where the two touch, `FORMAT.md` wins.
2. The build refuses an unknown command, an unknown option and an unknown
   config key. It names the item and exits with code 2.
3. Every size is in bytes unless the text says sectors. A sector is 2048
   bytes.
4. The tool never removes a snapshot and never frees disc space. There is no
   retention and no expiry for a snapshot.
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

## 2. Repository, staging and cache

### 2.1 The repository directory

A repository is one local directory.

| Path | Content |
|---|---|
| `<repo>/config` | The configuration file. It holds `repo.uuid`. |
| `<repo>/lock` | The repository lock file. |
| `<repo>/refs.txt` | The local refs. |
| `<repo>/staging/` | The staging store, unless `staging.dir` moves it. |
| `<repo>/cache/` | The local cache. See "Local cache layout". |

`init` creates the directory, `config`, `lock`, and `staging/` with the empty
directories `objects/` and `snapshots/`. The first `commit` creates `refs.txt`
and the state log. `cache/` appears the first time a command populates it. A
directory is a repository when it holds a `config` file.

Every disc carries the `repo_uuid`. `recover` recreates a lost repository from
its discs. The repository is the source of truth only for the objects that are
not yet CLEAN and for the refs that no disc carries yet.

### 2.2 Repository discovery

The first hit wins: `--repo=PATH`; then the environment variable
`NOAHSARK_REPO`; then the current directory and each ancestor of it.

With no repository, `commit`, `pack`, `gc`, `status` and `disc burned` exit
with code 2. `ls`, `log` and `restore --mount` reach the local cache through
the repository, and exit with code 1. `recover` creates the repository
directory; it needs `--repo` or `NOAHSARK_REPO` then. `verify` still checks
the disc root and changes no state.

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

Everything in the cache comes from discs or from staging, and `recover` builds
it again from the discs. The cache never holds anything whose loss loses
archive data, and it holds no chunk data. The location is always
`<repo>/cache/`, beside `staging/` and `config`. There is no override.

| Item | Content |
|---|---|
| `discs/<disc-uuid>/INDEX.bin`, `REFS.bin`, `DISCS.bin` | Byte copies of the index and the catalog of the disc with this uuid, as FORMAT.md's "The run index and the catalog" defines them. The key is the disc uuid, never `run_seq`: after a lost repository two discs can carry the same number. |
| `snapshots/<id>`, `trees/<id>`, `blobs/<id>` | A byte copy of every cached snapshot object, and of every tree and blob object that it reaches. |
| `state.txt` | For each snapshot id, whether its tree set is complete in the cache. |

`pack` writes the cache entries of the disc that it packs. `verify` with a
repository and `recover` write them from a disc. `gc` never trims the cache.

### 2.7 Cache-less operation

`commit`, `pack`, `status`, `disc burned` and `verify` do not read the cache.
`restore`, `ls` and `log` with disc roots read the given discs alone, and need
no repository. `restore --mount`, and `ls` and `log` with no disc root, need
the cache: run `recover` first, one time for each disc. `gc` frees nothing for
a disc whose INDEX is not cached, and says so. CI deletes the repository and
the cache and restores from the disc images alone.

## 3. Local file formats

No byte of these files reaches a disc. Every integer is little-endian, and
every CRC is CRC-32C.

### 3.2 State log record

`<staging>/state.db` is the staging state log. It is append-only. It has no
header. It is a whole number of fixed-width records, each 79 bytes:

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

The record holds no object kind, length or transition time. The run index
holds the kind and the length.

### 3.3 Local refs

`<repo>/refs.txt` is a text file. Each line holds a ref name, one space, and
a snapshot id in text form. `commit` replaces the line of the ref that it
moves. `recover` writes the file from the REFS tables of the discs.

### 3.4 Ledgers

`<staging>/discs.bin` is the disc ledger: one row for each disc that `pack`
wrote or `recover` read. It uses the container of FORMAT.md's "DISCS". `pack`
takes the next `run_seq` and `disc_seq` from the maximum that the ledger holds.
A fresh repository starts at `run_seq` 1 and `disc_seq` 0.

`<staging>/refslog.bin` is the ref ledger: every ref record that a disc
already carries. It uses the container of FORMAT.md's "REFS". `pack` merges the
refs of `refs.txt` into it, so that each disc carries every ref that the
repository knows.

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
                                                verify ok v
                                                     +--------+
                                                     | CLEAN  |
                                                     +--------+
                                                          |
                        gc, after staging.retain_after_clean (7 days)
                        and gc.min_verified_copies verifies (2)
                                                          v
                                                     +---------+
                                                     | ON-DISC |
                                                     +---------+
```

ON-DISC means that a disc holds the object and staging holds no file for it.
It is the last state.

### 4.2 Transition rules

1. `commit` is the only entry point. A new object enters at STAGED. `commit`
   records every new object as STAGED before it moves the ref.
2. `pack` moves the objects that it puts on a disc from STAGED to PACKED.
   There is no transition from PACKED back to STAGED.
3. The tool never concludes by itself that a burn occurred. `disc burned` is
   the only transition from PACKED to BURNED. `verify` never moves a PACKED
   object: a disc root that passes `verify` can be an image that no one
   burned.
4. `verify` is the only transition from BURNED to CLEAN. There is no timer
   and no manual override.
5. Each object counts its successful verifies. A successful `verify` of a
   BURNED object moves it to CLEAN and sets the count to 1. A successful
   `verify` of a CLEAN object adds 1 to the count. The count stops at 255.
6. The clean time is the time of the first successful verify. A later verify
   does not change it. The retention period counts from the first verify.
7. The two identical copies of a disc carry the same disc uuid. The tool
   cannot tell one copy from the other. The count counts successful verify
   passes, not distinct physical discs.
8. `disc burned --undo` moves the BURNED objects of a disc back to PACKED,
   with reason 1. It refuses a disc that has a CLEAN object.
9. A failed `verify` moves the BURNED objects of the disc back to PACKED, with
   reason 2. It leaves a CLEAN object alone and does not change its count.
   The disc root and the image stay on the local disk, thus the operator burns
   the same image on a new disc.
10. `gc` moves a CLEAN object to ON-DISC. `recover` records each object that it
    reads from a disc as ON-DISC, and leaves an object that the log already
    knows at its own state. An ON-DISC object needs no `disc burned` and no
    `verify`.

### 4.3 State log replay

The current state of an object is the newest record for that id. A reader
replays the log from the start. The build never compacts the log.

There is one torn-tail rule, and it runs at open. A partial record at the end
of the file, or a last record with a bad CRC, is what a crash during an append
leaves. A command that holds the repository lock cuts the file back to the
last good record, prints one warning, and goes on. A read-only command ignores
the torn tail and never changes the file.

A bad record anywhere else is damage, because good records follow it. The tool
reports an error, names the record, changes no byte of the file, and stops.

Each append reports an error from the write or from the close of the log file.
The command then stops and does not act on that record.

### 4.5 GC rules

1. `gc` frees the staged file of a CLEAN object only. It also frees an orphan:
   a staged file whose object is already ON-DISC.
2. `gc` frees it only after `staging.retain_after_clean`, default 7 days, has
   passed since the first successful verify. `--force-after` shortens this
   period for one run, and asks for a confirmation.
3. `gc` frees it only when its verify count is at least
   `gc.min_verified_copies`, default 2. Two identical discs are the
   redundancy, thus `gc` holds the staged data until the second copy passes
   `verify`. No option passes by this count. An operator who keeps one copy
   only sets `gc.min_verified_copies = 1`.
4. `gc` confirms, before it frees an object, that the cached `INDEX.bin` of
   the disc of the object's own record lists the object. When the cache does
   not hold that INDEX, `gc` leaves the object alone and reports it.
5. `gc` writes the ON-DISC record and flushes it to the disk before it
   unlinks the staged file. A flush error stops `gc` before the unlink. A
   crash between the two leaves an orphan, and the next `gc` unlinks it.
6. `gc` removes `plans/<disc-uuid>/` of a disc when every object of that
   disc is ON-DISC, whether an earlier run made it so or this run does. The
   directory holds the disc tree and the image, which are a second copy of
   bytes the disc now holds. A disc that is packed or burned, or that has
   fewer than `gc.min_verified_copies` verifies, keeps its directory. A
   `pack --out=DIR` outside staging writes no such directory, and `gc` never
   touches it.
7. `gc` is a separate command. The operator runs it. `gc` never trims the
   local cache.

## 5. Refs

A ref is a name for a snapshot. With no `--ref`, `commit` moves the ref
named by the local date of today, as `YYYY-MM-DD`. Two commits on one day
move that one name to the newer snapshot; `log` still reaches the older
snapshot. No ref name is reserved.

1. `commit` moves one ref in `refs.txt`, after it has written the snapshot
   object and recorded every object as STAGED.
2. `pack` writes into the REFS table of the disc every ref that an earlier
   disc carries, from the ref ledger, and every ref of `refs.txt` whose
   snapshot the ledger does not carry yet. Thus the newest disc names every
   ref of the repository.
3. `restore`, `ls` and `log` resolve a ref name from the cached REFS tables,
   or from the REFS tables of the given discs. The newest record of a name
   wins, as FORMAT.md's "Ref" states. A snapshot that no `pack` has put on a
   disc is not visible to these commands.
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
   `restore` writes only below its own output directory.
3. A command that cannot get the lock fails at once with exit code 1. The
   message names the lock file and says that another noahsark command runs on
   this repository. It never waits.
4. There is no separate cache lock and no drive lock.
5. A read-only command that opens the state log during a write replays it to
   the last valid record and never truncates the file.

The lock is advisory. It is not a security boundary.

## 7. Commit

### 7.1 Commit flow

1. Resolve the source root: the `SOURCE` argument, else `sources.root`.
2. Walk the source. Leave out each excluded path.
3. For each regular file: chunk it with FORMAT.md's one set of chunker
   parameters, hash each chunk with SHA-256, and compress each chunk with
   zstd. Write a chunk into staging
   only when the state log does not know its id. Write the blob object that
   lists the chunks of the file.
4. For each directory, bottom-up: write the tree object.
5. Write the snapshot object, with the root tree, the time and the `-m`
   message.
6. Record every new object in the state log as STAGED.
7. Move the ref in `refs.txt`.

FORMAT.md's "Objects" gives the shape of every object. The build holds the
hash algorithm, the chunker profile, the zstd level and the minimum
compression gain of 5 percent as constants. Only two config keys change a
disc byte: `fec.scheme`, and `pack.capacity`, which `DISC.bin` records.

`commit` reads every file of the source on every run. An unchanged file
gives the same chunk ids, thus `commit` writes no new chunk for it. `commit`
always writes a new snapshot object, because the snapshot holds the commit
time.

NoahsArk has no scheduler and no daemon. The operator or an external scheduler
runs `commit`. The repository lock keeps two commits from running at once.

### 7.5 Source policy

| Rule | Behaviour |
|---|---|
| Source roots | One source root for each commit. |
| Sources | Opened read-only, never written. Nothing is copied first. |
| Symlinks | Never followed. The link itself is stored. |
| FIFO, socket, device node | Recorded by type, with no content. `commit` warns about each one. |
| Unreadable or vanished file | Skipped and reported. The snapshot is still written. Exit code 1. |
| Mount points | Crossed by default. With `--one-file-system`, a directory on another device is not walked. Its own entry stays in the tree as an empty directory, and `commit` prints one line for it. There is no config key for it. |
| Owner | The build records mode, mtime, uid and gid. It also records the user name and the group name TLVs when the host names the ids. A lookup that fails records no name and is not an error. |

FORMAT.md's "The root tree" gives the root name encoding. `restore SNAPSHOT
OUT-DIR` creates `OUT-DIR/<root path>`.

### 7.6 In-flight change detection

`commit` stats a file, reads and chunks it, and stats it again. When the size
or the mtime differs, it reads the file again, up to `commit.retry_unstable`
times. When the file still differs, `commit` stores the content that it read
last, sets the `UNSTABLE` flag of FORMAT.md's "Entry flags" on the entry, and
prints `unstable PATH branch=flagged`. The exit code is then 1. The operator
runs `commit` again later.

`commit.restat_after_read = false` turns the detection off. Never set it
false on a live source. A read-only filesystem snapshot (btrfs, LVM or ZFS) as
the source removes in-flight changes entirely.

### 7.8 Excludes

`commit --exclude=PATTERN` (repeatable), the config key `sources.exclude`
(repeatable, one pattern on each key line), and a `.noahsarkignore` file in
the source root together name every path that `commit` leaves out. A path that
any one of them excludes is excluded. There is no negation, thus order never
matters.

- One pattern on each line. `#` starts a comment. An empty line is ignored.
- A pattern with no `/` matches a name at every depth (`*.tmp`,
  `node_modules`).
- A pattern with a `/` is anchored at the source root (`/cache`, `build/out`).
- A trailing `/` matches a directory only.
- `*` and `?` do not cross `/`. `**` crosses directories. `[abc]` classes
  work as in Go's `path.Match`.
- A leading `./` is stripped before matching.
- A pattern that starts with `!` is an error. `commit` names the pattern, and
  for `.noahsarkignore` the file and the line. The exit code is 2.

A matched directory is not walked. `commit` prints `excluded: N path(s)`.
`.noahsarkignore` itself is backed up like any other file. Only the ignore
file in the source root is read. The exclude rule set is not stored on the
snapshot.

## 8. Packing and locality

### 8.1 Packing rules

1. `pack` fills exactly one run for each invocation, and one run goes on one
   disc. Data that does not fit stays STAGED for the next `pack`.
2. `pack` always packs from the full STAGED pool, across every snapshot of
   the repository. `--ref` and `--snapshot` do not select objects.
3. `pack` walks the tree of every staged snapshot, in ascending snapshot id
   order, in post-order: the chunks of a file, then its blob, then the tree of
   its directory. It keeps the objects that are not on a disc yet. It takes
   the longest prefix of that order that passes the capacity check of "The
   budget formula". Thus the chunks of a file and the files of a directory
   stay together, and a split occurs only at the tail of a run.
4. A prefix is dependency-closed: a child that the prefix does not hold is
   already on an earlier disc. `pack` records that run in the Prereqs table of
   INDEX.
5. Every run carries REFS, DISCS and every snapshot object that staging
   holds, as FORMAT.md's "Catalog contents per run" states.
6. `pack` refuses a capacity that holds not one object. The message names the
   smallest staged object and its size. The exit code is 2.

### 8.8 Integrity checks and durable recording

`pack` checks the content id of every staged object that it selects, so that a
corrupt staged file cannot reach a disc. It checks a chunk while it copies the
bytes of the chunk into the disc root.

When an object fails this check, `pack` removes the part-written disc root. It
records no object as PACKED, saves no ledger, and uses no sequence number. The
operator runs `commit` again to write the staged object again, then runs
`pack` again.

`pack` syncs every file and every directory of the disc root after the write.
It marks the objects PACKED and saves the ledgers only after that sync. A sync
error is a failure at run time: `pack` records nothing.

An interrupted `pack` therefore leaves one thing: a part-written disc root
under `--out`. It leaves no PACKED record, and the sequence numbers stay
free. `pack` refuses an `--out` directory that holds files. The operator
deletes the part-written directory and runs `pack` again.

### 8.9 Dry run

`pack --dry-run` answers "how many discs does the staged data need, at this
capacity?". It writes nothing, uses no sequence number and takes no lock. It
runs the selection of a real `pack` one time for each predicted disc. It
prints one `disc N: objects, bytes` line for each disc, a `total:` line, and a
line that says that the numbers are an estimate: the DISCS table of a real
disc grows by one row on each later disc.

## 9. Capacity budget

### 9.1 Capacity

- The operator gives the capacity with `pack --capacity`, or one time in the
  config with `pack.capacity`. `pack` reads no drive. With neither, `pack`
  refuses and names both ways.
- The value is a preset name of the table below, or a size with a decimal
  unit (`k`, `M`, `G`, `T`, `kB`, `MB`, `GB`, `TB`) or a binary unit (`Ki`,
  `Mi`, `Gi`, `Ti`, `KiB`, `MiB`, `GiB`, `TiB`). `G` is not `Gi`. A bare
  number is a usage error.
- The media type that `DISC.bin` records follows the preset, else
  `BD-R-SL-25`. It is informational only.

### 9.2 Media capacity table

| Preset | Media | Sectors | Bytes |
|---|---|---:|---:|
| `dvd+r` | DVD+R 4.7 GB | 2,295,104 | 4,700,372,992 |
| `dvd-r` | DVD-R 4.7 GB | 2,298,496 | 4,707,319,808 |
| `bd25` | BD-R / BD-RE SL 25 GB | 12,219,392 | 25,025,314,816 |
| `bd50` | BD-R / BD-RE DL 50 GB | 24,438,784 | 50,050,629,632 |
| `bd100` | BD-R XL / BD-RE XL TL 100 GB | 48,878,592 | 100,103,356,416 |
| `bd128` | BD-R XL QL 128 GB | 62,500,864 | 128,001,769,472 |

A drive can report fewer sectors than the preset. The operator checks the
blank disc with `dvd+rw-mediainfo` and gives the smaller size as `--capacity`.

### 9.3 The budget formula

A run fits when this holds, in bytes:

```
stream_bytes + run_header_copies + checksum_bytes + parity_bytes
    + filesystem_overhead  <=  capacity_sectors * 2048
```

`checksum_bytes` and `parity_bytes` are zero when FEC is off, which is the
default. With FEC they follow FORMAT.md's "Parity layout".
`filesystem_overhead` is an estimate of the UDF metadata: the space bitmap,
4 MiB, two blocks for each file, two blocks for each directory, and a margin
of 0.1 percent of the capacity. The estimate is a heuristic. It changes no
disc byte.

### 9.4 Forced capacity

A `--capacity` value below the capacity of the medium is a forced capacity.
`pack --physical-capacity` gives the capacity of the medium; it defaults to
the `--capacity` value. `pack` records the value it used, `--capacity`, as
`capacity_sectors` in `DISC.bin` and in the DISCS row, as FORMAT.md's "Disc
superblock" states: the disc carries the one capacity that `pack` used, not
the medium's own reported capacity. `pack` refuses a `--capacity` above
`--physical-capacity`.

Everything that consumes capacity uses the forced value: the packer, the image
length, and the FEC layout when FEC is on.

## 10. Disc filesystems and image building

The build writes one run on one UDF disc. FORMAT.md's "The UDF volume" is the
home of the volume rules.

### 10.1 Profile 0 image build

`image build` does these steps.

1. Check the `mkudffs` version ("Tool version check").
2. Refuse to go on when it is not root. It never runs `sudo`. It prints the
   exact `sudo noahsark image build ...` line to run.
3. Read the target capacity from the `DISC.bin` of the disc root. Make a
   sparse image file of that length.
4. Run `mkudffs --utf8 --media-type=hd --blocksize=2048 --udfrev=2.01 --uid=0
   --gid=0 --mode=0555 --bootarea=erase FILE`.
5. Loop-mount the image file, copy the `NOAHSARK` tree into it, and unmount.
   This is the one mount that the tool does: it mounts an image file, never a
   device.

Never use `--media-type=bdr` or `dvdr`. Both make a write-once VAT volume,
which cannot be populated. `image build` removes the partial image when a step
fails.

### 10.2 Profile 0 burn paths

The burn is one `growisofs` call, which the operator runs. `pack` prints the
line, with `/dev/sr0` and speed 4. The operator edits the device and the speed
when they differ. Use speed 2 for M-DISC.

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

`spare:none` skips the format step. There is no spare area and no defect
management. `-dvd-compat` closes the disc. The choice is permanent.

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
| Read the disc back and check every object | The program | `noahsark verify` |

`pack` prints the exact `image build`, `growisofs`, `disc burned` and `verify`
command lines for the disc.

Rules for every burn:

- Never use `-overburn`.
- Never pass `-M` on a UDF disc.
- Eject and load the disc again before the verify, so that the read comes from
  the medium and not from a cache.

### 11.9 Tool version check

| Tool | Package | Version requirement |
|---|---|---|
| `growisofs` | dvd+rw-tools | Debian 7.1-14 or newer, Fedora 7.1-13 or newer, Arch 7.1-13 |
| `mkudffs` | udftools | 2.3 or newer |

`image build` reads the `mkudffs` version and refuses an older one. The tool
never runs `growisofs`, thus it does not check that version. The operator
checks it before the first burn. "Burning-host command reference" gives the
commands.

## 12. Disc lifecycle and closing

A disc is `blank`, then `open` after the default burn, or `sealed` after the
burn that `pack --close` prints. `--close` changes only the printed burn
command. The repository does not track the close state.

A disc is never closed by default. There is no config key for the close
policy. Every reader path works on an open disc.

A disc is good or is discarded. The operator discards a disc that fails
`verify` and burns the same image on a new disc.

`run_seq` and `disc_seq` are labels for the human. The host assigns them from
local state, thus after a lost repository two discs can carry the same number.
The tool finds a disc by its uuid. One disc holds one run, thus the disc uuid
identifies the run too.

The operator writes the label and the storage place on the sleeve of each
disc. The tool keeps no shelf notes.

## 13. Verify and heal

### 13.2 Verify procedure

`verify` reads a disc root: the mount point of a disc, the mount point of a
loop-mounted image, or a packed disc root.

`verify` reads `DISC.bin`, the run header, `INDEX.bin` and every object
through the filesystem. It checks every file that INDEX lists against its
recorded hash, and every object against its content id. When the run has FEC,
it checks the checksum column and the parity too.

With a repository, `verify` refuses a disc whose uuid the disc ledger does not
hold. On success it moves the BURNED objects of the disc to CLEAN, adds 1 to
the count of each CLEAN object, and writes the cache entries of the disc. On
failure it moves the BURNED objects back to PACKED.

The operator verifies both copies. `gc` frees the staged data only at
`gc.min_verified_copies` verifies. Verify the second copy in a different drive
when one is available.

### 13.4 Heal order

The operator tries the sources in this order.

1. **The second identical disc.** Restore from the good copy. Burn a new copy
   from the kept image, or from an image that `ddrescue` reads from the good
   copy.
2. **On-disc RS parity**, when the run has FEC. Copy the disc root to the
   local disk. `verify --heal` repairs the copy in place, and
   `verify --heal --out=DIR` writes the healed disc root into `DIR`. Then
   `verify` checks the result. `--heal` refuses a run that has no FEC.
3. **Another disc that holds the same content id.** `restore` with all the
   discs finds it through INDEX.
4. **The original source path**, if it still exists. `commit` stores it again.

## 14. Restore

`restore` has two ways to take the discs. The two share one report and one
set of safety rules.

### 14.1 All discs at once

The operator gives the disc roots: one `DISC-ROOT`, `--disc` for each disc, or
`--discs-dir`. This mode needs no repository and no cache. It resolves a ref
name from the REFS tables of the given discs, and finds each object through
their INDEX files. If the newest disc is lost, each other disc carries the
catalog as of its own pack. It writes each file into a part file, the same way
the disc-swap mode does, and gives the file its final name only after every
chunk is in place and verified. Every disc is given in this mode, thus no later
run can complete a file: a file that a damaged or a missing object cuts short
loses its part file too, and the report names the file. No path with the final
name is left behind.


### 14.2 Disc swap, one drive

`restore --mount=DIR` reads one disc at a time from `DIR`. It needs the
repository and the local cache.

**The plan.** `restore` reads the snapshot, the trees and the blobs from the
cache. It finds the disc of each chunk through the cached INDEX files and
DISCS tables, and groups the chunks by disc. The same inputs give the same
plan. A chunk that no cached INDEX lists is missing; `restore` then fails
before it reads a disc, and names `recover` as the fix. `restore` prints the
plan before it reads the first disc: one `disc SEQ "LABEL" (UUID): N objects,
B bytes` line for each disc, then a `totals:` line, in disc number order.
`--dry-run` prints the plan and stops. The disc-swap loop also asks for the
discs in that same order, lowest `disc_seq` first, because the operator looks
a disc up by the number on its sleeve, except that a disc already in the
drive and still needed is read first, whatever its number.

**Disc detection.** For each disc of the plan, `restore` reads
`NOAHSARK/DISC.bin` below `DIR` and compares the `disc_uuid`.

1. The expected disc: `restore` prints `disc SEQ LABEL: found` and goes on
   with no prompt.
2. An unreadable `DISC.bin`: the drive still settles, or nothing is mounted.
   `restore` tries 3 more times with a short pause, then prompts.
3. A wrong disc: `restore` names the expected and the found disc, then
   prompts: `insert disc SEQ "LABEL" (uuid UUID) into DIR and press Enter`.
   The operator mounts each disc at `DIR`.
4. `restore` never unmounts and never ejects. After each disc, it prints the
   next disc it needs and waits; the operator swaps the disc in a second
   terminal, mounts it at `DIR`, and presses Enter.

**Order.** For each disc, `restore` walks the tree of the snapshot one time
and writes each chunk of this disc into the part file of its own file. The
switch count equals the number of discs in the plan. A file whose chunks lie
on two discs is a normal case: a later disc completes it. `restore` copies
each byte one time, from the disc into the file. Nothing that it holds in
memory grows with the size of the snapshot.

### 14.3 The part file, resume and a killed run

Both modes write through a part file. Only the disc-swap mode keeps one for a
later run. `restore` writes the bytes of a file into a hidden part file in the
directory of the file, named `.<name>.noahsark-part`. When the snapshot holds a
file of that name itself, `restore` adds a number to the suffix. The part file
is opened with no-follow. The final size is set one time, so that a file with a
hole keeps the hole.


The final name appears one time, when the last chunk has landed: `restore`
links the part file to the final name, then unlinks the part file. A link
fails when the name already exists, thus the no-overwrite rule holds with no
race, and a part-written file never carries the final name. With `--overwrite`
the path in the way is unlinked first. A filesystem that has no hard link
falls back to a check and a rename.

At each open of a part file, `restore` checks each chunk that is already
there against its content id, and skips the good ones. A killed run leaves
its part files in the output directory. The next run of the same `restore`
completes them, and asks only for the discs that still hold a chunk that it
needs. A `restore` that completes leaves no part file of its own, and never
deletes a part file that it did not write. The all-discs mode removes its own
part file as soon as a file fails, because no later run can complete it.

`restore` checks the content id of every object after it reads it. An object
that does not verify fails the one file that needs it: `restore` names that
file, writes no bad data into it, and goes on. It walks the whole snapshot and
reports at the end ("Failure policy").

## 15. Metadata restore policy

FORMAT.md's "Tree entry fixed header" gives the metadata fields. `commit`
records mode, mtime, uid and gid, and the user name and the group name TLVs
when the host names the ids. `restore` applies type, mode, mtime, the symlink
target and, as root, the owner. `restore` applies the uid and the gid, never
the name TLVs: a name can point at a different id on the restoring host.

### 15.1 What restore applies

1. For a file and a directory: the owner first, then mode, then mtime. The
   owner comes first because a `chown` clears the setuid and the setgid bits,
   and the `chmod` that follows puts them back. The owner is applied only
   when `restore` runs as root.
2. For a symlink: the target, as data, never rewritten. As root, the owner,
   with a no-follow `lchown`. The mode and the times of a symlink are not
   restored.
3. Directory metadata is applied in a last pass, deepest directory first,
   because a write into a directory changes its mtime.
4. A `restore` that does not run as root does not attempt ownership. Files get
   the uid and gid of the invoking user. It prints no owner warning for any
   path, not even one whose stored owner differs, and the exit code stays 0.
   An ordinary user who restores their own files is the normal case. The
   report holds no ownership problem at all, thus the output says nothing
   about ownership. To restore ownership, run `restore` again as root with
   `--overwrite`.
5. A directory between `OUT-DIR` and the source root that no tree entry
   describes is created with mode 0755. A directory that already exists is
   left as it is.
6. Each hardlinked source path is stored as an independent entry, and
   `restore` creates an independent file for each one, as FORMAT.md's
   "Hardlinks" states. Dedup stores the shared data one time.
7. `restore` does not create a FIFO, a socket or a device node.
8. `restore` does not read the `UNSTABLE` flag. `ls` marks such an entry with
   `!`.

### 15.4 Failure policy

`restore` is strict for data and best-effort for metadata. It has one report.
Each problem carries the path, a kind and the reason.

| Kind | Meaning | Exit code |
|---|---|---:|
| existing path | The path is already there and `--overwrite` was not given. `restore` left it as found. | 1 |
| `--overwrite` could not replace | A directory that holds entries stood where a file or a symlink must go. `restore` never removes a directory tree. | 1 |
| unsupported entry | A device node, a FIFO or a socket. | 0 |
| metadata field | A `mode`, `times` or `owner` field that would not apply to a path that `restore` had written. | 1 |
| file not restored | A bad object, or a write that failed. | 1 |

`restore` prints one line for each problem, 20 lines at most, then the count
of the rest, then one summary line that counts each kind. An unsupported entry
alone never changes the exit code.

### 15.6 Name and symlink safety

1. `restore` refuses an entry whose name is empty, is `.` or `..`, or contains
   `/`, `\` or NUL, when it parses the tree.
2. `restore` never follows a symlink in the output directory. It checks each
   path component with `lstat` before it goes below it. Without `--overwrite`,
   a symlink that stands where a directory must be is reported, and nothing
   is written below it.
3. `restore` opens an output file with no-follow, and with exclusive create
   when it is not a part file.
4. With `--overwrite`, `restore` unlinks the existing path first and then
   creates the new one. It never opens an existing path for truncation. It
   never removes a directory tree.
5. A symlink target is stored and restored as data and is never rewritten.

## 16. CLI reference

### 16.1 Commands and global options

The commands are `init`, `commit`, `pack`, `image build`, `verify`, `restore`,
`ls`, `log`, `status`, `recover`, `disc burned` and `gc`. Each command prints
its syntax and its options with `-h`, and exits with code 0. The lists below
hold every option that the build has. `docs/guide.md` is the operator guide.

- Put every option before the positional arguments. The build refuses an
  option that comes after a positional argument, and names it.
- `--repo=PATH` names the repository ("Repository discovery"). Give it after
  the command name. Every command but `image build` takes it.
- A long command writes a progress line to standard error, only when standard
  error is a terminal. `--no-progress`, `--quiet` and `-q` turn it off. They
  can come before or after the command name.
- A `DISC` argument names one disc of the repository: the `disc_seq`, the full
  uuid, a uuid prefix, or the exact label. A command refuses a value that
  matches no disc or more than one disc, and lists the candidates. Two discs
  can carry the same `disc_seq`; the operator then gives a uuid prefix.
- A `DISC-ROOT` argument, `--disc=ROOT` and `--mount=DIR` name a directory:
  the mount point of a disc, or a copy of a disc root. `--discs-dir=DIR` names
  a directory whose immediate subdirectories are disc roots.
- Every command exits with 0 on success, 1 on a failure at run time and 2 on a
  usage error ("Exit code registry"). The text below names only the cases
  that are not obvious.

### 16.2 Syntax

```
noahsark init    [--repo=PATH] [--source=PATH]
noahsark commit  [--repo=PATH] [--ref=NAME] [-m MESSAGE] [--exclude=PATTERN]...
                 [--one-file-system] [SOURCE]
noahsark pack    [--repo=PATH] [--ref=NAME | --snapshot=ID]... [--capacity=SIZE]
                 [--physical-capacity=SIZE] [--label=TEXT] [--out=DIR]
                 [--fec | --no-fec] [--close] [--dry-run]
noahsark image build --out=FILE [--force] TREE-DIR
noahsark disc burned [--repo=PATH] [--undo] DISC [DISC...]
noahsark verify  [--repo=DIR] [--heal] [--out=DIR] DISC-ROOT
noahsark status  [--repo=PATH] [--json]
noahsark gc      [--repo=PATH] [--dry-run] [--force-after=DURATION]
noahsark restore [--include=PATH]... [--overwrite] DISC-ROOT SNAPSHOT OUT-DIR
noahsark restore [--include=PATH]... [--overwrite]
                 (--disc=ROOT... | --discs-dir=DIR) SNAPSHOT OUT-DIR
noahsark restore [--repo=PATH] [--include=PATH]... [--overwrite] --mount=DIR
                 [--dry-run] SNAPSHOT OUT-DIR
noahsark recover [--repo=PATH] [--disc=ROOT]... [--discs-dir=DIR]
noahsark ls      [--repo=PATH] [--long] [--recursive] [--json] [--unstable-only]
                 [--disc=ROOT]... [--discs-dir=DIR] [DISC-ROOT] SNAPSHOT [PATH]
noahsark log     [--repo=PATH] [--limit=N] [--json] [--disc=ROOT]...
                 [--discs-dir=DIR] [DISC-ROOT] [REF|SNAPSHOT]
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
| `pack` | `--physical-capacity` | The capacity of the medium ("Forced capacity"). Default: the `--capacity` value. |
| `pack` | `--label` | The human label. Default: the name of the ref whose snapshot is newest, then `disc SEQ`, for example `2026-09-21 disc 0`. |
| `pack` | `--out` | The directory that receives the disc root. It must be empty or absent. Default `<staging.dir>/plans/<disc uuid>/tree`. |
| `pack` | `--fec`, `--no-fec` | Write, or do not write, FEC for this run. They override `fec.scheme`. |
| `pack` | `--close` | Print the sealed burn command. Nothing else changes. |
| `pack` | `--dry-run` | Predict the disc count and stop ("Dry run"). It takes no `--out`, `--label`, `--fec`, `--no-fec` or `--close`. |
| `pack` | `--ref` | An extra ref name to carry onto the disc. `pack` carries every pending ref without it. |
| `pack` | `--snapshot` | A snapshot id whose refs the disc carries. Repeatable. Not together with `--ref`. |
| `image build` | `--out` | Where to write the image. Required. |
| `image build` | `--force` | Replace `--out` when it exists. Without it, an existing `--out` is a usage error. |
| `disc burned` | `--undo` | Move the BURNED objects of the disc back to PACKED, for a burn that was bad. |
| `verify` | `--repo` | The repository whose staging state to update. |
| `verify` | `--heal` | Repair the disc root with the Reed-Solomon parity of the run before the check. |
| `verify` | `--out` | With `--heal`, write the healed disc root into this directory, not in place. |
| `status`, `ls`, `log` | `--json` | Print JSON. |
| `gc` | `--dry-run` | Print one `would delete:` line for each disc, and one for each disc plan directory with its bytes, and delete nothing. |
| `gc` | `--force-after` | Shorten the retention for this run only, after a confirmation. `DURATION` is a whole number of days with a `d` suffix, or a Go duration such as `1h`. |
| `restore`, `recover`, `ls`, `log` | `--disc` | A disc root to read. Repeatable. |
| `restore`, `recover`, `ls`, `log` | `--discs-dir` | A directory whose immediate subdirectories are disc roots. |
| `restore` | `--include` | Restore only this path, relative to the snapshot. Repeatable. A directory includes everything below it. |
| `restore` | `--overwrite` | Unlink an existing path first and then create it. Without it, `restore` leaves an existing path alone. |
| `restore` | `--mount` | The directory where the one drive is mounted. There is no config default. |
| `restore` | `--dry-run` | Print the plan and stop. It needs `--mount`. |
| `ls` | `--long` | Print mode, owner, size and mtime. |
| `ls` | `--recursive` | Descend into subdirectories. |
| `ls` | `--unstable-only` | List only the `UNSTABLE` entries. |
| `log` | `--limit` | Print at most N entries. 0 means no limit. |

### 16.4 Command notes

**`init`** writes `repo.uuid` and `staging.dir = staging`. It writes no
capacity: the operator adds `pack.capacity` to the config, or gives
`--capacity` to each `pack`. It exits with code 2 when the directory already
is a repository. Do not run `init` to recover a lost repository.

**`commit`** uses `sources.root` when no `SOURCE` is given. With neither, it
refuses and names the two ways. It prints the snapshot id, the ref, the counts
of new and existing objects, one line for each unstable, skipped or special
path and for each mount point that was not crossed, and the STAGED total. It
prints 20 special file warnings at most, then the count of the rest. Exit: 1
also when a file was skipped or unstable; the snapshot is committed all the
same. A special file never changes the exit code. 2 also for a bad exclude
pattern.

**`pack`** prints `packed disc SEQ "LABEL": N object(s) on the disc, B bytes`,
the `uuid:` and `tree:` lines, a `next steps:` block with the exact `image
build`, `growisofs`, `disc burned` and `verify` lines, then `remaining staged:
N objects, B bytes`. The block repeats `--repo` only when the operator gave it.
With nothing left to pack, `pack` writes no run, prints `pack: nothing to pack:
...` with the reason, and exits 0. Exit: 0 also when objects stay STAGED for
the next disc. 2 also for a missing capacity, an `--out` directory that holds
files, or a capacity too small for one object.


**`image build`** takes the image length from the `DISC.bin` of `TREE-DIR`;
there is no `--capacity` option. It prints `built image FILE (N bytes)`.
Exit: 1 also when it is not root.

**`disc burned`** takes its options after `burned`. It moves every PACKED
object of each named disc to BURNED. It prints `disc SEQ LABEL: marked burned,
N object(s) marked`, or `disc SEQ LABEL: already burned, 0 objects to mark`.
The second copy of the same disc needs no second `disc burned`. `--undo`
refuses a disc that has a CLEAN object, with exit code 1.


**`verify`** prints `disc SEQ "LABEL": N objects, ok` on success, then, with a
repository, `verify: marked N object(s) CLEAN (disc UUID)` and `verify: copy 1
of 2 verified; verify the second copy before gc`, or `verify: 2 of 2 copies
verified`. A verify past `gc.min_verified_copies` prints `verify: verified`,
never "3 of 2". With `--heal` and no `--out`, `verify` prints `heal: no --out;
repairing DISC-ROOT in place` before it writes anything. When a PACKED object
of the disc remains, it prints the `disc burned` command to run. Exit: 1 when
the check or the heal failed, or when the repository does not know the disc.


**`status`** prints `staged: N objects, B bytes`, one `disc SEQ "LABEL"  STATE
UUID` line for each disc, and one `next:` line with the one action to take.
`STATE` is `not fed`, `packed`, `burned`, `verified C/N`, `verified` or `on
disc only`. `not fed` names a disc the ledger knows and `recover` has not read;
`next:` then names `recover`. A repository with no disc and nothing staged says
`next: commit your files, run: noahsark commit <SOURCE>`. `C` is the lowest
verify count of the CLEAN objects of the disc, and `N` is
`gc.min_verified_copies`. `--json` gives the exact numbers.


**`gc`** prints `gc: staging: deleted N staged object(s), B bytes` and `gc:
plans: deleted N disc plan directory(ies), B bytes`. The first line counts
staged object files; the second counts whole disc plan directories. When
nothing is eligible, it prints the earliest eligible date. It prints one line
for each disc that holds objects back for the verify count, and one line for
the objects whose INDEX is not cached. `--force-after` prints `delete N
object(s), B bytes? [y/N]` on standard error and deletes only on `y` or `yes`;
a script answers with a pipe. Exit: 0 also when nothing was eligible. 1 also
when a staged file could not be unlinked, and when the confirmation was
refused.


**`restore`** takes a snapshot id, as `log` prints it, or a ref name. The
first two forms are "All discs at once". The third form is "Disc swap, one
drive"; `--mount` does not go together with a `DISC-ROOT`, `--disc` or
`--discs-dir`. It prints the problem lines of "Failure policy" on standard
error, as `noahsark: restore: warning: PATH: REASON`, then `restored snapshot
ID into OUT-DIR`, `resumed: N file(s) already restored` when a file was
resumed, and the summary line when a problem occurred. Exit: 0 also when the
only problems are unsupported entries. 1 for each other problem kind, and when
a needed disc or object is missing. 2 also for an empty or unknown `SNAPSHOT`,
and for `--dry-run` without `--mount`.

**`recover`** builds again, from discs, the state that the discs can prove:
the config, the disc ledger, the refs, the state log and the local cache. It
creates the repository directory when it is absent, and merges into the state
that exists. With one drive, the operator runs it one time for each disc, in
any order. It refuses discs that do not share one `repo_uuid`, and a
repository whose `repo.uuid` differs from the discs. It records the objects of
a fed disc as ON-DISC; do not run `disc burned` or `verify` again for such a
disc. It prints `recover: ok`, or one `rebuild is partial: disc UUID (LABEL)
not fed yet` line for each disc that a fed disc names and that was not fed,
and then exits with code 1.

**`ls`** lists the tree of a snapshot; it reads tree objects only. **`log`**
lists every snapshot that a disc carries, newest first: id, time, refs, root
paths, object count and size; with a `REF` or a `SNAPSHOT`, it prints the
details of that one snapshot. With no disc given, both read the staging store
first and the local cache second, so a snapshot that `commit` has just written
lists before the first `pack`. A ref name resolves through `<repo>/refs.txt`
first, then through the cached REFS. A tree object that neither the staging
store nor the cache holds is reported by name, with `pack` and `recover` as the
two fixes. A repository with no ref at all fails with the empty-cache message.
With a `DISC-ROOT`, `--disc` or `--discs-dir`, both read the discs and need no
repository. A first argument that is an existing directory is a `DISC-ROOT`.
`ls` marks an `UNSTABLE` entry with `!` in the first column, and with
`"unstable": true` under `--json`.


## 17. Configuration reference

The config file is `<repo>/config`. It is a plain text file with one
`key = value` pair on each line and `#` for a comment. A CLI option overrides
the file. The build refuses an unknown key and names it.

These are all the keys.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `repo.uuid` | 32 hex digits | generated by `init` | The repository uuid. Never change it. |
| `staging.dir` | path | `staging` | The staging store. A relative path is relative to the repository directory. |
| `sources.root` | path | unset | The source root. `commit` uses it when no `SOURCE` is given. One line at most. `init --source` writes it. |
| `sources.exclude` | pattern | unset | An exclude pattern ("Excludes"). Repeatable: one key line for each pattern. |
| `commit.restat_after_read` | boolean | `true` | In-flight change detection. Never set it false on a live source. |
| `commit.retry_unstable` | integer | 1 | How many times `commit` reads an unstable file again. |
| `fec.scheme` | `none` or `rs255-gf8` | `none` | Whether `pack` writes FEC. `pack --fec` and `pack --no-fec` override it for one run. |
| `pack.capacity` | preset or size | unset | The capacity that `pack` uses with no `--capacity`. A bare number is a config error. |
| `staging.retain_after_clean` | duration | `7d` | The retention before `gc` may free a CLEAN object. It counts from the first successful verify. A whole number of days with `d`, or a Go duration. |
| `gc.min_verified_copies` | integer | 2 | The successful verifies that an object needs before `gc` may free it. A value below 1 is a config error. |

## 19. Exit code registry

Every command uses exactly these three codes.

| Code | Meaning |
|---:|---|
| 0 | Success. An outcome that needs no operator action, such as `gc` with nothing eligible, is success. |
| 1 | A failure at run time: a read or a write failed, a needed disc or object is missing, the repository lock is held, or a partial success needs the attention of the operator (an unstable file, a skipped path, a metadata field not applied). |
| 2 | A usage error: a bad option, a bad argument, a bad config value, an unknown command, or no repository for a command that writes one. |

The exit line of each command in the CLI reference states its own conditions.

## 20. Failure and recovery actions

Two identical discs are the primary redundancy. Copy A and copy B hold the
same image.

| # | Failure, and the message | Recovery action |
|---:|---|---|
| 1 | A burn fails midway (`growisofs` reports it) | Discard the disc. Burn the same image on a new disc. If `disc burned` already ran, run `disc burned --undo DISC`, then `disc burned DISC` after the good burn. |
| 2 | `verify` fails, or the disc does not mount: `verify: DISC failed; the burn mark is removed; N object(s) returned to packed` | Discard the disc. Burn a new disc from the same tree, run `disc burned SEQ`, then `verify`. `verify` prints that `next:` line itself. |
| 3 | `verify: disc SEQ is not marked burned; run: noahsark disc burned SEQ` | Run the printed command, then `verify` again. |
| 4 | `verify`: `disc UUID (LABEL) is not in repository PATH` | Give the right `--repo`, or run `recover --disc=ROOT` to add the disc. |
| 5 | One copy of a disc is lost or bad later | Read from the other copy. Burn a new copy from the kept image, or from an image that `ddrescue` reads from the good copy. When the run has FEC, `verify --heal --out=DIR` can repair a copy of the bad disc root. |
| 6 | The two copies of a disc are lost | `restore` with the other discs restores what they hold and names each file that it cannot restore. Then `commit` the source into a new repository. |
| 7 | The local cache is lost: `cache: no disc is cached yet; run pack, or recover, first` | `recover`, one time for each disc. |
| 8 | The repository directory is lost | `recover --repo=<new>`, one time for each disc. Do not run `init` first. The discs come back as ON-DISC; no `disc burned` and no `verify` follow. |
| 9 | `rebuild is partial: disc UUID (LABEL) not fed yet` | Run `recover` with that disc. |
| 10 | `the state log's tail was truncated; N byte(s) after the last valid record were ignored` | A crash left a torn tail. The tool cut it. Run the interrupted command again. A bad record in the middle of the log is an error: the tool changes nothing; restore `state.db` from a copy, or run `recover` into a new repository. |
| 11 | `repository lock PATH is held; another noahsark command runs on this repository` | Wait for the other command, then run the command again. |
| 12 | `pack` stops: a staged object fails its content id check, or a sync error occurs | `pack` records nothing. For a corrupt staged object, run `commit` again. Run `pack` again. |
| 13 | `pack`: `target capacity ... holds not one object; the smallest staged object is ...` | Give a larger `--capacity`. |
| 14 | `pack`: `no capacity: pass --capacity, or put pack.capacity in PATH` | Do one of the two. |
| 15 | `image build`: `populating the UDF image needs root for the loop mount; run: sudo noahsark image build ...` | Run the printed line. |
| 16 | `gc: disc UUID: C of N copies verified; K object(s) held; verify the second copy` | Verify the second copy. For one copy only, set `gc.min_verified_copies = 1`. |
| 17 | `gc: N object(s) skipped: their disc's INDEX is not cached` | Run `verify` or `recover` with that disc, then `gc`. |
| 18 | `commit`: `unstable PATH branch=flagged`, or `skipped PATH: REASON` | The snapshot is written. Run `commit` again later. |
| 19 | `restore`: `N object(s) have no run known to the cache; run recover with more discs` | Run `recover` with each disc, then `restore`. |
| 20 | `restore`: `expected disc UUID (LABEL), found UUID (LABEL)` | Mount the right disc, or its second copy, and press Enter. |
| 21 | A disc-swap restore is killed between two discs | The output directory keeps one `.<name>.noahsark-part` file for each file that is not complete. Run the same `restore` again. |
| 22 | An unknown format version on a disc | The tool refuses the disc. Use a newer tool. |

## 22. Test list

Every burn test uses an image file. Physical burns are a manual checklist, not
CI. `.github/workflows/ci.yml` runs the composite actions `lint`, `unit` and
`e2e` under `.github/actions/`.

**Unit tests** (`go test ./...`), by package:

- `internal/format`: each on-disc structure against a byte-exact golden file;
  canonical tree order; name validation.
- `internal/chunker`: golden cut points; the same cut points across buffer
  sizes; the vendored Gear table.
- `internal/fec`: repair at exactly `m` erasures, refusal at `m + 1`, burst
  damage, the checksum column, the printed worked example.
- `internal/object`: the commit walk, compression, excludes, one file system,
  the `UNSTABLE` flag, unreadable and vanished files.
- `internal/stage`: the golden state record, replay, the torn tail, a bad
  record in the middle, a close error.
- `internal/image`: packing order, the capacity budget, forced capacity, FEC
  on and off, the dry run, the ledgers, `README.txt` and `FORMAT.txt` against
  the golden text, bounded memory, the UDF image build.
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
| `media/dvd+r` | The full cycle on a DVD+R size image: commit, pack, image build, mount, verify, restore, the reference decoder. Then `cli` (the command-line cycle) and `iso` (a disc root burned as ISO 9660 still reads; Joliet does not). |
| `media/bd25-forced-10g` | A 25 GB medium with a forced capacity of 10 GB. |
| `chain/dvd-bd25-bd10` | About 40 GB across three discs of different media. Restore needs all three. A missing disc is named. Data that does not fit stays STAGED. |
| `lowmem` | The 25 GB flow under a process memory limit: peak memory does not grow with the data size. |
| `incremental` | A second commit packs only the change onto a second disc. Both snapshots restore from the discs alone. |
| `rebuild` | The repository and the cache are deleted. `log`, `ls` and `restore` work from the discs. `recover` brings back the state, and a later `pack` deduplicates against the discs. |
| `fec` | With `--fec`: heal a corrupt stripe, heal corrupt parity, heal at the maximum damage, refuse above the maximum. |

## 23. Manual physical checklist

These steps need a real drive and real media. Do them before a release and
after a change to the burn path.

1. `dvd+rw-mediainfo` on a blank disc shows the media type and the capacity.
2. A burn completes with the `growisofs` line that `pack` printed.
3. Eject, load, mount, and run `noahsark verify` on the mount point.
4. Burn and verify the second copy, in a second drive when one is available.
5. `noahsark restore` from the mounted disc gives the source tree back.
6. The disc mounts on Windows and on macOS, and `README.txt` is readable.

## 24. Burning-host command reference

The commands run on the Linux host that holds the drive.

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
