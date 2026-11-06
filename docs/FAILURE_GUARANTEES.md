# Failure guarantees

What NEXUS promises for each failure, the evidence that it keeps the promise,
and where the promise stops. The crash model is **process death** (the
process is killed at any instruction; the operating system and the disk
survive). **Power loss is not tested and not claimed.**

Evidence legend: `nexus-torture` = Torture Engine 2.0 against the real
executable ([TORTURE_TEST_PLAN.md](TORTURE_TEST_PLAN.md)); `TestGroupX/…` =
Go scenario tests in `src/scenarios` (fault injection plus real kills);
other names are unit tests. Measured results: [TORTURE_RESULTS.md](TORTURE_RESULTS.md)
and [results/final/](results/final/README.md).

| Failure | Expected behaviour | Evidence | Limitations |
|---|---|---|---|
| **Process killed during a PUT or DELETE** | Not acknowledged: the key holds its old value or the new value, never a mix or garbage. Acknowledged (`OK`/`ACK` received): the new value is there after restart. | `nexus-torture scenarios` A1–A7 (replay of every byte prefix of real put/delete frames, 1 MiB included; real kills of `kvdb put/del`; serve killed right after each ACK); `nexus-torture run` (every iteration kills `serve` with writes in flight; the oracle checks every acknowledged write); `TestGroupA/A1–A7` | A kill from outside cannot be aimed at an exact instruction; exact points are covered by replaying the disk state such a kill leaves. |
| **Process killed during a BATCH** | All or nothing: after restart either every operation of the batch is visible (in order, last op on a key wins) or none is. An acknowledged batch is fully visible. Readers never see half a batch. | `nexus-torture scenarios` B1–B6 (every byte prefix of 10-op frames, a 10,000-op frame on a 1,000,000-key database, put+delete of one key), D3, D4; modes `batch10`, `large-batch`, `readers`; the oracle reports any partial batch as `partial-batch` (mutation `partial-batch` is caught); `TestGroupB/B1–B6`, `cli.TestBatchFileOrderingAndAtomicity` | Batches are limited to 10,000 operations (one frame). |
| **Torn final frame** (kill left half a frame at the end) | `open` discards the incomplete last frame (it was never acknowledged), truncates the file to the last complete frame and syncs. `check --no-repair` reports `NEEDS REPAIR` (exit 4) without changing the file. | `recovery.TestTailC1…C7`, `TestTailKillPointSweep`, `TestInspectTornTailEveryLength`; `nexus-torture scenarios` A2, B1, B2, C2, F4 (1-byte hand truncation; file unchanged by `--no-repair`); every `nexus-torture` iteration runs `check --no-repair` and checks the files are byte-identical afterwards | A bit flip inside the **last** frame, when that frame ends exactly at the end of the file, looks the same as a torn write and is truncated, losing that frame even if it was acknowledged ([BREAKING.md §3](../BREAKING.md)). |
| **Corruption in the middle of the database** (bit rot, manual edits) | Detected by CRC32C (superblock, frame header, payload, every entry). Reported as `CORRUPT`; the file is left untouched; NEXUS refuses to open it rather than guess. Never "repaired". A damaged value found by `get` returns an error without closing the handle. | `recovery.TestInspectMiddleCorruption`, `TestTailC6BytesAfterBadPayload`, `TestTailC3NoForwardScan`, `TestReadErrorsNeverTruncate`, `TestInspectDamagedSnapshotAndSuperblock`, `format.TestFrameHeaderEveryBitFlipRejected`, `TestSuperblockEveryBitFlipRejected`, `storage.TestGetCorruptionKeepsHandleOpen`, `cli.TestCheckReportsCorruptionAndTornTail` | No repair tool: data behind real corruption is not recovered automatically. CRC32C detects accidental damage, not deliberate tampering. |
| **Disk full / size cap** (`--max-bytes`) | A write that does not fit fails with `full` **before** any byte is written; earlier data stays intact; the database stays readable. If compaction can make room it runs first. After deletes free space, writes succeed again. | `nexus-torture scenarios` E1–E5 (file compared byte for byte before/after a refused write; batch over the cap; kills at the cap; compaction at the cap); mode `disk-full`; `TestGroupE/E1–E5`, `storage.TestAdmissionCompactingCommitAndFull`, `TestCompactDiskFullAndCorruptionChangeNothing`, `cli.TestMaxBytesFlag`, `TestServeMaxBytesErrorsAreClean` | A **real** full disk is simulated by the cap and by fault injection (the platform disk-full error, `ERROR_DISK_FULL` / `ENOSPC`, injected mid-write by the fault file system: `storage.TestI1DiskFullRollbackKeepsHandleOpen`), not by filling a physical volume. The cap is per run, not stored in the file. |
| **Crash during compaction** | At every step exactly one complete file is the database: the old file until the new one is sealed (superblock written last) and synced; after the swap, the new one. Two renames that never overwrite. Leftover `.compact`/`.old` files are cleaned up or ignored. No acknowledged data is lost. | `nexus-torture scenarios` C3–C5 (states built from the real compaction output: unsealed temp of many lengths, partly written seal, between the renames, before cleanup), E4; mode `compaction` (kills with compaction in flight); `storage.TestCompactCrashSweep`, `TestCompactFaultSweep`, `kvdb.TestKillDuringCompactingCommits`, `recovery.TestAuthorityTable`, `TestInspectMatchesOpenForEveryLayout` (all 256 file layouts) | Windows has no directory `fsync`. Renames use `MoveFileExW(MOVEFILE_WRITE_THROUGH)`, but whether a finished rename survives a **power cut** is not tested and not claimed. |
| **Recovery after restart** (including being killed again during recovery) | `open` replays the log, checking every checksum and sequence number, and reaches the same state however often it is interrupted and rerun (idempotent). Recovery of 1,000,000 keys takes well under 2 s on the test machine. | `nexus-torture scenarios` C1, C2, F2 (real kills of `kvdb open` on large torn databases), F3 (1,000,000 keys, kill, timed open, oracle); mode `recovery-crash` (`kvdb open` killed 1–3 times after each crash); `recovery.TestOpenCrashSweep`, `TestOpenFaultSweep`, `TestOpenMillionKeysTiming`; `kvdb-bench` reopen phase | Recovery time grows with the log length (full replay, no checkpointed index); automatic compaction bounds it. The whole index must fit in memory. |

## What "acknowledged" means

A write is acknowledged only when the caller received success: `OK` from the
CLI, a complete `ACK <id>` line from `serve`, or `nil` from the library. NEXUS
sends that only after the frame was written **and** `FlushFileBuffers`/`fsync`
returned. If the flush fails, the answer is "outcome unknown" (exit 3, `ERR
unknown`), never success, and that handle refuses further writes.

## How the evidence is checked

Torture Engine 2.0 treats a request without a complete reply as **unknown**:
it may be present or absent, but if present, fully present. Its oracle
accepts a recovered state only if some legal execution explains it, and an
iteration passes only if the oracle says PASS. An oracle that runs out of
budget answers INCONCLUSIVE, which is reported separately and never counted
as a pass. The tester itself is checked by mutation testing: six
deliberately broken builds (ACK before sync, dropped acknowledged write,
partial batch, wrong unknown handling, bad recovery, corrupted value) are
all detected ([TORTURE_RESULTS.md](TORTURE_RESULTS.md)).
