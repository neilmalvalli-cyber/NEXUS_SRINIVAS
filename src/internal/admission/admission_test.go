package admission

import (
	"math"
	"testing"
)

func TestFitsNeverOverflows(t *testing.T) {
	cases := []struct {
		cap, end, n uint64
		want        bool
	}{
		{0, math.MaxUint64, math.MaxUint64, true}, // unlimited
		{100, 50, 50, true},
		{100, 50, 51, false},
		{100, 100, 0, true},
		{100, 101, 0, false}, // already over the cap: checked before subtracting
		{100, 200, 1, false},
		{math.MaxUint64, 1, math.MaxUint64, false},
		{math.MaxUint64, 1, math.MaxUint64 - 1, true},
	}
	for _, c := range cases {
		if got := Fits(c.cap, c.end, c.n); got != c.want {
			t.Errorf("Fits(%d,%d,%d) = %v", c.cap, c.end, c.n, got)
		}
	}
}

func TestDecide(t *testing.T) {
	never := func() uint64 { t.Fatal("snapshot size computed although not needed"); return 0 }
	size := func(s uint64) func() uint64 { return func() uint64 { return s } }
	cases := []struct {
		name string
		r    Request
		snap func() uint64
		want Decision
	}{
		{"unlimited", Request{Cap: 0, End: 1 << 40, FrameSize: 1 << 30, PutBearing: true}, never, Append},
		{"fits", Request{Cap: 1000, End: 500, FrameSize: 500, PutBearing: true}, never, Append},
		{"put: compact fits", Request{Cap: 1000, End: 900, FrameSize: 200, PutBearing: true}, size(1000), Compact},
		{"put: snapshot too big", Request{Cap: 1000, End: 900, FrameSize: 200, PutBearing: true}, size(1001), Full},
		{"put: over cap, compact fits", Request{Cap: 1000, End: 5000, FrameSize: 40, PutBearing: true}, size(800), Compact},
		{"put: over cap, compact shrinks but not enough", Request{Cap: 1000, End: 5000, FrameSize: 40, PutBearing: true}, size(2000), Full},
		{"delete: fits", Request{Cap: 1000, End: 900, FrameSize: 50}, never, Append},
		{"delete: over cap, append not allowed, shrinks", Request{Cap: 1000, End: 5000, FrameSize: 44}, size(4999), Compact},
		{"delete: over cap, does not shrink", Request{Cap: 1000, End: 5000, FrameSize: 44}, size(5000), Full},
		{"delete: under cap, snapshot within cap", Request{Cap: 1000, End: 990, FrameSize: 44}, size(1000), Compact},
	}
	for _, c := range cases {
		if got := Decide(c.r, c.snap); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTriggers(t *testing.T) {
	if CompactWorthwhile(100, 100) || !CompactWorthwhile(100, 99) {
		t.Fatal("CompactWorthwhile")
	}
	if ShouldAutoCompact(AutoCompactMinSize-1, 0) {
		t.Fatal("fired below 4 MiB")
	}
	if !ShouldAutoCompact(AutoCompactMinSize, AutoCompactMinSize/2-1) || ShouldAutoCompact(AutoCompactMinSize, AutoCompactMinSize/2) {
		t.Fatal("2x rule")
	}
	if Decision(9).String() == "" || Full.String() != "full" {
		t.Fatal("String")
	}
}
