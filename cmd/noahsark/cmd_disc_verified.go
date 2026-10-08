package main

import (
	"flag"
	"fmt"
	"strconv"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// "disc verified" records a verified disc on the word of the operator. It
// reads no disc. The gc wait counts from the time of its event.
func init() {
	register(&command{
		name:    "verified",
		group:   "disc",
		usage:   "disc verified DISC",
		summary: "Record a burned disc as verified without a check.",
		flags:   discVerifiedFlags,
	})
}

func discVerifiedFlags(*flag.FlagSet) runFunc {
	return runDiscVerified
}

// runDiscVerified implements "noahsark disc verified DISC". docs/states.md,
// rows 27 to 30, gives the messages.
func runDiscVerified(e *env, args []string) int {
	stdout, stderr := e.stdout, e.stderr
	const cmd = "disc verified"
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: noahsark disc verified DISC")
		return 2
	}

	s, code, ok := e.openLockedSession(cmd)
	if !ok {
		return code
	}
	defer s.close()
	disc, code, ok := s.disc(args[0])
	if !ok {
		return code
	}
	logs, ledger, discUUID := s.logs, s.ledger, disc.info.UUID

	if refusal := discVerifiedRefusal(disc, discCommandArg(ledger.Rows, logs.Discs, disc)); refusal != "" {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s\n", cmd, refusal)
		return 1
	}
	warning := []string{
		disc.warning(stage.DiscVerified),
		"the tool did not read this disc; gc can free the repository copy of its data at once; if the disc is bad, that data is lost",
	}
	if !e.confirm(confirmCritical, cmd, warning) {
		return 1
	}
	if err := logs.Discs.Append(discEvent(e.now(), discUUID, stage.EventMarkedVerified)); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s: verified record added; not checked\n", disc.name())
	_, _ = fmt.Fprintln(stdout, nextStatusLine)
	return 0
}

// discVerifiedRefusal returns the refusal of disc verified for the state
// of disc, or an empty string when disc verified asks its confirmation.
// arg is the disc argument of the command line in the refusal.
func discVerifiedRefusal(disc discTarget, arg string) string {
	switch disc.info.State {
	case stage.DiscBurned:
		return ""
	case stage.DiscPacked:
		return fmt.Sprintf("%s has no burn record; run: noahsark disc burned %s, or verify the disc", disc.short(), arg)
	case stage.DiscVerified, stage.DiscOnDiscOnly:
		return disc.short() + " is already verified"
	}
	return discStateRefusal(disc)
}

// discCommandArg is the disc argument that names disc in a printed
// command line: the disc number, or the full uuid when the number does
// not name this one disc.
func discCommandArg(rows []format.DiscsRow, discs *stage.DiscLog, disc discTarget) string {
	seq := strconv.FormatUint(disc.seq, 10)
	if got, err := resolveDisc(rows, discs, seq); err == nil && got == disc.info.UUID {
		return seq
	}
	return uuidText(disc.info.UUID)
}
