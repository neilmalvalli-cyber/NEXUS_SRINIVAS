package storage

import (
	"errors"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
)

// ReadHandles is the number of extra read-only handles a Log may open for
// GET, Range and compaction copying (docs/PERFORMANCE_OPTIMIZATION.md,
// change C).
//
// Why: on Windows, Go's os.File serializes ReadAt calls on one handle (the
// poll.FD lock), and the kernel serializes synchronous I/O on one file
// object, so with a single shared handle parallel GETs queue behind each
// other and behind the writer's WriteAt and FlushFileBuffers. Separate
// read-only handles of the same file see the same cached data and do not
// share those locks.
//
// Safety: read handles only ever read offsets the index points at, which
// are durable and never change (append-only); they never write. They are
// opened lazily, and closed before the compaction renames (Windows cannot
// rename a file with open handles) and on Close. Any failure to open one
// falls back to the main handle, so this is purely an optimization.
const ReadHandles = 4

// readHandle returns a read-only handle for the current data file; pooled
// reports whether it must be given back with putHandle (false: it is the
// main handle). Callers hold mu (shared or exclusive) or wmu, so the data
// file cannot be swapped while the handle is in use. No allocation on the
// common path.
func (l *Log) readHandle() (f fileio.File, pooled bool) {
	select {
	case f := <-l.rpool:
		return f, true
	default:
	}
	if l.ropened.Add(1) <= ReadHandles {
		if f, err := l.fsys.Open(l.path); err == nil {
			return f, true
		}
	}
	l.ropened.Add(-1)
	return l.f, false
}

// putHandle gives back a handle obtained from readHandle.
func (l *Log) putHandle(f fileio.File, pooled bool) {
	if pooled {
		l.rpool <- f
	}
}

// closeReadHandles closes every pooled read handle. Callers hold mu
// exclusively and wmu, so no reader holds one: all opened handles are back
// in the pool. A close error of a read-only handle cannot lose data.
func (l *Log) closeReadHandles() error {
	var errs []error
	for {
		select {
		case f := <-l.rpool:
			errs = append(errs, f.Close())
			l.ropened.Add(-1)
		default:
			return errors.Join(errs...)
		}
	}
}
