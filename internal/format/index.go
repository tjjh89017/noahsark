package format

import "encoding/binary"

const (
	// IndexFixedBodyLen is the size of INDEX's fixed body, after the
	// common header and before the Files table.
	IndexFixedBodyLen = 24
	// IndexHeaderLen is the common header plus the fixed body.
	IndexHeaderLen = CommonHeaderLen + IndexFixedBodyLen
	// IndexFileRecordLen is the size of one Files table row.
	IndexFileRecordLen = 48
	// IndexObjectRecordLen is the size of one Objects table row.
	IndexObjectRecordLen = 40
	// IndexPrereqRecordLen is the size of one Prereqs table row.
	IndexPrereqRecordLen = 48
)

// File role registry. A role names what a Files row describes.
const (
	FileRoleIndex     = 1
	FileRoleRun       = 2
	FileRoleDisc      = 3
	FileRoleReadme    = 4
	FileRoleFormat    = 5
	FileRoleRefs      = 7
	FileRoleDiscs     = 8
	FileRoleChecksum  = 10
	FileRoleParity    = 11
	FileRoleRun2      = 12
	FileRoleObject    = 13
	FileRoleReference = 14
)

// IndexFileRecord is one row of the Files table, 48 bytes. FileHash is
// set for the fixed-name files only; every other role carries zero.
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

func (r *IndexFileRecord) decode(buf []byte) {
	copy(r.FileHash[:], buf[0:32])
	r.ByteLen = binary.LittleEndian.Uint64(buf[32:40])
	r.Role = buf[40]
	copy(r.Reserved[:], buf[41:48])
}

// IndexObjectRecord is one row of the Objects table, 40 bytes. The id
// gives the file name and Kind gives the directory. The j-th role 13
// Files row describes the file of Objects row j.
type IndexObjectRecord struct {
	ContentID [32]byte
	Kind      ObjectKind
	Reserved  [7]byte
}

func (r *IndexObjectRecord) encode(buf []byte) {
	copy(buf[0:32], r.ContentID[:])
	buf[32] = byte(r.Kind)
	copy(buf[33:40], r.Reserved[:])
}

func (r *IndexObjectRecord) decode(buf []byte) error {
	copy(r.ContentID[:], buf[0:32])
	r.Kind = ObjectKind(buf[32])
	copy(r.Reserved[:], buf[33:40])
	if r.Kind < ObjectKindChunk || r.Kind > ObjectKindSnapshot {
		return ErrObjectKind
	}
	return nil
}

// IndexPrereqRecord is one row of the Prereqs table, 48 bytes. It names
// the disc by uuid, because a run_seq can repeat after a repository is
// rebuilt.
type IndexPrereqRecord struct {
	ContentID [32]byte
	DiscUUID  [16]byte
}

func (r *IndexPrereqRecord) encode(buf []byte) {
	copy(buf[0:32], r.ContentID[:])
	copy(buf[32:48], r.DiscUUID[:])
}

func (r *IndexPrereqRecord) decode(buf []byte) {
	copy(r.ContentID[:], buf[0:32])
	copy(r.DiscUUID[:], buf[32:48])
}

// Index is INDEX, the structure a reader opens first inside a run. It
// lists every file the run wrote, names every object the run stores, and
// names every object the run references but does not store. It holds no
// CRC; index_hash of the run header covers every byte.
type Index struct {
	Header      CommonHeader
	RunSeq      uint64
	FileCount   uint32
	ObjectCount uint32
	PrereqCount uint32
	ReservedU32 uint32
	Files       []IndexFileRecord
	Objects     []IndexObjectRecord
	Prereqs     []IndexPrereqRecord
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
// EncodedLen().
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
	binary.LittleEndian.PutUint32(buf[52:56], idx.ReservedU32)

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
	return total, nil
}

// Decode reads an Index from buf and returns the number of bytes read.
// It rejects a short buffer, a magic_kind mismatch, a header_len below
// the fixed part this build knows, a file length that does not agree
// with the row counts, and an Objects row whose kind is outside 1 to 4.
// It does not interpret a reserved field.
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
	tablesOff, err := h.fixedPartEnd(IndexHeaderLen)
	if err != nil {
		return 0, err
	}

	runSeq := binary.LittleEndian.Uint64(buf[32:40])
	fileCount := binary.LittleEndian.Uint32(buf[40:44])
	objectCount := binary.LittleEndian.Uint32(buf[44:48])
	prereqCount := binary.LittleEndian.Uint32(buf[48:52])
	reservedU32 := binary.LittleEndian.Uint32(buf[52:56])

	total := tablesOff +
		int(fileCount)*IndexFileRecordLen +
		int(objectCount)*IndexObjectRecordLen +
		int(prereqCount)*IndexPrereqRecordLen
	if len(buf) < total {
		return 0, ErrShort
	}
	if len(buf) != total {
		return 0, ErrBadField
	}

	off := tablesOff
	files := make([]IndexFileRecord, fileCount)
	for i := range files {
		files[i].decode(buf[off : off+IndexFileRecordLen])
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
	idx.ReservedU32 = reservedU32
	idx.Files = files
	idx.Objects = objects
	idx.Prereqs = prereqs
	return total, nil
}
