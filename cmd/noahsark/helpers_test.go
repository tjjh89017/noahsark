package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
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

// fakeStdinTTY tells every fake env that its standard input is a
// terminal. A test sets it with setFakeTerminal.
var fakeStdinTTY bool

// setFakeStdin makes r the standard input of every fake env until the
// test ends.
func setFakeStdin(t *testing.T, r io.Reader) {
	t.Helper()
	old := fakeStdin
	fakeStdin = r
	t.Cleanup(func() { fakeStdin = old })
}

// setFakeTerminal makes standard input of every fake env a terminal that
// holds answer, until the test ends. answer is the text that the
// operator types, for example "y\n".
func setFakeTerminal(t *testing.T, answer string) {
	t.Helper()
	setFakeStdin(t, strings.NewReader(answer))
	old := fakeStdinTTY
	fakeStdinTTY = true
	t.Cleanup(func() { fakeStdinTTY = old })
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
// no environment variables, the clock fakeNow, the standard input
// fakeStdin, a terminal on standard input when fakeStdinTTY is true, and
// the fake mount table.
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
		stdinTTY:  fakeStdinTTY,
		getwd:     func() (string, error) { return dir, nil },
		getenv:    func(k string) string { return te.vars[k] },
		now:       func() time.Time { return fakeNow() },
		euid:      os.Geteuid,
		mountinfo: fakeMountinfo,
		deviceOf:  fakeDeviceOf,
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

// repoCatalogDir resolves the catalog directory the same way pack and
// recover do.
func repoCatalogDir(t *testing.T, repo string) string {
	t.Helper()
	return catalog.Dir(repo)
}

// testLayout returns the layout of the repository at repo, from its
// config. A test takes every path of a repository from it.
func testLayout(t *testing.T, repo string) repoLayout {
	t.Helper()
	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	return layoutOf(repo, cfg)
}

// openTestLog opens the state log of the repository at repo.
func openTestLog(t *testing.T, repo string) *stage.Log {
	t.Helper()
	l, err := stage.Open(testLayout(t, repo).stateDir())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// listFilesUnder returns the path of every regular file below dir,
// relative to dir, sorted. A missing dir gives no file.
func listFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == dir {
				return filepath.SkipDir
			}
			return err
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	return files
}

// countByState opens repo's state log and counts every object
// currently in state.
func countByState(t *testing.T, repo string, state stage.State) int {
	t.Helper()
	l, err := stage.Open(testLayout(t, repo).stateDir())
	if err != nil {
		t.Fatal(err)
	}
	return l.CountState(state)
}

// packBurnDisc commits src into repo, packs it, copies the packed tree
// outside the staging directory to stand in for a mounted disc, and
// marks the disc burned. It runs no verify.
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
	copyTree(t, packedTreeDir(t, repo, packOut), mounted)
	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", packedDiscUUID(t, packOut)); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	return mounted
}

// packAndVerifyDisc commits src into repo, packs it, copies the packed
// tree outside the repository's staging directory to stand in for a
// mounted disc, marks it burned, and verifies it, so the disc is
// verified and its tables enter the catalog.
func packAndVerifyDisc(t *testing.T, work, repo, src string) {
	t.Helper()
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, packOut)
	}
	stagedTree := packedTreeDir(t, repo, packOut)
	discUUID := packedDiscUUID(t, packOut)
	mounted := filepath.Join(work, filepath.Base(t.TempDir()))
	copyTree(t, stagedTree, mounted)
	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", discUUID); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
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

// chunkDiscSeqs opens repo's catalog directly and builds the same plan
// restore --dry-run would, restricted to include, returning the
// disc_seq of every disc that plan assigns at least one chunk object
// to. A disc the plan names only for a tree or blob object never needs
// a physical visit: the assembler resolves those from the catalog, so
// this is the set of discs a disc-swap restore of include would
// actually prompt for.
func chunkDiscSeqs(t *testing.T, repo, snapID, include string) []int {
	t.Helper()
	c, err := catalog.Open(repo)
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
	layout := layoutOf(repo, cfg)
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		t.Fatalf("LoadDiscsLedger: %v", err)
	}
	return summarizeDiscs(ledger.Rows, readLogs(t, repo))
}

