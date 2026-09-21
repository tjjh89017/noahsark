package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tjjh89017/noahsark/internal/object"
)

// refsFileName is the local ref file's name inside a repository. It
// tracks the newest snapshot committed under each ref name so pack can
// select by --ref, the same way OPERATIONS.md's local ref log does,
// reduced to a flat text file since no ref history or state log exists
// in this build.
const refsFileName = "refs.txt"

// updateRef sets name to point at id in repoDir's ref file, creating the
// file if needed and replacing any earlier value for name.
func updateRef(repoDir, name string, id object.ID) error {
	refs, err := readRefs(repoDir)
	if err != nil {
		return err
	}
	refs[name] = id.TextForm()
	return writeRefs(repoDir, refs)
}

// writeRefs replaces repoDir's ref file with exactly the name-to-id-text
// pairs in refs. recover uses this to restore every ref a disc's
// REFS table names in one write, instead of one updateRef call per name.
func writeRefs(repoDir string, refs map[string]string) error {
	names := make([]string, 0, len(refs))
	for n := range refs {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, n := range names {
		_, _ = fmt.Fprintf(&b, "%s %s\n", n, refs[n])
	}
	return os.WriteFile(filepath.Join(repoDir, refsFileName), []byte(b.String()), 0o644)
}

// resolveRef returns the snapshot id name points at in repoDir's ref
// file.
func resolveRef(repoDir, name string) (object.ID, error) {
	refs, err := readRefs(repoDir)
	if err != nil {
		return object.ID{}, err
	}
	text, ok := refs[name]
	if !ok {
		return object.ID{}, fmt.Errorf("ref %q not found", name)
	}
	return parseSnapshotID(text)
}

// readRefs reads repoDir's ref file into a name-to-id-text map. A
// missing file is an empty map, matching a fresh repository with no
// commit yet.
func readRefs(repoDir string) (map[string]string, error) {
	refs := make(map[string]string)
	f, err := os.Open(filepath.Join(repoDir, refsFileName))
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

// parseSnapshotID parses a snapshot id in the text form object.ID.TextForm
// produces.
func parseSnapshotID(s string) (object.ID, error) {
	return object.ParseID(s)
}
