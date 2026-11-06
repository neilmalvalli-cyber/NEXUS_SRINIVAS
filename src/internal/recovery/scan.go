package recovery

import (
	"bufio"
	"errors"
	"fmt"
	"io"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/dberr"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/index"
)

// ScanBufferSize is the sequential read buffer used by Scan (DESIGN §8.4).
const ScanBufferSize = 4 << 20

// TailRule names the tail rule that ended a scan (DESIGN §8.2).
type TailRule string

const (
	TailClean TailRule = ""   // the last frame ends exactly at EOF
	TailC1    TailRule = "C1" // fewer than 32 bytes after the last frame
	TailC2    TailRule = "C2" // damaged header (bad magic / header CRC); no forward scan (C3)
	TailC4    TailRule = "C4" // valid header, payload runs past EOF
	TailC5    TailRule = "C5" // payload CRC wrong, frame ends exactly at EOF
)

// ScanResult is the state rebuilt from the log. Scan never modifies the
// file; when Tail != TailClean the caller truncates to End (which Scan has
// proven to be allowed) before accepting writes.
type ScanResult struct {
	Index *index.Index
	// NextSeq is the sequence number of the next frame to write. 0 means
	// the sequence space is exhausted (a frame with seq 2^64-1 exists);
	// every further write must be refused.
	NextSeq uint64
	Frames  uint64 // valid frames applied
	// End is the end of the last valid frame. Bytes in [End, Size) are the
	// torn final frame identified by Tail.
	End  int64
	Size int64
	Tail TailRule
	// TailCause is the decode error that identified the torn tail (for
	// diagnostics); nil when Tail is TailClean.
	TailCause error
}

// DeadBytes returns the log bytes after the superblock that are not live PUT
// entries (frame headers, DEL entries, overwritten PUTs), up to End.
func (r *ScanResult) DeadBytes() uint64 {
	return uint64(r.End) - format.SuperblockSize - r.Index.LiveBytes()
}

type pendingOp struct {
	op   format.OpCode
	key  string
	off  int64
	size uint32
}

