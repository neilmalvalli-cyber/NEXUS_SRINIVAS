package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestServer(t *testing.T, cfg Config) (*Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(dir, "main.kv")
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = filepath.Join(dir, "work")
	}
	if cfg.KvdbPath == "" {
		cfg.KvdbPath = filepath.Join(dir, "no-such-kvdb")
	}
	if cfg.TorturePath == "" {
		cfg.TorturePath = filepath.Join(dir, "no-such-torture")
	}
	if cfg.EvidenceDir == "" {
		cfg.EvidenceDir = filepath.Join(dir, "evidence")
	}
	cfg.AllowOrigin = "http://localhost:3000"
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(func() {
		ts.Close()
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return s, ts
}

type resp struct {
	status int
	body   map[string]any
	raw    []byte
}

func call(t *testing.T, ts *httptest.Server, method, path string, body any) resp {
	t.Helper()
	var rd *bytes.Reader
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(res.Body)
	out := resp{status: res.StatusCode, raw: buf.Bytes()}
	json.Unmarshal(buf.Bytes(), &out.body)
	return out
}

func errClassOf(r resp) string {
	e, _ := r.body["error"].(map[string]any)
	c, _ := e["class"].(string)
	return c
}

func TestHealthAndOverview(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	h := call(t, ts, "GET", "/api/health", nil)
	if h.status != 200 || h.body["status"] != "ok" {
		t.Fatalf("health: %d %s", h.status, h.raw)
	}
	tools := h.body["tools"].(map[string]any)
	if tools["kvdb"] != nil || tools["nexus_torture"] != nil {
		t.Fatalf("missing tools must be null: %v", tools)
	}
	o := call(t, ts, "GET", "/api/overview", nil)
	st := o.body["stats"].(map[string]any)
	if o.status != 200 || st["keys"].(float64) != 0 || st["size"].(float64) != 32 {
		t.Fatalf("overview: %s", o.raw)
	}
	lo := o.body["last_open"].(map[string]any)
	if lo["created"] != true {
		t.Fatalf("a new database must report created: %v", lo)
	}
}

func TestPutGetDeleteRoundTrip(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	key := "user/42 profile" // slash and space in the key
	p := "/api/keys/" + url.PathEscape(key)
	if r := call(t, ts, "PUT", p, map[string]string{"value": `{"name":"Ada"}`}); r.status != 200 || r.body["durable"] != true {
		t.Fatalf("put: %d %s", r.status, r.raw)
	}
	g := call(t, ts, "GET", p, nil)
	v := g.body["value"].(map[string]any)
	if g.status != 200 || v["data"] != `{"name":"Ada"}` || v["encoding"] != "utf8" {
		t.Fatalf("get: %d %s", g.status, g.raw)
	}
	if r := call(t, ts, "DELETE", p, nil); r.status != 200 {
		t.Fatalf("delete: %d %s", r.status, r.raw)
	}
	g = call(t, ts, "GET", p, nil)
	if g.status != 404 || errClassOf(g) != "notfound" {
		t.Fatalf("get after delete: %d %s", g.status, g.raw)
	}
	// Deleting a missing key succeeds, as in the engine.
	if r := call(t, ts, "DELETE", "/api/keys/never-existed", nil); r.status != 200 {
		t.Fatalf("delete missing: %d %s", r.status, r.raw)
	}
}

func TestBinaryValuesUseBase64(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	raw := []byte{0xff, 0x00, 0xfe, 'a'}
	b64 := base64.StdEncoding.EncodeToString(raw)
	if r := call(t, ts, "PUT", "/api/keys/bin", map[string]string{"value": b64, "encoding": "base64"}); r.status != 200 {
		t.Fatalf("put: %s", r.raw)
	}
	g := call(t, ts, "GET", "/api/keys/bin", nil)
	v := g.body["value"].(map[string]any)
	if v["encoding"] != "base64" || v["data"] != b64 {
		t.Fatalf("binary value: %s", g.raw)
	}
}

func TestValidationErrors(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	cases := []struct {
		name, method, path string
		body               any
		status             int
		class              string
	}{
		{"key too long", "PUT", "/api/keys/" + strings.Repeat("k", 1025), map[string]string{"value": "v"}, 400, "invalid"},
		{"value too large", "PUT", "/api/keys/big", map[string]string{"value": strings.Repeat("v", 1<<20+1)}, 413, "too-large"},
		{"malformed json", "PUT", "/api/keys/x", "{not json", 400, "invalid"},
		{"empty body", "PUT", "/api/keys/x", "", 400, "invalid"},
		{"unknown field", "PUT", "/api/keys/x", `{"value":"v","extra":1}`, 400, "invalid"},
		{"bad encoding", "PUT", "/api/keys/x", map[string]string{"value": "v", "encoding": "hex"}, 400, "invalid"},
		{"bad base64", "PUT", "/api/keys/x", map[string]string{"value": "!!", "encoding": "base64"}, 400, "invalid"},
		{"bad batch op", "POST", "/api/batch", map[string]any{"ops": []map[string]string{{"op": "inc", "key": "a"}}}, 400, "invalid"},
		{"unknown endpoint", "GET", "/api/nope", nil, 404, "not-found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := call(t, ts, c.method, c.path, c.body)
			if r.status != c.status || errClassOf(r) != c.class {
				t.Fatalf("got %d %s, want %d %s", r.status, r.raw, c.status, c.class)
			}
		})
	}
}

