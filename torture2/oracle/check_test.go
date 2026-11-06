package oracle

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

// ---- helpers ----

func val(s string) ValueID { return HashValue([]byte(s)) }

func put(k, v string) Mut { return Mut{Key: k, Val: val(v)} }
func del(k string) Mut    { return Mut{Key: k, Del: true} }

func wop(id, w string, st Status, start, end int64, muts ...Mut) *Op {
	k := KPut
	if len(muts) > 1 {
		k = KBatch
	} else if len(muts) == 1 && muts[0].Del {
		k = KDel
	}
	if st != Acked && st != Rejected {
		end = -1
	}
	return &Op{ID: id, Writer: w, Kind: k, Muts: muts, Start: start, End: end, Status: st}
}

func gop(id, w string, start, end int64, key string, found bool, v string) *Op {
	o := &Op{ID: id, Writer: w, Kind: KGet, Key: key, Start: start, End: end, Status: Acked, Found: found}
	if found {
		o.Read = val(v)
	}
	return o
}

func st(kv ...string) State {
	s := State{}
	for i := 0; i < len(kv); i += 2 {
		s[kv[i]] = val(kv[i+1])
	}
	return s
}

func expect(t *testing.T, name string, v Verdict, want Result, kind ViolationKind) {
	t.Helper()
	if v.Result != want {
		msg := ""
		if v.Violation != nil {
			msg = v.Violation.String()
		}
		t.Fatalf("%s: got %s, want %s (%s) %s", name, v.Result, want, v.Reason, msg)
	}
	if want == Fail && kind != "" && v.Violation.Kind != kind {
		t.Fatalf("%s: violation kind %s, want %s\n%s", name, v.Violation.Kind, kind, v.Violation)
	}
}

// ---- unit cases ----

