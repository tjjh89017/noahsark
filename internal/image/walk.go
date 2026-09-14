package image

import (
	"fmt"
	"os"
	"path/filepath"

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

// stagedObjectPath returns the path of id's object file under a staging
// directory. Both a snapshot and a non-snapshot object share the same
// two-level fan-out scheme; the caller picks the right root.
func stagedObjectPath(root string, id object.ID) string {
	return filepath.Join(root, id.FanoutByte(), id.TextForm())
}

// StagedPath returns the path of id's staged object file under
// stagingDir, given its kind.
func StagedPath(stagingDir string, id object.ID, kind format.ObjectKind) string {
	if kind == format.ObjectKindSnapshot {
		return filepath.Join(stagingDir, "snapshots", id.TextForm())
	}
	return stagedObjectPath(filepath.Join(stagingDir, "objects"), id)
}

// readObjectFile reads and returns the whole bytes of an object file at
// path, an object.ID computed from the staging directory's own layout.
func readObjectFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// CollectReachable walks every snapshot in snapshotIDs from stagingDir and
// returns the full set of objects a run over exactly those snapshots must
// store: the snapshot objects themselves, and every tree, blob and chunk
// object reachable from their root trees. Objects are deduplicated by id.
func CollectReachable(stagingDir string, snapshotIDs []object.ID) ([]ReachableObject, error) {
	seen := make(map[object.ID]bool)
	var out []ReachableObject

	add := func(id object.ID, kind format.ObjectKind, data []byte, byteLen uint64) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, ReachableObject{ID: id, Kind: kind, Bytes: data, ByteLen: byteLen})
	}

	cache := NewNameCache()
	objectsRoot := cache.Join(stagingDir, "objects")
	snapshotsRoot := cache.Join(stagingDir, "snapshots")

	var walkTree func(id object.ID) error
	walkTree = func(id object.ID) error {
		if seen[id] {
			return nil
		}
		data, err := readObjectFile(stagedObjectPath(objectsRoot, id))
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
				if err := walkBlob(objectsRoot, object.ID(entry.ContentID), add); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, snapID := range snapshotIDs {
		data, err := readObjectFile(filepath.Join(snapshotsRoot, snapID.TextForm()))
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
func walkBlob(objectsRoot string, id object.ID, add func(object.ID, format.ObjectKind, []byte, uint64)) error {
	data, err := readObjectFile(stagedObjectPath(objectsRoot, id))
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
		fi, err := os.Stat(stagedObjectPath(objectsRoot, chunkID))
		if err != nil {
			return fmt.Errorf("chunk %s: %w", chunkID.TextForm(), err)
		}
		add(chunkID, format.ObjectKindChunk, nil, uint64(fi.Size()))
	}
	return nil
}
