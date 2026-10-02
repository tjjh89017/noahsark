package format

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// rng is a half-open byte range of a structure.
type rng struct{ lo, hi int }

// fill writes b into every byte of every range.
func fill(buf []byte, b byte, ranges []rng) {
	for _, r := range ranges {
		for i := r.lo; i < r.hi; i++ {
			buf[i] = b
		}
	}
}

// structVector describes one structure for the derived golden vectors:
// the other header_len vector, the nonzero reserved vector and the
// version_major 0 vector. A reader must refuse each of them.
type structVector struct {
	// name is the golden file base name.
	name string
	// plain is the bytes the writer of this version produces.
	plain []byte
	// knownLen is the offset of the first byte after the fixed part.
	// Zero means the structure takes no enlarged header_len vector.
	knownLen int
	// reserved lists every reserved range of the plain bytes.
	reserved []rng
	// fixup recomputes every CRC of the structure after a mutation.
	fixup func(buf []byte)
	// enlarge raises the length fields that a larger fixed part changes.
	enlarge func(buf []byte, extra int)
	// decode decodes buf as the structure.
	decode func(buf []byte) error
	// kind is true for a structure with a common header of its own.
	kind bool
}

// crcAt recomputes a CRC-32C over buf[0:covered] and stores it at off.
func crcAt(off, covered int) func([]byte) {
	return func(buf []byte) {
		binary.LittleEndian.PutUint32(buf[off:off+4], crc32c(buf[0:covered]))
	}
}

// objectFixup recomputes the object header CRC of an object file.
var objectFixup = crcAt(objectHeaderCRCOffset, objectHeaderCRCOffset)

// commonReserved is the reserved range set of the 32-byte common header.
var commonReserved = []rng{{18, 20}, {22, 24}, {24, 32}}

// objectHeaderReserved is the reserved range set of the object header at
// base, the offset where that header starts.
func objectHeaderReserved(base int) []rng {
	return []rng{{base + 2, base + 3}, {base + 4, base + 8}, {base + 28, base + 32}}
}

// enlargeCommon raises header_len by extra.
func enlargeCommon(buf []byte, extra int) {
	binary.LittleEndian.PutUint16(buf[20:22], binary.LittleEndian.Uint16(buf[20:22])+uint16(extra))
}

// enlargeObject raises header_len, payload_len and stored_len by extra:
// the bytes a later writer adds to the fixed part are payload bytes.
func enlargeObject(buf []byte, extra int) {
	enlargeCommon(buf, extra)
	base := CommonHeaderLen
	binary.LittleEndian.PutUint64(buf[base+8:base+16], binary.LittleEndian.Uint64(buf[base+8:base+16])+uint64(extra))
	binary.LittleEndian.PutUint64(buf[base+16:base+24], binary.LittleEndian.Uint64(buf[base+16:base+24])+uint64(extra))
}

func encodeVector(t *testing.T, enc func([]byte) (int, error), size int) []byte {
	t.Helper()
	buf := make([]byte, size)
	n, err := enc(buf)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf[:n]
}

