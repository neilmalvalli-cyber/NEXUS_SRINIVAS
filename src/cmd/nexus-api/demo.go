package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

// demo is the crash-and-recover demonstration. It never touches the main
// database: it uses its own file in the work directory. Crashes are real: a
// `kvdb serve` child process is fed puts and killed (TerminateProcess on
// Windows). Inspection (kvdb.CheckReadOnly, the `check --no-repair` code)
// and recovery (kvdb.Open, the `open` code) run in-process on that file.
type demo struct {
	dir  string
	kvdb string

	mu        sync.Mutex // one demo action at a time
	crash     *crashRecord
	inspected *inspectResult
	recovered *recoverResult
	events    []demoEvent
}

type crashRecord struct {
	At          string   `json:"at"`
	Requested   int      `json:"requested_writes"`
	KillAfterMs int      `json:"kill_after_ms"`
	Sent        int      `json:"sent"`
	Acked       int      `json:"acked"`
	Rejected    int      `json:"rejected"`
	Unknown     int      `json:"unknown"`
	KilledAtMs  float64  `json:"killed_at_ms"`
	ExitedEarly bool     `json:"exited_before_kill"`
	ServeStderr string   `json:"serve_stderr,omitempty"`
	AckedSample []string `json:"acked_keys_sample"`
	TornByHand  bool     `json:"torn_by_hand"`

	acked map[string]string // key -> value of every ACKed put
}

type inspectResult struct {
	At          string   `json:"at"`
	OK          bool     `json:"ok"`
	NeedsRepair bool     `json:"needs_repair"`
	Missing     bool     `json:"missing"`
	Keys        int      `json:"keys"`
	Frames      uint64   `json:"frames"`
	Size        int64    `json:"size"`
	End         int64    `json:"end"`
	LiveBytes   uint64   `json:"live_bytes"`
	Rules       []string `json:"rules"`
	TailRule    string   `json:"tail_rule"`
	Repairs     []string `json:"repairs"`
	ElapsedMs   float64  `json:"elapsed_ms"`
	Error       *apiErr  `json:"error"`
	Method      string   `json:"method"`
}

type recoverResult struct {
	At       string   `json:"at"`
	OK       bool     `json:"ok"`
	Open     lastOpen `json:"open"`
	Keys     int      `json:"keys"`
	Size     int64    `json:"size"`
	Verified bool     `json:"file_verified"`
	// Acknowledged writes of the last crash, checked after recovery.
	AckedChecked int      `json:"acked_checked"`
	AckedMissing int      `json:"acked_missing"`
	AckedWrong   int      `json:"acked_wrong_value"`
	MissingKeys  []string `json:"missing_keys_sample"`
	Expectation  string   `json:"expectation"`
	Error        *apiErr  `json:"error"`
}

type demoEvent struct {
	At      string `json:"at"`
	Action  string `json:"action"`
	Summary string `json:"summary"`
	OK      bool   `json:"ok"`
}

func (d *demo) path() string { return filepath.Join(d.dir, "demo.kv") }

func (d *demo) note(action, summary string, ok bool) {
	d.events = append(d.events, demoEvent{time.Now().UTC().Format(time.RFC3339Nano), action, summary, ok})
	if len(d.events) > 50 {
		d.events = d.events[len(d.events)-50:]
	}
}

func (d *demo) state() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stateLocked()
}

func (d *demo) stateLocked() map[string]any {
	ev := make([]demoEvent, len(d.events))
	for i := range d.events {
		ev[i] = d.events[len(d.events)-1-i]
	}
	return map[string]any{
		"db_path":       d.path(),
		"file":          statFile("data", d.path(), "crash-demo database (separate from the main database)"),
		"kvdb":          nullable(d.kvdb),
		"last_crash":    d.crash,
		"last_inspect":  d.inspected,
		"last_recovery": d.recovered,
		"events":        ev,
	}
}

func (s *Server) handleDemoState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.demo.state())
}

