package oracle

import (
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strings"
	"time"
)

// Result is the oracle's verdict.
type Result uint8

const (
	Pass         Result = iota // a legal execution explains every observation
	Fail                       // no legal execution exists: a genuine violation
	Inconclusive               // a resource limit was hit before a decision
)

func (r Result) String() string {
	return [...]string{"PASS", "FAIL", "INCONCLUSIVE"}[r]
}

// Limits bound the work of one Check. Hitting any of them gives
// Inconclusive, never Pass.
type Limits struct {
	MaxSteps    int64         // graph-search steps
	MaxDuration time.Duration // wall time
	MaxWrites   int           // write requests in one history
}

// DefaultLimits are generous for the competition workloads.
func DefaultLimits() Limits {
	return Limits{MaxSteps: 2_000_000_000, MaxDuration: 2 * time.Minute, MaxWrites: 2_000_000}
}

// ViolationKind classifies a failure.
type ViolationKind string

const (
	VAckedLost        ViolationKind = "acknowledged-write-lost"
	VPartialBatch     ViolationKind = "partial-batch"
	VRejectedVisible  ViolationKind = "rejected-write-visible"
	VNeverWritten     ViolationKind = "value-never-written"
	VUntouchedChanged ViolationKind = "untouched-key-changed"
	VBadRead          ViolationKind = "invalid-read"
	VNoOrder          ViolationKind = "no-legal-order"
)

// Violation explains a Fail.
type Violation struct {
	Kind        ViolationKind `json:"kind"`
	Key         string        `json:"key,omitempty"`
	Expected    string        `json:"expected,omitempty"`
	Found       string        `json:"found,omitempty"`
	Explanation []string      `json:"explanation"`
	OpIDs       []string      `json:"op_ids,omitempty"`
}

func (v *Violation) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s", v.Kind)
	if v.Key != "" {
		fmt.Fprintf(&b, " on key %q", v.Key)
	}
	if v.Expected != "" || v.Found != "" {
		fmt.Fprintf(&b, "\n  expected: %s\n  found:    %s", v.Expected, v.Found)
	}
	for _, e := range v.Explanation {
		fmt.Fprintf(&b, "\n  %s", e)
	}
	return b.String()
}

// Stats describes the work done by Check.
type Stats struct {
	Writes     int           `json:"writes"`
	Reads      int           `json:"reads"`
	Unknown    int           `json:"unknown"`
	Keys       int           `json:"keys"`
	Nodes      int           `json:"nodes"`
	Edges      int           `json:"edges"`
	Choices    int           `json:"open_choices"`
	Decisions  int           `json:"decisions"`
	Backtracks int           `json:"backtracks"`
	Steps      int64         `json:"steps"`
	Elapsed    time.Duration `json:"elapsed_ns"`
}

// Verdict is the outcome of Check.
type Verdict struct {
	Result    Result     `json:"result"`
	Violation *Violation `json:"violation,omitempty"`
	Reason    string     `json:"reason,omitempty"` // why Inconclusive
	Stats     Stats      `json:"stats"`
}

// Check decides whether the recovered state final can result from baseline
// and the requests ops under the rules in the package comment. Rejected
// writes, failed reads and compactions carry no state; Pending requests are
// treated as Unknown.
//
// How: every write that took effect is a node; "a must take effect before b"
// is an edge. Real-time order (a acknowledged before b was sent) is encoded
// with a chain of time-point nodes, so it costs O(n) edges, not O(n²). For
// each key the value recovered identifies its last writer (values are
// unique per request in the workloads; repeated values become alternative
// candidates); every other write to that key that took effect must precede
// it. A read identifies the write it read from. Unknown requests take effect
// exactly when some observation needs them. A legal execution exists iff
// these choices can be made with an acyclic graph; choices with a single
// candidate are made first in bulk, the rest by a backtracking search with
// unit propagation, bounded by Limits.
func Check(baseline, final State, ops []*Op, lim Limits) Verdict {
	t0 := time.Now()
	s := newSolver(baseline, final, ops, lim, t0)
	v := s.run()
	v.Stats.Steps = s.steps
	v.Stats.Elapsed = time.Since(t0)
	v.Stats.Nodes = len(s.adj)
	v.Stats.Edges = s.edges
	v.Stats.Decisions = s.decisions
	v.Stats.Backtracks = s.backtracks
	return v
}

// ---- solver ----

const (
	whyChain uint8 = iota
	whyTime
	whyLast // a wrote key, b's value of key was recovered: a before b
	whyReadFrom
	whyAnti      // a read the older value of key, b overwrote it: a before b
	whyReadOrder // a wrote key, b's value was read later: a before b
)

type why struct {
	k   uint8
	key string
}

type keyVar struct {
	key     string
	final   Cell
	cands   []int // node ids; 0 = baseline
	writers []int // non-rejected writes touching key (nodes)
	reads   []int // readVar indexes on this key
	dec     int   // decided candidate, -1 if none

	// Static indexes over the acknowledged writers (they always took
	// effect, and their times are fixed), so the sound candidate filters
	// cost O(log n) instead of a scan.
	ackStarts []int64 // sorted send times of acknowledged writers
	ackSufMin []int64 // ackSufMin[i] = min reply time among ackStarts[i:]
	unknownW  []int   // writers with an unknown outcome
}

// ackedStartedAfter: some acknowledged writer was sent after t.
func (kv *keyVar) ackedStartedAfter(t int64) bool {
	return len(kv.ackStarts) > 0 && kv.ackStarts[len(kv.ackStarts)-1] > t
}

// ackedWithin: some acknowledged writer was sent after t1 and replied before t2.
func (kv *keyVar) ackedWithin(t1, t2 int64) bool {
	i := sort.Search(len(kv.ackStarts), func(i int) bool { return kv.ackStarts[i] > t1 })
	return i < len(kv.ackStarts) && kv.ackSufMin[i] < t2
}

type readVar struct {
	node  int
	key   string
	cands []int
	dec   int
}

type disj struct {
	a1, b1, a2, b2 int
	key            string
	open           bool
}

