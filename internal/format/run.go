package format

import (
	"encoding/binary"
	"fmt"
)

// RunLen is the encoded size of the run header. RUN.bin and RUN2.bin are
// each exactly this long.
const RunLen = 512

// Run is the run header: the identity, geometry and pointers of one run.
// It is the root of trust of the disc.
type Run struct {
	Common        CommonHeader
	DiscUUID      [16]byte
	RepoUUID      [16]byte
	RunSeq        uint64
	DiscSeq       uint64
	FECK          uint16
	FECM          uint16
	FECScheme     FECScheme
	HashAlgo      HashAlgo
	ReservedA     [10]byte
	IndexBytes    uint64
	IndexHash     [32]byte
	ReservedD     uint64
	ReservedB     [32]byte
	CreatedSec    int64
	CreatedNsec   uint32
	ToolVersion   uint32
	ReservedC     [312]byte
	HeaderCRC32C  uint32
	ReservedFinal [4]byte
}

// Encode writes r into buf[0:RunLen]. buf must be at least RunLen bytes.
// Encode computes header_crc32c itself, over bytes 0 to 503; any value in
// r.HeaderCRC32C is overwritten.
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
	copy(buf[86:96], r.ReservedA[:])
	binary.LittleEndian.PutUint64(buf[96:104], r.IndexBytes)
	copy(buf[104:136], r.IndexHash[:])
	binary.LittleEndian.PutUint64(buf[136:144], r.ReservedD)
	copy(buf[144:176], r.ReservedB[:])
	binary.LittleEndian.PutUint64(buf[176:184], uint64(r.CreatedSec))
	binary.LittleEndian.PutUint32(buf[184:188], r.CreatedNsec)
	binary.LittleEndian.PutUint32(buf[188:192], r.ToolVersion)
	copy(buf[192:504], r.ReservedC[:])
	crc := crc32c(buf[0:504])
	r.HeaderCRC32C = crc
	binary.LittleEndian.PutUint32(buf[504:508], crc)
	copy(buf[508:512], r.ReservedFinal[:])
	return nil
}

// Decode reads a Run from buf. It rejects a short buffer, a magic_kind
// mismatch, a header_crc32c mismatch, a nonzero reserved_d, and a nonzero
// fec_k or fec_m under fec_scheme 0. It does not interpret any other
// reserved field or padding byte.
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
	copy(r.ReservedA[:], buf[86:96])
	r.IndexBytes = binary.LittleEndian.Uint64(buf[96:104])
	copy(r.IndexHash[:], buf[104:136])
	r.ReservedD = binary.LittleEndian.Uint64(buf[136:144])
	copy(r.ReservedB[:], buf[144:176])
	r.CreatedSec = int64(binary.LittleEndian.Uint64(buf[176:184]))
	r.CreatedNsec = binary.LittleEndian.Uint32(buf[184:188])
	r.ToolVersion = binary.LittleEndian.Uint32(buf[188:192])
	copy(r.ReservedC[:], buf[192:504])
	r.HeaderCRC32C = binary.LittleEndian.Uint32(buf[504:508])
	if crc32c(buf[0:504]) != r.HeaderCRC32C {
		return ErrCRC
	}
	copy(r.ReservedFinal[:], buf[508:512])
	return r.checkFields()
}

// checkFields refuses the field values that a reader of format major 1
// cannot accept, and names the field and the value.
func (r *Run) checkFields() error {
	if r.ReservedD != 0 {
		return fmt.Errorf("%w: run header reserved_d is %d, want 0", ErrBadField, r.ReservedD)
	}
	if r.FECScheme == FECSchemeNone {
		if r.FECK != 0 {
			return fmt.Errorf("%w: run header fec_k is %d under fec_scheme 0, want 0", ErrBadField, r.FECK)
		}
		if r.FECM != 0 {
			return fmt.Errorf("%w: run header fec_m is %d under fec_scheme 0, want 0", ErrBadField, r.FECM)
		}
	}
	return nil
}
