// Package dberr defines the engine's public sentinel errors.
//
// They live in an internal leaf package so that every internal component
// (storage, recovery, compaction, admission, locking) can return them without
// importing the public kvdb package, which would create an import cycle. The
// kvdb package re-exports each one under the same name.
package dberr

import "errors"

var (
	// ErrNotFound: Get on a key that is absent. Delete of a missing key is
	// NOT an error [REQ §2].
	ErrNotFound = errors.New("kvdb: key not found")

	// ErrInvalidArgument: a request violates an API rule (empty key, key
	// longer than 1024 bytes, value on a delete, max_bytes in 1..31, ...).
	// Nothing was written.
	ErrInvalidArgument = errors.New("kvdb: invalid argument")

	// ErrTooLarge: a value exceeds 1,048,576 bytes or a batch exceeds 10,000
	// operations. Nothing was written.
	ErrTooLarge = errors.New("kvdb: too large")

	// ErrFull: the write cannot be admitted under max_bytes, or the disk
	// reported out-of-space and the partial frame was rolled back. The
	// committed database is unchanged and fully readable. [ASSUME] policy in
	// docs/JUDGE_CLARIFICATIONS.md.
	ErrFull = errors.New("kvdb: database full")

	// ErrClosed: the handle was closed.
	ErrClosed = errors.New("kvdb: database closed")

	// ErrFailed: the handle hit an unrecoverable I/O failure earlier and
	// refuses every operation except Close. Reopen the database; Open
	// resolves the authoritative state (docs/DESIGN.md §13.2).
	ErrFailed = errors.New("kvdb: handle failed; reopen required")

	// ErrOutcomeUnknown: a write failed after bytes may have reached the file
	// and rollback could not be proven. The operation may or may not be
	// present after the next Open (it is never partially present). The handle
	// is now failed.
	ErrOutcomeUnknown = errors.New("kvdb: outcome unknown")

	// ErrCorruption: the file IS one of our databases (its superblock is
	// valid) but its content violates the format in a way a killed write
	// cannot cause: snapshot_end beyond EOF, damage inside the sealed
	// snapshot, a checksum failure followed by more data, a wrong sequence
	// number, a checksum-valid but malformed frame (Open), or an indexed entry
	// that fails verification (Get). The engine refuses and never truncates or
	// rewrites anything. After a Get, the handle stays open.
	// See docs/DESIGN.md §8.2 (rules C0, C6, C7) and §13.3.
	ErrCorruption = errors.New("kvdb: corruption detected")

	// ErrBadFormat: the file is NOT a database this version can open. Its
	// superblock does not decode: shorter than 32 bytes (including empty), bad
	// magic, bad superblock checksum, unsupported version, or invalid
	// superblock fields. Returned only by Open, for `path` (authority rule A2)
	// or a damaged `.old` (rule A6). Open renames, deletes and creates nothing
	// in that case. See docs/DESIGN.md §11.4 and §13.3.
	//
	// ErrBadFormat and ErrCorruption never overlap: a valid superblock decides
	// between them. An OS read error is neither; it is returned wrapped.
	ErrBadFormat = errors.New("kvdb: not a valid database file")

	// ErrLocked: another process holds the database lock file.
	ErrLocked = errors.New("kvdb: database is locked by another process")
)
