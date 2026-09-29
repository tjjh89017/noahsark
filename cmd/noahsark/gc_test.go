package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestGCHoldsObjectsUntilTheSecondVerify checks that one verify is not
// enough: after the whole retention period gc still deletes nothing and
// names the disc it holds objects back for. The second verify frees
// them.
func TestGCHoldsObjectsUntilTheSecondVerify(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	mounted := packBurnDisc(t, work, repo, src)
	if code, out := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
		t.Fatalf("verify copy 1: exit %d: %s", code, out)
	}
	setFakeNow(t, func() time.Time { return before.Add(8 * 24 * time.Hour) })

	code, out := runCmd(t, "--repo="+repo, "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run: exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "would delete 0 staged object") {
		t.Fatalf("gc --dry-run output %q, want 0 objects", out)
	}
	if !strings.Contains(out, "1 of 2 copies verified") || !strings.Contains(out, "verify the second copy") {
		t.Fatalf("gc --dry-run output %q, want the held line", out)
	}
	if !gcHeldDiscLineRe.MatchString(out) {
		t.Fatalf("gc --dry-run output %q must name the held disc by number, label and uuid", out)
	}

	objDir := filepath.Join(repo, "staging", "objects")
	staged, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc: exit %d, want 0 (nothing eligible): %s", code, out)
	}
	if !strings.Contains(out, "1 of 2 copies verified") {
		t.Fatalf("gc output %q, want the held line", out)
	}
	stillStaged, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if stillStaged != staged {
		t.Fatalf("staging/objects has %d files after the held gc, had %d; want no delete", stillStaged, staged)
	}

	if code, out := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
		t.Fatalf("verify copy 2: exit %d: %s", code, out)
	}
	code, out = runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc after the second verify: exit %d: %s", code, out)
	}
	if strings.Contains(out, "deleted 0 staged object") {
		t.Fatalf("gc after the second verify output %q, want more than 0 objects deleted", out)
	}
	if strings.Contains(out, "copies verified;") {
		t.Fatalf("gc after the second verify output %q, want no held line", out)
	}
	afterGC, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if afterGC >= staged {
		t.Fatalf("staging/objects has %d files after gc, had %d before; want fewer", afterGC, staged)
	}
}

// TestGCMinVerifiedCopiesOne checks the escape hatch of an operator who
// keeps one copy: with gc.min_verified_copies = 1, one verify frees the
// objects once the retention period has passed.
func TestGCMinVerifiedCopiesOne(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "gc.min_verified_copies = 1")

	before := time.Now()
	mounted := packBurnDisc(t, work, repo, src)
	code, out := runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "verify: 1 of 1 copies verified") {
		t.Fatalf("verify output %q, want the 1 of 1 line", out)
	}

	setFakeNow(t, func() time.Time { return before.Add(8 * 24 * time.Hour) })
	code, out = runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if strings.Contains(out, "deleted 0 staged object") {
		t.Fatalf("gc output %q, want more than 0 objects deleted", out)
	}
}

// TestGCForceAfterDoesNotBypassTheVerifyCount checks that --force-after
// shortens the retention period only. One verified copy still holds the
// objects.
func TestGCForceAfterDoesNotBypassTheVerifyCount(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	mounted := packBurnDisc(t, work, repo, src)
	if code, out := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}

	objDir := filepath.Join(repo, "staging", "objects")
	staged, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	setFakeStdin(t, strings.NewReader("y\n"))
	code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=0s")
	if code != 0 {
		t.Fatalf("gc --force-after=0s: exit %d, want 0 (nothing eligible): %s", code, out)
	}
	if !strings.Contains(out, "1 of 2 copies verified") {
		t.Fatalf("gc --force-after output %q, want the held line", out)
	}
	stillStaged, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if stillStaged != staged {
		t.Fatalf("staging/objects has %d files after --force-after, had %d; want no delete", stillStaged, staged)
	}
}

// gcHeldDiscLineRe matches gc's held-for-copies line, which names the
// disc the way every other command names one.
var gcHeldDiscLineRe = regexp.MustCompile(`gc: disc \d+ "[^"]*" \([0-9a-f-]+\): \d+ of \d+ copies verified`)

