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

// mediaTypes maps a --media name to its FORMAT.md registry value.
// media_type is informational only and never gates reading or writing.
var mediaTypes = map[string]format.MediaType{
	"BD-R-SL-25":  format.MediaTypeBDRSL25GB,
	"BD-R-DL-50":  format.MediaTypeBDRDL50GB,
	"BD-R-XL-100": format.MediaTypeBDRXL100GB,
	"BD-R-XL-128": format.MediaTypeBDRXL128GB,
	"DVD+R-SL":    format.MediaTypeDVDPlusRSL,
	"DVD-R-SL":    format.MediaTypeDVDMinusRSL,
}

// mediaPresetAliases maps a --capacity preset name to the media type a
// pack built with that preset should record. --media accepts the same
// names, case insensitive.
var mediaPresetAliases = map[string]format.MediaType{
	"bd25":  format.MediaTypeBDRSL25GB,
	"bd50":  format.MediaTypeBDRDL50GB,
	"bd100": format.MediaTypeBDRXL100GB,
	"bd128": format.MediaTypeBDRXL128GB,
	"dvd+r": format.MediaTypeDVDPlusRSL,
	"dvd-r": format.MediaTypeDVDMinusRSL,
}

// resolveMediaType picks the media type a pack records. An explicit
// --media value is matched first against the registry names of
// mediaTypes, then against a capacity preset name (case insensitive).
// With no --media, the media type is derived from the matching
// --capacity preset; media_type is informational (FORMAT.md's media
// type registry), so every preset, BD or DVD, has an entry and none is
// ever refused on that basis.
func resolveMediaType(mediaGiven bool, media, capacityStr string) (format.MediaType, error) {
	if mediaGiven {
		if mt, ok := mediaTypes[strings.ToUpper(media)]; ok {
			return mt, nil
		}
		if mt, ok := mediaPresetAliases[strings.ToLower(media)]; ok {
			return mt, nil
		}
		return 0, fmt.Errorf("unknown media type %q", media)
	}
	if mt, ok := mediaPresetAliases[strings.ToLower(capacityStr)]; ok {
		return mt, nil
	}
	return mediaTypes["BD-R-SL-25"], nil
}

// cmdPack implements "noahsark pack". It reduces OPERATIONS.md's pack
// flags: object selection by staging fill or age (disc.min_fill,
// disc.max_wait), locality presets and burn-plan output do not exist in
// this build, so pack instead takes the snapshot(s) to place explicitly,
// by --ref or repeated --snapshot; with neither given, it carries
// forward every pending ref, falling back to LATEST only when that
// leaves nothing. See docs/decisions.md, "16. CLI reference".
func cmdPack(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if refuseLaterPhaseFlags("pack", args, stderr) {
		return 2
	}
	if refuseNotYetImplementedFlags("pack", args, stderr) {
		return 2
	}

	fs := newFlagSet("noahsark pack [--ref=NAME | --snapshot=ID]... --capacity=N [--physical-capacity=N] [--label=TEXT] [--media=NAME] [--out=DIR] [--fec | --no-fec] [--close]",
		"Pack staged objects into the next run.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	ref := fs.String("ref", "", "extra ref name to carry onto the disc; every pending ref is carried regardless")
	var snapshotFlags stringList
	fs.Var(&snapshotFlags, "snapshot", "snapshot id to pack; repeatable")
	capacityStr := fs.String("capacity", "", "target capacity ("+capacityHelpText()+"); required")
	physicalCapacityStr := fs.String("physical-capacity", "", "the disc's physical capacity (sectors, a preset, or a byte size); defaults to --capacity, so this only needs setting when the target is a forced, smaller limit")
	label := fs.String("label", "", "human label for the disc")
	media := fs.String("media", "", "media type: a FORMAT.md registry name (e.g. BD-R-SL-25), or a --capacity preset name (bd25, bd50, bd100, bd128, dvd+r, dvd-r); default derived from --capacity, else BD-R-SL-25")
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

	if *capacityStr == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack: target capacity is required: pass --capacity")
		return 2
	}
	capacitySectors, err := parseCapacity(*capacityStr)
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

	mediaType, err := resolveMediaType(*media != "", *media, *capacityStr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 2
	}

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
		Label:                   *label,
		MediaType:               mediaType,
		FECEnabled:              fecEnabled,
		StageLog:                stageLog,
		Progress:                prog,
	}
	result, err := image.Pack(opts)
	if err != nil {
		if tooSmall, ok := errors.AsType[*image.ErrCapacityTooSmall](err); ok {
			_, _ = fmt.Fprintf(stderr, "noahsark: pack: --capacity=%s (%d bytes, %d sectors) is too small; this run needs at least %d sectors (%d bytes)\n",
				*capacityStr, capacitySectors*image.SectorSize, capacitySectors,
				tooSmall.NeededSectors, tooSmall.NeededSectors*image.SectorSize)
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

	if fecEnabled {
		_, _ = fmt.Fprintf(stdout, "fec: on, budget used: %d stream blocks across %d stripes\n", result.StreamBlocks, result.StripeCount)
	} else {
		_, _ = fmt.Fprintf(stdout, "fec: off, budget used: %d stream blocks\n", result.StreamBlocks)
	}
	_, _ = fmt.Fprintf(stdout, "packed run %d on disc %d into %s\n", result.RunSeq, result.DiscSeq, absOut)
	_, _ = fmt.Fprintf(stdout, "objects: %d, files: %d, stream blocks: %d, stripes: %d\n",
		result.ObjectCount, result.FileCount, result.StreamBlocks, result.StripeCount)

	imageCapacityArg := *capacityStr
	if imageCapacityArg == "" {
		imageCapacityArg = fmt.Sprintf("%d", capacitySectors)
	}
	printNextSteps(stdout, repoDir, absOut, imageCapacityArg, uuidText(discUUID), *closeDisc)

	if result.RemainingObjects > 0 {
		_, _ = fmt.Fprintf(stdout, "remaining staged: %d objects, %d bytes\n", result.RemainingObjects, result.RemainingBytes)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "remaining staged: 0 objects, 0 bytes")
	return 0
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
// rather than run.
//
// The burn line follows FORMAT.md's and OPERATIONS.md's open-by-default
// rule: spare:min and no -dvd-compat, unless close is true, which is the
// only way this build ever prints -dvd-compat or spare:none.
//
// `disc burned` comes before `verify`: verify never moves an object
// from PACKED to BURNED itself, since a loop-mounted image checked
// before burning has the same disc uuid and would otherwise look
// burned too. See docs/decisions.md, "4. Staging state machine".
func printNextSteps(stdout io.Writer, repoDir, treeDir, capacityArg, discUUID string, sealDisc bool) {
	imagePath := treeDir + ".img"
	spareMode := "spare:min"
	dvdCompat := ""
	if sealDisc {
		spareMode = "spare:none"
		dvdCompat = "-dvd-compat "
	}
	_, _ = fmt.Fprintln(stdout, "next steps:")
	_, _ = fmt.Fprintf(stdout, "  sudo noahsark image build --out=%s --capacity=%s %s\n", imagePath, capacityArg, treeDir)
	_, _ = fmt.Fprintf(stdout, "  growisofs -speed=%d -use-the-force-luke=%s,tty %s-Z %s=%s\n",
		burnerDefaultSpeed, spareMode, dvdCompat, burnerDefaultDevice, imagePath)
	_, _ = fmt.Fprintf(stdout, "  noahsark disc burned --repo=%s %s\n", repoDir, discUUID)
	_, _ = fmt.Fprintf(stdout, "  noahsark verify --repo=%s --image=<mount point>\n", repoDir)
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
