package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadConfigRefusesForceCapacity checks that the config loader
// refuses a disc.force_capacity line by name, rather than parsing it,
// since capacity is no longer a repository setting: every pack gives
// its own --capacity.
func TestReadConfigRefusesForceCapacity(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	configFile := configPath(repo)
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("disc.force_capacity = 12219392\n")...)
	if err := os.WriteFile(configFile, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = readConfig(configFile)
	if err == nil {
		t.Fatal("readConfig: want an error for disc.force_capacity, got none")
	}
	if !strings.Contains(err.Error(), "pack --capacity") {
		t.Fatalf("readConfig error = %q, want it to point at pack --capacity", err.Error())
	}
}
