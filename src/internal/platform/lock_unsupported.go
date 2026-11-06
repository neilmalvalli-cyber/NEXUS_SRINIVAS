//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package platform

import (
	"errors"
	"io"
)

// ErrLockUnsupported is returned on platforms without a supported lock
// mechanism. Opening a database there fails rather than running unlocked.
var ErrLockUnsupported = errors.New("platform: process lock not supported on this OS")

func LockFile(path string) (io.Closer, error) { return nil, ErrLockUnsupported }

func IsLockBusy(err error) bool { return false }
