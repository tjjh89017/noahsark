package restore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// treeDisc is one disc of an assembler test, backed by an unpacked
// NOAHSARK tree. holds decides which chunk ids this disc carries; a nil
// holds carries every one.
type treeDisc struct {
	root  string
	holds map[object.ID]bool
	reads int
}

func (d *treeDisc) Has(id object.ID) bool {
	return d.holds == nil || d.holds[id]
}

func (d *treeDisc) Read(id object.ID) ([]byte, error) {
	d.reads++
	return ReadChunkFromRoot(d.root, id)
}

// cacheOfTree builds the local cache of treeDir's one run, the cache an
// assembler reads every tree and blob from.
func cacheOfTree(t *testing.T, treeDir string) *cache.Cache {
	t.Helper()
	c, err := cache.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.WriteFromRoot(c, treeDir); err != nil {
		t.Fatal(err)
	}
	return c
}

// chunkIDs returns every chunk id the snapshot under c needs, in the
// blob order of the tree walk.
func chunkIDs(t *testing.T, c *cache.Cache, snap *format.Snapshot) []object.ID {
	t.Helper()
	var out []object.ID
	var walk func(id object.ID)
	walk = func(id object.ID) {
		tree, err := c.ReadTree(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range tree.Entries {
			switch e.EntryType {
			case format.EntryTypeDirectory:
				walk(object.ID(e.ContentID))
			case format.EntryTypeRegular:
				blob, err := c.ReadBlob(object.ID(e.ContentID))
				if err != nil {
					t.Fatal(err)
				}
				for _, be := range blob.Entries {
					out = append(out, object.ID(be.ContentID))
				}
			}
		}
	}
	walk(object.ID(snap.RootTree))
	return out
}

// TestAssemblerSpansTwoDiscs splits one snapshot's chunks between two
// discs and checks the restore writes every file from both, with the
// final name appearing only once a file is complete.
func TestAssemblerSpansTwoDiscs(t *testing.T) {
	srcDir := buildSpanFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	c := cacheOfTree(t, treeDir)
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatal(err)
	}

	ids := chunkIDs(t, c, snap)
	if len(ids) < 2 {
		t.Fatalf("the fixture has %d chunk(s), want at least 2", len(ids))
	}
	first := map[object.ID]bool{ids[0]: true}
	second := make(map[object.ID]bool)
	for _, id := range ids[1:] {
		if !first[id] {
			second[id] = true
		}
	}

	outDir := filepath.Join(t.TempDir(), "out")
	a, err := NewAssembler(c, snap, outDir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	d1 := &treeDisc{root: treeDir, holds: first}
	if err := a.Disc(d1, nil); err != nil {
		t.Fatal(err)
	}
	if d1.reads == 0 {
		t.Fatal("the first disc was never read")
	}
	if left := partsUnder(t, outDir); len(left) == 0 {
		t.Fatal("the first disc finished every file; the fixture does not span two discs")
	}

	d2 := &treeDisc{root: treeDir, holds: second}
	if err := a.Disc(d2, nil); err != nil {
		t.Fatal(err)
	}
	a.Finish()
	if rep := a.Report(); rep.Failed() {
		t.Fatalf("restore reported %s", rep.Summary())
	}
	if left := partsUnder(t, outDir); len(left) > 0 {
		t.Fatalf("part file(s) left after a complete restore: %v", left)
	}
	compareFileBytes(t, filepath.Join(outDir, srcDir, "cross.bin"), filepath.Join(srcDir, "cross.bin"))
	compareFileBytes(t, filepath.Join(outDir, srcDir, "small.txt"), filepath.Join(srcDir, "small.txt"))
}

// buildSpanFixtureSrc writes one file of several chunks, so a test can
// give its chunks to two different discs.
func buildSpanFixtureSrc(t *testing.T) string {
	t.Helper()
	srcDir := t.TempDir()
	writeStreamedRandomFile(t, filepath.Join(srcDir, "cross.bin"), 12<<20)
	if err := os.WriteFile(filepath.Join(srcDir, "small.txt"), []byte("small file content"), 0o644); err != nil {
		t.Fatal(err)
	}
	return srcDir
}

// partsUnder returns every part file below dir.
func partsUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == partSuffix {
			out = append(out, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

func compareFileBytes(t *testing.T, got, want string) {
	t.Helper()
	gotBytes, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBytes) != string(wantBytes) {
		t.Fatalf("%s does not hold %s's bytes", got, want)
	}
}

// TestPartNameAvoidsASnapshotName checks that a snapshot which holds a
// file of the plain part name pushes the restore onto a numbered one.
func TestPartNameAvoidsASnapshotName(t *testing.T) {
	taken := map[string]bool{"f.bin": true, ".f.bin" + partSuffix: true}
	got := partName("f.bin", taken)
	if got == ".f.bin"+partSuffix {
		t.Fatal("the part name collides with a name the snapshot owns")
	}
	if want := ".f.bin" + partSuffix + "2"; got != want {
		t.Fatalf("part name %q, want %q", got, want)
	}
	if plain := partName("f.bin", map[string]bool{"f.bin": true}); plain != ".f.bin"+partSuffix {
		t.Fatalf("part name %q, want the plain one", plain)
	}
}

// TestLinkPartFallsBackToRename drives the no-hard-link path through the
// linkFile seam: the part file must still become the final name.
func TestLinkPartFallsBackToRename(t *testing.T) {
	dir := t.TempDir()
	part := filepath.Join(dir, ".f.bin"+partSuffix)
	dest := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(part, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	noLinks(t)

	if err := linkPart(part, dest); err != nil {
		t.Fatalf("linkPart: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Fatalf("%s holds %q", dest, got)
	}
	if _, err := os.Lstat(part); !os.IsNotExist(err) {
		t.Fatalf("the part file is still there: %v", err)
	}
}

// TestLinkPartFallbackKeepsAnExistingName checks the fallback path still
// refuses a name that is already there.
func TestLinkPartFallbackKeepsAnExistingName(t *testing.T) {
	dir := t.TempDir()
	part := filepath.Join(dir, ".f.bin"+partSuffix)
	dest := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(part, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("somebody else"), 0o644); err != nil {
		t.Fatal(err)
	}
	noLinks(t)

	if err := linkPart(part, dest); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("linkPart: %v, want an already-exists error", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "somebody else" {
		t.Fatalf("%s was replaced: %q", dest, got)
	}
}

// noLinks makes os.Link report a filesystem with no hard link, for the
// rest of the test.
func noLinks(t *testing.T) {
	t.Helper()
	linkFile = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: syscall.EPERM}
	}
	t.Cleanup(func() { linkFile = os.Link })
}
