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

## Phase 2 Stage 1 — file abstraction, lock, creation (✅ implemented)

| Package | Tests | Covers |
|---|---|---|
| `fileio` | `TestOSCreateExclusiveRefusesExisting`, `TestOSFileOperations`, `TestReadFullAtDistinguishesEOFFromIOError`, `TestWriteFullAtDetectsShortWrites`, `TestRetry`, `TestDefaultRetrySchedule`, `TestFaultFSModesAndTrace`, `TestFaultFSSyncTruncateCloseFaults` | full reads/writes; EOF vs real I/O errors; short writes with a nil error; retry schedule (15 attempts, 14 sleeps, 1 s); faultfs modes and trace; faulted Close still releases the handle |
| `platform` | `TestLockFileExclusiveAndReleasable`, `TestIsLockBusyRejectsOtherErrors` | exclusive lock, busy classification, lock file kept |
| `locking` | `TestAcquireReleaseKeepsLockFile` (T-LOCK-6), `TestSecondAcquireInSameProcessIsErrLocked` (T-LOCK-3), `TestBriefThirdPartyHandleIsAbsorbed` (T-LOCK-5), `TestNonBusyErrorIsNotErrLocked`, `TestAcquireTouchesOnlyTheLockFile`, `TestCheckRejectsNilReleasedAndForeignLocks`, `TestReleaseErrorStillReleases`, `TestLockContentionAcrossProcessesAndKill` (T-LOCK-4, child process killed), `TestLockReleasedByOrderlyChildExit` | lock contention within and across processes |
| `recovery` | `TestCreateThroughNew` (T-LOCK-1 + creation order), `TestReopenDoesNotRecreate`, `TestResolveRequiresLock`, `TestLockedOpenChangesNothing` (T-LOCK-2), `TestAuthorityTable` (T-AUTH-1…10, interrupted creation: empty / partial / sealed `.new`, D1, D3, non-regular files), `TestResolveDefersStaleCleanup` (D2), `TestCheckErrorsStopOpen` (T-AUTH-12), `TestShortSuperblockReadIsNotBadFormat`, `TestSharingViolationsAreRetried`, `TestRestartCap`, `TestFaultSweep` (fail-before / fail-after / short write at every operation), `TestCrashSweep` (T-AUTH-11: kill before/after/mid-write at every operation; re-run reaches the same state) | authority table and safe creation |

T-AUTH-11 is now covered for every state the table *resolves*. Kill points inside compaction itself (R1/R2/R3) are still Phase 3.

## Phase 2 Stage 2 — index, recovery scanner, tail rules (✅ implemented)

Files are built byte by byte with the Phase 1 encoder (`recovery/scan_test.go`).

| ID | Test | Result |
|---|---|---|
| — | `index.TestIndexPutGetDeleteAndCounters`, `TestIndexGetDoesNotAllocate` | ✅ |
| — | `TestOpenRebuildsIndex` (batches, overwrite, delete, live/dead bytes, clean Open changes nothing), `TestOpenFreshDatabase`, `TestStateCloseReleasesEverything` | ✅ |
| T-REC-1 | `TestTailC1ShortTrailer` (1…31 bytes, zeros and non-zeros; Sync after the truncate; no writes) | ✅ |
| T-REC-2 | `TestTailC2DamagedHeader` (bad magic, bad header CRC, zero fill, junk after) | ✅ |
| T-REC-3 | `TestTailC3NoForwardScan` | ✅ |
| T-REC-4, T-REC-11 | `TestTailKillPointSweep` (cut at every byte of a batch whose value contains valid frame headers with the expected seq; torn batch never partially applied; second Open clean) | ✅ |
| T-REC-5 | `TestTailC5PayloadCRCAtEOF` (each payload byte flipped; zero-filled suffixes) | ✅ |
| T-REC-6 | `TestTailC6BytesAfterBadPayload` | ✅ |
| T-REC-7 | `TestTailC4PayloadPastEOF` (incl. payload_len = 2^40) | ✅ |
| T-REC-8 | `TestTailC7CRCValidButWrong` (seq skip/repeat, first_seq mismatch, CRC-valid malformed payload, CRC-valid malformed header) | ✅ |
| T-REC-9 | `TestTailC0InsideSnapshot` (C5-shaped and header damage inside the snapshot, boundary mismatch; truncation at exactly snapshot_end allowed) | ✅ |
| T-REC-10, T-ERR-4 | `TestReadErrorsNeverTruncate` (error and short read at every read position, small and 5 MiB logs) | ✅ |
| T-REC-12 | `TestOpenCrashSweep` (kill before/after every Open operation incl. truncate and sync), `TestOpenFaultSweep` | ✅ |
| T-ERR-1, T-ERR-2 | `TestErrorMeanings`, plus every refusal test asserts the other error is not also matched | ✅ |
| — | `TestStaleTempsSurviveScanRefusal` (D2 end to end), `TestSequenceExhaustion` (E4) | ✅ |
| §8.4 | `TestOpenMillionKeysTiming` (opt-in, `KVDB_PERF=1`): 1,000,000 keys, 37 MB, **0.44 s**, about 195 MB heap | ✅ |

