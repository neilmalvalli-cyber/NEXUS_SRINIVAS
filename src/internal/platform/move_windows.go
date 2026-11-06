//go:build windows

package platform

import (
	"os"
	"syscall"
)

// MoveNoReplace renames from to to, failing if to already exists.
//
// It calls MoveFileW through the public syscall.MoveFile. MoveFileW never
// replaces an existing destination: it fails with ERROR_ALREADY_EXISTS (183)
// or ERROR_FILE_EXISTS (80), which errors.Is maps to fs.ErrExist. Both names
// must be in the same directory (same volume), which the engine guarantees by
// deriving every temp name from the data-file path.
//
// It does NOT retry. A sharing violation (an open handle without
// FILE_SHARE_DELETE, e.g. any file opened by Go's os package, or a scanner)
// is returned to the caller, which owns the bounded-backoff policy.
//
// Durability: under the official crash model (process kill), a completed
// MoveFileW survives. Power-loss durability of the directory entry is not
// claimed on Windows (docs/DESIGN.md §2.3).
func MoveNoReplace(from, to string) error {
	pf, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return &os.LinkError{Op: "movefile", Old: from, New: to, Err: err}
	}
	pt, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return &os.LinkError{Op: "movefile", Old: from, New: to, Err: err}
	}
	if err := syscall.MoveFile(pf, pt); err != nil {
		return &os.LinkError{Op: "movefile", Old: from, New: to, Err: err}
	}
	return nil
}
