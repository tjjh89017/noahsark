# Open questions (design draft)

This document holds the decisions of the redesign. "Open questions"
lists the points that the owner must still decide, each with its
options and a recommendation. No open question blocks implementation
now. Then come the points that the owner's review decided, the choices
that the drafts made, the flag count, notes for the rewrite, and the
list of deleted commands, flags, and config keys.

## Open questions

### Burn the folder directly

The owner asks for a second method that burns the disc root folder
without an image. `FORMAT.md`, "The UDF volume", requires a pure UDF
2.01 volume built with `mkudffs`, with no ISO 9660 bridge. A `growisofs`
burn of a folder runs `mkisofs`, which makes an ISO 9660 volume with a
UDF 1.02 bridge. That disc breaks the frozen format.

Options:

- (a) Wait for the planned userspace UDF writer, which can write a pure
  UDF volume from a folder.
- (b) Change `FORMAT.md` to accept the bridge volume. That needs a
  version bump.

Recommendation: (a). Delete the placeholder heading "Burn the folder
directly" from the guide until the writer exists.

### Find the repository

OPERATIONS.md, "Repository discovery", already finds the repository:
first `--repo`, then `NOAHSARK_REPO`, then the current directory and
each parent of it. The earlier form of this question asked whether
commands must find the repository from the current directory, as `git`
does. They already do. That premise was wrong. The open point is only
the `export NOAHSARK_REPO=...` line in the guide's set-up block.

Options:

- (a) Keep the discovery rule, and delete the `export` line. The
  operator runs `noahsark` in the repository directory, or gives
  `--repo`.
- (b) Keep the `export` line as a convenience for a shell outside the
  repository.

Recommendation: (a). `recover` still needs `--repo` or `NOAHSARK_REPO`
when it creates the repository directory.

The plan keeps `--repo`. It is a global option: it comes before the
command name, as in `noahsark --repo=PATH recover ...`. If a later
decision deletes it, the global options lose one.

A point to decide: what `image build` does with a global `--repo`.
OPERATIONS.md, "CLI reference", says that every command but `image
build` takes `--repo`. No document says whether `image build` refuses a
global `--repo` or ignores it.

### The device line

The owner asks why `init` prints and stores `device:`. `status` uses it
only in the `growisofs`, `eject`, `mount`, and `ddrescue` lines that it
prints.

Options:

- (a) Keep `init --device` and the `device:` line.
- (b) Always print `/dev/sr0`, and let the operator change the printed
  line.

Recommendation: (a). Add one guide sentence with the reason: `status`
prints the device in each line of its `next:` block, so the block pastes
without an edit. Option (b) breaks the rule that each `next:` line is
pasteable.

### Recover and restore --mount

`recover` takes positional disc roots. `restore` now takes `--disc=DIR`.
The draft guide also lets `restore --mount` build a repository when none
exists. `recover` does the same job. The guide example for that case
gives no `--source`, but `recover` always needs `--source`.

Options:

- (a) `recover` takes `--disc=DIR`, repeatable, in place of positional
  disc roots. Delete the repository building inside `restore --mount`.
  After a lost computer, the operator runs `recover`, then `restore`.
- (b) Keep both as the draft has them.

Recommendation: (a), both parts. The flag count goes up by one
(`recover --disc`).

### Confirmation for the other undo commands

`verify --undo` and `disc lost --undo` print a warning and ask
`Continue? [y/N]`. `pack --undo` and `disc burned --undo` do not ask.

Options:

- (a) Only `verify --undo`, `disc lost --undo`, and `disc verified` ask.
- (b) All four undo commands ask.

Recommendation: (b). Then one rule applies to every undo.

Note: three commands ask now: `verify --undo`, `disc lost --undo`, and
`disc verified`.

### Flags with no guide sentence

These flags have no guide sentence: `pack --dry-run`, `restore
--dry-run`, `ls --long`, `commit --ref`, `commit -m`, `commit
--one-file-system`, `image build --out`, `gc --dry-run`, and the global
options `-q` / `--quiet` and `--version`. `image build --force` and
`pack --out` have one sentence each, in "When something goes wrong" and
in "Options".

Options:

- (a) Cut each flag that has no guide sentence.
- (b) Keep a flag, and add one guide sentence for it.

Recommendation: decide flag by flag. Cut a flag, or add one guide
sentence for each flag that stays. When all go, the command options go
down by eight and the global options go down by two.

### Paths that ls prints

