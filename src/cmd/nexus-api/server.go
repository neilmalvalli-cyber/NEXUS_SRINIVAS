package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/cli"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

// Config is the adapter configuration (see the flags in main.go).
type Config struct {
	Addr        string
	DBPath      string
	MaxBytes    int64
	WorkDir     string
	KvdbPath    string
	TorturePath string
	EvidenceDir string
	AllowOrigin string
	PerfHistory string // recorded optimization history (JSON), served read-only
}

// Server is the HTTP adapter. It owns the main database handle.
type Server struct {
	cfg     Config
	db      *kvdb.DB
	dbAbs   string
	kvdb    string // kvdb executable ("" if not found)
	started time.Time

	openAt  time.Time
	openDur time.Duration
	openRec kvdb.RecoveryInfo

	act     *activity
	demo    *demo
	torture *tortureManager
	xray    *xrayStore

	// dbMu guards the db handle itself: requests that use the open
	// database hold it for reading, switching databases holds it for
	// writing. writeMu serialises the adapter's writes (and log scans) so
	// insert-vs-update answers and log maps are exact.
	dbMu    sync.RWMutex
	writeMu sync.Mutex

	mu          sync.Mutex
	lastCompact *compactResult
	verifies    []verifyResult // newest last, at most maxHistory

	mux *http.ServeMux
}

const maxHistory = 20

// New opens the main database and builds the router.
func New(cfg Config) (*Server, error) {
	if cfg.DBPath == "" {
		return nil, errors.New("--db is required")
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = filepath.Join(filepath.Dir(cfg.DBPath), "api-work")
	}
	abs, err := filepath.Abs(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, dbAbs: abs, started: time.Now(), act: newActivity()}
	t0 := time.Now()
	db, err := kvdb.Open(abs, cfg.MaxBytes)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", abs, err)
	}
	s.db, s.openAt, s.openDur, s.openRec = db, t0, time.Since(t0), db.Recovery()
	s.kvdb = findTool(cfg.KvdbPath, "kvdb")
	s.demo = &demo{dir: filepath.Join(cfg.WorkDir, "recovery-demo"), kvdb: s.kvdb}
	s.torture = &tortureManager{
		exe:      findTool(cfg.TorturePath, "nexus-torture"),
		kvdb:     s.kvdb,
		mutant:   findTool("", "kvdb-mutant"),
		bench:    findTool("", "kvdb-bench"),
		perf:     findTool("", "kvdb-perf"),
		powercut: findTool("", "nexus-powercut"),
		dir:      filepath.Join(cfg.WorkDir, "torture"),
		runs:     map[string]*tortureRun{},
	}
	s.xray = &xrayStore{dir: filepath.Join(cfg.WorkDir, "xray")}
	s.routes()
	return s, nil
}

// Close stops any running child process and closes the database.
func (s *Server) Close() error {
	s.torture.stopAll()
	return s.db.Close()
}

