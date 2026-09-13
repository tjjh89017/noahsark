package fec

import "testing"

func TestInvKnownValues(t *testing.T) {
	cases := map[byte]byte{
		1: 0x01,
		2: 0x8E,
		3: 0xF4,
		4: 0x47,
		5: 0xA7,
		6: 0x7A,
	}
	for a, want := range cases {
		got, err := Inv(a)
		if err != nil {
			t.Fatalf("Inv(%#x): %v", a, err)
		}
		if got != want {
			t.Errorf("Inv(%#x) = %#x, want %#x", a, got, want)
		}
	}
}

func TestMulInvIdentity(t *testing.T) {
	for a := 1; a < 256; a++ {
		inv, err := Inv(byte(a))
		if err != nil {
			t.Fatalf("Inv(%#x): %v", a, err)
		}
		if got := Mul(byte(a), inv); got != 1 {
			t.Errorf("Mul(%#x, Inv(%#x)) = %#x, want 1", a, a, got)
		}
	}
}

func TestMulZero(t *testing.T) {
	if Mul(0, 5) != 0 || Mul(5, 0) != 0 {
		t.Error("Mul with a zero operand must be zero")
	}
}

func TestInvZeroErrors(t *testing.T) {
	if _, err := Inv(0); err == nil {
		t.Error("Inv(0) must return an error")
	}
}

func TestDivMatchesMulInv(t *testing.T) {
	for a := range 256 {
		for b := 1; b < 256; b++ {
			inv, _ := Inv(byte(b))
			want := Mul(byte(a), inv)
			got, err := Div(byte(a), byte(b))
			if err != nil {
				t.Fatalf("Div(%#x, %#x): %v", a, b, err)
			}
			if got != want {
				t.Errorf("Div(%#x, %#x) = %#x, want %#x", a, b, got, want)
			}
		}
	}
}

// TestMulTableMatchesBitLoop checks the log/exp table lookup against the
// carry-less multiplication loop FORMAT.md states directly.
func TestMulTableMatchesBitLoop(t *testing.T) {
	for a := range 256 {
		for b := range 256 {
			want := mulBits(byte(a), byte(b))
			got := Mul(byte(a), byte(b))
			if got != want {
				t.Fatalf("Mul(%#x, %#x) = %#x, want %#x", a, b, got, want)
			}
		}
	}
}

// TestWorkedExampleMultiplications checks the two products the
// k=3, m=2 worked example computes by hand.
func TestWorkedExampleMultiplications(t *testing.T) {
	if got := Mul(0xF4, 0x53); got != 0x31 {
		t.Errorf("mul(0xF4,0x53) = %#x, want 0x31", got)
	}
	if got := Mul(0x8E, 0xA7); got != 0xDD {
		t.Errorf("mul(0x8E,0xA7) = %#x, want 0xDD", got)
	}
}
