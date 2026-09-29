package restore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/image"
)

// skipAsRoot skips a test that needs a file that cannot be opened: root
// opens every file.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root opens a file of mode 000")
	}
}

// TestHealAFileThatCannotBeOpened removes every permission from a parity
// file and from a small stream file. Each block of such a file is an
// erasure, and the heal must still give a tree that passes the full
// check.
func TestHealAFileThatCannotBeOpened(t *testing.T) {
	skipAsRoot(t)
	_, treeDir, _ := buildFixtureTree(t, buildFixtureSrc(t))
	col := smallFileColumn(t, treeDir, 4)
	paths, _, layout := streamLayout(t, treeDir)
	idx, _, err := layout.Locate(col * layout.StripeCount())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{parityPath(t, treeDir, 3), paths[idx]} {
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	}

	outDir := filepath.Join(t.TempDir(), "healed")
	if _, err := Heal(treeDir, outDir); err != nil {
		t.Fatalf("Heal: %v", err)
	}
	if _, err := image.Read(outDir); err != nil {
		t.Fatalf("the healed tree fails the full check: %v", err)
	}
}

// TestHealADirectoryThatCannotBeListed removes every permission from the
// parity directory. The copy lacks each parity file, and the heal must
// make each of them again.
func TestHealADirectoryThatCannotBeListed(t *testing.T) {
	skipAsRoot(t)
	_, treeDir, _ := buildFixtureTree(t, buildFixtureSrc(t))
	dir := filepath.Dir(parityPath(t, treeDir, 0))
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	outDir := filepath.Join(t.TempDir(), "healed")
	if _, err := Heal(treeDir, outDir); err != nil {
		t.Fatalf("Heal: %v", err)
	}
	if _, err := image.Read(outDir); err != nil {
		t.Fatalf("the healed tree fails the full check: %v", err)
	}
}

// TestHealRefusesASymlinkInTheTree puts an object file outside the tree
// behind a symlink, with one damaged byte. A heal that follows the
// symlink repairs the file outside the tree. Heal must refuse the tree
// and leave the file outside unchanged, with --out and in place.
func TestHealRefusesASymlinkInTheTree(t *testing.T) {
	for _, inPlace := range []bool{false, true} {
		name := "into a new directory"
		if inPlace {
			name = "in place"
		}
		t.Run(name, func(t *testing.T) {
			_, treeDir, _ := buildFixtureTree(t, buildFixtureSrc(t))
			paths, sizes, layout := streamLayout(t, treeDir)
			corruptDataBlockAt(t, paths, sizes, layout, 8, 0)
			idx, _, err := layout.Locate(8 * layout.StripeCount())
			if err != nil {
				t.Fatal(err)
			}
			inTree := paths[idx]
			outside := filepath.Join(t.TempDir(), "outside")
			damaged, err := os.ReadFile(inTree)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(outside, damaged, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(inTree); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, inTree); err != nil {
				t.Fatal(err)
			}

			outDir := filepath.Join(t.TempDir(), "healed")
			if inPlace {
				outDir = ""
			}
			if _, err := Heal(treeDir, outDir); err == nil {
				t.Fatal("Heal of a tree with a symlink: want an error, got none")
			}
			got, err := os.ReadFile(outside)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, damaged) {
				t.Fatal("Heal wrote the file outside the tree through the symlink")
			}
		})
	}
}
