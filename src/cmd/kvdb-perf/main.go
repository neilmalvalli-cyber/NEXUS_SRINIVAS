// Command kvdb-perf measures the NEXUS engine under workloads that kvdb-bench
// (single-threaded throughput) does not cover: concurrent writers, readers
// competing with writers, memory per key, allocations per operation,
// recovery time at growing sizes and compaction blocking. It drives the
// public src/kvdb API in-process, writes a JSON report, and optionally Go
// profiles (CPU, heap, mutex, block).
//
// The same binary, built from different engine versions, is what
// docs/PERFORMANCE_OPTIMIZATION.md compares: identical workloads, identical
// machine, one change at a time.
//
//	kvdb-perf --json perf.json [--quick] [--profiles DIR] [--only a,b] [--dir DIR]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

type result struct {
	Name    string             `json:"name"`
	Config  map[string]any     `json:"config"`
	Metrics map[string]float64 `json:"metrics"`
	Seconds float64            `json:"seconds"`
}

type report struct {
	Tool    string            `json:"tool"`
	Label   string            `json:"label"`
	Started string            `json:"started"`
	Machine map[string]string `json:"machine"`
	Quick   bool              `json:"quick"`
	Results []result          `json:"results"`
	Notes   []string          `json:"notes"`
}

var (
	scratch  string
	profDir  string
	quick    bool
	valueLen = 100
)

func main() {
	jsonOut := flag.String("json", "", "write the report to this file")
	flag.StringVar(&profDir, "profiles", "", "write CPU/heap/mutex/block profiles of selected phases into this directory")
	flag.BoolVar(&quick, "quick", false, "smaller workloads (minutes → seconds); not comparable to full runs")
	only := flag.String("only", "", "comma-separated phases to run (default all): writers,batch,delete,get,mixed,memory,recovery,compaction")
	dir := flag.String("dir", "", "scratch directory (default: a new temp directory, removed afterwards)")
	label := flag.String("label", "", "free text recorded in the report (for example: baseline, or the change under test)")
	flag.Parse()

	if *dir == "" {
		d, err := os.MkdirTemp("", "kvdb-perf-*")
		if err != nil {
			fail(err)
		}
		defer os.RemoveAll(d)
		scratch = d
	} else {
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			fail(err)
		}
		scratch = *dir
	}
	if profDir != "" {
		if err := os.MkdirAll(profDir, 0o755); err != nil {
			fail(err)
		}
	}
	want := map[string]bool{}
	for _, p := range strings.Split(*only, ",") {
		if p = strings.TrimSpace(p); p != "" {
			want[p] = true
		}
	}
	run := func(name string) bool { return len(want) == 0 || want[name] }

	rep := report{Tool: "kvdb-perf", Label: *label, Started: time.Now().Format(time.RFC3339), Machine: machine(), Quick: quick}
	add := func(rs ...result) {
		for _, r := range rs {
			fmt.Printf("%-34s %8.2fs  %s\n", r.Name, r.Seconds, fmtMetrics(r.Metrics))
			rep.Results = append(rep.Results, r)
		}
	}
	if run("writers") {
		for _, w := range []int{1, 4, 16} {
			add(writers(w))
		}
	}
	if run("batch") {
		add(batches())
	}
	if run("delete") {
		add(deletes())
	}
	if run("get") {
		add(gets(1), gets(8))
	}
	if run("mixed") {
		add(mixed())
	}
	if run("memory") {
		sizes := []int{100_000, 1_000_000}
		if quick {
			sizes = []int{100_000}
		}
		for _, n := range sizes {
			add(memory(n))
		}
	}
	if run("recovery") {
		sizes := []int{100_000, 1_000_000, 2_000_000}
		if quick {
			sizes = []int{100_000}
		}
		for _, n := range sizes {
			add(recoveryRun(n))
		}
	}
	if run("compaction") {
		add(compaction())
	}
	rep.Notes = append(rep.Notes,
		"Every PUT/DELETE/BATCH is synced before it returns (the engine has no other mode).",
		"Latencies are wall time around one public API call, measured by this tool.")
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*jsonOut, append(b, '\n'), 0o644); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "kvdb-perf:", err)
	os.Exit(1)
}

func fmtMetrics(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s=%.4g ", k, m[k])
	}
	return sb.String()
}

