# CLAUDE.md

This file gives an agent working guidance for this repository. It never
repeats format details. `FORMAT.md` and `OPERATIONS.md` are the design
authorities. `NOTES.md` is informative.

## Project overview

NoahsArk is a backup system for write-once Blu-ray optical media. It writes
content-addressed objects to discs and reads them back years later. It uses
content-defined chunking for dedup, self-describing on-disc tables, and
Reed-Solomon self-healing per run. The implementation language is Go. The Go
implementation of Phase 1 exists under `cmd/noahsark` and `internal/`. The
specification documents stay the design authority.

## Where the truth lives

Three documents replace the old single spec. `FORMAT.md` is the authority
for every on-disc byte: object layouts, disc and run structures, and reader
rules. `OPERATIONS.md` is the authority for host-side behaviour: the CLI, the
configuration keys, staging, packing, burning, restore, and failure handling.
`NOTES.md` is informative: rationale, evidence, worked examples, the
glossary, and the change log. Where `NOTES.md` disagrees with `FORMAT.md` or
`OPERATIONS.md`, the other two win.

Read the relevant section before you write or change any code. Do not copy
format tables, field layouts, magic values, or CLI syntax into this file.
Point to the document and the heading name instead.

Use this table to find a topic, by document and heading, not by number
(numbers drift when a document is edited):

| Topic | Document | Heading |
|---|---|---|
| Overview, goals, and platform tiers | NOTES.md | "1. Purpose and goals" |
| System structure and data flow | NOTES.md | "1.8 Architecture" |
| Byte layout rules that every structure obeys | FORMAT.md | "2. Binary format rules" |
| Hashing, multihash, and hash epochs | FORMAT.md | "3. Identity and hashing" |
| Chunking algorithm and profiles | FORMAT.md | "4. Chunking" |
| Compression rules | FORMAT.md | "5. Compression" |
| Chunk, blob, tree, snapshot, ref | FORMAT.md | "6. Objects" |
| Disc, run, and append behaviour (on-disc) | FORMAT.md | "7. Disc and run model" |
| Disc, run, and append behaviour (host-side) | OPERATIONS.md | "12. Disc lifecycle and closing" |
| Disc filesystem profiles (on-disc layout) | FORMAT.md | "8. Filesystem profiles and the volume tree" |
| The reference decoder | FORMAT.md | "8.6 Reference decoder" |
| Burning and image building (host-side) | OPERATIONS.md | "10. Disc filesystems and image building" and "11. Burning" |
| Reed-Solomon parity (on-disc layout) | FORMAT.md | "10. Forward error correction" |
| Self-healing and verify (host-side) | OPERATIONS.md | "13. Verify and heal" |
| The run index and the catalog | FORMAT.md | "11. The run index and the catalog" |
| Local cache | OPERATIONS.md | "2.4 Local cache layout" |
| Staging store and GC | OPERATIONS.md | "2.3 Staging store layout" and "4. Staging state machine" |
| Packing and locality | OPERATIONS.md | "8. Packing and locality" |
| Restore planning | OPERATIONS.md | "14. Restore" |
| File metadata and permissions | OPERATIONS.md | "15. Metadata restore policy" |
| Commit flow and the quick check | OPERATIONS.md | "7. Commit" |
| Command syntax | OPERATIONS.md | "16. CLI reference" |
| Config keys | OPERATIONS.md | "17. Configuration reference" |
| Format versioning rules | FORMAT.md | "12. Reader and writer rules" |
| Failure and recovery behaviour | OPERATIONS.md | "20. Failure and recovery actions" |
| Testing and CI | OPERATIONS.md | "22. Test list" and "23. Manual physical checklist" |
| Go-level implementation notes | NOTES.md | "6. Implementation notes" |
| What changed from the old design | NOTES.md | "2.19 Design changes from the superseded design" |
| Term definitions | NOTES.md | "8. Glossary" |
| The Gear table generation rule | FORMAT.md | "4.8 Gear table" |
| Magic numbers and registries | FORMAT.md | "2.2 Magic values" and "2.5 Registries" |
| Burning-host command reference | OPERATIONS.md | "24. Burning-host command reference" |
| Rejected designs and why | NOTES.md | "2.18 Rejected and superseded alternatives" |

If this file disagrees with `FORMAT.md` or `OPERATIONS.md`, those documents
win. Fix this file.

## Implementation rules

Follow these rules for every change, in addition to NOTES.md's "6.
Implementation notes" section.

- Write Go. Use the standard library where it covers the need.
- Code must explain itself. Do not lean on comments to carry the design.
- A comment carries only information related to the code beside it.
- A comment or a commit message must never cite a section number.
  Section numbers drift; describe the rule or name the section instead.
- Give every on-disc structure exactly one Go definition.
- Write explicit little-endian encode and decode functions for every
  structure. Do not use reflection-based marshalling. Do not use struct tags
  for encoding.
- Write the byte layout by hand, field by field, matching the structure's
  offset table in FORMAT.md.
- Write a golden-file test for every structure: encode known values, compare
  to a checked-in file; decode that file, compare the fields.
- Vendor the Gear table. Generate it once from the normative rule in
  FORMAT.md's "4.8 Gear table", check it in as a literal array, and never
  regenerate it from a dependency.
- Check pinned tool versions at startup: `dvd+rw-tools` 7.1-14 or later, and
  `udftools` 2.3 or later. Refuse to run the burn path on an older or
  unpatched build.

## Testing rules

Follow OPERATIONS.md's "22. Test list" and "23. Manual physical checklist"
sections. In summary:

- Test image-first. Build a filesystem image, loop-mount it, verify it,
  simulate append and damage on the image. Physical burns are a manual
  checklist, not CI.
- Put CI test steps in the composite actions under `.github/actions/`
  (`lint`, `unit`, `e2e`).
  Workflows call the composite action; they do not repeat its steps.
- When a tool's behaviour is an open question, write a probe action under
  `.github/actions/probe-<topic>/`. A probe records an unknown answer; a test
  asserts a known one. Promote a probe to a test once its answer is stable.
- Manual physical probes need a real drive and real media. Follow
  OPERATIONS.md's manual checklist and manual probes; do not attempt to
  automate them in CI.
- CI must prove the local cache is only an accelerator: delete the cache and
  restore from the disc images alone.

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
internal/cache        local cache: blobs, trees, runs, INDEX
internal/object       chunk, blob, tree, snapshot writers over a source tree
internal/fec          GF(2^8), Reed-Solomon, checksum column, stream mapping
internal/plan         restore planning from local cache, disc order
internal/progress     progress reporter for long-running commands
internal/image        lay out /NOAHSARK for one run, INDEX, RUN, DISC, REFS,
                      DISCS, decoder.py, parity; mkudffs image build
internal/repolock     repository advisory lock for concurrent state access
internal/restore      walk a snapshot from a mounted image, write files
internal/stage        staging state machine, state log records
docs/                 guide.md (operator guide), decisions.md, fec-reference.md
reference/decoder.py  the on-disc reference decoder
.github/actions       composite actions: lint, unit, e2e
```

## How to work on this repo

1. Read the FORMAT.md or OPERATIONS.md section for the topic before writing
   or changing code.
2. Write the golden-file test first, then the encode and decode functions.
3. Never change a frozen on-disc format without a version bump and a matching
   FORMAT.md change. Ask the user before changing anything FORMAT.md calls
   frozen.
