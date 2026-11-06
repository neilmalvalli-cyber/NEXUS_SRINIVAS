// Command torture tries to break kvdb: it runs `kvdb serve` as a child
// process, sends random puts, deletes, batches, gets and compactions from
// several concurrent writers, records every request and reply in its own
// oracle log, kills the child at a random millisecond (TerminateProcess on
// Windows, SIGKILL elsewhere), restarts with `kvdb open`, `kvdb check` and
// `kvdb dump`, and checks the recovered state against its independent model
// (see model.go). It stops at the first violation.
//
//	torture --iterations 500 --seed 12345
//	torture --iterations 100 --seed 999 --mode large-values
//	torture --iterations 100 --seed 7   --mode compaction
//	torture --iterations 100 --seed 42  --mode recovery-crash
//
// Modes: default, large-values (1 MiB values), large-batch (10,000-op
// batches against concurrent single writes), compaction, recovery-crash
// (kill `kvdb open` while it recovers), disk-full (small --max-bytes),
// concurrent-disjoint, same-key.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type config struct {
	iterations int
	seed       int64
	seedSet    bool
	mode       string
	kvdb       string
	dir        string
	report     string
	writers    int
	minKill    int
	maxKill    int
	maxBytes   int64
	quiet      bool
}

func main() { os.Exit(run(os.Args[1:])) }

