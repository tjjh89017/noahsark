# Decisions

Each entry states one decision: what, and why. The entries are ordered by
topic. `FORMAT.md`, `OPERATIONS.md` and `docs/states.md` hold the rules; this
file holds the reasons that the code does not show. A decision about a deleted
feature is deleted too; the git history keeps it.

## Scope and release

**A simple tool for one person.** The flow is: commit, pack one run on one
disc, burn it, verify it, store it, restore. A feature must show that it is
worth its complexity against that flow. An operator must be able to walk the
whole flow with `docs/guide.md`.

**The guide is not the specification.** The guide must be clear for a human.
The specification must be complete. `OPERATIONS.md` and `docs/states.md` are
the specification of host-side behaviour, and `FORMAT.md` is the
specification of the on-disc bytes. A flag or a behaviour needs an entry in
the specification. It does not need a sentence in the guide. `docs/states.md`
is part of the specification because a test reads its state x event table row
by row.

**The operator layer redesign keeps the on-disc format.** Only the host side
changed: the commands, the repository layout, the state machines and the
host packages. The on-disc structures, `internal/format`, the format golden
files, `internal/chunker`, `internal/object` and `internal/fec` stay as they
are. FORMAT.md changes in wording only: chiefly the text about the
filesystem type, and the name of the host catalog.

**No tag yet; a breaking change is fine before the first release.** No tag
means no release, thus no disc in the field carries the old bytes. The project
deletes a thing; it does not annotate it as deprecated. No one makes a tag
until the owner says so.

**One run on one disc. No append.** An append brings the hard problems: the
spare area can run out, the directory blocks move, and the tool needs an image
mirror. A blank disc is cheap. The default burn still leaves the disc open, so
a later version can use the space.

**No scheduler and no daemon.** `commit` is a batch job. The operator or an
external scheduler runs it. The repository lock keeps two commits apart.

**Memory is bounded by the chunk size and the stripe size.** Peak memory never
follows the data size. `commit`, `pack`, `verify` and `restore` stream. The
`lowmem` e2e cell runs the 25 GB flow under a memory limit to prove it.

## Deferred work

Each item is recorded in a GitHub issue and is not part of the redesign.

- **More `ls` output formats (issue 68).** `ls` prints one fixed format now.
- **A volume label (issue 69).**
- **`restore` reads local data (issue 70).** A `restore` could read chunks
  from staging, or from the current source or destination files, so that a
  small change does not ask for a disc that is years old. The owner wants to
  design the two together.

## Redundancy and recovery

**The tool counts one verified disc. The operator owns the second copy.** One
good `verify` of a counted mount moves a disc to `verified`. `gc` frees the
staged chunks after one verified disc and 7 days. A disc has at most one burn
record and one verified record. The guide recommends a second copy in a
different building and shows how to make it. The tool records nothing about
it. Reasons: the state machine is simpler, and the tool cannot tell two copies
of one disc apart without more flags. A second copy needs no code, and it
survives the total loss of one disc, which parity on the same disc does not.

**A disc can be verified many times.** A good `verify` of a `verified` or an
`on disc only` disc changes no state. It adds one line to the verify log, and
`status` shows the date of the last good check. This is the periodic check
over the years. The tool never counts these lines as copies.

**FEC is optional, off by default, and set for each disc.** `pack --fec`
writes Reed-Solomon parity for this disc; without it, the disc has no FEC.
There is no config key and no `--no-fec`. The format scope is the disc: the
run header stores `fec_scheme`, `fec_k` and `fec_m`, and `verify` and heal
read the run header only. `pack` also copies the choice into the disc state
log, so that `status` shows `fec` without a read of the disc. FEC costs 9
percent of the capacity, and a small host pays about three times the pack
time. Measured on the CI runner with a 1.26 GB fixture: 1.94 s without FEC,
4.21 s with it. The capacity budget follows the choice.

**No recovery by carving.** A disc whose filesystem does not mount counts as
lost. The tool reads every file by name through the filesystem. A filesystem
can store a small file inside its own metadata, and the writer adds no
padding to prevent that. The second copy is the answer to a dead disc.

