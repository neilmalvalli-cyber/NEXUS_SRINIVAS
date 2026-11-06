// Package faultfs wraps a fileio.FS to inject failures, simulate process
// kills and record every operation in order (DESIGN §20.1, §20.2).
//
// It is a separate package so that only tests import it: fault-injection and
// crash-point code is never linked into the release binary (DESIGN §20.2).
//
// Supported faults, per operation (or any operation) and optionally per path:
//
//   - FailBefore: fail before the operation happens (the OS is never called);
//   - FailAfter: take effect, then report failure (outcome unknown to the
//     caller, e.g. a move or create that happened although an error came
//     back);
//   - Partial: only the first Keep bytes are written/read, then Err (for
//     writes, Err == nil simulates a WriterAt that breaks its contract; for
//     reads, io.EOF simulates a short file, any other error a read failure);
//   - Crash / CrashAfter: simulate the process being killed just before /
//     just after the operation. The call panics with Crashed and, like the
//     kernel does for a dead process, every handle and lock opened through
//     this FS is closed. RunUntilCrash catches the panic.
package faultfs

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
)

// Op names a filesystem operation.
type Op string

const (
	OpAny      Op = "" // in a Rule: matches every operation
	OpStat     Op = "stat"
	OpOpen     Op = "open"
	OpOpenRW   Op = "openrw"
	OpCreate   Op = "create"
	OpRemove   Op = "remove"
	OpMove     Op = "move"
	OpSyncDir  Op = "syncdir"
	OpLock     Op = "lock"
	OpUnlock   Op = "unlock"
	OpRead     Op = "read"
	OpWrite    Op = "write"
	OpSync     Op = "sync"
	OpTruncate Op = "truncate"
	OpSize     Op = "size"
	OpClose    Op = "close"
)

// Mode says how a matching rule affects the operation.
type Mode int

const (
	FailBefore Mode = iota
	FailAfter
	Partial
	Crash
	CrashAfter
)

// Crashed is the panic value used by Crash and CrashAfter.
type Crashed struct{ Event Event }

func (c Crashed) String() string { return "faultfs: simulated crash at " + c.Event.String() }

// RunUntilCrash calls fn and reports whether it was stopped by a simulated
// crash. Any other panic is re-raised.
func RunUntilCrash(fn func()) (crashed bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(Crashed); ok {
				crashed = true
				return
			}
			panic(r)
		}
	}()
	fn()
	return false
}

// Rule describes one fault.
type Rule struct {
	Op Op // OpAny matches every operation
	// Path restricts the rule to one file (compared after filepath.Clean).
	// Empty matches every path.
	Path string
	// Skip lets this many matching calls through before the rule fires.
	Skip int
	// Count is how many times the rule fires; 0 means every time after Skip.
	Count int
	Mode  Mode
	Err   error
	// Keep is the number of bytes transferred in Partial mode, and before a
	// Crash in the middle of a read or write.
	Keep int

	seen, fired int
}

func (r Rule) String() string {
	return fmt.Sprintf("%q %s skip=%d count=%d mode=%d err=%v", r.Op, r.Path, r.Skip, r.Count, r.Mode, r.Err)
}

// Event is one recorded operation.
type Event struct {
	Op     Op
	Path   string
	Path2  string // destination of a move
	Off    int64
	Len    int // bytes requested
	N      int // bytes transferred
	Err    error
	Faulty bool // a rule fired
}

func (e Event) String() string {
	s := string(e.Op) + " " + filepath.Base(e.Path)
	if e.Path2 != "" {
		s += " -> " + filepath.Base(e.Path2)
	}
	if e.Err != nil {
		s += " !"
	}
	return s
}

// FS is a fault-injecting, recording fileio.FS.
type FS struct {
	Base fileio.FS

	mu      sync.Mutex
	rules   []*Rule
	events  []Event
	handles map[io.Closer]struct{} // open files and locks, closed on a crash
}

var _ fileio.FS = (*FS)(nil)

// New wraps base.
func New(base fileio.FS) *FS { return &FS{Base: base, handles: map[io.Closer]struct{}{}} }

// Add installs a rule. Rules are checked in the order they were added; the
// first one that fires wins.
func (f *FS) Add(r Rule) *FS {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.Path = clean(r.Path)
	f.rules = append(f.rules, &r)
	return f
}

