package kvdb

import (
	"errors"
	"testing"
)

func TestOpValidate(t *testing.T) {
	tests := []struct {
		name string
		op   Op
		want error
	}{
		{"put ok", Op{OpPut, []byte("k"), []byte("v")}, nil},
		{"put empty value ok", Op{OpPut, []byte("k"), nil}, nil},
		{"delete ok", Op{OpDelete, []byte("k"), nil}, nil},
		{"max key ok", Op{OpPut, make([]byte, MaxKeyLen), nil}, nil},
		{"max value ok", Op{OpPut, []byte("k"), make([]byte, MaxValueLen)}, nil},
		{"empty key", Op{OpPut, nil, nil}, ErrInvalidArgument},
		{"key too long", Op{OpPut, make([]byte, MaxKeyLen+1), nil}, ErrInvalidArgument},
		{"value too long", Op{OpPut, []byte("k"), make([]byte, MaxValueLen+1)}, ErrTooLarge},
		{"delete with value", Op{OpDelete, []byte("k"), []byte("v")}, ErrInvalidArgument},
		{"unknown kind", Op{OpKind(0), []byte("k"), nil}, ErrInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.op.Validate()
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestValidateBatch(t *testing.T) {
	if err := ValidateBatch(nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	ops := make([]Op, MaxBatchOps)
	for i := range ops {
		ops[i] = Op{OpDelete, []byte("k"), nil}
	}
	if err := ValidateBatch(ops); err != nil {
		t.Fatalf("10000 ops: %v", err)
	}
	if err := ValidateBatch(append(ops, Op{OpDelete, []byte("k"), nil})); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("10001 ops: %v", err)
	}
	ops[5000].Key = nil
	if err := ValidateBatch(ops); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad op inside batch: %v", err)
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	all := []error{ErrNotFound, ErrInvalidArgument, ErrTooLarge, ErrFull, ErrClosed,
		ErrFailed, ErrOutcomeUnknown, ErrCorruption, ErrBadFormat, ErrLocked}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("%v matches %v", a, b)
			}
		}
	}
}
