package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestMain puts the local cache of every test in this package under one
// temporary directory. A test that does not set cache.dir would
// otherwise write into the operator's own cache directory, and leave one
// directory there for each repository uuid it made.
func TestMain(m *testing.M) {
	base, err := os.MkdirTemp("", "noahsark-test-cache")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CACHE_HOME", base); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(base)
	os.Exit(code)
}

// runCmd runs one command in process and returns its exit code and the
// combined stdout and stderr text.
func runCmd(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String() + errOut.String()
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

// TestFullSequence runs init, commit, pack, verify and restore in
// sequence against an unpacked tree, and compares the restored source
// with the original, byte for byte.
func TestFullSequence(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--capacity=64MiB", "--out="+treeDir); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}

	if code, out := runCmd(t, "verify", treeDir); code != 0 {
		t.Fatalf("verify: exit %d: %s", code, out)
	}

	restoredDir := filepath.Join(work, "restored")
	if code, out := runCmd(t, "restore", treeDir, snapID, restoredDir); code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}

	compareTrees(t, filepath.Join(restoredDir, src), src)
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

// TestUnknownCommandRefused asserts that an unrecognized command name
// exits 2 with a message naming it, matching an unknown flag's exit
// code.
func TestUnknownCommandRefused(t *testing.T) {
	code, out := runCmd(t, "sync", "/nowhere")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output: %s", code, out)
	}
	if !strings.Contains(out, `unknown command "sync"`) {
		t.Fatalf("output = %q, want it to name the unknown command", out)
	}
}

// TestUnknownFlagRefused asserts that a flag no command defines exits 2,
// not the process crashing or a silent success.
func TestUnknownFlagRefused(t *testing.T) {
	code, _ := runCmd(t, "commit", "--no-such-flag", "/nowhere")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
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
func (f fakeStatInfo) Sys() any    { return nil }

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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	oldNewWriter := newWriter
	defer func() { newWriter = oldNewWriter }()
	var calls int
	newWriter = func(stagingDir string) *object.Writer {
		w := object.NewWriter(stagingDir)
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

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	// A directory in refs.txt's place makes writeRefs's os.WriteFile
	// fail, without needing a permission trick that root would ignore.
	if err := os.MkdirAll(filepath.Join(repo, "refs.txt"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 1 {
		t.Fatalf("commit: exit %d, want 1 (the ref move must fail); output: %s", code, out)
	}

	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	snapEntries, err := os.ReadDir(filepath.Join(cfg.StagingDir, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapEntries) != 1 {
		t.Fatalf("staging/snapshots has %d entries, want exactly 1", len(snapEntries))
	}
	snapID, err := object.ParseID(snapEntries[0].Name())
	if err != nil {
		t.Fatal(err)
	}

	l, err := stage.Open(cfg.StagingDir)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Get(snapID)
	if !ok || rec.State != stage.Staged {
		t.Fatalf("snapshot %s state = %+v, ok=%v, want a Staged record despite the ref move failing", snapEntries[0].Name(), rec, ok)
	}
}

// TestPackWithoutCapacityRefused asserts that pack refuses to run when
// --capacity is not given: the config carries no capacity default, so
// every pack must give its own.
func TestPackWithoutCapacityRefused(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "pack", "--repo="+repo, "--out="+filepath.Join(work, "tree"))
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output: %s", code, out)
	}
	if !strings.Contains(out, "no capacity") || !strings.Contains(out, "pack.capacity") {
		t.Fatalf("output = %q, want it to mention the missing capacity", out)
	}
}

// TestNoArgsPrintsUsage asserts the exit-code and usage contract for no
// arguments and -h.
func TestNoArgsPrintsUsage(t *testing.T) {
	if code, out := runCmd(t); code != 2 || !strings.Contains(out, "usage:") {
		t.Fatalf("no args: exit %d, output %q", code, out)
	}
	if code, out := runCmd(t, "-h"); code != 0 || !strings.Contains(out, "usage:") {
		t.Fatalf("-h: exit %d, output %q", code, out)
	}
}

// TestProgressFlags checks commit's progress line is off by default in a
// test process (stderr is not a terminal), and forced off by
// --no-progress and --quiet.
func TestProgressFlags(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	if code, out := runCmd(t, "commit", "--repo="+repo, src); code != 0 || strings.Contains(out, "commit:") {
		t.Fatalf("default (non-terminal stderr): exit %d, expected no commit progress line, got %q", code, out)
	}

	if code, out := runCmd(t, "commit", "--repo="+repo, "--no-progress", src); code != 0 || strings.Contains(out, "commit:") {
		t.Fatalf("--no-progress: exit %d, expected no commit progress line, got %q", code, out)
	}

	if code, out := runCmd(t, "commit", "--repo="+repo, "--quiet", src); code != 0 || strings.Contains(out, "commit:") {
		t.Fatalf("--quiet: exit %d, expected no commit progress line, got %q", code, out)
	}
}
