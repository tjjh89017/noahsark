package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"

	"github.com/tjjh89017/noahsark/internal/cache"
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
	var readRoots []string
	for _, root := range discRoots {
		rr, err := image.ReadWithProgress(root, prog)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: rebuild-cache: %s: %v\n", root, err)
			continue
		}
		results = append(results, rr)
		readRoots = append(readRoots, root)
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

	if err := rebuildCacheFromRoots(cfg, repoUUID, readRoots); err != nil {
		// The cache is only an accelerator: a failure to populate it
		// never fails rebuild-cache itself.
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache: cache:", err)
	}

	// EnsurePacked never resets an object past PACKED: an object already
	// CLEAN, BURNED or later stays there. Count the two outcomes
	// separately, so the summary never claims an object is PACKED when
	// it is, in fact, further along.
	var packedObjects, alreadyPastPacked int
	for _, rr := range results {
		for _, row := range rr.Index.Objects {
			id := object.ID(row.ContentID)
			wasPastPacked := false
			if rec, ok := stageLog.Get(id); ok && rec.State != stage.Packed {
				wasPastPacked = true
			}
			if err := stageLog.EnsurePacked(id, rr.Run.RunSeq, rr.Disc.DiscUUID); err != nil {
				_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
				return 1
			}
			if wasPastPacked {
				alreadyPastPacked++
			} else {
				packedObjects++
			}
		}
		// This disc's own catalog was just replayed above: record it as
		// fed, not merely named by some other disc's copy of its DISCS
		// row. This is the set the final "ok means every disc known is
		// fed" check reads.
		if err := stageLog.RecordFedDisc(rr.Disc.DiscUUID); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
			return 1
		}
	}

	// Load every ledger and ref this repository already carries before
	// replacing them, so a call fed only some of the discs merges into
	// what earlier calls already recorded instead of erasing it. A
	// rebuild-cache call otherwise never sees another call's own state.
	existingDiscs, err := image.LoadDiscsLedger(cfg.StagingDir, repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 1
	}
	existingRefsLedger, err := image.LoadRefsLedger(cfg.StagingDir, repoUUID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 1
	}
	existingLocalRefs, err := readRefs(repoDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 1
	}

	discRows := mergeDiscsRows(results, existingDiscs.Rows)
	discRows = fillUsedSectorsFromRuns(discRows, results)
	if err := image.SaveDiscsLedger(cfg.StagingDir, repoUUID, discRows); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 1
	}

	refRecords := bestRefRecords(results, existingRefsLedger.Records)
	if err := image.SaveRefsLedger(cfg.StagingDir, repoUUID, refRecords); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 1
	}
	// A local ref name whose snapshot was never packed onto any disc
	// never appears in refRecords: keep it, rather than let a disc
	// replay erase a commit rebuild-cache has no way to see. A name
	// a disc does carry always takes the disc's value.
	refs := existingLocalRefs
	if refs == nil {
		refs = make(map[string]string)
	}
	maps.Copy(refs, mergeRefs(refRecords))
	if err := writeRefs(repoDir, refs); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: rebuild-cache:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "rebuild-cache: level 1, %d disc(s) read, repo %s\n", len(results), repoDir)
	_, _ = fmt.Fprintf(stdout, "objects recorded: %d packed, %d already past packed (clean or burned)\n", packedObjects, alreadyPastPacked)
	_, _ = fmt.Fprintf(stdout, "discs known: %d, refs restored: %d\n", len(discRows), len(refs))

	notFed := discsNotFed(discRows, stageLog)
	if len(notFed) > 0 {
		for _, row := range notFed {
			label := string(row.Label[:row.LabelLen])
			_, _ = fmt.Fprintf(stdout, "rebuild is partial: disc %s (%s) not fed yet\n", uuidText(row.DiscUUID), label)
		}
		return 1
	}

	_, _ = fmt.Fprintln(stdout, "rebuild-cache: ok")
	return 0
}