**The Reed-Solomon backend is `klauspost/reedsolomon` with
`WithCauchyMatrix()`.** The default matrix of the library is a Vandermonde
matrix and gives other parity bytes, thus the option must never change. A
cross-check against a pure Go implementation of FORMAT.md's arithmetic gave
byte-identical parity for `k = 231`, `m = 23`. The library ran at 792 MB/s
against 11.7 MB/s. The pure Go code was then deleted; `docs/fec-reference.md`
writes the arithmetic up by hand, and the worked example of FORMAT.md stays as
a test.

**The FEC package takes byte slices only.** `internal/fec` knows nothing of
INDEX or of the checksum column file. The caller in `internal/restore` picks
the erasure sets and calls `Decode` for each attempt. This fixes which package
runs the retry loop, and changes no byte.

**`verify --heal` always writes into `--out`.** It never repairs a disc root
in place. The damaged copy stays as evidence, and a failed heal loses nothing.
A healed directory is never a verified disc. The operator burns it with the
folder burn method, because `image build` takes only a disc of the
repository. The healed disc keeps the disc uuid of the damaged disc.

**Heal needs a readable `INDEX.bin`.** Heal finds the stream layout through
INDEX. An object row is found by position: the Objects table and the object
file rows share the ascending content id order. No hash of the current bytes
of a file takes part, thus a corrupt file cannot misplace a row.

**`recover` rebuilds a lost repository; `restore` never does.** `recover`
takes `--disc=DIR` for the mounted disc. After a lost computer the operator
runs `recover` first, then `restore`. Rebuilding is the job of `recover`
alone.

**`recover` keeps what it can read from a damaged disc.** It keeps the
metadata that it can read, and lists the damaged objects. It does not skip the
disc: the rest of the metadata is still history.

## Repository and catalog

**The catalog is permanent history, not a cache.** `<repo>/catalog/`
holds every snapshot, tree and blob object, and the INDEX, REFS and DISCS
tables of each disc. `commit` writes the snapshot, tree and blob objects
directly into it, in directories by kind. Nothing trims it, and `gc` never
touches it. Reasons: the history must stay for ever; the metadata is small;
the chunk data lives on the discs. The Go package is `internal/catalog`.

**The repository has a permanent part and a part that git ignores.** Tracked:
`config.yaml`; `state/` with the state log, the disc state log, the disc
ledger, the ref log, `refs.txt` and the catalog state; and `catalog/`.
Ignored: `lock`, and `staging/` with the chunks and the plans of each packed
disc. `init` writes a `.gitignore` that holds `/lock` and `/staging/`. The
tool never runs git. Reason: git tracks the permanent part, and all that `gc`
frees is in the ignored part.

**Only chunks are freed.** `staging/` holds the chunks between `commit` and
`gc`, and the disc root and image of each packed disc under
`staging/plans/<uuid>/`. `gc` frees these only.

**`ls` and `log` read the catalog only.** They take no disc argument. The
repository holds the full history, so a disc is never needed to list it.

**`disc lost` keeps the catalog data of the disc.** The history of a lost
disc is still history. A `restore` plan shows such a disc as `(lost)`.

**`verify --no-mark` writes nothing.** It writes no record, no verify log
line and no catalog entry. `--no-mark` means that the repository does not
change.

**`verify` with no repository checks and records nothing.** It checks every
byte of the disc and prints `not counted: no repository`.

## Burning and disc lifecycle

**The tool does not burn.** The operator runs the `growisofs` line that
`status` prints. The burn needs a device, often root, and a human who loads
the disc. A printed line is simple to read, to edit and to repeat for the
second copy.

**The default burn leaves the disc open.** `-dvd-compat` is never passed by
default. Only `pack --close` makes `status` print the sealed variant. The
repository stores that choice. A close is permanent, thus it must be an
explicit act.

**A good `verify` of a counted mount records the burn.** It adds the burn
record, when there is none, and the verified record. `verify --no-mark`
checks and records nothing. `disc burned` records a burn without a verify.
Reason: the ordinary flow burns and then verifies, and the operator never
needs to look up a disc number to record the burn.

**A counted mount is a read-only mount outside the repository.** Its path is
itself a mount point. The tool
does not check the source of a mount, so a loop mount of an image counts as a
real disc. Reasons: CI needs loop mounts, and the tool stays simple. The
operator is responsible for the use of a real disc. A verify of a disc root
that is not a mount, such as the packed tree, checks every byte and records
nothing: staging and the disc are not independent.

