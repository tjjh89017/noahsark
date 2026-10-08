package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// lsLine gives the ls line that the file at src/rel must print, from the
// file itself.
func lsLine(t *testing.T, src, rel, typ string) string {
	t.Helper()
	fi, err := os.Lstat(filepath.Join(src, rel))
	if err != nil {
		t.Fatal(err)
	}
	size := fi.Size()
	if typ == "dir" {
		size = 0
	}
	return fmt.Sprintf("%04o\t%s\t%d\t%s\t%s", uint32(fi.Mode().Perm()), typ, size,
		fi.ModTime().UTC().Format("2006-01-02T15:04:05Z"), rel)
}

// runLs runs ls in repo and returns the exit code, standard output and
// standard error apart.
func runLs(t *testing.T, repo string, args ...string) (int, string, string) {
	t.Helper()
	te := newTestEnv(t.TempDir())
	code := run(te.env, append([]string{"--repo=" + repo}, args...))
	return code, te.out.String(), te.errOut.String()
}

// TestLsListsOneLevel checks that ls prints the entries of the source
// root, one level, one tab-separated line each, with paths relative to
// the source root.
func TestLsListsOneLevel(t *testing.T) {
	repo, src := initAndCommit(t)
	code, out, errOut := runLs(t, repo, "ls", defaultRefName())
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, errOut)
	}
	want := lsLine(t, src, "a.txt", "file") + "\n" + lsLine(t, src, "sub", "dir") + "\n"
	if out != want {
		t.Fatalf("ls output:\n%q\nwant:\n%q", out, want)
	}
	if errOut != "" {
		t.Fatalf("ls wrote to standard error: %q", errOut)
	}
}

// TestLsRecursive checks that -R and --recursive descend depth first.
func TestLsRecursive(t *testing.T) {
	repo, src := initAndCommit(t)
	want := lsLine(t, src, "a.txt", "file") + "\n" +
		lsLine(t, src, "sub", "dir") + "\n" +
		lsLine(t, src, "sub/b.txt", "file") + "\n"
	for _, flag := range []string{"-R", "--recursive"} {
		code, out, errOut := runLs(t, repo, "ls", flag, defaultRefName())
		if code != 0 {
			t.Fatalf("ls %s: exit %d: %s", flag, code, errOut)
		}
		if out != want {
			t.Fatalf("ls %s output:\n%q\nwant:\n%q", flag, out, want)
		}
	}
}

// TestLsPath checks PATH: a directory lists its entries, a file prints
// its own line, a trailing slash changes nothing, and a path that the
// snapshot does not hold is a usage error.
func TestLsPath(t *testing.T) {
	repo, src := initAndCommit(t)
	ref := defaultRefName()
	subLine := lsLine(t, src, "sub/b.txt", "file") + "\n"
	for _, c := range []struct{ path, want string }{
		{"sub", subLine},
		{"sub/", subLine},
		{"/sub", subLine},
		{"sub/b.txt", subLine},
		{"a.txt", lsLine(t, src, "a.txt", "file") + "\n"},
	} {
		code, out, errOut := runLs(t, repo, "ls", ref, c.path)
		if code != 0 {
			t.Fatalf("ls %s: exit %d: %s", c.path, code, errOut)
		}
		if out != c.want {
			t.Fatalf("ls %s output:\n%q\nwant:\n%q", c.path, out, c.want)
		}
	}
	for _, path := range []string{"nope", "a.txt/x", "sub/nope"} {
		code, out, errOut := runLs(t, repo, "ls", ref, path)
		if code != 2 || out != "" {
			t.Fatalf("ls %s: exit %d, output %q; want 2 and no output", path, code, out)
		}
		if !strings.Contains(errOut, path+" is not in snapshot ") {
			t.Fatalf("ls %s: standard error %q does not name the path", path, errOut)
		}
	}
}

