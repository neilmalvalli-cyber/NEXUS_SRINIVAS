package recovery

import (
	"fmt"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/locking"
)

// Inspection is the result of a read-only check (Inspect): what Open would
// find and what it would have to repair, without repairing anything.
type Inspection struct {
	Path string
	// Rules lists the authority rules Open would apply, in order, ending
	// with RuleA1 when `path` is (or would become) the database. Empty when
	// Open would create a new database (Missing is then true).
	Rules []Rule
	// Target is the file that holds (or would become) the database: `path`
	// itself under A1, or the temp file A3/A4/A5 would move into place.
	Target string
	// Missing is true when no database exists: Open would create an empty
	// one (rule A7). Nothing was scanned.
	Missing bool
	// Scan is the read-only scan of Target (nil when Missing).
	Scan *ScanResult
	// Stale lists temp files Open would delete after a successful scan
	// (A1 cleanup), or unsealed temps A7 would delete.
	Stale []string
}

// NeedsRepair reports whether a normal Open would change any file: move a
// temp file into place, cut a torn tail, delete stale temps, or create a
// new database.
func (in *Inspection) NeedsRepair() bool {
	return in.Missing || len(in.Stale) > 0 || in.Target != in.Path ||
		(in.Scan != nil && in.Scan.Tail != TailClean)
}

// Repairs describes, in plain words, every change a normal Open would make.
func (in *Inspection) Repairs() []string {
	var r []string
	if in.Missing {
		r = append(r, "no database exists; open would create an empty one (rule A7)")
	}
	if in.Target != "" && in.Target != in.Path {
		r = append(r, fmt.Sprintf("open would move %s into place as %s (rule %s: an interrupted compaction or creation)", in.Target, in.Path, in.Rules[0]))
	}
	if in.Scan != nil && in.Scan.Tail != TailClean {
		r = append(r, fmt.Sprintf("open would discard a torn %d-byte tail at offset %d (rule %s); it was never acknowledged",
			in.Scan.Size-in.Scan.End, in.Scan.End, in.Scan.Tail))
	}
	for _, s := range in.Stale {
		r = append(r, "open would delete leftover temp file "+s)
	}
	return r
}

// Inspect is the read-only counterpart of Open (`kvdb check --no-repair`).
//
// It takes the process lock (so no writer is appending while it reads; the
// lock file `<path>.lock` is created if absent, and nothing else is), then
// walks the authority table exactly as Resolve does, but instead of moving,
// creating or deleting files it records what Resolve would do. The file that
// is or would become the database is opened read-only and scanned with the
// same Scan as Open, so every frame, entry checksum, sequence number and
// tail rule is checked. Nothing is truncated, moved, written, synced or
// deleted.
//
// Errors are the same as Open's: ErrLocked, ErrBadFormat (rules A2, A6),
// ErrCorruption (damage that is not a torn tail, including damage inside
// the snapshot), or a wrapped I/O error. A torn tail is not an error: it is
// a normal crash state, reported through NeedsRepair.
func Inspect(fsys fileio.FS, path string, opt Options) (*Inspection, error) {
	lk, err := locking.Acquire(fsys, path, opt.LockRetry)
	if err != nil {
		return nil, err
	}
	defer lk.Release()
	if err := lk.Check(path); err != nil {
		return nil, fmt.Errorf("recovery: inspect %s: %w", path, err)
	}

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
		return nil, err
	}
	in.Scan = scan
	return in, nil
}

// plan mirrors Resolve's rule order (A1, A2, A3, A4, A6, A5, A7; design
// deviation D3) without changing anything. Each adoption in Resolve is
// followed by a restart that matches A1 on the adopted file, so the plan
// for an adoption is [A3|A4|A5, A1] with that file as the target.
func plan(fsys fileio.FS, path string, policy fileio.RetryPolicy) (*Inspection, error) {
	compact, old, newName := Names(path)
	in := &Inspection{Path: path}

	p, err := inspect(fsys, path, policy)
	if err != nil {
		return nil, err
	}
	switch p.state {
	case stValid: // A1
		in.Rules, in.Target = []Rule{RuleA1}, path
		for _, t := range []string{compact, old, newName} {
			ti, err := inspect(fsys, t, policy)
			if err != nil {
				return nil, err
			}
			if ti.state != stMissing {
				in.Stale = append(in.Stale, t)
			}
		}
		return in, nil
	case stInvalid, stCorrupt, stOther: // A2
		return nil, refuse(RuleA2, p)
	}

	c, err := inspect(fsys, compact, policy)
	if err != nil {
		return nil, err
	}
	switch c.state {
	case stValid: // A3
		in.Rules, in.Target = []Rule{RuleA3, RuleA1}, compact
		return in, nil
	case stCorrupt, stOther:
		return nil, refuse(RuleA3, c)
	}

	o, err := inspect(fsys, old, policy)
	if err != nil {
		return nil, err
	}
	switch o.state {
	case stValid: // A4
		in.Rules, in.Target = []Rule{RuleA4, RuleA1}, old
		return in, nil
	case stInvalid, stCorrupt, stOther: // A6
		return nil, refuse(RuleA6, o)
	}

	n, err := inspect(fsys, newName, policy)
	if err != nil {
		return nil, err
	}
	switch n.state {
	case stValid: // A5
		in.Rules, in.Target = []Rule{RuleA5, RuleA1}, newName
		return in, nil
	case stCorrupt, stOther:
		return nil, refuse(RuleA5, n)
	}

	// A7: nothing usable. Unsealed temps would be deleted, then a fresh
	// database created.
	for _, t := range []inspection{c, n} {
		if t.state == stInvalid {
			in.Stale = append(in.Stale, t.name)
		}
	}
	in.Missing = true
	return in, nil
}
