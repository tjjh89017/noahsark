package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadConfigRefusesUnknownKey checks that the config loader refuses
// an unknown key by name, naming both the key and the config file, since
// capacity is no longer a repository setting: every pack gives its own
// --capacity.
func TestReadConfigRefusesUnknownKey(t *testing.T) {
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
		t.Fatal("readConfig: want an error for an unknown key, got none")
	}
	if !strings.Contains(err.Error(), "disc.force_capacity") || !strings.Contains(err.Error(), configFile) {
		t.Fatalf("readConfig error = %q, want it to name the key and the file", err.Error())
	}
}
