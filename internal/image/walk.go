package image

import (
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// ReachableObject is one object a run must store: its id, its kind, and
// its staged size. Bytes carries a tree, blob or snapshot object's whole
// encoded file, small metadata bounded by the tree shape rather than by
// data size. Bytes is nil for a chunk object: its payload can be as
// large as the maximum chunk size, so a chunk's bytes stay on staging
// disk and are read only when its own turn comes, never held here.
type ReachableObject struct {
	ID      object.ID
	Kind    format.ObjectKind
	Bytes   []byte
	ByteLen uint64
}

// ObjectPathFunc gives the file path of one object from its kind and
// id. Pack and Build read every object through it.
type ObjectPathFunc func(kind format.ObjectKind, id object.ID) string

// SnapshotIDsFunc lists the id of every snapshot a pack covers.
type SnapshotIDsFunc func() ([]object.ID, error)

// readObjectFile reads and returns the whole bytes of the object file at
// path.
func readObjectFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// CollectReachable walks every snapshot in snapshotIDs and returns the
// full set of objects a run over exactly those snapshots must store: the
// snapshot objects themselves, and every tree, blob and chunk object
// reachable from their root trees. Objects are deduplicated by id. It
// finds each object file through objectPath.
func CollectReachable(objectPath ObjectPathFunc, snapshotIDs []object.ID) ([]ReachableObject, error) {
	seen := make(map[object.ID]bool)
	var out []ReachableObject

	add := func(id object.ID, kind format.ObjectKind, data []byte, byteLen uint64) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, ReachableObject{ID: id, Kind: kind, Bytes: data, ByteLen: byteLen})
	}

	var walkTree func(id object.ID) error
	walkTree = func(id object.ID) error {
		if seen[id] {
			return nil
		}
		data, err := readObjectFile(objectPath(format.ObjectKindTree, id))
		if err != nil {
			return fmt.Errorf("tree %s: %w", id.TextForm(), err)
		}
		var tree format.Tree
		if _, err := tree.Decode(data); err != nil {
			return fmt.Errorf("tree %s: %w", id.TextForm(), err)
		}
		add(id, format.ObjectKindTree, data, uint64(len(data)))
		for _, entry := range tree.Entries {
			switch entry.EntryType {
			case format.EntryTypeDirectory:
				if err := walkTree(object.ID(entry.ContentID)); err != nil {
					return err
				}
			case format.EntryTypeRegular:
				if err := walkBlob(objectPath, object.ID(entry.ContentID), add); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, snapID := range snapshotIDs {
		data, err := readObjectFile(objectPath(format.ObjectKindSnapshot, snapID))
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", snapID.TextForm(), err)
		}
		var snap format.Snapshot
		if _, err := snap.Decode(data); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", snapID.TextForm(), err)
		}
		add(snapID, format.ObjectKindSnapshot, data, uint64(len(data)))
		if err := walkTree(object.ID(snap.RootTree)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// walkBlob reads the blob object at id and adds it and every chunk it
// lists. A chunk's own bytes are never read here: only its staged file
// size, from a stat, so a chunk's payload never enters memory during the
// walk.
func walkBlob(objectPath ObjectPathFunc, id object.ID, add func(object.ID, format.ObjectKind, []byte, uint64)) error {
	data, err := readObjectFile(objectPath(format.ObjectKindBlob, id))
	if err != nil {
		return fmt.Errorf("blob %s: %w", id.TextForm(), err)
	}
	var blob format.Blob
	if _, err := blob.Decode(data); err != nil {
		return fmt.Errorf("blob %s: %w", id.TextForm(), err)
	}
	add(id, format.ObjectKindBlob, data, uint64(len(data)))
	for _, e := range blob.Entries {
		chunkID := object.ID(e.ContentID)
		fi, err := os.Stat(objectPath(format.ObjectKindChunk, chunkID))
		if err != nil {
			return fmt.Errorf("chunk %s: %w", chunkID.TextForm(), err)
		}
		add(chunkID, format.ObjectKindChunk, nil, uint64(fi.Size()))
	}
	return nil
}
