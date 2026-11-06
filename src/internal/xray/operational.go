package xray

import (
	"errors"
	"fmt"
	"time"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/platform"
)

// FromError turns an error returned by a NEXUS operation (op is a short
// name such as "put", "batch", "compact", "open") into a finding. It uses
// errors.Is on the engine's sentinels only; an error it cannot classify is
// reported as an unclassified operational error with the original message,
// never with an invented cause. It returns false for nil and for ordinary
// client errors (not found, invalid argument, too large), which are not
// health problems.
func FromError(op, path string, err error, at time.Time) (Finding, bool) {
	if err == nil || errors.Is(err, dberr.ErrNotFound) || errors.Is(err, dberr.ErrInvalidArgument) || errors.Is(err, dberr.ErrTooLarge) {
		return Finding{}, false
	}
	f := Finding{Category: CatOperational, File: path, Timestamp: at.UTC(), Component: "storage/" + op,
		OriginalError: err.Error(), Recovery: Detected}
	switch {
	case errors.Is(err, dberr.ErrOutcomeUnknown):
		f.Code, f.Severity, f.Title = "OP_OUTCOME_UNKNOWN", Error, "Write outcome unknown"
		f.Simple = "A write failed after some of its bytes may have reached the disk. It may or may not be present after the next open; it was not acknowledged."
		f.Technical = op + ": ErrOutcomeUnknown (bytes may be durable; the handle is failed)"
		f.DataImpact = "The single unacknowledged write may appear after reopening, whole or not at all (frames are atomic)."
		f.Action = "Reopen the database; run X-RAY to confirm the file is clean; re-read the key to learn the outcome."
	case errors.Is(err, dberr.ErrFailed):
		f.Code, f.Severity, f.Title = "OP_HANDLE_FAILED", Error, "Database handle failed"
		f.Simple = "An earlier write or sync failed, so this handle refuses all further work until the database is reopened. Nothing acknowledged was lost."
		f.Technical = op + ": ErrFailed (handle entered FAILED after an I/O error)"
		f.DataImpact = "None for acknowledged data."
		f.Action = "Reopen the database. Check disk space and health."
	case errors.Is(err, dberr.ErrFull):
		f.Code, f.Severity, f.Title = "OP_FULL", Error, "Database full"
		f.Simple = "The write was refused because it would exceed the configured size limit (max_bytes) or the disk is full. Nothing was written."
		f.Technical = op + ": ErrFull"
		if platform.IsDiskFull(err) {
			f.Technical += " (operating system reports disk full)"
		}
		f.DataImpact = "None: the write was cleanly rejected."
		f.Action = "Delete data and compact, raise max_bytes, or free disk space."
	case errors.Is(err, dberr.ErrLocked):
		f.Code, f.Severity, f.Title = "OP_LOCKED", Warning, "Database locked by another process"
		f.Simple = "Another process has the database open, so it could not be opened here."
		f.Technical = op + ": ErrLocked"
		f.Component = "locking"
		f.DataImpact = "None."
		f.Action = "Close the other process, or use it to access the database."
	case errors.Is(err, dberr.ErrClosed):
		f.Code, f.Severity, f.Title = "OP_CLOSED", Info, "Database was closed"
		f.Simple = "The operation was attempted on a closed database handle."
		f.Technical = op + ": ErrClosed"
		f.DataImpact = "None."
		f.Action = "Reopen the database."
	case errors.Is(err, dberr.ErrCorruption):
		f.Code, f.Severity, f.Title, f.Category = "OP_CORRUPTION", Critical, "Corruption detected during "+op, CatRecovery
		f.Simple = "NEXUS detected bytes that fail validation and stopped instead of returning or discarding bad data."
		f.Technical = op + ": ErrCorruption"
		f.Recovery = Refused
		f.DataImpact = "Committed data fails validation."
		f.Action = "Run X-RAY on the database file for the exact location and cause."
	case errors.Is(err, dberr.ErrBadFormat):
		f.Code, f.Severity, f.Title, f.Category = "OP_BAD_FORMAT", Critical, "Not a usable NEXUS database", CatRecovery
		f.Simple = "The file is not a NEXUS database this version can open."
		f.Technical = op + ": ErrBadFormat"
		f.Recovery = Refused
		f.DataImpact = "The file cannot be read by this version."
		f.Action = "Run X-RAY on the file for details."
	default:
		f.Code, f.Severity, f.Title = "OP_ERROR", Error, "Operation failed"
		f.Simple = "The operation failed with an error NEXUS does not classify further (typically an operating-system I/O error)."
		f.Technical = fmt.Sprintf("%s: %v", op, err)
		f.DataImpact = "Unknown; see the original error."
		f.Action = "Check the original error; run X-RAY if it concerns the database file."
	}
	f.ID = fmt.Sprintf("%s@%s:%d", f.Code, op, at.UnixNano())
	return f, true
}
