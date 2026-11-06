package recovery

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio/faultfs"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/locking"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
)

// ---- helpers ----

var errX = errors.New("injected fault")

// fastRetry retries sharing violations without real sleeping.
func fastRetry(sleeps *int) fileio.RetryPolicy {
	return fileio.RetryPolicy{
		Initial: time.Millisecond, Max: 4 * time.Millisecond, Budget: 20 * time.Millisecond,
		Sleep: func(time.Duration) {
			if sleeps != nil {
				*sleeps++
			}
		},
	}
}

var fastOpts = Options{LockRetry: fileio.NoRetry, IORetry: fastRetry(nil)}

// sb returns a valid 32-byte database whose first_seq identifies it.
func sb(firstSeq uint64) []byte {
	b, err := format.Superblock{FirstSeq: firstSeq, SnapshotEnd: format.SuperblockSize}.Encode()
	if err != nil {
		panic(err)
	}
	return b[:]
}

var (
	fresh    = sb(1) // what A7 creates
	unsealed = make([]byte, 64)
	// corrupt: our superblock, but snapshot_end (64) is beyond EOF (32).
	corrupt = func() []byte {
		b, err := format.Superblock{FirstSeq: 5, SnapshotEnd: 64}.Encode()
		if err != nil {
			panic(err)
		}
		return b[:]
	}()
)

const (
	seqPath    = 11
	seqCompact = 22
	seqOld     = 33
	seqNew     = 44
)

// layout maps a suffix ("" = path) to file contents; a nil value means a
// directory of that name.
type layout map[string][]byte

