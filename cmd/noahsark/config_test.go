package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigRefusesMinVerifiedCopiesBelowOne checks that a value below 1
// is a config error, not a silent "delete at once" setting.
func TestConfigRefusesMinVerifiedCopiesBelowOne(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfigLine(t, repo, "gc.min_verified_copies = 0")

	code, out := runCmd(t, "--repo="+repo, "gc", "--dry-run")
	if code != 2 {
		t.Fatalf("gc with gc.min_verified_copies = 0: exit %d, want 2: %s", code, out)
	}
	if !strings.Contains(out, "gc.min_verified_copies: must be at least 1") {
		t.Fatalf("gc output %q, want the config error", out)
	}
}

// TestReadConfigRefusesSecondSourceRoot checks that a config file that
// repeats sources.root stops commit, which reads the key, and leaves
// status, which does not, working.
func TestReadConfigRefusesSecondSourceRoot(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init", "--source="+writeFixtureSource(t)); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	configFile := configPath(repo)
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("sources.root = /another/root\n")...)
	if err := os.WriteFile(configFile, data, 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := runCmd(t, "--repo="+repo, "commit")
	if code != 2 || !strings.Contains(out, "only one source root") {
		t.Fatalf("commit: exit %d: %s, want exit 2 and the repeated sources.root fault", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "status"); code != 0 {
		t.Fatalf("status: exit %d: %s, want status to run with a key it never reads", code, out)
	}
}

// TestReadConfigStillRefusesUnknownKey checks that adding sources.root
// to the known-key set did not loosen the loader's refusal of an
// unrelated unknown key.
func TestReadConfigStillRefusesUnknownKey(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}

	configFile := configPath(repo)
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("sources.bogus = xyz\n")...)
	if err := os.WriteFile(configFile, data, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = readConfig(configFile)
	if err == nil {
		t.Fatal("readConfig: want an error for sources.bogus, got none")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("readConfig error = %q, want it to say unknown key", err.Error())
	}
}

// TestReadConfigRefusesUnknownKey checks that the config loader refuses
// an unknown key by name, naming both the key and the config file, since
// capacity is no longer a repository setting: every pack gives its own
// --capacity.
func TestReadConfigRefusesUnknownKey(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
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
			if code, out := runIn(t, repo, "init"); code != 0 {
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

// TestUnknownConfigKeyStopsEveryCommand checks that the loader still
// refuses a key it does not know, whichever command reads the config.
func TestUnknownConfigKeyStopsEveryCommand(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	appendConfig(t, repo, "nonsense.key = 1\n")
	if code, out := runCmd(t, "--repo="+repo, "status"); code != 2 || !strings.Contains(out, "unknown key") {
		t.Fatalf("status: exit %d: %s, want exit 2 for an unknown key", code, out)
	}
}
