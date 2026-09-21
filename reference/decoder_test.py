"""Unit tests for decoder.py against the checked-in golden files.

Each golden file under internal/format/testdata is a byte-exact encoding
of a Go test fixture (the matching *_test.go file in internal/format).
This test decodes every golden file with decoder.py and checks the
decoded fields against the same values the Go fixture used. It also
decodes the derived vectors: *_hdrlen.golden, which appends 8 bytes to a
structure's fixed part, and *_reserved.golden, which writes a nonzero
byte into every reserved field. A reader must take the same fields from
all three.

Run with:
    python3 -m unittest reference/decoder_test.py

The fixtures under reference/testdata are whole packed run trees, made
with the noahsark binary. To make them again:

    go build -o /tmp/noahsark ./cmd/noahsark

    # scheme0-fixture: src/hello.txt alone, packed --no-fec.
    #   printf 'hello from the scheme0 fixture\n' >src/hello.txt
    # two-files-fixture: the pair below, packed --no-fec.
    # fec-fixture: the same pair, packed --fec.
    #   printf 'hello from the two files fixture\n' >src/sub/hello.txt
    #   printf 'second file content here\n'         >src/sub/second.txt
    # Then, for each of the three:
    /tmp/noahsark init --repo=$w/repo
    /tmp/noahsark commit --repo=$w/repo $w/src
    /tmp/noahsark pack --repo=$w/repo --capacity=dvd+r $FEC --out=$w/tree
    cp -a $w/tree/NOAHSARK reference/testdata/$name/NOAHSARK

No test hard-codes an object id: each finds the object it needs by
walking the snapshot, so a rebuilt fixture needs no test change.
"""

import contextlib
import io
import os
import shutil
import struct
import sys
import tempfile
import types
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import decoder  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
TESTDATA = os.path.join(HERE, "..", "internal", "format", "testdata")

# SCHEME0_FIXTURE is a whole run tree, one snapshot of one small file,
# packed with --no-fec: fec_scheme 0, no checksum.bin, no parity.
SCHEME0_FIXTURE = os.path.join(HERE, "testdata", "scheme0-fixture")

# TWO_FILES_FIXTURE is a whole run tree, one snapshot of two regular
# files in a subdirectory, packed with --no-fec. The restore tests need
# more than one file, so restoring past a bad one still leaves something
# to check.
TWO_FILES_FIXTURE = os.path.join(HERE, "testdata", "two-files-fixture")

# FEC_FIXTURE is the same two-file tree packed with --fec: a checksum
# column and 23 parity files.
FEC_FIXTURE = os.path.join(HERE, "testdata", "fec-fixture")

# TWO_DISCS_FIXTURE is two disc roots of one repository, disc0 and
# disc1. Snapshot "first" holds src/sub/one.txt and is packed to disc0;
# snapshot "second" adds src/two.txt and is packed to disc1. disc1
# therefore needs the sub/ tree and the one.txt chunk of disc0. To make
# it again, with the recipe of this file's header and:
#   $N commit --repo=$w/repo --ref=first  $w/src   # src/sub/one.txt
#   $N pack   --repo=$w/repo --capacity=dvd+r --no-fec --out=$w/tree0
#   printf 'second disc file\n' >$w/src/two.txt
#   $N commit --repo=$w/repo --ref=second $w/src
#   $N pack   --repo=$w/repo --capacity=dvd+r --no-fec --out=$w/tree1
#   cp -a $w/tree0/NOAHSARK reference/testdata/two-discs-fixture/disc0/
#   cp -a $w/tree1/NOAHSARK reference/testdata/two-discs-fixture/disc1/
TWO_DISCS_FIXTURE = os.path.join(HERE, "testdata", "two-discs-fixture")
DISC0 = os.path.join(TWO_DISCS_FIXTURE, "disc0")
DISC1 = os.path.join(TWO_DISCS_FIXTURE, "disc1")


def golden(name: str) -> bytes:
    with open(os.path.join(TESTDATA, name), "rb") as f:
        return f.read()


def snapshot_payload(buf: bytes):
    """The snapshot golden fixture leaves payload_len and stored_len at
    zero: it round-trips the fixed body and the meta records only, not
    the lengths a real writer fills in. So its payload is sliced directly
    and paired with a bare ObjectFile, which parse_snapshot needs only
    for header_len."""
    common = decoder.decode_common_header(buf, "t")
    payload = buf[decoder.OBJECT_FIXED_LEN :]
    return decoder.ObjectFile(common, None, payload, None), payload


