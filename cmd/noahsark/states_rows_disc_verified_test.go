package main

import "github.com/tjjh89017/noahsark/internal/stage"

func init() {
	registerStateCases(
		stateCase{
			row: "27", name: "disc verified, answer yes",
			start: stage.DiscBurned, args: []string{"disc", "verified", "{SEQ}"},
			stdin: stdinYes,
			end:   stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "28", name: "disc verified, answer no",
			start: stage.DiscBurned, args: []string{"disc", "verified", "{SEQ}"},
			stdin:  stdinNo,
			absent: []string{"needs --force-yes"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "29", name: "disc verified of a packed disc",
			start: stage.DiscPacked, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscPacked, word: stage.WordPacked,
		},
		stateCase{
			row: "30", name: "disc verified of a verified disc",
			start: stage.DiscVerified, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			omit:   []string{"disc SEQ is marked lost", "disc SEQ is missing"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "30", name: "disc verified of an on disc only disc",
			start: stage.DiscOnDiscOnly, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			omit:   []string{"disc SEQ is marked lost", "disc SEQ is missing"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscOnDiscOnly, word: stage.WordOnDisc,
		},
		stateCase{
			row: "30", name: "disc verified of a lost disc",
			start: stage.DiscLost, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			omit:   []string{"disc SEQ is already verified", "disc SEQ is missing"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscLost,
		},
		stateCase{
			row: "30", name: "disc verified of a missing disc",
			start: stage.DiscMissing, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			omit:   []string{"disc SEQ is already verified", "disc SEQ is marked lost"},
			absent: []string{confirmQuestion, "warning:"},
			end:    stage.DiscMissing,
		},
		stateCase{
			row: "82", name: "disc verified with --force-yes", like: "27",
			start: stage.DiscBurned, args: []string{"--force-yes", "disc", "verified", "{SEQ}"},
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "83", name: "disc verified with --yes and no terminal", like: "27",
			start: stage.DiscBurned, args: []string{"--yes", "disc", "verified", "{SEQ}"},
			omit:   []string{"nothing changed; disc lost needs --force-yes"},
			absent: []string{confirmQuestion},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "28", name: "disc verified with no terminal and no answer flag",
			start: stage.DiscBurned, args: []string{"disc", "verified", "{SEQ}"},
			absent: []string{confirmQuestion, "needs --force-yes"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
		stateCase{
			row: "84", name: "disc verified with --yes, answer yes typed", like: "27",
			start: stage.DiscBurned, args: []string{"--yes", "disc", "verified", "{SEQ}"},
			stdin: stdinYes,
			end:   stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "84a", name: "disc verified with --yes, answer no typed", like: "27",
			start: stage.DiscBurned, args: []string{"--yes", "disc", "verified", "{SEQ}"},
			stdin:  stdinNo,
			absent: []string{"needs --force-yes"},
			end:    stage.DiscBurned, word: stage.WordBurned,
		},
	)
}
