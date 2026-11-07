package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// workload describes one mode's request mix.
type workload struct {
	writers     int
	keys        int // shared key space "k0000".."kNNNN"
	disjoint    bool
	sameKey     bool
	minVal      int
	maxVal      int
	bigValP     float64 // probability a put uses a value of exactly 1 MiB
	pPut        int     // weights
	pDel        int
	pBatch      int
	pGet        int
	pCompact    int
	batchMin    int
	batchMax    int
	bigBatch    bool // writer 0 sends 10,000-op batches on its own key space
	minKillMs   int
	maxKillMs   int
	maxBytes    int64
	recoverKill bool // kill `kvdb open` during recovery after each serve kill
	preloadKeys int
}

const oneMiB = 1 << 20

func modeWorkload(mode string) (workload, error) {
	w := workload{writers: 3, keys: 64, minVal: 8, maxVal: 256, pPut: 45, pDel: 15, pBatch: 20, pGet: 15, pCompact: 5,
		batchMin: 2, batchMax: 20, minKillMs: 5, maxKillMs: 600}
	switch mode {
	case "default":
	case "large-values":
		w.writers, w.keys, w.minVal, w.maxVal, w.bigValP = 2, 16, 256<<10, oneMiB, 0.5
		w.pPut, w.pDel, w.pBatch, w.pGet, w.pCompact = 60, 15, 10, 15, 0
		w.batchMin, w.batchMax, w.maxKillMs = 2, 3, 1500
	case "large-batch":
		w.writers, w.bigBatch, w.minVal, w.maxVal = 3, true, 8, 48
		w.pPut, w.pDel, w.pBatch, w.pGet, w.pCompact = 60, 20, 5, 15, 0
		w.maxKillMs = 2500
	case "compaction":
		w.writers, w.keys, w.minVal, w.maxVal = 3, 128, 1<<10, 16<<10
		w.pPut, w.pDel, w.pBatch, w.pGet, w.pCompact = 60, 10, 10, 8, 12
		w.batchMin, w.batchMax, w.minKillMs, w.maxKillMs = 2, 8, 20, 1500
	case "recovery-crash":
		w.recoverKill, w.preloadKeys, w.maxKillMs = true, 200_000, 800
	case "disk-full":
		w.keys, w.minVal, w.maxVal, w.maxBytes = 600, 256, 2048, 256<<10
		w.pPut, w.pDel, w.pBatch, w.pGet, w.pCompact = 55, 25, 10, 5, 5
		w.batchMin, w.batchMax, w.maxKillMs = 2, 10, 1200
	case "concurrent-disjoint":
		w.writers, w.disjoint = 2, true
		w.pPut, w.pDel, w.pBatch, w.pGet, w.pCompact = 70, 15, 0, 15, 0
	case "same-key":
		w.writers, w.sameKey = 2, true
		w.pPut, w.pDel, w.pBatch, w.pGet, w.pCompact = 80, 10, 0, 10, 0
		w.minVal, w.maxVal = 1024, 64<<10
	default:
		return w, fmt.Errorf("unknown --mode %q", mode)
	}
	return w, nil
}

var modes = []string{"default", "large-values", "large-batch", "compaction", "recovery-crash", "disk-full", "concurrent-disjoint", "same-key"}

// runner holds the state that carries over between iterations.
type runner struct {
	cfg      config
	wl       workload
	db       string
	baseline map[string]string // verified state after the previous iteration
	oracle   *oracleLog
	openMs   float64 // typical `kvdb open` wall time (recovery-crash kill window)
}

