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
			if o.Kind != format.ObjectKindChunk || !p.Holds(d.DiscUUID, o.ID) {
				t.Fatalf("disc %d yields %s, which it does not hold", d.DiscSeq, o.ID.TextForm())
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
	if p.Holds(a, testID(n-1)) || !p.Holds(b, testID(n-1)) {
		t.Error("Holds does not follow the INDEX of each disc")
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
