package restore

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"io"
	"os"
)

// fileStateLen is the size of one record of the file state file: the
// path tag as a little-endian uint64, the count of blob entries that
// still owe their bytes as a little-endian uint64, and one flag byte.
const fileStateLen = 17

const (
	statePending byte = 1 << iota
	stateResume
)

// errDestChanged stops a walk that does not meet the files of the first
// walk in the same order.
var errDestChanged = errors.New("the directories below the destination changed during the restore; run restore again")

// fileState is what the assembler keeps for one file it has not
// finished: how many blob entries still owe their bytes, and whether a
// part file from an earlier run was already on disk.
type fileState struct {
	remaining uint64
	resume    bool
}

// fileStates keeps one fixed-size record for each regular file of the
// walk, at the number of the file in walk order. Every walk of one
// selection meets the files in the same order, so the number finds the
// record with no index in memory. A record that was never written reads
// as zero: the file is not pending.
//
// The file lives in the system temporary directory and is unlinked at
// once, so it goes away when it is closed, also after a crash.
type fileStates struct {
	f *os.File
	// files is the number of files the first walk met.
	files int64
	// pending is the number of records that are pending.
	pending int64
}

func newFileStates() (*fileStates, error) {
	f, err := os.CreateTemp("", "noahsark-restore-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(f.Name()); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &fileStates{f: f}, nil
}

// pathTag is a short hash of a destination path. A record carries it, so
// that a walk that meets another file at a number stops instead of using
// the record of the wrong file.
func pathTag(dest string) uint64 {
	h := fnv.New64a()
	_, _ = io.WriteString(h, dest)
	return h.Sum64()
}

// get returns the state of file no, and whether it is pending.
func (s *fileStates) get(no int64, dest string) (fileState, bool, error) {
	var rec [fileStateLen]byte
	n, err := s.f.ReadAt(rec[:], no*fileStateLen)
	if n < len(rec) {
		if errors.Is(err, io.EOF) {
			return fileState{}, false, nil
		}
		return fileState{}, false, err
	}
	if rec[16]&statePending == 0 {
		return fileState{}, false, nil
	}
	if binary.LittleEndian.Uint64(rec[0:8]) != pathTag(dest) {
		return fileState{}, false, errDestChanged
	}
	return fileState{remaining: binary.LittleEndian.Uint64(rec[8:16]), resume: rec[16]&stateResume != 0}, true, nil
}

// put records file no as pending with st. wasPending tells whether its
// record is pending already.
func (s *fileStates) put(no int64, dest string, st fileState, wasPending bool) error {
	var rec [fileStateLen]byte
	binary.LittleEndian.PutUint64(rec[0:8], pathTag(dest))
	binary.LittleEndian.PutUint64(rec[8:16], st.remaining)
	rec[16] = statePending
	if st.resume {
		rec[16] |= stateResume
	}
	if _, err := s.f.WriteAt(rec[:], no*fileStateLen); err != nil {
		return err
	}
	if !wasPending {
		s.pending++
	}
	return nil
}

// clear records file no as not pending. A file that was not pending has
// no record to clear.
func (s *fileStates) clear(no int64, wasPending bool) error {
	if !wasPending {
		return nil
	}
	var rec [fileStateLen]byte
	if _, err := s.f.WriteAt(rec[:], no*fileStateLen); err != nil {
		return err
	}
	s.pending--
	return nil
}

func (s *fileStates) close() {
	if s.f != nil {
		_ = s.f.Close()
		s.f = nil
	}
}