`ls` prints paths from the file system root without the leading slash,
for example `srv/data/photos/`. A `restore` with no path writes the
content of the source root into `DEST`.

Options:

- (a) `ls` prints paths relative to the source root: `photos/`. The
  `restore` path uses the same text.
- (b) Keep `srv/data/photos/`.

Recommendation: (a). Then a path and the no-path `restore` use one
root.

### Flag names

The draft names `verify --no-mark`, `pack --undo SEQ`, `verify --undo
SEQ`, and `disc lost --undo SEQ`. The owner did not name
them. The owner decided that `verify` and `disc lost` get an undo, but
not the name of the flag.

Options: keep these names, or give new ones.

Recommendation: keep.

### Exit code of a refused pack and a refused image build

`states.md` rows 7 and 17 exit 2. Row 7 is a `pack` whose capacity holds
not one item. Row 17 is an `image build` whose image file exists. The
rule gives exit 1 for a refused state and exit 2 for a usage error.

Options:

- (a) Keep exit 2 for both rows.
- (b) Row 7 stays 2. Row 17 becomes 1.
- (c) Both rows become 1.

Recommendation: (b). In row 7 the value of an option is wrong, so it is
a usage error. In row 17 the state refuses the command, so it exits 1.

### Does verify --no-mark write cache entries

`verify` writes the cache entries of a disc. The documents do not say
whether `verify --no-mark` writes them.

Options:

- (a) `verify --no-mark` writes no cache entry.
- (b) `verify --no-mark` writes the cache entries, as `verify` does.

Recommendation: (a). `--no-mark` records nothing.

## Decided in the owner's review

The owner's review comments decided these points. The guide and
`states.md` follow them.

1. **`init` runs in the repository directory.** `init` makes the current
   directory the repository. It takes no `--repo` and no `--capacity`.
   It takes `--source`. Whether it takes `--device` depends on the open
   question "The device line".
2. **The capacity is given at each `pack`.** `pack --capacity` is
   required. No config key stores a capacity. `dvd+rw-tools` is a
   requirement, because `dvd+rw-mediainfo` reads the capacity of a
   blank disc.
3. **`pack` does not read the capacity itself.** The operator runs
   `dvd+rw-mediainfo` and reads the capacity from its output. The
   operator gives that value with `pack --capacity`. The tool never
   runs a drive tool itself.
4. **`pack` can be undone.** `pack --undo SEQ` returns the items of the
   newest disc to staged while that disc is `packed`, and removes the
   disc root in staging. It keeps a disc root that the operator gave
   with `pack --out`. It refuses a disc that has a burn record, and a
   disc that is not the newest. The `disc_seq` is never given to
   another disc (FORMAT.md, "The physical disc"), so the next pack gets
   the next seq.
5. **The first commit follows `init` directly.**
6. **Nested commands, no hyphen.** The command line permits nested
   commands, two levels or more. A command group is a command that has
   subcommands. The groups are `disc` (subcommands `burned`, `lost`, and
   `verified`) and `image` (subcommand `build`). No other command is in a group.
   - `image build` stays two words. An earlier decision renamed it to
     `image-build`. The owner chose nested commands after that rename,
     so the nested form wins.
   - Command options come after the last subcommand word. An option
     between the group and the subcommand is a usage error, exit 2.
     `-h` is the one exception.
   - Help for each level: `noahsark -h` lists the commands and the
     groups. `noahsark disc -h` lists the subcommands of `disc`, one line
     for each. `noahsark disc burned -h` lists the options of that
     subcommand. `noahsark -h disc` and `noahsark -h disc burned` do the
     same.
   - A group with no subcommand, for example `noahsark disc`, is a usage
     error, exit 2. An unknown subcommand is a usage error, exit 2. The
     message lists the subcommands of the group, and nothing changes.
   - A group has no options of its own, other than `-h`.
   - No `help` command exists.
7. **`verify` records the burn.** A good `verify` of a mounted disc
   records the burn and marks the disc `verified`. `verify --no-mark`
   checks and records nothing. For a `packed` disc it prints the
   `disc burned SEQ` line, so the operator never needs to look up the
   disc number. `disc burned` records a burn without a verify. This
   replaces the rule "`disc burned` is the only path from packed to
   burned". The owner asked for it in the review comment.
