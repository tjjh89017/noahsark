package main

import (
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/format"
)

// discArgRow builds one ledger row with seq, label and a uuid that
// repeats uuidByte in all 16 bytes.
func discArgRow(seq uint64, label string, uuidByte byte) format.DiscsRow {
	var l [format.DiscsLabelLen]byte
	n := copy(l[:], label)
	var u [16]byte
	for i := range u {
		u[i] = uuidByte
	}
	return format.DiscsRow{DiscSeq: seq, DiscUUID: u, Label: l, LabelLen: uint16(n)}
}

func twoDiscRows() []format.DiscsRow {
	return []format.DiscsRow{discArgRow(0, "disc-a", 0xaa), discArgRow(1, "disc-b", 0xbb)}
}

func TestResolveDiscArgMatchesOneDisc(t *testing.T) {
	rows := twoDiscRows()
	hyphenated := uuidText(rows[0].DiscUUID)
	plain := strings.ReplaceAll(hyphenated, "-", "")
	tests := []struct {
		name, arg string
		want      [16]byte
	}{
		{"number", "1", rows[1].DiscUUID},
		{"full uuid with hyphens", hyphenated, rows[0].DiscUUID},
		{"full uuid without hyphens", plain, rows[0].DiscUUID},
		{"full uuid in upper case", strings.ToUpper(hyphenated), rows[0].DiscUUID},
		{"prefix", "bbbb", rows[1].DiscUUID},
		{"prefix in upper case with a hyphen", "AAAAAAAA-AA", rows[0].DiscUUID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveDiscArg(rows, tt.arg)
			if err != nil {
				t.Fatalf("resolveDiscArg(%q): %v", tt.arg, err)
			}
			if got != tt.want {
				t.Fatalf("resolveDiscArg(%q) = %x, want %x", tt.arg, got, tt.want)
			}
		})
	}
}

// TestResolveDiscArgNumberBeforePrefix gives a value that is the number
// of one disc and a uuid prefix of another disc. The number wins.
func TestResolveDiscArgNumberBeforePrefix(t *testing.T) {
	rows := []format.DiscsRow{discArgRow(12, "disc-a", 0xaa), discArgRow(3, "disc-b", 0x12)}
	got, err := resolveDiscArg(rows, "12")
	if err != nil {
		t.Fatalf("resolveDiscArg(12): %v", err)
	}
	if got != rows[0].DiscUUID {
		t.Fatalf("resolveDiscArg(12) = %x, want the uuid of disc 12", got)
	}
}

// TestResolveDiscArgShortNumberIsNoPrefix gives decimal values of fewer
// than 8 digits that no disc has as its number. A uuid of a disc starts
// with each value, but no value names that disc.
func TestResolveDiscArgShortNumberIsNoPrefix(t *testing.T) {
	rows := []format.DiscsRow{discArgRow(0, "disc-a", 0xaa), discArgRow(1, "disc-b", 0x12)}
	for _, arg := range []string{"12", "1212121"} {
		_, err := resolveDiscArg(rows, arg)
		if err == nil || err.Error() != noDiscMatchText(arg) {
			t.Fatalf("resolveDiscArg(%q) error = %v, want no match", arg, err)
		}
	}
}

// TestResolveDiscArgEightDigitsArePrefix gives decimal values of 8
// digits or more. Each value is a uuid prefix, not a disc number.
func TestResolveDiscArgEightDigitsArePrefix(t *testing.T) {
	rows := []format.DiscsRow{discArgRow(12121212, "disc-a", 0xaa), discArgRow(1, "disc-b", 0x12)}
	for _, arg := range []string{"12121212", "121212121"} {
		got, err := resolveDiscArg(rows, arg)
		if err != nil {
			t.Fatalf("resolveDiscArg(%q): %v", arg, err)
		}
		if got != rows[1].DiscUUID {
			t.Fatalf("resolveDiscArg(%q) = %x, want the uuid of disc-b", arg, got)
		}
	}
}

