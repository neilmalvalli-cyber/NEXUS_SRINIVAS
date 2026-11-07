package xray

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/format"
	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/src/internal/recovery"
)

// Byte offsets of on-disk fields (docs/FILE_FORMAT.md). The decoders in
// package format are authoritative; these offsets are used only to show the
// raw stored value of a field that failed validation.
const (
	sbOffVersion  = 8
	sbOffCRC      = 28
	fhOffMagic    = 0
	fhOffSeq      = 8
	fhOffPayLen   = 16
	fhOffPayCRC   = 24
	fhOffHdrCRC   = 28
	maxEntryWalk  = 256 << 20 // payloads larger than this are checked by streaming, without per-entry detail
	walkBufferLen = 4 << 20
)

// walker collects findings while walking one file.
type walker struct {
	r    *Report
	file string
	w    *Walk

	deadRatio float64
	deadMin   int64
}

func u64(v uint64) *uint64  { return &v }
func i64(v int64) *int64    { return &v }
func hex32(v uint32) string { return fmt.Sprintf("0x%08x", v) }

// walkFile walks the superblock and every frame of name, read-only. It
// mirrors recovery.Scan's rule order (C1, C2/C3, C7, C4, payload, C5/C6/C0)
// but stops to describe the first failure in detail instead of returning a
// sentinel. It never scans forward past damage, except for one exactly
// positioned, CRC-checked look at the header that should follow a damaged
// frame whose own header is valid (evidence for C6).
func (x *walker) walkFile(name string) error {
	f, err := os.Open(name) // read-only
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size := fi.Size()
	x.w = &Walk{File: name, Size: size, ValidEnd: format.SuperblockSize}
	x.r.Walk = x.w

	if size < format.SuperblockSize {
		x.w.ValidEnd = 0
		x.add(Finding{Code: "SB_TOO_SHORT", Severity: Critical, Category: CatSuperblock,
			Title:     "File too short to be a NEXUS database",
			Simple:    fmt.Sprintf("The file is only %d bytes long. Every NEXUS database starts with a 32-byte header, so this file has no valid header.", size),
			Technical: fmt.Sprintf("file size %d < SuperblockSize %d", size, format.SuperblockSize),
			Offset:    i64(0), Expected: fmt.Sprintf(">= %d bytes", format.SuperblockSize), Actual: fmt.Sprintf("%d bytes", size),
			Component: "format/superblock", DataImpact: "No database state can be read from this file.",
			Recovery: Unrecoverable, Action: "Restore the database from a backup. Do not reuse this file."})
		return nil
	}
	var sbb [format.SuperblockSize]byte
	if _, err := f.ReadAt(sbb[:], 0); err != nil {
		return err
	}
	sb, err := format.DecodeSuperblock(sbb[:])
	if err != nil {
		x.superblockFinding(sbb[:], err)
		x.w.ValidEnd = 0
		return nil
	}
	x.w.Superblock = &SB{Version: binary.LittleEndian.Uint16(sbb[sbOffVersion:]), FirstSeq: sb.FirstSeq, SnapshotEnd: sb.SnapshotEnd}
	if err := sb.CheckFileSize(uint64(size)); err != nil {
		x.add(Finding{Code: "SB_SNAPSHOT_BEYOND_EOF", Severity: Critical, Category: CatSuperblock,
			Title:     "Compacted snapshot is missing bytes",
			Simple:    "The header says the file holds a compacted snapshot that ends past the end of the file. Part of the sealed snapshot is missing.",
			Technical: fmt.Sprintf("superblock.snapshot_end %d > file size %d (DESIGN note D1: ours but damaged)", sb.SnapshotEnd, size),
			Offset:    i64(20), Expected: fmt.Sprintf("snapshot_end <= %d", size), Actual: fmt.Sprintf("%d", sb.SnapshotEnd),
			Component: "format/superblock", DataImpact: "Committed snapshot data is missing; NEXUS will not open the file.",
			Recovery: Refused, Action: "Restore from a backup. Keep this file for analysis; do not delete it.", OriginalError: err.Error()})
		return nil
	}
	return x.walkFrames(f, sb, size)
}

