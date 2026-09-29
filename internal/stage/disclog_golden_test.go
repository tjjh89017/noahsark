package stage

import (
	"bytes"
	"os"
	"testing"
)

// goldenDiscRecords is one record for each event code, in code order,
// as testdata/discstate_golden.bin holds them. Record i has sequence i,
// time 1790000000 + 60*i, and a disc uuid with each byte 0x10 + i.
// Packed carries both flags and the numbers 4 and 9. Recovered carries
// the close flag.
func goldenDiscRecords() []DiscRecord {
	var recs []DiscRecord
	for i := uint64(1); i <= 13; i++ {
		rec := DiscRecord{
			Sequence: i,
			TimeSec:  1790000000 + int64(i)*60,
			DiscUUID: [16]byte(bytes.Repeat([]byte{0x10 + byte(i)}, 16)),
			Event:    DiscEvent(i),
		}
		switch rec.Event {
		case EventPacked:
			rec.Flags = FlagClose | FlagFEC
			rec.DiscSeq = 4
			rec.RunSeq = 9
		case EventRecovered:
			rec.Flags = FlagClose
		}
		recs = append(recs, rec)
	}
	return recs
}

// TestDiscRecordGolden encodes one record for each event and compares
// the bytes to the golden file. Then it decodes the golden file and
// compares the fields.
func TestDiscRecordGolden(t *testing.T) {
	want, err := os.ReadFile("testdata/discstate_golden.bin")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	recs := goldenDiscRecords()

	got := make([]byte, len(recs)*discRecordLen)
	for i := range recs {
		recs[i].encode(got[i*discRecordLen : (i+1)*discRecordLen])
	}
	if len(got) != len(want) {
		t.Fatalf("length: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("first different byte at offset %d (record %d, field offset %d): got 0x%02x, want 0x%02x",
				i, i/discRecordLen+1, i%discRecordLen, got[i], want[i])
		}
	}

	for i, wantRec := range recs {
		buf := want[i*discRecordLen : (i+1)*discRecordLen]
		if !recordIntact(buf) {
			t.Fatalf("record %d: bad CRC in the golden file", i+1)
		}
		if gotRec := decodeDiscRecord(buf); gotRec != wantRec {
			t.Fatalf("record %d: decoded %+v, want %+v", i+1, gotRec, wantRec)
		}
		// The golden Recovered record carries the close bit, which the
		// record check refuses: only the fec bit is valid in Recovered.
		err := wantRec.check()
		switch {
		case wantRec.Event == EventRecovered && err == nil:
			t.Fatalf("record %d: check accepts the close bit in Recovered", i+1)
		case wantRec.Event != EventRecovered && err != nil:
			t.Fatalf("record %d: check: %v", i+1, err)
		}
	}
}
