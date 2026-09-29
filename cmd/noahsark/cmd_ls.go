package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

func init() {
	register(&command{
		name:  "ls",
		usage: "ls [-R | --recursive] SNAPSHOT [PATH]",
		summary: "List the entries of a snapshot from the catalog. One entry on each line: " +
			"mode, type, size, time and path, separated by a tab.",
		flags: lsFlags,
	})
}

// lsOptions holds the command options of ls.
type lsOptions struct {
	recursive bool
}

func lsFlags(fs *flag.FlagSet) runFunc {
	o := &lsOptions{}
	fs.BoolVar(&o.recursive, "R", false, "descend into subdirectories")
	fs.BoolVar(&o.recursive, "recursive", false, "descend into subdirectories")
	return o.run
}

// lsUsage is the usage line that ls prints for a wrong argument count.
const lsUsage = "usage: noahsark ls [-R | --recursive] SNAPSHOT [PATH]"

// run implements "noahsark ls". It reads the catalog only, takes no lock
// and changes no file.
func (o *lsOptions) run(e *env, args []string) int {
	const cmd = "ls"
	stderr := e.stderr
	if len(args) < 1 || len(args) > 2 {
		_, _ = fmt.Fprintln(stderr, lsUsage)
		return 2
	}
	var pathArg string
	if len(args) == 2 {
		pathArg = args[1]
	}

	rc, code := openRepoCatalog(e, cmd)
	if rc == nil {
		return code
	}
	id, code, ok := rc.resolve(cmd, args[0])
	if !ok {
		return code
	}
	snap, code, ok := rc.snapshot(cmd, args[0], id)
	if !ok {
		return code
	}

	out := bufio.NewWriter(e.stdout)
	l := &lsLister{src: rc.src, out: out, recursive: o.recursive}
	err := l.run(object.ID(snap.RootTree), pathArg)
	_ = out.Flush()
	var missing *notHeldError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &missing):
		printPartial(stderr, cmd, id)
		return 1
	case errors.Is(err, errNoSuchPath):
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s is not in snapshot %s\n", cmd, escapeField(pathArg), shortID(id))
		return 2
	}
	_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
	return 1
}

// errNoSuchPath reports a PATH that the snapshot does not hold.
var errNoSuchPath = errors.New("path is not in the snapshot")

// lsItem is one entry that ls can print: the tree entry, its path
// relative to the source root, and the name that a PATH segment matches.
// The name of a root entry is its full source root path.
type lsItem struct {
	path  string
	name  string
	entry format.TreeEntry
}

// lsLister walks the part of a snapshot tree that ls lists. It prints
// each line when it finds the entry, thus it never holds the full list.
type lsLister struct {
	src       *catalogSource
	out       io.Writer
	recursive bool
}

// run lists the snapshot with the root tree rootID, below pathArg, or
// below the source root when pathArg is empty.
func (l *lsLister) run(rootID object.ID, pathArg string) error {
	top, err := l.topLevel(rootID)
	if err != nil {
		return err
	}
	segs := splitLsPath(pathArg)
	if len(segs) == 0 {
		return l.listItems(top)
	}
	item, err := l.find(top, segs)
	if err != nil {
		return err
	}
	if item.entry.EntryType != format.EntryTypeDirectory {
		l.emit(item)
		return nil
	}
	return l.listDir(item)
}

// topLevel gives the entries of the first level that ls prints. A
// snapshot with one source root skips the root tree level: the top level
// is the content of the source root. A snapshot with more than one
// source root keeps the root level: each root entry is a directory whose
// path is its source root path without the leading slash.
func (l *lsLister) topLevel(rootID object.ID) ([]lsItem, error) {
	root, err := l.src.Tree(rootID)
	if err != nil {
		return nil, err
	}
	if len(root.Entries) == 1 {
		return l.children(lsItem{entry: root.Entries[0]})
	}
	items := make([]lsItem, 0, len(root.Entries))
	for _, e := range root.Entries {
		p := strings.Join(splitLsPath(rootPathOf(e)), "/")
		items = append(items, lsItem{path: p, name: p, entry: e})
	}
	return items, nil
}

