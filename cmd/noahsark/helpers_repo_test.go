package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// repoCatalogDir resolves the catalog directory the same way pack and
// recover do.
func repoCatalogDir(t *testing.T, repo string) string {
	t.Helper()
	return catalog.Dir(repo)
}

// testLayout returns the layout of the repository at repo, from its
// config. A test takes every path of a repository from it.
func testLayout(t *testing.T, repo string) repoLayout {
	t.Helper()
	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	return layoutOf(repo, cfg)
}

// openTestLog opens the state log of the repository at repo.
func openTestLog(t *testing.T, repo string) *stage.Log {
	t.Helper()
	l, err := stage.Open(testLayout(t, repo).stateDir())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// listFilesUnder returns the path of every regular file below dir,
// relative to dir, sorted. A missing dir gives no file.
func listFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == dir {
				return filepath.SkipDir
			}
			return err
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	return files
}

// countByState opens repo's state log and counts every object
// currently in state.
func countByState(t *testing.T, repo string, state stage.State) int {
	t.Helper()
	l, err := stage.Open(testLayout(t, repo).stateDir())
	if err != nil {
		t.Fatal(err)
	}
	return l.CountState(state)
}

// countFiles counts the regular files under dir, recursively.
func countFiles(dir string) (int, error) {
	n := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	return n, err
}

// partFilesUnder returns every part file below dir, by path.
func partFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.Contains(d.Name(), ".noahsark-part") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return out
}

// statusDiscs reads repo's disc summaries the same way "status"
// computes them, straight through the internal packages: there is no
// --json to shell out through and parse.
func statusDiscs(t *testing.T, repo string) []discSummary {
	t.Helper()
	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		t.Fatalf("decodeUUID: %v", err)
	}
	layout := layoutOf(repo, cfg)
	ledger, err := image.LoadDiscsLedger(layout.discsLedgerFile(), repoUUID)
	if err != nil {
		t.Fatalf("LoadDiscsLedger: %v", err)
	}
	return summarizeDiscs(ledger.Rows, readLogs(t, repo))
}

// readLogs replays the item log and the disc state log of the
// repository at repo, read-only.
func readLogs(t *testing.T, repo string) *stage.Logs {
	t.Helper()
	logs, err := stage.OpenLogs(testLayout(t, repo).stateDir(), false)
	if err != nil {
		t.Fatalf("stage.OpenLogs: %v", err)
	}
	return logs
}

// discState returns the record of the disc uuidText in the disc state
// log of repo.
func discState(t *testing.T, repo, uuidText string) stage.DiscInfo {
	t.Helper()
	u, err := decodeUUID(strings.ReplaceAll(uuidText, "-", ""))
	if err != nil {
		t.Fatalf("bad disc uuid %q: %v", uuidText, err)
	}
	d, _ := readDiscLog(t, repo).Disc(u)
	return d
}

// itemWords counts the derived words of the items whose newest record
// names the disc uuidText in repo.
func itemWords(t *testing.T, repo, uuidText string) map[stage.ItemWord]int {
	t.Helper()
	u, err := decodeUUID(strings.ReplaceAll(uuidText, "-", ""))
	if err != nil {
		t.Fatalf("bad disc uuid %q: %v", uuidText, err)
	}
	logs := readLogs(t, repo)
	words := make(map[stage.ItemWord]int)
	for _, id := range logs.Items.ItemsOfDisc(u) {
		w, _ := logs.Word(id)
		words[w]++
	}
	return words
}

// readDiscLog replays the disc state log of the repository at repo.
func readDiscLog(t *testing.T, repo string) *stage.DiscLog {
	t.Helper()
	l, err := stage.OpenDiscLogReadOnly(testLayout(t, repo).stateDir())
	if err != nil {
		t.Fatalf("stage.OpenDiscLogReadOnly: %v", err)
	}
	return l
}

// appendConfig adds YAML text to the end of the config file of repo.
func appendConfig(t *testing.T, repo, text string) {
	t.Helper()
	f, err := os.OpenFile(configPath(repo), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeFile writes content to path, creating its parent directories.
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// statusNextLines returns the advice lines and the next block that
// status prints now for the repository repo.
func statusNextLines(t *testing.T, repo string) []string {
	t.Helper()
	te := newTestEnv(t.TempDir())
	_, _ = te.run("--repo="+repo, "status")
	lines := strings.Split(strings.TrimSuffix(te.out.String(), "\n"), "\n")
	i := slices.IndexFunc(lines, func(l string) bool {
		return strings.HasPrefix(l, "advice: ") || strings.HasPrefix(l, "next: ")
	})
	if i < 0 {
		t.Fatalf("status of %s printed no next block: %q", repo, te.out.String())
	}
	return lines[i:]
}

// wantNextBlock checks that out holds, from the start of a line, the
// advice lines and the next block that status prints now for repo, as
// the last lines of standard output. Standard error can follow them in
// out.
func wantNextBlock(t *testing.T, repo, out string) {
	t.Helper()
	want := strings.Join(statusNextLines(t, repo), "\n") + "\n"
	if !strings.HasPrefix(out, want) && !strings.Contains(out, "\n"+want) {
		t.Errorf("output %q does not hold the next block of status %q", out, want)
	}
}
