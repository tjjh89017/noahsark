package image

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// commitAt commits a source of files pseudo-random files of fileSize
// bytes each into stagingDir, with the snapshot time at. The content
// depends on name, thus two names give two snapshots that share no
// chunk. It marks every object of the snapshot Staged in l.
func commitAt(t *testing.T, stagingDir string, l *stage.Log, name string, at time.Time, files, fileSize int) object.ID {
	t.Helper()
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	rng := rand.New(rand.NewSource(int64(h.Sum64())))
	srcDir := t.TempDir()
	for i := range files {
		dir := filepath.Join(srcDir, fmt.Sprintf("sub%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, fileSize)
		if _, err := rng.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.bin"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := testWriter(stagingDir)
	w.Now = func() time.Time { return at }
	w.Profile = packFixtureChunkProfile
	id, _, err := w.Commit(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	markStagedFromCommit(t, stagingDir, id, l)
	return id
}

// reachableSet returns the id of every object that snapID reaches, the
// snapshot object included.
func reachableSet(t *testing.T, stagingDir string, snapID object.ID) map[object.ID]bool {
	t.Helper()
	objs, err := CollectReachable(testObjectPath(stagingDir), []object.ID{snapID})
	if err != nil {
		t.Fatal(err)
	}
	set := make(map[object.ID]bool, len(objs))
	for _, o := range objs {
		set[o.ID] = true
	}
	return set
}

// testOnDisc reports whether the state log l holds id on a disc.
func testOnDisc(l *stage.Log) func(object.ID) bool {
	return func(id object.ID) bool {
		rec, ok := l.Get(id)
		return ok && rec.State.OnDisc()
	}
}

// testCandidateOrder returns the pack order of every snapshot of the test
// repository stagingDir, as Pack builds it.
func testCandidateOrder(t *testing.T, stagingDir string, l *stage.Log) []packUnit {
	t.Helper()
	ids, err := testSnapshotIDs(stagingDir)()
	if err != nil {
		t.Fatal(err)
	}
	walk, err := packWalkOrder(testObjectPath(stagingDir), ids, testOnDisc(l))
	if err != nil {
		t.Fatal(err)
	}
	order, _, err := buildPackOrder(testObjectPath(stagingDir), walk, testOnDisc(l))
	if err != nil {
		t.Fatal(err)
	}
	return order
}

// unitIndex returns the index of id in order, or -1.
func unitIndex(order []packUnit, id object.ID) int {
	for i, u := range order {
		if u.ID == id {
			return i
		}
	}
	return -1
}

// TestPackTakesTheRestOfAnOldSnapshotFirst packs a part of an old
// snapshot, then commits a newer snapshot whose id sorts before the old
// one. The rest of the old snapshot, and its snapshot object, must come
// before each object of the new snapshot.
func TestPackTakesTheRestOfAnOldSnapshotFirst(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	t1 := fixedClock()
	old := commitAt(t, stagingDir, l, "old", t1, 8, 1_000_000)

	opts := packOpts(stagingDir, old, t.TempDir(), sectorsFor(7_000_000), 1, l)
	opts.FECEnabled = false
	if _, err := Pack(opts); err != nil {
		t.Fatalf("pack: %v", err)
	}
	if rec, _ := l.Get(old); rec.State != stage.Staged {
		t.Fatalf("the old snapshot object is %v after the first pack, want Staged: the capacity must hold a part only", rec.State)
	}

	var newer object.ID
	for i := range 64 {
		id := commitAt(t, stagingDir, l, fmt.Sprintf("new-%d", i), t1.Add(time.Hour), 2, 100_000)
		if lessBytes(id[:], old[:]) {
			newer = id
			break
		}
	}
	if newer == (object.ID{}) {
		t.Fatal("no new snapshot id sorts before the old snapshot id")
	}

	staged, err := StagedSnapshots(testObjectPath(stagingDir), mustSnapshotIDs(t, stagingDir), testOnDisc(l))
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) == 0 || staged[0].ID != old || !staged[0].PackedInParts {
		t.Fatalf("StagedSnapshots()[0] = %+v, want the old snapshot, packed in parts", staged[0])
	}
	oldReach := reachableSet(t, stagingDir, old)
	wantStaged := 0
	for id := range oldReach {
		if rec, _ := l.Get(id); rec.State == stage.Staged {
			wantStaged++
		}
	}
	if staged[0].StagedItems != wantStaged {
		t.Fatalf("StagedItems = %d, want %d", staged[0].StagedItems, wantStaged)
	}
	for _, s := range staged[1:] {
		if s.PackedInParts {
			t.Fatalf("snapshot %s is packed in parts, want no part on a disc", s.ID.TextForm())
		}
	}

	order := testCandidateOrder(t, stagingDir, l)
	oldAt := unitIndex(order, old)
	if oldAt < 0 {
		t.Fatal("the old snapshot object is not in the pack order")
	}
	for i, u := range order[:oldAt] {
		if !oldReach[u.ID] {
			t.Fatalf("unit %d (%s) of another snapshot goes before the old snapshot object at %d", i, u.ID.TextForm(), oldAt)
		}
	}
	if newAt := unitIndex(order, newer); newAt < oldAt {
		t.Fatalf("the new snapshot object is at %d, before the old snapshot object at %d", newAt, oldAt)
	}
}

// TestStagedSnapshotsGoInTimeOrder commits two snapshots whose id order
// is the reverse of their time order, with no item on a disc. The older
// snapshot goes first. Two snapshots with the same time go in id order.
func TestStagedSnapshotsGoInTimeOrder(t *testing.T) {
	stagingDir := t.TempDir()
	l, err := stage.Open(stagingDir)
	if err != nil {
		t.Fatal(err)
	}
	t1 := fixedClock()
	var older, newer object.ID
	for i := range 64 {
		a := commitAt(t, stagingDir, l, fmt.Sprintf("older-%d", i), t1, 1, 50_000)
		b := commitAt(t, stagingDir, l, fmt.Sprintf("newer-%d", i), t1.Add(time.Minute), 1, 50_000)
		if lessBytes(b[:], a[:]) {
			older, newer = a, b
			break
		}
		// Start again with an empty repository, so that only the last
		// pair is in it.
		stagingDir = t.TempDir()
		if l, err = stage.Open(stagingDir); err != nil {
			t.Fatal(err)
		}
	}
	if older == (object.ID{}) {
		t.Fatal("no pair of snapshots has an id order that is the reverse of the time order")
	}
	same := commitAt(t, stagingDir, l, "same-time", t1.Add(time.Minute), 1, 50_000)

	staged, err := StagedSnapshots(testObjectPath(stagingDir), mustSnapshotIDs(t, stagingDir), testOnDisc(l))
	if err != nil {
		t.Fatal(err)
	}
	var got []object.ID
	for _, s := range staged {
		if s.PackedInParts {
			t.Fatalf("snapshot %s is packed in parts, want no part on a disc", s.ID.TextForm())
		}
		got = append(got, s.ID)
	}
	want := []object.ID{older, newer, same}
	if lessBytes(same[:], newer[:]) {
		want = []object.ID{older, same, newer}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("StagedSnapshots order = %v, want %v", got, want)
	}

	order := testCandidateOrder(t, stagingDir, l)
	if o, n := unitIndex(order, older), unitIndex(order, newer); o < 0 || n < 0 || o > n {
		t.Fatalf("pack order: older snapshot at %d, newer at %d, want older first", o, n)
	}
}

// mustSnapshotIDs lists every snapshot of the test repository dir.
func mustSnapshotIDs(t *testing.T, dir string) []object.ID {
	t.Helper()
	ids, err := testSnapshotIDs(dir)()
	if err != nil {
		t.Fatal(err)
	}
	return ids
}
