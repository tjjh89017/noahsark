package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// statusVerifyLines are the lines of a block that verify a disc in the
// drive. The unmount line follows a ";", so that it also runs after a
// failed verify.
var statusVerifyLines = []string{
	"eject /dev/sr0 && eject -t /dev/sr0 && sleep 5 &&\n",
	"sudo mkdir -p /mnt/ark && sudo mount -o ro /dev/sr0 /mnt/ark &&\n",
	"noahsark verify /mnt/ark;\n",
	"sudo umount /mnt/ark && eject /dev/sr0\n",
}

// commitBlock is the block of a lost disc whose data waits for a commit.
var commitBlock = []string{
	"next: disc {SEQ} is lost; a new commit stages what the source still holds; run:\n",
	"noahsark commit\n",
}

// laterClockSetup puts the clock of the fake env one hour ahead, so that
// each event after it is newer than each snapshot before it.
func laterClockSetup(t *testing.T, _ *discFixture) {
	t.Helper()
	setFakeNow(t, func() time.Time { return time.Now().Add(time.Hour) })
}

// statusShows is a check: the output of status holds each of lines, in
// order.
func statusShows(lines ...string) func(*testing.T, *discFixture, string, string) {
	return func(t *testing.T, fx *discFixture, _, _ string) {
		t.Helper()
		_, out := fx.run(t, "status")
		wantInOrder(t, "status", out, lines, fx.filler())
	}
}

// statusLacks is a check: the output of status holds none of texts.
func statusLacks(texts ...string) func(*testing.T, *discFixture, string, string) {
	return func(t *testing.T, fx *discFixture, _, _ string) {
		t.Helper()
		_, out := fx.run(t, "status")
		fill := fx.filler()
		for _, text := range texts {
			if strings.Contains(out, fill(text)) {
				t.Errorf("status output %q holds %q", out, fill(text))
			}
		}
	}
}

// stagingGoneSetup moves the staging directory of the repository of fx
// away, as an unmounted volume does. {STAGING} is its path.
func stagingGoneSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	staging := testLayout(t, fx.repo).stagingDir()
	if err := os.Rename(staging, staging+".away"); err != nil {
		t.Fatal(err)
	}
	fx.set("{STAGING}", staging)
	fx.cell("DIR", staging)
}