// TestGCRetentionGate runs gc with a fake clock before and after the
// fixed 7-day retention has passed: gc must delete nothing before, and
// delete the CLEAN run's objects after, in both --dry-run and a real
// run.
func TestGCRetentionGate(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	// Before the retention period: nothing is eligible. --dry-run always
	// exits 0, and names when the run's objects will become eligible.
	setFakeNow(t, func() time.Time { return before.Add(24 * time.Hour) })
	code, out := runCmd(t, "--repo="+repo, "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run (before retention): exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "would delete 0 staged object") {
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
	setFakeNow(t, func() time.Time { return before.Add(8 * 24 * time.Hour) })
	code, out = runCmd(t, "--repo="+repo, "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run (after retention): exit %d: %s", code, out)
	}
	if strings.Contains(out, "would delete 0 staged object") {
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
	code, out = runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if strings.Contains(out, "deleted 0 staged object") {
		t.Fatalf("gc output %q, want more than 0 objects deleted", out)
	}
	after1, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if after1 >= before1 {
		t.Fatalf("staging/objects has %d files after gc, had %d before; want fewer", after1, before1)
	}

	// A second gc run finds nothing left to do; nothing eligible is
	// success, not a failure.
	code, out = runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc (second run): exit %d, want 0: %s", code, out)
	}
}

// TestGCPlainRunBeforeRetentionNamesTheReason checks that a plain gc
// (no --dry-run) that deletes nothing because the retention time is not
// over prints the same reason and earliest eligible date --dry-run
// prints, not just "deleted 0".
func TestGCPlainRunBeforeRetentionNamesTheReason(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	setFakeNow(t, func() time.Time { return before.Add(24 * time.Hour) })
	code, out := runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc (before retention): exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "deleted 0 staged object") {
		t.Fatalf("gc (before retention) output %q, want 0 objects deleted", out)
	}
	if !strings.Contains(out, "gc: nothing is eligible yet") {
		t.Fatalf("gc (before retention) output %q, want the nothing-eligible-yet message", out)
	}
	if !strings.Contains(out, "earliest eligible date:") {
		t.Fatalf("gc (before retention) output %q, want the earliest eligible date", out)
	}
}

// TestGCDryRunDefaultIsASummary checks that gc --dry-run prints no
// per-object "would delete" line, only the staging totals and one
// grouped line per disc.
func TestGCDryRunDefaultIsASummary(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	setFakeNow(t, func() time.Time { return before.Add(8 * 24 * time.Hour) })

	code, out := runCmd(t, "--repo="+repo, "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run: exit %d: %s", code, out)
	}
	if strings.Contains(out, "would delete 0 staged object") {
		t.Fatalf("gc --dry-run output %q, want more than 0 objects", out)
	}
	if !gcDiscLineRe.MatchString(out) {
		t.Fatalf("gc --dry-run output %q must name the disc by number, label and uuid", out)
	}
}

