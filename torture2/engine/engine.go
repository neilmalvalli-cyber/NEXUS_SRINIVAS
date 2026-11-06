// Package engine is the randomized crash loop of NEXUS Torture Engine 2.0:
// start the real `kvdb serve`, drive it from several concurrent writers,
// kill it at a random moment, restart it through the real CLI, and judge the
// recovered state with the independent oracle. PASS is printed only for an
// iteration whose recovery succeeded and whose oracle verdict is PASS.
package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/oracle"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/proc"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/workload"
)

// Config is one run.
type Config struct {
	Mode         workload.Mode
	Seed         int64
	FirstIter    int // first iteration number (replay: the failing iteration)
	Iterations   int
	Kvdb         proc.Binary
	Dir          string // work directory; created; must be empty or ours
	FromDB       string // start from this database file instead of an empty one
	ReplyTimeout time.Duration
	CmdTimeout   time.Duration
	Limits       oracle.Limits
	Quiet        bool
	StopOnFail   bool
	Out          io.Writer
}

// Marker identifies directories created by the engine; it only ever empties
// a directory that carries it.
const Marker = ".nexus-torture-run"

// IterResult is one line of results.jsonl.
type IterResult struct {
	Iter          int               `json:"iter"`
	IterSeed      int64             `json:"iter_seed"`
	KillAfterMs   int               `json:"kill_after_ms"`
	KillPoint     string            `json:"kill_point"` // what was in flight
	Ended         string            `json:"ended"`      // "kill", "close", "kill-during-close"
	Sent          int               `json:"sent"`
	Acked         int               `json:"acked"`        // acknowledged writes and compactions
	AckedWrites   int               `json:"acked_writes"` // acknowledged put/del/batch (data writes)
	Rejected      int               `json:"rejected"`
	NotSent       int               `json:"not_sent,omitempty"` // generated but not written: the graceful close came first
	Unknown       int               `json:"unknown"`
	Gets          int               `json:"gets"`
	ErrClasses    map[string]int    `json:"err_classes,omitempty"`
	RecoveryKills int               `json:"recovery_kills,omitempty"`
	RecoveryLand  int               `json:"recovery_kills_landed,omitempty"`
	Recovery      Recovery          `json:"recovery"`
	Keys          int               `json:"keys"`
	ServePeakMem  uint64            `json:"serve_peak_working_set_bytes"`
	OracleMs      float64           `json:"oracle_ms"`
	Oracle        oracle.Stats      `json:"oracle_stats"`
	Verdict       string            `json:"verdict"`                  // PASS, FAIL, INCONCLUSIVE
	OracleVerdict string            `json:"oracle_verdict,omitempty"` // the oracle's own verdict (also when another check failed first)
	FailKind      string            `json:"fail_kind,omitempty"`
	Failure       string            `json:"failure,omitempty"`
	Violation     *oracle.Violation `json:"violation,omitempty"`
	Repro         string            `json:"repro,omitempty"`
	Evidence      string            `json:"evidence_dir,omitempty"`
	DurationMs    float64           `json:"duration_ms"`
}

// Summary is summary.json.
type Summary struct {
	Tool           string       `json:"tool"`
	Mode           string       `json:"mode"`
	Seed           int64        `json:"seed"`
	FirstIter      int          `json:"first_iteration"`
	Planned        int          `json:"iterations_planned"`
	Completed      int          `json:"iterations_completed"`
	Passed         int          `json:"passed"`
	Failed         int          `json:"failed"`
	Inconclusive   int          `json:"inconclusive"`
	ToolError      string       `json:"tool_error,omitempty"`
	Result         string       `json:"result"`
	Kvdb           string       `json:"kvdb"`
	Env            []string     `json:"kvdb_env,omitempty"`
	GoOS           string       `json:"goos"`
	Started        string       `json:"started"`
	Acked          int          `json:"acked_writes"`
	Rejected       int          `json:"rejected_writes"`
	Unknown        int          `json:"unknown_writes"`
	Gets           int          `json:"gets_verified"`
	FullErrors     int          `json:"full_errors"`
	KillsMidWrite  int          `json:"kills_with_write_in_flight"`
	KillsMidCompac int          `json:"kills_with_compaction_in_flight"`
	Closes         int          `json:"graceful_closes"`
	KillsInClose   int          `json:"kills_during_close"`
	TornTails      int          `json:"recoveries_discarding_torn_tail"`
	Authority      int          `json:"recoveries_resolving_interrupted_compaction"`
	RecoveryKills  int          `json:"recovery_kills"`
	RecoveryLanded int          `json:"recovery_kills_landed"`
	Verification   Verification `json:"verification"`
	Perf           Perf         `json:"performance"`
	FirstFailure   *IterResult  `json:"first_failure,omitempty"`
	Inconclusives  []int        `json:"inconclusive_iterations,omitempty"`
}

