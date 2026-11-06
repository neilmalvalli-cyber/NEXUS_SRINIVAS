package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio/faultfs"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/index"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
)

var errX = errors.New("injected fault")

var fastOpts = recovery.Options{
	LockRetry: fileio.NoRetry,
	IORetry:   fileio.RetryPolicy{Initial: time.Millisecond, Max: time.Millisecond, Budget: 5 * time.Millisecond, Sleep: func(time.Duration) {}},
}

func put(k, v string) format.Entry {
	return format.Entry{Op: format.OpPut, Key: []byte(k), Value: []byte(v)}
}
func del(k string) format.Entry { return format.Entry{Op: format.OpDelete, Key: []byte(k)} }

func open(t *testing.T, fsys fileio.FS, db string) *Log {
	t.Helper()
	l, err := Open(fsys, db, fastOpts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l
}

func newDB(t *testing.T) string { return filepath.Join(t.TempDir(), "db.kv") }

func mustCommit(t *testing.T, l *Log, es ...format.Entry) {
	t.Helper()
	if err := l.Commit(es); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// contents reads every key through Get.
func contents(t *testing.T, l *Log) map[string]string {
	t.Helper()
	var keys []string
	l.mu.RLock()
	l.idx.Range(func(k string, _ index.Loc) bool { keys = append(keys, k); return true })
	l.mu.RUnlock()
	out := map[string]string{}
	for _, k := range keys {
		v, err := l.Get([]byte(k))
		if err != nil {
			t.Fatalf("Get(%q): %v", k, err)
		}
		out[k] = string(v)
	}
	return out
}

// reopen closes nothing; it opens db with the real FS and returns contents.
func reopen(t *testing.T, db string) map[string]string {
	t.Helper()
	l := open(t, fileio.OS{}, db)
	defer l.Close()
	return contents(t, l)
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func encodeFrame(t *testing.T, seq uint64, es ...format.Entry) []byte {
	t.Helper()
	var b bytes.Buffer
	if _, err := format.WriteFrame(&b, seq, es, format.MaxBatchOps); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// dataEvents returns the events on the data file of the given ops.
func dataEvents(ffs *faultfs.FS, db string, ops ...faultfs.Op) []faultfs.Event {
	var out []faultfs.Event
	for _, e := range ffs.Events() {
		if e.Path != db {
			continue
		}
		for _, op := range ops {
			if e.Op == op {
				out = append(out, e)
			}
		}
	}
	return out
}

var writeOps = []faultfs.Op{faultfs.OpWrite, faultfs.OpSync, faultfs.OpTruncate}

// ---- basic behaviour ----

func TestCommitGetDeleteAndReopen(t *testing.T) {
	db := newDB(t)
	l := open(t, fileio.OS{}, db)
	mustCommit(t, l, put("a", "1"))
	mustCommit(t, l, put("b", "2"), put("a", "11"), del("b"), put("c", ""), put("b", "22"))
	if err := l.Delete([]byte("c")); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "11", "b": "22"}
	if got := contents(t, l); !reflect.DeepEqual(got, want) {
		t.Fatalf("contents = %v", got)
	}
	if _, err := l.Get([]byte("c")); !errors.Is(err, dberr.ErrNotFound) {
		t.Fatalf("Get(c) = %v", err)
	}
	st := l.Stats()
	if st.Keys != 2 || st.NextSeq != 4 || st.Size != int64(len(readFile(t, db))) {
		t.Fatalf("stats %+v", st)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if got := reopen(t, db); !reflect.DeepEqual(got, want) {
		t.Fatalf("after reopen: %v", got)
	}
}

func TestGetReturnsIndependentCopyAndEmptyValue(t *testing.T) {
	l := open(t, fileio.OS{}, newDB(t))
	defer l.Close()
	mustCommit(t, l, put("k", "value"), put("e", ""))
	v, _ := l.Get([]byte("k"))
	v[0] = 'X'
	if v2, _ := l.Get([]byte("k")); string(v2) != "value" {
		t.Fatalf("Get returned a shared buffer: %q", v2)
	}
	if e, err := l.Get([]byte("e")); err != nil || e == nil || len(e) != 0 {
		t.Fatalf("empty value: %v %v", e, err)
	}
}

// A single Delete of a missing key and an empty batch do no I/O.
func TestNoIOForNoOps(t *testing.T) {
	db := newDB(t)
	ffs := faultfs.New(fileio.OS{})
	l := open(t, ffs, db)
	defer l.Close()
	n := ffs.OpCount()
	if err := l.Delete([]byte("missing")); err != nil {
		t.Fatal(err)
	}
	if err := l.Commit(nil); err != nil {
		t.Fatal(err)
	}
	if ffs.OpCount() != n {
		t.Fatalf("no-op did I/O:\n%s", ffs.TraceString())
	}
}

// Validation failures are reported before any I/O.
func TestValidationBeforeIO(t *testing.T) {
	db := newDB(t)
	ffs := faultfs.New(fileio.OS{})
	l := open(t, ffs, db)
	defer l.Close()
	n := ffs.OpCount()
	big := make([]byte, format.MaxValueLen+1)
	cases := []struct {
		es   []format.Entry
		want error
	}{
		{[]format.Entry{put("", "v")}, dberr.ErrInvalidArgument},
		{[]format.Entry{put(strings.Repeat("k", 1025), "v")}, dberr.ErrInvalidArgument},
		{[]format.Entry{{Op: format.OpPut, Key: []byte("k"), Value: big}}, dberr.ErrTooLarge},
		{[]format.Entry{{Op: format.OpDelete, Key: []byte("k"), Value: []byte("x")}}, dberr.ErrInvalidArgument},
		{[]format.Entry{{Op: 9, Key: []byte("k")}}, dberr.ErrInvalidArgument},
		{make([]format.Entry, format.MaxBatchOps+1), dberr.ErrTooLarge},
	}
	for i, c := range cases {
		if err := l.Commit(c.es); !errors.Is(err, c.want) {
			t.Fatalf("case %d: err = %v, want %v", i, err, c.want)
		}
	}
	if err := l.Delete(nil); !errors.Is(err, dberr.ErrInvalidArgument) {
		t.Fatalf("Delete(nil) = %v", err)
	}
	if ffs.OpCount() != n {
		t.Fatalf("validation did I/O:\n%s", ffs.TraceString())
	}
}

// One commit: writes in file order at the old end, then exactly one Sync,
// and nothing else touches the data file.
func TestCommitWritesThenSyncs(t *testing.T) {
	db := newDB(t)
	ffs := faultfs.New(fileio.OS{})
	l := open(t, ffs, db)
	defer l.Close()
	start := l.Stats().Size
	before := len(ffs.Events())
	mustCommit(t, l, put("small", "v"), put("big", strings.Repeat("x", 300<<10)), put("tail", "t"))
	var ops []string
	off := start
	for _, e := range ffs.Events()[before:] {
		ops = append(ops, string(e.Op))
		if e.Op == faultfs.OpWrite {
			if e.Off != off {
				t.Fatalf("write at %d, expected %d (not sequential)", e.Off, off)
			}
			off += int64(e.N)
		}
	}
	if ops[len(ops)-1] != "sync" || strings.Count(strings.Join(ops, " "), "sync") != 1 || len(ops) < 3 {
		t.Fatalf("op sequence %v", ops)
	}
	if off != l.Stats().Size || off != int64(len(readFile(t, db))) {
		t.Fatalf("end %d, size %d", off, l.Stats().Size)
	}
}

// ---- I1 and the failure table (DESIGN §6.3) ----

// T-I1-1: an out-of-space write failure mid-frame is rolled back (truncate +
// Sync); ErrFull is definite, the handle stays OPEN, and the next commit
// writes at the old end: file = previous bytes + new frame, exactly.
func TestI1DiskFullRollbackKeepsHandleOpen(t *testing.T) {
	big := strings.Repeat("y", 300<<10) // forces several WriteAt calls
	for _, skip := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("write#%d", skip), func(t *testing.T) {
			db := newDB(t)
			ffs := faultfs.New(fileio.OS{})
			l := open(t, ffs, db)
			defer l.Close()
			mustCommit(t, l, put("a", "1"))
			before := readFile(t, db)
			stats := l.Stats()
			ffs.Add(faultfs.Rule{Op: faultfs.OpWrite, Path: db, Skip: skip, Count: 1, Mode: faultfs.Partial, Keep: 5, Err: platform.DiskFullError()})
			err := l.Commit([]format.Entry{put("b", big), put("c", "3")})
			if !errors.Is(err, dberr.ErrFull) || errors.Is(err, dberr.ErrOutcomeUnknown) {
				t.Fatalf("err = %v, want ErrFull", err)
			}
			if !bytes.Equal(readFile(t, db), before) || l.Stats() != stats {
				t.Fatal("rollback did not restore the file or the counters")
			}
			if _, err := l.Get([]byte("b")); !errors.Is(err, dberr.ErrNotFound) {
				t.Fatalf("index changed: %v", err)
			}
			mustCommit(t, l, put("d", "4"))
			want := append(append([]byte(nil), before...), encodeFrame(t, stats.NextSeq, put("d", "4"))...)
			if !bytes.Equal(readFile(t, db), want) {
				t.Fatal("next commit did not land exactly at the old end")
			}
		})
	}
}

// assertFailed checks that every operation now returns ErrFailed with no
// I/O, that Close works, and that a reopen shows exactly the old contents
// (or the old contents plus the whole failed batch).
func assertFailed(t *testing.T, l *Log, ffs *faultfs.FS, db string, old, withBatch map[string]string) {
	t.Helper()
	n := ffs.OpCount()
	checks := map[string]error{
		"put":    l.Commit([]format.Entry{put("z", "z")}),
		"delete": l.Delete([]byte("a")),
		"empty":  l.Commit(nil),
	}
	_, checks["get"] = l.Get([]byte("a"))
	for name, err := range checks {
		if !errors.Is(err, dberr.ErrFailed) {
			t.Fatalf("%s in FAILED state: %v", name, err)
		}
	}
	if ffs.OpCount() != n {
		t.Fatalf("FAILED handle did I/O:\n%s", ffs.TraceString())
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close of FAILED handle: %v", err)
	}
	got := reopen(t, db)
	if !reflect.DeepEqual(got, old) && !reflect.DeepEqual(got, withBatch) {
		t.Fatalf("after reopen: %v (want %v or %v)", got, old, withBatch)
	}
}

// T-I1-2 and the remaining WriteAt rows: any other write failure, or a
// rollback that fails, gives ErrOutcomeUnknown and a FAILED handle.
func TestI1WriteFailuresFailTheHandle(t *testing.T) {
	big := strings.Repeat("y", 300<<10)
	old := map[string]string{"a": "1"}
	withBatch := map[string]string{"a": "1", "b": big, "c": "3"}
	cases := []struct {
		name  string
		rules []faultfs.Rule
	}{
		{"io-error", []faultfs.Rule{{Op: faultfs.OpWrite, Skip: 1, Count: 1, Err: errX}}},
		{"short-write-nil-error", []faultfs.Rule{{Op: faultfs.OpWrite, Skip: 1, Count: 1, Mode: faultfs.Partial, Keep: 3}}},
		{"io-error+truncate-fails", []faultfs.Rule{{Op: faultfs.OpWrite, Count: 1, Err: errX}, {Op: faultfs.OpTruncate, Err: errX}}},
		{"disk-full+truncate-fails", []faultfs.Rule{{Op: faultfs.OpWrite, Count: 1, Err: platform.DiskFullError()}, {Op: faultfs.OpTruncate, Err: errX}}},
		{"disk-full+rollback-sync-fails", []faultfs.Rule{{Op: faultfs.OpWrite, Count: 1, Err: platform.DiskFullError()}, {Op: faultfs.OpSync, Err: errX}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := newDB(t)
			ffs := faultfs.New(fileio.OS{})
			l := open(t, ffs, db)
			mustCommit(t, l, put("a", "1"))
			for _, r := range c.rules {
				r.Path = db
				ffs.Add(r)
			}
			err := l.Commit([]format.Entry{put("b", big), put("c", "3")})
			if !errors.Is(err, dberr.ErrOutcomeUnknown) || errors.Is(err, dberr.ErrFull) {
				t.Fatalf("err = %v, want ErrOutcomeUnknown", err)
			}
			ffs.ClearRules()
			assertFailed(t, l, ffs, db, old, withBatch)
		})
	}
}

// T-I1-3: a failed Sync is never retried: ErrOutcomeUnknown, FAILED, a
// best-effort truncate of the frame, and no further write or Sync.
func TestI1SyncFailureIsNotRetried(t *testing.T) {
	for _, mode := range []faultfs.Mode{faultfs.FailBefore, faultfs.FailAfter} {
		t.Run(fmt.Sprint("mode", mode), func(t *testing.T) {
			db := newDB(t)
			ffs := faultfs.New(fileio.OS{})
			l := open(t, ffs, db)
			mustCommit(t, l, put("a", "1"))
			before := len(ffs.Events())
			ffs.Add(faultfs.Rule{Op: faultfs.OpSync, Path: db, Count: 1, Mode: mode, Err: errX})
			err := l.Commit([]format.Entry{put("b", "2")})
			if !errors.Is(err, dberr.ErrOutcomeUnknown) {
				t.Fatalf("err = %v", err)
			}
			var ops []string
			for _, e := range ffs.Events()[before:] {
				if e.Path == db {
					ops = append(ops, string(e.Op))
				}
			}
			if strings.Join(ops, " ") != "write sync truncate" {
				t.Fatalf("data-file ops after the failure: %v", ops)
			}
			assertFailed(t, l, ffs, db, map[string]string{"a": "1"}, map[string]string{"a": "1", "b": "2"})
		})
	}
}

// F1 invariants (DESIGN §6.3, approved 2026-11-06), one sub-test per way a
// Sync can fail combined with the best-effort rollback succeeding or not:
//
//  1. ErrOutcomeUnknown, never nil and never a definite error;
//  2. FAILED: every operation but Close is rejected with no I/O;
//  3. no automatic retry: exactly "write sync truncate" on the data file;
//  4. after reopen the batch is all or nothing (both outcomes reached);
//  5. a failed truncate does not change the classification.
func TestF1SyncFailureInvariants(t *testing.T) {
	old := map[string]string{"a": "1"}
	withBatch := map[string]string{"a": "1", "b": "2", "c": "3"}
	sawOld, sawNew := false, false
	for _, syncMode := range []faultfs.Mode{faultfs.FailBefore, faultfs.FailAfter} {
		for _, truncFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("sync-mode%d/truncate-fails=%v", syncMode, truncFails), func(t *testing.T) {
				db := newDB(t)
				ffs := faultfs.New(fileio.OS{})
				l := open(t, ffs, db)
				mustCommit(t, l, put("a", "1"))
				before := len(ffs.Events())
				ffs.Add(faultfs.Rule{Op: faultfs.OpSync, Path: db, Count: 1, Mode: syncMode, Err: errX})
				if truncFails {
					ffs.Add(faultfs.Rule{Op: faultfs.OpTruncate, Path: db, Err: errX})
				}
				err := l.Commit([]format.Entry{put("b", "2"), put("c", "3")})
				// (1) and (5)
				if err == nil || !errors.Is(err, dberr.ErrOutcomeUnknown) {
					t.Fatalf("err = %v, want ErrOutcomeUnknown", err)
				}
				for _, definite := range []error{dberr.ErrFull, dberr.ErrInvalidArgument, dberr.ErrTooLarge, dberr.ErrClosed, dberr.ErrFailed} {
					if errors.Is(err, definite) {
						t.Fatalf("unknown outcome reported as definite %v", definite)
					}
				}
				// (3)
				var ops []string
				for _, e := range ffs.Events()[before:] {
					if e.Path == db {
						ops = append(ops, string(e.Op))
					}
				}
				if strings.Join(ops, " ") != "write sync truncate" {
					t.Fatalf("data-file ops = %v, want exactly write sync truncate", ops)
				}
				ffs.ClearRules()
				// (2): every operation, including Compact, is rejected with no I/O.
				n := ffs.OpCount()
				if err := l.Compact(); !errors.Is(err, dberr.ErrFailed) {
					t.Fatalf("Compact in FAILED: %v", err)
				}
				if ffs.OpCount() != n {
					t.Fatal("Compact in FAILED did I/O")
				}
				// (2) for the rest, Close, and (4) via reopen.
				assertFailed(t, l, ffs, db, old, withBatch)
				if got := reopen(t, db); reflect.DeepEqual(got, old) {
					sawOld = true
				} else {
					sawNew = true
				}
			})
		}
	}
	if !sawOld || !sawNew {
		t.Fatalf("(4) both outcomes must be reachable: old=%v new=%v", sawOld, sawNew)
	}
}

