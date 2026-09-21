package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
	"github.com/tjjh89017/noahsark/internal/restore"
)

// cmdPlan implements "noahsark plan". OPERATIONS.md's "14. Restore"
// resolves a restore plan from the repository's catalog and cache;
// this build reads the local cache alone, never a disc, matching
// "Computes the restore plan ... reads nothing from a disc beyond the
// catalog." SNAPSHOT accepts a snapshot id or a ref name, resolved the
// same way ls and log resolve it.
//
// The plan groups every object the restore needs by the disc that
// holds it, ordered by the tie-breaks of "14.1 The planner": most
// bytes first, then the newer disc, then the lower disc_seq. An
// object's size counts only when the disc that actually stores it is
// itself cached (cache.ObjectLocation.SizeKnown); an object known only
// through another cached disc's Prereqs table still counts toward that
// disc's object count, at zero bytes.
//
// internal/plan holds the planner itself, so "restore" can build the
// same plan and read discs in the order it names.
func cmdPlan(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark plan [--include=PATH]... [--out=FILE] [--staging-budget=SIZE] SNAPSHOT",
		"Compute a restore plan from the local cache: which discs a restore of SNAPSHOT would need, and what each holds.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	var includeFlags stringList
	fs.Var(&includeFlags, "include", "plan only this snapshot-relative path and, if it names a directory, everything under it; repeatable")
	outFile := fs.String("out", "", "write the plan as JSON to this file")
	stagingBudgetFlag := fs.String("staging-budget", "", "peak staging bytes allowed; overrides restore.staging_budget")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("plan", fs, stderr) {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark plan [--include=PATH]... [--out=FILE] [--staging-budget=SIZE] SNAPSHOT")
		return 2
	}

	repoDir, err := discoverRepo(*repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		return 1
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		return 1
	}

	stagingBudget := cfg.RestoreStagingBudget
	budgetSet := cfg.RestoreStagingBudgetSet
	if *stagingBudgetFlag != "" {
		stagingBudget, err = parseByteSize(*stagingBudgetFlag)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: plan: --staging-budget:", err)
			return 2
		}
		budgetSet = true
	}

	src, c, err := openCacheSource(*repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		return 1
	}

	snapID, err := src.ParseSnapshotArg(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		// A name matching no snapshot id and no ref is a usage error:
		// the argument itself is wrong. Every other failure here,
		// such as an empty cache with nothing rebuilt yet, is a
		// run-time condition, not a bad argument.
		if _, ok := err.(*refNotFoundError); ok || errors.Is(err, restore.ErrNoSnapshotArg) {
			return 2
		}
		return 1
	}

	if err := c.CheckComplete(snapID); err != nil {
		if ie, ok := err.(*cache.IncompleteError); ok {
			_, _ = fmt.Fprintln(stderr, formatIncompleteError("plan", ie))
			return 1
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		return 1
	}

	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		return reportSourceError("plan", stderr, err, c, snapID)
	}

	result, err := plan.Build(c, snap, snapID, includeFlags)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		return 1
	}

	if path, need, ok, err := plan.OverBudgetFile(c, snap, includeFlags, stagingBudget); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		return 1
	} else if ok {
		_, _ = fmt.Fprintf(stderr, "noahsark: plan: %s alone needs %d bytes of staging, above the staging budget of %d bytes; no split of one file's own chunks can honour it\n",
			path, need, stagingBudget)
		return 2
	}

	passSplit, err := plan.ComputePasses(result.Discs, stagingBudget)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
		return 1
	}

	printPlanText(stdout, result, passSplit, budgetSet)

	if *outFile != "" {
		repoUUID, err := decodeUUID(cfg.RepoUUID)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
			return 1
		}
		doc := buildPlanDocument(repoUUID, snapID, includeFlags, result, passSplit)
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
			return 1
		}
		if err := os.WriteFile(*outFile, append(b, '\n'), 0o644); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: plan:", err)
			return 1
		}
	}

	if len(result.Missing) > 0 {
		_, _ = fmt.Fprintf(stderr, "noahsark: plan: %d object(s) have no disc known to the cache; run recover with more discs\n", result.MissingObjectCount())
		return 1
	}
	return 0
}

