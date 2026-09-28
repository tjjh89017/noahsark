# States (design draft)

This document defines the two state machines behind `status`: the state
of one item, and the state of one disc. It gives the trust rule, a full
state x event table, and the `next:` block that `status` prints for each
state. A Go table-driven test can read the state x event table row by
row.

A counted mount is a read-only mounted filesystem outside the
repository. The tool does not check the source of a mount, so a loop
mount of an image counts as a real disc. CI uses loop mounts (row 32).
The operator is responsible for the use of a real disc.

## 1. Disc records

The tool counts one verified disc. The operator owns the second copy.
The tool records nothing about a second copy. See the guide, "A second
copy".

The repository keeps these records for each disc, in a host-side
ledger:

- a burn record: none or one;
- a verified record: none or one;
- a verify log: one line for each `verify` of a counted mount without
  `--no-mark`, and one
  line for each `disc verified`, with the time, the disc uuid, and the
  result. The result of a `disc verified` line is `not checked`.

The verify log is history. The tool never counts its lines as copies.
`status` reads it to show the time of the last check.

- `disc burned SEQ` adds the burn record.
- A good `verify` of a counted mount adds the verified record. When the
  disc has no burn record, it adds the burn record too. When the disc is
  already `verified` or `on disc only`, it changes no state. It only
  adds a line to the verify log.
- `disc burned --undo SEQ` removes the burn record of a `burned` disc.
- `disc verified SEQ` adds the verified record of a `burned` disc on the
  word of the operator. It reads no disc. It adds a verify log line with
  the result `not checked`. It asks `Continue? [y/N]` first, with the
  rules of the undo commands below.
- A failed `verify` removes one record: the verified record when one
  exists, else the burn record. It also adds a line to the verify log.
  For an `on disc only` disc, it removes nothing. See row 43.
- `verify --no-mark` checks a disc and writes no record and no verify
  log line.
- `verify --undo SEQ` removes the verified record of a `verified` disc,
  whatever made it: `verify` or `disc verified`.
  It keeps the burn record and the verify log. It refuses an `on disc
  only` disc, because `gc` already freed the staged copy.
- `disc lost` adds a lost mark. `disc lost --undo SEQ` removes the lost
  mark. For a disc that was `verified` when `disc lost` ran, it also
  removes the verified record, and keeps the burn record: the disc is
  `burned` until a good `verify`. It refuses a disc that had no
  verified record when `disc lost` ran, because `disc lost` removed its
  disc root.
- `verify --undo`, `disc lost --undo`, and `disc verified` print a
  warning and ask
  `Continue? [y/N]` on standard input before they change a record. Only
  `y` or `yes` continues. Any other answer, an empty line, or an end of
  input changes nothing: the command prints `nothing changed` and exits
  1. A refused case prints the refusal and does not ask.

## 2. Item states

An item is one piece of staged data, tracked by its content id in the
local state log. Five states, plus one flag:

| Word (internal) | Meaning |
|---|---|
| `staged` | Held only in the staging store. No disc carries it yet. |
| `packed` | Chosen into a disc root by `pack`. Its disc has no record. |
| `burned` | Its disc has a burn record and no verified record. |
| `clean` | Its disc has a verified record. The staged copy is kept. |
| `on-disc` | `gc` freed the staged copy, or `recover` read the item from a disc. A disc alone holds it. |
| `lost` (reason flag) | `disc lost` marked the item's disc gone after `gc` freed the item. The next `commit` treats it as not known. |

`status` never prints these words to the operator. It prints a disc's
state (see "Disc states") and a staged total.

Transitions:

```
commit -> staged
staged, pack -> packed
packed, pack --undo -> staged
packed, disc burned -> burned
burned, disc burned --undo -> packed
burned, disc verified -> clean (no disc read; a `not checked` verify log line)
packed or burned, verify ok -> clean
clean, verify ok -> clean (a verify log line only)
burned, verify fail -> packed
clean, verify fail -> burned
clean, verify --undo -> burned
clean, gc (verified, 7 days) -> on-disc
on-disc, verify ok or fail -> on-disc (a verify log line only)
packed or burned or clean, disc lost -> staged (the staged file still exists)
on-disc, disc lost -> lost
lost, commit (the source still has the data) -> staged (new record, same content id)
staged, no later pack took it, disc lost --undo (the disc was verified) -> burned
lost, disc lost --undo (the disc was on disc only) -> on-disc
```

An item that is `lost` and that the source no longer has stays `lost`.
Nothing deletes it from the log. The log is a history, not a cache.

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

`status` adds the word `fec` to the line of each disc that `pack
--fec` packed. The run header of the disc stores the FEC setting. The
tool takes the setting from that run header, not from the config.

After `verified` and `on disc only`, `status` adds the date of the last
good check from the verify log: `verified, last check 2026-09-27`. When
the newest verify log line of an `on disc only` disc is a failure, it
shows `on disc only, last check failed 2026-09-27` instead. A `burned`
or `packed` disc whose newest verify log line is a failure shows
`burned, last check failed DATE` or `packed, last check failed DATE`.
That disc is bad, and its `next:` block burns a new disc from the kept
image. A disc that `recover` rebuilt and that no `verify` read yet shows
no date. When the newest verify log line of a `verified` or an `on disc
only` disc is a `not checked` line from `disc verified`, `status` shows
`verified, not checked` or `on disc only, not checked`. A later good
`verify` replaces it with `last check DATE`.

`missing` exists only during `recover`. An item does not pass through
it. It changes to `on disc only` when the operator gives the disc to
`recover`. It changes to `lost` when the operator runs `disc lost`.

`disc lost --undo` moves a disc that was `verified` to `burned`, and
gives an `on disc only` or a `missing` disc back that state. It refuses
a disc that was `packed` or `burned`. Its disc root is gone, and its items are
staged again, so the next `pack` takes them.

## 4. The trust rule

- The state log, the disc ledger, and the ref file are the repository's
  own memory. The tool trusts them for anything that no disc confirms
  yet.
- A disc's own tables (`INDEX`, `DISCS`, `REFS`, all inside `NOAHSARK/`
  on that disc) are the truth for what that disc holds. `pack` writes
  the cache entries of the disc that it packs. `verify` and `recover`
  write them from a disc. No other command writes the cache.
- The disc wins when the two disagree. An example: the log says that an
  item is `on-disc` on disc X, but the `INDEX` of disc X does not list
  it. `gc` and `verify` do not trust the log alone. When `gc` cannot
  confirm an item against a cached `INDEX`, it keeps the item and
  reports it. It never frees an item on the log's word alone.
- `disc lost` is the one command that tells the tool to stop trusting a
  disc's tables. The disc itself is unreachable. After it, the log's own
  record of "lost" is the truth. A `commit` replaces that record with
  fresh staged data.
- Physical evidence only. A `verify` records a burn or a verified disc
  only when `DISC-ROOT` is a counted mount. A verify of a disc root that
  is not a mount (the packed tree under `staging/`, a `pack --out` DIR,
  or a healed DIR) checks every byte and fails on damage. But it never
  records a burn and never records a verified disc. Staging and the
  disc are not independent: the loss of the repository loses both at
  once. A counted mount is a read-only mounted filesystem outside the
  repository, as the start of this document defines.

## 5. State x event table

One row is one test case. Given `State` and `Event`, the tool reaches
`Result`, prints `Message`, exits with `Exit`, and prints `Next`. When
`Result` is `refused`, `Message` gives the reason and `Next` gives the
action to take instead.

Exit codes follow OPERATIONS.md, "Exit code registry": 0 for success, 1
for a failure at run time or a refused state, 2 for a usage error.

