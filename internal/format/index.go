package format

import "encoding/binary"

const (
	// IndexFixedBodyLen is the size of INDEX's fixed body, after the
	// common header and before the Files table.
	IndexFixedBodyLen = 48
	// IndexHeaderLen is the common header plus the fixed body.
	IndexHeaderLen = CommonHeaderLen + IndexFixedBodyLen
	// IndexFileRecordLen is the size of one Files table row.
	IndexFileRecordLen = 48
	// IndexObjectRecordLen is the size of one Objects table row.
	IndexObjectRecordLen = 72
	// IndexPrereqRecordLen is the size of one Prereqs table row.
	IndexPrereqRecordLen = 40
)

// File role registry. A role names what a Files row describes.
const (
	FileRoleReserved  = 0
	FileRoleIndex     = 1
	FileRoleRun       = 2
	FileRoleDisc      = 3
	FileRoleReadme    = 4
	FileRoleFormat    = 5
	FileRoleUnused6   = 6
	FileRoleRefs      = 7
	FileRoleDiscs     = 8
	FileRoleSnapobj   = 9
	FileRoleChecksum  = 10
	FileRoleParity    = 11
	FileRoleRun2      = 12
	FileRoleObject    = 13
	FileRoleReference = 14
)

// IndexFileRecord is one row of the Files table, 48 bytes.
type IndexFileRecord struct {
	FileHash [32]byte
	ByteLen  uint64
	Role     uint8
	Reserved [7]byte
}

func (r *IndexFileRecord) encode(buf []byte) {
	copy(buf[0:32], r.FileHash[:])
	binary.LittleEndian.PutUint64(buf[32:40], r.ByteLen)
	buf[40] = r.Role
	copy(buf[41:48], r.Reserved[:])
}

func (r *IndexFileRecord) decode(buf []byte) error {
	copy(r.FileHash[:], buf[0:32])
	r.ByteLen = binary.LittleEndian.Uint64(buf[32:40])
	r.Role = buf[40]
	copy(r.Reserved[:], buf[41:48])
	for _, b := range r.Reserved {
		if b != 0 {
			return ErrReserved
		}
	}
	return nil
}

// IndexObjectRecord is one row of the Objects table, 72 bytes.
type IndexObjectRecord struct {
	ContentID   [32]byte
	FileIndex   uint32
	Reserved1   uint32
	Offset      uint64
	StoredLen   uint64
	PayloadLen  uint64
	Kind        ObjectKind
	Compression Compression
	Flags       uint16
	Reserved2   uint32
}

func (r *IndexObjectRecord) encode(buf []byte) {
	copy(buf[0:32], r.ContentID[:])
	binary.LittleEndian.PutUint32(buf[32:36], r.FileIndex)
	binary.LittleEndian.PutUint32(buf[36:40], r.Reserved1)
	binary.LittleEndian.PutUint64(buf[40:48], r.Offset)
	binary.LittleEndian.PutUint64(buf[48:56], r.StoredLen)
	binary.LittleEndian.PutUint64(buf[56:64], r.PayloadLen)
	buf[64] = byte(r.Kind)
	buf[65] = byte(r.Compression)
	binary.LittleEndian.PutUint16(buf[66:68], r.Flags)
	binary.LittleEndian.PutUint32(buf[68:72], r.Reserved2)
}

func (r *IndexObjectRecord) decode(buf []byte) error {
	copy(r.ContentID[:], buf[0:32])
	r.FileIndex = binary.LittleEndian.Uint32(buf[32:36])
	r.Reserved1 = binary.LittleEndian.Uint32(buf[36:40])
	r.Offset = binary.LittleEndian.Uint64(buf[40:48])
	r.StoredLen = binary.LittleEndian.Uint64(buf[48:56])
	r.PayloadLen = binary.LittleEndian.Uint64(buf[56:64])
	r.Kind = ObjectKind(buf[64])
	r.Compression = Compression(buf[65])
	r.Flags = binary.LittleEndian.Uint16(buf[66:68])
	r.Reserved2 = binary.LittleEndian.Uint32(buf[68:72])
	if r.Kind < ObjectKindChunk || r.Kind > ObjectKindSnapshot {
		return ErrObjectKind
	}
	if r.Reserved1 != 0 || r.Reserved2 != 0 {
		return ErrReserved
	}
	return nil
}

