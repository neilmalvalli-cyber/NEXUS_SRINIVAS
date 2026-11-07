package main

import (
	"strings"
	"testing"
)

// These tests prove the oracle rejects broken recoveries and accepts every
// correct one; a checker that passes everything would be worthless.

func h(s string) string { return valueHash([]byte(s)) }

func put(id string, sendT, endT int64, st status, kv ...string) *opRec {
	o := &opRec{id: id, kind: "put", sendT: sendT, endT: endT, status: st, effects: map[string]effect{}}
	if len(kv) > 2 {
		o.kind = "batch"
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "<del>" {
			o.effects[kv[i]] = effect{del: true}
		} else {
			o.effects[kv[i]] = effect{hash: h(kv[i+1])}
		}
	}
	return o
}

func state(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = h(kv[i+1])
	}
	return m
}

func TestCheckerAcceptsCorrectOutcomes(t *testing.T) {
	base := state("a", "a0", "z", "z0")
	cases := []struct {
		name  string
		final map[string]string
		ops   []*opRec
	}{
		{"acked put present", state("a", "a1", "z", "z0"), []*opRec{put("p1", 1, 2, stAcked, "a", "a1")}},
		{"unknown put absent", state("a", "a0", "z", "z0"), []*opRec{put("p1", 1, 0, stUnknown, "a", "a1")}},
		{"unknown put present", state("a", "a1", "z", "z0"), []*opRec{put("p1", 1, 0, stUnknown, "a", "a1")}},
		{"unknown batch fully applied", state("a", "a1", "b", "b1", "z", "z0"), []*opRec{put("b1", 1, 0, stUnknown, "a", "a1", "b", "b1")}},
		{"unknown batch absent", state("a", "a0", "z", "z0"), []*opRec{put("b1", 1, 0, stUnknown, "a", "a1", "b", "b1")}},
		{"concurrent acked puts either order", state("a", "x", "z", "z0"), []*opRec{put("p1", 1, 5, stAcked, "a", "x"), put("p2", 2, 6, stAcked, "a", "y")}},
		{"acked delete", state("z", "z0"), []*opRec{put("d1", 1, 2, stAcked, "a", "<del>")}},
		{"batch put then del of same key (last wins)", state("z", "z0"), nil},
		{"rejected put not visible", state("a", "a0", "z", "z0"), []*opRec{put("p1", 1, 2, stRejected, "a", "a1")}},
		{"unknown batch applied, one key overwritten later by concurrent unknown", state("a", "c1", "b", "b1", "z", "z0"),
			[]*opRec{put("b1", 1, 0, stUnknown, "a", "a1", "b", "b1"), put("c1", 2, 0, stUnknown, "a", "c1")}},
	}
	for _, c := range cases {
		if c.name == "batch put then del of same key (last wins)" {
			// "put a, del a" inside one batch: the recorded effect is the delete.
			o := put("b1", 1, 2, stAcked, "q", "q1")
			o.effects["a"] = effect{del: true}
			c.ops = []*opRec{o}
			c.final = state("q", "q1", "z", "z0")
		}
		if v := checkState(base, c.final, c.ops); v != nil {
			t.Errorf("%s: rejected a correct outcome: %v", c.name, v)
		}
	}
}

func TestCheckerRejectsBrokenOutcomes(t *testing.T) {
	base := state("a", "a0", "z", "z0")
	cases := []struct {
		name, reason string
		final        map[string]string
		ops          []*opRec
	}{
		{"lost acked put", "acknowledged", state("a", "a0", "z", "z0"), []*opRec{put("p1", 1, 2, stAcked, "a", "a1")}},
		{"lost acked delete", "acknowledged", state("a", "a0", "z", "z0"), []*opRec{put("d1", 1, 2, stAcked, "a", "<del>")}},
		{"stale value after later acked put", "acknowledged", state("a", "a1", "z", "z0"),
			[]*opRec{put("p1", 1, 2, stAcked, "a", "a1"), put("p2", 3, 4, stAcked, "a", "a2")}},
		{"partial unknown batch", "partially", state("a", "a1", "z", "z0"), []*opRec{put("b1", 1, 0, stUnknown, "a", "a1", "b", "b1")}},
		{"partial acked batch", "acknowledged", state("a", "a1", "z", "z0"), []*opRec{put("b1", 1, 2, stAcked, "a", "a1", "b", "b1")}},
		{"garbage value", "never written", state("a", "garbage", "z", "z0"), nil},
		{"value from rejected op", "REJECTED", state("a", "a1", "z", "z0"), []*opRec{put("p1", 1, 2, stRejected, "a", "a1")}},
		{"untouched key lost", "no operation", state("a", "a0"), nil},
		{"untouched key invented", "no operation", state("a", "a0", "z", "z0", "new", "x"), nil},
		{"mix of two writers' values impossible", "never written", state("a", "xy", "z", "z0"),
			[]*opRec{put("p1", 1, 5, stAcked, "a", "x"), put("p2", 2, 6, stAcked, "a", "y")}},
	}
	for _, c := range cases {
		if c.name == "garbage value" {
			c.ops = []*opRec{put("p1", 1, 0, stUnknown, "a", "a1")}
		}
		v := checkState(base, c.final, c.ops)
		if v == nil {
			t.Errorf("%s: accepted a broken outcome", c.name)
			continue
		}
		if !strings.Contains(v.Reason, c.reason) {
			t.Errorf("%s: reason %q does not mention %q", c.name, v.Reason, c.reason)
		}
	}
}

func TestCheckGets(t *testing.T) {
	base := state("a", "a0")
	g := &opRec{id: "g1", kind: "get", status: stAcked, sendT: 5, endT: 6, getKey: "a", getFound: true, getHash: h("a1")}
	if v := checkGets(base, []*opRec{put("p1", 1, 0, stUnknown, "a", "a1"), g}); v != nil {
		t.Errorf("valid get rejected: %v", v)
	}
	g.getHash = h("torn")
	if v := checkGets(base, []*opRec{put("p1", 1, 0, stUnknown, "a", "a1"), g}); v == nil {
		t.Error("torn get value accepted")
	}
}

func TestParseDump(t *testing.T) {
	m, err := parseDump(strings.NewReader("a 1\nb two words\n\"sp ace\" \"x\\ny\"\nc \n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": h("1"), "b": h("two words"), "sp ace": h("x\ny"), "c": h("")}
	if len(m) != len(want) {
		t.Fatalf("got %v", m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("key %q: %s want %s", k, m[k], v)
		}
	}
	if _, err := parseDump(strings.NewReader("a 1\na 2\n")); err == nil {
		t.Error("duplicate key accepted")
	}
}
