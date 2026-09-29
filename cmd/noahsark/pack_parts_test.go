package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// partsCapacity is the capacity of each disc of the tests of a snapshot
// that is packed in parts. writeSeededSource(t, 42, 6) needs three discs
// of this capacity.
var partsCapacity = packSectors(7_000_000)

// writeSeededSource creates a source of files subdirectories, each with
// one file of 600,000 pseudo-random bytes from seed. Two seeds give two
// sources that share no chunk.
func writeSeededSource(t *testing.T, seed int64, files int) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src")
	rng := rand.New(rand.NewSource(seed))
	for i := range files {
		dir := filepath.Join(src, fmt.Sprintf("sub%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 600_000)
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.bin"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

// packPart packs one disc of partsCapacity with FEC into dir. It returns
// the disc uuid.
func packPart(t *testing.T, repo, dir string) string {
	t.Helper()
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+partsCapacity, "--fec", "--out="+dir)
	if code != 0 {
		t.Fatalf("pack %s: exit %d: %s", dir, code, out)
	}
	return packedDiscUUID(t, out)
}

// stagedChunkFiles returns the chunk file of each Staged item of repo
// that has a chunk file, sorted.
func stagedChunkFiles(t *testing.T, repo string) []string {
	t.Helper()
	layout := testLayout(t, repo)
	var files []string
	for _, id := range openTestLog(t, repo).IDsInState(stage.Staged) {
		path := layout.chunkFile(id)
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		}
	}
	slices.Sort(files)
	return files
}

// snapshotLineOf returns the status line that names the snapshot id, or
// "" when status names it on no line.
func snapshotLineOf(t *testing.T, repo, id string) string {
	t.Helper()
	parsed, err := object.ParseID(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range statusLines(t, repo) {
		if strings.HasPrefix(line, "snapshot "+shortID(parsed)+": ") {
			return line
		}
	}
	return ""
}

// TestSnapshotPackedInPartsReachesTheDiscs packs a snapshot in parts
// across three discs, with a newer snapshot committed before the third
// pack. status names the snapshot while its rest is staged. gc frees no
// chunk file of a Staged item. The third disc holds the rest and the
// snapshot object. recover from the three discs alone then finds the
// snapshot, and restore gives back the source.
func TestSnapshotPackedInPartsReachesTheDiscs(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	src := writeSeededSource(t, 42, 6)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	first := snapshotIDFromCommit(t, out)

	discRoots := make([]string, 3)
	for i := range 2 {
		discRoots[i] = filepath.Join(work, fmt.Sprintf("disc%d", i))
		packPart(t, repo, discRoots[i])
	}
	line := snapshotLineOf(t, repo, first)
	if line == "" {
		t.Fatalf("status names no snapshot line for %s after two packs: %q", first, statusLines(t, repo))
	}
	if want := fmt.Sprintf(": %d items staged, ", countByState(t, repo, stage.Staged)); !strings.Contains(line, want) {
		t.Fatalf("snapshot line %q, want the count %q", line, want)
	}

	for i := range 2 {
		mounted := filepath.Join(work, fmt.Sprintf("mounted%d", i))
		copyTree(t, discRoots[i], mounted)
		if code, out := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
			t.Fatalf("verify disc %d: exit %d: %s", i, code, out)
		}
	}
	before := stagedChunkFiles(t, repo)
	if len(before) == 0 {
		t.Fatal("no staged chunk file after two packs; the capacity must leave a rest")
	}
	code, out = runCmd(t, "--repo="+repo, "gc", "--force-after=0d")
	if code != 0 || strings.HasPrefix(out, "gc: freed 0 item(s)") {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if after := stagedChunkFiles(t, repo); !slices.Equal(after, before) {
		t.Fatalf("gc changed the chunk files of the Staged items:\nbefore %q\nafter  %q", before, after)
	}
	rest := openTestLog(t, repo).IDsInState(stage.Staged)

	// The second snapshot gets an id that sorts before the id of the
	// first snapshot. A new message gives a new snapshot of the same
	// data.
	second := writeSeededSource(t, 43, 6)
	firstID, _ := object.ParseID(first)
	for i := 0; ; i++ {
		code, out = runCmd(t, "--repo="+repo, "commit", "-m", fmt.Sprintf("try %d", i), second)
		if code != 0 {
			t.Fatalf("commit of the second source: exit %d: %s", code, out)
		}
		id, _ := object.ParseID(snapshotIDFromCommit(t, out))
		if string(id[:]) < string(firstID[:]) {
			break
		}
		if i == 32 {
			t.Fatal("no second snapshot id sorts before the first snapshot id")
		}
	}

	discRoots[2] = filepath.Join(work, "disc2")
	packPart(t, repo, discRoots[2])
	rr, err := image.Read(discRoots[2])
	if err != nil {
		t.Fatal(err)
	}
	onThird := map[object.ID]bool{}
	for _, row := range rr.Index.Objects {
		onThird[object.ID(row.ContentID)] = true
	}
	for _, id := range rest {
		if !onThird[id] {
			t.Fatalf("the third disc does not hold %s of the rest of the first snapshot", id.TextForm())
		}
	}
	if !onThird[firstID] {
		t.Fatal("the third disc does not hold the snapshot object of the first snapshot")
	}
	if line := snapshotLineOf(t, repo, first); line != "" {
		t.Fatalf("status names the first snapshot after its rest is packed: %q", line)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if code, out := recoverDisc(t, repo, src, discRoots[i]); code != 0 {
			t.Fatalf("recover disc %d: exit %d: %s", i, code, out)
		}
	}
	dest := filepath.Join(work, "restored")
	if code, out := restoreFromDiscs(t, repo, first, dest, discRoots[0], discRoots[1], discRoots[2]); code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	compareTrees(t, dest, src)
}
