package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// TestLsAndLogWithADamagedRefsTable damages the catalog REFS table of
// the one disc of a repository. ls of the ref name still lists the
// snapshot, with one warning. log lists the snapshot, warns, and exits
// with code 1.
func TestLsAndLogWithADamagedRefsTable(t *testing.T) {
	fx := repoWithDisc(t, stage.DiscPacked)
	path := filepath.Join(fx.repo, "catalog", "discs", fx.uuid, catalog.RefsFileName)
	if err := os.WriteFile(path, []byte("not a REFS table"), 0o644); err != nil {
		t.Fatal(err)
	}
	const warning = "warning: the catalog REFS table of disc "

	code, out, errOut := runLs(t, fx.repo, "ls", defaultRefName())
	if code != 0 || out == "" || strings.Count(errOut, warning) != 1 {
		t.Fatalf("ls: exit %d, output %q, stderr %q; want 0, the lines and one warning", code, out, errOut)
	}

	code, out, errOut = runLs(t, fx.repo, "log")
	if code != 1 || strings.Count(out, "\n") != 1 || strings.Count(errOut, warning) != 1 || !strings.Contains(errOut, fx.uuid) {
		t.Fatalf("log: exit %d, output %q, stderr %q; want 1, one line and one warning", code, out, errOut)
	}
}

// firstLogID gives the id field of the first log line of repo.
func firstLogID(t *testing.T, repo string) string {
	t.Helper()
	code, out, errOut := runLs(t, repo, "log")
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, errOut)
	}
	id, _, _ := strings.Cut(out, "\t")
	return id
}

// TestLogPrintsOneLineForEachSnapshot checks the fields of a log line:
// the 12-character id, the time in UTC, the refs, the source path and
// the message, separated by a tab.
func TestLogPrintsOneLineForEachSnapshot(t *testing.T) {
	when := time.Date(2026, time.September, 14, 8, 30, 0, 500, time.FixedZone("x", 3600))
	setFakeNow(t, func() time.Time { return when })
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=b", "-m", "first\tline\nsecond", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	full := fullSnapshotID(t, repo)
	appendRef(t, repo, "a", full)
	id := full[len(full)-64 : len(full)-52]

	want := id + "\t2026-09-14T07:30:00Z\ta,b\t" + src + "\tfirst\\tline\\nsecond\n"
	code, out, errOut := runLs(t, repo, "log")
	if code != 0 || out != want || errOut != "" {
		t.Fatalf("log: exit %d, output %q, stderr %q; want %q", code, out, errOut, want)
	}
	for _, arg := range []string{"a", "b", id, strings.ToUpper(id[:5]), full} {
		code, out, errOut := runLs(t, repo, "log", arg)
		if code != 0 || out != want {
			t.Fatalf("log %s: exit %d, output %q, stderr %q; want %q", arg, code, out, errOut, want)
		}
	}
}

// TestLogNewestFirst commits twice in the same second. The newer
// snapshot, by the nanoseconds of its time, comes first.
func TestLogNewestFirst(t *testing.T) {
	base := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	next := base
	setFakeNow(t, func() time.Time { return next })
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	next = base.Add(time.Millisecond)
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=old", src); code != 0 {
		t.Fatalf("commit 1: exit %d: %s", code, out)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	next = base.Add(2 * time.Millisecond)
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=new", "-m", "m", src); code != 0 {
		t.Fatalf("commit 2: exit %d: %s", code, out)
	}
	code, out, errOut := runLs(t, repo, "log")
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, errOut)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("log printed %d lines, want 2: %q", len(lines), out)
	}
	if f := strings.Split(lines[0], "\t"); f[2] != "new" || f[4] != "m" {
		t.Fatalf("first line %q, want the ref new and the message m", lines[0])
	}
	if f := strings.Split(lines[1], "\t"); f[2] != "old" || f[4] != "-" {
		t.Fatalf("second line %q, want the ref old and no message", lines[1])
	}
}

// TestLogValueOfADash commits with the message "-" and the ref "-". Each
// prints as `\x2d`, thus a field of "-" always means no value.
func TestLogValueOfADash(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := writeFixtureSource(t)
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=-", "-m", "-", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	code, out, errOut := runLs(t, repo, "log")
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, errOut)
	}
	f := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
	if len(f) != 5 || f[2] != `\x2d` || f[4] != `\x2d` {
		t.Fatalf("log line %q, want `\\x2d` in the refs and the message fields", out)
	}
}

