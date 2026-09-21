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
repository uuid: fb1afd50-2baa-3a1a-7d66-9ca20e45129f
disc uuid: 5ad6116a-c8a8-99c1-77d1-9ce666409c0b
disc sequence: 0
label: 2026-09-21 disc 0
hash algorithm: sha2-256
pack time: 2026-09-21T15:51:46+08:00
parity: none

3. HOW TO FIND THINGS
---------------------
/NOAHSARK/DISC.bin              disc superblock
/NOAHSARK/README.txt            this file
/NOAHSARK/FORMAT.txt            the full definition of the format
/NOAHSARK/REFERENCE/decoder.py  a standalone Python 3 reference decoder
/NOAHSARK/runs/<seq>/RUN.bin    run header, 512 bytes
/NOAHSARK/runs/<seq>/INDEX.bin  file order and the object table
/NOAHSARK/runs/<seq>/catalog/   REFS.bin and DISCS.bin
/NOAHSARK/runs/<seq>/RUN2.bin   run header copy
/NOAHSARK/objects/<ab>/<name>   chunks, blobs, trees
/NOAHSARK/snapshots/<name>      snapshot objects

<seq> is the run number, ten decimal digits, zero padded. A disc holds one
run directory.

4. HOW AN OBJECT IS NAMED
-------------------------
The name of an object is the hash of one kind byte and then the object's
uncompressed payload bytes. Nothing else enters the name. The kind byte is 1
for a chunk, 2 for a blob, 3 for a tree and 4 for a snapshot, the same value
the object header holds at its offset 0. Two objects of different kinds never
share a name, even when their payload bytes are equal: an empty file and an
empty directory are the common case. The compression and the header bytes do
not enter the name. The name on disc is the lowercase hex of the multihash:
two prefix bytes, then the digest. 1220 means SHA-256, the prefix on every
object of this disc, so the name is 68 hex characters. <ab> is the first two
hex characters of the digest, which is characters 5 and 6 of the file name.

5. HOW TO READ AN OBJECT
------------------------
An object file starts with a 32-byte common header, then a 32-byte object
header. In the object header, at byte offset 0 of that 32-byte header, is
the kind byte. At offset 3 is the compression id: 0 means none and 1 means
zstd. At offset 8 is payload_len, a little-endian unsigned 64-bit number. At
offset 16 is stored_len. Skip the 64 header bytes total, take the next
stored_len bytes, decompress them with the named algorithm into exactly
payload_len bytes, hash the kind byte and then the result with SHA-256, and
compare that digest with the digest in the name. They must be equal. If they
are not, the bytes are damaged; see part 7.

6. HOW TO WALK A SNAPSHOT
-------------------------
Read catalog/REFS.bin, which is a table of named pointers, and take the
record with the highest time. To pick an older state, take a record by its
name and time. The record gives a snapshot id. Read that snapshot object.
Its body names a root tree id.
Read that tree object: it is a list of directory entries, each with a name,
the POSIX metadata, and either a tree id for a subdirectory or a blob id for
a file. Read the blob object: it holds the ordered chunk ids of that file.
Concatenate the chunk payloads in order and the file is restored. An object
that is not on this disc is on another disc of the repository; INDEX.bin
names that disc by its uuid, and catalog/DISCS.bin gives its label.

7. HOW TO REPAIR
----------------
This disc carries no parity: the run directory holds no checksum.bin and no
parity/ directory. A damaged byte on this disc cannot be repaired from this
disc. Read the object from the second copy of this disc, or from another
disc of the repository that holds the same object.

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
listing. It verifies and it restores. It does not repair: the forward error
correction part of FORMAT.txt is the full recipe for a repair. Its restore,
verify and list commands take more than one disc root: mount every disc of
the repository and name each root on the one command line, because a
snapshot can span several discs.

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
