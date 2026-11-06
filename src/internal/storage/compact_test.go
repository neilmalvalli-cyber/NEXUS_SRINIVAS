package storage

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio/faultfs"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
)

func openCap(t *testing.T, fsys fileio.FS, db string, maxBytes int64, auto bool) *Log {
	t.Helper()
	l, err := OpenWith(fsys, db, Config{Recovery: fastOpts, MaxBytes: maxBytes, AutoCompact: auto})
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	return l
}

func fileSize(t *testing.T, name string) int64 {
	t.Helper()
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

// onlyDataFile asserts no temporary files remain next to db.
func onlyDataFile(t *testing.T, db string) {
	t.Helper()
	ents, err := os.ReadDir(filepath.Dir(db))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".lock") {
			names = append(names, e.Name())
		}
	}
	if !reflect.DeepEqual(names, []string{filepath.Base(db)}) {
		t.Fatalf("files next to the database: %v", names)
	}
}

func superblockOf(t *testing.T, db string) format.Superblock {
	t.Helper()
	sb, err := format.DecodeSuperblock(readFile(t, db))
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

// churn writes overwrites and deletes so the log carries dead data.
func churn(t *testing.T, l *Log) map[string]string {
	t.Helper()
	want := map[string]string{}
	for round := 0; round < 5; round++ {
		for k := 0; k < 30; k++ {
			key := fmt.Sprintf("k%02d", k)
			v := fmt.Sprintf("r%d-%s", round, strings.Repeat("v", k*13))
			mustCommit(t, l, put(key, v))
			want[key] = v
		}
		for k := round; k < 30; k += 7 {
			key := fmt.Sprintf("k%02d", k)
			mustCommit(t, l, del(key))
			delete(want, key)
		}
	}
	return want
}

// ---- plain compaction ----

func TestCompactPreservesLiveStateAndShrinks(t *testing.T) {
	db := newDB(t)
	l := open(t, fileio.OS{}, db)
	want := churn(t, l)
	before := l.Stats()
	if err := l.Compact(); err != nil {
		t.Fatal(err)
	}
	after := l.Stats()
	if after.Size >= before.Size || after.Size != fileSize(t, db) {
		t.Fatalf("size %d -> %d (file %d)", before.Size, after.Size, fileSize(t, db))
	}
	if got := contents(t, l); !reflect.DeepEqual(got, want) {
		t.Fatalf("contents changed by compaction")
	}
	// §9.1: first_seq of the new file is the old next_seq; numbering continues.
	sb := superblockOf(t, db)
	if sb.FirstSeq != before.NextSeq || sb.SnapshotEnd != uint64(after.Size) || after.NextSeq <= before.NextSeq {
		t.Fatalf("superblock %+v, next %d -> %d", sb, before.NextSeq, after.NextSeq)
	}
	if after.LiveBytes != before.LiveBytes || after.Keys != before.Keys {
		t.Fatalf("live counters changed: %+v vs %+v", before, after)
	}
	onlyDataFile(t, db)
	mustCommit(t, l, put("after", "compaction"))
	want["after"] = "compaction"
	l.Close()
	if got := reopen(t, db); !reflect.DeepEqual(got, want) {
		t.Fatal("reopen after compaction differs")
	}
}

func TestCompactIsNoOpWhenNothingToReclaim(t *testing.T) {
	db := newDB(t)
	ffs := faultfs.New(fileio.OS{})
	l := open(t, ffs, db)
	defer l.Close()
	n := ffs.OpCount()
	if err := l.Compact(); err != nil || ffs.OpCount() != n {
		t.Fatalf("compaction of an empty database: %v, I/O=%v", err, ffs.OpCount() != n)
	}
	churn(t, l)
	if err := l.Compact(); err != nil {
		t.Fatal(err)
	}
	b := readFile(t, db)
	if err := l.Compact(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, readFile(t, db)) {
		t.Fatal("compacting a compact file changed it (rule 4: never grow, no pointless rewrite)")
	}
}

func TestCompactAfterDeletingEverything(t *testing.T) {
	db := newDB(t)
	l := open(t, fileio.OS{}, db)
	mustCommit(t, l, put("a", "1"), put("b", "2"))
	mustCommit(t, l, del("a"), del("b"))
	next := l.Stats().NextSeq
	if err := l.Compact(); err != nil {
		t.Fatal(err)
	}
	if fileSize(t, db) != format.SuperblockSize || superblockOf(t, db).FirstSeq != next || l.Stats().NextSeq != next {
		t.Fatalf("empty snapshot: size %d sb %+v", fileSize(t, db), superblockOf(t, db))
	}
	mustCommit(t, l, put("c", "3"))
	l.Close()
	if got := reopen(t, db); !reflect.DeepEqual(got, map[string]string{"c": "3"}) {
		t.Fatal(got)
	}
}

// Large snapshots are split into frames by op count and payload size.
func TestCompactSplitsFrames(t *testing.T) {
	db := newDB(t)
	l := open(t, fileio.OS{}, db)
	want := map[string]string{}
	big := strings.Repeat("B", 900<<10)
	for i := 0; i < 8; i++ { // 8 x 900 KiB > 4 MiB frame cap
		k := fmt.Sprintf("big%d", i)
		mustCommit(t, l, put(k, big+k))
		want[k] = big + k
	}
	mustCommit(t, l, put("big0", "small now"))
	want["big0"] = "small now"
	if err := l.Compact(); err != nil {
		t.Fatal(err)
	}
	if got := contents(t, l); !reflect.DeepEqual(got, want) {
		t.Fatal("contents differ")
	}
	l.Close()
	f, _ := os.Open(db)
	defer f.Close()
	_, scan, err := recovery.ScanFile(fileFile{f}, db)
	if err != nil || scan.Frames < 2 {
		t.Fatalf("frames=%v err=%v", scan, err)
	}
}

// fileFile adapts *os.File to fileio.File for read-only checks.
type fileFile struct{ *os.File }

func (f fileFile) Size() (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func TestAutoCompactTrigger(t *testing.T) {
	db := newDB(t)
	l := open(t, fileio.OS{}, db)
	defer l.Close()
	v := strings.Repeat("x", 300<<10)
	maxSeen := int64(0)
	for i := 0; i < 40; i++ { // ~12 MiB written to one key
		mustCommit(t, l, put("hot", fmt.Sprint(i)+v))
		if s := l.Stats().Size; s > maxSeen {
			maxSeen = s
		}
	}
	if maxSeen >= 9<<20 || l.Stats().Size > 5<<20 {
		t.Fatalf("auto compaction did not keep the file small: max %d, now %d", maxSeen, l.Stats().Size)
	}
	if got, _ := l.Get([]byte("hot")); string(got) != "39"+v {
		t.Fatal("wrong value after auto compaction")
	}
	onlyDataFile(t, db)

	// Below 4 MiB nothing is triggered, however much is dead.
	db2 := newDB(t)
	l2 := open(t, fileio.OS{}, db2)
	defer l2.Close()
	for i := 0; i < 100; i++ {
		mustCommit(t, l2, put("k", strings.Repeat("y", 10<<10)))
	}
	if l2.Stats().Size < 100*(10<<10) {
		t.Fatal("compacted below the 4 MiB threshold")
	}
}

// ---- max_bytes admission (DESIGN §12.3) ----

func TestAdmissionCompactingCommitAndFull(t *testing.T) {
	db := newDB(t)
	const capBytes = 4096
	ffs := faultfs.New(fileio.OS{})
	l := openCap(t, ffs, db, capBytes, false)
	defer l.Close()
	v := strings.Repeat("v", 500)
	// Overwriting one key: appends until the cap, then compacting commits.
	sawCompaction := false
	for i := 0; i < 30; i++ {
		before := l.Stats().Size
		mustCommit(t, l, put("k", fmt.Sprint(i)+v))
		s := l.Stats().Size
		if s > capBytes || s != fileSize(t, db) {
			t.Fatalf("i=%d: size %d over cap", i, s)
		}
		if s < before {
			sawCompaction = true
		}
	}
	if !sawCompaction {
		t.Fatal("no compacting commit happened")
	}
	onlyDataFile(t, db)
	// A value that cannot fit even after compaction: ErrFull, nothing changes.
	b := readFile(t, db)
	n := len(ffs.Events())
	err := l.Commit([]format.Entry{put("huge", strings.Repeat("h", capBytes))})
	if !errors.Is(err, dberr.ErrFull) {
		t.Fatalf("err = %v, want ErrFull", err)
	}
	for _, e := range ffs.Events()[n:] {
		if e.Op == faultfs.OpWrite || e.Op == faultfs.OpCreate || e.Op == faultfs.OpMove {
			t.Fatalf("ErrFull did I/O: %s", e)
		}
	}
	if !bytes.Equal(b, readFile(t, db)) {
		t.Fatal("ErrFull changed the file")
	}
	if got, _ := l.Get([]byte("k")); string(got) != "29"+v {
		t.Fatal("handle not usable after ErrFull")
	}
}

func TestAdmissionOverCapDatabase(t *testing.T) {
	db := newDB(t)
	l := open(t, fileio.OS{}, db)
	for i := 0; i < 20; i++ {
		mustCommit(t, l, put(fmt.Sprintf("k%02d", i), strings.Repeat("v", 400)))
	}
	l.Close()
	size := fileSize(t, db)
	const capBytes = 2000
	// Rule 1: an over-cap database opens.
	ffs := faultfs.New(fileio.OS{})
	l = openCap(t, ffs, db, capBytes, false)
	defer l.Close()
	if v, err := l.Get([]byte("k00")); err != nil || len(v) != 400 {
		t.Fatal("over-cap database unreadable")
	}
	// Rule 2: a PUT cannot leave the file over the cap.
	if err := l.Commit([]format.Entry{put("new", "x")}); !errors.Is(err, dberr.ErrFull) {
		t.Fatalf("put on over-cap db: %v", err)
	}
	// A8: a net-empty commit succeeds with no I/O, even here.
	n := ffs.OpCount()
	if err := l.Commit([]format.Entry{put("ghost", "x"), del("ghost"), del("absent")}); err != nil || ffs.OpCount() != n {
		t.Fatalf("net-empty commit: %v, I/O=%v", err, ffs.OpCount() != n)
	}
	// Rule 3: a delete strictly shrinks the over-cap file (compacting commit).
	for i := 0; i < 20; i++ {
		before := l.Stats().Size
		if err := l.Delete([]byte(fmt.Sprintf("k%02d", i))); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
		s := l.Stats().Size
		if before > capBytes && s >= before {
			t.Fatalf("delete %d on an over-cap file did not shrink it: %d -> %d", i, before, s)
		}
		if before <= capBytes && s > capBytes {
			t.Fatalf("delete %d pushed the file over the cap: %d -> %d", i, before, s)
		}
	}
	if s := l.Stats().Size; s >= size || s > capBytes {
		t.Fatalf("final size %d", s)
	}
	// Under the cap again, PUTs are admitted.
	mustCommit(t, l, put("new", "x"))
}

// A deterministic random workload under a small cap, checked against a model
// after every operation: PUT-bearing commits that succeed leave the file
// within the cap; ErrFull changes nothing; a reopen equals the model.
func TestAdmissionRandomWorkloadMatchesModel(t *testing.T) {
	db := newDB(t)
	const capBytes = 16 << 10
	l := openCap(t, fileio.OS{}, db, capBytes, true)
	rng := rand.New(rand.NewSource(42))
	model := map[string]string{}
	fulls, oks := 0, 0
	for step := 0; step < 600; step++ {
		var es []format.Entry
		putBearing := false
		for j := 0; j < 1+rng.Intn(4); j++ {
			k := fmt.Sprintf("k%d", rng.Intn(25))
			if rng.Intn(3) == 0 {
				es = append(es, del(k))
			} else {
				es = append(es, put(k, strings.Repeat(string(rune('a'+rng.Intn(26))), rng.Intn(3000))))
			}
		}
		next := map[string]string{}
		for k, v := range model {
			next[k] = v
		}
		for _, e := range es {
			if e.Op == format.OpPut {
				next[string(e.Key)] = string(e.Value)
			} else {
				delete(next, string(e.Key))
			}
		}
		for _, e := range es {
			if _, ok := next[string(e.Key)]; ok {
				putBearing = true
			}
		}
		err := l.Commit(es)
		switch {
		case err == nil:
			oks++
			model = next
			if putBearing && l.Stats().Size > capBytes {
				t.Fatalf("step %d: size %d over cap after a PUT", step, l.Stats().Size)
			}
		case errors.Is(err, dberr.ErrFull):
			fulls++
		default:
			t.Fatalf("step %d: %v", step, err)
		}
		if step%50 == 0 {
			if got := contents(t, l); !reflect.DeepEqual(got, model) {
				t.Fatalf("step %d: contents differ from model", step)
			}
		}
	}
	if fulls == 0 || oks == 0 {
		t.Fatalf("workload not exercising both paths: ok=%d full=%d", oks, fulls)
	}
	l.Close()
	if got := reopen(t, db); !reflect.DeepEqual(got, model) {
		t.Fatal("reopen differs from model")
	}
	onlyDataFile(t, db)
}

// ---- compaction failure handling (DESIGN §11.3) ----

func TestCompactDiskFullAndCorruptionChangeNothing(t *testing.T) {
	t.Run("disk-full", func(t *testing.T) {
		db := newDB(t)
		ffs := faultfs.New(fileio.OS{})
		l := open(t, ffs, db)
		defer l.Close()
		want := churn(t, l)
		b := readFile(t, db)
		ffs.Add(faultfs.Rule{Op: faultfs.OpWrite, Path: db + recovery.SuffixCompact, Count: 1, Mode: faultfs.Partial, Keep: 9, Err: platform.DiskFullError()})
		if err := l.Compact(); !errors.Is(err, dberr.ErrFull) {
			t.Fatalf("err = %v", err)
		}
		if !bytes.Equal(b, readFile(t, db)) || !reflect.DeepEqual(contents(t, l), want) {
			t.Fatal("changed")
		}
		onlyDataFile(t, db)
		if err := l.Compact(); err != nil {
			t.Fatalf("handle unusable afterwards: %v", err)
		}
	})
	t.Run("corrupt-live-value", func(t *testing.T) {
		db := newDB(t)
		l := open(t, fileio.OS{}, db)
		defer l.Close()
		churn(t, l)
		loc, _ := l.idx.Get([]byte("k29"))
		f, _ := os.OpenFile(db, os.O_RDWR, 0)
		f.WriteAt([]byte{'#'}, loc.Off+int64(loc.Size)-1)
		f.Close()
		b := readFile(t, db)
		if err := l.Compact(); !errors.Is(err, dberr.ErrCorruption) {
			t.Fatalf("err = %v", err)
		}
		if !bytes.Equal(b, readFile(t, db)) {
			t.Fatal("file changed")
		}
		onlyDataFile(t, db)
		if _, err := l.Get([]byte("k00")); err != nil {
			t.Fatal("handle unusable")
		}
	})
}

// Sharing violations on the moves (antivirus scanning the file) are retried.
func TestCompactRetriesSharingViolations(t *testing.T) {
	sv := platform.SharingViolationError()
	if sv == nil {
		t.Skip("no sharing violations on this platform")
	}
	db := newDB(t)
	ffs := faultfs.New(fileio.OS{})
	l := open(t, ffs, db)
	defer l.Close()
	want := churn(t, l)
	ffs.Add(faultfs.Rule{Op: faultfs.OpMove, Count: 4, Err: sv}) // 2 per move for R1, then R2
	if err := l.Compact(); err != nil {
		t.Fatalf("transient violations not absorbed: %v\n%s", err, ffs.TraceString())
	}
	if !reflect.DeepEqual(contents(t, l), want) {
		t.Fatal("contents")
	}
}

// R2 fails and the restore of .old fails too: the outcome of the compacting
// commit is unknown (never a definite error, F1 rule 5), the handle is
// FAILED, and the next Open adopts the sealed .compact (rule A3), which
// contains the commit.
func TestCompactingCommitFailedRestoreIsUnknown(t *testing.T) {
	db := newDB(t)
	ffs := faultfs.New(fileio.OS{})
	l := openCap(t, ffs, db, 4096, false)
	for i := 0; mustSize(l) < 3500; i++ {
		mustCommit(t, l, put("k", fmt.Sprint(i)+strings.Repeat("v", 300)))
	}
	old := contents(t, l)
	ffs.Add(faultfs.Rule{Op: faultfs.OpMove, Path: db + recovery.SuffixCompact, Count: 1, Err: errX}) // R2
	ffs.Add(faultfs.Rule{Op: faultfs.OpMove, Path: db + recovery.SuffixOld, Count: 1, Err: errX})     // restore
	err := l.Commit([]format.Entry{put("k", "final"), put("j", strings.Repeat("j", 600))})
	if !errors.Is(err, dberr.ErrOutcomeUnknown) || errors.Is(err, dberr.ErrFull) {
		t.Fatalf("err = %v, want ErrOutcomeUnknown", err)
	}
	ffs.ClearRules()
	assertFailed(t, l, ffs, db, old, map[string]string{"k": "final", "j": strings.Repeat("j", 600)})
	if got := reopen(t, db); got["k"] != "final" {
		t.Fatalf("A3 did not adopt the sealed .compact: %v", got["k"])
	}
	onlyDataFile(t, db)
}

func mustSize(l *Log) int64 { return l.Stats().Size }

// ---- sweeps ----

type compactScenario struct {
	name   string
	setup  func(t *testing.T) string                          // builds the committed state, returns db path
	open   func(t *testing.T, fsys fileio.FS, db string) *Log // opens it for the operation
	op     func(l *Log) error                                 // the compaction under test
	oldSt  map[string]string
	newSt  map[string]string
	commit bool
}

func compactScenarios(t *testing.T) []compactScenario {
	// Plain compaction of a churned log.
	plainOld := func() map[string]string {
		l := open(t, fileio.OS{}, newDB(t))
		defer l.Close()
		return churn(t, l)
	}()
	plain := compactScenario{
		name: "plain",
		setup: func(t *testing.T) string {
			db := newDB(t)
			l := open(t, fileio.OS{}, db)
			churn(t, l)
			l.Close()
			return db
		},
		open:  func(t *testing.T, fsys fileio.FS, db string) *Log { return openCap(t, fsys, db, 0, false) },
		op:    func(l *Log) error { return l.Compact() },
		oldSt: plainOld, newSt: plainOld,
	}
	// Compacting commit forced by max_bytes.
	const capBytes = 4096
	v := strings.Repeat("v", 300)
	var ccOld map[string]string
	ccSetup := func(t *testing.T) string {
		db := newDB(t)
		l := openCap(t, fileio.OS{}, db, 0, false)
		for i := 0; i < 11; i++ {
			mustCommit(t, l, put("k", fmt.Sprint(i)+v), put(fmt.Sprintf("o%d", i%3), fmt.Sprint(i)))
		}
		ccOld = contents(t, l)
		l.Close()
		return db
	}
	ccSetup(t)
	batch := []format.Entry{put("k", "final"), del("o0"), put("n", strings.Repeat("n", 500))}
	ccNew := map[string]string{"k": "final", "o1": ccOld["o1"], "o2": ccOld["o2"], "n": strings.Repeat("n", 500)}
	cc := compactScenario{
		name:   "compacting-commit",
		setup:  ccSetup,
		open:   func(t *testing.T, fsys fileio.FS, db string) *Log { return openCap(t, fsys, db, capBytes, false) },
		op:     func(l *Log) error { return l.Commit(batch) },
		oldSt:  ccOld,
		newSt:  ccNew,
		commit: true,
	}
	return []compactScenario{plain, cc}
}

// countOps runs the scenario once and returns how many operations it does.
func countOps(t *testing.T, sc compactScenario) int {
	db := sc.setup(t)
	ffs := faultfs.New(fileio.OS{})
	l := sc.open(t, ffs, db)
	base := ffs.OpCount()
	if err := sc.op(l); err != nil {
		t.Fatalf("%s: fault-free run: %v", sc.name, err)
	}
	n := ffs.OpCount() - base
	if got := contents(t, l); !reflect.DeepEqual(got, sc.newSt) {
		t.Fatalf("%s: fault-free result %v", sc.name, got)
	}
	l.Close()
	if n < 10 {
		t.Fatalf("%s: only %d ops", sc.name, n)
	}
	return n
}

// T-AUTH-11 for compaction (DESIGN §11.5: before/during/between R1, R2,
// R3): the process is killed before, during and after every operation of a
// compaction or compacting commit. Reopen always succeeds; plain
// compaction never changes the contents; a compacting commit is all or
// nothing.
func TestCompactCrashSweep(t *testing.T) {
	for _, sc := range compactScenarios(t) {
		t.Run(sc.name, func(t *testing.T) {
			n := countOps(t, sc)
			seen := map[string]bool{}
			for i := 0; i < n; i++ {
				for _, r := range []faultfs.Rule{{Mode: faultfs.Crash}, {Mode: faultfs.Crash, Keep: 11}, {Mode: faultfs.CrashAfter}} {
					db := sc.setup(t)
					ffs := faultfs.New(fileio.OS{})
					l := sc.open(t, ffs, db)
					r.Skip, r.Count = i, 1
					ffs.Add(r)
					if !faultfs.RunUntilCrash(func() { sc.op(l) }) {
						t.Fatalf("op %d: no crash", i)
					}
					last := ffs.Events()[len(ffs.Events())-1]
					got := reopen(t, db)
					switch {
					case reflect.DeepEqual(got, sc.newSt):
						seen["new"] = true
					case reflect.DeepEqual(got, sc.oldSt):
						seen["old"] = true
					default:
						t.Fatalf("kill at op %d (%s, mode %d): partial state %v", i, last, r.Mode, got)
					}
					seen[string(last.Op)] = true
					onlyDataFile(t, db) // the next Open cleans every temp
				}
			}
			for _, op := range []string{"move", "create", "write", "sync", "remove"} {
				if !seen[op] {
					t.Fatalf("sweep never killed at a %s", op)
				}
			}
			if sc.commit && (!seen["new"] || !seen["old"]) {
				t.Fatalf("compacting commit did not reach both outcomes: %v", seen)
			}
		})
	}
}

// Every operation of a compaction fails in turn, before or after taking
// effect. Definite errors leave the handle OPEN with the old state (in
// memory and after reopen); unknown outcomes leave it FAILED and the reopen
// shows all or nothing. A failed restore never yields a definite error.
func TestCompactFaultSweep(t *testing.T) {
	for _, sc := range compactScenarios(t) {
		t.Run(sc.name, func(t *testing.T) {
			n := countOps(t, sc)
			classes := map[string]int{}
			for i := 0; i < n; i++ {
				for _, mode := range []faultfs.Mode{faultfs.FailBefore, faultfs.FailAfter} {
					db := sc.setup(t)
					ffs := faultfs.New(fileio.OS{})
					l := sc.open(t, ffs, db)
					ffs.Add(faultfs.Rule{Skip: i, Count: 1, Mode: mode, Err: errX})
					err := sc.op(l)
					ffs.ClearRules()
					ctx := fmt.Sprintf("op %d mode %d: %v", i, mode, err)
					failed := l.Stats() // works in any state
					_ = failed
					switch {
					case err == nil:
						classes["ok"]++
						if got := contents(t, l); !reflect.DeepEqual(got, sc.newSt) {
							t.Fatalf("%s: success but handle shows %v", ctx, got)
						}
						l.Close()
						if got := reopen(t, db); !reflect.DeepEqual(got, sc.newSt) {
							t.Fatalf("%s: success but reopen shows old state", ctx)
						}
					case errors.Is(err, dberr.ErrOutcomeUnknown), errors.Is(err, dberr.ErrFailed):
						classes["unknown"]++
						if _, gerr := l.Get([]byte("k")); !errors.Is(gerr, dberr.ErrFailed) {
							t.Fatalf("%s: handle not FAILED: %v", ctx, gerr)
						}
						l.Close()
						got := reopen(t, db)
						if !reflect.DeepEqual(got, sc.oldSt) && !reflect.DeepEqual(got, sc.newSt) {
							t.Fatalf("%s: partial state after reopen: %v", ctx, got)
						}
					default: // definite: nothing changed, handle OPEN and consistent
						classes["definite"]++
						if got := contents(t, l); !reflect.DeepEqual(got, sc.oldSt) {
							t.Fatalf("%s: definite error but handle shows %v", ctx, got)
						}
						if err := l.Commit([]format.Entry{put("probe", "p")}); err != nil && !errors.Is(err, dberr.ErrFull) {
							t.Fatalf("%s: handle unusable after definite error: %v", ctx, err)
						}
						l.Close()
						got := reopen(t, db)
						delete(got, "probe")
						if !reflect.DeepEqual(got, sc.oldSt) {
							t.Fatalf("%s: definite error but reopen shows %v", ctx, got)
						}
					}
					onlyDataFile(t, db)
				}
			}
			if classes["definite"] == 0 || classes["unknown"] == 0 {
				t.Fatalf("sweep did not reach all classes: %v", classes)
			}
		})
	}
}

// Readers never see an intermediate state while compactions run.
func TestCompactConcurrentReaders(t *testing.T) {
	l := open(t, fileio.OS{}, newDB(t))
	defer l.Close()
	want := churn(t, l)
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var wg sync.WaitGroup
	stop := make(chan struct{})
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
					if err != nil || string(v) != want[k] {
						t.Errorf("Get(%s) during compaction: %q %v", k, v, err)
						return
					}
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		mustCommit(t, l, put("churn", strings.Repeat("c", i*100)))
		if err := l.Compact(); err != nil {
			t.Error(err)
			break
		}
	}
	close(stop)
	wg.Wait()
}