8. **The tool counts one verified disc. The operator owns the second
   copy.** One good verify of a mounted disc moves the disc to
   `verified`. `gc` frees staging after one verified disc and 7 days.
   `gc.min_verified_copies` is deleted. A disc has at most one burn
   record. A failed verify of a `verified` disc removes the verified
   record: the disc goes back to `burned`, and `gc` holds the data. The
   guide's "A second copy" recommends a second copy and shows how to
   make it. The tool records nothing about it. Reason: a simpler state
   machine. Also, the tool has no way to tell two copies of one disc
   apart without more flags.
9. **A disc can be verified many times.** A good `verify` of a
   `verified` or an `on disc only` disc exits 0 and changes no state. It
   adds one line to a host-side verify log: the time, the disc uuid, and
   the result. The tool never counts these lines as copies. `status`
   shows the date of the last good check, as `verified, last check
   2026-09-27`. This is the periodic check over the years. A failed
   check of an `on disc only` disc tells the operator to copy the disc
   now, to use the second copy, or to run `disc lost`.
10. **A loop mount counts.** `verify` counts a read-only mount outside
    the repository. The tool does not check the source of a mount, so a
    loop mount of an image counts as a real disc. No build tag, and no
    check of the block device. Reasons: CI needs loop mounts, and the
    tool stays simple. The operator is responsible for the use of a real
    disc. The guide does not show loop mounts, because the guide is for
    an operator with a real drive. `states.md` keeps one row for a verify
    of a loop mount, because its table is the test list.
11. **The guide shows the mount before each verify,** with the eject
    and load that make the read come from the disc, a 5-second wait,
    and the unmount and eject after it.
12. **`ls --recursive` has the short form `-R`.** `-R` matches `ls` in
    coreutils.
13. **`restore [COMMAND-OPTIONS] SNAPSHOT [PATH...] DEST`.** The snapshot comes
    first, the destination last. Paths inside the snapshot are
    positional and replace `--include`. `--disc=DIR`, repeatable, gives
    each mounted disc root and replaces the positional disc roots. A
    path follows the `rsync` rule for a trailing slash.
14. **Each `next:` block joins its lines with `&&`.** A failed line,
    for example a failed `growisofs`, stops the lines after it. Thus a
    `verify` never runs on a disc that was not burned.
15. **`verify` and `disc lost` get an undo.** The owner decided that `verify` and
    `disc lost` get an undo.
    - `verify --undo SEQ` removes the verified record of a `verified`
      disc. The disc goes back to `burned`, and `gc` holds the data. The
      burn record stays: `disc burned --undo` removes it. The tool
      refuses a disc that is `on disc only`, because `gc` already freed
      staging. The tool refuses a disc that has no verified record. Use
      case: a verify by mistake, or a disc that the operator sees later
      is bad.
    - `disc lost --undo SEQ` removes the lost mark of a found disc.
      - A disc that was `verified` goes to `burned`, not to `verified`.
        The verified record is removed, and the burn record stays. An
        item that went back to staged, and that no later pack took, is
        `burned` again. `gc` frees nothing of this disc until a good
        `verify`. That `verify` moves the disc to `verified`, as for any
        `burned` disc. Reason: `disc lost` usually happens to a disc that
        was burned and verified and then went away. `gc` must not free
        data on the word of a disc that nobody checked after it was
        found.
      - A disc that was `on disc only` goes back to `on disc only`.
        Staging holds nothing of it, so `gc` has nothing to free. A
        `lost` item is `on-disc` again. An item that a later `commit`
        staged again stays `staged`, and the next pack takes it.
      - A disc that was `missing` goes back to `missing`.
      - An item that a later pack took stays on its new disc. The same
        data on two discs does no harm.
      - After a `y`, the command prints a `next:` block with the mount,
        `verify`, and unmount lines. For an `on disc only` disc, that
        `verify` is a check that changes no state.
      - The tool refuses a disc that was `packed` or `burned` when `disc
        lost` ran: `disc lost` removed its disc root, and its items are
        staged again for the next pack.
16. **The two new undo commands ask first.** `verify --undo` and `disc
    lost --undo` print a warning with the disc (seq, label, uuid), the
    state now, the state after, and the effect in one sentence. Then
    they ask `Continue? [y/N]` on standard input. Only `y` or `yes`
    continues. Any other answer, an empty line, or an end of input
    changes no record, prints `nothing changed`, and exits 1. No `--yes`
    flag and no `--force` flag exist for them: a script or a test gives
    the answer on standard input. A refused case prints the refusal and
    does not ask. See "Confirmation for the other undo commands".