// TestResolveDiscArgLeadingZeros gives a disc number with leading
// zeros. The value is still the disc number.
func TestResolveDiscArgLeadingZeros(t *testing.T) {
	rows := twoDiscRows()
	got, err := resolveDiscArg(rows, "01")
	if err != nil {
		t.Fatalf("resolveDiscArg(01): %v", err)
	}
	if got != rows[1].DiscUUID {
		t.Fatalf("resolveDiscArg(01) = %x, want the uuid of disc 1", got)
	}
}

func TestResolveDiscArgMoreThanOneDisc(t *testing.T) {
	prefixA := discArgRow(0, "disc-a", 0xaa)
	prefixB := discArgRow(1, "disc-b", 0xaa)
	prefixB.DiscUUID[15] = 0xbb
	tests := []struct {
		name, arg string
		rows      []format.DiscsRow
	}{
		{"prefix", "aaaa", []format.DiscsRow{prefixA, prefixB}},
		{"shared number", "1", []format.DiscsRow{discArgRow(1, "disc-a", 0xaa), discArgRow(1, "disc-b", 0xbb)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveDiscArg(tt.rows, tt.arg)
			if err == nil {
				t.Fatalf("resolveDiscArg(%q): no error, want a refusal", tt.arg)
			}
			lines := []string{manyDiscsMatchText(tt.arg)}
			for _, r := range tt.rows {
				lines = append(lines, discNameShort(r.DiscSeq, labelText(r.Label[:r.LabelLen]))+"  "+uuidText(r.DiscUUID))
			}
			if want := strings.Join(lines, "\n"); err.Error() != want {
				t.Fatalf("error = %q, want %q", err, want)
			}
		})
	}
}

func TestResolveDiscArgNoMatch(t *testing.T) {
	for _, arg := range []string{"7", "cccc", "disc-b", "", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaaaa"} {
		t.Run(arg, func(t *testing.T) {
			_, err := resolveDiscArg(twoDiscRows(), arg)
			if err == nil {
				t.Fatalf("resolveDiscArg(%q): no error, want a refusal", arg)
			}
			if want := noDiscMatchText(arg); err.Error() != want {
				t.Fatalf("error = %q, want %q", err, want)
			}
		})
	}
}

// TestResolveDiscArgHiddenDisc hides one disc, as the rule for an
// undone disc does. No form of argument names the hidden disc.
func TestResolveDiscArgHiddenDisc(t *testing.T) {
	rows := twoDiscRows()
	hidden := func(uuid [16]byte) bool { return uuid == rows[0].DiscUUID }
	for _, arg := range []string{"0", uuidText(rows[0].DiscUUID), "aaaa"} {
		if _, err := resolveDiscArgExcept(rows, arg, hidden); err == nil || err.Error() != noDiscMatchText(arg) {
			t.Fatalf("resolveDiscArgExcept(%q) error = %v, want no match", arg, err)
		}
	}
	got, err := resolveDiscArgExcept(rows, "1", hidden)
	if err != nil || got != rows[1].DiscUUID {
		t.Fatalf("resolveDiscArgExcept(1) = %x, %v, want the uuid of disc 1", got, err)
	}
}

// TestResolveDiscArgHiddenDiscSharesNumber hides one of two discs with
// the same number. The number then names the other disc.
func TestResolveDiscArgHiddenDiscSharesNumber(t *testing.T) {
	rows := []format.DiscsRow{discArgRow(1, "disc-a", 0xaa), discArgRow(1, "disc-b", 0xbb)}
	hidden := func(uuid [16]byte) bool { return uuid == rows[0].DiscUUID }
	got, err := resolveDiscArgExcept(rows, "1", hidden)
	if err != nil || got != rows[1].DiscUUID {
		t.Fatalf("resolveDiscArgExcept(1) = %x, %v, want the uuid of disc-b", got, err)
	}
}
