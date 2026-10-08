package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// ListSnapshots returns the id of every snapshot object the catalog
// holds, sorted by text form.
func (c *Catalog) ListSnapshots() ([]object.ID, error) {
	entries, err := os.ReadDir(filepath.Join(c.dir, snapshotsDirName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]object.ID, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, err := object.ParseID(e.Name())
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b object.ID) int { return strings.Compare(a.TextForm(), b.TextForm()) })
	return ids, nil
}

// catalogDiscs returns the uuid of every disc that has a
// discs/<disc-uuid>/ directory in the catalog, in uuid order. A
// directory name that is not a uuid in the hyphenated lower-case form is
// not a disc of the catalog.
func (c *Catalog) catalogDiscs() ([][16]byte, error) {
	entries, err := os.ReadDir(filepath.Join(c.dir, discsDirName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	uuids := make([][16]byte, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		uuid, ok := parseUUIDText(e.Name())
		if !ok {
			continue
		}
		uuids = append(uuids, uuid)
	}
	slices.SortFunc(uuids, func(a, b [16]byte) int { return bytes.Compare(a[:], b[:]) })
	return uuids, nil
}

// newestDisc returns the uuid of the catalog disc with the highest
// created_sec. It takes the time from the disc's own row in the disc's
// own catalog DISCS table, the only creation time the catalog holds. A
// disc whose table names no row for itself counts as created at 0.
//
// Two packs in one second give the same created_sec. The later pack
// then carries the longer lineage, thus a tie takes the disc whose own
// DISCS table has more rows. A tie on the row count too takes the
// higher uuid, so the answer is always the same one.
func (c *Catalog) newestDisc() ([16]byte, error) {
	uuids, err := c.catalogDiscs()
	if err != nil {
		return [16]byte{}, err
	}
	if len(uuids) == 0 {
		return [16]byte{}, errors.New(noDiscText)
	}
	var newest [16]byte
	var newestCreated int64
	newestRows := -1
	for _, uuid := range uuids {
		created := int64(0)
		rows := 0
		if table, err := c.discsTableOf(uuid); err == nil {
			rows = len(table.Rows)
			for _, row := range table.Rows {
				if row.DiscUUID == uuid {
					created = row.CreatedSec
				}
			}
		}
		if newestRows < 0 || newerDisc(created, rows, uuid, newestCreated, newestRows, newest) {
			newest, newestCreated, newestRows = uuid, created, rows
		}
	}
	return newest, nil
}

// newerDisc compares two catalog discs by created_sec, then by the
// number of rows in the disc's own DISCS table, then by uuid.
func newerDisc(created int64, rows int, uuid [16]byte, bestCreated int64, bestRows int, best [16]byte) bool {
	if created != bestCreated {
		return created > bestCreated
	}
	if rows != bestRows {
		return rows > bestRows
	}
	return bytes.Compare(uuid[:], best[:]) > 0
}

// discsTableOf reads and decodes one catalog disc's own DISCS.bin.
func (c *Catalog) discsTableOf(uuid [16]byte) (*format.DiscsTable, error) {
	buf, err := os.ReadFile(filepath.Join(c.discDir(uuid), DiscsFileName))
	if err != nil {
		return nil, fmt.Errorf("catalog: disc %s: %w", uuidText(uuid), err)
	}
	var discs format.DiscsTable
	if _, err := discs.Decode(buf); err != nil {
		return nil, fmt.Errorf("catalog: disc %s: DISCS.bin: %w", uuidText(uuid), err)
	}
	return &discs, nil
}

// ownDiscRow returns uuid's own row from uuid's own catalog DISCS table.
func (c *Catalog) ownDiscRow(uuid [16]byte) (format.DiscsRow, bool) {
	discs, err := c.discsTableOf(uuid)
	if err != nil {
		return format.DiscsRow{}, false
	}
	for _, row := range discs.Rows {
		if row.DiscUUID == uuid {
			return row, true
		}
	}
	return format.DiscsRow{}, false
}

// IndexForDisc reads and decodes the catalog INDEX.bin of one disc.
func (c *Catalog) IndexForDisc(uuid [16]byte) (*format.Index, error) {
	buf, err := os.ReadFile(filepath.Join(c.discDir(uuid), IndexFileName))
	if err != nil {
		return nil, fmt.Errorf("catalog: disc %s: %w", uuidText(uuid), err)
	}
	var idx format.Index
	if _, err := idx.Decode(buf); err != nil {
		return nil, fmt.Errorf("catalog: disc %s: INDEX.bin: %w", uuidText(uuid), err)
	}
	return &idx, nil
}

// Refs reads and decodes REFS.bin from the newest disc the catalog holds.
// REFS is replicated in full on every run, so the newest catalog copy
// names every ref the catalog knows.
func (c *Catalog) Refs() (*format.RefsTable, error) {
	uuid, err := c.newestDisc()
	if err != nil {
		return nil, err
	}
	buf, err := os.ReadFile(filepath.Join(c.discDir(uuid), RefsFileName))
	if err != nil {
		return nil, fmt.Errorf("catalog: disc %s: %w", uuidText(uuid), err)
	}
	var refs format.RefsTable
	if _, err := refs.Decode(buf); err != nil {
		return nil, fmt.Errorf("catalog: disc %s: REFS.bin: %w", uuidText(uuid), err)
	}
	return &refs, nil
}

// Discs reads and decodes DISCS.bin from the newest disc the catalog
// holds, the same way Refs resolves REFS.
func (c *Catalog) Discs() (*format.DiscsTable, error) {
	uuid, err := c.newestDisc()
	if err != nil {
		return nil, err
	}
	return c.discsTableOf(uuid)
}

// ReadTree reads one catalog tree object, checks it against id, and
// decodes it. A missing object gives an error for which os.IsNotExist
// holds. An object that does not give id is a *DamagedObjectError.
func (c *Catalog) ReadTree(id object.ID) (*format.Tree, error) {
	buf, err := c.readObject(format.ObjectKindTree, id)
	if err != nil {
		return nil, err
	}
	var t format.Tree
	if _, err := t.Decode(buf); err != nil {
		return nil, c.damaged(format.ObjectKindTree, id, err)
	}
	return &t, nil
}

// ReadBlob reads one catalog blob object, checks it against id, and
// decodes it. The errors are those of ReadTree.
func (c *Catalog) ReadBlob(id object.ID) (*format.Blob, error) {
	buf, err := c.readObject(format.ObjectKindBlob, id)
	if err != nil {
		return nil, err
	}
	var b format.Blob
	if _, err := b.Decode(buf); err != nil {
		return nil, c.damaged(format.ObjectKindBlob, id, err)
	}
	return &b, nil
}

// ReadSnapshot reads one catalog snapshot object, checks it against id,
// and decodes it. The errors are those of ReadTree.
func (c *Catalog) ReadSnapshot(id object.ID) (*format.Snapshot, error) {
	buf, err := c.readObject(format.ObjectKindSnapshot, id)
	if err != nil {
		return nil, err
	}
	var s format.Snapshot
	if _, err := s.Decode(buf); err != nil {
		return nil, c.damaged(format.ObjectKindSnapshot, id, err)
	}
	return &s, nil
}

// DiscsHolding returns the uuid of every disc whose INDEX in the catalog
// lists id in its Objects table, in uuid order. A disc whose INDEX
// cannot be read is left out.
func (c *Catalog) DiscsHolding(id object.ID) ([][16]byte, error) {
	uuids, err := c.catalogDiscs()
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	var holders [][16]byte
	for _, uuid := range uuids {
		idx, err := c.IndexForDisc(uuid)
		if err != nil {
			continue
		}
		if slices.ContainsFunc(idx.Objects, func(row format.IndexObjectRecord) bool {
			return object.ID(row.ContentID) == id
		}) {
			holders = append(holders, uuid)
		}
	}
	return holders, nil
}