**A failed `verify` removes one record.** It removes the verified record when
one exists, else the burn record. The disc state goes down one step, and `gc`
holds the data. For an `on disc only` disc it removes nothing: no lower state
can hold data that `gc` already freed.

**`disc verified` records a verified disc on the word of the operator.** It
reads no disc. It has the same relation to `verify` that `disc burned` has to
a burn. The 7-day wait of `gc` still applies. `status` never prints it in a
`next:` block.

**Every undo exists and asks first.** `pack --undo`, `disc burned --undo`,
`verify --undo` and `disc lost --undo` each change one step back. Each undo
and `disc verified` show the change and ask `Continue? [y/N]`. The owner chose
one rule for every undo.

**`pack --undo` removes the record of the disc.** The disc number is skipped
and never used again. Reason: no later DISCS table names a disc that never
existed.

**`disc lost --undo` gives a found disc back one step lower.** A disc that was
`verified` comes back as `burned`: `gc` must not free data on the word of a
disc that nobody checked after it was found. It refuses a disc that was
`packed` or `burned`, because `disc lost` removed its disc root.

**The tool never runs `sudo` and never mounts a device.** The operator mounts
a disc and gives the mount point. A tool that raises its own privileges hides
what it does. The one exception is `image build`: it loop-mounts the image
file that it builds, and it needs root for that. When it is not root, it
prints the exact `sudo noahsark --repo=REPO image build SEQ` line and stops.

**A disc is keyed by its uuid. A sequence number is a label.** The host
assigns `run_seq` and `disc_seq` from local state. After a lost repository two
discs can carry the same number. The state logs, the catalog and the Prereqs
table all name the disc uuid. A disc argument accepts the disc number, the
full uuid or a uuid prefix. A value of 1 to 7 decimal digits is a disc number
and nothing else. A number with a typing error must never select a disc by
the start of its uuid, because a command such as `disc lost` would then act
on a wrong disc. The tool refuses an ambiguous value and lists the uuids.
A value that matches no disc lists nothing. The label match is deleted: a label is free text and can match two
discs.

**The disc ledger is a local `DISCS.bin`.** `<repo>/state/discs.bin` uses the
on-disc container unchanged. There is no read-back step that can recover the
earlier rows from a drive, thus the ledger is their source for the next
`pack`. The ledger row carries the real `run_hash`; the row that a disc
carries for itself keeps it zero, because the run is not final when the row is
written.

**The tool keeps no shelf notes.** The operator writes the label and the
storage place on the sleeve. A disc is good or is discarded.

## Image build and filesystem

**The filesystem type is free; UDF stays recommended.** FORMAT.md gives
minimum requirements in place of a type. No reader depends on the type: the
tool, `decoder.py` and a person read each file by name through the mounted
filesystem. A CI test burns the disc root as ISO 9660, and `verify`,
`restore` and `decoder.py` pass. The format is not frozen before the first
release, so the change costs no version bump.

**The recommended method is `image build`, then a burn of the image.** Pure
UDF 2.01 from `mkudffs` has the widest platform reach, one tree to verify, and
one tool path. The reading of an ISO 9660 level 4 disc on Windows and macOS is
not verified on real hosts.

**The folder burn is the second method.** The operator burns the disc root
directly with `growisofs`. It needs `-R` or `-iso-level 4`, and never `-J`,
`-udf` or `-M`: Joliet cuts the names, a UDF bridge carries two trees that
can disagree, and `-M` appends. The documents give a warning and the
recommended command, and the operator chooses. `status` prints only the
recommended method in its `next:` block, and one line after the block that
points to the guide section about the folder burn. The folder burn needs no
`mkudffs`.

**`image build` takes a disc number.** It uses the repository, as the other
disc commands do, and accepts the global `--repo`. Reasons: a disc belongs to
the repository, and the operator thinks in disc numbers. It only reads the
repository, because it runs with `sudo`. `image build --out` is deleted: the
image always goes to `staging/plans/<uuid>/tree.img`. An image that exists
makes it exit 1, because the state refuses the command; `--force` builds it
again.

