package format

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Entry header layout (11 bytes, little-endian), followed by key then value:
//
//	0  1  op         1 = PUT, 2 = DEL
//	1  2  key_len    1..1024
//	3  4  val_len    0..1048576; must be 0 for DEL
//	7  4  entry_crc  CRC32C of (op, key_len, val_len, key, value)
//
// entry_crc lets GET verify one value without re-reading its whole batch. It
// covers header bytes 0..6, the key and the value, in that order.
const (
	ehOffOp     = 0
	ehOffKeyLen = 1
	ehOffValLen = 3
	ehOffCRC    = 7
)

// OpCode is an entry operation.
type OpCode uint8

const (
	OpPut    OpCode = 1
	OpDelete OpCode = 2
)

func (o OpCode) String() string {
	switch o {
	case OpPut:
		return "PUT"
	case OpDelete:
		return "DEL"
	}
	return fmt.Sprintf("OpCode(%d)", uint8(o))
}

// EntryHeader is a decoded entry header.
type EntryHeader struct {
	Op       OpCode
	KeyLen   uint16
	ValueLen uint32
	CRC      uint32
}

// Size returns the full encoded size of the entry (header + key + value).
// It cannot overflow: every term is bounded by a fixed-width field.
func (h EntryHeader) Size() uint64 {
	return EntryHeaderSize + uint64(h.KeyLen) + uint64(h.ValueLen)
}

func validateEntryFields(op OpCode, keyLen, valLen uint64, kind func(string, string, uint64, string) error) error {
	if op != OpPut && op != OpDelete {
		return kind("entry", "op", uint64(op), "must be 1 (PUT) or 2 (DEL)")
	}
	if keyLen < MinKeyLen || keyLen > MaxKeyLen {
		return kind("entry", "key_len", keyLen, "must be 1..1024")
	}
	if valLen > MaxValueLen {
		return kind("entry", "val_len", valLen, "exceeds 1048576")
	}
	if op == OpDelete && valLen != 0 {
		return kind("entry", "val_len", valLen, "must be 0 for DEL")
	}
	return nil
}

// ValidateEntry checks an entry the caller wants to encode.
func ValidateEntry(op OpCode, key, value []byte) error {
	return validateEntryFields(op, uint64(len(key)), uint64(len(value)), invalid)
}

func putEntryHeaderPrefix(b []byte, op OpCode, keyLen uint16, valLen uint32) {
	b[ehOffOp] = byte(op)
	binary.LittleEndian.PutUint16(b[ehOffKeyLen:], keyLen)
	binary.LittleEndian.PutUint32(b[ehOffValLen:], valLen)
}

// EntryChecksum computes entry_crc for (op, key, value). The lengths are
// taken from the slices; the caller must have validated them.
func EntryChecksum(op OpCode, key, value []byte) uint32 {
	var p [ehOffCRC]byte
	putEntryHeaderPrefix(p[:], op, uint16(len(key)), uint32(len(value)))
	crc := UpdateChecksum(0, p[:])
	crc = UpdateChecksum(crc, key)
	return UpdateChecksum(crc, value)
}

// EncodeEntryHeader validates (op, key, value) and returns the 11-byte header
// with entry_crc filled in. The key and value bytes are not copied.
func EncodeEntryHeader(op OpCode, key, value []byte) ([EntryHeaderSize]byte, error) {
	var b [EntryHeaderSize]byte
	if err := ValidateEntry(op, key, value); err != nil {
		return b, err
	}
	putEntryHeaderPrefix(b[:], op, uint16(len(key)), uint32(len(value)))
	binary.LittleEndian.PutUint32(b[ehOffCRC:], EntryChecksum(op, key, value))
	return b, nil
}

// AppendEntry validates and appends a complete encoded entry to dst.
func AppendEntry(dst []byte, op OpCode, key, value []byte) ([]byte, error) {
	h, err := EncodeEntryHeader(op, key, value)
	if err != nil {
		return dst, err
	}
	dst = append(dst, h[:]...)
	dst = append(dst, key...)
	return append(dst, value...), nil
}

