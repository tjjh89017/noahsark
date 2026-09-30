package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// saveTracked copies state/ and catalog/ of repo into a new directory,
// as a git commit keeps them, and returns that directory.
func saveTracked(t *testing.T, repo string) string {
	t.Helper()
	saved := t.TempDir()
	for _, d := range []string{"state", "catalog"} {
		copyTreePlain(t, filepath.Join(repo, d), filepath.Join(saved, d))
	}
	return saved
}

// restoreTracked puts the state/ and catalog/ of saved back into repo, as
// a git checkout of an old commit does. staging/ keeps its content.
func restoreTracked(t *testing.T, repo, saved string) {
	t.Helper()
	for _, d := range []string{"state", "catalog"} {
		if err := os.RemoveAll(filepath.Join(repo, d)); err != nil {
			t.Fatal(err)
		}
		copyTreePlain(t, filepath.Join(saved, d), filepath.Join(repo, d))
	}
}

func copyTreePlain(t *testing.T, src, dst string) {
	t.Helper()
	if out, err := exec.Command("cp", "-a", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("cp -a %s %s: %v: %s", src, dst, err, out)
	}
}

const rollbackRefusal = "state/ went back to an older version"

// TestGitRollbackRefused is the case of the review: state/ and catalog/
// go back to the commit before pack, after gc freed the chunks of the
// disc. Each command that changes state refuses, and changes no file.
// status warns and goes on.
func TestGitRollbackRefused(t *testing.T) {
	work := t.TempDir()
	repo, src := initAndCommit(t)
	saved := saveTracked(t, repo)
	packAndVerifyDisc(t, work, repo, src)
	if code, out := runCmd(t, "--repo="+repo, "gc", "--force-after=0s"); code != 0 {
		t.Fatalf("gc: %s", out)
	}
	after := saveTracked(t, repo)
	restoreTracked(t, repo, saved)
	layout := testLayout(t, repo)
	stateBefore := listFilesUnder(t, layout.stateDir())
	logBefore, err := os.ReadFile(layout.stateLogFile())
	if err != nil {
		t.Fatal(err)
	}

	te := newTestEnv(t.TempDir())
	te.run("--repo="+repo, "status")
	if !strings.Contains(te.errOut.String(), "noahsark: status: warning: "+rollbackRefusal) {
		t.Errorf("status stderr %q, want the roll back warning", te.errOut.String())
	}
	if strings.Contains(te.out.String(), rollbackRefusal) {
		t.Errorf("status stdout holds the warning: %q", te.out.String())
	}

	for _, args := range [][]string{
		{"pack", "--capacity=64MiB"},
		{"commit", src},
		{"gc"},
		{"--force-yes", "disc", "lost", "0"},
	} {
		code, out := runCmd(t, append([]string{"--repo=" + repo}, args...)...)
		if code != 1 || !strings.Contains(out, rollbackRefusal) || !strings.Contains(out, "put state/ and catalog/ forward again with git, or run recover with each disc into a new repository") {
			t.Errorf("%v after the roll back: exit %d, want 1 and the refusal: %s", args, code, out)
		}
	}
	if got := listFilesUnder(t, layout.stateDir()); strings.Join(got, ",") != strings.Join(stateBefore, ",") {
		t.Errorf("state files %v after the refusals, want %v", got, stateBefore)
	}
	if logAfter, _ := os.ReadFile(layout.stateLogFile()); string(logAfter) != string(logBefore) {
		t.Error("a refused command changed the state log")
	}

	// Forward again: the newest state/ and catalog/ come back.
	restoreTracked(t, repo, after)
	if code, out := runCmd(t, "--repo="+repo, "gc"); code != 0 {
		t.Errorf("gc after the forward step: exit %d: %s", code, out)
	}
}

