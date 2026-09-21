package format

import "testing"

func testTreeEntries() []TreeEntry {
	var dirID, fileID [32]byte
	for i := range dirID {
		dirID[i] = 0xAA
	}
	for i := range fileID {
		fileID[i] = 0xBB
	}
	dir := TreeEntry{
		EntryType:  EntryTypeDirectory,
		EntryFlags: 0,
		Size:       0,
		MtimeSec:   1700000000,
		Mode:       0o755,
		UID:        1000,
		GID:        1000,
		Name:       []byte("docs"),
		ContentID:  dirID,
	}
	file := TreeEntry{
		EntryType:  EntryTypeRegular,
		EntryFlags: EntryFlagCtimeAbsent,
		Size:       12345,
		MtimeSec:   1700000001,
		MtimeNsec:  500,
		Mode:       0o644,
		UID:        1000,
		GID:        1000,
		Name:       []byte("file.txt"),
		ContentID:  fileID,
		TLVs: []TLV{
			{Type: TLVTypeUserName, Flags: 0, Payload: []byte("alice")},
		},
	}
	return []TreeEntry{dir, file}
}

func testTree() Tree {
	entries := testTreeEntries()
	entriesLen := 0
	for i := range entries {
		entriesLen += entries[i].EncodedLen()
	}
	payloadLen := treeBodyFixedLen + entriesLen
	return Tree{
		Header: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicTree,
			VersionMajor: 1,
			HeaderLen:    treeFixedLen,
		},
		ObjectHeader: ObjectHeader{
			Kind:        ObjectKindTree,
			HashAlgo:    HashAlgoSHA256,
			Compression: CompressionNone,
			PayloadLen:  uint64(payloadLen),
			StoredLen:   uint64(payloadLen),
		},
		EntryCount: uint32(len(entries)),
		Entries:    entries,
	}
}

func TestTreeGolden(t *testing.T) {
	tr := testTree()
	buf := make([]byte, tr.EncodedLen())
	if _, err := tr.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "tree.golden", buf)

	golden := readGolden(t, "tree.golden")
	var got Tree
	n, err := got.Decode(golden)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != len(golden) {
		t.Fatalf("decode consumed %d bytes, want %d", n, len(golden))
	}
	if got.Header != tr.Header {
		t.Fatalf("header mismatch: got %+v, want %+v", got.Header, tr.Header)
	}
	if got.EntryCount != tr.EntryCount || got.ReservedU32 != 0 {
		t.Fatalf("body mismatch: got %+v, want %+v", got, tr)
	}
	if len(got.Entries) != len(tr.Entries) {
		t.Fatalf("entry count mismatch: got %d, want %d", len(got.Entries), len(tr.Entries))
	}
	for i := range tr.Entries {
		want := tr.Entries[i]
		g := got.Entries[i]
		if g.EntryType != want.EntryType || g.EntryFlags != want.EntryFlags ||
			g.Size != want.Size ||
			g.MtimeSec != want.MtimeSec || g.MtimeNsec != want.MtimeNsec ||
			g.Mode != want.Mode || g.UID != want.UID || g.GID != want.GID ||
			string(g.Name) != string(want.Name) || g.ContentID != want.ContentID {
			t.Fatalf("entry %d mismatch: got %+v, want %+v", i, g, want)
		}
		if len(g.TLVs) != len(want.TLVs) {
			t.Fatalf("entry %d TLV count mismatch: got %d, want %d", i, len(g.TLVs), len(want.TLVs))
		}
		for j := range want.TLVs {
			if g.TLVs[j].Type != want.TLVs[j].Type || string(g.TLVs[j].Payload) != string(want.TLVs[j].Payload) {
				t.Fatalf("entry %d TLV %d mismatch: got %+v, want %+v", i, j, g.TLVs[j], want.TLVs[j])
			}
		}
	}
}

