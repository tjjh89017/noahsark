package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	if code, out := runCmd(t, "init", "--repo="+repo, "--capacity=64MiB"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	code, out := runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID := snapshotIDFromCommit(t, out)

	treeDir := filepath.Join(work, "tree")
	if code, out := runCmd(t, "pack", "--repo="+repo, "--out="+treeDir); code != 0 {
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
	want := "noahsark: sync is a Phase 2 command; not available in Phase 1"
	if !strings.Contains(out, want) {
		t.Fatalf("output = %q, want it to contain %q", out, want)
	}
}

// TestPackWithoutCapacityRefused asserts that pack refuses to run when
// neither --capacity nor the config default is set.
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
