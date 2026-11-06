package kvdb

import "github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/storage"

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