func TestOracleUnitCases(t *testing.T) {
	L := DefaultLimits()
	cases := []struct {
		name     string
		base     State
		ops      []*Op
		final    State
		want     Result
		kind     ViolationKind
		comments string
	}{
		{"sequential puts", st(), []*Op{wop("a", "1", Acked, 0, 10, put("k", "1")), wop("b", "1", Acked, 11, 20, put("k", "2"))}, st("k", "2"), Pass, "", ""},
		{"acked write lost", st(), []*Op{wop("a", "1", Acked, 0, 10, put("k", "1")), wop("b", "1", Acked, 11, 20, put("k", "2"))}, st("k", "1"), Fail, VAckedLost, ""},
		{"acked write lost to baseline", st("k", "0"), []*Op{wop("a", "1", Acked, 0, 10, put("k", "1"))}, st("k", "0"), Fail, VAckedLost, ""},
		{"acked delete lost", st("k", "0"), []*Op{wop("a", "1", Acked, 0, 10, del("k"))}, st("k", "0"), Fail, VAckedLost, ""},
		{"unknown put applied", st(), []*Op{wop("a", "1", Unknown, 0, 0, put("k", "1"))}, st("k", "1"), Pass, "", ""},
		{"unknown put not applied", st(), []*Op{wop("a", "1", Unknown, 0, 0, put("k", "1"))}, st(), Pass, "", ""},
		{"unknown batch all", st(), []*Op{wop("a", "1", Unknown, 0, 0, put("x", "1"), put("y", "1"))}, st("x", "1", "y", "1"), Pass, "", ""},
		{"unknown batch partial", st(), []*Op{wop("a", "1", Unknown, 0, 0, put("x", "1"), put("y", "1"))}, st("x", "1"), Fail, VPartialBatch, ""},
		{"acked batch partial", st("y", "0"), []*Op{wop("a", "1", Acked, 0, 5, put("x", "1"), put("y", "1"))}, st("x", "1", "y", "0"), Fail, VAckedLost, ""},
		{"rejected visible", st(), []*Op{wop("a", "1", Rejected, 0, 5, put("k", "1"))}, st("k", "1"), Fail, VRejectedVisible, ""},
		{"rejected among others visible", st(), []*Op{wop("a", "1", Acked, 0, 5, put("k", "1")), wop("b", "1", Rejected, 6, 9, put("k", "2"))}, st("k", "2"), Fail, VRejectedVisible, ""},
		{"corrupted value", st(), []*Op{wop("a", "1", Acked, 0, 5, put("k", "1"))}, st("k", "garbage"), Fail, VNeverWritten, ""},
		{"untouched key changed", st("z", "0"), []*Op{wop("a", "1", Acked, 0, 5, put("k", "1"))}, st("k", "1"), Fail, VUntouchedChanged, ""},
		{"concurrent same key, either wins (1)", st(), []*Op{wop("a", "1", Acked, 0, 10, put("k", "1")), wop("b", "2", Acked, 5, 15, put("k", "2"))}, st("k", "1"), Pass, "", ""},
		{"concurrent same key, either wins (2)", st(), []*Op{wop("a", "1", Acked, 0, 10, put("k", "1")), wop("b", "2", Acked, 5, 15, put("k", "2"))}, st("k", "2"), Pass, "", ""},
		{"batch last op wins: put then del", st(), []*Op{wop("a", "1", Acked, 0, 10, put("x", "1"), del("x"))}, st(), Pass, "", ""},
		{"batch last op wins violated", st(), []*Op{wop("a", "1", Acked, 0, 10, put("x", "1"), del("x"))}, st("x", "1"), Fail, VPartialBatch, ""},
		{"batch intermediate delete visible", st("x", "0"), []*Op{wop("a", "1", Acked, 0, 10, del("x"), put("x", "1"))}, st(), Fail, VPartialBatch, ""},
		{"batch del then put", st("x", "0"), []*Op{wop("a", "1", Acked, 0, 10, del("x"), put("x", "2"))}, st("x", "2"), Pass, "", ""},
		{"missing-key delete", st("a", "1"), []*Op{wop("d", "1", Acked, 0, 10, del("nope"))}, st("a", "1"), Pass, "", ""},
		{"read sees acked write", st(), []*Op{wop("a", "1", Acked, 0, 10, put("k", "1")), gop("r", "2", 11, 12, "k", true, "1")}, st("k", "1"), Pass, "", ""},
		{"stale read after ack", st("k", "0"), []*Op{wop("a", "1", Acked, 0, 10, put("k", "1")), gop("r", "2", 11, 12, "k", true, "0")}, st("k", "1"), Fail, VBadRead, ""},
		{"read of future write", st(), []*Op{gop("r", "2", 0, 5, "k", true, "1"), wop("a", "1", Acked, 6, 10, put("k", "1"))}, st("k", "1"), Fail, VBadRead, ""},
		{"read of never-written value", st(), []*Op{gop("r", "2", 0, 5, "k", true, "x")}, st(), Fail, VBadRead, ""},
		{"read of unknown write makes it applied", st(), []*Op{wop("a", "1", Unknown, 0, 0, put("k", "1"), put("j", "1")), gop("r", "2", 5, 6, "k", true, "1")}, st(), Fail, VPartialBatch, ""},
		{"read during concurrent write either way", st("k", "0"), []*Op{wop("a", "1", Acked, 0, 10, put("k", "1")), gop("r", "2", 2, 3, "k", true, "0"), gop("s", "3", 4, 5, "k", true, "1")}, st("k", "1"), Pass, "", ""},
		{"non-monotonic reads", st("k", "0"), []*Op{wop("a", "1", Acked, 0, 10, put("k", "1")), gop("r", "2", 2, 3, "k", true, "1"), gop("s", "2", 4, 5, "k", true, "0")}, st("k", "1"), Fail, VBadRead, ""},
		{"half-batch read", st("x", "0", "y", "0"), []*Op{
			wop("b", "1", Acked, 0, 100, put("x", "1"), put("y", "1")),
			gop("r1", "2", 10, 11, "x", true, "1"),
			gop("r2", "2", 12, 13, "y", true, "0"),
		}, st("x", "1", "y", "1"), Fail, VBadRead, ""},
		{"whole-batch reads", st("x", "0", "y", "0"), []*Op{
			wop("b", "1", Acked, 0, 100, put("x", "1"), put("y", "1")),
			gop("r1", "2", 10, 11, "x", true, "0"),
			gop("r2", "2", 12, 13, "y", true, "1"),
		}, st("x", "1", "y", "1"), Pass, "", ""},
		{"concurrency needs order search", st(), []*Op{
			wop("a", "1", Acked, 0, 10, put("k", "1")),
			wop("b", "2", Acked, 5, 15, put("k", "2")),
			gop("c", "3", 11, 12, "k", true, "1"),
		}, st("k", "2"), Pass, "", ""},
		{"concurrency, impossible order", st(), []*Op{
			wop("a", "1", Acked, 0, 10, put("k", "1")),
			wop("b", "2", Acked, 5, 15, put("k", "2")),
			gop("c", "3", 11, 12, "k", true, "2"),
		}, st("k", "1"), Fail, "", ""},
		{"cross-key cycle via unknown batch", st(), []*Op{
			wop("p", "1", Acked, 0, 10, put("x", "p")),
			wop("u", "2", Unknown, 20, 0, put("x", "u"), put("y", "u")),
		}, st("x", "p", "y", "u"), Fail, VPartialBatch, ""},
		{"cross-key, concurrent ok", st(), []*Op{
			wop("p", "1", Acked, 0, 30, put("x", "p")),
			wop("u", "2", Unknown, 20, 0, put("x", "u"), put("y", "u")),
		}, st("x", "p", "y", "u"), Pass, "", ""},
		{"deletes: absent ambiguity", st("k", "0"), []*Op{
			wop("a", "1", Acked, 0, 10, del("k")),
			wop("b", "2", Acked, 5, 15, put("k", "2")),
			wop("c", "1", Unknown, 20, 0, del("k")),
		}, st(), Pass, "", ""},
		{"deletes: lost put after deletes", st("k", "0"), []*Op{
			wop("a", "1", Acked, 0, 10, del("k")),
			wop("b", "2", Acked, 11, 15, put("k", "2")),
		}, st(), Fail, VAckedLost, ""},
	}
	for _, c := range cases {
		v := Check(c.base, c.final, c.ops, L)
		expect(t, c.name, v, c.want, c.kind)
	}
}

