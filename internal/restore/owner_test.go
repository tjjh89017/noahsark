package restore

import (
	"bytes"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
)

// writeFakeIndex puts into c an INDEX for the disc uuid that lists ids.
// The chunks themselves stay in one NOAHSARK tree, which every disc of
// the test reads.
func writeFakeIndex(t *testing.T, c *catalog.Catalog, uuid [16]byte, ids []object.ID) {
	t.Helper()
	idx := format.Index{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic,
			MagicKind:    format.MagicIndex,
			VersionMajor: 1,
			HeaderLen:    format.IndexHeaderLen,
		},
		FileCount:   uint32(len(ids)),
		ObjectCount: uint32(len(ids)),
	}
	for _, id := range ids {
		idx.Files = append(idx.Files, format.IndexFileRecord{ByteLen: 1, Role: format.FileRoleObject})
		idx.Objects = append(idx.Objects, format.IndexObjectRecord{ContentID: id, Kind: format.ObjectKindChunk})
	}
	buf := make([]byte, idx.EncodedLen())
	if _, err := idx.Encode(buf); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteDisc(uuid, buf, nil, nil); err != nil {
		t.Fatal(err)
	}
}

// TestRestoreChunkOnTwoDiscs gives the chunks of one file to three discs
// whose INDEX tables overlap: disc 1 lists c0, disc 2 lists c0 to the
// chunk before the last one of cross.bin, disc 3 lists the rest. The
// restore must write every position of the file one time, give the
// final name only to the complete file, and report no problem.
func TestRestoreChunkOnTwoDiscs(t *testing.T) {
	srcDir := buildSpanFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	c := catalogOfTree(t, treeDir)
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatal(err)
	}
	ids := chunkIDs(t, c, snap)
	n := len(ids)
	if n < 4 {
		t.Fatalf("the fixture has %d chunk(s), want at least 4", n)
	}
	lists := [][]object.ID{{ids[0]}, ids[:n-2], ids[n-2:]}
	for i, l := range lists {
		writeFakeIndex(t, c, [16]byte{byte(i + 1)}, l)
	}

	sel, err := plan.Select(c, snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	var discs []plan.Disc
	for i := range lists {
		discs = append(discs, plan.Disc{DiscUUID: [16]byte{byte(i + 1)}, DiscSeq: uint64(i + 1)})
	}
	p, err := plan.New(c, sel, discs)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	outDir := filepath.Join(t.TempDir(), "out")
	if err := Scan(c, sel, outDir, false, p.Add); err != nil {
		t.Fatal(err)
	}
	if err := p.Count(); err != nil {
		t.Fatal(err)
	}
	a, err := NewAssembler(c, sel, outDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for i := range lists {
		if err := a.Disc(&planDisc{root: treeDir, uuid: [16]byte{byte(i + 1)}, p: p}, nil); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if _, err := os.Lstat(filepath.Join(outDir, "cross.bin")); err == nil {
				t.Fatal("cross.bin has its final name before its last chunk is written")
			}
		}
	}
	if err := a.Finish(); err != nil {
		t.Fatal(err)
	}
	if rep := a.Report(); rep.Failed() {
		t.Fatalf("restore reported %s", rep.Summary())
	}
	if left := partsUnder(t, outDir); len(left) > 0 {
		t.Fatalf("part file(s) left after a complete restore: %v", left)
	}
	compareFileBytes(t, filepath.Join(outDir, "cross.bin"), filepath.Join(srcDir, "cross.bin"))
	compareFileBytes(t, filepath.Join(outDir, "small.txt"), filepath.Join(srcDir, "small.txt"))
}

