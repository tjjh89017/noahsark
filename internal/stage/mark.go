package stage

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MarkFileName is the name of the sequence mark in the staging directory.
// The mark holds the last sequence of each log that a command with the
// repository lock saw. Git does not track the staging directory, thus the
// mark does not go back when state/ goes back.
const MarkFileName = "state-seq.txt"

// Rollback is one log whose last sequence is below the sequence of the
// mark: the file went back to an older version.
type Rollback struct {
	// Name is "state log" or "disc state log".
	Name   string
	Path   string
	Last   uint64
	Marked uint64
}

// UseMark compares the last sequence of each log with the mark at path,
// and returns one Rollback for each log that is below its mark. A missing
// mark is not a roll back. When no log went back and the logs are
// writable, UseMark writes the mark now, and again after each append.
func (s *Logs) UseMark(path string) ([]Rollback, error) {
	items, discs, found, err := readMark(path)
	if err != nil {
		return nil, err
	}
	var back []Rollback
	if found {
		if last := s.Items.file.lastSeq; last < items {
			back = append(back, Rollback{Name: "state log", Path: s.Items.Path(), Last: last, Marked: items})
		}
		if last := s.Discs.file.lastSeq; last < discs {
			back = append(back, Rollback{Name: "disc state log", Path: s.Discs.Path(), Last: last, Marked: discs})
		}
	}
	if len(back) > 0 || !s.Items.file.writable {
		return back, nil
	}
	keep := func() error { return writeMark(path, s.Items.file.lastSeq, s.Discs.file.lastSeq) }
	if !found || items != s.Items.file.lastSeq || discs != s.Discs.file.lastSeq {
		if err := keep(); err != nil {
			return nil, err
		}
	}
	// A mark below the log is not a roll back, thus a failed write after
	// an append loses no record. The next command with the lock writes
	// the mark again.
	after := func() { _ = keep() }
	s.Items.file.afterAppend = after
	s.Discs.file.afterAppend = after
	return nil, nil
}

// markItemsKey and markDiscsKey name the two lines of the mark.
const (
	markItemsKey = stateFileName
	markDiscsKey = discStateFileName
)

// readMark reads the mark at path. found is false when the file does not
// exist.
func readMark(path string) (items, discs uint64, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("stage: %w", err)
	}
	var haveItems, haveDiscs bool
	for line := range strings.Lines(string(data)) {
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
		n, perr := strconv.ParseUint(value, 10, 64)
		if !ok || perr != nil {
			return 0, 0, false, fmt.Errorf("stage: %s: bad line %q", path, line)
		}
		switch key {
		case markItemsKey:
			items, haveItems = n, true
		case markDiscsKey:
			discs, haveDiscs = n, true
		default:
			return 0, 0, false, fmt.Errorf("stage: %s: bad line %q", path, line)
		}
	}
	if !haveItems || !haveDiscs {
		return 0, 0, false, fmt.Errorf("stage: %s: a line is missing", path)
	}
	return items, discs, true, nil
}

// writeMark writes the mark at path: a temporary file, synced, then
// renamed over the old mark, then the directory synced. It creates the
// directory of path when it does not exist.
func writeMark(path string, items, discs uint64) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s %d\n%s %d\n", markItemsKey, items, markDiscsKey, discs)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	_, writeErr := f.Write(buf.Bytes())
	var syncErr error
	if writeErr == nil {
		syncErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("stage: %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	if err := syncRecDir(dir); err != nil {
		return fmt.Errorf("stage: %s: %w", dir, err)
	}
	return nil
}

// MarkRollbacks compares the last good record of each log in stateDir
// with the mark at path, as UseMark does, for a command that does not
// replay the logs. It reads only the end of each file, and writes
// nothing.
func MarkRollbacks(stateDir, path string) ([]Rollback, error) {
	items, discs, found, err := readMark(path)
	if err != nil || !found {
		return nil, err
	}
	var back []Rollback
	for _, l := range []struct {
		name, file string
		width      int
		marked     uint64
	}{
		{"state log", stateFileName, recordLen, items},
		{"disc state log", discStateFileName, discRecordLen, discs},
	} {
		logPath := filepath.Join(stateDir, l.file)
		last, err := lastSequence(logPath, l.width)
		if err != nil {
			return nil, err
		}
		if last < l.marked {
			back = append(back, Rollback{Name: l.name, Path: logPath, Last: last, Marked: l.marked})
		}
	}
	return back, nil
}

// lastSequence returns the sequence of the last record of the file at
// path whose CRC is good. It looks at the last two whole records only: a
// torn tail holds at most one bad record. A missing file gives 0.
func lastSequence(path string, width int) (uint64, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stage: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("stage: %w", err)
	}
	rec := make([]byte, width)
	full := info.Size() / int64(width)
	for i := full - 1; i >= 0 && i >= full-2; i-- {
		if _, err := f.ReadAt(rec, i*int64(width)); err != nil {
			return 0, fmt.Errorf("stage: %s: %w", path, err)
		}
		if recordIntact(rec) {
			return recordSequence(rec), nil
		}
	}
	return 0, nil
}