class CommonHeaderTest(unittest.TestCase):
    def test_common_header_golden(self):
        h = decoder.decode_common_header(golden("common_header.golden"), "t")
        self.assertEqual(h["magic_kind"], decoder.MAGIC_CHUNK)
        self.assertEqual(h["version_major"], 1)
        self.assertEqual(h["header_len"], decoder.OBJECT_FIXED_LEN)

    def test_reserved_bytes_are_ignored(self):
        h = decoder.decode_common_header(golden("common_header_reserved.golden"), "t")
        self.assertEqual(h["magic_kind"], decoder.MAGIC_CHUNK)
        self.assertEqual(h["header_len"], decoder.OBJECT_FIXED_LEN)

    def test_unknown_version_major_is_refused(self):
        b = bytearray(golden("common_header.golden"))
        struct.pack_into("<H", b, 16, 2)
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.decode_common_header(bytes(b), "t")
        self.assertIn("version_major", str(cm.exception))


class ObjectHeaderTest(unittest.TestCase):
    def want(self, f):
        self.assertEqual(f["kind"], 1)  # chunk
        self.assertEqual(f["hash_algo"], 0x12)  # sha2-256
        self.assertEqual(f["compression"], 1)  # zstd
        self.assertEqual(f["payload_len"], 1024)
        self.assertEqual(f["stored_len"], 512)
        self.assertEqual(f["header_crc32c"], 0xDEADBEEF)

    def test_object_header_golden(self):
        self.want(decoder.object_header_fields(golden("object_header.golden")))

    def test_object_header_reserved_golden(self):
        self.want(decoder.object_header_fields(golden("object_header_reserved.golden")))


class TLVTest(unittest.TestCase):
    def test_tlv_golden(self):
        tlvs = decoder.parse_tlvs(golden("tlv.golden"), "t")
        self.assertEqual(len(tlvs), 1)
        self.assertEqual(tlvs[0]["type"], decoder.TLV_GROUP_NAME)
        self.assertEqual(tlvs[0]["flags"], 0)
        self.assertEqual(tlvs[0]["payload"], b"staff")

    def test_unknown_critical_tlv_is_refused(self):
        b = bytearray(golden("tlv.golden"))
        struct.pack_into("<HH", b, 0, 0x8001, 1)
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.parse_tlvs(bytes(b), "t")
        self.assertIn("0x8001", str(cm.exception))

    def test_unknown_non_critical_tlv_is_kept(self):
        b = bytearray(golden("tlv.golden"))
        struct.pack_into("<HH", b, 0, 0x4001, 0)
        tlvs = decoder.parse_tlvs(bytes(b), "t")
        self.assertEqual(tlvs[0]["type"], 0x4001)
        self.assertEqual(tlvs[0]["payload"], b"staff")


class ChunkTest(unittest.TestCase):
    def check(self, name):
        obj = decoder.decode_object_bytes(golden(name), "t")
        self.assertEqual(obj.common["magic_kind"], decoder.MAGIC_CHUNK)
        self.assertEqual(obj.obj_header["kind"], 1)
        self.assertEqual(obj.obj_header["compression"], 0)
        payload = b"hello world chunk payload"
        self.assertEqual(obj.payload, payload)
        self.assertEqual(obj.obj_header["payload_len"], len(payload))
        self.assertEqual(obj.obj_header["stored_len"], len(payload))

    def test_chunk_golden(self):
        self.check("chunk.golden")

    def test_chunk_reserved_golden(self):
        self.check("chunk_reserved.golden")

    def test_chunk_header_len_other_than_64_is_refused(self):
        # Every payload byte of a chunk is file content, so a chunk has
        # no fixed body and its header_len is always 64.
        b = bytearray(golden("chunk.golden"))
        struct.pack_into("<H", b, 20, decoder.OBJECT_FIXED_LEN + 8)
        struct.pack_into("<I", b, 56, decoder.crc32c(bytes(b[0:56])))
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.decode_object_bytes(bytes(b), "t")
        self.assertIn("header_len", str(cm.exception))


class BlobTest(unittest.TestCase):
    ID1 = bytes(i for i in range(32))
    ID2 = bytes((i * 7 + 3) % 256 for i in range(32))

    def check(self, name):
        obj = decoder.decode_object_bytes(golden(name), "t")
        self.assertEqual(obj.common["magic_kind"], decoder.MAGIC_BLOB)
        blob = decoder.parse_blob(obj.payload, "t", obj)
        self.assertEqual(blob["entry_count"], 2)
        self.assertEqual(len(blob["entries"]), 2)
        self.assertEqual(blob["entries"][0]["content_id"], self.ID1.hex())
        self.assertEqual(blob["entries"][0]["length"], 4096)
        self.assertEqual(blob["entries"][1]["content_id"], self.ID2.hex())
        self.assertEqual(blob["entries"][1]["length"], 2048)
        # An entry stores no offset: the offset of an entry is the sum of
        # the lengths before it, and the file size is the whole sum.
        self.assertEqual(blob["entries"][0]["file_offset"], 0)
        self.assertEqual(blob["entries"][1]["file_offset"], 4096)
        self.assertEqual(blob["size"], 4096 + 2048)

    def test_blob_golden(self):
        self.check("blob.golden")

    def test_blob_reserved_golden(self):
        self.check("blob_reserved.golden")

    def test_blob_hdrlen_golden(self):
        self.check("blob_hdrlen.golden")


