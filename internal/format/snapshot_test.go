package format

import "testing"

func testSnapshot() Snapshot {
	s := Snapshot{
		Common: CommonHeader{
			MagicProject: ProjectMagic,
			MagicKind:    MagicSnapshot,
			VersionMajor: 1,
			VersionMinor: 0,
			HeaderLen:    CommonHeaderLen + ObjectHeaderLen + SnapshotFixedLen,
		},
		Object: ObjectHeader{
			Kind:      ObjectKindSnapshot,
			HashAlgo:  HashAlgoSHA256,
			DigestLen: 32,
		},
		Generation:           1,
		TimeSec:              1700000000,
		TimeNsec:             123456789,
		TzOffsetSec:          3600,
		TotalSize:            123456,
		ReachableObjectCount: 42,
		HashAlgo:             HashAlgoSHA256,
		ChunkerProfile:       ChunkerProfileP3,
		MetaCount:            2,
		SourceType:           SnapshotSourceLocal,
		ParentHashAlgo:       0,
		Meta: []SnapshotMeta{
			{Tag: SnapshotMetaAuthor, Value: []byte("date")},
			{Tag: SnapshotMetaHost, Value: []byte("host1")},
		},
	}
	for i := range s.RootTree {
		s.RootTree[i] = byte(i + 1)
	}
	return s
}

func TestSnapshotGolden(t *testing.T) {
	s := testSnapshot()
	buf := make([]byte, s.EncodedLen())
	if _, err := s.Encode(buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, "snapshot.golden", buf)

	golden := readGolden(t, "snapshot.golden")
	var got Snapshot
	n, err := got.Decode(golden)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != len(golden) {
		t.Fatalf("decode consumed %d bytes, want %d", n, len(golden))
	}
	if got.RootTree != s.RootTree || got.Parent != s.Parent ||
		got.Generation != s.Generation || got.TimeSec != s.TimeSec ||
		got.TimeNsec != s.TimeNsec || got.TzOffsetSec != s.TzOffsetSec ||
		got.TotalSize != s.TotalSize ||
		got.ReachableObjectCount != s.ReachableObjectCount ||
		got.HashAlgo != s.HashAlgo || got.ChunkerProfile != s.ChunkerProfile ||
		got.MetaCount != s.MetaCount || got.SourceType != s.SourceType ||
		got.SourceFlags != s.SourceFlags || got.ParentHashAlgo != s.ParentHashAlgo {
		t.Fatalf("decoded snapshot fixed body mismatch: got %+v, want %+v", got, s)
	}
	if len(got.Meta) != len(s.Meta) {
		t.Fatalf("meta count mismatch: got %d, want %d", len(got.Meta), len(s.Meta))
	}
	for i := range s.Meta {
		if got.Meta[i].Tag != s.Meta[i].Tag || got.Meta[i].Flags != s.Meta[i].Flags ||
			string(got.Meta[i].Value) != string(s.Meta[i].Value) {
			t.Fatalf("meta[%d] mismatch: got %+v, want %+v", i, got.Meta[i], s.Meta[i])
		}
	}

	if got.ReservedU8 != 0 {
		t.Errorf("reserved_u8 not zero: 0x%02x", got.ReservedU8)
	}
}

func TestSnapshotDecodeRejectsBadMagic(t *testing.T) {
	golden := readGolden(t, "snapshot.golden")
	buf := append([]byte(nil), golden...)
	buf[8] = 'X'
	var s Snapshot
	if _, err := s.Decode(buf); err != ErrBadMagic {
		t.Fatalf("decode bad magic: got %v, want %v", err, ErrBadMagic)
	}
}

func TestSnapshotDecodeRejectsBadCRC(t *testing.T) {
	golden := readGolden(t, "snapshot.golden")
	buf := append([]byte(nil), golden...)
	buf[objectHeaderCRCOffset] ^= 0xFF
	var s Snapshot
	if _, err := s.Decode(buf); err != ErrCRC {
		t.Fatalf("decode bad crc: got %v, want %v", err, ErrCRC)
	}
}

func TestSnapshotDecodeRejectsShort(t *testing.T) {
	golden := readGolden(t, "snapshot.golden")
	var s Snapshot
	short := CommonHeaderLen + ObjectHeaderLen + SnapshotFixedLen - 1
	if _, err := s.Decode(golden[:short]); err != ErrShort {
		t.Fatalf("decode short buffer: got %v, want %v", err, ErrShort)
	}
	full := testSnapshot()
	if _, err := full.Encode(make([]byte, full.EncodedLen()-1)); err != ErrShort {
		t.Fatalf("encode short buffer: got %v, want %v", err, ErrShort)
	}
}
