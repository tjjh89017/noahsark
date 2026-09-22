# Open questions (design draft)

Each item below is a choice I made that a reasonable person could make
differently. Then a list of what I deleted from the old CLI and config,
with one line of reason each.

## Decisions where the user could reasonably choose differently

1. **Whether a loop mount of the built image counts as a verified
   copy. Not yet changed; this is the first open question.** The current
   rule (guide.md's "Rehearsal without a drive"; states.md's "The trust
   rule") says: a `DISC-ROOT` counts toward the 2-of-2 rule when it is
   outside the repository and read-only. A loop mount of `tree.img`
   passes that test even when `tree.img` itself still sits inside
   `staging/plans/<uuid>/`.

   **The risk.** An operator can loop-mount that same `tree.img` twice,
   in two directories, and run `verify` on each mount. Both verifies
   pass; `gc` sees 2 of 2 and, after 7 days, frees the only staged copy.
   No disc was ever burned. A single lost repository directory, or a
   single failed hard disk, now takes the last copy of that data with
   it — the exact failure this design exists to prevent.

   **Option (a): count only a mount whose source is an optical device.**
   The tool reads `/proc/mounts` (or calls `stat` on the block device)
   and refuses to count a loop mount at all, rehearsal included.
   Simplest rule, closest to "physical evidence only." Cost: the
   rehearsal drill in the guide can no longer claim to prove anything
   about `verify`'s read path; it becomes a `tree`-only smoke test.
   *CI needs*: no loop-mount step in the counted path; the `lowmem` and
   `media/*` e2e cells still loop-mount to test `image build` and the
   reference decoder, but a separate, explicitly uncounted `verify` call
   proves the image is byte-correct, never feeding `gc`.

2. **Whether a loop mount of the built image counts as a verified
   copy — option (b): refuse a loop device whose backing file is inside
   the repository.** The tool resolves the loop device's backing file
   (`/sys/block/loopN/loop/backing_file` on Linux) and compares it
   against the repository path; a loop mount of a copy made outside the
   repository (for example, `tree.img` copied to `/mnt/scratch` first)
   still counts. Keeps the rehearsal drill meaningful when the operator
   deliberately copies the image out first. Cost: a second,
   platform-specific check to get right and to keep working across
   kernels and container setups; a operator who mounts through a
   network share or a bind mount can still fool it.
   *CI needs*: a fixture that loop-mounts `tree.img` both in place and
   from a copy, asserting the first is refused and the second counts;
   this must run on the real CI kernel, not a stub, since the check
   reads `/sys`.

   **Option (c): keep the rule as drafted, accept the risk, and rely on
   the guide's words.** Cheapest to build; correct for the common
   rehearsal case (a scratch repository under `/tmp`, thrown away after
   the drill, per the guide's own advice). Cost: a real repository's
   `staging/` is exactly where `tree.img` lives by default, so the
   dangerous case is also the default case, not an edge case.
   *CI needs*: only a test that a `tree`-directory verify prints
   "not counted," and a warning line the guide's word list can carry
   ("a loop mount only proves a second copy when its image did not stay
   in the repository") — no kernel-level check.

   I have not picked one of these three. My draft guide and states.md
   keep the same wording (b)'s spirit implies but do not yet enforce; a
   choice here changes a guide sentence, a `verify` behavior, and at
   least one e2e cell, so it needs a decision before the guide is final.

3. **Restore layout.** I write a snapshot's content directly into
   `OUT-DIR` (`OUT-DIR/notes.txt`), not below the full source path
   (`OUT-DIR/srv/data/notes.txt`). Alternative: keep the old layout,
   since it lets several snapshots from different source roots share one
   `OUT-DIR` without collision. Reason for my choice: the known problem
   named this as confusing, and one source root per repository is
   already the rule, so the collision case does not occur in practice.

4. **`disc list` removed, folded into `status`.** Alternative: keep it
   as a thin, real listing command for scripts that want disc state
   without the staged total and the `next:` block. Reason: the design
   rule makes `status` the single interface; a second command that
   prints a subset of the same facts invites the two to drift again, as
   the old stub did.

5. **`disc lost` re-stages by falling through to a normal `commit`,**
   rather than a dedicated "restage disc SEQ" command that reads the
   disc's own `INDEX` to know exactly what to re-add. Alternative: the
   dedicated command would work without a live source and would be
   exact rather than inferred from a fresh file walk. Reason: the
   source is the durable copy once a disc is gone; a plain `commit`
   already does the right thing for a changed or missing file, so a
   second, disc-aware code path is one more thing to keep correct.

6. **Burn device set once at `init --device`,** stored in the config,
   used only in printed lines. Alternative: a `pack --device` flag, set
   each time, or no config key at all and a hardcoded `/dev/sr0` with a
   comment telling the operator to edit it by hand. Reason: rule 5 (no
   flow needs a hand edit of the config) rules out the hardcoded
   version; a flag at `pack` repeats a fact that does not change disc to
   disc, so `init` is the one place to say it.

7. **`ls` and `log` stay two commands.** Alternative: merge them, since
   both read the same snapshot data, into one `log --paths SNAPSHOT`.
   Reason: `ls` answers "what is in this snapshot", `log` answers "which
   snapshots exist"; merging would need a flag to switch modes, which
   works against the fewest-flags target.

8. **`recover --source` is now required on every call,** not only when
   the rebuilt config lacks `sources.root`. Alternative (my first
   draft): make it optional, since `recover` into an existing,
   merely-out-of-date repository already has the source path on file.
   Reason for the change: the optional form needed `status` to print a
   third kind of `next:` line ("put `--source` into `recover`") for the
   rare case where it is still missing, and this revision deletes that
   line rather than defining a new state for it. Always asking is one
   fact fewer to reason about, at the cost of one repeated flag on a
   command an operator runs, at most, once per lost repository.

9. **Snapshot ids print at 12 characters, and any unique prefix of that
   length or shorter is accepted.** Alternative: 8, matching the disc
   uuid convention in this guide, or the full 68. Reason: 12 hex
   characters of a well-distributed hash gives about 2^48 of collision
   space, comfortably enough for a personal archive's snapshot count,
   while staying short enough to paste by hand.

10. **The word "item" replaces "object" everywhere the operator reads
    text.** Alternative: keep "object", since it is one syllable
    different and matches common backup-tool language. Reason: the word
    list rule forbids "object" in operator text; "item" is short, is not
    already a loaded word in this domain, and reads naturally in
    `staged: N items`.

11. **`gc` output states the reason it held items back in one line**
    (`1/2 copies verified, or too soon`), rather than two separate
    messages for the two reasons. Alternative: split them, since a
    script parsing output benefits from an exact cause. Reason: `status`
    already carries the exact disc state (`verified 1/2` vs. a fresh
    `verified`), so `gc`'s own line only needs to point the operator
    back to `status` and `verify`, not repeat the diagnosis.

## Flag count: proposed cuts

Before this pass, the command reference in guide.md listed 25 flags
(counting `--long` on `ls`, which an earlier tally missed). The
coordinator's count of the old tool is 22. This section checks six
candidate flags one by one, against the rule that a flag exists only
when a guide sentence needs it.

| Flag | Guide sentence that needs it | e2e need | Verdict |
|---|---|---|---|
| `pack --label` | None. The default label (the newest ref's name, then `disc SEQ`) is the only one the guide ever shows. | No e2e cell asserts a custom label; the `chain` and `incremental` cells read discs by uuid and seq, not by label text. | **Cut.** Nothing in the guide points at it. |
| `pack --out` | "Pack output": `pack --out=DIR` writes the disc root outside the repository, so `gc` never touches it. | The `rebuild` cell writes a disc root, deletes the repository, and must find the disc root again from outside `staging/`; without `--out` that cell has no path to point at. | **Keep.** One real sentence, one real e2e dependency. |
| `pack --fec` | The FEC bullet now says "set `fec.scheme = rs255-gf8` in the config" — the config key does the same job the flag did. | The `fec` e2e cell can set the key in the test repository's config before packing; it does not need a per-invocation override. | **Cut.** A second way to say the same thing is not a second flag. |
| `image build --force` | "When something goes wrong": `image build` refuses an existing image file; add `--force` to rebuild it. | Any e2e cell that reruns `image build` after a partial failure (`lowmem`, `media/*`) needs to replace a half-built image without deleting it by hand first. | **Keep.** |
| `gc --force-after` | "Free space": `gc --force-after=1h` shortens the 7-day wait for one run. | Every e2e cell that runs `gc` needs this: a test cannot wait 7 real days, and `--force-after` is the only way to make an item eligible sooner. | **Keep.** Deleting it would force every gc-testing cell to fake the system clock instead. |
| `verify --out` | The FEC bullet: `verify --heal --out=DIR` writes the healed disc root into `DIR`. | The `fec` e2e cell heals a corrupt stripe and reads the result from `--out`; it cannot assert anything about the heal without a path to read. | **Keep.** |

Cutting `--label` and `--fec` brings the count from 25 to 23: one more
than the coordinator's count of 22 for the old tool, and the guide.md
command reference table now reflects this cut. Every remaining flag has
one guide sentence and, where the test list names a relevant cell, one
e2e dependency.

## Deleted commands, flags, and config keys

| Item | Reason |
|---|---|
| `--no-progress` | Did the same as `--quiet` and `-q`. One flag is enough; kept `-q` / `--quiet`. |
| `disc list` | A rename stub that duplicated `status`. `status` is the single interface. |
| Manual `echo "pack.capacity = ..." >> config` step | `init --capacity` writes it; no flow should need a text editor. |
| `image build --out` as a **required** flag | Kept the flag, dropped the requirement: it now defaults to `TREE-DIR` plus `.img`. |
| The exact wording `verify: DISC failed; the burn mark is removed` | False: the mark is not what changes. Replaced with "this copy is bad, the other copy is still good, burn a new copy." |
| `pack --out` printing `/dev/sr0` unconditionally in the `next:` block | Replaced by the `init`-time `--device`, so the printed line matches the operator's real drive without an edit. |
| The "not fed" disc state, as a resting state with no exit | Replaced by `missing`, which `status` always pairs with a `next:` action: give the disc, or run `disc lost`. |
| `restore`'s old rule that any existing directory is a `DISC-ROOT` | Replaced by: a `DISC-ROOT` is a directory that holds `NOAHSARK/`. Fixes the collision with a date-named ref directory. |
| `ls`'s leading space and `!` column in the default listing | Moved the unstable mark out of the path column, so `ls --recursive` output pastes into `--include` unchanged. |
| `pack --label` | No guide sentence uses it; the default label is the only one shown. See "Flag count: proposed cuts." |
| `pack --fec` | `fec.scheme` in the config does the same work; a flag that repeats a config key is a second way to say one thing. See "Flag count: proposed cuts." |
| `status`'s `next:` line "put `--source` into `recover`" | `recover --source` is now required on every call, so `status` never has to ask for it later. |
| `pack`: `no capacity` as a guide troubleshooting row | `init --capacity` always sets `pack.capacity`, so the flow this row described no longer occurs. |

No config key that stores a fact (`repo.uuid`, `staging.dir`,
`sources.root`, `fec.scheme`, `pack.capacity`, `gc.min_verified_copies`)
was deleted; `pack.device` is new, and every key is now written by a
command (`init` or `recover`) instead of by hand.
