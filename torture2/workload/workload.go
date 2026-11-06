// Package workload generates the torture engine's requests. Everything is
// derived from (seed, iteration, stream): the same three numbers always give
// the same requests, so a failure is reproducible from its seed and
// iteration number. Which requests complete before a kill still depends on
// OS scheduling.
package workload

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/oracle"
)

// OneMiB is the largest value NEXUS accepts.
const OneMiB = 1 << 20

// Mode is one workload shape.
type Mode struct {
	Name        string
	Description string
	Writers     int // concurrent writer streams (each one request at a time)
	Readers     int // extra streams that only send gets (readers mode)
	Keys        int // shared key space k0000..
	Disjoint    bool
	SameKey     bool
	MinVal      int
	MaxVal      int
	BigValP     float64 // probability a put uses exactly 1 MiB
	// Request mix weights.
	PPut, PDel, PDelMissing, PBatch, PGet, PCompact int
	BatchMin, BatchMax                              int
	BigBatch                                        bool // writer 0 sends 10,000-op batches
	GroupBatch                                      bool // writer 0 sends 10-op batches over g0..g9; readers get them
	MinKillMs, MaxKillMs                            int
	MaxBytes                                        int64
	RecoveryKills                                   bool // kill `kvdb open` 1-3 times after each serve kill
	PreloadKeys                                     int
	CloseP                                          float64 // probability an iteration ends with a graceful close (C6)
}

// Modes returns every mode in a stable order.
func Modes() []Mode {
	base := Mode{Writers: 3, Keys: 64, MinVal: 8, MaxVal: 256,
		PPut: 40, PDel: 12, PDelMissing: 3, PBatch: 20, PGet: 20, PCompact: 5,
		BatchMin: 2, BatchMax: 20, MinKillMs: 5, MaxKillMs: 600}
	m := func(name, desc string, f func(*Mode)) Mode {
		x := base
		x.Name, x.Description = name, desc
		if f != nil {
			f(&x)
		}
		return x
	}
	return []Mode{
		m("default", "3 writers: puts, deletes (some of missing keys), 2-20-op batches, gets, compactions; kill at 5-600 ms", nil),
		m("batch10", "exactly 10-op batches (scenario group B) racing single writes", func(x *Mode) {
			x.PPut, x.PDel, x.PDelMissing, x.PBatch, x.PGet, x.PCompact = 20, 10, 0, 50, 20, 0
			x.BatchMin, x.BatchMax = 10, 10
		}),
		m("large-values", "values of 256 KiB-1 MiB, half exactly 1 MiB", func(x *Mode) {
			x.Writers, x.Keys, x.MinVal, x.MaxVal, x.BigValP = 2, 16, 256<<10, OneMiB, 0.5
			x.PPut, x.PDel, x.PDelMissing, x.PBatch, x.PGet, x.PCompact = 60, 12, 3, 10, 15, 0
			x.BatchMin, x.BatchMax, x.MaxKillMs = 2, 3, 1500
		}),
		m("large-batch", "writer 0 sends 10,000-op batches; writers 1-2 single puts/deletes, some on the batch keys", func(x *Mode) {
			x.BigBatch, x.MinVal, x.MaxVal = true, 8, 48
			x.PPut, x.PDel, x.PDelMissing, x.PBatch, x.PGet, x.PCompact = 60, 15, 5, 5, 15, 0
			x.MaxKillMs = 2500
		}),
		m("concurrent-disjoint", "2 writers on disjoint key sets (scenario D1)", func(x *Mode) {
			x.Writers, x.Disjoint = 2, true
			x.PPut, x.PDel, x.PDelMissing, x.PBatch, x.PGet, x.PCompact = 70, 15, 0, 0, 15, 0
		}),
		m("same-key", "2 writers putting 1-64 KiB values to one key (scenario D2)", func(x *Mode) {
			x.Writers, x.SameKey = 2, true
			x.PPut, x.PDel, x.PDelMissing, x.PBatch, x.PGet, x.PCompact = 80, 10, 0, 0, 10, 0
			x.MinVal, x.MaxVal = 1024, 64<<10
		}),
		m("readers", "writer 0 sends 10-op batches over g0..g9 while 2 readers get those keys (scenario D4)", func(x *Mode) {
			x.Writers, x.Readers, x.GroupBatch = 1, 2, true
			x.MinKillMs, x.MaxKillMs = 50, 800
		}),
		m("compaction", "1-16 KiB values with frequent compaction requests (scenarios C3-C5)", func(x *Mode) {
			x.Keys, x.MinVal, x.MaxVal = 128, 1<<10, 16<<10
			x.PPut, x.PDel, x.PDelMissing, x.PBatch, x.PGet, x.PCompact = 55, 10, 2, 10, 8, 15
			x.BatchMin, x.BatchMax, x.MinKillMs, x.MaxKillMs = 2, 8, 20, 1500
		}),
		m("recovery-crash", "200,000-key database; after each serve kill, kvdb open is killed 1-3 times during recovery (C1, C2, F2)", func(x *Mode) {
			x.RecoveryKills, x.PreloadKeys, x.MaxKillMs = true, 200_000, 800
		}),
		m("disk-full", "--max-bytes 262144: writes hit the cap, deletes and compaction free space (group E)", func(x *Mode) {
			x.Keys, x.MinVal, x.MaxVal, x.MaxBytes = 600, 256, 2048, 256<<10
			x.PPut, x.PDel, x.PDelMissing, x.PBatch, x.PGet, x.PCompact = 52, 25, 3, 10, 5, 5
			x.BatchMin, x.BatchMax, x.MaxKillMs = 2, 10, 1200
		}),
		m("close-reopen", "like default, but 60% of iterations end with a graceful close (stdin EOF), sometimes killed during the close (C6)", func(x *Mode) {
			x.CloseP = 0.6
		}),
		m("mixed-long", "long-running mixed workload: everything, kill at 2-8 s", func(x *Mode) {
			x.Writers, x.Keys, x.BigValP, x.MaxVal = 4, 256, 0.01, 4096
			x.MinKillMs, x.MaxKillMs = 2000, 8000
		}),
	}
}

