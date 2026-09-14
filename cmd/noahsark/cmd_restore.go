package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/plan"
	"github.com/tjjh89017/noahsark/internal/progress"
	"github.com/tjjh89017/noahsark/internal/restore"
)

// restoreStdin is where the disc-swap restore mode reads the operator's
// Enter key from. Tests replace it with a pipe.
var restoreStdin io.Reader = os.Stdin

// cmdRestore implements "noahsark restore". Two modes share this
// entry point.
//
// With a disc root, --disc or --discs-dir, restore reads every disc at
// once, unchanged from before: see resolveDiscRoots.
//
// With neither, and exactly SNAPSHOT and OUT-DIR left over, restore
// resolves SNAPSHOT through the local cache, the same way "plan" does,
// and walks the disc-swap loop of OPERATIONS.md's "14.2 Disc-major
// order": one disc at a time, in plan order, prompting the operator to
// insert the next one. This is the single-drive path; see
// docs/decisions.md.
func cmdRestore(args []string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if refuseLaterPhaseFlags("restore", args, stderr) {
		return 2
	}

	fs := newFlagSet("noahsark restore [--include=PATH]... [--overwrite] [--mount=DIR] [--no-eject] [--interactive] SNAPSHOT OUT-DIR",
		"Restore a snapshot to a directory. Accepts --disc (repeatable) or --discs-dir in place of DISC-ROOT for the all-discs-at-once mode.", stderr)
	repoFlag := fs.String("repo", "", "repository root")
	var discFlags stringList
	fs.Var(&discFlags, "disc", "a disc root to restore from; repeatable")
	discsDir := fs.String("discs-dir", "", "a directory whose immediate subdirectories are mounted disc roots")
	var includeFlags stringList
	fs.Var(&includeFlags, "include", "restore only this snapshot-relative path and, if it names a directory, everything under it; repeatable")
	overwrite := fs.Bool("overwrite", false, "unlink an existing path first and then create it; without this, an existing path is left alone")
	mountFlag := fs.String("mount", "", "the directory where the drive is mounted; required for the disc-swap mode")
	noEject := fs.Bool("no-eject", false, "do not eject after each disc")
	interactive := fs.Bool("interactive", false, "prompt on every disc, not only on a mismatch")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("restore", fs, stderr) {
		return 2
	}

	multi := len(discFlags) > 0 || *discsDir != ""
	if *mountFlag != "" && multi {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: --mount cannot be combined with --disc or --discs-dir")
		return 2
	}
	// A bare two positional arguments, with --mount given, is the
	// disc-swap mode's SNAPSHOT OUT-DIR. Without --mount, the same two
	// arguments are ambiguous: they could equally be a DISC-ROOT
	// SNAPSHOT for the all-discs-at-once mode with OUT-DIR left off. This
	// build resolves that only by requiring --mount for disc-swap, so a
	// missing OUT-DIR reports the usage line instead of silently running
	// the wrong mode.
	if !multi && fs.NArg() == 2 {
		if *mountFlag == "" {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark restore [--include=PATH]... [--overwrite] [--mount=DIR] [--no-eject] [--interactive] SNAPSHOT OUT-DIR")
			return 2
		}
		return cmdRestoreDiscSwap(*repoFlag, includeFlags, *overwrite, *mountFlag, *noEject, *interactive, fs.Arg(0), fs.Arg(1), stdout, stderr, prog)
	}

	var discRoots, positional []string
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

// errStdinClosed is returned when the disc-swap loop's prompt cannot
// read another line, the same signal a killed session leaves behind:
// the caller reports it as a failed restore, with whatever was already
// spooled left in place for a later resume.
var errStdinClosed = fmt.Errorf("stdin closed while waiting for the next disc")

// cmdRestoreDiscSwap runs the single-drive restore of OPERATIONS.md's
// "14.2 Disc-major order": build the same plan "plan" would print, then
// read one disc at a time, prompting the operator between discs.
func cmdRestoreDiscSwap(repoFlag string, includes stringList, overwrite bool, mountDir string, noEject, interactive bool, snapshotArg, outDir string, stdout, stderr io.Writer, prog *progress.Reporter) int {
	if mountDir == "" {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: --mount is required; OPERATIONS.md's configuration reference names no restore.mount key")
		return 2
	}

	repoDir, err := discoverRepo(repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}

	src, c, err := openCacheSource(repoFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	snapID, err := src.ParseSnapshotArg(snapshotArg)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	if err := c.CheckComplete(snapID); err != nil {
		if ie, ok := err.(*cache.IncompleteError); ok {
			_, _ = fmt.Fprintln(stderr, formatIncompleteError("restore", ie))
			return 3
		}
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	snap, err := c.ReadSnapshot(snapID)
	if err != nil {
		return reportSourceError("restore", stderr, err, c, snapID)
	}

	result, err := plan.Build(c, snap, snapID, includes)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 1
	}
	printPlanText(stdout, result)
	if len(result.Missing) > 0 {
		_, _ = fmt.Fprintf(stderr, "noahsark: restore: %d object(s) have no run known to the cache; rebuild-cache from more discs\n", result.MissingObjectCount())
		return 3
	}

	for _, d := range result.Discs {
		if d.Bytes > cfg.RestoreStagingBudget {
			_, _ = fmt.Fprintf(stderr, "noahsark: restore: disc %s needs %d bytes of staging, above restore.staging_budget (%d); this build does not split passes yet\n",
				plan.UUIDText(d.DiscUUID), d.Bytes, cfg.RestoreStagingBudget)
		}
	}

	m, err := restore.BuildManifest(c, snap, outDir, includes, overwrite)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}

	spoolRoot := filepath.Join(repoDir, "staging", "restore", snapID.TextForm())
	spoolDir := filepath.Join(spoolRoot, "objects")
	if err := os.MkdirAll(spoolDir, 0o755); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	resumed, err := resumeSpool(spoolDir, m, prog)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
		return 2
	}
	if resumed > 0 {
		_, _ = fmt.Fprintf(stdout, "resuming: %d object(s) already spooled\n", resumed)
	}

	scanner := bufio.NewScanner(restoreStdin)
	for _, d := range result.Discs {
		wanted := wantedChunks(d, m)
		if len(wanted) == 0 {
			continue
		}
		if err := detectDisc(mountDir, c, d, interactive, scanner, stdout, stderr); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
			return 2
		}
		for _, id := range wanted {
			payload, err := restore.ReadChunkFromRoot(mountDir, id)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
				return 2
			}
			if err := os.WriteFile(restore.SpoolObjectPath(spoolDir, id), payload, 0o644); err != nil {
				_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
				return 2
			}
			m.MarkSpooled(id)
		}
		if _, err := m.WriteReady(spoolDir, prog); err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
			return 2
		}
		if !noEject {
			ejectDrive(mountDir, stderr)
		}
	}

	if m.Pending() {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore: not every file could be assembled; missing object(s):")
		for _, id := range m.MissingObjects() {
			_, _ = fmt.Fprintln(stderr, " ", id.TextForm())
		}
		return 2
	}
	m.Finish()

	_, _ = fmt.Fprintf(stdout, "restored snapshot %s into %s\n", snapID.TextForm(), outDir)
	if m.Resumed() > 0 {
		_, _ = fmt.Fprintf(stdout, "resumed: %d file(s) already restored\n", m.Resumed())
	}
	if m.Skipped() > 0 {
		_, _ = fmt.Fprintf(stdout, "skipped %d existing path(s); pass --overwrite to replace them\n", m.Skipped())
		return 1
	}
	if err := removeEmptySpoolRoot(spoolRoot); err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: restore:", err)
	}
	return 0
}

