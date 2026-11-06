package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/scenario"
)

// tortureManager starts the real nexus-torture executable with validated
// arguments only (no shell, no free-form command line) and reports progress
// from the files it writes. One run at a time.
type tortureManager struct {
	exe      string // nexus-torture
	kvdb     string
	mutant   string // kvdb-mutant (mutation testing), "" if not built
	bench    string // kvdb-bench, "" if not built
	perf     string // kvdb-perf, "" if not built
	powercut string // nexus-powercut, "" if not built
	dir      string

	mu    sync.Mutex
	runs  map[string]*tortureRun
	order []string

	modesOnce sync.Once
	modes     []tortureMode
	modesErr  error
}

type tortureMode struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type tortureRun struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"` // "run", "scenarios", "mutants" or "bench"
	Mode       string          `json:"mode,omitempty"`
	Seed       string          `json:"seed,omitempty"`
	Iterations int             `json:"iterations,omitempty"`
	Only       []string        `json:"only,omitempty"`
	Iteration  int             `json:"iteration,omitempty"` // replay of one iteration
	ReplayOf   string          `json:"replay_of,omitempty"`
	Bench      *benchConfig    `json:"bench,omitempty"`
	Powercut   *powercutConfig `json:"powercut,omitempty"`
	Perf       *perfConfig     `json:"perf,omitempty"`
	Args       []string        `json:"args"`
	StartedAt  string          `json:"started_at"`
	EndedAt    string          `json:"ended_at,omitempty"`
	State      string          `json:"state"` // running, passed, failed, inconclusive, interrupted, error
	ExitCode   *int            `json:"exit_code"`
	Error      string          `json:"error,omitempty"`

	exe       string // executable; nexus-torture when empty
	dir       string
	console   string
	started   time.Time
	ended     time.Time
	cmd       *exec.Cmd
	cancelled bool
	done      chan struct{}
}

var (
	seedRe     = regexp.MustCompile(`^[0-9]{1,18}$`)
	scenarioRe = regexp.MustCompile(`^[A-F][1-9]$`)
)

const maxIterations = 1000

func (t *tortureManager) unavailable(w http.ResponseWriter) bool {
	if t.exe == "" || t.kvdb == "" {
		missing := []string{}
		if t.exe == "" {
			missing = append(missing, "nexus-torture")
		}
		if t.kvdb == "" {
			missing = append(missing, "kvdb")
		}
		writeError(w, http.StatusServiceUnavailable, "unavailable",
			strings.Join(missing, " and ")+" executable not found: build with `go build -o . ./src/cmd/kvdb ./torture2/cmd/nexus-torture` and start nexus-api from that directory (or pass --kvdb / --torture)", false)
		return true
	}
	return false
}

func (t *tortureManager) loadModes() ([]tortureMode, error) {
	t.modesOnce.Do(func() {
		out, err := exec.Command(t.exe, "modes").Output()
		if err != nil {
			t.modesErr = fmt.Errorf("nexus-torture modes: %w", err)
			return
		}
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			desc := strings.TrimSpace(strings.TrimPrefix(line, f[0]))
			t.modes = append(t.modes, tortureMode{f[0], desc})
		}
		if len(t.modes) == 0 {
			t.modesErr = errors.New("nexus-torture modes printed no modes")
		}
	})
	return t.modes, t.modesErr
}

func (t *tortureManager) running() *tortureRun {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range t.runs {
		if r.State == "running" {
			return r
		}
	}
	return nil
}

func (s *Server) handleTortureModes(w http.ResponseWriter, r *http.Request) {
	t := s.torture
	if t.unavailable(w) {
		return
	}
	modes, err := t.loadModes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "adapter", err.Error(), false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"modes": modes, "source": "nexus-torture modes", "max_iterations": maxIterations})
}