func machine() map[string]string {
	m := map[string]string{"os": runtime.GOOS + "/" + runtime.GOARCH, "go": runtime.Version(), "cpus": fmt.Sprint(runtime.NumCPU())}
	if v := os.Getenv("PROCESSOR_IDENTIFIER"); v != "" {
		m["cpu"] = v
	}
	if h, err := os.Hostname(); err == nil {
		m["host"] = h
	}
	return m
}

var dbCounter atomic.Int64

func freshDB() (*kvdb.DB, string) {
	p := filepath.Join(scratch, fmt.Sprintf("db-%d.kv", dbCounter.Add(1)))
	db, err := kvdb.Open(p, 0)
	if err != nil {
		fail(err)
	}
	return db, p
}

func remove(path string) {
	for _, suf := range []string{"", ".lock", ".compact", ".old", ".new"} {
		os.Remove(path + suf)
	}
}

func key(i int) []byte { return []byte(fmt.Sprintf("key%09d", i)) }

func value(rng *rand.Rand) []byte {
	b := make([]byte, valueLen)
	rng.Read(b)
	return b
}

func load(db *kvdb.DB, n int) {
	rng := rand.New(rand.NewSource(1))
	ops := make([]kvdb.Op, 0, 10000)
	for i := 0; i < n; i++ {
		ops = append(ops, kvdb.Op{Kind: kvdb.OpPut, Key: key(i), Value: value(rng)})
		if len(ops) == cap(ops) || i == n-1 {
			if err := db.Batch(ops); err != nil {
				fail(err)
			}
			ops = ops[:0]
		}
	}
}

type lat struct {
	mu sync.Mutex
	d  []time.Duration
}

func (l *lat) add(d time.Duration) { l.mu.Lock(); l.d = append(l.d, d); l.mu.Unlock() }

func (l *lat) into(m map[string]float64, prefix string) {
	if len(l.d) == 0 {
		return
	}
	s := slices.Clone(l.d)
	slices.Sort(s)
	q := func(p float64) float64 { return float64(s[int(p*float64(len(s)-1))].Microseconds()) }
	m[prefix+"p50_us"] = q(0.50)
	m[prefix+"p99_us"] = q(0.99)
	m[prefix+"max_us"] = float64(s[len(s)-1].Microseconds())
}

func startProfiles(name string) func() {
	if profDir == "" {
		return func() {}
	}
	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)
	cf, _ := os.Create(filepath.Join(profDir, name+".cpu.pprof"))
	_ = pprof.StartCPUProfile(cf)
	return func() {
		pprof.StopCPUProfile()
		cf.Close()
		for _, p := range []string{"mutex", "block", "allocs"} {
			f, _ := os.Create(filepath.Join(profDir, name+"."+p+".pprof"))
			_ = pprof.Lookup(p).WriteTo(f, 0)
			f.Close()
		}
		runtime.SetMutexProfileFraction(0)
		runtime.SetBlockProfileRate(0)
	}
}

// writers: w goroutines, each doing single PUTs of distinct keys.
func writers(w int) result {
	total := 8000
	if quick {
		total = 1600
	}
	db, p := freshDB()
	defer remove(p)
	defer db.Close()
	stop := startProfiles(fmt.Sprintf("writers-%d", w))
	var l lat
	var wg sync.WaitGroup
	t0 := time.Now()
	for g := 0; g < w; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := g; i < total; i += w {
				k, v := key(i), value(rng)
				s := time.Now()
				if err := db.Put(k, v); err != nil {
					fail(err)
				}
				l.add(time.Since(s))
			}
		}(g)
	}
	wg.Wait()
	el := time.Since(t0)
	stop()
	st := db.Stats()
	m := map[string]float64{"ops_per_sec": float64(total) / el.Seconds(), "file_bytes": float64(st.Size), "bytes_per_put": float64(st.Size-32) / float64(total)}
	l.into(m, "")
	return result{Name: fmt.Sprintf("put, %d writer(s)", w), Config: map[string]any{"writers": w, "ops": total, "value_size": valueLen}, Metrics: m, Seconds: el.Seconds()}
}

