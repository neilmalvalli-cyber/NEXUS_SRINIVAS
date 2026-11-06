package format

// Sizes of the fixed-width structures. They are part of format version 1 and
// can never change without bumping FormatVersion.
const (
	SuperblockSize  = 32
	FrameHeaderSize = 32
	EntryHeaderSize = 11
)

// Limits on user data.
const (
	// MinKeyLen and MaxKeyLen bound key_len (inclusive).
	MinKeyLen = 1
	MaxKeyLen = 1024

	// MaxValueLen bounds val_len (inclusive). We accept exactly 1 MiB.
	// [ASSUME] the challenge's "1 MB" means 1,048,576 bytes.
	MaxValueLen = 1 << 20

	// MinEntrySize is the smallest possible encoded entry: an 11-byte header
	// plus a 1-byte key and an empty value. It is used to bound op_count by
	// payload_len before any loop runs.
	MinEntrySize = EntryHeaderSize + MinKeyLen

	// MaxEntrySize is the largest possible encoded entry.
	MaxEntrySize = EntryHeaderSize + MaxKeyLen + MaxValueLen

	// MaxBatchOps is the largest user batch [REQ §2]. It is enforced by the
	// write path, not by the frame decoder, because compaction frames may be
	// larger.
	MaxBatchOps = 10000

	// MaxFrameOps is the largest op_count a frame header can carry (u16).
	MaxFrameOps = 65535
)

// MaxPayload is the largest payload_len accepted by encoder and decoder.
//
// 2^40 bytes covers the worst-case user batch (10,000 x MaxEntrySize ≈ 10.5 GB)
// and the worst-case compaction frame (65,535 x MaxEntrySize ≈ 68.8 GB) with a
// wide margin, while still rejecting hostile lengths before any allocation or
// read. It is fixed for format version 1 and may never decrease, otherwise an
// older valid file would become unreadable.
const MaxPayload uint64 = 1 << 40

// MaxFileOffset is the largest file position the engine can address. Go file
// APIs take int64 offsets, so any on-disk u64 position beyond this is hostile.
const MaxFileOffset uint64 = 1<<63 - 1

// Magic numbers and version.
const (
	// FrameMagic opens every frame header. It is non-zero and non-ASCII so a
	// zero-filled or text-filled tail can never be mistaken for a frame.
	FrameMagic uint32 = 0xD8C3B1A5

	// FormatVersion is the only superblock version this code reads or writes.
	FormatVersion uint16 = 1

	// FrameTypeCommit is the only defined frame type.
	FrameTypeCommit uint8 = 1
)

// superblockMagic is the ASCII magic at offset 0 of every data file.
var superblockMagic = [8]byte{'K', 'I', 'P', 'D', 'B', 'L', 'O', 'G'}

// SuperblockMagic returns a copy of the 8-byte superblock magic ("KIPDBLOG").
func SuperblockMagic() [8]byte { return superblockMagic }

// Compile-time check that the payload cap is large enough for the biggest
// frame we will ever write. If someone lowers MaxPayload or raises the entry
// limits, this constant expression underflows and the build fails.
const _ = MaxPayload - uint64(MaxFrameOps)*uint64(MaxEntrySize)