func (x *walker) superblockFinding(b []byte, err error) {
	base := Finding{Category: CatSuperblock, Severity: Critical, Component: "format/superblock", Offset: i64(0),
		OriginalError: err.Error(), Recovery: Unrecoverable,
		DataImpact: "NEXUS cannot read any state from this file and will not open it.",
		Action:     "Restore the database from a backup. Keep this file unchanged for analysis."}
	magic := format.SuperblockMagic()
	var fe *format.FieldError
	switch {
	case errors.Is(err, format.ErrBadMagic):
		base.Code, base.Title = "SB_BAD_MAGIC", "Database header has the wrong signature"
		base.Simple = "The first 8 bytes of the file should spell the NEXUS signature. They do not, so this is either not a NEXUS database or its header was overwritten."
		base.Technical = "superblock magic mismatch (rule A2 if this is the database path)"
		base.Expected, base.Actual = fmt.Sprintf("% x (%q)", magic[:], magic[:]), fmt.Sprintf("% x", b[:8])
	case errors.Is(err, format.ErrHeaderChecksum):
		stored := binary.LittleEndian.Uint32(b[sbOffCRC:])
		computed := format.Checksum(b[:sbOffCRC])
		base.Code, base.Title = "SB_CHECKSUM", "Database header checksum mismatch"
		base.Simple = "The database header is protected by a checksum. The stored checksum does not match the header bytes, so at least one header byte has changed since it was written."
		base.Technical = "CRC32C over superblock bytes [0:28) differs from the stored crc at offset 28"
		base.Offset = i64(sbOffCRC)
		base.Expected, base.Actual = hex32(computed)+" (computed)", hex32(stored)+" (stored)"
	case errors.Is(err, format.ErrUnsupportedVersion):
		v := binary.LittleEndian.Uint16(b[sbOffVersion:])
		base.Code, base.Title = "SB_UNSUPPORTED_VERSION", "Unsupported format version"
		base.Simple = fmt.Sprintf("The header is intact (its checksum is correct) but it says format version %d. This build of NEXUS reads only version %d.", v, format.FormatVersion)
		base.Technical = "superblock version field (offset 8) is CRC-valid but not FormatVersion"
		base.Offset = i64(sbOffVersion)
		base.Expected, base.Actual = fmt.Sprintf("%d", format.FormatVersion), fmt.Sprintf("%d", v)
		base.DataImpact = "The data may be intact, but this build cannot interpret it."
		base.Recovery = Refused
		base.Action = "Open the file with the NEXUS version that wrote it. Do not modify it."
	case errors.As(err, &fe):
		base.Code, base.Title = "SB_MALFORMED", "Database header field is invalid"
		base.Simple = fmt.Sprintf("The header checksum is correct but the %s field holds a value NEXUS never writes.", fe.Field)
		base.Technical = fmt.Sprintf("superblock.%s = %d: %s", fe.Field, fe.Value, fe.Reason)
		base.Expected, base.Actual = fe.Reason, fmt.Sprintf("%d", fe.Value)
	default:
		base.Code, base.Title = "SB_INVALID", "Database header is invalid"
		base.Simple = "The database header could not be decoded."
		base.Technical = err.Error()
	}
	x.add(base)
}