func TestOracleInconclusiveIsNotPass(t *testing.T) {
	// A history with many open choices and a tiny step budget.
	var ops []*Op
	for i := 0; i < 40; i++ {
		ops = append(ops, wop(fmt.Sprintf("d%d", i), fmt.Sprint(i), Acked, 0, 1000, del(fmt.Sprintf("k%d", i%4))))
		ops = append(ops, wop(fmt.Sprintf("p%d", i), fmt.Sprint(i+100), Acked, 0, 1000, put(fmt.Sprintf("k%d", i%4), fmt.Sprint(i))))
	}
	v := Check(State{}, State{}, ops, Limits{MaxSteps: 10})
	if v.Result == Pass {
		t.Fatalf("a check that ran out of budget returned PASS")
	}
	if v.Result != Inconclusive {
		t.Logf("result %s (acceptable only if the history is genuinely decided)", v.Result)
	}
	v = Check(State{}, State{}, make([]*Op, 0), Limits{MaxWrites: 1})
	if v.Result != Pass {
		t.Fatalf("empty history: %s", v.Result)
	}
	big := []*Op{wop("a", "1", Acked, 0, 1, put("k", "1")), wop("b", "1", Acked, 2, 3, put("k", "2"))}
	if v := Check(State{}, st("k", "2"), big, Limits{MaxWrites: 1}); v.Result != Inconclusive {
		t.Fatalf("over MaxWrites: got %s, want INCONCLUSIVE", v.Result)
	}
}

// ---- brute force cross-check ----

// bruteForce decides the same question by enumerating every subset of
// unknown writes and every order of the chosen writes and reads that respects
// real time. Exponential; only for tiny histories.
func bruteForce(base, final State, ops []*Op) bool {
	var writes, reads []*Op
	var unknown []*Op
	for _, o := range ops {
		switch {
		case o.Kind.IsWrite() && o.Status == Acked:
			writes = append(writes, o)
		case o.Kind.IsWrite() && o.Status != Rejected:
			unknown = append(unknown, o)
		case o.Kind == KGet && o.Status == Acked:
			reads = append(reads, o)
		}
	}
	before := func(a, b *Op) bool { return a.Status == Acked && a.End >= 0 && a.End < b.Start }
	for mask := 0; mask < 1<<len(unknown); mask++ {
		nodes := append([]*Op(nil), writes...)
		for i, u := range unknown {
			if mask&(1<<i) != 0 {
				nodes = append(nodes, u)
			}
		}
		nodes = append(nodes, reads...)
		used := make([]bool, len(nodes))
		order := make([]*Op, 0, len(nodes))
		var rec func() bool
		rec = func() bool {
			if len(order) == len(nodes) {
				cur := Apply(base)
				for _, o := range order {
					if o.Kind == KGet {
						c := cur.Cell(o.Key)
						if c != (Cell{Present: o.Found, Val: o.Read}) {
							return false
						}
						continue
					}
					cur = Apply(cur, o)
				}
				if len(cur) != len(final) {
					return false
				}
				for k, v := range final {
					if cur[k] != v {
						return false
					}
				}
				return true
			}
			for i, n := range nodes {
				if used[i] {
					continue
				}
				ok := true
				for j, m := range nodes {
					if !used[j] && j != i && before(m, n) {
						ok = false
						break
					}
				}
				if !ok {
					continue
				}
				used[i] = true
				order = append(order, n)
				if rec() {
					return true
				}
				order = order[:len(order)-1]
				used[i] = false
			}
			return false
		}
		if rec() {
			return true
		}
	}
	return false
}

