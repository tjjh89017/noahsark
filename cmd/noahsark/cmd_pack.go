package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

func init() {
	register(&command{
		name:    "pack",
		usage:   "pack --capacity=SIZE [--label=TEXT] [--out=DIR] [--fec] [--close] [--dry-run]",
		summary: "Pack staged objects onto the next disc.",
		flags:   packFlags,
	})
}

// packOptions holds the command options of pack.
type packOptions struct {
	capacity  string
	label     string
	outDir    string
	fec       bool
	closeDisc bool
	dryRun    bool
}

func packFlags(fs *flag.FlagSet) runFunc {
	o := &packOptions{}
	fs.StringVar(&o.capacity, "capacity", "", "target capacity ("+capacityHelpText()+"); required")
	fs.StringVar(&o.label, "label", "", "human label for the disc; defaults to the newest ref name and the disc number")
	fs.StringVar(&o.outDir, "out", "", "output directory for the packed tree; must not already exist or must be empty; default <staging.dir>/plans/<disc uuid>/tree")
	fs.BoolVar(&o.fec, "fec", false, "write a Reed-Solomon checksum column and parity for this run")
	fs.BoolVar(&o.closeDisc, "close", false, "print a burn command that seals the disc: spare:none and -dvd-compat, with no later append. It changes the printed command only; noahsark does not burn")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the discs the staged data needs at this capacity, and stop; writes nothing")
	return o.run
}

// run implements "noahsark pack". pack takes every pending ref; there
// is no way to name a snapshot explicitly. See docs/decisions.md, "Pack".
func (o *packOptions) run(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	outDir := o.outDir

	repoDir, err := e.findRepo()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 2
	}
	if o.capacity == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack needs --capacity")
		return 2
	}
	capacitySectors, err := parseCapacity(o.capacity)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 2
	}

	// --dry-run only reads the staging store and the ledgers; it takes
	// no repository lock, matching the rule that a read-only command
	// takes none. Every other pack path writes the state log, the
	// staging store or the ledgers, so it takes the lock as usual.
	var lk *repolock.Lock
	if !o.dryRun {
		var code int
		var ok bool
		lk, code, ok = lockRepo("pack", repoDir, stderr)
		if !ok {
			return code
		}
		defer releaseLock(lk)
	}

	var snapshots []image.SnapshotRef
	now := e.now()

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}

	layout := layoutOf(repoDir, cfg)
	c, err := catalog.Open(repoDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}

	snapshots, err = addPendingRefs(layout, repoUUID, snapshots, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}

	// addPendingRefs above already carried forward every ref pack has
	// not yet moved onto a run. When that left nothing, name the
	// newest ref of the repository, so a pack after gc reports an
	// already-packed repository instead of one that never had a
	// commit.
	if len(snapshots) == 0 {
		if newest := newestRef(c, allRepoRefs(layout)); newest != nil {
			newest.Time = now
			snapshots = append(snapshots, *newest)
		}
	}

	fecEnabled := o.fec

	// labelFor answers what label a disc with this number gets, so a
	// dry run predicts the same label, and so the same README bytes,
	// that the real pack of that disc writes.
	labelFor := func(discSeq uint64) string {
		if o.label != "" {
			return o.label
		}
		return defaultLabel(layout, c, snapshots, discSeq)
	}

	if o.dryRun {
		return runPackDryRun(stdout, stderr, layout, c, repoUUID, snapshots, capacitySectors, fecEnabled, labelFor)
	}

	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	_, nextDiscSeq := image.NextSeqNumbers(ledger.Rows)
	discLabel := labelFor(nextDiscSeq)

	var discUUID [16]byte
	if _, err := rand.Read(discUUID[:]); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}

	if outDir == "" {
		outDir = layout.planTree(discUUID)
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	if empty, err := dirIsEmptyOrMissing(absOut); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	} else if !empty {
		_, _ = fmt.Fprintf(stderr, "noahsark: pack: --out=%s already holds files; choose another --out\n", absOut)
		return 2
	}

	stageLog, err := stage.Open(layout.stateDir())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	warnIfTruncated("pack", stageLog, stderr)

	opts := image.PackOptions{
		Store:                 packStore(layout, c, stageLog),
		Snapshots:             snapshots,
		TargetCapacitySectors: capacitySectors,
		OutputDir:             absOut,
		RepoUUID:              repoUUID,
		DiscUUID:              discUUID,
		Label:                 discLabel,
		FECEnabled:            fecEnabled,
		StageLog:              stageLog,
		Progress:              e.progress(),
	}
	result, err := image.Pack(opts)
	if err != nil {
		if tooSmall, ok := errors.AsType[*image.ErrCapacityTooSmall](err); ok {
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: capacity %s (%d bytes) holds not one object; %s\n",
				o.capacity, capacitySectors*image.SectorSize, smallestObjectText(tooSmall))
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: use a capacity of %d bytes or more\n", tooSmall.NeededSectors*image.SectorSize)
			return 2
		}
		if errors.Is(err, image.ErrNothingToPack) {
			// Nothing left to write is not a failure: pack did
			// everything the repository's state allows.
			_, _ = fmt.Fprintln(stdout, "pack:", err)
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}

	if _, err := catalog.WriteTablesFromRoot(c, absOut); err != nil {
		// The disc root is packed and recorded. A failure to copy its
		// tables into the catalog does not undo that.
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
	}

	_, _ = fmt.Fprintf(stdout, "packed disc %d %q: %d object(s) on the disc, %d bytes\n",
		result.DiscSeq, discLabel, result.ObjectCount, result.ObjectBytes)
	_, _ = fmt.Fprintf(stdout, "uuid: %s\n", uuidText(discUUID))
	_, _ = fmt.Fprintf(stdout, "tree: %s\n", absOut)
	if fecEnabled {
		_, _ = fmt.Fprintln(stdout, "fec: on")
	}

	repoArg := ""
	if e.global.repo != "" {
		repoArg = " --repo=" + repoDir
	}
	printNextSteps(stdout, repoArg, absOut, result.DiscSeq, o.closeDisc)

	// Objects left STAGED after a successful pack are not a failure: the
	// disc was packed correctly, and the leftover simply waits for the
	// next disc. This line is how the operator learns to run pack again.
	if result.RemainingObjects > 0 {
		_, _ = fmt.Fprintf(stdout, "remaining staged: %d objects, %d bytes; pack again for the next disc\n", result.RemainingObjects, result.RemainingBytes)
		return 0
	}
	_, _ = fmt.Fprintln(stdout, "remaining staged: 0 objects, 0 bytes")
	return 0
}