// TestGCRefusesAnUncachedRun runs gc with the local cache emptied: gc
// must not delete any object whose run's INDEX it cannot confirm
// against, even though the object is otherwise eligible.
func TestGCRefusesAnUncachedRun(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	cacheDir := cache.Dir(repo)

	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	// Empty the cache: gc can no longer confirm any object's run.
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}

	setFakeNow(t, func() time.Time { return before.Add(8 * 24 * time.Hour) })
	code, out := runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc: exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "skipped") {
		t.Fatalf("gc output %q missing the skipped line", out)
	}
	if !strings.Contains(out, "deleted 0 staged object") {
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
	onDiscA := object.ComputeID(format.ObjectKindChunk, []byte("an object disc A holds"))
	onDiscB := object.ComputeID(format.ObjectKindChunk, []byte("an object staged for disc B, named by disc A's INDEX alone"))

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
		{ContentID: onDiscA, Kind: format.ObjectKindChunk},
		{ContentID: onDiscB, Kind: format.ObjectKindChunk},
	}, []uint64{74, 84})
	if err := c.WriteDisc(discA, indexOfA, encodeGCRefs(t), encodeGCDiscs(t, discA, sharedRunSeq)); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteDisc(discB, encodeGCIndex(t, sharedRunSeq, nil, nil), encodeGCRefs(t), encodeGCDiscs(t, discB, sharedRunSeq)); err != nil {
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
func encodeGCIndex(t *testing.T, runSeq uint64, rows []format.IndexObjectRecord, byteLens []uint64) []byte {
	t.Helper()
	files := make([]format.IndexFileRecord, len(rows))
	for i := range rows {
		files[i] = format.IndexFileRecord{Role: format.FileRoleObject, ByteLen: byteLens[i]}
	}
	idx := &format.Index{
		Header:      format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicIndex, VersionMajor: 1, HeaderLen: format.IndexHeaderLen},
		RunSeq:      runSeq,
		FileCount:   uint32(len(files)),
		ObjectCount: uint32(len(rows)),
		Files:       files,
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
		Header:      format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicDiscs, VersionMajor: 1, HeaderLen: format.DiscsHeaderLen},
		RecordCount: 1,
		Rows:        []format.DiscsRow{{RunSeq: runSeq, DiscUUID: uuid}},
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
		Header: format.CommonHeader{MagicProject: format.ProjectMagic, MagicKind: format.MagicRefs, VersionMajor: 1, HeaderLen: format.RefsHeaderLen},
	}
	buf := make([]byte, format.RefsHeaderLen)
	if _, err := refs.Encode(buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

// TestGCWritesTheRecordBeforeTheUnlink fails the unlink of every staged
// file, the way a crash right after the durable record does. The first
// run must leave every object recorded ON-DISC with its file still
// there. The second run, with the unlink working again, must find those
// orphans and free them. The record always comes first: the other order
// would leave an object the log calls CLEAN with no bytes behind it.
func TestGCWritesTheRecordBeforeTheUnlink(t *testing.T) {
	oldRemove := gcRemove
	t.Cleanup(func() { gcRemove = oldRemove })

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	setFakeNow(t, func() time.Time { return before.Add(8 * 24 * time.Hour) })

	objDir := filepath.Join(repo, "staging", "objects")
	staged, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if staged == 0 {
		t.Fatal("the test needs staged object files to free")
	}

	gcRemove = func(string) error { return errors.New("injected unlink failure") }
	code, out := runCmd(t, "--repo="+repo, "gc")
	if code != 1 {
		t.Fatalf("gc with a failing unlink: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "injected unlink failure") {
		t.Fatalf("gc with a failing unlink output %q, want the unlink error named", out)
	}
	if n, err := countFiles(objDir); err != nil || n != staged {
		t.Fatalf("staging/objects has %d file(s), %v; want the %d orphans left behind", n, err, staged)
	}
	if n := countByState(t, repo, stage.OnDiscOnly); n == 0 {
		t.Fatal("gc unlinked before it recorded: no ON-DISC record survived the failed unlink")
	}
	if n := countByState(t, repo, stage.Clean); n != 0 {
		t.Fatalf("%d object(s) still CLEAN; the record must go to the disk before the unlink", n)
	}

	gcRemove = oldRemove
	code, out = runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc (second run): exit %d, want 0: %s", code, out)
	}
	if n, err := countFiles(objDir); err != nil || n != 0 {
		t.Fatalf("staging/objects has %d file(s), %v; want the orphans freed", n, err)
	}
}

// gcDiscLineRe matches gc's grouped summary line, which names the disc
// the way every other command names one.
var gcDiscLineRe = regexp.MustCompile(`would delete: disc \d+ "[^"]*" \([0-9a-f-]+\): \d+ object\(s\)`)

// TestGCFreesThePlanDirectory checks that gc keeps a disc's plan
// directory while the disc is packed or not verified two times, names
// it with its bytes in --dry-run, and removes it once every object of
// the disc is ON-DISC.
func TestGCFreesThePlanDirectory(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	planDir := filepath.Dir(stagedTree)
	discUUID := packedDiscUUID(t, packOut)

	// A packed disc keeps its plan directory: the operator has not
	// burned it yet.
	setFakeNow(t, func() time.Time { return before.Add(8 * 24 * time.Hour) })
	if code, out := runCmd(t, "--repo="+repo, "gc"); code != 0 {
		t.Fatalf("gc (packed): exit %d: %s", code, out)
	}
	if _, err := os.Stat(planDir); err != nil {
		t.Fatalf("gc removed the plan directory of a packed disc: %v", err)
	}

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)
	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
		t.Fatalf("verify copy 1: exit %d: %s", code, out)
	}

	// One verify of two: the plan directory stays.
	if code, out := runCmd(t, "--repo="+repo, "gc"); code != 0 {
		t.Fatalf("gc (one verify): exit %d: %s", code, out)
	}
	if _, err := os.Stat(planDir); err != nil {
		t.Fatalf("gc removed the plan directory after one verify: %v", err)
	}

	if code, out := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
		t.Fatalf("verify copy 2: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "plan directory "+planDir) {
		t.Fatalf("gc --dry-run output %q, want the plan directory line", out)
	}
	if strings.Contains(out, "would delete 0 disc plan directory") {
		t.Fatalf("gc --dry-run output %q, want one plan directory", out)
	}
	if _, err := os.Stat(planDir); err != nil {
		t.Fatalf("gc --dry-run removed the plan directory: %v", err)
	}

	if code, out := runCmd(t, "--repo="+repo, "gc"); code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if _, err := os.Stat(planDir); !os.IsNotExist(err) {
		t.Fatalf("gc kept the plan directory of an ON-DISC disc: %v", err)
	}
}

