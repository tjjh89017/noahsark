package restore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
)

// MissingDiscError reports that restoring a snapshot needs a disc this
// call was not given, and which objects on it are needed.
type MissingDiscError struct {
	// ByDisc maps a needed disc's uuid to the object ids on it a
	// complete restore would need. Set only when at least one provided
	// disc's Prereqs row names the disc a missing object lives on.
	ByDisc map[[16]byte][]object.ID
	// UnnamedCount is the number of needed objects that no provided
	// disc's INDEX or Prereqs names. Set only when ByDisc is empty.
	UnnamedCount int
	// Candidates lists discs named by a provided disc's DISCS table, or
	// by WithKnownDiscs, that were not themselves provided. It is the
	// best guess at which disc to insert when no Prereqs row names the
	// disc. Set only when ByDisc is empty.
	Candidates []DiscCandidate
	// RootTreeMissing is set when the snapshot's own root tree, not
	// merely some object under it, could not be read from any provided
	// disc.
	RootTreeMissing bool
}

// DiscCandidate is one disc named by a DISCS table but not provided to
// a restore.
type DiscCandidate struct {
	UUID  [16]byte
	Label string
}

func (e *MissingDiscError) Error() string {
	var s strings.Builder
	if len(e.ByDisc) > 0 {
		s.WriteString("missing disc(s):")
		uuids := make([][16]byte, 0, len(e.ByDisc))
		for u := range e.ByDisc {
			uuids = append(uuids, u)
		}
		sort.Slice(uuids, func(i, j int) bool { return uuidText(uuids[i]) < uuidText(uuids[j]) })
		for _, u := range uuids {
			_, _ = fmt.Fprintf(&s, "\n  disc %s holds %d needed object(s)", uuidText(u), len(e.ByDisc[u]))
		}
		return s.String()
	}
	if e.RootTreeMissing {
		s.WriteString("the snapshot's root tree is not on the provided disc(s)")
	} else {
		_, _ = fmt.Fprintf(&s, "%d object(s) not found on any provided disc and named by no provided disc's INDEX", e.UnnamedCount)
	}
	if len(e.Candidates) > 0 {
		s.WriteString("; disc(s) not provided, that may hold them:")
		for _, c := range e.Candidates {
			_, _ = fmt.Fprintf(&s, "\n  disc %s", uuidText(c.UUID))
			if c.Label != "" {
				_, _ = fmt.Fprintf(&s, " (%s)", c.Label)
			}
		}
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
	names          *image.NameCache
	provided       map[[16]byte]bool   // uuids of the discs RestoreMulti was given
	discLabels     map[[16]byte]string // every disc uuid named by any provided disc's DISCS table, with its label
	wp             *writePolicy        // the overwrite rule for this restore, and its running resumed and skipped counts
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
func RestoreMulti(discRoots []string, snapshotID object.ID, outDir string, opts ...Option) (resumed, skipped int, err error) {
	return RestoreMultiWithProgress(discRoots, snapshotID, outDir, nil, opts...)
}

// RestoreMultiWithProgress is RestoreMulti, reporting bytes written
// through prog. A nil prog reports nothing.
//
// With WithInclude, every include path is checked against the
// snapshot's tree before any directory is created or file written,
// reading only the tree objects an included path needs. A missing disc
// found during that check, or during the restore itself, is reported as
// a *MissingDiscError; a path that matches nothing in the snapshot is
// reported as an *UnmatchedIncludeError.
func RestoreMultiWithProgress(discRoots []string, snapshotID object.ID, outDir string, prog *progress.Reporter, opts ...Option) (resumed, skipped int, err error) {
	o := newRestoreOptions(opts)
	if len(discRoots) == 0 {
		return 0, 0, fmt.Errorf("at least one disc root is required")
	}
	src, err := newMultiSource(discRoots)
	if err != nil {
		return 0, 0, err
	}
	src.wp = &writePolicy{overwrite: o.overwrite, onUnsupported: o.onUnsupported}
	for u, l := range o.knownDiscs {
		if _, ok := src.discLabels[u]; !ok {
			src.discLabels[u] = l
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, 0, err
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return 0, 0, err
	}

	snapRaw, _, ok := src.read(snapshotID, true)
	if !ok {
		return src.wp.resumed, src.wp.skipped, src.finalError()
	}
	var snap format.Snapshot
	if _, err := snap.Decode(snapRaw); err != nil {
		return src.wp.resumed, src.wp.skipped, fmt.Errorf("snapshot %s: %w", snapshotID.TextForm(), err)
	}

	rootRaw, _, ok := src.read(object.ID(snap.RootTree), false)
	if !ok {
		err := src.finalError()
		if mde, isMissing := err.(*MissingDiscError); isMissing && len(mde.ByDisc) == 0 {
			mde.RootTreeMissing = true
		}
		return src.wp.resumed, src.wp.skipped, err
	}
	var rootTree format.Tree
	if _, err := rootTree.Decode(rootRaw); err != nil {
		return src.wp.resumed, src.wp.skipped, fmt.Errorf("tree %s: %w", object.ID(snap.RootTree).TextForm(), err)
	}

	fs, err := newFilterState(o.includes)
	if err != nil {
		return src.wp.resumed, src.wp.skipped, err
	}
	if fs != nil {
		if err := src.resolveIncludes(rootTree.Entries, fs); err != nil {
			return src.wp.resumed, src.wp.skipped, err
		}
		if err := src.finalError(); err != nil {
			return src.wp.resumed, src.wp.skipped, err
		}
		if unmatched := unmatchedIncludes(fs, o.includes); len(unmatched) > 0 {
			return src.wp.resumed, src.wp.skipped, &UnmatchedIncludeError{Paths: unmatched}
		}
	}

	// Unlike the single-disc Restore, this does not pre-sum an expected
	// total: summing would mean an extra read pass through src, and a
	// miss during that pass would double-count itself into the eventual
	// MissingDiscError. Progress here reports bytes written and
	// throughput only, with no percentage or ETA.
	prog.Start("restore: bytes written", 0)
	defer prog.Done()

	for _, e := range rootTree.Entries {
		if err := src.restoreRootEntry(absOut, e, prog, fs); err != nil {
			return src.wp.resumed, src.wp.skipped, err
		}
	}
	return src.wp.resumed, src.wp.skipped, src.finalError()
}

// resolveIncludes checks every include path fs carries against
// rootEntries, reading only tree objects and writing nothing. A missing
// tree is recorded on src the same way a real restore records it, and is
// reported by the caller's next src.finalError call.
func (src *multiSource) resolveIncludes(rootEntries []format.TreeEntry, fs *filterState) error {
	for _, e := range rootEntries {
		if e.EntryType != format.EntryTypeDirectory {
			continue
		}
		rootPath := rootPathOf(e)
		if rootPath == "" {
			continue
		}
		child, include := stepInto(fs, splitPath(rootPath))
		if !include || child == nil {
			continue
		}
		if err := src.resolveIncludesDir(object.ID(e.ContentID), child); err != nil {
			return err
		}
	}
	return nil
}

// resolveIncludesDir is resolveIncludes for one already-matched
// directory's own tree.
func (src *multiSource) resolveIncludesDir(treeID object.ID, fs *filterState) error {
	raw, _, ok := src.read(treeID, false)
	if !ok {
		return nil
	}
	var t format.Tree
	if _, err := t.Decode(raw); err != nil {
		return fmt.Errorf("tree %s: %w", treeID.TextForm(), err)
	}
	for _, e := range t.Entries {
		child, include := stepInto(fs, []string{string(e.Name)})
		if !include || child == nil || e.EntryType != format.EntryTypeDirectory {
			continue
		}
		if err := src.resolveIncludesDir(object.ID(e.ContentID), child); err != nil {
			return err
		}
	}
	return nil
}

// newMultiSource resolves every disc root and reads its single run's
// INDEX (Objects and Prereqs) and DISCS, to build the id-to-run and
// run-to-disc maps a missing-object lookup needs.
func newMultiSource(discRoots []string) (*multiSource, error) {
	src := &multiSource{
		runSeqToUUID:  make(map[uint64][16]byte),
		contentToRun:  make(map[object.ID]uint64),
		missingByDisc: make(map[[16]byte][]object.ID),
		names:         image.NewNameCache(),
		provided:      make(map[[16]byte]bool),
		discLabels:    make(map[[16]byte]string),
	}
	for _, root := range discRoots {
		base, err := findNoahsark(root, src.names)
		if err != nil {
			return nil, err
		}
		discBuf, err := os.ReadFile(filepath.Join(base, src.names.Resolve(base, "DISC.bin")))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", base, err)
		}
		var disc format.Disc
		if err := disc.Decode(discBuf); err != nil {
			return nil, fmt.Errorf("%s: %w", base, err)
		}
		runDir, err := image.NewestRunDir(src.names.Join(base, "runs"))
		if err != nil {
			return nil, err
		}
		src.bases = append(src.bases, discSource{base: base, runDir: runDir, uuid: disc.DiscUUID})
		src.provided[disc.DiscUUID] = true
		idxBuf, err := os.ReadFile(filepath.Join(runDir, src.names.Resolve(runDir, "INDEX.bin")))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", runDir, err)
		}
		var idx format.Index
		if _, err := idx.Decode(idxBuf); err != nil {
			return nil, fmt.Errorf("%s: %w", runDir, err)
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

		catalogDir := src.names.Join(runDir, "catalog")
		discsBuf, err := os.ReadFile(filepath.Join(catalogDir, src.names.Resolve(catalogDir, "DISCS.bin")))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", runDir, err)
		}
		var discs format.DiscsTable
		if _, err := discs.Decode(discsBuf); err != nil {
			return nil, fmt.Errorf("%s: %w", runDir, err)
		}
		for _, row := range discs.Rows {
			src.runSeqToUUID[row.RunSeq] = row.DiscUUID
			if _, ok := src.discLabels[row.DiscUUID]; !ok {
				src.discLabels[row.DiscUUID] = discLabelText(row)
			}
		}
	}
	return src, nil
}

// discLabelText trims a DISCS row's fixed-width label field to its
// stored length.
func discLabelText(row format.DiscsRow) string {
	n := min(int(row.LabelLen), len(row.Label))
	return string(row.Label[:n])
}

// read looks for id directly on every provided disc root, and returns
// its raw file bytes and decompressed, verified payload. A miss is
// recorded (by the disc it must live on, when known) and reported false;
// the caller decides whether it can still make progress without id.
func (src *multiSource) read(id object.ID, snapshot bool) (raw, payload []byte, ok bool) {
	for _, b := range src.bases {
		path := objectPath(b.base, id, snapshot, src.names)
		if _, err := os.Stat(path); err == nil {
			if raw, payload, err := readVerifiedAt(path, id); err == nil {
				return raw, payload, true
			}
		}
		// A snapshot object's canonical copy is written only to the
		// disc that packed it, but its catalog/snapobj copy is
		// replicated in full on every run; try that too.
		if snapshot {
			snapobjPath := filepath.Join(src.names.Join(b.runDir, "catalog", "snapobj"), id.TextForm())
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
	var candidates []DiscCandidate
	for uuid, label := range src.discLabels {
		if src.provided[uuid] {
			continue
		}
		candidates = append(candidates, DiscCandidate{UUID: uuid, Label: label})
	}
	sort.Slice(candidates, func(i, j int) bool { return uuidText(candidates[i].UUID) < uuidText(candidates[j].UUID) })
	return &MissingDiscError{UnnamedCount: len(src.missingUnknown), Candidates: candidates}
}

// restoreRootEntry mirrors restoreRootEntry, reading through src instead
// of one fixed base.
func (src *multiSource) restoreRootEntry(outDir string, e format.TreeEntry, prog *progress.Reporter, fs *filterState) error {
	if e.EntryType != format.EntryTypeDirectory {
		return fmt.Errorf("root entry %q: expected a directory", e.Name)
	}
	rootPath := rootPathOf(e)
	if rootPath == "" {
		return fmt.Errorf("root entry %q: no root path TLV", e.Name)
	}
	childFS, include := stepInto(fs, splitPath(rootPath))
	if !include {
		return nil
	}
	dest, ok, err := ensureDir(outDir, splitPath(rootPath), src.wp)
	if err != nil || !ok {
		return err
	}
	if err := src.restoreDirContents(object.ID(e.ContentID), dest, prog, childFS); err != nil {
		return err
	}
	applyMetadata(dest, e)
	return nil
}

func (src *multiSource) restoreDirContents(treeID object.ID, dest string, prog *progress.Reporter, fs *filterState) error {
	raw, _, ok := src.read(treeID, false)
	if !ok {
		// This whole subtree is unreachable without a missing disc;
		// record it and skip it, so the rest of the tree still
		// restores.
		return nil
	}
	var t format.Tree
	if _, err := t.Decode(raw); err != nil {
		return fmt.Errorf("tree %s: %w", treeID.TextForm(), err)
	}
	for _, e := range t.Entries {
		childFS, include := stepInto(fs, []string{string(e.Name)})
		if !include {
			continue
		}
		if err := src.restoreEntry(dest, e, prog, childFS); err != nil {
			return err
		}
	}
	return nil
}

func (src *multiSource) restoreEntry(dir string, e format.TreeEntry, prog *progress.Reporter, fs *filterState) error {
	name := string(e.Name)
	child, err := joinSafe(dir, name)
	if err != nil {
		return err
	}
	switch e.EntryType {
	case format.EntryTypeDirectory:
		sub, ok, err := ensureDir(dir, []string{name}, src.wp)
		if err != nil || !ok {
			return err
		}
		if err := src.restoreDirContents(object.ID(e.ContentID), sub, prog, fs); err != nil {
			return err
		}
		applyMetadata(sub, e)
		return nil
	case format.EntryTypeRegular:
		skipped, err := src.restoreFile(child, object.ID(e.ContentID), e, prog)
		if err != nil {
			return err
		}
		if skipped {
			// The path already existed and --overwrite was not given;
			// leave it exactly as found.
			return nil
		}
		applyMetadata(child, e)
		return nil
	case format.EntryTypeSymlink:
		target, err := symlinkTarget(e)
		if err != nil {
			return err
		}
		return restoreSymlink(child, target, src.wp)
	default:
		src.wp.recordUnsupported(child, e.EntryType)
		return nil
	}
}

func (src *multiSource) restoreFile(dest string, blobID object.ID, e format.TreeEntry, prog *progress.Reporter) (skipped bool, err error) {
	raw, _, ok := src.read(blobID, false)
	if !ok {
		// The blob itself is unreachable; record it (already done by
		// read) and skip this file so the rest of the tree restores.
		return false, nil
	}
	var blob format.Blob
	if _, err := blob.Decode(raw); err != nil {
		return false, fmt.Errorf("blob %s: %w", blobID.TextForm(), err)
	}

	entries := append([]format.BlobEntry(nil), blob.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].FileOffset < entries[j].FileOffset })

	f, skipped, err := openForWrite(dest, e, entries, src.wp)
	if err != nil {
		return false, err
	}
	if skipped {
		return true, nil
	}

	// A chunk no provided disc holds leaves the file partly written.
	// That file stays in place: the missing-disc error already names
	// what is needed to finish it.
	_, err = writeChunks(f, entries, prog, func(id object.ID) ([]byte, bool, error) {
		_, payload, ok := src.read(id, false)
		return payload, ok, nil
	})
	return false, err
}
