# States

This document is part of the specification of host-side behaviour,
together with `OPERATIONS.md`. `OPERATIONS.md`, "Staging state machine",
points here. This document defines the two state machines behind
`status`: the state of one item, and the state of one disc. It gives the
trust rule, a full state x event table, and the `next:` block that
`status` prints for each state.

A test reads the state x event table ("State x event table") row by
row. Each row is one test case. The test fails when a row has no case.
Keep the table format: one row on each line, seven cells, the row
number in the first cell. A row number is stable. A new row takes the
next free number at the end of the table, or a letter suffix after the
row that it extends (`11a`).

A counted mount is a read-only mount, outside the repository, whose path
is itself a mount point. The tool does not check the source of a mount,
so a loop mount of an image counts as a real disc. CI uses read-only
loop mounts (row 32). The operator is responsible for the use of a real
disc.

## 1. Disc records

The tool counts one verified disc. The operator owns the second copy.
The tool records nothing about a second copy.

The repository keeps these records for each disc, as events in the disc
state log, `state/discstate.db` (OPERATIONS.md, "Disc state log
record"):

- a burn record: none or one;
- a verified record: none or one;
- a verify log: one event for each `verify` of a counted mount without
  `--no-mark`, and one event for each `disc verified`, with the time,
  the disc uuid, and the result. The result of a `disc verified` event
  is `not checked`.

The verify log is history. The tool never counts its events as copies.
`status` reads it to show the time of the last check.

- `disc burned SEQ` adds the burn record.
- A good `verify` of a counted mount adds the verified record. When the
  disc has no burn record, it adds the burn record first. When the disc
  is already `verified` or `on disc only`, it changes no state. It only
  adds an event to the verify log.
- `disc burned --undo SEQ` removes the burn record of a `burned` disc.
- `disc verified SEQ` adds the verified record of a `burned` disc on the
  word of the operator. It reads no disc. It adds a verify log event
  with the result `not checked`.
- A failed `verify` of a counted mount removes one record: the verified
  record when one exists, else the burn record. It also adds an event to
  the verify log. For a `packed` or an `on disc only` disc, it removes
  nothing. See rows 40 and 43.
- `verify --no-mark` checks a disc and writes nothing: no record, no
  verify log event, and no catalog entry.
- `verify --undo SEQ` removes the verified record of a `verified` disc,
  whatever made it: `verify` or `disc verified`. It keeps the burn
  record and the verify log. It refuses an `on disc only` disc, because
  `gc` already freed the staged copy.
- `disc lost SEQ` adds a lost mark. `disc lost --undo SEQ` removes the
  lost mark. For a disc that was `verified` when `disc lost` ran, it
  also removes the verified record, and keeps the burn record: the disc
  is `burned` until a good `verify`. It refuses a disc that had no
  verified record when `disc lost` ran, because `disc lost` removed its
  disc root.
- `pack --undo SEQ` removes the disc. `status` no longer shows it, and
  no disc argument matches it. Its disc number is never used again.

### 1.1 Replay of the disc state log

The state of a disc is the result of its events, in log order. The
state before the first event is `unknown`. This table gives the effect
of each event. An event that the table does not permit for a state is
damage: the tool reports it as a bad record.

A change of a disc state writes one event. There are two exceptions: a good
counted `verify` of a `packed` disc writes `BurnRecorded`, then `CheckOK`,
and `recover` of a damaged disc writes `Recovered`, then `CheckFailed`.
`recover` of a disc that the repository knows, and that is not `missing`,
writes no event.

| Event | Written by | Effect |
|---|---|---|
| `Packed` | `pack` | `unknown` -> `packed`. It carries the disc number, the run number, and the `close` and `fec` flags. |
| `PackUndone` | `pack --undo` | `packed` -> `undone`. An `undone` disc is out of the repository. |
| `BurnRecorded` | `disc burned`; a good counted `verify` of a `packed` disc, before its `CheckOK` | `packed` -> `burned`. |
| `BurnRemoved` | `disc burned --undo` | `burned` -> `packed`. |
| `CheckOK` | a good counted `verify` | `burned` -> `verified`. `verified` and `on disc only` do not change. |
| `CheckFailed` | a failed counted `verify`; `recover` of a damaged disc, after its `Recovered` | `verified` -> `burned`. `burned` -> `packed`. `packed` and `on disc only` do not change. |
| `MarkedVerified` | `disc verified` | `burned` -> `verified`. |
| `VerifyUndone` | `verify --undo` | `verified` -> `burned`. |
| `Freed` | `gc` | `verified` -> `on disc only`. |
| `Lost` | `disc lost` | `packed`, `burned`, `verified`, `on disc only`, or `missing` -> `lost`. The tool keeps the state before the event. |
| `LostUndone` | `disc lost --undo` | `lost` -> `burned` when the state before `Lost` was `verified`; -> `on disc only` or `missing` when it was that state. |
| `Recovered` | `recover` | `unknown` or `missing` -> `on disc only`. It carries the `close` and `fec` flags that the disc records. |
| `NamedMissing` | `recover` | `unknown` -> `missing`. |

The verified time of a disc is the time of the `CheckOK` or
`MarkedVerified` event that moved it to `verified`. The 7-day wait of
`gc` counts from it. The last check of a disc is its newest `CheckOK`,
`CheckFailed`, or `MarkedVerified` event.

### 1.2 Confirmations

Six commands ask before they change a record.

| Command | Kind |
|---|---|
| `pack --undo` | ordinary |
| `disc burned --undo` | ordinary |
| `verify --undo` | ordinary |
| `disc lost --undo` | ordinary |
| `disc verified` | critical |
| `disc lost` | critical |

The command prints a warning to standard error. The warning names the
disc (number, label, uuid), the state now, the state after, and the
effect in one sentence. Then the command asks `Continue? [y/N]` on
standard error, and reads one line from standard input.

- Only `y` or `yes` continues. Any other answer, an empty line, or an
  end of input changes nothing. The command prints `nothing changed` and
  exits 1.
- The global option `--yes` answers an ordinary confirmation. The
  global option `--force-yes` answers an ordinary and a critical
  confirmation. With an answer flag, the command prints the warning,
  does not ask, and continues.
- With no terminal on standard input and no answer flag that covers the
  confirmation, the answer is no. The command reads nothing, prints
  `nothing changed`, and exits 1. For a critical confirmation and
  `--yes`, the line is `nothing changed; COMMAND needs --force-yes`.
- A refused case prints the refusal and does not ask.

The answer-no rows exit 1. The operator asked for a change, and no
change happened. Thus a `&&` line after the command does not run.

## 2. Item states

An item is one object that `commit` wrote, tracked by its content id in
the state log, `state/state.db`. The state log stores four states:
`Staged`, `Packed`, `OnDisc`, and `Lost`. The tool derives the words
`burned` and `clean` from the state of the item's disc.

| Word (internal) | Stored state | Meaning |
|---|---|---|
| `staged` | `Staged` | Held only in the repository. No disc carries it yet. |
| `packed` | `Packed`, its disc `packed` | Chosen into a disc root by `pack`. Its disc has no record. |
| `burned` | `Packed`, its disc `burned` | Its disc has a burn record and no verified record. |
| `clean` | `Packed`, its disc `verified` | Its disc has a verified record. The staged copy is kept. |
| `on-disc` | `OnDisc` | `gc` freed the staged copy, or `recover` read the item from a disc. A disc alone holds its chunk data. |
| `lost` | `Lost` | `disc lost` marked the item's disc gone after `gc` freed the item. The next `commit` treats it as not known. |

`status` never prints these words to the operator. It prints a disc's
state (see "Disc states") and a staged total.

Transitions, with the `reason` of the new record in brackets:

```
commit -> staged [normal]
staged, pack -> packed [normal]
packed, pack --undo -> staged [pack undone]
packed, disc burned -> burned (no item record; the disc changes)
burned, disc burned --undo -> packed (no item record)
burned, disc verified -> clean (no item record; a `not checked` verify log event)
packed or burned, verify ok -> clean (no item record)
clean, verify ok -> clean (a verify log event only)
burned, verify fail -> packed (no item record)
clean, verify fail -> burned (no item record)
clean, verify --undo -> burned (no item record)
clean, gc (verified, 7 days) -> on-disc [normal]
on-disc, verify ok or fail -> on-disc (a verify log event only)
packed or burned or clean, disc lost -> staged [disc lost] (the staged file still exists)
on-disc, disc lost -> lost [disc lost]
lost, commit (the source still has the data) -> staged [normal] (new record, same content id)
staged, no later pack took it, disc lost --undo (the disc was verified) -> burned [lost undone]
lost, disc lost --undo (the disc was on disc only) -> on-disc [lost undone]
unknown, recover -> on-disc [normal]
lost, recover (a disc that holds the item) -> on-disc [normal]
```

An item that is `lost` and that the source no longer has stays `lost`,
until `recover` reads a disc that holds it. `recover` treats a `lost`
item as not known. Nothing deletes an item from the log. The log is a
history.

`disc lost --undo` gives an item back to the found disc in two cases
only. For a disc that was `verified`, a `staged` item that no later
pack took becomes `burned`, and `gc` holds it until a good `verify`. For
a disc that was `on disc only`, a `lost` item becomes `on-disc`. An item
that a later `commit` staged again stays `staged`, and the next `pack`
takes it. An item that a later `pack` took stays on its new disc. The
found disc also holds these items. The same data on two discs does no
harm.

## 3. Disc states

`status` shows one of these words for each disc:

| Word | Meaning |
|---|---|
| `packed` | `pack` wrote the disc root. No record. |
| `burned` | A burn record, no verified record. |
| `verified` | A verified record. The staged copy is still kept. |
| `on disc only` | `gc` freed the staged copy, or `recover` rebuilt the disc. |
| `lost` | `disc lost` ran. The disc is out of the backup for good. |
| `missing` | Named by another disc's tables during `recover`, not yet given. |

`status` adds the word `fec` to the line of each disc that has FEC. The
tool reads the `fec` flag of the `Packed` or `Recovered` event of the
disc. It does not read the config and it does not read the disc.

`DATE` in `status` and in `gc` is the local date, as `YYYY-MM-DD`.

After `verified` and `on disc only`, `status` adds the date of the last
good check from the verify log: `verified, last check 2026-09-27`. When
the newest verify log event of an `on disc only` disc is a failure, it
shows `on disc only, last check failed 2026-09-27` instead. A `burned`
or `packed` disc whose newest verify log event is a failure shows
`burned, last check failed DATE` or `packed, last check failed DATE`.
That disc is bad, and its `next:` block burns a new disc from the kept
disc root. A disc that `recover` rebuilt and that no `verify` read yet
shows no date. When the newest verify log event of a `verified` or an
`on disc only` disc is a `not checked` event from `disc verified`,
`status` shows `verified, not checked` or `on disc only, not checked`.
A later good `verify` replaces it with `last check DATE`.

`missing` exists only during `recover`. An item does not pass through
it. It changes to `on disc only` when the operator gives the disc to
`recover`. It changes to `lost` when the operator runs `disc lost`.

`disc lost --undo` moves a disc that was `verified` to `burned`, and
gives an `on disc only` or a `missing` disc back that state. It refuses
a disc that was `packed` or `burned`. Its disc root is gone, and its
items are staged again, so the next `pack` takes them.

## 4. The trust rule

- The state log, the disc state log, the disc ledger, and the ref files
  are the repository's own memory. The tool trusts them for anything
  that no disc confirms yet.
- A disc's own tables (`INDEX`, `DISCS`, `REFS`, all inside `NOAHSARK/`
  on that disc) are the truth for what that disc holds. The catalog
  keeps byte copies of them. `pack` writes the catalog tables of the
  disc that it packs. A counted `verify` and `recover` write them from a
  disc. `commit` writes the snapshot, tree, and blob objects. `pack
  --undo` removes the catalog tables of its disc. No other command
  writes the catalog. `disc lost` keeps the catalog data of the disc.