// handleScenarioCatalog lists the 32 official scenarios from the tester's
// own catalog (metadata only; nothing runs).
func (s *Server) handleScenarioCatalog(w http.ResponseWriter, r *http.Request) {
	var out []map[string]string
	for _, sc := range scenario.Catalog() {
		out = append(out, map[string]string{"id": sc.ID, "group": sc.Group, "title": sc.Title, "invariant": sc.Invariant, "method": sc.Method})
	}
	writeJSON(w, http.StatusOK, map[string]any{"scenarios": out, "source": "torture2/scenario.Catalog"})
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	t := s.torture
	if t.unavailable(w) {
		return
	}
	var b struct {
		Mode       string `json:"mode"`
		Seed       string `json:"seed"`
		Iterations int    `json:"iterations"`
	}
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	modes, err := t.loadModes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "adapter", err.Error(), false)
		return
	}
	known := false
	for _, m := range modes {
		known = known || m.Name == b.Mode
	}
	switch {
	case !known:
		writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("unknown mode %q (GET /api/torture/modes)", b.Mode), false)
		return
	case !seedRe.MatchString(b.Seed):
		writeError(w, http.StatusBadRequest, "invalid", "seed must be 1-18 decimal digits", false)
		return
	case b.Iterations < 1 || b.Iterations > maxIterations:
		writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("iterations must be between 1 and %d", maxIterations), false)
		return
	}
	run := &tortureRun{Kind: "run", Mode: b.Mode, Seed: b.Seed, Iterations: b.Iterations}
	args := []string{"run", "--mode", b.Mode, "--seed", b.Seed, "--iterations", strconv.Itoa(b.Iterations), "--kvdb", t.kvdb}
	s.startRun(w, run, args)
}

func (s *Server) handleStartScenarios(w http.ResponseWriter, r *http.Request) {
	t := s.torture
	if t.unavailable(w) {
		return
	}
	var b struct {
		Only []string `json:"only"`
	}
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	if len(b.Only) == 0 || len(b.Only) > 32 {
		writeError(w, http.StatusBadRequest, "invalid", "choose 1-32 scenario IDs (GET /api/torture/scenarios)", false)
		return
	}
	valid := map[string]bool{}
	for _, sc := range scenario.Catalog() {
		valid[sc.ID] = true
	}
	for _, id := range b.Only {
		if !scenarioRe.MatchString(id) || !valid[id] {
			writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("unknown scenario %q", id), false)
			return
		}
	}
	run := &tortureRun{Kind: "scenarios", Only: b.Only}
	args := []string{"scenarios", "--kvdb", t.kvdb, "--only", strings.Join(b.Only, ",")}
	s.startRun(w, run, args)
}

func (s *Server) startLocked(w http.ResponseWriter, run *tortureRun, args []string) bool {
	t := s.torture
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range t.runs {
		if r.State == "running" {
			writeError(w, http.StatusConflict, "busy", "a "+r.Kind+" run is already in progress ("+r.ID+"); only one run executes at a time so measurements do not disturb each other; wait for it or cancel it", false)
			return false
		}
	}
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "adapter", err.Error(), false)
		return false
	}
	now := time.Now()
	run.ID = fmt.Sprintf("r-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1e6)
	run.dir = filepath.Join(t.dir, run.ID)
	run.console = filepath.Join(t.dir, run.ID+".console.log")
	exe := run.exe
	if exe == "" {
		exe = t.exe
	}
	if run.Kind == "bench" {
		// kvdb-bench: --dir is the database directory, --json the report.
		if err := os.MkdirAll(run.dir, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, "adapter", err.Error(), false)
			return false
		}
		args = append(args, "--dir", filepath.Join(run.dir, "db"), "--json", filepath.Join(run.dir, "bench.json"))
	} else if run.Kind == "perf" {
		// kvdb-perf: --dir is its scratch directory, --json the report.
		if err := os.MkdirAll(run.dir, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, "adapter", err.Error(), false)
			return false
		}
		args = append(args, "--dir", filepath.Join(run.dir, "work"), "--json", filepath.Join(run.dir, "perf.json"))
	} else {
		args = append(args, "--dir", run.dir)
	}
	run.Args = append([]string{filepath.Base(exe)}, args...)
	f, err := os.Create(run.console)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "adapter", err.Error(), false)
		return false
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		f.Close()
		writeError(w, http.StatusInternalServerError, "adapter", "starting "+filepath.Base(exe)+": "+err.Error(), false)
		return false
	}
	run.cmd, run.started, run.State, run.done = cmd, now, "running", make(chan struct{})
	run.StartedAt = now.UTC().Format(time.RFC3339Nano)
	t.runs[run.ID] = run
	t.order = append(t.order, run.ID)
	go func() {
		err := cmd.Wait()
		f.Close()
		code := cmd.ProcessState.ExitCode()
		t.mu.Lock()
		run.ExitCode = &code
		run.ended = time.Now()
		run.EndedAt = run.ended.UTC().Format(time.RFC3339Nano)
		switch {
		case run.cancelled:
			run.State = "interrupted"
		case code == 0:
			run.State = "passed"
		case code == 1:
			run.State = "failed"
		case code == 3:
			run.State = "inconclusive"
		default:
			run.State = "error"
			if err != nil {
				run.Error = err.Error()
			}
		}
		t.mu.Unlock()
		close(run.done)
	}()
	return true
}

