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
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// mediaPresetAliases maps a --capacity preset name to the media type a
// pack built with that preset records in DISC.bin's media_type field,
// case insensitive.
var mediaPresetAliases = map[string]format.MediaType{
	"bd25":  format.MediaTypeBDRSL25GB,
	"bd50":  format.MediaTypeBDRDL50GB,
	"bd100": format.MediaTypeBDRXL100GB,
	"bd128": format.MediaTypeBDRXL128GB,
	"dvd+r": format.MediaTypeDVDPlusRSL,
	"dvd-r": format.MediaTypeDVDMinusRSL,
}

// discMediaType picks the media type a pack records: the type of the
// --capacity preset when capacityStr names one, else the BD-R-SL-25
// default. media_type is informational only (FORMAT.md's media type
// registry) and never gates reading or writing.
func discMediaType(capacityStr string) format.MediaType {
	if mt, ok := mediaPresetAliases[strings.ToLower(capacityStr)]; ok {
		return mt
	}
	return format.MediaTypeBDRSL25GB
}

// cmdPack implements "noahsark pack". It reduces OPERATIONS.md's pack
// flags: object selection by staging fill or age (disc.min_fill,
// disc.max_wait), locality presets and burn-plan output do not exist in
// this build, so pack instead takes the snapshot(s) to place explicitly,
// by --ref or repeated --snapshot; with neither given, it carries
// forward every pending ref, falling back to LATEST only when that
// leaves nothing. See docs/decisions.md, "16. CLI reference".
func cmdPack(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	fs := newFlagSet("noahsark pack [--ref=NAME | --snapshot=ID]... [--capacity=SIZE] [--physical-capacity=SIZE] [--label=TEXT] [--out=DIR] [--fec | --no-fec] [--close]",
		"Pack staged objects onto the next disc.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	ref := fs.String("ref", "", "extra ref name to carry onto the disc; every pending ref is carried regardless")
	var snapshotFlags stringList
	fs.Var(&snapshotFlags, "snapshot", "snapshot id to pack; repeatable")
	capacityStr := fs.String("capacity", "", "target capacity ("+capacityHelpText()+"); defaults to pack.capacity in the config")
	physicalCapacityStr := fs.String("physical-capacity", "", "the disc's physical capacity ("+capacityHelpText()+"); defaults to --capacity, so this only needs setting when the target is a forced, smaller limit")
	label := fs.String("label", "", "human label for the disc; defaults to the newest ref name and the disc number")
	outDir := fs.String("out", "", "output directory for the packed tree; must not already exist or must be empty; default <repo>/staging/plans/<disc uuid>/tree")
	fecOn := fs.Bool("fec", false, "write a Reed-Solomon checksum column and parity for this run; overrides fec.scheme")
	fecOff := fs.Bool("no-fec", false, "write no FEC for this run; overrides fec.scheme")
	closeDisc := fs.Bool("close", false, "seal the disc when it is burned: spare:none and -dvd-compat, no later append. Only the printed burn command changes; this build does not burn or track disc state")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("pack", fs, stderr) {
		return 2
	}
	if *fecOn && *fecOff {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack: --fec and --no-fec are mutually exclusive")
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

	lk, code, ok := lockRepo("pack", repoDir, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

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

	physicalCapacitySectors := capacitySectors
	if *physicalCapacityStr != "" {
		physicalCapacitySectors, err = parseCapacity(*physicalCapacityStr)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
			return 2
		}
	}

	mediaType := discMediaType(capacityArg)

	snapIDs := []string(snapshotFlags)
	if *ref != "" && len(snapIDs) > 0 {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack: --ref and --snapshot are mutually exclusive")
		return 2
	}
	explicitTarget := *ref != "" || len(snapIDs) > 0

	var snapshots []image.SnapshotRef
	now := time.Now()
	if *ref != "" {
		id, err := resolveRef(repoDir, *ref)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
			return 1
		}
		snapshots = append(snapshots, image.SnapshotRef{Name: *ref, ID: id, Time: now})
	}
	for _, s := range snapIDs {
		id, err := parseSnapshotID(s)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
			return 2
		}
		snapshots = append(snapshots, image.SnapshotRef{Name: id.TextForm(), ID: id, Time: now})
	}

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

	// With no --ref and no --snapshot, addPendingRefs above already
	// carried forward every ref pack has not yet moved onto a run. Fall
	// back to LATEST only when that left nothing pending, and only when
	// LATEST itself resolves; a repository whose commits always name
	// their own --ref never creates a LATEST ref, and pack must not
	// fail on that account.
	if !explicitTarget && len(snapshots) == 0 {
		if id, latestErr := resolveRef(repoDir, "LATEST"); latestErr == nil {
			snapshots = append(snapshots, image.SnapshotRef{Name: "LATEST", ID: id, Time: now})
		}
	}

	discLabel := *label
	if discLabel == "" {
		discLabel, err = defaultLabel(cfg, repoUUID, snapshots)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
			return 1
		}
	}

	discUUIDBytes := make([]byte, 16)
	if _, err := rand.Read(discUUIDBytes); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	var discUUID [16]byte
	copy(discUUID[:], discUUIDBytes)

	if *outDir == "" {
		*outDir = filepath.Join(repoDir, "staging", "plans", hex.EncodeToString(discUUIDBytes), "tree")
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

	fecEnabled := cfg.FECEnabled
	if *fecOn {
		fecEnabled = true
	} else if *fecOff {
		fecEnabled = false
	}

	opts := image.PackOptions{
		StagingDir:              cfg.StagingDir,
		Snapshots:               snapshots,
		TargetCapacitySectors:   capacitySectors,
		PhysicalCapacitySectors: physicalCapacitySectors,
		OutputDir:               absOut,
		RepoUUID:                repoUUID,
		DiscUUID:                discUUID,
		Label:                   discLabel,
		MediaType:               mediaType,
		FECEnabled:              fecEnabled,
		StageLog:                stageLog,
		Progress:                prog,
	}
	result, err := image.Pack(opts)
	if err != nil {
		if tooSmall, ok := errors.AsType[*image.ErrCapacityTooSmall](err); ok {
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: capacity %s (%d bytes) holds not one object; %s\n",
				capacityArg, capacitySectors*image.SectorSize, smallestObjectText(tooSmall))
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: use a capacity of %d bytes or more\n", tooSmall.NeededSectors*image.SectorSize)
			return 2
		}
		if exceeds, ok := errors.AsType[*image.ErrCapacityExceedsPhysical](err); ok {
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: capacity %s (%d bytes) is above the physical capacity (%d bytes)\n",
				capacityArg, exceeds.TargetSectors*image.SectorSize, exceeds.PhysicalSectors*image.SectorSize)
			return 2
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}

	if err := populateCache(cfg, repoUUID, absOut); err != nil {
		// The cache is only an accelerator: a failure to populate it
		// never fails the pack, since every command must still work
		// with the cache absent or stale.
		_, _ = fmt.Fprintln(stderr, "noahsark: pack: cache:", err)
	}

	_, _ = fmt.Fprintf(stdout, "packed disc %d %q: %d objects, %d bytes\n",
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
// the name of the newest ref this disc carries, and the disc number the
// next pack will take. The newest ref is the one whose snapshot has the
// latest commit time; a tie goes to the name that sorts first. A pack
// that carries no ref at all gets the disc number alone.
func defaultLabel(cfg repoConfig, repoUUID [16]byte, snapshots []image.SnapshotRef) (string, error) {
	ledger, err := image.LoadDiscsLedger(cfg.StagingDir, repoUUID)
	if err != nil {
		return "", err
	}
	_, discSeq := image.NextSeqNumbers(ledger.Rows)

	name := ""
	var newest int64
	for _, s := range snapshots {
		sec, err := snapshotTime(cfg.StagingDir, s.ID)
		if err != nil {
			continue
		}
		if name == "" || sec > newest || (sec == newest && s.Name < name) {
			name, newest = s.Name, sec
		}
	}
	if name == "" {
		return fmt.Sprintf("disc %d", discSeq), nil
	}
	return fmt.Sprintf("%s disc %d", name, discSeq), nil
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

// burnerDefaultDevice and burnerDefaultSpeed match burner.device and
// burner.speed's own defaults (OPERATIONS.md's configuration reference).
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
// burned too. See docs/decisions.md, "4. Staging state machine".
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
func populateCache(cfg repoConfig, repoUUID [16]byte, runRoot string) error {
	dir, err := cache.ResolveDir(repoUUID, cfg.CacheDir)
	if err != nil {
		return err
	}
	c, err := cache.Open(dir)
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
