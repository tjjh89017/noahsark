package main

import "github.com/tjjh89017/noahsark/internal/stage"

// statusVerifyLines are the lines of a block that verify a disc in the
// drive.
var statusVerifyLines = []string{
	"eject /dev/sr0 && eject -t /dev/sr0 && sleep 5 &&",
	"sudo mkdir -p /mnt/ark && sudo mount -o ro /dev/sr0 /mnt/ark &&",
	"noahsark verify /mnt/ark &&",
	"sudo umount /mnt/ark && eject /dev/sr0",
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
			end: stage.DiscPacked, word: stage.WordPacked,
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
				" --disc=/mnt/ark &&\n",
				"sudo umount /mnt/ark\n",
				"or, when disc {SEQ} is gone for good, run:\n",
				"noahsark disc lost {SEQ}\n",
			},
			end: stage.DiscMissing,
		},
	)
}
