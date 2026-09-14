package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// cmdDisc implements "noahsark disc". OPERATIONS.md's "disc list" reads
// a repository's catalog; this build has none, so it reads the local
// disc ledger (discs.bin, the same rows a DISCS table carries) and the
// staging state log instead. "disc label" and "disc mark-degraded" need
// a notes.bin this build does not keep, so both are refused rather than
// silently ignored. See docs/decisions.md, "16. CLI reference".
func cmdDisc(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark disc list [--json]")
		return 2
	}
	if args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprintln(stdout, "usage: noahsark disc list [--json]")
		return 0
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "list":
		return cmdDiscList(rest, stdout, stderr)
	case "label":
		_, _ = fmt.Fprintln(stderr, "noahsark: disc label: not in this build: the on-disc label is fixed at pack time, and notes.bin does not exist yet")
		return 2
	case "mark-degraded":
		_, _ = fmt.Fprintln(stderr, "noahsark: disc mark-degraded: not in this build: notes.bin does not exist yet")
		return 2
	default:
		_, _ = fmt.Fprintf(stderr, "noahsark: disc: unknown subcommand %q\n", sub)
		return 2
	}
}

// discSummary is one disc's row in "disc list": every run the ledger
// records for a disc_uuid, folded into a single line.
type discSummary struct {
	UUID          string `json:"uuid"`
	Seq           uint64 `json:"seq"`
	Label         string `json:"label"`
	CapacityBytes uint64 `json:"capacity_bytes"`
	UsedBytes     uint64 `json:"used_bytes"`
	Runs          int    `json:"runs"`
	PackedObjects int    `json:"packed_objects"`
}

func cmdDiscList(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark disc list [--json]", "List every disc the repository ledger knows.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	jsonOut := fs.Bool("json", false, "print discs as a JSON array")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("disc list", fs, stderr) {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark disc list [--json]")
		return 2
	}

	repoDir, err := discoverRepo(*repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc list:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc list:", err)
		return 2
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc list:", err)
		return 1
	}

	ledger, err := image.LoadDiscsLedger(cfg.StagingDir, repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc list:", err)
		return 1
	}
	stageLog, err := stage.Open(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc list:", err)
		return 1
	}
	packedByDisc := stageLog.PackedCountByDisc()

	discs := summarizeDiscs(ledger.Rows, packedByDisc)

	stagedObjects, stagedBytes, err := stagedTotals(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc list:", err)
		return 1
	}

	if *jsonOut {
		out := struct {
			Discs         []discSummary `json:"discs"`
			StagedObjects int           `json:"staged_objects"`
			StagedBytes   uint64        `json:"staged_bytes"`
		}{discs, stagedObjects, stagedBytes}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: disc list:", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(b))
		return 0
	}

	for _, d := range discs {
		_, _ = fmt.Fprintf(stdout, "%s  seq=%d  label=%q  capacity=%d  used=%d  runs=%d  objects=%d\n",
			d.UUID, d.Seq, d.Label, d.CapacityBytes, d.UsedBytes, d.Runs, d.PackedObjects)
	}
	_, _ = fmt.Fprintf(stdout, "staged: %d objects, %d bytes\n", stagedObjects, stagedBytes)
	return 0
}

// summarizeDiscs groups rows (a DISCS ledger's rows) by disc_uuid, in
// ascending disc_seq order, and folds each disc's rows into one
// discSummary: the label and forced capacity of its newest run, the sum
// of used_sectors across every run, the run count, and the disc's
// packed object count from packedByDisc.
func summarizeDiscs(rows []format.DiscsRow, packedByDisc map[[16]byte]int) []discSummary {
	order := make([]string, 0)
	byUUID := make(map[string][]format.DiscsRow)
	for _, r := range rows {
		key := uuidText(r.DiscUUID)
		if _, ok := byUUID[key]; !ok {
			order = append(order, key)
		}
		byUUID[key] = append(byUUID[key], r)
	}
	sort.Slice(order, func(i, j int) bool {
		return byUUID[order[i]][0].DiscSeq < byUUID[order[j]][0].DiscSeq
	})

	discs := make([]discSummary, 0, len(order))
	for _, key := range order {
		discRows := byUUID[key]
		newest := discRows[len(discRows)-1]
		var usedSectors uint64
		for _, r := range discRows {
			usedSectors += r.UsedSectors
		}
		discs = append(discs, discSummary{
			UUID:          key,
			Seq:           newest.DiscSeq,
			Label:         labelText(newest.Label[:newest.LabelLen]),
			CapacityBytes: newest.CapacityForcedSectors * image.SectorSize,
			UsedBytes:     usedSectors * image.SectorSize,
			Runs:          len(discRows),
			PackedObjects: packedByDisc[newest.DiscUUID],
		})
	}
	return discs
}