// Verification is the correctness evidence of a run, in one place.
type Verification struct {
	OpsAttempted     int            `json:"operations_attempted"`                       // requests written to kvdb serve (writes, gets, compactions)
	OpsNotSent       int            `json:"operations_not_sent"`                        // generated after a graceful close had closed the input
	Acked            int            `json:"acknowledged"`                               // complete ACK / VALUE / NOTFOUND replies
	AckedWrites      int            `json:"acknowledged_writes"`                        // acknowledged put / del / batch
	Rejected         int            `json:"cleanly_rejected"`                           // definite ERR class: nothing changed
	Unknown          int            `json:"unknown_outcome"`                            // no reply before the kill, or ERR unknown/io
	AckedWritesVerif int            `json:"acknowledged_writes_verified_after_restart"` // in iterations the oracle accepted
	ReadsVerified    int            `json:"reads_verified"`                             // acknowledged gets in iterations the oracle accepted
	BatchViolations  int            `json:"batch_atomicity_violations"`
	IntegrityViol    int            `json:"data_integrity_violations"` // every other oracle violation
	RecoveryFailures int            `json:"recovery_failures"`         // check --no-repair / open / check / dump misbehaved
	OtherFailures    int            `json:"protocol_or_liveness_failures"`
	OracleInconcl    int            `json:"oracle_inconclusive"`
	ViolationsByKind map[string]int `json:"violations_by_kind,omitempty"`
	Failures         []FailureRef   `json:"failures,omitempty"`
}

// FailureRef locates one failing iteration and how to replay it.
type FailureRef struct {
	Iter     int    `json:"iter"`
	IterSeed int64  `json:"iter_seed"`
	Kind     string `json:"kind"`
	Repro    string `json:"repro"`
	Evidence string `json:"evidence_dir,omitempty"`
}

func (v *Verification) add(r *IterResult) {
	v.OpsAttempted += r.Sent
	v.OpsNotSent += r.NotSent
	v.Acked += r.Acked + r.Gets
	v.AckedWrites += r.AckedWrites
	v.Rejected += r.Rejected
	v.Unknown += r.Unknown
	if r.OracleVerdict == "PASS" {
		v.AckedWritesVerif += r.AckedWrites
		v.ReadsVerified += r.Gets
	}
	if r.OracleVerdict == "INCONCLUSIVE" {
		v.OracleInconcl++
	}
	if r.Violation != nil {
		if v.ViolationsByKind == nil {
			v.ViolationsByKind = map[string]int{}
		}
		v.ViolationsByKind[string(r.Violation.Kind)]++
		if r.Violation.Kind == oracle.VPartialBatch {
			v.BatchViolations++
		} else {
			v.IntegrityViol++
		}
	}
	switch {
	case r.FailKind == "recovery":
		v.RecoveryFailures++
	case r.Verdict == "FAIL" && r.Violation == nil:
		v.OtherFailures++
	}
	if r.Verdict == "FAIL" {
		v.Failures = append(v.Failures, FailureRef{Iter: r.Iter, IterSeed: r.IterSeed, Kind: r.FailKind, Repro: r.Repro, Evidence: r.Evidence})
	}
}

// Perf collects the performance measurements.
type Perf struct {
	TotalSec        float64 `json:"total_sec"`
	ItersPerSec     float64 `json:"iterations_per_sec"`
	RestartMsAvg    float64 `json:"restart_ms_avg"`
	RestartMsMax    float64 `json:"restart_ms_max"`
	RecoveryMsAvg   float64 `json:"recovery_ms_avg"`
	RecoveryMsMax   float64 `json:"recovery_ms_max"`
	OracleMsAvg     float64 `json:"oracle_ms_avg"`
	OracleMsMax     float64 `json:"oracle_ms_max"`
	VerifyMsAvg     float64 `json:"verify_ms_avg"` // read-only check + check + dump
	ServePeakMemMax uint64  `json:"serve_peak_working_set_max_bytes"`
	TesterHeapMax   uint64  `json:"tester_heap_max_bytes"`
	TesterSysMax    uint64  `json:"tester_sys_max_bytes"`
}

type runner struct {
	cfg      Config
	db       string
	baseline oracle.State
	events   *Events
	results  *os.File
	openMs   float64 // typical open time (recovery-crash kill window)
}