class TreeTest(unittest.TestCase):
    def check(self, name):
        obj = decoder.decode_object_bytes(golden(name), "t")
        self.assertEqual(obj.common["magic_kind"], decoder.MAGIC_TREE)
        tree = decoder.parse_tree(obj.payload, "t", obj)
        self.assertEqual(tree["entry_count"], 2)
        # Tree entries are sorted ascending by name bytes, a directory
        # compared with a trailing '/'; "docs/" sorts before "file.txt".
        docs, filetxt = tree["entries"]
        self.assertEqual(docs["name"], b"docs")
        self.assertEqual(docs["entry_type"], 2)
        self.assertEqual(docs["mode"], 0o755)
        self.assertEqual(docs["uid"], 1000)
        self.assertEqual(docs["gid"], 1000)
        self.assertEqual(docs["mtime_sec"], 1700000000)
        self.assertEqual(docs["content_id"], "aa" * 32)

        self.assertEqual(filetxt["name"], b"file.txt")
        self.assertEqual(filetxt["entry_type"], 1)
        self.assertEqual(filetxt["size"], 12345)
        self.assertEqual(filetxt["mode"], 0o644)
        self.assertEqual(filetxt["mtime_sec"], 1700000001)
        self.assertEqual(filetxt["mtime_nsec"], 500)
        self.assertEqual(filetxt["content_id"], "bb" * 32)
        self.assertTrue(filetxt["entry_flags"] & decoder.ENTRY_FLAG_CTIME_ABSENT)
        self.assertEqual(len(filetxt["tlvs"]), 1)
        self.assertEqual(filetxt["tlvs"][0]["type"], decoder.TLV_USER_NAME)
        self.assertEqual(filetxt["tlvs"][0]["payload"], b"alice")

    def test_tree_golden(self):
        self.check("tree.golden")

    def test_tree_reserved_golden(self):
        self.check("tree_reserved.golden")

    def test_tree_hdrlen_golden(self):
        self.check("tree_hdrlen.golden")

    def test_wrong_entry_len_is_refused(self):
        obj = decoder.decode_object_bytes(golden("tree.golden"), "t")
        payload = bytearray(obj.payload)
        entry_len = struct.unpack_from("<I", payload, 8)[0]
        struct.pack_into("<I", payload, 8, entry_len + 8)
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.parse_tree(bytes(payload), "t", obj)
        self.assertIn("entry_len", str(cm.exception))

    def test_wrong_content_len_is_refused(self):
        obj = decoder.decode_object_bytes(golden("tree.golden"), "t")
        payload = bytearray(obj.payload)
        struct.pack_into("<I", payload, 8 + 60, 16)  # a directory needs 32
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.parse_tree(bytes(payload), "t", obj)
        self.assertIn("content_len", str(cm.exception))


class SnapshotTest(unittest.TestCase):
    def check(self, name):
        buf = golden(name)
        obj, payload = snapshot_payload(buf)
        self.assertEqual(obj.common["magic_kind"], decoder.MAGIC_SNAPSHOT)
        snap = decoder.parse_snapshot(payload, "t", obj)
        self.assertEqual(snap["root_tree"], bytes(i + 1 for i in range(32)).hex())
        self.assertEqual(snap["parent"], "00" * 32)
        self.assertEqual(snap["time_sec"], 1700000000)
        self.assertEqual(snap["time_nsec"], 123456789)
        self.assertEqual(snap["tz_offset_sec"], 3600)
        self.assertEqual(snap["total_size"], 123456)
        self.assertEqual(len(snap["meta"]), 2)
        self.assertEqual(snap["meta"][0]["tag"], 1)  # author
        self.assertEqual(snap["meta"][0]["value"], b"date")
        self.assertEqual(snap["meta"][1]["tag"], 2)  # host
        self.assertEqual(snap["meta"][1]["value"], b"host1")

    def test_snapshot_golden(self):
        self.check("snapshot.golden")

    def test_snapshot_reserved_golden(self):
        self.check("snapshot_reserved.golden")

    def test_snapshot_hdrlen_golden(self):
        self.check("snapshot_hdrlen.golden")

    def test_unknown_critical_meta_tag_is_refused(self):
        buf = bytearray(golden("snapshot.golden"))
        struct.pack_into("<H", buf, decoder.OBJECT_FIXED_LEN + 112, 0x8002)
        obj, payload = snapshot_payload(bytes(buf))
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.parse_snapshot(payload, "t", obj)
        self.assertIn("0x8002", str(cm.exception))


