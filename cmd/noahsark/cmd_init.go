package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// cmdInit implements "noahsark init". It takes no --capacity: the
// operator gives the capacity to pack, or puts pack.capacity in the
// config. --source stores sources.root, so a later commit with no SOURCE
// on its own command line can read it. It holds one path, since
// Writer.Commit takes one source directory. See docs/decisions.md,
// "Commit".
func cmdInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark init [--repo=PATH] [--source=PATH]",
		"Create a new, empty repository directory.", stderr)
	repoPath := fs.String("repo", ".", "repository directory to create")
	sourcePath := fs.String("source", "", "source root to store in the config; commit uses it when SOURCE is omitted")
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

	var absSourcePath string
	if *sourcePath != "" {
		absSourcePath, err = filepath.Abs(*sourcePath)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
			return 2
		}
	}

	if isRepoDir(absRepoPath) {
		_, _ = fmt.Fprintf(stderr, "noahsark: init: %s is already a noahsark repository\n", absRepoPath)
		return 2
	}

	if err := os.MkdirAll(absRepoPath, 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}

	lk, code, ok := lockRepo("init", absRepoPath, stderr)
	if !ok {
		return code
	}
	defer releaseLock(lk)

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
		SourceRoot: absSourcePath,
	}
	if err := writeConfig(filepath.Join(absRepoPath, configFileName), cfg); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "initialized repository %s\n", absRepoPath)
	_, _ = fmt.Fprintf(stdout, "staging: %s\n", stagingDir)
	if absSourcePath != "" {
		_, _ = fmt.Fprintf(stdout, "source: %s\n", absSourcePath)
	}
	return 0
}
