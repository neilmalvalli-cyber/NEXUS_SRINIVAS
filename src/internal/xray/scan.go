package xray

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/admission"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/fileio"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/locking"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
)

// Options control a scan.
type Options struct {
	// OwnerHoldsLock: the caller has the database open itself and has
	// excluded its own writers for the duration of the scan (the nexus-api
	// adapter does this for its live database). The lock probe is skipped
	// and the report is marked Live.
	OwnerHoldsLock bool
	// NoHash skips the SHA-256 fingerprints (size and mtime are still
	// compared). Hashing reads the file twice more.
	NoHash bool
	// DeadRatioWarn is the dead-byte fraction above which a WARNING is
	// reported (default 0.5, the auto-compaction threshold: end/2 > live).
	DeadRatioWarn float64
	// DeadMinBytes: no dead-byte warning below this log size (default
	// admission.AutoCompactMinSize, the engine's own auto-compaction floor;
	// on small files frame headers alone exceed half the bytes).
	DeadMinBytes int64
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Scan runs a read-only X-RAY of the database at path. It returns an error
// only for a usage problem (empty path); everything X-RAY finds about the
// file, including I/O errors while reading it, is a finding in the report.
func Scan(path string, opt Options) (*Report, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty path", dberr.ErrInvalidArgument)
	}
	now := time.Now
	if opt.Now != nil {
		now = opt.Now
	}
	if opt.DeadRatioWarn == 0 {
		opt.DeadRatioWarn = 0.5
	}
	if opt.DeadMinBytes == 0 {
		opt.DeadMinBytes = admission.AutoCompactMinSize
	}
	r := &Report{Tool: "nexus-xray", Version: ReportVersion, Path: path, Mode: "read-only",
		Live: opt.OwnerHoldsLock, StartedAt: now().UTC(), Findings: []Finding{}}
	x := &walker{r: r, file: path, deadRatio: opt.DeadRatioWarn, deadMin: opt.DeadMinBytes}
	ts := r.StartedAt

	r.Before = fingerprint(path, !opt.NoHash)

	// 1. Lock probe, without creating anything. The lock file is never
	// deleted (DESIGN §16.1), so if it does not exist nobody holds it.
	lockHeld := false
	if !opt.OwnerHoldsLock {
		lockHeld = probeLock(path, x)
	}

	// 2. Engine verdict.
	in, perr := recovery.Plan(fileio.OS{}, path, recovery.DefaultOptions)
	x.engine(in, perr)

	// 3. Walk the file the engine looked at (or refused).
	walkTarget := ""
	switch {
	case in != nil && in.Target != "":
		walkTarget = in.Target
	case r.Engine.RefusedFile != "":
		walkTarget = r.Engine.RefusedFile
	}
	if walkTarget != "" {
		if fi, err := os.Stat(walkTarget); err == nil && fi.Mode().IsRegular() {
			x.file = walkTarget
			if err := x.walkFile(walkTarget); err != nil {
				x.ioFinding("XRAY_READ_ERROR", walkTarget, err)
			}
		}
	}

	// 4. Cross-check and summarise.
	x.crossCheck()
	x.summarise(in, lockHeld)

	r.After = fingerprint(path, !opt.NoHash)
	r.Modified = r.Before.Exists != r.After.Exists || r.Before.Size != r.After.Size ||
		!r.Before.ModTime.Equal(r.After.ModTime) || r.Before.SHA256 != r.After.SHA256
	if r.Modified {
		why := "The file changed while X-RAY was reading it. X-RAY itself only reads."
		if r.Live || lockHeld {
			why += " The database was open by a writer, which explains the change."
		}
		x.add(Finding{Code: "XRAY_FILE_CHANGED", Severity: Warning, Category: CatOperational,
			Title: "Database file changed during the scan", Simple: why,
			Technical: fmt.Sprintf("before: size %d mtime %s; after: size %d mtime %s", r.Before.Size, r.Before.ModTime.Format(time.RFC3339Nano), r.After.Size, r.After.ModTime.Format(time.RFC3339Nano)),
			Component: "xray", DataImpact: "Findings describe the file as it was being read; a torn tail may be a write in progress.",
			Recovery: Detected, Action: "Re-run X-RAY when the database is idle or closed."})
	}
	for i := range r.Findings {
		if r.Findings[i].Timestamp.IsZero() {
			r.Findings[i].Timestamp = ts
		}
	}
	finish(r, now)
	return r, nil
}