type failure struct {
	attempt *attempt // the edge whose addition closed the cycle
	kind    ViolationKind
	key     string
	text    string
	cycle   []int // node path forming a cycle, if known
	ops     []int
}

type attempt struct {
	a, b int
	w    why
}

type solver struct {
	lim      Limits
	t0       time.Time
	baseline State
	final    State
	ops      []*Op

	nodeOp []*Op // index = node; nil for baseline (0) and chain nodes
	start  []int64
	end    []int64 // -1: no reply (unknown); baseline: MinInt64
	nOps   int     // nodes 0..nOps-1 are baseline + ops; the rest are chain nodes
	adj    [][]int32
	adjWhy [][]why
	edges  int

	keys     []*keyVar
	keyIndex map[string]int
	reads    []*readVar
	touches  [][]int // node -> keyVar indexes it writes
	applied  []int   // refcount: >0 means the write took effect
	disjs    []*disj

	trail []func()

	visit []uint32
	epoch uint32
	stack []int32

	byRank      []int32 // op nodes sorted by send time
	rankOf      []int32
	startSorted []int64
	cOut        []int32  // constraint edges leaving each op node
	active      []uint64 // bitset over ranks: node has constraint edges

	steps      int64
	nextClock  int64
	gver       int64 // changes whenever an edge is added or removed
	sweptVer   int64 // gver at the last full disjunction sweep
	sweptN     int   // disjunctions that existed at that sweep
	over       bool
	overReason string
	bulk       bool
	depth      int
	decisions  int
	backtracks int
	rootFail   *failure
	firstFail  *failure
	stats      Stats
}

func newSolver(baseline, final State, ops []*Op, lim Limits, t0 time.Time) *solver {
	if lim.MaxSteps <= 0 {
		lim.MaxSteps = DefaultLimits().MaxSteps
	}
	if lim.MaxDuration <= 0 {
		lim.MaxDuration = DefaultLimits().MaxDuration
	}
	if lim.MaxWrites <= 0 {
		lim.MaxWrites = DefaultLimits().MaxWrites
	}
	return &solver{lim: lim, t0: t0, baseline: baseline, final: final, ops: ops, keyIndex: map[string]int{}}
}

func (s *solver) run() Verdict {
	// Partition the requests.
	var writes, reads, rejected []*Op
	for _, o := range s.ops {
		switch {
		case o.Kind.IsWrite() && o.Status == Rejected:
			rejected = append(rejected, o)
		case o.Kind.IsWrite():
			writes = append(writes, o)
		case o.Kind == KGet && o.Status == Acked:
			reads = append(reads, o)
		}
	}
	s.stats.Writes, s.stats.Reads = len(writes), len(reads)
	for _, o := range writes {
		if o.Status != Acked {
			s.stats.Unknown++
		}
	}
	mk := func(v Verdict) Verdict {
		st := s.stats
		v.Stats = st
		return v
	}
	if len(writes) > s.lim.MaxWrites {
		return mk(Verdict{Result: Inconclusive, Reason: fmt.Sprintf("history has %d writes, limit %d", len(writes), s.lim.MaxWrites)})
	}

	// Nodes: 0 = baseline, then writes, then reads.
	s.nodeOp = append(s.nodeOp, nil)
	s.start = append(s.start, math.MinInt64)
	s.end = append(s.end, math.MinInt64)
	for _, o := range writes {
		s.addOpNode(o)
	}
	for _, o := range reads {
		s.addOpNode(o)
	}
	s.nOps = len(s.nodeOp)
	s.touches = make([][]int, s.nOps)
	s.applied = make([]int, s.nOps)

	// Keys touched by writes that may have taken effect.
	for n := 1; n <= len(writes); n++ {
		for k := range s.nodeOp[n].Effects() {
			ki := s.keyVarFor(k)
			s.keys[ki].writers = append(s.keys[ki].writers, n)
			s.touches[n] = append(s.touches[n], ki)
		}
	}
	// Keys nothing (non-rejected) touched must be exactly as in the baseline.
	if f := s.checkUntouched(rejected); f != nil {
		return mk(s.failVerdict(f))
	}
	// Candidates for each key's last writer.
	for _, kv := range s.keys {
		if s.baseline.Cell(kv.key) == kv.final {
			kv.cands = append(kv.cands, 0)
		}
		for _, n := range kv.writers {
			if s.nodeOp[n].Effects()[kv.key] == kv.final {
				kv.cands = append(kv.cands, n)
			}
		}
		if len(kv.cands) == 0 {
			return mk(s.failVerdict(s.noCandidate(kv, rejected)))
		}
	}
	s.stats.Keys = len(s.keys)
	// Candidates for each read's source.
	for i, o := range reads {
		n := len(writes) + 1 + i
		c := Cell{Present: o.Found, Val: o.Read}
		rv := &readVar{node: n, key: o.Key, dec: -1}
		if s.baseline.Cell(o.Key) == c {
			rv.cands = append(rv.cands, 0)
		}
		if ki, ok := s.keyIndex[o.Key]; ok {
			for _, w := range s.keys[ki].writers {
				if s.nodeOp[w].Effects()[o.Key] == c {
					rv.cands = append(rv.cands, w)
				}
			}
			s.keys[ki].reads = append(s.keys[ki].reads, len(s.reads))
		}
		if len(rv.cands) == 0 {
			f := &failure{kind: VBadRead, key: o.Key, ops: []int{n},
				text: fmt.Sprintf("read %s returned %s, which no request that may have taken effect ever wrote to this key (baseline %s)", o.ID, c, s.baseline.Cell(o.Key))}
			for _, r := range rejected {
				if r.Effects()[o.Key] == c && c.Present {
					f.text += fmt.Sprintf("; it was written by %s, which was REJECTED", r.ID)
				}
			}
			return mk(s.failVerdict(f))
		}
		s.reads = append(s.reads, rv)
	}
	sort.SliceStable(s.reads, func(i, j int) bool { return s.start[s.reads[i].node] < s.start[s.reads[j].node] })
	for _, kv := range s.keys {
		kv.reads = kv.reads[:0]
	}
	for i, rv := range s.reads {
		if ki, ok := s.keyIndex[rv.key]; ok {
			s.keys[ki].reads = append(s.keys[ki].reads, i)
		}
	}

	// Acknowledged writes took effect.
	for n := 1; n <= len(writes); n++ {
		if s.nodeOp[n].Status == Acked {
			s.applied[n] = 1
		}
	}
	for _, kv := range s.keys {
		var acked []int
		for _, w := range kv.writers {
			if s.nodeOp[w].Status == Acked {
				acked = append(acked, w)
			} else {
				kv.unknownW = append(kv.unknownW, w)
			}
		}
		sort.Slice(acked, func(i, j int) bool { return s.start[acked[i]] < s.start[acked[j]] })
		kv.ackStarts = make([]int64, len(acked))
		kv.ackSufMin = make([]int64, len(acked))
		m := int64(math.MaxInt64)
		for i := len(acked) - 1; i >= 0; i-- {
			kv.ackStarts[i] = s.start[acked[i]]
			if e := s.end[acked[i]]; e < m {
				m = e
			}
			kv.ackSufMin[i] = m
		}
	}
	// Static read filters: drop sources that are impossible by real time
	// alone (sound: they could never be the source in any execution).
	for _, rv := range s.reads {
		kept := rv.cands[:0]
		for _, c := range rv.cands {
			if s.readCandViable(rv, c) {
				kept = append(kept, c)
			}
		}
		if len(kept) == 0 {
			o := s.nodeOp[rv.node]
			return mk(s.failVerdict(&failure{kind: VBadRead, key: rv.key, ops: append([]int{rv.node}, rv.cands...),
				text: fmt.Sprintf("read %s returned %s, but every write that produced that value was certainly overwritten before the read was sent, or started after it returned", o.ID, Cell{Present: o.Found, Val: o.Read})}))
		}
		rv.cands = kept
	}
	s.buildTimeChain()
	s.visit = make([]uint32, len(s.adj))
	// Op nodes in send order, for reach's frontier scan.
	for n := 1; n < s.nOps; n++ {
		s.byRank = append(s.byRank, int32(n))
	}
	sort.Slice(s.byRank, func(i, j int) bool { return s.start[s.byRank[i]] < s.start[s.byRank[j]] })
	s.rankOf = make([]int32, s.nOps)
	s.startSorted = make([]int64, len(s.byRank))
	for r, n := range s.byRank {
		s.rankOf[n] = int32(r)
		s.startSorted[r] = s.start[n]
	}
	s.cOut = make([]int32, s.nOps)
	s.active = make([]uint64, len(s.byRank)/64+1)

	// Phase 1: unit choices in bulk (sound filters only), then one acyclicity
	// check over the whole graph.
	s.bulk = true
	if !s.propagate() {
		return mk(s.failVerdict(s.bestFailure()))
	}
	s.bulk = false
	if cyc := s.findCycle(); cyc != nil {
		return mk(s.failVerdict(s.cycleFailure(cyc)))
	}
	if s.over {
		return mk(Verdict{Result: Inconclusive, Reason: s.overReason})
	}
	// Phase 2: search the remaining choices.
	ok := s.solve()
	if s.over {
		return mk(Verdict{Result: Inconclusive, Reason: s.overReason})
	}
	if !ok {
		return mk(s.failVerdict(s.bestFailure()))
	}
	return mk(Verdict{Result: Pass})
}

