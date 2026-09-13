package image

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// ListEntry is one line of a snapshot listing: an entry's type, size,
// mode and path from the tree root.
type ListEntry struct {
	Type string
	Size uint64
	Mode uint32
	Path string
}

var entryTypeNames = map[uint8]string{
	format.EntryTypeRegular:   "regular",
	format.EntryTypeDirectory: "directory",
	format.EntryTypeSymlink:   "symlink",
	format.EntryTypeCharDev:   "chardev",
	format.EntryTypeBlockDev:  "blockdev",
	format.EntryTypeFIFO:      "fifo",
	format.EntryTypeSocket:    "socket",
}

// ListSnapshot walks the tree of the snapshot content id snapID from a
// disc tree rooted at base (as FindNoahsark resolves it), in the same
// pre-order walk the fill order inside a run defines, and returns one
// ListEntry per tree entry.
func ListSnapshot(root string, snapID object.ID) ([]ListEntry, error) {
	cache := NewNameCache()
	base, err := FindNoahsark(root, cache)
	if err != nil {
		return nil, err
	}
	snapshotsDir := cache.Join(base, "snapshots")
	data, err := os.ReadFile(filepath.Join(snapshotsDir, snapID.TextForm()))
	if err != nil {
		return nil, err
	}
	var snap format.Snapshot
	if _, err := snap.Decode(data); err != nil {
		return nil, err
	}
	objectsDir := cache.Join(base, "objects")
	var out []ListEntry
	if err := walkListTree(objectsDir, object.ID(snap.RootTree), "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func walkListTree(objectsDir string, treeID object.ID, prefix string, out *[]ListEntry) error {
	data, err := os.ReadFile(filepath.Join(objectsDir, treeID.FanoutByte(), treeID.TextForm()))
	if err != nil {
		return fmt.Errorf("image: tree %s: %w", treeID.TextForm(), err)
	}
	var tree format.Tree
	if _, err := tree.Decode(data); err != nil {
		return fmt.Errorf("image: tree %s: %w", treeID.TextForm(), err)
	}
	for _, e := range tree.Entries {
		name := string(e.Name)
		if prefix == "" {
			name = unescapeRootName(name)
		}
		path := filepath.ToSlash(filepath.Join(prefix, name))
		typeName := entryTypeNames[e.EntryType]
		if typeName == "" {
			typeName = fmt.Sprintf("type%d", e.EntryType)
		}
		*out = append(*out, ListEntry{Type: typeName, Size: e.Size, Mode: e.Mode, Path: path})
		if e.EntryType == format.EntryTypeDirectory {
			if err := walkListTree(objectsDir, object.ID(e.ContentID), path, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// unescapeRootName reverses the root entry name escape internal/object
// applies: %2F back to '/', %5C back to '\', %00 back to NUL, %25 back
// to '%'.
func unescapeRootName(name string) string {
	var b []byte
	for i := 0; i < len(name); i++ {
		if name[i] == '%' && i+2 < len(name) {
			switch name[i : i+3] {
			case "%2F":
				b = append(b, '/')
				i += 2
				continue
			case "%5C":
				b = append(b, '\\')
				i += 2
				continue
			case "%00":
				b = append(b, 0)
				i += 2
				continue
			case "%25":
				b = append(b, '%')
				i += 2
				continue
			}
		}
		b = append(b, name[i])
	}
	return string(b)
}

// SortedPaths returns entries' Path fields, sorted, for a
// formatting-independent comparison against another listing.
func SortedPaths(entries []ListEntry) []string {
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	sort.Strings(paths)
	return paths
}
