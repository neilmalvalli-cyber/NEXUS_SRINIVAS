package main

// NEXUS X-RAY endpoints (docs/XRAY_API.md). Scans are addressed by target
// ID, never by a client-supplied path:
//
//	main            the database this adapter has open (read-only scan
//	                under writeMu, so no adapter write is in flight)
//	demo            the crash-demo database (recovery-demo/demo.kv)
//	lab:<case>      a disposable corrupted copy built by xray.BuildLab in
//	                <work>/xray/lab
//
// Read-only by default. The only modifying call is POST /api/xray/repair,
// allowed for demo and lab targets only, with "confirm": true; it backs the
// files up first (xray.Repair).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/xray"
)

const xrayKeep = 50

type xrayStore struct {
	dir string // <work>/xray

	mu      sync.Mutex
	scanMu  sync.Mutex // one scan or lab rebuild at a time
	reports []*storedReport
	lab     []xray.LabCase
	labAt   time.Time
	ops     []xray.Finding // findings from failed operations on the main database
}

type storedReport struct {
	ID     string       `json:"id"`
	Target string       `json:"target"`
	Report *xray.Report `json:"report"`
}

type reportSummary struct {
	ID         string                `json:"id"`
	Target     string                `json:"target"`
	Kind       string                `json:"kind"` // live, demo, lab
	StartedAt  time.Time             `json:"started_at"`
	DurationMS float64               `json:"duration_ms"`
	Mode       string                `json:"mode"`
	Health     string                `json:"health"`
	Engine     string                `json:"engine_outcome"`
	Recovery   xray.RecoveryStatus   `json:"recovery_status"`
	Counts     map[xray.Severity]int `json:"counts"`
	Modified   bool                  `json:"modified"`
}

func targetKind(t string) string {
	switch {
	case t == "main":
		return "live"
	case t == "demo":
		return "demo"
	case strings.HasPrefix(t, "powercut:"):
		return "powercut"
	}
	return "lab"
}

func summarize(sr *storedReport) reportSummary {
	r := sr.Report
	return reportSummary{ID: sr.ID, Target: sr.Target, Kind: targetKind(sr.Target), StartedAt: r.StartedAt, DurationMS: r.DurationMS,
		Mode: r.Mode, Health: r.Health, Engine: r.Engine.Outcome, Recovery: r.Recovery.Status, Counts: r.Counts, Modified: r.Modified}
}

var labTargetRe = regexp.MustCompile(`^lab:([a-z][a-z-]{1,40})$`)
var reportIDRe = regexp.MustCompile(`^x-[0-9]{8}-[0-9]{6}-[0-9]{3}$`)

// resolveTarget maps a target ID to a path. It never accepts a path.
func (s *Server) resolveTarget(t string) (path string, err error) {
	switch {
	case t == "main":
		return s.dbAbs, nil
	case t == "demo":
		return s.demo.path(), nil
	}
	m := labTargetRe.FindStringSubmatch(t)
	if m == nil {
		return "", fmt.Errorf("unknown target %q: use main, demo or lab:<case> (GET /api/xray/targets)", t)
	}
	s.xray.mu.Lock()
	defer s.xray.mu.Unlock()
	for _, c := range s.xray.lab {
		if c.Name == m[1] {
			return c.Path, nil
		}
	}
	return "", fmt.Errorf("lab case %q does not exist; build the lab first (POST /api/xray/lab/reset)", m[1])
}

func (s *Server) handleXRayTargets(w http.ResponseWriter, r *http.Request) {
	s.xray.mu.Lock()
	lab := append([]xray.LabCase(nil), s.xray.lab...)
	labAt := s.xray.labAt
	s.xray.mu.Unlock()
	targets := []map[string]any{
		{"id": "main", "kind": "live", "label": "Main database (" + filepath.Base(s.dbAbs) + ")", "repair_allowed": false,
			"note": "held open by nexus-api: scanned read-only while adapter writes are paused; repair is never offered for it"},
		{"id": "demo", "kind": "demo", "label": "Crash-demo database (demo.kv)", "repair_allowed": true, "exists": fileExists(s.demo.path())},
	}
	for _, c := range lab {
		targets = append(targets, map[string]any{"id": "lab:" + c.Name, "kind": "lab", "label": c.Title, "description": c.Description,
			"repair_allowed": true, "expect": map[string]any{"code": c.ExpectCode, "severity": c.ExpectSeverity, "rule": c.ExpectRule, "recovery": c.ExpectRecovery}})
	}
	out := map[string]any{"targets": targets, "lab_built": len(lab) > 0}
	if !labAt.IsZero() {
		out["lab_built_at"] = labAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, out)
}

type scanBody struct {
	Target string `json:"target"`
	NoHash bool   `json:"no_hash"`
}

