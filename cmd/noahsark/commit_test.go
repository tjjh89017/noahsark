package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// writeExcludeFixture builds a source tree with paths an exclude pattern
// test can target: a name matched at every depth, an anchored path, and
// a directory-only pattern's target.
func writeExcludeFixture(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	mustMkdirCmd(t, filepath.Join(src, "node_modules"))
	mustWriteCmd(t, filepath.Join(src, "node_modules", "x.js"), "dep")
	mustWriteCmd(t, filepath.Join(src, "a.tmp"), "temp")
	mustMkdirCmd(t, filepath.Join(src, "build", "out"))
	mustWriteCmd(t, filepath.Join(src, "build", "out", "x"), "built")
	mustMkdirCmd(t, filepath.Join(src, "keep", "build", "out"))
	mustWriteCmd(t, filepath.Join(src, "keep", "build", "out", "y"), "kept")
	mustWriteCmd(t, filepath.Join(src, "keep.txt"), "keep")
	return src
}

func mustMkdirCmd(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteCmd(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// packAndLs packs repo's staged snapshot onto a disc, then runs
// "ls --recursive" from the catalog, and returns the output.
func packAndLs(t *testing.T, repo, snapID string) string {
	t.Helper()
	treeDir := filepath.Join(t.TempDir(), "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "ls", "--recursive", snapID)
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, out)
	}
	return out
}

func TestCommitExcludeFlag(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeExcludeFixture(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "commit", "--exclude=node_modules/", "--exclude=*.tmp", "--exclude=/build/out", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "excluded: 3 path(s)") {
		t.Fatalf("commit output %q, want an excluded summary line of 3", out)
	}

	ls := packAndLs(t, repo, snapshotIDFromCommit(t, out))
	for _, want := range []string{"node_modules", "a.tmp", "keep.txt"} {
		present := strings.Contains(ls, want)
		wantPresent := want == "keep.txt"
		if present != wantPresent {
			t.Errorf("ls output contains %q = %v, want %v\n%s", want, present, wantPresent, ls)
		}
	}
	if !strings.Contains(ls, "keep/build/out/y") {
		t.Errorf("ls output missing keep/build/out/y (only /build/out is anchored, not keep/build/out):\n%s", ls)
	}
	if strings.Contains(ls, "build/out/x") && !strings.Contains(ls, "keep/build/out") {
		t.Errorf("ls output should not contain the root build/out/x:\n%s", ls)
	}
}

func TestCommitExcludeBadFlagPatternIsUsageError(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeExcludeFixture(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "commit", "--exclude=!keep.txt", src)
	if code != 2 {
		t.Fatalf("commit: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "negation is not supported") {
		t.Fatalf("commit output %q, want it to name the negation problem", out)
	}
}

func TestCommitNoahsarkIgnoreFile(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeExcludeFixture(t)
	mustWriteCmd(t, filepath.Join(src, ignoreFileName), "# comment\n\nnode_modules/\n*.tmp\n/build/out\n")

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	ls := packAndLs(t, repo, snapshotIDFromCommit(t, out))
	if strings.Contains(ls, "a.tmp") || strings.Contains(ls, "node_modules") {
		t.Fatalf("ls output %q should not contain excluded paths", ls)
	}
	if !strings.Contains(ls, ignoreFileName) {
		t.Fatalf("ls output %q should still contain the ignore file itself", ls)
	}
}

func TestCommitNoahsarkIgnoreBadPatternIsConfigError(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeExcludeFixture(t)
	mustWriteCmd(t, filepath.Join(src, ignoreFileName), "!negated\n")

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 2 {
		t.Fatalf("commit: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, ignoreFileName) || !strings.Contains(out, "negation is not supported") {
		t.Fatalf("commit output %q, want it to name the ignore file and the negation problem", out)
	}
}

func TestCommitOneFileSystemFlagExists(t *testing.T) {
	// This build has no seam to fake a real mount for an end-to-end CLI
	// test; internal/object's writer tests cover the device-id logic
	// with a seam. Here, check only that the flag is accepted and that
	// a normal, single-filesystem commit still succeeds with it set.
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", "--one-file-system", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
}

// stagingObjectsSnapshot reports the file count and total byte size of
// repo's chunk files in staging, so a test can check a later call added
// nothing there.
func stagingObjectsSnapshot(t *testing.T, repo string) (files int, bytes int64) {
	t.Helper()
	err := filepath.Walk(testLayout(t, repo).chunksDir(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files++
			bytes += info.Size()
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return files, bytes
}

// TestCommitAfterGCDoesNotRefillStaging checks the severe staging-refill
// bug: once an object's disc has been burned, verified and gc'd, a
// commit that references it again must not re-stage it. The object
// already lives on a disc gc considered CLEAN before freeing it; a
// re-commit finding the same content must count it existing and leave
// staging untouched.
func TestCommitAfterGCDoesNotRefillStaging(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	packAndVerifyDisc(t, work, repo, src)

	setFakeStdin(t, strings.NewReader("y\n"))
	code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=0d")
	if code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if n := countByState(t, repo, stage.Staged); n != 0 {
		t.Fatalf("Staged objects after gc = %d, want 0", n)
	}

	filesBefore, bytesBefore := stagingObjectsSnapshot(t, repo)
	deletedBefore := countByState(t, repo, stage.OnDisc)

	// A commit of the same, unchanged source must find every chunk,
	// blob and tree already on the disc gc just freed, and must not
	// write any of them back into staging.
	code, out = runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("re-commit: exit %d: %s", code, out)
	}
	// The snapshot object is always new, since its timestamp makes every
	// commit's snapshot id unique; every chunk, blob and tree beneath it
	// must count existing instead.
	if !strings.Contains(out, "new items: 1, existing items:") {
		t.Fatalf("re-commit output %q, want new items: 1 (the snapshot only)", out)
	}

	filesAfter, bytesAfter := stagingObjectsSnapshot(t, repo)
	if filesAfter != filesBefore {
		t.Fatalf("staging chunk file count = %d, want unchanged %d; commit output: %s", filesAfter, filesBefore, out)
	}
	if bytesAfter != bytesBefore {
		t.Fatalf("staging chunk bytes = %d, want unchanged %d", bytesAfter, bytesBefore)
	}

	// Only the new snapshot object enters the state log Staged; every
	// chunk, blob and tree it references stays exactly where gc left
	// it, Deleted, not resurrected back to Staged.
	if n := countByState(t, repo, stage.Staged); n != 1 {
		t.Fatalf("Staged objects after re-commit = %d, want 1 (the new snapshot only)", n)
	}
	if n := countByState(t, repo, stage.OnDisc); n != deletedBefore {
		t.Fatalf("Deleted objects after re-commit = %d, want unchanged %d", n, deletedBefore)
	}
}

var stagedLineRe = regexp.MustCompile(`staged: (\d+) items, (\d+) bytes`)

// TestCommitPrintsStagedTotals checks that commit prints the
// repository-wide staged total after its own summary, and that the
// total drops to zero once everything staged has been packed.
func TestCommitPrintsStagedTotals(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	m := stagedLineRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("commit output %q has no staged line", out)
	}
	if m[1] == "0" || m[2] == "0" {
		t.Fatalf("staged line = %q, want nonzero objects and bytes after a fresh commit", m[0])
	}

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out = runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	m = stagedLineRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("second commit output %q has no staged line", out)
	}
	// The source is unchanged, so every tree, blob and chunk is already
	// packed and only the new commit's own snapshot object is staged.
	if m[1] != "1" {
		t.Fatalf("staged line = %q, want 1 object staged (the new snapshot)", m[0])
	}
}

// TestCommitWithNoArgUsesConfiguredSource checks that commit with no
// SOURCE positional argument falls back to init --source's config value.
func TestCommitWithNoArgUsesConfiguredSource(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init", "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "commit")
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "snapshot ") {
		t.Fatalf("commit output %q has no snapshot line", out)
	}
}