func setup(t *testing.T, dir string, l layout) string {
	t.Helper()
	db := filepath.Join(dir, "db.kv")
	for suf, data := range l {
		if data == nil {
			if err := os.Mkdir(db+suf, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(db+suf, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// snapshot returns name -> contents for every entry in dir except the lock
// file (which may be created by an attempt). Directories map to "<dir>".
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), locking.Suffix) {
			continue
		}
		if e.IsDir() {
			out[e.Name()] = "<dir>"
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func names(m map[string]string) []string {
	var s []string
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}

// runOpen is the Stage 1 part of Open: Prepare, (scan: Stage 2), RemoveStale,
// then Close releases the lock.
func runOpen(fsys fileio.FS, db string) (*Resolution, error) {
	lk, res, err := Prepare(fsys, db, fastOpts)
	if err != nil {
		return nil, err
	}
	defer lk.Release()
	if errs := RemoveStale(fsys, lk, res, fastOpts.IORetry); len(errs) > 0 {
		return res, errs[0]
	}
	return res, nil
}

// pathNeverInvalid checks the core creation guarantee: if `path` exists it
// holds a complete, valid database (never empty or partial).
func pathNeverInvalid(t *testing.T, db, ctx string) {
	t.Helper()
	b, err := os.ReadFile(db)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("%s: reading path: %v", ctx, err)
	}
	s, err := format.DecodeSuperblock(b)
	if err == nil {
		err = s.CheckFileSize(uint64(len(b)))
	}
	if err != nil {
		t.Fatalf("%s: path exists but is not valid: %v", ctx, err)
	}
}

// mutating reports events that change the directory (anything but reads).
func mutating(ffs *faultfs.FS, lockPath string) []string {
	var out []string
	for _, e := range ffs.Events() {
		switch e.Op {
		case faultfs.OpCreate, faultfs.OpRemove, faultfs.OpMove, faultfs.OpWrite,
			faultfs.OpTruncate:
			out = append(out, e.String())
		}
	}
	return out
}

// ---- creation ----

// A new database is created through .new: exclusive create, superblock,
// sync, close, non-replacing move, directory sync — in that order — and the
// lock is the very first operation (T-LOCK-1).
func TestCreateThroughNew(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db.kv")
	ffs := faultfs.New(fileio.OS{})
	res, err := runOpen(ffs, db)
	if err != nil {
		t.Fatalf("%v\n%s", err, ffs.TraceString())
	}
	if !res.Created || !reflect.DeepEqual(res.Rules, []Rule{RuleA7, RuleA1}) {
		t.Fatalf("Created=%v Rules=%v", res.Created, res.Rules)
	}
	if res.Superblock != format.NewSuperblock() || res.Size != format.SuperblockSize {
		t.Fatalf("superblock %+v size %d", res.Superblock, res.Size)
	}
	if got := snapshot(t, dir); len(got) != 1 || got["db.kv"] != string(fresh) {
		t.Fatalf("directory after create: %v", names(got))
	}

	ev := ffs.Events()
	if ev[0].Op != faultfs.OpLock || ev[0].Path != locking.Path(db) {
		t.Fatalf("first op is not the lock (T-LOCK-1):\n%s", ffs.TraceString())
	}
	var seq []string
	for _, e := range ev {
		if e.Path == db+SuffixNew || e.Op == faultfs.OpSyncDir {
			if e.Op == faultfs.OpStat {
				continue
			}
			seq = append(seq, string(e.Op))
		}
	}
	want := []string{"create", "write", "sync", "close", "move", "syncdir"}
	if !reflect.DeepEqual(seq, want) {
		t.Fatalf("creation sequence = %v, want %v\n%s", seq, want, ffs.TraceString())
	}
	if ffs.OpenHandles() != 0 {
		t.Fatalf("%d handles leaked", ffs.OpenHandles())
	}
}

// Re-opening an existing database uses it as it is (A1) and changes nothing.
func TestReopenDoesNotRecreate(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db.kv")
	if _, err := runOpen(fileio.OS{}, db); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	ffs := faultfs.New(fileio.OS{})
	res, err := runOpen(ffs, db)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || !reflect.DeepEqual(res.Rules, []Rule{RuleA1}) {
		t.Fatalf("Created=%v Rules=%v", res.Created, res.Rules)
	}
	if m := mutating(ffs, locking.Path(db)); len(m) != 0 {
		t.Fatalf("reopen mutated files: %v", m)
	}
	if !reflect.DeepEqual(before, snapshot(t, dir)) {
		t.Fatal("files changed on reopen")
	}
}

// Resolve refuses to run without the lock (precondition of §11.4) and
// touches nothing.
func TestResolveRequiresLock(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db.kv")
	ffs := faultfs.New(fileio.OS{})
	if _, err := Resolve(ffs, nil, db, fileio.NoRetry); !errors.Is(err, locking.ErrNotHeld) {
		t.Fatalf("err = %v, want ErrNotHeld", err)
	}
	other, err := locking.Acquire(fileio.OS{}, filepath.Join(dir, "other.kv"), fileio.NoRetry)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	if _, err := Resolve(ffs, other, db, fileio.NoRetry); !errors.Is(err, locking.ErrNotHeld) {
		t.Fatalf("foreign lock: err = %v, want ErrNotHeld", err)
	}
	if len(ffs.Events()) != 0 {
		t.Fatalf("Resolve touched files without the lock:\n%s", ffs.TraceString())
	}
}

// T-LOCK-2: with the lock held elsewhere and files in an interrupted-
// compaction state, Prepare returns ErrLocked and nothing changes; only the
// lock file was touched.
func TestLockedOpenChangesNothing(t *testing.T) {
	dir := t.TempDir()
	db := setup(t, dir, layout{SuffixCompact: sb(seqCompact), SuffixOld: sb(seqOld)})
	holder, err := locking.Acquire(fileio.OS{}, db, fileio.NoRetry)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	before := snapshot(t, dir)
	ffs := faultfs.New(fileio.OS{})
	if _, _, err := Prepare(ffs, db, fastOpts); !errors.Is(err, dberr.ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	for _, e := range ffs.Events() {
		if e.Path != locking.Path(db) {
			t.Fatalf("touched %s while locked out:\n%s", e.Path, ffs.TraceString())
		}
	}
	if !reflect.DeepEqual(before, snapshot(t, dir)) {
		t.Fatal("files changed while locked out")
	}
}

// ---- the authority table ----

type authCase struct {
	id        string
	files     layout
	wantErr   error  // nil: success
	errNames  string // substring the error must contain
	wantRules []Rule
	wantFinal []byte // contents of path after Open
}

var authCases = []authCase{
	{id: "T-AUTH-1", files: layout{"": sb(seqPath)}, wantRules: []Rule{RuleA1}, wantFinal: sb(seqPath)},
	{id: "T-AUTH-2", files: layout{"": sb(seqPath), SuffixCompact: sb(seqCompact), SuffixOld: sb(seqOld), SuffixNew: sb(seqNew)},
		wantRules: []Rule{RuleA1}, wantFinal: sb(seqPath)},
	{id: "T-AUTH-2/unsealed-temps", files: layout{"": sb(seqPath), SuffixCompact: unsealed, SuffixOld: unsealed, SuffixNew: unsealed},
		wantRules: []Rule{RuleA1}, wantFinal: sb(seqPath)},
	{id: "T-AUTH-3", files: layout{"": unsealed, SuffixCompact: sb(seqCompact), SuffixOld: sb(seqOld)},
		wantErr: dberr.ErrBadFormat, errNames: "db.kv (rule A2)"},
	{id: "T-AUTH-4", files: layout{"": {}}, wantErr: dberr.ErrBadFormat, errNames: "rule A2"},
	{id: "T-AUTH-4/short", files: layout{"": sb(seqPath)[:31]}, wantErr: dberr.ErrBadFormat, errNames: "rule A2"},
	{id: "T-AUTH-4/foreign", files: layout{"": []byte("this is not a database, just a text file")}, wantErr: dberr.ErrBadFormat},
	{id: "T-AUTH-5", files: layout{SuffixCompact: sb(seqCompact), SuffixOld: sb(seqOld)},
		wantRules: []Rule{RuleA3, RuleA1}, wantFinal: sb(seqCompact)},
	{id: "T-AUTH-6", files: layout{SuffixCompact: unsealed, SuffixOld: sb(seqOld)},
		wantRules: []Rule{RuleA4, RuleA1}, wantFinal: sb(seqOld)},
	{id: "T-AUTH-7", files: layout{SuffixOld: sb(seqOld)}, wantRules: []Rule{RuleA4, RuleA1}, wantFinal: sb(seqOld)},
	{id: "T-AUTH-8", files: layout{SuffixCompact: unsealed, SuffixNew: sb(seqNew)},
		wantRules: []Rule{RuleA5, RuleA1}, wantFinal: sb(seqNew)},
	{id: "T-AUTH-9", files: layout{SuffixOld: unsealed}, wantErr: dberr.ErrBadFormat, errNames: "db.kv.old (rule A6)"},
	{id: "T-AUTH-10", files: layout{SuffixCompact: unsealed, SuffixNew: unsealed},
		wantRules: []Rule{RuleA7, RuleA1}, wantFinal: fresh},
	// Interrupted creation, constructed states (DESIGN §17).
	{id: "create/empty-dir", files: layout{}, wantRules: []Rule{RuleA7, RuleA1}, wantFinal: fresh},
	{id: "create/empty-new", files: layout{SuffixNew: {}}, wantRules: []Rule{RuleA7, RuleA1}, wantFinal: fresh},
	{id: "create/partial-new", files: layout{SuffixNew: fresh[:10]}, wantRules: []Rule{RuleA7, RuleA1}, wantFinal: fresh},
	{id: "create/sealed-new-not-moved", files: layout{SuffixNew: fresh}, wantRules: []Rule{RuleA5, RuleA1}, wantFinal: fresh},
	// D3: a damaged .old is never buried under a sealed .new.
	{id: "D3/old-invalid-new-sealed", files: layout{SuffixOld: unsealed, SuffixNew: sb(seqNew)},
		wantErr: dberr.ErrBadFormat, errNames: "db.kv.old (rule A6)"},
	// D1: our superblock with snapshot_end beyond EOF is corruption, and is
	// never deleted, wherever it is.
	{id: "D1/path-corrupt", files: layout{"": corrupt}, wantErr: dberr.ErrCorruption, errNames: "rule A2"},
	{id: "D1/old-corrupt", files: layout{SuffixOld: corrupt, SuffixNew: sb(seqNew)}, wantErr: dberr.ErrCorruption, errNames: "db.kv.old"},
	{id: "D1/compact-corrupt", files: layout{SuffixCompact: corrupt}, wantErr: dberr.ErrCorruption, errNames: "db.kv.compact"},
	{id: "D1/new-corrupt", files: layout{SuffixNew: corrupt}, wantErr: dberr.ErrCorruption, errNames: "db.kv.new"},
	// Non-regular files are never touched.
	{id: "other/path-is-dir", files: layout{"": nil}, wantErr: dberr.ErrBadFormat},
	{id: "other/new-is-dir", files: layout{SuffixNew: nil}, wantErr: dberr.ErrBadFormat, errNames: "db.kv.new"},
}

func TestAuthorityTable(t *testing.T) {
	for _, c := range authCases {
		t.Run(c.id, func(t *testing.T) {
			dir := t.TempDir()
			db := setup(t, dir, c.files)
			before := snapshot(t, dir)
			ffs := faultfs.New(fileio.OS{})
			res, err := runOpen(ffs, db)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) || !strings.Contains(fmt.Sprint(err), c.errNames) {
					t.Fatalf("err = %v, want %v mentioning %q", err, c.wantErr, c.errNames)
				}
				if errors.Is(err, dberr.ErrBadFormat) && errors.Is(err, dberr.ErrCorruption) {
					t.Fatalf("err is both ErrBadFormat and ErrCorruption: %v", err)
				}
				if m := mutating(ffs, locking.Path(db)); len(m) != 0 {
					t.Fatalf("refusal mutated files: %v", m)
				}
				if !reflect.DeepEqual(before, snapshot(t, dir)) {
					t.Fatal("refusal changed files")
				}
				// The lock was released on error.
				l, err := locking.Acquire(fileio.OS{}, db, fileio.NoRetry)
				if err != nil {
					t.Fatalf("lock not released after refusal: %v", err)
				}
				l.Release()
				return
			}
			if err != nil {
				t.Fatalf("%v\n%s", err, ffs.TraceString())
			}
			if !reflect.DeepEqual(res.Rules, c.wantRules) {
				t.Fatalf("rules = %v, want %v\n%s", res.Rules, c.wantRules, ffs.TraceString())
			}
			after := snapshot(t, dir)
			if got := names(after); !reflect.DeepEqual(got, []string{"db.kv"}) {
				t.Fatalf("files after open = %v, want only db.kv", got)
			}
			if after["db.kv"] != string(c.wantFinal) {
				t.Fatal("path does not hold the expected database")
			}
			if ffs.OpenHandles() != 0 {
				t.Fatalf("%d handles leaked", ffs.OpenHandles())
			}
		})
	}
}

// D2: Resolve itself does not delete the stale temps next to a valid path;
// they are deleted only by RemoveStale (after the Stage 2 scan).
func TestResolveDefersStaleCleanup(t *testing.T) {
	dir := t.TempDir()
	db := setup(t, dir, layout{"": sb(seqPath), SuffixOld: sb(seqOld), SuffixNew: unsealed})
	ffs := faultfs.New(fileio.OS{})
	lk, res, err := Prepare(ffs, db, fastOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	if m := mutating(ffs, locking.Path(db)); len(m) != 0 {
		t.Fatalf("Resolve deleted or changed files: %v", m)
	}
	want := []string{db + SuffixOld, db + SuffixNew}
	if !reflect.DeepEqual(res.Stale, want) {
		t.Fatalf("Stale = %v, want %v", res.Stale, want)
	}
	// A failed best-effort delete is reported, not fatal, and the rest go on.
	ffs.Add(faultfs.Rule{Op: faultfs.OpRemove, Path: db + SuffixOld, Err: errX})
	errs := RemoveStale(ffs, lk, res, fastOpts.IORetry)
	if len(errs) != 1 || !errors.Is(errs[0], errX) {
		t.Fatalf("RemoveStale errs = %v", errs)
	}
	if got := names(snapshot(t, dir)); !reflect.DeepEqual(got, []string{"db.kv", "db.kv.old"}) {
		t.Fatalf("after RemoveStale: %v", got)
	}
	if errs := RemoveStale(fileio.OS{}, nil, res, fileio.NoRetry); len(errs) != 1 || !errors.Is(errs[0], locking.ErrNotHeld) {
		t.Fatalf("RemoveStale without lock: %v", errs)
	}
}

// T-AUTH-12: an I/O error while checking any file stops Open with that error
// (never ErrBadFormat/ErrCorruption) and changes nothing.
func TestCheckErrorsStopOpen(t *testing.T) {
	state := layout{SuffixCompact: unsealed, SuffixOld: unsealed} // forces checks of path, .compact, .old
	for _, target := range []string{"", SuffixCompact, SuffixOld} {
		for _, op := range []faultfs.Op{faultfs.OpStat, faultfs.OpOpen, faultfs.OpSize, faultfs.OpRead} {
			t.Run(fmt.Sprintf("%s/%s", op, "db.kv"+target), func(t *testing.T) {
				dir := t.TempDir()
				files := layout{}
				for k, v := range state {
					files[k] = v
				}
				if target == "" {
					files[""] = sb(seqPath)
				}
				db := setup(t, dir, files)
				before := snapshot(t, dir)
				ffs := faultfs.New(fileio.OS{})
				ffs.Add(faultfs.Rule{Op: op, Path: db + target, Err: errX})
				_, err := runOpen(ffs, db)
				if !errors.Is(err, errX) {
					t.Fatalf("err = %v, want the injected error\n%s", err, ffs.TraceString())
				}
				if errors.Is(err, dberr.ErrBadFormat) || errors.Is(err, dberr.ErrCorruption) {
					t.Fatalf("I/O error converted to a format error: %v", err)
				}
				if m := mutating(ffs, locking.Path(db)); len(m) != 0 {
					t.Fatalf("mutated files after check error: %v", m)
				}
				if !reflect.DeepEqual(before, snapshot(t, dir)) {
					t.Fatal("files changed")
				}
			})
		}
	}
}

// A short read of the superblock (file claims >= 32 bytes) is an I/O
// anomaly, reported as such and not as ErrBadFormat.
func TestShortSuperblockReadIsNotBadFormat(t *testing.T) {
	dir := t.TempDir()
	db := setup(t, dir, layout{"": sb(seqPath)})
	ffs := faultfs.New(fileio.OS{})
	ffs.Add(faultfs.Rule{Op: faultfs.OpRead, Path: db, Mode: faultfs.Partial, Keep: 7, Err: nil})
	_, err := runOpen(ffs, db)
	if !errors.Is(err, fileio.ErrShortRead) || errors.Is(err, dberr.ErrBadFormat) {
		t.Fatalf("err = %v, want ErrShortRead and not ErrBadFormat", err)
	}
}

// Sharing violations on reads and moves are retried with bounded backoff; a
// persistent one is returned as it is (not ErrLocked, not ErrBadFormat).
func TestSharingViolationsAreRetried(t *testing.T) {
	sv := platform.SharingViolationError()
	if sv == nil {
		t.Skip("no sharing violations on this platform")
	}
	for _, op := range []faultfs.Op{faultfs.OpOpen, faultfs.OpMove, faultfs.OpRemove} {
		t.Run(string(op), func(t *testing.T) {
			// .old valid + unsealed .compact: open/move happen on .old.
			// For remove: unsealed .new is deleted by A7.
			files := layout{SuffixCompact: unsealed, SuffixOld: sb(seqOld)}
			target := "db.kv" + SuffixOld
			if op == faultfs.OpRemove {
				files = layout{SuffixNew: unsealed}
				target = "db.kv" + SuffixNew
			}
			dir := t.TempDir()
			db := setup(t, dir, files)
			ffs := faultfs.New(fileio.OS{})
			ffs.Add(faultfs.Rule{Op: op, Path: filepath.Join(dir, target), Count: 2, Err: sv})
			sleeps := 0
			lk, _, err := Prepare(ffs, db, Options{LockRetry: fileio.NoRetry, IORetry: fastRetry(&sleeps)})
			if err != nil {
				t.Fatalf("transient sharing violation not absorbed: %v\n%s", err, ffs.TraceString())
			}
			lk.Release()
			if sleeps != 2 {
				t.Fatalf("sleeps = %d, want 2", sleeps)
			}

			dir = t.TempDir()
			db = setup(t, dir, files)
			before := snapshot(t, dir)
			ffs = faultfs.New(fileio.OS{})
			ffs.Add(faultfs.Rule{Op: op, Path: filepath.Join(dir, target), Err: sv})
			_, _, err = Prepare(ffs, db, fastOpts)
			if !platform.IsSharingViolation(err) || errors.Is(err, dberr.ErrLocked) || errors.Is(err, dberr.ErrBadFormat) {
				t.Fatalf("persistent violation: err = %v", err)
			}
			if !reflect.DeepEqual(before, snapshot(t, dir)) {
				t.Fatal("files changed after a failed operation")
			}
		})
	}
}

// The restart cap stops a table that does not settle (here: a filesystem
// whose moves report success but do nothing).
type lyingMoveFS struct{ fileio.FS }

func (lyingMoveFS) MoveNoReplace(from, to string) error { return nil }

func TestRestartCap(t *testing.T) {
	dir := t.TempDir()
	db := setup(t, dir, layout{SuffixOld: sb(seqOld)})
	before := snapshot(t, dir)
	_, _, err := Prepare(lyingMoveFS{fileio.OS{}}, db, fastOpts)
	if err == nil || !strings.Contains(err.Error(), "did not settle") {
		t.Fatalf("err = %v, want restart-cap error", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, dir)) {
		t.Fatal("files changed")
	}
}

// ---- fault and crash sweeps ----

func successCases() []authCase {
	var out []authCase
	for _, c := range authCases {
		if c.wantErr == nil {
			out = append(out, c)
		}
	}
	return out
}

// Every operation of every successful resolution (creation included) is
// made to fail — before it happens, after it happens, and for writes as a
// short write. Open either succeeds or returns the injected error; `path` is
// never left invalid; the lock is released; and a clean re-run reaches the
// expected database.
func TestFaultSweep(t *testing.T) {
	for _, c := range successCases() {
		t.Run(c.id, func(t *testing.T) {
			base := t.TempDir()
			dry := faultfs.New(fileio.OS{})
			if _, err := runOpen(dry, setup(t, mustTemp(t, base), c.files)); err != nil {
				t.Fatal(err)
			}
			n := dry.OpCount()
			type fault struct {
				mode faultfs.Mode
				err  error
				keep int
			}
			faults := []fault{{faultfs.FailBefore, errX, 0}, {faultfs.FailAfter, errX, 0}, {faultfs.Partial, nil, 10}}
			for i := 0; i < n; i++ {
				for _, f := range faults {
					dir := mustTemp(t, base)
					db := setup(t, dir, c.files)
					ffs := faultfs.New(fileio.OS{})
					ffs.Add(faultfs.Rule{Skip: i, Count: 1, Mode: f.mode, Err: f.err, Keep: f.keep})
					ctx := fmt.Sprintf("op %d mode %d", i, f.mode)
					_, err := runOpen(ffs, db)
					if err != nil && f.err != nil && !errors.Is(err, f.err) {
						t.Fatalf("%s: err = %v, want nil or the injected error\n%s", ctx, err, ffs.TraceString())
					}
					pathNeverInvalid(t, db, ctx)
					if ffs.OpenHandles() != 0 {
						t.Fatalf("%s: %d handles leaked", ctx, ffs.OpenHandles())
					}
					if _, err := runOpen(fileio.OS{}, db); err != nil {
						t.Fatalf("%s: re-run failed: %v\n%s", ctx, err, ffs.TraceString())
					}
					after := snapshot(t, dir)
					if !reflect.DeepEqual(names(after), []string{"db.kv"}) || after["db.kv"] != string(c.wantFinal) {
						t.Fatalf("%s: final state %v differs from the fault-free run\n%s", ctx, names(after), ffs.TraceString())
					}
				}
			}
		})
	}
}

// T-AUTH-11 (and interrupted creation): the process is killed before and
// after every operation of every successful resolution. Afterwards `path` is
// never invalid, the authoritative contents survive somewhere, the kill
// released the lock, and re-running Open reaches the same final state.
func TestCrashSweep(t *testing.T) {
	for _, c := range successCases() {
		t.Run(c.id, func(t *testing.T) {
			base := t.TempDir()
			dry := faultfs.New(fileio.OS{})
			if _, err := runOpen(dry, setup(t, mustTemp(t, base), c.files)); err != nil {
				t.Fatal(err)
			}
			n := dry.OpCount()
			crashes := 0
			for i := 0; i < n; i++ {
				for _, mode := range []faultfs.Mode{faultfs.Crash, faultfs.CrashAfter} {
					for _, keep := range []int{0, 10} {
						if keep > 0 && mode != faultfs.Crash {
							continue
						}
						dir := mustTemp(t, base)
						db := setup(t, dir, c.files)
						ffs := faultfs.New(fileio.OS{})
						ffs.Add(faultfs.Rule{Skip: i, Count: 1, Mode: mode, Keep: keep})
						ctx := fmt.Sprintf("kill at op %d mode %d keep %d", i, mode, keep)
						if !faultfs.RunUntilCrash(func() { runOpen(ffs, db) }) {
							t.Fatalf("%s: no crash happened\n%s", ctx, ffs.TraceString())
						}
						crashes++
						pathNeverInvalid(t, db, ctx)
						if !bytes.Equal(c.wantFinal, fresh) || c.id == "create/sealed-new-not-moved" {
							if !survives(t, dir, c.wantFinal) {
								t.Fatalf("%s: no copy of the authoritative database survives: %v\n%s",
									ctx, names(snapshot(t, dir)), ffs.TraceString())
							}
						}
						if _, err := runOpen(fileio.OS{}, db); err != nil {
							t.Fatalf("%s: re-run after kill failed: %v\n%s", ctx, err, ffs.TraceString())
						}
						after := snapshot(t, dir)
						if !reflect.DeepEqual(names(after), []string{"db.kv"}) || after["db.kv"] != string(c.wantFinal) {
							t.Fatalf("%s: final state %v differs\n%s", ctx, names(after), ffs.TraceString())
						}
					}
				}
			}
			if crashes == 0 {
				t.Fatal("sweep injected no crashes")
			}
		})
	}
}

func survives(t *testing.T, dir string, want []byte) bool {
	for _, v := range snapshot(t, dir) {
		if v == string(want) {
			return true
		}
	}
	return false
}

func mustTemp(t *testing.T, base string) string {
	t.Helper()
	d, err := os.MkdirTemp(base, "c")
	if err != nil {
		t.Fatal(err)
	}
	return d
}
