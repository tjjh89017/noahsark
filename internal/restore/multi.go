package restore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// MissingDiscError reports that restoring a snapshot needs a disc this
// call was not given, and which objects on it are needed.
type MissingDiscError struct {
	// ByDisc maps a needed disc's uuid to the object ids on it a
	// complete restore would need.
	ByDisc map[[16]byte][]object.ID
}

func (e *MissingDiscError) Error() string {
	var s strings.Builder
	s.WriteString("restore: missing disc(s):")
	uuids := make([][16]byte, 0, len(e.ByDisc))
	for u := range e.ByDisc {
		uuids = append(uuids, u)
	}
	sort.Slice(uuids, func(i, j int) bool { return uuidText(uuids[i]) < uuidText(uuids[j]) })
	for _, u := range uuids {
		_, _ = fmt.Fprintf(&s, " disc %s holds %d needed object(s)", uuidText(u), len(e.ByDisc[u]))
	}
	return s.String()
}

func uuidText(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// discSource is one provided disc root, resolved and read once.
type discSource struct {
	base   string
	runDir string
	uuid   [16]byte
}

// multiSource resolves an object id to the disc root that holds it,
// across every disc root RestoreMulti was given, and accumulates the
// objects and discs a restore could not find.
type multiSource struct {
	bases          []discSource
	runSeqToUUID   map[uint64][16]byte
	contentToRun   map[object.ID]uint64
	missingByDisc  map[[16]byte][]object.ID
	missingUnknown []object.ID // needed but no disc could be identified
}

// RestoreMulti reads snapshotID's tree from whichever of discRoots holds
// each object it needs, and writes it under outDir. A disc root is
// either a mounted disc image or an unpacked NOAHSARK tree, the same as
// Restore accepts.
//
// If the restore needs an object from a disc not among discRoots, the
// walk continues into every other branch it can still reach, and
// RestoreMulti returns a *MissingDiscError naming every needed disc and
// every needed object once the walk finishes, instead of stopping at the
// first miss.
func RestoreMulti(discRoots []string, snapshotID object.ID, outDir string) error {
	return RestoreMultiWithProgress(discRoots, snapshotID, outDir, nil)
}

// RestoreMultiWithProgress is RestoreMulti, reporting bytes written
// through prog. A nil prog reports nothing.
func RestoreMultiWithProgress(discRoots []string, snapshotID object.ID, outDir string, prog *progress.Reporter) error {
	if len(discRoots) == 0 {
		return fmt.Errorf("restore: at least one disc root is required")
	}
	src, err := newMultiSource(discRoots)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}

	snapRaw, _, ok := src.read(snapshotID, true)
	if !ok {
		return src.finalError()
	}
	var snap format.Snapshot
	if _, err := snap.Decode(snapRaw); err != nil {
		return fmt.Errorf("restore: snapshot %s: %w", snapshotID.TextForm(), err)
	}

	rootRaw, _, ok := src.read(object.ID(snap.RootTree), false)
	if !ok {
		return src.finalError()
	}
	var rootTree format.Tree
	if _, err := rootTree.Decode(rootRaw); err != nil {
		return fmt.Errorf("restore: tree %s: %w", object.ID(snap.RootTree).TextForm(), err)
	}

	// Unlike the single-disc Restore, this does not pre-sum an expected
	// total: summing would mean an extra read pass through src, and a
	// miss during that pass would double-count itself into the eventual
	// MissingDiscError. Progress here reports bytes written and
	// throughput only, with no percentage or ETA.
	prog.Start("restore: bytes written", 0)
	defer prog.Done()

	for _, e := range rootTree.Entries {
		if err := src.restoreRootEntry(absOut, e, prog); err != nil {
			return err
		}
	}
	return src.finalError()
}