// runPackDryRun implements "pack --dry-run". It answers how many discs
// the staged data needs at this capacity, and writes nothing: no output
// tree, no state record, no catalog entry, no ledger row, and no sequence
// number is used. It takes no repository lock, since it only reads the
// staging store and the ledgers.
func runPackDryRun(stdout, stderr io.Writer, layout repoLayout, c *catalog.Catalog, repoUUID [16]byte, snapshots []image.SnapshotRef, capacitySectors uint64, fecEnabled bool, labelFor func(uint64) string) int {
	stageLog, err := stage.OpenReadOnly(layout.stateDir())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	warnIfTruncated("pack", stageLog, stderr)

	opts := image.PackOptions{
		Store:                 packStore(layout, c, stageLog),
		Snapshots:             snapshots,
		TargetCapacitySectors: capacitySectors,
		RepoUUID:              repoUUID,
		FECEnabled:            fecEnabled,
		StageLog:              stageLog,
	}
	discs, err := image.DryRun(opts, labelFor)
	printDryRunDiscs(stdout, discs)
	if err != nil {
		if tooSmall, ok := errors.AsType[*image.ErrCapacityTooSmall](err); ok {
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: capacity (%d bytes) holds not one object; %s\n",
				capacitySectors*image.SectorSize, smallestObjectText(tooSmall))
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: use a capacity of %d bytes or more\n", tooSmall.NeededSectors*image.SectorSize)
			return 2
		}
		if errors.Is(err, image.ErrNothingToPack) {
			_, _ = fmt.Fprintln(stdout, "pack:", err)
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	return 0
}

// printDryRunDiscs prints one line for each predicted disc, then the
// totals and the one action the operator takes next.
func printDryRunDiscs(stdout io.Writer, discs []image.DryRunDisc) {
	var totalObjects int
	var totalBytes uint64
	for _, d := range discs {
		_, _ = fmt.Fprintf(stdout, "disc %d %q: %d object(s) on the disc, %d bytes\n", d.DiscSeq, d.Label, d.ObjectCount, d.ObjectBytes)
		totalObjects += d.ObjectCount
		totalBytes += d.ObjectBytes
	}
	_, _ = fmt.Fprintf(stdout, "total: %d disc(s), %d object(s) on the discs, %d bytes\n", len(discs), totalObjects, totalBytes)
	if len(discs) > 0 {
		_, _ = fmt.Fprintf(stdout, "next: run noahsark pack %d time(s), one disc for each pack\n", len(discs))
	}
}

// smallestObjectText names the smallest staged object of a refused
// capacity, the one object the capacity must first grow to hold.
func smallestObjectText(e *image.ErrCapacityTooSmall) string {
	return fmt.Sprintf("the smallest staged object is %s %s, %d bytes",
		kindWord(e.SmallestKind), e.SmallestID.TextForm(), e.SmallestBytes)
}

// kindWord renders an object kind as the word an operator message uses.
func kindWord(kind format.ObjectKind) string {
	switch kind {
	case format.ObjectKindChunk:
		return "chunk"
	case format.ObjectKindBlob:
		return "blob"
	case format.ObjectKindTree:
		return "tree"
	case format.ObjectKindSnapshot:
		return "snapshot"
	default:
		return "object"
	}
}

// defaultLabel builds the label a pack uses when --label names none:
// the name of the newest ref this disc carries, and discSeq. A disc
// that carries no ref uses the newest ref of the repository instead, so
// a later disc of the same run of packs keeps a name an operator reads.
// A repository with no ref at all gets the disc number alone.
func defaultLabel(layout repoLayout, c *catalog.Catalog, snapshots []image.SnapshotRef, discSeq uint64) string {
	name := newestRefName(c, snapshots)
	if name == "" {
		name = newestRefName(c, allRepoRefs(layout))
	}
	if name == "" {
		return fmt.Sprintf("disc %d", discSeq)
	}
	return fmt.Sprintf("%s disc %d", name, discSeq)
}

// newestRef returns the ref whose snapshot was committed last, by the
// same rule newestRefName applies, or nil when snapshots is empty.
func newestRef(c *catalog.Catalog, snapshots []image.SnapshotRef) *image.SnapshotRef {
	name := newestRefName(c, snapshots)
	if name == "" {
		return nil
	}
	for i := range snapshots {
		if snapshots[i].Name == name {
			return &snapshots[i]
		}
	}
	return nil
}

// newestRefName returns the name of the ref whose snapshot was
// committed last. A tie goes to the name that sorts last, so a set of
// date names committed in the same second still gives the latest date.
// A snapshot that the catalog does not hold counts as the oldest, thus
// a name is still returned while any ref is given.
func newestRefName(c *catalog.Catalog, snapshots []image.SnapshotRef) string {
	name := ""
	var newest int64
	for _, s := range snapshots {
		var sec int64
		if snap, err := c.ReadSnapshot(s.ID); err == nil {
			sec = snap.TimeSec
		}
		if name == "" || sec > newest || (sec == newest && s.Name > name) {
			name, newest = s.Name, sec
		}
	}
	return name
}

// allRepoRefs reads every ref of the repository, for the label of a
// disc that carries no ref of its own. An unreadable ref file gives no
// ref, and the label then falls back to the disc number.
func allRepoRefs(layout repoLayout) []image.SnapshotRef {
	refs, err := readRefs(layout.refsFile())
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(refs))
	for n := range refs {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]image.SnapshotRef, 0, len(names))
	for _, n := range names {
		id, err := parseSnapshotID(refs[n])
		if err != nil {
			continue
		}
		out = append(out, image.SnapshotRef{Name: n, ID: id})
	}
	return out
}