- The disc wins when the two disagree. An example: the log says that an
  item is `on-disc` on disc X, but the `INDEX` of disc X does not list
  it. `gc` and `verify` do not trust the log alone. When `gc` cannot
  confirm an item against the catalog `INDEX` of its disc, it keeps the
  item and reports it. It never frees an item on the log's word alone.
- `disc lost` is the one command that tells the tool to stop trusting a
  disc's tables. The disc itself is unreachable. After it, the log's own
  record of "lost" is the truth. A `commit` replaces that record with
  fresh staged data.
- Physical evidence only. A `verify` records a burn or a verified disc
  only when `DISC-ROOT` is a counted mount and a repository exists. A
  verify of a disc root that is not a counted mount (the packed tree
  under `staging/`, a `pack --out` DIR, a healed DIR, a read-write
  mount, or a directory that is not a mount point) checks every byte
  and fails on damage. But it never records a burn and never records a
  verified disc. Staging and the disc are not independent: the loss of
  the repository loses both at once.

## 5. State x event table

One row is one test case. Given `State` and `Event`, the tool reaches
`Result`, prints `Message`, exits with `Exit`, and prints `Next`. When
`Result` is `refused`, `Message` gives the reason and `Next` gives the
action to take instead.

Exit codes follow OPERATIONS.md, "Exit code registry": 0 for success, 1
for a failure at run time or a refused state, 2 for a usage error.