// ClearRules removes all rules (the event log is kept).
func (f *FS) ClearRules() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = nil
}

// Events returns a copy of the recorded operations.
func (f *FS) Events() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Event(nil), f.events...)
}

// OpCount returns the number of recorded events. Crash-point sweeps run a
// scenario once without faults to count its operations, then once per index
// with Rule{Op: OpAny, Skip: i, Count: 1, Mode: Crash}.
func (f *FS) OpCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// OpenHandles returns how many files and locks opened through f are still
// open (used to detect leaks).
func (f *FS) OpenHandles() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.handles)
}

// Trace returns the events as short strings ("lock db.kv.lock", "move a -> b").
func (f *FS) Trace() []string {
	var out []string
	for _, e := range f.Events() {
		out = append(out, e.String())
	}
	return out
}

// TraceString joins Trace with newlines, for test failure messages.
func (f *FS) TraceString() string { return strings.Join(f.Trace(), "\n") }

func clean(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

func (f *FS) match(op Op, path string) *Rule {
	f.mu.Lock()
	defer f.mu.Unlock()
	path = clean(path)
	for _, r := range f.rules {
		if (r.Op != OpAny && r.Op != op) || (r.Path != "" && r.Path != path) {
			continue
		}
		r.seen++
		if r.seen <= r.Skip {
			continue
		}
		if r.Count > 0 && r.fired >= r.Count {
			continue
		}
		r.fired++
		return r
	}
	return nil
}

func (f *FS) record(e Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *FS) track(c io.Closer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handles[c] = struct{}{}
}

func (f *FS) untrack(c io.Closer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.handles, c)
}

// crash records e, closes every tracked handle (as the kernel does for a
// killed process) and panics.
func (f *FS) crash(e Event) {
	e.Faulty = true
	f.record(e)
	f.mu.Lock()
	hs := f.handles
	f.handles = map[io.Closer]struct{}{}
	f.mu.Unlock()
	for c := range hs {
		c.Close()
	}
	panic(Crashed{Event: e})
}

// do runs an operation that transfers no data under the matching rule.
func (f *FS) do(op Op, path, path2 string, fn func() error) error {
	r := f.match(op, path)
	ev := Event{Op: op, Path: path, Path2: path2, Faulty: r != nil}
	if r != nil {
		switch r.Mode {
		case Crash:
			f.crash(ev)
		case CrashAfter:
			ev.Err = fn()
			f.crash(ev)
		case FailAfter, Partial:
			// Partial transfers no data here, so it behaves like FailAfter:
			// the operation happens, then r.Err (possibly nil) is returned.
			if err := fn(); err != nil {
				ev.Err = err
			} else {
				ev.Err = r.Err
			}
			f.record(ev)
			return ev.Err
		default: // FailBefore: the operation never happens
			if r.Err == nil {
				panic("faultfs: FailBefore rule without Err on " + string(op))
			}
			ev.Err = r.Err
			f.record(ev)
			return ev.Err
		}
	}
	ev.Err = fn()
	f.record(ev)
	return ev.Err
}

func (f *FS) Stat(name string) (fs.FileInfo, error) {
	var fi fs.FileInfo
	err := f.do(OpStat, name, "", func() (e error) { fi, e = f.Base.Stat(name); return })
	if err != nil {
		return nil, err
	}
	return fi, nil
}

func (f *FS) openWith(op Op, name string, open func(string) (fileio.File, error)) (fileio.File, error) {
	var file fileio.File
	err := f.do(op, name, "", func() (e error) {
		file, e = open(name)
		if e == nil {
			f.track(file)
		}
		return e
	})
	if err != nil {
		// FailAfter: the file was opened/created but the caller is told it
		// failed. Close our handle so it does not leak; the file stays.
		if file != nil {
			f.untrack(file)
			file.Close()
		}
		return nil, err
	}
	return &File{fs: f, f: file, name: name}, nil
}

func (f *FS) Open(name string) (fileio.File, error) { return f.openWith(OpOpen, name, f.Base.Open) }

func (f *FS) OpenRW(name string) (fileio.File, error) {
	return f.openWith(OpOpenRW, name, f.Base.OpenRW)
}

