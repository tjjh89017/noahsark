package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// cmdRebuildCache implements "noahsark rebuild-cache". This build keeps
// no local cache and no catalog: the repository directory holds only the
// state log, the disc ledger and the refs, and every one of those is
// exactly what a disc's own INDEX, DISCS and REFS tables already carry.
// So level 1, the only level this build supports, is a straight replay
// of every provided disc's tables into a fresh or existing repository
// directory. See docs/decisions.md, "16. CLI reference" and
// "2.5 Cache rebuild levels".
func cmdRebuildCache(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	fs := newFlagSet("noahsark rebuild-cache --from-disc DISC-ROOT... [--level=1] [--snapshot=ID]",
		"Rebuild the local repository state from one or more discs.", stderr)
	repoFlag := fs.String("repo", "", "repository directory to create or use")
	level := fs.Int("level", 1, "cache rebuild level: 1, 2 or 3")
	snapshot := fs.String("snapshot", "", "snapshot level 2 would cover; accepted and unused, since level 1 rebuilds every object regardless")
	fromDisc := fs.Bool("from-disc", false, "read from disc; required, since this build keeps no cache to check freshness against")
	var discFlags stringList
	fs.Var(&discFlags, "disc", "a disc root to rebuild from; repeatable")
	discsDir := fs.String("discs-dir", "", "a directory whose immediate subdirectories are mounted disc roots")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("rebuild-cache", fs, stderr) {
		return 2
	}

	if *level != 1 {
		_, _ = fmt.Fprintf(stderr, "noahsark: rebuild-cache: --level=%d is not available: this build keeps no object cache, only the repository state log, the disc ledger and the refs, and rebuilding those needs nothing past level 1\n", *level)
		return 2
	}
	if !*fromDisc {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache: --from-disc is required: this build keeps no cache, so rebuilding always reads discs")
		return 2
	}
	if *snapshot != "" {
		if _, err := parseSnapshotID(*snapshot); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache: --snapshot:", err)
			return 2
		}
	}

	discRoots, err := resolveDiscRoots(discFlags, *discsDir, fs.Args())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 2
	}

	repoDir, err := rebuildTargetRepoDir(*repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 2
	}

	// A disc root that fails to read (not mounted, not a NoahsArk tree,
	// damaged beyond verify) is skipped, not fatal: rebuild-cache still
	// uses whatever discs it can read, and only refuses outright when
	// none of them yielded anything.
	var results []*image.ReadResult
	for _, root := range discRoots {
		rr, err := image.ReadWithProgress(root, prog)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: rebuild-cache: %s: %v\n", root, err)
			continue
		}
		results = append(results, rr)
	}
	if len(results) == 0 {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache: no usable disc found")
		return 3
	}

	repoUUID := results[0].Run.RepoUUID
	for _, rr := range results {
		if rr.Run.RepoUUID != repoUUID {
			_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache: the provided discs do not share one repo_uuid")
			return 1
		}
	}

	cfg, err := ensureRebuildRepo(repoDir, repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 1
	}

	stageLog, err := stage.Open(cfg.StagingDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 1
	}

	var packedObjects int
	for _, rr := range results {
		for _, row := range rr.Index.Objects {
			id := object.ID(row.ContentID)
			if err := stageLog.EnsurePacked(id, rr.Run.RunSeq, rr.Disc.DiscUUID); err != nil {
				_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
				return 1
			}
			packedObjects++
		}
	}

	discRows, missing := mergeDiscsRows(results)
	discRows = fillUsedSectorsFromRuns(discRows, results)
	if err := image.SaveDiscsLedger(cfg.StagingDir, repoUUID, discRows); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 1
	}

	refRecords := bestRefRecords(results)
	if err := image.SaveRefsLedger(cfg.StagingDir, repoUUID, refRecords); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 1
	}
	refs := mergeRefs(refRecords)
	if err := writeRefs(repoDir, refs); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "rebuild-cache: level 1, %d disc(s) read, repo %s\n", len(results), repoDir)
	_, _ = fmt.Fprintf(stdout, "objects recorded packed: %d\n", packedObjects)
	_, _ = fmt.Fprintf(stdout, "discs known: %d, refs restored: %d\n", len(discRows), len(refs))

	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for u := range missing {
			names = append(names, uuidText(u))
		}
		sort.Strings(names)
		_, _ = fmt.Fprintf(stdout, "rebuild is partial: %d disc(s) named in DISCS were not provided: %s\n", len(names), strings.Join(names, ", "))
		return 1
	}

	_, _ = fmt.Fprintln(stdout, "rebuild-cache: ok")
	return 0
}

// rebuildTargetRepoDir resolves the repository directory rebuild-cache
// should use, whether or not it exists yet: explicitRepo when set, else
// NOAHSARK_REPO, else whatever discoverRepo's ancestor search finds. A
// missing repository is not an error here; ensureRebuildRepo creates it.
func rebuildTargetRepoDir(explicitRepo string) (string, error) {
	if explicitRepo != "" {
		return filepath.Abs(explicitRepo)
	}
	if env := os.Getenv("NOAHSARK_REPO"); env != "" {
		return filepath.Abs(env)
	}
	dir, err := discoverRepo("")
	if err == nil {
		return dir, nil
	}
	return "", fmt.Errorf("no repository directory given: pass --repo, or set NOAHSARK_REPO, to say where to rebuild one")
}

