package scenarios

import (
	"bufio"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio/faultfs"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/storage"
)

// tornDB builds a database with committed data and a torn final frame, so
// recovery has to replay and then truncate.
func tornDB(t *testing.T) (db string, want map[string]string) {
	t.Helper()
	db = filepath.Join(t.TempDir(), "db.kv")
	l := openLog(t, fileio.OS{}, db, 0)
	want = map[string]string{}
	for i := 0; i < 40; i++ {
		k, v := fmt.Sprintf("k%02d", i%25), fmt.Sprintf("v%d", i)
		commit(t, l, P(k, v))
		want[k] = v
	}
	l.Close()
	ffs := faultfs.New(fileio.OS{})
	l = openLog(t, ffs, db, 0)
	ffs.Add(faultfs.Rule{Op: faultfs.OpWrite, Path: db, Mode: faultfs.Crash, Keep: 40})
	faultfs.RunUntilCrash(func() { l.Commit([]format.Entry{P("torn", strings.Repeat("x", 200))}) })
	return db, want
}

// recoveryEvents returns the operations of one recovery (open) of db.
func recoveryEvents(t *testing.T, db string) []faultfs.Event {
	t.Helper()
	d := copyDB(t, db)
	ffs := faultfs.New(fileio.OS{})
	l, err := storage.OpenWith(ffs, d, storage.Config{Recovery: opts})
	if err != nil {
		t.Fatal(err)
	}
	ev := ffs.Events()
	l.Close()
	return ev
}

// crashOpen crashes a recovery at operation index i (mode r) and reports
// whether the crash fired.
func crashOpen(db string, r faultfs.Rule) bool {
	ffs := faultfs.New(fileio.OS{})
	ffs.Add(r)
	return faultfs.RunUntilCrash(func() {
		l, err := storage.OpenWith(ffs, db, storage.Config{Recovery: opts})
		if err == nil {
			l.Close()
		}
	})
}

func compactDB(t *testing.T) (string, map[string]string) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "db.kv")
	l := openLog(t, fileio.OS{}, db, 0)
	want := map[string]string{}
	for i := 0; i < 300; i++ {
		k, v := fmt.Sprintf("k%03d", i%60), fmt.Sprintf("value-%d-%s", i, strings.Repeat("p", 40))
		commit(t, l, P(k, v))
		want[k] = v
	}
	for i := 0; i < 10; i++ {
		commit(t, l, D(fmt.Sprintf("k%03d", i)))
		delete(want, fmt.Sprintf("k%03d", i))
	}
	l.Close()
	return db, want
}

func moveIndexes(ev []faultfs.Event) []int {
	var out []int
	for i, e := range ev {
		if e.Op == faultfs.OpMove {
			out = append(out, i)
		}
	}
	return out
}

// compactSweep crashes Compact at every operation (before, torn, after) and
// classifies the crash by phase relative to the two renames R1 and R2.
func compactSweep(s *sc, phase func(i int, moves []int) string, want func(string) bool) map[string]int {
	t := s.t
	db0, live := compactDB(t)
	ev := countEvents(t, copyDB(t, db0), 0, func(l *storage.Log) {
		if err := l.Compact(); err != nil {
			t.Fatal(err)
		}
	})
	moves := moveIndexes(ev)
	if len(moves) != 2 {
		s.fatalf("compaction made %d moves, want 2 (R1, R2): %v", len(moves), ev)
	}
	counts := map[string]int{}
	for i := range ev {
		p := phase(i, moves)
		if !want(p) {
			continue
		}
		for _, r := range []faultfs.Rule{{Mode: faultfs.Crash}, {Mode: faultfs.Crash, Keep: 100}, {Mode: faultfs.CrashAfter}} {
			db := copyDB(t, db0)
			r.Skip, r.Count = i, 1
			if ok, _ := crashAt(t, db, 0, r, func(l *storage.Log) { l.Compact() }); !ok {
				s.fatalf("op %d: no crash", i)
			}
			got := state(t, db)
			if !same(got, live) {
				s.failf("crash at op %d (%v, phase %s, mode %d): %d keys after reopen, want %d", i, ev[i], p, r.Mode, len(got), len(live))
			}
			if lo := leftovers(db); len(lo) > 0 {
				s.failf("crash at op %d (%s): temporary files left after reopen: %v", i, p, lo)
			}
			counts[p+":"+string(ev[i].Op)]++
		}
	}
	return counts
}

