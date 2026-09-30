package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestPackWarnsAboutAStagedTreeWithAFlippedByte flips the last byte of
// the root tree of one snapshot and keeps its length. The tree still
// decodes, thus only the content id check finds the damage. pack must
// print the damaged-item warning, pack the other snapshot, keep the
// damaged snapshot Staged, and exit 1.
func TestPackWarnsAboutAStagedTreeWithAFlippedByte(t *testing.T) {
	work := t.TempDir()
	repo := filepath.Join(work, "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	code, out := runCmd(t, "--repo="+repo, "commit", writeSeededSource(t, 42, 2))
	if code != 0 {
		t.Fatalf("commit A: exit %d: %s", code, out)
	}
	idA := snapshotIDFromCommit(t, out)
	damaged := flipRootTreeLastByte(t, repo, idA)
	code, out = runCmd(t, "--repo="+repo, "commit", writeSeededSource(t, 43, 2))
	if code != 0 {
		t.Fatalf("commit B: exit %d: %s", code, out)
	}
	idB := snapshotIDFromCommit(t, out)

	te := newTestEnv(t.TempDir())
	code, _ = te.run("--repo="+repo, "pack", "--capacity=bd25", "--out="+filepath.Join(work, "d0"))
	warning := fmt.Sprintf("noahsark: pack: warning: snapshot %s: cannot pack all of it: tree %s is damaged; commit the same source again", idA[4:16], damaged)
	if code != 1 || !strings.Contains(te.errOut.String(), warning) || !strings.Contains(te.out.String(), "packed disc 0 ") {
		t.Fatalf("pack: exit %d, stdout %q, stderr %q, want 1, the packed disc and the warning %q", code, te.out.String(), te.errOut.String(), warning)
	}
	log := openTestLog(t, repo)
	for _, c := range []struct {
		id   string
		want stage.State
	}{{idA, stage.Staged}, {idB, stage.Packed}} {
		id, err := object.ParseID(c.id)
		if err != nil {
			t.Fatal(err)
		}
		if rec, _ := log.Get(id); rec.State != c.want {
			t.Fatalf("snapshot %s is %v after the pack, want %v", c.id[4:16], rec.State, c.want)
		}
	}
}

// flipRootTreeLastByte flips one bit of the last byte of the root tree
// object file of the snapshot snapID of the catalog of repo, and
// returns the id text of the tree.
func flipRootTreeLastByte(t *testing.T, repo, snapID string) string {
	t.Helper()
	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	id, err := object.ParseID(snapID)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	root := object.ID(snap.RootTree)
	path := c.MetaPath(format.ObjectKindTree, root)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0x01
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return root.TextForm()
}