// finish sorts findings (worst first, then by offset), counts them and sets
// Health and timing.
func finish(r *Report, now func() time.Time) {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.Severity.Rank() != b.Severity.Rank() {
			return a.Severity.Rank() > b.Severity.Rank()
		}
		return off(a) < off(b)
	})
	r.Counts = map[Severity]int{Info: 0, Warning: 0, Error: 0, Critical: 0}
	worst := 0
	for _, f := range r.Findings {
		r.Counts[f.Severity]++
		if f.Severity.Rank() > worst {
			worst = f.Severity.Rank()
		}
	}
	r.Health = []string{"healthy", "attention", "degraded", "critical"}[worst]
	r.FinishedAt = now().UTC()
	r.DurationMS = float64(r.FinishedAt.Sub(r.StartedAt).Microseconds()) / 1000
}

func off(f Finding) int64 {
	if f.Offset == nil {
		return -1
	}
	return *f.Offset
}

// add appends a finding, giving it a stable ID.
func (x *walker) add(f Finding) {
	if f.File == "" {
		f.File = x.file
	}
	if f.Offset != nil {
		f.ID = fmt.Sprintf("%s@%d", f.Code, *f.Offset)
	} else {
		f.ID = fmt.Sprintf("%s@%s", f.Code, filepath.Base(f.File))
	}
	for _, g := range x.r.Findings { // one finding per ID
		if g.ID == f.ID {
			return
		}
	}
	x.r.Findings = append(x.r.Findings, f)
}

func probeLock(path string, x *walker) bool {
	lp := locking.Path(path)
	if _, err := os.Stat(lp); err != nil {
		return false // absent: nobody holds it, and probing would create it
	}
	c, err := fileio.OS{}.Lock(lp) // opens the existing file; does not change its contents
	if err == nil {
		_ = c.Close()
		return false
	}
	if platform.IsLockBusy(err) {
		x.add(Finding{Code: "LOCK_HELD", Severity: Warning, Category: CatOperational, File: lp,
			Title:     "Database is open in another process",
			Simple:    "Another process has this database open right now. X-RAY still reads it, but a write may be in progress, so the end of the file can look incomplete.",
			Technical: "exclusive lock on " + lp + " is held (sharing violation / EWOULDBLOCK on probe)",
			Component: "locking", DataImpact: "None by itself. Findings about the final frame may describe a write in progress.",
			Recovery: Detected, Action: "Re-run X-RAY after the owning process closes the database for a definitive result.",
			OriginalError: err.Error()})
		return true
	}
	return false
}

func (x *walker) ioFinding(code, file string, err error) {
	x.add(Finding{Code: code, Severity: Error, Category: CatOperational, File: file,
		Title:     "File could not be read",
		Simple:    "X-RAY could not read part of the file because the operating system reported an error. This is not evidence of corruption.",
		Technical: err.Error(),
		Component: "fileio", DataImpact: "Unknown: the affected bytes could not be checked.",
		Recovery: Detected, Action: "Check disk health, permissions and antivirus locks, then re-run X-RAY.", OriginalError: err.Error()})
}