func (s *Server) handleXRayScan(w http.ResponseWriter, r *http.Request) {
	var b scanBody
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	path, err := s.resolveTarget(b.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error(), false)
		return
	}
	s.xray.scanMu.Lock()
	defer s.xray.scanMu.Unlock()
	opt := xray.Options{NoHash: b.NoHash}
	var rep *xray.Report
	if b.Target == "main" {
		// The adapter holds this database open. Pause its writes (writeMu)
		// and keep the handle from being switched (dbMu) for the scan.
		s.dbMu.RLock()
		path = s.dbAbs
		s.writeMu.Lock()
		opt.OwnerHoldsLock = true
		rep, err = xray.Scan(path, opt)
		s.writeMu.Unlock()
		s.dbMu.RUnlock()
	} else {
		rep, err = xray.Scan(path, opt)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error(), false)
		return
	}
	sr := s.storeReport(b.Target, rep)
	writeJSON(w, http.StatusOK, map[string]any{"id": sr.ID, "target": sr.Target, "report": rep})
}

type repairBody struct {
	Target  string `json:"target"`
	Confirm bool   `json:"confirm"`
}

func (s *Server) handleXRayRepair(w http.ResponseWriter, r *http.Request) {
	var b repairBody
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	if b.Target == "main" {
		writeError(w, http.StatusForbidden, "forbidden", "repair is not available for the main database: it is open in this adapter, and the engine repairs crash leftovers itself on open", false)
		return
	}
	if !b.Confirm {
		writeError(w, http.StatusBadRequest, "invalid", `repair modifies files (after backing them up); send "confirm": true`, false)
		return
	}
	path, err := s.resolveTarget(b.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error(), false)
		return
	}
	if b.Target == "demo" {
		s.demo.mu.Lock()
		defer s.demo.mu.Unlock()
	}
	s.xray.scanMu.Lock()
	defer s.xray.scanMu.Unlock()
	rep, err := xray.Repair(path, filepath.Join(s.xray.dir, "backups"), xray.Options{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "adapter", err.Error(), false)
		return
	}
	sr := s.storeReport(b.Target, rep)
	writeJSON(w, http.StatusOK, map[string]any{"id": sr.ID, "target": sr.Target, "report": rep})
}

func (s *Server) storeReport(target string, rep *xray.Report) *storedReport {
	now := time.Now()
	sr := &storedReport{ID: fmt.Sprintf("x-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1e6), Target: target, Report: rep}
	st := s.xray
	st.mu.Lock()
	for _, o := range st.reports { // IDs have millisecond resolution; keep them unique
		if o.ID == sr.ID {
			time.Sleep(time.Millisecond)
			now = time.Now()
			sr.ID = fmt.Sprintf("x-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1e6)
		}
	}
	st.reports = append(st.reports, sr)
	if len(st.reports) > xrayKeep {
		st.reports = st.reports[len(st.reports)-xrayKeep:]
	}
	st.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(st.dir, "reports"), 0o755); err == nil {
		if b, err := json.MarshalIndent(sr, "", "  "); err == nil {
			os.WriteFile(filepath.Join(st.dir, "reports", sr.ID+".json"), b, 0o644)
		}
	}
	return sr
}

func (s *Server) handleXRayReports(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	s.xray.mu.Lock()
	out := []reportSummary{}
	for i := len(s.xray.reports) - 1; i >= 0; i-- {
		sm := summarize(s.xray.reports[i])
		if kind == "" || kind == sm.Kind {
			out = append(out, sm)
		}
	}
	s.xray.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"reports": out, "scope": "scans run by this nexus-api process (also saved under <work>/xray/reports)"})
}

func (s *Server) report(id string) *storedReport {
	if !reportIDRe.MatchString(id) {
		return nil
	}
	s.xray.mu.Lock()
	defer s.xray.mu.Unlock()
	for _, sr := range s.xray.reports {
		if sr.ID == id {
			return sr
		}
	}
	return nil
}

func (s *Server) handleXRayReport(w http.ResponseWriter, r *http.Request) {
	sr := s.report(r.PathValue("id"))
	if sr == nil {
		writeError(w, http.StatusNotFound, "not-found", "no such report", false)
		return
	}
	writeJSON(w, http.StatusOK, sr)
}

