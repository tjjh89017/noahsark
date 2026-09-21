package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
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

// TestGCRetentionGate runs gc with a fake clock before and after the
// fixed 7-day retention has passed: gc must delete nothing before, and
// delete the CLEAN run's objects after, in both --dry-run and a real
// run.
func TestGCRetentionGate(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	// Before the retention period: nothing is eligible. --dry-run always
	// exits 0, and names when the run's objects will become eligible.
	gcClock = func() time.Time { return before.Add(24 * time.Hour) }
	code, out := runCmd(t, "gc", "--repo="+repo, "--dry-run")
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
	gcClock = func() time.Time { return before.Add(8 * 24 * time.Hour) }
	code, out = runCmd(t, "gc", "--repo="+repo, "--dry-run")
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
	code, out = runCmd(t, "gc", "--repo="+repo)
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
	code, out = runCmd(t, "gc", "--repo="+repo)
	if code != 0 {
		t.Fatalf("gc (second run): exit %d, want 0: %s", code, out)
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
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	gcClock = func() time.Time { return before.Add(8 * 24 * time.Hour) }

	code, out := runCmd(t, "gc", "--repo="+repo, "--dry-run")
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

// TestGCRefusesAnUncachedRun runs gc with the local cache emptied: gc
// must not delete any object whose run's INDEX it cannot confirm
// against, even though the object is otherwise eligible.
func TestGCRefusesAnUncachedRun(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	cacheDir := cache.Dir(repo)

	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	// Empty the cache: gc can no longer confirm any object's run.
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}

	gcClock = func() time.Time { return before.Add(8 * 24 * time.Hour) }
	code, out := runCmd(t, "gc", "--repo="+repo)
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
	oldClock, oldRemove := gcClock, gcRemove
	defer func() { gcClock, gcRemove = oldClock, oldRemove }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	gcClock = func() time.Time { return before.Add(8 * 24 * time.Hour) }

	objDir := filepath.Join(repo, "staging", "objects")
	staged, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if staged == 0 {
		t.Fatal("the test needs staged object files to free")
	}

	gcRemove = func(string) error { return errors.New("injected unlink failure") }
	code, out := runCmd(t, "gc", "--repo="+repo)
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
	code, out = runCmd(t, "gc", "--repo="+repo)
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
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	planDir := filepath.Dir(stagedTree)
	discUUID := packedDiscUUID(t, packOut)

	// A packed disc keeps its plan directory: the operator has not
	// burned it yet.
	gcClock = func() time.Time { return before.Add(8 * 24 * time.Hour) }
	if code, out := runCmd(t, "gc", "--repo="+repo); code != 0 {
		t.Fatalf("gc (packed): exit %d: %s", code, out)
	}
	if _, err := os.Stat(planDir); err != nil {
		t.Fatalf("gc removed the plan directory of a packed disc: %v", err)
	}

	mounted := filepath.Join(work, "mounted")
	copyTree(t, stagedTree, mounted)
	if code, out := runCmd(t, "disc", "burned", "--repo="+repo, discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "verify", "--repo="+repo, mounted); code != 0 {
		t.Fatalf("verify copy 1: exit %d: %s", code, out)
	}

	// One verify of two: the plan directory stays.
	if code, out := runCmd(t, "gc", "--repo="+repo); code != 0 {
		t.Fatalf("gc (one verify): exit %d: %s", code, out)
	}
	if _, err := os.Stat(planDir); err != nil {
		t.Fatalf("gc removed the plan directory after one verify: %v", err)
	}

	if code, out := runCmd(t, "verify", "--repo="+repo, mounted); code != 0 {
		t.Fatalf("verify copy 2: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "gc", "--repo="+repo, "--dry-run")
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

	if code, out := runCmd(t, "gc", "--repo="+repo); code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if _, err := os.Stat(planDir); !os.IsNotExist(err) {
		t.Fatalf("gc kept the plan directory of an ON-DISC disc: %v", err)
	}
}

// setGCStdin installs r as gc's confirmation reader for the duration of
// the test.
func setGCStdin(t *testing.T, r io.Reader) {
	t.Helper()
	old := gcStdin
	gcStdin = r
	t.Cleanup(func() { gcStdin = old })
}

// TestGCForceAfterConfirmedDeletes runs gc --force-after with a fake
// clock placing an object CLEAN for longer than the forced duration but
// not the fixed 7-day retention, and a stdin pipe answering "y": the
// object must be deleted.
func TestGCForceAfterConfirmedDeletes(t *testing.T) {
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	// 2 hours past CLEAN: nowhere near the fixed 7-day retention, but
	// past a 1-hour --force-after.
	gcClock = func() time.Time { return before.Add(2 * time.Hour) }

	setGCStdin(t, strings.NewReader("y\n"))
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h")
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
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)

	gcClock = func() time.Time { return before.Add(2 * time.Hour) }

	setGCStdin(t, strings.NewReader("n\n"))
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h")
	if code == 0 {
		t.Fatalf("gc --force-after=1h (declined): exit 0, want non-zero: %s", out)
	}
	if !strings.Contains(out, "not confirmed") {
		t.Fatalf("gc output %q missing the not-confirmed message", out)
	}

	// A follow-up dry-run still finds the object eligible: nothing was
	// deleted.
	code, out = runCmd(t, "gc", "--repo="+repo, "--force-after=1h", "--dry-run")
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
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	gcClock = func() time.Time { return before.Add(2 * time.Hour) }

	setGCStdin(t, strings.NewReader(""))
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h")
	if code != 1 {
		t.Fatalf("gc --force-after=1h (empty stdin): exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "not confirmed") {
		t.Fatalf("gc output %q missing the not-confirmed message", out)
	}

	code, out = runCmd(t, "gc", "--repo="+repo, "--force-after=1h", "--dry-run")
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
	oldClock := gcClock
	defer func() { gcClock = oldClock }()

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	gcClock = func() time.Time { return before.Add(2 * time.Hour) }

	setGCStdin(t, strings.NewReader(""))
	code, out := runCmd(t, "gc", "--repo="+repo, "--force-after=1h", "--dry-run")
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
