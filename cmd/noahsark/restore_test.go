package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// restoreOneDisc runs restore of snapshot with --disc=disc into dest,
// with the repository repo.
func restoreOneDisc(t *testing.T, repo, disc, snapshot, dest string, extra ...string) (int, string) {
	t.Helper()
	args := append([]string{"--repo=" + repo, "restore", "--disc=" + disc}, extra...)
	args = append(args, snapshot, dest)
	return runCmd(t, args...)
}

// restoreFromDiscs runs restore of snapshot into dest with no terminal,
// one time for each disc of roots, in order, until a run exits with a
// code other than 1 for a disc to insert. It returns the code and the
// output of the last run.
func restoreFromDiscs(t *testing.T, repo, snapshot, dest string, roots ...string) (int, string) {
	t.Helper()
	code, out := 1, ""
	for _, root := range roots {
		code, out = restoreOneDisc(t, repo, root, snapshot, dest)
		if code != 1 || !strings.Contains(out, "and run restore again") {
			return code, out
		}
	}
	return code, out
}

// TestRestoreSnapshotForms checks that restore takes a ref name, a full
// snapshot id and a prefix of the digest, and that it writes the content
// of the source root directly into DEST.
func TestRestoreSnapshotForms(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)

	for _, arg := range []string{defaultRefName(), snapID, snapID[4:16], strings.ToUpper(snapID[4:10])} {
		dest := filepath.Join(t.TempDir(), "restored")
		code, out := restoreOneDisc(t, repo, treeDir, arg, dest)
		if code != 0 {
			t.Fatalf("restore %s: exit %d: %s", arg, code, out)
		}
		if !strings.Contains(out, "restored snapshot "+snapID[4:16]+" into "+dest+"\n") {
			t.Fatalf("restore %s: output %q, want the restored line with the 12-character id", arg, out)
		}
		if strings.Contains(out, nextStatusLine) {
			t.Fatalf("restore %s: output %q holds the next line", arg, out)
		}
		compareTrees(t, dest, src)
	}
}

// TestRestoreUnknownRefReportsTheSameError checks that an unknown ref
// name given to restore reports the same error ls reports for it.
func TestRestoreUnknownRefReportsTheSameError(t *testing.T) {
	treeDir, _, _ := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)

	restoreCode, restoreOut := restoreOneDisc(t, repo, treeDir, "NO-SUCH-REF", filepath.Join(t.TempDir(), "out"))
	lsCode, lsOut := runCmd(t, "--repo="+repo, "ls", "NO-SUCH-REF")
	if restoreCode != 2 {
		t.Fatalf("restore: exit %d, want 2: %s", restoreCode, restoreOut)
	}
	if want := "noahsark: ls: no snapshot matches NO-SUCH-REF\n"; lsCode != 2 || lsOut != want {
		t.Fatalf("ls: exit %d, output %q; want 2 and %q", lsCode, lsOut, want)
	}
	restoreMsg := strings.TrimPrefix(strings.TrimSpace(restoreOut), "noahsark: restore: ")
	lsMsg := strings.TrimPrefix(strings.TrimSpace(lsOut), "noahsark: ls: ")
	if restoreMsg != lsMsg || restoreMsg != "no snapshot matches NO-SUCH-REF" {
		t.Fatalf("restore error %q, want %q, the same as ls", restoreMsg, lsMsg)
	}
}

