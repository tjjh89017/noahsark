package main

import (
	"strings"
	"testing"
)

// TestDiscFlagBeforeSubcommandNamesTheFix checks that a flag given
// before disc's subcommand ("disc --repo=X burned") is reported with the
// corrected command line, not as an unknown subcommand.
func TestDiscFlagBeforeSubcommandNamesTheFix(t *testing.T) {
	code, out := runCmd(t, "disc", "--repo=X", "burned")
	if code != 2 {
		t.Fatalf("disc --repo=X burned: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "flags come after the subcommand: noahsark disc burned --repo=X") {
		t.Fatalf("disc --repo=X burned: output %q, want the flags-come-after-the-subcommand fix", out)
	}
	if strings.Contains(out, "unknown subcommand") {
		t.Fatalf("disc --repo=X burned: output %q, want no unknown-subcommand wording", out)
	}
}

// TestDiscListPointsAtStatus checks that the old "disc list" name is
// refused and names the command that replaced it.
func TestDiscListPointsAtStatus(t *testing.T) {
	code, out := runCmd(t, "disc", "list")
	if code != 2 {
		t.Fatalf("disc list: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "noahsark status") {
		t.Fatalf("disc list: output %q, want the status command named", out)
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
