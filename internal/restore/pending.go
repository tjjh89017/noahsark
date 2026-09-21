package restore

import (
	"fmt"
	"os"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// NeededChunks returns the ids of the chunks a restore of snap into
// outDir must still read from a disc. It reads no disc and writes
// nothing, so a dry run and a rerun both use it to list the discs the
// operator still has to insert.
//
// A file that is already complete at its final name needs nothing. A
// chunk whose bytes a part file already holds, checked by its content
// id, needs nothing either. Every other chunk of every file in scope is
// needed.
//
// The walk holds one file at a time: the blob entries of that file, one
// chunk buffer, and the set of needed ids. It never maps a chunk back
// to the files that hold it.
func NeededChunks(c *cache.Cache, snap *format.Snapshot, outDir string, includes []string, overwrite bool) (map[object.ID]bool, error) {
	s := &pendingScan{c: c, outDir: outDir, overwrite: overwrite, needed: make(map[object.ID]bool)}
	rootTree, err := c.ReadTree(object.ID(snap.RootTree))
	if err != nil {
		return nil, err
	}
	filter, err := newFilterState(includes)
	if err != nil {
		return nil, err
	}
	for _, e := range rootTree.Entries {
		if e.EntryType != format.EntryTypeDirectory {
			continue
		}
		rootPath := rootPathOf(e)
		if rootPath == "" {
			continue
		}
		childFilter, include := stepInto(filter, splitPath(rootPath))
		if !include {
			continue
		}
		dest, err := joinComponents(outDir, splitPath(rootPath))
		if err != nil {
			return nil, err
		}
		if err := s.dir(object.ID(e.ContentID), dest, childFilter); err != nil {
			return nil, err
		}
	}
	return s.needed, nil
}

// pendingScan is one NeededChunks walk.
type pendingScan struct {
	c         *cache.Cache
	outDir    string
	overwrite bool
	needed    map[object.ID]bool
	buf       []byte
}

func (s *pendingScan) dir(treeID object.ID, dest string, filter *filterState) error {
	t, err := s.c.ReadTree(treeID)
	if err != nil {
		return fmt.Errorf("tree %s: %w", treeID.TextForm(), err)
	}
	taken := make(map[string]bool, len(t.Entries))
	for _, e := range t.Entries {
		taken[string(e.Name)] = true
	}
	for _, e := range t.Entries {
		childFilter, include := stepInto(filter, []string{string(e.Name)})
		if !include {
			continue
		}
		name := string(e.Name)
		child, err := joinSafe(dest, name)
		if err != nil {
			return err
		}
		switch e.EntryType {
		case format.EntryTypeDirectory:
			if err := s.dir(object.ID(e.ContentID), child, childFilter); err != nil {
				return err
			}
		case format.EntryTypeRegular:
			part, err := joinSafe(dest, partName(name, taken))
			if err != nil {
				return err
			}
			if err := s.file(child, part, object.ID(e.ContentID), e); err != nil {
				return err
			}
		}
	}
	return nil
}

// file adds every chunk of one regular file that the destination does
// not already hold.
func (s *pendingScan) file(dest, part string, blobID object.ID, e format.TreeEntry) error {
	blob, err := s.c.ReadBlob(blobID)
	if err != nil {
		return fmt.Errorf("blob %s: not held by the cache; disc-swap restore needs every blob cached: %w", blobID.TextForm(), err)
	}
	if !s.overwrite {
		if resumed, found := existingFileStatus(dest, e, placeChunks(blob.Entries)); found && resumed {
			return nil
		}
	}
	f, err := os.Open(part)
	if err != nil {
		for _, be := range blob.Entries {
			s.needed[object.ID(be.ContentID)] = true
		}
		return nil
	}
	defer func() { _ = f.Close() }()
	for _, be := range placeChunks(blob.Entries) {
		if s.chunkInPlace(f, be) {
			continue
		}
		s.needed[object.ID(be.ContentID)] = true
	}
	return nil
}

// chunkInPlace reports whether f already holds be's own bytes at be's
// offset, the same content id check the assembler makes.
func (s *pendingScan) chunkInPlace(f *os.File, be placedChunk) bool {
	if uint64(cap(s.buf)) < be.Length {
		s.buf = make([]byte, be.Length)
	}
	buf := s.buf[:be.Length]
	if _, err := f.ReadAt(buf, int64(be.Offset)); err != nil {
		return false
	}
	return object.ComputeID(buf) == object.ID(be.ContentID)
}

// joinComponents joins every component under parent, refusing any that
// escapes it.
func joinComponents(parent string, components []string) (string, error) {
	dest := parent
	for _, c := range components {
		next, err := joinSafe(dest, c)
		if err != nil {
			return "", err
		}
		dest = next
	}
	return dest, nil
}
