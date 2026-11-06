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

### Prompt 17

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-06 23:45 (local time; 18:15 UTC, from the session transcript)
- **Notes:** Pasted with no other text; copied verbatim from the session transcript. Sent after the user committed the earlier work as 7a1f536. The prompt refers to an attached NEXUS UI reference image; no image was attached to the message (the transcript contains text only). Produced the HTTP adapter (`src/cmd/nexus-api`) and the frontend (`frontend/`). Full text as sent:

````text
# NEXUS — Complete Frontend Development (Phase 1)

You are building the frontend for **NEXUS**, a high-performance, crash-safe key-value database engine developed in Go.

Your task is to create a complete, polished, functional database management and observability dashboard that integrates with the existing NEXUS backend.

This is a real engineering project intended for a live technical judging demonstration. The interface must not just look impressive; its features must work with the actual database and testing tools.

---

# 1. IMPORTANT: Understand the Existing Project First

Before writing any code, inspect the repository thoroughly.

Repository location:

`C:\Users\MY PC\NEXUS_SRINIVAS`

Read and understand:

- `README.md`
- `ARCHITECTURE.md`
- `BREAKING.md`
- `docs/FRONTEND_INTEGRATION.md`
- `docs/FAILURE_GUARANTEES.md`
- `docs/RECOVERY.md`
- `docs/TORTURE_TOOLS.md`
- `docs/TORTURE_ARCHITECTURE.md`
- `docs/TORTURE_CLI.md`
- Relevant source code under `src/`
- The existing torture-testing implementation under `torture2/`

Understand the actual functionality of NEXUS before designing the interface.

**Critical restrictions:**

1. Do not modify the existing database storage format.
2. Do not rewrite or duplicate the database engine.
3. Do not introduce another database engine.
4. Do not remove existing functionality.
5. Do not invent backend capabilities or API endpoints.
6. Do not display fabricated performance statistics as real data.
7. Do not change existing Go functionality merely to accommodate frontend design.
8. Do not commit, push, reset, rebase, or amend Git history.

The existing database engine is already implemented, tested, committed, and pushed. Protect it.

---

# 2. Technology Stack

Use the following stack:

- Next.js with App Router
- TypeScript with strict mode
- Tailwind CSS
- shadcn/ui
- Lucide React icons
- Recharts for data visualizations
- TanStack Query for API state management
- Zod for validation
- React Hook Form for forms

Use reusable components and maintain a clean, scalable project structure.

Avoid unnecessary dependencies.

---

# 3. Visual Identity — STRICT MINIMALIST GREYSCALE

This is one of the most important requirements.

The entire NEXUS dashboard must follow a **minimalist charcoal, graphite, and greyscale aesthetic**.

Think of premium developer tools and sophisticated database management software.

The design must feel:
- Minimalist.
- Professional.
- Technical.
- Precise.
- Spacious.
- Elegant.
- Functional.

## Colour palette

Use this palette consistently across every page:

| Element | Colour |
|---|---|
| Main background | `#111111` |
| Sidebar background | `#151515` |
| Card background | `#1B1B1B` |
| Elevated surfaces | `#222222` |
| Borders | `#303030` |
| Primary text | `#F2F2F2` |
| Secondary text | `#A0A0A0` |
| Muted text | `#707070` |
| Subtle hover | `#292929` |

Use different shades of grey to establish depth and hierarchy.

## Strict design rules

- No purple accents.
- No neon colours.
- No colourful gradients.
- No flashy backgrounds.
- No excessive glassmorphism.
- No excessive glow or shadows.
- No oversized headings.
- No unnecessary decorative elements.
- No overcrowded cards.
- No excessive rounded corners.

Use:
- Thin grey borders.
- Subtle shadows only where needed.
- Clean typography.
- Generous whitespace.
- Compact, carefully aligned cards.
- Consistent spacing.
- Small, understated icons.
- Subtle hover states.
- Restrained transitions.
- Greyscale charts.

Reserve subtle green and red only for meaningful success, warning, and error states.

The overall interface should resemble a premium database engineering product rather than a generic SaaS dashboard.

## Reference image

Use the attached NEXUS UI reference image as inspiration for:
- Sidebar structure.
- Dashboard layout.
- Metric cards.
- Database operation forms.
- Storage tables.
- Recovery panels.
- Testing results.
- General page composition.

However, disregard the purple accents and use the strict charcoal-and-greyscale palette above.

The final design should be cleaner and less cluttered than the reference.

---

# 4. Application Structure

Build the main application directly.

**Do not create an introductory landing page or intro animation in this phase.**

The user should open the application and immediately see the main dashboard.

Do not implement:
- Logo reveal animations.
- Scrolling hero animations.
- Animated landing pages.
- Introductory splash screens.

## Sidebar navigation

Create a persistent left sidebar containing:

1. Overview
2. Database
3. Storage
4. Recovery
5. Torture Lab
6. Architecture

Include:
- NEXUS logo and name.
- Active navigation indicator.
- Backend connection status.
- Database health status.
- Consistent icons.
- Responsive behaviour.

Keep the sidebar minimalist and relatively narrow.

## Main content area

Every page should have:
- A clear page title.
- A short description.
- Relevant actions.
- Consistent spacing.
- Consistent card and table styling.

Include toast notifications, confirmation dialogs, loading indicators, and error states.

---

# 5. Page 1 — Overview Dashboard

This is the primary screen and should receive the most visual attention.

Create a clean database overview dashboard.

## Database status

Display actual backend information wherever available:

- Database health.
- Connection status.
- Total number of keys.
- Database file size.
- WAL size, if available.
- Storage levels, if available.
- Recent operations.
- Last compaction status.
- Recent recovery activity.

If a metric is not exposed by the backend, display `N/A` or `Not available`.

Never invent values.

## Performance visualizations

Create clean greyscale charts for:

- PUT operations.
- GET operations.
- DELETE operations.
- Operations per second.
- Latency, if measurable.

Use clear legends and readable axes.

Do not show fabricated historical metrics.

If live time-series information is unavailable, explain that the metric is unavailable instead of generating fake activity.

## Quick actions

Provide compact actions for:

- PUT
- GET
- DELETE
- COMPACT
- INSPECT

These should open functional forms or navigate to the appropriate page.

## Recent activity

Show recent operations and system activity where available.

Include:
- Operation type.
- Key, where appropriate.
- Timestamp.
- Result.
- Status.

Keep this section compact and readable.

---

# 6. Page 2 — Database

Build a functional interface for interacting with the NEXUS key-value store.

Provide four main operations.

## PUT

Allow users to:
- Enter a key.
- Enter a value.
- Submit the operation.
- See the result.

Support large values within the backend's actual limits.

Provide appropriate validation.

## GET

Allow users to:
- Enter a key.
- Retrieve its value.
- Display the returned value clearly.
- Display errors when the key does not exist.

Support readable JSON and plain-text formatting.

## DELETE

Allow users to:
- Enter a key.
- Delete it.
- Confirm the action.
- See the result.

## LIST KEYS

If the backend exposes key listing:
- Display keys in a table.
- Support pagination.
- Support prefix filtering if available.
- Show useful empty states.

Do not invent a listing endpoint if one does not exist.

## UX requirements

Use a clean interface with:
- Tabs or segmented controls for operations.
- Compact forms.
- Readable value viewers.
- Copy-to-clipboard functionality.
- Clear success and failure notifications.

Distinguish between a missing key and an actual backend failure.

---

# 7. Page 3 — Storage and Compaction