// reset recreates the demo database with n keys (in-process, synced).
func (s *Server) handleDemoReset(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Keys int `json:"keys"`
	}
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	if b.Keys < 0 || b.Keys > 10000 {
		writeError(w, http.StatusBadRequest, "invalid", "keys must be between 0 and 10000", false)
		return
	}
	d := s.demo
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		engineError(w, err, false)
		return
	}
	for _, suffix := range []string{"", ".compact", ".old"} {
		if err := os.Remove(d.path() + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			engineError(w, fmt.Errorf("removing old demo file: %w", err), false)
			return
		}
	}
	db, err := kvdb.Open(d.path(), 0)
	if err != nil {
		engineError(w, err, false)
		return
	}
	if b.Keys > 0 {
		ops := make([]kvdb.Op, b.Keys)
		for i := range ops {
			ops[i] = kvdb.Op{Kind: kvdb.OpPut, Key: []byte(fmt.Sprintf("base-%05d", i)), Value: []byte(fmt.Sprintf("value-%05d", i))}
		}
		if err := db.Batch(ops); err != nil {
			db.Close()
			engineError(w, err, true)
			return
		}
	}
	st := db.Stats()
	if err := db.Close(); err != nil {
		engineError(w, err, false)
		return
	}
	d.crash, d.inspected, d.recovered = nil, nil, nil
	d.note("reset", fmt.Sprintf("demo database recreated with %d keys (%d bytes)", st.Keys, st.Size), true)
	writeJSON(w, http.StatusOK, d.stateLocked())
}

// crash starts `kvdb serve demo.kv`, streams puts as fast as it accepts
// them and kills the process after kill_after_ms.
func (s *Server) handleDemoCrash(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Writes      int `json:"writes"`
		KillAfterMs int `json:"kill_after_ms"`
	}
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	if b.Writes < 1 || b.Writes > 20000 || b.KillAfterMs < 1 || b.KillAfterMs > 10000 {
		writeError(w, http.StatusBadRequest, "invalid", "writes must be 1-20000 and kill_after_ms 1-10000", false)
		return
	}
	d := s.demo
	if d.kvdb == "" {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "kvdb executable not found: build it with `go build -o . ./src/cmd/kvdb` and start nexus-api with --kvdb", false)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !fileExists(d.path()) {
		writeError(w, http.StatusConflict, "invalid", "create the demo database first (reset)", false)
		return
	}
	rec, err := runCrash(d.kvdb, d.path(), b.Writes, b.KillAfterMs)
	if err != nil {
		d.note("crash", "could not run kvdb serve: "+err.Error(), false)
		writeError(w, http.StatusInternalServerError, "adapter", err.Error(), false)
		return
	}
	d.crash, d.inspected, d.recovered = rec, nil, nil
	sum := fmt.Sprintf("kvdb serve killed after %.0f ms: %d sent, %d acknowledged, %d unknown", rec.KilledAtMs, rec.Sent, rec.Acked, rec.Unknown)
	if rec.ExitedEarly {
		sum = fmt.Sprintf("kvdb serve exited by itself before the kill: %d sent, %d acknowledged", rec.Sent, rec.Acked)
	}
	d.note("crash", sum, !rec.ExitedEarly)
	writeJSON(w, http.StatusOK, d.stateLocked())
}

