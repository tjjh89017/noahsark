package stage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjjh89017/noahsark/internal/object"
)

func TestEnsureStagedThenMarkPacked(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("chunk a"))

	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Get(id)
	if !ok || rec.State != Staged {
		t.Fatalf("got %+v, %v, want Staged", rec, ok)
	}

	var discUUID [16]byte
	discUUID[0] = 0xAB
	if err := l.MarkPacked(id, 3, discUUID); err != nil {
		t.Fatal(err)
	}
	rec, ok = l.Get(id)
	if !ok || rec.State != Packed || rec.RunSeq != 3 || rec.DiscUUID != discUUID {
		t.Fatalf("got %+v, %v, want Packed run 3", rec, ok)
	}

	// EnsureStaged after Packed must not resurrect the object to Staged.
	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}
	rec, ok = l.Get(id)
	if !ok || rec.State != Packed {
		t.Fatalf("EnsureStaged reset a Packed object: %+v", rec)
	}
}

func TestOpenReplaysAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	id1 := object.ComputeID([]byte("one"))
	id2 := object.ComputeID([]byte("two"))

	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id1); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id2); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(id1, 1, [16]byte{1}); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec1, ok := l2.Get(id1)
	if !ok || rec1.State != Packed || rec1.RunSeq != 1 {
		t.Fatalf("id1: got %+v, %v", rec1, ok)
	}
	rec2, ok := l2.Get(id2)
	if !ok || rec2.State != Staged {
		t.Fatalf("id2: got %+v, %v", rec2, ok)
	}
}

// TestEnsureOnDiscIsIdempotent checks that a repeat rebuild from the
// same discs appends nothing: the first call records the object, and
// every later call finds it already known.
func TestEnsureOnDiscIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("chunk a"))
	var discUUID [16]byte
	discUUID[0] = 0xCD

	for range 3 {
		if err := l.EnsureOnDisc(id, 5, discUUID); err != nil {
			t.Fatal(err)
		}
	}

	rec, ok := l.Get(id)
	if !ok || rec.State != OnDiscOnly || rec.RunSeq != 5 || rec.DiscUUID != discUUID || rec.Sequence != 1 {
		t.Fatalf("got %+v, %v, want a single OnDiscOnly record at sequence 1", rec, ok)
	}
}

// TestEnsureOnDiscKeepsAKnownState checks that replaying a disc's own
// catalog through EnsureOnDisc never undoes what the log already knows:
// an object already Packed, Burned, Clean or OnDiscOnly stays exactly
// there, and no record is appended.
func TestEnsureOnDiscKeepsAKnownState(t *testing.T) {
	var discUUID [16]byte
	discUUID[0] = 0x11

	for _, state := range []State{Packed, Burned, Clean, OnDiscOnly} {
		t.Run(stateName(state), func(t *testing.T) {
			dir := t.TempDir()
			l, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			id := object.ComputeID([]byte("chunk"))

			if err := l.MarkPacked(id, 1, discUUID); err != nil {
				t.Fatal(err)
			}
			if state != Packed {
				if err := l.MarkBurned(id, 1, discUUID); err != nil {
					t.Fatal(err)
				}
			}
			switch state {
			case Clean:
				if err := l.MarkVerified(id); err != nil {
					t.Fatal(err)
				}
			case OnDiscOnly:
				if err := l.MarkVerified(id); err != nil {
					t.Fatal(err)
				}
				if err := l.MarkOnDisc(id); err != nil {
					t.Fatal(err)
				}
			}

			seqBefore, _ := l.Get(id)
			if err := l.EnsureOnDisc(id, 1, discUUID); err != nil {
				t.Fatal(err)
			}
			rec, ok := l.Get(id)
			if !ok || rec.State != state {
				t.Fatalf("EnsureOnDisc changed state to %+v, want unchanged %v", rec, state)
			}
			if rec.Sequence != seqBefore.Sequence {
				t.Fatalf("EnsureOnDisc appended a record: sequence %d, want unchanged %d", rec.Sequence, seqBefore.Sequence)
			}
		})
	}
}

func stateName(s State) string {
	switch s {
	case Staged:
		return "Staged"
	case Packed:
		return "Packed"
	case Burned:
		return "Burned"
	case Clean:
		return "Clean"
	case OnDiscOnly:
		return "OnDiscOnly"
	default:
		return "unknown"
	}
}

func TestCountState(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id1 := object.ComputeID([]byte("one"))
	id2 := object.ComputeID([]byte("two"))
	id3 := object.ComputeID([]byte("three"))

	if err := l.EnsureStaged(id1); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id2); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(id2, 1, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(id3, 1, [16]byte{1}); err != nil {
		t.Fatal(err)
	}

	if n := l.CountState(Staged); n != 1 {
		t.Fatalf("CountState(Staged) = %d, want 1", n)
	}
	if n := l.CountState(Packed); n != 2 {
		t.Fatalf("CountState(Packed) = %d, want 2", n)
	}
}

