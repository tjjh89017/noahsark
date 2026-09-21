package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/restore"
)

// cmdLog implements "noahsark log". With no DISC-ROOT, it resolves
// REF|SNAPSHOT, and lists every known snapshot with none given, through
// the local cache, so log needs no disc present; give one or more
// DISC-ROOT positionals to read straight from a disc instead, the same
// way ls, restore and verify do.
func cmdLog(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark log [DISC-ROOT...] [REF|SNAPSHOT]",
		"Print a snapshot's history. Resolves through the local cache with no disc given; accepts one or more DISC-ROOT positionals to read a disc instead.", stderr)
	repoFlag := fs.String("repo", "", "repository root, for the cache; used only with no disc given")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("log", fs, stderr) {
		return 2
	}

	// Leading positional arguments that name an existing directory are
	// DISC-ROOTs; REF|SNAPSHOT is never a path that already exists on
	// this host, so every argument can be checked the same way, and all
	// of them may be DISC-ROOTs, leaving REF|SNAPSHOT unset (list-all).
	i := 0
	for i < fs.NArg() && looksLikeDiscRoot(fs.Arg(i)) {
		i++
	}
	discRootArgs := fs.Args()[:i]
	discRootGiven := len(discRootArgs) > 0
	if !discRootGiven && fs.NArg() > 0 && looksLikePathNotDisc(fs.Arg(0)) {
		_, _ = fmt.Fprintf(stderr, "noahsark: log: no such disc root: %s\n", fs.Arg(0))
		return 2
	}
	cacheMode := !discRootGiven

	var src snapshotSource
	var cacheObj *cache.Cache
	var positional []string
	switch {
	case cacheMode:
		if fs.NArg() > 1 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark log [REF|SNAPSHOT]")
			return 2
		}
		positional = fs.Args()
		cs, c, err := openCacheSource(*repoFlag)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
			return 1
		}
		src, cacheObj = cs, c
	default:
		positional = fs.Args()[len(discRootArgs):]
		if len(positional) > 1 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark log DISC-ROOT... [REF|SNAPSHOT]")
			return 2
		}
	}

	if !cacheMode {
		restoreSrc, err := restore.OpenSource(discRootArgs)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
			return 1
		}
		src = restoreSrc
	}

	if len(positional) == 1 {
		return logOne(src, cacheObj, positional[0], stdout, stderr)
	}
	return logAll(src, stdout, stderr)
}

// logRecord is one snapshot's history line: its id, time, the ref names
// pointing at it, the root paths it covers, and the counts the snapshot
// itself stores.
type logRecord struct {
	ID        string
	Parent    string
	Time      string
	Refs      []string
	Roots     []string
	TotalSize uint64

	// timeNanos is the snapshot's own time, at the full precision the
	// snapshot record carries: Time above is already the printed form,
	// truncated to whole seconds. logAll's sort uses this so two
	// snapshots committed in the same second, which tie on the printed
	// Time, still order newest first when the record's own nanoseconds
	// tell them apart.
	timeNanos int64
}

// logAll lists every snapshot the provided discs know, newest first.
func logAll(src snapshotSource, stdout, stderr io.Writer) int {
	ids, err := src.SnapshotIDs()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
		return 1
	}
	refs, err := src.Refs()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
		return 1
	}

	records := make([]logRecord, 0, len(ids))
	for _, id := range ids {
		snap, err := src.Snapshot(id)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
			return 1
		}
		records = append(records, buildLogRecord(src, id, snap, refs))
	}
	// The printed Time has only one-second resolution, so two snapshots
	// committed in the same second tie on it; sort by the record's own
	// full-precision time instead, so that tie is already broken by
	// real recency rather than only by display rounding. A further tie
	// there (the same nanosecond, or this build's constant generation 1)
	// falls back to generation descending, then snapshot id ascending,
	// the same order FORMAT.md uses for a run's snapshots, so the
	// result is stable across runs of log instead of depending on
	// sort.Slice's own unstable ordering of equal elements.
	sort.Slice(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.timeNanos != b.timeNanos {
			return a.timeNanos > b.timeNanos
		}
		return a.ID < b.ID
	})

	for _, r := range records {
		_, _ = fmt.Fprintf(stdout, "%s  %s  refs: %s  roots: %s  size: %d\n",
			r.ID, r.Time, joinOrNone(r.Refs), joinOrNone(r.Roots), r.TotalSize)
	}
	printRefsOnAnotherDisc(stdout, ids, refs)
	return 0
}

