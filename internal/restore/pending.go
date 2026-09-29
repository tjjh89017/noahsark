package restore

import (
	"os"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
)

// Scan calls need for each chunk that a restore of sel into outDir must
// still read from a disc. It reads no disc and writes nothing, so a dry
// run and a rerun both use it to plan the discs that are still needed.
//
// A file that the destination already holds needs nothing: without
// overwrite, the restore leaves it as it is. A chunk whose bytes a part
// file already holds, checked by its content id, needs nothing either.
// A file whose blob is not in the catalog, or does not have the size of
// the tree entry, needs nothing, because the restore cannot write it.
// need can get one chunk more than one time.
//
// The walk holds one file at a time: the blob entries of that file and
// one chunk buffer. It keeps no list of chunks.
func Scan(c *catalog.Catalog, sel *plan.Selection, outDir string, overwrite bool, need func(object.ID)) error {
	return sel.Walk(outDir, &pendingScan{c: c, overwrite: overwrite, need: need})
}

// pendingScan is the visitor of one Scan.
type pendingScan struct {
	c         *catalog.Catalog
	overwrite bool
	need      func(object.ID)
}

// Dir joins names below parent. Without overwrite, a name that stands in
// the destination and is not a directory skips what the directory
// holds, as the restore does.
func (s *pendingScan) Dir(parent string, names []string) (string, bool, error) {
	path, err := plan.JoinAll(parent, names)
	if err != nil {
		return "", false, err
	}
	if s.overwrite {
		return path, true, nil
	}
	dir := parent
	for _, name := range names {
		dir, _ = plan.JoinSafe(dir, name)
		if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
			return "", false, nil
		}
	}
	return path, true, nil
}

func (s *pendingScan) DirDone(string, format.TreeEntry) {}

func (s *pendingScan) Other(string, format.TreeEntry) error { return nil }

// File calls need for each chunk of one regular file that the
// destination does not hold.
func (s *pendingScan) File(dest, part string, e format.TreeEntry) error {
	blob, err := s.c.ReadFileBlob(e)
	if err != nil {
		return nil
	}
	entries := placeChunks(blob.Entries)
	if !s.overwrite {
		if _, found := existingFileStatus(dest, e, entries); found {
			return nil
		}
	}
	f, err := os.Open(part)
	if err != nil {
		for _, be := range entries {
			s.need(object.ID(be.ContentID))
		}
		return nil
	}
	defer func() { _ = f.Close() }()
	for _, be := range entries {
		if !chunkAt(f, be) {
			s.need(object.ID(be.ContentID))
		}
	}
	return nil
}