Events: `commit`, `pack`, `pack --undo`, `image build`, `disc burned`,
`disc burned --undo`, `disc verified`, `verify ok`, `verify fail`, `verify --no-mark ok`,
`verify ok (loop mount)`, `verify ok (a disc root that is not a
mount)`,
`verify fail (packed tree)`, `verify --undo`, `gc`, `disc lost`,
`disc lost --undo`, `recover`, `status`, a global option after the
command name, a command option before the command name, a group with
no subcommand, an unknown subcommand, `-h` on a group. `verify` without
a qualifier means a verify of a counted mount. `verify (any)` means
each `verify` event except `verify --undo`. For `verify --undo`, `disc
lost --undo`, and `disc verified`, "answer yes" is `y` or `yes` on standard input. "Answer no" is
any other answer, an empty line, or an end of input.

A message that reports a change names the disc as `disc SEQ "LABEL"`.
A refusal and a `next:` line can name it as `disc SEQ`. A message can
add the uuid after it, as `(UUID)`. Every message that
counts data uses `item(s)`, never `object(s)`.

`noahsark status` in the `Next` column prints the block that "State to
`next:` block" gives.

| # | State | Event | Result | Message | Exit | Next |
|---:|---|---|---|---|---:|---|
| 1 | (none) or staged | commit | staged | `staged: N items, B bytes` | 0 | `noahsark status` |
| 2 | a disc is `missing` | commit | refused | `disc SEQ "LABEL" is missing` | 1 | `noahsark status`: its block gives `recover` or `disc lost` |
| 3 | lost | commit, the source still has the data | staged (new record) | `staged: N items, B bytes` (includes the re-staged items) | 0 | `noahsark status` |
| 4 | lost | commit, the source no longer has the data | lost (unchanged) | commit succeeds. No line names the item: `commit` reads the source, not the log. | 0 | nothing. The data that only this disc held is gone. |
| 5 | staged | pack --capacity | packed | `packed disc SEQ "LABEL": N item(s), B bytes`, then `uuid: UUID` | 0 | `noahsark status` |
| 6 | staged | pack, no `--capacity` | refused | `pack needs --capacity` | 2 | `noahsark status`: its block gives the `pack` line |
| 7 | staged | pack, the capacity holds not one item | refused | `capacity ... holds not one item` | 2 | give a larger `--capacity` |
| 8 | nothing staged | pack | unchanged | `pack: nothing staged` | 0 | `noahsark status` |
| 9 | staged, another disc is `packed` | pack --capacity | packed, a new disc with the next seq | as row 5 | 0 | `noahsark status`: it handles the lowest seq first |
| 10 | a disc is `missing` | pack | refused | `disc SEQ "LABEL" is missing` | 1 | `noahsark status` |
| 11 | packed, the newest disc | pack --undo SEQ | staged. The seq is not given to another disc. The next pack gets the next seq. | `disc SEQ "LABEL": pack undone, N item(s) returned to staged`. For a `pack --out=DIR` disc, also `disc root DIR kept; delete it yourself`. | 0 | `noahsark status` |
| 12 | packed, a later disc exists | pack --undo SEQ | refused | `disc SEQ is not the newest disc; pack cannot be undone` | 1 | burn it as it is, or `noahsark disc lost SEQ` to return its items to staged |
| 13 | burned | pack --undo SEQ | refused | `disc SEQ has a burn record; pack cannot be undone` | 1 | `noahsark disc burned --undo SEQ` when no burn happened, then `pack --undo` |
| 14 | verified, on disc only, lost, or missing | pack --undo SEQ | refused | `disc SEQ is no longer packed; pack cannot be undone` | 1 | nothing to undo |
| 15 | packed | image build | unchanged | `built image FILE (N bytes)` | 0 | `noahsark status` |
| 16 | any, not root | image build | refused | `image build needs root for the loop mount` | 1 | the same line with `sudo`, as the `next:` block prints it |
| 17 | any, the image file exists | image build | refused | `FILE exists` | 2 | add `--force` to build it again |
| 18 | burned or verified | image build | unchanged | `built image FILE (N bytes)` | 0 | `noahsark status` |
| 19 | on disc only or lost | image build | refused | `no disc root at DIR` | 1 | copy a good disc with `ddrescue`, as the guide's "A second copy" shows |
| 20 | packed | disc burned | burned | `disc SEQ "LABEL": burn recorded` | 0 | `noahsark status` |
| 21 | burned | disc burned | refused | `disc SEQ already has a burn record` | 1 | `noahsark status` |
| 22 | verified or on disc only | disc burned | refused | `disc SEQ is already verified` | 1 | nothing to record. A second copy is not recorded. |
| 23 | lost or missing | disc burned | refused | `disc SEQ is marked lost`, or `disc SEQ is missing` | 1 | `noahsark status` |
| 24 | burned | disc burned --undo SEQ | packed | `disc SEQ "LABEL": burn record removed` | 0 | `noahsark status` |
| 25 | packed | disc burned --undo SEQ | refused | `disc SEQ has no burn record` | 1 | nothing to undo |
| 26 | verified, on disc only, lost, or missing | disc burned --undo SEQ | refused | `disc SEQ is not burned` | 1 | nothing to undo. A verified record is physical evidence. |
| 27 | burned | disc verified SEQ, answer yes | verified. The verified record is added. The tool reads no disc. A verify log line with the result `not checked` is added. | `warning: disc SEQ "LABEL" (UUID): burned -> verified`, then `the tool did not read this disc; gc frees the repository copy of its data after the wait time; if the disc is bad, that data is lost`, then `Continue? [y/N]`, then after the answer `disc SEQ "LABEL": verified record added; not checked` | 0 | `noahsark status`. A later good `verify` of the disc acts as row 34 and replaces `not checked` with `last check DATE`. `verify --undo SEQ` removes the record (row 47). |
| 28 | burned | disc verified SEQ, answer no | unchanged | the warning and the question of row 27, then `nothing changed` | 1 | `noahsark status` |
| 29 | packed | disc verified SEQ | refused, no question | `disc SEQ has no burn record; run: noahsark disc burned SEQ, or verify the disc` | 1 | `noahsark disc burned SEQ`, or `noahsark status`: its block verifies the disc |
| 30 | verified, on disc only, lost, or missing | disc verified SEQ | refused, no question | `disc SEQ is already verified`, `disc SEQ is marked lost`, or `disc SEQ is missing` | 1 | nothing to record, or `noahsark status` |
| 31 | packed | verify ok | verified | `disc SEQ "LABEL": N items, ok`, then `burn recorded; verified` | 0 | `noahsark status` |
| 32 | packed or burned | verify ok (loop mount) | verified, as rows 31 and 33. The tool does not check the source of the mount. | as rows 31 and 33 | 0 | `noahsark status`. The operator burns a real disc before `gc`. |
| 33 | burned | verify ok | verified | `disc SEQ "LABEL": N items, ok`, then `verified` | 0 | `noahsark status` |
| 34 | verified | verify ok | unchanged, a verify log line added | `disc SEQ "LABEL": N items, ok`, then `already verified; check logged` | 0 | `noahsark status` |
| 35 | on disc only | verify ok | unchanged, a verify log line added | `disc SEQ "LABEL": N items, ok`, then `check logged` | 0 | `noahsark status` |
| 36 | packed | verify --no-mark ok | unchanged | `disc SEQ "LABEL": N items, ok`, then `not marked; to record this burn, run: noahsark disc burned SEQ` | 0 | that line, or `verify` without `--no-mark` |
| 37 | burned, verified, or on disc only | verify --no-mark ok | unchanged | `disc SEQ "LABEL": N items, ok`, then `not marked` | 0 | `noahsark status` |
| 38 | packed, burned, or verified | verify ok (a disc root that is not a mount: the packed tree, a `pack --out` DIR, or a healed DIR) | unchanged | `disc SEQ "LABEL": N items, ok`, then `not counted: this is not a disc` | 0 | `noahsark status` |
| 39 | packed | verify fail (packed tree) | unchanged | `disc SEQ "LABEL": bad; the packed tree is damaged` | 1 | the newest disc: `noahsark pack --undo SEQ`, then `noahsark status`. Another disc: `noahsark disc lost SEQ`, then `noahsark status`. |
| 40 | packed | verify fail | unchanged, a failed verify log line added | `disc SEQ "LABEL": bad; this disc is bad; no record to remove` | 1 | discard the disc, then `noahsark status`: its block burns a new disc from the kept image |
| 41 | burned | verify fail | packed. The burn record is removed. A failed verify log line is added. | `disc SEQ "LABEL": bad; this disc is bad; burn record removed` | 1 | discard the disc, then `noahsark status` |
| 42 | verified | verify fail | burned. The verified record is removed. `gc` holds the data. A failed verify log line is added. | `disc SEQ "LABEL": bad; this disc is bad; verified record removed; gc holds the data` | 1 | discard the disc, then `noahsark status`: its block burns a new disc from the kept image. When the disc root is gone, its block gives `noahsark disc lost SEQ`. |
| 43 | on disc only | verify fail | unchanged, a failed verify log line added | `disc SEQ "LABEL": bad; the staged copy is already freed; copy this disc now, or use your second copy, or run: noahsark disc lost SEQ` | 1 | the guide's "A second copy" |
| 44 | lost | verify (any) | refused | `disc SEQ is marked lost` | 1 | nothing to verify |
| 45 | missing | verify (any) | refused | `disc SEQ is missing; give it to recover` | 1 | `noahsark status`: its block gives the `recover` line |
| 46 | the disc uuid is not in the disc ledger | verify (any) | refused | `disc UUID is not in this repository` | 1 | load a disc of this repository, then `noahsark status` |
| 47 | verified | verify --undo SEQ, answer yes | burned. The verified record is removed. The burn record and the verify log stay. | `warning: disc SEQ "LABEL" (UUID): verified -> burned`, then `the disc is no longer verified, and gc holds its data`, then `Continue? [y/N]`, then after the answer `disc SEQ "LABEL": verified record removed; burn record kept` | 0 | `noahsark status`: its block verifies the disc again. When the disc is bad: `noahsark disc burned --undo SEQ`, then `noahsark status`. |
| 48 | verified | verify --undo SEQ, answer no | unchanged | the warning and the question of row 47, then `nothing changed` | 1 | `noahsark status` |
| 49 | on disc only | verify --undo SEQ | refused, no question | `disc SEQ is on disc only; gc already freed the staged copy; verify cannot be undone` | 1 | the guide's "A second copy". When no copy can be read: `noahsark disc lost SEQ && noahsark commit`. |
| 50 | packed or burned | verify --undo SEQ | refused, no question | `disc SEQ has no verified record` | 1 | nothing to undo. For a `burned` disc, `noahsark disc burned --undo SEQ` removes the burn record. |
| 51 | lost or missing | verify --undo SEQ | refused, no question | `disc SEQ is marked lost`, or `disc SEQ is missing` | 1 | `noahsark status` |
| 52 | verified, 7 days or more | gc | on disc only | `gc: freed N item(s), B bytes` | 0 | `noahsark status` |
| 53 | verified, less than 7 days | gc | unchanged | `gc: disc SEQ: too soon; N item(s) held until DATE` | 0 | `noahsark status` |
| 54 | verified, the cache has no INDEX for the disc | gc | unchanged, the item skipped | `gc: N item(s) skipped: disc SEQ's table is not cached` | 1 | `noahsark verify` or `noahsark recover` for that disc, then `noahsark gc` |
| 55 | packed, burned, or missing | gc | unchanged | `gc: disc SEQ: not verified; N item(s) held` (no line for a missing disc) | 0 | `noahsark status` |
| 56 | lost | gc | unchanged | `gc: disc SEQ is marked lost; nothing to free` | 0 | `noahsark status` |
| 57 | packed, burned, or verified | disc lost | lost. Its items return to staged. The disc root under staging is removed. | `disc SEQ "LABEL": marked lost; N item(s) returned to staged` | 0 | `noahsark status` |
| 58 | on disc only | disc lost | lost. Its freed items are `lost`. | `disc SEQ "LABEL": marked lost; N item(s) need a new commit` | 0 | `noahsark commit` |
| 59 | missing | disc lost | lost | `disc SEQ "LABEL": marked lost; its items are not known; a new commit stages what the source still holds` | 0 | `noahsark commit` |
| 60 | lost | disc lost | refused | `disc SEQ is already marked lost` | 1 | nothing to do |
| 61 | lost, `verified` when marked lost | disc lost --undo SEQ, answer yes | burned. The lost mark and the verified record are removed. The burn record stays. Each item of the disc that is `staged` and that no later pack took is `burned`. An item that a later pack took stays on its new disc. `gc` frees nothing of this disc until a good `verify`. | `warning: disc SEQ "LABEL" (UUID): lost -> burned`, then `the tool trusts this disc again only after a good check; you must run verify on it`, then `Continue? [y/N]`, then after the answer `disc SEQ "LABEL": lost mark removed; N item(s) back on this disc; verify it now`, then a `next:` block with the verify lines of the `burned` block | 0 | the printed block, or `noahsark status`: its `burned` block has the same verify lines. A good verify acts as row 33. A failed verify acts as row 41, and the block for a disc with no disc root follows. |
| 62 | lost, `on disc only` when marked lost | disc lost --undo SEQ, answer yes | on disc only. The lost mark is removed. Each `lost` item is `on-disc`. An item that a later `commit` staged again stays `staged`, and the next `pack` takes it. | the warning and the question of row 61, with `lost -> on disc only`, then after the answer `disc SEQ "LABEL": lost mark removed; N item(s) back on this disc; verify it now`, then the same `next:` block as row 61 | 0 | the printed block: a check that changes no state. A good verify acts as row 35. A failed verify acts as row 43. |
| 63 | lost, `missing` when marked lost | disc lost --undo SEQ, answer yes | missing. The lost mark is removed. No item changes: the tool never knew the items of this disc. | the warning and the question of row 61, with `lost -> missing`, then after the answer `disc SEQ "LABEL": lost mark removed; give it to recover` | 0 | `noahsark status`: its block gives the `recover` line. `recover` then acts as row 69. |
| 64 | lost, `verified`, `on disc only`, or `missing` when marked lost | disc lost --undo SEQ, answer no | unchanged | the warning and the question of row 61, 62, or 63, then `nothing changed` | 1 | `noahsark status` |
| 65 | lost, `packed` or `burned` when marked lost | disc lost --undo SEQ | refused, no question | `disc SEQ had no verified record when it was marked lost; its items are staged again; the lost mark stays` | 1 | discard the disc, then `noahsark status`: the next pack takes the items |
| 66 | packed, burned, verified, on disc only, or missing | disc lost --undo SEQ | refused, no question | `disc SEQ is not marked lost` | 1 | nothing to undo |
| 67 | (no repository) | recover, every named disc given | on disc only, for every disc given | `recover: ok` | 0 | `noahsark status` |
| 68 | (no repository) | recover, a named disc not given | missing, for each disc that a given disc names and that no call gave | `recover: disc SEQ "LABEL" (UUID) named by another disc, not yet given` | 1 | `noahsark status` |
| 69 | missing | recover, that disc given | on disc only | `recover: ok` | 0 | `noahsark status` |
| 70 | an existing repository | recover | known items keep their state. Unknown items become on-disc. | `recover: ok` | 0 | `noahsark status` |
| 71 | any | status | unchanged | `staged: N items, B bytes`, one `disc SEQ "LABEL"  STATE  [fec  ]UUID` line for each disc, with `, last check DATE`, `, last check failed DATE`, or `, not checked` as "Disc states" defines, and `fec` when the disc has FEC | 0 | the `next:` block for the whole repository |
| 72 | any | a global option after the command name, for example `noahsark status --repo=PATH` | refused, usage error | `--repo is a global option; give it before the command name: noahsark --repo=PATH status` | 2 | the same line with the option before the command name |
| 73 | any | a command option before the command name, or between a group and its subcommand, for example `noahsark --undo disc burned 0` or `noahsark disc --undo burned 0` | refused, usage error | `--undo is an option of disc burned; give it after the last subcommand word: noahsark disc burned --undo 0; see: noahsark disc burned -h` | 2 | the same line with the option after the last subcommand word. `-h` is the one exception to the position rule: `noahsark COMMAND -h` is not a usage error. |
| 74 | any | a group with no subcommand, for example `noahsark disc` | refused, usage error | `disc needs a subcommand:`, then one line for each subcommand: `burned`, `lost`, `verified` | 2 | the same line with a subcommand, or `noahsark disc -h` |
| 75 | any | an unknown subcommand, for example `noahsark disc burnt 0` | refused, usage error | `unknown subcommand of disc: burnt; the subcommands are:`, then one line for each subcommand: `burned`, `lost`, `verified` | 2 | the same line with a listed subcommand |
| 76 | any | `-h` on a group: `noahsark disc -h` or `noahsark -h disc` | unchanged | one line for each subcommand of the group, with what it does | 0 | `noahsark disc SUBCOMMAND -h` for the options of one subcommand |