17. **Global options come before the command, command options after
    it.** The syntax is `noahsark [GLOBAL-OPTIONS] COMMAND
    [SUBCOMMAND...] [COMMAND-OPTIONS] [ARGUMENTS]`. A command option
    comes after the command name and before the positional arguments.
    In a group, it comes after the last subcommand word. See "Nested
    commands, no hyphen". A global option
    after the command name, or a command option before it, is a usage
    error: exit 2, and the message names the correct position. An option
    is global only when it has the same meaning for every command that
    accepts it. The
    global options are `--repo`, `-q` / `--quiet`, `-h`, and
    `--version`. `-h` is the one exception to the position rule: it works
    in both positions. `noahsark -h` lists the commands and the groups.
    `noahsark COMMAND -h` and `noahsark -h COMMAND` print the help of
    that command, or the subcommands of a group.
    Reason: an operator types `COMMAND -h` by habit, and a usage-error
    message must point to a help form that works.
    This replaces the rule in OPERATIONS.md, "CLI reference", that
    `--repo` comes after the command name.
18. **FEC is set for each disc with `pack --fec`.** `pack --fec` writes
    FEC for this disc. Without `--fec`, the disc has no FEC. FEC stays
    off by default. No `--no-fec` flag and no `init --fec` flag exist.
    The config key `fec.scheme` is deleted. `status` shows `fec` on the
    line of each disc that has FEC. The `next:` block for `pack` stays
    `noahsark pack --capacity=`: the operator adds `--fec`. Reasons:
    - The format scope is the disc. The run header stores `fec_scheme`,
      `fec_k`, and `fec_m`. One disc holds one run, and `verify` and
      heal read the run header only. The plan changes no on-disc byte.
    - `--fec` has the same form as `--capacity`: a choice that the
      operator makes at each `pack`.
    - No flow edits the config file by hand.
19. **`disc verified SEQ` marks a disc verified with no check.** The
    owner asked for it. It is a subcommand of `disc`. It has the same
    relation to `verify` that `disc burned` has to a burn: it records
    the fact on the word of the operator.
    - It reads no disc. It adds the verified record: `burned` ->
      `verified`. It accepts a `burned` disc only. For a `packed` disc
      it refuses and names `disc burned SEQ` and `verify`. It refuses
      `verified`, `on disc only`, `lost`, and `missing`. A refusal exits
      1.
    - It prints a warning and asks `Continue? [y/N]`, with the rules of
      the undo commands. The warning names the disc (seq, label, uuid)
      and `burned -> verified`. It says that the tool did not read the
      disc, that `gc` frees the repository copy after the wait time,
      and that the data is lost if the disc is bad.
    - It adds a verify log line with the result `not checked`. `status`
      shows `verified, not checked`. A later good `verify` shows `last
      check DATE` in its place.
    - `verify --undo SEQ` removes the verified record, whatever made it.
      No new undo flag exists.
    - `status` never prints `disc verified` in a `next:` block. The block
      of a `burned` disc stays the mount and `verify` lines.

## Choices the drafts made

Each item is a choice that the owner can make in a different way.

1. **Restore layout.** A `restore` with no path writes a snapshot's
   content directly into `DEST` (`DEST/notes.txt`), not below the full
   source path (`DEST/srv/data/notes.txt`). Alternative: keep the old
   layout, so that snapshots from different source roots can share one
   `DEST`. Reason: the known problem named the old layout as confusing,
   and one source root for each repository is already the rule.
2. **`disc list` removed, folded into `status`.** Alternative: keep it
   as a listing for scripts. Reason: `status` is the single interface. A
   second command that prints a subset of the same facts can drift, as
   the old stub did.
3. **`disc lost` re-stages through a normal `commit`,** not through a
   dedicated command that reads the disc's own `INDEX`. Alternative: the
   dedicated command works without a live source and is exact. Reason:
   the source is the durable copy once a disc is gone. A plain `commit`
   already handles a changed or missing file. Items whose staged file
   still exists go back to staged without a `commit`.
4. **Burn device set once at `init --device`,** stored in the config,
   used only in printed lines. Alternative: a `pack --device` flag, or a
   fixed `/dev/sr0` that the operator edits by hand in each printed
   line. Reason: the design rule is that each `next:` line pastes
   without an edit, and no flow edits the config file by hand. The
   device does not change from disc to disc, so `init` is the one place
   to give it. This choice depends on the open question "The device
   line", which stays open.