// iterResult is one line of the machine-readable report.
type iterResult struct {
	Iter           int        `json:"iter"`
	Seed           int64      `json:"iter_seed"`
	KillAfterMs    int        `json:"kill_after_ms"`
	Sent           int        `json:"sent"`
	Acked          int        `json:"acked"`
	Rejected       int        `json:"rejected"`
	Unknown        int        `json:"unknown"`
	Gets           int        `json:"gets"`
	InFlightAtKill []string   `json:"in_flight_at_kill"`
	CompactInKill  bool       `json:"compaction_in_flight_at_kill"`
	ErrClasses     []string   `json:"err_classes,omitempty"`
	RecoveryKills  int        `json:"recovery_kills"`
	RecoveryLanded int        `json:"recovery_kills_landed"`
	Keys           int        `json:"keys"`
	FileBytes      int64      `json:"file_bytes"`
	OpenMs         float64    `json:"open_recovery_ms"`
	StaleFiles     []string   `json:"stale_files_after_open,omitempty"`
	TornTail       bool       `json:"recovery_discarded_torn_tail"`
	AuthorityRule  bool       `json:"recovery_applied_authority_rule"`
	Pass           bool       `json:"pass"`
	Failure        *violation `json:"failure,omitempty"`
	FailureText    string     `json:"failure_text,omitempty"`
	DurationMs     float64    `json:"duration_ms"`
}

// oracleLog is the torture tool's own record of every request and reply.
type oracleLog struct {
	mu sync.Mutex
	f  *os.File
	w  *bufio.Writer
}

func (l *oracleLog) write(v any) {
	b, _ := json.Marshal(v)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.w.Write(b)
	l.w.WriteByte('\n')
}

func (l *oracleLog) flush() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.w.Flush(); err != nil {
		return err
	}
	return l.f.Sync()
}

// reply is one parsed line from serve.
type reply struct {
	kind  string // ACK, ERR, VALUE, NOTFOUND
	class string
	text  string
	value []byte
	at    int64
}

// router delivers serve's replies to the writer waiting for each op ID.
type router struct {
	mu      sync.Mutex
	waiting map[string]chan reply
	closed  bool
	stray   []string
}

func (r *router) register(id string) (chan reply, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, false
	}
	ch := make(chan reply, 1)
	r.waiting[id] = ch
	return ch, true
}

func (r *router) deliver(id string, rp reply) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.waiting[id]
	if !ok {
		r.stray = append(r.stray, id)
		return
	}
	delete(r.waiting, id)
	ch <- rp
}

func (r *router) closeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for id, ch := range r.waiting {
		close(ch)
		delete(r.waiting, id)
	}
}

func (r *router) readLoop(out io.Reader, start time.Time) {
	br := bufio.NewReaderSize(out, 1<<20)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			// EOF (process exited or was killed). A partial last line is
			// ignored: it is not a complete reply, so it promises nothing.
			r.closeAll()
			return
		}
		at := time.Since(start).Nanoseconds()
		line = strings.TrimRight(line, "\r\n")
		kind, rest, _ := strings.Cut(line, " ")
		id, rest, _ := strings.Cut(rest, " ")
		rp := reply{kind: kind, at: at}
		switch kind {
		case "ACK", "NOTFOUND":
		case "VALUE":
			v := rest
			if strings.HasPrefix(v, `"`) {
				if u, err := strconv.Unquote(v); err == nil {
					v = u
				}
			}
			rp.value = []byte(v)
		case "ERR":
			rp.class, rp.text, _ = strings.Cut(rest, " ")
		default:
			r.mu.Lock()
			r.stray = append(r.stray, "unparsable: "+truncate(line, 80))
			r.mu.Unlock()
			continue
		}
		r.deliver(id, rp)
	}
}

// definiteClasses are serve error classes that guarantee nothing changed.
var definiteClasses = map[string]bool{"full": true, "invalid": true, "too-large": true, "failed": true, "closed": true, "protocol": true}

// allowedErr lists the error classes a correct database may return in a
// mode. Anything else (corruption, bad-format, ...) fails the iteration.
func (w workload) allowedErr(class string) bool {
	switch class {
	case "unknown", "failed":
		return true // a failed Sync / swap; reported, handled as unknown / rejected
	case "full":
		return w.maxBytes > 0
	}
	return false
}

func (rn *runner) kvdb(args ...string) *exec.Cmd {
	return exec.Command(rn.cfg.kvdb, args...)
}