func (s *Server) handleXRayExport(w http.ResponseWriter, r *http.Request) {
	sr := s.report(r.PathValue("id"))
	if sr == nil {
		writeError(w, http.StatusNotFound, "not-found", "no such report", false)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="nexus-xray-%s.json"`, sr.ID))
	writeJSON(w, http.StatusOK, sr)
}

func (s *Server) handleXRayLab(w http.ResponseWriter, r *http.Request) {
	s.xray.mu.Lock()
	lab := append([]xray.LabCase(nil), s.xray.lab...)
	s.xray.mu.Unlock()
	cases := []map[string]any{}
	for _, c := range lab {
		cases = append(cases, map[string]any{"name": c.Name, "target": "lab:" + c.Name, "title": c.Title, "description": c.Description,
			"expect_code": c.ExpectCode, "expect_severity": c.ExpectSeverity, "expect_rule": c.ExpectRule, "expect_recovery": c.ExpectRecovery,
			"expect_offset": c.ExpectOffset, "hold_lock": c.HoldLock})
	}
	writeJSON(w, http.StatusOK, map[string]any{"cases": cases, "available": xray.LabCaseNames,
		"note": "disposable databases created with the real kvdb API and then damaged with exact byte edits; never the main database"})
}

// handleXRayLabReset rebuilds the lab in <work>/xray/lab. The directory is
// owned by the adapter and holds only disposable copies.
func (s *Server) handleXRayLabReset(w http.ResponseWriter, r *http.Request) {
	s.xray.scanMu.Lock()
	defer s.xray.scanMu.Unlock()
	dir := filepath.Join(s.xray.dir, "lab")
	if err := os.RemoveAll(dir); err != nil {
		writeError(w, http.StatusInternalServerError, "adapter", "clearing the lab: "+err.Error(), false)
		return
	}
	cases, err := xray.BuildLab(dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "adapter", err.Error(), false)
		return
	}
	s.xray.mu.Lock()
	s.xray.lab, s.xray.labAt = cases, time.Now()
	s.xray.mu.Unlock()
	s.handleXRayLab(w, r)
}

// handleXRayLabScanAll scans every lab case (holding the lock for the
// lock-conflict case, as the case requires) and returns one summary per
// case with whether the diagnosis matched the expectation.
func (s *Server) handleXRayLabScanAll(w http.ResponseWriter, r *http.Request) {
	s.xray.mu.Lock()
	lab := append([]xray.LabCase(nil), s.xray.lab...)
	s.xray.mu.Unlock()
	if len(lab) == 0 {
		writeError(w, http.StatusConflict, "invalid", "build the lab first (POST /api/xray/lab/reset)", false)
		return
	}
	s.xray.scanMu.Lock()
	defer s.xray.scanMu.Unlock()
	out := []map[string]any{}
	for _, c := range lab {
		var release func() error
		if c.HoldLock {
			var err error
			if release, err = xray.OpenHolder(c.Path); err != nil {
				out = append(out, map[string]any{"target": "lab:" + c.Name, "error": err.Error()})
				continue
			}
		}
		rep, err := xray.Scan(c.Path, xray.Options{})
		if release != nil {
			release()
		}
		if err != nil {
			out = append(out, map[string]any{"target": "lab:" + c.Name, "error": err.Error()})
			continue
		}
		sr := s.storeReport("lab:"+c.Name, rep)
		matched := false
		for _, f := range rep.Findings {
			if f.Code == c.ExpectCode && f.Severity == c.ExpectSeverity && f.Recovery == c.ExpectRecovery {
				matched = true
			}
		}
		out = append(out, map[string]any{"target": "lab:" + c.Name, "report": summarize(sr), "expected": c.ExpectCode, "matched": matched})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// noteOp records a failed operation on the main database as an X-RAY
// finding (xray.FromError: errors.Is on engine sentinels only).
func (s *Server) noteOp(op string, err error) {
	f, ok := xray.FromError(op, s.dbAbs, err, time.Now())
	if !ok {
		return
	}
	s.xray.mu.Lock()
	s.xray.ops = append(s.xray.ops, f)
	if len(s.xray.ops) > xrayKeep {
		s.xray.ops = s.xray.ops[len(s.xray.ops)-xrayKeep:]
	}
	s.xray.mu.Unlock()
}

// handleXRaySummary feeds the Overview: the latest scan of the live
// database (real counts from that scan, with its time), the most recent
// findings across scans, and operational findings from failed requests.
// Lab, demo and power-cut scans are counted separately from live health.
func (s *Server) handleXRaySummary(w http.ResponseWriter, r *http.Request) {
	s.xray.mu.Lock()
	defer s.xray.mu.Unlock()
	var live *storedReport
	byKind := map[string]int{}
	for i := len(s.xray.reports) - 1; i >= 0; i-- {
		sr := s.xray.reports[i]
		byKind[targetKind(sr.Target)]++
		if live == nil && sr.Target == "main" {
			live = sr
		}
	}
	type recent struct {
		ReportID string `json:"report_id"`
		Target   string `json:"target"`
		Kind     string `json:"kind"`
		xray.Finding
	}
	rec := []recent{}
	for i := len(s.xray.reports) - 1; i >= 0 && len(rec) < 8; i-- {
		sr := s.xray.reports[i]
		for _, f := range sr.Report.Findings {
			if f.Severity == xray.Info || len(rec) >= 8 {
				continue
			}
			rec = append(rec, recent{sr.ID, sr.Target, targetKind(sr.Target), f})
		}
	}
	ops := append([]xray.Finding(nil), s.xray.ops...)
	sort.Slice(ops, func(i, j int) bool { return ops[i].Timestamp.After(ops[j].Timestamp) })
	out := map[string]any{"recent_findings": rec, "operational_findings": ops, "scans_by_kind": byKind}
	if live != nil {
		out["live"] = summarize(live)
	} else {
		out["live"] = nil
		out["live_note"] = "the main database has not been scanned by this adapter yet (POST /api/xray/scan {\"target\":\"main\"})"
	}
	writeJSON(w, http.StatusOK, out)
}
