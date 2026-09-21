#!/usr/bin/env python3
"""NoahsArk reference decoder.

A standalone, standard-library-only Python 3 program that reads a NoahsArk
backup disc from a mounted directory (or from a copy of one on any local
filesystem) with no NoahsArk software installed. It parses the disc
superblock, a run's header and index, the REFS and DISCS catalog tables,
and every object it visits (chunk, blob, tree, snapshot); it verifies the
SHA-256 content id of every object it reads; and it can list, verify or
restore a snapshot.

This decoder holds no Reed-Solomon code. It cannot repair a damaged disc.
It reads the checksum column only to report what a repair tool would use.

It uses the standard library only. zstd-compressed payloads are expanded
with the compression.zstd module where the interpreter provides it
(Python 3.14+), or with the zstd command-line tool otherwise; when
neither is available, a compressed payload cannot be expanded and restore
of the files that use it fails, but listing and verification of
everything else still proceeds.

Usage:
    decoder.py summary <disc-root>
    decoder.py list <disc-root> [--snapshot NAME]
    decoder.py verify <disc-root>
    decoder.py restore <disc-root> --snapshot NAME --out DIR

<disc-root> is the directory that contains NOAHSARK, or the NOAHSARK
directory itself. NAME is a ref name (for example LATEST), or the 68-hex-
character snapshot content id text form.

Exit status is nonzero when any verification check fails.
"""

import argparse
import os
import struct
import subprocess
import sys
from hashlib import sha256

# ---------------------------------------------------------------------------
# CRC-32C (Castagnoli): polynomial 0x1EDC6F41, reflected, init 0xFFFFFFFF,
# final XOR 0xFFFFFFFF. The standard library's zlib.crc32 is plain CRC-32,
# a different polynomial, so the table is built by hand.

_CRC32C_POLY = 0x82F63B78  # reflected form of 0x1EDC6F41


def _build_crc32c_table():
    table = []
    for byte in range(256):
        crc = byte
        for _ in range(8):
            crc = (crc >> 1) ^ _CRC32C_POLY if crc & 1 else crc >> 1
        table.append(crc)
    return table


_CRC32C_TABLE = _build_crc32c_table()


def crc32c(data: bytes) -> int:
    crc = 0xFFFFFFFF
    for b in data:
        crc = _CRC32C_TABLE[(crc ^ b) & 0xFF] ^ (crc >> 8)
    return crc ^ 0xFFFFFFFF


# ---------------------------------------------------------------------------
# Errors and verification tracking.


class FormatError(Exception):
    """A structure failed a mandatory check: bad magic, bad CRC, bad field."""


class Report:
    """Collects verification failures across the whole run of the tool."""

    def __init__(self):
        self.failures = []
        self.notes = []

    def fail(self, where: str, message: str):
        self.failures.append(f"{where}: {message}")
        print(f"FAIL {where}: {message}", file=sys.stderr)

    def note(self, where: str, message: str):
        """Something this run cannot check, and that is not damage: an
        object that lives on a disc the command was not given."""
        self.notes.append(f"{where}: {message}")
        print(f"NOTE {where}: {message}", file=sys.stderr)

    def ok(self) -> bool:
        return not self.failures


# ---------------------------------------------------------------------------
# Magic values and registries.

PROJECT_MAGIC = b"NOAHSARK"


def _magic(name: bytes) -> bytes:
    if len(name) > 8:
        raise ValueError(name)
    return name.ljust(8, b"\0")


MAGIC_CHUNK = _magic(b"CHUNK")
MAGIC_BLOB = _magic(b"BLOB")
MAGIC_TREE = _magic(b"TREE")
MAGIC_SNAPSHOT = _magic(b"SNAPSHOT")
MAGIC_DISC = _magic(b"DISC")
MAGIC_RUN = _magic(b"RUN")
MAGIC_INDEX = _magic(b"INDEX")
MAGIC_REFS = _magic(b"REFS")
MAGIC_DISCS = _magic(b"DISCS")
MAGIC_CHECKSUM = _magic(b"CHECKSUM")

OBJECT_KIND_NAMES = {1: "chunk", 2: "blob", 3: "tree", 4: "snapshot"}
COMPRESSION_NAMES = {0: "none", 1: "zstd"}
FEC_SCHEME_NAMES = {0: "none", 1: "rs255-gf8"}

# Hash algorithm registry (multicodec codes). sha2-256 is the only
# algorithm of format major 1; every other code is refused by name.
HASH_ALGO_SHA256 = 0x12

ENTRY_TYPE_NAMES = {
    1: "regular",
    2: "directory",
    3: "symlink",
    4: "chardev",
    5: "blockdev",
    6: "fifo",
    7: "socket",
}

ENTRY_FLAG_CTIME_ABSENT = 1 << 2
ENTRY_FLAG_SPARSE = 1 << 4
ENTRY_FLAG_METADATA_PARTIAL = 1 << 5
ENTRY_FLAG_UNSTABLE = 1 << 7

TLV_SYMLINK_TARGET = 0x0001
TLV_USER_NAME = 0x0002
TLV_GROUP_NAME = 0x0003
TLV_KNOWN = (TLV_SYMLINK_TARGET, TLV_USER_NAME, TLV_GROUP_NAME)
TLV_FLAG_CRITICAL = 1

SNAPSHOT_META_TAG_NAMES = {
    1: "author",
    2: "host",
    3: "message",
    5: "exclude_rules",
    6: "checksum_commit",
}

# File role registry. Roles 3, 4, 5, 7, 8 and 14 carry a file_hash; the
# path of each follows from the role alone.
ROLE_INDEX = 1
ROLE_RUN = 2
ROLE_DISC = 3
ROLE_README = 4
ROLE_FORMAT = 5
ROLE_REFS = 7
ROLE_DISCS = 8
ROLE_CHECKSUM = 10
ROLE_PARITY = 11
ROLE_RUN2 = 12
ROLE_OBJECT = 13
ROLE_REFERENCE = 14
HASHED_ROLES = (ROLE_DISC, ROLE_README, ROLE_FORMAT, ROLE_REFS, ROLE_DISCS, ROLE_REFERENCE)
# The roles inside the FEC stream, in the row order of INDEX. The rows of
# every other role describe a file of the run outside the stream.
STREAM_ROLES = (ROLE_INDEX, ROLE_DISC, ROLE_README, ROLE_FORMAT, ROLE_REFS, ROLE_DISCS,
                ROLE_OBJECT, ROLE_REFERENCE)

BLOCK_SIZE = 2048

# ---------------------------------------------------------------------------
# Common header and object header.

COMMON_HEADER_LEN = 32
OBJECT_HEADER_LEN = 32
OBJECT_FIXED_LEN = COMMON_HEADER_LEN + OBJECT_HEADER_LEN


def decode_common_header(buf: bytes, where: str):
    """The 32-byte header that begins every structure: the project magic,
    the kind name, version_major and header_len. A reader refuses an
    unknown version_major and ignores every reserved field."""
    if len(buf) < COMMON_HEADER_LEN:
        raise FormatError(f"{where}: buffer shorter than the common header")
    magic_project = buf[0:8]
    magic_kind = buf[8:16]
    version_major, header_len = struct.unpack_from("<H2xH", buf, 16)
    if magic_project != PROJECT_MAGIC:
        raise FormatError(f"{where}: bad magic_project {magic_project!r}")
    if version_major != 1:
        raise FormatError(f"{where}: unsupported version_major {version_major}")
    return {
        "magic_kind": magic_kind,
        "version_major": version_major,
        "header_len": header_len,
    }


def check_magic_kind(common: dict, want: bytes, where: str):
    if common["magic_kind"] != want:
        raise FormatError(
            f"{where}: magic_kind {common['magic_kind']!r}, want {want!r}"
        )


def variable_area_start(common: dict, known_len: int, where: str) -> int:
    """header_len is the offset of the first byte after the fixed part, so
    it marks where the rows, the entries or the TLV records begin. A
    header_len below the fixed part this decoder knows is refused; a
    larger one means a later writer appended a field, and the excess is
    skipped instead of misread."""
    header_len = common["header_len"]
    if header_len < known_len:
        raise FormatError(
            f"{where}: header_len {header_len} below the known fixed part {known_len}"
        )
    return header_len


