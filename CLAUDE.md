# CLAUDE.md

This file gives an agent working guidance for this repository. It never
repeats format details. `FORMAT.md`, `OPERATIONS.md` and `docs/states.md` are
the design authorities. `NOTES.md` is informative.

## Project overview

NoahsArk is a backup system for write-once Blu-ray optical media. It writes
content-addressed objects to discs and reads them back years later. It uses
content-defined chunking for dedup and self-describing on-disc tables. Each
disc carries the full format description. The tool restores from the discs
alone, with no repository. The tool writes no repair data: the second copy
of a disc is the repair. The implementation language is
Go. The implementation is under `cmd/noahsark` and `internal/`. For host-side
behaviour the code is the truth, and `OPERATIONS.md` and `docs/states.md`
describe it.

## Where the truth lives

`FORMAT.md` is the authority for every on-disc byte: object layouts, disc
and run structures, the filesystem requirements, and reader rules.
`OPERATIONS.md` and `docs/states.md` together are the authority for host-side
behaviour. `OPERATIONS.md` holds the CLI, the configuration keys, staging,
packing, burning, restore, and failure handling. `docs/states.md` holds the
item and disc state machines and the state x event table; a test reads that
table row by row. `NOTES.md` is informative: rationale, evidence, the
glossary, and the change log. Where `NOTES.md` disagrees with the other
documents, the other documents win.

`docs/guide.md` is the operator guide. It is not a specification. It makes
the use clear to a human. A flag or a behaviour needs an entry in the
specification; it does not need a sentence in the guide.

Read the relevant section before you write or change any code. Do not copy
format tables, field layouts, magic values, or CLI syntax into this file.
Point to the document and the heading name instead.

Use this table to find a topic, by document and heading, not by number
(numbers drift when a document is edited):

| Topic | Document | Heading |
|---|---|---|
| Overview, goals, and platform tiers | NOTES.md | "1. Purpose and goals" |
| System structure and data flow | NOTES.md | "2. Architecture" |
| Byte layout rules that every structure obeys | FORMAT.md | "2. Binary format rules" |
| Hashing and multihash | FORMAT.md | "3. Identity and hashing" |
| Chunking algorithm and parameters | FORMAT.md | "4. Chunking" |
| Compression rules | FORMAT.md | "5. Compression" |
| Chunk, blob, tree, snapshot, ref | FORMAT.md | "6. Objects" |
| Disc and run structures (on-disc) | FORMAT.md | "7. Disc and run model" |
| Disc lifecycle (host-side) | OPERATIONS.md | "12. Disc lifecycle and closing" |
| Disc filesystem requirements and the volume tree (on-disc layout) | FORMAT.md | "8. Filesystem and the volume tree" and "8.1 Filesystem requirements" |
| Burning and image building (host-side) | OPERATIONS.md | "10. Disc filesystems and image building" and "11. Burning" |
| Verify and damage (host-side) | OPERATIONS.md | "13. Verify" |
| The run index and the catalog | FORMAT.md | "9. The run index and the catalog" |
| Repository layout, and what git tracks | OPERATIONS.md | "2. Repository, staging and catalog" |
| Catalog (permanent history in the repository) | OPERATIONS.md | "2.4 Catalog layout" |
| Staging store and GC | OPERATIONS.md | "2.3 Staging store layout" and "4. Staging state machine" |
| State log, local refs and ledgers | OPERATIONS.md | "3. Local file formats" |
| Disc state log | OPERATIONS.md | "3.4 Disc state log record" |
| Item states, disc states, the state x event table, and the `next:` blocks | docs/states.md | the whole document |
| Repository lock | OPERATIONS.md | "6. Concurrency and locking" |
| Exit codes | OPERATIONS.md | "18. Exit code registry" |
| Packing and locality | OPERATIONS.md | "8. Packing and locality" |
| Undo a pack | OPERATIONS.md | "8.4 Undo a pack" |
| Restore planning | OPERATIONS.md | "14. Restore" |
| File metadata and permissions | OPERATIONS.md | "15. Metadata restore policy" |
| Commit flow, excludes and unstable files | OPERATIONS.md | "7. Commit" |
| Command syntax | OPERATIONS.md | "16. CLI reference" |
| Confirmations and answer flags | OPERATIONS.md and docs/states.md | "16.5 Confirmations" and "1.2 Confirmations" |
| Config keys | OPERATIONS.md | "17. Configuration reference" |
| Format versioning rules | FORMAT.md | "2.6 Version policy" and "10. Reader and writer rules" |
| Failure and recovery behaviour | OPERATIONS.md | "19. Failure and recovery actions" |
| Testing and CI | OPERATIONS.md | "20. Test list" and "21. Manual physical checklist" |
| Go-level implementation notes | NOTES.md | "5. Implementation notes" |
| Why the build is as it is | docs/decisions.md | the topic headings |
| Term definitions | NOTES.md | "6. Glossary" |
| The Gear table generation rule | FORMAT.md | "4.6 Gear table" |
| Magic numbers and registries | FORMAT.md | "2.2 Magic values" and "2.5 Registries" |
| Burning-host command reference | OPERATIONS.md | "22. Burning-host command reference" |
| Rejected designs and why | NOTES.md | "3.11 Rejected alternatives" |

If this file disagrees with `FORMAT.md`, `OPERATIONS.md` or `docs/states.md`,
those documents win. Fix this file.

## Implementation rules

Follow these rules for every change, in addition to NOTES.md's "5.
Implementation notes" section.