func phaseOf(i int, moves []int) string {
	switch {
	case i < moves[0]:
		return "before-R1"
	case i == moves[0]:
		return "R1"
	case i < moves[1]:
		return "between-R1-R2"
	case i == moves[1]:
		return "R2"
	default:
		return "after-R2"
	}
}

// ----------------------------------------------------------------- Group C

func TestGroupC(t *testing.T) {
	scenario(t, "C1", "Kill during open() while it is replaying the log",
		"The next open() succeeds and reaches the same correct state",
		"database with 40 frames and a torn tail; faultfs crash at every read of the recovery scan (and every other recovery operation before truncation), then a normal open; also chains of 3 consecutive crashed recoveries",
		func(s *sc) {
			db0, want := tornDB(t)
			ev := recoveryEvents(t, db0)
			n := 0
			for i, e := range ev {
				if e.Op == faultfs.OpTruncate {
					break
				}
				db := copyDB(t, db0)
				if !crashOpen(db, faultfs.Rule{Skip: i, Count: 1, Mode: faultfs.Crash}) {
					s.fatalf("op %d: no crash", i)
				}
				if got := state(t, db); !same(got, want) {
					s.failf("crash at recovery op %d (%v): state differs (%d keys)", i, e, len(got))
				}
				n++
			}
			rng := rand.New(rand.NewSource(1))
			for c := 0; c < 20; c++ {
				db := copyDB(t, db0)
				for k := 0; k < 3; k++ {
					crashOpen(db, faultfs.Rule{Skip: rng.Intn(len(ev)), Count: 1, Mode: faultfs.Crash})
				}
				if got := state(t, db); !same(got, want) {
					s.failf("chain %d: state differs", c)
				}
			}
			s.note("%d single crash points during replay + 20 chains of 3 crashed recoveries: every final open reached the same %d-key state", n, len(want))
		})

	scenario(t, "C2", "Kill during open() while it is truncating or repairing a torn tail",
		"The next open() succeeds",
		"faultfs crash before and after the Truncate of the torn tail, and before and after the Sync that follows it; reopen",
		func(s *sc) {
			db0, want := tornDB(t)
			n := 0
			for _, r := range []faultfs.Rule{
				{Op: faultfs.OpTruncate, Mode: faultfs.Crash}, {Op: faultfs.OpTruncate, Mode: faultfs.CrashAfter},
				{Op: faultfs.OpSync, Mode: faultfs.Crash}, {Op: faultfs.OpSync, Mode: faultfs.CrashAfter},
			} {
				db := copyDB(t, db0)
				r.Path = db
				if !crashOpen(db, r) {
					s.fatalf("rule %v did not fire", r)
				}
				if got := state(t, db); !same(got, want) {
					s.failf("crash %v: state differs", r)
				}
				n++
			}
			s.note("%d crash points around truncation: every next open succeeded with the same state", n)
		})

	scenario(t, "C3", "Kill during compaction, before the new file replaces the old",
		"Old file is still authoritative, all data intact",
		"faultfs crash before/torn/after every operation of Compact before rename R1 (creating, writing and syncing .compact); reopen; .compact must be cleaned up",
		func(s *sc) {
			c := compactSweep(s, phaseOf, func(p string) bool { return p == "before-R1" })
			s.note("crash points by phase:op %v; all data intact, no leftovers", c)
		})

	scenario(t, "C4", "Kill during compaction, in the middle of the rename or swap",
		"Exactly one of old or new is authoritative, both are complete, nothing is lost",
		"faultfs crash before and after R1 (path -> .old), between R1 and R2, before and after R2 (.compact -> path); reopen (authority rules A3-A6 pick the complete file)",
		func(s *sc) {
			c := compactSweep(s, phaseOf, func(p string) bool { return p == "R1" || p == "between-R1-R2" || p == "R2" })
			s.note("crash points by phase:op %v; all data intact, exactly one data file after reopen", c)
		})

	scenario(t, "C5", "Kill during compaction, after swap but before cleanup of temporary files",
		"Data intact. Leftover temp files are cleaned up or ignored on the next open()",
		"faultfs crash at every operation after R2 (directory sync, reopen and validation of the new file, removal of .old); reopen",
		func(s *sc) {
			c := compactSweep(s, phaseOf, func(p string) bool { return p == "after-R2" })
			s.note("crash points by phase:op %v; data intact, .old removed by the next open", c)
			runTorture(s, "compaction", iters(10, 100), 7)
		})

	scenario(t, "C6", "Kill during close()",
		"Same as a kill at any other time. close() is not special",
		"faultfs crash before/after closing the data file and before/after releasing the lock during Close, after acknowledged writes; reopen",
		func(s *sc) {
			n := 0
			for _, r := range []faultfs.Rule{{Op: faultfs.OpClose, Mode: faultfs.Crash}, {Op: faultfs.OpClose, Mode: faultfs.CrashAfter},
				{Op: faultfs.OpUnlock, Mode: faultfs.Crash}, {Op: faultfs.OpUnlock, Mode: faultfs.CrashAfter}} {
				db := newDB(t)
				ffs := faultfs.New(fileio.OS{})
				l := openLog(t, ffs, db, 0)
				commit(t, l, P("a", "1"), P("b", "2"))
				commit(t, l, D("a"))
				ffs.Add(r)
				if !faultfs.RunUntilCrash(func() { l.Close() }) {
					s.fatalf("rule %v did not fire", r)
				}
				if got := state(t, db); !same(got, map[string]string{"b": "2"}) {
					s.failf("crash %v: %v", r, got)
				}
				n++
			}
			s.note("%d crash points in Close: all acknowledged writes present, the next open succeeds (lock released by the OS)", n)
		})
}