// ByName returns the mode called name.
func ByName(name string) (Mode, error) {
	var names []string
	for _, m := range Modes() {
		if m.Name == name {
			return m, nil
		}
		names = append(names, m.Name)
	}
	return Mode{}, fmt.Errorf("unknown mode %q (modes: %s)", name, strings.Join(names, ", "))
}

// Names lists the mode names.
func Names() []string {
	var out []string
	for _, m := range Modes() {
		out = append(out, m.Name)
	}
	return out
}

func splitmix(z uint64) uint64 {
	z += 0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// IterSeed derives iteration i's seed from the run seed. Iterations are
// independent: iteration i's requests do not depend on any other iteration.
func IterSeed(seed int64, iter int) int64 {
	return int64(splitmix(uint64(seed)^splitmix(uint64(iter))) >> 1)
}

// StreamSeed derives an independent stream (writer, kill schedule, ...) of
// an iteration.
func StreamSeed(iterSeed int64, stream int) int64 {
	return int64(splitmix(uint64(iterSeed)+uint64(stream)*0xD1B54A32D192ED03) >> 1)
}

// Stream numbers.
const (
	StreamSchedule = 0 // kill delay, close decision, recovery kills
	StreamWriter0  = 1 // writer w uses StreamWriter0 + w
)

// Request is one request: the oracle's record and the exact bytes to send.
type Request struct {
	Op      *oracle.Op
	Payload []byte
}

// Writer generates one stream's requests.
type Writer struct {
	mode    Mode
	iter    int
	w       int
	reader  bool
	rng     *rand.Rand
	group   int // readers: next group key to read
	batchNo int
}

// NewWriter returns stream w of iteration iter. Streams w >= mode.Writers
// are readers (readers mode).
func NewWriter(mode Mode, iter int, iterSeed int64, w int) *Writer {
	return &Writer{mode: mode, iter: iter, w: w, reader: w >= mode.Writers,
		rng: rand.New(rand.NewSource(StreamSeed(iterSeed, StreamWriter0+w)))}
}

// Name is the writer ID used on the serve protocol.
func (g *Writer) Name() string {
	if g.reader {
		return fmt.Sprintf("r%d", g.w)
	}
	return fmt.Sprintf("w%d", g.w)
}

const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.:"

func randText(rng *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(b)
}

func (g *Writer) key() string {
	m := g.mode
	switch {
	case m.SameKey:
		return "hot"
	case m.Disjoint:
		return fmt.Sprintf("w%d-k%03d", g.w, g.rng.Intn(m.Keys))
	case m.BigBatch && g.rng.Intn(10) == 0:
		return fmt.Sprintf("b%05d", g.rng.Intn(20000))
	}
	return fmt.Sprintf("k%04d", g.rng.Intn(m.Keys))
}

// value builds a value unique to (op, position): it starts with the op ID,
// so no two requests ever write the same value. That lets the oracle tell
// exactly which request a recovered value came from.
func (g *Writer) value(id string, idx int) string {
	m := g.mode
	n := m.MinVal
	if m.MaxVal > m.MinVal {
		n += g.rng.Intn(m.MaxVal - m.MinVal + 1)
	}
	if m.BigValP > 0 && g.rng.Float64() < m.BigValP {
		n = OneMiB
	}
	prefix := fmt.Sprintf("%s/%d/", id, idx)
	if n < len(prefix) {
		n = len(prefix)
	}
	if n > OneMiB {
		n = OneMiB
	}
	return prefix + randText(g.rng, n-len(prefix))
}

// Next returns the writer's seq-th request.
func (g *Writer) Next(seq int) Request {
	m := g.mode
	id := fmt.Sprintf("i%d-%s-%06d", g.iter, g.Name(), seq)
	o := &oracle.Op{ID: id, Writer: g.Name()}
	hdr := fmt.Sprintf("%s %s ", g.Name(), id)
	if g.reader {
		k := fmt.Sprintf("g%d", g.group%10)
		g.group++
		o.Kind, o.Key = oracle.KGet, k
		return Request{o, []byte(hdr + "get " + k + "\n")}
	}
	if m.GroupBatch && g.w == 0 {
		return g.groupBatch(o, hdr)
	}
	if m.BigBatch && g.w == 0 {
		return g.batch(o, hdr, 10000, true)
	}
	total := m.PPut + m.PDel + m.PDelMissing + m.PBatch + m.PGet + m.PCompact
	r := g.rng.Intn(total)
	switch {
	case r < m.PPut:
		k := g.key()
		v := g.value(id, 0)
		o.Kind = oracle.KPut
		o.Muts = []oracle.Mut{{Key: k, Val: oracle.HashValue([]byte(v))}}
		return Request{o, []byte(hdr + "put " + k + " " + v + "\n")}
	case r < m.PPut+m.PDel:
		k := g.key()
		o.Kind, o.Muts = oracle.KDel, []oracle.Mut{{Key: k, Del: true}}
		return Request{o, []byte(hdr + "del " + k + "\n")}
	case r < m.PPut+m.PDel+m.PDelMissing:
		k := "missing-" + id // never written by anyone
		o.Kind, o.Muts = oracle.KDel, []oracle.Mut{{Key: k, Del: true}}
		return Request{o, []byte(hdr + "del " + k + "\n")}
	case r < m.PPut+m.PDel+m.PDelMissing+m.PBatch:
		n := m.BatchMin + g.rng.Intn(m.BatchMax-m.BatchMin+1)
		return g.batch(o, hdr, n, false)
	case r < m.PPut+m.PDel+m.PDelMissing+m.PBatch+m.PGet:
		k := g.key()
		o.Kind, o.Key = oracle.KGet, k
		return Request{o, []byte(hdr + "get " + k + "\n")}
	default:
		o.Kind = oracle.KCompact
		return Request{o, []byte(hdr + "compact\n")}
	}
}

// batch builds an n-op batch. Keys may repeat; one in five ops is a delete,
// and sometimes a put and a delete of one key are adjacent, exercising the
// in-order, last-op-wins rule (scenario B5).
func (g *Writer) batch(o *oracle.Op, hdr string, n int, big bool) Request {
	o.Kind = oracle.KBatch
	var sb strings.Builder
	fmt.Fprintf(&sb, "%sbatch %d\n", hdr, n)
	for i := 0; i < n; i++ {
		var k string
		if big {
			k = fmt.Sprintf("b%05d", g.rng.Intn(20000))
		} else if i > 0 && g.rng.Intn(8) == 0 {
			k = o.Muts[len(o.Muts)-1].Key // same key as the previous op
		} else {
			k = g.key()
		}
		if g.rng.Intn(5) == 0 {
			sb.WriteString("del " + k + "\n")
			o.Muts = append(o.Muts, oracle.Mut{Key: k, Del: true})
			continue
		}
		v := g.value(o.ID, i)
		sb.WriteString("put " + k + " " + v + "\n")
		o.Muts = append(o.Muts, oracle.Mut{Key: k, Val: oracle.HashValue([]byte(v))})
	}
	return Request{o, []byte(sb.String())}
}

// groupBatch writes all ten group keys g0..g9 in one batch.
func (g *Writer) groupBatch(o *oracle.Op, hdr string) Request {
	o.Kind = oracle.KBatch
	g.batchNo++
	var sb strings.Builder
	fmt.Fprintf(&sb, "%sbatch 10\n", hdr)
	for i := 0; i < 10; i++ {
		k := fmt.Sprintf("g%d", i)
		v := fmt.Sprintf("%s/%d/gen%d", o.ID, i, g.batchNo)
		sb.WriteString("put " + k + " " + v + "\n")
		o.Muts = append(o.Muts, oracle.Mut{Key: k, Val: oracle.HashValue([]byte(v))})
	}
	return Request{o, []byte(sb.String())}
}

// Preload returns batch files (10,000 ops each) that create n keys
// p0000000.., deterministic in seed, and the state they produce.
func Preload(n int, seed int64) ([][]byte, oracle.State) {
	rng := rand.New(rand.NewSource(seed))
	st := oracle.State{}
	var files [][]byte
	for base := 0; base < n; base += 10000 {
		var sb strings.Builder
		for i := base; i < base+10000 && i < n; i++ {
			k := fmt.Sprintf("p%07d", i)
			v := randText(rng, 16+rng.Intn(48))
			fmt.Fprintf(&sb, "put %s %s\n", k, v)
			st[k] = oracle.HashValue([]byte(v))
		}
		files = append(files, []byte(sb.String()))
	}
	return files, st
}

// SortedKeys returns the keys of a state in order.
func SortedKeys(s oracle.State) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
