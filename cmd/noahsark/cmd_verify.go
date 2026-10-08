package main

import (
	"errors"
	"flag"
	"fmt"
)

const verifyUsage = "verify [--no-mark] DISC-ROOT\nverify --undo DISC"

func init() {
	register(&command{
		name:    "verify",
		usage:   verifyUsage,
		summary: "Check a disc root, and record the check of a counted mount; or remove a verified record with --undo.",
		flags:   verifyFlags,
	})
}

// verifyOptions holds the command options of verify.
type verifyOptions struct {
	noMark bool
	undo   bool
}

func verifyFlags(fs *flag.FlagSet) runFunc {
	o := &verifyOptions{}
	fs.BoolVar(&o.noMark, "no-mark", false, "check the disc and write nothing")
	fs.BoolVar(&o.undo, "undo", false, "remove the verified record of a verified disc")
	return o.run
}

// Texts of the line after the ok line or the bad line.
const (
	notCountedDisc   = "not counted: this is not a disc"
	notCountedNoRepo = "not counted: no repository"
	notMarked        = "not marked"
)

// Reasons of a failed check that records nothing. The detail of the
// check goes to standard error.
const (
	reasonDiscRootDamaged   = "the disc root is damaged"
	reasonPackedTreeDamaged = "the packed tree is damaged"
)

// run implements "noahsark verify". docs/states.md, rows 31 to 51, gives
// the lines.
func (o *verifyOptions) run(e *env, args []string) int {
	stderr := e.stderr
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark verify [--no-mark] DISC-ROOT")
		_, _ = fmt.Fprintln(stderr, "       noahsark verify --undo DISC")
		return 2
	}
	if o.undo {
		if o.noMark {
			_, _ = fmt.Fprintln(stderr, "noahsark: verify: --undo takes no other option")
			return 2
		}
		return undoVerify(e, args[0])
	}
	repoDir, err := e.findRepo()
	switch {
	case errors.Is(err, errNoRepo):
		repoDir = ""
	case err != nil:
		_, _ = fmt.Fprintf(stderr, "noahsark: verify: %v\n", err)
		return 2
	}
	var cfg repoConfig
	if repoDir != "" {
		if cfg, err = readConfig(configPath(repoDir)); err != nil {
			_, _ = fmt.Fprintf(stderr, "noahsark: verify: %v\n", err)
			return configExitCode(err)
		}
	}

	root := args[0]
	ident, err := readDiscIdentity(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: verify: %s: cannot read the disc: %v\n", root, err)
		return 1
	}
	if repoDir == "" {
		return o.verifyWithoutRepo(e, root, ident)
	}
	return o.verifyInRepo(e, repoDir, layoutOf(repoDir, cfg), root, ident)
}
