// Package oracle is the reference model of NEXUS Torture Engine 2.0.
//
// It knows nothing about how NEXUS stores data. It sees only what a client
// sees: the requests that were sent, when they were sent, which replies came
// back and when, and the key/value state read back after recovery. From that
// it decides whether some legal execution explains what was observed.
//
// The rules (the contract of a crash-safe key-value store):
//
//   - An acknowledged write is durable and atomic: it took effect at a single
//     moment between its send time and its acknowledgement.
//   - A definitely rejected write never took effect.
//   - A write with an unknown outcome (the process died first, or the store
//     said "unknown") either took effect at a single moment after it was sent,
//     or not at all. Never partly.
//   - A batch takes effect as a whole. Inside it operations apply in order and
//     the last operation on a key wins.
//   - Requests of one writer are issued one at a time, in order.
//   - A read returns the state at a single moment between its send time and
//     its reply.
//   - After recovery the state is exactly what the writes that took effect
//     produce, in the order they took effect.
//
// Check searches for such an execution. It does not compare the recovered
// state with the requests in submission order: concurrent requests may take
// effect in any order consistent with real time.
package oracle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// Status is what the client knows about a request.
type Status uint8

const (
	Pending  Status = iota // sent, no reply yet
	Acked                  // a complete success reply was received
	Rejected               // a complete reply said it definitely did not happen
	Unknown                // the reply never came, or said the outcome is unknown
)

func (s Status) String() string {
	switch s {
	case Pending:
		return "pending"
	case Acked:
		return "acked"
	case Rejected:
		return "rejected"
	case Unknown:
		return "unknown"
	}
	return fmt.Sprintf("status(%d)", s)
}

// Kind is the request type.
type Kind uint8

const (
	KPut Kind = iota
	KDel
	KBatch
	KGet
	KCompact
)

func (k Kind) String() string {
	return [...]string{"put", "del", "batch", "get", "compact"}[k]
}

// IsWrite reports whether the request can change logical state.
func (k Kind) IsWrite() bool { return k == KPut || k == KDel || k == KBatch }

// ValueID identifies a value without keeping it (values reach 1 MiB).
type ValueID [16]byte

// HashValue returns the ValueID of v.
func HashValue(v []byte) ValueID {
	s := sha256.Sum256(v)
	var id ValueID
	copy(id[:], s[:16])
	return id
}

func (v ValueID) String() string { return hex.EncodeToString(v[:6]) }

// Mut is one operation inside a write request.
type Mut struct {
	Key string
	Del bool
	Val ValueID // put only
}

// Cell is the state of one key: absent, or present with a value.
type Cell struct {
	Present bool
	Val     ValueID
}

func (c Cell) String() string {
	if !c.Present {
		return "absent"
	}
	return "value#" + c.Val.String()
}

// State maps each present key to its value.
type State map[string]ValueID

// Cell returns the state of key k.
func (s State) Cell(k string) Cell {
	v, ok := s[k]
	return Cell{Present: ok, Val: v}
}

// Op is one request and everything the client observed about it.
type Op struct {
	ID     string
	Writer string
	Kind   Kind
	Muts   []Mut // writes, in submission order
	Key    string
	Start  int64 // ns, taken just before the first byte was sent
	End    int64 // ns, when the complete reply was read; -1 without a reply
	Status Status
	Err    string // error class and text from the reply
	// Get replies.
	Found bool
	Read  ValueID

	eff map[string]Cell
}

// Effects returns the net effect of the request on each key it touches:
// operations apply in order, the last one on a key wins.
func (o *Op) Effects() map[string]Cell {
	if o.eff == nil {
		o.eff = make(map[string]Cell, len(o.Muts))
		for _, m := range o.Muts {
			if m.Del {
				o.eff[m.Key] = Cell{}
			} else {
				o.eff[m.Key] = Cell{Present: true, Val: m.Val}
			}
		}
	}
	return o.eff
}

// Completed reports whether a complete reply was received.
func (o *Op) Completed() bool { return o.End >= 0 && (o.Status == Acked || o.Status == Rejected) }

// History records requests as they happen. It is safe for concurrent use by
// several writer goroutines and enforces the client-side rules: a writer has
// at most one request in flight, and a request is acknowledged only once.
type History struct {
	mu       sync.Mutex
	ops      []*Op
	inflight map[string]*Op
	ids      map[string]bool
	clock    func() int64
}

// NewHistory returns an empty history that timestamps with clock (ns).
func NewHistory(clock func() int64) *History {
	return &History{inflight: map[string]*Op{}, ids: map[string]bool{}, clock: clock}
}

// Begin records that o is about to be sent and stamps its start time. It
// fails if the writer still has a request in flight (per-writer order) or the
// ID was used before.
func (h *History) Begin(o *Op) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p := h.inflight[o.Writer]; p != nil {
		return fmt.Errorf("oracle: writer %s sent %s while %s was still in flight", o.Writer, o.ID, p.ID)
	}
	if h.ids[o.ID] {
		return fmt.Errorf("oracle: duplicate op id %s", o.ID)
	}
	h.ids[o.ID] = true
	o.Status, o.End = Pending, -1
	o.Start = h.clock()
	h.inflight[o.Writer] = o
	h.ops = append(h.ops, o)
	return nil
}

// Finish records the outcome of an in-flight request. For Acked and Rejected
// the end time is stamped now; Unknown keeps End = -1.
func (h *History) Finish(o *Op, st Status, errText string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inflight[o.Writer] != o {
		return fmt.Errorf("oracle: %s finished but is not in flight", o.ID)
	}
	if st == Pending {
		return fmt.Errorf("oracle: %s finished as pending", o.ID)
	}
	delete(h.inflight, o.Writer)
	o.Status, o.Err = st, errText
	if st == Acked || st == Rejected {
		o.End = h.clock()
	}
	return nil
}

// Crash marks every request still in flight as Unknown and returns them.
func (h *History) Crash() []*Op {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*Op
	for w, o := range h.inflight {
		o.Status = Unknown
		out = append(out, o)
		delete(h.inflight, w)
	}
	return out
}

// Ops returns the recorded requests in send order.
func (h *History) Ops() []*Op {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*Op(nil), h.ops...)
}

// InFlight returns the requests that have no outcome yet.
func (h *History) InFlight() []*Op {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*Op
	for _, o := range h.inflight {
		out = append(out, o)
	}
	return out
}

// Apply returns the state produced by applying ops, in order, to base. It is
// the sequential reference semantics (used by tests and by scenarios whose
// order is fixed).
func Apply(base State, ops ...*Op) State {
	out := make(State, len(base))
	for k, v := range base {
		out[k] = v
	}
	for _, o := range ops {
		for _, m := range o.Muts {
			if m.Del {
				delete(out, m.Key)
			} else {
				out[m.Key] = m.Val
			}
		}
	}
	return out
}