Events: `commit`, `pack`, `pack --undo`, `image build`, `disc burned`,
`disc burned --undo`, `disc verified`, `verify ok`, `verify fail`,
`verify --no-mark ok`, `verify ok (loop mount)`, `verify ok (a disc
root that is not a counted mount)`, `verify fail (packed tree)`,
`verify --undo`, `gc`, `disc lost`, `disc lost --undo`, `recover`,
`restore`, `status`, and the syntax events of rows 72 to 76 and 77 to
79. `verify` without a qualifier means a verify of a counted mount with
a repository. `verify (any)` means each `verify` event except `verify
--undo`, and it includes `verify --heal`. "Answer yes" is `y` or `yes` on a terminal, or an answer flag
that covers the confirmation. "Answer no" is any other answer, an empty
line, an end of input, or no terminal and no covering answer flag. See
"Confirmations". Rows 80 to 84 test the answer rules once for all
asking commands.

A message that reports a change names the disc as `disc SEQ "LABEL"`.
A refusal and a `next:` line can name it as `disc SEQ`. A message can
add the uuid after it, as `(UUID)`. Every message that counts data uses
`item(s)`, never `object(s)`. `SEQ` in a command line of the `Next`
column is the disc number, or the full uuid when the number matches more
than one disc.

`noahsark status` in the `Next` column prints the block that "State to
`next:` block" gives.

The `Next` column is what the operator does next. The tool prints it only
under this rule. `status` prints the full `next:` block, with the exact
command lines. A command that changes state ends its output with one
line, `next: noahsark status`, as the last line on standard output. These
commands are `init`, `commit`, `pack` (also `pack --undo`), `disc burned`,
`disc verified`, `disc lost` (also with `--undo`), `disc burned --undo`,
`verify` (also `verify --undo`), `gc` and `recover`. A command that
changed nothing because it was refused, or because the operator answered
no, prints no `next:` line. `restore`, `ls`, `log`, `image build`, and
every `--dry-run` run print none. `verify --no-mark` and a `verify` that
is not counted change no state, and print none. The `Message` column
lists that last line where a row prints it.

