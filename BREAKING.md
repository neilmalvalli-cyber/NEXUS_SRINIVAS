# Breaking NEXUS

How we attacked our own design. Every crash point names the function where
the process dies; the tests named are in the repository and were run (results:
[docs/results](docs/results)).

## 1. Crash points

`commitLocked` = `src/internal/storage/log.go`; `compactLocked` =
`src/internal/storage/compact.go`; `recovery.Open` =
`src/internal/recovery/open.go`; `Resolve` = `src/internal/recovery/authority.go`.

| # | Process dies in / after | Disk state | What the next `open()` does |
|---|---|---|---|
| 1 | `commitLocked`, before `format.WriteFrame` writes a byte (A1) | committed frames only | nothing to repair; key has its old value |
| 2 | `commitLocked`, inside `WriteFrame` (torn frame, A2/B1/B6) | last frame cut short | tail rule C1/C2/C4 truncates to the last good frame, `fsync` |
| 3 | `commitLocked`, after `WriteFrame`, before `f.Sync()` (A3/B3) | complete frame, not flushed | complete frame passes every CRC: kept (allowed: never acknowledged) |
| 4 | `commitLocked`, after `f.Sync()` returned, before the index update / return (A4) | complete, durable frame | frame replayed: write present |
| 5 | `commitLocked`, `f.Sync()` returned an error (not a crash) | unknown | handle goes FAILED, caller gets `ErrOutcomeUnknown`; next open keeps or drops the whole frame |
| 6 | `recovery.Open`, during `Scan` (C1) | unchanged (scan is read-only) | same scan again |
| 7 | `recovery.Open`, between `f.Truncate(scan.End)` and `f.Sync()` (C2) | tail cut or not | truncation to the same offset again (idempotent) |
| 8 | `compactLocked` phase A, writing/syncing `.compact` (C3) | `path` valid, `.compact` unsealed or sealed | A1: `path` is authoritative, `.compact` deleted |
| 9 | `compactLocked` after R1 `MoveNoReplace(path, .old)`, before R2 (C4) | no `path`; `.old` valid; `.compact` sealed | A3: move `.compact` → `path` (complete, includes all data) |
| 10 | `compactLocked` after R2 `MoveNoReplace(.compact, path)` (C5) | `path` = new file; `.old` present | A1: use `path`, delete `.old` |
| 11 | `compactLocked` during R3 `remove(.old)` | `path` valid, maybe `.old` | A1 again; leftover deleted |
| 12 | `Resolve`/`createFresh` while creating a new database via `.new` | `.new` unsealed or sealed, no `path` | A7 deletes an unsealed `.new` and recreates; A5 adopts a sealed one |
| 13 | `Log.Close` (C6) | all acknowledged frames synced | nothing special; the OS releases the lock handle |
| 14 | compacting commit (cap reached, E4) at any step | as rows 8–11 | old state, or old state + the commit; never part of it |

## 2. Ordering argument

Write path (`commitLocked`), holding the exclusive `sync.RWMutex`:

1. `WriteFrame` → `WriteFile` (via `WriteAt`) of header + entries, in file order, at the current end.
2. `File.Sync` → **`FlushFileBuffers`** (Windows) / `fsync` (Linux, macOS, FreeBSD).
3. Only then: apply the entries to the in-memory index and return `nil`.
4. `serve` prints `ACK <op-id>` only after step 3 returned `nil`.

A frame is valid only if its header CRC, payload CRC and all entry CRCs verify,
so the payload CRC is the commit marker and a frame cannot be half-applied.
Because a frame is synced before the next one starts (invariant I1), only the
**last** frame can be incomplete after a kill; recovery may cut only that
frame, and only when its damage reaches exactly EOF.

Compaction (`compactLocked`): write snapshot frames to `.compact` → `Sync` →
write the superblock **last** → `Sync` → `Close` → close the data handle (Windows
cannot move an open file) → R1 `MoveFileExW(path, path.old)` → R2
`MoveFileExW(path.compact, path)` → directory sync → reopen and fully validate →
swap the index → R3 delete `.old`. `MoveFileExW` is called with
`MOVEFILE_WRITE_THROUGH` only, so it never replaces an existing file;
`os.Rename` would (it uses `MOVEFILE_REPLACE_EXISTING`), so it is never used.
Write-through makes the call return only after the rename is flushed; it is
not treated as evidence of power-loss durability.

**Directory fsync:** on Linux/macOS/FreeBSD we `fsync` the directory after R2
and after adopting a file. Windows offers no directory fsync; we do not need one
for the official model (process death: the rename is in the OS's file-system
state the moment `MoveFileExW` returns), and we do **not** claim survival of a
power cut on Windows.

## 3. Known weaknesses

1. **Power loss on Windows** is not covered (no directory flush; we rely on
   NTFS metadata ordering we have not verified). Process death is covered.
