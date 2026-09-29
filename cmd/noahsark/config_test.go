package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenRepoUUID is the repo.uuid of testdata/config.golden.yaml.
var goldenRepoUUID = [16]byte{0x0f, 0x1e, 0x2d, 0x3c, 0x4b, 0x5a, 0x69, 0x78, 0x87, 0x96, 0xa5, 0xb4, 0xc3, 0xd2, 0xe1, 0xf0}

// writeConfigText writes text as the config file of a new repository
// directory and returns the config file path.
func writeConfigText(t *testing.T, text string) string {
	t.Helper()
	path := configPath(t.TempDir())
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestWriteConfigGolden writes a config file and compares its bytes to
// the checked-in golden file. It then reads the golden file and compares
// each key.
func TestWriteConfigGolden(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "config.golden.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	path := configPath(t.TempDir())
	if err := writeConfig(path, newConfigFile(goldenRepoUUID, "/srv/data")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("written config:\n%s\nwant:\n%s", got, want)
	}

	f, err := decodeConfig(want)
	if err != nil {
		t.Fatal(err)
	}
	if f != newConfigFile(goldenRepoUUID, "/srv/data") {
		t.Fatalf("decoded golden config = %+v", f)
	}
}

// TestWriteConfigReplacesTheFile writes a config file over an old one.
// The new text replaces the old text, and no temporary file stays in
// the directory.
func TestWriteConfigReplacesTheFile(t *testing.T) {
	path := writeConfigText(t, "old text that is not yaml: [\n")
	if err := writeConfig(path, newConfigFile(goldenRepoUUID, "")); err != nil {
		t.Fatal(err)
	}
	cfg, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RepoUUID != "0f1e2d3c4b5a69788796a5b4c3d2e1f0" {
		t.Fatalf("RepoUUID = %q", cfg.RepoUUID)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != configFileName {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want only %s", names, configFileName)
	}
}

// TestReadConfigKeys reads a file that gives each key.
func TestReadConfigKeys(t *testing.T) {
	path := writeConfigText(t, `repo:
  uuid: 0F1E2D3C4B5A69788796A5B4C3D2E1F0
staging:
  dir: /var/noahsark/staging
sources:
  root: /srv/data
pack:
  device: /dev/sr1
`)
	cfg, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := repoConfig{
		RepoUUID:   "0F1E2D3C4B5A69788796A5B4C3D2E1F0",
		StagingDir: "/var/noahsark/staging",
		SourceRoot: "/srv/data",
		PackDevice: "/dev/sr1",
	}
	if cfg != want {
		t.Fatalf("readConfig = %+v, want %+v", cfg, want)
	}
}

// TestReadConfigDefaults reads a file that gives repo.uuid only. Each
// other key takes its default, and a relative staging.dir is relative to
// the repository directory.
func TestReadConfigDefaults(t *testing.T) {
	path := writeConfigText(t, "repo:\n  uuid: 0f1e2d3c4b5a69788796a5b4c3d2e1f0\n")
	cfg, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := repoConfig{
		RepoUUID:   "0f1e2d3c4b5a69788796a5b4c3d2e1f0",
		StagingDir: filepath.Join(filepath.Dir(path), "staging"),
		SourceRoot: "",
		PackDevice: "/dev/sr0",
	}
	if cfg != want {
		t.Fatalf("readConfig = %+v, want %+v", cfg, want)
	}
}

// TestReadConfigRefusesUnknownKey checks that an unknown key is an error
// that names the key in its dotted form. The keys fec.scheme,
// pack.capacity and gc.min_verified_copies are unknown keys too.
func TestReadConfigRefusesUnknownKey(t *testing.T) {
	const head = "repo:\n  uuid: 0f1e2d3c4b5a69788796a5b4c3d2e1f0\n"
	for key, text := range map[string]string{
		"fec.scheme":             head + "fec:\n  scheme: none\n",
		"pack.capacity":          head + "pack:\n  capacity: bd25\n",
		"gc.min_verified_copies": head + "gc:\n  min_verified_copies: 1\n",
		"repo.bogus":             head + "  bogus: 1\n",
		"nonsense":               head + "nonsense: 1\n",
	} {
		t.Run(key, func(t *testing.T) {
			path := writeConfigText(t, text)
			_, err := readConfig(path)
			if err == nil {
				t.Fatal("readConfig: no error")
			}
			if !isConfigError(err) {
				t.Fatalf("readConfig error %v is not a config error", err)
			}
			if !strings.Contains(err.Error(), "unknown key "+key) || !strings.Contains(err.Error(), path) {
				t.Fatalf("readConfig error = %q, want it to name the key %s and the file", err, key)
			}
		})
	}
}

// TestReadConfigRefusesABadFile checks the faults of the file itself.
func TestReadConfigRefusesABadFile(t *testing.T) {
	const head = "repo:\n  uuid: 0f1e2d3c4b5a69788796a5b4c3d2e1f0\n"
	for name, text := range map[string]string{
		"malformed":       "repo: [\n",
		"two documents":   head + "---\n" + head,
		"empty":           "",
		"wrong type":      head + "staging:\n  dir: [a, b]\n",
		"section scalar":  "repo: 1\n",
		"duplicate key":   head + head,
		"no uuid":         "staging:\n  dir: staging\n",
		"short uuid":      "repo:\n  uuid: 0f1e2d3c\n",
		"not hex uuid":    "repo:\n  uuid: 0f1e2d3c4b5a69788796a5b4c3d2e1fz\n",
		"hyphenated uuid": "repo:\n  uuid: 0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := readConfig(writeConfigText(t, text))
			if err == nil {
				t.Fatal("readConfig: no error")
			}
			if !isConfigError(err) {
				t.Fatalf("readConfig error %v is not a config error", err)
			}
		})
	}
}

// TestConfigFaultExitsTwo runs commands on a repository whose config has
// an unknown key. Each command exits with code 2 and names the key.
func TestConfigFaultExitsTwo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	// init writes the pack section last, so this line adds a key to it.
	appendConfig(t, repo, "  capacity: bd25\n")

	for _, args := range [][]string{
		{"status"},
		{"gc", "--dry-run"},
		{"pack", "--capacity=64MiB"},
		{"commit", writeFixtureSource(t)},
		{"disc", "burned", "0"},
	} {
		code, out := runCmd(t, append([]string{"--repo=" + repo}, args...)...)
		if code != 2 || !strings.Contains(out, "unknown key pack.capacity") {
			t.Fatalf("%v: exit %d: %s, want exit 2 and the unknown key", args, code, out)
		}
	}
}