| # | State | Event | Result | Message | Exit | Next |
|---:|---|---|---|---|---:|---|
| 1 | (none) or staged | commit | staged | `staged: N items, B bytes`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 2 | a disc is `missing` | commit | refused | `disc SEQ "LABEL" is missing` | 1 | `noahsark status`: its block gives `recover` or `disc lost` |
| 3 | lost | commit, the source still has the data | staged (new record) | `staged: N items, B bytes` (includes the re-staged items), then the last line `next: noahsark status` | 0 | `noahsark status` |
| 4 | lost | commit, the source no longer has the data | lost (unchanged) | commit succeeds. No line names the item: `commit` reads the source, not the log, then the last line `next: noahsark status` | 0 | nothing. The data that only this disc held is gone. |
| 5 | staged | pack --capacity | packed | `packed disc SEQ "LABEL": N item(s), B bytes`, then `uuid: UUID`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 6 | staged | pack, no `--capacity` | refused, usage error | `pack needs --capacity` | 2 | `noahsark status`: its block gives the `pack` line |
| 7 | staged | pack, the capacity holds not one item | refused, usage error | `capacity ... holds not one item` | 2 | give a larger `--capacity` |
| 8 | nothing staged | pack | unchanged | `pack: nothing staged`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 9 | staged, another disc is `packed` | pack --capacity | packed, a new disc with the next number | as row 5, then the last line `next: noahsark status` | 0 | `noahsark status`: it handles the lowest number first |
| 10 | a disc is `missing` | pack | refused | `disc SEQ "LABEL" is missing` | 1 | `noahsark status` |
| 11 | packed, the newest disc | pack --undo SEQ, answer yes | undone. Its items return to staged. Its record in `discs.bin` and its catalog tables are removed. The number is not given to another disc. The next pack gets the next number. | `warning: disc SEQ "LABEL" (UUID): packed -> undone`, then `the items return to staged; the disc number SEQ is not used again`, then `Continue? [y/N]` (no question with an answer flag), then `disc SEQ "LABEL": pack undone, N item(s) returned to staged`. For a `pack --out=DIR` disc, also `disc root DIR kept; delete it yourself`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 11a | packed, the newest disc | pack --undo SEQ, answer no | unchanged | the warning and the question of row 11, then `nothing changed` | 1 | `noahsark status` |
| 12 | packed, a later disc exists | pack --undo SEQ | refused, no question | `disc SEQ is not the newest disc; pack cannot be undone` | 1 | burn it as it is, or `noahsark disc lost SEQ` to return its items to staged |
| 13 | burned | pack --undo SEQ | refused, no question | `disc SEQ has a burn record; pack cannot be undone` | 1 | `noahsark disc burned --undo SEQ` when no burn happened, then `pack --undo` |
| 14 | verified, on disc only, lost, or missing | pack --undo SEQ | refused, no question | `disc SEQ is no longer packed; pack cannot be undone` | 1 | nothing to undo |
| 15 | packed | image build SEQ | unchanged. It writes `staging/plans/UUID/tree.img`. | `built image FILE (N bytes)` | 0 | `noahsark status` |
| 16 | packed, burned, or verified, the `mkudffs` version is good, not root | image build SEQ | refused | `image build needs root for the loop mount; run: sudo noahsark --repo=REPO image build SEQ` | 1 | the printed line |
| 17 | packed, burned, or verified, the image file exists | image build SEQ | refused | `FILE exists; add --force to build it again` | 1 | add `--force` to build it again |
| 17a | packed, burned, or verified, the image file exists | image build --force SEQ | unchanged. The image is built again. | `built image FILE (N bytes)` | 0 | `noahsark status` |
| 18 | burned or verified | image build SEQ | unchanged | `built image FILE (N bytes)` | 0 | `noahsark status` |
| 19 | on disc only, lost, or missing; or packed, burned, or verified with the disc root missing, as root | image build SEQ | refused | `no disc root at DIR` | 1 | copy a good disc with `ddrescue`, as the guide's "A second copy" shows |
| 20 | packed | disc burned SEQ | burned | `disc SEQ "LABEL": burn recorded`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 21 | burned | disc burned SEQ | refused | `disc SEQ already has a burn record` | 1 | `noahsark status` |
| 22 | verified or on disc only | disc burned SEQ | refused | `disc SEQ is already verified` | 1 | nothing to record. A second copy is not recorded. |
| 23 | lost or missing | disc burned SEQ | refused | `disc SEQ is marked lost`, or `disc SEQ is missing` | 1 | `noahsark status` |
| 24 | burned | disc burned --undo SEQ, answer yes | packed | `warning: disc SEQ "LABEL" (UUID): burned -> packed`, then `the burn record is removed; burn the disc again from its disc root`, then `Continue? [y/N]` (no question with an answer flag), then `disc SEQ "LABEL": burn record removed`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 24a | burned | disc burned --undo SEQ, answer no | unchanged | the warning and the question of row 24, then `nothing changed` | 1 | `noahsark status` |
| 25 | packed | disc burned --undo SEQ | refused, no question | `disc SEQ has no burn record` | 1 | nothing to undo |
| 26 | verified, on disc only, lost, or missing | disc burned --undo SEQ | refused, no question | `disc SEQ is not burned` | 1 | nothing to undo. A verified record is physical evidence. |
| 27 | burned | disc verified SEQ, answer yes (a terminal, or `--force-yes`) | verified. The verified record is added. The tool reads no disc. A verify log event with the result `not checked` is added. | `warning: disc SEQ "LABEL" (UUID): burned -> verified`, then `the tool did not read this disc; gc frees the repository copy of its data after the wait time; if the disc is bad, that data is lost`, then `Continue? [y/N]` (no question with `--force-yes`), then `disc SEQ "LABEL": verified record added; not checked`, then the last line `next: noahsark status` | 0 | `noahsark status`. A later good `verify` of the disc acts as row 34 and replaces `not checked` with `last check DATE`. `verify --undo SEQ` removes the record (row 47). |
| 28 | burned | disc verified SEQ, answer no | unchanged | the warning and the question of row 27, then `nothing changed` | 1 | `noahsark status` |
| 29 | packed | disc verified SEQ | refused, no question | `disc SEQ has no burn record; run: noahsark disc burned SEQ, or verify the disc` | 1 | `noahsark disc burned SEQ`, or `noahsark status`: its block verifies the disc |
| 30 | verified, on disc only, lost, or missing | disc verified SEQ | refused, no question | `disc SEQ is already verified`, `disc SEQ is marked lost`, or `disc SEQ is missing` | 1 | nothing to record, or `noahsark status` |
| 31 | packed | verify ok | verified. The burn record and the verified record are added. | `disc SEQ "LABEL": N items, ok`, then `burn recorded; verified`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 32 | packed or burned | verify ok (loop mount) | verified, as rows 31 and 33. The tool does not check the source of the mount. | as rows 31 and 33; the last line is `next: noahsark status` | 0 | `noahsark status`. The operator burns a real disc before `gc`. |
| 33 | burned | verify ok | verified | `disc SEQ "LABEL": N items, ok`, then `verified`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 34 | verified | verify ok | unchanged, a verify log event added | `disc SEQ "LABEL": N items, ok`, then `already verified; check logged`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 35 | on disc only | verify ok | unchanged, a verify log event added | `disc SEQ "LABEL": N items, ok`, then `check logged`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 36 | packed | verify --no-mark ok | unchanged. Nothing is written. | `disc SEQ "LABEL": N items, ok`, then `not marked; to record this burn, run: noahsark disc burned SEQ` | 0 | that line, or `verify` without `--no-mark` |
| 37 | burned, verified, or on disc only | verify --no-mark ok | unchanged. Nothing is written. | `disc SEQ "LABEL": N items, ok`, then `not marked` | 0 | `noahsark status` |
| 38 | packed, burned, or verified | verify ok (a disc root that is not a counted mount: the packed tree, a `pack --out` DIR, a healed DIR, a read-write mount, or a directory that is not a mount point) | unchanged. Nothing is written. | `disc SEQ "LABEL": N items, ok`, then `not counted: this is not a disc` | 0 | `noahsark status` |
| 38a | (no repository) | verify ok | unchanged. Nothing is written. | `disc UUID "LABEL": N items, ok`, then `not counted: no repository` | 0 | nothing |
| 38b | packed, burned, verified, or on disc only | verify fail (a disc root that is not a counted mount: the packed tree, a `pack --out` DIR, a healed DIR, a read-write mount, or a directory that is not a mount point) | unchanged. No record is removed and no verify log event is added. | `disc SEQ "LABEL": bad; REASON`, then `not counted: this is not a disc` | 1 | discard this copy and check the disc itself, or see row 39 for a packed tree |
| 38c | (no repository) | verify fail | unchanged. Nothing is written. | `disc UUID "LABEL": bad; REASON`, then `not counted: no repository` | 1 | nothing |
| 39 | packed | verify fail (packed tree) | unchanged | as row 38b, with the reason `the packed tree is damaged` | 1 | the newest disc: `noahsark pack --undo SEQ`, then `noahsark status`. Another disc: `noahsark disc lost SEQ`, then `noahsark status`. |
| 40 | packed | verify fail | unchanged, a failed verify log event added | `disc SEQ "LABEL": bad; this disc is bad; no record to remove`, then the last line `next: noahsark status` | 1 | discard the disc, then `noahsark status`: its block burns a new disc from the kept disc root |
| 41 | burned | verify fail | packed. The burn record is removed. A failed verify log event is added. | `disc SEQ "LABEL": bad; this disc is bad; burn record removed`, then the last line `next: noahsark status` | 1 | discard the disc, then `noahsark status` |
| 42 | verified | verify fail | burned. The verified record is removed. `gc` holds the data. A failed verify log event is added. | `disc SEQ "LABEL": bad; this disc is bad; verified record removed; gc holds the data`, then the last line `next: noahsark status` | 1 | discard the disc, then `noahsark status`: its block burns a new disc from the kept disc root. When the disc root is gone, its block gives `noahsark disc lost SEQ`. |
| 43 | on disc only | verify fail | unchanged, a failed verify log event added | `disc SEQ "LABEL": bad; the staged copy is already freed; copy this disc now, or use your second copy, or run: noahsark disc lost SEQ`, then the last line `next: noahsark status` | 1 | the guide's "A second copy" |
| 44 | lost | verify (any) | refused | `disc SEQ is marked lost` | 1 | nothing to verify |
| 45 | missing | verify (any) | refused | `disc SEQ is missing; give it to recover` | 1 | `noahsark status`: its block gives the `recover` line |
| 46 | the disc uuid is not in the disc state log | verify (any) | refused | `disc UUID is not in this repository` | 1 | load a disc of this repository, then `noahsark status` |
| 47 | verified | verify --undo SEQ, answer yes | burned. The verified record is removed. The burn record and the verify log stay. | `warning: disc SEQ "LABEL" (UUID): verified -> burned`, then `the disc is no longer verified, and gc holds its data`, then `Continue? [y/N]` (no question with an answer flag), then `disc SEQ "LABEL": verified record removed; burn record kept`, then the last line `next: noahsark status` | 0 | `noahsark status`: its block verifies the disc again. When the disc is bad: `noahsark disc burned --undo SEQ`, then `noahsark status`. |
| 48 | verified | verify --undo SEQ, answer no | unchanged | the warning and the question of row 47, then `nothing changed` | 1 | `noahsark status` |
| 49 | on disc only | verify --undo SEQ | refused, no question | `disc SEQ is on disc only; gc already freed the staged copy; verify cannot be undone` | 1 | the guide's "A second copy". When no copy can be read: `noahsark disc lost SEQ && noahsark commit`. |
| 50 | packed or burned | verify --undo SEQ | refused, no question | `disc SEQ has no verified record` | 1 | nothing to undo. For a `burned` disc, `noahsark disc burned --undo SEQ` removes the burn record. |
| 51 | lost or missing | verify --undo SEQ | refused, no question | `disc SEQ is marked lost`, or `disc SEQ is missing` | 1 | `noahsark status` |
| 52 | verified, 7 days or more | gc | on disc only. The chunk files of the disc and its `staging/plans/UUID/` directory are removed. | `gc: freed N item(s), B bytes`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 53 | verified, less than 7 days | gc | unchanged | `gc: freed 0 item(s), 0 bytes`, then `gc: disc SEQ: too soon; N item(s) held until DATE`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 54 | verified, the catalog has no INDEX for the disc | gc | unchanged, the item skipped | `gc: freed 0 item(s), 0 bytes`, then `gc: N item(s) skipped: disc SEQ's table is not in the catalog`, then the last line `next: noahsark status` | 1 | `noahsark verify` of that disc, then `noahsark gc` |
| 54a | verified, 7 days or more, the catalog INDEX of the disc does not list some items of the disc | gc | unchanged. No item of the disc is freed and no `Freed` event is appended. | `gc: freed 0 item(s), 0 bytes`, then `gc: N item(s) skipped: disc SEQ's table does not list them`, then the last line `next: noahsark status` | 1 | `noahsark verify` of that disc, then `noahsark gc`. When the skip stays, the disc does not hold these items: `noahsark disc lost SEQ` returns them to staged. |
| 55 | packed, burned, or missing | gc | unchanged | `gc: freed 0 item(s), 0 bytes`, then `gc: disc SEQ: not verified; N item(s) held` (no disc line for a missing disc), then the last line `next: noahsark status` | 0 | `noahsark status` |
| 56 | lost | gc | unchanged | `gc: freed 0 item(s), 0 bytes` (no disc line for a lost disc), then the last line `next: noahsark status` | 0 | `noahsark status` |
| 57 | packed, burned, or verified | disc lost SEQ, answer yes (a terminal, or `--force-yes`) | lost. Its items return to staged. The directory `staging/plans/UUID/` is removed. The catalog data of the disc stays. | `warning: disc SEQ "LABEL" (UUID): STATE -> lost`, then `the tool stops trusting this disc`, then `Continue? [y/N]` (no question with `--force-yes`), then `disc SEQ "LABEL": marked lost; N item(s) returned to staged`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 58 | on disc only | disc lost SEQ, answer yes | lost. Its freed items are `lost`. | the warning and the question of row 57, then `disc SEQ "LABEL": marked lost; N item(s) need a new commit`, then the last line `next: noahsark status` | 0 | `noahsark commit` |
| 59 | missing | disc lost SEQ, answer yes | lost | the warning and the question of row 57, then `disc SEQ "LABEL": marked lost; its items are not known; a new commit stages what the source still holds`, then the last line `next: noahsark status` | 0 | `noahsark commit` |
| 59a | packed, burned, verified, on disc only, or missing | disc lost SEQ, answer no | unchanged | the warning and the question of row 57, then `nothing changed` | 1 | `noahsark status` |
| 60 | lost | disc lost SEQ | refused, no question | `disc SEQ is already marked lost` | 1 | nothing to do |
| 61 | lost, `verified` when marked lost | disc lost --undo SEQ, answer yes | burned. The lost mark and the verified record are removed. The burn record stays. Each item of the disc that is `staged` and that no later pack took is `burned`. An item that a later pack took stays on its new disc. `gc` frees nothing of this disc until a good `verify`. | `warning: disc SEQ "LABEL" (UUID): lost -> burned`, then `the tool trusts this disc again only after a good check; you must run verify on it`, then `Continue? [y/N]` (no question with an answer flag), then `disc SEQ "LABEL": lost mark removed; N item(s) back on this disc; verify it now`, then the last line `next: noahsark status` | 0 | `noahsark status`: its `burned` block has the verify lines. A good verify acts as row 33. A failed verify acts as row 41, and the block for a disc with no disc root follows. |
| 62 | lost, `on disc only` when marked lost | disc lost --undo SEQ, answer yes | on disc only. The lost mark is removed. Each `lost` item is `on-disc`. An item that a later `commit` staged again stays `staged`, and the next `pack` takes it. | the warning and the question of row 61, with `lost -> on disc only`, then `disc SEQ "LABEL": lost mark removed; N item(s) back on this disc; verify it now`, then the last line `next: noahsark status` | 0 | `noahsark status`: its block verifies the disc, a check that changes no state. A good verify acts as row 35. A failed verify acts as row 43. |
| 63 | lost, `missing` when marked lost | disc lost --undo SEQ, answer yes | missing. The lost mark is removed. No item changes: the tool never knew the items of this disc. | the warning and the question of row 61, with `lost -> missing`, then `disc SEQ "LABEL": lost mark removed; give it to recover`, then the last line `next: noahsark status` | 0 | `noahsark status`: its block gives the `recover` line. `recover` then acts as row 69. |
| 64 | lost, `verified`, `on disc only`, or `missing` when marked lost | disc lost --undo SEQ, answer no | unchanged | the warning and the question of row 61, 62, or 63, then `nothing changed` | 1 | `noahsark status` |
| 65 | lost, `packed` or `burned` when marked lost | disc lost --undo SEQ | refused, no question | `disc SEQ had no verified record when it was marked lost; its items are staged again; the lost mark stays` | 1 | discard the disc, then `noahsark status`: the next pack takes the items |
| 66 | packed, burned, verified, on disc only, or missing | disc lost --undo SEQ | refused, no question | `disc SEQ is not marked lost` | 1 | nothing to undo |
| 67 | (no repository) | `recover --source=SOURCE --disc=DIR`, and the disc names no disc that the repository does not know | on disc only. The repository is created. | `recover: ok`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 68 | (no repository) | `recover --source=SOURCE --disc=DIR`, and the disc names a disc that no call gave | on disc only for the given disc. missing for each disc that it names and that no call gave. | `recover: disc SEQ "LABEL" (UUID) named by another disc, not yet given`, then the last line `next: noahsark status` | 1 | `noahsark status` |
| 69 | missing | recover, that disc given | on disc only | `recover: ok`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 70 | an existing repository | recover, a disc that the repository does not know | on disc only. Known items keep their state. Unknown items become on-disc. | `recover: ok`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 70a | (no repository), or an existing repository and a disc that the repository does not know, or a `missing` disc | recover, a disc with damaged objects | on disc only, with a failed check. Each object that passes its check is recorded. A damaged object gets no record. | `recover: damaged: ID` for each damaged object, then `recover: N item(s) damaged on disc SEQ "LABEL"`, then the last line `next: noahsark status` | 1 | `noahsark status`: its block gives the `on disc only, last check failed` lines |
| 70b | an existing repository | recover, a disc of another repository | refused | `disc UUID belongs to repository RUUID, not to this repository` | 1 | give a disc of this repository |
| 70c | an existing repository | recover, a disc that the repository knows, and that is not `missing` | unchanged. The catalog entries of the disc are written. No event is written. | `recover: ok; disc SEQ "LABEL" already known`, then the last line `next: noahsark status` | 0 | `noahsark status` |
| 70d | an existing repository | recover, a disc that the repository knows, and that is not `missing`, with damaged objects | unchanged. The catalog entries of the objects that pass are written. No event is written. | `recover: damaged: ID` for each damaged object, then `recover: N item(s) damaged on disc SEQ "LABEL"`, then the last line `next: noahsark status` | 1 | copy the disc now, or use the second copy, then `noahsark recover` with the copy |
| 71 | any | status | unchanged | `staged: N items, B bytes`, one `disc SEQ "LABEL"  STATE  [fec  ]UUID` line for each disc, with `, last check DATE`, `, last check failed DATE`, or `, not checked` as "Disc states" defines, and `fec` when the disc has FEC, then the full `next:` block for the whole repository | 0 | the `next:` block |
| 71a | (no repository) | a command other than `init`, `recover`, `verify`, `restore`, `ls`, and `log` | refused, usage error | `no repository; run noahsark init, or give --repo` | 2 | `noahsark init` in the directory of the repository, or the same line with `--repo=PATH` |
| 72 | any | a global option after the command name, for example `noahsark status --repo=PATH` | refused, usage error | `--repo is a global option; give it before the command name: noahsark --repo=PATH status` | 2 | the same line with the option before the command name |
| 73 | any | a command option before the command name, or between a group and its subcommand, for example `noahsark --undo disc burned 0` or `noahsark disc --undo burned 0` | refused, usage error | `--undo is an option of disc burned; give it after the last subcommand word: noahsark disc burned --undo 0; see: noahsark disc burned -h` | 2 | the same line with the option after the last subcommand word. `-h` is the one exception to the position rule: `noahsark COMMAND -h` is not a usage error. |
| 74 | any | a group with no subcommand, for example `noahsark disc` | refused, usage error | `disc needs a subcommand:`, then one line for each subcommand: `burned`, `lost`, `verified` | 2 | the same line with a subcommand, or `noahsark disc -h` |
| 75 | any | an unknown subcommand, for example `noahsark disc burnt 0` | refused, usage error | `unknown subcommand of disc: burnt; the subcommands are:`, then one line for each subcommand: `burned`, `lost`, `verified` | 2 | the same line with a listed subcommand |
| 76 | any | `-h` on a group: `noahsark disc -h` or `noahsark -h disc` | unchanged | one line for each subcommand of the group, with what it does | 0 | `noahsark disc SUBCOMMAND -h` for the options of one subcommand |
| 77 | any | `noahsark --repo=PATH init` | refused, usage error | `init makes the current directory the repository; it does not take --repo` | 2 | `cd PATH && noahsark init` |
| 78 | any | a disc argument that matches no disc | refused, usage error | `no disc matches ARG` | 2 | `noahsark status` lists the discs |
| 79 | any | a disc argument that matches more than one disc | refused, usage error | `ARG matches more than one disc:`, then one `disc SEQ "LABEL"  UUID` line for each candidate | 2 | the same line with the full uuid |
| 80 | a state where an ordinary confirmation asks (rows 11, 24, 47, 61 to 63) | the command with `--yes` or `--force-yes` | as the answer-yes row | the warning, no question, then the message of the answer-yes row | 0 | as the answer-yes row |
| 81 | a state where an ordinary confirmation asks | the command, no terminal on standard input, no answer flag | unchanged. Standard input is not read. | the warning, then `nothing changed` | 1 | the same line with `--yes` before the command name |
| 82 | a state where a critical confirmation asks (rows 27, 57 to 59) | the command with `--force-yes` | as the answer-yes row | the warning, no question, then the message of the answer-yes row | 0 | as the answer-yes row |
| 83 | a state where a critical confirmation asks | the command with `--yes`, no terminal on standard input | unchanged. Standard input is not read. | the warning, then `nothing changed; disc verified needs --force-yes` or `nothing changed; disc lost needs --force-yes` | 1 | the same line with `--force-yes` before the command name |
| 84 | a state where a critical confirmation asks | the command with `--yes`, a terminal on standard input, answer yes typed | as the answer-yes row | the warning and the question, then the message of the answer-yes row | 0 | as the answer-yes row |
| 84a | a state where a critical confirmation asks | the command with `--yes`, a terminal on standard input, answer no typed | unchanged | the warning and the question, then `nothing changed` | 1 | `noahsark status` |
| 85 | the plan names a `lost` disc, and no other disc holds its chunks | restore | the files that need the lost disc are not restored. Every other file is restored. | the plan line `disc SEQ "LABEL" (UUID): N items, B bytes (lost)`, the problem lines, then `restored snapshot ID into DEST` | 1 | nothing. The data that only this disc held is gone. |
| 85a | a needed chunk has no known disc: no catalog INDEX lists it, for example after a `missing` disc is marked lost | restore | the files that need such a chunk are not restored. Every other file is restored. `restore` does not stop before it reads a disc. | the plan with the line `restore: N item(s) have no disc known to the catalog; run recover with more discs`, the problem lines, then `restored snapshot ID into DEST` | 1 | `noahsark recover` with each disc that the operator still holds, then the same `restore` again. When no other disc exists, the data is gone. |
| 86 | the plan names discs, the disc at `--disc=DIR` is the expected disc | restore | the chunks of this disc are written into `DEST`. When the plan names more discs, `restore` asks for the next one, as OPERATIONS.md, "Disc swap, one drive", states. | `disc SEQ "LABEL": found`, then, after the last disc, `restored snapshot ID into DEST` | 0 | `noahsark status` |
| 87 | the plan names discs, no terminal on standard input, the disc at `--disc=DIR` is not the expected disc | restore | the chunks of each disc read so far stay in their part files | `restore: insert disc SEQ "LABEL" (UUID) into DIR and run restore again` | 1 | mount that disc at `DIR`, then run the same `restore` again. It resumes. |
| 88 | any | restore --dry-run | unchanged | the plan: one `disc SEQ "LABEL" (UUID): N items, B bytes` line for each disc, with ` (lost)` after a lost disc, the `restore: N item(s) have no disc known to the catalog; ...` line when row 85a applies, then `totals: D discs, N items, B bytes` (a fixed plural form, also for 1, because a program parses it). No `next:` line. | 0 | the same line without `--dry-run` |
| 89 | (no repository) | restore, ls, or log | refused, usage error | `no repository; run recover first, one time for each disc` | 2 | `noahsark --repo=PATH recover --source=SOURCE --disc=DIR` |
| 89a | the snapshot is `partial` in `catalog-state.txt` | restore or ls | refused. No disc is read. | `snapshot ID is partial; run recover with more discs` | 1 | `noahsark recover` with each disc that the operator still holds, then the same command again |
| 89b | a snapshot is `partial` in `catalog-state.txt` | log | the lines are printed as usual | every line of `log`, then `snapshot ID is partial; run recover with more discs` on standard error for each such snapshot | 1 | `noahsark recover` with each disc that the operator still holds |

