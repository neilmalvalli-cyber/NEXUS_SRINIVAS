// Package xray is NEXUS X-RAY: structured, evidence-based diagnostics for a
// NEXUS database file (docs/XRAY_ARCHITECTURE.md, docs/XRAY_DIAGNOSTICS.md).
//
// X-RAY never guesses. Every finding comes from a check that failed on real
// bytes (a magic number, a stored versus computed CRC32C, a sequence number,
// a length that runs past end of file) or from a structured engine error
// (errors.Is / errors.As on dberr sentinels, *format.FieldError,
// *recovery.ScanError, *recovery.RefusalError). Error messages are never
// parsed.
//
// Two independent sources are combined:
//
//   - the engine verdict: recovery.Plan, the lock-free, read-only form of
//     what kvdb.Open would do (same authority table, same Scan, same tail
//     rules C0–C8);
//   - the X-RAY walk: a frame-by-frame byte walk with the format decoders
//     that records exact offsets, stored and computed checksums and
//     expected and actual values.
//
// The two must agree on the outcome; if they ever disagree that is itself
// reported (XRAY_ENGINE_DISAGREEMENT) rather than hidden.
//
// Read-only by default: Scan opens files with os.Open only and never
// creates, writes, truncates, renames or deletes anything, including the
// lock file. Repair (repair.go) is a separate, explicit call that backs the
// file up first and then uses the real kvdb.Open.
package xray

import "time"

// Severity is matched to real impact (docs/XRAY_DIAGNOSTICS.md §2).
type Severity string

const (
	// Info: a fact, nothing is wrong.
	Info Severity = "INFO"
	// Warning: needs attention, no acknowledged data is lost (a torn tail
	// open would cut, an interrupted compaction open would finish, a held
	// lock, leftover temp files, a high dead-byte ratio).
	Warning Severity = "WARNING"
	// Error: an operation failed or could not be checked, stored data is
	// not shown to be damaged (I/O errors, a failed handle, an unknown
	// outcome, max_bytes reached).
	Error Severity = "ERROR"
	// Critical: NEXUS refuses to open the database because committed bytes
	// fail validation, or the file is not a usable NEXUS database.
	Critical Severity = "CRITICAL"
)

var severityRank = map[Severity]int{Info: 0, Warning: 1, Error: 2, Critical: 3}

// Rank orders severities (INFO 0 … CRITICAL 3).
func (s Severity) Rank() int { return severityRank[s] }

// Category groups findings by the part of the format or system involved.
type Category string

const (
	CatSuperblock  Category = "superblock"
	CatFrame       Category = "frame"
	CatSequence    Category = "sequence"
	CatOperation   Category = "operation"
	CatRecovery    Category = "recovery"
	CatCompaction  Category = "compaction"
	CatOperational Category = "operational"
	CatSummary     Category = "summary"
)

// RecoveryStatus says where a finding stands with respect to recovery.
// "Recovered" and "repaired" are only ever set after a verification that
// actually ran (repair.go); a read-only scan can at most say "recoverable".
type RecoveryStatus string

const (
	// NotApplicable: informational finding, nothing to recover.
	NotApplicable RecoveryStatus = "not_applicable"
	// Detected: found; no automatic recovery applies or is needed to open
	// (for example a held lock or an operational error).
	Detected RecoveryStatus = "detected"
	// Recoverable: the engine's own read-only plan shows that a normal open
	// repairs this automatically without losing acknowledged data. Nothing
	// has been changed yet.
	Recoverable RecoveryStatus = "recoverable"
	// Repaired: an explicit repair ran kvdb.Open, which applied the repair,
	// and a fresh read-only X-RAY scan plus Verify afterwards came back
	// clean.
	Repaired RecoveryStatus = "repaired"
	// Recovered: the database was opened and its state rebuilt and verified
	// (reported by an engine that has just opened the file).
	Recovered RecoveryStatus = "recovered"
	// Refused: NEXUS refuses to open, to protect committed data; the file
	// is left untouched. A validated prefix exists (see the report).
	Refused RecoveryStatus = "refused"
	// Unrecoverable: no part of the file validates (the superblock itself is
	// unusable and no temp file can be adopted); NEXUS cannot rebuild any
	// state from it.
	Unrecoverable RecoveryStatus = "unrecoverable"
)

// NEXUS never partially recovers: frames are applied whole or not at all,
// and damage to committed bytes is refused, not skipped. There is therefore
// no "partially recovered" status; see docs/XRAY_DIAGNOSTICS.md §4.

// Finding is one diagnostic. Optional numeric fields are pointers so that
// "unknown / not applicable" is never shown as 0.
type Finding struct {
	ID        string   `json:"id"`   // stable: CODE@offset or CODE@file
	Code      string   `json:"code"` // machine code, e.g. FRAME_PAYLOAD_CHECKSUM
	Severity  Severity `json:"severity"`
	Category  Category `json:"category"`
	Title     string   `json:"title"`
	Simple    string   `json:"simple"`    // plain-language explanation
	Technical string   `json:"technical"` // exact rule, field and values
	File      string   `json:"file,omitempty"`
	Offset    *int64   `json:"offset,omitempty"`
	Frame     *uint64  `json:"frame,omitempty"` // 1-based frame number in the walk
	Seq       *uint64  `json:"seq,omitempty"`
	Expected  string   `json:"expected,omitempty"`
	Actual    string   `json:"actual,omitempty"`
	// Rule is the engine rule involved (C1…C8, A1…A7), when there is one.
	Rule          string         `json:"rule,omitempty"`
	Timestamp     time.Time      `json:"timestamp"`
	Component     string         `json:"component"`
	DataImpact    string         `json:"data_impact"`
	Recovery      RecoveryStatus `json:"recovery_status"`
	Action        string         `json:"recommended_action"`
	OriginalError string         `json:"original_error,omitempty"`
}