// writeEmptyObjectSource creates a source tree that makes two objects
// with the same payload bytes: an empty directory, whose tree payload is
// eight zero bytes, and an empty file, whose blob payload is eight zero
// bytes too. It also holds a file of exactly eight zero bytes, and a
// normal file, so a restore compares real content beside the empty ones.
func writeEmptyObjectSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(src, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "zfile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "zeros8"), make([]byte, 8), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "normal.txt"), []byte("content of a normal file"), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// packSource commits src into a new repository and packs it onto one
// disc. It returns the repository, the disc root and the snapshot id.
func packSource(t *testing.T, src string) (repo, treeDir, snapID string) {
	t.Helper()
	work := t.TempDir()
	repo = filepath.Join(work, "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID = snapshotIDFromCommit(t, out)
	treeDir = filepath.Join(work, "tree")
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	return repo, treeDir, snapID
}

// TestRestoreEmptyDirectoryAndEmptyFile commits a source that holds an
// empty directory beside an empty file. The two objects have the same
// payload bytes and different kinds, so only a content id that covers
// the kind keeps them apart. A restore must give back both.
func TestRestoreEmptyDirectoryAndEmptyFile(t *testing.T) {
	src := writeEmptyObjectSource(t)
	repo, treeDir, snapID := packSource(t, src)

	dest := filepath.Join(t.TempDir(), "restored")
	if code, out := restoreOneDisc(t, repo, treeDir, snapID, dest); code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	compareTrees(t, dest, src)
	assertRestoredDirectory(t, filepath.Join(dest, "adir"))
}

// assertRestoredDirectory fails when path is not a directory.
func assertRestoredDirectory(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("restored empty directory %s: %v", path, err)
	}
	if !st.IsDir() {
		t.Fatalf("restored %s is not a directory", path)
	}
}

// TestRestorePaths checks the PATH rule: a path makes its entry in DEST,
// a trailing slash puts the content of a directory into DEST, and two
// paths restore both entries.
func TestRestorePaths(t *testing.T) {
	treeDir, snapID, src := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)
	cases := []struct {
		paths []string
		want  map[string]string
	}{
		{[]string{"sub/b.txt"}, map[string]string{"b.txt": "sub/b.txt"}},
		{[]string{"sub"}, map[string]string{"sub/b.txt": "sub/b.txt"}},
		{[]string{"sub/"}, map[string]string{"b.txt": "sub/b.txt"}},
		{[]string{"a.txt", "sub/b.txt"}, map[string]string{"a.txt": "a.txt", "b.txt": "sub/b.txt"}},
	}
	for _, c := range cases {
		dest := filepath.Join(t.TempDir(), "restored")
		args := append([]string{"--repo=" + repo, "restore", "--disc=" + treeDir, snapID}, c.paths...)
		code, out := runCmd(t, append(args, dest)...)
		if code != 0 {
			t.Fatalf("restore %v: exit %d: %s", c.paths, code, out)
		}
		var names []string
		for name, from := range c.want {
			compareFiles(t, filepath.Join(dest, name), filepath.Join(src, from))
			names = append(names, name)
		}
		if got := listFilesUnder(t, dest); len(got) != len(names) {
			t.Fatalf("restore %v wrote %v, want only %v", c.paths, got, names)
		}
	}
}

// TestRestorePathNotHeldWritesNothing checks that a PATH the snapshot
// does not hold is a usage error that writes nothing.
func TestRestorePathNotHeldWritesNothing(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)
	dest := filepath.Join(t.TempDir(), "restored")
	code, out := runCmd(t, "--repo="+repo, "restore", "--disc="+treeDir, snapID, "does/not/exist", dest)
	if code != 2 {
		t.Fatalf("restore: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "no entry of the snapshot matches does/not/exist") {
		t.Fatalf("output %q does not name the path", out)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("restore created DEST for a refused path: %v", err)
	}
}

// readFile reads path as a string.
func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// TestRestoreOverwrite asserts that restoring into a directory that
// already holds a differing file leaves that file alone and reports it,
// and that --overwrite replaces it instead.
func TestRestoreOverwrite(t *testing.T) {
	treeDir, snapID, _ := lsFixture(t)
	repo := repoDirFromTreeDir(t, treeDir)

	dest := filepath.Join(t.TempDir(), "restored")
	preexisting := filepath.Join(dest, "a.txt")
	if err := writeFile(preexisting, "pre-existing, different content"); err != nil {
		t.Fatal(err)
	}

	code, out := restoreOneDisc(t, repo, treeDir, snapID, dest)
	if code != 1 {
		t.Fatalf("restore without --overwrite: exit %d, want 1; output: %s", code, out)
	}
	if !strings.Contains(out, "noahsark: restore: warning: "+preexisting+": a path is already here; pass --overwrite to replace it\n") {
		t.Fatalf("output = %q, want the path named with the --overwrite advice", out)
	}
	if !strings.Contains(out, "noahsark: restore: warning: not restored: 1 existing path(s)") {
		t.Fatalf("output = %q, want the summary line", out)
	}
	if got, err := readFile(preexisting); err != nil || got != "pre-existing, different content" {
		t.Fatalf("existing file was modified without --overwrite: %q, %v", got, err)
	}

	code, out = restoreOneDisc(t, repo, treeDir, snapID, dest, "--overwrite")
	if code != 0 {
		t.Fatalf("restore --overwrite: exit %d, want 0; output: %s", code, out)
	}
	if got, err := readFile(preexisting); err != nil || got != "content of a" {
		t.Fatalf("--overwrite did not replace the existing file: %q, %v", got, err)
	}
}

