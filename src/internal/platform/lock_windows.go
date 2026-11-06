//go:build windows

package platform

import (
	"io"
	"os"
	"syscall"
)

// LockFile makes one attempt to take the process lock on path (DESIGN §16.1).
//
// It opens (creating if needed) path with share mode 0, so while the handle is
// open every other open of the file fails with ERROR_SHARING_VIOLATION, in
// this process and in any other. IsLockBusy reports that case. The handle:
//
//   - is not inheritable (nil security attributes), so child processes
//     cannot keep the lock alive;
//   - is released by the kernel when the process dies (TerminateProcess,
//     Task Manager), so a crash never leaves the database locked;
//   - is released by Close on the returned value.
//
// The lock file is never deleted, by design.
func LockFile(path string) (io.Closer, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "lock", Path: path, Err: err}
	}
	h, err := syscall.CreateFile(p,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0,   // share mode 0: exclusive
		nil, // not inheritable
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		return nil, &os.PathError{Op: "lock", Path: path, Err: err}
	}
	// os.NewFile takes ownership of the handle; Close calls CloseHandle.
	return os.NewFile(uintptr(h), path), nil
}

// IsLockBusy reports whether err from LockFile means another handle holds
// the lock.
func IsLockBusy(err error) bool { return IsSharingViolation(err) }
