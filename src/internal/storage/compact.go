package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"sort"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/admission"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/index"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
)

// SnapshotFramePayload bounds the payload of one snapshot frame, so that
// compaction holds at most a few MiB of values in memory at a time. One
// maximal entry (about 1 MiB) always fits.
const SnapshotFramePayload = 4 << 20

// snapItem is one live key in a snapshot: either an existing entry copied
// from the current log, or a PUT taken from the commit being applied.
type snapItem struct {
	key     string
	fromLog bool
	loc     index.Loc // fromLog
	value   []byte    // !fromLog: the caller's slice (Commit's contract)
}

func (it *snapItem) entrySize() uint64 {
	if it.fromLog {
		return uint64(it.loc.Size)
	}
	return format.EntryHeaderSize + uint64(len(it.key)) + uint64(len(it.value))
}

// snapshotPlan is the exact layout of a compacted file, computed before any
// I/O so that admission can decide on exact sizes.
type snapshotPlan struct {
	items  []snapItem
	bounds []int  // frame i holds items[bounds[i-1]:bounds[i]]
	size   uint64 // total file size: superblock + frames
}

// planSnapshot lists the live state with commit applied (nil for a plain
// compaction) and groups it into frames of at most MaxFrameOps entries and
// SnapshotFramePayload bytes. All items are ordered by key (byte order) so
// the output is deterministic. Callers hold mu.
func (l *Log) planSnapshot(commit []format.Entry) *snapshotPlan {
	final := make(map[string]int, len(commit)) // key -> index of its last op in commit
	for i, e := range commit {
		final[string(e.Key)] = i
	}
	p := &snapshotPlan{items: make([]snapItem, 0, l.idx.Len()+len(final))}
	l.idx.Range(func(k string, loc index.Loc) bool {
		if _, touched := final[k]; !touched {
			p.items = append(p.items, snapItem{key: k, fromLog: true, loc: loc})
		}
		return true
	})
	for i, e := range commit {
		if final[string(e.Key)] == i && e.Op == format.OpPut {
			p.items = append(p.items, snapItem{key: string(e.Key), value: e.Value})
		}
	}
	// Deterministic output: every snapshot lists its keys in ascending byte
	// order (Go string order), whatever the history, map order or commit
	// order. Two databases with the same live state and the same next
	// sequence number therefore compact to byte-identical files. Keys are
	// unique here (log keys touched by commit were skipped above).
	sort.Slice(p.items, func(i, j int) bool { return p.items[i].key < p.items[j].key })
	p.size = format.SuperblockSize
	for i := 0; i < len(p.items); {
		j, payload := i, uint64(0)
		for j < len(p.items) && j-i < format.MaxFrameOps {
			s := p.items[j].entrySize()
			if j > i && payload+s > SnapshotFramePayload {
				break
			}
			payload += s
			j++
		}
		p.bounds = append(p.bounds, j)
		p.size += format.FrameHeaderSize + payload
		i = j
	}
	return p
}

// estimateSnapshot is a cheap lower-ish estimate of the compacted size for
// the automatic trigger; the exact plan is computed only if it fires.
func (l *Log) estimateSnapshot() uint64 {
	live := l.idx.LiveBytes()
	frames := live/SnapshotFramePayload + uint64(l.idx.Len())/format.MaxFrameOps + 1
	return format.SuperblockSize + live + frames*format.FrameHeaderSize
}

// Compact rewrites the live state into a new file and swaps it in (DESIGN
// §10, §11). It never increases the file size (DESIGN §12.3 rule 4): if the
// compacted file would not be strictly smaller, or the log has no frames,
// it does nothing.
//
// Errors: ErrClosed / ErrFailed (not OPEN, nothing done); ErrFull (disk full
// while writing the new file; nothing changed); ErrCorruption (a live entry
// failed verification while copying; nothing changed); a wrapped OS error
// for any other failure before the replacement (nothing changed, OPEN);
// ErrFailed if the replacement failed without a proven restore (the data
// is intact on disk in .old or .compact and the next Open resolves it).
func (l *Log) Compact() error {
	l.wmu.Lock()
	defer l.wmu.Unlock()
	if err := l.checkUsable(); err != nil {
		return err
	}
	if l.end == format.SuperblockSize {
		return nil // no frames: nothing to compact (DESIGN §9.2)
	}
	p := l.planSnapshot(nil)
	if !admission.CompactWorthwhile(uint64(l.end), p.size) {
		return nil
	}
	return l.compactLocked(nil, p)
}

