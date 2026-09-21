package format

import (
	"encoding/binary"
	"reflect"
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

// structVector describes one structure for the two derived golden
// vectors: the enlarged header_len vector and the nonzero reserved
// vector. A reader must take the same fields from all three.
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
	// fields decodes buf and returns the fields a reader takes, with the
	// reserved fields, header_len and the lengths that the two variants
	// change removed.
	fields func(t *testing.T, buf []byte) any
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

// clearCommon zeroes the header fields the derived vectors change.
func clearCommon(h *CommonHeader) {
	h.HeaderLen = 0
	h.ReservedU16a = 0
	h.ReservedU16b = 0
	h.ReservedU64 = 0
}

// clearObject zeroes the object header fields the derived vectors change.
func clearObject(h *ObjectHeader) {
	h.ReservedU8 = 0
	h.ReservedA = [4]byte{}
	h.ReservedU32 = 0
	h.HeaderCRC32C = 0
	h.PayloadLen = 0
	h.StoredLen = 0
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
	ck := testChecksumRecord()

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
			fields: func(t *testing.T, buf []byte) any {
				var h CommonHeader
				if err := h.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				clearCommon(&h)
				return h
			},
		},
		{
			name:     "object_header",
			plain:    encodeVector(t, func(b []byte) (int, error) { h := testObjectHeader(); return ObjectHeaderLen, h.Encode(b) }, ObjectHeaderLen),
			reserved: objectHeaderReserved(0),
			fixup:    func([]byte) {},
			fields: func(t *testing.T, buf []byte) any {
				var h ObjectHeader
				if err := h.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				payload, stored := h.PayloadLen, h.StoredLen
				crc := h.HeaderCRC32C
				clearObject(&h)
				h.PayloadLen, h.StoredLen, h.HeaderCRC32C = payload, stored, crc
				return h
			},
		},
		{
			name:     "chunk",
			plain:    encodeVector(t, chunk.Encode, chunk.EncodedLen()),
			reserved: append(append([]rng(nil), commonReserved...), objectHeaderReserved(CommonHeaderLen)...),
			fixup:    objectFixup,
			fields: func(t *testing.T, buf []byte) any {
				var c Chunk
				if _, err := c.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				clearCommon(&c.Header)
				clearObject(&c.ObjectHeader)
				return c
			},
		},
		{
			name:     "blob",
			plain:    encodeVector(t, blob.Encode, blob.EncodedLen()),
			knownLen: blobFixedLen,
			reserved: append(append([]rng(nil), commonReserved...), objectHeaderReserved(CommonHeaderLen)...),
			fixup:    objectFixup,
			enlarge:  enlargeObject,
			fields: func(t *testing.T, buf []byte) any {
				var b Blob
				if _, err := b.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				clearCommon(&b.Header)
				clearObject(&b.ObjectHeader)
				return b
			},
		},
		{
			name:     "tree",
			plain:    encodeVector(t, tree.Encode, tree.EncodedLen()),
			knownLen: treeFixedLen,
			reserved: treeReserved,
			fixup:    objectFixup,
			enlarge:  enlargeObject,
			fields: func(t *testing.T, buf []byte) any {
				var tr Tree
				if _, err := tr.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				clearCommon(&tr.Header)
				clearObject(&tr.ObjectHeader)
				tr.ReservedU32 = 0
				for i := range tr.Entries {
					tr.Entries[i].ReservedU32 = 0
				}
				return tr
			},
		},
		{
			name:     "snapshot",
			plain:    encodeVector(t, snap.Encode, snap.EncodedLen()),
			knownLen: CommonHeaderLen + ObjectHeaderLen + SnapshotFixedLen,
			reserved: append(append(append([]rng(nil), commonReserved...), objectHeaderReserved(CommonHeaderLen)...),
				rng{128, 136}, rng{160, 168}, rng{168, 170}, rng{172, 176}),
			fixup:   objectFixup,
			enlarge: enlargeObject,
			fields: func(t *testing.T, buf []byte) any {
				var s Snapshot
				if _, err := s.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				clearCommon(&s.Common)
				clearObject(&s.Object)
				s.ReservedU64a, s.ReservedU64b, s.ReservedU16, s.ReservedU32 = 0, 0, 0, 0
				return s
			},
		},
		{
			name:     "disc",
			plain:    encodeVector(t, func(b []byte) (int, error) { return DiscLen, disc.Encode(b) }, DiscLen),
			reserved: append(append([]rng(nil), commonReserved...), rng{80, 120}, rng{136, 144}, rng{216, 2044}),
			fixup:    crcAt(2044, 2044),
			fields: func(t *testing.T, buf []byte) any {
				var d Disc
				if err := d.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				clearCommon(&d.Common)
				d.ReservedA, d.ReservedB, d.ReservedC = [40]byte{}, [8]byte{}, [1828]byte{}
				d.SuperCRC32C = 0
				return d
			},
		},
		{
			name:     "run",
			plain:    encodeVector(t, func(b []byte) (int, error) { return RunLen, run.Encode(b) }, RunLen),
			reserved: append(append([]rng(nil), commonReserved...), rng{86, 96}, rng{144, 176}, rng{192, 504}, rng{508, 512}),
			fixup:    crcAt(504, 504),
			fields: func(t *testing.T, buf []byte) any {
				var r Run
				if err := r.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				clearCommon(&r.Common)
				r.ReservedA, r.ReservedB, r.ReservedC, r.ReservedFinal = [10]byte{}, [32]byte{}, [312]byte{}, [4]byte{}
				r.HeaderCRC32C = 0
				return r
			},
		},
		{
			name:     "index",
			plain:    encodeVector(t, idx.Encode, idx.EncodedLen()),
			knownLen: IndexHeaderLen,
			reserved: append(append([]rng(nil), commonReserved...), indexReserved...),
			fixup:    func([]byte) {},
			enlarge:  enlargeCommon,
			fields: func(t *testing.T, buf []byte) any {
				var got Index
				if _, err := got.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				clearCommon(&got.Header)
				got.ReservedU32 = 0
				for i := range got.Files {
					got.Files[i].Reserved = [7]byte{}
				}
				for i := range got.Objects {
					got.Objects[i].Reserved = [7]byte{}
				}
				return got
			},
		},
		{
			name:     "refs",
			plain:    encodeVector(t, refs.Encode, refs.EncodedLen()),
			knownLen: RefsHeaderLen,
			reserved: append(append([]rng(nil), commonReserved...), refsReserved...),
			fixup:    func([]byte) {},
			enlarge:  enlargeCommon,
			fields: func(t *testing.T, buf []byte) any {
				var got RefsTable
				if _, err := got.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				clearCommon(&got.Header)
				for i := range got.Records {
					got.Records[i].ReservedU16 = 0
				}
				return got
			},
		},
		{
			name:     "discs",
			plain:    encodeVector(t, discs.Encode, discs.EncodedLen()),
			knownLen: DiscsHeaderLen,
			reserved: append(append([]rng(nil), commonReserved...), discsReserved...),
			fixup:    func([]byte) {},
			enlarge:  enlargeCommon,
			fields: func(t *testing.T, buf []byte) any {
				var got DiscsTable
				if _, err := got.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				clearCommon(&got.Header)
				for i := range got.Rows {
					got.Rows[i].ReservedU64a = 0
					got.Rows[i].ReservedU64b = 0
					got.Rows[i].ReservedU32 = 0
					got.Rows[i].Reserved = [10]byte{}
				}
				return got
			},
		},
		{
			name:     "checksum",
			plain:    encodeVector(t, func(b []byte) (int, error) { return ChecksumRecordLen, ck.Encode(b) }, ChecksumRecordLen),
			reserved: []rng{{14, 16}, {1868, 2048}},
			fixup:    crcAt(16, 16),
			fields: func(t *testing.T, buf []byte) any {
				var got ChecksumRecord
				if err := got.Decode(buf); err != nil {
					t.Fatalf("decode: %v", err)
				}
				got.ReservedU16 = 0
				got.Reserved = [180]byte{}
				got.HeaderCRC32C = 0
				return got
			},
		},
	}
}