No cell is a dead end. The `Next` column of each `refused` row names an
action that moves the repository forward.

Rows 41 and 42 apply one rule: a failed verify removes one record, the
verified record first. Thus a failed verify lowers the state of the
disc, and `gc` then holds the data. Row 40 has no record to remove: the
state stays `packed`, and the failed check is logged. Row 43 is the one
exception: the staged copy is already freed, so no lower state can hold
it. The tool logs the failure, `status` shows it, and the operator acts
from a second copy or with `disc lost`.

Rows 31 and 33 differ only in whether `disc burned` ran first. Both end
in `verified`. Row 31 is the default path of the guide.

Rows 34 and 35 are the periodic check. The operator can verify a disc
any number of times, over the years. A good check changes no state.

`pack --undo` accepts only the newest disc (rows 11 and 12). A later
disc can name an earlier disc in its Prereqs table. FORMAT.md, "The
physical disc", says that a `disc_seq` is never given to another disc.
Thus an undone number stays a hole, and the next `pack` gets the next
number.

Rows 47 to 51 are `verify --undo`. It is for a verify by mistake, or
for a `verified` disc that the operator sees later is bad. It lowers the
disc one step, to `burned`, and `gc` then holds the data. It refuses an
`on disc only` disc (row 49): no lower state can hold data that `gc`
already freed.

