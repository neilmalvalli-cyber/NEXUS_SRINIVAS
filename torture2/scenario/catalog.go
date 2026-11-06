package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/oracle"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/proc"
)

// Catalog returns the 32 official scenarios in order.
func Catalog() []Scenario {
	return []Scenario{
		{"A1", "A", "Kill before any bytes of a put reach the file", "Key absent or holds its previous value",
			"crash-state replay at prefix length 0 of the put's frame; real kills of `kvdb put` at random moments", runA1},
		{"A2", "A", "Kill after some bytes of the record are written (torn write)", "Previous value; torn bytes are never returned as a value",
			"crash-state replay at every byte offset inside the put's frame", runA2},
		{"A3", "A", "Kill after the record is fully written but before fsync", "Either outcome; nothing else corrupted",
			"crash-state replay of the complete frame (a process kill keeps written bytes in the OS cache); real kills", runA3},
		{"A4", "A", "Kill after fsync returns but before put returns success", "Key present with the new value",
			"crash-state replay of the complete, synced frame; real `kvdb put` kills that landed after the write was durable", runA4},
		{"A5", "A", "Kill immediately after put returns success", "Key present with the new value, no exceptions",
			"real serve sessions killed right after each ACK; `kvdb put` exit 0 then restart", runA5},
		{"A6", "A", "Kill during a delete", "Old value or absent, never a corrupted value",
			"crash-state replay at every byte of the delete's frame; real kills of `kvdb del`", runA6},
		{"A7", "A", "Kill during a put of a 1 MiB value", "Same as A1-A5 for a value spanning many disk blocks",
			"crash-state replay at sampled offsets of a 1 MiB put frame (written through serve; every byte near the ends and 4 KiB boundaries); engine large-values (real kills)", runA7},
		{"B1", "B", "Kill after 3 of 10 batch operations are written", "None of the 10 visible",
			"crash-state replay at every offset up to 30% of a 10-op batch frame", runB1},
		{"B2", "B", "Kill after all 10 are written but the commit marker is not", "None of the 10 visible",
			"crash-state replay at every offset from 30% to one byte short of the frame (the frame checksum is the commit marker)", runB2},
		{"B3", "B", "Kill after the commit marker is written but before fsync", "All or none, never some",
			"crash-state replay of the complete batch frame; real kills of `kvdb batch`", runB3},
		{"B4", "B", "Kill after the batch returns success", "All 10 visible",
			"real serve sessions killed right after the batch ACK", runB4},
		{"B5", "B", "A batch that puts and deletes the same key", "In order, last op wins (README), also after a crash",
			"batch put X/del X, del Y/put Y, put Z twice; replay at every offset; all-or-none with the documented order", runB5},
		{"B6", "B", "A 10,000-op batch killed halfway", "None visible; recovery under 2 seconds",
			"database with many keys; crash-state replay around the middle of a 10,000-op frame; timed recovery; engine large-batch", runB6},
		{"C1", "C", "Kill during open() while it replays the log", "Next open succeeds with the same correct state",
			"real kills of `kvdb open` at random moments of its run on a large database with a torn tail", runC1},
		{"C2", "C", "Kill during open() while it truncates a torn tail", "Next open succeeds",
			"torn database restored before each kill; kills aimed at the end of recovery; truncated and untruncated states", runC2},
		{"C3", "C", "Kill during compaction, before the new file replaces the old", "Old file authoritative, all data intact",
			"crash states from the real compaction output: no temp, unsealed .compact of many lengths, partial seal, sealed .compact", runC3},
		{"C4", "C", "Kill during compaction, in the middle of the rename/swap", "Exactly one of old or new authoritative, nothing lost",
			"crash states between the two renames (path missing, .old + sealed .compact) and after the second", runC4},
		{"C5", "C", "Kill after the swap but before temp-file cleanup", "Data intact; temp files cleaned or ignored",
			"crash states with the new file in place and .old left behind; engine compaction mode (real kills)", runC5},
		{"C6", "C", "Kill during close()", "Same as a kill at any other time",
			"serve stdin closed with 20 requests still queued, process killed 0-3 ms later, oracle check; engine close-reopen mode", runC6},
		{"D1", "D", "Two writers on different keys, killed at random", "Every acknowledged put from either writer present",
			"engine concurrent-disjoint: real kills, oracle per iteration", runD1},
		{"D2", "D", "Two writers on the same key, killed at random", "Value from one writer, never a mix",
			"engine same-key: unique 1-64 KiB values identified by hash", runD2},
		{"D3", "D", "A long batch and single puts, killed at random", "Batch all-or-nothing; acknowledged puts present",
			"engine large-batch: 10,000-op batches racing single writes", runD3},
		{"D4", "D", "A reader during a batch on the same keys", "Before or after, never in between",
			"engine readers: 10-op batches over g0..g9, two readers; the oracle rejects any read order that sees half a batch", runD4},
		{"E1", "E", "Size cap reached during a single put", "Error; database readable; previous data intact",
			"kvdb put with --max-bytes until `full`; state and file compared before/after", runE1},
		{"E2", "E", "Cap reached halfway through a batch", "Error; none of its operations visible",
			"10-op batch that does not fit under the cap", runE2},
		{"E3", "E", "Cap reached, then process killed", "Restart succeeds; acknowledged data intact",
			"engine disk-full: --max-bytes 262144, real kills", runE3},
		{"E4", "E", "Cap reached during compaction", "Compaction fails or completes; no data lost; database opens",
			"compact request on a full database under the cap, plus kills during it", runE4},
		{"E5", "E", "Deletes free space, compaction runs, writes succeed again", "Usable again without intervention",
			"fill to `full`, delete half, write again under the same cap", runE5},
		{"F1", "F", "Consecutive random kills and restarts", "Invariant holds after every one",
			"engine default mode (500 kills in full mode)", runF1},
		{"F2", "F", "Kill, restart, kill during recovery, again", "Invariant holds; recovery idempotent",
			"chains: torn database, kvdb open killed twice, then open; engine recovery-crash", runF2},
		{"F3", "F", "1,000,000 keys written, killed, open()", "Recovery under 2 seconds; acknowledged keys present",
			"keys loaded through serve (acknowledged), more puts, real kill, timed open, oracle check", runF3},
		{"F4", "F", "Database file truncated by 1 byte by hand", "Open succeeds; at most the last unacknowledged record lost",
			"truncate the last byte; check --no-repair reports it without changing the file; open repairs", runF4},
	}
}

