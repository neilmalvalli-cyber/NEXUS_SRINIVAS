//go:build !windows

package platform

import (
	"errors"
	"syscall"
)

// IsDiskFull reports whether err means the filesystem is out of space.
func IsDiskFull(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == syscall.ENOSPC || errno == syscall.EDQUOT
}

// DiskFullError returns an error value that IsDiskFull accepts, so tests in
// other packages can inject a realistic out-of-space failure.
func DiskFullError() error { return syscall.ENOSPC }

// SharingViolationError returns nil outside Windows: there is no such error
// to inject. Tests that need one skip when it is nil.
func SharingViolationError() error { return nil }

// IsSharingViolation is always false outside Windows: Unix has no
// mandatory share modes. The process lock uses flock there (Phase 2).
func IsSharingViolation(err error) bool { return false }