func (rn *runner) maxBytesArgs() []string {
	if rn.wl.maxBytes > 0 {
		return []string{"--max-bytes", strconv.FormatInt(rn.wl.maxBytes, 10)}
	}
	return nil
}

// iteration runs one start-workload-kill-recover-verify cycle.
func (rn *runner) iteration(iter int, iterSeed int64) (res iterResult, fatal error) {
	t0 := time.Now()
	res = iterResult{Iter: iter, Seed: iterSeed}
	rng := rand.New(rand.NewSource(iterSeed))
	res.KillAfterMs = rn.wl.minKillMs + rng.Intn(rn.wl.maxKillMs-rn.wl.minKillMs+1)

	cmd := rn.kvdb(append([]string{"serve", rn.db}, rn.maxBytesArgs()...)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return res, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return res, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return res, fmt.Errorf("starting %s serve: %w", rn.cfg.kvdb, err)
	}
	rt := &router{waiting: map[string]chan reply{}}
	readerDone := make(chan struct{})
	go func() { rt.readLoop(stdout, start); close(readerDone) }()

	var (
		opsMu    sync.Mutex
		ops      []*opRec
		stdinMu  sync.Mutex
		stop     atomic.Bool
		inflight sync.Map // writer -> *opRec
		wg       sync.WaitGroup
	)
	gen := newGenerator(rn.wl, iter, iterSeed)
	for w := 0; w < rn.wl.writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			wr := gen.writer(w)
			for seq := 0; !stop.Load(); seq++ {
				o, payload := wr.next(seq)
				ch, ok := rt.register(o.id)
				if !ok {
					return
				}
				o.sendT = time.Since(start).Nanoseconds()
				opsMu.Lock()
				ops = append(ops, o)
				opsMu.Unlock()
				inflight.Store(w, o)
				rn.oracle.write(map[string]any{"iter": iter, "ev": "send", "op": o.id, "writer": w, "kind": o.kind,
					"nops": o.nops, "effects": effectsJSON(o.effects), "get_key": o.getKey, "t_ns": o.sendT})
				stdinMu.Lock()
				_, werr := stdin.Write(payload)
				stdinMu.Unlock()
				var rp reply
				var got bool
				if werr == nil {
					rp, got = <-ch
				}
				inflight.Delete(w)
				switch {
				case !got:
					o.status = stUnknown
				case rp.kind == "ACK":
					o.status, o.endT = stAcked, rp.at
				case rp.kind == "VALUE" || rp.kind == "NOTFOUND":
					o.status, o.endT = stAcked, rp.at
					o.getFound = rp.kind == "VALUE"
					if o.getFound {
						o.getHash = valueHash(rp.value)
					}
				case rp.kind == "ERR" && definiteClasses[rp.class]:
					o.status, o.errText = stRejected, rp.class+" "+rp.text
				default:
					o.status, o.errText = stUnknown, rp.class+" "+rp.text
				}
				ev := map[string]any{"iter": iter, "ev": "result", "op": o.id, "status": o.status.String(), "t_ns": o.endT}
				if o.errText != "" {
					ev["err"] = o.errText
				}
				if o.getFound {
					ev["get_hash"] = o.getHash
				}
				rn.oracle.write(ev)
				if !got {
					return
				}
			}
		}(w)
	}

	// The kill: a genuinely random moment, in ms from process start,
	// independent of how many operations have run.
	time.Sleep(time.Until(start.Add(time.Duration(res.KillAfterMs) * time.Millisecond)))
	inflight.Range(func(k, v any) bool {
		o := v.(*opRec)
		res.InFlightAtKill = append(res.InFlightAtKill, o.id+":"+o.kind)
		if o.kind == "compact" {
			res.CompactInKill = true
		}
		return true
	})
	killErr := cmd.Process.Kill() // TerminateProcess on Windows, SIGKILL elsewhere
	stop.Store(true)
	// Read every reply the process wrote before it died (EOF comes when the
	// pipe's write end closes), and only then Wait, which closes our end.
	<-readerDone
	waitErr := cmd.Wait()
	stdin.Close()
	wg.Wait()
	exitedEarly := killErr != nil
	rn.oracle.write(map[string]any{"iter": iter, "ev": "kill", "after_ms": res.KillAfterMs, "kill_err": fmt.Sprint(killErr), "wait": fmt.Sprint(waitErr)})

	for _, o := range ops {
		res.Sent++
		switch {
		case o.kind == "get":
			res.Gets++
		case o.status == stAcked:
			res.Acked++
		case o.status == stRejected:
			res.Rejected++
			res.ErrClasses = append(res.ErrClasses, strings.Fields(o.errText)[0])
		case o.status == stUnknown:
			res.Unknown++
			if o.errText != "" {
				res.ErrClasses = append(res.ErrClasses, strings.Fields(o.errText)[0])
			}
		}
	}
	fail := func(v *violation) (iterResult, error) {
		res.Pass, res.Failure, res.FailureText = false, v, v.Error()
		res.DurationMs = ms(time.Since(t0).Nanoseconds())
		return res, nil
	}
	if exitedEarly {
		return fail(&violation{Reason: fmt.Sprintf("serve exited before the kill (exit: %v); stderr:\n%s", waitErr, stderr.String())})
	}
	if len(rt.stray) > 0 {
		return fail(&violation{Reason: fmt.Sprintf("serve printed replies for unknown op IDs or unparsable lines: %v", rt.stray)})
	}
	for _, o := range ops {
		if o.errText != "" && !rn.wl.allowedErr(strings.Fields(o.errText)[0]) {
			return fail(&violation{Reason: fmt.Sprintf("op %s (%s) failed with an error class a correct database must not produce here: %s", o.id, o.kind, o.errText)})
		}
	}

	// Kill during recovery (recovery-crash mode): start `kvdb open` and kill
	// it at a random point inside its expected run time, 1–3 times.
	if rn.wl.recoverKill {
		n := 1 + rng.Intn(3)
		for i := 0; i < n; i++ {
			delay := time.Duration(rng.Float64()*rn.openMs*1.2*1e6) * time.Nanosecond
			landed, err := rn.killDuringOpen(delay)
			if err != nil {
				return res, err
			}
			res.RecoveryKills++
			if landed {
				res.RecoveryLanded++
			}
		}
	}

	// Restart: open, check, dump. Each is a separate process.
	out, errOut, err := rn.runCmd(append([]string{"open", rn.db}, rn.maxBytesArgs()...)...)
	if err != nil {
		return fail(&violation{Reason: fmt.Sprintf("open failed after kill: %v\nstdout: %s\nstderr: %s", err, out, errOut)})
	}
	var openKeys int
	var size int64
	if _, err := fmt.Sscanf(out, "keys=%d recovery_ms=%f size=%d", &openKeys, &res.OpenMs, &size); err != nil {
		return fail(&violation{Reason: fmt.Sprintf("cannot parse open output %q: %v", out, err)})
	}
	res.StaleFiles = staleFiles(rn.db)
	res.TornTail = strings.Contains(errOut, "discarded a torn")
	res.AuthorityRule = strings.Contains(errOut, "authority rule")
	out, errOut, err = rn.runCmd("check", rn.db)
	if err != nil || strings.TrimSpace(out) != "OK" {
		return fail(&violation{Reason: fmt.Sprintf("check did not print OK: %v\nstdout: %s\nstderr: %s", err, out, errOut)})
	}
	dump := rn.kvdb("dump", rn.db)
	var dumpErr bytes.Buffer
	dump.Stderr = &dumpErr
	dout, err := dump.StdoutPipe()
	if err != nil {
		return res, err
	}
	if err := dump.Start(); err != nil {
		return res, err
	}
	final, perr := parseDump(dout)
	if werr := dump.Wait(); werr != nil || perr != nil {
		return fail(&violation{Reason: fmt.Sprintf("dump failed: %v / parse: %v\nstderr: %s", werr, perr, dumpErr.String())})
	}
	res.Keys = len(final)
	if fi, err := os.Stat(rn.db); err == nil {
		res.FileBytes = fi.Size()
	}
	if openKeys != len(final) {
		return fail(&violation{Reason: fmt.Sprintf("open reported %d keys but dump listed %d", openKeys, len(final))})
	}
	if rn.wl.maxBytes > 0 && res.FileBytes > rn.wl.maxBytes {
		return fail(&violation{Reason: fmt.Sprintf("file is %d bytes, above --max-bytes %d", res.FileBytes, rn.wl.maxBytes)})
	}
	if v := checkState(rn.baseline, final, ops); v != nil {
		return fail(v)
	}
	if v := checkGets(rn.baseline, ops); v != nil {
		return fail(v)
	}
	rn.baseline = final
	res.Pass = true
	res.DurationMs = ms(time.Since(t0).Nanoseconds())
	return res, nil
}