// children reads the tree of the directory dir and gives its entries.
func (l *lsLister) children(dir lsItem) ([]lsItem, error) {
	t, err := l.src.Tree(object.ID(dir.entry.ContentID))
	if err != nil {
		return nil, err
	}
	items := make([]lsItem, 0, len(t.Entries))
	for _, e := range t.Entries {
		name := string(e.Name)
		items = append(items, lsItem{path: joinLsPath(dir.path, name), name: name, entry: e})
	}
	return items, nil
}

// find walks from the top level to the entry that segs names.
func (l *lsLister) find(top []lsItem, segs []string) (lsItem, error) {
	level := top
	for {
		var next *lsItem
		var used int
		for i := range level {
			key := splitLsPath(level[i].name)
			if len(key) > 0 && len(key) <= len(segs) && slices.Equal(key, segs[:len(key)]) {
				next, used = &level[i], len(key)
				break
			}
		}
		if next == nil {
			return lsItem{}, errNoSuchPath
		}
		segs = segs[used:]
		if len(segs) == 0 {
			return *next, nil
		}
		if next.entry.EntryType != format.EntryTypeDirectory {
			return lsItem{}, errNoSuchPath
		}
		var err error
		if level, err = l.children(*next); err != nil {
			return lsItem{}, err
		}
	}
}

// listItems prints each item, and descends into a directory with -R.
func (l *lsLister) listItems(items []lsItem) error {
	for _, it := range items {
		l.emit(it)
		if l.recursive && it.entry.EntryType == format.EntryTypeDirectory {
			if err := l.listDir(it); err != nil {
				return err
			}
		}
	}
	return nil
}

// listDir prints the entries of the directory dir.
func (l *lsLister) listDir(dir lsItem) error {
	items, err := l.children(dir)
	if err != nil {
		return err
	}
	return l.listItems(items)
}

// emit prints one line: mode, type, size, time and path, separated by a
// tab.
func (l *lsLister) emit(it lsItem) {
	e := it.entry
	_, _ = fmt.Fprintf(l.out, "%04o\t%s\t%d\t%s\t%s\n",
		e.Mode&0o7777, entryTypeWord(e.EntryType), entrySize(e),
		utcTime(e.MtimeSec), escapeField(it.path))
}

// entryTypeWord gives the type field of an ls line.
func entryTypeWord(t uint8) string {
	switch t {
	case format.EntryTypeRegular:
		return "file"
	case format.EntryTypeDirectory:
		return "dir"
	case format.EntryTypeSymlink:
		return "symlink"
	case format.EntryTypeFIFO:
		return "fifo"
	case format.EntryTypeSocket:
		return "socket"
	case format.EntryTypeCharDev:
		return "chardev"
	case format.EntryTypeBlockDev:
		return "blockdev"
	}
	return fmt.Sprintf("type%d", t)
}

// entrySize gives the size field of an ls line: the content size of a
// file, the target length of a symlink, and 0 for every other type.
func entrySize(e format.TreeEntry) uint64 {
	switch e.EntryType {
	case format.EntryTypeRegular:
		return e.Size
	case format.EntryTypeSymlink:
		for _, t := range e.TLVs {
			if t.Type == format.TLVTypeSymlinkTarget {
				return uint64(len(t.Payload))
			}
		}
	}
	return 0
}

