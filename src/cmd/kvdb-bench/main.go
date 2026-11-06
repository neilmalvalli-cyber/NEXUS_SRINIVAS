// Command kvdb-bench measures NEXUS through its public library API
// (src/kvdb) with a deterministic workload, and records the machine and
// configuration next to every number so a run can be repeated and compared.
//
//	kvdb-bench [--n 100000] [--value-size 100] [--batch-size 1000] [--runs 3]
//	           [--seed 1] [--dir DIR] [--json FILE] [--md FILE] [--note TEXT]
//
// Phases, each on a fresh database per run:
//
//  1. put     N synced single puts of new keys
//  2. reopen  close and open again: recovery time with N keys
//  3. get     N gets in a seeded random order
//  4. batch   N overwrites in batches of --batch-size (one frame each)
//  5. compact explicit Compact(): time, file size before and after
//  6. delete  N synced single deletes
//  7. compact explicit Compact() of the now empty database
//
// Every write is durable when it returns (fsync / FlushFileBuffers), as in
// production use; nothing is relaxed for the benchmark. Automatic
// compactions that happen during a phase are counted and reported, because
// they are part of the cost of that phase. The numbers measure the engine
// in-process; the CLI and serve add process and pipe overhead on top.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

type latency struct {
	P50us float64 `json:"p50_us"`
	P99us float64 `json:"p99_us"`
	MaxUs float64 `json:"max_us"`
}

type phase struct {
	Name           string   `json:"phase"`
	Ops            int      `json:"ops"`
	Seconds        float64  `json:"seconds"`
	OpsPerSec      float64  `json:"ops_per_sec"`
	Latency        *latency `json:"latency_per_call,omitempty"`
	AutoCompaction int      `json:"automatic_compactions,omitempty"`
	SizeBefore     int64    `json:"file_bytes_before,omitempty"`
	SizeAfter      int64    `json:"file_bytes_after"`
	Keys           int      `json:"keys_after"`
}

type run struct {
	Run    int     `json:"run"`
	Phases []phase `json:"phases"`
}

type report struct {
	Tool      string            `json:"tool"`
	Started   string            `json:"started"`
	Machine   map[string]string `json:"machine"`
	Config    map[string]any    `json:"config"`
	Runs      []run             `json:"runs"`
	Median    []phase           `json:"median_of_runs"`
	Notes     []string          `json:"notes"`
	TotalSecs float64           `json:"total_seconds"`
}

func main() { os.Exit(realMain()) }

func realMain() int {
	n := flag.Int("n", 100000, "operations per phase (keys)")
	vsize := flag.Int("value-size", 100, "value size in bytes")
	bsize := flag.Int("batch-size", 1000, "operations per batch (max 10000)")
	runs := flag.Int("runs", 3, "independent runs; the report gives each and the median")
	seed := flag.Int64("seed", 1, "seed for values and the get order")
	dir := flag.String("dir", "", "directory for the database (default: a new temp directory, removed afterwards)")
	jsonOut := flag.String("json", "", "write the full report as JSON to this file")
	mdOut := flag.String("md", "", "write a Markdown summary to this file")
	note := flag.String("note", "", "free text recorded in the report (for example the disk model)")
	flag.Parse()
	if *n <= 0 || *vsize < 0 || *bsize <= 0 || *bsize > 10000 || *runs <= 0 {
		fmt.Fprintln(os.Stderr, "kvdb-bench: --n, --batch-size (1-10000) and --runs must be positive")
		return 2
	}
	base := *dir
	if base == "" {
		d, err := os.MkdirTemp("", "kvdb-bench-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "kvdb-bench:", err)
			return 2
		}
		defer os.RemoveAll(d)
		base = d
	} else if err := os.MkdirAll(base, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "kvdb-bench:", err)
		return 2
	}

	rep := report{Tool: "kvdb-bench 1.0", Started: time.Now().Format(time.RFC3339), Machine: machine(),
		Config: map[string]any{"n": *n, "value_size": *vsize, "batch_size": *bsize, "runs": *runs, "seed": *seed,
			"durability": "every put/delete/batch synced before it returns", "api": "in-process src/kvdb"}}
	if *note != "" {
		rep.Notes = append(rep.Notes, *note)
	}
	fmt.Printf("kvdb-bench: n=%d value-size=%d batch-size=%d runs=%d seed=%d\n", *n, *vsize, *bsize, *runs, *seed)
	for k, v := range rep.Machine {
		fmt.Printf("  %-10s %s\n", k, v)
	}
	t0 := time.Now()
	for r := 1; r <= *runs; r++ {
		path := filepath.Join(base, fmt.Sprintf("bench-run%d.kv", r))
		for _, suf := range []string{"", ".lock", ".compact", ".old", ".new"} {
			os.Remove(path + suf)
		}
		ph, err := oneRun(path, *n, *vsize, *bsize, *seed)
		for _, suf := range []string{"", ".lock", ".compact", ".old", ".new"} {
			os.Remove(path + suf)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "kvdb-bench: run %d: %v\n", r, err)
			return 1
		}
		rep.Runs = append(rep.Runs, run{Run: r, Phases: ph})
		fmt.Printf("\nrun %d\n", r)
		printPhases(ph)
	}
	rep.TotalSecs = time.Since(t0).Seconds()
	rep.Median = median(rep.Runs)
	fmt.Printf("\nmedian of %d run(s)\n", *runs)
	printPhases(rep.Median)
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*jsonOut, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "kvdb-bench:", err)
			return 1
		}
	}
	if *mdOut != "" {
		if err := os.WriteFile(*mdOut, []byte(markdown(rep)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "kvdb-bench:", err)
			return 1
		}
	}
	return 0
}