// walkFrames walks frames from offset 32. size is the file size.
func (x *walker) walkFrames(f *os.File, sb format.Superblock, size int64) error {
	usize := uint64(size)
	pos := uint64(format.SuperblockSize)
	expected := sb.FirstSeq
	br := bufio.NewReaderSize(io.NewSectionReader(f, format.SuperblockSize, size-format.SuperblockSize), walkBufferLen)
	var hdr [format.FrameHeaderSize]byte
	frameNo := uint64(0)

	// stop records where and why the walk ended.
	stop := func(rule string) {
		x.w.StoppedAt = i64(int64(pos))
		x.w.StopRule = rule
	}
	inSnapshot := func() bool { return pos < sb.SnapshotEnd }

	for pos < usize {
		frameNo++
		if !format.HeaderFits(pos, usize) {
			stop(x.tailOrC0(inSnapshot(), "C1"))
			x.tailFinding("FRAME_TORN_HEADER", "C1", inSnapshot(), pos, frameNo, nil,
				"Incomplete final frame header",
				fmt.Sprintf("The file ends %d bytes into a new frame, before its 32-byte header was complete. This is what an interrupted write leaves behind.", usize-pos),
				fmt.Sprintf("%d trailing bytes < FrameHeaderSize %d", usize-pos, format.FrameHeaderSize),
				fmt.Sprintf("%d header bytes", format.FrameHeaderSize), fmt.Sprintf("%d bytes", usize-pos), nil)
			break
		}
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return fmt.Errorf("reading frame header at %d: %w", pos, err)
		}
		storedSeq := binary.LittleEndian.Uint64(hdr[fhOffSeq:])
		h, err := format.DecodeFrameHeader(hdr[:])
		if err != nil {
			if format.Classify(err) == format.ClassDamaged {
				rule := x.tailOrC0(inSnapshot(), "C2")
				stop(rule)
				var exp, act, tech string
				if errors.Is(err, format.ErrBadMagic) {
					m := binary.LittleEndian.Uint32(hdr[fhOffMagic:])
					exp, act = hex32(format.FrameMagic), hex32(m)
					tech = "frame header magic mismatch"
				} else {
					stored := binary.LittleEndian.Uint32(hdr[fhOffHdrCRC:])
					exp, act = hex32(format.Checksum(hdr[:fhOffHdrCRC]))+" (computed)", hex32(stored)+" (stored)"
					tech = "CRC32C over frame header bytes [0:28) differs from the stored header_crc"
				}
				x.tailFinding("FRAME_HEADER_DAMAGED", "C2", inSnapshot(), pos, frameNo, nil,
					"Damaged frame header",
					"The header of this frame does not pass its integrity check, and no complete intact frame follows it, so this frame and everything after it are the torn end of an interrupted write (rule C2). NEXUS never applies anything past a damaged header.",
					tech+fmt.Sprintf("; %d bytes from here to end of file", usize-pos), exp, act, err)
				if !inSnapshot() {
					x.laterFrames(f, pos, usize, expected)
				}
				break
			}
			stop("C7")
			var fe *format.FieldError
			field, val, reason := "", uint64(0), err.Error()
			if errors.As(err, &fe) {
				field, val, reason = fe.Field, fe.Value, fe.Reason
			}
			x.add(Finding{Code: "FRAME_HEADER_MALFORMED", Severity: Critical, Category: CatFrame, Rule: "C7",
				Title:     "Frame header holds an impossible value",
				Simple:    fmt.Sprintf("This frame's header passes its checksum, so it was written whole, but its %s field holds a value NEXUS never writes. A torn write cannot produce this.", field),
				Technical: fmt.Sprintf("frame header %s = %d: %s (CRC-valid, so rule C7: corruption)", field, val, reason),
				Offset:    i64(int64(pos)), Frame: u64(frameNo), Seq: u64(storedSeq), Expected: reason, Actual: fmt.Sprintf("%d", val),
				Component: "recovery/scan", DataImpact: "A committed frame is invalid; NEXUS will not open the file.",
				Recovery: Refused, Action: "Restore from a backup, or keep this file for expert analysis. Do not truncate it.", OriginalError: err.Error()})
			break
		}
		if expected == 0 || h.Seq != expected {
			stop("C7")
			exp := fmt.Sprintf("%d", expected)
			if expected == 0 {
				exp = "none (sequence space exhausted by the previous frame)"
			}
			x.add(Finding{Code: "SEQ_MISMATCH", Severity: Critical, Category: CatSequence, Rule: "C7",
				Title:     "Frame sequence number out of order",
				Simple:    fmt.Sprintf("Every frame carries a sequence number one higher than the previous one. This frame is intact (checksum correct) but carries %d where %s was required, so frames are missing, duplicated or reordered.", h.Seq, exp),
				Technical: fmt.Sprintf("frame seq %d, expected %s (rule C7)", h.Seq, exp),
				Offset:    i64(int64(pos + fhOffSeq)), Frame: u64(frameNo), Seq: u64(h.Seq), Expected: exp, Actual: fmt.Sprintf("%d", h.Seq),
				Component: "recovery/scan", DataImpact: "The order of committed writes cannot be trusted from this point; NEXUS will not open the file.",
				Recovery: Refused, Action: "Restore from a backup. Keep this file unchanged for analysis."})
			break
		}
		if !format.PayloadFits(pos, usize, h.PayloadLen) {
			stop(x.tailOrC0(inSnapshot(), "C4"))
			x.tailFinding("FRAME_PAYLOAD_TRUNCATED", "C4", inSnapshot(), pos, frameNo, u64(h.Seq),
				"Incomplete final frame",
				"This frame's header is intact, but the file ends before all of its data was written. This is what an interrupted write leaves behind.",
				fmt.Sprintf("payload_len %d, only %d payload bytes before end of file", h.PayloadLen, usize-pos-format.FrameHeaderSize),
				fmt.Sprintf("%d payload bytes", h.PayloadLen), fmt.Sprintf("%d bytes", usize-pos-format.FrameHeaderSize), nil)
			break
		}
		frameEnd := pos + format.FrameHeaderSize + h.PayloadLen
		puts, dels, crc, perr := x.checkPayload(br, h, pos, frameNo)
		if perr != nil {
			if format.Classify(perr) == format.ClassDamaged && !errors.Is(perr, format.ErrTruncated) {
				// Payload CRC mismatch.
				computed := "a different CRC32C (payload larger than the detailed-walk limit; streamed)"
				if crc != nil {
					computed = hex32(*crc) + " (computed)"
				}
				if frameEnd == usize && !inSnapshot() {
					stop("C5")
					x.tailFinding("FRAME_PAYLOAD_CHECKSUM", "C5", false, pos, frameNo, u64(h.Seq),
						"Final frame data checksum mismatch",
						"The last frame in the file is the right length but its data does not match its checksum. As the very last frame, this is consistent with a write interrupted before the data reached the disk.",
						"payload CRC32C mismatch on the frame that ends exactly at end of file (rule C5)",
						computed, hex32(h.PayloadCRC)+" (stored)", perr)
					break
				}
				rule := "C6"
				if inSnapshot() {
					rule = "C0"
				}
				stop(rule)
				next := x.nextFrameIntact(f, frameEnd, usize, h.Seq)
				x.w.NextFrameIntact = next
				evidence := ""
				if next != nil && *next {
					evidence = fmt.Sprintf(" The next frame (sequence %d) starts exactly where this one ends and is intact, so committed data follows the damage.", h.Seq+1)
				}
				x.add(Finding{Code: "FRAME_PAYLOAD_CHECKSUM", Severity: Critical, Category: CatFrame, Rule: rule,
					Title:     "Committed frame data is corrupted",
					Simple:    fmt.Sprintf("The data of frame %d (sequence %d) does not match its checksum, and %d more bytes follow it in the file. A crash only damages the last frame, so this is corruption of data that had already been written.%s", frameNo, h.Seq, usize-frameEnd, evidence),
					Technical: fmt.Sprintf("payload CRC32C mismatch over [%d, %d); %d bytes follow the frame's declared end (rule %s)", pos+format.FrameHeaderSize, frameEnd, usize-frameEnd, rule),
					Offset:    i64(int64(pos)), Frame: u64(frameNo), Seq: u64(h.Seq), Expected: computed, Actual: hex32(h.PayloadCRC) + " (stored)",
					Component: "recovery/scan", DataImpact: fmt.Sprintf("Frame %d's %d operation(s) cannot be trusted; NEXUS will not open the file rather than discard committed data.", frameNo, h.OpCount),
					Recovery: Refused, Action: "Restore from a backup. Do not truncate: data after the damage would be lost. Keep this file for analysis.", OriginalError: perr.Error()})
				break
			}
			if errors.Is(perr, format.ErrTruncated) {
				return fmt.Errorf("frame at %d: short read inside the file size: %w", pos, perr)
			}
			if format.Classify(perr) == format.ClassIO {
				return perr
			}
			// Corrupt: payload CRC is right but the entries inside are not (C7).
			stop("C7")
			break // checkPayload added the operation finding
		}
		if inSnapshot() && frameEnd > sb.SnapshotEnd {
			stop("C0")
			x.add(Finding{Code: "SNAPSHOT_BOUNDARY", Severity: Critical, Category: CatFrame, Rule: "C0",
				Title:     "Snapshot boundary falls inside a frame",
				Simple:    "The header marks where the compacted snapshot ends, but that position falls in the middle of a frame. The header and the data disagree.",
				Technical: fmt.Sprintf("snapshot_end %d is not on a frame boundary (frame %d spans [%d, %d))", sb.SnapshotEnd, frameNo, pos, frameEnd),
				Offset:    i64(int64(pos)), Frame: u64(frameNo), Seq: u64(h.Seq), Expected: fmt.Sprintf("a frame boundary"), Actual: fmt.Sprintf("snapshot_end %d", sb.SnapshotEnd),
				Component: "recovery/scan", DataImpact: "The snapshot cannot be trusted; NEXUS will not open the file.",
				Recovery: Refused, Action: "Restore from a backup."})
			break
		}
		x.w.Frames++
		x.w.Entries += puts + dels
		x.w.Puts += puts
		x.w.Dels += dels
		if next, err := format.NextSeq(expected); err == nil {
			expected = next
		} else {
			expected = 0
		}
		pos = frameEnd
		x.w.ValidEnd = int64(pos)
	}
	x.w.TrailingLen = size - x.w.ValidEnd
	return nil
}

