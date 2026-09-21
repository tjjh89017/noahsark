package format

import (
	"bytes"
	"encoding/binary"
)

const (
	// TableFixedBodyLen is the size of the REFS and DISCS fixed body,
	// after the common header and before the records.
	TableFixedBodyLen = 24
	// RefsHeaderLen is the common header plus the fixed body.
	RefsHeaderLen = CommonHeaderLen + TableFixedBodyLen
	// RefRecordLen is the size of one REFS record.
	RefRecordLen = 88
	// RefNameLen is the size of a ref record's name field.
	RefNameLen = 40
)

// RefRecord is a named pointer to a snapshot, one row of REFS, 88 bytes.
// A record stores no run number.
type RefRecord struct {
	SnapshotID  [32]byte
	TimeSec     int64
	TimeNsec    uint32
	NameLen     uint16
	ReservedU16 uint16
	Name        [RefNameLen]byte
}

func (r *RefRecord) encode(buf []byte) {
	copy(buf[0:32], r.SnapshotID[:])
	binary.LittleEndian.PutUint64(buf[32:40], uint64(r.TimeSec))
	binary.LittleEndian.PutUint32(buf[40:44], r.TimeNsec)
	binary.LittleEndian.PutUint16(buf[44:46], r.NameLen)
	binary.LittleEndian.PutUint16(buf[46:48], r.ReservedU16)
	copy(buf[48:88], r.Name[:])
}

func (r *RefRecord) decode(buf []byte) {
	copy(r.SnapshotID[:], buf[0:32])
	r.TimeSec = int64(binary.LittleEndian.Uint64(buf[32:40]))
	r.TimeNsec = binary.LittleEndian.Uint32(buf[40:44])
	r.NameLen = binary.LittleEndian.Uint16(buf[44:46])
	r.ReservedU16 = binary.LittleEndian.Uint16(buf[46:48])
	copy(r.Name[:], buf[48:88])
}

// RefsTable is REFS, the repository-wide table of named pointers to
// snapshots. It is carried in full on every disc. It holds no CRC; the
// file_hash of its Files row covers every byte.
type RefsTable struct {
	Header      CommonHeader
	RepoUUID    [16]byte
	RecordCount uint64
	Records     []RefRecord
}

// EncodedLen returns the total encoded size of t: the header plus every
// record.
func (t *RefsTable) EncodedLen() int {
	return RefsHeaderLen + len(t.Records)*RefRecordLen
}

// Encode writes t into buf and returns the number of bytes written,
// EncodedLen().
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

	off := RefsHeaderLen
	for i := range t.Records {
		t.Records[i].encode(buf[off : off+RefRecordLen])
		off += RefRecordLen
	}
	return total, nil
}

// Decode reads a RefsTable from buf and returns the number of bytes
// read. It rejects a short buffer, a magic_kind mismatch, a header_len
// below the fixed part this build knows, and a file length that does
// not agree with record_count. It does not interpret a reserved field.
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
	recordsOff, err := h.fixedPartEnd(RefsHeaderLen)
	if err != nil {
		return 0, err
	}

	var repoUUID [16]byte
	copy(repoUUID[:], buf[32:48])
	recordCount := binary.LittleEndian.Uint64(buf[48:56])

	total := recordsOff + int(recordCount)*RefRecordLen
	if len(buf) < total {
		return 0, ErrShort
	}
	if len(buf) != total {
		return 0, ErrBadField
	}

	off := recordsOff
	records := make([]RefRecord, recordCount)
	for i := range records {
		records[i].decode(buf[off : off+RefRecordLen])
		off += RefRecordLen
	}

	t.Header = h
	t.RepoUUID = repoUUID
	t.RecordCount = recordCount
	t.Records = records
	return total, nil
}

// NewerRef reports whether a is newer than b for one ref name: the
// highest time_sec, then the highest time_nsec, then the highest
// snapshot_id bytes. A reader takes the newest record as the value of
// the name.
func NewerRef(a, b RefRecord) bool {
	if a.TimeSec != b.TimeSec {
		return a.TimeSec > b.TimeSec
	}
	if a.TimeNsec != b.TimeNsec {
		return a.TimeNsec > b.TimeNsec
	}
	return bytes.Compare(a.SnapshotID[:], b.SnapshotID[:]) > 0
}
