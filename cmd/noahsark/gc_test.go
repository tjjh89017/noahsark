package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestGCFreesAfterOneVerify checks that one good verify frees the
// objects once the retention period has passed.
func TestGCFreesAfterOneVerify(t *testing.T) {

	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	before := time.Now()
	mounted := packBurnDisc(t, work, repo, src)
	code, out := runCmd(t, "--repo="+repo, "verify", mounted)
	if code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "\nverified\n") {
		t.Fatalf("verify output %q, want the verified line", out)
	}

	setFakeNow(t, func() time.Time { return before.Add(8 * 24 * time.Hour) })
	code, out = runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if !strings.HasPrefix(out, "gc: freed ") || strings.HasPrefix(out, gcFreedNone) {
		t.Fatalf("gc output %q, want more than 0 items freed", out)
	}
	if !strings.HasSuffix(out, "\nnext: noahsark status\n") {
		t.Fatalf("gc output %q, want the next line last", out)
	}
	discs := readDiscLog(t, repo).Discs()
	if len(discs) != 1 || discs[0].State != stage.DiscOnDiscOnly {
		t.Fatalf("discs after gc = %+v, want one disc on disc only", discs)
	}
	if n := countByState(t, repo, stage.Packed); n != 0 {
		t.Fatalf("%d item(s) still Packed after gc, want 0", n)
	}
}

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

	// Before the wait is over: gc --dry-run holds every item, names the
	// end of the wait, exits 0 and prints no next line.
	setFakeNow(t, func() time.Time { return before.Add(24 * time.Hour) })
	code, out := runCmd(t, "--repo="+repo, "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run (before retention): exit %d, want 0: %s", code, out)
	}
	if !strings.HasPrefix(out, "gc: would free 0 item(s), 0 bytes\ngc: disc 0: too soon; ") {
		t.Fatalf("gc --dry-run (before retention) output %q, want 0 items and the too soon line", out)
	}
	if strings.Contains(out, "next:") {
		t.Fatalf("gc --dry-run output %q holds a next line", out)
	}

	// After the wait: gc --dry-run reports what it would free, and
	// changes nothing.
	setFakeNow(t, func() time.Time { return before.Add(8 * 24 * time.Hour) })
	code, out = runCmd(t, "--repo="+repo, "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run (after retention): exit %d: %s", code, out)
	}
	if !strings.HasPrefix(out, "gc: would free ") || strings.HasPrefix(out, "gc: would free 0 ") {
		t.Fatalf("gc --dry-run (after retention) output %q, want more than 0 items", out)
	}
	objDir := testLayout(t, repo).chunksDir()
	before1, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if before1 == 0 {
		t.Fatal("staging holds no chunk file before gc; test fixture produced nothing to delete")
	}

	// A real run actually deletes, and is reflected in the object count
	// on disk.
	code, out = runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if strings.HasPrefix(out, gcFreedNone) {
		t.Fatalf("gc output %q, want more than 0 items freed", out)
	}
	after1, err := countFiles(objDir)
	if err != nil {
		t.Fatal(err)
	}
	if after1 >= before1 {
		t.Fatalf("staging has %d chunk files after gc, had %d before; want fewer", after1, before1)
	}

	// A second gc run finds nothing left to do; nothing eligible is
	// success, not a failure.
	code, out = runCmd(t, "--repo="+repo, "gc")
	if want := gcFreedNone + "next: noahsark status\n"; code != 0 || out != want {
		t.Fatalf("gc (second run): exit %d, output %q; want exit 0, output %q", code, out, want)
	}
}