// newMultiSource resolves every disc root and reads its single run's
// INDEX (Objects and Prereqs) and DISCS, to build the id-to-run and
// run-to-disc maps a missing-object lookup needs.
func newMultiSource(discRoots []string) (*multiSource, error) {
	src := &multiSource{
		runSeqToUUID:  make(map[uint64][16]byte),
		contentToRun:  make(map[object.ID]uint64),
		missingByDisc: make(map[[16]byte][]object.ID),
	}
	for _, root := range discRoots {
		base, err := findNoahsark(root)
		if err != nil {
			return nil, err
		}
		discBuf, err := os.ReadFile(filepath.Join(base, "DISC.bin"))
		if err != nil {
			return nil, fmt.Errorf("restore: %s: %w", base, err)
		}
		var disc format.Disc
		if err := disc.Decode(discBuf); err != nil {
			return nil, fmt.Errorf("restore: %s: %w", base, err)
		}
		runDir, err := newestRunDir(filepath.Join(base, "runs"))
		if err != nil {
			return nil, err
		}
		src.bases = append(src.bases, discSource{base: base, runDir: runDir, uuid: disc.DiscUUID})
		idxBuf, err := os.ReadFile(filepath.Join(runDir, "INDEX.bin"))
		if err != nil {
			return nil, fmt.Errorf("restore: %s: %w", runDir, err)
		}
		var idx format.Index
		if _, err := idx.Decode(idxBuf); err != nil {
			return nil, fmt.Errorf("restore: %s: %w", runDir, err)
		}
		for _, row := range idx.Objects {
			src.contentToRun[object.ID(row.ContentID)] = idx.RunSeq
		}
		for _, row := range idx.Prereqs {
			if _, ok := src.contentToRun[object.ID(row.ContentID)]; !ok {
				src.contentToRun[object.ID(row.ContentID)] = row.RunSeq
			}
		}
		src.runSeqToUUID[idx.RunSeq] = disc.DiscUUID

		discsBuf, err := os.ReadFile(filepath.Join(runDir, "catalog", "DISCS.bin"))
		if err != nil {
			return nil, fmt.Errorf("restore: %s: %w", runDir, err)
		}
		var discs format.DiscsTable
		if _, err := discs.Decode(discsBuf); err != nil {
			return nil, fmt.Errorf("restore: %s: %w", runDir, err)
		}
		for _, row := range discs.Rows {
			src.runSeqToUUID[row.RunSeq] = row.DiscUUID
		}
	}
	return src, nil
}

// newestRunDir is image.NewestRunDir, duplicated locally so this package
// does not import internal/image.
func newestRunDir(runsDir string) (string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("restore: runs directory: %w", err)
	}
	best := ""
	var bestSeq int64 = -1
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var seq int64
		if _, err := fmt.Sscanf(e.Name(), "%d", &seq); err != nil {
			continue
		}
		if seq > bestSeq {
			bestSeq = seq
			best = e.Name()
		}
	}
	if best == "" {
		return "", fmt.Errorf("restore: no run directory under %s", runsDir)
	}
	return filepath.Join(runsDir, best), nil
}

// read looks for id directly on every provided disc root, and returns
// its raw file bytes and decompressed, verified payload. A miss is
// recorded (by the disc it must live on, when known) and reported false;
// the caller decides whether it can still make progress without id.
func (src *multiSource) read(id object.ID, snapshot bool) (raw, payload []byte, ok bool) {
	for _, b := range src.bases {
		path := objectPath(b.base, id, snapshot)
		if _, err := os.Stat(path); err == nil {
			if raw, payload, err := readVerifiedAt(path, id); err == nil {
				return raw, payload, true
			}
		}
		// A snapshot object's canonical copy is written only to the
		// disc that packed it, but its catalog/snapobj copy is
		// replicated in full on every run; try that too.
		if snapshot {
			snapobjPath := filepath.Join(b.runDir, "catalog", "snapobj", id.TextForm())
			if _, err := os.Stat(snapobjPath); err == nil {
				if raw, payload, err := readVerifiedAt(snapobjPath, id); err == nil {
					return raw, payload, true
				}
			}
		}
	}
	if runSeq, ok := src.contentToRun[id]; ok {
		if uuid, ok := src.runSeqToUUID[runSeq]; ok {
			src.missingByDisc[uuid] = append(src.missingByDisc[uuid], id)
			return nil, nil, false
		}
	}
	src.missingUnknown = append(src.missingUnknown, id)
	return nil, nil, false
}