Create a storage management dashboard.

Display information available from the backend:

- Database file size.
- WAL size.
- Storage levels.
- File counts.
- File sizes.
- Compaction status.
- Latest compaction result.
- Relevant storage information.

Do not assume that NEXUS uses storage levels or metadata that are not actually exposed.

## Compaction

Provide a `Compact Database` action.

When clicked:
1. Display a confirmation dialog.
2. Send the request to the actual backend.
3. Display the running state.
4. Display the final result.
5. Refresh relevant storage information.

Show meaningful errors if compaction fails.

## Storage visualization

Use a clean table or simple visual representation.

Avoid unnecessary animations and decorative graphics.

---

# 8. Page 4 — Recovery and Inspection

Create a dedicated recovery and database inspection interface.

This page is important because crash safety and recovery are major parts of NEXUS.

## Inspection

Provide a button to run database inspection using the existing inspection functionality.

Display the actual results returned by the backend.

## Results presentation

Where supported, display:

- WAL consistency.
- Snapshot or manifest consistency.
- Recovery status.
- Validation results.
- Warnings.
- Errors.
- Latest recovery information.

Present results using clear pass/fail indicators.

Use:
- Subtle green for confirmed success.
- Subtle red for confirmed failure.
- Grey for unavailable or unverified information.

## Recovery information

Show:
- Latest recovery status.
- Recovery logs, if available.
- Inspection history, if supported.
- Any detected inconsistencies.

Never display a successful recovery result unless the backend actually confirms it.

Do not expose destructive recovery operations unless they are explicitly supported by the existing system.

---

# 9. Page 5 — Torture Lab

This is one of the most important demonstration sections.

NEXUS has an existing Go-based torture-testing system under `torture2/`.

The frontend must integrate with the actual testing implementation.

Do not recreate the testing engine in JavaScript.

## Main interface

Create a professional testing dashboard with:

- Available workloads.
- Workload descriptions.
- Test configuration.
- Run Test button.
- Test status.
- Execution logs.
- Final results.

## Workload selection

Inspect the actual torture tester and expose only supported workloads.

Where supported, allow users to select workloads such as:

- Default workload.
- Concurrent disjoint keys.
- Same-key concurrency.
- Large batches.
- Large values.
- Compaction.
- Disk-full scenarios.
- Recovery/crash testing.
- Other workloads actually implemented by the tester.

Do not invent workloads.

## Test execution

When a user starts a test:

1. Validate the configuration.
2. Submit the request to the backend.
3. Show the running state.
4. Display progress if the backend exposes it.
5. Display logs if available.
6. Display the final result.
7. Allow the user to inspect the result.

## Results

Display actual information where available:

- Number of operations.
- Duration.
- Throughput.
- Number of failures.
- Number of successful checks.
- Test outcome.

Do not fabricate test results.

Clearly distinguish:
- Running.
- Passed.
- Failed.
- Interrupted.
- Blocked.

A test should never be marked as passed unless the backend confirms success.

If the existing torture tester cannot safely be triggered through the HTTP adapter, explain the limitation and propose a safe integration method. Do not execute arbitrary shell commands from the frontend.

---

# 10. Page 6 — Architecture

Create a visually clean architecture page explaining how NEXUS works.

Base the architecture entirely on the actual source code and documentation.

Include the real components:

- CLI/API layer.
- Database interface.
- In-memory hash index.
- Append-only binary log.
- Write and read paths.
- Recovery process.
- Compaction process.
- Independent verification.
- Torture-testing system.

## Architecture diagram

Create a simple connected diagram.

Each component should have:
- A name.
- A short explanation.
- Its role in the system.
- Connections to related components.

Use monochromatic boxes and connecting lines.

Provide a simple way to understand:
- How a write happens.
- How a read happens.
- How recovery works.
- How compaction works.

Do not invent distributed architecture, replication, consensus, or other features that NEXUS does not implement.

---

# 11. Backend and HTTP Integration

The NEXUS database engine currently exists separately from the frontend.

Read `docs/FRONTEND_INTEGRATION.md` and follow its API contract.

Build a dedicated API/service layer.

## Requirements

- Read the API base URL from an environment variable.
- Create `.env.example`.
- Use typed request and response interfaces.
- Centralize API calls.
- Use request timeouts.
- Handle network errors.
- Handle server disconnection.
- Handle malformed responses.
- Handle non-2xx responses.
- Display loading and error states.
- Refresh data after successful mutations.
- Avoid excessive polling.

Use TanStack Query for data fetching and cache invalidation.

Do not place API logic directly inside every UI component.

## HTTP adapter

If the HTTP adapter is not already implemented:

- Build it as a separate Go component.
- Use Go standard-library functionality where possible.
- Call the existing NEXUS library and tools.
- Follow the documented API contract.
- Do not duplicate database logic.
- Do not modify the storage format.
- Keep the adapter isolated from the core engine.
- Add appropriate tests.

The frontend must not directly execute arbitrary shell commands.

---

# 12. Demo Mode

Implement a clearly separated demo mode for development and UI previews.

Requirements:

- All mock data must be labelled `DEMO DATA`.
- Never mix mock data with live backend responses.
- Provide an explicit way to switch between demo and live mode.
- Show connection status prominently.
- Do not silently substitute fake metrics when the backend is unavailable.
- Show a useful disconnected state when the backend cannot be reached.

The default experience should make it obvious whether the dashboard is connected to a real NEXUS backend.

---

# 13. Testing — MANDATORY

Do not stop once the UI looks good.

Test every implemented feature.

## Frontend tests

Verify:

- TypeScript compilation.
- ESLint.
- Production build.
- All navigation links.
- Responsive behaviour.
- Loading states.
- Empty states.
- Error states.
- Disconnected states.
- Form validation.
- Dialogs and confirmations.
- Toast notifications.
- Charts.
- No dead buttons.
- No browser console errors.

## Real backend integration tests

With the actual NEXUS backend running, test:

1. Health/status request.
2. PUT a key-value pair.
3. GET the same key.
4. Verify the retrieved value matches.
5. DELETE the key.
6. Verify the key is no longer available.
7. Retrieve a nonexistent key.
8. Submit invalid input.
9. Test a large value within supported limits.
10. Run compaction.
11. Run inspection.
12. Run an available torture workload.
13. Verify that test logs and results display correctly.
14. Stop the backend.
15. Confirm that the frontend shows a disconnected state.
16. Restart the backend.
17. Verify that the frontend reconnects.

Do not claim a test passed unless it was actually executed and verified.

If a test cannot be performed because an endpoint or adapter is missing, mark it as `BLOCKED`.

Never report blocked tests as successful.

---

# 14. Code Quality and User Experience

The frontend should be production-quality.

Requirements:

- Strict TypeScript.
- Modular components.
- Reusable UI elements.
- Clear API/service separation.
- Consistent design tokens.
- Accessible controls.
- Responsive layout.
- Proper error handling.
- No unnecessary dependencies.
- No unused components.
- No dead interactions.
- No fake functionality.
- No excessive animation.

Use subtle transitions only where they improve usability.

The dashboard must work well at typical laptop and desktop resolutions, especially during a live judging demonstration.

---

# 15. Suggested Folder Structure

Use a sensible structure similar to:

frontend/
  src/
    app/
      overview/
      database/
      storage/
      recovery/
      torture/
      architecture/
    components/
      layout/
      dashboard/
      database/
      storage/
      recovery/
      torture/
      architecture/
      ui/
    lib/
      api/
      queries/
      validation/
      utils/
    hooks/
    types/
  public/
  .env.example
  README.md

