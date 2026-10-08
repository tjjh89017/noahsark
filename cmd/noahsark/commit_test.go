package main

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/durable"
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
	code, out := runCmd(t, "--repo="+repo, "gc")
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

// TestCommitAndStatusCountOnlyStagedFilesThatExist removes the chunk
// files of a commit, as an operator who empties the staging directory
// does. A commit of another source and status then count only the files
// that exist and warn with the count of the rest; commit exits 0 and
// status exits 1. A commit of the same source writes the files again,
// and the warning goes away.
func TestCommitAndStatusCountOnlyStagedFilesThatExist(t *testing.T) {
	repo, src := initAndCommit(t)
	removed := 0
	err := filepath.WalkDir(testLayout(t, repo).chunksDir(), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		removed++
		return os.Remove(path)
	})
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("the commit wrote no chunk file")
	}
	warning := fmt.Sprintf("warning: %d staged item(s) have no file in the staging store; commit the same source again", removed)

	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "other.txt"), []byte("another source"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", other)
	if code != 0 || !strings.Contains(out, "noahsark: commit: "+warning) || stagedLineRe.FindString(out) == "" {
		t.Fatalf("commit of another source: exit %d, want 0, the staged line and the warning: %s", code, out)
	}
	code, out = runCmd(t, "--repo="+repo, "status")
	if code != 1 || !strings.Contains(out, "noahsark: status: "+warning) || stagedLineRe.FindString(out) == "" {
		t.Fatalf("status: exit %d, want 1, the staged line and the warning: %s", code, out)
	}

	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 || strings.Contains(out, "have no file") {
		t.Fatalf("commit of the same source: exit %d, want 0 and no warning: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "status"); code != 0 || strings.Contains(out, "have no file") {
		t.Fatalf("status after the commit of the same source: exit %d, want 0 and no warning: %s", code, out)
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
// every commit. It asserts commit exits 1 and prints the path in one
// line, escaped as ls escapes a name: the name of the file holds a
// newline.
func TestCommitExitsOneAndReportsAnUnstablePath(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	target, err := filepath.Abs(filepath.Join(src, "a\nb.txt"))
	if err != nil {
		t.Fatal(err)
	}
	mustWriteCmd(t, target, "changes during the read")

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
	if !slices.Contains(strings.Split(out, "\n"), `unstable a\nb.txt: the file changed during the read`) {
		t.Fatalf("output = %q, want an unstable line naming a\\nb.txt", out)
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

	te := newTestEnv(t.TempDir())
	code, _ := te.run("--repo="+repo, "commit", src)
	stdout, stderr := te.out.String(), te.errOut.String()
	if code != 0 {
		t.Fatalf("commit: exit %d, want 0 for a special file alone: %s%s", code, stdout, stderr)
	}
	lines := strings.Split(stdout, "\n")
	if !slices.Contains(lines, "special pipe: FIFO, no content is backed up") {
		t.Fatalf("commit output %q has no special line for the FIFO", stdout)
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "special files: 1;") }) {
		t.Fatalf("commit output %q has no special-file count", stdout)
	}
	if strings.Contains(stdout, "warning:") || stderr != "" {
		t.Fatalf("commit printed a warning, want only records on standard output: stdout %q, stderr %q", stdout, stderr)
	}
}

// TestCommitEscapesASkippedPath gives the source a name that a tree entry
// cannot hold, with a newline in it. The skipped line names the path in
// one line, escaped as ls escapes a name.
func TestCommitEscapesASkippedPath(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	mustWriteCmd(t, filepath.Join(src, "bad\\name\nnext"), "data")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 1 {
		t.Fatalf("commit: exit %d, want 1 for a skipped path: %s", code, out)
	}
	want := `skipped bad\\name\nnext: the name holds a backslash, which a tree entry name must not hold`
	if !slices.Contains(strings.Split(out, "\n"), want) {
		t.Fatalf("commit output %q, want the line %q", out, want)
	}
}

// TestCommitRefusesABadRefName gives commit a ref name that a line of
// refs.txt or a REFS record cannot hold. commit exits 2, names the rule,
// and writes nothing.
func TestCommitRefusesABadRefName(t *testing.T) {
	for _, name := range []string{"", "a b", "a\tb", "a\x01b", "a\x7fb", "caf\u00e9", strings.Repeat("r", 41)} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "repo")
			src := writeFixtureSource(t)
			if code, out := runIn(t, repo, "init"); code != 0 {
				t.Fatalf("init: exit %d: %s", code, out)
			}
			code, out := runCmd(t, "--repo="+repo, "commit", "--ref="+name, src)
			if code != 2 {
				t.Fatalf("commit --ref=%q: exit %d, want 2: %s", name, code, out)
			}
			if !strings.Contains(out, "1 to 40 bytes of printable ASCII, with no space") {
				t.Fatalf("commit --ref=%q: output %q does not give the rule", name, out)
			}
			layout := testLayout(t, repo)
			if _, err := os.Stat(layout.refsFile()); !os.IsNotExist(err) {
				t.Fatalf("commit --ref=%q wrote refs.txt: %v", name, err)
			}
			if files := listFilesUnder(t, layout.catalogDir()); slices.ContainsFunc(files, func(f string) bool { return strings.HasPrefix(f, "snapshots") }) {
				t.Fatalf("commit --ref=%q wrote a snapshot: %v", name, files)
			}
			if code, out := runCmd(t, "--repo="+repo, "log"); code == 2 || strings.Contains(out, "malformed") {
				t.Fatalf("log after the refused commit: exit %d: %s", code, out)
			}
		})
	}
}