// TestEnlargedHeaderLenVectors checks that a reader obeys a header_len
// above the one this build knows: it skips the added bytes and takes the
// same fields.
func TestEnlargedHeaderLenVectors(t *testing.T) {
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

			golden := readGolden(t, v.name+"_hdrlen.golden")
			if got, want := v.fields(t, golden), v.fields(t, v.plain); !reflect.DeepEqual(got, want) {
				t.Fatalf("fields differ from the plain vector:\ngot  %+v\nwant %+v", got, want)
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
			var err error
			switch v.name {
			case "blob":
				var b Blob
				_, err = b.Decode(buf)
			case "tree":
				var tr Tree
				_, err = tr.Decode(buf)
			case "snapshot":
				var s Snapshot
				_, err = s.Decode(buf)
			case "index":
				var idx Index
				_, err = idx.Decode(buf)
			case "refs":
				var r RefsTable
				_, err = r.Decode(buf)
			case "discs":
				var d DiscsTable
				_, err = d.Decode(buf)
			}
			if err != ErrHeaderLen {
				t.Fatalf("decode short header_len: got %v, want %v", err, ErrHeaderLen)
			}
		})
	}
}

// TestNonzeroReservedVectors checks that a reader ignores a nonzero byte
// in every reserved field and takes the same fields.
func TestNonzeroReservedVectors(t *testing.T) {
	for _, v := range structVectors(t) {
		t.Run(v.name, func(t *testing.T) {
			buf := append([]byte(nil), v.plain...)
			fill(buf, 0xA5, v.reserved)
			v.fixup(buf)
			compareGolden(t, v.name+"_reserved.golden", buf)

			golden := readGolden(t, v.name+"_reserved.golden")
			if got, want := v.fields(t, golden), v.fields(t, v.plain); !reflect.DeepEqual(got, want) {
				t.Fatalf("fields differ from the plain vector:\ngot  %+v\nwant %+v", got, want)
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