// finalError returns the accumulated MissingDiscError, or nil when the
// whole walk found everything it needed.
func (src *multiSource) finalError() error {
	if len(src.missingByDisc) == 0 && len(src.missingUnknown) == 0 {
		return nil
	}
	if len(src.missingByDisc) > 0 {
		return &MissingDiscError{ByDisc: src.missingByDisc}
	}
	return fmt.Errorf("restore: %d object(s) not found on any provided disc and named by no provided disc's INDEX", len(src.missingUnknown))
}

// restoreRootEntry mirrors restoreRootEntry, reading through src instead
// of one fixed base.
func (src *multiSource) restoreRootEntry(outDir string, e format.TreeEntry, prog *progress.Reporter) error {
	if e.EntryType != format.EntryTypeDirectory {
		return fmt.Errorf("restore: root entry %q: expected a directory", e.Name)
	}
	rootPath := ""
	for _, t := range e.TLVs {
		if t.Type == format.TLVTypeRootPath {
			rootPath = string(t.Payload)
		}
	}
	if rootPath == "" {
		return fmt.Errorf("restore: root entry %q: no root path TLV", e.Name)
	}
	dest, err := joinSafe(outDir, rootPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if err := src.restoreDirContents(object.ID(e.ContentID), dest, prog); err != nil {
		return err
	}
	applyMetadata(dest, e)
	return nil
}

func (src *multiSource) restoreDirContents(treeID object.ID, dest string, prog *progress.Reporter) error {
	raw, _, ok := src.read(treeID, false)
	if !ok {
		// This whole subtree is unreachable without a missing disc;
		// record it and skip it, so the rest of the tree still
		// restores.
		return nil
	}
	var t format.Tree
	if _, err := t.Decode(raw); err != nil {
		return fmt.Errorf("restore: tree %s: %w", treeID.TextForm(), err)
	}
	for _, e := range t.Entries {
		if err := src.restoreEntry(dest, e, prog); err != nil {
			return err
		}
	}
	return nil
}

func (src *multiSource) restoreEntry(dir string, e format.TreeEntry, prog *progress.Reporter) error {
	name := string(e.Name)
	child, err := joinSafe(dir, name)
	if err != nil {
		return err
	}
	switch e.EntryType {
	case format.EntryTypeDirectory:
		if err := os.MkdirAll(child, 0o755); err != nil {
			return err
		}
		if err := src.restoreDirContents(object.ID(e.ContentID), child, prog); err != nil {
			return err
		}
		applyMetadata(child, e)
		return nil
	case format.EntryTypeRegular:
		if err := src.restoreFile(child, object.ID(e.ContentID), prog); err != nil {
			return err
		}
		applyMetadata(child, e)
		return nil
	case format.EntryTypeSymlink:
		target := ""
		for _, t := range e.TLVs {
			if t.Type == format.TLVTypeSymlinkTarget {
				target = string(t.Payload)
			}
		}
		if target == "" {
			return fmt.Errorf("restore: symlink %q has no target TLV", name)
		}
		if err := os.RemoveAll(child); err != nil {
			return err
		}
		return os.Symlink(target, child)
	default:
		return fmt.Errorf("restore: entry %q: entry type %d is not restored", name, e.EntryType)
	}
}

func (src *multiSource) restoreFile(dest string, blobID object.ID, prog *progress.Reporter) error {
	raw, _, ok := src.read(blobID, false)
	if !ok {
		// The blob itself is unreachable; record it (already done by
		// read) and skip this file so the rest of the tree restores.
		return nil
	}
	var blob format.Blob
	if _, err := blob.Decode(raw); err != nil {
		return fmt.Errorf("restore: blob %s: %w", blobID.TextForm(), err)
	}

	entries := append([]format.BlobEntry(nil), blob.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].FileOffset < entries[j].FileOffset })

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	complete := true
	for _, be := range entries {
		_, payload, ok := src.read(object.ID(be.ContentID), false)
		if !ok {
			complete = false
			continue
		}
		if uint64(len(payload)) != be.Length {
			return fmt.Errorf("restore: chunk %s: length %d, blob entry says %d",
				object.ID(be.ContentID).TextForm(), len(payload), be.Length)
		}
		if _, err := f.WriteAt(payload, int64(be.FileOffset)); err != nil {
			return err
		}
		prog.Add(int64(len(payload)))
	}
	if !complete {
		// Leave the partially written file in place; the missing-disc
		// error already names what is needed to finish it.
		return nil
	}
	return nil
}
