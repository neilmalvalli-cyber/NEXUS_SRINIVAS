package format

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func mustFH(t *testing.T, h FrameHeader) [FrameHeaderSize]byte {
	t.Helper()
	b, err := h.Encode()
	if err != nil {
		t.Fatalf("Encode(%+v): %v", h, err)
	}
	return b
}

func resealFH(b []byte) {
	binary.LittleEndian.PutUint32(b[fhOffHeaderCRC:], Checksum(b[:fhOffHeaderCRC]))
}

func TestFrameHeaderLayout(t *testing.T) {
	h := FrameHeader{OpCount: 0x0102, Seq: 0x1112131415161718, PayloadLen: 0x0000002122232425, PayloadCRC: 0x31323334}
	b := mustFH(t, h)
	want := []byte{
		0xA5, 0xB1, 0xC3, 0xD8, // magic 0xD8C3B1A5 LE
		0x01,       // type
		0x00,       // reserved
		0x02, 0x01, // op_count LE
		0x18, 0x17, 0x16, 0x15, 0x14, 0x13, 0x12, 0x11, // seq LE
		0x25, 0x24, 0x23, 0x22, 0x21, 0x00, 0x00, 0x00, // payload_len LE
		0x34, 0x33, 0x32, 0x31, // payload_crc LE
	}
	if !bytes.Equal(b[:28], want) {
		t.Fatalf("bytes 0..27 = % x\nwant          % x", b[:28], want)
	}
	if got, w := binary.LittleEndian.Uint32(b[28:]), Checksum(want); got != w {
		t.Fatalf("header crc = %#08x, want %#08x", got, w)
	}
}

func TestFrameHeaderRoundTrip(t *testing.T) {
	cases := []FrameHeader{
		{OpCount: 1, Seq: 1, PayloadLen: MinEntrySize, PayloadCRC: 0},
		{OpCount: 1, Seq: math.MaxUint64, PayloadLen: MaxEntrySize, PayloadCRC: 0xFFFFFFFF},
		{OpCount: MaxBatchOps, Seq: 99, PayloadLen: MaxBatchOps * MinEntrySize, PayloadCRC: 0xDEADBEEF},
		{OpCount: MaxFrameOps, Seq: 5, PayloadLen: MaxPayload, PayloadCRC: 1},
		{OpCount: 1, Seq: 2, PayloadLen: MaxPayload, PayloadCRC: 2},
	}
	for _, h := range cases {
		b := mustFH(t, h)
		got, err := DecodeFrameHeader(b[:])
		if err != nil {
			t.Fatalf("Decode(%+v): %v", h, err)
		}
		if got != h {
			t.Fatalf("round trip: got %+v want %+v", got, h)
		}
		size, ok := got.FrameSize()
		if !ok || size != FrameHeaderSize+h.PayloadLen {
			t.Fatalf("FrameSize = %d,%v", size, ok)
		}
	}
}

func TestFrameHeaderDecodeErrors(t *testing.T) {
	good := mustFH(t, FrameHeader{OpCount: 3, Seq: 10, PayloadLen: 100, PayloadCRC: 0xABCD})
	set := func(off int, put func([]byte)) func([]byte) []byte {
		return func(b []byte) []byte { put(b[off:]); resealFH(b); return b }
	}
	u16 := func(v uint16) func([]byte) { return func(b []byte) { binary.LittleEndian.PutUint16(b, v) } }
	u64 := func(v uint64) func([]byte) { return func(b []byte) { binary.LittleEndian.PutUint64(b, v) } }
	u8 := func(v byte) func([]byte) { return func(b []byte) { b[0] = v } }

	tests := []struct {
		name   string
		mutate func([]byte) []byte
		want   error
		class  Class
	}{
		{"short", func(b []byte) []byte { return b[:31] }, ErrTruncated, ClassDamaged},
		{"zeros", func(b []byte) []byte { return make([]byte, 32) }, ErrBadMagic, ClassDamaged},
		{"bad magic", func(b []byte) []byte { b[0] ^= 1; return b }, ErrBadMagic, ClassDamaged},
		{"bad header crc", func(b []byte) []byte { b[29] ^= 1; return b }, ErrHeaderChecksum, ClassDamaged},
		{"payload_len bit flip, crc stale", func(b []byte) []byte { b[20] ^= 0x10; return b }, ErrHeaderChecksum, ClassDamaged},
		{"type 0", set(fhOffType, u8(0)), ErrMalformed, ClassCorrupt},
		{"type 2", set(fhOffType, u8(2)), ErrMalformed, ClassCorrupt},
		{"type 255", set(fhOffType, u8(255)), ErrMalformed, ClassCorrupt},
		{"reserved 1", set(fhOffReserved, u8(1)), ErrMalformed, ClassCorrupt},
		{"op_count 0", set(fhOffOpCount, u16(0)), ErrMalformed, ClassCorrupt},
		{"op_count > payload/12", set(fhOffOpCount, u16(9)), ErrMalformed, ClassCorrupt}, // 100/12 = 8
		{"op_count max with tiny payload", set(fhOffOpCount, u16(math.MaxUint16)), ErrMalformed, ClassCorrupt},
		{"seq 0", set(fhOffSeq, u64(0)), ErrMalformed, ClassCorrupt},
		{"payload_len 0", set(fhOffPayloadLen, u64(0)), ErrMalformed, ClassCorrupt},
		{"payload_len 11", set(fhOffPayloadLen, u64(11)), ErrMalformed, ClassCorrupt},
		{"payload_len 2^40+1", set(fhOffPayloadLen, u64(MaxPayload+1)), ErrMalformed, ClassCorrupt},
		{"payload_len MaxUint64", set(fhOffPayloadLen, u64(math.MaxUint64)), ErrMalformed, ClassCorrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := append([]byte(nil), good[:]...)
			_, err := DecodeFrameHeader(tt.mutate(b))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if c := Classify(err); c != tt.class {
				t.Fatalf("class = %v, want %v", c, tt.class)
			}
		})
	}
}

