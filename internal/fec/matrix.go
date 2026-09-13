package fec

// BuildCauchyMatrix returns the m x k Cauchy matrix C of the rs255-gf8
// code: C[j][i] = inv(x_j ^ y_i), with x_j = k+j for j = 0..m-1 and
// y_i = i for i = 0..k-1.
func BuildCauchyMatrix(k, m int) ([][]byte, error) {
	if k <= 0 || m <= 0 || k+m > 255 {
		return nil, ErrShardCount
	}
	c := make([][]byte, m)
	for j := 0; j < m; j++ {
		row := make([]byte, k)
		xj := byte(k + j)
		for i := 0; i < k; i++ {
			yi := byte(i)
			v, err := Inv(xj ^ yi)
			if err != nil {
				return nil, err
			}
			row[i] = v
		}
		c[j] = row
	}
	return c, nil
}

// InvertMatrix inverts an n x n matrix over GF(2^8) by Gauss-Jordan
// elimination with row pivoting. It returns ErrSingular when no pivot is
// found in some column.
func InvertMatrix(m [][]byte) ([][]byte, error) {
	n := len(m)
	aug := make([][]byte, n)
	for i := range aug {
		row := make([]byte, 2*n)
		copy(row, m[i])
		row[n+i] = 1
		aug[i] = row
	}

	for col := 0; col < n; col++ {
		pivot := -1
		for r := col; r < n; r++ {
			if aug[r][col] != 0 {
				pivot = r
				break
			}
		}
		if pivot == -1 {
			return nil, ErrSingular
		}
		aug[col], aug[pivot] = aug[pivot], aug[col]

		inv, err := Inv(aug[col][col])
		if err != nil {
			return nil, err
		}
		row := aug[col]
		for c := 0; c < 2*n; c++ {
			row[c] = Mul(row[c], inv)
		}

		for r := 0; r < n; r++ {
			if r == col {
				continue
			}
			factor := aug[r][col]
			if factor == 0 {
				continue
			}
			other := aug[r]
			for c := 0; c < 2*n; c++ {
				other[c] ^= Mul(factor, row[c])
			}
		}
	}

	inv := make([][]byte, n)
	for i := range inv {
		inv[i] = append([]byte(nil), aug[i][n:2*n]...)
	}
	return inv, nil
}
