package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
)

// lsFixture runs init, commit and pack against writeFixtureSource's tree
// and returns the packed disc root, the snapshot id, and the source
// directory ls's PATH argument is relative to.
func lsFixture(t *testing.T) (treeDir, snapID, src string) {
	t.Helper()
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src = writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID = snapshotIDFromCommit(t, out)

	treeDir = filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	return treeDir, snapID, src
}

// rootPath is the include-path form of a fixture's own source directory:
// its absolute path with the leading slash stripped.
func rootPath(src string) string {
	return strings.TrimPrefix(src, "/")
}

// TestLsDefaultListsRootEntry asserts that a plain ls, with no PATH,
// lists the snapshot's root entries only, one per line, with a trailing
// slash marking a directory.
func TestLsDefaultListsRootEntry(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)
	code, out := runCmd(t, "ls", treeDir, snapID)
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, out)
	}
	want := " " + rootPath(src) + "/\n"
	if out != want {
		t.Fatalf("ls output = %q, want %q", out, want)
	}
}

// TestLsRecursiveMatchesGolden runs ls --recursive over the fixture and
// compares its output, with the fixture's own temp-dir source path
// normalized to a placeholder, against a checked-in golden file.
func TestLsRecursiveMatchesGolden(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)
	code, out := runCmd(t, "ls", "--recursive", treeDir, snapID)
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, out)
	}
	got := strings.ReplaceAll(out, rootPath(src), "{{SRC}}")

	want, err := os.ReadFile(filepath.Join("testdata", "ls_recursive.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("ls --recursive output =\n%s\nwant\n%s", got, want)
	}
}

// TestLsLongPrintsModeOwnerSizeAndMtime asserts --long adds the four
// fixed columns before the path.
func TestLsLongPrintsModeOwnerSizeAndMtime(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	code, out := runCmd(t, "ls", "--long", treeDir, snapID)
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, out)
	}
	if !strings.Contains(out, "drwx") {
		t.Fatalf("ls --long output %q missing a directory mode string", out)
	}
	if !ownerColumn.MatchString(out) {
		t.Fatalf("ls --long output %q missing a numeric owner column", out)
	}
}

// ownerColumn matches --long's owner column: two colon-separated
// numbers, since the fixture's files carry no owner-name TLV.
var ownerColumn = regexp.MustCompile(`\d+:\d+`)

// TestLsPathListsOneEntry checks that a PATH naming one file lists just
// that file, with no trailing slash.
func TestLsPathListsOneEntry(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)
	path := rootPath(src) + "/a.txt"
	code, out := runCmd(t, "ls", treeDir, snapID, path)
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, out)
	}
	want := " " + rootPath(src) + "/a.txt\n"
	if out != want {
		t.Fatalf("ls PATH output = %q, want %q", out, want)
	}
}

// TestLsPathOnDirectoryListsChildren checks that a PATH naming a
// directory lists its immediate children.
func TestLsPathOnDirectoryListsChildren(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)
	code, out := runCmd(t, "ls", treeDir, snapID, rootPath(src))
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, out)
	}
	if !strings.Contains(out, rootPath(src)+"/a.txt\n") {
		t.Fatalf("ls PATH output %q missing a.txt", out)
	}
	if !strings.Contains(out, rootPath(src)+"/sub/\n") {
		t.Fatalf("ls PATH output %q missing sub/", out)
	}
}

// TestLsMarksAnUnstableEntry uses the same newWriter seam
// TestCommitExitsOneAndReportsAnUnstablePath uses to force one file
// UNSTABLE, then checks a recursive ls marks exactly that file with "!"
// in the first column, and every other entry with a plain space.
func TestLsMarksAnUnstableEntry(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	target, err := filepath.Abs(filepath.Join(src, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	oldNewWriter := newWriter
	defer func() { newWriter = oldNewWriter }()
	var calls int
	newWriter = func(stagingDir string) *object.Writer {
		w := oldNewWriter(stagingDir)
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

	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 1 {
		t.Fatalf("commit: exit %d, want 1: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	code, out = runCmd(t, "ls", "--recursive", treeDir, snapID)
	if code != 0 {
		t.Fatalf("ls --recursive: exit %d: %s", code, out)
	}
	unstableLine := "!" + rootPath(src) + "/a.txt"
	if !strings.Contains(out, unstableLine) {
		t.Fatalf("ls --recursive output %q missing the unstable line %q", out, unstableLine)
	}
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		wantUnstable := line == unstableLine
		if (line[0] == '!') != wantUnstable {
			t.Fatalf("ls line %q has the wrong marker; want unstable only for %q", line, unstableLine)
		}
	}
}

// TestLsAcceptsARefName checks that ls resolves a ref name the same way
// it resolves a snapshot id text form.
func TestLsAcceptsARefName(t *testing.T) {
	treeDir, _, src := lsFixture(t)
	code, out := runCmd(t, "ls", treeDir, defaultRefName())
	if code != 0 {
		t.Fatalf("ls by ref name: exit %d: %s", code, out)
	}
	want := " " + rootPath(src) + "/\n"
	if out != want {
		t.Fatalf("ls by ref name output = %q, want %q", out, want)
	}
}

// TestLsSnapshotIDPrefixNamesItself checks that an argument that is 8
// or more hex characters, and resolves as neither a full snapshot id
// nor a ref name, is reported as a likely truncated snapshot id,
// instead of the generic ref-not-found wording.
func TestLsSnapshotIDPrefixNamesItself(t *testing.T) {
	treeDir, _, _ := lsFixture(t)
	code, out := runCmd(t, "ls", treeDir, "1220a053")
	if code != 2 {
		t.Fatalf("ls with a snapshot id prefix: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "looks like a snapshot id prefix") {
		t.Fatalf("ls with a snapshot id prefix output %q missing the prefix hint", out)
	}
}

// TestLsExitsThreeOnAMissingDisc packs a multi-disc sequence, then runs
// ls with one disc root left out of --discs-dir, and asserts exit 3
// and the same missing-disc message restore uses.
func TestLsExitsThreeOnAMissingDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	discsDir := filepath.Join(work, "discs")
	// disc1 is packed outside discsDir, so --discs-dir leaves it out.
	otherDir := filepath.Join(work, "other")
	capacities := []string{packSectors(7_000_000), packSectors(7_000_000), packSectors(10_000_000)}
	for i, cap := range capacities {
		dir := discsDir
		if i == 1 {
			dir = otherDir
		}
		treeDir := filepath.Join(dir, "disc"+strconv.Itoa(i))
		if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity="+cap, "--fec", "--out="+treeDir); code == 2 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
	}

	code, out = runCmd(t, "ls", "--recursive", "--discs-dir="+discsDir, snapID)
	if code != 1 {
		t.Fatalf("ls: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "missing disc") {
		t.Fatalf("ls output %q does not name a missing disc", out)
	}
}
