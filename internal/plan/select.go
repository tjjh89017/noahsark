package plan

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// PartSuffix ends the name of the hidden part file that holds the bytes
// of a file until the file is complete.
const PartSuffix = ".noahsark-part"

// PathError reports a PATH argument that the snapshot does not hold.
type PathError struct{ Path string }

func (e *PathError) Error() string {
	return fmt.Sprintf("no entry of the snapshot matches %s", e.Path)
}

// target is one entry that a restore writes below the destination.
type target struct {
	entry format.TreeEntry
	// parents are the directories below the destination that hold the
	// entry. No tree entry describes them.
	parents []string
	// name is the name of the entry in its parent. An empty name puts
	// the content of the directory entry directly into the parent.
	name string
}

// Selection is the part of a snapshot that a restore writes, and where
// each part goes below the destination.
type Selection struct {
	c       *catalog.Catalog
	targets []target
}

// rootDir is one source root of a snapshot.
type rootDir struct {
	entry format.TreeEntry
	segs  []string
}

// Select resolves paths in snap. A path is relative to the source root,
// as ls prints it. When the root tree holds more than one source root, a
// path starts with the path of its source root. With no path, the
// content of the one source root goes into the destination; with more
// than one source root, each root keeps its path below the destination.
//
// A path follows the rsync rule for a trailing slash: "photos" makes the
// directory "photos" in the destination, and "photos/" puts the content
// of "photos" directly into the destination. A path that the snapshot
// does not hold returns a *PathError.
func Select(c *catalog.Catalog, snap *format.Snapshot, paths []string) (*Selection, error) {
	rootID := object.ID(snap.RootTree)
	root, err := c.ReadTree(rootID)
	if err != nil {
		return nil, treeError(rootID, err)
	}
	var roots []rootDir
	for _, e := range root.Entries {
		if e.EntryType != format.EntryTypeDirectory {
			continue
		}
		path, _ := format.DecodeRootName(string(e.Name))
		roots = append(roots, rootDir{entry: e, segs: splitPath(path)})
	}

	s := &Selection{c: c}
	if len(paths) == 0 {
		for _, r := range roots {
			if len(roots) == 1 || len(r.segs) == 0 {
				s.targets = append(s.targets, target{entry: r.entry})
				continue
			}
			s.targets = append(s.targets, target{entry: r.entry, parents: r.segs[:len(r.segs)-1], name: r.segs[len(r.segs)-1]})
		}
		return s, nil
	}
	for _, p := range paths {
		t, err := s.resolve(roots, p)
		if err != nil {
			return nil, err
		}
		s.targets = append(s.targets, t)
	}
	return s, nil
}

// resolve finds the entry that path names.
func (s *Selection) resolve(roots []rootDir, path string) (target, error) {
	segs := splitPath(path)
	if len(segs) == 0 {
		return target{}, &PathError{Path: path}
	}
	var cur format.TreeEntry
	var rest []string
	switch {
	case len(roots) == 1:
		cur, rest = roots[0].entry, segs
	default:
		found := false
		for _, r := range roots {
			if len(r.segs) == 0 || len(r.segs) > len(segs) || !equalSegs(r.segs, segs[:len(r.segs)]) {
				continue
			}
			cur, rest, found = r.entry, segs[len(r.segs):], true
			break
		}
		if !found {
			return target{}, &PathError{Path: path}
		}
	}
	for _, name := range rest {
		if cur.EntryType != format.EntryTypeDirectory {
			return target{}, &PathError{Path: path}
		}
		id := object.ID(cur.ContentID)
		t, err := s.c.ReadTree(id)
		if err != nil {
			return target{}, treeError(id, err)
		}
		found := false
		for _, e := range t.Entries {
			if string(e.Name) == name {
				cur, found = e, true
				break
			}
		}
		if !found {
			return target{}, &PathError{Path: path}
		}
	}
	if strings.HasSuffix(path, "/") {
		if cur.EntryType != format.EntryTypeDirectory {
			return target{}, &PathError{Path: path}
		}
		return target{entry: cur}, nil
	}
	return target{entry: cur, name: segs[len(segs)-1]}, nil
}

