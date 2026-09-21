package format

import "testing"

func fileHashN(n int) [32]byte {
	var h [32]byte
	for i := range h {
		h[i] = byte((n*3 + i) % 256)
	}
	return h
}

func contentIDN(n int) [32]byte {
	var h [32]byte
	for i := range h {
		h[i] = byte((n*7 + i*2) % 256)
	}
	return h
}

func testIndex() Index {
	files := []IndexFileRecord{
		{ByteLen: 8192, Role: FileRoleIndex},
		{ByteLen: 512, Role: FileRoleRun},
		{FileHash: fileHashN(1), ByteLen: 2048, Role: FileRoleDisc},
		{FileHash: fileHashN(2), ByteLen: 5624, Role: FileRoleRefs},
		{FileHash: fileHashN(3), ByteLen: 408, Role: FileRoleDiscs},
		{ByteLen: 4160, Role: FileRoleObject},
		{ByteLen: 2112, Role: FileRoleObject},
		{ByteLen: 328, Role: FileRoleObject},
		{ByteLen: 240, Role: FileRoleObject},
		{ByteLen: 512, Role: FileRoleRun2},
	}
	return Index{
		Header: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicIndex,
			VersionMajor: 1,
			HeaderLen:    IndexHeaderLen,
		},
		RunSeq:      7,
		FileCount:   uint32(len(files)),
		ObjectCount: 4,
		PrereqCount: 2,
		Files:       files,
		Objects: []IndexObjectRecord{
			{ContentID: contentIDN(1), Kind: ObjectKindChunk},
			{ContentID: contentIDN(2), Kind: ObjectKindBlob},
			{ContentID: contentIDN(3), Kind: ObjectKindTree},
			{ContentID: contentIDN(4), Kind: ObjectKindSnapshot},
		},
		Prereqs: []IndexPrereqRecord{
			{ContentID: contentIDN(5), DiscUUID: discUUIDN(1)},
			{ContentID: contentIDN(6), DiscUUID: discUUIDN(2)},
		},
	}
}

func TestIndexGolden(t *testing.T) {
	idx := testIndex()
	buf := make([]byte, idx.EncodedLen())
	if _, err := idx.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "index.golden", buf)

	golden := readGolden(t, "index.golden")
	var got Index
	n, err := got.Decode(golden)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != len(golden) {
		t.Fatalf("decode consumed %d bytes, want %d", n, len(golden))
	}

	if got.RunSeq != idx.RunSeq || got.FileCount != idx.FileCount ||
		got.ObjectCount != idx.ObjectCount || got.PrereqCount != idx.PrereqCount {
		t.Fatalf("decoded fixed fields mismatch: got %+v", got)
	}
	for i := range idx.Files {
		if got.Files[i] != idx.Files[i] {
			t.Errorf("file row %d mismatch: got %+v, want %+v", i, got.Files[i], idx.Files[i])
		}
	}
	for i := range idx.Objects {
		if got.Objects[i] != idx.Objects[i] {
			t.Errorf("object row %d mismatch: got %+v, want %+v", i, got.Objects[i], idx.Objects[i])
		}
	}
	for i := range idx.Prereqs {
		if got.Prereqs[i] != idx.Prereqs[i] {
			t.Errorf("prereq row %d mismatch: got %+v, want %+v", i, got.Prereqs[i], idx.Prereqs[i])
		}
	}

	if got.ReservedU32 != 0 {
		t.Errorf("reserved_u32 not zero: %d", got.ReservedU32)
	}
	for i, f := range got.Files {
		if f.Reserved != ([7]byte{}) {
			t.Errorf("file row %d reserved not zero: %x", i, f.Reserved)
		}
	}
	for i, o := range got.Objects {
		if o.Reserved != ([7]byte{}) {
			t.Errorf("object row %d reserved not zero: %x", i, o.Reserved)
		}
	}
}

// TestIndexObjectRowsPairWithRole13Rows checks the pairing that replaced
// the stored file index: the j-th role 13 Files row describes the file of
// Objects row j.
func TestIndexObjectRowsPairWithRole13Rows(t *testing.T) {
	idx := testIndex()
	var role13 []IndexFileRecord
	for _, f := range idx.Files {
		if f.Role == FileRoleObject {
			role13 = append(role13, f)
		}
	}
	if len(role13) != int(idx.ObjectCount) {
		t.Fatalf("role 13 rows: got %d, want object_count %d", len(role13), idx.ObjectCount)
	}
	for i, f := range role13 {
		if f.FileHash != ([32]byte{}) {
			t.Errorf("role 13 row %d carries a file_hash: %x", i, f.FileHash)
		}
	}
	for i := 1; i < len(idx.Objects); i++ {
		if string(idx.Objects[i-1].ContentID[:]) >= string(idx.Objects[i].ContentID[:]) {
			t.Fatalf("Objects rows are not in ascending content id order at %d", i)
		}
	}
}

func TestIndexDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "index.golden")
	var idx Index
	if _, err := idx.Decode(golden[:IndexHeaderLen-1]); err != ErrShort {
		t.Fatalf("decode short header: got %v, want %v", err, ErrShort)
	}
	if _, err := idx.Decode(golden[:len(golden)-1]); err != ErrShort {
		t.Fatalf("decode short body: got %v, want %v", err, ErrShort)
	}
}

func TestIndexDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "index.golden")
	buf := append([]byte(nil), golden...)
	buf[8] = 'X'
	var idx Index
	if _, err := idx.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}

func TestIndexDecodeRejectsBadObjectKind(t *testing.T) {
	golden := readGolden(t, "index.golden")
	buf := append([]byte(nil), golden...)
	row := IndexHeaderLen + 10*IndexFileRecordLen
	buf[row+32] = 9
	var idx Index
	if _, err := idx.Decode(buf); err != ErrObjectKind {
		t.Fatalf("decode bad kind: got %v, want %v", err, ErrObjectKind)
	}
}