func structVectors(t *testing.T) []structVector {
	t.Helper()

	blob := testBlob()
	tree := testTree()
	snap := testSnapshot()
	idx := testIndex()
	refs := testRefsTable()
	discs := testDiscsTable()
	chunk := testChunk()
	disc := testDisc()
	run := testRun()

	indexReserved := []rng{{52, 56}}
	for i := range idx.Files {
		row := IndexHeaderLen + i*IndexFileRecordLen
		indexReserved = append(indexReserved, rng{row + 41, row + 48})
	}
	objBase := IndexHeaderLen + len(idx.Files)*IndexFileRecordLen
	for i := range idx.Objects {
		row := objBase + i*IndexObjectRecordLen
		indexReserved = append(indexReserved, rng{row + 33, row + 40})
	}

	refsReserved := []rng(nil)
	for i := range refs.Records {
		row := RefsHeaderLen + i*RefRecordLen
		refsReserved = append(refsReserved, rng{row + 46, row + 48})
	}

	discsReserved := []rng(nil)
	for i := range discs.Rows {
		row := DiscsHeaderLen + i*DiscsRowLen
		discsReserved = append(discsReserved,
			rng{row + 72, row + 80}, rng{row + 88, row + 96},
			rng{row + 96, row + 100}, rng{row + 166, row + 176})
	}

	treeReserved := append(append([]rng(nil), commonReserved...), objectHeaderReserved(CommonHeaderLen)...)
	treeReserved = append(treeReserved, rng{treeFixedLen - 4, treeFixedLen})
	treeReserved = append(treeReserved, rng{treeFixedLen + 68, treeFixedLen + 72})

	return []structVector{
		{
			name:     "common_header",
			plain:    encodeVector(t, func(b []byte) (int, error) { h := testCommonHeader(); return CommonHeaderLen, h.Encode(b) }, CommonHeaderLen),
			reserved: commonReserved,
			fixup:    func([]byte) {},
			decode: func(buf []byte) error {
				var h CommonHeader
				return h.Decode(buf)
			},
		},
		{
			name:     "object_header",
			plain:    encodeVector(t, func(b []byte) (int, error) { h := testObjectHeader(); return ObjectHeaderLen, h.Encode(b) }, ObjectHeaderLen),
			reserved: objectHeaderReserved(0),
			fixup:    func([]byte) {},
			decode: func(buf []byte) error {
				var h ObjectHeader
				return h.Decode(buf)
			},
		},
		{
			name:     "chunk",
			plain:    encodeVector(t, chunk.Encode, chunk.EncodedLen()),
			reserved: append(append([]rng(nil), commonReserved...), objectHeaderReserved(CommonHeaderLen)...),
			fixup:    objectFixup,
			decode: func(buf []byte) error {
				var c Chunk
				_, err := c.Decode(buf)
				return err
			},
			kind: true,
		},
		{
			name:     "blob",
			plain:    encodeVector(t, blob.Encode, blob.EncodedLen()),
			knownLen: blobFixedLen,
			reserved: append(append([]rng(nil), commonReserved...), objectHeaderReserved(CommonHeaderLen)...),
			fixup:    objectFixup,
			enlarge:  enlargeObject,
			decode: func(buf []byte) error {
				var b Blob
				_, err := b.Decode(buf)
				return err
			},
			kind: true,
		},
		{
			name:     "tree",
			plain:    encodeVector(t, tree.Encode, tree.EncodedLen()),
			knownLen: treeFixedLen,
			reserved: treeReserved,
			fixup:    objectFixup,
			enlarge:  enlargeObject,
			decode: func(buf []byte) error {
				var tr Tree
				_, err := tr.Decode(buf)
				return err
			},
			kind: true,
		},
		{
			name:     "snapshot",
			plain:    encodeVector(t, snap.Encode, snap.EncodedLen()),
			knownLen: CommonHeaderLen + ObjectHeaderLen + SnapshotFixedLen,
			reserved: append(append(append([]rng(nil), commonReserved...), objectHeaderReserved(CommonHeaderLen)...),
				rng{128, 136}, rng{160, 168}, rng{168, 170}, rng{172, 176}),
			fixup:   objectFixup,
			enlarge: enlargeObject,
			decode: func(buf []byte) error {
				var sn Snapshot
				_, err := sn.Decode(buf)
				return err
			},
			kind: true,
		},
		{
			name:     "disc",
			plain:    encodeVector(t, func(b []byte) (int, error) { return DiscLen, disc.Encode(b) }, DiscLen),
			reserved: append(append([]rng(nil), commonReserved...), rng{80, 120}, rng{136, 144}, rng{216, 2044}),
			fixup:    crcAt(2044, 2044),
			decode: func(buf []byte) error {
				var d Disc
				return d.Decode(buf)
			},
			kind: true,
		},
		{
			name:     "run",
			plain:    encodeVector(t, func(b []byte) (int, error) { return RunLen, run.Encode(b) }, RunLen),
			reserved: append(append([]rng(nil), commonReserved...), rng{86, 96}, rng{144, 176}, rng{192, 504}, rng{508, 512}),
			fixup:    crcAt(504, 504),
			decode: func(buf []byte) error {
				var r Run
				return r.Decode(buf)
			},
			kind: true,
		},
		{
			name:     "index",
			plain:    encodeVector(t, idx.Encode, idx.EncodedLen()),
			knownLen: IndexHeaderLen,
			reserved: append(append([]rng(nil), commonReserved...), indexReserved...),
			fixup:    func([]byte) {},
			enlarge:  enlargeCommon,
			decode: func(buf []byte) error {
				var got Index
				_, err := got.Decode(buf)
				return err
			},
			kind: true,
		},
		{
			name:     "refs",
			plain:    encodeVector(t, refs.Encode, refs.EncodedLen()),
			knownLen: RefsHeaderLen,
			reserved: append(append([]rng(nil), commonReserved...), refsReserved...),
			fixup:    func([]byte) {},
			enlarge:  enlargeCommon,
			decode: func(buf []byte) error {
				var got RefsTable
				_, err := got.Decode(buf)
				return err
			},
			kind: true,
		},
		{
			name:     "discs",
			plain:    encodeVector(t, discs.Encode, discs.EncodedLen()),
			knownLen: DiscsHeaderLen,
			reserved: append(append([]rng(nil), commonReserved...), discsReserved...),
			fixup:    func([]byte) {},
			enlarge:  enlargeCommon,
			decode: func(buf []byte) error {
				var got DiscsTable
				_, err := got.Decode(buf)
				return err
			},
			kind: true,
		},
	}
}