Rows 61 to 66 are `disc lost --undo`. It is for a lost disc that the
operator finds again. A disc that was `verified` goes back to `burned`,
not to `verified` (row 61). `gc` must not free data on the word of a
disc that nobody checked after it was found. A good `verify` then
moves it to `verified`, as for any `burned` disc. A disc that was `on
disc only` goes back to `on disc only` (row 62): staging holds nothing
of it, so `gc` has nothing to free. A disc that was `missing` goes back
to `missing` (row 63). It refuses a disc that was `packed` or `burned`
(row 65). `disc lost` removed the disc root of that disc, and no burn
can be repeated without it. Its items are staged again, and the next
`pack` takes them.

Rows 27 to 30 are `disc verified`. It records a verified disc on the
word of the operator, as `disc burned` records a burn. It reads no disc.
The 7-day wait of `gc` still applies (row 52). `status` never prints
`disc verified` in a `next:` block.

Rows 72 to 79 apply the syntax `noahsark [GLOBAL-OPTIONS] COMMAND
[SUBCOMMAND...] [COMMAND-OPTIONS] [ARGUMENTS]`. The command groups are
`disc` (subcommands `burned`, `lost`, and `verified`) and `image`
(subcommand `build`). A command option comes after the last subcommand
word. A group has no options of its own, other than `-h`. The global
options are `--repo`, `-q` / `--quiet`, `--yes`, `--force-yes`, `-h`,
and `--version`. Each other option is a command option. `-h` is the one
exception to the position rule: `noahsark -h` lists the commands and the
groups, and `noahsark COMMAND -h` and `noahsark -h COMMAND` print the
help of that command, or the subcommands of a group. The tool checks the
position before it reads the repository, so a usage error changes
nothing in any state.