// IndexPrereqRecord is one row of the Prereqs table, 40 bytes.
type IndexPrereqRecord struct {
	ContentID [32]byte
	RunSeq    uint64
}

func (r *IndexPrereqRecord) encode(buf []byte) {
	copy(buf[0:32], r.ContentID[:])
	binary.LittleEndian.PutUint64(buf[32:40], r.RunSeq)
}

func (r *IndexPrereqRecord) decode(buf []byte) {
	copy(r.ContentID[:], buf[0:32])
	r.RunSeq = binary.LittleEndian.Uint64(buf[32:40])
}

// Index is INDEX, the structure a reader opens first inside a run. It
// lists every file the run wrote, locates and verifies every object the
// run stores, and names every object the run references but does not
// store.
type Index struct {
	Header           CommonHeader
	RunSeq           uint64
	FileCount        uint32
	ObjectCount      uint32
	PrereqCount      uint32
	FileRecordSize   uint16
	ObjectRecordSize uint16
	PrereqRecordSize uint16
	HashAlgo         HashAlgo
	DigestLen        uint8
	Reserved         [4]byte
	ContainerLen     uint64
	BodyCRC32C       uint32
	HeaderCRC32C     uint32
	Files            []IndexFileRecord
	Objects          []IndexObjectRecord
	Prereqs          []IndexPrereqRecord
}

// EncodedLen returns the total encoded size of idx: the header plus every
// table row.
func (idx *Index) EncodedLen() int {
	return IndexHeaderLen +
		len(idx.Files)*IndexFileRecordLen +
		len(idx.Objects)*IndexObjectRecordLen +
		len(idx.Prereqs)*IndexPrereqRecordLen
}

// Encode writes idx into buf and returns the number of bytes written,
// EncodedLen(). It computes container_len, body_crc32c over the three
// tables, and header_crc32c over bytes 0 to 75, and overwrites
// idx.ContainerLen, idx.BodyCRC32C and idx.HeaderCRC32C with the computed
// values.
func (idx *Index) Encode(buf []byte) (int, error) {
	total := idx.EncodedLen()
	if len(buf) < total {
		return 0, ErrShort
	}
	if err := idx.Header.Encode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	binary.LittleEndian.PutUint64(buf[32:40], idx.RunSeq)
	binary.LittleEndian.PutUint32(buf[40:44], idx.FileCount)
	binary.LittleEndian.PutUint32(buf[44:48], idx.ObjectCount)
	binary.LittleEndian.PutUint32(buf[48:52], idx.PrereqCount)
	binary.LittleEndian.PutUint16(buf[52:54], idx.FileRecordSize)
	binary.LittleEndian.PutUint16(buf[54:56], idx.ObjectRecordSize)
	binary.LittleEndian.PutUint16(buf[56:58], idx.PrereqRecordSize)
	buf[58] = byte(idx.HashAlgo)
	buf[59] = idx.DigestLen
	copy(buf[60:64], idx.Reserved[:])
	idx.ContainerLen = uint64(total)
	binary.LittleEndian.PutUint64(buf[64:72], idx.ContainerLen)

	off := IndexHeaderLen
	for i := range idx.Files {
		idx.Files[i].encode(buf[off : off+IndexFileRecordLen])
		off += IndexFileRecordLen
	}
	for i := range idx.Objects {
		idx.Objects[i].encode(buf[off : off+IndexObjectRecordLen])
		off += IndexObjectRecordLen
	}
	for i := range idx.Prereqs {
		idx.Prereqs[i].encode(buf[off : off+IndexPrereqRecordLen])
		off += IndexPrereqRecordLen
	}

	bodyCRC := crc32c(buf[IndexHeaderLen:total])
	idx.BodyCRC32C = bodyCRC
	binary.LittleEndian.PutUint32(buf[72:76], bodyCRC)

	headerCRC := crc32c(buf[0:76])
	idx.HeaderCRC32C = headerCRC
	binary.LittleEndian.PutUint32(buf[76:80], headerCRC)
	return total, nil
}