- Write Go. Use the standard library where it covers the need. The YAML
  config file uses `go.yaml.in/yaml/v3`.
- Code must explain itself. Do not lean on comments to carry the design.
- A comment carries only information related to the code beside it.
- A comment or a commit message must never cite a section number.
  Section numbers drift; describe the rule or name the section instead.
- Give every on-disc structure exactly one Go definition.
- Write explicit little-endian encode and decode functions for every on-disc
  structure and every record of the local binary logs. Do not use
  reflection-based marshalling for them. Do not use struct tags for their
  encoding.
- The YAML config file `config.yaml` is the one exception. A Go struct holds
  its keys, with struct tags for `go.yaml.in/yaml/v3`.
- Write the byte layout by hand, field by field, matching the structure's
  offset table in FORMAT.md.
- Write a golden-file test for every structure: encode known values, compare
  to a checked-in file; decode that file, compare the fields.
- Vendor the Gear table. Generate it once from the normative rule in
  FORMAT.md's "4.6 Gear table", check it in as a literal array, and never
  regenerate it from a dependency.
- `image build` checks the `mkudffs` version: `udftools` 2.3 or later. The
  folder burn needs no `mkudffs`. The tool never runs `growisofs`, thus the
  operator checks `dvd+rw-tools` 7.1-14 or later. OPERATIONS.md's "11.2 Tool
  version check" holds the rule.

## Testing rules

Follow OPERATIONS.md's "20. Test list" and "21. Manual physical checklist"
sections. In summary:

- Test image-first. Build a filesystem image, loop-mount it, verify it,
  write damage into the image, and check that `verify` finds it. A mount that the tool reads (`verify`,
  `restore`, `recover`) is read-only. A test mounts an image read-write only
  to write damage into it. It mounts the image again read-only before the
  tool reads it. One test also builds the disc root
  as an ISO 9660 image, as the folder burn does, and verifies and restores
  it. Physical burns are a manual checklist, not CI, and not a release gate.
- Damage tests check detection, not repair. They use random damage with a
  fresh seed on each run. Print the seed, accept a seed to reproduce a run,
  and also run a fixed list of known seeds.
- Put CI test steps in the composite actions under `.github/actions/`
  (`lint`, `unit`, `e2e`).
  Workflows call the composite action; they do not repeat its steps.
- When a tool's behaviour is an open question, write a probe action under
  `.github/actions/probe-<topic>/`. A probe records an unknown answer; a test
  asserts a known one. Promote a probe to a test once its answer is stable.
- Manual physical checks need a real drive and real media. Follow
  OPERATIONS.md's manual checklist; do not attempt to automate it in CI.
- CI must prove that `recover` builds the catalog again from the disc images
  alone, and that a restore then works.
- The e2e suite holds a hostile source tree cell: odd names, deep paths,
  many small files, symbolic links, permissions, a file larger than one
  disc, and extreme modification times.
- The e2e suite holds a random damage cell: random bytes of a disc image
  change, `verify` must find the damage, `restore` must write no wrong data,
  and a restore from the second copy completes the tree.
- One CI job restores on a clean runner from the images of another job.
- Before the first tag, a person walks `docs/guide.md` in a clean container,
  command by command, from the install to a restore.
- The disc-root fixtures under `cmd/noahsark/testdata/format1/` are the frozen test
  data of format major 1. A Go test reads them. Regenerate them one time
  before the first tag with the `make.sh` beside them; never regenerate
  them after it.

## Git rules

- Never create a merge commit. Never merge a branch locally.
- Use rebase or cherry-pick to bring in changes instead of merging.
- Do not commit anything under `tmp/`.
- Do not commit research artifacts (probe output, scratch notes, exploratory
  scripts). Keep those local or in the scratchpad.
- Sign off every commit (`git commit -s`). The sign-off certifies the
  Developer Certificate of Origin.

## Writing style

Write this file, code comments, and commit messages in ASD-STE100 style:
short sentences, active voice, one instruction per sentence. FORMAT.md,
OPERATIONS.md, and NOTES.md already follow this style; match it.

## Directory layout

This is the layout in use.

```
cmd/noahsark          CLI
internal/format       structures, encode, decode, golden tests
internal/chunker      Gear table, FastCDC
internal/catalog      catalog: snapshots, trees, blobs, and the disc tables
                      INDEX, REFS, DISCS of each disc
internal/object       chunk, blob, tree, snapshot writers over a source tree
internal/plan         restore planning from the catalog, disc order
internal/progress     progress reporter for long-running commands
internal/image        lay out /NOAHSARK for one run, INDEX, RUN, DISC, REFS,
                      DISCS; mkudffs image build of one disc number
internal/repolock     repository advisory lock for concurrent state access
internal/restore      restore a snapshot one mounted disc at a time, write
                      files; verify
internal/stage        item and disc state machines, state log and disc
                      state log records
docs/                 guide.md (operator guide, not a specification),
                      states.md (state machines, part of the specification),
                      decisions.md
cmd/noahsark/testdata/format1
                      frozen disc-root fixtures of format major 1
.github/actions       composite actions: lint, unit, e2e
```

## How to work on this repo

1. Read the FORMAT.md or OPERATIONS.md section for the topic before writing
   or changing code.
2. Write the golden-file test first, then the encode and decode functions.
3. Never change a frozen on-disc format without a version bump and a matching
   FORMAT.md change. Ask the user before changing anything FORMAT.md calls
   frozen.