You may adjust this structure when appropriate.

Keep the implementation organised and avoid giant page components.

---

# 16. Final Deliverables

Provide:

1. Complete frontend under `frontend/`.
2. All reusable UI components.
3. Typed API/service layer.
4. `.env.example`.
5. HTTP adapter if required.
6. Frontend README with setup instructions.
7. Backend integration instructions.
8. Testing report.
9. List of missing or blocked functionality.

At the end, provide:

- What was implemented.
- Which pages are functional.
- Which features connect to the real backend.
- Which features remain blocked.
- Which tests were actually executed.
- Test results.
- Exact commands to run the frontend and backend.

---

# 17. Implementation Strategy

Work in the following order:

**Phase A — Repository understanding**
- Inspect the codebase.
- Read the integration contract.
- Identify available backend capabilities.
- Identify missing API functionality.

**Phase B — Design system**
- Establish the charcoal and greyscale theme.
- Build the sidebar and application layout.
- Create reusable cards, tables, dialogs, and forms.
- Verify visual consistency.

**Phase C — Main pages**
- Overview.
- Database.
- Storage.
- Recovery.
- Torture Lab.
- Architecture.

**Phase D — Backend integration**
- Implement the API service layer.
- Implement the HTTP adapter if needed.
- Connect every supported feature.
- Add loading, error, and disconnected states.

**Phase E — Testing**
- Run frontend checks.
- Run backend integration tests.
- Test all actual interactions.
- Fix errors.
- Record blocked tests honestly.

**Phase F — Final polish**
- Review every page.
- Remove dead buttons and placeholder interactions.
- Check responsive behaviour.
- Ensure the monochromatic design is consistent.
- Prepare the live demonstration flow.

Do not skip testing to save time.

Do not make unnecessary changes to the existing database engine.

## Final priority

The priority order is:

1. Correct integration with NEXUS.
2. Functional database operations.
3. Reliable recovery and testing interfaces.
4. Accurate metrics and results.
5. Clean, minimalist greyscale design.
6. Visual polish.

**The final product should feel like a premium, minimalist database engineering console built specifically for NEXUS — not a generic admin template.**
````

### Prompt 17a

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-07 01:00 (local time; 19:30 UTC, from the session transcript)
- **Notes:** A first paste of Prompt 18 that was cut off mid-sentence. Its text is exactly the beginning of Prompt 18 below, up to and including the line `Explain` in section 5 ("Show: / In-memory hash index → Append-only binary log / Explain"). The user interrupted the reply and re-sent the complete prompt as Prompt 18.

### Prompt 18

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-07 01:05 (local time; 19:35 UTC, from the session transcript)
- **Notes:** Pasted with no other text; copied verbatim from the session transcript. Sent while the Prompt 17 frontend was built but its live testing and documentation were unfinished. Extends `src/cmd/nexus-api` and `frontend/`. Full text as sent:

````text
NEXUS — Complete the Frontend and Build an Interactive Database Demonstration Platform
You are working on NEXUS, a custom persistent key-value database engine built in Go.
The core database engine, recovery mechanisms, compaction, CLI, benchmarking tools, independent cross-checking and torture-testing framework have already been implemented and validated.
The frontend is currently incomplete. Your task is to finish building a polished, fully functional frontend that allows mentors and judges to explore, operate, understand and evaluate NEXUS without having to manually execute every command in CMD.
This is a real engineering project, NOT a UI mockup.
1. FIRST: UNDERSTAND THE EXISTING PROJECT
Before changing anything:

1. Inspect the complete repository structure.
2. Read:
   * `README.md`
   * `ARCHITECTURE.md`
   * `docs/ARCHITECTURE.md`
   * `docs/FRONTEND_INTEGRATION.md`
   * `docs/PROJECT_AUDIT.md`
   * `docs/FAILURE_GUARANTEES.md`
   * `docs/RECOVERY.md`
   * `docs/TORTURE_ARCHITECTURE.md`
   * `docs/TORTURE_CLI.md`
   * `docs/TORTURE_TEST_PLAN.md`
   * `docs/TORTURE_RESULTS.md`
   * `docs/INDEPENDENT_VERIFICATION.md`
   * `docs/results/final/`
   * `docs/results/validation/`
3. Inspect the actual Go source code, CLI commands, database interfaces, torture-testing tools and recovery implementation.
4. Identify which functionality is already implemented and which functionality still needs an adapter or integration.
5. Read `docs/FRONTEND_INTEGRATION.md` carefully. Treat it as the starting API contract, but verify it against the actual implementation.

Critical: Never invent functionality, metrics, API endpoints, test results or architecture guarantees. If something does not exist, either implement the required integration layer or clearly indicate that the feature is unavailable.
Do not rewrite the database engine just to make the frontend easier.
2. DESIGN DIRECTION
Build a premium, minimalist, monochromatic database engineering dashboard.
The interface should feel like a professional database observability and infrastructure product.
Visual identity
Use this palette:

* Main background: `#111111`
* Sidebar: `#151515`
* Cards: `#1B1B1B`
* Elevated surfaces: `#222222`
* Borders: `#303030`
* Primary text: `#F2F2F2`
* Secondary text: `#A0A0A0`
* Muted text: `#707070`

Use green and red sparingly for success and failure states.
Design requirements

* Minimalist and monochromatic.
* Excellent spacing and typography.
* Clean technical diagrams.
* Subtle hover effects and transitions.
* Responsive layout.
* No excessive gradients.
* No neon colours.
* No unnecessary glassmorphism.
* No decorative animations that interfere with functionality.
* No introductory landing page or lengthy loading animation.

The user should enter directly into the database dashboard.
Suggested stack
Use the existing frontend stack if one already exists and is functional.
Otherwise, use:

* Next.js App Router
* TypeScript
* Tailwind CSS
* shadcn/ui
* Lucide icons
* Framer Motion for subtle transitions
* Recharts for actual metrics and charts

Keep the implementation maintainable and modular.
3. MAIN DASHBOARD
Build a sidebar with these sections:

1. Overview
2. Database Explorer
3. Storage Engine
4. Recovery
5. Torture Lab
6. Benchmark
7. Architecture
8. Database Comparison

The dashboard should display actual information obtained from NEXUS.
Overview page
Display:

* Database status
* Total number of keys
* Live key count
* Storage file size
* Database version
* Current sequence number, if exposed
* Last successful operation
* Recovery status
* Compaction status
* Integrity status

Include charts where the underlying data exists.
Add a recent operations panel showing actual PUT, GET, DELETE and BATCH operations.
Never use randomly generated values or pretend these are live measurements.
If the database is not running, display an honest disconnected state.
4. DATABASE EXPLORER — ACTUALLY USE NEXUS
This is one of the most important pages.
Create a functional interface for operating the database.
Features

* Open or create a database.
* Enter a key and value.
* PUT: Insert or update a record.
* GET: Retrieve a record.
* DELETE: Remove a record.
* BATCH: Execute multiple operations.
* DUMP: Display all current records.
* CHECK: Verify database integrity.

Display:

* Operation type
* Key
* Value
* Success or failure
* Execution time, if measured
* Relevant error messages