// ---- shared bodies ----

// killCLI kills the CLI command produced by next(i) at random moments, n
// times, starting from state cur. Each recovered state must be the state
// before or after that command. Returns the final state.
func (c *Ctx) killCLI(db string, n int, cur oracle.State, next func(i int) ([]string, func(oracle.State) oracle.State)) oracle.State {
	// Typical run time of one command.
	args, after := next(-1)
	t0 := time.Now()
	c.mustCLI(args...)
	typical := time.Since(t0)
	cur = after(cur)
	for i := 0; i < n; i++ {
		args, after := next(i)
		delay := time.Duration(c.rng.Float64() * 1.3 * float64(typical))
		landed, r := c.b.RunKillAfter(delay, args...)
		rc := c.recover(db, 0)
		want := after(cur)
		switch {
		case !landed && r.Exit == 0:
			c.kill(fmt.Sprintf("kill of `kvdb %s` at %.1f ms arrived after it returned OK", args[0], ms(delay)),
				outcomeOf(rc.State, want, "new value (acknowledged)"), equal(rc.State, want))
		case landed:
			got := oneOf(rc.State, []string{"old state", "new state"}, cur, want)
			c.kill(fmt.Sprintf("`kvdb %s` killed at %.1f ms (typical run %.1f ms)", args[0], ms(delay), ms(typical)),
				orBad(got, rc.State, cur), got != "")
		default:
			c.kill(fmt.Sprintf("`kvdb %s` exited %d before the kill", args[0], r.Exit), "unexpected failure: "+r.String(), false)
		}
		cur = rc.State
	}
	return cur
}

func outcomeOf(got, want oracle.State, label string) string {
	if equal(got, want) {
		return label
	}
	return "WRONG: " + diff(want, got)
}

