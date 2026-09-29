package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func init() {
	register(&command{
		name:    "init",
		usage:   "init [--source=PATH]",
		summary: "Make the current directory a new, empty repository.",
		flags:   initFlags,
	})
}

// initOptions holds the command options of init.
type initOptions struct {
	source string
}

func initFlags(fs *flag.FlagSet) runFunc {
	o := &initOptions{}
	fs.StringVar(&o.source, "source", "", "source root to store in the config; commit uses it when SOURCE is omitted")
	return o.run
}

// run implements "noahsark init". It makes the current directory the
// repository, and it does not use repository discovery. It takes no
// --capacity: the operator gives the capacity to each pack. --source
// stores sources.root, so a later
// commit with no SOURCE on its own command line can read it. It holds
// one path, since Writer.Commit takes one source directory. See
// docs/decisions.md, "Commit".
func (o *initOptions) run(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	if e.global.repo != "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: init makes the current directory the repository; it does not take --repo")
		return 2
	}
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark init [--source=PATH]")
		return 2
	}

	absRepoPath, err := e.getwd()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}

	var absSourcePath string
	if o.source != "" {
		absSourcePath, err = e.abs(o.source)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
			return 2
		}
	}

	if isRepoDir(absRepoPath) {
		_, _ = fmt.Fprintf(stderr, "noahsark: init: %s is already a noahsark repository\n", absRepoPath)
		return 2
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

	var repoUUID [16]byte
	if _, err := rand.Read(repoUUID[:]); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}

	if err := writeConfig(configPath(absRepoPath), newConfigFile(repoUUID, absSourcePath)); err != nil {
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
