package kvdb

import (
	"fmt"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/storage"
)

// DB is an open database. All methods are safe for concurrent use by
// multiple goroutines. Only one DB per database file can be open at a time,
// across all processes (ErrLocked otherwise).
type DB struct {
	log      *storage.Log
	maxBytes int64
}

// Open opens the database at path, creating it if neither it nor any
// recoverable temporary file exists, and recovers it after any crash
// (docs/DESIGN.md §8, §11.4).
//
// maxBytes caps the size of the data file: 0 means unlimited; negative
// values and values 1–31 are rejected with ErrInvalidArgument. The policy
// (DESIGN §12.3, assumption A2): an existing database opens even if it is
// already larger than maxBytes; a write that stores a value must leave the
// file at or below maxBytes, using a compacting commit when an append would
// not fit; a write that only deletes may run on an over-cap file only if it
// strictly shrinks it; anything else fails with ErrFull and changes nothing.
// The temporary .compact file used by compaction is not counted.
//
// Errors: ErrInvalidArgument, ErrLocked, ErrBadFormat (not a database file),
// ErrCorruption (a database whose content a crash cannot explain), or an
// operating-system error, returned wrapped.
func Open(path string, maxBytes int64) (*DB, error) {
	return openWith(fileio.OS{}, path, maxBytes, recovery.DefaultOptions)
}

func openWith(fsys fileio.FS, path string, maxBytes int64, opt recovery.Options) (*DB, error) {
	if maxBytes < 0 || (maxBytes > 0 && maxBytes < format.SuperblockSize) {
		return nil, fmt.Errorf("%w: max_bytes %d (use 0 for unlimited, or at least %d)",
			ErrInvalidArgument, maxBytes, format.SuperblockSize)
	}
	if path == "" {
		return nil, fmt.Errorf("%w: empty path", ErrInvalidArgument)
	}
	l, err := storage.OpenWith(fsys, path, storage.Config{Recovery: opt, MaxBytes: maxBytes, AutoCompact: true})
	if err != nil {
		return nil, err
	}
	return &DB{log: l, maxBytes: maxBytes}, nil
}

func checkKey(key []byte) error {
	switch {
	case len(key) == 0:
		return fmt.Errorf("%w: empty key", ErrInvalidArgument)
	case len(key) > MaxKeyLen:
		return fmt.Errorf("%w: key is %d bytes (max %d)", ErrInvalidArgument, len(key), MaxKeyLen)
	}
	return nil
}

// Put stores value under key. When Put returns nil the write is durable: it
// was synced to the file before Put returned. The caller must not modify key
// or value until Put returns. A nil or empty value is stored as an empty
// value (Get returns a non-nil empty slice).
func (db *DB) Put(key, value []byte) error {
	op := Op{Kind: OpPut, Key: key, Value: value}
	if err := op.Validate(); err != nil {
		return err
	}
	return db.log.Commit([]format.Entry{{Op: format.OpPut, Key: key, Value: value}})
}

// Get returns an independent copy of the value stored under key, or
// ErrNotFound. An entry that fails verification on disk is ErrCorruption;
// the handle stays usable.
func (db *DB) Get(key []byte) ([]byte, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	return db.log.Get(key)
}

// Delete removes key. Deleting a key that does not exist is not an error and
// performs no I/O. When Delete returns nil the deletion is durable.
func (db *DB) Delete(key []byte) error {
	if err := checkKey(key); err != nil {
		return err
	}
	return db.log.Delete(key)
}

// Batch applies ops atomically as one durable operation: after a crash
// either all of them are present or none is. They apply in order and the
// last operation on a key wins (see Op). An empty batch is a no-op. At most
// MaxBatchOps operations. The caller must not modify any Key or Value until
// Batch returns.
func (db *DB) Batch(ops []Op) error {
	if err := ValidateBatch(ops); err != nil {
		return err
	}
	entries := make([]format.Entry, len(ops))
	for i, o := range ops {
		entries[i] = format.Entry{Op: format.OpCode(o.Kind), Key: o.Key, Value: o.Value}
	}
	return db.log.Commit(entries)
}

// Compact rewrites the database so that it holds only the latest value of
// each live key, and replaces the file crash-safely (DESIGN §10, §11). It
// never makes the file larger; when that would happen, or there is nothing
// to reclaim, it does nothing and returns nil. Compaction also runs
// automatically when the file is at least 4 MiB and more than twice its
// compacted size, and when a write needs it to fit maxBytes.
//
// Errors: ErrClosed, ErrFailed, ErrFull (out of disk space; nothing
// changed), ErrCorruption (a stored value failed verification; nothing
// changed), another wrapped OS error (nothing changed), or ErrFailed when
// the file swap failed without a proven restore; then reopen, and Open
// completes the swap or keeps the old file.
func (db *DB) Compact() error { return db.log.Compact() }

// Close releases the file and the process lock. Every acknowledged write is
// already durable, so Close is not needed for durability. Close is allowed
// after a failure (ErrFailed) and calling it twice returns nil. Any call
// other than Close after Close returns ErrClosed.
func (db *DB) Close() error { return db.log.Close() }
