package main

import (
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPutReportsInsertVersusUpdateAndDeleteAbsent(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	p := "/api/keys/" + url.PathEscape("city")
	if r := call(t, ts, "PUT", p, map[string]string{"value": "Pune"}); r.status != 200 || r.body["action"] != "inserted" {
		t.Fatalf("first put: %d %s", r.status, r.raw)
	}
	if r := call(t, ts, "PUT", p, map[string]string{"value": "Mumbai"}); r.body["action"] != "updated" {
		t.Fatalf("second put must be an update: %s", r.raw)
	}
	if r := call(t, ts, "DELETE", p, nil); r.body["action"] != "deleted" {
		t.Fatalf("delete: %s", r.raw)
	}
	if r := call(t, ts, "GET", p, nil); r.status != 404 || errClassOf(r) != "notfound" {
		t.Fatalf("get after delete must be notfound: %d %s", r.status, r.raw)
	}
	if r := call(t, ts, "DELETE", p, nil); r.status != 200 || r.body["action"] != "absent" {
		t.Fatalf("deleting a missing key is a no-op success: %d %s", r.status, r.raw)
	}
}

func TestDumpReturnsEveryRecordAndFilters(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	ops := []map[string]string{}
	for _, k := range []string{"user:1", "user:2", "order:9"} {
		ops = append(ops, map[string]string{"op": "put", "key": k, "value": "v-" + k})
	}
	if r := call(t, ts, "POST", "/api/batch", map[string]any{"ops": ops}); r.status != 200 {
		t.Fatalf("batch: %s", r.raw)
	}
	d := call(t, ts, "GET", "/api/dump", nil)
	if d.status != 200 || d.body["total_keys"].(float64) != 3 || len(d.body["items"].([]any)) != 3 {
		t.Fatalf("dump: %s", d.raw)
	}
	first := d.body["items"].([]any)[0].(map[string]any)
	if first["key"].(map[string]any)["data"] != "order:9" || first["value"].(map[string]any)["data"] != "v-order:9" {
		t.Fatalf("dump must be in key order with values: %v", first)
	}
	f := call(t, ts, "GET", "/api/dump?q=USER", nil)
	if f.body["matched"].(float64) != 2 {
		t.Fatalf("filter is a case-insensitive substring: %s", f.raw)
	}
}

func TestFramesMapTheRealLog(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	call(t, ts, "PUT", "/api/keys/a", map[string]string{"value": "1"})
	call(t, ts, "PUT", "/api/keys/a", map[string]string{"value": "22"})
	call(t, ts, "POST", "/api/batch", map[string]any{"ops": []map[string]string{
		{"op": "put", "key": "b", "value": "x"}, {"op": "del", "key": "a"}}})
	m := call(t, ts, "GET", "/api/storage/frames", nil)
	if m.status != 200 {
		t.Fatalf("frames: %d %s", m.status, m.raw)
	}
	sb := m.body["superblock"].(map[string]any)
	if sb["valid"] != true || sb["magic"] != "KIPDBLOG" || sb["version"].(float64) != 1 {
		t.Fatalf("superblock: %v", sb)
	}
	frames := m.body["frames"].([]any)
	if m.body["total_frames"].(float64) != 3 || len(frames) != 3 || m.body["last_seq"].(float64) != 3 {
		t.Fatalf("three writes are three frames: %s", m.raw)
	}
	stop := m.body["stop"].(map[string]any)
	if stop["kind"] != "clean" || stop["offset"] != m.body["file_size"] {
		t.Fatalf("a healthy file ends on a frame boundary: %v", stop)
	}
	batch := frames[2].(map[string]any)
	if batch["op_count"].(float64) != 2 || len(batch["entries"].([]any)) != 2 {
		t.Fatalf("the batch is one frame with two entries: %v", batch)
	}
	first := frames[0].(map[string]any)
	// 32-byte frame header + 11-byte entry header + key "a" + value "1".
	if first["offset"].(float64) != 32 || first["size"].(float64) != 32+11+1+1 {
		t.Fatalf("first frame layout: %v", first)
	}
	idx := m.body["index_sample"].([]any)
	if m.body["index_keys"].(float64) != 1 || len(idx) != 1 || idx[0].(map[string]any)["key"].(map[string]any)["data"] != "b" {
		t.Fatalf("only b is live after the batch deleted a: %v", idx)
	}
}

func TestDemoFramesShowATornTail(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	call(t, ts, "POST", "/api/recovery/demo/reset", map[string]int{"keys": 20})
	call(t, ts, "POST", "/api/recovery/demo/tear", map[string]int{"bytes": 5})
	m := call(t, ts, "GET", "/api/recovery/demo/frames", nil)
	stop := m.body["stop"].(map[string]any)
	if m.status != 200 || stop["kind"] != "damaged" || m.body["total_frames"].(float64) != 0 {
		t.Fatalf("a torn single-frame file must stop as damaged with no complete frame: %s", m.raw)
	}
	if stop["bytes_after"].(float64) <= 0 {
		t.Fatalf("damaged stop must report the bytes after the valid end: %v", stop)
	}
}

func TestOpenAndCreateDatabasesStayInsideTheDataDirectory(t *testing.T) {
	s, ts := newTestServer(t, Config{})
	call(t, ts, "PUT", "/api/keys/k", map[string]string{"value": "main"})
	for _, bad := range []string{"../escape", "a/b", `c:\x`, "", ".hidden", "x y"} {
		if r := call(t, ts, "POST", "/api/databases/open", map[string]any{"name": bad, "create": true}); r.status != 400 {
			t.Fatalf("name %q must be rejected: %d %s", bad, r.status, r.raw)
		}
	}
	if r := call(t, ts, "POST", "/api/databases/open", map[string]any{"name": "other"}); r.status != 404 {
		t.Fatalf("opening a missing database without create: %d %s", r.status, r.raw)
	}
	r := call(t, ts, "POST", "/api/databases/open", map[string]any{"name": "other", "create": true})
	if r.status != 200 || r.body["action"] != "created" {
		t.Fatalf("create: %d %s", r.status, r.raw)
	}
	if !fileExists(filepath.Join(filepath.Dir(s.dbAbs), "other.kv")) {
		t.Fatal("other.kv must be created next to main.kv")
	}
	if g := call(t, ts, "GET", "/api/keys/k", nil); g.status != 404 {
		t.Fatalf("the new database is empty: %s", g.raw)
	}
	list := call(t, ts, "GET", "/api/databases", nil)
	if list.body["current"] != "other" || len(list.body["files"].([]any)) != 2 {
		t.Fatalf("list: %s", list.raw)
	}
	if r := call(t, ts, "POST", "/api/databases/open", map[string]any{"name": "main"}); r.status != 200 || r.body["action"] != "opened" {
		t.Fatalf("reopen main: %s", r.raw)
	}
	if g := call(t, ts, "GET", "/api/keys/k", nil); g.status != 200 {
		t.Fatalf("main still holds its key: %s", g.raw)
	}
}

func TestBenchAndMutantsReportMissingToolsAndValidate(t *testing.T) {
	_, ts := newTestServer(t, Config{})
	if r := call(t, ts, "POST", "/api/bench/runs", map[string]int{"n": 1000, "runs": 1}); r.status != 503 {
		t.Fatalf("bench without kvdb-bench must be unavailable: %d %s", r.status, r.raw)
	}
	c := call(t, ts, "GET", "/api/torture/mutants", nil)
	if c.status != 200 || c.body["available"] != false || len(c.body["mutations"].([]any)) != 7 {
		t.Fatalf("mutation catalog: %s", c.raw)
	}
	if r := call(t, ts, "POST", "/api/torture/runs/nope/replay", map[string]int{"iteration": 1}); r.status != 503 && r.status != 404 {
		t.Fatalf("replay of an unknown run: %d %s", r.status, r.raw)
	}
}

func TestBenchRunsTheRealTool(t *testing.T) {
	if testing.Short() {
		t.Skip("builds kvdb-bench")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "kvdb-bench"+exeSuffix())
	if out, err := goBuild(exe, "github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/cmd/kvdb-bench"); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	s, ts := newTestServer(t, Config{})
	s.torture.bench = exe
	if r := call(t, ts, "POST", "/api/bench/runs", map[string]int{"n": 500, "runs": 1}); r.status != 400 {
		t.Fatalf("n below 2000 must be rejected: %s", r.raw)
	}
	r := call(t, ts, "POST", "/api/bench/runs", map[string]int{"n": 2000, "runs": 1})
	if r.status != 202 {
		t.Fatalf("start: %d %s", r.status, r.raw)
	}
	id := r.body["run"].(map[string]any)["id"].(string)
	st := waitRun(t, ts, id)
	if st.body["run"].(map[string]any)["state"] != "passed" {
		t.Fatalf("bench run: %s", st.raw)
	}
	sum, ok := st.body["summary"].(map[string]any)
	if !ok || len(sum["median_of_runs"].([]any)) != 7 {
		t.Fatalf("bench.json must be returned as the summary: %s", st.raw)
	}
	if p := st.body["progress"].(map[string]any); p["completed"].(float64) != 7 {
		t.Fatalf("progress counts 7 phases: %v", p)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.WorkDir, "torture", id, "bench.json")); err != nil {
		t.Fatal(err)
	}
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func goBuild(out, pkg string) ([]byte, error) {
	return exec.Command("go", "build", "-o", out, pkg).CombinedOutput()
}

// waitRun polls a run until it leaves the running state (at most 3 minutes).
func waitRun(t *testing.T, ts *httptest.Server, id string) resp {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	var s resp
	for time.Now().Before(deadline) {
		s = call(t, ts, "GET", "/api/torture/runs/"+id, nil)
		if s.body["run"].(map[string]any)["state"] != "running" {
			return s
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("run %s did not finish: %s", id, s.raw)
	return s
}

func TestMutationIsDetectedThroughAdapter(t *testing.T) {
	kv, tor := buildTools(t)
	mut := filepath.Join(t.TempDir(), "kvdb-mutant"+exeSuffix())
	if out, err := goBuild(mut, "github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/mutants/kvdb-mutant"); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	s, ts := newTestServer(t, Config{KvdbPath: kv, TorturePath: tor})
	s.torture.mutant = mut
	if r := call(t, ts, "POST", "/api/torture/mutants", map[string]any{"only": []string{"no-such"}}); r.status != 400 {
		t.Fatalf("unknown mutation accepted: %s", r.raw)
	}
	r := call(t, ts, "POST", "/api/torture/mutants", map[string]any{"only": []string{"drop-acked"}, "iterations": 10})
	if r.status != 202 {
		t.Fatalf("start: %d %s", r.status, r.raw)
	}
	st := waitRun(t, ts, r.body["run"].(map[string]any)["id"].(string))
	sum, ok := st.body["summary"].(map[string]any)
	if !ok {
		t.Fatalf("no mutation report: %s", st.raw)
	}
	outs := sum["outcomes"].([]any)
	o := outs[0].(map[string]any)
	if len(outs) != 1 || o["mutation"] != "drop-acked" || o["detected"] != true || o["as_expected"] != true {
		t.Fatalf("drop-acked must be detected: %v", sum)
	}
	if st.body["run"].(map[string]any)["state"] != "passed" {
		t.Fatalf("all mutations as expected exits 0: %s", st.raw)
	}
}
