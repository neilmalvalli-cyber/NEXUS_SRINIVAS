package main

// Further run kinds managed by tortureManager (one child process at a time,
// validated arguments only, no shell): mutation testing with kvdb-mutant,
// replay of a failed torture iteration, and kvdb-bench benchmarks. Sharing
// the one-at-a-time rule keeps a benchmark from being skewed by a torture run.

import (
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/mutation"
)

// ---- mutation testing ----

func (s *Server) handleMutantCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"mutations": mutation.Cases,
		"available": s.torture.mutant != "" && s.torture.exe != "",
		"source":    "torture2/mutation.Cases; the mutation named none is the unmodified control build and must PASS",
	})
}

var mutationNameRe = regexp.MustCompile(`^[a-z][a-z-]{1,31}$`)

func (s *Server) handleStartMutants(w http.ResponseWriter, r *http.Request) {
	t := s.torture
	if t.unavailable(w) {
		return
	}
	if t.mutant == "" {
		writeError(w, http.StatusServiceUnavailable, "unavailable",
			"kvdb-mutant executable not found: build it with `go build -o . ./torture2/mutants/kvdb-mutant` next to nexus-api", false)
		return
	}
	var b struct {
		Only       []string `json:"only"`
		Iterations int      `json:"iterations"`
		Seed       string   `json:"seed"`
	}
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	known := map[string]bool{}
	for _, c := range mutation.Cases {
		known[c.Name] = true
	}
	for _, n := range b.Only {
		if !mutationNameRe.MatchString(n) || !known[n] {
			writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("unknown mutation %q (GET /api/torture/mutants)", n), false)
			return
		}
	}
	if b.Iterations == 0 {
		b.Iterations = 40
	}
	if b.Iterations < 1 || b.Iterations > 100 {
		writeError(w, http.StatusBadRequest, "invalid", "iterations must be between 1 and 100", false)
		return
	}
	if b.Seed == "" {
		b.Seed = "777"
	}
	if !seedRe.MatchString(b.Seed) {
		writeError(w, http.StatusBadRequest, "invalid", "seed must be 1-18 decimal digits", false)
		return
	}
	run := &tortureRun{Kind: "mutants", Only: b.Only, Seed: b.Seed, Iterations: b.Iterations}
	args := []string{"mutants", "--mutant", t.mutant, "--iterations", strconv.Itoa(b.Iterations), "--seed", b.Seed}
	if len(b.Only) > 0 {
		args = append(args, "--only", strings.Join(b.Only, ","))
	}
	s.startRun(w, run, args)
}

var mutantLineRe = regexp.MustCompile(`^--- ([a-z-]+): (.+?) after (\d+) iteration`)

func mutantProgress(console string, only int) map[string]any {
	total := only
	if total == 0 {
		total = len(mutation.Cases)
	}
	done := []map[string]string{}
	for _, l := range tailLines(console, 4000) {
		if m := mutantLineRe.FindStringSubmatch(l); m != nil {
			done = append(done, map[string]string{"mutation": m[1], "verdict": m[2], "iterations": m[3]})
		}
	}
	return map[string]any{"completed": len(done), "total": total, "mutations": done}
}

// ---- replay of a failed iteration ----

// handleReplay re-runs one failed iteration of an earlier run from the
// database copy nexus-torture saved before it (failures/iter-N/pre), exactly
// as the repro command it prints.
func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	t := s.torture
	if t.unavailable(w) {
		return
	}
	src := t.get(r.PathValue("id"))
	if src == nil {
		writeError(w, http.StatusNotFound, "not-found", "no such run", false)
		return
	}
	var b struct {
		Iteration int `json:"iteration"`
	}
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	t.mu.Lock()
	cp := *src
	t.mu.Unlock()
	if cp.Kind != "run" || cp.Mode == "" || cp.Seed == "" {
		writeError(w, http.StatusBadRequest, "invalid", "only workload runs can be replayed", false)
		return
	}
	if b.Iteration < 1 || b.Iteration > cp.Iterations {
		writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("iteration must be between 1 and %d", cp.Iterations), false)
		return
	}
	pre := filepath.Join(cp.dir, "failures", fmt.Sprintf("iter-%d", b.Iteration), "pre", "db.kv")
	if !fileExists(pre) {
		writeError(w, http.StatusNotFound, "notfound",
			fmt.Sprintf("no saved failure for iteration %d of %s: nexus-torture keeps failures/iter-N/pre only for failing iterations", b.Iteration, cp.ID), false)
		return
	}
	run := &tortureRun{Kind: "run", Mode: cp.Mode, Seed: cp.Seed, Iterations: cp.Iterations, Iteration: b.Iteration, ReplayOf: cp.ID}
	args := []string{"run", "--mode", cp.Mode, "--seed", cp.Seed, "--iterations", strconv.Itoa(cp.Iterations),
		"--iteration", strconv.Itoa(b.Iteration), "--from-db", pre, "--kvdb", t.kvdb}
	s.startRun(w, run, args)
}

// ---- benchmarks ----

type benchConfig struct {
	N         int `json:"n"`
	Runs      int `json:"runs"`
	ValueSize int `json:"value_size"`
	BatchSize int `json:"batch_size"`
}

func (s *Server) handleStartBench(w http.ResponseWriter, r *http.Request) {
	t := s.torture
	if t.bench == "" {
		writeError(w, http.StatusServiceUnavailable, "unavailable",
			"kvdb-bench executable not found: build it with `go build -o . ./src/cmd/kvdb-bench` next to nexus-api", false)
		return
	}
	var b benchConfig
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	if b.BatchSize == 0 {
		b.BatchSize = 1000
	}
	switch {
	case b.N < 2000 || b.N > 100000:
		writeError(w, http.StatusBadRequest, "invalid", "n must be between 2000 and 100000 (smaller runs finish phases faster than the timer resolution, which kvdb-bench reports as infinite ops/s)", false)
		return
	case b.Runs < 1 || b.Runs > 3:
		writeError(w, http.StatusBadRequest, "invalid", "runs must be between 1 and 3", false)
		return
	case b.ValueSize < 0 || b.ValueSize > 65536:
		writeError(w, http.StatusBadRequest, "invalid", "value_size must be between 0 and 65536", false)
		return
	case b.BatchSize < 1 || b.BatchSize > 10000:
		writeError(w, http.StatusBadRequest, "invalid", "batch_size must be between 1 and 10000", false)
		return
	}
	cfg := b
	run := &tortureRun{Kind: "bench", Bench: &cfg, exe: t.bench}
	args := []string{"--n", strconv.Itoa(b.N), "--runs", strconv.Itoa(b.Runs),
		"--value-size", strconv.Itoa(b.ValueSize), "--batch-size", strconv.Itoa(b.BatchSize)}
	s.startRun(w, run, args)
}

var benchPhaseRe = regexp.MustCompile(`^  (put|reopen \(recovery\)|get|batch|compact \(n live keys\)|delete|compact \(empty\))\s+\d`)

// benchProgress counts finished phases in the console (7 per run).
func benchProgress(console string) map[string]any {
	runs, phases := 0, 0
	inMedian := false
	for _, l := range tailLines(console, 2000) {
		switch {
		case strings.HasPrefix(l, "run "):
			runs++
		case strings.HasPrefix(l, "median of"):
			inMedian = true
		case !inMedian && benchPhaseRe.MatchString(l):
			phases++
		}
	}
	return map[string]any{"completed": phases, "runs_started": runs, "median_printed": inMedian}
}
