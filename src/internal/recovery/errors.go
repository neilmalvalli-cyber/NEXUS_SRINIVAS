package recovery

import (
	"fmt"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
)

// ScanError is the structured form of a corruption error from Scan (rules
// C0, C6, C7, and snapshot_end off a frame boundary). It unwraps to
// dberr.ErrCorruption only, exactly as the plain error it replaced, and its
// message is unchanged. Diagnostics (internal/xray) read Rule, Offset and
// Cause with errors.As instead of parsing the message.
type ScanError struct {
	Path   string
	Offset uint64 // file offset of the frame that failed
	Rule   string // "C0", "C6" or "C7"
	Cause  error  // the decode error or a description of the violation
}

func (e *ScanError) Error() string {
	return fmt.Sprintf("%v: %s: frame at offset %d (rule %s): %v", dberr.ErrCorruption, e.Path, e.Offset, e.Rule, e.Cause)
}

func (e *ScanError) Unwrap() error { return dberr.ErrCorruption }

// RefusalError is the structured form of an authority-table refusal (rules
// A2, A3, A5, A6 refusing a file). It unwraps to dberr.ErrCorruption when
// the file's superblock is ours but the file is damaged, and to
// dberr.ErrBadFormat otherwise, exactly as the plain error it replaced, and
// its message is unchanged.
type RefusalError struct {
	Rule    Rule
	File    string
	Corrupt bool  // true: ErrCorruption; false: ErrBadFormat
	Cause   error // decode / size error, or "not a regular file"
}

func (e *RefusalError) sentinel() error {
	if e.Corrupt {
		return dberr.ErrCorruption
	}
	return dberr.ErrBadFormat
}

func (e *RefusalError) Error() string {
	return fmt.Sprintf("%v: %s (rule %s): %v", e.sentinel(), e.File, e.Rule, e.Cause)
}

func (e *RefusalError) Unwrap() error { return e.sentinel() }

// Plan is Inspect without the lock: the authority-table plan for path and,
// when a target exists, the read-only Scan of it. It never creates, writes,
// truncates, moves or deletes any file, not even the lock file, so it is
// what read-only diagnostics use.
//
// Without the lock a writer may be appending while Plan reads; the caller
// must either know that no writer is active (it holds the database itself
// and has excluded its own writers) or treat a torn tail as possibly a write
// in progress. Errors are Inspect's, minus ErrLocked.
func Plan(fsys fileio.FS, path string, opt Options) (*Inspection, error) {
	in, err := plan(fsys, path, opt.IORetry)
	if err != nil || in.Missing {
		return in, err
	}
	var f fileio.File
	if _, err := fileio.Retry(opt.IORetry, retryable, func() (e error) { f, e = fsys.Open(in.Target); return }); err != nil {
		return nil, fmt.Errorf("recovery: opening %s read-only: %w", in.Target, err)
	}
	defer f.Close() // read-only handle: a close error cannot lose data
	_, scan, err := ScanFile(f, in.Target)
	if err != nil {
		return in, err // in carries Rules and Target for the caller's report
	}
	in.Scan = scan
	return in, nil
}