T-ERR-3 and T-I1-1…4 are covered in Stage 3 below.

## Phase 2 Stage 3 — write path, Get, public API (✅ implemented)

| ID | Test | Covers |
|---|---|---|
| — | `storage.TestCommitGetDeleteAndReopen`, `TestGetReturnsIndependentCopyAndEmptyValue`, `TestNoIOForNoOps`, `TestValidationBeforeIO`, `TestCommitWritesThenSyncs` | semantics; Delete of a missing key and empty batch do no I/O; validation before I/O; writes strictly sequential at the old end, then exactly one Sync |
| T-I1-1 | `TestI1DiskFullRollbackKeepsHandleOpen` (failure at the 1st/2nd/3rd WriteAt of a multi-write frame) | definite `ErrFull`, handle OPEN, file = previous bytes + next frame exactly |
| T-I1-2 | `TestI1WriteFailuresFailTheHandle` (I/O error, short write with nil error, failed truncate, failed rollback Sync) | `ErrOutcomeUnknown`, FAILED, every later call `ErrFailed` with no I/O, Close works, reopen all or nothing |
| T-I1-3 | `TestI1SyncFailureIsNotRetried` (Sync fails before / after taking effect) | data-file ops are exactly `write sync truncate`; FAILED |
| T-I1-4 | `TestI1ConcurrentWritersDoNotInterleave` (4 writers × 40 commits, some multi-write frames) | writes contiguous per frame, one Sync per frame, nothing interleaved |
| — | `TestConcurrentReadersSeeWholeValues` | a Get during batches sees a whole old or new value |
| T-ERR-3 | `TestGetCorruptionKeepsHandleOpen` | flipped byte → `ErrCorruption`, other keys readable, still writable; short read → `ErrCorruption`; OS read error → wrapped, not corruption |
| — | `TestClosedHandle`, `TestSequenceExhaustionIsErrFull` | lifecycle; F4 |
| — | `TestCommitFaultSweep` (every commit op × fail-before/after/short write/disk full) | **nil → batch present after reopen; `ErrFull` → absent; `ErrOutcomeUnknown` → all or nothing** |
| — | `TestCommitCrashSweep` (kill before / mid-write / after every commit op) | prior state intact, batch all or nothing; both outcomes reached |
| — | `kvdb.TestOpenValidatesArguments`, `TestAPIRoundTripAndPersistence`, `TestAPIValidationErrors`, `TestMaxBatchOfDistinctKeys`, `TestLockedAndClosed`, `TestBadFormatFileIsUntouched` | public API, last-wins batches, 1 MiB value / 1024-byte key, 10,000-op batch |
| — | `kvdb.TestKillDuringWritesLosesNothingAcknowledged` (child process writes Puts or 51-op Batches and prints ACK after each success; parent kills it with `TerminateProcess`; 3 rounds each) | **every ACKed write present and correct; the in-flight one all or nothing** |
| F1 | `storage.TestF1SyncFailureInvariants` (Sync fails before/after taking effect × rollback truncate succeeds/fails) | the five approved F1 invariants, both reopen outcomes reached |

## Phase 3 — max_bytes admission and crash-safe compaction (✅ implemented)

