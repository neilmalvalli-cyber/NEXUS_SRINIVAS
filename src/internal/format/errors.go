package format

import (
	"errors"
	"fmt"
)

// Sentinel errors. Use errors.Is to test for them; decoders wrap them with
// context (which structure, which field, which value).
var (
	// ErrShortBuffer: the caller passed a buffer smaller than the structure.
	// This is a programming error, not a property of the file.
	ErrShortBuffer = errors.New("format: buffer too short")

	// ErrBadMagic: the magic number does not match. Class: Damaged.
	ErrBadMagic = errors.New("format: bad magic")

	// ErrHeaderChecksum: a superblock or frame header CRC mismatch. Class: Damaged.
	ErrHeaderChecksum = errors.New("format: header checksum mismatch")

	// ErrPayloadChecksum: a frame's payload CRC mismatch. Class: Damaged.
	ErrPayloadChecksum = errors.New("format: payload checksum mismatch")

	// ErrTruncated: the input ended before the structure was complete.
	// Class: Damaged.
	ErrTruncated = errors.New("format: truncated input")

	// ErrUnsupportedVersion: checksum-valid superblock with an unknown
	// version. Class: Corrupt (we must not overwrite a newer file).
	ErrUnsupportedVersion = errors.New("format: unsupported format version")

	// ErrMalformed: all checksums covering the bytes pass, yet a field is out
	// of range or the structure does not add up. A torn write cannot produce
	// this. Class: Corrupt.
	ErrMalformed = errors.New("format: malformed structure")

	// ErrEntryChecksum: an entry CRC mismatch. Inside a frame whose payload
	// CRC passes it is reported wrapped in ErrMalformed (Corrupt); on the GET
	// path it means the indexed bytes are not what was written.
	ErrEntryChecksum = errors.New("format: entry checksum mismatch")

	// ErrInvalid: the caller asked to encode a value that violates the
	// format's limits. Nothing was written.
	ErrInvalid = errors.New("format: invalid value for encoding")
)

// FieldError describes a single out-of-range field. Kind is ErrMalformed when
// produced by a decoder and ErrInvalid when produced by an encoder.
type FieldError struct {
	Struct string // "superblock", "frame header", "entry"
	Field  string // e.g. "op_count"
	Value  uint64 // the offending value
	Reason string // human-readable rule that was violated
	Kind   error  // ErrMalformed or ErrInvalid
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%v: %s %s=%d: %s", e.Kind, e.Struct, e.Field, e.Value, e.Reason)
}

func (e *FieldError) Unwrap() error { return e.Kind }

func malformed(st, field string, v uint64, reason string) error {
	return &FieldError{Struct: st, Field: field, Value: v, Reason: reason, Kind: ErrMalformed}
}

func invalid(st, field string, v uint64, reason string) error {
	return &FieldError{Struct: st, Field: field, Value: v, Reason: reason, Kind: ErrInvalid}
}

// Class is the recovery-relevant category of a decode error.
type Class int

const (
	// ClassNone: err == nil.
	ClassNone Class = iota
	// ClassDamaged: consistent with a torn write (bad magic, checksum
	// mismatch, input ended early). Truncation is allowed only if the damage
	// is proven to be the tail of the log.
	ClassDamaged
	// ClassCorrupt: checksums pass but content is invalid, or unknown version.
	// Never produced by a torn write; recovery must refuse, not truncate.
	ClassCorrupt
	// ClassIO: anything else, in particular errors from the underlying
	// reader. Recovery must fail without modifying the file.
	ClassIO
	// ClassUsage: the caller misused the API (ErrInvalid: asked to encode
	// out-of-range values; ErrShortBuffer: inconsistent buffer lengths). It
	// says nothing about the file and nothing was written.
	ClassUsage
)

func (c Class) String() string {
	switch c {
	case ClassNone:
		return "none"
	case ClassDamaged:
		return "damaged"
	case ClassCorrupt:
		return "corrupt"
	case ClassIO:
		return "io"
	case ClassUsage:
		return "usage"
	}
	return fmt.Sprintf("Class(%d)", int(c))
}

// Classify maps an error returned by this package to its Class. Corrupt is
// checked first: a malformed frame may also carry a more specific sentinel,
// and malformed always wins.
//
// A bare ErrEntryChecksum (from VerifyEntry / DecodeEntry, i.e. the GET path)
// is Corrupt: GET reads an entry whose frame already passed every check at
// commit or recovery time, so a mismatch now means the bytes changed, not
// that a write was torn. Inside ScanPayload an entry checksum failure is
// reported as Malformed only if the payload CRC matches; otherwise the scanner
// reports ErrPayloadChecksum (Damaged).
func Classify(err error) Class {
	switch {
	case err == nil:
		return ClassNone
	case errors.Is(err, ErrMalformed), errors.Is(err, ErrUnsupportedVersion),
		errors.Is(err, ErrEntryChecksum):
		return ClassCorrupt
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrShortBuffer):
		return ClassUsage
	case errors.Is(err, ErrBadMagic), errors.Is(err, ErrHeaderChecksum),
		errors.Is(err, ErrPayloadChecksum), errors.Is(err, ErrTruncated):
		return ClassDamaged
	default:
		return ClassIO
	}
}
