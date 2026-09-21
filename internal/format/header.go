package format

import "encoding/binary"

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
// magic_project mismatch, and an unknown version_major.
func (h *CommonHeader) Decode(buf []byte) error {
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
		return ErrVersion
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

// fixedPartEnd returns the offset of the first byte after the fixed part.
// A header_len below the length this build knows is refused; a larger one
// is obeyed, so the variable part starts where the writer put it.
func (h *CommonHeader) fixedPartEnd(knownLen int) (int, error) {
	if int(h.HeaderLen) < knownLen {
		return 0, ErrHeaderLen
	}
	return int(h.HeaderLen), nil
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

// Decode reads an ObjectHeader from buf. It rejects a short buffer. The
// object header carries no magic or version of its own; the common header
// that precedes it carries those.
func (h *ObjectHeader) Decode(buf []byte) error {
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

// DecodeObjectFileHeader decodes the common header and the object header
// from head, the first CommonHeaderLen+ObjectHeaderLen bytes of an object
// file, and checks header_crc32c over the common header and bytes 0 to 23
// of the object header. A kind's own Decode (Chunk, Blob, Tree, Snapshot)
// redoes this same check as part of decoding its own fixed body; a caller
// that only wants the header, before it commits to a kind's fixed body,
// calls this function instead so the check cannot drift between callers.
func DecodeObjectFileHeader(head []byte) (CommonHeader, ObjectHeader, error) {
	var ch CommonHeader
	if err := ch.Decode(head); err != nil {
		return ch, ObjectHeader{}, err
	}
	var oh ObjectHeader
	if err := oh.Decode(head[CommonHeaderLen:]); err != nil {
		return ch, oh, err
	}
	if crc32c(head[0:objectHeaderCRCOffset]) != oh.HeaderCRC32C {
		return ch, oh, ErrCRC
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
