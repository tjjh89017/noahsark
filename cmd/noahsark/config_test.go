package main

import (
	"strings"
	"testing"
)

// TestDiscoverRepoNotFoundNamesTheFix checks that discoverRepo's
// not-found error tells the reader how to fix it: pass --repo, set
// NOAHSARK_REPO, or run from inside the repository. Every command that
// resolves a repository through discoverRepo shares this one message.
func TestDiscoverRepoNotFoundNamesTheFix(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("NOAHSARK_REPO", "")

	_, err := discoverRepo("")
	if err == nil {
		t.Fatal("discoverRepo(\"\") in an empty directory: expected an error")
	}
	if !strings.Contains(err.Error(), "no noahsark repository found") {
		t.Fatalf("error = %q, want the no-repository wording", err)
	}
	if !strings.Contains(err.Error(), "--repo=DIR") ||
		!strings.Contains(err.Error(), "NOAHSARK_REPO") ||
		!strings.Contains(err.Error(), "run from inside the repository") {
		t.Fatalf("error = %q, want it to name all three fixes", err)
	}
}