// TestGitRollbackKeepsThePlanDirectory goes back to the commit before
// pack while the disc is packed. pack refuses before it removes the plan
// directory of the disc that the old state does not know. After state/
// and catalog/ go forward again, pack works.
func TestGitRollbackKeepsThePlanDirectory(t *testing.T) {
	repo, src := initAndCommit(t)
	before := saveTracked(t, repo)
	code, packOut := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 0 {
		t.Fatalf("pack: %s", packOut)
	}
	u, err := decodeUUID(strings.ReplaceAll(packedDiscUUID(t, packOut), "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	after := saveTracked(t, repo)
	restoreTracked(t, repo, before)

	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code != 1 || !strings.Contains(out, rollbackRefusal) {
		t.Fatalf("pack after the roll back: exit %d, want 1 and the refusal: %s", code, out)
	}
	if _, err := os.Stat(testLayout(t, repo).planTree(u)); err != nil {
		t.Fatalf("the plan tree of the packed disc is gone after the refusal: %v", err)
	}

	restoreTracked(t, repo, after)
	if err := os.WriteFile(filepath.Join(src, "more.txt"), []byte("more content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit after the forward step: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB"); code != 0 || !strings.Contains(out, "packed disc 1 ") {
		t.Fatalf("pack after the forward step: exit %d: %s", code, out)
	}
}

// TestNewStagingDirectoryIsNoRollback removes the staging directory, as
// a clone on a second computer has none. pack and commit refuse while
// the Staged items need it. After mkdir, commit writes the chunk files
// again, and pack works.
func TestNewStagingDirectoryIsNoRollback(t *testing.T) {
	repo, src := initAndCommit(t)
	staging := testLayout(t, repo).stagingDir()
	if err := os.RemoveAll(staging); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"pack", "--capacity=64MiB"}, {"commit", src}} {
		code, out := runCmd(t, append([]string{"--repo=" + repo}, args...)...)
		if code != 1 || strings.Contains(out, rollbackRefusal) || !strings.Contains(out, "staging directory "+staging+" does not exist") {
			t.Fatalf("%s with no staging directory: exit %d, want the refusal of a lost staging directory: %s", args[0], code, out)
		}
	}
	if err := os.Mkdir(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB")
	if code == 0 || strings.Contains(out, rollbackRefusal) {
		t.Fatalf("pack with no chunk file: exit %d, want a failure that is not a roll back: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB"); code != 0 {
		t.Fatalf("pack after commit: exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(testLayout(t, repo).stagingDir(), stage.MarkFileName)); err != nil {
		t.Fatalf("no mark after pack: %v", err)
	}
}

// TestRollbackWarningWithoutLock checks the warning of a command that
// does not replay the logs.
func TestRollbackWarningWithoutLock(t *testing.T) {
	repo, _ := initAndCommit(t)
	before := saveTracked(t, repo)
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB"); code != 0 {
		t.Fatalf("pack: %s", out)
	}
	restoreTracked(t, repo, before)
	te := newTestEnv(t.TempDir())
	warnRollback("restore", testLayout(t, repo), te.stderr)
	if !strings.HasPrefix(te.errOut.String(), "noahsark: restore: warning: "+rollbackRefusal) {
		t.Fatalf("warning %q", te.errOut.String())
	}
	for _, args := range [][]string{{"log"}, {"ls", "any-ref"}} {
		te := newTestEnv(t.TempDir())
		te.run(append([]string{"--repo=" + repo}, args...)...)
		if !strings.Contains(te.errOut.String(), "noahsark: "+args[0]+": warning: "+rollbackRefusal) {
			t.Errorf("%s stderr %q, want the roll back warning", args[0], te.errOut.String())
		}
	}
}

// TestTornTailWhileAnotherCommandWrites holds the lock while status reads
// a log with a part of a record at its end. The warning says that
// another command writes the log. Without the lock, it names a crash.
func TestTornTailWhileAnotherCommandWrites(t *testing.T) {
	repo, _ := initAndCommit(t)
	f, err := os.OpenFile(testLayout(t, repo).stateLogFile(), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	lk, err := repolock.Acquire(repo)
	if err != nil {
		t.Fatal(err)
	}
	te := newTestEnv(t.TempDir())
	te.run("--repo="+repo, "status")
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	if want := "noahsark: status: the state log ends in a part of a record; 3 byte(s) after the last valid record were ignored; another noahsark command writes the log at this time"; !strings.Contains(te.errOut.String(), want) {
		t.Errorf("status stderr with the lock held %q, want %q", te.errOut.String(), want)
	}
	if strings.Contains(te.out.String(), "record") {
		t.Errorf("status stdout holds a warning: %q", te.out.String())
	}

	te = newTestEnv(t.TempDir())
	te.run("--repo="+repo, "status")
	if !strings.Contains(te.errOut.String(), "matching a crash during an earlier append") {
		t.Errorf("status stderr with no lock held %q, want the crash warning", te.errOut.String())
	}
}

// TestOpenLogsCreatesStateOnlyWithTheLock removes state/, as a clone of
// a repository with no record lacks it. status, log and ls create
// nothing. gc holds the lock and creates state/, and no catalog/.
func TestOpenLogsCreatesStateOnlyWithTheLock(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	l := testLayout(t, repo)
	for _, dir := range []string{l.stateDir(), l.catalogDir()} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"status"}, {"log"}} {
		runCmd(t, append([]string{"--repo=" + repo}, args...)...)
		for _, dir := range []string{l.stateDir(), l.catalogDir()} {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("%s created %s: %v", args[0], dir, err)
			}
		}
	}
	if code, out := runCmd(t, "--repo="+repo, "gc"); code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if fi, err := os.Stat(l.stateDir()); err != nil || !fi.IsDir() {
		t.Fatalf("gc left no state directory: %v", err)
	}
	if _, err := os.Stat(l.catalogDir()); !os.IsNotExist(err) {
		t.Fatalf("gc created the catalog directory: %v", err)
	}
}