Add a searchable key-value table.
Show the distinction between inserting a new key and updating an existing key.
Demonstrate NOTFOUND responses for deleted or nonexistent keys.
All operations must execute against the actual NEXUS database through a backend adapter.
Do not simulate database operations in JavaScript.
5. STORAGE ENGINE VISUALIZATION
Build an interactive visualization explaining how NEXUS stores information.
The architecture must reflect the real implementation.
Show:
In-memory hash index → Append-only binary log
Explain that the append-only log is authoritative and the hash index maps keys to their latest value offsets.
Visualize the write path

1. User sends PUT.
2. Database acquires the appropriate lock.
3. A frame is encoded.
4. Data is appended to the binary log.
5. Checksums are calculated and validated as appropriate.
6. The log is synchronized to disk.
7. The in-memory index is updated.
8. The operation is acknowledged.

Make it clear that the acknowledgement occurs only after the durable write completes.
Visualize a record
Explain:

* Superblock
* Frame header
* Operation type
* Sequence number
* Payload length
* Checksums
* Entry header
* Key
* Value

Use expandable technical explanations.
Show the actual binary format and field sizes from the source code.
Do not make up offsets or structures.
Add a visual representation of the log growing as records are written.
6. RECOVERY LAB — MAKE THIS INTERACTIVE
This section must demonstrate one of NEXUS's most important capabilities.
Explain what happens when the database is restarted after an unexpected interruption.
Build a recovery visualization based on the real recovery implementation.
Display the recovery process

1. Read and validate the superblock.
2. Verify magic, version and checksum.
3. Scan the binary log sequentially.
4. Validate frames.
5. Verify checksums and sequence information.
6. Replay valid operations.
7. Rebuild the in-memory hash index.
8. Handle damaged or incomplete tails according to the implemented recovery policy.

Interactive demonstration
Where the existing tooling permits it:

* Open a database.
* Show the number of records before recovery.
* Run recovery.
* Display recovery progress.
* Display records recovered.
* Show recovery duration.
* Show final integrity status.

Provide a timeline or log view showing recovery events.
Include actual error information if recovery encounters corruption.
Clearly distinguish:

* Valid log records
* Incomplete writes
* Corrupted records
* Recovered records
* Truncated or repaired data

Do not claim that every form of corruption can be recovered.
If a destructive crash demonstration is supported, make it an explicitly controlled experiment using a separate disposable database. Never run destructive operations on the user's actual database.
7. TORTURE LAB — DEMONSTRATE RELIABILITY
This section should allow a mentor to understand and run NEXUS's torture-testing framework through the frontend.
Inspect `torture2/` and its CLI before implementing this section.
Display

* Test suite status
* Number of iterations
* Random seed
* Operations executed
* Acknowledged writes
* Restart and recovery results
* Data integrity results
* Batch atomicity results
* Passed tests
* Failed tests
* Inconclusive tests
* Execution duration

Controls
Provide buttons to:

* Run the default torture test.
* Run a selected scenario.
* Run a test with a specified seed.
* View previous test results.
* Inspect failure details.
* Replay a failure where supported.

Display a live execution log.
Use progress indicators only when actual progress can be obtained.
Clearly label destructive tests and require confirmation before executing them.
Crash scenario visualization
Explore the existing 32 official crash scenarios and group them according to their actual categories.
Show:

* Scenario identifier
* Description
* Expected behavior
* Actual result
* Pass/fail/inconclusive status

Allow the user to select a scenario and view its execution details.
Do not invent additional crash scenarios.
Mutation testing
If the existing mutation framework supports it, demonstrate how deliberately broken implementations are detected.
Explain that mutation testing validates the effectiveness of the tests by introducing controlled faults.
Never present an unbroken control run as a detected mutation.
8. BENCHMARK PAGE
Connect to the actual `kvdb-bench` implementation.
Display:

* PUT throughput
* GET throughput
* DELETE throughput
* Reopen time
* Compaction duration
* Storage size before compaction
* Storage size after compaction

Allow users to run supported benchmarks and view their output.
Include charts comparing operations.
Use the existing recorded results as historical evidence, not as current live performance.
Historical benchmark evidence currently includes:

* 100,000 keys across 3 runs.
* PUT: approximately 2,699 operations/sec.
* GET: approximately 165,652 operations/sec.
* DELETE: approximately 2,668 operations/sec.
* Reopen: approximately 45 ms.
* Compaction: approximately 15.5 MB to 12.3 MB in 0.46 seconds.

Clearly label these as previously recorded measurements. They are machine-dependent and must not be presented as universal performance guarantees.
9. ARCHITECTURE PAGE — EXPLAIN HOW NEXUS WORKS
Create a visually impressive interactive architecture diagram.
Show the actual components and how data moves between them.
Include:

* CLI
* Database API
* Locking mechanism
* In-memory hash index
* Append-only binary log
* Frame encoder/decoder
* Checksums
* Synchronization
* Recovery engine
* Compaction engine
* Inspection/checking tools

Allow users to click components to see:

* Their responsibility
* Their inputs
* Their outputs
* How they interact with other components
* Relevant source code files

Include animated data flow for PUT, GET, DELETE, BATCH, recovery and compaction.
The diagram must represent the actual codebase, not a generic database architecture.
10. DATABASE COMPARISON — WHERE NEXUS DIFFERS
This is a major requirement.
Build a dedicated section titled:
"How NEXUS Differs From Existing Database Architectures"
The purpose is to help mentors understand why we designed NEXUS this way and what engineering decisions distinguish it from existing systems.
Compare NEXUS with:

1. Redis
2. RocksDB
3. LevelDB
4. Bitcask

Do not claim that NEXUS invented append-only logging or hash-indexed storage. These are established database design patterns.
Explain the architectural differences
NEXUS

* Custom persistent key-value engine.
* Append-only binary log is authoritative.
* In-memory hash index stores latest value offsets.
* Custom binary format with superblock and checksummed frames.
* Explicit sync-before-acknowledgement durability.
* Batch operations encoded in one frame.
* Sequential log recovery.
* Crash-safe compaction and recovery mechanisms.

Redis

* Primarily an in-memory data structure server.
* Persistence through configurable mechanisms such as RDB snapshots and AOF.
* Different design goals and memory model.

RocksDB

* Log-structured merge-tree architecture.
* Uses memtables, SSTables and compaction.
* Designed for high-performance embedded storage.

LevelDB

* Also uses an LSM-tree architecture.
* Organizes storage around log files, memtables, SSTables and compaction.

Bitcask

* The closest architectural comparison.
* Uses an append-only log and an in-memory key directory.
* Explain the similarities honestly.
* Identify the implementation-specific engineering decisions in NEXUS rather than pretending the overall pattern is unique.

Create a comparison table
Compare:

* Primary storage architecture
* Index structure
* Write path
* Read path
* Recovery strategy
* Compaction strategy
* Durability mechanism
* Intended use case

Use accurate, defensible information.
Add a "Why We Made These Decisions" section
Explain:

* Why we chose an append-only log.
* Why we use an in-memory hash index.
* Why checksums matter.
* Why sequence numbers matter.
* Why we synchronize before acknowledging durable writes.
* Why batch operations are encoded into one frame.
* Why recovery scans the log sequentially.
* Why compaction is necessary.

Important distinction
Make the following distinction obvious:
NEXUS is not claiming to outperform or replace Redis, RocksDB or LevelDB. It is a focused database-engine implementation that demonstrates control over storage formats, persistence, recovery, durability and verification.
The strongest comparison is not that our architecture is entirely new, but that we implemented and validated these storage mechanisms in our own custom engine.
Include a section called:
"What Makes NEXUS's Implementation Worth Studying?"
Highlight its implementation-level engineering decisions and test evidence.
11. DEMO MODE — FOR MENTORS AND JUDGES
Create a dedicated Demo Mode.
This should be a guided presentation through the actual running system.
The mentor should be able to click through:

