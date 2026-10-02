package image

import "fmt"

// VolumeLabel returns the volume label of the disc with the number
// discSeq: NOAHSARK_, then the number in decimal, zero-padded to 4
// digits. A larger number gets more digits. Both burn methods write
// this label. The tool never reads it.
func VolumeLabel(discSeq uint64) string {
	return fmt.Sprintf("NOAHSARK_%04d", discSeq)
}