// ----------------------------------------------------------------- Group D

func TestGroupD(t *testing.T) {
	scenario(t, "D1", "Two writers putting different keys, killed at random",
		"Every acknowledged put from either writer is present",
		"torture --mode concurrent-disjoint: 2 concurrent writers on disjoint keys through serve, TerminateProcess at a random ms, open/check/dump vs the oracle",
		func(s *sc) { runTorture(s, "concurrent-disjoint", iters(20, 100), 101) })

	scenario(t, "D2", "Two writers putting the same key, killed at random",
		"The key holds a value that one of the writers actually wrote. Never a mix of two values",
		"torture --mode same-key: 2 writers put unique 1-64 KiB values to one key; the oracle identifies every recovered value by hash",
		func(s *sc) { runTorture(s, "same-key", iters(20, 100), 202) })

	scenario(t, "D3", "One writer in a long batch, another doing single puts, killed at random",
		"Batch is all-or-nothing. Single puts that were acknowledged are present",
		"torture --mode large-batch: writer 0 sends 10,000-op batches, writers 1-2 single puts/deletes (some on the batch's keys)",
		func(s *sc) { runTorture(s, "large-batch", iters(10, 100), 303) })

	scenario(t, "D4", "A reader calling get during a batch on the same keys (no crash)",
		"Reader sees the state before the batch or after it. Never in between",
		"in-process: a writer commits 2,000 batches that set keys k0-k9 to generation g (64 KiB values); 4 readers call Get on all keys and Range (consistent snapshot) concurrently; every value must be a complete generation value, per-key generations never go back, and every Range snapshot holds one generation for all 10 keys",
		func(s *sc) {
			db := newDB(t)
			l := openLog(t, fileio.OS{}, db, 0)
			defer l.Close()
			val := func(g int) string { return fmt.Sprintf("gen-%06d-", g) + strings.Repeat("x", 64<<10) }
			mk := func(g int) []format.Entry {
				es := make([]format.Entry, 10)
				for i := range es {
					es[i] = P(fmt.Sprintf("k%d", i), val(g))
				}
				return es
			}
			commit(t, l, mk(0)...)
			var stop atomic.Bool
			var gets, snaps atomic.Int64
			var wg sync.WaitGroup
			errc := make(chan error, 8)
			for r := 0; r < 4; r++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					last := make([]int, 10)
					for !stop.Load() {
						for i := 0; i < 10; i++ {
							v, err := l.Get([]byte(fmt.Sprintf("k%d", i)))
							if err != nil {
								errc <- err
								return
							}
							var g int
							if _, err := fmt.Sscanf(string(v), "gen-%06d-", &g); err != nil || string(v) != val(g) {
								errc <- fmt.Errorf("torn value for k%d (%d bytes)", i, len(v))
								return
							}
							if g < last[i] {
								errc <- fmt.Errorf("k%d went back from generation %d to %d", i, last[i], g)
								return
							}
							last[i] = g
							gets.Add(1)
						}
						gen := -1
						err := l.Range(func(k, v []byte) error {
							var g int
							fmt.Sscanf(string(v), "gen-%06d-", &g)
							if gen >= 0 && g != gen {
								return fmt.Errorf("snapshot mixes generations %d and %d", gen, g)
							}
							gen = g
							return nil
						})
						if err != nil {
							errc <- err
							return
						}
						snaps.Add(1)
					}
				}()
			}
			for g := 1; g <= 2000; g++ {
				if err := l.Commit(mk(g)); err != nil {
					s.fatalf("commit: %v", err)
				}
			}
			stop.Store(true)
			wg.Wait()
			close(errc)
			for err := range errc {
				s.failf("reader: %v", err)
			}
			s.note("2,000 batches; %d gets and %d snapshots checked; no torn value, no regression, no mixed snapshot", gets.Load(), snaps.Load())
		})
}