// buildAndPackWithSymlink packs a fixture source that has a symlink
// "link" -> "a.txt" beside the usual a.txt and sub/b.txt.
func buildAndPackWithSymlink(t *testing.T) (repo, treeDir, snapID string) {
	t.Helper()
	src := writeFixtureSource(t)
	if err := os.Symlink("a.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	return packSource(t, src)
}

// TestRestoreOverwriteLeavesNonEmptyDirectory asserts that --overwrite,
// meeting a directory that holds entries where a symlink or a file must
// go, does not stop the restore: the directory and its content stay, a
// warning is printed, later entries still land, and the exit code is 1.
func TestRestoreOverwriteLeavesNonEmptyDirectory(t *testing.T) {
	for _, name := range []string{"link", "a.txt"} {
		t.Run(name, func(t *testing.T) {
			repo, treeDir, snapID := buildAndPackWithSymlink(t)
			dest := filepath.Join(t.TempDir(), "restored")
			blocker := filepath.Join(dest, name)
			kept := filepath.Join(blocker, "keep.txt")
			if err := writeFile(kept, "keep"); err != nil {
				t.Fatal(err)
			}

			code, out := restoreOneDisc(t, repo, treeDir, snapID, dest, "--overwrite")
			if code != 1 {
				t.Fatalf("restore --overwrite: exit %d, want 1; output: %s", code, out)
			}
			if !strings.Contains(out, "noahsark: restore: warning: "+blocker+": ") || !strings.Contains(out, "not empty") {
				t.Fatalf("output = %q, want a warning line naming %q and the directory that is not empty", out, blocker)
			}
			if got, err := readFile(kept); err != nil || got != "keep" {
				t.Fatalf("the existing directory or its content was removed: %q, %v", got, err)
			}
			if _, err := os.Stat(filepath.Join(dest, "sub", "b.txt")); err != nil {
				t.Fatalf("a later entry was not restored past the conflict: %v", err)
			}
		})
	}
}

// TestSnapshotArgEmptyNamesItself covers the fault of issue 48: an empty
// snapshot argument was quoted back as `ref ""`, which names nothing.
// Every command that takes a snapshot must say that no snapshot was
// given, and exit 2.
func TestSnapshotArgEmptyNamesItself(t *testing.T) {
	repo, treeDir, _ := snapshotArgFixture(t)

	cases := [][]string{
		{"--repo=" + repo, "restore", "--disc=" + treeDir, "", filepath.Join(t.TempDir(), "out")},
		{"--repo=" + repo, "ls", ""},
		{"--repo=" + repo, "restore", "--disc=" + t.TempDir(), "--dry-run", "", filepath.Join(t.TempDir(), "out")},
		{"--repo=" + repo, "log", ""},
	}
	for _, args := range cases {
		code, out := runCmd(t, args...)
		if code != 2 {
			t.Fatalf("%v: exit %d, want 2: %s", args, code, out)
		}
		if !strings.Contains(out, "no snapshot given") {
			t.Fatalf("%v: output = %q, want it to say no snapshot was given", args, out)
		}
		if strings.Contains(out, `""`) {
			t.Fatalf("%v: output = %q, quotes an empty name", args, out)
		}
	}
}

// TestRestoreUsageErrorsExitTwo checks that each usage error of restore
// exits 2 and writes nothing.
func TestRestoreUsageErrorsExitTwo(t *testing.T) {
	repo, _ := initAndCommit(t)
	dest := filepath.Join(t.TempDir(), "out")
	disc := "--disc=" + t.TempDir()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"--no-such-flag", disc, defaultRefName(), dest}, "flag provided but not defined"},
		{"no --disc", []string{defaultRefName(), dest}, "--disc=DIR is required"},
		{"--disc twice", []string{disc, disc, defaultRefName(), dest}, "the option takes one value only"},
		{"no DEST", []string{disc, defaultRefName()}, "usage: noahsark restore"},
		{"an option after a positional", []string{disc, defaultRefName(), dest, "--overwrite"}, "flags must come before positional arguments"},
		{"--mount", []string{"--mount=" + t.TempDir(), defaultRefName(), dest}, "flag provided but not defined: -mount"},
		{"--include", []string{disc, "--include=a.txt", defaultRefName(), dest}, "flag provided but not defined: -include"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"--repo=" + repo, "restore"}, c.args...)
			code, out := runCmd(t, args...)
			if code != 2 {
				t.Fatalf("args %v: exit %d, want 2: %s", c.args, code, out)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("args %v: output %q, want %q", c.args, out, c.want)
			}
			if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatalf("a usage error created DEST: %v", err)
			}
		})
	}
}
