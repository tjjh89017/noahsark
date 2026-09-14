package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRestorePlanFile runs "plan --out=FILE" against repo for snapID
// and returns the file path.
func writeRestorePlanFile(t *testing.T, repo, snapID string) string {
	t.Helper()
	planFile := filepath.Join(t.TempDir(), "plan.json")
	if code, out := runCmd(t, "plan", "--repo="+repo, "--out="+planFile, snapID); code != 0 {
		t.Fatalf("plan --out: exit %d: %s", code, out)
	}
	return planFile
}

// TestPlanJSONHasRepoUUIDAndCreated checks the two fields "restore
// --plan" needs to confirm a persisted plan against the repository it
// resumes against.
func TestPlanJSONHasRepoUUIDAndCreated(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, _, _ := multiDiscPlanFixture(t)
	planFile := writeRestorePlanFile(t, repo, snapID)

	data, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		RepoUUID string `json:"repo_uuid"`
		Created  string `json:"created"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal plan JSON: %v\n%s", err, data)
	}
	if doc.RepoUUID == "" {
		t.Fatalf("plan JSON repo_uuid is empty")
	}
	if doc.Created == "" {
		t.Fatalf("plan JSON created is empty")
	}
}

// TestRestorePlanFileResumesPersistedPlan drives a two-disc disc-swap
// restore from a plan file "plan --out" wrote, instead of letting
// restore build its own, and checks the result matches the source tree.
func TestRestorePlanFileResumesPersistedPlan(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, src, discRoots := discSwapFixture(t)
	seqs := planOrderDiscSeqs(t, "--repo="+repo, snapID)
	if len(seqs) != 2 {
		t.Fatalf("plan named %d disc(s), want 2", len(seqs))
	}
	planFile := writeRestorePlanFile(t, repo, snapID)

	mountDir := filepath.Join(t.TempDir(), "mount")
	mountDisc(t, mountDir, discRoots[seqs[0]])
	setRestoreStdin(t, &scriptedStdin{steps: []func(){
		func() { mountDisc(t, mountDir, discRoots[seqs[1]]) },
	}})

	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--plan="+planFile, "--mount="+mountDir, outDir)
	if code != 0 {
		t.Fatalf("restore --plan: exit %d: %s", code, out)
	}
	compareTrees(t, filepath.Join(outDir, src), src)
}

// TestRestorePlanFileRefusesWrongRepo points --plan at a plan file
// written for a different repository's uuid and checks restore refuses
// it, naming the mismatch, instead of resuming against the wrong cache.
func TestRestorePlanFileRefusesWrongRepo(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, _, _ := discSwapFixture(t)
	planFile := writeRestorePlanFile(t, repo, snapID)

	data, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatal(err)
	}
	// Force a mismatch by overwriting repo_uuid's value with all zeros,
	// a well-formed but wrong uuid.
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["repo_uuid"] = "00000000-0000-0000-0000-000000000000"
	tamperedBytes, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planFile, tamperedBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	mountDir := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--plan="+planFile, "--mount="+mountDir, outDir)
	if code != 2 {
		t.Fatalf("restore --plan (wrong repo): exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "is for repository") {
		t.Fatalf("restore --plan (wrong repo) output %q missing the mismatch message", out)
	}
}

// TestRestorePlanFileRejectsIncludeCombo checks --include cannot be
// combined with --plan, since the plan file already fixes the include
// list.
func TestRestorePlanFileRejectsIncludeCombo(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	repo, snapID, _, _ := discSwapFixture(t)
	planFile := writeRestorePlanFile(t, repo, snapID)

	mountDir := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "out")
	code, out := runCmd(t, "restore", "--repo="+repo, "--plan="+planFile, "--include=sub0", "--mount="+mountDir, outDir)
	if code != 2 {
		t.Fatalf("restore --plan --include: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "--include cannot be combined with --plan") {
		t.Fatalf("restore --plan --include output %q missing the refusal", out)
	}
}
