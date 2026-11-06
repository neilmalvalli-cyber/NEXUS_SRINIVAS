// Command kvdb-mutant is a deliberately broken stand-in for kvdb, used only
// to prove that NEXUS Torture Engine 2.0 detects real classes of bugs. It is
// a separate program built on the NEXUS library; the official kvdb binary
// and the engine source are not modified. It speaks the same CLI and serve
// protocol as kvdb. The fault is chosen by the KVDB_MUTANT environment
// variable:
//
//	none             faithful wrapper (control: must pass)
//	ack-before-sync  ACK first, write to disk later in the background
//	drop-acked       every 7th write is acknowledged but never written
//	partial-batch    batches are applied one operation at a time
//	wrong-unknown    every 5th put is written but reported as a definite failure
//	bad-recovery     recovery rolls back the last acknowledged put
//	corrupt-value    every 6th put stores a value with one byte changed
//
// Never use it for anything else.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

var mutation = os.Getenv("KVDB_MUTANT")

var mutations = []string{"none", "ack-before-sync", "drop-acked", "partial-batch", "wrong-unknown", "bad-recovery", "corrupt-value"}

func main() { os.Exit(run(os.Args[1:])) }

func usage() int {
	fmt.Fprintln(os.Stderr, "usage: kvdb-mutant <open|put|get|del|batch|dump|check|serve> <path> ... (KVDB_MUTANT="+strings.Join(mutations, "|")+")")
	return 2
}

func run(args []string) int {
	if len(args) < 2 {
		return usage()
	}
	ok := false
	for _, m := range mutations {
		ok = ok || m == mutation
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "kvdb-mutant: KVDB_MUTANT=%q is not one of %s\n", mutation, strings.Join(mutations, ", "))
		return 2
	}
	cmd, path := args[0], args[1]
	rest := args[2:]
	var maxBytes int64
	noRepair := false
	var pos []string
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--max-bytes":
			if i+1 < len(rest) {
				maxBytes, _ = strconv.ParseInt(rest[i+1], 10, 64)
				i++
			}
		case "--no-repair":
			noRepair = true
		default:
			pos = append(pos, rest[i])
		}
	}
	if cmd == "check" && noRepair {
		c, err := kvdb.CheckReadOnly(path)
		if err != nil {
			fmt.Println("ERROR:", err)
			return 1
		}
		if c.NeedsRepair() {
			fmt.Println("NEEDS REPAIR: " + strings.Join(c.Repairs, "; "))
			return 4
		}
		fmt.Println("OK")
		return 0
	}
	t0 := time.Now()
	db, err := openDB(path, maxBytes)
	if err != nil {
		fmt.Println("ERROR:", err)
		return 1
	}
	defer db.Close()
	switch cmd {
	case "open":
		st := db.Stats()
		fmt.Printf("keys=%d recovery_ms=%.3f size=%d\n", st.Keys, float64(time.Since(t0).Microseconds())/1000, st.Size)
	case "put":
		if len(pos) != 2 {
			return usage()
		}
		return okOrErr(db.Put([]byte(pos[0]), []byte(pos[1])))
	case "del":
		if len(pos) != 1 {
			return usage()
		}
		return okOrErr(db.Delete([]byte(pos[0])))
	case "get":
		if len(pos) != 1 {
			return usage()
		}
		v, err := db.Get([]byte(pos[0]))
		if errors.Is(err, kvdb.ErrNotFound) {
			fmt.Println("NOTFOUND")
			return 0
		}
		if err != nil {
			fmt.Println("ERROR:", err)
			return 1
		}
		fmt.Println(string(v))
	case "batch":
		if len(pos) != 1 {
			return usage()
		}
		b, err := os.ReadFile(pos[0])
		if err != nil {
			fmt.Println("ERROR:", err)
			return 1
		}
		var ops []kvdb.Op
		for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			op, err := parseOp(strings.TrimRight(l, "\r"))
			if err != nil {
				fmt.Println("ERROR:", err)
				return 1
			}
			ops = append(ops, op)
		}
		if err := db.Batch(ops); err != nil {
			fmt.Println("ERROR:", err)
			return 1
		}
		fmt.Printf("OK %d ops\n", len(ops))
	case "dump":
		type kv struct{ k, v string }
		var all []kv
		db.Range(func(k, v []byte) error { all = append(all, kv{string(k), string(v)}); return nil })
		sort.Slice(all, func(i, j int) bool { return all[i].k < all[j].k })
		w := bufio.NewWriterSize(os.Stdout, 1<<20)
		for _, e := range all {
			if plain(e.k) && plainValue(e.v) && !strings.Contains(e.k, " ") {
				fmt.Fprintf(w, "%s %s\n", e.k, e.v)
			} else {
				fmt.Fprintf(w, "%s %s\n", strconv.Quote(e.k), strconv.Quote(e.v))
			}
		}
		w.Flush()
	case "check":
		if _, err := db.Verify(); err != nil {
			fmt.Println("CORRUPT:", err)
			return 1
		}
		fmt.Println("OK")
	case "serve":
		return serve(db, path, maxBytes, t0)
	default:
		return usage()
	}
	return 0
}