func runCrash(kvdbExe, path string, writes, killAfterMs int) (*crashRecord, error) {
	tag := make([]byte, 4)
	rand.Read(tag)
	run := hex.EncodeToString(tag)
	cmd := exec.Command(kvdbExe, "serve", path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	ready := make(chan struct{})
	pr, pw := io.Pipe()
	cmd.Stderr = pw
	go func() {
		sc := bufio.NewScanner(pr)
		once := false
		for sc.Scan() {
			line := sc.Text()
			if stderr.Len() < 4096 {
				stderr.WriteString(line + "\n")
			}
			if !once && strings.HasPrefix(line, "serve: ready") {
				once = true
				close(ready)
			}
		}
		if !once {
			close(ready)
		}
	}()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); pw.Close(); close(exited) }()

	select {
	case <-ready:
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		<-exited
		return nil, errors.New("kvdb serve did not become ready within 30 s")
	}

	rec := &crashRecord{At: time.Now().UTC().Format(time.RFC3339Nano), Requested: writes, KillAfterMs: killAfterMs, acked: map[string]string{}}
	type sentPut struct{ k, v string }
	var mu sync.Mutex
	sent := map[string]sentPut{}

	replies := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) < 2 {
				continue
			}
			mu.Lock()
			p, ok := sent[f[1]]
			if ok {
				switch f[0] {
				case "ACK":
					rec.acked[p.k] = p.v
				case "ERR":
					rec.Rejected++ // ERR with a definite class (the class is f[2])
					if len(f) > 2 && (f[2] == "unknown" || f[2] == "io") {
						rec.Rejected--
					}
				}
				delete(sent, f[1])
			}
			mu.Unlock()
		}
		close(replies)
	}()

	start := time.Now()
	go func() {
		bw := bufio.NewWriterSize(stdin, 1<<16)
		for i := 0; i < writes; i++ {
			id := fmt.Sprintf("%d", i)
			k := fmt.Sprintf("crash-%s-%05d", run, i)
			v := fmt.Sprintf("v%d-%s", i, run)
			mu.Lock()
			sent[id] = sentPut{k, v}
			rec.Sent++
			mu.Unlock()
			if _, err := fmt.Fprintf(bw, "w0 %s put %s %s\n", id, k, v); err != nil {
				return
			}
			if i%32 == 31 || i == writes-1 {
				if err := bw.Flush(); err != nil {
					return
				}
			}
		}
	}()

	select {
	case <-exited:
		rec.ExitedEarly = true
	case <-time.After(time.Duration(killAfterMs) * time.Millisecond):
		cmd.Process.Kill() // TerminateProcess on Windows: no cleanup runs in kvdb
		rec.KilledAtMs = ms(time.Since(start))
		<-exited
	}
	stdin.Close()
	<-replies
	mu.Lock()
	defer mu.Unlock()
	rec.Acked = len(rec.acked)
	// Requests sent without a definite reply (or with ERR unknown/io): their
	// outcome is unknown. Writes never sent are not counted.
	rec.Unknown = rec.Sent - rec.Acked - rec.Rejected
	rec.ServeStderr = strings.TrimSpace(stderr.String())
	for k := range rec.acked {
		if len(rec.AckedSample) == 8 {
			break
		}
		rec.AckedSample = append(rec.AckedSample, k)
	}
	return rec, nil
}

// inspect runs the read-only check (no repair) on the demo database.
func (s *Server) handleDemoInspect(w http.ResponseWriter, r *http.Request) {
	d := s.demo
	d.mu.Lock()
	defer d.mu.Unlock()
	t0 := time.Now()
	c, err := kvdb.CheckReadOnly(d.path())
	res := &inspectResult{At: t0.UTC().Format(time.RFC3339Nano), OK: err == nil, NeedsRepair: c.NeedsRepair(),
		Missing: c.Missing, Keys: c.Keys, Frames: c.Frames, Size: c.Size, End: c.End, LiveBytes: c.LiveBytes,
		Rules: nonNil(c.Rules), TailRule: c.Tail, Repairs: nonNil(c.Repairs), ElapsedMs: ms(time.Since(t0)),
		Method: "kvdb.CheckReadOnly (the code behind `kvdb check --no-repair`): same authority rules and frame scan as open, nothing is written"}
	if err != nil {
		res.Error = &apiErr{errClass(err), err.Error()}
	}
	d.inspected = res
	switch {
	case err != nil:
		d.note("inspect", "read-only check failed: "+err.Error(), false)
	case c.NeedsRepair():
		d.note("inspect", "open would repair: "+strings.Join(c.Repairs, "; "), true)
	default:
		d.note("inspect", fmt.Sprintf("clean: %d keys, %d frames, nothing to repair", c.Keys, c.Frames), true)
	}
	writeJSON(w, http.StatusOK, d.stateLocked())
}