func randomTinyHistory(rng *rand.Rand) (State, []*Op, State) {
	keys := []string{"a", "b", "c"}[:1+rng.Intn(3)]
	base := State{}
	for _, k := range keys {
		if rng.Intn(2) == 0 {
			base[k] = val("base-" + k)
		}
	}
	nw := 1 + rng.Intn(5)
	nr := rng.Intn(3)
	var ops []*Op
	for i := 0; i < nw; i++ {
		start := int64(rng.Intn(40))
		end := start + int64(1+rng.Intn(25))
		n := 1 + rng.Intn(3)
		var muts []Mut
		for j := 0; j < n; j++ {
			k := keys[rng.Intn(len(keys))]
			if rng.Intn(3) == 0 {
				muts = append(muts, del(k))
			} else {
				muts = append(muts, put(k, fmt.Sprintf("w%d-%d", i, j)))
			}
		}
		stt := []Status{Acked, Acked, Unknown, Rejected}[rng.Intn(4)]
		ops = append(ops, wop(fmt.Sprintf("w%d", i), fmt.Sprintf("W%d", i), stt, start, end, muts...))
	}
	// Candidate observations: take one random legal-looking execution, then
	// sometimes perturb it.
	cur := Apply(base)
	order := rng.Perm(len(ops))
	sort.SliceStable(order, func(i, j int) bool { return ops[order[i]].Start < ops[order[j]].Start })
	for i := 0; i < nr; i++ {
		k := keys[rng.Intn(len(keys))]
		start := int64(rng.Intn(60))
		g := gop(fmt.Sprintf("r%d", i), fmt.Sprintf("R%d", i), start, start+int64(1+rng.Intn(10)), k, false, "")
		ops = append(ops, g)
	}
	for _, i := range order {
		o := ops[i]
		if o.Kind.IsWrite() && o.Status != Rejected && (o.Status == Acked || rng.Intn(2) == 0) {
			cur = Apply(cur, o)
		}
	}
	for _, o := range ops {
		if o.Kind != KGet {
			continue
		}
		// Read a random plausible value of its key.
		var choices []Cell
		choices = append(choices, base.Cell(o.Key), cur.Cell(o.Key))
		for _, w := range ops {
			if c, ok := w.Effects()[o.Key]; ok && w.Kind.IsWrite() {
				choices = append(choices, c)
			}
		}
		c := choices[rng.Intn(len(choices))]
		o.Found, o.Read = c.Present, c.Val
	}
	final := cur
	if rng.Intn(4) == 0 && len(keys) > 0 {
		k := keys[rng.Intn(len(keys))]
		if rng.Intn(2) == 0 {
			delete(final, k)
		} else {
			final[k] = val(fmt.Sprintf("w%d-%d", rng.Intn(nw), rng.Intn(3)))
		}
	}
	return base, ops, final
}

func TestOracleMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	n := 20000
	if testing.Short() {
		n = 3000
	}
	var pass, fail int
	for i := 0; i < n; i++ {
		base, ops, final := randomTinyHistory(rng)
		want := bruteForce(base, final, ops)
		v := Check(base, final, ops, DefaultLimits())
		if v.Result == Inconclusive {
			t.Fatalf("case %d: inconclusive on a tiny history: %s", i, v.Reason)
		}
		if (v.Result == Pass) != want {
			t.Fatalf("case %d: oracle %s, brute force legal=%v\nbase=%v final=%v\n%s\n%v", i, v.Result, want, base, final, dumpOps(ops), v.Violation)
		}
		if want {
			pass++
		} else {
			fail++
		}
	}
	t.Logf("%d tiny histories: %d legal, %d illegal; oracle agreed with brute force on all", n, pass, fail)
	if pass < n/10 || fail < n/10 {
		t.Fatalf("generator is unbalanced: %d legal, %d illegal", pass, fail)
	}
}

