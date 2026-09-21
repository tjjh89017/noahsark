package restore

import "github.com/tjjh89017/noahsark/internal/format"

// placedChunk is one blob entry with the file offset it sits at. A blob
// entry stores no offset: the offset of an entry is the sum of the
// lengths of the entries before it, and the entries are in file order.
type placedChunk struct {
	format.BlobEntry
	Offset uint64
}

// placeChunks pairs every entry of a blob with its running offset.
func placeChunks(entries []format.BlobEntry) []placedChunk {
	offsets := format.BlobOffsets(entries)
	out := make([]placedChunk, len(entries))
	for i, e := range entries {
		out[i] = placedChunk{BlobEntry: e, Offset: offsets[i]}
	}
	return out
}