// Scan rebuilds the index from the log in r (DESIGN §8 steps 4–10) and
// classifies the tail with rules C0–C8 (DESIGN §8.2). It only reads.
//
// sb must be the file's decoded superblock and size the file size, with
// sb.SnapshotEnd <= size already checked. path is used in error messages.
//
// Errors:
//   - C0, C6, C7 and snapshot_end not on a frame boundary: dberr.ErrCorruption.
//   - C8 (a read error, or the file ending before the size said it would):
//     the read error, wrapped; never ErrCorruption, never a truncation.
//
// Entries are applied to the index only after their whole frame has passed
// every check (DESIGN §8.1), so a torn batch never partially appears.
func Scan(r io.ReaderAt, path string, sb format.Superblock, size int64) (*ScanResult, error) {
	if size < format.SuperblockSize || sb.SnapshotEnd > uint64(size) {
		return nil, fmt.Errorf("recovery: internal error: Scan called with size %d, snapshot_end %d", size, sb.SnapshotEnd)
	}
	res := &ScanResult{
		Index: index.New(0),
		Size:  size,
		End:   format.SuperblockSize,
	}
	usize := uint64(size)
	snapEnd := sb.SnapshotEnd
	expected := sb.FirstSeq // 0 once exhausted
	pos := uint64(format.SuperblockSize)

	br := bufio.NewReaderSize(io.NewSectionReader(r, format.SuperblockSize, size-format.SuperblockSize), ScanBufferSize)
	var hdr [format.FrameHeaderSize]byte
	var pending []pendingOp

	corrupt := func(rule string, cause error) error {
		return fmt.Errorf("%w: %s: frame at offset %d (rule %s): %v", dberr.ErrCorruption, path, pos, rule, cause)
	}
	// tail decides between truncation (C1, C2, C4, C5) and C0.
	tail := func(rule TailRule, cause error) (*ScanResult, error) {
		if pos < snapEnd {
			return nil, corrupt("C0", fmt.Errorf("damage inside the sealed snapshot (snapshot_end %d), would be %s: %w", snapEnd, rule, cause))
		}
		res.Tail, res.TailCause = rule, cause
		res.NextSeq = expected
		return res, nil
	}
	ioErr := func(what string, err error) error {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, format.ErrTruncated) {
			// The size said these bytes exist. A short read is an I/O
			// anomaly (file changed underneath, faulty device), never
			// evidence of a torn write: fail without truncating (C8).
			return fmt.Errorf("recovery: reading %s %s at offset %d: unexpected end of file (size %d): %w", path, what, pos, size, err)
		}
		return fmt.Errorf("recovery: reading %s %s at offset %d (rule C8): %w", path, what, pos, err)
	}

	for pos < usize {
		if !format.HeaderFits(pos, usize) { // C1
			return tail(TailC1, fmt.Errorf("%w: %d trailing bytes, frame header needs %d", format.ErrTruncated, usize-pos, format.FrameHeaderSize))
		}
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return nil, ioErr("frame header", err)
		}
		h, err := format.DecodeFrameHeader(hdr[:])
		if err != nil {
			switch format.Classify(err) {
			case format.ClassDamaged: // C2; C3: never scan forward
				return tail(TailC2, err)
			default: // CRC-valid but malformed header: C7
				return nil, corrupt("C7", err)
			}
		}
		if expected == 0 || h.Seq != expected {
			return nil, corrupt("C7", fmt.Errorf("sequence %d, expected %d", h.Seq, expected))
		}
		if !format.PayloadFits(pos, usize, h.PayloadLen) { // C4
			return tail(TailC4, fmt.Errorf("%w: payload_len %d runs past end of file (size %d)", format.ErrTruncated, h.PayloadLen, size))
		}
		frameEnd := pos + format.FrameHeaderSize + h.PayloadLen // cannot overflow: PayloadFits

		pending = pending[:0]
		payloadStart := int64(pos) + format.FrameHeaderSize
		err = format.ScanPayload(io.LimitReader(br, int64(h.PayloadLen)), h, func(e format.EntryView) error {
			pending = append(pending, pendingOp{
				op:   e.Header.Op,
				key:  string(e.Key),
				off:  payloadStart + int64(e.Offset),
				size: uint32(e.Header.Size()),
			})
			return nil
		})
		if err != nil {
			switch {
			case errors.Is(err, format.ErrTruncated):
				return nil, ioErr("payload", err)
			case format.Classify(err) == format.ClassDamaged: // payload CRC
				if frameEnd == usize {
					return tail(TailC5, err)
				}
				if pos < snapEnd {
					return nil, corrupt("C0", err)
				}
				return nil, corrupt("C6", fmt.Errorf("%w; %d bytes follow the frame's declared end, impossible for a torn write", err, usize-frameEnd))
			case format.Classify(err) == format.ClassCorrupt:
				return nil, corrupt("C7", err)
			default:
				return nil, ioErr("payload", err)
			}
		}
		if pos < snapEnd && frameEnd > snapEnd {
			return nil, corrupt("C0", fmt.Errorf("snapshot_end %d is not on a frame boundary (frame ends at %d)", snapEnd, frameEnd))
		}

		// The whole frame is valid: apply it.
		for _, p := range pending {
			if p.op == format.OpPut {
				res.Index.Put(p.key, index.Loc{Off: p.off, Size: p.size})
			} else {
				res.Index.Delete([]byte(p.key))
			}
		}
		res.Frames++
		if next, err := format.NextSeq(expected); err == nil {
			expected = next
		} else {
			expected = 0 // exhausted; any later CRC-valid frame is C7
		}
		pos = frameEnd
		res.End = int64(pos)
	}
	res.NextSeq = expected
	return res, nil
}
