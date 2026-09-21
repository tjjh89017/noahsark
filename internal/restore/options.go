package restore

import "maps"

// Option configures an optional restriction on a Restore or RestoreMulti
// call.
type Option func(*restoreOptions)

// restoreOptions holds every optional restriction a restore call accepts.
type restoreOptions struct {
	includes   []string
	overwrite  bool
	knownDiscs map[[16]byte]DiscName
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
// already exists leaves that path alone, reports it in the Report and
// never opens it for truncation.
func WithOverwrite(overwrite bool) Option {
	return func(o *restoreOptions) {
		o.overwrite = overwrite
	}
}

// WithKnownDiscs names every disc the caller's own repository ledger or
// cache knows about, uuid to its number and label. A missing-disc error then names
// candidates from this whole set, not only the discs a provided disc's
// own DISCS table happens to mention, so a candidate list covers a disc
// packed after every provided disc too, not only an earlier one.
func WithKnownDiscs(discs map[[16]byte]DiscName) Option {
	return func(o *restoreOptions) {
		if o.knownDiscs == nil {
			o.knownDiscs = make(map[[16]byte]DiscName, len(discs))
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
