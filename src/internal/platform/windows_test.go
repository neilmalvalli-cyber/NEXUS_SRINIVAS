//go:build windows

package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestErrorClassificationWindows(t *testing.T) {
	tests := []struct {
		errno            syscall.Errno
		diskFull, shared bool
	}{
		{112, true, false}, // ERROR_DISK_FULL
		{39, true, false},  // ERROR_HANDLE_DISK_FULL
		{32, false, true},  // ERROR_SHARING_VIOLATION
		{33, false, true},  // ERROR_LOCK_VIOLATION
		{5, false, false},  // ERROR_ACCESS_DENIED
		{2, false, false},  // ERROR_FILE_NOT_FOUND
		{183, false, false},
	}
	for _, tt := range tests {
		wrapped := []error{
			tt.errno,
			&os.PathError{Op: "write", Path: "db.kv", Err: tt.errno},
			fmt.Errorf("commit: %w", &os.PathError{Op: "write", Path: "db.kv", Err: tt.errno}),
		}
		for _, err := range wrapped {
			if got := IsDiskFull(err); got != tt.diskFull {
				t.Errorf("IsDiskFull(%v) = %v", err, got)
			}
			if got := IsSharingViolation(err); got != tt.shared {
				t.Errorf("IsSharingViolation(%v) = %v", err, got)
			}
		}
	}
	for _, err := range []error{nil, errors.New("plain"), fs.ErrExist} {
		if IsDiskFull(err) || IsSharingViolation(err) {
			t.Errorf("non-errno %v classified", err)
		}
	}
}

// Documents the hazard that forbids os.Rename for R1/R2: on Windows it
// silently replaces an existing destination.
func TestOsRenameReplacesOnWindows(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	writeFile(t, src, "src")
	writeFile(t, dst, "dst")
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("os.Rename: %v", err)
	}
	if readFile(t, dst) != "src" {
		t.Fatal("expected os.Rename to overwrite; if this changed, revisit docs/DESIGN.md §11")
	}
}

// openExclusive opens path with share mode 0, the mechanism specified for
// the process lock file (docs/DESIGN.md §16.1). The handle is not inheritable
// (nil security attributes).
func openExclusive(t *testing.T, path string) (syscall.Handle, error) {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	return syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
}

// Verifies the lock-file mechanism: a second open of a share-mode-0 handle
// fails with a sharing violation, even in the same process, and succeeds
// again once the first handle is closed. A leftover lock file is harmless.
func TestExclusiveShareModeLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "db.kv.lock")
	h1, err := openExclusive(t, lock)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if h2, err := openExclusive(t, lock); err == nil {
		syscall.CloseHandle(h2)
		t.Fatal("second exclusive open succeeded")
	} else if !IsSharingViolation(err) {
		t.Fatalf("second open err = %v, want sharing violation", err)
	}
	// Ordinary Go opens are refused too.
	if f, err := os.OpenFile(lock, os.O_RDWR, 0); err == nil {
		f.Close()
		t.Fatal("os.OpenFile succeeded on an exclusively held lock file")
	} else if !IsSharingViolation(err) {
		t.Fatalf("os.OpenFile err = %v, want sharing violation", err)
	}
	if err := syscall.CloseHandle(h1); err != nil {
		t.Fatal(err)
	}
	// The file still exists (stale) and can be locked again.
	h3, err := openExclusive(t, lock)
	if err != nil {
		t.Fatalf("reopen after release: %v", err)
	}
	syscall.CloseHandle(h3)
}

