package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/tjjh89017/noahsark/internal/catalog"
	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

func init() {
	register(&command{
		name:  "log",
		usage: "log [REF | SNAPSHOT]",
		summary: "List the snapshots of the catalog, newest first. One snapshot on each line: " +
			"id, time, refs, source path and message, separated by a tab.",
		flags: func(*flag.FlagSet) runFunc { return cmdLog },
	})
}

// cmdLog implements "noahsark log". It reads the catalog only, takes no
// lock and changes no file.
func cmdLog(e *env, args []string) int {
	const cmd = "log"
	if len(args) > 1 {
		_, _ = fmt.Fprintln(e.stderr, "usage: noahsark log [REF | SNAPSHOT]")
		return 2
	}
	rc, code := openRepoCatalog(e, cmd)
	if rc == nil {
		return code
	}

	var ids []object.ID
	if len(args) == 1 {
		id, code, ok := rc.resolve(cmd, args[0])
		if !ok {
			return code
		}
		if !rc.known(id) {
			if _, err := rc.src.Snapshot(id); err != nil {
				_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %v\n", cmd, &refNotFoundError{arg: args[0]})
				return 2
			}
		}
		ids = []object.ID{id}
	} else {
		var err error
		if ids, err = rc.logIDs(); err != nil {
			_, _ = fmt.Fprintf(e.stderr, "noahsark: %s: %v\n", cmd, err)
			return 1
		}
	}

	records := make([]logRecord, 0, len(ids))
	for _, id := range ids {
		records = append(records, rc.logRecord(id))
	}
	sortLogRecords(records)

	out := bufio.NewWriter(e.stdout)
	for _, r := range records {
		_, _ = fmt.Fprintln(out, r.line())
	}
	_ = out.Flush()

	code = 0
	if rc.src.refsDamaged {
		code = 1
	}
	for _, r := range records {
		if rc.c.Partial(r.id) {
			printPartial(e.stderr, cmd, r.id)
			code = 1
		}
	}
	return code
}

// logRecord is the content of one log line. held is false when the
// catalog does not hold the snapshot object: a ref names it, but the
// line has no time, source path or message.
type logRecord struct {
	id      object.ID
	held    bool
	sec     int64
	nsec    uint32
	refs    []string
	sources []string
	message string
}

// logIDs gives every snapshot of the catalog and every snapshot that a
// ref names, once each.
func (rc *repoCatalog) logIDs() ([]object.ID, error) {
	ids, err := rc.c.ListSnapshots()
	if err != nil {
		return nil, err
	}
	for _, r := range rc.refs.Records {
		if id := object.ID(r.SnapshotID); !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// logRecord collects the fields of the line of id. A root tree that the
// catalog does not hold gives no source path.
func (rc *repoCatalog) logRecord(id object.ID) logRecord {
	r := logRecord{id: id}
	for _, ref := range rc.refs.Records {
		if object.ID(ref.SnapshotID) == id {
			r.refs = append(r.refs, catalog.RefName(ref))
		}
	}
	slices.Sort(r.refs)
	snap, err := rc.src.Snapshot(id)
	if err != nil {
		return r
	}
	r.held = true
	r.sec, r.nsec = snap.TimeSec, snap.TimeNsec
	for _, m := range snap.Meta {
		if m.Tag == format.SnapshotMetaMessage {
			r.message = string(m.Value)
		}
	}
	if root, err := rc.src.Tree(object.ID(snap.RootTree)); err == nil {
		for _, e := range root.Entries {
			r.sources = append(r.sources, rootPathOf(e))
		}
	}
	return r
}

// sortLogRecords puts the records newest first, at the full precision of
// the snapshot time. The records whose object the catalog does not hold
// come last. The snapshot id breaks each tie.
func sortLogRecords(records []logRecord) {
	slices.SortFunc(records, func(a, b logRecord) int {
		if a.held != b.held {
			if a.held {
				return -1
			}
			return 1
		}
		if a.sec != b.sec {
			if a.sec > b.sec {
				return -1
			}
			return 1
		}
		if a.nsec != b.nsec {
			if a.nsec > b.nsec {
				return -1
			}
			return 1
		}
		return bytes.Compare(a.id[:], b.id[:])
	})
}

// noLogValue is the log field of a value that is absent.
const noLogValue = "-"

// line gives the log line: the 12-character id, the time, the refs, the
// source path and the message, separated by a tab. A field with no value
// is noLogValue. More than one ref or source root gives the values
// separated by ",".
func (r logRecord) line() string {
	fields := []string{shortID(r.id), noLogValue, noLogValue, noLogValue, noLogValue}
	if len(r.refs) > 0 {
		fields[2] = joinLogValues(r.refs)
	}
	if r.held {
		fields[1] = utcTime(r.sec)
		if len(r.sources) > 0 {
			fields[3] = joinLogValues(r.sources)
		}
		if r.message != "" {
			fields[4] = logValue(r.message)
		}
	}
	return strings.Join(fields, "\t")
}

// logValue gives the print form of one value of a log field. A value
// that is exactly noLogValue prints as `\x2d`, thus a field of noLogValue
// always means no value.
func logValue(s string) string {
	if s == noLogValue {
		return `\x2d`
	}
	return escapeField(s)
}

// joinLogValues gives the print form of each value, separated by ",". A
// "," inside a value prints as `\x2c`, thus each "," of the field
// separates two values.
func joinLogValues(values []string) string {
	escaped := make([]string, len(values))
	for i, v := range values {
		escaped[i] = strings.ReplaceAll(logValue(v), ",", `\x2c`)
	}
	return strings.Join(escaped, ",")
}