// Run executes the configured iterations and returns the summary. The
// error is a tool error (the run could not be carried out), never a verdict.
func Run(cfg Config) (Summary, error) {
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}
	if cfg.FirstIter <= 0 {
		cfg.FirstIter = 1
	}
	if cfg.ReplyTimeout <= 0 {
		cfg.ReplyTimeout = 60 * time.Second
	}
	if cfg.CmdTimeout <= 0 {
		cfg.CmdTimeout = 120 * time.Second
	}
	if cfg.Limits.MaxSteps == 0 {
		cfg.Limits = oracle.DefaultLimits()
	}
	sum := Summary{Tool: "nexus-torture 2.0", Mode: cfg.Mode.Name, Seed: cfg.Seed, FirstIter: cfg.FirstIter, Planned: cfg.Iterations,
		Kvdb: cfg.Kvdb.Path, Env: cfg.Kvdb.Env, GoOS: runtime.GOOS, Started: time.Now().Format(time.RFC3339)}
	if err := PrepareDir(cfg.Dir); err != nil {
		return sum, err
	}
	ev, err := NewEvents(filepath.Join(cfg.Dir, "events.jsonl"))
	if err != nil {
		return sum, err
	}
	defer ev.Close()
	resf, err := os.Create(filepath.Join(cfg.Dir, "results.jsonl"))
	if err != nil {
		return sum, err
	}
	defer resf.Close()
	rn := &runner{cfg: cfg, db: filepath.Join(cfg.Dir, "db.kv"), baseline: oracle.State{}, events: ev, results: resf}
	m := cfg.Mode
	fmt.Fprintf(cfg.Out, "nexus-torture: mode=%s seed=%d iterations=%d..%d writers=%d readers=%d kill=%d..%dms max-bytes=%d\n  kvdb=%s dir=%s\n",
		m.Name, cfg.Seed, cfg.FirstIter, cfg.FirstIter+cfg.Iterations-1, m.Writers, m.Readers, m.MinKillMs, m.MaxKillMs, m.MaxBytes, cfg.Kvdb.Path, cfg.Dir)

	if err := rn.setup(); err != nil {
		return sum, err
	}
	t0 := time.Now()
	var restart, recov, orc, verify []float64
	var memStats runtime.MemStats
	for i := cfg.FirstIter; i < cfg.FirstIter+cfg.Iterations; i++ {
		res, terr := rn.iteration(i)
		if terr != nil {
			sum.ToolError = fmt.Sprintf("iteration %d: %v", i, terr)
			fmt.Fprintf(cfg.Out, "nexus-torture: TOOL ERROR (not a verdict on NEXUS): %s\n", sum.ToolError)
			break
		}
		sum.Completed++
		b, _ := json.Marshal(res)
		resf.Write(append(b, '\n'))
		switch res.Verdict {
		case "PASS":
			sum.Passed++
		case "FAIL":
			sum.Failed++
			if sum.FirstFailure == nil {
				c := res
				sum.FirstFailure = &c
			}
		default:
			sum.Inconclusive++
			sum.Inconclusives = append(sum.Inconclusives, i)
		}
		sum.Verification.add(&res)
		sum.Acked += res.Acked
		sum.Rejected += res.Rejected
		sum.Unknown += res.Unknown
		sum.Gets += res.Gets
		sum.FullErrors += res.ErrClasses["full"]
		if strings.Contains(res.KillPoint, "put") || strings.Contains(res.KillPoint, "del") || strings.Contains(res.KillPoint, "batch") {
			sum.KillsMidWrite++
		}
		if strings.Contains(res.KillPoint, "compact") {
			sum.KillsMidCompac++
		}
		switch res.Ended {
		case "close":
			sum.Closes++
		case "kill-during-close":
			sum.KillsInClose++
		}
		if res.Recovery.TornTail {
			sum.TornTails++
		}
		if res.Recovery.Authority {
			sum.Authority++
		}
		sum.RecoveryKills += res.RecoveryKills
		sum.RecoveryLanded += res.RecoveryLand
		restart = append(restart, res.Recovery.RestartMs)
		recov = append(recov, res.Recovery.RecoveryMs)
		orc = append(orc, res.OracleMs)
		verify = append(verify, res.Recovery.ReadOnlyMs+res.Recovery.CheckMs+res.Recovery.DumpMs)
		if res.ServePeakMem > sum.Perf.ServePeakMemMax {
			sum.Perf.ServePeakMemMax = res.ServePeakMem
		}
		runtime.ReadMemStats(&memStats)
		if memStats.HeapAlloc > sum.Perf.TesterHeapMax {
			sum.Perf.TesterHeapMax = memStats.HeapAlloc
		}
		if memStats.Sys > sum.Perf.TesterSysMax {
			sum.Perf.TesterSysMax = memStats.Sys
		}
		if !cfg.Quiet || res.Verdict != "PASS" {
			fmt.Fprintln(cfg.Out, res.Line(cfg.FirstIter+cfg.Iterations-1))
		}
		if res.Verdict == "FAIL" {
			fmt.Fprintf(cfg.Out, "\nFAIL at iteration %d (iteration seed %d, killed after %d ms with %s in flight)\n%s\n", i, res.IterSeed, res.KillAfterMs, res.KillPoint, res.Failure)
			fmt.Fprintf(cfg.Out, "evidence: %s\nreproduce: %s\n", res.Evidence, res.Repro)
			if cfg.StopOnFail {
				break
			}
		}
	}
	p := &sum.Perf
	p.TotalSec = time.Since(t0).Seconds()
	if p.TotalSec > 0 {
		p.ItersPerSec = float64(sum.Completed) / p.TotalSec
	}
	p.RestartMsAvg, p.RestartMsMax = avgMax(restart)
	p.RecoveryMsAvg, p.RecoveryMsMax = avgMax(recov)
	p.OracleMsAvg, p.OracleMsMax = avgMax(orc)
	p.VerifyMsAvg, _ = avgMax(verify)
	switch {
	case sum.ToolError != "":
		sum.Result = "ERROR"
	case sum.Failed > 0:
		sum.Result = "FAIL"
	case sum.Inconclusive > 0 || sum.Completed < sum.Planned:
		sum.Result = "INCONCLUSIVE"
	default:
		sum.Result = "PASS"
	}
	b, _ := json.MarshalIndent(sum, "", "  ")
	_ = os.WriteFile(filepath.Join(cfg.Dir, "summary.json"), b, 0o644)
	fmt.Fprintf(cfg.Out, "\nRESULT: %s  mode=%s seed=%d  passed=%d failed=%d inconclusive=%d of %d  acked=%d rejected=%d unknown=%d gets=%d  kills-mid-write=%d compact-in-flight=%d torn-tails=%d interrupted-compactions=%d recovery-kills=%d/%d closes=%d kills-in-close=%d\n",
		sum.Result, sum.Mode, sum.Seed, sum.Passed, sum.Failed, sum.Inconclusive, sum.Planned, sum.Acked, sum.Rejected, sum.Unknown, sum.Gets,
		sum.KillsMidWrite, sum.KillsMidCompac, sum.TornTails, sum.Authority, sum.RecoveryLanded, sum.RecoveryKills, sum.Closes, sum.KillsInClose)
	vf := sum.Verification
	fmt.Fprintf(cfg.Out, "VERIFY: attempted=%d acknowledged=%d (writes %d) cleanly-rejected=%d unknown=%d  acked-writes-verified-after-restart=%d reads-verified=%d  batch-atomicity-violations=%d data-integrity-violations=%d recovery-failures=%d protocol/liveness-failures=%d oracle-inconclusive=%d\n",
		vf.OpsAttempted, vf.Acked, vf.AckedWrites, vf.Rejected, vf.Unknown, vf.AckedWritesVerif, vf.ReadsVerified, vf.BatchViolations, vf.IntegrityViol, vf.RecoveryFailures, vf.OtherFailures, vf.OracleInconcl)
	for _, f := range vf.Failures {
		fmt.Fprintf(cfg.Out, "  failure: iteration %d (iteration seed %d) %s; reproduce: %s\n", f.Iter, f.IterSeed, f.Kind, f.Repro)
	}
	fmt.Fprintf(cfg.Out, "PERF: %.1fs total, %.2f iterations/s, restart %.1f ms avg / %.1f max, recovery %.2f ms avg / %.2f max, oracle %.2f ms avg / %.1f max, verify %.1f ms avg, serve peak %s, tester heap peak %s\n",
		p.TotalSec, p.ItersPerSec, p.RestartMsAvg, p.RestartMsMax, p.RecoveryMsAvg, p.RecoveryMsMax, p.OracleMsAvg, p.OracleMsMax, p.VerifyMsAvg, mib(p.ServePeakMemMax), mib(p.TesterHeapMax))
	fmt.Fprintf(cfg.Out, "report: %s\n", filepath.Join(cfg.Dir, "summary.json"))
	return sum, nil
}

