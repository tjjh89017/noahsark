# NoahsArk operations

**Document version 6.0.** Format major 1.

This document states what the NoahsArk build does on the host. `FORMAT.md` is
the authority for every byte on a disc. This document cites `FORMAT.md` by
heading text and never repeats an on-disc structure. It carries the byte
layout of a local file only, because those bytes never reach a disc.

This document and `docs/states.md` are the specification of host-side
behaviour. `docs/states.md` holds the state machines, the state x event
table, and the `next:` blocks of `status`. The code follows the
specification. `docs/guide.md` is the operator guide. It explains the use to
a human. It is not the specification.

## 1. Scope and conventions

1. This document and `docs/states.md` are the authority for host behaviour.
   `FORMAT.md` is the authority for disc bytes. Where they touch, `FORMAT.md`
   wins.
2. The build refuses an unknown command, an unknown option and an unknown
   config key. It names the item and exits with code 2.
3. Every size is in bytes unless the text says sectors. A sector is 2048
   bytes.
4. The tool never removes a snapshot and never frees disc space.
5. The tool counts one verified disc for each pack. A second copy of a disc
   is the job of the operator, and the tool records nothing about it. The
   tool writes no data for error correction: a damaged disc is replaced by
   its second copy. A disc that does not mount counts as dead.
   There is no recovery by carving.
6. The tool never runs `sudo`, a drive tool, or `git`. The tool never mounts a
   device. The operator mounts a disc and gives the mount point. There is one
   exception: `image build` loop-mounts the image file that it builds. The
   operator runs `image build` with `sudo`.
7. Every command is usable from a script. Each confirmation has an answer
   flag ("Confirmations"). `-q` turns off the progress line. `ls` and `log`
   print one record on each line in a fixed field order.
8. The design priorities are ordered: data durability, restore from the
   discs alone with no repository, restore usability, deduplication, speed,
   media utilization.
9. Peak memory follows the chunk size. It never follows the data size.

## 2. Repository, staging and catalog

### 2.1 The repository directory

A repository is one local directory. A directory is a repository when it
holds a `config.yaml` file.

| Path | Git | Content |
|---|---|---|
| `<repo>/.gitignore` | tracked | Written by `init` and `recover`. It holds the two lines `/lock` and `/staging/`. |
| `<repo>/config.yaml` | tracked | The configuration file ("Configuration reference"). |
| `<repo>/state/` | tracked | The state files ("Local file formats"). |
| `<repo>/catalog/` | tracked | The catalog ("Catalog layout"). |
| `<repo>/lock` | ignored | The repository lock file ("Concurrency and locking"). |
| `<repo>/staging/` | ignored | The staging store ("Staging store layout"), unless `staging.dir` moves it. |

The `state/` directory holds these files:

| File | Content |
|---|---|
| `state.db` | The state log ("State log record"). |
| `discstate.db` | The disc state log ("Disc state log record"). |
| `discs.bin` | The disc ledger ("Ledgers"). |
| `refslog.bin` | The ref ledger ("Ledgers"). |
| `refs.txt` | The local refs ("Local refs"). |
| `catalog-state.txt` | The completeness of each snapshot in the catalog ("Catalog layout"). |

Git keeps no empty directory, thus a clone of a repository can lack
`state/`, `catalog/` and `staging/`. `commit` creates each one that is
absent, with mode 0755. `status`, `ls`, `log` and `restore` read a missing
directory as empty, and change no file. The one exception is a missing
staging directory while the state log holds a Staged or a Packed item:
`status` warns about it and exits with code 1, and `commit` refuses, as
"The repository directory" states below.

The tracked part is permanent: the operator can keep it in a version control
system. The ignored part holds only what `gc` frees, and the sequence mark.
The tool never runs `git`. The operator must not use `git`, or any other tool,
to put a file of `state/` or `catalog/` back to an old version. The state logs
are append-only histories; an old version forgets events that the discs
already carry.

The tool detects such a roll back. `staging/state-seq.txt` is the sequence
mark: it holds the `sequence` of the last record of `state.db` and of
`discstate.db`. Git does not track it, thus it does not go back with
`state/`. Each command that holds the repository lock writes the mark after
each append to a log, and when it opens the logs. It writes a temporary file,
syncs it, renames it over the mark, and syncs the directory. The file has two
lines, `state.db N` and `discstate.db N`, with `N` in decimal. When a command
opens the logs, a log whose last `sequence` is below the mark went back:

- A command that holds the lock refuses, before it writes a file, and exits
  with code 1: `noahsark: CMD: state/ went back to an older version: the
  state log ends at record N, but MARK names record M; put state/ and
  catalog/ forward again with git, or run recover with each disc into a new
  repository`. There is one `the LOG ends at record N, but MARK names record
  M` part for each log that went back.
- A command that takes no lock (`status`, `restore`, `ls`, `log`) prints the
  same text on standard error as `noahsark: CMD: warning: TEXT; this command
  changes no file`, and goes on. It never writes the mark.

A log above the mark is not a roll back: a crash between an append and the
write of the mark leaves it. The next command with the lock writes the mark
again.

A staging directory with no mark is not a roll back: a new clone on a second
computer, or a staging directory that the operator deleted and then created
again with `mkdir -p` (a staging directory that does not exist follows below).
The first command with the lock writes the mark. The chunk files of the Staged
items are not there. `status` and `commit` then count only the files that
exist, and warn about the rest ("Command notes"). `pack` takes the other
items, and warns about each item that it cannot take. A `commit` of the same
source writes each missing chunk file again, and `pack` then works. The disc root of a `packed` disc is gone too: its `next:`
block gives `disc lost` ("State to `next:` block" in `docs/states.md`).

A staging directory that does not exist is not the same as an empty one. Its
volume can be unmounted, or `staging.dir` can name a wrong path. The disc
roots and the chunk files are then not gone. Files are expected in the staging
directory only while the state log holds a Staged or a Packed item: the chunk
files of the Staged items, and the disc roots of the discs of the Packed
items. When the state log holds no Staged and no Packed item, a missing
staging directory is normal, for example in a clone of a repository whose data
is all on discs: `status` gives no warning, and a command that needs the
directory creates it. When the state log holds a Staged or a Packed item:

- `status` prints `noahsark: status: warning: staging directory DIR does not
  exist; staging.dir in config.yaml names it` on standard error, the disc
  lines, and the `next:` block of a missing staging directory, and exits
  with code 1. It prints no `staged:` line and no snapshot line. No block
  names `disc lost` for a disc root that is missing with the staging
  directory. `DIR` is the absolute path.
- Each command that takes the lock, other than `recover`, refuses before it
  writes a file, and exits with code 1: `noahsark: CMD: staging directory
  DIR does not exist; mount its volume, or correct staging.dir in
  config.yaml; when the staging store is gone for good, create it with
  mkdir`. `commit` is one of them.
- `recover` creates a missing staging directory, as `init` does.
- The `next:` block of a `packed` disc gives `disc lost` only when the
  staging directory exists and the plan directory of that disc does not.

One computer at a time operates one repository. Two computers must not each
change `state/` and then combine the results: the state logs cannot be joined.

Thus git is safe for these uses: a commit of `state/` and `catalog/` after
each command, a push to a remote as a copy, and a clone to read the history.
Do not check out an old commit into the repository, and do not pull changes
that two computers made on their own.

Every disc carries the `repo_uuid`. `recover` makes a lost repository again
from its discs. The repository is the source of truth only for the items that
no verified disc holds yet and for the refs that no disc carries yet.

### 2.2 Repository discovery

The first hit wins: the global option `--repo=PATH`; then the environment
variable `NOAHSARK_REPO`; then the current directory and each ancestor of it.

`init` does not use discovery. It makes the current directory the repository,
and it refuses `--repo` with exit code 2.

With no repository, every command other than `init`, `recover` and `verify`
prints `no repository; give --repo, or run noahsark init for a new
repository, or noahsark recover for a lost one` and exits with code 2. After
the loss of the repository, the operator runs `recover` with each disc, and
never `init`: a new `init` makes a repository with a new `repo_uuid`, which
the discs do not carry. `restore`, `ls` and `log` print `no repository; run recover first, one
time for each disc` in its place. `recover` creates the repository directory when it is
absent; it needs `--repo` or `NOAHSARK_REPO` then. `verify` with no
repository checks the disc root, records nothing, and prints `not counted: no
repository`.

### 2.3 Staging store layout

```
staging/
    chunks/ab/<id>              chunk files that wait for gc
    plans/<disc-uuid>/tree      the disc root that pack writes, or a symlink to the pack --out directory
    plans/<disc-uuid>/tree.img  the image that image build writes
    plans/<disc-uuid>/close     an empty file: pack --close made the disc
    state-seq.txt               the sequence mark ("The repository directory")
```

`ab` is the first two hex digits of the digest of `<id>`. `<disc-uuid>` is the
uuid in the hyphenated form, in lower case: `8-4-4-4-12` hex digits. Staging holds chunk
data and the sequence mark only. It holds no state and no metadata object: the loss of `staging/`
loses the chunk data of the items that no disc holds yet, and nothing else.
Keep staging on a local filesystem.

### 2.4 Catalog layout

The location is always `<repo>/catalog/`. There is no override. The catalog
is the permanent history of the repository. It holds no chunk data. No command
trims it.

| Item | Content |
|---|---|
| `snapshots/<id>` | A byte copy of each snapshot object. |
| `trees/ab/<id>`, `blobs/ab/<id>` | A byte copy of each tree and blob object. |
| `discs/<disc-uuid>/INDEX.bin`, `REFS.bin`, `DISCS.bin` | Byte copies of the tables that FORMAT.md's "The run index and the catalog" defines. The key is the disc uuid, never `run_seq`. The uuid is written in the hyphenated form, in lower case. |

`<repo>/state/catalog-state.txt` names, for each snapshot id, whether the
catalog holds every tree and blob object that the snapshot reaches. One line
holds the full text form of the snapshot id, one space, and `complete` or
`partial`. `commit` writes `complete`. `recover` computes the value again on
each call, for each snapshot that the disc names or that the file lists:
`partial` when the catalog does not hold every object that the snapshot
reaches, else `complete`. A `partial` snapshot becomes `complete` when a later
`recover` gives the missing objects. `ls`, `log` and `restore` of a `partial`
snapshot exit with code 1 and name `recover` ("Command notes").

Writers of the catalog:

- `commit` writes the snapshot, tree and blob objects directly.
- `pack` writes the tables of the disc that it packs.
- A counted `verify` and `recover` write the tables and the objects that they
  read from a disc, when the catalog does not hold them, or holds a copy with
  other bytes. They use the check of the disc that they already made: they
  read the object files and the tables of the disc again, not the chunks.
- `pack --undo` removes `discs/<disc-uuid>/` of its disc.

No other command writes the catalog. `disc lost` keeps the catalog data of the
disc. `status`, `ls`, `log` and `restore` read the catalog.

Each write of a catalog file goes to a temporary file, which is synced, then
renamed; the directory is synced after the rename. An object that the tool
writes must give its content id. A file of the same name with other bytes is
replaced, thus `recover` or a counted `verify` of a good disc repairs a
damaged catalog object.

Each read of a snapshot, tree or blob object of the catalog checks the object
against its content id. An object that does not verify is an error that names
the object and the disc whose INDEX lists it: run `recover` with that disc.
The completeness value of `catalog-state.txt` reads each tree. For a blob it
reads only the headers: a blob file that is empty, short, or whose headers do
not pass their CRC counts as missing.

## 3. Local file formats

No byte of these files reaches a disc. Every integer is little-endian, and
every CRC is CRC-32C. "State log replay" gives the replay, the torn-tail and
the durability rules of the two logs.

### 3.1 State log record

`<repo>/state/state.db` is append-only. It has no header. It is a whole number
of fixed-width records, each 70 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u64 | `sequence` | Monotonic record number. It starts at 1. |
| 8 | 32 | u8[32] | `content_id` | The item. |
| 40 | 1 | u8 | `state` | 1 Staged, 2 Packed, 3 OnDisc, 4 Lost. |
| 41 | 8 | u64 | `run_seq` | The run, when the state is Packed, OnDisc or Lost. 0 otherwise. It is a label; the disc uuid is the key. |
| 49 | 16 | u8[16] | `disc_uuid` | The disc, when the state is Packed, OnDisc or Lost. All zero otherwise. |
| 65 | 1 | u8 | `reason` | 0 normal, 1 pack undone, 2 disc lost, 3 lost undone. |
| 66 | 4 | u32 | `record_crc32c` | CRC-32C over bytes 0 to 65. |

The item words `burned` and `clean` are not stored. The tool derives them from
the state of the item's disc, as `docs/states.md`, "Item states", defines.

### 3.2 Local refs

`<repo>/state/refs.txt` is a text file. Each line holds a ref name, one space,
and a snapshot id in text form. `commit` replaces the line of the ref that it
moves. `recover` writes the names that the REFS table of its disc carries.
Each name follows the ref name rule ("Refs"); the tool never writes a line
with another name.
Each write of the file goes to a temporary file, which is synced, then
renamed; the directory is synced after the rename. A crash thus leaves the
old file or the new file.

### 3.3 Ledgers

`<repo>/state/discs.bin` is the disc ledger: one row for each disc that `pack`
wrote, that `recover` read, or that a disc that `recover` read names. It uses
the container of FORMAT.md's "DISCS". `pack --undo` removes the row of its
disc: the tool writes the new ledger to a temporary file, syncs it, and
renames it over the old one.

`<repo>/state/refslog.bin` is the ref ledger: every ref record that a disc
already carries. It uses the container of FORMAT.md's "REFS".

### 3.4 Disc state log record

`<repo>/state/discstate.db` is append-only. It has no header. It is a whole
number of fixed-width records, each 54 bytes:

| Offset | Size | Type | Name | Meaning |
|---:|---:|---|---|---|
| 0 | 8 | u64 | `sequence` | Monotonic record number. It starts at 1. |
| 8 | 8 | i64 | `time_sec` | Unix time of the event. |
| 16 | 16 | u8[16] | `disc_uuid` | The disc. |
| 32 | 1 | u8 | `event` | The event code (table below). |
| 33 | 1 | u8 | `flags` | Bit 0 `close`. Set in `Packed` only; 0 in every other event. Bits 1 to 7 must be 0. A record with a bit that its event does not permit is damage. |
| 34 | 8 | u64 | `disc_seq` | The disc number. Set in `Packed` only; 0 otherwise. |
| 42 | 8 | u64 | `run_seq` | The run number. Set in `Packed` only; 0 otherwise. |
| 50 | 4 | u32 | `record_crc32c` | CRC-32C over bytes 0 to 49. |

| Code | Event | Code | Event |
|---:|---|---:|---|
| 1 | `Packed` | 8 | `VerifyUndone` |
| 2 | `PackUndone` | 9 | `Freed` |
| 3 | `BurnRecorded` | 10 | `Lost` |
| 4 | `BurnRemoved` | 11 | `LostUndone` |
| 5 | `CheckOK` | 12 | `Recovered` |
| 6 | `CheckFailed` | 13 | `NamedMissing` |
| 7 | `MarkedVerified` | | |