// TestGCPlanTakesTheIndexOfTheObjectsOwnDisc writes two discs that
// carry the same run_seq, as two discs do after a lost repository. gc
// must confirm a staged object in the catalog INDEX of the disc its own
// state record names. The INDEX of the other disc must never stand in
// for it.
func TestGCPlanTakesTheIndexOfTheObjectsOwnDisc(t *testing.T) {
	stagingDir := t.TempDir()
	discA := [16]byte{0xaa}
	discB := [16]byte{0xbb}
	const sharedRunSeq = 5
	onDiscA := object.ComputeID(format.ObjectKindChunk, []byte("an object disc A holds"))
	onDiscB := object.ComputeID(format.ObjectKindChunk, []byte("an object staged for disc B, named by disc A's INDEX alone"))

	c, err := catalog.Open(t.TempDir())
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

	layout := repoLayout{repo: t.TempDir(), staging: stagingDir}
	for _, tc := range []struct {
		disc         [16]byte
		item         object.ID
		wantObjs     int
		wantUnlisted int
	}{
		{discA, onDiscA, 1, 0},
		{discB, onDiscB, 0, 1},
	} {
		idx, err := c.IndexForDisc(tc.disc)
		if err != nil {
			t.Fatal(err)
		}
		objs, unlisted := gcPlanDisc(tc.disc, []object.ID{tc.item}, idx, layout)
		if len(objs) != tc.wantObjs || unlisted != tc.wantUnlisted {
			t.Fatalf("disc %x: %d file(s), %d unlisted; want %d and %d", tc.disc[0], len(objs), unlisted, tc.wantObjs, tc.wantUnlisted)
		}
		if len(objs) == 1 && (objs[0].id != tc.item || objs[0].discUUID != tc.disc) {
			t.Fatalf("file = %s on disc %v, want %s on disc %v", objs[0].id.TextForm(), objs[0].discUUID, tc.item.TextForm(), tc.disc)
		}
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

	objDir := testLayout(t, repo).chunksDir()
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
		t.Fatalf("staging has %d chunk file(s), %v; want the %d orphans left behind", n, err, staged)
	}
	if n := countByState(t, repo, stage.OnDisc); n == 0 {
		t.Fatal("gc unlinked before it recorded: no ON-DISC record survived the failed unlink")
	}
	if n := countByState(t, repo, stage.Packed); n != 0 {
		t.Fatalf("%d item(s) still Packed; the record must go to the disk before the unlink", n)
	}
	if discs := readDiscLog(t, repo).Discs(); len(discs) != 1 || discs[0].State != stage.DiscOnDiscOnly {
		t.Fatalf("discs = %+v, want the Freed event before the unlink", discs)
	}

	gcRemove = oldRemove
	code, out = runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc (second run): exit %d, want 0: %s", code, out)
	}
	if n, err := countFiles(objDir); err != nil || n != 0 {
		t.Fatalf("staging has %d chunk file(s), %v; want the orphans freed", n, err)
	}
}

// TestGCAfterACrashBeforeTheFreedEvent writes the OnDisc records of a
// verified disc and no Freed event, as a crash between the two batches
// leaves. The next gc appends Freed and unlinks the chunk files.
func TestGCAfterACrashBeforeTheFreedEvent(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscVerified)
	logs, err := stage.OpenLogs(testLayout(t, fx.repo).stateDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Items.MarkOnDisc(logs.Items.ItemsOfDiscInState(fx.uuidBytes(t), stage.Packed)...); err != nil {
		t.Fatal(err)
	}

	out := fx.mustRun(t, "gc", "--force-after=0d")
	if strings.HasPrefix(out, gcFreedNone) {
		t.Fatalf("gc output %q, want the orphans freed", out)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscOnDiscOnly {
		t.Fatalf("disc state %s, want on disc only", got)
	}
	if files := listFilesUnder(t, testLayout(t, fx.repo).chunksDir()); len(files) != 0 {
		t.Fatalf("staging chunks after gc: %v, want none", files)
	}
}

// TestGCFreesThePlanDirectory checks that gc keeps a disc's plan
// directory while the disc is packed, names
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
	stagedTree := packedTreeDir(t, repo, packOut)
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
		t.Fatalf("verify: exit %d: %s", code, out)
	}
	setFakeNow(t, func() time.Time { return before.Add(16 * 24 * time.Hour) })

	// A sparse image, as image build leaves it, frees its allocated
	// blocks only, never its apparent size.
	const sparseSize = 1 << 30
	img, err := os.Create(filepath.Join(planDir, "tree.img"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := img.WriteAt([]byte("image head"), 0); err != nil {
		t.Fatal(err)
	}
	if err := img.Truncate(sparseSize); err != nil {
		t.Fatal(err)
	}
	if err := img.Close(); err != nil {
		t.Fatal(err)
	}
	u, err := decodeUUID(strings.ReplaceAll(discUUID, "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	var want uint64
	for _, id := range readLogs(t, repo).Items.ItemsOfDiscInState(u, stage.Packed) {
		want += allocatedBytesUnder(t, testLayout(t, repo).chunkFile(id))
	}
	want += allocatedBytesUnder(t, planDir)
	if want >= sparseSize {
		t.Fatalf("the files to free hold %d bytes of disk space; want less than the sparse image size %d", want, sparseSize)
	}

	code, out := runCmd(t, "--repo="+repo, "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run: exit %d: %s", code, out)
	}
	if strings.HasPrefix(out, "gc: would free 0 ") {
		t.Fatalf("gc --dry-run output %q, want items to free", out)
	}
	if got := gcLineBytes(t, out); got != want {
		t.Fatalf("gc --dry-run output %q: %d bytes, want the %d allocated bytes", out, got, want)
	}
	if _, err := os.Stat(planDir); err != nil {
		t.Fatalf("gc --dry-run removed the plan directory: %v", err)
	}

	code, out = runCmd(t, "--repo="+repo, "gc")
	if code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if got := gcLineBytes(t, out); got != want {
		t.Fatalf("gc output %q: %d bytes, want the %d allocated bytes", out, got, want)
	}
	if _, err := os.Stat(planDir); !os.IsNotExist(err) {
		t.Fatalf("gc kept the plan directory of an ON-DISC disc: %v", err)
	}
}

// TestGCForceAfterShortensTheWait runs gc --force-after with a fake
// clock 2 hours past the verified time: nowhere near the 7-day wait,
// but past a 1-hour --force-after. gc frees the disc and asks nothing.
func TestGCForceAfterShortensTheWait(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	setFakeNow(t, func() time.Time { return before.Add(2 * time.Hour) })

	code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=1h")
	if code != 0 {
		t.Fatalf("gc --force-after=1h: exit %d: %s", code, out)
	}
	if strings.Contains(out, "?") {
		t.Fatalf("gc output %q asks a question; gc asks no confirmation", out)
	}
	if strings.HasPrefix(out, gcFreedNone) {
		t.Fatalf("gc output %q, want more than 0 items freed", out)
	}
}

// TestGCForceAfterDryRunChangesNothing checks that --dry-run with
// --force-after reports what gc would free and changes nothing.
func TestGCForceAfterDryRunChangesNothing(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	before := time.Now()
	packAndVerifyDisc(t, work, repo, src)
	setFakeNow(t, func() time.Time { return before.Add(2 * time.Hour) })
	chunksBefore, err := countFiles(testLayout(t, repo).chunksDir())
	if err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=1h", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --force-after=1h --dry-run: exit %d: %s", code, out)
	}
	if strings.HasPrefix(out, "gc: would free 0 ") || strings.Contains(out, "next:") {
		t.Fatalf("gc --dry-run output %q, want more than 0 items and no next line", out)
	}
	if n, err := countFiles(testLayout(t, repo).chunksDir()); err != nil || n != chunksBefore {
		t.Fatalf("chunk files = %d, %v after --dry-run; want %d", n, err, chunksBefore)
	}
	if discs := readDiscLog(t, repo).Discs(); len(discs) != 1 || discs[0].State != stage.DiscVerified {
		t.Fatalf("discs after --dry-run = %+v, want the disc still verified", discs)
	}
}