// tear truncates the last n bytes of the demo file by hand (scenario F4).
func (s *Server) handleDemoTear(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Bytes int64 `json:"bytes"`
	}
	if !decodeBody(w, r, 1<<10, &b) {
		return
	}
	if b.Bytes < 1 || b.Bytes > 4096 {
		writeError(w, http.StatusBadRequest, "invalid", "bytes must be between 1 and 4096", false)
		return
	}
	d := s.demo
	d.mu.Lock()
	defer d.mu.Unlock()
	st, err := os.Stat(d.path())
	if err != nil {
		writeError(w, http.StatusConflict, "invalid", "create the demo database first (reset)", false)
		return
	}
	if st.Size()-b.Bytes < 32 {
		writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("the file is %d bytes; cutting %d would reach the 32-byte superblock", st.Size(), b.Bytes), false)
		return
	}
	if err := os.Truncate(d.path(), st.Size()-b.Bytes); err != nil {
		engineError(w, err, false)
		return
	}
	if d.crash != nil {
		d.crash.TornByHand = true
	}
	d.inspected, d.recovered = nil, nil
	d.note("tear", fmt.Sprintf("truncated %d bytes by hand: %d → %d", b.Bytes, st.Size(), st.Size()-b.Bytes), true)
	writeJSON(w, http.StatusOK, d.stateLocked())
}

// recover opens the demo database (running recovery), checks every write
// acknowledged before the last crash, verifies the file and closes it.
func (s *Server) handleDemoRecover(w http.ResponseWriter, r *http.Request) {
	d := s.demo
	d.mu.Lock()
	defer d.mu.Unlock()
	if !fileExists(d.path()) {
		writeError(w, http.StatusConflict, "invalid", "create the demo database first (reset)", false)
		return
	}
	t0 := time.Now()
	db, err := kvdb.Open(d.path(), 0)
	el := time.Since(t0)
	res := &recoverResult{At: t0.UTC().Format(time.RFC3339Nano), MissingKeys: []string{}}
	if err != nil {
		res.Error = &apiErr{errClass(err), err.Error()}
		d.recovered = res
		d.note("recover", "open failed: "+err.Error(), false)
		writeJSON(w, http.StatusOK, d.stateLocked())
		return
	}
	res.Open = recoveryJSON(t0, el, db.Recovery())
	if d.crash != nil {
		for k, want := range d.crash.acked {
			res.AckedChecked++
			got, gerr := db.Get([]byte(k))
			switch {
			case errors.Is(gerr, kvdb.ErrNotFound):
				res.AckedMissing++
				if len(res.MissingKeys) < 10 {
					res.MissingKeys = append(res.MissingKeys, k)
				}
			case gerr != nil || string(got) != want:
				res.AckedWrong++
			}
		}
		switch {
		case d.crash.TornByHand:
			res.Expectation = "the file was truncated by hand after the crash, which can remove acknowledged frames on purpose; missing writes here are expected and are not a durability failure"
		default:
			res.Expectation = "every write acknowledged before the kill must be present with its value"
		}
	} else {
		res.Expectation = "no crash recorded since the last reset; nothing to check beyond a clean open and verify"
	}
	if _, verr := db.Verify(); verr == nil {
		res.Verified = true
	} else {
		res.Error = &apiErr{errClass(verr), verr.Error()}
	}
	st := db.Stats()
	res.Keys, res.Size = st.Keys, st.Size
	if cerr := db.Close(); cerr != nil && res.Error == nil {
		res.Error = &apiErr{errClass(cerr), cerr.Error()}
	}
	durable := res.AckedMissing == 0 && res.AckedWrong == 0
	res.OK = res.Error == nil && res.Verified && (durable || (d.crash != nil && d.crash.TornByHand))
	d.recovered = res
	sum := fmt.Sprintf("recovered in %.2f ms: %d keys", res.Open.DurationMs, res.Keys)
	if res.Open.TailRule != "" {
		sum += fmt.Sprintf("; discarded a torn %d-byte tail (rule %s)", res.Open.DiscardedBytes, res.Open.TailRule)
	}
	if d.crash != nil {
		sum += fmt.Sprintf("; %d of %d acknowledged writes present", res.AckedChecked-res.AckedMissing-res.AckedWrong, res.AckedChecked)
	}
	d.note("recover", sum, res.OK)
	writeJSON(w, http.StatusOK, d.stateLocked())
}
