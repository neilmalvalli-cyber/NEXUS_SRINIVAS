package scenarios

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio/faultfs"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/storage"
)

func frameSize(t *testing.T, es ...format.Entry) int {
	t.Helper()
	n, err := format.PayloadSize(es, format.MaxFrameOps)
	if err != nil {
		t.Fatal(err)
	}
	return format.FrameHeaderSize + int(n)
}

func entrySize(e format.Entry) int { return format.EntryHeaderSize + len(e.Key) + len(e.Value) }

// ----------------------------------------------------------------- Group A

func TestGroupA(t *testing.T) {
	base := map[string]string{"a": "old", "z": "untouched"}
	newPut := func(l *storage.Log) { l.Commit([]format.Entry{P("a", "new-value-for-a")}) }

	scenario(t, "A1", "Kill before any bytes of a put reach the file",
		"Key absent (or holds its previous value)",
		"faultfs crash at the put's first write to the data file, before any byte is written; reopen",
		func(s *sc) {
			for _, key := range []string{"a", "fresh"} {
				db := newDB(t, P("a", "old"), P("z", "untouched"))
				size := fileSize(t, db)
				ok, _ := crashAt(t, db, 0, faultfs.Rule{Op: faultfs.OpWrite, Path: db, Mode: faultfs.Crash},
					func(l *storage.Log) { l.Commit([]format.Entry{P(key, "new")}) })
				if !ok {
					s.fatalf("crash did not fire")
				}
				if got := state(t, db); !same(got, base) {
					s.failf("put of %q: after reopen %v, want %v", key, got, base)
				}
				if fileSize(t, db) != size {
					s.failf("file size changed: %d -> %d", size, fileSize(t, db))
				}
			}
			s.note("existing key keeps its previous value; a new key stays absent; file size unchanged")
		})

	scenario(t, "A2", "Kill after some bytes of the record are written but not all (torn write)",
		"Key absent or previous value; the torn bytes are ignored, never returned as a value",
		"faultfs crash in the middle of the frame write, keeping 1, 16, 31, 32, 33, 43 bytes and all but 1 byte; reopen (recovery truncates the torn tail)",
		func(s *sc) {
			n := frameSize(t, P("a", "new-value-for-a"))
			for _, keep := range []int{1, 16, 31, 32, 33, 43, n - 1} {
				db := newDB(t, P("a", "old"), P("z", "untouched"))
				size := fileSize(t, db)
				if ok, _ := crashAt(t, db, 0, faultfs.Rule{Op: faultfs.OpWrite, Path: db, Mode: faultfs.Crash, Keep: keep}, newPut); !ok {
					s.fatalf("crash did not fire")
				}
				if fileSize(t, db) != size+int64(keep) {
					s.fatalf("keep %d: torn bytes not on disk (size %d, want %d)", keep, fileSize(t, db), size+int64(keep))
				}
				if got := state(t, db); !same(got, base) {
					s.failf("keep %d of %d bytes: after reopen %v, want %v", keep, n, got, base)
				}
				if fileSize(t, db) != size {
					s.failf("keep %d: torn tail not truncated (size %d, want %d)", keep, fileSize(t, db), size)
				}
			}
			s.note("frame is %d bytes; 7 torn lengths; every reopen returned the previous value and cut the file back to its committed end", n)
		})

	scenario(t, "A3", "Kill after the record is fully written but before fsync",
		"Either outcome is acceptable (success was not returned); must not corrupt anything else",
		"faultfs crash at the Sync of the data file (all frame bytes written, Sync not called); also crash right after the write returns",
		func(s *sc) {
			outcomes := map[string]int{}
			for _, r := range []faultfs.Rule{{Op: faultfs.OpSync, Path: s.t.Name(), Mode: faultfs.Crash}, {Op: faultfs.OpWrite, Mode: faultfs.CrashAfter}} {
				db := newDB(t, P("a", "old"), P("z", "untouched"))
				r.Path = db
				if ok, _ := crashAt(t, db, 0, r, newPut); !ok {
					s.fatalf("crash did not fire")
				}
				got := state(t, db)
				switch {
				case same(got, base):
					outcomes["old"]++
				case same(got, with(base, "a", "new-value-for-a")):
					outcomes["new"]++
				default:
					s.failf("state after reopen %v is neither old nor new", got)
				}
			}
			s.note("outcomes %v (z untouched in every case)", outcomes)
		})

	scenario(t, "A4", "Kill after fsync returns but before put returns success to the caller",
		"Key present with the new value",
		"faultfs crash immediately after the data file's Sync returned (CrashAfter on Sync); reopen",
		func(s *sc) {
			db := newDB(t, P("a", "old"), P("z", "untouched"))
			if ok, _ := crashAt(t, db, 0, faultfs.Rule{Op: faultfs.OpSync, Path: db, Mode: faultfs.CrashAfter}, newPut); !ok {
				s.fatalf("crash did not fire")
			}
			if got := state(t, db); !same(got, with(base, "a", "new-value-for-a")) {
				s.failf("after reopen %v, want new value", got)
			}
		})

	scenario(t, "A5", "Kill immediately after put returns success",
		"Key present with the new value. No exceptions",
		"(1) in-process: Commit returns nil, then a crash on the next file operation; (2) real process: `kvdb serve`, kill (TerminateProcess) the moment `ACK` is read, 20 times; reopen",
		func(s *sc) {
			db := newDB(t, P("a", "old"), P("z", "untouched"))
			ffs := faultfs.New(fileio.OS{})
			l := openLog(t, ffs, db, 0)
			commit(t, l, P("a", "new-value-for-a"))
			ffs.Add(faultfs.Rule{Mode: faultfs.Crash})
			if !faultfs.RunUntilCrash(func() { l.Get([]byte("a")); l.Close() }) {
				s.fatalf("crash did not fire")
			}
			if got := state(t, db); !same(got, with(base, "a", "new-value-for-a")) {
				s.failf("in-process: after reopen %v", got)
			}
			db2 := newDB(t)
			for i := 0; i < 20; i++ {
				p := startServe(t, db2)
				p.send(t, fmt.Sprintf("w1 a5-%d put k%d v%d\n", i, i, i))
				if r := p.reply(t); r != fmt.Sprintf("ACK a5-%d", i) {
					p.kill()
					s.fatalf("reply %q", r)
				}
				p.kill()
			}
			got := state(t, db2)
			for i := 0; i < 20; i++ {
				if got[fmt.Sprintf("k%d", i)] != fmt.Sprintf("v%d", i) {
					s.failf("acknowledged k%d missing after kill: %v", i, got)
				}
			}
			s.note("20/20 acknowledged puts present after kill-on-ACK of the serve process")
		})

	scenario(t, "A6", "Kill during a delete",
		"Key either still present with old value, or absent. Never a corrupted value",
		"faultfs crash before, in the middle of (9 bytes kept) and after every file operation of Delete; reopen after each",
		func(s *sc) {
			db0 := newDB(t, P("a", "old"), P("z", "untouched"))
			ev := countEvents(t, copyDB(t, db0), 0, func(l *storage.Log) { l.Delete([]byte("a")) })
			seen := map[string]int{}
			for i := range ev {
				for _, r := range []faultfs.Rule{{Mode: faultfs.Crash}, {Mode: faultfs.Crash, Keep: 9}, {Mode: faultfs.CrashAfter}} {
					db := copyDB(t, db0)
					r.Skip, r.Count = i, 1
					if ok, _ := crashAt(t, db, 0, r, func(l *storage.Log) { l.Delete([]byte("a")) }); !ok {
						s.fatalf("op %d: no crash", i)
					}
					got := state(t, db)
					switch {
					case same(got, base):
						seen["present"]++
					case same(got, with(base, "a", "<del>")):
						seen["absent"]++
					default:
						s.failf("crash at op %d (%v): state %v", i, ev[i], got)
					}
				}
			}
			s.note("%d operations x 3 crash modes: outcomes %v", len(ev), seen)
		})

	scenario(t, "A7", "Kill during a put of a 1 MB value",
		"Same rules as A1 to A5, with a large value spanning many disk blocks",
		"faultfs crash at every file operation of a 1 MiB put, with torn writes keeping 0, 4 KiB, 512 KiB and all-but-1 bytes of each write, and after each; plus torture --mode large-values (real kills)",
		func(s *sc) {
			big := strings.Repeat("0123456789abcdef", (1<<20)/16)
			db0 := newDB(t, P("a", "old"), P("z", "untouched"))
			put := func(l *storage.Log) { l.Commit([]format.Entry{P("a", big)}) }
			ev := countEvents(t, copyDB(t, db0), 0, put)
			seen := map[string]int{}
			for i, e := range ev {
				rules := []faultfs.Rule{{Mode: faultfs.Crash}, {Mode: faultfs.CrashAfter}}
				if e.Op == faultfs.OpWrite {
					for _, k := range []int{4096, 512 << 10, e.Len - 1} {
						if k > 0 && k < e.Len {
							rules = append(rules, faultfs.Rule{Mode: faultfs.Crash, Keep: k})
						}
					}
				}
				for _, r := range rules {
					db := copyDB(t, db0)
					r.Skip, r.Count = i, 1
					if ok, _ := crashAt(t, db, 0, r, put); !ok {
						s.fatalf("op %d: no crash", i)
					}
					got := state(t, db)
					switch {
					case same(got, base):
						seen["old"]++
					case same(got, with(base, "a", big)):
						seen["new (1 MiB, byte-exact)"]++
					default:
						s.failf("crash at op %d %v keep %d: value of a has %d bytes", i, e, r.Keep, len(got["a"]))
					}
				}
			}
			s.note("%d operations, outcomes %v", len(ev), seen)
			runTorture(s, "large-values", iters(10, 100), 999)
		})
}