func orBad(name string, got, ref oracle.State) string {
	if name != "" {
		return name
	}
	return "WRONG: neither the state before nor after: " + diff(ref, got)
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// singleWriteDB creates a db with 5 base keys and key k = old value; returns
// db path, its bytes and its state.
func (c *Ctx) singleWriteDB(name string) (string, []byte, oracle.State) {
	db := c.path(name)
	_, st := c.baseDB(db, 5)
	old := c.value("old", 24)
	c.mustCLI("put", db, "k", old)
	st = apply(st, "put k "+old)
	b, err := os.ReadFile(db)
	if err != nil {
		c.failf("%v", err)
	}
	return db, b, st
}

// replayWrite runs a CLI write on a copy and replays the prefixes in
// [loFrac, hiFrac]; judge decides each state.
func (c *Ctx) replayWrite(db string, base []byte, args []string, before, after oracle.State, maxStates int, loFrac, hiFrac float64, label string,
	judge func(l, lo, hi int, got oracle.State) (string, bool)) {
	pre, post := c.writeStates(base, db, args...)
	c.replayStates(db, pre, post, maxStates, loFrac, hiFrac, label, judge)
}

func (c *Ctx) replayStates(db string, pre, post []byte, maxStates int, loFrac, hiFrac float64, label string,
	judge func(l, lo, hi int, got oracle.State) (string, bool)) {
	n := len(post) - len(pre)
	lo := len(pre) + int(loFrac*float64(n))
	hi := len(pre) + int(hiFrac*float64(n))
	for _, l := range prefixLens(lo, hi, maxStates) {
		rc := c.replay(db, post, l, 0)
		out, ok := judge(l, len(pre), len(post), rc.State)
		c.kill(fmt.Sprintf("%s: %d of %d bytes on disk", label, l-len(pre), n), out, ok)
	}
}

func beforeOnly(before oracle.State) func(l, lo, hi int, got oracle.State) (string, bool) {
	return func(l, lo, hi int, got oracle.State) (string, bool) {
		if equal(got, before) {
			return "previous state (nothing of the write visible)", true
		}
		return "WRONG: " + diff(before, got), false
	}
}

func eitherState(before, after oracle.State) func(l, lo, hi int, got oracle.State) (string, bool) {
	return func(l, lo, hi int, got oracle.State) (string, bool) {
		n := oneOf(got, []string{"previous state", "new state (whole write)"}, before, after)
		return orBad(n, got, before), n != ""
	}
}

func afterOnly(after oracle.State) func(l, lo, hi int, got oracle.State) (string, bool) {
	return func(l, lo, hi int, got oracle.State) (string, bool) {
		if equal(got, after) {
			return "new state (write durable)", true
		}
		return "WRONG: " + diff(after, got), false
	}
}

// ---- group A ----

func runA1(c *Ctx) {
	db, base, before := c.singleWriteDB("a1")
	v := c.value("new", 40)
	c.replayWrite(db, base, []string{"put", db, "k", v}, before, apply(before, "put k "+v), 1, 0, 0, "put frame", beforeOnly(before))
	os.WriteFile(db, base, 0o644)
	c.killCLI(db, c.iters(15, 40), before, func(i int) ([]string, func(oracle.State) oracle.State) {
		v := c.value(fmt.Sprintf("a1-%d", i), 40)
		return []string{"put", db, "k", v}, func(s oracle.State) oracle.State { return apply(s, "put k "+v) }
	})
}

func runA2(c *Ctx) {
	db, base, before := c.singleWriteDB("a2")
	v := c.value("new", 60)
	pre, post := c.writeStates(base, db, "put", db, "k", v)
	for l := len(pre) + 1; l < len(post); l++ {
		rc := c.replay(db, post, l, 0)
		ok := equal(rc.State, before)
		out := "previous value; torn bytes ignored"
		if !ok {
			out = "WRONG: " + diff(before, rc.State)
		}
		if ok && !rc.TornTail {
			c.note("prefix %d: recovered without reporting a torn tail", l-len(pre))
		}
		c.kill(fmt.Sprintf("torn put frame: %d of %d bytes on disk", l-len(pre), len(post)-len(pre)), out, ok)
	}
}

func runA3(c *Ctx) {
	db, base, before := c.singleWriteDB("a3")
	v := c.value("new", 60)
	after := apply(before, "put k "+v)
	c.replayWrite(db, base, []string{"put", db, "k", v}, before, after, 1, 1, 1, "complete put frame, not synced", eitherState(before, after))
	os.WriteFile(db, base, 0o644)
	c.killCLI(db, c.iters(15, 40), before, func(i int) ([]string, func(oracle.State) oracle.State) {
		v := c.value(fmt.Sprintf("a3-%d", i), 60)
		return []string{"put", db, "k", v}, func(s oracle.State) oracle.State { return apply(s, "put k "+v) }
	})
}

func runA4(c *Ctx) {
	db, base, before := c.singleWriteDB("a4")
	v := c.value("new", 60)
	after := apply(before, "put k "+v)
	c.replayWrite(db, base, []string{"put", db, "k", v}, before, after, 1, 1, 1, "complete put frame after fsync", afterOnly(after))
	c.note("a process kill cannot be timed between fsync returning and put returning; the on-disk state at that instant is the complete synced frame, which is what was replayed")
	os.WriteFile(db, base, 0o644)
	c.killCLI(db, c.iters(15, 40), before, func(i int) ([]string, func(oracle.State) oracle.State) {
		v := c.value(fmt.Sprintf("a4-%d", i), 60)
		return []string{"put", db, "k", v}, func(s oracle.State) oracle.State { return apply(s, "put k "+v) }
	})
}

// ackThenKill runs n serve sessions; each sends request(i), waits for its
// ACK and kills the process immediately. The recovered state must include
// the acknowledged write.
func (c *Ctx) ackThenKill(db string, n int, cur oracle.State, request func(i int) (string, []byte, func(oracle.State) oracle.State)) oracle.State {
	for i := 0; i < n; i++ {
		t0 := time.Now()
		clock := func() int64 { return time.Since(t0).Nanoseconds() }
		s, err := c.b.StartServe(db, nil, clock)
		if err != nil {
			c.failf("%v", err)
		}
		id, payload, after := request(i)
		ch, err := s.Send(id, payload)
		if err != nil {
			c.failf("send: %v", err)
		}
		var rp struct {
			ok   bool
			kind string
		}
		select {
		case r, ok := <-ch:
			rp.ok, rp.kind = ok, r.Kind
		case <-time.After(time.Minute):
			s.Kill()
			c.failf("no reply")
		}
		landed := s.Kill()
		s.Wait(time.Minute)
		s.Release()
		if !rp.ok || rp.kind != "ACK" {
			c.failf("expected ACK, got %+v (stderr %s)", rp, s.Stderr())
		}
		want := after(cur)
		rc := c.recover(db, 0)
		c.kill(fmt.Sprintf("serve killed right after ACK %d (kill landed: %v)", i, landed), outcomeOf(rc.State, want, "acknowledged write present"), equal(rc.State, want))
		cur = rc.State
	}
	return cur
}

func runA5(c *Ctx) {
	db, _, before := c.singleWriteDB("a5")
	cur := c.ackThenKill(db, c.iters(20, 60), before, func(i int) (string, []byte, func(oracle.State) oracle.State) {
		v := c.value(fmt.Sprintf("a5-%d", i), 80)
		id := fmt.Sprintf("p%d", i)
		return id, []byte(fmt.Sprintf("w1 %s put k %s\n", id, v)), func(s oracle.State) oracle.State { return apply(s, "put k "+v) }
	})
	// The CLI form: `kvdb put` returned OK, then the process is gone.
	for i := 0; i < 5; i++ {
		v := c.value(fmt.Sprintf("a5cli-%d", i), 80)
		c.mustCLI("put", db, "k", v)
		cur = apply(cur, "put k "+v)
		rc := c.recover(db, 0)
		c.kill(fmt.Sprintf("`kvdb put` returned OK (%d)", i), outcomeOf(rc.State, cur, "present"), equal(rc.State, cur))
	}
}

func runA6(c *Ctx) {
	db, base, before := c.singleWriteDB("a6")
	after := apply(before, "del k")
	c.replayWrite(db, base, []string{"del", db, "k"}, before, after, 400, 0, 1, "delete frame", func(l, lo, hi int, got oracle.State) (string, bool) {
		if l < hi {
			return beforeOnly(before)(l, lo, hi, got)
		}
		return eitherState(before, after)(l, lo, hi, got)
	})
	os.WriteFile(db, base, 0o644)
	cur := before
	c.killCLI(db, c.iters(15, 40), cur, func(i int) ([]string, func(oracle.State) oracle.State) {
		if i%2 == 0 || i < 0 {
			v := c.value(fmt.Sprintf("a6-%d", i), 30)
			return []string{"put", db, "k", v}, func(s oracle.State) oracle.State { return apply(s, "put k "+v) }
		}
		return []string{"del", db, "k"}, func(s oracle.State) oracle.State { return apply(s, "del k") }
	})
}

func runA7(c *Ctx) {
	db, base, before := c.singleWriteDB("a7")
	v := c.value("big", 1<<20)
	after := apply(before, "put k "+v)
	// 1 MiB does not fit on a Windows command line (32,767 characters), so
	// the put goes through serve; it appends the same single-put frame.
	pre, post := c.writeStatesServe(base, db, "big", []byte("w1 big put k "+v+"\n"))
	c.note("1 MiB put frame: %d bytes", len(post)-len(pre))
	c.replayStates(db, pre, post, c.iters(80, 200), 0, 1, "1 MiB put frame", func(l, lo, hi int, got oracle.State) (string, bool) {
		if l < hi {
			return beforeOnly(before)(l, lo, hi, got)
		}
		return eitherState(before, after)(l, lo, hi, got)
	})
	c.runEngine("large-values", c.iters(10, 100), c.opt.Seed+7)
}

// ---- group B ----

func (c *Ctx) batchDB(name string) (string, []byte, oracle.State, []string) {
	db := c.path(name)
	_, st := c.baseDB(db, 8)
	b, _ := os.ReadFile(db)
	var lines []string
	for i := 0; i < 10; i++ {
		if i%4 == 3 {
			lines = append(lines, fmt.Sprintf("del base%03d", i))
		} else {
			lines = append(lines, fmt.Sprintf("put bk%02d %s", i, c.value(fmt.Sprintf("b%d", i), 30)))
		}
	}
	return db, b, st, lines
}

func runB1(c *Ctx) {
	db, base, before, lines := c.batchDB("b1")
	c.replayWrite(db, base, []string{"batch", db, c.batchFile(db, lines)}, before, apply(before, lines...), 200, 0, 0.3, "10-op batch frame (first 30%)", beforeOnly(before))
}

func runB2(c *Ctx) {
	db, base, before, lines := c.batchDB("b2")
	pre, post := c.writeStates(base, db, "batch", db, c.batchFile(db, lines))
	n := len(post) - len(pre)
	for _, l := range prefixLens(len(pre)+int(0.3*float64(n)), len(post)-1, 300) {
		rc := c.replay(db, post, l, 0)
		ok := equal(rc.State, before)
		out := "none of the 10 visible"
		if !ok {
			out = "WRONG: " + diff(before, rc.State)
		}
		c.kill(fmt.Sprintf("10-op batch frame: %d of %d bytes on disk (commit marker incomplete)", l-len(pre), n), out, ok)
	}
}

func runB3(c *Ctx) {
	db, base, before, lines := c.batchDB("b3")
	after := apply(before, lines...)
	c.replayWrite(db, base, []string{"batch", db, c.batchFile(db, lines)}, before, after, 1, 1, 1, "complete batch frame, not synced", eitherState(before, after))
	os.WriteFile(db, base, 0o644)
	c.killCLI(db, c.iters(15, 40), before, func(i int) ([]string, func(oracle.State) oracle.State) {
		var ls []string
		for j := 0; j < 10; j++ {
			ls = append(ls, fmt.Sprintf("put bk%02d %s", j, c.value(fmt.Sprintf("b3-%d-%d", i, j), 30)))
		}
		return []string{"batch", db, c.batchFile(db, ls)}, func(s oracle.State) oracle.State { return apply(s, ls...) }
	})
}

func runB4(c *Ctx) {
	db, _, before, _ := c.batchDB("b4")
	c.ackThenKill(db, c.iters(15, 40), before, func(i int) (string, []byte, func(oracle.State) oracle.State) {
		var ls []string
		for j := 0; j < 10; j++ {
			ls = append(ls, fmt.Sprintf("put bk%02d %s", j, c.value(fmt.Sprintf("b4-%d-%d", i, j), 30)))
		}
		id := fmt.Sprintf("b%d", i)
		payload := fmt.Sprintf("w1 %s batch 10\n%s\n", id, strings.Join(ls, "\n"))
		return id, []byte(payload), func(s oracle.State) oracle.State { return apply(s, ls...) }
	})
}

func runB5(c *Ctx) {
	db := c.path("b5")
	_, before := c.baseDB(db, 3)
	y0 := c.value("y0", 20)
	c.mustCLI("put", db, "Y", y0)
	before = apply(before, "put Y "+y0)
	base, _ := os.ReadFile(db)
	lines := []string{
		"put X " + c.value("x1", 20), "del X",
		"del Y", "put Y " + c.value("y2", 20),
		"put Z " + c.value("z1", 20), "put Z " + c.value("z2", 20),
	}
	after := apply(before, lines...)
	if _, ok := after["X"]; ok {
		c.failf("test bug: X should be absent")
	}
	pre, post := c.writeStates(base, db, "batch", db, c.batchFile(db, lines))
	for _, l := range prefixLens(len(pre), len(post), 400) {
		rc := c.replay(db, post, l, 0)
		var out string
		var ok bool
		if l < len(post) {
			out, ok = beforeOnly(before)(l, len(pre), len(post), rc.State)
		} else {
			ok = equal(rc.State, after)
			out = "X absent, Y = new value, Z = second value (in order, last op wins)"
			if !ok {
				out = "WRONG: " + diff(after, rc.State)
			}
		}
		c.kill(fmt.Sprintf("put/del-same-key batch: %d of %d bytes on disk", l-len(pre), len(post)-len(pre)), out, ok)
	}
	c.note("documented order (README § Batch semantics): operations apply in order and the last operation on a key wins")
}

func runB6(c *Ctx) {
	n := c.iters(200_000, 1_000_000)
	db := c.path("b6")
	before := c.load(db, n, 0)
	base, _ := os.ReadFile(db)
	var lines []string
	for i := 0; i < 10000; i++ {
		if i%5 == 0 {
			lines = append(lines, fmt.Sprintf("put p%07d %s", i*7, c.value(fmt.Sprintf("o%d", i), 16)))
		} else {
			lines = append(lines, fmt.Sprintf("put n%05d %s", i, c.value(fmt.Sprintf("b6-%d", i), 16)))
		}
	}
	pre, post := c.writeStates(base, db, "batch", db, c.batchFile(db, lines))
	half := len(pre) + (len(post)-len(pre))/2
	worst := 0.0
	for _, l := range []int{half - 1, half, half + 1, len(pre) + 1, len(post) - 1} {
		rc := c.replay(db, post, l, 0)
		ok := equal(rc.State, before) && rc.RecoveryMs < 2000
		if rc.RecoveryMs > worst {
			worst = rc.RecoveryMs
		}
		out := fmt.Sprintf("0 of 10,000 visible; %d keys intact; recovery %.0f ms (open wall %.0f ms)", len(before), rc.RecoveryMs, rc.RestartMs)
		if !equal(rc.State, before) {
			out = "WRONG: " + diff(before, rc.State)
		} else if rc.RecoveryMs >= 2000 {
			out = fmt.Sprintf("recovery took %.0f ms (limit 2000)", rc.RecoveryMs)
		}
		c.kill(fmt.Sprintf("10,000-op frame on a %d-key database: %d of %d bytes on disk", n, l-len(pre), len(post)-len(pre)), out, ok)
	}
	c.note("database %d keys, %d bytes; 10,000-op frame %d bytes; worst recovery %.0f ms", n, len(pre), len(post)-len(pre), worst)
	c.runEngine("large-batch", c.iters(10, 100), c.opt.Seed+13)
}

// ---- group C ----

// tornDB loads n keys and leaves a torn tail (half a put frame). Returns
// db, the torn bytes and the expected state.
func (c *Ctx) tornDB(name string, n int) (string, []byte, oracle.State) {
	db := c.path(name)
	st := c.load(db, n, 0)
	base, _ := os.ReadFile(db)
	_, post := c.writeStates(base, db, "put", db, "torn-key", c.value("torn", 200))
	torn := post[:len(base)+(len(post)-len(base))/2]
	os.WriteFile(db, torn, 0o644)
	return db, torn, st
}

func (c *Ctx) openTime(db string) time.Duration {
	best := time.Duration(0)
	for i := 0; i < 3; i++ {
		r := c.mustCLI("open", db)
		if r.Dur > best {
			best = r.Dur
		}
	}
	return best
}

func runC1(c *Ctx) {
	n := c.iters(200_000, 1_000_000)
	db, torn, want := c.tornDB("c1", n)
	// Time open on a clean copy, then restore the torn file.
	typical := c.openTime(db)
	os.WriteFile(db, torn, 0o644)
	landedN := 0
	for i := 0; i < c.iters(20, 50); i++ {
		delay := time.Duration(c.rng.Float64() * 1.2 * float64(typical))
		landed, _ := c.b.RunKillAfter(delay, "open", db)
		if landed {
			landedN++
		}
		r := c.cli("check", db, "--no-repair")
		ok := r.Exit == 0 || r.Exit == 4
		c.kill(fmt.Sprintf("kvdb open killed at %.0f ms of ~%.0f ms (landed: %v)", ms(delay), ms(typical), landed),
			"read-only check after the kill: "+firstLine(r.Stdout), ok)
	}
	rc := c.recover(db, 0)
	c.kill("final open after all kills", outcomeOf(rc.State, want, fmt.Sprintf("same correct state (%d keys); torn tail discarded", len(want))), equal(rc.State, want))
	c.note("%d of %d kills landed while kvdb open was running", landedN, c.iters(20, 50))
}

func runC2(c *Ctx) {
	n := c.iters(200_000, 1_000_000)
	db, torn, want := c.tornDB("c2", n)
	typical := c.openTime(db) // also truncates
	clean, _ := os.ReadFile(db)
	// Both sides of the truncation step recover to the same state.
	for _, st := range []struct {
		name string
		b    []byte
	}{{"torn (truncation not started)", torn}, {"truncated (truncation done)", clean}} {
		os.WriteFile(db, st.b, 0o644)
		rc := c.recover(db, 0)
		c.kill("state: "+st.name, outcomeOf(rc.State, want, "next open succeeds, same state"), equal(rc.State, want))
	}
	landedN := 0
	for i := 0; i < c.iters(20, 50); i++ {
		os.WriteFile(db, torn, 0o644)
		delay := time.Duration((0.7 + 0.6*c.rng.Float64()) * float64(typical))
		landed, _ := c.b.RunKillAfter(delay, "open", db)
		if landed {
			landedN++
		}
		rc := c.recover(db, 0)
		c.kill(fmt.Sprintf("kvdb open on the torn file killed at %.0f ms of ~%.0f ms (near truncation; landed: %v)", ms(delay), ms(typical), landed),
			outcomeOf(rc.State, want, "next open succeeds, same state"), equal(rc.State, want))
	}
	c.note("%d of %d kills landed while kvdb open was running", landedN, c.iters(20, 50))
}

// compactionFixture builds a database with garbage and returns its bytes O,
// the real compaction output C and the state S.
func (c *Ctx) compactionFixture(name string) (string, []byte, []byte, oracle.State) {
	db := c.path(name)
	c.mustCLI("open", db)
	st := oracle.State{}
	for round := 0; round < 4; round++ {
		var lines []string
		for i := 0; i < 60; i++ {
			lines = append(lines, fmt.Sprintf("put c%03d %s", (i*7+round)%90, c.value(fmt.Sprintf("r%d-%d", round, i), 40)))
		}
		for i := 0; i < 10; i++ {
			lines = append(lines, fmt.Sprintf("del c%03d", (i*13+round)%90))
		}
		c.mustCLI("batch", db, c.batchFile(db, lines))
		st = apply(st, lines...)
	}
	orig, _ := os.ReadFile(db)
	// The real compaction, on a copy.
	cdb := filepath.Join(filepath.Dir(db), "compacted.kv")
	os.WriteFile(cdb, orig, 0o644)
	t0 := time.Now()
	s, err := c.b.StartServe(cdb, nil, func() int64 { return time.Since(t0).Nanoseconds() })
	if err != nil {
		c.failf("%v", err)
	}
	ch, _ := s.Send("cmp", []byte("w0 cmp compact\n"))
	rp, ok := <-ch
	s.CloseInput()
	s.Wait(time.Minute)
	if !ok || rp.Kind != "ACK" {
		c.failf("compaction: %+v %s", rp, s.Stderr())
	}
	comp, _ := os.ReadFile(cdb)
	if len(comp) >= len(orig) {
		c.failf("compaction did not shrink the file (%d -> %d)", len(orig), len(comp))
	}
	rc := c.recover(cdb, 0)
	if !equal(rc.State, st) {
		c.failf("compacted file has the wrong state: %s", diff(st, rc.State))
	}
	c.note("fixture: %d keys, %d-byte log compacted to %d bytes by the real kvdb", len(st), len(orig), len(comp))
	return db, orig, comp, st
}

const superblock = 32 // documented in docs/FILE_FORMAT.md

type layout struct {
	name                 string
	path, compact, older []byte // nil = file absent
}

func (c *Ctx) checkLayouts(db string, want oracle.State, ls []layout) {
	for _, l := range ls {
		for _, f := range []struct {
			p string
			b []byte
		}{{db, l.path}, {db + ".compact", l.compact}, {db + ".old", l.older}} {
			os.Remove(f.p)
			if f.b != nil {
				os.WriteFile(f.p, f.b, 0o644)
			}
		}
		os.Remove(db + ".new")
		rc := c.recover(db, 0)
		ok := equal(rc.State, want)
		out := fmt.Sprintf("all %d keys intact", len(want))
		if len(rc.StaleAfter) > 0 {
			out += "; temp files left (ignored): " + strings.Join(rc.StaleAfter, ",")
		} else {
			out += "; temp files cleaned up"
		}
		if rc.Authority {
			out += "; interrupted compaction resolved"
		}
		if !ok {
			out = "WRONG: " + diff(want, rc.State)
		}
		c.kill(l.name, out, ok)
	}
}

func unsealed(comp []byte, n int) []byte {
	b := make([]byte, n)
	copy(b, comp[:n])
	for i := 0; i < superblock && i < n; i++ {
		b[i] = 0 // frames written first; the superblock region is still zero
	}
	return b
}

func runC3(c *Ctx) {
	db, orig, comp, want := c.compactionFixture("c3")
	ls := []layout{{name: "before compaction starts (old file only)", path: orig}}
	ls = append(ls, layout{name: ".compact just created (0 bytes)", path: orig, compact: []byte{}})
	for _, n := range prefixLens(superblock, len(comp), c.iters(25, 80)) {
		ls = append(ls, layout{name: fmt.Sprintf("unsealed .compact, %d of %d bytes written", n, len(comp)), path: orig, compact: unsealed(comp, n)})
	}
	for _, k := range []int{1, 8, 16, 27, 28, 31} {
		b := unsealed(comp, len(comp))
		copy(b, comp[:k])
		ls = append(ls, layout{name: fmt.Sprintf("seal (superblock) torn after %d of 32 bytes", k), path: orig, compact: b})
	}
	ls = append(ls, layout{name: "sealed .compact, before the first rename", path: orig, compact: comp})
	c.checkLayouts(db, want, ls)
}

func runC4(c *Ctx) {
	db, orig, comp, want := c.compactionFixture("c4")
	c.checkLayouts(db, want, []layout{
		{name: "between rename 1 and 2: path missing, .old = old file, .compact = sealed new file", older: orig, compact: comp},
		{name: "between rename 1 and 2, .compact unsealed (impossible in NEXUS's order, must still be safe)", older: orig, compact: unsealed(comp, len(comp))},
		{name: "after rename 2: path = new file, .old = old file", path: comp, older: orig},
		{name: "rename 1 not started: path = old, sealed .compact", path: orig, compact: comp},
	})
}

func runC5(c *Ctx) {
	db, orig, comp, want := c.compactionFixture("c5")
	c.checkLayouts(db, want, []layout{
		{name: "after the swap, before deleting .old", path: comp, older: orig},
		{name: "after the swap, .old half-deleted (truncated)", path: comp, older: orig[:len(orig)/2]},
		{name: "after the swap, stale empty .compact", path: comp, compact: []byte{}},
		{name: "cleanup finished", path: comp},
	})
	c.runEngine("compaction", c.iters(10, 100), c.opt.Seed+5)
}

func runC6(c *Ctx) {
	db, _, cur := c.singleWriteDB("c6")
	landedN, rounds := 0, c.iters(20, 60)
	for i := 0; i < rounds; i++ {
		t0 := time.Now()
		clock := func() int64 { return time.Since(t0).Nanoseconds() }
		h := oracle.NewHistory(clock)
		s, err := c.b.StartServe(db, nil, clock)
		if err != nil {
			c.failf("%v", err)
		}
		base := cur
		// 5 acknowledged puts, then 20 more queued without waiting, so that
		// close() has work to drain when stdin closes.
		type pending struct {
			op *oracle.Op
			ch <-chan proc.Reply
		}
		var queued []pending
		for j := 0; j < 25; j++ {
			v := c.value(fmt.Sprintf("c6-%d-%d", i, j), 50)
			k := fmt.Sprintf("k%d", j%7)
			op := &oracle.Op{ID: fmt.Sprintf("c%d", j), Writer: fmt.Sprintf("w%d", j), Kind: oracle.KPut,
				Muts: []oracle.Mut{{Key: k, Val: oracle.HashValue([]byte(v))}}}
			if err := h.Begin(op); err != nil {
				c.failf("%v", err)
			}
			ch, err := s.Send(op.ID, []byte(fmt.Sprintf("w1 %s put %s %s\n", op.ID, k, v)))
			if err != nil {
				c.failf("send: %v", err)
			}
			if j < 5 {
				if rp, ok := <-ch; !ok || rp.Kind != "ACK" {
					c.failf("put %d: %+v", j, rp)
				}
				h.Finish(op, oracle.Acked, "")
				continue
			}
			queued = append(queued, pending{op, ch})
		}
		s.CloseInput() // close() begins with 20 requests still queued
		delay := time.Duration(c.rng.Intn(3000)) * time.Microsecond
		time.Sleep(delay)
		landed := s.Kill()
		if landed {
			landedN++
		}
		s.Wait(time.Minute)
		s.Release()
		acked := 5
		for _, p := range queued {
			select {
			case rp, ok := <-p.ch:
				if ok && rp.Kind == "ACK" {
					h.Finish(p.op, oracle.Acked, "")
					acked++
				}
			default:
			}
		}
		h.Crash()
		rc := c.recover(db, 0)
		v := oracle.Check(base, rc.State, h.Ops(), oracle.DefaultLimits())
		out := fmt.Sprintf("oracle %s: %d of 25 acknowledged before the kill, all present", v.Result, acked)
		if v.Violation != nil {
			out = "oracle FAIL: " + v.Violation.String()
		}
		c.kill(fmt.Sprintf("killed %.1f ms after close began, 20 requests queued (landed before exit: %v)", ms(delay), landed), out, v.Result == oracle.Pass)
		cur = rc.State
	}
	c.note("%d of %d kills landed before serve finished closing", landedN, rounds)
	if landedN == 0 {
		c.inconclusive("no kill landed during close()")
	}
	c.runEngine("close-reopen", c.iters(10, 100), c.opt.Seed+6)
}

// ---- group D ----

func runD1(c *Ctx) { c.runEngine("concurrent-disjoint", c.iters(20, 100), c.opt.Seed+21) }
func runD2(c *Ctx) { c.runEngine("same-key", c.iters(20, 100), c.opt.Seed+22) }
func runD3(c *Ctx) { c.runEngine("large-batch", c.iters(10, 100), c.opt.Seed+23) }
func runD4(c *Ctx) {
	sum := c.runEngine("readers", c.iters(15, 100), c.opt.Seed+24)
	if sum.Gets == 0 {
		c.failf("no reads were verified")
	}
	c.note("%d reads verified against the batches by the oracle", sum.Gets)
}

// ---- group E ----

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimSpace(s)
}

