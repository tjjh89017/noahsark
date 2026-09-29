package main

import "github.com/tjjh89017/noahsark/internal/stage"

// markWarning is the warning of "disc verified" in rows 27, 28, 82, 83,
// 84 and 84a.
var markWarning = []string{
	`warning: {DISC} ({UUID}): burned -> verified`,
	"the tool did not read this disc; gc frees the repository copy of its data after the wait time; if the disc is bad, that data is lost",
}

// markAdded is the message of "disc verified" after the change.
const markAdded = `{DISC}: verified record added; not checked`

func init() {
	registerStateCases(
		stateCase{
			row: "27", name: "disc verified, answer yes",
			start: stage.DiscBurned, args: []string{"disc", "verified", "{SEQ}"},
			stdin:  stdinYes,
			stderr: append(markWarning, confirmQuestion),
			stdout: []string{markAdded}, next: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "28", name: "disc verified, answer no",
			start: stage.DiscBurned, args: []string{"disc", "verified", "{SEQ}"},
			stdin: stdinNo, exit: 1,
			stderr: append(markWarning, confirmQuestion),
			stdout: []string{"nothing changed"},
			absent: []string{"needs --force-yes"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "29", name: "disc verified of a packed disc",
			start: stage.DiscPacked, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			exit:   1,
			stderr: []string{"disc {SEQ} has no burn record; run: noahsark disc burned {SEQ}, or verify the disc"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "30", name: "disc verified of a verified disc",
			start: stage.DiscVerified, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			exit: 1, stderr: []string{"disc {SEQ} is already verified"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "30", name: "disc verified of an on disc only disc",
			start: stage.DiscOnDiscOnly, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			exit: 1, stderr: []string{"disc {SEQ} is already verified"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "30", name: "disc verified of a lost disc",
			start: stage.DiscLost, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			exit: 1, stderr: []string{"disc {SEQ} is marked lost"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "30", name: "disc verified of a missing disc",
			start: stage.DiscMissing, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			exit: 1, stderr: []string{"disc {SEQ} is missing"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscMissing,
		},
		stateCase{
			row: "82", name: "disc verified with --force-yes",
			start: stage.DiscBurned, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			stderr: markWarning, absent: []string{confirmQuestion},
			stdout: []string{markAdded}, next: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "83", name: "disc verified with --yes and no terminal",
			start: stage.DiscBurned, args: []string{"--yes", "disc", "verified", "{SEQ}"},
			exit:   1,
			stderr: markWarning, absent: []string{confirmQuestion},
			stdout: []string{"nothing changed; disc verified needs --force-yes"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "28", name: "disc verified with no terminal and no answer flag",
			start: stage.DiscBurned, args: []string{"disc", "verified", "{SEQ}"},
			exit:   1,
			stderr: markWarning, absent: []string{confirmQuestion, "needs --force-yes"},
			stdout: []string{"nothing changed"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "84", name: "disc verified with --yes, answer yes typed",
			start: stage.DiscBurned, args: []string{"--yes", "disc", "verified", "{SEQ}"},
			stdin:  stdinYes,
			stderr: append(markWarning, confirmQuestion),
			stdout: []string{markAdded}, next: true,
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "84a", name: "disc verified with --yes, answer no typed",
			start: stage.DiscBurned, args: []string{"--yes", "disc", "verified", "{SEQ}"},
			stdin: stdinNo, exit: 1,
			stderr: append(markWarning, confirmQuestion),
			stdout: []string{"nothing changed"},
			absent: []string{"needs --force-yes"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
	)
}
