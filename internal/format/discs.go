package format

import (
	"encoding/binary"
	"fmt"
)

const (
	// DiscsHeaderLen is the common header plus the fixed body.
	DiscsHeaderLen = CommonHeaderLen + TableFixedBodyLen
	// DiscsRowLen is the size of one DISCS row.
	DiscsRowLen = 176
	// DiscsLabelLen is the size of a DISCS row's label field.
	DiscsLabelLen = 64
)

// DiscsRow is one row of DISCS, one per disc the repository knew at pack
// time, 176 bytes. The row records no health, no verification result and
// no state: those are host state, never disc state.
type DiscsRow struct {
	RunSeq          uint64
	DiscSeq         uint64
	DiscUUID        [16]byte
	RunHash         [32]byte
	CreatedSec      int64
	ReservedU64a    uint64
	CapacitySectors uint64
	ReservedU64b    uint64
	ReservedU32     uint32
	LabelLen        uint16
	Label           [DiscsLabelLen]byte
	Reserved        [10]byte
}

func (r *DiscsRow) encode(buf []byte) {
	binary.LittleEndian.PutUint64(buf[0:8], r.RunSeq)
	binary.LittleEndian.PutUint64(buf[8:16], r.DiscSeq)
	copy(buf[16:32], r.DiscUUID[:])
	copy(buf[32:64], r.RunHash[:])
	binary.LittleEndian.PutUint64(buf[64:72], uint64(r.CreatedSec))
	binary.LittleEndian.PutUint64(buf[72:80], r.ReservedU64a)
	binary.LittleEndian.PutUint64(buf[80:88], r.CapacitySectors)
	binary.LittleEndian.PutUint64(buf[88:96], r.ReservedU64b)
	binary.LittleEndian.PutUint32(buf[96:100], r.ReservedU32)
	binary.LittleEndian.PutUint16(buf[100:102], r.LabelLen)
	copy(buf[102:166], r.Label[:])
	copy(buf[166:176], r.Reserved[:])
}

func (r *DiscsRow) decode(buf []byte) error {
	r.RunSeq = binary.LittleEndian.Uint64(buf[0:8])
	r.DiscSeq = binary.LittleEndian.Uint64(buf[8:16])
	copy(r.DiscUUID[:], buf[16:32])
	copy(r.RunHash[:], buf[32:64])
	r.CreatedSec = int64(binary.LittleEndian.Uint64(buf[64:72]))
	r.ReservedU64a = binary.LittleEndian.Uint64(buf[72:80])
	r.CapacitySectors = binary.LittleEndian.Uint64(buf[80:88])
	r.ReservedU64b = binary.LittleEndian.Uint64(buf[88:96])
	r.ReservedU32 = binary.LittleEndian.Uint32(buf[96:100])
	r.LabelLen = binary.LittleEndian.Uint16(buf[100:102])
	copy(r.Label[:], buf[102:166])
	copy(r.Reserved[:], buf[166:176])
	if int(r.LabelLen) > DiscsLabelLen {
		return fmt.Errorf("%w: DISCS row label_len is %d, want at most %d", ErrBadField, r.LabelLen, DiscsLabelLen)
	}
	const name = "DISCS row"
	return firstError(
		zeroField(name, "reserved_u64a", r.ReservedU64a),
		zeroField(name, "reserved_u64b", r.ReservedU64b),
		zeroField(name, "reserved_u32", uint64(r.ReservedU32)),
		zeroBytes(name, "label padding", r.Label[r.LabelLen:]),
		zeroBytes(name, "reserved", r.Reserved[:]),
	)
}

// DiscsTable is DISCS, one row per disc the repository knew at pack time.
// It holds no CRC; the file_hash of its Files row covers every byte.
type DiscsTable struct {
	Header      CommonHeader
	RepoUUID    [16]byte
	RecordCount uint64
	Rows        []DiscsRow
}

// EncodedLen returns the total encoded size of t: the header plus every
// row.
func (t *DiscsTable) EncodedLen() int {
	return DiscsHeaderLen + len(t.Rows)*DiscsRowLen
}

// Encode writes t into buf and returns the number of bytes written,
// EncodedLen().
func (t *DiscsTable) Encode(buf []byte) (int, error) {
	total := t.EncodedLen()
	if len(buf) < total {
		return 0, ErrShort
	}
	if err := t.Header.Encode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	copy(buf[32:48], t.RepoUUID[:])
	binary.LittleEndian.PutUint64(buf[48:56], t.RecordCount)

	off := DiscsHeaderLen
	for i := range t.Rows {
		t.Rows[i].encode(buf[off : off+DiscsRowLen])
		off += DiscsRowLen
	}
	return total, nil
}

// Decode reads a DiscsTable from buf and returns the number of bytes
// read. It rejects a short buffer, a magic_kind mismatch, a header_len
// other than DiscsHeaderLen, a nonzero reserved field or padding byte,
// and a file length that does not agree with record_count.
func (t *DiscsTable) Decode(buf []byte) (int, error) {
	if len(buf) < DiscsHeaderLen {
		return 0, ErrShort
	}
	var h CommonHeader
	if err := h.decode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	if h.MagicKind != MagicDiscs {
		return 0, ErrBadMagic
	}
	rowsOff, err := h.fixedPartEnd(DiscsHeaderLen)
	if err != nil {
		return 0, err
	}
	if err := h.checkReserved(); err != nil {
		return 0, err
	}

	var repoUUID [16]byte
	copy(repoUUID[:], buf[32:48])
	recordCount := binary.LittleEndian.Uint64(buf[48:56])

	total := rowsOff + int(recordCount)*DiscsRowLen
	if len(buf) < total {
		return 0, ErrShort
	}
	if len(buf) != total {
		return 0, ErrBadField
	}

	off := rowsOff
	rows := make([]DiscsRow, recordCount)
	for i := range rows {
		if err := rows[i].decode(buf[off : off+DiscsRowLen]); err != nil {
			return 0, fmt.Errorf("DISCS row %d: %w", i, err)
		}
		off += DiscsRowLen
	}

	t.Header = h
	t.RepoUUID = repoUUID
	t.RecordCount = recordCount
	t.Rows = rows
	return total, nil
}