// utcTime gives a time as RFC 3339 in UTC, to the second.
func utcTime(sec int64) string {
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// joinLsPath joins a directory path and an entry name.
func joinLsPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// splitLsPath splits a path into its segments. It drops a leading or a
// trailing slash and an empty segment.
func splitLsPath(p string) []string {
	var out []string
	for s := range strings.SplitSeq(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// rootPathOf gives the source root path that the name of a root tree
// entry holds. A name that does not decode is used as it is.
func rootPathOf(e format.TreeEntry) string {
	path, _ := format.DecodeRootName(string(e.Name))
	return path
}

// repoCatalog is the catalog of a repository, opened read-only, and the
// merged refs. ls and log read it.
type repoCatalog struct {
	c      *catalog.Catalog
	src    *catalogSource
	refs   *format.RefsTable
	stderr io.Writer
}

// openRepoCatalog finds the repository and opens its catalog read-only.
// It takes no lock. On a failure it prints the reason and returns nil and
// the exit code.
func openRepoCatalog(e *env, cmd string) (*repoCatalog, int) {
	repoDir, err := e.findRepo()
	if errors.Is(err, errNoRepo) {
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: no repository; run recover first, one time for each disc\n", cmd)
		return nil, 2
	}
	if err != nil {
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %v\n", cmd, err)
		return nil, 2
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %v\n", cmd, err)
		return nil, configExitCode(err)
	}
	c, err := catalog.OpenReadOnly(repoDir)
	if err != nil {
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %v\n", cmd, err)
		return nil, 1
	}
	src := &catalogSource{c: c, refsPath: layoutOf(repoDir, cfg).refsFile(), stderr: e.stderr, cmd: cmd}
	refs, err := src.Refs()
	if errors.Is(err, catalog.ErrNoDisc) {
		refs, err = &format.RefsTable{}, nil
	}
	if err != nil {
		_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %v\n", cmd, err)
		return nil, 1
	}
	return &repoCatalog{c: c, src: src, refs: refs, stderr: e.stderr}, 0
}

// resolve resolves a SNAPSHOT argument. An empty catalog matches no
// snapshot. On a failure it prints the reason and returns false and the
// exit code.
func (rc *repoCatalog) resolve(cmd, arg string) (object.ID, int, bool) {
	id, err := rc.src.ParseSnapshotArg(arg)
	if err == nil {
		return id, 0, true
	}
	if errors.Is(err, catalog.ErrNoDisc) {
		err = &refNotFoundError{arg: arg}
	}
	_, _ = fmt.Fprintf(rc.stderr, "noahsark: %s: %v\n", cmd, err)
	return object.ID{}, exitForSnapshotArg(err), false
}

// known reports whether a ref names id, or the completeness file lists
// it.
func (rc *repoCatalog) known(id object.ID) bool {
	if rc.c.Partial(id) || rc.c.Complete(id) {
		return true
	}
	for _, r := range rc.refs.Records {
		if object.ID(r.SnapshotID) == id {
			return true
		}
	}
	return false
}

// snapshot reads the snapshot id that arg resolved to. A partial
// snapshot, and a known snapshot whose object the catalog does not hold,
// print the partial line and exit 1. A full id that nothing knows
// matches no snapshot. On a failure it prints the reason and returns
// false and the exit code.
func (rc *repoCatalog) snapshot(cmd, arg string, id object.ID) (*format.Snapshot, int, bool) {
	if rc.c.Partial(id) {
		printPartial(rc.stderr, cmd, id)
		return nil, 1, false
	}
	snap, err := rc.src.Snapshot(id)
	if err == nil {
		return snap, 0, true
	}
	if rc.known(id) {
		printPartial(rc.stderr, cmd, id)
		return nil, 1, false
	}
	_, _ = fmt.Fprintf(rc.stderr, "noahsark: %s: %v\n", cmd, &refNotFoundError{arg: arg})
	return nil, 2, false
}

// printPartial prints the line of a partial snapshot.
func printPartial(w io.Writer, cmd string, id object.ID) {
	_, _ = fmt.Fprintf(w, "noahsark: %s: snapshot %s is partial; run recover with more discs\n", cmd, shortID(id))
}
