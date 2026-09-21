package image

import (
	"fmt"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// ErrStagedDamaged reports a staged object file that does not hold what
// its name promises: too short for its own header, undecodable, or
// hashing to another id. Every object kind reports the damage with this
// one text, and every text names the cure, because the operator's
// action is the same in every case: commit the source again, which
// rewrites the staged copy.
type ErrStagedDamaged struct {
	ID   object.ID
	Kind format.ObjectKind
}

func (e *ErrStagedDamaged) Error() string {
	return fmt.Sprintf("staged %s %s is damaged; run commit again to write it once more, then pack",
		kindName(e.Kind), e.ID.TextForm())
}

// stagedDamaged builds the one damaged-staged-object error.
func stagedDamaged(id object.ID, kind format.ObjectKind) error {
	return &ErrStagedDamaged{ID: id, Kind: kind}
}

// verifyObjectID confirms that data, a staged blob, tree or snapshot
// file's whole bytes, really holds the content want names: the hash of
// its payload, the same computation the writer used to choose the file
// name. Such an object is always stored uncompressed, so its payload is
// the bytes right after the common and object headers.
//
// pack must decode these objects anyway to walk the graph, so the check
// costs no extra read. It is the last chance to catch a staging file
// that does not hold what its name promises, for example truncated by a
// crash between commit writing it and pack reading it. Trusting the
// name here would let a corrupt object reach a disc. A chunk carries no
// graph edge and is never read here; the run's own copy pass checks it.
func verifyObjectID(want object.ID, kind format.ObjectKind, data []byte) error {
	if len(data) < format.CommonHeaderLen+format.ObjectHeaderLen {
		return stagedDamaged(want, kind)
	}
	payload := data[format.CommonHeaderLen+format.ObjectHeaderLen:]

	if object.ComputeID(kind, payload) != want {
		return stagedDamaged(want, kind)
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
