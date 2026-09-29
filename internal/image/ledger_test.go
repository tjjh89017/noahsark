package image

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
)

// TestLedgersSaveAtTheGivenPathWithAReplace saves each ledger two
// times at an explicit path, loads it back, and checks that the
// directory holds only the ledger file: the save leaves no temporary
// file.
func TestLedgersSaveAtTheGivenPathWithAReplace(t *testing.T) {
	dir := t.TempDir()
	repoUUID := [16]byte{1, 2, 3, 4}
	discs := filepath.Join(dir, "discs.bin")
	refs := filepath.Join(dir, "refslog.bin")
	rows := []format.DiscsRow{{RunSeq: 1, DiscSeq: 0, DiscUUID: [16]byte{9}}}
	rec := format.RefRecord{NameLen: 1, TimeSec: 7}
	rec.Name[0] = 'a'

	for range 2 {
		if err := SaveDiscsLedger(discs, repoUUID, rows); err != nil {
			t.Fatal(err)
		}
		if err := SaveRefsLedger(refs, repoUUID, []format.RefRecord{rec}); err != nil {
			t.Fatal(err)
		}
	}
	gotDiscs, err := LoadDiscsLedger(discs, repoUUID)
	if err != nil || len(gotDiscs.Rows) != 1 || gotDiscs.Rows[0].DiscUUID != rows[0].DiscUUID {
		t.Fatalf("disc ledger = %+v, %v; want the saved row", gotDiscs.Rows, err)
	}
	gotRefs, err := LoadRefsLedger(refs, repoUUID)
	if err != nil || len(gotRefs.Records) != 1 || gotRefs.Records[0].Name[0] != 'a' {
		t.Fatalf("ref ledger = %+v, %v; want the saved record", gotRefs.Records, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("directory holds %d entries, want the two ledgers only", len(entries))
	}
}
