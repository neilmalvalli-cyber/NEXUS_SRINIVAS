package main

// Power-Cut Explorer runs and performance runs. Both start real executables
// (nexus-powercut, kvdb-perf) through the torture run manager: validated
// arguments only, no shell, one child process at a time (so a benchmark is
// never skewed by another run), cancellable, status and logs through the
// existing /api/torture/runs/{id} endpoints.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/powercut"
)

// ---- Power-Cut Explorer ----

type powercutConfig struct {
	Seed        int64 `json:"seed"`
	Writers     int   `json:"writers"`
	Ops         int   `json:"ops_per_writer"`
	MaxPoints   int   `json:"max_crash_points"`
	MaxVariants int   `json:"max_variants_per_point"`
	NoControls  bool  `json:"no_controls"`
}

func (s *Server) handlePowercutInfo(w http.ResponseWriter, r *http.Request) {
	t := s.torture
	t.mu.Lock()
	runs := []map[string]any{}
	for i := len(t.order) - 1; i >= 0; i-- {
		if run := t.runs[t.order[i]]; run != nil && run.Kind == "powercut" {
			cp := *run
			runs = append(runs, map[string]any{"id": cp.ID, "state": cp.State, "started_at": cp.StartedAt, "ended_at": cp.EndedAt, "config": cp.Powercut})
		}
	}
	t.mu.Unlock()
	d := powercut.DefaultConfig
	writeJSON(w, http.StatusOK, map[string]any{
		"available":   t.powercut != "",
		"defaults":    powercutConfig{Seed: d.Seed, Writers: d.Writers, Ops: d.OpsPerW, MaxPoints: d.MaxPoints, MaxVariants: d.MaxVar},
		"model":       powercut.ModelText,
		"not_modeled": powercut.NotModeledText,
		"disclaimer":  powercut.Disclaimer,
		"sector_size": powercut.SectorSize,
		"runs":        runs,
		"limits":      map[string]string{"seed": "0–999999999999", "writers": "1–8", "ops_per_writer": "1–500", "max_crash_points": "1–2000", "max_variants_per_point": "1–16"},
		"recorded":    s.recordedPowercut(),
	})
}

// recordedPowercut returns the recorded default run kept next to the
// optimization history (powercut-default-seed1.json), labelled with its file
// and modification time, or nil if it is not there.
func (s *Server) recordedPowercut() any {
	if s.cfg.PerfHistory == "" {
		return nil
	}
	p := filepath.Join(filepath.Dir(s.cfg.PerfHistory), "powercut-default-seed1.json")
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil || !json.Valid(b) {
		return nil
	}
	return map[string]any{"source": filepath.ToSlash(p), "modified": st.ModTime().UTC(), "report": json.RawMessage(b)}
}

func (s *Server) handleStartPowercut(w http.ResponseWriter, r *http.Request) {
	t := s.torture
	if t.powercut == "" {
		writeError(w, http.StatusServiceUnavailable, "unavailable",
			"nexus-powercut executable not found: build it with `go build -o . ./src/cmd/nexus-powercut` next to nexus-api", false)
		return
	}
	d := powercut.DefaultConfig
	b := powercutConfig{Seed: d.Seed, Writers: d.Writers, Ops: d.OpsPerW, MaxPoints: d.MaxPoints, MaxVariants: d.MaxVar}
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	switch {
	case b.Seed < 0 || b.Seed > 999999999999:
		writeError(w, http.StatusBadRequest, "invalid", "seed must be between 0 and 999999999999", false)
		return
	case b.Writers < 1 || b.Writers > 8:
		writeError(w, http.StatusBadRequest, "invalid", "writers must be between 1 and 8", false)
		return
	case b.Ops < 1 || b.Ops > 500:
		writeError(w, http.StatusBadRequest, "invalid", "ops_per_writer must be between 1 and 500", false)
		return
	case b.MaxPoints < 1 || b.MaxPoints > 2000:
		writeError(w, http.StatusBadRequest, "invalid", "max_crash_points must be between 1 and 2000", false)
		return
	case b.MaxVariants < 1 || b.MaxVariants > 16:
		writeError(w, http.StatusBadRequest, "invalid", "max_variants_per_point must be between 1 and 16", false)
		return
	}
	cfg := b
	run := &tortureRun{Kind: "powercut", Powercut: &cfg, exe: t.powercut, Seed: strconv.FormatInt(b.Seed, 10)}
	args := []string{"--seed", strconv.FormatInt(b.Seed, 10), "--writers", strconv.Itoa(b.Writers), "--ops", strconv.Itoa(b.Ops),
		"--max-points", strconv.Itoa(b.MaxPoints), "--max-variants", strconv.Itoa(b.MaxVariants), "--progress"}
	if b.NoControls {
		args = append(args, "--no-controls")
	}
	s.startRun(w, run, args)
}

