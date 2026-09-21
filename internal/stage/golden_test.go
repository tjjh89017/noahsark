package stage

import (
	"os"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
)

// fillID returns a 32-byte content id with every byte set to b, and
// fillDisc a 16-byte disc uuid the same way: simple, deterministic test
// fixtures, not real content ids.
func fillID(b byte) object.ID {
	var id object.ID
	for i := range id {
		id[i] = b
	}
	return id
}

func fillDisc(b byte) [16]byte {
	var d [16]byte
	for i := range d {
		d[i] = b
	}
	return d
}

// goldenCleanSec is a fixed clean time for the golden records, so the
// checked-in bytes never depend on the clock.
const goldenCleanSec = int64(1700000000)

// goldenRecords is one record per state, plus one Staged record per
// reason code, in a fixed order matching
// testdata/state_records_golden.bin. The two Clean records are the two
// verifies of the two identical discs; the second one keeps the clean
// time of the first, and the ON-DISC record carries both forward. The
// last record is the ON-DISC record recover writes: a disc holds
// the object, and no verify has happened here.
func goldenRecords() []Record {
	discA := fillDisc(0xAA)
	return []Record{
		{Sequence: 1, ContentID: fillID(0x11), State: Staged},
		{Sequence: 2, ContentID: fillID(0x22), State: Packed, RunSeq: 7, DiscUUID: discA},
		{Sequence: 3, ContentID: fillID(0x22), State: Burned, RunSeq: 7, DiscUUID: discA},
		{Sequence: 4, ContentID: fillID(0x22), State: Clean, RunSeq: 7, DiscUUID: discA, VerifyCount: 1, CleanSec: goldenCleanSec},
		{Sequence: 5, ContentID: fillID(0x22), State: Clean, RunSeq: 7, DiscUUID: discA, VerifyCount: 2, CleanSec: goldenCleanSec},
		{Sequence: 6, ContentID: fillID(0x22), State: OnDiscOnly, RunSeq: 7, DiscUUID: discA, VerifyCount: 2, CleanSec: goldenCleanSec},
		{Sequence: 7, ContentID: fillID(0x33), State: Staged, Reason: ReasonBurnFailed},
		{Sequence: 8, ContentID: fillID(0x44), State: Packed, RunSeq: 9, DiscUUID: fillDisc(0xBB), Reason: ReasonVerifyFailed},
		{Sequence: 9, ContentID: fillID(0x55), State: Staged, Reason: ReasonHealed},
		{Sequence: 10, ContentID: fillID(0x66), State: Staged, Reason: ReasonDuplicateLocality},
		{Sequence: 11, ContentID: fillID(0x77), State: OnDiscOnly, RunSeq: 3, DiscUUID: fillDisc(0xCC)},
	}
}

// TestStateRecordGolden encodes one record per state and per reason
// code and compares the bytes to a checked-in golden file, then decodes
// that file and compares the fields back.
func TestStateRecordGolden(t *testing.T) {
	recs := goldenRecords()
	var got []byte
	buf := make([]byte, recordLen)
	for i := range recs {
		recs[i].encode(buf)
		got = append(got, buf...)
	}

	want, err := os.ReadFile("testdata/state_records_golden.bin")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("first differing byte at offset %d: got 0x%02x, want 0x%02x", i, got[i], want[i])
		}
	}

	for i, r := range recs {
		off := i * recordLen
		decoded, ok := decodeRecord(want[off : off+recordLen])
		if !ok {
			t.Fatalf("record %d: decode reported a bad CRC", i)
		}
		if decoded != r {
			t.Fatalf("record %d: got %+v, want %+v", i, decoded, r)
		}
	}
}