func init() {
	// Row 71: status changes no state, prints the staged line, one line
	// for each disc and the next block of the repository, and exits 0.
	registerStateCases(
		stateCase{
			row: "71", name: "status of a packed disc",
			start: stage.DiscPacked, args: []string{"status"},
			cells: map[string]string{"N": "0", "B": "0"},
			also: append(append([]string{
				"next: load a blank disc, then run:\n",
				"image build {SEQ} &&\n",
				"growisofs -speed=4 -use-the-force-luke=spare:min,tty -Z /dev/sr0=",
			}, statusVerifyLines...), folderBurnPointer),
			absent: []string{"noahsark verify /mnt/ark &&"},
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "71", name: "status of a burned disc",
			start: stage.DiscBurned, args: []string{"status"},
			cells: map[string]string{"N": "0", "B": "0"},
			also: append([]string{
				"next: load disc {SEQ}, then run:\n",
			}, statusVerifyLines...),
			absent: []string{"growisofs", folderBurnPointer},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "71", name: "status of a verified disc",
			start: stage.DiscVerified, args: []string{"status"},
			cells: map[string]string{"N": "0", "B": "0"},
			also: []string{
				`advice: copy disc {SEQ} before gc; see the guide, "A second copy"` + "\n",
				"next: noahsark gc\n",
			},
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "71", name: "status of an on disc only disc",
			start: stage.DiscOnDiscOnly, args: []string{"status"},
			cells: map[string]string{"N": "0", "B": "0"},
			also: []string{
				"next: nothing to do\n",
			},
			absent: []string{"advice:"},
			end:    stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "71", name: "status of a lost disc",
			start: stage.DiscLost, args: []string{"status"},
			also: []string{
				"next: load a blank disc, then run:\n",
				"dvd+rw-mediainfo /dev/sr0 | grep -E 'Mounted Media|Free Blocks'\n",
				"then paste this line, type the capacity, and press Enter:\n",
				"noahsark pack --capacity=\n",
			},
			absent: []string{"staged: 0 items"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "71", name: "status of a missing disc",
			start: stage.DiscMissing, args: []string{"status"},
			also: []string{
				`next: load disc {SEQ} "{LABEL}", then run:` + "\n",
				"sudo mkdir -p /mnt/ark && sudo mount -o ro /dev/sr0 /mnt/ark &&\n",
				"noahsark recover --source=",
				" --disc=/mnt/ark;\n",
				"sudo umount /mnt/ark && eject /dev/sr0\n",
				"or, when disc {SEQ} is gone for good, run:\n",
				"noahsark disc lost {SEQ}\n",
			},
			end: stage.DiscMissing,
		},
		// Row 71b: the staging directory is gone with its volume. The
		// packed disc gets no disc lost block.
		stateCase{
			row: "71b", name: "status with no staging directory",
			start: stage.DiscPacked, setup: stagingGoneSetup,
			args: []string{"status"},
			also: []string{
				"next: staging directory {STAGING} does not exist. Mount its volume, or correct staging.dir in config.yaml. When the staging store is gone for good, run:\n",
				"mkdir -p {STAGING}\n",
				"noahsark: status: warning: staging directory {STAGING} does not exist; staging.dir in config.yaml names it\n",
			},
			absent:      []string{"disc lost", "staged:", "no disc root"},
			exactStderr: true, noEvent: true, sameCatalog: true,
			end: stage.DiscPacked, word: stage.WordPacked,
			check: func(t *testing.T, fx *discFixture, _, _ string) {
				if _, err := os.Stat(fx.vars["{STAGING}"]); !os.IsNotExist(err) {
					t.Errorf("status created the staging directory: %v", err)
				}
			},
		},
		// Row 71c: a command that takes the lock refuses, and creates no
		// staging directory.
		stateCase{
			row: "71c", name: "disc burned with no staging directory",
			start: stage.DiscPacked, setup: stagingGoneSetup,
			args:  []string{"disc", "burned", "{SEQ}"},
			exact: true, exactStderr: true, noEvent: true, sameCatalog: true,
			end: stage.DiscPacked, word: stage.WordPacked,
			check: func(t *testing.T, fx *discFixture, _, _ string) {
				if _, err := os.Stat(fx.vars["{STAGING}"]); !os.IsNotExist(err) {
					t.Errorf("disc burned created the staging directory: %v", err)
				}
			},
		},
		stateCase{
			row: "71c", name: "gc with no staging directory",
			start: stage.DiscVerified, setup: stagingGoneSetup,
			args:  []string{"gc"},
			exact: true, exactStderr: true, noEvent: true, sameCatalog: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "71c", name: "commit with no staging directory",
			start: stage.DiscPacked, setup: stagingGoneSetup,
			args:  []string{"commit", "{SRC}"},
			exact: true, exactStderr: true, noEvent: true, sameCatalog: true,
			end: stage.DiscPacked, word: stage.WordPacked,
			check: func(t *testing.T, fx *discFixture, _, _ string) {
				if _, err := os.Stat(fx.vars["{STAGING}"]); !os.IsNotExist(err) {
					t.Errorf("commit created the staging directory: %v", err)
				}
			},
		},
		// Row 71d: no Staged and no Packed item needs the staging
		// directory. status gives no warning and exits 0.
		stateCase{
			row: "71d", name: "status of an on disc only disc with no staging directory",
			start: stage.DiscOnDiscOnly, setup: stagingGoneSetup,
			args:    []string{"status"},
			cells:   map[string]string{"N": "0", "B": "0"},
			also:    []string{"next: nothing to do\n"},
			absent:  []string{"warning:", "staging directory"},
			noEvent: true, sameCatalog: true,
			end: stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		// Row 71e: the freed chunks of a lost disc are lost. status counts
		// them on the lost line, before the disc lines.
		stateCase{
			row: "71e", name: "status after disc lost of an on disc only disc",
			start: stage.DiscOnDiscOnly, from: stage.DiscLost, setup: lostCountSetup,
			args:    []string{"status"},
			also:    []string{"\nlost: {LOST} items; only a lost disc holds them\ndisc {SEQ} "},
			noEvent: true, sameCatalog: true,
			end: stage.DiscLost, word: stage.WordLost,
		},
		// Row 71e after a commit of a source that no longer holds one
		// file: the chunk of that file stays lost, and so does the line.
		stateCase{
			row: "71e", name: "the lost line stays after a commit",
			start: stage.DiscOnDiscOnly, from: stage.DiscLost,
			setup: func(t *testing.T, fx *discFixture) {
				lostSetup(t, fx)
				if err := os.Remove(filepath.Join(fx.src, "a.txt")); err != nil {
					t.Fatal(err)
				}
				fx.mustRun(t, "commit", fx.src)
				lostCountSetup(t, fx)
				if fixtureNumber(t, fx, "{LOST}") != 1 {
					t.Fatalf("%s items stay lost, want 1: the chunk of a.txt", fx.vars["{LOST}"])
				}
			},
			args:    []string{"status"},
			noEvent: true, sameCatalog: true,
			end: stage.DiscLost, word: stage.WordLost,
		},
	)
}

// lostCountSetup marks the disc of fx lost with disc lost, when it is
// not lost yet. Then {LOST} and the cell I are the number of Lost items.
func lostCountSetup(t *testing.T, fx *discFixture) {
	t.Helper()
	if discState(t, fx.repo, fx.uuid).State != stage.DiscLost {
		lostSetup(t, fx)
	}
	n := strconv.Itoa(countByState(t, fx.repo, stage.Lost))
	fx.set("{LOST}", n)
	fx.cell("I", n)
	fx.cell("N", "")
}