// tailOrC0 returns rule, or C0 when the damage lies inside the snapshot.
func (x *walker) tailOrC0(inSnap bool, rule string) string {
	if inSnap {
		return "C0"
	}
	return rule
}

// tailFinding records a torn-tail condition (C1, C2, C4, C5): repairable by
// a normal open, unless it lies inside the sealed snapshot (C0).
func (x *walker) tailFinding(code, rule string, inSnap bool, pos, frameNo uint64, seq *uint64, title, simple, tech, exp, act string, cause error) {
	size := x.w.Size
	f := Finding{Code: code, Category: CatFrame, Rule: rule, Title: title, Simple: simple,
		Technical: tech + fmt.Sprintf(" (rule %s)", rule),
		Offset:    i64(int64(pos)), Frame: u64(frameNo), Seq: seq, Expected: exp, Actual: act,
		Component: "recovery/scan"}
	if cause != nil {
		f.OriginalError = cause.Error()
	}
	if inSnap {
		f.Severity, f.Rule, f.Recovery = Critical, "C0", Refused
		f.Title = title + " inside the compacted snapshot"
		f.Simple = simple + " However, this position is inside the sealed snapshot written by compaction, which was fully synced before it was used, so a crash cannot explain it."
		f.Technical = tech + fmt.Sprintf(" at %d < snapshot_end (rule C0)", pos)
		f.DataImpact = "Snapshot data is damaged; NEXUS will not open the file."
		f.Action = "Restore from a backup. Keep this file for analysis."
	} else {
		f.Severity, f.Recovery = Warning, Recoverable
		f.DataImpact = fmt.Sprintf("The %d bytes from offset %d to end of file belong to a write that was never acknowledged (acknowledgement happens only after the whole frame is synced). No acknowledged data is affected.", size-int64(pos), pos)
		f.Action = fmt.Sprintf("A normal open will cut the file to %d bytes automatically. Run `kvdb xray --repair` or open the database to apply it.", pos)
	}
	x.add(f)
}