// openDB opens the database; the bad-recovery mutant then "recovers" by
// rolling back the last acknowledged put recorded in a sidecar file.
func openDB(path string, maxBytes int64) (*kvdb.DB, error) {
	db, err := kvdb.Open(path, maxBytes)
	if err != nil {
		return nil, err
	}
	if mutation == "bad-recovery" {
		side := path + ".mutant-last"
		if b, err := os.ReadFile(side); err == nil && len(b) > 0 {
			db.Delete(b)
			os.Remove(side)
		}
	}
	return db, nil
}

func okOrErr(err error) int {
	if err != nil {
		fmt.Println("ERROR:", err)
		return 1
	}
	fmt.Println("OK")
	return 0
}

func plain(s string) bool {
	for _, r := range s {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return s != ""
}

func plainValue(s string) bool {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return !strings.HasPrefix(s, `"`)
}

func parseOp(l string) (kvdb.Op, error) {
	f := strings.SplitN(l, " ", 3)
	switch {
	case len(f) == 3 && f[0] == "put":
		return kvdb.Op{Kind: kvdb.OpPut, Key: []byte(f[1]), Value: []byte(f[2])}, nil
	case len(f) == 2 && f[0] == "del":
		return kvdb.Op{Kind: kvdb.OpDelete, Key: []byte(f[1])}, nil
	}
	return kvdb.Op{}, fmt.Errorf("bad op line %q", l)
}

func class(err error) string {
	switch {
	case errors.Is(err, kvdb.ErrFull):
		return "full"
	case errors.Is(err, kvdb.ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, kvdb.ErrTooLarge):
		return "too-large"
	case errors.Is(err, kvdb.ErrOutcomeUnknown):
		return "unknown"
	case errors.Is(err, kvdb.ErrFailed):
		return "failed"
	case errors.Is(err, kvdb.ErrClosed):
		return "closed"
	}
	return "io"
}

// store wraps the DB with the mutation's write path.
type store struct {
	db      *kvdb.DB
	path    string
	mu      sync.Mutex
	writes  int
	puts    int
	pending []kvdb.Op // ack-before-sync: acknowledged, not yet written
	view    map[string]*string
}

func (s *store) get(k string) ([]byte, error) {
	s.mu.Lock()
	if v, ok := s.view[k]; ok {
		s.mu.Unlock()
		if v == nil {
			return nil, kvdb.ErrNotFound
		}
		return []byte(*v), nil
	}
	s.mu.Unlock()
	return s.db.Get([]byte(k))
}

// write applies ops (one request). It returns the reply: "" for ACK, or an
// error class and message.
func (s *store) write(ops []kvdb.Op, isBatch bool) (string, string) {
	s.mu.Lock()
	s.writes++
	n := s.writes
	s.mu.Unlock()
	switch mutation {
	case "ack-before-sync":
		s.mu.Lock()
		s.pending = append(s.pending, ops...)
		for _, op := range ops {
			if op.Kind == kvdb.OpDelete {
				s.view[string(op.Key)] = nil
			} else {
				v := string(op.Value)
				s.view[string(op.Key)] = &v
			}
		}
		s.mu.Unlock()
		return "", ""
	case "drop-acked":
		if n%7 == 0 {
			return "", ""
		}
	case "partial-batch":
		if isBatch {
			for _, op := range ops {
				var err error
				if op.Kind == kvdb.OpDelete {
					err = s.db.Delete(op.Key)
				} else {
					err = s.db.Put(op.Key, op.Value)
				}
				if err != nil {
					return class(err), err.Error()
				}
				time.Sleep(2 * time.Millisecond)
			}
			return "", ""
		}
	case "wrong-unknown":
		if !isBatch && ops[0].Kind == kvdb.OpPut {
			s.mu.Lock()
			s.puts++
			p := s.puts
			s.mu.Unlock()
			if p%5 == 0 {
				if err := s.db.Put(ops[0].Key, ops[0].Value); err != nil {
					return class(err), err.Error()
				}
				return "failed", "simulated failure (but the write was made durable)"
			}
		}
	case "corrupt-value":
		if !isBatch && ops[0].Kind == kvdb.OpPut {
			s.mu.Lock()
			s.puts++
			p := s.puts
			s.mu.Unlock()
			if p%6 == 0 && len(ops[0].Value) > 0 {
				v := append([]byte(nil), ops[0].Value...)
				v[len(v)-1] ^= 0x01
				ops = []kvdb.Op{{Kind: kvdb.OpPut, Key: ops[0].Key, Value: v}}
			}
		}
	}
	var err error
	if isBatch {
		err = s.db.Batch(ops)
	} else if ops[0].Kind == kvdb.OpDelete {
		err = s.db.Delete(ops[0].Key)
	} else {
		err = s.db.Put(ops[0].Key, ops[0].Value)
	}
	if err != nil {
		return class(err), err.Error()
	}
	if mutation == "bad-recovery" && !isBatch && ops[0].Kind == kvdb.OpPut {
		os.WriteFile(s.path+".mutant-last", ops[0].Key, 0o644)
	}
	return "", ""
}

func (s *store) flusher() {
	for {
		time.Sleep(25 * time.Millisecond)
		s.mu.Lock()
		ops := s.pending
		s.pending = nil
		s.mu.Unlock()
		for i := 0; i < len(ops); i += 10000 {
			j := i + 10000
			if j > len(ops) {
				j = len(ops)
			}
			s.db.Batch(ops[i:j])
		}
		s.mu.Lock()
		if len(s.pending) == 0 {
			s.view = map[string]*string{}
		}
		s.mu.Unlock()
	}
}

func serve(db *kvdb.DB, path string, maxBytes int64, t0 time.Time) int {
	s := &store{db: db, path: path, view: map[string]*string{}}
	if mutation == "ack-before-sync" {
		go s.flusher()
	}
	fmt.Fprintf(os.Stderr, "serve: ready, %d keys, recovery %.3f ms, max-bytes %d (mutant %s)\n", db.Stats().Keys, float64(time.Since(t0).Microseconds())/1000, maxBytes, mutation)
	in := bufio.NewReaderSize(os.Stdin, 2<<20)
	out := bufio.NewWriter(os.Stdout)
	reply := func(format string, a ...any) {
		fmt.Fprintf(out, format+"\n", a...)
		out.Flush()
	}
	readLine := func() (string, bool) {
		l, err := in.ReadString('\n')
		if err != nil {
			return "", false // a final line without newline is never executed
		}
		return strings.TrimRight(l, "\r\n"), true
	}
	for {
		line, ok := readLine()
		if !ok {
			return 0
		}
		f := strings.SplitN(line, " ", 5)
		if len(f) < 3 {
			reply("ERR - protocol cannot parse")
			continue
		}
		id, verb, args := f[1], f[2], f[3:]
		switch verb {
		case "put":
			if len(args) != 2 {
				reply("ERR %s protocol bad put", id)
				continue
			}
			if c, m := s.write([]kvdb.Op{{Kind: kvdb.OpPut, Key: []byte(args[0]), Value: []byte(args[1])}}, false); c != "" {
				reply("ERR %s %s %s", id, c, m)
			} else {
				reply("ACK %s", id)
			}
		case "del":
			if c, m := s.write([]kvdb.Op{{Kind: kvdb.OpDelete, Key: []byte(args[0])}}, false); c != "" {
				reply("ERR %s %s %s", id, c, m)
			} else {
				reply("ACK %s", id)
			}
		case "get":
			v, err := s.get(args[0])
			switch {
			case errors.Is(err, kvdb.ErrNotFound):
				reply("NOTFOUND %s", id)
			case err != nil:
				reply("ERR %s %s %v", id, class(err), err)
			case plainValue(string(v)):
				reply("VALUE %s %s", id, v)
			default:
				reply("VALUE %s %s", id, strconv.Quote(string(v)))
			}
		case "batch":
			n, _ := strconv.Atoi(args[0])
			var ops []kvdb.Op
			complete := true
			for i := 0; i < n; i++ {
				l, ok := readLine()
				if !ok {
					complete = false
					break
				}
				op, err := parseOp(l)
				if err != nil {
					complete = false
					break
				}
				ops = append(ops, op)
			}
			if !complete {
				return 0
			}
			if c, m := s.write(ops, true); c != "" {
				reply("ERR %s %s %s", id, c, m)
			} else {
				reply("ACK %s", id)
			}
		case "compact":
			if err := db.Compact(); err != nil {
				reply("ERR %s %s %v", id, class(err), err)
			} else {
				reply("ACK %s", id)
			}
		default:
			reply("ERR %s protocol unknown verb", id)
		}
	}
}
