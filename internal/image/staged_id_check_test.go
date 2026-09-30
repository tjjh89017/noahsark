package image

import (
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestPackSkipsAStagedObjectWithAFlippedByte flips the last byte of one
// staged tree, blob or snapshot object and keeps its length. The object
// still decodes, thus only the content id check can find the damage.
// Pack must report the object as damaged, leave it and the snapshot
// above it off the run, keep them Staged, and pack the other snapshot.
func TestPackSkipsAStagedObjectWithAFlippedByte(t *testing.T) {
	for _, kind := range []format.ObjectKind{format.ObjectKindTree, format.ObjectKindBlob, format.ObjectKindSnapshot} {
		t.Run(kindName(kind), func(t *testing.T) {
			stagingDir := t.TempDir()
			l, err := stage.Open(stagingDir)
			if err != nil {
				t.Fatal(err)
			}
			damagedSnap := commitNamedFixture(t, stagingDir, "damaged")
			goodSnap := commitNamedFixture(t, stagingDir, "good")
			markStagedFromCommit(t, stagingDir, damagedSnap, l)
			markStagedFromCommit(t, stagingDir, goodSnap, l)

			victim := onlyReachableFrom(t, stagingDir, damagedSnap, goodSnap, kind)
			flipLastByte(t, testObjectPath(stagingDir)(kind, victim))

			outDir := t.TempDir()
			opts := packOpts(stagingDir, goodSnap, outDir, sectorsFor(50_000_000), 1, l)
			var got []UnreadableItem
			opts.Unreadable = func(item UnreadableItem) { got = append(got, item) }
			if _, err := Pack(opts); err != nil {
				t.Fatalf("Pack: %v", err)
			}
			if len(got) != 1 || got[0].ID != victim || got[0].Kind != kind || got[0].Missing || got[0].Err != nil {
				t.Fatalf("Unreadable got %+v, want the damaged %s %s", got, kindName(kind), victim.TextForm())
			}
			if want := kindName(kind) + " " + victim.TextForm() + " is damaged"; got[0].Problem() != want {
				t.Fatalf("problem %q, want %q", got[0].Problem(), want)
			}

			rr, err := Read(outDir)
			if err != nil {
				t.Fatal(err)
			}
			held := make(map[object.ID]bool)
			for _, row := range rr.Index.Objects {
				held[object.ID(row.ContentID)] = true
			}
			if held[victim] || held[damagedSnap] {
				t.Fatalf("the run holds the damaged %s or its snapshot, want both left out", kindName(kind))
			}
			if !held[goodSnap] {
				t.Fatalf("the run does not hold the snapshot %s, want the other snapshot packed", goodSnap.TextForm())
			}
			replayed, err := stage.Open(stagingDir)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []object.ID{victim, damagedSnap} {
				if rec, _ := replayed.Get(id); rec.State != stage.Staged {
					t.Fatalf("%s is %v after the pack, want Staged", id.TextForm(), rec.State)
				}
			}
		})
	}
}

// onlyReachableFrom returns an object of kind that snapshot from
// reaches and snapshot other does not reach.
func onlyReachableFrom(t *testing.T, stagingDir string, from, other object.ID, kind format.ObjectKind) object.ID {
	t.Helper()
	paths := testObjectPath(stagingDir)
	shared, err := CollectReachable(paths, []object.ID{other})
	if err != nil {
		t.Fatal(err)
	}
	inOther := make(map[object.ID]bool)
	for _, o := range shared {
		inOther[o.ID] = true
	}
	objs, err := CollectReachable(paths, []object.ID{from})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if o.Kind == kind && !inOther[o.ID] {
			return o.ID
		}
	}
	t.Fatalf("snapshot %s reaches no %s of its own", from.TextForm(), kindName(kind))
	return object.ID{}
}
