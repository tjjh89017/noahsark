package format

import "testing"

func TestDispatchGoldenFiles(t *testing.T) {
	cases := []struct {
		golden string
		want   any
	}{
		{"chunk.golden", &Chunk{}},
		{"blob.golden", &Blob{}},
		{"tree.golden", &Tree{}},
		{"snapshot.golden", &Snapshot{}},
		{"disc.golden", &Disc{}},
		{"run.golden", &Run{}},
		{"index.golden", &Index{}},
		{"refs.golden", &RefsTable{}},
		{"discs.golden", &DiscsTable{}},
	}

	for _, c := range cases {
		buf := readGolden(t, c.golden)
		got, n, err := Dispatch(buf)
		if err != nil {
			t.Fatalf("%s: dispatch: %v", c.golden, err)
		}
		if n != len(buf) {
			t.Fatalf("%s: dispatch consumed %d bytes, want %d", c.golden, n, len(buf))
		}
		wantType := typeName(c.want)
		gotType := typeName(got)
		if gotType != wantType {
			t.Fatalf("%s: got type %s, want %s", c.golden, gotType, wantType)
		}
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *Chunk:
		return "*Chunk"
	case *Blob:
		return "*Blob"
	case *Tree:
		return "*Tree"
	case *Snapshot:
		return "*Snapshot"
	case *Disc:
		return "*Disc"
	case *Run:
		return "*Run"
	case *Index:
		return "*Index"
	case *RefsTable:
		return "*RefsTable"
	case *DiscsTable:
		return "*DiscsTable"
	default:
		return "unknown"
	}
}

func TestDispatchRejectsUnknownKind(t *testing.T) {
	golden := readGolden(t, "chunk.golden")
	buf := append([]byte(nil), golden...)
	// checksum.golden's magic_kind is not a structure Dispatch handles.
	copy(buf[8:16], MagicChecksum[:])
	if _, _, err := Dispatch(buf); err != ErrBadMagic {
		t.Fatalf("dispatch unknown kind: got %v, want %v", err, ErrBadMagic)
	}
}

func TestDispatchRejectsUnsupportedMajor(t *testing.T) {
	golden := readGolden(t, "chunk.golden")
	buf := append([]byte(nil), golden...)
	buf[16] = 2
	buf[17] = 0
	if _, _, err := Dispatch(buf); err != ErrVersion {
		t.Fatalf("dispatch unsupported major: got %v, want %v", err, ErrVersion)
	}
}
