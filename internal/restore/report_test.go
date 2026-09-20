package restore

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestReportCapsHeldProblems asserts the one cap: past ProblemCap the
// report counts a problem and drops its path, so a restore into a full
// output directory cannot grow its report without limit.
func TestReportCapsHeldProblems(t *testing.T) {
	var rep Report
	for i := range ProblemCap + 5 {
		rep.add(KindExists, fmt.Sprintf("/out/f%d", i), errExists)
	}
	if len(rep.Problems) != ProblemCap {
		t.Fatalf("held %d problems, want %d", len(rep.Problems), ProblemCap)
	}
	if rep.Count(KindExists) != ProblemCap+5 {
		t.Fatalf("count = %d, want %d", rep.Count(KindExists), ProblemCap+5)
	}
	if rep.Dropped() != 5 {
		t.Fatalf("dropped = %d, want 5", rep.Dropped())
	}
}

// TestReportExitRule asserts the one exit-code rule: an unsupported
// entry alone never fails a restore, and every other kind does.
func TestReportExitRule(t *testing.T) {
	var unsupported Report
	unsupported.add(KindUnsupported, "/out/pipe", errors.New("FIFO not restored"))
	if unsupported.Failed() {
		t.Fatal("an unsupported entry alone must not fail the restore")
	}
	for _, k := range []Kind{KindExists, KindBlocked, KindMetadata, KindFile} {
		var rep Report
		rep.add(KindUnsupported, "/out/pipe", errors.New("FIFO not restored"))
		rep.add(k, "/out/a", errors.New("boom"))
		if !rep.Failed() {
			t.Fatalf("kind %d must fail the restore", k)
		}
	}
}

// TestReportSummaryNamesEveryKind asserts that the one summary line
// counts every kind the restore met, and that an empty report has no
// summary line at all.
func TestReportSummaryNamesEveryKind(t *testing.T) {
	var empty Report
	if empty.Summary() != "" {
		t.Fatalf("summary = %q, want none", empty.Summary())
	}

	var rep Report
	rep.add(KindExists, "/out/a", errExists)
	rep.add(KindUnsupported, "/out/pipe", errors.New("FIFO not restored"))
	rep.add(KindUnsupported, "/out/dev", errors.New("block device not restored"))
	got := rep.Summary()
	for _, want := range []string{"1 existing path(s)", "2 unsupported entry(ies)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary = %q, want it to hold %q", got, want)
		}
	}
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("summary = %q, want one line", got)
	}
}

// TestEntryTypeNamesAreWords asserts that an unsupported entry reports
// its own kind by name, never a raw entry type number.
func TestEntryTypeNamesAreWords(t *testing.T) {
	want := map[uint8]string{4: "character device", 5: "block device", 6: "FIFO", 7: "socket"}
	for entryType, name := range want {
		if got := entryTypeName(entryType); got != name {
			t.Fatalf("entryTypeName(%d) = %q, want %q", entryType, got, name)
		}
	}
}
