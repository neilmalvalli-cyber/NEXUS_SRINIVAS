# Test plan

Status legend: **✅ implemented and passing** · **📋 specified, to be implemented in the named phase**.

## Phase 1 — format and platform (✅ implemented)

Package `src/internal/format`:

| Area | Tests |
|---|---|
| CRC32C | known vectors ("123456789" → `E3069283`, RFC 3720 B.4 vectors); streaming == one-shot at every split |
| Superblock | exact byte layout (magic, LE fields, CRC); round trip incl. max values; short/zero/bad magic/bad CRC; version 0 and 2; flags ≠ 0; first_seq 0; snapshot_end 0, 31, > 2^63−1, MaxUint64; snapshot_end vs file size; every single-bit flip rejected; encoder refuses invalid values and appends nothing |
| Frame header | exact byte layout; round trip incl. seq MaxUint64, payload 2^40, op_count 65,535; bad magic/CRC; type 0/2/255; reserved ≠ 0; op_count 0 and > payload/12 (exact boundary tested); seq 0; payload_len 0, 11, 2^40+1, MaxUint64; every bit flip rejected; `HeaderFits`/`PayloadFits` with positions near 2^64; `NextSeq` wraparound |
| Entry | exact layout + CRC coverage; round trip incl. 1024-byte key and 1 MiB value; key 0/1025/65,535/65,536; value 1 MiB+1 / MaxUint32; DEL with value; op 0/3; `CheckEntryFits` boundaries and max-field overflow; every bit flip rejected; length mismatch; GET key-mismatch defence |
| Streaming | encode/decode round trip incl. direct-write path; offsets decode standalone; validation writes nothing; max batch (10,000 × max entry) accepted with no arbitrary cap; writer failure reports exact byte count; short write detected; **every torn prefix is Damaged**; zero-filled tail is Damaged; CRC-valid malformed payloads are Corrupt; malformed + bad CRC is Damaged; reader errors stay IO and are never EOF; entries are tentative until CRC verifies |
| Classification | `TestClassifyTable` (every sentinel → class, incl. bare `ErrEntryChecksum` → Corrupt, `ErrInvalid`/`ErrShortBuffer` → Usage); `TestDecodeEntryErrorClasses` (GET-path errors are Damaged/Corrupt only); `TestTornFrameContainingEmbeddedFrameHeader` (a value full of valid-looking frame headers does not change how a torn frame is classified: supports rule C3) |
| Fuzz | `FuzzDecodeSuperblock`, `FuzzDecodeFrameHeader`, `FuzzDecodeEntry`, `FuzzScanFrame`: no panics, every error classified, anything accepted re-encodes byte-for-byte |

Package `src/internal/platform`:

| Test | Proves |
|---|---|
| `TestMoveNoReplaceMovesWhenDestinationAbsent` | move works; source removed |
| `TestMoveNoReplaceRefusesExistingDestination` | existing destination is never replaced; both files unchanged; `fs.ErrExist` |
| `TestMoveNoReplaceMissingSource` | `fs.ErrNotExist`; no destination created |
| `TestMoveNoReplaceTwoRenameSequence` | R1 + R2 leave the expected names; R1 over a stale `.old` fails (why §11.2 cleans first) |
| `TestErrorClassificationWindows` | codes 112/39 → disk full, 32/33 → sharing violation, others neither; works through `*os.PathError` and `%w` |
| `TestOsRenameReplacesOnWindows` | pins the hazard that forbids `os.Rename` |
| `TestExclusiveShareModeLock` | share-mode-0 lock: second open fails (same process, `os.OpenFile` too); released on close; stale file re-lockable |
| `TestOpenFileBlocksMove` | an open Go file handle blocks `MoveFile` (why the handle is closed before R1) |

Package `src/kvdb`: op/batch validation and error mapping, 10,000-op boundary,
all error values distinct (incl. `ErrBadFormat`).

## Specifications for the 2026-11-06 design corrections (📋)

All of these need the Phase 2 `fileOps` fault-injection layer and a recording
wrapper that logs every file operation in order.

### Locking (Phase 2)

| ID | Scenario | Expected |
|---|---|---|
| T-LOCK-1 | Open with the recorder | The first recorded file operation is the `.lock` open; no stat/read/rename/delete/truncate/create of `path`, `.compact`, `.old`, `.new` happens before it. |
| T-LOCK-2 | Lock held by another handle; files in an interrupted-compaction state (`path` missing, `.compact` sealed, `.old` valid) | Open returns `ErrLocked`; the directory listing and every file's bytes are unchanged. |
| T-LOCK-3 | Two `Open` calls in one process | Second returns `ErrLocked`; after `Close` of the first, Open succeeds. |
| T-LOCK-4 | Child process holds the lock and is killed with `TerminateProcess` | Parent Open succeeds right after `Wait()`. |
| T-LOCK-5 | A third-party handle opens `.lock` briefly (shorter than the retry budget) | Open succeeds; with a handle held longer than the budget, `ErrLocked`. |
| T-LOCK-6 | `Close` | `.lock` still exists afterwards (never deleted). |

### Invariant I1 (Phase 2)