func (s *solver) addOpNode(o *Op) {
	s.nodeOp = append(s.nodeOp, o)
	s.start = append(s.start, o.Start)
	e := o.End
	if o.Status != Acked {
		e = -1
	}
	s.end = append(s.end, e)
}

func (s *solver) keyVarFor(k string) int {
	if i, ok := s.keyIndex[k]; ok {
		return i
	}
	s.keyIndex[k] = len(s.keys)
	s.keys = append(s.keys, &keyVar{key: k, final: s.final.Cell(k), dec: -1})
	return len(s.keys) - 1
}

func (s *solver) checkUntouched(rejected []*Op) *failure {
	var bad []string
	for _, m := range []State{s.baseline, s.final} {
		for k := range m {
			if _, ok := s.keyIndex[k]; ok {
				continue
			}
			if s.baseline.Cell(k) != s.final.Cell(k) {
				bad = append(bad, k)
			}
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	k := bad[0]
	f := s.final.Cell(k)
	for _, r := range rejected {
		if e, ok := r.Effects()[k]; ok && e == f {
			return &failure{kind: VRejectedVisible, key: k,
				text: fmt.Sprintf("key %q is %s, written only by %s, which was REJECTED (%s)", k, f, r.ID, r.Err)}
		}
	}
	return &failure{kind: VUntouchedChanged, key: k,
		text: fmt.Sprintf("key %q changed from %s to %s, but no request that may have taken effect wrote it (%d such keys)", k, s.baseline.Cell(k), f, len(bad))}
}

func (s *solver) noCandidate(kv *keyVar, rejected []*Op) *failure {
	f := &failure{key: kv.key, ops: kv.writers}
	for _, r := range rejected {
		if e, ok := r.Effects()[kv.key]; ok && e == kv.final && kv.final.Present {
			f.kind = VRejectedVisible
			f.text = fmt.Sprintf("recovered %s was written only by %s, which was REJECTED (%s)", kv.final, r.ID, r.Err)
			return f
		}
	}
	// An intermediate state of a batch (an operation later overwritten in
	// the same batch) means the batch is visible half-applied.
	for _, w := range kv.writers {
		o := s.nodeOp[w]
		if o.Kind != KBatch {
			continue
		}
		for i, m := range o.Muts {
			if m.Key != kv.key || m.Del == kv.final.Present || (kv.final.Present && m.Val != kv.final.Val) {
				continue
			}
			f.kind = VPartialBatch
			f.ops = []int{w}
			f.text = fmt.Sprintf("recovered %s is the intermediate result of operation #%d of batch %s (%s), which a later operation in the same batch overwrites: the batch is visible half-applied, or its operations were not applied in order (last op wins)", kv.final, i, o.ID, o.Status)
			return f
		}
	}
	if kv.final.Present {
		f.kind = VNeverWritten
		f.text = fmt.Sprintf("recovered %s, a value no request ever wrote to this key (corrupted, torn or foreign value)", kv.final)
		return f
	}
	acked := false
	for _, w := range kv.writers {
		if s.nodeOp[w].Status == Acked {
			acked = true
		}
	}
	if acked {
		f.kind = VAckedLost
	} else {
		f.kind = VNoOrder
	}
	f.text = fmt.Sprintf("key is absent, but no request deleted it and the baseline was %s", s.baseline.Cell(kv.key))
	return f
}

// buildTimeChain adds the real-time order: op a precedes op b if a's reply
// arrived before b was sent. One chain node per event, events sorted by time
// with sends before replies at equal timestamps (so equal times give no edge).
func (s *solver) buildTimeChain() {
	s.adj = make([][]int32, s.nOps)
	s.adjWhy = make([][]why, s.nOps)
	type ev struct {
		t    int64
		end  bool
		node int
	}
	evs := make([]ev, 0, 2*s.nOps)
	for n := 1; n < s.nOps; n++ {
		evs = append(evs, ev{t: s.start[n], node: n})
		if s.end[n] >= 0 {
			evs = append(evs, ev{t: s.end[n], end: true, node: n})
		}
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].t != evs[j].t {
			return evs[i].t < evs[j].t
		}
		return !evs[i].end && evs[j].end
	})
	base := s.nOps
	for i := range evs {
		s.adj = append(s.adj, nil)
		s.adjWhy = append(s.adjWhy, nil)
		c := base + i
		if i > 0 {
			s.rawEdge(c-1, c, why{k: whyChain})
		}
		if evs[i].end {
			s.rawEdge(evs[i].node, c, why{k: whyTime})
		} else {
			s.rawEdge(c, evs[i].node, why{k: whyTime})
		}
	}
	if len(evs) > 0 {
		s.rawEdge(0, base, why{k: whyChain})
	}
	for len(s.nodeOp) < len(s.adj) {
		s.nodeOp = append(s.nodeOp, nil)
	}
}