// TestLogCommaInsideAValue commits a source path and a ref name that
// hold ",". In the refs and the source path fields, a "," inside a value
// prints as `\x2c`, thus each "," separates two values. The message
// field holds one value and keeps its ",".
func TestLogCommaInsideAValue(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	src := filepath.Join(t.TempDir(), "a,b")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out := runCmd(t, "--repo="+repo, "commit", "--ref=x,y", "-m", "m,n", src); code != 0 {
		t.Fatalf("commit: exit %d: %s", code, out)
	}
	appendRef(t, repo, "z", fullSnapshotID(t, repo))
	code, out, errOut := runLs(t, repo, "log")
	if code != 0 {
		t.Fatalf("log: exit %d: %s", code, errOut)
	}
	f := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
	wantSource := strings.ReplaceAll(src, ",", `\x2c`)
	if len(f) != 5 || f[2] != `x\x2cy,z` || f[3] != wantSource || f[4] != "m,n" {
		t.Fatalf("log line %q, want the refs `x\\x2cy,z`, the source %q and the message m,n", out, wantSource)
	}
}

// TestLogRefToASnapshotNotHeld checks the line of a ref whose snapshot
// object the catalog does not hold: "-" in the time, the source path and
// the message fields.
func TestLogRefToASnapshotNotHeld(t *testing.T) {
	repo := commitWithRef(t, "latest")
	full := fullSnapshotID(t, repo)
	c, err := catalog.OpenReadOnly(repo)
	if err != nil {
		t.Fatal(err)
	}
	id := snapshotIDs(t, repo)[0]
	if err := os.Remove(c.MetaPath(format.ObjectKindSnapshot, id)); err != nil {
		t.Fatal(err)
	}
	want := full[len(full)-64:len(full)-52] + "\t-\tlatest\t-\t-\n"
	for _, args := range [][]string{{"log"}, {"log", "latest"}} {
		code, out, errOut := runLs(t, repo, args...)
		if code != 0 || out != want {
			t.Fatalf("%q: exit %d, output %q, stderr %q; want %q", args, code, out, errOut, want)
		}
	}
}

// TestLogEmptyRepository checks that log in a repository with no
// snapshot prints nothing and exits 0, and that a SNAPSHOT there matches
// nothing.
func TestLogEmptyRepository(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if code, out := runIn(t, repo, "init"); code != 0 {
		t.Fatalf("init: exit %d: %s", code, out)
	}
	if code, out, errOut := runLs(t, repo, "log"); code != 0 || out != "" || errOut != "" {
		t.Fatalf("log: exit %d, output %q, stderr %q; want 0 and nothing", code, out, errOut)
	}
	code, _, errOut := runLs(t, repo, "log", "latest")
	if code != 2 || errOut != "noahsark: log: no snapshot matches latest\n" {
		t.Fatalf("log latest: exit %d, stderr %q; want 2", code, errOut)
	}
}

// TestLogUsageErrorsExitTwo checks the usage errors of log.
func TestLogUsageErrorsExitTwo(t *testing.T) {
	repo := commitWithRef(t, "latest")
	for _, args := range [][]string{
		{"log", "a", "b"},
		{"log", "--no-such-flag"},
		{"log", "no-such-ref"},
	} {
		code, out, errOut := runLs(t, repo, args...)
		if code != 2 || out != "" {
			t.Fatalf("%q: exit %d, output %q, stderr %q; want 2", args, code, out, errOut)
		}
	}
}

// appendRef adds the ref name for the snapshot fullID to the local ref
// file of repo.
func appendRef(t *testing.T, repo, name, fullID string) {
	t.Helper()
	f, err := os.OpenFile(testLayout(t, repo).refsFile(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(name + " " + fullID + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// logID gives the id field that log prints for the snapshot with the
// full text id fullID.
func logID(t *testing.T, fullID string) string {
	t.Helper()
	id, err := object.ParseID(fullID)
	if err != nil {
		t.Fatal(err)
	}
	return shortID(id)
}
