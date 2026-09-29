package stage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// testRecWidth is the width of the test records: sequence (8), payload
// (4), CRC (4).
const testRecWidth = 16

// testRec returns one sealed test record.
func testRec(seq uint64, payload uint32) []byte {
	buf := make([]byte, testRecWidth)
	binary.LittleEndian.PutUint64(buf[0:8], seq)
	binary.LittleEndian.PutUint32(buf[8:12], payload)
	sealRecord(buf)
	return buf
}

// testRecs returns sealed test records with sequences 1 to n.
func testRecs(n int) []byte {
	var out []byte
	for i := range n {
		out = append(out, testRec(uint64(i+1), uint32(100+i))...)
	}
	return out
}

// openTestRecFile opens path and collects the payload of each replayed
// record.
func openTestRecFile(t *testing.T, path string, writable bool) (*recFile, []uint32, error) {
	t.Helper()
	var payloads []uint32
	f, err := openRecFile(path, testRecWidth, writable, func(rec []byte) error {
		payloads = append(payloads, binary.LittleEndian.Uint32(rec[8:12]))
		return nil
	})
	return f, payloads, err
}

func writeTestFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRecFileMissingIsEmpty(t *testing.T) {
	f, payloads, err := openTestRecFile(t, filepath.Join(t.TempDir(), "none.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 0 || f.nextSeq() != 1 || f.tornBytes != 0 {
		t.Fatalf("payloads %v, next %d, torn %d; want an empty log", payloads, f.nextSeq(), f.tornBytes)
	}
}

func TestRecFileReplaysInOrder(t *testing.T) {
	path := writeTestFile(t, testRecs(3))
	f, payloads, err := openTestRecFile(t, path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(payloads, []uint32{100, 101, 102}) {
		t.Fatalf("payloads %v", payloads)
	}
	if f.nextSeq() != 4 {
		t.Fatalf("next sequence %d, want 4", f.nextSeq())
	}
}

func TestRecFileTornTail(t *testing.T) {
	badLast := testRecs(3)
	badLast[len(badLast)-1] ^= 0xFF
	cases := []struct {
		name string
		data []byte
		torn int64
	}{
		{"partial record", append(testRecs(2), testRec(3, 102)[:7]...), 7},
		{"last record with a bad CRC", badLast, testRecWidth},
		{"bad last record and a partial record", append(slices.Clone(badLast), 1, 2, 3), testRecWidth + 3},
	}
	for _, tc := range cases {
		t.Run(tc.name+", read-only", func(t *testing.T) {
			path := writeTestFile(t, tc.data)
			f, payloads, err := openTestRecFile(t, path, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(payloads) != 2 || f.tornBytes != tc.torn {
				t.Fatalf("payloads %v, torn %d; want 2 records, torn %d", payloads, f.tornBytes, tc.torn)
			}
			if !bytes.Equal(readTestFile(t, path), tc.data) {
				t.Fatal("a read-only open changed the file")
			}
		})
		t.Run(tc.name+", writable", func(t *testing.T) {
			path := writeTestFile(t, tc.data)
			f, payloads, err := openTestRecFile(t, path, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(payloads) != 2 || f.tornBytes != tc.torn {
				t.Fatalf("payloads %v, torn %d; want 2 records, torn %d", payloads, f.tornBytes, tc.torn)
			}
			if !bytes.Equal(readTestFile(t, path), testRecs(2)) {
				t.Fatal("the writable open did not cut the file back to the last good record")
			}
			if err := f.appendBatch(testRec(3, 200)); err != nil {
				t.Fatal(err)
			}
			_, payloads, err = openTestRecFile(t, path, false)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(payloads, []uint32{100, 101, 200}) {
				t.Fatalf("payloads after the append %v", payloads)
			}
		})
	}
}

func TestRecFileDamage(t *testing.T) {
	badMiddle := testRecs(3)
	badMiddle[testRecWidth+9] ^= 0x01
	cases := []struct {
		name  string
		data  []byte
		apply func([]byte) error
		want  string
	}{
		{"bad CRC in the middle", badMiddle, nil, "record 2 of 3 has a bad CRC"},
		{"sequence repeats", append(testRecs(2), testRec(2, 7)...), nil, "record 3 has sequence 2 after sequence 2"},
		{"sequence falls", append(testRecs(3), testRec(1, 7)...), nil, "record 4 has sequence 1 after sequence 3"},
		{"sequence starts at 0", testRec(0, 7), nil, "record 1 has sequence 0 after sequence 0"},
		{"decode refuses", testRecs(2), func(rec []byte) error {
			if recordSequence(rec) == 2 {
				return errors.New("not permitted")
			}
			return nil
		}, "record 2 (sequence 2): not permitted"},
	}
	for _, tc := range cases {
		for _, writable := range []bool{false, true} {
			path := writeTestFile(t, tc.data)
			apply := tc.apply
			if apply == nil {
				apply = func([]byte) error { return nil }
			}
			_, err := openRecFile(path, testRecWidth, writable, apply)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "damaged") {
				t.Fatalf("%s, writable %v: error %v, want one that contains %q", tc.name, writable, err, tc.want)
			}
			if !bytes.Equal(readTestFile(t, path), tc.data) {
				t.Fatalf("%s, writable %v: the open changed a damaged file", tc.name, writable)
			}
		}
	}
}

// fakeAppender wraps a real file and records its calls. syncErr and
// closeErr stand in for a device that refuses the call.
type fakeAppender struct {
	*os.File
	calls    *[]string
	syncErr  error
	closeErr error
}

func (f *fakeAppender) Write(p []byte) (int, error) {
	*f.calls = append(*f.calls, "write")
	return f.File.Write(p)
}

func (f *fakeAppender) Sync() error {
	*f.calls = append(*f.calls, "sync")
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.File.Sync()
}

func (f *fakeAppender) Close() error {
	*f.calls = append(*f.calls, "close")
	err := f.File.Close()
	if f.closeErr != nil {
		return f.closeErr
	}
	return err
}

// withRecSeams replaces openRecAppend and syncRecDir for one test and
// returns the log of calls.
func withRecSeams(t *testing.T, syncErr, closeErr, dirErr error) *[]string {
	t.Helper()
	calls := new([]string)
	origOpen, origDir := openRecAppend, syncRecDir
	openRecAppend = func(path string) (recAppender, bool, error) {
		f, created, err := origOpen(path)
		if err != nil {
			return nil, false, err
		}
		return &fakeAppender{File: f.(*os.File), calls: calls, syncErr: syncErr, closeErr: closeErr}, created, nil
	}
	syncRecDir = func(dir string) error {
		*calls = append(*calls, "syncdir")
		if dirErr != nil {
			return dirErr
		}
		return origDir(dir)
	}
	t.Cleanup(func() { openRecAppend, syncRecDir = origOpen, origDir })
	return calls
}

func TestRecFileAppendIsDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	f, _, err := openTestRecFile(t, path, true)
	if err != nil {
		t.Fatal(err)
	}
	calls := withRecSeams(t, nil, nil, nil)

	if err := f.appendBatch(append(testRec(1, 100), testRec(2, 101)...)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, ","); got != "write,sync,close,syncdir" {
		t.Fatalf("calls of the first append %q, want one write, one sync, and a directory sync", got)
	}

	*calls = nil
	if err := f.appendBatch(testRec(3, 102)); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, ","); got != "write,sync,close" {
		t.Fatalf("calls of the second append %q, want no directory sync", got)
	}
	if !bytes.Equal(readTestFile(t, path), testRecs(3)) {
		t.Fatal("the file does not hold the three records")
	}
	if f.nextSeq() != 4 {
		t.Fatalf("next sequence %d, want 4", f.nextSeq())
	}
}

func TestRecFileAppendErrors(t *testing.T) {
	errSync := errors.New("injected sync error")
	errClose := errors.New("injected close error")
	errDir := errors.New("injected directory sync error")
	cases := []struct {
		name                     string
		syncErr, closeErr, dirEr error
		want                     error
	}{
		{"sync", errSync, nil, nil, errSync},
		{"close", nil, errClose, nil, errClose},
		{"directory sync", nil, nil, errDir, errDir},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.db")
			f, _, err := openTestRecFile(t, path, true)
			if err != nil {
				t.Fatal(err)
			}
			withRecSeams(t, tc.syncErr, tc.closeErr, tc.dirEr)
			if err := f.appendBatch(testRec(1, 100)); !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want it to wrap %v", err, tc.want)
			}
			if f.nextSeq() != 1 {
				t.Fatalf("next sequence %d after a failed append, want 1", f.nextSeq())
			}
		})
	}
}

func TestRecFileAppendRefusals(t *testing.T) {
	path := writeTestFile(t, testRecs(2))

	ro, _, err := openTestRecFile(t, path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := ro.appendBatch(testRec(3, 1)); err == nil {
		t.Fatal("a read-only log accepted an append")
	}

	f, _, err := openTestRecFile(t, path, true)
	if err != nil {
		t.Fatal(err)
	}
	for name, batch := range map[string][]byte{
		"sequence that does not grow": testRec(2, 1),
		"second record that falls":    append(testRec(3, 1), testRec(3, 2)...),
		"partial record":              testRec(3, 1)[:10],
	} {
		if err := f.appendBatch(batch); err == nil {
			t.Fatalf("%s: the append succeeded", name)
		}
	}
	if !bytes.Equal(readTestFile(t, path), testRecs(2)) {
		t.Fatal("a refused append changed the file")
	}
}