// ensureRebuildRepo loads repoDir's config when it is already a
// repository, or creates a fresh one with repoUUID otherwise: the config
// file, an empty staging store, and nothing else, matching cmdInit's
// layout. An existing config's repo.uuid must match repoUUID.
func ensureRebuildRepo(repoDir string, repoUUID [16]byte) (repoConfig, error) {
	if isRepoDir(repoDir) {
		cfg, err := readConfig(configPath(repoDir))
		if err != nil {
			return repoConfig{}, err
		}
		existing, err := decodeUUID(cfg.RepoUUID)
		if err != nil {
			return repoConfig{}, err
		}
		if existing != repoUUID {
			return repoConfig{}, fmt.Errorf("repository %s has repo.uuid %s, the discs carry %s", repoDir, cfg.RepoUUID, hex.EncodeToString(repoUUID[:]))
		}
		return cfg, nil
	}

	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return repoConfig{}, err
	}
	stagingDir := filepath.Join(repoDir, "staging")
	if err := os.MkdirAll(filepath.Join(stagingDir, "objects"), 0o755); err != nil {
		return repoConfig{}, err
	}
	if err := os.MkdirAll(filepath.Join(stagingDir, "snapshots"), 0o755); err != nil {
		return repoConfig{}, err
	}
	cfg := repoConfig{RepoUUID: hex.EncodeToString(repoUUID[:]), StagingDir: stagingDir}
	if err := writeConfig(configPath(repoDir), cfg); err != nil {
		return repoConfig{}, err
	}
	return cfg, nil
}

// mergeDiscsRows unions every provided disc's DISCS rows, keyed by
// DiscUUID (a run burns exactly one disc in this build, so DiscUUID and
// RunSeq name the same row), sorted by RunSeq ascending so a later Pack
// call appends after them in the same order it would have burned them
// in. It also reports which of those rows name a disc that was not
// actually provided: a rebuild that finds any is partial.
func mergeDiscsRows(results []*image.ReadResult) (rows []format.DiscsRow, missing map[[16]byte]bool) {
	byUUID := make(map[[16]byte]format.DiscsRow)
	provided := make(map[[16]byte]bool, len(results))
	for _, rr := range results {
		provided[rr.Disc.DiscUUID] = true
		for _, row := range rr.Discs.Rows {
			byUUID[row.DiscUUID] = row
		}
	}
	rows = make([]format.DiscsRow, 0, len(byUUID))
	missing = make(map[[16]byte]bool)
	for u, row := range byUUID {
		rows = append(rows, row)
		if !provided[u] {
			missing[u] = true
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].RunSeq < rows[j].RunSeq })
	return rows, missing
}

// fillUsedSectorsFromRuns fills in used_sectors for a provided disc's
// own row. A disc's own DISCS.bin always carries used_sectors 0 for
// itself, since that run's final size is not known until after it is
// written (docs/decisions.md, "12. Disc lifecycle, closing and
// appending"); only a later run's copy of the row carries the real
// value. rebuild-cache instead reads it straight from that disc's own
// RUN.bin, which does carry the run's actual stream_bytes, converted to
// whole sectors the same way pack itself does.
func fillUsedSectorsFromRuns(rows []format.DiscsRow, results []*image.ReadResult) []format.DiscsRow {
	streamBytesByUUID := make(map[[16]byte]uint64, len(results))
	for _, rr := range results {
		streamBytesByUUID[rr.Disc.DiscUUID] = rr.Run.StreamBytes
	}
	for i, row := range rows {
		streamBytes, ok := streamBytesByUUID[row.DiscUUID]
		if !ok {
			continue
		}
		rows[i].UsedSectors = (streamBytes + image.SectorSize - 1) / image.SectorSize
	}
	return rows
}

// refKey orders REFS records the way FORMAT.md's ref resolution rule
// does: the highest run_seq, then the highest time_sec, then the
// highest time_nsec.
type refKey struct {
	runSeq   uint64
	timeSec  int64
	timeNsec uint32
}

// newer reports whether k is the newer record under refKey's ordering.
func (k refKey) newer(other refKey) bool {
	if k.runSeq != other.runSeq {
		return k.runSeq > other.runSeq
	}
	if k.timeSec != other.timeSec {
		return k.timeSec > other.timeSec
	}
	return k.timeNsec > other.timeNsec
}

// mergeRefs takes, for every ref name any provided disc's REFS table
// carries, the newest record under refKey's ordering.
// bestRefRecords returns one REFS record per ref name, the newest by
// refKey ordering across every provided disc. rebuild-cache uses this
// both to restore the flat local ref file and to restore the refs
// ledger a later pack extends.
func bestRefRecords(results []*image.ReadResult) []format.RefRecord {
	type keyed struct {
		key refKey
		rec format.RefRecord
	}
	best := make(map[string]keyed)
	for _, rr := range results {
		for _, rec := range rr.Refs.Records {
			name := string(rec.Name[:rec.NameLen])
			k := refKey{runSeq: rec.RunSeq, timeSec: rec.TimeSec, timeNsec: rec.TimeNsec}
			if cur, ok := best[name]; ok && !k.newer(cur.key) {
				continue
			}
			best[name] = keyed{key: k, rec: rec}
		}
	}
	recs := make([]format.RefRecord, 0, len(best))
	for _, kv := range best {
		recs = append(recs, kv.rec)
	}
	return recs
}

// mergeRefs turns REFS records into the name-to-id-text map the flat
// local ref file holds.
func mergeRefs(records []format.RefRecord) map[string]string {
	out := make(map[string]string, len(records))
	for _, rec := range records {
		out[string(rec.Name[:rec.NameLen])] = object.ID(rec.SnapshotID).TextForm()
	}
	return out
}

// uuidText formats a 16-byte uuid as hyphenated lowercase text, matching
// restore's own missing-disc error format.
func uuidText(u [16]byte) string {
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
