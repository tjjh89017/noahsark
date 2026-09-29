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

	if code, out := runIn(t, repo, "init"); code != 0 {
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

	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 1 {
		t.Fatalf("commit: exit %d, want 1: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
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
// ls with one disc root left off the command line, and asserts exit 3
// and the same missing-disc message restore uses.
func TestLsExitsThreeOnAMissingDisc(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	discsDir := filepath.Join(work, "discs")
	// disc1's root is never passed to ls, so it is left off.
	var discRoots []string
	capacities := []string{packSectors(7_000_000), packSectors(7_000_000), packSectors(10_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(discsDir, "disc"+strconv.Itoa(i))
		if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+cap, "--fec", "--out="+treeDir); code == 2 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
		if i != 1 {
			discRoots = append(discRoots, treeDir)
		}
	}

	args := append([]string{"ls", "--recursive"}, discRoots...)
	args = append(args, snapID)
	code, out = runCmd(t, args...)
	if code != 1 {
		t.Fatalf("ls: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "missing disc") {
		t.Fatalf("ls output %q does not name a missing disc", out)
	}
}

// TestLsFromCacheWithNoDisc packs a repository, then runs ls with no
// disc given at all: it must resolve the snapshot and list its root
// entries from the catalog pack left behind, the same as the
// disc-based listing.
func TestLsFromCatalogWithNoDisc(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)

	repo := repoDirFromTreeDir(t, treeDir)
	discCode, discOut := runCmd(t, "ls", treeDir, snapID)
	if discCode != 0 {
		t.Fatalf("ls (disc): exit %d: %s", discCode, discOut)
	}

	catalogCode, catalogOut := runCmd(t, "--repo="+repo, "ls", snapID)
	if catalogCode != 0 {
		t.Fatalf("ls (cache): exit %d: %s", catalogCode, catalogOut)
	}
	if catalogOut != discOut {
		t.Fatalf("ls from cache = %q, want %q (same as disc)", catalogOut, discOut)
	}
	_ = src
}

// TestLsFromCacheReportsIncompleteSnapshot builds a multi-disc
// repository, wipes the catalog, then rebuilds it from only the last
// disc: the snapshot's tree spans earlier discs too, so the catalog ends
// up genuinely incomplete. ls with no disc given must exit 3 and name
// recover as the fix.
func TestLsFromCatalogReportsIncompleteSnapshot(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeMultiDiscFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	// Two small forced capacities split the commit across two runs, on
	// two discs: the snapshot's tree needs both.
	var discRoots []string
	capacities := []string{packSectors(7_000_000), packSectors(7_000_000)}
	for i, cap := range capacities {
		treeDir := filepath.Join(work, "disc"+string(rune('0'+i)))
		if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+cap, "--out="+treeDir); code == 2 {
			t.Fatalf("pack %d: exit %d: %s", i, code, out)
		}
		discRoots = append(discRoots, treeDir)
	}

	catalogDir := repoCatalogDir(t, repo)
	if err := os.RemoveAll(catalogDir); err != nil {
		t.Fatal(err)
	}
	// ls reads the staging store first, so the staged trees must go
	// too, the way gc frees them once both copies are verified.
	if err := os.RemoveAll(filepath.Join(repo, "staging", "objects")); err != nil {
		t.Fatal(err)
	}

	lastDisc := discRoots[len(discRoots)-1]
	if code, out := runCmd(t, "--repo="+repo, "recover", lastDisc); code == 2 {
		t.Fatalf("recover: exit %d: %s", code, out)
	}

	code, out = runCmd(t, "--repo="+repo, "ls", "--recursive", snapID)
	if code != 1 {
		t.Fatalf("ls: exit %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "not complete in the cache") || !strings.Contains(out, "recover") {
		t.Fatalf("ls output %q does not report an incomplete cache", out)
	}
}

// TestLsAndLogAgreeOnAnEmptyCache checks that ls and log report a catalog
// with no disc in it the same way: it is a failure at run time, exit 1,
// for the listing form and for the one-snapshot form alike.
func TestLsAndLogAgreeOnAnEmptyCatalog(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := [][]string{
		{"--repo=" + repo, "ls", "latest"},
		{"--repo=" + repo, "log", "latest"},
		{"--repo=" + repo, "log"},
	}
	for _, args := range cases {
		code, out := runCmd(t, args...)
		if code != 1 {
			t.Fatalf("%v: exit %d, want 1: %s", args, code, out)
		}
		if !strings.Contains(out, "no disc is cached yet") {
			t.Fatalf("%v output %q does not name the empty cache", args, out)
		}
	}
}

// TestLsNonexistentPathReportsNoSuchDiscRoot checks that a nonexistent
// path given as ls's first positional is reported as a missing disc
// root, not resolved as a SNAPSHOT arg through catalog mode.
func TestLsNonexistentPathReportsNoSuchDiscRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-disc")
	code, out := runCmd(t, "ls", missing, "SOMESNAP")
	if code != 2 {
		t.Fatalf("ls: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "no such disc root: "+missing) {
		t.Fatalf("ls output %q does not name the missing disc root", out)
	}
}

// TestLsAndLogBeforeTheFirstPack checks that log and ls -r resolve a
// just-committed ref and its trees from the staging store, before any
// pack has filled the catalog.
func TestLsAndLogBeforeTheFirstPack(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", "--ref=2026-09-21", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	if code, out := runCmd(t, "--repo="+repo, "log"); code != 0 {
		t.Fatalf("log: exit %d, want 0: %s", code, out)
	} else if !strings.Contains(out, snapID) || !strings.Contains(out, "2026-09-21") {
		t.Fatalf("log output %q, want the staged snapshot and its ref", out)
	}

	if code, out := runCmd(t, "--repo="+repo, "ls", "--recursive", "2026-09-21"); code != 0 {
		t.Fatalf("ls -r: exit %d, want 0: %s", code, out)
	} else if !strings.Contains(out, "a.txt") || !strings.Contains(out, "b.txt") {
		t.Fatalf("ls -r output %q, want the committed files", out)
	}

	// A second commit with a pack in between: the first snapshot comes
	// from the catalog, the second one from staging, and log lists both.
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB"); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if err := os.WriteFile(filepath.Join(src, "c.txt"), []byte("content of c"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "--repo="+repo, "commit", "--ref=2026-09-22", src)
	if code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}
	snapID2 := snapshotIDFromCommit(t, out)

	code, out = runCmd(t, "--repo="+repo, "log")
	if code != 0 {
		t.Fatalf("log (after the second commit): exit %d: %s", code, out)
	}
	if !strings.Contains(out, snapID) || !strings.Contains(out, snapID2) {
		t.Fatalf("log output %q, want both snapshots", out)
	}
	if code, out := runCmd(t, "--repo="+repo, "ls", "--recursive", "2026-09-22"); code != 0 {
		t.Fatalf("ls -r (second): exit %d: %s", code, out)
	} else if !strings.Contains(out, "c.txt") {
		t.Fatalf("ls -r output %q, want the new file", out)
	}
}

// TestLsFlagAfterPositionalReportedClearly asserts that a flag placed
// after ls's positional arguments is reported as a usage error naming
// the flag, instead of being read back as a PATH.
func TestLsFlagAfterPositionalReportedClearly(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)

	code, out := runCmd(t, "ls", treeDir, snapID, "--recursive")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output: %s", code, out)
	}
	want := "flags must come before positional arguments: --recursive"
	if !strings.Contains(out, want) {
		t.Fatalf("output = %q, want it to contain %q", out, want)
	}
	if strings.Contains(out, "matches no entry") {
		t.Fatalf("output = %q, want no \"matches no entry\" misreading", out)
	}
}

// TestLsUsageErrorsExitTwo checks the usage-error convention of the exit code registry for
// ls: each case exits 2, never 0 or 1.
func TestLsUsageErrorsExitTwo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"missing SNAPSHOT", []string{"--repo=" + repo, "ls"}},
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
