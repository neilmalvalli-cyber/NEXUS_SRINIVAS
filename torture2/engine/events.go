package engine

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"sync"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/oracle"
)

// Events is the JSONL event log: one JSON object per line for every request
// sent, every reply, every kill and recovery step, and every verdict.
type Events struct {
	mu sync.Mutex
	f  *os.File
	w  *bufio.Writer
}

// NewEvents creates the log at path.
func NewEvents(path string) (*Events, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &Events{f: f, w: bufio.NewWriterSize(f, 1<<20)}, nil
}

// Write appends one event.
func (e *Events) Write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]any{"ev": "encode-error", "err": err.Error()})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.w.Write(b)
	e.w.WriteByte('\n')
}

// Send logs a request (effects summarized; values are logged as hashes).
func (e *Events) Send(iter int, o *oracle.Op) {
	ev := map[string]any{"ev": "send", "iter": iter, "op": o.ID, "writer": o.Writer, "kind": o.Kind.String(), "t_ns": o.Start}
	if o.Kind == oracle.KGet {
		ev["key"] = o.Key
	}
	if n := len(o.Muts); n > 0 {
		ev["nops"] = n
		if n <= 32 {
			eff := map[string]string{}
			for k, c := range o.Effects() {
				eff[k] = c.String()
			}
			ev["effects"] = eff
		}
	}
	e.Write(ev)
}

// Result logs a reply.
func (e *Events) Result(iter int, o *oracle.Op) {
	ev := map[string]any{"ev": "reply", "iter": iter, "op": o.ID, "status": o.Status.String(), "t_ns": o.End}
	if o.Err != "" {
		ev["err"] = o.Err
	}
	if o.Kind == oracle.KGet && o.Status == oracle.Acked {
		ev["read"] = oracle.Cell{Present: o.Found, Val: o.Read}.String()
	}
	e.Write(ev)
}

// Flush writes buffered events to disk.
func (e *Events) Flush() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.w.Flush(); err != nil {
		return err
	}
	return e.f.Sync()
}

// Close flushes and closes the log.
func (e *Events) Close() error {
	e.Flush()
	return e.f.Close()
}

// histOp is the JSON form of an oracle request.
type histOp struct {
	ID     string            `json:"id"`
	Writer string            `json:"writer"`
	Kind   string            `json:"kind"`
	Status string            `json:"status"`
	Start  int64             `json:"sent_ns"`
	End    int64             `json:"reply_ns"`
	Err    string            `json:"err,omitempty"`
	Key    string            `json:"key,omitempty"`
	Read   string            `json:"read,omitempty"`
	Muts   []string          `json:"ops,omitempty"`
	Effect map[string]string `json:"net_effect,omitempty"`
}

// WriteHistory writes a history (baseline, recovered state, requests) as
// JSON: the input the oracle judged, for offline inspection.
func WriteHistory(path string, base, final oracle.State, ops []*oracle.Op) error {
	enc := func(s oracle.State) map[string]string {
		m := map[string]string{}
		for k, v := range s {
			m[k] = v.String()
		}
		return m
	}
	var hs []histOp
	for _, o := range ops {
		h := histOp{ID: o.ID, Writer: o.Writer, Kind: o.Kind.String(), Status: o.Status.String(), Start: o.Start, End: o.End, Err: o.Err, Key: o.Key}
		if o.Kind == oracle.KGet && o.Status == oracle.Acked {
			h.Read = oracle.Cell{Present: o.Found, Val: o.Read}.String()
		}
		if len(o.Muts) <= 64 {
			for _, m := range o.Muts {
				if m.Del {
					h.Muts = append(h.Muts, "del "+m.Key)
				} else {
					h.Muts = append(h.Muts, "put "+m.Key+" value#"+m.Val.String())
				}
			}
		} else {
			h.Muts = []string{"(", itoa(len(o.Muts)), "ops)"}
		}
		hs = append(hs, h)
	}
	sort.SliceStable(hs, func(i, j int) bool { return hs[i].Start < hs[j].Start })
	b, err := json.MarshalIndent(map[string]any{"baseline_keys": len(base), "recovered_keys": len(final),
		"baseline": limitMap(enc(base)), "recovered": limitMap(enc(final)), "requests": hs}, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func limitMap(m map[string]string) map[string]string {
	if len(m) <= 5000 {
		return m
	}
	return map[string]string{"(omitted)": itoa(len(m)) + " keys"}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
