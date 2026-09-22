# States (design draft)

This document defines the two state machines behind `status`: the state
of one item, and the state of one disc. It gives the trust rule, then a
full state x event table written so a Go table-driven test can read it
row by row.

## 1. Item states

An item is one piece of staged data, tracked by its content id in the
local state log. Five states, plus one flag:

| Word (internal) | Meaning |
|---|---|
| `staged` | Held only in the staging store. No disc carries it yet. |
| `packed` | Chosen into a disc root by `pack`. Not yet marked burned. |
| `burned` | The operator recorded a burn with `disc burned`. Not yet checked. |
| `clean` | `verify` read this item back from a disc and it matched. |
| `on-disc` | `gc` freed the staged copy. A disc alone now holds it. |
| `lost` (reason flag) | `disc lost` marked the item's disc gone. Treated as not-yet-known by the next `commit`. |

`status` never prints these words to the operator. It prints a disc's
state (section 2) and a staged total.

Transitions:

```
commit -> staged
staged, pack -> packed
packed, disc burned -> burned
burned, disc burned --undo -> packed
burned, verify ok -> clean (verify count 1)
clean, verify ok -> clean (verify count += 1)
burned, verify fail -> packed (that copy's items only)
clean, gc (both copies verified, 7 days) -> on-disc
on-disc or clean or burned or packed, disc lost -> lost
lost, commit (source still has the data) -> staged (new record, same content id)
```

An item that is `lost` and that the source no longer has stays `lost`.
Nothing deletes it from the log; the log is a history, not a cache.

## 2. Disc states

`status` shows one of these words for each disc:

| Word | Meaning |
|---|---|
| `packed` | `pack` wrote the disc root. No burn recorded. |
| `burned` | `disc burned` ran. No successful `verify` yet. |
| `verified 1/2` | One copy verified ok. The default policy needs two. |
| `verified` | Two verified copies (or `gc.min_verified_copies`, whichever is set). |
| `on disc only` | Every item of the disc is `on-disc`; the staged copy is freed. |
| `lost` | `disc lost` ran. The disc is out of the backup for good. |
| `missing` | Named by another disc's tables during `recover`, not yet given. |

`missing` exists only mid-`recover`; it is not a state an item passes
through. It resolves to `on disc only` (once the disc is given) or to
`lost` (once the operator runs `disc lost`).

## 3. The trust rule

- The state log, the disc ledger, and the ref file are the repository's
  own memory. The tool trusts them for anything not yet confirmed on a
  disc.
- A disc's own tables (`INDEX`, `DISCS`, `REFS`, all inside `NOAHSARK/`
  on that disc) are the truth for what that disc holds. `verify` and
  `recover` read them and write matching cache entries; nothing else
  writes the cache.
- When the two disagree — the log says an item is `on-disc` on disc X,
  but disc X's own `INDEX` does not list it — the disc wins. `gc` and
  `verify` refuse to trust the log alone; a `gc` that cannot confirm an
  item against a cached `INDEX` leaves that item alone and reports it,
  rather than freeing on faith.
- `disc lost` is the one command that tells the tool to stop trusting a
  disc's tables, because the disc itself is unreachable. After it, the
  log's own record of "lost" is the truth, until a `commit` replaces it
  with fresh staged data.
- Physical evidence only: a `verify` counts a copy toward the 2-of-2 rule
  only when `DISC-ROOT` is outside the repository and is a read-only
  mount — a real disc, or a loop mount of the built image. A verify of
  the packed tree under `staging/` still checks every byte and still
  fails on damage, but it never raises a verify count, because staging
  and the disc are not independent: losing the repository would lose
  both at once.

## 4. State x event table

One row is one test case: given `state` and `event`, the tool reaches
`result`, prints `message`, and prints `next` (or is `refused`, in which
case `message` explains why and `next` says what to do instead).

Events: `commit`, `pack`, `image build`, `disc burned`, `disc burned
--undo`, `verify ok (disc)`, `verify ok (packed tree)`, `verify fail`,
`second verify ok`, `disc lost`, `gc`, `recover (all discs)`, `recover
(disc missing)`.

Every message that names a disc uses the same form the disc carries
everywhere else in this build: `disc SEQ "LABEL"`. Every message that
counts data uses `item(s)`, never `object(s)`.