| Area | Test | Covers |
|---|---|---|
| Admission arithmetic | `admission.TestFitsNeverOverflows`, `TestDecide`, `TestTriggers` | no overflow/underflow at 2^64 limits; every decision branch; snapshot size computed only when needed; 4 MiB / 2× trigger |
| Plain compaction | `storage.TestCompactPreservesLiveStateAndShrinks`, `TestCompactIsNoOpWhenNothingToReclaim`, `TestCompactAfterDeletingEverything`, `TestCompactSplitsFrames` | contents and live counters preserved, file shrinks, `first_seq` = old `next_seq` (§9.1), numbering continues, no temp files left; no-op cases do no I/O / no rewrite; empty snapshot (32 bytes); frame splitting by payload |
| Automatic trigger | `TestAutoCompactTrigger` | 12 MiB of overwrites keep the file < 9 MiB; nothing below 4 MiB |
| max_bytes policy | `TestAdmissionCompactingCommitAndFull`, `TestAdmissionOverCapDatabase`, `TestAdmissionRandomWorkloadMatchesModel` (600 random commits, cap 16 KiB, checked against a model), `kvdb.TestMaxBytesThroughAPI` | appends then compacting commits stay ≤ cap; oversized → `ErrFull` with no I/O and no change; rule 1 over-cap open; rule 2 PUT refused; rule 3 deletes shrink an over-cap file; A8 net-empty commit no I/O; rejected batch never partially applied |
| Failure handling (§11.3) | `TestCompactDiskFullAndCorruptionChangeNothing`, `TestCompactRetriesSharingViolations`, `TestCompactingCommitFailedRestoreIsUnknown` | disk-full / corrupt live value → definite, file unchanged, `.compact` removed; antivirus-style violations on moves absorbed; R2 + restore failure → `ErrOutcomeUnknown` (never definite), FAILED, next Open adopts `.compact` (A3) |
| Kill points (§11.5, T-AUTH-11) | `TestCompactCrashSweep` (plain and compacting commit; kill before / mid-write / after every operation, including R1, R2, R3, the seal and the stale-file deletes) | reopen always succeeds; plain compaction never changes contents; compacting commit all or nothing (both reached); no temp file survives the next Open |
| Fault sweep | `TestCompactFaultSweep` (every operation fails before / after taking effect) | definite errors: handle OPEN with the old state in memory and on reopen; unknown: FAILED, reopen all or nothing; both classes reached |
| Concurrency | `TestCompactConcurrentReaders` | Gets during 20 compactions always see the right values |
| Real kills | `kvdb.TestKillDuringCompactingCommits` (child writes under a 64 KiB cap, so it compacts every few writes; killed with `TerminateProcess`; 6 rounds) | every ACKed value survives; file ≤ cap after reopen; no leftover files |
| Public API | `kvdb.TestCompactThroughAPI` | Compact shrinks the file, leaves only `db.kv` + `.lock`, `ErrClosed` after Close |

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

## Phase 4 — CLI, torture tool, official scenarios, acceptance (✅ implemented)

Every row was executed; results are in [results/](results/) (scenario table:
[results/SCENARIO_RESULTS.md](results/SCENARIO_RESULTS.md)). Tags: [REQ] official
problem statement, [ASSUME] our interpretation, [OPT] extra.

### Official requirements → tests