No cell is a dead end. The `Next` column of each `refused` row names an
action that moves the repository forward.

Rows 41 and 42 apply one rule: a failed verify removes one record, the
verified record first. Thus a failed verify lowers the state of the
disc, and `gc` then holds the data. Row 40 has no record to remove: the
state stays `packed`, and the failed check is logged. Row 43 is the one exception: the
staged copy is already freed, so no lower state can hold it. The tool
logs the failure, `status` shows it, and the operator acts from a
second copy or with `disc lost`.

Rows 31 and 33 differ only in whether `disc burned` ran first. Both end
in `verified`. Row 31 is the default path of the guide.

Rows 34 and 35 are the periodic check. The operator can verify a disc
any number of times, over the years. A good check changes no state.

`pack --undo` accepts only the newest disc (rows 11 and 12). A later
disc can name an earlier disc in its Prereqs table. FORMAT.md, "The
physical disc", says that a `disc_seq` is never given to another disc.
Thus an undone seq stays a hole, and the next `pack` gets the next seq.

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
(row 65). `disc lost`
removed the disc root of that disc, and no burn can be repeated without
it. Its items are staged again, and the next `pack` takes them.

Rows 27 to 30 are `disc verified`. It records a verified disc on the
word of the operator, as `disc burned` records a burn. It reads no disc.
The 7-day wait of `gc` still applies (row 52). `status` never prints
`disc verified` in a `next:` block.