// TestGCForceAfterConfirmedDeletes runs gc --force-after with a fake
// clock placing an object CLEAN for longer than the forced duration but
// not the fixed 7-day retention, and a stdin pipe answering "y": the
// object must be deleted.
func TestGCForceAfterConfirmedDeletes(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	// 2 hours past CLEAN: nowhere near the fixed 7-day retention, but
	// past a 1-hour --force-after.
	setFakeNow(t, func() time.Time { return before.Add(2 * time.Hour) })

	setFakeStdin(t, strings.NewReader("y\n"))
	code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=1h")
	if code != 0 {
		t.Fatalf("gc --force-after=1h (confirmed): exit %d: %s", code, out)
	}
	if !strings.Contains(out, "delete") || !strings.Contains(out, "bytes?") {
		t.Fatalf("gc output %q missing the confirmation prompt", out)
	}
	if strings.Contains(out, "deleted 0 staged object") {
		t.Fatalf("gc output %q, want more than 0 objects deleted", out)
	}
}

// TestGCForceAfterDeclinedDeletesNothing checks answering "n" to the
// confirmation leaves every object in place.
func TestGCForceAfterDeclinedDeletesNothing(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	setFakeNow(t, func() time.Time { return before.Add(2 * time.Hour) })

	setFakeStdin(t, strings.NewReader("n\n"))
	code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=1h")
	if code == 0 {
		t.Fatalf("gc --force-after=1h (declined): exit 0, want non-zero: %s", out)
	}
	if !strings.Contains(out, "not confirmed") {
		t.Fatalf("gc output %q missing the not-confirmed message", out)
	}

	// A follow-up dry-run still finds the object eligible: nothing was
	// deleted.
	code, out = runCmd(t, "--repo="+repo, "gc", "--force-after=1h", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run (after decline): exit %d: %s", code, out)
	}
	if strings.Contains(out, "would delete 0 staged object") {
		t.Fatalf("gc --dry-run (after decline) output %q, want the object still eligible", out)
	}
}

// TestGCForceAfterEmptyStdinDeletesNothing checks that a closed or
// empty stdin answers no: a killed or scripted session must never read
// silence as consent to delete under a shortened retention. A script
// that means yes pipes a "y" in.
func TestGCForceAfterEmptyStdinDeletesNothing(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	setFakeNow(t, func() time.Time { return before.Add(2 * time.Hour) })

	setFakeStdin(t, strings.NewReader(""))
	code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=1h")
	if code != 1 {
		t.Fatalf("gc --force-after=1h (empty stdin): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "not confirmed") {
		t.Fatalf("gc output %q missing the not-confirmed message", out)
	}

	code, out = runCmd(t, "--repo="+repo, "gc", "--force-after=1h", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run (after empty stdin): exit %d: %s", code, out)
	}
	if strings.Contains(out, "would delete 0 staged object") {
		t.Fatalf("gc --dry-run output %q, want the object still eligible", out)
	}
}

// TestGCForceAfterDryRunSkipsConfirmation checks --dry-run with
// --force-after never prompts: it changes nothing either way.
func TestGCForceAfterDryRunSkipsConfirmation(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	setFakeNow(t, func() time.Time { return before.Add(2 * time.Hour) })

	setFakeStdin(t, strings.NewReader(""))
	code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=1h", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --force-after=1h --dry-run: exit %d: %s", code, out)
	}
	if strings.Contains(out, "would delete 0 staged object") {
		t.Fatalf("gc --dry-run output %q, want more than 0 objects reported", out)
	}
	if strings.Contains(out, "bytes?") {
		t.Fatalf("gc --dry-run output %q, want no confirmation prompt", out)
	}
}

