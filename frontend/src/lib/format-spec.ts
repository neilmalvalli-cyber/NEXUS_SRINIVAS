// The NEXUS on-disk format, version 1, copied from the comments and constants
// in src/internal/format (superblock.go, frame.go, entry.go, constants.go).
// All integers are little-endian. If the Go source changes, this must too.

export interface Field {
  offset: number;
  size: number;
  name: string;
  value: string;
  detail: string;
}

export interface Structure {
  id: string;
  title: string;
  size: string;
  source: string;
  summary: string;
  fields: Field[];
}

export const FORMAT: Structure[] = [
  {
    id: "superblock",
    title: "Superblock",
    size: "32 bytes, at offset 0",
    source: "src/internal/format/superblock.go",
    summary:
      "Identifies the file and says where the log starts. It is never rewritten in place: it is written once into a temporary file (.new or .compact) that is then renamed into place. In a compaction file it is written last, so a valid superblock means the file is complete.",
    fields: [
      { offset: 0, size: 8, name: "magic", value: "\"KIPDBLOG\"", detail: "ASCII magic. A file that does not start with it is not a NEXUS database." },
      { offset: 8, size: 2, name: "version", value: "1", detail: "FormatVersion. Any other version is refused (Corrupt class), so an older binary never overwrites a newer file." },
      { offset: 10, size: 2, name: "flags", value: "0", detail: "Reserved; must be 0 in version 1." },
      { offset: 12, size: 8, name: "first_seq", value: "≥ 1", detail: "Sequence number of the first frame in this file. A new database starts at 1; a compacted file starts where the snapshot left off." },
      { offset: 20, size: 8, name: "snapshot_end", value: "≥ 32", detail: "End of the compaction snapshot. Recovery never truncates below it: everything before it was sealed by this superblock and cannot be a torn tail." },
      { offset: 28, size: 4, name: "sb_crc", value: "CRC32C", detail: "Castagnoli CRC of bytes 0..27." },
    ],
  },
  {
    id: "frame",
    title: "Frame header",
    size: "32 bytes, then payload_len bytes of entries",
    source: "src/internal/format/frame.go",
    summary:
      "Every PUT, DELETE or BATCH is exactly one frame. A frame counts only if its header CRC and its payload CRC both match, which is what makes a batch all-or-nothing: a torn frame fails a checksum and none of its entries are applied.",
    fields: [
      { offset: 0, size: 4, name: "magic", value: "0xD8C3B1A5", detail: "FrameMagic. Non-zero and non-ASCII, so a zero-filled or text-filled tail can never look like a frame." },
      { offset: 4, size: 1, name: "type", value: "1", detail: "FrameTypeCommit, the only frame type." },
      { offset: 5, size: 1, name: "reserved", value: "0", detail: "Must be 0." },
      { offset: 6, size: 2, name: "op_count", value: "1 … payload_len / 12", detail: "Number of entries. User batches are at most 10,000; compaction frames up to 65,535." },
      { offset: 8, size: 8, name: "seq", value: "previous + 1", detail: "Sequence number. The first frame equals the superblock's first_seq and each later frame is exactly one more, so a missing or replayed frame is detected." },
      { offset: 16, size: 8, name: "payload_len", value: "12 … 2^40", detail: "Length of the entries that follow. It is covered by header_crc, so a damaged length can never send the scanner reading gigabytes of garbage." },
      { offset: 24, size: 4, name: "payload_crc", value: "CRC32C", detail: "CRC of the whole payload." },
      { offset: 28, size: 4, name: "header_crc", value: "CRC32C", detail: "CRC of header bytes 0..27, checked before any other field is trusted." },
    ],
  },
  {
    id: "entry",
    title: "Entry",
    size: "11-byte header, then key, then value",
    source: "src/internal/format/entry.go",
    summary:
      "One operation inside a frame's payload. The index points at an entry's offset, and GET verifies entry_crc on every read so a damaged byte is reported as an error, never returned as a value.",
    fields: [
      { offset: 0, size: 1, name: "op", value: "1 = PUT, 2 = DEL", detail: "The operation." },
      { offset: 1, size: 2, name: "key_len", value: "1 … 1024", detail: "Key length in bytes (MinKeyLen 1, MaxKeyLen 1024)." },
      { offset: 3, size: 4, name: "val_len", value: "0 … 1,048,576", detail: "Value length; exactly 1 MiB is the maximum, and it must be 0 for DEL." },
      { offset: 7, size: 4, name: "entry_crc", value: "CRC32C", detail: "CRC of (op, key_len, val_len, key, value) in that order." },
      { offset: 11, size: -1, name: "key", value: "key_len bytes", detail: "The key bytes." },
      { offset: -1, size: -1, name: "value", value: "val_len bytes", detail: "The value bytes (absent for DEL)." },
    ],
  },
];

/** Frame size for one PUT: 32 + 11 + key + value. */
export function putFrameSize(keyBytes: number, valueBytes: number) {
  return 32 + 11 + keyBytes + valueBytes;
}

export const WRITE_PATH = [
  { title: "Client sends PUT", detail: "nexus-api receives the request and validates the key (1–1024 bytes) and value (≤ 1 MiB)." },
  { title: "Acquire the write lock", detail: "After validating and sizing the entries, the engine takes its single RWMutex exclusively and holds it through the write, the fsync and the index update, so commits are strictly one after another (src/internal/storage/log.go, Commit). The OS lock file <path>.lock keeps other processes out." },
  { title: "Encode one frame", detail: "The operation becomes a 32-byte frame header plus an 11-byte entry header, the key and the value. Admission checks the size limit first." },
  { title: "Append at the end of the log", detail: "WriteAt at the current end of the file. Bytes reach the file strictly in order, so a crash leaves at most a prefix of this one frame." },
  { title: "Checksums", detail: "entry_crc, payload_crc and header_crc (CRC32C) are computed while encoding; recovery and GET verify them later." },
  { title: "fsync the log", detail: "FlushFileBuffers on Windows, fsync elsewhere. Only after this returns is the frame durable." },
  { title: "Update the in-memory index", detail: "key → {entry offset, length}. A failed write never reaches this step; it is truncated away instead." },
  { title: "Acknowledge", detail: "Success is returned only now, after the durable write. A crash before this point may lose the write, but then it was never acknowledged." },
];

export const RECOVERY_STEPS = [
  { title: "Resolve leftover files", detail: "The authority table decides which of <path>, .new, .compact and .old is authoritative after an interrupted create or compaction rename (src/internal/recovery/authority.go)." },
  { title: "Read and validate the superblock", detail: "Magic \"KIPDBLOG\", version 1, flags 0 and the superblock CRC. A bad superblock is refused, not repaired." },
  { title: "Scan the log sequentially", detail: "From offset 32, one frame at a time, streaming (src/internal/recovery/scan.go)." },
  { title: "Validate each frame", detail: "Frame magic and header CRC first, then field bounds, then that the payload fits in the file." },
  { title: "Verify checksums and sequence", detail: "Payload CRC and every entry CRC; seq must be exactly previous + 1." },
  { title: "Replay valid operations", detail: "A frame's entries are buffered and applied only after its payload CRC passes: a batch is all or nothing." },
  { title: "Rebuild the index", detail: "key → latest entry offset, with live key and byte counters." },
  { title: "Handle the tail", detail: "Damage that is provably the last frame (an incomplete write) is truncated and flushed. Damage followed by more data, or a checksum-valid but malformed frame, is corruption: open refuses and changes nothing." },
];