// removeEmptySpoolRoot removes a disc-swap restore's staging/restore/
// <snapshot> directory once every file has been assembled and no spool
// object remains under it. A failed or still-pending restore leaves the
// directory in place, so a later resume still finds its spooled objects.
func removeEmptySpoolRoot(spoolRoot string) error {
	if err := os.Remove(filepath.Join(spoolRoot, "objects")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(spoolRoot); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// wantedChunks returns the chunk object ids d's plan entry assigns that
// the manifest still needs. Only chunk payloads are read from a mounted
// disc: BuildManifest already resolved every tree and blob from the
// cache.
func wantedChunks(d plan.DiscEntry, m *restore.Manifest) []object.ID {
	var out []object.ID
	for _, o := range d.Objects {
		if o.Kind != format.ObjectKindChunk {
			continue
		}
		if m.NeedsChunk(o.ID) {
			out = append(out, o.ID)
		}
	}
	return out
}

// resumeSpool marks every object already spooled from an earlier,
// interrupted run, and assembles any file that completes as a result.
// It returns how many spooled objects it found.
func resumeSpool(spoolDir string, m *restore.Manifest, prog *progress.Reporter) (int, error) {
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	for _, e := range entries {
		id, err := object.ParseID(e.Name())
		if err != nil {
			continue
		}
		m.MarkSpooled(id)
	}
	if _, err := m.WriteReady(spoolDir, prog); err != nil {
		return len(entries), err
	}
	return len(entries), nil
}

// discSwapRetries is how many times detectDisc retries an unreadable
// DISC.bin before it prompts the operator, and the pause between tries.
const discSwapRetries = 3

var discSwapRetryPause = 300 * time.Millisecond

// detectDisc waits until the disc d names is in the drive at mountDir,
// OPERATIONS.md's "14.6 Disc detection": no prompt when the expected
// disc is already there, unless interactive is set; a prompt, and a
// mismatch report, otherwise.
func detectDisc(mountDir string, c *cache.Cache, d plan.DiscEntry, interactive bool, scanner *bufio.Scanner, stdout, stderr io.Writer) error {
	unreadable := 0
	// promptedOnce becomes true after the first prompt this call has
	// shown, so --interactive prompts exactly once per disc rather than
	// forever: a mismatch or an unreadable disc always prompts anyway,
	// and once it has, a later matching read is accepted straight away.
	promptedOnce := false
	for {
		uuid, err := restore.ReadDiscUUID(mountDir)
		switch {
		case err != nil:
			unreadable++
			if unreadable <= discSwapRetries {
				time.Sleep(discSwapRetryPause)
				continue
			}
		case uuid == d.DiscUUID:
			if !interactive || promptedOnce {
				_, _ = fmt.Fprintf(stdout, "disc %d %s: found\n", d.DiscSeq, d.Label)
				return nil
			}
		default:
			_, _ = fmt.Fprintf(stderr, "expected disc %s (%s), found %s (%s)\n",
				plan.UUIDText(d.DiscUUID), d.Label, plan.UUIDText(uuid), labelForUUID(c, uuid))
		}
		_, _ = fmt.Fprintf(stderr, "insert disc %d %q (uuid %s) into %s and press Enter\n",
			d.DiscSeq, d.Label, plan.UUIDText(d.DiscUUID), mountDir)
		if !scanner.Scan() {
			return errStdinClosed
		}
		unreadable = 0
		promptedOnce = true
	}
}

// labelForUUID looks up a disc's label in the cache's DISCS table, for
// the mismatch report. It returns "" when the cache does not know uuid.
func labelForUUID(c *cache.Cache, uuid [16]byte) string {
	discs, err := c.Discs()
	if err != nil {
		return ""
	}
	for _, row := range discs.Rows {
		if row.DiscUUID == uuid {
			n := min(int(row.LabelLen), len(row.Label))
			return string(row.Label[:n])
		}
	}
	return ""
}

// ejectWarnedNoBinary and ejectWarnedPermission latch the two eject
// warnings this loop can print, so a multi-disc restore prints each at
// most once instead of once per disc.
var ejectWarnedNoBinary, ejectWarnedPermission bool

// ejectDrive unmounts and ejects mountDir. A missing eject binary is
// skipped with one line, not a per-disc warning: it is expected on a
// minimal host and the operator can still remove the disc by hand. An
// unmount failure that looks like a permission problem prints one line
// pointing at sudo or --no-eject, also at most once; the operator can
// still remove the disc by hand either way.
func ejectDrive(mountDir string, stderr io.Writer) {
	if out, err := exec.Command("umount", mountDir).CombinedOutput(); err != nil {
		if isPermissionDenied(out) {
			if !ejectWarnedPermission {
				_, _ = fmt.Fprintf(stderr, "noahsark: restore: umount %s failed for permission; run restore with sudo, or pass --no-eject\n", mountDir)
				ejectWarnedPermission = true
			}
		} else {
			_, _ = fmt.Fprintf(stderr, "warning: umount %s: %v\n", mountDir, err)
		}
	}
	if _, err := exec.LookPath("eject"); err != nil {
		if !ejectWarnedNoBinary {
			_, _ = fmt.Fprintln(stderr, "noahsark: restore: eject: not found on PATH; skipping eject, remove the disc by hand")
			ejectWarnedNoBinary = true
		}
		return
	}
	if err := exec.Command("eject", mountDir).Run(); err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: eject %s: %v\n", mountDir, err)
	}
}

// isPermissionDenied reports whether umount's own output names a
// permission problem, the one umount failure this build gives its own,
// more actionable hint for.
func isPermissionDenied(output []byte) bool {
	return strings.Contains(strings.ToLower(string(output)), "permission denied") ||
		strings.Contains(strings.ToLower(string(output)), "must be superuser")
}
