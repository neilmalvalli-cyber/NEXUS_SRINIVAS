package storage

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/admission"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/index"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/locking"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
)

type handleState int

const (
	stateOpen handleState = iota
	stateFailed
	stateClosed
)

// Config configures OpenWith.
type Config struct {
	Recovery recovery.Options
	// MaxBytes is the storage cap (DESIGN §12); 0 = unlimited. The caller
	// validates it (negative and 1–31 are invalid).
	MaxBytes int64
	// AutoCompact enables the automatic compaction trigger (DESIGN §10.1)
	// after successful appends.
	AutoCompact bool
}

// Log is an open database: the recovered log file, its index and the single
// commit path. All methods are safe for concurrent use: one sync.RWMutex
// (DESIGN §16) serializes writers and lets readers share.
type Log struct {
	mu      sync.RWMutex
	state   handleState
	failErr error // the failure that put the handle in FAILED

	fsys fileio.FS
	path string
	cfg  Config
	lock *locking.Lock

	f       fileio.File // nil only after Close or a failed compaction
	idx     *index.Index
	end     int64  // end of the last committed frame == file size
	nextSeq uint64 // 0: sequence space exhausted

	recovered RecoveryInfo // what Open did (for the CLI; never used by the write path)
}

// Open recovers the database at path with no size cap and automatic
// compaction enabled.
func Open(fsys fileio.FS, path string, opt recovery.Options) (*Log, error) {
	return OpenWith(fsys, path, Config{Recovery: opt, AutoCompact: true})
}

// OpenWith recovers the database at path (recovery.Open: lock, authority,
// scan, tail truncation) and returns a Log ready for commits. A database
// already larger than cfg.MaxBytes opens normally (DESIGN §12.3 rule 1).
func OpenWith(fsys fileio.FS, path string, cfg Config) (*Log, error) {
	if cfg.MaxBytes < 0 {
		return nil, fmt.Errorf("%w: max_bytes %d", dberr.ErrInvalidArgument, cfg.MaxBytes)
	}
	rs, err := recovery.Open(fsys, path, cfg.Recovery)
	if err != nil {
		return nil, err
	}
	return &Log{
		fsys:      fsys,
		path:      path,
		cfg:       cfg,
		lock:      rs.Lock,
		f:         rs.File,
		idx:       rs.Scan.Index,
		end:       rs.Scan.End,
		nextSeq:   rs.Scan.NextSeq,
		recovered: recoveryInfo(rs),
	}, nil
}

// checkUsable returns ErrClosed or ErrFailed when the handle is not OPEN.
// Callers hold mu (shared or exclusive).
func (l *Log) checkUsable() error {
	switch l.state {
	case stateClosed:
		return dberr.ErrClosed
	case stateFailed:
		return fmt.Errorf("%w (cause: %v)", dberr.ErrFailed, l.failErr)
	}
	return nil
}

// fail moves the handle to FAILED. It never touches the index (DESIGN §13.2).
func (l *Log) fail(cause error) {
	l.state = stateFailed
	l.failErr = cause
}

