package storage

import (
	"fmt"
	"sort"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/index"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
)

// This file holds read-only inspection helpers used by the CLI (`open`,
// `dump`, `check`). None of them writes to the file or changes the index.

// RecoveryInfo describes what Open did to bring the file to a clean state.
type RecoveryInfo struct {
	// Rules lists the authority rules (A1–A7) that fired, in order.
	Rules []string
	// Created is true when Open created a new, empty database (rule A7).
	Created bool
	// Tail is the tail rule (C1, C2, C4, C5) whose torn frame was cut off,
	// or "" when the file ended on a frame boundary.
	Tail string
	// DiscardedBytes is the size of the torn tail that was truncated.
	DiscardedBytes int64
	// Frames is the number of valid frames replayed.
	Frames uint64
	// Warnings holds best-effort cleanup failures (stale temp files that
	// could not be removed). They do not affect correctness.
	Warnings []string
}

func recoveryInfo(rs *recovery.State) RecoveryInfo {
	ri := RecoveryInfo{Frames: rs.Scan.Frames}
	if rs.Resolution != nil {
		ri.Created = rs.Resolution.Created
		for _, r := range rs.Resolution.Rules {
			ri.Rules = append(ri.Rules, string(r))
		}
	}
	if rs.Truncated {
		ri.Tail = string(rs.Scan.Tail)
		ri.DiscardedBytes = rs.Scan.Size - rs.Scan.End
	}
	for _, w := range rs.Warnings {
		ri.Warnings = append(ri.Warnings, w.Error())
	}
	return ri
}

// Recovery returns what Open did when this handle was opened.
func (l *Log) Recovery() RecoveryInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.recovered
}

// sortedKeysLocked returns every live key in byte order. Callers hold mu.
func (l *Log) sortedKeysLocked() []string {
	keys := make([]string, 0, l.idx.Len())
	l.idx.Range(func(k string, _ index.Loc) bool {
		keys = append(keys, k)
		return true
	})
	sort.Strings(keys) // Go string order is byte order
	return keys
}

// Range calls fn for every live key in ascending byte order with its
// verified value. It holds the shared lock for the whole iteration, so it
// sees one consistent state (writers wait). The slices passed to fn are
// owned by fn. Iteration stops at the first error from fn or from reading a
// value (ErrCorruption for an entry that fails verification).
func (l *Log) Range(fn func(key, value []byte) error) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if err := l.checkUsable(); err != nil {
		return err
	}
	for _, k := range l.sortedKeysLocked() {
		loc, _ := l.idx.Get([]byte(k))
		v, err := readEntryValue(l.f, []byte(k), loc)
		if err != nil {
			return fmt.Errorf("key %q: %w", k, err)
		}
		if err := fn([]byte(k), v); err != nil {
			return err
		}
	}
	return nil
}

// VerifyReport summarizes a successful Verify.
type VerifyReport struct {
	Keys      int
	Frames    uint64
	Size      int64
	LiveBytes uint64
}

// Verify re-reads the whole data file through the open handle and checks
// that it is exactly what this handle believes (used by `kvdb check`):
//
//   - the superblock decodes and its snapshot_end is within the file;
//   - every frame from the superblock to EOF passes its header CRC, payload
//     CRC, entry CRCs and sequence checks, and the file ends exactly on a
//     frame boundary (no torn tail is left after recovery);
//   - the file size, next sequence number and the index rebuilt from the
//     file are identical to the handle's in-memory state;
//   - every live value can be read back and passes its entry CRC and key
//     check.
//
// It never modifies the file. Any mismatch is ErrCorruption with a reason.
func (l *Log) Verify() (VerifyReport, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if err := l.checkUsable(); err != nil {
		return VerifyReport{}, err
	}
	_, scan, err := recovery.ScanFile(l.f, l.path)
	if err != nil {
		return VerifyReport{}, err
	}
	corrupt := func(format string, a ...any) (VerifyReport, error) {
		return VerifyReport{}, fmt.Errorf("%w: %s: %s", dberr.ErrCorruption, l.path, fmt.Sprintf(format, a...))
	}
	if scan.Tail != recovery.TailClean {
		return corrupt("torn tail (rule %s) at offset %d after recovery: %v", scan.Tail, scan.End, scan.TailCause)
	}
	if scan.End != l.end || scan.Size != l.end {
		return corrupt("file size %d / end %d differs from the open handle's end %d", scan.Size, scan.End, l.end)
	}
	if scan.NextSeq != l.nextSeq {
		return corrupt("next sequence %d differs from the open handle's %d", scan.NextSeq, l.nextSeq)
	}
	if scan.Index.Len() != l.idx.Len() || scan.Index.LiveBytes() != l.idx.LiveBytes() {
		return corrupt("rescan found %d keys / %d live bytes, the open handle has %d / %d",
			scan.Index.Len(), scan.Index.LiveBytes(), l.idx.Len(), l.idx.LiveBytes())
	}
	var mismatch error
	scan.Index.Range(func(k string, loc index.Loc) bool {
		got, ok := l.idx.Get([]byte(k))
		if !ok || got != loc {
			mismatch = fmt.Errorf("key %q: rescan location %+v, open handle %+v (present %v)", k, loc, got, ok)
			return false
		}
		return true
	})
	if mismatch != nil {
		return corrupt("%v", mismatch)
	}
	for _, k := range l.sortedKeysLocked() {
		loc, _ := l.idx.Get([]byte(k))
		if _, err := readEntryValue(l.f, []byte(k), loc); err != nil {
			return VerifyReport{}, fmt.Errorf("key %q: %w", k, err)
		}
	}
	return VerifyReport{Keys: l.idx.Len(), Frames: scan.Frames, Size: l.end, LiveBytes: l.idx.LiveBytes()}, nil
}
