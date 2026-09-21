package main

import "fmt"

// discNameShort names a disc the way an operator reads it off the
// sleeve: the number and the label. Every command uses this one form.
func discNameShort(seq uint64, label string) string {
	return fmt.Sprintf("disc %d %q", seq, label)
}

// discName is discNameShort with the uuid, the exact name of a disc. A
// message that must tell two discs apart uses this form.
func discName(seq uint64, label string, uuid [16]byte) string {
	return fmt.Sprintf("disc %d %q (%s)", seq, label, uuidText(uuid))
}