def object_header_fields(body: bytes):
    """Parses the 32-byte object header body alone, with no CRC check."""
    if len(body) < OBJECT_HEADER_LEN:
        raise FormatError("buffer shorter than the object header")
    kind, hash_algo, compression = struct.unpack_from("<BBxB", body, 0)
    payload_len, stored_len = struct.unpack_from("<QQ", body, 8)
    header_crc32c = struct.unpack_from("<I", body, 24)[0]
    return {
        "kind": kind,
        "hash_algo": hash_algo,
        "compression": compression,
        "payload_len": payload_len,
        "stored_len": stored_len,
        "header_crc32c": header_crc32c,
    }


def decode_object_header(buf: bytes, where: str, verify_crc: bool = True):
    """Parses the object header that follows the common header in buf, and,
    when verify_crc is set, verifies header_crc32c over the common header
    and the first 24 bytes of the object header."""
    if len(buf) < OBJECT_FIXED_LEN:
        raise FormatError(f"{where}: buffer shorter than the object header")
    fields = object_header_fields(buf[COMMON_HEADER_LEN:OBJECT_FIXED_LEN])
    if verify_crc and crc32c(buf[0 : COMMON_HEADER_LEN + 24]) != fields["header_crc32c"]:
        raise FormatError(f"{where}: object header_crc32c mismatch")
    return fields


# ---------------------------------------------------------------------------
# zstd decompression: compression.zstd (3.14+), else the zstd CLI tool.

_ZSTD_MODULE_TRIED = False
_ZSTD_MODULE = None


def _zstd_module():
    global _ZSTD_MODULE_TRIED, _ZSTD_MODULE
    if not _ZSTD_MODULE_TRIED:
        _ZSTD_MODULE_TRIED = True
        try:
            import compression.zstd as m  # Python 3.14+

            _ZSTD_MODULE = m
        except ImportError:
            _ZSTD_MODULE = None
    return _ZSTD_MODULE


class ZstdUnavailable(Exception):
    """Neither compression.zstd nor the zstd command-line tool is present."""


def _which(name: str):
    for d in os.environ.get("PATH", "").split(os.pathsep):
        candidate = os.path.join(d, name)
        if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate
    return None


def zstd_decompress(data: bytes, payload_len: int) -> bytes:
    mod = _zstd_module()
    if mod is not None:
        out = mod.decompress(data)
    else:
        tool = _which("zstd")
        if tool is None:
            raise ZstdUnavailable(
                "no compression.zstd module and no zstd command-line tool found; "
                "cannot expand this compressed payload"
            )
        out = subprocess.run(
            [tool, "-d", "-c", "-q"], input=data, stdout=subprocess.PIPE, check=True
        ).stdout
    if len(out) != payload_len:
        raise FormatError(
            f"decompressed length {len(out)} does not match payload_len {payload_len}"
        )
    return out


# ---------------------------------------------------------------------------
# Generic object file reader. Common to chunk, blob, tree and snapshot.


class ObjectFile:
    """A decoded object file: its headers, and its uncompressed payload."""

    def __init__(self, common, obj_header, payload, content_id_hex):
        self.common = common
        self.obj_header = obj_header
        self.payload = payload
        self.content_id_hex = content_id_hex

    def kind_fixed_len(self, known_len: int, where: str) -> int:
        """The offset inside the payload where the entries or the TLV
        records start: header_len minus the two headers. A later writer
        can enlarge it; it never shrinks below the known fixed body."""
        off = self.common["header_len"] - OBJECT_FIXED_LEN
        if off < known_len:
            raise FormatError(
                f"{where}: header_len {self.common['header_len']} below the known "
                f"fixed body ({OBJECT_FIXED_LEN + known_len})"
            )
        return off


def decode_object_bytes(
    buf: bytes, where: str, report: Report = None, verify_crc: bool = True
) -> ObjectFile:
    """Decodes one object file's bytes: common header, object header, and
    the uncompressed payload. Computes the content id text form but does
    not compare it against any file name; read_object_file does that."""
    common = decode_common_header(buf, where)
    obj_header = decode_object_header(buf, where, verify_crc=verify_crc)
    # The stored bytes always start right after the two headers. A chunk
    # has no fixed body, so its header_len is exactly 64: every payload
    # byte of a chunk is file content.
    if common["magic_kind"] == MAGIC_CHUNK:
        if common["header_len"] != OBJECT_FIXED_LEN:
            raise FormatError(
                f"{where}: chunk header_len {common['header_len']}, want {OBJECT_FIXED_LEN}"
            )
    else:
        variable_area_start(common, OBJECT_FIXED_LEN, where)

    hash_algo = obj_header["hash_algo"]
    if hash_algo != HASH_ALGO_SHA256:
        raise FormatError(
            f"{where}: unsupported hash_algo 0x{hash_algo:02x}; "
            f"only sha2-256 (0x{HASH_ALGO_SHA256:02x}) is implemented"
        )
    kind = obj_header["kind"]
    if kind not in OBJECT_KIND_NAMES:
        raise FormatError(f"{where}: object kind {kind} outside 1 to 4")
    stored = buf[OBJECT_FIXED_LEN : OBJECT_FIXED_LEN + obj_header["stored_len"]]
    if len(stored) != obj_header["stored_len"]:
        raise FormatError(f"{where}: stored bytes truncated")

    compression = obj_header["compression"]
    if compression == 0:
        payload = stored
        if len(payload) != obj_header["payload_len"]:
            raise FormatError(f"{where}: payload_len does not match stored bytes")
    elif compression == 1:
        try:
            payload = zstd_decompress(stored, obj_header["payload_len"])
        except ZstdUnavailable as e:
            if report is not None:
                report.fail(where, str(e))
            return ObjectFile(common, obj_header, None, None)
    else:
        raise FormatError(f"{where}: unsupported compression id {compression}")

    text_form = "1220" + sha256(payload).hexdigest()
    return ObjectFile(common, obj_header, payload, text_form)


def read_object_file(path: str, report: Report = None) -> ObjectFile:
    """Reads an object file, verifies its header CRC and its content id
    against its file name, and returns the decoded payload bytes."""
    with open(path, "rb") as f:
        buf = f.read()
    obj = decode_object_bytes(buf, path, report=report)
    if obj.payload is None:
        return obj
    name = os.path.basename(path)
    if name != obj.content_id_hex:
        message = f"content id mismatch: file name {name}, computed {obj.content_id_hex}"
        if report is not None:
            # The bytes are damaged. They never reach a caller: a reader
            # verifies the content id before it returns object bytes.
            report.fail(path, message)
            return ObjectFile(obj.common, obj.obj_header, None, None)
        raise FormatError(f"{path}: {message}")
    return obj


# ---------------------------------------------------------------------------
# Chunk: opaque payload bytes. Nothing further to parse.

# ---------------------------------------------------------------------------
# Blob: the ordered chunk ids of one file. An entry stores no file
# offset; the offset of an entry is the sum of the lengths before it.

BLOB_BODY_FIXED_LEN = 8
BLOB_ENTRY_LEN = 40


def parse_blob(payload: bytes, where: str, obj: ObjectFile = None):
    entry_count = struct.unpack_from("<Q", payload, 0)[0]
    off = obj.kind_fixed_len(BLOB_BODY_FIXED_LEN, where) if obj else BLOB_BODY_FIXED_LEN
    if off + entry_count * BLOB_ENTRY_LEN != len(payload):
        raise FormatError(
            f"{where}: {entry_count} entries do not fill the payload "
            f"({len(payload)} bytes from offset {off})"
        )
    entries = []
    file_offset = 0
    for _ in range(entry_count):
        content_id = payload[off : off + 32]
        length = struct.unpack_from("<Q", payload, off + 32)[0]
        if length == 0:
            raise FormatError(f"{where}: blob entry with length 0")
        entries.append(
            {
                "content_id": content_id.hex(),
                "length": length,
                "file_offset": file_offset,
            }
        )
        file_offset += length
        off += BLOB_ENTRY_LEN
    return {"entry_count": entry_count, "size": file_offset, "entries": entries}