func TestLargestValueRoundTrip(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	v := strings.Repeat("x", 1<<20) // exactly the 1 MiB limit
	if r := call(t, ts, "PUT", "/api/keys/max", map[string]string{"value": v}); r.status != 200 {
		t.Fatalf("put 1 MiB: %d %s", r.status, r.raw)
	}
	g := call(t, ts, "GET", "/api/keys/max", nil)
	if g.body["size"].(float64) != 1<<20 || g.body["value"].(map[string]any)["data"] != v {
		t.Fatal("1 MiB value did not round-trip")
	}
}

func TestBatchAndListKeys(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	var ops []map[string]string
	for i := 0; i < 30; i++ {
		ops = append(ops, map[string]string{"op": "put", "key": fmt.Sprintf("a/%02d", i), "value": fmt.Sprintf("v%d", i)})
	}
	ops = append(ops, map[string]string{"op": "put", "key": "b/1", "value": "x"}, map[string]string{"op": "del", "key": "a/00"})
	if r := call(t, ts, "POST", "/api/batch", map[string]any{"ops": ops}); r.status != 200 || r.body["ops"].(float64) != 32 {
		t.Fatalf("batch: %s", r.raw)
	}
	var got []string
	after := ""
	for page := 0; page < 10; page++ {
		r := call(t, ts, "GET", "/api/keys?prefix=a/&limit=10&after="+url.QueryEscape(after), nil)
		for _, it := range r.body["items"].([]any) {
			got = append(got, it.(map[string]any)["key"].(map[string]any)["data"].(string))
		}
		next, ok := r.body["next_after"].(map[string]any)
		if !ok {
			break
		}
		after = next["data"].(string)
	}
	if len(got) != 29 || got[0] != "a/01" || got[28] != "a/29" {
		t.Fatalf("paged listing: %d keys %v", len(got), got)
	}
}

func TestCompactVerifyStorage(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	for i := 0; i < 50; i++ {
		call(t, ts, "PUT", fmt.Sprintf("/api/keys/k%d", i%5), map[string]string{"value": strings.Repeat("v", 200)})
	}
	c := call(t, ts, "POST", "/api/compact", nil)
	if c.status != 200 || c.body["ok"] != true || c.body["size_after"].(float64) >= c.body["size_before"].(float64) {
		t.Fatalf("compact: %s", c.raw)
	}
	v := call(t, ts, "POST", "/api/verify", nil)
	if v.status != 200 || v.body["ok"] != true || v.body["keys"].(float64) != 5 {
		t.Fatalf("verify: %s", v.raw)
	}
	st := call(t, ts, "GET", "/api/storage", nil)
	files := st.body["files"].([]any)
	if f := files[0].(map[string]any); f["role"] != "data" || f["exists"] != true || f["size"].(float64) != c.body["size_after"].(float64) {
		t.Fatalf("storage: %s", st.raw)
	}
	if st.body["last_compaction"] == nil {
		t.Fatal("last compaction not reported")
	}
	rec := call(t, ts, "GET", "/api/recovery", nil)
	if len(rec.body["verify_history"].([]any)) != 1 {
		t.Fatalf("verify history: %s", rec.raw)
	}
}

func TestSizeCapRejectsCleanly(t *testing.T) {
	_, ts := newTestServer(t, Config{MaxBytes: 4096})
	var last resp
	for i := 0; i < 100; i++ {
		last = call(t, ts, "PUT", fmt.Sprintf("/api/keys/k%03d", i), map[string]string{"value": strings.Repeat("v", 100)})
		if last.status != 200 {
			break
		}
	}
	if last.status != 507 || errClassOf(last) != "full" || last.body["error"].(map[string]any)["outcome_unknown"] != false {
		t.Fatalf("expected 507 full: %d %s", last.status, last.raw)
	}
}

