package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/durable"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// The local ref file, refs.txt, holds the newest snapshot of each ref
// name: one line for each name, the name, one space, and the snapshot id
// in text form. repoLayout.refsFile gives its path.

// refNameRule is the rule of checkRefName, as the operator reads it.
const refNameRule = "a ref name is 1 to 40 bytes of printable ASCII, with no space"

// checkRefName refuses a name that a line of refs.txt or a record of a
// REFS table cannot hold: an empty name, a name longer than the name
// field of a ref record, and a name with a space, a control byte or a
// byte that is not ASCII.
func checkRefName(name string) error {
	ok := len(name) >= 1 && len(name) <= format.RefNameLen
	for i := 0; ok && i < len(name); i++ {
		ok = name[i] > ' ' && name[i] < 0x7F
	}
	if !ok {
		return fmt.Errorf("ref name %q is not valid: %s", name, refNameRule)
	}
	return nil
}

// updateRef sets name to point at id in the ref file at path, creating
// the file if needed and replacing any earlier value for name.
func updateRef(path, name string, id object.ID) error {
	refs, err := readRefs(path)
	if err != nil {
		return err
	}
	refs[name] = id.TextForm()
	return writeRefs(path, refs)
}

// writeRefs replaces the ref file at path with exactly the
// name-to-id-text pairs in refs. recover uses this to restore every ref
// a disc's REFS table names in one write, instead of one updateRef call
// per name. A crash during the write leaves the old file or the new
// file, never a part of one. It refuses a name that checkRefName
// refuses, and writes nothing then.
func writeRefs(path string, refs map[string]string) error {
	names := make([]string, 0, len(refs))
	for n := range refs {
		if err := checkRefName(n); err != nil {
			return err
		}
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, n := range names {
		_, _ = fmt.Fprintf(&b, "%s %s\n", n, refs[n])
	}
	return durable.WriteFile(path, []byte(b.String()), 0o644, durable.Replace)
}

// resolveRef returns the snapshot id name points at in the ref file at
// path.
func resolveRef(path, name string) (object.ID, error) {
	refs, err := readRefs(path)
	if err != nil {
		return object.ID{}, err
	}
	text, ok := refs[name]
	if !ok {
		return object.ID{}, fmt.Errorf("ref %q not found", name)
	}
	return parseSnapshotID(text)
}

// readRefs reads the ref file at path into a name-to-id-text map. A
// missing file is an empty map, matching a fresh repository with no
// commit yet.
func readRefs(path string) (map[string]string, error) {
	refs := make(map[string]string)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return refs, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("refs: malformed line %q", line)
		}
		refs[fields[0]] = fields[1]
	}
	return refs, sc.Err()
}

// localRefRecord gives the ref record of the line name, id of refs.txt.
// The line has no time of its own: the record takes the time of the
// snapshot, or no time when the catalog c does not hold the snapshot.
// The caller checks that name fits a ref record.
func localRefRecord(c *catalog.Catalog, name string, id object.ID) format.RefRecord {
	rec := format.RefRecord{SnapshotID: id, NameLen: uint16(len(name))}
	copy(rec.Name[:], name)
	if snap, err := c.ReadSnapshot(id); err == nil {
		rec.TimeSec, rec.TimeNsec = snap.TimeSec, snap.TimeNsec
	}
	return rec
}

// parseSnapshotID parses a snapshot id in the text form object.ID.TextForm
// produces.
func parseSnapshotID(s string) (object.ID, error) {
	return object.ParseID(s)
}