func dumpOps(ops []*Op) string {
	s := ""
	for _, o := range ops {
		s += fmt.Sprintf("  %s %s %s [%d,%d] key=%s found=%v read=%s muts=", o.ID, o.Kind, o.Status, o.Start, o.End, o.Key, o.Found, o.Read)
		for _, m := range o.Muts {
			if m.Del {
				s += fmt.Sprintf(" del(%s)", m.Key)
			} else {
				s += fmt.Sprintf(" put(%s=%s)", m.Key, m.Val)
			}
		}
		s += "\n"
	}
	return s
}

// ---- stress: large simulated linearizable histories ----

type simConfig struct {
	writers, opsPerWriter, keys int
	batchP, delP, readP         float64
	batchMax                    int
	crashFrac                   float64
}

// simulate runs a linearizable in-memory store with concurrent writers,
// random commit points inside each request's interval, and a crash.
func simulate(rng *rand.Rand, c simConfig) (State, []*Op, State) {
	type pending struct {
		op     *Op
		commit int64
	}
	base := State{}
	for i := 0; i < c.keys/2; i++ {
		base[fmt.Sprintf("k%d", i)] = val(fmt.Sprintf("base-%d", i))
	}
	var all []pending
	for w := 0; w < c.writers; w++ {
		t := int64(rng.Intn(1000))
		for i := 0; i < c.opsPerWriter; i++ {
			id := fmt.Sprintf("w%d-%d", w, i)
			o := &Op{ID: id, Writer: fmt.Sprint(w), Start: t}
			r := rng.Float64()
			switch {
			case r < c.readP:
				o.Kind, o.Key = KGet, fmt.Sprintf("k%d", rng.Intn(c.keys))
			case r < c.readP+c.batchP:
				o.Kind = KBatch
				n := 2 + rng.Intn(c.batchMax-1)
				for j := 0; j < n; j++ {
					k := fmt.Sprintf("k%d", rng.Intn(c.keys))
					if rng.Float64() < c.delP {
						o.Muts = append(o.Muts, del(k))
					} else {
						o.Muts = append(o.Muts, put(k, id+"/"+fmt.Sprint(j)))
					}
				}
			default:
				k := fmt.Sprintf("k%d", rng.Intn(c.keys))
				if rng.Float64() < c.delP {
					o.Kind, o.Muts = KDel, []Mut{del(k)}
				} else {
					o.Kind, o.Muts = KPut, []Mut{put(k, id+"/0")}
				}
			}
			dur := int64(50 + rng.Intn(2000))
			commit := t + 1 + rng.Int63n(dur)
			o.End = t + dur + 1
			all = append(all, pending{o, commit})
			t = o.End + int64(rng.Intn(300))
		}
	}
	// Crash time.
	var maxT int64
	for _, p := range all {
		if p.op.End > maxT {
			maxT = p.op.End
		}
	}
	crash := int64(float64(maxT) * c.crashFrac)
	sort.Slice(all, func(i, j int) bool { return all[i].commit < all[j].commit })
	cur := Apply(base)
	var ops []*Op
	for _, p := range all {
		o := p.op
		if o.Start >= crash {
			continue // never sent
		}
		committed := p.commit < crash
		if o.Kind == KGet {
			if !committed || o.End >= crash {
				o.Status, o.End = Unknown, -1
			} else {
				c := cur.Cell(o.Key)
				o.Status, o.Found, o.Read = Acked, c.Present, c.Val
			}
			ops = append(ops, o)
			continue
		}
		if committed {
			cur = Apply(cur, o)
		}
		if committed && o.End < crash {
			o.Status = Acked
		} else {
			o.Status, o.End = Unknown, -1
		}
		ops = append(ops, o)
	}
	return base, ops, cur
}

