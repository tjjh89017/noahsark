package stage

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// This file holds the mechanics that the item log and the disc state log
// share. Each log is a file of fixed-width records with no header. A
// record starts with a u64 sequence and ends with a u32 CRC-32C over all
// bytes before it. Every integer is little-endian.

var recCRCTable = crc32.MakeTable(crc32.Castagnoli)

// recSequenceLen is the size of the sequence field at offset 0.
const recSequenceLen = 8

// recCRCLen is the size of the CRC field at the end of a record.
const recCRCLen = 4

// sealRecord writes the CRC-32C of rec into the last 4 bytes of rec.
func sealRecord(rec []byte) {
	body := len(rec) - recCRCLen
	binary.LittleEndian.PutUint32(rec[body:], crc32.Checksum(rec[:body], recCRCTable))
}

// recordIntact reports whether the CRC at the end of rec matches its
// other bytes.
func recordIntact(rec []byte) bool {
	body := len(rec) - recCRCLen
	return binary.LittleEndian.Uint32(rec[body:]) == crc32.Checksum(rec[:body], recCRCTable)
}

// recordSequence returns the sequence field of rec.
func recordSequence(rec []byte) uint64 {
	return binary.LittleEndian.Uint64(rec[:recSequenceLen])
}

// recAppender is the file that an append writes to. *os.File satisfies
// it.
type recAppender interface {
	io.Writer
	Sync() error
	Close() error
}

// openRecAppend opens path for an append. It creates the file when it
// does not exist, and then reports created as true. The parent directory
// must exist. Tests replace it to inject a sync or a close error.
var openRecAppend = func(path string) (f recAppender, created bool, err error) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		return file, true, nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return nil, false, err
	}
	file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, false, err
	}
	return file, false, nil
}

// syncRecDir syncs the directory dir, so that a new file name in it is
// durable. Tests replace it to inject an error.
var syncRecDir = func(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	return errors.Join(syncErr, closeErr)
}

// recFile is one open record file: its path, its record width, and the
// sequence of its last good record.
type recFile struct {
	path     string
	width    int
	lastSeq  uint64
	writable bool
	// tornBytes is the size of the torn tail that the open found. A
	// writable open cut these bytes. A read-only open ignored them.
	tornBytes int64
	// afterAppend runs after each durable append.
	afterAppend func()
}

// openRecFile reads path and calls apply for each good record, in file
// order. A missing file is an empty log.
//
// A partial record at the end, or a last record with a bad CRC, is a
// torn tail. A writable open cuts the file back to the last good record.
// A read-only open ignores the tail and does not change the file. Both
// report the size of the tail through tornBytes; the caller prints the
// warning.
//
// A bad CRC in any other record is damage. So is a sequence that does
// not grow, and an error from apply. Then openRecFile changes no byte of
// the file and returns an error that names the record.
//
// Only a holder of the repository lock opens the file writable.
func openRecFile(path string, width int, writable bool, apply func(rec []byte) error) (*recFile, error) {
	f := &recFile{path: path, width: width, writable: writable}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stage: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stage: %w", err)
	}

	// The records stream through one buffer, thus the peak memory of a
	// replay does not grow with the size of the file.
	size := info.Size()
	full := size / int64(width)
	r := bufio.NewReaderSize(file, 1<<16)
	rec := make([]byte, width)
	var good int64
	for i := range full {
		if _, err := io.ReadFull(r, rec); err != nil {
			return nil, fmt.Errorf("stage: %s: %w", path, err)
		}
		if !recordIntact(rec) {
			if i == full-1 {
				break
			}
			return nil, fmt.Errorf("stage: %s: record %d of %d has a bad CRC; the log is damaged", path, i+1, full)
		}
		seq := recordSequence(rec)
		if seq <= f.lastSeq {
			return nil, fmt.Errorf("stage: %s: record %d has sequence %d after sequence %d; the log is damaged", path, i+1, seq, f.lastSeq)
		}
		if err := apply(rec); err != nil {
			return nil, fmt.Errorf("stage: %s: record %d (sequence %d): %w; the log is damaged", path, i+1, seq, err)
		}
		f.lastSeq = seq
		good++
	}

	goodLen := good * int64(width)
	f.tornBytes = size - goodLen
	if f.tornBytes > 0 && writable {
		if err := os.Truncate(path, goodLen); err != nil {
			return nil, fmt.Errorf("stage: %w", err)
		}
	}
	return f, nil
}

// nextSeq returns the sequence of the next record to append.
func (f *recFile) nextSeq() uint64 {
	return f.lastSeq + 1
}

// appendBatch writes the records in batch, then syncs the file once.
// batch is a whole number of records, and each record is sealed. When
// the append creates the file, appendBatch also syncs the directory. An
// error from a write, a sync or a close is returned, and the caller must
// then not treat any record of the batch as written.
func (f *recFile) appendBatch(batch []byte) error {
	if !f.writable {
		return fmt.Errorf("stage: %s: the log is open read-only", f.path)
	}
	if len(batch) == 0 {
		return nil
	}
	if len(batch)%f.width != 0 {
		return fmt.Errorf("stage: %s: batch of %d bytes is not a whole number of %d-byte records", f.path, len(batch), f.width)
	}
	last := f.lastSeq
	for off := 0; off < len(batch); off += f.width {
		seq := recordSequence(batch[off : off+f.width])
		if seq <= last {
			return fmt.Errorf("stage: %s: batch record has sequence %d after sequence %d", f.path, seq, last)
		}
		last = seq
	}

	w, created, err := openRecAppend(f.path)
	if err != nil {
		return fmt.Errorf("stage: %w", err)
	}
	_, writeErr := w.Write(batch)
	var syncErr error
	if writeErr == nil {
		syncErr = w.Sync()
	}
	closeErr := w.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("stage: %s: %w", f.path, err)
	}
	if created {
		if err := syncRecDir(filepath.Dir(f.path)); err != nil {
			return fmt.Errorf("stage: %s: %w", filepath.Dir(f.path), err)
		}
	}
	f.lastSeq = recordSequence(batch[len(batch)-f.width:])
	if f.afterAppend != nil {
		f.afterAppend()
	}
	return nil
}
