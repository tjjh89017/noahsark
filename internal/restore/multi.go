package restore

import (
	"errors"
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
	// Names gives the disc number and the label of a disc uuid, when a
	// provided disc's DISCS table names it. The operator reads the
	// number and the label off the sleeve; the uuid alone makes them
	// compare 32 hexadecimal characters.
	Names map[[16]byte]DiscName
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

// DiscName is one disc's number and label, as a DISCS row gives them.
type DiscName struct {
	Seq   uint64
	Label string
}

// Text renders a disc's name and uuid the way every missing-disc line
// prints them.
func (n DiscName) Text(uuid [16]byte) string {
	if n.Label == "" {
		return fmt.Sprintf("disc %d (%s)", n.Seq, uuidText(uuid))
	}
	return fmt.Sprintf("disc %d %q (%s)", n.Seq, n.Label, uuidText(uuid))
}

// DiscCandidate is one disc named by a DISCS table but not provided to
// a restore.
type DiscCandidate struct {
	UUID [16]byte
	Name DiscName
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
			_, _ = fmt.Fprintf(&s, "\n  %s holds %d needed object(s)", e.Names[u].Text(u), len(e.ByDisc[u]))
		}
		return s.String()
	}
	if e.RootTreeMissing {
		s.WriteString("the snapshot's root tree is not on the provided disc(s)")
	} else {
		_, _ = fmt.Fprintf(&s, "%d object(s) not found on any provided disc and named by no provided disc's INDEX", e.UnnamedCount)
	}
	if len(e.Candidates) == 0 {
		s.WriteString("; no provided disc names the disc that holds them; provide more discs")
		return s.String()
	}
	s.WriteString("; disc(s) not provided, that may hold them:")
	for _, c := range e.Candidates {
		_, _ = fmt.Fprintf(&s, "\n  %s", c.Name.Text(c.UUID))
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
	contentToDisc  map[object.ID][16]byte
	missingByDisc  map[[16]byte][]object.ID
	missingUnknown []object.ID // needed but no disc could be identified
	// bad holds every object that a provided disc does hold, but whose
	// bytes do not verify. Such an object is damage, not a missing
	// disc, so it never enters the missing lists.
	bad       map[object.ID]error
	names     *image.NameCache
	provided  map[[16]byte]bool     // uuids of the discs RestoreMulti was given
	discNames map[[16]byte]DiscName // every disc uuid named by any provided disc's DISCS table, with its number and label
	wp        *writePolicy          // the overwrite rule for this restore, and its report
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
func RestoreMulti(discRoots []string, snapshotID object.ID, outDir string, opts ...Option) (Report, error) {
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
func RestoreMultiWithProgress(discRoots []string, snapshotID object.ID, outDir string, prog *progress.Reporter, opts ...Option) (Report, error) {
	o := newRestoreOptions(opts)
	if len(discRoots) == 0 {
		return Report{}, fmt.Errorf("at least one disc root is required")
	}
	src, err := newMultiSource(discRoots)
	if err != nil {
		return Report{}, err
	}
	src.wp = &writePolicy{overwrite: o.overwrite}
	for u, n := range o.knownDiscs {
		if _, ok := src.discNames[u]; !ok {
			src.discNames[u] = n
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return src.wp.report, err
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return src.wp.report, err
	}

	snapRaw, _, ok := src.read(snapshotID, true)
	if !ok {
		return src.wp.report, src.finalError()
	}
	var snap format.Snapshot
	if _, err := snap.Decode(snapRaw); err != nil {
		return src.wp.report, fmt.Errorf("snapshot %s: %w", snapshotID.TextForm(), err)
	}

	rootRaw, _, ok := src.read(object.ID(snap.RootTree), false)
	if !ok {
		err := src.finalError()
		if mde, isMissing := err.(*MissingDiscError); isMissing && len(mde.ByDisc) == 0 {
			mde.RootTreeMissing = true
		}
		return src.wp.report, err
	}
	var rootTree format.Tree
	if _, err := rootTree.Decode(rootRaw); err != nil {
		return src.wp.report, fmt.Errorf("tree %s: %w", object.ID(snap.RootTree).TextForm(), err)
	}

	fs, err := newFilterState(o.includes)
	if err != nil {
		return src.wp.report, err
	}
	if fs != nil {
		if err := src.resolveIncludes(rootTree.Entries, fs); err != nil {
			return src.wp.report, err
		}
		if err := src.finalError(); err != nil {
			return src.wp.report, err
		}
		if unmatched := unmatchedIncludes(fs, o.includes); len(unmatched) > 0 {
			return src.wp.report, &UnmatchedIncludeError{Paths: unmatched}
		}
	}

	// The walk does not pre-sum an expected total: summing would mean an
	// extra read pass through src, and a miss during that pass would
	// double-count itself into the eventual MissingDiscError. Progress
	// reports bytes written and throughput only, with no percentage or
	// ETA.
	//
	// The snapshot's own total_size is not that total. It sums the
	// payload of every distinct object, so it counts a deduplicated
	// chunk one time where the restore writes it into each file that
	// holds it, and it adds the tree and blob objects the restore never
	// writes. An --include filter narrows the walk further. A
	// percentage from that number would be wrong in both directions.
	prog.Start("restore: bytes written", 0)
	defer prog.Done()

	for _, e := range rootTree.Entries {
		if err := src.restoreRootEntry(absOut, e, prog, fs); err != nil {
			return src.wp.report, err
		}
	}
	return src.wp.report, src.finalError()
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
// INDEX (Objects and Prereqs) and DISCS, to build the map from object id
// to the disc that holds it.
//
// A run sequence number identifies a run only on the disc that wrote it.
// Two discs of different lineages can carry the same number. So a
// Prereqs row is resolved through the DISCS table of its own disc, and
// no run sequence number is ever compared across discs.
func newMultiSource(discRoots []string) (*multiSource, error) {
	src := &multiSource{
		contentToDisc: make(map[object.ID][16]byte),
		missingByDisc: make(map[[16]byte][]object.ID),
		bad:           make(map[object.ID]error),
		names:         image.NewNameCache(),
		provided:      make(map[[16]byte]bool),
		discNames:     make(map[[16]byte]DiscName),
	}
	// A Prereqs row only points at a disc. The disc that lists the
	// object in its own Objects rows is the better answer, so prereq
	// pointers are applied after every disc is read.
	type prereq struct {
		id   object.ID
		disc [16]byte
	}
	var prereqs []prereq
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
			if _, ok := src.discNames[row.DiscUUID]; !ok {
				src.discNames[row.DiscUUID] = DiscName{Seq: row.DiscSeq, Label: discLabelText(row)}
			}
		}

		for _, row := range idx.Objects {
			src.contentToDisc[object.ID(row.ContentID)] = disc.DiscUUID
		}
		for _, row := range idx.Prereqs {
			prereqs = append(prereqs, prereq{id: object.ID(row.ContentID), disc: row.DiscUUID})
		}
	}
	for _, p := range prereqs {
		if _, ok := src.contentToDisc[p.id]; !ok {
			src.contentToDisc[p.id] = p.disc
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
// the caller decides whether it can still make progress without id. An
// object that every provided disc holds but none of them can verify is
// recorded as damage instead, which badObject then names.
func (src *multiSource) read(id object.ID, snapshot bool) (raw, payload []byte, ok bool) {
	var badErr error
	for _, b := range src.bases {
		path := objectPath(b.base, id, snapshot, src.names)
		if _, err := os.Stat(path); err == nil {
			raw, payload, err := readVerifiedAt(path, id)
			if err == nil {
				return raw, payload, true
			}
			badErr = err
		}
	}
	if badErr != nil {
		src.bad[id] = badErr
		return nil, nil, false
	}
	if uuid, ok := src.contentToDisc[id]; ok {
		src.missingByDisc[uuid] = append(src.missingByDisc[uuid], id)
		return nil, nil, false
	}
	src.missingUnknown = append(src.missingUnknown, id)
	return nil, nil, false
}

// badObject returns why a provided disc's copy of id did not verify, or
// nil when id was simply not on any provided disc.
func (src *multiSource) badObject(id object.ID) error { return src.bad[id] }

// finalError returns the accumulated MissingDiscError, or nil when the
// whole walk found everything it needed. Damaged objects are not an
// error here: each one already names its own path in the report.
func (src *multiSource) finalError() error {
	if len(src.missingByDisc) == 0 && len(src.missingUnknown) == 0 {
		return nil
	}
	if len(src.missingByDisc) > 0 {
		return &MissingDiscError{ByDisc: src.missingByDisc, Names: src.discNames}
	}
	var candidates []DiscCandidate
	for uuid, name := range src.discNames {
		if src.provided[uuid] {
			continue
		}
		candidates = append(candidates, DiscCandidate{UUID: uuid, Name: name})
	}
	sort.Slice(candidates, func(i, j int) bool { return uuidText(candidates[i].UUID) < uuidText(candidates[j].UUID) })
	return &MissingDiscError{UnnamedCount: len(src.missingUnknown), Candidates: candidates}
}

// restoreRootEntry restores one entry of the synthetic root tree. Its
// destination is the source's own absolute path, carried in the entry's
// root-path TLV, joined under outDir; its content is the entry's own
// directory tree, restored directly into that destination rather than
// one level below it.
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
	applyMetadata(dest, e, src.wp)
	return nil
}

// restoreDirContents decodes the tree at treeID and restores every entry
// fs leaves in scope as a child of dest, which already exists.
func (src *multiSource) restoreDirContents(treeID object.ID, dest string, prog *progress.Reporter, fs *filterState) error {
	raw, _, ok := src.read(treeID, false)
	if !ok {
		// This whole subtree is unreachable: the tree object is on a
		// disc that was not provided, or its bytes are damaged. Both
		// are already recorded, so skip the subtree and let the rest of
		// the tree restore.
		if err := src.badObject(treeID); err != nil {
			src.wp.failed(dest, err)
		}
		return nil
	}
	var t format.Tree
	if _, err := t.Decode(raw); err != nil {
		src.wp.failed(dest, fmt.Errorf("tree %s: %w", treeID.TextForm(), err))
		return nil
	}
	taken := make(map[string]bool, len(t.Entries))
	for _, e := range t.Entries {
		taken[string(e.Name)] = true
	}
	for _, e := range t.Entries {
		childFS, include := stepInto(fs, []string{string(e.Name)})
		if !include {
			continue
		}
		if err := src.restoreEntry(dest, e, prog, childFS, taken); err != nil {
			return err
		}
	}
	return nil
}

// restoreEntry writes one tree entry as a child of dir. The caller has
// already decided the entry is in scope; fs is only used for a directory
// entry's own children. An entry that fails alone is recorded and the
// walk goes on to the next entry; only a failure that stops the whole
// walk is returned.
func (src *multiSource) restoreEntry(dir string, e format.TreeEntry, prog *progress.Reporter, fs *filterState, taken map[string]bool) error {
	name := string(e.Name)
	child, err := joinSafe(dir, name)
	if err != nil {
		src.wp.failed(filepath.Join(dir, name), err)
		return nil
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
		applyMetadata(sub, e, src.wp)
		return nil
	case format.EntryTypeRegular:
		part, err := joinSafe(dir, partName(name, taken))
		if err != nil {
			src.wp.failed(child, err)
			return nil
		}
		if err := src.restoreFile(child, part, object.ID(e.ContentID), e, prog); err != nil {
			src.wp.failed(child, err)
		}
		return nil
	case format.EntryTypeSymlink:
		target, err := symlinkTarget(e)
		if err != nil {
			src.wp.failed(child, err)
			return nil
		}
		if err := restoreSymlink(child, target, e, src.wp); err != nil {
			src.wp.failed(child, err)
		}
		return nil
	default:
		src.wp.unsupported(child, e.EntryType)
		return nil
	}
}

// restoreFile reassembles blobID's chunks into dest, in blob entry
// order, verifying every chunk's content id before it writes the bytes.
// The bytes go into the part file at part; dest gets its name only
// after every chunk is in place, so a file under the snapshot's own
// name is always complete.
//
// It leaves dest as found, and writes nothing, when the path already
// exists and --overwrite was not given, or when no provided disc holds
// the blob.
func (src *multiSource) restoreFile(dest, part string, blobID object.ID, e format.TreeEntry, prog *progress.Reporter) error {
	raw, _, ok := src.read(blobID, false)
	if !ok {
		// The blob itself is unreachable; read has already recorded a
		// missing disc, or badObject names the damage.
		return src.badObject(blobID)
	}
	var blob format.Blob
	if _, err := blob.Decode(raw); err != nil {
		return fmt.Errorf("blob %s: %w", blobID.TextForm(), err)
	}

	entries := placeChunks(blob.Entries)

	if !src.wp.overwrite {
		if resumed, found := existingFileStatus(dest, e, entries); found {
			if resumed {
				src.wp.resume()
				removePart(part)
			} else {
				src.wp.skip(dest)
			}
			return nil
		}
	}

	// A chunk no provided disc holds, or one whose bytes are damaged,
	// leaves the file short. Every disc is provided in this mode, so no
	// later run can finish it: the part file goes away and the file is
	// reported as not restored.
	var chunkErr error
	complete, err := writeChunks(part, e.Size, entries, prog, func(id object.ID) ([]byte, bool, error) {
		_, payload, ok := src.read(id, false)
		if !ok && chunkErr == nil {
			chunkErr = src.badObject(id)
		}
		return payload, ok, nil
	})
	if err != nil {
		removePart(part)
		return err
	}
	if !complete {
		removePart(part)
		if chunkErr != nil {
			return fmt.Errorf("%w; the part file is removed", chunkErr)
		}
		return errNoChunk
	}
	finishPart(dest, part, e, src.wp)
	return nil
}

// errNoChunk names a file whose chunks are not all on the provided
// discs. The missing-disc error names the disc to add; this line names
// the file that waits for it.
var errNoChunk = errors.New("a chunk of this file is on a disc that was not provided; the part file is removed")
