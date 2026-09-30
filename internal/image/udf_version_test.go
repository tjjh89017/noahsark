package image

import "testing"

// TestCheckUDFToolsVersion checks the version gate of image build on
// fake mkudffs output: udftools 2.3 or later passes, an older version
// and output with no version are refused. The minor number compares as
// a number, thus 2.10 is later than 2.3.
func TestCheckUDFToolsVersion(t *testing.T) {
	cases := []struct {
		out  string
		want string
		ok   bool
	}{
		{"mkudffs from udftools 2.2\nUsage:\n", "udftools 2.2", false},
		{"mkudffs from udftools 1.9\n", "udftools 1.9", false},
		{"mkudffs from udftools 2.3\nUsage:\n", "udftools 2.3", true},
		{"mkudffs from udftools 2.10\n", "udftools 2.10", true},
		{"mkudffs from udftools 3.0\n", "udftools 3.0", true},
		{"mkudffs: command not found\n", "", false},
		{"", "", false},
		{"udftools two.three\n", "", false},
	}
	for _, c := range cases {
		got, err := checkUDFToolsVersion(c.out)
		if (err == nil) != c.ok {
			t.Errorf("checkUDFToolsVersion(%q) error %v, want accepted = %v", c.out, err, c.ok)
		}
		if got != c.want {
			t.Errorf("checkUDFToolsVersion(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}
