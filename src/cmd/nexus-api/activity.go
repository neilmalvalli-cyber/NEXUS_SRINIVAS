package main

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// activity records the operations that went through THIS adapter: a ring of
// recent entries and per-second counters with latency. These are real
// measurements of requests served here, not engine-wide statistics; the API
// labels them that way ("scope": "adapter").
type activity struct {
	mu      sync.Mutex
	nextID  int64
	entries []activityEntry // ring, newest at the end
	secs    map[int64]*secBucket
	totals  map[string]*opTotals
}

type activityEntry struct {
	ID        int64   `json:"id"`
	At        string  `json:"at"`
	Op        string  `json:"op"`
	Key       string  `json:"key,omitempty"`
	Result    string  `json:"result"` // "ok", "notfound", or an error class
	ElapsedMs float64 `json:"elapsed_ms"`
	Detail    string  `json:"detail,omitempty"`
}

type secBucket struct {
	ops map[string]*opTotals
}

type opTotals struct {
	Count   int64   `json:"count"`
	Errors  int64   `json:"errors"`
	TotalMs float64 `json:"total_ms"`
	MaxMs   float64 `json:"max_ms"`
}

const (
	activityKeep = 200
	metricKeep   = 900 // seconds of per-second buckets
)

func newActivity() *activity {
	return &activity{secs: map[int64]*secBucket{}, totals: map[string]*opTotals{}}
}

// record adds one operation. ok=false counts as an error in the metrics
// (a GET of a missing key is not an error).
func (a *activity) record(op, key, result string, elapsed time.Duration, detail string) {
	now := time.Now()
	if len(key) > 96 {
		key = key[:96] + "…"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextID++
	a.entries = append(a.entries, activityEntry{
		ID: a.nextID, At: now.UTC().Format(time.RFC3339Nano), Op: op, Key: key,
		Result: result, ElapsedMs: ms(elapsed), Detail: detail,
	})
	if len(a.entries) > activityKeep {
		a.entries = append([]activityEntry(nil), a.entries[len(a.entries)-activityKeep:]...)
	}
	sec := now.Unix()
	b := a.secs[sec]
	if b == nil {
		b = &secBucket{ops: map[string]*opTotals{}}
		a.secs[sec] = b
		for s := range a.secs {
			if s < sec-metricKeep {
				delete(a.secs, s)
			}
		}
	}
	isErr := result != "ok" && result != "notfound"
	for _, t := range []*opTotals{bucketOp(b.ops, op), bucketOp(a.totals, op)} {
		t.Count++
		if isErr {
			t.Errors++
		}
		m := ms(elapsed)
		t.TotalMs += m
		if m > t.MaxMs {
			t.MaxMs = m
		}
	}
}

func bucketOp(m map[string]*opTotals, op string) *opTotals {
	t := m[op]
	if t == nil {
		t = &opTotals{}
		m[op] = t
	}
	return t
}

func (a *activity) recent(limit int) []activityEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := len(a.entries)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]activityEntry, 0, limit)
	for i := n - 1; i >= n-limit; i-- {
		out = append(out, a.entries[i])
	}
	return out
}

func (a *activity) totalsCopy() (map[string]opTotals, int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]opTotals{}
	var n int64
	for k, v := range a.totals {
		out[k] = *v
		n += v.Count
	}
	return out, n
}

type metricBucket struct {
	Start string              `json:"start"`
	Ops   map[string]opTotals `json:"ops"`
}

// series aggregates the last window seconds into buckets of step seconds.
func (a *activity) series(window, step int64) []metricBucket {
	now := time.Now().Unix()
	n := window / step
	if n < 1 {
		n = 1
	}
	last := now - now%step // start of the bucket holding the current second
	first := last - (n-1)*step
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []metricBucket
	for start := first; start <= now; start += step {
		mb := metricBucket{Start: time.Unix(start, 0).UTC().Format(time.RFC3339), Ops: map[string]opTotals{}}
		for s := start; s < start+step; s++ {
			b := a.secs[s]
			if b == nil {
				continue
			}
			for op, t := range b.ops {
				cur := mb.Ops[op]
				cur.Count += t.Count
				cur.Errors += t.Errors
				cur.TotalMs += t.TotalMs
				if t.MaxMs > cur.MaxMs {
					cur.MaxMs = t.MaxMs
				}
				mb.Ops[op] = cur
			}
		}
		out = append(out, mb)
	}
	return out
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", 50, 1, activityKeep)
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":       "adapter",
		"description": "operations served by this nexus-api process since it started (not engine-wide)",
		"items":       s.act.recent(limit),
		"measured_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	window := int64(intParam(r, "window", 300, 10, metricKeep))
	step := int64(intParam(r, "step", 5, 1, 60))
	totals, n := s.act.totalsCopy()
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":          "adapter",
		"description":    "per-operation counts and latency measured by nexus-api around each engine call it made; excludes work done by other processes and automatic compactions inside the engine",
		"window_seconds": window,
		"step_seconds":   step,
		"buckets":        s.act.series(window, step),
		"totals":         totals,
		"total_requests": n,
		"since":          s.started.UTC().Format(time.RFC3339Nano),
		"measured_at":    time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func intParam(r *http.Request, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
