package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// LockFile is exclusive (also within one process), classifies the busy case
// with IsLockBusy, releases on Close and leaves the file in place.
func TestLockFileExclusiveAndReleasable(t *testing.T) {
	name := filepath.Join(t.TempDir(), "db.kv.lock")
	l1, err := LockFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if l2, err := LockFile(name); err == nil {
		l2.Close()
		t.Fatal("second LockFile succeeded")
	} else if !IsLockBusy(err) {
		t.Fatalf("second LockFile err = %v, want IsLockBusy", err)
	}
	if err := l1.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("lock file removed: %v", err)
	}
	l3, err := LockFile(name)
	if err != nil {
		t.Fatalf("LockFile after release: %v", err)
	}
	l3.Close()
}

func TestIsLockBusyRejectsOtherErrors(t *testing.T) {
	_, err := LockFile(filepath.Join(t.TempDir(), "missing-dir", "x.lock"))
	if err == nil {
		t.Fatal("LockFile in a missing directory succeeded")
	}
	if IsLockBusy(err) {
		t.Fatalf("missing directory classified as busy: %v", err)
	}
	if IsLockBusy(nil) {
		t.Fatal("nil classified as busy")
	}
}