// engine records the recovery.Plan verdict and the findings that come only
// from it (authority rules, stale temp files, refusals of temp files).
func (x *walker) engine(in *recovery.Inspection, err error) {
	e := &x.r.Engine
	if in != nil {
		for _, rl := range in.Rules {
			e.Rules = append(e.Rules, string(rl))
		}
		e.Target, e.Stale = in.Target, in.Stale
		e.Repairs = in.Repairs()
	}
	var se *recovery.ScanError
	var re *recovery.RefusalError
	switch {
	case err == nil && in != nil && in.Missing:
		e.Outcome = "missing"
	case err == nil:
		s := in.Scan
		e.Frames, e.NextSeq = u64(s.Frames), u64(s.NextSeq)
		k := s.Index.Len()
		e.Keys = &k
		lb, db := s.Index.LiveBytes(), s.DeadBytes()
		e.LiveBytes, e.DeadBytes = &lb, &db
		if s.Tail != recovery.TailClean {
			e.Tail, e.TailOffset, e.TailBytes = string(s.Tail), i64(s.End), i64(s.Size-s.End)
		}
		if in.NeedsRepair() {
			e.Outcome = "repairable"
		} else {
			e.Outcome = "clean"
		}
	case errors.As(err, &se):
		e.Outcome, e.ErrorClass, e.Error = "refused", "corruption", err.Error()
		e.ScanRule, e.ScanOffset = se.Rule, i64(int64(se.Offset))
	case errors.As(err, &re):
		e.Outcome, e.Error = "refused", err.Error()
		e.RefusedFile, e.RefusedRule = re.File, string(re.Rule)
		if re.Corrupt {
			e.ErrorClass = "corruption"
		} else {
			e.ErrorClass = "bad-format"
		}
	case errors.Is(err, dberr.ErrCorruption):
		e.Outcome, e.ErrorClass, e.Error = "refused", "corruption", err.Error()
	case errors.Is(err, dberr.ErrBadFormat):
		e.Outcome, e.ErrorClass, e.Error = "refused", "bad-format", err.Error()
	default:
		e.Outcome, e.ErrorClass, e.Error = "io_error", "io", err.Error()
		x.ioFinding("ENGINE_IO_ERROR", x.file, err)
	}

	path := x.r.Path
	compact, old, newName := recovery.Names(path)
	if re != nil && re.File != path {
		// A temp file was refused (A3/A5 corrupt, A6 .old invalid). The walk
		// describes the bytes; this says why it matters.
		what := map[string]string{compact: "compaction output (.compact)", old: "pre-compaction copy (.old)", newName: "new-database file (.new)"}[re.File]
		x.add(Finding{Code: "RECOVERY_TEMP_REFUSED", Severity: Critical, Category: CatCompaction, File: re.File, Rule: string(re.Rule),
			Title:     "Leftover file from an interrupted compaction is damaged",
			Simple:    fmt.Sprintf("The database file is missing and the %s that recovery would use instead is damaged. NEXUS will not open, and will not delete that file, because it may be the only copy of your data.", what),
			Technical: fmt.Sprintf("authority rule %s refuses %s: %v", re.Rule, re.File, re.Cause),
			Component: "recovery/authority", DataImpact: "The database cannot be opened until this is resolved.",
			Recovery: Refused, Action: "Do not delete any .compact/.old/.new file. Restore from a backup, or keep all files for analysis.",
			OriginalError: err.Error()})
	}
	if in == nil {
		return
	}
	if len(in.Rules) > 0 && in.Rules[0] != recovery.RuleA1 {
		rl := in.Rules[0]
		f := Finding{Category: CatCompaction, Severity: Warning, File: in.Target, Rule: string(rl),
			Component: "recovery/authority", Recovery: Recoverable}
		switch rl {
		case recovery.RuleA3:
			f.Code, f.Title = "COMPACTION_INTERRUPTED_SWAP", "Compaction was interrupted during the file swap"
			f.Simple = "Compaction had finished and sealed the new file, then stopped between its two renames: the old file was moved aside but the new one was not yet moved into place. A normal open finishes the swap."
			f.Technical = fmt.Sprintf("%s missing, %s sealed: rule A3 will MoveNoReplace(.compact -> path) and restart at A1", path, compact)
			f.DataImpact = "None. The sealed .compact holds the complete state (a compacting commit's own operation may or may not appear; it was never acknowledged)."
		case recovery.RuleA4:
			f.Code, f.Title = "COMPACTION_RESTORE_OLD", "Database file missing; the pre-compaction copy is intact"
			f.Simple = "The database file is missing, but the copy compaction moved aside (.old) is intact. A normal open moves it back into place."
			f.Technical = fmt.Sprintf("%s missing, %s valid: rule A4 will MoveNoReplace(.old -> path)", path, old)
			f.DataImpact = "None for acknowledged data: .old holds the state from before the compaction."
		case recovery.RuleA5:
			f.Code, f.Title, f.Category = "CREATION_INTERRUPTED", "Database creation was interrupted", CatRecovery
			f.Simple = "A new database was being created and stopped just before its file was moved into place. A normal open finishes the creation."
			f.Technical = fmt.Sprintf("%s missing, %s sealed: rule A5 will MoveNoReplace(.new -> path)", path, newName)
			f.DataImpact = "None: the database was empty."
		}
		f.Action = "Open the database normally, or run `kvdb xray --repair`; the open applies the rule shown."
		if err != nil {
			f.Recovery = Refused
			f.Action = "The adopted file itself fails validation; see the other findings."
		}
		x.add(f)
	}
	for _, s := range in.Stale {
		f := Finding{Severity: Warning, Category: CatCompaction, File: s, Component: "recovery/authority",
			Recovery: Recoverable, Rule: "A1",
			DataImpact: "None. The database file is authoritative; this leftover is not used.",
			Action:     "A normal open deletes it after the database has been validated. No manual action needed."}
		switch s {
		case compact:
			f.Code, f.Title = "COMPACTION_LEFTOVER_COMPACT", "Leftover output from an interrupted compaction"
			f.Simple = "A compaction was interrupted before it replaced the database file. Its unfinished output file is still on disk. The database file itself was never touched."
		case old:
			f.Code, f.Title = "COMPACTION_LEFTOVER_OLD", "Leftover pre-compaction copy"
			f.Simple = "A compaction completed, but its final clean-up (deleting the old copy) did not run."
		default:
			f.Code, f.Title, f.Category = "CREATION_LEFTOVER", "Leftover file from database creation", CatRecovery
			f.Simple = "A file left over from creating a database is still on disk. It is not used."
		}
		f.Technical = fmt.Sprintf("%s exists while %s is valid (rule A1: stale, removed after a successful scan)", s, path)
		if in.Missing {
			f.Technical = fmt.Sprintf("%s is unsealed and no database exists (rule A7 deletes it and creates a fresh database)", s)
			f.DataImpact = "None: an unsealed temp file holds no committed data."
		}
		x.add(f)
	}
}