func (s *solver) rawEdge(a, b int, w why) {
	s.adj[a] = append(s.adj[a], int32(b))
	s.adjWhy[a] = append(s.adjWhy[a], w)
	s.edges++
}

// rtBefore: a certainly takes effect before b by real time alone.
func (s *solver) rtBefore(a, b int) bool {
	if a == b || a >= s.nOps || b >= s.nOps {
		return false
	}
	if a == 0 {
		return true
	}
	if b == 0 {
		return false
	}
	return s.end[a] >= 0 && s.end[a] < s.start[b]
}

func (s *solver) tick(n int64) bool {
	s.steps += n
	if s.steps > s.lim.MaxSteps {
		s.over, s.overReason = true, fmt.Sprintf("search exceeded %d steps", s.lim.MaxSteps)
	} else if s.steps >= s.nextClock {
		s.nextClock = s.steps + 1<<20
		if time.Since(s.t0) > s.lim.MaxDuration {
			s.over, s.overReason = true, fmt.Sprintf("search exceeded %s", s.lim.MaxDuration)
		}
	}
	return s.over
}

// reach reports whether b must take effect after a given everything known.
//
// Real-time order makes every request sent after a's reply reachable from a.
// So the search keeps a frontier time F (the earliest reply among the nodes
// reached so far): all requests sent after F are reachable, and only explicit
// constraint edges can reach earlier ones. The walk therefore follows
// constraint edges only, plus the sources of constraint edges that become
// reachable as F moves earlier; it never walks the time chain.
func (s *solver) reach(a, b int) bool {
	if a == b || s.rtBefore(a, b) {
		return true
	}
	if s.over {
		return true
	}
	if b == 0 {
		return false // nothing takes effect before the baseline
	}
	F := int64(math.MaxInt64)
	if s.end[a] >= 0 {
		F = s.end[a]
	}
	sb := s.start[b]
	s.epoch++
	if s.epoch == 0 {
		for i := range s.visit {
			s.visit[i] = 0
		}
		s.epoch = 1
	}
	s.visit[a] = s.epoch
	s.stack = append(s.stack[:0], int32(a))
	lo := len(s.byRank)
	var n int64
	for {
		for len(s.stack) > 0 {
			x := s.stack[len(s.stack)-1]
			s.stack = s.stack[:len(s.stack)-1]
			n++
			for _, y := range s.adj[x] {
				if int(y) >= s.nOps {
					continue
				}
				if int(y) == b {
					s.tick(n)
					return true
				}
				if s.visit[y] != s.epoch {
					s.visit[y] = s.epoch
					if e := s.end[y]; e >= 0 && e < F {
						F = e
						if sb > F {
							s.tick(n)
							return true
						}
					}
					s.stack = append(s.stack, y)
				}
			}
		}
		rb := sort.Search(len(s.startSorted), func(i int) bool { return s.startSorted[i] > F })
		if rb >= lo {
			break
		}
		for r := rb; r < lo; {
			word := s.active[r>>6] >> uint(r&63)
			if word == 0 {
				r = (r | 63) + 1
				continue
			}
			r += bits.TrailingZeros64(word)
			if r >= lo {
				break
			}
			x := s.byRank[r]
			if s.visit[x] != s.epoch {
				s.visit[x] = s.epoch
				s.stack = append(s.stack, x)
			}
			r++
			n++
		}
		lo = rb
		if len(s.stack) == 0 {
			break
		}
		if s.tick(n) {
			return true
		}
		n = 0
	}
	s.tick(n)
	return false
}

func (s *solver) noteConstraint(a int, delta int32) {
	if a <= 0 || a >= s.nOps {
		return
	}
	s.cOut[a] += delta
	r := s.rankOf[a]
	if s.cOut[a] > 0 {
		s.active[r>>6] |= 1 << uint(r&63)
	} else {
		s.active[r>>6] &^= 1 << uint(r&63)
	}
}