func TestTruncatedTailStopsReplay(t *testing.T) {
	dir := t.TempDir()
	id1 := object.ComputeID([]byte("one"))
	id2 := object.ComputeID([]byte("two"))

	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id1); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id2); err != nil {
		t.Fatal(err)
	}

	// Corrupt the last record's CRC, simulating a crash during an
	// append.
	path := filepath.Join(dir, stateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l2.Get(id1); !ok {
		t.Fatal("id1 should have replayed before the truncated record")
	}
	if _, ok := l2.Get(id2); ok {
		t.Fatal("id2's record was corrupted and must not replay")
	}

	// Bug 3: Open must expose that the tail was truncated, and by how
	// much, so every command that opens the log can report it.
	truncated, ignored := l2.Truncated()
	if !truncated {
		t.Fatal("Truncated() = false, want true after a corrupted trailing record")
	}
	if ignored != recordLen {
		t.Fatalf("Truncated() ignored %d byte(s), want %d (one whole record)", ignored, recordLen)
	}
}

// TestTruncatedTailReportsNoTruncationOnCleanLog checks that Truncated
// reports false when nothing is torn, so the warning never fires on an
// ordinary, healthy log.
func TestTruncatedTailReportsNoTruncationOnCleanLog(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("ok"))
	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if truncated, ignored := l2.Truncated(); truncated || ignored != 0 {
		t.Fatalf("Truncated() = (%v, %d), want (false, 0) on a healthy log", truncated, ignored)
	}
}

// TestAppendAfterTornTailStaysReachable checks the append-after-torn-tail
// hazard bug 3 also covers: a record appended after Open found a torn
// tail must still be reachable by a later replay, not stranded behind
// garbage replay stops at and never gets past.
func TestAppendAfterTornTailStaysReachable(t *testing.T) {
	dir := t.TempDir()
	id1 := object.ComputeID([]byte("one"))
	id2 := object.ComputeID([]byte("two"))
	id3 := object.ComputeID([]byte("three"))

	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id1); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id2); err != nil {
		t.Fatal(err)
	}

	// Corrupt the last record's CRC, simulating a crash during an
	// append, exactly as TestTruncatedTailStopsReplay does.
	path := filepath.Join(dir, stateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Open again, as a command would, and append a new record. Without
	// bug 3's fix, this new record lands after the torn one, where no
	// replay can ever reach it.
	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l2.EnsureStaged(id3); err != nil {
		t.Fatal(err)
	}

	l3, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l3.Get(id1); !ok {
		t.Fatal("id1 should still replay")
	}
	if _, ok := l3.Get(id2); ok {
		t.Fatal("id2's record was corrupted and must not replay")
	}
	if _, ok := l3.Get(id3); !ok {
		t.Fatal("id3 was appended after the torn tail and must still replay")
	}
	if truncated, _ := l3.Truncated(); truncated {
		t.Fatal("Truncated() = true after the torn tail was cut off and a good record appended")
	}
}

func TestIDsInState(t *testing.T) {
	dir := t.TempDir()
	staged := object.ComputeID([]byte("staged"))
	packed := object.ComputeID([]byte("packed"))

	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(staged); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(packed); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(packed, 1, [16]byte{1}); err != nil {
		t.Fatal(err)
	}

	got := l.IDsInState(Staged)
	if len(got) != 1 || got[0] != staged {
		t.Fatalf("IDsInState(Staged) = %v, want [%v]", got, staged)
	}
	got = l.IDsInState(Packed)
	if len(got) != 1 || got[0] != packed {
		t.Fatalf("IDsInState(Packed) = %v, want [%v]", got, packed)
	}
}

