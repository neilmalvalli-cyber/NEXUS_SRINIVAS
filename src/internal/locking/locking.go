package locking

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
)

// Suffix is appended to the data-file path to form the lock-file path.
const Suffix = ".lock"

// Path returns the lock-file path for a database path.
func Path(dbPath string) string { return dbPath + Suffix }

// Lock is a held process lock. The zero value is not a held lock.
type Lock struct {
	dbPath string
	mu     sync.Mutex
	c      io.Closer // nil once released
}

// ErrNotHeld is returned by Check for a nil, released or foreign lock.
var ErrNotHeld = errors.New("locking: database lock not held")

// Acquire takes the lock for the database at dbPath.
//
// A busy lock (held by another process, by another Open in this process, or
// briefly by a scanner) is retried according to policy; if it is still busy
// when the budget runs out, the result wraps dberr.ErrLocked. Any other error
// (permissions, missing directory, ...) is returned immediately, wrapped, and
// is NOT ErrLocked.
//
// Acquire touches only the lock file. It never reads, creates, renames or
// deletes the data file or any temporary file.
func Acquire(fsys fileio.FS, dbPath string, policy fileio.RetryPolicy) (*Lock, error) {
	name := Path(dbPath)
	var c io.Closer
	attempts, err := fileio.Retry(policy, platform.IsLockBusy, func() (e error) {
		c, e = fsys.Lock(name)
		return e
	})
	if err != nil {
		if platform.IsLockBusy(err) {
			return nil, fmt.Errorf("%w: %s (still busy after %d attempts): %v",
				dberr.ErrLocked, name, attempts, err)
		}
		return nil, fmt.Errorf("locking: acquire %s: %w", name, err)
	}
	return &Lock{dbPath: dbPath, c: c}, nil
}

// Check returns nil if l is held and belongs to dbPath. Components that
// modify files call it first, which makes "no cleanup before the lock"
// (DESIGN invariant 19) a checked rule rather than a convention.
func (l *Lock) Check(dbPath string) error {
	if l == nil {
		return ErrNotHeld
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.c == nil {
		return fmt.Errorf("%w: %s was released", ErrNotHeld, Path(l.dbPath))
	}
	if l.dbPath != dbPath {
		return fmt.Errorf("%w: lock is for %s, not %s", ErrNotHeld, l.dbPath, dbPath)
	}
	return nil
}

// Release gives the lock up. It is safe to call more than once; only the
// first call releases. The lock file itself is never deleted (DESIGN §16.1).
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.c == nil {
		return nil
	}
	err := l.c.Close()
	l.c = nil
	if err != nil {
		return fmt.Errorf("locking: release %s: %w", Path(l.dbPath), err)
	}
	return nil
}