func (rn *runner) setup() error {
	c := rn.cfg
	switch {
	case c.FromDB != "":
		b, err := os.ReadFile(c.FromDB)
		if err != nil {
			return fmt.Errorf("--from-db: %w", err)
		}
		if err := os.WriteFile(rn.db, b, 0o644); err != nil {
			return err
		}
		rc := Recover(c.Kvdb, rn.db, c.Mode.MaxBytes, c.CmdTimeout)
		if rc.Problem != "" {
			return fmt.Errorf("--from-db %s does not recover cleanly: %s", c.FromDB, rc.Problem)
		}
		rn.baseline = rc.State
		fmt.Fprintf(c.Out, "nexus-torture: starting from %s (%d keys)\n", c.FromDB, len(rc.State))
	case c.Mode.PreloadKeys > 0:
		t := time.Now()
		files, want := workload.Preload(c.Mode.PreloadKeys, c.Seed)
		f := filepath.Join(c.Dir, "preload.txt")
		for _, b := range files {
			if err := os.WriteFile(f, b, 0o644); err != nil {
				return err
			}
			if r := c.Kvdb.Run(c.CmdTimeout, nil, "batch", rn.db, f); !r.OK() {
				return fmt.Errorf("preload batch: %s", r.String())
			}
		}
		os.Remove(f)
		st, _, err := Dump(c.Kvdb, rn.db, c.CmdTimeout)
		if err != nil {
			return err
		}
		if d := stateDiff(want, st); d != "" {
			return fmt.Errorf("preload verification failed: %s", d)
		}
		rn.baseline = st
		fmt.Fprintf(c.Out, "nexus-torture: preloaded %d keys in %s (verified)\n", len(st), time.Since(t).Round(time.Millisecond))
	}
	if c.Mode.RecoveryKills {
		best := 0.0
		for i := 0; i < 3; i++ {
			r := c.Kvdb.Run(c.CmdTimeout, nil, "open", rn.db)
			if !r.OK() {
				return fmt.Errorf("open: %s", r.String())
			}
			if d := ms(r.Dur); d > best {
				best = d
			}
		}
		rn.openMs = best
		fmt.Fprintf(c.Out, "nexus-torture: kvdb open takes about %.1f ms; recovery kills land uniformly in 0..%.1f ms\n", best, best*1.2)
	}
	return nil
}

