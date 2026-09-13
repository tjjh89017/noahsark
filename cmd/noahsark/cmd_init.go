package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// cmdInit implements "noahsark init". It reduces OPERATIONS.md's init
// flags to --repo and --capacity: every other init flag exists to choose
// among alternatives (hash algorithm, chunker profile, filesystem
// profile, locality preset) that this build fixes to one value, or to
// recover sequence numbers from existing discs, which no multi-disc
// state exists yet to scan. See docs/decisions.md, "16. CLI reference".
func cmdInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repoPath := fs.String("repo", ".", "repository directory to create")
	capacityStr := fs.String("capacity", "", "default target capacity for pack (sectors, or e.g. 25GB)")
	if err := fs.Parse(args); err != nil {
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

	var capacitySectors uint64
	if *capacityStr != "" {
		capacitySectors, err = parseCapacity(*capacityStr)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
			return 2
		}
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
		RepoUUID:             hex.EncodeToString(uuidBytes),
		StagingDir:           stagingDir,
		ForceCapacitySectors: capacitySectors,
	}
	if err := writeConfig(filepath.Join(absRepoPath, configFileName), cfg); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "initialized repository %s\n", absRepoPath)
	_, _ = fmt.Fprintf(stdout, "staging: %s\n", stagingDir)
	return 0
}
