package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

// ---- health and overview ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"db_path":    s.dbAbs,
		"db_open":    true,
		"started_at": s.started.UTC().Format(time.RFC3339Nano),
		"tools": map[string]any{
			"kvdb":           nullable(s.kvdb),
			"nexus_torture":  nullable(s.torture.exe),
			"kvdb_mutant":    nullable(s.torture.mutant),
			"kvdb_bench":     nullable(s.torture.bench),
			"kvdb_perf":      nullable(s.torture.perf),
			"nexus_powercut": nullable(s.torture.powercut),
		},
		"torture_running": s.torture.running() != nil,
		"measured_at":     time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type lastOpen struct {
	At             string   `json:"at"`
	DurationMs     float64  `json:"duration_ms"`
	Created        bool     `json:"created"`
	Rules          []string `json:"rules"`
	TailRule       string   `json:"tail_rule"`
	DiscardedBytes int64    `json:"discarded_bytes"`
	Frames         uint64   `json:"frames"`
	Warnings       []string `json:"warnings"`
}

func recoveryJSON(at time.Time, d time.Duration, ri kvdb.RecoveryInfo) lastOpen {
	return lastOpen{
		At: at.UTC().Format(time.RFC3339Nano), DurationMs: ms(d), Created: ri.Created,
		Rules: nonNil(ri.Rules), TailRule: ri.Tail, DiscardedBytes: ri.DiscardedBytes,
		Frames: ri.Frames, Warnings: nonNil(ri.Warnings),
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type statsJSON struct {
	Keys      int    `json:"keys"`
	LiveBytes uint64 `json:"live_bytes"`
	Size      int64  `json:"size"`
	MaxBytes  int64  `json:"max_bytes"`
}

func (s *Server) stats() statsJSON {
	st := s.db.Stats()
	return statsJSON{st.Keys, st.LiveBytes, st.Size, st.MaxBytes}
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	totals, n := s.act.totalsCopy()
	s.mu.Lock()
	lc := s.lastCompact
	var lv *verifyResult
	if len(s.verifies) > 0 {
		v := s.verifies[len(s.verifies)-1]
		lv = &v
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"db_path":         s.dbAbs,
		"stats":           s.stats(),
		"format_version":  format.FormatVersion,
		"database":        strings.TrimSuffix(filepath.Base(s.dbAbs), ".kv"),
		"last_open":       recoveryJSON(s.openAt, s.openDur, s.openRec),
		"last_compaction": lc,
		"last_verify":     lv,
		"limits": map[string]int{
			"max_key_bytes": kvdb.MaxKeyLen, "max_value_bytes": kvdb.MaxValueLen, "max_batch_ops": kvdb.MaxBatchOps,
		},
		"adapter": map[string]any{
			"started_at":     s.started.UTC().Format(time.RFC3339Nano),
			"uptime_seconds": time.Since(s.started).Seconds(),
			"requests":       n,
			"by_op":          totals,
		},
		"source":      "kvdb.Stats, kvdb.Recovery, adapter timer around kvdb.Open, adapter request counters",
		"measured_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// ---- keys ----

var errStopRange = errors.New("stop")

type keyItem struct {
	Key          encodedBytes `json:"key"`
	ValueSize    int          `json:"value_size"`
	ValuePreview encodedBytes `json:"value_preview"`
	Truncated    bool         `json:"value_truncated"`
}

// handleListKeys pages through Range (key order). Range visits every key,
// so a page costs a full pass over the index; limit is capped at 500.
func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prefix, err := decodeBytes(q.Get("prefix"), q.Get("encoding"))
	if err != nil {
		engineError(w, err, false)
		return
	}
	after, err := decodeBytes(q.Get("after"), q.Get("encoding"))
	if err != nil {
		engineError(w, err, false)
		return
	}
	limit := intParam(r, "limit", 50, 1, 500)
	t0 := time.Now()
	items := []keyItem{}
	matched, more := 0, false
	err = s.db.Range(func(k, v []byte) error {
		if len(prefix) > 0 && !strings.HasPrefix(string(k), string(prefix)) {
			return nil
		}
		matched++
		if len(after) > 0 && string(k) <= string(after) {
			return nil
		}
		if len(items) == limit {
			more = true
			return errStopRange
		}
		p, cut := preview(v, 120)
		items = append(items, keyItem{encodeBytes(append([]byte(nil), k...)), len(v), p, cut})
		return nil
	})
	if err != nil && !errors.Is(err, errStopRange) {
		engineError(w, err, false)
		return
	}
	var next any
	if more && len(items) > 0 {
		next = items[len(items)-1].Key
	}
	s.act.record("list", string(prefix), "ok", time.Since(t0), "")
	writeJSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"next_after": next,
		"elapsed_ms": ms(time.Since(t0)),
		"note":       "pages are read with kvdb.Range, which visits keys in order and holds the read lock",
	})
}

func pathKey(r *http.Request) ([]byte, error) {
	return decodeBytes(r.PathValue("key"), r.URL.Query().Get("encoding"))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	key, err := pathKey(r)
	if err != nil {
		engineError(w, err, false)
		return
	}
	t0 := time.Now()
	v, err := s.db.Get(key)
	el := time.Since(t0)
	if errors.Is(err, kvdb.ErrNotFound) {
		s.act.record("get", string(key), "notfound", el, "")
		writeError(w, http.StatusNotFound, "notfound", "key not found", false)
		return
	}
	if err != nil {
		s.act.record("get", string(key), errClass(err), el, err.Error())
		engineError(w, err, false)
		return
	}
	s.act.record("get", string(key), "ok", el, "")
	writeJSON(w, http.StatusOK, map[string]any{
		"key": encodeBytes(key), "value": encodeBytes(v), "size": len(v), "elapsed_ms": ms(el),
	})
}

type putBody struct {
	Value    string `json:"value"`
	Encoding string `json:"encoding"`
}

func (s *Server) handlePut(w http.ResponseWriter, r *http.Request) {
	key, err := pathKey(r)
	if err != nil {
		engineError(w, err, true)
		return
	}
	var b putBody
	if !decodeBody(w, r, 3<<20, &b) {
		return
	}
	v, err := decodeBytes(b.Value, b.Encoding)
	if err != nil {
		engineError(w, err, true)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	had, herr := s.existed(key)
	t0 := time.Now()
	err = s.db.Put(key, v)
	el := time.Since(t0)
	if err != nil {
		s.act.record("put", string(key), errClass(err), el, err.Error())
		s.noteOp("put", err)
		engineError(w, err, true)
		return
	}
	action := "inserted"
	if had {
		action = "updated"
	} else if herr != nil {
		action = "written" // the pre-check failed; the write itself succeeded
	}
	s.act.record("put", string(key), "ok", el, action+", "+sizeDetail(len(v)))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "durable": true, "action": action, "size": len(v), "elapsed_ms": ms(el)})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	key, err := pathKey(r)
	if err != nil {
		engineError(w, err, true)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	had, herr := s.existed(key)
	t0 := time.Now()
	err = s.db.Delete(key)
	el := time.Since(t0)
	if err != nil {
		s.act.record("delete", string(key), errClass(err), el, err.Error())
		s.noteOp("delete", err)
		engineError(w, err, true)
		return
	}
	// Deleting a missing key is not an error in NEXUS and writes nothing.
	action := "deleted"
	if !had && herr == nil {
		action = "absent"
	}
	s.act.record("delete", string(key), "ok", el, action)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "durable": true, "action": action, "elapsed_ms": ms(el)})
}