# ---------------------------------------------------------------------------
# Tree, tree entry and TLV.

TREE_BODY_FIXED_LEN = 8
TREE_ENTRY_HEADER_LEN = 72

# The content area holds one blob id for a regular file, one tree id for
# a directory, and nothing for every other entry type.
CONTENT_LEN_BY_TYPE = {1: 32, 2: 32, 3: 0, 4: 0, 5: 0, 6: 0, 7: 0}


def _align8(n: int) -> int:
    return (n + 7) & ~7


def parse_tlvs(buf: bytes, where: str):
    """Extension TLV records of one tree entry. An unknown record with the
    critical bit set refuses the entry; an unknown non-critical record is
    kept and reported."""
    tlvs = []
    p = 0
    while p < len(buf):
        if p + 8 > len(buf):
            raise FormatError(f"{where}: truncated TLV prefix")
        tlv_type, flags, tlv_len = struct.unpack_from("<HHI", buf, p)
        n = _align8(8 + tlv_len)
        if p + n > len(buf):
            raise FormatError(f"{where}: truncated TLV payload")
        if tlv_type not in TLV_KNOWN and (
            flags & TLV_FLAG_CRITICAL or 0x8000 <= tlv_type <= 0xBFFF
        ):
            raise FormatError(f"{where}: unknown critical TLV type 0x{tlv_type:04x}")
        tlvs.append(
            {"type": tlv_type, "flags": flags, "payload": buf[p + 8 : p + 8 + tlv_len]}
        )
        p += n
    return tlvs


def parse_tree_entry(buf: bytes, where: str):
    """One tree entry: the 72-byte fixed header, then the name, the
    content area and the TLV area, each at the offset the entry's own
    lengths derive."""
    if len(buf) < TREE_ENTRY_HEADER_LEN:
        raise FormatError(f"{where}: truncated tree entry")
    entry_len, entry_type, entry_flags, name_len = struct.unpack_from("<IBBH", buf, 0)
    size, mtime_sec, ctime_sec = struct.unpack_from("<Qqq", buf, 8)
    mtime_nsec, ctime_nsec = struct.unpack_from("<II", buf, 32)
    mode, uid, gid, rdev_major, rdev_minor = struct.unpack_from("<IIIII", buf, 40)
    content_len, ext_len = struct.unpack_from("<II", buf, 60)

    if entry_type not in ENTRY_TYPE_NAMES:
        raise FormatError(f"{where}: bad entry_type {entry_type}")
    if name_len < 1 or name_len > 4095:
        raise FormatError(f"{where}: bad name_len {name_len}")
    if content_len != CONTENT_LEN_BY_TYPE[entry_type]:
        raise FormatError(
            f"{where}: content_len {content_len} for entry_type {entry_type}"
        )
    if ext_len % 8:
        raise FormatError(f"{where}: ext_len {ext_len} is not a multiple of 8")

    content_off = _align8(TREE_ENTRY_HEADER_LEN + name_len)
    ext_off = _align8(content_off + content_len)
    derived_len = _align8(ext_off + ext_len)
    if entry_len != derived_len:
        raise FormatError(
            f"{where}: entry_len {entry_len} differs from the derived {derived_len}"
        )
    if entry_len > len(buf):
        raise FormatError(f"{where}: entry_len {entry_len} past the end of the tree")

    name = buf[TREE_ENTRY_HEADER_LEN : TREE_ENTRY_HEADER_LEN + name_len]
    if name in (b".", b"..") or b"/" in name or b"\\" in name or 0 in name:
        raise FormatError(f"{where}: invalid entry name {name!r}")

    content_id = None
    if content_len:
        content_id = buf[content_off : content_off + content_len].hex()
    tlvs = parse_tlvs(buf[ext_off : ext_off + ext_len], where) if ext_len else []
    if entry_type == 3 and not any(t["type"] == TLV_SYMLINK_TARGET for t in tlvs):
        raise FormatError(f"{where}: symlink entry {name!r} carries no target TLV")

    return {
        "entry_len": entry_len,
        "entry_type": entry_type,
        "entry_flags": entry_flags,
        "size": size,
        "mtime_sec": mtime_sec,
        "ctime_sec": ctime_sec,
        "mtime_nsec": mtime_nsec,
        "ctime_nsec": ctime_nsec,
        "mode": mode,
        "uid": uid,
        "gid": gid,
        "rdev_major": rdev_major,
        "rdev_minor": rdev_minor,
        "name": name,
        "content_id": content_id,
        "tlvs": tlvs,
    }


def parse_tree(payload: bytes, where: str, obj: ObjectFile = None):
    entry_count = struct.unpack_from("<I", payload, 0)[0]
    pos = obj.kind_fixed_len(TREE_BODY_FIXED_LEN, where) if obj else TREE_BODY_FIXED_LEN
    entries = []
    prev_key = None
    for _ in range(entry_count):
        e = parse_tree_entry(payload[pos:], where)
        # A directory name compares as if a '/' byte were appended.
        key = e["name"] + (b"/" if e["entry_type"] == 2 else b"")
        if prev_key is not None and prev_key >= key:
            raise FormatError(f"{where}: tree entries not in ascending order")
        prev_key = key
        entries.append(e)
        pos += e["entry_len"]
    return {"entry_count": entry_count, "entries": entries}


# ---------------------------------------------------------------------------
# Snapshot.

SNAPSHOT_BODY_FIXED_LEN = 112


def parse_snapshot_meta(buf: bytes, where: str):
    """Snapshot metadata records. Each is padded to a 4-byte boundary. An
    unknown tag in the reserved critical range refuses the snapshot."""
    records = []
    p = 0
    while p < len(buf):
        if p + 8 > len(buf):
            raise FormatError(f"{where}: truncated snapshot meta prefix")
        tag, flags, value_len = struct.unpack_from("<HHI", buf, p)
        total = 8 + value_len
        if total % 4:
            total += 4 - (total % 4)
        if p + total > len(buf):
            raise FormatError(f"{where}: truncated snapshot meta value")
        if 0x8000 <= tag <= 0xBFFF:
            raise FormatError(f"{where}: unknown critical metadata tag 0x{tag:04x}")
        records.append(
            {"tag": tag, "flags": flags, "value": buf[p + 8 : p + 8 + value_len]}
        )
        p += total
    return records


def parse_snapshot(payload: bytes, where: str, obj: ObjectFile = None):
    root_tree = payload[0:32].hex()
    parent = payload[32:64].hex()
    time_sec = struct.unpack_from("<q", payload, 72)[0]
    time_nsec, tz_offset_sec = struct.unpack_from("<Ii", payload, 80)
    total_size = struct.unpack_from("<Q", payload, 88)[0]
    meta_count = struct.unpack_from("<H", payload, 106)[0]
    meta_start = (
        obj.kind_fixed_len(SNAPSHOT_BODY_FIXED_LEN, where)
        if obj
        else SNAPSHOT_BODY_FIXED_LEN
    )
    meta = parse_snapshot_meta(payload[meta_start:], where)
    if len(meta) != meta_count:
        raise FormatError(
            f"{where}: meta_count {meta_count} does not match {len(meta)} records found"
        )
    return {
        "root_tree": root_tree,
        "parent": parent,
        "time_sec": time_sec,
        "time_nsec": time_nsec,
        "tz_offset_sec": tz_offset_sec,
        "total_size": total_size,
        "meta": meta,
    }


# ---------------------------------------------------------------------------
# Disc superblock: 2048 bytes, one sector, never compressed.

DISC_LEN = 2048


