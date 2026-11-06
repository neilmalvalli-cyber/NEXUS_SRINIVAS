package main

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func fileHash(t *testing.T, p string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// TestXRayMainScanIsReadOnlyAndCountsAreReal: scanning the live database
// changes nothing, the summary reflects that scan, and paths are refused.
func TestXRayMainScanIsReadOnlyAndCountsAreReal(t *testing.T) {
	s, ts := newTestServer(t, Config{})
	for i := 0; i < 20; i++ {
		call(t, ts, "PUT", "/api/keys/k"+string(rune('a'+i)), map[string]string{"value": "v"})
	}
	sum := call(t, ts, "GET", "/api/xray/summary", nil)
	if sum.status != 200 || sum.body["live"] != nil {
		t.Fatalf("before any scan, live must be null: %s", sum.raw)
	}
	before := fileHash(t, s.dbAbs)
	r := call(t, ts, "POST", "/api/xray/scan", map[string]string{"target": "main"})
	if r.status != 200 {
		t.Fatalf("scan: %d %s", r.status, r.raw)
	}
	rep := r.body["report"].(map[string]any)
	if rep["health"] != "healthy" || rep["modified"] != false || rep["live"] != true || rep["mode"] != "read-only" {
		t.Fatalf("report: %v", rep)
	}
	if fileHash(t, s.dbAbs) != before {
		t.Fatal("scan modified the main database")
	}
	sum = call(t, ts, "GET", "/api/xray/summary", nil)
	live := sum.body["live"].(map[string]any)
	if live["id"] != r.body["id"] || live["health"] != "healthy" {
		t.Fatalf("summary: %s", sum.raw)
	}
	// Writes keep working after the scan released writeMu.
	if p := call(t, ts, "PUT", "/api/keys/after", map[string]string{"value": "x"}); p.status != 200 {
		t.Fatalf("put after scan: %s", p.raw)
	}
	for _, bad := range []string{"", "../main.kv", "C:\\Windows\\win.ini", "lab:../../x", "lab:nope", "/etc/passwd"} {
		if r := call(t, ts, "POST", "/api/xray/scan", map[string]string{"target": bad}); r.status != 400 {
			t.Errorf("target %q accepted: %d %s", bad, r.status, r.raw)
		}
	}
	if r := call(t, ts, "POST", "/api/xray/scan", map[string]any{"target": "main", "path": "x"}); r.status != 400 {
		t.Errorf("unknown field accepted: %s", r.raw)
	}
	if r := call(t, ts, "POST", "/api/xray/repair", map[string]any{"target": "main", "confirm": true}); r.status != 403 {
		t.Errorf("repair of main must be forbidden: %d %s", r.status, r.raw)
	}
	exp := call(t, ts, "GET", "/api/xray/reports/"+r.body["id"].(string)+"/export", nil)
	if exp.status != 200 {
		t.Fatalf("export: %s", exp.raw)
	}
	if call(t, ts, "GET", "/api/xray/reports/x-nope", nil).status != 404 {
		t.Error("unknown report id")
	}
}

// TestXRayConcurrentScansAndWrites: scans of the live database interleave
// with writes without errors or a modified-file report from X-RAY itself.
func TestXRayConcurrentScansAndWrites(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	var wg sync.WaitGroup
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 15; i++ {
				if r := call(t, ts, "PUT", "/api/keys/c"+string(rune('a'+g))+string(rune('a'+i)), map[string]string{"value": "v"}); r.status != 200 {
					t.Errorf("put: %s", r.raw)
				}
			}
		}(g)
	}
	for i := 0; i < 5; i++ {
		r := call(t, ts, "POST", "/api/xray/scan", map[string]any{"target": "main", "no_hash": true})
		if r.status != 200 {
			t.Fatalf("scan: %s", r.raw)
		}
		if rep := r.body["report"].(map[string]any); rep["health"] != "healthy" || rep["modified"] != false {
			t.Errorf("scan during writes: health %v modified %v", rep["health"], rep["modified"])
		}
	}
	wg.Wait()
}

