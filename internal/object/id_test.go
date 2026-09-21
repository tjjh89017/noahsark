package object

import (
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
)

// TestComputeIDCoversTheKindByte checks the content id rule against
// hand-computed vectors: the id is the hash of the kind byte and the
// payload. An empty file's blob payload and an empty directory's tree
// payload are the same eight zero bytes, so only the kind byte keeps
// the two ids apart.
func TestComputeIDCoversTheKindByte(t *testing.T) {
	emptyPayload := make([]byte, 8)
	cases := []struct {
		name string
		kind format.ObjectKind
		want string
	}{
		{"chunk of eight zero bytes", format.ObjectKindChunk, "a536aa3cede6ea3c1f3e0357c3c60e0f216a8c89b853df13b29daa8f85065dfb"},
		{"blob of an empty file", format.ObjectKindBlob, "4322fd2bc0a137d1375b37b3b2e2b4715b3d3dd7ca9682438d4fea0f8437fad3"},
		{"tree of an empty directory", format.ObjectKindTree, "dc4c8669df128318c5790c414c870cc76c585268552851e78d3ee8604dbec0e3"},
		{"snapshot", format.ObjectKindSnapshot, "93e60f669b99ad3e3ee6284b139e57adfb419960f390858e46ea565bbf82d001"},
	}
	seen := map[string]string{}
	for _, c := range cases {
		got := hexString(ComputeID(c.kind, emptyPayload))
		if got != c.want {
			t.Errorf("%s: id = %s, want %s", c.name, got, c.want)
		}
		if other, ok := seen[got]; ok {
			t.Errorf("%s: id equals the id of %s", c.name, other)
		}
		seen[got] = c.name
	}
}

// TestComputeIDEmptyChunk checks one id against a hand-computed value:
// the hash of the single chunk kind byte and no payload byte.
func TestComputeIDEmptyChunk(t *testing.T) {
	want := "4bf5122f344554c53bde2ebb8cd2b7e3d1600ad631c385a5d7cce23c7785459a"
	if got := hexString(ComputeID(format.ObjectKindChunk, nil)); got != want {
		t.Fatalf("id of an empty chunk = %s, want %s", got, want)
	}
}

func TestTextFormIsMultihash(t *testing.T) {
	id := ComputeID(format.ObjectKindChunk, nil)
	// 0x12 (sha2-256) and 0x20 (32) each fit one varint byte, so the
	// multihash prefix is the fixed string "1220".
	want := "1220" + hexString(id)
	got := id.TextForm()
	if got != want {
		t.Fatalf("TextForm() = %s, want %s", got, want)
	}
	if len(got) != 68 {
		t.Fatalf("TextForm() length = %d, want 68", len(got))
	}
}

func TestFanoutByte(t *testing.T) {
	var id ID
	id[0] = 0xab
	if got := id.FanoutByte(); got != "ab" {
		t.Fatalf("FanoutByte() = %s, want ab", got)
	}
}

func hexString(id ID) string {
	const hexdigits = "0123456789abcdef"
	buf := make([]byte, 0, len(id)*2)
	for _, b := range id {
		buf = append(buf, hexdigits[b>>4], hexdigits[b&0x0f])
	}
	return string(buf)
}