// discsNotFed returns, sorted by uuid text, every row of the merged
// ledger whose own disc has never itself been fed to rebuild-cache: its
// row may only have arrived here as a copy carried in a sibling disc's
// own DISCS table. "ok" must wait for every one of these to be read at
// least once, however many separate calls that takes.
func discsNotFed(rows []format.DiscsRow, l *stage.Log) []format.DiscsRow {
	var out []format.DiscsRow
	for _, row := range rows {
		if !l.FedDiscs(row.DiscUUID) {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return uuidText(out[i].DiscUUID) < uuidText(out[j].DiscUUID) })
	return out
}

// rebuildCacheFromRoots copies every one of readRoots' run catalog,
// snapshots and trees into the local cache, so rebuild-cache leaves ls
// and plan able to run with no disc present, the same way pack does
// right after building a run.
func rebuildCacheFromRoots(cfg repoConfig, repoUUID [16]byte, readRoots []string) error {
	if len(readRoots) == 0 {
		return nil
	}
	dir, err := cache.ResolveDir(repoUUID, cfg.CacheDir)
	if err != nil {
		return err
	}
	c, err := cache.Open(dir)
	if err != nil {
		return err
	}
	for _, root := range readRoots {
		if _, err := cache.WriteFromRoot(c, root); err != nil {
			return fmt.Errorf("%s: %w", root, err)
		}
	}
	return nil
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

// mergeDiscsRows unions every provided disc's DISCS rows with existing,
// the rows the local ledger already carried from an earlier call, keyed
// by DiscUUID (a run burns exactly one disc in this build, so DiscUUID
// and RunSeq name the same row). Between two rows for the same uuid,
// rowNewer picks the one to keep. The result is sorted by RunSeq
// ascending so a later Pack call appends after them in the same order
// it would have burned them in.
//
// Whether a rebuild is partial is decided separately, from the state
// log's fed-disc record: a row this function merges in from a sibling
// disc's own DISCS table names a disc, but never says that disc's own
// catalog was ever replayed.
func mergeDiscsRows(results []*image.ReadResult, existing []format.DiscsRow) []format.DiscsRow {
	byUUID := make(map[[16]byte]format.DiscsRow, len(existing))
	for _, row := range existing {
		byUUID[row.DiscUUID] = row
	}

	for _, rr := range results {
		for _, row := range rr.Discs.Rows {
			if cur, ok := byUUID[row.DiscUUID]; !ok || rowNewer(row, cur) {
				byUUID[row.DiscUUID] = row
			}
		}
	}

	rows := make([]format.DiscsRow, 0, len(byUUID))
	for _, row := range byUUID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].RunSeq < rows[j].RunSeq })
	return rows
}

// rowNewer reports whether a should replace b when both name the same
// disc: the row with the later last-verify time wins, and a tie goes to
// the row that already carries a real used-sectors count over one that
// still carries the zero placeholder a disc's own DISCS copy of itself
// always holds.
func rowNewer(a, b format.DiscsRow) bool {
	if a.LastVerifySec != b.LastVerifySec {
		return a.LastVerifySec > b.LastVerifySec
	}
	return a.UsedSectors > b.UsedSectors
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

// bestRefRecords returns one REFS record per ref name, the newest by
// refKey ordering across every provided disc and existing, the records
// the local refs ledger already carried from an earlier call.
// rebuild-cache uses this both to restore the flat local ref file and
// to restore the refs ledger a later pack extends.
func bestRefRecords(results []*image.ReadResult, existing []format.RefRecord) []format.RefRecord {
	type keyed struct {
		key refKey
		rec format.RefRecord
	}
	best := make(map[string]keyed)
	consider := func(rec format.RefRecord) {
		name := string(rec.Name[:rec.NameLen])
		k := refKey{runSeq: rec.RunSeq, timeSec: rec.TimeSec, timeNsec: rec.TimeNsec}
		if cur, ok := best[name]; ok && !k.newer(cur.key) {
			return
		}
		best[name] = keyed{key: k, rec: rec}
	}
	for _, rr := range results {
		for _, rec := range rr.Refs.Records {
			consider(rec)
		}
	}
	for _, rec := range existing {
		consider(rec)
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