// Commit writes entries as one atomic operation and applies them to the
// index (DESIGN §6.2). Entries apply in order and the last operation on a
// key wins. An empty slice, or a commit whose net effect changes nothing
// (only deletes of absent keys), succeeds with no I/O (assumption A8).
//
// Admission (DESIGN §12): the frame is appended if it fits max_bytes;
// otherwise a compacting commit writes the live state with the commit
// applied into a new file and swaps it in; otherwise ErrFull.
//
// The caller must not modify the key/value slices until Commit returns: the
// encoder reads them twice (checksums, then write).
//
// Errors (DESIGN §6.3, §11.3):
//   - ErrInvalidArgument / ErrTooLarge: invalid entries, nothing written.
//   - ErrClosed / ErrFailed: the handle is not OPEN, nothing written.
//   - ErrFull: not admitted under max_bytes, limits exhausted, or the disk
//     reported out-of-space and everything was rolled back. Definite.
//   - ErrCorruption: a compacting commit found a damaged live entry while
//     copying it; nothing changed, the handle stays OPEN.
//   - ErrOutcomeUnknown: a failure after bytes may have become durable,
//     with no proven rollback. The handle is FAILED; the next Open decides.
//   - any other error (wrapped OS error) from a compacting commit that was
//     aborted before the replacement: definite, nothing changed, OPEN.
func (l *Log) Commit(entries []format.Entry) error {
	if len(entries) == 0 {
		l.mu.RLock()
		defer l.mu.RUnlock()
		return l.checkUsable()
	}
	// Size and validate before taking the lock (pure computation).
	payloadLen, err := format.PayloadSize(entries, format.MaxBatchOps)
	if err != nil {
		return usageError(err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkUsable(); err != nil {
		return err
	}
	return l.commitLocked(entries, payloadLen)
}

// Delete removes key. Deleting a missing key succeeds with no I/O: the index
// reflects synced state, so the key is absent on disk too (DESIGN §6.1).
func (l *Log) Delete(key []byte) error {
	return l.Commit([]format.Entry{{Op: format.OpDelete, Key: key}})
}

func usageError(err error) error {
	if errors.Is(err, format.ErrInvalid) && isTooLarge(err) {
		return fmt.Errorf("%w: %v", dberr.ErrTooLarge, err)
	}
	return fmt.Errorf("%w: %v", dberr.ErrInvalidArgument, err)
}

// isTooLarge reports whether a format validation error is about a value
// length or an op count (ErrTooLarge) rather than a malformed request.
func isTooLarge(err error) bool {
	var fe *format.FieldError
	if errors.As(err, &fe) {
		switch fe.Field {
		case "val_len":
			return fe.Value > format.MaxValueLen // (a DEL with a value is invalid, not too large)
		case "op_count":
			return fe.Value > 1
		case "payload_len":
			return true
		}
	}
	return false
}

// netEffect reports whether the commit changes the live state at all, and
// whether its net effect leaves some key holding a PUT ("PUT-bearing",
// DESIGN §12.3). Callers hold mu.
func (l *Log) netEffect(entries []format.Entry) (changes, putBearing bool) {
	if len(entries) == 1 {
		e := entries[0]
		if e.Op == format.OpPut {
			return true, true
		}
		_, present := l.idx.Get(e.Key)
		return present, false
	}
	final := make(map[string]format.OpCode, len(entries))
	for _, e := range entries {
		final[string(e.Key)] = e.Op
	}
	for k, op := range final {
		if op == format.OpPut {
			return true, true // a PUT-bearing commit always changes something
		}
		if _, present := l.idx.Get([]byte(k)); present {
			changes = true
		}
	}
	return changes, false
}

// commitLocked runs DESIGN §6.2 steps 4–7 with mu held exclusively.
func (l *Log) commitLocked(entries []format.Entry, payloadLen uint64) error {
	if l.nextSeq == 0 {
		return fmt.Errorf("%w: sequence numbers exhausted", dberr.ErrFull)
	}
	changes, putBearing := l.netEffect(entries)
	if !changes {
		return nil // A8: nothing to change, no I/O
	}
	frameSize := format.FrameHeaderSize + payloadLen // payloadLen <= MaxPayload: no overflow

	var plan *snapshotPlan
	dec := admission.Decide(admission.Request{
		Cap:        uint64(l.cfg.MaxBytes),
		End:        uint64(l.end),
		FrameSize:  frameSize,
		PutBearing: putBearing,
	}, func() uint64 {
		plan = l.planSnapshot(entries)
		return plan.size
	})
	switch dec {
	case admission.Full:
		return fmt.Errorf("%w: commit of %d bytes does not fit max_bytes %d (file %d bytes, compacted with the commit: %d bytes)",
			dberr.ErrFull, frameSize, l.cfg.MaxBytes, l.end, plan.size)
	case admission.Compact:
		return l.compactLocked(entries, plan)
	}

	if uint64(l.end) > format.MaxFileOffset-frameSize {
		return fmt.Errorf("%w: frame of %d bytes would pass the maximum file offset", dberr.ErrFull, frameSize)
	}
	start := l.end
	w := &offsetWriter{f: l.f, off: start}
	n, werr := format.WriteFrame(w, l.nextSeq, entries, format.MaxBatchOps)
	if werr == nil && n != int64(frameSize) {
		werr = fmt.Errorf("storage: frame wrote %d bytes, expected %d: %w", n, frameSize, io.ErrShortWrite)
	}
	if werr != nil {
		if n == 0 && format.Classify(werr) == format.ClassUsage {
			return usageError(werr) // rejected before any byte was written
		}
		return l.rollbackAfterWriteError(start, werr)
	}

	if err := l.f.Sync(); err != nil {
		// Never retry Sync (a retry can falsely succeed after the kernel
		// dropped the dirty pages). Best-effort truncate of the frame, with
		// no further Sync (F1); the outcome is unknown either way.
		terr := l.f.Truncate(start)
		l.fail(fmt.Errorf("sync failed: %w (rollback truncate: %v)", err, terr))
		return fmt.Errorf("%w: syncing frame %d: %v", dberr.ErrOutcomeUnknown, l.nextSeq, err)
	}

	// Durable: apply to the index in order.
	off := start + format.FrameHeaderSize
	for _, e := range entries {
		size := int64(format.EntryHeaderSize + len(e.Key) + len(e.Value))
		if e.Op == format.OpPut {
			l.idx.Put(string(e.Key), index.Loc{Off: off, Size: uint32(size)})
		} else {
			l.idx.Delete(e.Key)
		}
		off += size
	}
	l.end = start + int64(frameSize)
	if next, err := format.NextSeq(l.nextSeq); err == nil {
		l.nextSeq = next
	} else {
		l.nextSeq = 0
	}

	// The commit is durable and acknowledged whatever happens next. An
	// automatic compaction that fails either aborts cleanly (handle OPEN)
	// or leaves the handle FAILED; both old and new files contain this
	// commit, so the return value stays nil.
	if l.cfg.AutoCompact && admission.ShouldAutoCompact(uint64(l.end), l.estimateSnapshot()) {
		if p := l.planSnapshot(nil); admission.CompactWorthwhile(uint64(l.end), p.size) {
			_ = l.compactLocked(nil, p)
		}
	}
	return nil
}

// rollbackAfterWriteError implements the WriteAt rows of DESIGN §6.3.
func (l *Log) rollbackAfterWriteError(start int64, werr error) error {
	terr := l.f.Truncate(start)
	var serr error
	if terr == nil {
		serr = l.f.Sync()
	}
	if platform.IsDiskFull(werr) && terr == nil && serr == nil {
		// Definite: the partial frame is gone and that is durable.
		return fmt.Errorf("%w: disk full while writing frame %d (rolled back): %v", dberr.ErrFull, l.nextSeq, werr)
	}
	l.fail(fmt.Errorf("write failed: %w (rollback truncate: %v, sync: %v)", werr, terr, serr))
	return fmt.Errorf("%w: writing frame %d: %v", dberr.ErrOutcomeUnknown, l.nextSeq, werr)
}

// Get returns a copy of the latest value of key (DESIGN §7, §15.2). The
// shared lock is held across lookup and read, so the handle cannot close
// or be swapped by compaction mid-read; bytes at an indexed offset never
// change (append-only).
//
// A short read or an entry that fails verification (CRC, key mismatch) is
// ErrCorruption; the handle stays OPEN. An OS read error is returned wrapped
// and is not ErrCorruption.
func (l *Log) Get(key []byte) ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if err := l.checkUsable(); err != nil {
		return nil, err
	}
	loc, ok := l.idx.Get(key)
	if !ok {
		return nil, dberr.ErrNotFound
	}
	return readEntryValue(l.f, key, loc)
}

// readEntryValue reads and verifies the PUT entry at loc and returns its
// value in a buffer owned by the caller.
func readEntryValue(f fileio.File, key []byte, loc index.Loc) ([]byte, error) {
	b := make([]byte, loc.Size)
	if err := fileio.ReadFullAt(f, b, loc.Off); err != nil {
		if errors.Is(err, fileio.ErrShortRead) {
			return nil, fmt.Errorf("%w: entry for key at offset %d: %v", dberr.ErrCorruption, loc.Off, err)
		}
		return nil, fmt.Errorf("storage: reading entry at offset %d: %w", loc.Off, err)
	}
	h, v, err := format.DecodeEntryForKey(b, key)
	if err == nil && h.Op != format.OpPut {
		err = fmt.Errorf("index points at a %v entry", h.Op)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: entry at offset %d: %v", dberr.ErrCorruption, loc.Off, err)
	}
	if len(v) == 0 {
		return []byte{}, nil // a PUT of an empty value: present, not nil
	}
	return v, nil // v aliases b, which this call allocated: an independent copy
}

// Stats is a snapshot of the handle's counters.
type Stats struct {
	Keys      int
	LiveBytes uint64
	Size      int64  // file size == end of the last committed frame
	NextSeq   uint64 // 0: exhausted
	MaxBytes  int64
}

// Stats returns the current counters (works in any state).
func (l *Log) Stats() Stats {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return Stats{Keys: l.idx.Len(), LiveBytes: l.idx.LiveBytes(), Size: l.end, NextSeq: l.nextSeq, MaxBytes: l.cfg.MaxBytes}
}

// Close closes the data file, then releases the process lock. Close is not a
// durability point: every acknowledged commit is already synced. It is
// allowed in the FAILED state, and closing twice returns nil. Waits for
// in-flight operations (it takes the exclusive lock).
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state == stateClosed {
		return nil
	}
	l.state = stateClosed
	var errs []error
	if l.f != nil {
		errs = append(errs, l.f.Close())
		l.f = nil
	}
	errs = append(errs, l.lock.Release())
	return errors.Join(errs...)
}

// offsetWriter turns sequential Writes into WriteAt calls at increasing
// offsets, so a frame reaches the file strictly in order and a kill leaves a
// prefix of it. A short write without an error is reported as one.
type offsetWriter struct {
	f   fileio.File
	off int64
}

func (w *offsetWriter) Write(p []byte) (int, error) {
	n, err := w.f.WriteAt(p, w.off)
	w.off += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}