func (t *tortureManager) get(id string) *tortureRun {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.runs[id]
}

func (t *tortureManager) stopAll() {
	t.mu.Lock()
	var live []*tortureRun
	for _, r := range t.runs {
		if r.State == "running" {
			r.cancelled = true
			live = append(live, r)
		}
	}
	t.mu.Unlock()
	for _, r := range live {
		killTree(r.cmd.Process)
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
		}
	}
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	t := s.torture
	t.mu.Lock()
	ids := append([]string(nil), t.order...)
	t.mu.Unlock()
	out := []map[string]any{}
	for i := len(ids) - 1; i >= 0; i-- {
		if run := t.get(ids[i]); run != nil {
			out = append(out, s.runStatus(run, false))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out, "scope": "runs started by this nexus-api process"})
}

func (s *Server) handleRunStatus(w http.ResponseWriter, r *http.Request) {
	run := s.torture.get(r.PathValue("id"))
	if run == nil {
		writeError(w, http.StatusNotFound, "not-found", "no such run", false)
		return
	}
	writeJSON(w, http.StatusOK, s.runStatus(run, true))
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	t := s.torture
	run := t.get(r.PathValue("id"))
	if run == nil {
		writeError(w, http.StatusNotFound, "not-found", "no such run", false)
		return
	}
	t.mu.Lock()
	live := run.State == "running"
	if live {
		run.cancelled = true
	}
	t.mu.Unlock()
	if live {
		killTree(run.cmd.Process)
		select {
		case <-run.done:
		case <-time.After(15 * time.Second):
		}
	}
	writeJSON(w, http.StatusOK, s.runStatus(run, false))
}

func (s *Server) handleRunLog(w http.ResponseWriter, r *http.Request) {
	run := s.torture.get(r.PathValue("id"))
	if run == nil {
		writeError(w, http.StatusNotFound, "not-found", "no such run", false)
		return
	}
	lines := intParam(r, "lines", 200, 1, 2000)
	writeJSON(w, http.StatusOK, map[string]any{"id": run.ID, "lines": tailLines(run.console, lines)})
}

