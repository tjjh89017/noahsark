package format

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
)

func TestCarverFindsEveryStructureInOrder(t *testing.T) {
	names := []string{
		"chunk.golden", "blob.golden", "tree.golden", "snapshot.golden",
		"disc.golden", "run.golden", "index.golden", "refs.golden", "discs.golden",
	}
	rng := rand.New(rand.NewSource(1))

	junk := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(rng.Intn(256))
		}
		return b
	}

	var stream bytes.Buffer
	stream.Write(junk(37)) // junk in front, with no magic

	var want [][]byte
	for _, name := range names {
		golden := readGolden(t, name)
		want = append(want, golden)
		stream.Write(golden)
		stream.Write(junk(1 + rng.Intn(64)))
	}

	c := NewCarver(bytes.NewReader(stream.Bytes()))
	var got [][]byte
	for {
		carved, err := c.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("carve: %v", err)
		}
		raw := stream.Bytes()[carved.Offset : carved.Offset+int64(carved.Length)]
		got = append(got, append([]byte(nil), raw...))
	}

	if len(got) != len(want) {
		t.Fatalf("found %d structures, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("structure %d (%s) mismatch", i, names[i])
		}
	}
}

func TestCarverSkipsFalseMagic(t *testing.T) {
	golden := readGolden(t, "chunk.golden")

	var stream bytes.Buffer
	stream.WriteString("NOAHSARK") // a bare magic with no valid header after it
	stream.Write(golden)

	c := NewCarver(&stream)
	carved, err := c.Next()
	if err != nil {
		t.Fatalf("carve: %v", err)
	}
	if carved.Offset != 8 {
		t.Fatalf("offset = %d, want 8", carved.Offset)
	}
	if _, ok := carved.Value.(*Chunk); !ok {
		t.Fatalf("value type = %T, want *Chunk", carved.Value)
	}

	if _, err := c.Next(); err != io.EOF {
		t.Fatalf("second carve: got %v, want io.EOF", err)
	}
}

func TestCarverSkipsTruncatedFinalStructure(t *testing.T) {
	golden := readGolden(t, "tree.golden")

	var stream bytes.Buffer
	stream.Write(golden)
	// A truncated structure at the end of the stream: enough for a magic
	// hit and a common header, not enough to decode.
	stream.Write(golden[:CommonHeaderLen+8])

	c := NewCarver(&stream)
	carved, err := c.Next()
	if err != nil {
		t.Fatalf("carve first: %v", err)
	}
	if carved.Offset != 0 {
		t.Fatalf("offset = %d, want 0", carved.Offset)
	}
	if _, ok := carved.Value.(*Tree); !ok {
		t.Fatalf("value type = %T, want *Tree", carved.Value)
	}

	if _, err := c.Next(); err != io.EOF {
		t.Fatalf("second carve: got %v, want io.EOF (truncated tail)", err)
	}
}
