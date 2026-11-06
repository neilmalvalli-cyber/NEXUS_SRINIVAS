package format

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestEntryHeaderLayout(t *testing.T) {
	key := []byte("k")
	val := []byte{0xAA, 0xBB}
	h, err := EncodeEntryHeader(OpPut, key, val)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x01, 0x01, 0x00, 0x02, 0x00, 0x00, 0x00}
	if !bytes.Equal(h[:7], want) {
		t.Fatalf("header prefix = % x, want % x", h[:7], want)
	}
	crcInput := append(append(append([]byte(nil), want...), key...), val...)
	if got, w := binary.LittleEndian.Uint32(h[7:]), Checksum(crcInput); got != w {
		t.Fatalf("entry crc = %#08x, want %#08x", got, w)
	}
}

func TestEntryRoundTrip(t *testing.T) {
	big := bytes.Repeat([]byte{0x5A}, MaxValueLen)
	maxKey := bytes.Repeat([]byte{'k'}, MaxKeyLen)
	cases := []struct {
		op         OpCode
		key, value []byte
	}{
		{OpPut, []byte("a"), nil},
		{OpPut, []byte("a"), []byte{}},
		{OpPut, []byte("hello"), []byte("world")},
		{OpPut, maxKey, big},
		{OpDelete, []byte("gone"), nil},
		{OpDelete, maxKey, nil},
	}
	for _, c := range cases {
		buf, err := AppendEntry(nil, c.op, c.key, c.value)
		if err != nil {
			t.Fatalf("AppendEntry(%v, %d, %d): %v", c.op, len(c.key), len(c.value), err)
		}
		if uint64(len(buf)) != EntryHeaderSize+uint64(len(c.key)+len(c.value)) {
			t.Fatalf("encoded length %d", len(buf))
		}
		h, k, v, err := DecodeEntry(buf)
		if err != nil {
			t.Fatalf("DecodeEntry: %v", err)
		}
		if h.Op != c.op || !bytes.Equal(k, c.key) || !bytes.Equal(v, c.value) || h.Size() != uint64(len(buf)) {
			t.Fatalf("round trip mismatch: %+v", h)
		}
		if _, v2, err := DecodeEntryForKey(buf, c.key); err != nil || !bytes.Equal(v2, c.value) {
			t.Fatalf("DecodeEntryForKey: %v", err)
		}
	}
}

func TestEntryValidation(t *testing.T) {
	tests := []struct {
		name       string
		op         OpCode
		key, value []byte
		ok         bool
	}{
		{"empty key", OpPut, nil, []byte("v"), false},
		{"key 1", OpPut, make([]byte, 1), nil, true},
		{"key 1024", OpPut, make([]byte, 1024), nil, true},
		{"key 1025", OpPut, make([]byte, 1025), nil, false},
		{"key 65535", OpPut, make([]byte, 65535), nil, false},
		{"key 65536 (u16 wrap)", OpPut, make([]byte, 65536), nil, false},
		{"value 1 MiB", OpPut, []byte("k"), make([]byte, MaxValueLen), true},
		{"value 1 MiB + 1", OpPut, []byte("k"), make([]byte, MaxValueLen+1), false},
		{"delete with value", OpDelete, []byte("k"), []byte("v"), false},
		{"delete no value", OpDelete, []byte("k"), nil, true},
		{"op 0", OpCode(0), []byte("k"), nil, false},
		{"op 3", OpCode(3), []byte("k"), nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEntry(tt.op, tt.key, tt.value)
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			_, encErr := EncodeEntryHeader(tt.op, tt.key, tt.value)
			if (encErr == nil) != tt.ok {
				t.Fatalf("EncodeEntryHeader err = %v", encErr)
			}
		})
	}
}

// rawEntryHeader builds an entry header without validation.
func rawEntryHeader(op byte, keyLen uint16, valLen uint32) []byte {
	b := make([]byte, EntryHeaderSize)
	b[0] = op
	binary.LittleEndian.PutUint16(b[1:], keyLen)
	binary.LittleEndian.PutUint32(b[3:], valLen)
	return b
}

