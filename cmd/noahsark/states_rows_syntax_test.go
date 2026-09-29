package main

import (
	"testing"

	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// sameNumberSetup adds a ledger row for a second disc with the number
// of the disc of fx and the label "other". {UUID2} is its uuid. Its
// uuid differs from the uuid of fx in the last byte only.
func sameNumberSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	cfg, err := readConfig(configPath(fx.repo))
	if err != nil {
		t.Fatal(err)
	}
	repoUUID, err := decodeUUID(cfg.RepoUUID)
	if err != nil {
		t.Fatal(err)
	}
	other := discArgRow(fx.seq, "other", 0)
	other.DiscUUID = fx.uuidBytes(t)
	other.DiscUUID[15] ^= 0xff
	rows := append(ledgerRows(t, fx.repo), other)
	if err := image.SaveDiscsLedger(testLayout(t, fx.repo).discsLedgerFile(), repoUUID, rows); err != nil {
		t.Fatal(err)
	}
	fx.set("{UUID2}", uuidText(other.DiscUUID))
}

// usageError is a case of a usage error. It changes no state of the
// packed disc of the fixture, writes nothing, and prints only the lines
// of the cell.
func usageError(row, name string, noRepo bool, args []string, cells map[string]string) stateCase {
	return stateCase{
		row: row, name: name,
		start: stage.DiscPacked, noRepo: noRepo, args: args, cells: cells,
		exact: true, exactStderr: true, noEvent: true, sameCatalog: true,
		end: stage.DiscPacked, word: stage.WordPacked,
	}
}

func init() {
	global := usageError("72", "a global option after the command name", true, []string{"status", "--repo={REPO}"}, nil)
	global.setup = func(t *testing.T, fx *discFixture) { fx.cell("PATH", fx.repo) }
	registerStateCases(
		usageError("71a", "status with no repository", true, []string{"status"}, nil),
		usageError("71a", "disc burned with no repository", true, []string{"disc", "burned", "0"}, nil),
		global,
		usageError("73", "a command option before the command name", true, []string{"--undo", "disc", "burned", "0"}, nil),
		usageError("73", "a command option between a group and its subcommand", true, []string{"disc", "--undo", "burned", "0"}, nil),
		usageError("74", "a group with no subcommand", false, []string{"disc"}, nil),
		usageError("75", "an unknown subcommand", false, []string{"disc", "burnt", "0"}, nil),
		// The Message cell of row 76 gives no line.
		stateCase{
			row: "76", name: "-h after a group",
			start: stage.DiscPacked, args: []string{"disc", "-h"},
			also: []string{"  burned", "  lost", "  verified"}, noEvent: true, sameCatalog: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "76", name: "-h before a group",
			start: stage.DiscPacked, args: []string{"-h", "disc"},
			also: []string{"  burned", "  lost", "  verified"}, noEvent: true, sameCatalog: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		usageError("77", "init with --repo", true, []string{"--repo={REPO}", "init"}, nil),
		usageError("78", "a disc argument that matches no disc", false, []string{"disc", "burned", "7"}, map[string]string{"ARG": "7"}),
		// The cell gives the form of a candidate line. also names the
		// line of the second candidate.
		stateCase{
			row: "79", name: "a disc number of two discs",
			start: stage.DiscPacked, setup: sameNumberSetup,
			args:  []string{"disc", "burned", "{SEQ}"},
			cells: map[string]string{"ARG": "0"},
			also:  []string{`disc {SEQ} "other"  {UUID2}` + "\n"},
			exact: true, noEvent: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
	)
}
