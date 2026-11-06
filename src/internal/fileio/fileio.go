// Package fileio is the engine's only door to the filesystem (DESIGN §3
// "fileOps", §20.1).
//
// Every component that touches disk (locking, authority resolution, creation,
// later the log writer, recovery scanner and compaction) does so through the
// FS and File interfaces below. Production code uses OS; tests wrap it with
// fileio/faultfs to inject failures (errors, short writes, short reads,
// operations that take effect but report failure) and to record the exact
// order of operations.
//
// The interfaces are deliberately small: only what the design needs.
// Platform-specific behaviour (non-overwriting move, lock files, directory
// sync, error classification) is delegated to package platform.
package fileio

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
)

// File is an open database or temporary file.
//
// ReadAt and WriteAt follow the io.ReaderAt / io.WriterAt contracts, but
// callers must not rely on implementations honouring them: use ReadFullAt and
// WriteFullAt, which check the byte counts themselves.
type File interface {
	io.ReaderAt
	io.WriterAt
	// Sync flushes file data to stable storage (FlushFileBuffers on Windows).
	Sync() error
	// Truncate changes the file size.
	Truncate(size int64) error
	// Size returns the current file size (a stat call).
	Size() (int64, error)
	// Close releases the handle. Calling it twice returns an error.
	Close() error
	// Name returns the path the file was opened with.
	Name() string
}

// FS is the set of filesystem operations the engine performs.
type FS interface {
	// Stat reports metadata for name; a missing file yields fs.ErrNotExist.
	Stat(name string) (fs.FileInfo, error)
	// Open opens an existing file read-only.
	Open(name string) (File, error)
	// OpenRW opens an existing file for reading and writing; it never creates.
	OpenRW(name string) (File, error)
	// CreateExclusive creates a new file for reading and writing and fails
	// with fs.ErrExist if name already exists. It never truncates or replaces.
	CreateExclusive(name string) (File, error)
	// Remove deletes name; a missing file yields fs.ErrNotExist.
	Remove(name string) error
	// MoveNoReplace renames from to to and fails with fs.ErrExist if to
	// exists. It is the only rename the engine uses (DESIGN §11.1).
	MoveNoReplace(from, to string) error
	// SyncDir makes directory-entry changes in dir durable where the OS
	// needs it (fsync on Unix). It is a no-op on Windows (DESIGN §6.4).
	SyncDir(dir string) error
	// Lock makes ONE attempt to take the exclusive process lock on name
	// (DESIGN §16.1). A lock held elsewhere fails with an error for which
	// platform.IsLockBusy is true. Closing the returned value releases it.
	// Retrying is the caller's job (package locking).
	Lock(name string) (io.Closer, error)
}

// ErrShortRead means the file ended before the requested bytes could be
// read. It is reported only when the OS signalled end-of-file; real read
// errors are returned as they are and are never converted into it.
var ErrShortRead = errors.New("fileio: unexpected end of file")

// ReadFullAt reads exactly len(p) bytes at off.
//
// Returns nil on success, an error wrapping ErrShortRead if the file ended
// first, or the underlying error unchanged (wrapped) for any real I/O
// failure. This separation is what lets recovery obey "never treat a read
// error as EOF" (DESIGN §8.2 rule C8).
func ReadFullAt(f File, p []byte, off int64) error {
	n, err := f.ReadAt(p, off)
	if n == len(p) {
		// io.ReaderAt may return io.EOF together with a full buffer at the
		// very end of the file; that is still a full read.
		if err == nil || errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("fileio: read %s at %d: %w", f.Name(), off, err)
	}
	if err == nil || errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %s: wanted %d bytes at offset %d, got %d",
			ErrShortRead, f.Name(), len(p), off, n)
	}
	return fmt.Errorf("fileio: read %s at %d: %w", f.Name(), off, err)
}

// WriteFullAt writes all of p at off. It returns the number of bytes the
// implementation reported as written (needed by callers that must truncate
// back after a partial write) and an error if fewer than len(p) bytes were
// written, even when the implementation broke the io.WriterAt contract by
// returning a short count without an error.
func WriteFullAt(f File, p []byte, off int64) (int, error) {
	n, err := f.WriteAt(p, off)
	if err != nil {
		return n, fmt.Errorf("fileio: write %s at %d: %w", f.Name(), off, err)
	}
	if n != len(p) {
		return n, fmt.Errorf("fileio: write %s at %d: wrote %d of %d bytes: %w",
			f.Name(), off, n, len(p), io.ErrShortWrite)
	}
	return n, nil
}

// OS is the production FS backed by the operating system.
type OS struct{}

var _ FS = OS{}

func (OS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

func (OS) Open(name string) (File, error) { return wrap(os.Open(name)) }

func (OS) OpenRW(name string) (File, error) { return wrap(os.OpenFile(name, os.O_RDWR, 0)) }

func (OS) CreateExclusive(name string) (File, error) {
	return wrap(os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644))
}

func (OS) Remove(name string) error { return os.Remove(name) }

func (OS) MoveNoReplace(from, to string) error { return platform.MoveNoReplace(from, to) }

func (OS) SyncDir(dir string) error { return platform.SyncDir(dir) }

func (OS) Lock(name string) (io.Closer, error) { return platform.LockFile(name) }

func wrap(f *os.File, err error) (File, error) {
	if err != nil {
		return nil, err
	}
	return osFile{f}, nil
}

// osFile adapts *os.File. os.File.WriteAt already loops until every byte is
// written or an error occurs; WriteFullAt checks the count anyway.
type osFile struct{ f *os.File }

func (o osFile) ReadAt(p []byte, off int64) (int, error)  { return o.f.ReadAt(p, off) }
func (o osFile) WriteAt(p []byte, off int64) (int, error) { return o.f.WriteAt(p, off) }
func (o osFile) Sync() error                              { return o.f.Sync() }
func (o osFile) Truncate(size int64) error                { return o.f.Truncate(size) }
func (o osFile) Close() error                             { return o.f.Close() }
func (o osFile) Name() string                             { return o.f.Name() }

func (o osFile) Size() (int64, error) {
	st, err := o.f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}