// TestGCApplyStagingObjectsSkipsAlreadyGoneFile checks that a gcObj
// whose staged file does not exist frees no bytes and is not counted
// deleted: only a file that gc actually removed counts.
func TestGCApplyStagingObjectsSkipsAlreadyGoneFile(t *testing.T) {
	dir := t.TempDir()
	id := object.ComputeID(format.ObjectKindChunk, []byte("gone"))
	objs := []gcObj{{id: id, path: filepath.Join(dir, "no", "such-file"), size: 1234}}

	deleted, bytesFreed, failures := gcApplyStagingObjects(objs)
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0: an already-gone file frees nothing this run", deleted)
	}
	if bytesFreed != 0 {
		t.Fatalf("bytesFreed = %d, want 0", bytesFreed)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none: an already-gone file is not a failure", failures)
	}
}

// TestGCApplyStagingObjectsCountsRealDelete is the control: a gcObj
// whose file exists is removed, counted, and its bytes freed.
func TestGCApplyStagingObjectsCountsRealDelete(t *testing.T) {
	dir := t.TempDir()
	id := object.ComputeID(format.ObjectKindChunk, []byte("present"))
	path := filepath.Join(dir, "chunks", "present")
	if err := writeFile(path, "payload"); err != nil {
		t.Fatal(err)
	}

	objs := []gcObj{{id: id, path: path, size: 7}}

	deleted, bytesFreed, failures := gcApplyStagingObjects(objs)
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if bytesFreed != 7 {
		t.Fatalf("bytesFreed = %d, want 7", bytesFreed)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file is still there: %v", err)
	}
}

// TestGCFailsFastWhenRepoLockHeld checks that gc refuses to run while
// another command holds the repository lock. A held lock is a failure at
// run time: exit 1. gc --dry-run takes no lock, and runs.
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

	code, out := runCmd(t, "--repo="+repo, "gc")
	if code != 1 {
		t.Fatalf("gc while locked: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "repository lock") {
		t.Fatalf("gc while locked output %q, want it to name the repository lock", out)
	}
	if !strings.Contains(out, "another noahsark command runs on this repository") {
		t.Fatalf("gc while locked output %q, want the other-command message", out)
	}
	if code, out := runCmd(t, "--repo="+repo, "gc", "--dry-run"); code != 0 {
		t.Fatalf("gc --dry-run while locked: exit %d, want 0: %s", code, out)
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
	if code, out := runCmd(t, "--repo="+repo, "gc"); code != 0 {
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
	statePath := testLayout(t, repo).stateLogFile()
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
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, data) {
		t.Fatal("gc --dry-run changed the state log; a dry run writes no file")
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

// gcLineBytes returns B of the first line of gc output: the freed line.
func gcLineBytes(t *testing.T, out string) uint64 {
	t.Helper()
	line, _, _ := strings.Cut(out, "\n")
	_, rest, ok := strings.Cut(line, " item(s), ")
	if !ok {
		t.Fatalf("gc output %q has no freed line", out)
	}
	n, err := strconv.ParseUint(strings.TrimSuffix(rest, " bytes"), 10, 64)
	if err != nil {
		t.Fatalf("gc freed line %q: %v", line, err)
	}
	return n
}