1. Introduction to NEXUS.
2. Database architecture.
3. Insert and retrieve records.
4. Update and delete records.
5. Inspect the storage engine.
6. Demonstrate persistence.
7. Run recovery.
8. Run torture tests.
9. View benchmark results.
10. Compare NEXUS with other databases.

Use a clean, guided sequence with a next-step control.
Each step should explain:

* What is happening.
* What the database is doing internally.
* Why it matters.

Do not make this a slideshow of static screenshots. The actual operations should be interactive.
12. BACKEND INTEGRATION
Inspect the existing backend integration documentation.
If no HTTP server exists, build a separate Go standard-library HTTP adapter around the existing database APIs.
Requirements:

* Do not modify the core storage format.
* Do not duplicate database logic in the frontend.
* Do not create a second implementation of the database.
* Use the existing CLI or package interfaces where appropriate.
* Expose only functionality that can actually be implemented.
* Return structured JSON responses.
* Handle errors correctly.
* Avoid blocking the UI during long-running operations.
* Use safe process management if invoking existing testing tools.
* Add request validation.
* Prevent arbitrary filesystem access through user-supplied paths.
* Keep destructive testing behind explicit confirmation.
* Handle concurrent requests safely.
* Provide clear disconnected and error states.

Implement the adapter in a separate package or directory.
Document every endpoint and its request/response format.
If a feature cannot be integrated safely, document the limitation instead of pretending it works.
13. ENGINEERING REQUIREMENTS

* Keep existing database functionality intact.
* Do not modify historical validation results.
* Do not alter test evidence to improve displayed numbers.
* Do not delete project documentation.
* Do not introduce unnecessary dependencies.
* Do not add unrelated features.
* Do not commit or push to GitHub.
* Preserve the current branch and all existing work.
* Follow existing code conventions.
* Ensure the project builds and runs on Windows.

Add appropriate tests for the frontend integration and backend adapter.
Run:

* Go formatting.
* Go tests.
* Go vet.
* Frontend linting.
* Frontend production build.
* API integration tests.

Fix errors rather than merely documenting them.
14. COMPLETION REQUIREMENTS
Do not stop after creating the dashboard layout.
The task is complete only when:

1. The frontend runs locally.
2. The backend adapter runs locally.
3. The frontend connects to the actual NEXUS database.
4. PUT, GET, DELETE, DUMP and CHECK work.
5. Database statistics reflect actual database state.
6. Recovery functionality is demonstrated using real recovery operations.
7. Torture tests can be initiated and their actual results displayed.
8. Benchmark results are displayed with correct historical/live labels.
9. The architecture page reflects the actual implementation.
10. The database comparison page accurately explains the differences.
11. Demo Mode works as a guided interactive demonstration.
12. The project builds successfully.

At the end, provide:

* What was implemented.
* Files created or modified.
* How to start the backend.
* How to start the frontend.
* The local URL.
* How to run the mentor demonstration.
* Which features are fully functional.
* Which features remain unavailable and why.
* Test and build results.

Final instruction: Prioritize a fully functional, technically accurate frontend over visual decoration. The goal is to make NEXUS understandable and demonstrable to someone who has never seen the codebase, while allowing them to verify that the underlying database operations and reliability mechanisms are real.
````

### Prompt 19

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-07 01:44 (local time; 20:14 UTC, from the session transcript)
- **Notes:** Pasted with no other text; extracted verbatim from the session transcript by script (not retyped). Sent after the Prompt 18 dashboard was finished and tested. Covers engine performance work, the NEXUS X-RAY diagnostics system and their integration into `frontend/` and `src/cmd/nexus-api`. Full text as sent:

````text
NEXUS — Complete Engineering Optimization, X-RAY Diagnostics & Frontend Integration
Project Objective
We are taking NEXUS to its next engineering stage.
NEXUS is a custom persistent key-value database engine written in Go. Its core implementation includes:

* Append-only binary log.
* In-memory hash index.
* Custom binary storage format.
* Checksummed frames.
* Sequence numbers.
* Explicit synchronization before write acknowledgement.
* PUT, GET, DELETE and BATCH operations.
* Sequential log recovery.
* Crash-safe compaction.
* Database inspection and integrity checking.
* Independent cross-checking.
* Torture testing and crash scenario validation.
* Benchmarking tools.

The core engine has already undergone extensive development, hardening and testing.
We now want to accomplish THREE major objectives:

1. Optimize the existing NEXUS database engine based on measurable performance bottlenecks.
2. Implement NEXUS X-RAY, a comprehensive database diagnostics and explainable error reporting system.
3. Integrate both improvements into our EXISTING frontend, making everything interactive and demonstrable.

The final result should be a polished, technically impressive database platform where a user can operate NEXUS, inspect its internal architecture, understand performance, diagnose failures, execute recovery and observe actual results.
This is not a request to create a new database engine or a separate frontend.
Preserve the existing project, architecture, frontend design and working functionality. Extend and improve them.
PART 1 — INITIAL REPOSITORY AND ARCHITECTURE AUDIT
Before modifying any code, inspect the complete repository.
1.1 Understand the database engine
Read and inspect:

* README.md
* ARCHITECTURE.md
* BREAKING.md
* docs/ARCHITECTURE.md
* docs/FRONTEND_INTEGRATION.md
* docs/PROJECT_AUDIT.md
* docs/FAILURE_GUARANTEES.md
* docs/RECOVERY.md
* docs/INDEPENDENT_VERIFICATION.md
* docs/TORTURE_ARCHITECTURE.md
* docs/TORTURE_CLI.md
* docs/TORTURE_TEST_PLAN.md
* docs/TORTURE_RESULTS.md
* docs/results/final/
* docs/results/validation/

Also inspect all relevant Go packages, tests, CLI commands, benchmarks and frontend files.
Verify the actual implementation rather than relying exclusively on documentation.
1.2 Understand the current frontend
Inspect:

* Existing frontend framework and dependencies.
* Current routing structure.
* Sidebar and navigation.
* Existing pages and components.
* Design system and colour palette.
* API integration, if implemented.
* Backend adapter, if implemented.
* Current dashboard functionality.
* Existing loading, error and disconnected states.

Do not discard or replace the existing frontend.
Preserve its current monochromatic visual identity and improve its existing structure where necessary.
1.3 Produce an implementation map
Before implementation, identify:

1. Existing functionality.
2. Existing functionality that needs optimization.
3. Missing backend integration.
4. Missing frontend functionality.
5. Existing tests and validation tools.
6. Compatibility risks.
7. Files that will need modification.

Create a concise implementation plan and then proceed with the work in controlled stages.
Do not stop after producing the plan.
PART 2 — NEXUS PERFORMANCE OPTIMIZATION
2.1 Objective
Make NEXUS as efficient as reasonably possible for its intended workload without compromising:

* Data durability.
* Crash consistency.
* Recovery correctness.
* Batch atomicity.
* Database integrity.
* Windows compatibility.
* Existing database format compatibility.

Do not chase benchmark numbers by weakening guarantees.
We want measurable engineering improvements, not superficial optimizations.
2.2 Establish a performance baseline
Before modifying the engine, benchmark the current implementation.
Measure:
Write performance

* PUT throughput.
* DELETE throughput.
* BATCH throughput.
* Write latency.
* Synchronization overhead.

Read performance

