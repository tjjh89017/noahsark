package cache

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

// WriteDisc copies one disc's three catalog files into the cache, byte
// for byte, replacing whatever discs/<disc-uuid>/ already holds.
func (c *Cache) WriteDisc(uuid [16]byte, indexBuf, refsBuf, discsBuf []byte) error {
	dir := c.discDir(uuid)
	if err := atomicWriteFile(filepath.Join(dir, IndexFileName), indexBuf); err != nil {
		return fmt.Errorf("cache: disc %s: INDEX.bin: %w", uuidText(uuid), err)
	}
	if err := atomicWriteFile(filepath.Join(dir, RefsFileName), refsBuf); err != nil {
		return fmt.Errorf("cache: disc %s: REFS.bin: %w", uuidText(uuid), err)
	}
	if err := atomicWriteFile(filepath.Join(dir, DiscsFileName), discsBuf); err != nil {
		return fmt.Errorf("cache: disc %s: DISCS.bin: %w", uuidText(uuid), err)
	}
	return nil
}

// WriteSnapshot copies one snapshot object's whole encoded bytes into
// the cache.
func (c *Cache) WriteSnapshot(id object.ID, raw []byte) error {
	if err := atomicWriteFile(filepath.Join(c.snapshotsDir(), id.TextForm()), raw); err != nil {
		return fmt.Errorf("cache: snapshot %s: %w", id.TextForm(), err)
	}
	return nil
}

// WriteTree copies one tree object's whole encoded bytes into the
// cache.
func (c *Cache) WriteTree(id object.ID, raw []byte) error {
	if err := atomicWriteFile(filepath.Join(c.treesDir(), id.TextForm()), raw); err != nil {
		return fmt.Errorf("cache: tree %s: %w", id.TextForm(), err)
	}
	return nil
}

// WriteBlob copies one blob object's whole encoded bytes into the
// cache.
func (c *Cache) WriteBlob(id object.ID, raw []byte) error {
	if err := atomicWriteFile(filepath.Join(c.blobsDir(), id.TextForm()), raw); err != nil {
		return fmt.Errorf("cache: blob %s: %w", id.TextForm(), err)
	}
	return nil
}

// WriteFromRoot copies one run's catalog, every snapshot object under
// snapshots/, and every object its own INDEX lists as a tree,
// into the cache. root is a freshly packed run tree or a mounted disc
// root; both share one on-disc layout (FORMAT.md "Disc and run model"),
// so the same read and copy path serves pack, right after it builds a
// run, and recover, for every disc it is given. It recomputes and
// persists the completeness of every snapshot it copied, and returns
// the read result so the caller can report what it found.
func WriteFromRoot(c *Cache, root string) (*image.ReadResult, error) {
	rr, err := image.Read(root)
	if err != nil {
		return nil, fmt.Errorf("cache: %s: %w", root, err)
	}

	names := image.NewNameCache()
	base, err := image.FindNoahsark(root, names)
	if err != nil {
		return nil, fmt.Errorf("cache: %s: %w", root, err)
	}
	runDir, err := image.NewestRunDir(names.Join(base, "runs"))
	if err != nil {
		return nil, fmt.Errorf("cache: %s: %w", root, err)
	}
	catalogDir := names.Join(runDir, "catalog")

	indexBuf, err := os.ReadFile(filepath.Join(runDir, names.Resolve(runDir, "INDEX.bin")))
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	refsBuf, err := os.ReadFile(filepath.Join(catalogDir, names.Resolve(catalogDir, "REFS.bin")))
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	discsBuf, err := os.ReadFile(filepath.Join(catalogDir, names.Resolve(catalogDir, "DISCS.bin")))
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	if err := c.WriteDisc(rr.Disc.DiscUUID, indexBuf, refsBuf, discsBuf); err != nil {
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
			return nil, fmt.Errorf("cache: snapshot %s: %w", id.TextForm(), err)
		}
		if err := c.WriteSnapshot(id, raw); err != nil {
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
			return nil, fmt.Errorf("cache: object %s: %w", id.TextForm(), err)
		}
		if row.Kind == format.ObjectKindTree {
			if err := c.WriteTree(id, raw); err != nil {
				return nil, err
			}
		} else {
			if err := c.WriteBlob(id, raw); err != nil {
				return nil, err
			}
		}
	}

	for _, id := range snapIDs {
		if err := c.refreshComplete(id); err != nil {
			return nil, err
		}
	}

	return rr, nil
}