// definite error classes: the request certainly did not take effect.
var definite = map[string]bool{"full": true, "invalid": true, "too-large": true, "failed": true, "closed": true, "protocol": true}

func (rn *runner) allowedClass(class string) bool {
	switch class {
	case "unknown", "failed":
		return true
	case "full":
		return rn.cfg.Mode.MaxBytes > 0
	}
	return false
}

// iteration runs one start-drive-kill-recover-verify cycle.
func (rn *runner) iteration(iter int) (IterResult, error) {
	c := rn.cfg
	m := c.Mode
	t0 := time.Now()
	iterSeed := workload.IterSeed(c.Seed, iter)
	sched := rand.New(rand.NewSource(workload.StreamSeed(iterSeed, workload.StreamSchedule)))
	res := IterResult{Iter: iter, IterSeed: iterSeed, ErrClasses: map[string]int{}}
	res.KillAfterMs = m.MinKillMs + sched.Intn(m.MaxKillMs-m.MinKillMs+1)
	graceful := sched.Float64() < m.CloseP
	closeKillMs := -1
	if graceful && sched.Intn(2) == 0 {
		closeKillMs = sched.Intn(15)
	}
	evDir := filepath.Join(c.Dir, "evidence")
	preDir := filepath.Join(evDir, "pre")
	os.RemoveAll(evDir)
	if err := CopyFiles(rn.db, preDir); err != nil {
		return res, err
	}

	start := time.Now()
	clock := func() int64 { return time.Since(start).Nanoseconds() }
	hist := oracle.NewHistory(clock)
	sess, err := c.Kvdb.StartServe(rn.db, maxBytesArgs(m.MaxBytes), clock)
	if err != nil {
		return res, err
	}
	rn.events.Write(map[string]any{"ev": "start", "iter": iter, "iter_seed": iterSeed, "pid": sess.Pid(), "kill_after_ms": res.KillAfterMs})

	var stop atomic.Bool
	var wg sync.WaitGroup
	var toolErr atomic.Value
	var timeouts atomic.Int32
	var protocolErr atomic.Value
	streams := m.Writers + m.Readers
	for w := 0; w < streams; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			gen := workload.NewWriter(m, iter, iterSeed, w)
			for seq := 0; !stop.Load(); seq++ {
				req := gen.Next(seq)
				if err := hist.Begin(req.Op); err != nil {
					toolErr.Store(err)
					return
				}
				rn.events.Send(iter, req.Op)
				ch, err := sess.Send(req.Op.ID, req.Payload)
				if errors.Is(err, proc.ErrInputClosed) {
					// The graceful close won the race: not one byte was sent.
					hist.Finish(req.Op, oracle.Rejected, notSent)
					rn.events.Write(map[string]any{"ev": "not-sent", "iter": iter, "op": req.Op.ID})
					return
				}
				if ch == nil {
					return // output already ended: stays in flight -> unknown
				}
				timer := time.NewTimer(c.ReplyTimeout)
				var rp proc.Reply
				var ok bool
				select {
				case rp, ok = <-ch:
					timer.Stop()
				case <-timer.C:
					timeouts.Add(1)
					rn.events.Write(map[string]any{"ev": "reply-timeout", "iter": iter, "op": req.Op.ID, "timeout_s": c.ReplyTimeout.Seconds()})
					return // stays in flight -> unknown; the iteration fails on liveness
				}
				if !ok {
					return // the process died first: unknown
				}
				st, errText := classify(req.Op, rp)
				if st == oracle.Pending {
					protocolErr.Store(fmt.Sprintf("%s (%s) got an impossible reply %s %s", req.Op.ID, req.Op.Kind, rp.Kind, rp.Class))
					st = oracle.Unknown
				}
				if req.Op.Kind == oracle.KGet && rp.Kind == "VALUE" {
					req.Op.Found, req.Op.Read = true, oracle.HashValue(rp.Value)
				}
				if err := hist.Finish(req.Op, st, errText); err != nil {
					toolErr.Store(err)
					return
				}
				// Use the reader goroutine's timestamp of the complete line.
				if st == oracle.Acked || st == oracle.Rejected {
					req.Op.End = rp.At
				}
				rn.events.Result(iter, req.Op)
			}
		}(w)
	}

	// The kill (or graceful close): a random moment independent of how many
	// requests have run.
	time.Sleep(time.Until(start.Add(time.Duration(res.KillAfterMs) * time.Millisecond)))
	var inflight []string
	for _, o := range hist.InFlight() {
		inflight = append(inflight, o.Kind.String())
	}
	sort.Strings(inflight)
	res.KillPoint = strings.Join(inflight, "+")
	if res.KillPoint == "" {
		res.KillPoint = "idle"
	}
	res.ServePeakMem = sess.PeakMemory()
	if graceful {
		stop.Store(true)
		sess.CloseInput()
		res.Ended = "close"
		if closeKillMs >= 0 {
			time.Sleep(time.Duration(closeKillMs) * time.Millisecond)
			if sess.Kill() {
				res.Ended = "kill-during-close"
			}
		}
	} else {
		res.Ended = "kill"
		if !sess.Kill() {
			return res, rn.finishEarly(&res, sess, "serve exited before the kill: "+trunc(sess.Stderr(), 400), t0, &wg, &stop)
		}
		stop.Store(true)
	}
	exit, exited := sess.Wait(30 * time.Second)
	if !exited {
		sess.Kill()
		sess.Wait(10 * time.Second)
		rn.fail(&res, "liveness", fmt.Sprintf("serve did not exit within 30 s after %s", res.Ended))
	}
	sess.Release()
	if !waitTimeout(&wg, 30*time.Second) {
		return res, errors.New("writers did not stop within 30 s after the process ended (tool bug)")
	}
	if e := toolErr.Load(); e != nil {
		return res, e.(error)
	}
	lost := hist.Crash()
	rn.events.Write(map[string]any{"ev": "end", "iter": iter, "how": res.Ended, "exit": exit, "in_flight": res.KillPoint, "unknown_at_end": len(lost)})
	if res.Ended == "close" && exit != 0 && res.Failure == "" {
		rn.fail(&res, "close", fmt.Sprintf("serve exited with status %d after a graceful close; stderr: %s", exit, trunc(sess.Stderr(), 400)))
	}
	if res.Ended == "close" && len(lost) > 0 && res.Failure == "" {
		var ids []string
		for _, o := range lost {
			ids = append(ids, o.ID)
		}
		rn.fail(&res, "close", "graceful close finished without replying to "+strings.Join(ids, ", "))
	}
	if n := timeouts.Load(); n > 0 && res.Failure == "" {
		rn.fail(&res, "liveness", fmt.Sprintf("%d requests got no reply within %s while serve was running", n, c.ReplyTimeout))
	}
	if s := sess.Stray(); len(s) > 0 && res.Failure == "" {
		rn.fail(&res, "protocol", "unexpected serve output: "+strings.Join(s, " | "))
	}
	if p := protocolErr.Load(); p != nil && res.Failure == "" {
		rn.fail(&res, "protocol", p.(string))
	}

	ops := hist.Ops()
	for _, o := range ops {
		if o.Err == notSent {
			res.NotSent++ // never written to the process; the oracle treats it as rejected
			continue
		}
		res.Sent++
		switch {
		case o.Kind == oracle.KGet:
			if o.Status == oracle.Acked {
				res.Gets++
			}
		case o.Status == oracle.Acked:
			res.Acked++
			if o.Kind != oracle.KCompact {
				res.AckedWrites++
			}
		case o.Status == oracle.Rejected:
			res.Rejected++
		default:
			res.Unknown++
		}
		if o.Err != "" {
			cls, _, _ := strings.Cut(o.Err, " ")
			res.ErrClasses[cls]++
			if !rn.allowedClass(cls) && res.Failure == "" {
				rn.fail(&res, "error-class", fmt.Sprintf("%s (%s) failed with class %q, which a correct database must not produce here: %s", o.ID, o.Kind, cls, o.Err))
			}
		}
	}
	if err := CopyFiles(rn.db, filepath.Join(evDir, "after-kill")); err != nil {
		return res, err
	}

	// Kill `kvdb open` during recovery (recovery-crash mode).
	if m.RecoveryKills {
		n := 1 + sched.Intn(3)
		for k := 0; k < n; k++ {
			delay := time.Duration(sched.Float64()*rn.openMs*1.2*1e6) * time.Nanosecond
			landed, _ := c.Kvdb.RunKillAfter(delay, append([]string{"open", rn.db}, maxBytesArgs(m.MaxBytes)...)...)
			res.RecoveryKills++
			if landed {
				res.RecoveryLand++
			}
			rn.events.Write(map[string]any{"ev": "kill-open", "iter": iter, "delay_ms": ms(delay), "landed": landed})
		}
	}

	// Recovery through the real CLI.
	res.Recovery = Recover(c.Kvdb, rn.db, m.MaxBytes, c.CmdTimeout)
	rn.events.Write(map[string]any{"ev": "recovery", "iter": iter, "recovery": res.Recovery})
	if res.Recovery.Problem != "" && res.Failure == "" {
		rn.fail(&res, "recovery", res.Recovery.Problem)
	}

	// The oracle. Never skipped: an iteration is PASS only if this says PASS.
	if res.Recovery.Problem == "" {
		final := res.Recovery.State
		res.Keys = len(final)
		ot := time.Now()
		v := oracle.Check(rn.baseline, final, ops, c.Limits)
		res.OracleMs = ms(time.Since(ot))
		res.Oracle = v.Stats
		rn.events.Write(map[string]any{"ev": "verdict", "iter": iter, "oracle": v.Result.String(), "stats": v.Stats, "violation": v.Violation, "reason": v.Reason})
		res.OracleVerdict = v.Result.String()
		if v.Result == oracle.Fail {
			res.Violation = v.Violation // counted even if another check failed the iteration first
		}
		switch {
		case res.Failure != "":
			// already failed (protocol, liveness, close ...); keep that
		case v.Result == oracle.Fail:
			res.Violation = v.Violation
			rn.fail(&res, string(v.Violation.Kind), v.Violation.String())
			rn.minimize(iter, final, ops, v.Violation)
		case v.Result == oracle.Inconclusive:
			res.Verdict = "INCONCLUSIVE"
			res.Failure = "oracle: " + v.Reason
		default:
			res.Verdict = "PASS"
		}
		// The next iteration starts from the database as recovered, whatever
		// the verdict: judging it against an older state would turn one
		// failure into a cascade of false ones under --keep-going.
		rn.baseline = final
	}
	if res.Verdict == "FAIL" {
		rn.keepEvidence(&res, ops)
	}
	res.DurationMs = ms(time.Since(t0))
	if err := rn.events.Flush(); err != nil {
		return res, err
	}
	return res, nil
}