// addEdge records "a takes effect before b". It fails if that contradicts
// what is already known. In bulk mode only real-time contradictions are
// detected here; findCycle checks the rest at the end of the bulk phase.
func (s *solver) addEdge(a, b int, w why) bool {
	if a == b {
		return true
	}
	if s.rtBefore(a, b) {
		return true // already implied
	}
	if s.rtBefore(b, a) {
		s.record(&failure{kind: s.kindForEdge(a, w), key: w.key, cycle: []int{b, a}, ops: []int{a, b}, attempt: &attempt{a, b, w}})
		return false
	}
	if !s.bulk && s.reach(b, a) {
		s.record(&failure{kind: s.kindForEdge(a, w), key: w.key, cycle: s.pathPlus(b, a), ops: []int{a, b}, attempt: &attempt{a, b, w}})
		return false
	}
	s.adj[a] = append(s.adj[a], int32(b))
	s.adjWhy[a] = append(s.adjWhy[a], w)
	s.edges++
	s.gver++
	s.noteConstraint(a, 1)
	s.trail = append(s.trail, func() {
		s.adj[a] = s.adj[a][:len(s.adj[a])-1]
		s.adjWhy[a] = s.adjWhy[a][:len(s.adjWhy[a])-1]
		s.edges--
		s.gver++
		s.noteConstraint(a, -1)
	})
	return true
}

func (s *solver) kindForEdge(a int, w why) ViolationKind {
	switch w.k {
	case whyReadFrom, whyAnti, whyReadOrder:
		return VBadRead
	case whyLast:
		if o := s.nodeOp[a]; o != nil && o.Status == Acked {
			return VAckedLost
		}
		return VPartialBatch
	}
	return VNoOrder
}

func (s *solver) record(f *failure) {
	if s.depth == 0 && s.rootFail == nil {
		s.rootFail = f
	}
	if s.firstFail == nil {
		s.firstFail = f
	}
}

func (s *solver) bestFailure() *failure {
	if s.rootFail != nil {
		return s.rootFail
	}
	if s.firstFail != nil {
		return s.firstFail
	}
	return &failure{kind: VNoOrder, text: "no legal execution explains the observations"}
}

// apply marks node n (a write) as having taken effect and adds the
// constraints that implies for already-made choices.
func (s *solver) apply(n int) bool {
	if n == 0 {
		return true
	}
	s.applied[n]++
	s.trail = append(s.trail, func() { s.applied[n]-- })
	if s.applied[n] > 1 {
		return true
	}
	for _, ki := range s.touches[n] {
		kv := s.keys[ki]
		if kv.dec == 0 {
			s.record(s.lostOrPartial(kv, n))
			return false
		}
		if kv.dec > 0 && kv.dec != n {
			if !s.addEdge(n, kv.dec, why{k: whyLast, key: kv.key}) {
				return false
			}
		}
		for _, ri := range kv.reads {
			rv := s.reads[ri]
			if rv.dec < 0 || rv.dec == n {
				continue
			}
			if rv.dec == 0 {
				if !s.addEdge(rv.node, n, why{k: whyAnti, key: kv.key}) {
					return false
				}
				continue
			}
			if !s.addDisj(n, rv.dec, rv.node, n, kv.key) {
				return false
			}
		}
	}
	return true
}

func (s *solver) lostOrPartial(kv *keyVar, n int) *failure {
	o := s.nodeOp[n]
	f := &failure{key: kv.key, ops: []int{n}}
	if o.Status == Acked {
		f.kind = VAckedLost
		f.text = fmt.Sprintf("%s was acknowledged and wrote this key, but the key still holds the baseline %s", o.ID, kv.final)
	} else {
		f.kind = VPartialBatch
		f.text = fmt.Sprintf("%s (outcome unknown) is visible on another key, so it took effect, but this key still holds the baseline %s instead of its %s", o.ID, kv.final, o.Effects()[kv.key])
	}
	return f
}

func (s *solver) assignKey(kv *keyVar, c int) bool {
	s.decisions++
	kv.dec = c
	s.trail = append(s.trail, func() { kv.dec = -1 })
	if c == 0 {
		for _, w := range kv.writers {
			if s.applied[w] > 0 {
				s.record(s.lostOrPartial(kv, w))
				return false
			}
		}
		return true
	}
	if !s.apply(c) {
		return false
	}
	for _, w := range kv.writers {
		if w != c && s.applied[w] > 0 {
			if !s.addEdge(w, c, why{k: whyLast, key: kv.key}) {
				return false
			}
		}
	}
	return true
}

func (s *solver) assignRead(rv *readVar, src int) bool {
	s.decisions++
	rv.dec = src
	s.trail = append(s.trail, func() { rv.dec = -1 })
	ki, hasKey := s.keyIndex[rv.key]
	if src == 0 {
		if hasKey {
			for _, w := range s.keys[ki].writers {
				if s.applied[w] > 0 && !s.addEdge(rv.node, w, why{k: whyAnti, key: rv.key}) {
					return false
				}
			}
		}
		return true
	}
	if !s.apply(src) {
		return false
	}
	if !s.addEdge(src, rv.node, why{k: whyReadFrom, key: rv.key}) {
		return false
	}
	for _, w := range s.keys[ki].writers {
		if w != src && s.applied[w] > 0 {
			if !s.addDisj(w, src, rv.node, w, rv.key) {
				return false
			}
		}
	}
	return true
}

// addDisj records "a1 before b1, or a2 before b2".
func (s *solver) addDisj(a1, b1, a2, b2 int, key string) bool {
	if s.rtBefore(a1, b1) || s.rtBefore(a2, b2) {
		return true
	}
	if s.rtBefore(b1, a1) {
		return s.addEdge(a2, b2, why{k: whyAnti, key: key})
	}
	if s.rtBefore(b2, a2) {
		return s.addEdge(a1, b1, why{k: whyReadOrder, key: key})
	}
	d := &disj{a1: a1, b1: b1, a2: a2, b2: b2, key: key, open: true}
	s.disjs = append(s.disjs, d)
	s.trail = append(s.trail, func() { s.disjs = s.disjs[:len(s.disjs)-1] })
	return true
}

func (s *solver) closeDisj(d *disj) {
	d.open = false
	s.trail = append(s.trail, func() { d.open = true })
}

