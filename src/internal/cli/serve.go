package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

// serve protocol (README "serve"; DESIGN §18.2–18.4)
//
// Input, one request per line:
//
//	<writer-id> <op-id> put <key> <value>
//	<writer-id> <op-id> del <key>
//	<writer-id> <op-id> get <key>
//	<writer-id> <op-id> batch <n>      then n lines "put k v" / "del k"
//	<writer-id> <op-id> compact
//
// "<writer-id> " may be omitted ("<op-id> put k v"); such requests belong to
// writer "-". Output, one line per request, flushed immediately:
//
//	ACK <op-id>                      the write is durable (Sync returned)
//	VALUE <op-id> <value>            get: value (Go-quoted if not plain text)
//	NOTFOUND <op-id>                 get: key absent
//	ERR <op-id> <class> <message>    failure; class "unknown" = may be durable
//
// Requests of one writer run strictly in submission order, each after the
// previous one finished. Different writers run concurrently; the engine's
// lock serializes their commits.

// serveCommands are the request verbs; used to tell the short form (no
// writer ID) from the full form.
var serveCommands = map[string]bool{"put": true, "del": true, "delete": true, "get": true, "batch": true, "compact": true}

type request struct {
	writer, id string
	verb       string
	key        []byte
	value      []byte
	ops        []kvdb.Op // batch
	err        error     // parse error to report instead of executing
}

// output serializes reply lines on stdout and flushes after each one.
type output struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func (o *output) line(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.w.WriteString(s)
	o.w.WriteByte('\n')
	o.w.Flush()
}

func newFlushWriter(w io.Writer) *bufio.Writer { return bufio.NewWriterSize(w, 64<<10) }

func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

func cmdServe(a args, env Env) error {
	start := time.Now()
	db, err := env.Open(a.pos[0], a.maxBytes)
	if err != nil {
		return err
	}
	reportRecovery(env.Stderr, db.Recovery())
	st := db.Stats()
	fmt.Fprintf(env.Stderr, "serve: ready, %d keys, recovery %.3f ms, max-bytes %d\n",
		st.Keys, float64(time.Since(start).Nanoseconds())/1e6, a.maxBytes)

	out := &output{w: newFlushWriter(env.Stdout)}
	queues := map[string]chan *request{}
	var wg sync.WaitGroup
	enqueue := func(r *request) {
		q, ok := queues[r.writer]
		if !ok {
			q = make(chan *request, 256)
			queues[r.writer] = q
			wg.Add(1)
			go func() {
				defer wg.Done()
				for r := range q {
					execute(db, r, out)
				}
			}()
		}
		q <- r
	}

	lr := newLineReader(env.Stdin)
	lr.requireEOL = true
	var readErr error
	for {
		line, err := lr.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, errLineTooLong) || errors.Is(err, errIncompleteLine) {
			out.line("ERR - protocol " + oneLine(err.Error()))
			continue
		}
		if err != nil {
			readErr = err
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		r := parseRequest(line, lr)
		if r == nil {
			continue
		}
		enqueue(r)
	}
	for _, q := range queues {
		close(q)
	}
	wg.Wait()
	cerr := db.Close()
	if readErr != nil {
		return fmt.Errorf("reading stdin: %w", readErr)
	}
	if cerr != nil {
		return fmt.Errorf("closing: %w", cerr)
	}
	return nil
}