// compactLocked writes plan into path.compact and replaces path with it
// (DESIGN §10.2), with wmu held and mu not held: Phase A (building the
// sealed .compact file) changes no in-memory state, so readers continue;
// mu is taken exclusively for Phase B (handle close, renames, swap). commit is the compacting
// commit's operations (already folded into plan), or nil for a plain
// compaction. The old file and index stay authoritative until the new file
// is in place and validated.
func (l *Log) compactLocked(commit []format.Entry, p *snapshotPlan) error {
	isCommit := commit != nil
	cpath := l.path + recovery.SuffixCompact
	opath := l.path + recovery.SuffixOld
	firstSeq := l.nextSeq
	nframes := uint64(len(p.bounds))
	if firstSeq == 0 || (nframes > 0 && firstSeq > math.MaxUint64-(nframes-1)) {
		return fmt.Errorf("%w: sequence numbers exhausted", dberr.ErrFull)
	}

	// ---- Phase A: build and seal path.compact. The live file is untouched;
	// every failure here is definite and leaves the handle OPEN.
	abort := func(cf fileio.File, err error) error {
		if cf != nil {
			cf.Close()
		}
		if cf != nil {
			l.removeRetry(cpath) // best-effort; A1 deletes it on the next Open anyway
		}
		switch {
		case platform.IsDiskFull(err):
			return fmt.Errorf("%w: disk full while compacting (nothing changed): %v", dberr.ErrFull, err)
		case errors.Is(err, dberr.ErrCorruption):
			return err
		}
		return fmt.Errorf("storage: compaction aborted, nothing changed: %w", err)
	}

	// §11.2: delete stale .compact and .old first; abort if impossible.
	for _, stale := range []string{cpath, opath} {
		if err := l.removeRetry(stale); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return abort(nil, fmt.Errorf("deleting stale %s: %w", stale, err))
		}
	}
	cf, err := l.fsys.CreateExclusive(cpath)
	if err != nil {
		return abort(nil, fmt.Errorf("creating %s: %w", cpath, err))
	}
	// Copy live entries through a read handle of our own, so the copy does
	// not queue behind concurrent readers on the main handle (change C).
	// It is given back before Phase B closes all read handles.
	src, pooled := l.readHandle()
	srcHeld := true
	release := func() {
		if srcHeld {
			l.putHandle(src, pooled)
			srcHeld = false
		}
	}
	defer release()
	off, seq := int64(format.SuperblockSize), firstSeq
	start := 0
	for _, end := range p.bounds {
		entries := make([]format.Entry, 0, end-start)
		for i := start; i < end; i++ {
			it := &p.items[i]
			v := it.value
			if it.fromLog {
				if v, err = readEntryValue(src, []byte(it.key), it.loc); err != nil {
					return abort(cf, err)
				}
			}
			entries = append(entries, format.Entry{Op: format.OpPut, Key: []byte(it.key), Value: v})
		}
		n, err := format.WriteFrame(&offsetWriter{f: cf, off: off}, seq, entries, format.MaxFrameOps)
		off += n
		if err != nil {
			return abort(cf, fmt.Errorf("writing snapshot frame %d: %w", seq, err))
		}
		seq++
		start = end
	}
	if uint64(off) != p.size {
		return abort(cf, fmt.Errorf("internal error: snapshot is %d bytes, planned %d", off, p.size))
	}
	if err := cf.Sync(); err != nil { // frames durable before the seal
		return abort(cf, fmt.Errorf("syncing %s: %w", cpath, err))
	}
	sb, err := format.Superblock{FirstSeq: firstSeq, SnapshotEnd: p.size}.Encode()
	if err != nil {
		return abort(cf, err)
	}
	if _, err := fileio.WriteFullAt(cf, sb[:], 0); err != nil { // superblock LAST: seals the file
		return abort(cf, fmt.Errorf("sealing %s: %w", cpath, err))
	}
	if err := cf.Sync(); err != nil {
		return abort(cf, fmt.Errorf("syncing seal of %s: %w", cpath, err))
	}
	if err := cf.Close(); err != nil {
		return abort(nil, fmt.Errorf("closing %s: %w", cpath, err)) // handle released; .compact left for A1
	}

	// ---- Phase B: replacement (DESIGN §11.1, §11.3). From here a failure
	// without a proven restore puts the handle in FAILED. Readers are
	// excluded from here on: the data handle is closed and swapped.
	release()
	l.mu.Lock()
	defer l.mu.Unlock()
	unknown := func(cause error) error {
		l.state = stateFailed // mu is held: set directly (l.fail would re-lock)
		l.failErr = fmt.Errorf("compaction replacement failed: %w", cause)
		if isCommit {
			return fmt.Errorf("%w: compacting commit: %v", dberr.ErrOutcomeUnknown, cause)
		}
		return fmt.Errorf("%w: compaction: %v", dberr.ErrFailed, cause)
	}
	// reopenOld reopens `path` (the old file) after a failed step and makes
	// it current again, with the old index; then the .compact is stale.
	restored := func(cause error) error {
		f, err := l.openRetry(l.path)
		if err != nil {
			return unknown(fmt.Errorf("%v; reopening %s: %w", cause, l.path, err))
		}
		l.f = f
		l.removeRetry(cpath) // stale while path is valid (§11.3 cleanup)
		return fmt.Errorf("storage: compaction failed, old database restored, nothing changed: %w", cause)
	}

	// Windows cannot move a file that has an open handle (Go opens without
	// FILE_SHARE_DELETE), so every handle is closed first (§11.1): the
	// read-only pool (idle: mu is held exclusively), then the data handle.
	_ = l.closeReadHandles()
	err = l.f.Close()
	l.f = nil
	if err != nil {
		return restored(fmt.Errorf("closing data file: %w", err))
	}
	if err := l.moveRetry(l.path, opath); err != nil { // R1
		return restored(fmt.Errorf("R1 %s -> %s: %w", l.path, opath, err))
	}
	if err := l.moveRetry(cpath, l.path); err != nil { // R2
		if rerr := l.moveRetry(opath, l.path); rerr != nil {
			// path missing, .old valid, .compact sealed: next Open applies A3.
			return unknown(fmt.Errorf("R2 %s -> %s: %v; restore of %s failed: %w", cpath, l.path, err, opath, rerr))
		}
		return restored(fmt.Errorf("R2 %s -> %s: %w (old file restored)", cpath, l.path, err))
	}
	if err := l.fsys.SyncDir(filepath.Dir(l.path)); err != nil {
		return unknown(fmt.Errorf("syncing directory after replacement: %w", err))
	}

	// Reopen and validate the new file (§10.2 step 12) before it becomes
	// authoritative in memory.
	nf, err := l.openRetry(l.path)
	if err != nil {
		return unknown(fmt.Errorf("reopening compacted %s: %w", l.path, err))
	}
	nsb, scan, err := recovery.ScanFile(nf, l.path)
	if err == nil && (nsb.FirstSeq != firstSeq || nsb.SnapshotEnd != p.size || scan.Tail != recovery.TailClean ||
		scan.End != int64(p.size) || scan.Frames != nframes || scan.Index.Len() != len(p.items)) {
		err = fmt.Errorf("compacted file does not match the plan (first_seq %d/%d, snapshot_end %d/%d, end %d, frames %d/%d, keys %d/%d, tail %q)",
			nsb.FirstSeq, firstSeq, nsb.SnapshotEnd, p.size, scan.End, scan.Frames, nframes, scan.Index.Len(), len(p.items), scan.Tail)
	}
	if err != nil {
		nf.Close()
		return unknown(fmt.Errorf("validating compacted file: %w", err))
	}

	// Swap (§10.2 step 13). The commit, if any, is acknowledged only now.
	l.f, l.idx, l.end, l.nextSeq = nf, scan.Index, scan.End, scan.NextSeq
	l.removeRetry(opath) // R3, best-effort: a stale .old next to a valid path is harmless (A1)
	return nil
}

func (l *Log) retryable() func(error) bool { return platform.IsSharingViolation }

// moveRetry is MoveNoReplace with bounded retry of sharing violations
// (antivirus, indexers). It never replaces an existing destination.
func (l *Log) moveRetry(from, to string) error {
	_, err := fileio.Retry(l.cfg.Recovery.IORetry, l.retryable(), func() error { return l.fsys.MoveNoReplace(from, to) })
	return err
}

func (l *Log) removeRetry(name string) error {
	_, err := fileio.Retry(l.cfg.Recovery.IORetry, l.retryable(), func() error { return l.fsys.Remove(name) })
	return err
}

func (l *Log) openRetry(name string) (fileio.File, error) {
	var f fileio.File
	_, err := fileio.Retry(l.cfg.Recovery.IORetry, l.retryable(), func() (e error) { f, e = l.fsys.OpenRW(name); return })
	if err != nil {
		return nil, err
	}
	return f, nil
}
