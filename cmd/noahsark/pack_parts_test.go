package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// partsCapacity is the capacity of each disc of the tests of a snapshot
// that is packed in parts. writeSeededSource(t, 42, 6) needs three discs
// of this capacity.
var partsCapacity = packSectors(6_000_000)

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

// packPart packs one disc of partsCapacity into dir. It returns
// the disc uuid.
func packPart(t *testing.T, repo, dir string) string {
	t.Helper()
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity="+partsCapacity, "--out="+dir)
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
	// The first snapshot id sorts in the upper half, thus many ids sort
	// before it.
	firstAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.Local)
	msg, firstID := snapshotMessageFor(t, filepath.Join(work, "probe1"), src, firstAt, func(id object.ID) bool {
		return id[0] >= 0x80
	})
	if got := commitAt(t, repo, firstAt, "-m", msg, src); got != firstID {
		t.Fatalf("first snapshot %s, want the predicted %s", got.TextForm(), firstID.TextForm())
	}
	first := firstID.TextForm()

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
	code, out := runCmd(t, "--repo="+repo, "gc")
	if code != 0 || strings.HasPrefix(out, "gc: freed 0 item(s)") {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if after := stagedChunkFiles(t, repo); !slices.Equal(after, before) {
		t.Fatalf("gc changed the chunk files of the Staged items:\nbefore %q\nafter  %q", before, after)
	}
	rest := openTestLog(t, repo).IDsInState(stage.Staged)

	// The second snapshot gets an id that sorts before the id of the
	// first snapshot, although it is newer. The message and the clock
	// select that id.
	second := writeSeededSource(t, 43, 6)
	secondAt := firstAt.Add(time.Hour)
	msg, secondID := snapshotMessageFor(t, filepath.Join(work, "probe2"), second, secondAt, func(id object.ID) bool {
		return bytes.Compare(id[:], firstID[:]) < 0
	})
	if got := commitAt(t, repo, secondAt, "-m", msg, second); got != secondID {
		t.Fatalf("second snapshot %s, want the predicted %s", got.TextForm(), secondID.TextForm())
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

// commitAt commits args into repo with the clock fixed at at, and
// returns the snapshot id. The clock is the real clock again after the
// commit.
func commitAt(t *testing.T, repo string, at time.Time, args ...string) object.ID {
	t.Helper()
	old := fakeNow
	fakeNow = func() time.Time { return at }
	code, out := runCmd(t, append([]string{"--repo=" + repo, "commit"}, args...)...)
	fakeNow = old
	if code != 0 {
		t.Fatalf("commit %q: exit %d: %s", args, code, out)
	}
	id, err := object.ParseID(snapshotIDFromCommit(t, out))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// snapshotMessageFor commits src at the time at into a probe repository
// in dir, and takes the snapshot object of that commit as a template.
// The root tree of a source that does not change is the same in every
// repository. It returns the first message "try N" whose snapshot of src
// at the time at has an id that accept accepts, and that id. A commit of
// src with that message at that time gives that id.
func snapshotMessageFor(t *testing.T, dir, src string, at time.Time, accept func(object.ID) bool) (string, object.ID) {
	t.Helper()
	if code, out := runIn(t, dir, "init"); code != 0 {
		t.Fatalf("init %s: exit %d: %s", dir, code, out)
	}
	probe := commitAt(t, dir, at, src)
	c, err := catalog.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, payload, err := object.ReadVerified(c.MetaPath(format.ObjectKindSnapshot, probe), probe)
	if err != nil {
		t.Fatal(err)
	}
	var tmpl format.Snapshot
	if _, err := tmpl.Decode(append(raw[:format.CommonHeaderLen+format.ObjectHeaderLen:format.CommonHeaderLen+format.ObjectHeaderLen], payload...)); err != nil {
		t.Fatal(err)
	}
	for i := range 1 << 16 {
		msg := fmt.Sprintf("try %d", i)
		s := tmpl
		s.Meta = []format.SnapshotMeta{{Tag: format.SnapshotMetaMessage, Value: []byte(msg)}}
		s.MetaCount = 1
		buf := make([]byte, s.EncodedLen())
		if _, err := s.Encode(buf); err != nil {
			t.Fatal(err)
		}
		id := object.ComputeID(format.ObjectKindSnapshot, buf[format.CommonHeaderLen+format.ObjectHeaderLen:])
		if accept(id) {
			return msg, id
		}
	}
	t.Fatal("no message gives an accepted snapshot id")
	return "", object.ID{}
}

// lostPartLineRe matches the status line of a snapshot whose snapshot
// object a disc holds while pack still takes Staged items with it.
var lostPartLineRe = regexp.MustCompile(`^snapshot ([0-9a-f]{12}): (\d+) items staged, not complete on discs; the discs alone cannot restore all of it$`)

// packThreeParts commits a source of six files into a new repository in
// work and packs it in three parts on the discs disc0, disc1 and disc2 of
// work. The trees and the snapshot object go on disc2. It returns the
// repository, the source, the snapshot id and the three disc roots.
func packThreeParts(t *testing.T, work string) (repo, src, snapID string, roots []string) {
	t.Helper()
	repo = filepath.Join(work, "repo")
	src = writeSeededSource(t, 42, 6)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	snapID = snapshotIDFromCommit(t, out)
	for i := range 3 {
		roots = append(roots, filepath.Join(work, fmt.Sprintf("disc%d", i)))
		packPart(t, repo, roots[i])
	}
	if n := countByState(t, repo, stage.Staged); n != 0 {
		t.Fatalf("%d items staged after three packs, want 0", n)
	}
	return repo, src, snapID, roots
}

// packRestagedAndRestore checks a repository whose Staged items belong
// to the snapshot snapID only through the trees and blobs of other
// discs. status names the snapshot with the count of these items, the
// next pack takes each of them, and status then names it no more. The
// test deletes the repository, recovers it from the discs of kept and
// the new disc, restores snapID and compares it with src. other is the
// count of the Staged items that go with another snapshot.
func packRestagedAndRestore(t *testing.T, work, repo, src, snapID string, kept []string, other int) {
	t.Helper()
	staged := countByState(t, repo, stage.Staged)
	if staged <= other {
		t.Fatal("no item is staged again; the fixture must return items to staged")
	}
	line := snapshotLineOf(t, repo, snapID)
	if m := lostPartLineRe.FindStringSubmatch(line); m == nil || m[2] != fmt.Sprint(staged-other) {
		t.Fatalf("status line of %s: %q, want the lost-part line with %d items", snapID, line, staged-other)
	}

	newDisc := filepath.Join(work, "disc-new")
	code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=64MiB", "--out="+newDisc)
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	if !strings.Contains(out, fmt.Sprintf(": %d item(s), ", staged)) {
		t.Fatalf("pack output %q, want the %d staged items on the new disc", out, staged)
	}
	if n := countByState(t, repo, stage.Staged); n != 0 {
		t.Fatalf("%d items staged after the pack, want 0", n)
	}
	if line := snapshotLineOf(t, repo, snapID); line != "" {
		t.Fatalf("status still names the snapshot: %q", line)
	}

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	discs := append(slices.Clone(kept), newDisc)
	for _, root := range discs {
		if code, out := recoverDisc(t, repo, src, root); code != 0 && !strings.Contains(out, "not yet given") {
			t.Fatalf("recover %s: exit %d: %s", root, code, out)
		}
	}
	dest := filepath.Join(work, "restored")
	if code, out := restoreFromDiscs(t, repo, snapID, dest, discs...); code != 0 {
		t.Fatalf("restore: exit %d: %s", code, out)
	}
	compareTrees(t, dest, src)
}

// TestPackTakesTheItemsOfALostDisc packs a snapshot in three parts and
// marks the first disc lost while it is burned. Its items return to
// staged, below trees and a snapshot object of the third disc. The next
// pack must take them, and the discs without the lost one must restore
// the snapshot.
func TestPackTakesTheItemsOfALostDisc(t *testing.T) {
	work := t.TempDir()
	repo, src, snapID, roots := packThreeParts(t, work)
	if code, out := runCmd(t, "--repo="+repo, "disc", "burned", "0"); code != 0 {
		t.Fatalf("disc burned: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "--force-yes", "disc", "lost", "0"); code != 0 {
		t.Fatalf("disc lost: exit %d: %s", code, out)
	}
	packRestagedAndRestore(t, work, repo, src, snapID, roots[1:], 0)
}

// TestPackTakesTheItemsOfALostDiscAfterGC packs a snapshot in three
// parts, verifies the discs, lets gc free them, and marks the first disc
// lost. Its items are lost. A commit of the same source stages them
// again. The next pack must take them, and the discs without the lost
// one must restore the first snapshot.
func TestPackTakesTheItemsOfALostDiscAfterGC(t *testing.T) {
	work := t.TempDir()
	repo, src, snapID, roots := packThreeParts(t, work)
	for i, root := range roots {
		mounted := filepath.Join(work, fmt.Sprintf("mounted%d", i))
		copyTree(t, root, mounted)
		if code, out := runCmd(t, "--repo="+repo, "verify", mounted); code != 0 {
			t.Fatalf("verify disc %d: exit %d: %s", i, code, out)
		}
	}
	if code, out := runCmd(t, "--repo="+repo, "gc"); code != 0 {
		t.Fatalf("gc: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "--force-yes", "disc", "lost", "0"); code != 0 {
		t.Fatalf("disc lost: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", src)
	if code != 0 {
		t.Fatalf("commit again: exit %d: %s", code, out)
	}
	second := snapshotIDFromCommit(t, out)
	// The second snapshot reaches the same root tree, which a disc holds,
	// so the items that the commit staged again go with the older first
	// snapshot. The second snapshot takes only its own snapshot object.
	if line := snapshotLineOf(t, repo, second); !strings.Contains(line, ": 1 items staged, ") {
		t.Fatalf("status line of the second snapshot: %q, want 1 item", line)
	}
	packRestagedAndRestore(t, work, repo, src, snapID, roots[1:], 1)
}

// TestPackOrderFollowsSnapshotTime commits an older snapshot that shares
// nothing with a disc, and a newer one that shares a file with a disc.
// pack takes the older snapshot first: an item on a disc gives a newer
// snapshot no place before it.
func TestPackOrderFollowsSnapshotTime(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	srcA := writeSeededSource(t, 42, 2)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", srcA); code != 0 {
		t.Fatalf("commit A: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "pack", "--capacity=bd25", "--out="+filepath.Join(work, "d0")); code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", "--ref=b", writeSeededSource(t, 43, 6))
	if code != 0 {
		t.Fatalf("commit B: exit %d: %s", code, out)
	}
	idB := snapshotIDFromCommit(t, out)
	extra := writeSeededSource(t, 44, 1)
	if err := os.Rename(filepath.Join(extra, "sub0"), filepath.Join(srcA, "new")); err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "--repo="+repo, "commit", "--ref=a2", srcA)
	if code != 0 {
		t.Fatalf("commit A2: exit %d: %s", code, out)
	}
	idA2 := snapshotIDFromCommit(t, out)

	var named []string
	for _, line := range statusLines(t, repo) {
		if m := statusSnapshotLineRe.FindStringSubmatch(line); m != nil {
			named = append(named, m[1])
		}
	}
	if want := []string{idB[4:16], idA2[4:16]}; !slices.Equal(named, want) {
		t.Fatalf("status names the snapshots %v, want %v: the older snapshot B first", named, want)
	}

	// A disc that holds only a part of B takes no item of A2.
	countOf := func(id string) string {
		m := statusSnapshotLineRe.FindStringSubmatch(snapshotLineOf(t, repo, id))
		if m == nil {
			return ""
		}
		return m[2]
	}
	beforeB, beforeA2 := countOf(idB), countOf(idA2)
	root := filepath.Join(work, "d1")
	code, out = runCmd(t, "--repo="+repo, "pack", "--capacity="+partsCapacity, "--out="+root)
	if code != 0 {
		t.Fatalf("pack: exit %d: %s", code, out)
	}
	afterB, afterA2 := countOf(idB), countOf(idA2)
	if afterB == "" || afterB == beforeB || afterA2 != beforeA2 {
		t.Fatalf("staged counts of B %s -> %s, of A2 %s -> %s; want a part of B only", beforeB, afterB, beforeA2, afterA2)
	}
	rr, err := image.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := object.ParseID(idA2)
	if slices.ContainsFunc(rr.Index.Objects, func(row format.IndexObjectRecord) bool { return object.ID(row.ContentID) == a2 }) {
		t.Fatal("the disc holds the snapshot object of the newer snapshot A2")
	}
}

// TestPackAndStatusGoOnPastADamagedSnapshot damages a staged tree of a
// snapshot whose source is gone, then commits a second snapshot that
// shares nothing with it. status prints its lines and a warning, and
// exits 1. pack warns about the damaged snapshot, packs the second
// snapshot, and exits 1.
func TestPackAndStatusGoOnPastADamagedSnapshot(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	srcA := writeSeededSource(t, 42, 2)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", srcA)
	if code != 0 {
		t.Fatalf("commit A: exit %d: %s", code, out)
	}
	idA := snapshotIDFromCommit(t, out)
	damaged := truncateOneStagedTree(t, repo)
	if err := os.RemoveAll(srcA); err != nil {
		t.Fatal(err)
	}
	code, out = runCmd(t, "--repo="+repo, "commit", writeSeededSource(t, 43, 2))
	if code != 0 {
		t.Fatalf("commit B: exit %d: %s", code, out)
	}
	idB := snapshotIDFromCommit(t, out)
	warning := fmt.Sprintf("warning: snapshot %s: cannot pack all of it: tree %s is damaged; commit the same source again", idA[4:16], damaged)

	te := newTestEnv(t.TempDir())
	code, _ = te.run("--repo="+repo, "status")
	if code != 1 || !strings.Contains(te.errOut.String(), "noahsark: status: "+warning) {
		t.Fatalf("status: exit %d, stderr %q, want 1 and the warning %q", code, te.errOut.String(), warning)
	}
	for _, id := range []string{idA, idB} {
		if !strings.Contains(te.out.String(), "snapshot "+id[4:16]+": ") {
			t.Fatalf("status stdout %q, want the line of snapshot %s", te.out.String(), id[4:16])
		}
	}

	code, _ = te.run("--repo="+repo, "pack", "--capacity=bd25", "--out="+filepath.Join(work, "d0"))
	if code != 1 || !strings.Contains(te.errOut.String(), "noahsark: pack: "+warning) || !strings.Contains(te.out.String(), "packed disc 0 ") {
		t.Fatalf("pack: exit %d, stdout %q, stderr %q, want 1, the packed disc and the warning", code, te.out.String(), te.errOut.String())
	}
	b, _ := object.ParseID(idB)
	if rec, _ := openTestLog(t, repo).Get(b); rec.State != stage.Packed {
		t.Fatalf("snapshot B is %v after the pack, want Packed", rec.State)
	}
	a, _ := object.ParseID(idA)
	if rec, _ := openTestLog(t, repo).Get(a); rec.State != stage.Staged {
		t.Fatalf("snapshot A is %v after the pack, want Staged", rec.State)
	}
}
