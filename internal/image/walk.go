package image

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// ReachableObject is one object a run must store: its id, its kind, and
// the whole encoded file bytes as staged.
type ReachableObject struct {
	ID    object.ID
	Kind  format.ObjectKind
	Bytes []byte
}

// stagedObjectPath returns the path of id's object file under a staging
// directory. Both a snapshot and a non-snapshot object share the same
// two-level fan-out scheme; the caller picks the right root.
func stagedObjectPath(root string, id object.ID) string {
	return filepath.Join(root, id.FanoutByte(), id.TextForm())
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

	add := func(id object.ID, kind format.ObjectKind, data []byte) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, ReachableObject{ID: id, Kind: kind, Bytes: data})
	}

	objectsRoot := filepath.Join(stagingDir, "objects")
	snapshotsRoot := filepath.Join(stagingDir, "snapshots")

	var walkTree func(id object.ID) error
	walkTree = func(id object.ID) error {
		if seen[id] {
			return nil
		}
		data, err := readObjectFile(stagedObjectPath(objectsRoot, id))
		if err != nil {
			return fmt.Errorf("image: tree %s: %w", id.TextForm(), err)
		}
		var tree format.Tree
		if _, err := tree.Decode(data); err != nil {
			return fmt.Errorf("image: tree %s: %w", id.TextForm(), err)
		}
		add(id, format.ObjectKindTree, data)
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
			return nil, fmt.Errorf("image: snapshot %s: %w", snapID.TextForm(), err)
		}
		var snap format.Snapshot
		if _, err := snap.Decode(data); err != nil {
			return nil, fmt.Errorf("image: snapshot %s: %w", snapID.TextForm(), err)
		}
		add(snapID, format.ObjectKindSnapshot, data)
		if err := walkTree(object.ID(snap.RootTree)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// walkBlob reads the blob object at id and adds it and every chunk it
// lists.
func walkBlob(objectsRoot string, id object.ID, add func(object.ID, format.ObjectKind, []byte)) error {
	data, err := readObjectFile(stagedObjectPath(objectsRoot, id))
	if err != nil {
		return fmt.Errorf("image: blob %s: %w", id.TextForm(), err)
	}
	var blob format.Blob
	if _, err := blob.Decode(data); err != nil {
		return fmt.Errorf("image: blob %s: %w", id.TextForm(), err)
	}
	add(id, format.ObjectKindBlob, data)
	for _, e := range blob.Entries {
		chunkID := object.ID(e.ContentID)
		cdata, err := readObjectFile(stagedObjectPath(objectsRoot, chunkID))
		if err != nil {
			return fmt.Errorf("image: chunk %s: %w", chunkID.TextForm(), err)
		}
		add(chunkID, format.ObjectKindChunk, cdata)
	}
	return nil
}