// crossCheck compares the engine verdict and the walk. They are independent
// readings of the same bytes and must agree.
func (x *walker) crossCheck() {
	e, w := x.r.Engine, x.r.Walk
	if w == nil || e.Target == "" || w.File != e.Target {
		return
	}
	var bad []string
	switch e.Outcome {
	case "clean", "repairable":
		if w.StopRule != e.Tail {
			bad = append(bad, fmt.Sprintf("tail rule: engine %q, walk %q", e.Tail, w.StopRule))
		}
		if e.Frames != nil && *e.Frames != w.Frames {
			bad = append(bad, fmt.Sprintf("valid frames: engine %d, walk %d", *e.Frames, w.Frames))
		}
	case "refused":
		if e.ScanRule != "" && e.ScanRule != w.StopRule {
			bad = append(bad, fmt.Sprintf("refusal rule: engine %s, walk %q", e.ScanRule, w.StopRule))
		}
		if e.ScanOffset != nil && w.StoppedAt != nil && *e.ScanOffset != *w.StoppedAt {
			bad = append(bad, fmt.Sprintf("failing frame offset: engine %d, walk %d", *e.ScanOffset, *w.StoppedAt))
		}
	}
	if len(bad) > 0 {
		x.add(Finding{Code: "XRAY_ENGINE_DISAGREEMENT", Severity: Error, Category: CatOperational,
			Title:     "X-RAY and the recovery engine disagree",
			Simple:    "X-RAY's own byte walk and the database's recovery engine read this file differently. The engine's verdict decides what open does; please report this.",
			Technical: fmt.Sprint(bad),
			Component: "xray", DataImpact: "Unknown; the engine verdict is authoritative.",
			Recovery: Detected, Action: "Report this with the JSON output. Do not modify the file."})
	}
}