5. **`ls` and `log` stay two commands.** Alternative: one `log --paths
   SNAPSHOT`. Reason: `ls` answers "what is in this snapshot", `log`
   answers "which snapshots exist". A merge needs a flag to switch
   modes.
6. **`recover --source` is required on every call.** Alternative: make
   it optional, since a repository that exists already has the source
   path. Reason: the optional form needs `status` to print one more kind
   of `next:` line for the case where the source is missing. Always
   asking is one fact fewer.
7. **Snapshot ids print as 12 characters of the digest.** The full id is
   68 hex characters: the multihash prefix `1220` and a 64-character
   SHA-256 digest. The tool drops the constant prefix and prints the
   first 12 digest characters, for example `1b03c7e2a9f4`. That gives 48
   bits, enough for the snapshot count of a personal archive. The tool
   accepts any unique prefix of the digest. Alternative: 8 characters,
   as for the disc uuid, or the full id.
8. **The word "item" replaces "object"** in all text that the operator
   reads. Alternative: keep "object", common in backup tools. Reason:
   "item" is short, has no other meaning in this domain, and reads well
   in `staged: N items`.
9. **`gc` gives the reason that it holds items in one line** (`too
   soon; N item(s) held until DATE`, or `not verified; N item(s)
   held`). `status` already shows the exact disc state, so the `gc`
   line only points the operator back to it.

## Flag count

The guide's command reference lists 4 global options and 29 command
options. The global options are `--repo`, `-q` / `--quiet`, `-h`, and
`--version`. `-q` and `--quiet` are one option. The command options
are:

| Command | Flags | Count |
|---|---|---:|
| `init` | `--source` `--device` | 2 |
| `commit` | `--ref` `-m` `--exclude` `--one-file-system` | 4 |
| `pack` | `--capacity` `--out` `--close` `--fec` `--dry-run` `--undo` | 6 |
| `image build` | `--out` `--force` | 2 |
| `disc burned` | `--undo` | 1 |
| `disc lost` | `--undo` | 1 |
| `verify` | `--no-mark` `--heal` `--out` `--undo` | 4 |
| `gc` | `--dry-run` `--force-after` | 2 |
| `restore` | `--disc` `--mount` `--overwrite` `--dry-run` | 4 |
| `recover` | `--source` | 1 |
| `ls` | `-R` / `--recursive` `--long` | 2 |
| Total | | 29 |

`-R` is the short form of `--recursive`, not a new flag. The global
options are not in the count of 29. A group has no option of its own,
other than `-h`, a global option. `disc verified` has no command
option.

The guide's command reference lists 14 commands: 10 one-word commands
(`init`, `commit`, `pack`, `verify`, `status`, `gc`, `restore`,
`recover`, `ls`, `log`), and 4 subcommands in 2 groups (`disc burned`,
`disc lost`, `disc verified`, `image build`).

The open questions change the count: "Recover and restore --mount" adds
one (`recover --disc`), and "Flags with no guide sentence" can remove up
to eight command options and two global options.

The review checked six flags against the rule that a flag exists only
when a guide sentence needs it:

| Flag | Guide sentence that needs it | e2e need | Verdict |
|---|---|---|---|
| `pack --label` | None. The default label (the newest ref's name, then `disc SEQ`) is the only one the guide shows. | No e2e cell asserts a custom label. The `chain` and `incremental` cells find discs by uuid and seq. | **Cut.** |
| `pack --out` | "Options": `pack --out=DIR` writes the disc root outside the repository. | The `rebuild` cell writes a disc root, deletes the repository, and must find the disc root outside `staging/`. | **Keep.** |
| `pack --fec` | "Options": `pack --fec` adds repair data to this disc only. | The `fec` e2e cell packs with `--fec`, then heals a damaged stripe. | **Keep.** |
| `image build --force` | "When something goes wrong": `image build` refuses an existing image file. Add `--force` to build it again. | A cell that runs `image build` again after a partial failure replaces a half-built image. | **Keep.** |
| `gc --force-after` | "Free space": `gc --force-after=1h` shortens the 7-day wait for one run. | Every e2e cell that runs `gc` needs it. A test cannot wait 7 days. | **Keep.** |
| `verify --out` | "Options": `verify --heal --out=DIR` writes the healed disc root into `DIR`. | The `fec` e2e cell heals a damaged stripe and reads the result from `--out`. | **Keep.** |

## Notes for the rewrite

- The burn record, the verified record, and the verify log of each disc
  go in a host-side ledger under `staging/`. They must not use bytes of
  the on-disc `DISCS` table. The plan changes no on-disc byte.
- The `--close` choice of each disc goes in the same host-side ledger,
  so that `status` prints the sealing `growisofs` line.
- The e2e tests must cover `disc verified` followed by `gc`, to prove
  that the 7-day wait still applies to a disc that no `verify` read.
- `pack --dry-run`, if it stays, uses the `--fec` value of the call. A
  dry run with `--fec` predicts less data for each disc.
- The parser needs one dispatch step for each level of the command
  tree: the global options, then the command or group, then each
  subcommand word, then the command options and arguments. A group
  level accepts only `-h` and a subcommand name.
- `status` is the one place that prints a burn line. `pack` and
  `image build` print `next: noahsark status`.
- `status` prints the `image build` line only when the image file does
  not exist. Thus a block pasted again after a failed burn does not
  stop at an image that exists.
- OPERATIONS.md, "CLI reference", must change to match the option
  positions: global options before the command name, and `-h` among
  them.
- OPERATIONS.md must change to match: "Staging state machine" (the
  single burn record, `verify` from `packed`, a failed verify of a
  `verified` disc), "GC rules" (one verified disc), "Configuration
  reference" (no `gc.min_verified_copies`, no `pack.capacity`, no
  `fec.scheme`), and the
  `pack`, `verify`, and `status` command notes.
