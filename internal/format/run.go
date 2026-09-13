package format

import "encoding/binary"

// RunLen is the encoded size of the run header structure. On the medium
// the header occupies the first 512 bytes of a 2048-byte sector; the
// remaining 1536 bytes are zero and are not part of this structure.
const RunLen = 512

// RunKind is the run_kind registry.
type RunKind uint8

const (
	RunKindData            RunKind = 1
	RunKindRepair          RunKind = 2
	RunKindDiscCloseParity RunKind = 3
)

// RunFlagClosing is run_flags bit 0: this run closed the disc.
const RunFlagClosing uint8 = 1 << 0

// Run is the run header: the identity, geometry, counts and pointers of
// one run.
type Run struct {
	Common          CommonHeader
	DiscUUID        [16]byte
	RepoUUID        [16]byte
	RunSeq          uint64
	DiscSeq         uint64
	FECK            uint16
	FECM            uint16
	FECScheme       FECScheme
	HashAlgo        HashAlgo
	ChunkerProfile  ChunkerProfile
	Compression     Compression
	FSProfile       DiscFSProfile
	RunKind         RunKind
	RunFlags        uint8
	ReservedU8      uint8
	ReservedU32a    uint32
	IndexBytes      uint64
	IndexHash       [32]byte
	StreamBytes     uint64
	PrevRunHash     [32]byte
	CreatedSec      int64
	CreatedNsec     uint32
	ToolVersion     uint32
	DiscObjectCount uint64
	DiscRunIndex    uint32
	ReservedU32b    uint32
	Reserved        [296]byte
	HeaderCRC32C    uint32
	ReservedFinal   [4]byte
}

// Encode writes r into buf[0:RunLen]. buf must be at least RunLen bytes.
func (r *Run) Encode(buf []byte) error {
	if len(buf) < RunLen {
		return ErrShort
	}
	if err := r.Common.Encode(buf[0:CommonHeaderLen]); err != nil {
		return err
	}
	copy(buf[32:48], r.DiscUUID[:])
	copy(buf[48:64], r.RepoUUID[:])
	binary.LittleEndian.PutUint64(buf[64:72], r.RunSeq)
	binary.LittleEndian.PutUint64(buf[72:80], r.DiscSeq)
	binary.LittleEndian.PutUint16(buf[80:82], r.FECK)
	binary.LittleEndian.PutUint16(buf[82:84], r.FECM)
	buf[84] = byte(r.FECScheme)
	buf[85] = byte(r.HashAlgo)
	buf[86] = byte(r.ChunkerProfile)
	buf[87] = byte(r.Compression)
	buf[88] = byte(r.FSProfile)
	buf[89] = byte(r.RunKind)
	buf[90] = r.RunFlags
	buf[91] = r.ReservedU8
	binary.LittleEndian.PutUint32(buf[92:96], r.ReservedU32a)
	binary.LittleEndian.PutUint64(buf[96:104], r.IndexBytes)
	copy(buf[104:136], r.IndexHash[:])
	binary.LittleEndian.PutUint64(buf[136:144], r.StreamBytes)
	copy(buf[144:176], r.PrevRunHash[:])
	binary.LittleEndian.PutUint64(buf[176:184], uint64(r.CreatedSec))
	binary.LittleEndian.PutUint32(buf[184:188], r.CreatedNsec)
	binary.LittleEndian.PutUint32(buf[188:192], r.ToolVersion)
	binary.LittleEndian.PutUint64(buf[192:200], r.DiscObjectCount)
	binary.LittleEndian.PutUint32(buf[200:204], r.DiscRunIndex)
	binary.LittleEndian.PutUint32(buf[204:208], r.ReservedU32b)
	copy(buf[208:504], r.Reserved[:])
	binary.LittleEndian.PutUint32(buf[504:508], r.HeaderCRC32C)
	copy(buf[508:512], r.ReservedFinal[:])
	return nil
}

// Decode reads a Run from buf. It rejects a short buffer, a magic_kind
// mismatch, and a nonzero reserved field.
func (r *Run) Decode(buf []byte) error {
	if len(buf) < RunLen {
		return ErrShort
	}
	if err := r.Common.Decode(buf[0:CommonHeaderLen]); err != nil {
		return err
	}
	if r.Common.MagicKind != MagicRun {
		return ErrBadMagic
	}
	copy(r.DiscUUID[:], buf[32:48])
	copy(r.RepoUUID[:], buf[48:64])
	r.RunSeq = binary.LittleEndian.Uint64(buf[64:72])
	r.DiscSeq = binary.LittleEndian.Uint64(buf[72:80])
	r.FECK = binary.LittleEndian.Uint16(buf[80:82])
	r.FECM = binary.LittleEndian.Uint16(buf[82:84])
	r.FECScheme = FECScheme(buf[84])
	r.HashAlgo = HashAlgo(buf[85])
	r.ChunkerProfile = ChunkerProfile(buf[86])
	r.Compression = Compression(buf[87])
	r.FSProfile = DiscFSProfile(buf[88])
	r.RunKind = RunKind(buf[89])
	r.RunFlags = buf[90]
	r.ReservedU8 = buf[91]
	if r.ReservedU8 != 0 {
		return ErrReserved
	}
	r.ReservedU32a = binary.LittleEndian.Uint32(buf[92:96])
	if r.ReservedU32a != 0 {
		return ErrReserved
	}
	r.IndexBytes = binary.LittleEndian.Uint64(buf[96:104])
	copy(r.IndexHash[:], buf[104:136])
	r.StreamBytes = binary.LittleEndian.Uint64(buf[136:144])
	copy(r.PrevRunHash[:], buf[144:176])
	r.CreatedSec = int64(binary.LittleEndian.Uint64(buf[176:184]))
	r.CreatedNsec = binary.LittleEndian.Uint32(buf[184:188])
	r.ToolVersion = binary.LittleEndian.Uint32(buf[188:192])
	r.DiscObjectCount = binary.LittleEndian.Uint64(buf[192:200])
	r.DiscRunIndex = binary.LittleEndian.Uint32(buf[200:204])
	r.ReservedU32b = binary.LittleEndian.Uint32(buf[204:208])
	if r.ReservedU32b != 0 {
		return ErrReserved
	}
	copy(r.Reserved[:], buf[208:504])
	for _, b := range r.Reserved {
		if b != 0 {
			return ErrReserved
		}
	}
	r.HeaderCRC32C = binary.LittleEndian.Uint32(buf[504:508])
	copy(r.ReservedFinal[:], buf[508:512])
	for _, b := range r.ReservedFinal {
		if b != 0 {
			return ErrReserved
		}
	}
	return nil
}
