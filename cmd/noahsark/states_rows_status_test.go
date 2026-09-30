package main

import (
	"os"
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
				"next: nothing to do; gc can free disc {SEQ} after ",
				`advice: copy disc {SEQ} before gc; see the guide, "A second copy"`,
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
			args:  []string{"gc", "--force-after=0d"},
			exact: true, exactStderr: true, noEvent: true, sameCatalog: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
	)
}