// checkPayload reads h.PayloadLen bytes and checks them. For payloads up to
// maxEntryWalk it walks entries itself to report the exact entry, field and
// values of a structural problem; it always confirms the result with
// format.ScanPayload so its verdict is the engine's.
func (x *walker) checkPayload(br *bufio.Reader, h format.FrameHeader, pos, frameNo uint64) (puts, dels uint64, crc *uint32, err error) {
	count := func(e format.EntryView) error {
		if e.Header.Op == format.OpPut {
			puts++
		} else {
			dels++
		}
		return nil
	}
	if h.PayloadLen > maxEntryWalk {
		err = format.ScanPayload(io.LimitReader(br, int64(h.PayloadLen)), h, count)
		if err != nil && format.Classify(err) == format.ClassCorrupt {
			x.opFinding(pos, frameNo, h, nil, err)
		}
		if err != nil {
			puts, dels = 0, 0
		}
		return
	}
	buf := make([]byte, h.PayloadLen)
	if _, rerr := io.ReadFull(br, buf); rerr != nil {
		return 0, 0, nil, fmt.Errorf("%w: %v", format.ErrTruncated, rerr)
	}
	c := format.Checksum(buf)
	crc = &c
	err = format.ScanPayload(bytesReader(buf), h, count)
	if err == nil || format.Classify(err) != format.ClassCorrupt {
		if err != nil {
			puts, dels = 0, 0
		}
		return
	}
	puts, dels = 0, 0
	x.opFinding(pos, frameNo, h, buf, err)
	return
}