// propagate makes every forced choice until nothing changes. It returns
// false on a contradiction.
func (s *solver) propagate() bool {
	for changed := true; changed; {
		changed = false
		if s.over {
			return false
		}
		if !s.bulk {
			// Only disjunctions added since the last sweep are examined here;
			// the rest are settled once, at the leaf (resolveFrom).
			from := s.sweptN
			if from > len(s.disjs) {
				from = len(s.disjs)
			}
			for i := from; i < len(s.disjs); i++ {
				d := s.disjs[i]
				if !d.open {
					continue
				}
				switch {
				case s.rtBefore(d.a1, d.b1) || s.rtBefore(d.a2, d.b2):
					s.closeDisj(d)
				case s.rtBefore(d.b1, d.a1):
					s.closeDisj(d)
					if !s.addEdge(d.a2, d.b2, why{k: whyAnti, key: d.key}) {
						return false
					}
					changed = true
				case s.rtBefore(d.b2, d.a2):
					s.closeDisj(d)
					if !s.addEdge(d.a1, d.b1, why{k: whyReadOrder, key: d.key}) {
						return false
					}
					changed = true
				case s.reach(d.a1, d.b1) || s.reach(d.a2, d.b2):
					s.closeDisj(d)
				case s.reach(d.b1, d.a1):
					s.closeDisj(d)
					if !s.addEdge(d.a2, d.b2, why{k: whyAnti, key: d.key}) {
						return false
					}
					changed = true
				case s.reach(d.b2, d.a2):
					s.closeDisj(d)
					if !s.addEdge(d.a1, d.b1, why{k: whyReadOrder, key: d.key}) {
						return false
					}
					changed = true
				}
			}
			s.sweptN = len(s.disjs)
		}
		for _, kv := range s.keys {
			if kv.dec >= 0 {
				continue
			}
			only, n := -1, 0
			for _, c := range kv.cands {
				if s.keyCandViable(kv, c) {
					only, n = c, n+1
				}
			}
			if n == 0 {
				s.record(s.keyDeadEnd(kv))
				return false
			}
			if n == 1 {
				if !s.assignKey(kv, only) {
					return false
				}
				changed = true
			}
		}
		for _, rv := range s.reads {
			if rv.dec >= 0 {
				continue
			}
			only, n := -1, 0
			for _, c := range rv.cands {
				if s.readCandViable(rv, c) {
					only, n = c, n+1
				}
			}
			if n == 0 {
				o := s.nodeOp[rv.node]
				s.record(&failure{kind: VBadRead, key: rv.key, ops: append([]int{rv.node}, rv.cands...),
					text: fmt.Sprintf("read %s returned %s, but every write that produced that value was certainly overwritten before the read was sent, or started after it returned", o.ID, Cell{Present: o.Found, Val: o.Read})})
				return false
			}
			if n == 1 {
				if !s.assignRead(rv, only) {
					return false
				}
				changed = true
			}
		}
	}
	return true
}

// keyCandViable applies sound filters only (never removes a possible choice).
func (s *solver) keyCandViable(kv *keyVar, c int) bool {
	if c == 0 {
		if len(kv.ackStarts) > 0 {
			return false
		}
		for _, w := range kv.unknownW {
			if s.applied[w] > 0 {
				return false
			}
		}
		return true
	}
	if s.end[c] < 0 {
		return true // unknown: may have taken effect at any time after it was sent
	}
	// c finished before an effective write to the key was sent: that write
	// must come after c, so c cannot be the last writer.
	if kv.ackedStartedAfter(s.end[c]) {
		return false
	}
	for _, w := range kv.unknownW {
		if w != c && s.applied[w] > 0 && s.start[w] > s.end[c] {
			return false
		}
	}
	return true
}

// readCandViable is static: unknown writers have no reply time, so only
// acknowledged writers can rule a source out by real time.
func (s *solver) readCandViable(rv *readVar, c int) bool {
	ki, hasKey := s.keyIndex[rv.key]
	g := rv.node
	if c == 0 {
		// Some acknowledged write to the key finished before the read began.
		return !hasKey || len(s.keys[ki].ackSufMin) == 0 || s.keys[ki].ackSufMin[0] >= s.start[g]
	}
	if s.rtBefore(g, c) {
		return false // the read returned before the write was sent
	}
	if s.end[c] >= 0 && s.keys[ki].ackedWithin(s.end[c], s.start[g]) {
		return false // overwritten by an acknowledged write before the read was sent
	}
	return true
}

func (s *solver) keyDeadEnd(kv *keyVar) *failure {
	// Explain with the most natural candidate: the latest-acknowledged one.
	// Real-time conflicts with a non-baseline candidate are explained first;
	// "the key still holds the baseline" only names a writer whose own
	// effect differs from the recovered cell.
	f := &failure{key: kv.key, ops: kv.writers}
	baseline := false
	for _, c := range kv.cands {
		if c == 0 {
			baseline = true
			continue
		}
		for _, w := range kv.writers {
			if w != c && s.applied[w] > 0 && s.rtBefore(c, w) {
				f.cycle = []int{w, c}
				f.attempt = &attempt{w, c, why{k: whyLast, key: kv.key}}
				if s.nodeOp[w].Status == Acked {
					f.kind = VAckedLost
					f.text = fmt.Sprintf("recovered %s comes from %s, but %s wrote this key after %s completed and was acknowledged", kv.final, s.nodeOp[c].ID, s.nodeOp[w].ID, s.nodeOp[c].ID)
				} else {
					f.kind = VPartialBatch
					f.text = fmt.Sprintf("recovered %s comes from %s, but %s (outcome unknown, visible on another key so it took effect) was sent after %s completed and wrote this key", kv.final, s.nodeOp[c].ID, s.nodeOp[w].ID, s.nodeOp[c].ID)
				}
				return f
			}
		}
	}
	if baseline {
		var fallback = -1
		for _, w := range kv.writers {
			if s.applied[w] == 0 {
				continue
			}
			if s.nodeOp[w].Effects()[kv.key] != kv.final {
				return s.lostOrPartial(kv, w)
			}
			if fallback < 0 {
				fallback = w
			}
		}
		if fallback >= 0 {
			return s.lostOrPartial(kv, fallback)
		}
	}
	f.kind = VNoOrder
	f.text = fmt.Sprintf("no write can be the last writer of %s", kv.final)
	return f
}

