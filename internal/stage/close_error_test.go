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

// TestMarkVerifiedSurfacesCloseError checks recordCleanTime, reached
// through MarkVerified, the same way. It injects the Close failure only on
// clean_times.db, so state.db's own Burned-to-Clean record still writes
// cleanly and the failure is isolated to the companion log MarkVerified
// writes second.
func TestMarkVerifiedSurfacesCloseError(t *testing.T) {
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
	if err := l.MarkVerified(id); !errors.Is(err, errInjectedClose) {
		t.Fatalf("MarkVerified error = %v, want it to wrap %v", err, errInjectedClose)
	}
	if _, ok := l.CleanTime(id); ok {
		t.Fatal("a clean time whose Close failed must not be recorded")
	}
}

// syncRecorder wraps a real *os.File and records the order in which its
// Sync and Close run, so a test can prove a durable append flushes
// before it closes. syncErr, when set, stands in for a disc that
// refuses the flush.
type syncRecorder struct {
	*os.File
	calls   *[]string
	syncErr error
}

var errInjectedSync = errors.New("injected sync failure")

func (f *syncRecorder) Sync() error {
	*f.calls = append(*f.calls, "sync")
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.File.Sync()
}

func (f *syncRecorder) Close() error {
	*f.calls = append(*f.calls, "close")
	return f.File.Close()
}

// withSyncRecorder replaces openAppend so every appended record's file
// records its own Sync and Close calls, and returns the call log.
func withSyncRecorder(t *testing.T, syncErr error) *[]string {
	t.Helper()
	calls := new([]string)
	orig := openAppend
	openAppend = func(path string) (appendCloser, error) {
		f, err := orig(path)
		if err != nil {
			return nil, err
		}
		osFile, ok := f.(*os.File)
		if !ok {
			t.Fatalf("openAppend returned a %T, want *os.File", f)
		}
		return &syncRecorder{File: osFile, calls: calls, syncErr: syncErr}, nil
	}
	t.Cleanup(func() { openAppend = orig })
	return calls
}

// TestMarkGCEligibleSyncsBeforeClose checks the durable append: gc
// removes a staged file right after this record, so the record must
// reach the disc before the file goes away.
func TestMarkGCEligibleSyncsBeforeClose(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("gc-me"))

	calls := withSyncRecorder(t, nil)
	if err := l.MarkGCEligible(id); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, ","); got != "sync,close" {
		t.Fatalf("MarkGCEligible calls = %q, want \"sync,close\"", got)
	}
}

// TestMarkGCEligibleSurfacesSyncError checks that a failed flush is
// returned, not swallowed: gc must not delete bytes whose record never
// reached the disc.
func TestMarkGCEligibleSurfacesSyncError(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("gc-me"))

	withSyncRecorder(t, errInjectedSync)
	if err := l.MarkGCEligible(id); !errors.Is(err, errInjectedSync) {
		t.Fatalf("MarkGCEligible error = %v, want it to wrap %v", err, errInjectedSync)
	}
	if _, ok := l.Get(id); ok {
		t.Fatal("a record whose Sync failed must not appear in the log")
	}
}

// TestCommitAppendDoesNotSync holds the other side of the rule: commit
// writes one record per object, so the ordinary append must stay a
// plain write and close. Only the record gc writes before a delete is
// durable.
func TestCommitAppendDoesNotSync(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	calls := withSyncRecorder(t, nil)
	if err := l.EnsureStaged(object.ComputeID([]byte("staged"))); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, ","); got != "close" {
		t.Fatalf("EnsureStaged calls = %q, want \"close\"", got)
	}
}