Rows 85 to 88 are `restore`. For each chunk, the plan takes a disc that
is not `lost` when one holds the chunk. A disc that is `lost` and that
still holds a needed chunk shows as `(lost)` in the plan. `restore`
does not ask for it. A chunk that no catalog INDEX lists has no known
disc (row 85a). `restore` handles it as a chunk on a `lost` disc: it
restores every file that it can, names each file that it cannot
restore, and exits 1. OPERATIONS.md, "Disc swap, one drive", gives the
exact lines of the plan. Every problem line has the prefix `noahsark:
restore: warning: `. `B` in `staged: N items, B bytes` is the sum of the
stored file sizes of the Staged items ("Item states"): the chunk files
in staging and the metadata object files in the catalog.

## 6. State to `next:` block

`status` prints one `next:` block. It is the one command that prints the
block. Each other command ends with the line `next: noahsark status` only
when it changed state. The block joins its lines with
`&&`, so that a failed line stops the lines after it. Two blocks have a
second part that is not joined: the `pack` line that the operator
completes, and the `disc lost` alternative of a `missing` disc. `DEV` is
`pack.device` from the config. `REPO` is the absolute path of the
repository. `TREE` and `IMG` are the disc root and the image under
`staging/plans/UUID/`; for a `pack --out` disc, `TREE` is the target
path of the symlink. `/mnt/ark` is the mount point that `status` always
uses. `SOURCE` is `sources.root` from the config.

