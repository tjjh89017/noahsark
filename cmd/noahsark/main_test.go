package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

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

	if code, out := runCmd(t, "verify", "--image="+treeDir); code != 0 {
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

// TestLaterPhaseFlagRefused asserts that a later-phase flag is refused
// with a message naming the flag and the phase, and exit code 2, before
// any repository lookup happens.
func TestLaterPhaseFlagRefused(t *testing.T) {
	code, out := runCmd(t, "commit", "--from=/nowhere", "/nowhere")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output: %s", code, out)
	}
	want := "noahsark: commit: --from is a Phase 2 option; not available in Phase 1"
	if !strings.Contains(out, want) {
		t.Fatalf("output = %q, want it to contain %q", out, want)
	}
}

// TestLaterPhaseCommandRefused asserts that a whole later-phase command
// name is refused the same way.
func TestLaterPhaseCommandRefused(t *testing.T) {
	code, out := runCmd(t, "sync", "/nowhere")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output: %s", code, out)
	}
	want := "noahsark: sync is a Phase 2 command; this build implements Phase 1"
	if !strings.Contains(out, want) {
		t.Fatalf("output = %q, want it to contain %q", out, want)
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
	if !strings.Contains(out, "target capacity is required") {
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

// TestTopLevelUsageListsEveryFlag asserts that every flag a command's own
// "-h" output defines also appears on that command's line, or lines, in
// the top-level "--help" summary. It catches the summary going stale when
// a command gains a flag, the bug this test was added to guard against.
func TestTopLevelUsageListsEveryFlag(t *testing.T) {
	_, topText := runCmd(t, "--help")

	// alwaysOptional names flags every top-level line may leave out:
	// --repo is accepted almost everywhere and is only spelled out where
	// its meaning differs (verify); --disc and --discs-dir are explained
	// once, in the paragraph under the command list, instead of being
	// repeated on every disc-reading command's line.
	alwaysOptional := map[string]bool{
		"repo":      true,
		"disc":      true,
		"discs-dir": true,
	}

	cases := []struct {
		args    []string // invoked with a trailing "-h"
		topLine string   // the top-level line's prefix, as it appears indented
	}{
		{[]string{"init"}, "  init"},
		{[]string{"commit"}, "  commit"},
		{[]string{"pack"}, "  pack"},
		{[]string{"image", "build"}, "  image build"},
		{[]string{"verify"}, "  verify"},
		{[]string{"restore"}, "  restore"},
		{[]string{"ls"}, "  ls"},
		{[]string{"log"}, "  log"},
		{[]string{"plan"}, "  plan"},
		{[]string{"rebuild-cache"}, "  rebuild-cache"},
		{[]string{"disc", "list"}, "  disc list"},
		{[]string{"disc", "burned"}, "  disc burned"},
		{[]string{"gc"}, "  gc"},
	}

	flagNamePattern := regexp.MustCompile(`(?m)^  -(\S+)`)

	for _, c := range cases {
		args := append(append([]string{}, c.args...), "-h")
		_, cmdHelp := runCmd(t, args...)
		matches := flagNamePattern.FindAllStringSubmatch(cmdHelp, -1)

		lineJoinPattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(c.topLine) + `\b.*$`)
		lines := lineJoinPattern.FindAllString(topText, -1)
		if len(lines) == 0 {
			t.Fatalf("%s: no top-level line found for prefix %q", strings.Join(c.args, " "), c.topLine)
		}
		block := strings.Join(lines, "\n")

		for _, m := range matches {
			name := m[1]
			if alwaysOptional[name] {
				continue
			}
			if !strings.Contains(block, "-"+name) {
				t.Errorf("%s: top-level usage is missing --%s\nblock:\n%s", strings.Join(c.args, " "), name, block)
			}
		}
	}
}

// TestNotYetImplementedFlagsRefused asserts, for every command and flag
// notYetImplementedFlags names, that the flag is refused with the clear
// "not in this build yet" message and exit code 2, not the raw flag
// package error, no matter what else is on the command line.
func TestNotYetImplementedFlagsRefused(t *testing.T) {
	for cmd, flags := range notYetImplementedFlags {
		for flagName := range flags {
			t.Run(cmd+" "+flagName, func(t *testing.T) {
				args := append(strings.Fields(cmd), flagName)
				code, out := runCmd(t, args...)
				if code != 2 {
					t.Fatalf("%s: exit code = %d, want 2; output: %s", strings.Join(args, " "), code, out)
				}
				want := fmt.Sprintf("noahsark: %s: flag %s is not in this build yet", cmd, flagName)
				if !strings.Contains(out, want) {
					t.Fatalf("output = %q, want it to contain %q", out, want)
				}
				if strings.Contains(out, "flag provided but not defined") {
					t.Fatalf("output = %q, want no raw flag package error", out)
				}
			})
		}
	}
}

// TestProgressFlags checks commit's progress line is off by default in a
// test process (stderr is not a terminal), forced on by --progress,
// forced off by --no-progress and --quiet even when --progress is not
// given, and that --progress and --no-progress together is a usage
// error.
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

	if code, out := runCmd(t, "commit", "--repo="+repo, "--progress", src); code != 0 || !strings.Contains(out, "commit:") {
		t.Fatalf("--progress: exit %d, expected a commit progress line, got %q", code, out)
	}

	if code, out := runCmd(t, "commit", "--repo="+repo, "--no-progress", src); code != 0 || strings.Contains(out, "commit:") {
		t.Fatalf("--no-progress: exit %d, expected no commit progress line, got %q", code, out)
	}

	if code, out := runCmd(t, "commit", "--repo="+repo, "--quiet", src); code != 0 || strings.Contains(out, "commit:") {
		t.Fatalf("--quiet: exit %d, expected no commit progress line, got %q", code, out)
	}

	if code, _ := runCmd(t, "commit", "--repo="+repo, "--progress", "--no-progress", src); code != 2 {
		t.Fatalf("--progress --no-progress: exit %d, want 2", code)
	}
}
