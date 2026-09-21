# Decisions

Each entry states one decision: what, and why. The entries are ordered by
topic. `FORMAT.md` and `OPERATIONS.md` hold the rules; this file holds the
reasons that the code does not show. A decision about a deleted feature is
deleted too; the git history keeps it.

## Scope and release

**A simple tool for one person.** The flow is: commit, pack one run on one
disc, burn two identical copies, verify, store, restore. A feature must show
that it is worth its complexity against that flow. An operator must be able to
walk the whole flow with `docs/guide.md`.

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

## Redundancy and recovery

**Two identical discs are the redundancy.** The operator burns each image two
times and stores the copies in different places. A second copy is simple, it
needs no code, and it survives the total loss of one disc, which parity on the
same disc does not.

**FEC is optional and off by default.** `fec.scheme` is `none`; `pack --fec`
writes Reed-Solomon parity for one run. FEC costs 9 percent of the capacity,
and a small host pays about three times the pack time. Measured on the CI
runner with a 1.26 GB fixture: 1.94 s without FEC, 4.21 s with it. There is no
`--no-fec`: the config key is the one switch. The capacity budget follows the
switch.

**No recovery by carving.** A disc whose filesystem does not mount counts as
dead. The tool reads every file by name through the filesystem. UDF can embed
a small file inside its File Entry, and the writer adds no padding to prevent
that. The second copy is the answer to a dead disc.

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

**Heal needs a readable `INDEX.bin`.** Heal finds the stream layout through
INDEX. An object row is found by position: the Objects table and the object
file rows share the ascending content id order. No hash of the current bytes
of a file takes part, thus a corrupt file cannot misplace a row.

## Burning and disc lifecycle

**The tool does not burn.** The operator runs the `growisofs` line that `pack`
prints. The burn needs a device, often root, and a human who loads the disc.
A printed line is simple to read, to edit and to repeat for the second copy.

**The default burn leaves the disc open.** `-dvd-compat` is never passed by
default. Only `pack --close` prints the sealed variant, and it changes the
printed command only. A close is permanent, thus it must be an explicit act.

**Only `disc burned` marks a burn. Only `verify` makes CLEAN.** The tool never
concludes by itself that a burn occurred. The guide tells the operator to
loop-mount and verify the image before the burn. The uuid of that image is
already in the ledger, because `pack` writes the ledger. A `verify` that took
a ledger match as the proof of a burn would mark that image CLEAN, and `gc`
would later delete staged data for a disc that no one burned.

**The tool never runs `sudo` and never mounts a device.** The operator mounts
a disc and gives the mount point. A tool that raises its own privileges hides
what it does. The one exception is `image build`: it loop-mounts the image
file that it builds, and it needs root for that. When it is not root, it
prints the exact `sudo noahsark image build ...` line and stops.

**A disc is keyed by its uuid. A sequence number is a label.** The host
assigns `run_seq` and `disc_seq` from local state. After a lost repository two
discs can carry the same number. The state log, the cache and the Prereqs
table all name the disc uuid. The `DISC` argument accepts the number, the
uuid, a uuid prefix or the label, and refuses a value that matches two discs.

**The disc ledger is a local `DISCS.bin`.** `<staging>/discs.bin` uses the
on-disc container unchanged. There is no read-back step that can recover the
earlier rows from a drive, thus the ledger is their source for the next
`pack`. The ledger row carries the real `run_hash`; the row that a disc
carries for itself keeps it zero, because the run is not final when the row is
written.

**The tool keeps no shelf notes.** The operator writes the label and the
storage place on the sleeve. A disc is good or is discarded.

## Image build

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
no UDF overhead budget. A first estimate of one block for each file failed on
a real DVD+R image with "No space left on device". The estimate now charges
the space bitmap, 4 MiB, two blocks for each file, two blocks for each
directory, and 0.1 percent of the capacity. A test guards the margin. The
estimate changes no disc byte.

## Staging and gc

**Five states, and ON-DISC is the last one.** ON-DISC means that a disc holds
the object and staging holds no file for it. `gc` and `recover` both record
it, because both say the same thing. A separate DELETED state would say no
more.

**One fixed-width state record, one file.** The record is 79 bytes and the log
is append-only. The newest record of an id is its state. The record carries
the verify count and the time of the first verify, thus there is no companion
file. An existing record is never replaced by STAGED, thus a second commit of
packed content cannot bring it back to STAGED.

**The torn tail is cut one time, at open.** A partial or bad last record is a
crash during an append; a lock holder cuts it and warns. A bad record with
good records after it is damage; the tool stops and changes nothing, because a
silent drop of good records would hide a real fault. A read-only command never
truncates: it could see an append that is still in progress.

**`gc` needs 2 verified copies and 7 days.** Two identical discs are the
redundancy, thus `gc` holds the staged data until the second copy passes
`verify`. `gc.min_verified_copies` is the key, for an operator who keeps one
copy only. The 7 days are fixed; `gc --force-after` is the escape, and it asks
for a confirmation. There is no flag that skips the confirmation: a script
pipes `y` in, which is one explicit act.

**`gc` confirms through the cached INDEX before it frees.** An object whose
disc is not cached is left alone and reported. `gc` never deletes on trust.

