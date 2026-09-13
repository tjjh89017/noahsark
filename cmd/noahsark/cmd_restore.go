package main

import (
	"fmt"
	"io"

	"github.com/tjjh89017/noahsark/internal/restore"
)

// cmdRestore implements "noahsark restore". OPERATIONS.md's
// "restore SNAPSHOT TARGET" resolves SNAPSHOT through a repository's
// catalog and cache, which this build does not keep; instead it takes
// the disc root directly, alongside the snapshot id and the output
// directory. See docs/decisions.md, "16. CLI reference".
func cmdRestore(args []string, stdout, stderr io.Writer) int {
	if refuseLaterPhaseFlags("restore", args, stderr) {
		return 2
	}
	if len(args) != 3 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark restore DISC-ROOT SNAPSHOT OUT-DIR")
		return 2
	}
	discRoot, snapshotArg, outDir := args[0], args[1], args[2]

	snapID, err := parseSnapshotID(snapshotArg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}

	if err := restore.Restore(discRoot, snapID, outDir); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "restored snapshot %s into %s\n", snapID.TextForm(), outDir)
	return 0
}