func (rn *runner) fail(res *IterResult, kind, text string) {
	if res.Verdict == "FAIL" {
		return
	}
	res.Verdict, res.FailKind, res.Failure = "FAIL", kind, text
}

func (rn *runner) finishEarly(res *IterResult, sess *proc.Session, why string, t0 time.Time, wg *sync.WaitGroup, stop *atomic.Bool) error {
	stop.Store(true)
	sess.Wait(10 * time.Second)
	sess.Release()
	waitTimeout(wg, 30*time.Second)
	return errors.New(why)
}

// classify maps a reply to a status. Pending means the reply cannot answer
// this request (a protocol violation).
func classify(o *oracle.Op, rp proc.Reply) (oracle.Status, string) {
	switch rp.Kind {
	case "ACK":
		if o.Kind == oracle.KGet {
			return oracle.Pending, ""
		}
		return oracle.Acked, ""
	case "VALUE", "NOTFOUND":
		if o.Kind != oracle.KGet {
			return oracle.Pending, ""
		}
		return oracle.Acked, ""
	case "ERR":
		text := rp.Class + " " + rp.Text
		if definite[rp.Class] {
			return oracle.Rejected, text
		}
		return oracle.Unknown, text // "unknown", "io", anything else: may or may not have happened
	}
	return oracle.Pending, ""
}

