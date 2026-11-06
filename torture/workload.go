package main

import (
	"fmt"
	"math/rand"
	"strings"
)

// generator produces each writer's deterministic request stream. The same
// --seed gives the same requests and kill delays; which of them complete
// before the kill depends on OS scheduling.
type generator struct {
	wl   workload
	iter int
	seed int64
}

func newGenerator(wl workload, iter int, seed int64) *generator {
	return &generator{wl: wl, iter: iter, seed: seed}
}

type writerGen struct {
	g   *generator
	w   int
	rng *rand.Rand
}

func (g *generator) writer(w int) *writerGen {
	return &writerGen{g: g, w: w, rng: rand.New(rand.NewSource(g.seed*7919 + int64(w)*104729 + 1))}
}

const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.:"

func randText(rng *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(b)
}

// key picks a key for a single request.
func (wg *writerGen) key() string {
	wl := wg.g.wl
	switch {
	case wl.sameKey:
		return "hot"
	case wl.disjoint:
		return fmt.Sprintf("w%d-k%03d", wg.w, wg.rng.Intn(wl.keys))
	case wl.bigBatch && wg.rng.Intn(10) == 0:
		return fmt.Sprintf("b%05d", wg.rng.Intn(20000)) // overlap with the big batches
	default:
		return fmt.Sprintf("k%04d", wg.rng.Intn(wl.keys))
	}
}

// value builds a unique value: it starts with the op ID and position, so a
// value can never be produced by two different requests, then random text up
// to the chosen length.
func (wg *writerGen) value(id string, idx int) string {
	wl := wg.g.wl
	n := wl.minVal
	if wl.maxVal > wl.minVal {
		n += wg.rng.Intn(wl.maxVal - wl.minVal + 1)
	}
	if wl.bigValP > 0 && wg.rng.Float64() < wl.bigValP {
		n = oneMiB
	}
	prefix := fmt.Sprintf("%s/%d/", id, idx)
	if n < len(prefix) {
		n = len(prefix)
	}
	if n > oneMiB {
		n = oneMiB
	}
	return prefix + randText(wg.rng, n-len(prefix))
}

// next returns the request record and the exact bytes to send.
func (wg *writerGen) next(seq int) (*opRec, []byte) {
	wl := wg.g.wl
	id := fmt.Sprintf("i%d-w%d-%06d", wg.g.iter, wg.w, seq)
	o := &opRec{id: id, writer: wg.w, effects: map[string]effect{}}
	hdr := fmt.Sprintf("w%d %s ", wg.w, id)
	if wl.bigBatch && wg.w == 0 {
		return wg.batch(o, hdr, 10000, true)
	}
	total := wl.pPut + wl.pDel + wl.pBatch + wl.pGet + wl.pCompact
	r := wg.rng.Intn(total)
	switch {
	case r < wl.pPut:
		k := wg.key()
		v := wg.value(id, 0)
		o.kind, o.nops = "put", 1
		o.effects[k] = effect{hash: valueHash([]byte(v))}
		return o, []byte(hdr + "put " + k + " " + v + "\n")
	case r < wl.pPut+wl.pDel:
		k := wg.key()
		o.kind, o.nops = "del", 1
		o.effects[k] = effect{del: true}
		return o, []byte(hdr + "del " + k + "\n")
	case r < wl.pPut+wl.pDel+wl.pBatch:
		n := wl.batchMin + wg.rng.Intn(wl.batchMax-wl.batchMin+1)
		return wg.batch(o, hdr, n, false)
	case r < wl.pPut+wl.pDel+wl.pBatch+wl.pGet:
		k := wg.key()
		o.kind, o.getKey = "get", k
		return o, []byte(hdr + "get " + k + "\n")
	default:
		o.kind = "compact"
		return o, []byte(hdr + "compact\n")
	}
}

// batch builds a batch of n operations. Keys may repeat inside a batch; the
// effect recorded per key is the last operation on it (the engine's
// documented order: operations apply in order, last one wins).
func (wg *writerGen) batch(o *opRec, hdr string, n int, big bool) (*opRec, []byte) {
	o.kind, o.nops = "batch", n
	var sb strings.Builder
	fmt.Fprintf(&sb, "%sbatch %d\n", hdr, n)
	for i := 0; i < n; i++ {
		var k string
		if big {
			k = fmt.Sprintf("b%05d", wg.rng.Intn(20000))
		} else {
			k = wg.key()
		}
		if wg.rng.Intn(5) == 0 {
			sb.WriteString("del " + k + "\n")
			o.effects[k] = effect{del: true}
			continue
		}
		v := wg.value(o.id, i)
		sb.WriteString("put " + k + " " + v + "\n")
		o.effects[k] = effect{hash: valueHash([]byte(v))}
	}
	return o, []byte(sb.String())
}
