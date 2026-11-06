// Package scenario runs the 32 official crash scenarios (groups A-F) against
// the real kvdb executable. Every scenario has a stable ID, a description,
// an explicit invariant, the kill points it exercised and their outcomes,
// and a machine-readable result.
//
// Two kinds of crash are used, both judged after a restart through the real
// CLI (check --no-repair, open, check, dump):
//
//   - Real kills: the real process is terminated (TerminateProcess on
//     Windows) at a random moment, many times.
//   - Crash-state replay: a kill can only leave on disk what the process had
//     already handed to the OS, and NEXUS appends each write in order. So the
//     possible on-disk states after a kill inside a write are exactly the
//     prefixes of the bytes that write appends. The engine has the real
//     binary perform the write on a copy, then recreates the file at each
//     prefix length (every byte for small writes, a dense sample for large
//     ones) and restarts the real binary on it. For compaction the states are
//     built the same way from the real compaction output and the documented
//     temp-file protocol (<db>.compact, <db>.old; superblock written last).
package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/engine"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/oracle"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/proc"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/workload"
)

// Scenario is one official scenario.
type Scenario struct {
	ID        string
	Group     string
	Title     string
	Invariant string
	Method    string
	run       func(c *Ctx)
}

// KillPoint is one crash the scenario exercised.
type KillPoint struct {
	Point   string `json:"point"`
	Outcome string `json:"outcome"`
	OK      bool   `json:"ok"`
}

// Result is one scenario's machine-readable result.
type Result struct {
	ID         string      `json:"id"`
	Group      string      `json:"group"`
	Title      string      `json:"title"`
	Invariant  string      `json:"invariant"`
	Method     string      `json:"method"`
	Status     string      `json:"status"` // PASS, FAIL, INCONCLUSIVE, UNSUPPORTED
	KillPoints []KillPoint `json:"kill_points"`
	NumKills   int         `json:"kill_points_total"`
	Notes      []string    `json:"notes,omitempty"`
	Failure    string      `json:"failure,omitempty"`
	Seconds    float64     `json:"seconds"`
}

// Options configure a scenario run.
type Options struct {
	Kvdb  proc.Binary
	Dir   string
	Group string
	Only  []string
	Full  bool
	Seed  int64
	Out   io.Writer
}

// Report is scenarios.json.
type Report struct {
	Tool    string   `json:"tool"`
	Kvdb    string   `json:"kvdb"`
	Full    bool     `json:"full"`
	Seed    int64    `json:"seed"`
	Started string   `json:"started"`
	Results []Result `json:"results"`
	Passed  int      `json:"passed"`
	Failed  int      `json:"failed"`
	Other   int      `json:"inconclusive_or_unsupported"`
	Result  string   `json:"result"`
	Seconds float64  `json:"seconds"`
}

// Ctx is the running scenario's context.
type Ctx struct {
	opt    Options
	b      proc.Binary
	dir    string
	res    *Result
	n      int
	rng    *rand.Rand
	failed bool
}

type abort struct{}

// failf records a failure and stops the scenario.
func (c *Ctx) failf(format string, a ...any) {
	c.res.Status = "FAIL"
	if c.res.Failure == "" {
		c.res.Failure = fmt.Sprintf(format, a...)
	}
	panic(abort{})
}

func (c *Ctx) inconclusive(format string, a ...any) {
	if c.res.Status != "FAIL" {
		c.res.Status = "INCONCLUSIVE"
		c.res.Failure = fmt.Sprintf(format, a...)
	}
	panic(abort{})
}

func (c *Ctx) note(format string, a ...any) {
	c.res.Notes = append(c.res.Notes, fmt.Sprintf(format, a...))
}

// kill records one exercised kill point; a failed one stops the scenario.
func (c *Ctx) kill(point, outcome string, ok bool) {
	c.res.NumKills++
	if len(c.res.KillPoints) < 400 || !ok {
		c.res.KillPoints = append(c.res.KillPoints, KillPoint{Point: point, Outcome: outcome, OK: ok})
	}
	if !ok {
		c.failf("kill point %q: %s", point, outcome)
	}
}

// path returns a fresh database path in its own directory.
func (c *Ctx) path(name string) string {
	c.n++
	d := filepath.Join(c.dir, fmt.Sprintf("%02d-%s", c.n, name))
	os.RemoveAll(d)
	if err := os.MkdirAll(d, 0o755); err != nil {
		c.failf("mkdir: %v", err)
	}
	return filepath.Join(d, "db.kv")
}

