package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

// Exit codes.
const (
	ExitOK      = 0 // success (also: get of a missing key, which prints NOTFOUND)
	ExitError   = 1 // the operation failed and changed nothing (definite)
	ExitUsage   = 2 // bad command line
	ExitUnknown = 3 // ErrOutcomeUnknown: the write may or may not be durable
)

// Usage is printed for -h, --help and command-line errors.
const Usage = `usage:
  kvdb open  <path> [--max-bytes N]        open or create, run recovery, print key count and recovery time
  kvdb put   <path> <key> <value> [--max-bytes N]
  kvdb get   <path> <key>                  print the value, or NOTFOUND
  kvdb del   <path> <key> [--max-bytes N]  deleting a missing key is not an error
  kvdb batch <path> <file> [--max-bytes N] one op per line: "put k v" or "del k"; applied atomically
  kvdb dump  <path>                        every key and value, sorted by key
  kvdb check <path>                        verify the whole file; print OK or the reason
  kvdb serve <path> [--max-bytes N]        read ops from stdin; print "ACK <op-id>" after each durable write

serve input, one per line ("<writer-id> " may be omitted):
  <writer-id> <op-id> put <key> <value>
  <writer-id> <op-id> del <key>
  <writer-id> <op-id> get <key>
  <writer-id> <op-id> batch <n>     followed by n lines "put k v" / "del k"
  <writer-id> <op-id> compact
serve output: ACK <op-id> | VALUE <op-id> <value> | NOTFOUND <op-id> | ERR <op-id> <class> <message>

--max-bytes N caps the data file at N bytes for this run (0 = unlimited).
exit status: 0 ok, 1 failed (nothing changed), 2 usage, 3 outcome unknown.
`

// Env is the process environment of one CLI invocation (injectable for tests).
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Open opens a database; nil means kvdb.Open.
	Open func(path string, maxBytes int64) (Store, error)
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

// args is a parsed command line.
type args struct {
	pos      []string
	maxBytes int64
	hasMax   bool
}

// parseArgs separates positional arguments from the one flag, --max-bytes,
// which may appear anywhere ("--max-bytes N" or "--max-bytes=N"). "--" ends
// flag parsing, so a key that starts with "--" can still be given.
func parseArgs(in []string) (args, error) {
	var a args
	for i := 0; i < len(in); i++ {
		s := in[i]
		switch {
		case s == "--":
			a.pos = append(a.pos, in[i+1:]...)
			return a, nil
		case s == "--max-bytes" || s == "-max-bytes":
			if i+1 >= len(in) {
				return a, usagef("--max-bytes needs a value")
			}
			i++
			s = "--max-bytes=" + in[i]
			fallthrough
		case strings.HasPrefix(s, "--max-bytes=") || strings.HasPrefix(s, "-max-bytes="):
			v := s[strings.IndexByte(s, '=')+1:]
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return a, usagef("--max-bytes: %q is not a non-negative integer", v)
			}
			a.maxBytes, a.hasMax = n, true
		case s == "-h" || s == "--help":
			return a, usagef("")
		case strings.HasPrefix(s, "--"):
			return a, usagef("unknown flag %s", s)
		default:
			a.pos = append(a.pos, s)
		}
	}
	return a, nil
}

// Run executes one kvdb command and returns the process exit code.
func Run(argv []string, env Env) int {
	if env.Open == nil {
		env.Open = func(p string, n int64) (Store, error) {
			db, err := kvdb.Open(p, n)
			if err != nil {
				return nil, err // never a non-nil interface holding a nil *DB
			}
			return db, nil
		}
	}
	if len(argv) == 0 {
		fmt.Fprint(env.Stderr, Usage)
		return ExitUsage
	}
	cmd := argv[0]
	a, err := parseArgs(argv[1:])
	if err == nil {
		err = dispatch(cmd, a, env)
	}
	var ue usageError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &ue):
		if ue.msg != "" {
			fmt.Fprintf(env.Stderr, "kvdb %s: %s\n", cmd, ue.msg)
		}
		fmt.Fprint(env.Stderr, Usage)
		return ExitUsage
	case errors.Is(err, kvdb.ErrOutcomeUnknown):
		fmt.Fprintf(env.Stderr, "kvdb %s: OUTCOME UNKNOWN (%s): %v\n", cmd, ErrorClass(err), err)
		return ExitUnknown
	default:
		fmt.Fprintf(env.Stderr, "kvdb %s: ERROR (%s): %v\n", cmd, ErrorClass(err), err)
		return ExitError
	}
}

