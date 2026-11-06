// Package kvdb is the public API of the engine.
//
// Phase 1 status: only the error values and the batch operation type are
// defined. The DB type and Open / Put / Get / Delete / Batch / Compact / Close
// arrive in Phase 2; nothing here performs I/O yet.
package kvdb

import (
	"fmt"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
)

// Errors returned by the engine. Test with errors.Is. See package dberr for
// the full contract of each value.
var (
	ErrNotFound        = dberr.ErrNotFound
	ErrInvalidArgument = dberr.ErrInvalidArgument
	ErrTooLarge        = dberr.ErrTooLarge
	ErrFull            = dberr.ErrFull
	ErrClosed          = dberr.ErrClosed
	ErrFailed          = dberr.ErrFailed
	ErrOutcomeUnknown  = dberr.ErrOutcomeUnknown
	ErrCorruption      = dberr.ErrCorruption
	ErrBadFormat       = dberr.ErrBadFormat
	ErrLocked          = dberr.ErrLocked
)

// Limits exposed to callers.
const (
	MaxKeyLen   = format.MaxKeyLen
	MaxValueLen = format.MaxValueLen
	MaxBatchOps = format.MaxBatchOps
)

// OpKind selects what a batch operation does.
type OpKind uint8

const (
	OpPut    OpKind = OpKind(format.OpPut)
	OpDelete OpKind = OpKind(format.OpDelete)
)

func (k OpKind) String() string { return format.OpCode(k).String() }

// Op is one operation inside a Batch. Operations apply in order and the last
// operation on a key wins:
//
//	PUT X=1, PUT X=2 -> X=2
//	PUT X=1, DEL X   -> X absent
//	DEL X,   PUT X=2 -> X=2
//
// The caller must not modify Key or Value while Batch is running.
type Op struct {
	Kind  OpKind
	Key   []byte
	Value []byte // must be empty for OpDelete
}

// Validate checks one operation against the API limits and maps violations
// to ErrInvalidArgument or ErrTooLarge.
func (o Op) Validate() error {
	switch {
	case o.Kind != OpPut && o.Kind != OpDelete:
		return fmt.Errorf("%w: unknown op kind %d", ErrInvalidArgument, o.Kind)
	case len(o.Key) == 0:
		return fmt.Errorf("%w: empty key", ErrInvalidArgument)
	case len(o.Key) > MaxKeyLen:
		return fmt.Errorf("%w: key is %d bytes (max %d)", ErrInvalidArgument, len(o.Key), MaxKeyLen)
	case len(o.Value) > MaxValueLen:
		return fmt.Errorf("%w: value is %d bytes (max %d)", ErrTooLarge, len(o.Value), MaxValueLen)
	case o.Kind == OpDelete && len(o.Value) != 0:
		return fmt.Errorf("%w: delete carries a value", ErrInvalidArgument)
	}
	return nil
}

// ValidateBatch checks a whole batch. An empty batch is valid (a no-op).
func ValidateBatch(ops []Op) error {
	if len(ops) > MaxBatchOps {
		return fmt.Errorf("%w: batch has %d operations (max %d)", ErrTooLarge, len(ops), MaxBatchOps)
	}
	for i, o := range ops {
		if err := o.Validate(); err != nil {
			return fmt.Errorf("op %d: %w", i, err)
		}
	}
	return nil
}
