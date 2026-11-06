package recovery

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/locking"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
)

// Temporary-file suffixes (DESIGN §4).
const (
	SuffixCompact = ".compact"
	SuffixOld     = ".old"
	SuffixNew     = ".new"
)

// MaxRestarts caps how often the table restarts after a move (DESIGN §11.4,
// "Termination"). A correct table needs at most one restart.
const MaxRestarts = 3

// Rule names a row of the authority table.
type Rule string

const (
	RuleA1 Rule = "A1" // path valid: use it
	RuleA2 Rule = "A2" // path invalid: ErrBadFormat
	RuleA3 Rule = "A3" // adopt sealed .compact
	RuleA4 Rule = "A4" // restore valid .old
	RuleA5 Rule = "A5" // adopt sealed .new
	RuleA6 Rule = "A6" // .old invalid: ErrBadFormat
	RuleA7 Rule = "A7" // nothing usable: create a fresh database
)

// Resolution is the outcome of a successful Resolve: `path` exists and its
// superblock is valid (rule A1 matched last).
type Resolution struct {
	Path       string
	Superblock format.Superblock
	Size       int64 // file size when the superblock was checked
	// Rules lists every rule that fired, in order, ending with RuleA1
	// (for example [A7 A1] for a newly created database).
	Rules []Rule
	// Created is true when rule A7 created the database in this call.
	Created bool
	// Stale lists the temporary files (.compact, .old, .new) that existed
	// when A1 matched. They are not authoritative. They are NOT deleted by
	// Resolve: Open deletes them with RemoveStale only after the recovery
	// scan has accepted `path` (design deviation D2, see DESIGN §11.4).
	Stale []string
}

// Names returns the four file names the table looks at.
func Names(path string) (compact, old, newName string) {
	return path + SuffixCompact, path + SuffixOld, path + SuffixNew
}

// fileState is what the table needs to know about one file.
type fileState int

const (
	stMissing fileState = iota
	stValid             // superblock decodes and snapshot_end <= size ("valid" / "sealed")
	stInvalid           // exists, superblock does not decode ("invalid" / "unsealed")
	stCorrupt           // superblock decodes but snapshot_end > size (ours, but damaged)
	stOther             // not a regular file (directory, device ...): never touched
)

type inspection struct {
	name  string
	state fileState
	sb    format.Superblock
	size  int64
	why   error // decode / size error for stInvalid and stCorrupt
}

// retryable reports whether an error from a read, move or delete is a
// transient sharing violation (antivirus, indexer, backup tool) worth
// retrying with bounded backoff (DESIGN §11.3, §11.4).
func retryable(err error) bool { return platform.IsSharingViolation(err) }

// inspect classifies one file. It only reads. Any operating-system error
// (after bounded retry of sharing violations) is returned as an error and
// the caller stops without changing anything (DESIGN §11.4, T-AUTH-12).
func inspect(fsys fileio.FS, name string, policy fileio.RetryPolicy) (inspection, error) {
	in := inspection{name: name}
	var fi fs.FileInfo
	_, err := fileio.Retry(policy, retryable, func() (e error) { fi, e = fsys.Stat(name); return })
	if errors.Is(err, fs.ErrNotExist) {
		in.state = stMissing
		return in, nil
	}
	if err != nil {
		return in, fmt.Errorf("recovery: checking %s: %w", name, err)
	}
	if !fi.Mode().IsRegular() {
		in.state = stOther
		in.why = fmt.Errorf("not a regular file (mode %v)", fi.Mode())
		return in, nil
	}

	var f fileio.File
	_, err = fileio.Retry(policy, retryable, func() (e error) { f, e = fsys.Open(name); return })
	if err != nil {
		// It existed a moment ago; under the lock nothing of ours removes
		// it, so any failure here (including not-exist) is reported.
		return in, fmt.Errorf("recovery: opening %s: %w", name, err)
	}
	defer f.Close() // read-only handle: a close error cannot lose data

	size, err := f.Size()
	if err != nil {
		return in, fmt.Errorf("recovery: size of %s: %w", name, err)
	}
	in.size = size
	if size < format.SuperblockSize {
		in.state = stInvalid
		in.why = fmt.Errorf("%w: file is %d bytes, superblock needs %d",
			format.ErrTruncated, size, format.SuperblockSize)
		return in, nil
	}
	var b [format.SuperblockSize]byte
	if err := fileio.ReadFullAt(f, b[:], 0); err != nil {
		// Includes ErrShortRead: the size said 32 bytes were there, so a
		// short read is an I/O anomaly, not evidence about the format.
		return in, fmt.Errorf("recovery: reading superblock of %s: %w", name, err)
	}
	sb, err := format.DecodeSuperblock(b[:])
	if err != nil {
		in.state = stInvalid
		in.why = err
		return in, nil
	}
	in.sb = sb
	if err := sb.CheckFileSize(uint64(size)); err != nil {
		in.state = stCorrupt
		in.why = err
		return in, nil
	}
	in.state = stValid
	return in, nil
}

