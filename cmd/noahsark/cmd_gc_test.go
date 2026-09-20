package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// appendConfigLine appends one "key = value" line to repo's config
// file, the same file format init writes.
func appendConfigLine(t *testing.T, repo, line string) {
	t.Helper()
	f, err := os.OpenFile(configPath(repo), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// packAndVerifyDisc commits src into repo, packs it, copies the packed
// tree outside the repository's staging directory to stand in for a
// mounted disc, marks it burned, and verifies it two times, so the run's
// objects reach CLEAN with the verify count gc's default asks for, and
// its catalog enters the local cache. The two verifies stand for the two
// identical discs the operator burns from the same tree.
func packAndVerifyDisc(t *testing.T, work, repo, src string) {
	t.Helper()
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	discUUID := packedDiscUUID(t, packOut)
	mounted := filepath.Join(work, filepath.Base(t.TempDir()))
	copyTree(t, stagedTree, mounted)
	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	for copyNumber := 1; copyNumber <= 2; copyNumber++ {
		if code, out := runCmd(t, "verify", "--repo="+repo, mounted); code != 0 {
			t.Fatalf("verify copy %d: exit %d: %s", copyNumber, code, out)
		}
	}
}

// TestGCRetentionGate runs gc with a fake clock before and after
// staging.retain_after_clean has passed: gc must delete nothing before,
// and delete the CLEAN run's objects after, in both --dry-run and a
// real run.
func TestGCRetentionGate(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "staging.retain_after_clean = 1h")

	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	// Before the retention period: nothing is eligible. --dry-run always
	// exits 0, and names when the run's objects will become eligible.
	gcClock = func() time.Time { return before.Add(30 * time.Minute) }
	code, out := runCmd(t, "gc", "--repo="+repo, "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run (before retention): exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "would delete 0 object") {
		t.Fatalf("gc --dry-run (before retention) output %q, want 0 objects", out)
	}
	if !strings.Contains(out, "gc: nothing is eligible yet") {
		t.Fatalf("gc --dry-run (before retention) output %q, want the nothing-eligible-yet message", out)
	}
	if !strings.Contains(out, "earliest eligible date:") {
		t.Fatalf("gc --dry-run (before retention) output %q, want the earliest eligible date", out)
	}

	// After the retention period: dry-run reports what it would do,
	// without changing anything.
	gcClock = func() time.Time { return before.Add(2 * time.Hour) }
	code, out = runCmd(t, "gc", "--repo="+repo, "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run (after retention): exit %d: %s", code, out)
	}
	if strings.Contains(out, "would delete 0 object") {
		t.Fatalf("gc --dry-run (after retention) output %q, want more than 0 objects", out)
	}
	objDir := filepath.Join(repo, "staging", "objects")
	before1, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if before1 == 0 {
		t.Fatal("staging/objects is empty before gc; test fixture produced nothing to delete")
	}

	// A real run actually deletes, and is reflected in the object count
	// on disk.
	code, out = runCmd(t, "gc", "--repo="+repo)
	if code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if strings.Contains(out, "deleted 0 object") {
		t.Fatalf("gc output %q, want more than 0 objects deleted", out)
	}
	after1, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if after1 >= before1 {
		t.Fatalf("staging/objects has %d files after gc, had %d before; want fewer", after1, before1)
	}

	// A second gc run finds nothing left to do.
	code, out = runCmd(t, "gc", "--repo="+repo)
	if code != 1 {
		t.Fatalf("gc (second run): exit %d, want 1: %s", code, out)
	}
}

// TestGCDryRunDefaultIsASummary checks that gc --dry-run prints no
// per-object "would delete" line, only the staging totals and one
// grouped line per disc.
func TestGCDryRunDefaultIsASummary(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "staging.retain_after_clean = 1h")

	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	gcClock = func() time.Time { return before.Add(2 * time.Hour) }

	code, out := runCmd(t, "gc", "--repo="+repo, "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run: exit %d: %s", code, out)
	}
	if strings.Contains(out, "would delete 0 object") {
		t.Fatalf("gc --dry-run output %q, want more than 0 objects", out)
	}
	if !strings.Contains(out, "would delete: disc ") {
		t.Fatalf("gc --dry-run output %q missing the grouped disc summary line", out)
	}
}