var progressLineRe = regexp.MustCompile(`^\{"progress":`)

// powercutProgress reads the last progress line nexus-powercut printed.
func powercutProgress(console string) map[string]any {
	out := map[string]any{"crash_points_done": 0, "crash_points_total": 0, "failed": 0}
	lines := tailLines(console, 400)
	for i := len(lines) - 1; i >= 0; i-- {
		if progressLineRe.MatchString(lines[i]) {
			var p struct {
				Progress map[string]int `json:"progress"`
			}
			if json.Unmarshal([]byte(lines[i]), &p) == nil {
				for k, v := range p.Progress {
					out[k] = v
				}
			}
			break
		}
	}
	return out
}

var imageNameRe = regexp.MustCompile(`^p[0-9]{5}-[a-z0-9+-]{1,60}$`)

// handlePowercutXRay serves the X-RAY report the explorer saved for one
// failed image (<run>/images/failures/<image>/xray.json).
func (s *Server) handlePowercutXRay(w http.ResponseWriter, r *http.Request) {
	run := s.torture.get(r.PathValue("id"))
	img := r.PathValue("image")
	if run == nil || run.Kind != "powercut" || !imageNameRe.MatchString(img) {
		writeError(w, http.StatusNotFound, "not-found", "no such power-cut run or image", false)
		return
	}
	p := filepath.Join(run.dir, "images", "failures", img, "xray.json")
	b, err := os.ReadFile(p)
	if err != nil {
		writeError(w, http.StatusNotFound, "not-found", "no X-RAY report for this image (only failed images are kept and diagnosed)", false)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(b)
}

// ---- performance runs (kvdb-perf) ----

var perfPhases = map[string]bool{"writers": true, "batch": true, "delete": true, "get": true, "mixed": true, "memory": true, "recovery": true, "compaction": true}

type perfConfig struct {
	Only  []string `json:"only"`
	Quick bool     `json:"quick"`
}

func (s *Server) handleStartPerf(w http.ResponseWriter, r *http.Request) {
	t := s.torture
	if t.perf == "" {
		writeError(w, http.StatusServiceUnavailable, "unavailable",
			"kvdb-perf executable not found: build it with `go build -o . ./src/cmd/kvdb-perf` next to nexus-api", false)
		return
	}
	var b perfConfig
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	for _, p := range b.Only {
		if !perfPhases[p] {
			writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("unknown phase %q (writers, batch, delete, get, mixed, memory, recovery, compaction)", p), false)
			return
		}
	}
	if len(b.Only) == 0 && !b.Quick {
		writeError(w, http.StatusBadRequest, "invalid", "a full run (all phases, official sizes) takes several minutes and needs over 1 GB of disk: choose phases with \"only\" or set \"quick\": true", false)
		return
	}
	cfg := b
	run := &tortureRun{Kind: "perf", Perf: &cfg, exe: t.perf}
	args := []string{"--label", "dashboard"}
	if b.Quick {
		args = append(args, "--quick")
	}
	if len(b.Only) > 0 {
		args = append(args, "--only", strings.Join(b.Only, ","))
	}
	s.startRun(w, run, args)
}

func perfProgress(console string) map[string]any {
	n := 0
	f, err := os.Open(console)
	if err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if l := sc.Text(); len(l) > 40 && strings.Contains(l, "s  ") {
				n++
			}
		}
		f.Close()
	}
	return map[string]any{"results_printed": n}
}

// handlePerfHistory serves the recorded optimization history
// (docs/results/optimization/history.json): measurements taken while the
// optimizations were made, labelled as recorded, with machine and date.
func (s *Server) handlePerfHistory(w http.ResponseWriter, r *http.Request) {
	p := s.cfg.PerfHistory
	st, err := os.Stat(p)
	if p == "" || err != nil {
		writeError(w, http.StatusNotFound, "not-found", "no recorded optimization history configured (--perf-history)", false)
		return
	}
	b, err := os.ReadFile(p)
	if err != nil || !json.Valid(b) {
		writeError(w, http.StatusInternalServerError, "adapter", "history file unreadable or not JSON", false)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": filepath.ToSlash(p), "modified": st.ModTime().UTC(), "kind": "recorded", "history": json.RawMessage(b)})
}
