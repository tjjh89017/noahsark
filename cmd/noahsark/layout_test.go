package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestLayoutPaths is the golden list of the paths of a repository.
func TestLayoutPaths(t *testing.T) {
	repo := t.TempDir()
	l := repoLayout{repo: repo, staging: filepath.Join(repo, "staging")}
	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	disc := [16]byte{0x8a, 0x3f, 0x10, 0x2b, 0xc4, 0xd5, 0x4e, 0x6f, 0x80, 0x91, 0xa2, 0xb3, 0xc4, 0xd5, 0xe6, 0xf7}
	id := object.ComputeID(format.ObjectKindChunk, []byte("layout"))
	ab, text := id.FanoutByte(), id.TextForm()
	objectPath := l.objectPath(c)

	got := []struct{ name, path string }{
		{"config", l.configFile()},
		{"gitignore", l.gitignoreFile()},
		{"lock", l.lockFile()},
		{"state", l.stateDir()},
		{"state log", l.stateLogFile()},
		{"disc state log", l.discLogFile()},
		{"disc ledger", l.discsLedgerFile()},
		{"ref ledger", l.refsLedgerFile()},
		{"refs", l.refsFile()},
		{"catalog state", l.catalogStateFile()},
		{"catalog", l.catalogDir()},
		{"staging", l.stagingDir()},
		{"chunks", l.chunksDir()},
		{"chunk", l.chunkFile(id)},
		{"chunk object", objectPath(format.ObjectKindChunk, id)},
		{"blob object", objectPath(format.ObjectKindBlob, id)},
		{"tree object", objectPath(format.ObjectKindTree, id)},
		{"snapshot object", objectPath(format.ObjectKindSnapshot, id)},
		{"plans", l.plansDir()},
		{"plan", l.planDir(disc)},
		{"plan tree", l.planTree(disc)},
		{"plan image", l.planImage(disc)},
	}
	want := map[string]string{
		"config":          "config.yaml",
		"gitignore":       ".gitignore",
		"lock":            "lock",
		"state":           "state",
		"state log":       "state/state.db",
		"disc state log":  "state/discstate.db",
		"disc ledger":     "state/discs.bin",
		"ref ledger":      "state/refslog.bin",
		"refs":            "state/refs.txt",
		"catalog state":   "state/catalog-state.txt",
		"catalog":         "catalog",
		"staging":         "staging",
		"chunks":          "staging/chunks",
		"chunk":           "staging/chunks/" + ab + "/" + text,
		"chunk object":    "staging/chunks/" + ab + "/" + text,
		"blob object":     "catalog/blobs/" + ab + "/" + text,
		"tree object":     "catalog/trees/" + ab + "/" + text,
		"snapshot object": "catalog/snapshots/" + text,
		"plans":           "staging/plans",
		"plan":            "staging/plans/8a3f102b-c4d5-4e6f-8091-a2b3c4d5e6f7",
		"plan tree":       "staging/plans/8a3f102b-c4d5-4e6f-8091-a2b3c4d5e6f7/tree",
		"plan image":      "staging/plans/8a3f102b-c4d5-4e6f-8091-a2b3c4d5e6f7/tree.img",
	}
	for _, g := range got {
		rel, err := filepath.Rel(repo, g.path)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.ToSlash(rel) != want[g.name] {
			t.Errorf("%s = %s, want %s", g.name, filepath.ToSlash(rel), want[g.name])
		}
	}
	if len(got) != len(want) {
		t.Fatalf("the test checks %d paths, the golden list holds %d", len(got), len(want))
	}
}

