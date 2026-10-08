package main

import (
	"fmt"
	"io"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/image"
	"github.com/tjjh89017/noahsark/internal/repolock"
	"github.com/tjjh89017/noahsark/internal/stage"
)

// session is the repository state that a command opens before its own
// work: the repository, its config and layout, the logs, and the disc
// ledger. The command holds the lock of the repository until close.
type session struct {
	cmd      string
	stderr   io.Writer
	repoDir  string
	cfg      repoConfig
	layout   repoLayout
	repoUUID [16]byte
	logs     *stage.Logs
	ledger   format.DiscsTable
	lock     *repolock.Lock
}

// openLockedSession finds the repository, reads its config, takes the
// lock, opens the logs for a write, and loads the disc ledger. cmd names
// the command in each message. On an error it prints the error, releases
// the lock, and returns the exit code with ok false.
func (e *env) openLockedSession(cmd string) (s *session, code int, ok bool) {
	stderr := e.stderr
	repoDir, err := e.findRepo()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return nil, 2, false
	}
	cfg, err := readConfig(configPath(repoDir))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "noahsark: %s: %v\n", cmd, err)
		return nil, configExitCode(err), false
	}
	lk, code, ok := lockRepo(cmd, repoDir, stderr)
	if !ok {
		return nil, code, false
	}
	s = &session{cmd: cmd, stderr: stderr, repoDir: repoDir, cfg: cfg, layout: layoutOf(repoDir, cfg), lock: lk}
	if err := s.load(); err != nil {
		s.close()
		return nil, s.fail(err), false
	}
	return s, 0, true
}

// load decodes the repository uuid, opens the logs and loads the disc
// ledger of s.
func (s *session) load() error {
	var err error
	if s.repoUUID, err = decodeUUID(s.cfg.RepoUUID); err != nil {
		return err
	}
	if s.logs, err = openLogs(s.cmd, s.layout, true, s.stderr); err != nil {
		return err
	}
	s.ledger, err = image.LoadDiscsLedger(s.layout.discsLedgerFile(), s.repoUUID)
	return err
}

// close releases the lock of s.
func (s *session) close() {
	releaseLock(s.lock)
}

// fail prints err as the error of the command and returns exit code 1.
func (s *session) fail(err error) int {
	_, _ = fmt.Fprintf(s.stderr, "noahsark: %s: %v\n", s.cmd, err)
	return 1
}

// disc resolves the disc argument arg against the ledger and the disc
// state log of s. On an error it prints the error and returns exit code
// 2 with ok false.
func (s *session) disc(arg string) (disc discTarget, code int, ok bool) {
	discUUID, err := resolveDisc(s.ledger.Rows, s.logs.Discs, arg)
	if err != nil {
		_, _ = fmt.Fprintf(s.stderr, "noahsark: %s: %v\n", s.cmd, err)
		return discTarget{}, 2, false
	}
	return discTargetOf(s.ledger.Rows, s.logs.Discs, discUUID), 0, true
}
