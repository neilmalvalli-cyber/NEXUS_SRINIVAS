// Package format is the on-disk codec for the KIPDBLOG data file.
//
// It owns every byte-level decision: the 32-byte superblock, the 32-byte frame
// header, the 11-byte entry header, CRC32C checksums, all length limits and the
// overflow-safe arithmetic that guards them. It performs no file I/O of its own
// beyond streaming through io.Reader / io.Writer values supplied by the caller,
// so it can be tested exhaustively in memory and fuzzed.
//
// File layout (all integers little-endian, see docs/FILE_FORMAT.md):
//
//	offset 0          32                    snapshot_end                 EOF
//	+-----------------+---------------------+----------------------------+
//	| SUPERBLOCK      | snapshot frames     | appended frames ...        |
//	+-----------------+---------------------+----------------------------+
//
//	FRAME   = FRAME HEADER (32 B) + PAYLOAD (payload_len bytes)
//	PAYLOAD = ENTRY_1 ... ENTRY_op_count
//	ENTRY   = ENTRY HEADER (11 B) + key (key_len B) + value (val_len B)
//
// Atomicity comes from the format itself: a frame is one commit (a single put,
// a single delete or a whole batch) and it counts only if its header CRC, its
// payload CRC, every entry CRC and its full structure all verify. A process
// killed mid-write leaves a strict prefix of a frame, which can never pass the
// payload CRC, so it is discarded as a unit.
//
// Error classes. Decoders report three kinds of file-related failure, and
// recovery must treat them differently (see Classify and docs/DESIGN.md §8.2):
//
//   - Damaged: bad magic, header/payload checksum mismatch, or input that ends
//     early. This is what a torn (killed) write looks like. Recovery may
//     truncate such a frame only if it is outside the snapshot and provably
//     the final frame of the log (invariant I1).
//   - Corrupt: every checksum passes but the structure is invalid, the
//     version is unknown, or (on the Get path) an entry checksum fails. A torn
//     write cannot produce this; recovery must refuse rather than truncate.
//   - I/O: any error coming from the underlying reader. It is returned
//     unchanged (wrapped) and must never be mistaken for end-of-file.
//
// A fourth class, Usage, covers caller mistakes (ErrInvalid, ErrShortBuffer)
// and says nothing about the file.
package format
