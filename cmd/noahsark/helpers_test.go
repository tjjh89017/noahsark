package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// fakeNow is the clock of every fake env. A test that needs a fixed time
// replaces it, and puts the old value back when it ends.
var fakeNow = time.Now

// fakeStdin is the standard input of every fake env. A test replaces it
// with setFakeStdin. nil gives an empty standard input.
var fakeStdin io.Reader

// setFakeStdin makes r the standard input of every fake env until the
// test ends.
func setFakeStdin(t *testing.T, r io.Reader) {
	t.Helper()
	old := fakeStdin
	fakeStdin = r
	t.Cleanup(func() { fakeStdin = old })
}

// defaultRefName is the ref a commit moves with no --ref under the fake
// clock: the local date of today, as YYYY-MM-DD.
func defaultRefName() string {
	return fakeNow().Format("2006-01-02")
}

// testEnv is a fake env and the buffers that collect its output.
type testEnv struct {
	*env
	out    bytes.Buffer
	errOut bytes.Buffer
	vars   map[string]string
}

// newTestEnv returns a fake env whose working directory is dir. It has
// no terminal, no environment variables, the clock fakeNow and the
// standard input fakeStdin.
func newTestEnv(dir string) *testEnv {
	te := &testEnv{vars: map[string]string{}}
	stdin := fakeStdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	te.env = &env{
		stdout:    &te.out,
		stderr:    &te.errOut,
		stdin:     stdin,
		getwd:     func() (string, error) { return dir, nil },
		getenv:    func(k string) string { return te.vars[k] },
		now:       func() time.Time { return fakeNow() },
		euid:      os.Geteuid,
		mountinfo: func() (io.ReadCloser, error) { return nil, errors.New("no mount table in a test") },
	}
	return te
}

// run runs the CLI with args in the fake env. It returns the exit code
// and the text of standard output and then standard error of this run.
func (te *testEnv) run(args ...string) (int, string) {
	te.out.Reset()
	te.errOut.Reset()
	te.global = globalOptions{}
	code := run(te.env, args)
	return code, te.out.String() + te.errOut.String()
}

// runIn runs the CLI with args in a fake env whose working directory is
// dir. It creates dir first. It returns the exit code and the text of
// standard output and then standard error.
func runIn(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return newTestEnv(dir).run(args...)
}

// runCmd runs the CLI with args in a fake env whose working directory is
// a new empty directory. It returns the exit code and the text of
// standard output and then standard error.
func runCmd(t *testing.T, args ...string) (int, string) {
	t.Helper()
	return newTestEnv(t.TempDir()).run(args...)
}

// repoCacheDir resolves the cache directory the same way pack and
// recover do.
func repoCacheDir(t *testing.T, repo string) string {
	t.Helper()
	return cache.Dir(repo)
}

