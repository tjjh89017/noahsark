# Text moved out of FORMAT.md

FORMAT.md held host-side text that no reader of a disc needs. This file keeps
that text until OPERATIONS.md takes it. Delete each part from this file when
OPERATIONS.md holds it. Nothing here defines an on-disc byte. The text was
moved as it was, less the parts known to be false; check each part against the
code before OPERATIONS.md takes it.

Two golden vectors moved with the text: the exclude pattern vector (a stated
tree and a stated rule set), and the host structure vectors (the burn step
tree listing, the local ref log and notes file, the state log, the burn
plan). FORMAT.md's golden vector list no longer names them.

## Exclude pattern language

The rules decide which entries a tree holds, and so every tree id. A reader
of a disc never needs them.

Rules and sources. A rule is one pattern line. The rules come from three
sources, in this order: every configured exclude key in configuration order,
then every command-line exclude option in command-line order, then the ignore
files on the path from the source root down to the directory that holds the
entry, root first. A rule in a deeper file comes after a rule in a shallower
one. Inside one file, rules are in line order.

Lines. A blank line and a line whose first byte is `#` are ignored. A leading
`\#` or `\!` is a literal `#` or `!`. Trailing spaces are ignored unless the
last one is escaped with `\`. Matching is on raw bytes, byte for byte,
case-sensitive, with no normalization.

Anchoring. A pattern that contains a `/` other than a trailing one is anchored:
it matches relative to the directory of the rule, which is the source root for
a configuration or command-line rule and the directory that holds the ignore
file otherwise. A leading `/` anchors a pattern in the same way and is then
removed. A pattern with no `/` other than a trailing one matches the name of an
entry at any depth below the rule's directory.

Trailing slash. A pattern that ends in `/` matches a directory only. The `/` is
then removed and the rest is matched as above. A pattern with no trailing `/`
matches an entry of any type.

Wildcards. `*` matches any run of zero or more bytes except `/`. `?` matches
exactly one byte except `/`. `[...]` matches one byte from the set, with `-`
for a range and a leading `!` for the complement. `**` has three forms: a
leading `**/` matches in every directory; a trailing `/**` matches every entry
below the named directory; `/**/` in the middle matches zero or more
directories. Any other `**` is two `*`. A `\` escapes the next byte.

Match order and negation. A pattern whose first byte is `!` is a negation. The
rules are applied in the order above to the path of an entry, relative to the
rule's directory, and the last matching rule wins. If it is a negation the
entry is included, otherwise it is excluded. An entry that no rule matches is
included. An excluded directory is not entered: nothing below it is scanned,
and no later negation can bring anything below it back. The source root itself
is never matched against any rule and cannot be excluded.

Snapshot metadata tag 5 stores the configured and the command-line rules of
each source root. FORMAT.md's "Snapshot" section states that record.

## Capacity invariants

Three names carry the whole capacity policy. None of the three is an on-disc
field: capacity planning is host state, computed before a burn.

- `data_budget`: the bytes that object data and a run's own framing files may
  occupy on the disc, across all its runs. `data_budget` charges the sum of
  every run's `stream_bytes` plus the header copies and the
  parity of every run.
- `fill_limit`: the byte position, counted from the start of the medium, that
  no run's last byte may reach. A writer computes it before packing a run and
  checks the plan against it; it is never read back from the disc.
- `reserve`: `capacity_forced - data_budget`.

Four invariants are normative. Any writer that holds them conforms, whatever
arithmetic it used to choose the numbers.

1. No run, its parity and its header copies included, reaches `fill_limit`.
2. `fill_limit` is at most `capacity_forced`, and it is defined
   as `capacity_forced - safety_margin - spare_area`, where `safety_margin`
   is `ceil(capacity_forced * (1 - fill_ratio))` and `spare_area` is
   `ceil(spare_reserve_bytes / 2048)` on a `spare:min` disc, the same
   expression on a `spare:default` disc with that mode's larger
   `spare_reserve_bytes`, and 0 on a sealed disc. That is the definition, and
   it holds whether or not an override is set.
3. When `fec_scheme` is 1, `data_budget` is a whole number of stripes of `k`
   data blocks per run:
   every run's `stream_blocks mod k == 0` once its stream is padded to a
   whole number of columns.
4. The sum of the bytes that every run of the disc occupies, plus the bytes
   the disc still holds free below `fill_limit`, never exceeds `fill_limit`.

The inputs of a writer's reserve estimator are heuristics. They are not part
of the on-disc contract, and a reader never uses them.

## Settings that change disc bytes

The settings below change what a writer puts on the medium. A change to any of
them, after a repository holds discs, is a new epoch or a new profile, never a
silent in-place change. An omission from this index is a defect in the index,
not license to change a setting silently.

| Setting | What it changes on disc |
|---|---|
| Format version, `format.version_major` and `format.version_minor` | The `version_major` and `version_minor` of every structure a writer emits. |
| Current hash algorithm | The multihash algorithm of every new object id. |
| Chunker profile | The cut points of every new chunk, hence the chunk boundaries in every new tree and blob. |
| Gear table id | The Gear table version, which changes every cut point under a profile name. |
| Compression algorithm and level | The stored bytes of every new chunk payload. |
| Compression minimum gain | Whether a chunk is stored compressed or raw. |
| Filesystem profile | The disc filesystem. Fixed per disc at its first burn. |
| Fan-out levels | The object path depth under `objects/`. |
| Close policy, `disc.close_policy` | Whether the first burn seals the disc, hence the superblock `sealed` byte, the `spare_area` term and the tail anchors. |
| Spare mode and spare reserve bytes | The `spare_area` term, hence `reserve` and `data_budget`. Fixed per disc at format time. |
| Expected runs per disc | `catalog_growth`, hence `reserve` and `data_budget`. |
| Fill ratio | `safety_margin`, hence `reserve` and `data_budget`. |
| Forced capacity, `pack --capacity` | `capacity_forced_sectors` in the superblock and in DISCS, hence every run's budget on the disc. |
| FEC scheme and geometry, `fec.scheme`, `fec.k` and `fec.m` | The stripe shape, the column count, the parity file set and the checksum column, hence the FEC stream layout of every run. Version 1 fixes `k` 231 and `m` 23 and refuses any other value. |
| Optional metadata switches | Which optional metadata fields are present in every new tree entry. |
| Source type, `source.type` | The `source_type` and `source_flags` bytes of every new snapshot payload, hence its content id. |
| Exclude rules, `sources.exclude` and `sources.ignore_file` | Which paths the walk keeps, and the exclude-rule bytes that snapshot metadata tag 5 stores. |
| Mount-point crossing, `sources.one_file_system` | Whether the walk descends into a directory that lies on another filesystem. It decides which entries exist in every new tree, and therefore every tree id, every snapshot id and the object set of the run. |
| Symlink following, `sources.follow_symlinks` | Whether the walk follows a symlink and stores the target's content, or stores the link itself as an `entry_type` 3 symlink entry. It decides the entry type, the size and the content reference of every affected entry, and therefore every tree id above it. |
| Label template and repository short name | The label text in the superblock, in DISCS and in `README.txt`. |
| Locality and split settings | Which chunks are rewritten instead of cross-referenced, hence the duplicated bytes on disc. |