func batches() result {
	nb, size := 200, 100
	if quick {
		nb = 50
	}
	db, p := freshDB()
	defer remove(p)
	defer db.Close()
	rng := rand.New(rand.NewSource(2))
	var l lat
	t0 := time.Now()
	for b := 0; b < nb; b++ {
		ops := make([]kvdb.Op, size)
		for i := range ops {
			ops[i] = kvdb.Op{Kind: kvdb.OpPut, Key: key(b*size + i), Value: value(rng)}
		}
		s := time.Now()
		if err := db.Batch(ops); err != nil {
			fail(err)
		}
		l.add(time.Since(s))
	}
	el := time.Since(t0)
	m := map[string]float64{"ops_per_sec": float64(nb*size) / el.Seconds(), "batches_per_sec": float64(nb) / el.Seconds()}
	l.into(m, "batch_")
	return result{Name: "batch of 100 puts", Config: map[string]any{"batches": nb, "ops_per_batch": size}, Metrics: m, Seconds: el.Seconds()}
}

func deletes() result {
	n := 4000
	if quick {
		n = 1000
	}
	db, p := freshDB()
	defer remove(p)
	defer db.Close()
	load(db, n)
	var l lat
	t0 := time.Now()
	for i := 0; i < n; i++ {
		s := time.Now()
		if err := db.Delete(key(i)); err != nil {
			fail(err)
		}
		l.add(time.Since(s))
	}
	el := time.Since(t0)
	m := map[string]float64{"ops_per_sec": float64(n) / el.Seconds()}
	l.into(m, "")
	return result{Name: "delete, 1 writer", Config: map[string]any{"ops": n}, Metrics: m, Seconds: el.Seconds()}
}

func gets(threads int) result {
	n, reads := 100_000, 400_000
	if quick {
		reads = 100_000
	}
	db, p := freshDB()
	defer remove(p)
	defer db.Close()
	load(db, n)
	keys := make([][]byte, 4096)
	rng := rand.New(rand.NewSource(3))
	for i := range keys {
		keys[i] = key(rng.Intn(n))
	}
	runtime.GC()
	var ms0, ms1 runtime.MemStats
	runtime.ReadMemStats(&ms0)
	stop := startProfiles(fmt.Sprintf("get-%d", threads))
	var wg sync.WaitGroup
	t0 := time.Now()
	for g := 0; g < threads; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; i < reads; i += threads {
				if _, err := db.Get(keys[i&4095]); err != nil {
					fail(err)
				}
			}
		}(g)
	}
	wg.Wait()
	el := time.Since(t0)
	stop()
	runtime.ReadMemStats(&ms1)
	m := map[string]float64{
		"ops_per_sec":   float64(reads) / el.Seconds(),
		"ns_per_op":     float64(el.Nanoseconds()) / float64(reads) * float64(threads),
		"allocs_per_op": float64(ms1.Mallocs-ms0.Mallocs) / float64(reads),
		"bytes_per_op":  float64(ms1.TotalAlloc-ms0.TotalAlloc) / float64(reads),
	}
	return result{Name: fmt.Sprintf("get, %d thread(s)", threads), Config: map[string]any{"keys": n, "reads": reads, "threads": threads}, Metrics: m, Seconds: el.Seconds()}
}

// mixed: readers doing GETs while writers do PUTs; the reader latency shows
// how long reads wait for the writers' lock (which is held across fsync).
func mixed() result {
	n, dur := 100_000, 4*time.Second
	if quick {
		dur = 2 * time.Second
	}
	const readers, wrs = 4, 4
	db, p := freshDB()
	defer remove(p)
	defer db.Close()
	load(db, n)
	stop := startProfiles("mixed")
	var rl, wl lat
	var reads, writes atomic.Int64
	done := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(100 + g)))
			for {
				select {
				case <-done:
					return
				default:
				}
				k := key(rng.Intn(n))
				s := time.Now()
				if _, err := db.Get(k); err != nil {
					fail(err)
				}
				rl.add(time.Since(s))
				reads.Add(1)
			}
		}(g)
	}
	for g := 0; g < wrs; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(200 + g)))
			for {
				select {
				case <-done:
					return
				default:
				}
				k, v := key(rng.Intn(n)), value(rng)
				s := time.Now()
				if err := db.Put(k, v); err != nil {
					fail(err)
				}
				wl.add(time.Since(s))
				writes.Add(1)
			}
		}(g)
	}
	t0 := time.Now()
	time.Sleep(dur)
	close(done)
	wg.Wait()
	el := time.Since(t0)
	stop()
	m := map[string]float64{"get_ops_per_sec": float64(reads.Load()) / el.Seconds(), "put_ops_per_sec": float64(writes.Load()) / el.Seconds()}
	rl.into(m, "get_")
	wl.into(m, "put_")
	return result{Name: "mixed: 4 readers + 4 writers", Config: map[string]any{"keys": n, "readers": readers, "writers": wrs, "seconds": dur.Seconds()}, Metrics: m, Seconds: el.Seconds()}
}