func TestOracleStressLegalHistoriesPass(t *testing.T) {
	// Realistic shapes (the engine's workloads are like these) must PASS.
	// The adversarial shape (4 keys, half the writes deletes, many reads that
	// return "absent", each with hundreds of possible source deletes) may hit
	// the search budget: it must then say INCONCLUSIVE, never FAIL or PASS.
	cfgs := []struct {
		name        string
		c           simConfig
		adversarial bool
	}{
		{"8 writers, 200 keys, mixed", simConfig{writers: 8, opsPerWriter: 2500, keys: 200, batchP: 0.2, delP: 0.2, readP: 0.2, batchMax: 20, crashFrac: 0.8}, false},
		{"2 writers, 1 hot key", simConfig{writers: 2, opsPerWriter: 5000, keys: 1, batchP: 0, delP: 0.1, readP: 0.2, batchMax: 2, crashFrac: 0.9}, false},
		{"16 writers, big batches", simConfig{writers: 16, opsPerWriter: 300, keys: 5000, batchP: 0.5, delP: 0.2, readP: 0.1, batchMax: 400, crashFrac: 0.6}, false},
		{"64 keys, 10k-op batches", simConfig{writers: 3, opsPerWriter: 400, keys: 20000, batchP: 0.05, delP: 0.2, readP: 0.15, batchMax: 10000, crashFrac: 0.7}, false},
		{"adversarial: 4 keys, heavy deletes", simConfig{writers: 4, opsPerWriter: 3000, keys: 4, batchP: 0.3, delP: 0.5, readP: 0.3, batchMax: 6, crashFrac: 0.7}, true},
	}
	if testing.Short() {
		for i := range cfgs {
			cfgs[i].c.opsPerWriter /= 5
		}
	}
	for seed := int64(1); seed <= 3; seed++ {
		for _, c := range cfgs {
			rng := rand.New(rand.NewSource(seed))
			base, ops, final := simulate(rng, c.c)
			t0 := time.Now()
			lim := DefaultLimits()
			if c.adversarial {
				lim.MaxSteps = 300_000_000
			}
			v := Check(base, final, ops, lim)
			if v.Result == Fail || (v.Result == Inconclusive && !c.adversarial) {
				t.Fatalf("%s seed %d: legal history judged %s: %s %v", c.name, seed, v.Result, v.Reason, v.Violation)
			}
			if v.Result == Inconclusive {
				t.Logf("%s seed %d: %d ops: INCONCLUSIVE (%s) in %s -- reported as such, not as PASS", c.name, seed, len(ops), v.Reason, time.Since(t0).Round(time.Millisecond))
				continue
			}
			t.Logf("%s seed %d: %d ops (%d unknown, %d reads), PASS in %s, %d steps, %d choices, %d backtracks",
				c.name, seed, len(ops), v.Stats.Unknown, v.Stats.Reads, time.Since(t0).Round(time.Millisecond), v.Stats.Steps, v.Stats.Choices, v.Stats.Backtracks)
		}
	}
}

