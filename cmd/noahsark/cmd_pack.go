package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
)

// mediaTypes maps a --media name to its FORMAT.md registry value.
// media_type is informational only and never gates reading or writing.
var mediaTypes = map[string]format.MediaType{
	"BD-R-SL-25":  format.MediaTypeBDRSL25GB,
	"BD-R-DL-50":  format.MediaTypeBDRDL50GB,
	"BD-R-XL-100": format.MediaTypeBDRXL100GB,
	"BD-R-XL-128": format.MediaTypeBDRXL128GB,
}

// cmdPack implements "noahsark pack". It reduces OPERATIONS.md's pack
// flags: object selection by staging fill or age (disc.min_fill,
// disc.max_wait), locality presets and burn-plan output do not exist in
// this build, so pack instead takes the snapshot(s) to place explicitly,
// by --ref (default LATEST) or repeated --snapshot. See
// docs/decisions.md, "16. CLI reference".
func cmdPack(args []string, stdout, stderr io.Writer) int {
	if refuseLaterPhaseFlags("pack", args, stderr) {
		return 2
	}

	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repoFlag := fs.String("repo", "", "repository root")
	ref := fs.String("ref", "", "ref naming the snapshot to pack, default LATEST")
	var snapshotFlags stringList
	fs.Var(&snapshotFlags, "snapshot", "snapshot id to pack; repeatable")
	capacityStr := fs.String("capacity", "", "target capacity (sectors, or e.g. 25GB); falls back to the config default")
	label := fs.String("label", "", "human label for the disc")
	media := fs.String("media", "BD-R-SL-25", "media type name")
	outDir := fs.String("out", "", "output directory for the packed tree; default <repo>/staging/plans/1/tree")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	repoDir, err := discoverRepo(*repoFlag)
	if err != nil {
		fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 2
	}

	var capacitySectors uint64
	switch {
	case *capacityStr != "":
		capacitySectors, err = parseCapacity(*capacityStr)
		if err != nil {
			fmt.Fprintln(stderr, "noahsark: pack:", err)
			return 2
		}
	case cfg.ForceCapacitySectors != 0:
		capacitySectors = cfg.ForceCapacitySectors
	default:
		fmt.Fprintln(stderr, "noahsark: pack: target capacity is required: pass --capacity or set disc.force_capacity in the config")
		return 2
	}

	mediaType, ok := mediaTypes[*media]
	if !ok {
		fmt.Fprintf(stderr, "noahsark: pack: unknown media type %q\n", *media)
		return 2
	}

	snapIDs := []string(snapshotFlags)
	if *ref != "" && len(snapIDs) > 0 {
		fmt.Fprintln(stderr, "noahsark: pack: --ref and --snapshot are mutually exclusive")
		return 2
	}
	if *ref == "" && len(snapIDs) == 0 {
		*ref = "LATEST"
	}

	var snapshots []image.SnapshotRef
	now := time.Now()
	if *ref != "" {
		id, err := resolveRef(repoDir, *ref)
		if err != nil {
			fmt.Fprintln(stderr, "noahsark: pack:", err)
			return 1
		}
		snapshots = append(snapshots, image.SnapshotRef{Name: *ref, ID: id, Time: now})
	}
	for _, s := range snapIDs {
		id, err := parseSnapshotID(s)
		if err != nil {
			fmt.Fprintln(stderr, "noahsark: pack:", err)
			return 2
		}
		snapshots = append(snapshots, image.SnapshotRef{Name: id.TextForm(), ID: id, Time: now})
	}

	if *outDir == "" {
		*outDir = filepath.Join(repoDir, "staging", "plans", "1", "tree")
	}
	absOut, err := filepath.Abs(*outDir)
	if err != nil {
		fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	discUUIDBytes := make([]byte, 16)
	if _, err := rand.Read(discUUIDBytes); err != nil {
		fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}
	var discUUID [16]byte
	copy(discUUID[:], discUUIDBytes)

	opts := image.BuildOptions{
		StagingDir:              cfg.StagingDir,
		Snapshots:               snapshots,
		TargetCapacitySectors:   capacitySectors,
		PhysicalCapacitySectors: capacitySectors,
		OutputDir:               absOut,
		RepoUUID:                repoUUID,
		DiscUUID:                discUUID,
		Label:                   *label,
		MediaType:               mediaType,
	}
	result, err := image.Build(opts)
	if err != nil {
		fmt.Fprintln(stderr, "noahsark: pack:", err)
		return 1
	}

	fmt.Fprintf(stdout, "packed run %d on disc %d into %s\n", result.RunSeq, result.DiscSeq, absOut)
	fmt.Fprintf(stdout, "objects: %d, files: %d, stream blocks: %d, stripes: %d\n",
		result.ObjectCount, result.FileCount, result.StreamBlocks, result.StripeCount)
	return 0
}

// stringList implements flag.Value for a repeatable flag.
type stringList []string

func (s *stringList) String() string { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
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
