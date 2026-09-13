package fec

import "testing"

// TestStreamLayoutHandComputed checks a small hand-computed example: two
// files of 5000 and 100 bytes, k=2. File 0 needs ceil(5000/2048) = 3
// blocks; file 1 needs ceil(100/2048) = 1 block. stream_blocks = 4,
// L = ceil(4/2) = 2.
func TestStreamLayoutHandComputed(t *testing.T) {
	layout, err := NewStreamLayout([]uint64{5000, 100}, 2)
	if err != nil {
		t.Fatalf("NewStreamLayout: %v", err)
	}
	if got := layout.BlockCount(); got != 4 {
		t.Fatalf("BlockCount() = %d, want 4", got)
	}
	if got := layout.StripeCount(); got != 2 {
		t.Fatalf("StripeCount() = %d, want 2", got)
	}

	cases := []struct {
		block  uint64
		file   int
		offset uint64
		column uint64
		stripe uint64
	}{
		{0, 0, 0, 0, 0},
		{1, 0, 2048, 0, 1},
		{2, 0, 4096, 1, 0},
		{3, 1, 0, 1, 1},
	}
	for _, c := range cases {
		file, offset, err := layout.Locate(c.block)
		if err != nil {
			t.Fatalf("Locate(%d): %v", c.block, err)
		}
		if file != c.file || offset != c.offset {
			t.Errorf("Locate(%d) = (%d, %d), want (%d, %d)", c.block, file, offset, c.file, c.offset)
		}
		if got := layout.Column(c.block); got != c.column {
			t.Errorf("Column(%d) = %d, want %d", c.block, got, c.column)
		}
		if got := layout.Stripe(c.block); got != c.stripe {
			t.Errorf("Stripe(%d) = %d, want %d", c.block, got, c.stripe)
		}
	}
}

func TestStreamLayoutOutOfRange(t *testing.T) {
	layout, err := NewStreamLayout([]uint64{2048}, 1)
	if err != nil {
		t.Fatalf("NewStreamLayout: %v", err)
	}
	if _, _, err := layout.Locate(1); err == nil {
		t.Error("Locate past BlockCount must return an error")
	}
}

func TestStreamLayoutEmpty(t *testing.T) {
	layout, err := NewStreamLayout(nil, 231)
	if err != nil {
		t.Fatalf("NewStreamLayout: %v", err)
	}
	if got := layout.BlockCount(); got != 0 {
		t.Errorf("BlockCount() = %d, want 0", got)
	}
	if got := layout.StripeCount(); got != 0 {
		t.Errorf("StripeCount() = %d, want 0", got)
	}
}