2. **Damage at the very end is trusted to be a torn write.** A bit flip inside
   the last frame's header or payload (when that frame ends at EOF) is
   indistinguishable from a kill and is truncated silently, losing that frame
   even if it had been acknowledged. Mid-file damage is detected and refused.
3. **Recovery time grows with the log.** Open replays the whole log (0.45 s for
   1,000,000 keys / 45 MB here). Without compaction, a log with many
   overwrites could exceed 2 s; automatic compaction (file ≥ 4 MiB and more than
   twice its live size) bounds this, but there is no checkpointed index.
4. **The index is all in memory** (~200 MB heap for 1M short keys).
5. **Third-party handles.** Antivirus or backup tools that open our files can
   make a rename fail; we retry sharing violations for up to 1 s, then the
   compaction aborts (definite) or the handle goes FAILED (reopen recovers).
6. **Real kills rarely land inside a write syscall.** Across the torture runs,
   recoveries that had to cut a torn tail are rare; torn frames are therefore
   proven by in-process fault injection (scenarios A2, A7, B1, B2, B6), not by
   the torture tool alone.
7. **The tester cannot aim a kill at an exact instruction.** Torture Engine
   2.0 kills the real process at random moments, and covers exact crash
   points (byte k of a frame, between fsync and the reply, every compaction
   step) by replaying the disk state such a kill leaves. Its oracle can
   answer INCONCLUSIVE on adversarial histories; that is reported and never
   counted as a pass ([docs/TORTURE_RESULTS.md](docs/TORTURE_RESULTS.md)).

## 4. What we tried that failed

* **`os.Rename` for the swap.** On Windows it silently replaces the target
  (`TestOsRenameReplacesOnWindows`). A crash-time adoption could overwrite the
  only complete file. Replaced by `syscall.MoveFile` (no replace), and since
  Prompt 10 by `MoveFileExW(MOVEFILE_WRITE_THROUGH)` (still no replace;
  `TestMoveNoReplaceNeverOverwritesWindows`).
* **Renaming the open data file.** Go opens files without
  `FILE_SHARE_DELETE`, so the move fails with a sharing violation
  (`TestOpenFileBlocksMove`). Fixed by closing the handle before R1 and
  reopening after.
* **Forward scan after a damaged header** (to "skip" damage) was rejected: a
  user value can contain a valid-looking frame header, so a torn write would be
  misread as corruption (`TestTornFrameContainingEmbeddedFrameHeader`).
* **Retrying a failed `fsync`.** Rejected: a retry can falsely succeed after the
  kernel dropped dirty pages. A failed Sync is `ErrOutcomeUnknown` + FAILED.
* **Torture-tool findings.**
  - A deliberately broken `kvdb` that prints `ACK` before the write was caught
    within 2–3 iterations (lost acknowledged write, with op IDs); one that
    commits a batch as separate frames was caught within 1–3 iterations
    (partial batch), also in `--mode large-batch`.
  - Our first disk-full mode never reached the cap (0 rejections): the key
    space was too small. Enlarged so every iteration hits `full`.
  - Writing the torture driver showed that `serve` would have executed a final
    request line with no newline (a writer killed mid-write). `serve` now
    refuses such a line (`TestServeIgnoresIncompleteFinalLine`).
  - Earlier fault-injection work found a `faultfs` bug (a partial lock
    "succeeded" without a handle) and several test bugs (wrong skip counts,
    shadowed paths); they were fixed in the tests, not by weakening the engine.
  - No engine bug was found by the final torture runs (results in
    [docs/results](docs/results)).
* **Cross-checking against an independent implementation** (teammate
  recovery package, [docs/INDEPENDENT_VERIFICATION.md](docs/INDEPENDENT_VERIFICATION.md)):
  - Encoding, torn-tail rules and corruption rules agreed at once. The two
    implementations were written separately from the same specification.
  - Compacted files differed only in key order: NEXUS wrote snapshot
    entries in old-file offset order, which depends on history. Compaction
    now sorts by key, and the two outputs are byte-identical (`crosscheck`
    check 8).
  - The independent `Verify` is read-only, while our `check` repairs before
    verifying. We kept that default (the spec has `check` run recovery) and
    added `check --no-repair`, which is proven read-only by faultfs traces,
    the 256-layout agreement test and byte comparisons.
  - Our own first version of the cross-check expected six frames and saw
    five: `del 6` from a second writer ran before `put 6` (different
    writers run concurrently), and deleting a missing key writes no frame.
    This was correct engine behaviour; the harness was wrong and was fixed.

## 5. If we had 24 more hours

1. A checkpointed index (or index snapshot frames) so recovery time no longer
   depends on log length, plus a background compaction thread.
2. Power-loss testing on Windows (VM snapshots / write-through `FILE_FLAG_WRITE_THROUGH`
   and `FlushFileBuffers` on the directory handle via `CreateFile` with
   `FILE_FLAG_BACKUP_SEMANTICS`).
