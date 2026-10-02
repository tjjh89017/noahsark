package format

import "fmt"

// zeroField refuses a nonzero reserved integer field. The error names the
// structure, the field and the value.
func zeroField(structure, field string, value uint64) error {
	if value == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s %s is 0x%x, want 0", ErrReserved, structure, field, value)
}

// zeroBytes refuses a reserved byte range or a padding range that holds
// a nonzero byte. The error names the structure, the field, the offset
// of the first nonzero byte inside the field, and its value.
func zeroBytes(structure, field string, b []byte) error {
	for i, v := range b {
		if v != 0 {
			return fmt.Errorf("%w: %s %s byte %d is 0x%02x, want 0", ErrReserved, structure, field, i, v)
		}
	}
	return nil
}

// zeroBits refuses a flag field with a reserved bit set. known holds the
// bits that the format defines. The error names the structure, the field
// and the value.
func zeroBits(structure, field string, value, known uint64) error {
	if value&^known == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s %s is 0x%x, reserved bits 0x%x are set", ErrReserved, structure, field, value, value&^known)
}

// firstError returns the first error of errs that is not nil.
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