func run(argv []string) int {
	var cfg config
	fs := flag.NewFlagSet("torture", flag.ContinueOnError)
	fs.IntVar(&cfg.iterations, "iterations", 500, "number of kill/recover/verify iterations")
	seed := fs.String("seed", "", "random seed (mandatory); the same seed replays the same requests and kill delays")
	fs.StringVar(&cfg.mode, "mode", "default", "workload: "+strings.Join(modes, ", "))
	fs.StringVar(&cfg.kvdb, "kvdb", "", "path to the kvdb binary (default: kvdb next to this program, then ./kvdb)")
	fs.StringVar(&cfg.dir, "dir", "", "work directory (default ./torture-runs/<mode>-seed<seed>); recreated on start")
	fs.StringVar(&cfg.report, "report", "", "JSON report path (default <dir>/report.json)")
	fs.IntVar(&cfg.writers, "writers", 0, "concurrent writers (default per mode, at least 2)")
	fs.IntVar(&cfg.minKill, "min-kill-ms", 0, "earliest kill, ms after serve starts (default per mode)")
	fs.IntVar(&cfg.maxKill, "max-kill-ms", 0, "latest kill, ms after serve starts (default per mode)")
	fs.Int64Var(&cfg.maxBytes, "max-bytes", -1, "storage cap passed to serve/open (default per mode; disk-full: 262144)")
	fs.BoolVar(&cfg.quiet, "quiet", false, "print only failures and the summary")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *seed == "" {
		fmt.Fprintln(os.Stderr, "torture: --seed is mandatory (for example --seed 12345)")
		return 2
	}
	if _, err := fmt.Sscan(*seed, &cfg.seed); err != nil {
		fmt.Fprintf(os.Stderr, "torture: --seed %q is not an integer\n", *seed)
		return 2
	}
	wl, err := modeWorkload(cfg.mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "torture:", err)
		return 2
	}
	if cfg.writers != 0 {
		if cfg.writers < 2 {
			fmt.Fprintln(os.Stderr, "torture: --writers must be at least 2")
			return 2
		}
		wl.writers = cfg.writers
	}
	if cfg.minKill > 0 {
		wl.minKillMs = cfg.minKill
	}
	if cfg.maxKill > 0 {
		wl.maxKillMs = cfg.maxKill
	}
	if wl.maxKillMs < wl.minKillMs {
		wl.maxKillMs = wl.minKillMs
	}
	if cfg.maxBytes >= 0 {
		wl.maxBytes = cfg.maxBytes
	}
	if cfg.kvdb == "" {
		cfg.kvdb = findKvdb()
	}
	if _, err := os.Stat(cfg.kvdb); err != nil {
		fmt.Fprintf(os.Stderr, "torture: kvdb binary not found at %s (build it: go build -o . ./src/cmd/kvdb ./torture)\n", cfg.kvdb)
		return 2
	}
	if cfg.dir == "" {
		cfg.dir = filepath.Join("torture-runs", fmt.Sprintf("%s-seed%d", cfg.mode, cfg.seed))
	}
	if err := prepareDir(cfg.dir); err != nil {
		fmt.Fprintln(os.Stderr, "torture:", err)
		return 2
	}
	if cfg.report == "" {
		cfg.report = filepath.Join(cfg.dir, "report.json")
	}
	of, err := os.Create(filepath.Join(cfg.dir, "oracle.jsonl"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "torture:", err)
		return 2
	}
	defer of.Close()
	rn := &runner{cfg: cfg, wl: wl, db: filepath.Join(cfg.dir, "db.kv"), baseline: map[string]string{},
		oracle: &oracleLog{f: of, w: bufio.NewWriterSize(of, 1<<20)}}

	fmt.Printf("torture: mode=%s seed=%d iterations=%d writers=%d kill=%d..%dms max-bytes=%d kvdb=%s dir=%s\n",
		cfg.mode, cfg.seed, cfg.iterations, wl.writers, wl.minKillMs, wl.maxKillMs, wl.maxBytes, cfg.kvdb, cfg.dir)
	if wl.preloadKeys > 0 {
		t := time.Now()
		if err := rn.preload(wl.preloadKeys, cfg.seed); err != nil {
			fmt.Fprintln(os.Stderr, "torture: preload:", err)
			return 1
		}
		b, err := rn.loadBaseline()
		if err != nil || len(b) != wl.preloadKeys {
			fmt.Fprintf(os.Stderr, "torture: preload verification: %d keys, err %v\n", len(b), err)
			return 1
		}
		rn.baseline = b
		fmt.Printf("torture: preloaded %d keys in %s\n", len(b), time.Since(t).Round(time.Millisecond))
	}
	if wl.recoverKill {
		if err := rn.measureOpen(); err != nil {
			fmt.Fprintln(os.Stderr, "torture:", err)
			return 1
		}
		fmt.Printf("torture: kvdb open takes about %.1f ms; recovery kills land uniformly in 0..%.1f ms\n", rn.openMs, rn.openMs*1.2)
	}

	rep := report{Mode: cfg.mode, Seed: cfg.seed, Iterations: cfg.iterations, Writers: wl.writers, MaxBytes: wl.maxBytes,
		KillWindowMs: [2]int{wl.minKillMs, wl.maxKillMs}, GoOS: runtime.GOOS, GoArch: runtime.GOARCH, Started: time.Now().Format(time.RFC3339)}
	start := time.Now()
	exit := 0
	for i := 1; i <= cfg.iterations; i++ {
		res, err := rn.iteration(i, iterSeed(cfg.seed, i))
		rn.oracle.write(map[string]any{"iter": i, "ev": "verdict", "pass": res.Pass})
		if ferr := rn.oracle.flush(); ferr != nil && err == nil {
			err = ferr
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "torture: iteration %d: tool error (not a database verdict): %v\n", i, err)
			rep.ToolError = err.Error()
			exit = 2
			break
		}
		rep.add(res)
		if !cfg.quiet || !res.Pass {
			fmt.Println(res.line(cfg.iterations))
		}
		if !res.Pass {
			fmt.Printf("\nFAIL at iteration %d (iteration seed %d, kill after %d ms)\n%s\n", i, res.Seed, res.KillAfterMs, res.FailureText)
			fmt.Printf("database kept at %s; oracle log %s\n", rn.db, filepath.Join(cfg.dir, "oracle.jsonl"))
			exit = 1
			break
		}
	}
	rep.ElapsedSec = time.Since(start).Seconds()
	if err := rep.write(cfg.report); err != nil {
		fmt.Fprintln(os.Stderr, "torture: writing report:", err)
	}
	verdict := "PASS"
	if exit != 0 {
		verdict = "FAIL"
	}
	fmt.Printf("\nRESULT: %s  passed=%d failed=%d of %d planned  mode=%s seed=%d  acked=%d rejected=%d unknown=%d  kills-mid-write=%d compact-in-flight=%d torn-tails=%d compaction-resolved=%d recovery-kills-landed=%d/%d  elapsed=%.1fs  report=%s\n",
		verdict, rep.Passed, rep.Failed, cfg.iterations, cfg.mode, cfg.seed, rep.Acked, rep.Rejected, rep.Unknown,
		rep.KillsMidWrite, rep.KillsMidCompaction, rep.TornTails, rep.AuthorityRules, rep.RecoveryLanded, rep.RecoveryKills, rep.ElapsedSec, cfg.report)
	return exit
}

// iterSeed derives a per-iteration seed (splitmix64), so iteration i can be
// identified and its requests regenerated from --seed alone.
func iterSeed(seed int64, i int) int64 {
	z := uint64(seed) + uint64(i)*0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return int64((z ^ (z >> 31)) >> 1)
}