// TestLsFieldsOfEachType checks the size of a symlink, the mode bits
// above the permission bits, and the escape of special bytes in a path.
func TestLsFieldsOfEachType(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"tab\tname", "new\nline", "bad\xffutf8", "del\x7f", "ok-é"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(src, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("target-of-12", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	sticky := filepath.Join(src, "sticky")
	if err := os.Mkdir(sticky, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sticky, 0o755|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	code, out, errOut := runLs(t, repo, "ls", defaultRefName())
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, errOut)
	}
	paths := map[string][]string{}
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			t.Fatalf("ls line %q has %d fields, want 5", line, len(f))
		}
		paths[f[4]] = f
	}
	for _, p := range []string{`tab\tname`, `new\nline`, `bad\xffutf8`, `del\x7f`, "ok-é"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("ls output has no path %q:\n%s", p, out)
		}
	}
	if f := paths["link"]; f == nil || f[0] != "0777" || f[1] != "symlink" || f[2] != "12" {
		t.Errorf("symlink line = %q, want mode 0777, type symlink, size 12", f)
	}
	if f := paths["sticky"]; f == nil || f[0] != "1755" || f[1] != "dir" || f[2] != "0" {
		t.Errorf("sticky line = %q, want mode 1755, type dir, size 0", f)
	}
}

// TestEscapeField checks the escape of each byte class in an ls path
// and a log field.
func TestEscapeField(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain/path é", "plain/path é"},
		{"a\\b", `a\\b`},
		{"a\tb", `a\tb`},
		{"a\nb", `a\nb`},
		{"a\rb\x00c\x1f", `a\x0db\x00c\x1f`},
		{"del\x7f", `del\x7f`},
		{"bad\xff\xc3", `bad\xff\xc3`},
		{"\xef\xbf\xbd", "\xef\xbf\xbd"},
	} {
		if got := escapeField(c.in); got != c.want {
			t.Errorf("escapeField(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestLsSnapshotArgument checks the forms of SNAPSHOT: a ref name, the
// full id, and a digest prefix give the same lines. A value that matches
// no snapshot is a usage error, also in an empty repository.
func TestLsSnapshotArgument(t *testing.T) {
	repo, _ := initAndCommit(t)
	_, want, _ := runLs(t, repo, "ls", defaultRefName())
	id := firstLogID(t, repo)
	for _, arg := range []string{id, strings.ToUpper(id[:8])} {
		code, out, errOut := runLs(t, repo, "ls", arg)
		if code != 0 || out != want {
			t.Fatalf("ls %s: exit %d, output %q, stderr %q; want %q", arg, code, out, errOut, want)
		}
	}
	full := fullSnapshotID(t, repo)
	if code, out, errOut := runLs(t, repo, "ls", full); code != 0 || out != want {
		t.Fatalf("ls FULL-ID: exit %d, output %q, stderr %q", code, out, errOut)
	}

	code, out, errOut := runLs(t, repo, "ls", "no-such-ref")
	if code != 2 || out != "" || !strings.Contains(errOut, "no snapshot matches no-such-ref") {
		t.Fatalf("ls no-such-ref: exit %d, output %q, stderr %q", code, out, errOut)
	}

	empty := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, empty, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, _, errOut = runLs(t, empty, "ls", "latest")
	if code != 2 || !strings.Contains(errOut, "no snapshot matches latest") {
		t.Fatalf("ls in an empty repository: exit %d, stderr %q", code, errOut)
	}
}

// TestLsPartialSnapshot checks that a snapshot whose tree the catalog
// does not hold exits 1 and names recover. The state table test covers a
// snapshot that the completeness file marks partial.
func TestLsPartialSnapshot(t *testing.T) {
	t.Run("tree not held", func(t *testing.T) {
		repo, _ := initAndCommit(t)
		removeTree(t, repo, "sub")
		code, _, errOut := runLs(t, repo, "ls", "-R", defaultRefName())
		if code != 1 || !strings.Contains(errOut, " is partial; run recover with more discs") {
			t.Fatalf("ls -R: exit %d, stderr %q; want 1 and the partial line", code, errOut)
		}
	})
}

// TestLsKeepsTheRootLevelOfSeveralRoots gives the snapshot a second
// source root. ls then prints the root level, each root as its path
// without the leading slash, and PATH starts with a root path.
func TestLsKeepsTheRootLevelOfSeveralRoots(t *testing.T) {
	repo, src := initAndCommit(t)
	const other = "/zz/other root"
	addSourceRoot(t, repo, other)

	code, out, errOut := runLs(t, repo, "ls", defaultRefName())
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, errOut)
	}
	var paths []string
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		f := strings.Split(line, "\t")
		if f[1] != "dir" {
			t.Fatalf("root line %q is not a directory", line)
		}
		paths = append(paths, f[4])
	}
	want := []string{rootPath(src), strings.TrimPrefix(other, "/")}
	if !slices.Equal(paths, want) {
		t.Fatalf("root level paths = %q, want %q", paths, want)
	}

	code, out, errOut = runLs(t, repo, "ls", defaultRefName(), rootPath(src)+"/sub")
	if code != 0 || !strings.HasSuffix(out, "\t"+rootPath(src)+"/sub/b.txt\n") {
		t.Fatalf("ls ROOT/sub: exit %d, output %q, stderr %q", code, out, errOut)
	}
}