// packedTreeDir returns the disc root of the disc that pack's output
// names in the repository repo: the tree of its plan directory, or the
// target of that tree for a pack --out disc.
func packedTreeDir(t *testing.T, repo, output string) string {
	t.Helper()
	discUUID, err := decodeUUID(strings.ReplaceAll(packedDiscUUID(t, output), "-", ""))
	if err != nil {
		t.Fatalf("bad uuid line in pack output: %q: %v", output, err)
	}
	tree := testLayout(t, repo).planTree(discUUID)
	if target, err := os.Readlink(tree); err == nil {
		return target
	}
	return tree
}

// readLogs replays the item log and the disc state log of the
// repository at repo, read-only.
func readLogs(t *testing.T, repo string) *stage.Logs {
	t.Helper()
	logs, err := stage.OpenLogs(testLayout(t, repo).stateDir(), false)
	if err != nil {
		t.Fatalf("stage.OpenLogs: %v", err)
	}
	return logs
}

// discState returns the record of the disc uuidText in the disc state
// log of repo.
func discState(t *testing.T, repo, uuidText string) stage.DiscInfo {
	t.Helper()
	u, err := decodeUUID(strings.ReplaceAll(uuidText, "-", ""))
	if err != nil {
		t.Fatalf("bad disc uuid %q: %v", uuidText, err)
	}
	d, _ := readDiscLog(t, repo).Disc(u)
	return d
}

// itemWords counts the derived words of the items whose newest record
// names the disc uuidText in repo.
func itemWords(t *testing.T, repo, uuidText string) map[stage.ItemWord]int {
	t.Helper()
	u, err := decodeUUID(strings.ReplaceAll(uuidText, "-", ""))
	if err != nil {
		t.Fatalf("bad disc uuid %q: %v", uuidText, err)
	}
	logs := readLogs(t, repo)
	words := make(map[stage.ItemWord]int)
	for _, id := range logs.Items.ItemsOfDisc(u) {
		w, _ := logs.Word(id)
		words[w]++
	}
	return words
}

// readDiscLog replays the disc state log of the repository at repo.
func readDiscLog(t *testing.T, repo string) *stage.DiscLog {
	t.Helper()
	l, err := stage.OpenDiscLogReadOnly(testLayout(t, repo).stateDir())
	if err != nil {
		t.Fatalf("stage.OpenDiscLogReadOnly: %v", err)
	}
	return l
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
// directory. The fake mount table lists dst as a read-only mount.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	cmd := exec.Command("cp", "-a", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cp -a %s %s: %v: %s", src, dst, err, out)
	}
	addFakeMount(t, dst, true)
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

// appendConfig adds YAML text to the end of the config file of repo.
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

// discFixture is a repository with one disc in a chosen state. The
// helpers build it through the CLI with the fake env, where a command
// exists for the step.
type discFixture struct {
	work string
	repo string
	src  string
	// uuid is the uuid text of the disc. seq and label name it.
	uuid  string
	seq   uint64
	label string
	// root is a copy of the disc root outside the repository: the
	// stand-in for the mounted disc.
	root string
	// vars maps more placeholders of a state case to their text.
	vars map[string]string
	// cells maps placeholders of the cells of the state x event table,
	// such as N, to the value that the event prints.
	cells map[string]string
}

// cell makes the placeholder name of the cells stand for value. An
// empty value matches the placeholder by its kind.
func (fx *discFixture) cell(name, value string) {
	if fx.cells == nil {
		fx.cells = map[string]string{}
	}
	fx.cells[name] = value
}