// Fingerprint identifies the bytes of a file at one moment. Comparing the
// fingerprints taken before and after a read-only scan proves the scan
// changed nothing.
type Fingerprint struct {
	Exists  bool      `json:"exists"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	SHA256  string    `json:"sha256,omitempty"`
}

// Engine is the verdict of recovery.Plan: what kvdb.Open would do.
type Engine struct {
	// Outcome: "clean", "repairable", "missing", "refused", "io_error".
	Outcome     string   `json:"outcome"`
	Rules       []string `json:"rules,omitempty"`
	Target      string   `json:"target,omitempty"`
	Repairs     []string `json:"repairs,omitempty"` // Inspection.Repairs(), verbatim
	Stale       []string `json:"stale,omitempty"`
	Tail        string   `json:"tail_rule,omitempty"`
	TailOffset  *int64   `json:"tail_offset,omitempty"`
	TailBytes   *int64   `json:"tail_bytes,omitempty"`
	Frames      *uint64  `json:"frames,omitempty"`
	Keys        *int     `json:"keys,omitempty"`
	NextSeq     *uint64  `json:"next_seq,omitempty"`
	LiveBytes   *uint64  `json:"live_bytes,omitempty"`
	DeadBytes   *uint64  `json:"dead_bytes,omitempty"`
	Error       string   `json:"error,omitempty"`
	ErrorClass  string   `json:"error_class,omitempty"` // corruption, bad-format, io
	RefusedFile string   `json:"refused_file,omitempty"`
	RefusedRule string   `json:"refused_rule,omitempty"`
	ScanRule    string   `json:"scan_rule,omitempty"`   // C0, C6, C7 from *recovery.ScanError
	ScanOffset  *int64   `json:"scan_offset,omitempty"` // offset from *recovery.ScanError
}

// Walk is what the X-RAY byte walk saw.
type Walk struct {
	File        string `json:"file"`
	Size        int64  `json:"size"`
	Superblock  *SB    `json:"superblock,omitempty"`
	Frames      uint64 `json:"frames_valid"`
	Entries     uint64 `json:"entries_valid"`
	Puts        uint64 `json:"puts"`
	Dels        uint64 `json:"dels"`
	ValidEnd    int64  `json:"valid_end"`            // end of the last valid frame
	StoppedAt   *int64 `json:"stopped_at,omitempty"` // offset of the frame that failed
	StopRule    string `json:"stop_rule,omitempty"`  // C0…C7 as the walk classified it
	TrailingLen int64  `json:"bytes_after_valid_end"`
	// NextFrameIntact is set for damage to a frame that does not end at
	// EOF: whether a CRC-valid header with the following sequence number
	// starts exactly where the damaged frame ends (evidence that committed
	// frames follow the damage).
	NextFrameIntact *bool `json:"next_frame_intact,omitempty"`
	// LaterFrame*: after a damaged header (C2), the first CRC-valid frame
	// header with a later sequence number found inside the region open
	// would truncate (diagnosis only; see walk.go laterFrames).
	LaterFrameOffset *int64  `json:"later_frame_offset,omitempty"`
	LaterFrameSeq    *uint64 `json:"later_frame_seq,omitempty"`
}

// SB is the decoded superblock.
type SB struct {
	Version     uint16 `json:"version"`
	FirstSeq    uint64 `json:"first_seq"`
	SnapshotEnd uint64 `json:"snapshot_end"`
}

// Recovery is the recovery-aware summary (docs/XRAY_DIAGNOSTICS.md §5).
type Recovery struct {
	Status              RecoveryStatus `json:"status"`
	ScanStoppedAt       *int64         `json:"scan_stopped_at,omitempty"`
	FailedFrame         *uint64        `json:"failed_frame,omitempty"`
	ValidFramesReplayed uint64         `json:"valid_frames_replayed"`
	ValidRecordsReplay  uint64         `json:"valid_records_replayed"`
	FileModified        bool           `json:"file_modified"`
	Truncated           bool           `json:"truncated"`
	WouldTruncateBytes  *int64         `json:"would_truncate_bytes,omitempty"`
	// IndexRebuilt: the in-memory index was rebuilt from the log by the
	// engine's Scan during this check (it is never written anywhere).
	IndexRebuilt bool   `json:"index_rebuilt_in_memory"`
	OpenSucceeds bool   `json:"open_would_succeed"`
	DataLoss     string `json:"possible_data_loss"`
}

// Report is the complete result of one X-RAY run.
type Report struct {
	Tool       string    `json:"tool"` // "nexus-xray"
	Version    int       `json:"version"`
	Path       string    `json:"path"`
	Mode       string    `json:"mode"` // "read-only" or "repair"
	Live       bool      `json:"live"` // scanned while the database was open by its owner
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	DurationMS float64   `json:"duration_ms"`
	// Health: "healthy", "attention", "degraded", "critical" from the worst
	// severity (INFO, WARNING, ERROR, CRITICAL).
	Health   string           `json:"health"`
	Counts   map[Severity]int `json:"counts"`
	Before   Fingerprint      `json:"fingerprint_before"`
	After    Fingerprint      `json:"fingerprint_after"`
	Modified bool             `json:"modified"` // any byte of the database file changed during the run
	Engine   Engine           `json:"engine"`
	Walk     *Walk            `json:"walk,omitempty"`
	Recovery Recovery         `json:"recovery"`
	Findings []Finding        `json:"findings"`
	Repair   *RepairResult    `json:"repair,omitempty"`
}

// ReportVersion is the schema version of Report.
const ReportVersion = 1