func TestActivityAndMetricsCountOnlyAdapterRequests(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	call(t, ts, "PUT", "/api/keys/a", map[string]string{"value": "1"})
	call(t, ts, "GET", "/api/keys/a", nil)
	call(t, ts, "GET", "/api/keys/missing", nil)
	a := call(t, ts, "GET", "/api/activity?limit=10", nil)
	items := a.body["items"].([]any)
	if a.body["scope"] != "adapter" || len(items) != 3 || items[0].(map[string]any)["result"] != "notfound" {
		t.Fatalf("activity: %s", a.raw)
	}
	m := call(t, ts, "GET", "/api/metrics?window=60&step=5", nil)
	tot := m.body["totals"].(map[string]any)
	if tot["get"].(map[string]any)["count"].(float64) != 2 || tot["get"].(map[string]any)["errors"].(float64) != 0 {
		t.Fatalf("metrics: %s", m.raw)
	}
	if len(m.body["buckets"].([]any)) != 12 {
		t.Fatalf("expected 12 buckets of 5 s in 60 s: %s", m.raw)
	}
}

func TestCORSAndPreflight(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	req, _ := http.NewRequest("OPTIONS", ts.URL+"/api/keys/a", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("preflight: %d %v", res.StatusCode, res.Header)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/health", nil)
	req.Header.Set("Origin", "http://evil.example")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unlisted origin must not get CORS headers")
	}
}

func TestToolsMissingIsReportedNotFaked(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	for _, p := range []string{"/api/torture/modes"} {
		r := call(t, ts, "GET", p, nil)
		if r.status != 503 || errClassOf(r) != "unavailable" {
			t.Fatalf("%s: %d %s", p, r.status, r.raw)
		}
	}
	r := call(t, ts, "POST", "/api/torture/runs", map[string]any{"mode": "default", "seed": "1", "iterations": 1})
	if r.status != 503 {
		t.Fatalf("start run without tools: %d %s", r.status, r.raw)
	}
	sc := call(t, ts, "GET", "/api/torture/scenarios", nil)
	if len(sc.body["scenarios"].([]any)) != 32 {
		t.Fatalf("catalog: %s", sc.raw)
	}
}

func TestEvidenceIsReadOnlyAndConfined(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bench.json"), []byte(`{"tool":"kvdb-bench"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "secret.exe"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "torture"), 0o755)
	os.WriteFile(filepath.Join(dir, "torture", "a.log"), []byte("RESULT: PASS"), 0o644)
	_, ts := newTestServer(t, Config{EvidenceDir: dir})
	l := call(t, ts, "GET", "/api/evidence", nil)
	if n := len(l.body["files"].([]any)); n != 2 {
		t.Fatalf("listing: %s", l.raw)
	}
	if r := call(t, ts, "GET", "/api/evidence/torture/a.log", nil); r.status != 200 || string(r.raw) != "RESULT: PASS" {
		t.Fatalf("file: %d %s", r.status, r.raw)
	}
	for _, p := range []string{"/api/evidence/secret.exe", "/api/evidence/../server.go", "/api/evidence/%2e%2e/x.json"} {
		if r := call(t, ts, "GET", p, nil); r.status != 404 {
			t.Fatalf("%s: %d", p, r.status)
		}
	}
	b := call(t, ts, "GET", "/api/benchmarks/latest", nil)
	if b.body["kind"] != "recorded" {
		t.Fatalf("benchmark: %s", b.raw)
	}
}

func TestDemoInspectTearRecoverInProcess(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	if r := call(t, ts, "POST", "/api/recovery/demo/reset", map[string]int{"keys": 100}); r.status != 200 {
		t.Fatalf("reset: %s", r.raw)
	}
	in := call(t, ts, "POST", "/api/recovery/demo/inspect", nil)
	li := in.body["last_inspect"].(map[string]any)
	if li["ok"] != true || li["needs_repair"] != false || li["keys"].(float64) != 100 {
		t.Fatalf("inspect clean: %s", in.raw)
	}
	if r := call(t, ts, "POST", "/api/recovery/demo/tear", map[string]int{"bytes": 1}); r.status != 200 {
		t.Fatalf("tear: %s", r.raw)
	}
	in = call(t, ts, "POST", "/api/recovery/demo/inspect", nil)
	li = in.body["last_inspect"].(map[string]any)
	if li["needs_repair"] != true || li["tail_rule"] == "" {
		t.Fatalf("inspect torn: %s", in.raw)
	}
	rc := call(t, ts, "POST", "/api/recovery/demo/recover", nil)
	lr := rc.body["last_recovery"].(map[string]any)
	open := lr["open"].(map[string]any)
	if lr["ok"] != true || open["tail_rule"] == "" || open["discarded_bytes"].(float64) <= 0 || lr["file_verified"] != true {
		t.Fatalf("recover: %s", rc.raw)
	}
	// The batch wrote 100 keys in ONE frame; cutting a byte tears that frame,
	// so recovery returns to the empty state (all or nothing).
	if lr["keys"].(float64) != 0 {
		t.Fatalf("torn batch must vanish completely: %s", rc.raw)
	}
}

// ---- tests that need the real executables (skipped with -short) ----

var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

