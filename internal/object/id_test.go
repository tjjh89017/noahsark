package object

import "testing"

func TestComputeIDKnownVector(t *testing.T) {
	// SHA-256 of the empty string is a standard test vector.
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	id := ComputeID(nil)
	got := hexString(id)
	if got != want {
		t.Fatalf("ComputeID(nil) = %s, want %s", got, want)
	}
}

func TestTextFormIsMultihash(t *testing.T) {
	id := ComputeID(nil)
	// 0x12 (sha2-256) and 0x20 (32) each fit one varint byte, so the
	// multihash prefix is the fixed string "1220".
	want := "1220" + hexString(id)
	got := id.TextForm()
	if got != want {
		t.Fatalf("TextForm() = %s, want %s", got, want)
	}
	if len(got) != 68 {
		t.Fatalf("TextForm() length = %d, want 68", len(got))
	}
}

func TestFanoutByte(t *testing.T) {
	var id ID
	id[0] = 0xab
	if got := id.FanoutByte(); got != "ab" {
		t.Fatalf("FanoutByte() = %s, want ab", got)
	}
}

func hexString(id ID) string {
	const hexdigits = "0123456789abcdef"
	buf := make([]byte, 0, len(id)*2)
	for _, b := range id {
		buf = append(buf, hexdigits[b>>4], hexdigits[b&0x0f])
	}
	return string(buf)
}
