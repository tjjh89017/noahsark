package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/cache"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/restore"
)

// cmdLs implements "noahsark ls". With no DISC-ROOT or --discs-dir,
// SNAPSHOT (an id or a ref name) resolves through the local cache, so
// ls needs no disc present; give a disc root or --discs-dir to read
// straight from a disc instead, the same way restore and verify do. ls
// reads tree objects only; it never opens a chunk.
func cmdLs(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("noahsark ls [--long] [--recursive] [DISC-ROOT...] SNAPSHOT [PATH]",
		"List a snapshot's tree. Resolves SNAPSHOT through the local cache with no disc given; accepts --discs-dir or one or more DISC-ROOT positionals to read a disc instead. "+
			"Each line's first column: '!' when the entry is UNSTABLE, a space otherwise.", stderr)
	repoFlag := fs.String("repo", "", "repository root, for the cache; used only with no disc given")
	discsDir := fs.String("discs-dir", "", "a directory whose immediate subdirectories are mounted disc roots")
	long := fs.Bool("long", false, "print mode, owner, size and mtime")
	recursive := fs.Bool("recursive", false, "descend into subdirectories")
	if err := fs.Parse(args); err != nil {
		return exitForFlagParse(err)
	}
	if checkPositionalsForFlags("ls", fs, stderr) {
		return 2
	}

	multi := *discsDir != ""
	// Leading positional arguments that name an existing directory are
	// DISC-ROOTs; at least one argument stays unconsumed for SNAPSHOT.
	// This needs no guess: SNAPSHOT and PATH are never paths that already
	// exist on this host.
	var discRootArgs []string
	if !multi {
		end := max(fs.NArg()-1, 0)
		i := 0
		for i < end && looksLikeDiscRoot(fs.Arg(i)) {
			i++
		}
		discRootArgs = fs.Args()[:i]
	}
	discRootGiven := len(discRootArgs) > 0
	if !multi && !discRootGiven && fs.NArg() > 0 && looksLikePathNotDisc(fs.Arg(0)) {
		_, _ = fmt.Fprintf(stderr, "noahsark: ls: no such disc root: %s\n", fs.Arg(0))
		return 2
	}
	cacheMode := !multi && !discRootGiven

	var src snapshotSource
	var cacheObj *cache.Cache
	var positional []string
	switch {
	case cacheMode:
		if fs.NArg() < 1 || fs.NArg() > 2 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark ls [--long] [--recursive] SNAPSHOT [PATH]")
			return 2
		}
		positional = fs.Args()
		cs, c, err := openCacheSource(*repoFlag)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: ls:", err)
			return 1
		}
		src, cacheObj = cs, c
	case multi:
		if fs.NArg() < 1 || fs.NArg() > 2 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark ls --discs-dir=DIR [--long] [--recursive] SNAPSHOT [PATH]")
			return 2
		}
		positional = fs.Args()
	default:
		positional = fs.Args()[len(discRootArgs):]
		if len(positional) < 1 || len(positional) > 2 {
			_, _ = fmt.Fprintln(stderr, "usage: noahsark ls [--long] [--recursive] DISC-ROOT... SNAPSHOT [PATH]")
			return 2
		}
	}

	if !cacheMode {
		discRoots, err := resolveDiscRoots(*discsDir, discRootArgs)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: ls:", err)
			return 2
		}
		restoreSrc, err := restore.OpenSource(discRoots)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "noahsark: ls:", err)
			return 1
		}
		src = restoreSrc
	}

	snapID, err := src.ParseSnapshotArg(positional[0])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "noahsark: ls:", err)
		return exitForSnapshotArg(err)
	}
	var pathArg string
	if len(positional) > 1 {
		pathArg = positional[1]
	}

	snap, err := src.Snapshot(snapID)
	if err != nil {
		return reportSourceError("ls", stderr, err, cacheObj, snapID)
	}

	lister := &lsLister{src: src, stdout: stdout, long: *long}
	if err := lister.run(object.ID(snap.RootTree), pathArg, *recursive); err != nil {
		return reportSourceError("ls", stderr, err, cacheObj, snapID)
	}
	return 0
}

// lsLister walks the part of a snapshot's tree ls was asked to list and
// prints one line per entry as it is found, so a whole-snapshot
// --recursive listing never holds the full entry list in memory.
type lsLister struct {
	src    snapshotSource
	stdout io.Writer
	long   bool
}

// run lists rootTreeID's tree, restricted to pathArg (the include-path
// form: root path then entry path, forward slashes, an optional leading
// slash), or the tree's root entries when pathArg is empty.
func (l *lsLister) run(rootTreeID object.ID, pathArg string, recursive bool) error {
	rootTree, err := l.src.Tree(rootTreeID)
	if err != nil {
		return err
	}

	if pathArg == "" {
		for _, e := range rootTree.Entries {
			rp := strings.Join(splitLsPath(rootPathOf(e)), "/")
			l.emit(rp, e)
			if recursive {
				if err := l.listDir(object.ID(e.ContentID), rp, true); err != nil {
					return err
				}
			}
		}
		return nil
	}

	target, targetPath, err := resolveLsPath(l.src, rootTree.Entries, pathArg)
	if err != nil {
		return err
	}
	if target.EntryType != format.EntryTypeDirectory {
		l.emit(targetPath, *target)
		return nil
	}
	return l.listDir(object.ID(target.ContentID), targetPath, recursive)
}