3. Kill the process *inside* `WriteFile` more often: a test build that
   deliberately splits frame writes into many small `WriteFile` calls, so random
   kills produce torn frames instead of relying on fault injection.

## Attack catalogue

Each row: what is attempted · why it threatens consistency · what a broken
implementation would do · how NEXUS handles it · the test · expected result.

| Attack | Threat | Broken implementation | NEXUS | Test | Expected |
|---|---|---|---|---|---|
| Partial frame write | half a record on disk | returns garbage or half a batch | CRCs reject it; tail rule truncates only the last frame | A2, B1, B6 (`src/scenarios`) | old value; file cut to last good frame |
| Failed `Sync` | data may or may not be durable | reports success, or retries Sync, or reports a definite failure | `ErrOutcomeUnknown`, FAILED, no retry, no ACK | `storage.TestF1SyncFailureInvariants`, `cli.TestServeNeverAcksFailedWrites` | no ACK; after reopen all-or-nothing |
| Interrupted batch | some ops durable, others not | partial batch visible | one frame per batch; applied only after full verification | B1–B3, D3, torture `large-batch` | none or all |
| Damaged frame header | could be a torn write or corruption | scans forward and resurrects garbage, or refuses a killed file | C2 at EOF → truncate; header damage followed by more frames → refuse | `recovery.TestTail*`, B2 | open succeeds after kill; corruption reported otherwise |
| Invalid payload CRC | torn tail vs bit rot | accepts bad data or truncates good data | C5 truncates only if the frame ends exactly at EOF, else C6 `ErrCorruption` | `recovery.TestTailC5*/C6*`, `cli.TestCheckReportsCorruptionAndTornTail` | torn tail cut; mid-file damage → `CORRUPT` |
| Process termination | kill at any instruction | lost ACKed writes | Sync before ACK; recovery idempotent | A1–A7, F1, torture (all modes) | every ACKed op present |
| Concurrent writers | interleaved bytes, lost updates | two frames interleaved | one exclusive lock around write+sync+index | D1–D4, `storage.TestI1ConcurrentWritersDoNotInterleave` | values written by one writer, never mixed |
| Two processes open one DB | both truncate/rename | double recovery corrupts files | `<path>.lock` with share mode 0; second open → `ErrLocked`, touches nothing | `cli.TestLockedWhileServing`, `locking` tests | second process: "locked" |
| Disk full | partial frame, then failures | half-written frame left behind | rollback truncate to frame start, `ErrFull`, handle stays OPEN | E2(b), `storage.TestI1DiskFullRollbackKeepsHandleOpen` | ErrFull, nothing visible, next write works |
| Compaction killed between moves | no file at `path` | starts a fresh empty DB (all data lost) | A3/A4 adopt the complete `.compact`/`.old` | C4, `recovery.TestAuthorityTable` | all data, one authoritative file |
| `.new` / `.compact` / `.old` left over | which file is real? | deletes the only complete copy | A1–A7: delete temps only when `path` is valid; adopt only sealed files; non-overwriting moves | C3–C5, `recovery.TestAuthorityTable` | data intact, temps removed |
| `maxBytes` exhaustion | file grows past cap or half-writes | partial write, file over cap | admission before any byte; compacting commit if the result fits; else `ErrFull` | E1, E2, E5, torture `disk-full` | `ERR full`, file ≤ cap, data intact |
| Interrupted compacting commit | commit + compaction in one step | commit half-applied or data lost | the commit is inside the sealed snapshot; acknowledged only after swap + validation | E4(c), `kvdb.TestKillDuringCompactingCommits` | old state or old + commit |

## Final readiness pass (2026-11-07)

Found and fixed during the last judge-readiness pass, each with a regression test:

| Issue | Impact | Fix | Test |
|---|---|---|---|
| Mid-log frame-header damage was truncated as if it were a torn tail (attack P7) | silent loss of acknowledged frames after one flipped bit | rule C3 amended: refuse when a complete valid later frame exists | `recovery.TestTailC3MidLogHeaderDamageRefused` and 3 more |
| `./torture` was a folder, not a program, in a clean clone; the README build line did not build it | the judge's first command fails | tool moved to `src/cmd/torture/`; the README builds it | `scenarios` harness builds `./src/cmd/torture` |
| `--seed 12abc` silently ran seed 12 | the run reports a different seed from the one the judge typed | strict integer parse, exit 2 | `torture.TestSeedMustBeExactInteger` |
| The dashboard showed lost write answers as "failed" | an op that may be durable shown as not applied | outcome-unknown state | `frontend/scripts/write-outcome-test.mjs` |

Measured, not modelled: 1M-key recovery 0.55–0.80 s (worst case one frame per key), compaction 103 MB → 10.3 MB after 90% deletes, 12 manual kill cycles with 19,492 ACKs and 0 lost. See `docs/JUDGE_READINESS/`.