**`pack --out=DIR` writes the disc root outside the repository.**
`staging/plans/<uuid>/tree` is then a symlink to `DIR`, and the image stays at
`staging/plans/<uuid>/tree.img`. `gc` and the undo commands remove the
symlink only. A directory of the operator is the operator's to keep or delete.

**`mkudffs` plus a loop mount, not a UDF writer in Go.** `mkudffs` makes an
empty volume only, and the Linux kernel fills it through a loop mount. This is
the shortest path to conforming UDF bytes with no new format code. The copy
always runs: an earlier design made it optional, and a developer machine then
built an empty image with no error.

**Never `--media-type=bdr` or `dvdr`.** Both make a write-once VAT volume. The
kernel mounts such a volume read-only, thus nothing can fill it.

**The image length comes from `DISC.bin`.** `image build` has no `--capacity`
option. One value serves the packer, the image and the FEC layout, thus the
three cannot disagree.

**The filesystem overhead is an estimate with a wide margin.** FORMAT.md gives
no filesystem overhead budget. A first estimate of one block for each file
failed on a real DVD+R image with "No space left on device". The estimate now
charges the space bitmap, 4 MiB, two blocks for each file, two blocks for each
directory, and 0.1 percent of the capacity. A test guards the margin. The
estimate changes no disc byte.

**image build checks in a fixed order.** The state check needs no tool. The
`mkudffs` check needs no root. So the cheapest and most useful refusal comes
first, and root comes last.

## Staging and gc

**gc frees a whole disc or nothing of it.** The `Freed` event moves a disc
to `on disc only`. When the INDEX of a disc in the catalog lacks some items,
a part free would leave a state that says the staged copy is gone while some
data stays only in staging. So `gc` skips the disc and reports it.

**The uuid in a path is hyphenated and lower case.** One form gives one
path for one disc, also on a filesystem that folds case.

**Staged bytes count stored file sizes.** The sum of the chunk files in
staging and the metadata files in the catalog is what the disk holds for the
Staged items. It is the number that the operator compares with the capacity.

**A date in `status` and `gc` is local.** The operator reads it against the
calendar of the host, as the default ref name does. `ls` and `log` print
times that a program parses, thus they use RFC 3339 in UTC.

**An item stores four states; the other item words are derived.** The state
log stores Staged, Packed, OnDisc and Lost. The words `burned` and `clean`
come from the state of the item's disc: a Packed item on a `burned` disc is
`burned`, and on a `verified` disc it is `clean`. Reason: a burn or a verify
changes one disc, thus it writes one disc event, not one record for each item.
OnDisc means that a disc holds the item and staging holds no file for it.
`gc` and `recover` both record it, because both say the same thing. Lost means
that the only disc of a freed item is gone.

**Two append-only logs with fixed-width records.** `state/state.db` holds one
70-byte record for each change of an item. `state/discstate.db` holds one
54-byte record for each event of a disc: pack, burn, check, verified mark,
free, lost, and recover. The newest item record of an id is its state. The
state of a disc is the replay of its events. The verify log is the check
events of the disc state log. `commit` writes a Staged record only for an
item that the log does not know, or that is Lost. Thus a second commit of
packed content cannot bring it back to Staged.

**The torn tail is cut one time, at open.** A partial or bad last record is a
crash during an append; a lock holder cuts it and warns. A bad record with
good records after it is damage; the tool stops and changes nothing, because a
silent drop of good records would hide a real fault. A read-only command never
truncates: it could see an append that is still in progress.

**`gc` needs one verified disc and 7 days.** The 7 days are fixed; `gc
--force-after` is the escape for one run. `gc` asks no confirmation: it frees
only the chunk data that a verified disc holds.

**`gc` confirms through the INDEX in the catalog before it frees.** An item
whose disc has no INDEX in the catalog is left alone and reported. `gc` never
deletes on trust.

**Every append is durable before the command goes on.** The tool syncs the
log after the append. `gc` syncs its OnDisc records and the `Freed` event
before it unlinks a chunk file, thus a crash cannot leave the bytes gone and
the record lost. A crash between the two leaves an orphan chunk file, and the
next `gc` frees it.

**Packed or OnDisc means "a disc holds it".** One test serves `pack`,
`commit` and `status`, whatever the state of the disc. An earlier `pack`
compared against one state only, and copied an object of a burned disc a
second time.

## Commit

