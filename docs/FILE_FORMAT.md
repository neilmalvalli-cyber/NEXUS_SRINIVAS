# File format (version 1)

Implemented in [`src/internal/format`](../src/internal/format). All integers are
**little-endian**, encoded explicitly. All checksums are **CRC32C (Castagnoli)**.

```
offset 0          32                    snapshot_end                 EOF
+-----------------+---------------------+----------------------------+
| SUPERBLOCK      | snapshot frames     | appended frames ...        |
+-----------------+---------------------+----------------------------+
FRAME   = FRAME HEADER (32 B) + PAYLOAD (payload_len B)
PAYLOAD = ENTRY_1 ... ENTRY_op_count
ENTRY   = ENTRY HEADER (11 B) + key + value
```

## Superblock (32 bytes)

| Off | Size | Field | Rule |
|---|---|---|---|
| 0 | 8 | magic | ASCII `KIPDBLOG` |
| 8 | 2 | version | `1`, anything else → `ErrUnsupportedVersion` |
| 10 | 2 | flags | `0` |
| 12 | 8 | first_seq | ≥ 1 |
| 20 | 8 | snapshot_end | 32 ≤ v ≤ 2^63−1 and v ≤ file size; on a frame boundary (checked by recovery) |
| 28 | 4 | sb_crc | CRC32C of bytes 0–27 |

Decode order: length → magic → CRC → version → flags → fields. The
superblock is never rewritten in place, and in a compaction file it is written
last, so *valid superblock ⇔ file complete*.

## Frame header (32 bytes)

| Off | Size | Field | Rule |
|---|---|---|---|
| 0 | 4 | magic | `0xD8C3B1A5` (bytes `A5 B1 C3 D8`) |
| 4 | 1 | type | `1` (COMMIT_FRAME) |
| 5 | 1 | reserved | `0` |
| 6 | 2 | op_count | 1 ≤ n ≤ payload_len / 12 |
| 8 | 8 | seq | ≥ 1; first frame = first_seq, then +1 (checked by recovery) |
| 16 | 8 | payload_len | 12 ≤ len ≤ 2^40 |
| 24 | 4 | payload_crc | CRC32C of the payload |
| 28 | 4 | header_crc | CRC32C of bytes 0–27 |

## Entry (11-byte header + key + value)

| Off | Size | Field | Rule |
|---|---|---|---|
| 0 | 1 | op | 1 = PUT, 2 = DEL |
| 1 | 2 | key_len | 1 – 1024 |
| 3 | 4 | val_len | 0 – 1,048,576; 0 for DEL |
| 7 | 4 | entry_crc | CRC32C of bytes 0–6 ‖ key ‖ value |

## Limits

| Constant | Value |
|---|---|
| `MaxKeyLen` | 1024 |
| `MaxValueLen` | 1,048,576 ([ASSUME] "1 MB" = 1 MiB) |
| `MaxBatchOps` | 10,000 (user batches; enforced by the write path) |
| `MaxFrameOps` | 65,535 (u16; compaction frames) |
| `MaxPayload` | 2^40 (fixed for v1; may never decrease) |

There is **no 64 MiB batch cap**: a maximal batch (10,000 × 1,049,611 B ≈ 10.5 GB)
is accepted by the size computation and streamed without copying.

## Error classes

| Class | Errors | Meaning for recovery |
|---|---|---|
| Damaged | `ErrBadMagic`, `ErrHeaderChecksum`, `ErrPayloadChecksum`, `ErrTruncated` | Looks like a torn write. May truncate only under the tail rules C1/C2/C4/C5 (DESIGN §8.2): outside the snapshot and provably the final frame. |
| Corrupt | `ErrMalformed` (incl. entry-CRC failure inside a CRC-valid payload), `ErrUnsupportedVersion`, a bare `ErrEntryChecksum` (Get path) | A torn write cannot produce this. Refuse; never truncate. |
| I/O | anything from the reader | Fail without modifying the file. Never treated as EOF. |
| Usage | `ErrInvalid` (encoder asked to write out-of-range values), `ErrShortBuffer` | Caller bug; says nothing about the file; nothing was written. |

How these map to the public `ErrBadFormat` / `ErrCorruption` is defined in
DESIGN §13.3.

`ScanPayload` keeps reading after a structural problem so it can finish the
payload CRC: a structural failure with a bad CRC is Damaged, and with a good CRC
it is Corrupt.

## Overflow-safety rules

* Positions are `uint64`; `HeaderFits` / `PayloadFits` subtract, never add.
* `payload_len ≤ MaxPayload` is checked before anything derived from it.
* `op_count` is bounded by `payload_len / 12` (division) before any loop.
* Per entry: `remaining ≥ 11` first, then `key_len + val_len ≤ remaining − 11`
  computed in `uint64` (max 65,535 + 2^32−1, no overflow).
* Encoding sums sizes in `uint64` and checks `≤ MaxPayload` after every addition.
* `NextSeq(MaxUint64)` is an error (no wraparound).
* 64-bit targets only: the build fails on 32-bit (`src/internal/format/arch.go`).
