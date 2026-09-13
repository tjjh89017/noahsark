package fec

import (
	"math/rand"
	"testing"
)

// TestCauchyMatrixWorkedExample checks the k=3, m=2 example FORMAT.md
// prints by hand.
func TestCauchyMatrixWorkedExample(t *testing.T) {
	c, err := BuildCauchyMatrix(3, 2)
	if err != nil {
		t.Fatalf("BuildCauchyMatrix: %v", err)
	}
	want0 := []byte{0xF4, 0x8E, 0x01}
	want1 := []byte{0x47, 0xA7, 0x7A}
	for i, w := range want0 {
		if c[0][i] != w {
			t.Errorf("C[0][%d] = %#x, want %#x", i, c[0][i], w)
		}
	}
	for i, w := range want1 {
		if c[1][i] != w {
			t.Errorf("C[1][%d] = %#x, want %#x", i, c[1][i], w)
		}
	}
}

func TestCauchyMatrixVersion1Geometry(t *testing.T) {
	c, err := BuildCauchyMatrix(K, M)
	if err != nil {
		t.Fatalf("BuildCauchyMatrix(%d, %d): %v", K, M, err)
	}
	if len(c) != M {
		t.Fatalf("got %d rows, want %d", len(c), M)
	}
	for j, row := range c {
		if len(row) != K {
			t.Fatalf("row %d has %d columns, want %d", j, len(row), K)
		}
		for i, v := range row {
			if v == 0 {
				t.Fatalf("C[%d][%d] is zero: x_j and y_i must never collide", j, i)
			}
		}
	}
}

func mulMatrix(a, b [][]byte) [][]byte {
	n := len(a)
	out := make([][]byte, n)
	for i := range out {
		out[i] = make([]byte, n)
		for j := range n {
			var sum byte
			for k := range n {
				sum ^= Mul(a[i][k], b[k][j])
			}
			out[i][j] = sum
		}
	}
	return out
}

func isIdentity(m [][]byte) bool {
	for i := range m {
		for j := range m[i] {
			want := byte(0)
			if i == j {
				want = 1
			}
			if m[i][j] != want {
				return false
			}
		}
	}
	return true
}

// TestInvertRandomSubsets builds [I_k ; C] for k=231, m=23, draws random
// k-subsets of its 254 rows, and checks every subset inverts to the
// identity when multiplied by itself.
func TestInvertRandomSubsets(t *testing.T) {
	c, err := BuildCauchyMatrix(K, M)
	if err != nil {
		t.Fatalf("BuildCauchyMatrix: %v", err)
	}
	full := make([][]byte, K+M)
	for i := range K {
		row := make([]byte, K)
		row[i] = 1
		full[i] = row
	}
	for j := range M {
		full[K+j] = c[j]
	}

	rng := rand.New(rand.NewSource(1))
	for trial := range 5 {
		perm := rng.Perm(K + M)[:K]
		square := make([][]byte, K)
		for i, idx := range perm {
			square[i] = full[idx]
		}
		inv, err := InvertMatrix(square)
		if err != nil {
			t.Fatalf("trial %d: InvertMatrix: %v", trial, err)
		}
		product := mulMatrix(inv, square)
		if !isIdentity(product) {
			t.Fatalf("trial %d: inv(square) * square is not the identity", trial)
		}
	}
}

func TestInvertMatrixSingular(t *testing.T) {
	square := [][]byte{
		{1, 2},
		{2, 4},
	}
	if _, err := InvertMatrix(square); err == nil {
		t.Error("InvertMatrix of a singular matrix must return an error")
	}
}
