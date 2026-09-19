package image

import (
	"fmt"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// verifyObjectID confirms that data, a staged object file's whole bytes,
// really holds the content want names: the hash of its payload, the
// same computation the writer used to choose the file name. A chunk is
// decompressed first, since its content id covers the payload before
// compression; a blob, tree or snapshot is always stored uncompressed,
// so its payload is the bytes right after the common and object
// headers.
//
// pack reads every staged object once to place it in a run. Reading it
// is the last chance to catch a staging file that does not hold what
// its name promises, for example truncated by a crash between commit
// writing it and pack reading it. Trusting the name here would let a
// corrupt object reach a disc.
func verifyObjectID(want object.ID, kind format.ObjectKind, data []byte) error {
	var payload []byte
	if kind == format.ObjectKindChunk {
		var c format.Chunk
		if _, err := c.Decode(data); err != nil {
			return fmt.Errorf("chunk %s: %w", want.TextForm(), err)
		}
		p, err := object.Decompress(c.Payload, c.ObjectHeader.Compression, c.ObjectHeader.PayloadLen)
		if err != nil {
			return fmt.Errorf("chunk %s: %w", want.TextForm(), err)
		}
		payload = p
	} else {
		if len(data) < format.CommonHeaderLen+format.ObjectHeaderLen {
			return fmt.Errorf("%s %s: object file is shorter than its own header", kindName(kind), want.TextForm())
		}
		payload = data[format.CommonHeaderLen+format.ObjectHeaderLen:]
	}

	got := object.ComputeID(payload)
	if got != want {
		return fmt.Errorf("staged %s %s does not match its own content (got %s); the staging copy is corrupt, run commit again to rewrite it before packing", kindName(kind), want.TextForm(), got.TextForm())
	}
	return nil
}

// kindName renders an object kind for an error message.
func kindName(kind format.ObjectKind) string {
	switch kind {
	case format.ObjectKindChunk:
		return "chunk"
	case format.ObjectKindBlob:
		return "blob"
	case format.ObjectKindTree:
		return "tree"
	case format.ObjectKindSnapshot:
		return "snapshot"
	default:
		return "object"
	}
}