// TestCommitArgOverridesConfiguredSource checks that a SOURCE given on
// commit's own command line wins over the configured source root.
func TestCommitArgOverridesConfiguredSource(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	configured := writeFixtureSource(t)
	override := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init", "--source="+configured); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "commit", override)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "snapshot ") {
		t.Fatalf("commit output %q has no snapshot line", out)
	}
}

// TestCommitWithNeitherArgNorConfigFails checks that commit refuses to
// run, with a clear message, when no SOURCE was given and the config
// holds no source root.
func TestCommitWithNeitherArgNorConfigFails(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "commit")
	if code != 2 {
		t.Fatalf("commit: exit %d, want 2: %s", code, out)
	}
	want := "no SOURCE given and no source root in the config; pass a path on the command line, or set sources.root in the config"
	if !strings.Contains(out, want) {
		t.Fatalf("commit output %q, want it to contain %q", out, want)
	}
}

// TestTwoCommitsOneDayMoveOneRefAndKeepBoth commits two times with no
// --ref. Both commits take the same ref name, the date of today, so the
// ref moves to the newer snapshot. The older snapshot must stay
// reachable: log still lists it.
func TestTwoCommitsOneDayMoveOneRefAndKeepBoth(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("first commit: exit %d: %s", code, out)
	}
	first := snapshotIDFromCommit(t, out)
	if err := os.WriteFile(filepath.Join(src, "second.txt"), []byte("content of the second commit"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	second := snapshotIDFromCommit(t, out)
	if first == second {
		t.Fatal("the two commits gave one snapshot id")
	}
	if !strings.Contains(out, "ref "+defaultRefName()+" -> "+second) {
		t.Fatalf("second commit output %q does not move the date ref to the newer snapshot", out)
	}

	code, logOut := runCmd(t, "--repo="+repo, "log")
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, logOut)
	}
	if !strings.Contains(logOut, logID(t, first)) {
		t.Fatalf("log output %q does not reach the older snapshot %s", logOut, first)
	}
	if !strings.Contains(logOut, logID(t, second)) {
		t.Fatalf("log output %q does not name the newer snapshot %s", logOut, second)
	}
}