// TestOtherHeaderLenVectorsAreRefused checks that a reader refuses a
// header_len 8 above the one this build knows, with 8 nonzero bytes in
// the gap, and names header_len.
func TestOtherHeaderLenVectorsAreRefused(t *testing.T) {
	const extra = 8
	for _, v := range structVectors(t) {
		if v.knownLen == 0 {
			continue
		}
		t.Run(v.name, func(t *testing.T) {
			buf := make([]byte, 0, len(v.plain)+extra)
			buf = append(buf, v.plain[:v.knownLen]...)
			for i := range extra {
				buf = append(buf, byte(0xA0+i))
			}
			buf = append(buf, v.plain[v.knownLen:]...)
			v.enlarge(buf, extra)
			v.fixup(buf)
			compareGolden(t, v.name+"_hdrlen.golden", buf)

			err := v.decode(readGolden(t, v.name+"_hdrlen.golden"))
			if !errors.Is(err, ErrHeaderLen) || !strings.Contains(err.Error(), "header_len is") {
				t.Fatalf("decode: got %v, want %v that names header_len", err, ErrHeaderLen)
			}
		})
	}
}

// TestHeaderLenBelowKnownIsRefused checks that a reader refuses a
// header_len below the fixed part it knows.
func TestHeaderLenBelowKnownIsRefused(t *testing.T) {
	for _, v := range structVectors(t) {
		if v.knownLen == 0 {
			continue
		}
		t.Run(v.name, func(t *testing.T) {
			buf := append([]byte(nil), v.plain...)
			binary.LittleEndian.PutUint16(buf[20:22], binary.LittleEndian.Uint16(buf[20:22])-8)
			v.fixup(buf)
			if err := v.decode(buf); !errors.Is(err, ErrHeaderLen) {
				t.Fatalf("decode short header_len: got %v, want %v", err, ErrHeaderLen)
			}
		})
	}
}

// TestNonzeroReservedVectorsAreRefused checks that a reader refuses the
// vector with a nonzero byte in every reserved field.
func TestNonzeroReservedVectorsAreRefused(t *testing.T) {
	for _, v := range structVectors(t) {
		t.Run(v.name, func(t *testing.T) {
			buf := append([]byte(nil), v.plain...)
			fill(buf, 0xA5, v.reserved)
			v.fixup(buf)
			compareGolden(t, v.name+"_reserved.golden", buf)

			if err := v.decode(readGolden(t, v.name+"_reserved.golden")); !errors.Is(err, ErrReserved) {
				t.Fatalf("decode: got %v, want %v", err, ErrReserved)
			}
		})
	}
}

// TestEachReservedRangeIsRefused sets one reserved range at a time to a
// nonzero value. A reader refuses each one and names the value.
func TestEachReservedRangeIsRefused(t *testing.T) {
	for _, v := range structVectors(t) {
		for _, r := range v.reserved {
			t.Run(fmt.Sprintf("%s/%d-%d", v.name, r.lo, r.hi), func(t *testing.T) {
				buf := append([]byte(nil), v.plain...)
				fill(buf, 0xA5, []rng{r})
				v.fixup(buf)
				err := v.decode(buf)
				if !errors.Is(err, ErrReserved) || !strings.Contains(err.Error(), "0xa5") {
					t.Fatalf("decode: got %v, want %v that names the value 0xa5", err, ErrReserved)
				}
			})
		}
	}
}

// TestVersionMajorZeroVectors checks the vector with version_major 0 of
// each structure kind. A reader refuses it and prints the value.
func TestVersionMajorZeroVectors(t *testing.T) {
	for _, v := range structVectors(t) {
		if !v.kind {
			continue
		}
		t.Run(v.name, func(t *testing.T) {
			buf := append([]byte(nil), v.plain...)
			binary.LittleEndian.PutUint16(buf[16:18], 0)
			v.fixup(buf)
			compareGolden(t, v.name+"_vmajor0.golden", buf)

			err := v.decode(readGolden(t, v.name+"_vmajor0.golden"))
			if !errors.Is(err, ErrVersion) || !strings.Contains(err.Error(), "version_major is 0") {
				t.Fatalf("decode: got %v, want %v that prints version_major 0", err, ErrVersion)
			}
		})
	}
}

// TestPlainVectorsHaveZeroReserved checks that the writer wrote zero into
// every reserved field and every padding byte of the plain vectors.
func TestPlainVectorsHaveZeroReserved(t *testing.T) {
	for _, v := range structVectors(t) {
		t.Run(v.name, func(t *testing.T) {
			for _, r := range v.reserved {
				for i := r.lo; i < r.hi; i++ {
					if v.plain[i] != 0 {
						t.Fatalf("reserved byte at offset %d is 0x%02x, want zero", i, v.plain[i])
					}
				}
			}
		})
	}
}