The answer-no rows (28, 48, and 64) exit 1. The operator asked for a change,
and no change happened. Thus a `&&` line after the command does not
run.

Rows 72 to 76 apply the syntax `noahsark [GLOBAL-OPTIONS] COMMAND
[SUBCOMMAND...] [COMMAND-OPTIONS] [ARGUMENTS]`. The command groups are
`disc` (subcommands `burned`, `lost`, and `verified`) and `image`
(subcommand `build`). A command option comes after the last subcommand
word. A group has no options of its own, other than `-h`. The global
options are `--repo`, `-q` / `--quiet`, `-h`, and `--version`. Each
other option is a command option. `-h` is the one exception to the
position rule: `noahsark -h` lists the commands and the groups, and
`noahsark COMMAND -h` and `noahsark -h COMMAND` print the help of that
command, or the subcommands of a group. The tool checks the position
before it reads the repository, so a usage error changes nothing in any
state.

## 6. State to `next:` block

`status` prints one `next:` block. The block joins its lines with
`&&`, so that a failed line stops the lines after it. Two blocks have a
second part that is not joined: the `pack` line that the operator
completes, and the `disc lost` alternative of a `missing` disc. `DEV` is the
device that `init` stored. `TREE` and `IMG` are the disc root and the
image under `staging/plans/UUID/`. `/mnt/ark` is the mount point that
`status` always uses. `SOURCE` is the source that the config holds.

