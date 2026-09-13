NoahsArk backup disc
====================

1. WHAT THIS DISC IS
--------------------
This disc holds part of a NoahsArk backup repository. The on-disc format is
major version 1, minor version 0. Everything that NoahsArk
wrote is an ordinary file under the directory /NOAHSARK/. There are no hidden
sectors and no raw areas outside this filesystem. Every structure starts
with the 8-byte text "NOAHSARK" and an 8-byte kind name, is little-endian and
packed, and ends with a checksum.

2. IDENTITY
-----------
repository uuid: d0d0d0d0-0000-0000-0000-000000000000
disc uuid: d1d1d1d1-0000-0000-0000-000000000000
disc sequence: 0
label: decoder-scheme0-fixture
media type: 
filesystem profile: oneshot
hash algorithm of the first run: sha2-256
chunker profile of the first run: P4
first write time: 2026-09-14T12:00:00+00:00
object fan-out levels: 1
parity geometry: k=231 data columns, m=23 parity columns

3. HOW TO FIND THINGS
---------------------
/NOAHSARK/DISC.bin              disc superblock, written once
/NOAHSARK/README.txt            this file
/NOAHSARK/FORMAT.txt            the byte layout of every structure
/NOAHSARK/REFERENCE/decoder.py  a standalone Python 3 reference decoder
/NOAHSARK/runs/<seq>/RUN.bin    run header, first file of the run
/NOAHSARK/runs/<seq>/INDEX.bin  file order and the object table
/NOAHSARK/runs/<seq>/catalog/   tables copied from the whole repository
/NOAHSARK/runs/<seq>/checksum.bin   per-block digests
/NOAHSARK/runs/<seq>/parity/        one file per parity column
/NOAHSARK/runs/<seq>/RUN2.bin   run header copy, last file of the run
/NOAHSARK/objects/<ab>/<name>   chunks, blobs, trees
/NOAHSARK/snapshots/<name>      snapshot objects

<seq> is the run number, ten decimal digits, zero padded. The run directory
with the highest number is the newest run, and its catalog/ directory is the
newest catalog. Read that one.

4. HOW AN OBJECT IS NAMED
-------------------------
The name of an object is the hash of its uncompressed payload bytes and
nothing else. The kind, the chunker profile, the compression and the object
header do not enter the name. The name on disc is the lowercase hex of the
multihash: two prefix bytes then the digest, following `hash_algo`. 1220
means SHA-256, the prefix on every object this version writes, so the name
is 68 hex characters. <ab> is the first two hex
characters of the digest, which is characters 5 and 6 of the file name.

5. HOW TO READ AN OBJECT
------------------------
An object file starts with a 32-byte common header, then a 32-byte object
header. In the object header, at byte offset 3 of that 32-byte header, is
the compression id: 0 means none and 1 means zstd. At offset 8 is
payload_len, a little-endian unsigned 64-bit number. At offset 16 is
stored_len. Skip the 64 header bytes total, take the next stored_len bytes,
decompress them with the named algorithm into exactly payload_len bytes,
hash the result with the algorithm the name declares, and compare that
digest with the digest in the name. They must be equal. If they are not, the
bytes are damaged; see part 6.

6. HOW TO WALK A SNAPSHOT
-------------------------
Read catalog/REFS.bin, which is a table of named pointers, and take the
newest record for the name LATEST. It gives a snapshot id. Read that
snapshot object. Its header names a root tree id. Read that tree object: it
is a list of directory entries, each with a name, the POSIX metadata, and
either a tree id for a subdirectory or a blob id for a file. Read the blob
object: it holds the ordered chunk ids of that file. Concatenate the chunk
payloads in order and the file is restored.

7. HOW TO REPAIR
----------------
Each run carries Reed-Solomon parity over its own data files, concatenated
in the order INDEX lists them: the FEC stream. The stream is cut into
231 equal columns of L blocks each; L is in the run header. Stripe i is
block i of every column. checksum.bin is one more column: its block i holds
an 8-byte digest of each of the 231 data blocks of stripe i, so a
damaged block can be found. The 23 files under parity/ are the parity
columns; block 0 of each is a copy of the run header and the column starts
one block later. Any 231 of the 231 data plus 23 parity blocks
of one stripe reconstruct the rest. FORMAT.txt gives the field arithmetic.
If the filesystem directory itself is damaged, scan the medium for the text
"NOAHSARK" to carve out the run header and INDEX by hand; FORMAT.txt gives
the length fields needed to do this.

8. WHERE THE BYTE LAYOUTS ARE
-----------------------------
FORMAT.txt in this directory holds the offset, size, type, name and meaning
of every field of every structure, the registries, the magic values and the
chunking constants. This file and that file together are enough to extract
one file from this disc by hand, with a hex editor and no NoahsArk software.
REFERENCE/decoder.py in this directory is a runnable Python 3 program that
does the same extraction in code: it parses DISC.bin, RUN.bin, INDEX.bin and
every object header, verifies content ids, walks a snapshot and prints the
listing.

9. THE FORMAT RULES
--------------------
1. Every integer is little-endian. No big-endian field exists.
2. Every type is fixed width: u8, u16, u32, u64, i32, i64.
3. Every structure is packed, and every gap is a named reserved field of
   zero bytes.
4. Every structure starts with an 8-byte project magic, an 8-byte kind name,
   then version_major and version_minor.
5. A reader refuses an unknown version_major and ignores an unknown
   version_minor.
6. A version_minor bump only appends fields an old reader can ignore; a
   version_major bump changes shape and an old reader refuses it.
7. Every checksum lies after every byte it covers. Checksums are CRC-32C,
   polynomial 0x1EDC6F41, reflected, init 0xFFFFFFFF, final xor 0xFFFFFFFF.
8. Every pointer to another structure carries the hash of that structure.
9. A string is encoding, three zero bytes, a 32-bit length, then the bytes.
   Encoding 0 is UTF-8. There is no terminator and no normalization.
10. Every structure has a byte-offset table, and FORMAT.txt holds it.