Each `noahsark` line of a block carries no `--repo`, except the `sudo`
line: `sudo` does not keep the environment. The operator pastes the
block in the shell where `status` found the repository.

When two or more discs need an action, `status` takes the first match
in this order, and inside one step the lowest number:

1. a `missing` disc;
2. an `on disc only` disc whose last check failed;
3. a `packed` disc, or a `burned` disc;
4. data that `gc` can free now;
5. staged data that no disc holds;
6. a `verified` disc that waits for the 7 days;
7. nothing.

| State | `next:` block |
|---|---|
| `packed` | `next: load a blank disc, then run:` then `sudo noahsark --repo=REPO image build SEQ &&` (only when `IMG` does not exist), `growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z DEV=IMG &&`, `eject DEV && eject -t DEV && sleep 5 &&`, `sudo mkdir -p /mnt/ark && sudo mount -o ro DEV /mnt/ark &&`, `noahsark verify /mnt/ark &&`, `sudo umount /mnt/ark && eject DEV`. For a disc packed with `--close`, the `growisofs` line is `growisofs -dvd-compat -speed=4 -use-the-force-luke=spare:none,tty -Z DEV=IMG &&`. The `close` flag of the `Packed` event holds that choice. After the block, one line that is not part of it: `or burn the folder directly; see the guide, "Burn the folder directly"`. |
| `burned`, last check failed | the `packed` block: the disc is bad, and a new disc gets the kept disc root |
| `packed`, or `burned` with a failed last check, and `TREE` does not exist (a disc that `disc lost --undo` gave back) | `next: disc SEQ has no disc root; no new disc can be burned from it. Discard the disc, then run:` then `noahsark disc lost SEQ`. Its items return to staged, and the next `pack` takes them. |
| `burned` | `next: load disc SEQ, then run:` then `eject DEV && eject -t DEV && sleep 5 &&`, `sudo mkdir -p /mnt/ark && sudo mount -o ro DEV /mnt/ark &&`, `noahsark verify /mnt/ark &&`, `sudo umount /mnt/ark && eject DEV` |
| `verified`, less than 7 days | `next: nothing to do; gc can free disc SEQ after DATE`, then one advice line: `advice: burn a second copy of IMG before gc; see the guide, "A second copy"`. When `IMG` does not exist, the advice line is `advice: copy disc SEQ before gc; see the guide, "A second copy"`. |
| `verified`, 7 days or more (data that `gc` can free) | the same advice line, then `next: noahsark gc` |
| `on disc only` | no block of its own |
| `on disc only`, last check failed | `next: disc SEQ failed its last check. Copy it now, or use your second copy; see the guide, "A second copy". When no copy can be read, run:` then `noahsark disc lost SEQ && noahsark commit` |
| `lost` | no block of its own. Its items are staged again, or wait for a `commit`. |
| `missing` | `next: load disc SEQ "LABEL", then run:` then `sudo mkdir -p /mnt/ark && sudo mount -o ro DEV /mnt/ark &&`, `noahsark recover --source=SOURCE --disc=/mnt/ark &&`, `sudo umount /mnt/ark`, then `or, when disc SEQ is gone for good, run:` then `noahsark disc lost SEQ` |
| staged data, no disc holds it | `next: load a blank disc, then run:` then `dvd+rw-mediainfo DEV \| grep -E 'Mounted Media\|Free Blocks'`, then `then paste this line, type the capacity, and press Enter:` then `noahsark pack --capacity=` |
| nothing | `next: nothing to do` |

The tool does not know the capacity of a blank disc. The `pack` line
therefore ends at `--capacity=`, and the operator types the value.

The `packed` block prints the recommended burn method only: `image
build`, then a burn of the image. The line after the block points to
the second method, a direct burn of the folder, which OPERATIONS.md,
"Burning", defines.

A `disc lost` line in a block carries no answer flag. `disc lost` asks
a critical confirmation, and the operator answers it at the terminal.

The `verified` block does not ask for a second copy as a required step.
The second copy is the operator's choice, and the advice line is the
only reminder.

## 7. The missing disc

A disc from a lost repository that never turns up is `missing` (row
68). `disc lost` (row 59) ends it. `missing` is not a resting state.
`status` always names it in its `next:` block, with `recover` or `disc
lost`. Thus `commit`, `pack`, and `status` never disagree about what is
staged.
