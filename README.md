# NEXUS — Kill It and Prove It

A crash-safe embedded key-value database written from scratch in Go (standard
library only), plus the tool that tries to break it.

* **Engine:** an append-only log of checksummed frames plus an in-memory hash
  index. One frame per write; `fsync` before a write is acknowledged.
* **CLI:** `kvdb` with the eight required commands (`open put get del batch
  dump check serve`), plus a read-only `check --no-repair`.
* **Torture tool (official): NEXUS Torture Engine 2.0, `nexus-torture`.** It
  starts the real `kvdb` executable, drives it from concurrent writers and
  readers, kills it at a random moment (`TerminateProcess` on Windows),
  restarts it, and has an independent oracle decide whether the recovered
  state is one a correct database could produce. It also runs the 32 official
  scenarios against the real executable and proves it catches six kinds of
  deliberately broken database ([docs/TORTURE_ARCHITECTURE.md](docs/TORTURE_ARCHITECTURE.md)).
* **Proof:** the 32 official crash scenarios run twice, independently: as
  `nexus-torture scenarios` against the real executable
  ([docs/TORTURE_RESULTS.md](docs/TORTURE_RESULTS.md)), and as Go tests with
  fault injection in [`src/scenarios`](src/scenarios)
  ([docs/results/validation/SCENARIO_RESULTS.md](docs/results/validation/SCENARIO_RESULTS.md)).
* **Independent check:** a teammate's separate implementation of the same
  file format reads and writes NEXUS files byte for byte
  ([docs/INDEPENDENT_VERIFICATION.md](docs/INDEPENDENT_VERIFICATION.md)).

