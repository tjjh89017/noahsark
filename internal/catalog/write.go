package catalog

import (
	"fmt"
	"os"
	"path/filepath"

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

// WriteObject writes the encoded bytes of one snapshot, tree or blob
// object to its path in the catalog, with an atomic replace. It does not
// write when the file exists with the same size: the id names the
// content.
func (c *Catalog) WriteObject(kind format.ObjectKind, id object.ID, raw []byte) error {
	path := c.MetaPath(kind, id)
	if path == "" {
		return fmt.Errorf("catalog: object %s: kind %d is not a metadata object", id.TextForm(), kind)
	}
	if info, err := os.Stat(path); err == nil && info.Size() == int64(len(raw)) {
		return nil
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
	rr, _, _, err := writeTables(c, root)
	return rr, err
}

// writeTables does the work of WriteTablesFromRoot. It also returns the
// NOAHSARK directory of root and its name cache.
func writeTables(c *Catalog, root string) (*image.ReadResult, string, *image.NameCache, error) {
	rr, err := image.Read(root)
	if err != nil {
		return nil, "", nil, fmt.Errorf("catalog: %s: %w", root, err)
	}

	names := image.NewNameCache()
	base, err := image.FindNoahsark(root, names)
	if err != nil {
		return nil, "", nil, fmt.Errorf("catalog: %s: %w", root, err)
	}
	runDir, err := image.NewestRunDir(names.Join(base, "runs"))
	if err != nil {
		return nil, "", nil, fmt.Errorf("catalog: %s: %w", root, err)
	}
	catalogDir := names.Join(runDir, "catalog")

	indexBuf, err := os.ReadFile(filepath.Join(runDir, names.Resolve(runDir, "INDEX.bin")))
	if err != nil {
		return nil, "", nil, fmt.Errorf("catalog: %w", err)
	}
	refsBuf, err := os.ReadFile(filepath.Join(catalogDir, names.Resolve(catalogDir, "REFS.bin")))
	if err != nil {
		return nil, "", nil, fmt.Errorf("catalog: %w", err)
	}
	discsBuf, err := os.ReadFile(filepath.Join(catalogDir, names.Resolve(catalogDir, "DISCS.bin")))
	if err != nil {
		return nil, "", nil, fmt.Errorf("catalog: %w", err)
	}
	if err := c.WriteDisc(rr.Disc.DiscUUID, indexBuf, refsBuf, discsBuf); err != nil {
		return nil, "", nil, err
	}
	return rr, base, names, nil
}

// WriteFromRoot copies the tables of the run at root, every snapshot
// object under snapshots/, and every tree and blob object that its own
// INDEX lists, into the catalog. A counted verify and recover call it
// for each disc that they read. It computes again and saves the
// completeness of every snapshot that it copied, and returns the read
// result so the caller can report what it found.
func WriteFromRoot(c *Catalog, root string) (*image.ReadResult, error) {
	rr, base, names, err := writeTables(c, root)
	if err != nil {
		return nil, err
	}

	snapshotsDir := names.Join(base, "snapshots")
	var snapIDs []object.ID
	for _, row := range rr.Index.Objects {
		if row.Kind != format.ObjectKindSnapshot {
			continue
		}
		id := object.ID(row.ContentID)
		raw, err := os.ReadFile(filepath.Join(snapshotsDir, names.Resolve(snapshotsDir, id.TextForm())))
		if err != nil {
			return nil, fmt.Errorf("catalog: snapshot %s: %w", id.TextForm(), err)
		}
		if err := c.WriteObject(format.ObjectKindSnapshot, id, raw); err != nil {
			return nil, err
		}
		snapIDs = append(snapIDs, id)
	}

	objectsDir := names.Join(base, "objects")
	for _, row := range rr.Index.Objects {
		if row.Kind != format.ObjectKindTree && row.Kind != format.ObjectKindBlob {
			continue
		}
		id := object.ID(row.ContentID)
		objDir := names.Join(objectsDir, id.FanoutByte())
		raw, err := os.ReadFile(filepath.Join(objDir, names.Resolve(objDir, id.TextForm())))
		if err != nil {
			return nil, fmt.Errorf("catalog: object %s: %w", id.TextForm(), err)
		}
		if err := c.WriteObject(row.Kind, id, raw); err != nil {
			return nil, err
		}
	}

	for _, id := range snapIDs {
		if err := c.RefreshComplete(id); err != nil {
			return nil, err
		}
	}

	return rr, nil
}
