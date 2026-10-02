package format

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// CommonHeaderLen is the encoded size of CommonHeader.
const CommonHeaderLen = 32

// ObjectHeaderLen is the encoded size of ObjectHeader.
const ObjectHeaderLen = 32

// CommonHeader begins every structure. It carries identity and versioning
// in one place.
type CommonHeader struct {
	MagicProject Magic
	MagicKind    Magic
	VersionMajor uint16
	ReservedU16a uint16
	HeaderLen    uint16
	ReservedU16b uint16
	ReservedU64  uint64
}

// Encode writes h into buf[0:CommonHeaderLen]. buf must be at least
// CommonHeaderLen bytes.
func (h *CommonHeader) Encode(buf []byte) error {
	if len(buf) < CommonHeaderLen {
		return ErrShort
	}
	copy(buf[0:8], h.MagicProject[:])
	copy(buf[8:16], h.MagicKind[:])
	binary.LittleEndian.PutUint16(buf[16:18], h.VersionMajor)
	binary.LittleEndian.PutUint16(buf[18:20], h.ReservedU16a)
	binary.LittleEndian.PutUint16(buf[20:22], h.HeaderLen)
	binary.LittleEndian.PutUint16(buf[22:24], h.ReservedU16b)
	binary.LittleEndian.PutUint64(buf[24:32], h.ReservedU64)
	return nil
}

// Decode reads a CommonHeader from buf. It rejects a short buffer, a
// magic_project mismatch, an unknown version_major, and a nonzero
// reserved field.
func (h *CommonHeader) Decode(buf []byte) error {
	if err := h.decode(buf); err != nil {
		return err
	}
	return h.checkReserved()
}

// decode reads a CommonHeader from buf. It rejects a short buffer, a
// magic_project mismatch, and an unknown version_major. The structure
// that holds the header checks the reserved fields after its CRC.
func (h *CommonHeader) decode(buf []byte) error {
	if len(buf) < CommonHeaderLen {
		return ErrShort
	}
	var magicProject, magicKind Magic
	copy(magicProject[:], buf[0:8])
	copy(magicKind[:], buf[8:16])
	if magicProject != ProjectMagic {
		return ErrBadMagic
	}
	versionMajor := binary.LittleEndian.Uint16(buf[16:18])
	if versionMajor != 1 {
		return fmt.Errorf("%w: %s version_major is %d", ErrVersion, kindName(magicKind), versionMajor)
	}
	h.MagicProject = magicProject
	h.MagicKind = magicKind
	h.VersionMajor = versionMajor
	h.ReservedU16a = binary.LittleEndian.Uint16(buf[18:20])
	h.HeaderLen = binary.LittleEndian.Uint16(buf[20:22])
	h.ReservedU16b = binary.LittleEndian.Uint16(buf[22:24])
	h.ReservedU64 = binary.LittleEndian.Uint64(buf[24:32])
	return nil
}

// checkReserved refuses a nonzero reserved field of h.
func (h *CommonHeader) checkReserved() error {
	name := kindName(h.MagicKind) + " common header"
	return firstError(
		zeroField(name, "reserved_u16a", uint64(h.ReservedU16a)),
		zeroField(name, "reserved_u16b", uint64(h.ReservedU16b)),
		zeroField(name, "reserved_u64", h.ReservedU64),
	)
}

// fixedPartEnd checks that header_len is knownLen, the value this build
// knows for the structure at version_major 1, and returns it: the
// offset of the first byte after the fixed part.
func (h *CommonHeader) fixedPartEnd(knownLen int) (int, error) {
	if int(h.HeaderLen) != knownLen {
		return 0, fmt.Errorf("%w: %s header_len is %d, want %d", ErrHeaderLen, kindName(h.MagicKind), h.HeaderLen, knownLen)
	}
	return knownLen, nil
}

// kindName is the magic_kind of a structure as quoted text, with the
// zero padding removed, for an error message.
func kindName(m Magic) string {
	return fmt.Sprintf("%q", bytes.TrimRight(m[:], "\x00"))
}

// ObjectHeader follows the common header in every object file. An object
// file carries no magic or version fields of its own beyond the common
// header.
type ObjectHeader struct {
	Kind         ObjectKind
	HashAlgo     HashAlgo
	ReservedU8   uint8
	Compression  Compression
	ReservedA    [4]byte
	PayloadLen   uint64
	StoredLen    uint64
	HeaderCRC32C uint32
	ReservedU32  uint32
}

// Encode writes h into buf[0:ObjectHeaderLen]. buf must be at least
// ObjectHeaderLen bytes.
func (h *ObjectHeader) Encode(buf []byte) error {
	if len(buf) < ObjectHeaderLen {
		return ErrShort
	}
	buf[0] = byte(h.Kind)
	buf[1] = byte(h.HashAlgo)
	buf[2] = h.ReservedU8
	buf[3] = byte(h.Compression)
	copy(buf[4:8], h.ReservedA[:])
	binary.LittleEndian.PutUint64(buf[8:16], h.PayloadLen)
	binary.LittleEndian.PutUint64(buf[16:24], h.StoredLen)
	binary.LittleEndian.PutUint32(buf[24:28], h.HeaderCRC32C)
	binary.LittleEndian.PutUint32(buf[28:32], h.ReservedU32)
	return nil
}