// TestCommitExitsOneAndReportsAnUnstablePath uses the newWriter seam to
// install a Stat function that never lets one target file's two stats
// agree, forcing the in-flight change detection to flag it UNSTABLE on
// every commit. It asserts commit exits 1 and prints the path.
func TestCommitExitsOneAndReportsAnUnstablePath(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	target, err := filepath.Abs(filepath.Join(src, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	oldNewWriter := newWriter
	defer func() { newWriter = oldNewWriter }()
	var calls int
	newWriter = func(chunkPath, metaPath object.PathFunc) *object.Writer {
		w := object.NewWriter(chunkPath, metaPath)
		w.Stat = func(path string) (os.FileInfo, error) {
			real, err := os.Lstat(path)
			if err != nil {
				return nil, err
			}
			if path != target {
				return real, nil
			}
			calls++
			return fakeStatInfo{FileInfo: real, size: real.Size() + int64(calls)}, nil
		}
		return w
	}

	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 1 {
		t.Fatalf("commit: exit %d, want 1; output: %s", code, out)
	}
	if !strings.Contains(out, "unstable a.txt branch=flagged") {
		t.Fatalf("output = %q, want an unstable line naming a.txt", out)
	}
	if !strings.Contains(out, "unstable: 1, skipped: 0") {
		t.Fatalf("output = %q, want the unstable/skipped count line", out)
	}
}

// TestCommitRecordsStagedBeforeMovingRef replaces refs.txt with a
// directory, so the ref move step fails after Commit itself succeeds.
// It asserts that every object Commit reached, and the snapshot object
// itself, already carry a Staged state log record. commit must record
// every new object as STAGED before it moves the ref: a crash or a
// failure between the two steps must never leave a ref pointing at a
// snapshot whose objects pack cannot find, since pack only ever places
// an id it finds STAGED.
func TestCommitRecordsStagedBeforeMovingRef(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	// A directory in refs.txt's place makes writeRefs's os.WriteFile
	// fail, without needing a permission trick that root would ignore.
	layout := testLayout(t, repo)
	if err := os.MkdirAll(layout.refsFile(), 0o755); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 1 {
		t.Fatalf("commit: exit %d, want 1 (the ref move must fail); output: %s", code, out)
	}

	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	snapIDs, err := c.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapIDs) != 1 {
		t.Fatalf("the catalog holds %d snapshots, want exactly 1", len(snapIDs))
	}
	snapID := snapIDs[0]

	l := openTestLog(t, repo)
	rec, ok := l.Get(snapID)
	if !ok || rec.State != stage.Staged {
		t.Fatalf("snapshot %s state = %+v, ok=%v, want a Staged record despite the ref move failing", snapID.TextForm(), rec, ok)
	}
}

// TestProgressFlags checks that commit writes its progress line only
// when standard error is a terminal, and that -q and --quiet turn it
// off. --no-progress is not an option.
func TestProgressFlags(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 || strings.Contains(out, "\r") {
		t.Fatalf("no terminal: exit %d, expected no commit progress line, got %q", code, out)
	}

	te := newTestEnv(work)
	te.stderrTTY = true
	if code, _ := te.run("--repo="+repo, "commit", src); code != 0 || !strings.Contains(te.errOut.String(), "commit") {
		t.Fatalf("terminal: exit %d, expected a commit progress line, got %q", code, te.errOut.String())
	}
	for _, q := range []string{"-q", "--quiet"} {
		if code, _ := te.run(q, "--repo="+repo, "commit", src); code != 0 || te.errOut.Len() != 0 {
			t.Fatalf("%s on a terminal: exit %d, expected no progress line, got %q", q, code, te.errOut.String())
		}
	}

	if code, out := runCmd(t, "--no-progress", "--repo="+repo, "commit", src); code != 2 {
		t.Fatalf("--no-progress: exit %d, want 2: %s", code, out)
	}
}

// TestCommitPrintsExcludedOnlyWhenSomethingWasExcluded checks that the
// excluded line is absent on a commit that excluded nothing.
func TestCommitPrintsExcludedOnlyWhenSomethingWasExcluded(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init", "--source="+src); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit")
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if strings.Contains(out, "excluded:") {
		t.Fatalf("commit output %q prints an excluded line with nothing excluded", out)
	}

	code, out = runCmd(t, "--repo="+repo, "commit", "--exclude=*.txt", src)
	if code != 0 {
		t.Fatalf("commit with an exclude: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "excluded:") {
		t.Fatalf("commit output %q, want the excluded line when a path was excluded", out)
	}
}

// TestCommitWarnsAboutASpecialFile checks that commit names a FIFO in
// the source, says it carries no content, counts it, and still exits 0:
// a special file is normal in a source tree, and the operator must hear
// about it while the source is still there.
func TestCommitWarnsAboutASpecialFile(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	fifo := filepath.Join(src, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo is not available here: %v", err)
	}
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d, want 0 for a special file alone: %s", code, out)
	}
	if !strings.Contains(out, "pipe: FIFO, no content is backed up") {
		t.Fatalf("commit output %q does not warn about the FIFO", out)
	}
	if !strings.Contains(out, "special files: 1") {
		t.Fatalf("commit output %q has no special-file count", out)
	}
}

// TestCommitMessageFlag asserts that -m accepts a commit message.
func TestCommitMessageFlag(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", "-m", "hello world", src); code != 0 {
		t.Fatalf("commit -m: exit %d: %s", code, out)
	}
}

// TestCommitUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// commit: each case exits 2, never 0 or 1.
func TestCommitUsageErrorsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"commit", "--no-such-flag", "/nowhere"}},
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
