package format

import "encoding/binary"

const (
	// RefsFixedBodyLen is the size of REFS's fixed body, after the common
	// header and before the records.
	RefsFixedBodyLen = 40
	// RefsHeaderLen is the common header plus the fixed body.
	RefsHeaderLen = CommonHeaderLen + RefsFixedBodyLen
	// RefRecordLen is the size of one REFS record.
	RefRecordLen = 96
	// RefNameLen is the size of a ref record's name field.
	RefNameLen = 40
)

// RefRecord is a named pointer to a snapshot, one row of REFS, 96 bytes.
type RefRecord struct {
	SnapshotID [32]byte
	TimeSec    int64
	TimeNsec   uint32
	NameLen    uint16
	HashAlgo   HashAlgo
	ReservedU8 uint8
	Name       [RefNameLen]byte
	RunSeq     uint64
}

func (r *RefRecord) encode(buf []byte) {
	copy(buf[0:32], r.SnapshotID[:])
	binary.LittleEndian.PutUint64(buf[32:40], uint64(r.TimeSec))
	binary.LittleEndian.PutUint32(buf[40:44], r.TimeNsec)
	binary.LittleEndian.PutUint16(buf[44:46], r.NameLen)
	buf[46] = byte(r.HashAlgo)
	buf[47] = r.ReservedU8
	copy(buf[48:88], r.Name[:])
	binary.LittleEndian.PutUint64(buf[88:96], r.RunSeq)
}

func (r *RefRecord) decode(buf []byte) error {
	copy(r.SnapshotID[:], buf[0:32])
	r.TimeSec = int64(binary.LittleEndian.Uint64(buf[32:40]))
	r.TimeNsec = binary.LittleEndian.Uint32(buf[40:44])
	r.NameLen = binary.LittleEndian.Uint16(buf[44:46])
	r.HashAlgo = HashAlgo(buf[46])
	r.ReservedU8 = buf[47]
	copy(r.Name[:], buf[48:88])
	r.RunSeq = binary.LittleEndian.Uint64(buf[88:96])
	if r.ReservedU8 != 0 {
		return ErrReserved
	}
	return nil
}

// RefsTable is REFS, the repository-wide table of named pointers to
// snapshots. It is replicated in full on every run.
type RefsTable struct {
	Header       CommonHeader
	RepoUUID     [16]byte
	RecordCount  uint64
	RecordSize   uint16
	HashAlgo     HashAlgo
	DigestLen    uint8
	Reserved     [4]byte
	BodyCRC32C   uint32
	HeaderCRC32C uint32
	Records      []RefRecord
}

// EncodedLen returns the total encoded size of t: the header plus every
// record.
func (t *RefsTable) EncodedLen() int {
	return RefsHeaderLen + len(t.Records)*RefRecordLen
}

// Encode writes t into buf and returns the number of bytes written,
// EncodedLen(). It computes body_crc32c over the records and
// header_crc32c over bytes 0 to 67, and overwrites t.BodyCRC32C and
// t.HeaderCRC32C with the computed values.
func (t *RefsTable) Encode(buf []byte) (int, error) {
	total := t.EncodedLen()
	if len(buf) < total {
		return 0, ErrShort
	}
	if err := t.Header.Encode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	copy(buf[32:48], t.RepoUUID[:])
	binary.LittleEndian.PutUint64(buf[48:56], t.RecordCount)
	binary.LittleEndian.PutUint16(buf[56:58], t.RecordSize)
	buf[58] = byte(t.HashAlgo)
	buf[59] = t.DigestLen
	copy(buf[60:64], t.Reserved[:])

	off := RefsHeaderLen
	for i := range t.Records {
		t.Records[i].encode(buf[off : off+RefRecordLen])
		off += RefRecordLen
	}

	bodyCRC := crc32c(buf[RefsHeaderLen:total])
	t.BodyCRC32C = bodyCRC
	binary.LittleEndian.PutUint32(buf[64:68], bodyCRC)

	headerCRC := crc32c(buf[0:68])
	t.HeaderCRC32C = headerCRC
	binary.LittleEndian.PutUint32(buf[68:72], headerCRC)
	return total, nil
}

// Decode reads a RefsTable from buf and returns the number of bytes
// read. It rejects a short buffer, a magic_kind mismatch, a nonzero
// reserved field, and a CRC mismatch.
func (t *RefsTable) Decode(buf []byte) (int, error) {
	if len(buf) < RefsHeaderLen {
		return 0, ErrShort
	}
	var h CommonHeader
	if err := h.Decode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	if h.MagicKind != MagicRefs {
		return 0, ErrBadMagic
	}

	var repoUUID [16]byte
	copy(repoUUID[:], buf[32:48])
	recordCount := binary.LittleEndian.Uint64(buf[48:56])
	recordSize := binary.LittleEndian.Uint16(buf[56:58])
	hashAlgo := HashAlgo(buf[58])
	digestLen := buf[59]
	var reserved [4]byte
	copy(reserved[:], buf[60:64])
	if reserved != ([4]byte{}) {
		return 0, ErrReserved
	}
	bodyCRC := binary.LittleEndian.Uint32(buf[64:68])
	headerCRC := binary.LittleEndian.Uint32(buf[68:72])

	if headerCRC != crc32c(buf[0:68]) {
		return 0, ErrCRC
	}

	total := RefsHeaderLen + int(recordCount)*RefRecordLen
	if len(buf) < total {
		return 0, ErrShort
	}
	if bodyCRC != crc32c(buf[RefsHeaderLen:total]) {
		return 0, ErrCRC
	}

	off := RefsHeaderLen
	records := make([]RefRecord, recordCount)
	for i := range records {
		if err := records[i].decode(buf[off : off+RefRecordLen]); err != nil {
			return 0, err
		}
		off += RefRecordLen
	}

	t.Header = h
	t.RepoUUID = repoUUID
	t.RecordCount = recordCount
	t.RecordSize = recordSize
	t.HashAlgo = hashAlgo
	t.DigestLen = digestLen
	t.Reserved = reserved
	t.BodyCRC32C = bodyCRC
	t.HeaderCRC32C = headerCRC
	t.Records = records
	return total, nil
}