def parse_disc(buf: bytes, where: str):
    if len(buf) != DISC_LEN:
        raise FormatError(f"{where}: disc superblock is {len(buf)} bytes, want {DISC_LEN}")
    common = decode_common_header(buf, where)
    check_magic_kind(common, MAGIC_DISC, where)
    super_crc32c = struct.unpack_from("<I", buf, 2044)[0]
    if crc32c(buf[0:2044]) != super_crc32c:
        raise FormatError(f"{where}: super_crc32c mismatch")
    disc_seq, capacity_sectors = struct.unpack_from("<QQ", buf, 64)
    created_sec = struct.unpack_from("<q", buf, 120)[0]
    created_nsec, tz_offset_sec = struct.unpack_from("<Ii", buf, 128)
    label_len = struct.unpack_from("<I", buf, 144)[0]
    if label_len > 64:
        raise FormatError(f"{where}: label_len {label_len} above 64")
    return {
        "disc_uuid": buf[32:48],
        "repo_uuid": buf[48:64],
        "disc_seq": disc_seq,
        "capacity_sectors": capacity_sectors,
        "created_sec": created_sec,
        "created_nsec": created_nsec,
        "tz_offset_sec": tz_offset_sec,
        "label_len": label_len,
        "label": buf[148 : 148 + label_len],
        "tool_version": struct.unpack_from("<I", buf, 212)[0],
    }


# ---------------------------------------------------------------------------
# Run header: 512 bytes, written two times as RUN.bin and RUN2.bin.

RUN_LEN = 512


def parse_run(buf: bytes, where: str):
    if len(buf) != RUN_LEN:
        raise FormatError(f"{where}: run header is {len(buf)} bytes, want {RUN_LEN}")
    common = decode_common_header(buf, where)
    check_magic_kind(common, MAGIC_RUN, where)
    header_crc32c = struct.unpack_from("<I", buf, 504)[0]
    if crc32c(buf[0:504]) != header_crc32c:
        raise FormatError(f"{where}: run header_crc32c mismatch")
    run_seq, disc_seq = struct.unpack_from("<QQ", buf, 64)
    fec_k, fec_m, fec_scheme, hash_algo = struct.unpack_from("<HHBB", buf, 80)
    if hash_algo != HASH_ALGO_SHA256:
        raise FormatError(
            f"{where}: unsupported hash_algo 0x{hash_algo:02x}; "
            f"only sha2-256 (0x{HASH_ALGO_SHA256:02x}) is implemented"
        )
    index_bytes = struct.unpack_from("<Q", buf, 96)[0]
    stream_bytes = struct.unpack_from("<Q", buf, 136)[0]
    created_sec = struct.unpack_from("<q", buf, 176)[0]
    created_nsec, tool_version = struct.unpack_from("<II", buf, 184)
    return {
        "disc_uuid": buf[32:48],
        "repo_uuid": buf[48:64],
        "run_seq": run_seq,
        "disc_seq": disc_seq,
        "fec_k": fec_k,
        "fec_m": fec_m,
        "fec_scheme": fec_scheme,
        "hash_algo": hash_algo,
        "index_bytes": index_bytes,
        "index_hash": buf[104:136],
        "stream_bytes": stream_bytes,
        "created_sec": created_sec,
        "created_nsec": created_nsec,
        "tool_version": tool_version,
    }