func (r iterResult) line(total int) string {
	v := "PASS"
	if !r.Pass {
		v = "FAIL"
	}
	extra := ""
	if r.RecoveryKills > 0 {
		extra += fmt.Sprintf(" recovery-kills=%d/%d", r.RecoveryLanded, r.RecoveryKills)
	}
	if r.CompactInKill {
		extra += " compact-in-flight"
	}
	if r.TornTail {
		extra += " torn-tail-discarded"
	}
	if r.AuthorityRule {
		extra += " interrupted-compaction-resolved"
	}
	if len(r.StaleFiles) > 0 {
		extra += " stale=" + strings.Join(r.StaleFiles, ",")
	}
	if n := countClass(r.ErrClasses, "full"); n > 0 {
		extra += fmt.Sprintf(" full-errors=%d", n)
	}
	return fmt.Sprintf("[%4d/%d] %s kill@%dms sent=%d acked=%d rejected=%d unknown=%d gets=%d in-flight=%d keys=%d file=%dB open=%.1fms%s",
		r.Iter, total, v, r.KillAfterMs, r.Sent, r.Acked, r.Rejected, r.Unknown, r.Gets, len(r.InFlightAtKill), r.Keys, r.FileBytes, r.OpenMs, extra)
}

func countClass(cs []string, c string) int {
	n := 0
	for _, x := range cs {
		if x == c {
			n++
		}
	}
	return n
}

type report struct {
	Mode               string       `json:"mode"`
	Seed               int64        `json:"seed"`
	Iterations         int          `json:"iterations_planned"`
	Writers            int          `json:"writers"`
	MaxBytes           int64        `json:"max_bytes"`
	KillWindowMs       [2]int       `json:"kill_window_ms"`
	GoOS               string       `json:"goos"`
	GoArch             string       `json:"goarch"`
	Started            string       `json:"started"`
	ElapsedSec         float64      `json:"elapsed_sec"`
	Passed             int          `json:"passed"`
	Failed             int          `json:"failed"`
	Acked              int          `json:"acked_writes"`
	Rejected           int          `json:"rejected_writes"`
	Unknown            int          `json:"unknown_writes"`
	FullErrors         int          `json:"full_errors"`
	KillsMidWrite      int          `json:"kills_with_write_in_flight"`
	KillsMidCompaction int          `json:"kills_with_compaction_in_flight"`
	TornTails          int          `json:"recoveries_discarding_torn_tail"`
	AuthorityRules     int          `json:"recoveries_resolving_interrupted_compaction"`
	RecoveryKills      int          `json:"recovery_kills"`
	RecoveryLanded     int          `json:"recovery_kills_landed"`
	MaxOpenMs          float64      `json:"max_open_recovery_ms"`
	ToolError          string       `json:"tool_error,omitempty"`
	FirstFailure       *iterResult  `json:"first_failure,omitempty"`
	PerIteration       []iterResult `json:"per_iteration"`
}

func (rp *report) add(r iterResult) {
	if r.Pass {
		rp.Passed++
	} else {
		rp.Failed++
		if rp.FirstFailure == nil {
			c := r
			rp.FirstFailure = &c
		}
	}
	rp.Acked += r.Acked
	rp.Rejected += r.Rejected
	rp.Unknown += r.Unknown
	rp.FullErrors += countClass(r.ErrClasses, "full")
	for _, f := range r.InFlightAtKill {
		if !strings.HasSuffix(f, ":get") {
			rp.KillsMidWrite++
			break
		}
	}
	if r.CompactInKill {
		rp.KillsMidCompaction++
	}
	if r.TornTail {
		rp.TornTails++
	}
	if r.AuthorityRule {
		rp.AuthorityRules++
	}
	rp.RecoveryKills += r.RecoveryKills
	rp.RecoveryLanded += r.RecoveryLanded
	if r.OpenMs > rp.MaxOpenMs {
		rp.MaxOpenMs = r.OpenMs
	}
	r.ErrClasses = nil // keep the per-iteration list short
	rp.PerIteration = append(rp.PerIteration, r)
}

func (rp *report) write(path string) error {
	b, err := json.MarshalIndent(rp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func findKvdb() string {
	name := "kvdb"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	p, _ := filepath.Abs(name)
	return p
}

// marker identifies a directory created by this tool, so it is the only
// kind of directory the tool ever empties.
const marker = ".torture-run"

func prepareDir(dir string) error {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
			return fmt.Errorf("work directory %s exists and was not created by torture; choose another --dir", dir)
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("clearing previous run in %s: %w", dir, err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, marker), []byte("created by the torture tool; safe to delete\n"), 0o644)
}