// name is the disc name of a message that reports a change:
// disc SEQ "LABEL".
func (fx *discFixture) name() string { return discNameShort(fx.seq, fx.label) }

// set makes the placeholder key stand for value in the texts of a state
// case.
func (fx *discFixture) set(key, value string) {
	if fx.vars == nil {
		fx.vars = map[string]string{}
	}
	fx.vars[key] = value
}

// filler returns the function that replaces the placeholders of a state
// case with the texts of fx.
func (fx *discFixture) filler() func(string) string {
	pairs := []string{
		"{DISC}", fx.name(),
		"{SEQ}", strconv.FormatUint(fx.seq, 10),
		"{LABEL}", fx.label,
		"{UUID}", fx.uuid,
		"{ROOT}", fx.root,
		"{SRC}", fx.src,
		"{REPO}", fx.repo,
		"{REF}", defaultRefName(),
	}
	for k, v := range fx.vars {
		pairs = append(pairs, k, v)
	}
	return strings.NewReplacer(pairs...).Replace
}

// uuidBytes is the uuid of the disc.
func (fx *discFixture) uuidBytes(t *testing.T) [16]byte {
	t.Helper()
	u, err := decodeUUID(strings.ReplaceAll(fx.uuid, "-", ""))
	if err != nil {
		t.Fatalf("bad disc uuid %q: %v", fx.uuid, err)
	}
	return u
}

// run runs the CLI with --repo of the fixture, then args.
func (fx *discFixture) run(t *testing.T, args ...string) (int, string) {
	t.Helper()
	return runCmd(t, append([]string{"--repo=" + fx.repo}, args...)...)
}

// mustRun runs the CLI like run, and fails the test on an exit code
// other than 0.
func (fx *discFixture) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, out := fx.run(t, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d: %s", args, code, out)
	}
	return out
}

