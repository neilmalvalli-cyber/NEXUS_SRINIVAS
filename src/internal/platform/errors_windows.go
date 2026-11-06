//go:build windows

package platform

import (
	"errors"
	"syscall"
)

// Windows error codes the engine reacts to. They are not defined in Go's
// public syscall package (verified on Go 1.27.3), so they are declared here
// and nowhere else.
const (
	errorSharingViolation syscall.Errno = 32  // ERROR_SHARING_VIOLATION
	errorLockViolation    syscall.Errno = 33  // ERROR_LOCK_VIOLATION
	errorHandleDiskFull   syscall.Errno = 39  // ERROR_HANDLE_DISK_FULL
	errorDiskFull         syscall.Errno = 112 // ERROR_DISK_FULL
)

// IsDiskFull reports whether err (possibly wrapped, e.g. in *os.PathError)
// means the volume is out of space. The write path uses it to decide between
// ErrFull (definite, after a proven rollback) and ErrOutcomeUnknown.
func IsDiskFull(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == errorDiskFull || errno == errorHandleDiskFull
}

// DiskFullError returns an error value that IsDiskFull accepts, so tests in
// other packages can inject a realistic out-of-space failure.
func DiskFullError() error { return errorDiskFull }

// SharingViolationError returns an error value that IsSharingViolation
// accepts. It exists so tests in other packages can inject a realistic
// sharing violation without spelling out a Windows error number.
func SharingViolationError() error { return errorSharingViolation }

// IsSharingViolation reports whether err means another handle (another
// process, an antivirus scanner, a backup tool, or an open handle in this
// process) denies the requested access. Callers retry these with bounded
// backoff; for the lock file, persistent violations become ErrLocked.
func IsSharingViolation(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == errorSharingViolation || errno == errorLockViolation
}