// TestXRayLabThroughAdapter builds the lab, scans every case, and repairs
// one torn tail after a confirmation.
func TestXRayLabThroughAdapter(t *testing.T) {
	s, ts := newTestServer(t, Config{})
	if r := call(t, ts, "POST", "/api/xray/lab/scan-all", nil); r.status != 409 {
		t.Fatalf("scan-all before build: %d", r.status)
	}
	b := call(t, ts, "POST", "/api/xray/lab/reset", nil)
	if b.status != 200 || len(b.body["cases"].([]any)) < 10 {
		t.Fatalf("lab: %s", b.raw)
	}
	all := call(t, ts, "POST", "/api/xray/lab/scan-all", nil)
	for _, x := range all.body["results"].([]any) {
		m := x.(map[string]any)
		if m["matched"] != true {
			t.Errorf("lab case %v not diagnosed as expected: %v", m["target"], m)
		}
	}
	tg := call(t, ts, "GET", "/api/xray/targets", nil)
	if tg.body["lab_built"] != true {
		t.Fatalf("targets: %s", tg.raw)
	}
	if r := call(t, ts, "POST", "/api/xray/repair", map[string]any{"target": "lab:torn-tail"}); r.status != 400 {
		t.Fatalf("repair without confirm: %d", r.status)
	}
	r := call(t, ts, "POST", "/api/xray/repair", map[string]any{"target": "lab:torn-tail", "confirm": true})
	rep := r.body["report"].(map[string]any)
	if r.status != 200 || rep["repair"].(map[string]any)["verified"] != true || rep["recovery"].(map[string]any)["status"] != "repaired" {
		t.Fatalf("repair: %s", r.raw)
	}
	r = call(t, ts, "POST", "/api/xray/repair", map[string]any{"target": "lab:committed-frame-corrupted", "confirm": true})
	rep = r.body["report"].(map[string]any)
	if rep["repair"].(map[string]any)["verified"] != false || rep["modified"] != false {
		t.Fatalf("repair of corruption must refuse: %s", r.raw)
	}
	if ents, _ := os.ReadDir(filepath.Join(s.xray.dir, "reports")); len(ents) == 0 {
		t.Fatal("reports not saved")
	}
	lst := call(t, ts, "GET", "/api/xray/reports?kind=lab", nil)
	if len(lst.body["reports"].([]any)) < 10 {
		t.Fatalf("report list: %s", lst.raw)
	}
}

func TestPowercutAndPerfValidateAndRun(t *testing.T) {
	s, ts := newTestServer(t, Config{})
	info := call(t, ts, "GET", "/api/powercut", nil)
	if info.status != 200 || info.body["available"] != false || info.body["disclaimer"] == "" {
		t.Fatalf("info: %s", info.raw)
	}
	if r := call(t, ts, "POST", "/api/powercut/runs", map[string]any{}); r.status != 503 {
		t.Fatalf("missing tool must be 503: %s", r.raw)
	}
	if r := call(t, ts, "POST", "/api/perf/runs", map[string]any{"quick": true}); r.status != 503 {
		t.Fatalf("missing kvdb-perf must be 503: %s", r.raw)
	}
	if testing.Short() {
		return
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "nexus-powercut"+exeSuffix())
	if out, err := goBuild(exe, "github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/cmd/nexus-powercut"); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	s.torture.powercut = exe
	for _, bad := range []map[string]any{{"writers": 0}, {"writers": 99}, {"ops_per_writer": 100000}, {"seed": -1}, {"max_crash_points": 0}, {"dir": "x"}} {
		if r := call(t, ts, "POST", "/api/powercut/runs", bad); r.status != 400 {
			t.Errorf("%v accepted: %d %s", bad, r.status, r.raw)
		}
	}
	r := call(t, ts, "POST", "/api/powercut/runs", map[string]any{"seed": 3, "writers": 2, "ops_per_writer": 12, "max_crash_points": 60, "max_variants_per_point": 4})
	if r.status != 202 {
		t.Fatalf("start: %d %s", r.status, r.raw)
	}
	id := r.body["run"].(map[string]any)["id"].(string)
	if b := call(t, ts, "POST", "/api/powercut/runs", map[string]any{}); b.status != 409 {
		t.Errorf("second concurrent run must be busy: %d", b.status)
	}
	st := waitRun(t, ts, id)
	if st.body["run"].(map[string]any)["state"] != "passed" {
		t.Fatalf("powercut run: %s", st.raw)
	}
	sum := st.body["summary"].(map[string]any)
	if sum["failed"].(float64) != 0 || sum["images_validated"].(float64) == 0 || sum["disclaimer"] == "" {
		t.Fatalf("summary: %v", sum)
	}
	if p := st.body["progress"].(map[string]any); p["crash_points_done"].(float64) == 0 {
		t.Fatalf("progress: %v", p)
	}
	if x := call(t, ts, "GET", "/api/powercut/runs/"+id+"/images/..%2f..%2fx/xray", nil); x.status != 404 {
		t.Errorf("image path traversal: %d", x.status)
	}
}
