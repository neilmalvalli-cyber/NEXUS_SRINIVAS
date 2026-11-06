package fileio_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio/faultfs"
)

func TestOSCreateExclusiveRefusesExisting(t *testing.T) {
	name := filepath.Join(t.TempDir(), "db.kv.new")
	var fsys fileio.OS
	f, err := fsys.CreateExclusive(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileio.WriteFullAt(f, []byte("keep me"), 0); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.CreateExclusive(name); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second CreateExclusive err = %v, want fs.ErrExist", err)
	}
	// The existing file must not have been truncated.
	g, err := fsys.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	buf := make([]byte, 7)
	if err := fileio.ReadFullAt(g, buf, 0); err != nil || string(buf) != "keep me" {
		t.Fatalf("content after refused create: %q, %v", buf, err)
	}
}

func TestOSFileOperations(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "f")
	var fsys fileio.OS

	f, err := fsys.CreateExclusive(name)
	if err != nil {
		t.Fatal(err)
	}
	if f.Name() != name {
		t.Fatalf("Name = %q", f.Name())
	}
	if _, err := fileio.WriteFullAt(f, []byte("hello world"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := fileio.WriteFullAt(f, []byte("W"), 6); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if sz, err := f.Size(); err != nil || sz != 11 {
		t.Fatalf("Size = %d, %v", sz, err)
	}
	buf := make([]byte, 11)
	if err := fileio.ReadFullAt(f, buf, 0); err != nil || string(buf) != "hello World" {
		t.Fatalf("read %q, %v", buf, err)
	}
	if err := f.Truncate(5); err != nil {
		t.Fatal(err)
	}
	if sz, _ := f.Size(); sz != 5 {
		t.Fatalf("size after truncate = %d", sz)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err == nil {
		t.Fatal("second Close returned nil")
	}

	if fi, err := fsys.Stat(name); err != nil || fi.Size() != 5 {
		t.Fatalf("Stat = %v, %v", fi, err)
	}
	if _, err := fsys.Stat(filepath.Join(dir, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat absent err = %v", err)
	}
	if _, err := fsys.OpenRW(filepath.Join(dir, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("OpenRW must not create: %v", err)
	}
	if err := fsys.MoveNoReplace(name, name+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Remove(name + ".moved"); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Remove(name + ".moved"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Remove absent err = %v", err)
	}
	if err := fsys.SyncDir(dir); err != nil {
		t.Fatalf("SyncDir: %v", err)
	}
}

func TestReadFullAtDistinguishesEOFFromIOError(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "f")
	ffs := faultfs.New(fileio.OS{})
	f, err := ffs.CreateExclusive(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fileio.WriteFullAt(f, []byte("0123456789"), 0); err != nil {
		t.Fatal(err)
	}

	// Full read, including one ending exactly at EOF.
	buf := make([]byte, 10)
	if err := fileio.ReadFullAt(f, buf, 0); err != nil {
		t.Fatalf("full read: %v", err)
	}
	// Past the end: short read, reported as ErrShortRead.
	if err := fileio.ReadFullAt(f, make([]byte, 4), 8); !errors.Is(err, fileio.ErrShortRead) {
		t.Fatalf("short read err = %v", err)
	}
	// A real read error is NOT ErrShortRead, even when some bytes arrived.
	errIO := errors.New("injected: device not ready")
	ffs.Add(faultfs.Rule{Op: faultfs.OpRead, Mode: faultfs.Partial, Keep: 3, Err: errIO, Count: 1})
	err = fileio.ReadFullAt(f, make([]byte, 10), 0)
	if !errors.Is(err, errIO) || errors.Is(err, fileio.ErrShortRead) {
		t.Fatalf("I/O error mapped wrongly: %v", err)
	}
	// An io.EOF from the device after a partial read is a short read.
	ffs.Add(faultfs.Rule{Op: faultfs.OpRead, Mode: faultfs.Partial, Keep: 3, Err: io.EOF, Count: 1})
	if err := fileio.ReadFullAt(f, make([]byte, 10), 0); !errors.Is(err, fileio.ErrShortRead) {
		t.Fatalf("partial+EOF err = %v", err)
	}
}

func TestWriteFullAtDetectsShortWrites(t *testing.T) {
	name := filepath.Join(t.TempDir(), "f")
	ffs := faultfs.New(fileio.OS{})
	f, err := ffs.CreateExclusive(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Contract-breaking writer: 4 bytes, no error.
	ffs.Add(faultfs.Rule{Op: faultfs.OpWrite, Mode: faultfs.Partial, Keep: 4, Count: 1})
	n, err := fileio.WriteFullAt(f, []byte("abcdefgh"), 0)
	if n != 4 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("n=%d err=%v, want 4 + ErrShortWrite", n, err)
	}
	// Disk-full-style partial write: 2 bytes then an error.
	errFull := errors.New("injected: disk full")
	ffs.Add(faultfs.Rule{Op: faultfs.OpWrite, Mode: faultfs.Partial, Keep: 2, Err: errFull, Count: 1})
	n, err = fileio.WriteFullAt(f, []byte("XYZW"), 4)
	if n != 2 || !errors.Is(err, errFull) {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// The bytes that "reached the disk" are really there: the caller must
	// truncate them away (DESIGN §6.3).
	if sz, _ := f.Size(); sz != 6 {
		t.Fatalf("file size = %d, want 6 (4 + 2 partial bytes)", sz)
	}
	buf := make([]byte, 6)
	if err := fileio.ReadFullAt(f, buf, 0); err != nil || string(buf) != "abcdXY" {
		t.Fatalf("content %q, %v", buf, err)
	}
}

func TestRetry(t *testing.T) {
	errBusy := errors.New("busy")
	errFatal := errors.New("fatal")
	isBusy := func(err error) bool { return errors.Is(err, errBusy) }
	var slept []time.Duration
	p := fileio.RetryPolicy{Initial: 10 * time.Millisecond, Max: 40 * time.Millisecond,
		Budget: 100 * time.Millisecond, Sleep: func(d time.Duration) { slept = append(slept, d) }}

	// Succeeds on the third attempt.
	calls := 0
	n, err := fileio.Retry(p, isBusy, func() error {
		calls++
		if calls < 3 {
			return errBusy
		}
		return nil
	})
	if err != nil || n != 3 || len(slept) != 2 || slept[0] != 10*time.Millisecond || slept[1] != 20*time.Millisecond {
		t.Fatalf("n=%d err=%v slept=%v", n, err, slept)
	}

	// Non-retryable error: one attempt, no sleep.
	slept = nil
	n, err = fileio.Retry(p, isBusy, func() error { return errFatal })
	if n != 1 || !errors.Is(err, errFatal) || len(slept) != 0 {
		t.Fatalf("fatal: n=%d err=%v slept=%v", n, err, slept)
	}

	// Always busy: stops when the budget is spent, total sleep == budget.
	slept = nil
	n, err = fileio.Retry(p, isBusy, func() error { return errBusy })
	var total time.Duration
	for _, d := range slept {
		total += d
	}
	// 10 + 20 + 40 + 30 (clipped) = 100
	if !errors.Is(err, errBusy) || total != 100*time.Millisecond || n != 5 {
		t.Fatalf("exhausted: n=%d total=%v slept=%v", n, total, slept)
	}

	// NoRetry: exactly one attempt.
	n, _ = fileio.Retry(fileio.NoRetry, isBusy, func() error { return errBusy })
	if n != 1 {
		t.Fatalf("NoRetry attempts = %d", n)
	}
}

func TestDefaultRetrySchedule(t *testing.T) {
	var slept []time.Duration
	p := fileio.DefaultRetry
	p.Sleep = func(d time.Duration) { slept = append(slept, d) }
	n, _ := fileio.Retry(p, func(error) bool { return true }, func() error { return errors.New("busy") })
	var total time.Duration
	for _, d := range slept {
		total += d
	}
	if n != 15 || len(slept) != 14 || total != time.Second || slept[len(slept)-1] != 45*time.Millisecond {
		t.Fatalf("attempts=%d sleeps=%v total=%v", n, slept, total)
	}
}

func TestFaultFSModesAndTrace(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	ffs := faultfs.New(fileio.OS{})
	errX := errors.New("injected")

	// FailBefore on create: nothing is created.
	ffs.Add(faultfs.Rule{Op: faultfs.OpCreate, Path: a, Err: errX, Count: 1})
	if _, err := ffs.CreateExclusive(a); !errors.Is(err, errX) {
		t.Fatalf("create err = %v", err)
	}
	if _, err := (fileio.OS{}).Stat(a); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("FailBefore create left a file behind")
	}

	// FailAfter on create: the file exists although an error was returned.
	ffs.Add(faultfs.Rule{Op: faultfs.OpCreate, Path: a, Mode: faultfs.FailAfter, Err: errX, Count: 1})
	if _, err := ffs.CreateExclusive(a); !errors.Is(err, errX) {
		t.Fatalf("create err = %v", err)
	}
	if _, err := (fileio.OS{}).Stat(a); err != nil {
		t.Fatal("FailAfter create did not create the file")
	}

	// Skip + Count: the second move fails, the first and third succeed.
	ffs.Add(faultfs.Rule{Op: faultfs.OpMove, Skip: 1, Count: 1, Err: errX})
	if err := ffs.MoveNoReplace(a, b); err != nil {
		t.Fatalf("move 1: %v", err)
	}
	if err := ffs.MoveNoReplace(b, a); !errors.Is(err, errX) {
		t.Fatalf("move 2 err = %v", err)
	}
	if _, err := (fileio.OS{}).Stat(b); err != nil {
		t.Fatal("FailBefore move must not move")
	}
	if err := ffs.MoveNoReplace(b, a); err != nil {
		t.Fatalf("move 3: %v", err)
	}

	// Path filter: a rule for another path does not fire.
	ffs.Add(faultfs.Rule{Op: faultfs.OpRemove, Path: b, Err: errX})
	if err := ffs.Remove(a); err != nil {
		t.Fatalf("remove a: %v", err)
	}

	want := []string{"create a !", "create a !", "move a -> b", "move b -> a !", "move b -> a", "remove a"}
	got := ffs.Trace()
	if len(got) != len(want) {
		t.Fatalf("trace:\n%s", ffs.TraceString())
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trace[%d] = %q, want %q\n%s", i, got[i], want[i], ffs.TraceString())
		}
	}
	ev := ffs.Events()
	if !ev[0].Faulty || ev[2].Faulty {
		t.Fatal("Faulty flag wrong")
	}
}

func TestFaultFSSyncTruncateCloseFaults(t *testing.T) {
	name := filepath.Join(t.TempDir(), "f")
	ffs := faultfs.New(fileio.OS{})
	errX := errors.New("injected")
	f, err := ffs.CreateExclusive(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileio.WriteFullAt(f, bytes.Repeat([]byte{1}, 100), 0); err != nil {
		t.Fatal(err)
	}
	ffs.Add(faultfs.Rule{Op: faultfs.OpSync, Err: errX, Count: 1})
	if err := f.Sync(); !errors.Is(err, errX) {
		t.Fatalf("sync err = %v", err)
	}
	ffs.Add(faultfs.Rule{Op: faultfs.OpTruncate, Err: errX, Count: 1})
	if err := f.Truncate(10); !errors.Is(err, errX) {
		t.Fatalf("truncate err = %v", err)
	}
	if sz, _ := f.Size(); sz != 100 {
		t.Fatalf("FailBefore truncate changed size to %d", sz)
	}
	ffs.Add(faultfs.Rule{Op: faultfs.OpSize, Err: errX, Count: 1})
	if _, err := f.Size(); !errors.Is(err, errX) {
		t.Fatalf("size err = %v", err)
	}
	ffs.Add(faultfs.Rule{Op: faultfs.OpClose, Err: errX, Count: 1})
	if err := f.Close(); !errors.Is(err, errX) {
		t.Fatalf("close err = %v", err)
	}
	// The real handle was still released: the file can be removed (Windows
	// refuses to delete files with open handles).
	if err := (fileio.OS{}).Remove(name); err != nil {
		t.Fatalf("handle leaked after faulty close: %v", err)
	}
}