// fillToFull puts 200-byte values under cap until a put fails with "full".
func (c *Ctx) fillToFull(db string, cap int64) oracle.State {
	mb := fmt.Sprint(cap)
	c.mustCLI("open", db, "--max-bytes", mb)
	st := oracle.State{}
	for i := 0; i < 10000; i++ {
		v := c.value(fmt.Sprintf("e%d", i), 200)
		k := fmt.Sprintf("e%04d", i)
		before, _ := os.ReadFile(db)
		r := c.cli("put", db, k, v, "--max-bytes", mb)
		if r.Exit == 0 {
			st = apply(st, "put "+k+" "+v)
			continue
		}
		if !strings.Contains(r.Stdout+r.Stderr, "full") {
			c.failf("put failed without `full`: %s", r.String())
		}
		after, _ := os.ReadFile(db)
		ok := string(before) == string(after)
		c.kill(fmt.Sprintf("put #%d hit the %d-byte cap", i, cap), map[bool]string{true: "error `full`, file unchanged", false: "WRONG: file changed by a failed put"}[ok], ok)
		return st
	}
	c.failf("never reached the cap")
	return nil
}

func runE1(c *Ctx) {
	db := c.path("e1")
	cap := int64(8192)
	st := c.fillToFull(db, cap)
	rc := c.recover(db, cap)
	c.kill("after the failed put: restart", outcomeOf(rc.State, st, fmt.Sprintf("database readable, %d keys intact, file %d <= %d bytes", len(st), rc.FileBytes, cap)), equal(rc.State, st) && rc.FileBytes <= cap)
	for k := range st {
		r := c.mustCLI("get", db, k)
		if oracle.HashValue([]byte(strings.TrimRight(r.Stdout, "\r\n"))) != st[k] {
			c.failf("get %s returned a different value", k)
		}
		break
	}
}