- `verify` of the packed tree under `staging/` checks every byte,
  prints `not counted: this is not a disc`, and records nothing. The
  drafts do not make `verify` refuse the packed tree.

## Deleted commands, flags, and config keys

| Item | Reason |
|---|---|
| `--no-progress` | Did the same as `--quiet` and `-q`. Kept `-q` / `--quiet`. |
| `disc list` | A rename stub that duplicated `status`. `status` is the single interface. |
| Manual `echo "pack.capacity = ..." >> config` step | `pack --capacity` is required at each pack. No flow needs a text editor. |
| `pack.capacity` config key | Each blank disc can differ, so `pack` asks for the capacity every time. |
| `fec.scheme` config key | FEC is set for each disc with `pack --fec`. The run header of each disc stores the setting. |
| `gc.min_verified_copies` config key | The tool counts one verified disc. The operator owns the second copy. |
| The copy count (`verified C/N`, `verified 1/2`) | Same reason. A disc is `verified` or not. |
| A second burn record for one disc | Same reason. A disc has at most one burn record. |
| `init --capacity` | The capacity belongs to `pack`, not to `init`. |
| `init --repo` | `init` makes the current directory the repository. |
| `restore --include` | Paths inside the snapshot are positional: `restore [COMMAND-OPTIONS] SNAPSHOT [PATH...] DEST`. |
| `restore`'s positional `DISC-ROOT` arguments | Replaced by `--disc=DIR`, repeatable. |
| The rule "`disc burned` is the only path from packed to burned" | A good `verify` of a mounted disc records the burn. `verify --no-mark` turns that off. |
| `image build --out` as a **required** flag | Kept the flag, dropped the requirement: it defaults to `TREE-DIR` plus `.img`. |
| The message `verify: DISC failed; the burn mark is removed` | Replaced by a message that names the record that the failed verify removed. |
| The `next steps:` block of `pack` | `status` is the one place that prints the burn line. `pack` prints `next: noahsark status`. |
| A fixed `/dev/sr0` in the printed lines | Replaced by the `init`-time `--device`, so each printed line matches the real drive without an edit. |
| The "not fed" disc state, as a resting state with no exit | Replaced by `missing`, which `status` always pairs with a `next:` block: give the disc, or run `disc lost`. |
| `restore`'s old rule that any existing directory is a `DISC-ROOT` | Replaced by: a `DISC-ROOT` is a directory that holds `NOAHSARK/`, given with `--disc`. Fixes the collision with a date-named ref directory. |
| `ls`'s leading space and `!` column in the default listing | Moved the unstable mark out of the path column, so `ls -R` output pastes into a `restore` line unchanged. |
| `pack --label` | No guide sentence uses it. See "Flag count". |
| `status`'s `next:` line "put `--source` into `recover`" | `recover --source` is required on every call. |
| `pack`: `no capacity` as a guide troubleshooting row | `pack` without `--capacity` is a usage error that names the flag. |

The config keys that stay are `repo.uuid`, `staging.dir`,
`sources.root`, and the new `pack.device`. `init` or `recover` writes
each of them. No flow edits the config file by hand.