// opFinding locates the first structural problem in a CRC-valid payload by
// walking its entries with the format decoders.
func (x *walker) opFinding(pos, frameNo uint64, h format.FrameHeader, buf []byte, scanErr error) {
	payloadStart := pos + format.FrameHeaderSize
	f := Finding{Code: "OP_INVALID", Severity: Critical, Category: CatOperation, Rule: "C7",
		Title:     "Invalid operation inside an intact frame",
		Simple:    "This frame's data matches its checksum, so it was written whole, yet an operation inside it is invalid. A torn write cannot produce this.",
		Technical: scanErr.Error() + " (payload CRC valid, rule C7)",
		Offset:    i64(int64(payloadStart)), Frame: u64(frameNo), Seq: u64(h.Seq),
		Component: "recovery/scan", DataImpact: fmt.Sprintf("Frame %d (%d operations) cannot be applied; NEXUS will not open the file.", frameNo, h.OpCount),
		Recovery: Refused, Action: "Restore from a backup. Keep this file for analysis.", OriginalError: scanErr.Error()}
	if buf != nil {
		off, n := uint64(0), 0
		for off < uint64(len(buf)) {
			rem := uint64(len(buf)) - off
			if rem < format.EntryHeaderSize {
				f.Code, f.Offset = "OP_TRAILING_BYTES", i64(int64(payloadStart+off))
				f.Technical = fmt.Sprintf("%d trailing payload bytes after entry %d, shorter than an entry header (rule C7)", rem, n)
				f.Expected, f.Actual = "0 trailing bytes", fmt.Sprintf("%d", rem)
				break
			}
			eh, err := format.DecodeEntryHeader(buf[off:])
			var fe *format.FieldError
			if err != nil {
				f.Code, f.Offset = "OP_MALFORMED", i64(int64(payloadStart+off))
				if errors.As(err, &fe) {
					f.Technical = fmt.Sprintf("entry %d at file offset %d: %s = %d: %s (rule C7)", n, payloadStart+off, fe.Field, fe.Value, fe.Reason)
					f.Expected, f.Actual = fe.Reason, fmt.Sprintf("%s=%d", fe.Field, fe.Value)
					f.Simple = fmt.Sprintf("Operation %d in this intact frame has an invalid %s (%d). A torn write cannot produce this.", n+1, fe.Field, fe.Value)
				}
				break
			}
			if err := format.CheckEntryFits(eh, rem); err != nil {
				f.Code, f.Offset = "OP_LENGTH", i64(int64(payloadStart+off))
				f.Technical = fmt.Sprintf("entry %d at file offset %d: key_len %d + value_len %d run past the payload (%d bytes left) (rule C7)", n, payloadStart+off, eh.KeyLen, eh.ValueLen, rem)
				f.Expected, f.Actual = fmt.Sprintf("<= %d bytes", rem), fmt.Sprintf("%d bytes", eh.Size())
				break
			}
			kv := buf[off+format.EntryHeaderSize : off+eh.Size()]
			key, val := kv[:eh.KeyLen], kv[eh.KeyLen:]
			if got := format.EntryChecksum(eh.Op, key, val); got != eh.CRC {
				f.Code, f.Offset = "OP_ENTRY_CHECKSUM", i64(int64(payloadStart+off))
				f.Title = "Operation checksum mismatch inside an intact frame"
				f.Simple = fmt.Sprintf("Operation %d's own checksum does not match its key and value, even though the frame as a whole matches its checksum. This frame was written with bad content.", n+1)
				f.Technical = fmt.Sprintf("entry %d at file offset %d: entry_crc stored %s, computed %s (rule C7)", n, payloadStart+off, hex32(eh.CRC), hex32(got))
				f.Expected, f.Actual = hex32(got)+" (computed)", hex32(eh.CRC)+" (stored)"
				break
			}
			n++
			off += eh.Size()
		}
		if f.Code == "OP_INVALID" && uint64(n) != uint64(h.OpCount) {
			f.Code, f.Offset = "OP_COUNT_MISMATCH", i64(int64(pos+6))
			f.Technical = fmt.Sprintf("frame header op_count %d, payload holds %d entries (rule C7)", h.OpCount, n)
			f.Expected, f.Actual = fmt.Sprintf("%d entries", h.OpCount), fmt.Sprintf("%d entries", n)
			f.Simple = fmt.Sprintf("The frame header says it holds %d operations but its intact data holds %d.", h.OpCount, n)
		}
	}
	x.add(f)
}