type batchOp struct {
	Op       string `json:"op"` // "put" or "del"/"delete"
	Key      string `json:"key"`
	Value    string `json:"value"`
	Encoding string `json:"encoding"`
}

func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Ops []batchOp `json:"ops"`
	}
	if !decodeBody(w, r, 32<<20, &b) {
		return
	}
	ops := make([]kvdb.Op, 0, len(b.Ops))
	for _, o := range b.Ops {
		k, err := decodeBytes(o.Key, o.Encoding)
		if err != nil {
			engineError(w, err, true)
			return
		}
		switch o.Op {
		case "put":
			v, err := decodeBytes(o.Value, o.Encoding)
			if err != nil {
				engineError(w, err, true)
				return
			}
			ops = append(ops, kvdb.Op{Kind: kvdb.OpPut, Key: k, Value: v})
		case "del", "delete":
			if o.Value != "" {
				writeError(w, http.StatusBadRequest, "invalid", "a delete carries no value", false)
				return
			}
			ops = append(ops, kvdb.Op{Kind: kvdb.OpDelete, Key: k})
		default:
			writeError(w, http.StatusBadRequest, "invalid", `op must be "put" or "del"`, false)
			return
		}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	t0 := time.Now()
	err := s.db.Batch(ops)
	el := time.Since(t0)
	if err != nil {
		s.act.record("batch", "", errClass(err), el, err.Error())
		s.noteOp("batch", err)
		engineError(w, err, true)
		return
	}
	s.act.record("batch", "", "ok", el, plural(len(ops), "op"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "durable": true, "ops": len(ops), "elapsed_ms": ms(el)})
}

// ---- compaction and verification ----

type compactResult struct {
	At         string  `json:"at"`
	OK         bool    `json:"ok"`
	SizeBefore int64   `json:"size_before"`
	SizeAfter  int64   `json:"size_after"`
	Keys       int     `json:"keys"`
	ElapsedMs  float64 `json:"elapsed_ms"`
	Error      *apiErr `json:"error"`
}

type apiErr struct {
	Class   string `json:"class"`
	Message string `json:"message"`
}