// countFiles counts the regular files under dir, recursively.
func countFiles(dir string) (int, error) {
	n := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	return n, err
}

// TestGCTrimsCacheToNewestSnapshots packs and verifies two discs, each
// holding one snapshot, then runs gc --keep-snapshots=1: the older
// snapshot's own trees must be dropped from the cache, so ls on it
// reports the cache incomplete, while ls on the newest snapshot still
// works from the cache alone.
func TestGCTrimsCacheToNewestSnapshots(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	src := writeFixtureSource(t)
	code, commitOut := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit 1: exit %d: %s", code, commitOut)
	}
	snapA := snapshotIDFromCommit(t, commitOut)
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack 1: exit %d: %s", code, packOut)
	}
	treeA := packedTreeDir(t, packOut)
	discA := packedDiscUUID(t, packOut)
	mountedA := filepath.Join(work, "mountedA")
	copyTree(t, treeA, mountedA)
	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discA); code != 0 {
		t.Fatalf("disc burned 1: exit %d: %s", code, out)
	}
	for range 2 {
		if code, out := runCmd(t, "verify", "--repo="+repo, mountedA); code != 0 {
			t.Fatalf("verify 1: exit %d: %s", code, out)
		}
	}

	if err := os.WriteFile(filepath.Join(src, "new.txt"), []byte("a brand new file for the second snapshot"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, commitOut = runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, commitOut)
	}
	snapB := snapshotIDFromCommit(t, commitOut)
	code, packOut = runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack 2: exit %d: %s", code, packOut)
	}
	treeB := packedTreeDir(t, packOut)
	discB := packedDiscUUID(t, packOut)
	mountedB := filepath.Join(work, "mountedB")
	copyTree(t, treeB, mountedB)
	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discB); code != 0 {
		t.Fatalf("disc burned 2: exit %d: %s", code, out)
	}
	for range 2 {
		if code, out := runCmd(t, "verify", "--repo="+repo, mountedB); code != 0 {
			t.Fatalf("verify 2: exit %d: %s", code, out)
		}
	}

	// Before trimming, both snapshots list fine from the cache alone.
	if code, out := runCmd(t, "ls", "--repo="+repo, snapA); code != 0 {
		t.Fatalf("ls snapA (before trim): exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "ls", "--repo="+repo, snapB); code != 0 {
		t.Fatalf("ls snapB (before trim): exit %d: %s", code, out)
	}

	code, out := runCmd(t, "gc", "--repo="+repo, "--keep-snapshots=1")
	if code != 0 {
		t.Fatalf("gc --keep-snapshots=1: exit %d: %s", code, out)
	}
	if strings.Contains(out, "cache: deleted 0 tree") {
		t.Fatalf("gc output %q, want more than 0 cache trees deleted", out)
	}

	// The older snapshot is no longer complete in the cache.
	code, out = runCmd(t, "ls", "--repo="+repo, snapA)
	if code == 0 {
		t.Fatalf("ls snapA (after trim): exit 0, want the cache reported incomplete: %s", out)
	}
	if !strings.Contains(out, "not complete in the cache") {
		t.Fatalf("ls snapA (after trim) output %q missing the incomplete-cache message", out)
	}

	// The newest snapshot still lists fine, entirely from the cache.
	if code, out := runCmd(t, "ls", "--repo="+repo, snapB); code != 0 {
		t.Fatalf("ls snapB (after trim): exit %d: %s", code, out)
	}
}

// TestGCRefusesAnUncachedRun runs gc with the local cache emptied: gc
// must not delete any object whose run's INDEX it cannot confirm
// against, even though the object is otherwise eligible.
func TestGCRefusesAnUncachedRun(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	cacheDir := filepath.Join(work, "cache")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "staging.retain_after_clean = 1h")
	appendConfigLine(t, repo, "cache.dir = "+cacheDir)

	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	// Empty the cache: gc can no longer confirm any object's run.
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}

	gcClock = func() time.Time { return before.Add(2 * time.Hour) }
	code, out := runCmd(t, "gc", "--repo="+repo)
	if code != 1 {
		t.Fatalf("gc: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "skipped") {
		t.Fatalf("gc output %q missing the skipped line", out)
	}
	if !strings.Contains(out, "deleted 0 object") {
		t.Fatalf("gc output %q, want 0 objects deleted", out)
	}
}

