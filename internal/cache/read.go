package cache

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// ListSnapshots returns the id of every snapshot object the cache
// holds, sorted by text form.
func (c *Cache) ListSnapshots() ([]object.ID, error) {
	entries, err := os.ReadDir(c.snapshotsDir())
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
	sort.Slice(ids, func(i, j int) bool { return ids[i].TextForm() < ids[j].TextForm() })
	return ids, nil
}

// cachedDiscs returns the uuid of every disc the cache holds a
// discs/<disc-uuid>/ directory for, in uuid text order. A directory
// name that is not a uuid is not a cached disc; an older cache that
// still holds runs/<seq>/ directories therefore reports no disc, and
// the caller tells the operator to run pack or rebuild-cache.
func (c *Cache) cachedDiscs() ([][16]byte, error) {
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

// newestCachedDisc returns the uuid of the cached disc with the highest
// created_sec. It takes the time from the disc's own row in the disc's
// own cached DISCS table, the only creation time the cache holds. A
// disc whose table names no row for itself counts as created at 0. Two
// equal times keep the lower uuid.
func (c *Cache) newestCachedDisc() ([16]byte, error) {
	uuids, err := c.cachedDiscs()
	if err != nil {
		return [16]byte{}, err
	}
	if len(uuids) == 0 {
		return [16]byte{}, fmt.Errorf("cache: no disc is cached yet; run pack, or rebuild-cache, first")
	}
	newest := uuids[0]
	var newestCreated int64
	have := false
	for _, uuid := range uuids {
		created := int64(0)
		if row, found := c.ownDiscRow(uuid); found {
			created = row.CreatedSec
		}
		if !have || created > newestCreated {
			newest, newestCreated, have = uuid, created, true
		}
	}
	return newest, nil
}

// discsTableOf reads and decodes one cached disc's own DISCS.bin.
func (c *Cache) discsTableOf(uuid [16]byte) (*format.DiscsTable, error) {
	buf, err := os.ReadFile(filepath.Join(c.discDir(uuid), DiscsFileName))
	if err != nil {
		return nil, fmt.Errorf("cache: disc %s: %w", uuidText(uuid), err)
	}
	var discs format.DiscsTable
	if _, err := discs.Decode(buf); err != nil {
		return nil, fmt.Errorf("cache: disc %s: DISCS.bin: %w", uuidText(uuid), err)
	}
	return &discs, nil
}

// ownDiscRow returns uuid's own row from uuid's own cached DISCS table.
func (c *Cache) ownDiscRow(uuid [16]byte) (format.DiscsRow, bool) {
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

// IndexForDisc reads and decodes the cached INDEX.bin of one disc.
func (c *Cache) IndexForDisc(uuid [16]byte) (*format.Index, error) {
	buf, err := os.ReadFile(filepath.Join(c.discDir(uuid), IndexFileName))
	if err != nil {
		return nil, fmt.Errorf("cache: disc %s: %w", uuidText(uuid), err)
	}
	var idx format.Index
	if _, err := idx.Decode(buf); err != nil {
		return nil, fmt.Errorf("cache: disc %s: INDEX.bin: %w", uuidText(uuid), err)
	}
	return &idx, nil
}

// Refs reads and decodes REFS.bin from the newest disc the cache holds.
// REFS is replicated in full on every run, so the newest cached copy
// names every ref the cache knows.
func (c *Cache) Refs() (*format.RefsTable, error) {
	uuid, err := c.newestCachedDisc()
	if err != nil {
		return nil, err
	}
	buf, err := os.ReadFile(filepath.Join(c.discDir(uuid), RefsFileName))
	if err != nil {
		return nil, fmt.Errorf("cache: disc %s: %w", uuidText(uuid), err)
	}
	var refs format.RefsTable
	if _, err := refs.Decode(buf); err != nil {
		return nil, fmt.Errorf("cache: disc %s: REFS.bin: %w", uuidText(uuid), err)
	}
	return &refs, nil
}

// Discs reads and decodes DISCS.bin from the newest disc the cache
// holds, the same way Refs resolves REFS.
func (c *Cache) Discs() (*format.DiscsTable, error) {
	uuid, err := c.newestCachedDisc()
	if err != nil {
		return nil, err
	}
	return c.discsTableOf(uuid)
}

// ReadTree reads and decodes one cached tree object.
func (c *Cache) ReadTree(id object.ID) (*format.Tree, error) {
	buf, err := os.ReadFile(filepath.Join(c.treesDir(), id.TextForm()))
	if err != nil {
		return nil, err
	}
	var t format.Tree
	if _, err := t.Decode(buf); err != nil {
		return nil, fmt.Errorf("cache: tree %s: %w", id.TextForm(), err)
	}
	return &t, nil
}

// ReadBlob reads and decodes one cached blob object. A blob not yet in
// the cache reports the plain os.ErrNotExist-wrapped error, since a
// blob's absence does not by itself mean the cache is incomplete: only
// tree reachability counts toward Complete and CheckComplete.
func (c *Cache) ReadBlob(id object.ID) (*format.Blob, error) {
	buf, err := os.ReadFile(filepath.Join(c.blobsDir(), id.TextForm()))
	if err != nil {
		return nil, err
	}
	var b format.Blob
	if _, err := b.Decode(buf); err != nil {
		return nil, fmt.Errorf("cache: blob %s: %w", id.TextForm(), err)
	}
	return &b, nil
}

// ReadSnapshot reads and decodes one cached snapshot object.
func (c *Cache) ReadSnapshot(id object.ID) (*format.Snapshot, error) {
	buf, err := os.ReadFile(filepath.Join(c.snapshotsDir(), id.TextForm()))
	if err != nil {
		return nil, err
	}
	var s format.Snapshot
	if _, err := s.Decode(buf); err != nil {
		return nil, fmt.Errorf("cache: snapshot %s: %w", id.TextForm(), err)
	}
	return &s, nil
}