// printPlanText prints one line per disc, by disc number and then by
// uuid, then the plan's totals. The operator looks a disc up by the
// number on its sleeve, so the list follows that number, not the read
// order the planner chose. The pass split and the peak staging bytes
// appear only when a staging budget was asked for: without one, a pass
// count of 1 tells the operator nothing.
func printPlanText(stdout io.Writer, r *plan.Result, ps plan.PassSplit, budgetSet bool) {
	type line struct {
		disc   plan.DiscEntry
		passes int
	}
	lines := make([]line, len(r.Discs))
	for i, d := range r.Discs {
		lines[i] = line{disc: d, passes: ps.DiscPasses[i]}
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].disc.DiscSeq != lines[j].disc.DiscSeq {
			return lines[i].disc.DiscSeq < lines[j].disc.DiscSeq
		}
		return plan.UUIDText(lines[i].disc.DiscUUID) < plan.UUIDText(lines[j].disc.DiscUUID)
	})
	for _, l := range lines {
		d := l.disc
		if budgetSet {
			_, _ = fmt.Fprintf(stdout, "disc %d %q (%s): %d objects, %d bytes, %d passes\n",
				d.DiscSeq, d.Label, plan.UUIDText(d.DiscUUID), len(d.Objects), d.Bytes, l.passes)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "disc %d %q (%s): %d objects, %d bytes\n",
			d.DiscSeq, d.Label, plan.UUIDText(d.DiscUUID), len(d.Objects), d.Bytes)
	}
	for _, m := range r.Missing {
		if !m.HasDisc {
			_, _ = fmt.Fprintf(stdout, "missing: %d object(s), disc unknown\n", m.Objects)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "missing: %d object(s) on disc %s, no cached DISCS row names it\n",
			m.Objects, plan.UUIDText(m.DiscUUID))
	}
	if budgetSet {
		_, _ = fmt.Fprintf(stdout, "totals: %d discs, %d objects, %d bytes, %d passes, peak staging %d bytes\n",
			len(r.Discs), r.TotalObjects, r.TotalBytes, ps.Total, ps.PeakBytes)
		return
	}
	_, _ = fmt.Fprintf(stdout, "totals: %d discs, %d objects, %d bytes\n",
		len(r.Discs), r.TotalObjects, r.TotalBytes)
}

// planDiscJSON is one disc of the JSON plan's discs array, the subset
// of OPERATIONS.md "14.4 The plan file"'s discs[] fields this build
// knows: order, disc_uuid, disc_seq, label, objects_to_read,
// bytes_to_read and pass, added here for the staging budget's split.
type planDiscJSON struct {
	Order         int    `json:"order"`
	DiscUUID      string `json:"disc_uuid"`
	DiscSeq       uint64 `json:"disc_seq"`
	Label         string `json:"label"`
	ObjectsToRead int    `json:"objects_to_read"`
	BytesToRead   uint64 `json:"bytes_to_read"`
	Passes        int    `json:"passes"`
}

// planMissingJSON is one missing_discs entry: the disc uuid when a
// cached INDEX names the disc that holds the object but no cached DISCS
// row describes that disc, empty when no cached INDEX names the disc at
// all.
type planMissingJSON struct {
	DiscUUID string `json:"disc_uuid"`
	Objects  int    `json:"objects"`
}

// planDocument is the JSON plan --out writes: OPERATIONS.md "14.4 The
// plan file"'s fields, as far as a cache-only, filter-less build knows
// them. RepoUUID and Created let "restore --plan" confirm a persisted
// plan still matches the repository it is resumed against.
type planDocument struct {
	Format           string            `json:"format"`
	Version          int               `json:"version"`
	RepoUUID         string            `json:"repo_uuid"`
	Created          string            `json:"created"`
	Snapshot         string            `json:"snapshot"`
	Include          []string          `json:"include,omitempty"`
	Objects          int               `json:"objects"`
	Bytes            uint64            `json:"bytes"`
	PeakStagingBytes uint64            `json:"peak_staging_bytes"`
	Switches         int               `json:"switches"`
	Passes           int               `json:"passes"`
	Discs            []planDiscJSON    `json:"discs"`
	MissingDiscs     []planMissingJSON `json:"missing_discs"`
}

func buildPlanDocument(repoUUID [16]byte, snapID object.ID, includes []string, r *plan.Result, ps plan.PassSplit) planDocument {
	discs := make([]planDiscJSON, len(r.Discs))
	for i, d := range r.Discs {
		discs[i] = planDiscJSON{
			Order:         d.Order,
			DiscUUID:      plan.UUIDText(d.DiscUUID),
			DiscSeq:       d.DiscSeq,
			Label:         d.Label,
			ObjectsToRead: len(d.Objects),
			BytesToRead:   d.Bytes,
			Passes:        ps.DiscPasses[i],
		}
	}
	missing := make([]planMissingJSON, len(r.Missing))
	for i, m := range r.Missing {
		uuid := ""
		if m.HasDisc {
			uuid = plan.UUIDText(m.DiscUUID)
		}
		missing[i] = planMissingJSON{DiscUUID: uuid, Objects: m.Objects}
	}
	return planDocument{
		Format:           "noahsark-restore-plan",
		Version:          1,
		RepoUUID:         plan.UUIDText(repoUUID),
		Created:          planClock().UTC().Format(time.RFC3339),
		Snapshot:         snapID.TextForm(),
		Include:          includes,
		Objects:          r.TotalObjects,
		Bytes:            r.TotalBytes,
		PeakStagingBytes: ps.PeakBytes,
		Switches:         len(r.Discs),
		Passes:           ps.Total,
		Discs:            discs,
		MissingDiscs:     missing,
	}
}

// planClock is the source of "created"'s timestamp. Tests may replace it
// for a deterministic value.
var planClock = time.Now
