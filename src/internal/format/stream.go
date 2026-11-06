package format

import (
	"errors"
	"fmt"
	"io"
)

// Entry is one operation to encode into a frame.
type Entry struct {
	Op    OpCode
	Key   []byte
	Value []byte // must be empty for OpDelete
}

// stagingSize is the size of the encoder's staging buffer. Values of at least
// directWriteThreshold bytes skip the buffer and are written straight from
// the caller's slice, so a batch is never copied as a whole.
const (
	stagingSize          = 1 << 20
	directWriteThreshold = 256 << 10
)

// PayloadSize validates entries as one frame and returns the exact payload
// length. All arithmetic is uint64 and checked against MaxPayload after every
// addition, so it cannot overflow (each addend is <= MaxEntrySize, far below
// 2^64 - MaxPayload).
//
// maxOps is the op-count limit to enforce: MaxBatchOps for user batches,
// MaxFrameOps for compaction frames.
func PayloadSize(entries []Entry, maxOps int) (uint64, error) {
	if maxOps < 1 || maxOps > MaxFrameOps {
		return 0, invalid("frame header", "max_ops", uint64(maxOps), "must be 1..65535")
	}
	if len(entries) == 0 {
		return 0, invalid("frame header", "op_count", 0, "must be >= 1")
	}
	if len(entries) > maxOps {
		return 0, invalid("frame header", "op_count", uint64(len(entries)),
			fmt.Sprintf("exceeds limit %d", maxOps))
	}
	var total uint64
	for i := range entries {
		e := &entries[i]
		if err := ValidateEntry(e.Op, e.Key, e.Value); err != nil {
			return 0, fmt.Errorf("entry %d: %w", i, err)
		}
		total += EntryHeaderSize + uint64(len(e.Key)) + uint64(len(e.Value))
		if total > MaxPayload {
			return 0, invalid("frame header", "payload_len", total, "exceeds MaxPayload (2^40)")
		}
	}
	return total, nil
}

// WriteFrame streams one complete frame (header + payload) to w and returns
// the number of bytes written.
//
// It makes two passes over the caller's slices: pass 1 computes every
// entry_crc and the payload CRC without copying anything; pass 2 writes the
// header and then the entries, strictly in file order. Because bytes reach w
// sequentially, a process killed mid-call leaves a strict prefix of the frame,
// which can never pass the payload CRC.
//
// Caller contract: the key and value slices must not be modified while
// WriteFrame runs. A change between the passes would produce a frame whose
// CRC does not match its bytes; recovery would then discard a frame that may
// already have been acknowledged.
//
// On error, an unknown prefix of the frame may have been written; the caller
// (the storage write path) must truncate back to the frame start.
func WriteFrame(w io.Writer, seq uint64, entries []Entry, maxOps int) (int64, error) {
	payloadLen, err := PayloadSize(entries, maxOps)
	if err != nil {
		return 0, err
	}

	// Pass 1: checksums.
	entryCRCs := make([]uint32, len(entries))
	var payloadCRC uint32
	var eh [EntryHeaderSize]byte
	for i := range entries {
		e := &entries[i]
		putEntryHeaderPrefix(eh[:], e.Op, uint16(len(e.Key)), uint32(len(e.Value)))
		c := EntryChecksum(e.Op, e.Key, e.Value)
		entryCRCs[i] = c
		putUint32(eh[ehOffCRC:], c)
		payloadCRC = UpdateChecksum(payloadCRC, eh[:])
		payloadCRC = UpdateChecksum(payloadCRC, e.Key)
		payloadCRC = UpdateChecksum(payloadCRC, e.Value)
	}

	hdr, err := FrameHeader{
		OpCount:    uint16(len(entries)),
		Seq:        seq,
		PayloadLen: payloadLen,
		PayloadCRC: payloadCRC,
	}.Encode()
	if err != nil {
		return 0, err
	}

	// Pass 2: write in file order.
	sw := stagedWriter{w: w, buf: make([]byte, 0, min(stagingSize, int(FrameHeaderSize+payloadLen)))}
	sw.write(hdr[:])
	for i := range entries {
		e := &entries[i]
		putEntryHeaderPrefix(eh[:], e.Op, uint16(len(e.Key)), uint32(len(e.Value)))
		putUint32(eh[ehOffCRC:], entryCRCs[i])
		sw.write(eh[:])
		sw.write(e.Key)
		if len(e.Value) >= directWriteThreshold {
			sw.flush()
			sw.writeDirect(e.Value)
		} else {
			sw.write(e.Value)
		}
	}
	sw.flush()
	if sw.err != nil {
		return sw.n, sw.err
	}
	if want := int64(FrameHeaderSize + payloadLen); sw.n != want {
		return sw.n, fmt.Errorf("format: wrote %d bytes, expected %d: %w", sw.n, want, io.ErrShortWrite)
	}
	return sw.n, nil
}

