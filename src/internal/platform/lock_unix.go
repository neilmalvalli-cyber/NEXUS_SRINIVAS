//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package platform

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// LockFile makes one attempt to take the process lock on path using
// flock(LOCK_EX|LOCK_NB) (DESIGN §16.1, Linux is a secondary target). flock
// locks belong to the open file description, so a second LockFile in the same
// process also fails, matching Windows. The kernel releases the lock when the
// process dies. The lock file is never deleted.
func LockFile(path string) (io.Closer, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, &os.PathError{Op: "lock", Path: path, Err: err}
	}
	return f, nil
}

// IsLockBusy reports whether err from LockFile means another holder has it.
func IsLockBusy(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == syscall.EWOULDBLOCK || errno == syscall.EAGAIN
}
