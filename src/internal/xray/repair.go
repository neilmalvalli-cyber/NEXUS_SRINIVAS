package xray

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/kvdb"
)

// RepairResult records an explicit repair (Repair). "Repaired" is claimed
// only when Verified is true.
type RepairResult struct {
	Backups     []string  `json:"backups"` // copies made before anything was changed
	Planned     []string  `json:"planned"` // Inspection.Repairs() before the open
	OpenError   string    `json:"open_error,omitempty"`
	Applied     []string  `json:"applied,omitempty"` // what kvdb.Open reports it did
	VerifyError string    `json:"verify_error,omitempty"`
	Keys        *int      `json:"keys,omitempty"`
	After       *Summary  `json:"after,omitempty"` // fresh read-only X-RAY after the repair
	Verified    bool      `json:"verified"`
	At          time.Time `json:"at"`
}

// Summary is a short form of a Report (used for the post-repair scan).
type Summary struct {
	Health   string           `json:"health"`
	Outcome  string           `json:"engine_outcome"`
	Counts   map[Severity]int `json:"counts"`
	Findings []string         `json:"finding_ids"`
}

func summary(r *Report) *Summary {
	s := &Summary{Health: r.Health, Outcome: r.Engine.Outcome, Counts: r.Counts}
	for _, f := range r.Findings {
		s.Findings = append(s.Findings, f.ID)
	}
	return s
}

// Repair is the only X-RAY operation that changes files. It must never be
// pointed at a database without the owner's explicit confirmation.
//
//  1. Scan read-only. If the engine refuses the file (corruption, bad
//     format) Repair stops: NEXUS never "repairs" committed data, and
//     neither does X-RAY.
//  2. Copy every existing file of the database (path, .compact, .old,
//     .new) to backupDir.
//  3. Open with the real kvdb.Open, which applies exactly the repairs the
//     read-only plan listed (torn-tail truncation, authority-rule moves,
//     stale-file deletion), then Verify and Close.
//  4. Scan read-only again. Findings of the first scan become "repaired"
//     only if the open, Verify and the second scan all came back clean.
func Repair(path, backupDir string, opt Options) (*Report, error) {
	r, err := Scan(path, opt)
	if err != nil {
		return nil, err
	}
	r.Mode = "repair"
	rr := &RepairResult{Planned: r.Engine.Repairs, At: time.Now().UTC()}
	r.Repair = rr
	switch r.Engine.Outcome {
	case "clean":
		if len(r.Engine.Repairs) == 0 {
			rr.Verified = true
			rr.After = summary(r)
			return r, nil // nothing to do; nothing touched
		}
	case "repairable":
		if r.Walk != nil && r.Walk.LaterFrameOffset != nil {
			rr.OpenError = "not attempted: the torn-tail repair would truncate a committed frame (see FRAME_HEADER_DAMAGED); refusing to lose acknowledged data"
			return r, nil
		}
	default:
		rr.OpenError = "not attempted: engine outcome is " + r.Engine.Outcome + "; only crash leftovers are repaired, never committed data"
		return r, nil
	}
	if opt.OwnerHoldsLock {
		rr.OpenError = "not attempted: the database is open by its owner"
		return r, nil
	}

	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, fmt.Errorf("xray: backup dir: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405.000Z")
	compact, old, newName := recovery.Names(path)
	for _, src := range []string{path, compact, old, newName} {
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := fmt.Sprintf("%s/%s.%s.bak", backupDir, baseName(src), stamp)
		if err := copyFile(src, dst); err != nil {
			return nil, fmt.Errorf("xray: backup of %s failed, nothing was changed: %w", src, err)
		}
		rr.Backups = append(rr.Backups, dst)
	}

	db, err := kvdb.Open(path, 0)
	if err != nil {
		rr.OpenError = err.Error()
		return r, nil
	}
	ri := db.Recovery()
	for _, rl := range ri.Rules {
		if rl != "A1" {
			rr.Applied = append(rr.Applied, "authority rule "+rl+" applied")
		}
	}
	if ri.Tail != "" {
		rr.Applied = append(rr.Applied, fmt.Sprintf("truncated a torn %d-byte tail (rule %s)", ri.DiscardedBytes, ri.Tail))
	}
	if len(r.Engine.Stale) > 0 {
		rr.Applied = append(rr.Applied, fmt.Sprintf("stale temp files removed: %v (warnings: %v)", r.Engine.Stale, ri.Warnings))
	}
	if _, verr := db.Verify(); verr != nil {
		rr.VerifyError = verr.Error()
	}
	k := db.Stats().Keys
	rr.Keys = &k
	if cerr := db.Close(); cerr != nil && rr.VerifyError == "" {
		rr.VerifyError = "close: " + cerr.Error()
	}

	after, err := Scan(path, opt)
	if err != nil {
		return nil, err
	}
	rr.After = summary(after)
	rr.Verified = rr.OpenError == "" && rr.VerifyError == "" && after.Engine.Outcome == "clean" &&
		len(after.Engine.Repairs) == 0 && after.Counts[Critical] == 0 && after.Counts[Error] == 0
	r.Recovery.FileModified = true
	r.Recovery.Truncated = ri.Tail != ""
	r.After = after.After
	r.Modified = true
	if rr.Verified {
		r.Recovery.Status = Repaired
		for i := range r.Findings {
			if r.Findings[i].Recovery == Recoverable {
				r.Findings[i].Recovery = Repaired
			}
		}
	}
	return r, nil
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