// ----------------------------------------------------------------- Group E

func TestGroupE(t *testing.T) {
	const capB = 64 << 10
	fill := func(t *testing.T, l *storage.Log) map[string]string {
		m := map[string]string{}
		for i := 0; ; i++ {
			k, v := fmt.Sprintf("k%04d", i), strings.Repeat(fmt.Sprintf("%04d", i), 100)
			if err := l.Commit([]format.Entry{P(k, v)}); err != nil {
				if !errors.Is(err, dberr.ErrFull) {
					t.Fatalf("fill: %v", err)
				}
				return m
			}
			m[k] = v
		}
	}

	scenario(t, "E1", "File size cap reached during a single put",
		"put returns an error. Database still readable. Previous data intact",
		"max_bytes 64 KiB; puts of 400-byte values until ErrFull; the rejected put must leave the file byte-identical, the handle usable for reads, and the data intact after reopen; repeated through `kvdb put --max-bytes`",
		func(s *sc) {
			db := newDB(t)
			l := openLog(t, fileio.OS{}, db, capB)
			want := fill(t, l)
			before, _ := os.ReadFile(db)
			err := l.Commit([]format.Entry{P("overflow", strings.Repeat("o", 4000))})
			after, _ := os.ReadFile(db)
			if !errors.Is(err, dberr.ErrFull) || string(before) != string(after) {
				s.failf("over-cap put: err %v, file changed %v", err, string(before) != string(after))
			}
			if v, err := l.Get([]byte("k0000")); err != nil || string(v) != want["k0000"] {
				s.failf("read after ErrFull: %v", err)
			}
			l.Close()
			if got := state(t, db); !same(got, want) {
				s.failf("after reopen: %d keys, want %d", len(got), len(want))
			}
			if fileSize(t, db) > capB {
				s.failf("file %d > cap", fileSize(t, db))
			}
			_, e, err := runKvdb(t, "put", db, "cli-overflow", strings.Repeat("o", 4000), "--max-bytes", fmt.Sprint(capB))
			if err == nil || !strings.Contains(e, "(full)") {
				s.failf("CLI over-cap put: %v %q", err, e)
			}
			s.note("%d keys stored up to the cap (file %d of %d bytes); over-cap put -> ErrFull, file byte-identical; CLI prints %q", len(want), fileSize(t, db), capB, strings.TrimSpace(e))
		})

	scenario(t, "E2", "Cap reached halfway through a batch",
		"Batch returns an error. None of its operations are visible",
		"(a) admission: a batch whose frame does not fit max_bytes -> ErrFull before any byte is written; (b) the disk reports out-of-space halfway through writing the batch frame (faultfs partial write + ERROR_DISK_FULL) -> rollback truncate, ErrFull; reopen",
		func(s *sc) {
			db := newDB(t)
			l := openLog(t, fileio.OS{}, db, capB)
			want := fill(t, l)
			var es []format.Entry
			for i := 0; i < 20; i++ {
				es = append(es, P(fmt.Sprintf("b%02d", i), strings.Repeat("b", 300)))
			}
			if err := l.Commit(es); !errors.Is(err, dberr.ErrFull) {
				s.failf("(a) over-cap batch: %v", err)
			}
			l.Close()
			if got := state(t, db); !same(got, want) {
				s.failf("(a) state changed")
			}
			db2 := newDB(t, P("a", "1"))
			ffs := faultfs.New(fileio.OS{})
			l2 := openLog(t, ffs, db2, 0)
			n := frameSize(t, es...)
			ffs.Add(faultfs.Rule{Op: faultfs.OpWrite, Path: db2, Count: 1, Mode: faultfs.Partial, Keep: n / 2, Err: platform.DiskFullError()})
			err := l2.Commit(es)
			if !errors.Is(err, dberr.ErrFull) {
				s.failf("(b) disk full mid-batch: %v", err)
			}
			if err := l2.Commit([]format.Entry{P("after", "ok")}); err != nil {
				s.failf("(b) handle not usable after rollback: %v", err)
			}
			l2.Close()
			if got := state(t, db2); !same(got, map[string]string{"a": "1", "after": "ok"}) {
				s.failf("(b) state %v", got)
			}
			s.note("(a) ErrFull, nothing written; (b) %d of %d bytes written then disk-full: ErrFull, rolled back, none of 20 visible, next write OK", n/2, n)
		})

	scenario(t, "E3", "Cap reached, then process killed",
		"Restart succeeds. All previously acknowledged data intact",
		"(1) in-process: fill to ErrFull, then crash; reopen; (2) torture --mode disk-full: serve --max-bytes 256 KiB, writers hit ErrFull, TerminateProcess at random; file never above the cap",
		func(s *sc) {
			db := newDB(t)
			ffs := faultfs.New(fileio.OS{})
			l := openLog(t, ffs, db, capB)
			want := fill(t, l)
			ffs.Add(faultfs.Rule{Mode: faultfs.Crash})
			faultfs.RunUntilCrash(func() { l.Commit([]format.Entry{P("x", strings.Repeat("x", 9000))}); l.Get([]byte("k0000")) })
			if got := state(t, db); !same(got, want) {
				s.failf("in-process: state differs")
			}
			sum := runTorture(s, "disk-full", iters(10, 100), 404)
			if sum.FullErrors == 0 {
				s.failf("disk-full torture never reached the cap")
			}
		})

	scenario(t, "E4", "Cap reached during compaction",
		"Compaction fails or completes. Either way, no data lost and database still opens",
		"(a) the disk reports out-of-space while compaction writes .compact -> ErrFull, .compact removed; (b) a compacting commit whose result exceeds max_bytes -> ErrFull; (c) crash at every operation of a compacting commit triggered by the cap; reopen each",
		func(s *sc) {
			db, want := compactDB(t)
			ffs := faultfs.New(fileio.OS{})
			l := openLog(t, ffs, db, 0)
			ffs.Add(faultfs.Rule{Op: faultfs.OpWrite, Path: db + recovery.SuffixCompact, Mode: faultfs.FailBefore, Err: platform.DiskFullError()})
			err := l.Compact()
			if !errors.Is(err, dberr.ErrFull) {
				s.failf("(a) compaction on full disk: %v", err)
			}
			if _, gerr := l.Get([]byte("k050")); gerr != nil {
				s.failf("(a) handle unusable: %v", gerr)
			}
			l.Close()
			if got := state(t, db); !same(got, want) || exists(db+recovery.SuffixCompact) {
				s.failf("(a) data or leftover .compact")
			}
			// (b) and (c): an over-cap file where the next put needs a compacting commit.
			db2 := newDB(t)
			l2 := openLog(t, fileio.OS{}, db2, 0)
			for i := 0; i < 200; i++ {
				commit(t, l2, P(fmt.Sprintf("k%02d", i%20), strings.Repeat("v", 300)))
			}
			l2.Close()
			size := fileSize(t, db2)
			capC := size - 100 // the append does not fit; the snapshot (20 x 300 B) does
			huge := []format.Entry{P("huge", strings.Repeat("h", int(capC)))}
			l3 := openLog(t, fileio.OS{}, copyDB(t, db2), capC)
			if err := l3.Commit(huge); !errors.Is(err, dberr.ErrFull) {
				s.failf("(b) compacting commit over cap: %v", err)
			}
			l3.Close()
			want2 := state(t, copyDB(t, db2))
			put := func(l *storage.Log) { l.Commit([]format.Entry{P("new", "value")}) }
			ev := countEvents(t, copyDB(t, db2), capC, put)
			outcomes := map[string]int{}
			for i := range ev {
				for _, r := range []faultfs.Rule{{Mode: faultfs.Crash}, {Mode: faultfs.CrashAfter}} {
					d := copyDB(t, db2)
					r.Skip, r.Count = i, 1
					crashAt(t, d, capC, r, put)
					switch got := state(t, d); {
					case same(got, want2):
						// The old file (already over the cap, which is allowed
						// to open) stays authoritative and unchanged.
						outcomes["old"]++
						if fileSize(t, d) != size {
							s.failf("crash at op %d: old outcome but file size %d != %d", i, fileSize(t, d), size)
						}
					case same(got, with(want2, "new", "value")):
						// The compacted file with the commit replaced it: it fits.
						outcomes["new"]++
						if fileSize(t, d) > capC {
							s.failf("crash at op %d: new file %d bytes above cap %d", i, fileSize(t, d), capC)
						}
					default:
						s.failf("crash at op %d of compacting commit (%v): partial state", i, ev[i])
					}
				}
			}
			s.note("(a) ErrFull, .compact removed, data intact; (b) ErrFull; (c) %d operations of the compacting commit crashed: %v", len(ev), outcomes)
		})

	scenario(t, "E5", "After deletes free up space, compaction runs, writes succeed again",
		"Database is usable again without manual intervention",
		"max_bytes 64 KiB: fill until ErrFull, delete 80% of keys (delete-only commits on a full file), then new puts succeed (admission runs a compacting commit by itself); reopen",
		func(s *sc) {
			db := newDB(t)
			l := openLog(t, fileio.OS{}, db, capB)
			want := fill(t, l)
			n := len(want)
			i := 0
			for k := range want {
				if i%5 != 0 {
					if err := l.Commit([]format.Entry{D(k)}); err != nil {
						s.fatalf("delete on full database: %v", err)
					}
					delete(want, k)
				}
				i++
			}
			added := 0
			for j := 0; j < n/2; j++ {
				k := fmt.Sprintf("new%04d", j)
				if err := l.Commit([]format.Entry{P(k, strings.Repeat("n", 400))}); err != nil {
					s.failf("put %d after deletes: %v", j, err)
					break
				}
				want[k] = strings.Repeat("n", 400)
				added++
			}
			l.Close()
			if got := state(t, db); !same(got, want) {
				s.failf("after reopen: %d keys, want %d", len(got), len(want))
			}
			s.note("filled %d keys to the cap, deleted %d, then %d new puts succeeded with no manual step; file %d <= %d bytes", n, n-n/5-1, added, fileSize(t, db), capB)
		})
}