**commit repairs a Staged chunk file.** A Staged item whose chunk file is
missing or has the wrong size cannot be packed. The source still holds the
data, so `commit` writes the chunk again. No other command can repair it.

**Every snapshot is a root snapshot, from one source root.** The build writes
no parent id and reads every file on every run. Dedup makes the second commit
cheap in space. The quick check and parent chains were cut: they add state
that can be wrong.

**The default ref is the date of today.** With no `--ref`, `commit` moves
`YYYY-MM-DD`. No ref name is reserved, and there is no `LATEST`. A reader
takes the record with the highest time.

**`commit -m` stays.** A ref moves to a later snapshot. A message stays with
its snapshot, so it records why that snapshot exists.

**An unstable file is stored and flagged.** `commit` stats, reads, and stats
again, with one retry. There is no parent entry to reuse, thus the content
that was read last is stored with the `UNSTABLE` flag, and the exit code is 1.
The detection has no switch.

**An unreadable or vanished file is skipped and reported.** Nothing was read,
thus there is nothing to flag. The snapshot is still written, and the exit
code is 1. Only an error on the source root or in the staging store stops the
commit.

**`commit` records the real uid and gid, and the names when the host has
them.** It caches one name lookup for each distinct id. A lookup that fails
records no name and is not an error.

**Only a chunk is compressed.** A blob, a tree and a snapshot are written with
compression 0. They are small, and raw storage is always a valid result of the
minimum-gain rule.

**A hole is ordinary zero data.** The writer never probes `SEEK_HOLE`. Equal
zero chunks deduplicate to one object.

**The object writer does not own the state log.** `commit` collects the
reachable objects after the write and records them as Staged. This costs one more
walk of the tree and keeps `internal/object` free of staging state.

## Pack

**pack --undo takes the highest disc number that is not undone.** The disc
ledger row and the `Packed` event give the same number for a live disc.
An undone disc is out of both, thus its hole never counts as the newest disc.

**pack --undo leaves the ref ledger.** The ledger is an append-only history
of refs that a disc carried. A ref that a later `pack` writes again is
harmless, and a rewrite of the ledger would add a failure point.

**`pack` takes the whole Staged pool.** It has no option that selects a
snapshot or a ref. Every run carries every pending ref and every snapshot
object, thus the newest disc names every ref of the repository.

**The selection is a prefix of a post-order walk.** A child comes before its
parent, thus every prefix is dependency-closed, and the Prereqs rows need no
search. `pack` grows the prefix while the capacity check passes and stops at
the first object that does not fit. Locality asks for "keep together", not for
a bin-packing search.

**`--capacity` is required at each `pack`, and refuses a bare number.** Each
blank disc can differ, so no config key stores a capacity. A bare number once
meant sectors and reads as bytes, a factor of 2048 apart. A preset gives the
real sector count of the medium, because a marketing size is not the
capacity. `pack` reads no drive: the operator reads the capacity with
`dvd+rw-mediainfo`. A capacity that holds not one item is a usage error, exit
2, because the value of an option is wrong.

**`pack` checks each staged object where the read is free.** A tree, a blob
and a snapshot are checked in the selection walk, which decodes them anyway. A
chunk is checked while it is copied into the disc root. A failure removes the
part-written disc root and records nothing.

**`pack` syncs in one pass, then records.** One pass at the end syncs every
file and directory. Only then does `pack` record the items as Packed, write
the catalog tables, and save the ledger. A
512 MiB pack with FEC took 13.5 s that way, against 22.1 s with a sync for
each file.

**`FORMAT.txt` is a byte copy of `FORMAT.md`.** It is checked in as
`internal/image/format.txt`, and a test compares the two. `README.txt` comes
from a checked-in template. The `{label}` slot receives the label text without
the zero padding of the 64-byte field.

**Files that are final only after INDEX carry a zero `file_hash`.**
`INDEX.bin`, `RUN.bin`, `RUN2.bin`, the checksum file and the parity files
cannot hold a hash of themselves in INDEX. The run header and their own CRCs
protect them.

## Restore

**The restore plan keeps counts, not chunk lists.** Peak memory must follow
the chunk size and never the snapshot size. Each disc pass reads the INDEX of
that disc from the catalog and takes the chunks that belong to it.

**Every restore problem line has one prefix.** A script finds the lines of
one command with one pattern.