func (f *FS) CreateExclusive(name string) (fileio.File, error) {
	return f.openWith(OpCreate, name, f.Base.CreateExclusive)
}

func (f *FS) Remove(name string) error {
	return f.do(OpRemove, name, "", func() error { return f.Base.Remove(name) })
}

func (f *FS) MoveNoReplace(from, to string) error {
	return f.do(OpMove, from, to, func() error { return f.Base.MoveNoReplace(from, to) })
}

func (f *FS) SyncDir(dir string) error {
	return f.do(OpSyncDir, dir, "", func() error { return f.Base.SyncDir(dir) })
}

func (f *FS) Lock(name string) (io.Closer, error) {
	var c io.Closer
	err := f.do(OpLock, name, "", func() (e error) {
		c, e = f.Base.Lock(name)
		if e == nil {
			f.track(c)
		}
		return e
	})
	if err != nil {
		if c != nil {
			f.untrack(c)
			c.Close()
		}
		return nil, err
	}
	return &lockHandle{fs: f, c: c, name: name}, nil
}

type lockHandle struct {
	fs   *FS
	c    io.Closer
	name string
}

// Close always releases the real lock, even when a rule makes it report an
// error (an OS close that fails still releases the handle).
func (l *lockHandle) Close() error {
	return l.fs.closeTracked(OpUnlock, l.name, l.c)
}

// closeTracked implements Close for files and locks.
func (f *FS) closeTracked(op Op, name string, c io.Closer) error {
	r := f.match(op, name)
	ev := Event{Op: op, Path: name, Faulty: r != nil}
	if r != nil && r.Mode == Crash {
		f.crash(ev) // closes c with everything else
	}
	f.untrack(c)
	ev.Err = c.Close()
	if r != nil {
		if r.Mode == CrashAfter {
			f.crash(ev)
		}
		if ev.Err == nil {
			ev.Err = r.Err
		}
	}
	f.record(ev)
	return ev.Err
}

// File is a fault-injecting, recording fileio.File.
type File struct {
	fs   *FS
	f    fileio.File
	name string
}

func (h *File) Name() string { return h.f.Name() }

func (h *File) transfer(op Op, p []byte, off int64, io func([]byte, int64) (int, error)) (int, error) {
	r := h.fs.match(op, h.name)
	ev := Event{Op: op, Path: h.name, Off: off, Len: len(p), Faulty: r != nil}
	if r == nil {
		ev.N, ev.Err = io(p, off)
		h.fs.record(ev)
		return ev.N, ev.Err
	}
	switch r.Mode {
	case Partial:
		ev.N, ev.Err = io(p[:min(r.Keep, len(p))], off)
		if ev.Err == nil {
			ev.Err = r.Err // may be nil: a contract-breaking short transfer
		}
	case FailAfter:
		ev.N, ev.Err = io(p, off)
		if ev.Err == nil {
			ev.Err = r.Err
		}
	case Crash:
		if r.Keep > 0 {
			ev.N, ev.Err = io(p[:min(r.Keep, len(p))], off)
		}
		h.fs.crash(ev)
	case CrashAfter:
		ev.N, ev.Err = io(p, off)
		h.fs.crash(ev)
	default:
		ev.Err = r.Err
	}
	h.fs.record(ev)
	return ev.N, ev.Err
}

func (h *File) ReadAt(p []byte, off int64) (int, error) {
	return h.transfer(OpRead, p, off, h.f.ReadAt)
}

func (h *File) WriteAt(p []byte, off int64) (int, error) {
	return h.transfer(OpWrite, p, off, h.f.WriteAt)
}

func (h *File) Sync() error { return h.fs.do(OpSync, h.name, "", h.f.Sync) }

func (h *File) Truncate(size int64) error {
	return h.fs.do(OpTruncate, h.name, "", func() error { return h.f.Truncate(size) })
}

func (h *File) Size() (int64, error) {
	var sz int64
	err := h.fs.do(OpSize, h.name, "", func() (e error) { sz, e = h.f.Size(); return })
	return sz, err
}

// Close always releases the real handle, even when a rule makes it report an
// error: an OS close that fails still releases the handle, and a leaked
// handle on Windows would block later moves and deletes in the test.
func (h *File) Close() error { return h.fs.closeTracked(OpClose, h.name, h.f) }
