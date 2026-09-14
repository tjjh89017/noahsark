package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/restore"
)

// cmdRestore implements "noahsark restore". OPERATIONS.md's
// "restore SNAPSHOT TARGET" resolves SNAPSHOT through a repository's
// catalog and cache, which this build does not keep; instead it takes
// one or more disc roots directly, alongside the snapshot argument and
// the output directory. See docs/decisions.md, "16. CLI reference" and
// "14. Restore".
//
// SNAPSHOT accepts a snapshot id or a ref name, resolved the same way
// ls and log resolve it, against the REFS table of the given discs.
//
// A restore that needs only one disc keeps the old positional form,
// "restore DISC-ROOT SNAPSHOT OUT-DIR". A restore spanning several discs
// repeats --disc, or names a directory of mounted discs with
// --discs-dir; either way SNAPSHOT and OUT-DIR are then the only
// positional arguments.
func cmdRestore(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if refuseLaterPhaseFlags("restore", args, stderr) {
		return 2
	}

	fs := newFlagSet("noahsark restore [--include=PATH]... [--overwrite] DISC-ROOT SNAPSHOT OUT-DIR",
		"Restore a snapshot to a directory. Accepts --disc (repeatable) or --discs-dir in place of DISC-ROOT.", stderr)
	var discFlags stringList
	fs.Var(&discFlags, "disc", "a disc root to restore from; repeatable")
	discsDir := fs.String("discs-dir", "", "a directory whose immediate subdirectories are mounted disc roots")
	var includeFlags stringList
	fs.Var(&includeFlags, "include", "restore only this snapshot-relative path and, if it names a directory, everything under it; repeatable")
	overwrite := fs.Bool("overwrite", false, "unlink an existing path first and then create it; without this, an existing path is left alone")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("restore", fs, stderr) {
		return 2
	}

	var discRoots, positional []string
	multi := len(discFlags) > 0 || *discsDir != ""
	switch {
	case multi:
		if fs.NArg() != 2 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--include=PATH]... [--overwrite] --disc=ROOT [--disc=ROOT]... SNAPSHOT OUT-DIR")
			return 2
		}
		positional = fs.Args()
	default:
		if fs.NArg() != 3 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--include=PATH]... [--overwrite] DISC-ROOT SNAPSHOT OUT-DIR")
			return 2
		}
		positional = fs.Args()[1:]
	}
	var err error
	discRoots, err = resolveDiscRoots(discFlags, *discsDir, fs.Args())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	snapshotArg, outDir := positional[0], positional[1]

	src, err := restore.OpenSource(discRoots)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	snapID, err := src.ParseSnapshotArg(snapshotArg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}

	opts := []restore.Option{restore.WithInclude(includeFlags), restore.WithOverwrite(*overwrite)}
	skipped, err := restore.RestoreMultiWithProgress(discRoots, snapID, outDir, prog, opts...)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "restored snapshot %s into %s\n", snapID.TextForm(), outDir)
	if skipped > 0 {
		_, _ = fmt.Fprintf(stdout, "skipped %d existing path(s); pass --overwrite to replace them\n", skipped)
		return 1
	}
	return 0
}

// resolveDiscRoots builds the disc root list restore reads from: the
// single positional DISC-ROOT when neither --disc nor --discs-dir is
// given, else every --disc value followed by every immediate
// subdirectory of --discs-dir.
func resolveDiscRoots(discFlags stringList, discsDir string, positional []string) ([]string, error) {
	if len(discFlags) == 0 && discsDir == "" {
		if len(positional) == 0 {
			return nil, fmt.Errorf("no disc root given")
		}
		return []string{positional[0]}, nil
	}

	roots := append([]string(nil), discFlags...)
	if discsDir != "" {
		entries, err := os.ReadDir(discsDir)
		if err != nil {
			return nil, fmt.Errorf("--discs-dir %s: %w", discsDir, err)
		}
		for _, e := range entries {
			path := filepath.Join(discsDir, e.Name())
			isDir := e.IsDir()
			if !isDir && e.Type()&os.ModeSymlink != 0 {
				// A symlinked disc root (a mounted image linked in
				// under --discs-dir) reports as a symlink, not a
				// directory, from ReadDir's own lstat. Stat through
				// it so a symlinked mount is not skipped.
				if info, err := os.Stat(path); err == nil {
					isDir = info.IsDir()
				}
			}
			if isDir {
				roots = append(roots, path)
			}
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("no disc root found: pass --disc or a --discs-dir with mounted subdirectories")
	}
	return roots, nil
}