* GET throughput.
* Read latency.
* Index lookup performance.

Recovery

* Database opening time.
* Recovery time with increasing log sizes.
* Recovery performance at 100K, 1M and larger supported key counts.

Compaction

* Compaction duration.
* Storage reduction.
* Temporary disk space.
* Write blocking duration.
* Disk I/O.

Memory

* Memory consumption.
* Allocations per operation.
* Index memory consumption.
* Memory consumption as key count increases.

Concurrency

* Concurrent readers.
* Concurrent writers.
* Mixed read/write workloads.
* Mutex contention.
* Blocked goroutines.

Use Go profiling tools where appropriate:

* CPU profiling.
* Heap profiling.
* Allocation profiling.
* Mutex profiling.
* Block profiling.

Record the hardware, OS, Go version and workload configuration.
Never compare results from different workloads as if they were directly equivalent.
2.3 Optimization A — Write path and Group Commit
Inspect the existing synchronization strategy.
Currently, NEXUS explicitly synchronizes durable writes before acknowledging them.
Investigate group commit.
The objective is to allow multiple write operations to share a synchronization operation while preserving the guarantee that no write is acknowledged as durable before the required synchronization completes.
Evaluate:

* Write batching.
* Synchronization frequency.
* Concurrent writer coordination.
* Group commit scheduling.
* Failure handling.
* Acknowledgement ordering.
* Shutdown behavior.
* Crash consistency.

Do not introduce arbitrary delays that make individual writes unpredictable.
Any new group commit mechanism must have clearly documented durability semantics.
Benchmark it against the original implementation.
2.4 Optimization B — Concurrency and locking
Inspect the current global sync.RWMutex.
Determine whether lock contention is a significant performance bottleneck.
Evaluate:

* Finer-grained locking.
* Sharded index locking.
* Read/write concurrency.
* Write serialization.
* Compaction interaction.
* Recovery interaction.

Do not replace the global mutex simply because sharded locking sounds faster.
Only make changes supported by profiling evidence.
Verify that concurrent PUT, GET, DELETE and BATCH operations cannot corrupt the index or persistent log.
Run the race detector where the environment supports it.
2.5 Optimization C — In-memory hash index
Analyze the memory efficiency of the existing hash index.
Investigate:

* Key storage overhead.
* Value offset representation.
* Hash map allocations.
* Object overhead.
* Unnecessary copying.
* Memory fragmentation.
* Garbage collection pressure.

Explore optimizations that reduce memory consumption without sacrificing lookup correctness.
Benchmark at multiple database sizes.
Do not introduce a complex custom hash table unless it produces a measurable benefit over Go's built-in map.
2.6 Optimization D — Serialization and frame processing
Inspect:

* Frame encoding.
* Frame decoding.
* Checksums.
* Payload construction.
* Key and value allocation.
* Buffer reuse.
* Binary serialization.

Reduce unnecessary allocations and copying where safe.
Ensure that:

* Checksums are still calculated correctly.
* Frame boundaries remain validated.
* Payload lengths remain validated.
* Endianness remains unchanged.
* Existing database files remain compatible.

Do not change the on-disk format just to obtain a small performance improvement.
Any necessary format change must be versioned and documented.
2.7 Optimization E — Recovery performance
Optimize sequential recovery.
Investigate:

* Buffered reads.
* Allocation reduction.
* Frame validation overhead.
* Index reconstruction.
* Large log scanning.
* Memory growth during recovery.

Measure recovery performance at increasing database sizes.
Preserve:

* Superblock validation.
* Frame validation.
* Checksum validation.
* Sequence validation.
* Correct replay ordering.
* Damaged-tail handling.
* Existing corruption policies.

Recovery must never silently skip invalid data to make startup faster.
2.8 Optimization F — Compaction
Inspect the complete compaction implementation.
Measure:

* Time taken.
* Storage before and after.
* Temporary storage requirements.
* I/O amplification.
* Lock duration.
* Memory consumption.

Investigate ways to reduce unnecessary I/O and improve compaction efficiency.
Preserve crash-safe file replacement and recovery behavior.
Do not compromise the existing `.compact` and `.old` recovery protocol without a fully tested replacement.
2.9 Optimization acceptance criteria
For every optimization:

1. Record the original baseline.
2. Identify the actual bottleneck.
3. Implement one change at a time.
4. Run unit tests.
5. Run relevant integration tests.
6. Run crash scenarios.
7. Run torture testing.
8. Run benchmarks using the same workload.
9. Compare results.
10. Document trade-offs.

If an optimization does not produce a meaningful improvement, revert it rather than retaining unnecessary complexity.
Create:
`docs/PERFORMANCE_OPTIMIZATION.md`
Include:

* Original architecture.
* Identified bottlenecks.
* Changes made.
* Before/after measurements.
* Correctness verification.
* Known limitations.
* Future optimization opportunities.

PART 3 — NEXUS X-RAY DIAGNOSTIC SYSTEM
3.1 Objective
Introduce a feature named:
NEXUS X-RAY
X-RAY is a diagnostic and explainable error reporting system.
Its purpose is to help users understand what happened inside the database when something goes wrong.
Instead of simply showing:
`checksum mismatch`
X-RAY should explain:

* What happened.
* Where it happened.
* Why it happened, where determinable.
* Which data may be affected.
* Whether recovery is possible.
* What NEXUS actually did.
* What the user should do next.

It should provide both human-readable explanations and technical diagnostic information.
3.2 Structured diagnostic architecture
Create a structured diagnostic model.
Each diagnostic should support the following fields where applicable:

* Diagnostic ID.
* Severity.
* Error category.
* Error title.
* Human-readable explanation.
* Technical explanation.
* Database path or file identifier.
* Byte offset.
* Frame number.
* Sequence number.
* Expected value.
* Actual value.
* Detection timestamp.
* Affected component.
* Data impact assessment.
* Recovery status.
* Recommended action.
* Original underlying error.

Severity levels:

* INFO
* WARNING
* ERROR
* CRITICAL

Do not make every error critical.
Severity should reflect the actual impact.
Use structured error types internally.
Preserve original low-level errors for debugging.
Avoid building the entire diagnostic system around string matching.
3.3 Diagnostic categories
Integrate X-RAY with the actual database validation, recovery and storage code.
Cover these conditions wherever they can actually be detected.
Superblock problems

* Invalid magic number.
* Unsupported format version.
* Superblock checksum mismatch.
* Invalid metadata.
* Corrupted superblock.

Frame problems

* Invalid frame magic.
* Invalid frame type.
* Invalid frame length.
* Invalid payload length.
* Frame checksum mismatch.
* Incomplete frame.
* Truncated frame.

Sequence and operation problems

* Unexpected sequence number.
* Invalid operation type.
* Invalid operation payload.
* Invalid key length.
* Invalid value length.
* Invalid batch structure.

Recovery problems

* Corruption at the end of the log.
* Corruption inside previously committed data.
* Recovery stopped unexpectedly.
* Recovery refused.
* Recovery partially completed.
* Truncation performed.
* Index reconstruction failure.

Compaction problems

* Compaction file creation failure.
* Write failure.
* Synchronization failure.
* File replacement failure.
* Reopen failure.
* Cleanup failure.

Operational problems

* Database lock conflict.
* Permission failure.
* File I/O error.
* Database path unavailable.
* Storage capacity exceeded.

