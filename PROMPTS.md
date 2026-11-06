# AI Prompt Log

Every prompt sent to any AI tool for this project is recorded here **unedited**, in
chronological order, with the tool name and an approximate timestamp (local time).
Missing entries cost points (challenge rule §9), so append every new prompt before
committing work produced from it.

Prompts that produced `docs/DESIGN.md` (the v1 → v5 architecture review) were sent
before this repository existed; append them below (in the section "Design-phase
prompts") if they have not been recorded yet.

---

## Design-phase prompts

_TODO (team): paste the design-review prompts that produced `docs/DESIGN.md` here,
unedited, with tool name and timestamp._

---

## Implementation prompts

### Prompt 1

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 12:30 (local time)
- **Notes:** The message attached `C:\Users\MY PC\Downloads\DESIGN.md` (copied into this
  repository as `docs/DESIGN.md`) and contained the master prompt below. Full text as sent:

````text
@"C:\Users\MY PC\Downloads\DESIGN.md"

PROJECT: KILL IT AND PROVE IT — BUILD A DATABASE ENGINE FROM SCRATCH
You are the lead Go systems engineer responsible for building a crash-safe key-value database engine from scratch for a competitive 24-hour database-engine challenge.
Repository:
https://github.com/neilmalvalli-cyber/NEXUS_SRINIVAS
You are working inside the locally cloned repository. Inspect the current repository before making changes.
1. YOUR ROLE
Act as a senior database-storage engineer, not as a code-generation assistant that blindly follows instructions.
You must:

* Think critically about storage correctness.
* Identify crash-consistency problems before implementing.
* Explain important architectural decisions.
* Write clean, maintainable, idiomatic Go.
* Test every important component.
* Avoid unnecessary dependencies.
* Never claim a feature is complete without testing it.

The judging environment is native 64-bit Windows. Linux support is secondary.
2. PROJECT OBJECTIVE
Build a key-value database from scratch.
Forbidden: using an existing database engine or storage library such as SQLite, RocksDB or LevelDB.
Required API:

```
Open(path string, maxBytes int64) (*DB, error)
(db *DB) Put(key, value []byte) error
(db *DB) Get(key []byte) ([]byte, error)
(db *DB) Delete(key []byte) error
(db *DB) Batch(ops []Op) error
(db *DB) Compact() error
(db *DB) Close() error
```

Required CLI commands:

* open
* put
* get
* del
* batch
* dump
* check
* serve

Core guarantees:

* Acknowledged writes survive process termination.
* Incomplete operations are fully committed or discarded.
* Batches are all-or-nothing.
* Recovery never exposes partially written values.
* The database can recover after a crash.
* Compaction is mandatory and crash-safe.
* At least two concurrent writers must be supported.
* Recovery target: under two seconds with one million keys.
* Values can be up to 1 MB.
* Batches can contain up to 10,000 operations.

3. APPROVED ARCHITECTURE BASELINE
Use the final reviewed v4 architecture with the following corrections.
Storage
Use an append-only binary log as the authoritative storage.
Maintain an in-memory hash index mapping keys to their latest value locations on disk.
Do not store all values in the index.
File layout
For database path `database.kv`, use:

* `database.kv` — authoritative data file
* `database.kv.lock` — process lock
* `database.kv.new` — new database creation temporary file
* `database.kv.compact` — compaction temporary file
* `database.kv.old` — previous database during replacement

Binary format
Superblock: 32 bytes, little-endian.

* Bytes 0–7: magic `KIPDBLOG`
* Bytes 8–9: version = 1
* Bytes 10–11: flags = 0
* Bytes 12–19: `first_seq`
* Bytes 20–27: `snapshot_end`
* Bytes 28–31: CRC32C of bytes 0–27

Frame header: 32 bytes.

* Magic: `0xD8C3B1A5`
* Type: 1
* Reserved: 0
* Operation count: uint16
* Sequence: uint64
* Payload length: uint64
* Payload CRC32C
* Header CRC32C over bytes 0–27

Entry header: 11 bytes.

* Operation: PUT = 1, DELETE = 2
* Key length: uint16
* Value length: uint32
* Entry CRC32C
* Key bytes followed by value bytes

Limits:

* Key: 1–1024 bytes
* Value: 0–1,048,576 bytes
* Maximum payload: 2^40 bytes
* Batch: maximum 10,000 operations

Use streaming encoding and decoding. Do not impose an arbitrary 64 MiB batch limit.
Durability
Normal write ordering:

1. Validate the request.
2. Acquire the exclusive database lock.
3. Check admission limits.
4. Write the complete frame.
5. Call Sync.
6. Update the in-memory index.
7. Return success / acknowledge.

Never acknowledge before the durability operation succeeds.
If a write fails halfway through, truncate back to the frame's starting offset and sync. If rollback cannot be proven successful, mark the handle failed and return an outcome-unknown error.
Recovery
Recovery must:

1. Resolve database-file authority using the documented temporary-file protocol.
2. Validate the superblock.
3. Scan frames sequentially.
4. Validate header CRC, payload CRC, entry CRC and sequence numbers.
5. Apply only complete valid frames.
6. Classify a damaged tail carefully.
7. Never truncate below `snapshot_end`.
8. Never treat an actual read error as end-of-file.
9. Truncate only when structural tail damage is proven.
10. Sync after truncation.

A real read error must fail recovery without modifying the file.
Windows compaction protocol — IMPORTANT
Do not use a single replace-existing rename.
Use two non-overwriting renames:

1. Write the complete compacted file to `.compact`.
2. Flush frames.
3. Write the superblock last.
4. Flush again.
5. Close the temporary file.
6. Close the old data-file handle.
7. Move `path` to `path.old` using a Windows operation that fails if the destination exists.
8. Move `path.compact` to `path` using the same non-overwriting operation.
9. Remove `path.old` best-effort.
10. Reopen and validate the new file.
11. Only then swap the in-memory index.

Use Go's Windows syscall support or a small, carefully isolated Windows-specific implementation. Verify actual API availability on the installed Go version.
Recovery must understand all interrupted rename states.
Never delete the only valid copy of the database.
If R1 fails, restore/reopen the old data file.
If R2 fails, try to restore `.old` to the original path. If restoration fails, mark the handle failed and let the next Open resolve the authority state.
Keep all temporary files in the same directory as the database.
Retry sharing violations with bounded backoff.
Concurrency
Use one global `sync.RWMutex`.

* GET holds a read lock through lookup and disk read.
* PUT, DELETE, BATCH and COMPACT hold the write lock.
* Index updates happen only after durable commit.
* GET must never observe a partially applied batch.

Use a process-level lock file so two processes cannot open the same database simultaneously.
Batch semantics
Operations execute sequentially.
The last operation on a key wins.
Examples:

* PUT X=1, PUT X=2 → X=2
* PUT X=1, DELETE X → X absent
* DELETE X, PUT X=2 → X=2

Recovery, live index updates, compacted-size calculation and compaction must all use the same semantics.
4. MAX_BYTES
Implement the reviewed policy as a clearly isolated admission component.

* 0 means unlimited.
* Values below 32 are invalid.
* Open must succeed for an existing readable database even if its size exceeds the newly supplied cap.
* A PUT-containing commit must leave the final database within the cap.
* If necessary, use a compacting commit.
* A delete-only commit may shrink a database that was already over the cap, but must never increase its size.
* Standalone compaction must never increase file size.
* If an operation cannot be safely admitted, return ErrFull without changing the committed database.

Add extensive boundary and overflow tests.
Mark this policy as an assumption pending judge clarification. Do not represent it as an official clarification.
5. PROJECT STRUCTURE
Create a clean Go project structure. Suggested starting layout:

```
NEXUS_SRINIVAS/
  go.mod
  README.md
  PROMPTS.md
  BREAKING.md
  docs/
    ARCHITECTURE.md
    FILE_FORMAT.md
    RECOVERY.md
    TEST_PLAN.md
    JUDGE_CLARIFICATIONS.md
  src/
    cmd/
      kvdb/
    internal/
      storage/
      format/
      recovery/
      compaction/
      index/
      admission/
      locking/
      cli/
  tests/
```

You may improve this structure if there is a compelling technical reason. Explain any structural deviation.
6. PHASE 1 — WHAT TO BUILD NOW
Do not try to implement the entire database in one response.
Begin with the foundation:

1. Inspect the repository.
2. Initialize the Go module.
3. Create the directory structure.
4. Create `PROMPTS.md`.
5. Create documented errors and types.
6. Implement the 32-byte superblock encoder/decoder.
7. Implement the 32-byte frame-header encoder/decoder.
8. Implement the 11-byte entry-header encoder/decoder.
9. Implement CRC32C validation.
10. Add unit tests and fuzz tests for the format.
11. Ensure all arithmetic and length validation is safe.
12. Verify the project builds on Windows.

Do not implement compaction, recovery or the full CLI during this first phase unless the foundational work is already complete.
7. TESTING REQUIREMENTS
For Phase 1, test:

* Correct magic bytes.
* Correct little-endian encoding.
* Valid superblock round trip.
* Invalid superblock CRC.
* Invalid version and flags.
* Invalid `snapshot_end`.
* Valid frame-header round trip.
* Invalid frame-header CRC.
* Invalid type and reserved fields.
* Invalid operation counts.
* Payload-length bounds.
* Sequence-number boundaries.
* Entry-header round trip.
* Invalid key lengths.
* Invalid value lengths.
* DELETE entries with nonzero value length.
* CRC32C known test vectors.
* Integer overflow and hostile length inputs.

Run:

```
go test ./...
go test -race ./...
go vet ./...
go build -o .\kvdb.exe .\src\cmd\kvdb
```

If a command is not applicable yet because the CLI has not been implemented, say so explicitly.
8. AI PROMPT LOGGING — MANDATORY
Create `PROMPTS.md` immediately.
Every prompt sent to any AI tool for this project must be recorded there unedited, with:

* Tool name.
* Approximate timestamp.
* Full prompt text.

Do not silently omit prompts or rewrite them.
Record this initial master prompt in the file.
For subsequent prompts, remind me to append them if you cannot reliably do so yourself.
9. DEVELOPMENT RULES

* Use standard Go library wherever practical.
* Do not add dependencies without explaining why.
* Do not use unsafe code without a compelling reason.
* Do not use an existing database or storage engine.
* Never ignore errors.
* Never return success before durability is established.
* Never use a test that merely checks whether a function runs; test correctness.
* Use table-driven tests where useful.
* Add comments explaining non-obvious storage invariants.
* Keep Windows-specific behavior isolated behind platform-specific files.
* Do not prematurely optimize before correctness is proven.
* Keep assumptions separate from official requirements.

10. REQUIRED RESPONSE AFTER PHASE 1
At the end, provide:

1. Files created and modified.
2. Architecture decisions made.
3. Tests implemented.
4. Exact commands executed.
5. Test/build results.
6. Known limitations.
7. Outstanding judge questions.
8. Recommended next implementation phase.

Do not claim the database is functional yet. Phase 1 is only the storage-format foundation.
Start by inspecting the repository and then implement Phase 1.

Claude records this prompt in PROMPTS.md. The prompt log is part of the competition deliverables.
````

### Prompt 1a (answers to Claude's clarifying questions during Prompt 1)

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 12:33 (local time)
- **Notes:** Multiple-choice answers given in the app, recorded verbatim.

```text
Q: I couldn't find a local clone of NEXUS_SRINIVAS (the GitHub repo exists and looks empty), and Go is not installed on this PC. Where should I clone it?
A: C:\Users\MY PC\NEXUS_SRINIVAS (Recommended)

Q: Go isn't installed (needed for go test/vet/build). Install Go 1.27.3 via winget (GoLang.Go, official go.dev package)?
A: Yes, install via winget (Recommended)

Q: (branch proposal) Put the changes on a new branch?
A: New branch named phase1-format
```

### Prompt 1b (message sent while Prompt 1 was running)

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 12:40 (local time)

```text
tell once go is installed
```

### Prompt 2

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 13:00 (local time)
- **Notes:** Pasted with no other text; the continuation of DESIGN.md (sections 8–27), appended to `docs/DESIGN.md`. Full text as sent:

````text
# DESIGN.md — Continuation

## 8. Recovery Architecture

**[REQ]**
- Acknowledged writes must survive process termination.
- Incomplete operations must be completely committed or discarded.
- Recovery must reconstruct the database's latest state.
- Recovery must be repeatable.
- Opening a database produced by our own implementation must not fail.

**[ASSUME]** The append-only log is the authoritative source of truth. The in-memory index is reconstructed during recovery.

Recovery proceeds in this order:

1. Resolve the authoritative database file using the temporary-file authority protocol.
2. Open and validate the 32-byte superblock.
3. Read the file size.
4. Initialize the in-memory index and sequence counter.
5. Scan frames sequentially, starting at offset 32.
6. Validate each frame header and payload.
7. Verify CRC32C checksums.
8. Verify contiguous sequence numbers.
9. Apply entries only after the complete frame has passed validation.
10. Classify any incomplete or damaged tail.
11. Truncate a proven damaged tail when permitted.
12. Sync after truncation.
13. Recompute live and dead byte counters.
14. Mark the database ready.

### 8.1 Frame validation

A frame is accepted only when:

- Its header is valid.
- Its sequence number is correct.
- Its payload length is within the format limit.
- Its payload checksum matches.
- Every entry is structurally valid.
- Every entry checksum matches.
- The operation count matches the decoded entries.

Entries remain provisional until the entire frame passes validation.

A partially written batch must never partially update the index.

### 8.2 Damaged tail versus corruption

The decoder distinguishes three classes:

- **Damaged:** Bad magic, checksum mismatch, or data ending prematurely.
- **Corrupt:** Checksums pass, but the structure is invalid.
- **I/O:** An actual operating-system read error.

Recovery may discard a damaged tail when the damage is outside the protected compacted snapshot.

Recovery must not silently truncate structurally corrupt data whose checksums validate.

A real read error must never be interpreted as EOF and must never cause truncation.

### 8.3 Snapshot boundary

The superblock's `snapshot_end` identifies the end of the protected compacted region.

Recovery must never truncate below this offset.

Damage inside the protected snapshot is corruption, not an incomplete append.

### 8.4 Recovery performance

**[REQ]** Opening a database with one million keys must complete in under two seconds.

Measure:
- File scanning time.
- CRC calculation time.
- Index reconstruction time.
- Total Open duration.
- Peak memory usage.

Use a 4 MiB read buffer initially. Further optimizations may include pre-sizing the index, sequential scan hints and allocation reduction.

Do not disable checksum validation to improve performance.

---

## 9. Sequence Number Management

Every committed frame has a monotonically increasing sequence number.

The superblock stores `first_seq`.

The first frame after the superblock must have sequence `first_seq`.

Each subsequent frame increments the sequence by one.

The next sequence number is maintained in memory as `next_seq`.

### 9.1 Compaction sequence handling

Compaction creates a new log containing the current live state.

The new file's `first_seq` is the old database's `next_seq`.

The compacted frames are assigned contiguous sequence numbers starting from that value.

New writes continue after the compacted frames.

Sequence numbers identify positions in database history, not individual user operations.

### 9.2 Empty database

When compaction removes all live keys:

- The new superblock has `first_seq` equal to the old `next_seq`.
- `snapshot_end` equals 32.
- There are no frames.
- The next write uses `first_seq`.

Compaction is a no-op when the existing database has no frames.

The implementation must guard against sequence-number overflow.

---

## 10. Compaction Architecture

Compaction is mandatory.

The append-only log accumulates obsolete records when keys are overwritten or deleted.

Compaction rewrites only the latest live state into a new database file.

For each key:
- Keep the latest PUT if the key is live.
- Exclude deleted keys.
- Exclude superseded PUT records.

### 10.1 Compaction trigger

Automatic compaction is initially triggered when:

- The file is at least 4 MiB.
- The file is more than twice its estimated compacted size.

A second trigger may be introduced when the file exceeds a benchmark-derived `RECOVERY_SAFE_BYTES` threshold.

### 10.2 Compaction process

1. Acquire the exclusive database lock.
2. Confirm that the database has at least one frame.
3. Calculate the compacted output size.
4. Create the `.compact` file.
5. Write compacted frames.
6. Sync the frames.
7. Write the superblock last.
8. Sync again.
9. Close the temporary file.
10. Close the old data-file handle.
11. Perform the Windows two-rename replacement protocol.
12. Reopen and validate the resulting database.
13. Swap the in-memory index and counters.
14. Remove obsolete temporary files best-effort.

The old index and counters must remain authoritative until the replacement succeeds and the new file has been validated.

### 10.3 Compaction and concurrent operations

Compaction holds the exclusive lock.

No reads or writes may observe an intermediate database state.

All concurrent operations resume after compaction completes.

### 10.4 Compaction correctness

Plain compaction preserves all live key-value pairs.

A compacting commit preserves the old state plus the new commit.

The new state must not be acknowledged until the replacement has succeeded.

---

## 11. Windows File Replacement Protocol

A single replace-existing rename is not accepted as the correctness foundation.

Use two non-overwriting rename operations.

The source and destination must be in the same directory and on the same volume.

### 11.1 Rename sequence

R1:

Move `path` to `path.old`.

R2:

Move `path.compact` to `path`.

R3:

Delete `path.old` best-effort.

Use a Windows operation that fails if the destination already exists.

Do not use an operation that silently replaces the destination.

### 11.2 Before compaction

Delete stale `.compact` and `.old` files.

If either cannot be deleted, abort compaction without changing the authoritative database.

### 11.3 Failure handling

If R1 fails:
- Reopen the original database file.
- Keep the old index.
- Keep the database usable if reopening succeeds.

If R2 fails:
- Attempt to restore `.old` to `path`.
- If restoration succeeds, reopen the old database.
- If restoration cannot be proven successful, mark the handle failed.

Sharing violations may be retried using bounded backoff.

### 11.4 Recovery authority table

Evaluate in this order:

1. If `path` has a valid superblock, use `path`.
2. If `path` is missing and `.compact` is sealed, adopt `.compact`.
3. If `path` is missing and `.old` is valid, restore `.old`.
4. If `path` is missing and `.new` is sealed, adopt `.new`.
5. If both `path` and `.old` are missing, remove unsealed temporary files and create a fresh database.
6. If `path` exists but has an invalid superblock, return `ErrBadFormat`.

The ordering is important.

A valid `path` always takes precedence over stale temporary files.

### 11.5 Crash testing

Inject crashes:
- Before R1.
- During R1.
- Between R1 and R2.
- During R2.
- Between R2 and R3.
- During R3.

Also test manually constructed filename states, including states where neither source nor destination name exists temporarily.

At every point, verify that at least one complete valid database copy survives.

---

## 12. Disk Capacity and max_bytes

`max_bytes` limits database storage according to the admission policy.

- Zero means unlimited.
- Values below 32 are invalid.
- Negative values are invalid.

### 12.1 Normal admission

Before writing a frame, calculate whether it fits.

Avoid unsigned arithmetic overflow.

Check whether the current end offset already exceeds the cap before calculating remaining capacity.

### 12.2 Compaction admission

If an append does not fit, the database may attempt a compacting commit.

The compacting commit writes a snapshot containing the existing live state plus the requested operations.

### 12.3 Approved working policy

Until judges clarify the interpretation:

1. Opening an existing readable database succeeds even if its file is already larger than the newly supplied cap.
2. A commit containing PUT operations must finish with the database file at or below `max_bytes`.
3. A delete-only commit may operate on a database that was already over the cap, but must strictly reduce its size.
4. Standalone compaction must never increase file size.
5. If a write cannot be safely admitted, return `ErrFull` without changing committed data.

This is a design assumption, not an official judge clarification.

### 12.4 Disk-full errors

If an append fails because the disk is full:

1. Attempt to truncate back to the frame start.
2. Sync the truncation.
3. If rollback succeeds and the error is a recognized disk-full condition, return `ErrFull`.
4. Keep the database usable.
5. If rollback cannot be proven successful, mark the handle failed and return `ErrOutcomeUnknown`.

Test Windows disk-full error codes, including 112 and 39.

---

## 13. Error Model

Errors are shared through `src/internal/dberr` and re-exported through the public API.

Expected errors include:

- `ErrNotFound`
- `ErrInvalidArgument`
- `ErrTooLarge`
- `ErrFull`
- `ErrClosed`
- `ErrFailed`
- `ErrOutcomeUnknown`
- `ErrLocked`
- `ErrBadFormat`
- `ErrCorruption`

The exact shared definitions in the implementation remain authoritative.

### 13.1 Error categories

**Definite errors**

The operation did not commit.

Examples:
- Invalid arguments.
- Key not found.
- Database full before any write.
- Database already closed.

**Outcome unknown**

A storage failure occurred after an operation may have reached durable storage, and rollback cannot be confirmed.

The caller must not assume that the operation was committed or discarded.

### 13.2 Failed state

When the database enters FAILED state:

- Reject subsequent operations.
- Permit Close.
- Require a fresh Open to determine the authoritative state.

Never continue using a potentially inconsistent in-memory index.

---

## 14. Public API

The public package is `src/kvdb`.

It exposes the database operations and shared errors.

```go
Open(path string, maxBytes int64) (*DB, error)

(db *DB) Put(key, value []byte) error

(db *DB) Get(key []byte) ([]byte, error)

(db *DB) Delete(key []byte) error

(db *DB) Batch(ops []Op) error

(db *DB) Compact() error

(db *DB) Close() error
```

### 14.1 Input validation

Reject:
- Empty keys.
- Keys longer than 1024 bytes.
- Values larger than 1 MiB.
- Batches larger than 10,000 operations.
- Invalid operation types.
- Nil or malformed requests where prohibited by the API contract.

DELETE of a missing key is not an error.

### 14.2 Slice ownership

Batch encoding uses two passes.

The caller must not modify operation keys or values until Batch returns.

The implementation must document this contract.

GET returns an independent copy of the value, not a mutable view into internal storage.

### 14.3 Batch behavior

All operations in a batch are committed together.

Operations execute in input order.

The last operation on a key determines its final state.

---

## 15. In-Memory Index

The index maps keys to their latest value locations.

It does not store complete values.

An index entry contains sufficient information to locate and validate the corresponding PUT record.

DELETE removes the key from the live index.

Overwritten records become dead log data.

### 15.1 Index update ordering

Normal commit:

1. Write frame.
2. Sync frame.
3. Apply index changes.

Compacting commit:

1. Build new snapshot and index.
2. Complete replacement.
3. Validate reopened file.
4. Swap index.

If a commit fails, do not partially modify the index.

### 15.2 GET validation

GET must:
- Hold a read lock.
- Locate the indexed value.
- Read the stored entry.
- Verify the entry checksum.
- Verify the stored key matches the requested key.
- Return a copied value.

An unexpected short read is corruption.

---

## 16. Concurrency and Locking

Use one global `sync.RWMutex`.

This is intentionally simple and prioritizes correctness.

### Readers

GET acquires a shared lock and holds it through lookup and disk read.

### Writers

PUT, DELETE, BATCH and COMPACT acquire the exclusive lock.

The exclusive lock is held through the durability operation and index update.

### Process-level locking

Use a `.lock` file to prevent multiple processes from opening the same database simultaneously.

If the database is already locked, return `ErrLocked`.

### Concurrency requirements

At least two concurrent writers must be supported.

A single global lock is permitted.

A GET running concurrently with BATCH must observe either the complete old state or the complete new state.

It must never observe a partially applied batch.

---

## 17. File Creation and Open

New database creation uses `.new`.

The file is created and initialized through a temporary-file protocol.

The superblock is written and synced before the file is adopted as the authoritative database.

Recovery must distinguish:
- A completed new database.
- An incomplete new database.
- A previously existing database.
- A database interrupted during compaction.

Open must resolve temporary files before scanning the authoritative data.

A database produced by our implementation must remain recoverable after process termination at any supported crash point.

---

## 18. CLI Specification

The CLI binary is `kvdb`.

Mandatory commands:

- `open`
- `put`
- `get`
- `del`
- `batch`
- `dump`
- `check`
- `serve`

### 18.1 Command behavior

`open`: Open or initialize a database.

`put`: Store a key-value pair.

`get`: Retrieve a value.

`del`: Delete a key.

`batch`: Execute a group of operations atomically.

`dump`: Display database contents.

`check`: Validate the database and report structural integrity.

`serve`: Keep a database open and process multiple requests.

### 18.2 Serve protocol

Serve accepts writer IDs, operation IDs and commands.

Example conceptual request:

`writer-1 op-001 put apple 100`

A successful durable write produces:

`ACK op-001`

An ACK must never be emitted before the durability operation succeeds.

Use buffered output and flush after every ACK.

Protect stdout writes with a mutex.

### 18.3 Concurrent writers

Each writer's operations must preserve their submitted order.

Different writers may execute concurrently.

The database's global lock provides storage-level serialization.

### 18.4 Input parsing

Raise the default scanner limit to support values up to 1 MiB.

Strip trailing CR characters from Windows CRLF input.

For `put key value`, the value is the remainder of the line and may contain spaces.

A PUT with no value after the key stores an empty value.

Batch parsing must validate the operation count before allocating large structures.

---

## 19. PROMPTS.md and AI Usage

Every prompt sent to any AI tool for this project must be logged.

Each entry must include:
- Tool name.
- Approximate timestamp.
- Complete original prompt.

Prompts must not be silently rewritten or omitted.

The team is responsible for logging prompts sent through its own AI tools.

Do not fabricate timestamps or claim that an unlogged prompt was recorded.

---

## 20. Test Architecture

Tests must validate correctness, not merely successful execution.

Use:
- Table-driven unit tests.
- Property tests.
- Fuzz tests.
- Fault injection.
- Process-kill tests.
- Model-based randomized workloads.
- Benchmarks.

### 20.1 Fault injection

Use a `fileOps` abstraction to inject failures into:
- Writes.
- Reads.
- Sync.
- Truncate.
- Rename.
- Close.
- File creation.

Tests must verify the resulting file bytes and in-memory state.

### 20.2 Crash points

Crash-point hooks must be available in test builds.

They must not be included in release binaries.

Test crashes during:
- Normal writes.
- Batch writes.
- Recovery.
- Compaction.
- Rename replacement.
- Temporary-file cleanup.

### 20.3 Fuzzing

Fuzz:
- Superblock decoding.
- Frame-header decoding.
- Entry decoding.
- Streaming frame decoding.
- CLI input parsing.
- Recovery classification.

Accepted input must re-encode consistently.

Every decoder error must have a defined class.

### 20.4 Race testing

Run `go test -race ./...` when a compatible C compiler is available.

Test concurrent GET, PUT and BATCH operations.

---

## 21. Torture Testing

The independent torture tester must generate workloads and terminate database processes at controlled points.

It must verify:
- No acknowledged writes are lost.
- Incomplete batches are fully discarded.
- Recovery is repeatable.
- No partially written values are returned.
- Compaction preserves live state.
- Concurrent writers do not corrupt the log.

Use `Kill()` followed by `Wait()` before restarting the database process.

The torture tool must compare recovered data against an independent reference model.

The reference model is a testing oracle, not a database engine used by the product.

---

## 22. Performance Requirements

Primary target:

Open a database containing one million keys in under two seconds.

Benchmark:

| Dimension | Test values |
|---|---|
| Keys | 100k, 1M |
| Value sizes | 16 B, 100 B, 1 KB, 10 KB |
| Overwrite factor | 1x, 3x |
| Cache | Warm and cold |
| Defender | Enabled and disabled |

Measure:
- Total Open duration.
- Frame scan duration.
- CRC duration.
- Index reconstruction duration.
- Peak memory.
- File size.
- Compaction duration.

Do not present estimates as measured results.

Performance optimizations must preserve checksum validation and commit ordering.

---

## 23. Recovery and Compaction Acceptance Tests

Mandatory scenarios include:

1. Create an empty database and reopen it.
2. Commit one PUT and reopen.
3. Commit multiple PUTs and reopen.
4. Delete a key and reopen.
5. Crash during an incomplete PUT.
6. Crash during an incomplete batch.
7. Recover repeatedly.
8. Compact a database containing live keys.
9. Compact a database containing no live keys.
10. Kill the process before R1.
11. Kill between R1 and R2.
12. Kill between R2 and R3.
13. Kill during recovery.
14. Reopen after interrupted compaction.
15. Reopen using a smaller max_bytes value.
16. Verify that a full database remains readable.
17. Verify that PUT returns ErrFull without changing committed data.
18. Verify that DELETE reduces an already oversized database.
19. Run one million PUTs followed by one million DELETEs and verify that compaction reduces the file size.

All results must be recorded.

---

## 24. Documentation and Submission

The repository must contain:

- `README.md`
- `PROMPTS.md`
- `BREAKING.md`
- `docs/DESIGN.md`
- `docs/ARCHITECTURE.md`
- `docs/FILE_FORMAT.md`
- `docs/RECOVERY.md`
- `docs/TEST_PLAN.md`
- `docs/JUDGE_CLARIFICATIONS.md`

The documentation must distinguish:
- Official requirements.
- Design assumptions.
- Optional optimizations.
- Measured results.
- Known limitations.

`BREAKING.md` must document known weaknesses and evidence from attempts to break the database.

Architecture diagrams must match the actual implementation.

Do not claim unimplemented or untested features are complete.

---

## 25. Known Risks and Unresolved Questions

### Windows rename behavior

The two-rename protocol avoids replacing an existing target.

The design assumes a rename affects only its source and destination names.

Power-loss durability on Windows is not claimed.

### Antivirus and file locking

Windows Defender or other software may temporarily hold file handles.

Use bounded retries and test on the judging machine.

### Recovery time

Large historical logs may exceed the two-second target.

Automatic compaction must control historical growth.

### Large batches

The maximum theoretical batch can be approximately 10.5 GB.

Streaming avoids requiring the entire encoded frame to fit in memory.

### max_bytes

The exact treatment of temporary compaction files remains unconfirmed.

### Race detector

The current development machine does not have a C compiler configured for the race detector.

This must be resolved before relying on concurrency testing.

---

## 26. Implementation Roadmap

### Phase 1 — Format foundation

Completed work:
- Go module.
- Binary format.
- Superblock codec.
- Frame codec.
- Entry codec.
- CRC32C.
- Shared errors.
- Unit tests.
- Fuzz tests.
- Format documentation.

### Phase 2 — Core database

Implement:
1. File abstraction and fault injection.
2. In-memory index.
3. Process-level locking.
4. Temporary-file database creation.
5. Recovery scanner.
6. Damaged-tail classification.
7. Open.
8. Put.
9. Get.
10. Delete.
11. Batch.
12. Close.

Prove commit ordering and recovery before moving forward.

### Phase 3 — Compaction and admission

Implement:
1. Compacted snapshot generation.
2. Windows two-rename protocol.
3. Temporary-file authority recovery.
4. Compacting commits.
5. max_bytes admission.
6. Automatic compaction.
7. Interrupted-compaction testing.

### Phase 4 — CLI and testing

Implement:
1. All eight CLI commands.
2. Serve protocol.
3. Concurrent writers.
4. Independent torture tester.
5. Crash injection.
6. Model-based testing.
7. Benchmarks.
8. Final documentation.

The exact order may be adjusted when test results expose dependencies.

---

## 27. Final Design Invariants

The following invariants must hold throughout implementation.

1. The append-only log is authoritative.
2. The in-memory index is derived from committed frames.
3. No operation is acknowledged before durability is established.
4. A batch is one atomic frame.
5. A frame is applied only after complete validation.
6. Sequence numbers are contiguous.
7. Recovery never truncates below `snapshot_end`.
8. Real I/O errors never trigger tail truncation.
9. Compaction never loses the only valid database copy.
10. The old index remains authoritative until a replacement is validated.
11. GET never observes a partially applied batch.
12. A failed handle rejects operations other than Close.
13. max_bytes admission must not silently overflow.
14. Windows file replacement must use the non-overwriting two-rename protocol.
15. Every important crash guarantee must have a corresponding test.
16. All AI prompts must be recorded in PROMPTS.md.
17. Official requirements must never be confused with design assumptions.

**This document is the design baseline. Changes must be documented and reviewed before implementation deviates from these invariants.**
````

### Prompt 2a (reply to Claude's "Commit Phase 1 plus the design update?" question)

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 13:10 (local time)

```text
Not yet
```

### Prompt 3

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 13:15 (local time)
- **Notes:** Pasted with no other text. (A local `/autocompact 400000` settings command was run just before it; that command configures the tool and is not a prompt, so it is only noted here.)

````text
Review the latest recovery and Windows-specific design findings.
Implement the documentation and design corrections before Phase 2.
Required changes:

1. Process-level locking must happen before any recovery authority resolution, rename, deletion or temporary-file cleanup.
2. A valid-header frame with a bad payload checksum may only be truncated when its declared end is exactly EOF and it is outside the protected snapshot.
3. A damaged frame header is treated as the final torn frame. Do not scan forward searching for another possible frame header.
4. Explicitly document the invariant that only one uncommitted frame can exist and it must be the final frame.
5. Update the Windows file replacement protocol to use syscall.MoveFile, not os.Rename.
6. Document the exclusive-sharing lock-file implementation and its limitations.
7. Keep Windows error codes isolated in Windows-specific files.
8. Update the authority table with the agreed ordering and cleanup behavior.
9. Ensure ErrBadFormat and ErrCorruption have distinct documented meanings.
10. Cross-check all sections against the Phase 1 implementation.

Add tests or test specifications for every change.
Do not start Phase 2 yet. Do not commit or push automatically.
At the end, report any contradictions, unresolved risks and remaining judge questions.
````

### Prompt 3a (message about a Supercharge AI screenshot)

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 13:40 (local time)
- **Notes:** The message contained only a screenshot of the Supercharge AI start screen showing: "inside a git repository. Navigate to a git repo or run 'git init' first." No text was typed.

### Prompt 4

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 14:00 (local time)
- **Notes:** Pasted with no other text. Full text as sent:

````text
We are continuing development of NEXUS_SRINIVAS.
Supercharge AI is being temporarily excluded. You are the primary implementation assistant.
IMPORTANT PROJECT STATE:

* Repository: C:\Users\MY PC\NEXUS_SRINIVAS
* Branch: phase1-format
* Phase 1 commit: ee4b5d6
* Go version: 1.27.3
* Target OS: Windows 64-bit
* Phase 1 binary format, tests and documentation are complete.

FIRST:

1. Verify the repository path and Git branch.
2. Check git status and preserve any existing changes.
3. Verify commit ee4b5d6.
4. Read the COMPLETE docs/DESIGN.md, including sections 8–28.
5. Read docs/RECOVERY.md, ARCHITECTURE.md, FILE_FORMAT.md, TEST_PLAN.md and JUDGE_CLARIFICATIONS.md.
6. Inspect the existing Phase 1 source code before modifying anything.

NOW BEGIN PHASE 2 — STAGE 1 ONLY.
Implement:
A. File abstraction

* A small interface for database file operations.
* Read, write, sync, truncate, stat and close operations as required by the design.
* Fault injection support for tests.
* Ability to simulate short writes and real I/O errors.

B. Windows process locking

* Implement the approved exclusive-sharing lock-file approach.
* Keep Windows-specific functionality inside src/internal/platform.
* Classify sharing violations correctly.
* Return ErrLocked after the documented bounded retries.
* Never perform recovery cleanup before acquiring the lock.

C. Safe database creation

* Implement creation through the .new temporary file.
* Follow the approved authority rules in DESIGN.md.
* Never overwrite or delete the only valid database copy.
* Ensure the file is sealed and synced according to the design.

STRICT RULES:

* Do not change the binary file format.
* Do not weaken any recovery invariant.
* Do not implement compaction yet.
* Do not implement max_bytes yet.
* Do not implement the complete recovery scanner yet.
* Do not silently change public API signatures.
* Keep Windows-specific code isolated.
* Use only the Go standard library unless the design explicitly approves otherwise.
* Append this prompt and every subsequent AI prompt to PROMPTS.md exactly as required.

TESTING:

* Add unit tests for the file abstraction.
* Add fault-injection tests.
* Test lock contention between processes where practical.
* Test database creation and interrupted creation.
* Run gofmt.
* Run go vet ./...
* Run go test ./...
* Run Windows build and relevant tests.

WORKFLOW:
Implement Stage 1 in small steps.
After each step, report:

1. Files created or modified.
2. Design sections followed.
3. Tests added and results.
4. Unresolved issues.
5. Next step.

Do not commit, push, reset or discard any work.
STOP after Stage 1 and wait for my review before beginning Stage 2.
Prioritize correctness, crash safety and testability over speed.
````

### Prompt 4a

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 (local time), during Phase 2 Stage 1
- **Notes:** Sent after interrupting a tool call (a vet run for several OS targets). Full text as sent:

````text
continue
````

### Prompt 5

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 (local time), after the Phase 2 Stage 1 report
- **Notes:** Sent by clicking the "Approve, start Stage 2" button, which answered Claude's question "Do you approve design notes D1–D3 so I can start Stage 2?". The Stage 1 report had named Stage 2 as "the recovery scanner and tail rules C0–C8". Full text as sent:

````text
Approve, start Stage 2
````

### Prompt 6

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 (local time), after the Phase 2 Stage 2 report
- **Notes:** Sent by clicking the "Approve, start Stage 3" button, which answered Claude's question "Do you approve notes E1–E4 so I can start Stage 3 (Put/Get/Delete/Batch/Close)?". Full text as sent:

````text
Approve, start Stage 3
````

### Prompt 7

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 (local time), after the Phase 2 Stage 3 report
- **Notes:** Pasted with no other text. Full text as sent:

````text
I approve F1–F5.
For F1, please ensure the following invariants are explicitly documented and tested:

1. A failed Sync returns ErrOutcomeUnknown, never a definite success or definite absence.
2. The handle enters FAILED and accepts no further operations except Close.
3. The database does not automatically retry an operation with an unknown outcome.
4. After reopening, the operation may be fully present or fully absent, but never partially applied.
5. A failed rollback/truncate must not accidentally turn an unknown outcome into a definite failure classification.

Proceed to Phase 3: maxBytes enforcement and crash-safe compaction.
Before implementation, review the compaction authority table, replacement protocol, Windows rename behaviour and maxBytes semantics. Preserve all existing invariants and tests.
Do not commit or push. Keep PROMPTS.md updated, run the full test suite and stop for review when Phase 3 is complete. Report unresolved risks separately.
````