// printRefsOnAnotherDisc lists every ref whose own snapshot object is
// not among ids: gc can free an old run's staged snapshot object once it
// is no longer needed there, so a later pack stops carrying that
// snapshot object forward, while REFS.bin still carries the ref itself
// forward on every run. Such a ref must still appear in log, naming the
// disc it needs instead of vanishing from the list.
func printRefsOnAnotherDisc(stdout io.Writer, ids []object.ID, refs *format.RefsTable) {
	known := make(map[object.ID]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	var elsewhere []format.RefRecord
	for _, rec := range refs.Records {
		if !known[object.ID(rec.SnapshotID)] {
			elsewhere = append(elsewhere, rec)
		}
	}
	sort.Slice(elsewhere, func(i, j int) bool {
		return string(elsewhere[i].Name[:elsewhere[i].NameLen]) < string(elsewhere[j].Name[:elsewhere[j].NameLen])
	})
	for _, rec := range elsewhere {
		_, _ = fmt.Fprintf(stdout, "%s  refs: %s  on another disc\n",
			object.ID(rec.SnapshotID).TextForm(), string(rec.Name[:rec.NameLen]))
	}
}

// logOne prints one snapshot's own details.
func logOne(src snapshotSource, cacheObj *cache.Cache, arg string, stdout, stderr io.Writer) int {
	id, err := src.ParseSnapshotArg(arg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
		return exitForSnapshotArg(err)
	}
	snap, err := src.Snapshot(id)
	if err != nil {
		return reportSourceError("log", stderr, err, cacheObj, id)
	}
	refs, err := src.Refs()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
		return 1
	}
	r := buildLogRecord(src, id, snap, refs)

	_, _ = fmt.Fprintf(stdout, "snapshot %s\n", r.ID)
	parent := r.Parent
	if parent == "" {
		parent = "(none)"
	}
	_, _ = fmt.Fprintf(stdout, "parent: %s\n", parent)
	_, _ = fmt.Fprintf(stdout, "time: %s\n", r.Time)
	_, _ = fmt.Fprintf(stdout, "refs: %s\n", joinOrNone(r.Refs))
	_, _ = fmt.Fprintf(stdout, "root paths: %s\n", joinOrNone(r.Roots))
	_, _ = fmt.Fprintf(stdout, "total size: %d\n", r.TotalSize)
	return 0
}

// buildLogRecord gathers one snapshot's log fields: the refs naming it
// from refs, and the root paths from its own root tree. A root tree that
// no provided disc holds leaves Roots empty rather than failing the whole
// command; log has no exit code for a missing disc, unlike ls.
func buildLogRecord(src snapshotSource, id object.ID, snap *format.Snapshot, refs *format.RefsTable) logRecord {
	r := logRecord{
		ID:        id.TextForm(),
		Time:      time.Unix(snap.TimeSec, int64(snap.TimeNsec)).UTC().Format(time.RFC3339),
		TotalSize: snap.TotalSize,
		timeNanos: snap.TimeSec*int64(time.Second) + int64(snap.TimeNsec),
	}
	if snap.Parent != ([32]byte{}) {
		r.Parent = object.ID(snap.Parent).TextForm()
	}
	for _, rec := range refs.Records {
		if object.ID(rec.SnapshotID) == id {
			r.Refs = append(r.Refs, string(rec.Name[:rec.NameLen]))
		}
	}
	sort.Strings(r.Refs)
	if tree, err := src.Tree(object.ID(snap.RootTree)); err == nil {
		for _, e := range tree.Entries {
			if segs := splitLsPath(rootPathOf(e)); len(segs) > 0 {
				r.Roots = append(r.Roots, strings.Join(segs, "/"))
			}
		}
	}
	return r
}

// joinOrNone joins items with ", ", or reports "(none)" for an empty
// list, so a text listing never prints a bare empty field.
func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ", ")
}