Only report conditions that the implementation can genuinely identify.
Do not infer the cause of an error when the evidence only establishes its symptoms.
3.4 Explainable error reporting
Every diagnostic should have two presentation levels.
Level 1 — Simple explanation
Example:
Frame checksum mismatch
"The checksum stored in this database frame does not match the checksum calculated from its contents. The frame may have been corrupted or modified."
Level 2 — Technical explanation
Display:

* Frame sequence.
* Byte offset.
* Expected checksum.
* Actual checksum.
* Frame type.
* Payload length.
* File location.
* Original error.

Use real values obtained from the engine.
The example above is illustrative. Never display fabricated diagnostic values as real findings.
3.5 Recovery-aware diagnostics
X-RAY must integrate closely with the existing recovery engine.
When recovery encounters an error, report:

* Where scanning stopped.
* Which frame failed.
* How many valid records were replayed, if measurable.
* Whether the database was modified.
* Whether truncation occurred.
* Whether the index was rebuilt.
* Whether recovery completed successfully.
* Whether data may have been lost.

Clearly distinguish:

* Detected.
* Recovered.
* Repaired.
* Partially recovered.
* Recovery refused.
* Unrecoverable.

Do not claim a record was recovered unless the resulting database state has been verified.
Do not silently truncate or modify files during read-only diagnostics.
3.6 X-RAY CLI integration
Add a CLI command using the existing CLI conventions.
Proposed interface:
`kvdb xray <database-path>`
Support useful options where appropriate:

* Human-readable output.
* JSON output.
* Read-only diagnostics.
* Verbose technical information.

The read-only mode must never modify the database.
If a diagnostic requires recovery or modification, make that a separate explicit operation.
Provide clear command help and examples.
Verify the exact command syntax through the actual implementation.
PART 4 — INTEGRATION WITH THE EXISTING FRONTEND
This is a major requirement.
We already have an existing NEXUS frontend.
Do not build a second frontend or replace the current dashboard.
Extend the existing project, reuse its components, maintain its navigation and preserve its visual identity.
4.1 Design language
Preserve the existing monochromatic design:

* Main background: `#111111`
* Sidebar: `#151515`
* Cards: `#1B1B1B`
* Elevated surfaces: `#222222`
* Borders: `#303030`
* Primary text: `#F2F2F2`
* Secondary text: `#A0A0A0`
* Muted text: `#707070`

Use restrained green and red for meaningful status indicators.
Maintain:

* Consistent spacing.
* Clean typography.
* Responsive layouts.
* Minimalist cards.
* Clear technical visualizations.
* Subtle transitions.
* Good information hierarchy.

No unnecessary gradients, neon effects, excessive glassmorphism or decorative animation.
The frontend must feel like a serious database engineering product.
4.2 Navigation
Integrate the following sections into the existing sidebar.

1. Overview
2. Database Explorer
3. Storage Engine
4. Recovery
5. Torture Lab
6. Benchmark
7. Architecture
8. Database Comparison
9. X-RAY

If some of these already exist, extend them instead of duplicating them.
4.3 Overview dashboard
Display actual database information:

* Database health.
* Database status.
* Total keys.
* Storage size.
* Current sequence number, if available.
* Last operation.
* Recovery status.
* Compaction status.
* Integrity status.
* Recent diagnostics.
* Performance information.

Add an X-RAY alert summary.
For example:

* 0 critical issues.
* 1 warning.
* 2 informational findings.

Only calculate these counts from actual diagnostic results.
Show the latest diagnostic findings and allow navigation to their full details.
4.4 Performance dashboard
Create a dedicated performance section or integrate performance cards into the existing Benchmark page.
Display:

* PUT throughput.
* GET throughput.
* DELETE throughput.
* BATCH throughput.
* Latency percentiles where measured.
* Recovery duration.
* Compaction duration.
* Memory consumption.
* Allocation counts.
* Lock contention where measurable.

Add before-and-after comparisons for optimization changes.
Clearly distinguish:

* Historical benchmark data.
* Current benchmark results.
* Estimated performance.
* Actual measurements.

Never mix these categories.
Provide an optimization history showing which changes improved which metrics.
4.5 X-RAY frontend page
Build a complete X-RAY diagnostic interface.
Top summary
Show:

* Overall database health.
* Critical findings.
* Errors.
* Warnings.
* Informational findings.
* Last diagnostic scan.

Diagnostic timeline
Display findings chronologically.
Each finding should include:

* Time.
* Severity.
* Category.
* Short title.
* Affected component.
* Resolution status.

Diagnostic cards
Each card should show:

* Error title.
* Severity.
* Simple explanation.
* Technical details expandable section.
* File and byte offset.
* Expected and actual values.
* Data impact.
* Recovery status.
* Recommended action.

Filters
Allow filtering by:

* Severity.
* Category.
* Date.
* Recovery status.
* Component.

Actions
Provide buttons for:

* Run diagnostics.
* View technical details.
* View affected frame information.
* Navigate to recovery.
* Export diagnostic report.

Only provide actions that are implemented and safe.
Read-only diagnostics must be the default.
Require explicit confirmation for destructive or modifying operations.
4.6 Recovery frontend integration
Connect X-RAY findings with the existing Recovery page.
When a user selects a diagnostic finding, provide a link to the relevant recovery information.
Show:

* Detected corruption.
* Location.
* Recovery process.
* Valid records replayed.
* Truncation details.
* Final status.
* Verification result.

If recovery is unavailable, explain why.
Do not fabricate recovery progress.
4.7 Torture Lab integration
Preserve the existing Torture Lab functionality.
Integrate X-RAY where appropriate.
If a torture scenario produces an error:

* Display the actual test failure.
* Display relevant diagnostic information.
* Link to the affected database or scenario.
* Explain what was detected.
* Show whether the test passed, failed or was inconclusive.

Do not classify every expected simulated corruption as an actual production database failure.
Keep test-environment findings separate from live database health.
4.8 Architecture visualization
Update the existing Architecture page.
Show where X-RAY sits in the architecture.
The diagram should include:
User/CLI/Frontend
→ Database API
→ Storage Engine
→ Binary Log and Hash Index
And the diagnostic integration:
Storage Validation
→ Structured Errors
→ X-RAY Diagnostic Engine
→ Diagnostic Report
→ Frontend
Also show how Recovery and X-RAY interact.
Allow users to click components and view explanations and relevant source files.
The diagram must accurately reflect the implementation.
PART 5 — BACKEND AND API INTEGRATION
The frontend must use real database operations and diagnostic results.
Inspect the existing backend adapter and API contract.
If an HTTP adapter is already implemented, extend it.
If not, create a separate Go standard-library HTTP adapter around the existing database APIs.
Do not duplicate the database engine.
Do not implement database logic in JavaScript.
Potential API capabilities include:
Database

* Open database.
* Close database.
* PUT.
* GET.
* DELETE.
* BATCH.
* DUMP.
* CHECK.

Performance

* Run supported benchmarks.
* Retrieve historical results.
* Retrieve current measurements.

Diagnostics

* Run X-RAY.
* Retrieve diagnostic findings.
* Retrieve diagnostic details.
* Export diagnostic reports.

Recovery

* Start supported recovery operation.
* Retrieve recovery status.
* Retrieve recovery report.

Torture

* Start supported torture tests.
* Retrieve execution status.
* Retrieve results.
* Retrieve logs.

These are required capabilities to evaluate, not permission to invent endpoints without implementing them.
For each implemented endpoint:

* Define request schema.
* Define response schema.
* Validate inputs.
* Return structured errors.
* Handle concurrent requests safely.
* Document its behavior.
* Add integration tests.