// countByState opens repo's staging state log and counts every object
// currently in state.
func countByState(t *testing.T, repo string, state stage.State) int {
	t.Helper()
	l, err := stage.Open(filepath.Join(repo, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	return l.CountState(state)
}

// packBurnDisc commits src into repo, packs it, copies the packed tree
// outside the staging directory to stand in for a mounted disc, and
// marks the disc burned. It runs no verify, so each test drives the
// verify count itself.
func packBurnDisc(t *testing.T, work, repo, src string) string {
	t.Helper()
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	mounted := filepath.Join(work, filepath.Base(t.TempDir()))
	copyTree(t, packedTreeDir(t, packOut), mounted)
	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", packedDiscUUID(t, packOut)); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	return mounted
}

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
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, packOut)
	discUUID := packedDiscUUID(t, packOut)
	mounted := filepath.Join(work, filepath.Base(t.TempDir()))
	copyTree(t, stagedTree, mounted)
	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	for copyNumber := 1; copyNumber <= 2; copyNumber++ {
		if code, out := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
			t.Fatalf("verify copy %d: exit %d: %s", copyNumber, code, out)
		}
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

// lsFixture runs init, commit and pack against writeFixtureSource's tree
// and returns the packed disc root, the snapshot id, and the source
// directory ls's PATH argument is relative to.
func lsFixture(t *testing.T) (treeDir, snapID, src string) {
	t.Helper()
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src = writeFixtureSource(t)

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
	return treeDir, snapID, src
}

// rootPath is the include-path form of a fixture's own source directory:
// its absolute path with the leading slash stripped.
func rootPath(src string) string {
	return strings.TrimPrefix(src, "/")
}

// writeRefsCarryFixture writes a small source tree whose content depends
// on tag, so two calls produce two distinct commits.
func writeRefsCarryFixture(t *testing.T, tag string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("content of "+tag), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// chunkDiscSeqs opens repo's cache directly and builds the same plan
// restore --dry-run would, restricted to include, returning the
// disc_seq of every disc that plan assigns at least one chunk object
// to. A disc the plan names only for a tree or blob object never needs
// a physical visit: the assembler resolves those from the cache, so
// this is the set of discs a disc-swap restore of include would
// actually prompt for.
func chunkDiscSeqs(t *testing.T, repo, snapID, include string) []int {
	t.Helper()
	c, err := cache.Open(repoCacheDir(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	id, err := object.ParseID(snapID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	result, err := plan.Build(c, snap, id, []string{include})
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int
	for _, d := range result.Discs {
		for _, o := range d.Objects {
			if o.Kind == format.ObjectKindChunk {
				seqs = append(seqs, int(d.DiscSeq))
				break
			}
		}
	}
	return seqs
}

// mountDisc replaces mountDir with a symlink to discRoot, standing in
// for an operator swapping the disc a real drive has mounted there.
func mountDisc(t *testing.T, mountDir, discRoot string) {
	t.Helper()
	if err := os.RemoveAll(mountDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(discRoot, mountDir); err != nil {
		t.Fatal(err)
	}
}

// scriptedStdin feeds one newline per Scan, running a step first so a
// test can swap the mount directory's contents right where a real
// operator would, between the prompt and pressing Enter. Reading past
// the last step reports EOF, the same as a closed terminal.
type scriptedStdin struct {
	steps []func()
	next  int
}

func (s *scriptedStdin) Read(p []byte) (int, error) {
	if s.next >= len(s.steps) {
		return 0, io.EOF
	}
	s.steps[s.next]()
	s.next++
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = '\n'
	return 1, nil
}

// partFilesUnder returns every part file below dir, by path.
func partFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.Contains(d.Name(), ".noahsark-part") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

// statusDiscs reads repo's disc summaries the same way "status"
// computes them, straight through the internal packages: there is no
// --json to shell out through and parse.
func statusDiscs(t *testing.T, repo string) []discSummary {
	t.Helper()
	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		t.Fatalf("decodeUUID: %v", err)
	}
	ledger, err := image.LoadDiscsLedger(cfg.StagingDir, repoUUID)
	if err != nil {
		t.Fatalf("LoadDiscsLedger: %v", err)
	}
	stageLog, err := stage.OpenReadOnly(cfg.StagingDir)
	if err != nil {
		t.Fatalf("stage.OpenReadOnly: %v", err)
	}
	return summarizeDiscs(ledger.Rows, stageLog, cfg.MinVerifiedCopies)
}

// packedTreeDir picks the "tree: DIR" line out of pack's output.
func packedTreeDir(t *testing.T, output string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if after, found := strings.CutPrefix(line, "tree: "); found {
			return after
		}
	}
	t.Fatalf("no tree line in pack output: %q", output)
	return ""
}

// packedDiscUUID picks the disc uuid out of pack's "uuid: UUID" line.
func packedDiscUUID(t *testing.T, output string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if after, found := strings.CutPrefix(line, "uuid: "); found {
			return after
		}
	}
	t.Fatalf("no uuid line in pack output: %q", output)
	return ""
}

// copyTree copies src to dst, standing in for burning src's bytes to a
// disc and mounting it back (or loop-mounting the image before it is
// burned): a byte-identical tree outside the repository's staging
// directory.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	cmd := exec.Command("cp", "-a", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cp -a %s %s: %v: %s", src, dst, err, out)
	}
}

// repoDirFromTreeDir recovers a fixture's repository directory from its
// packed tree directory, both children of the same lsFixture work
// directory ("work/tree" and "work/repo").
func repoDirFromTreeDir(t *testing.T, treeDir string) string {
	t.Helper()
	return filepath.Join(filepath.Dir(treeDir), "repo")
}

// snapshotIDFromCommit picks the "snapshot <id>" line out of commit's
// output.
func snapshotIDFromCommit(t *testing.T, output string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if rest, ok := strings.CutPrefix(line, "snapshot "); ok {
			return rest
		}
	}
	t.Fatalf("no snapshot line in commit output: %q", output)
	return ""
}