// Go's os package opens files without FILE_SHARE_DELETE, so a file that is
// still open in this process cannot be moved. This is why compaction closes
// the old data-file handle BEFORE R1 (docs/DESIGN.md §10.2 step 10), and why
// a scanner holding the file open makes R1 fail with a sharing violation that
// must be retried, never ignored.
func TestOpenFileBlocksMove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.kv")
	writeFile(t, path, "data")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	err = MoveNoReplace(path, path+".old")
	f.Close()
	if err == nil {
		t.Fatal("moved a file that is open without FILE_SHARE_DELETE")
	}
	if !IsSharingViolation(err) {
		t.Fatalf("err = %v, want sharing violation", err)
	}
	if readFile(t, path) != "data" {
		t.Fatal("source changed")
	}
	if err := MoveNoReplace(path, path+".old"); err != nil {
		t.Fatalf("move after close: %v", err)
	}
}

// MoveNoReplace must pass MOVEFILE_WRITE_THROUGH and must never pass
// MOVEFILE_REPLACE_EXISTING (the no-replace property R1/R2 rely on).
func TestMoveFlagsWriteThroughNoReplace(t *testing.T) {
	if moveFlags&movefileWriteThrough == 0 {
		t.Fatalf("moveFlags %#x lacks MOVEFILE_WRITE_THROUGH", moveFlags)
	}
	if moveFlags&movefileReplaceExisting != 0 {
		t.Fatalf("moveFlags %#x includes MOVEFILE_REPLACE_EXISTING", moveFlags)
	}
	if moveFlags != movefileWriteThrough {
		t.Fatalf("moveFlags = %#x, want exactly MOVEFILE_WRITE_THROUGH (0x8)", moveFlags)
	}
}

// An existing destination is never overwritten, whatever it is: an empty
// file, a large file, a file another process holds open, or a directory.
// Neither the source nor the destination changes.
func TestMoveNoReplaceNeverOverwritesWindows(t *testing.T) {
	big := string(make([]byte, 3<<20))
	cases := []struct {
		name  string
		setup func(t *testing.T, dst string) func()
	}{
		{"empty file", func(t *testing.T, dst string) func() { writeFile(t, dst, ""); return func() {} }},
		{"3 MiB file", func(t *testing.T, dst string) func() { writeFile(t, dst, big); return func() {} }},
		{"open file", func(t *testing.T, dst string) func() {
			writeFile(t, dst, "held")
			f, err := os.Open(dst)
			if err != nil {
				t.Fatal(err)
			}
			return func() { f.Close() }
		}},
		{"read-only file", func(t *testing.T, dst string) func() {
			writeFile(t, dst, "ro")
			if err := os.Chmod(dst, 0o444); err != nil {
				t.Fatal(err)
			}
			return func() { os.Chmod(dst, 0o644) }
		}},
		{"directory", func(t *testing.T, dst string) func() {
			if err := os.Mkdir(dst, 0o755); err != nil {
				t.Fatal(err)
			}
			return func() {}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "db.kv.compact"), filepath.Join(dir, "db.kv")
			writeFile(t, src, "compacted")
			undo := c.setup(t, dst)
			before, _ := os.Stat(dst)
			var wantDst []byte
			if !before.IsDir() {
				wantDst, _ = os.ReadFile(dst)
			}
			err := MoveNoReplace(src, dst)
			undo()
			if err == nil {
				t.Fatal("MoveNoReplace replaced an existing destination")
			}
			if readFile(t, src) != "compacted" {
				t.Fatal("source changed")
			}
			after, serr := os.Stat(dst)
			if serr != nil || after.IsDir() != before.IsDir() {
				t.Fatalf("destination type changed: %v", serr)
			}
			if !before.IsDir() {
				if got, _ := os.ReadFile(dst); string(got) != string(wantDst) {
					t.Fatal("destination contents changed")
				}
			}
		})
	}
}

// The move is reported as fs.ErrExist (not a generic failure) so callers can
// tell "destination present" from an I/O error.
func TestMoveNoReplaceExistErrorClass(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	writeFile(t, src, "a")
	writeFile(t, dst, "b")
	err := MoveNoReplace(src, dst)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	var le *os.LinkError
	if !errors.As(err, &le) || le.Op != "movefileex" {
		t.Fatalf("err = %#v, want *os.LinkError from movefileex", err)
	}
}
