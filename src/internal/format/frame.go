package format

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Frame header layout (32 bytes, little-endian):
//
//	0   4  magic        0xD8C3B1A5
//	4   1  type         1 = COMMIT_FRAME
//	5   1  reserved     0
//	6   2  op_count     1 <= n <= payload_len / MinEntrySize
//	8   8  seq          first frame = superblock.first_seq, then +1 each frame
//	16  8  payload_len  MinEntrySize <= len <= MaxPayload
//	24  4  payload_crc  CRC32C of the payload
//	28  4  header_crc   CRC32C of bytes 0..27
//
// header_crc covers payload_len and payload_crc, so a damaged length can never
// send the scanner off reading gigabytes of garbage: the header is rejected
// before payload_len is trusted.
const (
	fhOffMagic      = 0
	fhOffType       = 4
	fhOffReserved   = 5
	fhOffOpCount    = 6
	fhOffSeq        = 8
	fhOffPayloadLen = 16
	fhOffPayloadCRC = 24
	fhOffHeaderCRC  = 28
)

// FrameHeader holds the variable fields of a frame header.
type FrameHeader struct {
	OpCount    uint16
	Seq        uint64
	PayloadLen uint64
	PayloadCRC uint32
}

// validate checks every field rule that can be checked from the header alone.
// The order follows docs/DESIGN.md §5.6: payload_len against MaxPayload before
// anything derived from it, op_count bounded by payload_len before any loop.
func (h FrameHeader) validate(kind func(string, string, uint64, string) error) error {
	if h.Seq == 0 {
		return kind("frame header", "seq", 0, "must be >= 1")
	}
	if h.PayloadLen > MaxPayload {
		return kind("frame header", "payload_len", h.PayloadLen, "exceeds MaxPayload (2^40)")
	}
	if h.PayloadLen < MinEntrySize {
		return kind("frame header", "payload_len", h.PayloadLen, "smaller than one minimal entry (12)")
	}
	if h.OpCount == 0 {
		return kind("frame header", "op_count", 0, "must be >= 1")
	}
	// Division, not multiplication: cannot overflow.
	if uint64(h.OpCount) > h.PayloadLen/MinEntrySize {
		return kind("frame header", "op_count", uint64(h.OpCount),
			fmt.Sprintf("cannot fit in payload_len %d", h.PayloadLen))
	}
	return nil
}

// FrameSize returns FrameHeaderSize + PayloadLen. It is safe because
// PayloadLen is at most MaxPayload for any header that passed validation; for
// an unvalidated header it reports ok=false instead of overflowing.
func (h FrameHeader) FrameSize() (size uint64, ok bool) {
	if h.PayloadLen > MaxPayload {
		return 0, false
	}
	return FrameHeaderSize + h.PayloadLen, true
}

// AppendTo validates h and appends its 32-byte encoding to dst.
func (h FrameHeader) AppendTo(dst []byte) ([]byte, error) {
	if err := h.validate(invalid); err != nil {
		return dst, err
	}
	var b [FrameHeaderSize]byte
	binary.LittleEndian.PutUint32(b[fhOffMagic:], FrameMagic)
	b[fhOffType] = FrameTypeCommit
	b[fhOffReserved] = 0
	binary.LittleEndian.PutUint16(b[fhOffOpCount:], h.OpCount)
	binary.LittleEndian.PutUint64(b[fhOffSeq:], h.Seq)
	binary.LittleEndian.PutUint64(b[fhOffPayloadLen:], h.PayloadLen)
	binary.LittleEndian.PutUint32(b[fhOffPayloadCRC:], h.PayloadCRC)
	binary.LittleEndian.PutUint32(b[fhOffHeaderCRC:], Checksum(b[:fhOffHeaderCRC]))
	return append(dst, b[:]...), nil
}

// Encode validates h and returns its 32-byte encoding.
func (h FrameHeader) Encode() ([FrameHeaderSize]byte, error) {
	var out [FrameHeaderSize]byte
	b, err := h.AppendTo(out[:0])
	if err != nil {
		return out, err
	}
	copy(out[:], b)
	return out, nil
}

// DecodeFrameHeader parses and validates the first 32 bytes of b.
//
// Magic and header CRC are checked before any field is interpreted. A header
// whose CRC passes but whose fields are invalid is ErrMalformed (Corrupt): a
// torn write leaves bytes that fail the CRC, it does not forge a valid CRC.
func DecodeFrameHeader(b []byte) (FrameHeader, error) {
	if len(b) < FrameHeaderSize {
		return FrameHeader{}, fmt.Errorf("%w: frame header needs %d bytes, have %d",
			ErrTruncated, FrameHeaderSize, len(b))
	}
	b = b[:FrameHeaderSize]
	if m := binary.LittleEndian.Uint32(b[fhOffMagic:]); m != FrameMagic {
		return FrameHeader{}, fmt.Errorf("%w: frame header %#08x", ErrBadMagic, m)
	}
	want := binary.LittleEndian.Uint32(b[fhOffHeaderCRC:])
	if got := Checksum(b[:fhOffHeaderCRC]); got != want {
		return FrameHeader{}, fmt.Errorf("%w: frame header (stored %#08x, computed %#08x)",
			ErrHeaderChecksum, want, got)
	}
	if t := b[fhOffType]; t != FrameTypeCommit {
		return FrameHeader{}, malformed("frame header", "type", uint64(t), "must be 1 (COMMIT_FRAME)")
	}
	if r := b[fhOffReserved]; r != 0 {
		return FrameHeader{}, malformed("frame header", "reserved", uint64(r), "must be 0")
	}
	h := FrameHeader{
		OpCount:    binary.LittleEndian.Uint16(b[fhOffOpCount:]),
		Seq:        binary.LittleEndian.Uint64(b[fhOffSeq:]),
		PayloadLen: binary.LittleEndian.Uint64(b[fhOffPayloadLen:]),
		PayloadCRC: binary.LittleEndian.Uint32(b[fhOffPayloadCRC:]),
	}
	if err := h.validate(malformed); err != nil {
		return FrameHeader{}, err
	}
	return h, nil
}

// HeaderFits reports whether a full frame header fits between pos and size.
// It subtracts instead of adding so hostile positions cannot overflow.
func HeaderFits(pos, size uint64) bool {
	return pos <= size && size-pos >= FrameHeaderSize
}

// PayloadFits reports whether a payload of length payloadLen, following a
// frame header that starts at pos, ends at or before size. The MaxPayload
// check comes first so that nothing derived from a hostile length is used.
func PayloadFits(pos, size, payloadLen uint64) bool {
	if payloadLen > MaxPayload || !HeaderFits(pos, size) {
		return false
	}
	return payloadLen <= size-pos-FrameHeaderSize
}

// NextSeq returns seq+1, or an error if the sequence would wrap to 0.
// Wraparound is a hard error: frame ordering would become ambiguous.
func NextSeq(seq uint64) (uint64, error) {
	if seq == math.MaxUint64 {
		return 0, invalid("frame header", "seq", seq, "sequence number exhausted")
	}
	return seq + 1, nil
}