func TestDecodeEntryHeaderErrors(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want error
	}{
		{"short", make([]byte, 10), ErrTruncated},
		{"op 0", rawEntryHeader(0, 1, 0), ErrMalformed},
		{"op 3", rawEntryHeader(3, 1, 0), ErrMalformed},
		{"key_len 0", rawEntryHeader(1, 0, 0), ErrMalformed},
		{"key_len 1025", rawEntryHeader(1, 1025, 0), ErrMalformed},
		{"key_len 65535", rawEntryHeader(1, 0xFFFF, 0), ErrMalformed},
		{"val_len 1 MiB + 1", rawEntryHeader(1, 1, MaxValueLen+1), ErrMalformed},
		{"val_len MaxUint32", rawEntryHeader(1, 1, 0xFFFFFFFF), ErrMalformed},
		{"DEL with val_len 1", rawEntryHeader(2, 1, 1), ErrMalformed},
		{"DEL with val_len max", rawEntryHeader(2, 1, MaxValueLen), ErrMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeEntryHeader(tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
	// Boundary values that must be accepted.
	for _, in := range [][]byte{
		rawEntryHeader(1, 1, 0), rawEntryHeader(1, 1024, MaxValueLen), rawEntryHeader(2, 1024, 0),
	} {
		if _, err := DecodeEntryHeader(in); err != nil {
			t.Fatalf("% x rejected: %v", in, err)
		}
	}
}

func TestCheckEntryFits(t *testing.T) {
	h := EntryHeader{Op: OpPut, KeyLen: 4, ValueLen: 6} // size 21
	for _, tc := range []struct {
		rem uint64
		ok  bool
	}{{0, false}, {10, false}, {11, false}, {20, false}, {21, true}, {22, true}, {^uint64(0), true}} {
		err := CheckEntryFits(h, tc.rem)
		if (err == nil) != tc.ok {
			t.Fatalf("remaining %d: err = %v", tc.rem, err)
		}
		if err != nil && !errors.Is(err, ErrMalformed) {
			t.Fatalf("remaining %d: err = %v, want ErrMalformed", tc.rem, err)
		}
	}
	// Largest possible header cannot overflow the sum.
	hMax := EntryHeader{Op: OpPut, KeyLen: 0xFFFF, ValueLen: 0xFFFFFFFF}
	if err := CheckEntryFits(hMax, EntryHeaderSize+0xFFFF+0xFFFFFFFF-1); err == nil {
		t.Fatal("max header fit into one byte too few")
	}
	if hMax.Size() != EntryHeaderSize+0xFFFF+0xFFFFFFFF {
		t.Fatalf("Size overflowed: %d", hMax.Size())
	}
}

func TestDecodeEntryDetectsDamage(t *testing.T) {
	good, err := AppendEntry(nil, OpPut, []byte("key"), []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	// Every bit flip must be rejected.
	for i := 0; i < len(good)*8; i++ {
		b := append([]byte(nil), good...)
		b[i/8] ^= 1 << (i % 8)
		if _, _, _, err := DecodeEntry(b); err == nil {
			t.Fatalf("bit %d flip accepted", i)
		}
	}
	// Wrong length (short and long) is rejected.
	if _, _, _, err := DecodeEntry(good[:len(good)-1]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("short: %v", err)
	}
	if _, _, _, err := DecodeEntry(append(append([]byte(nil), good...), 0)); !errors.Is(err, ErrTruncated) {
		t.Fatalf("long: %v", err)
	}
	// Value flip specifically reports an entry checksum failure.
	b := append([]byte(nil), good...)
	b[len(b)-1] ^= 0xFF
	if _, _, _, err := DecodeEntry(b); !errors.Is(err, ErrEntryChecksum) {
		t.Fatalf("value flip: %v", err)
	}
	// Key mismatch on the GET path is refused even with valid CRCs.
	if _, _, err := DecodeEntryForKey(good, []byte("kez")); !errors.Is(err, ErrMalformed) {
		t.Fatalf("key mismatch: %v", err)
	}
}

func TestVerifyEntryLengthMismatch(t *testing.T) {
	h := EntryHeader{Op: OpPut, KeyLen: 3, ValueLen: 1, CRC: EntryChecksum(OpPut, []byte("abc"), []byte("d"))}
	if err := VerifyEntry(h, []byte("abc"), []byte("d")); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEntry(h, []byte("ab"), []byte("cd")); !errors.Is(err, ErrShortBuffer) {
		t.Fatalf("err = %v", err)
	}
}