func effectsJSON(m map[string]effect) map[string]string {
	if len(m) > 64 {
		return map[string]string{"(count)": strconv.Itoa(len(m))}
	}
	out := make(map[string]string, len(m))
	for k, e := range m {
		if e.del {
			out[k] = "del"
		} else {
			out[k] = e.hash
		}
	}
	return out
}

func (rn *runner) runCmd(args ...string) (string, string, error) {
	c := rn.kvdb(args...)
	var o, e bytes.Buffer
	c.Stdout, c.Stderr = &o, &e
	err := c.Run()
	return o.String(), e.String(), err
}

// killDuringOpen starts `kvdb open` and kills it after delay. landed reports
// whether the process was still running (recovery in progress) when killed.
func (rn *runner) killDuringOpen(delay time.Duration) (landed bool, err error) {
	c := rn.kvdb("open", rn.db)
	c.Stdout, c.Stderr = io.Discard, io.Discard
	if err := c.Start(); err != nil {
		return false, err
	}
	time.Sleep(delay)
	kerr := c.Process.Kill()
	c.Wait()
	rn.oracle.write(map[string]any{"ev": "kill-open", "delay_ms": ms(delay.Nanoseconds()), "landed": kerr == nil})
	return kerr == nil, nil
}

func staleFiles(db string) []string {
	var out []string
	for _, s := range []string{".compact", ".old", ".new"} {
		if _, err := os.Stat(db + s); err == nil {
			out = append(out, filepath.Base(db+s))
		}
	}
	return out
}

