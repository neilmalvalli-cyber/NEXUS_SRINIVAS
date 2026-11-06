package workload

import (
	"bytes"
	"testing"
)

func TestDeterministicAndIndependent(t *testing.T) {
	for _, m := range Modes() {
		for _, w := range []int{0, 1} {
			a := NewWriter(m, 7, IterSeed(42, 7), w)
			b := NewWriter(m, 7, IterSeed(42, 7), w)
			for i := 0; i < 50; i++ {
				ra, rb := a.Next(i), b.Next(i)
				if !bytes.Equal(ra.Payload, rb.Payload) || ra.Op.ID != rb.Op.ID {
					t.Fatalf("%s writer %d request %d differs between two runs", m.Name, w, i)
				}
			}
		}
	}
	if IterSeed(42, 1) == IterSeed(42, 2) || IterSeed(42, 1) == IterSeed(43, 1) {
		t.Fatal("iteration seeds collide")
	}
	if StreamSeed(5, 1) == StreamSeed(5, 2) {
		t.Fatal("stream seeds collide")
	}
	// Iteration 9's requests do not depend on whether iterations 1..8 ran.
	m, _ := ByName("default")
	x := NewWriter(m, 9, IterSeed(1, 9), 0).Next(0)
	_ = NewWriter(m, 3, IterSeed(1, 3), 0).Next(0)
	y := NewWriter(m, 9, IterSeed(1, 9), 0).Next(0)
	if !bytes.Equal(x.Payload, y.Payload) {
		t.Fatal("iteration stream not independent")
	}
}

func TestRequestShapes(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range Modes() {
		g := NewWriter(m, 1, IterSeed(3, 1), 0)
		for i := 0; i < 400; i++ {
			r := g.Next(i)
			if !bytes.HasSuffix(r.Payload, []byte("\n")) {
				t.Fatalf("%s: payload without final newline", m.Name)
			}
			n := bytes.Count(r.Payload, []byte("\n"))
			switch r.Op.Kind.String() {
			case "batch":
				if n != len(r.Op.Muts)+1 {
					t.Fatalf("%s: batch header says %d ops, %d lines", m.Name, len(r.Op.Muts), n-1)
				}
				seen[m.Name+":batch"] = true
				if len(r.Op.Muts) == 10000 {
					seen["10k"] = true
				}
				if len(r.Op.Muts) == 10 {
					seen["10"] = true
				}
			default:
				if n != 1 {
					t.Fatalf("%s: %s has %d lines", m.Name, r.Op.Kind, n)
				}
			}
			for _, mu := range r.Op.Muts {
				if len(mu.Key) > 1024 || mu.Key == "" {
					t.Fatalf("bad key %q", mu.Key)
				}
			}
			if len(r.Payload) > OneMiB+5*1024 && r.Op.Kind.String() != "batch" {
				t.Fatalf("%s: line too long: %d", m.Name, len(r.Payload))
			}
			if bytes.Contains(r.Payload, []byte(" del missing-")) {
				seen["missing-del"] = true
			}
			if len(r.Payload) > OneMiB {
				seen["1MiB"] = true
			}
		}
	}
	for _, k := range []string{"10k", "10", "missing-del", "1MiB"} {
		if !seen[k] {
			t.Errorf("no request of shape %s generated", k)
		}
	}
}
