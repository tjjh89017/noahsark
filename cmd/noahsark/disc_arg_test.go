package main

import (
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
)

// discArgRow builds one synthetic DiscsRow for resolveDiscArg tests,
// enough of it to resolve by seq, uuid or label: seq, label and a uuid
// built from uuidByte repeated across all 16 bytes.
func discArgRow(seq uint64, label string, uuidByte byte) format.DiscsRow {
	var l [format.DiscsLabelLen]byte
	n := copy(l[:], label)
	var u [16]byte
	for i := range u {
		u[i] = uuidByte
	}
	return format.DiscsRow{DiscSeq: seq, DiscUUID: u, Label: l, LabelLen: uint16(n)}
}

func TestResolveDiscArgBySeq(t *testing.T) {
	rows := []format.DiscsRow{discArgRow(0, "disc-a", 0xaa), discArgRow(1, "disc-b", 0xbb)}
	uuid, err := resolveDiscArg(rows, "1")
	if err != nil {
		t.Fatalf("resolveDiscArg(1): %v", err)
	}
	if uuid != rows[1].DiscUUID {
		t.Fatalf("resolveDiscArg(1) = %x, want disc-b's uuid", uuid)
	}
}

func TestResolveDiscArgByFullUUID(t *testing.T) {
	rows := []format.DiscsRow{discArgRow(0, "disc-a", 0xaa), discArgRow(1, "disc-b", 0xbb)}
	arg := uuidText(rows[0].DiscUUID)
	uuid, err := resolveDiscArg(rows, arg)
	if err != nil {
		t.Fatalf("resolveDiscArg(%s): %v", arg, err)
	}
	if uuid != rows[0].DiscUUID {
		t.Fatalf("resolveDiscArg(%s) = %x, want disc-a's uuid", arg, uuid)
	}
}

func TestResolveDiscArgByUniquePrefix(t *testing.T) {
	rows := []format.DiscsRow{discArgRow(0, "disc-a", 0xaa), discArgRow(1, "disc-b", 0xbb)}
	uuid, err := resolveDiscArg(rows, "aaaaaaaa")
	if err != nil {
		t.Fatalf("resolveDiscArg(aaaaaaaa): %v", err)
	}
	if uuid != rows[0].DiscUUID {
		t.Fatalf("resolveDiscArg(aaaaaaaa) = %x, want disc-a's uuid", uuid)
	}
}

func TestResolveDiscArgByLabel(t *testing.T) {
	rows := []format.DiscsRow{discArgRow(0, "disc-a", 0xaa), discArgRow(1, "disc-b", 0xbb)}
	uuid, err := resolveDiscArg(rows, "disc-b")
	if err != nil {
		t.Fatalf("resolveDiscArg(disc-b): %v", err)
	}
	if uuid != rows[1].DiscUUID {
		t.Fatalf("resolveDiscArg(disc-b) = %x, want disc-b's uuid", uuid)
	}
}

func TestResolveDiscArgAmbiguousPrefix(t *testing.T) {
	// Two discs whose uuids share their first 8 hex characters.
	rowA := discArgRow(0, "disc-a", 0xaa)
	rowB := discArgRow(1, "disc-b", 0xaa)
	rowB.DiscUUID[15] = 0xbb
	rows := []format.DiscsRow{rowA, rowB}

	_, err := resolveDiscArg(rows, "aaaaaaaa")
	if err == nil {
		t.Fatal("resolveDiscArg(aaaaaaaa): expected an ambiguous-match error")
	}
	if !strings.Contains(err.Error(), "more than one disc") {
		t.Fatalf("error = %q, want the ambiguous-match wording", err)
	}
	if strings.Count(err.Error(), "\n") != 2 {
		t.Fatalf("error = %q, want one candidate line per disc", err)
	}
}

func TestResolveDiscArgDuplicateLabel(t *testing.T) {
	rows := []format.DiscsRow{discArgRow(0, "spare", 0xaa), discArgRow(1, "spare", 0xbb)}
	_, err := resolveDiscArg(rows, "spare")
	if err == nil {
		t.Fatal("resolveDiscArg(spare): expected an ambiguous-match error")
	}
	if !strings.Contains(err.Error(), "more than one disc") {
		t.Fatalf("error = %q, want the ambiguous-match wording", err)
	}
	if strings.Count(err.Error(), "\n") != 2 {
		t.Fatalf("error = %q, want one candidate line per disc", err)
	}
}

func TestResolveDiscArgNoMatchListsCandidates(t *testing.T) {
	rows := []format.DiscsRow{discArgRow(0, "disc-a", 0xaa), discArgRow(1, "disc-b", 0xbb)}
	_, err := resolveDiscArg(rows, "no-such-disc")
	if err == nil {
		t.Fatal("resolveDiscArg(no-such-disc): expected a no-match error")
	}
	if !strings.Contains(err.Error(), "matches no disc") {
		t.Fatalf("error = %q, want the no-match wording", err)
	}
	if strings.Count(err.Error(), "\n") != 2 {
		t.Fatalf("error = %q, want the full disc list as candidates", err)
	}
}
