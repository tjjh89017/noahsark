package format

import "testing"

func TestMagicValues(t *testing.T) {
	cases := []struct {
		name string
		got  Magic
		want string
	}{
		{"NOAHSARK", ProjectMagic, "NOAHSARK"},
		{"CHUNK", MagicChunk, "CHUNK\x00\x00\x00"},
		{"BLOB", MagicBlob, "BLOB\x00\x00\x00\x00"},
		{"TREE", MagicTree, "TREE\x00\x00\x00\x00"},
		{"SNAPSHOT", MagicSnapshot, "SNAPSHOT"},
		{"DISC", MagicDisc, "DISC\x00\x00\x00\x00"},
		{"RUN", MagicRun, "RUN\x00\x00\x00\x00\x00"},
		{"INDEX", MagicIndex, "INDEX\x00\x00\x00"},
		{"CHECKSUM", MagicChecksum, "CHECKSUM"},
		{"REFS", MagicRefs, "REFS\x00\x00\x00\x00"},
		{"DISCS", MagicDiscs, "DISCS\x00\x00\x00"},
	}
	for _, c := range cases {
		want := magicFromString(c.want)
		if c.got != want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, want)
		}
		if len(c.want) != 8 {
			t.Errorf("%s: padded name is %d bytes, want 8", c.name, len(c.want))
		}
	}
}