func (rn *runner) keepEvidence(res *IterResult, ops []*oracle.Op) {
	dir := filepath.Join(rn.cfg.Dir, "failures", fmt.Sprintf("iter-%d", res.Iter))
	os.RemoveAll(dir)
	os.MkdirAll(filepath.Dir(dir), 0o755)
	if err := os.Rename(filepath.Join(rn.cfg.Dir, "evidence"), dir); err != nil {
		dir = filepath.Join(rn.cfg.Dir, "evidence")
	}
	WriteHistory(filepath.Join(dir, "history.json"), rn.baseline, res.Recovery.State, ops)
	os.WriteFile(filepath.Join(dir, "failure.txt"), []byte(res.Failure+"\n"), 0o644)
	res.Evidence = dir
	exe, _ := os.Executable()
	from := ""
	if pre := filepath.Join(dir, "pre", filepath.Base(rn.db)); fileExists(pre) {
		from = " --from-db " + shellQuote(pre)
	} // no pre copy: the iteration started from an empty database
	res.Repro = fmt.Sprintf("%s run --mode %s --seed %d --iteration %d%s --kvdb %s",
		shellQuote(exe), rn.cfg.Mode.Name, rn.cfg.Seed, res.Iter, from, shellQuote(rn.cfg.Kvdb.Path))
	if len(rn.cfg.Kvdb.Env) > 0 {
		res.Repro = strings.Join(rn.cfg.Kvdb.Env, " ") + " " + res.Repro
	}
}

