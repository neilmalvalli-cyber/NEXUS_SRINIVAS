// Package powercut is the NEXUS Power-Cut Explorer: a reliability-testing
// module that records the exact bytes and synchronization events the real
// engine issues, generates modeled power-loss disk images from that trace,
// and validates every image with the real NEXUS recovery (kvdb.Open, then
// Verify and an oracle check against what was acknowledged).
//
// It is separate from the engine and from the torture framework: it never
// changes how the engine writes. The engine runs unmodified on top of a
// recording fileio.FS that passes every call through to the real OS.
//
// The images are MODELED: they are what the storage failure model in
// docs/POWER_CUT_EXPLORER.md says a disk may contain after power loss at a
// given moment. A run tests a finite, seeded sample of those states. It is
// not an exhaustive proof that NEXUS survives every possible power loss.
package powercut

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sync"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
)

// Op names a recorded filesystem operation.
type Op string

const (
	OpCreate   Op = "create"   // CreateExclusive: new empty inode, directory entry pending
	OpWrite    Op = "write"    // WriteAt: bytes at an offset of an inode (unsynced)
	OpTruncate Op = "truncate" // Truncate: size change of an inode (unsynced)
	OpSync     Op = "sync"     // Sync (fsync / FlushFileBuffers) of an inode
	OpMove     Op = "move"     // MoveNoReplace: directory entry change (pending)
	OpRemove   Op = "remove"   // Remove: directory entry removal (pending)
	OpSyncDir  Op = "syncdir"  // SyncDir: all pending directory changes durable
)

// Event is one recorded operation. Only operations that succeeded are
// recorded; a failed call changed nothing on disk.
type Event struct {
	Index int    `json:"i"`
	Op    Op     `json:"op"`
	Name  string `json:"name,omitempty"` // base name in the database directory
	Inode int    `json:"inode,omitempty"`
	Off   int64  `json:"off,omitempty"`
	Len   int    `json:"len,omitempty"`
	Size  int64  `json:"size,omitempty"` // truncate
	To    string `json:"to,omitempty"`   // move
	Data  []byte `json:"-"`              // write: an exact copy of the bytes written
}

// Recorder is a fileio.FS that passes everything to fileio.OS and records
// every mutating operation in order. It is safe for concurrent use; the
// order of Events is the order in which the OS calls completed.
type Recorder struct {
	os fileio.OS

	mu        sync.Mutex
	events    []Event
	names     map[string]int // current namespace: base name -> inode
	nextInode int
	bytes     int64
}

// NewRecorder returns a recorder for a database in an empty directory.
func NewRecorder() *Recorder { return &Recorder{names: map[string]int{}} }

// Mark returns the number of events recorded so far. A workload calls it
// right before issuing an operation and right after it is acknowledged; a
// crash "at point k" means events [0, k) happened.
func (r *Recorder) Mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// Events returns the recorded trace.
func (r *Recorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

// BytesWritten is the total number of bytes recorded in write events.
func (r *Recorder) BytesWritten() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bytes
}

func (r *Recorder) add(e Event) {
	e.Index = len(r.events)
	r.events = append(r.events, e)
}

func base(name string) string { return filepath.Base(name) }

func (r *Recorder) Stat(name string) (fs.FileInfo, error) { return r.os.Stat(name) }

func (r *Recorder) Open(name string) (fileio.File, error) {
	f, err := r.os.Open(name)
	if err != nil {
		return nil, err
	}
	return &recFile{File: f, r: r, inode: r.inodeOf(name), readOnly: true}, nil
}

func (r *Recorder) OpenRW(name string) (fileio.File, error) {
	f, err := r.os.OpenRW(name)
	if err != nil {
		return nil, err
	}
	return &recFile{File: f, r: r, inode: r.inodeOf(name)}, nil
}

func (r *Recorder) inodeOf(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ino, ok := r.names[base(name)]; ok {
		return ino
	}
	// A file that existed before recording began; the explorer always
	// starts from an empty directory, so this is a harness error.
	panic(fmt.Sprintf("powercut: %s was not created under the recorder", name))
}

func (r *Recorder) CreateExclusive(name string) (fileio.File, error) {
	f, err := r.os.CreateExclusive(name)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.nextInode++
	ino := r.nextInode
	r.names[base(name)] = ino
	r.add(Event{Op: OpCreate, Name: base(name), Inode: ino})
	r.mu.Unlock()
	return &recFile{File: f, r: r, inode: ino}, nil
}

func (r *Recorder) Remove(name string) error {
	if err := r.os.Remove(name); err != nil {
		return err
	}
	r.mu.Lock()
	ino := r.names[base(name)]
	delete(r.names, base(name))
	r.add(Event{Op: OpRemove, Name: base(name), Inode: ino})
	r.mu.Unlock()
	return nil
}

func (r *Recorder) MoveNoReplace(from, to string) error {
	if err := r.os.MoveNoReplace(from, to); err != nil {
		return err
	}
	r.mu.Lock()
	ino := r.names[base(from)]
	delete(r.names, base(from))
	r.names[base(to)] = ino
	r.add(Event{Op: OpMove, Name: base(from), To: base(to), Inode: ino})
	r.mu.Unlock()
	return nil
}

func (r *Recorder) SyncDir(dir string) error {
	if err := r.os.SyncDir(dir); err != nil {
		return err
	}
	r.mu.Lock()
	r.add(Event{Op: OpSyncDir})
	r.mu.Unlock()
	return nil
}

// Lock is passed through and not recorded: the lock file holds no data and
// recovery never reads it.
func (r *Recorder) Lock(name string) (io.Closer, error) { return r.os.Lock(name) }

type recFile struct {
	fileio.File
	r        *Recorder
	inode    int
	readOnly bool
}

func (f *recFile) WriteAt(p []byte, off int64) (int, error) {
	n, err := f.File.WriteAt(p, off)
	if n > 0 {
		f.r.mu.Lock()
		f.r.add(Event{Op: OpWrite, Inode: f.inode, Off: off, Len: n, Data: append([]byte(nil), p[:n]...)})
		f.r.bytes += int64(n)
		f.r.mu.Unlock()
	}
	return n, err
}

func (f *recFile) Truncate(size int64) error {
	if err := f.File.Truncate(size); err != nil {
		return err
	}
	f.r.mu.Lock()
	f.r.add(Event{Op: OpTruncate, Inode: f.inode, Size: size})
	f.r.mu.Unlock()
	return nil
}

func (f *recFile) Sync() error {
	if err := f.File.Sync(); err != nil {
		return err
	}
	f.r.mu.Lock()
	f.r.add(Event{Op: OpSync, Inode: f.inode})
	f.r.mu.Unlock()
	return nil
}

var _ fileio.FS = (*Recorder)(nil)
