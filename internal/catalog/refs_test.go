package catalog_test

import (
	"errors"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
)

// refRecord makes a ref record that points name at the snapshot whose
// digest starts with the byte idByte.
func refRecord(name string, idByte byte, sec int64, nsec uint32) format.RefRecord {
	r := format.RefRecord{TimeSec: sec, TimeNsec: nsec, NameLen: uint16(len(name))}
	r.SnapshotID[0] = idByte
	copy(r.Name[:], name)
	return r
}

// writeDiscRefs writes a disc into the catalog whose REFS table holds
// recs. The INDEX and DISCS files are placeholders: MergedRefs does not
// read them.
func writeDiscRefs(t *testing.T, c *catalog.Catalog, uuid [16]byte, recs ...format.RefRecord) {
	t.Helper()
	table := format.RefsTable{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicRefs,
			VersionMajor: 1, HeaderLen: format.RefsHeaderLen,
		},
		RecordCount: uint64(len(recs)), Records: recs,
	}
	buf := make([]byte, table.EncodedLen())
	if _, err := table.Encode(buf); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteDisc(uuid, []byte("index"), buf, []byte("discs")); err != nil {
		t.Fatal(err)
	}
}

func TestMergedRefsNoDisc(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.MergedRefs(); !errors.Is(err, catalog.ErrNoDisc) {
		t.Fatalf("MergedRefs of an empty catalog: err %v, want ErrNoDisc", err)
	}
}

// TestMergedRefsNewestWins checks that for a name that two discs carry
// with different times, the newest record wins, whatever the uuid order
// of the discs is. A name that only one disc carries keeps its record.
func TestMergedRefsNewestWins(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The disc with the lower uuid carries the newer record of "daily".
	writeDiscRefs(t, c, [16]byte{1},
		refRecord("daily", 0xbb, 200, 0),
		refRecord("only-a", 0x11, 50, 0))
	writeDiscRefs(t, c, [16]byte{2},
		refRecord("daily", 0xaa, 100, 999),
		refRecord("only-b", 0x22, 60, 0))

	got, err := c.MergedRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("MergedRefs: %d names, want 3: %v", len(got), got)
	}
	if got["daily"].SnapshotID[0] != 0xbb {
		t.Fatalf("daily points at %x, want the newer record bb", got["daily"].SnapshotID[0])
	}
	if got["only-a"].SnapshotID[0] != 0x11 || got["only-b"].SnapshotID[0] != 0x22 {
		t.Fatalf("a name on one disc lost its record: %v", got)
	}
}

// TestMergeRefTieOnTime checks the tie rule: with equal times, the
// record with the higher snapshot id wins, in either order.
func TestMergeRefTieOnTime(t *testing.T) {
	low, high := refRecord("x", 0x01, 10, 5), refRecord("x", 0x02, 10, 5)
	for _, order := range [][]format.RefRecord{{low, high}, {high, low}} {
		m := map[string]format.RefRecord{}
		for _, r := range order {
			catalog.MergeRef(m, r)
		}
		if m["x"].SnapshotID[0] != 0x02 {
			t.Fatalf("tie on time: got %x, want 02", m["x"].SnapshotID[0])
		}
	}
}

func TestRefName(t *testing.T) {
	if got := catalog.RefName(refRecord("2026-09-13", 0, 0, 0)); got != "2026-09-13" {
		t.Fatalf("RefName = %q", got)
	}
}

// TestMergedRefsSkipsADamagedTable checks that a REFS table that does
// not decode leaves the records of the other tables, and that the error
// names the disc of the damaged table.
func TestMergedRefsSkipsADamagedTable(t *testing.T) {
	c, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeDiscRefs(t, c, [16]byte{1}, refRecord("daily", 0xbb, 200, 0))
	bad := [16]byte{2}
	if err := c.WriteDisc(bad, []byte("index"), []byte("not a REFS table"), []byte("discs")); err != nil {
		t.Fatal(err)
	}

	got, err := c.MergedRefs()
	damaged, ok := errors.AsType[*catalog.DamagedRefsError](err)
	if !ok {
		t.Fatalf("MergedRefs: err %v, want a *catalog.DamagedRefsError", err)
	}
	if len(damaged.Discs) != 1 || damaged.Discs[0] != bad {
		t.Fatalf("damaged tables %v, want the disc %v", damaged.Discs, bad)
	}
	if got["daily"].SnapshotID[0] != 0xbb {
		t.Fatalf("MergedRefs lost the record of the good table: %v", got)
	}
}
