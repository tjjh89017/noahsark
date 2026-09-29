package main

import "github.com/tjjh89017/noahsark/internal/stage"

func init() {
	registerStateCases(
		stateCase{
			row: "20", name: "packed disc burned",
			start: stage.DiscPacked, args: []string{"disc", "burned", "{SEQ}"},
			end: stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "21", name: "burned disc burned again",
			start: stage.DiscBurned, args: []string{"disc", "burned", "{SEQ}"},
			end: stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "22", name: "verified disc burned",
			start: stage.DiscVerified, args: []string{"disc", "burned", "{SEQ}"},
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "22", name: "on disc only disc burned",
			start: stage.DiscOnDiscOnly, args: []string{"disc", "burned", "{SEQ}"},
			end: stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "23", name: "lost disc burned",
			start: stage.DiscLost, args: []string{"disc", "burned", "{SEQ}"},
			omit: []string{"disc SEQ is missing"},
			end:  stage.DiscLost,
		},
		stateCase{
			row: "23", name: "missing disc burned",
			start: stage.DiscMissing, args: []string{"disc", "burned", "{SEQ}"},
			omit: []string{"disc SEQ is marked lost"},
			end:  stage.DiscMissing,
		},
		stateCase{
			row: "24", name: "burn undone, answer yes",
			start: stage.DiscBurned, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin: stdinYes,
			end:   stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "24a", name: "burn undo, answer no",
			start: stage.DiscBurned, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin: stdinNo,
			end:   stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "25", name: "burn undo of a packed disc",
			start: stage.DiscPacked, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin:  stdinYes,
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "26", name: "burn undo of a verified disc",
			start: stage.DiscVerified, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin:  stdinYes,
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "26", name: "burn undo of an on disc only disc",
			start: stage.DiscOnDiscOnly, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin:  stdinYes,
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "26", name: "burn undo of a lost disc",
			start: stage.DiscLost, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin:  stdinYes,
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "26", name: "burn undo of a missing disc",
			start: stage.DiscMissing, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			stdin:  stdinYes,
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscMissing,
		},
		stateCase{
			row: "80", name: "burn undo with --yes", like: "24",
			start: stage.DiscBurned, args: []string{"--yes", "disc", "burned", "--undo", "{SEQ}"},
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "80", name: "burn undo with --force-yes", like: "24",
			start: stage.DiscBurned, args: []string{"--force-yes", "disc", "burned", "--undo", "{SEQ}"},
			end: stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "81", name: "burn undo with no terminal and no answer flag", like: "24",
			start: stage.DiscBurned, args: []string{"disc", "burned", "--undo", "{SEQ}"},
			absent: []string{confirmQuestion},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
	)
}