func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	before := s.db.Stats().Size
	t0 := time.Now()
	err := s.db.Compact()
	el := time.Since(t0)
	st := s.db.Stats()
	res := &compactResult{At: t0.UTC().Format(time.RFC3339Nano), OK: err == nil, SizeBefore: before,
		SizeAfter: st.Size, Keys: st.Keys, ElapsedMs: ms(el)}
	if err != nil {
		res.Error = &apiErr{errClass(err), err.Error()}
	}
	s.mu.Lock()
	s.lastCompact = res
	s.mu.Unlock()
	if err != nil {
		s.act.record("compact", "", errClass(err), el, err.Error())
		s.noteOp("compact", err)
		engineError(w, err, true)
		return
	}
	s.act.record("compact", "", "ok", el, sizeDetail(int(before))+" → "+sizeDetail(int(st.Size)))
	writeJSON(w, http.StatusOK, res)
}

type verifyResult struct {
	At        string  `json:"at"`
	OK        bool    `json:"ok"`
	Keys      int     `json:"keys"`
	Frames    uint64  `json:"frames"`
	Size      int64   `json:"size"`
	LiveBytes uint64  `json:"live_bytes"`
	ElapsedMs float64 `json:"elapsed_ms"`
	Error     *apiErr `json:"error"`
	Method    string  `json:"method"`
}

// handleVerify re-reads and checks the whole main database file through the
// open handle (kvdb.Verify, the same check as `kvdb check`). A failed check
// is a result (200, ok=false), not a transport error.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	t0 := time.Now()
	rep, err := s.db.Verify()
	el := time.Since(t0)
	res := verifyResult{At: t0.UTC().Format(time.RFC3339Nano), OK: err == nil, Keys: rep.Keys, Frames: rep.Frames,
		Size: rep.Size, LiveBytes: rep.LiveBytes, ElapsedMs: ms(el),
		Method: "kvdb.Verify: superblock, every frame's header CRC, payload CRC, entry CRCs and sequence numbers, file ends on a frame boundary, index matches the file"}
	if err != nil {
		res.Error = &apiErr{errClass(err), err.Error()}
		s.act.record("verify", "", errClass(err), el, err.Error())
		s.noteOp("verify", err)
	} else {
		s.act.record("verify", "", "ok", el, plural(int(rep.Frames), "frame"))
	}
	s.mu.Lock()
	s.verifies = append(s.verifies, res)
	if len(s.verifies) > maxHistory {
		s.verifies = s.verifies[len(s.verifies)-maxHistory:]
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

// ---- storage ----

type fileInfo struct {
	Role     string `json:"role"`
	Name     string `json:"name"`
	Exists   bool   `json:"exists"`
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
	Meaning  string `json:"meaning"`
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	files := []fileInfo{
		statFile("data", s.dbAbs, "the database: a 32-byte superblock followed by CRC-checked frames, appended in commit order"),
		statFile("lock", s.dbAbs+".lock", "process lock (held while a process has the database open); always empty"),
		statFile("compaction-temp", s.dbAbs+".compact", "exists only while a compaction is being written, or after a crash during one"),
		statFile("compaction-old", s.dbAbs+".old", "exists only during the swap step of a compaction, or after a crash during it"),
	}
	s.mu.Lock()
	lc := s.lastCompact
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"directory":       filepath.Dir(s.dbAbs),
		"files":           files,
		"stats":           s.stats(),
		"last_compaction": lc,
		"layout": map[string]any{
			"kind":            "single-file append-only log",
			"wal":             "not applicable: the data file is itself the log; there is no separate write-ahead log",
			"levels":          "not applicable: no LSM levels or SSTables; one file, compacted in place by rewrite-and-swap",
			"auto_compaction": "the engine compacts automatically when dead bytes dominate; those runs are not reported individually",
		},
		"measured_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func statFile(role, path, meaning string) fileInfo {
	fi := fileInfo{Role: role, Name: filepath.Base(path), Meaning: meaning}
	if st, err := os.Stat(path); err == nil {
		fi.Exists, fi.Size, fi.Modified = true, st.Size(), st.ModTime().UTC().Format(time.RFC3339Nano)
	}
	return fi
}

// ---- recovery information of the main database ----

func (s *Server) handleRecovery(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	vs := append([]verifyResult(nil), s.verifies...)
	s.mu.Unlock()
	for i, j := 0, len(vs)-1; i < j; i, j = i+1, j-1 {
		vs[i], vs[j] = vs[j], vs[i]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"last_open":       recoveryJSON(s.openAt, s.openDur, s.openRec),
		"verify_history":  vs,
		"demo":            s.demo.state(),
		"read_only_check": "the main database is held open by nexus-api, so `kvdb check --no-repair` cannot lock it; use verify for the main database and the crash demo for the read-only check",
	})
}

// ---- small helpers ----

func errClass(err error) string {
	if err == nil {
		return "ok"
	}
	return classOf(err)
}

func sizeDetail(n int) string {
	switch {
	case n >= 1<<20:
		return trimFloat(float64(n)/(1<<20)) + " MiB"
	case n >= 1<<10:
		return trimFloat(float64(n)/(1<<10)) + " KiB"
	}
	return plural(n, "byte")
}