func (s *Server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", s.handleHealth)
	m.HandleFunc("GET /api/overview", s.handleOverview)
	m.HandleFunc("GET /api/activity", s.handleActivity)
	m.HandleFunc("GET /api/metrics", s.handleMetrics)

	m.HandleFunc("GET /api/keys", s.handleListKeys)
	m.HandleFunc("GET /api/keys/{key...}", s.handleGet)
	m.HandleFunc("PUT /api/keys/{key...}", s.handlePut)
	m.HandleFunc("DELETE /api/keys/{key...}", s.handleDelete)
	m.HandleFunc("POST /api/batch", s.handleBatch)
	m.HandleFunc("POST /api/compact", s.handleCompact)
	m.HandleFunc("POST /api/verify", s.handleVerify)
	m.HandleFunc("GET /api/storage", s.handleStorage)
	m.HandleFunc("GET /api/storage/frames", s.handleFrames)
	m.HandleFunc("GET /api/dump", s.handleDump)
	m.HandleFunc("GET /api/databases", s.handleDatabases)
	m.HandleFunc("POST /api/databases/open", s.handleOpenDatabase)

	m.HandleFunc("GET /api/recovery", s.handleRecovery)
	m.HandleFunc("GET /api/recovery/demo", s.handleDemoState)
	m.HandleFunc("POST /api/recovery/demo/reset", s.handleDemoReset)
	m.HandleFunc("POST /api/recovery/demo/crash", s.handleDemoCrash)
	m.HandleFunc("POST /api/recovery/demo/inspect", s.handleDemoInspect)
	m.HandleFunc("POST /api/recovery/demo/tear", s.handleDemoTear)
	m.HandleFunc("POST /api/recovery/demo/recover", s.handleDemoRecover)
	m.HandleFunc("GET /api/recovery/demo/frames", s.handleDemoFrames)

	m.HandleFunc("GET /api/torture/modes", s.handleTortureModes)
	m.HandleFunc("GET /api/torture/scenarios", s.handleScenarioCatalog)
	m.HandleFunc("POST /api/torture/scenarios", s.handleStartScenarios)
	m.HandleFunc("GET /api/torture/runs", s.handleListRuns)
	m.HandleFunc("POST /api/torture/runs", s.handleStartRun)
	m.HandleFunc("GET /api/torture/runs/{id}", s.handleRunStatus)
	m.HandleFunc("GET /api/torture/runs/{id}/log", s.handleRunLog)
	m.HandleFunc("POST /api/torture/runs/{id}/cancel", s.handleCancelRun)
	m.HandleFunc("POST /api/torture/runs/{id}/replay", s.handleReplay)
	m.HandleFunc("GET /api/torture/mutants", s.handleMutantCatalog)
	m.HandleFunc("POST /api/torture/mutants", s.handleStartMutants)
	m.HandleFunc("POST /api/bench/runs", s.handleStartBench)

	m.HandleFunc("GET /api/evidence", s.handleEvidence)
	m.HandleFunc("GET /api/evidence/{name...}", s.handleEvidenceFile)
	m.HandleFunc("GET /api/benchmarks/latest", s.handleBenchmark)

	m.HandleFunc("GET /api/xray/targets", s.handleXRayTargets)
	m.HandleFunc("POST /api/xray/scan", s.handleXRayScan)
	m.HandleFunc("POST /api/xray/repair", s.handleXRayRepair)
	m.HandleFunc("GET /api/xray/reports", s.handleXRayReports)
	m.HandleFunc("GET /api/xray/reports/{id}", s.handleXRayReport)
	m.HandleFunc("GET /api/xray/reports/{id}/export", s.handleXRayExport)
	m.HandleFunc("GET /api/xray/summary", s.handleXRaySummary)
	m.HandleFunc("GET /api/xray/lab", s.handleXRayLab)
	m.HandleFunc("POST /api/xray/lab/reset", s.handleXRayLabReset)
	m.HandleFunc("POST /api/xray/lab/scan-all", s.handleXRayLabScanAll)

	m.HandleFunc("GET /api/powercut", s.handlePowercutInfo)
	m.HandleFunc("POST /api/powercut/runs", s.handleStartPowercut)
	m.HandleFunc("GET /api/powercut/runs/{id}/images/{image}/xray", s.handlePowercutXRay)

	m.HandleFunc("POST /api/perf/runs", s.handleStartPerf)
	m.HandleFunc("GET /api/perf/history", s.handlePerfHistory)

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not-found", "no such endpoint: "+r.Method+" "+r.URL.Path, false)
	})
	s.mux = m
}

// ServeHTTP adds CORS for the configured dashboard origin(s).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); o != "" && s.originAllowed(o) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", o)
		h.Set("Vary", "Origin")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		h.Set("Access-Control-Max-Age", "600")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if usesOpenDB(r.URL.Path) {
		s.dbMu.RLock()
		defer s.dbMu.RUnlock()
	}
	s.mux.ServeHTTP(w, r)
}

