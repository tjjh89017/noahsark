package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// cmdInit implements "noahsark init". It reduces OPERATIONS.md's init
// flags to --repo: every other init flag exists to choose among
// alternatives (hash algorithm, chunker profile, filesystem profile,
// locality preset) that this build fixes to one value, or to recover
// sequence numbers from existing discs, which no multi-disc state
// exists yet to scan. Capacity is not among them: a disc's capacity is
// a per-disc value chosen at pack time, not a repository setting, so
// init takes no --capacity and the config carries no capacity default.
// See docs/decisions.md, "16. CLI reference".
func cmdInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark init [--repo=PATH]",
		"Create a new, empty repository directory.", stderr)
	repoPath := fs.String("repo", ".", "repository directory to create")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("init", fs, stderr) {
		return 2
	}

	absRepoPath, err := filepath.Abs(*repoPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 2
	}

	if isRepoDir(absRepoPath) {
		_, _ = fmt.Fprintf(stderr, "noahsark: init: %s is already a noahsark repository\n", absRepoPath)
		return 2
	}

	if err := os.MkdirAll(absRepoPath, 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}

	stagingDir := filepath.Join(absRepoPath, "staging")
	if err := os.MkdirAll(filepath.Join(stagingDir, "objects"), 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Join(stagingDir, "snapshots"), 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}

	uuidBytes := make([]byte, 16)
	if _, err := rand.Read(uuidBytes); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}

	cfg := repoConfig{
		RepoUUID: hex.EncodeToString(uuidBytes),
		// Written relative to the repository directory, so the staging
		// store still follows the repository if its directory is later
		// renamed or moved; readConfig resolves it back to an absolute
		// path against the config file's own directory.
		StagingDir: "staging",
	}
	if err := writeConfig(filepath.Join(absRepoPath, configFileName), cfg); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "initialized repository %s\n", absRepoPath)
	_, _ = fmt.Fprintf(stdout, "staging: %s\n", stagingDir)
	return 0
}
