"""Unit tests for decoder.py against the checked-in golden files.

Each golden file under internal/format/testdata is a byte-exact encoding of
a Go test fixture (the matching *_test.go file in internal/format). This
test decodes every golden file with decoder.py and checks the decoded
fields against the same values the Go fixture used.

Run with:
    python3 -m unittest reference/decoder_test.py
"""

import os
import sys
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
        run_dir = decoder.run_dir(os.path.join(SCHEME0_FIXTURE, "NOAHSARK"), 1)
        self.assertFalse(os.path.exists(os.path.join(run_dir, "checksum.bin")))
        self.assertFalse(os.path.exists(os.path.join(run_dir, "parity")))

    def test_verify_passes_by_content_id_and_file_hash_alone(self):
        args = types.SimpleNamespace(disc_root=SCHEME0_FIXTURE)
        self.assertEqual(decoder.cmd_verify(args), 0)


if __name__ == "__main__":
    unittest.main()