func heapNow() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// memory: live heap attributable to an open database of n keys (after GC),
// measured after a reopen so only the recovered index remains.
func memory(n int) result {
	db, p := freshDB()
	load(db, n)
	db.Close()
	base := heapNow()
	t0 := time.Now()
	db2, err := kvdb.Open(p, 0)
	if err != nil {
		fail(err)
	}
	el := time.Since(t0)
	h := heapNow()
	st := db2.Stats()
	db2.Close()
	remove(p)
	m := map[string]float64{"heap_bytes": float64(h - base), "heap_bytes_per_key": float64(h-base) / float64(n), "keys": float64(st.Keys)}
	return result{Name: fmt.Sprintf("memory, %d keys", n), Config: map[string]any{"keys": n, "key_len": 12, "value_size": valueLen}, Metrics: m, Seconds: el.Seconds()}
}

func recoveryRun(n int) result {
	db, p := freshDB()
	load(db, n)
	db.Close()
	fi, _ := os.Stat(p)
	stop := startProfiles(fmt.Sprintf("recovery-%d", n))
	var ds []time.Duration
	var ms0, ms1 runtime.MemStats
	for r := 0; r < 3; r++ {
		runtime.GC()
		runtime.ReadMemStats(&ms0)
		t0 := time.Now()
		d, err := kvdb.Open(p, 0)
		if err != nil {
			fail(err)
		}
		ds = append(ds, time.Since(t0))
		runtime.ReadMemStats(&ms1)
		d.Close()
	}
	stop()
	remove(p)
	slices.Sort(ds)
	m := map[string]float64{
		"open_ms_median":      float64(ds[1].Microseconds()) / 1000,
		"open_ms_min":         float64(ds[0].Microseconds()) / 1000,
		"file_bytes":          float64(fi.Size()),
		"mb_per_sec":          float64(fi.Size()) / 1e6 / ds[1].Seconds(),
		"allocs_per_key":      float64(ms1.Mallocs-ms0.Mallocs) / float64(n),
		"alloc_bytes_per_key": float64(ms1.TotalAlloc-ms0.TotalAlloc) / float64(n),
	}
	return result{Name: fmt.Sprintf("recovery (open), %d keys", n), Config: map[string]any{"keys": n, "runs": 3}, Metrics: m, Seconds: ds[1].Seconds()}
}

// compaction: 100k keys, every key overwritten once (half the file dead),
// then Compact while 2 readers measure how long their GETs wait.
func compaction() result {
	n := 100_000
	db, p := freshDB()
	defer remove(p)
	defer db.Close()
	load(db, n)
	// Overwrite 70% of the keys: the file grows to about 1.7x its compacted
	// size, below the automatic trigger (2x), so the explicit Compact below
	// does the work (an earlier version overwrote 100% and auto-compaction
	// had already run).
	load(db, n*7/10)
	before := db.Stats()
	var rl lat
	done := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(300 + g)))
			for {
				select {
				case <-done:
					return
				default:
				}
				s := time.Now()
				if _, err := db.Get(key(rng.Intn(n))); err != nil {
					fail(err)
				}
				rl.add(time.Since(s))
			}
		}(g)
	}
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	var ms0, ms1 runtime.MemStats
	runtime.ReadMemStats(&ms0)
	stop := startProfiles("compaction")
	t0 := time.Now()
	if err := db.Compact(); err != nil {
		fail(err)
	}
	el := time.Since(t0)
	stop()
	runtime.ReadMemStats(&ms1)
	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()
	after := db.Stats()
	m := map[string]float64{
		"compact_ms":                  float64(el.Microseconds()) / 1000,
		"bytes_before":                float64(before.Size),
		"bytes_after":                 float64(after.Size),
		"temp_file_bytes":             float64(after.Size), // the sealed .compact file is the new file
		"alloc_bytes":                 float64(ms1.TotalAlloc - ms0.TotalAlloc),
		"bytes_written_per_live_byte": float64(after.Size) / float64(after.LiveBytes),
	}
	rl.into(m, "reader_get_")
	return result{Name: "compaction, 100k keys, 50% dead", Config: map[string]any{"keys": n}, Metrics: m, Seconds: el.Seconds()}
}