// Decode reads an ObjectHeader from buf. It rejects a short buffer and
// a nonzero reserved field. The object header carries no magic or
// version of its own; the common header that precedes it carries those.
func (h *ObjectHeader) Decode(buf []byte) error {
	if err := h.decode(buf); err != nil {
		return err
	}
	return h.checkReserved()
}

// decode reads an ObjectHeader from buf. It rejects a short buffer. The
// object file checks the reserved fields after header_crc32c.
func (h *ObjectHeader) decode(buf []byte) error {
	if len(buf) < ObjectHeaderLen {
		return ErrShort
	}
	h.Kind = ObjectKind(buf[0])
	h.HashAlgo = HashAlgo(buf[1])
	h.ReservedU8 = buf[2]
	h.Compression = Compression(buf[3])
	copy(h.ReservedA[:], buf[4:8])
	h.PayloadLen = binary.LittleEndian.Uint64(buf[8:16])
	h.StoredLen = binary.LittleEndian.Uint64(buf[16:24])
	h.HeaderCRC32C = binary.LittleEndian.Uint32(buf[24:28])
	h.ReservedU32 = binary.LittleEndian.Uint32(buf[28:32])
	return nil
}

// checkReserved refuses a nonzero reserved field of h.
func (h *ObjectHeader) checkReserved() error {
	const name = "object header"
	return firstError(
		zeroField(name, "reserved_u8", uint64(h.ReservedU8)),
		zeroBytes(name, "reserved_a", h.ReservedA[:]),
		zeroField(name, "reserved_u32", uint64(h.ReservedU32)),
	)
}

// objectHeaderLens maps the magic_kind of each object kind to its kind
// value and to the header_len of version_major 1.
var objectHeaderLens = map[Magic]struct {
	kind      ObjectKind
	headerLen int
}{
	MagicChunk:    {ObjectKindChunk, ChunkHeaderLen},
	MagicBlob:     {ObjectKindBlob, BlobHeaderLen},
	MagicTree:     {ObjectKindTree, TreeHeaderLen},
	MagicSnapshot: {ObjectKindSnapshot, SnapshotHeaderLen},
}

// DecodeObjectFileHeader decodes the common header and the object header
// from head, the first CommonHeaderLen+ObjectHeaderLen bytes of an object
// file, and checks them in the order of the reader procedure: the magic,
// version_major, header_len for the kind, header_crc32c over the common
// header and bytes 0 to 23 of the object header, the reserved fields, a
// kind that agrees with magic_kind, hash_algo and compression. A kind's
// own Decode (Chunk, Blob, Tree, Snapshot) runs the same function before
// it decodes its fixed body.
func DecodeObjectFileHeader(head []byte) (CommonHeader, ObjectHeader, error) {
	return decodeObjectHead(head, Magic{})
}

// decodeObjectHead is DecodeObjectFileHeader. A nonzero want is the
// magic_kind that the caller expects.
func decodeObjectHead(head []byte, want Magic) (CommonHeader, ObjectHeader, error) {
	var ch CommonHeader
	var oh ObjectHeader
	if len(head) < CommonHeaderLen+ObjectHeaderLen {
		return ch, oh, ErrShort
	}
	if err := ch.decode(head); err != nil {
		return ch, oh, err
	}
	known, ok := objectHeaderLens[ch.MagicKind]
	if !ok || (want != Magic{} && ch.MagicKind != want) {
		return ch, oh, ErrBadMagic
	}
	if _, err := ch.fixedPartEnd(known.headerLen); err != nil {
		return ch, oh, err
	}
	if err := oh.decode(head[CommonHeaderLen:]); err != nil {
		return ch, oh, err
	}
	if crc32c(head[0:objectHeaderCRCOffset]) != oh.HeaderCRC32C {
		return ch, oh, ErrCRC
	}
	if err := firstError(ch.checkReserved(), oh.checkReserved()); err != nil {
		return ch, oh, err
	}
	if oh.Kind != known.kind {
		return ch, oh, fmt.Errorf("%w: object header kind is %d, magic_kind %s wants %d", ErrBadField, oh.Kind, kindName(ch.MagicKind), known.kind)
	}
	if oh.HashAlgo != HashAlgoSHA256 {
		return ch, oh, fmt.Errorf("%w: object header hash_algo is 0x%02x, want 0x%02x", ErrBadField, uint8(oh.HashAlgo), uint8(HashAlgoSHA256))
	}
	if oh.Compression != CompressionNone && oh.Compression != CompressionZstd {
		return ch, oh, fmt.Errorf("%w: object header compression is %d, want 0 or 1", ErrBadField, oh.Compression)
	}
	return ch, oh, nil
}

// The header_len values this version's writer records for the four
// object kinds: the common header, the object header, and the kind's own
// fixed body.
const (
	ChunkHeaderLen    = chunkFixedLen
	BlobHeaderLen     = blobFixedLen
	TreeHeaderLen     = treeFixedLen
	SnapshotHeaderLen = CommonHeaderLen + ObjectHeaderLen + SnapshotFixedLen
)
