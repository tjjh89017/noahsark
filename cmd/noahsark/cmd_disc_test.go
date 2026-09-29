package main

import (
	"strings"
	"testing"
)

// TestDiscGlobalOptionBeforeSubcommandNamesTheFix checks that a global
// option between disc and its subcommand ("disc --repo=X burned") is
// reported with the corrected command line, not as an unknown
// subcommand.
func TestDiscGlobalOptionBeforeSubcommandNamesTheFix(t *testing.T) {
	code, out := runCmd(t, "disc", "--repo=X", "burned")
	if code != 2 {
		t.Fatalf("disc --repo=X burned: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--repo is a global option; give it before the command name: noahsark --repo=X disc burned") {
		t.Fatalf("disc --repo=X burned: output %q, want the corrected command line", out)
	}
	if strings.Contains(out, "unknown subcommand") {
		t.Fatalf("disc --repo=X burned: output %q, want no unknown-subcommand wording", out)
	}
}

// TestDiscUnknownSubcommandRefused checks that a subcommand this build
// does not have, such as the old "label" and "mark-degraded", is
// refused as an unknown subcommand rather than silently ignored.
func TestDiscUnknownSubcommandRefused(t *testing.T) {
	for _, args := range [][]string{
		{"disc", "label", "00000000-0000-0000-0000-000000000000", "TEXT"},
		{"disc", "mark-degraded", "00000000-0000-0000-0000-000000000000"},
	} {
		code, out := runCmd(t, args...)
		if code != 2 {
			t.Fatalf("%v: exit %d, want 2: %s", args, code, out)
		}
		if !strings.Contains(out, "unknown subcommand") {
			t.Fatalf("%v: output %q, want \"unknown subcommand\"", args, out)
		}
	}
}
