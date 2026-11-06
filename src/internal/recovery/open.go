package recovery

import (
	"errors"
	"fmt"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/locking"
)

// State is a recovered database, ready for the write path (Stage 3).
// The caller owns Lock and File and must release them on Close: File first,
// then Lock.
type State struct {
	Path       string
	Lock       *locking.Lock
	File       fileio.File // opened read-write; its size is Scan.End
	Superblock format.Superblock
	Resolution *Resolution
	Scan       *ScanResult
	// Truncated is true when a torn tail (Scan.Tail) was cut off; the file
	// shrank from Scan.Size to Scan.End and was synced.
	Truncated bool
	// Warnings holds best-effort cleanup failures (stale temp files that
	// could not be deleted). They do not affect correctness.
	Warnings []error
}

// Open runs the whole recovery sequence of DESIGN §8:
//
//   - steps 0–1: take the lock and resolve authority (Prepare);
//   - steps 2–3: open `path` read-write, re-check its superblock and size;
//   - steps 4–10: Scan (read-only);
//   - steps 11–12: truncate a proven torn tail to exactly Scan.End, then Sync;
//   - finally delete stale temp files (D2) and return the state.
//
// On any error every handle is closed, the lock is released and no State is
// returned. Errors are ErrLocked, ErrBadFormat, ErrCorruption, or an I/O
// error (wrapped, never converted to one of the others). Running Open again
// after a crash at any point, including during the truncation, reaches the
// same state (T-REC-12): truncation is to an offset determined by the bytes
// before it, which it does not change.
func Open(fsys fileio.FS, path string, opt Options) (st *State, err error) {
	lk, res, err := Prepare(fsys, path, opt)
	if err != nil {
		return nil, err
	}
	var f fileio.File
	defer func() {
		if err != nil {
			if f != nil {
				f.Close()
			}
			lk.Release()
		}
	}()

	_, err = fileio.Retry(opt.IORetry, retryable, func() (e error) { f, e = fsys.OpenRW(path); return })
	if err != nil {
		f = nil
		return nil, fmt.Errorf("recovery: opening %s: %w", path, err)
	}
	size, err := f.Size()
	if err != nil {
		return nil, fmt.Errorf("recovery: size of %s: %w", path, err)
	}
	sb, err := checkSuperblock(f, path, size)
	if err != nil {
		return nil, err
	}
	scan, err := Scan(f, path, sb, size)
	if err != nil {
		return nil, err
	}

	st = &State{Path: path, Lock: lk, Superblock: sb, Resolution: res, Scan: scan}
	if scan.Tail != TailClean {
		if scan.End < int64(sb.SnapshotEnd) || scan.End >= size {
			// Scan guarantees this cannot happen; refuse rather than cut.
			return nil, fmt.Errorf("recovery: internal error: refusing truncation of %s to %d (size %d, snapshot_end %d)", path, scan.End, size, sb.SnapshotEnd)
		}
		if err = f.Truncate(scan.End); err != nil {
			return nil, fmt.Errorf("recovery: truncating torn tail of %s to %d (rule %s): %w", path, scan.End, scan.Tail, err)
		}
		if err = f.Sync(); err != nil {
			return nil, fmt.Errorf("recovery: syncing %s after truncation: %w", path, err)
		}
		st.Truncated = true
	}
	st.Warnings = RemoveStale(fsys, lk, res, opt.IORetry)
	st.File = f
	return st, nil
}

// ScanFile validates an already-open database file without modifying it:
// superblock (as in Open), then Scan. Compaction uses it to validate the
// file it has just moved into place (DESIGN §10.2 step 12); the caller
// decides what a non-clean tail means.
func ScanFile(f fileio.File, path string) (format.Superblock, *ScanResult, error) {
	size, err := f.Size()
	if err != nil {
		return format.Superblock{}, nil, fmt.Errorf("recovery: size of %s: %w", path, err)
	}
	sb, err := checkSuperblock(f, path, size)
	if err != nil {
		return format.Superblock{}, nil, err
	}
	scan, err := Scan(f, path, sb, size)
	if err != nil {
		return format.Superblock{}, nil, err
	}
	return sb, scan, nil
}

// checkSuperblock re-reads the superblock through the read-write handle, so
// the scan works on exactly what this handle sees (the read-only check in
// Resolve used another handle). Under the lock nothing should have changed;
// if something did, the same classification as rule A2 / D1 applies.
func checkSuperblock(f fileio.File, path string, size int64) (format.Superblock, error) {
	if size < format.SuperblockSize {
		return format.Superblock{}, fmt.Errorf("%w: %s: file is %d bytes", dberr.ErrBadFormat, path, size)
	}
	var b [format.SuperblockSize]byte
	if err := fileio.ReadFullAt(f, b[:], 0); err != nil {
		return format.Superblock{}, fmt.Errorf("recovery: reading superblock of %s: %w", path, err)
	}
	sb, err := format.DecodeSuperblock(b[:])
	if err != nil {
		return format.Superblock{}, fmt.Errorf("%w: %s: %v", dberr.ErrBadFormat, path, err)
	}
	if err := sb.CheckFileSize(uint64(size)); err != nil {
		return format.Superblock{}, fmt.Errorf("%w: %s: %v", dberr.ErrCorruption, path, err)
	}
	return sb, nil
}

// Close releases a State: the data file first, then the lock. It is safe to
// call more than once.
func (s *State) Close() error {
	var errs []error
	if s.File != nil {
		errs = append(errs, s.File.Close())
		s.File = nil
	}
	if s.Lock != nil {
		errs = append(errs, s.Lock.Release())
		s.Lock = nil
	}
	return errors.Join(errs...)
}