func dispatch(cmd string, a args, env Env) error {
	need := func(n int, form string) error {
		if len(a.pos) != n {
			return usagef("expected %s, got %d argument(s)", form, len(a.pos))
		}
		return nil
	}
	noMax := func() error {
		if a.hasMax {
			return usagef("--max-bytes is not used by %s", cmd)
		}
		return nil
	}
	switch cmd {
	case "open":
		if err := need(1, "<path>"); err != nil {
			return err
		}
		return cmdOpen(a, env)
	case "put":
		if err := need(3, "<path> <key> <value>"); err != nil {
			return err
		}
		return withDB(a, env, func(db Store) error {
			if err := db.Put([]byte(a.pos[1]), []byte(a.pos[2])); err != nil {
				return err
			}
			fmt.Fprintln(env.Stdout, "OK")
			return nil
		})
	case "get":
		if err := need(2, "<path> <key>"); err != nil {
			return err
		}
		if err := noMax(); err != nil {
			return err
		}
		return withDB(a, env, func(db Store) error {
			v, err := db.Get([]byte(a.pos[1]))
			if errors.Is(err, kvdb.ErrNotFound) {
				fmt.Fprintln(env.Stdout, "NOTFOUND")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(env.Stdout, formatValue(v))
			return nil
		})
	case "del", "delete":
		if err := need(2, "<path> <key>"); err != nil {
			return err
		}
		return withDB(a, env, func(db Store) error {
			if err := db.Delete([]byte(a.pos[1])); err != nil {
				return err
			}
			fmt.Fprintln(env.Stdout, "OK")
			return nil
		})
	case "batch":
		if err := need(2, "<path> <file>"); err != nil {
			return err
		}
		return cmdBatch(a, env)
	case "dump":
		if err := need(1, "<path>"); err != nil {
			return err
		}
		if err := noMax(); err != nil {
			return err
		}
		return cmdDump(a, env)
	case "check":
		if err := need(1, "<path>"); err != nil {
			return err
		}
		if err := noMax(); err != nil {
			return err
		}
		return cmdCheck(a, env)
	case "serve":
		if err := need(1, "<path>"); err != nil {
			return err
		}
		return cmdServe(a, env)
	case "help", "-h", "--help":
		fmt.Fprint(env.Stdout, Usage)
		return nil
	default:
		return usagef("unknown command %q", cmd)
	}
}

// withDB opens the database, runs fn and closes it. A Close error is
// reported even when fn succeeded (it never affects durability: every
// acknowledged write was synced before fn returned).
func withDB(a args, env Env, fn func(Store) error) error {
	db, err := env.Open(a.pos[0], a.maxBytes)
	if err != nil {
		return err
	}
	reportRecovery(env.Stderr, db.Recovery())
	ferr := fn(db)
	cerr := db.Close()
	if ferr != nil {
		return ferr
	}
	if cerr != nil {
		return fmt.Errorf("closing: %w", cerr)
	}
	return nil
}

// reportRecovery prints, on stderr, anything recovery did beyond replaying a
// clean log.
func reportRecovery(w io.Writer, ri kvdb.RecoveryInfo) {
	if ri.Created {
		fmt.Fprintln(w, "recovery: created a new empty database")
	}
	for _, r := range ri.Rules {
		if r != "A1" && r != "A7" {
			fmt.Fprintf(w, "recovery: authority rule %s applied (interrupted creation or compaction completed)\n", r)
		}
	}
	if ri.Tail != "" {
		fmt.Fprintf(w, "recovery: discarded a torn %d-byte tail (rule %s); it was never acknowledged\n", ri.DiscardedBytes, ri.Tail)
	}
	for _, s := range ri.Warnings {
		fmt.Fprintf(w, "recovery: warning: %s\n", s)
	}
}

func cmdOpen(a args, env Env) error {
	start := time.Now()
	db, err := env.Open(a.pos[0], a.maxBytes)
	elapsed := time.Since(start)
	if err != nil {
		return err
	}
	reportRecovery(env.Stderr, db.Recovery())
	st := db.Stats()
	if err := db.Close(); err != nil {
		return fmt.Errorf("closing: %w", err)
	}
	fmt.Fprintf(env.Stdout, "keys=%d recovery_ms=%.3f size=%d\n", st.Keys, float64(elapsed.Nanoseconds())/1e6, st.Size)
	return nil
}

func cmdBatch(a args, env Env) error {
	f, err := os.Open(a.pos[1])
	if err != nil {
		return fmt.Errorf("%w: batch file: %v", kvdb.ErrInvalidArgument, err)
	}
	ops, err := readBatchFile(f)
	f.Close()
	if err != nil {
		if !errors.Is(err, kvdb.ErrInvalidArgument) && !errors.Is(err, kvdb.ErrTooLarge) {
			err = fmt.Errorf("%w: batch file %s: %v", kvdb.ErrInvalidArgument, a.pos[1], err)
		}
		return err
	}
	return withDB(a, env, func(db Store) error {
		if err := db.Batch(ops); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "OK %d ops\n", len(ops))
		return nil
	})
}