func TestOracleStressInjectedViolationsFail(t *testing.T) {
	c := simConfig{writers: 6, opsPerWriter: 1500, keys: 300, batchP: 0.25, delP: 0.2, readP: 0.15, batchMax: 12, crashFrac: 0.8}
	if testing.Short() {
		c.opsPerWriter = 300
	}
	injections := []struct {
		name string
		kind []ViolationKind
		do   func(rng *rand.Rand, base State, ops []*Op, final State) bool
	}{
		{"drop last acked put", []ViolationKind{VAckedLost}, func(rng *rand.Rand, base State, ops []*Op, final State) bool {
			// An acked put that is certainly the last write of its key (every
			// other write to it was acknowledged before the put was sent):
			// losing it is illegal in every execution.
			for i := len(ops) - 1; i >= 0; i-- {
				o := ops[i]
				if o.Kind == KPut && o.Status == Acked && final[o.Muts[0].Key] == o.Muts[0].Val && soleLast(ops, o, o.Muts[0].Key) {
					delete(final, o.Muts[0].Key)
					return true
				}
			}
			return false
		}},
		{"partial batch", []ViolationKind{VPartialBatch, VAckedLost}, func(rng *rand.Rand, base State, ops []*Op, final State) bool {
			for i := len(ops) - 1; i >= 0; i-- {
				o := ops[i]
				if o.Kind != KBatch || o.Status != Acked {
					continue
				}
				eff := o.Effects()
				keys := sortedKeys(eff)
				if len(keys) < 2 {
					continue
				}
				for _, k := range keys {
					c := eff[k]
					if c.Present && final.Cell(k) == c && soleLast(ops, o, k) {
						delete(final, k) // the batch's other keys stay visible
						return true
					}
				}
			}
			return false
		}},
		{"corrupt a value", []ViolationKind{VNeverWritten}, func(rng *rand.Rand, base State, ops []*Op, final State) bool {
			ks := make([]string, 0, len(final))
			for k := range final {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			final[ks[len(ks)/2]] = val("bitflip")
			return true
		}},
		{"rejected write visible", []ViolationKind{VRejectedVisible}, func(rng *rand.Rand, base State, ops []*Op, final State) bool {
			ops = append(ops, nil)
			r := &Op{ID: "rej", Writer: "R", Kind: KPut, Muts: []Mut{put("k1", "rejected")}, Start: 1, End: 2, Status: Rejected}
			ops[len(ops)-1] = r
			final["k1"] = val("rejected")
			rejectedHack = r
			return true
		}},
		{"stale read", []ViolationKind{VBadRead}, func(rng *rand.Rand, base State, ops []*Op, final State) bool {
			// A read that returns a value certainly overwritten before it was sent.
			for i, o := range ops {
				if o.Kind != KPut || o.Status != Acked {
					continue
				}
				for _, p := range ops[i+1:] {
					if p.Kind == KPut && p.Status == Acked && p.Muts[0].Key == o.Muts[0].Key && p.Start > o.End {
						g := gop("stale", "S", p.End+1, p.End+2, o.Muts[0].Key, true, "")
						g.Read = o.Muts[0].Val
						staleHack = g
						return true
					}
				}
			}
			return false
		}},
	}
	for _, inj := range injections {
		for seed := int64(1); seed <= 3; seed++ {
			rng := rand.New(rand.NewSource(seed))
			base, ops, final := simulate(rng, c)
			rejectedHack, staleHack = nil, nil
			if !inj.do(rng, base, ops, final) {
				t.Fatalf("%s seed %d: injection not applicable", inj.name, seed)
			}
			if rejectedHack != nil {
				ops = append(ops, rejectedHack)
			}
			if staleHack != nil {
				ops = append(ops, staleHack)
			}
			v := Check(base, final, ops, DefaultLimits())
			if v.Result != Fail {
				t.Fatalf("%s seed %d: injected violation judged %s", inj.name, seed, v.Result)
			}
			ok := false
			for _, k := range inj.kind {
				ok = ok || v.Violation.Kind == k
			}
			if !ok {
				t.Fatalf("%s seed %d: classified as %s\n%s", inj.name, seed, v.Violation.Kind, v.Violation)
			}
			t.Logf("%s seed %d: FAIL %s in %s", inj.name, seed, v.Violation.Kind, v.Stats.Elapsed.Round(time.Millisecond))
		}
	}
}

var rejectedHack, staleHack *Op

// soleLast: every other non-rejected write to key k was acknowledged before
// o was sent, so o is the last write of k in every legal execution.
func soleLast(ops []*Op, o *Op, k string) bool {
	for _, p := range ops {
		if p == o || !p.Kind.IsWrite() || p.Status == Rejected {
			continue
		}
		if _, ok := p.Effects()[k]; !ok {
			continue
		}
		if p.Status != Acked || p.End >= o.Start {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]Cell) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestHistoryRecorderRules(t *testing.T) {
	var now int64
	h := NewHistory(func() int64 { now++; return now })
	a := &Op{ID: "a", Writer: "w", Kind: KPut, Muts: []Mut{put("k", "1")}}
	b := &Op{ID: "b", Writer: "w", Kind: KPut, Muts: []Mut{put("k", "2")}}
	if err := h.Begin(a); err != nil {
		t.Fatal(err)
	}
	if err := h.Begin(b); err == nil {
		t.Fatal("second request of one writer while the first is in flight was accepted")
	}
	if err := h.Finish(a, Acked, ""); err != nil {
		t.Fatal(err)
	}
	if err := h.Finish(a, Acked, ""); err == nil {
		t.Fatal("double acknowledgement accepted")
	}
	if err := h.Begin(&Op{ID: "a", Writer: "x"}); err == nil {
		t.Fatal("duplicate op id accepted")
	}
	if err := h.Begin(b); err != nil {
		t.Fatal(err)
	}
	lost := h.Crash()
	if len(lost) != 1 || lost[0] != b || b.Status != Unknown || b.End != -1 {
		t.Fatalf("crash did not mark the in-flight request unknown: %+v", lost)
	}
	if a.End <= a.Start {
		t.Fatal("ack time not after send time")
	}
}
