package kvdb

import (
	"fmt"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/storage"
)

// Read-only inspection used by the CLI (open, dump, check). Added in Phase 4;
// none of these changes the file.

// Stats is a snapshot of a database's counters.
type Stats struct {
	Keys      int    // live keys
	LiveBytes uint64 // bytes of live PUT entries
	Size      int64  // data file size in bytes
	MaxBytes  int64  // the cap this handle enforces (0 = unlimited)
}

// RecoveryInfo describes what Open did: authority rules that fired, whether
// the database was created, and whether a torn tail was cut off.
type RecoveryInfo = storage.RecoveryInfo

// VerifyReport summarizes a successful Verify.
type VerifyReport = storage.VerifyReport

// Stats returns the current counters. It works in any state.
func (db *DB) Stats() Stats {
	s := db.log.Stats()
	return Stats{Keys: s.Keys, LiveBytes: s.LiveBytes, Size: s.Size, MaxBytes: s.MaxBytes}
}

// Recovery returns what Open did when this handle was opened.
func (db *DB) Recovery() RecoveryInfo { return db.log.Recovery() }

// Range calls fn for every live key in ascending byte order with its value,
// as one consistent snapshot (writers wait until Range returns). Every value
// is verified (entry CRC and key) before fn sees it. fn owns the slices.
// Iteration stops at the first error.
func (db *DB) Range(fn func(key, value []byte) error) error { return db.log.Range(fn) }

// Verify re-reads the whole file and checks it against the open handle:
// every frame and entry checksum, sequence numbers, no torn tail, the index
// rebuilt from disk equal to the in-memory index, and every live value
// readable. It does not modify the file. A mismatch is ErrCorruption.
func (db *DB) Verify() (VerifyReport, error) { return db.log.Verify() }

// ReadOnlyCheck is the result of CheckReadOnly.
type ReadOnlyCheck struct {
	Keys      int      // live keys in the (would-be) database
	Frames    uint64   // valid frames
	Size      int64    // file size on disk
	End       int64    // end of the last valid frame (== Size unless a torn tail exists)
	LiveBytes uint64   // bytes of live PUT entries
	Rules     []string // authority rules a normal open would apply
	Target    string   // the file that was scanned
	Missing   bool     // no database exists; a normal open would create one
	Tail      string   // tail rule of a torn tail ("" if none)
	Repairs   []string // every change a normal open would make, in plain words
}

// NeedsRepair reports whether a normal Open would change any file.
func (c ReadOnlyCheck) NeedsRepair() bool { return len(c.Repairs) > 0 }

// CheckReadOnly verifies the database at path WITHOUT modifying it
// (`kvdb check --no-repair`). It takes the process lock (creating the empty
// `<path>.lock` file if needed, nothing else), applies the same authority
// table and the same frame-by-frame scan as Open, and reports what Open
// would repair instead of repairing it: no truncation, move, deletion,
// creation, write or sync of any database or temporary file.
//
// A torn tail or an interrupted compaction is not an error (both are normal
// crash states); they appear in Repairs. Errors are ErrLocked, ErrBadFormat,
// ErrCorruption (damage that is not a torn tail, including damage inside the
// compacted snapshot), or an I/O error.
func CheckReadOnly(path string) (ReadOnlyCheck, error) {
	if path == "" {
		return ReadOnlyCheck{}, fmt.Errorf("%w: empty path", ErrInvalidArgument)
	}
	in, err := recovery.Inspect(fileio.OS{}, path, recovery.DefaultOptions)
	if err != nil {
		return ReadOnlyCheck{}, err
	}
	c := ReadOnlyCheck{Target: in.Target, Missing: in.Missing, Repairs: in.Repairs()}
	for _, r := range in.Rules {
		c.Rules = append(c.Rules, string(r))
	}
	if s := in.Scan; s != nil {
		c.Keys, c.Frames, c.Size, c.End = s.Index.Len(), s.Frames, s.Size, s.End
		c.LiveBytes, c.Tail = s.Index.LiveBytes(), string(s.Tail)
	}
	return c, nil
}
