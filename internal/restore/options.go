package restore

import "maps"

// Option configures an optional restriction on a Restore or RestoreMulti
// call.
type Option func(*restoreOptions)

// restoreOptions holds every optional restriction a restore call accepts.
type restoreOptions struct {
	includes           []string
	overwrite          bool
	knownDiscs         map[[16]byte]string
	onUnsupported      func(path string, entryType uint8)
	onOverwriteBlocked func(entry OverwriteBlockedEntry)
	onMetadataFailure  func(f MetadataFailure)
}

// WithUnsupportedEntry registers fn, called once for every entry whose
// type this build does not restore: a device node, a FIFO or a socket.
// The restore records the entry and continues, so a later entry in the
// walk still reaches its path.
func WithUnsupportedEntry(fn func(path string, entryType uint8)) Option {
	return func(o *restoreOptions) {
		o.onUnsupported = fn
	}
}

// WithOverwriteBlocked registers fn, called once for every path
// --overwrite could not replace, most often a directory that still
// holds entries. The path is left exactly as found, and the walk
// continues into every other path.
func WithOverwriteBlocked(fn func(entry OverwriteBlockedEntry)) Option {
	return func(o *restoreOptions) {
		o.onOverwriteBlocked = fn
	}
}

// WithMetadataFailure registers fn, called once for every
// metadata_not_applied event: a mode, times or owner field that a
// path's Chmod, Chtimes or Chown could not apply. The restore records
// the event and continues; the file itself was already written.
func WithMetadataFailure(fn func(f MetadataFailure)) Option {
	return func(o *restoreOptions) {
		o.onMetadataFailure = fn
	}
}

// WithInclude restricts a restore to these snapshot-relative paths, and
// everything under a path that names a directory. Several WithInclude
// paths, or several calls, union. A path that matches nothing in the
// snapshot fails the restore before any file is written.
func WithInclude(paths []string) Option {
	return func(o *restoreOptions) {
		o.includes = append(o.includes, paths...)
	}
}

// WithOverwrite allows a restore to replace an existing path: unlink it,
// then create the new file. Without it, a restore that meets a path that
// already exists leaves that path alone, counts it as skipped and never
// opens it for truncation.
func WithOverwrite(overwrite bool) Option {
	return func(o *restoreOptions) {
		o.overwrite = overwrite
	}
}

// WithKnownDiscs names every disc the caller's own repository ledger or
// cache knows about, uuid to label. A missing-disc error then names
// candidates from this whole set, not only the discs a provided disc's
// own DISCS table happens to mention, so a candidate list covers a disc
// packed after every provided disc too, not only an earlier one.
func WithKnownDiscs(discs map[[16]byte]string) Option {
	return func(o *restoreOptions) {
		if o.knownDiscs == nil {
			o.knownDiscs = make(map[[16]byte]string, len(discs))
		}
		maps.Copy(o.knownDiscs, discs)
	}
}

func newRestoreOptions(opts []Option) *restoreOptions {
	o := &restoreOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return o
}