// TestRestoreNeverLinksWrongContent drives the assembler with two discs
// that both name c0, against the rule that one restore takes each chunk
// from one disc only. The check before the link must catch the file:
// no final name with wrong content, a reported problem, and a part file
// that the next run completes.
func TestRestoreNeverLinksWrongContent(t *testing.T) {
	srcDir := buildSpanFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	c := catalogOfTree(t, treeDir)
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatal(err)
	}
	ids := chunkIDs(t, c, snap)
	n := len(ids)
	if n < 4 {
		t.Fatalf("the fixture has %d chunk(s), want at least 4", n)
	}
	holds := []map[object.ID]bool{{ids[0]: true}, {}, {}}
	for _, id := range ids[:n-2] {
		holds[1][id] = true
	}
	for _, id := range ids[n-2:] {
		holds[2][id] = true
	}

	sel, err := plan.Select(c, snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "out")
	a, err := NewAssembler(c, sel, outDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, h := range holds {
		if err := a.Disc(&treeDisc{root: treeDir, holds: h}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Finish(); err != nil {
		t.Fatal(err)
	}
	cross := filepath.Join(outDir, "cross.bin")
	got, err := os.ReadFile(cross)
	if err == nil {
		want, err := os.ReadFile(filepath.Join(srcDir, "cross.bin"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("cross.bin has its final name with content that is not the content of the snapshot")
		}
	} else {
		if len(problemsOf(a.Report(), KindFile)) == 0 {
			t.Fatal("cross.bin is not restored, and restore reports no problem")
		}
		if len(partsUnder(t, outDir)) == 0 {
			t.Fatal("the part file of cross.bin does not stay")
		}
	}

	if rep, err := restoreTree(t, treeDir, snapID, outDir, false); err != nil || rep.Failed() {
		t.Fatalf("the next run: %v, %s", err, rep.Summary())
	}
	compareFileBytes(t, cross, filepath.Join(srcDir, "cross.bin"))
}

// buildRepeatFixtureSrc writes one file that holds the same block four
// times, so that its blob holds the same chunk at several positions.
func buildRepeatFixtureSrc(t *testing.T) string {
	t.Helper()
	srcDir := t.TempDir()
	block := make([]byte, 4<<20)
	rand.New(rand.NewSource(7)).Read(block)
	data := bytes.Repeat(block, 4)
	if err := os.WriteFile(filepath.Join(srcDir, "repeat.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return srcDir
}

// TestRestoreChunkAtTwoPositions restores a file that holds one chunk at
// several positions, from two discs, with a stop and a new run between
// the discs. Each position must be written, and the file must be
// complete only after the second disc.
func TestRestoreChunkAtTwoPositions(t *testing.T) {
	srcDir := buildRepeatFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	c := catalogOfTree(t, treeDir)
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatal(err)
	}
	ids := chunkIDs(t, c, snap)
	seen := make(map[object.ID]int)
	var repeated object.ID
	for _, id := range ids {
		seen[id]++
		if seen[id] == 2 {
			repeated = id
		}
	}
	if repeated == (object.ID{}) {
		t.Fatal("the fixture holds no chunk at two positions")
	}
	first := map[object.ID]bool{repeated: true}
	second := make(map[object.ID]bool)
	for _, id := range ids {
		if id != repeated {
			second[id] = true
		}
	}

	sel, err := plan.Select(c, snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "out")
	run := func(holds map[object.ID]bool) Report {
		t.Helper()
		a, err := NewAssembler(c, sel, outDir, false)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		if err := a.Disc(&treeDisc{root: treeDir, holds: holds}, nil); err != nil {
			t.Fatal(err)
		}
		if err := a.Finish(); err != nil {
			t.Fatal(err)
		}
		return a.Report()
	}
	run(first)
	if _, err := os.Lstat(filepath.Join(outDir, "repeat.bin")); err == nil {
		t.Fatal("repeat.bin has its final name after the first disc")
	}
	if rep := run(second); rep.Failed() {
		t.Fatalf("restore reported %s", rep.Summary())
	}
	compareFileBytes(t, filepath.Join(outDir, "repeat.bin"), filepath.Join(srcDir, "repeat.bin"))
}

// TestRestoreChecksContentOfExistingFile changes one byte of a restored
// file and keeps its size and mtime. The next run must not count the
// file as restored.
func TestRestoreChecksContentOfExistingFile(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	outDir := t.TempDir()
	if rep, err := restoreTree(t, treeDir, snapID, outDir, false); err != nil || rep.Failed() {
		t.Fatalf("the first run: %v, %s", err, rep.Summary())
	}
	path := filepath.Join(outDir, "sub", "big2.bin")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600|0o200); err != nil {
		t.Fatal(err)
	}
	flipByte(t, path, 1000)
	if err := os.Chtimes(path, time.Now(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}

	rep, err := restoreTree(t, treeDir, snapID, outDir, false)
	if err != nil {
		t.Fatal(err)
	}
	skipped := problemsOf(rep, KindExists)
	if len(skipped) != 1 || skipped[0].Path != path {
		t.Fatalf("skipped %v, want only %s", skipped, path)
	}
}

// TestRestoreRefusesBlobOfOtherSize damages the size of cross.bin in the
// catalog tree, so that the blob and the tree entry do not agree. The
// restore must not give the final name to a file of the wrong size.
func TestRestoreRefusesBlobOfOtherSize(t *testing.T) {
	srcDir := buildSpanFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	c := catalogOfTree(t, treeDir)
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatal(err)
	}
	var holder object.ID
	var walk func(id object.ID)
	walk = func(id object.ID) {
		tree, err := c.ReadTree(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range tree.Entries {
			switch {
			case string(e.Name) == "cross.bin":
				holder = id
			case e.EntryType == format.EntryTypeDirectory:
				walk(object.ID(e.ContentID))
			}
		}
	}
	walk(object.ID(snap.RootTree))
	path := c.MetaPath(format.ObjectKindTree, holder)
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	name := bytes.Index(buf, []byte("cross.bin"))
	if name < format.TreeEntryHeaderLen {
		t.Fatal("no tree holds cross.bin")
	}
	// The size field is the u64 at offset 8 of the entry header.
	buf[name-format.TreeEntryHeaderLen+8+2] ^= 0x01
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(t.TempDir(), "out")
	rep, err := restoreWith(t, c, treeDir, snapID, outDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(outDir, "cross.bin")); err == nil {
		t.Fatal("cross.bin has its final name, but its blob does not have the size of its tree entry")
	}
	if len(problemsOf(rep, KindFile)) != 1 {
		t.Fatalf("problems %v, want one file not restored", rep.Problems)
	}
}

// TestLaterWalkCreatesNoDirectory removes a directory that the first
// walk created. The next disc must stop with the changed destination
// error, and must not create the directory again.
func TestLaterWalkCreatesNoDirectory(t *testing.T) {
	srcDir := buildFixtureSrc(t)
	_, treeDir, snapID := buildFixtureTree(t, srcDir)
	c := catalogOfTree(t, treeDir)
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		t.Fatal(err)
	}
	ids := chunkIDs(t, c, snap)
	sel, err := plan.Select(c, snap, nil)
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "out")
	a, err := NewAssembler(c, sel, outDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Disc(&treeDisc{root: treeDir, holds: map[object.ID]bool{}}, nil); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(outDir, "sub")
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	rest := make(map[object.ID]bool)
	for _, id := range ids {
		rest[id] = true
	}
	if err := a.Disc(&treeDisc{root: treeDir, holds: rest}, nil); !errors.Is(err, errDestChanged) {
		t.Fatalf("the second disc: %v, want the changed destination error", err)
	}
	if _, err := os.Lstat(sub); err == nil {
		t.Fatal("a later walk created a directory")
	}
}
