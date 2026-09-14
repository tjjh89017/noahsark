package main

import (
	"encoding/json"
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

// cmdLog implements "noahsark log". With no DISC-ROOT, --disc or
// --discs-dir, it resolves REF|SNAPSHOT, and lists every known
// snapshot with none given, through the local cache, so log needs no
// disc present; give a disc root, --disc or --discs-dir to read
// straight from a disc instead, the same way ls, restore and verify
// do.
func cmdLog(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark log [DISC-ROOT] [REF|SNAPSHOT] [--limit=N] [--json]",
		"Print a snapshot's history. Resolves through the local cache with no disc given; accepts --disc (repeatable), --discs-dir or a DISC-ROOT positional to read a disc instead.", stderr)
	repoFlag := fs.String("repo", "", "repository root, for the cache; used only with no disc given")
	var discFlags stringList
	fs.Var(&discFlags, "disc", "a disc root to read from; repeatable")
	discsDir := fs.String("discs-dir", "", "a directory whose immediate subdirectories are mounted disc roots")
	limit := fs.Int("limit", 0, "print at most this many entries; 0 means no limit")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("log", fs, stderr) {
		return 2
	}

	multi := len(discFlags) > 0 || *discsDir != ""
	discRootGiven := !multi && fs.NArg() > 0 && looksLikeDiscRoot(fs.Arg(0))
	if !multi && !discRootGiven && fs.NArg() > 0 && looksLikePathNotDisc(fs.Arg(0)) {
		_, _ = fmt.Fprintf(stderr, "noahsark: log: no such disc root: %s\n", fs.Arg(0))
		return 2
	}
	cacheMode := !multi && !discRootGiven

	var src snapshotSource
	var cacheObj *cache.Cache
	var positional []string
	switch {
	case cacheMode:
		if fs.NArg() > 1 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark log [REF|SNAPSHOT] [--limit=N] [--json]")
			return 2
		}
		positional = fs.Args()
		cs, c, err := openCacheSource(*repoFlag)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
			return 1
		}
		src, cacheObj = cs, c
	case multi:
		if fs.NArg() > 1 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark log --disc=ROOT [--disc=ROOT]... [REF|SNAPSHOT] [--limit=N] [--json]")
			return 2
		}
		positional = fs.Args()
	default:
		if fs.NArg() < 1 || fs.NArg() > 2 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark log DISC-ROOT [REF|SNAPSHOT] [--limit=N] [--json]")
			return 2
		}
		positional = fs.Args()[1:]
	}

	if !cacheMode {
		discRoots, err := resolveDiscRoots(discFlags, *discsDir, fs.Args())
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
			return 2
		}
		restoreSrc, err := restore.OpenSource(discRoots)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
			return 1
		}
		src = restoreSrc
	}

	if len(positional) == 1 {
		return logOne(src, cacheObj, positional[0], *jsonOut, stdout, stderr)
	}
	return logAll(src, *limit, *jsonOut, stdout, stderr)
}

// logRecord is one snapshot's history line: its id, time, the ref names
// pointing at it, the root paths it covers, and the counts the snapshot
// itself stores.
type logRecord struct {
	ID               string   `json:"id"`
	Parent           string   `json:"parent,omitempty"`
	Generation       uint64   `json:"generation"`
	Time             string   `json:"time"`
	Refs             []string `json:"refs"`
	Roots            []string `json:"roots"`
	ReachableObjects uint64   `json:"reachable_objects"`
	TotalSize        uint64   `json:"total_size"`
}

// logAll lists every snapshot the provided discs know, newest first,
// limited to limit entries when limit is greater than zero.
func logAll(src snapshotSource, limit int, jsonOut bool, stdout, stderr io.Writer) int {
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
	sort.Slice(records, func(i, j int) bool { return records[i].Time > records[j].Time })
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}

	if jsonOut {
		b, err := json.MarshalIndent(records, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, r := range records {
		_, _ = fmt.Fprintf(stdout, "%s  %s  refs: %s  roots: %s  objects: %d  size: %d\n",
			r.ID, r.Time, joinOrNone(r.Refs), joinOrNone(r.Roots), r.ReachableObjects, r.TotalSize)
	}
	return 0
}

// logOne prints one snapshot's own details.
func logOne(src snapshotSource, cacheObj *cache.Cache, arg string, jsonOut bool, stdout, stderr io.Writer) int {
	id, err := src.ParseSnapshotArg(arg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
		return 2
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

	if jsonOut {
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: log:", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(b))
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "snapshot %s\n", r.ID)
	parent := r.Parent
	if parent == "" {
		parent = "(none)"
	}
	_, _ = fmt.Fprintf(stdout, "parent: %s\n", parent)
	_, _ = fmt.Fprintf(stdout, "generation: %d\n", r.Generation)
	_, _ = fmt.Fprintf(stdout, "time: %s\n", r.Time)
	_, _ = fmt.Fprintf(stdout, "refs: %s\n", joinOrNone(r.Refs))
	_, _ = fmt.Fprintf(stdout, "root paths: %s\n", joinOrNone(r.Roots))
	_, _ = fmt.Fprintf(stdout, "reachable objects: %d\n", r.ReachableObjects)
	_, _ = fmt.Fprintf(stdout, "total size: %d\n", r.TotalSize)
	return 0
}

// buildLogRecord gathers one snapshot's log fields: the refs naming it
// from refs, and the root paths from its own root tree. A root tree that
// no provided disc holds leaves Roots empty rather than failing the whole
// command; log has no exit code for a missing disc, unlike ls.
func buildLogRecord(src snapshotSource, id object.ID, snap *format.Snapshot, refs *format.RefsTable) logRecord {
	r := logRecord{
		ID:               id.TextForm(),
		Generation:       snap.Generation,
		Time:             time.Unix(snap.TimeSec, int64(snap.TimeNsec)).UTC().Format(time.RFC3339),
		ReachableObjects: snap.ReachableObjectCount,
		TotalSize:        snap.TotalSize,
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