// TestCommitAcceptsARefOfFortyBytes gives commit a ref name at the limit.
// log then names it.
func TestCommitAcceptsARefOfFortyBytes(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	name := strings.Repeat("r", 39) + "~"
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref="+name, src); code != 0 {
		t.Fatalf("commit --ref=%s: exit %d: %s", name, code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "log"); code != 0 || !strings.Contains(out, name) {
		t.Fatalf("log: exit %d, want the ref %s: %s", code, name, out)
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

// TestCommitSkipsANameTheTreeFormatForbids gives the source a file and a
// directory whose names hold a backslash. commit skips both, names each
// in the skipped list and exits 1. The snapshot decodes, and ls lists it.
func TestCommitSkipsANameTheTreeFormatForbids(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	mustWriteCmd(t, filepath.Join(src, `back\slash.txt`), "forbidden name")
	mustMkdirCmd(t, filepath.Join(src, `dir\name`))
	mustWriteCmd(t, filepath.Join(src, `dir\name`, "inner.txt"), "inner")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 1 {
		t.Fatalf("commit: exit %d, want 1: %s", code, out)
	}
	for _, want := range []string{
		"unstable: 0, skipped: 2\n",
		`skipped back\\slash.txt: the name holds a backslash, which a tree entry name must not hold` + "\n",
		`skipped dir\\name: the name holds a backslash, which a tree entry name must not hold` + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("commit output %q has no line %q", out, want)
		}
	}

	snap := snapshotIDFromCommit(t, out)
	lsCode, ls, errOut := runLs(t, repo, "ls", "-R", snap)
	if lsCode != 0 {
		t.Fatalf("ls -R: exit %d: %s", lsCode, errOut)
	}
	if strings.Contains(ls, "slash") || strings.Contains(ls, "inner.txt") {
		t.Fatalf("ls -R output %q lists a skipped path", ls)
	}
	if !strings.Contains(ls, "a.txt") {
		t.Fatalf("ls -R output %q does not list a.txt", ls)
	}
}

// TestCommitSyncsEveryDirectoryBeforeTheStateLog checks the durable order
// of commit: each directory that got an object file is synced before
// the state log gets a record. A crash after the log write then finds
// each object that the log names.
func TestCommitSyncsEveryDirectoryBeforeTheStateLog(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	stateLog := testLayout(t, repo).stateLogFile()
	logSize := func() int64 {
		fi, err := os.Stat(stateLog)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	before := logSize()

	oldNewWriter := newWriter
	defer func() { newWriter = oldNewWriter }()
	synced := map[string]bool{}
	newWriter = func(chunkPath, metaPath object.PathFunc) *object.Writer {
		w := object.NewWriter(chunkPath, metaPath)
		w.SyncDir = func(dir string) error {
			if n := logSize(); n != before {
				t.Errorf("directory %s synced after the state log grew from %d to %d bytes", dir, before, n)
			}
			synced[dir] = true
			return durable.SyncDir(dir)
		}
		return w
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if logSize() == before {
		t.Fatal("commit wrote no state log record")
	}
	layout := testLayout(t, repo)
	for _, root := range []string{layout.chunksDir(), layout.catalogDir()} {
		for _, rel := range listFilesUnder(t, root) {
			if dir := filepath.Dir(filepath.Join(root, rel)); !synced[dir] {
				t.Errorf("directory %s holds a new object, but commit did not sync it", dir)
			}
		}
	}
}

// TestCommitStagesOnlyWhatTheSnapshotReaches gives a file other content
// on its first read, as a file that changes during the read. The first
// read gets no Staged record, and one pack takes every Staged item.
func TestCommitStagesOnlyWhatTheSnapshotReaches(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := filepath.Join(work, "src")
	mustMkdirCmd(t, src)
	target := filepath.Join(src, "f.bin")
	data := make([]byte, 600_000)
	rand.New(rand.NewSource(7)).Read(data)
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	oldNewWriter := newWriter
	defer func() { newWriter = oldNewWriter }()
	opens, stats := 0, 0
	newWriter = func(chunkPath, metaPath object.PathFunc) *object.Writer {
		w := object.NewWriter(chunkPath, metaPath)
		w.Open = func(path string) (io.ReadCloser, error) {
			if path == target {
				opens++
				if opens == 1 {
					other := make([]byte, 600_000)
					rand.New(rand.NewSource(8)).Read(other)
					return io.NopCloser(bytes.NewReader(other)), nil
				}
			}
			return os.Open(path)
		}
		w.Stat = func(path string) (os.FileInfo, error) {
			real, err := os.Lstat(path)
			if err != nil || path != target {
				return real, err
			}
			stats++
			if stats >= 2 {
				return fakeStatInfo{FileInfo: real, size: real.Size() + 1}, nil
			}
			return real, nil
		}
		return w
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	newWriter = oldNewWriter
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	// The snapshot, two trees, one blob and one chunk.
	if !strings.Contains(out, "staged: 5 items,") {
		t.Fatalf("commit output %q, want staged: 5 items", out)
	}
	if n := countByState(t, repo, stage.Staged); n != 5 {
		t.Fatalf("Staged items = %d, want 5", n)
	}
	if files := listFilesUnder(t, testLayout(t, repo).chunksDir()); len(files) != 1 {
		t.Fatalf("chunk files = %v, want the one chunk of the second read", files)
	}
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=bd25", "--out="+filepath.Join(work, "d0")); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if n := countByState(t, repo, stage.Staged); n != 0 {
		t.Fatalf("Staged items after pack = %d, want 0", n)
	}
}

// TestCommitLeavesTheRepositoryOutOfTheSource puts the repository, a
// staging store outside the repository, and a pack --out directory in
// the source. commit leaves each out, names each on one line, and exits
// 0. A later commit of the same source adds no chunk file.
func TestCommitLeavesTheRepositoryOutOfTheSource(t *testing.T) {
	src := writeSeededSource(t, 42, 2)
	repo := filepath.Join(src, "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	cfgText, err := os.ReadFile(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	cfgText = []byte(strings.Replace(string(cfgText), "dir: staging", "dir: "+filepath.Join(src, "stage"), 1))
	if err := os.WriteFile(configPath(repo), cfgText, 0o644); err != nil {
		t.Fatal(err)
	}
	// The repository through a symlink: the walk compares device and
	// inode, not the path text.
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(src, alias); err != nil {
		t.Fatal(err)
	}
	repoArg := "--repo=" + filepath.Join(alias, "repo")

	code, out := runCmd(t, repoArg, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	for _, want := range []string{"excluded repo: the repository\n", "excluded stage: the staging store\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("commit output %q, want line %q", out, want)
		}
	}
	chunks := listFilesUnder(t, testLayout(t, repo).chunksDir())
	if len(chunks) != 2 {
		t.Fatalf("chunk files after the first commit = %v, want the 2 chunks of the source", chunks)
	}

	if code, out := runCmd(t, repoArg, "pack", "--capacity=bd25", "--out="+filepath.Join(src, "out")); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	for i := range 2 {
		code, out = runCmd(t, repoArg, "commit", src)
		if code != 0 {
			t.Fatalf("commit %d: exit %d: %s", i+2, code, out)
		}
		if !strings.Contains(out, "excluded out: the disc root of a pack --out\n") {
			t.Fatalf("commit output %q, want the pack --out line", out)
		}
		if got := listFilesUnder(t, testLayout(t, repo).chunksDir()); !slices.Equal(got, chunks) {
			t.Fatalf("chunk files after commit %d = %v, want the %v of the source only", i+2, got, chunks)
		}
	}
}

// TestCommitRefusesASymlinkSourceRoot checks that commit refuses a
// source root that is a symlink, and names the directory to give.
func TestCommitRefusesASymlinkSourceRoot(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	link := filepath.Join(work, "link")
	if err := os.Symlink(src, link); err != nil {
		t.Fatal(err)
	}
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", link)
	if code != 1 {
		t.Fatalf("commit: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, link+" is a symlink") || !strings.Contains(out, "give the directory that it points to: "+src) {
		t.Fatalf("commit output %q, want the symlink refusal that names %s", out, src)
	}
}
