# Goal: NoahsArk Phase 1, on-disc format first

Repository: /home/date/git/noahsark. Module path: github.com/tjjh89017/noahsark.
Go 1.27. Read CLAUDE.md first. FORMAT.md, document version 0.4.3, is the
only authority for on-disc bytes. OPERATIONS.md and NOTES.md still describe
an older design; use them for intent only, never for bytes or CLI shape.

## Fixed decisions. Do not reopen them.

- One hash: SHA-256, multihash code 0x12. Every id is 32 bytes.
- One chunker profile, the one FORMAT.md marks default. Gear table vendored
  as a literal array, generated once from the normative rule, checked by a
  test that recomputes it.
- One compression: zstd, via github.com/klauspost/compress/zstd. The only
  third-party dependency allowed. Everything else is standard library.
- No bundle, no feature bits, no LBA, no filter, no TLV spill, no xattr, no
  ACL, no hardlink groups, no append, no encryption. Reserved fields are
  written as zero.
- FEC is file based over the stream INDEX defines, k=231, m=23, 2048-byte
  blocks, Cauchy matrix exactly as FORMAT.md states. Checksum column uses
  the first 8 bytes of SHA-256.
- Every disc carries REFERENCE/decoder.py, standard-library Python 3,
  copied byte for byte from the repository.

## Rules

- One Go struct per on-disc structure. Flat structs, fields in offset order.
- Hand-written little-endian Encode and Decode per structure. No reflection,
  no struct tags, no encoding/binary.Read on structs.
- Golden-file test per structure before the encoder: bytes written by hand
  from the spec table into internal/format/testdata/<name>.golden, encode
  known values and compare, decode the file and compare fields, assert every
  reserved byte is zero.
- Comments carry only facts about the code beside them. Never cite a section
  number. ASD-STE100 style in comments and commit messages.
- Commit small, sign off every commit (git commit -s), no merge commits,
  never commit tmp/ or scratch output. Push after every milestone.
- When FORMAT.md is ambiguous, take the simplest reading that keeps bytes
  deterministic, record it in docs/decisions.md with the spec heading name,
  and continue. Do not edit FORMAT.md. If a reading would change bytes on
  disc, stop and report instead.
- go vet, gofmt, go test must pass before every commit.

## Layout. Update CLAUDE.md's proposed layout to this first.

```
cmd/noahsark          CLI
internal/format       structures, encode, decode, golden tests, carving reader
internal/chunker      Gear table, FastCDC
internal/object       chunk, blob, tree, snapshot writers over a source tree
internal/fec          GF(2^8), Reed-Solomon, checksum column, stream mapping
internal/image        lay out /NOAHSARK for one run, INDEX, RUN, DISC, REFS,
                      DISCS, decoder.py, parity; mkudffs image build
internal/restore      walk a snapshot from a mounted image, write files
reference/decoder.py  the on-disc reference decoder
.github/actions/test  composite action
```

## Milestones. Finish each in full, with tests, commit, push, then continue.

M1 format. Every structure in FORMAT.md: CommonHeader, ObjectHeader, Chunk,
   Blob, Tree, TreeEntry, TLV, Snapshot, SnapshotMeta, Disc, Run, Index and
   its three rows, ChecksumRecord, RefsTable, DiscsTable. A dispatcher that
   reads a CommonHeader and calls the decoder for the kind and major. A
   carving reader that scans a byte stream for the project magic and cuts
   out every structure by its own length fields. Golden tests for all.

M2 objects. FastCDC on an io.Reader. Content ids. zstd with the minimum-gain
   rule. Write a source directory into a staging directory as chunk, blob,
   tree and snapshot files under objects/<ab>/<id> and snapshots/<id>,
   metadata per the tree entry rules, entries sorted per the spec. Test:
   commit a fixture tree twice and get byte-identical objects; change one
   file and get exactly the expected new objects.

M3 image. Build one run: INDEX with Files, Objects and Prereqs, RUN with
   m+2 copies, DISC, REFS, DISCS, decoder.py, checksum column, parity.
   Run mkudffs (udftools 2.3 or later, check at startup) to make a UDF 2.01
   image. Loop-mount in the test, read every structure back, verify every
   content id, run reference/decoder.py against the mount and compare its
   listing with the Go reader's. This is the composite CI action.

M4 restore and heal. Restore a snapshot from the mounted image alone into a
   directory and compare byte for byte with the source. Corrupt up to m
   blocks per stripe in the image file, heal through parity, restore again,
   compare. Corrupt m+1 and assert a clean failure. Delete the staging
   directory and prove restore needs only the image.

M5 CLI. init, commit, pack, image build, verify --image, restore. Phase 1
   flags only; refuse anything else with a clear message. Minimal config.

## Done means

All five milestones on main, CI green, docs/decisions.md lists every
reading you chose, and a README.md that states what NoahsArk is in two
sentences and how to run the M4 experiment.