// writeFixtureSource creates a small deterministic source tree to commit.
func writeFixtureSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("content of a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("content of b, a bit longer than a"), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// compareTrees walks want and asserts that got holds byte-identical
// files at the same relative paths.
func compareTrees(t *testing.T, got, want string) {
	t.Helper()
	err := filepath.Walk(want, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(want, path)
		if err != nil {
			return err
		}
		gotPath := filepath.Join(got, rel)
		if info.IsDir() {
			if st, err := os.Stat(gotPath); err != nil || !st.IsDir() {
				t.Errorf("missing restored directory %s", gotPath)
			}
			return nil
		}
		wantBytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		gotBytes, err := os.ReadFile(gotPath)
		if err != nil {
			t.Errorf("reading restored file %s: %v", gotPath, err)
			return nil
		}
		if !bytes.Equal(gotBytes, wantBytes) {
			t.Errorf("restored file %s does not match source", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fakeStatInfo wraps a real os.FileInfo but reports a caller-chosen size,
// so the Writer's Stat seam can make one restat disagree with the one
// before it, deterministically, without touching the real filesystem
// clock.
type fakeStatInfo struct {
	os.FileInfo
	size int64
}

func (f fakeStatInfo) Size() int64 { return f.size }

func (f fakeStatInfo) Sys() any { return nil }

// writeMultiDiscFixtureSource creates a source tree of several
// subdirectories, each holding one file of pseudo-random content, sized
// so a small forced capacity has to split the commit across several
// runs.
func writeMultiDiscFixtureSource(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	rng := rand.New(rand.NewSource(42))
	for i := range 6 {
		dir := filepath.Join(src, fmt.Sprintf("sub%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 600_000)
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.bin"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

// packSectors converts a byte budget to a --capacity value with a unit.
// --capacity refuses a bare number, so the budget is rendered in whole
// binary kibibytes, which is exact for a whole number of sectors.
func packSectors(bytes uint64) string {
	const sectorSize = 2048
	sectors := (bytes + sectorSize - 1) / sectorSize
	return strconv.FormatUint(sectors*2, 10) + "KiB"
}

// appendConfig adds text to a repository's config file.
func appendConfig(t *testing.T, repo, text string) {
	t.Helper()
	f, err := os.OpenFile(configPath(repo), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// initAndCommit makes a repository with one commit and returns the
// repository directory and the source directory.
func initAndCommit(t *testing.T) (repo, src string) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "repo")
	src = writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	return repo, src
}

// writeFile writes content to path, creating its parent directories.
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// snapshotArgFixture commits and packs one source tree, and returns the
// repository, the packed disc root and the snapshot id commit printed.
func snapshotArgFixture(t *testing.T) (repo, treeDir, snapID string) {
	t.Helper()
	work := t.TempDir()
	repo = filepath.Join(work, "repo")
	src := writeFixtureSource(t)

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

// setFakeNow sets the clock of every fake env until the test ends.
func setFakeNow(t *testing.T, now func() time.Time) {
	t.Helper()
	old := fakeNow
	t.Cleanup(func() { fakeNow = old })
	fakeNow = now
}
