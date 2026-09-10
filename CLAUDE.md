# CLAUDE.md

This file gives an agent working guidance for this repository. It never
repeats format details. `spec.md` is the design authority.

## Project overview

NoahsArk is a backup system for write-once Blu-ray optical media. It writes
content-addressed objects to discs and reads them back years later. It uses
content-defined chunking for dedup, self-describing on-disc tables, and
Reed-Solomon self-healing per run. The implementation language is Go. The
project is spec-only today. No code exists yet.

## Where the truth lives

`spec.md` is the single design authority. Read the relevant section before
you write or change any code. Do not copy format tables, field layouts, magic
values, or CLI syntax into this file. Point to the spec section name instead.

Use these section names to find a topic, not the numbers (numbers drift when
the spec is edited):

- Overview, goals, and platform tiers: "Overview" and "Goals, non-goals, and
  priorities".
- System structure and data flow: "Architecture".
- Byte layout rules that every structure obeys: "Binary format rules".
- Hashing, multihash, and hash epochs: "Identity and hashing".
- Chunking algorithm and profiles: "Chunking".
- Compression rules: "Compression".
- Chunk, bundle, chunklist, tree, snapshot, ref: "Object model".
- Disc, run, and append behaviour: "Disc, run, and append model".
- Burning and disc filesystem profiles: "Disc filesystems and burning".
- Reed-Solomon parity and healing: "FEC and self-healing".
- Filters, manifests, and the catalog: "Filters, manifests, and catalog".
- Local cache: "Local cache".
- Staging store and GC: "Staging store".
- Packing and locality: "Packing and locality".
- Restore planning: "Restore and the disc plan".
- File metadata and permissions: "File metadata and permissions".
- Commit flow, quick check, mirror mode: "Commit flow".
- Command syntax: "CLI reference".
- Config keys: "Configuration reference".
- Format versioning rules: "Format evolution and compatibility".
- Failure and recovery behaviour: "Failure modes and recovery matrix".
- Testing and CI: "Testing and CI".
- Go-level implementation notes: "Implementation notes".
- What changed from the old design: "Design changes from the previous
  specification".
- Term definitions: "Glossary".
- The Gear table generation rule: "Appendix A. Gear table".
- Magic numbers and registries: "Appendix B. Magic numbers and registry
  summary".
- Burning-host command reference: "Appendix C. Command reference for the
  burning host".
- Rejected designs and why: "Appendix D. Rejected and superseded
  alternatives".

If this file and `spec.md` ever disagree, `spec.md` wins. Fix this file.

## Implementation rules

Follow these rules for every change, in addition to the spec's "Implementation
notes" section.

- Write Go. Use the standard library where it covers the need.
- Code must explain itself. Do not lean on comments to carry the design.
- A comment carries only information related to the code beside it.
- A comment or a commit message must never cite a spec section number.
  Section numbers drift; describe the rule or name the section instead.
- Give every on-disc structure exactly one Go definition.
- Write explicit little-endian encode and decode functions for every
  structure. Do not use reflection-based marshalling. Do not use struct tags
  for encoding.
- Write the byte layout by hand, field by field, matching the structure's
  offset table in the spec.
- Write a golden-file test for every structure: encode known values, compare
  to a checked-in file; decode that file, compare the fields.
- Vendor the Gear table. Generate it once from the normative rule in
  "Appendix A. Gear table", check it in as a literal array, and never
  regenerate it from a dependency.
- Check pinned tool versions at startup: `dvd+rw-tools` 7.1-14 or later, and
  `udftools` 2.3 or later. Refuse to run the burn path on an older or
  unpatched build.

## Phase discipline

- Implement Phase 1 only, unless the user asks for a later phase.
- The phase table lives in the spec's "Implementation phases" and "CLI
  reference" sections. Check a command's or a config key's phase tag before
  you touch it.
- A Phase 1 build must refuse a later-phase option or key with a clear
  message. Do not silently ignore it.
- Never start a Backlog item without an explicit decision from the user.
  Backlog items are specified and reserved in the format, but not scheduled.

## Testing rules

Follow the spec's "Testing and CI" section. In summary:

- Test image-first. Build a filesystem image, loop-mount it, verify it,
  simulate append and damage on the image. Physical burns are a manual
  checklist, not CI.
- Put CI test steps in a composite action at `.github/actions/test/`.
  Workflows call the composite action; they do not repeat its steps.
- When a tool's behaviour is an open question, write a probe action under
  `.github/actions/probe-<topic>/`. A probe records an unknown answer; a test
  asserts a known one. Promote a probe to a test once its answer is stable.
- Manual physical probes need a real drive and real media. Follow the spec's
  manual checklist and manual probes; do not attempt to automate them in CI.
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
short sentences, active voice, one instruction per sentence. The spec already
follows this style; match it.

## Proposed directory layout

No Go module exists yet. This layout is a proposal for when implementation
starts. Confirm it with the user before creating it, and adjust as the design
needs.

```
cmd/noahsark        CLI entry point
internal/format     binary structures: encode, decode, golden tests
internal/chunker     FastCDC chunking, Gear table, profiles
internal/object      chunk, bundle, chunklist, tree, snapshot, ref
internal/staging     staging store, state machine, GC
internal/pack        packing, locality, burn plan
internal/fec         Reed-Solomon parity, heal, scrub
internal/catalog     filters, manifests, snapshot table, ref table
internal/restore     restore planner, restore pipeline
internal/burn        burner wrapper, command templates
internal/udf         disc filesystem profile handling
.github/actions      composite test action, probe actions
.github/workflows    CI workflow definitions
```

## How to work on this repo

1. Read the spec section for the topic before writing or changing code.
2. Check the phase tag for the command, key, or feature you touch.
3. Write the golden-file test first, then the encode and decode functions.
4. Never change a frozen on-disc format without a version bump and a matching
   spec change. Ask the user before changing anything the spec calls frozen.