// repoWithDisc returns a repository with one disc in state, and nothing
// staged:
//
//   - packed: init, commit, pack.
//   - burned: then disc burned.
//   - verified: then a verify of the copy of the disc root.
//   - on disc only: then gc with no wait.
//   - lost: a verified disc, then the records that disc lost writes.
//   - missing: two discs packed, the repository removed, and recover of
//     the second disc only. The fixture names the first disc.
//   - undone: a packed disc, then the records that pack --undo writes.
func repoWithDisc(t *testing.T, state stage.DiscState) *discFixture {
	t.Helper()
	if state == stage.DiscMissing {
		return repoWithMissingDisc(t)
	}
	work := t.TempDir()
	fx := &discFixture{work: work, repo: filepath.Join(work, "repo"), src: writeFixtureSource(t)}
	if code, out := runIn(t, fx.repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	fx.mustRun(t, "commit", fx.src)
	packOut := fx.mustRun(t, "pack", "--capacity=64MiB")
	fx.uuid = packedDiscUUID(t, packOut)
	fx.seq, fx.label = 0, defaultRefName()+" disc 0"
	fx.root = filepath.Join(work, "disc")
	copyTree(t, packedTreeDir(t, fx.repo, packOut), fx.root)

	switch state {
	case stage.DiscPacked:
	case stage.DiscUndone:
		markPackUndoneInLog(t, fx)
	case stage.DiscBurned:
		fx.mustRun(t, "disc", "burned", fx.uuid)
	case stage.DiscVerified, stage.DiscOnDiscOnly, stage.DiscLost:
		fx.mustRun(t, "disc", "burned", fx.uuid)
		fx.mustRun(t, "verify", fx.root)
		switch state {
		case stage.DiscOnDiscOnly:
			fx.mustRun(t, "gc", "--force-after=0d")
		case stage.DiscLost:
			markDiscLostInLog(t, fx)
		}
	default:
		t.Fatalf("repoWithDisc: no fixture for state %s", state)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != state {
		t.Fatalf("repoWithDisc: disc state %s, want %s", got, state)
	}
	return fx
}

// repoWithMissingDisc packs two discs, removes the repository, and
// recovers it from the second disc only. The first disc is then
// missing. The fixture names the first disc; its root is the pack --out
// directory of that disc.
func repoWithMissingDisc(t *testing.T) *discFixture {
	t.Helper()
	work := t.TempDir()
	fx := &discFixture{work: work, repo: filepath.Join(work, "repo"), src: writeFixtureSource(t)}
	if code, out := runIn(t, fx.repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	fx.mustRun(t, "commit", fx.src)
	fx.root = filepath.Join(work, "disc0")
	fx.uuid = packedDiscUUID(t, fx.mustRun(t, "pack", "--capacity=64MiB", "--out="+fx.root))
	fx.seq, fx.label = 0, defaultRefName()+" disc 0"
	if err := os.WriteFile(filepath.Join(fx.src, "second.txt"), []byte("content of the second disc"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.mustRun(t, "commit", fx.src)
	second := filepath.Join(work, "disc1")
	fx.mustRun(t, "pack", "--capacity=64MiB", "--out="+second)
	if err := os.RemoveAll(fx.repo); err != nil {
		t.Fatal(err)
	}
	addFakeMount(t, fx.root, true)
	addFakeMount(t, second, true)
	if code, out := fx.run(t, "recover", "--source="+fx.src, "--disc="+second); code != 1 {
		t.Fatalf("recover of the second disc: exit %d, want 1: %s", code, out)
	}
	if got := discState(t, fx.repo, fx.uuid).State; got != stage.DiscMissing {
		t.Fatalf("repoWithMissingDisc: disc state %s, want missing", got)
	}
	return fx
}

// markDiscLostInLog writes the records that disc lost writes for the
// disc of fx: its Packed items return to Staged, its OnDisc items become
// Lost, the Lost event, and the removal of its plan directory.
func markDiscLostInLog(t *testing.T, fx *discFixture) {
	t.Helper()
	u := fx.uuidBytes(t)
	logs, err := stage.OpenLogs(testLayout(t, fx.repo).stateDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Items.MarkStaged(stage.ReasonDiscLost, logs.Items.ItemsOfDiscInState(u, stage.Packed)...); err != nil {
		t.Fatal(err)
	}
	if err := logs.Items.MarkLost(logs.Items.ItemsOfDiscInState(u, stage.OnDisc)...); err != nil {
		t.Fatal(err)
	}
	if err := logs.Discs.Append(discEvent(fakeNow(), u, stage.EventLost)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(testLayout(t, fx.repo).planDir(u)); err != nil {
		t.Fatal(err)
	}
}

// markPackUndoneInLog writes the records that pack --undo writes for the
// packed disc of fx: its items return to Staged, the PackUndone event,
// and the removal of its ledger row, its catalog tables and its plan
// directory.
func markPackUndoneInLog(t *testing.T, fx *discFixture) {
	t.Helper()
	u := fx.uuidBytes(t)
	layout := testLayout(t, fx.repo)
	logs, err := stage.OpenLogs(layout.stateDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.Items.MarkPackUndone(logs.Items.ItemsOfDiscInState(u, stage.Packed)...); err != nil {
		t.Fatal(err)
	}
	if err := logs.Discs.Append(discEvent(fakeNow(), u, stage.EventPackUndone)); err != nil {
		t.Fatal(err)
	}
	cfg, err := readConfig(configPath(fx.repo))
	if err != nil {
		t.Fatal(err)
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		t.Fatal(err)
	}
	rows := slices.DeleteFunc(ledger.Rows, func(r format.DiscsRow) bool { return r.DiscUUID == u })
	if err := image.SaveDiscsLedger(layout.discsLedgerFile(), repoUUID, rows); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(fx.repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveDisc(u); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(layout.planDir(u)); err != nil {
		t.Fatal(err)
	}
}
