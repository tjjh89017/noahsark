package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// testSnapID makes a snapshot id from the hexadecimal text of the start of
// its digest. The other digest bytes are zero.
func testSnapID(t *testing.T, digestHex string) object.ID {
	t.Helper()
	id, err := object.ParseID("1220" + digestHex + strings.Repeat("0", 64-len(digestHex)))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// snapArgFixture makes a catalog that lists the snapshots ids. It writes
// an empty file for each snapshot: the prefix search reads only the
// file names. The catalog holds no disc and refs.txt does not exist.
func snapArgFixture(t *testing.T, ids ...object.ID) *catalogSource {
	t.Helper()
	repo := t.TempDir()
	c, err := catalog.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		path := c.MetaPath(format.ObjectKindSnapshot, id)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &catalogSource{c: c, refsPath: filepath.Join(repo, "refs.txt")}
}

// addDiscRefs writes a disc into the catalog whose REFS table holds
// recs.
func addDiscRefs(t *testing.T, s *catalogSource, uuid [16]byte, recs ...format.RefRecord) {
	t.Helper()
	table := format.RefsTable{
		Header: format.CommonHeader{
			MagicProject: format.ProjectMagic, MagicKind: format.MagicRefs,
			VersionMajor: 1, HeaderLen: format.RefsHeaderLen,
		},
		RecordCount: uint64(len(recs)), Records: recs,
	}
	buf := make([]byte, table.EncodedLen())
	if _, err := table.Encode(buf); err != nil {
		t.Fatal(err)
	}
	if err := s.c.WriteDisc(uuid, []byte("index"), buf, []byte("discs")); err != nil {
		t.Fatal(err)
	}
}

func refRec(name string, id object.ID, sec int64) format.RefRecord {
	r := format.RefRecord{SnapshotID: id, TimeSec: sec, NameLen: uint16(len(name))}
	copy(r.Name[:], name)
	return r
}

func mustResolve(t *testing.T, s *catalogSource, arg string, want object.ID) {
	t.Helper()
	got, err := s.ParseSnapshotArg(arg)
	if err != nil {
		t.Fatalf("ParseSnapshotArg(%q): %v", arg, err)
	}
	if got != want {
		t.Fatalf("ParseSnapshotArg(%q) = %s, want %s", arg, got.TextForm(), want.TextForm())
	}
}

func TestShortID(t *testing.T) {
	id := testSnapID(t, "0123456789abcdef")
	if got := shortID(id); got != "0123456789ab" {
		t.Fatalf("shortID = %q, want 0123456789ab", got)
	}
}

func TestSnapshotArgByID(t *testing.T) {
	a := testSnapID(t, "abcdef0123456789")
	b := testSnapID(t, "abd0")
	c := testSnapID(t, "9f")
	s := snapArgFixture(t, a, b, c)

	cases := []struct {
		name, arg string
		want      object.ID
	}{
		{"full text id", a.TextForm(), a},
		{"full text id in upper case", strings.ToUpper(a.TextForm()), a},
		{"full digest", a.TextForm()[4:], a},
		{"12-character prefix", shortID(a), a},
		{"shorter unique prefix", "abc", a},
		{"one character, the shortest prefix", "9", c},
		{"upper case prefix", "ABCDEF", a},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustResolve(t, s, tc.arg, tc.want)
		})
	}
}

// TestSnapshotArgAmbiguous checks that a prefix of two snapshots is a
// usage error that lists both candidates in their full text form.
func TestSnapshotArgAmbiguous(t *testing.T) {
	a := testSnapID(t, "abcdef")
	b := testSnapID(t, "abd0")
	s := snapArgFixture(t, a, b)

	_, err := s.ParseSnapshotArg("AB")
	if _, ok := errors.AsType[*ambiguousSnapshotError](err); !ok {
		t.Fatalf("ParseSnapshotArg(AB): err %v, want an ambiguous error", err)
	}
	if code := exitForSnapshotArg(err); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	want := "AB matches more than one snapshot:\nsnapshot " + a.TextForm() + "\nsnapshot " + b.TextForm()
	if err.Error() != want {
		t.Fatalf("message %q, want %q", err.Error(), want)
	}
}

func TestSnapshotArgNoMatch(t *testing.T) {
	s := snapArgFixture(t, testSnapID(t, "abcdef"))
	addDiscRefs(t, s, [16]byte{1}, refRec("daily", testSnapID(t, "abcdef"), 10))

	for _, arg := range []string{"abd", "no-such-ref", "g1"} {
		_, err := s.ParseSnapshotArg(arg)
		if err == nil || err.Error() != "no snapshot matches "+arg {
			t.Fatalf("ParseSnapshotArg(%q): err %v, want no snapshot matches", arg, err)
		}
		if code := exitForSnapshotArg(err); code != 2 {
			t.Fatalf("ParseSnapshotArg(%q): exit %d, want 2", arg, code)
		}
	}
}

// TestSnapshotArgEmptyCatalog checks that a catalog with no disc and no
// local ref is a failure at run time when nothing matches, but that a
// prefix of a committed snapshot still resolves.
func TestSnapshotArgEmptyCatalog(t *testing.T) {
	a := testSnapID(t, "abcdef")
	s := snapArgFixture(t, a)

	_, err := s.ParseSnapshotArg("latest")
	if !errors.Is(err, catalog.ErrNoDisc) || exitForSnapshotArg(err) != 1 {
		t.Fatalf("ParseSnapshotArg(latest): err %v, want ErrNoDisc with exit 1", err)
	}
	mustResolve(t, s, "abc", a)
}

func TestSnapshotArgByRef(t *testing.T) {
	older := testSnapID(t, "1111")
	newer := testSnapID(t, "2222")
	local := testSnapID(t, "3333")
	s := snapArgFixture(t, older, newer, local)
	// The disc with the lower uuid carries the newer record.
	addDiscRefs(t, s, [16]byte{1}, refRec("daily", newer, 200))
	addDiscRefs(t, s, [16]byte{2}, refRec("daily", older, 100), refRec("weekly", older, 100))
	if err := os.WriteFile(s.refsPath, []byte("only-local "+local.TextForm()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("ref name", func(t *testing.T) { mustResolve(t, s, "weekly", older) })
	t.Run("newest of two discs wins", func(t *testing.T) { mustResolve(t, s, "daily", newer) })
	t.Run("ref only in refs.txt", func(t *testing.T) { mustResolve(t, s, "only-local", local) })
	t.Run("ref names are case sensitive", func(t *testing.T) {
		if _, err := s.ParseSnapshotArg("DAILY"); err == nil {
			t.Fatal("DAILY resolved, want no match")
		}
	})
}

// TestSnapshotArgRefWinsOverPrefix checks that a ref name that is also
// a valid prefix of another snapshot resolves as the ref.
func TestSnapshotArgRefWinsOverPrefix(t *testing.T) {
	prefixed := testSnapID(t, "cafe")
	named := testSnapID(t, "1234")
	s := snapArgFixture(t, prefixed, named)
	addDiscRefs(t, s, [16]byte{1}, refRec("cafe", named, 10))

	mustResolve(t, s, "cafe", named)
	mustResolve(t, s, "caf", prefixed)
}

// TestSnapshotArgRefTarget checks that a snapshot that a ref names is a
// candidate of a prefix, also when the catalog does not hold its object.
func TestSnapshotArgRefTarget(t *testing.T) {
	s := snapArgFixture(t)
	absent := testSnapID(t, "beef")
	addDiscRefs(t, s, [16]byte{1}, refRec("old", absent, 10))

	mustResolve(t, s, "bee", absent)
}
