#!/usr/bin/env python3
"""NoahsArk reference decoder.

A standalone, standard-library-only Python 3 program that reads a NoahsArk
backup disc from a mounted directory (or from a copy of one on any local
filesystem) with no NoahsArk software installed. It parses the disc
superblock, a run's header and index, the REFS and DISCS catalog tables,
and every object it visits (chunk, blob, tree, snapshot); it verifies the
SHA-256 content id of every object it reads; and it can list, verify or
restore a snapshot.

It uses the standard library only. zstd-compressed chunks are expanded with
the compression.zstd module where the interpreter provides it (Python
3.14+), or with the zstd command-line tool otherwise; when neither is
available, a compressed chunk cannot be expanded and restore of the files
that use it fails, but listing and verification of everything else still
proceeds.

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
import binascii
import os
import stat
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

    def fail(self, where: str, message: str):
        self.failures.append(f"{where}: {message}")
        print(f"FAIL {where}: {message}", file=sys.stderr)

    def ok(self) -> bool:
        return not self.failures


# ---------------------------------------------------------------------------
# Magic values.

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

OBJECT_KIND_NAMES = {1: "chunk", 2: "blob", 3: "tree", 4: "snapshot"}
COMPRESSION_NAMES = {0: "none", 1: "zstd", 2: "lz4"}

ENTRY_TYPE_NAMES = {
    1: "regular",
    2: "directory",
    3: "symlink",
    4: "chardev",
    5: "blockdev",
    6: "fifo",
    7: "socket",
}

ENTRY_FLAG_ATIME_ABSENT = 1 << 1
ENTRY_FLAG_CTIME_ABSENT = 1 << 2
ENTRY_FLAG_BTIME_ABSENT = 1 << 3
ENTRY_FLAG_SPARSE = 1 << 4
ENTRY_FLAG_UNSTABLE = 1 << 7

TLV_SYMLINK_TARGET = 0x0001
TLV_USER_NAME = 0x0002
TLV_GROUP_NAME = 0x0003
TLV_ROOT_PATH = 0x0004


# ---------------------------------------------------------------------------
# Common header and object header.

COMMON_HEADER_LEN = 32
OBJECT_HEADER_LEN = 32


def decode_common_header(buf: bytes, where: str):
    if len(buf) < COMMON_HEADER_LEN:
        raise FormatError(f"{where}: buffer shorter than the common header")
    magic_project = buf[0:8]
    magic_kind = buf[8:16]
    version_major, version_minor, header_len, _reserved_u16 = struct.unpack_from(
        "<HHHH", buf, 16
    )
    if magic_project != PROJECT_MAGIC:
        raise FormatError(f"{where}: bad magic_project {magic_project!r}")
    if version_major != 1:
        raise FormatError(f"{where}: unsupported version_major {version_major}")
    return {
        "magic_kind": magic_kind,
        "version_major": version_major,
        "version_minor": version_minor,
        "header_len": header_len,
    }


def object_header_fields(body: bytes):
    """Parses the 32-byte object header body alone, with no CRC check."""
    if len(body) < OBJECT_HEADER_LEN:
        raise FormatError("buffer shorter than the object header")
    kind, hash_algo, digest_len, compression, crypto, _r8, _r16 = struct.unpack_from(
        "<BBBBBBH", body, 0
    )
    payload_len, stored_len = struct.unpack_from("<QQ", body, 8)
    header_crc32c = struct.unpack_from("<I", body, 24)[0]
    return {
        "kind": kind,
        "hash_algo": hash_algo,
        "digest_len": digest_len,
        "compression": compression,
        "crypto": crypto,
        "payload_len": payload_len,
        "stored_len": stored_len,
        "header_crc32c": header_crc32c,
    }


def decode_object_header(buf: bytes, where: str, verify_crc: bool = True):
    """Parses the object header that follows the common header in buf, and,
    when verify_crc is set, verifies header_crc32c against the common
    header and object header bytes it covers."""
    if len(buf) < COMMON_HEADER_LEN + OBJECT_HEADER_LEN:
        raise FormatError(f"{where}: buffer shorter than the object header")
    body = buf[COMMON_HEADER_LEN : COMMON_HEADER_LEN + OBJECT_HEADER_LEN]
    fields = object_header_fields(body)
    if not verify_crc:
        return fields
    covered = buf[0 : COMMON_HEADER_LEN + 24]
    if crc32c(covered) != fields["header_crc32c"]:
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


def zstd_decompress(data: bytes, payload_len: int) -> bytes:
    mod = _zstd_module()
    if mod is not None:
        out = mod.decompress(data)
        if len(out) != payload_len:
            raise FormatError(
                f"decompressed length {len(out)} does not match payload_len {payload_len}"
            )
        return out
    tool = _which("zstd")
    if tool is None:
        raise ZstdUnavailable(
            "no compression.zstd module and no zstd command-line tool found; "
            "cannot expand this compressed chunk"
        )
    proc = subprocess.run(
        [tool, "-d", "-c", "-q"], input=data, stdout=subprocess.PIPE, check=True
    )
    out = proc.stdout
    if len(out) != payload_len:
        raise FormatError(
            f"decompressed length {len(out)} does not match payload_len {payload_len}"
        )
    return out


def _which(name: str):
    for d in os.environ.get("PATH", "").split(os.pathsep):
        candidate = os.path.join(d, name)
        if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate
    return None


# ---------------------------------------------------------------------------
# Generic object file reader. Common to chunk, blob, tree and snapshot.


class ObjectFile:
    """A decoded object file: its headers, and its uncompressed payload."""

    def __init__(self, common, obj_header, payload: bytes, content_id_hex: str):
        self.common = common
        self.obj_header = obj_header
        self.payload = payload
        self.content_id_hex = content_id_hex


def decode_object_bytes(
    buf: bytes, where: str, report: Report = None, verify_crc: bool = True
) -> ObjectFile:
    """Decodes one object file's bytes: common header, object header, and
    the uncompressed payload. Computes the content id text form but does
    not compare it against any file name; read_object_file does that."""
    common = decode_common_header(buf, where)
    obj_header = decode_object_header(buf, where, verify_crc=verify_crc)
    fixed_len = COMMON_HEADER_LEN + OBJECT_HEADER_LEN
    stored = buf[fixed_len : fixed_len + obj_header["stored_len"]]
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

    text_form = "1220" + sha256(payload).hexdigest()  # the text form
    return ObjectFile(common, obj_header, payload, text_form)


def read_object_file(path: str, report: Report = None) -> ObjectFile:
    """Reads an object file, verifies its header CRC and its content id
    against its file name, and returns the decoded payload bytes."""
    with open(path, "rb") as f:
        buf = f.read()
    where = path
    obj = decode_object_bytes(buf, where, report=report)
    if obj.payload is None:
        return obj
    name = os.path.basename(path)
    if name != obj.content_id_hex:
        message = f"content id mismatch: file name {name}, computed {obj.content_id_hex}"
        if report is not None:
            report.fail(where, message)
            return ObjectFile(obj.common, obj.obj_header, obj.payload, None)
        raise FormatError(f"{where}: {message}")
    return obj


# ---------------------------------------------------------------------------
# Chunk: opaque payload bytes. Nothing further to parse.


# ---------------------------------------------------------------------------
# Blob.

BLOB_ENTRY_LEN = 48


def parse_blob(payload: bytes, where: str):
    entry_count, total_size = struct.unpack_from("<QQ", payload, 0)
    entry_size, hash_algo, digest_len, level = struct.unpack_from(
        "<HBBB", payload, 16
    )
    if level > 1:
        raise FormatError(f"{where}: blob level {level} above 1")
    entries = []
    off = 24
    for _ in range(entry_count):
        content_id = payload[off : off + 32]
        length, file_offset = struct.unpack_from("<QQ", payload, off + 32)
        entries.append(
            {
                "content_id": content_id.hex(),
                "length": length,
                "file_offset": file_offset,
            }
        )
        off += BLOB_ENTRY_LEN
    return {
        "entry_count": entry_count,
        "total_size": total_size,
        "entry_size": entry_size,
        "level": level,
        "entries": entries,
    }


# ---------------------------------------------------------------------------
# Tree, tree entry and TLV.

TREE_ENTRY_HEADER_LEN = 112


def _align8(n: int) -> int:
    return (n + 7) & ~7


def parse_tlvs(buf: bytes, where: str):
    tlvs = []
    p = 0
    while p < len(buf):
        if p + 8 > len(buf):
            raise FormatError(f"{where}: truncated TLV prefix")
        tlv_type, flags, tlv_len = struct.unpack_from("<HHI", buf, p)
        n = _align8(8 + tlv_len)
        if p + n > len(buf):
            raise FormatError(f"{where}: truncated TLV payload")
        payload = buf[p + 8 : p + 8 + tlv_len]
        pad = buf[p + 8 + tlv_len : p + n]
        if any(pad):
            raise FormatError(f"{where}: nonzero TLV padding")
        tlvs.append({"type": tlv_type, "flags": flags, "payload": payload})
        p += n
    return tlvs


def parse_tree_entry(buf: bytes, where: str):
    entry_len = struct.unpack_from("<I", buf, 0)[0]
    if entry_len < TREE_ENTRY_HEADER_LEN or entry_len % 8 != 0 or entry_len > len(buf):
        raise FormatError(f"{where}: bad entry_len {entry_len}")
    entry_type = buf[6]
    entry_flags = buf[7]
    size = struct.unpack_from("<Q", buf, 8)[0]
    mtime_sec = struct.unpack_from("<q", buf, 24)[0]
    atime_sec = struct.unpack_from("<q", buf, 32)[0]
    ctime_sec = struct.unpack_from("<q", buf, 40)[0]
    btime_sec = struct.unpack_from("<q", buf, 48)[0]
    mtime_nsec, atime_nsec, ctime_nsec, btime_nsec = struct.unpack_from(
        "<IIII", buf, 56
    )
    mode, uid, gid, rdev_major, rdev_minor = struct.unpack_from("<IIIII", buf, 72)
    content_off, content_len, ext_off, ext_len = struct.unpack_from("<IIII", buf, 92)
    name_off, name_len = struct.unpack_from("<HH", buf, 108)

    if name_len < 1 or name_len > 4095 or name_off + name_len > entry_len:
        raise FormatError(f"{where}: bad name_len {name_len}")
    name = buf[name_off : name_off + name_len]
    if name in (b".", b"..") or b"/" in name or b"\\" in name or 0 in name:
        raise FormatError(f"{where}: invalid entry name {name!r}")

    content_id = None
    if content_len:
        content_id = buf[content_off : content_off + content_len].hex()

    tlvs = []
    if ext_len:
        tlvs = parse_tlvs(buf[ext_off : ext_off + ext_len], where)

    return {
        "entry_len": entry_len,
        "entry_type": entry_type,
        "entry_flags": entry_flags,
        "size": size,
        "mtime_sec": mtime_sec,
        "atime_sec": atime_sec,
        "ctime_sec": ctime_sec,
        "btime_sec": btime_sec,
        "mtime_nsec": mtime_nsec,
        "atime_nsec": atime_nsec,
        "ctime_nsec": ctime_nsec,
        "btime_nsec": btime_nsec,
        "mode": mode,
        "uid": uid,
        "gid": gid,
        "rdev_major": rdev_major,
        "rdev_minor": rdev_minor,
        "name": name,
        "content_id": content_id,
        "tlvs": tlvs,
    }


def parse_tree(payload: bytes, where: str):
    entry_count, reserved_u32 = struct.unpack_from("<II", payload, 0)
    if reserved_u32 != 0:
        raise FormatError(f"{where}: tree reserved_u32 not zero")
    entries = []
    pos = 8
    prev_key = None
    for _ in range(entry_count):
        e = parse_tree_entry(payload[pos:], where)
        key = e["name"] + (b"/" if e["entry_type"] == 2 else b"")
        if prev_key is not None and prev_key >= key:
            raise FormatError(f"{where}: tree entries not in ascending order")
        prev_key = key
        entries.append(e)
        pos += e["entry_len"]
    return {"entry_count": entry_count, "entries": entries}


# ---------------------------------------------------------------------------
# Snapshot.

SNAPSHOT_FIXED_LEN = 112

SNAPSHOT_META_TAG_NAMES = {
    1: "author",
    2: "host",
    3: "message",
    4: "source_root",
    5: "exclude_rules",
    6: "checksum_commit",
}


def parse_snapshot_meta(buf: bytes, where: str):
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
        value = buf[p + 8 : p + 8 + value_len]
        pad = buf[p + 8 + value_len : p + total]
        if any(pad):
            raise FormatError(f"{where}: nonzero snapshot meta padding")
        records.append({"tag": tag, "flags": flags, "value": value})
        p += total
    return records


def parse_snapshot(payload: bytes, where: str):
    root_tree = payload[0:32].hex()
    parent = payload[32:64].hex()
    generation = struct.unpack_from("<Q", payload, 64)[0]
    time_sec = struct.unpack_from("<q", payload, 72)[0]
    time_nsec, tz_offset_sec = struct.unpack_from("<Ii", payload, 80)
    total_size, reachable_object_count = struct.unpack_from("<QQ", payload, 88)
    hash_algo, chunker_profile = struct.unpack_from("<BB", payload, 104)
    meta_count = struct.unpack_from("<H", payload, 106)[0]
    source_type, source_flags, parent_hash_algo, reserved_u8 = struct.unpack_from(
        "<BBBB", payload, 108
    )
    if reserved_u8 != 0:
        raise FormatError(f"{where}: snapshot reserved_u8 not zero")
    meta = parse_snapshot_meta(payload[SNAPSHOT_FIXED_LEN:], where)
    if len(meta) != meta_count:
        raise FormatError(
            f"{where}: meta_count {meta_count} does not match {len(meta)} records found"
        )
    return {
        "root_tree": root_tree,
        "parent": parent,
        "generation": generation,
        "time_sec": time_sec,
        "time_nsec": time_nsec,
        "tz_offset_sec": tz_offset_sec,
        "total_size": total_size,
        "reachable_object_count": reachable_object_count,
        "hash_algo": hash_algo,
        "chunker_profile": chunker_profile,
        "source_type": source_type,
        "source_flags": source_flags,
        "parent_hash_algo": parent_hash_algo,
        "meta": meta,
    }


# ---------------------------------------------------------------------------
# Disc superblock. 2048 bytes, never compressed, no object
# header: it carries only the common header before its own fixed body.

DISC_LEN = 2048


def parse_disc(buf: bytes, where: str):
    if len(buf) < DISC_LEN:
        raise FormatError(f"{where}: short disc superblock")
    common = decode_common_header(buf, where)
    if common["magic_kind"] != MAGIC_DISC:
        raise FormatError(f"{where}: not a disc superblock")
    disc_uuid = buf[32:48]
    repo_uuid = buf[48:64]
    disc_seq, capacity_sectors, capacity_forced_sectors = struct.unpack_from(
        "<QQQ", buf, 64
    )
    prev_disc_super_hash = buf[88:120]
    created_sec = struct.unpack_from("<q", buf, 120)[0]
    created_nsec, tz_offset_sec = struct.unpack_from("<Ii", buf, 128)
    media_type, fs_profile, fanout_levels, capacity_is_forced, sealed = buf[136:141]
    label_len = struct.unpack_from("<I", buf, 144)[0]
    label = buf[148 : 148 + min(label_len, 64)]
    tool_version = struct.unpack_from("<I", buf, 212)[0]
    super_crc32c = struct.unpack_from("<I", buf, 2044)[0]
    if crc32c(buf[0:2044]) != super_crc32c:
        raise FormatError(f"{where}: super_crc32c mismatch")
    return {
        "disc_uuid": disc_uuid,
        "repo_uuid": repo_uuid,
        "disc_seq": disc_seq,
        "capacity_sectors": capacity_sectors,
        "capacity_forced_sectors": capacity_forced_sectors,
        "prev_disc_super_hash": prev_disc_super_hash,
        "created_sec": created_sec,
        "created_nsec": created_nsec,
        "tz_offset_sec": tz_offset_sec,
        "media_type": media_type,
        "fs_profile": fs_profile,
        "fanout_levels": fanout_levels,
        "capacity_is_forced": capacity_is_forced,
        "sealed": sealed,
        "label_len": label_len,
        "label": label,
        "tool_version": tool_version,
    }


# ---------------------------------------------------------------------------
# Run header. 512 bytes of structure inside a 2048-byte file;
# bytes 512 to 2047 are zero padding, not part of the structure.

RUN_LEN = 512


def parse_run(buf: bytes, where: str):
    if len(buf) < RUN_LEN:
        raise FormatError(f"{where}: short run header")
    common = decode_common_header(buf, where)
    if common["magic_kind"] != MAGIC_RUN:
        raise FormatError(f"{where}: not a run header")
    disc_uuid = buf[32:48]
    repo_uuid = buf[48:64]
    run_seq, disc_seq = struct.unpack_from("<QQ", buf, 64)
    fec_k, fec_m = struct.unpack_from("<HH", buf, 80)
    fec_scheme, hash_algo, chunker_profile, compression, fs_profile, run_kind, run_flags = (
        buf[84:91]
    )
    index_bytes = struct.unpack_from("<Q", buf, 96)[0]
    index_hash = buf[104:136]
    stream_bytes = struct.unpack_from("<Q", buf, 136)[0]
    prev_run_hash = buf[144:176]
    created_sec = struct.unpack_from("<q", buf, 176)[0]
    created_nsec, tool_version = struct.unpack_from("<II", buf, 184)
    disc_object_count = struct.unpack_from("<Q", buf, 192)[0]
    disc_run_index = struct.unpack_from("<I", buf, 200)[0]
    header_crc32c = struct.unpack_from("<I", buf, 504)[0]
    if crc32c(buf[0:504]) != header_crc32c:
        raise FormatError(f"{where}: run header_crc32c mismatch")
    return {
        "disc_uuid": disc_uuid,
        "repo_uuid": repo_uuid,
        "run_seq": run_seq,
        "disc_seq": disc_seq,
        "fec_k": fec_k,
        "fec_m": fec_m,
        "fec_scheme": fec_scheme,
        "hash_algo": hash_algo,
        "chunker_profile": chunker_profile,
        "compression": compression,
        "run_kind": run_kind,
        "run_flags": run_flags,
        "index_bytes": index_bytes,
        "index_hash": index_hash,
        "stream_bytes": stream_bytes,
        "prev_run_hash": prev_run_hash,
        "created_sec": created_sec,
        "created_nsec": created_nsec,
        "tool_version": tool_version,
        "disc_object_count": disc_object_count,
        "disc_run_index": disc_run_index,
    }


# ---------------------------------------------------------------------------
# INDEX.

INDEX_FIXED_BODY_LEN = 48
INDEX_HEADER_LEN = COMMON_HEADER_LEN + INDEX_FIXED_BODY_LEN
INDEX_FILE_RECORD_LEN = 48
INDEX_OBJECT_RECORD_LEN = 72
INDEX_PREREQ_RECORD_LEN = 40


def parse_index(buf: bytes, where: str):
    if len(buf) < INDEX_HEADER_LEN:
        raise FormatError(f"{where}: short INDEX header")
    common = decode_common_header(buf, where)
    if common["magic_kind"] != MAGIC_INDEX:
        raise FormatError(f"{where}: not an INDEX")
    run_seq = struct.unpack_from("<Q", buf, 32)[0]
    file_count, object_count, prereq_count = struct.unpack_from("<III", buf, 40)
    hash_algo, digest_len = buf[58:60]
    container_len = struct.unpack_from("<Q", buf, 64)[0]
    body_crc32c, header_crc32c = struct.unpack_from("<II", buf, 72)
    if crc32c(buf[0:76]) != header_crc32c:
        raise FormatError(f"{where}: INDEX header_crc32c mismatch")

    total = (
        INDEX_HEADER_LEN
        + file_count * INDEX_FILE_RECORD_LEN
        + object_count * INDEX_OBJECT_RECORD_LEN
        + prereq_count * INDEX_PREREQ_RECORD_LEN
    )
    if len(buf) < total:
        raise FormatError(f"{where}: INDEX buffer shorter than container_len")
    if crc32c(buf[INDEX_HEADER_LEN:total]) != body_crc32c:
        raise FormatError(f"{where}: INDEX body_crc32c mismatch")

    off = INDEX_HEADER_LEN
    files = []
    for _ in range(file_count):
        row = buf[off : off + INDEX_FILE_RECORD_LEN]
        file_hash = row[0:32]
        byte_len = struct.unpack_from("<Q", row, 32)[0]
        role = row[40]
        files.append({"file_hash": file_hash, "byte_len": byte_len, "role": role})
        off += INDEX_FILE_RECORD_LEN

    objects = []
    for _ in range(object_count):
        row = buf[off : off + INDEX_OBJECT_RECORD_LEN]
        content_id = row[0:32]
        file_index = struct.unpack_from("<I", row, 32)[0]
        offset, stored_len, payload_len = struct.unpack_from("<QQQ", row, 40)
        kind, compression = row[64:66]
        flags = struct.unpack_from("<H", row, 66)[0]
        if kind < 1 or kind > 4:
            raise FormatError(f"{where}: Objects row kind {kind} outside 1 to 4")
        objects.append(
            {
                "content_id": content_id.hex(),
                "file_index": file_index,
                "offset": offset,
                "stored_len": stored_len,
                "payload_len": payload_len,
                "kind": kind,
                "compression": compression,
                "flags": flags,
            }
        )
        off += INDEX_OBJECT_RECORD_LEN

    prereqs = []
    for _ in range(prereq_count):
        row = buf[off : off + INDEX_PREREQ_RECORD_LEN]
        content_id = row[0:32]
        run_seq_p = struct.unpack_from("<Q", row, 32)[0]
        prereqs.append({"content_id": content_id.hex(), "run_seq": run_seq_p})
        off += INDEX_PREREQ_RECORD_LEN

    return {
        "run_seq": run_seq,
        "file_count": file_count,
        "object_count": object_count,
        "prereq_count": prereq_count,
        "hash_algo": hash_algo,
        "digest_len": digest_len,
        "container_len": container_len,
        "files": files,
        "objects": objects,
        "prereqs": prereqs,
    }


# ---------------------------------------------------------------------------
# REFS, and the Ref object.

REFS_FIXED_BODY_LEN = 40
REFS_HEADER_LEN = COMMON_HEADER_LEN + REFS_FIXED_BODY_LEN
REF_RECORD_LEN = 96


def parse_refs(buf: bytes, where: str):
    if len(buf) < REFS_HEADER_LEN:
        raise FormatError(f"{where}: short REFS header")
    common = decode_common_header(buf, where)
    if common["magic_kind"] != MAGIC_REFS:
        raise FormatError(f"{where}: not a REFS table")
    repo_uuid = buf[32:48]
    record_count = struct.unpack_from("<Q", buf, 48)[0]
    record_size, hash_algo, digest_len = struct.unpack_from("<HBB", buf, 56)
    body_crc32c, header_crc32c = struct.unpack_from("<II", buf, 64)
    if crc32c(buf[0:68]) != header_crc32c:
        raise FormatError(f"{where}: REFS header_crc32c mismatch")

    total = REFS_HEADER_LEN + record_count * REF_RECORD_LEN
    if len(buf) < total:
        raise FormatError(f"{where}: REFS buffer shorter than its record count")
    if crc32c(buf[REFS_HEADER_LEN:total]) != body_crc32c:
        raise FormatError(f"{where}: REFS body_crc32c mismatch")

    off = REFS_HEADER_LEN
    records = []
    for _ in range(record_count):
        row = buf[off : off + REF_RECORD_LEN]
        snapshot_id = row[0:32]
        time_sec = struct.unpack_from("<q", row, 32)[0]
        time_nsec, name_len = struct.unpack_from("<IH", row, 40)
        ref_hash_algo = row[46]
        name = row[48:88].rstrip(b"\0")
        run_seq = struct.unpack_from("<Q", row, 88)[0]
        records.append(
            {
                "snapshot_id": snapshot_id.hex(),
                "time_sec": time_sec,
                "time_nsec": time_nsec,
                "name_len": name_len,
                "hash_algo": ref_hash_algo,
                "name": name.decode("utf-8", errors="replace"),
                "run_seq": run_seq,
            }
        )
        off += REF_RECORD_LEN

    return {"repo_uuid": repo_uuid, "record_count": record_count, "records": records}


def latest_ref(records, name: str):
    """The newest on-disc ref record for name: highest run_seq, then
    highest time_sec, then highest time_nsec, the Ref object's ordering
    rule."""
    matches = [r for r in records if r["name"] == name and r["run_seq"] != 0]
    if not matches:
        return None
    return max(matches, key=lambda r: (r["run_seq"], r["time_sec"], r["time_nsec"]))


# ---------------------------------------------------------------------------
# DISCS.

DISCS_FIXED_BODY_LEN = 40
DISCS_HEADER_LEN = COMMON_HEADER_LEN + DISCS_FIXED_BODY_LEN
DISCS_ROW_LEN = 176

RUN_STATUS_NAMES = {1: "verified", 2: "burned, not yet verified", 3: "withdrawn"}
HEALTH_NAMES = {
    1: "healthy",
    2: "degraded",
    3: "critical",
    4: "failed",
    5: "unknown",
    6: "unverified, this disc",
}


def parse_discs(buf: bytes, where: str):
    if len(buf) < DISCS_HEADER_LEN:
        raise FormatError(f"{where}: short DISCS header")
    common = decode_common_header(buf, where)
    if common["magic_kind"] != MAGIC_DISCS:
        raise FormatError(f"{where}: not a DISCS table")
    repo_uuid = buf[32:48]
    record_count = struct.unpack_from("<Q", buf, 48)[0]
    record_size, hash_algo, digest_len = struct.unpack_from("<HBB", buf, 56)
    body_crc32c, header_crc32c = struct.unpack_from("<II", buf, 64)
    if crc32c(buf[0:68]) != header_crc32c:
        raise FormatError(f"{where}: DISCS header_crc32c mismatch")

    total = DISCS_HEADER_LEN + record_count * DISCS_ROW_LEN
    if len(buf) < total:
        raise FormatError(f"{where}: DISCS buffer shorter than its record count")
    if crc32c(buf[DISCS_HEADER_LEN:total]) != body_crc32c:
        raise FormatError(f"{where}: DISCS body_crc32c mismatch")

    off = DISCS_HEADER_LEN
    rows = []
    for _ in range(record_count):
        row = buf[off : off + DISCS_ROW_LEN]
        run_seq, disc_seq = struct.unpack_from("<QQ", row, 0)
        disc_uuid = row[16:32]
        run_hash = row[32:64]
        created_sec, last_verify_sec = struct.unpack_from("<qq", row, 64)
        capacity_sectors, used_sectors = struct.unpack_from("<QQ", row, 80)
        run_status, health = row[96:98]
        rs_margin_percent, label_len = struct.unpack_from("<HH", row, 98)
        label = row[102:166].rstrip(b"\0")
        state_flags = row[166]
        capacity_forced_sectors = struct.unpack_from("<Q", row, 168)[0]
        rows.append(
            {
                "run_seq": run_seq,
                "disc_seq": disc_seq,
                "disc_uuid": disc_uuid,
                "run_hash": run_hash,
                "created_sec": created_sec,
                "last_verify_sec": last_verify_sec,
                "capacity_sectors": capacity_sectors,
                "used_sectors": used_sectors,
                "run_status": run_status,
                "health": health,
                "rs_margin_percent": rs_margin_percent,
                "label": label.decode("utf-8", errors="replace"),
                "state_flags": state_flags,
                "capacity_forced_sectors": capacity_forced_sectors,
            }
        )
        off += DISCS_ROW_LEN

    return {"repo_uuid": repo_uuid, "record_count": record_count, "rows": rows}


# ---------------------------------------------------------------------------
# Checksum column record. The decoder does not use this
# structure for verify or restore; it is parsed for completeness, since a
# scrub tool would need it to find a damaged block.

CHECKSUM_RECORD_LEN = 2048
CHECKSUM_DIGESTS_AREA_LEN = 1848
CHECKSUM_DIGEST_SIZE = 8
CHECKSUM_HEADER_LEN = 20
MAGIC_CHECKSUM = _magic(b"CHECKSUM")


def parse_checksum_record(buf: bytes, where: str):
    if len(buf) < CHECKSUM_RECORD_LEN:
        raise FormatError(f"{where}: short checksum record")
    magic_kind = buf[0:8]
    if magic_kind != MAGIC_CHECKSUM:
        raise FormatError(f"{where}: not a checksum record")
    stripe_index = struct.unpack_from("<I", buf, 8)[0]
    digest_count, digest_bytes, hash_algo = struct.unpack_from("<HBB", buf, 12)
    header_crc32c = struct.unpack_from("<I", buf, 16)[0]
    if crc32c(buf[0:16]) != header_crc32c:
        raise FormatError(f"{where}: checksum record header_crc32c mismatch")

    digests_area = buf[CHECKSUM_HEADER_LEN : CHECKSUM_HEADER_LEN + CHECKSUM_DIGESTS_AREA_LEN]
    used = digest_count * CHECKSUM_DIGEST_SIZE
    if used > CHECKSUM_DIGESTS_AREA_LEN:
        raise FormatError(f"{where}: digest_count does not fit the digests area")
    digests = [digests_area[i : i + 8] for i in range(0, used, CHECKSUM_DIGEST_SIZE)]
    if any(digests_area[used:]):
        raise FormatError(f"{where}: nonzero padding after the digests")

    reserved = buf[CHECKSUM_HEADER_LEN + CHECKSUM_DIGESTS_AREA_LEN : CHECKSUM_RECORD_LEN]
    if any(reserved):
        raise FormatError(f"{where}: checksum record reserved area not zero")

    return {
        "stripe_index": stripe_index,
        "digest_count": digest_count,
        "digest_bytes": digest_bytes,
        "hash_algo": hash_algo,
        "digests": digests,
    }


# ---------------------------------------------------------------------------
# Repository layout helpers: files at the volume root, and fan-out on disc.


def find_noahsark_root(path: str) -> str:
    if os.path.basename(os.path.normpath(path)) == "NOAHSARK" and os.path.isfile(
        os.path.join(path, "DISC.bin")
    ):
        return path
    candidate = os.path.join(path, "NOAHSARK")
    if os.path.isfile(os.path.join(candidate, "DISC.bin")):
        return candidate
    raise FormatError(f"{path}: no /NOAHSARK/DISC.bin found under this path")


def list_run_seqs(root: str):
    runs_dir = os.path.join(root, "runs")
    if not os.path.isdir(runs_dir):
        return []
    seqs = []
    for name in os.listdir(runs_dir):
        if name.isdigit():
            seqs.append(int(name))
    return sorted(seqs)


def run_dir(root: str, seq: int) -> str:
    return os.path.join(root, "runs", f"{seq:010d}")


def text_form(id_hex: str) -> str:
    """Normalizes a content id to its 68-character multihash text form.
    A digest field inside a record is the raw 32-byte digest,
    64 hex characters; an on-disk file name already carries the 4-character
    sha2-256 multihash prefix "1220" and is 68 characters."""
    if len(id_hex) == 68:
        return id_hex
    if len(id_hex) == 64:
        return "1220" + id_hex
    raise FormatError(f"not a 32-byte digest or a multihash text form: {id_hex!r}")


def object_path(root: str, content_id_hex: str, fanout_levels: int, under: str = "objects") -> str:
    """The path of an object file under objects/ or snapshots/, the fan-out
    on disc rule."""
    name = text_form(content_id_hex)
    if under == "snapshots":
        return os.path.join(root, "snapshots", name)
    d0d1 = name[4:6]  # digest starts after the 4-hex multihash prefix
    if fanout_levels >= 2:
        d2d3 = name[6:8]
        return os.path.join(root, "objects", d0d1, d2d3, name)
    return os.path.join(root, "objects", d0d1, name)


# ---------------------------------------------------------------------------
# Repo: ties the pieces above into one entry point.


class Repo:
    def __init__(self, path: str):
        self.root = find_noahsark_root(path)
        with open(os.path.join(self.root, "DISC.bin"), "rb") as f:
            self.disc = parse_disc(f.read(), "DISC.bin")
        self.run_seqs = list_run_seqs(self.root)
        if not self.run_seqs:
            raise FormatError(f"{self.root}: no runs found under runs/")
        self.newest_seq = self.run_seqs[-1]

    def run_header(self, seq: int):
        path = os.path.join(run_dir(self.root, seq), "RUN.bin")
        with open(path, "rb") as f:
            return parse_run(f.read(), path)

    def index(self, seq: int):
        path = os.path.join(run_dir(self.root, seq), "INDEX.bin")
        with open(path, "rb") as f:
            return parse_index(f.read(), path)

    def refs(self, seq: int = None):
        seq = seq or self.newest_seq
        path = os.path.join(run_dir(self.root, seq), "catalog", "REFS.bin")
        with open(path, "rb") as f:
            return parse_refs(f.read(), path)

    def discs(self, seq: int = None):
        seq = seq or self.newest_seq
        path = os.path.join(run_dir(self.root, seq), "catalog", "DISCS.bin")
        with open(path, "rb") as f:
            return parse_discs(f.read(), path)

    def snapshot_names(self, seq: int = None):
        seq = seq or self.newest_seq
        snapobj_dir = os.path.join(run_dir(self.root, seq), "catalog", "snapobj")
        if not os.path.isdir(snapobj_dir):
            return []
        return sorted(os.listdir(snapobj_dir))

    def read_object(self, content_id_hex: str, under: str = "objects", report: Report = None):
        path = object_path(self.root, content_id_hex, self.disc["fanout_levels"], under)
        return read_object_file(path, report)

    def read_snapshot(self, name: str, report: Report = None):
        """name is a ref name, or a snapshot content id as a 68-character
        multihash text form or a 64-character raw digest."""
        if len(name) in (64, 68) and all(c in "0123456789abcdef" for c in name):
            obj = self.read_object(name, under="snapshots", report=report)
        else:
            refs = self.refs()
            ref = latest_ref(refs["records"], name)
            if ref is None:
                raise FormatError(f"no ref named {name!r} in REFS")
            obj = self.read_object(ref["snapshot_id"], under="snapshots", report=report)
        if obj.payload is None:
            raise FormatError(f"snapshot {name!r} could not be decompressed")
        snap = parse_snapshot(obj.payload, f"snapshot {name!r}")
        snap["content_id"] = obj.content_id_hex
        return snap


# ---------------------------------------------------------------------------
# Walking a snapshot tree, the fill order inside a run's walk order:
# pre-order, tree-entry order, descending into a directory before the
# next sibling entry.


def unescape_root_name(name: bytes) -> str:
    """Reverses the four-byte escape the root tree defines: %2F, %5C, %00,
    %25."""
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


def walk_tree(repo: Repo, tree_id_hex: str, path_prefix: str, report: Report, visit):
    """Depth-first, pre-order walk of one tree, in tree-entry order. visit
    is called with (path, entry) for every entry before its children."""
    try:
        obj = repo.read_object(tree_id_hex, under="objects", report=report)
        if obj.payload is None:
            report.fail(tree_id_hex, "tree could not be decompressed, walk stopped here")
            return
        tree = parse_tree(obj.payload, f"tree {tree_id_hex}")
    except (FormatError, FileNotFoundError) as e:
        report.fail(tree_id_hex, f"tree could not be read, walk stopped here: {e}")
        return
    for entry in tree["entries"]:
        name = entry["name"]
        display_name = (
            unescape_root_name(name) if path_prefix == "" else name.decode(
                "utf-8", errors="surrogateescape"
            )
        )
        child_path = display_name if path_prefix == "" else f"{path_prefix}/{display_name}"
        visit(child_path, entry)
        if entry["entry_type"] == 2:  # directory
            walk_tree(repo, entry["content_id"], child_path, report, visit)


def iter_file_chunks(repo: Repo, blob_id_hex: str, report: Report):
    """Yields (content_id_hex, length, file_offset) leaf chunk entries of a
    file's blob, following level-1 nested blobs. Phase 1 never writes
    level 1, but a reader must still follow it, the Blob rule."""
    obj = repo.read_object(blob_id_hex, under="objects", report=report)
    if obj.payload is None:
        report.fail(blob_id_hex, "blob could not be decompressed")
        return
    blob = parse_blob(obj.payload, f"blob {blob_id_hex}")
    if blob["level"] == 0:
        for e in blob["entries"]:
            yield e["content_id"], e["length"], e["file_offset"]
    else:
        for e in blob["entries"]:
            yield from iter_file_chunks(repo, e["content_id"], report)


# ---------------------------------------------------------------------------
# summary


def cmd_summary(args):
    report = Report()
    repo = Repo(args.disc_root)
    d = repo.disc
    print(f"NOAHSARK root: {repo.root}")
    print(f"disc uuid:     {d['disc_uuid'].hex()}")
    print(f"repo uuid:     {d['repo_uuid'].hex()}")
    print(f"disc seq:      {d['disc_seq']}")
    print(f"label:         {d['label'].decode('utf-8', errors='replace')}")
    print(f"fs profile:    {d['fs_profile']}")
    print(f"fanout levels: {d['fanout_levels']}")
    print(f"sealed:        {bool(d['sealed'])}")
    print(f"runs found:    {repo.run_seqs}")

    run = repo.run_header(repo.newest_seq)
    print(f"\nnewest run seq: {run['run_seq']}")
    print(f"hash_algo:      0x{run['hash_algo']:02x}")
    print(f"chunker_profile:{run['chunker_profile']}")
    print(f"fec_k / fec_m:  {run['fec_k']} / {run['fec_m']}")
    print(f"disc_object_count: {run['disc_object_count']}")

    idx = repo.index(repo.newest_seq)
    print(f"\nINDEX for run {idx['run_seq']}:")
    print(f"  files:   {idx['file_count']}")
    print(f"  objects: {idx['object_count']}")
    print(f"  prereqs: {idx['prereq_count']}")

    refs = repo.refs()
    print(f"\nREFS: {refs['record_count']} records")
    discs = repo.discs()
    print(f"DISCS: {discs['record_count']} rows")
    for row in discs["rows"]:
        status = RUN_STATUS_NAMES.get(row["run_status"], str(row["run_status"]))
        health = HEALTH_NAMES.get(row["health"], str(row["health"]))
        print(f"  run {row['run_seq']}: status={status} health={health}")

    names = repo.snapshot_names()
    print(f"\nsnapshots in this run's catalog: {len(names)}")
    return 0 if report.ok() else 1


# ---------------------------------------------------------------------------
# list


def cmd_list(args):
    report = Report()
    repo = Repo(args.disc_root)

    if args.snapshot is None:
        refs = repo.refs()
        names_by_snapshot = {}
        for r in refs["records"]:
            names_by_snapshot.setdefault(text_form(r["snapshot_id"]), []).append(r["name"])
        for name in repo.snapshot_names():
            snap = repo.read_snapshot(name, report=report)
            refnames = ", ".join(sorted(set(names_by_snapshot.get(name, []))))
            print(
                f"{name}  gen={snap['generation']:<4} "
                f"time={snap['time_sec']}  parent={snap['parent'][:16]}...  "
                f"refs=[{refnames}]"
            )
        return 0 if report.ok() else 1

    snap = repo.read_snapshot(args.snapshot, report=report)
    print(f"# snapshot {snap['content_id']}")
    print(f"# generation {snap['generation']}, time {snap['time_sec']}")
    for m in snap["meta"]:
        tag_name = SNAPSHOT_META_TAG_NAMES.get(m["tag"], str(m["tag"]))
        if m["tag"] in (1, 2, 3):
            print(f"# {tag_name}: {m['value'].decode('utf-8', errors='replace')}")

    def visit(path, entry):
        type_name = ENTRY_TYPE_NAMES.get(entry["entry_type"], str(entry["entry_type"]))
        size = entry["size"]
        mode = oct(entry["mode"])
        print(f"{type_name:9} {size:12} {mode:7} {path}")

    walk_tree(repo, snap["root_tree"], "", report, visit)
    return 0 if report.ok() else 1


# ---------------------------------------------------------------------------
# verify


def cmd_verify(args):
    report = Report()
    repo = Repo(args.disc_root)

    for seq in repo.run_seqs:
        try:
            run = repo.run_header(seq)
        except FormatError as e:
            report.fail(f"run {seq}", str(e))
            continue
        try:
            idx = repo.index(seq)
        except FormatError as e:
            report.fail(f"run {seq}", str(e))
            continue

        index_path = os.path.join(run_dir(repo.root, seq), "INDEX.bin")
        with open(index_path, "rb") as f:
            index_bytes = f.read()
        if sha256(index_bytes).digest() != run["index_hash"]:
            report.fail(f"run {seq}", "index_hash does not match INDEX.bin")

        try:
            repo.refs(seq)
        except FormatError as e:
            report.fail(f"run {seq} REFS", str(e))
        try:
            repo.discs(seq)
        except FormatError as e:
            report.fail(f"run {seq} DISCS", str(e))

        for row in idx["objects"]:
            cid = row["content_id"]
            under = "snapshots" if row["kind"] == 4 else "objects"
            try:
                obj = repo.read_object(cid, under=under, report=report)
                if obj.payload is not None and obj.content_id_hex != text_form(cid):
                    report.fail(f"object {cid}", "content id mismatch against INDEX")
            except FormatError as e:
                report.fail(f"object {cid}", str(e))
            except FileNotFoundError:
                report.fail(f"object {cid}", "object file not found")

    for name in repo.snapshot_names():
        try:
            snap = repo.read_snapshot(name, report=report)
            walk_tree(repo, snap["root_tree"], "", report, lambda p, e: None)
        except FormatError as e:
            report.fail(f"snapshot {name}", str(e))
            continue

    if report.ok():
        print("verify: all checks passed")
    else:
        print(f"verify: {len(report.failures)} check(s) failed", file=sys.stderr)
    return 0 if report.ok() else 1


# ---------------------------------------------------------------------------
# restore


def restore_symlink(dest: str, target: bytes):
    target_str = target.decode("utf-8", errors="surrogateescape")
    if os.path.lexists(dest):
        os.remove(dest)
    os.symlink(target_str, dest)


def restore_file(repo: Repo, dest: str, blob_id_hex: str, report: Report):
    chunks = sorted(
        iter_file_chunks(repo, blob_id_hex, report), key=lambda c: c[2]
    )
    with open(dest, "wb") as out:
        for content_id_hex, length, file_offset in chunks:
            obj = repo.read_object(content_id_hex, under="objects", report=report)
            if obj.payload is None:
                report.fail(dest, f"chunk {content_id_hex} could not be expanded")
                continue
            if len(obj.payload) != length:
                report.fail(dest, f"chunk {content_id_hex} length mismatch")
            out.write(obj.payload)


def apply_metadata(dest: str, entry, report: Report):
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
    repo = Repo(args.disc_root)
    snap = repo.read_snapshot(args.snapshot, report=report)
    os.makedirs(args.out, exist_ok=True)

    def visit(path, entry):
        # A root entry's decoded name is the source's absolute path (the
        # root tree's escape); strip its leading '/' so it joins under
        # --out instead of replacing it.
        dest = os.path.join(args.out, path.lstrip("/"))
        entry_type = entry["entry_type"]
        if entry_type == 2:  # directory
            os.makedirs(dest, exist_ok=True)
            apply_metadata(dest, entry, report)
        elif entry_type == 1:  # regular
            os.makedirs(os.path.dirname(dest), exist_ok=True)
            restore_file(repo, dest, entry["content_id"], report)
            apply_metadata(dest, entry, report)
        elif entry_type == 3:  # symlink
            target = b""
            for t in entry["tlvs"]:
                if t["type"] == TLV_SYMLINK_TARGET:
                    target = t["payload"]
            os.makedirs(os.path.dirname(dest), exist_ok=True)
            restore_symlink(dest, target)
        else:
            report.fail(dest, f"entry type {entry_type} not restored by this decoder")

    walk_tree(repo, snap["root_tree"], "", report, visit)

    if report.ok():
        print(f"restore: wrote snapshot into {args.out}")
    else:
        print(f"restore: {len(report.failures)} problem(s)", file=sys.stderr)
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
    sp.add_argument("disc_root")
    sp.add_argument("--snapshot", help="ref name or snapshot content id")
    sp.set_defaults(func=cmd_list)

    sp = sub.add_parser("verify", help="verify every structure and object this decoder can reach")
    sp.add_argument("disc_root")
    sp.set_defaults(func=cmd_verify)

    sp = sub.add_parser("restore", help="restore one snapshot into an output directory")
    sp.add_argument("disc_root")
    sp.add_argument("--snapshot", required=True, help="ref name or snapshot content id")
    sp.add_argument("--out", required=True, help="output directory")
    sp.set_defaults(func=cmd_restore)

    return p


def main(argv=None):
    args = build_parser().parse_args(argv)
    try:
        return args.func(args)
    except FormatError as e:
        print(f"error: {e}", file=sys.stderr)
        return 1
    except FileNotFoundError as e:
        print(f"error: {e}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