// refuse builds the error for a file the table must not touch.
func refuse(rule Rule, in inspection) error {
	// stCorrupt: its superblock is ours, so this is corruption, not a
	// foreign file (DESIGN §13.3; design note D1). See RefusalError.
	return &RefusalError{Rule: rule, File: in.name, Corrupt: in.state == stCorrupt, Cause: in.why}
}

// Resolve runs the recovery authority table (DESIGN §11.4) for the database
// at path and returns once `path` holds a database whose superblock is valid.
//
// Precondition: lk must be the held lock for path; Resolve checks this
// before touching any file. Resolve never overwrites a file (all moves are
// non-replacing), never deletes `path`, never deletes a valid `.old` or a
// sealed `.compact`/`.new` while it could be the only copy, and on any
// refusal or I/O error while checking it stops without changing anything.
//
// Rule order (design deviation D3): A1, A2, A3, A4, A6, A5, A7. A6 (a damaged
// .old) is checked before A5 (adopt a sealed .new) so that a possibly-only
// .old is never buried under a fresh database and then deleted by A1.
//
// A temp file whose superblock decodes but whose snapshot_end lies beyond
// EOF (stCorrupt) is refused with ErrCorruption rather than treated as
// unsealed and deleted (design note D1): it is ours and a kill cannot cause
// it, so deleting it could destroy the only evidence of the data.
func Resolve(fsys fileio.FS, lk *locking.Lock, path string, policy fileio.RetryPolicy) (*Resolution, error) {
	if err := lk.Check(path); err != nil {
		return nil, fmt.Errorf("recovery: resolve %s: %w", path, err)
	}
	compact, old, newName := Names(path)
	dir := filepath.Dir(path)
	res := &Resolution{Path: path}

	for restarts := 0; ; restarts++ {
		if restarts > MaxRestarts {
			return nil, fmt.Errorf("recovery: internal error: authority table did not settle after %d restarts (rules %v)", MaxRestarts, res.Rules)
		}

		p, err := inspect(fsys, path, policy)
		if err != nil {
			return nil, err
		}
		switch p.state {
		case stValid: // A1
			res.Rules = append(res.Rules, RuleA1)
			res.Superblock, res.Size = p.sb, p.size
			for _, t := range []string{compact, old, newName} {
				if _, err := fsys.Stat(t); !errors.Is(err, fs.ErrNotExist) {
					res.Stale = append(res.Stale, t) // exists, or unknown: try later
				}
			}
			return res, nil
		case stInvalid, stCorrupt, stOther: // A2
			return nil, refuse(RuleA2, p)
		}

		// `path` is missing.
		c, err := inspect(fsys, compact, policy)
		if err != nil {
			return nil, err
		}
		switch c.state {
		case stValid: // A3
			if err := adopt(fsys, compact, path, dir, policy); err != nil {
				return nil, err
			}
			res.Rules = append(res.Rules, RuleA3)
			continue
		case stCorrupt, stOther:
			return nil, refuse(RuleA3, c)
		}

		o, err := inspect(fsys, old, policy)
		if err != nil {
			return nil, err
		}
		switch o.state {
		case stValid: // A4
			if err := adopt(fsys, old, path, dir, policy); err != nil {
				return nil, err
			}
			res.Rules = append(res.Rules, RuleA4)
			continue
		case stInvalid, stCorrupt, stOther: // A6
			return nil, refuse(RuleA6, o)
		}

		n, err := inspect(fsys, newName, policy)
		if err != nil {
			return nil, err
		}
		switch n.state {
		case stValid: // A5
			if err := adopt(fsys, newName, path, dir, policy); err != nil {
				return nil, err
			}
			res.Rules = append(res.Rules, RuleA5)
			continue
		case stCorrupt, stOther:
			return nil, refuse(RuleA5, n)
		}

		// A7: path and .old missing; .compact and .new missing or unsealed.
		// Unsealed temps must be deleted first, and the deletion must
		// succeed, otherwise the exclusive create below could not proceed
		// and a later Open could mistake leftovers for state.
		for _, in := range []inspection{c, n} {
			if in.state != stInvalid {
				continue
			}
			if err := remove(fsys, in.name, policy); err != nil {
				return nil, fmt.Errorf("recovery: rule A7: deleting unsealed %s: %w", in.name, err)
			}
		}
		if err := createFresh(fsys, newName, path, dir, policy); err != nil {
			return nil, err
		}
		res.Rules = append(res.Rules, RuleA7)
		res.Created = true
	}
}