func runE2(c *Ctx) {
	db := c.path("e2")
	cap := int64(8192)
	st := c.fillToFull(db, cap)
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, fmt.Sprintf("put eb%d %s", i, c.value(fmt.Sprintf("eb%d", i), 300)))
	}
	before, _ := os.ReadFile(db)
	r := c.cli("batch", db, c.batchFile(db, lines), "--max-bytes", fmt.Sprint(cap))
	after, _ := os.ReadFile(db)
	ok := r.Exit != 0 && strings.Contains(r.Stdout+r.Stderr, "full") && string(before) == string(after)
	c.kill("10-op batch over the cap", fmt.Sprintf("exit %d, %s; file unchanged: %v", r.Exit, firstLine(r.Stdout+r.Stderr), string(before) == string(after)), ok)
	rc := c.recover(db, cap)
	c.kill("restart after the failed batch", outcomeOf(rc.State, st, "none of the 10 visible, previous data intact"), equal(rc.State, st))
}

func runE3(c *Ctx) {
	sum := c.runEngine("disk-full", c.iters(15, 100), c.opt.Seed+31)
	if sum.FullErrors == 0 {
		c.failf("the cap was never reached")
	}
	c.note("%d writes were refused with `full` and verified absent", sum.FullErrors)
}

func runE4(c *Ctx) {
	db := c.path("e4")
	cap := int64(16384)
	mb := fmt.Sprint(cap)
	c.mustCLI("open", db, "--max-bytes", mb)
	st := oracle.State{}
	// Fill to the cap with live data plus some garbage (every third put
	// overwrites a hot key), until a put is refused with `full`.
	full := false
	puts := 0
	for i := 0; i < 2000 && !full; i++ {
		k := fmt.Sprintf("e%d", i)
		if i%3 == 0 {
			k = fmt.Sprintf("hot%d", i%2)
		}
		v := c.value(fmt.Sprintf("e4-%d", i), 300)
		r := c.cli("put", db, k, v, "--max-bytes", mb)
		switch {
		case r.Exit == 0:
			st = apply(st, "put "+k+" "+v)
			puts++
		case strings.Contains(r.Stdout+r.Stderr, "full"):
			full = true
		default:
			c.failf("put failed without `full`: %s", r.String())
		}
	}
	if !full {
		c.failf("the %d-byte cap was never reached", cap)
	}
	c.note("cap reached after %d accepted puts (%d live keys)", puts, len(st))
	for i := 0; i < c.iters(10, 30); i++ {
		t0 := time.Now()
		s, err := c.b.StartServe(db, []string{"--max-bytes", mb}, func() int64 { return time.Since(t0).Nanoseconds() })
		if err != nil {
			c.failf("%v", err)
		}
		s.WaitReady(time.Minute)
		ch, _ := s.Send("cmp", []byte("w0 cmp compact\n"))
		kill := i%2 == 1
		reply := ""
		if kill {
			d := time.Duration(c.rng.Intn(4000)) * time.Microsecond
			time.Sleep(d)
			landed := s.Kill()
			reply = fmt.Sprintf("killed %.1f ms after the compact request, landed %v", ms(d), landed)
		} else {
			rp, ok := <-ch
			reply = fmt.Sprintf("reply %s %s", rp.Kind, rp.Class)
			if !ok {
				reply = "no reply"
			}
			s.CloseInput()
		}
		s.Wait(time.Minute)
		s.Release()
		rc := c.recover(db, cap)
		c.kill(fmt.Sprintf("compaction under a %d-byte cap (%s)", cap, reply), outcomeOf(rc.State, st, "no data lost, database opens"), equal(rc.State, st))
		// Make garbage again for the next round.
		k := fmt.Sprintf("e%d", i%6)
		v := c.value(fmt.Sprintf("e4r-%d", i), 300)
		if r := c.cli("put", db, k, v, "--max-bytes", mb); r.Exit == 0 {
			st = apply(st, "put "+k+" "+v)
		}
	}
}

