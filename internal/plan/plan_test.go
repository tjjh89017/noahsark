package plan

import (
	"crypto/sha256"
	"encoding/binary"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// testID returns a content id that stands for item i.
func testID(i int) object.ID {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(i))
	return object.ID(sha256.Sum256(b[:]))
}

// writeIndex puts into c an INDEX for the disc uuid that lists the items
// ids, each with an object file of size bytes.
func writeIndex(t *testing.T, c *catalog.Catalog, uuid [16]byte, ids []int, size uint64) {
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
	for _, i := range ids {
		idx.Files = append(idx.Files, format.IndexFileRecord{ByteLen: size, Role: format.FileRoleObject})
		idx.Objects = append(idx.Objects, format.IndexObjectRecord{ContentID: testID(i), Kind: format.ObjectKindChunk})
	}
	buf := make([]byte, idx.EncodedLen())
	if _, err := idx.Encode(buf); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteDisc(uuid, buf, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func span(from, to int) []int {
	var out []int
	for i := from; i < to; i++ {
		out = append(out, i)
	}
	return out
}

// TestPlanCountsEachItemOnce spreads items over three discs that
// overlap, one of them lost, and adds each item several times, more
// items than one sort run holds. Each item goes to the first disc in
// the plan order, and an item that no disc lists counts as no disc.
func TestPlanCountsEachItemOnce(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, b, lost := [16]byte{1}, [16]byte{2}, [16]byte{3}
	const n = 3 * sortRunIDs / 2
	writeIndex(t, c, a, span(0, n/2), 10)
	writeIndex(t, c, b, span(n/4, n), 20)
	writeIndex(t, c, lost, span(n/2, n+100), 30)

	p, err := New(c, nil, []Disc{
		{DiscUUID: lost, DiscSeq: 0, Lost: true},
		{DiscUUID: b, DiscSeq: 2},
		{DiscUUID: a, DiscSeq: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for range 2 {
		for i := range n + 200 {
			p.Add(testID(i))
		}
	}
	if err := p.Count(); err != nil {
		t.Fatal(err)
	}

	type share struct {
		seq   uint64
		items int
		bytes uint64
	}
	want := []share{
		{0, 100, 100 * 30},
		{1, n / 2, n / 2 * 10},
		{2, n - n/2, (n - n/2) * 20},
	}
	got := p.Discs()
	if len(got) != len(want) {
		t.Fatalf("the plan names %d disc(s), want %d", len(got), len(want))
	}
	for i, d := range got {
		if d.DiscSeq != want[i].seq || d.Items != want[i].items || d.Bytes != want[i].bytes {
			t.Errorf("disc %d: seq %d, %d items, %d bytes; want %+v", i, d.DiscSeq, d.Items, d.Bytes, want[i])
		}
		count := 0
		for _, o := range d.Objects {
			if o.Kind != format.ObjectKindChunk || !p.Owns(d.DiscUUID, o.ID) {
				t.Fatalf("disc %d yields %s, which it does not own", d.DiscSeq, o.ID.TextForm())
			}
			count++
		}
		if count != d.Items {
			t.Errorf("disc %d yields %d item(s), want %d", d.DiscSeq, count, d.Items)
		}
	}
	if p.NoDisc() != 100 {
		t.Errorf("%d item(s) with no disc, want 100", p.NoDisc())
	}
	if p.Owns(a, testID(n-1)) || !p.Owns(b, testID(n-1)) {
		t.Error("Owns does not follow the INDEX of each disc")
	}
	// Disc a and disc b both list the items n/4 to n/2. The plan gives
	// them to disc a, the lower disc_seq, and to no other disc.
	if !p.Owns(a, testID(n/4)) || p.Owns(b, testID(n/4)) {
		t.Error("Owns gives an item that two discs list to both discs, or to the wrong one")
	}
	if p.Owns(lost, testID(n/2)) || !p.Owns(b, testID(n/2)) {
		t.Error("Owns gives an item that a lost disc and another disc list to the lost disc")
	}
}

// TestPlanWithNoDisc counts every item as no disc when the repository
// has no disc.
func TestPlanWithNoDisc(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Add(testID(1))
	p.Add(testID(2))
	p.Add(testID(1))
	if err := p.Count(); err != nil {
		t.Fatal(err)
	}
	if len(p.Discs()) != 0 || p.NoDisc() != 2 {
		t.Fatalf("%d disc(s), %d item(s) with no disc; want 0 and 2", len(p.Discs()), p.NoDisc())
	}
}

// localSet is a LocalStore that holds the items of a set, each with an
// object file of size bytes.
type localSet struct {
	held map[object.ID]bool
	size uint64
}

func (s localSet) Check(id object.ID) (uint64, bool) { return s.size, s.held[id] }

// TestPlanLocalStoreFirst gives the items that the local store holds to
// the store, before any disc. A disc whose items the store holds all is
// not in the plan, and an item with no disc that the store holds counts
// as local.
func TestPlanLocalStoreFirst(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, b := [16]byte{1}, [16]byte{2}
	writeIndex(t, c, a, span(0, 10), 10)
	writeIndex(t, c, b, span(10, 20), 20)
	p, err := New(c, nil, []Disc{{DiscUUID: a, DiscSeq: 0}, {DiscUUID: b, DiscSeq: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	held := map[object.ID]bool{}
	for _, i := range append(span(5, 20), 25) {
		held[testID(i)] = true
	}
	p.UseLocal(localSet{held: held, size: 7})
	for i := range 30 {
		p.Add(testID(i))
	}
	if err := p.Count(); err != nil {
		t.Fatal(err)
	}
	if items, bytes := p.Local(); items != 16 || bytes != 16*7 {
		t.Fatalf("local: %d items, %d bytes; want 16 and %d", items, bytes, 16*7)
	}
	discs := p.Discs()
	if len(discs) != 1 || discs[0].DiscUUID != a || discs[0].Items != 5 || discs[0].Bytes != 50 {
		t.Fatalf("discs %+v, want disc a alone with 5 items of 10 bytes", discs)
	}
	if p.NoDisc() != 9 {
		t.Errorf("%d item(s) with no disc, want 9", p.NoDisc())
	}
	if !p.Owns(a, testID(4)) || p.Owns(a, testID(5)) || p.Owns(b, testID(15)) {
		t.Error("Owns gives a disc an item that the local store supplies, or misses an item of the disc")
	}
	if !p.OwnsLocal(0, testID(5)) || !p.OwnsLocal(0, testID(25)) || p.OwnsLocal(0, testID(4)) || p.OwnsLocal(0, testID(26)) || p.OwnsLocal(1, testID(5)) {
		t.Error("OwnsLocal does not follow the local store")
	}
}

// TestPlanLocalBatches splits the items of the local store into batches
// of one sort run each. Each item belongs to one batch only.
func TestPlanLocalBatches(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	const n = localBatch + localBatch/2
	p.UseLocal(localAll{})
	for i := range n {
		p.Add(testID(i))
	}
	if p.LocalBatches() != 2 {
		t.Fatalf("%d local batch(es), want 2", p.LocalBatches())
	}
	// Ask one batch for every item before the next batch: Owns keeps the
	// owned ids of the last batch only.
	in0 := make([]bool, n)
	for i := range n {
		in0[i] = p.OwnsLocal(0, testID(i))
	}
	counts := [2]int{}
	for i := range n {
		in1 := p.OwnsLocal(1, testID(i))
		if in0[i] == in1 {
			t.Fatalf("item %d: batch 0 %v, batch 1 %v; want exactly one", i, in0[i], in1)
		}
		if in0[i] {
			counts[0]++
		} else {
			counts[1]++
		}
	}
	if counts[0] != localBatch || counts[1] != n-localBatch {
		t.Fatalf("batches hold %v items, want [%d %d]", counts, localBatch, n-localBatch)
	}
}

// localAll is a LocalStore that holds every item.
type localAll struct{}

func (localAll) Check(object.ID) (uint64, bool) { return 1, true }
