package restore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
)

// catalogCheckSource writes a small source tree: one file below the
// source root.
func catalogCheckSource(t *testing.T) string {
	t.Helper()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("content of a"), 0o644); err != nil {
		t.Fatal(err)
	}
	return srcDir
}

// restoreOnce restores snap from the disc tree treeDir with the catalog
// c into a new directory. It returns the directory and whether the
// restore succeeded: no error and no failed file.
func restoreOnce(t *testing.T, c *catalog.Catalog, snap *format.Snapshot, treeDir string) (string, bool) {
	t.Helper()
	outDir := filepath.Join(t.TempDir(), "out")
	sel, err := plan.Select(c, snap, nil)
	if err != nil {
		return outDir, false
	}
	a, err := NewAssembler(c, sel, outDir, false)
	if err != nil {
		return outDir, false
	}
	defer a.Close()
	if err := a.Disc(&treeDisc{root: treeDir}, nil); err != nil {
		return outDir, false
	}
	if err := a.Finish(); err != nil {
		return outDir, false
	}
	rep := a.Report()
	return outDir, !rep.Failed()
}

// sourceTree returns the id and the tree of the one source root of snap.
func sourceTree(t *testing.T, c *catalog.Catalog, snap *format.Snapshot) (*format.Tree, object.ID, *format.Tree) {
	t.Helper()
	root, err := c.ReadTree(object.ID(snap.RootTree))
	if err != nil {
		t.Fatal(err)
	}
	id := object.ID(root.Entries[0].ContentID)
	top, err := c.ReadTree(id)
	if err != nil {
		t.Fatal(err)
	}
	return root, id, top
}

// TestRestoreRefusesADamagedCatalogTree flips one bit of the size of a
// tree entry in the catalog. The file keeps its size. restore must not
// report success. A second copy of the disc repairs the catalog, and
// restore then writes the file as it was.
func TestRestoreRefusesADamagedCatalogTree(t *testing.T) {
	srcDir := catalogCheckSource(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	c := catalogOfTree(t, treeDir)
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatal(err)
	}
	_, topID, _ := sourceTree(t, c, snap)

	path := c.MetaPath(format.ObjectKindTree, topID)
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(buf, []byte("a.txt"))
	if i < format.TreeEntryHeaderLen {
		t.Fatal("the tree does not hold a.txt")
	}
	// The size field of the entry is at offset 8 of the entry header.
	buf[i-format.TreeEntryHeaderLen+8+2] ^= 0x01
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := restoreOnce(t, c, snap, treeDir); ok {
		t.Fatal("restore with a damaged catalog tree reports success")
	}

	if _, err := catalog.WriteFromRoot(c, treeDir); err != nil {
		t.Fatal(err)
	}
	outDir, ok := restoreOnce(t, c, snap, treeDir)
	if !ok {
		t.Fatal("restore after the repair failed")
	}
	got, _ := os.ReadFile(filepath.Join(outDir, "a.txt"))
	if string(got) != "content of a" {
		t.Fatalf("a.txt = %q after the repair", got)
	}
}

// TestRestoreChecksTheFileSize writes a tree whose entry gives a size
// that is not the sum of the chunk lengths of its blob. The tree and
// its snapshot give their ids. restore fails that file.
func TestRestoreChecksTheFileSize(t *testing.T) {
	srcDir := catalogCheckSource(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	c := catalogOfTree(t, treeDir)
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatal(err)
	}
	root, _, top := sourceTree(t, c, snap)
	for i := range top.Entries {
		if string(top.Entries[i].Name) == "a.txt" {
			top.Entries[i].Size++
		}
	}
	root.Entries[0].ContentID = writeTreeObject(t, c, top)
	snap.RootTree = writeTreeObject(t, c, root)

	outDir, ok := restoreOnce(t, c, snap, treeDir)
	if ok {
		t.Fatal("restore of a file whose size does not match its blob reports success")
	}
	if _, err := os.Stat(filepath.Join(outDir, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("restore wrote a.txt: %v", err)
	}
}

// writeTreeObject encodes tr, writes it into c, and returns its id.
func writeTreeObject(t *testing.T, c *catalog.Catalog, tr *format.Tree) object.ID {
	t.Helper()
	buf := make([]byte, tr.EncodedLen())
	if _, err := tr.Encode(buf); err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID(format.ObjectKindTree, buf[format.CommonHeaderLen+format.ObjectHeaderLen:])
	if err := c.WriteObject(format.ObjectKindTree, id, buf); err != nil {
		t.Fatal(err)
	}
	return id
}
