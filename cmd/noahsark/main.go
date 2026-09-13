// Command noahsark is the NoahsArk command-line tool. This build
// implements the Phase 1 subset of OPERATIONS.md's CLI reference: init,
// commit, pack, image build, verify and restore. Every other command
// name, and every flag or config key of a later phase, is refused.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one command and returns the process exit code: 0 for
// success, 1 for a failure the command reports cleanly, 2 for a usage
// error, including a refused later-phase name.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	if args[0] == "-h" || args[0] == "--help" {
		printUsage(stdout)
		return 0
	}

	cmd := args[0]
	rest := args[1:]

	if phase, ok := laterPhaseCommands[cmd]; ok {
		fmt.Fprintf(stderr, "noahsark: %s is a %s command; not available in Phase 1\n", cmd, phase)
		return 2
	}

	switch cmd {
	case "init":
		return cmdInit(rest, stdout, stderr)
	case "commit":
		return cmdCommit(rest, stdout, stderr)
	case "pack":
		return cmdPack(rest, stdout, stderr)
	case "image":
		return cmdImage(rest, stdout, stderr)
	case "verify":
		return cmdVerify(rest, stdout, stderr)
	case "restore":
		return cmdRestore(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "noahsark: unknown command %q\n", cmd)
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `usage: noahsark <command> [arguments]

Phase 1 commands:
  init    SOURCE-less setup of a repository directory
  commit  SOURCE [--ref=NAME]
  pack    [--ref=NAME | --snapshot=ID]... --capacity=N [--label=TEXT] [--media=NAME] [--out=DIR]
  image build --out=FILE [--capacity=N] TREE-DIR
  verify  --image=PATH [--heal] [--out=DIR]
  restore DISC-ROOT SNAPSHOT OUT-DIR

Run "noahsark <command> -h" for a command's own flags.
`)
}

// laterPhaseCommands names every OPERATIONS.md command that this build
// does not implement because it belongs to a later phase.
var laterPhaseCommands = map[string]string{
	"sync":        "Phase 2",
	"append":      "Phase 2",
	"close":       "Phase 2",
	"watch":       "Phase 3",
	"consolidate": "Phase 3",
	"reindex":     "Phase 3",
	"catalog":     "Backlog",
	"import":      "Backlog",
}

// refuseLaterPhaseFlags scans args for any flag name later than Phase 1,
// per the table for this command, and reports the first one it finds.
// It runs before flag.Parse so the message names the phase, not just
// "flag provided but not defined".
func refuseLaterPhaseFlags(cmd string, args []string, stderr io.Writer) bool {
	table := laterPhaseFlags[cmd]
	for _, a := range args {
		name := a
		if i := indexByte(a, '='); i >= 0 {
			name = a[:i]
		}
		if phase, ok := table[name]; ok {
			fmt.Fprintf(stderr, "noahsark: %s: %s is a %s option; not available in Phase 1\n", cmd, name, phase)
			return true
		}
	}
	return false
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// laterPhaseFlags names every later-phase flag OPERATIONS.md lists for a
// Phase 1 command. Every other flag OPERATIONS.md lists for these
// commands is a Phase 1 option this build does not implement yet; it is
// simply not defined, so passing it is a plain usage error. See
// docs/decisions.md, "16. CLI reference".
var laterPhaseFlags = map[string]map[string]string{
	"commit": {
		"--from":       "Phase 2",
		"--copy-first": "Phase 2",
		"--out":        "Backlog",
		"--catalog":    "Backlog",
	},
	"pack": {
		"--disc": "Phase 2",
	},
	"restore": {
		"--no-xattr":      "Phase 2",
		"--no-acl":        "Phase 2",
		"--translate-acl": "Phase 2",
	},
}