// measureOpen times a few `kvdb open` runs (process start + recovery).
func (rn *runner) measureOpen() error {
	best := 0.0
	for i := 0; i < 3; i++ {
		t := time.Now()
		if _, e, err := rn.runCmd("open", rn.db); err != nil {
			return fmt.Errorf("open: %v: %s", err, e)
		}
		d := ms(time.Since(t).Nanoseconds())
		if d > best {
			best = d
		}
	}
	rn.openMs = best
	return nil
}

// preload fills the database with n keys "p0000000".. through `kvdb batch`
// (10,000 ops per batch), so recovery has real work to do.
func (rn *runner) preload(n int, seed int64) error {
	rng := rand.New(rand.NewSource(seed))
	dir := filepath.Dir(rn.db)
	for base := 0; base < n; base += 10000 {
		var sb strings.Builder
		for i := base; i < base+10000 && i < n; i++ {
			fmt.Fprintf(&sb, "put p%07d %s\n", i, randText(rng, 16+rng.Intn(48)))
		}
		f := filepath.Join(dir, "preload.txt")
		if err := os.WriteFile(f, []byte(sb.String()), 0o644); err != nil {
			return err
		}
		if _, e, err := rn.runCmd("batch", rn.db, f); err != nil {
			return fmt.Errorf("preload batch: %v: %s", err, e)
		}
		os.Remove(f)
	}
	return nil
}

// loadBaseline reads the current database state via dump (used once at the
// start when the database was preloaded; verified against the preload).
func (rn *runner) loadBaseline() (map[string]string, error) {
	c := rn.kvdb("dump", rn.db)
	out, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	m, perr := parseDump(out)
	if err := errors.Join(c.Wait(), perr); err != nil {
		return nil, err
	}
	return m, nil
}