`docs/states.md`, "Replay of the disc state log", gives the effect of each
event on the state of a disc, and names the command that writes it.

## 4. Staging state machine

### 4.1 States and transitions

`docs/states.md` defines the state machines. It gives the item states, the
disc states, the trust rule, the state x event table, and the `next:` block of
`status` for each state. This section gives only the rules that tie the state
machines to the files of the repository.

### 4.2 Transition rules

1. `commit` is the only entry point. It records every new item as Staged
   before it moves the ref.
2. `pack` records the items that it puts on a disc as Packed, and appends one
   `Packed` event for the disc. `pack --undo` records them as Staged with
   reason 1, and appends `PackUndone`.
3. Only these write a burn record: `disc burned`, and a good `verify` of a
   counted mount of a `packed` disc. The tool never infers a burn from
   anything else.
4. Only these write a verified record: a good `verify` of a counted mount, and
   `disc verified`.
5. A counted mount is a disc root that meets each rule below, after the tool
   resolves its symlinks. The tool reads the mount table of the process
   (`/proc/self/mountinfo`) and the device (`st_dev`) of each path. It needs
   no root. The tool checks the rules in this order, and the first rule that
   fails gives the `REASON` of the refusal:

   | Rule | `REASON` when the rule fails |
   |---|---|
   | The path is outside the repository. | `inside the repository` |
   | The path is outside the staging store. | `inside the staging store` |
   | The path is not the packed tree of a disc: `staging/plans/<disc-uuid>/tree`, the `pack --out` DIR that it links to, or the same directory at another path, such as a bind mount. | `the packed tree of a disc` |
   | The path is itself a mount point. | `not a mount point` |
   | The device of the path is the device of its mount table entry. A later mount on the path or on a parent hides the entry. | `hidden by another mount` |
   | The mount shows the root of its filesystem (the root field of the entry is `/`), not a directory of it. | `a bind mount of a directory` |
   | The filesystem type can hold a disc or a disc image. The tool refuses the union and overlay filesystems (`overlay`), the memory filesystems (`tmpfs`, `ramfs`), the network filesystems (such as `nfs`, `nfs4`, `cifs`, `9p`, `fuse.sshfs`) and the kernel pseudo filesystems (such as `proc`, `sysfs`). | `not a disc filesystem` |
   | The mount is read-only. | `not a read-only mount` |
   | The device of the path is not the device of the repository. When the repository directory does not exist yet, its nearest parent that exists gives the device. | `on the device of the repository` |
   | The device of the path is not the device of the staging store. | `on the device of the staging store` |

   The tool does not check the source of a mount. A loop mount of an image
   counts, also of an image file inside the repository, such as the image
   that `image build` writes: the mount shows the loop device. The format
   does not fix the filesystem type of a disc (FORMAT.md, "Filesystem
   requirements"), thus the tool refuses a list of types and does not
   accept a list of types. `verify` of any other disc root checks every
   byte and records nothing.
6. A change of a disc state writes one event to the disc state log. There are
   two exceptions. A good counted `verify` of a `packed` disc writes
   `BurnRecorded`, then `CheckOK`. `recover` of a damaged disc writes
   `Recovered`, then `CheckFailed`. A change writes no item record, except for
   `pack`, `pack --undo`, `gc`, `disc lost`, `disc lost --undo` and `recover`.
7. `gc` records a freed item as OnDisc. `recover` records each item that it
   reads from a disc and that the log does not know as OnDisc. It treats an
   item that is `Lost` in the log as not known, and records it as OnDisc. It
   leaves an item that the log knows in another state at its own state.

### 4.3 State log replay

These rules apply to `state.db` and to `discstate.db`.

The current state of an item is the newest record for its content id. The
current state of a disc is the replay of its events in log order. A reader
replays each log from the start. The build never compacts a log.

A partial record at the end of the file, or a last record with a bad CRC, is
what a crash during an append leaves. A command that holds the repository lock
cuts the file back to the last good record, prints one warning, and goes on. A
command that takes no lock ignores the torn tail, never changes the file, and
prints the same warning on stderr with the word `ignored` in place of `cut
off`: `noahsark: CMD: the disc state log's tail was truncated; N byte(s) after
the last valid record were ignored, matching a crash during an earlier append`.
It prints nothing on stdout. A command that takes no lock can also read a
part of the record that another command appends at this time. Thus, when it
finds a torn tail, it tries a shared lock on the lock file, with no wait, and
lets go of it at once. It never creates the lock file. When another command
holds the lock, the warning is `noahsark: CMD: the disc state log ends in a
part of a record; N byte(s) after the last valid record were ignored; another
noahsark command writes the log at this time`.

A bad record anywhere else is damage. So is a `sequence` that does not grow,
and a disc event that the replay does not permit. The tool reports an error,
names the record, changes no byte of the file, and stops.

An append is durable: the tool writes the record, syncs the file, and syncs
the directory when the append created the file. A command can append several
records as one batch. It writes all records of the batch, then syncs the file
one time, before the next step that depends on the records. An error from a
write, a sync or a close stops the command.

A command that changes the state of a disc and the records of its items
(`disc lost`, `disc lost --undo`, `pack --undo`, `gc`) writes the disc event
first, as one synced append. Then it writes the item records that the new state of
the disc asks for, as one synced batch. The event is the intent. A command
that stops between the two leaves a disc whose items do not follow its
state. The item records that each disc state asks for:

| Disc state | Item records |
|---|---|
| `lost` | Each Packed item of the disc returns to Staged. Each OnDisc item of the disc that is a snapshot, tree or blob object of the catalog returns to Staged: the catalog file of one of these kinds exists for its id and gives that id. Each other OnDisc item of the disc, a chunk, is Lost. All with reason 2. |
| `undone` | Each Packed item of the disc returns to Staged, with reason 1. |
| `on disc only` | Each Packed item of the disc is OnDisc, with reason 0. Each Lost item of the disc is OnDisc, with reason 3. While a Lost item of the disc remains, the same batch also records as OnDisc on the disc, with the run number of the catalog INDEX of the disc and reason 3, each item that is Staged with reason 2, that the INDEX lists, and that is a snapshot, tree or blob object of the catalog. |
| `burned`, when its newest event is `LostUndone` and no item is Packed on the disc | Each Staged item that the catalog INDEX of the disc lists is Packed on the disc, with the run number of that INDEX and reason 3. |

Each command that holds the repository lock completes these records when it
opens the logs, before it does its own work: one batch for each disc. It
prints one note on standard error for each disc: `noahsark: CMD: disc SEQ
"LABEL": an earlier COMMAND stopped before it wrote the records of its items;
N item record(s) now written`. `COMMAND` is the command that wrote the event.
Thus the operator needs to know no repair command: the next command with the
lock repairs. `status` takes no lock and writes nothing: it names the repair
in its `next:` block (`docs/states.md`, "State to `next:` block").

### 4.4 GC rules

1. `gc` frees the chunk files and the plan directory of a `verified` disc
   only. It also frees an orphan: a chunk file whose item is already OnDisc.
   It never removes a file of `catalog/` or `state/`.
2. One verified disc is enough. `gc` frees the data of a disc as soon as the
   disc is `verified`. There is no wait time. No option and no config key asks
   for more. `gc` asks no confirmation.
3. `gc` confirms, before it frees an item, that the catalog `INDEX.bin` of the
   disc of the item's own record lists the item. When the catalog does not
   hold that INDEX, `gc` leaves the item alone and reports it. When the INDEX
   does not list some items of a `verified` disc, `gc` frees no item of that
   disc, appends no `Freed` event for it, and reports the skip. `Freed` moves
   the whole disc, thus `gc` never frees a part of a disc.
4. `gc` writes the `Freed` events, then the OnDisc records, and syncs them
   before it unlinks a chunk file ("State log replay"). A crash after the
   records and before the unlink leaves an orphan.
5. `gc` removes `staging/plans/<disc-uuid>/` of a freed disc. For a `pack
   --out=DIR` disc, it removes the symlink `tree` and never touches `DIR`.
   It also removes a plan directory that remains for an `on disc only` disc.
6. `gc --dry-run` prints what `gc` would free, writes nothing and takes no
   lock ("Command notes" gives the lines).
7. `gc` reports the disk space that it frees: the allocated blocks of each
   file that it removes, not the apparent size. A sparse image counts only
   its allocated blocks. The dry run uses the same count.

## 5. Refs

A ref is a name for a snapshot. With no `--ref`, `commit` moves the ref
named by the local date of today, as `YYYY-MM-DD`. Two commits on one day
move that one name to the newer snapshot; `log` still lists the older
snapshot. No ref name is reserved.

A ref name is 1 to 40 bytes of printable ASCII, with no space: each byte is
from 0x21 to 0x7E. The limit of 40 bytes is the name field of FORMAT.md's
"Ref". `commit` refuses another `--ref` value before it writes a file, with
exit code 2: `noahsark: commit: ref name "NAME" is not valid: a ref name is 1
to 40 bytes of printable ASCII, with no space`. Thus every name that `pack`
writes into a REFS table follows the rule. A REFS table of a disc that another
writer made can hold another name. `recover` then writes that name into the
ref ledger, but not into `refs.txt`; `restore`, `ls` and `log` still resolve
it from the catalog REFS table.

1. `commit` moves one ref in `refs.txt`, as its last step.
2. `pack` writes into the REFS table of the disc every ref of the ref ledger
   and every ref of `refs.txt` whose snapshot the ledger does not carry yet.
   Thus the newest disc names every ref of the repository.
3. `restore`, `ls` and `log` resolve a ref name from `refs.txt` and from the
   catalog REFS tables. The newest record of a name wins, as FORMAT.md's
   "Ref" states.
4. `recover` writes every ref record that the given disc carries into the
   ref ledger. In `refs.txt`, it changes only the names that the disc
   carries, and it never moves a name back to an older snapshot ("Command
   notes" gives the rule).

A `SNAPSHOT` argument is a ref name, a full snapshot id, or a prefix of the
digest part of a snapshot id. "Commands and global options" gives the rules
of the match. The tool prints a snapshot id in one of two forms:

- The full text form. `commit` prints it in its `snapshot` and `ref` lines,
  because a script reads the id from these lines. A file of `state/`, the
  line `recover: damaged: ID` and the candidate list of an ambiguous prefix
  also use it.
- The short form: the first 12 hex characters of the digest, without the
  constant multihash prefix. `ls`, `log`, `restore`, `status` and the
  messages use it.

Each snapshot is a root snapshot: the build writes no parent id. `log` orders
snapshots by their time.

## 6. Concurrency and locking

One process at a time writes the local state of a repository.

1. `<repo>/lock` is the lock file. A command that writes `state/`,
   `catalog/`, the staging store or the config takes a non-blocking
   exclusive advisory lock (`flock`) on it, and holds it until it exits. The
   first such command creates the file. Those commands are `init`, `commit`,
   `pack`, `gc`, `disc burned`, `disc verified`, `disc lost`, `recover`,
   `verify --undo`, and a `verify` that records: a `verify` of a counted
   mount with a repository, with no `--no-mark`.
2. A read-only command takes no lock: `ls`, `log`, `status`, `restore`,
   `pack --dry-run`, `gc --dry-run`, and each `verify` that records nothing:
   `verify --no-mark`, a `verify` of a root that is not a
   counted mount, and `verify` with no repository.
3. `image build` takes no lock and never creates the lock file. It runs as
   root, and a lock file that root creates would block the operator. It writes
   only the image file ("Disc filesystems and image building").
4. A command that cannot get the lock fails at once with exit code 1. The
   message names the lock file. It never waits.
5. There is no separate catalog lock and no drive lock.

The lock is advisory. It is not a security boundary.

## 7. Commit

### 7.1 Commit flow

1. Resolve the source root: the `SOURCE` argument, else `sources.root`.
   Refuse a staging directory that does not exist while the state log holds
   a Staged or a Packed item ("The repository directory"). Else create a
   missing staging directory.
2. Walk the source. Leave out each excluded path.
3. For each regular file: chunk it, hash each chunk with SHA-256, and compress
   each chunk with zstd. Write a chunk into `staging/chunks/` only when the
   state log does not know its id, or when the item is Staged and its chunk
   file in `staging/chunks/` is missing or has a size other than the size of
   the chunk that `commit` made. Write the blob object that lists the chunks
   into `catalog/blobs/`.
4. For each directory, bottom-up: write the tree object into `catalog/trees/`.
5. Write the snapshot object, with the root tree, the time and the `-m`
   message, into `catalog/snapshots/`.
   Each object file of steps 3 to 5 goes to a temporary file, which is
   synced, then renamed. `commit` does this in groups of at most 64 files
   or 64 MiB: it syncs the temporary files of a group at the same time,
   then renames each one, while it writes the next group. Then `commit`
   syncs each directory that got a new name, one time.
6. Record every new item in the state log as Staged. An item is a chunk, a
   blob, a tree or a snapshot object. Only an item that the snapshot reaches
   gets a record.
7. Mark the snapshot `complete` in `catalog-state.txt`.
8. Move the ref in `refs.txt`.

A `Lost` item counts as new in step 6 only when this commit makes the same
object again. Then `commit` records it as Staged. A `Lost` item is a chunk
in almost all cases: `disc lost` stages each snapshot, tree and blob object
that the catalog holds. A chunk of a file that the source no longer holds
is not made again, thus it stays `Lost`.

`commit` refuses to run while a disc is `missing`, with exit code 1.

FORMAT.md's "Objects", "Chunking" and "Compression" give the shapes and the
constants. No config key changes them. No config key changes a disc byte:
`pack --capacity` does.

`commit` reads every file of the source on every run. An unchanged file gives
the same chunk ids, thus `commit` writes no new chunk for it. NoahsArk has no
scheduler and no daemon.

### 7.2 Source policy

| Rule | Behaviour |
|---|---|
| Source roots | One source root for each commit. It is opened read-only. It must be a directory. A source root that is a symlink is refused with exit code 1: the message names the directory that the link points to, and the operator gives that directory. |
| The repository and the staging store | Never walked, also when the source holds them. `commit` finds them by device and inode, not by path. It also leaves out the `pack --out` directory that a plan symlink in `staging/plans/` names. For each one it prints `excluded PATH: the repository`, `excluded PATH: the staging store` or `excluded PATH: the disc root of a pack --out`. The exit code does not change. |
| Symlinks | Never followed. The link itself is stored. |
| FIFO, socket, device node | Recorded by type, with no content. `commit` prints a `special PATH: KIND, no content is backed up` line for each one ("Command notes"). The exit code does not change. |
| Unreadable or vanished file | Skipped and reported. The snapshot is still written. Exit code 1. |
| Name that a tree entry cannot hold | A name that FORMAT.md's "Name validation" or the name limit of FORMAT.md's "Limits" refuses. On a POSIX source this is a name that holds `\`. Skipped like an unreadable file: `commit` prints `skipped PATH: REASON` and counts the path in `skipped: N`. A directory with such a name is not walked. The snapshot is still written. Exit code 1. |
| Mount points | Crossed by default. With `--one-file-system`, a directory on another device stays in the tree as an empty directory, and `commit` prints one line for it. |
| Owner | The build records mode, mtime, uid and gid, and the user name and the group name TLVs when the host names the ids. |

FORMAT.md's "The root tree" gives the root name encoding. `ls` and `restore`
use paths relative to the source root. When the root tree of a snapshot holds
more than one source root, they keep the root level.

### 7.3 In-flight change detection

`commit` stats a file, reads and chunks it, and stats it again. When the size
or the mtime differs, it reads the file again, one time. When the file still
differs, `commit` stores the content that it read last, sets the `UNSTABLE`
flag of FORMAT.md's "Entry flags" on the entry, and prints `unstable PATH: the
file changed during the read`. The exit code is then 1. A read that `commit` does not
keep, and the read of a file that fails part way, give no item. `commit`
removes each chunk file that such a read wrote, unless the snapshot reaches
the chunk or the state log has a record for it. This detection always runs. A
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
   disc. Data that does not fit stays Staged for the next `pack`.
2. `pack` always packs from the full Staged pool. It has no option that
   selects items.
3. `pack` walks the tree of every staged snapshot in post-order: the chunks
   of a file, then its blob, then the tree of its directory. It takes the
   longest prefix of that order that passes "The budget formula". Thus the
   chunks of a file and the files of a directory stay together. It reads
   chunks from `staging/chunks/`, and blobs, trees and snapshots from
   `catalog/`. The snapshot object comes after each object that it reaches.
   `pack` walks the snapshots in the order of their snapshot time, the
   oldest first. Snapshots with equal times go in the order of the bytes of
   their ids. An item that two snapshots reach goes with the first of them
   in this order. Thus the rest of an older snapshot always goes before a
   newer snapshot. `pack` has no minimum fill: the rest of a snapshot stays
   Staged until the operator packs it. `status` names each snapshot that is
   not complete on discs ("Command notes").
4. `pack` takes each Staged item, whatever the state of the items above it.
   The walk stops at a tree or a blob that a disc holds. When that walk
   misses a Staged item of the state log, `pack` walks again, and goes down
   into the trees and blobs that a disc holds, also below a snapshot object
   that a disc holds, until it finds each such item. `disc lost` of a
   `packed` or `burned` disc, and a `commit` after `disc lost` of a freed
   disc, give such items. An item goes with the first snapshot in the
   order above that reaches it. A Staged item that no snapshot reaches goes
   last, in the order of the bytes of its id. The disc that takes such
   items holds them as ordinary objects: a reader finds an object on any
   disc whose INDEX lists it (FORMAT.md, "INDEX"), and the trees and blobs
   above it stay on their own discs.
5. A Staged item that `pack` cannot take stays Staged: a tree, a blob or a
   snapshot object whose file in `catalog/` is missing or does not verify,
   and a chunk whose file is missing or fails its check during the copy.
   The trees above it and its snapshot object stay Staged too. `pack` takes
   each other item. It prints one warning on standard error for each
   snapshot that reaches such an item, and one for each such item that no
   snapshot reaches. When a chunk fails its check during the copy, `pack`
   removes the part-written disc root and writes it again without the
   chunk. "Failure and recovery actions" gives the repair.
6. A prefix is dependency-closed: a child that the prefix does not hold is
   already on an earlier disc. `pack` records that disc in the Prereqs table
   of INDEX.
7. Every run carries INDEX, REFS, DISCS and the snapshot objects that
   FORMAT.md's "Catalog contents per run" names. `pack` reads the snapshot
   objects from the catalog. Each disc carries every snapshot object of the
   catalog. The snapshots stay in the catalog after `gc`, thus a disc that is
   packed after a `gc` carries the old snapshots too. When the catalog file
   of such a snapshot object is missing or does not verify, `pack` stops
   with exit code 1, and names `recover` with a disc that holds it.
8. `pack` refuses a capacity that holds not one item, with exit code 2.
9. `pack` refuses to run while a disc is `missing`, with exit code 1.
10. The next `disc_seq` is one more than the highest `disc_seq` of all `Packed`
   events of the disc state log and of all rows of the disc ledger. The next
   `run_seq` follows the same rule. A fresh repository starts at `run_seq` 1
   and `disc_seq` 0. An undone disc number is never used again.
11. The label of a disc is the name of the newest ref, then ` disc SEQ`, for
   example `2026-09-21 disc 0`. With no ref, it is `disc SEQ`.

### 8.2 Integrity checks and durable recording

`pack` checks the content id of every item that it selects. It checks a tree,
a blob and a snapshot object when it walks them, and a chunk while it copies
the chunk into the disc root. "Packing rules" gives what `pack` does with an
item that fails. Another failure while `pack` writes the disc root, such as a
read error or a sync error, removes the part-written disc root and records
nothing.

`pack` writes the disc root to `staging/plans/<disc-uuid>/tree`. With
`--out=DIR`, it writes the disc root into `DIR` and makes
`staging/plans/<disc-uuid>/tree` a symlink to the absolute path of `DIR`.
`pack` makes `DIR` absolute. Before it writes, it refuses with exit code 2 a
`DIR` inside the repository or inside the staging store (`--out=DIR is inside
the repository or the staging store; give a directory outside them`), a `DIR`
that holds files (`--out=DIR holds files; give an empty or absent
directory`) and a path that is not a directory (`--out=DIR is not a
directory`). The check resolves the symlinks of the part of each path that
exists. With `--close`, `pack` writes the empty file
`staging/plans/<disc-uuid>/close` and syncs it before it writes the disc
root.

`pack` syncs every file and every directory of the disc root after the write.
It writes the catalog tables of the disc, the disc ledger row, the Packed item
records and the `Packed` event only after that sync, in this order. The
`Packed` event carries the `close` flag for `--close`.

A `pack` without `--dry-run` completes what an interrupted `pack` left, after
it completes the undone packs ("Undo a pack") and before it writes a disc
root:

- A ledger row with no `Packed` event, whose `staging/plans/<disc-uuid>/tree`
  exists, belongs to a `pack` that stopped after its ledger row. `pack`
  records each item that the catalog INDEX of the disc lists and that is
  Staged or has no record as Packed on that disc. Then it appends the
  `Packed` event, with the time of the ledger row, and the `close` flag when
  the file `close` of the plan directory exists. It prints `noahsark: pack: disc SEQ "LABEL": an
  earlier pack stopped before it recorded the disc; its records are now
  complete` on standard error.
- A directory `catalog/discs/<disc-uuid>/` or `staging/plans/<disc-uuid>/`
  whose uuid neither the disc state log nor the disc ledger names belongs to
  a `pack` or a `recover` that stopped before its ledger row. `pack` removes
  it and prints `noahsark: pack: removed PATH: no record names disc UUID; an
  earlier pack or recover stopped before it recorded the disc` on standard
  error. For a `pack --out=DIR` disc, it removes the symlink and never
  touches `DIR`.

### 8.3 Dry run

`pack --dry-run` answers "how many discs does the staged data need?". It needs
`--capacity`. It writes nothing, uses
no sequence number and takes no lock. It prints one `disc N: I items, B bytes`
line for each disc, then `total: D discs, I items, B bytes`. The plural form
is fixed, also for 1, because a program parses it. It prints no `next:` line.
The numbers are an estimate.

### 8.4 Undo a pack

`pack --undo DISC` returns the items of the newest disc to Staged, while that
disc is `packed`. The newest disc is the disc with the highest `disc_seq` among
the rows of the disc ledger and the `Packed` events whose disc is not undone. It asks an ordinary confirmation. After a yes, it does these
steps in this order:

1. Append the `PackUndone` event, then the Staged records with reason 1
   ("State log replay").
2. Remove the disc row from the disc ledger.
3. Remove `catalog/discs/<disc-uuid>/`.
4. Remove `staging/plans/<disc-uuid>/`. For a `pack --out=DIR` disc, it
   removes the symlink and keeps `DIR`, and prints `disc root DIR kept; delete
   it yourself`.

When a step after the `PackUndone` event fails, `pack --undo` prints
`noahsark: pack: disc SEQ is undone, but its files stay: ERROR; the next pack
removes them` to standard error. The disc is undone all the same, thus it
prints the `next:` line as the last line of standard output, and exits with
code 1.

A `pack --undo` that stops after the `PackUndone` event and before the item
records leaves item records that each command with the lock writes ("State
log replay"). A `pack` without `--dry-run` completes each `pack --undo` that
stopped after the item records. It does this after the check for a `missing`
disc and before it writes a disc root. For each undone disc, it removes the
ledger row, the catalog tables and the plan directory that remain, in the
order of the steps above. It prints one note on standard error: `noahsark:
pack: disc SEQ: an earlier pack --undo stopped before it removed the records
of the disc; they are now removed`. For a `pack --out=DIR` disc, it also
prints `noahsark: pack: disc root DIR kept; delete it yourself`. Thus the
DISCS table of a new disc never names an undone disc.

`pack --undo` leaves `state/refslog.bin` as it is. The ref ledger is an
append-only history, and the next `pack` writes each ref of it again.

It refuses a disc that is not the newest, and a disc that is not `packed`.
`docs/states.md`, rows 11 to 14, gives the messages.

## 9. Capacity budget

### 9.1 Capacity

The operator gives the capacity with `pack --capacity` at each `pack`. No
config key holds a capacity. `pack` reads no drive. With no `--capacity`,
`pack` refuses with exit code 2. The value is a preset name, or a size with a
decimal unit (`k`, `M`, `G`, `T`, `kB`, `MB`, `GB`, `TB`) or a binary unit
(`Ki`, `Mi`, `Gi`, `Ti`, `KiB`, `MiB`, `GiB`, `TiB`). `G` is not `Gi`. A bare
number is a usage error.

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
packer and the image length both use this one value. The preset name is
not recorded: `DISC.bin` holds no media type.

### 9.2 The budget formula

A run fits when this holds, in bytes:

```
file_bytes + run_header_copies + filesystem_overhead  <=  capacity_sectors * 2048
```

`file_bytes` is the sum of the files of the run other than the two run header
copies, each rounded up to a multiple of 2048. `run_header_copies` is the two
run header copies, 1024 bytes. `filesystem_overhead` is an estimate of
the filesystem metadata: the space bitmap, 4 MiB, two blocks for each file,
two blocks for each directory, and 0.1 percent of the capacity. It changes no
disc byte. The estimate covers the UDF image and the ISO 9660 folder burn.

## 10. Disc filesystems and image building

FORMAT.md's "Filesystem and the volume tree" gives the minimum requirements of
the disc filesystem. The build supports two methods to put a disc root on a
disc ("Burning"). The recommended method is a UDF image that `image build`
makes. `image build DISC [--force]` does these steps.

1. Resolve `DISC` through the repository. It reads `config.yaml`,
   `state/discs.bin` and `state/discstate.db`, and writes no file of them.
   It reads no other file of `state/` and no file of `catalog/`. It refuses
   a disc that is `on disc only`, `lost` or `missing` with `no disc root at
   DIR`, and a disc with no record in the disc state log, with exit code 1.
   The state check comes first.
2. Check the `mkudffs` version ("Tool version check"). This check comes after
   the state check and before the check for root.
3. Refuse to go on when it is not root, with exit code 1. It prints the exact
   `sudo noahsark --repo=REPO image build SEQ` line to run. `REPO` is the
   absolute path of the repository. It is in single quotes when it holds a
   character other than an ASCII letter, a digit, or one of
   `_ . / : = @ % + , -`. `SEQ` is the disc number, or the full
   uuid when another disc that is not undone has the same number. With
   `--force`, the line is `sudo noahsark --repo=REPO image build --force
   SEQ`.
4. Refuse to go on when the disc root does not exist, with exit code 1.
5. Refuse to go on when `staging/plans/<disc-uuid>/tree.img` exists, with exit
   code 1. With `--force`, it removes the old image first. It removes a
   symlink there, never the file that the symlink names. A build that then
   fails leaves no image.
6. Read the capacity from the `DISC.bin` of the disc root. Create the image
   file `staging/plans/<disc-uuid>/tree.img` with mode 0600, and make it a
   sparse file of that length.
7. Run `mkudffs --utf8 --media-type=hd --blocksize=2048 --udfrev=2.01
   --label=LABEL --uid=0 --gid=0 --mode=0555 --bootarea=erase /dev/fd/3`.
   `LABEL` is the volume label of the disc (below). The file descriptor 3 is
   the image file.
8. Attach the image file to a free loop device, with the autoclear flag.
   Mount the loop device with `nosuid`, `nodev` and `noexec` on a new mount
   point `noahsark-udf-mount-XXXXXXXX` in the system temporary directory.
   Copy the `NOAHSARK` tree into it. Unmount it, detach the loop device, and
   remove the mount point.
9. Give the image file the owner and the group of the plan directory, and
   mode 0644.

The volume label is for the operator only: it names the disc when the
operating system shows the mounted disc. The tool never reads it. Both burn
methods write the same label: `NOAHSARK_`, then the disc number in decimal,
zero-padded to 4 digits, with more digits when the number is larger. For
example, disc 7 gets `NOAHSARK_0007`, and disc 12345 gets `NOAHSARK_12345`.
The label uses only upper-case letters, digits and `_`, the characters that
an ISO 9660 volume identifier permits. The largest `disc_seq`, 2^64 - 1, has
20 digits, thus a label has at most 29 characters. That is within the UDF
volume identifier limit of 30 characters and the ISO 9660 limit of 32
characters. `mkudffs --label` writes the label into the UDF logical volume
identifier and the volume identifier.

Never use `--media-type=bdr` or `dvdr`. Both make a write-once VAT volume,
which cannot be populated. `image build` removes the partial image when a step
fails. It writes no file in `state/` or `catalog/`, and never creates the lock
file. It prints `built image FILE (N bytes)`.

### 10.1 Image build as root

`image build` runs as root on a repository of a user with no privilege. That
user, or a program of that user, can rename, replace and create files and
symlinks in the repository while the build runs. `image build` gives that
user no way to read or change a file that the user cannot read or change.
These rules do that:

- It opens each directory once and holds its file descriptor. Each later
  step goes through the descriptor, never through the path again.
- It reads `config.yaml`, `state/discs.bin`, `state/discstate.db` and
  `DISC.bin` only when each is a regular file and not a symlink, and belongs
  to the owner of the directory that holds it. A refusal names the path and
  prints no content of the file. The disc state log goes to a new directory
  of mode 0700 in the system temporary directory, for the replay, and
  `image build` removes that directory before it goes on.
- The plan directory `staging/plans/<disc-uuid>/` must not be a symlink. It
  must belong to the owner of the repository directory.
- The disc root `tree` can be a symlink, as `pack --out` makes it.
  `image build` follows it one time and holds the directory that it names.
- The disc root holds only directories and regular files. `image build`
  opens each entry with no follow of a symlink, and refuses a symlink, a
  FIFO, a socket, a device, and each entry that does not belong to the owner
  of the plan directory. A refusal stops the build with exit code 1. A hard
  link to a file of another user thus never goes into the image.
- It creates the image file only where no file and no symlink is. `mkudffs`,
  the loop device, the owner and the mode get the file descriptor of the
  image, never its path. A rename or a symlink at `tree.img` during the build
  changes no other file.
- The mount point is a new directory of mode 0700 in the system temporary
  directory. As root, each element of the path of that directory must be a
  directory of root, and a directory that others can write must have the
  sticky bit. Else `image build` refuses with `the temporary directory DIR is
  not safe for the mount point, ...; set TMPDIR to a directory that only root
  can change`. `sudo` removes `TMPDIR` by default, thus the directory is
  `/tmp`.
- It looks for `mkudffs` in `/usr/sbin`, `/usr/bin`, `/sbin`, `/bin`,
  `/usr/local/sbin` and `/usr/local/bin`, in this order, never in `PATH`.
  `mkudffs` gets only `PATH` of these directories and `LC_ALL=C`.

A failure after the mount unmounts the image, detaches the loop device,
removes the mount point and removes the image file. SIGINT, SIGTERM and
SIGHUP stop the copy and do the same. When the unmount fails, `image build`
removes the name of the image file and exits with code 1. Its message names
the mount point and the loop device, and ends with `to clean up, run: sudo
umount MNT && sudo rmdir MNT`. The kernel detaches the loop device after the
unmount.

A kill with SIGKILL, or a crash, can leave these:

| What stays | How to find it | How to remove it |
|---|---|---|
| The partial image `tree.img`, of root, mode 0600 | `ls -l staging/plans/*/tree.img` | `image build --force` removes it. The owner of the plan directory can also remove it with `rm`. |
| A mount of the image | `findmnt -t udf` shows `/tmp/noahsark-udf-mount-XXXXXXXX` | `sudo umount MNT`. The kernel then detaches the loop device. |
| The mount point | `ls -d /tmp/noahsark-udf-mount-*` | `sudo rmdir MNT` after the unmount. |
| A loop device with no mount | `losetup -l` shows the image file | `sudo losetup -d DEV`. |
| A copy of the disc state log | `ls -d /tmp/noahsark-state-*` | `sudo rm -r DIR`. |

## 11. Burning

### 11.1 Burning is externalized

The program does not burn. It has no `burn` command. `status` prints the
lines to paste (`docs/states.md`, "State to `next:` block").

| Step | Owner | Command |
|---|---|---|
| Write the disc root | The program | `noahsark pack` |
| Build the UDF image | The program, as root | `sudo noahsark image build DISC` |
| Burn the image | The operator | the `growisofs` line that `status` prints |
| Eject, load and mount the disc read-only | The operator | `eject`, `mount -o ro` |
| Check every item, record the burn and the verified disc | The program | `noahsark verify MOUNT` |
| Record a burn without a check | The operator | `noahsark disc burned DISC` |

`status` prints each line with the device of `pack.device` and speed 4. Use
speed 2 for M-DISC.

**Recommended: burn the image, left open.**

```bash
growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=tree.img
```

`spare:min` formats a blank BD-R with the smallest spare area. `-dvd-compat`
is not passed. The disc stays open.

**Recommended, sealed: `pack --close`.**

```bash
growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:none,tty \
          -Z /dev/sr0=tree.img
```

`spare:none` skips the format step, thus there is no defect management.
`-dvd-compat` closes the disc. The choice is permanent.

**Second method: burn the folder directly.**

```bash
growisofs -Z /dev/sr0 -R -iso-level 4 -V NOAHSARK_0007 TREE
```

`growisofs` runs `mkisofs` and makes an ISO 9660 volume with Rock Ridge from
the disc root `TREE`. `-V` takes the volume label of the disc ("Disc
filesystems and image building"); the example is for disc 7. For a `pack --out` disc, `TREE` is the `pack --out`
directory. Add `-dvd-compat` for a `pack --close` disc. Never pass `-J`: Joliet
cuts long names. Never pass `-udf`: it adds a UDF 1.02 bridge. Never pass
`-M`. The reading of an ISO 9660 level 4 disc on Windows and on macOS is not
verified. `status` never prints this method in a `next:` block. It prints one
line after the block that points to the guide section "Burn the folder
directly".

Never use `-overburn`. Never pass `-M` on any disc. Eject and load the disc
again before the verify, so that the read comes from the medium.

### 11.2 Tool version check

| Tool | Package | Version requirement |
|---|---|---|
| `growisofs` | dvd+rw-tools | Debian 7.1-14 or newer, Fedora 7.1-13 or newer, Arch 7.1-13 |
| `dvd+rw-mediainfo` | dvd+rw-tools | as `growisofs` |
| `mkudffs` | udftools | 2.3 or newer |

`image build` reads the `mkudffs` version and refuses an older one. It runs
`mkudffs` with no argument and reads the version from the `udftools X.Y`
text that `mkudffs` prints. It looks for `mkudffs` only in the system
directories that "Image build as root" names, never in `PATH`. Each refusal
exits with code 1:

| Case | Message |
|---|---|
| `mkudffs` does not run | `noahsark: image build: mkudffs: ERROR` |
| The output holds no version | `noahsark: image build: could not parse mkudffs version from: OUTPUT` |
| The version is older than 2.3 | `noahsark: image build: mkudffs is from udftools X.Y; image build needs udftools 2.3 or newer` |

The tool never runs `growisofs` or `dvd+rw-mediainfo`, thus the operator
checks that version before the first burn. "Burning-host command reference" gives the
commands.

## 12. Disc lifecycle and closing

A disc is blank, then open after the default burn, or sealed after the burn
with `-dvd-compat`. `pack --close` changes only the burn line that `status`
prints. The `close` flag of the `Packed` event holds the choice. A disc is
never closed by default, and there is no config key for it. Every reader path
works on an open disc. The repository does not check whether a burn sealed the
disc.

A disc is good or is discarded. The operator discards a disc that fails
`verify` and burns the same disc root on a new disc.

`run_seq` and `disc_seq` are labels for the human. The host assigns them from
local state ("Packing rules"). After a lost repository, two discs can carry
the same number. The tool finds a disc by its uuid. One disc holds one run,
thus the disc uuid identifies the run too. The operator writes the number, the
first 8 characters of the uuid and the storage place on the sleeve of each
disc. The tool keeps no shelf notes.

## 13. Verify

`verify [--no-mark] DISC-ROOT` reads a disc root: the mount
point of a disc, the mount point of a loop-mounted image, or a directory that
holds `NOAHSARK/`. It reads `DISC.bin`, the run header, `INDEX.bin` and every
object through the filesystem. It checks every file that INDEX lists against
its recorded hash, every table and header against its CRC, and every object
against its content id. This is how the tool finds damage. The tool does not
repair a disc: the repair is the second copy of the disc.

`verify` checks in this order, and stops at the first refusal:

1. The options. `--undo` with another option is a usage error, with exit
   code 2.
2. The repository and its config, as for every command.
3. The `DISC.bin` of `DISC-ROOT`. When `verify` cannot read it, it prints
   `noahsark: verify: DISC-ROOT: cannot read the disc: ERROR` and exits
   with code 1.
4. With a repository: whether `DISC-ROOT` is a
   counted mount. This check refuses nothing. It fails only when `verify`
   cannot resolve the path, read the mount table, or read the device of a
   path, with exit code 1.
5. With a repository: the state of the disc. `verify` refuses a disc whose
   uuid the disc state log does not hold, an undone disc, a `lost` disc and a
   `missing` disc, with exit code 1.

Then `verify` reads every object. `verify` reads the run header from
`RUN.bin`, or from `RUN2.bin` when `RUN.bin` cannot be read or fails its
checks (FORMAT.md's "Run header copies"). Damage to one copy fails the
check, also when the other copy is good: the disc is damaged. `recover`
goes on with the good copy. A disc tree that holds a symlink or
another entry that is not a regular file or a directory fails the check:
the tool never writes such an entry on a disc, and never follows one.

After the read, with a repository, `verify` compares
the disc uuid of the full read with the disc uuid of step 3. After a failed
read, it reads `DISC.bin` again for the uuid. When the two uuids differ, it
prints `noahsark: verify: DISC-ROOT: the disc changed during the check: disc
UUID before, disc UUID after; nothing is recorded`, records nothing, and
exits with code 1.

When `DISC-ROOT` is a counted mount ("Transition rules") and `--no-mark` is
not given:

- A good check writes the catalog tables and objects of the disc that the
  catalog does not hold, or holds with other bytes, then appends `BurnRecorded` when the disc is
  `packed`, then `CheckOK`. The catalog write uses the result of the check:
  it reads again only the snapshot, tree and blob files and the tables, not
  the chunks. When the catalog write fails, the
  disc is not at fault: `verify` appends no event, prints no ok line and no
  bad line, prints `noahsark: verify: the disc passed its check, but the
  catalog write failed: ERROR; the check is not recorded; correct the cause
  and run verify again` to standard error, and exits with code 1.
- A failed check appends `CheckFailed`. The event removes one record, as
  `docs/states.md`, "Disc records", states.

When `DISC-ROOT` is not a counted mount, `verify` checks every byte, records
nothing, and prints `not counted: this is not a disc`. It prints `noahsark:
verify: DISC-ROOT is not counted: REASON` to standard error, with the `REASON`
of "Transition rules". With no repository, it
prints `not counted: no repository`. A failed check of such a root prints
`disc SEQ "LABEL": bad; REASON` first, then the same `not counted` line, and
exits with code 1. It removes no record and logs nothing. `--no-mark` writes
nothing: no record, no verify log event and no catalog entry.

The first line of a check is `disc SEQ "LABEL": N items, ok` or `disc SEQ
"LABEL": bad; REASON`. With no repository, `verify` names the disc by uuid.
`N` is the number of objects that the check verified on the disc. It is the
same number with a repository and with no repository. It counts the snapshot
objects that the disc carries from earlier discs, thus it can be larger than
the item count that `pack` printed for the disc.

A failed check that records nothing has one of two fixed `REASON` texts:
`the packed tree is damaged` when `DISC-ROOT` is the disc root that `pack`
wrote for the disc (`staging/plans/<disc-uuid>/tree`, or the target of its
symlink), else `the disc root is damaged`. A failed counted check uses the
`REASON` of `docs/states.md`, rows 40 to 43. `verify` prints the detail of
the failure to standard error, as `noahsark: verify: DETAIL`.

A run whose `fec_scheme` is not 0 is read in full: the reader cannot use the
scheme, and it reads every object through the filesystem (FORMAT.md, "Run
header"). `verify` then prints this line to standard error, with `FILE` the
run header copy that it read, `RUN.bin` or `RUN2.bin`, and `N` the value:

```
noahsark: verify: FILE: fec_scheme N: this reader cannot use the scheme; it read every object without it
```

`recover` prints the same line with `recover` in place of `verify`. The line
changes no state and no exit code.

One line follows the ok line or the bad line, except after a failed counted
check:

- a good counted check: the text of `docs/states.md`, rows 31 to 35;
- a root that is not a counted mount, also with `--no-mark`: `not counted:
  this is not a disc`;
- no repository: `not counted: no repository`;
- `--no-mark` of a counted mount: `not marked`. After a good check of a
  `packed` disc, the line is `not marked; to record this burn, run: noahsark
  disc burned SEQ`.

`verify --undo DISC` removes the verified record of a `verified` disc. It asks
an ordinary confirmation and reads no disc.

When `verify` finds damage, the operator uses these sources, in this order:

1. **A second copy.** Read from the operator's second copy. Burn a new copy
   from the kept image, or from an image that `ddrescue` reads from a good
   copy.
2. **Another disc that holds the same content id.** The `restore` plan finds
   it through the catalog INDEX tables.
3. **The original source path**, if it still exists. `commit` stores it again.

## 14. Restore

`restore [--overwrite] [--dry-run] --disc=DIR SNAPSHOT [PATH...] DEST` reads
one disc at a time from the mount point `DIR`. It needs a repository. It plans
from the catalog and reads chunk data from discs only. It never reads
`staging/` and never builds a repository. After the loss of the repository,
the operator runs `recover` for each disc first, then `restore`.

### 14.1 Paths and the destination

With no `PATH`, `restore` writes the content of the source root into `DEST`:
`DEST/notes.txt`, not `DEST/srv/data/notes.txt`. A `PATH` is relative to the
source root, as `ls` prints it. A `PATH` follows the `rsync` rule for a
trailing slash:

- `photos` makes the directory `DEST/photos`.
- `photos/` puts the content of `photos` directly into `DEST`.

A `PATH` that the snapshot does not hold is a usage error, with exit code 2.
A directory between `DEST` and a restored entry that no tree entry describes
is created with mode 0755.

More rules for `PATH`:

1. A `PATH` puts the entry that it names directly into `DEST`, with the last
   name of the `PATH`: `photos/2024/a.jpg` makes `DEST/a.jpg`.
2. `restore` ignores an empty name, so a leading or a doubled slash changes
   nothing. A `PATH` with no name, and a `PATH` with a trailing slash that
   names a file, are usage errors, with exit code 2.
3. Each `PATH` is resolved alone. `restore` writes the entries in the order
   of the `PATH` arguments.

When the root tree holds more than one source root ("Source policy"):

1. With no `PATH`, each source root keeps its path below `DEST`:
   `DEST/srv/data/notes.txt`. A source root `/` puts its content directly
   into `DEST`.
2. A `PATH` starts with the path of its source root: `srv/data/photos`.

`DEST` is relative to the working directory. Before the first disc,
`restore` creates `DEST` and each missing parent with mode 0755. With
`--dry-run`, `restore` creates nothing.

### 14.2 Disc swap, one drive

**The plan.** `restore` reads the snapshot, the trees and the blobs from the
catalog. It finds the disc of each chunk through the catalog INDEX tables and
the disc ledger. When several discs hold a chunk, the plan takes a disc that
is not `lost`, the lowest `disc_seq` first. The plan gives each chunk to that
one disc, and `restore` reads the chunk from that disc only. A chunk that no
catalog INDEX lists has no known disc. This happens when the repository
misses the tables of a disc, for example a disc that was `missing` and is
then marked `lost`. `restore` skips each file that `DEST` already holds with
the content and the type of the snapshot, and each chunk that a part file
already holds. The plan counts only what is left.

To find a file that `DEST` already holds, `restore` compares the size, then
reads the file and checks each chunk against its content id. The size and the
mtime alone do not decide. Thus a restore that runs again reads each regular
file that `DEST` already holds two times: one time for the plan and one time
for the walk of the first disc.

An item of the plan is one chunk that `restore` still needs. The plan counts
an item one time, on one disc, also when many files hold it. Trees and blobs
come from the catalog and are not items. `B` is the sum of the byte lengths
of the object files of the items on the disc, as the Files table of the
catalog INDEX gives them. It is not the size of the restored files.

The plan keeps counts only: for each disc, the number of items and the number
of bytes. It keeps no list of chunks in memory, so that the memory of
`restore` does not grow with the snapshot ("Scope and conventions"). It holds
one catalog INDEX in memory at a time:

1. The walk writes the content id of each needed chunk to a temporary file,
   and sorts the ids on disk.
2. For each disc, in the order of the plan, `restore` reads the catalog INDEX
   of the disc, counts the ids that it lists, and keeps the other ids for the
   next disc.
3. The ids that no disc lists are the items with no known disc.

The cost is one walk of the selection, a sort of the ids on disk, and one
read of the id file for each disc. The temporary files are in the system
temporary directory (`TMPDIR`). `restore` unlinks each one when it creates
it. `restore` changes no file of the repository.

While `restore` reads a disc, it decides for each chunk of the walk whether
the plan gives the chunk to this disc. It reads the ids that the plan gives
to the disc one time for each disc. It writes such a chunk at each position
of each file that still needs it. Thus `restore` writes each position of a
file one time, also when several discs hold the chunk.

`restore` prints the plan first, in this form:

```
disc SEQ "LABEL" (UUID): N items, B bytes
restore: N item(s) have no disc known to the catalog; run recover with more discs
totals: D discs, N items, B bytes
```

There is one `disc` line for each disc that the plan needs. A `lost` disc that
the plan still needs has ` (lost)` at the end of its line. The `restore:` line
is present only when a needed chunk has no known disc. The `totals:` line is
always the last line of the plan: `D` is the number of `disc` lines, and `N`
and `B` are the sums of their counts. Its form is fixed: it uses `discs` and
`items` also for the value 1, because a program parses it. `--dry-run` prints the plan and stops.

`restore` does not ask for a `lost` disc, and it cannot ask for a chunk that
has no known disc. It handles the two cases in the same way: it does not stop
before it reads a disc, it restores every file that it can, it reports each
file that needs such a chunk as `file not restored`, and it exits with code 1.
The line of such a file is:

```
noahsark: restore: warning: PATH: file not restored: a chunk of this file is on a lost disc or on no disc known to the catalog; the part file stays
```

The loop takes the discs in `disc_seq` order. Before each disc, `restore`
reads `DISC.bin` below `DIR`. When the disc at `DIR` is a disc that the plan
still needs, `restore` reads it next. When the plan needs no disc that is not
`lost`, `restore` still walks the snapshot one time, and writes what needs no
disc.

`restore` checks a disc ("Disc detection") only when the walk first needs a
chunk of that disc. Thus `restore` does not ask for a disc that it does not
need: for example a disc whose chunks belong only to files that are already
complete or that already failed.

**Disc detection.** For each disc of the plan, `restore` reads
`NOAHSARK/DISC.bin` below `DIR` and compares the `disc_uuid`.

1. The expected disc: `restore` prints `disc SEQ "LABEL": found` and goes on.
2. An unreadable `DISC.bin`: `restore` tries 3 more times with a short pause,
   then prompts.
3. A wrong disc: `restore` prints `expected disc SEQ "LABEL" (UUID), found
   disc SEQ "LABEL" (UUID)`. When the repository does not know the found
   disc, the second part is `found disc UUID`. Then it prompts on standard
   error: `insert disc SEQ "LABEL" (UUID) into DIR and press Enter`. It reads
   one line from standard input.
4. With no terminal on standard input, `restore` does not prompt. It prints
   `restore: insert disc SEQ "LABEL" (UUID) into DIR and run restore again`
   and exits with code 1. The part files stay, and the next run resumes. A
   script mounts the named disc and runs the same `restore` again.
5. An end of input at the prompt stops `restore` in the same way.
6. `restore` never unmounts and never ejects. The operator swaps the disc in
   a second terminal, mounts it at `DIR`, and presses Enter.

For each disc, `restore` walks the tree of the snapshot one time and writes
each chunk of this disc into the part file of its own file. A file whose
chunks lie on two discs is a normal case. `restore` copies each byte one time.
Nothing that it holds in memory grows with the size of the snapshot.

**The file state.** `restore` keeps the state of each regular file in a
temporary file, not in memory. The file has one record of fixed size for each
regular file, at the number of the file in walk order. Each walk of the
snapshot meets the files in the same order, thus the number finds the record.
A record holds the number of chunk positions that the file still needs, a
flag for a part file of an earlier run, and a hash of the path. A chunk that
the file holds at two positions counts two times. A file that is complete,
skipped or failed has no pending record, and a later walk reads neither its
blob nor its bytes.

1. The file is in the system temporary directory (`TMPDIR`). `restore`
   unlinks it when it creates it. Thus nothing of it stays after a normal end
   or after a crash.
2. After a crash, or a stop for a disc, only the part files below `DEST`
   stay. The next run makes a new state file and checks each part file
   against the content ids ("The part file and resume").
3. A later walk must meet the files of the first walk in the same order. When
   a directory below `DEST` changes between two walks, the order changes.
   Only the first walk creates directories. When a later walk finds a
   directory missing, the part files below it are gone too. In both cases,
   `restore` stops with `noahsark: restore: the directories below the
   destination changed during the restore; run restore again` and exit
   code 1.

After the last disc, `restore` walks the snapshot one more time. It reads no
blob and no disc in this walk. It reports each file that is not complete, and
applies the metadata of each directory after every entry below it, thus the
deepest directory first.

### 14.3 The part file and resume

`restore` writes the bytes of a file into a hidden part file in the directory
of the file, named `.<name>.noahsark-part`. When the snapshot holds a file of
that name itself, `restore` adds a number to the suffix. A part name must fit
the name limit of 255 bytes of the destination filesystem. For a name longer
than 232 bytes, the part name keeps the first 200 bytes of the name, then `~`
and the first 16 hex digits of the SHA-256 of the whole name. The final size is set
one time, so that a file with a hole keeps the hole.

The final name appears one time, when the last chunk has landed: `restore`
links the part file to the final name, then unlinks the part file. A link
fails when the name already exists, thus the no-overwrite rule holds with no
race. With `--overwrite` the path in the way is unlinked first. A filesystem
that has no hard link falls back to a check and a rename.

Before the link, `restore` reads the whole part file. It checks the size and
each chunk against the blob, then flushes the part file to stable storage
(`fsync`). Thus a power loss cannot leave a final name with no data. A part
file that does not match stays, `restore` reports the file as `file not
restored`, and the next run checks each chunk of the part file again. Before
a walk of a disc ends, `restore` flushes each directory that got a final name
in the walk. The cost is one more read of each restored byte, one `fsync` for
each restored file, and one `fsync` for each directory that gets a final name.
With many small files, the `fsync` of each file is the main cost.

A blob whose chunk lengths do not add up to the size of its tree entry fails
its file before `restore` writes a byte of it.

At each open of a part file, `restore` checks each chunk that is already there
against its content id, and skips the good ones. A stopped run leaves its part
files. The next run of the same `restore` completes them, and asks only for
the discs that it still needs. `restore` never deletes a part file that it did
not write.

`restore` checks the content id of every object after it reads it. An object
that does not verify fails the one file that needs it. `restore` writes no bad
data, goes on, and reports at the end ("Failure policy").

**A damaged copy.** An object of a disc that does not read or does not verify
fails each file that needs it. `restore` writes no byte of that object. The
part file of each such file stays, with the chunks that verified. `restore`
restores every other file, reports each failed file as `file not restored`,
and exits with code 1. The operator then mounts the second copy of the same
disc at `DIR` and runs the same `restore` again. The two copies carry the
same disc uuid, thus `restore` takes the second copy as the disc. The second
run skips each file that the first run completed, and counts it on the
`skipped:` line. It checks each part file, and reads from the disc only the
chunks that the part files still need. When every file is complete, it exits
with code 0. A copy whose `DISC.bin` does not read is not the disc: `restore`
asks for the disc ("Disc detection") and reads nothing from that copy.

### 14.4 Output lines and exit codes

`restore` writes the plan and the result to standard output, and every
other line to standard error. `ID` is the short snapshot id. `DEST` and `DIR`
are the texts that the operator gave. `PATH` in a warning line is the
absolute path below `DEST`.

| Line | Stream | When |
|---|---|---|
| the plan: the `disc` lines, the `restore: N item(s) have no disc known to the catalog; run recover with more discs` line, the `totals:` line | standard output | first, before any disc |
| `disc SEQ "LABEL": found` | standard output | the expected disc is at `DIR` |
| `expected disc SEQ "LABEL" (UUID), found ...` | standard error | a wrong disc is at `DIR` |
| `insert disc SEQ "LABEL" (UUID) into DIR and press Enter` | standard error | the prompt |
| `restore: insert disc SEQ "LABEL" (UUID) into DIR and run restore again` | standard error | the stop: no terminal, or the end of input. Exit code 1. No line follows. |
| `noahsark: restore: warning: PATH: REASON` | standard error | one line for each problem, 20 lines at most |
| `noahsark: restore: warning: N more problem(s) not shown` | standard error | more than 20 problems |
| `noahsark: restore: warning: not restored: N KIND, N KIND; see the warning(s) above` | standard error | the summary, after the problem lines, when there is a problem |
| `restored snapshot ID into DEST` | standard output | after the last disc, also after problem lines |
| `skipped: N file(s) already restored` | standard output | `DEST` already held N files or symlinks with the content of the snapshot |

The problem lines come in the order that `restore` meets the problems. The
walk of the first disc reports the paths that exist and the special files.
The walk of each disc reports the files that fail and the file metadata that
does not apply. The last walk reports each file that is not complete and each
directory whose metadata does not apply, in walk order: a file before the
directory that holds it.

The summary names each kind that has a problem, in this order: `existing
path(s)`, `path(s) --overwrite could not replace`, `unsupported entry(ies)`,
`metadata field(s)`, `file(s) not restored`.

`restore` stops with one line on standard error in these cases. Each case
but the last stops before the plan.

| Line | Exit code |
|---|---:|
| `noahsark: restore: --disc=DIR is required`, then `usage: noahsark restore [--overwrite] [--dry-run] --disc=DIR SNAPSHOT [PATH...] DEST` | 2 |
| `usage: noahsark restore [--overwrite] [--dry-run] --disc=DIR SNAPSHOT [PATH...] DEST`, when `SNAPSHOT` or `DEST` is missing | 2 |
| `noahsark: restore: no repository; run recover first, one time for each disc` | 2 |
| `noahsark: restore: REASON` for a `SNAPSHOT` that names no snapshot | 2 |
| `noahsark: restore: no entry of the snapshot matches PATH` | 2 |
| `noahsark: restore: snapshot ID is partial; run recover with more discs` | 1 |
| `noahsark: restore: REASON` for a `config.yaml` with a bad value or an unknown key | 2 |
| `noahsark: restore: REASON` for a `config.yaml` that does not read | 1 |
| `noahsark: restore: REASON` for every other failure, such as a catalog read that fails | 1 |

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
5. Each hardlinked source path is restored as an independent file, as
   FORMAT.md's "Hardlinks" states.
6. `restore` does not create a FIFO, a socket or a device node.
7. `restore` does not read the `UNSTABLE` flag.

### 15.2 Failure policy

`restore` is strict for data and best-effort for metadata. It has one report.
Each problem carries the path, a kind and the reason.

| Kind | Meaning | Exit code |
|---|---|---:|
| existing path | The path is already there with other content or another type, and `--overwrite` was not given. `restore` left it as found. | 1 |
| `--overwrite` could not replace | A directory that holds entries stood where a file or a symlink must go. `restore` never removes a directory tree. | 1 |
| unsupported entry | A device node, a FIFO or a socket. | 0 |
| metadata field | A `mode`, `times` or `owner` field that would not apply. | 1 |
| file not restored | A bad object, a chunk on a `lost` disc, a chunk with no known disc, or a write that failed. | 1 |

`restore` prints one line for each problem, 20 lines at most, then the count
of the rest, then one summary line that counts each kind. Every one of these
lines has the prefix `noahsark: restore: warning: `. The line of a problem is
`noahsark: restore: warning: PATH: REASON`.

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

```
noahsark [GLOBAL-OPTIONS] COMMAND [SUBCOMMAND...] [COMMAND-OPTIONS] [ARGUMENTS]
```

The commands are `init`, `commit`, `pack`, `verify`, `status`, `gc`,
`restore`, `recover`, `ls` and `log`. The command groups are `disc`, with the
subcommands `burned`, `verified` and `lost`, and `image`, with the subcommand
`build`. There is no `help` command.

- A global option comes before the command name. A command option comes after
  the last command or subcommand word and before the positional arguments. An
  option in the wrong position is a usage error: the tool names the correct
  position and exits with code 2. `-h` is the one exception: it works in both
  positions.
- The build refuses an option that comes after a positional argument, and
  names it.
- A group has no options of its own, other than `-h`. A group with no
  subcommand, or with an unknown subcommand, is a usage error: the tool lists
  the subcommands and exits with code 2.
- `noahsark -h` lists the commands and the groups. `noahsark COMMAND -h` and
  `noahsark -h COMMAND` print the help of the command, or the subcommands of a
  group, and exit with code 0.
- The tool checks the syntax before it reads the repository. A usage error
  changes nothing.
- `status` prints the full `next:` block. The block holds the exact command
  lines for the next step. `status` is the one source of that step.
- A command that changes state ends its output with one line: `next:
  noahsark status`. These commands are `init`, `commit`, `pack`, `pack
  --undo`, `disc burned`, `disc verified`, `disc lost`, `verify`, `verify
  --undo`, `gc` and `recover`. `disc burned --undo` and `disc lost --undo`
  also print it. A `verify` that is counted prints it, also when the check
  fails. The line goes to standard output, and it is the last line.
- A command that changed nothing because it was refused, or because the
  operator answered no, prints no `next:` line. `restore`, `ls`, `log`,
  `image build` and every `--dry-run` run print none. `verify --no-mark`
  and a `verify` that is not counted change no state, and print none.
  `docs/states.md`, "State x event table", gives the rule for each row.

| Global option | Meaning |
|---|---|
| `--repo=PATH` | The repository ("Repository discovery"). Every command but `init` accepts it. |
| `-q`, `--quiet` | Print no progress line. Without it, a long command writes a progress line to standard error, only when standard error is a terminal. |
| `--yes` | Answer yes to an ordinary confirmation ("Confirmations"). |
| `--force-yes` | Answer yes to an ordinary and to a critical confirmation. |
| `-h` | Print help and exit. |
| `--version` | Print the version and exit. |

A `DISC` argument names one disc of the repository. A value of 1 to 7
decimal digits is a disc number and nothing else; a leading zero is allowed.
Every other value is a uuid prefix: a value of 8 or more decimal digits, or a
value that holds a hexadecimal letter or a hyphen. A uuid and a uuid prefix
can have hyphens and can use any letter case. A value that matches no disc is
a usage error: the tool prints `no disc matches ARG` and exits with code 2. A
value that matches more than one disc is a usage error: the tool lists the
candidates and exits with code 2. Each candidate line has the form
`disc SEQ "LABEL"  UUID`, with two spaces before the uuid. The uuid is in
lower case, with hyphens. An undone disc matches nothing.

A `SNAPSHOT` argument is a ref name, a full snapshot id, or a prefix of the
digest part of a snapshot id. The tool resolves it in this order:

1. A ref name wins over a prefix. A ref name is exact, with its letter case.
2. A prefix has 1 to 64 hexadecimal characters, in any letter case. It must
   match one snapshot.

The refs come from `state/refs.txt` and from the REFS table of every disc in
the catalog. For each name, the newest record wins. A REFS table of the
catalog that does not decode does not stop the command: it uses the other
tables, and prints one line `noahsark: COMMAND: warning: the catalog REFS
table of disc UUID does not decode; the refs of the other tables are used;
run recover, or verify, with that disc: ERROR` to standard error. A full id
and a prefix still match. `ls` and `restore` then go on with their own exit
code. `log` lists what it can and exits with code 1. A value that matches no
snapshot is a usage error: the tool prints `no snapshot matches ARG` and
exits with code 2. A prefix that matches more than one snapshot is a usage
error: the tool prints the line `ARG matches more than one snapshot:`, then
one line `snapshot FULL-TEXT-ID` for each candidate, in the order of the
digest, and exits with code 2. The list uses the full text id, because the
12-character form of two candidates can be the same.

A `DISC-ROOT` argument and `--disc=DIR` name one directory that holds
`NOAHSARK/`: the mount point of a disc, or a copy of a disc root.

### 16.2 Syntax

```
noahsark init    [--source=PATH]
noahsark commit  [--ref=NAME] [-m MESSAGE] [--exclude=PATTERN]...
                 [--one-file-system] [SOURCE]
noahsark pack    --capacity=SIZE [--close] [--out=DIR] [--dry-run]
noahsark pack    --undo DISC
noahsark image build [--force] DISC
noahsark disc burned [--undo] DISC
noahsark disc verified DISC
noahsark disc lost [--undo] DISC
noahsark verify  [--no-mark] DISC-ROOT
noahsark verify  --undo DISC
noahsark status
noahsark gc      [--dry-run]
noahsark restore [--overwrite] [--dry-run] --disc=DIR SNAPSHOT [PATH...] DEST
noahsark recover --source=PATH --disc=DIR
noahsark ls      [-R | --recursive] SNAPSHOT [PATH]
noahsark log     [REF | SNAPSHOT]
```

Each line takes the global options before the command name. A positional
argument that a line does not have is a usage error: the tool prints the
usage line of the command and exits with code 2. For example, `noahsark pack
--capacity=bd25 extra` prints `usage: noahsark pack --capacity=SIZE
[--close] [--out=DIR] [--dry-run]`.

### 16.3 Options

| Command | Option | Meaning |
|---|---|---|
| `init` | `--source` | The source root. `init` stores the absolute path as `sources.root`. |
| `commit` | `-m` | The commit message, stored on the snapshot. |
| `commit` | `--ref` | The ref to move. Default: the local date of today, `YYYY-MM-DD`. The name follows the ref name rule ("Refs"). |
| `commit` | `--exclude` | An exclude pattern ("Excludes"). Repeatable. |
| `commit` | `--one-file-system` | Do not cross a mount point. |
| `pack` | `--capacity` | The target capacity ("Capacity"). Required, except with `--undo`. |
| `pack` | `--close` | Make `status` print the sealing burn line for this disc. Nothing else changes. |
| `pack` | `--out` | The directory that receives the disc root. It must be empty or absent. `staging/plans/<disc-uuid>/tree` becomes a symlink to it. |
| `pack` | `--dry-run` | Predict the disc count and stop ("Dry run"). |
| `pack` | `--undo` | Undo the pack of the newest disc ("Undo a pack"). No other option goes with it. |
| `image build` | `--force` | Build the image again when it exists. |
| `disc burned` | `--undo` | Remove the burn record of a `burned` disc. |
| `disc lost` | `--undo` | Remove the lost mark of a found disc. |
| `verify` | `--no-mark` | Check and write nothing. |
| `verify` | `--undo` | Remove the verified record of a `verified` disc. No other option goes with it. |
| `gc` | `--dry-run` | Print what `gc` would free, and free nothing. |
| `restore` | `--disc` | The mount point of the one drive. One value. Required. |
| `restore` | `--overwrite` | Unlink an existing path first and then create it. |
| `restore` | `--dry-run` | Print the plan and stop. |
| `recover` | `--source` | The source root. Required. `recover` stores it as `sources.root`. |
| `recover` | `--disc` | The mount point of the disc to read. One value. Required. |
| `ls` | `-R`, `--recursive` | Descend into subdirectories. |

### 16.4 Command notes

**`init`** makes the current directory a repository. It writes `config.yaml`
with a new `repo.uuid`, `staging.dir: staging`, `sources.root` from
`--source`, and `pack.device: /dev/sr0`. It creates `state/`, `catalog/` and
`staging/`, writes `.gitignore`, and writes `config.yaml` last. It syncs each
of them. A crash before `config.yaml` thus leaves a directory that is not a
repository, and `init` runs again. It prints `initialized repository PATH`,
`source: PATH` (only when `--source` is given), `device: DEV`, and `next: noahsark status`. To use another device, the operator edits `pack.device` in
`config.yaml`. `init` exits with code 2 when the directory already is a
repository. Do not run `init` to recover a lost repository.

**`commit`** prints `snapshot ID`, `ref NAME -> ID`, `new items: N, existing
items: N`, `unstable: N, skipped: N`, one line for each unstable, skipped,
special or left-out path ("Source policy"), `staged: N items, B bytes`, and `next: noahsark status`. `ID`
is the full text form of the snapshot id ("Refs"). `B` is
the sum of the stored file sizes of the Staged items: the chunk files in
`staging/chunks/` and the metadata object files in `catalog/`. `N` and `B`
count only the Staged items whose file exists. When a Staged item has no file,
`commit` prints `noahsark: commit: warning: N staged item(s) have no file in
the staging store; commit the same source again` on standard error after the
`staged:` line; the warning does not change the exit code. Exit: 1
when a file was skipped or unstable; the snapshot is committed all the same,
and `commit` still prints the `next:` line. A special file never changes the
exit code.

The path lines are records on standard output, in this order:

| Line | When |
|---|---|
| `unstable PATH: the file changed during the read` | The file changed during each read ("In-flight change detection"). |
| `skipped PATH: REASON` | The file could not be read, or a tree entry cannot hold its name ("Source policy"). |
| `mount point PATH: not crossed, recorded as an empty directory` | `--one-file-system` and a directory on another device. |
| `excluded PATH: WHAT` | The repository, the staging store, or the disc root of a `pack --out`. |
| `special PATH: KIND, no content is backed up` | A FIFO, a socket or a device node. `KIND` is `FIFO`, `socket`, `character device` or `block device`. 20 lines at most. |
| `special files not shown: N` | More than 20 special files. |
| `special files: N; a FIFO, a socket and a device node carry no content, and restore does not create them` | One special file or more. |
| `excluded: N path(s)` | An exclude pattern left out one path or more. |

`PATH` is relative to the source root. `commit` escapes `PATH` and `REASON` as
`ls` escapes a path, thus each record is one line. `commit` prints
`noahsark: commit: MESSAGE` on standard error only for a failure.

**`pack`** prints `packed disc SEQ "LABEL": N item(s), B bytes`, then `uuid:
UUID`, then `next: noahsark status`. The label is the newest ref of the
repository and the text ` disc SEQ`, for example `2026-09-14 disc 0`. With no
ref, the label is `disc SEQ`. With nothing staged, it prints `pack: nothing
staged`, then `next: noahsark status`, and exits 0. With nothing staged and
`--dry-run`, it prints only `pack: nothing staged`. A dry run prints the lines
of "Dry run" and no `next:` line. For each item that it cannot take
("Packing rules"), `pack` and `pack --dry-run` print one warning on standard
error:

```
noahsark: pack: warning: snapshot ID: cannot pack all of it: PROBLEM; REPAIR
noahsark: pack: warning: cannot pack: PROBLEM; REPAIR
```

The second form names an item that no snapshot reaches. `PROBLEM` is `KIND
ID is damaged`, `the file of KIND ID is missing`, `KIND ID cannot be read:
ERROR` or `staged item ID has no file`. `REPAIR` is "Failure and recovery
actions". After a warning, `pack` still packs the other items and prints its
lines, then exits 1. When it can take no item at all, it prints no line on
standard output and exits 1. Exit: 2 for a missing capacity, an `--out` path
inside the repository or the staging store, an `--out` directory that holds
files, an `--out` path that is not a directory, or a capacity too small for
one item. 1 while a disc is `missing`, and after a warning.

**`image build`** is "Disc filesystems and image building". It prints no
`next:` line.

**`disc burned`**, **`disc verified`** and **`disc lost`** change the
records of one disc, as `docs/states.md` gives. Each ends with `next:
noahsark status` after it changed a record. `disc verified` and `disc
lost` ask a critical confirmation. `disc burned --undo` and `disc lost
--undo` ask an ordinary confirmation. `disc lost` removes
`staging/plans/<disc-uuid>/` (for a `pack --out` disc, the symlink only), and
keeps the catalog data of the disc. It does these steps in this order: it
appends the `Lost` event, then the item records as one batch, then it
removes the plan directory. The batch returns each Packed item to Staged.
It returns each OnDisc snapshot, tree and blob object that the catalog
holds to Staged: `pack` reads these objects from the catalog. It records
each other OnDisc item, a chunk whose file `gc` freed, as Lost. For a
`packed`, `burned` or `verified` disc it prints `disc SEQ "LABEL": marked
lost; N item(s) returned to staged`. For an `on disc only` disc it prints
`disc SEQ "LABEL": marked lost; I item(s) returned to staged; N item(s) need
a new commit`: `I` counts the objects of the catalog, and `N` the Lost
items.
`disc lost --undo` appends the `LostUndone` event, then the item records as
one batch. For a disc that was `verified`, it finds the items of the disc
in the catalog INDEX of the disc: a Staged record names no disc. It reads
the INDEX before the event, and refuses with exit code 1 when it cannot. It
gives back each item that the INDEX lists and that is Staged. For a disc
that was `on disc only`, it takes the Lost items whose record names the
disc, and the catalog objects that `disc lost` staged and that the INDEX of
the disc lists. "State log replay" gives the item records, and the repair
after a stop between the event and the item records.

**`verify`** prints `disc SEQ "LABEL": N items, ok` or `disc SEQ "LABEL":
bad; REASON`, then one line that names what changed, as `docs/states.md`,
rows 31 to 46, gives. It ends with `next: noahsark status` when it wrote a
record or a verify log event. `--no-mark` and a `verify` that is not counted
print no `next:` line. A failed check of a root that is not counted prints
`disc SEQ "LABEL": bad; REASON`, then `not counted: this is not a disc`, and
exits with code 1. With no repository, it names the disc by uuid. Exit: 1
when the check failed, or when the state refused the command. 2 for `--undo`
with another option. "Verify" gives the order of the checks, the `REASON`
texts and the count `N`.

**`status`** prints `staged: N items, B bytes`, then one snapshot line for
each snapshot that is not complete on discs, then the `lost:` line while an
item is Lost, then one disc line for each disc that is not undone, then one
`next:` block:

```
snapshot ID: N items staged, not complete on discs; recover cannot find it from the discs alone
snapshot ID: N items staged, not complete on discs; the discs alone cannot restore all of it
lost: N items; only a lost disc holds them
disc SEQ "LABEL"  STATE  UUID
```

The first form names each snapshot whose snapshot object is Staged. `pack`
puts the snapshot object on a disc only after each item that it reaches,
thus `recover` from the discs alone cannot find such a snapshot. The second
form names a snapshot whose snapshot object a disc holds, while `pack` still
takes Staged items with it: items of a lost disc ("Packing rules"). `ID` is
the short form of the snapshot id ("Refs"). `N` is the number of Staged items
that `pack` takes with the snapshot: the Staged items that it reaches and
that no snapshot before it in the pack order reaches, its snapshot object
included when it is Staged. Thus the `N` of all lines add up to the staged
total, less the Staged items that no snapshot reaches. The plural form
`items` is fixed, also for 1. The snapshot lines come in the order in which
`pack` takes the snapshots ("Packing rules"). The snapshot lines change no
`next:` block: the staged items are staged data.

`N` in the `lost:` line is the number of items whose newest record is Lost.
The plural form is fixed. The state log holds no size, thus the line holds
no byte count. The line stays for as long as an item is Lost, also after a
`commit` that does not make the same data again. No command dismisses it.
It goes away when the items leave Lost: a `commit` of a source that still
holds the data, `recover` of a disc that holds them, or `disc lost --undo`.
`status` counts the Lost records of the state log that it already reads. It
reads no object file for the line, thus it names no snapshot.
`restore --dry-run SNAPSHOT` names the lost disc that a snapshot needs. The
line changes no `next:` block, and is printed also when the staging directory
does not exist.

For each item that `pack` cannot take ("Packing rules"), `status` prints the
warning of `pack` on standard error, with `status` in place of `pack`. For a
snapshot object that a disc holds and whose catalog file is missing or does
not verify, `REPAIR` is `run recover with a disc that holds it`. `status`
prints all its lines on standard output as usual, then exits 1.

`status` reads no chunk file, thus it does not check a chunk. It looks only
whether the file of each Staged item exists. The `staged:` line counts only
the Staged items whose file exists. When a Staged item has no file, for
example after the operator emptied the staging directory, `status` prints its
other lines as usual, then this line on standard error, and exits 1. `N` is
the number of the Staged items with no file. The snapshot lines still count
such an item.

```
noahsark: status: warning: N staged item(s) have no file in the staging store; commit the same source again
```

When the staging directory does not exist and the state log holds a Staged
or a Packed item, `status` prints no `staged:` line and no snapshot line. It prints the warning that "The repository directory"
gives, the disc lines, and the `next:` block of a missing staging
directory, then exits 1.

Two spaces separate the fields. `STATE` is the state word, with the suffix
`, last check DATE`, `, last check failed DATE` or `, not checked` where
`docs/states.md`, "Disc states", gives one. `DATE` is a local date, as `YYYY-MM-DD`. `B` in the
`staged:` line is the sum of the stored file sizes of the Staged items: the
chunk files in `staging/chunks/` and the metadata object files in `catalog/`.
`docs/states.md`, "Disc states" and "State to `next:` block", give the words
and the blocks. `status` writes nothing.

**`gc`** prints the freed line first, then one line for each disc that holds
items back, then one line for each disc whose items it skipped:

```
gc: freed N item(s), B bytes
gc: disc SEQ: not verified; N item(s) held
gc: N item(s) skipped: disc SEQ's table is not in the catalog
gc: N item(s) skipped: disc SEQ's table does not list them
```

The last skip line means that the INDEX of a `verified` disc in the catalog
does not list N items of the disc. `gc` then frees no item of that disc and appends no `Freed` event
for it.

In the freed line, `N` counts every item that `gc` records OnDisc, also an
item with no chunk file, and each orphan whose chunk file it unlinked. `B`
is the disk space that `gc` frees: the allocated blocks of the chunk files
that it unlinked and of the regular files in the plan directories that it
removed. A sparse image thus counts its allocated blocks, not its apparent
size. On a platform with no block count, a file counts its apparent size. A
symlink adds nothing. A held line appears only for a disc that still has a
Packed item and is not `verified`: a `packed` or a `burned` disc.

The freed line is always present, also as `gc: freed 0 item(s), 0 bytes`.
The last line is `next: noahsark status`. `--dry-run` prints no `next:`
line. A
`missing` or `lost` disc gets no line. It asks no confirmation. When `gc`
cannot unlink a chunk file or remove a plan directory, it prints `noahsark:
gc: PATH: ERROR` to standard error for each one. It still prints the freed
line and the `next:` line, and the records stay written. Exit: 0 when
nothing was eligible. 1 when a chunk file could not be unlinked or a plan
directory could not be removed, or when an item was skipped because its
INDEX is not in the catalog or does not list it.

`gc --dry-run` prints the same lines, with `gc: would free N item(s), B
bytes` in place of the freed line. It counts every orphan and every file
that `gc` would remove, with the same rule for `B`. It prints no `next:` line. It exits with code 1 when `gc` would skip
an item, else 0.

**`restore`** takes a snapshot id prefix or a ref name. For a `partial`
snapshot, it prints `snapshot ID is partial; run recover with more discs`
and exits with code 1 before it reads a disc. It prints the plan,
the disc lines, the problem lines as `noahsark: restore: warning: PATH:
REASON`, then `restored snapshot ID into DEST`, then `skipped: N file(s)
already restored` when `DEST` already held a file. It prints no `next:`
line. Exit: see "Failure
policy". 1 also when it stops for the next disc with no terminal.

**`recover`** reads one disc for each call, from `--disc=DIR`. It creates the
repository directory, `config.yaml` and `.gitignore` when they are absent,
and merges into the state that exists. It stores `--source` as
`sources.root`. It refuses a disc whose `repo_uuid` differs from the
repository: it prints `disc UUID belongs to repository RUUID, not to this
repository` and exits with code 1. It reads and checks every object of the disc, as
`verify` does. It writes the catalog objects and tables that it can read, the
disc ledger rows of the disc and of each disc that the disc names, the refs,
the OnDisc records of the items that pass their check, and a `Recovered`
event. `Recovered` never carries the `close` flag: no disc byte records the close
choice. The flag changes only the burn line of `status`, and `status` never
prints a burn line for an `on disc only` disc. For a disc that
the repository already knows and that is not `missing`, it writes only the
catalog entries, and prints `recover: ok; disc SEQ "LABEL" already known`.
It still checks every object of that disc. It writes no event. The one
exception is an `on disc only` disc whose last check failed, as after a
recover of a damaged copy: a copy with no damaged object records each item
of the disc that has no record, or that is `Lost`, as OnDisc, appends
`CheckOK`, and prints `recover: ok; disc SEQ "LABEL" already known; check
logged` (`docs/states.md`, row 70e). When an object
is damaged, it prints the `damaged:` lines below instead of `ok`, and exits
with code 1. It treats an item that is `Lost` in the log as not known, and
records it as OnDisc. After each call it computes the value of
`catalog-state.txt` again ("Catalog layout"). For each disc that the disc names and that the repository does not know, it
appends `NamedMissing`. A partly damaged disc is not skipped: `recover` keeps
each object that passes its check, appends `CheckFailed` after `Recovered`,
and prints these lines:

```
recover: damaged: ID
recover: N item(s) damaged on disc SEQ "LABEL"
```

There is one `damaged:` line for each damaged object, with its full text id,
then the count line.
Then it exits with code 1. Else it prints `recover: ok`, or one `recover:
disc SEQ "LABEL" (UUID) named by another disc, not yet given` line for each
`missing` disc, and then exits with code 1. Each of these ends with `next:
noahsark status`, unless `recover` refused the disc. Do not run `disc burned` or
`verify` for a recovered disc to raise its state: it is `on disc only`.

`recover` stores `--source` as an absolute path: a relative path is taken
from the working directory. It refuses these cases before it writes the
catalog or a record, with exit code 1:

- `--disc=DIR` is not a counted mount ("Transition rules"). It prints
  `DIR is not counted: REASON; recover reads only a read-only mount point
  outside the repository`. `REASON` is one of the texts of "Transition
  rules".
- The disc cannot be read: `NOAHSARK`, `DISC.bin` or `INDEX.bin` is missing
  or does not pass its check, or neither run header copy (`RUN.bin` and
  `RUN2.bin`) passes its check. It prints `DIR: cannot read the disc:
  ERROR`.
- The disc log records the disc as undone by `pack --undo`. It prints `disc
  UUID was undone by pack --undo; it is not in this repository`.

`recover` writes in this order: the catalog objects, the catalog tables and
`catalog-state.txt`; the disc ledger and the ref ledger, then `refs.txt`; the
item records; the disc events. A `recover` that stops before the events
leaves a disc that the repository does not know, or that is still `missing`.
A repeat of the call reads the disc again and writes each step again. Each
step merges, thus the repeat gives the same result as one call.

`recover` adds the ref records of the disc to the ref ledger. Then, for each
name that the disc carries, it takes the newest record of the name on the
disc ("Ref" in FORMAT.md gives the rule). A line of `refs.txt` has no time
of its own: it takes the time of its snapshot, as `restore`, `ls` and `log`
do. When the record is newer than the line of the name, `recover` writes
the snapshot of the record into `refs.txt`. Else the line stays, thus a
name that a later `commit` moved keeps its newer snapshot. A name that the
disc does not carry stays as it is.

A damaged file that is not an object, for example `README.txt`, one run
header copy (`RUN.bin` or `RUN2.bin`), gives the line `noahsark: recover: damaged file: FILE:
REASON` on standard error and no `damaged:` line. The count line does not
count it. The disc still gets `CheckFailed`, and `recover` exits with code 1.
When `REFS.bin` or `DISCS.bin` is damaged, `recover` writes none of the
INDEX, REFS and DISCS tables of the disc into the catalog. A damaged
`REFS.bin` gives no ref. A damaged `DISCS.bin` gives no disc ledger row and
no `NamedMissing` event.

The `named by another disc` lines name each `missing` disc of the
repository, not only the discs that this disc names. They are sorted by the
disc number, then by the uuid.

**`ls`** lists the entries of a snapshot below `PATH`, or below the source
root, from the catalog. For a `partial` snapshot ("Catalog layout"), it
prints `snapshot ID is partial; run recover with more discs` and exits with
code 1. Without `-R`, it lists one level. It prints one entry
on each line. The fields are separated by one tab, in this order:

| Field | Form |
|---|---|
| mode | The permission bits and the setuid, setgid and sticky bits, as 4 octal digits, for example `0644`. |
| type | `file`, `dir`, `symlink`, `fifo`, `socket`, `chardev` or `blockdev`. An `entry_type` with no word prints as `typeN`, with `N` in decimal. The tree decoder refuses such a type, thus a catalog tree never gives it. |
| size | The content size in bytes, as a decimal number. 0 for a directory and a special file. The target length for a symlink. |
| time | The mtime as RFC 3339 in UTC, to the second, for example `2026-09-14T08:30:00Z`. |
| path | The path relative to the source root, with no leading `/` and no trailing `/`. |

In a path, the tool writes a backslash as `\\`, a tab as `\t`, a newline as
`\n`, each other byte below 0x20, the byte 0x7F, and each byte that is not
part of valid UTF-8 as `\xHH`, with two lower-case hexadecimal digits. Every
other byte prints as it is. `ls` sorts
the entries in the canonical order of FORMAT.md's "Canonical ordering",
depth first. A path that `ls` prints goes into a `restore` line unchanged,
when it holds no escaped byte.

`ls` splits `PATH` at each `/`. It ignores a leading `/`, a trailing `/` and
an empty segment. A `PATH` with no segment lists the source root. It compares
each segment with the raw bytes of an entry name, not with the escaped form.
When the root tree holds more than one source root, the first segments of
`PATH` are the segments of a source root path. A `PATH` that names a
directory lists the entries of that directory. A `PATH` that names another
entry prints the line of that entry only. A `PATH` that the snapshot does not
hold is a usage error: `ls` prints `PATH is not in snapshot ID`, with `PATH`
escaped, and exits with code 2.

`ls` and `log` treat a snapshot as partial in these cases:
`catalog-state.txt` marks it `partial`; the catalog does not hold its
snapshot object, and a ref or `catalog-state.txt` names it; or `ls` reads a
tree that the catalog does not hold or that does not decode. In the last
case `ls` has already printed the lines before that tree. The partial line
goes to standard error, and the exit code is 1. A full snapshot id that no
catalog object, no ref and no line of `catalog-state.txt` names matches no
snapshot, with exit code 2. In a repository with no snapshot, every
`SNAPSHOT` argument matches no snapshot, and `log` with no argument prints
nothing and exits with code 0.

**`log`** lists every snapshot of the catalog, newest first. With a `REF` or a
`SNAPSHOT`, it prints the line of that one only. It prints one snapshot on
each line. The fields are separated by one tab, in this order: the snapshot
id (12 characters), the time as RFC 3339 in UTC, the ref names separated by
`,` or `-` when there is none, the source path, and the message or `-` when
there is none. The ref names are sorted by their bytes. When the root tree
holds more than one source root, the source path field holds each root
path, separated by `,`. The source path field is `-` when the catalog does
not hold the root tree. The ref names, the source
path and the message are escaped as an `ls` path is. A value that is
exactly `-` prints as `\x2d`, thus a field of `-` always means no value.
In the ref names field and the source path field, a `,` inside a value
prints as `\x2c`, thus each `,` of these fields separates two values. The
message field keeps a `,` as it is.
`log` sorts the lines by the snapshot time, with its nanoseconds, newest
first. The lines of snapshots whose object the catalog does not hold come
last. Lines of the same time are in the order of the snapshot id bytes. When `refs.txt` or a
catalog REFS table names a snapshot whose object the catalog does not hold,
`log` prints a line for it with `-` in the time, the source path and the
message fields. The time is in UTC, not in local time. When a line names a
`partial` snapshot, `log` prints all its lines, then `snapshot ID is partial;
run recover with more discs` for each such snapshot to standard error, and
exits with code 1.

### 16.5 Confirmations

Six commands ask before they change a record. `pack --undo`, `disc burned
--undo`, `verify --undo` and `disc lost --undo` ask an ordinary confirmation.
`disc verified` and `disc lost` ask a critical confirmation.

1. The command prints a warning to standard error. It names the disc (number,
   label, uuid), the state now, the state after, and the effect.
2. The command prints `Continue? [y/N]` to standard error and reads one line
   from standard input. The answers `y` and `yes` continue, in any letter case.
   Spaces around the answer are ignored. Every other answer, an empty line,
   and the end of input change nothing.
3. `--yes` answers an ordinary confirmation. `--force-yes` answers both kinds.
   With an answer flag, the command prints the warning and does not ask.
4. With no terminal on standard input and no answer flag that covers the
   confirmation, the answer is no. The command prints the warning, does not
   ask, and reads nothing.
5. An answer of no changes nothing. The command prints `nothing changed` and
   exits with code 1. For a critical confirmation with `--yes` and no
   terminal, the line is `nothing changed; COMMAND needs --force-yes`.
6. A refused state prints the refusal and does not ask.

`docs/states.md`, "Confirmations", gives the rows that test these rules.

## 17. Configuration reference

The config file is `<repo>/config.yaml`, in YAML. Each key is a nested
mapping: `repo.uuid` is the key `uuid` in the mapping `repo`. An unknown key
is an error, with exit code 2. `init` and `recover` write the file. The
operator can edit it. No command option overrides a key.

```yaml
repo:
  uuid: 0f1e2d3c4b5a69788796a5b4c3d2e1f0
staging:
  dir: staging
sources:
  root: /srv/data
pack:
  device: /dev/sr0
```

| Key | Type | Default | Meaning |
|---|---|---|---|
| `repo.uuid` | 32 hex digits | generated by `init` | The repository uuid. Never change it. |
| `staging.dir` | path | `staging` | The staging store. A relative path is relative to the repository directory. |
| `sources.root` | path | unset | The source root. `commit` uses it when no `SOURCE` is given. |
| `pack.device` | path | `/dev/sr0` | The drive that `status` names in the lines that it prints. The tool never opens it. |

## 18. Exit code registry

Every command uses exactly these three codes.

| Code | Meaning |
|---:|---|
| 0 | Success. An outcome that needs no operator action, such as `gc` with nothing eligible, is success. |
| 1 | A failure at run time, or a refused state: a read or a write failed, a needed disc or object is missing, the state of a disc refuses the command, a confirmation was answered no, the repository lock is held, or a partial success needs the attention of the operator. |
| 2 | A usage error: a bad option, a bad argument, an option in the wrong position, a bad config value, an unknown config key, an unknown command, a `pack` capacity that holds not one item, or no repository for a command that needs one. |

## 19. Failure and recovery actions

| # | Failure, and the message | Recovery action |
|---:|---|---|
| 1 | A burn fails midway | Discard the disc. When `growisofs` says that the image does not fit, run `pack --undo DISC` and pack again. Else run `status` and paste its block with a new blank disc. If `disc burned` already ran, run `disc burned --undo DISC` first. |
| 2 | `disc SEQ "LABEL": bad; this disc is bad; burn record removed`, or `...; verified record removed; gc holds the data` | Discard the disc. `status` prints the block that burns a new disc from the kept disc root. |
| 3 | `disc SEQ "LABEL": bad; the staged copy is already freed; ...` | Copy the disc now with `ddrescue`, or use the second copy. When no copy can be read, run `disc lost DISC`, then `commit`. |
| 4 | `verify`: `disc UUID is not in this repository` | Give the right `--repo`, or run `recover` with the disc. |
| 5 | A disc does not mount | The disc is bad. For a disc that is not yet `verified`: discard it, and run `disc burned --undo DISC` if `disc burned` ran. Then `status`. |
| 6 | Every copy of a disc is lost | `disc lost DISC`. `restore` restores what the other discs hold and names each file that it cannot restore. |
| 6a | `status`: `lost: N items; only a lost disc holds them` | `commit` the source. Each lost item that the source still holds is staged again, and the next `pack` takes it. The line stays while the source no longer holds an item: that data is gone. `restore` of an old snapshot then restores every other file, names each file that it cannot restore, and exits 1. When the disc turns up again, run `disc lost --undo DISC`. |
| 7 | `no repository; run recover first, one time for each disc` | `recover --source=PATH --disc=DIR`, one time for each disc. Then `restore`. |
| 8 | The repository directory is lost | `noahsark --repo=<new> recover --source=PATH --disc=DIR`, one time for each disc, in any order. Do not run `init` first. |
| 9 | `recover: disc SEQ "LABEL" (UUID) named by another disc, not yet given` | Run `recover` with that disc. When it is gone for good, run `disc lost DISC`. |
| 10 | `recover: damaged: ID` | The disc is `on disc only` with a failed check. Copy it now, or use the second copy, and run `recover` with the copy. |
| 11 | `the state log's tail was truncated; ...` | A crash left a torn tail. Run the interrupted command again. When it refuses because the disc already has the new state, its event was written, and the command with the lock wrote the rest (row 28). For a bad record in the middle of a log, run `recover` into a new repository. Do not put an old version of a log back (row 29). |
| 12 | `repository lock PATH is held; another noahsark command runs on this repository` | Wait for the other command. |
| 13 | `pack` stops: a read error or a sync error occurs while it writes the disc root | `pack` records nothing. Correct the cause, then run `pack` again. |
| 13a | `pack` or `status`: `warning: snapshot ID: cannot pack all of it: KIND ID is damaged; ...`, or `... the file of KIND ID is missing; ...` | `pack` took the other items. For a tree, a blob or a snapshot object: `commit` the same source again; `commit` writes the object again, then run `pack`. For a damaged chunk: delete the chunk file that the warning names, `commit` the same source again, then run `pack`; for a missing chunk file, `commit` then `pack`. When the source no longer holds the data, the item cannot be packed, and the snapshot stays Staged and not complete on discs. The other snapshots are not affected. No command drops a snapshot. |
| 13b | `pack`: `snapshot ID: ...; each disc carries it: run recover with a disc that holds it` | The catalog copy of a snapshot object that a disc holds is missing or damaged. Run `recover` with a disc that holds it; `recover` writes the object again. Then run `pack`. |
| 13c | `status` or `commit`: `warning: N staged item(s) have no file in the staging store; commit the same source again` | `commit` the same source; `commit` writes each missing file again. Then `status` and `pack`. When the source no longer holds the data, `pack` names each item that it cannot take (row 13a). |
| 14 | `pack`: `capacity ... holds not one item` | Give a larger `--capacity`. |
| 15 | `pack needs --capacity` | Give `--capacity`. |
| 16 | `image build needs root for the loop mount; run: ...` | Run the printed `sudo` line. |
| 17 | `image build`: `FILE exists; add --force to build it again` | Add `--force`. |
| 18 | `gc: N item(s) skipped: disc SEQ's table is not in the catalog` | Run `verify` of that disc, then `gc`. |
| 19 | `commit`: `unstable PATH: the file changed during the read`, or `skipped PATH: REASON` | The snapshot is written. Run `commit` again later. |
| 20 | `restore: N item(s) have no disc known to the catalog; run recover with more discs` | `restore` restores every other file. Run `recover` with each disc that you still hold, then run the same `restore` again. When no other disc exists, the files that it names cannot be restored. |
| 21 | `expected disc SEQ "LABEL" (UUID), found disc SEQ "LABEL" (UUID)` | Mount the right disc, or its second copy, and press Enter. |
| 22 | `restore` stops between two discs: killed, or no terminal | Mount the named disc and run the same `restore` again. It completes the part files. |
| 23 | `nothing changed` | The confirmation was answered no. Run the command again, and answer `y`, or give `--yes` (`--force-yes` for `disc verified` and `disc lost`). |
| 24 | An unknown format version on a disc | The tool refuses the disc. Use a newer tool. |
| 25 | `no repository; give --repo, or run noahsark init for a new repository, or noahsark recover for a lost one` | Give `--repo` or set `NOAHSARK_REPO`, or go into the repository. For a new repository, run `init` in its directory. After the loss of the repository, do not run `init`: run `noahsark --repo=PATH recover --source=SOURCE --disc=DIR` with each disc. |
| 26 | `snapshot ID is partial; run recover with more discs` | Run `recover` with each disc that you still hold, then run the command again. |
| 27 | `the state log ends in a part of a record; ...; another noahsark command writes the log at this time` | Nothing is wrong. Wait for the other command, then run the command again. |
| 28 | `disc SEQ "LABEL": an earlier COMMAND stopped before it wrote the records of its items; N item record(s) now written` | Nothing to do. The command that printed the note wrote the records, then did its own work. When `status` names such a disc, run the command that its `next:` block gives. Do not run the stopped command again: its disc event is written. |
| 29 | `state/ went back to an older version: ...` | `state/` is older than the staging directory, for example after a `git checkout` of an old commit. Put `state/` and `catalog/` forward again to the newest commit with git. When the newest version is gone, run `noahsark --repo=<new> recover --source=PATH --disc=DIR` with each disc, into a new repository. Commands that take no lock warn and go on. |
| 30 | `staging directory DIR does not exist; ...`, or the `status` warning `staging directory DIR does not exist; staging.dir in config.yaml names it`. Only while the state log holds a Staged or a Packed item. | Mount the volume of the staging store, or correct `staging.dir` in `config.yaml`. Then run `status`. When the staging store is gone for good, run `mkdir -p DIR`: `status` then names each `packed` disc with `disc lost`, and a `commit` of the same source writes the chunk files of the Staged items again. |

## 20. Test list

Every burn test uses an image file. `.github/workflows/ci.yml` runs the
composite actions `lint`, `unit` and `e2e` under `.github/actions/`.

**Unit tests** (`go test ./...`), by package:

- `internal/format`: each on-disc structure against a byte-exact golden file;
  the refusal of a nonzero reserved field, of another `header_len` and of
  `version_major` 0; canonical tree order; name validation.
- `internal/chunker`: golden cut points across buffer sizes; the Gear table.
- `internal/object`: the commit walk, compression, excludes, one file system,
  the `UNSTABLE` flag, unreadable and vanished files, the sync of each object
  file before its rename in groups, one sync of each directory.
- `internal/stage`: the golden state log record and the golden disc state log
  record; replay of both logs; each disc event against the replay table of
  `docs/states.md`; a refused event; the torn tail; a bad record in the
  middle; a close error; the flag bits of each event; the item records of
  each disc state after a stop between an event and its item records; the
  sequence mark and a roll back.
- `internal/image`: packing order, the capacity budget, the selection of many
  small objects whose filesystem overhead fills the disc, the staged total
  with a missing file, the dry run, the ledgers, `README.txt` and `FORMAT.txt` against the golden text,
  bounded memory, the content id check of each staged tree, blob and snapshot
  object, the `mkudffs` version check, the UDF image build. The tests that
  need root and a real `mkudffs` run only with `NOAHSARK_CI=1`. Without it
  they skip. With it, a missing tool fails them.
- `internal/catalog`, `internal/repolock`: the catalog by disc uuid, the
  writes of `commit`, the removal by `pack --undo`; the lock.
- `internal/restore`: a planted symlink, no overwrite by default, the skip of
  files that `DEST` holds, part files and resume, a file on two discs, `PATH`
  with and without a trailing slash, a `lost` disc and a chunk with no known
  disc in the plan, metadata as root and not as root, hardlinks, special
  files, case-folded names, the part name of a 255-byte name, bounded memory.
- `cmd/noahsark`: each command: options and their positions, output, the `ls`
  and `log` line formats and escapes, the escapes of the `commit` path lines,
  the ref name rule, exit codes, the `DISC` argument, the
  confirmations with a terminal, with no terminal, with `--yes` and with
  `--force-yes`, `config.yaml` with an unknown key, each command that takes
  the repository lock while another process holds it, `commit` and `status`
  with a Staged item that has no file.
- `cmd/noahsark`, the rebuild after `disc lost`: with the real commands,
  `commit`, `pack`, a counted `verify`, `gc`, `disc lost` of the `on disc
  only` disc, `status`, `commit` of the same source, `status`, and `pack` to
  a new disc. Then `restore` of the old and the new snapshot from the new disc
  alone gives the source back byte for byte. A second case deletes one source
  file before the second `commit`: its items stay Lost, `status` keeps the
  `lost:` line, and `restore` of the old snapshot restores every other file,
  names the lost file, writes no wrong data, and exits 1.
- `cmd/noahsark`, the state table test: a table-driven test reads the state x
  event table of `docs/states.md` row by row. It runs one case for each row,
  and checks the result, the message, the exit code and the next line. It
  fails when a row has no case, and when a case names a row that the table
  does not hold.

**The e2e cells** (`test/e2e/disc`, real `mkudffs` images). A loop mount that
the tool reads (`verify`, `restore`, `recover`) is read-only (`mount -o
ro,loop`), so that each `verify` of it is a counted mount. A test mounts an
image read-write only to write damage into it. It then unmounts the image and
mounts it again read-only before the tool reads it. With `NOAHSARK_E2E=1`, a
missing prerequisite (root, `mkudffs`, `mount`, `sudo`) fails the cell; it
does not skip it.

| Cell | What it proves |
|---|---|
| `media/dvd+r` | The full cycle on a DVD+R size image. Then the command-line cycle, and a disc root burned as ISO 9660 with the folder burn options (`-R -iso-level 4 -V NOAHSARK_0000` for disc 0) that `verify` counts and `restore` reads. |
| `media/bd25-forced-10g` | A 25 GB medium, packed at a 10 GB `--capacity`. |
| `chain/dvd-bd25-bd10` | Two snapshots of about 22.5 GB each across three discs. `pack` takes the older snapshot first. Data that does not fit stays Staged, and `status` names the snapshot that is not complete on discs. A fourth disc takes the rest. The repository is deleted, and `recover` runs one time for each of the four discs. Both snapshots then restore; restore swaps the discs at one mount point. A missing disc is named. |
| `lowmem` | The 25 GB flow under a memory limit: peak memory does not grow with the data size. |
| `incremental` | A second commit packs only the change. Both snapshots restore. The cell also runs the `internal/image` tests that need root and a real `mkudffs`. |
| `rebuild` | The repository is deleted. `recover` runs one time for each disc. `log`, `ls` and `restore` then work, and a later `pack` deduplicates against the discs. This proves that the discs alone hold the backup. |
| `lifecycle` | `disc verified` then `gc` frees the data of the disc at once; `pack --undo`; `verify --undo`; `disc lost` and `disc lost --undo`; each with `--yes` or `--force-yes`. |
| `damage` | Random damage detection ("Random damage detection"). |
| `cross-runner/build`, `cross-runner/restore` | Two jobs ("Cross-runner restore"). |
| `hostile` | A hostile source tree ("Hostile source tree"). |

**Random damage detection.** The test uses a fresh seed on each run and
prints the seed. The environment variable `NOAHSARK_E2E_DAMAGE_SEED` gives a
seed in place of the fresh one, to run it again. A fixed list of known seeds
also runs on each run. The test packs one small disc and keeps its image as
the second copy. For each seed, it copies the image and flips 1 to 4 random
bytes of the files below `NOAHSARK/` of the copy: the object files, the
snapshot files, `INDEX.bin`, `REFS.bin`, `DISCS.bin`, `DISC.bin`, `RUN.bin`,
`RUN2.bin`, `README.txt` and `FORMAT.txt`. Then `verify` of the copy must
exit 1, report the disc bad or not readable, and name a damaged file.
`restore` from the copy must not write wrong data: each file that it
completes equals the source, and it exits 1 when a file is not restored.
`restore` from the second copy into the same destination then completes the
tree and exits 0 ("Restore", "A damaged copy"). The seed changes the file and
the offset of each flip. The bytes of the image change from run to run, with
the times and the uuids, thus a seed hits the same place, not the same byte.

**Cross-runner restore.** Job one builds a small set of images of several
discs. It uploads the images as an artifact, with the checksums of the source
tree. Job two runs on a clean runner. It builds only the tool, downloads the
images, and has no repository and no source tree. It runs `verify` and
`recover` for each disc, then `restore`. It compares the result with the
checksums of the source tree, byte for byte, and checks that no file is
missing or extra.

**Hostile source tree.** The cell commits a source tree that holds odd file
names (spaces, a leading `-`, a newline, a tab, bytes that are not UTF-8, a
255-byte name, CJK and emoji names, a name in NFC and in NFD), a path of
about 3900 bytes, an empty directory and an empty file, ten thousand small
files, symbolic links (to a file, to a directory, dangling, absolute, with
`..`), a hard link, a read-only file and directory, setuid, setgid and sticky
bits, another owner, files at the chunk size limits and one byte below and
above, modification times before 1970 and after 2038, a FIFO, a socket, a
device node, and a file larger than one disc. It packs the tree across discs,
builds and verifies each image, deletes the repository, recovers each disc,
and restores. The restore must give the tree back, with the names, the
content, the symlink targets, the modes, the owners and the modification
times that "Metadata restore policy" applies. The FIFO, the socket and the
device node are not restored, and `restore` reports them as unsupported
entries. The cell runs as root. An unreadable file and a planted symlink
in the destination are unit tests.

**Guide walk.** Before the first tag, a person walks `docs/guide.md` in a
clean container, command by command, from the install to a restore. Each
command must work as the guide states it. This is a manual release gate, not
a CI step.

**Frozen format-1 test data.** The disc-root fixtures under
`cmd/noahsark/testdata/format1/` are the frozen test data of format major 1.
`make.sh` in that directory makes them again; it runs only before the first
tag. After the first tag, nobody makes them again. `TestFormat1Fixtures`
reads each fixture: `verify` with no repository, `recover` of each disc into
a new repository, a counted `verify`, and `restore` of each ref, compared
with the `expected-REF.txt` beside the fixture.
`TestFormat1FixturesUnchanged` compares each fixture file with the checked-in
`SHA256SUMS`, and fails on a changed, missing or extra file. Each later format
major adds its own set of fixtures, and keeps the sets of the earlier majors.

## 21. Manual physical checklist

These steps need a real drive and real media. Do them after a change to the
burn path. A physical burn is not a release gate.

1. `dvd+rw-mediainfo` on a blank disc shows the media type and the capacity.
2. A burn completes with the `growisofs` line that `status` printed.
3. Eject, load, mount read-only, and run `noahsark verify` on the mount
   point. `status` then shows the disc `verified`.
4. Burn a second copy of the same image, and check it with `verify
   --no-mark`.
5. A direct folder burn with the command of "Burning" completes, and `verify`
   of the disc succeeds.
6. `noahsark restore --disc=/mnt/ark SNAPSHOT DEST` from the mounted disc
   gives the source tree back.
7. The disc mounts on Windows and on macOS, and `README.txt` is readable. Do
   this for a UDF disc and for a folder-burn disc.

## 22. Burning-host command reference

```bash
# Check the tool versions: dvd+rw-tools 7.1-14 or newer, udftools 2.3 or newer.
growisofs -version 2>&1 | head -2
mkudffs 2>&1 | head -1
# Look at the blank disc.
dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Free Blocks|Track Size'
# Build the image, then burn it with the growisofs line that status prints.
sudo noahsark --repo=/srv/ark/repo image build <DISC>
# After the burn: load the disc again, mount it read-only, verify.
eject /dev/sr0 && eject -t /dev/sr0 && sleep 5
sudo mkdir -p /mnt/ark && sudo mount -o ro /dev/sr0 /mnt/ark
noahsark verify /mnt/ark; sudo umount /mnt/ark && eject /dev/sr0
# Copy a good disc to an image, to burn a new copy.
ddrescue -b 2048 -n -r1 /dev/sr0 copy.img copy.map
```
