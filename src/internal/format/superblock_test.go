package format

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func mustSB(t *testing.T, s Superblock) [SuperblockSize]byte {
	t.Helper()
	b, err := s.Encode()
	if err != nil {
		t.Fatalf("Encode(%+v): %v", s, err)
	}
	return b
}

// reseal recomputes the superblock CRC after a test tampers with fields, so
// that the test reaches the field check instead of failing on the CRC.
func resealSB(b []byte) {
	binary.LittleEndian.PutUint32(b[sbOffCRC:], Checksum(b[:sbOffCRC]))
}

func TestSuperblockLayout(t *testing.T) {
	b := mustSB(t, Superblock{FirstSeq: 0x0102030405060708, SnapshotEnd: 0x1112131415161718})
	want := []byte{
		'K', 'I', 'P', 'D', 'B', 'L', 'O', 'G', // magic
		0x01, 0x00, // version 1, little-endian
		0x00, 0x00, // flags
		0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01, // first_seq LE
		0x18, 0x17, 0x16, 0x15, 0x14, 0x13, 0x12, 0x11, // snapshot_end LE
	}
	if !bytes.Equal(b[:28], want) {
		t.Fatalf("bytes 0..27 = % x\nwant          % x", b[:28], want)
	}
	if got, w := binary.LittleEndian.Uint32(b[28:]), Checksum(want); got != w {
		t.Fatalf("crc = %#08x, want %#08x", got, w)
	}
	if m := SuperblockMagic(); string(m[:]) != "KIPDBLOG" {
		t.Fatalf("magic = %q", m)
	}
}

func TestSuperblockRoundTrip(t *testing.T) {
	cases := []Superblock{
		NewSuperblock(),
		{FirstSeq: 1, SnapshotEnd: 32},
		{FirstSeq: 42, SnapshotEnd: 1 << 30},
		{FirstSeq: math.MaxUint64, SnapshotEnd: MaxFileOffset},
	}
	for _, s := range cases {
		b := mustSB(t, s)
		got, err := DecodeSuperblock(b[:])
		if err != nil {
			t.Fatalf("Decode(%+v): %v", s, err)
		}
		if got != s {
			t.Fatalf("round trip: got %+v want %+v", got, s)
		}
		// Extra trailing bytes are ignored (decoder reads exactly 32).
		got2, err := DecodeSuperblock(append(b[:], 0xAA, 0xBB))
		if err != nil || got2 != s {
			t.Fatalf("with trailing bytes: %+v, %v", got2, err)
		}
	}
	if s := NewSuperblock(); s.FirstSeq != 1 || s.SnapshotEnd != 32 {
		t.Fatalf("NewSuperblock = %+v", s)
	}
}

func TestSuperblockDecodeErrors(t *testing.T) {
	good := mustSB(t, Superblock{FirstSeq: 7, SnapshotEnd: 100})
	tests := []struct {
		name   string
		mutate func(b []byte) []byte
		want   error
		class  Class
	}{
		{"short", func(b []byte) []byte { return b[:31] }, ErrTruncated, ClassDamaged},
		{"empty", func(b []byte) []byte { return nil }, ErrTruncated, ClassDamaged},
		{"all zeros (unsealed)", func(b []byte) []byte { return make([]byte, 32) }, ErrBadMagic, ClassDamaged},
		{"bad magic", func(b []byte) []byte { b[0] = 'X'; return b }, ErrBadMagic, ClassDamaged},
		{"bad crc", func(b []byte) []byte { b[31] ^= 0x01; return b }, ErrHeaderChecksum, ClassDamaged},
		{"flipped field bit, crc stale", func(b []byte) []byte { b[15] ^= 0x80; return b }, ErrHeaderChecksum, ClassDamaged},
		{"version 0", func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[sbOffVersion:], 0)
			resealSB(b)
			return b
		}, ErrUnsupportedVersion, ClassCorrupt},
		{"version 2", func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[sbOffVersion:], 2)
			resealSB(b)
			return b
		}, ErrUnsupportedVersion, ClassCorrupt},
		{"flags nonzero", func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[sbOffFlags:], 1)
			resealSB(b)
			return b
		}, ErrMalformed, ClassCorrupt},
		{"first_seq zero", func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[sbOffFirstSeq:], 0)
			resealSB(b)
			return b
		}, ErrMalformed, ClassCorrupt},
		{"snapshot_end zero", func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[sbOffSnapshotEnd:], 0)
			resealSB(b)
			return b
		}, ErrMalformed, ClassCorrupt},
		{"snapshot_end 31", func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[sbOffSnapshotEnd:], 31)
			resealSB(b)
			return b
		}, ErrMalformed, ClassCorrupt},
		{"snapshot_end > MaxInt64", func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[sbOffSnapshotEnd:], MaxFileOffset+1)
			resealSB(b)
			return b
		}, ErrMalformed, ClassCorrupt},
		{"snapshot_end MaxUint64", func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[sbOffSnapshotEnd:], math.MaxUint64)
			resealSB(b)
			return b
		}, ErrMalformed, ClassCorrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := append([]byte(nil), good[:]...)
			_, err := DecodeSuperblock(tt.mutate(b))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if c := Classify(err); c != tt.class {
				t.Fatalf("class = %v, want %v", c, tt.class)
			}
		})
	}
}

// Every single-bit flip of a valid superblock must be rejected.
func TestSuperblockEveryBitFlipRejected(t *testing.T) {
	good := mustSB(t, Superblock{FirstSeq: 3, SnapshotEnd: 4096})
	for i := 0; i < SuperblockSize*8; i++ {
		b := good
		b[i/8] ^= 1 << (i % 8)
		if _, err := DecodeSuperblock(b[:]); err == nil {
			t.Fatalf("bit %d flip accepted", i)
		}
	}
}

func TestSuperblockEncodeRejectsInvalid(t *testing.T) {
	for _, s := range []Superblock{
		{FirstSeq: 0, SnapshotEnd: 32},
		{FirstSeq: 1, SnapshotEnd: 0},
		{FirstSeq: 1, SnapshotEnd: 31},
		{FirstSeq: 1, SnapshotEnd: MaxFileOffset + 1},
	} {
		if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Encode(%+v) err = %v, want ErrInvalid", s, err)
		}
		dst := []byte{1, 2, 3}
		out, err := s.AppendTo(dst)
		if err == nil || len(out) != 3 {
			t.Fatalf("AppendTo(%+v) must not append on error: len %d, err %v", s, len(out), err)
		}
	}
}

func TestSuperblockCheckFileSize(t *testing.T) {
	s := Superblock{FirstSeq: 1, SnapshotEnd: 1000}
	for _, tc := range []struct {
		size uint64
		ok   bool
	}{{999, false}, {1000, true}, {1001, true}, {0, false}, {math.MaxUint64, true}} {
		err := s.CheckFileSize(tc.size)
		if (err == nil) != tc.ok {
			t.Fatalf("size %d: err = %v, want ok=%v", tc.size, err, tc.ok)
		}
		if err != nil && Classify(err) != ClassCorrupt {
			t.Fatalf("size %d: class %v, want corrupt", tc.size, Classify(err))
		}
	}
}
