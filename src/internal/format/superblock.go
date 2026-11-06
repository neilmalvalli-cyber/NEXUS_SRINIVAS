package format

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Superblock layout (32 bytes, little-endian):
//
//	0   8  magic         "KIPDBLOG"
//	8   2  version       1
//	10  2  flags         0
//	12  8  first_seq     sequence number of the first frame in this file (>= 1)
//	20  8  snapshot_end  end of the compaction snapshot (>= 32)
//	28  4  sb_crc        CRC32C of bytes 0..27
//
// Storage invariants that depend on this structure:
//
//   - The superblock is never rewritten in place. It is written once into a
//     temporary file (.new or .compact) that is then renamed into place.
//   - In a compaction file it is written LAST, after the snapshot frames are
//     flushed, then flushed again. Until then offset 0 holds zeros, which
//     fail the magic check. Hence: valid superblock <=> temp file complete.
//   - Recovery never truncates below snapshot_end: everything before it was
//     sealed by the superblock and is not a torn tail.
const (
	sbOffMagic       = 0
	sbOffVersion     = 8
	sbOffFlags       = 10
	sbOffFirstSeq    = 12
	sbOffSnapshotEnd = 20
	sbOffCRC         = 28
)

// Superblock holds the variable fields of the superblock. Magic, version and
// flags are constants of format version 1.
type Superblock struct {
	FirstSeq    uint64
	SnapshotEnd uint64
}

// NewSuperblock returns the superblock of a brand-new, empty database.
func NewSuperblock() Superblock {
	return Superblock{FirstSeq: 1, SnapshotEnd: SuperblockSize}
}

// validate checks the field rules that do not depend on the file size.
func (s Superblock) validate(kind func(string, string, uint64, string) error) error {
	if s.FirstSeq == 0 {
		return kind("superblock", "first_seq", 0, "must be >= 1")
	}
	if s.SnapshotEnd < SuperblockSize {
		return kind("superblock", "snapshot_end", s.SnapshotEnd, "must be >= 32")
	}
	if s.SnapshotEnd > MaxFileOffset {
		return kind("superblock", "snapshot_end", s.SnapshotEnd, "exceeds maximum file offset")
	}
	return nil
}

// CheckFileSize verifies snapshot_end against the actual file size. A
// snapshot that ends beyond EOF means the sealed region is missing bytes,
// which a torn append can never cause (appends happen after snapshot_end),
// so it is reported as Malformed (Corrupt), not Damaged.
//
// Checking that snapshot_end lands exactly on a frame boundary requires
// scanning and is done by recovery.
func (s Superblock) CheckFileSize(size uint64) error {
	if s.SnapshotEnd > size {
		return malformed("superblock", "snapshot_end", s.SnapshotEnd,
			fmt.Sprintf("beyond end of file (size %d)", size))
	}
	return nil
}

// AppendTo validates s and appends its 32-byte encoding to dst.
func (s Superblock) AppendTo(dst []byte) ([]byte, error) {
	if err := s.validate(invalid); err != nil {
		return dst, err
	}
	var b [SuperblockSize]byte
	copy(b[sbOffMagic:], superblockMagic[:])
	binary.LittleEndian.PutUint16(b[sbOffVersion:], FormatVersion)
	binary.LittleEndian.PutUint16(b[sbOffFlags:], 0)
	binary.LittleEndian.PutUint64(b[sbOffFirstSeq:], s.FirstSeq)
	binary.LittleEndian.PutUint64(b[sbOffSnapshotEnd:], s.SnapshotEnd)
	binary.LittleEndian.PutUint32(b[sbOffCRC:], Checksum(b[:sbOffCRC]))
	return append(dst, b[:]...), nil
}

// Encode validates s and returns its 32-byte encoding.
func (s Superblock) Encode() ([SuperblockSize]byte, error) {
	var out [SuperblockSize]byte
	b, err := s.AppendTo(out[:0])
	if err != nil {
		return out, err
	}
	copy(out[:], b)
	return out, nil
}

// DecodeSuperblock parses and validates the first 32 bytes of b.
//
// Check order matters for classification: magic first (zeros = unsealed temp
// file), then CRC (damage), and only then the field rules, so that a field
// violation is reported only for bytes the CRC vouches for.
func DecodeSuperblock(b []byte) (Superblock, error) {
	if len(b) < SuperblockSize {
		return Superblock{}, fmt.Errorf("%w: superblock needs %d bytes, have %d",
			ErrTruncated, SuperblockSize, len(b))
	}
	b = b[:SuperblockSize]
	if !bytes.Equal(b[sbOffMagic:sbOffMagic+8], superblockMagic[:]) {
		return Superblock{}, fmt.Errorf("%w: superblock", ErrBadMagic)
	}
	want := binary.LittleEndian.Uint32(b[sbOffCRC:])
	if got := Checksum(b[:sbOffCRC]); got != want {
		return Superblock{}, fmt.Errorf("%w: superblock (stored %#08x, computed %#08x)",
			ErrHeaderChecksum, want, got)
	}
	if v := binary.LittleEndian.Uint16(b[sbOffVersion:]); v != FormatVersion {
		return Superblock{}, fmt.Errorf("%w: superblock version %d (want %d)",
			ErrUnsupportedVersion, v, FormatVersion)
	}
	if f := binary.LittleEndian.Uint16(b[sbOffFlags:]); f != 0 {
		return Superblock{}, malformed("superblock", "flags", uint64(f), "must be 0")
	}
	s := Superblock{
		FirstSeq:    binary.LittleEndian.Uint64(b[sbOffFirstSeq:]),
		SnapshotEnd: binary.LittleEndian.Uint64(b[sbOffSnapshotEnd:]),
	}
	if err := s.validate(malformed); err != nil {
		return Superblock{}, err
	}
	return s, nil
}