// ----------------------------------------------------------------- Group F

func TestGroupF(t *testing.T) {
	scenario(t, "F1", "500 consecutive random kills and restarts",
		"Invariant holds after every single one",
		"torture --mode default --iterations 500 (quick mode: 30): 3 writers, puts/deletes/batches/gets/compactions through serve, TerminateProcess at a random 5-600 ms, open/check/dump vs the oracle after every kill",
		func(s *sc) { runTorture(s, "default", iters(30, 500), 12345) })

	scenario(t, "F2", "Kill, restart, kill during recovery, restart, kill during recovery again",
		"Invariant holds. Recovery is idempotent",
		"(1) in-process: torn database, recovery crashed at a random operation twice in a row (100 chains), then a full recovery; (2) torture --mode recovery-crash: 200,000-key database, after each serve kill `kvdb open` is killed 1-3 times at a random point of its run time",
		func(s *sc) {
			db0, want := tornDB(t)
			ev := recoveryEvents(t, db0)
			rng := rand.New(rand.NewSource(2))
			for c := 0; c < 100; c++ {
				db := copyDB(t, db0)
				for k := 0; k < 2; k++ {
					mode := []faultfs.Mode{faultfs.Crash, faultfs.CrashAfter}[rng.Intn(2)]
					crashOpen(db, faultfs.Rule{Skip: rng.Intn(len(ev)), Count: 1, Mode: mode})
				}
				if got := state(t, db); !same(got, want) {
					s.failf("chain %d: state differs", c)
				}
				// Idempotence: another recovery changes nothing.
				b1, _ := os.ReadFile(db)
				state(t, db)
				if b2, _ := os.ReadFile(db); string(b1) != string(b2) {
					s.failf("chain %d: a second recovery changed the file", c)
				}
			}
			s.note("100 chains of 2 crashed recoveries over %d recovery operations: same state, and a further recovery leaves the file byte-identical", len(ev))
			runTorture(s, "recovery-crash", iters(5, 50), 42)
		})

	scenario(t, "F3", "1,000,000 keys written, process killed, open() called",
		"Recovery completes in under 2 seconds, all acknowledged keys present",
		"`kvdb serve` child receives 100 acknowledged batches of 10,000 puts (1,000,000 keys), then more writes are sent and the process is killed (TerminateProcess); `kvdb open` (timed, as printed and wall-clock), `kvdb check`, `kvdb dump` verifies every acknowledged key and value",
		func(s *sc) {
			db := filepath.Join(t.TempDir(), "db.kv")
			p := startServe(t, db)
			val := func(i int) string { return fmt.Sprintf("value-%07d-%s", i, "abcdefghij") }
			acked := 0
			for b := 0; b < 100; b++ {
				var sb strings.Builder
				fmt.Fprintf(&sb, "w1 f3-%03d batch 10000\n", b)
				for i := b * 10000; i < (b+1)*10000; i++ {
					fmt.Fprintf(&sb, "put key%07d %s\n", i, val(i))
				}
				p.send(t, sb.String())
				if r := p.reply(t); r != fmt.Sprintf("ACK f3-%03d", b) {
					p.kill()
					s.fatalf("batch %d: reply %q", b, r)
				}
				acked += 10000
			}
			// Keep writing (unacknowledged) and kill mid-stream.
			go func() {
				for i := 0; i < 5000; i++ {
					if _, err := fmt.Fprintf(p.in, "w2 extra-%d put extra%05d %s\n", i, i, strings.Repeat("e", 100)); err != nil {
						return
					}
				}
			}()
			time.Sleep(30 * time.Millisecond)
			p.kill()
			size := fileSize(t, db)
			start := time.Now()
			out, e, err := runKvdb(t, "open", db)
			wall := time.Since(start)
			if err != nil {
				s.fatalf("open: %v %s", err, e)
			}
			var keys int
			var recMs float64
			fmt.Sscanf(out, "keys=%d recovery_ms=%f", &keys, &recMs)
			if recMs >= 2000 {
				s.failf("recovery %.1f ms >= 2000 ms", recMs)
			}
			if out, _, err := runKvdb(t, "check", db); err != nil || strings.TrimSpace(out) != "OK" {
				s.failf("check: %v %q", err, out)
			}
			kv, _ := buildBinaries(t)
			cmd := execCommand(kv, "dump", db)
			stdout, _ := cmd.StdoutPipe()
			cmd.Start()
			br := bufio.NewReaderSize(stdout, 1<<20)
			found := 0
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					break
				}
				k, v, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
				var i int
				if _, err := fmt.Sscanf(k, "key%07d", &i); err == nil {
					if v != val(i) {
						s.failf("key %s has wrong value", k)
					}
					found++
				}
			}
			cmd.Wait()
			if found != acked {
				s.failf("dump has %d of %d acknowledged keys", found, acked)
			}
			s.note("%d acknowledged keys; file %d bytes; `kvdb open` printed recovery_ms=%.1f (process wall time %v incl. start-up), keys=%d (acked + any completed unacknowledged puts); dump verified all %d values",
				acked, size, recMs, wall.Round(time.Millisecond), keys, found)
		})

	scenario(t, "F4", "Database file truncated by 1 byte at the end by hand",
		"open() succeeds. At most the last unacknowledged record is lost",
		"10 acknowledged puts; os.Truncate(size-1); `kvdb open` and `kvdb check` succeed; the last record (now torn) is gone, the other 9 are intact, file cut back to the 9th frame",
		func(s *sc) {
			db := newDB(t)
			l := openLog(t, fileio.OS{}, db, 0)
			var ends []int64
			for i := 0; i < 10; i++ {
				commit(t, l, P(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)))
				ends = append(ends, l.Stats().Size)
			}
			l.Close()
			if err := os.Truncate(db, ends[9]-1); err != nil {
				s.fatalf("%v", err)
			}
			out, e, err := runKvdb(t, "open", db)
			if err != nil {
				s.fatalf("open after truncation: %v %s", err, e)
			}
			got := state(t, db)
			want := map[string]string{}
			for i := 0; i < 9; i++ {
				want[fmt.Sprintf("k%d", i)] = fmt.Sprintf("v%d", i)
			}
			if !same(got, want) {
				s.failf("state %v", got)
			}
			if fileSize(t, db) != ends[8] {
				s.failf("file %d, want %d", fileSize(t, db), ends[8])
			}
			s.note("open printed %q and stderr %q; 9 of 10 keys present, last record dropped", strings.TrimSpace(out), strings.TrimSpace(e))
		})
}