func TestTreeDecodeIgnoresReservedField(t *testing.T) {
	golden := readGolden(t, "tree.golden")
	buf := append([]byte(nil), golden...)
	buf[CommonHeaderLen+ObjectHeaderLen+4] = 0xFF // the body's reserved_u32

	var got Tree
	if _, err := got.Decode(buf); err != nil {
		t.Fatalf("decode nonzero reserved field: %v", err)
	}
	if got.ReservedU32 == 0 {
		t.Fatalf("reserved field not preserved: %d", got.ReservedU32)
	}
	tr := testTree()
	if got.EntryCount != tr.EntryCount {
		t.Fatalf("body mismatch: got %+v, want %+v", got, tr)
	}
}

func TestTreeEntryDecodeIgnoresPaddingBytes(t *testing.T) {
	e := TreeEntry{
		EntryType: EntryTypeRegular,
		Mode:      0o644,
		Name:      []byte("f"),
		ContentID: [32]byte{1, 2, 3},
	}
	buf := make([]byte, e.EncodedLen())
	if _, err := e.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	// The name is one byte, so the alignment padding before the content
	// area, offset 73 to 79, is nonzero here.
	buf[73] = 0xFF

	var got TreeEntry
	n, err := got.Decode(buf)
	if err != nil {
		t.Fatalf("decode nonzero padding byte: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("decode read %d bytes, want %d", n, len(buf))
	}
	if got.ContentID != e.ContentID || string(got.Name) != string(e.Name) {
		t.Fatalf("decoded mismatch: got %+v, want %+v", got, e)
	}
}

func TestTreeDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "tree.golden")
	var tr Tree
	if _, err := tr.Decode(golden[:treeFixedLen-1]); err != ErrShort {
		t.Fatalf("decode short buffer: got %v, want %v", err, ErrShort)
	}
}

func TestTreeDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "tree.golden")
	buf := append([]byte(nil), golden...)
	buf[8] = 'X'
	var tr Tree
	if _, err := tr.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}

func TestTreeDecodeRejectsBadOrder(t *testing.T) {
	entries := testTreeEntries()
	// Reverse the entries so "file.txt" precedes "docs/", violating the
	// mandatory ascending name order.
	entries[0], entries[1] = entries[1], entries[0]
	entriesLen := 0
	for i := range entries {
		entriesLen += entries[i].EncodedLen()
	}
	payloadLen := treeBodyFixedLen + entriesLen
	tr := Tree{
		Header: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicTree,
			VersionMajor: 1,
			HeaderLen:    treeFixedLen,
		},
		ObjectHeader: ObjectHeader{
			Kind:        ObjectKindTree,
			HashAlgo:    HashAlgoSHA256,
			Compression: CompressionNone,
			PayloadLen:  uint64(payloadLen),
			StoredLen:   uint64(payloadLen),
		},
		EntryCount: uint32(len(entries)),
		Entries:    entries,
	}
	buf := make([]byte, tr.EncodedLen())
	if _, err := tr.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got Tree
	if _, err := got.Decode(buf); err != ErrBadField {
		t.Fatalf("decode out-of-order entries: got %v, want %v", err, ErrBadField)
	}
}

func TestTreeEntryDecodeRejectsBadName(t *testing.T) {
	e := TreeEntry{
		EntryType: EntryTypeRegular,
		Mode:      0o644,
		Name:      []byte(".."),
	}
	buf := make([]byte, e.EncodedLen())
	if _, err := e.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got TreeEntry
	if _, err := got.Decode(buf); err != ErrBadField {
		t.Fatalf("decode name '..': got %v, want %v", err, ErrBadField)
	}
}

func TestTreeEntryDecodeRejectsUnknownCriticalTLV(t *testing.T) {
	e := TreeEntry{
		EntryType: EntryTypeRegular,
		Mode:      0o644,
		Name:      []byte("f"),
		TLVs: []TLV{
			{Type: 0x9000, Flags: TLVFlagCritical, Payload: []byte("x")},
		},
	}
	buf := make([]byte, e.EncodedLen())
	if _, err := e.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got TreeEntry
	if _, err := got.Decode(buf); err != ErrBadField {
		t.Fatalf("decode unknown critical TLV: got %v, want %v", err, ErrBadField)
	}
}