// ----------------------------------------------------------------- Group B

func tenOps() []format.Entry {
	es := make([]format.Entry, 10)
	for i := range es {
		es[i] = P(fmt.Sprintf("b%d", i), fmt.Sprintf("batch-value-%d", i))
	}
	return es
}

func TestGroupB(t *testing.T) {
	base := map[string]string{"b0": "old0", "z": "untouched"}
	afterAll := with(base)
	for i, e := range tenOps() {
		_ = i
		afterAll[string(e.Key)] = string(e.Value)
	}
	batch := func(l *storage.Log) { l.Commit(tenOps()) }
	mk := func() string { return newDB(t, P("b0", "old0"), P("z", "untouched")) }

	scenario(t, "B1", "Kill after 3 of 10 operations in a batch are written",
		"None of the 10 are visible",
		"faultfs crash during the batch frame write keeping exactly the header and the first 3 entries (and 3.5 entries); reopen",
		func(s *sc) {
			es := tenOps()
			three := format.FrameHeaderSize + entrySize(es[0]) + entrySize(es[1]) + entrySize(es[2])
			for _, keep := range []int{three, three + entrySize(es[3])/2} {
				db := mk()
				if ok, _ := crashAt(t, db, 0, faultfs.Rule{Op: faultfs.OpWrite, Path: db, Mode: faultfs.Crash, Keep: keep}, batch); !ok {
					s.fatalf("no crash")
				}
				if got := state(t, db); !same(got, base) {
					s.failf("keep %d bytes: state %v", keep, got)
				}
			}
			s.note("3 complete entries (%d bytes incl. header) on disk: none visible after reopen", three)
		})

	scenario(t, "B2", "Kill after all 10 are written but the commit marker is not",
		"None of the 10 are visible",
		"NEXUS has no separate marker: the commit marker is the frame header's payload_len + payload CRC32C, valid only once every payload byte is present. Executed as (a) all 10 entries written but the frame's final byte missing, (b) all 10 entries on disk but the 32-byte header (the marker) zeroed; reopen",
		func(s *sc) {
			n := frameSize(t, tenOps()...)
			db := mk()
			if ok, _ := crashAt(t, db, 0, faultfs.Rule{Op: faultfs.OpWrite, Path: db, Mode: faultfs.Crash, Keep: n - 1}, batch); !ok {
				s.fatalf("no crash")
			}
			if got := state(t, db); !same(got, base) {
				s.failf("(a) state %v", got)
			}
			db = mk()
			size := fileSize(t, db)
			if ok, _ := crashAt(t, db, 0, faultfs.Rule{Op: faultfs.OpSync, Path: db, Mode: faultfs.Crash}, batch); !ok {
				s.fatalf("no crash")
			}
			zeroAt(t, db, size, format.FrameHeaderSize)
			if got := state(t, db); !same(got, base) {
				s.failf("(b) state %v", got)
			}
			if fileSize(t, db) != size {
				s.failf("(b) torn frame not truncated")
			}
			s.note("(a) %d of %d bytes: none visible; (b) entries present, header zeroed: rule C2 truncates, none visible", n-1, n)
		})

	scenario(t, "B3", "Kill after the commit marker is written but before fsync",
		"Either all or none. Never some",
		"faultfs crash at the data file's Sync after the complete frame was written, and right after the write; reopen",
		func(s *sc) {
			seen := map[string]int{}
			for _, r := range []faultfs.Rule{{Op: faultfs.OpSync, Mode: faultfs.Crash}, {Op: faultfs.OpWrite, Mode: faultfs.CrashAfter}} {
				db := mk()
				r.Path = db
				if ok, _ := crashAt(t, db, 0, r, batch); !ok {
					s.fatalf("no crash")
				}
				switch got := state(t, db); {
				case same(got, base):
					seen["none"]++
				case same(got, afterAll):
					seen["all"]++
				default:
					s.failf("partial batch: %v", got)
				}
			}
			s.note("outcomes %v", seen)
		})

	scenario(t, "B4", "Kill after the batch returns success",
		"All 10 visible",
		"(1) Commit returns nil, then crash on the next operation; (2) real process: serve batch of 10, kill on ACK; reopen",
		func(s *sc) {
			db := mk()
			ffs := faultfs.New(fileio.OS{})
			l := openLog(t, ffs, db, 0)
			commit(t, l, tenOps()...)
			ffs.Add(faultfs.Rule{Mode: faultfs.Crash})
			faultfs.RunUntilCrash(func() { l.Get([]byte("b1")); l.Close() })
			if got := state(t, db); !same(got, afterAll) {
				s.failf("in-process: %v", got)
			}
			db2 := newDB(t)
			p := startServe(t, db2)
			var sb strings.Builder
			sb.WriteString("w1 b4 batch 10\n")
			for i := 0; i < 10; i++ {
				fmt.Fprintf(&sb, "put k%d v%d\n", i, i)
			}
			p.send(t, sb.String())
			r := p.reply(t)
			p.kill()
			if r != "ACK b4" {
				s.fatalf("reply %q", r)
			}
			got := state(t, db2)
			for i := 0; i < 10; i++ {
				if got[fmt.Sprintf("k%d", i)] != fmt.Sprintf("v%d", i) {
					s.failf("process: k%d missing: %v", i, got)
				}
			}
			s.note("all 10 visible in both variants")
		})

	scenario(t, "B5", "A batch that puts key X and deletes key X in the same batch",
		"Define the ordering in your README and honour it after a crash",
		"Defined order: operations apply in order, the last one on a key wins. Batches [put X, del X], [del Y, put Y], [put Z=1, put Z=2] committed then crash; plus a crash sweep over every operation of such a batch: the result is always old state or the ordered result",
		func(s *sc) {
			mixed := []format.Entry{P("X", "1"), D("X"), D("Y"), P("Y", "2"), P("Z", "1"), P("Z", "2")}
			db := newDB(t, P("X", "old"), P("Y", "old"))
			ffs := faultfs.New(fileio.OS{})
			l := openLog(t, ffs, db, 0)
			commit(t, l, mixed...)
			ffs.Add(faultfs.Rule{Mode: faultfs.Crash})
			faultfs.RunUntilCrash(func() { l.Get([]byte("X")); l.Close() })
			want := map[string]string{"Y": "2", "Z": "2"}
			if got := state(t, db); !same(got, want) {
				s.failf("after crash: %v, want %v (X absent, Y=2, Z=2)", got, want)
			}
			db0 := newDB(t, P("X", "old"), P("Y", "old"))
			old := map[string]string{"X": "old", "Y": "old"}
			ev := countEvents(t, copyDB(t, db0), 0, func(l *storage.Log) { l.Commit(mixed) })
			seen := map[string]int{}
			for i := range ev {
				for _, r := range []faultfs.Rule{{Mode: faultfs.Crash}, {Mode: faultfs.Crash, Keep: 20}, {Mode: faultfs.CrashAfter}} {
					d := copyDB(t, db0)
					r.Skip, r.Count = i, 1
					crashAt(t, d, 0, r, func(l *storage.Log) { l.Commit(mixed) })
					switch got := state(t, d); {
					case same(got, old):
						seen["old"]++
					case same(got, want):
						seen["ordered result"]++
					default:
						s.failf("crash at op %d: %v", i, got)
					}
				}
			}
			s.note("after commit+crash: X absent, Y=2, Z=2; sweep of %d ops: %v", len(ev), seen)
		})

	scenario(t, "B6", "A batch of 10,000 operations, killed halfway",
		"None visible. Recovery still completes in under 2 seconds",
		"database with 1,000,000 keys; faultfs crash halfway through writing a 10,000-op batch frame (and with half of its bytes on disk); reopen timed (kvdb.Open: lock, authority, full scan with CRC checks, index rebuild, truncation); plus torture --mode large-batch",
		func(s *sc) {
			db := filepath.Join(t.TempDir(), "db.kv")
			l := openLog(t, fileio.OS{}, db, 0)
			for b := 0; b < 100; b++ {
				es := make([]format.Entry, 10000)
				for i := range es {
					k := b*10000 + i
					es[i] = P(fmt.Sprintf("key%07d", k), fmt.Sprintf("value-%07d-xxxxxxxx", k))
				}
				commit(t, l, es...)
			}
			l.Close()
			size := fileSize(t, db)
			big := make([]format.Entry, 10000)
			for i := range big {
				big[i] = P(fmt.Sprintf("batch%05d", i), "v")
			}
			n := frameSize(t, big...)
			if ok, _ := crashAt(t, db, 0, faultfs.Rule{Op: faultfs.OpWrite, Path: db, Mode: faultfs.Crash, Keep: n / 2},
				func(l *storage.Log) { l.Commit(big) }); !ok {
				s.fatalf("no crash")
			}
			start := time.Now()
			l2, err := storage.OpenWith(fileio.OS{}, db, storage.Config{Recovery: opts})
			el := time.Since(start)
			if err != nil {
				s.fatalf("reopen: %v", err)
			}
			st := l2.Stats()
			_, gerr := l2.Get([]byte("batch00000"))
			_, gerr2 := l2.Get([]byte("batch09999"))
			ri := l2.Recovery()
			l2.Close()
			if st.Keys != 1_000_000 || gerr == nil || gerr2 == nil {
				s.failf("keys %d (want 1000000), batch keys visible: %v %v", st.Keys, gerr == nil, gerr2 == nil)
			}
			if el > 2*time.Second {
				s.failf("recovery took %v (limit 2s)", el)
			}
			if st.Size != size {
				s.failf("torn batch not truncated")
			}
			s.note("1,000,000-key file %d bytes; 10,000-op frame %d bytes, %d on disk at the kill; recovery %v, discarded tail %d bytes (rule %s); 0 of 10,000 visible",
				size, n, n/2, el.Round(time.Millisecond), ri.DiscardedBytes, ri.Tail)
			runTorture(s, "large-batch", iters(5, 50), 61)
		})
}

func zeroAt(t *testing.T, path string, off int64, n int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteAt(make([]byte, n), off); err != nil {
		t.Fatal(err)
	}
}