// Visitor receives the entries of a walk of a selection.
type Visitor interface {
	// Dir makes or enters the directories names below parent, and
	// returns the last one. When ok is false, the walk skips what the
	// directory holds.
	Dir(parent string, names []string) (path string, ok bool, err error)
	// DirDone follows the walk of a directory entry that Dir entered.
	DirDone(path string, e format.TreeEntry)
	// File gets a regular file, with the path of its part file.
	File(dest, part string, e format.TreeEntry) error
	// Other gets a symlink or a special file.
	Other(dest string, e format.TreeEntry) error
}

// Walk walks the selection below outDir, depth first, in the order of
// the trees. It reads the trees from the catalog. An error of v stops
// the walk.
func (s *Selection) Walk(outDir string, v Visitor) error {
	for _, t := range s.targets {
		dir, ok, err := v.Dir(outDir, t.parents)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if t.name == "" {
			if err := s.walkTree(object.ID(t.entry.ContentID), dir, v); err != nil {
				return err
			}
			continue
		}
		if err := s.visit(dir, t.name, t.entry, map[string]bool{t.name: true}, v); err != nil {
			return err
		}
	}
	return nil
}

// walkTree visits each entry of the tree id, whose directory is dir.
func (s *Selection) walkTree(id object.ID, dir string, v Visitor) error {
	t, err := s.c.ReadTree(id)
	if err != nil {
		return treeError(id, err)
	}
	taken := make(map[string]bool, len(t.Entries))
	for _, e := range t.Entries {
		taken[string(e.Name)] = true
	}
	for _, e := range t.Entries {
		if err := s.visit(dir, string(e.Name), e, taken, v); err != nil {
			return err
		}
	}
	return nil
}

// visit gives one entry, named name in dir, to v. taken holds the names
// that the snapshot puts in dir.
func (s *Selection) visit(dir, name string, e format.TreeEntry, taken map[string]bool, v Visitor) error {
	dest, err := JoinSafe(dir, name)
	if err != nil {
		return err
	}
	switch e.EntryType {
	case format.EntryTypeDirectory:
		sub, ok, err := v.Dir(dir, []string{name})
		if err != nil || !ok {
			return err
		}
		if err := s.walkTree(object.ID(e.ContentID), sub, v); err != nil {
			return err
		}
		v.DirDone(sub, e)
		return nil
	case format.EntryTypeRegular:
		part, err := JoinSafe(dir, PartName(name, taken))
		if err != nil {
			return err
		}
		return v.File(dest, part, e)
	default:
		return v.Other(dest, e)
	}
}

// treeError reports a tree that the catalog cannot give. A damaged tree
// already names the tree and the cause.
func treeError(id object.ID, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("tree %s is not in the catalog; run recover with the disc that holds it: %w", id.TextForm(), err)
	}
	if _, damaged := errors.AsType[*catalog.DamagedObjectError](err); damaged {
		return err
	}
	return fmt.Errorf("tree %s: %w", id.TextForm(), err)
}

// PartName returns the name of the part file of a file called name, in a
// directory whose entry names are taken. When the snapshot itself holds
// a file of the plain part name, the suffix carries a number. The names
// come from the tree, thus every run picks the same name.
func PartName(name string, taken map[string]bool) string {
	candidate := "." + name + PartSuffix
	for i := 2; taken[candidate]; i++ {
		candidate = fmt.Sprintf(".%s%s%d", name, PartSuffix, i)
	}
	return candidate
}

// JoinSafe joins name below dir, and refuses a result outside dir.
func JoinSafe(dir, name string) (string, error) {
	dest := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, dest)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("name %q leaves its directory", name)
	}
	return dest, nil
}

// JoinAll joins each of names below parent with JoinSafe.
func JoinAll(parent string, names []string) (string, error) {
	path := parent
	for _, name := range names {
		next, err := JoinSafe(path, name)
		if err != nil {
			return "", err
		}
		path = next
	}
	return path, nil
}

// splitPath splits a slash-separated path into its names. It drops
// empty names, so a leading, a doubled or a trailing slash adds none.
func splitPath(p string) []string {
	var out []string
	for s := range strings.SplitSeq(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func equalSegs(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
