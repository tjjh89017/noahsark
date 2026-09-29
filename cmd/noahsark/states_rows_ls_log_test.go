package main

import "github.com/tjjh89017/noahsark/internal/stage"

func init() {
	// Row 79a: a snapshot argument that matches no snapshot is a usage
	// error. ls and log change no state and print no next line. The
	// harness always gives a repository, thus TestLsNoRepository and
	// TestLogNoRepository test row 89, and TestLsPartialSnapshot and
	// TestLogPartialSnapshot test rows 89a and 89b.
	registerStateCases(
		stateCase{
			row: "79a", name: "ls of a snapshot that nothing matches",
			start: stage.DiscVerified, args: []string{"ls", "no-such-ref"},
			exit: 2, stderr: []string{"no snapshot matches no-such-ref\n"},
			end: stage.DiscVerified, word: stage.WordClean,
		},
		stateCase{
			row: "79a", name: "log of a snapshot that nothing matches",
			start: stage.DiscPacked, args: []string{"log", "no-such-ref"},
			exit: 2, stderr: []string{"no snapshot matches no-such-ref\n"},
			end: stage.DiscPacked, word: stage.WordPacked,
		},
	)
}