// TestLsFormatNames checks that --format=names prints the path only, in
// the order of the default format, also with -R.
func TestLsFormatNames(t *testing.T) {
	repo, _ := initAndCommit(t)
	code, out, errOut := runLs(t, repo, "ls", "-R", "--format=names", defaultRefName())
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, errOut)
	}
	if want := "a.txt\nsub\nsub/b.txt\n"; out != want {
		t.Fatalf("ls --format=names output:\n%q\nwant:\n%q", out, want)
	}
}

// TestLsFormatJSON checks that --format=json prints one JSON object on
// each line, with the fields of the default line.
func TestLsFormatJSON(t *testing.T) {
	repo, src := initAndCommit(t)
	code, out, errOut := runLs(t, repo, "ls", "--recursive", "--format=json", defaultRefName())
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, errOut)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	want := []struct{ rel, typ string }{{"a.txt", "file"}, {"sub", "dir"}, {"sub/b.txt", "file"}}
	if len(lines) != len(want) {
		t.Fatalf("ls --format=json printed %d lines, want %d:\n%s", len(lines), len(want), out)
	}
	for i, w := range want {
		var got struct {
			Mode string `json:"mode"`
			Type string `json:"type"`
			Size uint64 `json:"size"`
			Time string `json:"time"`
			Path string `json:"path"`
		}
		dec := json.NewDecoder(strings.NewReader(lines[i]))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&got); err != nil {
			t.Fatalf("line %q: %v", lines[i], err)
		}
		f := strings.Split(lsLine(t, src, w.rel, w.typ), "\t")
		if gotLine := strings.Join([]string{got.Mode, got.Type, strconv.FormatUint(got.Size, 10), got.Time, got.Path}, "\t"); gotLine != strings.Join(f, "\t") {
			t.Errorf("line %q gives %q, want %q", lines[i], gotLine, strings.Join(f, "\t"))
		}
	}
}

