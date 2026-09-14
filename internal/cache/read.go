package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"

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

// cachedRunSeqs returns every run_seq the cache holds a runs/<seq>/
// directory for, ascending.
func (c *Cache) cachedRunSeqs() ([]uint64, error) {
	entries, err := os.ReadDir(filepath.Join(c.dir, runsDirName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seqs := make([]uint64, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		seq, err := strconv.ParseUint(e.Name(), 10, 64)
		if err != nil {
			continue
		}
		seqs = append(seqs, seq)
	}
	slices.Sort(seqs)
	return seqs, nil
}

// newestRunSeq returns the highest run_seq the cache holds a copy for.
func (c *Cache) newestRunSeq() (uint64, error) {
	seqs, err := c.cachedRunSeqs()
	if err != nil {
		return 0, err
	}
	if len(seqs) == 0 {
		return 0, fmt.Errorf("cache: no run is cached yet; run pack, or rebuild-cache --from-disc, first")
	}
	return seqs[len(seqs)-1], nil
}

// IndexForRun reads and decodes the cached INDEX.bin of run seq.
func (c *Cache) IndexForRun(seq uint64) (*format.Index, error) {
	buf, err := os.ReadFile(filepath.Join(c.runDir(seq), IndexFileName))
	if err != nil {
		return nil, fmt.Errorf("cache: run %d: %w", seq, err)
	}
	var idx format.Index
	if _, err := idx.Decode(buf); err != nil {
		return nil, fmt.Errorf("cache: run %d: INDEX.bin: %w", seq, err)
	}
	return &idx, nil
}

// Refs reads and decodes REFS.bin from the newest run the cache holds.
// REFS is replicated in full on every run, so the newest cached copy
// names every ref the cache knows.
func (c *Cache) Refs() (*format.RefsTable, error) {
	seq, err := c.newestRunSeq()
	if err != nil {
		return nil, err
	}
	buf, err := os.ReadFile(filepath.Join(c.runDir(seq), RefsFileName))
	if err != nil {
		return nil, fmt.Errorf("cache: run %d: %w", seq, err)
	}
	var refs format.RefsTable
	if _, err := refs.Decode(buf); err != nil {
		return nil, fmt.Errorf("cache: run %d: REFS.bin: %w", seq, err)
	}
	return &refs, nil
}

// Discs reads and decodes DISCS.bin from the newest run the cache
// holds, the same way Refs resolves REFS.
func (c *Cache) Discs() (*format.DiscsTable, error) {
	seq, err := c.newestRunSeq()
	if err != nil {
		return nil, err
	}
	buf, err := os.ReadFile(filepath.Join(c.runDir(seq), DiscsFileName))
	if err != nil {
		return nil, fmt.Errorf("cache: run %d: %w", seq, err)
	}
	var discs format.DiscsTable
	if _, err := discs.Decode(buf); err != nil {
		return nil, fmt.Errorf("cache: run %d: DISCS.bin: %w", seq, err)
	}
	return &discs, nil
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