func (c *Ctx) cli(args ...string) proc.Result {
	r := c.b.Run(2*time.Minute, nil, args...)
	if r.StartErr != nil || r.TimedOut {
		c.failf("kvdb %s: %s", argString(args), r.String())
	}
	return r
}

// argString shortens long arguments (values) for messages.
func argString(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if len(a) > 60 {
			a = fmt.Sprintf("%s...(%d bytes)", a[:40], len(a))
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

func (c *Ctx) mustCLI(args ...string) proc.Result {
	r := c.cli(args...)
	if !r.OK() {
		c.failf("kvdb %s: %s", argString(args), r.String())
	}
	return r
}

// recover restarts db through the real CLI and returns its state.
func (c *Ctx) recover(db string, maxBytes int64) engine.Recovery {
	rc := engine.Recover(c.b, db, maxBytes, 2*time.Minute)
	if rc.Problem != "" {
		c.failf("recovery of %s failed: %s", filepath.Base(filepath.Dir(db)), rc.Problem)
	}
	return rc
}

// batchFile writes ops to a batch file next to db and returns its path.
func (c *Ctx) batchFile(db string, lines []string) string {
	f := filepath.Join(filepath.Dir(db), fmt.Sprintf("batch-%d.txt", c.rng.Int63()))
	if err := os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		c.failf("%v", err)
	}
	return f
}

// state is the expected-state helper: apply "put k v" / "del k" lines.
func apply(s oracle.State, lines ...string) oracle.State {
	out := oracle.State{}
	for k, v := range s {
		out[k] = v
	}
	for _, l := range lines {
		f := strings.SplitN(l, " ", 3)
		switch f[0] {
		case "put":
			out[f[1]] = oracle.HashValue([]byte(f[2]))
		case "del":
			delete(out, f[1])
		}
	}
	return out
}

func equal(a, b oracle.State) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func diff(want, got oracle.State) string {
	var d []string
	for _, k := range workload.SortedKeys(want) {
		if g, ok := got[k]; !ok {
			d = append(d, k+": missing")
		} else if g != want[k] {
			d = append(d, k+": different value")
		}
	}
	for _, k := range workload.SortedKeys(got) {
		if _, ok := want[k]; !ok {
			d = append(d, k+": unexpected")
		}
	}
	if len(d) > 8 {
		d = append(d[:8], fmt.Sprintf("... %d more", len(d)-8))
	}
	return strings.Join(d, ", ")
}

// oneOf reports which of the named states got equals ("" if none).
func oneOf(got oracle.State, names []string, states ...oracle.State) string {
	for i, s := range states {
		if equal(got, s) {
			return names[i]
		}
	}
	return ""
}

// ---- crash-state replay ----

// writeStates performs write (a CLI command on db) on a copy of base and
// returns the file before and after. The write must append (it fails the
// scenario if the file was rewritten, e.g. by compaction).
func (c *Ctx) writeStates(base []byte, db string, args ...string) (pre, post []byte) {
	if err := os.WriteFile(db, base, 0o644); err != nil {
		c.failf("%v", err)
	}
	c.mustCLI(args...)
	post, err := os.ReadFile(db)
	if err != nil {
		c.failf("%v", err)
	}
	if !bytes.HasPrefix(post, base) || len(post) <= len(base) {
		c.failf("the write did not simply append (%d -> %d bytes); crash-state replay needs an append", len(base), len(post))
	}
	return base, post
}

// writeStatesServe is writeStates for a write sent through `kvdb serve`
// (for values too large for a command line). It waits for the ACK and
// closes the session normally.
func (c *Ctx) writeStatesServe(base []byte, db string, id string, payload []byte) (pre, post []byte) {
	if err := os.WriteFile(db, base, 0o644); err != nil {
		c.failf("%v", err)
	}
	t0 := time.Now()
	s, err := c.b.StartServe(db, nil, func() int64 { return time.Since(t0).Nanoseconds() })
	if err != nil {
		c.failf("%v", err)
	}
	ch, err := s.Send(id, payload)
	if err != nil {
		s.Kill()
		c.failf("send: %v", err)
	}
	var rp proc.Reply
	ok := false
	select {
	case rp, ok = <-ch:
	case <-time.After(2 * time.Minute):
	}
	s.CloseInput()
	if _, ok := s.Wait(time.Minute); !ok {
		s.Kill()
	}
	s.Release()
	if !ok || rp.Kind != "ACK" {
		c.failf("serve write: reply %+v ok=%v (stderr %s)", rp, ok, s.Stderr())
	}
	post, err = os.ReadFile(db)
	if err != nil {
		c.failf("%v", err)
	}
	if !bytes.HasPrefix(post, base) || len(post) <= len(base) {
		c.failf("the write did not simply append (%d -> %d bytes); crash-state replay needs an append", len(base), len(post))
	}
	return base, post
}

// prefixLens picks the prefix lengths to replay between lo and hi: every
// length when the range is small, otherwise every byte near both ends and
// near 4 KiB boundaries plus an even sample.
func prefixLens(lo, hi int, max int) []int {
	n := hi - lo
	set := map[int]bool{}
	if n+1 <= max {
		for l := lo; l <= hi; l++ {
			set[l] = true
		}
	} else {
		for i := 0; i <= 40; i++ {
			set[lo+i] = true
			set[hi-i] = true
		}
		for b := (lo/4096 + 1) * 4096; b < hi; b += 4096 {
			if len(set) > max/2 {
				break
			}
			set[b-1], set[b], set[b+1] = true, true, true
		}
		step := n / (max / 2)
		if step < 1 {
			step = 1
		}
		for l := lo; l <= hi; l += step {
			set[l] = true
		}
	}
	var out []int
	for l := range set {
		if l >= lo && l <= hi {
			out = append(out, l)
		}
	}
	sort.Ints(out)
	return out
}

// replay writes post[:l] as the database file, restarts the real binary on
// it and returns the recovered state.
func (c *Ctx) replay(db string, post []byte, l int, maxBytes int64) engine.Recovery {
	for _, s := range []string{".compact", ".old", ".new"} {
		os.Remove(db + s)
	}
	if err := os.WriteFile(db, post[:l], 0o644); err != nil {
		c.failf("%v", err)
	}
	return c.recover(db, maxBytes)
}

// ---- data helpers ----

// value makes a unique printable value of n bytes.
func (c *Ctx) value(tag string, n int) string {
	const alpha = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	p := tag + "/"
	if n < len(p) {
		n = len(p)
	}
	b := make([]byte, n-len(p))
	for i := range b {
		b[i] = alpha[c.rng.Intn(len(alpha))]
	}
	return p + string(b)
}

// baseDB creates a database with a few keys through the CLI and returns its
// bytes and state.
func (c *Ctx) baseDB(db string, keys int) ([]byte, oracle.State) {
	var lines []string
	for i := 0; i < keys; i++ {
		lines = append(lines, fmt.Sprintf("put base%03d %s", i, c.value(fmt.Sprintf("base%d", i), 20)))
	}
	c.mustCLI("open", db)
	if keys > 0 {
		c.mustCLI("batch", db, c.batchFile(db, lines))
	}
	b, err := os.ReadFile(db)
	if err != nil {
		c.failf("%v", err)
	}
	return b, apply(oracle.State{}, lines...)
}

// load fills db with n keys through one serve process (10,000-op batches,
// each acknowledged) and returns the state.
func (c *Ctx) load(db string, n int, maxBytes int64) oracle.State {
	files, want := workload.Preload(n, c.opt.Seed)
	t0 := time.Now()
	clock := func() int64 { return time.Since(t0).Nanoseconds() }
	var extra []string
	if maxBytes > 0 {
		extra = []string{"--max-bytes", fmt.Sprint(maxBytes)}
	}
	s, err := c.b.StartServe(db, extra, clock)
	if err != nil {
		c.failf("%v", err)
	}
	for i, f := range files {
		lines := bytes.Count(f, []byte("\n"))
		id := fmt.Sprintf("load%d", i)
		ch, err := s.Send(id, append([]byte(fmt.Sprintf("w0 %s batch %d\n", id, lines)), f...))
		if err != nil {
			c.failf("load: %v", err)
		}
		select {
		case rp, ok := <-ch:
			if !ok || rp.Kind != "ACK" {
				c.failf("load batch %d: %+v (stderr %s)", i, rp, s.Stderr())
			}
		case <-time.After(2 * time.Minute):
			s.Kill()
			c.failf("load batch %d: no reply", i)
		}
	}
	s.CloseInput()
	if code, ok := s.Wait(2 * time.Minute); !ok || code != 0 {
		c.failf("serve exit %d after load", code)
	}
	return want
}

// runEngine runs the randomized engine in mode and requires PASS.
func (c *Ctx) runEngine(mode string, iterations int, seed int64) engine.Summary {
	m, err := workload.ByName(mode)
	if err != nil {
		c.failf("%v", err)
	}
	dir := filepath.Join(c.dir, fmt.Sprintf("engine-%s-seed%d", mode, seed))
	var out bytes.Buffer
	sum, err := engine.Run(engine.Config{Mode: m, Seed: seed, Iterations: iterations, Kvdb: c.b, Dir: dir, Quiet: true, StopOnFail: true, Out: &out})
	if err != nil {
		c.failf("engine %s: tool error: %v", mode, err)
	}
	c.note("engine --mode %s --seed %d --iterations %d: %s, %d passed, acked %d, rejected %d, unknown %d, gets %d, kills with a write in flight %d, compaction in flight %d, torn tails %d, interrupted compactions %d, recovery kills %d/%d, max recovery %.1f ms",
		mode, seed, iterations, sum.Result, sum.Passed, sum.Acked, sum.Rejected, sum.Unknown, sum.Gets, sum.KillsMidWrite, sum.KillsMidCompac, sum.TornTails, sum.Authority, sum.RecoveryLanded, sum.RecoveryKills, sum.Perf.RecoveryMsMax)
	for i := 0; i < sum.Completed && i < 3; i++ {
		_ = i
	}
	c.res.NumKills += sum.Completed
	c.res.KillPoints = append(c.res.KillPoints, KillPoint{Point: fmt.Sprintf("%d real kills of kvdb serve (mode %s, seed %d)", sum.Completed, mode, seed),
		Outcome: fmt.Sprintf("%s: %d/%d iterations verified by the oracle", sum.Result, sum.Passed, sum.Planned), OK: sum.Result == "PASS"})
	switch sum.Result {
	case "PASS":
	case "INCONCLUSIVE":
		c.inconclusive("engine mode %s: %d iterations inconclusive", mode, sum.Inconclusive)
	default:
		f := ""
		if sum.FirstFailure != nil {
			f = sum.FirstFailure.Failure + "\nreproduce: " + sum.FirstFailure.Repro
		}
		c.failf("engine mode %s: %s %s\n%s", mode, sum.Result, sum.ToolError, f)
	}
	return sum
}

func (c *Ctx) iters(quick, full int) int {
	if c.opt.Full {
		return full
	}
	return quick
}

// RunAll runs the selected scenarios and writes scenarios.json,
// scenarios.jsonl and SCENARIOS.md into the work directory.
func RunAll(o Options) (Report, error) {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	rep := Report{Tool: "nexus-torture 2.0", Kvdb: o.Kvdb.Path, Full: o.Full, Seed: o.Seed, Started: time.Now().Format(time.RFC3339)}
	if err := engine.PrepareDir(o.Dir); err != nil {
		return rep, err
	}
	jl, err := os.Create(filepath.Join(o.Dir, "scenarios.jsonl"))
	if err != nil {
		return rep, err
	}
	defer jl.Close()
	size := "quick"
	if o.Full {
		size = "full"
	}
	fmt.Fprintf(o.Out, "nexus-torture scenarios: %s sizes, seed %d, kvdb %s\n", size, o.Seed, o.Kvdb.Path)
	t0 := time.Now()
	for i, s := range Catalog() {
		if o.Group != "" && s.Group != o.Group {
			continue
		}
		if len(o.Only) > 0 && !has(o.Only, s.ID) {
			continue
		}
		r := runOne(o, s, int64(i))
		b, _ := json.Marshal(r)
		jl.Write(append(b, '\n'))
		rep.Results = append(rep.Results, r)
		switch r.Status {
		case "PASS":
			rep.Passed++
		case "FAIL":
			rep.Failed++
		default:
			rep.Other++
		}
		fmt.Fprintf(o.Out, "[%s] %-12s %6.1fs  kill points %-6d %s\n", s.ID, r.Status, r.Seconds, r.NumKills, s.Title)
		if r.Status != "PASS" {
			fmt.Fprintf(o.Out, "     %s\n", strings.ReplaceAll(r.Failure, "\n", "\n     "))
		}
	}
	rep.Seconds = time.Since(t0).Seconds()
	switch {
	case rep.Failed > 0:
		rep.Result = "FAIL"
	case rep.Other > 0:
		rep.Result = "INCONCLUSIVE"
	case len(rep.Results) == 0:
		rep.Result = "ERROR"
	default:
		rep.Result = "PASS"
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	os.WriteFile(filepath.Join(o.Dir, "scenarios.json"), b, 0o644)
	os.WriteFile(filepath.Join(o.Dir, "SCENARIOS.md"), []byte(markdown(rep)), 0o644)
	fmt.Fprintf(o.Out, "\nSCENARIOS RESULT: %s  %d passed, %d failed, %d inconclusive/unsupported, of %d  (%.0fs)\nreport: %s\n",
		rep.Result, rep.Passed, rep.Failed, rep.Other, len(rep.Results), rep.Seconds, filepath.Join(o.Dir, "scenarios.json"))
	return rep, nil
}

func runOne(o Options, s Scenario, idx int64) (r Result) {
	r = Result{ID: s.ID, Group: s.Group, Title: s.Title, Invariant: s.Invariant, Method: s.Method, Status: "PASS"}
	c := &Ctx{opt: o, b: o.Kvdb, dir: filepath.Join(o.Dir, s.ID), res: &r, rng: rand.New(rand.NewSource(o.Seed + idx*7919))}
	os.RemoveAll(c.dir)
	os.MkdirAll(c.dir, 0o755)
	t0 := time.Now()
	func() {
		defer func() {
			if p := recover(); p != nil {
				if _, ok := p.(abort); !ok {
					r.Status = "FAIL"
					r.Failure = fmt.Sprintf("tool panic: %v", p)
				}
			}
		}()
		if s.run == nil {
			r.Status = "UNSUPPORTED"
			r.Failure = "not implemented"
			return
		}
		s.run(c)
	}()
	r.Seconds = time.Since(t0).Seconds()
	if r.Status == "PASS" && r.NumKills == 0 && s.Group != "E" && s.ID != "D4" && s.ID != "B5" {
		r.Status, r.Failure = "FAIL", "scenario exercised no kill point (tool bug)"
	}
	return r
}

func has(xs []string, s string) bool {
	for _, x := range xs {
		if strings.EqualFold(strings.TrimSpace(x), s) {
			return true
		}
	}
	return false
}

func markdown(rep Report) string {
	var b strings.Builder
	size := "quick"
	if rep.Full {
		size = "full"
	}
	fmt.Fprintf(&b, "# Official crash scenarios — NEXUS Torture Engine 2.0\n\nGenerated by `nexus-torture scenarios` (%s sizes, seed %d) on %s against `%s`.\n\n", size, rep.Seed, rep.Started, rep.Kvdb)
	fmt.Fprintf(&b, "**Result: %s** — %d passed, %d failed, %d inconclusive/unsupported, of %d (%.0f s).\n\n", rep.Result, rep.Passed, rep.Failed, rep.Other, len(rep.Results), rep.Seconds)
	b.WriteString("| ID | Scenario | Invariant | Method | Kill points | Result | Time |\n|---|---|---|---|---|---|---|\n")
	for _, r := range rep.Results {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | **%s** | %.1fs |\n", r.ID, esc(r.Title), esc(r.Invariant), esc(r.Method), r.NumKills, r.Status, r.Seconds)
	}
	b.WriteString("\n## Details\n")
	for _, r := range rep.Results {
		fmt.Fprintf(&b, "\n### %s — %s: %s\n\n", r.ID, esc(r.Title), r.Status)
		if r.Failure != "" {
			fmt.Fprintf(&b, "Failure: `%s`\n\n", strings.ReplaceAll(r.Failure, "\n", " "))
		}
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "- %s\n", esc(n))
		}
		shown := 0
		for _, k := range r.KillPoints {
			if shown == 12 {
				fmt.Fprintf(&b, "- … %d kill points in total (all in scenarios.json)\n", r.NumKills)
				break
			}
			mark := "ok"
			if !k.OK {
				mark = "FAILED"
			}
			fmt.Fprintf(&b, "- kill point: %s → %s (%s)\n", esc(k.Point), esc(k.Outcome), mark)
			shown++
		}
	}
	return b.String()
}

func esc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
