package format

import "encoding/binary"

const (
	// DiscsFixedBodyLen is the size of DISCS's fixed body, after the
	// common header and before the rows.
	DiscsFixedBodyLen = 40
	// DiscsHeaderLen is the common header plus the fixed body.
	DiscsHeaderLen = CommonHeaderLen + DiscsFixedBodyLen
	// DiscsRowLen is the size of one DISCS row.
	DiscsRowLen = 176
	// DiscsLabelLen is the size of a DISCS row's label field.
	DiscsLabelLen = 64
)

// DISCS state_flags bits.
const (
	DiscsStateClosed          = 1 << 0
	DiscsStateAppendRawOnly   = 1 << 1
	DiscsStateSpareBelowLimit = 1 << 2
	DiscsStateCapacityForced  = 1 << 3
)

// DiscsRow is one row of DISCS, one per run that was burned, 176 bytes.
type DiscsRow struct {
	RunSeq                uint64
	DiscSeq               uint64
	DiscUUID              [16]byte
	RunHash               [32]byte
	CreatedSec            int64
	LastVerifySec         int64
	CapacitySectors       uint64
	UsedSectors           uint64
	RunStatus             uint8
	Health                uint8
	RsMarginPercent       uint16
	LabelLen              uint16
	Label                 [DiscsLabelLen]byte
	StateFlags            uint8
	Reserved              uint8
	CapacityForcedSectors uint64
}

func (r *DiscsRow) encode(buf []byte) {
	binary.LittleEndian.PutUint64(buf[0:8], r.RunSeq)
	binary.LittleEndian.PutUint64(buf[8:16], r.DiscSeq)
	copy(buf[16:32], r.DiscUUID[:])
	copy(buf[32:64], r.RunHash[:])
	binary.LittleEndian.PutUint64(buf[64:72], uint64(r.CreatedSec))
	binary.LittleEndian.PutUint64(buf[72:80], uint64(r.LastVerifySec))
	binary.LittleEndian.PutUint64(buf[80:88], r.CapacitySectors)
	binary.LittleEndian.PutUint64(buf[88:96], r.UsedSectors)
	buf[96] = r.RunStatus
	buf[97] = r.Health
	binary.LittleEndian.PutUint16(buf[98:100], r.RsMarginPercent)
	binary.LittleEndian.PutUint16(buf[100:102], r.LabelLen)
	copy(buf[102:166], r.Label[:])
	buf[166] = r.StateFlags
	buf[167] = r.Reserved
	binary.LittleEndian.PutUint64(buf[168:176], r.CapacityForcedSectors)
}

func (r *DiscsRow) decode(buf []byte) error {
	r.RunSeq = binary.LittleEndian.Uint64(buf[0:8])
	r.DiscSeq = binary.LittleEndian.Uint64(buf[8:16])
	copy(r.DiscUUID[:], buf[16:32])
	copy(r.RunHash[:], buf[32:64])
	r.CreatedSec = int64(binary.LittleEndian.Uint64(buf[64:72]))
	r.LastVerifySec = int64(binary.LittleEndian.Uint64(buf[72:80]))
	r.CapacitySectors = binary.LittleEndian.Uint64(buf[80:88])
	r.UsedSectors = binary.LittleEndian.Uint64(buf[88:96])
	r.RunStatus = buf[96]
	r.Health = buf[97]
	r.RsMarginPercent = binary.LittleEndian.Uint16(buf[98:100])
	r.LabelLen = binary.LittleEndian.Uint16(buf[100:102])
	copy(r.Label[:], buf[102:166])
	r.StateFlags = buf[166]
	r.Reserved = buf[167]
	r.CapacityForcedSectors = binary.LittleEndian.Uint64(buf[168:176])
	if r.Reserved != 0 {
		return ErrReserved
	}
	return nil
}

// DiscsTable is DISCS, one row per run that was burned. It carries every
// run's disc, geometry summary and verification status, and chains each
// disc's superblock hash.
type DiscsTable struct {
	Header       CommonHeader
	RepoUUID     [16]byte
	RecordCount  uint64
	RecordSize   uint16
	HashAlgo     HashAlgo
	DigestLen    uint8
	Reserved     [4]byte
	BodyCRC32C   uint32
	HeaderCRC32C uint32
	Rows         []DiscsRow
}

// EncodedLen returns the total encoded size of t: the header plus every
// row.
func (t *DiscsTable) EncodedLen() int {
	return DiscsHeaderLen + len(t.Rows)*DiscsRowLen
}

// Encode writes t into buf and returns the number of bytes written,
// EncodedLen(). It computes body_crc32c over the rows and
// header_crc32c over bytes 0 to 67, and overwrites t.BodyCRC32C and
// t.HeaderCRC32C with the computed values.
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
	binary.LittleEndian.PutUint16(buf[56:58], t.RecordSize)
	buf[58] = byte(t.HashAlgo)
	buf[59] = t.DigestLen
	copy(buf[60:64], t.Reserved[:])

	off := DiscsHeaderLen
	for i := range t.Rows {
		t.Rows[i].encode(buf[off : off+DiscsRowLen])
		off += DiscsRowLen
	}

	bodyCRC := crc32c(buf[DiscsHeaderLen:total])
	t.BodyCRC32C = bodyCRC
	binary.LittleEndian.PutUint32(buf[64:68], bodyCRC)

	headerCRC := crc32c(buf[0:68])
	t.HeaderCRC32C = headerCRC
	binary.LittleEndian.PutUint32(buf[68:72], headerCRC)
	return total, nil
}

// Decode reads a DiscsTable from buf and returns the number of bytes
// read. It rejects a short buffer, a magic_kind mismatch, a nonzero
// reserved field, and a CRC mismatch.
func (t *DiscsTable) Decode(buf []byte) (int, error) {
	if len(buf) < DiscsHeaderLen {
		return 0, ErrShort
	}
	var h CommonHeader
	if err := h.Decode(buf[0:CommonHeaderLen]); err != nil {
		return 0, err
	}
	if h.MagicKind != MagicDiscs {
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

	total := DiscsHeaderLen + int(recordCount)*DiscsRowLen
	if len(buf) < total {
		return 0, ErrShort
	}
	if bodyCRC != crc32c(buf[DiscsHeaderLen:total]) {
		return 0, ErrCRC
	}

	off := DiscsHeaderLen
	rows := make([]DiscsRow, recordCount)
	for i := range rows {
		if err := rows[i].decode(buf[off : off+DiscsRowLen]); err != nil {
			return 0, err
		}
		off += DiscsRowLen
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
	t.Rows = rows
	return total, nil
}