def fec_geometry(run: dict):
    """stream_blocks and L, the block count of one column, derived from
    the run header alone. Neither is stored."""
    if run["fec_scheme"] != 1:
        return None
    stream_blocks = run["stream_bytes"] // BLOCK_SIZE
    k = run["fec_k"]
    if k == 0:
        raise FormatError("run header: fec_scheme 1 with fec_k 0")
    return {
        "stream_blocks": stream_blocks,
        "column_blocks": -(-stream_blocks // k),
    }


# ---------------------------------------------------------------------------
# INDEX: the Files, Objects and Prereqs tables of one run.

INDEX_HEADER_LEN = 56
INDEX_FILE_ROW_LEN = 48
INDEX_OBJECT_ROW_LEN = 40
INDEX_PREREQ_ROW_LEN = 48


def parse_index(buf: bytes, where: str):
    common = decode_common_header(buf, where)
    check_magic_kind(common, MAGIC_INDEX, where)
    run_seq = struct.unpack_from("<Q", buf, 32)[0]
    file_count, object_count, prereq_count = struct.unpack_from("<III", buf, 40)

    off = variable_area_start(common, INDEX_HEADER_LEN, where)
    total = (
        off
        + file_count * INDEX_FILE_ROW_LEN
        + object_count * INDEX_OBJECT_ROW_LEN
        + prereq_count * INDEX_PREREQ_ROW_LEN
    )
    if len(buf) != total:
        raise FormatError(
            f"{where}: INDEX is {len(buf)} bytes, the row counts need {total}"
        )

    files = []
    for _ in range(file_count):
        byte_len = struct.unpack_from("<Q", buf, off + 32)[0]
        files.append(
            {"file_hash": buf[off : off + 32], "byte_len": byte_len, "role": buf[off + 40]}
        )
        off += INDEX_FILE_ROW_LEN

    objects = []
    for _ in range(object_count):
        kind = buf[off + 32]
        if kind not in OBJECT_KIND_NAMES:
            raise FormatError(f"{where}: Objects row kind {kind} outside 1 to 4")
        objects.append({"content_id": buf[off : off + 32].hex(), "kind": kind})
        off += INDEX_OBJECT_ROW_LEN

    prereqs = []
    for _ in range(prereq_count):
        prereqs.append(
            {
                "content_id": buf[off : off + 32].hex(),
                "disc_uuid": buf[off + 32 : off + 48],
            }
        )
        off += INDEX_PREREQ_ROW_LEN

    object_rows = [f for f in files if f["role"] == ROLE_OBJECT]
    if len(object_rows) != object_count:
        raise FormatError(
            f"{where}: {len(object_rows)} object file rows, object_count {object_count}"
        )
    return {
        "run_seq": run_seq,
        "file_count": file_count,
        "object_count": object_count,
        "prereq_count": prereq_count,
        "files": files,
        "objects": objects,
        "prereqs": prereqs,
        "object_rows": object_rows,
    }


def stream_bytes_from_index(idx: dict) -> int:
    """The byte length of the FEC stream: every stream file of INDEX, in
    row order, each padded with zero bytes to a multiple of the block
    size. The run header records the same number."""
    total = 0
    for f in idx["files"]:
        if f["role"] in STREAM_ROLES:
            total += -(-f["byte_len"] // BLOCK_SIZE) * BLOCK_SIZE
    return total


# ---------------------------------------------------------------------------
# REFS and DISCS: one container shape, two record kinds.

TABLE_HEADER_LEN = 56
REF_RECORD_LEN = 88
DISCS_ROW_LEN = 176


def _parse_table(buf: bytes, where: str, magic: bytes, record_len: int):
    common = decode_common_header(buf, where)
    check_magic_kind(common, magic, where)
    record_count = struct.unpack_from("<Q", buf, 48)[0]
    off = variable_area_start(common, TABLE_HEADER_LEN, where)
    total = off + record_count * record_len
    if len(buf) != total:
        raise FormatError(
            f"{where}: file is {len(buf)} bytes, {record_count} records need {total}"
        )
    return buf[32:48], record_count, off


def parse_refs(buf: bytes, where: str):
    repo_uuid, record_count, off = _parse_table(buf, where, MAGIC_REFS, REF_RECORD_LEN)
    records = []
    for _ in range(record_count):
        row = buf[off : off + REF_RECORD_LEN]
        time_sec = struct.unpack_from("<q", row, 32)[0]
        time_nsec, name_len = struct.unpack_from("<IH", row, 40)
        if name_len < 1 or name_len > 40:
            raise FormatError(f"{where}: ref name_len {name_len} outside 1 to 40")
        records.append(
            {
                "snapshot_id": row[0:32].hex(),
                "time_sec": time_sec,
                "time_nsec": time_nsec,
                "name_len": name_len,
                "name": row[48 : 48 + name_len].decode("utf-8", errors="replace"),
            }
        )
        off += REF_RECORD_LEN
    return {"repo_uuid": repo_uuid, "record_count": record_count, "records": records}


def latest_ref(records, name: str):
    """The newest record of a name: the highest time_sec, then the highest
    time_nsec, then the highest snapshot_id bytes."""
    matches = [r for r in records if r["name"] == name]
    if not matches:
        return None
    return max(
        matches, key=lambda r: (r["time_sec"], r["time_nsec"], bytes.fromhex(r["snapshot_id"]))
    )


def parse_discs(buf: bytes, where: str):
    repo_uuid, record_count, off = _parse_table(buf, where, MAGIC_DISCS, DISCS_ROW_LEN)
    rows = []
    for _ in range(record_count):
        row = buf[off : off + DISCS_ROW_LEN]
        run_seq, disc_seq = struct.unpack_from("<QQ", row, 0)
        created_sec = struct.unpack_from("<q", row, 64)[0]
        capacity_sectors = struct.unpack_from("<Q", row, 80)[0]
        label_len = struct.unpack_from("<H", row, 100)[0]
        if label_len > 64:
            raise FormatError(f"{where}: DISCS label_len {label_len} above 64")
        rows.append(
            {
                "run_seq": run_seq,
                "disc_seq": disc_seq,
                "disc_uuid": row[16:32],
                "run_hash": row[32:64],
                "created_sec": created_sec,
                "capacity_sectors": capacity_sectors,
                "label": row[102 : 102 + label_len].decode("utf-8", errors="replace"),
            }
        )
        off += DISCS_ROW_LEN
    return {"repo_uuid": repo_uuid, "record_count": record_count, "rows": rows}


# ---------------------------------------------------------------------------
# Checksum column block. This decoder holds no Reed-Solomon code, so it
# never repairs; it parses a block to report what a repair tool would
# use to locate a damaged data block.

CHECKSUM_BLOCK_LEN = 2048
CHECKSUM_DIGESTS_OFF = 20
CHECKSUM_DIGEST_SIZE = 8
CHECKSUM_DIGESTS_AREA_LEN = 1848


def parse_checksum_block(buf: bytes, where: str):
    if len(buf) < CHECKSUM_BLOCK_LEN:
        raise FormatError(f"{where}: short checksum block")
    if buf[0:8] != MAGIC_CHECKSUM:
        raise FormatError(f"{where}: not a checksum block")
    stripe_index = struct.unpack_from("<I", buf, 8)[0]
    digest_count = struct.unpack_from("<H", buf, 12)[0]
    header_crc32c = struct.unpack_from("<I", buf, 16)[0]
    if crc32c(buf[0:16]) != header_crc32c:
        raise FormatError(f"{where}: checksum block header_crc32c mismatch")
    if digest_count * CHECKSUM_DIGEST_SIZE > CHECKSUM_DIGESTS_AREA_LEN:
        raise FormatError(f"{where}: digest_count does not fit the digests area")
    digests = [
        buf[CHECKSUM_DIGESTS_OFF + i * 8 : CHECKSUM_DIGESTS_OFF + i * 8 + 8]
        for i in range(digest_count)
    ]
    return {
        "stripe_index": stripe_index,
        "digest_count": digest_count,
        "digests": digests,
    }


# ---------------------------------------------------------------------------
# Repository layout: the volume tree, and the fan-out on disc.


class NameCache:
    """Resolves a fixed on-disc name inside a directory case-
    insensitively, so a reader accepts a burner's output that folds
    every name to lowercase (plain ISO 9660 level 4, no Rock Ridge)
    alongside the exact case FORMAT.md defines. Caches each directory's
    own listing so repeated lookups do not rescan it."""

    def __init__(self):
        self._listings = {}

    def resolve(self, dir_path: str, want: str) -> str:
        if os.path.exists(os.path.join(dir_path, want)):
            return want
        names = self._listings.get(dir_path)
        if names is None:
            names = {}
            try:
                for entry in os.listdir(dir_path):
                    names[entry.lower()] = entry
            except OSError:
                pass
            self._listings[dir_path] = names
        return names.get(want.lower(), want)

    def join(self, dir_path: str, *parts: str) -> str:
        cur = dir_path
        for part in parts:
            cur = os.path.join(cur, self.resolve(cur, part))
        return cur


def find_noahsark_root(path: str, names: NameCache) -> str:
    base = os.path.normpath(path)
    for candidate in (base, os.path.join(base, names.resolve(base, "NOAHSARK"))):
        disc_name = names.resolve(candidate, "DISC.bin")
        if os.path.isfile(os.path.join(candidate, disc_name)):
            return candidate
    raise FormatError(f"{path}: no /NOAHSARK/DISC.bin found under this path")


def list_run_seqs(root: str, names: NameCache):
    runs_dir = names.join(root, "runs")
    if not os.path.isdir(runs_dir):
        return []
    return sorted(int(n) for n in os.listdir(runs_dir) if n.isdigit())


def run_dir(root: str, seq: int, names: NameCache) -> str:
    return names.join(root, "runs", f"{seq:010d}")


def text_form(id_hex: str) -> str:
    """Normalizes a content id to its 68-character multihash text form. A
    digest field inside a record holds the raw 32-byte digest, 64 hex
    characters; a file name already carries the sha2-256 multihash prefix
    "1220" and is 68 characters."""
    if len(id_hex) == 68:
        return id_hex
    if len(id_hex) == 64:
        return "1220" + id_hex
    raise FormatError(f"not a 32-byte digest or a multihash text form: {id_hex!r}")


def object_path(root: str, content_id_hex: str, names: NameCache, under: str = "objects") -> str:
    """The path of an object file. Chunks, blobs and trees live under
    objects/<d0d1>/, snapshots under snapshots/. The fan-out directory is
    the first two hex digits of the digest, which start after the
    4-character multihash prefix. Fan-out is always one level."""
    name = text_form(content_id_hex)
    if under == "snapshots":
        return os.path.join(names.join(root, "snapshots"), name)
    return os.path.join(names.join(root, "objects"), name[4:6], name)


# Fixed-name files, by their INDEX file role.
ROLE_PATHS = {
    ROLE_DISC: ("DISC.bin",),
    ROLE_README: ("README.txt",),
    ROLE_FORMAT: ("FORMAT.txt",),
    ROLE_REFERENCE: ("REFERENCE", "decoder.py"),
}
ROLE_RUN_PATHS = {
    ROLE_INDEX: ("INDEX.bin",),
    ROLE_RUN: ("RUN.bin",),
    ROLE_RUN2: ("RUN2.bin",),
    ROLE_REFS: ("catalog", "REFS.bin"),
    ROLE_DISCS: ("catalog", "DISCS.bin"),
    ROLE_CHECKSUM: ("checksum.bin",),
}


def role_path(root: str, seq: int, role: int, names: NameCache):
    if role in ROLE_PATHS:
        return names.join(root, *ROLE_PATHS[role])
    if role in ROLE_RUN_PATHS:
        return names.join(run_dir(root, seq, names), *ROLE_RUN_PATHS[role])
    return None


# ---------------------------------------------------------------------------
# Repo: ties the pieces above into one entry point.


class Repo:
    def __init__(self, path: str):
        self.names = NameCache()
        self.root = find_noahsark_root(path, self.names)
        disc_name = self.names.resolve(self.root, "DISC.bin")
        with open(os.path.join(self.root, disc_name), "rb") as f:
            self.disc = parse_disc(f.read(), "DISC.bin")
        self.run_seqs = list_run_seqs(self.root, self.names)
        if not self.run_seqs:
            raise FormatError(f"{self.root}: no runs found under runs/")
        self.newest_seq = self.run_seqs[-1]

    def _read(self, path: str) -> bytes:
        with open(path, "rb") as f:
            return f.read()

    def run_header(self, seq: int):
        """RUN.bin, or RUN2.bin when RUN.bin is unreadable or fails its
        checks. Both copies are byte-identical."""
        d = run_dir(self.root, seq, self.names)
        first = None
        for want in ("RUN.bin", "RUN2.bin"):
            path = os.path.join(d, self.names.resolve(d, want))
            try:
                return parse_run(self._read(path), path)
            except (FormatError, OSError) as e:
                first = first or e
        raise first

    def index(self, seq: int, run: dict = None):
        """INDEX.bin, verified against index_bytes and index_hash of the
        run header before any row is used."""
        d = run_dir(self.root, seq, self.names)
        path = os.path.join(d, self.names.resolve(d, "INDEX.bin"))
        buf = self._read(path)
        run = run or self.run_header(seq)
        if len(buf) != run["index_bytes"]:
            raise FormatError(
                f"{path}: INDEX is {len(buf)} bytes, index_bytes says {run['index_bytes']}"
            )
        if sha256(buf).digest() != run["index_hash"]:
            raise FormatError(f"{path}: index_hash does not match INDEX.bin")
        idx = parse_index(buf, path)
        if idx["run_seq"] != run["run_seq"]:
            raise FormatError(
                f"{path}: run_seq {idx['run_seq']} differs from the run header's "
                f"{run['run_seq']}"
            )
        return idx

    def hashed_file(self, seq: int, role: int, magic_parse, idx: dict = None):
        """Reads a fixed-name file and verifies it against the file_hash of
        its Files row, the only thing that covers REFS, DISCS, DISC.bin,
        README.txt, FORMAT.txt and decoder.py."""
        path = role_path(self.root, seq, role, self.names)
        buf = self._read(path)
        idx = idx if idx is not None else self.index(seq)
        rows = [f for f in idx["files"] if f["role"] == role]
        if not rows:
            raise FormatError(f"{path}: INDEX has no Files row of role {role}")
        if len(buf) != rows[0]["byte_len"] or sha256(buf).digest() != rows[0]["file_hash"]:
            raise FormatError(f"{path}: file_hash of the role {role} Files row does not match")
        return magic_parse(buf, path) if magic_parse else buf

    def refs(self, seq: int = None, idx: dict = None):
        seq = seq or self.newest_seq
        return self.hashed_file(seq, ROLE_REFS, parse_refs, idx)

    def discs(self, seq: int = None, idx: dict = None):
        seq = seq or self.newest_seq
        return self.hashed_file(seq, ROLE_DISCS, parse_discs, idx)

    def snapshot_names(self):
        """Every snapshot object of the repository lies under snapshots/,
        named by its content id. Every disc carries every snapshot."""
        d = self.names.join(self.root, "snapshots")
        if not os.path.isdir(d):
            return []
        return sorted(os.listdir(d))

    def read_object(self, content_id_hex: str, under: str = "objects", report: Report = None):
        return read_object_file(
            object_path(self.root, content_id_hex, self.names, under), report
        )

    def read_snapshot(self, name: str, report: Report = None):
        """name is a ref name, or a snapshot content id as a 68-character
        multihash text form or a 64-character raw digest."""
        if len(name) in (64, 68) and all(c in "0123456789abcdef" for c in name):
            obj = self.read_object(name, under="snapshots", report=report)
        else:
            ref = latest_ref(self.refs()["records"], name)
            if ref is None:
                raise FormatError(f"no ref named {name!r} in REFS")
            obj = self.read_object(ref["snapshot_id"], under="snapshots", report=report)
        if obj.payload is None:
            raise FormatError(f"snapshot {name!r} could not be expanded")
        snap = parse_snapshot(obj.payload, f"snapshot {name!r}", obj)
        snap["content_id"] = obj.content_id_hex
        return snap


# ---------------------------------------------------------------------------
# DiscSet: every disc root one command was given, read as one.


class DiscSet:
    """Every disc root a command was given, in the order given. An
    object that another given root holds is found there, so a snapshot
    that spans several discs reads as one. The first root is the one
    whose runs `verify` and `summary` report first."""

    def __init__(self, paths):
        self.repos = [Repo(p) for p in paths]
        self.primary = self.repos[0]
        self.root = self.primary.root
        self.names = self.primary.names
        self.newest_seq = self.primary.newest_seq
        self.run_seqs = self.primary.run_seqs
        self.disc = self.primary.disc

    def index(self, seq=None, run=None):
        return self.primary.index(seq or self.newest_seq, run)

    def discs(self, seq=None, idx=None):
        return self.primary.discs(seq, idx)

    def read_object(self, content_id_hex: str, under: str = "objects", report: Report = None):
        """The first given root that holds the object answers. A root
        that holds it but cannot expand it is damage, and its error is
        raised once every root has been tried."""
        damage = None
        for repo in self.repos:
            path = object_path(repo.root, content_id_hex, repo.names, under)
            if not os.path.exists(path):
                continue
            try:
                return read_object_file(path, report)
            except (FormatError, OSError) as e:
                damage = damage or e
        if damage is not None:
            raise damage
        raise FileNotFoundError(object_path(self.root, content_id_hex, self.names, under))

    def holds(self, content_id_hex: str, under: str = "objects") -> bool:
        return any(
            os.path.exists(object_path(r.root, content_id_hex, r.names, under))
            for r in self.repos
        )

    def refs(self, seq=None, idx=None):
        """The REFS of every given disc, merged by ref name, newest
        record of each name. A disc packed before a ref was made carries
        a shorter table, so no one disc's copy is trusted alone."""
        if len(self.repos) == 1:
            return self.primary.refs(seq, idx)
        merged = {}
        for repo in self.repos:
            for r in repo.refs()["records"]:
                cur = merged.get(r["name"])
                if cur is None or latest_ref([cur, r], r["name"]) is r:
                    merged[r["name"]] = r
        return {"repo_uuid": self.primary.disc["repo_uuid"], "records": list(merged.values())}

    def snapshot_names(self):
        names = set()
        for repo in self.repos:
            names.update(repo.snapshot_names())
        return sorted(names)

    def read_snapshot(self, name: str, report: Report = None):
        if len(name) in (64, 68) and all(c in "0123456789abcdef" for c in name):
            obj = self.read_object(name, under="snapshots", report=report)
        else:
            ref = latest_ref(self.refs()["records"], name)
            if ref is None:
                raise FormatError(f"no ref named {name!r} in REFS")
            obj = self.read_object(ref["snapshot_id"], under="snapshots", report=report)
        if obj.payload is None:
            raise FormatError(f"snapshot {name!r} could not be expanded")
        snap = parse_snapshot(obj.payload, f"snapshot {name!r}", obj)
        snap["content_id"] = obj.content_id_hex
        return snap

    def given_uuids(self):
        return {r.disc["disc_uuid"] for r in self.repos}

    def other_disc(self, content_id_hex: str):
        """Names the disc that holds content_id_hex, when a Prereqs row
        of some given disc names it and that disc was not given. Returns
        the operator's own words for the disc: number, label and uuid."""
        want = text_form(content_id_hex)
        given = self.given_uuids()
        for repo in self.repos:
            try:
                idx = repo.index(repo.newest_seq)
            except (FormatError, OSError):
                continue
            for pr in idx.get("prereqs", []):
                if text_form(pr["content_id"]) != want or pr["disc_uuid"] in given:
                    continue
                return describe_disc(repo, pr["disc_uuid"]) + ", which was not given"
        # No Prereqs row names it. The object still belongs to a disc of
        # this repository; DISCS names every disc that exists.
        others = self.ungiven_discs()
        if others:
            return "a disc that was not given; DISCS names " + ", ".join(others)
        return None

    def ungiven_discs(self):
        """Every disc a given disc's DISCS table names, and that was not
        itself given, in disc number order."""
        given = self.given_uuids()
        seen = {}
        for repo in self.repos:
            try:
                rows = repo.discs()["rows"]
            except (FormatError, OSError):
                continue
            for row in rows:
                if row["disc_uuid"] in given:
                    continue
                seen[row["disc_uuid"]] = (row["disc_seq"], describe_disc(repo, row["disc_uuid"]))
        return [text for _, text in sorted(seen.values())]


def describe_disc(repo: Repo, uuid: bytes) -> str:
    """disc NUMBER "LABEL" (UUID), from repo's own DISCS table when it
    names the disc, and the uuid alone when it does not."""
    try:
        for row in repo.discs()["rows"]:
            if row["disc_uuid"] == uuid:
                return f'disc {row["disc_seq"]} "{row["label"]}" ({uuid.hex()})'
    except (FormatError, OSError):
        pass
    return f"disc {uuid.hex()}"


# ---------------------------------------------------------------------------
# Walking a snapshot: pre-order, in tree entry order, descending into a
# directory before the next sibling entry.


def unescape_root_name(name: bytes) -> str:
    """Reverses the escape the root tree entry name carries: %2F, %5C,
    %00, %25. The root entry name is the one place the source path
    lives."""
    out = bytearray()
    i = 0
    while i < len(name):
        if name[i : i + 1] == b"%" and i + 3 <= len(name):
            code = name[i + 1 : i + 3]
            table = {b"2F": b"/", b"5C": b"\\", b"00": b"\0", b"25": b"%"}
            replacement = table.get(code.upper())
            if replacement is not None:
                out += replacement
                i += 3
                continue
        out.append(name[i])
        i += 1
    return out.decode("utf-8", errors="surrogateescape")


def walk_tree(repo, tree_id_hex: str, path_prefix: str, report: Report, visit, missing_note=False):
    """Depth-first, pre-order walk of one tree, in tree entry order. visit
    is called with (path, entry) for every entry before its children.
    With missing_note, a tree that no given disc holds, and that a
    Prereqs row puts on another disc, is a note that names that disc,
    not a failure; damaged bytes stay a failure."""
    try:
        obj = repo.read_object(tree_id_hex, under="objects", report=report)
        if obj.payload is None:
            report.fail(tree_id_hex, "tree did not verify or could not be expanded, walk stopped here")
            return
        tree = parse_tree(obj.payload, f"tree {tree_id_hex}", obj)
    except OSError as e:
        where = repo.other_disc(tree_id_hex)
        if missing_note and where:
            report.note(tree_id_hex, f"tree is on {where}; not checked")
        else:
            extra = f"; it is on {where}" if where else ""
            report.fail(tree_id_hex, f"tree could not be read, walk stopped here: {e}{extra}")
        return
    except FormatError as e:
        report.fail(tree_id_hex, f"tree could not be read, walk stopped here: {e}")
        return
    for entry in tree["entries"]:
        name = entry["name"]
        display_name = (
            unescape_root_name(name)
            if path_prefix == ""
            else name.decode("utf-8", errors="surrogateescape")
        )
        child_path = display_name if path_prefix == "" else f"{path_prefix}/{display_name}"
        visit(child_path, entry)
        if entry["entry_type"] == 2:
            walk_tree(repo, entry["content_id"], child_path, report, visit, missing_note)


def iter_file_chunks(repo: Repo, blob_id_hex: str, report: Report):
    """Yields (content_id_hex, length) of one file's chunks, in file
    order. A blob has one level: every entry names a chunk."""
    obj = repo.read_object(blob_id_hex, under="objects", report=report)
    if obj.payload is None:
        report.fail(blob_id_hex, "blob did not verify or could not be expanded")
        return
    blob = parse_blob(obj.payload, f"blob {blob_id_hex}", obj)
    for e in blob["entries"]:
        yield e["content_id"], e["length"]


# ---------------------------------------------------------------------------
# summary


def _fec_line(run: dict) -> str:
    scheme = FEC_SCHEME_NAMES.get(run["fec_scheme"], str(run["fec_scheme"]))
    if run["fec_scheme"] != 1:
        return f"fec:            {scheme}"
    geo = fec_geometry(run)
    return (
        f"fec:            {scheme}, k={run['fec_k']} m={run['fec_m']}, "
        f"{geo['column_blocks']} blocks per column\n"
        "                this decoder holds no Reed-Solomon code and cannot repair"
    )


def cmd_summary(args):
    repo = Repo(args.disc_root)
    d = repo.disc
    print(f"NOAHSARK root: {repo.root}")
    print(f"disc uuid:     {d['disc_uuid'].hex()}")
    print(f"repo uuid:     {d['repo_uuid'].hex()}")
    print(f"disc seq:      {d['disc_seq']}")
    print(f"label:         {d['label'].decode('utf-8', errors='replace')}")
    print(f"capacity:      {d['capacity_sectors']} sectors")
    print(f"runs found:    {repo.run_seqs}")

    run = repo.run_header(repo.newest_seq)
    print(f"\nnewest run seq: {run['run_seq']}")
    print(f"hash_algo:      0x{run['hash_algo']:02x}")
    print(f"created:        {run['created_sec']}")
    print(_fec_line(run))

    idx = repo.index(repo.newest_seq, run)
    print(f"\nINDEX for run {idx['run_seq']}:")
    print(f"  files:   {idx['file_count']}")
    print(f"  objects: {idx['object_count']}")
    print(f"  prereqs: {idx['prereq_count']}")

    refs = repo.refs(repo.newest_seq, idx)
    print(f"\nREFS: {refs['record_count']} records")
    discs = repo.discs(repo.newest_seq, idx)
    print(f"DISCS: {discs['record_count']} rows")
    for row in discs["rows"]:
        print(f"  run {row['run_seq']}: disc {row['disc_uuid'].hex()} label {row['label']}")

    print(f"\nsnapshot objects on this disc: {len(repo.snapshot_names())}")
    return 0


# ---------------------------------------------------------------------------
# list


def cmd_list(args):
    report = Report()
    repo = DiscSet(args.disc_root)

    if args.snapshot is None:
        names_by_snapshot = {}
        for r in repo.refs()["records"]:
            names_by_snapshot.setdefault(text_form(r["snapshot_id"]), []).append(r["name"])
        for name in repo.snapshot_names():
            snap = repo.read_snapshot(name, report=report)
            refnames = ", ".join(sorted(set(names_by_snapshot.get(name, []))))
            print(
                f"{name}  time={snap['time_sec']}  "
                f"parent={snap['parent'][:16]}...  refs=[{refnames}]"
            )
        return 0 if report.ok() else 1

    snap = repo.read_snapshot(args.snapshot, report=report)
    print(f"# snapshot {snap['content_id']}")
    print(f"# time {snap['time_sec']}")
    for m in snap["meta"]:
        tag_name = SNAPSHOT_META_TAG_NAMES.get(m["tag"], str(m["tag"]))
        if m["tag"] in (1, 2, 3):
            print(f"# {tag_name}: {m['value'].decode('utf-8', errors='replace')}")

    def visit(path, entry):
        type_name = ENTRY_TYPE_NAMES.get(entry["entry_type"], str(entry["entry_type"]))
        print(f"{type_name:9} {entry['size']:12} {oct(entry['mode']):7} {path}")

    walk_tree(repo, snap["root_tree"], "", report, visit)
    return 0 if report.ok() else 1


# ---------------------------------------------------------------------------
# verify


def cmd_verify(args):
    report = Report()
    discs_given = DiscSet(args.disc_root)
    for repo in discs_given.repos:
        verify_one_disc(repo, report)

    for name in discs_given.snapshot_names():
        try:
            snap = discs_given.read_snapshot(name, report=report)
            walk_tree(discs_given, snap["root_tree"], "", report, lambda p, e: None, True)
        except (FormatError, OSError) as e:
            report.fail(f"snapshot {name}", str(e))

    if report.ok():
        print("verify: all checks passed")
    else:
        print(f"verify: {len(report.failures)} check(s) failed", file=sys.stderr)
    return 0 if report.ok() else 1


def verify_one_disc(repo: Repo, report: Report):
    for seq in repo.run_seqs:
        try:
            run = repo.run_header(seq)
            idx = repo.index(seq, run)
        except (FormatError, OSError) as e:
            report.fail(f"run {seq}", str(e))
            continue

        want = stream_bytes_from_index(idx)
        if want != run["stream_bytes"]:
            report.fail(
                f"run {seq}",
                f"stream_bytes {run['stream_bytes']} differs from the {want} the "
                "Files rows add up to",
            )

        if run["fec_scheme"] == 1:
            print(
                f"run {seq}: this run carries parity; this decoder holds no "
                "Reed-Solomon code and cannot repair"
            )

        for role in HASHED_ROLES:
            try:
                repo.hashed_file(seq, role, None, idx)
            except (FormatError, OSError) as e:
                report.fail(f"run {seq} role {role}", str(e))

        try:
            refs = repo.refs(seq, idx)
            if refs["repo_uuid"] != run["repo_uuid"]:
                report.fail(f"run {seq} REFS", "repo_uuid differs from the run header's")
        except (FormatError, OSError) as e:
            report.fail(f"run {seq} REFS", str(e))
        try:
            discs = repo.discs(seq, idx)
            if discs["repo_uuid"] != run["repo_uuid"]:
                report.fail(f"run {seq} DISCS", "repo_uuid differs from the run header's")
        except (FormatError, OSError) as e:
            report.fail(f"run {seq} DISCS", str(e))

        for field in ("disc_uuid", "repo_uuid", "disc_seq"):
            if repo.disc[field] != run[field]:
                report.fail("DISC.bin", f"{field} differs from the run header's")

        # The j-th role 13 Files row describes the file of Objects row j.
        for row, obj_row in zip(idx["object_rows"], idx["objects"]):
            cid = obj_row["content_id"]
            under = "snapshots" if obj_row["kind"] == 4 else "objects"
            try:
                path = object_path(repo.root, cid, repo.names, under)
                if os.path.getsize(path) != row["byte_len"]:
                    report.fail(f"object {cid}", "file size differs from its Files row")
                obj = repo.read_object(cid, under=under, report=report)
                if obj.payload is not None and obj.content_id_hex != text_form(cid):
                    report.fail(f"object {cid}", "content id mismatch against INDEX")
            except FormatError as e:
                report.fail(f"object {cid}", str(e))
            except OSError:
                report.fail(f"object {cid}", "object file not found")


# ---------------------------------------------------------------------------
# restore


def restore_symlink(dest: str, target: bytes):
    target_str = target.decode("utf-8", errors="surrogateescape")
    if os.path.lexists(dest):
        os.remove(dest)
    os.symlink(target_str, dest)


def describe_missing_object(cid_hex: str, repo) -> str:
    """Adds context to an object-not-found message: names the disc that
    holds the object, when a Prereqs row of a given disc puts it on a
    disc that was not given."""
    where = repo.other_disc(cid_hex)
    return f"; it is on {where}" if where else ""


def restore_file(repo, dest: str, blob_id_hex: str, report: Report) -> bool:
    """Writes one regular file's chunks into dest, in file order. Writes
    to a temporary name beside dest and renames it into place only on
    full success, and removes the temporary file on any failure, so a
    file that could not be fully restored never looks complete. Returns
    whether it succeeded."""
    try:
        chunks = list(iter_file_chunks(repo, blob_id_hex, report))
    except (FormatError, OSError) as e:
        report.fail(
            dest,
            f"blob {blob_id_hex} could not be read: {e}"
            f"{describe_missing_object(blob_id_hex, repo)}",
        )
        return False

    tmp = dest + ".noahsark-partial"
    ok = True
    try:
        with open(tmp, "wb") as out:
            for content_id_hex, length in chunks:
                try:
                    obj = repo.read_object(content_id_hex, under="objects", report=report)
                except (FormatError, OSError) as e:
                    report.fail(
                        dest,
                        f"chunk {content_id_hex} could not be read: {e}"
                        f"{describe_missing_object(content_id_hex, repo)}",
                    )
                    ok = False
                    break
                if obj.payload is None:
                    report.fail(dest, f"chunk {content_id_hex} did not verify or could not be expanded")
                    ok = False
                    break
                if len(obj.payload) != length:
                    report.fail(dest, f"chunk {content_id_hex} length mismatch")
                    ok = False
                    break
                out.write(obj.payload)
    except OSError as e:
        report.fail(dest, f"could not write the file: {e}")
        ok = False

    if ok:
        os.replace(tmp, dest)
    else:
        try:
            os.remove(tmp)
        except OSError:
            pass
    return ok


def safe_dest(out_dir: str, path: str, report: Report):
    """Joins path under out_dir and refuses a result that would land
    outside out_dir. A root entry name is unescaped before it reaches
    this function (unescape_root_name), and the escape can turn a name
    that passed tree entry validation into one holding '/' or '..'
    segments; this is the last line of defense against writing outside
    --out."""
    out_abs = os.path.abspath(out_dir)
    candidate = os.path.abspath(os.path.join(out_abs, path.lstrip("/")))
    if candidate != out_abs and not candidate.startswith(out_abs + os.sep):
        report.fail(path, f"path would restore outside --out ({out_dir!r}); entry skipped")
        return None
    return candidate


def apply_metadata(dest: str, entry):
    try:
        os.chmod(dest, entry["mode"] & 0o7777, follow_symlinks=False)
    except (NotImplementedError, OSError):
        pass
    try:
        mtime = entry["mtime_sec"] + entry["mtime_nsec"] / 1e9
        os.utime(dest, (mtime, mtime), follow_symlinks=False)
    except (NotImplementedError, OSError):
        pass


def cmd_restore(args):
    report = Report()
    repo = DiscSet(args.disc_root)
    snap = repo.read_snapshot(args.snapshot, report=report)
    os.makedirs(args.out, exist_ok=True)

    counts = {"files": 0, "failed": 0}

    def visit(path, entry):
        # A root entry's decoded name is the source's absolute path; its
        # leading '/' is stripped so it joins under --out instead of
        # replacing it. safe_dest refuses a decoded name that would
        # otherwise escape --out.
        dest = safe_dest(args.out, path, report)
        if dest is None:
            if entry["entry_type"] == 1:
                counts["files"] += 1
                counts["failed"] += 1
            return
        for t in entry["tlvs"]:
            if t["type"] not in TLV_KNOWN:
                print(
                    f"note {path}: unknown TLV type 0x{t['type']:04x} kept, not applied",
                    file=sys.stderr,
                )
        entry_type = entry["entry_type"]
        try:
            if entry_type == 2:
                os.makedirs(dest, exist_ok=True)
                apply_metadata(dest, entry)
            elif entry_type == 1:
                counts["files"] += 1
                os.makedirs(os.path.dirname(dest), exist_ok=True)
                if restore_file(repo, dest, entry["content_id"], report):
                    apply_metadata(dest, entry)
                else:
                    counts["failed"] += 1
            elif entry_type == 3:
                target = b""
                for t in entry["tlvs"]:
                    if t["type"] == TLV_SYMLINK_TARGET:
                        target = t["payload"]
                os.makedirs(os.path.dirname(dest), exist_ok=True)
                restore_symlink(dest, target)
            else:
                report.fail(dest, f"entry type {entry_type} not restored by this decoder")
        except OSError as e:
            report.fail(dest, f"could not restore this entry: {e}")
            if entry_type == 1:
                counts["failed"] += 1

    walk_tree(repo, snap["root_tree"], "", report, visit)

    if report.ok():
        print(f"restore: wrote snapshot into {args.out}")
    else:
        print(
            f"restore: {counts['failed']} of {counts['files']} file(s) failed, "
            f"{len(report.failures)} problem(s) total",
            file=sys.stderr,
        )
    return 0 if report.ok() else 1


# ---------------------------------------------------------------------------
# CLI


def build_parser():
    p = argparse.ArgumentParser(
        prog="decoder.py",
        description="NoahsArk reference decoder: read a backup disc with no NoahsArk software.",
    )
    sub = p.add_subparsers(dest="command", required=True)

    sp = sub.add_parser("summary", help="print disc, run and catalog summary")
    sp.add_argument("disc_root")
    sp.set_defaults(func=cmd_summary)

    sp = sub.add_parser("list", help="list snapshots, or walk one snapshot's tree")
    sp.add_argument("disc_root", nargs="+", help="one or more disc roots")
    sp.add_argument("--snapshot", help="ref name or snapshot content id")
    sp.set_defaults(func=cmd_list)

    sp = sub.add_parser("verify", help="verify every structure and object this decoder can reach")
    sp.add_argument("disc_root", nargs="+", help="one or more disc roots")
    sp.set_defaults(func=cmd_verify)

    sp = sub.add_parser("restore", help="restore one snapshot into an output directory")
    sp.add_argument("disc_root", nargs="+", help="one or more disc roots")
    sp.add_argument("--snapshot", required=True, help="ref name or snapshot content id")
    sp.add_argument("--out", required=True, help="output directory")
    sp.set_defaults(func=cmd_restore)

    return p


def main(argv=None):
    args = build_parser().parse_args(argv)
    try:
        return args.func(args)
    except (FormatError, OSError) as e:
        print(f"error: {e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