// summarise fills Recovery and the summary findings.
func (x *walker) summarise(in *recovery.Inspection, lockHeld bool) {
	e, w, rc := x.r.Engine, x.r.Walk, &x.r.Recovery
	if w != nil {
		rc.ValidFramesReplayed, rc.ValidRecordsReplay = w.Frames, w.Entries
		rc.ScanStoppedAt = w.StoppedAt
		if w.StoppedAt != nil {
			rc.FailedFrame = u64(w.Frames + 1)
		}
	}
	switch e.Outcome {
	case "clean":
		rc.Status, rc.OpenSucceeds, rc.IndexRebuilt = NotApplicable, true, true
		rc.DataLoss = "none"
		if len(e.Stale) > 0 || (len(e.Rules) > 0 && e.Rules[0] != "A1") {
			rc.Status = Recoverable
		}
	case "repairable":
		rc.Status, rc.OpenSucceeds, rc.IndexRebuilt = Recoverable, true, true
		rc.DataLoss = "none of the acknowledged data: only bytes of a write that was never acknowledged would be discarded (docs/FAILURE_GUARANTEES.md)"
		if e.TailBytes != nil {
			rc.WouldTruncateBytes = e.TailBytes
		}
		if w != nil && w.LaterFrameOffset != nil {
			rc.Status = Detected
			rc.DataLoss = fmt.Sprintf("YES if opened normally: the %d bytes open would truncate contain a committed frame (seq %d at offset %d)", deref64(e.TailBytes), *w.LaterFrameSeq, *w.LaterFrameOffset)
		}
	case "missing":
		rc.Status, rc.OpenSucceeds, rc.DataLoss = Detected, true, "none: no database exists; open would create an empty one (rule A7)"
	case "refused":
		rc.Status, rc.DataLoss = Refused, "possible: committed data fails validation; NEXUS refuses to open instead of discarding it"
		for _, f := range x.r.Findings { // the summary follows the findings
			if f.Recovery == Unrecoverable {
				rc.Status = Unrecoverable
				rc.DataLoss = "the file holds no readable NEXUS state"
			}
		}
		if w != nil && w.Superblock != nil && w.Frames > 0 {
			rc.DataLoss += fmt.Sprintf("; %d frames (%d operations) before offset %d validate", w.Frames, w.Entries, w.ValidEnd)
		}
	default:
		rc.Status, rc.DataLoss = Detected, "unknown: the file could not be read"
	}

	// Summary findings: facts, only from what was measured.
	if e.Outcome == "missing" {
		x.add(Finding{Code: "DB_MISSING", Severity: Info, Category: CatSummary,
			Title: "No database at this path", Simple: "There is no database file here yet. Opening the path would create a new, empty database.",
			Technical: "rule A7: no usable file among path, .compact, .old, .new", Component: "recovery/authority",
			DataImpact: "None.", Recovery: NotApplicable, Action: "None, unless you expected a database here: check the path."})
	}
	if e.Outcome == "clean" && w != nil {
		x.add(Finding{Code: "SCAN_CLEAN", Severity: Info, Category: CatSummary,
			Title:     "All frames validated",
			Simple:    fmt.Sprintf("Every byte of the database was checked. %d frames holding %d operations passed all checksum, length and sequence checks, and the file ends exactly on a frame boundary.", w.Frames, w.Entries),
			Technical: fmt.Sprintf("superblock v%d first_seq %d snapshot_end %d; %d frames, %d PUT, %d DEL; end %d = size; next_seq %d; %d live keys", w.Superblock.Version, w.Superblock.FirstSeq, w.Superblock.SnapshotEnd, w.Frames, w.Puts, w.Dels, w.ValidEnd, deref(e.NextSeq), derefInt(e.Keys)),
			Component: "recovery/scan", DataImpact: "None.", Recovery: NotApplicable, Action: "None."})
	}
	if (e.Outcome == "clean" || e.Outcome == "repairable") && e.DeadBytes != nil && w != nil && w.ValidEnd > 0 && w.ValidEnd >= x.deadMin {
		dead := float64(*e.DeadBytes) / float64(w.ValidEnd)
		if dead > x.ratio() {
			x.add(Finding{Code: "SPACE_DEAD_BYTES", Severity: Warning, Category: CatCompaction,
				Title:     "Most of the file is obsolete data",
				Simple:    fmt.Sprintf("%.0f%% of the log holds overwritten or deleted values. Compaction would reclaim this space.", dead*100),
				Technical: fmt.Sprintf("dead bytes %d of %d (live %d); threshold %.0f%%", *e.DeadBytes, w.ValidEnd, deref(e.LiveBytes), x.ratio()*100),
				Component: "admission", DataImpact: "None; uses disk space and lengthens recovery.",
				Recovery: NotApplicable, Action: "Run compaction (kvdb serve: compact, or the dashboard's Compact action)."})
		}
	}
}

func (x *walker) ratio() float64 { return x.deadRatio }

func deref(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// fingerprint reads size, mtime and (optionally) SHA-256 of a file.
func fingerprint(path string, hash bool) Fingerprint {
	fi, err := os.Stat(path)
	if err != nil {
		return Fingerprint{Exists: !errors.Is(err, fs.ErrNotExist)}
	}
	fp := Fingerprint{Exists: true, Size: fi.Size(), ModTime: fi.ModTime().UTC()}
	if hash && fi.Mode().IsRegular() {
		if f, err := os.Open(path); err == nil {
			h := sha256.New()
			if _, err := io.Copy(h, f); err == nil {
				fp.SHA256 = hex.EncodeToString(h.Sum(nil))
			}
			f.Close()
		}
	}
	return fp
}

func deref64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