// TestGCPlanTakesTheIndexOfTheObjectsOwnDisc caches two discs that
// carry the same run_seq, as two discs do after a lost repository. gc
// must confirm a staged object in the cached INDEX of the disc its own
// state record names. The INDEX of the other disc must never stand in
// for it.
func TestGCPlanTakesTheIndexOfTheObjectsOwnDisc(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}

	discA := [16]byte{0xaa}
	discB := [16]byte{0xbb}
	const sharedRunSeq = 5
	onDiscA := object.ComputeID([]byte("an object disc A holds"))
	onDiscB := object.ComputeID([]byte("an object staged for disc B, named by disc A's INDEX alone"))

	for _, staged := range []struct {
		id   object.ID
		disc [16]byte
	}{{onDiscA, discA}, {onDiscB, discB}} {
		if err := l.EnsureStaged(staged.id); err != nil {
			t.Fatal(err)
		}
		if err := l.MarkPacked(staged.id, sharedRunSeq, staged.disc); err != nil {
			t.Fatal(err)
		}
		if err := l.MarkBurned(staged.id, sharedRunSeq, staged.disc); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := l.MarkVerified(staged.id); err != nil {
				t.Fatal(err)
			}
		}
	}

	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	indexOfA := encodeGCIndex(t, sharedRunSeq, []format.IndexObjectRecord{
		{ContentID: onDiscA, PayloadLen: 10, StoredLen: 10, Kind: format.ObjectKindChunk},
		{ContentID: onDiscB, PayloadLen: 20, StoredLen: 20, Kind: format.ObjectKindChunk},
	})
	if err := c.WriteDisc(discA, indexOfA, encodeGCRefs(t), encodeGCDiscs(t, discA, sharedRunSeq)); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteDisc(discB, encodeGCIndex(t, sharedRunSeq, nil), encodeGCRefs(t), encodeGCDiscs(t, discB, sharedRunSeq)); err != nil {
		t.Fatal(err)
	}

	objs, uncached := gcPlanStagingObjects(l, c, stagingDir, 0, 2, time.Now())
	if len(objs) != 1 {
		t.Fatalf("gcPlanStagingObjects returned %d object(s), want 1", len(objs))
	}
	if objs[0].id != onDiscA || objs[0].discUUID != discA {
		t.Fatalf("candidate = %s on disc %v, want %s on disc %v",
			objs[0].id.TextForm(), objs[0].discUUID, onDiscA.TextForm(), discA)
	}
	if uncached != 1 {
		t.Fatalf("uncached = %d, want 1: disc B's own INDEX names no object", uncached)
	}
}

// encodeGCIndex builds a minimal, valid INDEX holding rows.
func encodeGCIndex(t *testing.T, runSeq uint64, rows []format.IndexObjectRecord) []byte {
	t.Helper()
	idx := &format.Index{
		Header:      format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicIndex, VersionMajor: 1},
		RunSeq:      runSeq,
		ObjectCount: uint32(len(rows)),
		HashAlgo:    format.HashAlgoSHA256,
		DigestLen:   32,
		Objects:     rows,
	}
	buf := make([]byte, idx.EncodedLen())
	if _, err := idx.Encode(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

// encodeGCDiscs builds a DISCS table naming one disc and its run.
func encodeGCDiscs(t *testing.T, uuid [16]byte, runSeq uint64) []byte {
	t.Helper()
	discs := &format.DiscsTable{
		Header:      format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicDiscs, VersionMajor: 1},
		HashAlgo:    format.HashAlgoSHA256,
		DigestLen:   32,
		RecordCount: 1,
		RecordSize:  format.DiscsRowLen,
		Rows:        []format.DiscsRow{{RunSeq: runSeq, DiscUUID: uuid, RunStatus: 1, Health: 1}},
	}
	buf := make([]byte, format.DiscsHeaderLen+format.DiscsRowLen)
	if _, err := discs.Encode(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

// encodeGCRefs builds a minimal, valid empty REFS table.
func encodeGCRefs(t *testing.T) []byte {
	t.Helper()
	refs := &format.RefsTable{
		Header:     format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicRefs, VersionMajor: 1},
		HashAlgo:   format.HashAlgoSHA256,
		DigestLen:  32,
		RecordSize: format.RefRecordLen,
	}
	buf := make([]byte, format.RefsHeaderLen)
	if _, err := refs.Encode(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}