func buildTools(t *testing.T) (kvdbExe, tortureExe string) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds kvdb and nexus-torture")
	}
	buildOnce.Do(func() {
		binDir, buildErr = os.MkdirTemp("", "nexus-api-bin")
		if buildErr != nil {
			return
		}
		cmd := exec.Command("go", "build", "-o", binDir+string(os.PathSeparator),
			"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/cmd/kvdb",
			"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/cmd/nexus-torture")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("%v: %s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return filepath.Join(binDir, "kvdb"+ext), filepath.Join(binDir, "nexus-torture"+ext)
}

func TestDemoRealCrashLosesNoAcknowledgedWrite(t *testing.T) {
	kv, _ := buildTools(t)
	_, ts := newTestServer(t, Config{KvdbPath: kv})
	call(t, ts, "POST", "/api/recovery/demo/reset", map[string]int{"keys": 10})
	cr := call(t, ts, "POST", "/api/recovery/demo/crash", map[string]int{"writes": 5000, "kill_after_ms": 300})
	lc := cr.body["last_crash"].(map[string]any)
	if cr.status != 200 || lc["acked"].(float64) == 0 || lc["exited_before_kill"] == true {
		t.Fatalf("crash: %d %s", cr.status, cr.raw)
	}
	rc := call(t, ts, "POST", "/api/recovery/demo/recover", nil)
	lr := rc.body["last_recovery"].(map[string]any)
	if lr["ok"] != true || lr["acked_missing"].(float64) != 0 || lr["acked_wrong_value"].(float64) != 0 ||
		lr["acked_checked"].(float64) != lc["acked"].(float64) {
		t.Fatalf("recover after real kill: %s", rc.raw)
	}
	t.Logf("sent %v, acked %v, unknown %v; recovered %v keys", lc["sent"], lc["acked"], lc["unknown"], lr["keys"])
}

func TestTortureRunThroughAdapter(t *testing.T) {
	kv, tor := buildTools(t)
	_, ts := newTestServer(t, Config{KvdbPath: kv, TorturePath: tor})
	m := call(t, ts, "GET", "/api/torture/modes", nil)
	if m.status != 200 || len(m.body["modes"].([]any)) < 12 {
		t.Fatalf("modes: %s", m.raw)
	}
	bad := call(t, ts, "POST", "/api/torture/runs", map[string]any{"mode": "default; rm -rf /", "seed": "1", "iterations": 1})
	if bad.status != 400 {
		t.Fatalf("invalid mode accepted: %s", bad.raw)
	}
	bad = call(t, ts, "POST", "/api/torture/runs", map[string]any{"mode": "default", "seed": "1 --dir C:\\", "iterations": 1})
	if bad.status != 400 {
		t.Fatalf("invalid seed accepted: %s", bad.raw)
	}
	st := call(t, ts, "POST", "/api/torture/runs", map[string]any{"mode": "default", "seed": "42", "iterations": 3})
	if st.status != 202 {
		t.Fatalf("start: %s", st.raw)
	}
	id := st.body["run"].(map[string]any)["id"].(string)
	if r := call(t, ts, "POST", "/api/torture/runs", map[string]any{"mode": "default", "seed": "1", "iterations": 1}); r.status != 409 {
		t.Fatalf("second concurrent run must be refused: %d", r.status)
	}
	deadline := time.Now().Add(2 * time.Minute)
	var s resp
	for time.Now().Before(deadline) {
		s = call(t, ts, "GET", "/api/torture/runs/"+id, nil)
		if s.body["run"].(map[string]any)["state"] != "running" {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	run := s.body["run"].(map[string]any)
	prog := s.body["progress"].(map[string]any)
	if run["state"] != "passed" || prog["completed"].(float64) != 3 || prog["passed"].(float64) != 3 || s.body["summary"] == nil {
		t.Fatalf("run: %s", s.raw)
	}
	lg := call(t, ts, "GET", "/api/torture/runs/"+id+"/log", nil)
	joined := fmt.Sprint(lg.body["lines"])
	if !strings.Contains(joined, "RESULT: PASS") {
		t.Fatalf("log: %s", lg.raw)
	}
}

func TestTortureCancelMarksInterrupted(t *testing.T) {
	kv, tor := buildTools(t)
	_, ts := newTestServer(t, Config{KvdbPath: kv, TorturePath: tor})
	st := call(t, ts, "POST", "/api/torture/runs", map[string]any{"mode": "default", "seed": "7", "iterations": 500})
	id := st.body["run"].(map[string]any)["id"].(string)
	time.Sleep(1500 * time.Millisecond)
	c := call(t, ts, "POST", "/api/torture/runs/"+id+"/cancel", nil)
	if s := c.body["run"].(map[string]any)["state"]; s != "interrupted" {
		t.Fatalf("cancel: state %v %s", s, c.raw)
	}
}