// TestLsFormatHuman checks that --format=human prints the default line
// with the size in units of 1024, and a size below 1024 in bytes.
func TestLsFormatHuman(t *testing.T) {
	src := writeFixtureSource(t)
	if err := os.WriteFile(filepath.Join(src, "big.bin"), make([]byte, 4300), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, out, errOut := runLs(t, repo, "ls", "-R", "--format=human", defaultRefName())
	if code != 0 {
		t.Fatalf("ls: exit %d: %s", code, errOut)
	}
	var want strings.Builder
	for _, w := range []struct{ rel, typ, size string }{
		{"a.txt", "file", "12"}, {"big.bin", "file", "4.2K"}, {"sub", "dir", "0"}, {"sub/b.txt", "file", "33"},
	} {
		f := strings.Split(lsLine(t, src, w.rel, w.typ), "\t")
		f[2] = w.size
		want.WriteString(strings.Join(f, "\t") + "\n")
	}
	if out != want.String() {
		t.Fatalf("ls --format=human output:\n%q\nwant:\n%q", out, want.String())
	}
}

// TestHumanSize checks the unit and the rounding of a human size.
func TestHumanSize(t *testing.T) {
	for _, c := range []struct {
		in   uint64
		want string
	}{
		{0, "0"},
		{1023, "1023"},
		{1024, "1.0K"},
		{4300, "4.2K"},
		{1024*1024 - 1, "1.0M"},
		{1363149, "1.3M"},
		{2 << 30, "2.0G"},
		{5 << 40, "5.0T"},
		{1<<64 - 1, "16.0E"},
	} {
		if got := humanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestLsFormatDefaultIsTheDefault checks that --format=default prints
// the same lines as no --format.
func TestLsFormatDefaultIsTheDefault(t *testing.T) {
	repo, _ := initAndCommit(t)
	_, want, _ := runLs(t, repo, "ls", "-R", defaultRefName())
	code, out, errOut := runLs(t, repo, "ls", "-R", "--format=default", defaultRefName())
	if code != 0 || out != want {
		t.Fatalf("ls --format=default: exit %d, output %q, stderr %q; want %q", code, out, errOut, want)
	}
}

// TestLsUnknownFormat checks that an unknown format is a usage error
// that lists each value.
func TestLsUnknownFormat(t *testing.T) {
	repo, _ := initAndCommit(t)
	code, out, errOut := runLs(t, repo, "ls", "--format=long", defaultRefName())
	if code != 2 || out != "" {
		t.Fatalf("ls --format=long: exit %d, output %q; want 2 and no output", code, out)
	}
	if want := `noahsark: ls: invalid format "long"; give default, names, json, human` + "\n"; errOut != want {
		t.Fatalf("ls --format=long: stderr %q, want %q", errOut, want)
	}
}

// TestLsUsageErrorsExitTwo checks the usage errors of ls.
func TestLsUsageErrorsExitTwo(t *testing.T) {
	repo, _ := initAndCommit(t)
	for _, args := range [][]string{
		{"ls"},
		{"ls", "a", "b", "c"},
		{"ls", "--long", defaultRefName()},
		{"ls", defaultRefName(), "--recursive"},
	} {
		code, out, errOut := runLs(t, repo, args...)
		if code != 2 || out != "" {
			t.Fatalf("%q: exit %d, output %q, stderr %q; want 2", args, code, out, errOut)
		}
	}
}

// TestLsAndLogChangeNoFile lists every file and directory of the work
// tree, with its mode, size and modification time, before and after ls
// and log. The two lists must be equal, also for a failure.
func TestLsAndLogChangeNoFile(t *testing.T) {
	commands := [][]string{
		{"ls", "-R", "latest"},
		{"log"},
		{"log", "latest"},
	}
	check := func(t *testing.T, work, repo string) {
		t.Helper()
		for _, args := range commands {
			before := treeSnapshot(t, work)
			runCmd(t, append([]string{"--repo=" + repo}, args...)...)
			after := treeSnapshot(t, work)
			if !mapsEqual(before, after) {
				t.Errorf("%q changed the tree:\nbefore %v\nafter  %v", args, before, after)
			}
		}
	}
	t.Run("empty repository", func(t *testing.T) {
		work := t.TempDir()
		repo := filepath.Join(work, "repo")
		if code, out := runIn(t, repo, "init"); code != 0 {
			t.Fatalf("init: exit %d: %s", code, out)
		}
		check(t, work, repo)
	})
	t.Run("no catalog directory", func(t *testing.T) {
		work := t.TempDir()
		repo := filepath.Join(work, "repo")
		if code, out := runIn(t, repo, "init"); code != 0 {
			t.Fatalf("init: exit %d: %s", code, out)
		}
		if err := os.RemoveAll(filepath.Join(repo, "catalog")); err != nil {
			t.Fatal(err)
		}
		check(t, work, repo)
	})
	t.Run("committed", func(t *testing.T) {
		repo := commitWithRef(t, "latest")
		check(t, filepath.Dir(repo), repo)
	})
	t.Run("packed", func(t *testing.T) {
		repo := commitWithRef(t, "latest")
		if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB"); code != 0 {
			t.Fatalf("pack: exit %d: %s", code, out)
		}
		check(t, filepath.Dir(repo), repo)
	})
	t.Run("partial", func(t *testing.T) {
		repo := commitWithRef(t, "latest")
		removeTree(t, repo, "sub")
		check(t, filepath.Dir(repo), repo)
	})
}

// mapsEqual reports whether two tree listings are equal.
func mapsEqual(a, b map[string]treeEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// commitWithRef makes a repository with one commit that moves ref, and
// returns the repository directory.
func commitWithRef(t *testing.T, ref string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref="+ref, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	return repo
}

// fullSnapshotID gives the full text id of the one snapshot of repo.
func fullSnapshotID(t *testing.T, repo string) string {
	t.Helper()
	ids := snapshotIDs(t, repo)
	if len(ids) != 1 {
		t.Fatalf("repository holds %d snapshots, want 1", len(ids))
	}
	return ids[0].TextForm()
}

// snapshotIDs lists the snapshots of the catalog of repo.
func snapshotIDs(t *testing.T, repo string) []object.ID {
	t.Helper()
	c, err := catalog.OpenReadOnly(repo)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := c.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// rootTreeOf reads the root tree of the one snapshot of repo.
func rootTreeOf(t *testing.T, repo string) (*catalog.Catalog, object.ID, *format.Tree) {
	t.Helper()
	c, err := catalog.OpenReadOnly(repo)
	if err != nil {
		t.Fatal(err)
	}
	id, err := object.ParseID(fullSnapshotID(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	rootID := object.ID(snap.RootTree)
	root, err := c.ReadTree(rootID)
	if err != nil {
		t.Fatal(err)
	}
	return c, rootID, root
}

// removeTree deletes from the catalog the tree of the directory name
// below the source root of the one snapshot of repo.
func removeTree(t *testing.T, repo, name string) {
	t.Helper()
	c, _, root := rootTreeOf(t, repo)
	top, err := c.ReadTree(object.ID(root.Entries[0].ContentID))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range top.Entries {
		if string(e.Name) == name {
			if err := os.Remove(c.MetaPath(format.ObjectKindTree, object.ID(e.ContentID))); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("no directory %s below the source root", name)
}

// addSourceRoot adds a second root entry, a copy of the first one with
// the source root path path, to the root tree of the one snapshot of
// repo. The catalog checks the content id of each object, thus the
// changed root tree and its snapshot get new ids, and the default ref
// moves to the new snapshot.
func addSourceRoot(t *testing.T, repo, path string) {
	t.Helper()
	c, _, root := rootTreeOf(t, repo)
	oldLen := uint64(root.EncodedLen())
	extra := root.Entries[0]
	extra.Name = []byte(format.EncodeRootName(path))
	root.Entries = append(root.Entries, extra)
	slices.SortFunc(root.Entries, func(a, b format.TreeEntry) int {
		return bytes.Compare(append(slices.Clone(a.Name), '/'), append(slices.Clone(b.Name), '/'))
	})
	root.EntryCount = uint32(len(root.Entries))
	grow := uint64(root.EncodedLen()) - oldLen
	root.ObjectHeader.PayloadLen += grow
	root.ObjectHeader.StoredLen += grow
	buf := make([]byte, root.EncodedLen())
	if _, err := root.Encode(buf); err != nil {
		t.Fatal(err)
	}
	const headLen = format.CommonHeaderLen + format.ObjectHeaderLen
	rootID := object.ComputeID(format.ObjectKindTree, buf[headLen:])
	if err := c.WriteObject(format.ObjectKindTree, rootID, buf); err != nil {
		t.Fatal(err)
	}

	oldSnap, err := object.ParseID(fullSnapshotID(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.ReadSnapshot(oldSnap)
	if err != nil {
		t.Fatal(err)
	}
	snap.RootTree = rootID
	snapBuf := make([]byte, snap.EncodedLen())
	if _, err := snap.Encode(snapBuf); err != nil {
		t.Fatal(err)
	}
	snapID := object.ComputeID(format.ObjectKindSnapshot, snapBuf[headLen:])
	if err := c.WriteObject(format.ObjectKindSnapshot, snapID, snapBuf); err != nil {
		t.Fatal(err)
	}
	if err := updateRef(testLayout(t, repo).refsFile(), defaultRefName(), snapID); err != nil {
		t.Fatal(err)
	}
}
