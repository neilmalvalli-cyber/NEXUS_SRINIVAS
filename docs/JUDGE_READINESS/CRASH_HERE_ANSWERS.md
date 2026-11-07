# "What if it crashes on this line?": prepared answers

For judging minutes 8–12. Line numbers are for the files as they are on 2026-11-07; `log.go`, `compact.go` and `open.go` have no uncommitted edits. Every answer names the recovery rule and the test that proves it.

**Ground rules for every answer**
1. **The promise:** ACK means durable. An op acknowledged before the crash is in the file after recovery. An op whose ACK was not sent is *unknown*: it may be present or absent, and if it is a batch, completely or not at all.
2. **Invariant I1:** at most one frame is ever unsynced, and it is always the last one. So a crash can only damage the end of the file.
3. **A process kill is not a power cut.** After a kill, every byte the process had passed to `WriteAt` is still in the OS cache and reaches the disk. A power cut can lose or reorder unsynced sectors. The answers below cover both; power cut is covered only by simulation (POWER_FAILURE_MODEL.md).

---

## The three to memorise

### 1. `src/internal/storage/log.go:374`: crash while writing the frame

```go
n, werr := format.WriteFrame(w, l.nextSeq, entries, maxOps)
```

**State on disk.** Some prefix of the new frame is after the old end of file: zero bytes, part of the 32-byte header, the header and part of the payload, or (after a kill) the whole frame. Nothing before `start` is touched; the log is append-only.

**What recovery does** (`recovery.Scan`, tail rules, DESIGN §8.2):
* fewer than 32 bytes left → **C1**: truncate to the frame start;
* header valid, but the payload runs past EOF → **C4**: truncate;
* payload present, but CRC wrong, and the frame ends exactly at EOF → **C5**: truncate;
* complete and valid → kept (the kill case), and the op is applied.

Truncation is always to exactly the frame start, never below `snapshot_end`, and it is synced before any new write (`recovery/open.go:84–89`).

**Client view.** No ACK was sent, so the outcome is unknown, and both results are legal. A batch is one frame with one payload CRC, so it is all or nothing. Every earlier acknowledged frame is intact (I1).

**Proof.** `storage.TestCommitCrashSweep` crashes at every I/O op of a commit (with 0, 9 or all bytes kept) and reopens. `recovery.TestTailKillPointSweep` cuts a file at every byte. Torture default mode: `kills-mid-write=200` per run.

### 2. `src/internal/storage/log.go:388`: crash during, or just after, `Sync`, before the ACK

```go
if err := l.f.Sync(); err != nil {
```

**State on disk.**
* *Kill:* the whole frame is in the OS cache, so it survives.
* *Power cut during Sync:* any subset of the frame's sectors may be on disk.
* *After Sync returned* (`log.go:398`, before the index update and the ACK): the frame is durable.

**What recovery does.**
* A complete frame with the expected seq and a correct CRC is replayed into the index.
* A torn one is handled by C1, C4 or C5, as in answer 1.
* *Power cut, header sector lost but later sectors kept:* if a complete valid frame follows, open **refuses** (C3, amended 2026-11-07) rather than truncating. Otherwise it is C2, a torn tail.

**Client view.** Still no ACK, so unknown. This is exactly the "one more key than ACKs" seen in the manual kill rehearsal: the frame was durable, but the ACK died with the process. If `Sync` *returns an error*, NEXUS never retries it (a retried fsync can falsely succeed). It marks the handle failed and returns `ErrOutcomeUnknown` (CLI exit 3), not "failed" (`log.go:389–394`).

**Proof.** `TestCommitCrashSweep` (the `CrashAfter` mode covers "Sync done, crashed before return"). `kvdb.TestKillDuringWritesLosesNothingAcknowledged` uses a real child process and real kills. Manual rehearsal: 19,492 ACKs checked, 0 lost.

### 3. `src/internal/storage/compact.go:266`: crash in the middle of the compaction swap (after R1, before R2)

```go
if err := l.moveRetry(l.path, opath); err != nil { // R1   (line 263)
if err := l.moveRetry(cpath, l.path); err != nil { // R2   (line 266)
```

**State on disk.** `db` is missing. `db.old` is the complete old file. `db.compact` is the complete new file, sealed: its superblock was written **last** (line 218) and synced (line 221) before any rename.

**What recovery does** (authority rules, DESIGN §11, evaluated A1 → A7):
* `path` missing and `.compact` sealed → **A3**: move `.compact` to `path` (no-replace move), then restart at A1.
* A1 sees a valid `path` and deletes the stale `.old`.

The new file holds exactly the live keys of the old one (plus the commit, for a compacting commit), so no data is lost.

**The neighbouring crash points**
* *Before R1* (anywhere while writing `.compact`, lines 200–225): `path` is still valid, so **A1** uses it and deletes `.compact`. The compaction is lost; nothing else is.
* *Seal half-written* (line 218): the superblock CRC fails, so the file is "unsealed", which is never authoritative.
* *After R2* (line 273 onward): `path` is the new file; A1 deletes `.old`.

**Client view.** Plain compaction changes no data, so there is nothing to be unsure about. For a *compacting commit*, the commit's ACK is sent only after line 295, so a crash anywhere earlier leaves it unknown.

**Proof.**
* `storage.TestCompactCrashSweep` crashes at every compaction I/O step.
* `recovery.TestCrashSweep` and the authority tests cover every A-rule file combination.
* `kvdb.TestKillDuringCompactingCommits`.
* Torture `--mode compaction`.
* Rehearsed on a 103 MB file: kills at 20–1000 ms left either `.compact` (old file used) or `.old` (new file used), and all 60,000 live keys survived every time (COMPACTION_SHRINK_TEST.md).

---

## Spares (if the judge points elsewhere)

### `src/internal/recovery/open.go:84`: crash during recovery's own truncation

```go
if err = f.Truncate(scan.End); err != nil {
```

Recovery is idempotent: it only ever cuts the torn tail, and that holds no acknowledged data. If it crashes during the truncate or the following sync, the next open scans again from the start. It finds either the same torn tail and cuts it again, or the already-cut file, which is clean. A guard at line 80 refuses any truncation below `snapshot_end` or not shorter than the file. Proof: `recovery.TestOpenCrashSweep`, and torture `--mode recovery-crash`, which kills the process *during* recovery (the result line reports `recovery-kills-landed`).

### `src/internal/storage/log.go:398–415`: crash while updating the in-memory index

The index is memory only and is rebuilt from the file at every open, so a crash here loses nothing. The frame was already synced at line 388. Readers are excluded (`l.mu.Lock()`) while the index changes, so no reader sees half a batch.

### `src/internal/storage/compact.go:211`: crash before the frames of `.compact` are synced

`.compact` has no valid superblock yet: offset 0 is zeros until the seal at line 218. So it is unsealed, and **A1** keeps the old `path` and deletes it. Sealing last means "valid superblock ⇔ file complete" (DESIGN §10).

### A crash between the ACK being written and the client reading it

The op is durable (ACK is sent only after `commitLocked` returns). The client sees no ACK, so to the client it is unknown, and retrying a `put` or `del` is idempotent. The dashboard and HTTP API show this as "outcome unknown", never as "failed" (FRONTEND_FAILURE_TESTS.md, N2/N3).
