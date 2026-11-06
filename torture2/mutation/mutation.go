// Package mutation proves the torture engine detects real bugs: it runs the
// engine against kvdb-mutant, a deliberately broken build of NEXUS, once per
// mutation, and checks that each one is caught (and that the faithful control
// build is not).
package mutation

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/engine"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/oracle"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/proc"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/workload"
)

// Case is one mutation and how it should be caught.
type Case struct {
	Name     string   `json:"mutation"`
	Simulate string   `json:"simulates"`
	Mode     string   `json:"mode"`
	Expect   []string `json:"expected_failure_kinds"` // empty: control, must PASS
}

// Cases lists the mutations.
var Cases = []Case{
	{"none", "faithful wrapper around the NEXUS library (control)", "default", nil},
	{"ack-before-sync", "ACK sent before the write is durable (written 25 ms later)", "default", []string{string(oracle.VAckedLost), string(oracle.VPartialBatch), string(oracle.VNoOrder)}},
	{"drop-acked", "every 7th write acknowledged but never written", "default", []string{string(oracle.VAckedLost), string(oracle.VBadRead), string(oracle.VNoOrder)}},
	{"partial-batch", "batches applied one operation at a time (not atomic)", "batch10", []string{string(oracle.VPartialBatch), string(oracle.VAckedLost), string(oracle.VBadRead), string(oracle.VNoOrder)}},
	{"wrong-unknown", "a durable put reported as a definite failure", "default", []string{string(oracle.VRejectedVisible), string(oracle.VBadRead)}},
	{"bad-recovery", "recovery rolls back the last acknowledged put", "default", []string{string(oracle.VAckedLost), string(oracle.VNoOrder)}},
	{"corrupt-value", "every 6th put stores a value with one byte changed", "default", []string{string(oracle.VNeverWritten), string(oracle.VBadRead)}},
}

// Options configure a mutation run.
type Options struct {
	Mutant     string
	Dir        string
	Iterations int
	Seed       int64
	Only       []string
	Out        io.Writer
}

// Outcome is one mutation's result.
type Outcome struct {
	Case
	Result     string  `json:"engine_result"`
	Detected   bool    `json:"detected"`
	AsExpected bool    `json:"as_expected"`
	Iteration  int     `json:"detected_at_iteration,omitempty"`
	Kind       string  `json:"failure_kind,omitempty"`
	Failure    string  `json:"failure,omitempty"`
	Iterations int     `json:"iterations_run"`
	Seconds    float64 `json:"seconds"`
	Repro      string  `json:"repro,omitempty"`
}

// Report is the whole run.
type Report struct {
	Mutant      string    `json:"mutant"`
	Seed        int64     `json:"seed"`
	Outcomes    []Outcome `json:"outcomes"`
	AllDetected bool      `json:"all_as_expected"`
}

// Run executes every (selected) mutation.
func Run(o Options) (Report, error) {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	rep := Report{Mutant: o.Mutant, Seed: o.Seed, AllDetected: true}
	if err := engine.PrepareDir(o.Dir); err != nil {
		return rep, err
	}
	for _, c := range Cases {
		if len(o.Only) > 0 && !contains(o.Only, c.Name) {
			continue
		}
		b, err := proc.Locate(o.Mutant)
		if err != nil {
			return rep, err
		}
		b.Env = []string{"KVDB_MUTANT=" + c.Name}
		m, err := workload.ByName(c.Mode)
		if err != nil {
			return rep, err
		}
		iters := o.Iterations
		if c.Expect == nil && iters > 15 {
			iters = 15 // the control only needs to show the wrapper itself is sound
		}
		fmt.Fprintf(o.Out, "\n=== mutation %s (%s), mode %s, up to %d iterations\n", c.Name, c.Simulate, c.Mode, iters)
		t0 := time.Now()
		sum, err := engine.Run(engine.Config{Mode: m, Seed: o.Seed, Iterations: iters, Kvdb: b,
			Dir: filepath.Join(o.Dir, c.Name), StopOnFail: true, Quiet: true, Out: o.Out})
		if err != nil {
			return rep, fmt.Errorf("mutation %s: %w", c.Name, err)
		}
		out := Outcome{Case: c, Result: sum.Result, Iterations: sum.Completed, Seconds: time.Since(t0).Seconds()}
		out.Detected = sum.Result == "FAIL"
		if f := sum.FirstFailure; f != nil {
			out.Iteration, out.Kind, out.Repro = f.Iter, f.FailKind, f.Repro
			out.Failure = trunc(f.Failure, 600)
		}
		if c.Expect == nil {
			out.AsExpected = sum.Result == "PASS"
		} else {
			out.AsExpected = out.Detected && (contains(c.Expect, out.Kind) || out.Kind == "recovery" || out.Kind == "protocol")
		}
		if !out.AsExpected {
			rep.AllDetected = false
		}
		verdict := "DETECTED"
		switch {
		case c.Expect == nil && out.AsExpected:
			verdict = "CONTROL PASSED (no false alarm)"
		case c.Expect == nil:
			verdict = "CONTROL FAILED (false alarm or wrapper bug)"
		case !out.Detected:
			verdict = "NOT DETECTED"
		case !out.AsExpected:
			verdict = "DETECTED with an unexpected kind"
		}
		fmt.Fprintf(o.Out, "--- %s: %s after %d iteration(s) in %.1fs; kind=%s\n", c.Name, verdict, out.Iterations, out.Seconds, out.Kind)
		rep.Outcomes = append(rep.Outcomes, out)
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	os.WriteFile(filepath.Join(o.Dir, "mutation-report.json"), b, 0o644)
	fmt.Fprintf(o.Out, "\nMUTATION RESULT: %s\n", map[bool]string{true: "every mutation detected, control passed", false: "NOT all as expected"}[rep.AllDetected])
	for _, x := range rep.Outcomes {
		fmt.Fprintf(o.Out, "  %-16s %-12s iteration=%-3d kind=%s\n", x.Name, map[bool]string{true: "as expected", false: "UNEXPECTED"}[x.AsExpected], x.Iteration, x.Kind)
	}
	return rep, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