Other documents: [ARCHITECTURE.md](ARCHITECTURE.md) (diagram, proof table,
three guarantees), [BREAKING.md](BREAKING.md) (attacks on the design),
[PROMPTS.md](PROMPTS.md) (every AI prompt), [docs/TEST_PLAN.md](docs/TEST_PLAN.md)
(requirement → test map), [docs/DESIGN.md](docs/DESIGN.md) (full design and
change log), [docs/FAILURE_GUARANTEES.md](docs/FAILURE_GUARANTEES.md)
(every failure case: promise, evidence, limits),
[ARCHITECTURE.md § Design trade-offs](ARCHITECTURE.md#design-trade-offs).
Torture Engine 2.0: [architecture](docs/TORTURE_ARCHITECTURE.md),
[test plan](docs/TORTURE_TEST_PLAN.md), [results](docs/TORTURE_RESULTS.md),
[command line](docs/TORTURE_CLI.md).

## Quick start for judges (clean clone, works offline)

Requires only the Go toolchain (1.22 or newer; developed with 1.27.3). Standard
library only: nothing is downloaded, so it builds with no network. From the
repository root:

```bash
go build -o . ./src/cmd/kvdb ./src/cmd/torture
```

This puts `kvdb` and `torture` in the repository root (`kvdb.exe` and
`torture.exe` on Windows). The same commands work in bash (Linux, macOS, Git
Bash) and in PowerShell (`./torture` runs `torture.exe`):

```bash
./torture --iterations 200 --seed 12345                    # random crash torture; any integer seed
./torture --iterations 200 --seed 12345 --mode compaction
./torture --iterations 200 --seed 12345 --mode recovery-crash
./kvdb check judge.db                                        # after a manual kill: OK
./kvdb dump judge.db
```

Each run ends with `RESULT: PASS` and exits 0. Exit 1 means a database
violation was found: the run stops, prints `FAIL at iteration N` and keeps the
database. Exit 2 means a usage error (for example `--seed 12abc` is refused) or a
tool error that is not a database verdict. On the development laptop, 200
iterations took about 70 s in the default mode and about 160 s with
`--mode compaction`. The step-by-step judging script, including manual kills of
`kvdb serve`, is in [docs/JUDGE_READINESS/FINAL_COMMAND_REFERENCE.md](docs/JUDGE_READINESS/FINAL_COMMAND_REFERENCE.md).

## Build everything

```bash
go build -o . ./src/cmd/kvdb ./src/cmd/torture ./torture2/cmd/nexus-torture ./torture2/mutants/kvdb-mutant ./src/cmd/kvdb-bench
```

This adds `nexus-torture` (Torture Engine 2.0: 12 modes, the 32 scenarios
against the real executable, and mutation testing), `kvdb-mutant`
(deliberately broken builds for mutation testing; the real `kvdb` is never
modified) and `kvdb-bench` (benchmark). `torture` is the first-generation
tool, which takes `--iterations N --seed N [--mode M]` directly. It lived in
`torture/` until 2026-11-07 and moved to `src/cmd/torture/` so that the binary
`./torture` no longer collides with a folder of the same name.

## Judge demonstration (copy and paste)

PowerShell on Windows. Use `./kvdb` and `./nexus-torture` on other systems; in Git
Bash `./kvdb.exe` works too. Every command below has been run on the
development machine. The expected output is shown; numbers marked ≈ vary from
run to run.

```powershell
go build -o . ./src/cmd/kvdb ./src/cmd/torture ./torture2/cmd/nexus-torture ./torture2/mutants/kvdb-mutant ./src/cmd/kvdb-bench

# 1. The eight commands
.\kvdb.exe open demo.db                 # keys=0 recovery_ms=≈2 size=32   (stderr: created a new empty database)
.\kvdb.exe put demo.db apple "red fruit"   # OK
.\kvdb.exe get demo.db apple            # red fruit
.\kvdb.exe get demo.db pear             # NOTFOUND
.\kvdb.exe del demo.db apple            # OK
Set-Content ops.txt "put a 1`nput b 2`ndel a"
.\kvdb.exe batch demo.db ops.txt        # OK 3 ops
.\kvdb.exe dump demo.db                 # b 2
.\kvdb.exe check demo.db                # OK
.\kvdb.exe check demo.db --no-repair    # OK   (stderr: ... file not modified)
"w1 1 put k v`nw1 2 get k" | .\kvdb.exe serve demo.db   # ACK 1 / VALUE 2 v

# 2. Crash torture (Torture Engine 2.0): one line per iteration, then RESULT: PASS ... and PERF: ...
.\nexus-torture.exe run --seed 12345 --iterations 500                     # ≈3 min
.\nexus-torture.exe run --seed 2026 --iterations 100 --mode large-batch
.\nexus-torture.exe run --seed 2026 --iterations 100 --mode recovery-crash
.\nexus-torture.exe run --seed 2026 --iterations 100 --mode disk-full
.\nexus-torture.exe modes                                                 # all 12 modes

# 2b. The 32 official scenarios against the real executable: SCENARIOS RESULT: PASS  32 passed ...
.\nexus-torture.exe scenarios                 # quick sizes, ≈7 min
.\nexus-torture.exe scenarios --full          # official sizes (1,000,000 keys, 500 kills)

# 2c. The tester catches broken databases: MUTATION RESULT: every mutation detected, control passed
.\nexus-torture.exe mutants --mutant .\kvdb-mutant.exe

# 3. A torn tail by hand (scenario F4): read-only check reports it, check repairs it
$f = Get-Item demo.db; $s = [IO.File]::Open($f.FullName, 'Open'); $s.SetLength($f.Length - 1); $s.Close()
.\kvdb.exe check demo.db --no-repair    # NEEDS REPAIR: open would discard a torn N-byte tail at offset X (rule C4) ...  (exit 4, file unchanged)
.\kvdb.exe check demo.db                # OK   (stderr: recovery: discarded a torn N-byte tail (rule C4); it was never acknowledged)

# 4. All tests, the 32 scenarios in full mode, the 1M acceptance workload
go test ./...
$env:KVDB_SCENARIOS="full"; go test ./src/scenarios -run TestGroup -v -timeout 120m; Remove-Item Env:KVDB_SCENARIOS
$env:KVDB_ACCEPTANCE="1"; go test ./src/scenarios -run TestAcceptanceMillionPutDelete -v -timeout 60m; Remove-Item Env:KVDB_ACCEPTANCE

# 5. Cross-check against the independent implementation (Git Bash)
sh crosscheck/run.sh                    # 9 PASS lines, "crosscheck: 9 checks, 0 failed"

# 6. Benchmark with machine and configuration recorded (≈4 min; numbers vary by disk)
.\kvdb-bench.exe --n 100000 --runs 3 --md bench.md --json bench.json

# 7. The whole final validation, logs in docs/results/final/ (Git Bash, ≈2 h;
#    the recorded run used run-final.sh steps 01-08 + run-targeted.sh, ≈1 h)
bash docs/results/final/run-final.sh
```

Each torture run ends with a line like
`RESULT: PASS  mode=default seed=12345  passed=500 failed=0 inconclusive=0 of 500  acked=… rejected=… unknown=… …`
and exits 0 (PASS), 1 (FAIL), 2 (tool error) or 3 (INCONCLUSIVE, never a
pass). The same seed reproduces the same requests and kill schedule (timing
jitter can change how far a writer got before the kill, so counts differ
slightly). A failure prints a ready-to-paste repro command and keeps the
evidence ([docs/TORTURE_CLI.md](docs/TORTURE_CLI.md)).
The results of the validation run are in [§ Results](#results).

## Run each command

On Windows use `.\kvdb.exe` (PowerShell) or `kvdb.exe`; elsewhere `./kvdb`.

| Command | Output | Exit status |
|---|---|---|
| `kvdb open <path> [--max-bytes N]` | `keys=<n> recovery_ms=<ms> size=<bytes>`; creates the database if needed and runs recovery | 0 |
| `kvdb put <path> <key> <value>` | `OK` once the write is durable | 0, or 1 on error |
| `kvdb get <path> <key>` | the value, or `NOTFOUND` | 0 |
| `kvdb del <path> <key>` | `OK` (deleting a missing key is not an error) | 0 |
| `kvdb batch <path> <file>` | `OK <n> ops`; the file has one op per line, `put k v` or `del k`, applied atomically | 0, or 1 |
| `kvdb dump <path>` | one `key value` line per key, sorted by key (byte order) | 0 |
| `kvdb check <path>` | `OK`, or `CORRUPT: <reason>` / `BAD FORMAT: <reason>` / `ERROR: <reason>` | 0 if OK, else 1 |
| `kvdb check <path> --no-repair` | `OK`, or `NEEDS REPAIR: <what open would change>`, or the same `CORRUPT` / `BAD FORMAT` / `LOCKED` / `ERROR` lines | 0 if OK, 4 if needs repair, else 1 |
| `kvdb serve <path> [--max-bytes N]` | reads requests on stdin; prints `ACK <op-id>` after each durable write | 0 at end of input |

Details:

* Recovery notes (a torn tail cut off, an interrupted compaction completed, a
  new database created) are printed on **stderr**, so stdout stays parseable.
* `--max-bytes N` caps the data file at N bytes for that run (0 = unlimited;
  1–31 is rejected). The cap is not stored in the file [ASSUME], so pass it to
  every writing command. A write that does not fit fails with
  `ERROR (full)` and changes nothing.
* Keys on the command line cannot contain spaces in `batch` files and `serve`
  (the value is everything after the key, and may contain spaces). `--`
  ends option parsing, for keys that start with `--`.
* `dump` prints `key value` when both are plain printable text; otherwise both
  are printed as Go-quoted strings (`"k e y" "line1\nline2"`). A line starting
  with `"` is always the quoted form.
* **`check` vs `check --no-repair`.**
  - `check` opens the database exactly like `open`, so it **may change the
    file**: it cuts a torn, never-acknowledged tail, completes an interrupted
    compaction and deletes leftover temp files (reported on stderr). It then
    re-reads the **whole** file: superblock, every frame header CRC, payload
    CRC and entry CRC, sequence numbers, no torn tail left, the index rebuilt
    from disk equal to the in-memory index, and every live value readable.
  - `check --no-repair` **never modifies the database**. It applies the same
    rules but only reads, and says what `open` would change
    (`NEEDS REPAIR: …`, exit 4). It does not truncate, move, delete, create,
    write or sync any database or temp file. The only file it may create is
    the empty lock file `<path>.lock`.
  - Neither ever "repairs" corruption: damage that a crash cannot explain is
    reported (`CORRUPT`), and the file is left as it is.
* Exit status: 0 success, 1 failed with nothing changed, 2 usage error, 3
  outcome unknown (the write may or may not be durable; never printed as OK),
  4 needs repair (`check --no-repair` only; nothing changed).

### `serve` protocol

One request per line. `<writer-id> ` may be omitted (then the writer is `-`).

```
<writer-id> <op-id> put <key> <value>
<writer-id> <op-id> del <key>
<writer-id> <op-id> get <key>
<writer-id> <op-id> batch <n>        followed by exactly n lines: "put k v" or "del k"
<writer-id> <op-id> compact
```

Replies, one line each, flushed immediately:

```
ACK <op-id>                          the write (put/del/batch/compact) is durable
VALUE <op-id> <value>                get (value Go-quoted if not plain text)
NOTFOUND <op-id>                     get, key absent
ERR <op-id> <class> <message>        failure
```

* `ACK` is printed only after the engine call returned success, which happens
  only after `fsync` (`FlushFileBuffers`) of the frame returned. It is never
  printed for an error, including `unknown`.
* Error classes: `full`, `invalid`, `too-large`, `failed`, `closed`,
  `protocol` mean **nothing changed**. `unknown` means the write may or may
  not have become durable (all or nothing). `io` is any other OS error;
  treat it as unknown.
* Requests of one writer run strictly in order, each after the previous one
  finished. Different writers run concurrently (the engine serializes commits
  with one lock).
* Lines may end in CRLF. Lines up to 1 MiB + 5 KiB are accepted (a 1 MiB value
  fits). A final line without a newline is a request whose sender died while
  writing it: it is reported as `ERR - protocol` and never executed. An
  incomplete batch (input ends inside it) is never executed.

## What NEXUS guarantees, in plain language

* **Durability and ACK.** A write is acknowledged (`OK` from the CLI, `ACK`
  from `serve`, `nil` from the API) only after its bytes are in the file and
  `FlushFileBuffers` / `fsync` has returned. If the process is killed at any
  moment after that, the write is there after restart. If it is killed
  before, the write is either completely there or completely absent, never
  half there. If the flush itself fails, nothing is acknowledged, the answer
  is "outcome unknown", and that handle refuses further writes.
* **Batch atomicity.** A batch of up to 10,000 operations is written as one
  frame protected by one checksum. After a crash either every operation is
  visible or none is. Inside a batch, operations apply in order and the last
  one on a key wins. Readers never see half a batch.
* **Recovery.** Opening the database replays the log, checking every
  checksum and sequence number. A kill can only damage the last frame, which
  was never acknowledged, so recovery cuts it off. Damage anywhere else cannot
  come from a kill, so it is reported as corruption and the file is left
  untouched. Recovery can itself be killed and rerun any number of times, and
  it reaches the same state. One million keys recover in about half a second.
* **Compaction.** When the file is mostly dead data, live entries are written
  to a new file, sorted by key. That file is sealed (superblock last), synced,
  checked, and swapped in with two renames that never overwrite anything. A
  kill at any step leaves either the old complete file or the new complete
  file as the database. Compaction runs automatically, on `Compact()`, on a
  `serve` `compact` request, and when a write needs it to fit the size cap.
* **Size cap (`--max-bytes`).** A write that would make the file larger than
  the cap is refused with `full` before any byte is written (if compaction can
  make room, it compacts first). Previously acknowledged data stays intact,
  the database stays readable, and after deletes free space, writes succeed
  again.

## Torture tool: NEXUS Torture Engine 2.0

```bash
nexus-torture run --seed 12345 --iterations 500
nexus-torture run --seed 2026 --iterations 100 --mode same-key
nexus-torture run --seed 777 --iteration 3 --from-db <evidence>/pre/db.kv   # replay one iteration
nexus-torture scenarios [--group A] [--only A2,B6] [--full]
nexus-torture mutants --mutant ./kvdb-mutant
```

How one iteration works: start the real `kvdb serve`; writers (and readers)
send puts, deletes (including deletes of missing keys), batches of 2–10,000
operations, gets and compactions, with values up to 1 MiB that each embed
their request ID; kill the process at a random moment (or close it
gracefully, sometimes killing it during the close); restart through the real
CLI (`check --no-repair`, `open`, `check`, `dump`); then the **oracle**
searches for a legal execution that explains the recovered state and every
read. Only an oracle PASS is a pass. A request counts as acknowledged only
when its complete reply line arrived; a request with no reply is *unknown*
and may be present or absent, but never half present.

`--seed` is mandatory; every failure is reproducible from seed and
iteration. The 12 modes (`nexus-torture modes`) cover batches of 10 and
10,000, 1 MiB values, disjoint and same-key writers, readers during batches,
compaction, kills during recovery, the size cap, graceful close, and long
mixed runs. Each run writes `events.jsonl`, `results.jsonl` and
`summary.json`, and on failure keeps `failures/iter-N/` with the full
history, a minimized failing history and the database files. Design:
[docs/TORTURE_ARCHITECTURE.md](docs/TORTURE_ARCHITECTURE.md). What it tests:
[docs/TORTURE_TEST_PLAN.md](docs/TORTURE_TEST_PLAN.md). Usage:
[docs/TORTURE_CLI.md](docs/TORTURE_CLI.md). Results:
[docs/TORTURE_RESULTS.md](docs/TORTURE_RESULTS.md).

The first-generation tool in `src/cmd/torture/` is kept because the Go scenario
tests build and use it; its earlier results are in § Results.

## The 32 official crash scenarios

Every scenario is checked twice, by two independent methods.
`nexus-torture scenarios` runs each one against the **real executable**
(real kills, and replay of the exact disk states a kill leaves), records
every kill point and its outcome, and writes a machine-readable result
([docs/TORTURE_TEST_PLAN.md](docs/TORTURE_TEST_PLAN.md),
[docs/TORTURE_RESULTS.md](docs/TORTURE_RESULTS.md)). In addition, every
scenario is a subtest of `go test ./src/scenarios`. Crash points inside
a write are hit exactly with a fault-injecting file system that "crashes"
(stops all further I/O) at a chosen operation. Process-level scenarios start
the real `kvdb` binary and kill it with `TerminateProcess`. After every crash
the database is reopened, fully verified, and compared with the expected
state. Full method and diagnostics per scenario:
[docs/results/validation/SCENARIO_RESULTS.md](docs/results/validation/SCENARIO_RESULTS.md).

| Group | Scenarios | What is attacked | Outcome required (and observed) |
|---|---|---|---|
| A | A1–A7 | kill before, during, after the write and the fsync of a put, a delete, a 1 MiB put | unacknowledged: old value or new value, never garbage; acknowledged: present |
| B | B1–B6 | kill inside a 10-op batch, before/after its commit marker (the frame checksum), after success; put+delete of one key; a 10,000-op batch on a 1M-key database | all or nothing; B5 last-op-wins; recovery < 2 s |
| C | C1–C6 | kill during recovery replay and truncation, during every step of compaction (before, during, after the renames), during close | next open succeeds with the same correct state; exactly one complete file is authoritative; temps cleaned |
| D | D1–D4 | two writers (disjoint keys, same key), a long batch racing single puts, readers during batches | every acknowledged write present; a key holds one writer's whole value; readers never see half a batch |
| E | E1–E5 | size cap during a put, in the middle of a batch, then a kill, during compaction, then deletes and writes again | clean `full` error, nothing changed, data intact, usable again |
| F | F1–F4 | 500 random kills; kills during recovery twice in a row; 1M keys then kill and open; a file truncated by 1 byte by hand | invariant holds every time; idempotent recovery; open < 2 s; at most the unacknowledged tail lost |

Result of the validation run today: **32 of 32 PASS** (details in [§ Results](#results)).

## On-disk format (one page)

One data file. All integers little-endian, all checksums CRC32C (Castagnoli).

```
 offset 0            32                                                     EOF
 +-------------------+-------------------------+----------------------------+
 | SUPERBLOCK 32 B   | FRAME 1                 | FRAME 2 ...                |
 +-------------------+-------------------------+----------------------------+
                     |                         |
                     v                         |
 +----------------------------------+---------------------------------------+
 | FRAME HEADER 32 B                | PAYLOAD (payload_len bytes)           |
 +----------------------------------+---------------------------------------+
                                    | ENTRY 1 | ENTRY 2 | ... | ENTRY n     |
                                    +---------+---------+-----+-------------+
 ENTRY = op(1) key_len(2) val_len(4) entry_crc(4) | key | value
```

| Structure | Bytes | Fields (offset: size) |
|---|---|---|
| Superblock | 32 | 0: magic `KIPDBLOG` (8) · 8: version = 1 (2) · 10: flags = 0 (2) · 12: first_seq (8) · 20: snapshot_end (8) · 28: CRC32C of bytes 0–27 (4) |
| Frame header | 32 | 0: magic `0xD8C3B1A5`, bytes `A5 B1 C3 D8` (4) · 4: type = 1 (1) · 5: reserved = 0 (1) · 6: op_count (2) · 8: seq (8) · 16: payload_len (8) · 24: payload CRC32C (4) · 28: CRC32C of bytes 0–27 (4) |
| Entry | 11 + key + value | 0: op, 1 = PUT, 2 = DEL (1) · 1: key_len 1–1024 (2) · 3: val_len 0–1,048,576, 0 for DEL (4) · 7: CRC32C of bytes 0–6 ‖ key ‖ value (4) |

* Every put, delete and batch is **one frame**. A batch of n operations is one
  frame with n entries. Frame sequence numbers start at `first_seq` and
  increase by 1.
* The frame's `payload_len` + payload CRC is the **commit marker**: a frame
  counts only if its header CRC, payload CRC and every entry CRC verify. There
  is no separate marker record.
* `snapshot_end` marks the end of the frames written by compaction (sorted
  by key). A fresh database has `snapshot_end = 32`. Compaction writes the
  superblock **last**, so a temporary file with a valid superblock is complete.
* Files next to the database: `<path>.lock` (process lock, never deleted),
  and during compaction or creation `<path>.compact`, `<path>.old`,
  `<path>.new`.
* The format is implemented independently by the teammate recovery package;
  both produce byte-identical files ([docs/INDEPENDENT_VERIFICATION.md](docs/INDEPENDENT_VERIFICATION.md)).

Byte-level detail and decode order: [docs/FILE_FORMAT.md](docs/FILE_FORMAT.md).

## Recovery algorithm in plain English

When a database is opened (by any command except `check --no-repair`, which
only reads):

1. **Take the lock.** Open `<path>.lock` exclusively (Windows share mode 0).
   If another process holds it, retry for about a second, then fail with
   "locked" without touching any file.
2. **Decide which file is the database** (authority rules A1–A7). If `<path>`
   has a valid superblock, it is the database and any `.compact`/`.old`/`.new`
   left by a crash are stale. If `<path>` is missing because a crash hit
   compaction between its two renames, the complete file (`.compact` with a
   valid superblock, else `.old`) is moved back into place. If nothing exists,
   a new empty database is created through `.new` and moved into place. A
   `<path>` that exists but is not a database is refused, never overwritten.
3. **Replay the log.** Read frames from offset 32 to the end with a 4 MiB
   buffer. For each frame check the header CRC, the sequence number, the
   payload CRC and each entry CRC; only then apply its entries to the index
   (PUT: key → file offset of the entry; DEL: remove the key). A frame is
   applied completely or not at all.
4. **Handle the end of the file.** A process kill can leave only the last
   frame incomplete, because frames are written one at a time and each is
   synced before the next starts. So if the damage is in the last frame and
   reaches exactly the end of the file (header cut short, damaged header,
   payload cut short, or payload CRC wrong with the frame ending exactly at EOF)
   that frame was never acknowledged: cut the file back to the end of the last
   good frame and `fsync`. Damage with more bytes after it cannot come from a
   kill, so it is reported as corruption and the file is left untouched.
5. **Clean up.** Delete stale temporary files (best effort) and accept writes.

Re-running recovery after a crash at any step reaches the same state: step 2
only moves complete files with non-overwriting moves (`MoveFileExW` with
write-through, never replace-existing), and step 4 truncates to an offset that
depends only on the bytes before it.

## Batch semantics (scenario B5)

Operations inside a batch apply **in order and the last operation on a key
wins**: `put X=1, del X` leaves X absent; `del X, put X=2` leaves X=2;
`put X=1, put X=2` leaves X=2. The whole batch is one frame, so after a crash
it is either entirely present (with this ordering) or entirely absent.
Verified by `TestGroupB/B5` and `TestBatchFileOrderingAndAtomicity`.

## Library API

Package [`src/kvdb`](src/kvdb):

```go
db, err := kvdb.Open(path, maxBytes)         // 0 = unlimited
err = db.Put(key, value)                     // durable when it returns nil
v, err := db.Get(key)                        // kvdb.ErrNotFound if absent
err = db.Delete(key)                         // missing key: nil
err = db.Batch([]kvdb.Op{{Kind: kvdb.OpPut, Key: k, Value: v}, {Kind: kvdb.OpDelete, Key: k2}})
err = db.Compact()
err = db.Close()
st := db.Stats(); rep, err := db.Verify(); err = db.Range(func(k, v []byte) error { ... })
c, err := kvdb.CheckReadOnly(path)           // check --no-repair: never modifies the database
```

Errors (test with `errors.Is`): `ErrNotFound`, `ErrInvalidArgument`,
`ErrTooLarge`, `ErrFull`, `ErrLocked`, `ErrClosed`, `ErrFailed`,
`ErrOutcomeUnknown`, `ErrCorruption`, `ErrBadFormat`.

## Tests

```bash
go test ./...
go vet ./...
```

`go test ./...` takes about 3 minutes: it includes the 32 scenarios in quick mode
(it builds `kvdb` and `torture` into a temporary directory and runs short
torture runs).

Heavier runs:

```bash
bash docs/results/validation/run-validation.sh      # everything below, logs in docs/results/validation/
KVDB_SCENARIOS=full KVDB_SCENARIO_REPORT=docs/results/validation/SCENARIO_RESULTS.md go test ./src/scenarios -run TestGroup -v -timeout 120m
KVDB_ACCEPTANCE=1 go test ./src/scenarios -run TestAcceptanceMillionPutDelete -v -timeout 60m
KVDB_PERF=1 go test ./src/internal/recovery -run TestOpenMillionKeysTiming -v
sh crosscheck/run.sh
```

(In PowerShell set the variables with `$env:KVDB_SCENARIOS="full"` first.)
`go test -race` needs a C compiler, which this machine does not have; it was
not run.

## Results

**Final validation** (2026-11-06, 22:15–23:13 IST, same machine, engine
unchanged since 19:07; logs and every exit code in
[docs/results/final/](docs/results/final/README.md), torture details in
[docs/TORTURE_RESULTS.md](docs/TORTURE_RESULTS.md)):

| Check | Result |
|---|---|
| Clean build, gofmt, vet (4 OSes), `go test ./...`, crosscheck (9/9), targeted tests (375 PASS) | all exit 0 |
| Open 1,000,000 keys (37 MB) | 474 ms |
| 32 official scenarios, Go fault-injection suite, full mode | **32 / 32 PASS** |
| `nexus-torture run --seed 12345 --iterations 500` | **PASS 500/500**, 0 inconclusive; 148,100 acknowledged writes, all verified after restart; 0 atomicity / integrity / recovery failures |
| `nexus-torture scenarios` (quick sizes, real executable) | **32 / 32 PASS**, 1,385 kill points |
| `nexus-torture mutants` | all 6 broken databases detected at iteration 1, control passed |
| Same seed twice | 14,883 requests compared, 0 mismatches |
| `kvdb-bench`, 100,000 keys, median of 3 (every write synced) | put 2,699 ops/s (p50 520 µs), delete 2,668 ops/s, get 165,652 ops/s, 1,000-op batch 1.0 ms p50, reopen 45 ms, compaction 15.5 MB → 12.3 MB in 0.46 s |

Not re-run in that window (covered by the runs below on the same engine
code): the 11 other torture modes, `scenarios --full`, and the 1M PUT/DELETE
acceptance test.

**Earlier the same day** (2026-11-06, 19:11–19:58 IST, current working tree, by
[docs/results/validation/run-validation.sh](docs/results/validation/run-validation.sh);
logs in [docs/results/validation/](docs/results/validation/README.md)).
Machine: Windows 11 Home 10.0.26200, AMD Ryzen 7 5800H, 32 GB RAM, NVMe SSD,
Defender on, Go 1.27.3. The older Phase 4 run (before the Prompt 10
changes) is kept as a historical record in [docs/results/](docs/results/README.md).

| Check | Result today |
|---|---|
| `gofmt -l`, `go vet` (windows, linux, darwin, freebsd), `go build` (incl. linux/arm64) | clean / ok |
| `go test -count=1 ./...` | all packages ok (exit 0) |
| 32 official scenarios, full mode | **32 of 32 PASS** (909 s) — [SCENARIO_RESULTS.md](docs/results/validation/SCENARIO_RESULTS.md) |
| Cross-check with the independent implementation | **9 of 9 PASS** |
| Read-only check, Windows moves, deterministic compaction tests | ok |
| Open 1,000,000 keys (37 MB) | 467 ms |
| 1,000,000 synced PUTs, then open | 6 m 20 s (2,629 ops/s); open 661 ms (limit 2 s) |
| 1,000,000 synced DELETEs | 6 m 24 s; file peaked at 101.7 MB, shrank 9 times by automatic compaction to 1.16 MB; explicit `Compact` → 32 bytes |
| 10,000 PUTs after compaction, reopen, full verify | PASS (10,000 keys, values verified) |

First-generation `torture` tool, same run:

| Torture command | Result | Acked | Rejected | Unknown | Time |
|---|---|---|---|---|---|
| `--iterations 500 --seed 12345` | **PASS 500/500** | 155,520 | 0 | 1,397 | 170 s |
| `--iterations 100 --seed 999 --mode large-values` | **PASS 100/100** | 11,010 | 0 | 158 | 86 s |
| `--iterations 100 --seed 13 --mode large-batch` | **PASS 100/100** | 130,189 | 0 | 273 | 186 s |
| `--iterations 100 --seed 7 --mode compaction` | **PASS 100/100** | 43,952 | 0 | 295 | 79 s |
| `--iterations 100 --seed 42 --mode recovery-crash` | **PASS 100/100** (125 of 206 kills landed during recovery) | 1,383 | 0 | 270 | 267 s |
| `--iterations 100 --seed 11 --mode disk-full` | **PASS 100/100** | 13,528 | 6,821 `full` | 292 | 63 s |
| `--iterations 100 --seed 21 --mode concurrent-disjoint` | **PASS 100/100** | 64,373 | 0 | 178 | 33 s |
| `--iterations 100 --seed 22 --mode same-key` | **PASS 100/100** | 49,410 | 0 | 185 | 36 s |

"Unknown" means the process was killed while the write was in flight. The
oracle accepted each such write only as fully present or fully absent.
Not run: `go test -race` (no C compiler), power-cut tests.

## Team and credits

| Part | Who | Where | Status |
|---|---|---|---|
| Engine, CLI, torture tools (`torture`; Torture Engine 2.0 `nexus-torture`), scenarios, docs | Neil Malvalli (with Claude Code; every prompt in [PROMPTS.md](PROMPTS.md)) | this repository | complete, measured |
| Independent recovery/compaction implementation of the same format | GitHub `sharanyashetty013-droid` | [kipdb-recovery](https://github.com/sharanyashetty013-droid/kipdb-recovery) @ `f0d8b9c` | 30/30 of its tests pass on the judging machine; used by `crosscheck/` as an independent verifier; not part of NEXUS |
| Rust crash-torture harness with an independent linearization oracle | Rohith A Kumar (`rohithakumar`) | [Torture-Tool-Two-Pointer-](https://github.com/rohithakumar/Torture-Tool-Two-Pointer-) @ `30941e9` | separate project; not part of NEXUS and not used for any result in this repository |

## Limitations

* **No power-loss guarantee on Windows.** Durability is guaranteed against
  process death (the official crash model). Windows has no directory `fsync`.
  Renames use `MOVEFILE_WRITE_THROUGH`, but we have not tested power cuts, so
  we do not claim that a completed compaction rename survives one. Data
  frames are flushed with `FlushFileBuffers`.
* **Bit rot** in the middle of the file is detected (CRC) and reported as
  corruption, not repaired. Damage just behind a damaged header at the very end
  is indistinguishable from a torn write and is truncated (see BREAKING.md).
* The whole index lives in memory (about 200 MB of Go heap for 1,000,000 short
  keys).
* `--max-bytes` is per run, not stored in the file.
* One process at a time per database (the lock); writers inside a process are
  serialized by one lock. `check --no-repair` also needs the lock, so it
  reports `LOCKED` while a `serve` process is running.
* Keys with spaces cannot be written through `serve` or batch files (the API
  accepts any bytes).
* Real kills almost never land between two `WriteFile` calls of one frame, so
  torture runs show `torn-tails=0`. Torn frames are proven instead by
  replaying every prefix of real frames (`nexus-torture scenarios`, A2, A7,
  B1, B2, B6) and by the fault-injection scenarios (BREAKING.md §3).
* Torture Engine 2.0 limits: a kill from outside cannot be aimed at an exact
  instruction, so exact crash points are covered by crash-state replay
  rather than by a kill at that instant; the oracle may answer INCONCLUSIVE
  on adversarial histories (it never turns that into a pass); everything ran
  on one Windows machine ([docs/TORTURE_RESULTS.md](docs/TORTURE_RESULTS.md)).