// adopt moves a sealed/valid file into place with a non-replacing move and
// makes the rename durable where the platform needs it (A3, A4, A5).
func adopt(fsys fileio.FS, from, to, dir string, policy fileio.RetryPolicy) error {
	if _, err := fileio.Retry(policy, retryable, func() error { return fsys.MoveNoReplace(from, to) }); err != nil {
		return fmt.Errorf("recovery: moving %s to %s: %w", from, to, err)
	}
	if err := fsys.SyncDir(dir); err != nil {
		return fmt.Errorf("recovery: syncing directory %s after move: %w", dir, err)
	}
	return nil
}

func remove(fsys fileio.FS, name string, policy fileio.RetryPolicy) error {
	_, err := fileio.Retry(policy, retryable, func() error { return fsys.Remove(name) })
	return err
}

// createFresh implements the creation half of rule A7 (DESIGN §11.4, §17):
//
//	exclusive create .new -> write superblock -> Sync -> Close ->
//	MoveNoReplace(.new -> path) -> SyncDir
//
// The file becomes visible as `path` only after its superblock is written
// and synced, so `path` is never an empty or partial file. A kill at any
// step leaves either nothing, an unsealed .new (deleted by the next A7), or
// a sealed .new (adopted by the next A5). On error the .new is left as it
// is for the next Open to resolve; Close happens before the move because
// Windows refuses to move a file with an open handle.
func createFresh(fsys fileio.FS, newName, path, dir string, policy fileio.RetryPolicy) error {
	sb, err := format.NewSuperblock().Encode()
	if err != nil {
		return fmt.Errorf("recovery: encoding superblock: %w", err) // cannot happen
	}
	f, err := fsys.CreateExclusive(newName)
	if err != nil {
		return fmt.Errorf("recovery: rule A7: creating %s: %w", newName, err)
	}
	if _, err := fileio.WriteFullAt(f, sb[:], 0); err != nil {
		f.Close()
		return fmt.Errorf("recovery: rule A7: writing superblock to %s: %w", newName, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("recovery: rule A7: syncing %s: %w", newName, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("recovery: rule A7: closing %s: %w", newName, err)
	}
	return adopt(fsys, newName, path, dir, policy)
}

// RemoveStale deletes the temporary files A1 found next to a valid `path`,
// best-effort (DESIGN §11.4 A1 cleanup). Open calls it only after the
// recovery scan has accepted `path`. Files that are already gone are not
// errors; other failures are returned for logging and are not fatal.
func RemoveStale(fsys fileio.FS, lk *locking.Lock, r *Resolution, policy fileio.RetryPolicy) []error {
	if err := lk.Check(r.Path); err != nil {
		return []error{err}
	}
	var errs []error
	for _, t := range r.Stale {
		if err := remove(fsys, t, policy); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("recovery: deleting stale %s: %w", t, err))
		}
	}
	return errs
}

// Options configures Prepare.
type Options struct {
	LockRetry fileio.RetryPolicy // retry of a busy lock before ErrLocked
	IORetry   fileio.RetryPolicy // retry of sharing violations on reads, moves, deletes
}

// DefaultOptions uses fileio.DefaultRetry for both.
var DefaultOptions = Options{LockRetry: fileio.DefaultRetry, IORetry: fileio.DefaultRetry}

// Prepare is the first part of Open: take the process lock (always the
// first filesystem operation, DESIGN §16.1), then resolve the authority
// table. On any error the lock is released and no handle is returned. On
// success the caller owns the lock and must release it on Close.
func Prepare(fsys fileio.FS, path string, opt Options) (*locking.Lock, *Resolution, error) {
	lk, err := locking.Acquire(fsys, path, opt.LockRetry)
	if err != nil {
		return nil, nil, err
	}
	res, err := Resolve(fsys, lk, path, opt.IORetry)
	if err != nil {
		lk.Release()
		return nil, nil, err
	}
	return lk, res, nil
}
