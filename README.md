# NEXUS — Kill It and Prove It

A crash-safe embedded key-value database written from scratch in Go (standard
library only), plus the tool that tries to break it.

* **Engine:** an append-only log of checksummed frames plus an in-memory hash
  index. One frame per write; `fsync` before a write is acknowledged.
* **CLI:** `kvdb` with the eight required commands (`open put get del batch
  dump check serve`).
* **Torture tool:** `torture` starts `kvdb serve`, writes from several
  concurrent writers, kills the process at a random millisecond
  (`TerminateProcess` on Windows), recovers, and checks the result against its
  own oracle.
* **Proof:** the 32 official crash scenarios run as Go tests in
  [`src/scenarios`](src/scenarios). Results:
  [docs/results/SCENARIO_RESULTS.md](docs/results/SCENARIO_RESULTS.md).

Other documents: [ARCHITECTURE.md](ARCHITECTURE.md) (diagram, proof table,
three guarantees), [BREAKING.md](BREAKING.md) (attacks on the design),
[PROMPTS.md](PROMPTS.md) (every AI prompt), [docs/TEST_PLAN.md](docs/TEST_PLAN.md)
(requirement → test map), [docs/DESIGN.md](docs/DESIGN.md) (full design and
change log).

## Build (one command)

Requires only the Go toolchain (1.22 or newer; developed with 1.27.3) on 64-bit
Windows (the primary target), Linux, macOS or FreeBSD. From the repository root:

```bash
go build -o . ./src/cmd/kvdb ./torture
```

This produces `kvdb.exe` and `torture.exe` (no `.exe` suffix on other systems)
in the repository root. There are no dependencies to download.

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
| `kvdb serve <path> [--max-bytes N]` | reads requests on stdin; prints `ACK <op-id>` after each durable write | 0 at end of input |

Example session:

```bash
kvdb open demo.db
kvdb put demo.db apple "red fruit"
kvdb get demo.db apple
kvdb del demo.db apple
kvdb batch demo.db ops.txt
kvdb dump demo.db
kvdb check demo.db
```

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
* `check` opens the database (running the same recovery as `open`, so a torn,
  never-acknowledged tail is cut off and reported on stderr), then re-reads the
  **whole** file: superblock, every frame header CRC, every payload CRC, every
  entry CRC, sequence numbers, no torn tail left, the index rebuilt from disk
  equal to the in-memory index, and every live value readable. It never
  repairs corruption; it reports it.
* Exit status: 0 success, 1 failed with nothing changed, 2 usage error, 3
  outcome unknown (the write may or may not be durable; never printed as OK).

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

## Torture tool

```bash
torture --iterations 500 --seed 12345
torture --iterations 100 --seed 999 --mode large-values
torture --iterations 100 --seed 7 --mode compaction
torture --iterations 100 --seed 42 --mode recovery-crash
```

`--seed` is mandatory. Modes: `default`, `large-values` (1 MiB values),
`large-batch` (10,000-op batches racing single writes), `compaction`,
`recovery-crash` (kills `kvdb open` while it recovers a 200,000-key database),
`disk-full` (`--max-bytes 262144`), `concurrent-disjoint`, `same-key`. Each
iteration prints one line; the run ends with a `RESULT: PASS|FAIL` line and
writes a JSON report and its oracle log (`oracle.jsonl`) under
`torture-runs/<mode>-seed<seed>/`. On the first failure it stops and prints
the expected and found state with the operation IDs involved. How it works:
[ARCHITECTURE.md § Torture tool](ARCHITECTURE.md#torture-tool).

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
* `snapshot_end` marks the end of the frames written by compaction. A fresh
  database has `snapshot_end = 32`. Compaction writes the superblock **last**,
  so a temporary file with a valid superblock is complete.
* Files next to the database: `<path>.lock` (process lock, never deleted),
  and during compaction or creation `<path>.compact`, `<path>.old`,
  `<path>.new`.

Byte-level detail and decode order: [docs/FILE_FORMAT.md](docs/FILE_FORMAT.md).

## Recovery algorithm in plain English

When a database is opened (by any command):

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
only moves complete files with non-overwriting moves, and step 4 truncates to
an offset that depends only on the bytes before it.

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

Heavier runs (results are in [docs/results](docs/results)):

```bash
KVDB_SCENARIOS=full KVDB_SCENARIO_REPORT=docs/results/SCENARIO_RESULTS.md go test ./src/scenarios -v -timeout 90m
KVDB_ACCEPTANCE=1 go test ./src/scenarios -run TestAcceptanceMillionPutDelete -v -timeout 90m
KVDB_PERF=1 go test ./src/internal/recovery -run TestOpenMillionKeysTiming -v
```

(In PowerShell set the variables with `$env:KVDB_SCENARIOS="full"` first.)
`go test -race` needs a C compiler, which this machine does not have; it was
not run.

## Limitations

* **No power-loss guarantee on Windows.** Durability is guaranteed against
  process death (the official crash model). Windows has no directory `fsync`,
  so after a power cut a completed compaction rename might not survive; data
  frames are flushed with `FlushFileBuffers`.
* **Bit rot** in the middle of the file is detected (CRC) and reported as
  corruption, not repaired. Damage just behind a damaged header at the very end
  is indistinguishable from a torn write and is truncated (see BREAKING.md).
* The whole index lives in memory (about 200 MB of Go heap for 1,000,000 short
  keys).
* `--max-bytes` is per run, not stored in the file.
* One process at a time per database (the lock); writers inside a process are
  serialized by one lock.
* Keys with spaces cannot be written through `serve` or batch files (the API
  accepts any bytes).