func (rn *runner) minimize(iter int, final oracle.State, ops []*oracle.Op, v *oracle.Violation) {
	lim := rn.cfg.Limits
	lim.MaxDuration = 20 * time.Second
	b, f, h := oracle.Minimize(rn.baseline, final, ops, v, lim, 400)
	dir := filepath.Join(rn.cfg.Dir, "evidence")
	os.MkdirAll(dir, 0o755)
	WriteHistory(filepath.Join(dir, "minimized.json"), b, f, h)
	rn.events.Write(map[string]any{"ev": "minimized", "iter": iter, "ops_before": len(ops), "ops_after": len(h), "keys_after": len(f)})
}

func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// Line is the one-line terminal form of an iteration.
func (r IterResult) Line(last int) string {
	extra := ""
	if r.RecoveryKills > 0 {
		extra += fmt.Sprintf(" recovery-kills=%d/%d", r.RecoveryLand, r.RecoveryKills)
	}
	if r.Recovery.TornTail {
		extra += " torn-tail-cut"
	}
	if r.Recovery.Authority {
		extra += " interrupted-compaction-resolved"
	}
	if n := r.ErrClasses["full"]; n > 0 {
		extra += fmt.Sprintf(" full=%d", n)
	}
	if r.Ended != "kill" {
		extra += " " + r.Ended
	}
	return fmt.Sprintf("[%4d/%d] %-4s kill@%dms in-flight=%s sent=%d acked=%d rejected=%d unknown=%d gets=%d keys=%d file=%dB restart=%.1fms recovery=%.2fms oracle=%.1fms%s",
		r.Iter, last, r.Verdict, r.KillAfterMs, r.KillPoint, r.Sent, r.Acked, r.Rejected, r.Unknown, r.Gets, r.Keys, r.Recovery.FileBytes,
		r.Recovery.RestartMs, r.Recovery.RecoveryMs, r.OracleMs, extra)
}

// PrepareDir creates dir, or empties it if a previous engine run created it.
func PrepareDir(dir string) error {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		if _, err := os.Stat(filepath.Join(dir, Marker)); err != nil {
			return fmt.Errorf("work directory %s exists and was not created by nexus-torture; choose another --dir", dir)
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("clearing previous run in %s: %w", dir, err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, Marker), []byte("created by nexus-torture; safe to delete\n"), 0o644)
}

func stateDiff(want, got oracle.State) string {
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			return fmt.Sprintf("key %q: want %s, got %s", k, v, got.Cell(k))
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			return fmt.Sprintf("unexpected key %q", k)
		}
	}
	return ""
}

func avgMax(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var s, m float64
	for _, x := range xs {
		s += x
		if x > m {
			m = x
		}
	}
	return s / float64(len(xs)), m
}

func mib(b uint64) string {
	if b == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// shellQuote quotes a path that contains spaces so the repro line can be
// pasted into a shell as-is.
func shellQuote(p string) string {
	if strings.ContainsAny(p, " 	'\"") {
		return "\"" + p + "\""
	}
	return p
}

// notSent marks a request the writer generated after the graceful close had
// already closed serve's input; no byte of it reached the process.
const notSent = "not-sent: input already closed"
