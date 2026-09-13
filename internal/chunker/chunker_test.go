package chunker

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
)

// testProfile is a small profile so tests run fast. It keeps the frozen
// relation max = 4*avg, min = avg/4. avg = 64 = 2^6, so b = 6: mask_s uses
// b+2 = 8 bits, mask_l uses b-2 = 4 bits.
var testProfile = Profile{
	Min:   16,
	Avg:   64,
	Max:   256,
	MaskS: spreadMaskForTest(8),
	MaskL: spreadMaskForTest(4),
}

// spreadMaskForTest mirrors the mask rule for a small bit count, used only
// to build a plausible test profile. It is not a copy of the production
// mask derivation.
func spreadMaskForTest(n int) uint64 {
	var mask uint64
	for j := 0; j < n; j++ {
		pos := 63 - (j * 32 / n)
		mask |= 1 << uint(pos)
	}
	return mask
}

func TestDefaultProfileValues(t *testing.T) {
	// Frozen P4 values from the chunker profile registry.
	if DefaultProfile.Min != 1<<20 {
		t.Errorf("Min = %d, want %d", DefaultProfile.Min, 1<<20)
	}
	if DefaultProfile.Avg != 4<<20 {
		t.Errorf("Avg = %d, want %d", DefaultProfile.Avg, 4<<20)
	}
	if DefaultProfile.Max != 16<<20 {
		t.Errorf("Max = %d, want %d", DefaultProfile.Max, 16<<20)
	}
	if DefaultProfile.MaskS != 0xEEEEEEEE00000000 {
		t.Errorf("MaskS = 0x%016x, want 0xEEEEEEEE00000000", DefaultProfile.MaskS)
	}
	if DefaultProfile.MaskL != 0xDADADADA00000000 {
		t.Errorf("MaskL = 0x%016x, want 0xDADADADA00000000", DefaultProfile.MaskL)
	}
}

func TestEmptyInput(t *testing.T) {
	c := New(bytes.NewReader(nil), testProfile)
	_, err := c.Next()
	if err != io.EOF {
		t.Fatalf("Next() error = %v, want io.EOF", err)
	}
}

func TestShorterThanMin(t *testing.T) {
	data := make([]byte, testProfile.Min-1)
	rand.New(rand.NewSource(1)).Read(data)

	c := New(bytes.NewReader(data), testProfile)
	chunk, err := c.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if !bytes.Equal(chunk, data) {
		t.Fatalf("chunk of length %d != input of length %d", len(chunk), len(data))
	}

	if _, err := c.Next(); err != io.EOF {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}
}

// TestExactlyMax checks an input of exactly Max bytes: no chunk exceeds
// Max, and the chunks concatenate back to the input. A cut can still land
// before the last byte, so this does not require a single Max-sized chunk.
func TestExactlyMax(t *testing.T) {
	data := make([]byte, testProfile.Max)
	rand.New(rand.NewSource(2)).Read(data)

	chunks := chunkAll(t, bytes.NewReader(data), testProfile)

	var total []byte
	for i, chunk := range chunks {
		if len(chunk) > testProfile.Max {
			t.Errorf("chunk %d length %d > Max %d", i, len(chunk), testProfile.Max)
		}
		total = append(total, chunk...)
	}
	if !bytes.Equal(total, data) {
		t.Fatal("concatenated chunks != input")
	}
}

func chunkAll(t *testing.T, r io.Reader, p Profile) [][]byte {
	t.Helper()
	c := New(r, p)
	var chunks [][]byte
	for {
		chunk, err := c.Next()
		if err == io.EOF {
			return chunks
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		chunks = append(chunks, chunk)
	}
}

// TestAllZeroInputCutsAtMax checks the zero-region rule: an all-zero
// input of several times Max cuts into chunks of exactly Max bytes. The
// chunker code has no special case for this; the property comes from the
// gear table and mask alone, so it holds for the production profile too.
func TestAllZeroInputCutsAtMax(t *testing.T) {
	for _, p := range []Profile{testProfile, DefaultProfile} {
		data := make([]byte, 5*p.Max)
		chunks := chunkAll(t, bytes.NewReader(data), p)
		if len(chunks) != 5 {
			t.Fatalf("profile max=%d: got %d chunks, want 5", p.Max, len(chunks))
		}
		for i, chunk := range chunks {
			if len(chunk) != p.Max {
				t.Errorf("profile max=%d: chunk %d length %d, want %d", p.Max, i, len(chunk), p.Max)
			}
		}
	}
}

func TestLargeRandomInput(t *testing.T) {
	data := make([]byte, 200*testProfile.Max)
	rand.New(rand.NewSource(42)).Read(data)

	chunks := chunkAll(t, bytes.NewReader(data), testProfile)
	if len(chunks) == 0 {
		t.Fatal("no chunks produced")
	}

	var total []byte
	for i, chunk := range chunks {
		last := i == len(chunks)-1
		if len(chunk) < testProfile.Min && !(last && len(data) < testProfile.Min) {
			// A chunk shorter than Min is only valid as the final
			// remainder of the stream.
			if !last {
				t.Errorf("chunk %d length %d < Min %d and not last", i, len(chunk), testProfile.Min)
			}
		}
		if len(chunk) > testProfile.Max {
			t.Errorf("chunk %d length %d > Max %d", i, len(chunk), testProfile.Max)
		}
		total = append(total, chunk...)
	}

	if !bytes.Equal(total, data) {
		t.Fatal("concatenated chunks != input")
	}
}

// sizedReader returns Read calls capped at a fixed size, to exercise the
// chunker under different read patterns for the same underlying bytes.
type sizedReader struct {
	data []byte
	pos  int
	step int
}

func (r *sizedReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := r.step
	if n > len(p) {
		n = len(p)
	}
	remaining := len(r.data) - r.pos
	if n > remaining {
		n = remaining
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	return n, nil
}

func TestDeterminism(t *testing.T) {
	data := make([]byte, 50*testProfile.Max)
	rand.New(rand.NewSource(7)).Read(data)

	var reference [][]byte
	for _, step := range []int{1, 3, 7, 64, 4096} {
		chunks := chunkAll(t, &sizedReader{data: data, step: step}, testProfile)
		if reference == nil {
			reference = chunks
			continue
		}
		if len(chunks) != len(reference) {
			t.Fatalf("step %d: got %d chunks, want %d", step, len(chunks), len(reference))
		}
		for i := range chunks {
			if !bytes.Equal(chunks[i], reference[i]) {
				t.Fatalf("step %d: chunk %d differs from reference", step, i)
			}
		}
	}
}
