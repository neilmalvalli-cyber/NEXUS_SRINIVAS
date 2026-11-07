# Disk-full safety: validated

Status: **PASS for the `--max-bytes` cap and for injected out-of-space errors. A physically full volume was NOT TESTED** (see "Not tested"). Measured 2026-11-07 on disposable files in the session scratch folder. The real system drive was never filled.

## How NEXUS handles "full"

* A write that would take the data file past `max_bytes` is refused with `ErrFull` **before any byte is written**. The file stays byte-for-byte unchanged, and the database stays open and usable.
* Before refusing, NEXUS tries compaction: if the commit fits once old versions and deleted keys are dropped, it compacts and commits (DESIGN §6.3, "compacting commit").
* If the operating system returns "disk full" (ENOSPC / `ERROR_DISK_FULL`) partway through a write, NEXUS truncates the file back to its last committed end, keeps the handle open, and returns `ErrFull`. A rollback it cannot prove becomes `ErrOutcomeUnknown`, exit code 3, never a false failure (I1 rule; DESIGN §6).
* In the CLI, `ErrFull` is exit code 1, `ERROR (full)`. In `serve` it is `ERR <op-id> full …`.

## CLI test (real binary, 200,000-byte cap)

| Step | Result |
|---|---|
| `kvdb put f.kv k<n> <1 KB value> --max-bytes 200000`, repeated | 197 puts accepted, file 199,909 B. Automatic compaction kept it to one frame along the way. |
| Put #198 | refused, exit 1: `ERROR (full): kvdb: database full: commit of 1047 bytes does not fit max_bytes 200000 (file 199909 bytes, compacted with the commit: 200924 bytes)` |
| After the refusal | `check`: 197 keys, OK. The last accepted key reads back; the refused key is `NOTFOUND`. |
| A 3-put `batch` that does not fit | refused, exit 1. **None** of its 3 keys exist (atomic), and the size is unchanged at 199,909 B. |
| `serve --max-bytes 200000`: a put, then a get | `ERR p1 full …`, then `VALUE g1 …`. The server keeps serving after the refusal. |
| `batch` of 99 deletes while full | accepted (`OK 99 ops`). Compaction reclaimed space, and the file dropped to 99,479 B. |
| A new 1 KB put | accepted, file 100,527 B; `check`: 99 keys, OK. A deleted key is `NOTFOUND`, a kept key is intact. |

So a full database **recovers by itself once data is deleted**, with no manual step. (The engine refuses `max_bytes` below the size needed for one frame.)

## Automated tests (run 2026-11-07, all PASS)

| Test | What it proves |
|---|---|
| `storage.TestI1DiskFullRollbackKeepsHandleOpen` | An OS disk-full error injected partway through a multi-write commit (at write 0, 1 and 2, with partial bytes kept): the file and counters are restored exactly, `ErrFull` (not unknown) is returned, and the handle still works. |
| `storage.TestCompactDiskFullAndCorruptionChangeNothing` | Disk full during compaction: the original file is untouched and temp files are removed. |
| `storage.TestAdmissionCompactingCommitAndFull` | The cap triggers a compacting commit when that is enough, and `ErrFull` when it is not. |
| `storage.TestGroupCommitSplitsUnderMaxBytes` | Concurrent writers under the cap: each gets a definite answer; no group spills over the cap. |
| `storage.TestSequenceExhaustionIsErrFull` | Limits that cannot be met give a definite `ErrFull` with no I/O. |
| `kvdb.TestMaxBytesThroughAPI` | The public API's behaviour under the cap. |
| `cli.TestMaxBytesFlag`, `cli.TestServeMaxBytesErrorsAreClean` | CLI and serve output and exit codes. |
| `fileio.TestWriteFullAtDetectsShortWrites` | A short write is detected, never treated as success. |

Command: `go test -count=1 -run 'MaxBytes|DiskFull|Full|SequenceExhaustion|ShortWrites' ./src/...`

Torture also has a `--mode disk-full` (real process kills while the cap is being hit). See CRASH_SCENARIO_RESULTS.md for the run.

## Not tested

* **A physically full volume (S2): NOT TESTED.** It needs a small dedicated volume (VHD or RAM disk), and creating one needs administrator rights. The real drive was deliberately not filled. The OS error path is covered by fault injection (`faultfs` returns the platform's real disk-full error), not by a real full disk.
* NTFS metadata exhaustion and quota limits were not tested.