// T-I1-4: with concurrent writers, a frame's writes are never interleaved
// with another frame's, and each frame is synced before the next starts.
func TestI1ConcurrentWritersDoNotInterleave(t *testing.T) {
	db := newDB(t)
	ffs := faultfs.New(fileio.OS{})
	l := open(t, ffs, db)
	start := l.Stats().Size
	const writers, each = 4, 40
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				v := strings.Repeat(fmt.Sprint(w), 1+i*997%(300<<10)) // some frames use several writes
				if err := l.Commit([]format.Entry{put(fmt.Sprintf("w%d-%d", w, i), v), put(fmt.Sprintf("last-%d", w), fmt.Sprint(i))}); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	frames, off, open := 0, start, false
	for _, e := range dataEvents(ffs, db, writeOps...) {
		switch e.Op {
		case faultfs.OpWrite:
			if e.Off != off {
				t.Fatalf("write at %d, expected %d: frames interleaved", e.Off, off)
			}
			off += int64(e.N)
			open = true
		case faultfs.OpSync:
			if !open {
				t.Fatal("sync without a frame")
			}
			frames++
			open = false
		default:
			t.Fatalf("unexpected %s", e)
		}
	}
	if frames != writers*each || off != l.Stats().Size {
		t.Fatalf("frames=%d end=%d size=%d", frames, off, l.Stats().Size)
	}
	want := contents(t, l)
	l.Close()
	if got := reopen(t, db); !reflect.DeepEqual(got, want) || len(got) != writers*each+writers {
		t.Fatalf("reopen has %d keys", len(got))
	}
}

