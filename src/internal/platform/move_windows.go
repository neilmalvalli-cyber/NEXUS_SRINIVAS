//go:build windows

package platform

import (
	"os"
	"syscall"
	"unsafe"
)

// MoveFileExW flags (winbase.h).
const (
	movefileReplaceExisting = 0x1 // NEVER used: it would let a move clobber `path`
	movefileWriteThrough    = 0x8 // return only after the move is flushed to disk
)

// moveFlags is the exact flag set MoveNoReplace passes. It is a variable only
// so the tests can assert its value; nothing changes it.
var moveFlags uint32 = movefileWriteThrough

var procMoveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

// MoveNoReplace renames from to to, failing if to already exists.
//
// It calls MoveFileExW with MOVEFILE_WRITE_THROUGH and WITHOUT
// MOVEFILE_REPLACE_EXISTING. Without REPLACE_EXISTING, MoveFileExW never
// replaces an existing destination: it fails with ERROR_ALREADY_EXISTS (183)
// or ERROR_FILE_EXISTS (80), which errors.Is maps to fs.ErrExist. Both names
// must be in the same directory (same volume), which the engine guarantees by
// deriving every temp name from the data-file path.
//
// WRITE_THROUGH makes the call return only after the rename has been flushed
// by the file system, so the new directory entry is not left in a cache when
// the compaction reports success. This is a best-effort strengthening: it is
// not evidence of power-loss durability, which is still not claimed on
// Windows (docs/DESIGN.md §2.3). Under the official crash model (process
// kill) a completed move survives either way.
//
// It does NOT retry. A sharing violation (an open handle without
// FILE_SHARE_DELETE, e.g. any file opened by Go's os package, or a scanner)
// is returned to the caller, which owns the bounded-backoff policy.
func MoveNoReplace(from, to string) error {
	pf, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return &os.LinkError{Op: "movefileex", Old: from, New: to, Err: err}
	}
	pt, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return &os.LinkError{Op: "movefileex", Old: from, New: to, Err: err}
	}
	if err := procMoveFileExW.Find(); err != nil {
		return &os.LinkError{Op: "movefileex", Old: from, New: to, Err: err}
	}
	r, _, e := procMoveFileExW.Call(uintptr(unsafe.Pointer(pf)), uintptr(unsafe.Pointer(pt)), uintptr(moveFlags))
	if r == 0 {
		if e == nil || e == syscall.Errno(0) {
			e = syscall.EINVAL
		}
		return &os.LinkError{Op: "movefileex", Old: from, New: to, Err: e}
	}
	return nil
}
