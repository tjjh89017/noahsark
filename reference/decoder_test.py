"""Unit tests for decoder.py against the checked-in golden files.

Each golden file under internal/format/testdata is a byte-exact encoding of
a Go test fixture (the matching *_test.go file in internal/format). This
test decodes every golden file with decoder.py and checks the decoded
fields against the same values the Go fixture used.

Run with:
    python3 -m unittest reference/decoder_test.py
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

TESTDATA = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "..", "internal", "format", "testdata"
)

# SCHEME0_FIXTURE is a whole run tree, one snapshot of one small file,
# built by internal/image.Build with FECEnabled false: fec_scheme 0, no
# checksum.bin, no parity directory. See NOTES.md's change log entry for
# document version 0.4.4.
SCHEME0_FIXTURE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "testdata", "scheme0-fixture")

# TWO_FILES_FIXTURE is a whole run tree, one snapshot of two regular files
# in a subdirectory, built with pack --no-fec. Used for restore tests that
# need more than one file, so restoring past a missing one still leaves
# something to check.
TWO_FILES_FIXTURE = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "testdata", "two-files-fixture"
)


def golden(name: str) -> bytes:
    with open(os.path.join(TESTDATA, name), "rb") as f:
        return f.read()


class CommonHeaderTest(unittest.TestCase):
    def test_common_header_golden(self):
        h = decoder.decode_common_header(golden("common_header.golden"), "t")
        self.assertEqual(h["magic_kind"], decoder.MAGIC_CHUNK)
        self.assertEqual(h["version_major"], 1)
        self.assertEqual(h["version_minor"], 0)
        self.assertEqual(h["header_len"], decoder.COMMON_HEADER_LEN + decoder.OBJECT_HEADER_LEN)


class ObjectHeaderTest(unittest.TestCase):
    def test_object_header_golden(self):
        f = decoder.object_header_fields(golden("object_header.golden"))
        self.assertEqual(f["kind"], 1)  # ObjectKindChunk
        self.assertEqual(f["hash_algo"], 0x12)  # HashAlgoSHA256
        self.assertEqual(f["digest_len"], 32)
        self.assertEqual(f["compression"], 1)  # CompressionZstd
        self.assertEqual(f["crypto"], 0)
        self.assertEqual(f["payload_len"], 1024)
        self.assertEqual(f["stored_len"], 512)
        self.assertEqual(f["header_crc32c"], 0xDEADBEEF)


class TLVTest(unittest.TestCase):
    def test_tlv_golden(self):
        tlvs = decoder.parse_tlvs(golden("tlv.golden"), "t")
        self.assertEqual(len(tlvs), 1)
        self.assertEqual(tlvs[0]["type"], 0x0003)  # TLVTypeGroupName
        self.assertEqual(tlvs[0]["flags"], 0)
        self.assertEqual(tlvs[0]["payload"], b"staff")


class ChunkTest(unittest.TestCase):
    def test_chunk_golden(self):
        obj = decoder.decode_object_bytes(golden("chunk.golden"), "t")
        self.assertEqual(obj.common["magic_kind"], decoder.MAGIC_CHUNK)
        self.assertEqual(obj.obj_header["kind"], 1)
        self.assertEqual(obj.obj_header["compression"], 0)
        payload = b"hello world chunk payload"
        self.assertEqual(obj.payload, payload)
        self.assertEqual(obj.obj_header["payload_len"], len(payload))
        self.assertEqual(obj.obj_header["stored_len"], len(payload))


class BlobTest(unittest.TestCase):
    def test_blob_golden(self):
        obj = decoder.decode_object_bytes(golden("blob.golden"), "t")
        self.assertEqual(obj.common["magic_kind"], decoder.MAGIC_BLOB)
        blob = decoder.parse_blob(obj.payload, "t")
        self.assertEqual(blob["entry_count"], 2)
        self.assertEqual(blob["total_size"], 4096 + 2048)
        self.assertEqual(blob["entry_size"], 48)
        self.assertEqual(blob["level"], 0)
        self.assertEqual(len(blob["entries"]), 2)
        id1 = bytes(i for i in range(32))
        id2 = bytes((i * 7 + 3) % 256 for i in range(32))
        self.assertEqual(blob["entries"][0]["content_id"], id1.hex())
        self.assertEqual(blob["entries"][0]["length"], 4096)
        self.assertEqual(blob["entries"][0]["file_offset"], 0)
        self.assertEqual(blob["entries"][1]["content_id"], id2.hex())
        self.assertEqual(blob["entries"][1]["length"], 2048)
        self.assertEqual(blob["entries"][1]["file_offset"], 4096)


class TreeTest(unittest.TestCase):
    def test_tree_golden(self):
        obj = decoder.decode_object_bytes(golden("tree.golden"), "t")
        self.assertEqual(obj.common["magic_kind"], decoder.MAGIC_TREE)
        tree = decoder.parse_tree(obj.payload, "t")
        self.assertEqual(tree["entry_count"], 2)
        entries = tree["entries"]
        self.assertEqual(len(entries), 2)

        # Tree entries are sorted ascending by name bytes, a directory
        # compared with a trailing '/'; "docs/" sorts before "file.txt".
        docs, filetxt = entries
        self.assertEqual(docs["name"], b"docs")
        self.assertEqual(docs["entry_type"], 2)  # EntryTypeDirectory
        self.assertEqual(docs["mode"], 0o755)
        self.assertEqual(docs["uid"], 1000)
        self.assertEqual(docs["gid"], 1000)
        self.assertEqual(docs["mtime_sec"], 1700000000)
        self.assertEqual(docs["content_id"], ("aa" * 32))

        self.assertEqual(filetxt["name"], b"file.txt")
        self.assertEqual(filetxt["entry_type"], 1)
        self.assertEqual(filetxt["size"], 12345)
        self.assertEqual(filetxt["mode"], 0o644)
        self.assertEqual(filetxt["mtime_sec"], 1700000001)
        self.assertEqual(filetxt["mtime_nsec"], 500)
        self.assertEqual(filetxt["content_id"], ("bb" * 32))
        self.assertTrue(filetxt["entry_flags"] & decoder.ENTRY_FLAG_ATIME_ABSENT)
        self.assertTrue(filetxt["entry_flags"] & decoder.ENTRY_FLAG_CTIME_ABSENT)
        self.assertTrue(filetxt["entry_flags"] & decoder.ENTRY_FLAG_BTIME_ABSENT)
        self.assertEqual(len(filetxt["tlvs"]), 1)
        self.assertEqual(filetxt["tlvs"][0]["type"], 0x0002)  # USER_NAME
        self.assertEqual(filetxt["tlvs"][0]["payload"], b"alice")


class SnapshotTest(unittest.TestCase):
    def test_snapshot_golden(self):
        # The snapshot_test.go fixture leaves ObjectHeader.PayloadLen and
        # StoredLen at zero: it round-trips the fixed body and the meta
        # records only, not the payload-length fields a real writer fills
        # in (writeSnapshot in internal/object does). So this checks field
        # decoding directly on the payload bytes, the same slice a real
        # writer's stored_len would have named.
        buf = golden("snapshot.golden")
        common = decoder.decode_common_header(buf, "t")
        self.assertEqual(common["magic_kind"], decoder.MAGIC_SNAPSHOT)
        payload = buf[decoder.COMMON_HEADER_LEN + decoder.OBJECT_HEADER_LEN :]
        snap = decoder.parse_snapshot(payload, "t")
        root_tree = bytes(i + 1 for i in range(32))
        self.assertEqual(snap["root_tree"], root_tree.hex())
        self.assertEqual(snap["parent"], "00" * 32)
        self.assertEqual(snap["generation"], 1)
        self.assertEqual(snap["time_sec"], 1700000000)
        self.assertEqual(snap["time_nsec"], 123456789)
        self.assertEqual(snap["tz_offset_sec"], 3600)
        self.assertEqual(snap["total_size"], 123456)
        self.assertEqual(snap["reachable_object_count"], 42)
        self.assertEqual(snap["hash_algo"], 0x12)
        self.assertEqual(snap["chunker_profile"], 1)  # ChunkerProfileP3
        self.assertEqual(snap["source_type"], 1)  # SnapshotSourceLocal
        self.assertEqual(snap["parent_hash_algo"], 0)
        self.assertEqual(len(snap["meta"]), 2)
        self.assertEqual(snap["meta"][0]["tag"], 1)  # author
        self.assertEqual(snap["meta"][0]["value"], b"date")
        self.assertEqual(snap["meta"][1]["tag"], 2)  # host
        self.assertEqual(snap["meta"][1]["value"], b"host1")


class DiscTest(unittest.TestCase):
    def test_disc_golden(self):
        d = decoder.parse_disc(golden("disc.golden"), "t")
        self.assertEqual(d["disc_uuid"], bytes(i + 1 for i in range(16)))
        self.assertEqual(d["repo_uuid"], bytes(i + 101 for i in range(16)))
        self.assertEqual(d["disc_seq"], 0)
        self.assertEqual(d["capacity_sectors"], 12219392)
        self.assertEqual(d["capacity_forced_sectors"], 12000000)
        self.assertEqual(d["created_sec"], 1700000000)
        self.assertEqual(d["created_nsec"], 500000000)
        self.assertEqual(d["tz_offset_sec"], -25200)
        self.assertEqual(d["media_type"], 1)  # MediaTypeBDRSL25GB
        self.assertEqual(d["fs_profile"], 0)  # DiscFSProfileOneshot
        self.assertEqual(d["fanout_levels"], 1)
        self.assertEqual(d["capacity_is_forced"], 1)
        self.assertEqual(d["sealed"], 1)
        self.assertEqual(d["label_len"], 14)
        self.assertEqual(d["label"], b"Archive Disc 1")
        self.assertEqual(d["tool_version"], 0x01000001)


class RunTest(unittest.TestCase):
    def test_run_golden(self):
        r = decoder.parse_run(golden("run.golden"), "t")
        self.assertEqual(r["disc_uuid"], bytes(i + 1 for i in range(16)))
        self.assertEqual(r["repo_uuid"], bytes(i + 101 for i in range(16)))
        self.assertEqual(r["run_seq"], 1)
        self.assertEqual(r["disc_seq"], 0)
        self.assertEqual(r["fec_k"], 231)
        self.assertEqual(r["fec_m"], 23)
        self.assertEqual(r["hash_algo"], 0x12)
        self.assertEqual(r["chunker_profile"], 1)
        self.assertEqual(r["compression"], 1)  # CompressionZstd
        self.assertEqual(r["run_kind"], 1)  # RunKindData
        self.assertEqual(r["run_flags"] & 1, 1)  # RunFlagClosing
        self.assertEqual(r["index_bytes"], 65536)
        self.assertEqual(r["index_hash"], bytes(i + 1 for i in range(32)))
        self.assertEqual(r["stream_bytes"], 471859200)
        self.assertEqual(r["created_sec"], 1700000000)
        self.assertEqual(r["created_nsec"], 500000000)
        self.assertEqual(r["tool_version"], 0x01000001)
        self.assertEqual(r["disc_object_count"], 1000)
        self.assertEqual(r["disc_run_index"], 0)


class IndexTest(unittest.TestCase):
    def test_index_golden(self):
        idx = decoder.parse_index(golden("index.golden"), "t")
        self.assertEqual(idx["run_seq"], 7)
        self.assertEqual(idx["file_count"], 2)
        self.assertEqual(idx["object_count"], 2)
        self.assertEqual(idx["prereq_count"], 1)
        self.assertEqual(idx["hash_algo"], 0x12)
        self.assertEqual(idx["digest_len"], 32)
        self.assertEqual(idx["container_len"], len(golden("index.golden")))

        def file_hash_n(n):
            return bytes((n * 3 + i) % 256 for i in range(32))

        def content_id_n(n):
            return bytes((n * 7 + i * 2) % 256 for i in range(32))

        self.assertEqual(idx["files"][0]["file_hash"], file_hash_n(1))
        self.assertEqual(idx["files"][0]["byte_len"], 8192)
        self.assertEqual(idx["files"][0]["role"], 1)  # FileRoleIndex
        self.assertEqual(idx["files"][1]["file_hash"], file_hash_n(2))
        self.assertEqual(idx["files"][1]["byte_len"], 512)
        self.assertEqual(idx["files"][1]["role"], 2)  # FileRoleRun

        self.assertEqual(idx["objects"][0]["content_id"], content_id_n(1).hex())
        self.assertEqual(idx["objects"][0]["kind"], 1)  # chunk
        self.assertEqual(idx["objects"][0]["compression"], 1)  # zstd
        self.assertEqual(idx["objects"][1]["content_id"], content_id_n(2).hex())
        self.assertEqual(idx["objects"][1]["kind"], 3)  # tree
        self.assertEqual(idx["objects"][1]["compression"], 0)  # none
        self.assertEqual(idx["objects"][1]["flags"], 2)

        self.assertEqual(idx["prereqs"][0]["content_id"], content_id_n(3).hex())
        self.assertEqual(idx["prereqs"][0]["run_seq"], 99)


class RefsTest(unittest.TestCase):
    def test_refs_golden(self):
        t = decoder.parse_refs(golden("refs.golden"), "t")
        self.assertEqual(t["repo_uuid"], bytes(range(16)))
        self.assertEqual(t["record_count"], 2)

        def snapshot_id_n(n):
            return bytes((n * 5 + i) % 256 for i in range(32))

        r0, r1 = t["records"]
        self.assertEqual(r0["snapshot_id"], snapshot_id_n(1).hex())
        self.assertEqual(r0["time_sec"], 1700000000)
        self.assertEqual(r0["time_nsec"], 123456)
        self.assertEqual(r0["name"], "LATEST")
        self.assertEqual(r0["run_seq"], 3)
        self.assertEqual(r1["snapshot_id"], snapshot_id_n(2).hex())
        self.assertEqual(r1["name"], "daily")
        self.assertEqual(r1["run_seq"], 4)

    def test_latest_ref(self):
        t = decoder.parse_refs(golden("refs.golden"), "t")
        r = decoder.latest_ref(t["records"], "LATEST")
        self.assertIsNotNone(r)
        self.assertEqual(r["run_seq"], 3)
        self.assertIsNone(decoder.latest_ref(t["records"], "nonexistent"))


class DiscsTest(unittest.TestCase):
    def test_discs_golden(self):
        t = decoder.parse_discs(golden("discs.golden"), "t")
        self.assertEqual(t["repo_uuid"], bytes(16 + i for i in range(16)))
        self.assertEqual(t["record_count"], 2)

        def disc_uuid_n(n):
            return bytes((n * 11 + i) % 256 for i in range(16))

        def run_hash_n(n):
            return bytes((n * 13 + i) % 256 for i in range(32))

        row0, row1 = t["rows"]
        self.assertEqual(row0["run_seq"], 1)
        self.assertEqual(row0["disc_seq"], 0)
        self.assertEqual(row0["disc_uuid"], disc_uuid_n(1))
        self.assertEqual(row0["run_hash"], run_hash_n(1))
        self.assertEqual(row0["created_sec"], 1700000000)
        self.assertEqual(row0["last_verify_sec"], 1700003600)
        self.assertEqual(row0["capacity_sectors"], 12219392)
        self.assertEqual(row0["used_sectors"], 500000)
        self.assertEqual(row0["run_status"], 1)  # verified
        self.assertEqual(row0["health"], 1)  # healthy
        self.assertEqual(row0["rs_margin_percent"], 95)
        self.assertEqual(row0["label"], "BACKUP01")
        self.assertEqual(row0["state_flags"] & 1, 1)  # closed

        self.assertEqual(row1["run_seq"], 2)
        self.assertEqual(row1["run_hash"], b"\x00" * 32)
        self.assertEqual(row1["run_status"], 2)  # burned, not yet verified
        self.assertEqual(row1["health"], 6)  # unverified, this disc


class ChecksumTest(unittest.TestCase):
    def test_checksum_golden(self):
        r = decoder.parse_checksum_record(golden("checksum.golden"), "t")
        self.assertEqual(r["stripe_index"], 5)
        self.assertEqual(r["digest_count"], 3)
        self.assertEqual(r["digest_bytes"], 8)
        self.assertEqual(r["hash_algo"], 0x12)
        self.assertEqual(len(r["digests"]), 3)
        for i, d in enumerate(r["digests"]):
            self.assertEqual(d, bytes([i + 1]) * 8)


class Scheme0FixtureTest(unittest.TestCase):
    """cmd_verify on a run that carries no FEC (fec_scheme 0): no
    checksum.bin, no parity directory. Verify must pass by content id and
    file hash alone, the same as it always has, since this decoder never
    performs a checksum-column or parity check of its own."""

    def test_run_header_fec_scheme_is_none(self):
        repo = decoder.Repo(SCHEME0_FIXTURE)
        run = repo.run_header(repo.newest_seq)
        self.assertEqual(run["fec_scheme"], 0)

    def test_no_checksum_or_parity_files(self):
        names = decoder.NameCache()
        run_dir = decoder.run_dir(os.path.join(SCHEME0_FIXTURE, "NOAHSARK"), 1, names)
        self.assertFalse(os.path.exists(os.path.join(run_dir, "checksum.bin")))
        self.assertFalse(os.path.exists(os.path.join(run_dir, "parity")))

    def test_verify_passes_by_content_id_and_file_hash_alone(self):
        args = types.SimpleNamespace(disc_root=SCHEME0_FIXTURE)
        self.assertEqual(decoder.cmd_verify(args), 0)


def fold_tree_to_lower(src: str, dst: str):
    """Copies every file and directory under src into dst, folding every
    path segment to lowercase. This mirrors what plain ISO 9660 level 4,
    with no Rock Ridge, does to every name on a real burn."""
    for root, _dirs, files in os.walk(src):
        rel = os.path.relpath(root, src)
        target_root = dst if rel == "." else os.path.join(dst, *(p.lower() for p in rel.split(os.sep)))
        os.makedirs(target_root, exist_ok=True)
        for name in files:
            shutil.copyfile(os.path.join(root, name), os.path.join(target_root, name.lower()))


class CaseFoldedFixtureTest(unittest.TestCase):
    """A plain ISO 9660 level 4 image with no Rock Ridge folds every
    fixed name to lowercase: NOAHSARK becomes noahsark, DISC.bin becomes
    disc.bin, and so on. This checks that decoder.py accepts that layout
    and agrees with the original, correctly-cased tree."""

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
        args = types.SimpleNamespace(disc_root=self.folded)
        self.assertEqual(decoder.cmd_verify(args), 0)

    def test_list_matches_original_tree(self):
        def listing(root):
            args = types.SimpleNamespace(disc_root=root, snapshot="LATEST")
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                code = decoder.cmd_list(args)
            self.assertEqual(code, 0)
            return buf.getvalue()

        self.assertEqual(listing(self.folded), listing(SCHEME0_FIXTURE))


# ---------------------------------------------------------------------------
# Defect 1: header_len forward compatibility.
#
# header_len (FORMAT.md section 2.3) is the common header plus a
# structure's fixed body; a reader computes the end of the fixed body from
# it, not from its own compiled size, and skips the excess when a newer
# minor version appended fields. These build a variant of an existing
# golden file with header_len enlarged and the extra bytes it now covers
# inserted, and check the decoder still reads the known fields from
# beyond the insertion, then check that a too-small header_len is refused.


def insert_padding(buf: bytes, insert_at: int, extra: int, header_crc_at: int) -> bytes:
    """For INDEX, REFS and DISCS: grows header_len (common header offset
    20) by extra, inserts extra zero bytes at insert_at (the old start of
    the variable-length tables), and recomputes header_crc32c at
    header_crc_at, since header_len itself lies inside the bytes that CRC
    covers. body_crc32c needs no recompute: it covers the table bytes by
    content, and their content, only their position, is what moves."""
    b = bytearray(buf)
    old_header_len = struct.unpack_from("<H", b, 20)[0]
    struct.pack_into("<H", b, 20, old_header_len + extra)
    struct.pack_into("<I", b, header_crc_at, decoder.crc32c(bytes(b[0:header_crc_at])))
    return bytes(b[:insert_at]) + bytes(extra) + bytes(b[insert_at:])


def bump_object_kind_body(buf: bytes, insert_at: int, extra: int) -> bytes:
    """For an object file (blob or tree, compression none, so payload
    bytes equal stored bytes): grows header_len, payload_len and
    stored_len by extra, inserts extra zero bytes at insert_at (the old
    end of the kind's own known fixed body inside the payload), and
    recomputes header_crc32c, which covers header_len, payload_len and
    stored_len alike."""
    b = bytearray(buf)
    old_header_len = struct.unpack_from("<H", b, 20)[0]
    struct.pack_into("<H", b, 20, old_header_len + extra)
    payload_len = struct.unpack_from("<Q", b, 40)[0]
    stored_len = struct.unpack_from("<Q", b, 48)[0]
    struct.pack_into("<Q", b, 40, payload_len + extra)
    struct.pack_into("<Q", b, 48, stored_len + extra)
    struct.pack_into("<I", b, 56, decoder.crc32c(bytes(b[0:56])))
    return bytes(b[:insert_at]) + bytes(extra) + bytes(b[insert_at:])


class HeaderLenForwardCompatTest(unittest.TestCase):
    def test_blob_header_len_skips_appended_fixed_field(self):
        insert_at = (
            decoder.COMMON_HEADER_LEN + decoder.OBJECT_HEADER_LEN + decoder.BLOB_BODY_FIXED_LEN
        )
        buf = bump_object_kind_body(golden("blob.golden"), insert_at, 8)
        obj = decoder.decode_object_bytes(buf, "t")
        blob = decoder.parse_blob(obj.payload, "t", obj.common["header_len"])
        self.assertEqual(blob["entry_count"], 2)
        self.assertEqual(blob["entries"][0]["length"], 4096)
        self.assertEqual(blob["entries"][1]["length"], 2048)

    def test_tree_header_len_skips_appended_fixed_field(self):
        insert_at = (
            decoder.COMMON_HEADER_LEN + decoder.OBJECT_HEADER_LEN + decoder.TREE_BODY_FIXED_LEN
        )
        buf = bump_object_kind_body(golden("tree.golden"), insert_at, 8)
        obj = decoder.decode_object_bytes(buf, "t")
        tree = decoder.parse_tree(obj.payload, "t", obj.common["header_len"])
        self.assertEqual(tree["entry_count"], 2)
        self.assertEqual(tree["entries"][0]["name"], b"docs")
        self.assertEqual(tree["entries"][1]["name"], b"file.txt")

    def test_snapshot_header_len_skips_appended_fixed_field(self):
        # snapshot.golden leaves PayloadLen/StoredLen at zero (see
        # SnapshotTest above), so it is sliced directly rather than
        # through decode_object_bytes, the same as SnapshotTest does.
        insert_at = (
            decoder.COMMON_HEADER_LEN + decoder.OBJECT_HEADER_LEN + decoder.SNAPSHOT_FIXED_LEN
        )
        b = bytearray(golden("snapshot.golden"))
        old_header_len = struct.unpack_from("<H", b, 20)[0]
        struct.pack_into("<H", b, 20, old_header_len + 8)
        buf = bytes(b[:insert_at]) + bytes(8) + bytes(b[insert_at:])
        common = decoder.decode_common_header(buf, "t")
        payload = buf[decoder.COMMON_HEADER_LEN + decoder.OBJECT_HEADER_LEN :]
        snap = decoder.parse_snapshot(payload, "t", common["header_len"])
        self.assertEqual(snap["generation"], 1)
        self.assertEqual(len(snap["meta"]), 2)
        self.assertEqual(snap["meta"][0]["value"], b"date")

    def test_index_header_len_skips_appended_fixed_field(self):
        buf = insert_padding(golden("index.golden"), decoder.INDEX_HEADER_LEN, 8, 76)
        idx = decoder.parse_index(buf, "t")
        self.assertEqual(idx["file_count"], 2)
        self.assertEqual(idx["objects"][0]["kind"], 1)
        self.assertEqual(idx["prereqs"][0]["run_seq"], 99)

    def test_refs_header_len_skips_appended_fixed_field(self):
        buf = insert_padding(golden("refs.golden"), decoder.REFS_HEADER_LEN, 8, 68)
        t = decoder.parse_refs(buf, "t")
        self.assertEqual(t["record_count"], 2)
        self.assertEqual(t["records"][0]["name"], "LATEST")

    def test_discs_header_len_skips_appended_fixed_field(self):
        buf = insert_padding(golden("discs.golden"), decoder.DISCS_HEADER_LEN, 8, 68)
        t = decoder.parse_discs(buf, "t")
        self.assertEqual(t["record_count"], 2)
        self.assertEqual(t["rows"][0]["label"], "BACKUP01")

    def test_object_header_len_too_small_is_refused(self):
        b = bytearray(golden("chunk.golden"))
        struct.pack_into(
            "<H", b, 20, decoder.COMMON_HEADER_LEN + decoder.OBJECT_HEADER_LEN - 1
        )
        struct.pack_into("<I", b, 56, decoder.crc32c(bytes(b[0:56])))
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.decode_object_bytes(bytes(b), "t")
        self.assertIn("header_len", str(cm.exception))

    def test_index_header_len_too_small_is_refused(self):
        b = bytearray(golden("index.golden"))
        struct.pack_into("<H", b, 20, decoder.INDEX_HEADER_LEN - 1)
        struct.pack_into("<I", b, 76, decoder.crc32c(bytes(b[0:76])))
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.parse_index(bytes(b), "t")
        self.assertIn("header_len", str(cm.exception))

    def test_refs_header_len_too_small_is_refused(self):
        b = bytearray(golden("refs.golden"))
        struct.pack_into("<H", b, 20, decoder.REFS_HEADER_LEN - 1)
        struct.pack_into("<I", b, 68, decoder.crc32c(bytes(b[0:68])))
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.parse_refs(bytes(b), "t")
        self.assertIn("header_len", str(cm.exception))

    def test_discs_header_len_too_small_is_refused(self):
        b = bytearray(golden("discs.golden"))
        struct.pack_into("<H", b, 20, decoder.DISCS_HEADER_LEN - 1)
        struct.pack_into("<I", b, 68, decoder.crc32c(bytes(b[0:68])))
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.parse_discs(bytes(b), "t")
        self.assertIn("header_len", str(cm.exception))


# ---------------------------------------------------------------------------
# Defect 3: hash_algo is checked, not just parsed.


class HashAlgoTest(unittest.TestCase):
    def test_unsupported_hash_algo_is_refused(self):
        b = bytearray(golden("chunk.golden"))
        b[33] = 0x1E  # hash_algo, object header offset 1 (absolute 33)
        struct.pack_into("<I", b, 56, decoder.crc32c(bytes(b[0:56])))
        with self.assertRaises(decoder.FormatError) as cm:
            decoder.decode_object_bytes(bytes(b), "t")
        self.assertIn("0x1e", str(cm.exception))

    def test_sha256_golden_is_accepted(self):
        obj = decoder.decode_object_bytes(golden("chunk.golden"), "t")
        self.assertEqual(obj.obj_header["hash_algo"], decoder.HASH_ALGO_SHA256)


# ---------------------------------------------------------------------------
# Defect 2: restore continues past a missing or bad object, names the
# disc a Prereqs entry points to, and never leaves a partial file.


class DescribeMissingObjectTest(unittest.TestCase):
    """describe_missing_object is the message helper restore_file uses to
    add Prereqs/DISCS context; tested directly since neither checked-in
    fixture packs a second run to reference as a prerequisite."""

    def test_names_run_and_disc_label(self):
        cid = "aa" * 32
        idx = {"prereqs": [{"content_id": cid, "run_seq": 5}]}
        discs = {"rows": [{"run_seq": 5, "label": "BACKUP07", "disc_uuid": b"\x01" * 16}]}
        msg = decoder.describe_missing_object(cid, idx, discs)
        self.assertIn("run 5", msg)
        self.assertIn("BACKUP07", msg)

    def test_falls_back_to_disc_uuid_when_no_label(self):
        cid = "bb" * 32
        idx = {"prereqs": [{"content_id": cid, "run_seq": 9}]}
        discs = {"rows": [{"run_seq": 9, "label": "", "disc_uuid": b"\x02" * 16}]}
        msg = decoder.describe_missing_object(cid, idx, discs)
        self.assertIn("run 9", msg)
        self.assertIn("02" * 16, msg)

    def test_names_run_alone_when_disc_unknown(self):
        cid = "cc" * 32
        idx = {"prereqs": [{"content_id": cid, "run_seq": 3}]}
        discs = {"rows": []}
        msg = decoder.describe_missing_object(cid, idx, discs)
        self.assertIn("run 3", msg)

    def test_empty_when_not_a_prereq(self):
        idx = {"prereqs": []}
        discs = {"rows": []}
        self.assertEqual(decoder.describe_missing_object("dd" * 32, idx, discs), "")


class SafeDestTest(unittest.TestCase):
    """safe_dest is restore's last line of defense against a decoded path
    that would land outside --out; unescape_root_name can turn a root
    entry name that passed tree-entry validation into one holding '/' or
    '..' segments (FORMAT.md's root name escape, section 6.15)."""

    def test_normal_relative_path_joins_under_out(self):
        report = decoder.Report()
        dest = decoder.safe_dest("/out", "a/b.txt", report)
        self.assertEqual(dest, os.path.abspath("/out/a/b.txt"))
        self.assertTrue(report.ok())

    def test_leading_slash_is_relative_to_out(self):
        report = decoder.Report()
        dest = decoder.safe_dest("/out", "/home/user/src/file.txt", report)
        self.assertEqual(dest, os.path.abspath("/out/home/user/src/file.txt"))
        self.assertTrue(report.ok())

    def test_dotdot_is_refused(self):
        report = decoder.Report()
        dest = decoder.safe_dest("/out", "../../etc/passwd", report)
        self.assertIsNone(dest)
        self.assertFalse(report.ok())

    def test_escaped_slash_in_root_name_cannot_escape_out(self):
        # unescape_root_name turns "a%2F..%2F..%2Fetc%2Fpasswd" into
        # "a/../../etc/passwd", a shape ordinary tree-entry validation
        # (parse_tree_entry) would have refused had it not been hidden
        # behind the escape.
        name = decoder.unescape_root_name(b"a%2F..%2F..%2Fetc%2Fpasswd")
        report = decoder.Report()
        dest = decoder.safe_dest("/out", name, report)
        self.assertIsNone(dest)
        self.assertFalse(report.ok())


class RestoreContinuesPastMissingObjectTest(unittest.TestCase):
    """TWO_FILES_FIXTURE carries two independent files; hiding one's
    chunk object must not stop the other from restoring."""

    MISSING_OBJECT = os.path.join(
        "objects", "14", "122014167ee847295a1cf543cc4d1bc5b91581d891effd6a58c0f007727fa5addd38"
    )

    def setUp(self):
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp, ignore_errors=True)
        self.root = os.path.join(tmp, "disc")
        shutil.copytree(TWO_FILES_FIXTURE, self.root)
        self.out = os.path.join(tmp, "out")
        missing = os.path.join(self.root, "NOAHSARK", self.MISSING_OBJECT)
        self.assertTrue(os.path.isfile(missing), "fixture layout changed under this test")
        os.remove(missing)

    def _restored_files(self):
        found = {}
        for dirpath, _dirs, files in os.walk(self.out):
            for name in files:
                found[name] = os.path.join(dirpath, name)
        return found

    def test_other_file_restored_missing_one_reported_nonzero_exit(self):
        args = types.SimpleNamespace(disc_root=self.root, snapshot="LATEST", out=self.out)
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            code = decoder.cmd_restore(args)
        self.assertEqual(code, 1)
        self.assertIn("hello.txt", err.getvalue())

        found = self._restored_files()
        self.assertNotIn("hello.txt", found, "a failed file must not be left behind at all")
        self.assertIn("second.txt", found)
        with open(found["second.txt"], "rb") as f:
            self.assertEqual(f.read(), b"second file content here\n")

    def test_no_partial_file_left_behind(self):
        args = types.SimpleNamespace(disc_root=self.root, snapshot="LATEST", out=self.out)
        with contextlib.redirect_stderr(io.StringIO()):
            decoder.cmd_restore(args)
        for dirpath, _dirs, files in os.walk(self.out):
            for name in files:
                self.assertFalse(
                    name.endswith(".noahsark-partial"), f"stray partial file: {name}"
                )


if __name__ == "__main__":
    unittest.main()