// listDir lists the children of the tree at id, whose own path is
// prefix, descending into subdirectories when recurse is true.
func (l *lsLister) listDir(id object.ID, prefix string, recurse bool) error {
	t, err := l.src.Tree(id)
	if err != nil {
		return err
	}
	for _, e := range t.Entries {
		path := prefix + "/" + string(e.Name)
		l.emit(path, e)
		if recurse && e.EntryType == format.EntryTypeDirectory {
			if err := l.listDir(object.ID(e.ContentID), path, recurse); err != nil {
				return err
			}
		}
	}
	return nil
}

// emit prints one entry.
func (l *lsLister) emit(path string, e format.TreeEntry) {
	unstable := e.EntryFlags&format.EntryFlagUnstable != 0
	marker := byte(' ')
	if unstable {
		marker = '!'
	}
	displayPath := path
	if e.EntryType == format.EntryTypeDirectory {
		displayPath += "/"
	}
	if l.long {
		_, _ = fmt.Fprintf(l.stdout, "%c%s  %-20s  %10d  %s  %s\n",
			marker, modeString(e), ownerString(e), e.Size, mtimeString(e), displayPath)
		return
	}
	_, _ = fmt.Fprintf(l.stdout, "%c%s\n", marker, displayPath)
}

// resolveLsPath walks from rootEntries to the entry pathArg names, in the
// include-path form: pathArg's leading segments match one root entry's
// own root path in full, whole segment by whole segment, and any
// remaining segments name a path inside that root entry's own tree. It
// returns the entry found and its full path.
func resolveLsPath(src snapshotSource, rootEntries []format.TreeEntry, pathArg string) (*format.TreeEntry, string, error) {
	segs := splitLsPath(pathArg)
	if len(segs) == 0 {
		return nil, "", fmt.Errorf("ls: empty path")
	}

	var cur *format.TreeEntry
	var consumed []string
	for i := range rootEntries {
		e := rootEntries[i]
		rp := splitLsPath(rootPathOf(e))
		if len(rp) == 0 || len(rp) > len(segs) || !segsEqual(rp, segs[:len(rp)]) {
			continue
		}
		cur = &e
		consumed = rp
		break
	}
	if cur == nil {
		return nil, "", fmt.Errorf("ls: path %q matches no entry", pathArg)
	}

	curEntry := *cur
	for _, name := range segs[len(consumed):] {
		if curEntry.EntryType != format.EntryTypeDirectory {
			return nil, "", fmt.Errorf("ls: path %q matches no entry", pathArg)
		}
		t, err := src.Tree(object.ID(curEntry.ContentID))
		if err != nil {
			return nil, "", err
		}
		found := false
		for _, e := range t.Entries {
			if string(e.Name) == name {
				curEntry = e
				found = true
				break
			}
		}
		if !found {
			return nil, "", fmt.Errorf("ls: path %q matches no entry", pathArg)
		}
		consumed = append(consumed, name)
	}
	return &curEntry, strings.Join(consumed, "/"), nil
}

func segsEqual(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// splitLsPath splits a forward-slash path into segments, stripping one
// leading slash and dropping any empty segment a doubled or trailing
// slash would otherwise produce. It matches restore --include's own path
// form, so a line copied from ls works as --include.
func splitLsPath(p string) []string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// rootPathOf returns the source root path a root tree entry's name
// holds, decoded by the root name escape rule. An undecodable name is
// used as it is.
func rootPathOf(e format.TreeEntry) string {
	path, _ := format.DecodeRootName(string(e.Name))
	return path
}

// modeString renders e's type and permission bits the way "ls -l" does:
// one type character followed by nine rwx characters.
func modeString(e format.TreeEntry) string {
	var typeChar byte
	switch e.EntryType {
	case format.EntryTypeDirectory:
		typeChar = 'd'
	case format.EntryTypeSymlink:
		typeChar = 'l'
	case format.EntryTypeCharDev:
		typeChar = 'c'
	case format.EntryTypeBlockDev:
		typeChar = 'b'
	case format.EntryTypeFIFO:
		typeChar = 'p'
	case format.EntryTypeSocket:
		typeChar = 's'
	default:
		typeChar = '-'
	}
	const bits = "rwxrwxrwx"
	b := make([]byte, 10)
	b[0] = typeChar
	for i := range 9 {
		if e.Mode&(1<<uint(8-i)) != 0 {
			b[i+1] = bits[i]
		} else {
			b[i+1] = '-'
		}
	}
	// setuid, setgid and sticky each replace the execute character of
	// their own triple, upper case when that triple has no execute bit.
	for _, m := range []struct {
		bit  uint32
		pos  int
		set  byte
		nset byte
	}{
		{0o4000, 3, 's', 'S'},
		{0o2000, 6, 's', 'S'},
		{0o1000, 9, 't', 'T'},
	} {
		if e.Mode&m.bit == 0 {
			continue
		}
		if b[m.pos] == 'x' {
			b[m.pos] = m.set
		} else {
			b[m.pos] = m.nset
		}
	}
	return string(b)
}

// ownerString renders e's owner as "user:group", using the entry's
// user_name and group_name TLVs when present, and the numeric uid and
// gid otherwise.
func ownerString(e format.TreeEntry) string {
	user := strconv.FormatUint(uint64(e.UID), 10)
	group := strconv.FormatUint(uint64(e.GID), 10)
	for _, t := range e.TLVs {
		switch t.Type {
		case format.TLVTypeUserName:
			user = string(t.Payload)
		case format.TLVTypeGroupName:
			group = string(t.Payload)
		}
	}
	return user + ":" + group
}

// mtimeString renders e's mtime in UTC, so ls output does not depend on
// the host's local time zone.
func mtimeString(e format.TreeEntry) string {
	return time.Unix(e.MtimeSec, int64(e.MtimeNsec)).UTC().Format("2006-01-02 15:04:05")
}
