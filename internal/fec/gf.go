// Package fec implements the rs255-gf8 forward error correction code:
// GF(2^8) arithmetic, the Cauchy code, the stream-to-stripe mapping, and
// the checksum column.
package fec

// polynomial is the GF(2^8) reduction polynomial x^8 + x^4 + x^3 + x^2 + 1.
const polynomial = 0x11D

// generator is the field element that generates every nonzero element by
// repeated multiplication.
const generator = 0x02

var expTable [255]byte
var logTable [256]byte

func init() {
	x := byte(1)
	for i := range 255 {
		expTable[i] = x
		logTable[x] = byte(i)
		x = mulBits(x, generator)
	}
}

// mulBits multiplies two field elements by carry-less polynomial
// multiplication reduced modulo the field polynomial. It builds the log
// and exp tables; Mul uses the tables instead of this loop.
func mulBits(a, b byte) byte {
	var r uint16
	aa := uint16(a)
	bb := b
	for bb != 0 {
		if bb&1 != 0 {
			r ^= aa
		}
		aa <<= 1
		if aa&0x100 != 0 {
			aa ^= polynomial
		}
		bb >>= 1
	}
	return byte(r)
}

// Mul multiplies two GF(2^8) elements.
func Mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	sum := int(logTable[a]) + int(logTable[b])
	if sum >= 255 {
		sum -= 255
	}
	return expTable[sum]
}

// Inv returns the multiplicative inverse of a. a must be nonzero.
func Inv(a byte) (byte, error) {
	if a == 0 {
		return 0, ErrSingular
	}
	diff := 255 - int(logTable[a])
	if diff == 255 {
		diff = 0
	}
	return expTable[diff], nil
}

// Div divides a by b. b must be nonzero.
func Div(a, b byte) (byte, error) {
	inv, err := Inv(b)
	if err != nil {
		return 0, err
	}
	return Mul(a, inv), nil
}
