package main

import (
	"fmt"

	"github.com/tjjh89017/noahsark/internal/stage"
)

// undoVerify implements "verify --undo DISC": it removes the verified
// record of a verified disc after an ordinary confirmation. It reads no
// disc.
func undoVerify(e *env, arg string) int {
	stdout, stderr := e.stdout, e.stderr
	const cmd = "verify"
	s, code, ok := e.openLockedSession(cmd)
	if !ok {
		return code
	}
	defer s.close()
	disc, code, ok := s.disc(arg)
	if !ok {
		return code
	}
	logs, discUUID := s.logs, disc.info.UUID

	switch disc.info.State {
	case stage.DiscVerified:
	case stage.DiscOnDiscOnly:
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s is on disc only; gc already freed the staged copy; verify cannot be undone\n", cmd, disc.short())
		return 1
	case stage.DiscPacked, stage.DiscBurned:
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s has no verified record\n", cmd, disc.short())
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %s\n", cmd, discStateRefusal(disc))
		return 1
	}
	warning := []string{
		disc.warning(stage.DiscBurned),
		"the disc is no longer verified, and gc holds its data",
	}
	if !e.confirm(confirmOrdinary, "verify --undo", warning) {
		return 1
	}
	if err := logs.Discs.Append(discEvent(e.now(), discUUID, stage.EventVerifyUndone)); err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s: verified record removed; burn record kept\n", disc.name())
	printNext(e, s.repoDir)
	return 0
}