// Decode reads an Index from buf and returns the number of bytes read.
// It rejects a short buffer, a magic_kind mismatch, a nonzero reserved
// field, a CRC mismatch, and an Objects row whose kind is outside 1 to 4.
func (idx *Index) Decode(buf []byte) (int, error) {
	if len(buf) < IndexHeaderLen {
		return 0, ErrShort
	}
	var h CommonHeader
	if err := h.Decode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	if h.MagicKind != MagicIndex {
		return 0, ErrBadMagic
	}

	runSeq := binary.LittleEndian.Uint64(buf[32:40])
	fileCount := binary.LittleEndian.Uint32(buf[40:44])
	objectCount := binary.LittleEndian.Uint32(buf[44:48])
	prereqCount := binary.LittleEndian.Uint32(buf[48:52])
	fileRecordSize := binary.LittleEndian.Uint16(buf[52:54])
	objectRecordSize := binary.LittleEndian.Uint16(buf[54:56])
	prereqRecordSize := binary.LittleEndian.Uint16(buf[56:58])
	hashAlgo := HashAlgo(buf[58])
	digestLen := buf[59]
	var reserved [4]byte
	copy(reserved[:], buf[60:64])
	if reserved != ([4]byte{}) {
		return 0, ErrReserved
	}
	containerLen := binary.LittleEndian.Uint64(buf[64:72])
	bodyCRC := binary.LittleEndian.Uint32(buf[72:76])
	headerCRC := binary.LittleEndian.Uint32(buf[76:80])

	if headerCRC != crc32c(buf[0:76]) {
		return 0, ErrCRC
	}

	total := IndexHeaderLen +
		int(fileCount)*IndexFileRecordLen +
		int(objectCount)*IndexObjectRecordLen +
		int(prereqCount)*IndexPrereqRecordLen
	if len(buf) < total {
		return 0, ErrShort
	}
	if bodyCRC != crc32c(buf[IndexHeaderLen:total]) {
		return 0, ErrCRC
	}

	off := IndexHeaderLen
	files := make([]IndexFileRecord, fileCount)
	for i := range files {
		if err := files[i].decode(buf[off : off+IndexFileRecordLen]); err != nil {
			return 0, err
		}
		off += IndexFileRecordLen
	}
	objects := make([]IndexObjectRecord, objectCount)
	for i := range objects {
		if err := objects[i].decode(buf[off : off+IndexObjectRecordLen]); err != nil {
			return 0, err
		}
		off += IndexObjectRecordLen
	}
	prereqs := make([]IndexPrereqRecord, prereqCount)
	for i := range prereqs {
		prereqs[i].decode(buf[off : off+IndexPrereqRecordLen])
		off += IndexPrereqRecordLen
	}

	idx.Header = h
	idx.RunSeq = runSeq
	idx.FileCount = fileCount
	idx.ObjectCount = objectCount
	idx.PrereqCount = prereqCount
	idx.FileRecordSize = fileRecordSize
	idx.ObjectRecordSize = objectRecordSize
	idx.PrereqRecordSize = prereqRecordSize
	idx.HashAlgo = hashAlgo
	idx.DigestLen = digestLen
	idx.Reserved = reserved
	idx.ContainerLen = containerLen
	idx.BodyCRC32C = bodyCRC
	idx.HeaderCRC32C = headerCRC
	idx.Files = files
	idx.Objects = objects
	idx.Prereqs = prereqs
	return total, nil
}
