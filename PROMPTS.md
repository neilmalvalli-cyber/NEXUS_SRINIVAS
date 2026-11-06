# AI Prompt Log

Every prompt sent to any AI tool for this project is recorded here **unedited**, in
chronological order, with the tool name and an approximate timestamp (local time,
IST = UTC+5:30). Timestamps of the implementation prompts were checked against the
Claude Code session transcript on 2026-11-06 23:30 and corrected where the earlier
estimate was off.
Missing entries cost points (challenge rule §9), so append every new prompt before
committing work produced from it.

Prompts that produced `docs/DESIGN.md` (the v1 → v5 architecture review) were sent
before this repository existed; append them below (in the section "Design-phase
prompts") if they have not been recorded yet.

---

## Design-phase prompts

_TODO (team): paste the design-review prompts that produced `docs/DESIGN.md` here,
unedited, with tool name and timestamp._

**Status (checked 2026-11-06 23:30): not recorded yet.** These prompts (the v1 → v5
architecture review that produced the first part of `docs/DESIGN.md`, sections 1–7,
brought into this session as an attachment with Prompt 1) were sent in a different
tool before this repository existed. No copy exists on the development machine: every
local Claude Code transcript was searched and none contains them. They are not
reconstructed here, because this log must be unedited. Only the team member who sent
them can paste them, from that tool's chat history. Sections 8–27 of `docs/DESIGN.md`
are recorded verbatim below as Prompt 2.

---

## Implementation prompts

### Prompt 1

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 12:33 (local time; 07:03 UTC, from the session transcript)
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
- **Approximate timestamp:** 2026-11-06 12:34 (local time; 07:04 UTC, from the session transcript)
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
- **Approximate timestamp:** 2026-11-06 12:38 (local time; 07:08 UTC, from the session transcript)

```text
tell once go is installed
```

### Prompt 2

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 12:50 (local time; 07:19 UTC, from the session transcript)
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
- **Approximate timestamp:** 2026-11-06 12:54 (local time; 07:23 UTC, from the session transcript)

```text
Not yet
```

### Prompt 3

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 12:55 (local time; 07:25 UTC, from the session transcript)
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
- **Approximate timestamp:** 2026-11-06 13:07 (local time; 07:37 UTC, from the session transcript)
- **Notes:** The message contained only a screenshot of the Supercharge AI start screen showing: "inside a git repository. Navigate to a git repo or run 'git init' first." No text was typed.

### Prompt 4

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 13:18 (local time; 07:48 UTC, from the session transcript)
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
- **Approximate timestamp:** 2026-11-06 14:02 (local time; 08:31 UTC, from the session transcript), during Phase 2 Stage 1; the tool call it followed was interrupted at 13:24
- **Notes:** Sent after interrupting a tool call (a vet run for several OS targets). Full text as sent:

````text
continue
````

### Prompt 5

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 14:13 (local time; 08:42 UTC, from the session transcript), after the Phase 2 Stage 1 report
- **Notes:** Sent by clicking the "Approve, start Stage 2" button, which answered Claude's question "Do you approve design notes D1–D3 so I can start Stage 2?". The Stage 1 report had named Stage 2 as "the recovery scanner and tail rules C0–C8". Full text as sent:

````text
Approve, start Stage 2
````

### Prompt 6

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 14:26 (local time; 08:56 UTC, from the session transcript), after the Phase 2 Stage 2 report
- **Notes:** Sent by clicking the "Approve, start Stage 3" button, which answered Claude's question "Do you approve notes E1–E4 so I can start Stage 3 (Put/Get/Delete/Batch/Close)?". Full text as sent:

````text
Approve, start Stage 3
````

### Prompt 7

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 14:37 (local time; 09:06 UTC, from the session transcript), after the Phase 2 Stage 3 report
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

### Prompt 8

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 15:05 (local time; 09:34 UTC, from the session transcript), after the user committed Phase 3 as 5c9eb13
- **Notes:** Pasted with no other text. Full text as sent:

````text
NEXUS — PHASE 4: CLI, TORTURE TESTING & FINAL SUBMISSION
You are working on the NEXUS repository:
`C:\Users\MY PC\NEXUS_SRINIVAS`
NEXUS is a custom crash-safe key-value database written in Go.
1. Current project status
Phases 1–3 are COMPLETE and committed.
Latest commit:
`9eb13 — Complete Phase 3: compaction and maxBytes enforcement`
Branch:
`phase1-format`
The working tree was clean after the Phase 3 commit.
Implemented capabilities:

* Custom binary database format.
* Superblock, frame and entry encoding.
* CRC32C checksums.
* Sequence-number validation.
* Append-only storage engine.
* In-memory hash index.
* Recovery and tail handling.
* Atomic batches.
* Crash-safe writes and Sync-before-ACK.
* Rollback and FAILED-state handling.
* Windows process locking.
* Safe creation through `.new`.
* Fault injection and simulated crash infrastructure.
* `Open`, `Put`, `Get`, `Delete`, `Batch`, `Compact`, `Stats` and `Close`.
* Crash-safe compaction.
* Automatic compaction.
* `maxBytes` admission and enforcement.
* Compacting commits.

Phase 3 has passed the existing test suite, gofmt, vet and Windows build.
Do not rewrite or weaken any of these implementations.
2. Your task
Implement Phase 4 completely.
This phase covers:

1. Mandatory CLI.
2. Torture-testing tool.
3. Official crash scenarios.
4. BREAKING.md.
5. Final documentation and architecture proof.
6. Acceptance testing.

Before coding, inspect the entire repository, DESIGN.md, TEST_PLAN.md, ARCHITECTURE.md, README.md, PROMPTS.md and the original problem statement if available.
Use the official problem statement as the source of truth.
Distinguish:

* [REQ] Official requirements.
* [ASSUME] Explicit design assumptions.
* [OPT] Optional improvements.

Do not invent requirements.
3. Mandatory CLI
Implement these commands:

* `open`
* `put`
* `get`
* `del`
* `batch`
* `dump`
* `check`
* `serve`

Use a consistent command-line interface with clear usage instructions and error messages.
Requirements:
open
Open or create a database at the requested path.
put
Insert or update a key-value pair.
get
Retrieve a value and return a clear not-found response when absent.
del
Delete a key. Deleting a nonexistent key must not be treated as an error.
batch
Accept multiple operations and submit them through the atomic Batch API.
Preserve operation ordering, including repeated operations on the same key.
dump
Display the database contents in a useful and deterministic format.
check
Validate database integrity and report corruption or other failures accurately.
serve
Implement the required serving interface.
CRITICAL:
Print `ACK <op-id>` ONLY after the corresponding operation has successfully completed its durability requirement.
Never print ACK before Sync succeeds.
If an operation returns ErrOutcomeUnknown, do not print ACK.
Support multiple concurrent writers while preserving per-writer ordering.
Do not invent an incompatible protocol. Check the original problem statement for the expected input format.
4. Torture-testing tool
Implement the competition-required torture-testing tool.
It must:

* Exercise the database through its supported interface.
* Generate realistic PUT, GET, DELETE and BATCH workloads.
* Include concurrent writers.
* Support crash injection.
* Kill and restart database processes.
* Reopen and verify database consistency.
* Detect partial batches.
* Detect missing acknowledged operations.
* Detect corrupted values.
* Detect invalid recovery behaviour.
* Exercise compaction.
* Exercise maxBytes.
* Produce clear machine-readable and human-readable results.

The tool must not simply trust the database's own recovery output.
Maintain an independent reference model or operation history so it can verify the recovered state.
Where practical, store operation IDs, expected values and acknowledged operations independently of the database process.
The tool should distinguish:

* Definitely committed operations.
* Definitely rejected operations.
* Operations with unknown outcomes.

Unknown outcomes may be fully present or fully absent, but must never be partially applied.
5. Official crash scenarios
Inspect the original problem statement and TEST_PLAN.md.
Implement all 32 official crash scenarios across groups A–F.
Do not approximate the scenarios or silently omit difficult ones.
For each scenario:

1. Explain what is being tested.
2. Execute the relevant crash or fault.
3. Restart/reopen the database.
4. Verify the expected state.
5. Report PASS or FAIL.
6. Record useful diagnostic information.

If the official test environment requires a particular interface or invocation, follow it exactly.
Also implement the required `compaction` mode.
Ensure the torture tool can be run repeatedly and deterministically where applicable.
6. BREAKING.md
Create a meaningful BREAKING.md containing technical failure scenarios.
It must explain:

* What operation or failure is being attempted.
* Why the scenario threatens database consistency.
* What a broken implementation might do.
* How NEXUS handles the scenario.
* What test demonstrates the behaviour.
* What the expected result is.

Include examples involving:

* Partial frame writes.
* Failed Sync.
* Interrupted batch.
* Damaged frame headers.
* Invalid payload checksums.
* Process termination.
* Concurrent writers.
* Two processes attempting to open the same database.
* Disk-full conditions.
* Compaction interrupted between file moves.
* `.new`, `.compact` and `.old` recovery.
* maxBytes exhaustion.
* Recovery after an interrupted compacting commit.

Do not claim tests passed unless they were actually executed.
7. Documentation
Update:
README.md
Include:

* Project overview.
* Architecture summary.
* Build instructions.
* Run instructions.
* CLI usage and examples.
* API overview.
* Test instructions.
* Current limitations.

ARCHITECTURE.md
Explain:

* Binary format.
* Append-only log.
* In-memory index.
* Write path.
* Sync and ACK ordering.
* Recovery.
* Batch atomicity.
* Compaction.
* maxBytes.
* Locking.
* Error model.

Include a clear architecture diagram, preferably Mermaid.
TEST_PLAN.md
Map all official requirements and crash scenarios to their corresponding tests.
PROMPTS.md
Every prompt sent to any AI tool must be logged unedited, with tool name and approximate time.
Log this complete instruction as the next prompt.
Do not fabricate historical prompts or times.
8. Performance and acceptance requirements
Explicitly test and report:

1. Recovery of one million keys under the two-second target.
2. One million PUT operations followed by one million DELETE operations.
3. Database file shrinking after compaction.
4. Successful writes after compaction.
5. maxBytes enforcement.
6. A batch with up to 10,000 operations, subject to the official requirements.
7. Values up to 1 MB.
8. Concurrent writers.
9. Process termination during writes.
10. Process termination during compaction.
11. Every official crash scenario.
12. `serve` ACK durability.

Do not substitute a small benchmark for the official workload.
Record actual:

* Hardware/environment where available.
* Runtime.
* Memory usage where practical.
* Database file size before and after compaction.
* Number of operations.
* PASS/FAIL.

If an acceptance requirement cannot be run, state that clearly.
9. Windows compatibility
The judging machine runs Windows.
Windows is the primary target.
Verify:

* Windows build.
* Windows process termination tests.
* Windows locking.
* Windows file move and replacement behaviour.
* File handles are closed before moves.
* Antivirus/sharing violations are handled according to the existing bounded retry policy.

Do not claim Windows power-loss durability. The existing design explicitly does not guarantee it.
Linux, macOS and FreeBSD compilation and tests should be preserved where practical.
10. Strict engineering constraints

* Do not use SQLite, RocksDB, LevelDB or another database engine.
* Do not replace the custom storage engine.
* Do not weaken existing tests.
* Do not remove fault-injection coverage.
* Do not silently ignore errors.
* Do not acknowledge writes before Sync.
* Do not change the binary format without a demonstrated correctness reason.
* Do not change existing public APIs unnecessarily.
* Do not delete potentially authoritative database files during recovery.
* Preserve the A1–A7 authority rules.
* Preserve F1 invariants.
* Preserve G1–G8 decisions.
* Preserve the maxBytes admission rules.
* Keep all AI prompts in PROMPTS.md.

If a genuine design issue requires changing an existing invariant, stop and explain the issue before changing it.
11. Git instructions
The user manages Git commits manually.
Therefore:

* Do not commit.
* Do not push.
* Do not reset.
* Do not rebase.
* Do not amend existing commits.
* Do not discard user changes.
* Do not switch branches without permission.
* Do not create unnecessary worktrees.

Work on the current branch and preserve the existing Phase 1–3 commits.
12. Final review report
When Phase 4 is complete, STOP and provide a report containing:

1. All files created or modified.
2. All eight CLI commands and their status.
3. Torture tool architecture and usage.
4. The 32 official crash scenarios and individual results.
5. Compaction-mode results.
6. BREAKING.md coverage.
7. Documentation completed.
8. Test suite results.
9. Windows build results.
10. One-million-key recovery timing.
11. One-million-PUT / one-million-DELETE compaction results.
12. maxBytes results.
13. Remaining risks.
14. Any requirements not completed.
15. Exact Git status and current branch.

Do not claim Phase 4 is complete if mandatory requirements remain unimplemented or untested.
Stop for my review before making any commit or push.
Begin by inspecting the repository and official requirements. Then implement Phase 4 in small, testable stages.
````

### Prompt 9

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 18:45 (local time; 13:15 UTC in the session transcript)
- **Notes:** Pasted with no other text. Recovered verbatim from the Claude Code session transcript (`~/.claude/projects/.../5535522f-....jsonl`, the user message at 13:15:32 UTC), not reconstructed. Produced the integration and competition-readiness audit (no code changes). Full text as sent:

````text
# NEXUS — COMPLETE INTEGRATION & COMPETITION READINESS AUDIT

You are acting as a senior database systems engineer, storage-engine reviewer, reliability engineer, and competitive programming project auditor.

I have completed my main database engine, NEXUS, and have now received access to two teammates' repositories:

1. **Main database engine (my repository)**
   - Local path: `C:\Users\MY PC\NEXUS_SRINIVAS`
   - Language: Go
   - Windows is the primary judging environment.
   - All four development phases are implemented, tested and committed.

2. **Torture tester**
   - Repository: https://github.com/rohitahkumar/Torture-Tool-Two-Pointer-
   - Owner: rohitahkumar
   - Repository name: Torture-Tool-Two-Pointer-
   - Screenshot shows `seeds/`, `src/`, `tests/`, `ARCHITECTURE.md`, `PROMPTS.md`, and a Phase 3.1 oracle-hardening commit.

3. **Recovery and compaction**
   - Repository: https://github.com/sharanyashetty013-droid/kipdb-recovery
   - Owner: sharanyashetty013-droid
   - Repository name: kipdb-recovery
   - Screenshot shows `docs/`, `recovery/`, `go.mod`, and commits concerning frame recovery, torn-tail handling, and crash-safe compaction.

## CRITICAL INSTRUCTIONS

This is an AUDIT FIRST task.

**DO NOT start integrating, modifying, merging, or rewriting anything yet.**

Do not:
- Commit or push.
- Reset, rebase, amend or switch branches.
- Overwrite existing files.
- Delete existing work.
- Modify the main database engine.
- Modify the frontend.
- Assume that a repository is correct because its README claims it works.
- Assume that tests pass because a commit message says they passed.

Preserve all three repositories and their existing commit histories.

If you cannot access a repository, tell me exactly what access or authentication is missing. Do not pretend to have inspected it.

Your task is to investigate, test, compare and report.

---

# PART 1 — UNDERSTAND THE MAIN NEXUS ENGINE

Start by inspecting the main NEXUS repository.

Read:
- README.md
- ARCHITECTURE.md
- BREAKING.md
- TEST_PLAN.md, if present
- PROMPTS.md
- All source code related to storage, recovery, compaction, format, locking, admission and the CLI.
- All relevant tests.

Understand the exact implementation, not just the documentation.

Establish:

1. Binary file format.
2. Superblock layout.
3. Frame header and entry layout.
4. Checksums and validation.
5. Sequence-number handling.
6. Append-only log behaviour.
7. In-memory hash index.
8. PUT, GET, DELETE and BATCH implementation.
9. Sync-before-ACK durability guarantees.
10. Recovery and torn-tail handling.
11. Compaction protocol and crash safety.
12. max_bytes admission and ErrFull behaviour.
13. Windows process locking.
14. Error handling, including ErrOutcomeUnknown.
15. CLI and torture-test integration interfaces.

Document the actual APIs, binary format and expected behaviour.

Treat the main NEXUS engine as the authoritative implementation unless a demonstrable bug is discovered.

# PART 2 — AUDIT THE TORTURE TESTER

This is the highest-priority part of the audit.

The torture tester carries 15 competition points. I want to make it substantially stronger than an ordinary random-operation testing script.

Inspect the entire repository:
- Source code.
- Tests.
- Seed files.
- Architecture documentation.
- Oracle implementation.
- Crash injection implementation.
- Test runner.
- Reporting.
- Commit history.

Do not simply summarise what the author claims.

Determine what the code actually does.

## A. Independent reference model

Investigate whether the tester maintains an independent reference database/model.

It must be capable of tracking the expected state of the database independently of NEXUS.

Check:
- PUT behaviour.
- GET behaviour.
- DELETE behaviour.
- Repeated writes to the same key.
- Missing-key deletion.
- Batch ordering.
- Atomic batches.
- Multiple concurrent writers.
- Operation IDs.
- Acknowledged versus unacknowledged operations.
- Recovery after process termination.

Look for oracle weaknesses.

Could the tester accidentally agree with an incorrect database implementation?

Could the tester fail to detect lost acknowledged writes?

Could it incorrectly accept partially applied batches?

Could it report false positives or false negatives?

Evaluate the oracle's independence and correctness.

## B. Official 32 crash scenarios

The competition requires 32 official crash scenarios across groups A–F.

Find the official scenario definitions in the project documentation and compare them against the actual torture tester.

Create a table containing:

| Scenario | Official requirement | Implemented? | Test location | What it verifies | Weakness |
|---|---|---|---|---|---|

Verify all 32 scenarios individually.

Do not mark a scenario complete simply because a test has a similar name.

Determine whether it genuinely exercises the required failure point and validates the expected result.

Identify:
- Missing scenarios.
- Duplicate scenarios.
- Scenarios that don't actually crash the process.
- Scenarios that only simulate errors.
- Scenarios with insufficient post-recovery verification.
- Scenarios that don't verify acknowledged-write survival.
- Scenarios that don't check atomicity.
- Scenarios that don't test the actual NEXUS executable.

## C. Crash injection quality

Inspect how crashes are injected.

Determine whether the tester supports:
- Crash before a write.
- Crash after a write.
- Crash during a partial write.
- Crash after a successful sync.
- Crash during batch processing.
- Crash during compaction.
- Crash during file renames.
- Crash during recovery.
- Restart and verification.

Distinguish carefully between:
1. Injected I/O errors.
2. Simulated process crashes.
3. Actual operating-system process termination.

Determine which are implemented and which are missing.

Evaluate whether the crash mechanism accurately models the competition's process-kill assumptions.

## D. Torture workload quality

Investigate:
- Random operation generation.
- Reproducible seeds.
- Workload size.
- Key and value sizes.
- Maximum value size of 1 MB.
- Batch size up to 10,000 operations.
- Concurrent writer behaviour.
- Long-running workloads.
- Delete-heavy workloads.
- Compaction workloads.
- max_bytes exhaustion.
- Recovery after repeated restarts.

Can a failure be reproduced from a seed?

Can a failed run be replayed exactly?

Does the tester save enough information to reproduce the failure?

## E. Reporting and judge-facing output

Evaluate the current report format.

I want the torture tester to produce a professional, easy-to-understand report showing:

- Total scenarios.
- Passed scenarios.
- Failed scenarios.
- Skipped scenarios.
- Operations executed.
- Crashes injected.
- Recovery cycles.
- Acknowledged operations checked.
- Lost acknowledged operations.
- Atomicity violations.
- Oracle mismatches.
- Compaction verification.
- Final database consistency.
- Reproducible failure seed.
- Execution time.

Only report metrics that are genuinely measured.

Identify what the current implementation already reports and what would need to be added.

Recommend a concise judge-facing summary and a detailed machine-readable report, such as JSON, if appropriate.

## F. Torture tester scoring improvements

The objective is to maximise the 15-point torture-tool score.

Rank improvements by:
- Competition scoring impact.
- Reliability improvement.
- Implementation complexity.
- Risk of introducing bugs.
- Time required.

Give me the five highest-value improvements.

Do not recommend cosmetic features ahead of correctness.

---

# PART 3 — AUDIT THE RECOVERY REPOSITORY

Inspect the recovery repository's complete implementation.

Determine exactly what the teammate has built.

Investigate:
- Binary frame scanning.
- Header validation.
- Entry validation.
- CRC32C verification.
- Sequence-number checks.
- Torn-tail detection.
- Middle-of-file corruption handling.
- Truncation rules.
- Snapshot boundary handling.
- Superblock validation.
- Compaction.
- `.compact`, `.old` and `.new` file handling, if present.
- Crash-safe file replacement.
- Windows-specific behaviour.
- Directory syncing.
- Recovery performance.
- Error classification.

Compare it with the recovery and compaction implementation already present in NEXUS.

For each difference, classify it as:

1. Better implementation.
2. Equivalent implementation.
3. Missing functionality.
4. Incompatible implementation.
5. Potential bug.
6. Unnecessary duplication.

Pay special attention to whether this repository implements an alternative recovery engine or reusable functionality that can safely be integrated.

Do not merge two recovery implementations merely because they have similar names.

Determine which implementation is safer and why.

---

# PART 4 — INTEGRATION COMPATIBILITY

This is extremely important.

Compare the actual binary formats and APIs across all three repositories.

Create a compatibility matrix:

| Feature | NEXUS | Recovery repo | Torture tester | Compatible? |
|---|---|---|---|---|

Check:
- Magic numbers.
- Version numbers.
- Superblock size.
- Frame header size.
- Entry header size.
- Byte order.
- CRC32C algorithm.
- CRC coverage.
- Frame types.
- Operation types.
- Sequence numbering.
- Batch encoding.
- Maximum frame size.
- Snapshot boundaries.
- File naming.
- Recovery semantics.
- CLI arguments.
- Exit codes.
- Output formats.

**Do not assume that two implementations are compatible just because both use append-only logs and CRC32C.**

Identify every format mismatch.

Determine whether the torture tester launches the actual NEXUS binary or tests a separate implementation.

Determine whether recovery code can be integrated without changing the on-disk format.

---

# PART 5 — OFFICIAL COMPETITION REQUIREMENTS AUDIT

Compare all three repositories against the official competition requirements already provided.

At minimum, verify:

- PUT.
- GET.
- DELETE.
- BATCH.
- OPEN.
- CLOSE.
- Crash durability.
- Atomic batch behaviour.
- Recovery after process kills.
- No partial or corrupted values.
- 1 million keys.
- Recovery under 2 seconds.
- Values up to 1 MB.
- Batches up to 10,000 operations.
- Concurrent writers.
- Compaction.
- File shrinkage after 1 million puts followed by 1 million deletes.
- max_bytes.
- Clean ErrFull behaviour.
- Writes after compaction.
- Mandatory CLI commands.
- Serve and ACK semantics.
- 32 crash scenarios.
- Torture tester.
- BREAKING.md.
- Architecture documentation.
- PROMPTS.md.

Classify every requirement:

- PASS — verified by actual tests.
- PARTIAL — implemented but inadequately tested.
- FAIL — missing or incorrect.
- UNKNOWN — insufficient evidence.

Do not mark documentation as proof of functionality.

---

# PART 6 — TEST EVERYTHING

Run appropriate tests in each repository where possible.

Inspect the available Go versions and dependencies first.

Run:
- go test ./...
- go vet ./...
- go build ./...
- Relevant race tests if supported.
- Existing torture tests.
- Existing crash/recovery tests.

Use Windows as the primary compatibility target.

If a test requires Linux, macOS or special privileges, identify that limitation.

For the torture tester, run a small reproducible workload first, then the full suite if safe.

Do not run destructive tests against the only copy of the main NEXUS database.

Use disposable test directories.

Record exact commands and actual output.

Do not claim tests passed unless they were executed successfully.

---

# PART 7 — WHAT IS LEFT TO BUILD?

After the audit, create a detailed remaining-work list.

Separate it into:

### P0 — Critical
Bugs or incompatibilities that could cause lost data, incorrect recovery, false torture-test results, or failure to satisfy mandatory requirements.

### P1 — High-value
Improvements that could significantly improve competition scoring, especially torture testing and crash verification.

### P2 — Useful
Performance, reporting, reproducibility and usability improvements.

### P3 — Optional
Cosmetic or additional features that do not materially improve scoring.

For every item include:
- What needs to change.
- Why it matters.
- Which repository/file it affects.
- How to implement it.
- How to test it.
- Risk level.
- Estimated effort.

---

# PART 8 — INTEGRATION PLAN

Propose a safe integration plan.

Determine:
1. Which code should be retained from NEXUS.
2. Which recovery code, if any, should be ported.
3. Which torture tester code should be retained.
4. What interfaces need adapters.
5. What documentation needs updating.
6. What tests must be rerun after integration.

Give an exact recommended order of integration.

Prefer minimal, isolated changes.

Do not overwrite a working implementation with an unverified teammate implementation.

Do not cherry-pick or merge anything yet.

---

# PART 9 — FINAL REPORT

Present the findings in this structure:

## 1. Executive Summary
Is NEXUS ready for judging?

## 2. Repository Inventory
What each teammate actually built.

## 3. Torture Tester Audit
Detailed findings, 32-scenario matrix, oracle quality and scoring improvements.

## 4. Recovery Audit
What is implemented and whether it is better, equivalent or incompatible with NEXUS.

## 5. Integration Compatibility
Exact format and API mismatches.

## 6. Official Requirements Scorecard
PASS / PARTIAL / FAIL / UNKNOWN.

## 7. Test Results
Exact commands, results and limitations.

## 8. Critical Bugs and Risks
Ranked by severity.

## 9. Recommended Improvements
Prioritise torture tester and crash durability.

## 10. Integration Roadmap
Step-by-step implementation order.

## 11. Final Readiness Assessment
Give separate readiness ratings for:
- Main database engine.
- Recovery.
- Torture tester.
- Integration.
- Overall competition readiness.

## 12. Immediate Next Actions
Give me the next five actions in order.

---

# FINAL RULE

Do not make code changes during this audit.

I want an honest engineering review, not reassurance.

If something is weak, say so. If the recovery implementation is redundant, say so. If the torture tester does not adequately prove crash safety, explain exactly why.

Our objective is not merely to have three repositories that compile.

Our objective is to produce one integrated database system that can survive the competition's tests and convincingly prove its correctness to the judges.

**Stop after delivering the complete audit. Wait for my approval before implementing any changes.**
````

### Prompt 10

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 18:57 (local time; 13:27 UTC in the session transcript)
- **Notes:** Pasted with no other text; copied verbatim from the same session transcript. Produced the hardening and evidence work recorded in docs/DESIGN.md change log #33 onward. Full text as sent:

````text
NEXUS — Complete Competition Readiness Improvement
Use the latest integration and competition-readiness audit as the source of truth.
Objective
Improve every audited component toward a demonstrable 9+/10 standard. Do not chase cosmetic improvements at the expense of correctness. Every change must have tests and measurable evidence.
1. Protect the existing project

* Inspect all three repositories and the current Git state before changing anything.
* Preserve existing user changes.
* Never commit, push, reset, rebase, amend, switch branches, or discard changes.
* Do not merge the recovery repository into NEXUS.
* Do not remove working functionality to simplify implementation.
* Keep Windows as the primary judging platform.
* Do not claim power-loss durability without evidence.

2. NEXUS engine hardening
Implement and test:
A. Windows atomic moves

* Update `src/internal/platform/move_windows.go`.
* Use `MoveFileExW` with `MOVEFILE_WRITE_THROUGH` only.
* Preserve no-replace semantics. Never use `MOVEFILE_REPLACE_EXISTING`.
* Add tests proving an existing destination is never overwritten.
* Run compaction crash scenarios C3–C5.

B. Read-only verification

* Add an optional `--no-repair` mode to `check`.
* Default `check` must retain its current recovery-and-repair behavior.
* Read-only mode must never truncate, rewrite, compact, or otherwise modify the database.
* Test valid files, torn tails, middle corruption, and damaged snapshots.
* Clearly explain the difference in CLI help and README.

C. Deterministic compaction

* Sort keys during compaction.
* Ensure deterministic output for identical logical database contents.
* Verify that sorted output remains compatible with existing NEXUS files and the independent recovery implementation.
* Test reopening and writing after compaction.

3. Torture tester — highest priority
The NEXUS Go torture tool is currently the proven official tester. Keep it as the official competition tool unless the Rust tool is fully implemented and independently validated.
For the Rust tool, investigate and address:

* Main function is currently a stub.
* Add a real iteration loop.
* Add a working NEXUS process adapter.
* Correctly interpret `ERR ... unknown|io` as an unknown outcome, not as a definite no-effect operation.
* Mix iteration number into every random stream.
* Fix oracle scalability through early pruning and define what an inconclusive result means.
* Support values up to 1 MiB without hex doubling exceeding the engine limit.
* Add `--max-bytes`.
* Add real recovery-crash and compaction modes.
* Add GET operations.
* Add official 10-operation and 10,000-operation batch cases.
* Fix silent writer timeouts and ignored cleanup errors.
* Ensure every iteration starts from a clean database.
* Make results reproducible from seed and iteration number.

Do not claim the Rust tool works unless it actually launches NEXUS and validates recovered state.
If Rust cannot be built or tested because the toolchain is unavailable, document that limitation and keep the Go tool as the official tester. Do not present a non-running Rust tool as a completed deliverable.
4. Independent recovery verification
Keep the recovery repository separate.
Produce reproducible cross-compatibility evidence for:

* NEXUS writes → recovery Verify.
* Recovery compaction → NEXUS check.
* NEXUS writes after recovery compaction.
* Recovery-created files → NEXUS reads and writes.
* Torn-tail handling.
* Identical format constants and checksum rules.

Document that the recovery project is an independent implementation, not a component required to run NEXUS.
5. Competition evidence
Review and improve:

* README.md
* ARCHITECTURE.md
* BREAKING.md
* TEST_PLAN.md
* PROMPTS.md

Requirements:

* Every AI prompt must be logged exactly as sent, with tool and approximate time.
* Find the original Prompt 9 in the actual conversation/tool history. Do not reconstruct or fabricate it.
* If the original cannot be recovered, clearly document that it remains missing.
* Credit the recovery and torture-tool teammates accurately.
* Distinguish measured results from previously recorded results.
* Explain the 32 official crash scenarios and their outcomes.
* Include exact commands judges can run.
* Include reproducible seeds and expected output.
* Explain durability, ACK semantics, batch atomicity, recovery, compaction and size-cap behavior in plain language.
* Clearly state known limitations.

6. Final validation
Run and record:

* `gofmt`
* `go vet ./...`
* `go build ./...`
* `go test -count=1 ./...`
* All 32 official crash scenarios.
* Normal torture runs with reproducible seeds.
* Disk-full mode.
* 1M-key recovery benchmark.
* 1M puts followed by 1M deletes, file shrink, and successful writes after compaction.
* 1 MiB value tests.
* 10,000-operation batch tests.
* Concurrent writer tests.
* Read-only check tests.
* Windows move and compaction tests.
* Cross-format compatibility tests.

Do not report a test as passing unless it was actually run. Distinguish tests run today from historical recorded measurements.
7. Final report
Provide:

1. Every file changed.
2. What each fix does.
3. Exact test commands and results.
4. Any failures or unverified work.
5. Updated evidence-based ratings for each component.
6. Remaining blockers to reaching 9+/10.
7. Exact commands for the judges' demonstration.

Stop without committing or pushing anything.
````


### Prompt 10a

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 19:57 (local time; 14:27 UTC, from the session transcript)
- **Notes:** Typed while the Prompt 10 validation script was running.

```text
how much longer
```

### Prompt 11

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 20:38 (local time; 15:08 UTC in the session transcript)
- **Notes:** Pasted with no other text; copied verbatim from the same session transcript. Produced NEXUS Torture Engine 2.0 (`torture2/`, the `nexus-torture` command) and docs/TORTURE_ARCHITECTURE.md, docs/TORTURE_TEST_PLAN.md, docs/TORTURE_RESULTS.md and docs/TORTURE_CLI.md. Full text as sent:

````text
PROJECT: NEXUS TORTURE ENGINE 2.0
Build a new, first-party, competition-grade torture-testing system for NEXUS.
Objective
Create a reliable, independent testing framework targeting demonstrable 9.5–10/10 quality.
Use Go and the standard library wherever practical. Target native Windows first.
Do not merely rename or cosmetically modify the existing tester. Inspect its architecture and decide which components are worth retaining. The new system must have a clear, independently testable design.
Repository safety

* Inspect Git status and repository structure first.
* Preserve all working NEXUS functionality.
* Do not commit, push, reset, rebase, amend, switch branches or discard changes.
* Do not modify any teammate's separate repository.
* Do not merge external torture-tool code.
* Keep the implementation first-party.
* Remove unnecessary references to external tester implementations from judge-facing NEXUS documentation, without falsifying project history, deleting required prompt logs or removing legitimate attribution.
* Keep PROMPTS.md accurate.

1. Independent oracle
Implement a reference model independent of NEXUS storage internals.
It must:

* Track submitted operations.
* Track acknowledged operations.
* Track definite rejections.
* Track unknown outcomes.
* Preserve per-writer ordering.
* Account for legal concurrency interleavings.
* Validate recovered database state.
* Verify batch atomicity.
* Verify last-operation-wins ordering inside batches.
* Detect acknowledged-write loss.
* Detect unexpected partial batches.
* Distinguish genuine violations from inconclusive results.

Do not simply compare the recovered database against the requests in submission order when operations were concurrent.
2. Real process management
The tester must:

* Launch the actual NEXUS executable.
* Communicate through its real CLI or serve interface.
* Kill the actual process.
* Restart the database.
* Verify recovery after every relevant kill.
* Record ACKs only when complete ACK responses are received.
* Correctly classify unknown outcomes.
* Enforce explicit timeouts.
* Never silently abandon writers.
* Clean up temporary files safely.
* Fail clearly if the executable cannot be launched.

Mock engines may be used for unit tests, never as proof of NEXUS correctness.
3. Workload generation
Implement:

* Deterministic seeds.
* Independent per-iteration random streams.
* Single PUT and DELETE.
* GET verification.
* Missing-key deletion.
* Batches of 10 operations.
* Batches of 10,000 operations.
* Values up to 1 MiB.
* Concurrent disjoint-key writers.
* Concurrent same-key writers.
* Readers operating during batches.
* Long-running mixed workloads.
* Disk-full conditions.
* Compaction workloads.
* Recovery-crash workloads.
* Close and reopen tests.

Each failure must be reproducible from its seed and iteration number.
4. Crash testing
Support all 32 official scenarios.
Organize them by official groups A–F. Each scenario must:

* Have a stable ID.
* Have a clear description.
* Have an explicit expected invariant.
* Execute against the real NEXUS executable.
* Record its kill point and outcome.
* Verify recovered state.
* Produce a machine-readable result.

Do not report unsupported scenarios as passing.
5. Oracle scalability
The reference checker must support realistic competition workloads.

* Prune impossible candidate histories early.
* Avoid unnecessary factorial enumeration.
* Use per-key reasoning where valid.
* Preserve global ordering constraints where required.
* Set explicit resource limits.
* Report inconclusive results distinctly from PASS.
* Add stress tests for the checker itself.

A timeout or inconclusive result must never count as a pass.
6. Mutation testing
Build deliberately broken test binaries or controlled test variants that simulate:

* ACK before durable sync.
* Dropped acknowledged write.
* Partial batch application.
* Incorrect unknown-outcome handling.
* Incorrect recovery behavior.
* Corrupted values.

The new tester must detect these mutations.
Do not modify the official production binary to introduce mutations.
7. Reporting
Produce:

* Human-readable terminal summary.
* JSONL machine-readable event logs.
* Summary report.
* Failure reproduction command.
* Seed and iteration information.
* Crash location.
* Acknowledged and unknown operation counts.
* Recovery duration.
* Oracle verdict.
* Minimized failure workload where feasible.

Never report PASS without successful oracle verification.
8. Performance
Measure:

* Iterations per second.
* Process restart time.
* Recovery duration.
* Oracle execution time.
* Memory usage.
* Total test duration.

Do not compromise correctness for speed.
9. Documentation
Write:

* TORTURE_ARCHITECTURE.md
* TORTURE_TEST_PLAN.md
* TORTURE_RESULTS.md
* CLI usage documentation

Explain the tool in language a competition judge can understand.
10. Validation and acceptance
Run:

* All 32 official crash scenarios.
* At least 500 randomized iterations.
* 100 iterations per specialized mode.
* 10,000-operation batch tests.
* 1 MiB value tests.
* Concurrent writer and reader tests.
* Disk-full tests.
* Compaction and recovery-crash tests.
* Mutation tests.
* Reproducibility tests.
* Windows process-kill tests.

Use a clean scratch directory.
Report exact commands, actual results, failures and limitations.
Do not invent results or claim unexecuted tests passed.
Completion report
Provide:

1. Architecture.
2. Files created and changed.
3. Test results.
4. Mutation detection results.
5. Performance measurements.
6. Remaining weaknesses.
7. Evidence-based rating.
8. Exact judge demonstration commands.

Do not commit or push.
````


### Prompt 11a

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 22:06 (local time; 16:35 UTC, from the session transcript)
- **Notes:** Typed while the Prompt 11 validation was running, one minute before Prompt 12.

```text
how much longer
```

### Prompt 12

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 22:06 (local time; 16:36 UTC in the session transcript)
- **Notes:** Pasted with no other text; copied verbatim from the same session transcript. Sent while the Prompt 11 validation was still running. Produced the verification counters in nexus-torture, the `kvdb-bench` benchmark tool, docs/FAILURE_GUARANTEES.md, the trade-offs section of ARCHITECTURE.md and the final validation run in docs/results/final/. Full text as sent:

````text
We have approximately 12 hours remaining before the NEXUS database competition.
After researching the judge's professional background, we want to place additional emphasis on production-grade engineering: reliability, measurable performance, observability, reproducibility, architectural clarity and honest documentation.
Do NOT redesign or rewrite the database engine. Do NOT introduce unnecessary features.
First, inspect the current repository, existing tests, documentation and torture tester. Identify what is already implemented. Do not duplicate existing functionality.
Priority 1 — Torture tester correctness evidence
Ensure the torture tester reports:

* Total operations attempted.
* Successfully acknowledged operations.
* Cleanly rejected operations.
* Operations with unknown outcomes.
* Acknowledged writes verified after restart.
* Batch atomicity violations.
* Data integrity violations.
* Recovery failures.
* Reproducible seed and iteration number for every failure.

An inconclusive or unknown result must never be reported as PASS.
Test actual NEXUS processes, including killing and restarting them. Use an independent reference model where possible.
Priority 2 — Reproducible benchmark report
If benchmarking is not already available, add a simple benchmark command or standalone benchmark tool.
Measure:

* PUT throughput.
* GET throughput.
* DELETE throughput.
* Batch throughput.
* Recovery time.
* Compaction time.
* File size before and after compaction.

Requirements:

* Use actual measured values.
* Record OS, CPU, Go version, workload size and test configuration.
* Make runs reproducible.
* Do not claim superiority over other databases without fair comparisons.
* Do not modify core storage behaviour just to improve benchmark numbers.

Priority 3 — Failure guarantees documentation
Create or improve a concise failure-guarantees table documenting:

* Process killed during PUT.
* Process killed during BATCH.
* Torn final frame.
* Corruption in the middle of the database.
* Disk full.
* Crash during compaction.
* Recovery after restart.

For every case, document expected behaviour, actual test evidence and any limitations.
Do not claim power-loss safety unless it has actually been tested.
Priority 4 — Architecture trade-offs
Ensure ARCHITECTURE.md explains, in simple technical language:

* Why Go was selected.
* Why append-only storage was selected.
* Why an in-memory hash index is used.
* Why a global mutex is used.
* Why CRC32C is used.
* Why writes are synced before acknowledgement.
* Current scalability limitations.
* What NEXUS deliberately does not support.

Explain trade-offs honestly. Do not present design limitations as advantages.
Priority 5 — Final validation
Run:

* All unit and integration tests.
* All 32 official crash scenarios.
* The complete torture suite.
* Cross-implementation verification.
* Recovery performance test for one million keys.
* Compaction and disk-full tests.
* Clean build from scratch.

Save logs and results in the existing validation documentation structure.
Strict constraints

1. No SQL support, replication, authentication, encryption or new storage formats.
2. No major refactoring.
3. No unnecessary frontend work.
4. Do not remove existing tests, documentation, evidence or AI prompt logs.
5. Do not fabricate results.
6. Do not commit or push anything.
7. Preserve all existing working changes.
8. If a proposed improvement risks destabilising the project, document it instead of implementing it.

At the end, provide:

* Files changed.
* Features already present versus newly implemented.
* Exact tests executed and their results.
* Remaining limitations.
* Any issues requiring manual attention.

The goal is to make NEXUS demonstrably reliable, measurable and technically defensible—not simply bigger.
````


### Prompt 13

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 22:18 (local time; 16:48 UTC in the session transcript)
- **Notes:** Pasted with no other text; copied verbatim from the same session transcript. Sent while the Prompt 12 final validation was running. Produced docs/PROJECT_AUDIT.md and docs/FRONTEND_INTEGRATION.md (assessment only; no engine change). Full text as sent:

````text
# NEXUS — Complete Project Audit & Frontend Readiness Assessment

We are approaching the final stage of the NEXUS database competition. The core database engine and testing infrastructure have undergone substantial development.

We are now preparing to build a polished frontend separately using Codex.

Before frontend development progresses, I want you to perform a COMPLETE audit of the current NEXUS repository and tell us exactly where the project stands.

This is an assessment task first. Do not immediately start implementing new features.

## 1. Understand the entire project

Inspect the current repository, including:

- Go database engine.
- Storage format and binary structures.
- Indexing implementation.
- PUT, GET, DELETE and BATCH operations.
- Recovery mechanism.
- Crash consistency.
- Compaction.
- maxBytes enforcement.
- Concurrency and locking.
- CLI implementation.
- Serve mode and communication protocol.
- Torture tester.
- Independent verification tools.
- Benchmarking infrastructure, if present.
- Unit and integration tests.
- Documentation.
- Validation logs and evidence.
- Existing frontend-related files, if any.

Do not rely on previous audit reports alone. Inspect the actual current code and repository state.

## 2. Give us an honest completion assessment

Create a comprehensive status report.

For every major component, classify it as:

- COMPLETE — implemented and adequately tested.
- IMPLEMENTED, NEEDS VALIDATION — exists but evidence is insufficient.
- PARTIALLY COMPLETE — some functionality remains.
- NOT IMPLEMENTED — missing.
- BLOCKED — cannot be completed because of an external dependency.

Cover these categories:

| Component | What to evaluate |
|---|---|
| Core engine | Operations and correctness |
| Binary format | Integrity and validation |
| Index | Correctness and performance |
| Recovery | Crash handling and consistency |
| Compaction | Correctness and crash safety |
| Concurrency | Race conditions and locking |
| Disk limits | maxBytes and ErrFull |
| CLI | All required commands |
| Serve mode | Protocol and integration readiness |
| Torture tester | Workload quality and independent oracle |
| Independent verifier | Cross-implementation compatibility |
| Benchmarks | Reproducibility and performance |
| Documentation | Accuracy and completeness |
| Evidence | Actual logs and reproducible results |

Give each category a percentage completion estimate, but explain how you calculated it. Do not invent percentages simply to make the project look complete.

## 3. Verify the actual test evidence

Inspect available logs and determine:

- Which tests have actually been executed.
- Which tests passed.
- Which tests failed.
- Which tests have not been run.
- Whether results are reproducible.
- Whether tests exercise real NEXUS processes.
- Whether any tests only use mocks.
- Whether the independent verification is genuinely independent.
- Whether benchmark results have enough environmental information.
- Whether the official crash scenarios have sufficient evidence.

Pay particular attention to the torture tester.

A test that completes without crashing is not automatically proof of data correctness.

Identify any gaps in the testing strategy.

Do not rerun extremely expensive tests without first explaining the cost and checking whether recent logs are sufficient.

## 4. Assess whether NEXUS is ready for judging

Evaluate the project against the competition's important requirements:

- Crash survival.
- Atomic batches.
- Durable acknowledgements.
- Recovery correctness.
- Compaction.
- maxBytes enforcement.
- Concurrent writers.
- Recovery performance for one million keys.
- Torture testing.
- Architecture explanation.
- BREAKING.md.
- Design extension.

Give us:

1. What is already strong.
2. What could cause us to lose marks.
3. What could cause a live demonstration to fail.
4. What technical questions we may struggle to answer.
5. What should be fixed before judging.
6. What should deliberately remain out of scope.

Be critical. We want to discover problems now, not in front of judges.

## 5. FRONTEND INTEGRATION READINESS — VERY IMPORTANT

We are creating the frontend separately using Codex.

The frontend should be a professional, simple dashboard that demonstrates the actual capabilities of NEXUS.

We want these five sections:

1. Overview
2. Database
3. Recovery
4. Torture Lab
5. Architecture

Inspect the current implementation and determine exactly how the frontend can communicate with NEXUS.

### Investigate the existing interface

Determine:

- Does NEXUS already have a running server mode?
- How is the server started?
- What protocol does it use?
- Is communication over HTTP, TCP, stdin/stdout or something else?
- What operations can currently be performed through it?
- What responses and error formats does it return?
- Can it expose database status?
- Can it expose key-value records?
- Can it expose recovery information?
- Can it expose torture-test results?
- Can it expose benchmark information?
- Can it expose compaction information?

Do not assume that functionality exists just because the CLI supports it.

### Create a frontend integration specification

Recommend a clean integration architecture.

If HTTP is already supported, document the existing endpoints.

If HTTP is not supported, recommend the smallest practical adapter that can expose the existing engine without changing its storage format or core semantics.

Provide:

- API endpoint names.
- HTTP methods, if applicable.
- Request examples.
- Response examples.
- Error responses.
- Data types.
- Loading and disconnected states.
- How the frontend should handle operations in progress.
- How the frontend can refresh database state.
- How torture-test progress and results can be displayed.

Clearly distinguish:
- Existing functionality.
- Functionality that needs a small adapter.
- Functionality that would require new engine capabilities.

The frontend must never display fabricated metrics or pretend a mock operation changed the real database.

### Integration principle

The frontend should communicate through a clean service/API layer.

We should be able to use mock data initially and later switch to the real NEXUS backend without rewriting the UI.

Avoid unnecessary changes to the Go engine.

## 6. Evaluate the product as a whole

Imagine you are an experienced software engineering professional evaluating this project.

Would you consider NEXUS a convincing demonstration of database-engine engineering?

Evaluate:

- Technical depth.
- Reliability.
- Performance.
- Architecture.
- Testing quality.
- Ease of demonstration.
- Documentation.
- Practical usefulness.
- Limitations and trade-offs.

Tell us what makes the project impressive and what currently makes it less convincing.

Do not recommend adding features simply to increase the feature count.

## 7. Final deliverables

Produce a detailed report with these sections:

1. Executive summary.
2. Overall project completion percentage.
3. Component-by-component completion table.
4. Verified test results.
5. Unverified claims and missing evidence.
6. Critical bugs and risks.
7. Competition readiness assessment.
8. Frontend integration readiness.
9. Recommended API/service contract.
10. Remaining work, ordered by importance.
11. Features we should NOT add.
12. Final verdict: Are we ready to proceed with frontend development?

At the end, give us a clear decision:

- READY — frontend can be built against the existing interface.
- READY WITH A SMALL ADAPTER — only a limited integration layer is required.
- NOT READY — specific blockers must be fixed first.

## STRICT RULES

- Do not commit or push.
- Do not reset, rebase, switch branches or discard changes.
- Do not modify the database engine during this assessment.
- Preserve all existing work and documentation.
- Do not invent test results.
- Do not claim tests passed without evidence.
- Do not claim API functionality exists without inspecting its implementation.
- Do not introduce unnecessary dependencies.
- Do not recommend a major rewrite.
- Keep the final recommendations practical given our limited remaining time.

We need an honest engineering assessment, not reassurance.

The most important outcome is knowing whether the NEXUS product is sufficiently complete and stable to build a frontend around, and exactly what integration work remains.
````

### Prompt 14

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 22:28 (local time; 16:57 UTC, from the session transcript)
- **Notes:** Pasted with no other text; copied verbatim from the session transcript. Sent while the Prompt 12 final validation was running. Produced an investigation of running and stopped validation tasks (status table in the chat; findings later recorded in docs/TORTURE_RESULTS.md and docs/results/final/README.md). No files were changed for it. Full text as sent:

````text
Before running any more validation, stop launching additional full test suites.
I can see multiple validation tasks marked Stopped, while other validation scripts are still running.
I need you to investigate the current state first.

1. Identify every currently running validation process and its parent process.
2. Identify why each previously stopped validation task stopped.
3. Retrieve the actual exit codes and output logs for each stopped task.
4. Determine whether any tests are still running in the background.
5. Check whether multiple validation scripts are testing the same components unnecessarily.
6. Identify which tests have genuinely passed, failed, timed out or remain incomplete.
7. Check whether the final validation script is waiting on another process or has become stuck.

Do not kill working processes blindly.
Do not restart the entire validation suite.
Give me a concise status table showing:

* Test name
* Status
* Duration
* Exit code
* Reason for stopping/failure
* Whether its result is trustworthy

Then recommend the smallest remaining set of tests needed to establish final readiness.
Do not modify the database engine or torture tester during this investigation. Do not commit or push.
````

### Prompt 15

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 22:31 (local time; 17:01 UTC, from the session transcript)
- **Notes:** Pasted with no other text; copied verbatim from the session transcript. Produced docs/results/final/run-targeted.sh, the targeted final validation (logs in docs/results/final/), docs/TORTURE_RESULTS.md, docs/results/final/README.md, and the final-validation tables in README.md and docs/PROJECT_AUDIT.md. Full text as sent:

````text
Proceed with the optimized validation plan.

1. Let the currently running step 08 finish naturally. Do not interrupt it.
2. Once it finishes, stop the remaining redundant steps in run-final.sh, provided their results are already covered by trustworthy earlier validation.
3. Preserve all existing logs and evidence. Do not delete previous validation results.
4. Run the targeted validation set you proposed:
   * Default Torture Engine 2.0: 500 iterations, seed 12345.
   * Mutation testing and 20-iteration detection-rate check.
   * Reproducibility: two runs with seed 31337 and compare results.
   * All 32 scenarios in quick mode using the latest nexus-torture build.
   * kvdb-bench with 100k operations, three runs.
5. Capture exit codes, logs, seeds, durations and actual results.
6. Clearly distinguish previous engine validation from current tester validation.
7. If any test fails, investigate before proceeding. Do not classify inconclusive tests as passing.

Do not change the database engine, make unnecessary tester changes, or commit/push.
At the end, give me a final validation summary and tell me whether NEXUS is ready for frontend integration and judging.
````

### Prompt 15a

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 22:48 (local time; 17:18 UTC, from the session transcript)
- **Notes:** Typed while the targeted validation was running.

```text
how much longer
```

### Prompt 15b

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 23:10 (local time; 17:39 UTC, from the session transcript)
- **Notes:** Typed while the targeted validation was running.

```text
time left
```

### Prompt 15c

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 23:25 (local time; 17:54 UTC, from the session transcript)
- **Notes:** Sent by clicking the "Not yet" button, which answered Claude's question "Commit the working tree now (most of Prompts 10–13 is uncommitted)?". Nothing was committed.

```text
Not yet
```

### Prompt 16

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 23:25 (local time; 17:55 UTC, from the session transcript)
- **Notes:** Typed right after Prompt 15c (the reply in progress was interrupted). Produced this update of PROMPTS.md: Prompts 10a, 11a and 14–16 added, implementation-prompt timestamps checked against the session transcript, and the status of the design-phase section stated.

```text
fix prompts.md and make sure it is up to date
```