// TestGCApplyStagingObjectsSkipsAlreadyGoneFile checks that a gcObj
// whose staged file does not exist frees no bytes and is not counted
// deleted: only an object gc actually removed counts, even though the
// state log still moves it on to ON-DISC.
func TestGCApplyStagingObjectsSkipsAlreadyGoneFile(t *testing.T) {
	dir := t.TempDir()
	l, err := stage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	id := object.ComputeID(format.ObjectKindChunk, []byte("gone"))
	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}

	objs := []gcObj{{
		id:          id,
		path:        filepath.Join(dir, "objects", "no", "such-file"),
		size:        1234,
		needsRecord: true,
	}}

	deleted, bytesFreed, failures := gcApplyStagingObjects(l, objs, false)
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0: an already-gone file frees nothing this run", deleted)
	}
	if bytesFreed != 0 {
		t.Fatalf("bytesFreed = %d, want 0", bytesFreed)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none: an already-gone file is not a failure", failures)
	}

	rec, ok := l.Get(id)
	if !ok || rec.State != stage.OnDiscOnly {
		t.Fatalf("got %+v, %v, want OnDiscOnly: the state machine still moves on", rec, ok)
	}
}

// TestGCApplyStagingObjectsCountsRealDelete is the control: a gcObj
// whose file exists is removed, counted, and its bytes freed.
func TestGCApplyStagingObjectsCountsRealDelete(t *testing.T) {
	dir := t.TempDir()
	l, err := stage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	id := object.ComputeID(format.ObjectKindChunk, []byte("present"))
	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "objects", "present")
	if err := writeFile(path, "payload"); err != nil {
		t.Fatal(err)
	}

	objs := []gcObj{{id: id, path: path, size: 7, needsRecord: true}}

	deleted, bytesFreed, failures := gcApplyStagingObjects(l, objs, false)
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if bytesFreed != 7 {
		t.Fatalf("bytesFreed = %d, want 7", bytesFreed)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
}

// TestGCFailsFastWhenRepoLockHeld checks bug 1: gc must refuse to run
// while another command holds the repository's exclusive lock, instead
// of replaying a state log another process may change underneath it.
// It asserts the clear message and the exit code a held lock gives: a
// failure at run time, not a usage error.
func TestGCFailsFastWhenRepoLockHeld(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	held, err := repolock.Acquire(repo)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	defer func() { _ = held.Release() }()

	code, out := runCmd(t, "--repo="+repo, "gc", "--dry-run")
	if code != 1 {
		t.Fatalf("gc while locked: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "repository lock") {
		t.Fatalf("gc while locked output %q, want it to name the repository lock", out)
	}
	if !strings.Contains(out, "another noahsark command runs on this repository") {
		t.Fatalf("gc while locked output %q, want the other-command message", out)
	}
}

// TestRepoLockFreeAfterGC checks that gc releases the repository lock on
// exit, so the next command never finds a lock a crashed or finished
// process left behind.
func TestRepoLockFreeAfterGC(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "gc", "--dry-run"); code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}

	lk, err := repolock.Acquire(repo)
	if err != nil {
		t.Fatalf("acquire after gc: %v, want the lock free", err)
	}
	_ = lk.Release()
}

// TestGCWarnsOnTruncatedStateLog checks bug 3: a command that opens a
// state log whose tail was truncated by a crash during an earlier
// append must print one warning line, matching OPERATIONS.md's "State
// log replay".
func TestGCWarnsOnTruncatedStateLog(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	// Corrupt the last byte of state.db's last record's CRC, simulating
	// a crash mid-append, the same way internal/stage's own truncated
	// tail tests do.
	statePath := filepath.Join(repo, "staging", "state.db")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xFF
	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "--repo="+repo, "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("gc after a truncated log: exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, "state log's tail was truncated") {
		t.Fatalf("gc after a truncated log output %q, want the truncated-tail warning", out)
	}
}

// TestGCUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// gc: each case exits 2, never 0 or 1.
func TestGCUsageErrorsExitTwo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"unexpected positional argument", []string{"--repo=" + repo, "gc", "extra"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := runCmd(t, c.args...)
			if code != 2 {
				t.Fatalf("args %v: exit %d, want 2: %s", c.args, code, out)
			}
		})
	}
}
