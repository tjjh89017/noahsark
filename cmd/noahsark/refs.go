package main

import (
	"bufio"
	"encoding/hex"
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

	names := make([]string, 0, len(refs))
	for n := range refs {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s %s\n", n, refs[n])
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
	defer f.Close()

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
// produces: lowercase hex of the multihash varint prefix (algorithm code
// 0x12, length 32) followed by the 32-byte digest.
func parseSnapshotID(s string) (object.ID, error) {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return object.ID{}, fmt.Errorf("snapshot id %q: %w", s, err)
	}
	if len(raw) != 34 || raw[0] != 0x12 || raw[1] != 0x20 {
		return object.ID{}, fmt.Errorf("snapshot id %q: not a sha256 multihash id", s)
	}
	var id object.ID
	copy(id[:], raw[2:])
	return id, nil
}