func TestFrameHeaderOpCountBoundary(t *testing.T) {
	// op_count == payload_len/12 exactly is allowed; one more is not.
	for _, pl := range []uint64{12, 23, 24, 120000, MaxPayload} {
		limit := pl / MinEntrySize
		if limit > MaxFrameOps {
			limit = MaxFrameOps
		}
		h := FrameHeader{OpCount: uint16(limit), Seq: 1, PayloadLen: pl}
		if _, err := h.Encode(); err != nil {
			t.Fatalf("payload %d op_count %d rejected: %v", pl, limit, err)
		}
		if limit < MaxFrameOps {
			h.OpCount++
			if _, err := h.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("payload %d op_count %d accepted", pl, h.OpCount)
			}
		}
	}
}

func TestFrameHeaderEncodeRejectsInvalid(t *testing.T) {
	for _, h := range []FrameHeader{
		{OpCount: 0, Seq: 1, PayloadLen: 12},
		{OpCount: 1, Seq: 0, PayloadLen: 12},
		{OpCount: 1, Seq: 1, PayloadLen: 11},
		{OpCount: 1, Seq: 1, PayloadLen: MaxPayload + 1},
		{OpCount: 2, Seq: 1, PayloadLen: 23},
	} {
		if _, err := h.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Encode(%+v) err = %v, want ErrInvalid", h, err)
		}
	}
}

func TestFrameHeaderEveryBitFlipRejected(t *testing.T) {
	good := mustFH(t, FrameHeader{OpCount: 2, Seq: 77, PayloadLen: 4096, PayloadCRC: 0x12345678})
	for i := 0; i < FrameHeaderSize*8; i++ {
		b := good
		b[i/8] ^= 1 << (i % 8)
		if _, err := DecodeFrameHeader(b[:]); err == nil {
			t.Fatalf("bit %d flip accepted", i)
		}
	}
}

func TestFrameSizeHostile(t *testing.T) {
	if _, ok := (FrameHeader{PayloadLen: math.MaxUint64}).FrameSize(); ok {
		t.Fatal("FrameSize accepted MaxUint64 payload")
	}
	if _, ok := (FrameHeader{PayloadLen: MaxPayload + 1}).FrameSize(); ok {
		t.Fatal("FrameSize accepted MaxPayload+1")
	}
}

func TestHeaderAndPayloadFits(t *testing.T) {
	const M = math.MaxUint64
	tests := []struct {
		pos, size, pl   uint64
		header, payload bool
	}{
		{32, 64, 0, true, true},
		{32, 63, 0, false, false},
		{32, 76, 12, true, true},
		{32, 75, 12, true, false},
		{100, 50, 0, false, false},     // pos beyond size
		{M, M, 0, false, false},        // pos == size at the top
		{M - 31, M, 0, false, false},   // only 31 bytes left
		{M - 32, M, 0, true, true},     // exactly 32 bytes left
		{0, M, M, true, false},         // payload_len above MaxPayload
		{0, M, MaxPayload, true, true}, // huge but bounded
		{M - 40, M, 8, true, true},     // near the top, no overflow
		{M - 40, M, 9, true, false},
		{0, 32 + MaxPayload, MaxPayload, true, true},
		{1, 32 + MaxPayload, MaxPayload, true, false},
	}
	for _, tt := range tests {
		if got := HeaderFits(tt.pos, tt.size); got != tt.header {
			t.Errorf("HeaderFits(%d,%d) = %v, want %v", tt.pos, tt.size, got, tt.header)
		}
		if got := PayloadFits(tt.pos, tt.size, tt.pl); got != tt.payload {
			t.Errorf("PayloadFits(%d,%d,%d) = %v, want %v", tt.pos, tt.size, tt.pl, got, tt.payload)
		}
	}
}

func TestNextSeq(t *testing.T) {
	for _, tc := range []struct {
		in, out uint64
		ok      bool
	}{{0, 1, true}, {1, 2, true}, {math.MaxUint64 - 1, math.MaxUint64, true}, {math.MaxUint64, 0, false}} {
		got, err := NextSeq(tc.in)
		if (err == nil) != tc.ok || (tc.ok && got != tc.out) {
			t.Fatalf("NextSeq(%d) = %d, %v", tc.in, got, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalid) {
			t.Fatalf("NextSeq wrap err = %v", err)
		}
	}
}
