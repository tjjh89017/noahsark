package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

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
//
// "disc burned" is not an OPERATIONS.md command; it is this build's
// explicit stand-in for the missing burn step (see docs/decisions.md,
// "4. Staging state machine"): the operator burns with growisofs by
// hand, and running it is how the staging state machine learns a disc
// really was burned, since no on-disc structure records that moment
// and this build has no `burn` command to record it automatically.
func cmdDisc(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark disc list [--json] | disc burned [--undo] DISC [DISC...]")
		return 2
	}
	if args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprintln(stdout, "usage: noahsark disc list [--json] | disc burned [--undo] DISC [DISC...]")
		return 0
	}
	sub := args[0]
	rest := args[1:]

	if strings.HasPrefix(sub, "-") {
		_, _ = fmt.Fprintf(stderr, "noahsark: disc: flags come after the subcommand: noahsark disc list %s\n", sub)
		return 2
	}

	switch sub {
	case "list":
		return cmdDiscList(rest, stdout, stderr)
	case "burned":
		return cmdDiscBurned(rest, stdout, stderr)
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

// cmdDiscBurned implements "noahsark disc burned [--undo] DISC [DISC...]".
// It moves every PACKED object of each named disc's runs to BURNED,
// standing in for the missing `burn` command: the operator runs it
// right after burning both twins by hand. --undo reverses that, for a
// burn that turned out bad, moving BURNED objects back to PACKED with
// the burn-failed reason.
func cmdDiscBurned(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark disc burned [--undo] DISC [DISC...]",
		"Mark a disc burned, moving its PACKED objects to BURNED.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	undo := fs.Bool("undo", false, "undo: move BURNED objects back to PACKED, for a burn that turned out bad")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("disc burned", fs, stderr) {
		return 2
	}
	if fs.NArg() == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark disc burned [--undo] DISC [DISC...]")
		return 2
	}

	repoDir, err := discoverRepo(*repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
		return 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
		return 2
	}
	lk, code, ok := lockExclusive("disc burned", repoDir, cfg.LockTimeout, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
		return 1
	}
	ledger, err := image.LoadDiscsLedger(cfg.StagingDir, repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
		return 1
	}
	stageLog, err := stage.Open(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
		return 1
	}
	warnIfTruncated("disc burned", stageLog, stderr)

	for _, arg := range fs.Args() {
		discUUID, err := resolveDiscArg(ledger.Rows, arg)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
			return 2
		}
		rows := discRowsForUUID(ledger.Rows, discUUID)

		if *undo {
			if n := countInStateAcrossRuns(stageLog, stage.Clean, discUUID, rows); n > 0 {
				_, _ = fmt.Fprintf(stderr, "noahsark: disc burned: disc %s is verified (CLEAN) and cannot be returned to packed\n", uuidText(discUUID))
				return 1
			}
		}

		for _, row := range rows {
			label := labelText(row.Label[:row.LabelLen])
			if *undo {
				n := undoBurnForRun(stageLog, discUUID, row.RunSeq)
				_, _ = fmt.Fprintf(stdout, "disc %d %s: undo: returned to packed, %d objects\n",
					row.DiscSeq, label, n)
				continue
			}
			n := markBurnedForRun(stageLog, discUUID, row.RunSeq)
			if n == 0 {
				_, _ = fmt.Fprintf(stdout, "disc %d %s: already burned, 0 objects to mark\n", row.DiscSeq, label)
				continue
			}
			_, _ = fmt.Fprintf(stdout, "disc %d %s: marked burned, %d objects\n", row.DiscSeq, label, n)
		}
		if !*undo {
			if err := stageLog.RecordBurnTime(discUUID); err != nil {
				_, _ = fmt.Fprintln(stderr, "noahsark: disc burned:", err)
				return 1
			}
		}
	}
	return 0
}

// discRowsForUUID returns every ledger row for discUUID.
func discRowsForUUID(rows []format.DiscsRow, discUUID [16]byte) []format.DiscsRow {
	var out []format.DiscsRow
	for _, r := range rows {
		if r.DiscUUID == discUUID {
			out = append(out, r)
		}
	}
	return out
}

// markBurnedForRun moves every object of run runSeq on disc discUUID
// that is at PACKED to BURNED, and reports how many objects it moved.
func markBurnedForRun(l *stage.Log, discUUID [16]byte, runSeq uint64) int {
	n := 0
	for _, id := range l.IDsInState(stage.Packed) {
		rec, ok := l.Get(id)
		if !ok || rec.RunSeq != runSeq || rec.DiscUUID != discUUID {
			continue
		}
		if err := l.MarkBurned(id, runSeq, discUUID); err == nil {
			n++
		}
	}
	return n
}

// undoBurnForRun moves every object of run runSeq on disc discUUID that
// is at BURNED back to PACKED, and reports how many objects it moved.
func undoBurnForRun(l *stage.Log, discUUID [16]byte, runSeq uint64) int {
	n := 0
	for _, id := range l.IDsInState(stage.Burned) {
		rec, ok := l.Get(id)
		if !ok || rec.RunSeq != runSeq || rec.DiscUUID != discUUID {
			continue
		}
		if err := l.MarkBurnUndone(id); err == nil {
			n++
		}
	}
	return n
}

// countInStateAcrossRuns sums countInState over every one of rows' runs.
func countInStateAcrossRuns(l *stage.Log, state stage.State, discUUID [16]byte, rows []format.DiscsRow) int {
	n := 0
	for _, row := range rows {
		n += countInState(l, state, discUUID, row.RunSeq)
	}
	return n
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
	OnDiscObjects int    `json:"on_disc_objects"`
	PackedObjects int    `json:"packed_objects"`
	CleanObjects  int    `json:"clean_objects"`
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

	lk, code, ok := lockShared("disc list", repoDir, cfg.LockTimeout, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

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
	warnIfTruncated("disc list", stageLog, stderr)
	onDiscByDisc := stageLog.OnDiscCountByDisc()
	packedByDisc := stageLog.PackedCountByDisc()
	cleanByDisc := stageLog.CleanCountByDisc()

	discs := summarizeDiscs(ledger.Rows, onDiscByDisc, packedByDisc, cleanByDisc)

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
		_, _ = fmt.Fprintf(stdout, "%s  seq=%d  label=%q  capacity=%d  used=%d  runs=%d  objects=%d  packed=%d  clean=%d\n",
			d.UUID, d.Seq, d.Label, d.CapacityBytes, d.UsedBytes, d.Runs, d.OnDiscObjects, d.PackedObjects, d.CleanObjects)
	}
	_, _ = fmt.Fprintf(stdout, "staged: %d objects, %d bytes\n", stagedObjects, stagedBytes)
	return 0
}

// summarizeDiscs groups rows (a DISCS ledger's rows) by disc_uuid, in
// ascending disc_seq order, and folds each disc's rows into one
// discSummary: the label and forced capacity of its newest run, the sum
// of used_sectors across every run, the run count, and the disc's
// on-disc, packed, and clean object counts.
func summarizeDiscs(rows []format.DiscsRow, onDiscByDisc, packedByDisc, cleanByDisc map[[16]byte]int) []discSummary {
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
			OnDiscObjects: onDiscByDisc[newest.DiscUUID],
			PackedObjects: packedByDisc[newest.DiscUUID],
			CleanObjects:  cleanByDisc[newest.DiscUUID],
		})
	}
	return discs
}