| Requirement | Tag | Test / evidence |
|---|---|---|
| Six operations, durable put/delete/batch, get not-found | [REQ] | `kvdb` package tests; `cli.TestCommandsRoundTrip`; scenarios A, B |
| Keys 1–1024 B, values 0–1 MB, batches ≤ 10,000 ops | [REQ] ("1 MB" = 1 MiB [ASSUME]) | `kvdb.TestOpValidate`, `kvdb.TestValidateBatch`, `kvdb.TestAPIValidationErrors`, `kvdb.TestMaxBatchOfDistinctKeys`; `cli.TestBatchFileOrderingAndAtomicity` (10,000 accepted, 10,001 rejected); `cli.TestLineReaderLimits` (1 MiB line) |
| ≥ 1,000,000 keys; open < 2 s | [REQ] | `TestGroupB/B6`, `TestGroupF/F3`, `recovery.TestOpenMillionKeysTiming`, `TestAcceptanceMillionPutDelete` |
| ≥ 2 concurrent writers; get never sees half a batch | [REQ] | `TestGroupD/D1–D4`; `cli.TestServeConcurrentWritersKeepOrder`; `storage.TestI1ConcurrentWritersDoNotInterleave` |
| Disk full / `max_bytes` | [REQ] | `TestGroupE/E1–E5`; `cli.TestMaxBytesFlag`, `cli.TestServeMaxBytesErrorsAreClean`; torture `disk-full` |
| CLI: open (key count + recovery time), put, get (NOTFOUND), del, batch file, dump (sorted), check (OK or reason), serve (ACK after durable) | [REQ] | `src/internal/cli/cli_test.go` (all commands, usage errors, quoting, check on torn/corrupt/garbage files, lock while serving) |
| serve input format (`<writer-id> <op-id> <cmd>`, `batch <n>` + n lines, `compact`, replies VALUE/NOTFOUND/ERR) | [ASSUME] (statement fixes only `ACK <op-id>`) | `cli.TestServeProtocol`, `FuzzParseRequest` |
| serve never ACKs a failed / unknown write | [REQ] | `cli.TestServeNeverAcksFailedWrites` (ErrOutcomeUnknown, ErrFailed, ErrFull, OS error) |
| serve never executes a request cut off mid-line | [ASSUME] | `cli.TestServeIgnoresIncompleteFinalLine` |
| `--max-bytes` per invocation, not persisted | [ASSUME] | `cli.TestMaxBytesFlag` |
| check runs recovery first (a torn unacknowledged tail is not corruption) | [ASSUME] | `cli.TestCheckReportsCorruptionAndTornTail` |
| Torture: serve child, ≥ 2 writers, puts/deletes/batches, oracle file, random-ms kill, open → check → dump, compare, 500 default iterations, one line per iteration, stop on first failure with op IDs, `--seed` mandatory and reproducible | [REQ] | `torture/` (run logs in results/); `torture/model_test.go` (checker rejects lost writes, partial batches, garbage, rejected values; accepts legal outcomes); mutation runs in BREAKING.md §4 |
| Torture modes: large values, large batches, recovery kills, compaction kills, disk full | [REQ] | `--mode large-values`, `large-batch`, `recovery-crash`, `compaction`, `disk-full` |
| Extra torture modes `concurrent-disjoint`, `same-key`, gets, JSON report | [OPT] | D1, D2 |
| 32 scenarios A1–F4 | [REQ] | `src/scenarios` (`TestGroupA` … `TestGroupF`), one subtest per scenario; and `nexus-torture scenarios` against the real executable |
| Benchmark: PUT, GET, DELETE, batch throughput, recovery time, compaction time, file size before/after, with OS/CPU/Go/configuration recorded | [OPT] (Prompt 12) | `src/cmd/kvdb-bench`; results in [results/final/BENCHMARK.md](results/final/BENCHMARK.md) |
| Torture Engine 2.0: independent oracle (acked/rejected/unknown, real-time order, batch atomicity, last-op-wins, reads), real process kill/restart, 12 modes, 32 scenarios, mutation testing, JSONL reports, repro from seed + iteration | [REQ] | `torture2/` — plan and test map in [TORTURE_TEST_PLAN.md](TORTURE_TEST_PLAN.md); tests `go test ./torture2/...` |
| 1M PUT then 1M DELETE, compaction shrinks the file, writes after compaction | [REQ] (DESIGN §23 #19) | `TestAcceptanceMillionPutDelete` (KVDB_ACCEPTANCE=1) |

### Official scenarios → tests

| # | Test | Crash method |
|---|---|---|
| A1 | `TestGroupA/A1` | faultfs crash before the first write |
| A2 | `TestGroupA/A2` | faultfs crash mid-write, 7 torn lengths |
| A3 | `TestGroupA/A3` | crash at Sync / right after the write |
| A4 | `TestGroupA/A4` | crash right after Sync returned |
| A5 | `TestGroupA/A5` | crash after Commit returned; serve killed on ACK ×20 |
| A6 | `TestGroupA/A6` | crash sweep over Delete |
| A7 | `TestGroupA/A7` | crash sweep over a 1 MiB put; torture `large-values` |
| B1 | `TestGroupB/B1` | torn write keeping header + 3 entries |
| B2 | `TestGroupB/B2` | payload minus last byte; header (marker) zeroed |
| B3 | `TestGroupB/B3` | crash at Sync after the full frame |
| B4 | `TestGroupB/B4` | crash after success; serve kill on ACK |
| B5 | `TestGroupB/B5` | put X/del X ordering + crash sweep |
| B6 | `TestGroupB/B6` | 1M-key DB, 10,000-op frame torn halfway, timed reopen; torture `large-batch` |
| C1 | `TestGroupC/C1` | crash at every recovery operation before truncation; 3-crash chains |
| C2 | `TestGroupC/C2` | crash around Truncate and its Sync |
| C3 | `TestGroupC/C3` | crash sweep of compaction before R1 |
| C4 | `TestGroupC/C4` | crash at/after R1, at/after R2 |
| C5 | `TestGroupC/C5` | crash sweep after R2; torture `compaction` |
| C6 | `TestGroupC/C6` | crash around closing the file and releasing the lock |
| D1 | `TestGroupD/D1` | torture `concurrent-disjoint` |
| D2 | `TestGroupD/D2` | torture `same-key` |
| D3 | `TestGroupD/D3` | torture `large-batch` |
| D4 | `TestGroupD/D4` | 4 readers (Get + Range snapshots) during 2,000 batches |
| E1 | `TestGroupE/E1` | cap during a put; CLI |
| E2 | `TestGroupE/E2` | over-cap batch; disk-full halfway through the frame |
| E3 | `TestGroupE/E3` | crash after ErrFull; torture `disk-full` |
| E4 | `TestGroupE/E4` | disk full writing `.compact`; over-cap compacting commit; crash sweep |
| E5 | `TestGroupE/E5` | fill, delete 80 %, put again |
| F1 | `TestGroupF/F1` | torture `default`, 500 iterations in full mode |
| F2 | `TestGroupF/F2` | 100 chains of 2 crashed recoveries; torture `recovery-crash` |
| F3 | `TestGroupF/F3` | 1M keys via serve, kill, `kvdb open` timed, dump verified |
| F4 | `TestGroupF/F4` | `os.Truncate(size-1)` |

### Not run

* `go test -race` (no C compiler on this machine).
* Power-loss tests (out of the official model; not claimed).
* Linux/macOS/FreeBSD runtime tests (cross-compiled and vetted only).

## Prompt 10 — competition-readiness hardening (✅ implemented)

Results of the validation run: [results/validation/](results/validation/).
Design notes: DESIGN change log #33–36.

| Change | Test | What it proves |
|---|---|---|
| Windows moves: `MoveFileExW(MOVEFILE_WRITE_THROUGH)`, no replace | `platform.TestMoveFlagsWriteThroughNoReplace` | the exact flag set is `0x8`; `MOVEFILE_REPLACE_EXISTING` (`0x1`) is never passed |
| | `platform.TestMoveNoReplaceNeverOverwritesWindows` | an existing destination (empty, 3 MiB, held open, read-only, directory) is never overwritten; source and destination unchanged |
| | `platform.TestMoveNoReplaceExistErrorClass`, existing `TestMoveNoReplace*`, `TestOpenFileBlocksMove` | destination-present is `fs.ErrExist`; open handles still block the move |
| | `storage.TestCompactCrashSweep`, `TestCompactFaultSweep`; scenarios C3–C5, E4 | compaction crash safety unchanged with the new move |
| `check --no-repair` | `recovery.TestInspectCleanLog` | valid file: nothing to repair, exact frame/key counts |
| | `recovery.TestInspectTornTailEveryLength` | every torn length of the last frame: reported with the exact offset and rule, file unchanged; a real `Open` then truncates exactly there |
| | `recovery.TestInspectMiddleCorruption`, `TestInspectDamagedSnapshotAndSuperblock` | middle corruption and damaged snapshots → `ErrCorruption`, damaged superblock → `ErrBadFormat`, as `Open` does; nothing changed |
| | `recovery.TestInspectMatchesOpenForEveryLayout` | for all 256 combinations of {missing, valid, unsealed, corrupt} × {path, .compact, .old, .new}: same refusals and same authority rules as `Open`, and "needs repair" exactly when `Open` changes the directory |
| | every `TestInspect*` (via `inspectUnchanged`) | faultfs trace contains only stat/open/read/size/close/lock/unlock; directory bytes identical before and after; no handle leaked |
| | `cli.TestCheckNoRepair`, `TestCheckNoRepairLocked` | CLI output and exit codes (OK 0, NEEDS REPAIR 4, CORRUPT/BAD FORMAT/LOCKED 1) for valid, torn, middle-corrupt, damaged-snapshot, damaged-superblock, interrupted-compaction, stale-temp and missing databases; default `check` still repairs |
| Deterministic compaction | `storage.TestCompactDeterministicOutput` | three different random histories with the same final state and next_seq compact to byte-identical files |
| | `storage.TestCompactSnapshotSortedEveryPath` | plain (multi-frame), compacting-commit and automatic compaction all write keys in strictly ascending byte order; reopen and later writes work |
| Independent implementation | `sh crosscheck/run.sh` (9 checks, see docs/INDEPENDENT_VERIFICATION.md) | identical encoding, both directions of read/write, compaction interchange, identical torn-tail and corruption rules, byte-identical compaction |
