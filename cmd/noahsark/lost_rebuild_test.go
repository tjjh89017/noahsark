package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// lostRebuild is a repository whose one disc was verified, freed by gc
// and then marked lost with the real commands.
type lostRebuild struct {
	work, repo, src string
	// snap1 is the snapshot of the first commit, the one that the lost
	// disc held.
	snap1 string
}

// newLostRebuild commits a source, packs it, verifies a counted mount of
// the disc root, frees the disc with gc, and marks the disc lost with
// --force-yes. The Lost event is newer than the first snapshot. It
// checks the output of disc lost and of status after it.
func newLostRebuild(t *testing.T) *lostRebuild {
	t.Helper()
	work := t.TempDir()
	lr := &lostRebuild{work: work, repo: filepath.Join(work, "repo"), src: writeFixtureSource(t)}
	if code, out := runIn(t, lr.repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	lr.snap1 = snapshotIDFromCommit(t, lr.mustRun(t, "commit", lr.src))
	packOut := lr.mustRun(t, "pack", "--capacity=64MiB")
	root := filepath.Join(work, "disc0")
	copyTree(t, packedTreeDir(t, lr.repo, packOut), root)
	if out := lr.mustRun(t, "verify", root); !strings.Contains(out, "\nburn recorded; verified\n") {
		t.Fatalf("verify output %q, want a counted verify", out)
	}
	if out := lr.mustRun(t, "gc"); !strings.HasPrefix(out, "gc: freed ") || strings.HasPrefix(out, gcFreedNone) {
		t.Fatalf("gc output %q, want freed items", out)
	}
	if got := discState(t, lr.repo, packedDiscUUID(t, packOut)).State; got != stage.DiscOnDiscOnly {
		t.Fatalf("disc state %s after gc, want on disc only", got)
	}

	setFakeNow(t, func() time.Time { return time.Now().Add(time.Hour) })
	out := lr.mustRun(t, "--force-yes", "disc", "lost", "0")
	staged, lost := countByState(t, lr.repo, stage.Staged), countByState(t, lr.repo, stage.Lost)
	if staged == 0 || lost == 0 || countByState(t, lr.repo, stage.OnDisc) != 0 {
		t.Fatalf("after disc lost: %d staged, %d lost items; want both, and no on-disc item", staged, lost)
	}
	wantLines(t, out, " marked lost; "+strconv.Itoa(staged)+" item(s) returned to staged; "+strconv.Itoa(lost)+" item(s) need a new commit\n")
	wantLines(t, lr.status(t, 0),
		"lost: "+strconv.Itoa(lost)+" items; only a lost disc holds them\n",
		"next: disc 0 is lost; a new commit stages what the source still holds; run:\n",
		"noahsark commit\n")
	return lr
}

func (lr *lostRebuild) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	code, out := runCmd(t, append([]string{"--repo=" + lr.repo}, args...)...)
	if code != 0 {
		t.Fatalf("%v: exit %d: %s", args, code, out)
	}
	return out
}

// status runs status and checks its exit code.
func (lr *lostRebuild) status(t *testing.T, want int) string {
	t.Helper()
	code, out := runCmd(t, "--repo="+lr.repo, "status")
	if code != want {
		t.Fatalf("status: exit %d, want %d: %s", code, want, out)
	}
	return out
}

