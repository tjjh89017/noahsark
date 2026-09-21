package object

import (
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/format"
)

// ReadVerified reads an object file at path, checks its header_crc32c,
// decompresses its stored bytes, and checks the payload's content id
// against id. It returns the whole raw file bytes, for a caller that goes
// on to run a kind-specific Decode over the fixed body, and the
// decompressed payload.
//
// This is the one place both restore and verify check an object file
// before they trust its kind, its lengths, its compression, or its
// bytes. Neither runs its own, separately maintained version of this
// check. An object file is at most the maximum chunk size, the same
// bound restore already reads a chunk under, so reading it whole here
// stays within that bound; it never scales with the total data a disc
// holds.
func ReadVerified(path string, id ID) (raw, payload []byte, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", id.TextForm(), err)
	}
	headerLen := format.CommonHeaderLen + format.ObjectHeaderLen
	if len(data) < headerLen {
		return nil, nil, fmt.Errorf("%s: file too short", id.TextForm())
	}
	_, oh, err := format.DecodeObjectFileHeader(data[:headerLen])
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", id.TextForm(), err)
	}
	end := uint64(headerLen) + oh.StoredLen
	if uint64(len(data)) < end {
		return nil, nil, fmt.Errorf("%s: file too short", id.TextForm())
	}
	stored := data[uint64(headerLen):end]
	payload, err = Decompress(stored, oh.Compression, oh.PayloadLen)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", id.TextForm(), err)
	}
	if ComputeID(oh.Kind, payload) != id {
		return nil, nil, fmt.Errorf("%s: content id does not verify", id.TextForm())
	}
	return data, payload, nil
}
