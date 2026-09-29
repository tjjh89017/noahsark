package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/object"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// partialSetup marks the one snapshot of the repository of fx partial
// in catalog-state.txt. {SHORT} and the placeholder ID of the cells are
// the 12-character id that ls and log print.
func partialSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	full := fullSnapshotID(t, fx.repo)
	if err := os.WriteFile(catalog.StatePath(fx.repo), []byte(full+" partial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.set("{SHORT}", logID(t, full))
	fx.cell("ID", logID(t, full))
}

// twoSnapshotsSetup commits the source of fx again, with a new file each
// time, until two snapshots share the first digit of their digest. The
// digest has 16 digits, thus at most 17 snapshots are needed. {PREFIX}
// and the placeholder ARG of the cells are that digit, and {SNAP1} and {SNAP2} are the full text ids of the
// two snapshots, in the order of the digest.
func twoSnapshotsSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	byDigit := map[string]string{}
	add := func(full string) bool {
		digit := full[4:5]
		first, ok := byDigit[digit]
		if !ok {
			byDigit[digit] = full
			return false
		}
		pair := []string{first, full}
		sort.Strings(pair)
		fx.set("{PREFIX}", digit)
		fx.cell("ARG", digit)
		fx.set("{SNAP1}", pair[0])
		fx.set("{SNAP2}", pair[1])
		return true
	}
	if add(fullSnapshotID(t, fx.repo)) {
		return
	}
	for i := range 17 {
		name := filepath.Join(fx.src, "extra-"+strings.Repeat("x", i+1)+".txt")
		if err := os.WriteFile(name, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		id, err := object.ParseID(snapshotIDFromCommit(t, fx.mustRun(t, "commit", fx.src)))
		if err != nil {
			t.Fatal(err)
		}
		if add(id.TextForm()) {
			return
		}
	}
	t.Fatal("no two snapshots share the first digit of their digest")
}

// logLineFirst is a check: the first line of log is the line of the
// snapshot {SHORT}.
func logLineFirst(t *testing.T, fx *discFixture, stdout, _ string) {
	t.Helper()
	if want := fx.filler()("{SHORT}\t"); !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout %q, want the line of the snapshot first", stdout)
	}
}

func init() {
	registerStateCases(
		// Row 79a: a snapshot argument that matches no snapshot is a usage
		// error. ls and log change no state and print no next line.
		stateCase{
			row: "79a", name: "ls of a snapshot that nothing matches",
			start: stage.DiscVerified, args: []string{"ls", "no-such-ref"},
			cells: map[string]string{"ARG": "no-such-ref"},
			exact: true, exactStderr: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "79a", name: "log of a snapshot that nothing matches",
			start: stage.DiscPacked, args: []string{"log", "no-such-ref"},
			cells: map[string]string{"ARG": "no-such-ref"},
			exact: true, exactStderr: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		// Row 79b: a prefix of two snapshots lists both, in the order of
		// the digest. The cell gives the form of a candidate line. also
		// names both lines.
		stateCase{
			row: "79b", name: "ls of a prefix of two snapshots",
			start: stage.DiscPacked, setup: twoSnapshotsSetup,
			args:  []string{"ls", "{PREFIX}"},
			also:  []string{"snapshot {SNAP1}\n", "snapshot {SNAP2}\n"},
			exact: true,
			end:   stage.DiscPacked, word: stage.WordPacked,
		},
		// Row 89: ls and log with no repository name recover.
		stateCase{
			row: "89", name: "ls with no repository",
			start: stage.DiscPacked, noRepo: true, args: []string{"ls", "latest"},
			exact: true, exactStderr: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "89", name: "log with no repository",
			start: stage.DiscPacked, noRepo: true, args: []string{"log"},
			exact: true, exactStderr: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		// Row 89a: ls of a partial snapshot reads no disc and exits 1.
		stateCase{
			row: "89a", name: "ls of a partial snapshot",
			start: stage.DiscPacked, setup: partialSetup,
			args:  []string{"ls", "{REF}"},
			exact: true, exactStderr: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		// Row 89b: log prints its lines, then names each partial snapshot
		// on standard error, and exits 1.
		stateCase{
			row: "89b", name: "log with a partial snapshot",
			start: stage.DiscPacked, setup: partialSetup,
			args:        []string{"log"},
			exactStderr: true,
			end:         stage.DiscPacked, word: stage.WordPacked,
			check: logLineFirst,
		},
		stateCase{
			row: "89b", name: "log of a partial snapshot by its ref",
			start: stage.DiscPacked, setup: partialSetup,
			args:        []string{"log", "{REF}"},
			exactStderr: true,
			end:         stage.DiscPacked, word: stage.WordPacked,
			check: logLineFirst,
		},
	)
}
