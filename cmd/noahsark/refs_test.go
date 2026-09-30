package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/object"
)

// TestWriteRefsReplacesTheFileByRename checks that writeRefs never writes
// refs.txt in place. A second name of the old file keeps the old bytes:
// the new file is a new inode that a rename put in place. A crash during
// the write thus leaves the old file or the new file, never a torn one.
func TestWriteRefsReplacesTheFileByRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "refs.txt")
	const old = "a sha256-old\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "old-link")
	if err := os.Link(path, keep); err != nil {
		t.Fatal(err)
	}

	if err := writeRefs(path, map[string]string{"b": "sha256-2", "a": "sha256-1"}); err != nil {
		t.Fatal(err)
	}

	kept, err := os.ReadFile(keep)
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != old {
		t.Fatalf("the old inode holds %q, want %q: writeRefs wrote the file in place", kept, old)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a sha256-1\nb sha256-2\n" {
		t.Fatalf("refs.txt = %q", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("refs.txt mode = %v, want 0644", fi.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("writeRefs left the temporary file %s", e.Name())
		}
	}
}

// TestCommitKeepsEachRefWhenItMovesOne checks that commit moves its ref
// and keeps the lines of the other refs.
func TestCommitKeepsEachRefWhenItMovesOne(t *testing.T) {
	repo, src := initAndCommit(t)
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=other", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	refs, err := readRefs(testLayout(t, repo).refsFile())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs["other"] == "" {
		t.Fatalf("refs = %v, want the date ref and other", refs)
	}
}

// TestWriteRefsRefusesABadName checks that refs.txt never gets a name
// that checkRefName refuses: writeRefs refuses the whole write and keeps
// the old file.
func TestWriteRefsRefusesABadName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refs.txt")
	const old = "a sha256-old\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeRefs(path, map[string]string{"a": "sha256-1", "b c": "sha256-2"}); err == nil {
		t.Fatal(`writeRefs wrote the name "b c"`)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != old {
		t.Fatalf("refs.txt = %q, %v; want the old file %q", got, err, old)
	}
}

// TestRecoverRefsKeepsABadNameOutOfRefsFile gives recoverRefs a REFS
// record whose name has a space, as a disc of another writer can hold.
// The name stays out of refs.txt, and the ref ledger holds it. The good
// name of the same disc goes into refs.txt.
func TestRecoverRefsKeepsABadNameOutOfRefsFile(t *testing.T) {
	repo, _ := initAndCommit(t)
	layout := testLayout(t, repo)
	cfg, err := readConfig(configPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	repoUUID, err := parseRepoUUID(cfg.RepoUUID)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID(format.ObjectKindSnapshot, []byte("a snapshot"))
	record := func(name string) format.RefRecord {
		r := format.RefRecord{SnapshotID: id, TimeSec: 1, NameLen: uint16(len(name))}
		copy(r.Name[:], name)
		return r
	}
	if err := recoverRefs(layout, repoUUID, []format.RefRecord{record("bad name"), record("good")}); err != nil {
		t.Fatal(err)
	}
	refs, err := readRefs(layout.refsFile())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := refs["bad name"]; ok {
		t.Fatal(`refs.txt holds the name "bad name"`)
	}
	if refs["good"] != id.TextForm() {
		t.Fatalf("refs.txt names good as %q, want %s", refs["good"], id.TextForm())
	}
	ledger, err := image.LoadRefsLedger(layout.refsLedgerFile(), repoUUID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(ledger.Records, func(r format.RefRecord) bool { return catalog.RefName(r) == "bad name" }) {
		t.Fatal(`the ref ledger does not hold the name "bad name"`)
	}
}