// runStatus builds the status from the run's own files: results.jsonl (one
// line per finished iteration), summary.json / scenarios.json (at the end).
func (s *Server) runStatus(run *tortureRun, detail bool) map[string]any {
	t := s.torture
	t.mu.Lock()
	cp := *run
	t.mu.Unlock()
	end := time.Now()
	if !cp.ended.IsZero() {
		end = cp.ended
	}
	st := map[string]any{"run": cp, "elapsed_seconds": end.Sub(cp.started).Seconds()}
	switch cp.Kind {
	case "run":
		prog := iterProgress(filepath.Join(cp.dir, "results.jsonl"), detail)
		st["progress"] = prog
		st["summary"] = rawJSONFile(filepath.Join(cp.dir, "summary.json"))
	case "scenarios":
		st["progress"] = scenarioProgress(cp.console, len(cp.Only))
		st["summary"] = rawJSONFile(filepath.Join(cp.dir, "scenarios.json"))
	case "mutants":
		st["progress"] = mutantProgress(cp.console, len(cp.Only))
		st["summary"] = rawJSONFile(filepath.Join(cp.dir, "mutation-report.json"))
	case "bench":
		st["progress"] = benchProgress(cp.console)
		st["summary"] = rawJSONFile(filepath.Join(cp.dir, "bench.json"))
	case "powercut":
		st["progress"] = powercutProgress(cp.console)
		st["summary"] = rawJSONFile(filepath.Join(cp.dir, "report.json"))
	case "perf":
		st["progress"] = perfProgress(cp.console)
		st["summary"] = rawJSONFile(filepath.Join(cp.dir, "perf.json"))
	}
	if detail {
		st["log_tail"] = tailLines(cp.console, 40)
	}
	return st
}

type iterLine struct {
	Iter        int     `json:"iter"`
	Verdict     string  `json:"verdict"`
	KillAfterMs int     `json:"kill_after_ms"`
	KillPoint   string  `json:"kill_point"`
	Sent        int     `json:"sent"`
	AckedWrites int     `json:"acked_writes"`
	Unknown     int     `json:"unknown"`
	Gets        int     `json:"gets"`
	Keys        int     `json:"keys"`
	OracleMs    float64 `json:"oracle_ms"`
	FailKind    string  `json:"fail_kind,omitempty"`
	Failure     string  `json:"failure,omitempty"`
}

func iterProgress(path string, detail bool) map[string]any {
	out := map[string]any{"completed": 0, "passed": 0, "failed": 0, "inconclusive": 0}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	var all []iterLine
	counts := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l iterLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue // the last line may be half-written while the run is going
		}
		counts[l.Verdict]++
		if len(l.Failure) > 400 {
			l.Failure = l.Failure[:400] + "…"
		}
		all = append(all, l)
	}
	out["completed"] = len(all)
	out["passed"], out["failed"], out["inconclusive"] = counts["PASS"], counts["FAIL"], counts["INCONCLUSIVE"]
	if len(all) > 0 {
		out["last"] = all[len(all)-1]
	}
	if detail {
		keep := all
		if len(keep) > 200 {
			keep = keep[len(keep)-200:]
		}
		out["iterations"] = keep
	}
	return out
}

var scenarioLineRe = regexp.MustCompile(`^\[([A-F][1-9])\]\s+(PASS|FAIL|INCONCLUSIVE|UNSUPPORTED)\b`)

func scenarioProgress(console string, total int) map[string]any {
	done := []map[string]string{}
	for _, l := range tailLines(console, 2000) {
		if m := scenarioLineRe.FindStringSubmatch(l); m != nil {
			done = append(done, map[string]string{"id": m[1], "status": m[2]})
		}
	}
	return map[string]any{"completed": len(done), "total": total, "scenarios": done}
}

func rawJSONFile(path string) json.RawMessage {
	b, err := os.ReadFile(path)
	if err != nil || !json.Valid(b) {
		return nil
	}
	return b
}

// tailLines returns the last n lines of a text file (reads at most 256 KiB).
func tailLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return []string{}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return []string{}
	}
	const max = 256 << 10
	off := st.Size() - max
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && len(buf) > 0 {
		return []string{}
	}
	lines := strings.Split(strings.ReplaceAll(string(buf), "\r\n", "\n"), "\n")
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // first line may be cut
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// startRun starts the child and replies with its status. The status is
// built after the manager lock is released (runStatus takes it).
func (s *Server) startRun(w http.ResponseWriter, run *tortureRun, args []string) {
	if s.startLocked(w, run, args) {
		writeJSON(w, http.StatusAccepted, s.runStatus(run, false))
	}
}