**Only the ON-DISC append syncs.** That record reaches the disk before the
unlink, thus a crash cannot leave the bytes gone and the record lost. `commit`
writes one record for each object, and a sync for each would set its pace. A
lost tail there replays as STAGED, and the next `pack` handles it.

**The cache lives in `<repo>/cache/` and is never trimmed.** It is an
accelerator, it is small, and `recover` builds it again. There is no key and
no flag for its place or its size. CI deletes it and restores from the disc
images alone.

**PACKED or later means "a disc holds it".** One test, `State.OnDisc()`,
serves `pack`, `commit` and `status`. An earlier `pack` compared against
PACKED only, and copied a BURNED object a second time.

## Commit

**Every snapshot is a root snapshot, from one source root.** The build writes
no parent id and reads every file on every run. Dedup makes the second commit
cheap in space. The quick check and parent chains were cut: they add state
that can be wrong.

**The default ref is the date of today.** With no `--ref`, `commit` moves
`YYYY-MM-DD`. No ref name is reserved, and there is no `LATEST`. A reader
takes the record with the highest time.

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
reachable objects after the write and marks them STAGED. This costs one more
walk of the tree and keeps `internal/object` free of staging state.

## Pack

**`pack` takes the whole STAGED pool.** It has no option that selects a
snapshot or a ref. Every run carries every pending ref and every snapshot
object, thus the newest disc names every ref of the repository.

**The selection is a prefix of a post-order walk.** A child comes before its
parent, thus every prefix is dependency-closed, and the Prereqs rows need no
search. `pack` grows the prefix while the capacity check passes and stops at
the first object that does not fit. Locality asks for "keep together", not for
a bin-packing search.

**`--capacity` refuses a bare number.** A bare number once meant sectors and
reads as bytes, a factor of 2048 apart. A preset gives the real sector count
of the medium, because a marketing size is not the capacity. `pack` reads no
drive.

**`pack` checks each staged object where the read is free.** A tree, a blob
and a snapshot are checked in the selection walk, which decodes them anyway. A
chunk is checked while it is copied into the disc root. A failure removes the
part-written disc root and records nothing.

**`pack` syncs in one pass, then records.** One pass at the end syncs every
file and directory. Only then does `pack` mark PACKED and save the ledgers. A
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

**One write path, through a part file.** Both modes write
`.<name>.noahsark-part` and give the file its final name with `link` after the
last chunk is verified. `link` fails when the name exists, thus the
no-overwrite rule has no race, and a part-written file never carries the final
name.

**The disc-swap mode has no spool and no plan file.** For each disc, `restore`
walks the tree one time and writes each chunk of that disc at its own offset.
It holds one small record for each file that is not complete. Each byte is
copied one time.

**Resume is a check, not a journal.** At each open of a part file, the chunks
that are already there are checked against their content ids. A file that
already exists and matches its tree entry counts as resumed. A second run asks
only for the discs that it still needs.

**`restore` never unmounts and never ejects.** It names the disc that it
needs and waits. The operator swaps the disc in a second terminal. `--mount`
has no config default.

**The all-discs mode needs no repository.** It reads the REFS and the INDEX of
the given discs. It names every missing disc and every file that it cannot
restore in one report at the end, and does not stop at the first one.

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

**Exit codes are 0, 1 and 2.** 0 is success, 1 is a failure at run time, 2 is
a usage error. No command keeps a special code. A partial success that needs
the operator, such as an unstable file, is 1. An outcome that needs no action,
such as `gc` with nothing eligible, is 0.

**Options come before the positional arguments.** Go's `flag` package stops at
the first positional argument. An option after it would be read as a path,
thus the build refuses it by name.

**The config is a flat `key = value` file with six keys.** The standard
library parses it. The loader refuses an unknown key by name. A key exists
only when an operator needs it: `staging.dir` stays because staging holds tens
of gigabytes and can need another disk.

**A `DISC-ROOT` is an argument that is an existing directory.** `ls`, `log`
and `restore` tell a disc root from a snapshot id or a ref name this way, and
need no separate option for the common case.

**`status` prints a state word and one `next:` line.** A counter answers a
question that the operator did not ask. The `next:` line answers the one that
they did.

**`pack` prints the next steps.** The block holds the exact `image build`,
`growisofs`, `disc burned` and `verify` lines for the disc, in order, so that
the ordinary flow cannot skip `disc burned`.

## Locking

**One non-blocking exclusive `flock` on `<repo>/lock`.** A command that writes
local state takes it and fails at once when it is held. It never waits. The
lock file is never removed, thus every `open` locks the same inode.

**A read-only command takes no lock.** `ls`, `log`, `status`, `pack --dry-run`
and every mode of `restore` take none. `status` opens the state log with the
read-only replay, which never truncates the file.

## Testing

**Test image-first.** Every burn test uses a real `mkudffs` image and a real
loop mount. Physical burns are a manual checklist, not CI.

**The heal test corrupts the real image.** It loop-mounts the image
read-write and writes bad bytes into the files at the exact stripe position.
The write lands on the sectors of the image file, thus the test does not
depend on an unpacked tree.

**A probe records an unknown answer; a test asserts a known one.** A probe
moves into the test list when its answer is stable.