**`restore` has one mode: one mounted disc at a time.** It plans from the
catalog, reads one mounted disc at a time, and asks for the next disc. It
always needs a repository. The mode that read several mounted discs at once
is deleted. Reasons: rebuilding is the job of `recover` alone, and one mode
makes `--dry-run` and resume work in every case.

**One write path, through a part file.** `restore` writes
`.<name>.noahsark-part` and gives the file its final name with `link` after
the last chunk is verified. `link` fails when the name exists, thus the
no-overwrite rule has no race, and a part-written file never carries the final
name.

**No spool and no plan file.** For each disc, `restore` walks the tree one
time and writes each chunk of that disc at its own offset. It holds one small
record for each file that is not complete. Each byte is copied one time.

**Resume is a check, not a journal.** At each open of a part file, the chunks
that are already there are checked against their content ids. A file that
already exists and matches its tree entry counts as resumed. A second run asks
only for the discs that it still needs.

**`restore` never unmounts and never ejects.** It names the disc that it
needs and waits. The operator swaps the disc in a second terminal. `--disc`
has no config default.

**Paths are relative to the source root.** `ls` prints `photos/`, not
`srv/data/photos/`, and `restore` takes the same text. A snapshot with more
than one source root keeps the root level. `log` shows the source path of each
snapshot. Reason: a path and a `restore` with no path then use one root.

**Owner first, then mode, then times.** A `chown` clears the setuid and the
setgid bits, and the `chmod` after it puts them back. Go holds setuid, setgid
and sticky outside the low 12 bits of `os.FileMode`, thus `restore` translates
the three bits.

**`restore` applies the uid and the gid, never the names.** A name can point
at a different id on the restoring host. A `restore` that is not root applies
no owner and prints no warning: an ordinary user who restores their own files
is the normal case.

**A symlink gets its target and, as root, its owner.** A `chmod` or a time
call would follow the link, and the standard library has no no-follow time
call.

**`restore` never removes a directory tree.** `--overwrite` unlinks a file or
a symlink. A directory that holds entries where a file must go is reported.

**A reader accepts a case-folded fixed name.** Some burners fold names to
lower case, for example plain ISO 9660 level 4. The reader tries the exact
name first, then a match without case in the listing of that directory.
Object names are lower-case hex already. The writer still writes the exact
case. `reference/decoder.py` follows the same rule.

## CLI and config

**Exit codes are 0, 1 and 2.** 0 is success, 1 is a failure at run time or a
refused state, 2 is a usage error. No command keeps a special code. A partial
success that needs the operator, such as an unstable file, is 1. An outcome
that needs no action, such as `gc` with nothing eligible, is 0.

**Global options come before the command, command options after it.** The
syntax is `noahsark [GLOBAL-OPTIONS] COMMAND [SUBCOMMAND...]
[COMMAND-OPTIONS] [ARGUMENTS]`. An option is global only when it has the same
meaning for every command that accepts it. `-h` works in both positions,
because an operator types `COMMAND -h` by habit. Go's `flag` package stops at
the first positional argument, thus an option after it is refused by name.

**Nested commands, no hyphen.** The groups are `disc` and `image`. A group has
no option of its own, other than `-h`.

**Confirmations have two levels, and answer flags.** `--yes` answers an
ordinary confirmation. `--force-yes` answers a critical confirmation, and
includes the effect of `--yes`. The critical confirmations are `disc
verified` and `disc lost`; all other confirmations are ordinary. With no
terminal and no answer flag, the answer is no, and the command exits 1. The
answers `y` and `yes` continue in any letter case, because the question shows
`[y/N]` and an operator who types `Y` means yes. There
is no `--no` flag. Reasons: every command must be usable from a script, and a
critical confirmation protects data that `gc` can free.

**The kept flags.** `-q` / `--quiet`, `--version`, `commit --ref`, `commit -m`,
`commit --one-file-system`, and the `--dry-run` of `pack`, `restore` and `gc`
stay. `verify --no-mark` and the `--undo` flags keep their names.

**`ls` prints the full information, in a fixed format.** One entry on each
line. The fields are separated by a tab, in this order: mode, type, size in
bytes, time as RFC 3339 UTC, path. A special character in a path is escaped.
There is no `--long`. Reasons: the output is easy to parse, and the values are
exact.