// stringList implements flag.Value for a repeatable flag.
type stringList []string

func (s *stringList) String() string { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// This build has no burn command and no burner config keys, so the
// printed burn line always uses these defaults; a user with a different
// device or speed edits the printed line before running it.
const (
	burnerDefaultDevice = "/dev/sr0"
	burnerDefaultSpeed  = 4
)

// printNextSteps prints the four copy-ready commands that turn a packed
// tree into a burned, verified disc: building the UDF image, burning
// it, telling the staging state machine the burn happened, and
// verifying the mount. This build stops at pack, so these are printed
// rather than run. repoArg repeats --repo only when the operator gave
// it, so a repository found from NOAHSARK_REPO or from the working
// directory keeps the printed commands free of flags.
//
// The burn line follows FORMAT.md's and OPERATIONS.md's open-by-default
// rule: spare:min and no -dvd-compat, unless close is true, which is the
// only way this build ever prints -dvd-compat or spare:none.
//
// `disc burned` comes before `verify`: verify never moves an object
// from PACKED to BURNED itself, since a loop-mounted image checked
// before burning has the same disc uuid and would otherwise look
// burned too. See docs/decisions.md, "Burning and disc lifecycle".
func printNextSteps(stdout io.Writer, repoArg, treeDir string, discSeq uint64, sealDisc bool) {
	imagePath := treeDir + ".img"
	spareMode := "spare:min"
	dvdCompat := ""
	if sealDisc {
		spareMode = "spare:none"
		dvdCompat = "-dvd-compat "
	}
	_, _ = fmt.Fprintln(stdout, "next steps:")
	_, _ = fmt.Fprintf(stdout, "  sudo noahsark image build --out=%s %s\n", imagePath, treeDir)
	_, _ = fmt.Fprintf(stdout, "  growisofs -speed=%d -use-the-force-luke=%s,tty %s-Z %s=%s\n",
		burnerDefaultSpeed, spareMode, dvdCompat, burnerDefaultDevice, imagePath)
	_, _ = fmt.Fprintf(stdout, "  noahsark%s disc burned %d\n", repoArg, discSeq)
	_, _ = fmt.Fprintf(stdout, "  noahsark%s verify <MOUNT>\n", repoArg)
}

// dirIsEmptyOrMissing reports whether path does not exist yet, or exists
// as an empty directory. Pack refuses to write into a directory a run is
// already packed into, so it never rewrites another run's DISC.bin or
// README.txt.
func dirIsEmptyOrMissing(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

// addPendingRefs implements OPERATIONS.md's rule that pack carries
// forward every local ref record whose run_seq is still 0: a ref that
// commit moved since the last pack. named holds the refs already
// selected by --ref or --snapshot; addPendingRefs appends every other
// ref name whose current snapshot the refs ledger does not already
// carry under that name, so a pack picks up every pending ref, not
// only the one named on its command line.
func addPendingRefs(layout repoLayout, repoUUID [16]byte, named []image.SnapshotRef, now time.Time) ([]image.SnapshotRef, error) {
	allRefs, err := readRefs(layout.refsFile())
	if err != nil {
		return named, err
	}
	ledger, err := image.LoadRefsLedger(layout.refsLedgerFile(), repoUUID)
	if err != nil {
		return named, err
	}
	carried := make(map[string]object.ID, len(ledger.Records))
	for _, rec := range ledger.Records {
		carried[string(rec.Name[:rec.NameLen])] = object.ID(rec.SnapshotID)
	}
	haveName := make(map[string]bool, len(named))
	for _, s := range named {
		haveName[s.Name] = true
	}

	names := make([]string, 0, len(allRefs))
	for n := range allRefs {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		if haveName[n] {
			continue
		}
		id, err := parseSnapshotID(allRefs[n])
		if err != nil {
			return named, fmt.Errorf("ref %q: %w", n, err)
		}
		if cur, ok := carried[n]; ok && cur == id {
			continue // already carried forward with its own run_seq
		}
		named = append(named, image.SnapshotRef{Name: n, ID: id, Time: now})
		haveName[n] = true
	}
	return named, nil
}

// packStore gives pack the paths of the repository. Chunk objects come
// from staging, and snapshot, tree and blob objects from the catalog c.
// The snapshots are the snapshots of the catalog that the state log l
// knows: a staged snapshot is packed, and a snapshot that a disc holds
// is carried, as every run carries every snapshot of the repository.
func packStore(layout repoLayout, c *catalog.Catalog, l *stage.Log) image.Store {
	return image.Store{
		ObjectPath: layout.objectPath(c),
		SnapshotIDs: func() ([]object.ID, error) {
			all, err := c.ListSnapshots()
			if err != nil {
				return nil, err
			}
			known := make([]object.ID, 0, len(all))
			for _, id := range all {
				if _, ok := l.Get(id); ok {
					known = append(known, id)
				}
			}
			return known, nil
		},
		DiscsLedger: layout.discsLedgerFile(),
		RefsLedger:  layout.refsLedgerFile(),
	}
}

func decodeUUID(s string) ([16]byte, error) {
	var out [16]byte
	raw, err := hex.DecodeString(s)
	if err != nil {
		return out, fmt.Errorf("repo.uuid: %w", err)
	}
	if len(raw) != 16 {
		return out, fmt.Errorf("repo.uuid: expected 16 bytes, got %d", len(raw))
	}
	copy(out[:], raw)
	return out, nil
}