| # | State | Event | Result | Message | Next |
|---:|---|---|---|---|---|
| 1 | (none) | commit | staged | `staged: N items, B bytes` | `noahsark pack` when near one disc |
| 2 | staged | pack | packed | `packed disc SEQ "LABEL": N item(s), B bytes` | `noahsark status` |
| 3 | staged | pack, capacity holds not one item | refused | `capacity ... holds not one item` | use a larger `--capacity` |
| 4 | packed | image build | packed | `built image FILE (N bytes)` | run the printed `growisofs` line |
| 5 | packed | image build, not root | refused | `image build needs root for the loop mount` | run the printed `sudo` line |
| 6 | packed | disc burned | burned | `disc SEQ "LABEL": marked burned, N item(s) marked` | mount, then `noahsark verify` |
| 7 | packed | disc burned --undo | refused | `disc SEQ is not burned` | nothing to undo |
| 8 | packed | verify ok (disc) | refused | `disc SEQ is not marked burned; run: noahsark disc burned SEQ` | run that, then `verify` again |
| 9 | packed | verify ok (packed tree) | packed | `disc SEQ "LABEL": N items, ok` then `not counted: this is not a disc` | burn it, `disc burned`, then `verify` the disc |
| 10 | burned | verify ok (disc) | clean, verify 1/2 | `disc SEQ "LABEL": N items, ok` then `verify: copy 1 of 2 verified; verify the second copy before you free space` | `noahsark status` |
| 11 | burned | second verify ok | clean, verify 2/2 | `disc SEQ "LABEL": N items, ok` then `verify: 2 of 2 copies verified` | nothing to do |
| 12 | burned, no copy of this disc has ever verified ok | verify fail | packed (reason: verify failed) | `disc SEQ "LABEL": bad; this copy is bad; N item(s) returned to packed` | burn a new copy from the kept image, `disc burned`, then `verify` |
| 13 | burned | disc burned --undo | packed | `disc SEQ "LABEL": burn mark removed` | burn again when ready, then `disc burned` |
| 14 | burned | disc burned | refused | `disc SEQ is already burned` | nothing to do |
| 15 | clean (1/2) | second verify ok | clean (2/2) | `disc SEQ "LABEL": N items, ok` then `verify: 2 of 2 copies verified` | nothing to do |
| 16 | clean (1/2 or 2/2), at least one copy has verified ok before | verify fail | clean (unchanged); this copy only is bad | `disc SEQ "LABEL": bad; this copy is bad, the other copy is still good; burn a new copy, then verify it.` | burn a new copy of the same disc, then `verify` it |
| 17 | clean (any) | disc burned --undo | refused | `disc SEQ is verified and cannot be returned to packed` | no action; burn and verify a fresh copy instead |
| 18 | clean 2/2, 7+ days | gc | on-disc | `gc: freed N item(s), B bytes` | nothing to do |
| 19 | clean 1/2, or < 7 days | gc | clean (unchanged) | `gc: disc SEQ: 1/2 copies verified, or too soon; N item(s) held` | verify the second copy, or wait |
| 20 | clean, cache has no INDEX for the disc | gc | clean (unchanged), item skipped | `gc: N item(s) skipped: disc SEQ's table is not cached` | `verify` or `recover` that disc, then `gc` |
| 21 | on-disc | verify ok (disc) | on-disc, verify += 1 | `disc SEQ "LABEL": N items, ok` then `verify: N of N copies verified` | nothing to do |
| 22 | on-disc | verify fail | refused for gc purposes | `disc SEQ "LABEL": bad; the staged copy is already freed` | restore from the other copy; see row 27 |
| 23 | any (packed, burned, clean, on-disc) | disc lost | lost | `disc SEQ "LABEL": marked lost, N item(s) will be re-staged if the source still holds them` | `noahsark commit` |
| 24 | lost | verify (any) | refused | `disc SEQ is marked lost` | nothing to verify; `commit` to re-stage what the source still has |
| 25 | lost | disc lost | refused | `disc SEQ is already marked lost` | nothing to do |
| 26 | lost | gc | refused | `disc SEQ is marked lost; nothing to free` | nothing to do |
| 27 | lost | commit, source still has the data | staged (new record) | `staged: N items, B bytes` (includes re-staged items) | `noahsark pack` |
| 28 | lost | commit, source no longer has the data | lost (unchanged) | commit succeeds; no line names the item, because commit only sees the source, never the log | data from this disc alone is gone |
| 29 | (repo lost) | recover (all discs) | on-disc, for every item every disc lists | `recover: ok` | `noahsark status` |
| 30 | (repo lost) | recover (disc missing) | missing, for the disc that no given disc could name | `recover: disc UUID (LABEL) named by another disc, not yet given` | `give disc SEQ to noahsark recover, or run: noahsark disc lost SEQ` |
| 31 | missing | recover (that disc given) | on-disc | `recover: ok` | `noahsark status` |
| 32 | missing | disc lost | lost | `disc SEQ "LABEL": marked lost, N item(s) will be re-staged if the source still holds them` | `noahsark commit` |
| 33 | missing | commit or pack | refused | `next: give disc SEQ to noahsark recover, or run: noahsark disc lost SEQ` | one of the two |
| 34 | on-disc (all items of a disc) | (any state check) | `on disc only` shown by status | `disc SEQ  on disc only  UUID` | nothing to do |

No cell is a dead end: every `refused` row's `Next` column names an
action that moves the repository forward. Row 22 covers "the item was
already freed by `gc` and the disc that held it later fails `verify`":
the tool cannot re-run `gc` in reverse, so the fix is a restore from the
other copy, and, only if both copies are gone, `disc lost` followed by a
`commit` (row 23, then row 27).

Rows 12 and 16 look alike but differ in one fact: whether a copy of this
disc has ever verified ok before. Row 12 is the first verify a disc ever
sees; the failure genuinely removes the burn mark, because nothing has
proven this disc good yet. Row 16 is a later copy of a disc that already
has one good, counted verify; that count is not touched, because the
good copy is still good, and the message says so.

## 5. What replaces "not fed"

The current build's `not fed` disc, with no way forward when a disc from
a lost repository never turns up, becomes `missing` (row 30) plus one new
command, `disc lost` (row 23 and 32). `missing` is not a resting state:
`status` always names it in `next:`, pointing at `recover` or `disc
lost`, never leaving the repository in a state where `commit`, `pack`,
and `status` disagree about what is staged.
