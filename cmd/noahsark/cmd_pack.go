package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// cmdPack implements "noahsark pack". pack takes every pending ref;
// there is no way to name a snapshot explicitly. See docs/decisions.md,
// "Pack".
func cmdPack(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	fs := newFlagSet("noahsark pack [--capacity=SIZE] [--label=TEXT] [--out=DIR] [--fec] [--close] [--dry-run]",
		"Pack staged objects onto the next disc.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	capacityStr := fs.String("capacity", "", "target capacity ("+capacityHelpText()+"); defaults to pack.capacity in the config")
	label := fs.String("label", "", "human label for the disc; defaults to the newest ref name and the disc number")
	outDir := fs.String("out", "", "output directory for the packed tree; must not already exist or must be empty; default <staging.dir>/plans/<disc uuid>/tree")
	fecOn := fs.Bool("fec", false, "write a Reed-Solomon checksum column and parity for this run; overrides fec.scheme")
	closeDisc := fs.Bool("close", false, "print a burn command that seals the disc: spare:none and -dvd-compat, with no later append. It changes the printed command only; noahsark does not burn")
	dryRun := fs.Bool("dry-run", false, "print the discs the staged data needs at this capacity, and stop; writes nothing")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("pack", fs, stderr) {
		return 2
	}

	repoDir, err := discoverRepo(*repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 2
	}
	if refuseBadConfig("pack", cfg, stderr, configKeysForPack...) {
		return 2
	}

	// --dry-run only reads the staging store and the ledgers; it takes
	// no repository lock, matching the rule that a read-only command
	// takes none. Every other pack path writes the state log, the
	// staging store or the ledgers, so it takes the lock as usual.
	var lk *repolock.Lock
	if !*dryRun {
		var code int
		var ok bool
		lk, code, ok = lockRepo("pack", repoDir, stderr)
		if !ok {
			return code
		}
		defer releaseLock(lk)
	}

	capacityArg := *capacityStr
	if capacityArg == "" {
		capacityArg = cfg.PackCapacity
	}
	if capacityArg == "" {
		_, _ = fmt.Fprintf(stderr, "noahsark: pack: no capacity: pass --capacity, or put pack.capacity in %s\n", configPath(repoDir))
		return 2
	}
	capacitySectors, err := parseCapacity(capacityArg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 2
	}

	var snapshots []image.SnapshotRef
	now := time.Now()

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}

	snapshots, err = addPendingRefs(repoDir, cfg.StagingDir, repoUUID, snapshots, now)
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
		if newest := newestRef(cfg.StagingDir, allRepoRefs(repoDir)); newest != nil {
			newest.Time = now
			snapshots = append(snapshots, *newest)
		}
	}

	fecEnabled := cfg.FECEnabled
	if *fecOn {
		fecEnabled = true
	}

	// labelFor answers what label a disc with this number gets, so a
	// dry run predicts the same label, and so the same README bytes,
	// that the real pack of that disc writes.
	labelFor := func(discSeq uint64) string {
		if *label != "" {
			return *label
		}
		return defaultLabel(repoDir, cfg, snapshots, discSeq)
	}

	if *dryRun {
		return runPackDryRun(stdout, stderr, cfg, repoUUID, snapshots, capacitySectors, fecEnabled, labelFor)
	}

	ledger, err := image.LoadDiscsLedger(cfg.StagingDir, repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	_, nextDiscSeq := image.NextSeqNumbers(ledger.Rows)
	discLabel := labelFor(nextDiscSeq)

	discUUIDBytes := make([]byte, 16)
	if _, err := rand.Read(discUUIDBytes); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	var discUUID [16]byte
	copy(discUUID[:], discUUIDBytes)

	if *outDir == "" {
		*outDir = filepath.Join(cfg.StagingDir, "plans", hex.EncodeToString(discUUIDBytes), "tree")
	}
	absOut, err := filepath.Abs(*outDir)
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

	stageLog, err := stage.Open(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	warnIfTruncated("pack", stageLog, stderr)

	opts := image.PackOptions{
		StagingDir:            cfg.StagingDir,
		Snapshots:             snapshots,
		TargetCapacitySectors: capacitySectors,
		OutputDir:             absOut,
		RepoUUID:              repoUUID,
		DiscUUID:              discUUID,
		Label:                 discLabel,
		FECEnabled:            fecEnabled,
		StageLog:              stageLog,
		Progress:              prog,
	}
	result, err := image.Pack(opts)
	if err != nil {
		if tooSmall, ok := errors.AsType[*image.ErrCapacityTooSmall](err); ok {
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: capacity %s (%d bytes) holds not one object; %s\n",
				capacityArg, capacitySectors*image.SectorSize, smallestObjectText(tooSmall))
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

	if err := populateCache(repoDir, absOut); err != nil {
		// The cache is only an accelerator: a failure to populate it
		// never fails the pack, since every command must still work
		// with the cache absent or stale.
		_, _ = fmt.Fprintln(stderr, "noahsark: pack: cache:", err)
	}

	_, _ = fmt.Fprintf(stdout, "packed disc %d %q: %d object(s) on the disc, %d bytes\n",
		result.DiscSeq, discLabel, result.ObjectCount, result.ObjectBytes)
	_, _ = fmt.Fprintf(stdout, "uuid: %s\n", uuidText(discUUID))
	_, _ = fmt.Fprintf(stdout, "tree: %s\n", absOut)
	if fecEnabled {
		_, _ = fmt.Fprintln(stdout, "fec: on")
	}

	repoArg := ""
	if *repoFlag != "" {
		repoArg = " --repo=" + repoDir
	}
	printNextSteps(stdout, repoArg, absOut, result.DiscSeq, *closeDisc)

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
// tree, no state record, no cache entry, no ledger row, and no sequence
// number is used. It takes no repository lock, since it only reads the
// staging store and the ledgers.
func runPackDryRun(stdout, stderr io.Writer, cfg repoConfig, repoUUID [16]byte, snapshots []image.SnapshotRef, capacitySectors uint64, fecEnabled bool, labelFor func(uint64) string) int {
	stageLog, err := stage.OpenReadOnly(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	warnIfTruncated("pack", stageLog, stderr)

	opts := image.PackOptions{
		StagingDir:            cfg.StagingDir,
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
func defaultLabel(repoDir string, cfg repoConfig, snapshots []image.SnapshotRef, discSeq uint64) string {
	name := newestRefName(cfg.StagingDir, snapshots)
	if name == "" {
		name = newestRefName(cfg.StagingDir, allRepoRefs(repoDir))
	}
	if name == "" {
		return fmt.Sprintf("disc %d", discSeq)
	}
	return fmt.Sprintf("%s disc %d", name, discSeq)
}

// newestRef returns the ref whose snapshot was committed last, by the
// same rule newestRefName applies, or nil when snapshots is empty.
func newestRef(stagingDir string, snapshots []image.SnapshotRef) *image.SnapshotRef {
	name := newestRefName(stagingDir, snapshots)
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
// A snapshot whose staged object gc has already freed counts as the
// oldest, thus a name is still returned while any ref is given.
func newestRefName(stagingDir string, snapshots []image.SnapshotRef) string {
	name := ""
	var newest int64
	for _, s := range snapshots {
		sec, err := snapshotTime(stagingDir, s.ID)
		if err != nil {
			sec = 0
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
func allRepoRefs(repoDir string) []image.SnapshotRef {
	refs, err := readRefs(repoDir)
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

// snapshotTime reads one staged snapshot object's commit time.
func snapshotTime(stagingDir string, id object.ID) (int64, error) {
	data, err := os.ReadFile(filepath.Join(stagingDir, "snapshots", id.TextForm()))
	if err != nil {
		return 0, err
	}
	var snap format.Snapshot
	if _, err := snap.Decode(data); err != nil {
		return 0, err
	}
	return snap.TimeSec, nil
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
	_, _ = fmt.Fprintf(stdout, "  noahsark disc burned%s %d\n", repoArg, discSeq)
	_, _ = fmt.Fprintf(stdout, "  noahsark verify%s <MOUNT>\n", repoArg)
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
func addPendingRefs(repoDir, stagingDir string, repoUUID [16]byte, named []image.SnapshotRef, now time.Time) ([]image.SnapshotRef, error) {
	allRefs, err := readRefs(repoDir)
	if err != nil {
		return named, err
	}
	ledger, err := image.LoadRefsLedger(stagingDir, repoUUID)
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

// populateCache copies the run just packed at runRoot, every known
// snapshot, and every tree it reaches, into the local cache, so a
// later ls or plan can run with no disc present.
func populateCache(repoDir, runRoot string) error {
	c, err := cache.Open(cache.Dir(repoDir))
	if err != nil {
		return err
	}
	_, err = cache.WriteFromRoot(c, runRoot)
	return err
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