func runE5(c *Ctx) {
	db := c.path("e5")
	cap := int64(8192)
	mb := fmt.Sprint(cap)
	st := c.fillToFull(db, cap)
	keys := make([]string, 0, len(st))
	for k := range st {
		keys = append(keys, k)
	}
	for i, k := range keys {
		if i%2 == 0 {
			c.mustCLI("del", db, k, "--max-bytes", mb)
			st = apply(st, "del "+k)
		}
	}
	wrote := 0
	for i := 0; i < len(keys)/2; i++ {
		k := fmt.Sprintf("again%d", i)
		v := c.value(k, 200)
		r := c.cli("put", db, k, v, "--max-bytes", mb)
		if r.Exit != 0 {
			break
		}
		st = apply(st, "put "+k+" "+v)
		wrote++
	}
	rc := c.recover(db, cap)
	ok := wrote > 0 && equal(rc.State, st) && rc.FileBytes <= cap
	c.kill(fmt.Sprintf("after deleting half the keys: %d new puts accepted under the same cap", wrote), outcomeOf(rc.State, st, fmt.Sprintf("usable again; file %d <= %d", rc.FileBytes, cap)), ok)
}

// ---- group F ----

func runF1(c *Ctx) { c.runEngine("default", c.iters(50, 500), c.opt.Seed+1) }

