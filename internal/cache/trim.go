package cache

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// NewestSnapshotsByTime returns up to n of the cache's snapshot ids,
// ordered newest first by each snapshot's own TimeSec and TimeNsec,
// breaking a tie by text form for a stable order. Two commits inside the
// same wall-clock second differ only in TimeNsec, so both fields must
// order the list or the tie-break by hash can rank the older snapshot
// first. n <= 0 returns every cached snapshot, in the same newest-first
// order. A snapshot object the cache cannot read is skipped rather than
// failing the whole call.
func (c *Cache) NewestSnapshotsByTime(n int) ([]object.ID, error) {
	ids, err := c.ListSnapshots()
	if err != nil {
		return nil, err
	}

	type dated struct {
		id       object.ID
		timeSec  int64
		timeNsec uint32
	}
	all := make([]dated, 0, len(ids))
	for _, id := range ids {
		snap, err := c.ReadSnapshot(id)
		if err != nil {
			continue
		}
		all = append(all, dated{id: id, timeSec: snap.TimeSec, timeNsec: snap.TimeNsec})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].timeSec != all[j].timeSec {
			return all[i].timeSec > all[j].timeSec
		}
		if all[i].timeNsec != all[j].timeNsec {
			return all[i].timeNsec > all[j].timeNsec
		}
		return all[i].id.TextForm() < all[j].id.TextForm()
	})
	if n > 0 && n < len(all) {
		all = all[:n]
	}
	out := make([]object.ID, len(all))
	for i, d := range all {
		out[i] = d.id
	}
	return out, nil
}

// TrimToSnapshots deletes every cached tree and blob that is not
// reachable from one of keep's snapshots, and reports how many objects
// and bytes it removed, or would remove for dryRun. It never touches
// runs/<seq>/, the snapshot objects themselves, or state.txt directly;
// after a real (non-dry-run) trim it recomputes every cached snapshot's
// completeness, so a dropped snapshot's tree set is correctly reported
// incomplete again.
func (c *Cache) TrimToSnapshots(keep []object.ID, dryRun bool) (deleted int, bytesFreed uint64, err error) {
	keepTrees := make(map[object.ID]bool)
	keepBlobs := make(map[object.ID]bool)
	for _, id := range keep {
		c.collectReachable(id, keepTrees, keepBlobs)
	}

	n, b, err := trimDir(c.treesDir(), keepTrees, dryRun)
	if err != nil {
		return deleted, bytesFreed, err
	}
	deleted += n
	bytesFreed += b

	n, b, err = trimDir(c.blobsDir(), keepBlobs, dryRun)
	if err != nil {
		return deleted, bytesFreed, err
	}
	deleted += n
	bytesFreed += b

	if dryRun {
		return deleted, bytesFreed, nil
	}
	all, err := c.ListSnapshots()
	if err != nil {
		return deleted, bytesFreed, err
	}
	for _, id := range all {
		if err := c.refreshComplete(id); err != nil {
			return deleted, bytesFreed, err
		}
	}
	return deleted, bytesFreed, nil
}

// collectReachable walks every tree reachable from snapID's root tree,
// using only what the cache already holds, and records every tree id
// and every regular file's blob id it finds into trees and blobs. A
// snapshot or a tree the cache cannot read simply stops that branch of
// the walk, the same way walkTrees does for CheckComplete.
func (c *Cache) collectReachable(snapID object.ID, trees, blobs map[object.ID]bool) {
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		return
	}
	var walk func(object.ID)
	walk = func(treeID object.ID) {
		if trees[treeID] {
			return
		}
		trees[treeID] = true
		tree, err := c.ReadTree(treeID)
		if err != nil {
			return
		}
		for _, e := range tree.Entries {
			switch e.EntryType {
			case format.EntryTypeDirectory:
				walk(object.ID(e.ContentID))
			case format.EntryTypeRegular:
				blobs[object.ID(e.ContentID)] = true
			}
		}
	}
	walk(object.ID(snap.RootTree))
}

// trimDir deletes every regular file directly under dir whose parsed
// object id is not in keep, and reports how many files and bytes it
// removed, or would remove for dryRun. A missing dir is not an error;
// it has nothing to trim.
func trimDir(dir string, keep map[object.ID]bool, dryRun bool) (int, uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	var n int
	var bytesFreed uint64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, err := object.ParseID(e.Name())
		if err != nil {
			continue
		}
		if keep[id] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !dryRun {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				continue
			}
		}
		n++
		bytesFreed += uint64(info.Size())
	}
	return n, bytesFreed, nil
}