// Concurrent Gets during batches see either the old or the new value of a
// key, never a torn or foreign value.
func TestConcurrentReadersSeeWholeValues(t *testing.T) {
	l := open(t, fileio.OS{}, newDB(t))
	defer l.Close()
	keys := []string{"k0", "k1", "k2", "k3"}
	val := func(k string, i int) string { return fmt.Sprintf("%s=%06d|", k, i) + strings.Repeat("#", i%5000) }
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, k := range keys {
					v, err := l.Get([]byte(k))
					if errors.Is(err, dberr.ErrNotFound) {
						continue
					}
					if err != nil {
						t.Error(err)
						return
					}
					var i int
					if _, err := fmt.Sscanf(string(v), k+"=%06d|", &i); err != nil || string(v) != val(k, i) {
						t.Errorf("torn or foreign value for %s: %.40q", k, v)
						return
					}
				}
			}
		}()
	}
	for i := 0; i < 300; i++ {
		var es []format.Entry
		for _, k := range keys {
			es = append(es, put(k, val(k, i)))
		}
		mustCommit(t, l, es...)
	}
	close(stop)
	wg.Wait()
}

// ---- Get errors (T-ERR-3) ----

func TestGetCorruptionKeepsHandleOpen(t *testing.T) {
	db := newDB(t)
	ffs := faultfs.New(fileio.OS{})
	l := open(t, ffs, db)
	defer l.Close()
	mustCommit(t, l, put("a", "hello"), put("b", "world"))
	loc, _ := l.idx.Get([]byte("a"))
	// Another handle changes one value byte on disk (Go opens the data file
	// with FILE_SHARE_WRITE, so this is possible while the DB is open).
	f, err := os.OpenFile(db, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{'J'}, loc.Off+int64(loc.Size)-1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := l.Get([]byte("a")); !errors.Is(err, dberr.ErrCorruption) {
		t.Fatalf("Get(a) = %v, want ErrCorruption", err)
	}
	if v, err := l.Get([]byte("b")); err != nil || string(v) != "world" {
		t.Fatalf("Get(b) = %q %v", v, err)
	}
	mustCommit(t, l, put("c", "still writable"))

	// A short read is corruption; an OS read error is not.
	ffs.Add(faultfs.Rule{Op: faultfs.OpRead, Path: db, Count: 1, Mode: faultfs.Partial, Keep: 3, Err: io.EOF})
	if _, err := l.Get([]byte("b")); !errors.Is(err, dberr.ErrCorruption) {
		t.Fatalf("short read: %v", err)
	}
	ffs.Add(faultfs.Rule{Op: faultfs.OpRead, Path: db, Count: 1, Err: errX})
	if _, err := l.Get([]byte("b")); !errors.Is(err, errX) || errors.Is(err, dberr.ErrCorruption) {
		t.Fatalf("read error: %v", err)
	}
	if v, err := l.Get([]byte("b")); err != nil || string(v) != "world" {
		t.Fatalf("handle unusable after read errors: %v", err)
	}
}

// ---- lifecycle ----

func TestClosedHandle(t *testing.T) {
	db := newDB(t)
	l := open(t, fileio.OS{}, db)
	mustCommit(t, l, put("a", "1"))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := l.Commit([]format.Entry{put("b", "2")}); !errors.Is(err, dberr.ErrClosed) {
		t.Fatal(err)
	}
	if err := l.Commit(nil); !errors.Is(err, dberr.ErrClosed) {
		t.Fatal(err)
	}
	if err := l.Delete([]byte("a")); !errors.Is(err, dberr.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := l.Get([]byte("a")); !errors.Is(err, dberr.ErrClosed) {
		t.Fatal(err)
	}
	l2 := open(t, fileio.OS{}, db) // lock was released
	l2.Close()
}

func TestSequenceExhaustionIsErrFull(t *testing.T) {
	db := newDB(t)
	sb, _ := format.Superblock{FirstSeq: math.MaxUint64, SnapshotEnd: 32}.Encode()
	data := append(sb[:], encodeFrame(t, math.MaxUint64, put("a", "1"))...)
	if err := os.WriteFile(db, data, 0o644); err != nil {
		t.Fatal(err)
	}
	ffs := faultfs.New(fileio.OS{})
	l := open(t, ffs, db)
	defer l.Close()
	n := ffs.OpCount()
	if err := l.Commit([]format.Entry{put("b", "2")}); !errors.Is(err, dberr.ErrFull) {
		t.Fatalf("err = %v", err)
	}
	if ffs.OpCount() != n {
		t.Fatal("exhausted commit did I/O")
	}
	if v, err := l.Get([]byte("a")); err != nil || string(v) != "1" {
		t.Fatal("existing data unreadable")
	}
}

// ---- fault and crash sweeps over a commit ----

// The committed base state, then one batch (with a value large enough to be
// written in several pieces).
var (
	sweepBig   = strings.Repeat("Z", 300<<10)
	sweepBatch = []format.Entry{put("a", "new-a"), del("b"), put("big", sweepBig), put("c", "3")}
	sweepOld   = map[string]string{"a": "1", "b": "2"}
	sweepNew   = map[string]string{"a": "new-a", "big": sweepBig, "c": "3"}
)

func sweepSetup(t *testing.T) string {
	db := newDB(t)
	l := open(t, fileio.OS{}, db)
	mustCommit(t, l, put("a", "1"), put("b", "2"))
	l.Close()
	return db
}

func sameMap(a, b map[string]string) bool { return reflect.DeepEqual(a, b) }

// Every operation of a commit fails in turn (before, after, short write).
// Definite results are definite after a reopen: nil -> the batch is there;
// ErrFull -> it is not; ErrOutcomeUnknown -> all or nothing, never part.
func TestCommitFaultSweep(t *testing.T) {
	db := sweepSetup(t)
	ffs := faultfs.New(fileio.OS{})
	l := open(t, ffs, db)
	base := ffs.OpCount()
	mustCommit(t, l, sweepBatch...)
	n := ffs.OpCount() - base
	l.Close()
	type fault struct {
		mode faultfs.Mode
		err  error
	}
	faults := []fault{{faultfs.FailBefore, errX}, {faultfs.FailAfter, errX}, {faultfs.Partial, nil},
		{faultfs.FailBefore, platform.DiskFullError()}, {faultfs.Partial, platform.DiskFullError()}}
	for i := 0; i < n; i++ {
		for _, f := range faults {
			db := sweepSetup(t)
			ffs := faultfs.New(fileio.OS{})
			l := open(t, ffs, db)
			ffs.Add(faultfs.Rule{Skip: i, Count: 1, Mode: f.mode, Err: f.err, Keep: 7})
			err := l.Commit(sweepBatch)
			l.Close()
			got := reopen(t, db)
			ctx := fmt.Sprintf("op %d mode %d err %v: commit returned %v", i, f.mode, f.err, err)
			switch {
			case err == nil:
				if !sameMap(got, sweepNew) {
					t.Fatalf("%s but reopen lacks the batch", ctx)
				}
			case errors.Is(err, dberr.ErrFull):
				if !sameMap(got, sweepOld) {
					t.Fatalf("%s but reopen shows the batch", ctx)
				}
			case errors.Is(err, dberr.ErrOutcomeUnknown):
				if !sameMap(got, sweepOld) && !sameMap(got, sweepNew) {
					t.Fatalf("%s and reopen shows a partial state: %v", ctx, got)
				}
			default:
				t.Fatalf("%s: unexpected error class", ctx)
			}
		}
	}
}

// The process is killed before, during (part of the bytes written) and
// after every operation of a commit. After a reopen the earlier committed
// state is intact and the batch is all or nothing.
func TestCommitCrashSweep(t *testing.T) {
	db := sweepSetup(t)
	ffs := faultfs.New(fileio.OS{})
	l := open(t, ffs, db)
	base := ffs.OpCount()
	mustCommit(t, l, sweepBatch...)
	n := ffs.OpCount() - base
	l.Close()
	sawNew, sawOld := false, false
	for i := 0; i < n; i++ {
		for _, r := range []faultfs.Rule{{Mode: faultfs.Crash}, {Mode: faultfs.Crash, Keep: 9}, {Mode: faultfs.CrashAfter}} {
			db := sweepSetup(t)
			ffs := faultfs.New(fileio.OS{})
			l := open(t, ffs, db)
			r.Skip, r.Count = i, 1 // rules count only events after they are added
			ffs.Add(r)
			if !faultfs.RunUntilCrash(func() { l.Commit(sweepBatch) }) {
				t.Fatalf("op %d: no crash", i)
			}
			got := reopen(t, db)
			switch {
			case sameMap(got, sweepOld):
				sawOld = true
			case sameMap(got, sweepNew):
				sawNew = true
			default:
				t.Fatalf("kill at op %d (mode %d keep %d): partial state %v", i, r.Mode, r.Keep, got)
			}
		}
	}
	if !sawNew || !sawOld {
		t.Fatalf("sweep did not reach both outcomes (old=%v new=%v)", sawOld, sawNew)
	}
}