Use safe process management for external tools.
Never allow arbitrary filesystem access through user-controlled paths.
Do not expose unrestricted shell execution through the frontend.
Long-running tasks should not block ordinary database operations.
If background execution is needed, implement controlled task management and cancellation where safe.
PART 6 — DATABASE COMPARISON PAGE
Preserve and improve the existing Database Comparison page.
Compare NEXUS with:

* Bitcask.
* Redis.
* RocksDB.
* LevelDB.

Explain differences in:

* Storage architecture.
* Index structure.
* Write path.
* Read path.
* Recovery.
* Compaction.
* Durability.
* Intended use case.

Be particularly honest about Bitcask because its broad append-only log and in-memory index architecture is similar to NEXUS.
Highlight NEXUS's implementation-specific decisions:

* Custom binary storage format.
* Superblock validation.
* Checksummed frames.
* Sequence validation.
* Explicit sync-before-acknowledgement.
* Batch encoding.
* Recovery validation.
* Crash-safe compaction.
* Independent verification.
* Explainable diagnostics through X-RAY.

Do not claim NEXUS is universally faster or superior.
Explain its engineering goals and trade-offs.
PART 7 — TESTING AND REGRESSION PROTECTION
All optimization and diagnostic work must preserve existing reliability.
7.1 Existing tests
Run the existing Go test suite and all relevant validation tools.
Preserve the existing official crash scenarios.
Run the torture framework after meaningful storage engine changes.
Do not alter historical logs or existing evidence.
7.2 X-RAY tests
Create controlled corrupted copies of disposable database files.
Test:

* Corrupted superblock.
* Invalid magic number.
* Unsupported version.
* Checksum mismatch.
* Invalid frame length.
* Incomplete final frame.
* Corrupted committed frame.
* Invalid sequence number.
* Invalid operation payload.
* Compaction failure where reproducible.
* Lock conflict.

Verify:

* Correct diagnosis.
* Correct byte offset.
* Correct severity.
* Accurate explanation.
* Correct recovery classification.
* No false recovery claims.
* No modification in read-only mode.

7.3 Performance regression
After every optimization:

* Re-run comparable benchmarks.
* Compare latency and throughput.
* Compare memory.
* Compare recovery.
* Compare compaction.
* Run concurrency tests.
* Run crash tests.

If performance improves but reliability regresses, reject the optimization.
7.4 Frontend testing
Verify:

* Frontend production build.
* Linting.
* API integration.
* Database operation handling.
* Diagnostic display.
* Diagnostic filtering.
* Recovery integration.
* Torture integration.
* Disconnected backend behavior.
* Error handling.
* Responsive layouts.

PART 8 — DOCUMENTATION
Create or update:

1. `docs/PERFORMANCE_OPTIMIZATION.md`
2. `docs/XRAY_ARCHITECTURE.md`
3. `docs/XRAY_DIAGNOSTICS.md`
4. `docs/XRAY_API.md`
5. `docs/FRONTEND_INTEGRATION.md`
6. `docs/ARCHITECTURE.md`

Document:

* What changed.
* Why it changed.
* Architecture decisions.
* API contract.
* Diagnostic categories.
* Recovery behavior.
* Performance measurements.
* Known limitations.
* How to run everything locally.

Do not delete or rewrite historical evidence.
PART 9 — NON-NEGOTIABLE RULES

1. Do not rewrite the database engine from scratch.
2. Do not replace the existing frontend.
3. Do not break existing working pages.
4. Do not change the storage format without a documented compatibility plan.
5. Do not weaken durability to improve throughput.
6. Do not silently discard corrupted records.
7. Do not invent diagnostic findings.
8. Do not fabricate benchmark results.
9. Do not confuse historical and current measurements.
10. Do not modify historical validation evidence.
11. Do not remove existing tests.
12. Do not introduce unnecessary dependencies.
13. Preserve Windows compatibility.
14. Do not commit or push to GitHub.
15. Do not reset, rebase, switch branches or discard existing user changes.
16. Preserve the current branch and working tree.
17. Keep the implementation modular and maintainable.
18. Make backups before any potentially destructive operation.
19. Never run destructive recovery or corruption demonstrations against the actual user database.
20. Do not stop at mockups or placeholder functionality.

PART 10 — FINAL COMPLETION CRITERIA
The project is complete only when the following are achieved.
Database optimization

* Existing engine audited.
* Bottlenecks measured.
* Optimizations implemented where justified.
* Before-and-after benchmarks documented.
* Existing reliability guarantees preserved.

X-RAY

* Structured diagnostic system implemented.
* Real storage errors detected and explained.
* Diagnostic locations and technical information displayed.
* Recovery-aware reporting implemented.
* CLI support available.
* Read-only diagnostics verified.
* Controlled corruption tests passing.

Frontend

* Existing frontend preserved.
* X-RAY page integrated.
* Performance dashboard integrated.
* Architecture updated.
* Recovery connected to diagnostics.
* Torture results connected to diagnostics.
* Actual database operations functional.
* No fabricated metrics or placeholder success states.

Build and validation

* Go build succeeds.
* Go tests pass.
* Go vet passes.
* Frontend lint passes.
* Frontend production build succeeds.
* API integration tests pass.
* Relevant torture and crash scenarios pass.

FINAL REPORT
At the end, provide:

1. Complete list of performance optimizations.
2. Before-and-after performance table.
3. X-RAY diagnostic capabilities.
4. Frontend pages and components modified.
5. Backend endpoints implemented.
6. Tests executed and results.
7. Build status.
8. How to start the backend.
9. How to start the frontend.
10. Local frontend URL.
11. A complete mentor demonstration sequence.
12. Remaining limitations and unimplemented functionality.

Clearly identify anything that could not be completed.
Do not claim completion merely because the UI renders.
FINAL ENGINEERING DIRECTION
The objective is to make NEXUS a database engine that is not only functional and reliable but also measurable, inspectable and understandable.
We want three complementary strengths:
PERFORMANCE: A carefully optimized storage engine with evidence-backed improvements.
RELIABILITY: Existing durability, recovery and crash-safety guarantees preserved.
OBSERVABILITY: X-RAY explains what happens inside the database and makes errors understandable.
The frontend should bring these together into one coherent platform.
A mentor should be able to open NEXUS, insert data, inspect its storage architecture, measure performance, deliberately inspect a corrupted test database, understand the resulting X-RAY report, observe recovery and inspect the final integrity result.
Build this as a complete engineering platform, not a collection of disconnected pages.
Prioritize correctness, measurable improvements and real integration over visual decoration.
````

### Prompt 19a

- **Tool:** Claude Code (Claude desktop app, Code tab), model Claude Opus 5.5
- **Approximate timestamp:** 2026-11-07 01:54 (local time; 20:24 UTC, from the session transcript)
- **Notes:** Sent mid-turn while Prompt 19 was being implemented; extracted verbatim from the session transcript by script (not retyped). Adds the Power-Cut Explorer (`src/internal/powercut`, `src/cmd/nexus-powercut`, Reliability Lab page). Full text as sent:

````text
Implement and integrate Power-Cut Explorer as a separate reliability-testing module. Preserve the existing database engine and torture framework. Record actual write bytes and synchronization events, generate modeled power-loss disk images, and validate each image using the real NEXUS recovery implementation. Integrate its results into the frontend Reliability Lab and connect failures to X-RAY. Clearly document the storage failure model, assumptions, coverage limitations and reproducibility information. Never describe a finite generated test set as exhaustive proof of all possible power-loss states.
````