class DiscTest(unittest.TestCase):
    def check(self, name):
        d = decoder.parse_disc(golden(name), "t")
        self.assertEqual(d["disc_uuid"], bytes(i + 1 for i in range(16)))
        self.assertEqual(d["repo_uuid"], bytes(i + 101 for i in range(16)))
        self.assertEqual(d["disc_seq"], 0)
        self.assertEqual(d["capacity_sectors"], 12219392)
        self.assertEqual(d["created_sec"], 1700000000)
        self.assertEqual(d["created_nsec"], 500000000)
        self.assertEqual(d["tz_offset_sec"], -25200)
        self.assertEqual(d["label_len"], 14)
        self.assertEqual(d["label"], b"Archive Disc 1")
        self.assertEqual(d["tool_version"], 0x01000001)

    def test_disc_golden(self):
        self.check("disc.golden")

    def test_disc_reserved_golden(self):
        self.check("disc_reserved.golden")


class RunTest(unittest.TestCase):
    def check(self, name):
        r = decoder.parse_run(golden(name), "t")
        self.assertEqual(r["disc_uuid"], bytes(i + 1 for i in range(16)))
        self.assertEqual(r["repo_uuid"], bytes(i + 101 for i in range(16)))
        self.assertEqual(r["run_seq"], 1)
        self.assertEqual(r["disc_seq"], 0)
        self.assertEqual(r["fec_k"], 231)
        self.assertEqual(r["fec_m"], 23)
        self.assertEqual(r["fec_scheme"], 1)
        self.assertEqual(r["hash_algo"], 0x12)
        self.assertEqual(r["index_bytes"], 65536)
        self.assertEqual(r["index_hash"], bytes(i + 1 for i in range(32)))
        self.assertEqual(r["stream_bytes"], 471859200)
        self.assertEqual(r["created_sec"], 1700000000)
        self.assertEqual(r["created_nsec"], 500000000)
        self.assertEqual(r["tool_version"], 0x01000001)

    def test_run_golden(self):
        self.check("run.golden")

    def test_run_reserved_golden(self):
        self.check("run_reserved.golden")

    def test_fec_geometry_is_derived(self):
        r = decoder.parse_run(golden("run.golden"), "t")
        geo = decoder.fec_geometry(r)
        self.assertEqual(geo["stream_blocks"], 471859200 // 2048)
        self.assertEqual(geo["column_blocks"], -(-geo["stream_blocks"] // 231))


class IndexTest(unittest.TestCase):
    @staticmethod
    def file_hash_n(n):
        return bytes((n * 3 + i) % 256 for i in range(32))

    @staticmethod
    def content_id_n(n):
        return bytes((n * 7 + i * 2) % 256 for i in range(32))

    @staticmethod
    def disc_uuid_n(n):
        return bytes((n * 11 + i) % 256 for i in range(16))

    def check(self, name):
        idx = decoder.parse_index(golden(name), "t")
        self.assertEqual(idx["run_seq"], 7)
        self.assertEqual(idx["file_count"], 10)
        self.assertEqual(idx["object_count"], 4)
        self.assertEqual(idx["prereq_count"], 2)

        self.assertEqual(idx["files"][0]["role"], decoder.ROLE_INDEX)
        self.assertEqual(idx["files"][0]["byte_len"], 8192)
        self.assertEqual(idx["files"][0]["file_hash"], b"\0" * 32)
        self.assertEqual(idx["files"][2]["role"], decoder.ROLE_DISC)
        self.assertEqual(idx["files"][2]["file_hash"], self.file_hash_n(1))
        self.assertEqual(idx["files"][3]["role"], decoder.ROLE_REFS)
        self.assertEqual(idx["files"][4]["role"], decoder.ROLE_DISCS)
        self.assertEqual(idx["files"][9]["role"], decoder.ROLE_RUN2)

        # The j-th role 13 row pairs with Objects row j, by position.
        self.assertEqual(len(idx["object_rows"]), idx["object_count"])
        self.assertEqual(idx["object_rows"][0]["byte_len"], 4160)
        for n, row in enumerate(idx["objects"], start=1):
            self.assertEqual(row["content_id"], self.content_id_n(n).hex())
            self.assertEqual(row["kind"], n)

        self.assertEqual(idx["prereqs"][0]["content_id"], self.content_id_n(5).hex())
        self.assertEqual(idx["prereqs"][0]["disc_uuid"], self.disc_uuid_n(1))
        self.assertEqual(idx["prereqs"][1]["disc_uuid"], self.disc_uuid_n(2))

    def test_index_golden(self):
        self.check("index.golden")

    def test_index_reserved_golden(self):
        self.check("index_reserved.golden")

    def test_index_hdrlen_golden(self):
        self.check("index_hdrlen.golden")

    def test_row_counts_must_fill_the_file(self):
        b = bytearray(golden("index.golden"))
        struct.pack_into("<I", b, 44, 5)  # object_count, one too many
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.parse_index(bytes(b), "t")
        self.assertIn("bytes", str(cm.exception))

    def test_header_len_below_the_known_fixed_part_is_refused(self):
        b = bytearray(golden("index.golden"))
        struct.pack_into("<H", b, 20, decoder.INDEX_HEADER_LEN - 8)
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.parse_index(bytes(b), "t")
        self.assertIn("header_len", str(cm.exception))


class RefsTest(unittest.TestCase):
    @staticmethod
    def snapshot_id_n(n):
        return bytes((n * 5 + i) % 256 for i in range(32))

    def check(self, name):
        t = decoder.parse_refs(golden(name), "t")
        self.assertEqual(t["repo_uuid"], bytes(range(16)))
        self.assertEqual(t["record_count"], 2)
        r0, r1 = t["records"]
        self.assertEqual(r0["snapshot_id"], self.snapshot_id_n(1).hex())
        self.assertEqual(r0["time_sec"], 1700000000)
        self.assertEqual(r0["time_nsec"], 123456)
        self.assertEqual(r0["name"], "LATEST")
        self.assertEqual(r1["snapshot_id"], self.snapshot_id_n(2).hex())
        self.assertEqual(r1["time_sec"], 1700000100)
        self.assertEqual(r1["name"], "daily")

    def test_refs_golden(self):
        self.check("refs.golden")

    def test_refs_reserved_golden(self):
        self.check("refs_reserved.golden")

    def test_refs_hdrlen_golden(self):
        self.check("refs_hdrlen.golden")

    def test_latest_ref(self):
        t = decoder.parse_refs(golden("refs.golden"), "t")
        r = decoder.latest_ref(t["records"], "LATEST")
        self.assertEqual(r["snapshot_id"], self.snapshot_id_n(1).hex())
        self.assertIsNone(decoder.latest_ref(t["records"], "nonexistent"))

    def test_newest_ref_is_the_highest_time_then_snapshot_id(self):
        records = [
            {"name": "LATEST", "time_sec": 5, "time_nsec": 0, "snapshot_id": "ff" * 32},
            {"name": "LATEST", "time_sec": 5, "time_nsec": 7, "snapshot_id": "00" * 32},
            {"name": "LATEST", "time_sec": 5, "time_nsec": 7, "snapshot_id": "01" * 32},
            {"name": "LATEST", "time_sec": 4, "time_nsec": 9, "snapshot_id": "ee" * 32},
        ]
        self.assertEqual(decoder.latest_ref(records, "LATEST")["snapshot_id"], "01" * 32)


class DiscsTest(unittest.TestCase):
    @staticmethod
    def disc_uuid_n(n):
        return bytes((n * 11 + i) % 256 for i in range(16))

    @staticmethod
    def run_hash_n(n):
        return bytes((n * 13 + i) % 256 for i in range(32))

    def check(self, name):
        t = decoder.parse_discs(golden(name), "t")
        self.assertEqual(t["repo_uuid"], bytes(16 + i for i in range(16)))
        self.assertEqual(t["record_count"], 2)
        row0, row1 = t["rows"]
        self.assertEqual(row0["run_seq"], 1)
        self.assertEqual(row0["disc_seq"], 0)
        self.assertEqual(row0["disc_uuid"], self.disc_uuid_n(1))
        self.assertEqual(row0["run_hash"], self.run_hash_n(1))
        self.assertEqual(row0["created_sec"], 1700000000)
        self.assertEqual(row0["capacity_sectors"], 12219392)
        self.assertEqual(row0["label"], "BACKUP01")
        # The row of the disc that carries the table holds an all-zero
        # run_hash: that header is written after the table.
        self.assertEqual(row1["disc_uuid"], self.disc_uuid_n(2))
        self.assertEqual(row1["run_hash"], b"\x00" * 32)
        self.assertEqual(row1["label"], "BACKUP02")

    def test_discs_golden(self):
        self.check("discs.golden")

    def test_discs_reserved_golden(self):
        self.check("discs_reserved.golden")

    def test_discs_hdrlen_golden(self):
        self.check("discs_hdrlen.golden")


class ChecksumTest(unittest.TestCase):
    def check(self, name):
        r = decoder.parse_checksum_block(golden(name), "t")
        self.assertEqual(r["stripe_index"], 5)
        self.assertEqual(r["digest_count"], 3)
        self.assertEqual(len(r["digests"]), 3)
        for i, d in enumerate(r["digests"]):
            self.assertEqual(d, bytes([i + 1]) * 8)

    def test_checksum_golden(self):
        self.check("checksum.golden")

    def test_checksum_reserved_golden(self):
        self.check("checksum_reserved.golden")


# ---------------------------------------------------------------------------
# Whole-tree fixtures.


def find_entry(repo, snapshot, want_name):
    """The tree entry of the first file named want_name in a snapshot."""
    report = decoder.Report()
    found = []

    def visit(path, entry):
        if entry["name"] == want_name:
            found.append(entry)

    decoder.walk_tree(repo, snapshot["root_tree"], "", report, visit)
    if not found:
        raise AssertionError(f"no entry named {want_name!r} in the fixture")
    return found[0]


def chunk_path(fixture_root, name=b"hello.txt"):
    """The path of the one chunk object that holds the file named name.
    Found by walking the snapshot, so no object id is hard-coded."""
    repo = decoder.Repo(fixture_root)
    snap = repo.read_snapshot("LATEST")
    entry = find_entry(repo, snap, name)
    chunks = list(decoder.iter_file_chunks(repo, entry["content_id"], decoder.Report()))
    assert len(chunks) == 1, "the fixture file must be one chunk"
    return decoder.object_path(repo.root, chunks[0][0], repo.names)


class Scheme0FixtureTest(unittest.TestCase):
    """A run that carries no FEC: no checksum.bin, no parity directory.
    Verify must pass by content id and file hash alone."""

    def test_run_header_fec_scheme_is_none(self):
        repo = decoder.Repo(SCHEME0_FIXTURE)
        run = repo.run_header(repo.newest_seq)
        self.assertEqual(run["fec_scheme"], 0)
        self.assertIsNone(decoder.fec_geometry(run))

    def test_no_checksum_or_parity_files(self):
        names = decoder.NameCache()
        d = decoder.run_dir(os.path.join(SCHEME0_FIXTURE, "NOAHSARK"), 1, names)
        self.assertFalse(os.path.exists(os.path.join(d, "checksum.bin")))
        self.assertFalse(os.path.exists(os.path.join(d, "parity")))

    def test_verify_passes_by_content_id_and_file_hash_alone(self):
        args = types.SimpleNamespace(disc_root=[SCHEME0_FIXTURE])
        self.assertEqual(decoder.cmd_verify(args), 0)

    def test_snapshot_objects_live_under_snapshots(self):
        repo = decoder.Repo(SCHEME0_FIXTURE)
        names = repo.snapshot_names()
        self.assertEqual(len(names), 1)
        self.assertEqual(len(names[0]), 68)
        ref = decoder.latest_ref(repo.refs()["records"], "LATEST")
        self.assertEqual(decoder.text_form(ref["snapshot_id"]), names[0])


def fold_tree_to_lower(src: str, dst: str):
    """Copies every file and directory under src into dst, folding every
    path segment to lowercase. This mirrors what plain ISO 9660 level 4,
    with no Rock Ridge, does to every name on a real burn."""
    for root, _dirs, files in os.walk(src):
        rel = os.path.relpath(root, src)
        target_root = (
            dst if rel == "." else os.path.join(dst, *(p.lower() for p in rel.split(os.sep)))
        )
        os.makedirs(target_root, exist_ok=True)
        for name in files:
            shutil.copyfile(os.path.join(root, name), os.path.join(target_root, name.lower()))


class CaseFoldedFixtureTest(unittest.TestCase):
    """A plain ISO 9660 level 4 image with no Rock Ridge folds every
    fixed name to lowercase: NOAHSARK becomes noahsark, DISC.bin becomes
    disc.bin, and so on. The decoder accepts that layout and agrees with
    the original, correctly-cased tree."""

    def setUp(self):
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp, ignore_errors=True)
        self.folded = os.path.join(tmp, "folded")
        fold_tree_to_lower(SCHEME0_FIXTURE, self.folded)

    def test_repo_finds_root_and_run(self):
        want = decoder.Repo(SCHEME0_FIXTURE)
        got = decoder.Repo(self.folded)
        self.assertEqual(got.run_seqs, want.run_seqs)
        self.assertEqual(got.disc["disc_uuid"], want.disc["disc_uuid"])
        self.assertEqual(got.snapshot_names(), want.snapshot_names())

    def test_verify_passes_on_folded_tree(self):
        args = types.SimpleNamespace(disc_root=[self.folded])
        self.assertEqual(decoder.cmd_verify(args), 0)

    def test_list_matches_original_tree(self):
        def listing(root):
            args = types.SimpleNamespace(disc_root=[root], snapshot="LATEST")
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                code = decoder.cmd_list(args)
            self.assertEqual(code, 0)
            return buf.getvalue()

        self.assertEqual(listing(self.folded), listing(SCHEME0_FIXTURE))


class DescribeMissingObjectTest(unittest.TestCase):
    """describe_missing_object is the message helper restore_file uses to
    add Prereqs and DISCS context. A Prereqs row names the disc by uuid,
    never by a run number, so the lookup in DISCS is by uuid."""

    UUID = bytes(range(16))

    def fake_set(self, prereqs, rows):
        """A stand-in for DiscSet with one disc that was given and one
        disc, named only by a Prereqs row, that was not."""
        repo = types.SimpleNamespace(
            disc={"disc_uuid": b"\x99" * 16},
            newest_seq=0,
            index=lambda seq: {"prereqs": prereqs},
            discs=lambda: {"rows": rows},
        )
        s = types.SimpleNamespace(repos=[repo])
        s.given_uuids = lambda: {repo.disc["disc_uuid"]}
        s.other_disc = lambda cid: decoder.DiscSet.other_disc(s, cid)
        s.ungiven_discs = lambda: decoder.DiscSet.ungiven_discs(s)
        return s

    def test_names_disc_number_label_and_uuid(self):
        cid = "aa" * 32
        rows = [{"disc_uuid": self.UUID, "disc_seq": 3, "label": "BACKUP07"}]
        msg = decoder.describe_missing_object(
            cid, self.fake_set([{"content_id": cid, "disc_uuid": self.UUID}], rows)
        )
        self.assertIn(self.UUID.hex(), msg)
        self.assertIn("BACKUP07", msg)
        self.assertIn("disc 3", msg)

    def test_names_disc_uuid_alone_when_discs_has_no_row(self):
        cid = "bb" * 32
        msg = decoder.describe_missing_object(
            cid, self.fake_set([{"content_id": cid, "disc_uuid": self.UUID}], [])
        )
        self.assertIn(self.UUID.hex(), msg)

    def test_empty_when_no_prereq_row_and_no_other_disc(self):
        self.assertEqual(
            decoder.describe_missing_object("dd" * 32, self.fake_set([], [])), ""
        )


class SafeDestTest(unittest.TestCase):
    """safe_dest is restore's last line of defense against a decoded path
    that would land outside --out; unescape_root_name can turn a root
    entry name that passed tree entry validation into one holding '/' or
    '..' segments."""

    def test_normal_relative_path_joins_under_out(self):
        report = decoder.Report()
        self.assertEqual(
            decoder.safe_dest("/out", "a/b.txt", report), os.path.abspath("/out/a/b.txt")
        )
        self.assertTrue(report.ok())

    def test_leading_slash_is_relative_to_out(self):
        report = decoder.Report()
        dest = decoder.safe_dest("/out", "/home/user/src/file.txt", report)
        self.assertEqual(dest, os.path.abspath("/out/home/user/src/file.txt"))
        self.assertTrue(report.ok())

    def test_dotdot_is_refused(self):
        report = decoder.Report()
        self.assertIsNone(decoder.safe_dest("/out", "../../etc/passwd", report))
        self.assertFalse(report.ok())

    def test_escaped_slash_in_root_name_cannot_escape_out(self):
        name = decoder.unescape_root_name(b"a%2F..%2F..%2Fetc%2Fpasswd")
        report = decoder.Report()
        self.assertIsNone(decoder.safe_dest("/out", name, report))
        self.assertFalse(report.ok())


class RootNameEscapeTest(unittest.TestCase):
    def test_examples_of_the_escape_rule(self):
        self.assertEqual(decoder.unescape_root_name(b"%2Fsrv%2Fdata"), "/srv/data")
        self.assertEqual(decoder.unescape_root_name(b"%2Fa%25b%2Fc"), "/a%b/c")
        self.assertEqual(decoder.unescape_root_name(b"%2Fx%5Cy"), "/x\\y")

    def test_lowercase_digits_are_accepted(self):
        self.assertEqual(decoder.unescape_root_name(b"%2fsrv"), "/srv")


class CopiedFixture(unittest.TestCase):
    """Copies a fixture into a temporary tree so a test can damage it."""

    FIXTURE = TWO_FILES_FIXTURE

    def setUp(self):
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp, ignore_errors=True)
        self.root = os.path.join(tmp, "disc")
        shutil.copytree(self.FIXTURE, self.root)
        self.out = os.path.join(tmp, "out")

    def restore(self):
        args = types.SimpleNamespace(disc_root=[self.root], snapshot="LATEST", out=self.out)
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            code = decoder.cmd_restore(args)
        return code, err.getvalue()

    def restored_files(self):
        found = {}
        for dirpath, _dirs, files in os.walk(self.out):
            for name in files:
                found[name] = os.path.join(dirpath, name)
        return found

    def assert_no_partial_file(self):
        for _dirpath, _dirs, files in os.walk(self.out):
            for name in files:
                self.assertFalse(name.endswith(".noahsark-partial"), f"stray partial: {name}")


class RestoreContinuesPastMissingObjectTest(CopiedFixture):
    """The fixture carries two independent files; removing one's chunk
    object must not stop the other from restoring."""

    def setUp(self):
        super().setUp()
        os.remove(chunk_path(self.root))

    def test_other_file_restored_missing_one_reported_nonzero_exit(self):
        code, err = self.restore()
        self.assertEqual(code, 1)
        self.assertIn("hello.txt", err)
        found = self.restored_files()
        self.assertNotIn("hello.txt", found, "a failed file must not be left behind at all")
        self.assertIn("second.txt", found)
        with open(found["second.txt"], "rb") as f:
            self.assertEqual(f.read(), b"second file content here\n")
        self.assert_no_partial_file()


class DamagedObjectFailsItsFileTest(CopiedFixture):
    """A bad object fails its own file and the restore continues. The
    damaged bytes never reach the output: a reader verifies the content
    id before it returns object bytes."""

    FIXTURE = FEC_FIXTURE

    def setUp(self):
        super().setUp()
        self.damaged = chunk_path(self.root)
        with open(self.damaged, "r+b") as f:
            f.seek(decoder.OBJECT_FIXED_LEN)
            first = f.read(1)
            f.seek(decoder.OBJECT_FIXED_LEN)
            f.write(bytes([first[0] ^ 0xFF]))

    def test_fixture_carries_parity(self):
        repo = decoder.Repo(self.root)
        run = repo.run_header(repo.newest_seq)
        self.assertEqual(run["fec_scheme"], 1)
        self.assertEqual((run["fec_k"], run["fec_m"]), (231, 23))
        geo = decoder.fec_geometry(run)
        names = decoder.NameCache()
        d = decoder.run_dir(repo.root, repo.newest_seq, names)
        self.assertEqual(
            os.path.getsize(os.path.join(d, "checksum.bin")),
            geo["column_blocks"] * decoder.BLOCK_SIZE,
        )
        parity = sorted(os.listdir(os.path.join(d, "parity")))
        self.assertEqual(len(parity), run["fec_m"])
        self.assertEqual(parity[0], "p0232.bin")

    def test_verify_names_the_damaged_object_and_says_it_cannot_repair(self):
        args = types.SimpleNamespace(disc_root=[self.root])
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = decoder.cmd_verify(args)
        self.assertEqual(code, 1)
        self.assertIn(os.path.basename(self.damaged), err.getvalue())
        self.assertIn("content id mismatch", err.getvalue())
        # The run has parity, and this decoder holds no Reed-Solomon
        # code: it must say so rather than look repaired.
        self.assertIn("cannot repair", out.getvalue())

    def test_restore_fails_that_file_and_keeps_the_other(self):
        code, err = self.restore()
        self.assertEqual(code, 1)
        self.assertIn("hello.txt", err)
        found = self.restored_files()
        self.assertNotIn("hello.txt", found)
        self.assertIn("second.txt", found)
        self.assert_no_partial_file()


class TwoDiscsTest(unittest.TestCase):
    """restore, verify and list take several disc roots. An object that
    another given root holds is found there."""

    def setUp(self):
        self.out = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.out, ignore_errors=True)

    def run_cmd(self, func, **kw):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = func(types.SimpleNamespace(**kw))
        return code, out.getvalue(), err.getvalue()

    def restored_files(self):
        found = set()
        for _, _, files in os.walk(self.out):
            found.update(files)
        return found

    def test_restore_across_two_roots(self):
        code, _, err = self.run_cmd(
            decoder.cmd_restore,
            disc_root=[DISC1, DISC0],
            snapshot="second",
            out=self.out,
        )
        self.assertEqual(code, 0, err)
        self.assertEqual(self.restored_files(), {"one.txt", "two.txt"})

    def test_restore_from_one_root_alone_names_the_other_disc(self):
        code, _, err = self.run_cmd(
            decoder.cmd_restore, disc_root=[DISC1], snapshot="second", out=self.out
        )
        self.assertEqual(code, 1)
        self.assertIn("which was not given", err)
        self.assertIn("disc 0", err)

    def test_verify_notes_a_disc_that_was_not_given(self):
        code, _, err = self.run_cmd(decoder.cmd_verify, disc_root=[DISC1])
        self.assertEqual(code, 0, err)
        self.assertIn("NOTE", err)
        self.assertNotIn("FAIL", err)
        self.assertIn("disc 0", err)

    def test_verify_of_both_roots_notes_nothing(self):
        code, out, err = self.run_cmd(decoder.cmd_verify, disc_root=[DISC0, DISC1])
        self.assertEqual(code, 0, err)
        self.assertNotIn("NOTE", err)
        self.assertIn("all checks passed", out)

    def test_verify_still_fails_a_damaged_object(self):
        work = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, work, ignore_errors=True)
        copy0 = os.path.join(work, "disc0")
        copy1 = os.path.join(work, "disc1")
        shutil.copytree(DISC0, copy0)
        shutil.copytree(DISC1, copy1)
        victim = None
        for base, _, files in os.walk(os.path.join(copy0, "NOAHSARK", "objects")):
            for name in files:
                victim = os.path.join(base, name)
        self.assertIsNotNone(victim)
        with open(victim, "r+b") as f:
            f.seek(-1, os.SEEK_END)
            last = f.read(1)
            f.seek(-1, os.SEEK_END)
            f.write(bytes([last[0] ^ 0xFF]))
        code, _, err = self.run_cmd(decoder.cmd_verify, disc_root=[copy0, copy1])
        self.assertEqual(code, 1)
        self.assertIn("FAIL", err)

    def test_list_reads_one_snapshot_across_two_roots(self):
        code, out, err = self.run_cmd(
            decoder.cmd_list, disc_root=[DISC1, DISC0], snapshot="second"
        )
        self.assertEqual(code, 0, err)
        self.assertIn("one.txt", out)
        self.assertIn("two.txt", out)


if __name__ == "__main__":
    unittest.main()
