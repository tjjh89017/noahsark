package main

import "github.com/tjjh89017/noahsark/internal/stage"

// undoWarning is the warning of "disc burned --undo" in rows 24, 24a, 80
// and 81.
var undoWarning = []string{
	`warning: {DISC} ({UUID}): burned -> packed`,
	"the burn record is removed; burn the disc again from its disc root",
}

func init() {
	registerStateCases(
		stateCase{
			row: "20", name: "packed disc burned",
			start: stage.DiscPacked, args: []string{"disc", "burned", "{SEQ}"},
			stdout: []string{`{DISC}: burn recorded`}, next: true,
			end: stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "21", name: "burned disc burned again",
			start: stage.DiscBurned, args: []string{"disc", "burned", "{SEQ}"},
			exit: 1, stderr: []string{"disc {SEQ} already has a burn record"},
			end: stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "22", name: "verified disc burned",
			start: stage.DiscVerified, args: []string{"disc", "burned", "{SEQ}"},
			exit: 1, stderr: []string{"disc {SEQ} is already verified"},
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "22", name: "on disc only disc burned",
			start: stage.DiscOnDiscOnly, args: []string{"disc", "burned", "{SEQ}"},
			exit: 1, stderr: []string{"disc {SEQ} is already verified"},
			end: stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "23", name: "lost disc burned",
			start: stage.DiscLost, args: []string{"disc", "burned", "{SEQ}"},
			exit: 1, stderr: []string{"disc {SEQ} is marked lost"},
			end: stage.DiscLost,
		},
		stateCase{
			row: "23", name: "missing disc burned",
			start: stage.DiscMissing, args: []string{"disc", "burned", "{SEQ}"},
			exit: 1, stderr: []string{"disc {SEQ} is missing"},
			end: stage.DiscMissing,
		},
		stateCase{
			row: "24", name: "burn undone, answer yes",
			start: stage.DiscBurned, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin:  stdinYes,
			stderr: append(undoWarning, confirmQuestion),
			stdout: []string{`{DISC}: burn record removed`}, next: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "24a", name: "burn undo, answer no",
			start: stage.DiscBurned, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin: stdinNo, exit: 1,
			stderr: append(undoWarning, confirmQuestion),
			stdout: []string{"nothing changed"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "25", name: "burn undo of a packed disc",
			start: stage.DiscPacked, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin: stdinYes, exit: 1,
			stderr: []string{"disc {SEQ} has no burn record"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "26", name: "burn undo of a verified disc",
			start: stage.DiscVerified, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin: stdinYes, exit: 1,
			stderr: []string{"disc {SEQ} is not burned"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "26", name: "burn undo of an on disc only disc",
			start: stage.DiscOnDiscOnly, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin: stdinYes, exit: 1,
			stderr: []string{"disc {SEQ} is not burned"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "26", name: "burn undo of a lost disc",
			start: stage.DiscLost, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin: stdinYes, exit: 1,
			stderr: []string{"disc {SEQ} is not burned"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "26", name: "burn undo of a missing disc",
			start: stage.DiscMissing, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin: stdinYes, exit: 1,
			stderr: []string{"disc {SEQ} is not burned"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscMissing,
		},
		stateCase{
			row: "80", name: "burn undo with --yes",
			start: stage.DiscBurned, args: []string{"--yes", "disc", "burned", "--undo", "{SEQ}"},
			stderr: undoWarning, absent: []string{confirmQuestion},
			stdout: []string{`{DISC}: burn record removed`}, next: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "80", name: "burn undo with --force-yes",
			start: stage.DiscBurned, args: []string{"--force-yes", "disc", "burned", "--undo", "{SEQ}"},
			stderr: undoWarning, absent: []string{confirmQuestion},
			stdout: []string{`{DISC}: burn record removed`}, next: true,
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "81", name: "burn undo with no terminal and no answer flag",
			start: stage.DiscBurned, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			exit:   1,
			stderr: undoWarning, absent: []string{confirmQuestion},
			stdout: []string{"nothing changed"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
	)
}