| ID | Scenario | Expected |
|---|---|---|
| T-I1-1 | Inject a write failure mid-frame, rollback succeeds | Next commit writes at the old end; file = previous bytes + new frame exactly. |
| T-I1-2 | Inject a write failure, then make the truncate fail | Handle is FAILED; every later Put/Delete/Batch/Compact returns `ErrFailed` without any recorded write. |
| T-I1-3 | Inject a Sync failure | `ErrOutcomeUnknown`, handle FAILED, no retry of Sync, no further writes. |
| T-I1-4 | Concurrent writers (≥ 2 goroutines, recorder) | Recorded writes never interleave between two frames; each frame's writes are followed by its Sync before the next frame's first write. |

### Recovery tail rules (Phase 2)

Construct files byte by byte with the Phase 1 encoder, then Open.

| ID | Rule | File | Expected |
|---|---|---|---|
| T-REC-1 | C1 | valid frames + 1…31 trailing bytes | truncated to the last frame end; Sync recorded after truncate |
| T-REC-2 | C2 | valid frames + header with bad magic / bad header CRC (+ arbitrary bytes after) | truncated to the damaged header start |
| T-REC-3 | C3 | valid frames + damaged header + a perfectly valid next frame after it | still truncated at the damaged header (no forward scan); documented accepted risk |
| T-REC-4 | C3 | a frame whose value contains valid frame headers with seq = next, cut at every byte | always opens; truncated to the frame start |
| T-REC-5 | C5 | last frame with valid header, payload ends exactly at EOF, one payload byte flipped / zero-filled | truncated to that frame's start |
| T-REC-6 | C6 | same frame but with ≥ 1 extra byte (or another valid frame) after its declared end | `ErrCorruption`; file bytes unchanged |
| T-REC-7 | C4 | valid header, `payload_len` beyond EOF | truncated |
| T-REC-8 | C7 | CRC-valid frame with wrong seq; CRC-valid malformed payload | `ErrCorruption`; file unchanged |
| T-REC-9 | C0 | any damage at an offset < `snapshot_end` (incl. a C5-shaped frame inside the snapshot) | `ErrCorruption`; file unchanged; never truncated below `snapshot_end` |
| T-REC-10 | C8 | reader error injected at every read position | Open fails with the injected error (not `ErrCorruption`, not success); zero truncate/write calls recorded |
| T-REC-11 | all | kill-point sweep: cut the last frame at every byte offset | Open succeeds; index = state before that frame |
| T-REC-12 | repeatability | crash injected during truncation; Open again | same index both times |

### Authority table (Phase 2 for A1, A2, A5, A7; Phase 3 for A3, A4, A6)

"V" = valid/sealed, "U" = unsealed/invalid, "–" = absent. Columns: `path`, `.compact`, `.old`, `.new`.

| ID | path | .compact | .old | .new | Expected |
|---|---|---|---|---|---|
| T-AUTH-1 | V | – | – | – | use `path` |
| T-AUTH-2 | V | V | V | V | use `path`; all three temps deleted |
| T-AUTH-3 | U | V | V | – | `ErrBadFormat`; nothing changed |
| T-AUTH-4 | zero-length | – | – | – | `ErrBadFormat`; nothing changed |
| T-AUTH-5 | – | V | V | – | `.compact` adopted; `.old` deleted; contents = compacted state |
| T-AUTH-6 | – | U | V | – | `.old` restored; `.compact` deleted |
| T-AUTH-7 | – | – | V | – | `.old` restored |
| T-AUTH-8 | – | U | – | V | `.new` adopted; `.compact` deleted |
| T-AUTH-9 | – | – | U | – | `ErrBadFormat` naming `.old`; nothing changed |
| T-AUTH-10 | – | U | – | U | unsealed temps deleted; fresh database created |
| T-AUTH-11 | every state above | | | | kill injected before/after every move and delete; re-running Open reaches the same final state and contents |
| T-AUTH-12 | any | | | | a read error while checking any file stops Open with that error and changes nothing |

### Error meanings (Phase 2)

| ID | Scenario | Expected |
|---|---|---|
| T-ERR-1 | `path` = random bytes / zero-length / superblock with version 2 | `ErrBadFormat`; not `ErrCorruption`; file unchanged |
| T-ERR-2 | valid superblock, `snapshot_end` > size; C0/C6/C7 files | `ErrCorruption`; not `ErrBadFormat`; file unchanged |
| T-ERR-3 | Get after flipping a byte of an indexed value on disk | `ErrCorruption`; handle still OPEN; other keys still readable |
| T-ERR-4 | read error during Open | neither `ErrBadFormat` nor `ErrCorruption`; wraps the OS error |

## Later phases (planned)

* Storage: commit path with fault-injecting `fileOps` (fail at every write / sync / truncate).
* Compaction: kill at every step of the R1/R2 protocol; R1/R2 failure cleanup rules (§11.3).
* Admission: max_bytes boundary and overflow tables.
* Crash harness: child process killed with `TerminateProcess` at random points, oracle checks invariant rules 1–5.
* Concurrency: ≥ 2 writers + readers under `-race`, no half-batch visible.
* Performance: open with 1,000,000 keys < 2 s.
