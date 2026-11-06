// Package locking provides the process-level database lock (DESIGN §16.1).
//
// Acquire is the first filesystem operation Open performs (DESIGN §8 step 0,
// invariant 19). It returns a *Lock, which other packages demand as proof
// that the lock is held before they rename, delete, create or truncate
// anything (see recovery.Resolve).
//
// Mechanism (in package platform): exclusive share mode on Windows, flock on
// Unix. This package adds the bounded retry and the mapping of "lock busy"
// to dberr.ErrLocked.
package locking
