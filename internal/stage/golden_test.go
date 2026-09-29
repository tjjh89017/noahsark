package stage

import (
	"bytes"
	"os"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
)

// fillID returns a content id with every byte set to b, and fillDisc a
// disc uuid the same way: simple test values, not real content ids.
func fillID(b byte) object.ID {
	return object.ID(bytes.Repeat([]byte{b}, len(object.ID{})))
}

func fillDisc(b byte) [16]byte {
	return [16]byte(bytes.Repeat([]byte{b}, 16))
}

// goldenRecords is one record for each state and for each reason, in the
// order of testdata/state_records_golden.bin. A separate script with its
// own CRC-32C code wrote that file.
func goldenRecords() []Record {
	discA, discB := fillDisc(0xAA), fillDisc(0xBB)
	return []Record{
		{Sequence: 1, ContentID: fillID(0x11), State: Staged},
		{Sequence: 2, ContentID: fillID(0x22), State: Packed, RunSeq: 7, DiscUUID: discA},
		{Sequence: 3, ContentID: fillID(0x22), State: OnDisc, RunSeq: 7, DiscUUID: discA},
		{Sequence: 4, ContentID: fillID(0x22), State: Lost, RunSeq: 7, DiscUUID: discA, Reason: ReasonDiscLost},
		{Sequence: 5, ContentID: fillID(0x33), State: Staged, Reason: ReasonPackUndone},
		{Sequence: 6, ContentID: fillID(0x44), State: Staged, Reason: ReasonDiscLost},
		{Sequence: 7, ContentID: fillID(0x55), State: Packed, RunSeq: 9, DiscUUID: discB, Reason: ReasonLostUndone},
		{Sequence: 8, ContentID: fillID(0x22), State: OnDisc, RunSeq: 7, DiscUUID: discA, Reason: ReasonLostUndone},
	}
}

// TestStateRecordGolden encodes the golden records and compares the
// bytes to the golden file. Then it decodes the golden file and compares
// the fields.
func TestStateRecordGolden(t *testing.T) {
	want, err := os.ReadFile("testdata/state_records_golden.bin")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	recs := goldenRecords()

	got := make([]byte, len(recs)*recordLen)
	for i := range recs {
		recs[i].encode(got[i*recordLen : (i+1)*recordLen])
	}
	if len(got) != len(want) {
		t.Fatalf("length: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("first different byte at offset %d (record %d, field offset %d): got 0x%02x, want 0x%02x",
				i, i/recordLen+1, i%recordLen, got[i], want[i])
		}
	}

	for i, wantRec := range recs {
		buf := want[i*recordLen : (i+1)*recordLen]
		if !recordIntact(buf) {
			t.Fatalf("record %d: bad CRC in the golden file", i+1)
		}
		gotRec := decodeRecord(buf)
		if gotRec != wantRec {
			t.Fatalf("record %d: decoded %+v, want %+v", i+1, gotRec, wantRec)
		}
		if err := gotRec.check(); err != nil {
			t.Fatalf("record %d: check: %v", i+1, err)
		}
	}
}