// parseRequest parses one request line, reading the body lines of a batch
// from lr. A malformed request is returned with err set, so the error is
// reported in the writer's order; a line too broken to name an op ID is
// answered immediately with "ERR - protocol ..." by returning a request for
// writer "-".
func parseRequest(line string, lr *lineReader) *request {
	f1, rest1 := cutWord(line)
	f2, rest2 := cutWord(rest1)
	f3, rest3 := cutWord(rest2)
	r := &request{}
	switch {
	case serveCommands[strings.ToLower(f3)]:
		r.writer, r.id, r.verb = f1, f2, strings.ToLower(f3)
	case serveCommands[strings.ToLower(f2)]:
		r.writer, r.id, r.verb = "-", f1, strings.ToLower(f2)
		rest3 = rest2 // the arguments follow the verb
	default:
		id := "-"
		if f2 != "" {
			id = f2
		}
		return &request{writer: "-", id: id, err: fmt.Errorf("protocol: cannot parse %q (want \"<writer-id> <op-id> <put|del|get|batch|compact> ...\")", truncate(line, 80))}
	}
	switch r.verb {
	case "put":
		key, value := cutWord(rest3)
		if key == "" {
			r.err = fmt.Errorf("%w: put needs a key", kvdb.ErrInvalidArgument)
			break
		}
		r.key, r.value = []byte(key), []byte(value)
	case "del", "delete", "get":
		key, extra := cutWord(rest3)
		if key == "" || strings.TrimSpace(extra) != "" {
			r.err = fmt.Errorf("%w: %s needs exactly one key", kvdb.ErrInvalidArgument, r.verb)
			break
		}
		r.key = []byte(key)
	case "compact":
		if strings.TrimSpace(rest3) != "" {
			r.err = fmt.Errorf("%w: compact takes no arguments", kvdb.ErrInvalidArgument)
		}
	case "batch":
		ns := strings.TrimSpace(rest3)
		n, err := strconv.Atoi(ns)
		if err != nil || n < 0 || n > kvdb.MaxBatchOps {
			// The count is the only framing: without it the body cannot be
			// skipped. Following lines are parsed as requests of their own.
			r.err = fmt.Errorf("protocol: batch count %q must be 0..%d", ns, kvdb.MaxBatchOps)
			break
		}
		r.ops = make([]kvdb.Op, 0, n)
		var bodyErr error
		for i := 0; i < n; i++ {
			l, err := lr.next()
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, errIncompleteLine) {
					// The request is incomplete: never execute it.
					r.err = fmt.Errorf("protocol: input ended after %d of %d batch lines", i, n)
					return r
				}
				if bodyErr == nil {
					bodyErr = fmt.Errorf("batch line %d: %w", i+1, err)
				}
				continue
			}
			op, err := parseOp(l)
			if err != nil && bodyErr == nil {
				bodyErr = fmt.Errorf("batch line %d: %w", i+1, err)
			}
			r.ops = append(r.ops, op)
		}
		if bodyErr != nil {
			if !errors.Is(bodyErr, kvdb.ErrInvalidArgument) && !errors.Is(bodyErr, kvdb.ErrTooLarge) {
				bodyErr = fmt.Errorf("%w: %v", kvdb.ErrInvalidArgument, bodyErr)
			}
			r.err, r.ops = bodyErr, nil
		}
	}
	return r
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// execute runs one request and writes its reply. ACK is written only after
// the engine call returned nil, i.e. after the frame was synced
// (kvdb.DB.Put/Delete/Batch/Compact return only after Sync succeeded).
func execute(db Store, r *request, out *output) {
	if r.err != nil {
		class := ErrorClass(r.err)
		if strings.HasPrefix(r.err.Error(), "protocol:") {
			class = "protocol"
		}
		out.line(fmt.Sprintf("ERR %s %s %s", r.id, class, oneLine(strings.TrimPrefix(r.err.Error(), "protocol: "))))
		return
	}
	var err error
	switch r.verb {
	case "put":
		err = db.Put(r.key, r.value)
	case "del", "delete":
		err = db.Delete(r.key)
	case "batch":
		err = db.Batch(r.ops)
	case "compact":
		err = db.Compact()
	case "get":
		v, gerr := db.Get(r.key)
		switch {
		case gerr == nil:
			out.line("VALUE " + r.id + " " + formatValue(v))
		case errors.Is(gerr, kvdb.ErrNotFound):
			out.line("NOTFOUND " + r.id)
		default:
			out.line(fmt.Sprintf("ERR %s %s %s", r.id, ErrorClass(gerr), oneLine(gerr.Error())))
		}
		return
	}
	if err != nil {
		out.line(fmt.Sprintf("ERR %s %s %s", r.id, ErrorClass(err), oneLine(err.Error())))
		return
	}
	out.line("ACK " + r.id)
}
