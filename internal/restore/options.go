package restore

// Option configures an optional restriction on a Restore or RestoreMulti
// call.
type Option func(*restoreOptions)

// restoreOptions holds every optional restriction a restore call accepts.
type restoreOptions struct {
	includes  []string
	overwrite bool
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

func newRestoreOptions(opts []Option) *restoreOptions {
	o := &restoreOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return o
}