func TestPackedCountByDisc(t *testing.T) {
	dir := t.TempDir()
	discA := [16]byte{0xA}
	discB := [16]byte{0xB}
	idA1 := object.ComputeID([]byte("a1"))
	idA2 := object.ComputeID([]byte("a2"))
	idB1 := object.ComputeID([]byte("b1"))
	idStaged := object.ComputeID([]byte("staged"))

	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []object.ID{idA1, idA2, idB1, idStaged} {
		if err := l.EnsureStaged(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.MarkPacked(idA1, 1, discA); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(idA2, 1, discA); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(idB1, 2, discB); err != nil {
		t.Fatal(err)
	}

	counts := l.PackedCountByDisc()
	if counts[discA] != 2 {
		t.Fatalf("counts[discA] = %d, want 2", counts[discA])
	}
	if counts[discB] != 1 {
		t.Fatalf("counts[discB] = %d, want 1", counts[discB])
	}
	if len(counts) != 2 {
		t.Fatalf("counts has %d discs, want 2 (staged object must not appear)", len(counts))
	}
}

func TestBurnedCleanTransitions(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("chunk a"))
	discUUID := [16]byte{0xAB}

	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(id, 3, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkBurned(id, 3, discUUID); err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Get(id)
	if !ok || rec.State != Burned || rec.RunSeq != 3 || rec.DiscUUID != discUUID {
		t.Fatalf("got %+v, %v, want Burned run 3", rec, ok)
	}

	if _, ok := l.CleanTime(id); ok {
		t.Fatal("CleanTime reported a time before MarkVerified ran")
	}
	if err := l.MarkVerified(id); err != nil {
		t.Fatal(err)
	}
	rec, ok = l.Get(id)
	if !ok || rec.State != Clean || rec.RunSeq != 3 || rec.DiscUUID != discUUID {
		t.Fatalf("got %+v, %v, want Clean run 3", rec, ok)
	}
	cleanAt, ok := l.CleanTime(id)
	if !ok || cleanAt.IsZero() {
		t.Fatalf("CleanTime after MarkVerified: got %v, %v", cleanAt, ok)
	}

	// The clean time survives a reopen: it is a field of the record.
	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cleanAt2, ok := l2.CleanTime(id)
	if !ok || !cleanAt2.Equal(cleanAt) {
		t.Fatalf("reopened CleanTime: got %v, %v, want %v", cleanAt2, ok, cleanAt)
	}
}

func TestVerifyFailedReturnsBurnedToPacked(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("chunk a"))
	discUUID := [16]byte{0xCD}

	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(id, 5, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkBurned(id, 5, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkVerifyFailed(id); err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Get(id)
	if !ok || rec.State != Packed || rec.Reason != ReasonVerifyFailed || rec.RunSeq != 5 || rec.DiscUUID != discUUID {
		t.Fatalf("got %+v, %v, want Packed run 5 reason ReasonVerifyFailed", rec, ok)
	}
}

// TestMarkOnDiscIsTerminal checks the one state gc records before it
// unlinks a staged file.
func TestMarkOnDiscIsTerminal(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("chunk a"))
	discUUID := [16]byte{0xEF}

	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(id, 1, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkBurned(id, 1, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkVerified(id); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkOnDisc(id); err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Get(id)
	if !ok || rec.State != OnDiscOnly || rec.DiscUUID != discUUID {
		t.Fatalf("got %+v, %v, want OnDiscOnly on disc %x", rec, ok, discUUID)
	}
}

func TestBurnUndoReturnsBurnedToPacked(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("chunk a"))
	discUUID := [16]byte{0x12}

	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(id, 4, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkBurned(id, 4, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkBurnUndone(id); err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Get(id)
	if !ok || rec.State != Packed || rec.Reason != ReasonBurnFailed || rec.RunSeq != 4 || rec.DiscUUID != discUUID {
		t.Fatalf("got %+v, %v, want Packed run 4 reason ReasonBurnFailed", rec, ok)
	}
}

// TestStateOnDisc checks that OnDisc names every state a disc already
// holds an object's data for: Packed, Burned, Clean and OnDiscOnly, and
// only those.
func TestStateOnDisc(t *testing.T) {
	onDisc := map[State]bool{
		Staged:     false,
		Packed:     true,
		Burned:     true,
		Clean:      true,
		OnDiscOnly: true,
	}
	for state, want := range onDisc {
		if got := state.OnDisc(); got != want {
			t.Errorf("State(%d).OnDisc() = %v, want %v", state, got, want)
		}
	}
}

// TestVerifyCountRisesWithEachVerify checks the verify count of an
// object over the two verifies of the two identical discs: 0 while
// Burned, 1 after the first verify, 2 after the second. The object stays
// Clean, and the count survives a reopen of the log.
func TestVerifyCountRisesWithEachVerify(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("chunk a"))
	discUUID := [16]byte{0x5A}

	if err := l.EnsureStaged(id); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkPacked(id, 2, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkBurned(id, 2, discUUID); err != nil {
		t.Fatal(err)
	}
	if rec, ok := l.Get(id); !ok || rec.VerifyCount != 0 {
		t.Fatalf("Burned record %+v, %v, want verify count 0", rec, ok)
	}

	for want := uint8(1); want <= 2; want++ {
		if err := l.MarkVerified(id); err != nil {
			t.Fatal(err)
		}
		rec, ok := l.Get(id)
		if !ok || rec.State != Clean || rec.VerifyCount != want {
			t.Fatalf("after verify %d: got %+v, %v, want Clean with verify count %d", want, rec, ok, want)
		}
	}

	if lowest := l.MinCleanVerifyCountByDisc()[discUUID]; lowest != 2 {
		t.Fatalf("MinCleanVerifyCountByDisc = %d, want 2", lowest)
	}

	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := l2.Get(id)
	if !ok || rec.State != Clean || rec.VerifyCount != 2 {
		t.Fatalf("after reopen: got %+v, %v, want Clean with verify count 2", rec, ok)
	}
}

// TestVerifyCountSurvivesOnDisc checks that the ON-DISC record carries
// the verify count and the clean time forward.
func TestVerifyCountSurvivesOnDisc(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("chunk b"))
	discUUID := [16]byte{0x7C}

	if err := l.MarkPacked(id, 1, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkBurned(id, 1, discUUID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := l.MarkVerified(id); err != nil {
			t.Fatal(err)
		}
	}
	cleanAt, ok := l.CleanTime(id)
	if !ok {
		t.Fatal("CleanTime reported no time after two verifies")
	}
	if err := l.MarkOnDisc(id); err != nil {
		t.Fatal(err)
	}
	rec, ok := l.Get(id)
	if !ok || rec.State != OnDiscOnly || rec.VerifyCount != 2 {
		t.Fatalf("OnDiscOnly record %+v, %v, want verify count 2", rec, ok)
	}
	carried, ok := l.CleanTime(id)
	if !ok || !carried.Equal(cleanAt) {
		t.Fatalf("clean time after MarkOnDisc: got %v, %v, want %v", carried, ok, cleanAt)
	}
}

// TestSecondVerifyKeepsTheFirstCleanTime checks that the retention
// period counts from the first verify: a later verify raises the count
// and leaves the clean time exactly as it was.
func TestSecondVerifyKeepsTheFirstCleanTime(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := object.ComputeID([]byte("chunk c"))
	discUUID := [16]byte{0x1D}

	if err := l.MarkPacked(id, 1, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkBurned(id, 1, discUUID); err != nil {
		t.Fatal(err)
	}
	if err := l.MarkVerified(id); err != nil {
		t.Fatal(err)
	}
	first, ok := l.CleanTime(id)
	if !ok {
		t.Fatal("CleanTime reported no time after the first verify")
	}

	time.Sleep(time.Millisecond)
	if err := l.MarkVerified(id); err != nil {
		t.Fatal(err)
	}
	second, ok := l.CleanTime(id)
	if !ok || !second.Equal(first) {
		t.Fatalf("CleanTime after the second verify: got %v, %v, want %v", second, ok, first)
	}

	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	reopened, ok := l2.CleanTime(id)
	if !ok || !reopened.Equal(first) {
		t.Fatalf("reopened CleanTime: got %v, %v, want %v", reopened, ok, first)
	}
}

// TestPartialTailRecordIsCut writes a real short file: two good records
// and a few bytes of a third. Open must cut the partial record and
// report the cut, and the file on the disk must shrink, so the next
// append lands right after the last good record.
func TestPartialTailRecordIsCut(t *testing.T) {
	dir := t.TempDir()
	id1 := object.ComputeID([]byte("one"))
	id2 := object.ComputeID([]byte("two"))
	id3 := object.ComputeID([]byte("three"))

	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id1); err != nil {
		t.Fatal(err)
	}
	if err := l.EnsureStaged(id2); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, stateFileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{1, 2, 3, 4, 5}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	truncated, ignored := l2.Truncated()
	if !truncated || ignored != 5 {
		t.Fatalf("Truncated() = (%v, %d), want (true, 5)", truncated, ignored)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != int64(2*recordLen) {
		t.Fatalf("file size after Open = %d, want %d", fi.Size(), 2*recordLen)
	}

	if err := l2.EnsureStaged(id3); err != nil {
		t.Fatal(err)
	}
	l3, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l3.Get(id3); !ok {
		t.Fatal("the record appended after the cut must replay")
	}
	if truncated, _ := l3.Truncated(); truncated {
		t.Fatal("Truncated() = true after the partial tail was cut")
	}
}

// TestBadRecordInTheMiddleIsAnError writes a real short file of three
// records and breaks the middle one. Good records follow it, so this is
// damage, not a torn tail: Open must report it and leave the file
// alone, rather than silently drop the good records behind it.
func TestBadRecordInTheMiddleIsAnError(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two", "three"} {
		if err := l.EnsureStaged(object.ComputeID([]byte(name))); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(dir, stateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[recordLen+1] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(dir); err == nil {
		t.Fatal("Open accepted a log with a bad record in the middle")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != int64(len(data)) {
		t.Fatalf("Open changed the file: size %d, want %d", fi.Size(), len(data))
	}
}
