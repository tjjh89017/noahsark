package stage

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tjjh89017/noahsark/internal/object"
)

// failCloseFile wraps a real *os.File so its Write succeeds (the bytes
// really do reach the OS) but its Close reports an error, the way a
// deferred, discarded Close error used to hide a failed flush from the
// caller.
type failCloseFile struct {
	*os.File
}

var errInjectedClose = errors.New("injected close failure")

func (f *failCloseFile) Close() error {
	_ = f.File.Close()
	return errInjectedClose
}

// withFailingClose replaces openAppend for the duration of the test so
// every appender opens a file whose Close always fails, and restores it
// on cleanup.
func withFailingClose(t *testing.T) {
	t.Helper()
	withFailingCloseForSuffix(t, "")
}

// withFailingCloseForSuffix is withFailingClose, but the injected Close
// failure applies only to a path ending in suffix; every other path
// still closes normally. An empty suffix matches every path.
func withFailingCloseForSuffix(t *testing.T, suffix string) {
	t.Helper()
	orig := openAppend
	openAppend = func(path string) (appendCloser, error) {
		f, err := orig(path)
		if err != nil {
			return nil, err
		}
		if suffix != "" && !strings.HasSuffix(path, suffix) {
			return f, nil
		}
		osFile, ok := f.(*os.File)
		if !ok {
			t.Fatalf("openAppend returned a %T, want *os.File", f)
		}
		return &failCloseFile{File: osFile}, nil
	}
	t.Cleanup(func() { openAppend = orig })
}

// TestWriteRecordSurfacesCloseError checks bug 2 at the shared helper:
// writeRecord must return a Close error instead of the appenders'
// old "defer func() { _ = f.Close() }(); return nil" pattern, which
// dropped it silently on the success path.
func TestWriteRecordSurfacesCloseError(t *testing.T) {
	withFailingClose(t)
	dir := t.TempDir()
	if err := writeRecord(dir+"/state.db", []byte("x")); !errors.Is(err, errInjectedClose) {
		t.Fatalf("writeRecord error = %v, want it to wrap %v", err, errInjectedClose)
	}
}

// TestAppendSurfacesCloseErrorAndKeepsStateConsistent checks that
// Log.append, through EnsureStaged, reports a Close failure instead of
// claiming success, and that it never updates the in-memory state (or
// nextSeq) for a record that is not durably written: a caller like gc
// must be able to trust that a returned nil error means the record
// really is on disk.
func TestAppendSurfacesCloseErrorAndKeepsStateConsistent(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("payload"))

	withFailingClose(t)
	if err := l.EnsureStaged(id); !errors.Is(err, errInjectedClose) {
		t.Fatalf("EnsureStaged error = %v, want it to wrap %v", err, errInjectedClose)
	}
	if _, ok := l.Get(id); ok {
		t.Fatal("a record whose Close failed must not appear as staged")
	}
}

// TestRecordBurnTimeSurfacesCloseError checks the burn time companion
// appender the same way.
func TestRecordBurnTimeSurfacesCloseError(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var discUUID [16]byte
	copy(discUUID[:], "0123456789abcdef")

	withFailingClose(t)
	if err := l.RecordBurnTime(discUUID); !errors.Is(err, errInjectedClose) {
		t.Fatalf("RecordBurnTime error = %v, want it to wrap %v", err, errInjectedClose)
	}
	if _, ok := l.BurnTime(discUUID); ok {
		t.Fatal("a burn time whose Close failed must not be recorded")
	}
}

// TestRecordFedDiscSurfacesCloseError checks the fed-disc companion
// appender the same way.
func TestRecordFedDiscSurfacesCloseError(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var discUUID [16]byte
	copy(discUUID[:], "fedcba9876543210")

	withFailingClose(t)
	if err := l.RecordFedDisc(discUUID); !errors.Is(err, errInjectedClose) {
		t.Fatalf("RecordFedDisc error = %v, want it to wrap %v", err, errInjectedClose)
	}
	if l.FedDiscs(discUUID) {
		t.Fatal("a fed-disc record whose Close failed must not be recorded")
	}
}

// TestMarkCleanSurfacesCloseError checks recordCleanTime, reached
// through MarkClean, the same way. It injects the Close failure only on
// clean_times.db, so state.db's own Burned-to-Clean record still writes
// cleanly and the failure is isolated to the companion log MarkClean
// writes second.
func TestMarkCleanSurfacesCloseError(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("clean-me"))
	if err := l.MarkPacked(id, 1, [16]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkBurned(id, 1, [16]byte{}); err != nil {
		t.Fatal(err)
	}

	withFailingCloseForSuffix(t, cleanTimeFileName)
	if err := l.MarkClean(id); !errors.Is(err, errInjectedClose) {
		t.Fatalf("MarkClean error = %v, want it to wrap %v", err, errInjectedClose)
	}
	if _, ok := l.CleanTime(id); ok {
		t.Fatal("a clean time whose Close failed must not be recorded")
	}
}