// solve is the backtracking search over the remaining choices.
func (s *solver) solve() bool {
	if !s.propagate() {
		return false
	}
	if s.over {
		return false
	}
	// Reads first, in send order: that follows how the execution unfolded
	// and rarely needs backtracking. Then the key with the fewest
	// candidates, then open disjunctions.
	var bestK *keyVar
	var bestR *readVar
	for _, rv := range s.reads {
		if rv.dec < 0 {
			bestR = rv
			break
		}
	}
	if bestR == nil {
		best := math.MaxInt
		for _, kv := range s.keys {
			if kv.dec < 0 && len(kv.cands) < best {
				bestK, best = kv, len(kv.cands)
			}
		}
	}
	if bestK == nil && bestR == nil {
		return s.resolveFrom(0) // every source chosen; order the rest
	}
	s.stats.Choices++
	var cands []int
	if bestK != nil {
		cands = s.order(bestK.cands)
	} else {
		cands = s.orderRead(bestR)
	}
	for _, c := range cands {
		if bestK != nil && !s.keyCandViable(bestK, c) || bestR != nil && !s.readCandViable(bestR, c) {
			continue
		}
		mark := len(s.trail)
		s.depth++
		var ok bool
		if bestK != nil {
			ok = s.assignKey(bestK, c)
		} else {
			ok = s.assignRead(bestR, c)
		}
		if ok && s.solve() {
			s.depth--
			return true
		}
		s.depth--
		s.undo(mark)
		s.backtracks++
		if s.over {
			return false
		}
	}
	return false
}

// resolveFrom settles the open "a1 before b1, or a2 before b2" constraints in
// creation order (which follows the reads' send order), adding one edge each
// and backtracking when an edge would close a cycle.
func (s *solver) resolveFrom(i int) bool {
	for i < len(s.disjs) && !s.disjs[i].open {
		i++
	}
	if i == len(s.disjs) {
		return true
	}
	if s.over {
		return false
	}
	d := s.disjs[i]
	mark := len(s.trail)
	if s.reach(d.a1, d.b1) || s.reach(d.a2, d.b2) {
		s.closeDisj(d)
		if s.resolveFrom(i + 1) {
			return true
		}
		s.undo(mark)
		return false
	}
	s.stats.Choices++
	// Try the order send times suggest first: the writer before the source
	// if it was sent before the source, otherwise after the read.
	first := 0
	if s.start[d.a1] > s.start[d.b1] {
		first = 1
	}
	for k := 0; k < 2; k++ {
		opt := first ^ k
		s.depth++
		s.closeDisj(d)
		var ok bool
		if opt == 0 {
			ok = s.addEdge(d.a1, d.b1, why{k: whyReadOrder, key: d.key})
		} else {
			ok = s.addEdge(d.a2, d.b2, why{k: whyAnti, key: d.key})
		}
		if ok && s.resolveFrom(i+1) {
			s.depth--
			return true
		}
		s.depth--
		s.undo(mark)
		s.backtracks++
		if s.over {
			return false
		}
	}
	return false
}

// order tries acknowledged candidates first, latest reply first, then
// already-applied unknown ones, then the baseline, then other unknown ones.
func (s *solver) order(c []int) []int {
	out := append([]int(nil), c...)
	rank := func(n int) int {
		switch {
		case n == 0:
			return 2
		case s.nodeOp[n].Status == Acked:
			return 0
		case s.applied[n] > 0:
			return 1
		}
		return 3
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return s.end[out[i]] > s.end[out[j]]
	})
	return out
}

// orderRead tries the most recent possible source first: the write that
// started last before the read returned.
func (s *solver) orderRead(rv *readVar) []int {
	out := append([]int(nil), rv.cands...)
	end := s.end[rv.node]
	key := func(n int) int64 {
		if n == 0 {
			return math.MinInt64
		}
		if s.start[n] > end {
			return math.MinInt64 + 1
		}
		return s.start[n]
	}
	sort.SliceStable(out, func(i, j int) bool { return key(out[i]) > key(out[j]) })
	return out
}

func (s *solver) undo(mark int) {
	for len(s.trail) > mark {
		f := s.trail[len(s.trail)-1]
		s.trail = s.trail[:len(s.trail)-1]
		f()
	}
}

// findCycle returns a cycle (list of nodes) if the graph has one.
func (s *solver) findCycle() []int {
	n := len(s.adj)
	color := make([]uint8, n) // 0 white, 1 grey, 2 black
	parent := make([]int32, n)
	type frame struct {
		node int32
		i    int
	}
	var st []frame
	for root := 0; root < n; root++ {
		if color[root] != 0 {
			continue
		}
		st = append(st[:0], frame{node: int32(root)})
		color[root] = 1
		parent[root] = -1
		for len(st) > 0 {
			top := &st[len(st)-1]
			if top.i < len(s.adj[top.node]) {
				y := s.adj[top.node][top.i]
				top.i++
				switch color[y] {
				case 0:
					color[y] = 1
					parent[y] = top.node
					st = append(st, frame{node: y})
				case 1:
					// Cycle: y -> ... -> top.node -> y.
					cyc := []int{int(y)}
					for x := top.node; x != y && x >= 0; x = parent[x] {
						cyc = append(cyc, int(x))
					}
					// cyc is y, top, parent(top)..., reverse to get path order.
					for i, j := 1, len(cyc)-1; i < j; i, j = i+1, j-1 {
						cyc[i], cyc[j] = cyc[j], cyc[i]
					}
					return cyc
				}
				continue
			}
			color[top.node] = 2
			st = st[:len(st)-1]
		}
		if s.tick(int64(len(s.adj[root]) + 1)) {
			return nil
		}
	}
	return nil
}

