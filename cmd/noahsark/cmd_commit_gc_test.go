package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// stagingObjectsSnapshot reports the file count and total byte size of
// repo's staging/objects tree, so a test can check a later call added
// nothing there.
func stagingObjectsSnapshot(t *testing.T, repo string) (files int, bytes int64) {
	t.Helper()
	objectsDir := filepath.Join(repo, "staging", "objects")
	err := filepath.Walk(objectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files++
			bytes += info.Size()
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return files, bytes
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

// TestCommitAfterGCDoesNotRefillStaging checks the severe staging-refill
// bug: once an object's disc has been burned, verified and gc'd, a
// commit that references it again must not re-stage it. The object
// already lives on a disc gc considered CLEAN before freeing it; a
// re-commit finding the same content must count it existing and leave
// staging untouched.
func TestCommitAfterGCDoesNotRefillStaging(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeFixtureSource(t)

	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "staging.retain_after_clean = 0d")

	packAndVerifyDisc(t, work, repo, src)

	code, out := runCmd(t, "gc", "--repo="+repo)
	if code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if n := countByState(t, repo, stage.Staged); n != 0 {
		t.Fatalf("Staged objects after gc = %d, want 0", n)
	}

	filesBefore, bytesBefore := stagingObjectsSnapshot(t, repo)
	deletedBefore := countByState(t, repo, stage.Deleted)

	// A commit of the same, unchanged source must find every chunk,
	// blob and tree already on the disc gc just freed, and must not
	// write any of them back into staging.
	code, out = runCmd(t, "commit", "--repo="+repo, src)
	if code != 0 {
		t.Fatalf("re-commit: exit %d: %s", code, out)
	}
	// The snapshot object is always new, since its timestamp makes every
	// commit's snapshot id unique; every chunk, blob and tree beneath it
	// must count existing instead.
	if !strings.Contains(out, "new objects: 1, existing objects:") {
		t.Fatalf("re-commit output %q, want new objects: 1 (the snapshot only)", out)
	}

	filesAfter, bytesAfter := stagingObjectsSnapshot(t, repo)
	if filesAfter != filesBefore {
		t.Fatalf("staging/objects file count = %d, want unchanged %d; commit output: %s", filesAfter, filesBefore, out)
	}
	if bytesAfter != bytesBefore {
		t.Fatalf("staging/objects bytes = %d, want unchanged %d", bytesAfter, bytesBefore)
	}

	// Only the new snapshot object enters the state log Staged; every
	// chunk, blob and tree it references stays exactly where gc left
	// it, Deleted, not resurrected back to Staged.
	if n := countByState(t, repo, stage.Staged); n != 1 {
		t.Fatalf("Staged objects after re-commit = %d, want 1 (the new snapshot only)", n)
	}
	if n := countByState(t, repo, stage.Deleted); n != deletedBefore {
		t.Fatalf("Deleted objects after re-commit = %d, want unchanged %d", n, deletedBefore)
	}
}