When two or more discs need an action, `status` takes the first match
in this order, and inside one step the lowest seq:

1. a `missing` disc;
2. an `on disc only` disc whose last check failed;
3. a `packed` disc, or a `burned` disc;
4. data that `gc` can free now;
5. staged data that no disc holds;
6. a `verified` disc that waits for the 7 days;
7. nothing.

| State | `next:` block |
|---|---|
| `packed` | `next: load a blank disc, then run:` then `sudo noahsark image build TREE &&` (only when `IMG` does not exist), `growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z DEV=IMG &&`, `eject DEV && eject -t DEV && sleep 5 &&`, `sudo mkdir -p /mnt/ark && sudo mount -o ro DEV /mnt/ark &&`, `noahsark verify /mnt/ark &&`, `sudo umount /mnt/ark && eject DEV`. For a disc packed with `--close`, the `growisofs` line carries `-dvd-compat`. The repository stores that choice at `pack`. |
| `burned`, last check failed | the `packed` block: the disc is bad, and a new disc gets the kept image |
| `packed`, or `burned` with a failed last check, and `TREE` does not exist (a disc that `disc lost --undo` gave back) | `next: disc SEQ has no disc root; no new disc can be burned from it. Discard the disc, then run:` then `noahsark disc lost SEQ`. Its items return to staged, and the next `pack` takes them. |
| `burned` | `next: load disc SEQ, then run:` then `eject DEV && eject -t DEV && sleep 5 &&`, `sudo mkdir -p /mnt/ark && sudo mount -o ro DEV /mnt/ark &&`, `noahsark verify /mnt/ark &&`, `sudo umount /mnt/ark && eject DEV` |
| `verified`, less than 7 days | `next: nothing to do; gc can free disc SEQ after DATE`, then one advice line: `advice: burn a second copy of IMG before gc; see the guide, "A second copy"`. When `IMG` does not exist (a disc that `disc lost --undo` gave back), the advice line is `advice: copy disc SEQ before gc; see the guide, "A second copy"`. |
| `verified`, 7 days or more (data that `gc` can free) | the same advice line, then `next: noahsark gc` |
| `on disc only` | no block of its own |
| `on disc only`, last check failed | `next: disc SEQ failed its last check. Copy it now, or use your second copy; see the guide, "A second copy". When no copy can be read, run:` then `noahsark disc lost SEQ && noahsark commit` |
| `lost` | no block of its own. Its items are staged again, or wait for a `commit`. |
| `missing` | `next: load disc SEQ "LABEL", then run:` then `sudo mkdir -p /mnt/ark && sudo mount -o ro DEV /mnt/ark &&`, `noahsark recover --source=SOURCE /mnt/ark &&`, `sudo umount /mnt/ark`, then `or, when disc SEQ is gone for good, run:` then `noahsark disc lost SEQ` |
| staged data, no disc holds it | `next: load a blank disc, then run:` then `dvd+rw-mediainfo DEV \| grep -E 'Mounted Media\|Free Blocks'`, then `then paste this line, type the capacity, and press Enter:` then `noahsark pack --capacity=` |
| nothing | `next: nothing to do` |

The tool does not know the capacity of a blank disc. The `pack` line
therefore ends at `--capacity=`, and the operator types the value. The
guide shows how to read it.

The `verified` block does not ask for a second copy as a required step.
The second copy is the operator's choice, and the advice line is the
only reminder.

## 7. What replaces "not fed"

The current build's `not fed` disc has no way forward when a disc from
a lost repository never turns up. It becomes `missing` (row 68) and one
new command, `disc lost` (rows 57 to 59). `missing` is not a resting
state. `status` always names it in its `next:` block, with `recover` or
`disc lost`. Thus `commit`, `pack`, and `status` never disagree about
what is staged.