func runF2(c *Ctx) {
	n := c.iters(100_000, 200_000)
	db, torn, want := c.tornDB("f2", n)
	typical := c.openTime(db)
	for i := 0; i < c.iters(10, 30); i++ {
		os.WriteFile(db, torn, 0o644)
		var notes []string
		for k := 0; k < 2; k++ {
			delay := time.Duration(c.rng.Float64() * 1.2 * float64(typical))
			landed, _ := c.b.RunKillAfter(delay, "open", db)
			notes = append(notes, fmt.Sprintf("%.0fms(landed %v)", ms(delay), landed))
		}
		rc := c.recover(db, 0)
		c.kill("kill, restart killed at "+strings.Join(notes, ", then "), outcomeOf(rc.State, want, "same state; recovery idempotent"), equal(rc.State, want))
	}
	c.runEngine("recovery-crash", c.iters(5, 30), c.opt.Seed+2)
}

func runF3(c *Ctx) {
	n := c.iters(200_000, 1_000_000)
	db := c.path("f3")
	base := c.load(db, n, 0)
	// More puts through serve; kill at a random moment.
	t0 := time.Now()
	clock := func() int64 { return time.Since(t0).Nanoseconds() }
	h := oracle.NewHistory(clock)
	s, err := c.b.StartServe(db, nil, clock)
	if err != nil {
		c.failf("%v", err)
	}
	s.WaitReady(time.Minute)
	stop := time.After(time.Duration(200+c.rng.Intn(400)) * time.Millisecond)
	acked := 0
loop:
	for i := 0; ; i++ {
		k := fmt.Sprintf("f3-%d", i%500)
		v := c.value(fmt.Sprintf("f3-%d", i), 40)
		op := &oracle.Op{ID: fmt.Sprintf("f%d", i), Writer: "w1", Kind: oracle.KPut, Muts: []oracle.Mut{{Key: k, Val: oracle.HashValue([]byte(v))}}}
		h.Begin(op)
		ch, err := s.Send(op.ID, []byte(fmt.Sprintf("w1 %s put %s %s\n", op.ID, k, v)))
		if err != nil {
			break
		}
		select {
		case <-stop:
			break loop
		case rp, ok := <-ch:
			if !ok {
				break loop
			}
			if rp.Kind == "ACK" {
				h.Finish(op, oracle.Acked, "")
				acked++
			} else {
				c.failf("put: %+v", rp)
			}
		}
	}
	s.Kill()
	s.Wait(time.Minute)
	s.Release()
	h.Crash()
	rc := c.recover(db, 0)
	v := oracle.Check(base, rc.State, h.Ops(), oracle.DefaultLimits())
	ok := v.Result == oracle.Pass && rc.RecoveryMs < 2000
	out := fmt.Sprintf("oracle %s: %d loaded + %d acknowledged puts present; recovery %.0f ms (open wall %.0f ms)", v.Result, n, acked, rc.RecoveryMs, rc.RestartMs)
	if v.Violation != nil {
		out += ": " + v.Violation.String()
	}
	c.kill(fmt.Sprintf("serve killed on a %d-key database after %d acknowledged puts", n, acked), out, ok)
}

func runF4(c *Ctx) {
	db, _, st := c.singleWriteDB("f4")
	v := c.value("last", 50)
	c.mustCLI("put", db, "last", v)
	full := apply(st, "put last "+v)
	b, _ := os.ReadFile(db)
	os.WriteFile(db, b[:len(b)-1], 0o644) // the hand truncation
	r := c.cli("check", db, "--no-repair")
	after, _ := os.ReadFile(db)
	ok := r.Exit == 4 && len(after) == len(b)-1
	c.kill("file truncated by 1 byte: kvdb check --no-repair", fmt.Sprintf("exit %d: %s; file unchanged: %v", r.Exit, firstLine(r.Stdout), len(after) == len(b)-1), ok)
	rc := c.recover(db, 0)
	got := oneOf(rc.State, []string{"only the last (torn) record lost", "nothing lost"}, st, full)
	c.kill("open after the truncation", orBad(got, rc.State, st), got != "")
}
