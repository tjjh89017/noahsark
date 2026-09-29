package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"strings"
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

	var repoUUID [16]byte
	if _, err := rand.Read(repoUUID[:]); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}

	if err := writeConfig(configPath(absRepoPath), newConfigFile(repoUUID, absSourcePath)); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}
	cfg, err := readConfig(configPath(absRepoPath))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return configExitCode(err)
	}
	if err := makeRepoLayout(layoutOf(absRepoPath, cfg)); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: init:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "initialized repository %s\n", absRepoPath)
	if absSourcePath != "" {
		_, _ = fmt.Fprintf(stdout, "source: %s\n", absSourcePath)
	}
	_, _ = fmt.Fprintf(stdout, "device: %s\n", cfg.PackDevice)
	_, _ = fmt.Fprintln(stdout, nextStatusLine)
	return 0
}

// makeRepoLayout creates the directories of a repository and writes its
// .gitignore. It keeps each directory and file that exists. init and
// recover call it.
func makeRepoLayout(l repoLayout) error {
	for _, dir := range []string{l.stateDir(), l.catalogDir(), l.chunksDir(), l.plansDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return ensureGitignore(l.gitignoreFile())
}

// ensureGitignore writes the file at path with the lines of
// gitignoreLines. It never overwrites a file that exists: it appends
// only each line that the file lacks.
func ensureGitignore(path string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	have := make(map[string]bool)
	for line := range strings.Lines(string(data)) {
		have[strings.TrimSpace(line)] = true
	}
	var add strings.Builder
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		add.WriteString("\n")
	}
	missing := false
	for _, line := range gitignoreLines {
		if !have[line] {
			add.WriteString(line + "\n")
			missing = true
		}
	}
	if !missing {
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(add.String()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