func putUint32(b []byte, v uint32) {
	_ = b[3]
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

// stagedWriter batches small writes into a buffer and remembers the first
// error; after an error every call is a no-op.
type stagedWriter struct {
	w   io.Writer
	buf []byte
	n   int64
	err error
}

func (s *stagedWriter) write(p []byte) {
	for len(p) > 0 && s.err == nil {
		space := cap(s.buf) - len(s.buf)
		if space == 0 {
			s.flush()
			continue
		}
		k := min(space, len(p))
		s.buf = append(s.buf, p[:k]...)
		p = p[k:]
	}
}

func (s *stagedWriter) flush() {
	if s.err != nil || len(s.buf) == 0 {
		return
	}
	s.writeDirect(s.buf)
	s.buf = s.buf[:0]
}

func (s *stagedWriter) writeDirect(p []byte) {
	if s.err != nil || len(p) == 0 {
		return
	}
	n, err := s.w.Write(p)
	s.n += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	s.err = err
}

// EntryView is one entry seen by ScanPayload. Key and Value alias internal
// buffers and are valid only during the callback; copy them to keep them.
type EntryView struct {
	Header EntryHeader
	Key    []byte
	Value  []byte
	// Offset is the position of the entry header relative to the start of
	// the payload. Its absolute file offset is frameStart + 32 + Offset.
	Offset uint64
}

// ScanPayload reads exactly h.PayloadLen bytes from r, parses and verifies
// every entry, and verifies the payload CRC and op_count.
//
// IMPORTANT: fn is called for each entry BEFORE the payload CRC is known. The
// entries are tentative: the caller must buffer them and apply them only if
// ScanPayload returns nil. This is what keeps a torn batch all-or-nothing.
// If fn returns an error, scanning stops and that error is returned.
//
// Classification of failures:
//   - The reader ends early: ErrTruncated (Damaged).
//   - The reader fails: its error, wrapped (IO). Never reported as EOF.
//   - Payload CRC mismatch: ErrPayloadChecksum (Damaged).
//   - Payload CRC matches but entries are malformed, an entry CRC fails, the
//     entry count differs from op_count, or bytes are left over: ErrMalformed
//     (Corrupt).
//
// To tell the last two apart, a structural problem does not stop the scan:
// the rest of the payload is drained through the CRC first. The caller has
// already bounded h.PayloadLen by the file size (PayloadFits), so draining is
// bounded too.
func ScanPayload(r io.Reader, h FrameHeader, fn func(EntryView) error) error {
	if h.PayloadLen > MaxPayload {
		return invalid("frame header", "payload_len", h.PayloadLen, "exceeds MaxPayload (2^40)")
	}
	cr := &crcReader{r: r}
	n := h.PayloadLen

	var structErr error
	var hdrBuf [EntryHeaderSize]byte
	var data []byte // reused buffer for key+value
	var count uint64

	for structErr == nil && cr.n < n {
		remaining := n - cr.n
		if remaining < EntryHeaderSize {
			structErr = malformed("entry", "remaining", remaining, "trailing bytes shorter than an entry header")
			break
		}
		off := cr.n
		if err := cr.readFull(hdrBuf[:]); err != nil {
			return err
		}
		eh, err := DecodeEntryHeader(hdrBuf[:])
		if err != nil {
			structErr = fmt.Errorf("entry %d at payload offset %d: %w", count, off, err)
			break
		}
		if err := CheckEntryFits(eh, remaining); err != nil {
			structErr = fmt.Errorf("entry %d at payload offset %d: %w", count, off, err)
			break
		}
		if count >= uint64(h.OpCount) {
			structErr = malformed("frame header", "op_count", uint64(h.OpCount), "payload holds more entries")
			break
		}
		kv := int(eh.KeyLen) + int(eh.ValueLen) // <= 1024 + 1 MiB
		if cap(data) < kv {
			data = make([]byte, kv)
		}
		data = data[:kv]
		if err := cr.readFull(data); err != nil {
			return err
		}
		key, value := data[:eh.KeyLen], data[eh.KeyLen:]
		if err := VerifyEntry(eh, key, value); err != nil {
			structErr = fmt.Errorf("entry %d at payload offset %d: %w: %w", count, off, ErrMalformed, err)
			break
		}
		count++
		if fn != nil {
			if err := fn(EntryView{Header: eh, Key: key, Value: value, Offset: off}); err != nil {
				return err
			}
		}
	}
	if structErr == nil && count != uint64(h.OpCount) {
		structErr = malformed("frame header", "op_count", uint64(h.OpCount),
			fmt.Sprintf("payload holds %d entries", count))
	}

	// Drain whatever is left so the payload CRC covers all n bytes.
	if cr.n < n {
		if _, err := io.CopyN(io.Discard, cr, int64(n-cr.n)); err != nil {
			return classifyReadErr(err)
		}
	}
	if cr.crc != h.PayloadCRC {
		return fmt.Errorf("%w (stored %#08x, computed %#08x)", ErrPayloadChecksum, h.PayloadCRC, cr.crc)
	}
	return structErr
}

// crcReader feeds every byte it reads into a running CRC32C and counts them.
// Every payload byte goes through it exactly once.
type crcReader struct {
	r   io.Reader
	crc uint32
	n   uint64
}

func (c *crcReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	if k > 0 {
		c.crc = UpdateChecksum(c.crc, p[:k])
		c.n += uint64(k)
	}
	return k, err
}

func (c *crcReader) readFull(p []byte) error {
	if _, err := io.ReadFull(c, p); err != nil {
		return classifyReadErr(err)
	}
	return nil
}

// classifyReadErr turns end-of-input into ErrTruncated and wraps anything
// else as an I/O error. A real read error is never reported as EOF.
func classifyReadErr(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w: payload ended early", ErrTruncated)
	}
	return fmt.Errorf("format: reading payload: %w", err)
}