// pathPlus returns a node path from a to b (b reachable from a).
func (s *solver) pathPlus(a, b int) []int {
	if s.rtBefore(a, b) {
		return []int{a, b}
	}
	prev := map[int]int{a: -1}
	q := []int{a}
	for len(q) > 0 {
		x := q[0]
		q = q[1:]
		if x == b {
			break
		}
		for _, y := range s.adj[x] {
			if _, ok := prev[int(y)]; !ok {
				prev[int(y)] = x
				q = append(q, int(y))
			}
		}
		if len(prev) > 200000 {
			return []int{a, b}
		}
	}
	if _, ok := prev[b]; !ok {
		return []int{a, b}
	}
	var path []int
	for x := b; x != -1; x = prev[x] {
		path = append(path, x)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func (s *solver) cycleFailure(cyc []int) *failure {
	f := &failure{kind: VNoOrder, cycle: cyc}
	// Classify by the last-writer and read edges on the cycle.
	for i := range cyc {
		a, b := cyc[i], cyc[(i+1)%len(cyc)]
		w, ok := s.edgeWhy(a, b)
		if !ok {
			continue
		}
		switch w.k {
		case whyLast:
			f.key = w.key
			if o := s.nodeOp[a]; o != nil && o.Status == Acked {
				f.kind = VAckedLost
			} else if o != nil {
				f.kind = VPartialBatch
			}
		case whyReadFrom, whyAnti, whyReadOrder:
			if f.kind == VNoOrder {
				f.kind, f.key = VBadRead, w.key
			}
		}
		if f.kind == VAckedLost {
			break
		}
	}
	return f
}

func (s *solver) edgeWhy(a, b int) (why, bool) {
	for i, y := range s.adj[a] {
		if int(y) == b {
			return s.adjWhy[a][i], true
		}
	}
	return why{}, false
}

// ---- explanation ----

func (s *solver) describe(n int) string {
	if n == 0 {
		return "baseline (state before this run)"
	}
	o := s.nodeOp[n]
	if o == nil {
		return "time-point"
	}
	done := "no reply"
	if o.End >= 0 {
		done = fmt.Sprintf("reply at %.3fms", float64(o.End)/1e6)
	}
	extra := ""
	switch o.Kind {
	case KGet:
		extra = fmt.Sprintf(" key=%q -> %s", o.Key, Cell{Present: o.Found, Val: o.Read})
	case KBatch:
		extra = fmt.Sprintf(" %d ops", len(o.Muts))
	case KPut, KDel:
		if len(o.Muts) == 1 {
			extra = fmt.Sprintf(" key=%q -> %s", o.Muts[0].Key, o.Effects()[o.Muts[0].Key])
		}
	}
	return fmt.Sprintf("%s [%s %s, writer %s, sent at %.3fms, %s%s]", o.ID, o.Kind, o.Status, o.Writer, float64(o.Start)/1e6, done, extra)
}

func (s *solver) failVerdict(f *failure) Verdict {
	v := &Violation{Kind: f.kind, Key: f.key}
	if f.key != "" {
		v.Found = s.final.Cell(f.key).String()
		if ki, ok := s.keyIndex[f.key]; ok {
			var exp []string
			if s.baseline.Cell(f.key) == s.final.Cell(f.key) || len(s.keys[ki].writers) == 0 {
				exp = append(exp, "baseline "+s.baseline.Cell(f.key).String())
			}
			for _, w := range s.keys[ki].writers {
				o := s.nodeOp[w]
				if o.Status == Acked {
					exp = append(exp, fmt.Sprintf("%s from %s (acked)", o.Effects()[f.key], o.ID))
				}
			}
			if len(exp) > 0 {
				v.Expected = "consistent with " + strings.Join(exp, ", ") + " (in a legal order)"
			}
		}
	}
	if f.text != "" {
		v.Explanation = append(v.Explanation, f.text)
	}
	if len(f.cycle) > 0 {
		v.Explanation = append(v.Explanation, "no legal order exists; these constraints form a cycle:")
		ops := s.compressCycle(f.cycle)
		for i := range ops {
			a, b := ops[i], ops[(i+1)%len(ops)]
			reason := s.reasonText(a, b)
			if at := f.attempt; at != nil && at.a == a && at.b == b {
				reason = s.whyText(at.w)
			}
			v.Explanation = append(v.Explanation, fmt.Sprintf("  %s\n      must take effect before %s  (%s)", s.describe(a), s.describe(b), reason))
		}
	}
	seen := map[int]bool{}
	add := func(n int) {
		if n > 0 && n < s.nOps && !seen[n] {
			seen[n] = true
			v.OpIDs = append(v.OpIDs, s.nodeOp[n].ID)
		}
	}
	for _, n := range f.cycle {
		add(n)
	}
	for _, n := range f.ops {
		add(n)
	}
	if f.key != "" {
		if ki, ok := s.keyIndex[f.key]; ok {
			v.Explanation = append(v.Explanation, fmt.Sprintf("requests on key %q (baseline %s):", f.key, s.baseline.Cell(f.key)))
			lst := s.keys[ki].writers
			if len(lst) > 40 {
				lst = lst[len(lst)-40:]
			}
			for _, w := range lst {
				v.Explanation = append(v.Explanation, "  "+s.describe(w)+" -> "+s.nodeOp[w].Effects()[f.key].String())
				add(w)
			}
		}
	}
	return Verdict{Result: Fail, Violation: v}
}

// compressCycle drops time-point nodes, keeping op nodes in cycle order.
func (s *solver) compressCycle(c []int) []int {
	var out []int
	for _, n := range c {
		if n < s.nOps {
			out = append(out, n)
		}
	}
	return out
}

func (s *solver) whyText(w why) string {
	switch w.k {
	case whyLast:
		return fmt.Sprintf("both wrote key %q and the recovered value of %q comes from the second", w.key, w.key)
	case whyReadFrom:
		return fmt.Sprintf("the read of %q returned the value written by the first", w.key)
	case whyAnti:
		return fmt.Sprintf("the read of %q returned an older value than the one the second wrote", w.key)
	case whyReadOrder:
		return fmt.Sprintf("both wrote key %q and a read returned the second's value after it", w.key)
	}
	return "real time: the first was acknowledged before the second was sent"
}

func (s *solver) reasonText(a, b int) string {
	if w, ok := s.edgeWhy(a, b); ok && w.k != whyTime && w.k != whyChain {
		return s.whyText(w)
	}
	if s.rtBefore(a, b) {
		return "real time: the first was acknowledged before the second was sent"
	}
	return "real time: the first was acknowledged before the second was sent (via other requests)"
}