func key(i int) []byte { return []byte(fmt.Sprintf("key%09d", i)) }

func values(n, size int, rng *rand.Rand) [][]byte {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([][]byte, n)
	for i := range out {
		v := make([]byte, size)
		for j := range v {
			v[j] = letters[rng.Intn(len(letters))]
		}
		out[i] = v
	}
	return out
}

func oneRun(path string, n, vsize, bsize int, seed int64) ([]phase, error) {
	rng := rand.New(rand.NewSource(seed))
	vals := values(n, vsize, rng)
	vals2 := values(n, vsize, rng)
	order := rng.Perm(n)

	db, err := kvdb.Open(path, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	var out []phase
	lat := make([]time.Duration, 0, n)

	// timed runs fn for each i, tracking latency and automatic compactions.
	timed := func(name string, ops int, fn func(i int) error) error {
		lat = lat[:0]
		shrinks := 0
		last := db.Stats().Size
		start := time.Now()
		for i := 0; i < ops; i++ {
			t := time.Now()
			if err := fn(i); err != nil {
				return fmt.Errorf("%s %d: %w", name, i, err)
			}
			lat = append(lat, time.Since(t))
			if s := db.Stats().Size; s < last {
				shrinks++
				last = s
			} else {
				last = s
			}
		}
		el := time.Since(start).Seconds()
		units := ops
		if name == "batch" {
			units = n
		}
		st := db.Stats()
		p := phase{Name: name, Ops: units, Seconds: el, OpsPerSec: float64(units) / el,
			AutoCompaction: shrinks, SizeAfter: st.Size, Keys: st.Keys}
		if name != "get" { // a get takes a few µs, near the timer resolution: throughput only
			p.Latency = pct(lat)
		}
		out = append(out, p)
		return nil
	}

	if err := timed("put", n, func(i int) error { return db.Put(key(i), vals[i]) }); err != nil {
		return nil, err
	}

	// Reopen: recovery with n keys.
	if err := db.Close(); err != nil {
		return nil, err
	}
	db = nil
	start := time.Now()
	db, err = kvdb.Open(path, 0)
	if err != nil {
		return nil, err
	}
	el := time.Since(start).Seconds()
	st := db.Stats()
	out = append(out, phase{Name: "reopen (recovery)", Ops: st.Keys, Seconds: el, OpsPerSec: float64(st.Keys) / el, SizeAfter: st.Size, Keys: st.Keys})

	if err := timed("get", n, func(i int) error {
		v, err := db.Get(key(order[i]))
		if err == nil && len(v) != vsize {
			err = fmt.Errorf("value of %d bytes, want %d", len(v), vsize)
		}
		return err
	}); err != nil {
		return nil, err
	}

	batches := (n + bsize - 1) / bsize
	if err := timed("batch", batches, func(b int) error {
		ops := make([]kvdb.Op, 0, bsize)
		for i := b * bsize; i < n && i < (b+1)*bsize; i++ {
			ops = append(ops, kvdb.Op{Kind: kvdb.OpPut, Key: key(i), Value: vals2[i]})
		}
		return db.Batch(ops)
	}); err != nil {
		return nil, err
	}

	compact := func(name string) error {
		before := db.Stats().Size
		start := time.Now()
		if err := db.Compact(); err != nil {
			return err
		}
		el := time.Since(start).Seconds()
		st := db.Stats()
		out = append(out, phase{Name: name, Ops: st.Keys, Seconds: el, SizeBefore: before, SizeAfter: st.Size, Keys: st.Keys})
		return nil
	}
	if err := compact("compact (n live keys)"); err != nil {
		return nil, err
	}
	if err := timed("delete", n, func(i int) error { return db.Delete(key(i)) }); err != nil {
		return nil, err
	}
	if err := compact("compact (empty)"); err != nil {
		return nil, err
	}
	err = db.Close()
	db = nil
	return out, err
}

func pct(d []time.Duration) *latency {
	if len(d) == 0 {
		return nil
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	us := func(x time.Duration) float64 { return float64(x.Nanoseconds()) / 1000 }
	return &latency{P50us: us(s[len(s)/2]), P99us: us(s[len(s)*99/100]), MaxUs: us(s[len(s)-1])}
}

func median(rs []run) []phase {
	if len(rs) == 0 {
		return nil
	}
	out := make([]phase, len(rs[0].Phases))
	for p := range out {
		var ph []phase
		for _, r := range rs {
			ph = append(ph, r.Phases[p])
		}
		sort.Slice(ph, func(i, j int) bool { return ph[i].Seconds < ph[j].Seconds })
		out[p] = ph[len(ph)/2]
	}
	return out
}

func printPhases(ps []phase) {
	fmt.Printf("  %-22s %10s %10s %14s %12s %12s %8s %14s %14s\n", "phase", "ops", "seconds", "ops/s", "p50 us", "p99 us", "autocmp", "file before", "file after")
	for _, p := range ps {
		p50, p99 := "", ""
		if p.Latency != nil {
			p50, p99 = fmt.Sprintf("%.0f", p.Latency.P50us), fmt.Sprintf("%.0f", p.Latency.P99us)
		}
		ops := ""
		if p.OpsPerSec > 0 {
			ops = fmt.Sprintf("%.0f", p.OpsPerSec)
		}
		before := ""
		if p.SizeBefore > 0 {
			before = fmt.Sprint(p.SizeBefore)
		}
		fmt.Printf("  %-22s %10d %10.3f %14s %12s %12s %8d %14s %14d\n", p.Name, p.Ops, p.Seconds, ops, p50, p99, p.AutoCompaction, before, p.SizeAfter)
	}
}

func markdown(r report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# kvdb-bench results\n\nStarted %s, %d run(s), %.0f s in total.\n\n", r.Started, len(r.Runs), r.TotalSecs)
	b.WriteString("| Machine | |\n|---|---|\n")
	keys := make([]string, 0, len(r.Machine))
	for k := range r.Machine {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "| %s | %s |\n", k, r.Machine[k])
	}
	b.WriteString("\n| Configuration | |\n|---|---|\n")
	ck := make([]string, 0, len(r.Config))
	for k := range r.Config {
		ck = append(ck, k)
	}
	sort.Strings(ck)
	for _, k := range ck {
		fmt.Fprintf(&b, "| %s | %v |\n", k, r.Config[k])
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "\nNote: %s\n", n)
	}
	b.WriteString("\nMedian of the runs (by phase duration):\n\n| Phase | Ops | Seconds | Ops/s | p50 µs | p99 µs | Automatic compactions | File before | File after |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, p := range r.Median {
		p50, p99, ops, before := "—", "—", "—", "—"
		if p.Latency != nil {
			p50, p99 = fmt.Sprintf("%.0f", p.Latency.P50us), fmt.Sprintf("%.0f", p.Latency.P99us)
		}
		if p.OpsPerSec > 0 {
			ops = fmt.Sprintf("%.0f", p.OpsPerSec)
		}
		if p.SizeBefore > 0 {
			before = fmt.Sprint(p.SizeBefore)
		}
		fmt.Fprintf(&b, "| %s | %d | %.3f | %s | %s | %s | %d | %s | %d |\n", p.Name, p.Ops, p.Seconds, ops, p50, p99, p.AutoCompaction, before, p.SizeAfter)
	}
	b.WriteString("\nEvery run:\n\n| Run | Phase | Seconds | Ops/s |\n|---|---|---|---|\n")
	for _, rr := range r.Runs {
		for _, p := range rr.Phases {
			ops := "—"
			if p.OpsPerSec > 0 {
				ops = fmt.Sprintf("%.0f", p.OpsPerSec)
			}
			fmt.Fprintf(&b, "| %d | %s | %.3f | %s |\n", rr.Run, p.Name, p.Seconds, ops)
		}
	}
	b.WriteString("\n\"batch\" ops/s counts operations (n), not batches. \"reopen\" ops/s is keys recovered per second. Every write is synced before it returns.\n")
	return b.String()
}

func machine() map[string]string {
	m := map[string]string{
		"os":         runtime.GOOS + "/" + runtime.GOARCH,
		"go":         runtime.Version(),
		"cpus":       fmt.Sprint(runtime.NumCPU()),
		"cpu":        cpuName(),
		"os_version": osVersion(),
	}
	if h, err := os.Hostname(); err == nil {
		m["host"] = h
	}
	return m
}
