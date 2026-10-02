package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestRefuseWhileMissingListsDiscsByNumber gives three missing discs
// whose uuid order is the reverse of their disc number order. The
// refusal must list them by disc number. The disc state log records no
// disc number for a missing disc, thus the number comes from the ledger.
func TestRefuseWhileMissingListsDiscsByNumber(t *testing.T) {
	repo := t.TempDir()
	repoUUID := [16]byte{0x11}
	cfg := repoConfig{RepoUUID: hex.EncodeToString(repoUUID[:])}
	layout := layoutOf(repo, cfg)
	if err := os.MkdirAll(layout.stateDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	var rows []format.DiscsRow
	var recs []stage.DiscRecord
	for seq := range uint64(3) {
		row := format.DiscsRow{RunSeq: seq + 1, DiscSeq: seq, CreatedSec: int64(seq + 1)}
		row.DiscUUID[0] = byte(0xf0 - seq)
		label := "disc " + string(rune('0'+seq))
		row.LabelLen = uint16(copy(row.Label[:], label))
		rows = append(rows, row)
		recs = append(recs, stage.DiscRecord{TimeSec: 1, DiscUUID: row.DiscUUID, Event: stage.EventNamedMissing})
	}
	if err := image.SaveDiscsLedger(layout.discsLedgerFile(), repoUUID, rows); err != nil {
		t.Fatal(err)
	}
	discs, err := stage.OpenDiscLog(layout.stateDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := discs.Append(recs...); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	if !refuseWhileMissing("pack", layout, cfg, discs, &stderr) {
		t.Fatal("refuseWhileMissing did not refuse")
	}
	want := strings.Join([]string{
		`noahsark: pack: disc 0 "disc 0" is missing`,
		`noahsark: pack: disc 1 "disc 1" is missing`,
		`noahsark: pack: disc 2 "disc 2" is missing`,
	}, "\n") + "\n"
	if got := stderr.String(); got != want {
		t.Errorf("refusal\n%s\nwant\n%s", got, want)
	}
}
