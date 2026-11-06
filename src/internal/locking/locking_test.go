package locking_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio/faultfs"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/locking"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
)

// fastPolicy retries without real sleeping and records the sleeps.
func fastPolicy(budget time.Duration, onSleep func(n int)) (fileio.RetryPolicy, *int) {
	n := new(int)
	return fileio.RetryPolicy{
		Initial: 10 * time.Millisecond, Max: 50 * time.Millisecond, Budget: budget,
		Sleep: func(time.Duration) {
			*n++
			if onSleep != nil {
				onSleep(*n)
			}
		},
	}, n
}

func TestAcquireReleaseKeepsLockFile(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.kv")
	l, err := locking.Acquire(fileio.OS{}, db, fileio.NoRetry)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Check(db); err != nil {
		t.Fatalf("Check while held: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	// T-LOCK-6: the lock file is never deleted.
	if _, err := os.Stat(locking.Path(db)); err != nil {
		t.Fatalf("lock file gone after Release: %v", err)
	}
	if err := l.Check(db); !errors.Is(err, locking.ErrNotHeld) {
		t.Fatalf("Check after release = %v", err)
	}
	// A stale lock file does not block a new Acquire.
	l2, err := locking.Acquire(fileio.OS{}, db, fileio.NoRetry)
	if err != nil {
		t.Fatalf("re-acquire over stale lock file: %v", err)
	}
	l2.Release()
}

// T-LOCK-3: a second Acquire in the same process is refused with ErrLocked
// after the bounded retries, and succeeds once the first is released.
func TestSecondAcquireInSameProcessIsErrLocked(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.kv")
	l1, err := locking.Acquire(fileio.OS{}, db, fileio.NoRetry)
	if err != nil {
		t.Fatal(err)
	}
	ffs := faultfs.New(fileio.OS{})
	p, sleeps := fastPolicy(100*time.Millisecond, nil)
	_, err = locking.Acquire(ffs, db, p)
	if !errors.Is(err, dberr.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	// 10 + 20 + 40 + 30(clipped) = 100 ms of sleeps -> 5 attempts.
	attempts := 0
	for _, e := range ffs.Events() {
		if e.Op == faultfs.OpLock {
			attempts++
			if !platform.IsLockBusy(e.Err) {
				t.Fatalf("attempt error not classified as busy: %v", e.Err)
			}
		}
	}
	if attempts != 5 || *sleeps != 4 {
		t.Fatalf("attempts=%d sleeps=%d, want 5 and 4", attempts, *sleeps)
	}
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := locking.Acquire(fileio.OS{}, db, fileio.NoRetry)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	l2.Release()
}

// T-LOCK-5: a third-party handle on the lock file that goes away within the
// retry budget does not cause ErrLocked; one that outlasts it does.
func TestBriefThirdPartyHandleIsAbsorbed(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.kv")
	holder, err := platform.LockFile(locking.Path(db)) // e.g. an antivirus scan
	if err != nil {
		t.Fatal(err)
	}
	p, _ := fastPolicy(time.Second, func(n int) {
		if n == 3 {
			holder.Close()
		}
	})
	l, err := locking.Acquire(fileio.OS{}, db, p)
	if err != nil {
		t.Fatalf("brief holder caused: %v", err)
	}
	l.Release()

	holder, err = platform.LockFile(locking.Path(db))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	p, _ = fastPolicy(200*time.Millisecond, nil)
	if _, err := locking.Acquire(fileio.OS{}, db, p); !errors.Is(err, dberr.ErrLocked) {
		t.Fatalf("persistent holder: err = %v, want ErrLocked", err)
	}
}

// Errors other than "busy" are returned at once and are not ErrLocked.
func TestNonBusyErrorIsNotErrLocked(t *testing.T) {
	db := filepath.Join(t.TempDir(), "no-such-dir", "db.kv")
	ffs := faultfs.New(fileio.OS{})
	p, sleeps := fastPolicy(time.Second, nil)
	_, err := locking.Acquire(ffs, db, p)
	if err == nil || errors.Is(err, dberr.ErrLocked) {
		t.Fatalf("err = %v, want a non-ErrLocked error", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want it to wrap fs.ErrNotExist", err)
	}
	if *sleeps != 0 || len(ffs.Events()) != 1 {
		t.Fatalf("non-busy error was retried: sleeps=%d\n%s", *sleeps, ffs.TraceString())
	}
}

// Acquire touches only the lock file, even when temp files are lying around
// (part of T-LOCK-1 / invariant 19; the full Open-level check is in package
// recovery).
func TestAcquireTouchesOnlyTheLockFile(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db.kv")
	for _, s := range []string{"", ".compact", ".old", ".new"} {
		if err := os.WriteFile(db+s, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ffs := faultfs.New(fileio.OS{})
	l, err := locking.Acquire(ffs, db, fileio.NoRetry)
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	for _, e := range ffs.Events() {
		if e.Path != locking.Path(db) {
			t.Fatalf("Acquire touched %s:\n%s", e.Path, ffs.TraceString())
		}
	}
}

func TestCheckRejectsNilReleasedAndForeignLocks(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.kv"), filepath.Join(dir, "b.kv")
	var nilLock *locking.Lock
	if err := nilLock.Check(a); !errors.Is(err, locking.ErrNotHeld) {
		t.Fatalf("nil lock: %v", err)
	}
	l, err := locking.Acquire(fileio.OS{}, a, fileio.NoRetry)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if err := l.Check(b); !errors.Is(err, locking.ErrNotHeld) {
		t.Fatalf("foreign path: %v", err)
	}
}

func TestReleaseErrorStillReleases(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db.kv")
	ffs := faultfs.New(fileio.OS{})
	errX := errors.New("injected close failure")
	ffs.Add(faultfs.Rule{Op: faultfs.OpUnlock, Err: errX, Count: 1})
	l, err := locking.Acquire(ffs, db, fileio.NoRetry)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); !errors.Is(err, errX) {
		t.Fatalf("Release err = %v", err)
	}
	if err := l.Check(db); !errors.Is(err, locking.ErrNotHeld) {
		t.Fatal("lock still considered held after a failed release")
	}
	l2, err := locking.Acquire(fileio.OS{}, db, fileio.NoRetry)
	if err != nil {
		t.Fatalf("OS handle leaked: %v", err)
	}
	l2.Release()
}

// ---- Cross-process tests (T-LOCK-2 lock part, T-LOCK-4) ----

const helperEnv = "KVDB_LOCK_HELPER_DB"

// TestHelperLockProcess is not a real test: when helperEnv is set it runs as
// a child process that takes the lock, prints LOCKED and waits until stdin
// closes or it is killed.
func TestHelperLockProcess(t *testing.T) {
	db := os.Getenv(helperEnv)
	if db == "" {
		t.Skip("helper process only")
	}
	l, err := locking.Acquire(fileio.OS{}, db, fileio.NoRetry)
	if err != nil {
		fmt.Println("ERROR", err)
		os.Exit(3)
	}
	fmt.Println("LOCKED")
	io.Copy(io.Discard, os.Stdin) // block until the parent closes stdin or kills us
	l.Release()
	os.Exit(0)
}

func startLockHolder(t *testing.T, db string) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperLockProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), helperEnv+"="+db)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	line := make(chan string, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			if txt := strings.TrimSpace(s.Text()); txt == "LOCKED" || strings.HasPrefix(txt, "ERROR") {
				line <- txt
				return
			}
		}
		line <- "EOF"
	}()
	select {
	case got := <-line:
		if got != "LOCKED" {
			t.Fatalf("helper did not lock: %s", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("helper process timed out")
	}
	return cmd, stdin
}

// Another process holds the lock: Acquire fails with ErrLocked after retries.
// The holder is then killed with TerminateProcess (Process.Kill on Windows);
// right after Wait, the lock is free (T-LOCK-4).
func TestLockContentionAcrossProcessesAndKill(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a child process")
	}
	db := filepath.Join(t.TempDir(), "db.kv")
	cmd, _ := startLockHolder(t, db)

	p, _ := fastPolicy(200*time.Millisecond, nil)
	if _, err := locking.Acquire(fileio.OS{}, db, p); !errors.Is(err, dberr.ErrLocked) {
		t.Fatalf("while child holds lock: err = %v, want ErrLocked", err)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait() // "Kill() followed by Wait()" (DESIGN §21)

	l, err := locking.Acquire(fileio.OS{}, db, fileio.NoRetry)
	if err != nil {
		t.Fatalf("lock not released by process death: %v", err)
	}
	l.Release()
}

// A child that releases normally (stdin closed) also frees the lock.
func TestLockReleasedByOrderlyChildExit(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a child process")
	}
	db := filepath.Join(t.TempDir(), "db.kv")
	cmd, stdin := startLockHolder(t, db)
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper exit: %v", err)
	}
	l, err := locking.Acquire(fileio.OS{}, db, fileio.NoRetry)
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
}
