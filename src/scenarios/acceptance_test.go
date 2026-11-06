package scenarios

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

// memSampler records the peak Go heap and total memory obtained from the OS
// while a workload runs.
type memSampler struct {
	stop     chan struct{}
	wg       sync.WaitGroup
	peakHeap atomic.Uint64
	peakSys  atomic.Uint64
}

func startMem() *memSampler {
	m := &memSampler{stop: make(chan struct{})}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > m.peakHeap.Load() {
				m.peakHeap.Store(ms.HeapAlloc)
			}
			if ms.Sys > m.peakSys.Load() {
				m.peakSys.Store(ms.Sys)
			}
			select {
			case <-m.stop:
				return
			case <-t.C:
			}
		}
	}()
	return m
}

func (m *memSampler) done() (heapMB, sysMB float64) {
	close(m.stop)
	m.wg.Wait()
	return float64(m.peakHeap.Load()) / (1 << 20), float64(m.peakSys.Load()) / (1 << 20)
}

// TestAcceptanceMillionPutDelete is the official workload: 1,000,000 PUT
// operations, then 1,000,000 DELETE operations, each a separate durable call
// (one frame and one Sync per operation, no batching), then compaction,
// then writes after compaction, through the public kvdb API on the real
// file system. It takes about 15 minutes, so it runs only with
// KVDB_ACCEPTANCE=1.
func TestAcceptanceMillionPutDelete(t *testing.T) {
	if os.Getenv("KVDB_ACCEPTANCE") != "1" {
		t.Skip("set KVDB_ACCEPTANCE=1 to run (about 15 minutes)")
	}
	const n = 1_000_000
	dir := t.TempDir()
	path := filepath.Join(dir, "accept.kv")
	var report []string
	logf := func(format string, a ...any) {
		s := fmt.Sprintf(format, a...)
		report = append(report, s)
		t.Log(s)
	}
	size := func() int64 {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	key := func(i int) []byte { return []byte(fmt.Sprintf("key%07d", i)) }
	val := func(i int) []byte { return []byte(fmt.Sprintf("value-%07d-%s", i, "0123456789abcdef0123456789")) }
	logf("environment: %s/%s, %d logical CPUs, %s, test dir %s", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version(), dir)

	db, err := kvdb.Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	mem := startMem()
	start := time.Now()
	for i := 0; i < n; i++ {
		if err := db.Put(key(i), val(i)); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	el := time.Since(start)
	h, s := mem.done()
	sizeAfterPuts := size()
	logf("PUT phase: %d individual Put calls (each synced) in %v (%.0f ops/s, %.3f ms/op); file %d bytes; keys %d; peak heap %.0f MiB, peak Go sys %.0f MiB",
		n, el.Round(time.Millisecond), float64(n)/el.Seconds(), el.Seconds()*1000/float64(n), sizeAfterPuts, db.Stats().Keys, h, s)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	mem = startMem()
	start = time.Now()
	db, err = kvdb.Open(path, 0)
	el = time.Since(start)
	h, s = mem.done()
	if err != nil {
		t.Fatal(err)
	}
	if db.Stats().Keys != n {
		t.Fatalf("reopen: %d keys", db.Stats().Keys)
	}
	logf("RECOVERY: Open of %d keys / %d bytes in %v (limit 2 s): %s; peak heap %.0f MiB, peak Go sys %.0f MiB",
		n, sizeAfterPuts, el.Round(time.Millisecond), passFail(el < 2*time.Second), h, s)
	if el >= 2*time.Second {
		t.Errorf("recovery %v >= 2s", el)
	}
	for _, i := range []int{0, 1, n / 2, n - 1} {
		if v, err := db.Get(key(i)); err != nil || string(v) != string(val(i)) {
			t.Fatalf("get %d after reopen: %v", i, err)
		}
	}

	mem = startMem()
	start = time.Now()
	peak, minSize, drops := sizeAfterPuts, sizeAfterPuts, 0
	prev := sizeAfterPuts
	for i := 0; i < n; i++ {
		if err := db.Delete(key(i)); err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
		if i%1000 == 999 {
			sz := db.Stats().Size
			if sz > peak {
				peak = sz
			}
			if sz < minSize {
				minSize = sz
			}
			if sz < prev {
				drops++
			}
			prev = sz
		}
	}
	el = time.Since(start)
	h, s = mem.done()
	sizeAfterDeletes := size()
	logf("DELETE phase: %d individual Delete calls (each synced) in %v (%.0f ops/s); file peaked at %d bytes, shrank %d times by automatic compaction, %d bytes at the end; keys %d; peak heap %.0f MiB",
		n, el.Round(time.Millisecond), float64(n)/el.Seconds(), peak, drops, sizeAfterDeletes, db.Stats().Keys, h)
	if db.Stats().Keys != 0 {
		t.Fatalf("%d keys left", db.Stats().Keys)
	}

	start = time.Now()
	if err := db.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	el = time.Since(start)
	sizeAfterCompact := size()
	logf("COMPACTION: explicit Compact in %v: %d -> %d bytes; largest file during the workload %d bytes -> %d bytes (%s)",
		el.Round(time.Millisecond), sizeAfterDeletes, sizeAfterCompact, max(peak, sizeAfterPuts), sizeAfterCompact,
		passFail(sizeAfterCompact < sizeAfterPuts && sizeAfterCompact <= sizeAfterDeletes))
	if sizeAfterCompact >= sizeAfterPuts {
		t.Errorf("compaction did not shrink the file")
	}

	start = time.Now()
	const after = 10_000
	for i := 0; i < after; i++ {
		if err := db.Put(key(i), []byte("after-compaction")); err != nil {
			t.Fatalf("put after compaction %d: %v", i, err)
		}
	}
	db.Close()
	db, err = kvdb.Open(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	ok := db.Stats().Keys == after
	for i := 0; i < after && ok; i += 997 {
		v, err := db.Get(key(i))
		ok = err == nil && string(v) == "after-compaction"
	}
	if _, err := db.Verify(); err != nil {
		ok = false
		t.Errorf("verify: %v", err)
	}
	db.Close()
	logf("WRITES AFTER COMPACTION: %d puts then reopen in %v: %d keys, values verified, full-file verify OK: %s; final file %d bytes",
		after, time.Since(start).Round(time.Millisecond), after, passFail(ok), size())
	if !ok {
		t.Errorf("writes after compaction not intact")
	}
	if p := os.Getenv("KVDB_ACCEPTANCE_REPORT"); p != "" {
		if err := os.WriteFile(fromRoot(p), []byte(strings.Join(report, "\n")+"\n"), 0o644); err != nil {
			t.Errorf("writing report: %v", err)
		}
	}
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}
