package catalog

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

// WriteDisc copies one disc's three catalog files into the catalog, byte
// for byte, replacing whatever discs/<disc-uuid>/ already holds.
func (c *Catalog) WriteDisc(uuid [16]byte, indexBuf, refsBuf, discsBuf []byte) error {
	dir := c.discDir(uuid)
	if err := atomicWriteFile(filepath.Join(dir, IndexFileName), indexBuf); err != nil {
		return fmt.Errorf("catalog: disc %s: INDEX.bin: %w", uuidText(uuid), err)
	}
	if err := atomicWriteFile(filepath.Join(dir, RefsFileName), refsBuf); err != nil {
		return fmt.Errorf("catalog: disc %s: REFS.bin: %w", uuidText(uuid), err)
	}
	if err := atomicWriteFile(filepath.Join(dir, DiscsFileName), discsBuf); err != nil {
		return fmt.Errorf("catalog: disc %s: DISCS.bin: %w", uuidText(uuid), err)
	}
	return nil
}

// RemoveDisc removes the tables of one disc, the directory
// discs/<disc-uuid>/. It does nothing when the directory is absent.
func (c *Catalog) RemoveDisc(uuid [16]byte) error {
	if err := os.RemoveAll(c.discDir(uuid)); err != nil {
		return fmt.Errorf("catalog: disc %s: %w", uuidText(uuid), err)
	}
	return nil
}

// WriteObject writes raw, the object file of one snapshot, tree or blob
// object, to its path in the catalog, with an atomic replace. raw must
// give id, else WriteObject writes nothing and returns an error. A file
// that already holds exactly raw stays. A file that holds other bytes is
// replaced, thus a good copy repairs a damaged catalog object. The cost
// is one hash of raw and one read of the file that exists.
func (c *Catalog) WriteObject(kind format.ObjectKind, id object.ID, raw []byte) error {
	path := c.MetaPath(kind, id)
	if path == "" {
		return fmt.Errorf("catalog: object %s: kind %d is not a metadata object", id.TextForm(), kind)
	}
	if err := checkObject(kind, id, raw); err != nil {
		return fmt.Errorf("catalog: %s %s: the copy to write is damaged: %w", kindWord(kind), id.TextForm(), err)
	}
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, raw) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("catalog: object %s: %w", id.TextForm(), err)
	}
	if err := replaceFile(path, raw); err != nil {
		return fmt.Errorf("catalog: object %s: %w", id.TextForm(), err)
	}
	return nil
}

// WriteTablesFromRoot copies the INDEX, REFS and DISCS tables of the
// run at root into the catalog. root is a packed run tree or a mounted
// disc root; both share one on-disc layout (FORMAT.md "Disc and run
// model"). pack calls it: commit already wrote every snapshot, tree and
// blob object of the run into the catalog. It returns the read result.
func WriteTablesFromRoot(c *Catalog, root string) (*image.ReadResult, error) {
	rr, err := image.Read(root)
	if err != nil {
		return nil, fmt.Errorf("catalog: %s: %w", root, err)
	}
	names := image.NewNameCache()
	base, err := image.FindNoahsark(root, names)
	if err != nil {
		return nil, fmt.Errorf("catalog: %s: %w", root, err)
	}
	if err := c.writeTablesOf(base, names, rr.Disc.DiscUUID); err != nil {
		return nil, err
	}
	return rr, nil
}

// WriteFromRoot reads and checks the run at root, then copies it into
// the catalog as WriteFromRead does. The read is a full check of the
// run: a caller that already holds the result of that check calls
// WriteFromRead instead.
func WriteFromRoot(c *Catalog, root string) (*image.ReadResult, error) {
	rr, err := image.Read(root)
	if err != nil {
		return nil, fmt.Errorf("catalog: %s: %w", root, err)
	}
	if _, err := WriteFromRead(c, root, rr); err != nil {
		return nil, err
	}
	return rr, nil
}

// WriteFromRead copies the run at root into the catalog. rr is the
// result of a check of that run. It copies each snapshot, tree and blob
// object that passed the check, then the INDEX, REFS and DISCS tables
// when REFS and DISCS passed the check. It reads again only these object
// files and the three tables, not the chunks. Then it computes again
// the completeness of each snapshot that the INDEX or the REFS table of
// the run names, and returns these snapshot ids. A counted verify and
// recover call it after their check of a disc.
func WriteFromRead(c *Catalog, root string, rr *image.ReadResult) ([]object.ID, error) {
	names := image.NewNameCache()
	base, err := image.FindNoahsark(root, names)
	if err != nil {
		return nil, fmt.Errorf("catalog: %s: %w", root, err)
	}

	snapshots := map[object.ID]bool{}
	for _, row := range rr.Index.Objects {
		id := object.ID(row.ContentID)
		if row.Kind == format.ObjectKindSnapshot {
			snapshots[id] = true
		}
		if !rr.ObjectIntact(id) {
			continue
		}
		var dir string
		switch row.Kind {
		case format.ObjectKindSnapshot:
			dir = names.Join(base, "snapshots")
		case format.ObjectKindTree, format.ObjectKindBlob:
			dir = names.Join(names.Join(base, "objects"), id.FanoutByte())
		default:
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, names.Resolve(dir, id.TextForm())))
		if err != nil {
			return nil, fmt.Errorf("catalog: %s %s: %w", kindWord(row.Kind), id.TextForm(), err)
		}
		if err := c.WriteObject(row.Kind, id, raw); err != nil {
			return nil, err
		}
	}

	if rr.RefsIntact && rr.DiscsIntact {
		if err := c.writeTablesOf(base, names, rr.Disc.DiscUUID); err != nil {
			return nil, err
		}
	}

	for _, rec := range rr.Refs.Records {
		snapshots[object.ID(rec.SnapshotID)] = true
	}
	ids := slices.SortedFunc(maps.Keys(snapshots), func(a, b object.ID) int { return bytes.Compare(a[:], b[:]) })
	for _, id := range ids {
		if err := c.RefreshComplete(id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// writeTablesOf copies the INDEX, REFS and DISCS files of the one run
// under base into the catalog, as the tables of disc discUUID.
func (c *Catalog) writeTablesOf(base string, names *image.NameCache, discUUID [16]byte) error {
	runDir, err := image.RunDir(names.Join(base, "runs"))
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	catalogDir := names.Join(runDir, "catalog")
	var bufs [3][]byte
	for i, p := range []string{
		filepath.Join(runDir, names.Resolve(runDir, IndexFileName)),
		filepath.Join(catalogDir, names.Resolve(catalogDir, RefsFileName)),
		filepath.Join(catalogDir, names.Resolve(catalogDir, DiscsFileName)),
	} {
		if bufs[i], err = os.ReadFile(p); err != nil {
			return fmt.Errorf("catalog: %w", err)
		}
	}
	return c.WriteDisc(discUUID, bufs[0], bufs[1], bufs[2])
}