// packNewDisc packs the staged items to a new disc, copies its disc root
// as a counted mount, and returns the disc root. Each staged item goes on
// the disc, the snapshot of the lost disc included.
func (lr *lostRebuild) packNewDisc(t *testing.T) string {
	t.Helper()
	packOut := lr.mustRun(t, "pack", "--capacity=64MiB")
	if n := countByState(t, lr.repo, stage.Staged); n != 0 {
		t.Fatalf("%d items staged after pack, want 0", n)
	}
	root := filepath.Join(lr.work, "disc1")
	copyTree(t, packedTreeDir(t, lr.repo, packOut), root)

	c, err := catalog.OpenReadOnly(lr.repo)
	if err != nil {
		t.Fatal(err)
	}
	u, err := decodeUUID(strings.ReplaceAll(packedDiscUUID(t, packOut), "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := c.IndexForDisc(u)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, row := range idx.Objects {
		listed = listed || object.ID(row.ContentID).TextForm() == lr.snap1
	}
	if !listed {
		t.Fatalf("the new disc does not hold snapshot %s", lr.snap1)
	}
	return root
}

// restore restores snapshot snap from the disc root at root into a new
// directory, checks the exit code, and returns the directory and the
// output.
func (lr *lostRebuild) restore(t *testing.T, root, snap string, want int) (string, string) {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "--repo="+lr.repo, "restore", "--disc="+root, snap, dest)
	if code != want {
		t.Fatalf("restore %s: exit %d, want %d: %s", snap, code, want, out)
	}
	return dest, out
}

// TestLostDiscRebuildFromSource runs the rebuild after disc lost of a
// freed disc: a commit of the unchanged source stages each lost chunk
// again, the lost line goes away, and the new disc alone restores the
// old and the new snapshot.
func TestLostDiscRebuildFromSource(t *testing.T) {
	lr := newLostRebuild(t)
	snap2 := snapshotIDFromCommit(t, lr.mustRun(t, "commit", lr.src))
	if n := countByState(t, lr.repo, stage.Lost); n != 0 {
		t.Fatalf("%d items stay lost after the commit, want 0", n)
	}
	out := lr.status(t, 0)
	for _, text := range []string{"lost: ", "next: disc 0 is lost"} {
		if strings.Contains(out, text) {
			t.Errorf("status after the commit holds %q: %s", text, out)
		}
	}

	root := lr.packNewDisc(t)
	for _, snap := range []string{lr.snap1, snap2} {
		dest, out := lr.restore(t, root, snap, 0)
		if strings.Contains(out, "(lost)") || strings.Contains(out, "warning:") {
			t.Errorf("restore of %s needs the lost disc: %s", snap, out)
		}
		compareTrees(t, dest, lr.src)
	}
}

// TestLostDiscRebuildWithoutAFile runs the rebuild when the source no
// longer holds one file: its chunk stays lost, status keeps the lost line
// after the commit, and the restore of the old snapshot from the new disc
// restores each other file, names the lost file and exits 1.
func TestLostDiscRebuildWithoutAFile(t *testing.T) {
	lr := newLostRebuild(t)
	gone := filepath.Join(lr.src, "a.txt")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	snap2 := snapshotIDFromCommit(t, lr.mustRun(t, "commit", lr.src))
	if n := countByState(t, lr.repo, stage.Lost); n != 1 {
		t.Fatalf("%d items stay lost after the commit, want 1: the chunk of a.txt", n)
	}
	out := lr.status(t, 0)
	wantLines(t, out, "lost: 1 items; only a lost disc holds them\n")
	if strings.Contains(out, "next: disc 0 is lost") {
		t.Errorf("status after the commit holds the commit block: %s", out)
	}

	root := lr.packNewDisc(t)
	wantLines(t, lr.status(t, 0), "lost: 1 items; only a lost disc holds them\n")

	dest, out := lr.restore(t, root, lr.snap1, 1)
	wantLines(t, out, " (lost)\n", "noahsark: restore: warning: "+filepath.Join(dest, "a.txt")+": file not restored: ")
	if _, err := os.Lstat(filepath.Join(dest, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("restore wrote a.txt, which only the lost disc held: %v", err)
	}
	wantB, err := os.ReadFile(filepath.Join(lr.src, "sub", "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if gotB, err := os.ReadFile(filepath.Join(dest, "sub", "b.txt")); err != nil || !bytes.Equal(gotB, wantB) {
		t.Errorf("restored sub/b.txt = %q, %v; want the source bytes", gotB, err)
	}

	dest, _ = lr.restore(t, root, snap2, 0)
	compareTrees(t, dest, lr.src)
}
