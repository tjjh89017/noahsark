package restore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/image"
)

// foldTreeToLower copies every file and directory under src into dst,
// folding every path segment to lowercase. This mirrors what plain ISO
// 9660 level 4, with no Rock Ridge, does to every name on a real burn:
// NOAHSARK becomes noahsark, RUN.bin becomes run.bin, and so on. Object
// names and fan-out directories are already lowercase, so folding them
// again changes nothing.
func foldTreeToLower(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := dst
		if rel != "." {
			for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
				target = filepath.Join(target, strings.ToLower(part))
			}
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestCaseFoldedTreeReadsListsAndRestores builds one run's tree, copies
// it with every fixed name folded to lowercase, and checks that
// image.Read, image.ListSnapshot and Restore all still work against the
// folded copy and agree with the original tree.
func TestCaseFoldedTreeReadsListsAndRestores(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)

	foldedDir := t.TempDir()
	foldTreeToLower(t, treeDir, foldedDir)

	wantRR, err := image.Read(treeDir)
	if err != nil {
		t.Fatalf("Read on the original tree: %v", err)
	}
	gotRR, err := image.Read(foldedDir)
	if err != nil {
		t.Fatalf("Read on the case-folded tree: %v", err)
	}
	if gotRR.ObjectsVerified != wantRR.ObjectsVerified || gotRR.RunCopies != wantRR.RunCopies {
		t.Fatalf("Read on the case-folded tree: got %+v, want %+v", gotRR, wantRR)
	}

	wantEntries, err := image.ListSnapshot(treeDir, snapID)
	if err != nil {
		t.Fatalf("ListSnapshot on the original tree: %v", err)
	}
	gotEntries, err := image.ListSnapshot(foldedDir, snapID)
	if err != nil {
		t.Fatalf("ListSnapshot on the case-folded tree: %v", err)
	}
	if !reflect.DeepEqual(image.SortedPaths(wantEntries), image.SortedPaths(gotEntries)) {
		t.Fatalf("ListSnapshot on the case-folded tree disagrees with the original tree")
	}

	outDir := t.TempDir()
	if err := Restore(foldedDir, snapID, outDir); err != nil {
		t.Fatalf("Restore on the case-folded tree: %v", err)
	}
	compareRestoredTree(t, srcDir, outDir)
}
