NoahsArk backup disc
====================

1. WHAT THIS DISC IS
--------------------
This disc holds part of a NoahsArk backup repository. The on-disc format is
major version 1. Everything that NoahsArk wrote is an ordinary file under the
directory /NOAHSARK/. There are no hidden sectors and no raw areas outside
this filesystem. Every structure starts with the 8-byte text "NOAHSARK" and
an 8-byte kind name, and is little-endian and packed.

2. IDENTITY
-----------
repository uuid: 7905bde1-9332-0cc7-62bd-2f34ff5cbc56
disc uuid: 10e5bfbe-9a95-bb77-08b6-dbfe89e6cd81
disc sequence: 0
label: LATEST disc 0
hash algorithm: sha2-256
pack time: 2026-09-21T14:07:37+08:00
parity geometry: k=231 data columns, m=23 parity columns

3. HOW TO FIND THINGS
---------------------
/NOAHSARK/DISC.bin              disc superblock
/NOAHSARK/README.txt            this file
/NOAHSARK/FORMAT.txt            the full definition of the format
/NOAHSARK/REFERENCE/decoder.py  a standalone Python 3 reference decoder
/NOAHSARK/runs/<seq>/RUN.bin    run header, 512 bytes
/NOAHSARK/runs/<seq>/INDEX.bin  file order and the object table
/NOAHSARK/runs/<seq>/catalog/   REFS.bin and DISCS.bin
/NOAHSARK/runs/<seq>/checksum.bin   per-block digests, if parity exists
/NOAHSARK/runs/<seq>/parity/        one file per parity column
/NOAHSARK/runs/<seq>/RUN2.bin   run header copy
/NOAHSARK/objects/<ab>/<name>   chunks, blobs, trees
/NOAHSARK/snapshots/<name>      snapshot objects

<seq> is the run number, ten decimal digits, zero padded. A disc holds one
run directory.

4. HOW AN OBJECT IS NAMED
-------------------------
The name of an object is the hash of its uncompressed payload bytes and
nothing else. The kind, the compression and the object header do not enter
the name. The name on disc is the lowercase hex of the multihash: two prefix
bytes, then the digest. 1220 means SHA-256, the prefix on every object of
this disc, so the name is 68 hex characters. <ab> is the first two hex
characters of the digest, which is characters 5 and 6 of the file name.

5. HOW TO READ AN OBJECT
------------------------
An object file starts with a 32-byte common header, then a 32-byte object
header. In the object header, at byte offset 3 of that 32-byte header, is
the compression id: 0 means none and 1 means zstd. At offset 8 is
payload_len, a little-endian unsigned 64-bit number. At offset 16 is
stored_len. Skip the 64 header bytes total, take the next stored_len bytes,
decompress them with the named algorithm into exactly payload_len bytes,
hash the result with SHA-256, and compare that digest with the digest in the
name. They must be equal. If they are not, the bytes are damaged; see
part 7.

6. HOW TO WALK A SNAPSHOT
-------------------------
Read catalog/REFS.bin, which is a table of named pointers, and take the
newest record for the name LATEST: the one with the highest time. It gives
a snapshot id. Read that snapshot object. Its body names a root tree id.
Read that tree object: it is a list of directory entries, each with a name,
the POSIX metadata, and either a tree id for a subdirectory or a blob id for
a file. Read the blob object: it holds the ordered chunk ids of that file.
Concatenate the chunk payloads in order and the file is restored. An object
that is not on this disc is on another disc of the repository; INDEX.bin
names that disc by its uuid, and catalog/DISCS.bin gives its label.

7. HOW TO REPAIR
----------------
If the run directory holds no parity/ directory, this disc has no parity;
use the second copy of the disc. Otherwise the run carries Reed-Solomon
parity over its own data files, concatenated in the order INDEX.bin lists
them, each padded to 2048 bytes: the FEC stream. The stream is cut into
231 equal columns of L blocks of 2048 bytes each; FORMAT.txt says how to
derive L from the run header. Stripe i is block i of every column.
checksum.bin is one more column: its block i holds an 8-byte digest of each
of the 231 data blocks of stripe i, so a damaged block can be found. The
23 files under parity/ are the parity columns. Any 231 of the
231 data plus 23 parity blocks of one stripe reconstruct the rest.
FORMAT.txt gives the field arithmetic, the matrix and a worked example.

8. WHERE THE BYTE LAYOUTS ARE
-----------------------------
FORMAT.txt in this directory is the full format document. It holds the
offset, size, type, name and meaning of every field of every structure, the
registries, the magic values, the chunking constants and the Reed-Solomon
definition. It is enough to extract every file from this disc, and to repair
a damaged disc that has parity, with no NoahsArk software.
REFERENCE/decoder.py in this directory is a runnable Python 3 program that
does the extraction in code: it parses DISC.bin, RUN.bin, INDEX.bin and
every object header, verifies content ids, walks a snapshot and prints the
listing.

9. THE FORMAT RULES
--------------------
1. Every integer is little-endian. No big-endian field exists.
2. Every type is fixed width: u8, u16, u32, u64, i32, i64.
3. Every structure is packed, and every gap is a named reserved field. A
   writer writes zero there, and a reader ignores it.
4. Every structure starts with an 8-byte project magic, an 8-byte kind name,
   then version_major and header_len.
5. A reader refuses an unknown version_major. There is no minor version.
6. header_len is the offset of the first byte after the fixed part of a
   structure. A reader obeys it and skips fixed bytes it does not know.
7. Checksums are CRC-32C, polynomial 0x1EDC6F41, reflected, init
   0xFFFFFFFF, final xor 0xFFFFFFFF. A checksum lies after the bytes it
   covers.
8. Every pointer to another structure carries the hash of that structure.
9. A string is encoding, three zero bytes, a 32-bit length, then the bytes.
   Encoding 0 is UTF-8. There is no terminator and no normalization.
10. Every structure has a byte-offset table, and FORMAT.txt holds it.