// usesOpenDB reports whether a request reads or writes the open main
// database handle (and so must not overlap a database switch).
func usesOpenDB(p string) bool {
	if strings.HasPrefix(p, "/api/recovery/demo") || p == "/api/databases/open" {
		return false
	}
	for _, pre := range []string{"/api/health", "/api/overview", "/api/keys", "/api/batch", "/api/compact",
		"/api/verify", "/api/storage", "/api/dump", "/api/recovery", "/api/databases"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

func (s *Server) originAllowed(o string) bool {
	for _, a := range strings.Split(s.cfg.AllowOrigin, ",") {
		if a = strings.TrimSpace(a); a != "" && (a == "*" || a == o) {
			return true
		}
	}
	return false
}

// ---- JSON helpers ----

type apiError struct {
	Class          string `json:"class"`
	Message        string `json:"message"`
	OutcomeUnknown bool   `json:"outcome_unknown"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, class, msg string, unknown bool) {
	writeJSON(w, status, map[string]apiError{"error": {Class: class, Message: msg, OutcomeUnknown: unknown}})
}

// engineError maps an engine error to HTTP using the CLI's error classes
// (cli.ErrorClass), so the dashboard and the CLI never disagree. write is
// true for mutating calls: there an io/unknown error means the outcome is
// unknown.
func engineError(w http.ResponseWriter, err error, write bool) {
	class := cli.ErrorClass(err)
	status := http.StatusInternalServerError
	switch class {
	case "invalid":
		status = http.StatusBadRequest
	case "too-large":
		status = http.StatusRequestEntityTooLarge
	case "notfound":
		status = http.StatusNotFound
	case "locked":
		status = http.StatusConflict
	case "full":
		status = http.StatusInsufficientStorage
	case "closed":
		status = http.StatusServiceUnavailable
	}
	unknown := write && (class == "unknown" || class == "io")
	writeError(w, status, class, err.Error(), unknown)
}

// decodeBody reads a JSON body of at most limit bytes into v.
func decodeBody(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "too-large", fmt.Sprintf("request body larger than %d bytes", limit), false)
			return false
		}
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid", "empty request body (JSON expected)", false)
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid", "malformed JSON: "+err.Error(), false)
		return false
	}
	return true
}

// ---- byte strings in JSON ----

// encodedBytes carries a key or value: UTF-8 text as is, anything else as
// base64 (JSON strings cannot hold arbitrary bytes).
type encodedBytes struct {
	Data     string `json:"data"`
	Encoding string `json:"encoding"` // "utf8" or "base64"
}

func encodeBytes(b []byte) encodedBytes {
	if utf8.Valid(b) {
		return encodedBytes{string(b), "utf8"}
	}
	return encodedBytes{base64.StdEncoding.EncodeToString(b), "base64"}
}

func decodeBytes(s, enc string) ([]byte, error) {
	switch enc {
	case "", "utf8":
		return []byte(s), nil
	case "base64":
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid base64: %v", kvdb.ErrInvalidArgument, err)
		}
		return b, nil
	}
	return nil, fmt.Errorf("%w: encoding must be utf8 or base64, not %q", kvdb.ErrInvalidArgument, enc)
}

// preview returns at most n bytes of b for list views.
func preview(b []byte, n int) (encodedBytes, bool) {
	if len(b) <= n {
		return encodeBytes(b), false
	}
	p := b[:n]
	if utf8.Valid(b) {
		for i := 0; i < 3 && !utf8.Valid(p); i++ {
			p = p[:len(p)-1] // do not cut a multi-byte character of valid text
		}
	}
	return encodeBytes(p), true
}

func ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

func classOf(err error) string { return cli.ErrorClass(err) }

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	return strings.TrimSuffix(s, ".0")
}

// findTool resolves an executable: the flag value, else next to this
// executable, else the current directory. "" if none exists.
func findTool(flagValue, name string) string {
	if flagValue != "" {
		if p, err := filepath.Abs(flagValue); err == nil && fileExists(p) {
			return p
		}
		return ""
	}
	exe := name
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	var dirs []string
	if self, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(self))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd)
	}
	for _, d := range dirs {
		if p := filepath.Join(d, exe); fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