// TestLayoutAgreesWithThePackages checks that the state log, the disc
// state log and the catalog use the paths of the layout.
func TestLayoutAgreesWithThePackages(t *testing.T) {
	repo := t.TempDir()
	l := repoLayout{repo: repo, staging: filepath.Join(repo, "staging")}
	if err := os.MkdirAll(l.stateDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	log, err := stage.Open(l.stateDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := log.EnsureStaged(object.ComputeID(format.ObjectKindChunk, []byte("x"))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.stateLogFile()); err != nil {
		t.Fatalf("the state log is not at %s: %v", l.stateLogFile(), err)
	}
	discLog, err := stage.OpenDiscLogReadOnly(l.stateDir())
	if err != nil {
		t.Fatal(err)
	}
	if discLog.Path() != l.discLogFile() {
		t.Fatalf("disc state log = %s, want %s", discLog.Path(), l.discLogFile())
	}
	if catalog.Dir(repo) != l.catalogDir() || catalog.StatePath(repo) != l.catalogStateFile() {
		t.Fatalf("catalog paths %s and %s differ from the layout", catalog.Dir(repo), catalog.StatePath(repo))
	}
}

// TestInitCommitPackUseTheLayout runs init, commit and pack, and checks
// which files exist: each file of the layout, and no file at a path of
// the old layout.
func TestInitCommitPackUseTheLayout(t *testing.T) {
	repo, _ := initAndCommit(t)
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	l := testLayout(t, repo)
	tree := packedTreeDir(t, repo, out)

	for _, path := range []string{
		l.configFile(), l.gitignoreFile(), l.stateLogFile(), l.discsLedgerFile(),
		l.refsLedgerFile(), l.refsFile(), l.catalogStateFile(), l.chunksDir(),
		filepath.Join(tree, "NOAHSARK"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
	if filepath.Dir(tree) != l.planDir(parseDiscUUID(t, packedDiscUUID(t, out))) || filepath.Base(tree) != planTreeName {
		t.Errorf("tree = %s, want the plan tree of the disc below %s", tree, l.plansDir())
	}

	chunks := listFilesUnder(t, l.chunksDir())
	if len(chunks) == 0 {
		t.Fatal("staging holds no chunk file")
	}
	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	snaps, err := c.ListSnapshots()
	if err != nil || len(snaps) != 1 {
		t.Fatalf("catalog snapshots = %v, %v; want one", snaps, err)
	}
	if !c.Complete(snaps[0]) {
		t.Fatal("commit did not mark the snapshot complete")
	}
	for _, rel := range listFilesUnder(t, l.catalogDir()) {
		top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if !slices.Contains([]string{"snapshots", "trees", "blobs", "discs"}, top) {
			t.Errorf("catalog file %s is outside snapshots, trees, blobs and discs", rel)
		}
	}
	for _, rel := range listFilesUnder(t, l.stagingDir()) {
		top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if top != chunksDirName && top != plansDirName {
			t.Errorf("staging file %s is outside chunks and plans", rel)
		}
	}

	for _, old := range []string{
		filepath.Join(l.stagingDir(), "objects"),
		filepath.Join(l.stagingDir(), "snapshots"),
		filepath.Join(l.stagingDir(), stateLogFileName),
		filepath.Join(l.stagingDir(), discsLedgerName),
		filepath.Join(repo, refsFileName),
	} {
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Errorf("%s exists; it is a path of the old layout", old)
		}
	}
}

// parseDiscUUID parses the hyphenated text form of a disc uuid.
func parseDiscUUID(t *testing.T, text string) [16]byte {
	t.Helper()
	u, err := decodeUUID(strings.ReplaceAll(text, "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	if uuidText(u) != text {
		t.Fatalf("uuid %s is not in the hyphenated lower-case form", text)
	}
	return u
}

// TestInitWritesTheGitignore checks the content of .gitignore and the
// lines that init prints.
func TestInitWritesTheGitignore(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	code, out := runIn(t, repo, "init", "--source="+src)
	if code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	wantOut := "initialized repository " + repo + "\nsource: " + src + "\ndevice: /dev/sr0\nnext: noahsark status\n"
	if out != wantOut {
		t.Fatalf("init output = %q, want %q", out, wantOut)
	}
	l := testLayout(t, repo)
	data, err := os.ReadFile(l.gitignoreFile())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "/lock\n/staging/\n" {
		t.Fatalf(".gitignore = %q, want the lines /lock and /staging/", data)
	}
	for _, dir := range []string{l.stateDir(), l.catalogDir(), l.chunksDir(), l.plansDir()} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("init did not create %s: %v", dir, err)
		}
	}
}

// TestInitKeepsAnExistingGitignore checks that init keeps the lines of
// a .gitignore that exists, and appends only the line that it lacks.
func TestInitKeepsAnExistingGitignore(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if err := writeFile(filepath.Join(repo, gitignoreFileName), "*.tmp\n/lock"); err != nil {
		t.Fatal(err)
	}
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	data, err := os.ReadFile(testLayout(t, repo).gitignoreFile())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "*.tmp\n/lock\n/staging/\n" {
		t.Fatalf(".gitignore = %q, want the old lines and /staging/ once", data)
	}
}

// TestCommitTwiceWritesNoNewFile commits the same source two times, at
// the same time, and checks that the second commit writes no file.
func TestCommitTwiceWritesNoNewFile(t *testing.T) {
	setFakeNow(t, func() time.Time { return time.Date(2026, time.September, 21, 8, 30, 0, 0, time.UTC) })
	repo, src := initAndCommit(t)
	l := testLayout(t, repo)
	files := func() []string {
		return slices.Concat(listFilesUnder(t, l.chunksDir()), listFilesUnder(t, l.catalogDir()))
	}
	before := files()
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("second commit: exit %d: %s", code, out)
	}
	if after := files(); !slices.Equal(before, after) {
		t.Fatalf("files after the second commit = %v, want %v", after, before)
	}
}

// TestGCFreesChunksAndKeepsTheCatalog checks that gc removes the chunk
// files and keeps every file of the catalog, and that ls and log still
// work with no disc and an empty staging.
func TestGCFreesChunksAndKeepsTheCatalog(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	packAndVerifyDisc(t, work, repo, src)
	l := testLayout(t, repo)
	catalogBefore := listFilesUnder(t, l.catalogDir())

	setFakeStdin(t, strings.NewReader("y\n"))
	if code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=0d"); code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if files := listFilesUnder(t, l.stagingDir()); len(files) != 0 {
		t.Fatalf("staging after gc holds %v, want no file", files)
	}
	if after := listFilesUnder(t, l.catalogDir()); !slices.Equal(catalogBefore, after) {
		t.Fatalf("catalog after gc = %v, want %v", after, catalogBefore)
	}

	refs, err := readRefs(l.refsFile())
	if err != nil || len(refs) != 1 {
		t.Fatalf("refs = %v, %v; want one", refs, err)
	}
	var snapID string
	for _, id := range refs {
		snapID = id
	}
	code, out := runCmd(t, "--repo="+repo, "log")
	if code != 0 || !strings.Contains(out, snapID) {
		t.Fatalf("log after gc: exit %d: %s", code, out)
	}
	code, out = runCmd(t, "--repo="+repo, "ls", "--recursive", snapID)
	if code != 0 || !strings.Contains(out, "b.txt") {
		t.Fatalf("ls after gc: exit %d: %s", code, out)
	}
}