// DecodeEntryHeader parses and validates the first 11 bytes of b. It cannot
// check entry_crc (that needs key and value); see VerifyEntry / DecodeEntry.
//
// Field violations are ErrMalformed. Inside a frame, the scanner decides the
// final class by finishing the payload CRC (see ScanPayload).
func DecodeEntryHeader(b []byte) (EntryHeader, error) {
	if len(b) < EntryHeaderSize {
		return EntryHeader{}, fmt.Errorf("%w: entry header needs %d bytes, have %d",
			ErrTruncated, EntryHeaderSize, len(b))
	}
	h := EntryHeader{
		Op:       OpCode(b[ehOffOp]),
		KeyLen:   binary.LittleEndian.Uint16(b[ehOffKeyLen:]),
		ValueLen: binary.LittleEndian.Uint32(b[ehOffValLen:]),
		CRC:      binary.LittleEndian.Uint32(b[ehOffCRC:]),
	}
	if err := validateEntryFields(h.Op, uint64(h.KeyLen), uint64(h.ValueLen), malformed); err != nil {
		return EntryHeader{}, err
	}
	return h, nil
}

// CheckEntryFits verifies that an entry with header h fits in the remaining
// bytes of a payload. remaining is the number of payload bytes from the start
// of the entry header to the end of the payload. Subtraction only.
func CheckEntryFits(h EntryHeader, remaining uint64) error {
	if remaining < EntryHeaderSize {
		return malformed("entry", "remaining", remaining, "less than an entry header")
	}
	if uint64(h.KeyLen)+uint64(h.ValueLen) > remaining-EntryHeaderSize {
		return malformed("entry", "size", h.Size(),
			fmt.Sprintf("runs past end of payload (%d bytes left)", remaining))
	}
	return nil
}

// VerifyEntry checks entry_crc of h against key and value, and that the
// slice lengths match the header.
func VerifyEntry(h EntryHeader, key, value []byte) error {
	if len(key) != int(h.KeyLen) || len(value) != int(h.ValueLen) {
		return fmt.Errorf("%w: entry length mismatch", ErrShortBuffer)
	}
	if got := EntryChecksum(h.Op, key, value); got != h.CRC {
		return fmt.Errorf("%w (stored %#08x, computed %#08x)", ErrEntryChecksum, h.CRC, got)
	}
	return nil
}

// DecodeEntry decodes one complete entry that occupies exactly b (the GET
// path reads exactly entry_len bytes at an indexed offset). It verifies the
// header, the exact length and entry_crc. The returned key and value alias b.
func DecodeEntry(b []byte) (h EntryHeader, key, value []byte, err error) {
	h, err = DecodeEntryHeader(b)
	if err != nil {
		return EntryHeader{}, nil, nil, err
	}
	if uint64(len(b)) != h.Size() {
		return EntryHeader{}, nil, nil, fmt.Errorf("%w: entry is %d bytes, buffer has %d",
			ErrTruncated, h.Size(), len(b))
	}
	key = b[EntryHeaderSize : EntryHeaderSize+int(h.KeyLen)]
	value = b[EntryHeaderSize+int(h.KeyLen):]
	if err := VerifyEntry(h, key, value); err != nil {
		return EntryHeader{}, nil, nil, err
	}
	return h, key, value, nil
}

// DecodeEntryForKey is DecodeEntry plus a check that the stored key equals
// want. GET uses it as defence in depth for invariant rule 4: a key must never
// return a value written to a different key, even if the index were wrong.
func DecodeEntryForKey(b, want []byte) (EntryHeader, []byte, error) {
	h, key, value, err := DecodeEntry(b)
	if err != nil {
		return EntryHeader{}, nil, err
	}
	if !bytes.Equal(key, want) {
		return EntryHeader{}, nil, fmt.Errorf("%w: stored key does not match requested key", ErrMalformed)
	}
	return h, value, nil
}