**The config is YAML, in `config.yaml`.** A Go struct holds the keys, with
struct tags for `go.yaml.in/yaml/v3`. Reasons: a new field is a new struct
field, and the library is a standard choice. Version 4 of the library has
only release candidates. Version 3 is stable, and it has the same
maintainers. The loader refuses an unknown
key by name. `pack.device` stays, with the default `/dev/sr0`; `init` prints
it on its `device:` line. There is no `init --device`: the operator edits
`config.yaml` to change the device.

**The repository is found as git finds one.** First `--repo`, then
`NOAHSARK_REPO`, then the current directory and each parent of it. The guide
shows no `export NOAHSARK_REPO=...` line: the operator runs the tool in the
repository directory.

**`status` prints a state word and one `next:` block.** A counter answers a
question that the operator did not ask. The `next:` block answers the one
that they did. `status` is the one place that prints a burn line; `pack` prints
`next: noahsark status`, and `image build` prints no `next:` line. The lines of a block are joined
with `&&`, so a failed line stops the lines after it.

**One source gives the full next step, and that source is `status`.** A
command that changes state ends with the one line `next: noahsark status`. It
does not print its own block. Two sources would drift apart: the block of a
command would say one thing, and `status` would say another after a later
change. A command that changed nothing prints no `next:` line, because a
refusal or a no answer leaves the step the same. `restore`, `ls`, `log`,
`image build` and `--dry-run` runs are not steps of the cycle. `verify
--no-mark` and a `verify` that is not counted change no state, thus they
print none. The `totals:` line of the restore plan has a fixed plural form,
`totals: D discs, N items, B bytes`, also for 1, because a program parses it.

## Recover and the catalog

**recover computes `partial` again on each call.** The operator gives the
discs in any order. A snapshot becomes `complete` when the discs together
hold every object, and the file must show that. `ls`, `log` and `restore`
of a `partial` snapshot exit 1 and name `recover`, so that the operator
learns why a listing is short.

**recover of a known disc writes no event.** The state of that disc is
already right. The call only checks the objects and adds the catalog entries,
and it reports damage in the same lines as a first `recover`.

**A `Lost` item that a disc holds becomes OnDisc.** The disc is the truth
for what it holds ("The trust rule"), and the log's own record of the loss
is the older word.

**Ids in files and in the damaged lines are full.** The 12-character form
can match two ids in a large catalog, and a file must not hold an ambiguous
key.

## State log

**A change writes one event, with two exceptions.** A good `verify` of a
`packed` disc records the burn, then the check, so that replay never skips a
state. `recover` of a damaged disc records the read, then the failed check.
A batch of records gets one sync before the next dependent step: the next
step needs the records durable, not each record on its own.

**A failed verify of a root that is not counted removes no record.** The
tool cannot tell that root from the disc, so it cannot lower the disc.

## Locking

**One non-blocking exclusive `flock` on `<repo>/lock`.** A command that writes
local state takes it and fails at once when it is held. It never waits. The
lock file is never removed, thus every `open` locks the same inode.

**A read-only command takes no lock.** `ls`, `log`, `status`, `restore`,
`pack --dry-run`, `gc --dry-run`, `verify --no-mark` and `verify` with no
repository take none. `status` opens the state log with the read-only
replay, which never truncates the file.

## Testing

**Test image-first.** Every burn test uses a real filesystem image and a real
loop mount. A mount that the tool reads (`verify`, `restore`, `recover`) is
read-only. Most use a `mkudffs` image. One test builds the disc
root as an ISO 9660 image, as the folder burn does, and checks that `verify`,
`restore` and `decoder.py` pass. Physical burns are a manual checklist, not
CI.

**CI rebuilds the catalog from the discs.** A test deletes the repository,
runs `recover` on the disc images alone, and then restores. This proves that
the discs answer every question.

**The heal test corrupts the real image.** It loop-mounts the image
read-write and writes bad bytes into the files at the exact stripe position.
The write lands on the sectors of the image file, thus the test does not
depend on an unpacked tree. A read-write mount is only for the damage: the
test unmounts the image and mounts it again read-only before the tool reads
it.

**A probe records an unknown answer; a test asserts a known one.** A probe
moves into the test list when its answer is stable.