func cmdDump(a args, env Env) error {
	n := 0
	err := withDB(a, env, func(db Store) error {
		w := newFlushWriter(env.Stdout)
		err := db.Range(func(k, v []byte) error {
			n++
			_, err := w.WriteString(formatPair(k, v) + "\n")
			return err
		})
		if ferr := w.Flush(); err == nil {
			err = ferr
		}
		return err
	})
	if err == nil {
		fmt.Fprintf(env.Stderr, "dump: %d keys\n", n)
	}
	return err
}

func cmdCheck(a args, env Env) error {
	db, err := env.Open(a.pos[0], 0)
	if err != nil {
		fmt.Fprintf(env.Stdout, "%s: %v\n", checkReason(err), err)
		return err
	}
	reportRecovery(env.Stderr, db.Recovery())
	rep, verr := db.Verify()
	cerr := db.Close()
	if verr != nil {
		fmt.Fprintf(env.Stdout, "%s: %v\n", checkReason(verr), verr)
		return verr
	}
	if cerr != nil {
		fmt.Fprintf(env.Stdout, "ERROR: closing: %v\n", cerr)
		return cerr
	}
	fmt.Fprintln(env.Stdout, "OK")
	fmt.Fprintf(env.Stderr, "check: %d keys, %d frames, %d bytes, %d live bytes\n", rep.Keys, rep.Frames, rep.Size, rep.LiveBytes)
	return nil
}

func checkReason(err error) string {
	switch {
	case errors.Is(err, kvdb.ErrCorruption):
		return "CORRUPT"
	case errors.Is(err, kvdb.ErrBadFormat):
		return "BAD FORMAT"
	case errors.Is(err, kvdb.ErrLocked):
		return "LOCKED"
	default:
		return "ERROR"
	}
}

// ErrorClass maps an engine error to the short class name used in serve
// replies and CLI messages. Only "unknown" means the write may have become
// durable; every other class is definite (nothing changed), except "io",
// which callers should also treat as unknown to be safe.
func ErrorClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, kvdb.ErrOutcomeUnknown):
		return "unknown"
	case errors.Is(err, kvdb.ErrFull):
		return "full"
	case errors.Is(err, kvdb.ErrTooLarge):
		return "too-large"
	case errors.Is(err, kvdb.ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, kvdb.ErrFailed):
		return "failed"
	case errors.Is(err, kvdb.ErrClosed):
		return "closed"
	case errors.Is(err, kvdb.ErrLocked):
		return "locked"
	case errors.Is(err, kvdb.ErrCorruption):
		return "corruption"
	case errors.Is(err, kvdb.ErrBadFormat):
		return "bad-format"
	case errors.Is(err, kvdb.ErrNotFound):
		return "notfound"
	default:
		return "io"
	}
}

// Store is the part of *kvdb.DB the commands use. It exists so tests can
// substitute a store that fails in specific ways (for example a write whose
// outcome is unknown) to prove serve never acknowledges such a write.
type Store interface {
	Put(key, value []byte) error
	Get(key []byte) ([]byte, error)
	Delete(key []byte) error
	Batch(ops []kvdb.Op) error
	Compact() error
	Close() error
	Stats() kvdb.Stats
	Recovery() kvdb.RecoveryInfo
	Range(fn func(key, value []byte) error) error
	Verify() (kvdb.VerifyReport, error)
}

var _ Store = (*kvdb.DB)(nil)
