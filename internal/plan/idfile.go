package plan

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"slices"

	"github.com/tjjh89017/noahsark/internal/object"
)

// idLen is the size of one content id in an id file.
const idLen = len(object.ID{})

// sortRunIDs is how many ids the sorter holds in memory before it writes
// them to a run file: 2 MiB of ids.
const sortRunIDs = 1 << 16

// mergeFanIn is how many run files the sorter merges at one time. It
// bounds the open files and the read buffers of one merge.
const mergeFanIn = 64

// readBufLen is the read buffer of one id file.
const readBufLen = 16 << 10

func compareID(a, b object.ID) int { return bytes.Compare(a[:], b[:]) }

// tempFile creates a file in the system temporary directory and unlinks
// it at once. The file goes away when it is closed, also after a crash.
func tempFile() (*os.File, error) {
	f, err := os.CreateTemp("", "noahsark-plan-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(f.Name()); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// idFile is a temporary file of n content ids in ascending order, with
// no id two times.
type idFile struct {
	f *os.File
	n int64
}

func (s idFile) reader() *bufio.Reader {
	return bufio.NewReaderSize(io.NewSectionReader(s.f, 0, s.n*int64(idLen)), readBufLen)
}

func (s idFile) close() {
	if s.f != nil {
		_ = s.f.Close()
	}
}

// readID reads the next id of r. ok is false at the end of r.
func readID(r *bufio.Reader) (id object.ID, ok bool, err error) {
	_, err = io.ReadFull(r, id[:])
	if errors.Is(err, io.EOF) {
		return id, false, nil
	}
	return id, err == nil, err
}

// idWriter writes a new idFile. The caller gives the ids in ascending
// order; the writer drops an id that repeats the one before it.
type idWriter struct {
	f    *os.File
	w    *bufio.Writer
	n    int64
	last object.ID
}

func newIDWriter() (*idWriter, error) {
	f, err := tempFile()
	if err != nil {
		return nil, err
	}
	return &idWriter{f: f, w: bufio.NewWriterSize(f, readBufLen)}, nil
}

func (w *idWriter) write(id object.ID) error {
	if w.n > 0 && id == w.last {
		return nil
	}
	w.last = id
	w.n++
	_, err := w.w.Write(id[:])
	return err
}

func (w *idWriter) done() (idFile, error) {
	if err := w.w.Flush(); err != nil {
		_ = w.f.Close()
		return idFile{}, err
	}
	return idFile{f: w.f, n: w.n}, nil
}

func (w *idWriter) discard() { _ = w.f.Close() }

// idSorter sorts the ids that a walk gives, with no duplicate, in
// temporary files. It holds at most sortRunIDs ids in memory.
type idSorter struct {
	buf  []object.ID
	runs []idFile
	err  error
}

// add takes one id. The first error stops the sorter, and sorted
// returns it.
func (s *idSorter) add(id object.ID) {
	if s.err != nil {
		return
	}
	if s.buf == nil {
		s.buf = make([]object.ID, 0, sortRunIDs)
	}
	s.buf = append(s.buf, id)
	if len(s.buf) == cap(s.buf) {
		s.err = s.flush()
	}
}

// flush writes the ids in memory to a run file. When mergeFanIn runs
// exist, it merges them into one.
func (s *idSorter) flush() error {
	if len(s.buf) == 0 {
		return nil
	}
	slices.SortFunc(s.buf, compareID)
	w, err := newIDWriter()
	if err != nil {
		return err
	}
	for _, id := range s.buf {
		if err := w.write(id); err != nil {
			w.discard()
			return err
		}
	}
	run, err := w.done()
	if err != nil {
		return err
	}
	s.buf = s.buf[:0]
	s.runs = append(s.runs, run)
	if len(s.runs) < mergeFanIn {
		return nil
	}
	merged, err := mergeIDFiles(s.runs)
	s.runs = nil
	if err != nil {
		return err
	}
	s.runs = []idFile{merged}
	return nil
}

// sorted returns every id that add took, as one idFile, and frees the
// memory of the sorter.
func (s *idSorter) sorted() (idFile, error) {
	if s.err == nil {
		s.err = s.flush()
	}
	s.buf = nil
	runs := s.runs
	s.runs = nil
	if s.err != nil {
		for _, r := range runs {
			r.close()
		}
		return idFile{}, s.err
	}
	switch len(runs) {
	case 0:
		w, err := newIDWriter()
		if err != nil {
			return idFile{}, err
		}
		return w.done()
	case 1:
		return runs[0], nil
	}
	return mergeIDFiles(runs)
}

// mergeIDFiles merges sorted runs into one idFile, and closes the runs.
func mergeIDFiles(runs []idFile) (idFile, error) {
	defer func() {
		for _, r := range runs {
			r.close()
		}
	}()
	readers := make([]*bufio.Reader, len(runs))
	heads := make([]object.ID, len(runs))
	live := make([]bool, len(runs))
	for i, r := range runs {
		readers[i] = r.reader()
		var err error
		if heads[i], live[i], err = readID(readers[i]); err != nil {
			return idFile{}, err
		}
	}
	w, err := newIDWriter()
	if err != nil {
		return idFile{}, err
	}
	for {
		least := -1
		for i := range runs {
			if live[i] && (least < 0 || compareID(heads[i], heads[least]) < 0) {
				least = i
			}
		}
		if least < 0 {
			return w.done()
		}
		if err := w.write(heads[least]); err != nil {
			w.discard()
			return idFile{}, err
		}
		if heads[least], live[least], err = readID(readers[least]); err != nil {
			w.discard()
			return idFile{}, err
		}
	}
}
