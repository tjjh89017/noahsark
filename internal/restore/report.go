package restore

import (
	"errors"
	"fmt"

	"github.com/tjjh89017/noahsark/internal/format"
)

// ProblemCap is how many problems a Report keeps. A restore into a full
// output directory can meet one problem per path, so the report holds a
// bounded sample and counts the rest. A caller prints the sample and
// then the count of the problems it does not hold.
const ProblemCap = 20

// Kind classifies one restore problem. It decides the exit code: an
// unsupported entry is a warning, and every other kind is a loss the
// operator must act on.
type Kind int

const (
	// KindExists is a path the restore left exactly as found, because
	// --overwrite was not given.
	KindExists Kind = iota
	// KindBlocked is a path --overwrite could not replace, most often a
	// directory that still holds entries.
	KindBlocked
	// KindUnsupported is an entry this build does not restore: a FIFO, a
	// device node or a socket.
	KindUnsupported
	// KindMetadata is a mode, times or owner field that would not apply
	// to a path the restore had already written.
	KindMetadata
	// KindFile is a path the restore could not write: a bad object, or a
	// write that failed. The walk goes on to the next entry.
	KindFile
	numKinds
)

// label names one kind for the summary line, in the plural.
func (k Kind) label() string {
	switch k {
	case KindExists:
		return "existing path(s)"
	case KindBlocked:
		return "path(s) --overwrite could not replace"
	case KindUnsupported:
		return "unsupported entry(ies)"
	case KindMetadata:
		return "metadata field(s)"
	case KindFile:
		return "file(s) not restored"
	}
	return "problem(s)"
}

// Problem is one path a restore did not fully handle.
type Problem struct {
	Path string
	Kind Kind
	Err  error
}

// Report is the one record of what a restore did not do. Restore and
// RestoreMulti return it, and the disc-swap Manifest carries the same
// type, so every restore mode reports through one print loop.
type Report struct {
	// Resumed counts the paths that already held the snapshot's own
	// content, from an earlier, interrupted restore.
	Resumed int
	// Problems holds at most ProblemCap problems, in the order the walk
	// met them. Count gives the true total of each kind.
	Problems []Problem
	counts   [numKinds]int
}

// add records one problem. Past ProblemCap it counts the problem and
// drops its path, so a long restore's report stays bounded.
func (r *Report) add(kind Kind, path string, err error) {
	r.counts[kind]++
	if len(r.Problems) < ProblemCap {
		r.Problems = append(r.Problems, Problem{Path: path, Kind: kind, Err: err})
	}
}

// Count is how many problems of kind k the restore met, the dropped
// ones included.
func (r *Report) Count(k Kind) int { return r.counts[k] }

// Total is how many problems of every kind the restore met.
func (r *Report) Total() int {
	n := 0
	for _, c := range r.counts {
		n += c
	}
	return n
}

// Dropped is how many problems the report counted but does not hold.
func (r *Report) Dropped() int { return r.Total() - len(r.Problems) }

// Skipped is how many paths the restore left exactly as found: a path
// --overwrite was not given for, and a path --overwrite could not
// replace.
func (r *Report) Skipped() int { return r.counts[KindExists] + r.counts[KindBlocked] }

// Failed reports whether the restore lost data or metadata. An
// unsupported entry alone is a warning, so it never fails a restore.
func (r *Report) Failed() bool { return r.Total()-r.counts[KindUnsupported] > 0 }

// Summary is the one line that counts every kind the restore met, or ""
// when it met none.
func (r *Report) Summary() string {
	line := ""
	for k := range numKinds {
		if r.counts[k] == 0 {
			continue
		}
		if line != "" {
			line += ", "
		}
		line += fmt.Sprintf("%d %s", r.counts[k], k.label())
	}
	if line == "" {
		return ""
	}
	return "not restored: " + line + "; see the warning(s) above"
}

// errExists is the reason for every path a restore left as found
// because --overwrite was not given. It is one value, so a restore that
// meets many such paths allocates nothing per path.
var errExists = errors.New("a path is already here; pass --overwrite to replace it")

// entryTypeName names an entry type this build does not restore, so a
// warning line reads "FIFO" or "socket" rather than a number.
func entryTypeName(entryType uint8) string {
	switch entryType {
	case format.EntryTypeCharDev:
		return "character device"
	case format.EntryTypeBlockDev:
		return "block device"
	case format.EntryTypeFIFO:
		return "FIFO"
	case format.EntryTypeSocket:
		return "socket"
	}
	return fmt.Sprintf("entry type %d", entryType)
}

// writePolicy carries the overwrite rule through a restore walk and
// collects the walk's report.
type writePolicy struct {
	overwrite bool
	report    Report
}

// skip records one path left exactly as found, because --overwrite was
// not given.
func (wp *writePolicy) skip(path string) {
	if wp == nil {
		return
	}
	wp.report.add(KindExists, path, errExists)
}

// blocked records one path --overwrite could not clear. want names what
// the snapshot wanted there: "file", "directory" or "symlink".
func (wp *writePolicy) blocked(want, path string, unlinkErr error) {
	if wp == nil {
		return
	}
	wp.report.add(KindBlocked, path, fmt.Errorf("%s not created: %s", want, overwriteBlockReason(unlinkErr)))
}

// unsupported records one entry whose type this build does not restore.
func (wp *writePolicy) unsupported(path string, entryType uint8) {
	if wp == nil {
		return
	}
	wp.report.add(KindUnsupported, path, fmt.Errorf("%s not restored; this build restores a file, a directory or a symlink only", entryTypeName(entryType)))
}

// metadataFailed records one metadata field that would not apply.
// field is "mode", "times" or "owner".
func (wp *writePolicy) metadataFailed(path, field string, err error) {
	if wp == nil {
		return
	}
	wp.report.add(KindMetadata, path, fmt.Errorf("%s not applied: %w", field, err))
}

// failed records one path the restore could not write. The walk goes on
// to the next entry.
func (wp *writePolicy) failed(path string, err error) {
	if wp == nil {
		return
	}
	wp.report.add(KindFile, path, err)
}

// resume counts one path that already held the snapshot's own content.
func (wp *writePolicy) resume() {
	if wp == nil {
		return
	}
	wp.report.Resumed++
}
