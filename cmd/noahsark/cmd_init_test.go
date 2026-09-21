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

// TestReadConfigRefusesRemovedKeys checks that a key this build used to
// read, and no longer does, gets the same unknown-key refusal as any
// other key it has never read.
func TestReadConfigRefusesRemovedKeys(t *testing.T) {
	for _, key := range []string{
		"commit.restat_after_read",
		"commit.retry_unstable",
		"sources.exclude",
		"staging.retain_after_clean",
	} {
		t.Run(key, func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "repo")
			if code, out := runCmd(t, "init", "--repo="+repo); code != 0 {
				t.Fatalf("init: exit %d: %s", code, out)
			}
			configFile := configPath(repo)
			data, err := os.ReadFile(configFile)
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, []byte(key+" = true\n")...)
			if err := os.WriteFile(configFile, data, 0o644); err != nil {
				t.Fatal(err)
			}

			_, err = readConfig(configFile)
			if err == nil {
				t.Fatalf("readConfig: want an error for removed key %s, got none", key)
			}
			if !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), key) {
				t.Fatalf("readConfig error = %q, want it to call %s an unknown key", err.Error(), key)
			}
		})
	}
}