// nextFrameIntact looks at exactly one position: where a damaged frame with
// a valid header says it ends. It reports whether a CRC-valid header with
// the following sequence number starts there (nil when nothing fits).
func (x *walker) nextFrameIntact(f *os.File, at, size, seq uint64) *bool {
	if !format.HeaderFits(at, size) {
		return nil
	}
	var b [format.FrameHeaderSize]byte
	if _, err := f.ReadAt(b[:], int64(at)); err != nil {
		return nil
	}
	h, err := format.DecodeFrameHeader(b[:])
	ok := err == nil && h.Seq == seq+1
	return &ok
}

type sliceReader struct {
	b []byte
	i int
}

func bytesReader(b []byte) *sliceReader { return &sliceReader{b: b} }

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

// laterFrames applies the engine's own rule C3 evidence check
// (recovery.LaterCommittedFrame, the same function open uses, so X-RAY and
// the engine cannot disagree). After a damaged header outside the snapshot,
// a torn write can only affect the last frame, so a complete, fully valid
// frame with a sequence number >= expected after it proves mid-log damage.
// Open then refuses instead of truncating; X-RAY reports the same.
func (x *walker) laterFrames(f *os.File, pos, size, expected uint64) {
	if at, seq, ok := recovery.LaterCommittedFrame(f, pos, size, expected); ok {
		x.raiseTailLoss(pos, at, seq, size)
	}
}

// raiseTailLoss rewrites the C2 finding at pos into the C3 refusal:
// committed frames follow the damaged header.
func (x *walker) raiseTailLoss(pos, at, seq, size uint64) {
	for i := range x.r.Findings {
		g := &x.r.Findings[i]
		if g.Code != "FRAME_HEADER_DAMAGED" || g.Offset == nil || uint64(*g.Offset) != pos {
			continue
		}
		g.Severity = Critical
		g.Rule = "C3"
		g.Recovery = Refused
		g.Title = "Damaged frame header with committed frames after it"
		g.Simple = fmt.Sprintf("This frame's header is damaged, but an intact frame (sequence %d) follows it at offset %d. An interrupted write can only damage the last frame, so this is damage in the middle of the log. Cutting the file here would lose acknowledged data, so NEXUS refuses to open it and leaves the file untouched.", seq, at)
		g.Technical += fmt.Sprintf("; rule C3: complete valid frame seq %d at offset %d inside the %d bytes after the damaged header; open refuses (ErrCorruption) without truncating", seq, at, size-pos)
		g.DataImpact = fmt.Sprintf("No data is lost by opening: open refuses and changes nothing. The %d bytes after the damaged header include at least one committed frame (seq %d) that is not readable until the file is restored.", size-pos, seq)
		g.Action = "Keep a byte-for-byte backup and restore from a known-good backup, or keep the file for expert recovery. X-RAY repair refuses this case."
		g.Expected = g.Expected + "; no intact frame after a torn write"
		g.Actual = g.Actual + fmt.Sprintf("; intact frame seq %d at offset %d", seq, at)
		x.w.StopRule = "C3"
		x.w.LaterFrameOffset, x.w.LaterFrameSeq = i64(int64(at)), u64(seq)
		return
	}
}
